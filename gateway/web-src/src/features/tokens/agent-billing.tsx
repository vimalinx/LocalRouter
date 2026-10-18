import { useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { AgentBillingEntry, AgentBillingPage, AgentBudget } from '@/lib/types'

export function usdInputMicros(value: string) {
  if (!value.trim()) return 0
  if (!/^\d+(\.\d{1,6})?$/.test(value.trim())) throw new Error('金额须为非负美元数，最多 6 位小数')
  const micros = Math.round(Number(value) * 1_000_000)
  if (!Number.isSafeInteger(micros) || micros > 1_000_000_000_000) throw new Error('金额不能超过 $1,000,000')
  return micros
}

export function microUSD(value: number) {
  return `$${(value / 1_000_000).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 6 })}`
}

export function BudgetSummary({ budget }: { budget?: AgentBudget }) {
  if (!budget || (!budget.daily_limit_usd_micros && !budget.monthly_limit_usd_micros)) return null
  return <div className='space-y-1 text-xs' aria-label='金额预算'>
    {budget.daily_limit_usd_micros > 0 ? <p>日预算：{microUSD(budget.daily_spent_usd_micros)} / {microUSD(budget.daily_limit_usd_micros)} · 可用 {microUSD(budget.daily_remaining_usd_micros || 0)}</p> : null}
    {budget.monthly_limit_usd_micros > 0 ? <p>月预算：{microUSD(budget.monthly_spent_usd_micros)} / {microUSD(budget.monthly_limit_usd_micros)} · 可用 {microUSD(budget.monthly_remaining_usd_micros || 0)}</p> : null}
    {budget.held_usd_micros > 0 ? <p>预留 {microUSD(budget.held_usd_micros)} · {budget.pending_requests} 笔进行中或待核对</p> : null}
    {budget.unreconciled_overruns > 0 ? <p className='text-destructive'>费用超出单次预留，已暂停新请求，请核对。</p> : null}
    {budget.unpriced_requests > 0 ? <p className='text-amber-600'>本月有 {budget.unpriced_requests} 笔历史调用缺少费用，未计入已知金额。</p> : null}
  </div>
}

const states: Record<string, string> = { reserved: '进行中', unknown: '费用待核对', settled: '已结算', overrun: '超出预留' }

export function AgentBillingDialog(props: { agent: { id: number; name: string } | null; adminToken: string; onClose: () => void; onChanged: () => Promise<unknown> }) {
  const [page, setPage] = useState(1)
  const [target, setTarget] = useState<AgentBillingEntry | null>(null)
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const id = props.agent?.id
  const result = useQuery({ queryKey: ['agent-billing', id, page], enabled: id !== undefined,
    queryFn: ({ signal }) => adminRequest<AgentBillingPage>(`/local/api/agent-billing/${id}?page=${page}&page_size=20`, props.adminToken, { signal }), retry: false })
  async function reconcile(event: FormEvent) {
    event.preventDefault()
    if (!target || id === undefined) return
    setSaving(true)
    try {
      await adminRequest(`/local/api/agent-billing/${id}/${target.id}/reconcile`, props.adminToken, { method: 'POST', body: JSON.stringify({ amount_usd_micros: usdInputMicros(amount), reason: reason.trim() }) })
      setTarget(null)
      await Promise.all([result.refetch(), props.onChanged()])
      toast.success('已按核实费用结算，历史记录保留')
    } catch (error) { toast.error(error instanceof Error ? error.message : '核对保存失败') }
    finally { setSaving(false) }
  }
  return <Dialog open={Boolean(props.agent)} onOpenChange={open => { if (!open) { props.onClose(); setTarget(null); setPage(1) } }}>
    <DialogContent className='sm:max-w-4xl'>
      <DialogHeader><DialogTitle>消费明细 · {props.agent?.name}</DialogTitle><DialogDescription>按 Agent 保存模型、渠道或服务、Token 和费用。日/月预算按 UTC 重置；未确认费用不会视为免费。</DialogDescription></DialogHeader>
      <BudgetSummary budget={result.data?.budget} />
      {result.isPending ? <p role='status'>正在读取账单…</p> : result.error ? <p role='alert'>账单读取失败：{result.error.message}</p> : <>
        <div className='max-h-[50svh] overflow-auto'>
          <table className='w-full text-left text-xs'><caption className='sr-only'>Agent 消费记录</caption><thead><tr className='border-b'><th className='p-2'>时间 / 模型</th><th className='p-2'>渠道或服务 / 操作</th><th className='p-2'>Token</th><th className='p-2'>费用 / 预留</th><th className='p-2'>状态</th></tr></thead>
            <tbody>{result.data?.items.map(entry => <tr key={entry.id} className='border-b align-top'>
              <td className='p-2'>{formatTimestamp(entry.created_at)}<p className='break-all font-mono'>{entry.model || '—'}</p></td>
              <td className='max-w-64 break-all p-2'>{entry.service}<p className='text-muted-foreground'>{entry.operation}</p></td>
              <td className='p-2 tabular-nums'>{entry.usage.total_tokens || (entry.usage.input_tokens || 0) + (entry.usage.output_tokens || 0)}</td>
              <td className='p-2 tabular-nums'>{entry.state === 'settled' || entry.state === 'overrun' ? microUSD(entry.charged_usd_micros) : '待核对'}{entry.reserved_usd_micros > 0 ? <p className='text-muted-foreground'>预留 {microUSD(entry.reserved_usd_micros)}</p> : null}</td>
              <td className='p-2'>{states[entry.state] || entry.state}{entry.source === 'operator-reconciled' ? <p title={entry.reason}>人工核对：{entry.reason}</p> : null}{entry.state === 'unknown' || entry.state === 'overrun' ? <Button size='sm' variant='outline' className='mt-1' aria-label={`核对账单 ${entry.id}`} onClick={() => { setTarget(entry); setAmount(entry.state === 'overrun' ? String(entry.charged_usd_micros / 1_000_000) : ''); setReason('') }}>核对费用</Button> : null}</td>
            </tr>)}</tbody></table>
          {!result.data?.items.length ? <p className='p-4 text-muted-foreground'>暂无消费记录。</p> : null}
        </div>
        <nav className='flex items-center justify-end gap-3 text-sm' aria-label='账单分页'><span>第 {page} 页 · 共 {result.data?.total || 0} 条</span><Button variant='outline' disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</Button><Button variant='outline' disabled={page * 20 >= (result.data?.total || 0)} onClick={() => setPage(page + 1)}>下一页</Button></nav>
      </>}
      {target ? <form onSubmit={reconcile} className='grid gap-3 border-t pt-3 sm:grid-cols-2'>
        <p className='text-xs sm:col-span-2'>请依据上游账单填写最终金额；确认未收费时可填 0。保存后按该金额计入预算，释放原预留。</p>
        <div><Label htmlFor='reconcile-amount'>核实金额（USD）</Label><Input id='reconcile-amount' type='number' min='0' max='1000000' step='0.000001' required value={amount} onChange={event => setAmount(event.target.value)} /></div>
        <div><Label htmlFor='reconcile-reason'>核对依据</Label><Input id='reconcile-reason' minLength={3} maxLength={512} required value={reason} onChange={event => setReason(event.target.value)} placeholder='例如：上游账单编号及核对结果' /></div>
        <div className='flex gap-2 sm:col-span-2'><Button type='submit' disabled={saving}>确认结算</Button><Button type='button' variant='outline' onClick={() => setTarget(null)}>取消核对</Button></div>
      </form> : null}
    </DialogContent>
  </Dialog>
}

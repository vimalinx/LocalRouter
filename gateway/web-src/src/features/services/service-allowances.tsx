import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { adminRequest } from '@/lib/api'

type Rule = {
  enabled: boolean; mode: 'quota' | 'approval' | 'deny'; unit: 'requests' | 'usd_micros'
  period: 'once' | 'day' | 'month'; limit: number; revision: number
  approval_operations: string[]; denied_operations: string[]
}
type Receipt = { id: string; operation: string; token_id: number; amount: number }
type Allowance = { service: string; name: string; rule: Rule; spent: number; reserved: number; remaining: number; pending: Receipt[] }
const selectClass = 'h-10 w-full rounded-md border bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const splitOperations = (text: string) => text.split(/[\n,]/).map(value => value.trim()).filter(Boolean)
const units = (amount: number, rule: Rule) => rule.unit === 'usd_micros' ? `$${(amount / 1e6).toFixed(6)}` : `${amount} 次`

export function ServiceAllowances({ adminToken }: { adminToken: string }) {
  const [items, setItems] = useState<Allowance[]>([])
  const [selected, setSelected] = useState('')
  const [draft, setDraft] = useState<Rule | null>(null)
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [tokenID, setTokenID] = useState('')
  const [operation, setOperation] = useState('')
  const [receiptID, setReceiptID] = useState('')
  const [actual, setActual] = useState('')
  const [reason, setReason] = useState('')
  const [limitText, setLimitText] = useState('0')
  const [approvalText, setApprovalText] = useState('')
  const [deniedText, setDeniedText] = useState('')
  const current = items.find(item => item.service === selected)
  useEffect(() => { setApprovalText((draft?.approval_operations || []).join(', ')); setDeniedText((draft?.denied_operations || []).join(', ')) }, [selected, draft?.revision])
  useEffect(() => { setLimitText(String(draft ? draft.limit / (draft.unit === 'requests' ? 1 : 1e6) : 0)) }, [selected, draft?.revision, draft?.unit])
  async function load(selection = selected) {
    const result = await adminRequest<Allowance[]>('/local/api/service-allowances', adminToken)
    setItems(result)
    const item = result.find(value => value.service === selection) || result[0]
    setSelected(item?.service || '')
    setDraft(item ? { ...item.rule } : null)
  }
  useEffect(() => {
    let live = true
    adminRequest<Allowance[]>('/local/api/service-allowances', adminToken).then(result => {
      if (!live) return
      setItems(result); setSelected(result[0]?.service || ''); setDraft(result[0]?.rule || null)
    }).catch(err => { if (live) setError(String(err)) }).finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [adminToken])
  async function mutate(path: string, method: string, body: unknown, success: string) {
    setBusy(true); setError(''); setMessage('')
    try {
      await adminRequest(path, adminToken, { method, body: JSON.stringify(body) })
      await load(); setMessage(success)
    } catch (err) { setError(err instanceof Error ? err.message : String(err)) }
    finally { setBusy(false) }
  }
  const base = `/local/api/service-allowances/${encodeURIComponent(selected)}`
  if (loading) return <p role='status' className='p-4 text-sm'>正在读取服务额度…</p>
  return <section className='mx-auto max-w-3xl space-y-5 p-4'>
    <div><h2 className='text-base font-semibold'>服务自主使用额度</h2><p className='mt-1 text-sm text-muted-foreground'>默认关闭。只有人保存并启用后，已登记的 Agent 才能在共享额度内免逐次确认；原有访问权限仍然有效。</p></div>
    {error && <p role='alert' className='text-sm text-destructive'>{error}</p>}
    {message && <p role='status' className='text-sm'>{message}</p>}
    {!items.length ? <p className='text-sm text-muted-foreground'>暂无服务。</p> : <>
      <label className='block space-y-1 text-sm'><span>服务</span><select className={selectClass} value={selected} disabled={busy} onChange={event => {
        const item = items.find(value => value.service === event.target.value)
        setSelected(event.target.value); setDraft(item ? { ...item.rule } : null); setMessage(''); setError('');setReceiptID('');setOperation('');setTokenID('')
      }}>{items.map(item => <option key={item.service} value={item.service}>{item.name} · {item.rule.enabled ? '已启用' : '未启用'}</option>)}</select></label>
      {draft && current && <>
        <form className='space-y-4' onSubmit={event => { event.preventDefault(); void mutate(base, 'PUT', { ...draft, limit: Math.round(Number(limitText) * (draft.unit === 'requests' ? 1 : 1e6)), approval_operations: splitOperations(approvalText), denied_operations: splitOperations(deniedText) }, draft.enabled ? '规则已保存并启用。' : '规则已保存，额度功能关闭。') }}>
          <fieldset disabled={busy} className='space-y-4'>
            <label className='flex min-h-11 items-center gap-2 text-sm'><input type='checkbox' checked={draft.enabled} onChange={event => setDraft({ ...draft, enabled: event.target.checked })} />启用此服务的自主使用限制</label>
            <label className='block space-y-1 text-sm'><span>使用方式</span><select className={selectClass} value={draft.mode} onChange={event => setDraft({ ...draft, mode: event.target.value as Rule['mode'] })}><option value='quota'>额度内自主使用</option><option value='approval'>始终需要批准</option><option value='deny'>禁止使用</option></select></label>
            {draft.mode === 'quota' && <>
              <div className='grid gap-3 sm:grid-cols-3'>
                <label className='block space-y-1 text-sm'><span>计量单位</span><select className={selectClass} value={draft.unit} onChange={event => setDraft({ ...draft, unit: event.target.value as Rule['unit'], limit: 0 })}><option value='requests'>调用次数</option><option value='usd_micros'>美元</option></select></label>
                <label className='block space-y-1 text-sm'><span>总额度</span><Input type='number' required min='0' max={draft.unit === 'requests' ? 1e12 : 1e6} step={draft.unit === 'requests' ? 1 : 0.000001} value={limitText} onChange={event => setLimitText(event.target.value)} /></label>
                <label className='block space-y-1 text-sm'><span>额度周期</span><select className={selectClass} value={draft.period} onChange={event => setDraft({ ...draft, period: event.target.value as Rule['period'] })}><option value='once'>一次性</option><option value='day'>每天（UTC）</option><option value='month'>每月（UTC）</option></select></label>
              </div>
              <p className='text-sm text-muted-foreground'>多个 Agent 合计扣减。次数按获准调用计，不因失败退回。美元仅支持已确认的固定调用价格；变动或未知费用需单独批准。已有用量后不能更换单位和周期。</p>
              <p className='text-sm'>当前已用 {units(current.spent, current.rule)} · 待核对 {units(current.reserved, current.rule)} · 剩余 {units(current.remaining, current.rule)}</p>
            </>}
            <label className='block space-y-1 text-sm'><span>始终需要批准的操作</span><Input value={approvalText} onChange={event => setApprovalText(event.target.value)} /></label>
            <label className='block space-y-1 text-sm'><span>禁止的操作</span><Input value={deniedText} onChange={event => setDeniedText(event.target.value)} /></label>
            <p className='text-xs text-muted-foreground'>填写操作 ID，以逗号分隔；* 表示全部。兼容渠道填写请求方法和路径，例如 POST /v1/chat/completions。</p>
            <Button type='submit'>{busy ? '保存中…' : '保存配置'}</Button>
          </fieldset>
        </form>
        {current.rule.enabled && current.rule.mode !== 'deny' && <form className='space-y-3 border-t pt-4' onSubmit={event => { event.preventDefault(); void mutate(`${base}/grants`, 'POST', { token_id: Number(tokenID), operation }, '已批准该 Agent 调用此操作一次，一小时内有效。') }}>
          <h3 className='text-sm font-semibold'>批准一次调用</h3>
          <p className='text-sm text-muted-foreground'>授权所填 Agent 在一小时内调用指定操作一次，不增加共享额度，也不解除禁止规则。此批准覆盖该操作的参数和实际费用，请确认使用范围。</p>
          <div className='grid gap-3 sm:grid-cols-2'>
            <label className='block space-y-1 text-sm'><span>Agent Token ID</span><Input type='number' min='1' required value={tokenID} onChange={event => setTokenID(event.target.value)} /></label>
            <label className='block space-y-1 text-sm'><span>批准的操作 ID</span><Input required value={operation} onChange={event => setOperation(event.target.value)} /></label>
          </div><Button type='submit' disabled={busy}>批准一次</Button>
        </form>}
        {current.pending.length > 0 && <form className='space-y-3 border-t pt-4' onSubmit={event => { event.preventDefault(); void mutate(`${base}/receipts/${encodeURIComponent(receiptID)}/reconcile`, 'POST', { amount: Math.round(Number(actual) * 1e6), reason }, '核对结果已保存。') }}>
          <h3 className='text-sm font-semibold'>核对未结算费用</h3>
          <p className='text-sm text-muted-foreground'>超时或结果不明的占用会保留。请先核对供应商记录，再填写实际费用；确认未收费才填 0。</p>
          <label className='block space-y-1 text-sm'><span>待核对调用</span><select required className={selectClass} value={receiptID} onChange={event => setReceiptID(event.target.value)}><option value=''>选择调用</option>{current.pending.map(receipt => <option key={receipt.id} value={receipt.id}>{receipt.id} · {receipt.operation} · Agent {receipt.token_id} · {units(receipt.amount, current.rule)}</option>)}</select></label>
          <label className='block space-y-1 text-sm'><span>确认实际费用（美元）</span><Input required type='number' min='0' max='1000000' step='0.000001' value={actual} onChange={event => setActual(event.target.value)} /></label>
          <label className='block space-y-1 text-sm'><span>核对依据</span><Input required value={reason} onChange={event => setReason(event.target.value)} /></label>
          <Button type='submit' disabled={busy}>保存核对结果</Button>
        </form>}
      </>}
    </>}
  </section>
}

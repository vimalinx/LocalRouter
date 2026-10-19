import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { microUSD, usdInputMicros } from './agent-billing'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { AgentBudgetRequest, AgentBudgetSettings } from '@/lib/types'

const dollars = (micros?: number) => micros ? String(micros / 1_000_000) : ''

export function BudgetRequests({ adminToken, onChanged }: { adminToken: string; onChanged: () => Promise<unknown> }) {
  const [requests, setRequests] = useState<AgentBudgetRequest[]>([])
  const [settings, setSettings] = useState<AgentBudgetSettings | null>(null)
  const [amounts, setAmounts] = useState<Record<string, { monthly: string; daily: string }>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const reload = useCallback(async () => {
    setBusy(true)
    try {
      const data = await adminRequest<{ items: AgentBudgetRequest[]; settings: AgentBudgetSettings }>('/local/api/budget-requests', adminToken)
      setRequests(data.items); setSettings(data.settings); setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '预算申请读取失败') }
    finally { setBusy(false) }
  }, [adminToken])
  useEffect(() => { void reload() }, [reload])

  async function decide(request: AgentBudgetRequest, approve: boolean) {
    setBusy(true); setError('')
    try {
      const edited = amounts[request.id]
      const body: Record<string, unknown> = { approve }
      if (approve && request.kind === 'formal' && edited) {
        body.monthly_usd_micros = usdInputMicros(edited.monthly)
        body.daily_usd_micros = usdInputMicros(edited.daily)
      }
      await adminRequest(`/local/api/budget-requests/${request.id}/decision`, adminToken, { method: 'POST', body: JSON.stringify(body) })
      await reload(); await onChanged()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '处理失败，请刷新查看状态') }
    finally { setBusy(false) }
  }

  const pending = requests.filter((request) => request.state === 'pending')
  return <section className='space-y-3 border-y py-3' aria-labelledby='budget-requests-title'>
    <div className='flex flex-wrap items-center justify-between gap-2'>
      <div><h2 id='budget-requests-title' className='text-sm font-semibold'>预算申请 <span className='text-muted-foreground'>{pending.length}</span></h2>
        <p className='mt-1 text-xs text-muted-foreground'>新 Agent 自动获得试用额度；用完后由 Agent 估算金额并说明理由，你批准一次即可。金额可在批准前修改。</p></div>
      <Button variant='ghost' size='sm' disabled={busy} onClick={reload}><RefreshCw aria-hidden='true' />刷新</Button>
    </div>
    {error ? <p role='alert' className='text-sm text-destructive'>{error}</p> : null}
    {pending.map((request) => {
      const edited = amounts[request.id] || { monthly: dollars(request.monthly_usd_micros), daily: dollars(request.daily_usd_micros) }
      const update = (key: 'monthly' | 'daily', value: string) => setAmounts((current) => ({ ...current, [request.id]: { ...edited, [key]: value } }))
      return <article key={request.id} className='space-y-2 border-t pt-3'>
        <div className='flex flex-wrap items-center gap-2'><strong className='text-sm'>{request.agent_name}</strong><Badge variant='outline'>{request.kind === 'formal' ? '转为正式预算' : '再给一轮试用'}</Badge><span className='text-xs text-muted-foreground'>{request.agent_code} · 试用已花 {microUSD(request.trial_spent_usd_micros)} · {formatTimestamp(request.created_at)}</span></div>
        <p className='break-words text-sm'>{request.reason}</p>
        {request.kind === 'formal' ? <div className='grid max-w-md gap-2 sm:grid-cols-2'>
          <div className='grid gap-1.5'><Label htmlFor={`${request.id}-monthly`}>每月预算（USD）</Label><Input id={`${request.id}-monthly`} type='number' min='0' step='0.01' value={edited.monthly} onChange={(event) => update('monthly', event.target.value)} placeholder='不限' /></div>
          <div className='grid gap-1.5'><Label htmlFor={`${request.id}-daily`}>每日预算（USD）</Label><Input id={`${request.id}-daily`} type='number' min='0' step='0.01' value={edited.daily} onChange={(event) => update('daily', event.target.value)} placeholder='不限' /></div>
        </div> : <p className='text-xs text-muted-foreground'>批准后追加 {microUSD(settings?.trial_per_agent_usd_micros || 0)} 试用额度，仍受每月试用总额约束。</p>}
        <div className='flex flex-wrap gap-2'><Button size='sm' disabled={busy} onClick={() => decide(request, true)}>批准</Button><Button size='sm' variant='outline' disabled={busy} onClick={() => decide(request, false)}>拒绝</Button></div>
      </article>
    })}
    {settings ? <TrialSettings key={settings.revision} adminToken={adminToken} settings={settings} onSaved={reload} /> : null}
  </section>
}

function TrialSettings({ adminToken, settings, onSaved }: { adminToken: string; settings: AgentBudgetSettings; onSaved: () => Promise<void> }) {
  const [auto, setAuto] = useState(settings.auto_approve_identities)
  const [autoServices, setAutoServices] = useState(settings.auto_apply_services)
  const [perAgent, setPerAgent] = useState(dollars(settings.trial_per_agent_usd_micros))
  const [pool, setPool] = useState(dollars(settings.trial_pool_monthly_usd_micros))
  const [reserve, setReserve] = useState(dollars(settings.trial_request_reserve_usd_micros))
  const [saving, setSaving] = useState(false)
  async function save(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    try {
      await adminRequest('/local/api/agent-budget-settings', adminToken, { method: 'PUT', body: JSON.stringify({
        auto_approve_identities: auto, auto_apply_services: autoServices, trial_per_agent_usd_micros: usdInputMicros(perAgent),
        trial_pool_monthly_usd_micros: usdInputMicros(pool), trial_request_reserve_usd_micros: usdInputMicros(reserve), revision: settings.revision,
      }) })
      toast.success('试用设置已保存，只影响之后发放的额度')
      await onSaved()
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : '试用设置保存失败') }
    finally { setSaving(false) }
  }
  return <details className='group border-t pt-3'>
    <summary className='cursor-pointer text-sm font-medium'>试用设置 <span className='text-xs font-normal text-muted-foreground'>每个 Agent {microUSD(settings.trial_per_agent_usd_micros)} · 每月合计 {microUSD(settings.trial_pool_monthly_usd_micros)}</span></summary>
    <form onSubmit={save} className='mt-3 grid max-w-2xl gap-3 sm:grid-cols-3'>
      <label className='flex items-center gap-2 text-sm sm:col-span-3'><Checkbox checked={auto} onChange={(event) => setAuto(event.target.checked)} />自动批准 Agent 身份申请（严格模式开启时始终需要人工审批）</label>
      <label className='flex items-center gap-2 text-sm sm:col-span-3'><Checkbox checked={autoServices} onChange={(event) => setAutoServices(event.target.checked)} />自动接入 Agent 提供了密钥和价格的新公网 HTTPS 服务（可随时撤销）</label>
      <div className='grid gap-1.5'><Label htmlFor='trial-per-agent'>每个 Agent 试用（USD）</Label><Input id='trial-per-agent' type='number' min='0' step='0.01' required value={perAgent} onChange={(event) => setPerAgent(event.target.value)} /></div>
      <div className='grid gap-1.5'><Label htmlFor='trial-pool'>每月试用合计（USD）</Label><Input id='trial-pool' type='number' min='0' step='0.01' required value={pool} onChange={(event) => setPool(event.target.value)} /></div>
      <div className='grid gap-1.5'><Label htmlFor='trial-reserve'>单次默认预留（USD）</Label><Input id='trial-reserve' type='number' min='0' step='0.001' required value={reserve} onChange={(event) => setReserve(event.target.value)} /></div>
      <p className='text-xs text-muted-foreground sm:col-span-3'>价格不固定的调用先按单次预留占用，调用后按真实费用结算；最后一笔可能略超试用额度。</p>
      <div className='sm:col-span-3'><Button type='submit' size='sm' disabled={saving}>保存试用设置</Button></div>
    </form>
  </details>
}

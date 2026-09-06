import { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { TokenPolicy } from '@/lib/types'

type IdentityRequest = {
  id: string; agent_code: string; agent_name: string; workspace: string; runtime: string
  digest: string; state: string; expires_at: number; policy: TokenPolicy; owner_token_id?: number
}

export function IdentityRequests({ adminToken, onChanged }: { adminToken: string; onChanged: () => Promise<unknown> }) {
  const [requests, setRequests] = useState<IdentityRequest[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const reload = useCallback(async () => {
    setBusy(true)
    try { setRequests(await adminRequest<IdentityRequest[]>('/local/api/identity-requests', adminToken)); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '申请读取失败') }
    finally { setBusy(false) }
  }, [adminToken])
  useEffect(() => { void reload() }, [reload])
  async function decide(request: IdentityRequest, approve: boolean) {
    setBusy(true); setError('')
    try {
      await adminRequest(`/local/api/identity-requests/${request.id}/decision`, adminToken, {
        method: 'POST', body: JSON.stringify({ digest: request.digest, approve }),
      })
      await reload(); await onChanged()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '申请处理失败，请刷新查看状态') }
    finally { setBusy(false) }
  }
  const pending = requests.filter((request) => request.state === 'pending')
  return <section className='border-y py-3' aria-labelledby='identity-requests-title'>
    <div className='flex flex-wrap items-center justify-between gap-2'>
      <div><h2 id='identity-requests-title' className='text-sm font-semibold'>接入申请 <span className='text-muted-foreground'>{pending.length}</span></h2>
        <p className='mt-1 text-xs text-muted-foreground'>批准后自动交付凭据，范围内自主调用。不同 Agent 分别授权，可随时撤销。</p></div>
      <Button variant='ghost' size='sm' disabled={busy} onClick={reload}><RefreshCw aria-hidden='true' />刷新申请</Button>
    </div>
    {error ? <p role='alert' className='mt-3 text-sm text-destructive'>{error}</p> : null}
    {pending.map((request) => <article key={request.id} className='mt-3 space-y-3 border-t pt-3'>
      <div className='flex flex-wrap items-center gap-2'><strong className='text-sm'>{request.agent_name}</strong><Badge variant='outline'>{request.owner_token_id ? '权限调整' : '新身份接入'}</Badge><span className='break-all text-xs text-muted-foreground'>{request.agent_code} · {request.runtime}</span></div>
      <p className='break-all text-xs'>{request.workspace}</p>
      <dl className='grid gap-2 text-xs sm:grid-cols-[5rem_1fr]'>
        {request.owner_token_id ? <><dt className='text-muted-foreground'>调整方式</dt><dd>以下为批准后的完整范围，替换当前 Token 策略；已有用量不清零，能力包和共享额度仍然生效。</dd></> : null}
        <dt className='text-muted-foreground'>可用服务</dt><dd className='break-words'>{request.policy.packs?.join('、')}</dd>
        <dt className='text-muted-foreground'>可用操作</dt><dd className='flex flex-wrap gap-1'>{request.policy.operations?.map((operation) => <code className='break-all rounded bg-muted px-1.5 py-0.5' key={operation}>{operation}</code>)}</dd>
        <dt className='text-muted-foreground'>模型范围</dt><dd>{request.policy.models?.length ? request.policy.models.join('、') : '所选操作的全部模型'}</dd>
        <dt className='text-muted-foreground'>请求限制</dt><dd>每日 {request.policy.daily_request_limit || '不限'} · 每分钟 {request.policy.requests_per_minute || '不限'} · 并发 {request.policy.max_in_flight || '不限'}；另受服务共享额度约束</dd>
        <dt className='text-muted-foreground'>授权有效期</dt><dd>{request.policy.expires_at ? formatTimestamp(request.policy.expires_at) : '长期有效，直到撤销'}</dd>
      </dl>
      <div className='flex flex-wrap gap-2'><Button size='sm' disabled={busy} onClick={() => decide(request, true)}>批准以上范围</Button><Button size='sm' variant='outline' disabled={busy} onClick={() => decide(request, false)}>拒绝申请</Button></div>
    </article>)}
    {!pending.length && !error ? <p className='mt-3 text-xs text-muted-foreground'>暂无待处理申请。Agent 发起接入后会显示在这里。</p> : null}
    {requests.some((request) => request.state === 'approved') ? <p className='mt-3 text-xs text-muted-foreground'>已批准的 Agent 可继续领取凭据；已签发身份在下方管理，撤销后不能重新领取恢复权限。</p> : null}
  </section>
}

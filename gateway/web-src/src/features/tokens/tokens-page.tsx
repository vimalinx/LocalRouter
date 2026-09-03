import { Bot, Copy, Eye, EyeOff, KeyRound, Plus, RefreshCw, Save, Settings2, ShieldCheck, Trash2 } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { AgentUsage, LocalToken, MaintenanceAccess, TokenPolicy } from '@/lib/types'

type AgentDraft = {
  tokenName: string
  agentCode: string
  agentName: string
  workspace: string
  runtime: string
  requestsPerMinute: string
  dailyRequestLimit: string
  maxInFlight: string
  maintainer: boolean
}

const emptyDraft: AgentDraft = {
  tokenName: '', agentCode: '', agentName: '', workspace: '', runtime: 'codex',
  requestsPerMinute: '', dailyRequestLimit: '', maxInFlight: '', maintainer: false,
}

const compactNumber = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const decimalNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 })

function integerValue(value: string) {
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function formatUSD(value: number) {
  if (!value) return '$0.00'
  return Math.abs(value) < 0.01 ? `$${value.toFixed(4)}` : `$${value.toFixed(2)}`
}

function shellQuote(value: string) {
  return `'${value.replaceAll("'", `'"'"'`)}'`
}

function costLabel(usage?: AgentUsage) {
  if (!usage || usage.cost_status === 'unavailable') return '未接入价格'
  const suffix = usage.cost_status === 'estimated' ? ' 估算' : usage.cost_status === 'partial' ? ' 部分' : ''
  return `${formatUSD(usage.cost_usd)}${suffix}`
}

function quotaLabel(usage?: AgentUsage) {
  if (!usage?.daily_request_limit) return '每日不限量'
  return `今日 ${usage.today_requests}/${usage.daily_request_limit}`
}

export function TokensPage(props: {
  adminToken: string
  tokens: LocalToken[]
  usage: AgentUsage[]
  policies?: TokenPolicy[]
  maintenanceAccess: MaintenanceAccess
  apiTokenFile: string
  onChanged: () => Promise<LocalToken[]>
}) {
  const [visibleTokens, setVisibleTokens] = useState<Record<number, string>>({})
  const [loadingId, setLoadingId] = useState<number | null>(null)
  const [issueOpen, setIssueOpen] = useState(false)
  const [draft, setDraft] = useState<AgentDraft>(emptyDraft)
  const [issuing, setIssuing] = useState(false)
  const [revokeTarget, setRevokeTarget] = useState<LocalToken | null>(null)
  const [policyTarget, setPolicyTarget] = useState<LocalToken | null>(null)
  const [savingPolicy, setSavingPolicy] = useState(false)
  const [savingMaintenanceAccess, setSavingMaintenanceAccess] = useState(false)
  const [maintenanceKey, setMaintenanceKey] = useState('')
  const [maintenanceKeyVisible, setMaintenanceKeyVisible] = useState(false)
  const [savingMaintenanceKey, setSavingMaintenanceKey] = useState(false)
  const usageByToken = new Map(props.usage.map((item) => [item.token_id, item]))
  const registeredTokens = props.tokens.filter((token) => token.name !== 'LocalRouter default' && token.agent_code && token.workspace)
  const maintenanceToken = registeredTokens.find((token) => props.policies?.find((policy) => policy.token_id === token.id)?.capabilities?.includes('localrouter.maintain'))

  async function toggleAgentMaintenance() {
    setSavingMaintenanceAccess(true)
    try {
      if (!props.maintenanceAccess.agent_tokens_enabled && !maintenanceToken) {
        let created = registeredTokens.find((token) => token.agent_code === 'localrouter-maintainer')
        if (!created) {
          await adminRequest('/local/api/tokens', props.adminToken, {
            method: 'POST',
            body: JSON.stringify({ name: 'Agent maintenance', agent_code: 'localrouter-maintainer', agent_name: 'LocalRouter 维护 Agent', workspace: 'operator', runtime: 'maintenance', expired_time: -1 }),
          })
          const tokens = await props.onChanged()
          created = tokens.find((token) => token.agent_code === 'localrouter-maintainer')
        }
        if (!created) throw new Error('维护 Agent 创建后未找到绑定 Token')
        await adminRequest(`/local/api/token-policies/${created.id}`, props.adminToken, {
          method: 'PUT', body: JSON.stringify({ capabilities: ['localrouter.maintain'] }),
        })
      }
      await adminRequest<MaintenanceAccess>('/local/api/maintenance-access', props.adminToken, {
        method: 'PUT', body: JSON.stringify({ agent_tokens_enabled: !props.maintenanceAccess.agent_tokens_enabled }),
      })
      await props.onChanged()
      toast.success(props.maintenanceAccess.agent_tokens_enabled ? 'Agent 维护入口已关闭' : 'Agent 维护入口已开启')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '维护入口设置失败')
    } finally {
      setSavingMaintenanceAccess(false)
    }
  }

  function randomizeMaintenanceKey() {
    const bytes = new Uint8Array(32)
    crypto.getRandomValues(bytes)
    setMaintenanceKey(`sk-${Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')}`)
    setMaintenanceKeyVisible(true)
  }

  async function saveMaintenanceKey() {
    if (!maintenanceToken || maintenanceKey.length < 16) {
      toast.error('维护 Key 至少需要 16 个可打印字符。')
      return
    }
    setSavingMaintenanceKey(true)
    try {
      await adminRequest(`/local/api/tokens/${maintenanceToken.id}/key`, props.adminToken, {
        method: 'PUT', body: JSON.stringify({ key: maintenanceKey }),
      })
      setMaintenanceKey('')
      setMaintenanceKeyVisible(false)
      await props.onChanged()
      toast.success('维护 Key 已轮换；旧 Key 立即失效')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '维护 Key 保存失败')
    } finally {
      setSavingMaintenanceKey(false)
    }
  }

  async function readTokenKey(token: LocalToken) {
    const result = await adminRequest<{ key: string }>(`/local/api/tokens/${token.id}/reveal`, props.adminToken, { method: 'POST' })
    return result.key.startsWith('sk-') ? result.key : `sk-${result.key}`
  }

  async function revealToken(token: LocalToken) {
    if (visibleTokens[token.id]) {
      setVisibleTokens((current) => {
        const next = { ...current }
        delete next[token.id]
        return next
      })
      return
    }
    setLoadingId(token.id)
    try {
      const fullToken = await readTokenKey(token)
      setVisibleTokens((current) => ({ ...current, [token.id]: fullToken }))
      toast.success('令牌只在当前控制台内存中临时显示')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '令牌读取失败')
    } finally {
      setLoadingId(null)
    }
  }

  async function issueAgent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!draft.tokenName.trim() || !draft.agentCode.trim() || !draft.agentName.trim() || !draft.workspace.trim()) {
      toast.error('请填写 Token 名称、Agent 编码、名称和工作区。')
      return
    }
    setIssuing(true)
    try {
      await adminRequest<unknown>('/local/api/tokens', props.adminToken, {
        method: 'POST',
        body: JSON.stringify({
          name: draft.tokenName.trim(), agent_code: draft.agentCode.trim(), agent_name: draft.agentName.trim(),
          workspace: draft.workspace.trim(), runtime: draft.runtime.trim(), expired_time: -1,
        }),
      })
      const updated = await props.onChanged()
      const created = updated.filter((token) => token.agent_code === draft.agentCode.trim()).sort((left, right) => right.id - left.id)[0]
      if (created) {
        await adminRequest(`/local/api/token-policies/${created.id}`, props.adminToken, {
          method: 'PUT',
          body: JSON.stringify({
            capabilities: draft.maintainer ? ['localrouter.maintain'] : [],
            requests_per_minute: integerValue(draft.requestsPerMinute),
            daily_request_limit: integerValue(draft.dailyRequestLimit),
            max_in_flight: integerValue(draft.maxInFlight),
          }),
        })
        const fullToken = await readTokenKey(created)
        setVisibleTokens((current) => ({ ...current, [created.id]: fullToken }))
      }
      setDraft(emptyDraft)
      setIssueOpen(false)
      await props.onChanged()
      toast.success('Agent 已注册，调用身份、工作区和额度策略已绑定')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : 'Agent 注册失败')
    } finally {
      setIssuing(false)
    }
  }

  async function copyConnection(token: LocalToken) {
    setLoadingId(token.id)
    try {
      const fullToken = visibleTokens[token.id] || await readTokenKey(token)
      setVisibleTokens((current) => ({ ...current, [token.id]: fullToken }))
      const configuration = [
        `export OPENAI_BASE_URL=${shellQuote(`${window.location.origin}/v1`)}`,
        `export OPENAI_API_KEY=${shellQuote(fullToken)}`,
        `export LOCALROUTER_AGENT_CODE=${shellQuote(token.agent_code)}`,
        `export LOCALROUTER_WORKSPACE=${shellQuote(token.workspace)}`,
      ].join('\n')
      await navigator.clipboard.writeText(configuration)
      toast.success('快速接入配置已复制；Token 仅在当前页内存中临时显示')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '接入配置复制失败')
    } finally {
      setLoadingId(null)
    }
  }

  async function copyToken(token: LocalToken) {
    const visible = visibleTokens[token.id]
    if (!visible) return
    await navigator.clipboard.writeText(visible)
    toast.success('API Token 已复制')
  }

  async function revokeToken() {
    if (!revokeTarget) return
    try {
      await adminRequest<unknown>(`/local/api/token-policies/${revokeTarget.id}`, props.adminToken, { method: 'DELETE' })
      await adminRequest<unknown>(`/local/api/tokens/${revokeTarget.id}`, props.adminToken, { method: 'DELETE' })
      const revokedId = revokeTarget.id
      setRevokeTarget(null)
      setVisibleTokens((current) => {
        const next = { ...current }
        delete next[revokedId]
        return next
      })
      await props.onChanged()
      toast.success('Agent Token 已撤销')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '令牌撤销失败')
    }
  }

  function openPolicy(token: LocalToken) {
    const policy = props.policies?.find((item) => item.token_id === token.id)
    setDraft({
      tokenName: token.name, agentCode: token.agent_code || '', agentName: token.agent_name || token.name,
      workspace: token.workspace || '', runtime: token.runtime || '',
      requestsPerMinute: policy?.requests_per_minute?.toString() || '',
      dailyRequestLimit: policy?.daily_request_limit?.toString() || '',
      maxInFlight: policy?.max_in_flight?.toString() || '',
      maintainer: policy?.capabilities?.includes('localrouter.maintain') || false,
    })
    setPolicyTarget(token)
  }

  async function savePolicy(event: FormEvent) {
    event.preventDefault()
    if (!policyTarget) return
    setSavingPolicy(true)
    try {
      if (policyTarget.name !== 'LocalRouter default') {
        await adminRequest('/local/api/tokens', props.adminToken, {
          method: 'PUT',
          body: JSON.stringify({
            id: policyTarget.id, status: policyTarget.status, expired_time: -1,
            name: draft.tokenName.trim(), agent_code: draft.agentCode.trim(), agent_name: draft.agentName.trim(),
            workspace: draft.workspace.trim(), runtime: draft.runtime.trim(),
          }),
        })
      }
      await adminRequest(`/local/api/token-policies/${policyTarget.id}`, props.adminToken, {
        method: 'PUT',
        body: JSON.stringify({
          capabilities: draft.maintainer ? ['localrouter.maintain'] : [],
          requests_per_minute: integerValue(draft.requestsPerMinute),
          daily_request_limit: integerValue(draft.dailyRequestLimit),
          max_in_flight: integerValue(draft.maxInFlight),
        }),
      })
      setPolicyTarget(null)
      setDraft(emptyDraft)
      await props.onChanged()
      toast.success('Agent 注册资料与额度策略已更新')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '策略保存失败')
    } finally {
      setSavingPolicy(false)
    }
  }

  return (
    <div className='space-y-5'>
      <SectionHeader
        eyebrow='Identity · Usage · Quota'
        title='Agent 工作台'
        description='每个 Agent 使用唯一编码、工作区与 Token，调用、额度和成本在同一身份下归因。'
        actions={
          <Dialog open={issueOpen} onOpenChange={(open) => { setIssueOpen(open); if (!open) setDraft(emptyDraft) }}>
            <DialogTrigger asChild><Button><Plus aria-hidden='true' />注册 Agent</Button></DialogTrigger>
            <DialogContent className='sm:max-w-2xl'>
              <DialogHeader><DialogTitle>注册 Agent 并签发 Token</DialogTitle><DialogDescription>编码在本机唯一；Token 与身份绑定，额度留空表示不限。</DialogDescription></DialogHeader>
              <AgentForm id='issue-agent-form' draft={draft} onChange={setDraft} onSubmit={issueAgent} />
              <DialogFooter><Button variant='outline' type='button' onClick={() => setIssueOpen(false)}>取消</Button><Button form='issue-agent-form' type='submit' disabled={issuing}>{issuing ? '注册中…' : '注册并签发'}</Button></DialogFooter>
            </DialogContent>
          </Dialog>
        }
      />

      <section className='border-y py-3' aria-labelledby='maintenance-title'>
        <div className='flex flex-wrap items-center gap-2'>
          <h2 id='maintenance-title' className='text-sm font-medium'>Agent 维护</h2>
          <Badge variant={props.maintenanceAccess.agent_tokens_enabled ? 'success' : 'secondary'}>{props.maintenanceAccess.agent_tokens_enabled ? '已开启' : '默认关闭'}</Badge>
          <Badge variant='outline'>维护专用</Badge>
          <span className='min-w-0 flex-1 text-[11px] text-muted-foreground'>仅访问 /manage/mcp，与服务调用隔离</span>
          <Button type='button' variant='outline' size='sm' role='switch' aria-checked={props.maintenanceAccess.agent_tokens_enabled} disabled={savingMaintenanceAccess} onClick={toggleAgentMaintenance}>{savingMaintenanceAccess ? '保存中…' : props.maintenanceAccess.agent_tokens_enabled ? '关闭' : '开启'}</Button>
        </div>
        {props.maintenanceAccess.agent_tokens_enabled ? (
          <div className='mt-3 flex flex-wrap items-center gap-1.5 border-t pt-3'>
            <Label htmlFor='maintenance-key' className='shrink-0 text-xs'>维护 Key</Label>
            <div className='flex min-w-[16rem] flex-1 items-center'>
              <Input id='maintenance-key' className='h-8 rounded-r-none font-mono text-xs' type={maintenanceKeyVisible ? 'text' : 'password'} autoComplete='new-password' spellCheck={false} value={maintenanceKey} placeholder={maintenanceToken?.key || '输入自定义 Key，或生成随机 Key'} onChange={(event) => setMaintenanceKey(event.target.value)} />
              <Button className='h-8 rounded-none border-l-0' type='button' variant='outline' size='icon' aria-label={maintenanceKeyVisible ? '隐藏维护 Key' : '显示维护 Key'} onClick={() => setMaintenanceKeyVisible((visible) => !visible)}>{maintenanceKeyVisible ? <EyeOff aria-hidden='true' /> : <Eye aria-hidden='true' />}</Button>
              <Button className='h-8 rounded-l-none border-l-0' type='button' variant='outline' size='icon' aria-label='生成随机维护 Key' onClick={randomizeMaintenanceKey}><RefreshCw aria-hidden='true' /></Button>
            </div>
            <Button type='button' size='sm' disabled={!maintenanceToken || !maintenanceKey || savingMaintenanceKey} onClick={saveMaintenanceKey}><Save aria-hidden='true' />{savingMaintenanceKey ? '保存中' : '保存'}</Button>
          </div>
        ) : null}
      </section>

      <section aria-labelledby='agent-list-title'>
        <div className='flex items-end justify-between border-b pb-2'>
          <h2 id='agent-list-title' className='text-sm font-semibold'>Agent 用量与额度</h2>
          <span className='text-xs tabular-nums text-muted-foreground'>{registeredTokens.length} 个</span>
        </div>
        {registeredTokens.length ? (
          <div className='divide-y border-b'>
            {registeredTokens.map((token) => {
              const visible = visibleTokens[token.id]
              const usage = usageByToken.get(token.id)
              const system = false
              const registered = Boolean(token.agent_code && token.workspace)
              const policy = props.policies?.find((item) => item.token_id === token.id)
              const maintainer = policy?.capabilities?.includes('localrouter.maintain') || false
              return (
                <article key={token.id} className='grid gap-3 py-3 xl:grid-cols-[minmax(13rem,1fr)_minmax(14rem,1.1fr)_minmax(17rem,1.15fr)_auto] xl:items-center'>
                  <div className='min-w-0'>
                    <div className='flex flex-wrap items-center gap-2'><Bot aria-hidden='true' className='size-4 shrink-0 text-primary' /><p className='truncate text-sm font-medium'>{token.agent_name || token.name}</p>{system ? <Badge variant='secondary'>系统</Badge> : registered ? <Badge variant='success'>已注册</Badge> : <Badge variant='warning'>待补登记</Badge>}{maintainer ? <Badge variant='secondary'>维护专用</Badge> : null}</div>
                    <p className='mt-1 truncate font-mono text-[11px] text-muted-foreground'>{token.agent_code || `token-${token.id}`} · {token.runtime || 'runtime 未登记'}</p>
                    <p className='mt-1 truncate text-[11px] text-muted-foreground' title={token.workspace}>{token.workspace || '工作区未登记'}</p>
                  </div>
                  <div className='grid grid-cols-3 divide-x border-y py-2 text-center text-xs'>
                    <div><span className='block font-medium tabular-nums'>{compactNumber.format(usage?.requests || 0)}</span><span className='text-[10px] text-muted-foreground'>调用</span></div>
                    <div><span className='block font-medium tabular-nums'>{compactNumber.format((usage?.prompt_tokens || 0) + (usage?.completion_tokens || 0))}</span><span className='text-[10px] text-muted-foreground'>Token</span></div>
                    <div><span className='block font-medium tabular-nums'>{usage?.requests ? `${decimalNumber.format(((usage.successful || 0) / usage.requests) * 100)}%` : '—'}</span><span className='text-[10px] text-muted-foreground'>成功率</span></div>
                  </div>
                  <div className='grid grid-cols-2 gap-x-4 gap-y-1 text-xs'>
                    <span className='text-muted-foreground'>额度</span><span className='text-right tabular-nums'>{quotaLabel(usage)}</span>
                    <span className='text-muted-foreground'>成本</span><span className='text-right tabular-nums'>{costLabel(usage)}</span>
                    <span className='text-muted-foreground'>最后使用</span><span className='text-right'>{formatTimestamp(usage?.last_used_at || token.accessed_time)}</span>
                    <span className='text-muted-foreground'>Token</span><code className='truncate text-right'>{visible || token.key || '已遮罩'}</code>
                  </div>
                  <div className='flex flex-wrap gap-1 xl:justify-end'>
                    {!system && registered && !maintainer ? <Button variant='outline' size='sm' disabled={loadingId === token.id} onClick={() => copyConnection(token)}><Copy aria-hidden='true' />{loadingId === token.id ? '读取中' : '快速接入'}</Button> : null}
                    <Button variant='ghost' size='icon' aria-label={`${visible ? '隐藏' : '显示'}令牌 ${token.name}`} disabled={loadingId === token.id} onClick={() => revealToken(token)}>{visible ? <EyeOff aria-hidden='true' /> : <Eye aria-hidden='true' />}</Button>
                    <Button variant='ghost' size='icon' aria-label={`复制令牌 ${token.name}`} disabled={!visible} onClick={() => copyToken(token)}><KeyRound aria-hidden='true' /></Button>
                    <Button variant='ghost' size='icon' aria-label={`编辑 Agent ${token.agent_name || token.name}`} onClick={() => openPolicy(token)}><Settings2 aria-hidden='true' /></Button>
                    {!system ? <Button variant='ghost' size='icon' className='text-destructive hover:bg-destructive/10 hover:text-destructive' aria-label={`撤销令牌 ${token.name}`} onClick={() => setRevokeTarget(token)}><Trash2 aria-hidden='true' /></Button> : null}
                  </div>
                </article>
              )
            })}
          </div>
        ) : <EmptyState icon={Bot} title='还没有 Agent' description='注册 Agent 后会同时签发一枚绑定身份的本机调用 Token。' />}
      </section>

      <Dialog open={Boolean(policyTarget)} onOpenChange={(open) => { if (!open) { setPolicyTarget(null); setDraft(emptyDraft) } }}>
        <DialogContent className='sm:max-w-2xl'>
          <DialogHeader><DialogTitle>Agent 与额度 · {policyTarget?.agent_name || policyTarget?.name}</DialogTitle><DialogDescription>修改注册资料、每日请求上限、每分钟速率和并发上限。</DialogDescription></DialogHeader>
          <AgentForm id='agent-policy-form' draft={draft} onChange={setDraft} onSubmit={savePolicy} system={policyTarget?.name === 'LocalRouter default'} />
          <DialogFooter><Button variant='outline' type='button' onClick={() => setPolicyTarget(null)}>取消</Button><Button form='agent-policy-form' type='submit' disabled={savingPolicy}><ShieldCheck aria-hidden='true' />保存</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(revokeTarget)} onOpenChange={(open) => !open && setRevokeTarget(null)}>
        <DialogContent><DialogHeader><DialogTitle>撤销 Agent Token？</DialogTitle><DialogDescription>“{revokeTarget?.agent_name || revokeTarget?.name}”将立即无法访问模型与 Protocol Pack；删除前的请求日志不会清除。</DialogDescription></DialogHeader><DialogFooter><Button variant='outline' onClick={() => setRevokeTarget(null)}>取消</Button><Button variant='destructive' onClick={revokeToken}><Trash2 aria-hidden='true' />确认撤销</Button></DialogFooter></DialogContent>
      </Dialog>
    </div>
  )
}

function AgentForm(props: {
  id: string
  draft: AgentDraft
  onChange: (draft: AgentDraft) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  system?: boolean
}) {
  function update(field: keyof AgentDraft, value: string | boolean) {
    props.onChange({ ...props.draft, [field]: value })
  }
  return (
    <form id={props.id} className='grid gap-3 sm:grid-cols-2' onSubmit={props.onSubmit}>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-agent-code`}>Agent 编码</Label><Input id={`${props.id}-agent-code`} required={!props.system} disabled={props.system} pattern='[A-Za-z0-9._-]{2,48}' value={props.draft.agentCode} onChange={(event) => update('agentCode', event.target.value)} placeholder='build-agent-007' /></div>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-agent-name`}>Agent 名称</Label><Input id={`${props.id}-agent-name`} required={!props.system} disabled={props.system} maxLength={80} value={props.draft.agentName} onChange={(event) => update('agentName', event.target.value)} placeholder='构建 Agent' /></div>
      <div className='grid gap-1.5 sm:col-span-2'><Label htmlFor={`${props.id}-workspace`}>工作区</Label><Input id={`${props.id}-workspace`} required={!props.system} disabled={props.system} maxLength={512} value={props.draft.workspace} onChange={(event) => update('workspace', event.target.value)} placeholder='/workspace/example' /></div>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-runtime`}>运行时</Label><Input id={`${props.id}-runtime`} disabled={props.system} maxLength={64} value={props.draft.runtime} onChange={(event) => update('runtime', event.target.value)} placeholder='codex / claude / omp' /></div>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-token-name`}>Token 名称</Label><Input id={`${props.id}-token-name`} required disabled={props.system} maxLength={64} value={props.draft.tokenName} onChange={(event) => update('tokenName', event.target.value)} placeholder='build-agent-token' /></div>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-rpm`}>每分钟请求</Label><Input id={`${props.id}-rpm`} type='number' min='0' max='100000' inputMode='numeric' value={props.draft.requestsPerMinute} onChange={(event) => update('requestsPerMinute', event.target.value)} placeholder='不限' /></div>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-daily`}>每日请求</Label><Input id={`${props.id}-daily`} type='number' min='0' max='10000000' inputMode='numeric' value={props.draft.dailyRequestLimit} onChange={(event) => update('dailyRequestLimit', event.target.value)} placeholder='不限' /></div>
      <div className='grid gap-1.5'><Label htmlFor={`${props.id}-concurrency`}>最大并发</Label><Input id={`${props.id}-concurrency`} type='number' min='0' max='10000' inputMode='numeric' value={props.draft.maxInFlight} onChange={(event) => update('maxInFlight', event.target.value)} placeholder='不限' /></div>
      <label className='flex min-h-10 cursor-pointer items-center gap-3 rounded-md border px-3 py-2 text-sm'><input className='size-4 accent-primary' type='checkbox' checked={props.draft.maintainer} disabled={props.system} onChange={(event) => update('maintainer', event.target.checked)} /><span>维护专用 Token</span></label>
    </form>
  )
}

import { Copy, Eye, EyeOff, KeyRound, Plus, Settings2, ShieldCheck, Trash2 } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { LocalToken, MaintenanceAccess, TokenPolicy } from '@/lib/types'

export function TokensPage(props: {
  adminToken: string
  tokens: LocalToken[]
  policies?: TokenPolicy[]
  maintenanceAccess: MaintenanceAccess
  apiTokenFile: string
  onChanged: () => Promise<LocalToken[]>
}) {
  const [visibleTokens, setVisibleTokens] = useState<Record<number, string>>({})
  const [loadingId, setLoadingId] = useState<number | null>(null)
  const [issueOpen, setIssueOpen] = useState(false)
  const [tokenName, setTokenName] = useState('')
  const [issuing, setIssuing] = useState(false)
  const [revokeTarget, setRevokeTarget] = useState<LocalToken | null>(null)
  const [policyTarget, setPolicyTarget] = useState<LocalToken | null>(null)
  const [policyDraft, setPolicyDraft] = useState({ surfaces: '', packs: '', operations: '', models: '', maintainer: false })
  const [savingPolicy, setSavingPolicy] = useState(false)
  const [savingMaintenanceAccess, setSavingMaintenanceAccess] = useState(false)

  async function toggleAgentMaintenance() {
    setSavingMaintenanceAccess(true)
    try {
      await adminRequest<MaintenanceAccess>('/local/api/maintenance-access', props.adminToken, {
        method: 'PUT',
        body: JSON.stringify({ agent_tokens_enabled: !props.maintenanceAccess.agent_tokens_enabled }),
      })
      await props.onChanged()
      toast.success(props.maintenanceAccess.agent_tokens_enabled ? 'Agent 维护入口已关闭' : 'Agent 维护入口已开启')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '维护入口设置失败')
    } finally {
      setSavingMaintenanceAccess(false)
    }
  }

  async function readTokenKey(token: LocalToken) {
    const result = await adminRequest<{ key: string }>(
      `/local/api/tokens/${token.id}/reveal`,
      props.adminToken,
      { method: 'POST' }
    )
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

  async function issueToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = tokenName.trim()
    if (!name) {
      toast.error('请输入令牌名称。')
      return
    }
    setIssuing(true)
    try {
      await adminRequest<unknown>('/local/api/tokens', props.adminToken, {
        method: 'POST',
        body: JSON.stringify({ name, expired_time: -1 }),
      })
      const updated = await props.onChanged()
      const created = updated
        .filter((token) => token.name === name)
        .sort((left, right) => right.id - left.id)[0]
      if (created) {
		await adminRequest(`/local/api/token-policies/${created.id}`, props.adminToken, {
		  method: 'PUT',
		  body: JSON.stringify({}),
		})
        const fullToken = await readTokenKey(created)
        setVisibleTokens((current) => ({ ...current, [created.id]: fullToken }))
      }
      setTokenName('')
      setIssueOpen(false)
      toast.success('本机 API Token 已签发，默认长期有效且不限调用量')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '令牌签发失败')
    } finally {
      setIssuing(false)
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
      const revokedName = revokeTarget.name
      setRevokeTarget(null)
      setVisibleTokens((current) => {
        const next = { ...current }
        delete next[revokedId]
        return next
      })
      await props.onChanged()
      toast.success(`已撤销 ${revokedName}`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '令牌撤销失败')
    }
  }

  function openPolicy(token: LocalToken) {
	const policy = props.policies?.find((item) => item.token_id === token.id)
	setPolicyDraft({
	  surfaces: (policy?.surfaces || []).join(', '), packs: (policy?.packs || []).join(', '),
	  operations: (policy?.operations || []).join(', '), models: (policy?.models || []).join(', '),
	  maintainer: policy?.capabilities?.includes('localrouter.maintain') || false,
	})
	setPolicyTarget(token)
  }

  function splitValues(value: string) { return value.split(',').map((item) => item.trim()).filter(Boolean) }

  async function savePolicy(event: FormEvent) {
	event.preventDefault()
	if (!policyTarget) return
	setSavingPolicy(true)
	try {
	  await adminRequest(`/local/api/token-policies/${policyTarget.id}`, props.adminToken, { method: 'PUT', body: JSON.stringify({
		surfaces: splitValues(policyDraft.surfaces), packs: splitValues(policyDraft.packs), operations: splitValues(policyDraft.operations), models: splitValues(policyDraft.models),
		capabilities: policyDraft.maintainer ? ['localrouter.maintain'] : [], requests_per_minute: 0, daily_request_limit: 0, max_in_flight: 0, expires_at: 0,
	  }) })
	  setPolicyTarget(null)
	  await props.onChanged()
	  toast.success('令牌策略已生效')
	} catch (cause) {
	  toast.error(cause instanceof Error ? cause.message : '策略保存失败')
	} finally { setSavingPolicy(false) }
  }

  return (
    <div className='space-y-5'>
      <SectionHeader
        title='本机 API Token'
        actions={
          <Dialog open={issueOpen} onOpenChange={setIssueOpen}>
            <DialogTrigger asChild>
              <Button>
                <Plus aria-hidden='true' />
                签发 Token
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>签发本机 API Token</DialogTitle>
                <DialogDescription>服务调用 · 无限额度 · 可撤销 · 仅当前页明文</DialogDescription>
              </DialogHeader>
              <form id='issue-token-form' className='grid gap-2' onSubmit={issueToken}>
                <Label htmlFor='token-name'>名称</Label>
                <Input
                  id='token-name'
                  autoFocus
                  maxLength={50}
                  placeholder='例如 video-agent'
                  value={tokenName}
                  onChange={(event) => setTokenName(event.target.value)}
                />
              </form>
              <DialogFooter>
                <Button variant='outline' type='button' onClick={() => setIssueOpen(false)}>取消</Button>
                <Button form='issue-token-form' type='submit' disabled={issuing}>{issuing ? '签发中…' : '签发 Token'}</Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        }
      />

      <div className='flex flex-col gap-2 border-y py-3 sm:flex-row sm:items-center'>
        <div className='flex min-w-0 flex-1 items-center gap-2'>
          <ShieldCheck aria-hidden='true' className='size-4 shrink-0 text-primary' />
          <span className='text-xs font-medium'>初始化 Token 文件</span>
          <code className='min-w-0 truncate text-xs text-muted-foreground'>{props.apiTokenFile || '由网关运行时管理'}</code>
        </div>
        <Badge variant='success'>0600 · loopback</Badge>
        <div className='flex flex-wrap gap-1'>
          {['/v1', '/v1beta', '/p', '/w'].map((path) => <Badge key={path} variant='outline' className='font-mono'>{path}</Badge>)}
        </div>
      </div>

      <div className='flex flex-col gap-2 border-b pb-3 sm:flex-row sm:items-center'>
        <div className='min-w-0 flex-1'>
          <div className='flex flex-wrap items-center gap-2'>
            <span className='text-sm font-medium'>Agent 维护令牌</span>
            <Badge variant={props.maintenanceAccess.agent_tokens_enabled ? 'success' : 'secondary'}>
              {props.maintenanceAccess.agent_tokens_enabled ? '已开启' : '默认关闭'}
            </Badge>
            <Badge variant='outline'>与服务 Token 隔离</Badge>
          </div>
          <p className='mt-1 text-xs text-muted-foreground'>管理密钥始终可维护；开启后，标记为维护专用的 Token 才能访问 /manage/mcp。</p>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          role='switch'
          aria-checked={props.maintenanceAccess.agent_tokens_enabled}
          disabled={savingMaintenanceAccess}
          onClick={toggleAgentMaintenance}
        >
          {savingMaintenanceAccess ? '保存中…' : props.maintenanceAccess.agent_tokens_enabled ? '关闭 Agent 维护' : '开启 Agent 维护'}
        </Button>
      </div>

      <section aria-labelledby='token-list-title'>
        <div className='flex items-end justify-between border-b pb-2'>
          <h2 id='token-list-title' className='text-sm font-semibold'>已签发本机凭证</h2>
          <span className='text-xs tabular-nums text-muted-foreground'>{props.tokens.length} 枚</span>
        </div>

        {props.tokens.length ? (
          <div className='divide-y border-b'>
            {props.tokens.map((token) => {
              const visible = visibleTokens[token.id]
              const isBootstrap = token.name === 'LocalRouter default'
              const policy = props.policies?.find((item) => item.token_id === token.id)
              const isMaintainer = policy?.capabilities?.includes('localrouter.maintain') || false
              return (
                <div key={token.id} className='grid gap-3 py-3 lg:grid-cols-[minmax(11rem,0.7fr)_minmax(16rem,1.3fr)_10rem_auto] lg:items-center'>
                  <div className='min-w-0'>
                    <div className='flex items-center gap-2'>
                      <KeyRound aria-hidden='true' className='size-4 shrink-0 text-primary' />
                      <p className='truncate text-sm font-medium'>{token.name}</p>
                      {isBootstrap ? <Badge variant='secondary'>默认</Badge> : null}
                      <Badge variant={isMaintainer ? 'secondary' : 'outline'}>{isMaintainer ? '维护专用' : '服务调用'}</Badge>
                      {!isMaintainer ? <Badge variant={policy ? 'outline' : 'warning'}>{policy ? (policy.surfaces?.join(' · ') || '全部入口') : '无限制'}</Badge> : null}
                      {isMaintainer && !props.maintenanceAccess.agent_tokens_enabled ? <Badge variant='warning'>入口关闭</Badge> : null}
                    </div>
                    <p className='mt-1 text-xs text-muted-foreground'>#{token.id} · {token.status === 1 ? '启用' : '停用'}</p>
                  </div>
                  <code className='block min-h-9 overflow-x-auto border-l-2 border-muted px-3 py-2 text-xs'>
                    {visible || token.key || '已遮罩'}
                  </code>
                  <div className='text-xs text-muted-foreground'>
                    <p>无限额度 · {token.group}</p>
                    <p className='mt-1'>{formatTimestamp(token.accessed_time)}</p>
                  </div>
                  <div className='flex gap-1 lg:justify-end'>
                    <Button variant='ghost' size='sm' disabled={loadingId === token.id} onClick={() => revealToken(token)}>
                      {visible ? <EyeOff aria-hidden='true' /> : <Eye aria-hidden='true' />}
                      {loadingId === token.id ? '读取中' : visible ? '隐藏' : '显示'}
                    </Button>
                    <Button variant='ghost' size='icon' aria-label={`复制令牌 ${token.name}`} disabled={!visible} onClick={() => copyToken(token)}>
                      <Copy aria-hidden='true' />
                    </Button>
                    <Button variant='ghost' size='icon' aria-label={`编辑令牌策略 ${token.name}`} onClick={() => openPolicy(token)}><Settings2 aria-hidden='true' /></Button>
                    {!isBootstrap ? (
                      <Button variant='ghost' size='icon' className='text-destructive hover:bg-destructive/10 hover:text-destructive' aria-label={`撤销令牌 ${token.name}`} onClick={() => setRevokeTarget(token)}>
                        <Trash2 aria-hidden='true' />
                      </Button>
                    ) : null}
                  </div>
                </div>
              )
            })}
          </div>
        ) : (
          <EmptyState icon={KeyRound} title='没有可用的本机 Token' description='点击“签发 Token”创建一枚供 Agent 或本地应用使用的调用凭证。' />
        )}
      </section>

      <Dialog open={Boolean(policyTarget)} onOpenChange={(open) => !open && setPolicyTarget(null)}>
        <DialogContent>
          <DialogHeader><DialogTitle>令牌策略 · {policyTarget?.name}</DialogTitle><DialogDescription>服务 Token 用于调用；维护专用 Token 只用于修改 LocalRouter，两者不会混用。</DialogDescription></DialogHeader>
          <form id='token-policy-form' className='grid gap-3 sm:grid-cols-2' onSubmit={savePolicy}>
            <div className='grid gap-1.5 sm:col-span-2'><Label htmlFor='policy-surfaces'>入口</Label><Input id='policy-surfaces' value={policyDraft.surfaces} onChange={(event) => setPolicyDraft((current) => ({ ...current, surfaces: event.target.value }))} placeholder='p, w, mcp' /></div>
            <div className='grid gap-1.5'><Label htmlFor='policy-packs'>Packs</Label><Input id='policy-packs' value={policyDraft.packs} onChange={(event) => setPolicyDraft((current) => ({ ...current, packs: event.target.value }))} placeholder='search-primary, media-worker' /></div>
            <div className='grid gap-1.5'><Label htmlFor='policy-operations'>Operations</Label><Input id='policy-operations' value={policyDraft.operations} onChange={(event) => setPolicyDraft((current) => ({ ...current, operations: event.target.value }))} placeholder='search-primary.query' /></div>
            <div className='grid gap-1.5 sm:col-span-2'><Label htmlFor='policy-models'>Models</Label><Input id='policy-models' value={policyDraft.models} onChange={(event) => setPolicyDraft((current) => ({ ...current, models: event.target.value }))} placeholder='gpt-5, claude-sonnet' /></div>
            <label className='flex items-start gap-3 border-t pt-3 sm:col-span-2'>
              <input className='mt-0.5 size-4 accent-primary' type='checkbox' checked={policyDraft.maintainer} disabled={policyTarget?.name === 'LocalRouter default'} onChange={(event) => setPolicyDraft((current) => ({ ...current, maintainer: event.target.checked }))} />
              <span><span className='block text-sm font-medium'>切换为 Agent 维护专用 Token</span><span className='mt-0.5 block text-xs text-muted-foreground'>{policyTarget?.name === 'LocalRouter default' ? '默认服务 Token 受保护；请先签发另一枚 Token。' : '只能使用 /manage/mcp，不能调用模型或服务；还需开启页面上方的 Agent 维护入口。'}</span></span>
            </label>
          </form>
          <DialogFooter><Button variant='outline' onClick={() => setPolicyTarget(null)}>取消</Button><Button form='token-policy-form' type='submit' disabled={savingPolicy}><ShieldCheck />保存策略</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(revokeTarget)} onOpenChange={(open) => !open && setRevokeTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>撤销 API Token？</DialogTitle>
            <DialogDescription>“{revokeTarget?.name}”将立即无法访问模型与 Protocol Pack 入口，此操作不可撤销。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant='outline' onClick={() => setRevokeTarget(null)}>取消</Button>
            <Button variant='destructive' onClick={revokeToken}><Trash2 aria-hidden='true' />确认撤销</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

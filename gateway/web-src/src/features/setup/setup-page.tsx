import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, BookOpen, Check, CheckCheck, ChevronRight, Clock3, Copy, Layers3, LoaderCircle, RefreshCw, ShieldCheck, Waypoints, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { LocalToken } from '@/lib/types'
import { ServiceTraces } from './service-traces'
import type { ServiceBundle, ServiceProposal, ServiceTemplate, WorkspaceData } from './types'

const stateLabels: Record<string, string> = {
  superseded: '已有替代方案', preparing: 'Agent 正在准备', awaiting_approval: '等待授权', applying: '正在接入', applied: '已完成', undone: '已撤销',
  preparation_failed: '准备遇到问题', apply_failed: '接入遇到问题', rejected: '未授权', withdrawn: '已撤回', abandoned: '未完成授权已撤销',
}
const tabs = [ ['progress', '接入进展'], ['bundles', '能力包'], ['templates', '服务模板'], ['traces', '调用追踪'] ] as const

function proposalTitle(item: ServiceProposal) { return item.connection?.definition.name || item.bundle?.name || item.template?.name || item.id }
const probeLabels: Record<string, string> = {
  'probe-unhealthy': '上游返回了异常状态', 'probe-transport-failed': '连不上上游地址', 'probe-auth-failed': '密钥无法注入', 'probe-credential-unavailable': '没有可用的密钥',
  'probe-invalid-target': '目标地址无效', 'probe-read-failed': '读取上游响应失败', 'probe-expression-failed': '健康判断规则出错',
}
function liveFailure(item: ServiceProposal) { const status = item.verification?.startsWith('live-failed:') ? item.verification.slice(12) : ''; return status && (probeLabels[status] || status) }
function needsReview(item: ServiceProposal) { return Boolean((item.bundle && item.grant_token_id && !item.grant_policy) || item.connection?.definition.enabled === false) }
function stale(item: ServiceProposal) { return item.freshness === 'conflict' || item.freshness === 'unknown' }
// Batch approval covers plain service connections over HTTPS; permission grants,
// maintenance delegation and other targets keep individual review.
function batchable(item: ServiceProposal) {
  return item.state === 'awaiting_approval' && item.kind === 'connection' && !!item.connection && !needsReview(item) && !stale(item)
    && item.connection.definition.base_url.startsWith('https://') && !item.grant_token_id && !item.maintainer_token_id
}
function approvedMessage(item?: ServiceProposal) {
  const failure = item && liveFailure(item)
  if (failure) return `已接入，但上游检查未通过：${failure}。Agent 会收到修正提示。`
  if (item?.verification === 'live-ok') return '已接入，上游已连通'
  return '已按这份方案完成授权，Agent 可以继续'
}
const changeLabels: Record<string, string> = { added: '新增', modified: '修改', removed: '删除', deleted: '删除' }
function batchSummary(status: BatchStatus) {
  const items = Object.values(status), done = items.filter(item => item.state === 'done').length
  return done === items.length ? `${done} 份全部已接入，连通情况见每一项。` : `${done} 份已接入，${items.length - done} 份需要处理，原因见下方。`
}
type BatchStatus = Record<string, { state: 'pending' | 'running' | 'done' | 'failed'; message?: string }>

function BundleMembers({ bundle }: { bundle: ServiceBundle }) {
  return <div className='divide-y rounded-md border'>{bundle.members.length === 0
    ? <p className='p-4 text-sm text-muted-foreground'>空能力包 · 不授予任何服务调用权限</p>
    : bundle.members.map(member => <div className='px-4 py-3' key={member.pack}>
      <p className='text-sm font-medium'>{member.pack}</p>
      <p className='mt-1 break-words text-sm text-muted-foreground'>{member.operations.join(' · ') || '没有开放操作'}</p>
      {!!member.workflows?.length && <p className='mt-1 text-sm text-muted-foreground'>工作流：{member.workflows.join(' · ')}（已包含内部调用的操作）</p>}
    </div>)}</div>
}

export function SetupPage({ adminToken, tokens, focus }: { adminToken: string; tokens: LocalToken[]; focus?: string }) {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<typeof tabs[number][0]>('progress')
  const [page, setPage] = useState(1)
  const [selection, setSelection] = useState<ServiceProposal | null>(null)
  const [template, setTemplate] = useState<ServiceTemplate | null>(null)
  const [credential, setCredential] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [evidence, setEvidence] = useState<Record<string, unknown> | null>(null)
  const [batch, setBatch] = useState<ServiceProposal[] | null>(null)
  const [batchStatus, setBatchStatus] = useState<BatchStatus>({})
  const dataQuery = useQuery({ queryKey: ['service-workspace', page], queryFn: () => adminRequest<WorkspaceData>(`/local/api/service-workspace?page=${page}&page_size=20`, adminToken), refetchInterval: 5000, retry: false })
  const data = dataQuery.data
  const selected = data?.proposals.items.find(item => item.id === selection?.id) || selection
  const reviewBlocked = Boolean(selected && needsReview(selected))
  const selectedStale = Boolean(selected && selected.state === 'awaiting_approval' && stale(selected))
  const batchCandidates = data?.proposals.items.filter(batchable) || []
  const batchRunning = Object.values(batchStatus).some(item => item.state === 'running' || item.state === 'pending')
  const batchFinished = batch !== null && Object.keys(batchStatus).length > 0 && !batchRunning
  const agentName = (id: number) => { const token = tokens.find(item => item.id === id); return token?.agent_name || token?.name || `身份 ${id}` }

  // Notification and Action Desk links open the named proposal directly.
  useEffect(() => {
    if (!focus) return
    let cancelled = false
    adminRequest<{ proposal: ServiceProposal }>(`/local/api/service-proposals/${encodeURIComponent(focus)}`, adminToken)
      .then(result => { if (!cancelled && result?.proposal) { setTab('progress'); inspect(result.proposal) } })
      .catch(() => { if (!cancelled) toast.error('没有找到这份接入方案，可能已被替代或删除') })
    return () => { cancelled = true }
  }, [focus, adminToken])

  async function refresh() { await queryClient.invalidateQueries({ queryKey: ['service-workspace'] }) }
  function inspect(item: ServiceProposal) { setSelection(item); setCredential(''); setError(''); setEvidence(null) }
  async function decide(decision: 'approve' | 'reject' | 'abandon') {
    if (!selected) return
    setBusy(true); setError('')
    try {
      const result = await adminRequest<{ proposal?: ServiceProposal }>(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/decision`, adminToken, { method: 'POST', body: JSON.stringify({ digest: selected.digest, decision, ...(credential ? { credential } : {}) }) })
      if (decision !== 'approve') toast.success('已拒绝这份申请')
      else if (result?.proposal && liveFailure(result.proposal)) toast.warning(approvedMessage(result.proposal))
      else toast.success(approvedMessage(result?.proposal))
      setSelection(null)
    } catch (cause) { setError(cause instanceof Error ? cause.message : '处理失败，请刷新查看实际状态') }
    finally { setCredential(''); setBusy(false); await refresh() }
  }
  async function approveBatch() {
    if (!batch) return
    setBatchStatus(Object.fromEntries(batch.map(item => [item.id, { state: 'pending' }])))
    // Sequential on purpose: each apply changes live Packs and the next one is
    // rebuilt on top of it by the server.
    for (const item of batch) {
      setBatchStatus(current => ({ ...current, [item.id]: { state: 'running' } }))
      try {
        const result = await adminRequest<{ proposal?: ServiceProposal }>(`/local/api/service-proposals/${encodeURIComponent(item.id)}/decision`, adminToken, { method: 'POST', body: JSON.stringify({ digest: item.digest, decision: 'approve' }) })
        const failure = result?.proposal && liveFailure(result.proposal)
        setBatchStatus(current => ({ ...current, [item.id]: { state: failure ? 'failed' : 'done', message: failure ? `已接入，但上游检查未通过：${failure}` : result?.proposal?.verification === 'live-ok' ? '已接入，上游已连通' : '已接入' } }))
      } catch (cause) {
        setBatchStatus(current => ({ ...current, [item.id]: { state: 'failed', message: cause instanceof Error ? cause.message : '处理失败，请刷新查看实际状态' } }))
      }
    }
    await refresh()
  }
  function closeBatch() { if (!batchRunning) { setBatch(null); setBatchStatus({}) } }
  async function reprepare() {
    if (!selected) return
    setBusy(true); setError(''); setCredential('')
    try {
      const result = await adminRequest<{ proposal: ServiceProposal }>(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/reprepare`, adminToken, { method: 'POST', body: JSON.stringify({ digest: selected.digest, ...(selected.connection?.definition.enabled === false ? { enable: true } : {}) }) })
      inspect(result.proposal)
      await refresh()
      toast.success('已生成新方案，请重新核对服务与完整权限后授权')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '重新准备失败，请查看现有方案') }
    finally { setBusy(false) }
  }
  async function verify() {
    if (!selected) return
    setBusy(true); setError('')
    try { setEvidence(await adminRequest(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/verification`, adminToken)) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取验证记录') }
    finally { setBusy(false) }
  }
  async function undo() {
    if (!selected) return
    setBusy(true); setError('')
    try { await adminRequest(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/undo`, adminToken, { method: 'POST' }); await refresh(); toast.success('已撤销：新增的服务已删除，授权已收回，记录保留') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '撤销失败，请刷新查看状态') }
    finally { setBusy(false) }
  }
  async function recheck() {
    if (!selected) return
    setBusy(true); setError('')
    try {
      const result = await adminRequest<{ proposal?: ServiceProposal }>(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/check`, adminToken, { method: 'POST' })
      const failure = result?.proposal && liveFailure(result.proposal)
      if (failure) toast.warning(`上游仍然连不通：${failure}`); else if (result?.proposal?.verification === 'live-ok') toast.success('上游已连通'); else toast.message('这个服务没有可用于检查的探测或模型列表')
      await refresh()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '检查失败，请稍后再试') }
    finally { setBusy(false) }
  }
  async function reconcile() {
    if (!selected) return
    setBusy(true); setError('')
    try { await adminRequest(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/reconcile`, adminToken, { method: 'POST' }); await refresh() }
    catch (cause) { setError(cause instanceof Error ? cause.message : '无法核对接入状态') }
    finally { setBusy(false) }
  }
  async function revoke(id: string, kind: 'grants' | 'delegations') {
    setBusy(true)
    try { await adminRequest(`/local/api/service-${kind}/${id}`, adminToken, { method: 'DELETE' }); await refresh(); toast.success('授权已收回，后续调用立即按新权限检查') }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : '收回授权失败') }
    finally { setBusy(false) }
  }
  async function copy(value: string) {
    try { await navigator.clipboard.writeText(value); toast.success('已复制，可交给 Agent 继续') }
    catch { toast.error('复制失败，请手动选择文本') }
  }

  return <div className='space-y-6'>
    <SectionHeader title='接入与授权' description='Agent 准备接入、验证和维护。你查看进展，在需要新权限时授权。' actions={<Button variant='outline' onClick={refresh} disabled={dataQuery.isFetching}><RefreshCw aria-hidden='true' />刷新</Button>} />
    <nav aria-label='接入工作区' className='flex flex-wrap gap-2 border-b pb-3'>{tabs.map(([id, label]) => <Button key={id} variant={tab === id ? 'default' : 'ghost'} aria-pressed={tab === id} onClick={() => setTab(id)}>{label}</Button>)}</nav>
    {dataQuery.error && <p role='alert' className='rounded-md border border-destructive/30 p-4 text-sm text-destructive'>{dataQuery.error.message}</p>}
    {dataQuery.isPending && <p role='status' className='p-6 text-sm text-muted-foreground'>正在读取 Agent 接入进展…</p>}
    {data && tab === 'progress' && <>
      <div className='rounded-lg border bg-muted/20 p-5 sm:flex sm:items-center sm:justify-between sm:gap-6'>
        <div><p className='flex items-center gap-2 text-sm font-medium'><Waypoints aria-hidden='true' className='size-4' />从 Agent 的一个需求开始</p><p className='mt-2 max-w-2xl text-sm leading-6 text-muted-foreground'>让 Agent 选择模板并提交接入方案。目标服务、开放操作和维护范围会一起展示；授权后，Agent 接着完成调用验证。</p></div>
        <Button className='mt-4 shrink-0 sm:mt-0' variant='outline' onClick={() => copy('先运行 lr init 检查独立 Agent 身份，再读 lr guide。新服务没有独立身份时，用 lr identity prepare <agent-code> 申请仅准备身份，人批准后 lr identity claim；不要借无关服务权限。用 lr setup templates 查看目录，lr setup template <id> <version> 读取选中模板，lr setup schema 查看提案结构。按供应商文档准备 connection 及明确的 bundle，通过 lr setup prepare @proposal.json 提交。等待授权后检查状态，按契约预检并执行已获授权的调用，再用 lr setup verify <id> 核对证据。')}><Copy aria-hidden='true' />复制给 Agent 的说明</Button>
      </div>
      {batchCandidates.length >= 2 && <div className='flex flex-col gap-3 rounded-lg border border-primary/30 bg-primary/5 p-4 sm:flex-row sm:items-center sm:justify-between'>
        <p className='text-sm leading-6'><span className='font-medium'>{batchCandidates.length} 份服务接入可以一起处理。</span><span className='text-muted-foreground'>都只接入 HTTPS 服务，不改任何权限；批准后会逐个检查上游是否连通。</span></p>
        <Button className='shrink-0' onClick={() => { setBatch(batchCandidates); setBatchStatus({}) }}><CheckCheck aria-hidden='true' />核对并全部批准</Button>
      </div>}
      {data.proposals.items.length === 0 ? <EmptyState icon={Waypoints} title='等待 Agent 的第一份接入方案' description='Agent 提交申请后，准备进展、需要的权限和后续验证会出现在这里。' />
        : <section aria-label='接入申请' className='divide-y border-y'>{data.proposals.items.map(item => <article key={item.id} className='grid gap-4 py-5 sm:grid-cols-[minmax(0,1fr)_auto]'>
          <div className='min-w-0'><div className='flex flex-wrap items-center gap-3'><h2 className='font-medium'>{proposalTitle(item)}</h2><Badge variant='outline'>{stateLabels[item.state] || item.state}</Badge>{item.decided_by?.startsWith('auto:') && <Badge variant='secondary'>Agent 自动接入</Badge>}{item.state === 'awaiting_approval' && stale(item) && <Badge variant='outline' className='border-amber-500/50 text-amber-700 dark:text-amber-400'>需要重新准备</Badge>}{item.kind === 'connection' && item.state === 'applied' && (item.verification === 'live-ok' ? <Badge variant='outline' className='border-emerald-500/50 text-emerald-700 dark:text-emerald-400'>上游已连通</Badge> : liveFailure(item) ? <Badge variant='destructive'>连不通 · {liveFailure(item)}</Badge> : <span className='text-xs text-muted-foreground'>已安装 · 查看调用证据</span>)}</div>
            <p className='mt-2 text-sm leading-6 text-muted-foreground'>{item.reason}</p>
            <p className='mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground'><span>{agentName(item.owner_token_id)}</span><span>{formatTimestamp(item.updated_at)}</span>{item.bundle && <span>{item.bundle.members.length} 个服务 · 明确操作范围</span>}{item.maintainer_token_id && <span>申请兼容修复权限</span>}</p>
          </div>
          <Button className='self-center justify-self-start sm:justify-self-end' variant={item.state === 'awaiting_approval' ? 'default' : 'outline'} onClick={() => inspect(item)}>{item.state === 'awaiting_approval' ? '查看并授权' : '查看进展'}<ChevronRight aria-hidden='true' /></Button>
        </article>)}</section>}
      {data.proposals.total > 20 && <nav aria-label='接入申请分页' className='flex items-center justify-end gap-3 text-sm'><span>第 {page} 页 · 共 {data.proposals.total} 份</span><Button variant='outline' disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</Button><Button variant='outline' disabled={page * 20 >= data.proposals.total} onClick={() => setPage(page + 1)}>下一页</Button></nav>}
    </>}
    {data && tab === 'templates' && <div className='grid gap-4 md:grid-cols-2'>{data.templates.map(item => <article key={`${item.id}:${item.version}`} className='flex flex-col rounded-lg border p-5'><div className='flex items-center justify-between gap-3'><h2 className='font-medium'>{item.name}</h2><Badge variant='outline'>v{item.version}</Badge></div><p className='mt-3 flex-1 text-sm leading-6 text-muted-foreground'>{item.summary}</p><p className='mt-4 text-xs text-muted-foreground'>通用接入模式 · 凭据在本机单独配置</p><Button className='mt-4 w-fit' variant='outline' onClick={() => setTemplate(item)}><BookOpen aria-hidden='true' />查看接入说明</Button></article>)}</div>}
    {data && tab === 'bundles' && <div className='space-y-6'>
      <p className='text-sm leading-6 text-muted-foreground'>授权固定在明确的能力包版本上。新增操作需要重新授权；模型调用继续使用原有策略。</p>
      {data.bundles.length === 0 ? <EmptyState icon={Layers3} title='还没有能力包' description='Agent 可以把所需服务和操作组合成一份申请，授权后在这里复用。' />
        : data.bundles.map(bundle => <article className='space-y-4 rounded-lg border p-5' key={bundle.revision}><div className='flex flex-wrap items-center justify-between gap-3'><div><h2 className='font-medium'>{bundle.name}</h2><p className='mt-1 text-sm text-muted-foreground'>{bundle.description}</p></div><Badge variant='outline'>版本 {bundle.revision.slice(0, 8)}</Badge></div><BundleMembers bundle={bundle} />{bundle.guide && <p className='whitespace-pre-wrap text-sm leading-6 text-muted-foreground'>{bundle.guide}</p>}
          {Object.entries(data.grants).filter(([, grants]) => grants.some(grant => grant.revision === bundle.revision)).map(([id, grants]) => <div className='flex flex-wrap items-center justify-between gap-3 border-t pt-4' key={id}><p className='text-sm'>{agentName(Number(id))}<span className='ml-2 text-muted-foreground'>{grants.find(grant => grant.revision === bundle.revision)?.expires_at ? `到期 ${formatTimestamp(grants.find(grant => grant.revision === bundle.revision)?.expires_at)}` : '持续授权'}</span></p><Button variant='outline' disabled={busy} onClick={() => revoke(id, 'grants')}>收回该身份的全部服务授权</Button></div>)}
        </article>)}
      {Object.entries(data.delegations).filter(([, grants]) => grants.length > 0).map(([id, grants]) => <article className='rounded-lg border p-5' key={id}><div className='flex flex-wrap items-center justify-between gap-3'><h2 className='flex items-center gap-2 font-medium'><ShieldCheck className='size-4' aria-hidden='true' />{agentName(Number(id))} 的维护范围</h2><Button variant='outline' disabled={busy} onClick={() => revoke(id, 'delegations')}>收回维护授权</Button></div><p className='mt-3 text-sm text-muted-foreground'>{grants.map(grant => grant.pack).join(' · ')} · 允许范围内的兼容修复</p></article>)}
    </div>}
    {tab === 'traces' && <ServiceTraces adminToken={adminToken} tokens={tokens} />}

    <Dialog open={Boolean(selected)} onOpenChange={open => { if (!open && !busy) { setSelection(null); setCredential(''); setError(''); if (window.location.hash.startsWith('#setup/')) window.history.replaceState(null, '', '#setup') } }}>
      <DialogContent className='max-h-[90svh] max-w-3xl overflow-y-auto'>
        <DialogHeader><DialogTitle>{selected ? proposalTitle(selected) : '接入方案'}</DialogTitle><DialogDescription>{selected && `${agentName(selected.owner_token_id)} · ${stateLabels[selected.state] || selected.state}`}</DialogDescription></DialogHeader>
        {selected && <div className='space-y-5'>
          <p className='text-sm leading-6'>{selected.reason}</p>
          {selected.supersedes && <p className='break-all text-sm text-muted-foreground'>替代方案来源：{selected.supersedes}。此次须重新审阅授权。</p>}
          {selected.superseded_by && <p className='break-all text-sm'>请查看替代方案：{selected.superseded_by}</p>}
          {selected.state === 'awaiting_approval' && selected.freshness === 'rebase' && <p role='status' className='rounded-md bg-muted/40 p-3 text-sm leading-6 text-muted-foreground'>提交后其他服务有过更新。批准时会在当前配置上应用同一份变更，涉及的文件和内容与下面一致。</p>}
          {selectedStale && <p role='status' className='rounded-md border border-amber-500/40 p-4 text-sm leading-6'>{selected.freshness === 'conflict' ? `${selected.connection?.definition.id} 在这份方案提交后被修改过。` : '这份方案较早，无法自动对齐当前配置。'}重新准备会对照当前版本生成新方案，内容不变的话直接批准即可。</p>}
          {selected.state === 'applied' && liveFailure(selected) && <p role='alert' className='rounded-md border border-destructive/30 p-4 text-sm leading-6 text-destructive'>已接入，但上游检查未通过：{liveFailure(selected)}。可以撤销，或让 Agent 修正地址、认证或密钥后重新提交。</p>}
          {selected.state === 'awaiting_approval' && reviewBlocked && <p role='status' className='rounded-md border p-4 text-sm leading-6'>这份方案需要重新准备：旧权限快照不完整或连接未启用。重新准备会保留旧记录，展示新的完整权限；确认后再填写密钥并授权。</p>}
          <ol aria-label='接入步骤' className='grid gap-2 rounded-md bg-muted/30 p-4 text-sm sm:grid-cols-3'><li className='flex items-center gap-2'><Check className='size-4' aria-hidden='true' />Agent 准备方案</li><li className='flex items-center gap-2'><ShieldCheck className='size-4' aria-hidden='true' />{selected.state === 'applied' ? '已授权并应用' : '确认服务与权限'}</li><li className='flex items-center gap-2'>{selected.verification === 'live-ok' ? <><Check className='size-4 text-emerald-600' aria-hidden='true' />上游已连通</> : liveFailure(selected) ? <><X className='size-4 text-destructive' aria-hidden='true' />上游连不通</> : <><Clock3 className='size-4' aria-hidden='true' />查看真实调用证据</>}</li></ol>
          {selected.connection && <section className='space-y-3'><h3 className='text-sm font-medium'>接入服务</h3><dl className='grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[6rem_minmax(0,1fr)]'><dt className='text-muted-foreground'>目标地址</dt><dd className='break-all'>{selected.connection.definition.base_url}</dd><dt className='text-muted-foreground'>批准后连接</dt><dd>{selected.connection.definition.enabled ? '启用' : '禁用 · 需要重新准备'}</dd><dt className='text-muted-foreground'>认证方式</dt><dd>{({ none: '无需密钥', bearer: 'Bearer 密钥', header: `请求头密钥 ${selected.connection.definition.auth.header || ''}` } as Record<string, string>)[selected.connection.definition.auth.type] || selected.connection.definition.auth.type}</dd><dt className='text-muted-foreground'>模板</dt><dd>{selected.connection.template_id} · v{selected.connection.template_version}</dd></dl><div className='divide-y rounded-md border'>{selected.connection.definition.routes.map(route => <div key={route.operation_id} className='px-4 py-3 text-sm'><p className='font-medium'>{route.summary || route.operation_id}</p><p className='mt-1 break-all font-mono text-xs text-muted-foreground'>{route.methods.join(', ')} {route.path} · {route.operation_id}</p></div>)}</div></section>}
          {selected.bundle && <section className='space-y-3'><h3 className='text-sm font-medium'>授予 {selected.grant_token_id ? agentName(selected.grant_token_id) : '能力包目录'} 的服务能力</h3><BundleMembers bundle={selected.bundle} /><p className='text-sm text-muted-foreground'>本次服务能力与下方保留的旧能力共同生效；Token 策略、共享额度和服务连接状态也必须允许调用。</p></section>}
          {selected.retained_bundle && <section className='space-y-3'><h3 className='text-sm font-medium'>继续保留的既有服务</h3><BundleMembers bundle={selected.retained_bundle} /></section>}
          {selected.grant_policy && <section className='space-y-3 rounded-md border p-4 text-sm' aria-label='生效权限'><h3 className='font-medium'>批准后的完整 Token 范围</h3><p>接口：{selected.grant_policy.after.surfaces?.join('、') || '全部'}；服务：{selected.grant_policy.after.packs?.join('、') || '全部'}。</p><p className='break-all'>操作（含必要依赖）：{selected.grant_policy.after.operations?.join('、') || '全部'}。</p><p>模型：{selected.grant_policy.after.models?.join('、') || '所选操作的全部模型'}。请求次数、并发、金额预算与身份有效期沿用原策略；共享额度仍然生效。</p><details><summary className='cursor-pointer'>核对调整前后策略</summary><pre className='mt-2 max-h-64 overflow-auto text-xs'>{JSON.stringify({before:selected.grant_policy.before,after:selected.grant_policy.after},null,2)}</pre></details></section>}
          {selected.maintainer_token_id && <section className='rounded-md border p-4 text-sm leading-6'><p className='font-medium'>允许 {agentName(selected.maintainer_token_id)} 维护这个服务</p><p className='mt-2 text-muted-foreground'>可以修复既定接口内的 JSON 映射、说明、计量和超时。目标地址、认证、操作、路径、方法或工作流发生变化时，需要新的授权。维护 Token 必须单独签发，维护入口需已启用。</p></section>}
          {selected.template && <section className='text-sm leading-6'><p>发布模板 {selected.template.name} · v{selected.template.version}</p><p className='mt-2 text-muted-foreground'>{selected.template.summary}</p></section>}
          <details className='rounded-md border p-4 text-sm'><summary className='cursor-pointer font-medium'>查看这次变更的完整范围</summary><div className='mt-3 space-y-2 text-muted-foreground'>{selected.impact?.files.map(file => <p className='break-all' key={file.path}>{changeLabels[file.change] || file.change} · {file.path}</p>)}<p className='break-all'>方案版本 {selected.digest}</p>{selected.pack_digest && <p className='break-all'>Pack 版本 {selected.pack_digest}</p>}</div></details>
          {selected.state === 'awaiting_approval' && !reviewBlocked && selected.connection && selected.connection.definition.auth.type !== 'none' && selected.credential_provisioned && <p className='text-sm text-muted-foreground'>Agent 已提供服务密钥并保存在本机，批准时无需填写。</p>}
          {selected.state === 'awaiting_approval' && !reviewBlocked && selected.connection && selected.connection.definition.auth.type !== 'none' && !selected.credential_provisioned && <div className='space-y-2'><Label htmlFor='service-credential'>服务密钥</Label><Input id='service-credential' type='password' autoComplete='new-password' value={credential} onChange={event => setCredential(event.target.value)} /><p className='text-xs text-muted-foreground'>首次接入时填写；已有凭据绑定时留空。密钥单独保存在本机，不会写入模板或调用记录。</p></div>}
          {(error || selected.error) && <p role='alert' className='break-words rounded-md border border-destructive/30 p-3 text-sm text-destructive'>{error || selected.error}</p>}
          {evidence && <section aria-label='验证记录' className='rounded-md border p-4 text-sm leading-6'><p className='font-medium'>{evidence.installation === 'matches' ? '安装内容与授权版本一致' : '安装内容需要核对'}</p><p className='mt-2'>{evidence.upstream_operation === 'response-received' ? '已找到真实调用响应，业务结果请结合任务状态查看。' : '暂未找到这个版本的成功调用响应；已安装不代表服务已验证。'}</p>{typeof evidence.live_check === 'string' && evidence.live_check.startsWith('live-') && <p className='mt-2'>{evidence.live_check === 'live-ok' ? '批准时的上游连通检查已通过。' : `批准时的上游连通检查未通过：${probeLabels[evidence.live_check.slice(12)] || evidence.live_check.slice(12)}。`}</p>}<p className='mt-2 text-muted-foreground'>此次检查只读取已有记录，没有发起供应商调用。</p></section>}
          <div className='flex flex-wrap justify-end gap-3 border-t pt-4'>
            {selected.state === 'awaiting_approval' && <><Button variant='outline' disabled={busy} onClick={() => decide('reject')}>暂不授权</Button><Button variant={selectedStale ? 'default' : 'outline'} disabled={busy} onClick={reprepare}>{selected.connection?.definition.enabled === false ? '改为启用并重新准备' : '重新准备供审阅'}</Button><Button disabled={busy || reviewBlocked || selectedStale} onClick={() => decide('approve')}>{busy ? '正在执行…' : '授权并执行这份方案'}<ArrowRight aria-hidden='true' /></Button></>}
            {selected.state === 'applied' && selected.connection && <><Button variant='outline' disabled={busy} onClick={recheck}>重新检查连通</Button><Button variant='outline' disabled={busy} onClick={verify}>查看调用验证</Button></>}
            {selected.state === 'undone' && <p className='w-full text-sm text-muted-foreground'>已由{selected.undone_by?.startsWith('agent:') ? ' Agent 自己' : selected.undone_by === 'desktop' ? '桌面通知' : '控制台'}撤销：这次新增的服务已删除，授权已收回，Agent 提供的密钥已删除，记录保留。</p>}
            {selected.state === 'applied' && (selected.connection || selected.bundle) && <Button variant='outline' disabled={busy} onClick={undo}>撤销接入</Button>}
            {selected.state === 'applying' && <><p className='w-full text-sm text-muted-foreground'>撤销未完成授权会恢复这次应用前的策略；若之后已有人工改动则保留该改动。已安装服务和上游任务保留。</p><Button variant='outline' disabled={busy} onClick={() => decide('abandon')}>撤销未完成授权</Button><Button variant='outline' disabled={busy} onClick={reconcile}>核对已安装状态</Button></>}
          </div>
        </div>}
      </DialogContent>
    </Dialog>
    <Dialog open={Boolean(batch)} onOpenChange={open => { if (!open) closeBatch() }}>
      <DialogContent className='max-h-[90svh] max-w-3xl overflow-y-auto'>
        <DialogHeader><DialogTitle>{batchFinished ? '批量处理完成' : `批准 ${batch?.length || 0} 份服务接入`}</DialogTitle><DialogDescription>{batchFinished ? batchSummary(batchStatus) : '逐项核对目标地址和原因。每份只修改它自己的服务，不改任何权限。'}</DialogDescription></DialogHeader>
        {batch && <ol aria-label='待批准的接入' className='divide-y rounded-md border'>{batch.map(item => { const status = batchStatus[item.id]; return <li key={item.id} className='grid grid-cols-[1.25rem_minmax(0,1fr)] gap-3 px-4 py-3 text-sm'>
          <span className='pt-0.5' aria-hidden='true'>{status?.state === 'running' ? <LoaderCircle className='size-4 animate-spin' /> : status?.state === 'done' ? <Check className='size-4 text-emerald-600' /> : status?.state === 'failed' ? <X className='size-4 text-destructive' /> : <Clock3 className='size-4 text-muted-foreground' />}</span>
          <div className='min-w-0'><p className='font-medium'>{proposalTitle(item)} <span className='font-normal text-muted-foreground'>· {agentName(item.owner_token_id)}</span></p>
            <p className='mt-1 break-all font-mono text-xs text-muted-foreground'>{item.connection?.definition.base_url}</p>
            <p className='mt-1 text-muted-foreground'>{item.reason}</p>
            <p className='mt-1 text-xs text-muted-foreground'>{item.impact?.files.map(file => `${changeLabels[file.change] || file.change} ${file.path}`).join(' · ')}{item.freshness === 'rebase' && ' · 批准时在当前配置上应用'}</p>
            {status?.message && <p role={status.state === 'failed' ? 'alert' : undefined} className={`mt-1 break-words ${status.state === 'failed' ? 'text-destructive' : 'text-emerald-700 dark:text-emerald-400'}`}>{status.message}</p>}
          </div></li> })}</ol>}
        <div className='flex flex-wrap justify-end gap-3 border-t pt-4'>{batchFinished
          ? <Button onClick={closeBatch}>完成</Button>
          : <><Button variant='outline' disabled={batchRunning} onClick={closeBatch}>取消</Button><Button disabled={batchRunning} onClick={approveBatch}>{batchRunning ? '正在逐个接入…' : `全部批准 ${batch?.length || 0} 份`}<ArrowRight aria-hidden='true' /></Button></>}</div>
      </DialogContent>
    </Dialog>
    <Dialog open={Boolean(template)} onOpenChange={open => !open && setTemplate(null)}><DialogContent className='max-h-[90svh] max-w-2xl overflow-y-auto'><DialogHeader><DialogTitle>{template?.name}</DialogTitle><DialogDescription>通用模板 · v{template?.version} · 需要 Agent 按实际供应商文档适配</DialogDescription></DialogHeader>{template && <div className='space-y-4'><p className='text-sm leading-6'>{template.summary}</p><p className='text-sm text-muted-foreground'>Agent 可通过 lr setup template {template.id} {template.version} 读取同一份模板。准备完成后，授权申请会出现在接入进展中。</p><details className='rounded-md border p-4'><summary className='cursor-pointer text-sm font-medium'>Agent 维护说明与契约示例</summary><p className='mt-4 whitespace-pre-wrap text-sm leading-6'>{template.maintenance_guide}</p><pre className='mt-4 max-h-80 overflow-auto rounded-md bg-muted/40 p-4 text-xs'>{JSON.stringify(template.example, null, 2)}</pre></details><Button variant='outline' onClick={() => copy(JSON.stringify(template, null, 2))}><Copy aria-hidden='true' />复制完整模板</Button></div>}</DialogContent></Dialog>
  </div>
}

import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, BookOpen, Check, ChevronRight, Clock3, Copy, Layers3, RefreshCw, ShieldCheck, Waypoints } from 'lucide-react'
import { useState } from 'react'
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
  preparing: 'Agent 正在准备', awaiting_approval: '等待授权', applying: '正在接入', applied: '已完成',
  preparation_failed: '准备遇到问题', apply_failed: '接入遇到问题', rejected: '未授权', withdrawn: '已撤回',
}
const tabs = [ ['progress', '接入进展'], ['bundles', '能力包'], ['templates', '服务模板'], ['traces', '调用追踪'] ] as const

function proposalTitle(item: ServiceProposal) { return item.connection?.definition.name || item.bundle?.name || item.template?.name || item.id }

function BundleMembers({ bundle }: { bundle: ServiceBundle }) {
  return <div className='divide-y rounded-md border'>{bundle.members.length === 0
    ? <p className='p-4 text-sm text-muted-foreground'>空能力包 · 不授予任何服务调用权限</p>
    : bundle.members.map(member => <div className='px-4 py-3' key={member.pack}>
      <p className='text-sm font-medium'>{member.pack}</p>
      <p className='mt-1 break-words text-sm text-muted-foreground'>{member.operations.join(' · ') || '没有开放操作'}</p>
      {!!member.workflows?.length && <p className='mt-1 text-sm text-muted-foreground'>工作流：{member.workflows.join(' · ')}（已包含内部调用的操作）</p>}
    </div>)}</div>
}

export function SetupPage({ adminToken, tokens }: { adminToken: string; tokens: LocalToken[] }) {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<typeof tabs[number][0]>('progress')
  const [page, setPage] = useState(1)
  const [selection, setSelection] = useState<ServiceProposal | null>(null)
  const [template, setTemplate] = useState<ServiceTemplate | null>(null)
  const [credential, setCredential] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [evidence, setEvidence] = useState<Record<string, unknown> | null>(null)
  const dataQuery = useQuery({ queryKey: ['service-workspace', page], queryFn: () => adminRequest<WorkspaceData>(`/local/api/service-workspace?page=${page}&page_size=20`, adminToken), refetchInterval: 5000, retry: false })
  const data = dataQuery.data
  const selected = data?.proposals.items.find(item => item.id === selection?.id) || selection
  const agentName = (id: number) => { const token = tokens.find(item => item.id === id); return token?.agent_name || token?.name || `身份 ${id}` }

  async function refresh() { await queryClient.invalidateQueries({ queryKey: ['service-workspace'] }) }
  function inspect(item: ServiceProposal) { setSelection(item); setCredential(''); setError(''); setEvidence(null) }
  async function decide(decision: 'approve' | 'reject') {
    if (!selected) return
    setBusy(true); setError('')
    try {
      await adminRequest(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/decision`, adminToken, { method: 'POST', body: JSON.stringify({ digest: selected.digest, decision, ...(credential ? { credential } : {}) }) })
      toast.success(decision === 'approve' ? '已按这份方案完成授权，Agent 可以继续' : '已拒绝这份申请')
      setSelection(null)
    } catch (cause) { setError(cause instanceof Error ? cause.message : '处理失败，请刷新查看实际状态') }
    finally { setCredential(''); setBusy(false); await refresh() }
  }
  async function verify() {
    if (!selected) return
    setBusy(true); setError('')
    try { setEvidence(await adminRequest(`/local/api/service-proposals/${encodeURIComponent(selected.id)}/verification`, adminToken)) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取验证记录') }
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
        <Button className='mt-4 shrink-0 sm:mt-0' variant='outline' onClick={() => copy('先运行 lr init 检查独立 Agent 身份，再读 lr guide。用 lr setup templates 查看目录，lr setup template <id> <version> 读取选中模板，lr setup schema 查看提案结构。按供应商文档准备 connection 及明确的 bundle，通过 lr setup prepare @proposal.json 提交。等待授权后检查状态，按契约预检并执行已获授权的调用，再用 lr setup verify <id> 核对证据。')}><Copy aria-hidden='true' />复制给 Agent 的说明</Button>
      </div>
      {data.proposals.items.length === 0 ? <EmptyState icon={Waypoints} title='等待 Agent 的第一份接入方案' description='Agent 提交申请后，准备进展、需要的权限和后续验证会出现在这里。' />
        : <section aria-label='接入申请' className='divide-y border-y'>{data.proposals.items.map(item => <article key={item.id} className='grid gap-4 py-5 sm:grid-cols-[minmax(0,1fr)_auto]'>
          <div className='min-w-0'><div className='flex flex-wrap items-center gap-3'><h2 className='font-medium'>{proposalTitle(item)}</h2><Badge variant='outline'>{stateLabels[item.state] || item.state}</Badge>{item.kind === 'connection' && item.state === 'applied' && <span className='text-xs text-muted-foreground'>已安装 · 查看调用证据</span>}</div>
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

    <Dialog open={Boolean(selected)} onOpenChange={open => { if (!open && !busy) { setSelection(null); setCredential(''); setError('') } }}>
      <DialogContent className='max-h-[90svh] max-w-3xl overflow-y-auto'>
        <DialogHeader><DialogTitle>{selected ? proposalTitle(selected) : '接入方案'}</DialogTitle><DialogDescription>{selected && `${agentName(selected.owner_token_id)} · ${stateLabels[selected.state] || selected.state}`}</DialogDescription></DialogHeader>
        {selected && <div className='space-y-5'>
          <p className='text-sm leading-6'>{selected.reason}</p>
          <ol aria-label='接入步骤' className='grid gap-2 rounded-md bg-muted/30 p-4 text-sm sm:grid-cols-3'><li className='flex items-center gap-2'><Check className='size-4' aria-hidden='true' />Agent 准备方案</li><li className='flex items-center gap-2'><ShieldCheck className='size-4' aria-hidden='true' />{selected.state === 'applied' ? '已授权并应用' : '确认服务与权限'}</li><li className='flex items-center gap-2'><Clock3 className='size-4' aria-hidden='true' />查看真实调用证据</li></ol>
          {selected.connection && <section className='space-y-3'><h3 className='text-sm font-medium'>接入服务</h3><dl className='grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[6rem_minmax(0,1fr)]'><dt className='text-muted-foreground'>目标地址</dt><dd className='break-all'>{selected.connection.definition.base_url}</dd><dt className='text-muted-foreground'>认证方式</dt><dd>{selected.connection.definition.auth.type}</dd><dt className='text-muted-foreground'>模板</dt><dd>{selected.connection.template_id} · v{selected.connection.template_version}</dd></dl><div className='divide-y rounded-md border'>{selected.connection.definition.routes.map(route => <div key={route.operation_id} className='px-4 py-3 text-sm'><p className='font-medium'>{route.summary || route.operation_id}</p><p className='mt-1 break-all font-mono text-xs text-muted-foreground'>{route.methods.join(', ')} {route.path} · {route.operation_id}</p></div>)}</div></section>}
          {selected.bundle && <section className='space-y-3'><h3 className='text-sm font-medium'>授予 {selected.grant_token_id ? agentName(selected.grant_token_id) : '能力包目录'} 的服务能力</h3><BundleMembers bundle={selected.bundle} /><p className='text-sm text-muted-foreground'>仅允许已分配能力包中的服务操作；其他服务调用会被拒绝。模型调用保持原有策略。</p></section>}
          {selected.maintainer_token_id && <section className='rounded-md border p-4 text-sm leading-6'><p className='font-medium'>允许 {agentName(selected.maintainer_token_id)} 维护这个服务</p><p className='mt-2 text-muted-foreground'>可以修复既定接口内的 JSON 映射、说明、计量和超时。目标地址、认证、操作、路径、方法或工作流发生变化时，需要新的授权。维护 Token 必须单独签发，维护入口需已启用。</p></section>}
          {selected.template && <section className='text-sm leading-6'><p>发布模板 {selected.template.name} · v{selected.template.version}</p><p className='mt-2 text-muted-foreground'>{selected.template.summary}</p></section>}
          <details className='rounded-md border p-4 text-sm'><summary className='cursor-pointer font-medium'>查看这次变更的完整范围</summary><div className='mt-3 space-y-2 text-muted-foreground'>{selected.impact?.files.map(file => <p className='break-all' key={file.path}>{file.change} · {file.path}</p>)}<p className='break-all'>方案版本 {selected.digest}</p>{selected.pack_digest && <p className='break-all'>Pack 版本 {selected.pack_digest}</p>}</div></details>
          {selected.state === 'awaiting_approval' && selected.connection && selected.connection.definition.auth.type !== 'none' && <div className='space-y-2'><Label htmlFor='service-credential'>服务密钥</Label><Input id='service-credential' type='password' autoComplete='new-password' value={credential} onChange={event => setCredential(event.target.value)} /><p className='text-xs text-muted-foreground'>首次接入时填写；已有凭据绑定时留空。密钥单独保存在本机，不会写入模板或调用记录。</p></div>}
          {(error || selected.error) && <p role='alert' className='break-words rounded-md border border-destructive/30 p-3 text-sm text-destructive'>{error || selected.error}</p>}
          {evidence && <section aria-label='验证记录' className='rounded-md border p-4 text-sm leading-6'><p className='font-medium'>{evidence.installation === 'matches' ? '安装内容与授权版本一致' : '安装内容需要核对'}</p><p className='mt-2'>{evidence.upstream_operation === 'response-received' ? '已找到真实调用响应，业务结果请结合任务状态查看。' : '暂未找到这个版本的成功调用响应；已安装不代表服务已验证。'}</p><p className='mt-2 text-muted-foreground'>此次检查只读取已有记录，没有发起供应商调用。</p></section>}
          <div className='flex flex-wrap justify-end gap-3 border-t pt-4'>
            {selected.state === 'awaiting_approval' && <><Button variant='outline' disabled={busy} onClick={() => decide('reject')}>暂不授权</Button><Button disabled={busy} onClick={() => decide('approve')}>{busy ? '正在执行…' : '授权并执行这份方案'}<ArrowRight aria-hidden='true' /></Button></>}
            {selected.state === 'applied' && selected.connection && <Button variant='outline' disabled={busy} onClick={verify}>查看调用验证</Button>}
            {selected.state === 'applying' && <Button variant='outline' disabled={busy} onClick={reconcile}>核对已安装状态</Button>}
          </div>
        </div>}
      </DialogContent>
    </Dialog>
    <Dialog open={Boolean(template)} onOpenChange={open => !open && setTemplate(null)}><DialogContent className='max-h-[90svh] max-w-2xl overflow-y-auto'><DialogHeader><DialogTitle>{template?.name}</DialogTitle><DialogDescription>通用模板 · v{template?.version} · 需要 Agent 按实际供应商文档适配</DialogDescription></DialogHeader>{template && <div className='space-y-4'><p className='text-sm leading-6'>{template.summary}</p><p className='text-sm text-muted-foreground'>Agent 可通过 lr setup template {template.id} {template.version} 读取同一份模板。准备完成后，授权申请会出现在接入进展中。</p><details className='rounded-md border p-4'><summary className='cursor-pointer text-sm font-medium'>Agent 维护说明与契约示例</summary><p className='mt-4 whitespace-pre-wrap text-sm leading-6'>{template.maintenance_guide}</p><pre className='mt-4 max-h-80 overflow-auto rounded-md bg-muted/40 p-4 text-xs'>{JSON.stringify(template.example, null, 2)}</pre></details><Button variant='outline' onClick={() => copy(JSON.stringify(template, null, 2))}><Copy aria-hidden='true' />复制完整模板</Button></div>}</DialogContent></Dialog>
  </div>
}

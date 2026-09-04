import {
  Bot,
  Check,
  Clipboard,
  ExternalLink,
  FileCode2,
  GitCompareArrows,
  PencilLine,
  Plus,
  RefreshCcw,
  Rocket,
  Save,
  ServerCog,
  Trash2,
  Undo2,
} from 'lucide-react'
import { useMemo, useState, type FormEvent } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { ProtocolDraft, ProtocolRevision, ProtocolView } from '@/lib/types'
import { cn } from '@/lib/utils'

type ConfirmAction =
  | { kind: 'delete-draft'; draft: ProtocolDraft }
  | { kind: 'delete-file'; draftId: string; path: string }
  | { kind: 'rollback'; revision: ProtocolRevision }
  | { kind: 'reset-pool'; protocol: ProtocolView }

type EditorIntent =
  | { kind: 'create'; id: string }
  | { kind: 'edit'; draft: ProtocolDraft }

const AI_REMINDER_STORAGE_KEY = 'localrouter.protocol-editor.ai-reminder-dismissed'

const sectionLabels: Record<string, string> = {
  definition: '协议定义',
  routes: '接口与转换',
  pool: '号池',
  workflows: '工作流',
  auth: '认证',
  upstream: '上游',
  availability: '可用性',
  metadata: '说明',
  guides: '指南',
  guide: '指南',
  adapter: '适配模块',
  module: '适配模块',
  catalog: '能力目录',
  schema: '协议 Schema',
  other: '其他',
}

const changeLabels = { added: '新增', modified: '修改', removed: '删除' } as const

const agentEntries = [
  { name: '能力发现', path: '/.well-known/localrouter.json', auth: '公开', mode: 'open' as const },
  { name: '协议与指南', path: '/docs', auth: '公开', mode: 'open' as const },
  { name: 'OpenAPI', path: '/docs/openapi.json', auth: '公开', mode: 'open' as const },
  { name: 'MCP 工具', path: '/mcp', auth: 'API Token', mode: 'copy' as const },
  { name: '变更生命周期', path: '/local/api/protocol-drafts', auth: 'Admin Token', mode: 'copy' as const },
]

function draftFileURL(draftId: string, path: string) {
  return `/local/api/protocol-drafts/${encodeURIComponent(draftId)}/files/${path.split('/').map(encodeURIComponent).join('/')}`
}

function isTextFile(path: string) {
  return path.endsWith('.json') || path.endsWith('.md')
}

function isWritableTextFile(path: string) {
  const parts = path.split('/')
  if (parts.length === 1) return path.endsWith('.json')
  if (parts.length === 3 && parts[1] === 'guides') return path.endsWith('.md')
  if (parts.length === 2 && parts[0] === 'catalogs') return path.endsWith('.json') || path.endsWith('.md')
  return false
}

function impactSummary(draft: ProtocolDraft) {
  const counts = { added: 0, modified: 0, removed: 0 }
  for (const file of draft.impact.files) counts[file.change] += 1
  return counts
}

function poolQuotaSummary(protocol: ProtocolView) {
  const runtime = protocol.pool_runtime!
  const quota = runtime.quota
  if (!quota?.tracked_accounts) return <Badge variant='secondary'>未接入 · 0/{runtime.total}</Badge>
  if (quota.status === 'mixed-unit') return <Badge variant='warning'>单位不一致 · {quota.tracked_accounts}/{runtime.total}</Badge>
  if (quota.status === 'stale') return <Badge variant='warning'>已过期 {quota.stale_accounts} · {quota.tracked_accounts}/{runtime.total}</Badge>
  if (quota.used_percent !== undefined) {
    return <span className='text-xs tabular-nums'>已用 {Math.round(quota.used_percent)}% · 余 {quota.remaining ?? '—'} {quota.unit || ''} · {quota.tracked_accounts}/{runtime.total}</span>
  }
  if (quota.remaining !== undefined) return <span className='text-xs tabular-nums'>余量 {quota.remaining} {quota.unit || ''} · 仅余量 · {quota.tracked_accounts}/{runtime.total}</span>
  return <Badge variant='secondary'>部分接入 · {quota.tracked_accounts}/{runtime.total}</Badge>
}

export function ControlPlanePage(props: {
  adminToken: string
  drafts: ProtocolDraft[]
  revisions: ProtocolRevision[]
  protocols: ProtocolView[]
  onChanged: () => Promise<void>
  embedded?: boolean
}) {
  const [draftId, setDraftId] = useState('')
  const [busy, setBusy] = useState('')
  const [planned, setPlanned] = useState<{ draft: string; digest: string } | null>(null)
  const [confirm, setConfirm] = useState<ConfirmAction | null>(null)
  const [managedDraftId, setManagedDraftId] = useState('')
  const [selectedFile, setSelectedFile] = useState('')
  const [fileContent, setFileContent] = useState('')
  const [savedContent, setSavedContent] = useState('')
  const [newFilePath, setNewFilePath] = useState('')
  const [editorIntent, setEditorIntent] = useState<EditorIntent | null>(null)
  const [dismissAiReminder, setDismissAiReminder] = useState(false)

  const managedDraft = props.drafts.find((draft) => draft.id === managedDraftId)
  const localPools = props.protocols.filter((protocol) => protocol.pool_runtime?.ownership === 'local')
  const poolDrafts = useMemo(() => {
    const result = new Map<string, string[]>()
    for (const draft of props.drafts) {
      for (const poolId of draft.impact.pool_ids) {
        result.set(poolId, [...(result.get(poolId) || []), draft.id])
      }
    }
    return result
  }, [props.drafts])

  function reminderDismissed() {
    try {
      return typeof window.localStorage?.getItem === 'function' && window.localStorage.getItem(AI_REMINDER_STORAGE_KEY) === '1'
    } catch {
      return false
    }
  }

  async function createDraftById(id: string) {
    setBusy('create')
    try {
      await adminRequest('/local/api/protocol-drafts', props.adminToken, {
        method: 'POST',
        body: JSON.stringify({ id }),
      })
      setDraftId('')
      await props.onChanged()
      toast.success(`草稿 ${id} 已从当前发布版创建`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '草稿创建失败')
    } finally {
      setBusy('')
    }
  }

  function createDraft(event: FormEvent) {
    event.preventDefault()
    const id = draftId.trim()
    if (!id) return
    if (!reminderDismissed()) {
      setEditorIntent({ kind: 'create', id })
      return
    }
    void createDraftById(id)
  }

  async function validate(draft: ProtocolDraft) {
    setBusy(`validate:${draft.id}`)
    try {
      await adminRequest(`/local/api/protocol-drafts/${draft.id}/validate`, props.adminToken, { method: 'POST' })
      await props.onChanged()
      toast.success(`${draft.id} 校验通过`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '校验失败')
    } finally {
      setBusy('')
    }
  }

  async function plan(draft: ProtocolDraft) {
    setBusy(`plan:${draft.id}`)
    try {
      const result = await adminRequest<{ digest: string }>(`/local/api/protocol-drafts/${draft.id}/plan`, props.adminToken, { method: 'POST' })
      setPlanned({ draft: draft.id, digest: result.digest })
      toast.success('发布计划已锁定，可按 digest 发布')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '计划生成失败')
    } finally {
      setBusy('')
    }
  }

  async function apply() {
    if (!planned) return
    setBusy('apply')
    try {
      await adminRequest('/local/api/protocols/apply', props.adminToken, {
        method: 'POST',
        body: JSON.stringify({ digest: planned.digest }),
      })
      setPlanned(null)
      setManagedDraftId('')
      await props.onChanged()
      toast.success(`${planned.draft} 已原子发布`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '发布失败')
    } finally {
      setBusy('')
    }
  }

  async function copyPath(path: string) {
    try {
      await navigator.clipboard.writeText(`${window.location.origin}${path}`)
      toast.success('地址已复制')
    } catch {
      toast.error('浏览器未允许写入剪贴板')
    }
  }

  async function loadFile(draft: ProtocolDraft, path: string, initial = false) {
    setManagedDraftId(draft.id)
    setSelectedFile(path)
    setBusy(`file:${path}`)
    try {
      const response = await fetch(draftFileURL(draft.id, path), {
        headers: { Accept: 'text/plain, application/json', 'X-Local-Admin': props.adminToken },
      })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const content = await response.text()
      setFileContent(content)
      setSavedContent(content)
    } catch (cause) {
      setFileContent('')
      setSavedContent('')
      if (!initial) toast.error(cause instanceof Error ? cause.message : '文件读取失败')
    } finally {
      setBusy('')
    }
  }

  function openDraftNow(draft: ProtocolDraft) {
    setManagedDraftId(draft.id)
    const preferred = draft.impact.files.find((file) => file.change !== 'removed' && isTextFile(file.path))?.path
      || draft.files.find(isTextFile)
      || ''
    if (preferred) void loadFile(draft, preferred, true)
    else {
      setSelectedFile('')
      setFileContent('')
      setSavedContent('')
    }
  }

  function openDraft(draft: ProtocolDraft) {
    if (!reminderDismissed()) {
      setEditorIntent({ kind: 'edit', draft })
      return
    }
    openDraftNow(draft)
  }

  function continueEditorIntent() {
    const intent = editorIntent
    if (!intent) return
    if (dismissAiReminder) {
      try {
        if (typeof window.localStorage?.setItem === 'function') window.localStorage.setItem(AI_REMINDER_STORAGE_KEY, '1')
      } catch {
        // Storage may be disabled; editing remains available for this session.
      }
    }
    setEditorIntent(null)
    setDismissAiReminder(false)
    if (intent.kind === 'create') void createDraftById(intent.id)
    else openDraftNow(intent.draft)
  }

  function beginNewFile() {
    const path = newFilePath.trim().replace(/^\/+/, '')
    if (!path || !isWritableTextFile(path)) {
      toast.error('新文件必须是允许路径下的 .json 或 .md')
      return
    }
    setSelectedFile(path)
    setFileContent(path.endsWith('.json') ? '{\n\n}\n' : '# 新指南\n')
    setSavedContent('')
    setNewFilePath('')
  }

  async function saveFile() {
    if (!managedDraft || !selectedFile || !isWritableTextFile(selectedFile)) return
    setBusy(`save:${selectedFile}`)
    try {
      await adminRequest(draftFileURL(managedDraft.id, selectedFile), props.adminToken, {
        method: 'PUT',
        headers: { 'Content-Type': selectedFile.endsWith('.json') ? 'application/json' : 'text/markdown; charset=utf-8' },
        body: fileContent,
      })
      setSavedContent(fileContent)
      await props.onChanged()
      toast.success(`${selectedFile} 已保存`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '文件保存失败')
    } finally {
      setBusy('')
    }
  }

  async function confirmAction() {
    if (!confirm) return
    const action = confirm
    setBusy(`confirm:${action.kind}`)
    try {
      if (action.kind === 'delete-draft') {
        await adminRequest(`/local/api/protocol-drafts/${action.draft.id}`, props.adminToken, { method: 'DELETE' })
        if (managedDraftId === action.draft.id) setManagedDraftId('')
      } else if (action.kind === 'delete-file') {
        await adminRequest(draftFileURL(action.draftId, action.path), props.adminToken, { method: 'DELETE' })
        setSelectedFile('')
        setFileContent('')
        setSavedContent('')
      } else if (action.kind === 'rollback') {
        await adminRequest('/local/api/protocols/rollback', props.adminToken, {
          method: 'POST', body: JSON.stringify({ digest: action.revision.digest }),
        })
        setPlanned(null)
      } else {
        await adminRequest(`/local/api/protocols/${action.protocol.id}/pool/reset`, props.adminToken, {
          method: 'POST', body: '{}',
        })
      }
      setConfirm(null)
      await props.onChanged()
      toast.success(
        action.kind === 'delete-draft' ? '草稿已删除'
          : action.kind === 'delete-file' ? '文件已删除'
            : action.kind === 'rollback' ? '发布版已回滚'
              : `${action.protocol.name} 的异常调度状态已清除`
      )
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '操作失败')
    } finally {
      setBusy('')
    }
  }

  const confirmTitle = confirm?.kind === 'delete-draft' ? '删除草稿？'
    : confirm?.kind === 'delete-file' ? '删除文件？'
      : confirm?.kind === 'rollback' ? '回滚发布版？'
        : '清除号池异常状态？'
  const confirmDescription = confirm?.kind === 'delete-draft' ? `草稿 ${confirm.draft.id} 将被永久删除。`
    : confirm?.kind === 'delete-file' ? `${confirm.path} 将从草稿中删除，发布后对应能力可能消失。`
      : confirm?.kind === 'rollback' ? `协议目录将原子切换到 ${confirm.revision.digest.slice(0, 12)}。`
        : confirm?.kind === 'reset-pool' ? `${confirm.protocol.name} 的冷却、连续失败、调度停用、占用和租约状态会被清零；账号配置与凭据不变。`
          : ''

  return (
    <div className={props.embedded ? 'space-y-5' : 'space-y-6'}>
      <header className='flex min-h-10 flex-wrap items-center gap-2 border-b pb-2'>
        <div>
          <h1 className='text-lg font-semibold tracking-tight'>协议发布台</h1>
          <p className='text-[11px] text-muted-foreground'>接入 · 变更 · 人工复核 · 号池 · 发布</p>
        </div>
        <form onSubmit={createDraft} className='ml-auto flex min-w-0 items-end gap-1.5'>
          <Label className='sr-only' htmlFor='draft-id'>草稿 ID</Label>
          <Input className='h-8 w-40 text-xs' id='draft-id' pattern='[a-z][a-z0-9-]{1,31}' placeholder='草稿 ID' value={draftId} onChange={(event) => setDraftId(event.target.value)} />
          <Button type='submit' size='sm' disabled={busy === 'create' || !draftId.trim()}><Plus />{busy === 'create' ? '创建中' : '创建草稿'}</Button>
        </form>
      </header>

      <section className='border-y' aria-labelledby='agent-entry-title'>
        <div className='flex items-center gap-2 border-b px-3 py-2'><Bot aria-hidden='true' className='size-4 text-muted-foreground' /><h2 id='agent-entry-title' className='text-sm font-semibold'>Agent 接入</h2><span className='ml-auto text-[11px] text-muted-foreground'>一个入口发现全部 Pack</span></div>
        <Table>
          <TableHeader><TableRow><TableHead>能力</TableHead><TableHead>地址</TableHead><TableHead>权限</TableHead><TableHead className='text-right'>操作</TableHead></TableRow></TableHeader>
          <TableBody>{agentEntries.map((entry) => <TableRow key={entry.path}><TableCell className='font-medium'>{entry.name}</TableCell><TableCell><code className='text-[11px]'>{entry.path}</code></TableCell><TableCell><Badge variant='outline'>{entry.auth}</Badge></TableCell><TableCell><div className='flex justify-end'>{entry.mode === 'open' ? <Button asChild size='sm' variant='ghost'><a href={entry.path} target='_blank' rel='noreferrer'><ExternalLink />打开</a></Button> : <Button size='sm' variant='ghost' onClick={() => copyPath(entry.path)}><Clipboard />复制地址</Button>}</div></TableCell></TableRow>)}</TableBody>
        </Table>
      </section>

      <section aria-labelledby='draft-title'>
        <div className='flex min-h-10 items-center gap-2 border-b'><GitCompareArrows aria-hidden='true' className='size-4 text-muted-foreground' /><h2 id='draft-title' className='text-sm font-semibold'>变更草稿</h2><span className='text-[11px] text-muted-foreground'>{props.drafts.length} 个</span>{planned ? <div className='ml-auto flex items-center gap-2'><Badge variant='warning'>{planned.draft} · {planned.digest.slice(0, 8)}</Badge><Button size='sm' onClick={apply} disabled={busy === 'apply'}><Rocket />发布</Button></div> : null}</div>
        {props.drafts.length ? <Table>
          <TableHeader><TableRow><TableHead>草稿</TableHead><TableHead>实际变更</TableHead><TableHead>涉及 Pack</TableHead><TableHead>涉及号池</TableHead><TableHead>更新时间</TableHead><TableHead className='text-right'>操作</TableHead></TableRow></TableHeader>
          <TableBody>{props.drafts.map((draft) => {
            const counts = impactSummary(draft)
            return <TableRow key={draft.id}><TableCell><div className='flex items-center gap-2'><span className='font-medium'>{draft.id}</span><Badge variant={draft.stale ? 'warning' : draft.valid ? 'success' : 'destructive'}>{draft.stale ? '已过期' : draft.valid ? '可校验' : '需修正'}</Badge></div><code className='text-[10px] text-muted-foreground'>{draft.digest?.slice(0, 12) || '无 digest'}</code></TableCell><TableCell>{draft.impact.changed_files ? <div className='flex flex-wrap gap-1 text-[11px]'><span>{draft.impact.changed_files} 文件</span>{counts.added ? <Badge variant='outline'>+{counts.added}</Badge> : null}{counts.modified ? <Badge variant='outline'>~{counts.modified}</Badge> : null}{counts.removed ? <Badge variant='outline'>−{counts.removed}</Badge> : null}</div> : <span className='text-xs text-muted-foreground'>未更改</span>}</TableCell><TableCell><div className='flex max-w-64 flex-wrap gap-1'>{draft.impact.protocols.length ? draft.impact.protocols.map((item) => <Badge key={item.id} variant='secondary'>{item.name || item.id}</Badge>) : <span className='text-xs text-muted-foreground'>—</span>}</div></TableCell><TableCell><div className='flex flex-wrap gap-1'>{draft.impact.pool_ids.length ? draft.impact.pool_ids.map((id) => <Badge key={id} variant='warning'>{id}</Badge>) : <span className='text-xs text-muted-foreground'>不涉及</span>}</div></TableCell><TableCell className='text-xs text-muted-foreground'>{formatTimestamp(draft.updated_at)}</TableCell><TableCell><div className='flex justify-end gap-1'><Button size='sm' variant='ghost' onClick={() => openDraft(draft)}><PencilLine />管理</Button><Button size='sm' variant='ghost' onClick={() => validate(draft)} disabled={busy !== '' || draft.stale}><Check />校验</Button><Button size='sm' variant='ghost' onClick={() => plan(draft)} disabled={!draft.valid || draft.stale || busy !== '' || draft.impact.changed_files === 0}><GitCompareArrows />计划</Button><Button size='icon' variant='ghost' className='text-destructive' aria-label={`删除草稿 ${draft.id}`} onClick={() => setConfirm({ kind: 'delete-draft', draft })}><Trash2 /></Button></div></TableCell></TableRow>
          })}</TableBody>
        </Table> : <EmptyState icon={GitCompareArrows} title='没有变更草稿' description='新建草稿后，Agent 与人工可在同一隔离副本内协作。' />}
      </section>

      <section aria-labelledby='pool-ops-title'>
        <div className='flex min-h-10 items-center gap-2 border-b'><ServerCog aria-hidden='true' className='size-4 text-muted-foreground' /><h2 id='pool-ops-title' className='text-sm font-semibold'>号池运维</h2><span className='text-[11px] text-muted-foreground'>{localPools.length} 个本地池</span></div>
        <Table>
          <TableHeader><TableRow><TableHead>号池</TableHead><TableHead>调度</TableHead><TableHead>异常</TableHead><TableHead>额度</TableHead><TableHead>草稿影响</TableHead><TableHead className='text-right'>人工操作</TableHead></TableRow></TableHeader>
          <TableBody>{localPools.map((protocol) => {
            const runtime = protocol.pool_runtime!
            const invalid = Math.max(0, runtime.total - runtime.ready - runtime.busy)
            const affectedDrafts = poolDrafts.get(protocol.id) || []
            return <TableRow key={protocol.id}><TableCell><p className='font-medium'>{protocol.name}</p><code className='text-[10px] text-muted-foreground'>{protocol.id}</code></TableCell><TableCell className='text-xs tabular-nums'>{runtime.ready + runtime.busy}/{runtime.total} 可用 · {runtime.in_flight} 请求中</TableCell><TableCell><Badge variant={invalid ? 'warning' : 'success'}>{invalid ? `${invalid} 个异常` : '正常'}</Badge></TableCell><TableCell>{poolQuotaSummary(protocol)}</TableCell><TableCell><div className='flex flex-wrap gap-1'>{affectedDrafts.length ? affectedDrafts.map((id) => <Badge key={id} variant='outline'>{id}</Badge>) : <span className='text-xs text-muted-foreground'>无</span>}</div></TableCell><TableCell><div className='flex justify-end'><Button size='sm' variant='ghost' disabled={busy !== ''} onClick={() => setConfirm({ kind: 'reset-pool', protocol })}><RefreshCcw />清除异常状态</Button></div></TableCell></TableRow>
          })}</TableBody>
        </Table>
      </section>

      <section aria-labelledby='revision-title'>
        <div className='flex min-h-10 items-center gap-2 border-b'><Undo2 aria-hidden='true' className='size-4 text-muted-foreground' /><h2 id='revision-title' className='text-sm font-semibold'>发布历史</h2><span className='text-[11px] text-muted-foreground'>{props.revisions.length} 个版本</span></div>
        <Table><TableHeader><TableRow><TableHead>Digest</TableHead><TableHead>创建时间</TableHead><TableHead className='text-right'>状态</TableHead></TableRow></TableHeader><TableBody>{props.revisions.map((revision) => <TableRow key={revision.digest}><TableCell><code className='text-xs'>{revision.digest.slice(0, 20)}</code></TableCell><TableCell className='text-xs text-muted-foreground'>{formatTimestamp(revision.created_at)}</TableCell><TableCell><div className='flex justify-end'>{revision.live ? <Badge variant='success'>当前</Badge> : <Button size='sm' variant='ghost' onClick={() => setConfirm({ kind: 'rollback', revision })}><Undo2 />回滚</Button>}</div></TableCell></TableRow>)}</TableBody></Table>
      </section>

      <Sheet open={Boolean(managedDraft)} onOpenChange={(open) => !open && setManagedDraftId('')}>
        <SheetContent className='sm:max-w-[58rem]'>
          {managedDraft ? <><SheetHeader><SheetTitle>草稿 · {managedDraft.id}</SheetTitle><SheetDescription>{managedDraft.impact.changed_files} 个实际变更 · {managedDraft.impact.protocols.length} 个 Pack · {managedDraft.impact.pool_ids.length} 个号池</SheetDescription></SheetHeader>
            <SheetBody className='space-y-5'>
              {managedDraft.stale ? <div className='border-l-2 border-amber-300 bg-amber-50/50 px-3 py-2 text-xs text-amber-800 dark:border-amber-800 dark:bg-amber-950/20 dark:text-amber-200'>该草稿基于旧发布版，已禁止校验和发布。请从当前 live 新建草稿，只重做本次需要的变更。</div> : null}
              {!managedDraft.valid && managedDraft.error ? <div className='border-l-2 border-rose-300 bg-rose-50/50 px-3 py-2 text-xs text-rose-800 dark:border-rose-800 dark:bg-rose-950/20 dark:text-rose-200'>{managedDraft.error}</div> : null}
              <section aria-labelledby='impact-title'>
                <div className='flex min-h-9 items-center border-b'><h3 id='impact-title' className='text-sm font-semibold'>变更影响</h3></div>
                {managedDraft.impact.protocols.length ? <Table><TableHeader><TableRow><TableHead>Pack</TableHead><TableHead>变更范围</TableHead><TableHead>接口变化</TableHead><TableHead>号池</TableHead></TableRow></TableHeader><TableBody>{managedDraft.impact.protocols.map((impact) => <TableRow key={impact.id}><TableCell><p className='font-medium'>{impact.name || impact.id}</p><Badge variant='outline'>{changeLabels[impact.change]}</Badge></TableCell><TableCell><div className='flex max-w-56 flex-wrap gap-1'>{impact.sections.map((section) => <Badge key={section} variant='secondary'>{sectionLabels[section] || section}</Badge>)}</div></TableCell><TableCell className='text-[11px]'>{impact.operations_added?.length ? <p className='text-emerald-700/80 dark:text-emerald-300/80'>+ {impact.operations_added.join(', ')}</p> : null}{impact.operations_modified?.length ? <p className='text-muted-foreground'>~ {impact.operations_modified.join(', ')}</p> : null}{impact.operations_removed?.length ? <p className='text-rose-700/80 dark:text-rose-300/80'>− {impact.operations_removed.join(', ')}</p> : null}{!impact.operations_added?.length && !impact.operations_modified?.length && !impact.operations_removed?.length ? '—' : null}</TableCell><TableCell className='text-[11px]'>{impact.sections.includes('pool') ? `${impact.pool_mode_before || '无'} → ${impact.pool_mode_after || '无'}` : '不变'}</TableCell></TableRow>)}</TableBody></Table> : <p className='py-4 text-xs text-muted-foreground'>草稿与当前发布版一致。</p>}
              </section>
              <section aria-labelledby='file-editor-title'>
                <div className='flex min-h-9 items-center gap-2 border-b'><h3 id='file-editor-title' className='text-sm font-semibold'>人工编辑</h3><span className='text-[11px] text-muted-foreground'>仅协议、指南与目录文件；凭据路径不可写</span></div>
                <div className='grid min-h-[30rem] border-b lg:grid-cols-[17rem_minmax(0,1fr)]'>
                  <div className='border-b lg:border-b-0 lg:border-r'><div className='flex gap-1 border-b p-2'><Input className='h-8 text-xs' aria-label='新文件路径' placeholder='pack/guides/usage.md' value={newFilePath} onChange={(event) => setNewFilePath(event.target.value)} /><Button size='icon' variant='ghost' aria-label='新建文件' onClick={beginNewFile}><Plus /></Button></div><div className='max-h-80 overflow-y-auto p-1 lg:max-h-[34rem]'>{managedDraft.files.map((path) => { const change = managedDraft.impact.files.find((file) => file.path === path); return <button key={path} type='button' className={cn('flex min-h-9 w-full cursor-pointer items-center gap-2 rounded-sm px-2 text-left text-xs outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring', selectedFile === path && 'bg-muted')} onClick={() => isTextFile(path) ? void loadFile(managedDraft, path) : toast.info('WASM 模块只显示影响，不在文本编辑器中打开')}><FileCode2 aria-hidden='true' className='size-3.5 shrink-0 text-muted-foreground' /><span className='min-w-0 flex-1 truncate'>{path}</span>{change ? <span className='text-[10px] text-muted-foreground'>{changeLabels[change.change]}</span> : null}</button> })}</div></div>
                  <div className='flex min-w-0 flex-col'><div className='flex min-h-10 items-center gap-2 border-b px-3'><code className='min-w-0 flex-1 truncate text-[11px]'>{selectedFile || '选择或新建文件'}</code>{selectedFile && !isWritableTextFile(selectedFile) ? <Badge variant='secondary'>只读</Badge> : null}{selectedFile && isWritableTextFile(selectedFile) ? <Button size='sm' variant='ghost' className='text-destructive' onClick={() => setConfirm({ kind: 'delete-file', draftId: managedDraft.id, path: selectedFile })}><Trash2 />删除</Button> : null}</div>{selectedFile && isTextFile(selectedFile) ? <Textarea aria-label={`${isWritableTextFile(selectedFile) ? '编辑' : '查看'} ${selectedFile}`} className='min-h-[26rem] flex-1 resize-none rounded-none border-0 font-mono text-xs leading-5 focus-visible:ring-inset' spellCheck={false} readOnly={!isWritableTextFile(selectedFile)} value={fileContent} onChange={(event) => setFileContent(event.target.value)} /> : <div className='flex flex-1 items-center justify-center text-xs text-muted-foreground'>选择 JSON 或 Markdown 文件</div>}</div>
                </div>
              </section>
              <section aria-labelledby='file-impact-title'><div className='flex min-h-9 items-center border-b'><h3 id='file-impact-title' className='text-sm font-semibold'>文件清单</h3></div><Table><TableHeader><TableRow><TableHead>文件</TableHead><TableHead>动作</TableHead><TableHead>归属</TableHead><TableHead>影响面</TableHead></TableRow></TableHeader><TableBody>{managedDraft.impact.files.map((file) => <TableRow key={file.path}><TableCell><code className='text-[11px]'>{file.path}</code></TableCell><TableCell><Badge variant='outline'>{changeLabels[file.change]}</Badge></TableCell><TableCell className='text-xs'>{file.protocol_id || '全局'}</TableCell><TableCell className='text-xs'>{sectionLabels[file.area] || file.area}</TableCell></TableRow>)}</TableBody></Table></section>
            </SheetBody>
            <SheetFooter><Button variant='outline' onClick={() => validate(managedDraft)} disabled={busy !== '' || managedDraft.stale}><Check />校验</Button><Button variant='outline' onClick={() => plan(managedDraft)} disabled={!managedDraft.valid || managedDraft.stale || busy !== '' || managedDraft.impact.changed_files === 0}><GitCompareArrows />锁定计划</Button><Button onClick={saveFile} disabled={!selectedFile || !isWritableTextFile(selectedFile) || fileContent === savedContent || busy !== '' || managedDraft.stale}><Save />保存文件</Button></SheetFooter></> : null}
        </SheetContent>
      </Sheet>

      <Dialog open={Boolean(confirm)} onOpenChange={(open) => !open && setConfirm(null)}><DialogContent><DialogHeader><DialogTitle>{confirmTitle}</DialogTitle><DialogDescription>{confirmDescription}</DialogDescription></DialogHeader><DialogFooter><Button variant='outline' onClick={() => setConfirm(null)}>取消</Button><Button variant={confirm?.kind === 'reset-pool' ? 'default' : 'destructive'} onClick={confirmAction}>{confirm?.kind === 'reset-pool' ? <RefreshCcw /> : confirm?.kind === 'rollback' ? <Undo2 /> : <Trash2 />}确认</Button></DialogFooter></DialogContent></Dialog>
      <Dialog open={Boolean(editorIntent)} onOpenChange={(open) => { if (!open) { setEditorIntent(null); setDismissAiReminder(false) } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>推荐使用 AI Agent</DialogTitle>
            <DialogDescription>协议结构、认证、号池和发布链路关联较多，推荐让 AI Agent 创建和校验。</DialogDescription>
          </DialogHeader>
          <label className='flex cursor-pointer items-center gap-2 text-xs text-muted-foreground'>
            <input className='size-4 accent-primary' type='checkbox' checked={dismissAiReminder} onChange={(event) => setDismissAiReminder(event.target.checked)} />
            永久不再提示
          </label>
          <DialogFooter><Button variant='outline' onClick={() => setEditorIntent(null)}>取消</Button><Button onClick={continueEditorIntent}>继续人工编辑</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

import { Activity, Workflow } from 'lucide-react'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { EmptyState } from '@/components/empty-state'
import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { ProtocolEvent, WorkflowJob } from '@/lib/types'

function stateVariant(state: string) {
  if (state === 'succeeded') return 'success' as const
  if (state === 'failed' || state === 'timed_out' || state === 'outcome_unknown') return 'destructive' as const
  if (state === 'running' || state === 'pending') return 'warning' as const
  return 'secondary' as const
}

function eventUsage(event: ProtocolEvent) {
  const usage = event.usage
  if (!usage) return '—'
  const total = usage.total_tokens || (usage.input_tokens || 0) + (usage.output_tokens || 0)
  const cache = (usage.cache_read_input_tokens || 0) + (usage.cache_write_input_tokens || 0)
  return `${total.toLocaleString('zh-CN')} T${cache ? ` · 缓存 ${cache.toLocaleString('zh-CN')}` : ''}`
}

function eventCost(event: ProtocolEvent) {
  if (!event.cost) return '未计价'
  const label = `$${event.cost.amount_usd.toFixed(6)}`
  if (event.cost.status === 'reported') return `${label} 上游`
  if (event.cost.status === 'estimated') return `${label} 估算`
  if (event.cost.status === 'partial') return `${label} 部分`
  return label
}

export function JobsPage(props: { jobs: WorkflowJob[]; events: ProtocolEvent[]; adminToken?: string; onChanged?: () => Promise<void> }) {
  const [selected, setSelected] = useState<WorkflowJob | null>(null)
  const [cancelTarget, setCancelTarget] = useState<WorkflowJob | null>(null)
  const [cancelling, setCancelling] = useState(false)
  const detail = selected
  useEffect(() => {
    const latest = props.jobs.find(job => job.id === selected?.id && job.protocol_id === selected?.protocol_id)
    if (latest && selected && latest.updated_at > selected.updated_at) setSelected(latest)
  }, [props.jobs, selected])
  async function cancelJob() {
    if (!cancelTarget) return
    setCancelling(true)
    try {
      const path = [cancelTarget.protocol_id, cancelTarget.workflow_id, cancelTarget.id].map(encodeURIComponent).join('/')
      const updated = await adminRequest<WorkflowJob>(`/local/api/workflows/${path}/cancel`, props.adminToken || '', { method: 'POST' })
      setCancelTarget(null)
      setSelected(updated)
      if (updated.state === 'cancelled') toast.success('任务已取消')
      else if (['outcome_unknown', 'failed', 'timed_out'].includes(updated.state)) toast.error('未能确认取消，请查看任务详情')
      else toast.info('正在执行取消操作')
      await props.onChanged?.().catch(() => toast.error('取消结果已返回，但刷新列表失败，请稍后刷新'))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : '取消失败，请查看任务状态后再决定下一步')
    } finally { setCancelling(false) }
  }
  return <div className='space-y-8'>
    <SectionHeader title='任务与事件' />
    <section aria-labelledby='jobs-title'><div className='flex items-center justify-between border-b pb-2'><h2 id='jobs-title' className='text-sm font-semibold'>Workflow Jobs</h2><span className='text-xs text-muted-foreground'>{props.jobs.length}</span></div>{props.jobs.length ? <Table><TableHeader><TableRow><TableHead>任务</TableHead><TableHead>Pack / Workflow</TableHead><TableHead>状态</TableHead><TableHead>尝试</TableHead><TableHead>更新时间</TableHead><TableHead>操作</TableHead></TableRow></TableHeader><TableBody>{props.jobs.map((job) => <TableRow key={job.id}><TableCell><code className='text-xs'>{job.id}</code>{job.owner_token_id ? <p className='text-[10px] text-muted-foreground'>Token #{job.owner_token_id}</p> : null}</TableCell><TableCell className='text-xs'>{job.protocol_id} / {job.workflow_id}</TableCell><TableCell><Badge variant={stateVariant(job.state)}>{job.state}</Badge></TableCell><TableCell className='tabular-nums'>{job.attempts}/{job.max_attempts}</TableCell><TableCell className='text-xs text-muted-foreground'>{formatTimestamp(job.updated_at)}</TableCell><TableCell><div className='flex gap-2'><Button size='sm' variant='outline' aria-label={`查看任务 ${job.id}`} onClick={() => setSelected(job)}>详情</Button>{job.cancellable ? <Button size='sm' variant='outline' aria-label={`取消任务 ${job.id}`} disabled={cancelling || Boolean(job.cancelling && job.state === 'running')} onClick={() => setCancelTarget(job)}>取消</Button> : null}</div></TableCell></TableRow>)}</TableBody></Table> : <EmptyState icon={Workflow} title='暂无异步任务' description='通过 /w 创建的任务会出现在这里。' />}</section>
    <Dialog open={Boolean(detail)} onOpenChange={open => { if (!open) setSelected(null) }}>
      <DialogContent className='max-w-2xl'>
        <DialogHeader><DialogTitle>任务详情</DialogTitle><DialogDescription>{detail?.protocol_id} / {detail?.workflow_id} · {detail?.id}</DialogDescription></DialogHeader>
        {detail ? <div className='space-y-4'>
          <div className='flex flex-wrap gap-3 text-sm'><Badge variant={stateVariant(detail.state)}>{detail.state}</Badge><span>执行 {detail.attempts}/{detail.max_attempts} 次</span>{detail.cancel_attempts ? <span>取消操作 {detail.cancel_attempts} 次</span> : null}</div>
          {detail.error ? <div role='alert' className='whitespace-pre-wrap break-words rounded border border-destructive/40 p-3 text-sm text-destructive'>{detail.error}</div> : null}
          <h3 className='text-sm font-medium'>任务结果</h3>
          {detail.result !== undefined && detail.result !== null ? <pre className='max-h-[50svh] overflow-auto rounded border bg-muted p-3 text-xs'>{JSON.stringify(detail.result, null, 2)}</pre> : <p className='text-sm text-muted-foreground'>暂无结果</p>}
        </div> : null}
      </DialogContent>
    </Dialog>
    <Dialog open={Boolean(cancelTarget)} onOpenChange={open => { if (!open && !cancelling) setCancelTarget(null) }}>
      <DialogContent><DialogHeader><DialogTitle>取消任务</DialogTitle><DialogDescription>将请求取消 {cancelTarget?.id}。取消结果以任务状态为准。</DialogDescription></DialogHeader><DialogFooter><Button variant='outline' disabled={cancelling} onClick={() => setCancelTarget(null)}>返回</Button><Button disabled={cancelling} onClick={() => void cancelJob()}>{cancelling ? '取消中…' : '确认取消'}</Button></DialogFooter></DialogContent>
    </Dialog>
    <section aria-labelledby='events-title' ><div className='flex items-center justify-between border-b pb-2'><div><h2 id='events-title' className='text-sm font-semibold'>调用账本</h2><p className='mt-1 text-[11px] text-muted-foreground'>每次 Protocol Pack、Workflow 与 MCP 调用独立记录，Token 与成本以事件发生时的数据为准。</p></div><span className='text-xs text-muted-foreground'>{props.events.length}</span></div>{props.events.length ? <Table><TableHeader><TableRow><TableHead>时间</TableHead><TableHead>入口</TableHead><TableHead>操作</TableHead><TableHead>结果</TableHead><TableHead>用量 / 成本</TableHead><TableHead>延迟</TableHead><TableHead>后端 / 池引用</TableHead></TableRow></TableHeader><TableBody>{props.events.map((event) => <TableRow key={event.id}><TableCell className='whitespace-nowrap text-xs text-muted-foreground'>{formatTimestamp(event.created_at)}</TableCell><TableCell><Badge variant='outline'>{event.surface}</Badge></TableCell><TableCell><p className='text-xs font-medium'>{event.protocol_id || 'model'} · {event.operation_id || event.method}</p>{event.model ? <code className='block max-w-72 truncate text-[10px] text-primary'>{event.model}</code> : null}<code className='block max-w-72 truncate text-[10px] text-muted-foreground'>{event.path}</code></TableCell><TableCell><Badge variant={event.status >= 200 && event.status < 400 ? 'success' : 'destructive'}>{event.status}</Badge></TableCell><TableCell><p className='whitespace-nowrap text-xs tabular-nums'>{eventUsage(event)}</p><p className='whitespace-nowrap text-[10px] text-muted-foreground tabular-nums'>{eventCost(event)}</p></TableCell><TableCell className='tabular-nums'>{event.latency_ms} ms</TableCell><TableCell>{event.target ? <p className='text-xs font-medium'>{event.target}</p> : null}<code className='text-[10px]'>{event.credential_ref || '—'}</code></TableCell></TableRow>)}</TableBody></Table> : <EmptyState icon={Activity} title='暂无调用记录' description='Protocol Pack、Workflow 与 MCP 调用会写入脱敏账本。' />}</section>
  </div>
}

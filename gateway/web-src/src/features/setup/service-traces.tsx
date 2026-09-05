import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, ChevronRight, Search, Waypoints } from 'lucide-react'
import { useMemo, useState } from 'react'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { adminRequest, formatTimestamp } from '@/lib/api'
import type { LocalToken } from '@/lib/types'
import type { ServiceTrace, TracePage } from './types'

const outcomeLabels: Record<string, string> = {
  running: '进行中', response_received: '已收到响应', response_headers_received: '已收到响应头',
  rejected: '本地拒绝', upstream_error: '上游返回错误', transport_failed: '连接失败', outcome_unknown: '结果待核对',
  response_incomplete: '响应不完整', client_disconnected: '客户端断开', succeeded: '任务完成', cancelled: '任务已取消', failed: '任务失败', pending: '等待执行', timed_out: '任务超时',
}
const kindLabels: Record<string, string> = { request: '服务调用', attempt: '上游请求', projection: '工具 / 工作流入口', workflow: '任务状态', management: '接入管理操作' }

export function ServiceTraces({ adminToken, tokens }: { adminToken: string; tokens: LocalToken[] }) {
  const [search, setSearch] = useState('')
  const [task, setTask] = useState('')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<{ trace: string; token: number } | null>(null)
  const parameters = new URLSearchParams({ page: String(page), page_size: '50', ...(task ? { task_id: task } : {}), ...(selected ? { trace_id: selected.trace, token_id: String(selected.token) } : {}) })
  const query = useQuery({ queryKey: ['service-traces', parameters.toString()], queryFn: () => adminRequest<TracePage>(`/local/api/service-traces?${parameters}`, adminToken), refetchInterval: 5000, retry: false })
  const groups = useMemo(() => {
    const result = new Map<string, ServiceTrace[]>()
    for (const item of query.data?.items || []) { const key = `${item.token_id}:${item.trace_id}`; result.set(key, [...(result.get(key) || []), item]) }
    return [...result.values()]
  }, [query.data])
  const agentName = (id: number) => tokens.find(token => token.id === id)?.agent_name || `身份 ${id}`
  const events = [...(query.data?.items || [])].sort((a, b) => a.started_at.localeCompare(b.started_at))

  return <div className='space-y-5'>
    <div><h2 className='font-medium'>每次服务任务的来龙去脉</h2><p className='mt-2 text-sm leading-6 text-muted-foreground'>从 Agent 身份追到具体操作、上游请求和异步结果。费用缺失时保留未知，轮询和重试单独展示。</p></div>
    <form className='flex max-w-xl gap-2' onSubmit={event => { event.preventDefault(); setTask(search.trim()); setPage(1); setSelected(null) }}><Input aria-label='按任务标识查询' placeholder='输入 Agent 提供的任务标识' value={search} onChange={event => setSearch(event.target.value)} /><Button type='submit' variant='outline'><Search aria-hidden='true' />查询</Button></form>
    {query.data?.summary && <section aria-label='用量汇总' className='rounded-md border p-4 text-sm'><p>全部匹配记录：{query.data.summary.requests} 次服务调用 · {query.data.summary.attempts} 次上游尝试 · {query.data.summary.unknown_outcomes} 次结果待核对</p><p className='mt-2 text-muted-foreground'>{query.data.summary.unknown_costs} 次调用费用未知；已知金额按计价来源分别统计。</p>{Object.entries(query.data.summary.cost_usd_by_status).map(([status, amount]) => <p key={status}>{status} · ${amount.toFixed(6)}</p>)}{query.data.summary.units.map((unit, i) => <p className='mt-2' key={i}>{unit.pack} · {unit.quantity} {unit.unit} · {unit.source === 'request' ? '请求参数' : '供应商返回'} · {unit.mode === 'snapshot' ? '已按资源去重' : '累加记录'}</p>)}</section>}
    {query.error && <p role='alert' className='text-sm text-destructive'>{query.error.message}</p>}
    {query.isPending && <p role='status' className='text-sm text-muted-foreground'>正在读取调用记录…</p>}
    {selected && <div className='flex flex-wrap items-center gap-3'><Button variant='outline' onClick={() => { setSelected(null); setPage(1) }}><ArrowLeft aria-hidden='true' />返回调用列表</Button><p className='break-all font-mono text-xs text-muted-foreground'>{selected.trace}</p></div>}
    {!selected && groups.length > 0 && <section aria-label='服务任务列表' className='divide-y border-y'>{groups.map(items => { const item = items[0]; return <article className='flex flex-wrap items-center justify-between gap-4 py-5' key={`${item.token_id}:${item.trace_id}`}><div className='min-w-0'><p className='break-all text-sm font-medium'>{item.kind === 'management' ? '接入管理操作' : item.task_id || item.pack || '独立调用'}<span className='ml-3 font-normal text-muted-foreground'>{agentName(item.token_id)}</span></p><p className='mt-2 text-xs text-muted-foreground'>{formatTimestamp(item.started_at)} · 本页 {items.length} 条记录 · {item.trace_id.slice(0, 12)}</p></div><Button variant='outline' onClick={() => { setSelected({ trace: item.trace_id, token: item.token_id }); setPage(1) }}>展开调用链<ChevronRight aria-hidden='true' /></Button></article> })}</section>}
    {selected && events.length > 0 && <ol aria-label='调用时间线' className='space-y-3'>{events.map(item => <li key={item.span_id} className={`rounded-md border p-4 ${item.kind === 'attempt' ? 'ml-4 border-dashed bg-muted/15 sm:ml-8' : ''}`}>
      <div className='flex flex-wrap items-center justify-between gap-3'><p className='text-sm font-medium'>{item.pack && `${item.pack} · `}{item.operation || kindLabels[item.kind]}{item.attempt ? ` · 第 ${item.attempt} 次请求` : ''}</p><Badge variant='outline'>{outcomeLabels[item.outcome] || item.outcome}</Badge></div>
      <p className='mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground'><span>{kindLabels[item.kind] || item.kind}</span><span>{formatTimestamp(item.started_at)}</span><span>{item.latency_ms} ms</span>{item.http_status > 0 && <span>HTTP {item.http_status}</span>}{item.resource_state && <span>资源状态：{item.resource_state}</span>}</p>
      {item.kind === 'request' && <p className='mt-3 text-sm'>{item.cost ? `$${item.cost.amount_usd.toFixed(6)} · ${item.cost.status === 'reported' ? '供应商返回费用' : item.cost.status === 'estimated' ? '估算费用' : item.cost.status === 'partial' ? '部分计价' : '按已知价格计量'}` : '费用未知'}</p>}
      {!!item.units?.length && <div className='mt-3 space-y-1 text-sm'>{item.units.map((unit, index) => <p key={index}>{unit.quantity} {unit.unit}<span className='ml-2 text-muted-foreground'>{unit.source === 'request' ? '请求参数，非实际结算' : '供应商返回'} · {unit.mode === 'snapshot' ? '资源快照，不重复累加' : '本次记录'}</span></p>)}</div>}
      {item.job_id && <p className='mt-2 break-all text-xs text-muted-foreground'>任务 {item.job_id}</p>}
      <details className='mt-3 text-xs text-muted-foreground'><summary className='cursor-pointer'>身份与版本记录</summary><div className='mt-2 space-y-1 break-all'><p>{agentName(item.token_id)} · 身份 {item.token_id}</p><p>记录 {item.span_id}{item.parent_span_id && ` · 父记录 ${item.parent_span_id}`}</p><p>契约 {item.contract_digest || '未记录'}</p><p>能力包 {item.grant_revisions?.join(', ') || '沿用身份原有策略'}</p></div></details>
    </li>)}</ol>}
    {query.data && query.data.items.length === 0 && <EmptyState icon={Waypoints} title={task ? '没有这个任务的调用记录' : '还没有服务调用记录'} description='Agent 的调用会自动记录。传入任务标识后，可以把多次服务调用关联到同一件事。' />}
    {query.data && query.data.total > 0 && <nav aria-label='调用记录分页' className='flex flex-wrap items-center justify-end gap-3 text-sm'><span>第 {page} 页 · 共 {query.data.total} 条记录</span><Button variant='outline' disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</Button><Button variant='outline' disabled={page * 50 >= query.data.total} onClick={() => setPage(page + 1)}>下一页</Button></nav>}
  </div>
}

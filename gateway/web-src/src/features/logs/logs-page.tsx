import { Braces, Clock3, ScrollText, Search } from 'lucide-react'
import { useMemo, useState } from 'react'

import { EmptyState } from '@/components/empty-state'
import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { formatTimestamp } from '@/lib/api'
import type { AgentUsage, RequestLog } from '@/lib/types'

function logModel(log: RequestLog): string {
  return log.model_name || log.model || '—'
}

function logContent(log: RequestLog): string {
  if (log.content) return log.content
  if (log.type !== undefined) return `请求类型 ${String(log.type)}`
  return '网关请求'
}

function logTokens(log: RequestLog) {
  return log.total_tokens || (log.prompt_tokens || 0) + (log.completion_tokens || 0)
}

export function LogsPage(props: { logs: RequestLog[]; agents?: AgentUsage[]; agentId?: string; query?: string; onFilterChange?: (query: string, agentId: string) => void; page?: number; total?: number; onPageChange?: (page: number) => void }) {
  const [query, setQuery] = useState(props.query || '')
  const [selected, setSelected] = useState<RequestLog | null>(null)
  const filtered = useMemo(() => {
    if (props.onFilterChange) return props.logs
    const normalized = query.trim().toLocaleLowerCase()
    if (!normalized) return props.logs
    return props.logs.filter((log) =>
      [logModel(log), logContent(log), log.channel_name, log.username, log.token_name, log.agent_code, log.agent_name]
        .filter(Boolean)
        .join(' ')
        .toLocaleLowerCase()
        .includes(normalized)
    )
  }, [props.logs, props.onFilterChange, query])

  return (
    <div className='space-y-6'>
      <SectionHeader
        title='最近请求'
      />

      <form className='flex flex-wrap items-center gap-3' onSubmit={event => { event.preventDefault(); props.onFilterChange?.(query.trim(), props.agentId || '') }}>
      <div className='relative min-w-64 flex-1'>
        <Search
          aria-hidden='true'
          className='pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground'
        />
        <Input
          type='search'
          aria-label='搜索请求日志'
          placeholder='搜索全部日志：Agent、模型、渠道或内容…'
          className='pl-10'
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>
      <select aria-label='筛选 Agent' className='h-10 max-w-full rounded-md border bg-background px-3 text-sm' value={props.agentId || ''} onChange={event => props.onFilterChange?.(query.trim(), event.target.value)}>
        <option value=''>全部 Agent</option>
        {(props.agents || []).map(agent => <option key={agent.token_id} value={agent.token_id}>{agent.agent_name || agent.token_name} · #{agent.token_id}{agent.revoked ? '（已撤销）' : ''}</option>)}
      </select>
      <Button type='submit' variant='outline'>搜索</Button>
      </form>

      <section className='border-y'>
        {filtered.length ? (
          <div>
            <div className='hidden grid-cols-[9rem_minmax(8rem,0.7fr)_minmax(10rem,1fr)_minmax(10rem,1fr)_7rem] border-b bg-muted/35 px-5 py-3 text-xs font-medium text-muted-foreground md:grid'>
              <span>时间</span>
              <span>Agent</span>
              <span>模型 / 渠道</span>
              <span>内容</span>
              <span className='text-right'>详情</span>
            </div>
            <div className='divide-y'>
              {filtered.map((log, index) => (
                <div
                  key={log.id ?? `${String(log.created_time)}-${index}`}
                  className='grid gap-3 px-5 py-4 transition-colors hover:bg-muted/25 md:grid-cols-[9rem_minmax(8rem,0.7fr)_minmax(10rem,1fr)_minmax(10rem,1fr)_7rem] md:items-center'
                >
                  <div className='flex items-center gap-2 text-xs text-muted-foreground'>
                    <Clock3 aria-hidden='true' className='size-3.5' />
                    {formatTimestamp(log.created_at || log.created_time)}
                  </div>
                  <div className='min-w-0 text-xs'><p>{log.agent_name || log.token_name || '未归属'}</p><p className='truncate text-[11px] text-muted-foreground'>{log.agent_code || (log.token_id ? `Token #${log.token_id}` : '')}</p>{log.revoked ? <Badge variant='secondary'>已撤销</Badge> : null}</div>
                  <div className='min-w-0'>
                    <code className='block truncate text-xs font-medium'>{logModel(log)}</code>
                    {log.channel_name ? (
                      <p className='truncate text-[11px] text-muted-foreground'>{log.channel_name}</p>
                    ) : null}
                  </div>
                  <div className='text-xs leading-5'><p className='line-clamp-2 text-muted-foreground'>{logContent(log)}</p><p>{logTokens(log).toLocaleString('zh-CN')} Token · {log.cost_status === 'reported' || log.cost_status === 'confirmed' ? `$${Number(log.cost_usd || 0).toFixed(6)}` : '费用未知'}</p></div>
                  <Button variant='outline' size='sm' className='w-fit md:justify-self-end' onClick={() => setSelected(log)}>
                    <Braces aria-hidden='true' />
                    JSON
                  </Button>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <EmptyState
            icon={query || props.agentId ? Search : ScrollText}
            title={query || props.agentId ? '没有匹配的日志' : '还没有转发请求'}
            description={
              query || props.agentId
                ? '换一个 Agent、模型名、渠道名或内容关键词。'
                : '模型渠道的请求显示在这里；完整服务账单可从 Token 管理的消费明细查看。'
            }
          />
        )}
      </section>

      {props.onPageChange ? <nav aria-label='日志分页' className='flex items-center justify-end gap-3 text-sm'><span>第 {props.page || 1} 页 · 共 {props.total || 0} 条</span><Button variant='outline' disabled={(props.page || 1) <= 1} onClick={() => props.onPageChange?.((props.page || 1) - 1)}>上一页</Button><Button variant='outline' disabled={(props.page || 1) * 50 >= (props.total || 0)} onClick={() => props.onPageChange?.((props.page || 1) + 1)}>下一页</Button></nav> : null}

      <Dialog open={Boolean(selected)} onOpenChange={(open) => !open && setSelected(null)}>
        <DialogContent className='max-w-2xl'>
          <DialogHeader>
            <DialogTitle>请求记录</DialogTitle>
            <DialogDescription>
              {selected ? `${formatTimestamp(selected.created_at || selected.created_time)} · ${logModel(selected)}` : ''}
            </DialogDescription>
          </DialogHeader>
          {selected ? (
            <>
              <div className='flex flex-wrap gap-2'>
                <Badge variant='outline'>{selected.agent_name || selected.token_name || '未归属'}</Badge>
                <Badge variant='outline'>{logModel(selected)}</Badge>
                {selected.channel_name ? <Badge variant='outline'>{selected.channel_name}</Badge> : null}
                {selected.elapsed_time !== undefined ? (
                  <Badge variant='outline'>{selected.elapsed_time} ms</Badge>
                ) : null}
                {logTokens(selected) ? <Badge variant='outline'>{logTokens(selected).toLocaleString('zh-CN')} Token</Badge> : null}
                {selected.cached_input_tokens ? <Badge variant='outline'>缓存读 {selected.cached_input_tokens.toLocaleString('zh-CN')}</Badge> : null}
                {selected.reasoning_tokens ? <Badge variant='outline'>推理 {selected.reasoning_tokens.toLocaleString('zh-CN')}</Badge> : null}
                {selected.cost_status === 'reported' ? <Badge variant='outline'>上游 ${Number(selected.cost_usd || 0).toFixed(6)}</Badge> : null}
              </div>
              <pre className='max-h-[58svh] overflow-auto border bg-slate-950 p-4 text-xs leading-5 text-slate-100'>
                {JSON.stringify(selected, null, 2)}
              </pre>
            </>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}

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
import type { RequestLog } from '@/lib/types'

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

export function LogsPage(props: { logs: RequestLog[]; page?: number; total?: number; onPageChange?: (page: number) => void }) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<RequestLog | null>(null)
  const filtered = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase()
    if (!normalized) return props.logs
    return props.logs.filter((log) =>
      [logModel(log), logContent(log), log.channel_name, log.username]
        .filter(Boolean)
        .join(' ')
        .toLocaleLowerCase()
        .includes(normalized)
    )
  }, [props.logs, query])

  return (
    <div className='space-y-6'>
      <SectionHeader
        title='最近请求'
      />

      <div className='relative max-w-md'>
        <Search
          aria-hidden='true'
          className='pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground'
        />
        <Input
          type='search'
          aria-label='搜索请求日志'
          placeholder='筛选本页模型、渠道或内容…'
          className='pl-10'
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>

      <section className='border-y'>
        {filtered.length ? (
          <div>
            <div className='hidden grid-cols-[10rem_minmax(11rem,0.8fr)_minmax(16rem,1.4fr)_7rem] border-b bg-muted/35 px-5 py-3 text-xs font-medium text-muted-foreground md:grid'>
              <span>时间</span>
              <span>模型</span>
              <span>内容</span>
              <span className='text-right'>详情</span>
            </div>
            <div className='divide-y'>
              {filtered.map((log, index) => (
                <div
                  key={log.id ?? `${String(log.created_time)}-${index}`}
                  className='grid gap-3 px-5 py-4 transition-colors hover:bg-muted/25 md:grid-cols-[10rem_minmax(11rem,0.8fr)_minmax(16rem,1.4fr)_7rem] md:items-center'
                >
                  <div className='flex items-center gap-2 text-xs text-muted-foreground'>
                    <Clock3 aria-hidden='true' className='size-3.5' />
                    {formatTimestamp(log.created_at || log.created_time)}
                  </div>
                  <div className='min-w-0'>
                    <code className='block truncate text-xs font-medium'>{logModel(log)}</code>
                    {log.channel_name ? (
                      <p className='truncate text-[11px] text-muted-foreground'>{log.channel_name}</p>
                    ) : null}
                  </div>
                  <p className='line-clamp-2 text-xs leading-5 text-muted-foreground'>{logContent(log)}</p>
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
            icon={props.logs.length ? Search : ScrollText}
            title={props.logs.length ? '没有匹配的日志' : '还没有转发请求'}
            description={
              props.logs.length
                ? '换一个模型名、渠道名或内容关键词。'
                : '发起一次 /v1 或 Protocol Pack 调用后，活动会显示在这里。'
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

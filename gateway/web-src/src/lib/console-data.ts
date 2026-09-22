import { useQueries, useQueryClient } from '@tanstack/react-query'
import { adminRequest, ApiError } from '@/lib/api'
import type { AgentUsage, Analytics, Channel, LocalToken, MaintenanceAccess, Paginated, ProtocolDraft, ProtocolEvent, ProtocolRevision, ProtocolView, Provider, RequestLog, Summary, TokenPolicy, WorkflowJob } from '@/lib/types'

export type ConsoleData = {
  summary?: Summary
  analytics?: Analytics
  protocols: ProtocolView[]
  providers: Provider[]
  channels: Channel[]
  tokens: LocalToken[]
  logs: RequestLog[]
  logTotal: number
  policies: TokenPolicy[]
  maintenanceAccess?: MaintenanceAccess
  drafts: ProtocolDraft[]
  revisions: ProtocolRevision[]
  jobs: WorkflowJob[]
  events: ProtocolEvent[]
  agentUsage: AgentUsage[]
}

// These are small operator-owned collections. Load every page instead of
// silently hiding older channels/Agents; logs retain explicit pagination.
export async function adminCollection<T extends { id: number }>(path: string, token: string, signal?: AbortSignal): Promise<T[]> {
  const items = new Map<number, T>()
  for (let page = 1; ; page++) {
    const result = await adminRequest<Paginated<T>>(`${path}?page=${page}&page_size=100`, token, { signal })
    if (!result || !Array.isArray(result.items) || !Number.isFinite(result.total) || result.total < 0) throw new Error('列表响应格式错误')
    const previousSize = items.size
    for (const item of result.items) items.set(item.id, item)
    if (page * 100 >= result.total) return [...items.values()]
    if (items.size === previousSize) throw new Error('列表在加载时发生变化，请刷新重试')
  }
}

export const consoleSources = [
  ['summary', '运行概览', '/local/api/summary'],
  ['analytics', '用量统计', '/local/api/analytics'],
  ['protocols', '服务号池', '/local/api/protocols'],
  ['providers', '协议类型', '/local/api/providers'],
  ['channels', '模型渠道', '/local/api/channels'],
  ['tokens', 'Agent Token', '/local/api/tokens'],
  ['logs', '请求日志', '/local/api/logs'],
  ['policies', 'Token 策略', '/local/api/token-policies'],
  ['maintenanceAccess', '维护权限', '/local/api/maintenance-access'],
  ['drafts', '发布草稿', '/local/api/protocol-drafts'],
  ['revisions', '发布历史', '/local/api/protocols/history'],
  ['jobs', '异步任务', '/local/api/workflows/jobs'],
  ['events', '调用事件', '/local/api/protocol-events?limit=100'],
  ['agentUsage', 'Agent 用量', '/local/api/agent-usage'],
] as const
export type ConsoleKey = typeof consoleSources[number][0]
const requirements: Record<string, ConsoleKey[]> = {
  setup: [],
  overview: ['summary', 'analytics'],
  protocols: ['protocols', 'providers', 'channels'],
  editor: ['drafts', 'revisions'],
  tokens: ['tokens', 'policies', 'maintenanceAccess'],
  jobs: ['jobs'],
  logs: ['logs'],
}
const supplements: Record<string, ConsoleKey[]> = {
  setup: ['tokens'],
  overview: ['protocols', 'agentUsage'],
  tokens: ['agentUsage'],
  jobs: ['events'],
}

type QueryValue = { data?: unknown; error?: Error | null; isPending?: boolean }
export function collectConsole(values: QueryValue[]): ConsoleData {
  const data: ConsoleData = { protocols: [], providers: [], channels: [], tokens: [], logs: [], logTotal: 0, policies: [], drafts: [], revisions: [], jobs: [], events: [], agentUsage: [] }
  values.forEach((value, index) => {
    if (value.data === undefined) return
    const key = consoleSources[index][0]
    if (key === 'logs') {
      const page = value.data as Paginated<RequestLog>
      data.logs = page.items
      data.logTotal = page.total
    } else {
      Object.assign(data, { [key]: value.data })
    }
  })
  return data
}

export function useConsoleData(token: string, enabled: boolean, section: string, logPage: number) {
  const queryClient = useQueryClient()
  const needed = requirements[section] || requirements.overview
  const selected = [...needed, ...(supplements[section] || [])]
  const queries = useQueries({ queries: consoleSources.map(([key, , path]) => ({
    queryKey: ['console-data', key, ...(key === 'logs' ? [logPage] : [])],
    queryFn: ({ signal }) => key === 'channels' || key === 'tokens'
      ? adminCollection(path, token, signal)
      : adminRequest<unknown>(key === 'logs' ? `${path}?page=${logPage}&page_size=50` : path, token, { signal }),
    enabled: enabled && selected.includes(key),
    staleTime: 10_000,
    retry: false,
  })) })
  const blocking = queries.filter((_, i) => needed.includes(consoleSources[i][0]))
  const active = queries.filter((_, i) => enabled && selected.includes(consoleSources[i][0]))
  return {
    data: collectConsole(queries),
    isPending: enabled && blocking.some(query => query.isPending),
    error: blocking.find(query => query.error && query.data === undefined)?.error,
    isFetching: active.some(query => query.isFetching),
    authError: active.find(query => query.error instanceof ApiError && query.error.status === 401)?.error,
    warnings: queries.flatMap((query, i) => enabled && selected.includes(consoleSources[i][0]) && query.error ? [{ key: consoleSources[i][0], message: `${consoleSources[i][1]}暂时不可用：${query.error.message}` }] : []),
    async refetch() {
      // Other pages become stale, but only the visible page makes requests.
      await queryClient.invalidateQueries({ queryKey: ['console-data'], refetchType: 'none' })
      const values: QueryValue[] = [...queries]
      const pending = queries.map((query, i) => enabled && selected.includes(consoleSources[i][0])
        ? query.refetch().then(result => { values[i] = result; return result })
        : Promise.resolve(query))
      const results = await Promise.all(pending)
      return { data: collectConsole(values), error: results.find(query => query.error)?.error }
    },
  }
}

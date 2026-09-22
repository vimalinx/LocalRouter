// Synthetic documentation fixtures only. No live accounts, credentials or prices.
import type { AgentUsage, Analytics, AnalyticsService, LocalToken, ProtocolView } from '../src/lib/types'

export const capturedAt = '2026-09-05T12:00:00Z'
const end = Date.parse(capturedAt)
const hour = 3_600_000
const sum = (values: number[]) => values.reduce((total, value) => total + value, 0)

const agents = [
  ['product-dev', '产品开发 Agent', 'codex', '/workspace/atlas/app', 2400],
  ['code-review', '代码审查 Agent', 'claude-code', '/workspace/atlas/review', 1600],
  ['research', '资料研究 Agent', 'worker', '/workspace/atlas/research', 1200],
  ['ui-check', '界面验收 Agent', 'browser', '/workspace/atlas/web', 800],
  ['daily-brief', '每日简报 Agent', 'scheduler', '/workspace/atlas/brief', 600],
] as const

export const tokens: LocalToken[] = agents.map(([code, name, runtime, workspace], index) => ({
  id: index + 1, name: `${code}-token`, agent_code: code, agent_name: name,
  runtime, workspace, status: 1, unlimited_quota: true, group: 'default',
  key: 'demo-••••••••', created_time: (end - 7 * 24 * hour) / 1000,
}))
export const policies = tokens.map(token => ({
  token_id: token.id, capabilities: [], requests_per_minute: 60,
  daily_request_limit: agents[token.id - 1][4], max_in_flight: 4,
}))

function protocol(id: string, name: string, unit: string, accountCount: number, rate: number, operations: string[]): ProtocolView {
  const accounts = Array.from({ length: accountCount }, (_, index) => {
    const total = 10_000
    const remaining = [8240, 6375, 4820, 9130, 3560, 7420, 2180, 5670, 6910, 4300][index]
    const status = index === 7 ? 'cooldown' : index === 8 ? 'disabled' : index === 2 ? 'busy' : 'ready'
    return {
      ref: `demo-${id}-${index + 1}`, label: `${name} ${String(index + 1).padStart(2, '0')}`,
      status, status_label: { ready: '可调度', busy: '请求中', cooldown: '冷却中', disabled: '调度停用' }[status],
      in_flight: status === 'busy' ? 2 : 0, consecutive_failures: status === 'cooldown' ? 2 : 0,
      last_used: new Date(end - (status === 'busy' ? 1 : index * 137 + 18) * 1000).toISOString(),
      ...(status === 'cooldown' ? { cooldown_until: new Date(end + 45_000).toISOString() } : {}),
      quota: { tracked: true, status: 'confirmed', stale: false, total, remaining,
        used: total - remaining, used_percent: (total - remaining) / total * 100,
        unit, checked_at: capturedAt,
        reference_value: { status: 'confirmed', currency: 'USD', total: total * rate, remaining: remaining * rate, used: (total - remaining) * rate },
      },
    } as NonNullable<ProtocolView['pool_runtime']>['accounts'][number]
  })
  const count = (status: string) => accounts.filter(account => account.status === status).length
  const total = sum(accounts.map(account => account.quota.total!))
  const remaining = sum(accounts.map(account => account.quota.remaining!))
  return {
    id, name, description: `${name}演示服务，所有账号与价格均为模拟数据。`,
    mount: `/p/${id}`, workflow_mount: `/w/${id}`, enabled: true, ready: true, status: 'ready', status_label: '已就绪',
    routes: operations.map((operation, index) => ({ operation_id: operation, operation_key: `${id}.${operation}`,
      methods: ['POST'], path: `/${operation}`, summary: ['标准请求', '批量处理', '内容提取'][index], enabled: true })),
    docs: { html: '/docs', manifest: '/docs', markdown: '/docs', examples: '/docs' },
    pool: { mode: 'local', strategy: 'least-inflight', total: accounts.length, eligible: count('ready'), in_flight: 2 },
    pricing: { entries: operations.map(operation => ({ id: operation, label: operation, scope: 'operation',
      amount: rate * 1000, currency: 'USD', unit: `per-1k-${unit}`, status: 'confirmed',
      source_url: 'https://example.invalid/demo-pricing', source_type: 'documentation-demo', checked_at: capturedAt,
      note: '虚构演示价格，不代表供应商报价。' })) },
    pool_runtime: {
      ownership: 'local', status: count('cooldown') ? 'degraded' : 'ready', total: accounts.length,
      ready: count('ready'), busy: count('busy'), cooling: count('cooldown'), disabled: count('disabled'),
      expired: 0, balance_low: 0, balance_tracked: false, balance_empty: 0, in_flight: 2, accounts,
      quota: { status: 'confirmed', tracked_accounts: accounts.length, confirmed_accounts: accounts.length,
        unknown_accounts: 0, stale_accounts: 0, total, remaining, used: total - remaining,
        used_percent: (total - remaining) / total * 100, unit,
        reference_value: { status: 'confirmed', currency: 'USD', total: total * rate, remaining: remaining * rate, used: (total - remaining) * rate },
      },
    },
  }
}

export const protocols = [
  protocol('search', '网络检索', 'requests', 10, 0.002, ['search', 'batch', 'extract']),
  protocol('crawl', '网页抓取', 'pages', 6, 0.001, ['fetch', 'crawl']),
  protocol('voice', '语音服务', 'characters', 4, 0.0001, ['synthesize', 'transcribe']),
  protocol('image', '图像生成', 'images', 3, 0.02, ['generate', 'edit']),
]
export const channels = ['通用模型', '代码模型', '视觉模型'].map((name, index) => ({
  id: index + 1, name, type: 1, base_url: 'https://example.invalid/v1',
  models: ['atlas-chat,atlas-fast', 'forge-code', 'prism-vision'][index],
  status: 1, response_time: [842, 1260, 1785][index], weight: 10, priority: 0,
}))

const services = [
  ...channels.map(channel => ({ id: `channel:${channel.id}`, name: channel.name, kind: 'model-provider' as const, operations: 2 })),
  ...protocols.map(pack => ({ id: `protocol:${pack.id}`, name: pack.name, kind: 'protocol' as const, operations: pack.routes.length })),
]
const activity = [18, 13, 9, 6, 4, 3, 4, 6, 12, 22, 35, 44, 41, 33, 29, 39, 48, 53, 46, 39, 33, 29, 26, 21]
// One deterministic ledger feeds all three pages, including daily limits and charts.
const records = services.flatMap((service, serviceIndex) => Array.from({ length: 72 }, (_, bucket) => {
  const started = end - (72 - bucket) * hour
  const localHour = (new Date(started).getUTCHours() + 8) % 24
  const volume = Math.round(activity[localHour] * [2.1, 1.2, 0.65, 1.35, 0.6, 0.25, 0.15][serviceIndex] * (0.85 + (bucket * 7 % 9) / 30))
  return Array.from({ length: volume }, (_, index) => {
    const sequence = bucket * 113 + serviceIndex * 17 + index
    const failed = sequence % 97 === 0
    const input = serviceIndex < 3 && !failed ? 700 + sequence % 4600 : 0
    const output = serviceIndex < 3 && !failed ? 120 + sequence % 1100 : 0
    const cost = failed ? 0 : serviceIndex < 3 ? (input * 0.6 + output * 2.4) / 1e6 : [0, 0, 0, 0.002, 0.001, 0.004, 0.02][serviceIndex]
    return { service: service.id, serviceIndex, model: ['atlas-chat', 'forge-code', 'prism-vision'][serviceIndex] || '',
      agent: (sequence % 10 < 4 ? 1 : sequence % 10 < 6 ? 2 : sequence % 10 < 8 ? 3 : sequence % 10 === 8 ? 4 : 5),
      time: started + Math.floor((index + 1) / (volume + 1) * hour), bucket, failed, input, output, cost,
      cached: sequence % 3 ? Math.floor(input * 0.35) : 0, reasoning: serviceIndex === 1 ? Math.floor(output * 0.2) : 0,
      latency: serviceIndex < 3 ? 650 + sequence % 1900 : 180 + sequence % 1100 }
  })
}).flat())
type RecordRow = typeof records[number]
const totalCost = (rows: RecordRow[]) => sum(rows.map(row => row.cost))
const inputTokens = (rows: RecordRow[]) => sum(rows.map(row => row.input))
const outputTokens = (rows: RecordRow[]) => sum(rows.map(row => row.output))
const failedCount = (rows: RecordRow[]) => rows.filter(row => row.failed).length
const averageLatency = (rows: RecordRow[]) => Math.round(sum(rows.map(row => row.latency)) / rows.length)
const modelRows = records.filter(row => row.serviceIndex < 3)
const protocolRows = records.filter(row => row.serviceIndex >= 3)
const serviceUsage: AnalyticsService[] = services.map(service => {
  const rows = records.filter(row => row.service === service.id)
  const failed = failedCount(rows)
  return { ...service, status: 'ready', requests: rows.length, successful: rows.length - failed, failed,
    success_rate: (rows.length - failed) / rows.length * 100, average_latency_ms: averageLatency(rows),
    prompt_tokens: inputTokens(rows), completion_tokens: outputTokens(rows),
    cached_input_tokens: sum(rows.map(row => row.cached)), reasoning_tokens: sum(rows.map(row => row.reasoning)),
    tokenized_requests: service.kind === 'model-provider' ? rows.length - failed : 0,
    cost_usd: totalCost(rows), priced_requests: rows.length - failed, cost_status: 'confirmed',
    trend: Array.from({ length: 24 }, (_, index) => { const bucket = rows.filter(row => row.bucket === index + 48)
      return { requests: bucket.length, tokens: inputTokens(bucket) + outputTokens(bucket), cost_usd: totalCost(bucket) }
    }),
  }
})
export const agentUsage: AgentUsage[] = tokens.map(token => {
  const rows = records.filter(row => row.agent === token.id)
  return { ...token, token_name: token.name, registered: true, system: false,
    requests: rows.length, failed: failedCount(rows), successful: rows.length - failedCount(rows),
    today_requests: rows.filter(row => row.time >= Date.parse(`${capturedAt.slice(0, 10)}T00:00:00Z`)).length,
    prompt_tokens: inputTokens(rows), completion_tokens: outputTokens(rows), cost_usd: totalCost(rows), cost_status: 'measured',
    cached_input_tokens: sum(rows.map(row => row.cached)), reasoning_tokens: sum(rows.map(row => row.reasoning)),
    last_used_at: Math.floor(Math.max(...rows.map(row => row.time)) / 1000), ...policies[token.id - 1],
  }
})
export const analytics: Analytics = {
  generated_at: capturedAt, window: 'retained-all + 24h-trend',
  totals: { requests: records.length, model_requests: modelRows.length, protocol_requests: protocolRows.length,
    successful: records.length - failedCount(records), failed: failedCount(records),
    success_rate: (records.length - failedCount(records)) / records.length * 100,
    prompt_tokens: inputTokens(records), completion_tokens: outputTokens(records), total_tokens: inputTokens(records) + outputTokens(records),
    cached_input_tokens: sum(records.map(row => row.cached)), reasoning_tokens: sum(records.map(row => row.reasoning)),
    model_cost_usd: totalCost(modelRows), protocol_cost_usd: totalCost(protocolRows),
    protocol_priced_calls: protocolRows.length - failedCount(protocolRows), average_latency_ms: averageLatency(records),
    protocol_p95_latency_ms: [...protocolRows].sort((a, b) => a.latency - b.latency)[Math.floor(protocolRows.length * 0.95)].latency,
    active_services: services.length, active_operations: sum(services.map(service => service.operations)),
  },
  trend: Array.from({ length: 24 }, (_, index) => {
    const rows = records.filter(row => row.bucket === index + 48)
    return { started_at: new Date(end - (24 - index) * hour).toISOString(),
      model_requests: rows.filter(row => row.serviceIndex < 3).length, protocol_requests: rows.filter(row => row.serviceIndex >= 3).length,
      failed: failedCount(rows), tokens: inputTokens(rows) + outputTokens(rows), cost_usd: totalCost(rows) }
  }),
  services: serviceUsage,
  models: ['atlas-chat', 'forge-code', 'prism-vision'].map(name => {
    const rows = records.filter(row => row.model === name)
    return { name, requests: rows.length, prompt_tokens: inputTokens(rows), completion_tokens: outputTokens(rows),
      cached_input_tokens: sum(rows.map(row => row.cached)), reasoning_tokens: sum(rows.map(row => row.reasoning)),
      cost_usd: totalCost(rows), cost_status: 'confirmed' }
  }),
}

export const logs = records.filter(row => row.model).slice(-20).reverse().map((row, index) => ({
  id: index + 1, created_time: Math.floor(row.time / 1000), model_name: row.model,
  channel_name: services[row.serviceIndex].name, username: tokens[row.agent - 1].agent_name,
  type: row.failed ? 5 : 2, elapsed_time: row.latency / 1000,
  prompt_tokens: row.input, completion_tokens: row.output, total_tokens: row.input + row.output,
  cost_usd: row.cost, cost_status: 'confirmed',
}))

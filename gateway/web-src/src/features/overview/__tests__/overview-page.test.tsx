import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { OverviewPage } from '@/features/overview/overview-page'
import type { Analytics, ProtocolView, Summary } from '@/lib/types'

const summary: Summary = {
  channels: 1,
  tokens: 2,
  listen: '127.0.0.1:8317',
  admin_token_file: 'data/admin-token',
  api_token_file: 'data/api-token',
  database_path: 'data/local.db',
  config_dir: 'config',
  state_dir: 'state',
  cache_dir: 'cache',
  protocol_dir: 'protocols',
  engine: 'localrouter-native',
  protocols: 1,
  protocols_ready: 1,
  billing: 'disabled',
  oauth: 'external',
}

const protocol: ProtocolView = {
  id: 'search',
  name: 'Search API',
  description: 'Search',
  mount: '/p/search',
  workflow_mount: '/w/search',
  enabled: true,
  ready: true,
  status: 'ready',
  status_label: '已就绪',
  routes: [{ operation_id: 'query', methods: ['POST'], path: '/query', summary: 'Query' }],
  docs: { html: '/docs/search', manifest: '/docs/search.json', markdown: '/docs/search.md', examples: '/docs/search/examples' },
  pool_runtime: {
    ownership: 'local', status: 'ready', total: 2, ready: 2, cooling: 0, disabled: 0, expired: 0,
    busy: 0, balance_low: 0, balance_tracked: true, balance_empty: 0, in_flight: 0, accounts: [],
    quota: {
      status: 'confirmed', tracked_accounts: 2, confirmed_accounts: 2, unknown_accounts: 0, stale_accounts: 0,
      reference_value: { status: 'confirmed', currency: 'USD', total: 30, remaining: 25, used: 5 },
    },
  },
}

const analytics: Analytics = {
  generated_at: '2026-08-31T08:00:00Z',
  window: 'retained-all + 24h-trend',
  totals: {
    requests: 12, model_requests: 10, protocol_requests: 2, successful: 11, failed: 1, success_rate: 91.7,
    prompt_tokens: 800, completion_tokens: 200, total_tokens: 1000, model_cost_usd: 0.2,
    protocol_cost_usd: 0.1, protocol_priced_calls: 1, average_latency_ms: 120, protocol_p95_latency_ms: 220,
    active_services: 2, active_operations: 3,
  },
  trend: Array.from({ length: 24 }, (_, index) => ({
    started_at: new Date(Date.UTC(2026, 7, 30, 9 + index)).toISOString(),
    model_requests: index === 23 ? 3 : 0,
    protocol_requests: index === 23 ? 1 : 0,
    failed: 0,
    tokens: index === 23 ? 400 : 0,
    cost_usd: index === 23 ? 0.03 : 0,
  })),
  services: [
    {
      id: 'channel:1', name: 'OpenAI compatible', kind: 'model-provider', status: 'ready', requests: 10,
      successful: 10, failed: 0, success_rate: 100, average_latency_ms: 100, operations: 2,
      prompt_tokens: 800, completion_tokens: 200, cost_usd: 0.2, cost_status: 'measured',
      trend: Array.from({ length: 24 }, (_, index) => index === 23 ? { requests: 3, tokens: 400, cost_usd: 0.02 } : { requests: 0, tokens: 0, cost_usd: 0 }),
    },
    {
      id: 'protocol:search', name: 'Search API', kind: 'protocol', status: 'ready', requests: 2,
      successful: 1, failed: 1, success_rate: 50, average_latency_ms: 220, operations: 1,
      cost_usd: 0.1, priced_requests: 1, cost_status: 'partial',
      trend: Array.from({ length: 24 }, (_, index) => index === 23 ? { requests: 1, tokens: 0, cost_usd: 0.01 } : { requests: 0, tokens: 0, cost_usd: 0 }),
    },
  ],
  models: [{ name: 'gpt-test', requests: 10, prompt_tokens: 800, completion_tokens: 200, cost_usd: 0.2 }],
}

describe('OverviewPage', () => {
  it('shows model and service API activity without mixing pool value into spend', async () => {
    const user = userEvent.setup()
    render(<OverviewPage summary={summary} analytics={analytics} protocols={[protocol]} onChangeAdminToken={vi.fn()} />)

    expect(screen.getByRole('heading', { name: '运行概览' })).toBeInTheDocument()
    expect(screen.getByText('10 模型 · 2 服务 API')).toBeInTheDocument()
    expect(screen.getAllByText('OpenAI compatible').length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByText('Search API').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('$25.00')).toBeInTheDocument()
    expect(screen.getByText('$0.10 部分')).toBeInTheDocument()
    expect(screen.getByText(/号池参考余额是当前可用资源估值/)).toBeInTheDocument()
    expect(screen.getByLabelText('供应商图例')).toHaveTextContent('OpenAI compatible')
    expect(screen.getByLabelText('供应商图例')).toHaveTextContent('Search API')

    await user.click(screen.getByRole('button', { name: '已确认成本' }))
    expect(screen.getByRole('img', { name: '过去 24 小时成本趋势' })).toBeInTheDocument()
    expect(screen.getByText('$0.03')).toBeInTheDocument()
  })

  it('renders explicit empty states for a fresh gateway', () => {
    const empty = {
      ...analytics,
      totals: Object.fromEntries(Object.keys(analytics.totals).map((key) => [key, 0])) as unknown as Analytics['totals'],
      services: [],
      models: [],
    }
    render(<OverviewPage summary={{ ...summary, channels: 0, protocols: 0 }} analytics={empty} protocols={[]} onChangeAdminToken={vi.fn()} />)
    expect(screen.getByText('还没有可统计的服务。')).toBeInTheDocument()
    expect(screen.getByText('还没有模型调用记录。')).toBeInTheDocument()
    expect(screen.getByText('未接入')).toBeInTheDocument()
  })
})

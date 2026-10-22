import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentBillingDialog, BudgetSummary, usdInputMicros } from '../agent-billing'
import { TokensPage } from '../tokens-page'
import { adminRequest } from '@/lib/api'
import type { AgentBudget, AgentUsage } from '@/lib/types'

vi.mock('@/lib/api', () => ({ adminRequest: vi.fn(), formatTimestamp: () => '09/23' }))

const budget: AgentBudget = { daily_limit_usd_micros: 1000000, monthly_limit_usd_micros: 20000000, request_reserve_usd_micros: 250000, daily_spent_usd_micros: 500000, monthly_spent_usd_micros: 500000, held_usd_micros: 250000, pending_requests: 1, unpriced_requests: 0, unreconciled_overruns: 0, daily_remaining_usd_micros: 250000, monthly_remaining_usd_micros: 19250000, period_timezone: 'UTC' }

describe('Agent billing', () => {
  beforeEach(() => vi.clearAllMocks())
  it('keeps exact micro-dollar input and rejects silent rounding or invalid values', () => {
    expect(usdInputMicros('0.000001')).toBe(1)
    expect(usdInputMicros('20.25')).toBe(20250000)
    expect(usdInputMicros('')).toBe(0)
    for (const invalid of ['NaN', '-1', '0.0000001', '1000001']) expect(() => usdInputMicros(invalid)).toThrow()
  })
  it('shows available money after held requests, with an explicit overrun pause', () => {
    render(<BudgetSummary budget={{ ...budget, unreconciled_overruns: 1 }} />)
    expect(screen.getByText(/日预算：\$0.50 \/ \$1.00 · 可用 \$0.25/)).toBeVisible()
    expect(screen.getByText(/预留 \$0.25/)).toBeVisible()
    expect(screen.getByText(/已暂停新请求/)).toBeVisible()
  })
  it('keeps revoked usage visible without offering credential or access controls', () => {
    const usage = { token_id: 8, token_name: 'old-token', agent_name: 'Old Agent', agent_code: 'old-agent', revoked: true, requests: 2, prompt_tokens: 100, completion_tokens: 20, cost_usd: 0.03, cost_status: 'reported', budget } as AgentUsage
    render(<TokensPage adminToken='' tokens={[]} usage={[usage]} maintenanceAccess={{ agent_tokens_enabled: false, default_auth: 'admin', admin_header: 'X-Local-Admin', agent_auth: 'bearer', agent_capability: 'localrouter.maintain', service_tokens: 'call-only', maintenance_tokens: 'maintenance-only' }} apiTokenFile='' onChanged={vi.fn()} />)
    expect(screen.getByRole('region', { name: '已撤销 Agent 的历史用量' })).toHaveTextContent('Old Agent')
    expect(screen.getByRole('button', { name: '消费明细 old-token' })).toBeVisible()
    expect(screen.queryByRole('button', { name: '显示令牌 old-token' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑 Agent Old Agent' })).not.toBeInTheDocument()
  })
  it('paginates per-Agent billing and reconciles unknown cost with an explicit amount and reason', async () => {
    const user = userEvent.setup()
    vi.mocked(adminRequest).mockResolvedValue({ budget, items: [{ id: 'receipt-1', token_id: 8, model: 'demo-model', service: 'Fixture channel', operation: 'chat', created_at: 1, state: 'unknown', reserved_usd_micros: 250000, charged_usd_micros: 0, source: '', usage: { total_tokens: 120 } }], total: 21 } as never)
    const onChanged = vi.fn().mockResolvedValue(undefined)
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AgentBillingDialog agent={{ id: 8, name: 'Old Agent' }} adminToken='' onClose={vi.fn()} onChanged={onChanged} /></QueryClientProvider>)
    expect(await screen.findByText('demo-model')).toBeVisible()
    expect(screen.getByText('Fixture channel')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '核对账单 receipt-1' }))
    await user.type(screen.getByLabelText('核实金额（USD）'), '0.01')
    await user.type(screen.getByLabelText('核对依据'), 'provider invoice checked')
    await user.click(screen.getByRole('button', { name: '确认结算' }))
    await waitFor(() => expect(adminRequest).toHaveBeenCalledWith('/local/api/agent-billing/8/receipt-1/reconcile', '', { method: 'POST', body: JSON.stringify({ amount_usd_micros: 10000, reason: 'provider invoice checked' }) }))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: '下一页' }))
    await waitFor(() => expect(adminRequest).toHaveBeenCalledWith('/local/api/agent-billing/8?page=2&page_size=20', '', expect.anything()))
  })
})

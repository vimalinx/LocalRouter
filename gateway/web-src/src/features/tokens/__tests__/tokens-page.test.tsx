import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { TokensPage } from '@/features/tokens/tokens-page'
import { adminRequest } from '@/lib/api'
import type { LocalToken } from '@/lib/types'

vi.mock('@/features/tokens/identity-requests', () => ({ IdentityRequests: () => null }))

vi.mock('@/lib/api', () => ({
  adminRequest: vi.fn(),
  formatTimestamp: () => '—',
}))

const defaultToken: LocalToken = {
  id: 1,
  name: 'LocalRouter default',
  agent_code: 'localrouter-system',
  agent_name: 'LocalRouter system',
  workspace: 'operator',
  runtime: 'bootstrap',
  status: 1,
  unlimited_quota: true,
  group: 'default',
}

describe('TokensPage issuance', () => {
  beforeEach(() => vi.mocked(adminRequest).mockReset())

  it('makes default and legacy tokens manageable without revealing secrets on load', async () => {
    const user = userEvent.setup()
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    const legacy = { ...defaultToken, id: 12, name: 'legacy-app', agent_code: '', agent_name: '', workspace: '', runtime: '' }
    vi.mocked(adminRequest).mockResolvedValue({ key: 'sk-fixture-only-token' } as never)
    render(<TokensPage adminToken='' tokens={[defaultToken, legacy]} usage={[]} maintenanceAccess={{ agent_tokens_enabled: false, default_auth: 'admin', admin_header: 'X-Local-Admin', agent_auth: 'bearer', agent_capability: 'localrouter.maintain', service_tokens: 'call-only', maintenance_tokens: 'maintenance-only' }} apiTokenFile='/fixture/token' lanListen='192.168.1.9:8318' onChanged={vi.fn()} />)
    expect(screen.getByRole('heading', { name: 'Token 管理' })).toBeVisible()
    expect(screen.getByText('legacy-app')).toBeVisible()
    expect(screen.getByText('http://192.168.1.9:8318/v1')).toBeVisible()
    expect(adminRequest).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '复制 Token' }))
    expect(clipboard).toHaveBeenCalledWith('sk-fixture-only-token')
    expect(screen.queryByText('sk-fixture-only-token')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '显示 Token' }))
    expect(screen.getByText('sk-fixture-only-token')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '隐藏 Token' }))
    expect(screen.queryByText('sk-fixture-only-token')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '撤销令牌 LocalRouter default' })).not.toBeInTheDocument()
  })

  it('preserves service scope and expiry when editing limits, and revokes without dropping the policy first', async () => {
    const user = userEvent.setup()
    const token: LocalToken = { ...defaultToken, id: 9, name: 'scoped', agent_code: 'scoped-agent', agent_name: 'Scoped Agent', workspace: '/fixture', expired_time: 1900000000 }
    const policy = { token_id: 9, surfaces: ['p'], packs: ['search'], operations: ['search.find'], models: ['chosen'], expires_at: 1900000000, daily_request_limit: 10 }
    vi.mocked(adminRequest).mockResolvedValue(undefined as never)
    render(<TokensPage adminToken='' tokens={[token]} usage={[]} policies={[policy]} maintenanceAccess={{ agent_tokens_enabled: false, default_auth: 'admin', admin_header: 'X-Local-Admin', agent_auth: 'bearer', agent_capability: 'localrouter.maintain', service_tokens: 'call-only', maintenance_tokens: 'maintenance-only' }} apiTokenFile='/fixture/token' onChanged={vi.fn().mockResolvedValue([token])} />)
    await user.click(screen.getByRole('button', { name: '编辑 Agent Scoped Agent' }))
    await user.clear(screen.getByLabelText('每日请求'))
    await user.type(screen.getByLabelText('每日请求'), '12')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const policyWrite = vi.mocked(adminRequest).mock.calls.find(([path]) => path === '/local/api/token-policies/9')!
    expect(JSON.parse(policyWrite[2]!.body as string)).toMatchObject({ ...policy, daily_request_limit: 12 })
    const identityWrite = vi.mocked(adminRequest).mock.calls.find(([path]) => path === '/local/api/tokens')!
    expect(JSON.parse(identityWrite[2]!.body as string).expired_time).toBe(1900000000)
    await user.click(screen.getByRole('button', { name: '撤销令牌 scoped' }))
    await user.click(screen.getByRole('button', { name: '确认撤销' }))
    await waitFor(() => expect(adminRequest).toHaveBeenCalledWith('/local/api/tokens/9', '', { method: 'DELETE' }))
    expect(vi.mocked(adminRequest).mock.calls.filter(([, , init]) => init?.method === 'DELETE').map(([path]) => path)).toEqual(['/local/api/tokens/9'])
  })

  it('registers an Agent, binds its workspace and limits, then reveals the new value once', async () => {
    const user = userEvent.setup()
    const issued: LocalToken = {
      ...defaultToken, id: 2, name: 'video-agent-token', agent_code: 'video-agent-007',
      agent_name: 'Video Agent', workspace: '/workspace/video', runtime: 'codex',
    }
    const onChanged = vi.fn().mockResolvedValue([defaultToken, issued])
    vi.mocked(adminRequest)
      .mockResolvedValueOnce({ id: 2 } as never)
      .mockResolvedValueOnce(undefined as never)
      .mockResolvedValueOnce({ key: 'issued-token-value' } as never)

    render(
      <TokensPage
        adminToken='admin-test'
        tokens={[defaultToken]}
        usage={[]}
        maintenanceAccess={{
          agent_tokens_enabled: false,
          default_auth: 'admin',
          admin_header: 'X-Local-Admin',
          agent_auth: 'bearer',
          agent_capability: 'localrouter.maintain',
          service_tokens: 'call-only',
          maintenance_tokens: 'maintenance-only',
        }}
        apiTokenFile='/protected/api-token'
        onChanged={onChanged}
      />
    )

    await user.click(screen.getByRole('button', { name: '创建 Token' }))
    await user.type(screen.getByLabelText('Agent 编码'), 'video-agent-007')
    await user.type(screen.getByLabelText('Agent 名称'), 'Video Agent')
    await user.type(screen.getByLabelText('工作区'), '/workspace/video')
    await user.type(screen.getByLabelText('Token 名称'), 'video-agent-token')
    await user.type(screen.getByLabelText('每日请求'), '500')
    await user.type(screen.getByLabelText('每月预算（USD）'), '20')
    await user.type(screen.getByLabelText('单次预留（USD）'), '0.25')
    await user.click(screen.getByRole('button', { name: '创建并签发' }))

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2))
    expect(adminRequest).toHaveBeenCalledWith(
      '/local/api/tokens',
      'admin-test',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          name: 'video-agent-token', agent_code: 'video-agent-007', agent_name: 'Video Agent',
          workspace: '/workspace/video', runtime: 'codex', expired_time: -1,
        }),
      })
    )
    await waitFor(() => expect(adminRequest).toHaveBeenCalledWith(
      '/local/api/token-policies/2',
      'admin-test',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ capabilities: [], requests_per_minute: 0, daily_request_limit: 500, max_in_flight: 0, daily_budget_usd_micros: 0, monthly_budget_usd_micros: 20000000, request_reserve_usd_micros: 250000 }) })
    ))
    await waitFor(() => expect(adminRequest).toHaveBeenCalledWith(
      '/local/api/tokens/2/reveal',
      'admin-test',
      { method: 'POST' }
    ))
  })

  it('keeps token rows stacked through tablet width before using the desktop grid', () => {
    const registered = { ...defaultToken, id: 2, name: 'build-agent', agent_code: 'build-agent', agent_name: 'Build Agent', workspace: '/workspace/build', runtime: 'codex' }
    render(
      <TokensPage
        adminToken='admin-test'
        tokens={[defaultToken, registered]}
        usage={[]}
        maintenanceAccess={{
          agent_tokens_enabled: false,
          default_auth: 'admin',
          admin_header: 'X-Local-Admin',
          agent_auth: 'bearer',
          agent_capability: 'localrouter.maintain',
          service_tokens: 'call-only',
          maintenance_tokens: 'maintenance-only',
        }}
        apiTokenFile='/protected/api-token'
        onChanged={vi.fn()}
      />
    )

    expect(screen.queryByText('localrouter-system · bootstrap')).not.toBeInTheDocument()
    const row = screen.getByText('build-agent · codex').closest('article')
    expect(row).toHaveClass('xl:grid-cols-[minmax(13rem,1fr)_minmax(14rem,1.1fr)_minmax(17rem,1.15fr)_auto]')
    expect(row).not.toHaveClass('md:grid-cols-[minmax(13rem,1fr)_minmax(14rem,1.1fr)_minmax(17rem,1.15fr)_auto]')
    expect(screen.getByText('未接入价格')).toBeVisible()
  })

  it('opens the optional maintenance controls and preserves password-style key editing', async () => {
    const user = userEvent.setup()
    const maintainer = { ...defaultToken, id: 8, name: 'Agent maintenance', key: 'sk-abcd****wxyz', agent_code: 'localrouter-maintainer', agent_name: 'LocalRouter 维护 Agent', workspace: 'operator', runtime: 'maintenance' }
    vi.mocked(adminRequest).mockResolvedValue({ updated: true } as never)
    render(
      <TokensPage
        adminToken='admin-test'
        tokens={[defaultToken, maintainer]}
        usage={[]}
        policies={[{ token_id: 8, capabilities: ['localrouter.maintain'] }]}
        maintenanceAccess={{ agent_tokens_enabled: true, default_auth: 'admin', admin_header: 'X-Local-Admin', agent_auth: 'bearer', agent_capability: 'localrouter.maintain', service_tokens: 'call-only', maintenance_tokens: 'maintenance-only' }}
        apiTokenFile='/protected/api-token'
        onChanged={vi.fn().mockResolvedValue([defaultToken, maintainer])}
      />
    )

    await user.click(screen.getByText('Agent 维护', { exact: true }))
    const input = screen.getByLabelText('维护 Key')
    expect(input).toHaveAttribute('type', 'password')
    await user.click(screen.getByRole('button', { name: '生成随机维护 Key' }))
    expect(input).toHaveAttribute('type', 'text')
    expect((input as HTMLInputElement).value).toMatch(/^sk-[a-f0-9]{64}$/)
    await user.click(screen.getByRole('button', { name: /^保存$/ }))
    expect(adminRequest).toHaveBeenCalledWith('/local/api/tokens/8/key', 'admin-test', expect.objectContaining({ method: 'PUT' }))
  })
})

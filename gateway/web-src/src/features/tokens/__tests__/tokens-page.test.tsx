import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { TokensPage } from '@/features/tokens/tokens-page'
import { adminRequest } from '@/lib/api'
import type { LocalToken } from '@/lib/types'

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

  it('registers an Agent, binds its workspace and limits, then reveals the new value once', async () => {
    const user = userEvent.setup()
    const issued: LocalToken = {
      ...defaultToken, id: 2, name: 'video-agent-token', agent_code: 'video-agent-007',
      agent_name: 'Video Agent', workspace: '/workspace/video', runtime: 'codex',
    }
    const onChanged = vi.fn().mockResolvedValue([defaultToken, issued])
    vi.mocked(adminRequest)
      .mockResolvedValueOnce(undefined as never)
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

    await user.click(screen.getByRole('button', { name: '注册 Agent' }))
    await user.type(screen.getByLabelText('Agent 编码'), 'video-agent-007')
    await user.type(screen.getByLabelText('Agent 名称'), 'Video Agent')
    await user.type(screen.getByLabelText('工作区'), '/workspace/video')
    await user.type(screen.getByLabelText('Token 名称'), 'video-agent-token')
    await user.type(screen.getByLabelText('每日请求'), '500')
    await user.click(screen.getByRole('button', { name: '注册并签发' }))

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
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ capabilities: [], requests_per_minute: 0, daily_request_limit: 500, max_in_flight: 0 }) })
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

  it('expands password-style maintenance key controls only when maintenance is enabled', async () => {
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

    const input = screen.getByLabelText('维护 Key')
    expect(input).toHaveAttribute('type', 'password')
    await user.click(screen.getByRole('button', { name: '生成随机维护 Key' }))
    expect(input).toHaveAttribute('type', 'text')
    expect((input as HTMLInputElement).value).toMatch(/^sk-[a-f0-9]{64}$/)
    await user.click(screen.getByRole('button', { name: /^保存$/ }))
    expect(adminRequest).toHaveBeenCalledWith('/local/api/tokens/8/key', 'admin-test', expect.objectContaining({ method: 'PUT' }))
  })
})

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
  status: 1,
  unlimited_quota: true,
  group: 'default',
}

describe('TokensPage issuance', () => {
  beforeEach(() => vi.mocked(adminRequest).mockReset())

  it('issues a local token, refreshes the list, and reveals the new value once', async () => {
    const user = userEvent.setup()
    const issued: LocalToken = { ...defaultToken, id: 2, name: 'video-agent' }
    const onChanged = vi.fn().mockResolvedValue([defaultToken, issued])
    vi.mocked(adminRequest)
      .mockResolvedValueOnce(undefined as never)
      .mockResolvedValueOnce({ key: 'issued-token-value' } as never)

    render(
      <TokensPage
        adminToken='admin-test'
        tokens={[defaultToken]}
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

    await user.click(screen.getByRole('button', { name: '签发 Token' }))
    await user.type(screen.getByLabelText('名称'), 'video-agent')
    await user.click(screen.getByRole('button', { name: '签发 Token' }))

    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce())
    expect(adminRequest).toHaveBeenCalledWith(
      '/local/api/tokens',
      'admin-test',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ name: 'video-agent', expired_time: -1 }) })
    )
    await waitFor(() => expect(adminRequest).toHaveBeenCalledWith(
      '/local/api/tokens/2/reveal',
      'admin-test',
      { method: 'POST' }
    ))
  })

  it('keeps token rows stacked through tablet width before using the desktop grid', () => {
    render(
      <TokensPage
        adminToken='admin-test'
        tokens={[defaultToken]}
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

    const row = screen.getByText('#1 · 启用').parentElement?.parentElement
    expect(row).toHaveClass('lg:grid-cols-[minmax(11rem,0.7fr)_minmax(16rem,1.3fr)_10rem_auto]')
    expect(row).not.toHaveClass('md:grid-cols-[minmax(11rem,0.7fr)_minmax(16rem,1.3fr)_10rem_auto]')
  })
})

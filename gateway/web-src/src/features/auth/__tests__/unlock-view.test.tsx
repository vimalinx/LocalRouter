import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { UnlockView } from '@/features/auth/unlock-view'

describe('UnlockView', () => {
  it('keeps the license visible without adding historical provenance to the product UI', () => {
    render(<UnlockView onUnlock={vi.fn()} />)

    expect(screen.getAllByText('AGPL-3.0')).not.toHaveLength(0)
    expect(screen.queryByRole('link', { name: /历史来源/ })).not.toBeInTheDocument()
  })

  it('describes the token location without exposing a host filesystem path', () => {
    render(
      <UnlockView
        status={{
          success: true,
          mode: 'local-self-use',
          listen: '127.0.0.1:8317',
          admin_auth_enabled: true,
          admin_token_file: '/private-host/LocalRouter/data/admin-token',
          api_token_file: '/private-host/LocalRouter/data/api-token',
          database_path: '/private-host/LocalRouter/data/localrouter.db',
          protocol_dir: '/private-host/LocalRouter/protocols',
          state_dir: '/private-host/LocalRouter/state',
          cache_dir: '/private-host/LocalRouter/cache',
          path_layout: 'xdg-v1',
          engine: 'localrouter-native',
          oauth: 'external-maintainer',
        }}
        onUnlock={vi.fn()}
      />,
    )

    expect(screen.getByText(/XDG_DATA_HOME\/localrouter\/admin-token/)).toBeInTheDocument()
    expect(screen.queryByText(/\/private-host/)).not.toBeInTheDocument()
  })

  it('keeps the user on the form and exposes an accessible error when the token is empty', async () => {
    const user = userEvent.setup()
    const unlock = vi.fn()
    render(<UnlockView onUnlock={unlock} />)

    await user.click(screen.getByRole('button', { name: '进入控制台' }))

    expect(unlock).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('请输入本机管理密钥')
    expect(screen.getByLabelText('本机管理密钥')).toHaveAttribute('aria-invalid', 'true')
  })

  it('submits the entered token without writing it to localStorage', async () => {
    const user = userEvent.setup()
    const unlock = vi.fn().mockResolvedValue(undefined)
    const setItem = vi.fn()
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: { getItem: vi.fn(), setItem },
    })
    render(<UnlockView onUnlock={unlock} />)

    await user.type(screen.getByLabelText('本机管理密钥'), 'local-admin-value')
    await user.click(screen.getByRole('button', { name: '进入控制台' }))

    expect(unlock).toHaveBeenCalledWith('local-admin-value')
    expect(setItem).not.toHaveBeenCalled()
  })
})

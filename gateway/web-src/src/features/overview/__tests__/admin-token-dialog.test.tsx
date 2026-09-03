import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { AdminTokenDialog } from '@/features/overview/admin-token-dialog'

describe('AdminTokenDialog', () => {
  it('rejects weak or mismatched values without submitting', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<AdminTokenDialog enabled onChange={onChange} onSetEnabled={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '管理密码' }))
    await user.type(screen.getByLabelText('新密码'), 'short')
    await user.type(screen.getByLabelText('再次输入'), 'different')
    await user.click(screen.getByRole('button', { name: '更新密码' }))

    expect(screen.getByRole('alert')).toHaveTextContent('至少需要 16 个字符')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('submits matching values and never renders the token after success', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn().mockResolvedValue(undefined)
    render(<AdminTokenDialog enabled onChange={onChange} onSetEnabled={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '管理密码' }))
    await user.type(screen.getByLabelText('新密码'), 'custom-localrouter-login-2026')
    await user.type(screen.getByLabelText('再次输入'), 'custom-localrouter-login-2026')
    await user.click(screen.getByRole('button', { name: '更新密码' }))

    expect(onChange).toHaveBeenCalledWith('custom-localrouter-login-2026')
    expect(screen.queryByDisplayValue('custom-localrouter-login-2026')).not.toBeInTheDocument()
  })

  it('enables password protection with a custom password', async () => {
    const user = userEvent.setup()
    const onSetEnabled = vi.fn().mockResolvedValue(undefined)
    render(<AdminTokenDialog enabled={false} onChange={vi.fn()} onSetEnabled={onSetEnabled} />)

    await user.click(screen.getByRole('button', { name: '开启密码' }))
    await user.type(screen.getByLabelText('自定义密码'), 'custom-localrouter-login-2026')
    await user.type(screen.getByLabelText('再次输入'), 'custom-localrouter-login-2026')
    await user.click(screen.getByRole('button', { name: '开启并使用' }))

    expect(onSetEnabled).toHaveBeenCalledWith(true, 'custom-localrouter-login-2026')
  })

  it('can restore the default password-free mode', async () => {
    const user = userEvent.setup()
    const onSetEnabled = vi.fn().mockResolvedValue(undefined)
    render(<AdminTokenDialog enabled onChange={vi.fn()} onSetEnabled={onSetEnabled} />)

    await user.click(screen.getByRole('button', { name: '管理密码' }))
    await user.click(screen.getByRole('button', { name: '关闭密码保护' }))

    expect(onSetEnabled).toHaveBeenCalledWith(false)
  })
})

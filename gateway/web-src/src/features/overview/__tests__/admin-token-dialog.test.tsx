import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { AdminTokenDialog } from '@/features/overview/admin-token-dialog'

describe('AdminTokenDialog', () => {
  it('rejects weak or mismatched values without submitting', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<AdminTokenDialog onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: '更改登录密钥' }))
    await user.type(screen.getByLabelText('新登录密钥'), 'short')
    await user.type(screen.getByLabelText('再次输入'), 'different')
    await user.click(screen.getByRole('button', { name: '更新并继续使用' }))

    expect(screen.getByRole('alert')).toHaveTextContent('至少需要 16 个字符')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('submits matching values and never renders the token after success', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn().mockResolvedValue(undefined)
    render(<AdminTokenDialog onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: '更改登录密钥' }))
    await user.type(screen.getByLabelText('新登录密钥'), 'custom-localrouter-login-2026')
    await user.type(screen.getByLabelText('再次输入'), 'custom-localrouter-login-2026')
    await user.click(screen.getByRole('button', { name: '更新并继续使用' }))

    expect(onChange).toHaveBeenCalledWith('custom-localrouter-login-2026')
    expect(screen.queryByDisplayValue('custom-localrouter-login-2026')).not.toBeInTheDocument()
  })
})

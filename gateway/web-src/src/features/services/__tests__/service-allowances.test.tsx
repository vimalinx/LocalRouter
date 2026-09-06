import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { ServiceAllowances } from '../service-allowances'
import { adminRequest } from '@/lib/api'
vi.mock('@/lib/api', () => ({ adminRequest: vi.fn() }))
const rule = { enabled: false, mode: 'quota', unit: 'requests', period: 'month', limit: 0, revision: 0, approval_operations: [], denied_operations: [] }
const item = { service: 'test', name: '测试服务', rule, spent: 0, reserved: 0, remaining: 0, pending: [] }
beforeEach(() => vi.mocked(adminRequest).mockReset())
it('starts disabled and writes only after explicit human save', async () => {
  vi.mocked(adminRequest).mockResolvedValue([item])
  const user = userEvent.setup()
  render(<ServiceAllowances adminToken='' />)
  const enabled = await screen.findByRole('checkbox', { name: '启用此服务的自主使用限制' })
  expect(enabled).not.toBeChecked()
  expect(adminRequest).toHaveBeenCalledTimes(1)
  await user.click(enabled)
  await user.clear(screen.getByLabelText('总额度')); await user.type(screen.getByLabelText('总额度'), '10')
  await user.type(screen.getByLabelText('始终需要批准的操作'), 'delete, payment')
  expect(adminRequest).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(adminRequest).toHaveBeenCalledWith('/local/api/service-allowances/test', '', expect.objectContaining({ method: 'PUT' })))
  const init = vi.mocked(adminRequest).mock.calls[1][2]
  expect(JSON.parse(String(init?.body))).toMatchObject({ enabled: true, limit: 10, approval_operations: ['delete', 'payment'] })
})
it('reports persistence failure without showing success', async () => {
  vi.mocked(adminRequest).mockResolvedValueOnce([item]).mockRejectedValueOnce(new Error('磁盘写入失败'))
  const user = userEvent.setup(); render(<ServiceAllowances adminToken='' />)
  await user.click(await screen.findByRole('button', { name: '保存配置' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('磁盘写入失败')
  expect(screen.queryByText('规则已保存，额度功能关闭。')).not.toBeInTheDocument()
})
it('accepts fractional dollar limits without losing the decimal point', async () => {
  vi.mocked(adminRequest).mockResolvedValue([item])
  const user = userEvent.setup(); render(<ServiceAllowances adminToken='' />)
  await user.selectOptions(await screen.findByLabelText('计量单位'), 'usd_micros')
  await user.clear(screen.getByLabelText('总额度')); await user.type(screen.getByLabelText('总额度'), '0.25')
  await user.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(adminRequest).toHaveBeenCalledTimes(3))
  expect(JSON.parse(String(vi.mocked(adminRequest).mock.calls[1][2]?.body))).toMatchObject({ unit: 'usd_micros', limit: 250000 })
})

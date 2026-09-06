import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { IdentityRequests } from '../identity-requests'
import { adminRequest } from '@/lib/api'

vi.mock('@/lib/api', () => ({ adminRequest: vi.fn(), formatTimestamp: () => '明日' }))
beforeEach(() => vi.mocked(adminRequest).mockReset())
const request = {
  id: 'identity-fixture', digest: 'reviewed-fixture', state: 'pending', agent_code: 'agent-fixture',
  agent_name: '研究 Agent', workspace: '/fixture/research', runtime: 'fixture',
  policy: { packs: ['search'], operations: ['search.find'], models: ['chosen-model'], daily_request_limit: 50 },
}

it('shows the exact scope and approves its digest without requesting or revealing a credential', async () => {
  vi.mocked(adminRequest).mockResolvedValueOnce([request]).mockResolvedValueOnce({}).mockResolvedValueOnce([])
  const changed = vi.fn().mockResolvedValue(undefined)
  render(<IdentityRequests adminToken='' onChanged={changed} />)
  expect(await screen.findByText('search.find')).toBeVisible()
  expect(screen.getByText('chosen-model')).toBeVisible()
  expect(screen.getByText('/fixture/research')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '批准以上范围' }))
  await waitFor(() => expect(changed).toHaveBeenCalledOnce())
  expect(adminRequest).toHaveBeenNthCalledWith(2, '/local/api/identity-requests/identity-fixture/decision', '', {
    method: 'POST', body: JSON.stringify({ digest: 'reviewed-fixture', approve: true }),
  })
  expect(vi.mocked(adminRequest).mock.calls.some(([path]) => /claim|reveal/.test(path))).toBe(false)
})

it('keeps a stale approval visible with an actionable error and refresh control', async () => {
  vi.mocked(adminRequest).mockResolvedValueOnce([request]).mockRejectedValueOnce(new Error('申请已变化，请刷新'))
  render(<IdentityRequests adminToken='' onChanged={vi.fn()} />)
  await screen.findByText('search.find')
  await userEvent.click(screen.getByRole('button', { name: '批准以上范围' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('申请已变化，请刷新')
  expect(screen.getByText('search.find')).toBeVisible()
  expect(screen.getByRole('button', { name: '刷新申请' })).toBeEnabled()
})

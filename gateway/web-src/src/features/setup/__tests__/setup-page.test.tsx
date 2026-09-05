import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { SetupPage } from '../setup-page'
import { adminRequest } from '@/lib/api'
import type { WorkspaceData } from '../types'
vi.mock('@/lib/api', () => ({ adminRequest: vi.fn(), formatTimestamp: () => '09:00' }))
const data: WorkspaceData = { templates: [], bundles: [], grants: {}, delegations: {}, proposals: { total: 1, page: 1, page_size: 20, items: [{ id: 'proposal-one', owner_token_id: 7, agent_code: 'fixture', kind: 'bundle', reason: '需要受限研究权限', created_at: '', updated_at: '', state: 'awaiting_approval', digest: 'reviewed-digest', verification: 'contract-only', bundle: { id: 'research', name: '研究工具', revision: 'bundle-digest', members: [] } }] } }
function mount() { render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><SetupPage adminToken='' tokens={[]} /></QueryClientProvider>) }
beforeEach(() => vi.mocked(adminRequest).mockReset())
it('requires inspection and sends exactly the reviewed digest once', async () => {
 vi.mocked(adminRequest).mockResolvedValue(data)
 mount()
 await userEvent.click(await screen.findByRole('button', { name: '查看并授权' }))
 expect(screen.getByRole('dialog')).toHaveTextContent('空能力包')
 expect(vi.mocked(adminRequest).mock.calls.filter(call => call[2]?.method === 'POST')).toHaveLength(0)
 await userEvent.click(screen.getByRole('button', { name: '授权并执行这份方案' }))
 await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
 expect(vi.mocked(adminRequest).mock.calls.filter(call => call[2]?.method === 'POST')).toEqual([['/local/api/service-proposals/proposal-one/decision', '', { method: 'POST', body: JSON.stringify({ digest: 'reviewed-digest', decision: 'approve' }) }]])
})
it('keeps a rejected digest visible without retrying the decision', async () => {
 vi.mocked(adminRequest).mockImplementation(async (_path, _token, init) => { if (init?.method === 'POST') throw new Error('proposal changed'); return data as never })
 mount()
 await userEvent.click(await screen.findByRole('button', { name: '查看并授权' }))
 await userEvent.click(screen.getByRole('button', { name: '授权并执行这份方案' }))
 expect(await screen.findByRole('alert')).toHaveTextContent('proposal changed')
 expect(screen.getByRole('dialog')).toBeInTheDocument()
 expect(vi.mocked(adminRequest).mock.calls.filter(call => call[2]?.method === 'POST')).toHaveLength(1)
})

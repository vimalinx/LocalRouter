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
it('shows the complete effective scope and retained capabilities before approval', async () => {
 const reviewed = structuredClone(data)
 const proposal = reviewed.proposals.items[0]
 proposal.grant_policy = {
  base_digest: 'base-policy',
  before: { token_id: 7, setup_only: true, daily_request_limit: 3 },
  after: { token_id: 7, surfaces: ['p', 'mcp'], packs: ['new-service'], operations: ['new-service.chat', 'new-service.models'], daily_request_limit: 3 },
 }
 proposal.retained_bundle = { id: 'retained', name: '保留旧服务', revision: 'old-digest', members: [{ pack: 'old-service', operations: ['search'] }] }
 vi.mocked(adminRequest).mockResolvedValue(reviewed)
 mount()
 await userEvent.click(await screen.findByRole('button', { name: '查看并授权' }))
 expect(screen.getByRole('dialog')).toHaveTextContent('继续保留的既有服务')
 expect(screen.getByRole('dialog')).toHaveTextContent('old-service')
 expect(screen.getByRole('dialog')).toHaveTextContent('new-service.models')
 expect(screen.getByRole('dialog')).toHaveTextContent('批准后的完整 Token 范围')
 expect(vi.mocked(adminRequest).mock.calls.filter(call => call[2]?.method === 'POST')).toHaveLength(0)
})

it('reprepares a disabled legacy proposal without approving or asking for credentials', async () => {
 const legacy = structuredClone(data)
 const item = legacy.proposals.items[0]
 item.kind = 'connection'; item.grant_token_id = 7
 item.connection = { template_id: 'read-api', template_version: '1', definition: { id: 'new-service', name: '新服务', description: 'Fixture', enabled: false, base_url: 'https://service.example.invalid', auth: { type: 'bearer' }, routes: [{operation_id: 'search', methods: ['GET'], path: '/search', summary: '查询'}] } }
 const replacement = structuredClone(item)
 replacement.id = 'replacement'; replacement.digest = 'fresh-digest'; replacement.supersedes = item.id
 replacement.connection!.definition.enabled = true
 replacement.grant_policy = { base_digest: 'current', before: {token_id:7,setup_only:true}, after: {token_id:7,packs:['new-service'],operations:['new-service.search']} }
 vi.mocked(adminRequest).mockImplementation(async (_path, _token, init) => init?.method === 'POST' ? {proposal:replacement} as never : legacy as never)
 mount()
 await userEvent.click(await screen.findByRole('button', {name:'查看并授权'}))
 expect(screen.getByRole('button', {name:'授权并执行这份方案'})).toBeDisabled()
 expect(screen.queryByLabelText('服务密钥')).not.toBeInTheDocument()
 await userEvent.click(screen.getByRole('button', {name:'改为启用并重新准备'}))
 await waitFor(() => expect(screen.getByRole('dialog')).toHaveTextContent('此次须重新审阅授权'))
 expect(screen.getByRole('button', {name:'授权并执行这份方案'})).toBeEnabled()
 expect(screen.getByLabelText('服务密钥')).toHaveValue('')
 expect(vi.mocked(adminRequest).mock.calls.filter(call=>call[2]?.method==='POST')).toEqual([['/local/api/service-proposals/proposal-one/reprepare','',{method:'POST',body:JSON.stringify({digest:'reviewed-digest',enable:true})}]])
})

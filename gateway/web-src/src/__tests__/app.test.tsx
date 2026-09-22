import { act, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '@/app'
import { adminRequest, ApiError, publicRequest } from '@/lib/api'

vi.mock('@/lib/api', async () => ({ ...await vi.importActual('@/lib/api'), adminRequest: vi.fn(), publicRequest: vi.fn() }))
vi.mock('@/features/overview/overview-page', () => ({ OverviewPage: () => <div>已读取运行数据</div> }))

let client: QueryClient
beforeEach(() => {
  vi.resetAllMocks()
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })))
  vi.stubGlobal('localStorage', { getItem: vi.fn(), setItem: vi.fn() })
  window.history.replaceState(null, '', '/#overview')
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.mocked(publicRequest).mockResolvedValue({ success: true, admin_auth_enabled: false, listen: '127.0.0.1:8317' })
  vi.mocked(adminRequest).mockResolvedValue([])
})
afterEach(() => { client.clear(); vi.unstubAllGlobals() })

const start = () => render(<QueryClientProvider client={client}><App /></QueryClientProvider>)

describe('console recovery', () => {
  it('reconnects after initial connection failure without asking for a password', async () => {
    const user = userEvent.setup()
    vi.mocked(publicRequest).mockRejectedValueOnce(new ApiError('无法连接网关', 0))
    start()
    expect(await screen.findByText('暂时无法连接 LocalRouter')).toBeVisible()
    expect(screen.queryByLabelText('本机管理密钥')).not.toBeInTheDocument()
    expect(adminRequest).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '重新连接' }))
    expect(await screen.findByText('已读取运行数据')).toBeVisible()
  })

  it('marks stale data when the gateway disconnects and recovers on refresh', async () => {
    const user = userEvent.setup()
    start()
    await screen.findByText('已读取运行数据')
    vi.mocked(publicRequest).mockRejectedValueOnce(new ApiError('离线', 0))
    await act(async () => { await client.refetchQueries({ queryKey: ['public-status'] }) })
    expect(await screen.findByText('连接中断')).toBeVisible()
    expect(screen.getByText(/当前显示的是上次读取的数据/)).toBeVisible()
    expect(screen.getByText('已读取运行数据')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '刷新当前数据' }))
    await waitFor(() => expect(screen.queryByText('连接中断')).not.toBeInTheDocument())
  })

  it('clears cached management data and requires unlocking after a 401', async () => {
    start()
    await screen.findByText('已读取运行数据')
    vi.mocked(adminRequest).mockRejectedValue(new ApiError('expired', 401))
    await act(async () => { await client.refetchQueries({ queryKey: ['console-data'] }) })
    expect(await screen.findByText('解锁本机控制台')).toBeVisible()
    expect(screen.queryByText('已读取运行数据')).not.toBeInTheDocument()
    expect(client.getQueryData(['console-data', 'summary'])).toBeUndefined()
    expect(screen.getByRole('alert')).toHaveTextContent('登录已失效')
  })

  it('still opens when the browser does not allow theme storage', async () => {
    vi.stubGlobal('localStorage', { getItem: () => { throw new Error('storage blocked') }, setItem: () => { throw new Error('storage blocked') } })
    start()
    expect(await screen.findByText('已读取运行数据')).toBeVisible()
  })
})

import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { adminCollection, useConsoleData } from '@/lib/console-data'
import { adminRequest, ApiError } from '@/lib/api'

vi.mock('@/lib/api', async () => ({ ...await vi.importActual('@/lib/api'), adminRequest: vi.fn() }))
beforeEach(() => { vi.mocked(adminRequest).mockReset() })

describe('console data boundaries', () => {
  it('stops a collection whose pages repeat without making progress', async () => {
    vi.mocked(adminRequest).mockResolvedValue({ items: [{ id: 1 }], total: 1000 })
    await expect(adminCollection('/local/api/tokens', '')).rejects.toThrow('列表在加载时发生变化')
    expect(adminRequest).toHaveBeenCalledTimes(2)
  })

  it('includes supplementary failures in a manual refresh result', async () => {
    let fail = false
    vi.mocked(adminRequest).mockImplementation(async path => {
      if (fail && path === '/local/api/agent-usage') throw new Error('usage unavailable')
      return []
    })
    const client = new QueryClient()
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const { result, unmount } = renderHook(() => useConsoleData('', true, 'overview', 1), { wrapper })
    await waitFor(() => expect(result.current.isFetching).toBe(false))
    fail = true
    const refreshed = await result.current.refetch()
    expect(refreshed.error?.message).toBe('usage unavailable')
    expect(result.current.data.summary).toEqual([])
    unmount(); client.clear()
  })

  it('reports authentication failure even when a cached collection exists', async () => {
    vi.mocked(adminRequest).mockRejectedValue(new ApiError('expired', 401))
    const client = new QueryClient()
    client.setQueryData(['console-data', 'summary'], { version: 'cached' })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const { result, unmount } = renderHook(() => useConsoleData('', true, 'overview', 1), { wrapper })
    await waitFor(() => expect(result.current.authError).toBeDefined())
    expect(result.current.authError).toMatchObject({ status: 401 })
    unmount(); client.clear()
  })

  it('loads the 101st channel or Token instead of truncating the collection', async () => {
    vi.mocked(adminRequest)
      .mockResolvedValueOnce({ items: Array.from({ length: 100 }, (_, id) => ({ id })), total: 101 })
      .mockResolvedValueOnce({ items: [{ id: 100 }], total: 101 })
    const items = await adminCollection('/local/api/channels', '')
    expect(items).toHaveLength(101)
    expect(adminRequest).toHaveBeenLastCalledWith('/local/api/channels?page=2&page_size=100', '', { signal: undefined })
  })

  it('loads the services page without requesting unrelated jobs', async () => {
    vi.mocked(adminRequest).mockImplementation(async path => {
      if (path === '/local/api/workflows/jobs') throw new Error('job fixture failure')
      if (path.includes('/channels?')) return { items: [{ id: 101 }], total: 1 }
      if (path.includes('/tokens?') || path.includes('/logs?')) return { items: [], total: 0 }
      return []
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const { result, unmount } = renderHook(() => useConsoleData('', true, 'protocols', 1), { wrapper })
    await waitFor(() => expect(result.current.isFetching).toBe(false))
    expect(result.current.data.channels).toEqual([{ id: 101 }])
    expect(result.current.error).toBeUndefined()
    expect(result.current.warnings).toEqual([])
    expect(vi.mocked(adminRequest).mock.calls.map(([path]) => path)).toEqual([
      '/local/api/protocols', '/local/api/providers', '/local/api/channels?page=1&page_size=100',
    ])
    unmount(); client.clear()
  })

  it('does not wait for a stalled jobs request to display other sections', async () => {
    vi.mocked(adminRequest).mockImplementation(async path => {
      if (path === '/local/api/workflows/jobs') return new Promise(() => {})
      if (path.includes('/channels?') || path.includes('/tokens?') || path.includes('/logs?')) return { items: [], total: 0 }
      return []
    })
    const client = new QueryClient()
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const { result, unmount } = renderHook(() => useConsoleData('', true, 'protocols', 1), { wrapper })
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.isFetching).toBe(false)
    const refreshed = await result.current.refetch()
    expect(refreshed.error).toBeUndefined()
    expect(result.current.error).toBeUndefined()
    expect(vi.mocked(adminRequest).mock.calls.some(([path]) => path === '/local/api/workflows/jobs')).toBe(false)
    unmount(); client.clear()
  })
})

import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { adminCollection, useConsoleData } from '@/lib/console-data'
import { adminRequest } from '@/lib/api'

vi.mock('@/lib/api', () => ({ adminRequest: vi.fn() }))
beforeEach(() => { vi.mocked(adminRequest).mockReset() })

describe('console data boundaries', () => {
  it('loads the 101st channel or Token instead of truncating the collection', async () => {
    vi.mocked(adminRequest)
      .mockResolvedValueOnce({ items: Array.from({ length: 100 }, (_, id) => ({ id })), total: 101 })
      .mockResolvedValueOnce({ items: [{ id: 100 }], total: 101 })
    const items = await adminCollection('/local/api/channels', '')
    expect(items).toHaveLength(101)
    expect(adminRequest).toHaveBeenLastCalledWith('/local/api/channels?page=2&page_size=100', '')
  })

  it('renders channel data when the jobs request fails', async () => {
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
    expect(result.current.warnings[0].message).toContain('job fixture failure')
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
    unmount(); client.clear()
  })
})

import { afterEach, describe, expect, it, vi } from 'vitest'
import { adminRequest, publicRequest } from '@/lib/api'

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

describe('console request recovery', () => {
  it('bounds a stalled read and reports a timeout', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn((_path, init) => new Promise((_resolve, reject) => {
      init.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    })))
    const request = expect(publicRequest('/local/status')).rejects.toMatchObject({ status: 0, message: expect.stringContaining('读取超时') })
    await vi.advanceTimersByTimeAsync(15_000)
    await request
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('cancels reads when their owner releases the query', async () => {
    vi.stubGlobal('fetch', vi.fn((_path, init) => new Promise((_resolve, reject) => {
      init.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    })))
    const owner = new AbortController()
    const request = expect(adminRequest('/local/api/summary', '', { signal: owner.signal })).rejects.toMatchObject({ name: 'AbortError' })
    owner.abort()
    await request
  })

  it('keeps a disconnected mutation outcome uncertain and never resubmits it', async () => {
    const request = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
    vi.stubGlobal('fetch', request)
    await expect(adminRequest('/local/api/example', '', { method: 'POST', body: '{}' })).rejects.toMatchObject({ status: 0, message: expect.stringContaining('操作可能已经执行') })
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('does not turn a broken success response into empty console data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>Unavailable</html>')))
    await expect(adminRequest('/local/api/summary', '')).rejects.toMatchObject({ message: '网关返回了无法识别的响应。' })
  })

  it('preserves authentication failures for session recovery', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ success: false, message: '登录已失效' }, { status: 401 })))
    await expect(adminRequest('/local/api/summary', '')).rejects.toMatchObject({ status: 401, message: '登录已失效' })
  })

  it('accepts the existing success-only mutation envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ success: true })))
    await expect(adminRequest('/local/api/example', '', { method: 'DELETE' })).resolves.toBeUndefined()
  })

  it('marks unreadable mutation responses for reconciliation', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('broken response')))
    await expect(adminRequest('/local/api/example', '', { method: 'POST' })).rejects.toMatchObject({ message: expect.stringContaining('核对状态') })
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})

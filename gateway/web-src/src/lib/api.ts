import type { ApiEnvelope, Paginated } from '@/lib/types'

export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function requestJSON(path: string, init: RequestInit) {
  const reading = !init.method || ['GET', 'HEAD'].includes(init.method.toUpperCase())
  const controller = new AbortController()
  let timedOut = false
  const cancel = () => controller.abort(init.signal?.reason)
  if (init.signal?.aborted) cancel()
  else init.signal?.addEventListener('abort', cancel, { once: true })
  const timer = reading ? setTimeout(() => {
    timedOut = true
    controller.abort()
  }, 15_000) : undefined
  try {
    const response = await fetch(path, { ...init, signal: controller.signal })
    const payload: unknown = await response.json().catch(error => {
      if (controller.signal.aborted) throw error
      return null
    })
    return { response, payload }
  } catch (cause) {
    if (init.signal?.aborted) throw cause
    throw new ApiError(reading
      ? timedOut ? '读取超时，请检查网关后重新读取。' : '无法连接网关，请检查服务是否运行。'
      : '未收到操作结果，操作可能已经执行。请先刷新并核对状态，避免重复提交。', 0)
  } finally {
    clearTimeout(timer)
    init.signal?.removeEventListener('abort', cancel)
  }
}

export async function publicRequest<T>(path: string, signal?: AbortSignal): Promise<T> {
  const { response, payload } = await requestJSON(path, { headers: { Accept: 'application/json' }, signal })
  if (!response.ok) throw new ApiError(`HTTP ${response.status}`, response.status)
  if (payload === null || typeof payload !== 'object') throw new ApiError('网关返回了无法识别的响应。', response.status)
  return payload as T
}

export async function adminRequest<T>(
  path: string,
  adminToken: string,
  init: RequestInit = {}
): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (adminToken) headers.set('X-Local-Admin', adminToken)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const result = await requestJSON(path, { ...init, headers })
  const { response } = result
  const payload = result.payload as ApiEnvelope<T> | null
  if (!response.ok || payload?.success === false) {
    throw new ApiError(typeof payload?.message === 'string' ? payload.message : `HTTP ${response.status}`, response.status)
  }
  const reading = !init.method || ['GET', 'HEAD'].includes(init.method.toUpperCase())
  if (!payload || payload.success !== true || (reading && !Object.hasOwn(payload, 'data'))) {
    throw new ApiError(reading ? '网关返回了无法识别的响应。' : '操作响应无法识别，请先刷新并核对状态，避免重复提交。', response.status)
  }
  return payload.data
}

export function normalizeItems<T>(payload: Paginated<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.items
}

export function formatTimestamp(value?: number | string): string {
  if (value === undefined || value === null || value === '') return '—'
  const numeric = typeof value === 'number' ? value : Number(value)
  const date = Number.isFinite(numeric)
    ? new Date(numeric < 10_000_000_000 ? numeric * 1000 : numeric)
    : new Date(value)
  if (Number.isNaN(date.getTime())) return String(value)
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date)
}

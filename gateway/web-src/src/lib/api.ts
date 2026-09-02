import type { ApiEnvelope, Paginated } from '@/lib/types'

export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export async function publicRequest<T>(path: string): Promise<T> {
  const response = await fetch(path, { headers: { Accept: 'application/json' } })
  const payload = (await response.json().catch(() => null)) as T | null
  if (!response.ok || payload === null) {
    throw new ApiError(`HTTP ${response.status}`, response.status)
  }
  return payload
}

export async function adminRequest<T>(
  path: string,
  adminToken: string,
  init: RequestInit = {}
): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  headers.set('X-Local-Admin', adminToken)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(path, { ...init, headers })
  const payload = (await response.json().catch(() => null)) as
    | ApiEnvelope<T>
    | null
  if (!response.ok || payload === null || payload.success === false) {
    throw new ApiError(payload?.message || `HTTP ${response.status}`, response.status)
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

import {
  ArrowUpRight,
  Boxes,
  ChevronRight,
  CircleGauge,
  GitBranch,
  PanelRightOpen,
  RefreshCcw,
  Search,
  Server,
  UsersRound,
  Waves,
} from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ActivationToggle } from '@/components/activation-toggle'
import { ProtocolStatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { adminRequest, formatTimestamp } from '@/lib/api'
import { supplierColor } from '@/lib/supplier-colors'
import type { ProtocolPricingEntry, ProtocolQuotaReferenceValue, ProtocolView } from '@/lib/types'
import { cn } from '@/lib/utils'

type PoolRuntime = NonNullable<ProtocolView['pool_runtime']>
type PoolAccount = PoolRuntime['accounts'][number]
type ExternalPoolSnapshot = {
  service?: string
  mode?: string
  total?: number
  ready?: number
  in_flight?: number
  sticky_resources?: number
}
type DetailTarget = { kind: 'protocol' } | { kind: 'account'; ref: string }
type ServiceTab = 'accounts' | 'services'

const PAGE_SIZE = 50

function accountVariant(status: PoolAccount['status']) {
  if (status === 'ready') return 'success' as const
  if (status === 'cooldown' || status === 'busy' || status === 'balance-low') return 'warning' as const
  return 'destructive' as const
}

function readyLabel(protocol: ProtocolView) {
  const runtime = protocol.pool_runtime
  if (!runtime) return protocol.ready ? '已连接' : '不可用'
  if (runtime.ownership === 'upstream') return protocol.ready ? '已连接' : '不可用'
  return `${runtime.ready}/${runtime.total}`
}

function percent(value: number, total: number) {
  if (total <= 0) return 0
  return Math.max(0, Math.min(100, Math.round((value / total) * 100)))
}

function compactNumber(value: number) {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1, notation: 'compact' }).format(value)
}

function quotaValue(value: number, unit?: string) {
  return `${compactNumber(value)}${unit ? ` ${unit}` : ''}`
}

function referenceMoney(value: number, currency?: string) {
  const formatted = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 4 }).format(value)
  return `${formatted}${currency ? ` ${currency}` : ''}`
}

function quotaReferenceSummary(value?: ProtocolQuotaReferenceValue) {
  if (!value) return null
  if (value.status === 'ambiguous') return '费率不唯一，无法折算'
  const parts = []
  if (value.total !== undefined) parts.push(`参考总值 ${referenceMoney(value.total, value.currency)}`)
  if (value.remaining !== undefined) parts.push(`余值 ${referenceMoney(value.remaining, value.currency)}`)
  return parts.length ? parts.join(' · ') : null
}

function quotaStatusLabel(status?: PoolRuntime['quota']['status']) {
  if (status === 'untracked') return '额度未配置'
  if (status === 'unknown') return '上游未返回'
  if (status === 'confirmed') return '已确认'
  if (status === 'estimated') return '估算'
  if (status === 'remaining-only') return '仅余量'
  if (status === 'partial') return '部分接入'
  if (status === 'stale') return '已过期'
  if (status === 'mixed-unit') return '单位不一致'
  return '未接入'
}

function accountQuotaSummary(account: PoolAccount) {
  const quota = account.quota
  if (!quota?.tracked) return '—'
  if (quota.remaining !== undefined) {
    const remaining = quotaValue(quota.remaining, quota.unit)
    const reference = quota.reference_value
    return reference?.remaining !== undefined
      ? `${remaining} · ${referenceMoney(reference.remaining, reference.currency)}`
      : remaining
  }
  if (quota.used_percent !== undefined) return `已用 ${Math.round(quota.used_percent)}%`
  return quota.status === 'stale' ? '已过期' : '部分数据'
}

function routeTargetCount(route: ProtocolView['routes'][number]) {
  if (!route.target_selector) return 0
  return new Set([
    ...Object.values(route.target_selector.mappings || {}),
    route.target_selector.default_target,
  ].filter((target): target is string => Boolean(target))).size
}

function pricingStatusLabel(status: ProtocolPricingEntry['status']) {
  switch (status) {
    case 'confirmed': return '已确认'
    case 'estimated': return '估算'
    case 'unknown': return '未知'
    default: return '未公开'
  }
}

function pricingToneClass(status: ProtocolPricingEntry['status']) {
  switch (status) {
    case 'confirmed': return 'text-emerald-700/80 dark:text-emerald-300/80'
    case 'estimated': return 'text-sky-700/80 dark:text-sky-300/80'
    case 'unknown': return 'text-amber-700/80 dark:text-amber-300/80'
    default: return 'text-muted-foreground'
  }
}

function pricingSummary(protocol: ProtocolView) {
  const entries = protocol.pricing?.entries || []
  if (!entries.length) return '未接入价格'
  const pending = entries.filter((entry) => entry.status === 'unknown' || entry.status === 'unpublished').length
  if (!pending) return `定价 ×${entries.length} 已确认`
  return pending === entries.length ? `定价 ×${entries.length} 未知/未公开` : `定价 ×${entries.length} · ${pending} 项待补`
}

function pricingAmountText(entry: ProtocolPricingEntry) {
  if (entry.amount === undefined) return '—'
  const value = Number.isInteger(entry.amount) ? entry.amount.toFixed(0) : String(entry.amount)
  return `${value} ${entry.currency || ''}`.trim()
}

function pricingUnitText(unit?: string) {
  if (!unit) return '计费单位未公开'
  return unit.replace(/^per-/, '').replaceAll('-', ' ')
}

function pricingValueText(entry: ProtocolPricingEntry) {
  if (entry.amount === undefined) return '未接入价格'
  if (entry.amount === 0) return `免费 / ${pricingUnitText(entry.unit)}`
  return `${pricingAmountText(entry)} / ${pricingUnitText(entry.unit)}`
}

function primaryPricingEntry(protocol: ProtocolView) {
  const entries = protocol.pricing?.entries || []
  return entries.find((entry) =>
    (entry.scope === 'operation' || entry.scope === 'model') &&
    entry.status === 'confirmed' &&
    typeof entry.amount === 'number' &&
    entry.amount > 0
  ) || entries.find((entry) =>
    (entry.scope === 'operation' || entry.scope === 'model') &&
    entry.status === 'confirmed' &&
    typeof entry.amount === 'number'
  ) || entries.find((entry) => entry.status === 'confirmed' && typeof entry.amount === 'number')
    || entries.find((entry) => entry.status === 'estimated' && typeof entry.amount === 'number')
    || entries[0]
}

function PricingCompact(props: { protocol: ProtocolView }) {
  const entry = primaryPricingEntry(props.protocol)
  if (!entry) {
    return <span className='mt-0.5 block truncate text-[10px] font-medium text-muted-foreground'>未接入价格</span>
  }
  const item = entry.label || entry.id
  const value = pricingValueText(entry)
  return (
    <span
      className='mt-0.5 block truncate text-[10px] font-medium tabular-nums text-foreground/80'
      title={`${item} · ${value} · ${pricingStatusLabel(entry.status)}`}
    >
      {value}
    </span>
  )
}

function ServicePricingTable(props: {
  protocol: ProtocolView
  busyId: string
  canManage: boolean
  onToggle: (operationId: string, enabled: boolean) => void
}) {
  const entries = props.protocol.pricing?.entries || []
  const routes = props.protocol.routes
  const routeIds = new Set(routes.map((route) => route.operation_id))
  const pricingByOperation = new Map(entries.filter((entry) => entry.scope === 'operation').map((entry) => [entry.id, entry]))
  const standaloneEntries = entries.filter((entry) => !routeIds.has(entry.id))
  const pending = entries.filter((entry) => entry.status === 'unknown' || entry.status === 'unpublished').length
  return (
    <section aria-labelledby={`service-pricing-${props.protocol.id}`}>
      <div className='flex min-h-10 flex-wrap items-center gap-2 border-b px-3 py-2 sm:px-4'>
        <h3 id={`service-pricing-${props.protocol.id}`} className='text-xs font-semibold'>子服务与计价</h3>
        <span className='text-[10px] tabular-nums text-muted-foreground'>{routes.length} 个子服务 · {entries.length} 项价格</span>
        {pending ? <span className='text-[10px] text-amber-700/80 dark:text-amber-300/80'>{pending} 项待补</span> : null}
      </div>
      <Table aria-label={`${props.protocol.name} 定价`}>
        <TableHeader>
          <TableRow className='hover:bg-transparent'>
            <TableHead className='h-8'>子服务</TableHead>
            <TableHead className='hidden h-8 md:table-cell'>接口</TableHead>
            <TableHead className='h-8'>价格</TableHead>
            <TableHead className='hidden h-8 w-36 sm:table-cell'>状态 / 来源</TableHead>
            <TableHead className='h-8 w-20 text-right'>启用</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {routes.map((route) => {
            const entry = pricingByOperation.get(route.operation_id)
            const enabled = route.enabled !== false
            return (
              <TableRow key={`route:${route.operation_id}`}>
                <TableCell className='min-w-40 py-2'>
                  <span className='block text-xs font-medium'>{entry?.label || route.summary || route.operation_id}</span>
                  <code className='mt-0.5 block text-[10px] text-muted-foreground'>{route.operation_id}</code>
                </TableCell>
                <TableCell className='hidden min-w-44 py-2 md:table-cell'>
                  <code className='text-[10px] text-muted-foreground'>{route.methods.join(' / ')} · {route.path}</code>
                </TableCell>
                <TableCell className='min-w-40 py-2'>
                  <span className='block text-xs font-medium tabular-nums'>{entry ? pricingValueText(entry) : '未接入价格'}</span>
                  {entry?.free_tier ? <span className='mt-0.5 block text-[10px] leading-4 text-muted-foreground'>免费层：{entry.free_tier}</span> : null}
                </TableCell>
                <TableCell className='hidden py-2 sm:table-cell'>
                  {entry ? <>
                    <span className={`block text-[10px] ${pricingToneClass(entry.status)}`}>{pricingStatusLabel(entry.status)}</span>
                    <a className='inline-flex items-center gap-1 text-[10px] text-muted-foreground underline decoration-border underline-offset-2 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring' href={entry.source_url} target='_blank' rel='noreferrer' aria-label={`打开 ${entry.label || entry.id} 定价来源`}>
                      {entry.source_type} · {entry.checked_at}<ArrowUpRight aria-hidden='true' className='size-3' />
                    </a>
                  </> : <span className='text-[10px] text-muted-foreground'>—</span>}
                </TableCell>
                <TableCell className='py-2 text-right'>
                  <ActivationToggle compact checked={enabled} busy={props.busyId === `${props.protocol.id}:${route.operation_id}`} disabled={!props.canManage || !props.protocol.enabled} label={`${enabled ? '停用' : '启用'}子服务 ${route.operation_id}`} onChange={() => props.onToggle(route.operation_id, !enabled)} />
                </TableCell>
              </TableRow>
            )
          })}
          {standaloneEntries.map((entry) => (
            <TableRow key={`pricing:${entry.id}`}>
              <TableCell className='min-w-40 py-2'>
                <span className='block text-xs font-medium'>{entry.label || entry.id}</span>
                <code className='mt-0.5 block text-[10px] text-muted-foreground'>{entry.id}</code>
              </TableCell>
              <TableCell className='hidden py-2 md:table-cell'><Badge variant='outline'>{entry.scope}</Badge></TableCell>
              <TableCell className='min-w-40 py-2'>
                <span className='block text-xs font-medium tabular-nums'>{pricingValueText(entry)}</span>
                {entry.free_tier ? <span className='mt-0.5 block text-[10px] leading-4 text-muted-foreground'>免费层：{entry.free_tier}</span> : null}
              </TableCell>
              <TableCell className='hidden py-2 sm:table-cell'>
                <span className={`block text-[10px] ${pricingToneClass(entry.status)}`}>{pricingStatusLabel(entry.status)}</span>
                <a className='inline-flex items-center gap-1 text-[10px] text-muted-foreground underline decoration-border underline-offset-2 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring' href={entry.source_url} target='_blank' rel='noreferrer' aria-label={`打开 ${entry.label || entry.id} 定价来源`}>
                  {entry.source_type} · {entry.checked_at}<ArrowUpRight aria-hidden='true' className='size-3' />
                </a>
              </TableCell>
              <TableCell className='py-2 text-right text-xs text-muted-foreground'>—</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  )
}

function poolHealth(protocol: ProtocolView) {
  const runtime = protocol.pool_runtime
  if (!runtime) return { healthy: protocol.ready ? 1 : 0, invalid: protocol.ready ? 0 : 1, total: 1 }
  if (runtime.total === 0) {
    return { healthy: protocol.ready ? 1 : 0, invalid: protocol.ready ? 0 : 1, total: 1 }
  }
  const healthy = runtime.ready + runtime.busy
  return { healthy, invalid: Math.max(0, runtime.total - healthy), total: runtime.total }
}

function HealthDonut(props: { protocol: ProtocolView; compact?: boolean }) {
  const health = poolHealth(props.protocol)
  const healthyPercent = percent(health.healthy, health.total)
  const size = props.compact ? 'size-10' : 'size-12'
  return (
    <span
      role='img'
      aria-label={`${props.protocol.name} 健康 ${health.healthy}，失效 ${health.invalid}`}
      className={cn('relative inline-flex shrink-0 items-center justify-center', size)}
      title={`健康 ${health.healthy} · 失效 ${health.invalid}`}
    >
      <svg aria-hidden='true' viewBox='0 0 36 36' className='size-full -rotate-90'>
        <circle cx='18' cy='18' r='14' pathLength='100' fill='none' className='stroke-rose-300 dark:stroke-rose-900' strokeWidth='5' />
        {healthyPercent > 0 ? <circle cx='18' cy='18' r='14' pathLength='100' fill='none' className='stroke-emerald-300 dark:stroke-emerald-700' strokeWidth='5' strokeLinecap='butt' strokeDasharray={`${healthyPercent} 100`} /> : null}
      </svg>
      <span className={cn('absolute text-[9px] font-semibold tabular-nums', healthyPercent === 0 ? 'text-rose-700 dark:text-rose-300' : healthyPercent === 100 ? 'text-emerald-700 dark:text-emerald-300' : 'text-muted-foreground')}>{healthyPercent}%</span>
    </span>
  )
}

function QuotaProgress(props: { protocol: ProtocolView; compact?: boolean }) {
  const runtime = props.protocol.pool_runtime
  const quota = runtime?.quota
  const usedPercent = typeof quota?.used_percent === 'number' ? quota.used_percent : 0
  const hasPercent = (quota?.status === 'confirmed' || quota?.status === 'estimated') && typeof quota.used_percent === 'number'
  const remainingPercent = hasPercent ? Math.max(0, Math.min(100, 100 - usedPercent)) : 0
  const estimated = quota?.status === 'estimated'
  const tracked = Boolean(quota?.tracked_accounts)
  const statusLabel = quotaStatusLabel(quota?.status)
  const referenceSummary = quotaReferenceSummary(quota?.reference_value)
  const compactReferenceLabel = quota?.reference_value?.total !== undefined
    ? `总值 ${referenceMoney(quota.reference_value.total, quota.reference_value.currency)}`
    : quota?.reference_value?.remaining !== undefined
      ? `余值 ${referenceMoney(quota.reference_value.remaining, quota.reference_value.currency)}`
      : null
  const label = props.compact && compactReferenceLabel
    ? compactReferenceLabel
    : hasPercent
    ? `${estimated ? '估算' : ''}余量 ${Math.round(remainingPercent)}%`
    : tracked && quota?.remaining !== undefined
      ? `余量 ${quotaValue(quota.remaining, quota.unit)}`
      : statusLabel
  const details = quota
    ? `${quotaStatusLabel(quota.status)} · 已登记 ${quota.tracked_accounts}/${runtime?.total || 0}${quota.stale_accounts ? ` · 过期 ${quota.stale_accounts}` : ''}${referenceSummary ? ` · ${referenceSummary}` : ''}`
    : '上游未提供额度遥测'
  return (
    <div className={cn('min-w-0', props.compact ? 'w-24' : 'w-full')} title={details}>
      <div className='flex items-center justify-between gap-2 text-[10px] leading-4 text-muted-foreground'>
        <span className='truncate'>{label}</span>
        {!props.compact && hasPercent && quota?.remaining !== undefined ? <span className='shrink-0 tabular-nums'>余 {quotaValue(quota.remaining, quota.unit)}</span> : null}
      </div>
      <div
        role={hasPercent ? 'progressbar' : 'status'}
        aria-label={hasPercent ? `${props.protocol.name} 剩余额度${estimated ? '（估算）' : ''}` : `${props.protocol.name} ${statusLabel}`}
        aria-valuemin={hasPercent ? 0 : undefined}
        aria-valuemax={hasPercent ? 100 : undefined}
        aria-valuenow={hasPercent ? Math.round(remainingPercent) : undefined}
        className={cn(
          'mt-0.5 h-1.5 overflow-hidden rounded-full border',
          hasPercent ? 'bg-[var(--quota-empty)]'
            : !tracked ? 'border-amber-300 bg-amber-200/70 dark:border-amber-800 dark:bg-amber-950/70'
              : quota?.status === 'stale' || quota?.status === 'mixed-unit' ? 'border-amber-300 bg-amber-100 dark:border-amber-800 dark:bg-amber-950/50'
                : 'border-sky-300 bg-sky-100 dark:border-sky-800 dark:bg-sky-950/50'
        )}
      >
        {hasPercent ? <div className='h-full rounded-full bg-[var(--quota-remaining)]' style={{ width: `${remainingPercent}%` }} /> : null}
      </div>
      {!props.compact && quota && quota.status !== 'confirmed' ? <span className='mt-0.5 block text-[9px] text-muted-foreground'>{quotaStatusLabel(quota.status)} · {quota.tracked_accounts}/{runtime?.total || 0} 个账号</span> : null}
      {!props.compact && referenceSummary ? <span className='mt-0.5 block text-[9px] tabular-nums text-muted-foreground'>{referenceSummary}</span> : null}
    </div>
  )
}

function PoolMetric(props: { label: string; value: number; tone?: 'healthy' | 'warning' | 'invalid' }) {
  return (
    <div className='min-w-0 px-3 py-2'>
      <p className='text-[10px] text-muted-foreground'>{props.label}</p>
      <p className={cn('mt-0.5 text-sm font-semibold tabular-nums', props.tone === 'healthy' && 'text-emerald-700/80 dark:text-emerald-300/80', props.tone === 'warning' && 'text-amber-700/80 dark:text-amber-300/80', props.tone === 'invalid' && 'text-rose-700/80 dark:text-rose-300/80')}>{props.value}</p>
    </div>
  )
}

function DetailRow(props: { label: string; children: ReactNode }) {
  return (
    <div className='grid grid-cols-[7rem_minmax(0,1fr)] gap-3 border-b py-3 text-sm last:border-b-0'>
      <dt className='text-muted-foreground'>{props.label}</dt>
      <dd className='min-w-0 break-words text-right'>{props.children}</dd>
    </div>
  )
}

export function ProtocolsPage(props: {
  protocols: ProtocolView[]
  adminToken?: string
  onChanged?: () => Promise<void>
  embedded?: boolean
  onOpenEditor?: () => void
}) {
  const [selectedId, setSelectedId] = useState(() => props.protocols[0]?.id || '')
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [visibleLimit, setVisibleLimit] = useState(PAGE_SIZE)
  const [activeServiceTab, setActiveServiceTab] = useState<ServiceTab>('accounts')
  const [busyId, setBusyId] = useState('')
  const [externalPools, setExternalPools] = useState<Record<string, ExternalPoolSnapshot>>({})
  const [detailTarget, setDetailTarget] = useState<DetailTarget | null>(null)

  const selectedProtocol = props.protocols.find((item) => item.id === selectedId) || props.protocols[0]
  const runtime = selectedProtocol?.pool_runtime
  const accounts = runtime?.accounts || []
  const selectedAccount = detailTarget?.kind === 'account'
    ? accounts.find((account) => account.ref === detailTarget.ref)
    : undefined

  useEffect(() => {
    if (!props.protocols.length) {
      setSelectedId('')
      return
    }
    if (!props.protocols.some((item) => item.id === selectedId)) {
      setSelectedId(props.protocols[0].id)
    }
  }, [props.protocols, selectedId])

  const filteredAccounts = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase()
    return accounts.filter((account) => {
      const matchesStatus = statusFilter === 'all' || account.status === statusFilter
      const matchesQuery = !normalized || [account.label, account.status_label]
        .join(' ')
        .toLocaleLowerCase()
        .includes(normalized)
      return matchesStatus && matchesQuery
    })
  }, [accounts, query, statusFilter])

  const visibleAccounts = filteredAccounts.slice(0, visibleLimit)

  function selectProtocol(protocol: ProtocolView) {
    setSelectedId(protocol.id)
    setQuery('')
    setStatusFilter('all')
    setVisibleLimit(PAGE_SIZE)
    setActiveServiceTab('accounts')
    setDetailTarget(null)
    if (protocol.pool_runtime?.ownership === 'upstream' && props.adminToken !== undefined && !externalPools[protocol.id]) {
      void checkBackend(protocol, true)
    }
  }

  async function resetAccount(protocol: ProtocolView, credentialRef: string) {
    if (props.adminToken === undefined) return
    setBusyId(protocol.id)
    try {
      await adminRequest(`/local/api/protocols/${protocol.id}/pool/reset`, props.adminToken, {
        method: 'POST',
        body: JSON.stringify({ credential_ref: credentialRef }),
      })
      await props.onChanged?.()
      toast.success('账号已恢复调度')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '账号状态重置失败')
    } finally {
      setBusyId('')
    }
  }

  async function checkBackend(protocol: ProtocolView, silent = false) {
    if (props.adminToken === undefined) return
    setBusyId(protocol.id)
    try {
      const result = await adminRequest<{ ready: boolean; pool?: ExternalPoolSnapshot }>(
        `/local/api/protocols/${protocol.id}/readiness/refresh`,
        props.adminToken,
        { method: 'POST' }
      )
      if (result.pool) {
        setExternalPools((current) => ({ ...current, [protocol.id]: result.pool! }))
      }
      await props.onChanged?.()
      if (!silent) {
        toast[result.ready ? 'success' : 'error'](
          result.ready ? `${protocol.name} 连接正常` : `${protocol.name} 当前不可用`
        )
      }
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '连接检查失败')
    } finally {
      setBusyId('')
    }
  }

  async function setActivation(protocol: ProtocolView, enabled: boolean, operationId?: string) {
    if (props.adminToken === undefined) return
    const busyKey = operationId ? `${protocol.id}:${operationId}` : protocol.id
    setBusyId(busyKey)
    try {
      const endpoint = operationId
        ? `/local/api/protocols/${encodeURIComponent(protocol.id)}/operations/${encodeURIComponent(operationId)}/activation`
        : `/local/api/protocols/${encodeURIComponent(protocol.id)}/activation`
      await adminRequest(endpoint, props.adminToken, {
        method: 'PUT', body: JSON.stringify({ enabled }),
      })
      await props.onChanged?.()
      toast.success(`${operationId || protocol.name} 已${enabled ? '启用' : '停用'}`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '运行状态保存失败')
    } finally {
      setBusyId('')
    }
  }

  if (!selectedProtocol) {
    return (
      <section className='border-y'>
        <EmptyState icon={Boxes} title='还没有 Protocol Pack' description='发布 Pack 后会在这里出现。' />
      </section>
    )
  }

  const externalPool = externalPools[selectedProtocol.id]
  const localPool = runtime?.ownership === 'local'
  const externalPoolManaged = runtime?.ownership === 'upstream'
  const canReset = selectedAccount
    ? selectedAccount.status === 'cooldown' || selectedAccount.status_label === '调度停用'
    : false
  const serviceTabId = (tab: ServiceTab) => `service-${selectedProtocol.id}-${tab}-tab`
  const servicePanelId = (tab: ServiceTab) => `service-${selectedProtocol.id}-${tab}-panel`

  function switchServiceTab(tab: ServiceTab, moveFocus = false) {
    setActiveServiceTab(tab)
    if (moveFocus) document.getElementById(serviceTabId(tab))?.focus()
  }

  return (
    <div className='flex h-full min-h-0 flex-col gap-2 overflow-hidden'>
      {!props.embedded ? <header className='flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b pb-2'>
        <h1 className='text-lg font-semibold tracking-tight'>服务与号池</h1>
        <span className='text-xs text-muted-foreground'>{props.protocols.length} 个服务</span>
        <Button asChild className='ml-auto' variant='ghost' size='sm'>
          <a href='/.well-known/localrouter.json' target='_blank' rel='noreferrer'>
            Agent 发现
            <ArrowUpRight aria-hidden='true' />
          </a>
        </Button>
      </header> : null}

      <div className='flex min-h-0 flex-1 flex-col overflow-hidden border-y lg:grid lg:grid-cols-[19rem_minmax(0,1fr)]'>
        <aside className='flex min-h-0 shrink-0 flex-col border-b lg:h-auto lg:shrink lg:border-b-0 lg:border-r' aria-label='服务列表'>
          <div className='flex shrink-0 items-center justify-between border-b px-3 py-2'>
            <span className='text-xs font-medium text-muted-foreground'>服务</span>
            <span className='text-[11px] text-muted-foreground'>健康 / 额度 / 定价</span>
          </div>
          <nav
            className='flex shrink-0 gap-1 overflow-x-auto overscroll-contain p-1.5 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring lg:block lg:min-h-0 lg:flex-1 lg:space-y-1 lg:overflow-x-hidden lg:overflow-y-auto lg:[scrollbar-gutter:stable]'
            aria-label='Protocol Pack'
            tabIndex={0}
          >
            {props.protocols.map((protocol) => {
              const selected = protocol.id === selectedProtocol.id
              const streaming = protocol.routes.some((route) => route.streaming)
              const identityColor = supplierColor(`protocol:${protocol.id}`)
              return (
                <button
                  key={protocol.id}
                  type='button'
                  aria-current={selected ? 'page' : undefined}
                  className={cn(
                    'flex min-h-[5rem] min-w-[17rem] cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-left outline-none transition-colors hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring lg:min-w-0 lg:w-full',
                    selected && 'bg-muted text-foreground'
                  )}
                  style={{ boxShadow: selected ? `inset 2px 0 ${identityColor}` : undefined }}
                  onClick={() => selectProtocol(protocol)}
                >
                  <span
                    className='flex size-8 shrink-0 items-center justify-center rounded-md'
                    style={{ color: identityColor, backgroundColor: `color-mix(in oklch, ${identityColor} 12%, transparent)` }}
                  >
                    {streaming ? <Waves aria-hidden='true' className='size-4' /> : <Boxes aria-hidden='true' className='size-4' />}
                  </span>
                  <span className='min-w-0 flex-1'>
                    <span className='block truncate text-sm font-medium'>{protocol.name}</span>
                    <code className='block truncate text-[10px] text-muted-foreground'>{protocol.mount}</code>
                    <span className='mt-0.5 block text-[10px] tabular-nums text-muted-foreground'>{readyLabel(protocol)} 可调度</span>
                  </span>
                  <span className='flex shrink-0 items-center gap-2'>
                    <HealthDonut protocol={protocol} compact />
                    <span className='w-24 min-w-0'>
                      <QuotaProgress protocol={protocol} compact />
                      <PricingCompact protocol={protocol} />
                    </span>
                  </span>
                </button>
              )
            })}
          </nav>
          {props.onOpenEditor ? (
            <div className='shrink-0 border-t p-2'>
              <Button className='w-full justify-start' variant='ghost' size='sm' onClick={props.onOpenEditor}>
                <GitBranch aria-hidden='true' />
                协议编辑
              </Button>
            </div>
          ) : null}
        </aside>

        <section
          className='min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring [scrollbar-gutter:stable]'
          aria-labelledby='selected-protocol-title'
          tabIndex={0}
        >
          <div className='flex min-h-14 flex-wrap items-center gap-2 border-b px-3 py-2 sm:px-4'>
            <div className='min-w-0 flex-1'>
              <div className='flex flex-wrap items-center gap-2'>
                <span className='h-5 w-1 shrink-0 rounded-full' style={{ backgroundColor: supplierColor(`protocol:${selectedProtocol.id}`) }} aria-hidden='true' />
                <h2 id='selected-protocol-title' className='truncate text-base font-semibold'>{selectedProtocol.name}</h2>
                <ProtocolStatusBadge protocol={selectedProtocol} />
              </div>
              <div className='mt-0.5 flex flex-wrap gap-x-2 text-[11px] text-muted-foreground'>
                <code>{selectedProtocol.mount}</code>
                <span>{selectedProtocol.routes.length} 个接口</span>
                {(() => { const summary = pricingSummary(selectedProtocol); return summary ? <span>{summary}</span> : null })()}
                {localPool ? <span>{selectedProtocol.pool?.strategy || 'fixed'}</span> : null}
              </div>
            </div>
            <ActivationToggle
              checked={selectedProtocol.enabled}
              busy={busyId === selectedProtocol.id}
              disabled={props.adminToken === undefined}
              label={`${selectedProtocol.enabled ? '停用' : '启用'}服务 ${selectedProtocol.name}`}
              onChange={() => setActivation(selectedProtocol, !selectedProtocol.enabled)}
            />
            <Button variant='ghost' size='icon' aria-label={`查看 ${selectedProtocol.name} 详情`} onClick={() => setDetailTarget({ kind: 'protocol' })}>
              <PanelRightOpen aria-hidden='true' />
            </Button>
          </div>

          {runtime ? (
            <>
              <div className='grid grid-cols-3 divide-x border-b sm:grid-cols-6'>
                <PoolMetric label='健康' value={runtime.ready + runtime.busy} tone='healthy' />
                <PoolMetric label='失效' value={Math.max(0, runtime.total - runtime.ready - runtime.busy)} tone='invalid' />
                <PoolMetric label='冷却' value={runtime.cooling} tone={runtime.cooling ? 'warning' : undefined} />
                <PoolMetric label='停用' value={runtime.disabled} tone={runtime.disabled ? 'invalid' : undefined} />
                <PoolMetric label='过期' value={runtime.expired} tone={runtime.expired ? 'invalid' : undefined} />
                <PoolMetric label='请求中' value={runtime.in_flight} />
              </div>
              <div className='flex flex-wrap items-center gap-3 border-b px-3 py-2 sm:px-4'>
                <HealthDonut protocol={selectedProtocol} />
                <div className='min-w-[11rem] flex-1'>
                  <div className='flex flex-wrap gap-x-3 gap-y-1 text-[10px] text-muted-foreground'>
                    <span><b className='font-medium text-emerald-700/80 dark:text-emerald-300/80'>健康</b> {runtime.ready + runtime.busy}</span>
                    <span><b className='font-medium text-amber-700/80 dark:text-amber-300/80'>冷却</b> {runtime.cooling}</span>
                    <span><b className='font-medium text-rose-700/80 dark:text-rose-300/80'>停用/过期</b> {runtime.disabled + runtime.expired}</span>
                    <span>余额不足 {runtime.balance_low}</span>
                  </div>
                  <div className='mt-1.5 flex h-1.5 overflow-hidden rounded-full bg-muted' aria-hidden='true'>
                    <span className='bg-emerald-300 dark:bg-emerald-700' style={{ width: `${percent(runtime.ready + runtime.busy, runtime.total)}%` }} />
                    <span className='bg-amber-300 dark:bg-amber-700' style={{ width: `${percent(runtime.cooling, runtime.total)}%` }} />
                    <span className='bg-rose-300 dark:bg-rose-800' style={{ width: `${percent(runtime.disabled + runtime.expired + runtime.balance_low, runtime.total)}%` }} />
                  </div>
                </div>
                <div className='min-w-[12rem] flex-1 sm:max-w-xs'><QuotaProgress protocol={selectedProtocol} /></div>
              </div>
            </>
          ) : null}

          <div
            role='tablist'
            aria-label={`${selectedProtocol.name} 服务数据`}
            className='flex min-h-11 items-end gap-1 border-b px-3 sm:px-4'
            onKeyDown={(event) => {
              if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                event.preventDefault()
                switchServiceTab(activeServiceTab === 'accounts' ? 'services' : 'accounts', true)
              } else if (event.key === 'Home' || event.key === 'End') {
                event.preventDefault()
                switchServiceTab(event.key === 'Home' ? 'accounts' : 'services', true)
              }
            }}
          >
            <button
              id={serviceTabId('accounts')}
              type='button'
              role='tab'
              aria-selected={activeServiceTab === 'accounts'}
              aria-controls={servicePanelId('accounts')}
              tabIndex={activeServiceTab === 'accounts' ? 0 : -1}
              className={cn(
                'relative flex min-h-11 cursor-pointer items-center gap-2 px-3 text-xs font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
                activeServiceTab === 'accounts' && 'text-foreground after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary'
              )}
              onClick={() => switchServiceTab('accounts')}
            >
              <UsersRound aria-hidden='true' className='size-4' />
              账号明细
              <span className='tabular-nums text-[10px] text-muted-foreground'>{localPool ? accounts.length : 1}</span>
            </button>
            <button
              id={serviceTabId('services')}
              type='button'
              role='tab'
              aria-selected={activeServiceTab === 'services'}
              aria-controls={servicePanelId('services')}
              tabIndex={activeServiceTab === 'services' ? 0 : -1}
              className={cn(
                'relative flex min-h-11 cursor-pointer items-center gap-2 px-3 text-xs font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
                activeServiceTab === 'services' && 'text-foreground after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary'
              )}
              onClick={() => switchServiceTab('services')}
            >
              <Boxes aria-hidden='true' className='size-4' />
              服务
              <span className='tabular-nums text-[10px] text-muted-foreground'>{selectedProtocol.routes.length}</span>
            </button>
          </div>

          {activeServiceTab === 'accounts' ? (
            <div
              id={servicePanelId('accounts')}
              role='tabpanel'
              aria-labelledby={serviceTabId('accounts')}
              tabIndex={0}
              className='outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring'
            >
              {localPool ? (
                <>
                  <div className='flex flex-wrap items-center gap-2 border-b px-3 py-1.5 sm:px-4'>
                    <div className='relative min-w-[13rem] flex-1 sm:max-w-sm'>
                      <Search aria-hidden='true' className='pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground' />
                      <Input
                        type='search'
                        aria-label='搜索当前服务账号'
                        placeholder='搜索账号或状态…'
                        className='h-8 min-h-8 pl-9 text-xs'
                        value={query}
                        onChange={(event) => {
                          setQuery(event.target.value)
                          setVisibleLimit(PAGE_SIZE)
                        }}
                      />
                    </div>
                    <select
                      aria-label='筛选账号状态'
                      className='h-8 cursor-pointer rounded-md border bg-background px-2.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring'
                      value={statusFilter}
                      onChange={(event) => {
                        setStatusFilter(event.target.value)
                        setVisibleLimit(PAGE_SIZE)
                      }}
                    >
                      <option value='all'>全部状态</option>
                      <option value='ready'>可调度</option>
                      <option value='busy'>占用中</option>
                      <option value='cooldown'>冷却中</option>
                      <option value='disabled'>已停用</option>
                      <option value='expired'>已过期</option>
                      <option value='balance-low'>余额不足</option>
                    </select>
                    <span className='text-[10px] tabular-nums text-muted-foreground'>{filteredAccounts.length} / {accounts.length}</span>
                  </div>

                  {visibleAccounts.length ? (
                    <>
                      <Table>
                        <TableHeader>
                          <TableRow className='hover:bg-transparent'>
                            <TableHead className='h-8'>账号</TableHead>
                            <TableHead className='h-8 w-24'>状态</TableHead>
                            <TableHead className='hidden h-8 w-28 text-right sm:table-cell'>额度</TableHead>
                            <TableHead className='hidden h-8 w-20 sm:table-cell'>请求中</TableHead>
                            <TableHead className='hidden h-8 w-20 md:table-cell'>失败</TableHead>
                            <TableHead className='hidden h-8 w-32 xl:table-cell'>最近使用</TableHead>
                            <TableHead className='h-8 w-14 text-right'>操作</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {visibleAccounts.map((account) => (
                            <TableRow key={account.ref} className='group'>
                              <TableCell className='max-w-0 py-1'>
                                <button
                                  type='button'
                                  className='block min-h-9 w-full cursor-pointer truncate rounded-sm text-left font-mono text-[11px] font-medium outline-none hover:text-primary focus-visible:ring-2 focus-visible:ring-ring'
                                  title={account.label}
                                  onClick={() => setDetailTarget({ kind: 'account', ref: account.ref })}
                                >
                                  {account.label}
                                </button>
                              </TableCell>
                              <TableCell className='py-1'><Badge className='min-h-5 px-1.5 text-[10px]' variant={accountVariant(account.status)}>{account.status_label}</Badge></TableCell>
                              <TableCell className='hidden py-1 text-right text-[11px] tabular-nums sm:table-cell'>
                                <span className={cn(account.quota?.stale && 'text-amber-700/80 dark:text-amber-300/80')}>{accountQuotaSummary(account)}</span>
                              </TableCell>
                              <TableCell className='hidden py-1 text-[11px] tabular-nums sm:table-cell'>{account.in_flight || '—'}</TableCell>
                              <TableCell className='hidden py-1 text-[11px] tabular-nums md:table-cell'>{account.consecutive_failures || '—'}</TableCell>
                              <TableCell className='hidden py-1 text-[10px] text-muted-foreground xl:table-cell'>{formatTimestamp(account.last_used)}</TableCell>
                              <TableCell className='py-1 text-right'>
                                <Button className='size-9 min-h-9' variant='ghost' size='icon' aria-label={`管理 ${account.label}`} onClick={() => setDetailTarget({ kind: 'account', ref: account.ref })}>
                                  <ChevronRight aria-hidden='true' />
                                </Button>
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                      <div className='flex min-h-10 items-center justify-between border-t px-3 text-[10px] text-muted-foreground sm:px-4'>
                        <span>显示 {visibleAccounts.length} / {filteredAccounts.length}</span>
                        {visibleAccounts.length < filteredAccounts.length ? (
                          <Button variant='ghost' size='sm' onClick={() => setVisibleLimit((current) => current + PAGE_SIZE)}>显示更多</Button>
                        ) : null}
                      </div>
                    </>
                  ) : (
                    <EmptyState
                      icon={UsersRound}
                      title={accounts.length ? '没有匹配的账号' : '账号池为空'}
                      description={accounts.length ? '调整搜索词或状态筛选。' : '该服务当前没有可调度账号。'}
                    />
                  )}
                </>
              ) : (
                <div className='divide-y'>
                  <button
                    type='button'
                    className='flex min-h-16 w-full cursor-pointer items-center gap-3 px-4 text-left outline-none transition-colors hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring'
                    onClick={() => setDetailTarget({ kind: 'protocol' })}
                  >
                    <Server aria-hidden='true' className='size-4 text-primary' />
                    <span className='min-w-0 flex-1'>
                      <span className='block text-sm font-medium'>{externalPool?.service || selectedProtocol.name}</span>
                      <code className='block truncate text-[10px] text-muted-foreground'>{externalPool?.mode || selectedProtocol.mount}</code>
                    </span>
                    <Badge variant={selectedProtocol.ready ? 'success' : 'destructive'}>{selectedProtocol.ready ? '已连接' : '不可用'}</Badge>
                    <ChevronRight aria-hidden='true' className='size-4 text-muted-foreground' />
                  </button>
                </div>
              )}
            </div>
          ) : (
            <div
              id={servicePanelId('services')}
              role='tabpanel'
              aria-labelledby={serviceTabId('services')}
              tabIndex={0}
              className='outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring'
            >
              <ServicePricingTable
                protocol={selectedProtocol}
                busyId={busyId}
                canManage={props.adminToken !== undefined}
                onToggle={(operationId, enabled) => setActivation(selectedProtocol, enabled, operationId)}
              />
            </div>
          )}
        </section>
      </div>

      <Sheet open={Boolean(detailTarget)} onOpenChange={(open) => { if (!open) setDetailTarget(null) }}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>{selectedAccount?.label || selectedProtocol.name}</SheetTitle>
            <SheetDescription>
              {selectedAccount ? `${selectedProtocol.name} · 账号运行态` : `${selectedProtocol.mount} · Protocol Pack`}
            </SheetDescription>
          </SheetHeader>
          <SheetBody>
            {selectedAccount ? (
              <section aria-label='账号详情'>
                <dl>
                  <DetailRow label='状态'><Badge variant={accountVariant(selectedAccount.status)}>{selectedAccount.status_label}</Badge></DetailRow>
                  <DetailRow label='额度状态'>{selectedAccount.quota?.tracked ? (selectedAccount.quota.stale ? '已过期' : selectedAccount.quota.status === 'estimated' ? '估算' : '已确认') : '未接入'}</DetailRow>
                  {selectedAccount.quota?.total !== undefined ? <DetailRow label='额度总量'>{quotaValue(selectedAccount.quota.total, selectedAccount.quota.unit)}</DetailRow> : null}
                  {selectedAccount.quota?.used !== undefined ? <DetailRow label='已使用'>{quotaValue(selectedAccount.quota.used, selectedAccount.quota.unit)}{selectedAccount.quota.used_percent !== undefined ? ` · ${Math.round(selectedAccount.quota.used_percent)}%` : ''}</DetailRow> : null}
                  {selectedAccount.quota?.remaining !== undefined ? <DetailRow label='额度余量'>{quotaValue(selectedAccount.quota.remaining, selectedAccount.quota.unit)}</DetailRow> : null}
                  {selectedAccount.quota?.reference_value?.total !== undefined ? <DetailRow label='参考总值'>{referenceMoney(selectedAccount.quota.reference_value.total, selectedAccount.quota.reference_value.currency)}</DetailRow> : null}
                  {selectedAccount.quota?.reference_value?.used !== undefined ? <DetailRow label='已用价值'>{referenceMoney(selectedAccount.quota.reference_value.used, selectedAccount.quota.reference_value.currency)}</DetailRow> : null}
                  {selectedAccount.quota?.reference_value?.remaining !== undefined ? <DetailRow label='剩余价值'>{referenceMoney(selectedAccount.quota.reference_value.remaining, selectedAccount.quota.reference_value.currency)}</DetailRow> : null}
                  {selectedAccount.quota?.reference_value?.pricing_id ? <DetailRow label='折算依据'><code>{selectedAccount.quota.reference_value.pricing_id}</code></DetailRow> : null}
                  {selectedAccount.quota?.checked_at ? <DetailRow label='额度检查'>{formatTimestamp(selectedAccount.quota.checked_at)}</DetailRow> : null}
                  <DetailRow label='请求中'>{selectedAccount.in_flight || '—'}</DetailRow>
                  <DetailRow label='连续失败'>{selectedAccount.consecutive_failures || '—'}</DetailRow>
                  <DetailRow label='最近使用'>{formatTimestamp(selectedAccount.last_used)}</DetailRow>
                  <DetailRow label='冷却至'>{formatTimestamp(selectedAccount.cooldown_until)}</DetailRow>
                  <DetailRow label='调度策略'>{selectedProtocol.pool?.strategy || 'fixed'}</DetailRow>
                  {selectedAccount.targets?.length ? <DetailRow label='兼容后端'>{selectedAccount.targets.join(' · ')}</DetailRow> : null}
                </dl>
              </section>
            ) : (
              <div className='space-y-5'>
                <section aria-labelledby='pool-detail-title'>
                  <h3 id='pool-detail-title' className='flex items-center gap-2 text-sm font-semibold'>
                    <CircleGauge aria-hidden='true' className='size-4 text-primary' />
                    号池
                  </h3>
                  <dl className='mt-2 border-y'>
                    <DetailRow label='归属'>{localPool ? 'LocalRouter' : externalPoolManaged ? '上游网关' : '静态连接'}</DetailRow>
                    <DetailRow label='策略'>{selectedProtocol.pool?.strategy || 'fixed'}</DetailRow>
                    <DetailRow label='可用'>{runtime ? `${runtime.ready} / ${runtime.total}` : selectedProtocol.ready ? '已连接' : '不可用'}</DetailRow>
                    {runtime ? <DetailRow label='额度状态'>{quotaStatusLabel(runtime.quota.status)}</DetailRow> : null}
                    {runtime?.quota.total !== undefined ? <DetailRow label='额度总量'>{quotaValue(runtime.quota.total, runtime.quota.unit)}</DetailRow> : null}
                    {runtime?.quota.used !== undefined ? <DetailRow label='已使用'>{quotaValue(runtime.quota.used, runtime.quota.unit)}{runtime.quota.used_percent !== undefined ? ` · ${Math.round(runtime.quota.used_percent)}%` : ''}</DetailRow> : null}
                    {runtime?.quota.remaining !== undefined ? <DetailRow label='额度余量'>{quotaValue(runtime.quota.remaining, runtime.quota.unit)}</DetailRow> : null}
                    {runtime?.quota.reference_value?.status === 'ambiguous' ? <DetailRow label='参考总值'>费率不唯一，无法折算</DetailRow> : null}
                    {runtime?.quota.reference_value?.total !== undefined ? <DetailRow label='参考总值'>{referenceMoney(runtime.quota.reference_value.total, runtime.quota.reference_value.currency)}</DetailRow> : null}
                    {runtime?.quota.reference_value?.used !== undefined ? <DetailRow label='已用价值'>{referenceMoney(runtime.quota.reference_value.used, runtime.quota.reference_value.currency)}</DetailRow> : null}
                    {runtime?.quota.reference_value?.remaining !== undefined ? <DetailRow label='剩余价值'>{referenceMoney(runtime.quota.reference_value.remaining, runtime.quota.reference_value.currency)}</DetailRow> : null}
                    {runtime?.quota.reference_value?.pricing_id ? <DetailRow label='折算依据'><code>{runtime.quota.reference_value.pricing_id}</code></DetailRow> : null}
                    {runtime ? <DetailRow label='额度登记'>{runtime.quota.tracked_accounts} / {runtime.total} 个账号</DetailRow> : null}
                    {runtime?.quota.stale_accounts ? <DetailRow label='额度过期'>{runtime.quota.stale_accounts}</DetailRow> : null}
                    {runtime?.balance_tracked ? <DetailRow label='额度耗尽'>{runtime.balance_empty}</DetailRow> : null}
                    {runtime?.balance_tracked ? <DetailRow label='余额不足'>{runtime.balance_low}</DetailRow> : null}
                    <DetailRow label='请求中'>{runtime?.in_flight || externalPool?.in_flight || '—'}</DetailRow>
                    {runtime?.cooling ? <DetailRow label='冷却'>{runtime.cooling}</DetailRow> : null}
                    {runtime?.disabled ? <DetailRow label='停用'>{runtime.disabled}</DetailRow> : null}
                    {runtime?.expired ? <DetailRow label='过期'>{runtime.expired}</DetailRow> : null}
                    {runtime?.unroutable ? <DetailRow label='未映射'>{runtime.unroutable}</DetailRow> : null}
                  </dl>
                </section>

                <section aria-labelledby='pricing-detail-title'>
                  <h3 id='pricing-detail-title' className='text-sm font-semibold'>定价</h3>
                  <div className='mt-2 divide-y border-y'>
                    {(selectedProtocol.pricing?.entries || []).map((entry) => (
                      <div key={entry.id} className='py-3'>
                        <div className='flex flex-wrap items-center gap-2'>
                          <code className='text-xs font-medium'>{entry.id}</code>
                          <Badge variant='outline'>{entry.scope}</Badge>
                          <span className={`text-[10px] ${pricingToneClass(entry.status)}`}>{pricingStatusLabel(entry.status)}</span>
                        </div>
                        <p className='mt-1 text-xs tabular-nums'>
                          {entry.label ? `${entry.label} · ` : ''}
                          {entry.amount !== undefined ? `${pricingAmountText(entry)} / ${entry.unit}` : '未接入价格'}
                        </p>
                        {entry.free_tier ? <p className='mt-1 text-[11px] text-muted-foreground'>免费层：{entry.free_tier}</p> : null}
                        {entry.note ? <p className='mt-1 text-[11px] text-muted-foreground'>{entry.note}</p> : null}
                        <p className='mt-1 text-[10px] text-muted-foreground'>
                          核对 {entry.checked_at} · 来源 <a className='underline outline-none hover:text-foreground' href={entry.source_url} target='_blank' rel='noreferrer'>{entry.source_type}</a>
                        </p>
                      </div>
                    ))}
                  </div>
                </section>

                <section aria-labelledby='route-detail-title'>
                  <h3 id='route-detail-title' className='text-sm font-semibold'>接口</h3>
                  <div className='mt-2 divide-y border-y'>
                    {selectedProtocol.routes.map((route) => (
                      <div key={route.operation_id} className='py-3'>
                        <div className='flex items-center gap-2'>
                          <code className='text-xs font-medium'>{route.operation_id}</code>
                          {route.streaming ? <Waves aria-label='流式接口' className='size-3.5 text-primary' /> : null}
                          {route.target_selector ? <Badge variant='outline'>{routeTargetCount(route)} 后端</Badge> : null}
                        </div>
                        <p className='mt-1 truncate text-xs text-muted-foreground'>{route.methods.join(' / ')} · {route.path}</p>
                      </div>
                    ))}
                  </div>
                </section>
              </div>
            )}
          </SheetBody>
          <SheetFooter>
            {!selectedAccount && externalPoolManaged && props.adminToken ? (
              <Button variant='outline' disabled={busyId === selectedProtocol.id} onClick={() => checkBackend(selectedProtocol)}>
                <RefreshCcw aria-hidden='true' />
                检查连接
              </Button>
            ) : null}
            {selectedAccount && canReset && props.adminToken ? (
              <Button disabled={busyId === selectedProtocol.id || selectedAccount.in_flight > 0} onClick={() => resetAccount(selectedProtocol, selectedAccount.ref)}>
                <RefreshCcw aria-hidden='true' />
                恢复调度
              </Button>
            ) : null}
            <Button asChild variant='outline'>
              <a href={selectedProtocol.docs.html} target='_blank' rel='noreferrer'>
                接口文档
                <ArrowUpRight aria-hidden='true' />
              </a>
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  )
}

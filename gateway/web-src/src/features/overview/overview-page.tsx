import {
  Activity,
  ArrowUpRight,
  Boxes,
  CheckCircle2,
  CircleDollarSign,
  Copy,
  Gauge,
  KeyRound,
  RadioTower,
  Route,
} from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { AdminTokenDialog } from '@/features/overview/admin-token-dialog'
import { supplierColor } from '@/lib/supplier-colors'
import type { Analytics, AnalyticsBucket, AnalyticsService, ProtocolView, Summary } from '@/lib/types'
import { cn } from '@/lib/utils'

const relayEndpoints = [
  ['/v1/chat/completions', 'OpenAI Chat'],
  ['/v1/responses', 'OpenAI Responses'],
  ['/v1/messages', 'Anthropic Messages'],
  ['/v1beta/models/*', 'Gemini Native'],
]

type TrendMetric = 'requests' | 'tokens' | 'cost'

const compactNumber = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const integerNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 })
const decimalNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 })

function formatCount(value: number) {
  return compactNumber.format(value || 0)
}

function formatUSD(value: number) {
  if (!value) return '$0.00'
  if (Math.abs(value) < 0.01) return `$${value.toFixed(4)}`
  return `$${value.toFixed(2)}`
}

function formatLatency(value: number) {
  if (!value) return '—'
  if (value >= 1000) return `${decimalNumber.format(value / 1000)}s`
  return `${integerNumber.format(value)}ms`
}

function serviceStatusLabel(status: string) {
  if (status === 'ready') return '就绪'
  if (status === 'disabled') return '停用'
  if (status === 'removed') return '已移除'
  if (status === 'pool-not-ready') return '号池未就绪'
  if (status === 'secret-not-ready') return '凭据未就绪'
  if (status === 'verification-required') return '等待验证'
  if (status === 'verification-stale') return '验证过期'
  return status || '不可用'
}

function costLabel(service: AnalyticsService) {
  if (service.kind === 'model-provider') return formatUSD(service.cost_usd || 0)
  if (service.cost_status === 'unavailable') return '未接入'
  const suffix = service.cost_status === 'partial' ? ' 部分' : service.cost_status === 'estimated' ? ' 估算' : ''
  return `${formatUSD(service.cost_usd || 0)}${suffix}`
}

function TrendChart(props: { buckets: AnalyticsBucket[]; services: AnalyticsService[]; metric: TrendMetric }) {
  const values = props.buckets.map((bucket) => {
    if (props.metric === 'tokens') return bucket.tokens
    if (props.metric === 'cost') return bucket.cost_usd
    return bucket.model_requests + bucket.protocol_requests
  })
  const maximum = Math.max(...values, 1)
  const total = values.reduce((sum, value) => sum + value, 0)
  const serviceValue = (service: AnalyticsService, index: number) => {
    const point = service.trend[index]
    if (!point) return 0
    if (props.metric === 'tokens') return point.tokens
    if (props.metric === 'cost') return point.cost_usd
    return point.requests
  }
  const activeServices = props.services
    .map((service) => ({ service, total: service.trend.reduce((sum, _point, index) => sum + serviceValue(service, index), 0) }))
    .filter((entry) => entry.total > 0)
    .sort((left, right) => right.total - left.total)

  return (
    <div className='pt-4'>
      <div className='mb-4 flex items-end justify-between gap-3'>
        <div>
          <p className='text-xs text-muted-foreground'>过去 24 小时</p>
          <p className='mt-1 text-2xl font-semibold tracking-tight tabular-nums'>
            {props.metric === 'cost' ? formatUSD(total) : formatCount(total)}
          </p>
        </div>
        <div className='flex max-w-[70%] flex-wrap justify-end gap-x-3 gap-y-1 text-[10px] text-muted-foreground' aria-label='供应商图例'>
          {activeServices.slice(0, 6).map(({ service }) => (
            <span key={service.id} className='flex min-w-0 items-center gap-1.5'>
              <i className='size-2 shrink-0 rounded-[2px]' style={{ backgroundColor: supplierColor(service.id) }} />
              <span className='max-w-24 truncate' title={service.name}>{service.name}</span>
            </span>
          ))}
          {activeServices.length > 6 ? <span>+{activeServices.length - 6}</span> : null}
        </div>
      </div>
      <div
        className='grid h-52 grid-cols-24 items-end gap-1 border-b border-l px-2 pt-4'
        role='img'
        aria-label={`过去 24 小时${props.metric === 'requests' ? '调用' : props.metric === 'tokens' ? 'Token' : '成本'}趋势`}
      >
        {props.buckets.map((bucket, index) => {
          const value = values[index]
          const height = value ? Math.max(3, (value / maximum) * 100) : 1
          const segments = activeServices
            .map(({ service }) => ({ service, value: serviceValue(service, index) }))
            .filter((segment) => segment.value > 0)
          const time = new Date(bucket.started_at)
          const label = time.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
          const breakdown = segments.map((segment) => `${segment.service.name} ${props.metric === 'cost' ? formatUSD(segment.value) : integerNumber.format(segment.value)}`).join(' · ')
          return (
            <div
              key={bucket.started_at}
              className='group relative flex h-full min-w-0 flex-col justify-end'
              title={`${label} · ${props.metric === 'cost' ? formatUSD(value) : integerNumber.format(value)}${breakdown ? ` · ${breakdown}` : ''}`}
            >
              <span className='pointer-events-none absolute bottom-[calc(100%+0.35rem)] left-1/2 z-10 hidden -translate-x-1/2 whitespace-nowrap border bg-popover px-1.5 py-1 text-[10px] shadow-sm group-hover:block'>
                {label} · {props.metric === 'cost' ? formatUSD(value) : integerNumber.format(value)}
              </span>
              <span className='flex w-full flex-col justify-end bg-muted/45' style={{ height: `${height}%` }}>
                {segments.length ? segments.map((segment) => (
                  <i
                    key={segment.service.id}
                    className='block w-full'
                    style={{ backgroundColor: supplierColor(segment.service.id), height: `${(segment.value / value) * 100}%` }}
                  />
                )) : <i className='block h-full w-full bg-muted-foreground/20' />}
              </span>
              {index % 4 === 0 ? <span className='absolute top-[calc(100%+0.3rem)] -translate-x-1/4 text-[9px] text-muted-foreground'>{label.slice(0, 2)}</span> : null}
            </div>
          )
        })}
      </div>
    </div>
  )
}

export function OverviewPage(props: {
  summary: Summary
  analytics: Analytics
  protocols: ProtocolView[]
  onChangeAdminToken: (token: string) => Promise<void>
}) {
  const [trendMetric, setTrendMetric] = useState<TrendMetric>('requests')
  const origin = window.location.origin
  const totals = props.analytics.totals
  const configuredServices = props.summary.channels + props.summary.protocols
  const readyServices = props.analytics.services.filter((service) => service.status === 'ready').length
  const protocolSuccessful = props.analytics.services
    .filter((service) => service.kind === 'protocol')
    .reduce((sum, service) => sum + service.successful, 0)
  const pricingCoverage = protocolSuccessful ? Math.round((totals.protocol_priced_calls / protocolSuccessful) * 100) : 0
  const referenceValues = props.protocols
    .map((protocol) => protocol.pool_runtime?.quota.reference_value)
    .filter((value) => value?.currency === 'USD' && typeof value.remaining === 'number')
  const poolReferenceRemaining = referenceValues.reduce((sum, value) => sum + (value?.remaining || 0), 0)
  const staleReferenceValues = referenceValues.filter((value) => value?.status === 'stale').length
  const stats = [
    {
      label: '总调用', value: formatCount(totals.requests),
      detail: `${formatCount(totals.model_requests)} 模型 · ${formatCount(totals.protocol_requests)} 服务 API`, icon: Activity,
    },
    {
      label: '服务能力', value: `${readyServices}/${configuredServices}`,
      detail: `${totals.active_operations} 项能力 · ${totals.active_services} 个服务有流量`, icon: Boxes,
    },
    {
      label: '资源消耗', value: formatCount(totals.total_tokens),
      detail: `${formatUSD(totals.model_cost_usd)} 模型 · ${formatUSD(totals.protocol_cost_usd)} 服务`, icon: CircleDollarSign,
    },
    {
      label: '请求质量', value: `${decimalNumber.format(totals.success_rate)}%`,
      detail: `平均 ${formatLatency(totals.average_latency_ms)} · Pack P95 ${formatLatency(totals.protocol_p95_latency_ms)}`, icon: Gauge,
    },
  ]

  async function copyBaseUrl() {
    await navigator.clipboard.writeText(origin)
    toast.success('本机网关地址已复制')
  }

  return (
    <div className='space-y-5'>
      <SectionHeader
        eyebrow='LocalRouter'
        title='运行概览'
        description='模型协议与通用服务 API 使用同一套调用、质量、成本和能力视图。'
        actions={<Button asChild variant='outline' size='sm'><a href='/docs/openapi.json' target='_blank' rel='noreferrer'>OpenAPI<ArrowUpRight aria-hidden='true' /></a></Button>}
      />

      <section aria-label='运行统计' className='grid divide-y border-y sm:grid-cols-2 sm:divide-x xl:grid-cols-4 xl:divide-y-0'>
        {stats.map(({ label, value, detail, icon: Icon }) => (
          <div key={label} className='min-w-0 px-4 py-4 first:pl-0 sm:first:pl-4 xl:first:pl-0'>
            <div className='flex items-center gap-2 text-xs text-muted-foreground'>
              <Icon aria-hidden='true' className='size-3.5 text-primary' />
              {label}
            </div>
            <p className='mt-2 text-2xl font-semibold tracking-tight tabular-nums'>{value}</p>
            <p className='mt-1 truncate text-[11px] text-muted-foreground' title={detail}>{detail}</p>
          </div>
        ))}
      </section>

      <section className='grid border-y xl:grid-cols-[minmax(0,1fr)_19rem]' aria-labelledby='analysis-title'>
        <div className='min-w-0 px-0 py-4 xl:border-r xl:pr-5'>
          <div className='flex flex-wrap items-center justify-between gap-3 border-b pb-2'>
            <div className='flex items-center gap-2'>
              <Activity aria-hidden='true' className='size-4 text-primary' />
              <h2 id='analysis-title' className='text-sm font-semibold'>使用分析</h2>
            </div>
            <div className='flex items-center gap-1' aria-label='趋势指标'>
              {([
                ['requests', '调用'],
                ['tokens', 'Token'],
                ['cost', '已确认成本'],
              ] as const).map(([metric, label]) => (
                <button
                  key={metric}
                  type='button'
                  aria-pressed={trendMetric === metric}
                  className={cn('min-h-9 border-b-2 px-2 text-xs transition-colors', trendMetric === metric ? 'border-primary font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground')}
                  onClick={() => setTrendMetric(metric)}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          <TrendChart buckets={props.analytics.trend} services={props.analytics.services} metric={trendMetric} />
        </div>

        <aside className='divide-y border-t xl:border-t-0 xl:pl-5' aria-labelledby='runtime-info-title'>
          <div className='py-4'>
            <div className='mb-3 flex items-center gap-2'>
              <Route aria-hidden='true' className='size-4 text-primary' />
              <h2 id='runtime-info-title' className='text-sm font-semibold'>运行信息</h2>
            </div>
            <dl className='divide-y text-xs'>
              <div className='flex items-center gap-3 py-2'>
                <dt className='text-muted-foreground'>Base URL</dt>
                <dd className='ml-auto min-w-0 truncate font-mono'>{origin}</dd>
                <Button size='icon' variant='ghost' className='size-7' aria-label='复制网关地址' onClick={copyBaseUrl}><Copy aria-hidden='true' /></Button>
              </div>
              <div className='flex items-center justify-between gap-3 py-2'><dt className='text-muted-foreground'>监听</dt><dd className='font-mono'>{props.summary.listen}</dd></div>
              <div className='flex items-center justify-between gap-3 py-2'><dt className='text-muted-foreground'>本机 API Token</dt><dd className='tabular-nums'>{props.summary.tokens}</dd></div>
              <div className='flex items-center justify-between gap-3 py-2'><dt className='text-muted-foreground'>服务计价覆盖</dt><dd className='tabular-nums'>{pricingCoverage}%</dd></div>
              <div className='flex items-center justify-between gap-3 py-2'>
                <dt className='text-muted-foreground'>号池参考余额</dt>
                <dd className='text-right tabular-nums'>
                  {referenceValues.length ? formatUSD(poolReferenceRemaining) : '未接入'}
                  {staleReferenceValues ? <span className='ml-1 text-amber-600 dark:text-amber-400'>· {staleReferenceValues} 过期</span> : null}
                </dd>
              </div>
            </dl>
          </div>
          <div className='flex items-center justify-between gap-3 py-4'>
            <div className='min-w-0'>
              <p className='flex items-center gap-2 text-xs font-medium'><KeyRound aria-hidden='true' className='size-3.5 text-primary' />控制台登录密钥</p>
              <p className='mt-1 text-[11px] text-muted-foreground'>仅写入本机受保护文件</p>
            </div>
            <AdminTokenDialog onChange={props.onChangeAdminToken} />
          </div>
          <p className='py-3 text-[11px] leading-5 text-muted-foreground'>号池参考余额是当前可用资源估值，不属于历史消耗；只有可安全换算为“每次请求”的服务价格才计入成本。</p>
        </aside>
      </section>

      <section aria-labelledby='service-table-title'>
        <div className='flex flex-wrap items-end justify-between gap-2 border-b pb-2'>
          <div>
            <h2 id='service-table-title' className='text-sm font-semibold'>服务使用情况</h2>
            <p className='mt-1 text-[11px] text-muted-foreground'>模型供应商和 Protocol Pack 按服务统一排列，保留各自的计量方式。</p>
          </div>
          <span className='text-[11px] text-muted-foreground'>可用记录累计 · {new Date(props.analytics.generated_at).toLocaleString('zh-CN')}</span>
        </div>
        <div className='overflow-x-auto' tabIndex={0} aria-label='服务使用情况表格，可横向滚动'>
          <table className='w-full min-w-[760px] text-left text-xs'>
            <thead className='border-b text-[11px] text-muted-foreground'>
              <tr><th className='py-2 pr-4 font-medium'>服务</th><th className='px-3 py-2 font-medium'>类型</th><th className='px-3 py-2 text-right font-medium'>调用</th><th className='px-3 py-2 text-right font-medium'>成功率</th><th className='px-3 py-2 text-right font-medium'>消耗 / 能力</th><th className='px-3 py-2 text-right font-medium'>成本</th><th className='py-2 pl-3 text-right font-medium'>平均延迟</th></tr>
            </thead>
            <tbody className='divide-y'>
              {props.analytics.services.map((service) => (
                <tr key={service.id} className='transition-colors hover:bg-muted/25'>
                  <td className='py-2.5 pr-4'>
                    <div className='flex min-w-0 items-center gap-2'>
                      <span className='h-4 w-1 shrink-0 rounded-full' style={{ backgroundColor: supplierColor(service.id) }} aria-hidden='true' />
                      <span className='max-w-52 truncate font-medium'>{service.name}</span>
                      <span className={cn('text-[10px]', service.status === 'ready' ? 'text-emerald-700 dark:text-emerald-300' : service.status === 'disabled' ? 'text-muted-foreground' : 'text-amber-700 dark:text-amber-300')}>{serviceStatusLabel(service.status)}</span>
                    </div>
                  </td>
                  <td className='px-3 py-2.5'><Badge variant='outline' className='font-normal'>{service.kind === 'model-provider' ? '模型' : '服务 API'}</Badge></td>
                  <td className='px-3 py-2.5 text-right tabular-nums'>{integerNumber.format(service.requests)}</td>
                  <td className='px-3 py-2.5 text-right tabular-nums'>{service.requests ? `${decimalNumber.format(service.success_rate)}%` : '—'}</td>
                  <td className='px-3 py-2.5 text-right tabular-nums'>{service.kind === 'model-provider' ? `${formatCount((service.prompt_tokens || 0) + (service.completion_tokens || 0))} Token` : `${service.operations} 项能力`}</td>
                  <td className={cn('px-3 py-2.5 text-right tabular-nums', service.cost_status === 'unavailable' && 'text-muted-foreground')}>{costLabel(service)}</td>
                  <td className='py-2.5 pl-3 text-right tabular-nums'>{formatLatency(service.average_latency_ms)}</td>
                </tr>
              ))}
              {!props.analytics.services.length ? <tr><td colSpan={7} className='py-10 text-center text-muted-foreground'>还没有可统计的服务。</td></tr> : null}
            </tbody>
          </table>
        </div>
      </section>

      <section className='grid border-y lg:grid-cols-[minmax(0,1fr)_22rem]' aria-label='模型与调用入口'>
        <div className='py-4 lg:border-r lg:pr-5'>
          <div className='mb-2 flex items-center gap-2'><RadioTower aria-hidden='true' className='size-4 text-primary' /><h2 className='text-sm font-semibold'>模型用量</h2></div>
          {props.analytics.models.length ? (
            <div className='divide-y border-t'>
              {props.analytics.models.slice(0, 6).map((model) => (
                <div key={model.name} className='grid grid-cols-[minmax(0,1fr)_4rem_6rem_5rem] items-center gap-3 py-2 text-xs'>
                  <span className='truncate font-mono'>{model.name}</span>
                  <span className='text-right tabular-nums text-muted-foreground'>{formatCount(model.requests)} 次</span>
                  <span className='text-right tabular-nums text-muted-foreground'>{formatCount(model.prompt_tokens + model.completion_tokens)} T</span>
                  <span className='text-right tabular-nums'>{formatUSD(model.cost_usd)}</span>
                </div>
              ))}
            </div>
          ) : <p className='border-t py-8 text-center text-xs text-muted-foreground'>还没有模型调用记录。</p>}
        </div>
        <div className='border-t py-4 lg:border-t-0 lg:pl-5'>
          <div className='mb-2 flex items-center gap-2'><CheckCircle2 aria-hidden='true' className='size-4 text-primary' /><h2 className='text-sm font-semibold'>兼容调用入口</h2></div>
          <div className='divide-y border-t'>
            {relayEndpoints.map(([endpoint, label]) => <div key={endpoint} className='flex items-center justify-between gap-3 py-2'><code className='min-w-0 truncate text-[11px] text-primary'>{endpoint}</code><span className='shrink-0 text-[11px] text-muted-foreground'>{label}</span></div>)}
          </div>
        </div>
      </section>
    </div>
  )
}

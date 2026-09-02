import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { ProtocolsPage } from '@/features/protocols/protocols-page'
import type { ProtocolView } from '@/lib/types'

function protocol(overrides: Partial<ProtocolView>): ProtocolView {
  return {
    id: 'video',
    name: 'Video Jobs',
    description: 'Generate video jobs',
    mount: '/p/video',
    workflow_mount: '/w/video',
    enabled: true,
    ready: true,
    status: 'ready',
    status_label: '已就绪',
    routes: [{
      operation_id: 'jobs.create',
      methods: ['POST'],
      path: '/jobs',
      summary: 'Create a video job',
      target_selector: { metadata_key: 'provider', mappings: { a: 'provider-a', b: 'provider-b' } },
    }],
    docs: {
      html: '/docs/packs/video',
      manifest: '/docs/packs/video/manifest.json',
      markdown: '/docs/packs/video/guide.md',
      examples: '/docs/packs/video/examples.json',
    },
    pool: { mode: 'local', strategy: 'least-inflight', total: 2 },
    pool_runtime: {
      ownership: 'local',
      status: 'ready',
      total: 2,
      ready: 1,
      cooling: 1,
      disabled: 0,
      expired: 0,
      busy: 0,
      balance_low: 0,
      balance_tracked: true,
      balance_remaining: 12,
      balance_empty: 0,
      quota: {
        status: 'confirmed', tracked_accounts: 2, confirmed_accounts: 2, unknown_accounts: 0, stale_accounts: 0,
        total: 20, remaining: 12, used: 8, used_percent: 40, unit: 'credits',
        reference_value: { status: 'confirmed', currency: 'USD', pricing_id: 'credit-pack', total: 0.2, remaining: 0.12, used: 0.08 },
      },
      in_flight: 0,
      accounts: [
        { ref: 'one', label: '账号 01 · abc123', status: 'ready', status_label: '可调度', balance: 7, quota: { tracked: true, status: 'confirmed', total: 10, remaining: 7, used: 3, used_percent: 30, unit: 'credits', stale: false, reference_value: { status: 'confirmed', currency: 'USD', pricing_id: 'credit-pack', total: 0.1, remaining: 0.07, used: 0.03 } }, in_flight: 0, targets: ['provider-a'] },
        { ref: 'two', label: '账号 02 · def456', status: 'cooldown', status_label: '冷却中', balance: 5, quota: { tracked: true, status: 'confirmed', total: 10, remaining: 5, used: 5, used_percent: 50, unit: 'credits', stale: false, reference_value: { status: 'confirmed', currency: 'USD', pricing_id: 'credit-pack', total: 0.1, remaining: 0.05, used: 0.05 } }, in_flight: 0, consecutive_failures: 2, targets: ['provider-b'] },
      ],
    },
    ...overrides,
  }
}

describe('ProtocolsPage master-detail pool console', () => {
  it('shows dense health and quota summaries before expanding account details', async () => {
    const user = userEvent.setup()
    render(<ProtocolsPage protocols={[protocol({})]} />)

    expect(screen.getByRole('heading', { name: 'Video Jobs' })).toBeVisible()
    expect(screen.getByRole('navigation', { name: 'Protocol Pack' })).toHaveAttribute('tabindex', '0')
    expect(screen.getByRole('region', { name: 'Video Jobs' })).toHaveAttribute('tabindex', '0')
    expect(screen.getAllByRole('img', { name: 'Video Jobs 健康 1，失效 1，额度已接入' })).toHaveLength(2)
    const quotaBars = screen.getAllByRole('progressbar', { name: 'Video Jobs 剩余额度' })
    expect(quotaBars).toHaveLength(2)
    expect(quotaBars[0]).toHaveAttribute('aria-valuenow', '60')
    expect(quotaBars[0]).toHaveClass('bg-[var(--quota-empty)]')
    expect(quotaBars[0].firstElementChild).toHaveClass('bg-[var(--quota-remaining)]')
    expect(quotaBars[0].firstElementChild).toHaveStyle({ width: '60%' })
    expect(screen.getByText('总值 0.2 USD')).toBeVisible()
    expect(screen.getByText('余 12 credits')).toBeVisible()
    expect(screen.getByText('参考总值 0.2 USD · 余值 0.12 USD')).toBeVisible()
    expect(screen.queryByRole('button', { name: '账号 01 · abc123' })).not.toBeInTheDocument()
    expect(screen.queryByText('jobs.create')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /展开 Video Jobs 号池账号/ }))
    expect(screen.getByRole('button', { name: '账号 01 · abc123' })).toBeVisible()
    expect(screen.getByText('7 credits · 0.07 USD')).toBeVisible()

    await user.click(screen.getByRole('button', { name: '查看 Video Jobs 详情' }))
    const drawer = screen.getByRole('dialog')
    expect(within(drawer).getByText('jobs.create')).toBeVisible()
    expect(within(drawer).getByText('2 后端')).toBeVisible()
    expect(within(drawer).getByRole('link', { name: /接口文档/ })).toBeVisible()
  })

  it('switches pools from the service navigation without an accordion step', async () => {
    const user = userEvent.setup()
    const searchFixture = protocol({
      id: 'search-fixture',
      name: 'Search Fixture',
      mount: '/p/search-fixture',
      pool_runtime: {
        ownership: 'local',
        status: 'ready',
        total: 1,
        ready: 1,
        cooling: 0,
        disabled: 0,
        expired: 0,
        busy: 0,
        balance_low: 0,
        balance_tracked: false,
        balance_empty: 0,
        quota: { status: 'unknown', tracked_accounts: 0, confirmed_accounts: 0, unknown_accounts: 1, stale_accounts: 0 },
        in_flight: 0,
        accounts: [{ ref: 'search-one', label: 'Fixture 账号 01', status: 'ready', status_label: '可调度', quota: { tracked: false, status: 'unknown', stale: false }, in_flight: 0 }],
      },
    })
    render(<ProtocolsPage protocols={[protocol({}), searchFixture]} />)

    const serviceNavigation = screen.getByRole('navigation', { name: 'Protocol Pack' })
    const searchButton = within(serviceNavigation).getByRole('button', { name: /Search Fixture/ })
    await user.click(searchButton)
    expect(searchButton).toHaveAttribute('aria-current', 'page')
    expect(within(serviceNavigation).queryByRole('progressbar', { name: 'Search Fixture 剩余额度' })).not.toBeInTheDocument()
    const untrackedDonuts = screen.getAllByRole('img', { name: 'Search Fixture 健康 1，失效 0，额度未接入' })
    expect(untrackedDonuts).toHaveLength(2)
    expect(untrackedDonuts[0].querySelector('circle')).toHaveClass('stroke-slate-300')
    expect(untrackedDonuts[0]).toHaveTextContent('—')
    expect(screen.queryByRole('button', { name: 'Fixture 账号 01' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /展开 Search Fixture 号池账号/ }))
    expect(screen.getByRole('button', { name: 'Fixture 账号 01' })).toBeVisible()
    expect(screen.queryByRole('button', { name: '账号 01 · abc123' })).not.toBeInTheDocument()
  })

  it('opens account operations in the right-side detail surface', async () => {
    const user = userEvent.setup()
    render(<ProtocolsPage protocols={[protocol({})]} adminToken='test-admin' />)

    await user.click(screen.getByRole('button', { name: /展开 Video Jobs 号池账号/ }))
    await user.click(screen.getByRole('button', { name: '账号 02 · def456' }))
    const drawer = screen.getByRole('dialog')
    expect(within(drawer).getByRole('heading', { name: '账号 02 · def456' })).toBeVisible()
    expect(within(drawer).getByText('连续失败')).toBeVisible()
    expect(within(drawer).getByText('provider-b')).toBeVisible()
    expect(within(drawer).getByText('0.1 USD')).toBeVisible()
    expect(within(drawer).getByText('credit-pack')).toBeVisible()
    expect(within(drawer).getByRole('button', { name: '恢复调度' })).toBeEnabled()
  })

  it('filters the current pool and keeps empty results contextual', async () => {
    const user = userEvent.setup()
    render(<ProtocolsPage protocols={[protocol({})]} />)

    await user.click(screen.getByRole('button', { name: /展开 Video Jobs 号池账号/ }))
    await user.selectOptions(screen.getByRole('combobox', { name: '筛选账号状态' }), 'cooldown')
    expect(screen.getByRole('button', { name: '账号 02 · def456' })).toBeVisible()
    expect(screen.queryByRole('button', { name: '账号 01 · abc123' })).not.toBeInTheDocument()

    await user.type(screen.getByRole('searchbox', { name: '搜索当前服务账号' }), '不存在')
    expect(screen.getByText('没有匹配的账号')).toBeVisible()
  })

  it('shows remaining-only quota without inventing a percentage', () => {
    const remainingOnly = protocol({})
    remainingOnly.pool_runtime!.quota = {
      status: 'remaining-only', tracked_accounts: 2, confirmed_accounts: 2, unknown_accounts: 0, stale_accounts: 0, remaining: 12, unit: 'credits',
    }
    remainingOnly.pool_runtime!.accounts = remainingOnly.pool_runtime!.accounts.map((account) => ({
      ...account,
      quota: { ...account.quota, total: undefined, used: undefined, used_percent: undefined },
    }))
    render(<ProtocolsPage protocols={[remainingOnly]} />)

    expect(screen.queryByRole('progressbar', { name: 'Video Jobs 剩余额度' })).not.toBeInTheDocument()
    expect(screen.getAllByText('余量 12 credits')).toHaveLength(2)
    expect(screen.getByText('仅余量 · 2/2 个账号')).toBeVisible()
  })

  it('shows provider-derived progress as a muted estimate', () => {
    const estimated = protocol({})
    estimated.pool_runtime!.quota = {
      status: 'estimated', tracked_accounts: 2, confirmed_accounts: 0, unknown_accounts: 0, stale_accounts: 0,
      total: 20, remaining: 16, used: 4, used_percent: 20, unit: 'credits',
    }
    render(<ProtocolsPage protocols={[estimated]} />)

    const estimatedBars = screen.getAllByRole('progressbar', { name: 'Video Jobs 剩余额度（估算）' })
    expect(estimatedBars).toHaveLength(2)
    expect(estimatedBars[0]).toHaveAttribute('aria-valuenow', '80')
    expect(estimatedBars[0].firstElementChild).toHaveStyle({ width: '80%' })
    expect(screen.getAllByText('估算余量 80%')).toHaveLength(2)
  })

  it('leaves the bar empty only when no quota remains', () => {
    const exhausted = protocol({})
    exhausted.pool_runtime!.quota = {
      status: 'confirmed', tracked_accounts: 2, confirmed_accounts: 2, unknown_accounts: 0, stale_accounts: 0,
      total: 20, remaining: 0, used: 20, used_percent: 100, unit: 'credits',
    }
    render(<ProtocolsPage protocols={[exhausted]} />)

    const exhaustedBars = screen.getAllByRole('progressbar', { name: 'Video Jobs 剩余额度' })
    expect(exhaustedBars).toHaveLength(2)
    expect(exhaustedBars[0]).toHaveAttribute('aria-valuenow', '0')
    expect(exhaustedBars[0].firstElementChild).toHaveStyle({ width: '0%' })
    expect(screen.getAllByText('余量 0%')).toHaveLength(2)
  })

  it('surfaces stale and mixed-unit telemetry as text, not a false aggregate', () => {
    const mixed = protocol({})
    mixed.pool_runtime!.quota = {
      status: 'mixed-unit', tracked_accounts: 2, confirmed_accounts: 1, unknown_accounts: 0, stale_accounts: 1,
      reference_value: { status: 'ambiguous' },
    }
    render(<ProtocolsPage protocols={[mixed]} />)

    expect(screen.queryByRole('progressbar', { name: 'Video Jobs 剩余额度' })).not.toBeInTheDocument()
    expect(screen.getAllByText('单位不一致')).toHaveLength(2)
    expect(screen.getByText('单位不一致 · 2/2 个账号')).toBeVisible()
    expect(screen.getByText('费率不唯一，无法折算')).toBeVisible()
  })

  it('shows concrete pricing in the service row and the main content without opening details', () => {
    const priced = protocol({
      pricing: {
        entries: [
          {
            id: 'jobs.create',
            scope: 'operation',
            label: '视频生成',
            amount: 10,
            currency: 'USD',
            unit: 'per-1000-credits',
            free_tier: '每月 200 credits',
            status: 'confirmed',
            source_url: 'https://example.test/pricing',
            source_type: 'official-pricing-page',
            checked_at: '2026-08-31',
          },
          {
            id: 'vip',
            scope: 'platform',
            status: 'unpublished',
            source_url: 'https://example.test/pricing',
            source_type: 'official-console',
            checked_at: '2026-08-31',
          },
        ],
      },
    })
    render(<ProtocolsPage protocols={[priced]} />)

    const serviceNavigation = screen.getByRole('navigation', { name: 'Protocol Pack' })
    expect(within(serviceNavigation).getByText('10 USD / 1000 credits')).toBeVisible()

    const pricingTable = screen.getByRole('table', { name: 'Video Jobs 定价' })
    expect(within(pricingTable).getByText('视频生成')).toBeVisible()
    expect(within(pricingTable).getByText('10 USD / 1000 credits')).toBeVisible()
    expect(within(pricingTable).getByText('免费层：每月 200 credits')).toBeVisible()
    expect(within(pricingTable).getByText('未公开')).toBeVisible()
    expect(within(pricingTable).getByRole('link', { name: '打开 视频生成 定价来源' })).toBeVisible()
  })
})

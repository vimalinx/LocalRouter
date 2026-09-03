import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ControlPlanePage } from '@/features/control/control-plane-page'
import type { ProtocolDraft, ProtocolView } from '@/lib/types'

const draft: ProtocolDraft = {
  id: 'agent-change',
  updated_at: '2026-08-30T00:00:00Z',
  digest: 'a'.repeat(64),
  valid: true,
  files: ['video.json', 'video/guides/usage.md', 'schema/protocol-pack-v3.schema.json'],
  protocols: [{ id: 'video', name: 'Video Jobs', routes: 2, guides: 1, workflows: 0, pool_mode: 'local' }],
  impact: {
    changed_files: 2,
    files: [
      { path: 'video.json', change: 'modified', area: 'definition', protocol_id: 'video' },
      { path: 'video/guides/usage.md', change: 'modified', area: 'guide', protocol_id: 'video' },
    ],
    protocols: [{
      id: 'video',
      name: 'Video Jobs',
      change: 'modified',
      sections: ['guides', 'pool', 'routes'],
      operations_added: ['jobs.status'],
      operations_modified: ['jobs.create'],
      pool_mode_before: 'static',
      pool_mode_after: 'local',
    }],
    pool_ids: ['video'],
  },
}

const protocol: ProtocolView = {
  id: 'video', name: 'Video Jobs', description: 'Video', mount: '/p/video', workflow_mount: '/w/video', enabled: true, ready: true, status: 'ready', status_label: '已就绪',
  routes: [], docs: { html: '/docs/packs/video', manifest: '', markdown: '', examples: '' },
  pool: { mode: 'local', strategy: 'least-inflight', total: 2 },
  pool_runtime: { ownership: 'local', status: 'ready', total: 2, ready: 1, cooling: 1, disabled: 0, expired: 0, busy: 0, balance_low: 0, balance_tracked: false, balance_empty: 0, quota: { status: 'unknown', tracked_accounts: 0, confirmed_accounts: 0, unknown_accounts: 2, stale_accounts: 0 }, in_flight: 0, accounts: [] },
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ControlPlanePage', () => {
  it('unifies Agent entry, detailed draft impact, and human pool operations', async () => {
    const localStorageValues = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => localStorageValues.get(key) ?? null,
      setItem: (key: string, value: string) => localStorageValues.set(key, value),
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"id":"video"}', { status: 200 })))
    const user = userEvent.setup()
    render(<ControlPlanePage adminToken='admin' drafts={[draft]} revisions={[]} protocols={[protocol]} onChanged={vi.fn().mockResolvedValue(undefined)} />)

    expect(screen.getByRole('heading', { name: '协议发布台' })).toBeVisible()
    expect(screen.getByText('/.well-known/localrouter.json')).toBeVisible()
    expect(screen.getByRole('cell', { name: 'video' })).toBeVisible()
    expect(screen.getByRole('button', { name: /清除异常状态/ })).toBeVisible()

    await user.click(screen.getByRole('button', { name: /管理/ }))
    const reminder = screen.getByRole('dialog', { name: '推荐使用 AI Agent' })
    expect(within(reminder).getByText('永久不再提示')).toBeVisible()
    await user.click(within(reminder).getByRole('checkbox'))
    await user.click(within(reminder).getByRole('button', { name: '继续人工编辑' }))
    expect(window.localStorage.getItem('localrouter.protocol-editor.ai-reminder-dismissed')).toBe('1')
    const sheet = screen.getByRole('dialog')
    expect(within(sheet).getByRole('heading', { name: '草稿 · agent-change' })).toBeVisible()
    expect(within(sheet).getByText('接口与转换')).toBeVisible()
    expect(within(sheet).getByText('+ jobs.status')).toBeVisible()
    expect(within(sheet).getByText('~ jobs.create')).toBeVisible()
    expect(within(sheet).getByText('static → local')).toBeVisible()
    expect(within(sheet).getByRole('textbox', { name: '编辑 video.json' })).toBeVisible()

    await user.click(within(sheet).getByRole('button', { name: 'schema/protocol-pack-v3.schema.json' }))
    expect(within(sheet).getByRole('textbox', { name: '查看 schema/protocol-pack-v3.schema.json' })).toHaveAttribute('readonly')
    expect(within(sheet).getByText('只读')).toBeVisible()
  })
})

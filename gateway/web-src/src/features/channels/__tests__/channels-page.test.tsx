import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { ChannelsPage } from '@/features/channels/channels-page'

describe('ChannelsPage supplier request profile', () => {
  it('edits an existing supplier without requiring its stored key', async () => {
    const user = userEvent.setup()
    render(
      <ChannelsPage
        adminToken='test-admin'
        providers={[{ id: 1, key: 'openai-compatible', name: 'OpenAI compatible', base_url: 'https://example.test', requires_key: true }]}
        channels={[{
          id: 7,
          name: 'Provider A',
          type: 1,
          base_url: 'https://example.test/v1',
          models: 'model-a',
          status: 1,
          upstream_profile: {
            set_headers: { 'X-Provider-Mode': 'silent' },
            remove_headers: ['X-Debug'],
            user_agent: 'omit',
            query: 'preserve-raw',
          },
        }]}
        onChanged={vi.fn().mockResolvedValue(undefined)}
      />
    )

    await user.click(screen.getByRole('button', { name: '编辑渠道 Provider A' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('heading', { name: '编辑 Provider A' })).toBeVisible()
    expect(within(dialog).getByPlaceholderText('留空保留当前密钥')).toHaveValue('')

    await user.click(within(dialog).getByText('供应商请求处理'))
    expect(within(dialog).getByLabelText('固定请求头')).toHaveValue('X-Provider-Mode: silent')
    expect(within(dialog).getByLabelText('发送前删除')).toHaveValue('X-Debug')
    expect(within(dialog).getByLabelText('User-Agent')).toHaveValue('omit')
    expect(within(dialog).getByLabelText('查询串')).toHaveValue('preserve-raw')
  })
})

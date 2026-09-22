import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ServicesPage } from '@/features/services/services-page'

vi.mock('@/lib/console-data', () => ({ useConsoleData: () => ({ data: { drafts: [], revisions: [] }, refetch: vi.fn() }) }))
vi.mock('@/features/protocols/protocols-page', () => ({ ProtocolsPage: () => <p>服务列表内容</p> }))
vi.mock('@/features/channels/channels-page', () => ({ ChannelsPage: () => <p>模型列表内容</p> }))
vi.mock('@/features/services/service-allowances', () => ({ ServiceAllowances: () => <p>额度内容</p> }))
vi.mock('@/features/control/control-plane-page', () => ({ ControlPlanePage: () => <p>编辑器内容</p> }))

const props = { adminToken: '', protocols: [], channels: [], providers: [], onChanged: vi.fn() }

describe('service workspace navigation', () => {
  it('follows changed legacy destinations without requiring a page reload', async () => {
    const { rerender } = render(<ServicesPage {...props} initialTab='models' />)
    expect(await screen.findByText('模型列表内容')).toBeVisible()
    rerender(<ServicesPage {...props} initialTab='services' />)
    expect(screen.getByRole('tab', { name: '服务号池' })).toHaveAttribute('aria-selected', 'true')
    rerender(<ServicesPage {...props} initialTab='services' initialEditorOpen />)
    expect(await screen.findByText('编辑器内容')).toBeVisible()
  })

  it('uses keyboard navigation to select and focus all service categories', async () => {
    const user = userEvent.setup()
    render(<ServicesPage {...props} />)
    screen.getByRole('tab', { name: '服务号池' }).focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: '模型渠道' })).toHaveFocus()
    expect(await screen.findByText('模型列表内容')).toBeVisible()
    await user.keyboard('{End}')
    expect(screen.getByRole('tab', { name: '自主额度' })).toHaveFocus()
    expect(await screen.findByText('额度内容')).toBeVisible()
    await user.keyboard('{Home}')
    expect(screen.getByRole('tab', { name: '服务号池' })).toHaveFocus()
    expect(screen.getByRole('tabpanel', { name: '服务号池' })).toHaveTextContent('服务列表内容')
  })
})

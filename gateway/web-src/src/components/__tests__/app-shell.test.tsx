import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { AppShell } from '@/components/app-shell'

describe('AppShell mobile navigation', () => {
  it('reports the collapsed state and requests opening through the menu button', async () => {
    const user = userEvent.setup()
    const setOpen = vi.fn()
    render(
      <AppShell
        activeSection='overview'
        mobileOpen={false}
        dark={false}
        refreshing={false}
        listener='127.0.0.1:8317'
        onNavigate={vi.fn()}
        onMobileOpenChange={setOpen}
        onThemeToggle={vi.fn()}
        onRefresh={vi.fn()}
        onLock={vi.fn()}
      >
        <p>content</p>
      </AppShell>
    )

    const menu = screen.getByRole('button', { name: '打开导航' })
    expect(menu).toHaveAttribute('aria-expanded', 'false')
    expect(menu).toHaveAttribute('aria-controls', 'mobile-navigation')
    await user.click(menu)
    expect(setOpen).toHaveBeenCalledWith(true)
  })

  it('marks only the current navigation destination as the page', () => {
    render(
      <AppShell
        activeSection='protocols'
        mobileOpen={true}
        dark={false}
        refreshing={false}
        listener='127.0.0.1:8317'
        onNavigate={vi.fn()}
        onMobileOpenChange={vi.fn()}
        onThemeToggle={vi.fn()}
        onRefresh={vi.fn()}
        onLock={vi.fn()}
      >
        <p>content</p>
      </AppShell>
    )

    expect(screen.getByRole('link', { name: /服务与渠道/ })).toHaveAttribute(
      'aria-current',
      'page'
    )
    expect(screen.getByRole('link', { name: /运行概览/ })).not.toHaveAttribute('aria-current')
    expect(screen.getByText('AGPL-3.0')).toBeVisible()
    expect(screen.queryByRole('link', { name: /历史来源/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /协议发布/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /^模型渠道$/ })).not.toBeInTheDocument()
    expect(document.querySelector('#main-content')).toHaveClass('overflow-hidden')
  })

  it('keeps other sections inside the fixed shell with one main scroll region', () => {
    render(
      <AppShell
        activeSection='overview'
        mobileOpen={false}
        dark={false}
        refreshing={false}
        listener='127.0.0.1:8317'
        onNavigate={vi.fn()}
        onMobileOpenChange={vi.fn()}
        onThemeToggle={vi.fn()}
        onRefresh={vi.fn()}
        onLock={vi.fn()}
      >
        <p>content</p>
      </AppShell>
    )

    expect(document.querySelector('#main-content')).toHaveClass('overflow-y-auto')
  })
})

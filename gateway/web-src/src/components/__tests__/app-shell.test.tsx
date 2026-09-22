import { render, screen, within } from '@testing-library/react'
import { useState } from 'react'
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
    expect(within(screen.getByRole('dialog')).getByText('AGPL-3.0')).toBeVisible()
    expect(screen.queryByRole('link', { name: /历史来源/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /协议发布/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /^模型渠道$/ })).not.toBeInTheDocument()
    expect(document.querySelector('#main-content')).toHaveClass('overflow-hidden')
  })

  it('contains mobile keyboard focus and restores the trigger after Escape', async () => {
    const user = userEvent.setup()
    function MobileShell() {
      const [open, setOpen] = useState(false)
      return <AppShell activeSection='overview' mobileOpen={open} dark={false} refreshing={false} listener='' onNavigate={vi.fn()} onMobileOpenChange={setOpen} onThemeToggle={vi.fn()} onRefresh={vi.fn()}><button>页面操作</button></AppShell>
    }
    render(<MobileShell />)
    const menu = screen.getByRole('button', { name: '打开导航' })
    await user.click(menu)
    const dialog = screen.getByRole('dialog', { name: '控制台导航' })
    expect(dialog).toContainElement(document.activeElement as HTMLElement)
    await user.keyboard('{Shift>}{Tab}{/Shift}')
    expect(dialog).toContainElement(document.activeElement as HTMLElement)
    expect(screen.queryByRole('button', { name: '页面操作' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(menu).toHaveFocus()
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

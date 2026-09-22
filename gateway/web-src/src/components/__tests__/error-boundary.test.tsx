import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ErrorBoundary } from '@/components/error-boundary'

describe('page error recovery', () => {
  it('preserves a recovery action and allows navigation to a healthy page', () => {
    const report = vi.spyOn(console, 'error').mockImplementation(() => {})
    function BrokenPage(): never { throw new Error('fixture import failure') }
    try {
      const { rerender } = render(<ErrorBoundary key='broken'><BrokenPage /></ErrorBoundary>)
      expect(screen.getByRole('alert')).toHaveTextContent('页面暂时无法显示')
      expect(screen.getByRole('button', { name: '重新加载页面' })).toBeVisible()
      rerender(<ErrorBoundary key='healthy'><p>页面已恢复</p></ErrorBoundary>)
      expect(screen.getByText('页面已恢复')).toBeVisible()
    } finally { report.mockRestore() }
  })
})

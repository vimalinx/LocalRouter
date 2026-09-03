import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ActivationToggle } from '@/components/activation-toggle'

describe('ActivationToggle', () => {
  it('keeps the thumb anchored inside the track in both states', () => {
    const { rerender } = render(<ActivationToggle checked={false} label='启用服务' onChange={vi.fn()} />)
    const track = screen.getByRole('switch').querySelector('span[aria-hidden="true"]')
    const thumb = track?.querySelector('span')

    expect(track).toHaveClass('relative', 'w-7')
    expect(thumb).toHaveClass('left-0.5', 'translate-x-0')

    rerender(<ActivationToggle checked label='停用服务' onChange={vi.fn()} />)
    const enabledThumb = screen.getByRole('switch').querySelector('span[aria-hidden="true"] span')
    expect(enabledThumb).toHaveClass('left-0.5', 'translate-x-3')
  })
})

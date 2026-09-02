import { describe, expect, it } from 'vitest'

import { supplierColor, supplierColorSlot } from '@/lib/supplier-colors'

describe('supplier colors', () => {
  it('assigns every operator-defined provider a valid deterministic slot', () => {
    const first = supplierColorSlot('channel:custom-provider')
    expect(first).toBeGreaterThanOrEqual(0)
    expect(first).toBeLessThan(12)
    expect(supplierColorSlot('channel:custom-provider')).toBe(first)
    expect(supplierColor('protocol:search-primary')).toBe(supplierColor('protocol:search-primary'))
  })
})

const supplierColorCount = 12

export function supplierColorSlot(id: string) {
  let hash = 2166136261
  for (let index = 0; index < id.length; index += 1) {
    hash ^= id.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0) % supplierColorCount
}

export function supplierColor(id: string) {
  return `var(--supplier-${supplierColorSlot(id) + 1})`
}

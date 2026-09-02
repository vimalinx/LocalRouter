import type * as React from 'react'

import { cn } from '@/lib/utils'

function Label(props: React.ComponentProps<'label'>) {
  return (
    <label
      data-slot='label'
      {...props}
      className={cn('text-sm font-medium leading-none', props.className)}
    />
  )
}

export { Label }

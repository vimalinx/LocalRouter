import type * as React from 'react'

import { cn } from '@/lib/utils'

function Separator(props: React.ComponentProps<'div'>) {
  return (
    <div
      role='separator'
      data-slot='separator'
      {...props}
      className={cn('h-px w-full shrink-0 bg-border', props.className)}
    />
  )
}

export { Separator }

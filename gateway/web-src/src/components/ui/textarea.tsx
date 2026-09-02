import type * as React from 'react'

import { cn } from '@/lib/utils'

function Textarea(props: React.ComponentProps<'textarea'>) {
  return (
    <textarea
      data-slot='textarea'
      {...props}
      className={cn(
        'min-h-24 w-full resize-y rounded-md border border-input bg-background px-3 py-2 text-sm outline-none transition-colors placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50',
        props.className
      )}
    />
  )
}

export { Textarea }

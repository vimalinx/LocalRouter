import type * as React from 'react'

import { cn } from '@/lib/utils'

function Card(props: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card'
      {...props}
      className={cn(
        'border-y border-border bg-transparent text-card-foreground',
        props.className
      )}
    />
  )
}

function CardHeader(props: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-header'
      {...props}
      className={cn('flex flex-col gap-1.5 px-0 py-4', props.className)}
    />
  )
}

function CardTitle(props: React.ComponentProps<'h3'>) {
  return (
    <h3
      data-slot='card-title'
      {...props}
      className={cn('font-semibold leading-none tracking-tight', props.className)}
    />
  )
}

function CardDescription(props: React.ComponentProps<'p'>) {
  return (
    <p
      data-slot='card-description'
      {...props}
      className={cn('text-sm leading-5 text-muted-foreground', props.className)}
    />
  )
}

function CardContent(props: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-content'
      {...props}
      className={cn('pb-4', props.className)}
    />
  )
}

function CardFooter(props: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-footer'
      {...props}
      className={cn('flex items-center pb-4', props.className)}
    />
  )
}

export { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle }

import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'

import { cn } from '@/lib/utils'

const Dialog = DialogPrimitive.Root
const DialogTrigger = DialogPrimitive.Trigger
const DialogClose = DialogPrimitive.Close

function DialogContent(
  props: React.ComponentProps<typeof DialogPrimitive.Content>
) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className='fixed inset-0 z-50 bg-slate-950/55 backdrop-blur-[2px] data-[state=closed]:animate-out data-[state=open]:animate-in' />
      <DialogPrimitive.Content
        {...props}
        className={cn(
          'fixed left-1/2 top-1/2 z-50 grid max-h-[90svh] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 gap-4 overflow-y-auto rounded-lg border bg-background p-6 shadow-xl outline-none focus-visible:ring-2 focus-visible:ring-ring',
          props.className
        )}
      >
        {props.children}
        <DialogPrimitive.Close className='absolute right-4 top-4 flex size-10 cursor-pointer items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'>
          <X aria-hidden='true' className='size-4' />
          <span className='sr-only'>关闭</span>
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

function DialogHeader(props: React.ComponentProps<'div'>) {
  return <div {...props} className={cn('grid gap-2 pr-8', props.className)} />
}

function DialogTitle(
  props: React.ComponentProps<typeof DialogPrimitive.Title>
) {
  return (
    <DialogPrimitive.Title
      {...props}
      className={cn('text-lg font-semibold', props.className)}
    />
  )
}

function DialogDescription(
  props: React.ComponentProps<typeof DialogPrimitive.Description>
) {
  return (
    <DialogPrimitive.Description
      {...props}
      className={cn('text-sm leading-5 text-muted-foreground', props.className)}
    />
  )
}

function DialogFooter(props: React.ComponentProps<'div'>) {
  return (
    <div
      {...props}
      className={cn('flex flex-col-reverse gap-2 sm:flex-row sm:justify-end', props.className)}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
}

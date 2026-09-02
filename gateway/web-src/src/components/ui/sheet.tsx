import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type * as React from 'react'

import { cn } from '@/lib/utils'

const Sheet = DialogPrimitive.Root
const SheetTrigger = DialogPrimitive.Trigger
const SheetClose = DialogPrimitive.Close

function SheetContent(props: React.ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className='fixed inset-0 z-50 bg-slate-950/55 backdrop-blur-[1px] transition-opacity data-[state=closed]:opacity-0 data-[state=open]:opacity-100' />
      <DialogPrimitive.Content
        {...props}
        className={cn(
          'fixed inset-y-0 right-0 z-50 flex h-svh w-full flex-col border-l bg-background shadow-xl outline-none transition-transform duration-200 data-[state=closed]:translate-x-full data-[state=open]:translate-x-0 motion-reduce:transition-none sm:max-w-[30rem]',
          props.className
        )}
      >
        {props.children}
        <DialogPrimitive.Close className='absolute right-3 top-3 flex size-10 cursor-pointer items-center justify-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring'>
          <X aria-hidden='true' className='size-4' />
          <span className='sr-only'>关闭</span>
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

function SheetHeader(props: React.ComponentProps<'div'>) {
  return <div {...props} className={cn('border-b px-5 py-4 pr-16', props.className)} />
}

function SheetTitle(props: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title {...props} className={cn('text-base font-semibold', props.className)} />
}

function SheetDescription(props: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description {...props} className={cn('mt-1 text-xs text-muted-foreground', props.className)} />
}

function SheetBody(props: React.ComponentProps<'div'>) {
  return <div {...props} className={cn('min-h-0 flex-1 overflow-y-auto px-5 py-4', props.className)} />
}

function SheetFooter(props: React.ComponentProps<'div'>) {
  return <div {...props} className={cn('flex flex-wrap justify-end gap-2 border-t px-5 py-3', props.className)} />
}

export {
  Sheet,
  SheetBody,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
}

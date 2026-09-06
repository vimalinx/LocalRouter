import { useEffect, useRef, type ComponentProps } from 'react'
import { Check, Minus } from 'lucide-react'
import { cn } from '@/lib/utils'

export function Checkbox({ indeterminate = false, className, ...props }: Omit<ComponentProps<'input'>, 'type'> & { indeterminate?: boolean }) {
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => { if (input.current) input.current.indeterminate = indeterminate }, [indeterminate])
  const Icon = indeterminate ? Minus : Check
  return <span className={cn('relative inline-flex size-6 shrink-0 items-center justify-center align-middle', className)}>
    <input {...props} ref={input} type='checkbox' className='peer absolute inset-0 z-10 m-0 size-full cursor-pointer opacity-0 disabled:cursor-not-allowed' />
    <span aria-hidden='true' className='pointer-events-none flex size-4 items-center justify-center rounded-[4px] border !border-muted-foreground/60 bg-muted/20 text-transparent shadow-xs transition-colors peer-hover:!border-primary/70 peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background peer-checked:!border-primary peer-checked:bg-primary peer-checked:text-primary-foreground peer-indeterminate:!border-primary peer-indeterminate:bg-primary peer-indeterminate:text-primary-foreground peer-disabled:opacity-40'>
      <Icon className='size-3' strokeWidth={3} />
    </span>
  </span>
}

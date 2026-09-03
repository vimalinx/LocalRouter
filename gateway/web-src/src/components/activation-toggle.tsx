import { Power } from 'lucide-react'

import { cn } from '@/lib/utils'

export function ActivationToggle(props: {
  checked: boolean
  label: string
  disabled?: boolean
  busy?: boolean
  compact?: boolean
  onChange: () => void
}) {
  return (
    <button
      type='button'
      role='switch'
      aria-checked={props.checked}
      aria-label={props.label}
      disabled={props.disabled || props.busy}
      className={cn(
        'inline-flex min-h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-2 text-[11px] font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
        props.checked
          ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
          : 'border-border bg-muted/45 text-muted-foreground'
      )}
      onClick={(event) => { event.stopPropagation(); props.onChange() }}
    >
      <Power aria-hidden='true' className={cn('size-3.5', props.busy && 'animate-pulse')} />
      {props.compact ? null : props.checked ? '启用' : '停用'}
      <span aria-hidden='true' className={cn('relative inline-block h-4 w-7 shrink-0 rounded-full transition-colors', props.checked ? 'bg-emerald-500/70' : 'bg-muted-foreground/25')}>
        <span className={cn('absolute left-0.5 top-0.5 size-3 rounded-full bg-background shadow-sm transition-transform', props.checked ? 'translate-x-3' : 'translate-x-0')} />
      </span>
    </button>
  )
}

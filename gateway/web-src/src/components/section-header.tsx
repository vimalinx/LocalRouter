import type { ReactNode } from 'react'

export function SectionHeader(props: {
  eyebrow?: string
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <header className='flex flex-col gap-3 border-b pb-3 sm:flex-row sm:items-center sm:justify-between'>
      <div className='min-w-0'>
        {props.eyebrow ? (
          <p className='mb-1 text-[11px] font-semibold uppercase tracking-[0.14em] text-primary'>
            {props.eyebrow}
          </p>
        ) : null}
        <h1 className='text-xl font-semibold tracking-tight sm:text-2xl'>
          {props.title}
        </h1>
        {props.description ? (
          <p className='mt-1 max-w-3xl text-sm leading-5 text-muted-foreground'>
            {props.description}
          </p>
        ) : null}
      </div>
      {props.actions ? (
        <div className='flex shrink-0 flex-wrap items-center gap-2'>
          {props.actions}
        </div>
      ) : null}
    </header>
  )
}

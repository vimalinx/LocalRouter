import type { LucideIcon } from 'lucide-react'

export function EmptyState(props: {
  icon: LucideIcon
  title: string
  description: string
}) {
  const Icon = props.icon
  return (
    <div className='flex min-h-48 flex-col items-center justify-center px-6 py-10 text-center'>
      <span className='mb-3 flex size-10 items-center justify-center text-muted-foreground'>
        <Icon aria-hidden='true' className='size-5' />
      </span>
      <h3 className='font-medium'>{props.title}</h3>
      <p className='mt-1 max-w-md text-sm leading-5 text-muted-foreground'>
        {props.description}
      </p>
    </div>
  )
}

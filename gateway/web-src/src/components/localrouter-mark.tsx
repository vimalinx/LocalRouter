import { cn } from '@/lib/utils'

export function LocalRouterMark(props: { className?: string }) {
  return (
    <svg
      aria-hidden='true'
      className={cn('shrink-0', props.className)}
      viewBox='0 0 64 64'
      xmlns='http://www.w3.org/2000/svg'
    >
      <rect x='2' y='2' width='60' height='60' rx='15' fill='#6D5CE7' />
      <g fill='none' stroke='#FFFFFF' strokeLinecap='round' strokeLinejoin='round' strokeWidth='5'>
        <path d='M15 18h7c7 0 7 10 15 14' />
        <path d='M15 32h22' />
        <path d='M15 46h7c7 0 7-10 15-14' />
        <path d='M37 32h14' />
        <path d='m47 26 6 6-6 6' />
      </g>
      <circle cx='37' cy='32' r='5' fill='#83D5E4' />
    </svg>
  )
}

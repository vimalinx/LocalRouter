import type { HTMLAttributes, TableHTMLAttributes, TdHTMLAttributes, ThHTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

export function Table(props: TableHTMLAttributes<HTMLTableElement>) {
  return <div className='w-full overflow-x-auto'><table {...props} className={cn('w-full caption-bottom text-sm', props.className)} /></div>
}
export function TableHeader(props: HTMLAttributes<HTMLTableSectionElement>) { return <thead {...props} className={cn('[&_tr]:border-b', props.className)} /> }
export function TableBody(props: HTMLAttributes<HTMLTableSectionElement>) { return <tbody {...props} className={cn('[&_tr:last-child]:border-0', props.className)} /> }
export function TableRow(props: HTMLAttributes<HTMLTableRowElement>) { return <tr {...props} className={cn('border-b transition-colors hover:bg-muted/30', props.className)} /> }
export function TableHead(props: ThHTMLAttributes<HTMLTableCellElement>) { return <th {...props} className={cn('h-10 px-3 text-left align-middle text-xs font-medium text-muted-foreground', props.className)} /> }
export function TableCell(props: TdHTMLAttributes<HTMLTableCellElement>) { return <td {...props} className={cn('px-3 py-3 align-middle', props.className)} /> }

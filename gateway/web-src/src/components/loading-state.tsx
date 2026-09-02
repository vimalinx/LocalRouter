export function LoadingState(props: { label?: string }) {
  return (
    <div
      className='flex min-h-48 items-center justify-center gap-3 text-sm text-muted-foreground'
      role='status'
    >
      <span className='size-4 animate-spin rounded-full border-2 border-primary/25 border-t-primary motion-reduce:animate-none' />
      {props.label || '正在读取本机网关…'}
    </div>
  )
}

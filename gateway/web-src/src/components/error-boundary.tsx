import { Component, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'

export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <section role='alert' className='flex min-h-64 flex-col items-center justify-center gap-3 border-y p-6 text-center'>
        <h1 className='font-semibold'>页面暂时无法显示</h1>
        <p className='max-w-lg text-sm text-muted-foreground'>页面资源可能已更新，或当前内容加载失败。可以切换其他页面，或重新加载控制台。</p>
        <Button variant='outline' onClick={() => window.location.reload()}>重新加载页面</Button>
      </section>
    )
  }
}

import type { LucideIcon } from 'lucide-react'
import {
  Activity,
  BookOpen,
  Boxes,
  ChevronRight,
  KeyRound,
  LockKeyhole,
  Menu,
  Moon,
  RefreshCw,
  ScrollText,
  Workflow,
  Waypoints,
  Sun,
} from 'lucide-react'
import { useEffect, type ReactNode } from 'react'

import { LocalRouterMark } from '@/components/localrouter-mark'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'

export type SectionId = 'overview' | 'protocols' | 'jobs' | 'tokens' | 'logs' | 'setup'

const navigation: Array<{
  id: SectionId
  label: string
  icon: LucideIcon
  advanced?: boolean
}> = [
  { id: 'overview', label: '运行概览', icon: Activity },
  { id: 'protocols', label: '服务与渠道', icon: Boxes },
  { id: 'tokens', label: 'Token 管理', icon: KeyRound },
  { id: 'logs', label: '请求日志', icon: ScrollText },
  { id: 'setup', label: '接入与授权', icon: Waypoints, advanced: true },
  { id: 'jobs', label: '任务与事件', icon: Workflow, advanced: true },
]

export function AppShell(props: {
  activeSection: SectionId
  mobileOpen: boolean
  dark: boolean
  refreshing: boolean
  listener: string
  connection?: 'online' | 'degraded' | 'unavailable'
  children: ReactNode
  onNavigate: (section: SectionId) => void
  onMobileOpenChange: (open: boolean) => void
  onThemeToggle: () => void
  onRefresh: () => void
  onLock?: () => void
}) {
  const protocolsWorkspace = props.activeSection === 'protocols'
  const advancedActive = navigation.some(item => item.advanced && item.id === props.activeSection)
  function navigationItem(item: typeof navigation[number]) {
    const Icon = item.icon
    const selected = props.activeSection === item.id
    return (
      <li key={item.id}>
        <a
          href={`#${item.id}`}
          aria-current={selected ? 'page' : undefined}
          className={cn(
            'group flex min-h-11 cursor-pointer items-center gap-3 rounded-md px-3 py-2 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring',
            selected ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'
          )}
          onClick={() => props.onNavigate(item.id)}
        >
          <Icon aria-hidden='true' className='size-[18px] shrink-0' />
          <span className='min-w-0 flex-1 truncate text-sm font-medium text-current'>{item.label}</span>
          {selected ? <span aria-hidden='true' className='h-5 w-0.5 bg-primary' /> : null}
        </a>
      </li>
    )
  }

  useEffect(() => {
    if (!window.matchMedia) return
    const desktop = window.matchMedia('(min-width: 1024px)')
    const closeOnDesktop = () => { if (desktop.matches && props.mobileOpen) props.onMobileOpenChange(false) }
    closeOnDesktop()
    desktop.addEventListener('change', closeOnDesktop)
    return () => desktop.removeEventListener('change', closeOnDesktop)
  }, [props.mobileOpen, props.onMobileOpenChange])

  const sidebar = <>
    <div className='flex h-12 items-center gap-3 px-4'>
      <LocalRouterMark className='size-8' />
      <p className='min-w-0 flex-1 truncate font-semibold tracking-tight'>LocalRouter</p>
    </div>
    <Separator />

    <nav className='flex-1 overflow-y-auto px-2 py-3' aria-label='控制台导航'>
      <ul className='space-y-1'>{navigation.filter(item => !item.advanced).map(navigationItem)}</ul>
      <details className='group mt-4 border-t pt-3' open={advancedActive || undefined}>
        <summary className='flex min-h-10 cursor-pointer list-none items-center gap-2 rounded-md px-3 text-xs font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden'>
          <ChevronRight aria-hidden='true' className='size-3.5 transition-transform group-open:rotate-90' />更多工具
        </summary>
        <ul className='mt-1 space-y-1'>{navigation.filter(item => item.advanced).map(navigationItem)}</ul>
      </details>
    </nav>

    <div className='border-t p-3'>
      <div className='px-1 py-2'>
        <div className='flex items-center justify-between gap-2'>
          <span className='text-xs font-medium'>本机监听</span>
          <Badge variant={props.connection === 'unavailable' ? 'destructive' : props.connection === 'degraded' ? 'warning' : 'success'}>
            {props.connection === 'unavailable' ? '连接中断' : props.connection === 'degraded' ? '数据不完整' : '在线'}
          </Badge>
        </div>
        <code className='mt-2 block truncate text-[11px] text-muted-foreground'>
          {props.listener || '127.0.0.1:8317'}
        </code>
      </div>
      <a
        href='/docs'
        className='mt-1 flex min-h-11 cursor-pointer items-center gap-3 rounded-md px-1 text-sm font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring'
      >
        <BookOpen aria-hidden='true' className='size-4' />
        接口文档
      </a>
      <p className='flex min-h-9 items-center px-1 text-xs text-muted-foreground'>AGPL-3.0</p>
    </div>
  </>

  return (
    <Sheet open={props.mobileOpen} onOpenChange={props.onMobileOpenChange}>
      <div className='h-svh overflow-hidden lg:grid lg:grid-cols-[15rem_minmax(0,1fr)]'>
        <aside aria-hidden={props.mobileOpen || undefined} className='hidden h-svh flex-col border-r bg-background lg:flex'>
          {sidebar}
        </aside>
        <SheetContent id='mobile-navigation' aria-describedby={undefined} className='left-0 right-auto w-[15rem] border-l-0 border-r data-[state=closed]:-translate-x-full sm:max-w-[15rem]'>
          <SheetTitle className='sr-only'>控制台导航</SheetTitle>
          {sidebar}
        </SheetContent>

        <div className='flex h-svh min-w-0 flex-col overflow-hidden'>
          <header className='z-30 flex h-12 shrink-0 items-center gap-3 border-b bg-background px-4 sm:px-5'>
            <SheetTrigger asChild>
              <Button
                className='lg:hidden'
                variant='outline'
                size='icon'
                aria-label='打开导航'
                aria-controls='mobile-navigation'
                aria-expanded={props.mobileOpen}
              >
                <Menu aria-hidden='true' />
              </Button>
            </SheetTrigger>
            <span className='min-w-0 flex-1' />
            <div className='flex items-center gap-1 sm:gap-2'>
              <Button
                variant='ghost'
                size='icon'
                aria-label='刷新当前数据'
                disabled={props.refreshing}
                onClick={props.onRefresh}
              >
                <RefreshCw
                  aria-hidden='true'
                  className={cn(props.refreshing && 'animate-spin motion-reduce:animate-none')}
                />
              </Button>
              <Button
                variant='ghost'
                size='icon'
                aria-label={props.dark ? '切换到浅色主题' : '切换到深色主题'}
                onClick={props.onThemeToggle}
              >
                {props.dark ? <Sun aria-hidden='true' /> : <Moon aria-hidden='true' />}
              </Button>
              {props.onLock ? (
                <Button variant='outline' size='sm' onClick={props.onLock}>
                  <LockKeyhole aria-hidden='true' />
                  <span className='hidden sm:inline'>锁定</span>
                </Button>
              ) : null}
            </div>
          </header>

          <main
            id='main-content'
            tabIndex={-1}
            className={cn(
              'mx-auto min-h-0 w-full max-w-[1440px] flex-1 p-4 outline-none sm:p-5 lg:p-6',
              protocolsWorkspace ? 'flex flex-col overflow-hidden' : 'overflow-y-auto overscroll-contain [scrollbar-gutter:stable]'
            )}
          >
            {props.children}
          </main>
        </div>
      </div>
    </Sheet>
  )
}

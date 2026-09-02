import type { LucideIcon } from 'lucide-react'
import {
  Activity,
  BookOpen,
  Boxes,
  GitBranch,
  KeyRound,
  LockKeyhole,
  Menu,
  Moon,
  RadioTower,
  RefreshCw,
  ScrollText,
  Workflow,
  Sun,
  X,
} from 'lucide-react'
import type { ReactNode } from 'react'

import { LocalRouterMark } from '@/components/localrouter-mark'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { cn } from '@/lib/utils'

export type SectionId = 'overview' | 'protocols' | 'control' | 'jobs' | 'channels' | 'tokens' | 'logs'

const navigation: Array<{
  id: SectionId
  label: string
  icon: LucideIcon
}> = [
  { id: 'overview', label: '运行概览', icon: Activity },
  { id: 'protocols', label: '服务与号池', icon: Boxes },
  { id: 'control', label: 'Agent 工作台', icon: GitBranch },
  { id: 'jobs', label: '任务与事件', icon: Workflow },
  { id: 'channels', label: '模型渠道', icon: RadioTower },
  { id: 'tokens', label: 'API 令牌', icon: KeyRound },
  { id: 'logs', label: '请求日志', icon: ScrollText },
]

export function AppShell(props: {
  activeSection: SectionId
  mobileOpen: boolean
  dark: boolean
  refreshing: boolean
  listener: string
  children: ReactNode
  onNavigate: (section: SectionId) => void
  onMobileOpenChange: (open: boolean) => void
  onThemeToggle: () => void
  onRefresh: () => void
  onLock: () => void
}) {
  const protocolsWorkspace = props.activeSection === 'protocols'

  return (
    <div className='h-svh overflow-hidden lg:grid lg:grid-cols-[15rem_minmax(0,1fr)]'>
      {props.mobileOpen ? (
        <button
          type='button'
          aria-label='关闭导航'
          className='fixed inset-0 z-40 cursor-pointer bg-slate-950/40 backdrop-blur-[1px] lg:hidden'
          onClick={() => props.onMobileOpenChange(false)}
        />
      ) : null}

      <aside
        id='mobile-navigation'
        className={cn(
          'fixed inset-y-0 left-0 z-50 flex w-[15rem] flex-col border-r bg-background transition-transform duration-200 lg:sticky lg:top-0 lg:z-20 lg:h-svh lg:translate-x-0',
          props.mobileOpen ? 'translate-x-0' : '-translate-x-full'
        )}
      >
        <div className='flex h-12 items-center gap-3 px-4'>
          <LocalRouterMark className='size-8' />
          <p className='min-w-0 flex-1 truncate font-semibold tracking-tight'>LocalRouter</p>
          <Button
            className='lg:hidden'
            variant='ghost'
            size='icon'
            aria-label='关闭导航'
            onClick={() => props.onMobileOpenChange(false)}
          >
            <X aria-hidden='true' />
          </Button>
        </div>
        <Separator />

        <nav className='flex-1 overflow-y-auto px-2 py-3' aria-label='控制台导航'>
          <ul className='space-y-1'>
            {navigation.map((item) => {
              const Icon = item.icon
              const selected = props.activeSection === item.id
              return (
                <li key={item.id}>
                  <a
                    href={`#${item.id}`}
                    aria-current={selected ? 'page' : undefined}
                    className={cn(
                      'group flex min-h-11 cursor-pointer items-center gap-3 rounded-md px-3 py-2 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring',
                      selected
                        ? 'bg-muted text-foreground'
                        : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                    )}
                    onClick={() => props.onNavigate(item.id)}
                  >
                    <Icon aria-hidden='true' className='size-[18px] shrink-0' />
                    <span className='min-w-0 flex-1 truncate text-sm font-medium text-current'>
                      {item.label}
                    </span>
                    {selected ? <span aria-hidden='true' className='h-5 w-0.5 bg-primary' /> : null}
                  </a>
                </li>
              )
            })}
          </ul>
        </nav>

        <div className='border-t p-3'>
          <div className='px-1 py-2'>
            <div className='flex items-center justify-between gap-2'>
              <span className='text-xs font-medium'>本机监听</span>
              <Badge variant='success'>在线</Badge>
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
      </aside>

      <div className='flex h-svh min-w-0 flex-col overflow-hidden'>
        <header className='z-30 flex h-12 shrink-0 items-center gap-3 border-b bg-background px-4 sm:px-5'>
          <Button
            className='lg:hidden'
            variant='outline'
            size='icon'
            aria-label='打开导航'
            aria-controls='mobile-navigation'
            aria-expanded={props.mobileOpen}
            onClick={() => props.onMobileOpenChange(true)}
          >
            <Menu aria-hidden='true' />
          </Button>
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
            <Button variant='outline' size='sm' onClick={props.onLock}>
              <LockKeyhole aria-hidden='true' />
              <span className='hidden sm:inline'>锁定</span>
            </Button>
          </div>
        </header>

        <main
          id='main-content'
          tabIndex={-1}
          className={cn(
            'mx-auto min-h-0 w-full max-w-[1440px] flex-1 p-4 outline-none sm:p-5 lg:p-6',
            protocolsWorkspace ? 'overflow-hidden' : 'overflow-y-auto overscroll-contain [scrollbar-gutter:stable]'
          )}
        >
          {props.children}
        </main>
      </div>
    </div>
  )
}

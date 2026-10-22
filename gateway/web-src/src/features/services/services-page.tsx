import { Bot, RadioTower, ServerCog, ShieldCheck, Sparkles } from 'lucide-react'
import { lazy, Suspense, useEffect, useId, useState } from 'react'

import { LoadingState } from '@/components/loading-state'
import { ErrorBoundary } from '@/components/error-boundary'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ProtocolsPage } from '@/features/protocols/protocols-page'
import { useConsoleData } from '@/lib/console-data'
import type { Channel, ProtocolView, Provider } from '@/lib/types'
import { cn } from '@/lib/utils'

const ChannelsPage = lazy(() => import('@/features/channels/channels-page').then(module => ({ default: module.ChannelsPage })))
const ServiceAllowances = lazy(() => import('@/features/services/service-allowances').then(module => ({ default: module.ServiceAllowances })))
const ModelPreferences = lazy(() => import('@/features/services/model-preferences').then(module => ({ default: module.ModelPreferences })))
const ControlPlanePage = lazy(() => import('@/features/control/control-plane-page').then(module => ({ default: module.ControlPlanePage })))

type WorkspaceTab = 'services' | 'models' | 'defaults' | 'allowances'
const tabs = [
  ['services', '服务号池', ServerCog],
  ['models', '模型渠道', RadioTower],
  ['defaults', '默认模型', Sparkles],
  ['allowances', '自主额度', ShieldCheck],
] as const

export function ServicesPage(props: {
  adminToken: string
  protocols: ProtocolView[]
  channels: Channel[]
  providers: Provider[]
  initialTab?: WorkspaceTab
  initialEditorOpen?: boolean
  onChanged: () => Promise<void>
}) {
  const [activeTab, setActiveTab] = useState<WorkspaceTab>(props.initialTab || 'services')
  const [editorOpen, setEditorOpen] = useState(props.initialEditorOpen || false)
  const editor = useConsoleData(props.adminToken, editorOpen, 'editor', 1)
  const tabsId = useId()
  useEffect(() => { setActiveTab(props.initialTab || 'services') }, [props.initialTab])
  useEffect(() => { setEditorOpen(props.initialEditorOpen || false) }, [props.initialEditorOpen])

  return (
    <div className='flex h-full min-h-0 flex-col gap-2 overflow-hidden'>
      <header className='flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b pb-2'>
        <div className='mr-2'>
          <h1 className='text-lg font-semibold tracking-tight'>服务与渠道</h1>
          <p className='text-[11px] text-muted-foreground'>通用服务、号池与模型渠道统一管理</p>
        </div>
        <div role='tablist' aria-label='服务与渠道分类' className='flex items-center rounded-md border bg-muted/25 p-0.5' onKeyDown={event => {
          const index = tabs.findIndex(([id]) => id === activeTab)
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : -1
          if (next < 0) return
          event.preventDefault()
          setActiveTab(tabs[next][0])
          document.getElementById(`${tabsId}-${tabs[next][0]}`)?.focus()
        }}>
          {tabs.map(([id, label, Icon]) => (
            <button key={id} id={`${tabsId}-${id}`} type='button' role='tab' aria-selected={activeTab === id} aria-controls={`${tabsId}-panel`} tabIndex={activeTab === id ? 0 : -1} className={cn('flex min-h-8 cursor-pointer items-center gap-1.5 rounded px-3 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring', activeTab === id ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')} onClick={() => setActiveTab(id)}>
              <Icon aria-hidden='true' className='size-3.5' />{label}
            </button>
          ))}
        </div>
      </header>

      <div id={`${tabsId}-panel`} role='tabpanel' aria-labelledby={`${tabsId}-${activeTab}`} tabIndex={0} className={cn('min-h-0 flex-1 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring', activeTab === 'models' ? 'overflow-y-auto overscroll-contain' : 'overflow-hidden')}>
        <ErrorBoundary key={activeTab}>
        <Suspense fallback={<LoadingState label='正在打开服务页面…' />}>
        {activeTab === 'allowances' ? <ServiceAllowances adminToken={props.adminToken} /> : activeTab === 'defaults' ? <ModelPreferences adminToken={props.adminToken} protocols={props.protocols} /> : activeTab === 'services' ? (
          <ProtocolsPage embedded protocols={props.protocols} adminToken={props.adminToken} onChanged={props.onChanged} onOpenEditor={() => setEditorOpen(true)} />
        ) : (
          <ChannelsPage embedded adminToken={props.adminToken} channels={props.channels} providers={props.providers} onChanged={props.onChanged} />
        )}
        </Suspense>
        </ErrorBoundary>
      </div>

      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent className='flex h-[92svh] w-[96vw] max-w-[84rem] flex-col overflow-hidden p-0'>
          <DialogHeader className='shrink-0 border-b px-5 py-4'>
            <div className='flex items-center gap-2'><Bot aria-hidden='true' className='size-4 text-primary' /><DialogTitle>协议发布编辑器</DialogTitle></div>
            <DialogDescription>草稿、复核、发布与回滚集中管理。</DialogDescription>
          </DialogHeader>
          <div className='min-h-0 flex-1 overflow-y-auto p-5'>
            {editor.isPending ? <LoadingState label='正在读取发布记录…' /> : editor.error ? (
              <div role='alert' className='space-y-3 text-sm'>
                <p>发布记录暂时不可用：{editor.error.message}</p>
                <Button variant='outline' onClick={() => void editor.refetch()}>重新读取</Button>
              </div>
            ) : editorOpen ? (
              <Suspense fallback={<LoadingState label='正在打开编辑器…' />}>
                <ControlPlanePage embedded adminToken={props.adminToken} drafts={editor.data.drafts} revisions={editor.data.revisions} protocols={props.protocols} onChanged={async () => { await editor.refetch(); await props.onChanged() }} />
              </Suspense>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

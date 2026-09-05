import { Bot, RadioTower, ServerCog } from 'lucide-react'
import { useState } from 'react'

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ChannelsPage } from '@/features/channels/channels-page'
import { ControlPlanePage } from '@/features/control/control-plane-page'
import { ProtocolsPage } from '@/features/protocols/protocols-page'
import type { Channel, ProtocolDraft, ProtocolRevision, ProtocolView, Provider } from '@/lib/types'
import { cn } from '@/lib/utils'

type WorkspaceTab = 'services' | 'models'

export function ServicesPage(props: {
  adminToken: string
  protocols: ProtocolView[]
  channels: Channel[]
  providers: Provider[]
  drafts: ProtocolDraft[]
  revisions: ProtocolRevision[]
  editorAvailable?: boolean
  initialTab?: WorkspaceTab
  onChanged: () => Promise<void>
}) {
  const [activeTab, setActiveTab] = useState<WorkspaceTab>(props.initialTab || 'services')
  const [editorOpen, setEditorOpen] = useState(false)

  return (
    <div className='flex h-full min-h-0 flex-col gap-2 overflow-hidden'>
      <header className='flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b pb-2'>
        <div className='mr-2'>
          <h1 className='text-lg font-semibold tracking-tight'>服务与渠道</h1>
          <p className='text-[11px] text-muted-foreground'>通用服务、号池与模型渠道统一管理</p>
        </div>
        <div role='tablist' aria-label='服务与渠道分类' className='flex items-center rounded-md border bg-muted/25 p-0.5'>
          {([
            ['services', '服务号池', ServerCog],
            ['models', '模型渠道', RadioTower],
          ] as const).map(([id, label, Icon]) => (
            <button key={id} type='button' role='tab' aria-selected={activeTab === id} className={cn('flex min-h-8 cursor-pointer items-center gap-1.5 rounded px-3 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring', activeTab === id ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')} onClick={() => setActiveTab(id)}>
              <Icon aria-hidden='true' className='size-3.5' />{label}
            </button>
          ))}
        </div>
      </header>

      <div className={cn('min-h-0 flex-1', activeTab === 'models' ? 'overflow-y-auto overscroll-contain' : 'overflow-hidden')}>
        {activeTab === 'services' ? (
          <ProtocolsPage embedded protocols={props.protocols} adminToken={props.adminToken} onChanged={props.onChanged} onOpenEditor={props.editorAvailable === false ? undefined : () => setEditorOpen(true)} />
        ) : (
          <ChannelsPage embedded adminToken={props.adminToken} channels={props.channels} providers={props.providers} onChanged={props.onChanged} />
        )}
      </div>

      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent className='flex h-[92svh] w-[96vw] max-w-[84rem] flex-col overflow-hidden p-0'>
          <DialogHeader className='shrink-0 border-b px-5 py-4'>
            <div className='flex items-center gap-2'><Bot aria-hidden='true' className='size-4 text-primary' /><DialogTitle>协议发布编辑器</DialogTitle></div>
            <DialogDescription>草稿、复核、发布与回滚集中管理。</DialogDescription>
          </DialogHeader>
          <div className='min-h-0 flex-1 overflow-y-auto p-5'>
            <ControlPlanePage embedded adminToken={props.adminToken} drafts={props.drafts} revisions={props.revisions} protocols={props.protocols} onChanged={props.onChanged} />
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

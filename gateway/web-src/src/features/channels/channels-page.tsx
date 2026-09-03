import {
  CircleCheck,
  Pencil,
  Plus,
  RadioTower,
  Server,
  TestTube2,
  Trash2,
} from 'lucide-react'
import { useMemo, useState, type FormEvent } from 'react'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ActivationToggle } from '@/components/activation-toggle'
import { SectionHeader } from '@/components/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { adminRequest } from '@/lib/api'
import type { Channel, Provider } from '@/lib/types'

type ChannelDraft = {
  name: string
  type: number
  baseUrl: string
  models: string
  key: string
  forwardHeaders: string
  setHeaders: string
  removeHeaders: string
  userAgent: 'inherit' | 'preserve' | 'omit' | 'localrouter' | 'configured'
  query: 'normalized' | 'preserve-raw'
}

function newDraft(providers: Provider[]): ChannelDraft {
  const provider = providers[0]
  return {
    name: '',
    type: provider?.id || 1,
    baseUrl: provider?.base_url || '',
    models: '',
    key: '',
    forwardHeaders: '',
    setHeaders: '',
    removeHeaders: '',
    userAgent: 'inherit',
    query: 'normalized',
  }
}

function editDraft(channel: Channel): ChannelDraft {
  const profile = channel.upstream_profile
  return {
    name: channel.name,
    type: channel.type,
    baseUrl: channel.base_url || '',
    models: channel.models || '',
    key: '',
    forwardHeaders: (profile?.forward_headers || []).join(', '),
    setHeaders: Object.entries(profile?.set_headers || {})
      .map(([name, value]) => `${name}: ${value}`)
      .join('\n'),
    removeHeaders: (profile?.remove_headers || []).join(', '),
    userAgent: profile?.user_agent || 'inherit',
    query: profile?.query || 'normalized',
  }
}

function headerList(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean)
}

function fixedHeaders(value: string): Record<string, string> {
  const result: Record<string, string> = {}
  for (const rawLine of value.split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    const separator = line.indexOf(':')
    if (separator <= 0) throw new Error(`固定请求头格式错误：${line}`)
    result[line.slice(0, separator).trim()] = line.slice(separator + 1).trim()
  }
  return result
}

export function ChannelsPage(props: {
  adminToken: string
  channels: Channel[]
  providers: Provider[]
  onChanged: () => Promise<void>
	 embedded?: boolean
}) {
  const [addOpen, setAddOpen] = useState(false)
  const [draft, setDraft] = useState<ChannelDraft>(() => newDraft(props.providers))
  const [editTarget, setEditTarget] = useState<Channel | null>(null)
  const [saving, setSaving] = useState(false)
  const [testingId, setTestingId] = useState<number | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null)
  const providerMap = useMemo(
    () => new Map(props.providers.map((provider) => [provider.id, provider.name])),
    [props.providers]
  )
  const selectedProvider = props.providers.find((provider) => provider.id === draft.type)

  function openAddDialog(open: boolean) {
    setAddOpen(open)
    if (open) {
      setEditTarget(null)
      setDraft(newDraft(props.providers))
    }
  }

  function openEditDialog(channel: Channel) {
    setEditTarget(channel)
    setDraft(editDraft(channel))
    setAddOpen(true)
  }

  function changeProvider(value: string) {
    const type = Number(value)
    const provider = props.providers.find((item) => item.id === type)
    setDraft((current) => ({ ...current, type, baseUrl: provider?.base_url || '' }))
  }

  async function submitChannel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const models = draft.models
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
      .join(',')
    if (!draft.name.trim() || (!editTarget && selectedProvider?.requires_key !== false && !draft.key.trim()) || !models) {
      toast.error(`请填写渠道名称、模型列表${editTarget || selectedProvider?.requires_key === false ? '' : '和上游密钥'}。`)
      return
    }

    setSaving(true)
    try {
      const channel = {
        id: editTarget?.id,
        type: draft.type,
        name: draft.name.trim(),
        key: draft.key.trim(),
        models,
        group: 'default',
        status: editTarget?.status || 1,
        weight: editTarget?.weight || 100,
        priority: editTarget?.priority || 0,
        auto_ban: editTarget?.auto_ban || 1,
        base_url: draft.baseUrl.trim() || null,
        upstream_profile: {
          forward_headers: headerList(draft.forwardHeaders),
          set_headers: fixedHeaders(draft.setHeaders),
          remove_headers: headerList(draft.removeHeaders),
          user_agent: draft.userAgent,
          query: draft.query,
        },
      }
      await adminRequest<unknown>('/local/api/channels', props.adminToken, editTarget ? {
        method: 'PUT',
        body: JSON.stringify(channel),
      } : {
        method: 'POST',
        body: JSON.stringify({ mode: 'single', channel }),
      })
      setDraft((current) => ({ ...current, key: '' }))
      setAddOpen(false)
      setEditTarget(null)
      await props.onChanged()
      toast.success(editTarget ? '供应商请求规则已更新' : '上游渠道已保存')
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '渠道保存失败')
    } finally {
      setSaving(false)
    }
  }

  async function testChannel(channel: Channel) {
    setTestingId(channel.id)
    try {
      await adminRequest<unknown>(
        `/local/api/channels/${channel.id}/test`,
        props.adminToken
      )
      await props.onChanged()
      toast.success(`${channel.name} 测试通过`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '渠道测试失败')
    } finally {
      setTestingId(null)
    }
  }

  async function deleteChannel() {
    if (!deleteTarget) return
    try {
      await adminRequest<unknown>(
        `/local/api/channels/${deleteTarget.id}`,
        props.adminToken,
        { method: 'DELETE' }
      )
      const deletedName = deleteTarget.name
      setDeleteTarget(null)
      await props.onChanged()
      toast.success(`已删除渠道 ${deletedName}`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '渠道删除失败')
    }
  }

  async function toggleChannel(channel: Channel) {
    setTestingId(channel.id)
    try {
      await adminRequest('/local/api/channels', props.adminToken, {
        method: 'PUT',
        body: JSON.stringify({
          ...channel,
          key: '',
          group: 'default',
          status: channel.status === 1 ? 2 : 1,
          weight: channel.weight || 100,
          priority: channel.priority || 0,
          auto_ban: channel.auto_ban || 1,
        }),
      })
      await props.onChanged()
      toast.success(`${channel.name} 已${channel.status === 1 ? '停用' : '启用'}`)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : '渠道状态保存失败')
    } finally {
      setTestingId(null)
    }
  }

  return (
    <div className={props.embedded ? 'space-y-4' : 'space-y-6'}>
      <SectionHeader
        title='模型渠道'
        actions={
          <Dialog open={addOpen} onOpenChange={openAddDialog}>
            <DialogTrigger asChild>
              <Button>
                <Plus aria-hidden='true' />
                添加渠道
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>{editTarget ? `编辑 ${editTarget.name}` : '添加上游渠道'}</DialogTitle>
                <DialogDescription>供应商配置 · 本机保存 · 密钥不回显</DialogDescription>
              </DialogHeader>
              <form id='channel-form' className='grid gap-4' onSubmit={submitChannel}>
                <div className='grid gap-2'>
                  <Label htmlFor='channel-name'>渠道名称</Label>
                  <Input
                    id='channel-name'
                    maxLength={64}
                    placeholder='例如本机兼容网关'
                    value={draft.name}
                    onChange={(event) =>
                      setDraft((current) => ({ ...current, name: event.target.value }))
                    }
                  />
                </div>
                <div className='grid gap-2'>
                  <Label htmlFor='channel-provider'>协议适配器</Label>
                  <select
                    id='channel-provider'
                    className='h-10 w-full cursor-pointer rounded-md border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/30'
                    value={draft.type}
                    onChange={(event) => changeProvider(event.target.value)}
                  >
                    {props.providers.map((provider) => (
                      <option key={provider.id} value={provider.id}>
                        {provider.name} · {provider.id}
                      </option>
                    ))}
                  </select>
                </div>
                <div className='grid gap-2'>
                  <Label htmlFor='channel-url'>上游基础地址</Label>
                  <Input
                    id='channel-url'
                    type='url'
                    placeholder='http://127.0.0.1:8317'
                    value={draft.baseUrl}
                    onChange={(event) =>
                      setDraft((current) => ({ ...current, baseUrl: event.target.value }))
                    }
                  />
                </div>
                <div className='grid gap-2'>
                  <Label htmlFor='channel-models'>模型列表</Label>
                  <Input
                    id='channel-models'
                    placeholder='gpt-5.4, claude-sonnet-4-6'
                    value={draft.models}
                    onChange={(event) =>
                      setDraft((current) => ({ ...current, models: event.target.value }))
                    }
                  />
                  <p className='text-xs text-muted-foreground'>使用英文逗号分隔。</p>
                </div>
                <div className='grid gap-2'>
				  <Label htmlFor='channel-key'>上游密钥{selectedProvider?.requires_key === false ? '（此 Profile 不需要）' : ''}</Label>
                  <Textarea
                    id='channel-key'
                    autoComplete='off'
                    spellCheck={false}
                    placeholder={editTarget ? '留空保留当前密钥' : '粘贴上游 API Key'}
                    value={draft.key}
                    onChange={(event) =>
                      setDraft((current) => ({ ...current, key: event.target.value }))
                    }
                  />
                </div>
                <details className='border-y py-3'>
                  <summary className='cursor-pointer text-sm font-medium'>供应商请求处理</summary>
                  <div className='mt-3 grid gap-3'>
                    <div className='grid gap-2'>
                      <Label htmlFor='channel-set-headers'>固定请求头</Label>
                      <Textarea id='channel-set-headers' spellCheck={false} placeholder={'X-Provider-Mode: silent\nX-Client-Version: 2'} value={draft.setHeaders} onChange={(event) => setDraft((current) => ({ ...current, setHeaders: event.target.value }))} />
                    </div>
                    <div className='grid gap-3 sm:grid-cols-2'>
                      <div className='grid gap-2'>
                        <Label htmlFor='channel-forward-headers'>补充转发</Label>
                        <Input id='channel-forward-headers' placeholder='X-Trace, X-Region' value={draft.forwardHeaders} onChange={(event) => setDraft((current) => ({ ...current, forwardHeaders: event.target.value }))} />
                      </div>
                      <div className='grid gap-2'>
                        <Label htmlFor='channel-remove-headers'>发送前删除</Label>
                        <Input id='channel-remove-headers' placeholder='X-Debug, X-Internal' value={draft.removeHeaders} onChange={(event) => setDraft((current) => ({ ...current, removeHeaders: event.target.value }))} />
                      </div>
                    </div>
                    <div className='grid gap-3 sm:grid-cols-2'>
                      <div className='grid gap-2'>
                        <Label htmlFor='channel-user-agent'>User-Agent</Label>
                        <select id='channel-user-agent' className='h-10 w-full cursor-pointer rounded-md border border-input bg-background px-3 text-sm' value={draft.userAgent} onChange={(event) => setDraft((current) => ({ ...current, userAgent: event.target.value as ChannelDraft['userAgent'] }))}>
                          <option value='inherit'>沿用现状</option>
                          <option value='preserve'>保留调用方</option>
                          <option value='omit'>不发送</option>
                          <option value='localrouter'>LocalRouter</option>
                          <option value='configured'>由固定请求头指定</option>
                        </select>
                      </div>
                      <div className='grid gap-2'>
                        <Label htmlFor='channel-query'>查询串</Label>
                        <select id='channel-query' className='h-10 w-full cursor-pointer rounded-md border border-input bg-background px-3 text-sm' value={draft.query} onChange={(event) => setDraft((current) => ({ ...current, query: event.target.value as ChannelDraft['query'] }))}>
                          <option value='normalized'>规范化</option>
                          <option value='preserve-raw'>保留原始编码与顺序</option>
                        </select>
                      </div>
                    </div>
                  </div>
                </details>
              </form>
              <DialogFooter>
                <Button variant='outline' type='button' onClick={() => setAddOpen(false)}>
                  取消
                </Button>
                <Button form='channel-form' type='submit' disabled={saving}>
                  {saving ? '正在保存…' : '保存渠道'}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        }
      />

      <section className='border-y'>
        {props.channels.length ? (
          <div>
            <div className='hidden grid-cols-[minmax(13rem,1.1fr)_minmax(12rem,1fr)_8rem_11rem] border-b bg-muted/35 px-5 py-3 text-xs font-medium text-muted-foreground md:grid'>
              <span>渠道</span>
              <span>模型</span>
              <span>状态</span>
              <span className='text-right'>操作</span>
            </div>
            <div className='divide-y'>
              {props.channels.map((channel) => (
                <div
                  key={channel.id}
                  className='grid gap-4 px-5 py-4 transition-colors hover:bg-muted/25 md:grid-cols-[minmax(13rem,1.1fr)_minmax(12rem,1fr)_8rem_11rem] md:items-center'
                >
                  <div className='flex min-w-0 items-start gap-3'>
                    <span className='flex size-8 shrink-0 items-center justify-center text-primary'>
                      <Server aria-hidden='true' className='size-4' />
                    </span>
                    <div className='min-w-0'>
                      <p className='truncate text-sm font-medium'>{channel.name}</p>
                      <p className='truncate text-xs text-muted-foreground'>
                        {providerMap.get(channel.type) || `Adapter ${channel.type}`} · #{channel.id}
                      </p>
                      <code className='mt-1 block truncate text-[11px] text-muted-foreground' title={channel.base_url || '默认地址'}>
                        {channel.base_url || '默认地址'}
                      </code>
                    </div>
                  </div>
                  <code className='line-clamp-2 break-all text-xs text-muted-foreground'>
                    {channel.models || '—'}
                  </code>
                  <Badge variant={channel.status === 1 ? 'success' : 'warning'} className='w-fit'>
                    {channel.status === 1 ? (
                      <CircleCheck aria-hidden='true' className='size-3.5' />
                    ) : null}
                    {channel.status === 1 ? '启用' : '停用'}
                  </Badge>
                  <div className='flex gap-2 md:justify-end'>
                    <ActivationToggle
                      checked={channel.status === 1}
                      busy={testingId === channel.id}
                      label={`${channel.status === 1 ? '停用' : '启用'}模型渠道 ${channel.name}`}
                      onChange={() => toggleChannel(channel)}
                    />
                    <Button variant='ghost' size='icon' aria-label={`编辑渠道 ${channel.name}`} onClick={() => openEditDialog(channel)}>
                      <Pencil aria-hidden='true' />
                    </Button>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={testingId === channel.id}
                      onClick={() => testChannel(channel)}
                    >
                      <TestTube2 aria-hidden='true' />
                      {testingId === channel.id ? '测试中' : '测试'}
                    </Button>
                    <Button
                      variant='ghost'
                      size='icon'
                      className='text-destructive hover:bg-destructive/10 hover:text-destructive'
                      aria-label={`删除渠道 ${channel.name}`}
                      onClick={() => setDeleteTarget(channel)}
                    >
                      <Trash2 aria-hidden='true' />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <EmptyState
            icon={RadioTower}
            title='尚未添加模型渠道'
            description='在这里添加和管理模型渠道。'
          />
        )}
      </section>

      <Dialog open={Boolean(deleteTarget)} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除渠道？</DialogTitle>
            <DialogDescription>
              删除“{deleteTarget?.name}”后，对应模型路由会立即停止。此操作不会删除外部服务中的账号。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant='outline' onClick={() => setDeleteTarget(null)}>取消</Button>
            <Button variant='destructive' onClick={deleteChannel}>
              <Trash2 aria-hidden='true' />
              确认删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

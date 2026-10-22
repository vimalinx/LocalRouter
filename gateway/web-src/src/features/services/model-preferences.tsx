import { useCallback, useEffect, useId, useState, type FormEvent } from 'react'
import { Plus, RefreshCw, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { adminRequest } from '@/lib/api'
import type { ModelPreferences as ModelPreferenceDocument, ModelServicePreference, ProtocolView } from '@/lib/types'

export function ModelPreferences({ adminToken, protocols }: { adminToken: string; protocols: ProtocolView[] }) {
  const [document, setDocument] = useState<ModelPreferenceDocument | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const packListId = useId()
  const reload = useCallback(async () => {
    setBusy(true)
    try {
      setDocument(await adminRequest<ModelPreferenceDocument>('/local/api/model-preferences', adminToken)); setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '模型偏好读取失败') }
    finally { setBusy(false) }
  }, [adminToken])
  useEffect(() => { void reload() }, [reload])

  const updateService = (index: number, change: Partial<ModelServicePreference>) => setDocument((current) => current && {
    ...current, services: current.services.map((service, position) => position === index ? { ...service, ...change } : service),
  })
  async function save(event: FormEvent) {
    event.preventDefault()
    if (!document) return
    setBusy(true)
    try {
      const services = document.services.filter((service) => service.pack.trim() !== '').map((service) => ({
        ...service, v1_aliases: service.v1 ? service.v1_aliases : undefined,
      }))
      setDocument(await adminRequest<ModelPreferenceDocument>('/local/api/model-preferences', adminToken, { method: 'PUT', body: JSON.stringify({ ...document, services }) }))
      toast.success('模型偏好已保存，立即生效')
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : '模型偏好保存失败') }
    finally { setBusy(false) }
  }

  return <section className='h-full space-y-3 overflow-y-auto overscroll-contain py-3' aria-labelledby='model-preferences-title'>
    <div className='flex flex-wrap items-center justify-between gap-2'>
      <div><h2 id='model-preferences-title' className='text-sm font-semibold'>默认模型与首选模型</h2>
        <p className='mt-1 max-w-3xl text-xs text-muted-foreground'>/v1 请求不带 model 时使用默认模型。每个服务可以设首选模型，lr 和程序在未指定模型时使用它。勾选“放进 /v1”的服务，其模型可直接用 /v1 + 模型名调用；没有 Channel 支持该模型时按列表顺序依次尝试，未发出或被限流（429）时自动换下一个。</p></div>
      <Button variant='ghost' size='sm' disabled={busy} onClick={reload}><RefreshCw aria-hidden='true' />刷新</Button>
    </div>
    {error ? <p role='alert' className='text-sm text-destructive'>{error}</p> : null}
    {document ? <form onSubmit={save} className='max-w-4xl space-y-3'>
      <div className='grid max-w-md gap-1.5'><Label htmlFor='default-model'>默认模型</Label>
        <Input id='default-model' value={document.default_model} placeholder='不设置' onChange={(event) => setDocument({ ...document, default_model: event.target.value })} /></div>
      <datalist id={packListId}>{protocols.map((protocol) => <option key={protocol.id} value={protocol.id}>{protocol.name}</option>)}</datalist>
      <div className='space-y-2'>
        {document.services.map((service, index) => <div key={index} className='grid items-end gap-2 border-t pt-2 sm:grid-cols-[12rem_1fr_auto_auto]'>
          <div className='grid gap-1.5'><Label htmlFor={`pack-${index}`}>服务</Label><Input id={`pack-${index}`} list={packListId} value={service.pack} onChange={(event) => updateService(index, { pack: event.target.value })} /></div>
          <div className='grid gap-1.5'><Label htmlFor={`preferred-${index}`}>首选模型</Label><Input id={`preferred-${index}`} value={service.preferred_model || ''} placeholder='不设置' onChange={(event) => updateService(index, { preferred_model: event.target.value })} /></div>
          <label className='flex min-h-9 items-center gap-2 text-sm'><Checkbox checked={Boolean(service.v1)} onChange={(event) => updateService(index, { v1: event.target.checked })} />放进 /v1</label>
          <Button type='button' variant='ghost' size='sm' aria-label={`删除 ${service.pack || '这一行'}`} onClick={() => setDocument({ ...document, services: document.services.filter((_, position) => position !== index) })}><Trash2 aria-hidden='true' /></Button>
          {service.v1 && service.v1_aliases && Object.keys(service.v1_aliases).length > 0 ? <p className='text-xs text-muted-foreground sm:col-span-4'>/v1 别名：{Object.entries(service.v1_aliases).map(([alias, model]) => `${alias} → ${model}`).join('，')}</p> : null}
        </div>)}
      </div>
      <div className='flex flex-wrap gap-2'>
        <Button type='button' variant='outline' size='sm' onClick={() => setDocument({ ...document, services: [...document.services, { pack: '' }] })}><Plus aria-hidden='true' />添加服务</Button>
        <Button type='submit' size='sm' disabled={busy}>保存</Button>
      </div>
    </form> : null}
  </section>
}

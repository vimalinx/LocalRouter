import { useEffect, useState } from 'react'
import { RefreshCcw, Search, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { adminRequest } from '@/lib/api'
import { supplierColor } from '@/lib/supplier-colors'
import { cn } from '@/lib/utils'
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

type Rule = {
  enabled: boolean; mode: 'quota' | 'approval' | 'deny'; unit: 'requests' | 'usd_micros'
  period: 'once' | 'day' | 'month'; limit: number; revision: number
  approval_operations: string[]; denied_operations: string[]; operation_limits?: Record<string, number>
}
type Receipt = { id: string; operation: string; token_id: number; amount: number }
type SubAllowance = { id: string; name: string; configured: boolean; limit: number; spent: number; reserved: number; remaining: number }
type Allowance = { operations?: SubAllowance[]; service: string; name: string; rule: Rule; spent: number; reserved: number; remaining: number; pending: Receipt[] }
const selectClass = 'h-10 w-full rounded-md border bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const splitOperations = (text: string) => text.split(/[\n,]/).map(value => value.trim()).filter(Boolean)
const units = (amount: number, rule: Rule) => rule.unit === 'usd_micros' ? `$${new Intl.NumberFormat('en-US', { maximumFractionDigits: 6 }).format(amount / 1e6)}` : `${new Intl.NumberFormat('zh-CN').format(amount)} 次`
const allowanceMode = (rule: Rule) => !rule.enabled ? '未启用' : rule.mode === 'quota' ? '额度内自主' : rule.mode === 'approval' ? '需批准' : '禁止使用'

export function ServiceAllowances({ adminToken }: { adminToken: string }) {
  const [items, setItems] = useState<Allowance[]>([])
  const [search, setSearch] = useState('')
  const [checkedServices, setCheckedServices] = useState<string[]>([])
  const [batchOpen, setBatchOpen] = useState(false)
  const [batchUnit, setBatchUnit] = useState<Rule['unit']>('usd_micros')
  const [batchAmount, setBatchAmount] = useState('3')
  const [subLimits, setSubLimits] = useState<Record<string, string>>({})
  const [checkedOperations, setCheckedOperations] = useState<string[]>([])
  const [subBatchAmount, setSubBatchAmount] = useState('3')
  const [operationSearch, setOperationSearch] = useState('')
  const [selected, setSelected] = useState('')
  const [draft, setDraft] = useState<Rule | null>(null)
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [tokenID, setTokenID] = useState('')
  const [operation, setOperation] = useState('')
  const [receiptID, setReceiptID] = useState('')
  const [actual, setActual] = useState('')
  const [reason, setReason] = useState('')
  const [limitText, setLimitText] = useState('0')
  const [approvalText, setApprovalText] = useState('')
  const [deniedText, setDeniedText] = useState('')
  const current = items.find(item => item.service === selected)
  const query = search.trim().toLowerCase()
  const visibleItems = items.filter(item => `${item.name} ${item.service}`.toLowerCase().includes(query))
  const enabledCount = items.filter(item => item.rule.enabled).length
  const operationQuery = operationSearch.trim().toLowerCase()
  const operations = (current?.operations || []).filter(op => `${op.id} ${op.name}`.toLowerCase().includes(operationQuery))
  const toggleSelection = (values: string[], id: string) => values.includes(id) ? values.filter(value => value !== id) : [...values, id]
  function setSubLimit(id: string, value?: string) {
    setSubLimits(previous => { const next = { ...previous }; if (value === undefined) delete next[id]; else next[id] = value; return next })
  }
  function fillSelectedSubLimits() {
    setSubLimits(previous => ({ ...previous, ...Object.fromEntries(checkedOperations.map(id => [id, subBatchAmount])) }))
    setMessage(`已填入 ${checkedOperations.length} 项子额度，保存配置后生效。`)
  }
  async function saveBatch() {
    const services = items.filter(item => checkedServices.includes(item.service)).map(item => ({ service: item.service, rule: { ...item.rule, unit: batchUnit, limit: Math.round(Number(batchAmount) * (batchUnit === 'usd_micros' ? 1e6 : 1)) } }))
    if (await mutate('/local/api/service-allowances/batch', 'POST', { services }, `已保存 ${services.length} 个服务的总额度，启用状态保持原样。`)) { setBatchOpen(false); setCheckedServices([]) }
  }
  function selectService(item: Allowance) {
    setSelected(item.service); setDraft({ ...item.rule }); setMessage(''); setError('')
    setReceiptID(''); setOperation(''); setTokenID(''); setActual(''); setReason(''); setCheckedOperations([]); setOperationSearch('')
  }
  async function refresh() {
    setBusy(true); setError(''); setMessage('')
    try { await load() } catch (err) { setError(err instanceof Error ? err.message : String(err)) }
    finally { setBusy(false) }
  }
  useEffect(() => { setApprovalText((draft?.approval_operations || []).join(', ')); setDeniedText((draft?.denied_operations || []).join(', ')) }, [selected, draft?.revision])
  useEffect(() => { setLimitText(String(draft ? draft.limit / (draft.unit === 'requests' ? 1 : 1e6) : 0)) }, [selected, draft?.revision, draft?.unit])
  useEffect(() => { setSubLimits(Object.fromEntries(Object.entries(draft?.operation_limits || {}).map(([id, value]) => [id, String(value / (draft?.unit === 'usd_micros' ? 1e6 : 1))]))) }, [selected, draft?.revision, draft?.unit])
  async function load(selection = selected) {
    const result = await adminRequest<Allowance[]>('/local/api/service-allowances', adminToken)
    setItems(result)
    const item = result.find(value => value.service === selection) || result[0]
    setSelected(item?.service || '')
    setDraft(item ? { ...item.rule } : null)
  }
  useEffect(() => {
    let live = true
    adminRequest<Allowance[]>('/local/api/service-allowances', adminToken).then(result => {
      if (!live) return
      setItems(result); setSelected(result[0]?.service || ''); setDraft(result[0]?.rule || null)
    }).catch(err => { if (live) setError(String(err)) }).finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [adminToken])
  async function mutate(path: string, method: string, body: unknown, success: string) {
    setBusy(true); setError(''); setMessage('')
    try {
      await adminRequest(path, adminToken, { method, body: JSON.stringify(body) })
      await load(); setMessage(success); return true
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); return false }
    finally { setBusy(false) }
  }
  const base = `/local/api/service-allowances/${encodeURIComponent(selected)}`
  if (loading) return <p role='status' className='p-4 text-sm'>正在读取服务额度…</p>
  return <div className='flex h-full min-h-0 flex-col gap-2 overflow-hidden'>
    <div className='flex min-h-0 flex-1 flex-col overflow-hidden border-y lg:grid lg:grid-cols-[22rem_minmax(0,1fr)]'>
      <aside className='flex max-h-64 min-h-0 shrink-0 flex-col border-b lg:max-h-none lg:shrink lg:border-b-0 lg:border-r' aria-label='自主额度服务列表'>
        <div className='flex shrink-0 items-center justify-between border-b px-3 py-2'>
          <label className='flex items-center gap-2 text-xs font-medium'><input type='checkbox' aria-label='选择全部筛选服务' disabled={busy || !visibleItems.length} checked={visibleItems.length > 0 && visibleItems.every(item => checkedServices.includes(item.service))} onChange={event => setCheckedServices(event.target.checked ? [...new Set([...checkedServices, ...visibleItems.map(item => item.service)])] : checkedServices.filter(id => !visibleItems.some(item => item.service === id)))} />服务 <span className='ml-1 text-muted-foreground'>{items.length}</span></label>
          <span className='text-[11px] text-muted-foreground'>{enabledCount} 已启用 · {items.length - enabledCount} 未启用</span>
        </div>
        <div className='relative m-2 shrink-0'>
          <Search aria-hidden='true' className='pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground' />
          <Input aria-label='搜索额度服务' placeholder='搜索服务名称' className='h-9 pl-8 text-xs' value={search} onChange={event => setSearch(event.target.value)} />
        </div>
        <div className='mx-2 mb-2 flex shrink-0 items-center justify-between gap-2'><span className='text-[11px] text-muted-foreground'>手动配置与启用</span><Button size='sm' variant='outline' disabled={busy || !checkedServices.length} onClick={() => { setBatchOpen(true); setError(''); setMessage('') }}>批量设置{checkedServices.length ? ` (${checkedServices.length})` : ''}</Button></div>
        <nav aria-label='自主额度服务' className='min-h-0 flex-1 space-y-1 overflow-y-auto overscroll-contain p-1.5 pt-0 [scrollbar-gutter:stable]'>
          {visibleItems.map(item => {
            const selectedItem = item.service === selected
            const color = supplierColor(item.service.startsWith('compatibility:') ? item.service : `protocol:${item.service}`)
            const quota = item.rule.enabled && item.rule.mode === 'quota'
            const usedPercent = item.rule.limit > 0 ? Math.max(0, Math.min(100, (item.spent + item.reserved) / item.rule.limit * 100)) : 0
            return <div key={item.service} className='flex items-start gap-0.5'><input className='ml-1.5 mt-4 shrink-0' type='checkbox' aria-label={`选择服务 ${item.name}`} disabled={busy} checked={checkedServices.includes(item.service)} onChange={() => setCheckedServices(previous => toggleSelection(previous, item.service))} /><button type='button' disabled={busy} aria-label={`${item.name} 自主额度`} aria-current={selectedItem ? 'page' : undefined}
              className={cn('flex min-h-14 min-w-0 flex-1 cursor-pointer items-start gap-2 rounded-md px-2.5 py-2.5 text-left outline-none transition-colors hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-wait', selectedItem && 'bg-muted text-foreground')}
              style={{ boxShadow: selectedItem ? `inset 2px 0 ${color}` : undefined }} onClick={() => selectService(item)}>
              <span className='mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md' style={{ color, backgroundColor: `color-mix(in oklch, ${color} 12%, transparent)` }}><ShieldCheck aria-hidden='true' className='size-4' /></span>
              <span className='min-w-0 flex-1'>
                <span className='flex items-start justify-between gap-1.5'><span className='min-w-0 break-words text-sm font-medium'>{item.name}</span><span className={cn('shrink-0 rounded px-1.5 py-0.5 text-[10px]', item.rule.enabled ? 'bg-primary/10 text-primary' : 'bg-background/60 text-muted-foreground')}>{allowanceMode(item.rule)}</span></span>
                {quota && <>
                  <span className='mt-3 block text-xs text-muted-foreground'>剩余额度</span>
                  <span className={cn('mt-1 block break-all text-3xl font-semibold leading-tight tracking-tight tabular-nums', item.remaining <= 0 && 'text-amber-700 dark:text-amber-300')}>{units(item.remaining, item.rule)}</span>
                  <span className='mt-1 block text-xs tabular-nums text-muted-foreground'>总额度 {units(item.rule.limit, item.rule)} · {item.rule.period === 'day' ? '每天' : item.rule.period === 'month' ? '每月' : '一次性'}</span>
                  <span aria-hidden='true' className='mt-2 block h-1.5 overflow-hidden rounded-full bg-background'><span className={cn('block h-full rounded-full', item.remaining <= 0 ? 'bg-amber-500' : 'bg-primary/60')} style={{ width: `${usedPercent}%` }} /></span>
                </>}
                {quota && Object.keys(item.rule.operation_limits || {}).length > 0 && <span className='mt-2 block text-xs text-muted-foreground'>{Object.keys(item.rule.operation_limits || {}).length} 项独立子额度</span>}
                {item.rule.enabled && item.pending.length > 0 && <span className='mt-2 block text-xs text-amber-700 dark:text-amber-300'>{item.pending.length} 笔费用待核对</span>}
              </span>
            </button></div>
          })}
          {!visibleItems.length && <p role='status' className='p-3 text-xs text-muted-foreground'>{items.length ? '没有匹配的服务。' : '暂无服务。'}</p>}
        </nav>
      </aside>
      <section aria-label={current ? `${current.name} 额度配置` : '额度配置'} className='min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain [scrollbar-gutter:stable]'>
        <header className='flex items-start justify-between gap-3 border-b px-4 py-3 sm:px-5'>
          <div className='min-w-0'><h2 className='break-words text-base font-semibold'>{current?.name || '服务自主使用额度'}</h2><p className='mt-1 text-xs text-muted-foreground'>{current ? `${allowanceMode(current.rule)} · ${current.service.startsWith('compatibility:') ? '模型渠道' : '通用服务'}` : '从左侧选择服务配置共享额度。'}</p></div>
          <Button variant='ghost' size='sm' disabled={busy} onClick={() => void refresh()} aria-label='刷新服务额度'><RefreshCcw aria-hidden='true' className={cn('size-3.5', busy && 'animate-spin')} /><span className='hidden sm:inline'>刷新</span></Button>
        </header>
        <div className='max-w-4xl space-y-5 p-4 sm:p-5'>
          {!current?.rule.enabled && <p className='text-sm text-muted-foreground'>自主额度未启用。可先保存配置，再手动开启。</p>}
          {error && !batchOpen && <p role='alert' className='text-sm text-destructive'>{error}</p>}
          {message && <p role='status' className='text-sm'>{message}</p>}
          {draft && current && <>
        {current.rule.enabled && current.rule.mode === 'quota' && <section aria-label='当前服务额度' className='border-b pb-5'>
          <p className='text-sm text-muted-foreground'>剩余额度</p>
          <p className={cn('mt-2 break-all text-5xl font-semibold tracking-tight tabular-nums', current.remaining <= 0 && 'text-amber-700 dark:text-amber-300')}>{units(current.remaining, current.rule)}</p>
          <dl className='mt-4 flex flex-wrap gap-x-8 gap-y-3 text-sm'>
            <div><dt className='text-muted-foreground'>总额度</dt><dd className='mt-1 text-lg font-medium tabular-nums'>{units(current.rule.limit, current.rule)}</dd></div>
            <div><dt className='text-muted-foreground'>已用</dt><dd className='mt-1 text-lg font-medium tabular-nums'>{units(current.spent, current.rule)}</dd></div>
            <div><dt className='text-muted-foreground'>待核对</dt><dd className='mt-1 text-lg font-medium tabular-nums'>{units(current.reserved, current.rule)}</dd></div>
          </dl>
        </section>}
        <form className='space-y-4' onSubmit={event => { event.preventDefault(); void mutate(base, 'PUT', { ...draft, limit: Math.round(Number(limitText) * (draft.unit === 'requests' ? 1 : 1e6)), approval_operations: splitOperations(approvalText), denied_operations: splitOperations(deniedText), operation_limits: Object.fromEntries(Object.entries(subLimits).map(([id, value]) => [id, Math.round(Number(value) * (draft.unit === 'usd_micros' ? 1e6 : 1))])) }, draft.enabled ? '规则已保存并启用。' : '规则已保存，额度功能关闭。') }}>
          <fieldset disabled={busy} className='space-y-4'>
            <label className='flex min-h-11 items-center gap-2 text-sm'><input type='checkbox' checked={draft.enabled} onChange={event => setDraft({ ...draft, enabled: event.target.checked })} />启用此服务的自主使用限制</label>
            <label className='block space-y-1 text-sm'><span>使用方式</span><select className={selectClass} value={draft.mode} onChange={event => setDraft({ ...draft, mode: event.target.value as Rule['mode'] })}><option value='quota'>额度内自主使用</option><option value='approval'>始终需要批准</option><option value='deny'>禁止使用</option></select></label>
            {draft.mode === 'quota' && <>
              <div className='grid gap-3 sm:grid-cols-3'>
                <label className='block space-y-1 text-sm'><span>计量单位</span><select disabled={Object.keys(subLimits).length > 0} className={selectClass} value={draft.unit} onChange={event => setDraft({ ...draft, unit: event.target.value as Rule['unit'], limit: event.target.value === 'usd_micros' ? 3000000 : 3 })}><option value='requests'>调用次数</option><option value='usd_micros'>美元</option></select></label>
                <label className='block space-y-1 text-sm'><span>总额度</span><Input type='number' required min='0' max={draft.unit === 'requests' ? 1e12 : 1e6} step={draft.unit === 'requests' ? 1 : 0.000001} value={limitText} onChange={event => setLimitText(event.target.value)} /></label>
                <label className='block space-y-1 text-sm'><span>额度周期</span><select className={selectClass} value={draft.period} onChange={event => setDraft({ ...draft, period: event.target.value as Rule['period'] })}><option value='once'>一次性</option><option value='day'>每天（UTC）</option><option value='month'>每月（UTC）</option></select></label>
              </div>
              <p className='text-sm text-muted-foreground'>多个 Agent 合计扣减。次数按获准调用计，不因失败退回。美元仅支持已确认的固定调用价格；变动或未知费用需单独批准。已有用量后不能更换单位和周期。</p>
            </>}
            {draft.mode === 'quota' && <section className='space-y-3 border-y py-4' aria-label='操作子额度'>
              <div className='flex flex-wrap items-center justify-between gap-2'><h3 className='text-sm font-semibold'>操作子额度 <span className='font-normal text-muted-foreground'>{(current.operations || []).length} 项操作</span></h3><span className='text-xs text-muted-foreground'>{Object.keys(subLimits).length} 项已设置</span></div>
              <p className='text-xs text-muted-foreground'>每项可单独配置，也可批量填写。子额度和服务总额度同时生效，共用计量单位与周期；未设置的操作只受总额度约束。金额初始值为 $3。</p>
              <Input aria-label='搜索子额度操作' placeholder='搜索操作名称或 ID' className='h-9 text-xs' value={operationSearch} onChange={event => setOperationSearch(event.target.value)} />
              <div className='flex flex-wrap items-center gap-2'>
                <label className='flex min-h-9 items-center gap-2 text-xs'><input type='checkbox' aria-label='选择全部筛选操作' checked={operations.length > 0 && operations.every(op => checkedOperations.includes(op.id))} disabled={!operations.length} onChange={event => setCheckedOperations(event.target.checked ? [...new Set([...checkedOperations, ...operations.map(op => op.id)])] : checkedOperations.filter(id => !operations.some(op => op.id === id)))} />全选操作</label>
                <Input aria-label='批量子额度' type='number' min='0' max={draft.unit === 'usd_micros' ? 1e6 : 1e12} step={draft.unit === 'usd_micros' ? 0.000001 : 1} className='h-9 w-24' value={subBatchAmount} onChange={event => setSubBatchAmount(event.target.value)} /><span className='text-xs text-muted-foreground'>{draft.unit === 'usd_micros' ? '美元' : '次'}</span>
                <Button size='sm' variant='outline' type='button' disabled={!checkedOperations.length || subBatchAmount === '' || !Number.isFinite(Number(subBatchAmount)) || Number(subBatchAmount) < 0} onClick={fillSelectedSubLimits}>填入已选 ({checkedOperations.length})</Button>
              </div>
              <div className='max-h-80 overflow-auto rounded-md border'>
                <Table><TableHeader><TableRow><TableHead className='w-8 px-2'><span className='sr-only'>选择</span></TableHead><TableHead>操作</TableHead><TableHead className='w-36'>子额度（{draft.unit === 'usd_micros' ? '美元' : '次'}）</TableHead></TableRow></TableHeader><TableBody>
                  {operations.map(op => {
                    const configured = Object.hasOwn(subLimits, op.id)
                    return <TableRow key={op.id}>
                      <TableCell className='px-2 py-2'><input type='checkbox' aria-label={`选择操作 ${op.id}`} checked={checkedOperations.includes(op.id)} onChange={() => setCheckedOperations(previous => toggleSelection(previous, op.id))} /></TableCell>
                      <TableCell className='max-w-0 py-2'><span className='block break-words text-xs font-medium'>{op.name || op.id}</span><code className='block break-all text-[10px] text-muted-foreground'>{op.id}</code>{current.rule.enabled && <span className='mt-2 block text-sm tabular-nums'>{op.configured ? `剩余 ${units(op.remaining, current.rule)}` : '共用服务总额度'}</span>}</TableCell>
                      <TableCell className='py-2'><label className='mb-1 flex min-h-6 items-center gap-1.5 text-[11px]'><input aria-label={`设置 ${op.id} 子额度`} type='checkbox' checked={configured} onChange={event => setSubLimit(op.id, event.target.checked ? '3' : undefined)} />单独设置</label><Input aria-label={`${op.id} 子额度`} className='h-8 text-xs' type='number' min='0' max={draft.unit === 'usd_micros' ? 1e6 : 1e12} step={draft.unit === 'usd_micros' ? 0.000001 : 1} required={configured} disabled={!configured} placeholder='未设置' value={subLimits[op.id] ?? ''} onChange={event => setSubLimit(op.id, event.target.value)} /></TableCell>
                    </TableRow>
                  })}
                  {!operations.length && <TableRow><TableCell colSpan={3} className='text-xs text-muted-foreground'>没有匹配的操作。</TableCell></TableRow>}
                </TableBody></Table>
              </div>
            </section>}
            <label className='block space-y-1 text-sm'><span>始终需要批准的操作</span><Input value={approvalText} onChange={event => setApprovalText(event.target.value)} /></label>
            <label className='block space-y-1 text-sm'><span>禁止的操作</span><Input value={deniedText} onChange={event => setDeniedText(event.target.value)} /></label>
            <p className='text-xs text-muted-foreground'>填写操作 ID，以逗号分隔；* 表示全部。兼容渠道填写请求方法和路径，例如 POST /v1/chat/completions。</p>
            <Button type='submit'>{busy ? '保存中…' : '保存配置'}</Button>
          </fieldset>
        </form>
        {current.rule.enabled && current.rule.mode !== 'deny' && <form className='space-y-3 border-t pt-4' onSubmit={event => { event.preventDefault(); void mutate(`${base}/grants`, 'POST', { token_id: Number(tokenID), operation }, '已批准该 Agent 调用此操作一次，一小时内有效。') }}>
          <h3 className='text-sm font-semibold'>批准一次调用</h3>
          <p className='text-sm text-muted-foreground'>授权所填 Agent 在一小时内调用指定操作一次，不增加共享额度，也不解除禁止规则。此批准覆盖该操作的参数和实际费用，请确认使用范围。</p>
          <div className='grid gap-3 sm:grid-cols-2'>
            <label className='block space-y-1 text-sm'><span>Agent Token ID</span><Input type='number' min='1' required value={tokenID} onChange={event => setTokenID(event.target.value)} /></label>
            <label className='block space-y-1 text-sm'><span>批准的操作 ID</span><Input required value={operation} onChange={event => setOperation(event.target.value)} /></label>
          </div><Button type='submit' disabled={busy}>批准一次</Button>
        </form>}
        {current.pending.length > 0 && <form className='space-y-3 border-t pt-4' onSubmit={event => { event.preventDefault(); void mutate(`${base}/receipts/${encodeURIComponent(receiptID)}/reconcile`, 'POST', { amount: Math.round(Number(actual) * 1e6), reason }, '核对结果已保存。') }}>
          <h3 className='text-sm font-semibold'>核对未结算费用</h3>
          <p className='text-sm text-muted-foreground'>超时或结果不明的占用会保留。请先核对供应商记录，再填写实际费用；确认未收费才填 0。</p>
          <label className='block space-y-1 text-sm'><span>待核对调用</span><select required className={selectClass} value={receiptID} onChange={event => setReceiptID(event.target.value)}><option value=''>选择调用</option>{current.pending.map(receipt => <option key={receipt.id} value={receipt.id}>{receipt.id} · {receipt.operation} · Agent {receipt.token_id} · {units(receipt.amount, current.rule)}</option>)}</select></label>
          <label className='block space-y-1 text-sm'><span>确认实际费用（美元）</span><Input required type='number' min='0' max='1000000' step='0.000001' value={actual} onChange={event => setActual(event.target.value)} /></label>
          <label className='block space-y-1 text-sm'><span>核对依据</span><Input required value={reason} onChange={event => setReason(event.target.value)} /></label>
          <Button type='submit' disabled={busy}>保存核对结果</Button>
        </form>}
          </>}
        </div>
      </section>
    </div>
    <Sheet open={batchOpen} onOpenChange={open => { if (!busy) setBatchOpen(open) }}><SheetContent><SheetHeader><SheetTitle>批量设置服务额度</SheetTitle><SheetDescription>已选 {checkedServices.length} 个服务。金额基础值为 $3，保存后保留各服务原有启用状态。</SheetDescription></SheetHeader><SheetBody>
      <form className='space-y-4' onSubmit={event => { event.preventDefault(); void saveBatch() }}><fieldset disabled={busy} className='space-y-4'>
        {error && <p role='alert' className='text-sm text-destructive'>{error}</p>}
        <ul className='max-h-44 overflow-y-auto text-xs text-muted-foreground'>{items.filter(item => checkedServices.includes(item.service)).map(item => <li key={item.service} className='py-1'>{item.name} · {allowanceMode(item.rule)}</li>)}</ul>
        <label className='block space-y-1 text-sm'><span>批量计量单位</span><select className={selectClass} value={batchUnit} onChange={event => setBatchUnit(event.target.value as Rule['unit'])}><option value='usd_micros'>美元</option><option value='requests'>调用次数</option></select></label>
        <label className='block space-y-1 text-sm'><span>每个服务的总额度</span><Input required type='number' min='0' max={batchUnit === 'usd_micros' ? 1e6 : 1e12} step={batchUnit === 'usd_micros' ? 0.000001 : 1} value={batchAmount} onChange={event => setBatchAmount(event.target.value)} /></label>
        <p className='text-xs text-muted-foreground'>仅修改总额度和计量单位，不自动开启功能，保留操作子额度、周期与批准规则。已有用量或子额度的服务不能切换计量单位；任何一项校验失败，整批都不会保存。</p>
        <Button type='submit' disabled={!checkedServices.length}>{busy ? '保存中…' : '保存批量额度'}</Button>
      </fieldset></form>
    </SheetBody></SheetContent></Sheet>
  </div>
}

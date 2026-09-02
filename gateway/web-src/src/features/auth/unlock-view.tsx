import { ArrowRight, BookOpen, Eye, EyeOff, KeyRound, ShieldCheck } from 'lucide-react'
import { useRef, useState, type FormEvent } from 'react'

import { LocalRouterMark } from '@/components/localrouter-mark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { PublicStatus } from '@/lib/types'

export function UnlockView(props: {
  status?: PublicStatus
  statusError?: string
  onUnlock: (token: string) => Promise<void>
}) {
  const [token, setToken] = useState('')
  const [showToken, setShowToken] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const tokenInput = useRef<HTMLInputElement>(null)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const value = token.trim()
    if (!value) {
      setError('请输入本机管理密钥。')
      tokenInput.current?.focus()
      return
    }

    setSubmitting(true)
    setError('')
    try {
      await props.onUnlock(value)
      setToken('')
    } catch (cause) {
      setError(cause instanceof Error ? `解锁失败：${cause.message}` : '解锁失败。')
      tokenInput.current?.focus()
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className='grid h-svh overflow-y-auto overscroll-contain lg:grid-cols-[minmax(0,1.15fr)_minmax(26rem,0.85fr)]'>
      <section className='hidden min-h-svh flex-col justify-between border-r p-10 lg:flex xl:p-16'>
        <div className='flex items-center gap-3'>
          <LocalRouterMark className='size-9' />
          <p className='font-semibold tracking-tight'>LocalRouter</p>
        </div>

        <div className='max-w-2xl py-16'>
          <h2 className='max-w-xl text-4xl font-semibold leading-[1.08] tracking-[-0.035em] xl:text-6xl'>
            模型 · 协议 · 工作流
          </h2>
          <div className='mt-10 max-w-xl divide-y border-y'>
            {[
              ['模型 API', '/v1'],
              ['Protocol Packs', '/p'],
              ['异步工作流', '/w'],
            ].map(([label, path]) => (
              <div key={label} className='flex items-center justify-between gap-4 px-1 py-3'>
                <p className='text-sm font-medium'>{label}</p>
                <code className='text-xs text-primary'>{path}</code>
              </div>
            ))}
          </div>
        </div>

        <p className='text-xs leading-5 text-muted-foreground'>AGPL-3.0</p>
      </section>

      <section className='flex min-h-svh items-center justify-center p-4 sm:p-8'>
        <div className='w-full max-w-md'>
          <div className='mb-8 flex items-center gap-3 lg:hidden'>
            <LocalRouterMark className='size-9' />
            <div>
              <p className='font-semibold'>LocalRouter</p>
              <p className='text-xs text-muted-foreground'>本机 AI 网关</p>
            </div>
          </div>

          <div className='border-y py-6 sm:py-7'>
            <div className='pb-4'>
              <div className='flex items-center justify-between gap-4'>
                <h1 className='text-xl font-semibold tracking-tight'>解锁本机控制台</h1>
                <span className='flex items-center gap-1.5 text-xs text-muted-foreground'>
                  <ShieldCheck aria-hidden='true' className='size-4 text-emerald-600' />
                  当前标签页
                </span>
              </div>
            </div>
            <div>
              <form className='space-y-5' onSubmit={handleSubmit} noValidate>
                <div className='space-y-2'>
                  <Label htmlFor='admin-token'>本机管理密钥</Label>
                  <div className='relative'>
                    <KeyRound
                      aria-hidden='true'
                      className='pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground'
                    />
                    <Input
                      ref={tokenInput}
                      id='admin-token'
                      type={showToken ? 'text' : 'password'}
                      autoComplete='off'
                      spellCheck={false}
                      className='px-10'
                      value={token}
                      aria-invalid={Boolean(error)}
                      aria-describedby='admin-token-help unlock-error'
                      onChange={(event) => setToken(event.target.value)}
                    />
                    <button
                      type='button'
                      className='absolute right-0 top-0 flex size-10 cursor-pointer items-center justify-center rounded-lg text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring'
                      aria-label={showToken ? '隐藏管理密钥' : '显示管理密钥'}
                      aria-pressed={showToken}
                      onClick={() => setShowToken((current) => !current)}
                    >
                      {showToken ? <EyeOff aria-hidden='true' className='size-4' /> : <Eye aria-hidden='true' className='size-4' />}
                    </button>
                  </div>
                  <p id='admin-token-help' className='text-xs leading-5 text-muted-foreground'>
                    默认位于 <code>$XDG_DATA_HOME/localrouter/admin-token</code>；未设置 XDG 时使用{' '}
                    <code>~/.local/share/localrouter/admin-token</code>。
                  </p>
                  <p id='unlock-error' className='min-h-5 text-sm text-destructive' role='alert'>
                    {error || props.statusError || ''}
                  </p>
                </div>
                <Button className='w-full' type='submit' disabled={submitting}>
                  {submitting ? '正在验证…' : '进入控制台'}
                  {!submitting ? <ArrowRight aria-hidden='true' /> : null}
                </Button>
              </form>

              <Button asChild variant='link' className='mt-4 h-auto min-h-10 w-full'>
                <a href='/docs'>
                  <BookOpen aria-hidden='true' />
                  先查看 Agent 协议文档
                </a>
              </Button>
              <p className='text-center text-xs text-muted-foreground lg:hidden'>AGPL-3.0</p>
            </div>
          </div>
        </div>
      </section>
    </main>
  )
}

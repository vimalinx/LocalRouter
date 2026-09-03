import { Eye, EyeOff, KeyRound } from 'lucide-react'
import { useState, type FormEvent } from 'react'

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

export function AdminTokenDialog(props: {
  enabled: boolean
  onChange: (token: string) => Promise<void>
  onSetEnabled: (enabled: boolean, token?: string) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [token, setToken] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [showToken, setShowToken] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function disable() {
    setSubmitting(true)
    setError('')
    try {
      await props.onSetEnabled(false)
      setOpen(false)
      reset()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '密码保护关闭失败。')
    } finally {
      setSubmitting(false)
    }
  }

  function reset() {
    setToken('')
    setConfirmation('')
    setShowToken(false)
    setError('')
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (token.length < 16) {
      setError('登录密钥至少需要 16 个字符。')
      return
    }
    if (token.length > 512) {
      setError('登录密钥不能超过 512 个字符。')
      return
    }
    if (token.trim() !== token || /[^\x20-\x7e]/.test(token)) {
      setError('登录密钥只能使用可打印 ASCII 字符，且不能以空白开头或结尾。')
      return
    }
    if (token !== confirmation) {
      setError('两次输入的登录密钥不一致。')
      return
    }

    setSubmitting(true)
    setError('')
    try {
      if (props.enabled) await props.onChange(token)
      else await props.onSetEnabled(true, token)
      setOpen(false)
      reset()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '登录密钥更新失败。')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (!nextOpen && !submitting) reset()
      }}
    >
      <DialogTrigger asChild>
        <Button variant='outline' size='sm'>
          <KeyRound aria-hidden='true' />
          {props.enabled ? '管理密码' : '开启密码'}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{props.enabled ? '管理控制台密码' : '开启控制台密码保护'}</DialogTitle>
          <DialogDescription>
            {props.enabled
              ? '可更换自定义密码，或恢复为默认的本机免密模式。新密码只写入受保护文件。'
              : '默认免密仅适用于 loopback 本机访问。开启后请使用自定义密码进入控制台。'}
          </DialogDescription>
        </DialogHeader>
        <form className='space-y-4' onSubmit={submit} noValidate>
          <div className='space-y-2'>
            <Label htmlFor='new-admin-token'>{props.enabled ? '新密码' : '自定义密码'}</Label>
            <div className='relative'>
              <Input
                id='new-admin-token'
                type={showToken ? 'text' : 'password'}
                autoComplete='new-password'
                minLength={16}
                maxLength={512}
                value={token}
                aria-invalid={Boolean(error)}
                aria-describedby='admin-token-rules admin-token-change-error'
                className='pr-10'
                onChange={(event) => setToken(event.target.value)}
              />
              <button
                type='button'
                className='absolute right-0 top-0 flex size-10 cursor-pointer items-center justify-center rounded-lg text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring'
                aria-label={showToken ? '隐藏新登录密钥' : '显示新登录密钥'}
                aria-pressed={showToken}
                onClick={() => setShowToken((current) => !current)}
              >
                {showToken ? <EyeOff aria-hidden='true' className='size-4' /> : <Eye aria-hidden='true' className='size-4' />}
              </button>
            </div>
            <p id='admin-token-rules' className='text-xs leading-5 text-muted-foreground'>
              16–512 个可打印 ASCII 字符；允许中间空格，不允许首尾空白。
            </p>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='confirm-admin-token'>再次输入</Label>
            <Input
              id='confirm-admin-token'
              type={showToken ? 'text' : 'password'}
              autoComplete='new-password'
              minLength={16}
              maxLength={512}
              value={confirmation}
              aria-invalid={Boolean(error)}
              aria-describedby='admin-token-change-error'
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </div>
          <p id='admin-token-change-error' className='min-h-5 text-sm text-destructive' role='alert'>
            {error}
          </p>
          <DialogFooter>
            {props.enabled ? (
              <Button type='button' variant='outline' disabled={submitting} onClick={disable}>
                关闭密码保护
              </Button>
            ) : null}
            <Button type='button' variant='outline' disabled={submitting} onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button type='submit' disabled={submitting}>
              {submitting ? '正在保存…' : props.enabled ? '更新密码' : '开启并使用'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

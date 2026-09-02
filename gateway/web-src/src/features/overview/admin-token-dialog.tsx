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
  onChange: (token: string) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [token, setToken] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [showToken, setShowToken] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

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
      await props.onChange(token)
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
          更改登录密钥
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>更改控制台登录密钥</DialogTitle>
          <DialogDescription>
            新密钥会立即写入受保护文件并生效。当前标签页会自动切换，其他已解锁标签页将失效。
          </DialogDescription>
        </DialogHeader>
        <form className='space-y-4' onSubmit={submit} noValidate>
          <div className='space-y-2'>
            <Label htmlFor='new-admin-token'>新登录密钥</Label>
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
            <Button type='button' variant='outline' disabled={submitting} onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button type='submit' disabled={submitting}>
              {submitting ? '正在更新…' : '更新并继续使用'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

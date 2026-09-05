import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, RotateCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Toaster, toast } from 'sonner'

import { AppShell, type SectionId } from '@/components/app-shell'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { UnlockView } from '@/features/auth/unlock-view'
import { LogsPage } from '@/features/logs/logs-page'
import { JobsPage } from '@/features/jobs/jobs-page'
import { OverviewPage } from '@/features/overview/overview-page'
import { ServicesPage } from '@/features/services/services-page'
import { TokensPage } from '@/features/tokens/tokens-page'
import { SetupPage } from '@/features/setup/setup-page'
import { adminRequest, publicRequest } from '@/lib/api'
import type { PublicStatus, Summary, UpdateStatus } from '@/lib/types'
import { useConsoleData } from '@/lib/console-data'

const validSections = new Set<SectionId>([
  'overview',
  'protocols',
  'jobs',
  'tokens',
  'logs',
  'setup',
])

function currentSection(): SectionId {
  const raw = window.location.hash.slice(1)
  const section = (raw === 'channels' || raw === 'control' ? 'protocols' : raw) as SectionId
  return validSections.has(section) ? section : 'overview'
}

export default function App() {
  const queryClient = useQueryClient()
  const [adminToken, setAdminToken] = useState<string | null>(null)
  const [activeSection, setActiveSection] = useState<SectionId>(currentSection)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [logPage, setLogPage] = useState(1)
  const [dark, setDark] = useState(() => {
    const saved = window.localStorage.getItem('localrouter-theme')
    if (saved) return saved === 'dark'
    return window.matchMedia('(prefers-color-scheme: dark)').matches
  })

  const publicStatus = useQuery({
    queryKey: ['public-status'],
    queryFn: () => publicRequest<PublicStatus>('/local/status'),
    retry: 1,
  })

  const adminAuthEnabled = publicStatus.data?.admin_auth_enabled ?? true
  const consoleAccessReady = Boolean(publicStatus.data) && (!adminAuthEnabled || adminToken !== null)
  const requestAdminToken = adminToken || ''

  const consoleData = useConsoleData(requestAdminToken, consoleAccessReady, activeSection, logPage)

  useEffect(() => {
    function onHashChange() {
      setActiveSection(currentSection())
      setMobileOpen(false)
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    window.localStorage.setItem('localrouter-theme', dark ? 'dark' : 'light')
  }, [dark])

  async function unlock(token: string) {
    await adminRequest<Summary>('/local/api/summary', token)
    setAdminToken(token)
  }

  function lock() {
    setAdminToken(null)
    setMobileOpen(false)
    queryClient.removeQueries({ queryKey: ['console-data'] })
    toast.success('控制台已锁定，管理密钥已从页面内存清除')
  }

  function navigate(section: SectionId) {
    setActiveSection(section)
    setMobileOpen(false)
    window.requestAnimationFrame(() => document.getElementById('main-content')?.focus())
  }

  async function refresh() {
    const result = await consoleData.refetch()
    if (result.error) {
      toast.error(result.error.message)
      return
    }
    toast.success('运行数据已刷新')
  }

  async function checkUpdate() {
    try {
      const update = await adminRequest<UpdateStatus>('/local/api/update/check', requestAdminToken, { method: 'POST' })
      queryClient.setQueryData<Summary>(['console-data', 'summary'], current => current ? { ...current, update } : current)
      if (update.status === 'available') {
        toast.info(`发现新版本 ${update.latest_version}`)
      } else if (update.status === 'error') {
        toast.error('版本检查失败，网关运行不受影响')
      } else {
        toast.success('已经是当前更新通道的最新版本')
      }
    } catch {
      toast.error('无法发起版本检查，网关运行不受影响')
    }
  }

  async function changeAdminToken(token: string) {
    await adminRequest<{ changed: boolean }>('/local/api/admin-token', requestAdminToken, {
      method: 'PUT',
      body: JSON.stringify({ token }),
    })
    setAdminToken(token)
    toast.success('控制台登录密钥已更新并立即生效')
  }

  async function changeAdminAuth(enabled: boolean, token?: string) {
    await adminRequest<{ enabled: boolean; changed: boolean }>('/local/api/admin-auth', requestAdminToken, {
      method: 'PUT',
      body: JSON.stringify({ enabled, ...(token ? { token } : {}) }),
    })
    setAdminToken(enabled ? token || adminToken : '')
    await publicStatus.refetch()
    queryClient.setQueryData<Summary>(['console-data', 'summary'], current => current ? { ...current, admin_auth_enabled: enabled } : current)
    toast.success(enabled ? '控制台密码保护已开启' : '控制台密码保护已关闭')
  }

  if (publicStatus.isPending) {
    return (
      <main className='grid h-svh place-items-center p-6'>
        <LoadingState label='正在读取本机控制台设置…' />
      </main>
    )
  }

  if (adminAuthEnabled && adminToken === null) {
    return (
      <>
        <UnlockView
          status={publicStatus.data}
          statusError={publicStatus.error?.message}
          onUnlock={unlock}
        />
        <Toaster theme={dark ? 'dark' : 'light'} position='top-right' richColors />
      </>
    )
  }

  let content
  if (consoleData.isPending) {
    content = (
      <section className='border-y'>
        <LoadingState label='正在装载控制台运行态…' />
      </section>
    )
  } else if (consoleData.error || !consoleData.data) {
    content = (
      <section className='flex min-h-64 flex-col items-center justify-center border-y p-8 text-center'>
          <span className='mb-3 flex size-10 items-center justify-center text-destructive'>
            <AlertTriangle aria-hidden='true' className='size-5' />
          </span>
          <h1 className='font-semibold'>无法读取控制台数据</h1>
          <p className='mt-1 max-w-lg text-sm text-muted-foreground'>
            {consoleData.error?.message || '本机网关返回了无法识别的响应。'}
          </p>
          <Button className='mt-5' variant='outline' onClick={refresh}>
            <RotateCw aria-hidden='true' />
            重新读取
          </Button>
      </section>
    )
  } else {
    const data = consoleData.data
    switch (activeSection) {
      case 'setup':
        content = <SetupPage adminToken={requestAdminToken} tokens={data.tokens} />
        break
      case 'protocols':
        content = (
          <ServicesPage
            adminToken={requestAdminToken}
            protocols={data.protocols}
            channels={data.channels}
            providers={data.providers}
            editorAvailable={!consoleData.warnings.some(warning => warning.key === 'drafts' || warning.key === 'revisions')}
            drafts={data.drafts}
            revisions={data.revisions}
            initialTab={window.location.hash === '#channels' ? 'models' : 'services'}
            onChanged={async () => {
              await consoleData.refetch()
            }}
          />
        )
        break
      case 'jobs':
        content = <JobsPage jobs={data.jobs} events={data.events} adminToken={requestAdminToken} onChanged={async () => { await consoleData.refetch() }} />
        break
      case 'tokens':
        content = (
          <TokensPage
            adminToken={requestAdminToken}
            tokens={data.tokens}
            usage={data.agentUsage}
            policies={data.policies}
            maintenanceAccess={data.maintenanceAccess!}
            apiTokenFile={data.summary?.api_token_file || publicStatus.data?.api_token_file || ''}
            onChanged={async () => {
              const result = await consoleData.refetch()
              return result.data?.tokens || []
            }}
          />
        )
        break
      case 'logs':
        content = <LogsPage logs={data.logs} page={logPage} total={data.logTotal} onPageChange={setLogPage} />
        break
      default:
        content = (
          <OverviewPage
            summary={data.summary!}
            analytics={data.analytics!}
            protocols={data.protocols}
            agentUsage={data.agentUsage}
            onChangeAdminToken={changeAdminToken}
            onChangeAdminAuth={changeAdminAuth}
            onCheckUpdate={checkUpdate}
          />
        )
    }
  }

  return (
    <>
      <a
        href='#main-content'
        className='fixed left-3 top-3 z-[100] -translate-y-20 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground outline-none transition-transform focus:translate-y-0'
      >
        跳到主要内容
      </a>
      <AppShell
        activeSection={activeSection}
        mobileOpen={mobileOpen}
        dark={dark}
        refreshing={consoleData.isFetching}
        listener={consoleData.data?.summary?.listen || publicStatus.data?.listen || ''}
        onNavigate={navigate}
        onMobileOpenChange={setMobileOpen}
        onThemeToggle={() => setDark((current) => !current)}
        onRefresh={refresh}
        onLock={adminAuthEnabled ? lock : undefined}
      >
        {consoleData.warnings.length > 0 && !consoleData.error ? <div role='status' className='mb-4 rounded border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-100'>{consoleData.warnings.map(warning => <p key={warning.key}>{warning.message}</p>)}</div> : null}
        {content}
      </AppShell>
      <Toaster theme={dark ? 'dark' : 'light'} position='top-right' richColors />
    </>
  )
}

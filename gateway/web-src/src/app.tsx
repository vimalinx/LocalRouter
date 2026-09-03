import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, RotateCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Toaster, toast } from 'sonner'

import { AppShell, type SectionId } from '@/components/app-shell'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { ChannelsPage } from '@/features/channels/channels-page'
import { ControlPlanePage } from '@/features/control/control-plane-page'
import { UnlockView } from '@/features/auth/unlock-view'
import { LogsPage } from '@/features/logs/logs-page'
import { JobsPage } from '@/features/jobs/jobs-page'
import { OverviewPage } from '@/features/overview/overview-page'
import { ProtocolsPage } from '@/features/protocols/protocols-page'
import { TokensPage } from '@/features/tokens/tokens-page'
import { adminRequest, normalizeItems, publicRequest } from '@/lib/api'
import type {
	Analytics,
  Channel,
  LocalToken,
  Paginated,
  ProtocolView,
  Provider,
  PublicStatus,
  RequestLog,
  Summary,
	TokenPolicy,
	MaintenanceAccess,
	ProtocolDraft,
	ProtocolRevision,
	WorkflowJob,
	ProtocolEvent,
} from '@/lib/types'

const validSections = new Set<SectionId>([
  'overview',
  'protocols',
  'control',
  'jobs',
  'channels',
  'tokens',
  'logs',
])

type ConsoleData = {
  summary: Summary
  analytics: Analytics
  protocols: ProtocolView[]
  providers: Provider[]
  channels: Channel[]
  tokens: LocalToken[]
  logs: RequestLog[]
  policies: TokenPolicy[]
  maintenanceAccess: MaintenanceAccess
  drafts: ProtocolDraft[]
  revisions: ProtocolRevision[]
  jobs: WorkflowJob[]
  events: ProtocolEvent[]
}

function currentSection(): SectionId {
  const section = window.location.hash.slice(1) as SectionId
  return validSections.has(section) ? section : 'overview'
}

async function loadConsole(adminToken: string): Promise<ConsoleData> {
  const [summary, analytics, protocols, providers, channels, tokens, logs, policies, maintenanceAccess, drafts, revisions, jobs, events] = await Promise.all([
    adminRequest<Summary>('/local/api/summary', adminToken),
    adminRequest<Analytics>('/local/api/analytics', adminToken),
    adminRequest<ProtocolView[]>('/local/api/protocols', adminToken),
    adminRequest<Provider[]>('/local/api/providers', adminToken),
    adminRequest<Paginated<Channel>>('/local/api/channels?page=1&page_size=100', adminToken),
    adminRequest<Paginated<LocalToken>>('/local/api/tokens?page=1&page_size=100', adminToken),
    adminRequest<Paginated<RequestLog>>('/local/api/logs?page=1&page_size=50', adminToken),
    adminRequest<TokenPolicy[]>('/local/api/token-policies', adminToken),
    adminRequest<MaintenanceAccess>('/local/api/maintenance-access', adminToken),
    adminRequest<ProtocolDraft[]>('/local/api/protocol-drafts', adminToken),
    adminRequest<ProtocolRevision[]>('/local/api/protocols/history', adminToken),
    adminRequest<WorkflowJob[]>('/local/api/workflows/jobs', adminToken),
    adminRequest<ProtocolEvent[]>('/local/api/protocol-events?limit=100', adminToken),
  ])
  return {
    summary,
    analytics,
    protocols,
    providers,
    channels: normalizeItems(channels),
    tokens: normalizeItems(tokens),
    logs: normalizeItems(logs),
    policies,
    maintenanceAccess,
    drafts,
    revisions,
    jobs,
    events,
  }
}

export default function App() {
  const queryClient = useQueryClient()
  const [adminToken, setAdminToken] = useState<string | null>(null)
  const [activeSection, setActiveSection] = useState<SectionId>(currentSection)
  const [mobileOpen, setMobileOpen] = useState(false)
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

  const consoleData = useQuery({
    queryKey: ['console-data'],
    queryFn: () => loadConsole(requestAdminToken),
    enabled: consoleAccessReady,
    staleTime: 10_000,
    retry: false,
  })

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
    queryClient.setQueryData<ConsoleData>(['console-data'], (current) => current ? {
      ...current,
      summary: { ...current.summary, admin_auth_enabled: enabled },
    } : current)
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
      case 'protocols':
        content = <ProtocolsPage protocols={data.protocols} adminToken={requestAdminToken} onChanged={async () => { await consoleData.refetch() }} />
        break
      case 'channels':
        content = (
          <ChannelsPage
            adminToken={requestAdminToken}
            channels={data.channels}
            providers={data.providers}
            onChanged={async () => {
              await consoleData.refetch()
            }}
          />
        )
        break
      case 'control':
        content = <ControlPlanePage adminToken={requestAdminToken} drafts={data.drafts} revisions={data.revisions} protocols={data.protocols} onChanged={async () => { await consoleData.refetch() }} />
        break
      case 'jobs':
        content = <JobsPage jobs={data.jobs} events={data.events} />
        break
      case 'tokens':
        content = (
          <TokensPage
            adminToken={requestAdminToken}
            tokens={data.tokens}
            policies={data.policies}
            maintenanceAccess={data.maintenanceAccess}
            apiTokenFile={data.summary.api_token_file}
            onChanged={async () => {
              const result = await consoleData.refetch()
              return result.data?.tokens || []
            }}
          />
        )
        break
      case 'logs':
        content = <LogsPage logs={data.logs} />
        break
      default:
        content = (
          <OverviewPage
            summary={data.summary}
            analytics={data.analytics}
            protocols={data.protocols}
            onChangeAdminToken={changeAdminToken}
            onChangeAdminAuth={changeAdminAuth}
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
        listener={consoleData.data?.summary.listen || publicStatus.data?.listen || ''}
        onNavigate={navigate}
        onMobileOpenChange={setMobileOpen}
        onThemeToggle={() => setDark((current) => !current)}
        onRefresh={refresh}
        onLock={adminAuthEnabled ? lock : undefined}
      >
        {content}
      </AppShell>
      <Toaster theme={dark ? 'dark' : 'light'} position='top-right' richColors />
    </>
  )
}

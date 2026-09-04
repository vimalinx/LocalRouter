export type PublicStatus = {
  success: boolean
  mode: string
  listen: string
  admin_auth_enabled: boolean
  admin_token_file: string
  api_token_file: string
  database_path: string
  protocol_dir: string
  channel_profiles?: string
  state_dir: string
  cache_dir: string
  path_layout: string
  engine: string
  oauth: string
}

export type Summary = {
  channels: number
  tokens: number
  listen: string
  admin_auth_enabled: boolean
  admin_token_file: string
  api_token_file: string
  database_path: string
  config_dir: string
  state_dir: string
  cache_dir: string
  protocol_dir: string
  channel_profiles?: string
  engine: string
  protocols: number
  protocols_ready: number
  billing: string
  oauth: string
}

export type AnalyticsTotals = {
  requests: number
  model_requests: number
  protocol_requests: number
  successful: number
  failed: number
  success_rate: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  model_cost_usd: number
  protocol_cost_usd: number
  protocol_priced_calls: number
  average_latency_ms: number
  protocol_p95_latency_ms: number
  active_services: number
  active_operations: number
}

export type AnalyticsBucket = {
  started_at: string
  model_requests: number
  protocol_requests: number
  failed: number
  tokens: number
  cost_usd: number
}

export type AnalyticsService = {
  id: string
  name: string
  kind: 'model-provider' | 'protocol'
  status: string
  requests: number
  successful: number
  failed: number
  success_rate: number
  average_latency_ms: number
  operations: number
  prompt_tokens?: number
  completion_tokens?: number
  cost_usd?: number
  priced_requests?: number
  cost_status: 'measured' | 'confirmed' | 'estimated' | 'partial' | 'unavailable'
  trend: Array<{ requests: number; tokens: number; cost_usd: number }>
}

export type AnalyticsModel = {
  name: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  cost_usd: number
}

export type Analytics = {
  generated_at: string
  window: string
  totals: AnalyticsTotals
  trend: AnalyticsBucket[]
  services: AnalyticsService[]
  models: AnalyticsModel[]
}

export type Provider = {
  id: number
  key: string
  name: string
  base_url: string
  requires_key: boolean
}

export type Channel = {
  id: number
  name: string
  type: number
  base_url?: string | null
  models?: string
  status: number
  response_time?: number
  test_time?: number
  weight?: number
  priority?: number
  auto_ban?: number
  upstream_profile?: {
    forward_headers?: string[]
    set_headers?: Record<string, string>
    remove_headers?: string[]
    user_agent?: 'inherit' | 'preserve' | 'omit' | 'localrouter' | 'configured'
    query?: 'normalized' | 'preserve-raw'
  }
}

export type LocalToken = {
  id: number
  name: string
  key?: string
  status: number
  agent_code: string
  agent_name: string
  workspace: string
  runtime: string
  unlimited_quota: boolean
  group: string
  accessed_time?: number
  created_time?: number
}

export type AgentUsage = {
  token_id: number
  token_name: string
  agent_code: string
  agent_name: string
  workspace: string
  runtime: string
  status: number
  registered: boolean
  system: boolean
  requests: number
  successful: number
  failed: number
  today_requests: number
  prompt_tokens: number
  completion_tokens: number
  cost_usd: number
  cost_status: 'measured' | 'estimated' | 'partial' | 'unavailable'
  last_used_at: number
  requests_per_minute: number
  daily_request_limit: number
  max_in_flight: number
}

export type TokenPolicy = {
  token_id: number
  surfaces?: string[]
  packs?: string[]
  operations?: string[]
  models?: string[]
  capabilities?: string[]
  requests_per_minute?: number
  daily_request_limit?: number
  max_in_flight?: number
  expires_at?: number
}

export type MaintenanceAccess = {
  agent_tokens_enabled: boolean
  default_auth: 'admin'
  admin_header: 'X-Local-Admin'
  agent_auth: 'bearer'
  agent_capability: 'localrouter.maintain'
  service_tokens: 'call-only'
  maintenance_tokens: 'maintenance-only'
}

export type ProtocolDraft = {
  id: string
  updated_at: string
  digest?: string
  base_digest?: string
  live_digest: string
  stale: boolean
  valid: boolean
  error?: string
  files: string[]
  protocols?: Array<{ id: string; name: string; routes: number; guides: number; workflows: number; pool_mode: string }>
  impact: {
    changed_files: number
    files: Array<{
      path: string
      change: 'added' | 'modified' | 'removed'
      area: 'definition' | 'guide' | 'module' | 'catalog' | 'schema' | 'other'
      protocol_id?: string
    }>
    protocols: Array<{
      id: string
      name?: string
      change: 'added' | 'modified' | 'removed'
      sections: string[]
      operations_added?: string[]
      operations_modified?: string[]
      operations_removed?: string[]
      pool_mode_before?: string
      pool_mode_after?: string
    }>
    pool_ids: string[]
  }
}

export type ProtocolRevision = { digest: string; created_at: string; live?: boolean }

export type WorkflowJob = {
  id: string
  protocol_id: string
  workflow_id: string
  state: string
  attempts: number
  max_attempts: number
  created_at: string
  updated_at: string
  owner_token_id?: number
  error?: string
}

export type ProtocolEvent = {
  id: string
  created_at: string
  token_id?: number
  surface: string
  protocol_id?: string
  operation_id?: string
  workflow_id?: string
  job_id?: string
  method: string
  path: string
  status: number
  latency_ms: number
  response_bytes: number
  attempts?: number
  credential_ref?: string
  target?: string
  outcome?: string
}

export type RequestLog = {
  id?: number
  created_at?: string
  created_time?: number | string
  model_name?: string
  model?: string
  content?: string
  type?: string | number
  username?: string
  channel_name?: string
  elapsed_time?: number
  [key: string]: unknown
}

export type ProtocolRoute = {
  enabled?: boolean
  operation_key?: string
  operation_id: string
  operation_id_role?: 'semantic-selector'
  operation_id_is_url?: false
  methods: string[]
  path: string
  call_url?: string
  call?: {
    methods: string[]
    default_method: string
    url: string
    authenticated: boolean
    authorization: string
    cli?: string
  }
  request_example_role?: 'illustrative-shape'
  dynamic_inputs?: Record<string, {
    source_operation_key?: string
    source_call_url?: string
    extract?: string
    rule: string
  }>
  summary: string
  streaming?: boolean
  target_selector?: {
    metadata_key: string
    mappings?: Record<string, string>
    default_target?: string
  }
}

export type ProtocolGuide = {
  id: string
  title: string
  summary: string
  status: string
  last_verified?: string
  markdown_url: string
}

export type ProtocolPricingEntry = {
  id: string
  scope: 'operation' | 'model' | 'platform'
  label?: string
  amount?: number
  currency?: string
  unit?: string
  free_tier?: string
  status: 'confirmed' | 'estimated' | 'unknown' | 'unpublished'
  source_url: string
  source_type: string
  checked_at: string
  note?: string
}

export type ProtocolPricing = {
  entries: ProtocolPricingEntry[]
}

export type ProtocolQuotaReferenceValue = {
  status: 'confirmed' | 'estimated' | 'remaining-only' | 'partial' | 'stale' | 'ambiguous'
  currency?: string
  pricing_id?: string
  total?: number
  remaining?: number
  used?: number
}

export type ProtocolView = {
  id: string
  name: string
  description: string
  mount: string
  workflow_mount: string
  enabled: boolean
  ready: boolean
  status: string
  status_label: string
  routes: ProtocolRoute[]
  guides?: ProtocolGuide[] | null
  pricing?: ProtocolPricing
  docs: {
    html: string
    manifest: string
    markdown: string
    examples: string
  }
  pool?: {
    mode: string
    strategy: string
    source?: string
    total?: number
    eligible?: number
    in_flight?: number
    affinities?: number
  }
  pool_runtime?: {
    ownership: 'local' | 'upstream' | 'static'
    status: 'ready' | 'degraded' | 'delegated' | 'static' | 'unavailable'
    total: number
    ready: number
    cooling: number
    disabled: number
    expired: number
    busy: number
    balance_low: number
    unroutable?: number
    balance_tracked: boolean
    balance_remaining?: number
    balance_empty: number
    quota: {
      status: 'untracked' | 'unknown' | 'remaining-only' | 'confirmed' | 'estimated' | 'partial' | 'stale' | 'mixed-unit'
      tracked_accounts: number
      confirmed_accounts: number
      unknown_accounts: number
      stale_accounts: number
      total?: number
      remaining?: number
      used?: number
      used_percent?: number
      unit?: string
      reference_value?: ProtocolQuotaReferenceValue
    }
    in_flight: number
    accounts: Array<{
      ref: string
      label: string
      status: 'ready' | 'cooldown' | 'disabled' | 'expired' | 'busy' | 'balance-low' | 'unroutable'
      status_label: string
      balance?: number
      quota: {
        tracked: boolean
        status: 'unknown' | 'confirmed' | 'estimated' | 'stale'
        total?: number
        remaining?: number
        used?: number
        used_percent?: number
        unit?: string
        checked_at?: string
        stale: boolean
        reference_value?: ProtocolQuotaReferenceValue
      }
      in_flight: number
      consecutive_failures?: number
      cooldown_until?: string
      last_used?: string
      targets?: string[]
    }>
  }
  workflows?: Array<{ id: string; name: string }> | null
}

export type Paginated<T> = {
  page: number
  page_size: number
  total: number
  items: T[]
}

export type ApiEnvelope<T> = {
  success: boolean
  data: T
  message?: string
}

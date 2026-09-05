export type BundleMember = { pack: string; operations: string[]; workflows?: string[] }
export type ServiceBundle = { id: string; name: string; description?: string; guide?: string; revision: string; includes?: string[]; members: BundleMember[] }
export type ServiceTemplate = { id: string; version: string; name: string; summary: string; maintenance_guide: string; checks: string[]; example: Record<string, unknown>; digest: string }
export type ServiceProposal = {
  id: string; owner_token_id: number; agent_code: string; kind: 'connection' | 'bundle' | 'template'; reason: string
  created_at: string; updated_at: string; state: string; digest: string; verification: string
  bundle?: ServiceBundle; template?: ServiceTemplate
  connection?: { template_id: string; template_version: string; source_url?: string; guide?: string; definition: {
    id: string; name: string; description: string; base_url: string; auth: { type: string; header?: string }
    routes: { operation_id: string; methods: string[]; path: string; summary: string }[]
    workflows?: { id: string; name: string }[]
  } }
  grant_token_id?: number; grant_expires_at?: number; maintainer_token_id?: number; maintenance_mode?: string
  draft_id?: string; pack_digest?: string; applied_digest?: string; error?: string; decided_by?: string
  impact?: { changed_files: number; files: { path: string; change: string; area: string }[]; protocols: { id: string; sections: string[] }[]; pool_ids: string[] }
}
export type WorkspaceData = {
  templates: ServiceTemplate[]; bundles: ServiceBundle[]
  proposals: { items: ServiceProposal[]; total: number; page: number; page_size: number }
  grants: Record<string, { revision: string; granted_at: string; expires_at?: number }[]>
  delegations: Record<string, { pack: string; mode: string; expires_at?: number }[]>
}
export type ServiceTrace = {
  span_id: string; trace_id: string; parent_span_id?: string; task_id?: string; token_id: number; kind: string
  surface: string; pack?: string; operation?: string; contract_digest?: string; grant_revisions?: string[]; model?: string; job_id?: string
  resource_ref?: string; resource_state?: string; method: string; started_at: string; finished_at?: string
  latency_ms: number; http_status: number; outcome: string; upstream_called: boolean; attempt?: number
  units?: { unit: string; quantity: number; source: string; mode: string }[]
  cost?: { amount_usd: number; status: string; source: string }
}
export type TraceSummary = { requests: number; attempts: number; unknown_costs: number; unknown_outcomes: number; cost_usd_by_status: Record<string, number>; units: { token_id: number; pack: string; unit: string; quantity: number; source: string; mode: string }[] }
export type TracePage = { summary?: TraceSummary; items: ServiceTrace[]; total: number; page: number; page_size: number }

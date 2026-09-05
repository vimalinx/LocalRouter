# Changelog

All notable LocalRouter changes are documented here. The project follows
[Semantic Versioning](https://semver.org/) once a stable `1.0.0` contract is
published.

## Unreleased

## 0.2.0 - 2026-09-06

- Published the first non-prerelease 0.x milestone from the validated alpha.7
  code baseline. This release changes version metadata and release notes;
  the gateway, CLI, schemas, and configuration formats remain unchanged.
- Includes Agent-prepared service onboarding with exact approval, scoped
  capability bundles, delegated maintenance, and task-attributed call traces.
- Includes guided Agent discovery, independent identity checks, explicit model
  selection, path/query parameter support, and durable call-result handling.
- Includes isolated operator/LAN surfaces, hardened Docker deployment, and
  prebuilt Linux amd64/arm64 archives with the XDG installer and Agent Skill.
- Existing alpha.7 installations can upgrade with the archive installer and
  restart the service using their existing configuration and persistent state.
  Provider accounts and operator Pack definitions are not distributed.
- Provider verification remains operation-specific. Account readiness,
  incomplete model output, and unverified media downloads are not promoted
  to successful provider acceptance by this version change.

## 0.1.0-alpha.7 - 2026-09-05

- Added Agent-prepared service onboarding with exact-digest human approval,
  versioned service templates, and capability bundles pinned to approved
  operations. Compatible maintenance can be delegated separately from calls.
- Added the service workspace console for reviewing proposals, granting and
  revoking bundles, inspecting templates, and following request traces.
- Persisted task and owner attribution across HTTP, MCP, and workflow calls,
  including upstream attempts, usage provenance, resource snapshots, and
  explicitly unknown outcomes and costs. Internal calls preserve the original
  caller's policy, quota, and revocation checks.
- Added interrupted-approval reconciliation that preserves intervening grants
  and revocations, and kept preparation separate from installation and execution.
- Added `lr init` and `lr guide` for identity checks and a concise Agent entry
  flow. Bootstrap identity is distinguished from independent Agent registration;
  service and maintenance credentials remain separate.
- Made template discovery compact and on demand, propagated MCP errors through
  CLI exit codes, and fixed body, path, and query arguments in `lr call` and
  generated usage examples.
- Clarified service readiness, caller authorization, proposal digests, provider
  resource IDs, and workflow Job IDs in installed Agent guidance. Public legacy
  guides direct consumers to file-based Token locators.
- Extended isolated CLI, authorization, trace-accounting, console, and release
  acceptance. The release contains generic templates and no provider accounts
  or operator Pack configuration.

## 0.1.0-alpha.6 - 2026-09-05

- Reject foreign browser origins and non-loopback operator hosts; prohibit
  upstream redirects that could forward supplier credentials.
- Stop replaying potentially accepted POST requests after transport failures or
  upstream 5xx responses; report incomplete non-streaming responses as errors.
- Route multipart model requests without modifying file bytes, filter model
  catalogs by protocol profile and Token policy, and enforce model restrictions
  for large and chunked requests.
- Persist limited Token request counters across restarts in a separate private
  usage file. Merge streaming usage incrementally, including nested start and
  terminal events even when streams exceed the previous capture limit.
- Run workflow network operations outside shared state locks, interrupt active
  requests for explicit cancellation, preserve independent cleanup budgets, and
  mark interrupted durable executions as outcome_unknown without automatic replay.
- Load all channel and Agent pages, paginate request logs, isolate unavailable
  console sections, and add workflow result/error details and operator cancellation.
- Rewrote the project README around installation, service configuration, Agent
  access, permissions, and recovery behavior.
- Added a non-blocking release update checker that runs on startup and every
  six hours, uses anonymous conditional GitHub API requests, respects stable
  and prerelease channels, and only prompts without downloading or installing.
- Added update state and a manual refresh action to the loopback Run Overview;
  the LAN service surface remains unchanged.

## 0.1.0-alpha.5 - 2026-09-04

- Added an opt-in LAN service listener that exposes only authenticated consumer
  routes and sanitized discovery while keeping the console, local API, and
  maintenance MCP bound to loopback.
- Added a pinned, non-root Docker image and hardened Compose deployment with a
  read-only root filesystem, dropped capabilities, persistent private volumes,
  graceful shutdown, configurable health checks, and fresh/migration guidance.
- Hardened remote `lr` use with explicit LAN opt-in, service-only discovery
  verification before reading a Token, loopback-only maintenance, bounded model
  catalogue requests, and Pack-qualified exact model lookup.
- Generalized dynamic model discovery for catalogue-backed and request-schema
  enum inputs, including `model_cls`, media model catalogues, and compatibility
  binding to every declared dynamic input.
- Improved pooled readiness probes to try distinct credentials safely and
  promptly recheck after unhealthy credentials enter cooldown.
- Added request usage accounting, unified service management, retired Token
  locator migration, and stale-draft rejection introduced after alpha.4.
- Extended LAN, Docker, Protocol Pack, clean-source, release-archive, security,
  and multi-transport acceptance, including physical LAN verification on a
  second Linux host.

## 0.1.0-alpha.4 - 2026-09-03

- Made the loopback Web console and `/local/api/*` password-free by default,
  while keeping password protection available as an explicit operator choice.
- Added an in-console flow to enable protection with a custom password, rotate
  it without restart, or return to the default password-free mode.
- Persisted the console-auth switch in a private mode-600 XDG data file and
  published its state through `/local/status`, discovery, summaries, and
  `lr status` without exposing credential values.
- Kept `/manage/mcp` on its separate administrator or explicitly enabled
  maintenance-only Agent lane; an open human console never grants Agent
  mutation authority.
- Extended Go, Web, smoke, isolated-install, global Skill/AGENTS, browser, and
  release-artifact acceptance for both the protected and password-free flows.

## 0.1.0-alpha.3 - 2026-09-03

- Added one-shot `lr tree`, explicit compatibility/Protocol Pack discovery,
  provider-qualified exact model selection, and verified `/p/<pack>/v1`
  OpenAI runtime handoff.
- Made CLI discovery safer for Agents with default 20-result bounds, `--all`
  and `--exact`, strict nonzero preflight exits, and structured positive error
  guidance with ready alternatives.
- Let Agent commands consume either bare operation IDs or catalog-published
  Pack-qualified operation keys, made subcommand help non-executing, and
  documented one-call capture before offline parsing for paid verification.
- Installed the LocalRouter consumer contract into managed global AGENTS files
  for shared Agents, Codex, and OMP, while preserving unrelated instructions
  and removing only LocalRouter-owned blocks on uninstall.
- Added source-tree race testing, current-worktree isolation, secret and
  vulnerability scanning, deterministic Web rebuild checks, GoReleaser
  snapshot verification, dual-architecture archive inspection, and a real
  archive-only install acceptance.
- Included the complete Protocol Pack Skill and dependency/license materials
  in binary release archives; the previous alpha.2 archive did not contain the
  newly introduced global Skill source.

## 0.1.0-alpha.2 - 2026-09-02

- Removed every provider Pack, provider guide, real-provider acceptance script,
  and provider-specific sidecar from the public distribution.
- Fresh installations now publish zero provider operations until an operator
  applies a private Pack through the reviewed exact-digest lifecycle.
- Kept the generic Protocol Pack schemas, transport adapters, credential-pool
  engine, workflows, discovery, documentation, MCP tools, and fictional
  isolated acceptance fixtures.

## 0.1.0-alpha.1 - 2026-09-02

- Native loopback-only Go gateway with local SQLite channels and API tokens.
- OpenAI-compatible, Anthropic, Gemini, Protocol Pack, workflow, MCP and Agent
  documentation surfaces.
- Local credential pools, quota telemetry, health, weighted routing and
  controlled retries.
- XDG Base Directory layout, standalone embedded authoring schemas, user systemd
  service, installer, non-destructive migration and release automation.

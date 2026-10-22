# Changelog

All notable LocalRouter changes are documented here. The project follows
[Semantic Versioning](https://semver.org/) once a stable `1.0.0` contract is
published.

## 0.4.2 - 2026-10-23

- Agent maintenance is on by default. `lr manage-token [agent-code]` issues a
  distinct maintenance-only Token at once through the loopback identity
  handshake (no console step), saves it mode 0600, and `lr manage-*` then uses
  it automatically. The Token can maintain Packs through the reviewed draft
  lifecycle but cannot call services, and a service identity cannot widen
  itself into one. Turning the switch off on the console Tokens page rejects
  every maintenance Token at once and is remembered. Policy files written
  before this release did not record the switch when it was off, so they now
  load as on.
- `lr manage-list` and `lr manage-call` print the gateway's structured error
  instead of failing silently.
- A Pack operation that declares a model enum now claims those models even when
  the Pack also has a model catalogue: model search lists embedding models only
  under the embeddings operation and chat models only under chat, and `lr exec`
  rejects the mismatch before dispatch.
- `/v1` serves every model-declaring operation of a Pack the operator puts on
  `/v1`, not only chat: discovery `models.v1_pack_models[]` gains `path`, and
  `GET /v1/models` lists embedding models served on `/v1/embeddings`.
- Preflight no longer prints "per per-…" for units that already start with
  "per".

- `lr describe` and preflight include model-scoped prices for operations that
  take a model, so a Pack priced per model (for example per video second) no
  longer reports `pricing: []` and "operation price is unknown"; preflight names
  the price of the selected model.
- `lr tree` marks operations whose published availability is `blocked` or
  `deprecated`, so a ready Pack no longer hides an unusable operation.

## 0.4.1 - 2026-10-22

- Built with Go 1.26.10 (toolchain and Docker build image), resolving the
  reachable standard-library advisory GO-2026-6809 in `crypto/x509`.
- The `v0.4.0` tag stopped at its release vulnerability check and published no
  artifacts; 0.4.1 is the first release that contains every 0.4.0 change below.

## 0.4.0 - 2026-10-22 (tagged, not published)

- Updated the console build to Vite 7.3.7 and Vitest 4.1.12, resolving
  GHSA-587h-7wmj-h5pm, GHSA-698v-4r3x-3vcv, GHSA-9jrq-w75r-8gcw,
  GHSA-r825-w8mm-5pxw, GHSA-rq7h-c2jc-7f22, GHSA-qxg6-89mg-rqv8 and
  GHSA-xp26-3454-mjgp.
- Preflight now enforces `minLength`, `maxLength`, `pattern`, `minimum`,
  `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `minItems` and
  `maxItems` from explicit request schemas, so an out-of-range paid request is
  blocked before `lr exec` sends it.
- An upstream 429 settles its budget reservation at zero, so a `/v1` failover
  past a rate-limited Pack no longer consumes the trial. All Pack attempts of
  one `/v1` request share a trace ID in `/agent/traces`.
- `/v1` answers an unknown model name with 404 `model_not_found` and a next
  step, instead of 503 `channel_not_ready`.
- Published `call.cli` uses `lr exec`; `lr result <id> --show` prints the saved
  response; `lr budget request --wait` prints the request ID before waiting;
  `lr init` omits `registration_url` once the Token is ready; a rejected
  schema-enum model names the allowed values; trial-exhausted errors explain
  the reservation and how a program asks for budget.
- The live guide adds a runnable `/v1` call, states that `/v1` needs no
  preflight, when to use `lr exec`, `lr call` or `lr run`, how `lr resolve`
  selects candidates, that `ready` does not check provider balance, how
  reservations block a trial early, and how to act as a connected program.
  These gaps were found by Agents that completed real tasks using only the
  guide.
- `/v1` is the shortest path for programs: a Service Token and a model name.
  A request without `model` uses the operator's default model, and models of
  full Packs that the operator puts on `/v1` are answered there and listed by
  `GET /v1/models`. When no Channel serves the model, the request runs through
  the same `/p/<pack>` pipeline (auth, policy, budget, trace) for each listed
  Pack in order, moving on only when nothing was sent upstream or the provider
  answered 429. `X-LocalRouter-Pack` and `X-LocalRouter-Default-Model` report
  the choice. Contract schema 19.
- Model preferences live in `$XDG_CONFIG_HOME/localrouter/models.json` and the
  console tab 服务与渠道 → 默认模型: the default model, each service's
  `preferred_model`, which Packs answer on `/v1`, and `/v1` name aliases. Hand
  edits apply without a restart. Discovery publishes `models` and per-service
  `preferred_model`; `lr tree` shows them, `lr runtime-openai <pack>` defaults
  to the preferred model, and `lr connect` writes the default model into
  `localrouter.toml`.
- Model search runs on the gateway (`GET /agent/models`). `lr find model` only
  forwards the query, catalogues are read concurrently, and matches mark
  `preferred` and `default`. The output shape is unchanged.
- The installed global Agent contract and `docs/AGENT-USAGE.md` keep only the
  fixed boundaries and point to the live guide (`lr guide`), which is now the
  single usage contract.
- Agent identity review is gone. `lr identity request`, `lr identity prepare`
  and `lr identity access` take effect at once; a new identity starts on the
  trial budget, and money stays the only human decision. A policy of `{}` means
  every service. The console's identity request list, the auto-approve setting
  and `/local/api/identity-requests` are removed. The former strict-mode switch
  now only turns on per-service allowance checks and no longer requires an
  independent identity; `whoami` drops `strict_mode`, `identity_required` and
  `auto_enrollment`. Contract schema 18.
- `lr connect <program>` gives a program its own identity at once, saves the
  Token to `$XDG_DATA_HOME/localrouter/programs/<program>/service-token`
  (mode 0600) and writes `$XDG_CONFIG_HOME/<program>/localrouter.toml` with the
  loopback base URLs, Token path and agent code. Rerunning reuses a working
  Token and replaces a revoked one.
- `lr status`, `lr find model`, `lr find pool` and other commands no longer fail
  with `jq: Argument list too long` on large discovery documents or catalogues;
  large JSON reaches jq through a file descriptor.
- `lr find model` searches standard compatibility Packs such as
  `openai-compatible` through their live `GET .../models` listing, so
  `lr find model --exact openai-compatible:<model>` resolves instead of
  reporting `pack_not_found`.

- The gateway opens a second loopback listener on `127.0.0.1:29317`
  (`LOCAL_GATEWAY_ALIAS_PORT`, `LOCAL_GATEWAY_ALIAS_ENABLED`) serving the
  identical operator engine, so local programs can keep one LocalRouter-owned
  port as a stable alternative to the configured primary port. The port is
  unassigned by IANA and below the Linux ephemeral range. A bind failure is
  logged and reported as `loopback_alias.error` in `/local/status`, the
  console summary and discovery; the primary listener is unaffected.
- Approving one service no longer makes every other pending proposal stale.
  A proposal records the live bytes of its own Pack files; while they are
  unchanged, approval rebuilds the identical change on the current live Packs.
  Proposals report `freshness`, and only a real conflict on their own files
  needs repreparation.
- After apply, a connection makes one credentialed request to its readiness
  probe or model listing and records `verification: live-ok` or
  `live-failed:<status>`; business operations are never called by setup.
- The console approves several plain HTTPS connections in one reviewed batch,
  shows whether each service is reachable, and `/#setup/<id>` opens a proposal
  directly. `lr setup check <id>` and the console recheck an applied
  connection the same way. Desktop notifications for waiting proposals offer 批准 for those
  same connections and 查看 for everything else.
- Human hook events are delivered by one worker in order, so a hook never races
  itself or sees a decision before its request.

- Agents connect new services themselves: `lr setup prepare --credential-file`
  stores the credential at preparation (deleted on rejection or withdrawal), so
  the human never types it. In ordinary mode a new public HTTPS service with
  published pricing entries, granted only to the preparing budgeted Agent,
  applies at once; anything else waits for review with `auto_apply.reason`.
- The human can undo an automatic connection from the desktop notification or
  `/#setup`, and the preparing Agent can undo its own with `lr setup undo`.
  Undo removes exactly the Pack files the proposal added through a reviewed
  draft, withdraws the grant and deletes the Agent's credential. A console
  switch turns automatic connection off.
- An Agent repairs a service it connected automatically, while no other Token
  is granted it, without review; withdrawals and replacements now notify the
  human hook so mirrored Todos close.
- `LOCAL_GATEWAY_HUMAN_REQUEST_HOOK` receives budget and service events as JSON,
  for example to keep requests in a Todo list until decided.
- Contract schema 17.
- The installer now restarts a running service on upgrade, and
  `lr budget request --wait` succeeds only when the request is approved.

- Trial budgets replace identity review in ordinary mode. `lr identity request`
  and `lr identity prepare` now issue and bind immediately; every new identity
  starts on a one-time trial ($2 by default) drawn from a shared monthly trial
  pool ($5 by default). Agent-supplied money fields are ignored.
- Agents ask for more money with `lr budget request formal|trial`; the human
  approves once in the console (optionally lowering the amount) or from a
  desktop notification (`LOCAL_GATEWAY_DESKTOP_NOTIFICATIONS=true`).
- Contract schema 16 adds `auto_enrollment` and `budget` to `/agent/whoami`,
  the `agent_trial_exhausted` and `agent_trial_pool_exhausted` codes, and the
  `/agent/budget-requests` routes. Scope changes keep the approved budget.

- Complete new-service enrollment discovery and structured recovery errors; publish live enrollment and Pack authoring schemas, credential-free examples, and exact-digest proposal repreparation with preserved history and fresh approval.
- Contract schema 15 adds setup-only identities (`lr identity prepare`) that can
  prepare proposals but never call services, and reports `setup_only`,
  `service_access` and `maintenance_access` from `/agent/whoami`.

- Added setup-only enrollment, reviewed dependency closure, and exact workflow
  and compatibility selectors enforced by the actual service routes.
- Made connection grants review and activate the complete Token policy while
  retaining prior permissions. Stale policies cannot overwrite concurrent edits;
  interrupted activation blocks calls until reconciliation completes.
- Separated bounded discovery and existing-job recovery from business request
  counts, and aligned budget preflight failures with human recovery guidance.
- Persisted workflow creation intent before dispatch, propagated unknown outcomes
  through parallel/graph execution, and stopped CLI watches on unknown outcomes.
- Contract schema 14 makes workflow GET observational; use POST /advance to
  progress a job. Updated CLI workflows retain a private single-dispatch receipt.

- Added independent daily/monthly USD budgets per Agent, atomic pre-dispatch
  reservations, durable settlement, and operator reconciliation of unknown or
  over-reservation charges. Variable prices require an explicit reservation;
  this estimate is not a provider-enforced maximum charge.
- Retained revoked Agent identities, usage and costs in a durable billing ledger,
  importing retained history once and preserving it across diagnostic rotation.
- Added per-Agent billing details and server-side Agent/name/model/channel log
  filtering with pagination, including revoked identities.
- Included monetary budget checks in Agent preflight (introduced in schema 13).

## 0.3.0 - 2026-09-22

- Updated the Vitest development dependency to 4.1.11 to address
  GHSA-82fw-gwwq-j7x9 in the test mocker.
- Simplified the console to four everyday destinations, with advanced tools
  and the protocol editor loaded on demand. Service operations now precede
  account details, including when switching services.
- Added connection recovery, bounded and cancellable console reads, stale-data
  indicators, expired-session cache cleanup, and page-load error recovery.
  Interrupted mutations remain uncertain and are never automatically replayed.
- Added accessible mobile navigation and keyboard service tabs, retained
  legacy channel/editor links, and made empty service lists actionable.
- Included operator guides, Agent instructions, and README images in binary
  archives, with installation and archive-content regression checks.

- Added human-reviewed exact-scope identity enrollment with private resumable CLI
  delivery, automatic claim on daily calls, and owned scope-change requests.
  Approvals preserve counters, reject stale policy changes, and cannot revive
  revoked identities; enrollment is absent from the LAN listener.
- Added private raw execution receipts and trace-only reconciliation through
  `lr result`; structured failures report the blocking stage and next actor.
- Recover bounded pre-dispatch failures without spending a second allowance;
  confirmed unsent calls release reservations, while unknown results stay held.
- Preserve service scope when editing Agent limits, revoke Tokens before any
  policy cleanup, roll back failed policy persistence in memory, and reject
  service credentials on human console APIs.

- Exempted explicitly published read-only model discovery from generation
  budgets while preserving explicit deny/approval and Token/pool policies.
- Reject unpriceable automatic USD allowances when enabling a rule or the
  master switch; surface unsupported operations before a failed call.
- Added session-scoped identity locator binding and `lr exec` to perform
  identity, contract, exact model and preflight checks before one request.
- Preserve structured model catalogue failures and HTTP status; failed
  discovery exits nonzero and is explicitly marked incomplete.

- Added a default-off master switch for shared allowances, with collapsed
  configuration while off, preserved usage on re-enable, and migration that
  retains already-active policies.
- Added human-configured, opt-in shared service allowances with request-count
  and confirmed fixed-price USD budgets. All existing services remain disabled
  by default; Token and capability-bundle permissions still apply.
- Added per-operation approval/prohibition, expiring one-call approvals,
  persistent atomic reservations, and human reconciliation of unknown costs.
- Added a searchable per-service allowance list, manual operation sub-budgets,
  and atomic batch configuration. New budgets start at USD 3 per month and
  remain disabled until manually enabled; child and service limits both apply.
- Added the service allowance console and non-consuming Agent preflight checks.
  Pack calls, workflow/MCP projections, and compatibility channel dispatches
  enforce the configured limits; Agents cannot edit them through service or
  maintenance credentials.
- Variable or unknown costs require approval. Published v0.2.0 artifacts retain
  their original tag; these changes are included in v0.3.0.

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

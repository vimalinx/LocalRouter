# Changelog

All notable LocalRouter changes are documented here. The project follows
[Semantic Versioning](https://semver.org/) once a stable `1.0.0` contract is
published.

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

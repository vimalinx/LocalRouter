# Changelog

All notable LocalRouter changes are documented here. The project follows
[Semantic Versioning](https://semver.org/) once a stable `1.0.0` contract is
published.

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

# Changelog

All notable LocalRouter changes are documented here. The project follows
[Semantic Versioning](https://semver.org/) once a stable `1.0.0` contract is
published.

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

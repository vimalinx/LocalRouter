# Provenance and independence boundary

## Historical source

Early LocalRouter revisions extracted a local-only entry point from
[QuantumNous New API](https://github.com/QuantumNous/new-api) at commit
`918427d8ab41f6adaa4113d0496f1f8621855b70`. The current runtime was rewritten
after that source had been inspected, so this project does not describe the
work as a clean-room implementation and keeps the historical attribution.

## Current runtime

The current executable is built only from this repository's `gateway/*.go`
source. It has no Go module, replace directive, Git submodule, generated source,
or runtime import from New API. LocalRouter now owns:

- its SQLite schema compatibility layer and data access;
- channel and API Token administration;
- native OpenAI-compatible, Anthropic Messages, and Gemini pass-through routes;
- model selection across multiple channels, priority fallback, weighted
  rotation, bounded pre-stream failover, request logging, and local analytics;
- Protocol Pack, credential-pool, workflow, MCP, documentation, release, and
  rollback behavior.

Existing local databases keep compatible `users`, `tokens`, `channels`, and
`logs` table shapes so installations can upgrade without exporting secrets.
Compatibility does not create a source or build dependency.

## Architectural references

[router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) was
reviewed at commit `f0de1d008fe8881dcb7431cf97b147295874c2b2` for credential
selection and cooldown concepts. It is neither vendored nor linked. No Pack,
provider endpoint, provider-specific adapter, or integration for that project
ships in the public distribution.

## License material

LocalRouter remains distributed under AGPL-3.0; see `LICENSE`. `NOTICE` records
the historical derivative boundary, and `THIRD-PARTY-LICENSES.md` inventories
the dependencies currently linked into the Go and Web builds. `gateway/LICENSE`
is a compatibility link to the root license.

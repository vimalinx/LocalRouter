# Protocol Packs

Top-level `*.json` files in this directory are validated protocol contracts. Schema v1 and v2 remain compatible; new universal-protocol work uses v3. LocalRouter exposes operations at `/p/<id>`, optional async workflows at `/w/<id>/<workflow>`, and generates `/docs` from the same contract, so runtime behavior and documentation cannot silently diverge. Agent-authored details live separately in `<id>/guides/*.md` and are merged only after their metadata and operation references validate.

## Empty by design

The public distribution contains no top-level Pack JSON, real provider endpoint, provider guide, credential pool, or provider-specific sidecar. This directory ships only schemas, neutral authoring documentation, and an empty pool-catalog contract. Operator Packs live in the private XDG configuration directory and enter the runtime only through the reviewed exact-digest lifecycle. Fictional fixtures under `tests/fixtures/` are test inputs; they are not embedded, installed, or published by a fresh runtime.

The v2 contract is documented in `../../docs/PROTOCOL-PACK-V2.md`; the universal v3 contract is `../../docs/PROTOCOL-PACK-V3.md`. Their schemas are under `schema/`. Runtime validation remains authoritative and intentionally stricter than schema-only validation.

## Safety model

- A client cannot choose an arbitrary upstream URL; `base_url` exists only in an operator-owned template file.
- Every method and path must be explicitly allowlisted. `{name}` matches one path segment and a terminal `*` matches the remaining path.
- Client `Authorization`, `x-api-key`, hop-by-hop headers, cookies, and `Host` are never forwarded.
- Upstream authentication is injected from a mode-600 file below `$XDG_DATA_HOME/localrouter/protocol-secrets/` (or the explicit `LOCAL_GATEWAY_DATA_DIR`).
- Agent-managed release is validate → plan → exact-digest apply. If any top-level template or guide is invalid or the candidate changes after plan, the running registry stays unchanged.
- `/p/*` uses the same local API token as the model relay.
- Named targets are operator-owned; request data and expressions cannot choose an upstream. A v3 `target_selector` may map protected credential metadata only to an allowlisted target name, never to a URL.
- Named `upstream_profiles` own supplier-specific non-secret request headers, User-Agent behavior, and raw-vs-normalized query handling. Routes may select one default and override it by the already-resolved named target; client input never selects a profile.
- Side-effecting retries require an explicit provider idempotency contract; ambiguous writes return an unknown outcome.
- WASM modules are capability-free, bounded, digest-covered files under `<id>/modules/`.

## Agent maintenance contract

An Agent may propose or edit a Pack and its route descriptions, then follow the project skill at `../../.agents/skills/localrouter-protocol-pack/SKILL.md`. It must not write credentials, enable an unreviewed arbitrary wildcard, or bypass the hash-bound lifecycle. After review, use `../../tools/protocol-pack-lifecycle.sh plan` and apply the exact reviewed digest. The legacy reload endpoint is compatibility-only.

Port-only Agents and human operators share the same draft. `GET /local/api/protocol-drafts` reports content-based `impact.files`, per-Pack `impact.protocols` (including changed sections and operation IDs), and `impact.pool_ids` for runtime pools exposed to the change. A pool ID may be affected by a route, auth, upstream, workflow, or adapter change even when the pool configuration is unchanged; check whether the Pack impact contains the `pool` section before describing it as a pool configuration change. The flat workbench at `/#control` edits only the same allowlisted JSON/Markdown draft files and uses the same validate, plan, exact-digest apply, and rollback endpoints.

Templates are documentation sources, not secret stores. `/docs`, `/.well-known/localrouter.json`, `/docs/index.json`, `/docs/openapi.json`, and Pack endpoints intentionally omit `base_url`, authentication headers, secret-file paths, and source filenames.

## Agent-authored guides

Each guide is a Markdown file below `protocols/<id>/guides/`. It starts with strict YAML front matter:

```markdown
---
id: quickstart
title: Quickstart
summary: A short description shown in the Pack index.
status: draft
operations:
  - search
visibility: local
---
# Detailed usage

The Agent-maintained body begins here.
```

- The filename must be `<id>.md`.
- `status` is `draft`, `observed`, `verified`, or `deprecated`.
- A `verified` guide must declare `last_verified`.
- Every item in `operations` must match a route `operation_id`; a broken reference makes reload fail atomically.
- Raw HTML is not enabled. Guides must never include credentials, cookies, secret paths, or private upstream addresses.

Published representations:

- `/.well-known/localrouter.json` — stable Agent discovery document.
- `/docs` and `/docs/packs/<id>` — human HTML.
- `/docs/index.json` and `/docs/packs/<id>/manifest.json` — machine-readable inventory.
- `/docs/packs/<id>/guide.md` — generated reference plus all authored guides.
- `/docs/packs/<id>/guides/<guide>.md` — one authored guide body.
- `/docs/packs/<id>/examples.json` — generated operation examples.
- `/docs/openapi.json` — generated OpenAPI 3.1 document.
- `/docs/pools/index.json` and `/docs/pools/catalog.md` — sanitized operator pool inventory, ownership and adapter status.

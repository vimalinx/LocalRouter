---
name: localrouter-protocol-pack
description: Use when authoring, changing, releasing, diagnosing, or rolling back a LocalRouter Protocol Pack, custom transport or adapter, workflow, authentication, credential pool, quota telemetry, or Agent guide. Also use when an Agent must discover or maintain LocalRouter capabilities through port 8317. Do not use for an ordinary call to an unchanged documented Pack.
---

# Maintain LocalRouter Protocol Packs

Live discovery is authoritative. Readiness is not provider proof.

## Start from the live contract

1. Request configured discovery, defaulting to `http://127.0.0.1:8317/.well-known/localrouter.json`. Before sending a Token to non-loopback, require `scope=lan-service` and `maintenance.available=false`.
2. Run `lr init` and `lr guide`. Bootstrap identity is not an independent Agent; follow the returned registration steps. Agent maintenance requires an enabled, distinct maintenance-only Token. Never read the administrator credential or use its human fallback.
3. Classify the work before reading more detail:
   - **New service/template/bundle:** `lr setup templates` → `lr setup template <id> <version>` → `lr setup schema` → prepare → human exact approval → verify. Preparation grants no authority.
   - **Use only:** call the already documented operation and do not edit the Pack.
   - **Compatibility Pack:** standard APIs use Channel Profile + Channels on `/v1` or `/v1beta`.
   - **Pack change:** use authorized `maintenance.mcp`; LocalRouter owns formatting, validation, digests, installation, and rollback.
   - **Runtime change:** Go, WebUI, schema, or handlers require build, restart, and any Pack release.
4. Determine who owns registration, credential refresh, health, request-time selection, quota measurement, and upstream OAuth. Do not silently move ownership into LocalRouter.

`operation_id` is a semantic selector, never a path.

## Load only the relevant detail

| Current work | Read |
|---|---|
| Legacy v2 transforms, affinity, or basic workflows | [references/pack-v2.md](references/pack-v2.md) |
| v3 targets, expressions, advanced auth, readiness, selectors, leases, WebSocket/gRPC, adapters, or graph workflows | [references/pack-v3.md](references/pack-v3.md) |
| Choosing REST, multipart/file, SSE, WebSocket, gRPC, async, callback, or adapter patterns | [references/protocol-recipes.md](references/protocol-recipes.md) |
| Local, external, or external-readonly pools; registration-time quota; health and balance | [references/pool-quota.md](references/pool-quota.md) |
| HMAC, OAuth2, SigV4, secrets, targets, headers, SSRF, or untrusted payloads | [references/security.md](references/security.md) |
| Authoring without repository filesystem access | [references/port-authoring.md](references/port-authoring.md) |
| Writing or updating Agent-facing usage guidance | [references/agent-documentation.md](references/agent-documentation.md) |
| Operation changes, deprecation, v2-to-v3 migration, or pool schema evolution | [references/compatibility.md](references/compatibility.md) |
| Validation, impact review, release, binary restart, live verification, or rollback | [references/release-lifecycle.md](references/release-lifecycle.md) |
| A failed validation, 401/403/409/422/429/5xx, degraded pool, stale quota, or unknown outcome | [references/troubleshooting.md](references/troubleshooting.md) |
| Any completion claim | [references/acceptance.md](references/acceptance.md) |
| Delivering a newly published model to OMP or another Agent runtime | [references/runtime-handoff.md](references/runtime-handoff.md) |

Schemas live under `gateway/protocols/schema/`; see `docs/PROTOCOL-PACK-V3.md`.

## Non-negotiable boundaries

- Keep the operator gateway on loopback. An explicit LAN listener may expose only authenticated consumer routes and sanitized docs, never the console, `/local/status`, `/local/api`, or `/manage/mcp`. Request data never selects targets or adapter/module paths.
- Keep credentials, cookies, locators, private upstream addresses, and pool contents out of Pack source, guides, logs, tests, and `.ai` project-visible notes. Installed protected material stays below `$XDG_DATA_HOME/localrouter/` with mode `0600`; isolated tests may set `LOCAL_GATEWAY_DATA_DIR`.
- Keep registration, CAPTCHA, human OAuth consent, anti-bot challenges, payment, and account production outside the request path.
- Keep externally owned pools external. Use `pool.mode=local` only when LocalRouter owns request-time selection. An external maintainer may atomically update a private external-readonly source without transferring registration ownership.
- Default retry to `safe`. Never replay an unknown side-effecting outcome without idempotency or authoritative reconciliation.
- Prefer byte passthrough for multipart, files, SSE, WebSocket, gRPC, and unknown bodies. Apply JSON transforms only to observed JSON contracts.
- Give every route a stable `operation_id`. Define workflows only from observed IDs, status paths, transitions, terminal values, results, and cancellation behavior.
- Unknown capability, cost, balance, or account state stays unknown. Missing quota is not zero; balance is not provider task success.
- Service Tokens can prepare owned setup proposals but cannot approve authority or maintain Packs; pool concurrency, lease, cooldown, health, and quota still apply. Optional Agent maintenance requires a distinct maintenance-only `localrouter.maintain` Token. Never mix purposes.
- Pack is the common service view. Full Protocol Packs cover isolated routes, transforms, special auth, dedicated pools, adapters, or workflows; `/w` and `/mcp` are projections.

## Required lifecycle

1. Discover the live Pack and ownership boundary.
2. For advanced Pack authoring, confirm the enabled maintenance Token and scope. Open an isolated `/manage/mcp` draft; never request, print, copy, or edit credentials there.
3. Use the narrowest semantic tool for Pack core, operation, supplier profile, or guide. Lint content edits. Use merge patch only for advanced/removal fields outside those tools.
4. Read the draft's `impact.files`, `impact.protocols`, and `impact.pool_ids`. Inspect every changed section and affected pool before planning.
5. Run strict validation, focused mocks, relevant transport/workflow tests, and the Skill doctor.
6. Plan once, retain the reviewed digest, and apply that exact digest. Draft drift invalidates the plan.
7. Re-discover through port 8317 and verify each applicable real layer. Resolve dynamic models with `lr find model --exact <pack>:<model-id>`; a zero-result exact query is blocking even if fuzzy suggestions exist. Before OpenAI-compatible runtime configuration, run `lr runtime-openai <pack> <exact-model>` for its public mount.
8. Report pass/fail/not-covered plus the revision digest. Do not create `.ai` evidence while automation is paused.
9. Post-apply verification failure preserves the draft and attempts the previous revision. Inspect `error.stage`, `error.code`, and `error.rollback`; reconcile unknown upstream outcomes before retry.

Discovery doctor:

```bash
python .agents/skills/localrouter-protocol-pack/scripts/protocol_pack_doctor.py
```

Doctor checks discovery/docs only; provider execution needs separate evidence.

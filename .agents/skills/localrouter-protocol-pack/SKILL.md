---
name: localrouter-protocol-pack
description: Use when authoring, changing, releasing, diagnosing, or rolling back a LocalRouter Protocol Pack, custom transport or adapter, workflow, authentication, credential pool, quota telemetry, or Agent guide. Also use when an Agent must discover or maintain LocalRouter capabilities through port 8317. Do not use for an ordinary call to an unchanged documented Pack.
---

# Maintain LocalRouter Protocol Packs

Treat the loopback listener as consumer authority and a reviewed Pack draft as the authoring unit. A parseable file, healthy account, model list, or balance is not provider proof.

## Start from the live contract

1. Request `http://127.0.0.1:8317/.well-known/localrouter.json`.
2. Follow its Manifest, guide, OpenAPI, pool catalog, and maintenance links. Consumers need no repository access. An Agent may maintain only when discovery reports `maintenance.auth.agent_token.enabled=true` and an operator supplied a maintenance-only Token locator.
3. Classify the work before reading more detail:
   - **Use only:** call the already documented operation and do not edit the Pack.
   - **Pack change:** use the semantic tools at `maintenance.mcp` only through the authorized maintenance lane; LocalRouter owns draft paths, JSON/YAML formatting, validation, exact digests, installation, and local rollback.
   - **Runtime change:** Go, WebUI, schema, or handler behavior requires a binary/UI build and restart in addition to any Pack release.
4. Determine who owns registration, credential refresh, health, request-time selection, quota measurement, and upstream OAuth. Do not silently move ownership into LocalRouter.

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

Do not read every reference by default. The authoritative schemas remain `gateway/protocols/schema/protocol-pack-v2.schema.json` and `protocol-pack-v3.schema.json`; `docs/PROTOCOL-PACK-V3.md` is the complete runtime contract.

## Non-negotiable boundaries

- Bind the public gateway to loopback. Targets and adapter/module paths are operator-owned constants; request data never selects them.
- Keep credentials, cookies, locators, private upstream addresses, and pool contents out of Pack source, guides, logs, tests, and `.ai` project-visible notes. Installed protected material stays below `$XDG_DATA_HOME/localrouter/` with mode `0600`; isolated tests may set `LOCAL_GATEWAY_DATA_DIR`.
- Keep registration, CAPTCHA, human OAuth consent, anti-bot challenges, payment, and account production outside the request path.
- Use `pool.mode=external` when another gateway owns the complete pool. Use `pool.mode=local` only when LocalRouter owns request-time selection. An external maintainer may atomically update a private external-readonly source without transferring registration ownership.
- Default retry to `safe`. Never replay a side-effecting request after an unknown outcome unless the provider honors the same idempotency key or an authoritative reconciliation proves it safe.
- Prefer byte passthrough for multipart, files, SSE, WebSocket, gRPC, and unknown bodies. Apply JSON transforms only to observed JSON contracts.
- Give every route a stable `operation_id`. Define workflows only from observed IDs, status paths, transitions, terminal values, results, and cancellation behavior.
- Unknown capability, cost, balance, or account state stays unknown. Missing quota is not zero; balance is not provider task success.
- Consumer Tokens are normally long-lived and call-only. Pool concurrency, lease, cooldown, health, and quota still apply. Maintenance uses the administrator credential. Optional Agent maintenance defaults off; when enabled it requires a distinct maintenance-only `localrouter.maintain` Token. Never mix purposes.

## Required lifecycle

1. Discover the live Pack and ownership boundary.
2. Confirm discovery's maintenance lane. Operators use the administrator header; Agents require the enabled maintenance-only lane. Open an isolated `/manage/mcp` draft; never request or edit credentials there.
3. Author the smallest contract and guide change with the narrowest semantic tool: Pack core, one operation, one upstream supplier profile, or one guide. Run Pack lint after content edits. Use generic merge patch only for advanced/removal fields not owned by a narrower tool. Do not construct paths, front matter, or release payloads manually when a maintenance tool owns them.
4. Read the draft's `impact.files`, `impact.protocols`, and `impact.pool_ids`. Inspect every changed section and affected pool before planning.
5. Run strict validation, focused mocks, relevant transport/workflow tests, and the Skill doctor.
6. Plan once, retain the reviewed digest, and apply that exact digest. Draft drift invalidates the plan.
7. Re-discover through port 8317 and verify docs plus every applicable real layer.
8. Report the acceptance matrix as pass/fail/not-covered and retain the revision digest in the task result. Do not create `.ai` evidence while the project-wide automation pause is active.
9. A failed local post-apply verification automatically attempts the previous immutable revision and preserves the draft. Inspect the structured `error.stage`, `error.code`, and `error.rollback` before deciding whether to retry. Unknown upstream outcomes are never replayed automatically.

Run the read-only discovery doctor when the listener is available:

```bash
python .agents/skills/localrouter-protocol-pack/scripts/protocol_pack_doctor.py
```

Use `--pack <id>` to narrow the report. Passing the doctor proves discovery/document consistency only; provider calls, streaming terminals, workflows, cost, and rollback still require their own evidence.

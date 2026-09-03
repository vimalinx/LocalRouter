---
name: localrouter-protocol-pack
description: Use when authoring, changing, releasing, diagnosing, or rolling back a LocalRouter Protocol Pack, custom transport or adapter, workflow, authentication, credential pool, quota telemetry, or Agent guide. Also use when an Agent must discover or maintain LocalRouter capabilities through port 8317. Do not use for an ordinary call to an unchanged documented Pack.
---

# Maintain LocalRouter Protocol Packs

Treat the loopback listener as consumer authority and a reviewed Pack draft as the authoring unit. A parseable file, healthy account, model list, or balance is not provider proof.

## Start from the live contract

1. Request `http://127.0.0.1:8317/.well-known/localrouter.json`.
2. Follow its links. Before mutation choose an enabled maintenance Agent Token, explicit delegation for one `lr manage-*` change, or read-only discovery. Keep credentials private and use the port as authority.
3. Classify the work before reading more detail:
   - **Use only:** call the already documented operation and do not edit the Pack.
   - **Compatibility Pack:** standard OpenAI, Anthropic, or Gemini routing uses lightweight Channel Profile + Channels on `/v1` or `/v1beta`.
   - **Pack change:** use the semantic tools at `maintenance.mcp` only through the authorized maintenance lane; LocalRouter owns draft paths, JSON/YAML formatting, validation, exact digests, installation, and local rollback.
   - **Runtime change:** Go, WebUI, schema, or handler behavior requires a binary/UI build and restart in addition to any Pack release.
4. Determine who owns registration, credential refresh, health, request-time selection, quota measurement, and upstream OAuth. Do not silently move ownership into LocalRouter.

With a Pack argument, `lr` accepts bare `operation_id` or Pack-qualified `operation_key`; never turn dotted selectors into paths.

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

Authoritative schemas are `gateway/protocols/schema/protocol-pack-v2.schema.json` and `protocol-pack-v3.schema.json`; `docs/PROTOCOL-PACK-V3.md` is the full runtime contract.

## Non-negotiable boundaries

- Bind the gateway to loopback. Targets and adapter/module paths are operator-owned; request data never selects them.
- Keep credentials, cookies, locators, private upstream addresses, and pool contents out of Pack source, guides, logs, tests, and `.ai` project-visible notes. Installed protected material stays below `$XDG_DATA_HOME/localrouter/` with mode `0600`; isolated tests may set `LOCAL_GATEWAY_DATA_DIR`.
- Keep registration, CAPTCHA, human OAuth consent, anti-bot challenges, payment, and account production outside the request path.
- Use `pool.mode=external` when another gateway owns the complete pool. Use `pool.mode=local` only when LocalRouter owns request-time selection. An external maintainer may atomically update a private external-readonly source without transferring registration ownership.
- Default retry to `safe`. Never replay an unknown side-effecting outcome without idempotency or authoritative reconciliation.
- Prefer byte passthrough for multipart, files, SSE, WebSocket, gRPC, and unknown bodies. Apply JSON transforms only to observed JSON contracts.
- Give every route a stable `operation_id`. Define workflows only from observed IDs, status paths, transitions, terminal values, results, and cancellation behavior.
- Unknown capability, cost, balance, or account state stays unknown. Missing quota is not zero; balance is not provider task success.
- Consumer Tokens are call-only; pool concurrency, lease, cooldown, health, and quota still apply. Optional Agent maintenance requires a distinct maintenance-only `localrouter.maintain` Token. Never mix purposes.
- Pack is the common service view. Full Protocol Packs cover isolated routes, transforms, special auth, dedicated pools, adapters, or workflows; `/w` and `/mcp` are projections.

## Required lifecycle

1. Discover the live Pack and ownership boundary.
2. Confirm the selected lane and scope. Open an isolated `/manage/mcp` draft; never request, print, copy, or edit credentials there.
3. Use the narrowest semantic tool for Pack core, operation, supplier profile, or guide. Lint content edits. Use merge patch only for advanced/removal fields outside those tools.
4. Read the draft's `impact.files`, `impact.protocols`, and `impact.pool_ids`. Inspect every changed section and affected pool before planning.
5. Run strict validation, focused mocks, relevant transport/workflow tests, and the Skill doctor.
6. Plan once, retain the reviewed digest, and apply that exact digest. Draft drift invalidates the plan.
7. Re-discover through port 8317 and verify each applicable real layer. Resolve dynamic models with `lr find model --exact <pack>:<model-id>`; a zero-result exact query is blocking even if fuzzy suggestions exist. Before OpenAI-compatible runtime configuration, run `lr runtime-openai <pack> <exact-model>` for its public mount.
8. Report pass/fail/not-covered plus the revision digest. Do not create `.ai` evidence while automation is paused.
9. Post-apply verification failure preserves the draft and attempts the previous revision. Inspect `error.stage`, `error.code`, and `error.rollback`; reconcile unknown upstream outcomes before retry.

Run the discovery doctor when the listener is available:

```bash
python .agents/skills/localrouter-protocol-pack/scripts/protocol_pack_doctor.py
```

Use `--pack <id>` to narrow. Doctor success proves discovery/document consistency only; calls, streams, workflows, cost, and rollback need separate evidence.

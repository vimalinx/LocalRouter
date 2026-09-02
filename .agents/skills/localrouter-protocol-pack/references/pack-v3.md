# Protocol Pack v3 Agent reference

Use v3 when the Pack needs any of: named targets, expression transforms, HMAC/SigV4/OAuth2, route readiness evidence, explicit retry semantics, capability selectors, expiring leases, WebSocket, raw gRPC, adapters, or graph workflows. Keep v2 for stable legacy Packs that do not need these features.

## Authoring sequence

1. Define fixed named targets. Never derive a target URL from client input, an expression, or credential metadata. For compatible multi-provider routes, map a protected metadata value to those names with `target_selector`. When targets require different non-secret headers, User-Agent, or raw-query behavior, define named `upstream_profiles` and select them with `upstream_profile` or `upstream_profiles_by_target`.
2. Give every route a stable `operation_id`, transport, availability status, verification level/covered layers, and retry policy. Declare required non-path Query names in `query_parameters`; never leave a required Query only in prose.
3. Add literal transforms first, then the smallest required expression. Expressions do not receive credentials.
4. Choose credential ownership. For a LocalRouter pool, map capability metadata and use a boolean `pool_selector`. If several providers share the exact operation contract, use `target_selector` to map an account's provider metadata to a fixed target name. For an external gateway, retain one stable client credential.
5. Add readiness only from a cheap, non-mutating authenticated probe. A login page or health 200 is not proof that a generation/search operation is usable.
6. Add graph workflow steps only from observed provider contracts. Include an unmatched failure transition and a terminal path.
7. Use a loopback `http-envelope` sidecar for arbitrary network protocols. Use `wasm-envelope` only for deterministic, capability-free logic.
8. Write the Agent guide and complete the acceptance matrix before the hash-bound release.

## Agent contract review

- `capabilities` groups semantically related operations for discovery only. Every provider remains a distinct `operation_key` with its own pool, schema, pricing, retry, readiness, and verification facts.
- `compare` accepts 2–50 exact operation keys, preserves caller order, and returns complete independent descriptors with `recommendation: null`. It does not rank, merge, or choose.
- `query_parameters` lists required Query names. Describe, preflight, generated OpenAPI, and MCP input must agree and reject a missing required value before an upstream call.
- `availability.level` is one of `contract`, `mock`, `real-readonly`, `real-operation`, `real-stream`, or `real-workflow`; `covers` names only the layers actually observed. Keep unavailable operations discoverable with an exact blocking reason, while omitting them from directly callable MCP tools.
- Supplier request profiles apply before route transforms and protected auth. Use `auth` for all provider secrets. `query=preserve-raw` is for byte-sensitive query contracts and cannot coexist with query transforms. `user_agent=inherit` preserves legacy transport behavior; use `preserve`, `omit`, `localrouter`, or `configured` only from observed provider requirements.

## Retry review

- Default to `safe`.
- Side-effecting create/send/generate calls use `never`, or `idempotent` only when the provider honors the named idempotency header.
- `always` requires retained provider evidence.
- Treat an outcome-unknown response as reconciliation work. Do not issue a second request until resource ID, provider history, nonce, or an equivalent authoritative signal resolves it.

## Workflow review

- `operation` invokes one route; expressions can compute path params, query, and JSON body.
- `parallel` returns `{call_id: {status, body, error?}}` in the step context.
- `callback` exposes `callback_url` to workflow expressions and waits without polling.
- `transitions` are ordered. The first true `when` wins; an empty `when` is the fallback.
- `cancel_step` is a compensation path, not merely a local state flip.
- Do not put secrets in workflow input/context; persistent job files are private but still operational state.

## WASM review

The module must live at `<pack>/modules/<name>.wasm`, export the documented envelope ABI, and require no imports. LocalRouter grants no WASI or host capabilities. Keep the defaults unless measured evidence justifies a change. Include module bytes in plan/apply digest review.

See `docs/PROTOCOL-PACK-V3.md` for the full contract and `gateway/protocols/schema/protocol-pack-v3.schema.json` for structure.

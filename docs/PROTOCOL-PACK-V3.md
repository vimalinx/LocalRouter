# LocalRouter Protocol Pack v3

Protocol Pack v3 extends v2 from an HTTP template into a constrained universal gateway runtime. It keeps a fixed local mount and operator-owned targets, while allowing an Agent to describe request/response expressions, authentication, account selection, long-running state machines, streaming transports, and sandboxed adapters without changing Go source.

The authoritative schema is `gateway/protocols/schema/protocol-pack-v3.schema.json`; runtime validation is intentionally stricter.

## Expressiveness boundary

The built-in runtime directly supports HTTP/HTTPS, SSE/chunked bodies, multipart/files, WebSocket, and raw gRPC over HTTP/2 or h2c. Other schemes such as TCP, UDP, Unix sockets, and provider-specific binary protocols are represented as named targets and invoked through one of two adapter ABIs:

- `http-envelope`: a loopback sidecar receives a canonical JSON envelope and returns a canonical response envelope. The sidecar owns its protocol I/O.
- `wasm-envelope`: a capability-free WebAssembly module maps the same envelope to a response. It receives no WASI, network, filesystem, environment, clock, or process imports and is bounded by time, memory, input, and output limits.

This is an extensibility boundary, not a claim that every possible protocol can be inferred automatically. A new wire format needs a configuration, expression, or adapter implementation and retained evidence.

## Targets and operation availability

`base_url` remains an HTTP(S) fallback. `targets` adds named, operator-owned destinations. A route selects one with `target`; clients can never supply or override it.

When several providers expose the same public operation and wire contract, `target_selector` can bind protected credential metadata to those fixed target names. This makes one operation a real multi-provider pool without letting a request or an externally maintained account record provide a URL:

```json
{
  "targets": {
    "provider-a": "https://provider-a.example.com/v1",
    "provider-b": "https://provider-b.example.com/v1"
  },
  "pool": {
    "mode": "local",
    "credentials_file": "protocol-pools/compatible-providers.json",
    "strategy": "least-inflight",
    "max_attempts": 2
  },
  "routes": [{
    "operation_id": "generate",
    "methods": ["POST"],
    "path": "/generate",
    "summary": "Generate through any compatible provider",
    "target_selector": {
      "metadata_key": "provider",
      "mappings": {
        "a": "provider-a",
        "b": "provider-b"
      }
    },
    "retry": {"mode": "idempotent", "max_attempts": 2, "idempotency_header": "Idempotency-Key"}
  }]
}
```

Each protected credential carries only metadata such as `{"provider":"a"}`. Credentials with an unmapped value are ineligible for that route. Multiple credentials may map to the same target, so one provider can have many API keys while other compatible providers participate in the same scheduler. Priority, weight, balance, cooldown, leases, retry exclusion, and affinity continue to apply per credential; affinity therefore preserves both the creating key and its provider target. `default_target` is available only when an explicit fallback is safe. If providers need different paths, payload transforms, or authentication semantics, publish separate operations or normalize them in a trusted adapter instead of pretending they share one wire contract.

### Supplier request profiles

`upstream_profiles` keeps provider-specific request behavior reusable and operator-owned. A route selects a default with `upstream_profile`; `upstream_profiles_by_target` may override it after a protected credential resolves to a named target. This is the request-policy companion to `target_selector`: client input still cannot choose a provider or profile.

```json
{
  "upstream_profiles": {
    "provider-a": {
      "forward_headers": ["Accept-Language"],
      "set_headers": {"X-Provider-Version": "2026-09", "X-Route": "{{path.kind}}"},
      "remove_headers": ["Referer"],
      "user_agent": "omit",
      "query": "preserve-raw"
    }
  },
  "routes": [{
    "operation_id": "generate",
    "methods": ["POST"],
    "path": "/generate/{kind}",
    "summary": "Generate media",
    "upstream_profiles_by_target": {"provider-a": "provider-a"}
  }]
}
```

Rules apply in a deterministic order: legacy safe forwarding, profile forwarding/static/removal rules, route transforms, idempotency, then protected authentication. Route transforms may deliberately override non-secret profile headers. `user_agent` is `inherit`, `preserve`, `omit`, `localrouter`, or `configured`; `configured` requires `set_headers.User-Agent`, while forwarding/removing User-Agent through the generic lists is rejected as ambiguous. `query=preserve-raw` retains byte-level query order and escaping and therefore cannot be combined with query transforms. The default `normalized` mode uses canonical query encoding.

Profiles cannot set, forward, or remove authentication, cookie, host, hop-by-hop, content-length, or token/key/secret-like headers. Put provider credentials in `auth` or a protected pool, never in `set_headers`. The same profile semantics feed HTTP, WebSocket, raw gRPC, and adapter envelopes. A new Pack that omits profiles retains the prior transport behavior.

Each operation can declare `availability.status`: `draft`, `observed`, `verified`, `blocked`, or `deprecated`. `availability.level` records the strongest tested layer (`contract`, `mock`, `real-readonly`, `real-operation`, `real-stream`, or `real-workflow`), while `covers` names exactly which of schema, auth, pool, upstream, response, stream, workflow, and side effects were exercised. Verified evidence can expire via `verified_at` plus `valid_for_seconds`. `readiness.mode=probe` performs a cached authenticated GET/HEAD and may require a boolean expression and currently verified operations. A parseable Pack or HTTP 200 alone is not provider readiness.

Routes may declare full JSON Schema values in `request_schema` and `response_schema`. When only `request_example` is present, generated OpenAPI and MCP infer a conservative schema from that example; an explicit schema always wins.

Routes may also declare stable `capabilities` such as `web.search`, `ai.chat`, `video.generate`, or `model3d.generate`. `GET /agent/operations` exposes every Token-visible operation independently. `/agent/resolve` returns all exact capability matches, falling back to text matches only when no exact item exists; it never collapses providers or silently selects one. `/agent/compare` accepts 2–50 exact operation keys and returns their full contracts in caller order with `recommendation: null`. Every result keeps its own `operation_key=<pack>.<operation_id>`, readiness, verification, pool, schema, pricing, retry and guide metadata. `/agent/operations/<pack>/<operation>` progressively discloses one explicitly chosen sanitized contract, while `/agent/preflight` validates the proposed method, model policy, path parameters, input schema, price status, and runtime readiness without calling the provider. Consumers cache these results only while both discovery's `contract.digest` and `contract.schema_version` remain unchanged.

## Expressions

Expressions use `expr` with an 8 KiB source and 512-node compile bound. Request environments expose `body`, `path`, `query`, `headers`, `method`, `status`, `now_unix`, and `now_rfc3339`. They can populate `set_expr`, `replace_expr`, `header_expr`, and `query_expr`. Response expressions see the transformed response body, headers, and status.

Expressions are deterministic mappings; they cannot read secrets, files, the network, or process state. Authentication remains a separate protected stage.

## Authentication

v3 supports `none`, bearer/header/cookie/dual, HMAC-SHA256, AWS SigV4, and OAuth2 client-credentials or refresh-token grants. Installed secret files remain mode `0600` below `$XDG_DATA_HOME/localrouter`; OAuth access tokens are cached only in memory and invalidated after upstream 401. Human OAuth consent, CAPTCHA, account creation, payment, and provider-specific refresh automation remain outside request handling.

## Safe retry and unknown outcomes

Every route has an explicit retry policy:

- `never`: no replay.
- `safe`: retries only naturally idempotent methods or a request known not to have been written.
- `idempotent`: side-effecting retries require `idempotency_header`; LocalRouter preserves or creates one key across attempts.
- `always`: operator assertion for a provider contract that is safe to replay.

If a side-effecting request was written and the connection fails without an idempotency key, LocalRouter returns an `outcome: unknown` 5xx response and does not replay it. A timeout is not proof that the provider did nothing.

## Capability-aware pools

Local pools add expiring in-flight leases and an inter-process file lock, so a crash does not leave permanent capacity and two LocalRouter processes do not race state writes. `pool_selector` evaluates against `id`, `metadata`, `balance`, `priority`, and `weight` before the normal strategy. `target_selector` then resolves an eligible credential to one of the Pack's fixed named targets. External-readonly sources can map arbitrary string metadata through `metadata_paths`.

Selectors decide request-time eligibility only. The external maintainer still owns registration, replenishment, refresh, and authoritative account mutation.

## Graph workflows

v3 workflows may keep the legacy create/poll contract or define `steps`. Step kinds are:

- `operation`: invoke one stable `operation_id` with expression-derived path/query/body.
- `parallel`: invoke up to 32 named calls concurrently and expose their status/body map in `context.<step>`.
- `callback`: persist a capability URL and wait for an authenticated-by-random-token callback.

Ordered transitions implement branching, loops, polling, terminal results, and compensation. `cancel_step` can invoke provider cancellation/cleanup before reaching `cancelled`. Jobs persist with mode `0600`; a bounded background scheduler advances due jobs after restart, while GET can also advance one due step. Callback tokens and workflow input are omitted from the public job representation.

Workflow expressions see `input`, `context`, `response`, `status`, `error`, `callback_url`, and sanitized job metadata. Limits and terminal transitions prevent an unbounded implicit execution path; provider operations still need safe route retry policies.

## Adapter envelope

Input:

```json
{
  "schema_version": "1",
  "operation_id": "binary.invoke",
  "request": {
    "method": "POST",
    "url": "tcp://127.0.0.1:9000/invoke",
    "headers": {"Content-Type": ["application/octet-stream"]},
    "body_base64": "AAE="
  }
}
```

Output:

```json
{
  "status": 200,
  "headers": {"Content-Type": ["application/octet-stream"]},
  "body_base64": "AgM=",
  "outcome": "complete"
}
```

`outcome` is `complete`, `not_sent`, `sent`, or `unknown`. Unknown provider outcome is surfaced and never silently replayed.

The WASM ABI exports `memory`, `alloc(len) -> ptr`, `transform(ptr,len) -> (output_ptr << 32 | output_len)`, and `dealloc(ptr,len)`. Modules live only at `<pack>/modules/*.wasm`, participate in candidate digests and immutable revisions, and are restored by rollback.

## Release and evidence

### Quota telemetry contract

For a local or external-readonly pool, quota is mapped independently from health and request cost. A source may declare `quota_total_path`, `quota_remaining_path`, `quota_used_path`, `quota_unit_path`, `quota_status_path`, and `quota_checked_at_path`; legacy `balance_path` remains a fallback for remaining-only data. `quota_stale_after_seconds` controls freshness.

The protected record shape maintained by an external registrar is:

```json
{"quota":{"total":100,"remaining":35,"used":65,"unit":"credits","status":"confirmed","checked_at":"2026-08-30T12:00:00Z"}}
```

Missing measurements must be stored as `{"quota":{"status":"unknown","checked_at":"..."}}`, never as zero. LocalRouter may derive `used = total - remaining` or `remaining = total - used`, but never invents a total. Aggregation is allowed only when every included account uses the same unit. Remaining-only, stale, partial, mixed-unit, and untracked states do not produce a percentage progress bar. Quota appears only in the authenticated management view; public discovery and client docs remain credential- and account-free.

The authenticated management view may also expose `quota.reference_value`. LocalRouter derives it only when one monetary pricing denominator matches the quota unit, such as `10 USD / per-1000-credits` with quota in `credits`. It scales each available total, used, and remaining value independently; it never creates missing quota or invents a total. Conflicting rates return `status: "ambiguous"` without amounts, while incompatible or absent rates omit the field. The result is a reference value at the published rate, not proof of payment or an account invoice.

### Request usage accounting

Every accepted Pack request produces one private protocol event. For JSON and SSE responses that expose OpenAI, Anthropic, or Gemini usage shapes, LocalRouter normalizes input, output, cache-read, cache-write, reasoning, and total tokens. Cache and reasoning fields are dimensions of the input/output count and are not added to total a second time. The request model is recorded when the transformed provider request contains a bounded string `model` field.

New events freeze their attributable cost at request completion. An explicit `usage.cost_usd` or `usage.total_cost_usd` is stored as provider-reported cost. Otherwise LocalRouter can add an exact operation `per-request` rate and exact-model input/output/cache/reasoning/total-token rates from the Pack pricing entries. Unsupported units, currencies, missing usage, and conflicting rates remain partial or unavailable. Failed or unknown outcomes do not invent a charge. This ledger is accounting evidence, not payment, balance mutation, or an upstream invoice.

`/manage/mcp` is separate from every service-call surface. It uses the administrator credential by default. Optional Agent access is disabled by default; when an operator enables it, an Agent without filesystem access may use a distinct revocable Bearer Token carrying `localrouter.maintain`. That maintenance Token cannot call consumer services. The Agent opens an isolated draft and should prefer `localrouter_draft_put_pack`, `localrouter_draft_put_operation`, and `localrouter_draft_put_upstream_profile`: these tools publish their live input schemas, generate explicit safe defaults, upsert one semantic unit, and reject protected header misuse. `localrouter_draft_lint_pack` returns a structured field path, allowed enum values, and next repair action. The generic merge patch remains available for advanced or removal edits. The Agent then reviews exact file/protocol/pool impact, plans the reviewed digest, and applies that exact digest. LocalRouter owns paths, JSON/YAML formatting, atomic writes, local post-install verification, immutable revisions, and automatic restoration of the previous revision after a failed local verification. Drafts are preserved on failure and operational locators are not returned.

`POST /mcp` exposes the same published route contracts as stateless MCP tools. `tools/list` omits disabled or unready Packs; `tools/call` accepts `path_params`, `query`, and `body`, then uses the normal route, auth, pool, retry, and event pipeline. Bearer Token policies can independently constrain `mcp`, `p`, `w`, `v1`, and `v1beta` surfaces.

Run unit, race, schema, Mock, and listener acceptance before `validate → plan → exact digest apply`. After apply, re-read discovery/docs and test the smallest meaningful real operation. Record configuration, listener, transport, workflow terminal state, real provider, cost/quota, and rollback as separate rows; unsupported or unauthorised real calls remain `not covered`.

# LocalRouter protocol and pool architecture

## Two forwarding planes

LocalRouter now separates two jobs that should not share one implicit contract:

| Plane | Public mount | Purpose | Routing authority |
|---|---|---|---|
| Model relay | Profile-declared paths such as `/v1/*`, `/v1beta/*` | Byte-preserving model APIs whose path ownership, authentication and model catalogue are data | `channel-profiles.json` plus LocalRouter's generic channel selector |
| Custom protocol | `/p/<protocol>/*` | Allowlisted HTTP/SSE/files, WebSocket, raw gRPC, expressions, and adapter-backed services | Validated Protocol Pack |
| Async workflow | `/w/<protocol>/<workflow>/*` | Persistent legacy polling or v3 graph state machine with branch/parallel/callback/cancel | Validated Workflow contract |

The custom plane is not an arbitrary reverse proxy. A template fixes named targets, transport/adapter, authentication mode, credential ownership, allowed methods/paths, timeout, retry semantics and safe headers. Client requests cannot supply a target URL, credential path, or module path. See `PROTOCOL-PACK-V3.md` for the universal extension boundary.

## One extension rule, no provider branches

The core does not switch on a supplier name. A service fits one of three generic contracts:

1. A byte-compatible model endpoint is a channel Profile: paths, default URL, header/query/no-auth placement, fixed protocol headers and model-result extraction live in the mode-600 XDG configuration.
2. A declarative service is a Protocol Pack: routes, transformations, auth, target selection, pool policy, retry, affinity, pricing and documentation remain validated data.
3. A login session, binary protocol or multi-stage exchange is a fixed-loopback `http-envelope` sidecar (or a capability-free `wasm-envelope` transform). The sidecar implements provider I/O only; LocalRouter still owns caller auth, target allowlisting, credential selection, leases, retry policy and sanitized discovery.

The public distribution implements rule 3's generic envelope contract but ships no sidecar. An operator may supervise a private fixed-loopback adapter and reference it from a private Pack without introducing a supplier branch into the core.

## External maintainer boundary

An optional external maintainer may already own a provider account pool:

- stable friend-facing key versus private upstream keys;
- round-robin key selection;
- immediate in-memory disabling after 401/403;
- switching keys on 429;
- provider-specific upstream authentication;
- sticky resource ID to credential mapping for long-lived resources;
- SSE/chunked response pass-through.

If that upstream gateway owns selection, the Pack uses `pool.mode=external` and LocalRouter stores only its stable client credential. If the maintainer owns replenishment while LocalRouter owns request-time selection, the Pack uses a protected external-readonly source. Neither form copies an operator's pool into public Pack source.

## What CLIProxyAPI teaches us

The audited reference is [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) at commit `f0de1d008fe8881dcb7431cf97b147295874c2b2`.

Its account-pool design is not just a slice of tokens. The useful abstraction is:

```text
Store -> Auth records -> eligibility/cooldown filter -> Selector
      -> Provider Executor -> Result classifier -> state update
      -> optional refresh scheduler -> persisted Auth update
```

The comparison produced two groups: request-time patterns adopted by the opt-in LocalRouter-owned v2 pool, and provider-lifecycle patterns deliberately left to external maintainers.

1. Credential records are first-class state with provider, supported models, disabled state, priority, weight, quota/cooldown state and refresh metadata.
2. Selection happens only after availability filtering. Higher priority tiers win cold selection; round-robin, smooth weighted round-robin or fill-first operates inside the eligible tier.
3. Round-robin remembers the last credential ID, not merely an array index, so temporary candidate removal does not distort rotation.
4. Session affinity wraps the base selector, has a TTL, and fails over only when the bound credential becomes unavailable.
5. A request tries distinct credentials and classifies results. LocalRouter disables or cools down 401/403, cools down 429 and transient failures, and never retries a streamed response after bytes are sent.
6. Generic OAuth2 token exchange/cache is available for a Pack, but human consent, provider-specific refresh scheduling, account production, CAPTCHA and replenishment remain with the external maintainer.
7. Credential files are mode `0600`; runtime cooldown persistence is a separate optional concern.

The design review used these files at the pinned public commit; the snapshot is
not vendored or linked into LocalRouter:

- `sdk/cliproxy/auth/conductor.go`
- `sdk/cliproxy/auth/selector.go`
- `sdk/cliproxy/auth/conductor_execution.go`
- `sdk/cliproxy/auth/conductor_cooldown.go`
- `sdk/cliproxy/auth/conductor_refresh.go`
- `sdk/cliproxy/auth/auto_refresh_loop.go`
- `sdk/auth/filestore.go`

## Ownership decision

LocalRouter does not duplicate an external gateway's OAuth or API-key pool. Such a private Pack declares `pool.mode=external`, uses one stable client credential, and delegates provider-specific lifecycle to the upstream adapter.

For providers with no existing pool authority, `pool.mode=local` opts into protected credential loading, availability filtering, priority tiers, round-robin/LRU/least-inflight/balance-aware/smooth-weighted selection, bounded distinct-credential retry, cooldown/disable state, concurrency limits and resource affinity. Runtime state and credentials are stored separately; state never contains Secret values.

When an external maintainer owns replenishment and refresh but LocalRouter should select per request, `pool.mode=local` may declare an external-readonly `source`. The Pack contains only JSON/JSONL field mapping. A private mode-600 locator contains the real path; the target must be a mode-600 regular file owned by the LocalRouter user. External identities are hashed before entering state, avoiding a second credential copy.

Quota telemetry follows the same ownership boundary. The registrar/maintainer probes the provider and atomically writes normalized `quota.total`, `quota.remaining`, `quota.used`, `quota.unit`, `quota.status`, and `quota.checked_at` fields into the protected authority file. LocalRouter reads those fields, derives only algebraically knowable values, applies staleness, and refuses to aggregate mixed units. A successful registration without provider telemetry is recorded as `unknown`, not zero; any pool without an authoritative quota source remains gray.

The implementation keeps the following conceptual boundaries even though they are internal runtime components rather than exported Go interfaces:

```go
type CredentialStore interface { List(context.Context, protocolID string) ([]Credential, error) }
type Selector interface { Pick(context.Context, Request, []Credential) (Credential, error) }
type ResultPolicy interface { Observe(Credential, Result) StateTransition }
type Refresher interface { Refresh(context.Context, Credential) (Credential, error) }
```

The Pack must never contain OAuth browser automation, registration, CAPTCHA, anti-bot or payment logic. An external maintainer may atomically refresh the protected pool file; LocalRouter owns only request-time selection and state transitions.

## AI-maintained documentation workflow

1. AI proposes a template edit under `gateway/protocols/` and updates route summaries/examples in that same file.
2. JSON decoding rejects unknown fields; semantic validation rejects unsafe URLs, path escapes, unsupported methods and credential paths outside the private data directory.
3. Unit and real-binary protocol tests run before apply.
4. Authenticated `validate` and `plan` produce a content digest; `apply` accepts only the exact reviewed digest and captures an immutable revision. A failed or drifted candidate does not replace the live registry.
5. `/docs`, `/.well-known/localrouter.json`, the JSON Pack manifests and OpenAPI render only the sanitized client contract; upstream URLs and credential locations are never returned.
6. Agent-authored Markdown lives outside the generated contract under `protocols/<id>/guides/`. Strict front matter binds each guide to stable `operation_id` values, so invalid references fail reload without replacing the live registry.
7. Human HTML, Markdown, JSON discovery, examples and OpenAPI are served from the same LocalRouter listener. Consumer Agents do not need filesystem access.
8. History exposes sanitized immutable revision receipts; rollback validates a known revision, atomically restores managed Pack files, and swaps the live registry.
9. `/docs/pools/index.json` and `/docs/pools/catalog.md` publish the sanitized ownership/compatibility catalog through the same port. Catalog JSON and Markdown participate in candidate digest, revision capture and rollback.

This keeps AI maintenance useful without letting an AI response silently turn the gateway into an open proxy or secret browser.

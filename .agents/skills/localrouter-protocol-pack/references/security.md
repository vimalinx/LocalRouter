# Security review

Read this reference for authentication, targets, adapters, forwarded headers, untrusted payloads, or protected pool sources.

## Trust boundaries

- LocalRouter's complete operator listener stays on a loopback IP. An optional private-LAN listener may expose only Service-Token-authenticated consumer routes plus sanitized discovery/docs. Do not register or proxy the console, `/local/status`, `/local/api`, or `/manage/mcp` on that listener.
- Public API Tokens authorize consumer surfaces only. The human console and `/local/api` use password-free loopback access by default and may be switched to the administrator credential by the operator. `/manage/mcp` still requires that separate administrator credential or an explicitly enabled maintenance-only Agent Token. A password-free console is never implicit Agent mutation authority.
- Provider targets are operator-owned Pack constants. Client input must never become a target URL, DNS name, socket path, OAuth token URL, adapter path, or WASM module path. `target_selector` accepts credential metadata only as a lookup key into a Pack-owned map of fixed target names; an unmapped value is ineligible rather than interpreted as an address.
- Workflow callback base URLs are operator-owned. On a LAN listener, use the configured private bind address or an explicit `LOCAL_GATEWAY_LAN_PUBLIC_BASE_URL`; never derive callbacks from an untrusted request `Host` or forwarded header.
- Human OAuth consent, CAPTCHA, account registration, anti-bot handling, and payment occur outside the request path.

## Secrets

- Store installed protected values only below `$XDG_DATA_HOME/localrouter/` or in the external authority's private source. Isolated tests may set `LOCAL_GATEWAY_DATA_DIR`.
- Require mode `0600` for secret files, pool locators, pool sources, state, and job files; reject loose permissions, non-regular files, wrong ownership, and unsafe symlinks.
- Never place credentials, cookies, account identities, private upstream addresses, source paths, or unredacted provider bodies in Pack JSON, guides, tests, terminal output, screenshots, events, or project-visible ledger notes.
- Use placeholders in fixtures. A string that merely looks fake is not acceptable if it is a real credential.
- Do not forward `Authorization`, `X-Api-Key`, `Host`, hop-by-hop headers, or arbitrary client authentication upstream. Use the Pack `auth` stage.
- `upstream_profiles` contain only non-secret provider request policy. Token/key/secret-like headers, Cookie, Authorization, Host, Content-Length, and hop-by-hop headers are reserved and rejected. A profile is selected only by Pack route and resolved target, never by client input.

## Authentication

- `bearer/header/cookie/dual`: verify the exact header/cookie template with a protected fixture.
- HMAC: prove canonical message bytes, timestamp, key ID, encoding, and clock expectations.
- SigV4: prove region, service, canonical path/query/headers, payload hash, and provider response.
- OAuth2: token URL must be HTTPS or loopback HTTP. Prove client authentication mode, scopes/audience/extra fields, cache expiry skew, and 401 invalidation. Refresh-token ownership remains explicit.
- Never use a browser login page or an unauthenticated health page as authentication/readiness proof.

## Payload and execution limits

- Protocol request/response bodies are bounded to 32 MiB; draft files to 2 MiB; guides to 1 MiB; WASM modules to 16 MiB. Do not design a Pack that relies on larger implicit buffering.
- Prefer streaming/byte passthrough for opaque data.
- Expressions are bounded deterministic mappings without file/network/secret access.
- WASM receives no WASI or host capabilities and must stay within configured time/memory/output limits.
- Loopback adapters are separate trusted processes; review their own input validation, resource bounds, and target allowlist.

## Security acceptance

Verify operator loopback binding; optional LAN listener private-address validation, exact Origin allowlist, Service Token enforcement and absence of all management routes; the advertised console-auth switch and its default-off state; `/manage/mcp` administrator/API token separation; target immutability, path allowlists, file owner/modes, symlink rejection, sanitized management output, bounded payload behavior, no secret leakage, and safe unknown outcomes. A successful upstream response does not replace this review.

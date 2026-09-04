# Security policy

LocalRouter is local-first. Its complete operator listener refuses non-loopback
addresses. An optional second listener may bind a private LAN address, but it
registers only Service-Token-authenticated consumer routes plus sanitized
discovery and documentation. It never registers the console, `/local/status`,
`/local/api`, or `/manage/mcp`.

Do not expose port 8317, the operator listener, token files, pool sources, or
provider credentials to a network. Restrict the LAN service port to the intended
private subnet. Plain LAN HTTP is not suitable across an untrusted network; use
a reviewed TLS reverse proxy or private overlay without forwarding the operator
listener.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/vimalinx/LocalRouter/security/advisories/new). Do not
open a public issue containing credentials, cookies, account identities,
private upstream addresses, pool contents, database files, or raw provider
responses. Include a minimal redacted reproducer and the affected revision.

## Supported release

Only the newest tagged release is supported. Before reporting, reproduce from
a clean clone and run `./tests/verify.sh` plus the
release checks documented in `docs/OPEN_SOURCE_RELEASE.md`.

Security fixes must preserve loopback binding for the operator plane, route
isolation on the optional LAN service plane, separate consumer/admin tokens,
operator-owned upstream targets, bounded payloads, safe retry semantics, and
mode-0600 protected files below the LocalRouter XDG data/state directories.

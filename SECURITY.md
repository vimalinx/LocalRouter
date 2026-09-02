# Security policy

LocalRouter is designed for a single machine and refuses non-loopback listen
addresses. Do not expose port 8317, the admin API, token files, pool sources,
or provider credentials to an untrusted network.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/vimalinx/LocalRouter/security/advisories/new). Do not
open a public issue containing credentials, cookies, account identities,
private upstream addresses, pool contents, database files, or raw provider
responses. Include a minimal redacted reproducer and the affected revision.

## Supported release

Only the newest tagged release is supported. Before reporting, reproduce from
a clean clone and run `./tests/verify.sh` plus the
release checks documented in `docs/OPEN_SOURCE_RELEASE.md`.

Security fixes must preserve loopback binding, separate consumer/admin tokens,
operator-owned upstream targets, bounded payloads, safe retry semantics, and
mode-0600 protected files below the LocalRouter XDG data/state directories.

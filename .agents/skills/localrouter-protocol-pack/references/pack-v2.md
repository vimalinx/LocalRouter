# Protocol Pack v2 reference

The authoritative JSON Schema is `gateway/protocols/schema/protocol-pack-v2.schema.json`. Runtime validation is stricter than schema-only validation.

## Transform order

Request processing order:

1. Match the public method and path allowlist.
2. Render `upstream_path` from public path parameters.
3. Apply configured query values.
4. Read the bounded request body.
5. Apply JSON rename, set, remove, extract, and envelope operations.
6. Select a credential and inject authentication.
7. Send the upstream request.

Response processing order:

1. Classify the HTTP result and update pool state.
2. Retry a distinct eligible credential only before downstream bytes are sent.
3. Extract and bind an affinity resource ID from the original upstream JSON.
4. Apply the public response transform.
5. Return the sanitized public response.

JSON paths use gjson/sjson dot syntax. `set` values are literal JSON values, not template strings.

## Local pool file

The protected file has this shape:

```json
{
  "schema_version": "1",
  "credentials": [
    {
      "id": "account-a",
      "secret": "stored-only-in-the-protected-file",
      "priority": 10,
      "weight": 1,
      "balance": 100,
      "expires_at": "2026-09-01T00:00:00Z",
      "disabled": false,
      "metadata": {"capability": "video"}
    }
  ]
}
```

The runtime state file stores only credential IDs, last use, inflight count, cooldown, failures, disabled reason, selector weight, and resource affinity. It never stores the secret.

Lower numeric priority wins. Selection happens only inside the best eligible priority tier. A request excludes credentials already tried.

For an externally maintained JSON or JSONL pool, use `pool.source` instead of copying it. `locator_file` is a private mode-600 JSON file containing the absolute source path. The source target must resolve to a regular mode-600 file owned by the LocalRouter user. Map `id_path`, `secret_path`, and optional eligibility/balance/weight/priority/expiry paths. External identities are SHA-256-derived before entering runtime state. Use `cookie-list-json` only for a browser Cookie export string and name the one Cookie to extract with `secret_selector`.

## Workflow contract

The create operation must be a POST route without path parameters. The poll operation must be a GET route with exactly one path parameter. Local Job creation calls the public transformed operation, so `resource_id_path` and `status_path` refer to public response shapes.

`POST /w/<pack>/<workflow>` creates a persistent local Job. `GET /w/<pack>/<workflow>/<job>` returns cached state before `next_poll_at`; once due, it advances exactly one upstream poll. This makes Agent polling bounded and restart-safe.

Terminal states are `succeeded`, `failed`, `cancelled`, and `timed_out`.

## Streaming boundary

Pool failover is safe only before the first downstream byte. Do not configure response JSON transforms or server-managed Workflow polling on SSE/chunked operations. Preserve partial stream events when a provider disconnects; do not blindly replay a side-effecting request.

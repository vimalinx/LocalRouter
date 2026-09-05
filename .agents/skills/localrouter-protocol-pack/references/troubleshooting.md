# Troubleshooting

Read this reference after a concrete failure. Preserve the layer where it failed instead of immediately editing or resetting everything.

| Symptom | Likely layer | First checks |
|---|---|---|
| Discovery/docs unavailable | listener/runtime | `/local/status`, loopback port, binary/assets |
| MCP `pack_invalid`, lint `valid=false`, or `draft_invalid` | authoring contract | follow structured `issues[].path`, `allowed`, and `suggested_action`; fix the retained semantic draft and lint again |
| Maintenance 401 | LocalRouter authorization | selected administrator or Agent-maintenance locator and its expected header; do not inspect upstream accounts |
| `agent_maintenance_disabled` | LocalRouter authorization | Agent lane is off; stop rather than reading the administrator credential |
| Maintenance 403 capability error | LocalRouter authorization | human-selected maintenance-only Token and `localrouter.maintain`; do not request the administrator credential |
| `draft_changed_after_review` or `live_changed_after_plan` | lifecycle drift | re-read impact against live state and plan the reviewed digest again |
| `verification_failed` | local installation | inspect structured automatic rollback result before retrying |
| Public 401/403 | upstream credential/entitlement | sanitized pool state and direct read-only provider probe |
| 402 | provider quota/billing | balance/quote separately; do not mark credential malformed |
| 429 | rate limit | provider retry headers, cooldown, concurrency, idempotency |
| 503 before upstream | readiness/pool | operation availability, ready/disabled/expired/cooling/busy counts |
| 520 or `outcome: unknown` | side-effecting transport | reconcile provider state; never blind replay |
| Balance present but 503 | schedulability | login, entitlement, Nexus/capability, readiness operation |
| Gray quota | telemetry absent | official balance API and source mappings; never substitute zero |
| Stale quota | maintainer/freshness | last successful check, timer/service, preserved old value |
| Stream connects but no terminal event | transport/provider | buffering, response headers, partial events, provider close |
| Workflow never completes | state machine | resource ID, affinity, transitions, fallback, next poll, restart scheduler |
| Rollback passes but provider still fails | external state | pool/provider/maintainer was not part of Pack revision |

## Diagnostic order

1. Public discovery and generated docs.
2. Draft/static validation.
3. Local listener, service/proposal consumer authorization, human administrator maintenance, then optional Agent-maintenance switch and capability.
4. Sanitized operation availability/readiness.
5. Sanitized pool scheduler and quota state.
6. Direct cheap provider probe through the owning maintainer.
7. Small real operation if authorized.
8. Transport/workflow terminal behavior.

Consumer errors expose `code`, `reason`, `retryable`, `retry_after`, `next_action`, and ready `alternatives`. Use those fields before escalating to Pack inspection. Run `lr preflight` to reproduce authorization, readiness, schema, price-status, path-parameter, and required-query checks without another provider request; `ok=false` also exits nonzero. Preflight does not call a provider model catalogue, so prove a dynamic model separately with `lr find model --exact <pack>:<model-id>`. When several providers expose the same capability, use `lr compare` to inspect their independent readiness, verification, pool, price, retry, and schema facts; a failed provider is not permission for LocalRouter to select another one silently. A `watch_timeout` is only a client wait boundary: keep the Job ID and resume `lr watch`; it does not cancel the provider task.

Do not reset a whole pool to hide 401/429/unknown-outcome evidence. Do not treat a successful health, login, balance, catalogue, or mock as a successful paid task. Retain failed runs and follow them with a new passing run after repair.

For an authorized paid call, capture the raw `lr call` response and exit status exactly once, then run `jq` or another parser against the captured value. If the parser or display command fails, repair the local inspection step; do not repeat the upstream request unless the first outcome is authoritatively known and a new call is explicitly in scope.

# Port-only authoring

Read this reference when the Agent can reach port 8317 but cannot access the repository filesystem. The listener is the authority; do not ask a consumer Agent to locate project files.

## Authentication

Discovery and documentation are public on loopback. Calls to `/agent/*`, `/p/*`, `/w/*`, `/mcp`, `/v1/*`, and `/v1beta/*` use `Authorization: Bearer <API token>`; these Tokens are service-call credentials only. `/manage/mcp` is a separate maintenance surface. Operators use `X-Local-Admin`. Agent access is disabled by default; when discovery reports `maintenance.auth.agent_token.enabled=true`, a separately issued maintenance-only Bearer Token with `localrouter.maintain` may use it.

The human enables Agent maintenance and marks a distinct Token as maintenance-only in `/#tokens`. Do not ask for or use the console administrator credential. Never print a Token, include it in a guide, persist it in a draft, or copy it into an Agent-visible note. `agent_maintenance_disabled` means stop and ask the operator to use the console or explicitly enable the Agent lane. `maintenance_token_required` and `maintenance_capability_required` concern LocalRouter authorization, not an upstream provider.

## Draft lifecycle

1. `GET /.well-known/localrouter.json`; follow `maintenance.mcp` and documentation links.
2. Call MCP `tools/list` on `/manage/mcp`. Tool schemas are the live authority; do not copy a stale request shape from this reference.
3. `localrouter_draft_open` seeds or reopens a mode-600 isolated draft.
4. Use `localrouter_draft_get_pack` to read safe semantic content. Operational locators and private targets are omitted but preserved by later merge patches.
5. Prefer one-purpose semantic tools:
   - `localrouter_draft_put_pack` creates or updates the v3 Pack core and writes explicit safe defaults (`auth.type=none` when omitted, credential readiness, timeout);
   - `localrouter_draft_put_upstream_profile` upserts one supplier request policy without permitting credentials in headers;
   - `localrouter_draft_put_operation` upserts one operation and supplies safe transport/retry/draft-availability defaults;
   - `localrouter_draft_lint_pack` returns `valid` plus structured `issues[].path`, `allowed`, and `suggested_action` without changing the draft.
6. Use `localrouter_draft_patch_pack` only for an advanced field/removal not owned by a narrower tool, and `localrouter_draft_put_guide` for guide metadata plus Markdown. LocalRouter owns filenames, paths, JSON indentation, YAML front matter, atomic writes, and size limits.
7. `localrouter_draft_review` runs strict validation and returns one exact digest. Its validation failures also include the first structured issue.
8. Review all of:
   - `impact.files[]`: exact path, add/modify/remove, area, Pack;
   - `impact.protocols[]`: sections, operations added/modified/removed, pool mode before/after;
   - `impact.pool_ids[]`: every runtime pool exposed to the change.
9. Stop if the impact is broader than the user's request or differs from the human review in `/#control`.
10. Call `localrouter_draft_plan` with the exact `reviewed_digest`. A later edit invalidates review and plan.
11. Call `localrouter_draft_apply` with the plan digest. The server atomically installs, re-reads the live tree, verifies the digest, and restores the prior revision if that local verification fails.
12. Re-read discovery, docs, history, and live Pack state. Use `localrouter_draft_abort` only when the retained draft is no longer useful.

Shell Agents may use `lr manage-list` and `lr manage-call` only when `LOCALROUTER_MAINTAINER_TOKEN_FILE` was explicitly supplied for them and discovery says the Agent lane is enabled. These commands still call the port MCP endpoint and never expose the Token value. Ordinary `lr call` uses the separate consumer Token locator. With no Agent locator, `lr manage-*` is an operator command that reads the administrator credential and must not be run by a consumer Agent.

## Expected status classes

| Status | Meaning | Action |
|---|---|---|
| HTTP 401 | missing/wrong LocalRouter maintenance credential | fix the selected maintenance lane; do not diagnose upstream |
| `agent_maintenance_disabled` | optional Agent lane is off | stop; ask the operator to maintain or explicitly enable Agent access |
| HTTP 403 capability error | maintenance Token lacks `localrouter.maintain` | ask the human to select a distinct maintenance-only Token |
| `invalid_arguments` | tool input did not match its schema | fix content, then call again |
| `pack_invalid` or lint `valid=false` | one Pack field violates the runtime contract | follow `issues[].path`, `allowed`, and `suggested_action`; lint again |
| `draft_invalid` | Pack or guide validation failed | fix the retained draft; never bypass review |
| `draft_changed_after_review` | content changed after review | run review again, inspect impact, then plan |
| `live_changed_after_plan` | another reviewed release won the race | reopen/review against current live state |
| `verification_failed` | installed tree failed local post-apply checks | inspect `error.rollback`; the draft remains available |
| `rollback_failed` | previous immutable revision could not be restored | stop mutation and report the live digest and structured error |

Port-only authoring changes Pack-managed content. It cannot modify Go/WebUI/runtime schemas, pool contents, locators, credentials, console Tokens, or administrator settings.


## Agent-led service setup

When discovery advertises `service_workspace`, a registered Service Token can
prepare an owned proposal through `lr init`, `lr guide`, `lr setup templates`, `lr setup template <id> <version>`, `lr setup schema`, and
`lr setup prepare @proposal.json`. Preparation does not install Packs or grant
authority. The human reviews the complete connection, explicit bundle and optional
compatible maintenance scope together in `/#setup`, then approves the exact
proposal digest. Continue with `lr setup get`, ordinary describe/preflight/call,
and `lr setup verify`; verification only reads existing evidence.

Use `lr setup reconcile` for interrupted applies. It never replays provider calls
and refuses to recreate grants after an intervening authority change. A scoped
maintenance-only Token uses `LOCALROUTER_SETUP_LANE=maintenance` for preparation
and `lr setup apply <id> <digest>` for compatible repairs. New authority still
requires human approval. Read `lr guide` and the published `/docs/agent.json` for the current port-only contract; repository access is not required.

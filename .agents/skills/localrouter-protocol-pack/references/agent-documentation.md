# Agent-facing documentation

Read this reference when adding or changing a guide. Generated docs describe the machine contract; authored guides explain correct use, limits, and operational detail. Generation never overwrites authored guidance.

## Front matter

```yaml
---
id: quickstart
title: Human-readable title
summary: What the Agent learns here.
status: draft
operations:
  - jobs.create
visibility: local
---
```

- `draft`: proposal not observed.
- `observed`: provider behavior was directly observed but full acceptance is incomplete.
- `verified`: retained acceptance evidence exists; add `last_verified: YYYY-MM-DD`.
- `deprecated`: replacement and migration are documented.

Every listed operation must exist in the Pack. Use stable `operation_id` names, not transient provider labels.

`operation_id` is a semantic selector, not a URL fragment. Agents pass it to `lr call`, Agent describe/preflight APIs, or MCP. Direct HTTP callers must use the runtime-generated `call_url` exactly as published in discovery, Manifest, examples, or the Agent operation descriptor. Never construct `/p/<pack>/<operation_id>`: dotted selectors such as `chat.completions` intentionally map to slash paths such as `/p/<pack>/chat/completions`. The public contract must publish `operation_id_is_url=false`, and documentation checks must fail when `call_url != mount + path`.

Generated `request_example` values demonstrate shape only. They are never evidence that a model, resource ID, price, or entitlement is currently available. When the runtime publishes `dynamic_inputs`, Agents must resolve those fields first; for a model input, call the advertised `source_operation_key`/`source_call_url`, extract the current `data[].id`, and choose an exact returned ID. Do not copy a stale `request_example.model` after a live model catalog is available.

Add stable, lower-case `routes[].capabilities` to describe the exact public result, for example `web.search`, `web.scrape`, `ai.chat`, or `video.generate`. A shared semantic capability only makes independently addressable operations discoverable together; it never merges providers, pools, models, prices, schemas, readiness, or retry behavior. The stable identity is always `operation_key=<pack>.<operation_id>`. Capabilities are additive metadata: never rename an existing `operation_id` just to improve discovery.

## What a useful guide contains

1. Public LocalRouter path and authentication surface, never the private upstream URL.
2. Preconditions and readiness meaning.
3. Minimal request and response examples with fake values.
4. Required versus optional fields and defaults.
5. Streaming, polling, callback, timeout, cancellation, and retry behavior where applicable.
6. Quota/cost semantics and whether values are confirmed, estimated, remaining-only, or unknown.
7. Known errors and whether retry, reconciliation, user action, or pool repair is appropriate.
8. Capability limits, unsupported modes, and last verified scope.
9. Links to related operations/workflows through LocalRouter docs.

Prefer a small number of task-oriented guides over one generated field dump. Consumer Agents should learn how to call and interpret the service entirely through `/.well-known/localrouter.json`, Manifest, OpenAPI, examples, and guides.

For progressive disclosure, Agents may list the complete independent catalog or resolve a capability, send 2–50 exact candidates to `lr compare`, explicitly choose one `operation_key`, describe that operation, preflight the proposed input, then call it. Compare preserves caller order and every full independent contract; it returns no recommendation and never merges providers. LocalRouter must not silently select or collapse a provider. Cache descriptions only until discovery's contract digest or Agent schema version changes.

Guides must state every required path and query parameter separately from the JSON body. A required Query such as `project_id` belongs in `routes[].query_parameters`, in the generated OpenAPI/MCP schema, and in the example call; hiding it in prose is not enough. Explain `availability.level` and `covers` at the evidence layer actually observed. `contract` or `mock` never means a paid operation succeeded, `real-readonly` never means a mutating call succeeded, and a partial real call never proves a stream terminal or full workflow.

Long-running guides must name the workflow ID, terminal states, cancellation support, and the Pack/workflow/Job tuple needed to resume `lr watch` without an arbitrary client timeout.

## Prohibited content

Do not include credentials, cookies, pool entries, account IDs/emails, locator/source paths, private upstream addresses, internal CAPTCHA/OAuth steps, or real paid payloads. Do not mark a guide verified from parser success, a catalogue, balance, login, or mock alone.

After release, confirm that discovery, Manifest, generated OpenAPI, HTML, aggregate Markdown, examples, and every authored guide agree on operation IDs, paths, status, and workflow names.

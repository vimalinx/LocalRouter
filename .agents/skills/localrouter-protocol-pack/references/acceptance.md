# Acceptance matrix

Mark each row pass, fail, or not covered. Do not collapse the rows into one “works” claim.

| Layer | Evidence |
|---|---|
| Discovery | Port 8317 advertises the Pack, docs, auth surfaces, and maintenance lifecycle |
| Syntax | Strict JSON and guide front matter validation |
| Contract | Stable operation references, required path/query parameters, and workflow references |
| Draft impact | Every changed file, Pack section, operation, pool mode, and `pool_id` was reviewed before plan |
| Security | Loopback binding, path allowlist, protected credential modes, sanitized docs/state |
| Authorization surfaces | Console `/local/api` auth defaults off on loopback and can be enabled with a custom password; `/manage/mcp` still requires the separate administrator lane; Service Tokens are call-only; optional Agent maintenance defaults off and accepts only a maintenance-only `localrouter.maintain` Token |
| Transform | Mock proves exact upstream path, query, headers, request body, and public response |
| Authentication | Exact signature/token behavior, cache/refresh boundary, and no secret disclosure |
| Retry safety | No unsafe replay; idempotency key remains stable; unknown outcome is explicit |
| Pool | Distinct credential retry and expected selector behavior |
| Concurrency | Expiring leases and inter-process state locking recover after crash/contention |
| Affinity | Poll/resource request returns to the creation credential and fails over deliberately |
| REST | Real binary listener returns expected non-streaming response |
| Streaming | SSE/chunked response reaches terminal event without buffering |
| WebSocket/gRPC | Frame or HTTP/2 message/trailer passthrough reaches terminal exchange |
| Adapter | Envelope, sandbox/resource limits, and target ownership are proven |
| Workflow | Local Job persists and reaches a real provider terminal state |
| Workflow lifecycle | Branch/parallel/callback/cancel/restart scheduler paths are covered where declared |
| Quota telemetry | Missing/estimated/stale/unit semantics and registration/refresh behavior are correct |
| Cost | Quote or balance checked separately from task success |
| Maintainer | External pool writer is atomic/private; timer/service and read-only provider probe behave as expected |
| Documentation | Discovery, Manifest, OpenAPI, HTML, Markdown, and examples agree |
| Agent decision | Catalog and resolve filter Token policy, preserve every provider as an independent `operation_key`, expose ready and unavailable exact matches without implicit selection, compare returns 2–50 full contracts in caller order with no merge or recommendation, describe returns one sanitized contract, and preflight performs no upstream call |
| Verification fact | Every operation declares a precise level and covered layers; contract/mock/read-only evidence is never presented as a paid operation, stream terminal, or complete workflow |
| Agent recovery | Contract digest is stable across reads; long workflow watch can resume by Job ID and cancel preserves Token ownership |
| Compatibility | Existing IDs/paths/shapes remain compatible or a documented deprecation/migration exists |
| Runtime build | Go/WebUI/schema changes were built, restarted, and verified separately from Pack apply |
| Release | Candidate digest reviewed and exact digest applied |
| Recovery | Failed local post-apply verification restores the previous revision, preserves the draft, and reports the rollback result |
| Evidence | Pass/fail/not-covered result and final digest are reported without writing paused `.ai` automation records; strict validation passes |

For paid providers, “not covered” is correct until spending is authorized and a real minimal call completes.

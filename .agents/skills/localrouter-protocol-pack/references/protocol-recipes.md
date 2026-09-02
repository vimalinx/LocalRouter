# Protocol recipe chooser

Read this reference to choose the smallest runtime pattern that matches an observed service. These are decision patterns, not copy-paste provider contracts.

| Observed contract | Use | Key proof |
|---|---|---|
| JSON request/response HTTP | v2 for literal transforms; v3 for expressions/advanced auth | exact path, query, headers, request, response |
| Multipart upload or binary download | HTTP byte passthrough | bytes/content type preserved; no JSON transform |
| SSE or chunked HTTP | `streaming: true` | first byte is unbuffered and terminal event arrives |
| WebSocket | v3 `transport: websocket` | upgrade, subprotocol, binary/text frames, close |
| Raw gRPC | v3 `transport: grpc` | HTTP/2 message framing and trailers |
| Create then poll resource | route affinity; legacy or graph workflow | resource ID, creating credential, statuses, terminal result |
| Provider callback/webhook | v3 callback workflow | callback URL/token, restart persistence, terminal transition |
| Parallel/branch/cancel orchestration | v3 graph workflow | ordered transitions, fallback failure, compensation |
| TCP/UDP/Unix/provider binary protocol | fixed loopback `http-envelope` sidecar | bounded envelope and sidecar ownership |
| Deterministic format mapping with no I/O | `wasm-envelope` | ABI, no imports, time/memory/input/output limits |
| Several providers with the same wire contract | v3 local pool + `target_selector` | every credential maps to a fixed target; retry and affinity cross providers safely |

## HTTP and transforms

- Keep the public `path` stable; use `upstream_path` and optional fixed `target` for provider routing.
- When providers share the exact path, body, response, and auth contract, use `target_selector` to map protected credential metadata to fixed named targets. Otherwise keep separate operations or normalize with an adapter.
- Preserve client query parameters unless the contract explicitly replaces them.
- Literal `set` values are JSON values, not templates. Use v3 expressions only when observed data-dependent mapping is required.
- Expressions never receive credentials. Authentication is injected after transforms.
- Add explicit request/response JSON Schemas for Agent/MCP contracts. Example inference is conservative and does not prove provider behavior.
- Forward only intentionally allowed, non-hop-by-hop, non-authentication headers.

## Streaming and upgraded transports

- Do not apply response JSON transforms to SSE, chunked, WebSocket, raw gRPC, or opaque binary bodies.
- Pool failover is allowed only before downstream bytes/frames are committed.
- Preserve a partial stream and surface disconnect; do not replay a side-effecting stream.
- Test terminal behavior, not merely successful connection/upgrade.

## Async and affinity

For a create/poll API, observe and record:

1. create method/path and whether it is idempotent;
2. public response resource ID path;
3. poll path parameter;
4. pending/running/success/failure/cancel values;
5. result path and provider error shape;
6. cancellation and timeout semantics;
7. whether the poll must use the creating credential.

Use a Workflow only when these are known. Include an unmatched failure transition. A timeout produces a real `timed_out`/failed state; it is not success.

## Adapters

`http-envelope` targets must be fixed loopback endpoints. The sidecar owns network/protocol I/O and must return `outcome: complete|not_sent|sent|unknown`. `wasm-envelope` is capability-free and deterministic. Request data cannot choose target URLs, socket paths, module files, functions, or provider credentials.

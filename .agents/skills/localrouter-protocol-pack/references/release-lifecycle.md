# Validation, release, and recovery

Read this reference before planning, applying, restarting runtime code, or claiming completion.

## Decide the release lane

- **Pack-only:** top-level Pack JSON, authored guides, catalogs, and Pack WASM modules use the draft/digest lifecycle. No process restart is required.
- **Runtime:** Go handlers/runtime schemas require Go tests, build, controlled restart, listener verification, and then Pack lifecycle if managed files also changed.
- **LAN/container runtime:** additionally verify the operator listener remains loopback-only, the LAN listener omits all management routes, Service Tokens and Origin policy are enforced, container state persists across recreation, and no native/container process shares the same SQLite state concurrently.
- **WebUI:** source requires tests, typecheck, build into embedded assets, Go rebuild, controlled restart, and rendered browser acceptance.
- **External maintainer:** pool scripts/services are outside Pack rollback. Test their file format, permissions, atomic write, timers, and real read-only provider behavior separately.

## Before plan

1. Start from live discovery and the current guide.
2. Create an isolated draft.
3. Validate JSON, front matter, operation/workflow references, schemas, targets, source mappings, and modules.
4. Add focused mocks for transforms, auth, retries, selection, affinity, concurrency, streaming, adapters, or workflows.
5. Run the relevant local suite:

```bash
go -C gateway test ./...
go -C gateway vet ./...
go -C gateway build -trimpath -o localrouter .
./tests/protocol_e2e.sh
python .agents/skills/localrouter-protocol-pack/scripts/protocol_pack_doctor.py
```

6. Read `impact.files`, `impact.protocols`, and `impact.pool_ids`. For each protocol, inspect `sections`, operation additions/modifications/removals, and pool-mode change. Do not plan until the impact matches the request and the human view in `/#control`.

## Hash-bound apply

For repository authoring:

```bash
./tools/protocol-pack-lifecycle.sh validate
./tools/protocol-pack-lifecycle.sh plan
./tools/protocol-pack-lifecycle.sh apply <reviewed-64-character-digest>
```

For a port-only draft, use `localrouter_draft_review`, pass its exact digest to `localrouter_draft_plan`, then pass the plan digest to `localrouter_draft_apply`. The candidate and live base must still match the plan. Digest drift requires another review; never overwrite a newer live release.

Each new draft records the live digest it was seeded from. If another release changes live before review or plan, LocalRouter returns `stale_draft` and blocks the old full-tree snapshot; if the base marker is absent or invalid, it returns `draft_base_unknown`. Open a fresh draft from current live and reapply only the intended edits. After a successful apply, LocalRouter advances that draft's base marker to the installed digest so deliberate successive releases from the same draft remain safe.

Apply captures an immutable revision before atomically replacing live managed files, re-reads the installed tree, and verifies its digest. A local verification failure preserves the draft and automatically attempts the previous revision. Read the structured rollback result before any retry. Do not use the legacy reload endpoint for Agent-managed releases.

## After apply or runtime restart

1. Confirm `/local/status` and loopback listener.
2. Re-read discovery.
3. Check Pack Manifest, OpenAPI, HTML, Markdown, examples, and authored guides.
4. Run the doctor against the affected Pack.
5. Invoke each applicable layer: non-streaming, streaming terminal, WebSocket/gRPC terminal exchange, adapter, workflow terminal/cancel/restart, pool selection, and quota.
6. Use the smallest meaningful real provider call. Paid/quota-consuming calls require explicit spending authority.
7. Inspect sanitized management state without reading/printing secrets.
8. Report the final digest and pass/fail/not-covered matrix in the task result. Do not write `.ai` evidence while the owner pause is active.

## Recovery

Use history to find a known immutable digest:

```bash
./tools/protocol-pack-lifecycle.sh history
./tools/protocol-pack-lifecycle.sh rollback <known-digest>
```

Rollback restores Pack-managed source only. It does not revert external pool files, provider accounts, maintainer services, database side effects, or an already issued upstream task. After rollback, repeat discovery, docs, focused invocation, and the doctor.

Reset pool runtime state only after diagnosis:

```bash
./tools/protocol-pack-lifecycle.sh pool-reset <pack> [credential-id]
```

Reset clears runtime cooldown/failure/lease state; it does not make invalid credentials healthy.

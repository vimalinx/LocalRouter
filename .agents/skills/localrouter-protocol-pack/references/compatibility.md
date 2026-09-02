# Compatibility and migration

Read this reference when changing an existing public path, operation, schema, workflow, pool mapping, or Pack version.

## Compatibility policy

- Keep Pack ID, mount, public path, and `operation_id` stable for compatible changes.
- Prefer additive optional fields, new operations, new guides, and new capability metadata.
- A changed provider name does not justify renaming a stable public operation.
- If request/response meaning, authentication, side effects, or workflow terminal semantics change incompatibly, add a new operation/path and deprecate the old one.
- Keep the deprecated operation usable until its guide names the replacement, behavioral difference, and removal condition.
- Never silently reinterpret an existing field or change a safe operation into a side-effecting one.

## v2 and v3

Keep v2 when a stable Pack needs only literal HTTP transforms, basic pools/affinity, and legacy create/poll workflows. Move to v3 only when the Pack needs targets, expressions, advanced auth, readiness, retry policy, selectors/leases, WebSocket/gRPC, adapters, or graph workflows.

For v2-to-v3:

1. preserve IDs, paths, examples, and public shapes;
2. add explicit retry behavior equivalent to the old behavior;
3. verify authentication and pool selection again;
4. test docs/OpenAPI/MCP drift;
5. compare the draft impact and apply as one reviewed digest;
6. keep a known pre-migration revision for rollback.

## Pool schema evolution

- The external maintainer owns its record schema; LocalRouter owns only configured mapping paths.
- Add normalized fields before switching Pack mappings.
- Write the new source atomically, validate it with the draft, and verify sanitized account/quota aggregates.
- Keep old fields during a transition when the maintainer and Pack cannot switch atomically.
- Never use rollback to rewrite the external source. Pack rollback restores mappings, not provider accounts or maintainer state.
- Treat a unit change as incompatible quota telemetry; do not aggregate old and new units.

## Availability and evidence

Use operation availability `draft|observed|verified|blocked|deprecated`. Verified evidence may expire. A provider regression changes availability/readiness or the guide status; it does not require deleting the contract immediately.

Report migrations at three layers: Pack source/digest, running listener/docs, and external pool/provider state.

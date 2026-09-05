# Agent-led service workspace

LocalRouter gives Agents the same preparation, inspection and recovery operations
as the console. Humans approve authority changes; Agents perform the work.

## First release contract

- A service template is a versioned, credential-free recipe, maintenance guide
  and validation contract. Installing a template does not call its provider.
- An onboarding proposal belongs to its authenticated creator. Preparation is
  allowed with a service Token; it cannot install a Pack or grant permissions.
  The human approves the exact proposal digest, including target, operations,
  credential binding and any requested maintenance delegation.
- A capability bundle is a named immutable revision of explicit Pack operations
  and workflows. Whole-Pack selection expands at preparation time. Assignments
  pin revisions; later additions do not silently broaden existing authority.
  An assigned empty bundle grants no service operations. Existing model relay
  policy remains independent of service bundles.
- A maintenance delegation belongs to a separate maintenance-only Token. It is
  constrained to selected Packs. Compatible repairs preserve targets, auth,
  public and upstream paths, methods, operation IDs, transports and workflows.
  Changes outside that boundary become proposals requiring a new approval.
- A service trace records authenticated ownership, task correlation, the applied
  contract and grant revisions, attempts, asynchronous work, resource units and
  cost provenance. A network attempt is not a business task. Unknown outcomes
  and unknown costs remain unknown. Request and response bodies are not logged.

Preparation, approval, apply and verification are separate durable states.
Interrupted applies are reconciled against the installed digest, never blindly
replayed. Local validation is reported separately from provider execution.

The Agent consumer API is available on the authenticated consumer surface.
Approvals and maintenance execution remain loopback-only. No Agent endpoint
accepts a claimed owner identity in place of the authenticated Token.

## Delivery boundaries

Service restrictions cover LocalRouter routes; they do not sandbox external
network access. Agent task IDs are correlation labels, never authorization.
Credential provisioning, human OAuth consent, CAPTCHA, registration and payment
remain outside automatic request execution. Template repairs use the existing
reviewed-digest Pack release and rollback mechanism.

## Agent workflow

Use the installed `lr` discovery contract and a registered Service Token locator.
No administrator Token is needed for preparation. Begin with `lr init` and `lr guide` to check independent identity, then `lr status` and
`lr tree`, then run:

```sh
lr setup templates
lr setup template read-api 1
lr setup schema
lr setup prepare @proposal.json
lr setup get <proposal-id>
```

`prepare` accepts `kind: connection | bundle | template`, a reason and the relevant
object. For a connection, select an exact `template_id` and `template_version`,
then supply the adapted full Pack in `connection.definition`. Include the observed
documentation URL in `connection.source_url` and repair instructions in
`connection.guide`. Templates include read API, JSON API, multipart API and an
asynchronous submit/status workflow. These are fictional protocol recipes, not
claims of support for a particular provider.

To request the service and call permissions in one approval, include a bundle:

```json
{
  "kind": "connection",
  "reason": "Search documents for this task",
  "connection": {
    "template_id": "read-api",
    "template_version": "1",
    "definition": "REPLACE with the complete adapted template example object"
  },
  "bundle": {
    "id": "research-kit",
    "name": "Research tools",
    "members": [{"pack": "chosen-pack", "operations": ["search"]}]
  }
}
```

The placeholder above is deliberately not a runnable Pack. Read the current
machine schema and adapt the actual template. Omit `auth.secret_file`; LocalRouter
owns the binding. The human enters a new credential in the approval dialog.
The first version supports one target with none/bearer/header authentication.
Pools, adapters, target selection and advanced authentication retain the full
Protocol Pack authoring lifecycle.

A human opens `/#setup`, inspects the target, operations, bundle, maintenance
boundary and digest, then approves once. Agents cannot approve authority changes
through `/agent` or the consumer MCP. After approval, `lr setup get` reports the
installed state. Resolve dynamic inputs, inspect readiness and preflight before
an authorized real call. Capture that call once and inspect its saved response.

```sh
lr setup bundles
lr describe chosen-pack search
lr preflight chosen-pack search '{}' '{}' '{"q":"your authorized query"}'
# Execute only the separately authorized operation with its actual parameters.
lr setup verify <proposal-id>
lr setup traces
```

Verification reads recorded evidence; it never issues a hidden provider request.
`applying` after an interrupted process requires `lr setup reconcile <id>`.
Reconciliation checks the installed release digest and completes the durable
approved receipt only while authority and target eligibility remain unchanged.
It refuses to restore permissions after an intervening grant/revocation change.
An uncertain upstream request must be reconciled with provider state, not replayed.

## Bundle and maintenance semantics

A bundle-only proposal uses `kind: bundle` with `bundle` as above. `includes`
accepts exact existing bundle revision digests; their members are flattened into
the reviewed revision. Operations use semantic IDs, including dotted IDs.
`workflows` names workflow IDs and discloses their constituent operations.
`operations: ["*"]` expands to the current operation set during preparation.
Updating the same bundle ID replaces that bundle assignment for the target Token;
other assigned bundles remain. A Token with no bundle assignment retains its
existing policy; an explicitly empty assignment denies service calls. Bundles
intersect existing Token policies and do not constrain model-relay budgets.

Optionally request `maintainer_token_id` and `maintenance_mode: compatible` in a
connection proposal. This must be an existing separate maintenance-only identity.
The human must have enabled the maintenance lane. A delegated maintainer runs
preparation through that lane and applies only its own compatible proposal:

```sh
LOCALROUTER_SETUP_LANE=maintenance lr setup prepare @repair.json
lr setup apply <proposal-id> <exact-proposal-digest>
```

Use the documented maintenance Token file locator, never the administrator
credential. Delegated Tokens cannot access the older unrestricted maintenance
tools. Revocation leaves the identity scoped with no permissions; it does not
fall back to global maintenance. New targets, auth, routes, methods, operations,
transports or workflows require another human approval. Template publication is
also proposed and approved; a published template version is immutable. Publish a
new version for a revised recipe.

## Tracing and accounting

`LOCALROUTER_TASK_ID` and `LOCALROUTER_TRACE_ID` on `lr` add bounded correlation
headers. Trace IDs are 32 lowercase hexadecimal characters. The API also accepts
W3C-format `traceparent`. Correlation labels never change ownership. Authenticated
Agents can read only their own traces; the human console can inspect all records.

MCP and workflows create projection spans; actual calls retain the initiating
Token and create request spans plus transport attempt spans. Persisted workflow
jobs retain their trace/task parentage, and every internal call rechecks current
permissions. HTTP, gRPC, WebSocket handshakes and adapter invocations are traced
at LocalRouter's boundary. Adapter invocation evidence does not prove its private
provider call, and a WebSocket handshake does not account for individual frames.
Unknown interrupted calls and incomplete HTTP bodies remain distinguishable from
completed responses. HTTP success is not a claim of business completion.

Pack routes can publish `metering` with `resource_id_path`, `state_path` and numeric
`units` mappings (`unit`, JSON `path`, `source: request | response`, and
`mode: delta | snapshot`). Request parameters are separate from provider-reported
usage. Snapshot summaries keep the latest observation per Token, Pack, hashed
resource, source and unit; they do not sum repeated polls. Missing resource keys
are excluded from snapshot totals and counted separately. Cost totals remain
separated by reported/estimated/partial provenance. Missing prices stay unknown.
Trace list responses include a summary over all matching records, independent of
pagination. Model usage/cost uses the existing meter; no model budget mechanism
is introduced here.

Workspace proposals, bundles, templates and delegations are saved atomically in
`$XDG_DATA_HOME/localrouter/service-workspace.json`, mode 0600. The `service_traces`
table resides in the existing private SQLite database. Trace evidence is retained
until the operator archives it; there is no automatic deletion in this release.
Back up the database and workspace document together. Bodies and credential
values are excluded from these trace records; source URLs and Agent-written
reasons/guides must still be kept free of sensitive material.

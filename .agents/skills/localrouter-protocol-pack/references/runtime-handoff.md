# Provider and runtime handoff

Read this reference when a user asks to add a model, provider, or fixed local service to LocalRouter and then use it from OMP, Codex, Claude, or another runtime.

## Classify the change

- A documented Pack plus a new runtime choice is **consumer configuration**: discover the exact live model, then configure the runtime.
- A new provider, model relay, fixed local upstream, authentication mode, or request contract is a **Pack change**. It requires one authorized maintenance lane and an isolated semantic draft.
- Do not use a runtime `models.yml`, a database row, or a guessed `/p/<pack>` URL as a substitute for a published Pack.

## Required sequence

1. Record the user-approved scope: Pack ID, fixed upstream owner, intended runtime, and whether a smallest real upstream probe is authorized.
2. Probe the fixed upstream without exposing credentials. Confirm the actual models catalogue and each intended request surface, including the streaming terminal when it will be used. An HTTP listener, a login, or a guessed model name is not enough.
3. Open a semantic draft through the selected lane. For an OpenAI-compatible model relay, publish both a `models` operation with `ai.models`/`openai.models` and a `chat.completions` operation with `ai.chat`/`openai.chat`. Give `chat.completions.model` a dynamic input sourced from the published models operation.
4. Keep the upstream target, secret locator, credentials, and pool records out of Pack source, authored guides, command output, and runtime configuration. Declare only the operator-owned target name and safe request contract.
5. Lint, then review `impact.files`, `impact.protocols`, and `impact.pool_ids`. The intended Pack and operations must be the only change. Plan and apply the exact reviewed digest.
6. Re-read discovery and run `lr find model --exact <pack>:<requested-name>`. Require one exact live result and confirm its compatible operation. A zero-result exact query is blocking; do not treat fuzzy suggestions, a `request_example.model`, or a model name from an upstream configuration as availability evidence.
7. For an OpenAI-compatible runtime, run `lr runtime-openai <pack> <exact-model>` before editing configuration. It verifies the Pack has `GET /models` and `POST /chat/completions`, then emits its published `/p/<pack>/v1` compatibility Base URL. The runtime appends the bare operation path; keep an upstream `/v1` prefix in the target.
8. For a real smoke call, capture one raw `lr call` result and its exit status before parsing. Summarize the captured response with a null-safe expression such as `(.choices // [])[0]`; a parser failure is not a reason to call upstream again.
8. Configure the runtime from that emitted Base URL and exact `data[].id`. For OMP, preserve unrelated providers/models and use its existing mode-600 token-file command substitution or locator mechanism for the LocalRouter Token.
9. Start a fresh runtime session or use the runtime's documented reload boundary, then make one smallest authorized request. Verify the runtime reports the selected provider and exact model ID. Report Pack digest, runtime configuration path, and pass/fail/not-covered acceptance layers.

## Lane boundary

An ordinary Agent can use the semantic maintenance MCP only with the enabled maintenance-only Token. A user may explicitly delegate one named change to a local operator; that operator may use `lr manage-*` with the local administrator-backed lane, but the operation remains scoped to the request and no credential value may enter an Agent transcript, source file, guide, log, or command line. If neither condition holds, finish the upstream probe and draft design only; do not publish or configure the runtime.

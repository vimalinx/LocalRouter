<!-- LOCALROUTER:BEGIN managed-block global-consumer-contract version=1 -->
## LocalRouter global consumer contract

- The live port is the only usage contract: run `lr init` (require `ready=true`) and follow `lr guide` plus discovery. This block lists only the boundaries that hold regardless of version; when it seems to disagree with the live guide, the guide wins.
- Use `lr` against loopback port 8317; port 29317 is the same listener when discovery reports `listeners.loopback_alias.enabled=true`. The shortest LLM path is `/v1` with a Service Token and a model name; a request without `model` uses the LocalRouter default.
- Any existing Service Token can call; there is no identity review. Separate billing or limits: `lr identity request <agent-code> {}`. A program's own identity and `localrouter.toml`: `lr connect <program>`. When the trial runs out, ask once with `lr budget request`; never switch identities to bypass it.
- Treat `request_example` as shape-only and `operation_id` / `operation_key` as selectors, never URLs. Outside `/v1`, pick a model from one exact `lr find model --exact <pack>:<model-id>` result. Run `lr preflight` (or `lr exec`) before paid or side-effecting calls; a nonzero preflight blocks. Capture one real response, parse it offline, and never replay an unknown outcome because of a display or parse failure.
- Token locators are mode-600 file paths, never Token values: do not print, export or paste Token contents, and never read the administrator credential. Do not mutate `/local/api`, even when the console is password-free. Pack, auth, pool, workflow, guide or release changes use the `localrouter-protocol-pack` Skill and a distinct maintenance-only Token.
- For an operator-approved LAN endpoint, require `LOCALROUTER_ALLOW_LAN=true` and let `lr` verify `scope=lan-service` and unavailable maintenance before it sends a Token.
<!-- LOCALROUTER:END managed-block global-consumer-contract -->

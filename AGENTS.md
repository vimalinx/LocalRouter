# LocalRouter Agent instructions

When adding or changing a service, custom protocol, transform, credential pool, async workflow, Pack documentation, release, or rollback, load `.agents/skills/localrouter-protocol-pack/SKILL.md` first and follow its complete lifecycle.

The default discovery authority remains loopback port 8317. An operator-approved non-loopback endpoint is consumer-only: require `LOCALROUTER_ALLOW_LAN=true`, and verify discovery reports `scope=lan-service` plus `maintenance.available=false` before reading or sending a Service Token. Never use a LAN endpoint for `/local/api` or `/manage/mcp`.

Consumer Agents discover capabilities from `http://127.0.0.1:8317/.well-known/localrouter.json`; they must not depend on repository filesystem access. Start with `lr tree [pack]`: every callable service appears as either a lightweight compatibility Pack (`/v1`, `/v1beta`) or a full Protocol Pack (`/p/<pack>`), while `/w` and `/mcp` are Pack projections rather than separate service definitions. Classify what is being searched before narrowing it: use `lr find operation <intent>` for callable Protocol Pack operations, `lr find model <name>` for live upstream model discovery, and `lr find pool <provider-or-pack>` for readiness, quota and pricing. Final model selection must use `lr find model --exact <pack>:<model-id>` and return one exact live result. Model and catalog output is capped at 20 by default; refine first and use `--all` only when the complete machine catalog is required. `lr find <mixed intent>` returns all three domains separately; it never treats an OMP/runtime configuration as a LocalRouter operation. Use `lr catalog [pack]` or `lr catalog --all` for independently addressable Protocol Pack operations, or `lr resolve <capability>` for exact/fuzzy operation matches, then `lr compare <pack.operation>...` when multiple candidates remain. LocalRouter never merges providers or silently chooses one: compare every returned `operation_key`, Pack, readiness, verification level, pool, schema, pricing, retry and guide, choose explicitly, then use `lr describe` and `lr preflight` before a paid or side-effecting call. Treat a nonzero `lr preflight` exit as blocking and consume its structured `next_action` and `alternatives`. Reuse cached contracts only while both `contract.digest` and `contract.schema_version` are unchanged.

Treat `operation_key` and `operation_id` as semantic selectors, never as URL fragments. When an `lr` command already takes a Pack, its operation argument accepts either the bare `operation_id` or the published Pack-qualified `operation_key`, so an Agent may safely paste a catalog result. Direct HTTP calls must use the exact published `call_url`; prefer `lr call`, Agent describe/preflight, or MCP so dotted IDs such as `chat.completions` cannot be mistaken for slash paths.

Treat `request_example` as shape-only. Resolve every published `dynamic_inputs` field before calling; for `model` or `model_cls`, use `lr find model --exact <pack>:<model-id>`. Prefer the advertised model-catalogue operation and its declared extraction path; a Pack whose provider exposes no catalogue may instead publish a reviewed, explicit request-schema enum. Preflight does not refresh provider discovery, so require one exact result from the current contract rather than assuming the example value is available. A model search result is provider-qualified as `<pack>:<model-id>` and includes `compatible_operations`, derived from the Pack's declared dynamic-input relationship rather than a hard-coded API family. Select both explicitly, then configure OMP or another external Agent runtime separately if needed.

Use `lr run` for an operation or persistent workflow. For long workflows, retain the returned Pack, workflow and Job ID, then use `lr watch`; it has no default timeout and can be resumed with the same Job ID. Use `lr cancel` only when the workflow advertises cancellation. Treat structured `code`, `retryable`, `retry_after`, `next_action`, and `alternatives` as authoritative; never blindly replay an unknown side-effecting outcome.

For an authorized real or paid call, invoke `lr call` exactly once and capture its raw response plus exit status before running `jq` or another parser. Summarize that captured value offline. A parser, display, or pipe failure is not authority to repeat the upstream request.

Consumer API Tokens are long-lived and unlimited by default, but remain call-only. This never bypasses Pack pool concurrency, lease expiry, cooldown, health, or quota eligibility. Maintenance is separate and uses the administrator credential by default. Optional Agent maintenance is disabled by default; an Agent may call `/manage/mcp` only when discovery reports it enabled and the human supplied a distinct maintenance-only Token with `localrouter.maintain`. Agents must not request or read the administrator credential.

The human browser console and `/local/api` use password-free loopback access by default and may be protected with a custom password from Run Overview. This convenience does not authorize Agent mutation: Agents still use the explicit `/manage/mcp` maintenance lane and must not treat an open console as delegated authority.

Keep installed secrets below `$XDG_DATA_HOME/localrouter/` with mode `0600`; isolated tests may override `LOCAL_GATEWAY_DATA_DIR`. Never put credentials, cookies, pool contents, or private upstream addresses in source, guides, logs, test output, or `.ai` project-visible notes.

Preserve external ownership: external gateways retain their credential pools unless a Pack explicitly selects `pool.mode=local`. Registration, CAPTCHA, human OAuth consent, payment, and anti-bot challenges remain outside the request path.

For Agent changes, prefer the semantic tools exposed by `/manage/mcp`: open draft → patch Pack/write guide → review impact → plan reviewed digest → apply exact digest → live verification → history/rollback. LocalRouter owns paths, formatting, atomic writes, and digest calculation. A local post-apply verification failure preserves the draft and automatically attempts the previous revision; inspect the structured error and rollback result before retrying. The legacy reload endpoint exists for compatibility, not for Agent-managed releases.

Keep provider policy out of the Go core. Choose the smallest Pack form that preserves the observed contract: ordinary standard API routing uses a lightweight compatibility Pack backed by `$XDG_CONFIG_HOME/localrouter/channel-profiles.json` plus Channels (request-path ownership, auth placement, model catalogue parsing); isolated paths, transforms, special authentication, dedicated pools, workflows, or adapters use a full Protocol Pack. Providers needing login sessions or non-standard transports use an independently supervised fixed-loopback `http-envelope` adapter referenced by that Protocol Pack. Provider-specific behavior belongs in these data contracts, not Go switches.

Before planning, read each draft's `impact.files`, `impact.protocols`, and `impact.pool_ids`. Treat `pool_ids` as runtime pool exposure; `protocols[].sections` tells whether the pool configuration itself changed. Human reviewers use `/#control` against the same draft and digest, so never bypass or overwrite their draft edits.

<!-- VIMALINXOS:BEGIN managed-block project-agent-responsibility version=1 project=localrouter -->
## 子项目 Agent 责任边界

- 当前责任项目是注册项目 `localrouter`；项目身份、路径和负责人以父工作区 `governance/vimalinxos-projects.toml` 为准。
- 收到需求时先判断观察、修改、验证和最终结论是否属于本项目；只执行本项目负责的部分，不静默接管其他注册项目。
- 反思、复盘、自查或故障归因只针对当前 Agent 在本项目内的选择、改动、遗漏、证据和验证，不替其他项目、其他 Agent 或用户反思，也不推测或甩锅。
- 对其他项目或 Agent 只可陈述完成本项目任务所必需且已直接观察到的接口事实；未观察状态写 `unknown`，其诊断和修复交还对应负责人。
- 需求跨越项目边界时，完成可安全分离的本项目部分，然后向 VimalinxOS 根协调者交接外部项目、观察证据、待决定事项和应负责的 owner；子项目会话不得越界修改另一个注册项目。
- 最终答复只声明本项目内实际完成并验证的工作；AIOS 或根工作区的聚合健康不等于本项目任务完成，本项目测试也不等于外部系统已验证。

<!-- VIMALINXOS:END managed-block project-agent-responsibility -->

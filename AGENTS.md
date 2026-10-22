# LocalRouter Agent instructions

For Pack, auth, pool, workflow, adapter, guide, release or rollback changes, read `.agents/skills/localrouter-protocol-pack/SKILL.md`. Consumers use the live port contract through `lr init`, `lr guide`, and scoped discovery; they must not require repository access. Any existing protected mode-600 Service Token can call; there is no identity review. Independent identities (`lr identity request`, `lr connect <program>`) are issued at once on the trial budget and are needed only for separate billing, limits or setup proposals. Maintenance authority remains separate.

## Runtime and authority boundaries

- Loopback port 8317 is authoritative; loopback port 29317 is the same listener under an alias and carries identical authority. An operator-approved LAN endpoint is consumer-only: require `LOCALROUTER_ALLOW_LAN=true`, `scope=lan-service`, and `maintenance.available=false` before reading or sending a Service Token. No LAN maintenance.
- Agents do not self-register or mutate through `/local/api`, even when the human console is password-free. Maintain through the Agent lane: `lr manage-token` issues a distinct maintenance-only Token while the operator switch is on (the default); never request or read administrator credentials.
- Store secrets under `$XDG_DATA_HOME/localrouter/` with mode `0600`; isolated tests may set `LOCAL_GATEWAY_DATA_DIR`. Credentials, cookies, pool contents and private upstream addresses must not enter source, guides, logs, test output or `.ai` notes.
- `operation_key` / `operation_id` are semantic selectors. Use published call URLs or `lr call`; external OpenAI runtimes keep the Base URL returned by `lr runtime-openai` unchanged.
- Examples are shape-only. Resolve declared dynamic inputs; select one exact `lr find model --exact <pack>:<model-id>` result and a compatible operation. Use a current catalogue or a reviewed schema enum when no catalogue exists. Preflight does not refresh provider discovery.
- Paid, long-running or side-effecting calls require successful preflight. Capture an authorized upstream response and exit status once, then parse offline. Display failures never authorize replay; resolve unknown outcomes using structured status and retry guidance. Persistent workflows retain their Pack, workflow and Job ID; cancel only when advertised.
- Reuse contracts only while both `contract.digest` and `contract.schema_version` match. Service Tokens remain call-only and do not bypass pool concurrency, leases, cooldown, health or quota eligibility.

## Architecture and release invariants

- Keep provider policy out of the Go core. Standard API routing uses Channel Profiles and Channels; isolated paths, transformations, special authentication, dedicated pools and workflows use Protocol Packs. Non-standard transports use independently supervised fixed-loopback `http-envelope` adapters referenced by their Pack.
- External gateways retain credential-pool ownership unless `pool.mode=local` is explicit. Registration, CAPTCHA, human OAuth consent, payments and anti-bot challenges remain outside the request path.
- Use the semantic draft lifecycle. Review `impact.files`, `impact.protocols` and `impact.pool_ids` before planning; `pool_ids` describes runtime exposure, while `protocols[].sections` identifies configuration changes. Apply the reviewed exact digest, inspect verification and rollback results, and preserve concurrent human draft edits.
- The default model, each service's `preferred_model` and which Packs answer on `/v1` are operator choices in `$XDG_CONFIG_HOME/localrouter/models.json`, not Pack contract; change them through the console or that file, never by editing a Pack.
- LocalRouter owns paths, formatting, atomic writes and digest calculation. Follow the skill and live guide for concrete commands rather than duplicating their tutorials here.

<!-- VIMALINXOS:BEGIN managed-block project-agent-responsibility version=1 project=localrouter -->
## 子项目 Agent 责任边界

- 当前责任项目是注册项目 `localrouter`；项目身份、路径和负责人以父工作区 `governance/vimalinxos-projects.toml` 为准。
- 收到需求时先判断观察、修改、验证和最终结论是否属于本项目；只执行本项目负责的部分，不静默接管其他注册项目。
- 反思、复盘、自查或故障归因只针对当前 Agent 在本项目内的选择、改动、遗漏、证据和验证，不替其他项目、其他 Agent 或用户反思，也不推测或甩锅。
- 对其他项目或 Agent 只可陈述完成本项目任务所必需且已直接观察到的接口事实；未观察状态写 `unknown`，其诊断和修复交还对应负责人。
- 需求跨越项目边界时，完成可安全分离的本项目部分，然后向 VimalinxOS 根协调者交接外部项目、观察证据、待决定事项和应负责的 owner；子项目会话不得越界修改另一个注册项目。
- 最终答复只声明本项目内实际完成并验证的工作；AIOS 或根工作区的聚合健康不等于本项目任务完成，本项目测试也不等于外部系统已验证。

<!-- VIMALINXOS:END managed-block project-agent-responsibility -->

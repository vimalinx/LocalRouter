# LocalRouter Agent 使用

日常从 `lr init` 开始；选好服务后，用 `lr exec` 完成检查和一次调用。LocalRouter 不替你选择供应商。

## 日常调用

```sh
lr init
lr status
lr tree
lr resolve <能力>          # 例如 web.search，按能力标签列出候选
lr find operation <需求>
lr describe <pack> <operation>
lr exec <pack> <operation> @request.json
```

- `lr init` 是只读检查。有效的现有 Service Token（包括默认 Token）即可调用，没有身份审查。需要单独计费或限额时，`lr identity request <agent-code> {}` 立即签发并绑定独立身份，从试用额度开始。会话绑定的身份继续核对工作区。要求 `ready=true`；Token、预算、能力包和账号池限制始终有效。
- 首次选择服务时，查看 `lr status`、`lr tree` 和操作契约中的输入、费用、验证范围与重试规则。已明确服务时不必重复浏览全目录。`ready=true` 表示就绪，不证明真实供应商调用成功；缺少价格不表示免费。
- `lr exec` 适用于完整 Protocol Pack 操作：自动核对身份、当前契约和精确模型，预检通过后发送一次正式请求，并私下保存响应、退出状态和追踪 ID。它会自动完成上次中断的身份交付。授权范围内无需逐次询问；准备和预检不授予新权限。
- JSON 可直接传对象，也可传 `@文件`。需要路径和查询参数时，使用 `lr exec <pack> <operation> <body-json> <path-json> <query-json>`。三份 JSON 默认 `{}`；GET 的 body 必须为 `{}`。路径参数值必须是字符串，查询参数可以是字符串、数字或布尔值。
- 三个调用命令：`lr exec` = 预检 + 一次调用，日常都用它；`lr call` 只发送，必须先自己 `lr preflight` 通过；`lr run` / `lr watch` / `lr cancel` 只用于 `lr tree` 和发现文档列出的工作流（`wf`）。没有工作流的异步服务，用它的创建操作和状态操作，先保存返回的资源 ID 再轮询。
- `side_effecting` 按 HTTP 方法保守判定：除 GET/HEAD 外都算有副作用，即使是 POST 形式的搜索或状态查询；它决定预检提醒保留资源 ID，不代表一定会改变上游状态。
- `ready=true` 表示配置和账号池可用，不检查供应商余额；遇到余额不足（如 `insufficient_user_quota`）换一个服务，不要重试同一个。
- `operation_id` / `operation_key` 是语义标识，不是 URL。可传裸操作 ID 或 Pack 限定的 operation_key；直接 HTTP 只使用契约的 `call_url`。

## 模型与其他调用入口

最短路径是 `/v1` + Service Token + 模型名。Token 文件取 `lr init` 返回的 `token_file`：

```sh
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer $(cat <token_file>)" -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"你好"}]}'
```

`/v1` 不需要 `lr preflight`：权限、预算和额度在发往上游前检查，被拦下时返回结构化错误且 `upstream_called=false`。`POST /v1/chat/completions` 等标准接口按模型名选路，`GET /v1/models` 列出可用名称。请求不带 `model` 时使用 LocalRouter 默认模型（发现文档 `models.default`，响应头 `X-LocalRouter-Default-Model`）。没有 Channel 支持的模型会交给操作者放进 `/v1` 的 Pack（`models.v1_pack_models`），响应头 `X-LocalRouter-Pack` 标出实际服务；未发出或被 429 拒绝时按顺序换下一个 Pack，已发出后的失败不换。每个服务可公布 `preferred_model`（`lr tree` 显示），未指定模型时优先用它。模型名不存在时返回 404 `model_not_found`。`/v1/embeddings` 等接口只接受 `GET /v1/models` 里有、且对应渠道支持该接口的模型；完整 Pack 的模型目录里出现嵌入模型，不代表它公布了嵌入操作，以 `compatible_operations` 为准。

模型从当前目录选择；`request_example` 仅展示形状。`lr exec` 自动对 `model`、`model_cls` 等动态输入做精确检查。需要自行准备外部客户端时，运行 `lr find model --exact <pack>:<model-id>`，要求一个匹配且操作兼容；上游没有目录时只接受当前已审阅 schema 的显式枚举。

`lr find model` 由网关执行（`GET /agent/models`），结果标出 `preferred`（该服务首选）和 `default`（默认模型），默认最多 20 项。模糊搜索会读取所有就绪目录，把 Pack 名作为搜索词不会限制上游范围；只查一个 Pack 应用限定 Pack 的 `--exact`。查询失败时 `success=false`、`complete=false` 且非零退出，不等于模型不存在；按 `failures[].code/reason/http_status/next_action` 处理。需要完整目录时才用 `--all`，保存一次后离线筛选。

标准兼容渠道使用它公布的入口和 `lr request`。外部 OpenAI 客户端使用 `lr runtime-openai <pack> [exact-model]` 返回的原样 Base URL，保留 `/p/<pack>/v1`；省略模型时用该服务的 `preferred_model`。需要手动分步时仍可用 `lr preflight` 和 `lr call`，参数顺序与 `lr exec` 相同；真实、付费或有副作用的调用须在已批准范围内，且预检非零退出必须停止。预检不会刷新模型目录。

只在 `contract.digest` 和 `contract.schema_version` 都一致时复用契约。`lr tree` 是文本树；JSON 中 `lr status` 的服务数组是 `protocols`、`lr catalog` 的操作数组是 `operations`、`lr find model` 的结果数组是 `matches`。

## 失败与恢复

```sh
lr result
lr result <call-id>
lr result <call-id> --refresh
```

`--show` 打印保存的原始响应，`--refresh` 只读取同一调用的网关证据，都不重发供应商请求。`GET /agent/traces?trace_id=<id>` 列出一次请求的全部上游尝试（包括 `/v1` 换 Pack 前的尝试）。`response_received` 只表示收到响应，必须核对实际业务结果；流式回复不完整、只有推理片段或输出截断均不能算完成。

失败先读 `blocked_at`、`code`、`reason`、`retryable`、`next_action` 和 `resolution`。`upstream_called=false` 表示预检没有调用供应商。响应或结果为 unknown 时，检查保存的内容和已公布的状态查询操作；解析、显示或网络失败都不能成为重放已提交操作的理由。

批量调用也须逐项立即保存开始记录、原始响应和退出码，不能等整批结束才留证据。超时保留已有输出并标记未知。只有明确未发出的请求可按结构化提示恢复；共享额度对未知结果保留占用。

持久工作流用 `lr run` 启动；CLI 在发送前记录意图，并私下保存一次响应与退出状态，可用 `lr result` 找回。保存 Pack、workflow 和 LocalRouter Job ID，再用 `lr watch` 推进同一个任务；`watch` 默认无超时，但 `outcome_unknown` 会立即非零退出，要求核对上游，不能重新创建任务。契约 14 起，GET `/w/<pack>/<workflow>/<job>` 只读取已保存状态；POST 同一路径的 `/advance` 才允许推进一次，可能调用上游。供应商 resourceId/taskId 不是 LocalRouter Job ID，没有工作流时用该服务的状态操作。只对声明可取消的工作流执行 `lr cancel`，并核对实际终态。

## 需要新增身份或权限时

| 需要 | 路径 |
|---|---|
| 给一个程序接入 LocalRouter | `lr connect <program>`：立即签发程序身份，Token 存到 0600 文件，并写好 `localrouter.toml` |
| 独立身份 | `lr identity request <agent-code> {}`（全部服务）或 `@policy.json`（限定范围）：立即签发、领取并绑定，从试用额度开始 |
| 新服务尚未发布，只需要准备接入方案 | `lr identity prepare <agent-code>`：立即签发；该身份不能调用任何服务 |
| 试用或正式预算用完 | `lr budget request formal --monthly <usd> [--daily <usd>] --reason <理由> --wait`，或 `lr budget request trial --reason <理由> --wait`；人批准一次 |
| 已有人签发独立 Token | `lr identity bind <agent-code> <token-file>`，核对编码、工作区与仅调用权限 |
| 调整现有服务范围或限额 | `lr identity access @complete-policy.json`：立即生效，保留 Token、用量和预算 |
| 接入新服务或能力包 | 独立身份运行 `lr setup templates` → `lr setup template <id> <version>` → `lr setup schema` → `lr setup prepare @proposal.json` → 人审阅精确方案并批准 |

策略 `{}` 表示全部服务；限定范围时，操作须属于所选 Pack，并使用当前公开契约中的 Pack 限定操作；申请中的金额字段会被忽略，金额只能通过 `lr budget request` 由人决定；仅准备身份使用 `{"setup_only":true}`，不填写这些选择器。`lr identity schema` 公布完整字段和类型；按需限制模型、每日请求、频率、并发和截止时间。请求不读取默认/管理员 Token，领取用私有证明，凭据只保存到 0600 文件。相同会话的相同申请复用原身份；领取窗口为 24 小时。签发失败时先 `lr identity cancel` 再换范围；已签发身份在控制台撤销，撤销后不能用旧证明恢复，用量也不清零。

身份绑定按 Agent 会话、工作区和服务地址隔离。Codex 使用会话 ID，其他宿主设置稳定独立的 `LOCALROUTER_AGENT_SESSION`。显式 `LOCALROUTER_API_TOKEN_FILE` 优先；需要自动绑定时先去掉该覆盖。`lr identity forget` 只忘记定位器。身份未准备好时仍可阅读 `lr guide/tree/docs`。

身份申请会展开并展示模型目录、工作流内部操作和恢复入口等必要依赖。标准兼容接口使用发现文档的精确 `operation_key`（例如 `openai-compatible.post.v1.chat.completions`）；实际请求仍使用公布的原始 URL。

调整范围提交的是完整替换策略，立即生效，保留 Token、累计用量和预算。能力包固定在批准版本，新增操作不自动获得授权，显式空授权禁止调用。模型限制不等于任意业务资源的访问控制。

新服务提案即使普通模式也要求独立身份；准备不安装、不调用上游、不授予权限。`connection` 可附带明确的 `bundle`，由人一次批准目标和完整生效策略，保留原金额、模型、次数和有效期限制；新增连接时还会展示并保留此前可用的服务能力。审批后发生策略漂移会拒绝覆盖。密钥用 `lr setup prepare @proposal.json --credential-file <0600 文件>` 提供，只保存为凭据绑定，不回显，方案被拒或撤回时删除；不要把密钥写进定义、命令参数或输出，也不指定 secret_file。

只新增公网 HTTPS 服务、授权仅给自己、带有价格条目（官方价格、`source_url`、`checked_at`）且已提供密钥的方案会立即生效（`auto_apply.applied=true`），之后在预算内预检并调用；修正自己自动接入、且没有别的 Agent 在用的服务时，提交不带 `bundle` 的连接方案即可自动生效，沿用原密钥。接错或不再需要时，用 `lr setup undo <id>` 撤销自己接入的服务：删除这次新增的服务、收回授权并删除你提供的密钥，之后可以重新提交方案。别的 Agent 也在用的服务或对已有服务的修改由人处理。人也可以随时撤销；人撤销的（`undone_by` 不是 `agent:`）不要自行重新接入。生效后看 `verification`：`live-ok` 表示上游已连通；`live-failed:<状态>` 表示连不通，修正地址、认证或密钥后用 `lr setup check <id>` 重查，不要靠调用业务操作来试。不满足条件时 `auto_apply.reason` 说明原因，方案等人批准。价格请查服务商官方价格页后填写。用 `lr setup get/bundles/verify` 核对状态和已有证据；中断在 `applying` 时用 `lr setup reconcile`，不重放 apply。`LOCALROUTER_TASK_ID` 仅用于关联追踪，不改变权限。

## 给程序接入

```sh
lr connect <program>
```

程序身份立即签发，默认可调用全部服务并从试用额度开始。Token 存在 `$XDG_DATA_HOME/localrouter/programs/<program>/service-token`（0600），配置写到 `$XDG_CONFIG_HOME/<program>/localrouter.toml`，只保存 `base_urls`、`token_file`、`agent_code` 和默认模型，不保存 Token 值。程序读取 `token_file`，以 `Authorization: Bearer` 调用。重复运行复用仍有效的 Token；身份被撤销后会重新签发。`--policy @policy.json` 限定范围，`--config <path>` 改配置位置，`--no-config` 只签发 Token。以程序身份查看或申请预算时，在命令前加 `LOCALROUTER_API_TOKEN_FILE=<token_file>`，例如 `LOCALROUTER_API_TOKEN_FILE=<token_file> lr budget`。

## 新服务的可提交形状与旧提案恢复

`lr setup schema` 是完整提案结构，`lr setup schema pack` 是 Pack v3 源文件结构；均从当前端口提供，不需要仓库路径。模板的 `example` 可作为 `connection.definition` 的起点；保留顶层 `enabled:true`。发现文档中的 route `enabled`、`operation_key`、`call_url` 是运行时投影，不要复制进定义。

无认证使用 `"auth":{"type":"none"}`；Bearer 使用 `"auth":{"type":"bearer"}`；自定义请求头使用 `"auth":{"type":"header","header":"X-API-Key"}`。后两种都不填写密钥或 `secret_file`；LocalRouter 在准备阶段分配凭据位置，由人在审批时填写密钥。准备不探测上游。

遇到 `identity_scope_unpublished`，按错误的 `next_action` 先取消残留申请，再走 `lr identity prepare`；不要借无关服务充当载体。已有独立身份可直接准备新服务。

旧提案缺少完整权限快照、权限发生变化时，提案所有者用 `lr setup reprepare <id> <digest>` 生成替代方案；旧连接为禁用状态且本次意图是接入启用服务时，明确加 `--enable`。控制台提供同样的重新准备入口。旧记录保留并指向新提案；重试会返回同一替代方案，不继承旧批准、不安装、不调用。人必须审阅新的摘要、完整权限和启用状态，再批准。正在 `applying` 的提案须先 reconcile，不能以重新准备绕过未知结果。

## 试用与正式预算

新签发的独立身份从试用档开始：每个 Agent 一次性试用额度（默认 $2），所有试用 Agent 每个 UTC 自然月合计不超过试用总额（默认 $5），金额都由人在控制台设置。`lr budget` 查看当前档位和剩余金额。

- 价格不固定的调用先按单次预留（默认 $0.02）占用，调用后按上游报告的真实费用结算；最后一笔可能略超试用额度。剩余额度不足一次预留时就会被拦下，不必等到 0。上游返回 429 的尝试释放预留；其他已发出但费用未知的尝试保留预留，直到人核对。
- 用完时调用在发往上游之前被拦下，返回 `agent_trial_exhausted` 或 `agent_trial_pool_exhausted`。不要换身份或改用默认 Token 绕过。
- 转为长期使用时，先按供应商公布的价格和预计用量估算金额，再运行 `lr budget request formal --monthly <usd> [--daily <usd>] --reason <用途与估算依据>`。只需要再试一下时用 `lr budget request trial --reason <理由>`，金额由人决定。
- 加 `--wait` 会先在 stderr 打印申请 ID，再最多等 15 分钟，期间只查询状态，不重复提交；只有获批才返回 0，超时仍待处理或被拒绝都非零退出，之后用 `lr budget status <id>` 查看。人可在批准时调低金额；`denied` 表示拒绝，不要原样重提。
- 正式预算按 UTC 日/月重置，只统计转正之后的正式消费。扩大服务范围（`lr identity access`）不会改变金额。

## 自主额度与维护

自主额度检查总开关默认关闭，它不要求独立身份。只有总开关和服务规则都启用时，才检查该服务共享额度；关闭时保留规则与用量，重新开启不清零。额度按服务共享，操作子额度和总额度同时约束。额度内调用仍须满足 Token/能力包权限并通过预检。

明确公布的 GET/HEAD 模型目录读取不扣业务请求次数；读取已保存任务、推进既有任务的只读状态或取消步骤使用独立的每 Token 每分钟 120 次控制额度。模型目录不扣自主额度；任务推进和取消仍受金额预算、共享额度、账号池和操作权限约束，不能借此创建新任务。Token、能力包、有效期和显式禁止/批准规则始终有效。美元自动额度只适用于已确认的固定调用价格；费用不能确定时改用次数或单次批准。`service_approval_required` 交人批准一次或调整额度，`service_use_denied` 表示禁止，单次批准不能覆盖。批准绑定 Token、服务和操作，一小时内一次有效；`X-LocalRouter-Allowance-Receipt` 是占用记录，未知结果先核对，不能重试探测。

维护独立授权：Agent 维护默认开启（人可在控制台 Token 页关闭）。在回环端口运行 `lr manage-token` 立即领取独立的仅维护 Token（0600 文件），之后 `lr manage-*` 自动使用它经 `/manage/mcp` 维护；其他位置用 `LOCALROUTER_MAINTAINER_TOKEN_FILE` 指定。维护 Token 不能调用服务，Service Token 无维护权限，免密控制台也不构成 Agent 授权；不读管理员凭据，不通过 `/local/api` 自行批准。已委托的兼容修复用维护 lane 准备提案，审阅后 `lr setup apply <id> <proposal.digest>`；扩大目标、认证或操作需新批准。高级 Pack 编辑仍走 draft → review → plan → exact-digest apply，保留他人的草稿修改。

LAN 地址须经操作者批准，设置 `LOCALROUTER_ALLOW_LAN=true` 后，`lr` 在读 Token 前核对 `scope=lan-service` 和 `maintenance.available=false`。LAN 只提供调用，身份领取和维护留在 loopback。Token locator 是私有文件路径，不是密钥值；不要把 Token 放进聊天、源码或日志。

完整命令参考：`lr help all`。所选服务的操作说明：`lr docs <pack>`。

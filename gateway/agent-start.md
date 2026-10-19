# LocalRouter Agent 使用

日常从 `lr init` 开始；选好服务后，用 `lr exec` 完成检查和一次调用。LocalRouter 不替你选择供应商。

## 日常调用

```sh
lr init
lr status
lr tree
lr find operation <需求>
lr describe <pack> <operation>
lr exec <pack> <operation> @request.json
```

- `lr init` 是只读检查。普通模式 `identity_required=false` 时，有效的现有 Service Token（包括默认 Token）即可调用，无需额外登记。需要单独计费时，`auto_enrollment=true` 表示 `lr identity request` 会立即签发并绑定独立身份，从试用额度开始。严格模式才要求独立 Agent；已有身份的工作区绑定在两种模式下都继续检查。要求 `ready=true`；Token、能力包和账号池限制始终有效。
- 首次选择服务时，查看 `lr status`、`lr tree` 和操作契约中的输入、费用、验证范围与重试规则。已明确服务时不必重复浏览全目录。`ready=true` 表示就绪，不证明真实供应商调用成功；缺少价格不表示免费。
- `lr exec` 适用于完整 Protocol Pack 操作：自动核对身份、当前契约和精确模型，预检通过后发送一次正式请求，并私下保存响应、退出状态和追踪 ID。它会自动领取已批准的待接入身份。已获人批准的范围内无需逐次询问；准备和预检不授予新权限。
- JSON 可直接传对象，也可传 `@文件`。需要路径和查询参数时，使用 `lr exec <pack> <operation> <body-json> <path-json> <query-json>`。三份 JSON 默认 `{}`；GET 的 body 必须为 `{}`。路径参数值必须是字符串，查询参数可以是字符串、数字或布尔值。
- `operation_id` / `operation_key` 是语义标识，不是 URL。可传裸操作 ID 或 Pack 限定的 operation_key；直接 HTTP 只使用契约的 `call_url`。

## 模型与其他调用入口

模型从当前目录选择；`request_example` 仅展示形状。`lr exec` 自动对 `model`、`model_cls` 等动态输入做精确检查。需要自行准备外部客户端时，运行 `lr find model --exact <pack>:<model-id>`，要求一个匹配且操作兼容；上游没有目录时只接受当前已审阅 schema 的显式枚举。

模型搜索默认最多 20 项。模糊搜索会读取所有就绪目录，把 Pack 名作为搜索词不会限制上游范围；只查一个 Pack 应用限定 Pack 的 `--exact`。查询失败时 `success=false`、`complete=false` 且非零退出，不等于模型不存在；按 `failures[].code/reason/http_status/next_action` 处理。需要完整目录时才用 `--all`，保存一次后离线筛选。

标准兼容渠道使用它公布的入口和 `lr request`。外部 OpenAI 客户端使用 `lr runtime-openai <pack> <exact-model>` 返回的原样 Base URL，保留 `/p/<pack>/v1`。需要手动分步时仍可用 `lr preflight` 和 `lr call`，参数顺序与 `lr exec` 相同；真实、付费或有副作用的调用须在已批准范围内，且预检非零退出必须停止。预检不会刷新模型目录。

只在 `contract.digest` 和 `contract.schema_version` 都一致时复用契约。`lr tree` 是文本树；JSON 中 `lr status` 的服务数组是 `protocols`、`lr catalog` 的操作数组是 `operations`、`lr find model` 的结果数组是 `matches`。

## 失败与恢复

```sh
lr result
lr result <call-id>
lr result <call-id> --refresh
```

`--refresh` 只读取同一调用的网关证据，不重发供应商请求。`response_received` 只表示收到响应，必须核对实际业务结果；流式回复不完整、只有推理片段或输出截断均不能算完成。

失败先读 `blocked_at`、`code`、`reason`、`retryable`、`next_action` 和 `resolution`。`upstream_called=false` 表示预检没有调用供应商。响应或结果为 unknown 时，检查保存的内容和已公布的状态查询操作；解析、显示或网络失败都不能成为重放已提交操作的理由。

批量调用也须逐项立即保存开始记录、原始响应和退出码，不能等整批结束才留证据。超时保留已有输出并标记未知。只有明确未发出的请求可按结构化提示恢复；共享额度对未知结果保留占用。

持久工作流用 `lr run` 启动；CLI 在发送前记录意图，并私下保存一次响应与退出状态，可用 `lr result` 找回。保存 Pack、workflow 和 LocalRouter Job ID，再用 `lr watch` 推进同一个任务；`watch` 默认无超时，但 `outcome_unknown` 会立即非零退出，要求核对上游，不能重新创建任务。契约 14 起，GET `/w/<pack>/<workflow>/<job>` 只读取已保存状态；POST 同一路径的 `/advance` 才允许推进一次，可能调用上游。供应商 resourceId/taskId 不是 LocalRouter Job ID，没有工作流时用该服务的状态操作。只对声明可取消的工作流执行 `lr cancel`，并核对实际终态。

## 需要新增身份或权限时

| 需要 | 路径 |
|---|---|
| 独立身份（普通模式） | `lr identity request <agent-code> @policy.json`：立即签发、领取并绑定，从试用额度开始，无需等待 |
| 严格模式要求独立身份 | 同一命令；申请等人在 registration_url（`/#tokens`）批准，再运行 `lr identity claim` |
| 新服务尚未发布，只需要准备接入方案 | `lr identity prepare <agent-code>`；普通模式立即生效，严格模式等人批准后 `lr identity claim`；该身份不能调用任何服务 |
| 试用或正式预算用完 | `lr budget request formal --monthly <usd> [--daily <usd>] --reason <理由> --wait`，或 `lr budget request trial --reason <理由> --wait`；人批准一次 |
| 已有人签发独立 Token | `lr identity bind <agent-code> <token-file>`，核对编码、工作区与仅调用权限 |
| 扩大现有服务范围或限额 | `lr identity access @complete-policy.json` → 人批准；`lr identity access-status <id>` 看进度 |
| 接入新服务或能力包 | 独立身份运行 `lr setup templates` → `lr setup template <id> <version>` → `lr setup schema` → `lr setup prepare @proposal.json` → 人审阅精确方案并批准 |

调用权限申请须明确 Pack 和 Pack 限定的操作，来自当前公开契约，不使用通配范围；申请中的金额字段会被忽略，金额只能通过 `lr budget request` 由人决定；仅准备身份使用 `{"setup_only":true}`，不填写这些选择器。`lr identity schema` 公布完整字段和类型；按需限制模型、每日请求、频率、并发和截止时间。请求不读取默认/管理员 Token，领取用私有证明，凭据只保存到 0600 文件。相同会话的相同申请复用原身份；申请及领取窗口为 24 小时。修改待接入范围先 `lr identity cancel`，已批准身份在控制台撤销。批准不能恢复已撤销身份，也不清零用量。

身份绑定按 Agent 会话、工作区和服务地址隔离。Codex 使用会话 ID，其他宿主设置稳定独立的 `LOCALROUTER_AGENT_SESSION`。显式 `LOCALROUTER_API_TOKEN_FILE` 优先；需要自动绑定时先去掉该覆盖。`lr identity forget` 只忘记定位器。身份未准备好时仍可阅读 `lr guide/tree/docs`。

身份申请会展开并展示模型目录、工作流内部操作和恢复入口等必要依赖。标准兼容接口使用发现文档的精确 `operation_key`（例如 `openai-compatible.post.v1.chat.completions`）；实际请求仍使用公布的原始 URL。

扩权提交的是完整替换策略，批准前保持原权限，批准后保留 Token 和累计用量；策略已被他人修改时须重新准备。能力包固定在批准版本，新增操作不自动获得授权，显式空授权禁止调用。模型限制不等于任意业务资源的访问控制。

新服务提案即使普通模式也要求独立身份；准备不安装、不调用上游、不授予权限。`connection` 可附带明确的 `bundle`，由人一次批准目标和完整生效策略，保留原金额、模型、次数和有效期限制；新增连接时还会展示并保留此前可用的服务能力。审批后发生策略漂移会拒绝覆盖；密钥由人单独填写，Agent 不指定 secret_file。用 `lr setup get/bundles/verify` 核对状态和已有证据；中断在 `applying` 时用 `lr setup reconcile`，不重放 apply。`LOCALROUTER_TASK_ID` 仅用于关联追踪，不改变权限。

## 新服务的可提交形状与旧提案恢复

`lr setup schema` 是完整提案结构，`lr setup schema pack` 是 Pack v3 源文件结构；均从当前端口提供，不需要仓库路径。模板的 `example` 可作为 `connection.definition` 的起点；保留顶层 `enabled:true`。发现文档中的 route `enabled`、`operation_key`、`call_url` 是运行时投影，不要复制进定义。

无认证使用 `"auth":{"type":"none"}`；Bearer 使用 `"auth":{"type":"bearer"}`；自定义请求头使用 `"auth":{"type":"header","header":"X-API-Key"}`。后两种都不填写密钥或 `secret_file`；LocalRouter 在准备阶段分配凭据位置，由人在审批时填写密钥。准备不探测上游。

遇到 `identity_scope_unpublished`，按错误的 `next_action` 先取消残留申请，再走 `lr identity prepare`；不要借无关服务充当载体。已有独立身份可直接准备新服务。

旧提案缺少完整权限快照、权限发生变化时，提案所有者用 `lr setup reprepare <id> <digest>` 生成替代方案；旧连接为禁用状态且本次意图是接入启用服务时，明确加 `--enable`。控制台提供同样的重新准备入口。旧记录保留并指向新提案；重试会返回同一替代方案，不继承旧批准、不安装、不调用。人必须审阅新的摘要、完整权限和启用状态，再批准。正在 `applying` 的提案须先 reconcile，不能以重新准备绕过未知结果。

## 试用与正式预算

新签发的独立身份从试用档开始：每个 Agent 一次性试用额度（默认 $2），所有试用 Agent 每个 UTC 自然月合计不超过试用总额（默认 $5），金额都由人在控制台设置。`lr budget` 查看当前档位和剩余金额。

- 价格不固定的调用先按单次预留（默认 $0.02）占用，调用后按上游报告的真实费用结算；最后一笔可能略超试用额度。
- 用完时调用在发往上游之前被拦下，返回 `agent_trial_exhausted` 或 `agent_trial_pool_exhausted`。不要换身份或改用默认 Token 绕过。
- 转为长期使用时，先按供应商公布的价格和预计用量估算金额，再运行 `lr budget request formal --monthly <usd> [--daily <usd>] --reason <用途与估算依据>`。只需要再试一下时用 `lr budget request trial --reason <理由>`，金额由人决定。
- 加 `--wait` 会最多等 15 分钟，期间只查询状态，不重复提交；只有获批才返回 0，超时仍待处理或被拒绝都非零退出，之后用 `lr budget status <id>` 查看。人可在批准时调低金额；`denied` 表示拒绝，不要原样重提。
- 正式预算按 UTC 日/月重置，只统计转正之后的正式消费。扩大服务范围（`lr identity access`）不会改变金额。

## 自主额度与维护

严格模式总开关默认关闭。只有总开关和服务规则都启用时，才检查该服务共享额度；关闭时保留规则与用量，重新开启不清零。额度按服务共享，操作子额度和总额度同时约束。额度内调用仍须满足 Token/能力包权限并通过预检。

明确公布的 GET/HEAD 模型目录读取不扣业务请求次数；读取已保存任务、推进既有任务的只读状态或取消步骤使用独立的每 Token 每分钟 120 次控制额度。模型目录不扣自主额度；任务推进和取消仍受金额预算、共享额度、账号池和操作权限约束，不能借此创建新任务。Token、能力包、有效期和显式禁止/批准规则始终有效。美元自动额度只适用于已确认的固定调用价格；费用不能确定时改用次数或单次批准。`service_approval_required` 交人批准一次或调整额度，`service_use_denied` 表示禁止，单次批准不能覆盖。批准绑定 Token、服务和操作，一小时内一次有效；`X-LocalRouter-Allowance-Receipt` 是占用记录，未知结果先核对，不能重试探测。

维护独立授权：人开启 Agent 维护并签发仅维护 Token 后，通过 `LOCALROUTER_MAINTAINER_TOKEN_FILE` 和 `/manage/mcp` 维护。Service Token 无维护权限，免密控制台也不构成 Agent 授权；不读管理员凭据，不通过 `/local/api` 自行批准。已委托的兼容修复用维护 lane 准备提案，审阅后 `lr setup apply <id> <proposal.digest>`；扩大目标、认证或操作需新批准。高级 Pack 编辑仍走 draft → review → plan → exact-digest apply，保留他人的草稿修改。

LAN 地址须经操作者批准，设置 `LOCALROUTER_ALLOW_LAN=true` 后，`lr` 在读 Token 前核对 `scope=lan-service` 和 `maintenance.available=false`。LAN 只提供调用，身份领取和维护留在 loopback。Token locator 是私有文件路径，不是密钥值；不要把 Token 放进聊天、源码或日志。

完整命令参考：`lr help all`。所选服务的操作说明：`lr docs <pack>`。

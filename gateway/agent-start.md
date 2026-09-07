# LocalRouter Agent 入门

`lr` 是 LocalRouter 的命令行入口。它发现并调用明确的服务，不替你选择供应商；接入新服务由 Agent 准备，人批准权限。

## 1. 先确认你是谁

运行 `lr init`。这是只读检查，不创建账号、不修改权限、不调用供应商。

- 默认普通模式（`strict_mode: false`、`identity_required: false`）：有效的现有 Service API Token 即可调用，包括系统默认 Token，无需额外登记或审批。`ready: true` 表示可以继续准备调用；`identity_ready` 单独表示是否为已登记的独立 Agent。Token 策略、能力包及供应商池限制仍然生效。
- 主动开启严格模式后，CLI 才要求独立 Agent 身份；核对 agent_code、workspace 是否属于你。已有独立身份的工作目录绑定在两种模式下都继续检查。以下申请流程仅用于需要独立身份或细分授权时。
- `ready: false` 或非零退出表示身份未准备好。严格模式下，`identity_kind: bootstrap` 不能满足独立身份要求。
- 新接入优先运行 `lr identity request <agent-code> <policy-json|@file>`，明确给出 `packs`、Pack 限定的 `operations`，以及需要的 `models`、`daily_request_limit`、`requests_per_minute`、`max_in_flight`、`expires_at`。操作必须来自当前公共契约；不要使用通配 Pack 或操作。CLI 自动携带当前工作目录，不读取系统默认 Token 或管理员凭据。此命令仅申请权限，不调用供应商、不自动批准。
- 人在返回的 registration_url（本机 `/#tokens`）核对服务、操作、模型和额度，点击「批准以上范围」。随后 `lr identity claim` 自动将凭据保存到私有 0600 文件并绑定当前 Agent；下一次 `lr exec` 也会自动领取已批准申请。不要把 Token 发到聊天里。未批准的申请返回明确状态，不能继续调用。
- 同一会话再次申请相同内容、领取中断后重试都复用原申请/原身份，不清零用量。申请及凭据领取窗口为申请后 24 小时。用 `lr identity cancel` 撤回未生效的申请后可提交修改版；已经批准的身份应通过工作台撤销。Token 默认长期有效，无需周期性重新签发；设置的授权截止时间不会自动延长。
- 已有人签发的 Token 仍支持 `lr identity bind <agent-code> <token-file>`。绑定核对 agent_code、工作目录和服务权限，只保存定位器。Codex 自动使用会话 ID；其他宿主设置稳定、独立的 `LOCALROUTER_AGENT_SESSION`。不同 Agent 会话、目录或服务地址不共用绑定；显式 `LOCALROUTER_API_TOKEN_FILE` 始终优先，启用自动接入前需去掉这个覆盖项。跨机器 LAN 仍使用人签发并安全交付的 Service Token，身份领取只在 loopback 提供。

身份未准备好时，可以继续 `lr guide`、`lr tree` 和 `lr docs <pack>` 阅读公共契约；不要自行读管理员凭据或借免密 `/local/api` 签发身份。

已完成接入、明确了服务和模型后，日常调用使用 `lr exec` 即可。无需每次手动重复 init、目录查询和 preflight；工具会自动执行必要检查。需要更换绑定时运行 `lr identity bind`，`lr identity forget` 只忘记定位器，不删除或撤销 Token。

## 2. 首次选择服务或排查问题

```sh
lr status
lr tree
lr find operation <你要做的事>
lr describe <pack> <operation_key>
lr docs <pack>
```

`lr tree` 返回供人阅读的文本树，不是 JSON。查看整包用 `lr show <pack>` 或 `lr catalog <pack>`；`lr describe` 必须同时传 Pack 和 operation。写脚本时先检查实际 JSON 结构：`lr status` 的服务数组是 `protocols`，`lr catalog` 的操作数组是 `operations`，`lr find model` 的匹配数组是 `matches`。不要猜字段，也不要因为离线解析失败而重复请求供应商。

比较返回的供应商、ready、验证覆盖、请求 schema、费用和重试规则，明确选择一个 Pack 和 operation。服务目录的 ready 来自公共发现，不证明当前 Agent 已注册，更不证明它有调用权限；身份只看 lr init / lr whoami，授权还要核对有效策略、lr setup bundles 和预检。`ready: true` 不等于真实供应商调用已经验证。费用缺失是未知，不是免费。

只找操作用 `lr find operation`；找池用 `lr find pool`；找模型用 `lr find model`。模型搜索可能请求供应商目录。需要动态模型时，最终执行 `lr find model --exact <pack>:<model-id>` 并要求唯一结果；`lr exec` 会按当前契约自动执行同样的精确检查，不需要在它前面再查一次。示例模型名不证明可用。模型查询失败时 `success=false`、`complete=false` 且退出非零；读取 `failures[].code/reason/http_status/next_action`，不要把这时的零条匹配解释成模型不存在。

当前非 `--exact` 的模型搜索会读取所有就绪的模型目录，再筛选结果；把 Pack 名作为搜索词不会限制上游查询范围。不要对每个 Pack 重复做模糊搜索。确实需要覆盖全部模型服务时，可一次 `lr find model --all` 保存完整快照，离线按 Pack 选候选，再逐个精确确认；带 Pack 的 `--exact` 查询只访问该 Pack。没有供应商目录而采用请求 schema 枚举的模型，也要完成这个精确确认步骤。

## 3. 日常调用

在人已批准的服务、操作、模型、有效期和资源上限内，调用已获授权，无需逐次询问用户。只有扩大范围或提高上限才再次请求决定。推荐一次完成准备和调用：

```text
lr exec <pack> <operation> <body-json> <path-params-json> <query-params-json>
```

它自动领取已批准的待接入身份，按当前模式核对调用凭据并复用本次执行的契约，解析精确模型与兼容操作，通过 preflight 后向网关发送一次正式请求。响应（包括 SSE）直接输出，同时保存原始响应、退出状态及追踪 ID 到当前 Agent 的私有结果目录。`lr result` 列出记录，`lr result <call-id>` 查看原始响应文件定位器；`lr result <call-id> --refresh` 查询同一调用的网关证据，不重发供应商请求。`response_received` 只表示收到响应，不代表业务任务完成；中断或失败时结果保留为 unknown，先读已有结果和已公布的状态查询操作。

预检失败的 `blocked_at`、`reason`、`resolution.automatic_checks`、`resolution.next_actor` 和 `next_action` 说明拦截位置、已做检查以及谁需要做什么。`upstream_called=false` 表示预检未调用供应商。目录读取是单独的只读请求，不是生成。网关按照 Pack 既有重试上限处理安全重试；启用共享额度时仅能确认未发出的请求可自动恢复，已发送或结果未知的请求不会因额度预留而重复发出。确定未发出的终止请求自动释放预留额度。

需要扩大服务/操作/模型范围或提高 Token 限额时，Agent 可用 `lr identity access <complete-policy-json|@file>` 提交完整替换策略，人仍在工作台批准；`lr identity access-status <access-id>` 检查进度。批准前继续执行原权限；批准后 Token 与累计用量保持不变，能力包和共享额度继续约束。期间有人修改策略时旧批准被拒绝，需重新准备。服务新增操作不会自动加入接入时批准的范围。资源访问粒度以已批准操作的契约为准，Token 模型限制不等于任意业务对象的访问控制。

需要单独检查或已有外部运行时自行准备时，保留低层命令：

```text
lr preflight <pack> <operation> <body-json> <path-params-json> <query-params-json>
lr call      <pack> <operation> <body-json> <path-params-json> <query-params-json>
```

三份 JSON 的默认值都是 `{}`。GET 的 body 必须是 `{}`。例如，一个已发布 `GET /jobs/{jobId}` 的操作，其路径参数应放在第四个参数 `'{"jobId":"实际ID"}'`；查询参数如 `'{"limit":5}'` 放在第五个。`operation_id` 是语义标识，点号不改成斜杠；直接 HTTP 只用契约的 `call_url`。

路径参数值必须是字符串。即使供应商返回数字 `jobId: 123`，也应传 `'{"jobId":"123"}'`，不要把它放入 body；Python 中先用 `str(resource_id)`。查询参数可以是字符串、数字或布尔值。以每个操作发布的路径名和必填查询字段为准，不把另一个操作的参数照搬过来。

真实、付费或有副作用的调用要先取得该操作的授权。预检不调用供应商，非零退出必须处理。调用只执行一次，先保存原始响应和退出码，再离线解析；解析失败不能重发。未知结果先核对资源状态。

批量脚本应在每次真实调用前保存唯一的开始记录，每项结束立即保存 stdout、stderr 和退出码；不能等整批结束才写证据。超时保存已收到的内容并标记结果未知，不重放已经开始的操作。总执行期限须覆盖各项期限，或拆成短批。聊天响应被输出上限截断、仅有推理片段时，不能算完成了用户请求。

只有通过 LocalRouter 已发布工作流启动，并取得 LocalRouter Job ID 后，才保留 Pack、workflow 和 Job ID，用 `lr watch` 观察。普通供应商返回的 taskId/resourceId 不是 LocalRouter Job ID；没有已发布工作流时，用该 Pack 的状态查询操作核对资源，不能自行套用 `lr watch`。仅对声明支持取消的工作流使用 `lr cancel`。

## 4. 缺少服务时由你准备

```sh
lr setup templates
lr setup template <id> <version>
lr setup schema
lr setup prepare @proposal.json
lr setup get <proposal-id>
```

模板目录只返回摘要，选中后才读取完整契约；`--all` 仅用于确实需要全部模板的情况。按供应商实际文档适配，模板示例只表示形状。

`kind: connection` 可以同时带明确的 `bundle`。人一次批准目标服务、操作范围和能力包版本。`kind: bundle` 单独申请能力组合，`kind: template` 发布可复用模板。准备不会安装、调用上游或授予权限。密钥由人在批准时单独填写，Agent 不指定 secret_file。

获批后检查 `lr setup get` 和 `lr setup bundles`，再按第 3 步执行已授权调用。`lr setup verify <id>` 只读取安装状态和已有调用证据；HTTP 200 不代表业务任务完成。`applying` 中断时用 `lr setup reconcile <id>`，不要重放 apply。

能力包固定在批准版本；新增操作不会自动获得权限。显式空授权禁止服务调用。任务标识 `LOCALROUTER_TASK_ID` 用于关联追踪，不改变身份或权限。资源快照按资源去重，费用保留来源与未知状态。

## 5. 维护权限单独授予

Service Token 不具有维护权限。由人在 `/#tokens`（Agent 工作台）签发独立的仅维护 Token，并开启 Agent 维护；`/manage/mcp` 是执行维护操作的入口，不负责签发 Token。Agent 获得这份独立凭据后设置 `LOCALROUTER_MAINTAINER_TOKEN_FILE`，并确认发现接口已启用 Agent 维护。没有这个条件就把准确方案交给人，不要读取管理员凭据，也不要使用管理员后备通道。

已经授权的兼容修复使用 `LOCALROUTER_SETUP_LANE=maintenance lr setup prepare @repair.json` 准备，再用同一维护 lane 的 `lr setup get <id>` 检查。执行命令是 `lr setup apply <id> <digest>`；这里的 digest 是 `setup get` 返回的 `proposal.digest`，不是模板 digest，也不是 pack_digest。改变目标、认证、操作或工作流需要新批准。高级 Pack 作者使用 `/manage/mcp` 的独立 draft → review → plan → exact-digest apply；无论哪条路径都不能覆盖其他人的修改。


## 服务共享自主额度

人可在「服务与渠道 → 自主额度」先开启默认关闭的「严格模式」总开关，再为单个服务启用共享额度，新配置基础额度为每月 3 美元，默认关闭，批量配置也不会自动启用。总开关关闭时沿用现有调用权限，暂停额度检查但保留配置和用量；重新开启不清零。升级保留规则和用量，但旧规则不会隐式开启严格模式；显式保存的总开关状态继续生效。
明确公布的 GET/HEAD 模型目录读取不扣自主额度，但仍遵守 Token、能力包、号池和显式禁止/批准规则。缺乏固定价格的操作不能启用美元自动额度，须选择次数或单次批准；界面会在保存前提示。
操作可设置独立子额度；调用同时受服务总额度与操作子额度约束，共用周期和单位。
启用后，额度内的调用属于人预先批准的范围，仍须满足当前 Token/能力包权限并执行预检。
`service_approval_required` 表示额度不足、操作必须批准或费用无法安全预估；返回人批准一次或调整额度，不自动重试，不自行修改配置。
`service_use_denied` 表示明确禁止，单次批准不能覆盖。关闭额度功能不授予新的调用权限。
单次批准绑定 Agent Token 与精确操作，一小时内只可使用一次；参数和实际费用均在该操作批准范围内。
`X-LocalRouter-Allowance-Receipt` 是占用记录编号。未知结果保留占用，先核对供应商记录，不能重新调用以试探结果。

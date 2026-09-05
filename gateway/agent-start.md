# LocalRouter Agent 入门

`lr` 是 LocalRouter 的命令行入口。它发现并调用明确的服务，不替你选择供应商；接入新服务由 Agent 准备，人批准权限。

## 1. 先确认你是谁

运行 `lr init`。这是只读检查，不创建账号、不修改权限、不调用供应商。

- `ready: true` 表示当前 Token 对应一个已注册 Agent。核对 agent_code、workspace 是否确实属于你；共用别人的 Token 不会获得独立身份。
- `ready: false` 或非零退出表示身份未准备好。`identity_kind: bootstrap` 是系统默认身份，不能当作你的独立身份。
- 请人到返回的 registration_url（本机 `/#tokens`）登记 agent_code、agent_name、workspace，签发 Service Token，并保存到权限 0600 的私有文件。将 `LOCALROUTER_API_TOKEN_FILE` 设为该文件的绝对路径，再运行 `lr init`。变量保存路径，不保存 Token 值。不要把 Token 发到聊天里。

身份未准备好时，可以继续 `lr guide`、`lr tree` 和 `lr docs <pack>` 阅读公共契约；不要自行读管理员凭据或借免密 `/local/api` 签发身份。

## 2. 找到已经存在的服务

```sh
lr status
lr tree
lr find operation <你要做的事>
lr describe <pack> <operation_key>
lr docs <pack>
```

`lr tree` 返回供人阅读的文本树，不是 JSON。查看整包用 `lr show <pack>` 或 `lr catalog <pack>`；`lr describe` 必须同时传 Pack 和 operation。写脚本时先检查实际 JSON 结构：`lr status` 的服务数组是 `protocols`，`lr catalog` 的操作数组是 `operations`，`lr find model` 的匹配数组是 `matches`。不要猜字段，也不要因为离线解析失败而重复请求供应商。

比较返回的供应商、ready、验证覆盖、请求 schema、费用和重试规则，明确选择一个 Pack 和 operation。服务目录的 ready 来自公共发现，不证明当前 Agent 已注册，更不证明它有调用权限；身份只看 lr init / lr whoami，授权还要核对有效策略、lr setup bundles 和预检。`ready: true` 不等于真实供应商调用已经验证。费用缺失是未知，不是免费。

只找操作用 `lr find operation`；找池用 `lr find pool`；找模型用 `lr find model`。模型搜索可能请求供应商目录。需要动态模型时，最终执行 `lr find model --exact <pack>:<model-id>` 并要求唯一结果；示例模型名不证明可用。

当前非 `--exact` 的模型搜索会读取所有就绪的模型目录，再筛选结果；把 Pack 名作为搜索词不会限制上游查询范围。不要对每个 Pack 重复做模糊搜索。确实需要覆盖全部模型服务时，可一次 `lr find model --all` 保存完整快照，离线按 Pack 选候选，再逐个精确确认；带 Pack 的 `--exact` 查询只访问该 Pack。没有供应商目录而采用请求 schema 枚举的模型，也要完成这个精确确认步骤。

## 3. 调用时把三类参数分开

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

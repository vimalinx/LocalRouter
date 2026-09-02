# LocalRouter Protocol Pack v2

Protocol Pack v2 把非 LLM HTTP 服务表示为可验证、可发布和可回滚的配置，而不是为每个供应商修改 Go 源码。

## 运行面

- `/p/<pack>/*`：声明式操作路由，使用 LocalRouter 客户端 Bearer Token。
- `/w/<pack>/<workflow>`：创建本地异步 Workflow Job。
- `/w/<pack>/<workflow>/<job>`：读取并按配置节奏推进一次轮询。
- `/docs`、`/.well-known/localrouter.json`、`/docs/openapi.json`：Agent 发现与文档。

## 转换能力

每条 route 可以声明：

- `capabilities`：稳定的 Agent 语义标签，例如 `web.search`、`ai.chat` 或 `video.generate`；共享标签只让独立 `operation_key=<pack>.<operation_id>` 并列可发现，不会合并供应商、模型、号池、价格、readiness 或线协议。
- `upstream_path`：把稳定的本地路径映射到供应商路径，支持 `{path_parameter}`。
- `request_transform.query` 和 `request_transform.headers`：设置安全的查询参数与非敏感 Header，值可引用 `{{path.id}}` 或 `{{query.mode}}`。
- `request_transform.json`、`response_transform`：使用点路径执行 `set`、`rename`、`remove`、`extract`、`envelope`。
- `affinity`：从创建响应提取资源 ID，并让后续包含该 ID 的操作继续使用原账号。

JSON 转换只处理 JSON。multipart、文件和流式操作保持字节透传；流开始后不做重放或换号。

## 凭据所有权

三种模式不可混用：

1. 没有 `pool`：一个 mode-600 `secret_file`，适合单 Key。
2. `pool.mode=external`：LocalRouter 使用一个稳定凭据，外部网关拥有账号生命周期。
3. `pool.mode=local`：LocalRouter 读取 mode-600 凭据池，拥有选号、冷却、并发、重试和 affinity；注册、验证码、人工 OAuth 和账号生产仍在外部维护器。

`pool.mode=local` 有两种受保护来源：

- `credentials_file`：LocalRouter 原生 `{schema_version, credentials}` 文件。
- `source`：外部维护器拥有的只读 JSON/JSONL 池。Pack 只声明字段映射；mode-600 locator 保存真实路径。LocalRouter 将外部身份哈希成 `src-*`，不会在状态、文档或修订中保存邮箱、路径或 Secret。`cookie-list-json` codec 可从浏览器 Cookie 导出中选择单个 Cookie，并继承其过期时间。

本地池支持 `round-robin`、`lru`、`least-inflight`、`balance-aware` 和 `smooth-weighted`。运行状态与凭据文件分离，状态文件不保存 Secret。

## 异步 Workflow

Workflow 引用稳定 `operation_id`：

- `create_operation` 必须是无路径参数的 POST。
- `poll_operation` 必须是含一个资源 ID 参数的 GET。
- 可选 `cancel_operation`。
- `resource_id_path`、`status_path`、状态集合、轮询间隔和最大次数全部配置化。

LocalRouter 创建并持久化本地 Job。GET Job 时，到达 `next_poll_at` 才执行一次上游轮询；成功、失败、取消和超时都是终态。重启后 Job 状态仍可读取和继续。

## Agent 发布生命周期

1. 修改 `gateway/protocols/<id>.json` 和 `<id>/guides/*.md`。
2. 运行单元测试、Mock 契约测试和真实最小调用。
3. `tools/protocol-pack-lifecycle.sh validate`。
4. `tools/protocol-pack-lifecycle.sh plan`，检查 `digest` 和协议清单。
5. 使用完全相同的 digest 执行 `apply`；候选发生任何变化都会被拒绝。
6. 从 `/.well-known/localrouter.json` 重新发现能力，并验证 `/p` 或 `/w`。
7. 使用 `history` 查看不可变配置修订；必要时按 digest `rollback`。

旧 `/local/api/protocols/reload` 保留兼容，但 Agent 维护应使用 plan/apply 路径。

## 号池目录

Agent 可从同一端口读取 `/docs/pools/index.json` 和 `/docs/pools/catalog.md`。目录区分 external Pack、external-readonly source、模型面、待部署网关、空池、NO-GO、账号池和出站代理池；它不包含账号、邮箱、Key、Cookie 或私有文件路径。

# LocalRouter Gateway

## 作用

这个目录是 LocalRouter 的独立本机运行时：

- 只允许 `127.0.0.0/8` 或 `::1` 等回环地址；
- 自动创建单一本机管理员和可撤销的默认 API 密钥；
- 本地管理 API 默认仅靠 loopback 边界免密使用，并可选用 `X-Local-Admin` 密码保护；
- 保留 OpenAI、Anthropic 与 Gemini 协议入口；
- 每条模型渠道可独立配置供应商请求 Profile：固定、移除或补充转发安全请求头，控制 User-Agent 和查询串编码；上游鉴权在 Profile 之后注入且不可被覆盖；
- 控制台把模型渠道和 Protocol Pack 号池合并到同一工作区，并将 Pack/operation 的即时启停状态保存在私有 `service-controls.json`；该运行态覆盖不改发布定义或 digest；
- 不注册用户注册、登录、OAuth、支付、订阅和公开管理站点路由；
- 不包含用户平台、支付、订阅、注册或 Provider OAuth 后台任务；
- 从 `protocols/*.json` 加载经过验证的 Protocol Pack v1/v2：REST/SSE、multipart/文件字节透传、路径/查询/JSON 转换、本地或外部号池、资源粘性和异步 Workflow，并把生成的运行事实与 `protocols/<id>/guides/*.md` 中的 Agent 指南合并发布到 `/docs`。

OAuth 和账号刷新属于操作者选择的外部维护器。公开发行版不附带供应商 Pack、真实端点、账号池或供应商 sidecar；新安装默认没有可调用的供应商能力。

## 运行

要求：Go 1.25.13+ 和 Bun。控制台源码位于 `web-src/`，使用 React、Tailwind CSS 和 shadcn 风格组件；`web/` 是编译后由 Go 嵌入的产物。

```bash
bun install --cwd gateway/web-src --frozen-lockfile
make -C gateway web
./gateway/run.sh
```

默认监听 `http://127.0.0.1:8317`。安装运行读取
`$XDG_CONFIG_HOME/localrouter/config.env`；源码开发仍可用 `.env`。
`LOCAL_GATEWAY_HOST` 如果不是回环 IP，程序会拒绝启动。

首次启动会生成：

- `$XDG_DATA_HOME/localrouter/admin-token`：控制台密码保护开启时解锁 `/local/api/*`，并始终用于人工维护 MCP；
- `$XDG_DATA_HOME/localrouter/admin-auth.json`：控制台密码保护开关，默认关闭；
- `$XDG_DATA_HOME/localrouter/api-token`：AI 客户端访问 `/v1/*`、`/p/*`、`/w/*` 或 `/mcp`；
- `$XDG_DATA_HOME/localrouter/localrouter.db`：渠道、令牌和日志；
- `$XDG_CONFIG_HOME/localrouter/protocols`：由二进制首次初始化、之后由用户维护的 Pack；
- `$XDG_STATE_HOME/localrouter`：事件、工作流、调度状态、草稿和发布历史。

不要在终端打印这些文件。默认打开浏览器控制台会直接进入；启用密码保护后，密码只保存在当前标签页内存，不写 LocalStorage。

控制台密码保护默认关闭。可在“运行概览 → 控制台密码保护”中开启并设置 16–512 个可打印 ASCII 字符的自定义密码，也可随时关闭恢复免密。服务端原子替换 `admin-token`、持久化 `admin-auth.json`、保持 `0600` 并立即更新鉴权，不需要重启；控制台密码与客户端 API Token 相互独立。

## 接入已有的本机上游

1. 先在外部服务内完成登录、账号池和刷新配置，并确认它的固定回环端点可用。
2. 打开 `http://127.0.0.1:8317/`；默认直接进入，若操作者开启了密码保护则输入自定义密码。
3. 新建与上游线协议匹配的私有渠道。
4. Base URL 填上游的固定本机地址；模型列表和密钥以该上游实际合同为准。
5. 点击渠道测试，再让客户端把 Base URL 指向 LocalRouter。

具体端口、模型和密钥由本机操作者的私有配置决定，本项目不猜测、不复制，也不将其作为公开支持清单。

## 客户端入口

主要入口包括：

- OpenAI：`/v1/models`、`/v1/chat/completions`、`/v1/responses`、图片、音频、embedding、rerank、moderation；
- Anthropic：`/v1/messages`；
- Gemini：`/v1beta/models/*`；
- 本地状态：`/healthz`、`/local/status`；
- 本地管理：`/local/api/*` 默认在 loopback 上免密；`PUT /local/api/admin-auth` 开关密码保护，`PUT /local/api/admin-token` 安全轮换自定义密码且不回显新值。开启后请求需要 `X-Local-Admin`。
- 自定义协议：`/p/<protocol>/*`，使用本地 API 令牌；模板与 Agent 指南位于 `/docs`。
- 异步工作流：`/w/<protocol>/<workflow>` 创建本地 Job，`/w/<protocol>/<workflow>/<job>` 按受控节奏推进轮询或取消。
- MCP：`POST /mcp` 提供无状态 JSON-RPC `initialize`、`tools/list`、`tools/call`，工具从已发布且就绪的 Pack 生成。
- 维护 MCP：`POST /manage/mcp` 默认只接受独立的 `X-Local-Admin` 管理密钥。可选 Agent 入口默认关闭；开启后仅接受带 `localrouter.maintain` 的维护专用 Bearer Token，且该 Token 不能调用服务入口。此端点提供 Pack/operation/供应商请求 profile 的强类型 upsert、结构化 lint、通用 merge patch、指南生成、影响审查、精确 digest 发布和回滚工具。
- Agent 发现：`/.well-known/localrouter.json`；机器索引：`/docs/index.json`；汇总 OpenAPI：`/docs/openapi.json`。
- Agent 决策：`GET /agent/operations` 返回 Token 可见的全部独立 operation；`POST /agent/resolve` 返回所有精确匹配（无精确项时才做文本匹配）；`POST /agent/compare` 按调用者顺序比较 2–50 个完整合同且不推荐、不合并；`GET /agent/operations/<pack>/<operation>` 渐进披露一个明确 `operation_key` 的契约；`POST /agent/preflight` 在不访问上游的情况下检查输入和运行条件；`GET /agent/whoami` 返回当前服务 Token 的脱敏有效权限。
- 契约缓存：discovery 的 `contract.digest` 与机器索引的 `contract_digest` 一致；digest 或 Agent `schema_version` 任一变化都刷新缓存。Pack route 的 `capabilities` 是语义标签，Pack、operation、模型、池、价格和可用性始终分别公开。
- 长任务 CLI：`lr run` 自动区分普通 operation 与已发布 workflow；`lr watch` 默认无限等待且可用相同 Job ID 恢复；`lr cancel` 走已有的 Token 所有权和工作流取消语义。
- 号池目录：`/docs/pools/index.json`；Agent 适配说明：`/docs/pools/catalog.md`。
- Pack 生命周期：Agent 使用 `/manage/mcp` 或上级 `lr manage-*`；LocalRouter 负责路径、格式、原子安装、线上摘要复验和本地失败自动回滚。`/local/api/protocols/*` 继续作为人工控制台使用的管理 API。
- 端口草稿：维护工具支持隔离创建、语义修改、校验和计划；精确 digest 发布失败时保留草稿，并返回结构化阶段、错误码和回滚结果。
- Token 策略：`/local/api/token-policies/*` 限制入口、Pack、operation、model、每分钟/每日请求、并发与到期时间。
- Agent 注册与计量：非系统 Token 必须绑定唯一 `agent_code`、名称和工作区；`GET /local/api/agent-usage` 按 Token ID 合并模型日志与 Protocol Pack 事件，返回调用、输入/输出/缓存读写/推理 Token、成本状态和额度使用。`GET /agent/whoami` 返回当前 Token 的脱敏 Agent 身份。
- 调用账本：`GET /local/api/protocol-events` 为每次 Protocol Pack、Workflow 与 MCP 调用保留一条脱敏事件。模型响应存在标准 usage 时会记录规范化 Token；当上游明确返回 USD 成本或 Pack 有可计算的 operation/model 标价时，事件会冻结当次成本及组成，后续改价不会重算新格式的历史事件。
- 任务与审计：`/local/api/workflows/jobs` 和 `/local/api/protocol-events` 只返回脱敏状态；客户端只能列出和操作自己 Token 创建的新 Job。

客户端应通过其 secret-file、环境文件或系统凭据能力读取
`$XDG_DATA_HOME/localrouter/api-token`。不要将令牌写入仓库、命令历史或截图。

## 用量与成本边界

LocalRouter 的 `usage-accounting` 是本机计量和成本归因，不是支付、余额扣减或供应商发票系统。兼容入口与 OpenAI、Anthropic、Gemini 形状的 Protocol Pack 响应会规范化记录 input、output、cache-read、cache-write、reasoning 和 total Token；缓存与推理 Token 是输入/输出的细分维度，不会再次加入 total。

成本来源分开标记：上游明确返回 `usage.cost_usd` 或 `usage.total_cost_usd` 时记为 `reported`；Pack 的 `operation` 级 `per-request` 价格以及 `model` 级 input/output/cache/reasoning/total-token 价格记为 `confirmed` 或 `estimated`；只有部分价格单位可归因时记为 `partial`；没有可靠价格时记为 `unavailable`，绝不把零展示成免费。旧 Channel 日志中的 `quota / 500000` 只作为历史兼容值保留。

## 验证

```bash
make web-test
go test ./...
go build -trimpath -o localrouter .
../tests/smoke_local_gateway.sh
../tests/e2e_relay.sh
```

`smoke_local_gateway.sh` 验证真实二进制、回环监听、权限、鉴权及被移除路由；`e2e_relay.sh` 用确定性的本机 OpenAI 兼容上游验证渠道创建、模型路由、非流式响应、SSE 流式响应和日志。

`protocol_e2e.sh` 用真实 LocalRouter 二进制和虚构 loopback fixture 验证 allowlist、上游认证隔离、普通 JSON、SSE 流式透传、转换、外部只读池源、号池重试、资源粘性、持久化 Workflow、哈希绑定发布和确定性生成的脱敏文档。

这些测试不调用任何真实供应商或付费服务。操作者配置私有上游后，应在自己的环境中单独做 provider-backed smoke test。

## 来源与许可

当前二进制只由本目录源码构建，不包含外部网关子模块。早期衍生关系和被审计参考见上级 `PROVENANCE.md`；这次独立化不是 clean-room 重写。`LICENSE` 指向仓库根 AGPLv3，当前直接依赖清单位于根目录 `THIRD-PARTY-LICENSES.md`。

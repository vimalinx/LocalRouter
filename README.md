<div align="center">

<img src="gateway/web-src/public/assets/localrouter.svg" alt="LocalRouter" width="88" height="88">

# LocalRouter

**只运行在本机的通用 AI / API 网关**

把模型 API、普通 REST、SSE、文件、WebSocket、gRPC、异步任务和账号池统一到一个可发现、可配置、可回滚的本机入口。

![License](https://img.shields.io/badge/license-AGPL--3.0-6b7280)
![Go](https://img.shields.io/badge/Go-1.25.13%2B-64748b)
![React](https://img.shields.io/badge/React-19-64748b)
![Network](https://img.shields.io/badge/listen-loopback_only-5f8f7b)

[控制台](http://127.0.0.1:8317/) · [Agent 文档](http://127.0.0.1:8317/docs) · [机器发现](http://127.0.0.1:8317/.well-known/localrouter.json) · [OpenAPI](http://127.0.0.1:8317/docs/openapi.json)

[源码](https://github.com/vimalinx/LocalRouter) · [安全政策](SECURITY.md) · [贡献指南](CONTRIBUTING.md) · [发布记录](CHANGELOG.md)

</div>

---

LocalRouter 现在使用自己的 Go 运行时：本地 SQLite 渠道与 Token、同模型多渠道选路、流式透传、请求日志、声明式 Protocol Pack、账号池、异步工作流、Agent 文档和哈希绑定发布均由本仓库实现，编译和运行不依赖外部网关源码。

它不是账号注册平台，也不接管人工 OAuth、CAPTCHA、支付或订阅。公开发行版不附带任何供应商 Pack、真实上游地址、账号池或供应商专用适配器；安装完成后，只有本机操作者明确发布的私有配置才会产生可调用能力。

## 一分钟启动

源码安装需要 Go 1.25.13+、Bun 和 Git；Release 压缩包只包含通用主网关和管理工具。

```bash
git clone https://github.com/vimalinx/LocalRouter.git
cd LocalRouter
./tools/install-localrouter.sh install
```

安装器把主程序和 `lr` 放入 `~/.local/bin`，创建并启动
`localrouter.service` 用户服务。卸载只移除程序和服务，不删除用户配置、
数据库、Key 或状态：

```bash
./tools/install-localrouter.sh uninstall
```

然后打开：

- 控制台：<http://127.0.0.1:8317/>
- Agent 文档：<http://127.0.0.1:8317/docs>

首次启动遵循 XDG Base Directory Specification：

| XDG 目录 | 默认位置 | 内容 |
|---|---|---|
| 配置 | `~/.config/localrouter` | `config.env`、模型渠道 Profile、可编辑 Protocol Packs |
| 数据 | `~/.local/share/localrouter` | SQLite、管理 Key、API Key、Provider 凭据与池定位器 |
| 状态 | `~/.local/state/localrouter` | 调用事件、工作流、调度状态、草稿和发布历史 |
| 缓存 | `~/.cache/localrouter` | 可删除的临时缓存 |

私有目录使用 `0700`，Key、数据库和状态文件使用 `0600`。运行
`localrouter paths` 可以查看当前机器解析后的路径，但不会显示任何 Key。

## 自定义控制台登录密钥

第一次使用自动生成的 `~/.local/share/localrouter/admin-token` 解锁控制台。进入“运行概览”，点击“更改登录密钥”即可设置自己的密钥：

- 长度为 16–512 个可打印 ASCII 字符；
- 允许中间空格，不允许首尾空白；
- 服务端原子写回 XDG 数据目录中的 `admin-token` 并保持 `0600`；
- 新密钥立即生效，不需要重启；
- 当前标签页自动切换到新密钥，其他旧标签页会失效；
- 密钥不会出现在 API 响应、日志或 LocalStorage 中。

管理密钥和客户端 API Token 是两套独立凭据。更改登录密钥不会影响已经发给应用或 Agent 的 API Token。

## 能做什么

| 能力 | LocalRouter 的处理方式 |
|---|---|
| 模型协议 | OpenAI-compatible（含 Responses 等同协议入口）、Anthropic Messages 与 Gemini 原生透传 |
| 普通服务 API | 通过 Protocol Pack 声明路径、查询、请求头、请求体、响应与错误转换 |
| 流和二进制 | SSE、multipart、文件与未知内容优先采用字节透传 |
| 双向/高性能协议 | WebSocket、原始 gRPC、loopback adapter 和受限 WASM adapter |
| 异步任务 | 创建、轮询、继续、取消、结果提取和重启恢复 |
| 账号池 | 本地轮换、冷却、并发租约、资源粘性、健康状态与额度遥测 |
| 同服务多供应商 | `/v1` 按同模型渠道的优先级/权重路由；每条模型渠道和每个 Protocol Pack target 都可使用同一套供应商请求 Profile，固定、移除或补充转发安全请求头并控制 User-Agent 与查询串；Pack 另可用 `target_selector` 把多组 key 安全映射到固定兼容后端 |
| 模型协议 Profile | `$XDG_CONFIG_HOME/localrouter/channel-profiles.json` 声明请求路径归属、默认地址、鉴权位置和模型目录解析；增加 header/query/no-auth 供应商不改 Go 源码 |
| 外部号池 | 支持 `external` 与 `external-readonly`，不强行迁移外部网关的所有权 |
| Agent 维护 | 端口发现、草稿、影响审查、精确 digest 发布、历史与回滚 |

Protocol Pack 不限定 LLM，也不假设请求或响应一定是 JSON。仓库只发布 Schema、生命周期工具和虚构测试夹具，不发布可连接真实服务的 Pack。测试夹具只在隔离测试目录运行，不会被安装、嵌入或出现在运行时发现结果中。

## 运行结构

```text
本机 App / Agent
        │  API Token
        ▼
  127.0.0.1:8317
        │
        ├── /v1   模型兼容入口
        ├── /p    自定义 Protocol Pack
        ├── /w    持久化异步工作流
        ├── /mcp  Agent 工具发现与调用（服务 Token）
        ├── /manage/mcp  语义化维护（管理密钥；可选维护 Token）
        └── /docs 运行契约、OpenAPI 与用法指南
                 │
                 ▼
       渠道 / 本地号池 / 外部网关
```

LocalRouter 强制监听回环 IP。请求参数不能选择任意上游地址、认证端点、adapter 路径或 WASM 模块。

同一个模型仍可配置多条 `/v1` 兼容渠道，但 Agent 能力面不会把这些供应商折叠成一个条目：每个 Pack 和 operation 都保留独立 `operation_key`、地址契约、模型映射、价格、readiness 与号池状态。只有同一供应商、同一调用契约下的多枚凭据才进入该 Pack 自己的号池做轮换；跨供应商能力只以共享语义标签并列发现，由 Agent 明确选择。

## 本机入口

| 入口 | 地址 |
|---|---|
| 控制台 | `http://127.0.0.1:8317/` |
| 健康状态 | `http://127.0.0.1:8317/healthz` |
| Agent 发现 | `http://127.0.0.1:8317/.well-known/localrouter.json` |
| 文档首页 | `http://127.0.0.1:8317/docs` |
| 机器索引 | `http://127.0.0.1:8317/docs/index.json` |
| 汇总 OpenAPI | `http://127.0.0.1:8317/docs/openapi.json` |
| MCP | `POST http://127.0.0.1:8317/mcp` |
| 维护 MCP | `POST http://127.0.0.1:8317/manage/mcp` |
| 号池目录 | `http://127.0.0.1:8317/docs/pools/index.json` |

`/doc` 是 `/docs` 的永久重定向别名。

## 接入已有的本机上游

1. 在外部服务中完成它自己的登录、账号池和刷新配置。
2. 确认它的固定回环端点已经可用。
3. 在 LocalRouter 中创建私有渠道或 Protocol Pack。
4. 填写固定的本机 Base URL、协议和所需凭据定位。
5. 测试渠道，再把客户端 Base URL 改为 LocalRouter。

外部服务继续拥有登录状态和账号生命周期。LocalRouter 不复制这些状态，也不会在请求路径里执行人工授权。该配置只存在于本机 XDG 目录，不属于公开发行版的支持清单。

## Agent 如何使用和维护

普通消费者不需要文件系统权限：

1. 请求 `/.well-known/localrouter.json`；
2. 沿返回链接读取 Manifest、OpenAPI、示例和 Markdown 指南；
3. 通过 `/mcp` 的 `tools/list` 发现所有已发布且就绪的操作；
4. 使用 API Token 调用 `/v1`、`/p`、`/w` 或 `/mcp`。

发现结果是可执行契约，不需要 Agent 拼路径。`operation_key`/`operation_id` 是供 catalog、describe、preflight、`lr` 和 MCP 使用的语义选择标识；它们不是 URL。直接发 HTTP 时必须使用每个 operation 已解析好的 `call_url` 和 `call.methods`。例如 `operation_id=chat.completions` 可以对应 `call_url=/p/provider/chat/completions`，不能写成 `/p/provider/chat.completions`。Manifest、示例、Agent descriptor 与 OpenAPI 都发布同一个 `call_url`，doctor 会把不一致判为失败。`request_example` 只表示请求形状；当 operation 发布 `dynamic_inputs` 时，先调用其中指向的模型/资源目录并使用当前返回值，不能把示例模型当成可用性证明。

### `lr`：给本机 Agent 的快速入口

仓库提供 [`tools/lr`](tools/lr)，把 discovery、文档定位、operation 搜索、鉴权和 MCP 调用收敛成一个轻量 CLI。服务命令只从 `0600` 文件读取 API Token；维护命令默认读取独立的管理密钥。任何凭据都不会被导出或打印。

```bash
lr status
lr find '适合长任务的文本模型'
lr find operation '聊天补全'
lr find model 'operator-model'
lr find pool media-worker
lr catalog
lr catalog search-primary
lr resolve web.search
lr compare search-primary.query search-backup.query
lr describe search-primary query
lr call <pack> chat.completions '{"model":"example","messages":[]}'
lr preflight search-primary query '{"query":"LocalRouter","count":5}'
lr preflight media-worker asset.download '{}' '{"task_id":"task-example"}' '{"project_id":"project-example"}'
lr run search-primary query '{"query":"LocalRouter","count":5}'
lr watch <pack> <workflow> <job-id>
lr whoami
lr mcp-list responses
lr request GET /v1/models
```

`lr describe` 直接返回所选 operation 的顶层合同（不是 HTTP API 的 `.data` 包装），因此 Agent 可以直接读取 `operation_key`、`call_url`、`request_schema`、`dynamic_inputs`、`pricing`、`pool` 与 `verification`。`request_schema` 的必填项是调用约束，不因供应商免 Key 或免费而放宽；免 Key 只影响上游鉴权。

先分清搜索对象：operation 是可调用契约，model 是就绪 Pack 从上游实时列出的模型 ID，pool 是号池 readiness、额度与价格，OMP/其他 Agent runtime 的模型分配则属于外部运行时。`lr find <混合意图>` 会把前三类分栏返回；已知类别时直接使用 `lr find operation|model|pool`，避免把模型名或运行时配置误当 operation。模型结果使用 `<pack>:<model-id>` 保留供应商身份，不跨供应商合并，并通过 `compatible_operations` 列出确实声明会消费该模型目录的操作；这条关系来自 Pack 的 `dynamic_inputs`，不靠 LocalRouter 猜它是不是聊天、图片、视频或其他 API。

推荐调用顺序是 `find/catalog/resolve → compare（有多个候选时）→ Agent 明确选择 operation_key 与 model_key → describe → preflight → run`。`catalog` 返回 Token 允许访问的全部独立 operation；`resolve` 只解析 operation，并返回所有精确匹配（没有精确匹配时才做宽松文本匹配），默认同时保留 ready 与 unavailable 项；`compare` 按调用者给定顺序返回完整独立合同与 verification 层级，永远不输出推荐项。LocalRouter 不合并供应商、不隐藏差异，也不替 Agent 选择某一家。每项分别公开 Pack、号池、readiness、capabilities、aliases、请求/响应 schema、价格、重试、指南、工作流和调用路径。`preflight` 检查鉴权、模型策略、Pack/号池、operation、必需 path/query parameters、输入 schema、价格状态和副作用，但不会访问上游或消费额度。第五个参数是 Query JSON；MCP 和 OpenAPI 也会把必需 Query 公开为机器约束。已知固定接口时可直接 `call` 或 `request`。异步任务使用 `run → watch`，`watch` 默认不设超时，Agent 中断后可凭同一个 Job ID 恢复；只有明确需要时才传超时秒数或调用 `cancel`。

发现文档顶层的 `contract.digest` 与 `/docs/index.json` 的 `contract_digest` 是同一份 Pack 契约摘要。Agent 可以缓存 operation 描述，但 `contract.digest` 或 `contract.schema_version` 任一变化都必须重新读取。调用失败时优先消费统一的 `code`、`reason`、`retryable`、`retry_after`、`next_action` 和 `alternatives`，不要从自然语言猜测是否重试。

普通 Token 默认长期有效、无分钟/每日/并发调用上限；实际资源安全由每个 Pack 的账号池并发、租约、冷却、健康和额度资格控制。它始终只用于调用，不能修改 LocalRouter。

维护默认走管理密钥，可由控制台或人工 `lr manage-*` 使用。控制台中的“Agent 维护令牌”开关默认关闭。只有人工打开它并把一枚独立 Token 标记为“维护专用”后，该 Token 才能访问 `/manage/mcp`；维护 Token 不能调用模型、Protocol Pack 或工作流，也不会得到控制台 Token 管理权或池 Secret。关闭开关会立即停用全部 Agent 维护 Token。

维护 Agent 应先加载 [LocalRouter Protocol Pack Skill](.agents/skills/localrouter-protocol-pack/SKILL.md)，再使用语义化工具：

```text
discover → open draft → put Pack / operation / supplier profile → lint
         → write guide / advanced patch → review impact
         → plan reviewed digest → apply exact digest → verify / rollback
```

Agent 不再拼文件路径、JSON 缩进、YAML front matter 或发布请求。Pack core、单个 operation、单个供应商请求 profile 和 guide 都有各自的强类型工具；无 Key 服务省略 `auth` 时会明确生成 `auth.type=none`，operation 会生成安全的 transport/retry/draft-availability 缺省值。`lint` 与 review 失败返回 `issues[].path`、允许值和修复动作。LocalRouter 负责格式、原子写入、严格校验和 digest；发布后会重新读取线上 Pack 树，本地验证失败时默认恢复上一修订并保留草稿。

人工维护终端可以直接使用；授权给 Agent 时还必须显式设置其 `LOCALROUTER_MAINTAINER_TOKEN_FILE`：

```bash
lr manage-list
lr manage-call localrouter_draft_open '{"draft_id":"agent-change"}'
lr manage-call localrouter_draft_put_pack '{"draft_id":"agent-change","pack_id":"publicapi","name":"Public API","description":"No-key API","base_url":"https://provider.example/v1"}'
lr manage-call localrouter_draft_put_operation '{"draft_id":"agent-change","pack_id":"publicapi","operation":{"operation_id":"models","methods":["GET"],"path":"/models","summary":"List models"}}'
lr manage-call localrouter_draft_lint_pack '{"draft_id":"agent-change","pack_id":"publicapi"}'
lr manage-call localrouter_draft_review '{"draft_id":"agent-change"}'
```

人工控制台 `/#control` 与维护工具读取同一份草稿、影响范围和 digest。Provider 凭据、账号内容、私有上游和 locator 不会通过维护工具返回。

仓库侧辅助命令：

```bash
./tools/protocol-pack-lifecycle.sh validate
./tools/protocol-pack-lifecycle.sh plan
./tools/protocol-pack-lifecycle.sh apply <reviewed-digest>
./tools/protocol-pack-lifecycle.sh history
./tools/protocol-pack-lifecycle.sh rollback <known-digest>
```

## 配置

安装器在 `$XDG_CONFIG_HOME/localrouter/config.env` 创建配置。可参考
[`packaging/localrouter.env.example`](packaging/localrouter.env.example) 调整端口或目录；
`LOCAL_GATEWAY_HOST` 只接受回环地址。源码目录中的 `gateway/.env` 仅作为开发运行入口。

| 服务端变量 | 默认值 |
|---|---|
| `LOCAL_GATEWAY_HOST` | `127.0.0.1`，只接受回环地址 |
| `LOCAL_GATEWAY_PORT` | `8317` |
| `LOCAL_GATEWAY_CONFIG_DIR` | `$XDG_CONFIG_HOME/localrouter` |
| `LOCAL_GATEWAY_DATA_DIR` | `$XDG_DATA_HOME/localrouter` |
| `LOCAL_GATEWAY_STATE_DIR` | `$XDG_STATE_HOME/localrouter` |
| `LOCAL_GATEWAY_CACHE_DIR` | `$XDG_CACHE_HOME/localrouter` |
| `LOCAL_GATEWAY_PROTOCOL_DIR` | `$XDG_CONFIG_HOME/localrouter/protocols` |
| `LOCAL_GATEWAY_CHANNEL_PROFILES_FILE` | `$XDG_CONFIG_HOME/localrouter/channel-profiles.json`；模型兼容入口的协议、鉴权和模型目录配置 |

模型渠道不在核心中判断供应商名称。`channel-profiles.json` 的每个 Profile 独立声明：本机兼容请求路径如何归类、默认 `base_url`、密钥放入哪个 header 或 query parameter、固定协议头，以及如何从上游模型目录提取 ID。安装时只在文件不存在时写入通用协议族 Profile；之后人工或 Agent 修改配置不会被升级覆盖。非标准登录、浏览器会话或多阶段签名可由操作者自行部署固定 loopback `http-envelope` sidecar，核心只看到统一 envelope；公开发行版不附带任何供应商 sidecar。

登录密钥不放在环境文件中；请通过控制台安全轮换。Provider 凭据、Cookie
和池内容必须留在 `$XDG_DATA_HOME/localrouter` 或外部权威池中。

本机 Agent 可使用下列客户端环境变量；只配置 URL 和文件定位，不配置 Token 值：

| 变量 | 默认值/用途 |
|---|---|
| `LOCALROUTER_BASE_URL` | `http://127.0.0.1:8317` |
| `LOCALROUTER_DISCOVERY_URL` | `/.well-known/localrouter.json` 的完整地址 |
| `LOCALROUTER_DOCS_URL` | 文档首页 |
| `LOCALROUTER_OPENAPI_URL` | 汇总 OpenAPI |
| `LOCALROUTER_MCP_URL` | MCP JSON-RPC 入口 |
| `LOCALROUTER_MAINTENANCE_MCP_URL` | 维护 MCP，默认 `http://127.0.0.1:8317/manage/mcp` |
| `LOCALROUTER_API_TOKEN_FILE` | 默认 `$XDG_DATA_HOME/localrouter/api-token` |
| `LOCALROUTER_MAINTAINER_TOKEN_FILE` | 可选；显式指定已授权 Agent 的维护专用 Token locator；无默认值 |
| `LOCALROUTER_ADMIN_TOKEN_FILE` | 默认 `$XDG_DATA_HOME/localrouter/admin-token`；供控制台和人工 `lr manage-*` 使用，不交给 Agent |

`lr` 会拒绝非回环 Base URL、符号链接 Token 文件、错误所有者以及非 `0600` 权限，避免环境变量被篡改后把 Token 发往外部地址。

已有仓库内运行数据可在安装时非破坏性复制并拆分到 XDG 目录；原目录不会删除：

```bash
./tools/install-localrouter.sh install --migrate-from ./gateway/data
```

## 开发与验证

```bash
make -C gateway web-test
go -C gateway test ./...
go -C gateway vet ./...
./tests/verify.sh
```

发布前运行完全隔离的洁净发行门禁：

```bash
./tests/clean_release_acceptance.sh
```

它会从当前 Git 索引重建无本机状态的副本，执行前端构建、Go 验证、协议验收、Skill 测试、Gitleaks、govulncheck 和 OSV。真实 Provider、真实号池和付费调用只在显式配置并取得授权后运行。

更深入的资料：

- [网关运行与接口](gateway/README.md)
- [Protocol Pack v3](docs/PROTOCOL-PACK-V3.md)
- [协议与号池架构](docs/PROTOCOL-ARCHITECTURE.md)
- [开源发行门禁](docs/OPEN_SOURCE_RELEASE.md)
- [历史来源与独立边界](PROVENANCE.md)
- [贡献指南](CONTRIBUTING.md)
- [安全策略](SECURITY.md)

## 边界与许可证

LocalRouter 的早期实现参考并复用了 QuantumNous New API；当前运行时已无其源码或模块依赖，但这不是 clean-room 重写，历史归属见 [PROVENANCE.md](PROVENANCE.md)。本项目继续采用 [AGPL-3.0](LICENSE)，直接依赖许可证见 [THIRD-PARTY-LICENSES.md](THIRD-PARTY-LICENSES.md)。

注册、CAPTCHA、人工 OAuth 同意、支付、反机器人挑战和账号生产不属于 LocalRouter 的请求运行面。

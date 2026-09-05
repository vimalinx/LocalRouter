<div align="center">

<img src="gateway/web-src/public/assets/localrouter.svg" alt="LocalRouter 项目标志" width="88" height="88">

# LocalRouter

**在自己的机器上管理模型 API、服务接口和 Agent 调用。**

LocalRouter 提供一个本机网关，把上游连接、访问凭据和调用记录集中管理。应用通过固定地址访问服务，Agent 则可以先查询有哪些能力，再选择具体操作。

![License](https://img.shields.io/badge/license-AGPL--3.0-6b7280)
![Go](https://img.shields.io/badge/Go-1.25.13%2B-64748b)
![React](https://img.shields.io/badge/React-19-64748b)
![Runtime](https://img.shields.io/badge/runtime-single_binary-7c5ce7)

[安装](#安装与首次使用) · [控制台](#在控制台里管理服务) · [Agent 接入](#让-agent-发现并调用服务) · [局域网](#让局域网设备调用) · [开发](#开发与验证)

[GitHub](https://github.com/vimalinx/LocalRouter) · [下载发行版](https://github.com/vimalinx/LocalRouter/releases) · [变更记录](CHANGELOG.md)

</div>

## 它解决什么问题

当几个应用或 Agent 同时使用多种 API 时，每个客户端都要保存上游地址和密钥，调用记录也容易分散。LocalRouter 把这些配置放到网关里。客户端使用自己的 Service Token，网关根据已经配置好的渠道或 Protocol Pack 转发请求，并记录用量。

普通模型接口可以直接配置渠道。服务需要特殊鉴权、请求转换或异步任务时，可以用 Protocol Pack 描述它的调用规则。每个 Pack 保留自己的操作、模型目录、价格和账号池，Agent 能看到这些差异，并明确选择要用哪一个。

主程序由 Go 编写，内嵌 React 控制台，使用 SQLite 保存渠道、Token 和模型请求日志。构建完成后可作为单个程序运行，默认地址为 `127.0.0.1:8317`。需要给其他设备调用时，可以另开受 Token 保护的局域网端口。

公开发行包包含通用网关、工具和协议 Schema。**新安装没有供应商账号，也没有可直接使用的供应商 Pack。** 需要先配置你有权使用的上游服务。账号注册、登录授权、验证码和付款由用户或上游服务处理。

本文说明当前源码的功能。使用发行包时，请以对应版本的[发布记录](https://github.com/vimalinx/LocalRouter/releases)为准。

## 安装与首次使用

### 从源码安装

准备 Linux、Go 1.25.13 或更新版本、Bun 和 Git。默认安装方式使用 systemd 用户服务。

```bash
git clone https://github.com/vimalinx/LocalRouter.git
cd LocalRouter
./tools/install-localrouter.sh install
```

安装完成后打开 [本机控制台](http://127.0.0.1:8317/)。接口说明位于 [本机文档页](http://127.0.0.1:8317/docs)。

安装器会把程序和 `lr` 命令放入 `~/.local/bin`，创建并启动 `localrouter.service`。如果终端找不到命令，请确认 `~/.local/bin` 已加入 `PATH`。

### 使用发行包

从 [Releases](https://github.com/vimalinx/LocalRouter/releases) 下载与你的 Linux 架构对应的压缩包，解压后在包内执行相同的安装命令。

```bash
./tools/install-localrouter.sh install
```

发行包提供预编译程序，安装时无需 Go 或 Bun。仅安装、不启动服务可加 `--no-start`；自行管理进程时可用 `--no-systemd` 跳过 systemd 配置，再运行 `localrouter`。

Docker 部署和持久化卷的配置见 [Docker 与 LAN 部署指南](docs/DOCKER.md)。

### 配好第一个调用入口

1. 先确认上游服务能正常使用，并准备它要求的地址、模型与凭据。
2. 打开控制台的“服务与渠道”。标准模型 API 在“模型渠道”中添加；需要自定义协议的服务通过 Protocol Pack 配置并发布。
3. 在“Agent 工作台”登记调用方，填写唯一编码、名称与工作区，取得它自己的 Service Token。
4. 按已发布的接口文档配置客户端。检查服务可用状态和模型，再发起请求。

对于 OpenAI 兼容的模型渠道，客户端通常使用 `http://127.0.0.1:8317/v1`。独立 Pack 的地址要从发现文档取得；符合 OpenAI 模型与聊天契约的 Pack，可以用 `lr runtime-openai` 查询客户端应使用的 Base URL。

<details>
<summary>安装器对 Agent 配置的改动</summary>

安装器会把 `localrouter-protocol-pack` Skill 安装到 `~/.agents/skills/` 和 `~/.omp/agent/skills/`，并向以下文件写入标明 LocalRouter 所有权的指令块。

- `~/.agents/AGENTS.md`
- `~/.codex/AGENTS.md`
- `~/.omp/agent/AGENTS.md`

这些指令说明如何发现服务、读取 Token 文件，以及如何维护 Pack。安装器遇到不属于 LocalRouter 的同名 Skill，或无法识别的损坏指令块时会停止，避免覆盖原有内容。

卸载命令会移除本项目安装的程序、服务、Skill 和指令块，保留用户配置、凭据、数据库及运行状态。

```bash
./tools/install-localrouter.sh uninstall
```

</details>

## 在控制台里管理服务

### 运行概览

<a href="docs/images/localrouter-overview.jpg">
  <img src="docs/images/localrouter-overview.jpg" alt="运行概览页面，包含请求趋势、用量统计和服务状态">
</a>

概览用于查看近期调用、Token 用量、已知成本和服务状态，也提供控制台密码及版本检查入口。统计会区分已知价格和缺失价格，无法计价的调用会保留未知或部分计价状态。

### 服务与渠道

<a href="docs/images/localrouter-services.jpg">
  <img src="docs/images/localrouter-services.jpg" alt="服务管理页面，展示账号池、可用状态和价格信息">
</a>

这里分别管理模型渠道与 Protocol Pack。选择一个服务后，可以查看账号明细、额度信息和已发布的操作。服务及操作的启用开关会立即影响调度，开关状态单独保存，不改写 Pack 草稿或发布摘要。

需要修改协议时，从这个页面进入协议编辑。控制台与维护工具使用同一份草稿，发布前能够看到影响范围。

### Agent 工作台

<a href="docs/images/localrouter-agent-workbench.jpg">
  <img src="docs/images/localrouter-agent-workbench.jpg" alt="Agent 工作台，按调用身份展示工作区、用量与额度">
</a>

每个 Agent 使用独立编码和 Token，便于按身份查看调用量、输入输出 Token、成功率及费用。可分别设置每分钟请求数、每日请求数、并发数和过期时间。开启请求限额后，计数会保存到磁盘，重启不会清零；每日限额按 UTC 日期重置。

除系统初始化 Token 外，服务 Token 需要完整的 Agent 编码与工作区资料。旧 Token 缺少这些资料时，可以在工作台补齐。只有主动使用显示或快速接入功能时，页面才读取相应 Token；密钥不写入浏览器持久存储。

以上截图取自隔离演示环境，其中的服务、模型和 Agent 均为虚构数据。

### 任务与日志

“任务与事件”提供工作流状态、结果和错误详情。支持取消的任务会显示取消入口，确认后可以继续查看实际返回的状态。“请求日志”支持翻页，渠道和 Agent 列表会加载后续页的数据。

控制台分别加载各块数据。某个接口失败时会显示对应提示，其他已经取得数据的页面仍可使用。

## 支持哪些接入方式

| 接入需求 | 配置方式与行为 |
|---|---|
| 标准模型 API | 通过模型渠道使用 OpenAI 兼容接口、Anthropic Messages 或 Gemini 原生接口 |
| 自定义 HTTP 服务 | 在 Pack 中声明方法、路径、查询参数、鉴权及请求响应转换 |
| 文件与流式响应 | 支持 multipart、二进制文件和 SSE；不需要转换的内容按字节转发 |
| 双向通信 | 支持 WebSocket 和原始 gRPC 转发 |
| 特殊传输 | 通过固定回环地址的 adapter 或受限 WASM adapter 接入 |
| 长时间任务 | 用工作流描述创建、轮询、回调、结果提取和取消 |
| 多枚供应商凭据 | 由账号池处理轮换、冷却、并发租约、健康检查和额度资格 |
| 已有外部网关 | 保留其账号池与登录状态，由 LocalRouter 调用配置好的固定地址 |

标准模型渠道的路径归属、鉴权位置和模型目录解析由 `channel-profiles.json` 定义。普通协议差异可以通过配置表达。独立路径、特殊认证、转换、专用号池或工作流则使用完整的 Protocol Pack。

同模型的兼容渠道可以按优先级和权重选路。独立 Pack 之间始终保留供应商身份；共享能力标签只方便搜索，不会把不同供应商的价格、模型或账号池合并。

账号池有三种所有权模式。`local` 由 LocalRouter 调度本地凭据；`external` 由外部网关管理；`external-readonly` 允许读取外部维护者写入的私有池文件。登录、凭据刷新和账号生命周期仍由配置中指定的一方负责。

转发遇到不确定结果时，需要先查清上游状态。兼容入口不会在 POST 遇到连接错误或上游 5xx 后自动换渠道重放，携带供应商凭据的请求也不会自动跟随重定向。Pack 的重试规则应依据实际幂等性配置。

工作流在发起请求前保存执行记录，网络等待不会占住其他任务的状态锁。进程中断留下的不确定执行会标为 `outcome_unknown`，避免恢复时重复提交；取消步骤有独立的次数预算。具体终态与取消能力仍取决于该工作流的定义。

## 让 Agent 发现并调用服务

推荐从 `lr` 开始。消费型 Agent 可以仅通过端口获取能力说明，无需读取仓库文件。

```bash
lr status
lr tree
lr find operation 'web.search'
lr find model 'model-name'
lr find pool 'pack-name'
```

operation、model 和 pool 分别回答“能做什么”“可以用哪个模型”和“哪个池可用”。查询结果保留所属 Pack，目录和模型查询默认最多显示 20 项。优先缩小查询范围，确实需要完整目录时再加 `--all`。

选定服务后，用实际的 Pack ID、operation ID 和模型 ID 替换下面的占位值。这些命令用于查看契约和精确匹配模型。

```bash
pack='your-pack'
operation='your-operation'
model='exact-model-id'

lr show "$pack"
lr describe "$pack" "$operation"
lr find model --exact "$pack:$model"
```

调用前确认 `ready=true`，并查看 schema、价格、验证层级和重试规则。模型必须得到一个精确匹配结果。上游没有模型目录时，可以使用当前已审阅请求 schema 明确列出的枚举；示例里的模型名只用于展示格式。

有多个候选操作时，`lr compare` 可以并排返回各自契约，选择由调用方作出。付费或会改变上游状态的请求，需要先明确操作与动态输入，再运行 `lr preflight`。预检不会访问上游，也不能代替实时模型发现；非零退出时应处理返回的 `code`、`next_action` 和 `alternatives`，不要继续调用。

获得授权并通过预检后，使用 `lr call` 或 `lr run` 执行。一次真实请求应先保存原始响应和退出状态，再解析结果；显示或解析失败不应导致重复提交。

`operation_id` 和 `operation_key` 用来标识操作。它们不直接决定 URL，例如 `chat.completions` 不一定对应带点号的路径。优先让 `lr` 或 MCP 处理地址，直接发 HTTP 时使用契约公布的 `call_url`。已经单独指定 Pack 的 `lr` 命令，也接受完整的 `<pack>.<operation_id>`。

长任务保留 Pack、workflow 和 Job ID，随后可继续观察同一个任务。`watch` 默认一直等待；只有工作流声明支持取消时，才执行 `lr cancel "$pack" "$workflow" "$job_id"`。

```bash
workflow='your-workflow'
job_id='returned-job-id'
lr watch "$pack" "$workflow" "$job_id"
```

接入 OpenAI 兼容客户端时，可以查询 Pack 发布的地址。

```bash
lr runtime-openai "$pack" "$model"
```

保留它返回的 `/p/<pack>/v1` Base URL，并使用已确认的精确模型。其他 Agent 运行时的模型配置由相应客户端管理。

发现文档中的 `contract.digest` 和 `contract.schema_version` 可用于判断操作契约缓存是否过期；服务树另有 `topology.digest`。摘要或版本变化后应重新发现。

## 控制台、调用和维护权限

| 用途 | 使用的凭据 |
|---|---|
| 应用与 Agent 调用服务 | 各自的 Service Token，默认长期有效且不限调用次数，可另配额度策略 |
| 人工使用控制台 | 默认仅本机免密访问，也可启用自定义密码 |
| 人工使用维护 MCP | 独立的管理员凭据，始终需要鉴权 |
| Agent 修改 Pack | 人工明确授予的维护权限，默认关闭；使用带 `localrouter.maintain` 的独立维护 Token |

调用 Token 不授予管理权限，维护 Token 也不能拿来调用模型或工作流。服务 Token 自身不限量时，仍要遵守账号池的并发、冷却、租约与额度限制。

在“运行概览”中可以开启或关闭控制台密码保护。密码长度为 16 至 512 个可打印 ASCII 字符，允许中间空格，不接受首尾空白。修改立即生效，无需重启，也不会改变已发给应用的 Service Token。

控制台密码与人工维护 MCP 使用同一份管理员凭据。密码开关只控制 `/local/api` 是否要求它，不能解除 `/manage/mcp` 的鉴权。免密控制台也不代表用户已经授权 Agent 修改配置。

需要 Agent 维护时，先加载 [Protocol Pack Skill](.agents/skills/localrouter-protocol-pack/SKILL.md)。维护工具按草稿操作，先修改 Pack 或指南，再审阅受影响的文件、协议与池，最后规划并应用同一个已审阅摘要。草稿内容变化后，旧的发布计划失效。

LocalRouter 负责校验、原子写入和版本记录。发布后的本地校验失败时，会尝试恢复上一修订并保留草稿。Go 或 WebUI 的修改需要另外构建、重启和验收，Pack 回滚不会撤销已提交给供应商的任务。

## 让局域网设备调用

控制台和维护接口固定留在本机回环地址。启用额外的 LAN 监听器后，其他设备可以使用服务 API 和脱敏文档。

在 `config.env` 中填写当前机器实际拥有的私有地址，再重启服务。例如下面的地址仅为配置示意。

```dotenv
LOCAL_GATEWAY_LAN_ENABLED=true
LOCAL_GATEWAY_LAN_HOST=192.168.1.10
LOCAL_GATEWAY_LAN_PORT=8318
```

LAN 监听器提供 `/v1`、`/v1beta`、`/p`、`/w`、`/mcp`、`/agent` 及公开发现文档，不注册控制台、`/local/status`、`/local/api` 或 `/manage/mcp`。为每个调用方分配独立 Service Token，并用主机防火墙限制可访问的网段。浏览器调用还需配置准确的 `LOCAL_GATEWAY_LAN_ALLOWED_ORIGINS`。

远端使用 `lr` 时，必须由操作者批准地址，并设置 `LOCALROUTER_ALLOW_LAN=true`。`lr` 会先检查发现结果是否为 `scope=lan-service`、维护接口是否不可用，确认后才读取并发送 Service Token。LAN 地址不能用于维护。

## 配置和数据放在哪里

程序按 XDG 目录规则保存配置与状态。没有自定义 XDG 环境变量时，使用下列位置。

| 默认目录 | 保存内容 |
|---|---|
| `~/.config/localrouter` | `config.env`、Channel Profiles 和 Protocol Packs |
| `~/.local/share/localrouter` | SQLite 数据库、Token、供应商凭据、池配置和限额计数 |
| `~/.local/state/localrouter` | 调用事件、工作流状态、草稿与发布历史 |
| `~/.cache/localrouter` | 可以重建的缓存 |

私有目录权限为 `0700`，凭据、数据库及私有状态文件为 `0600`。`localrouter paths` 会显示本机解析后的路径，不输出密钥内容。

主要服务配置可从 [`packaging/localrouter.env.example`](packaging/localrouter.env.example) 查阅。

| 变量 | 默认值或用途 |
|---|---|
| `LOCAL_GATEWAY_HOST` | `127.0.0.1`，仅接受回环地址 |
| `LOCAL_GATEWAY_PORT` | `8317` |
| `LOCAL_GATEWAY_UPDATE_CHECK_ENABLED` | `true`，启动后及每六小时检查公开 GitHub Release |
| `LOCAL_GATEWAY_CONFIG_DIR` | 覆盖配置目录 |
| `LOCAL_GATEWAY_DATA_DIR` | 覆盖私有数据目录 |
| `LOCAL_GATEWAY_STATE_DIR` | 覆盖运行状态目录 |
| `LOCAL_GATEWAY_CACHE_DIR` | 覆盖缓存目录 |
| `LOCAL_GATEWAY_PROTOCOL_DIR` | 覆盖 Pack 目录，默认为配置目录中的 `protocols` |
| `LOCAL_GATEWAY_CHANNEL_PROFILES_FILE` | 覆盖模型协议配置文件位置 |

版本检查只显示更新提示，不下载或安装程序，也不读取 GitHub Token。正式版本只跟踪正式发行，预发布版本同时跟踪预发布与正式发行；请求失败不影响转发。离线运行可关闭 `LOCAL_GATEWAY_UPDATE_CHECK_ENABLED`。

`lr` 的客户端配置使用 `LOCALROUTER_BASE_URL` 指定入口，默认是 `http://127.0.0.1:8317`。`LOCALROUTER_API_TOKEN_FILE` 指向权限为 `0600` 的 Token 文件，默认读取 XDG 数据目录中的 `api-token`。这个变量保存文件路径，不能填入密钥本身。

显式授权的维护 Agent 使用 `LOCALROUTER_MAINTAINER_TOKEN_FILE` 指定维护 Token 文件，该变量没有默认值。`LOCALROUTER_ADMIN_TOKEN_FILE` 留给人工管理使用。凭据、Cookie 和池内容应保存在私有数据目录或外部维护者管理的位置，不写入源码、文档或日志。

如果仍使用旧版仓库内的数据目录，可以在安装时复制迁移。目标已有同名内容时安装器会停止，原目录会保留。

```bash
./tools/install-localrouter.sh install --migrate-from ./gateway/data
```

## 常用本机地址

| 入口 | 地址 |
|---|---|
| 控制台 | `http://127.0.0.1:8317/` |
| 健康检查 | `http://127.0.0.1:8317/healthz` |
| Agent 发现 | `http://127.0.0.1:8317/.well-known/localrouter.json` |
| 接口文档 | `http://127.0.0.1:8317/docs` |
| 机器文档索引 | `http://127.0.0.1:8317/docs/index.json` |
| 汇总 OpenAPI | `http://127.0.0.1:8317/docs/openapi.json` |
| 号池目录 | `http://127.0.0.1:8317/docs/pools/index.json` |
| 服务 MCP | `POST http://127.0.0.1:8317/mcp` |
| 维护 MCP | `POST http://127.0.0.1:8317/manage/mcp` |

旧地址 `/doc` 会永久重定向到 `/docs`。

## 开发与验证

前端位于 `gateway/web-src`，构建产物写入 `gateway/web` 并嵌入 Go 程序。修改界面后，需要重新构建程序才能更新独立运行的网关。

```bash
make build
make -C gateway web-test
go -C gateway test ./...
go -C gateway vet ./...
./tests/verify.sh
```

`verify.sh` 检查前端、Go、竞态、协议、LAN 隔离和独立安装等环节。Docker 的验收单独执行。

```bash
make docker-test
```

准备发行版本时，运行洁净副本验收。

```bash
./tests/clean_release_acceptance.sh
```

该流程还会检查密钥泄露、依赖漏洞和发行包。测试中的供应商服务与凭据均为隔离夹具。真实供应商调用、流式终态和付费任务需要另外授权、另外验证，不能从本地测试通过推断其结果。

更详细的配置与贡献说明见以下文档。

- [网关运行和接口说明](gateway/README.md)
- [Protocol Pack v3 参考](docs/PROTOCOL-PACK-V3.md)
- [协议与账号池架构](docs/PROTOCOL-ARCHITECTURE.md)
- [Docker 与局域网部署](docs/DOCKER.md)
- [开源发行流程](docs/OPEN_SOURCE_RELEASE.md)
- [参与开发](CONTRIBUTING.md)
- [报告安全问题](SECURITY.md)

## 项目来源与许可证

早期版本参考并复用了 QuantumNous New API。当前运行时不再依赖其源码或 Go 模块，项目仍保留这段开发历史及相应归属说明，不作 clean-room 重写声明。详细记录见 [PROVENANCE.md](PROVENANCE.md)。

LocalRouter 使用 [AGPL-3.0](LICENSE) 许可证。直接依赖及其许可证列在 [THIRD-PARTY-LICENSES.md](THIRD-PARTY-LICENSES.md)，其他归属信息见 [NOTICE](NOTICE)。

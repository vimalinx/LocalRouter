# 配置、权限与维护

[返回 README](../README.md) · [Agent 调用指南](AGENT-USAGE.md) · [Docker 部署](DOCKER.md)

本文保留日常安装、更新、权限和数据迁移的详细说明。调用 Token 指 Service Token，供应用和 Agent 调用服务；管理员凭据与维护 Token 的用途见下表。

## 控制台、调用和维护权限

| 用途 | 使用的凭据 |
|---|---|
| 应用与 Agent 调用服务 | 各自的调用 Token，默认长期有效且不限调用次数，可另配额度策略 |
| 人工使用控制台 | 默认仅本机免密访问，也可启用自定义密码 |
| 人工使用维护 MCP | 独立的管理员凭据，始终需要鉴权 |
| Agent 修改 Pack | 人工明确授予的维护权限，默认关闭；使用带 `localrouter.maintain` 的独立维护 Token |

调用 Token 不授予管理权限，维护 Token 也不能拿来调用模型或工作流。调用 Token 自身不限量时，仍要遵守账号池的并发、冷却、租约与额度限制。

在“运行概览”中可以开启或关闭控制台密码保护。密码长度为 16 至 512 个可打印 ASCII 字符，允许中间空格，不接受首尾空白。修改立即生效，无需重启，也不会改变已发给应用的调用 Token。

控制台密码与人工维护 MCP 使用同一份管理员凭据。密码开关只控制 `/local/api` 是否要求它，不能解除 `/manage/mcp` 的鉴权。免密控制台也不代表用户已经授权 Agent 修改配置。

需要 Agent 维护时，先加载 [Protocol Pack Skill](../.agents/skills/localrouter-protocol-pack/SKILL.md)。维护工具按草稿操作，先修改 Pack 或指南，再审阅受影响的文件、协议与池，最后规划并应用同一个已审阅摘要。草稿内容变化后，旧的发布计划失效。

LocalRouter 负责校验、原子写入和版本记录。发布后的本地校验失败时，会尝试恢复上一修订并保留草稿。Go 或 WebUI 的修改需要另外构建、重启和验收，Pack 回滚不会撤销已提交给供应商的任务。

## 安装器对 Agent 配置的改动

安装器会把 `localrouter-protocol-pack` Skill 安装到 `~/.agents/skills/` 和 `~/.omp/agent/skills/`，并向以下文件写入标明 LocalRouter 所有权的指令块。

- `~/.agents/AGENTS.md`
- `~/.codex/AGENTS.md`
- `~/.omp/agent/AGENTS.md`

这些指令说明如何发现服务、读取 Token 文件，以及如何维护 Pack。安装器遇到不属于 LocalRouter 的同名 Skill，或无法识别的损坏指令块时会停止，避免覆盖原有内容。

卸载命令会移除本项目安装的程序、服务、Skill 和指令块，保留用户配置、凭据、数据库及运行状态。

```bash
./tools/install-localrouter.sh uninstall
```

## 更新程序

控制台遇到以下情况时，可以先在页面内恢复：

| 现象 | 处理方式 |
|---|---|
| 首次打开时无法连接 | 检查本机服务，再点击「重新连接」；连接失败不会要求输入管理密钥 |
| 显示「连接中断」或「数据不完整」 | 已有数据可能过期，恢复连接后点击顶部刷新；部分读取失败会明确提示 |
| 登录失效 | 页面清除已缓存的管理数据，再用当前管理密钥解锁 |
| 页面资源加载失败 | 切换其他页面，或点击「重新加载页面」读取最新资源 |
| 提交操作后没有收到结果 | 先刷新并核对已保存状态；供应商任务还需核对调用记录或状态查询结果，避免重复提交 |

控制台读取请求在 15 秒后超时；后台任务和修改请求不使用这个读取期限。页面可见时每 30 秒检查本机连接状态，这只确认控制台连接，不代表供应商服务可用。锁定控制台会取消未完成的管理数据读取并清理缓存。

更新前，先备份配置、数据与状态目录。采用停机备份时，先停止网关，再复制这些目录，避免备份到仍在写入的 SQLite 数据库。

下载所需版本并解压，在新的发行包目录执行安装命令。默认 systemd 安装需要随后重启服务，才能让正在运行的进程使用新程序。

```bash
./tools/install-localrouter.sh install
systemctl --user restart localrouter.service
systemctl --user status localrouter.service --no-pager
curl --fail http://127.0.0.1:8317/healthz
```

源码用户先更新到所需版本，再执行同样的安装与重启步骤。安装器保留已有配置，卸载也保留配置和数据。自行管理进程时，使用 `--no-systemd` 安装，并通过自己的进程管理方式停止旧进程、启动新进程。Docker 更新与迁移见 [部署指南](DOCKER.md)。

启动后的健康检查只确认本机进程能响应；还需在控制台检查渠道和账号池，并验证实际要使用的服务。

## 让局域网设备调用

控制台和维护接口固定留在本机回环地址。启用额外的 LAN 监听器后，其他设备可以使用服务 API 和脱敏文档。

在 `config.env` 中填写当前机器实际拥有的私有地址，再重启服务。例如下面的地址仅为配置示意。

```dotenv
LOCAL_GATEWAY_LAN_ENABLED=true
LOCAL_GATEWAY_LAN_HOST=192.168.1.10
LOCAL_GATEWAY_LAN_PORT=8318
```

LAN 监听器提供 `/v1`、`/v1beta`、`/p`、`/w`、`/mcp`、`/agent` 及公开发现文档，不注册控制台、`/local/status`、`/local/api` 或 `/manage/mcp`。为每个调用方分配独立调用 Token，并用主机防火墙限制可访问的网段。浏览器调用还需配置准确的 `LOCAL_GATEWAY_LAN_ALLOWED_ORIGINS`。

远端使用 `lr` 时，必须由操作者批准地址，并设置 `LOCALROUTER_ALLOW_LAN=true`。`lr` 会先检查发现结果是否为 `scope=lan-service`、维护接口是否不可用，确认后才读取并发送调用 Token。LAN 地址不能用于维护。

## 配置和数据放在哪里

程序按 XDG 目录规则保存配置与状态。没有自定义 XDG 环境变量时，使用下列位置。

| 默认目录 | 保存内容 |
|---|---|
| `~/.config/localrouter` | `config.env`、Channel Profiles 和 Protocol Packs |
| `~/.local/share/localrouter` | SQLite 数据库、Token、供应商凭据、池配置和限额计数 |
| `~/.local/state/localrouter` | 调用事件、工作流状态、草稿与发布历史 |
| `~/.cache/localrouter` | 可以重建的缓存 |

私有目录权限为 `0700`，凭据、数据库及私有状态文件为 `0600`。`localrouter paths` 会显示本机解析后的路径，不输出密钥内容。

主要服务配置可从 [`packaging/localrouter.env.example`](../packaging/localrouter.env.example) 查阅。

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
| `LOCAL_GATEWAY_DESKTOP_NOTIFICATIONS` | `false`（安装模板为 `true`），用 `notify-send` 弹出预算申请与自动接入通知，可直接同意、拒绝或撤销 |
| `LOCAL_GATEWAY_HUMAN_REQUEST_HOOK` | 空。绝对路径的可执行文件；需要人处理或已处理的事件以 JSON 写入其标准输入，30 秒超时，失败不影响请求 |

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
| 账号池目录 | `http://127.0.0.1:8317/docs/pools/index.json` |
| 服务 MCP | `POST http://127.0.0.1:8317/mcp` |
| 维护 MCP | `POST http://127.0.0.1:8317/manage/mcp` |

旧地址 `/doc` 会永久重定向到 `/docs`。

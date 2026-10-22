# Agent 发现与调用

[返回 README](../README.md) · [配置与权限](OPERATIONS.md) · [Protocol Pack v3](PROTOCOL-PACK-V3.md)

Agent 的使用契约只有一份：网关在 `/docs/agent-start.md` 提供，`lr guide` 读取它，源文件是 [gateway/agent-start.md](../gateway/agent-start.md)。机器可读部分在发现文档 `/.well-known/localrouter.json`。本页只说明三种接入方式各适合什么，具体命令、字段和失败处理以实时指南为准，这里不再重复。

| 场景 | 入口 | 说明 |
|---|---|---|
| 程序只需要调用 LLM | `/v1` + Service Token + 模型名 | 不带 `model` 时用默认模型；`GET /v1/models` 列出可用名称，也包括操作者放进 `/v1` 的 Pack 模型 |
| Agent 调用完整 Protocol Pack | `lr init` → `lr tree` / `lr find` → `lr exec` | 自动核对身份、精确模型和预检，只发送一次并保存结果 |
| 给程序一个独立身份 | `lr connect <program>` | 签发 0600 Token 文件并写好 `localrouter.toml`，从试用额度开始 |

默认模型、各服务首选模型和放进 `/v1` 的 Pack 由操作者在控制台“服务与渠道 → 默认模型”设置，保存在 `$XDG_CONFIG_HOME/localrouter/models.json`，见 [配置与权限](OPERATIONS.md#默认模型与首选模型)。

维护 Pack 需要独立的维护权限，流程见 [Protocol Pack Skill](../.agents/skills/localrouter-protocol-pack/SKILL.md)。LAN 调用见 [局域网配置](OPERATIONS.md#让局域网设备调用)。

# Agent 发现与调用指南

[返回 README](../README.md) · [配置与权限](OPERATIONS.md) · [Protocol Pack v3](PROTOCOL-PACK-V3.md)

Agent 通过 `lr` 从网关读取能力说明，无需访问仓库。本文的命令模板需要替换实际 Pack、操作和模型，公开发行包不预装可直接调用的供应商服务。

## 准备调用身份

先运行 `lr init` 和 `lr guide`。普通模式 `identity_required=false` 时，有效的现有调用 Token（包括默认系统 Token）即可使用，不要求额外登记。只有严格模式、独立授权或新服务接入提案才需要独立身份：`lr identity request` → 人在“访问权限”批准 → `lr identity claim` 自动保存并绑定凭据。已有独立 Token 可用 `lr identity bind`；`LOCALROUTER_API_TOKEN_FILE` 仅用于明确指定权限为 `0600` 的私有文件，变量中只填路径。

```bash
export LOCALROUTER_API_TOKEN_FILE='/absolute/private/path/service-token'
lr status
lr tree
lr whoami
```

默认网关地址为 `http://127.0.0.1:8317`。未指定 Token 文件时，`lr` 读取 XDG 数据目录下的 `localrouter/api-token`；使用独立 Agent 身份时应明确指定自己的文件。`lr whoami` 返回当前 Token 的脱敏身份与策略，不输出密钥。

## 先找能力，再选择模型

按查询对象选择命令，搜索词替换为自己的需求。

```bash
lr find operation 'web.search'
lr find model 'model-name'
lr find pool 'pack-name'
```

| 查询对象 | 返回的信息 |
|---|---|
| operation | 可调用的 Protocol Pack 操作及所属服务 |
| model | 当前可发现的模型 ID、所属 Pack 和兼容操作 |
| pool | 账号池的就绪状态、额度和价格信息 |

模型和目录查询默认最多显示 20 项，优先缩小范围，需要完整目录时再加 `--all`。`lr catalog <pack>` 列出独立操作，`lr resolve <capability>` 按能力标签找候选项。多个候选操作可用 `lr compare <pack.operation> <pack.operation>` 对照；LocalRouter 保留每个供应商的契约，选择由调用方作出。

先用 `lr show` 确认 Pack 类型。标准模型渠道对应轻量兼容 Pack，使用它公布的路径及 `lr request`；下文的 `lr describe`、`lr preflight` 和 `lr call` 适用于完整 Protocol Pack。

```bash
pack='your-pack'
operation='your-operation'
model='exact-model-id'

lr show "$pack"
lr describe "$pack" "$operation"
lr find model --exact "$pack:$model"
```

模型操作必须得到一个精确匹配结果，并与选定操作的 `compatible_operations` 对应。优先使用实时模型目录；上游没有目录时，只接受当前已审阅请求 schema 中明确列出的枚举。`model`、`model_cls` 和其他 `dynamic_inputs` 都需要按契约解析，`request_example` 仅展示请求形状。无需模型的操作可跳过模型查询。

调用前确认 `ready=true`，读取 schema、价格、验证层级、额度和重试规则。就绪状态、模型列表和余额各自说明一个条件，真实请求是否成功需要单独验证。

## 预检并只提交一次

下面是供聊天操作填写的 `request.json` 形状。请用已精确匹配的模型替换占位值，并按所选操作的 schema 调整字段。其他服务应使用其公布的请求格式。

```json
{
  "model": "exact-model-id",
  "messages": [{ "role": "user", "content": "请只回复 OK" }],
  "stream": false
}
```

付费或会改变上游状态的调用需要先明确所选 Pack、操作和动态输入，并取得相应授权。预检不调用上游，也不会刷新模型目录。非零退出时读取 `code`、`retryable`、`next_action` 和 `alternatives`，处理后再决定下一步。

日常使用 `lr exec` 自动检查精确模型和权限、执行预检，再发送一次请求。原始响应、退出状态和追踪 ID 自动保存到当前 Agent 的私有结果目录。

```bash
lr exec "$pack" "$operation" @request.json
lr result
```

需要路径或查询参数时，在 body 后依次传入两份 JSON：`lr exec <pack> <operation> <body-json> <path-json> <query-json>`。路径参数值必须为字符串；GET 的 body 为 `{}`。`lr preflight` 和 `lr call` 使用相同参数顺序，保留给需要分步控制的调用方。流式、文件、WebSocket 和 gRPC 操作仍须遵守各自的传输契约。

`lr result <call-id>` 查看保存的响应位置，`lr result <call-id> --refresh` 只读取同一调用的网关证据，不重发供应商请求。

原始响应是否成功，需要结合退出状态与响应体判断；聊天接口还应看到模型回复，异步接口应记录返回的任务 ID。显示或解析失败时，继续检查已保存的文件，不要因此重复提交。遇到 `outcome_unknown`，先核对上游状态。

## 操作名和 URL

`operation_id` 和 `operation_key` 都是语义标识。例如 `chat.completions` 可以对应 `/chat/completions` 路径。优先使用 `lr` 或 MCP，直接发 HTTP 时使用契约公布的 `call_url`。

已经单独指定 Pack 的 `lr` 命令，也接受完整的 `<pack>.<operation_id>`。必填路径参数、查询参数和请求体分别按 schema 提供，不要通过操作名猜测 URL。

## 接入模型客户端

标准模型渠道的客户端配置见 [README 的首次接入步骤](../README.md#配好第一个调用入口)。

完整 Protocol Pack 同时公布 `GET /models`、`POST /chat/completions` 和兼容 Base URL 时，可以使用以下命令获取运行时配置。

```bash
lr runtime-openai "$pack" "$model"
```

保留返回的 `/p/<pack>/v1` Base URL，使用已确认的精确模型和自己的调用 Token。此命令不会替外部客户端修改配置，也不适用于所有轻量兼容 Pack。

## 长任务与恢复

`lr run` 可以执行普通操作或启动持久工作流。创建成功后，保存 Pack、workflow 和 Job ID，后续始终观察同一个任务。

```bash
workflow='your-workflow'
job_id='returned-job-id'
lr watch "$pack" "$workflow" "$job_id"
```

`watch` 默认持续等待，也可退出后用相同 ID 恢复。只有工作流明确支持取消时，才执行 `lr cancel "$pack" "$workflow" "$job_id"`。取消请求发出后继续观察实际任务状态，不能把“已请求取消”当作上游已经取消。

工作流在请求前保存执行记录，网络等待只占用对应任务。进程中断留下的不确定执行会标记为 `outcome_unknown`，避免恢复时重复提交；取消步骤有独立的次数预算。具体终态与取消能力由工作流定义。

## 账号池、成本与重试

账号池有三种所有权模式。`local` 由 LocalRouter 调度本地凭据，`external` 由外部网关管理，`external-readonly` 读取外部维护者写入的私有池文件。注册、登录、验证码、付款和凭据刷新由配置中指定的一方处理。

调用 Token 默认长期有效且不限次数，仍需遵守账号池的并发、租约、冷却、健康和额度资格。未返回额度时保持未知；缺少价格时不能认定为免费。成本来源和部分计价的含义见 [用量与成本说明](../gateway/README.md#用量与成本边界)。

同模型的兼容渠道按优先级和权重选路。兼容入口在 POST 遇到连接错误或上游 5xx 后不会自动换渠道重放，携带供应商凭据的请求也不会自动跟随重定向。完整 Pack 的重试行为以实际幂等性和发布的契约为准。

## 契约缓存与维护

仅当 `contract.digest` 和 `contract.schema_version` 都未变化时复用操作契约缓存。服务树另有 `topology.digest`；相关摘要或版本变化后重新发现。

调用 Token 只用于服务调用。修改 Pack 需要人工明确授予独立的维护权限，按草稿、影响审阅、精确摘要发布和验证流程执行。详见 [维护权限](OPERATIONS.md#控制台调用和维护权限) 与 [Protocol Pack Skill](../.agents/skills/localrouter-protocol-pack/SKILL.md)。免密控制台不构成 Agent 维护授权。

LAN 使用方式及读取 Token 前的发现检查见 [局域网配置](OPERATIONS.md#让局域网设备调用)。

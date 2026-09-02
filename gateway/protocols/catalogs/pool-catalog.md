# LocalRouter 号池目录

公开发行版的号池目录为空。安装后的实际号池目录由本机操作者维护，并通过同一端口的 `/docs/pools/index.json` 和 `/docs/pools/catalog.md` 向本机 Agent 发布脱敏契约。

## 所有权边界

| 模式 | 请求时选择 | 账号生命周期 | 适用场景 |
|---|---|---|---|
| `static` | 固定凭据或免鉴权 | 操作者 | 单一 API Key、公开服务 |
| `local` | LocalRouter | LocalRouter 或外部维护器 | 本机轮换、冷却、租约与 affinity |
| `external` | 上游网关 | 上游网关 | 不可拆分的 OAuth/会话号池 |
| `external-readonly` source | LocalRouter | 外部维护器 | 维护器原子更新权威源，LocalRouter 只读选号 |

## 公开与本机数据

公开仓库只包含 Schema 和空目录契约，不包含供应商 Pack 或号池示例。下列内容只能位于 XDG 私有目录，不得进入 Pack、指南、测试输出或 Git 历史：

- API Key、Cookie、邮箱、账号 ID 和权威池内容；
- locator 和真实源文件路径；
- 私有上游地址；
- 实时账号数、余额、到期时间和供应商巡检结果。

缺失额度必须表示为未知，不能当作零。账号健康、可调度性、额度和真实业务调用是四个独立证据层。

## Agent 维护流程

Agent 通过维护 MCP 打开草稿，只写入脱敏目录内容，然后执行影响审查、严格校验、精确 digest 发布和线上验证。凭据与 locator 仍由人工在受保护的 XDG 数据目录中配置。

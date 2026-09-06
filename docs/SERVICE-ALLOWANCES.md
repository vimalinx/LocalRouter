# 服务共享自主额度

此功能设有默认关闭的总开关，不改变现有 Token 和能力包权限。人从控制台「服务与渠道 → 自主额度」开启总开关后，从可搜索的服务列表选择服务，配置并保存启用。关闭总开关时收起配置区，所有服务暂停自主额度检查；已保存的规则、用量和待核对记录保留，重新开启继续使用，未使用的单次批准会撤销。旧版本若已有启用的服务规则，升级保留总开关开启，避免意外撤销现有调用限制；旧版本没有启用规则及新安装均默认关闭。新配置以每月 3 美元为基础额度，仍须手动开启。Agent 没有配置、提额或批准自己的 MCP/Service API。

每个完整 Protocol Pack 共用一个额度池；兼容渠道按 Channel Profile 共用一个池（`compatibility:<profile-key>`），同类 Profile 下的多个渠道不是多个独立池。已登记的 Agent 共享余额，bootstrap 身份不能使用启用后的额度池。

## 规则

- `quota`：额度内自主使用；不足时返回 `service_approval_required`。明确公布 `ai.models`、`openai.models` 或 `media.models` 能力且上下游均为 GET/HEAD 的模型目录操作属于发现过程，不受总/子额度余额限制，不产生额度占用；Token、能力包、号池和显式禁止/批准规则仍适用。普通 GET 请求不会因此豁免。
- `approval`：每次需人批准。
- `deny`：禁止调用，单次批准不能覆盖禁止。
- 操作级 `approval_operations` 和 `denied_operations` 使用精确 operation ID，或 `*`。兼容渠道使用 `METHOD /request/path`，例如 `POST /v1/chat/completions`。
- `operation_limits` 可为已发布操作配置独立子额度，单位和周期继承服务。一次调用必须同时满足子额度与服务总额度；未配置子额度的操作仅受总额度约束。兼容动态模型路径使用页面公布的操作模板，多个模型共用该操作的子额度。
- 可多选服务批量设置总额度（初始值 3 美元），也可在单个服务内多选操作批量填入子额度后保存。批量设置保留每个服务的启用状态、周期、子额度及批准规则；任何一项冲突或校验失败，整批不保存。没有用量时可一次保存更换单位和子额度；界面切换单位会清除草稿中的子额度，避免旧数字被误当成新单位。
- 周期支持一次性、UTC 自然日、UTC 自然月。用量产生后单位和周期不可改，避免改配置清空消费；可修改额度总量。任何规则保存都会撤销尚未使用的单次批准。
- 未开启功能时遵守原有授权方式，不能把“关闭”理解成获得无限自主使用许可。

次数池按获准调用计，一次占用一次，失败、超时和重启不退款。每条 Pack 调用启用额度后只允许一个内部上游尝试，避免一次批准被隐含重试扩大。兼容渠道的安全跨渠道重试每次都重新占用额度。

金额单位为整数微美元（1 USD = 1,000,000 `usd_micros`），不是供应商充值余额。自动调用仅接受契约中已确认的固定请求价格；Token、时长、未知或估算费用不能作为安全预占值，因此请求人批准，或改用次数池。兼容模型渠道目前没有固定价格契约，不能启用美元自动额度，除非将所有无法计价的操作设为批准或禁止。保存启用规则和重新开启总开关都会验证这些条件；原有不适用规则显示提示，运行时仍按原规则要求批准，不静默改成不限额。

完整 Pack 在调用前原子预占费用；成功的普通响应根据已有费用记录结算。未知结果、未确认的流式结束或失败保留预占，跨周期和重启仍扣留。人核对供应商记录后可输入实际费用与核对依据。未结算占用不会自动过期退款。此功能控制 LocalRouter 的调用授权，不是供应商账单的硬封顶保证。

## 人工批准与隔离

人可对指定 Agent Token ID 和精确操作批准一次，一小时内有效。这项批准覆盖该操作的请求参数和实际费用，因此应确认操作范围。批准不增加共享余额，不授予 Token 原先没有的权限，不允许禁止的操作。批准被原子消费，重复或并发请求不能复用。

配置接口仅安装在 loopback 人类管理路由，沿用控制台密码设置；携带 Service 或维护 Bearer/API Key 的请求被拒绝，LAN 和 MCP 无对应写入口。免密本机控制台仍遵循既有本机操作者信任边界，不是对能够伪装成人类本机请求的恶意进程的安全隔离。

人工接口：

- `GET /local/api/service-allowance-settings`，读取总开关及 revision。
- `PUT /local/api/service-allowance-settings`，发送 `{enabled, revision}`，立即生效，冲突时须重新读取；单独保存服务规则不会自动开启总开关。
- `GET /local/api/service-allowances`
- `PUT /local/api/service-allowances/:service`，发送完整 rule，必须带读取到的 revision。
- `POST /local/api/service-allowances/batch`，发送 `{services: [{service, rule}]}`，每条 rule 必须带 revision，最多 100 个服务，原子保存。
- `POST /local/api/service-allowances/:service/grants`，发送 `{token_id, operation}`。
- `POST /local/api/service-allowances/:service/receipts/:receipt/reconcile`，发送 `{amount, reason}`。

状态位于私有 data 目录的 `service-allowances.json`，权限 0600；进程锁、重新读取和原子写入确保多实例不能透支。存储失败时阻止新调用，预检只读取不消耗额度。直接 Pack、MCP/工作流的实际 Pack 调用和兼容渠道上游调用经过检查。本地静态目录读取不扣上游调用次数。

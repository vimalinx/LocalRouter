# 独立 Agent 预算与消费明细

在控制台 **Token 管理 → 创建 Token / 编辑 Agent** 配置各 Agent 的每日请求、每分钟请求、最大并发，以及独立的每日 / 每月美元预算。各项留空或填 0 表示关闭该限制。人手动创建的 Token 默认不启用金额限制，也不会改动现有 Token 的权限。

## 试用档与正式档

Agent 通过 `lr identity request` / `lr identity prepare` 或程序通过 `lr connect` 获得的身份立即签发，一律从**试用档**开始。没有身份审查，人只决定花多少钱：

| 设置（Token 管理 → 预算申请 → 试用设置） | 默认 | 说明 |
|---|---|---|
| 每个 Agent 试用 | $2 | 一次性额度，不按周期重置 |
| 每月试用合计 | $5 | 所有试用 Agent 在一个 UTC 自然月内的合计上限 |
| 单次默认预留 | $0.02 | 价格不固定时调用前的占用值，调用后按真实费用结算 |

- 试用额度用完或试用合计用完时，调用不会发往上游，返回 `agent_trial_exhausted` / `agent_trial_pool_exhausted`。
- 试用档中，已知费用超过单次预留时如实计入，不暂停 Agent；因此最后一笔可能略超试用额度。没有费用信息的调用仍按预留占用，直到人工核对。
- Agent 用 `lr budget request formal --monthly <usd> [--daily <usd>] --reason <理由>` 申请转为**正式档**，或用 `lr budget request trial --reason <理由>` 申请再给一轮试用。每个 Agent 同时只有一条待处理申请，新申请会替换旧申请。
- 申请出现在 **Token 管理 → 预算申请**。批准前可修改金额；批准只改写该 Agent 的金额字段，不改变服务范围。正式档只统计转正之后的消费。
- Agent 在身份申请或扩权申请中填写的金额字段一律忽略，金额只能由人决定。在“编辑 Agent”中为试用 Agent 填写日或月预算，同样会把它转为正式档。

### 桌面通知

在 `$XDG_CONFIG_HOME/localrouter/config.env` 中设置 `LOCAL_GATEWAY_DESKTOP_NOTIFICATIONS=true` 并重启服务后，新的预算申请会通过 `notify-send` 弹出桌面通知，带有“同意 / 拒绝 / 打开控制台”按钮。“同意”按 Agent 申请的金额批准；需要改金额时打开控制台。通知同时最多 3 条；没有图形会话或两小时内未处理时，申请保持待处理，控制台仍然可用。默认关闭，避免测试或无桌面环境误弹窗。

## 金额预算如何扣减

- 日 / 月周期均使用 UTC。启用后，该周期内已记录的可靠费用也计入预算；不会重新赠送一份余额。
- 同一个 Agent 的标准模型渠道与 Protocol Pack 共用预算。网关在上游调用前持久化预留，并发调用不能重复使用同一份可用金额。
- 已确认的固定每次价格自动作为预留值。可变价格或未知价格需要填写 **单次预留（USD）**；未配置时拒绝调用，并返回 `agent_budget_quote_required`。
- 单次预留是操作者设置的费用估计，不能强制上游按该金额封顶。应按请求的模型、输入、输出和服务行为设置足够的金额。实际收费超过预留时如实记账，暂停该 Agent 后续调用，等待核对；已经产生的供应商费用不能撤销。
- 收到可靠费用后，用实际金额替换预留，释放差额。预算不足时不发出上游请求，返回 429 `agent_daily_budget_exceeded` 或 `agent_monthly_budget_exceeded`。
- 超时、缺失费用、未完整结束的流式响应保留预留，不能算免费。重启不会释放这些占用；跨日 / 月的未结算预留仍占用可用金额。
- 金额预算启用的调用只允许一次上游尝试，避免透明重试产生额外费用。预检只读预算、不预留；实际发送前仍会再次原子检查。

## 查明钱花在哪

每个 Agent 的 **消费明细** 保存时间、模型、渠道或服务、操作、Token、费用和预留状态，并支持分页。历史费用未知时明确显示“待核对”。这是网关账本，不能替代供应商账单。

**请求日志** 支持服务端按 Agent 筛选、按 Agent 编码/名称、Token 名称、模型、渠道和内容搜索，搜索作用于全部日志，分页总数也是筛选后的总数。此列表展示标准模型渠道请求；Protocol Pack 记录在对应 Agent 的消费明细中。

## 未知费用和异常结算

在 **消费明细 → 核对费用** 中，根据上游账单填入最终 USD 金额和核对依据。确认未收费时可填 0。仅允许核对“费用待核对”和“超出预留”的记录；进行中的请求不能提前释放。核对结果留在历史记录中并更新预算和 Agent 费用汇总。

常见错误码：

| code | 含义 |
|---|---|
| `agent_budget_quote_required` | 可变价格没有配置单次预留 |
| `agent_daily_budget_exceeded` | 日预算余额不足 |
| `agent_monthly_budget_exceeded` | 月预算余额不足 |
| `agent_budget_reconciliation_required` | 实际费用超出预留，等待人工核对 |
| `agent_budget_unavailable` | 账本读写失败，请求未获准发送 |
| `agent_trial_exhausted` | 该 Agent 的试用额度已用完 |
| `agent_trial_pool_exhausted` | 本月所有 Agent 的试用合计已用完 |

## 撤销与迁移

撤销立即使 Key 失效，同时保留 Agent 编码、名称、用量、费用及消费明细。已撤销 Agent 位于历史区，不能显示 Key、复制 Key 或恢复调用权限。同名 Agent 使用不同 Token ID 分账。

升级时一次性导入现有请求日志及仍保留的 Protocol Pack 事件。升级之前已轮转丢弃的事件无法恢复。缺少价格的历史调用不计入已知金额，界面明确提示，可按供应商记录补充核对。升级之后的账单保存在 SQLite 中，不随诊断事件轮转消失。

新增 Token 策略字段使用整数微美元（1 USD = 1,000,000 微美元）：

```json
{
  "monthly_budget_usd_micros": 20000000,
  "daily_budget_usd_micros": 2000000,
  "request_reserve_usd_micros": 250000
}
```

试用档写作 `{"budget_tier": "trial", "trial_budget_usd_micros": 2000000, "request_reserve_usd_micros": 20000}`。

这些字段扩展了策略文件；回滚到旧程序前应恢复兼容的策略备份。账本表是附加表，不删除原日志或 Token 数据。

消费明细读取使用人类控制台 `/local/api/agent-billing/:id`；核对使用 `/local/api/agent-billing/:id/:receipt/reconcile`。试用设置为 `GET/PUT /local/api/agent-budget-settings`（PUT 须带读取到的 `revision`），预算申请为 `GET /local/api/budget-requests` 与 `POST /local/api/budget-requests/:id/decision`（`{approve, monthly_usd_micros?, daily_usd_micros?}`）。Agent 侧只有 loopback 上的 `POST /agent/budget-requests` 与 `GET /agent/budget-requests/:id`，只能申请，不能批准。这些入口不授予 Service Token 或维护 Token 人类管理权限，LAN 不暴露它们。

本地验收：

```sh
go -C gateway test -race ./...
bun run --cwd gateway/web-src test
python3 tests/agent_billing_acceptance.py --binary /absolute/path/to/localrouter
```

该验收只调用隔离的本地模拟上游，不证明任意真实供应商都会返回费用，也不产生付费调用。

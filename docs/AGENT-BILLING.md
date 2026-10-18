# 独立 Agent 预算与消费明细

在控制台 **Token 管理 → 创建 Token / 编辑 Agent** 配置各 Agent 的每日请求、每分钟请求、最大并发，以及独立的每日 / 每月美元预算。各项留空或填 0 表示关闭该限制。默认不启用金额限制，也不会改动现有 Token 的权限。

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

这些字段扩展了策略文件；回滚到旧程序前应恢复兼容的策略备份。账本表是附加表，不删除原日志或 Token 数据。

消费明细读取使用人类控制台 `/local/api/agent-billing/:id`；核对使用 `/local/api/agent-billing/:id/:receipt/reconcile`。这些入口不授予 Service Token 或维护 Token 人类管理权限，LAN 不暴露它们。

本地验收：

```sh
go -C gateway test -race ./...
bun run --cwd gateway/web-src test
python3 tests/agent_billing_acceptance.py --binary /absolute/path/to/localrouter
```

该验收只调用隔离的本地模拟上游，不证明任意真实供应商都会返回费用，也不产生付费调用。

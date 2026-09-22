# README 截图

README 中的三张 PNG 直接截取当前 React 控制台。所有服务名称、模型、账号、工作区、Token 遮罩、价格和调用记录均为虚构数据，不表示发行包预装了这些服务或账号。

## 本地预览

在项目根目录执行以下命令。需要 Bun 和已安装的前端依赖；首次使用可先在 `gateway/web-src` 执行 `bun install --frozen-lockfile`。

```bash
cd gateway/web-src
bun run demo:readme
```

打开 `http://127.0.0.1:18327/`。使用浅色主题，依次进入以下页面，每页刷新一次，等待数据加载及主题、导航颜色过渡结束后截图。保存后检查固定页眉和侧栏也已完整绘制。

| 页面 | 地址片段 | 图片 |
| --- | --- | --- |
| 运行概览 | `#overview` | `images/localrouter-overview.png` |
| 服务与渠道，默认网络检索服务列表 | `#protocols` | `images/localrouter-services.png` |
| 访问权限 | `#tokens` | `images/localrouter-agent-workbench.png` |

截图使用相同宽度，概览保留更高的视口以展示模型与 Agent 排行。当前概览 PNG 为 1898 × 1250，服务页与权限页 PNG 为 1898 × 1127，均保留浏览器原始截图，没有后期修改页面样式或拼图。像素尺寸随浏览器缩放与设备像素比变化；重新截图时检查表格、文字、加载状态及导航选中项，截完后恢复临时视口设置。

## 模拟数据的范围

- 数据固定在 `2026-09-05 20:00:00 Asia/Shanghai`，包含 72 小时的确定性调用记录。累计统计、过去 24 小时趋势、模型排行和 Agent 用量均从这些记录计算；今日额度按 UTC 日期统计，与产品契约一致。
- 3 个模型渠道、4 个服务、5 个 Agent；账号池另有模拟的历史额度余额，包含可调度、请求中、冷却和停用状态。
- 数据定义位于 `gateway/web-src/scripts/readme-demo-data.ts`，预览入口位于 `gateway/web-src/scripts/readme-demo.ts`。修改数据后需重启预览命令并刷新页面。
- 预览仅监听独立的 loopback 端口，端口占用时直接失败；API 请求由内存中的模拟响应处理，没有上游代理。所有非 GET API 请求返回 405，未定义的 API 返回 404。
- 此入口不读取本机网关数据库或凭据，也不写入已安装服务。演示数据不会被正常前端构建包含。结束预览可按 `Ctrl+C`。

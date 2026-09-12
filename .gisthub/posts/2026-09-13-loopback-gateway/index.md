---
title: 模型和第三方 API 先经过本机 8317，再给 Agent 用
date: 2026-09-13
type: note
category: AI
cover: images/cover.png
tags: [local-ai, gateway, agents]
author: Vimalinx
---

几个 Agent 同时调不同的上游时，地址和密钥很容易散落在各处的环境变量里。LocalRouter 把这些收进一个本机网关。应用拿自己的 Service Token 进来，网关按已经配好的渠道或 Protocol Pack 转发，并记下用量。

默认地址是 `127.0.0.1:8317`。这台开发机上的控制台现在就能打开：左侧是运行概览、服务与渠道、Token、请求日志；右下角写着本机监听在线。公开发行包不带供应商账号，新安装要自己把有权使用的上游配进去。

普通模型接口走渠道，Base URL 一般是 `http://127.0.0.1:8317/v1`。需要特殊鉴权或异步任务的服务，用 Protocol Pack 描述操作、模型和账号池，Agent 先发现再选具体操作。主程序是一条 Go 二进制，控制台是嵌进去的 React，状态存在 SQLite。

要给局域网其他设备用，可以另开受 Token 保护的端口。安装器会把 `lr` 放进 `~/.local/bin`，并按用户 systemd 拉起服务。

材料来自公开仓库 [vimalinx/LocalRouter](https://github.com/vimalinx/LocalRouter) 的 README，以及本机打开 `http://127.0.0.1:8317/` 时能看到的控制台骨架。

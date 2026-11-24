# 设计：匿名实例统计与源代码链接

## 包与边界

- `internal/insights`（客户端）：调度、采集、发送、状态存储。采集通过消费方定义的窄接口读取各模块的聚合数字（曲库计数、成员数、各功能开关、设备使用），
  在 `app` 里装配；不直接查询其他模块的表。
- `internal/insights/collector`（统计端）：HTTP 处理、校验、限速、文件存储、汇总；不导入客户端以外的业务包。
- `cmd/roomusic-insights`：统计端入口，参数 `-addr`（默认 `127.0.0.1:18110`）、`-data`（数据目录）。
- 共用的载荷类型与分档常量放在 `internal/insights/schema`，客户端与统计端都用它，保证两边校验一致。

## 数据

迁移 `0072_instance_insights`：单行表 `instance_insights`（`id boolean primary key default true check (id)`、`instance_id uuid not null`、`enabled boolean not null default true`、
`notice_acknowledged_at timestamptz`、`last_attempt_at`、`last_success_at timestamptz`、`last_error text`（错误码，不是原文）、`created_at timestamptz not null default now()`）。
首次启动插入一行；下行迁移删表。

## 配置

- `ROOMUSIC_INSIGHTS_DISABLED`（布尔，默认假）；`ROOMUSIC_INSIGHTS_URL`（默认统计端地址；构建版本为空时默认不发送，显式设置才发送）；`ROOMUSIC_SOURCE_URL`（默认仓库地址，必须是 http(s)）。
- 设了 `ROOMUSIC_UPSTREAM_MOCK_URL` 时，心跳改发到 mock 的 `/insights/api/v1/heartbeat`，mock 记录请求。

## 调度

后台任务随应用生命周期启动：启动后读状态；`now >= created_at + 24h` 且存在启用的管理员、开关打开、未被部署层关闭时，到期就发送；
下一次 = 上次尝试 + 24h + [0,1h) 随机偏移。发送失败只记错误码（`network`、`http_<code>`、`invalid_response`），等下一轮。关闭时取消上下文，不等待发送完成。

## 接口

- `GET /api/v1/admin/insights` → `{enabled, forced_off, instance_id, notice_acknowledged, first_send_after, last_attempt_at, last_success_at, last_error, endpoint_host, public_page_url, payload}`；
  `payload` 与实际发送的 JSON 用同一个函数生成。
- `PUT /api/v1/admin/insights` `{enabled}`；被部署层关闭时返回 `409 insights_forced_off`。
- `POST /api/v1/admin/insights/notice-acknowledgement` → 204，幂等。
- 新增 `GET /api/v1/system/about`，不需要登录，只返回 `{license: "AGPL-3.0-only", source_url}`；不含版本号，避免登录前暴露版本。版本仍由管理员专用的 `GET /api/v1/system/version` 提供（现状）。
- 接口样本写入 `internal/app/testdata/api-samples/insights/`，前端解码器按样本测试。

## 统计端

- `POST /api/v1/heartbeat`：`Content-Type: application/json`，最大 4 KiB，未知字段拒绝，枚举与分档逐项校验，`instance_id` 必须是 UUID；成功 204，失败 400；
  每个 IP 每小时最多 30 次（内存令牌桶，按 IPv4 /32、IPv6 /64 计），超出 429。不写 IP 到日志或文件。
- 存储：`<data>/instances.json`，`map[instance_id]{first_seen, last_seen, payload}`；每次写入先写临时文件再改名；启动时读入；每天清理 90 天未见的实例。
- `GET /api/v1/summary`：`{generated_at, active_7d, active_30d, versions, arch, container, library, members, features, devices_30d}`，各项按 30 天活跃实例计数。
- `GET /`：服务端渲染的单页（中文为主，附英文小标题），显示汇总与“收集了什么、怎么关闭”的链接；不加载外部资源。
- 部署：`deploy/insights/Dockerfile`（静态二进制 + alpine，非 root，只读根文件系统，`cap_drop: [ALL]`）、`deploy/insights/compose.yaml`（项目名 `roomusic-insights`，固定容器名，只监听 `127.0.0.1:18110`，数据目录绑定）、`deploy/insights/README.md`（OpenResty 反向代理与 DNS 步骤）。

## 前端

- `features/system`（或新 `features/insights`，按目录规范选择）：解码器、查询、管理员概览说明、设置区段。
- 概览说明：管理员、`notice_acknowledged=false` 时显示；“查看发送内容”打开对话框显示格式化 JSON；“关闭统计”直接关；“知道了”确认。
- 设置区段放在管理员设置里，靠近备份；“设置 › 关于”所有用户可见。
- 登录页底部一行小字：“ROOMusic 是开源软件 · 源代码”。

## 风险

- 统计端被灌假数据：限速只能挡住粗暴的灌法；汇总页注明数字是自报的。
- 默认开启的争议：说明写清字段、提供部署层关闭，与 ADR 一起公开。

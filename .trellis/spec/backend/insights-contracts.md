# 匿名统计合同

决策见 ADR 0008（`docs/architecture/adr/0008-instance-insights.md`），给用户的字段说明见 `docs/insights.md`。

## 客户端（`internal/insights`）

- 迁移 `0073_instance_insights`：单行表 `instance_insights`（`id boolean` 主键恒为 true），`instance_id` 首次启动随机生成，`enabled` 默认 true，`notice_acknowledged_at`、`last_attempt_at`、`last_success_at`、`last_error`（错误码：`network`、`http_<状态码>`、`invalid_response`）、`created_at`。
- 配置：`ROOMUSIC_INSIGHTS_DISABLED`（布尔）强制关闭；`ROOMUSIC_INSIGHTS_URL` 留空取默认 `https://insights.roomusic.org/api/v1/heartbeat`；`ROOMUSIC_SOURCE_URL` 留空取仓库地址，二者必须是 http(s)。
  设了 `ROOMUSIC_UPSTREAM_MOCK_URL` 时心跳发到 mock 的 `insights` 服务。
- 调度只在“未被部署层关闭”且“有版本号或显式设置了地址”时启动：首次发送不早于 `created_at + 24h`，且存在启用的管理员；之后为上次尝试 + 24 小时 + 由实例 ID 与尝试时刻确定的 [0, 1h) 偏移，重启后不变。
  尝试前以 `last_attempt_at` 做条件更新，并发启动或崩溃不会补发。失败只记错误码，不重试。开关关闭时每 5 分钟复查。
- 出站：`egress.PublicOnly`、不跟随重定向、10 秒超时、`User-Agent: ROOMusic/<版本>`、`Content-Type: application/json`，成功仅认 204。
- 载荷由 `internal/insights/schema` 生成与校验（客户端与统计端共用），字段固定为 `schema`、`instance_id`、`version`、`os`、`arch`、`container`、`library{tracks,releases}`、`members`、`features{auto_curation,steward_model,mcp_token,lan_devices,dsp,scheduled_scan,plugins}`、`devices_30d{browser,upnp,cast}`（`schema` 为 2；`cast` 自 2 起）。
  分档：曲库 `0`、`1-999`、`1k-10k`、`10k-50k`、`50k+`；成员 `1`、`2-5`、`6-20`、`21+`；插件与设备 `0`、`1`、`2-5`、`6+`。新增字段要同时改 schema、前端解码器、`docs/insights.md`，并提升 `schema` 版本。
- 各模块只通过 `app` 装配的窄接口提供聚合数字，`insights` 不查询其他模块的表。

## 接口

| 方法与路径 | 权限 | 说明 |
| --- | --- | --- |
| `GET /api/v1/admin/insights` | 管理员 | `{enabled, forced_off, instance_id, notice_acknowledged, first_send_after, last_attempt_at, last_success_at, last_error, endpoint_host, public_page_url, payload}`；`payload` 与实际发送的 JSON 由同一函数生成 |
| `PUT /api/v1/admin/insights` | 管理员 | `{enabled}`，返回同上；部署层关闭时 `409 insights_forced_off`；缺字段 `400 invalid_input` |
| `POST /api/v1/admin/insights/notice-acknowledgement` | 管理员 | 204，幂等 |
| `GET /api/v1/system/about` | 无需登录 | `{license: "AGPL-3.0-only", source_url}`，不含版本号 |

样本在 `backend/internal/app/testdata/api-samples/insights/`。

## 统计端（`cmd/roomusic-insights`、`internal/insights/collector`）

- `POST /api/v1/heartbeat`：先按来源限速（IPv4 /32、IPv6 /64，每小时 30 次，超出 429），再要求 `application/json`、不超过 `schema.MaxBytes`、严格解码（未知字段、错误类型与枚举都 400），成功 204。
  只有来自 `-trusted-proxy` 地址的连接才采用 `X-Real-IP`。IP 只在内存的限速表里，过期清理，不写文件或日志。
- 存储：`<data>/instances.json`，按 `instance_id` 保存 `first_seen`、`last_seen` 与最新载荷；先写临时文件再改名；启动时读入；90 天未见的实例删除。
- 统计端同时接受 `schema` 1 与 2（客户端与统计端独立部署）；`cast` 分布只计 `schema` 2 的实例。
- `GET /api/v1/summary`：`generated_at`、`active_7d`、`active_30d` 与按 30 天活跃实例统计的版本、架构、容器、曲库、成员、功能、设备分布。`GET /` 为不加载外部资源的汇总页。
- 部署文件在 `deploy/insights/`：非 root、只读根文件系统、`cap_drop: [ALL]`、只监听 `127.0.0.1:18110`，由反向代理提供 HTTPS。

# 匿名统计合同

决策见 ADR 0008（`docs/architecture/adr/0008-instance-insights.md`），给用户的字段说明见 `docs/insights.md`。

## 客户端（`internal/insights`）

- 迁移 `0073_instance_insights`：单行表 `instance_insights`（`id boolean` 主键恒为 true），`instance_id` 首次启动随机生成，`enabled` 默认 true，`notice_acknowledged_at`、`last_attempt_at`、`last_success_at`、`last_error`（错误码：`network`、`http_<状态码>`、`invalid_response`）、`created_at`。
- 配置：`ROOMUSIC_INSIGHTS_DISABLED`（布尔）强制关闭；`ROOMUSIC_INSIGHTS_URL` 留空取默认 `https://roomusic.org/api/v1/heartbeat`，设置页的公开页地址取同一主机的 `/stats`；`ROOMUSIC_SOURCE_URL` 留空取仓库地址，二者必须是 http(s)。
  设了 `ROOMUSIC_UPSTREAM_MOCK_URL` 时心跳发到 mock 的 `insights` 服务。
- 调度只在“未被部署层关闭”且“有版本号或显式设置了地址”时启动：首次发送不早于 `created_at + 24h`，且存在启用的管理员；之后为上次尝试 + 24 小时 + 由实例 ID 与尝试时刻确定的 [0, 1h) 偏移，重启后不变。
  尝试前以 `last_attempt_at` 做条件更新，并发启动或崩溃不会补发。失败只记错误码，不重试。开关关闭时每 5 分钟复查。
- 出站：`egress.PublicOnly`、不跟随重定向、10 秒超时、`User-Agent: ROOMusic/<版本>`、`Content-Type: application/json`，成功仅认 204。
- 载荷由 `internal/insights/schema` 生成与校验（实例与 mock 使用；统计端在门户项目里有自己的副本），字段固定为 `schema`、`instance_id`、`version`、`os`、`arch`、`container`、`library{tracks,releases}`、`members`、`features{auto_curation,steward_model,mcp_token,lan_devices,dsp,scheduled_scan,plugins}`、`devices_30d{browser,upnp,cast}`（`schema` 为 2；`cast` 自 2 起）。
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

## 统计端（门户项目 `roomusic-site`）

统计端程序、汇总页与部署文件在同级仓库 `roomusic-site`（`insights/` 模块与 `deploy/insights/`），行为与合同见那边的 `docs/heartbeat.md`。本仓库需要知道的只有：

- 统计端接收 `POST /api/v1/heartbeat`，成功 204；同时接受仍在使用的各个 `schema` 版本。
- 改心跳格式的顺序：先在 `roomusic-site` 让统计端接收新版本并上线，再提升本仓库的 `schema.Version` 开始发送；统计端继续接收旧版本。
- 字段变化后，把本仓库 `testdata/api-samples/insights/status.json` 里的 `payload` 同步为门户项目 `insights/testdata/heartbeats/` 的新样本。

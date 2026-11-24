# 仓库现状（2026-11-24 调研）

## 播放区与输出端
- 一台设备一个播放区；表 `playback_devices`（`kind CHECK IN ('browser','native')`，`backend/migrations/0043_playback_zones.sql:4`；校验 `backend/internal/zones/devices.go:14`）。
- 运行时 `zoneRuntime`（`backend/internal/zones/service.go:44-87`），每区一把锁，单进程；用户播放区首次请求时懒加载（`service.go:141-166`）。
- 输出端协议（无 Go 接口，HTTP + Service 方法）：`ClaimOutput / ReportOutput / ReleaseOutput / TakeStream / SubscribeOutput`（`playback-zone-service-contracts.md:120-128`）。认领 `outputs.go:12-105`；回报 `status/ended/fault` 带 `stream_seq`、`stream_seconds`，播放中每 10 秒（`playback-zone-contracts.md:122-137`）；在线 = 45 秒租约或 SSE（`output_types.go:160-166`、`output_lifecycle.go:22-80`）。
- 流 URL：`/api/v1/me/playback/zones/{zone}/streams/{seq}`（`render_events.go:88-91`），`seq` = 播放区 revision，可预测。
- 交付格式（`playback/render/negotiate.go:24-31`）：长流 flac、wav、opus、aac；HLS alac、aac。按设备能力 `stream_formats` 顺序协商；采样率上限 768k，位深 16/24（`negotiate.go:140-219`）。WAV 长度 0xFFFFFFFF，FLAC STREAMINFO 长度为 0。
- 长流只能取一次（第二次 410），忽略 Range、`Accept-Ranges: none`，HEAD 405（`outputs.go:503-507`、`zones/httpapi/stream.go:15-40`、`handlers.go:26-28,55-59`）。
- 渲染受播放头节流：领先 ≤20 秒 + 64 KiB（`render/session.go:17,489-526`）；播放头来自输出端回报（`render/events.go:64-101`）。seek 是控制命令，开新流（新 seq、新 URL）；暂停 5 秒后关流（`render_runtime.go:116-164`）。
- 音量只在输出端施加，不进渲染流（`playback-zone-contracts.md:68`）；`next_stream` 只在格式变化时出现；跨曲目在同一长流里连续（无缝、交叉渐变）。
- DSP 全在服务端；DSD 只以 PCM 交付。

## 鉴权与网络
- 流路由只认 `roomusic_session` Cookie（`identity/guard.go:85-97`）；没有签名 URL 或一次性令牌；只读 GET 不做来源校验。令牌生成与哈希可借鉴 `identity.GenerateSessionToken / HashSessionToken`（`guard.go:29-39`）。
- 生产禁止 `ROOMUSIC_PUBLIC_URL`（`platform/config/config.go:93-106`），服务端不知道局域网设备该用哪个地址访问它。
- `egress` 只有 PublicOnly / PublicOrAllowlist / Unrestricted，没有“仅局域网私有地址”（`platform/egress/client.go:19-25`）。
- 生产 Compose：bridge 网络 `roomusic`，端口 `0.0.0.0:8090:8080`，只发布 IPv4，`read_only`、`cap_drop: [ALL]`（`compose.prod.yaml:31-49,74-77`）；收不到 SSDP 组播。开发后端在宿主机运行，可以直接用 SSDP。

## 前端
- 设备列表 `frontend/src/features/player/components/device-picker.tsx`（数据来自快照 `snapshot.devices`），控制目标、转移、改名、删除离线设备都已有；`Device.kind` 是字符串（`zones/model.ts:137`）。UPnP 设备作为一台 zones 设备在线即可出现在列表里并可控制、转移。
- 需补：发现与添加入口、按 kind 的图标与文案、固定音量。

## 权限
- 播放区只属于本人，管理员也只能访问自己的（`playback-zone-contracts.md:8`）；归属不符 404。没有“局域网共享设备”的概念。会话吊销钩子 `WithSessionRevoked`（`app/app.go:288`）。

## 接入点
- 新包在进程内调用 `zones.Service` 的输出端方法，扮演“服务端托管的输出端”；装配在 `app/app.go:154` 附近。

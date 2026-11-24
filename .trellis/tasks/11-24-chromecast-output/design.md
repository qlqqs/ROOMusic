# 设计：Chromecast 投放

## 包

- `internal/devices/mdns`：通用 mDNS 查询（PTR→SRV/TXT/A），按网卡发送，结果带来源网卡与本机地址；Cast 与 AirPlay 共用。
- `internal/devices/cast`：注册表、CASTV2 客户端（`castv2/` 子包：帧编解码用手写的最小 protobuf，只有 CastMessage 一种消息）、托管输出、httpapi。
- `internal/devices/lan`（可选，实现时判断）：把 UPnP 与 Cast 共用的设置、分配、占用、可见性规则提出来；如果提取代价大，先让 Cast 调用 UPnP 服务里已有的规则函数，不复制逻辑。
- `zones`：`ManagedOutputs` 的设备带 `Kind`，`visible()` 与相关处按设备种类取值；`UPnPStream` 改名或加通用别名，供 Cast 取流。
- `playback/render`：`encode.go` 增加 `mp3`（libmp3lame，CBR 320k）。
- `cmd/roomusic-fake-cast`：假接收端（TLS 自签证书、mDNS 公告、CASTV2 子集、按 URL 拉流计字节、`/_fake/state`）。

## 数据

迁移：设置表增加 `cast_enabled`（默认 true）与 `upnp_enabled`（默认 true，现有行为不变）；手动设备表能存 Cast 的 IP 与端口（新表 `cast_manual_devices` 或给现有表加种类列，按迁移规范选择）；
设备级设置（格式、采样率上限）存在成员设备行或设备设置表，沿用 UPnP 采样率设置的位置。设备键 `cast:<id>` 与 UPnP 的 UDN 共用分配表。

## 状态机

连接 → CONNECT receiver-0 → LAUNCH → 拿到 transportId → CONNECT transport → LOAD。设备推送 MEDIA_STATUS：`PLAYING`/`PAUSED`/`BUFFERING`/`IDLE(FINISHED|CANCELLED|INTERRUPTED|ERROR)`；
`FINISHED` 且有接续流 → LOAD 下一首；`CANCELLED`/`INTERRUPTED` 或 RECEIVER_STATUS 里应用变了 → 视为被接管，释放；心跳 PING/PONG 每 5 秒，连续 3 次无响应视为离线。

## 前端

设备列表的设备类型标签（局域网/Google Cast）、设置里的协议开关与单台设备格式；沿用 lan-devices 功能目录。

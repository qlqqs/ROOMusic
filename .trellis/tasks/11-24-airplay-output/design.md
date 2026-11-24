# 设计：AirPlay 2 投放

## 包

- `internal/devices/airplay`：注册表（mDNS 结果 → 设备：名称、型号、特性位、是否需要 PIN、是否 FairPlay）、配对凭据存储、托管输出、httpapi。
- `internal/devices/airplay/rtsp`：RTSP 客户端（明文与 ChaCha20 加密两种传输）。
- `internal/devices/airplay/hap`：SRP-6a（3072 位组，SHA-512）、pair-setup / pair-verify、HKDF 派生、会话密钥。可参考 brutella/hap（Apache-2.0，角色相反）。
- `internal/devices/airplay/bplist`：二进制 plist 编解码（自写最小子集，或引入 BSD 许可的 `howett.net/plist`，按依赖规范评估）。
- `internal/devices/airplay/rtp`：ALAC 未压缩帧打包、RTP 头、发送节奏、重传缓冲、音频加密。
- `internal/devices/airplay/raop`：AirPlay 1 路径（ANNOUNCE/SDP、RSA 加密 AES 密钥、NTP 计时端口）。
- `internal/ptp` 与 `cmd/roomusic-ptp`：PTP 端点与本地时钟接口（`127.0.0.1:<端口>`，只读查询当前偏移与 grandmaster 状态）。
- `zones`：新增“推流输出”形态：输出端从渲染会话取连续 PCM 读取器（采样率、位深按设备设置），渲染会话照常应用音效并按实时节奏领先播放头；位置回报以发送的帧数和接收端时钟为准。

## 部署

`compose.prod.yaml` 增加 `ptp` 服务（与应用同一镜像，入口 `roomusic-ptp`），`user: "0:0"`、`cap_drop: [ALL]`、`cap_add: [NET_BIND_SERVICE]`、`no-new-privileges`、`read_only`、`network_mode: host`；
应用经 `ROOMUSIC_PTP_ADDR`（默认 `127.0.0.1:55440`）读取。bridge 覆盖文件里移除 `ptp` 并注明 bridge 模式不支持 AirPlay 2 原生对时。单文件 NAS 版本同步。

## 测试设施

`scripts/test-airplay.sh`（或集成测试里的 Docker 编排）：建独立 bridge 网络，起 shairport-sync（AirPlay 2，含 nqptp）与 classic 两个接收端，输出到管道文件；发送端以测试二进制在同网络容器里运行。镜像按摘要固定。

## 风险

- 接收端固件行为变化：把各设备的特殊处理集中在一个“设备差异表”里，便于以后修。
- PTP 实现复杂：先做接收端需要的最小集合（Sync/Follow_Up/Delay_Req/Delay_Resp，作为 master），再按 Sonos/HomePod 的需要补。

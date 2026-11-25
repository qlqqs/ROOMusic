# 局域网设备：高码率与多房间同步、现有协议审查

状态：规划中（父任务）。父任务本身没有实现工作，不启动；子任务各自规划、实施、验收和归档。

## 目标

1. 让 ROOMusic 能把高码率（含 DSD）音频送到局域网设备，并能让多台设备同步播放同一个播放区。
2. 借鉴通用的局域网音频工程经验，审查并修复现有 AirPlay、Google Cast、UPnP 输出。

## 背景

- 起因：qlqq 2026-11-25 让我分析 `temp/roomusic局域网发现与串流设计.md`。分析结论：这份文档是 Roon 私有协议（RAAT、SOOD、Roon Bridge）的逆向整理稿，只是换了名字。
  - 不能作为 ROOMusic 的实现依据：ROOMusic 是 AGPL 开源项目，从闭源程序逆向来的内部结构、参数表和厂商表不进仓库。
  - 不能用它驱动 Roon Ready 设备：设备只执行经厂商签名服务签名的脚本，绕过签名属于规避技术保护措施。
  - 只借鉴与来源无关的通用工程经验，例如多网卡组播、帧长校验、请求看门狗、故障隔离和逐端点延迟补偿。
- 现状：每台局域网设备只能作为一个播放区的单一输出，没有多设备编组。
  - 音质上限：UPnP 默认 48 kHz/24 bit；Cast 用 FLAC 时最高 96 kHz；AirPlay 最高 48 kHz/24 bit，不能直送 DSD。
  - 合同见 `.trellis/spec/backend/upnp-device-contracts.md`、`playback-zone-contracts.md`、`playback-zone-service-contracts.md`。

## 决定记录

| 编号 | 决定 | 选择者与理由 |
| --- | --- | --- |
| L1 | 两项都做：①审查现有三种协议；②新增高码率与多房间同步的设备支持 | qlqq 2026-11-25 |
| L2 | 新设备走开放生态：实现 SlimProto 服务端，接入 Squeezelite 类播放器；不自写设备端，不仿制 Roon 协议 | 主代理推荐，qlqq 未反对。Squeezelite 是开源设备端，piCorePlayer、moOde 等系统和不少播放器内置，支持高码率、DSD 与多台同步 |
| L4 | 高精度多房间加 Snapcast 子任务；SlimProto 编组照常做（毫秒级，给 piCorePlayer 这类用户）；SlimProto 的服务器端漂移补偿等实测发现跳过时有咔哒声再加 | qlqq 2026-11-25 选择。SlimProto 同步是毫秒级（时间戳以毫秒为单位，靠跳过修正），Snapcast 客户端按采样点校正，官方称偏差通常低于 0.2 ms，是唯一现成、精度能超过 RAAT 的路 |
| L5 | 没有网络功能、插在 NAS 上的 USB 解码器，用 Squeezelite 边车容器接入，只写文档与示例，不写新后端代码 | 主代理取，回答 qlqq“设备不支持怎么办” |
| L6 | Snapcast 子任务暂缓：先用真实设备试 SlimProto 编组，漂移下的跳帧修正听得出来时，再做 Snapcast 或给 SlimProto 加服务器端漂移补偿 | qlqq 2026-11-25 选择。SlimProto 编组实测时钟一致时 p95 0.35 ms，+100 ppm 时 p95 9.2 ms、峰值 10.3 ms（每几分钟一次 strm a），Snapcast 的精度优势只剩漂移场景 |
| L7 | 无实机条件，L6 的实机门槛改为软件替代验证（子任务 `11-25-slimproto-sync-verify`）：跳帧修正在持续音、和声上有明显咔哒（切点高频能量高出基线 74–91 dB），噪声类素材可掩盖；旧版 Squeezelite 兼容性矩阵顺带修复了 1479 前版本 352.8 kHz 起不了播的问题 | qlqq 2026-11-25 选择软件替代验证。是否做 Snapcast 或服务器端漂移补偿，待 qlqq 按 WAV 试听与测量决定 |
| L3 | 按“规划完直接实现、本地合并、不推送”进行；审查先行 | 沿用 M7/M8 的做法。审查会通读现有设备层，SlimProto 的设计要复用它的托管输出与取流约定 |

## 子任务

| 顺序 | 子任务 | 交付 |
| --- | --- | --- |
| 1 | `11-25-lan-protocol-audit` | 按清单审查现有 AirPlay、Cast、UPnP、mDNS、SSDP、PTP，修复确认的问题 |
| 2 | `11-25-slimproto-squeezelite` | SlimProto 服务端，单台设备：发现、连接、高码率无损播放（DSD 转 PCM）、音量、断线 |
| 3 | `11-25-slimproto-sync-groups` | 多台 SlimProto 设备编组同步播放（毫秒级）；依赖第 2 项合入；定义“一个播放区输出到多台设备”的编组模型 |
| 4 | `11-25-snapcast-multiroom` | Snapcast 服务端：snapclient 设备的单台播放与亚毫秒级编组同步；复用第 3 项的编组模型 |

## 后续候选（不在本父任务内）

不支持 SlimProto 的商用设备，按各自协议自带的编组接入多房间（qlqq 2026-11-25 问及）：

- AirPlay 2 多设备同步：协议原生支持，复用现有 AirPlay 2 与 PTP 服务，商用音箱覆盖面最大。建议作为 SlimProto 之后的下一项。
- Cast 音箱组：Google Home 里建的组在局域网上表现为一台独立的 Cast 设备，现有发现可能已经能列出（`devices/cast/registry.go` 没有特殊处理），需要用真实设备验证。
- UPnP：标准没有同步。Sonos、WiiM 等在自家应用里编组，由主设备分发，ROOMusic 推给主设备即可；在文档里说明。
- 跨协议同组不做：各协议的时钟与缓冲机制不同，Roon 也不支持。
- ROOMusic Bridge（qlqq 2026-11-25 问及）：自研的播放端程序，装在树莓派或小主机上，把 USB 解码器变成 ROOMusic 输出。在播放区协议 v1（`playback-zone-contracts.md`，原生客户端的认领、长流、回报已定义，能力里预留了 `dsd: dop|native` 与 `exclusive`）上补三样：设备配对凭据、基于现有 PTP 服务的对时与按时刻起播、DSD 直送的渲染路径。相比 Snapcast 多出 DSD 直送、自动识别声卡能力、硬件音量和更高的同步精度；代价是 ALSA 输出、多平台打包与长期维护。等 Snapcast 编组实测后再决定是否立项。

## 跨子任务验收

- 每个子任务合入后，没有启用新功能的实例行为不变。
- 仓库里不出现从那份逆向文档搬来的内容：Roon 的服务号、端口与组播地址、内部类名、厂商例外表、协议报文定义。
- README、路线图与对应 spec 同步新状态。

## 进度（2026-11-25）

| 子任务 | 状态 |
| --- | --- |
| `11-25-lan-protocol-audit` | 已合入并归档 |
| `11-25-slimproto-squeezelite` | 已合入并归档 |
| `11-25-slimproto-sync-groups` | 已合入并归档 |
| `11-25-slimproto-sync-verify` | 已合入并归档（L7） |
| `11-25-snapcast-multiroom` | 暂缓（L6），待 L7 结论后决定，规划与协议调研保留 |

待 qlqq：试听 `temp/slimproto-skip/` 的 WAV，决定是否做 Snapcast 或服务器端漂移补偿；审阅各子任务 PRD 里主代理替他定的决定（审查 F1–F10、S1–S11、G1–G11）。

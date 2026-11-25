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
| L3 | 按“规划完直接实现、本地合并、不推送”进行；审查先行 | 沿用 M7/M8 的做法。审查会通读现有设备层，SlimProto 的设计要复用它的托管输出与取流约定 |

## 子任务

| 顺序 | 子任务 | 交付 |
| --- | --- | --- |
| 1 | `11-25-lan-protocol-audit` | 按清单审查现有 AirPlay、Cast、UPnP、mDNS、SSDP、PTP，修复确认的问题 |
| 2 | `11-25-slimproto-squeezelite` | SlimProto 服务端，单台设备：发现、连接、高码率无损播放（DSD 转 PCM）、音量、断线 |
| 3 | `11-25-slimproto-sync-groups` | 多台 SlimProto 设备编组同步播放；依赖第 2 项合入 |

## 后续候选（不在本父任务内）

不支持 SlimProto 的商用设备，按各自协议自带的编组接入多房间（qlqq 2026-11-25 问及）：

- AirPlay 2 多设备同步：协议原生支持，复用现有 AirPlay 2 与 PTP 服务，商用音箱覆盖面最大。建议作为 SlimProto 之后的下一项。
- Cast 音箱组：Google Home 里建的组在局域网上表现为一台独立的 Cast 设备，现有发现可能已经能列出（`devices/cast/registry.go` 没有特殊处理），需要用真实设备验证。
- UPnP：标准没有同步。Sonos、WiiM 等在自家应用里编组，由主设备分发，ROOMusic 推给主设备即可；在文档里说明。
- 跨协议同组不做：各协议的时钟与缓冲机制不同，Roon 也不支持。

## 跨子任务验收

- 每个子任务合入后，没有启用新功能的实例行为不变。
- 仓库里不出现从那份逆向文档搬来的内容：Roon 的服务号、端口与组播地址、内部类名、厂商例外表、协议报文定义。
- README、路线图与对应 spec 同步新状态。

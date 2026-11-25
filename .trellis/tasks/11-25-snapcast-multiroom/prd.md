# Snapcast 服务端：高精度多房间同步

状态：规划中（草稿）。协议事实见 `research/snapcast-protocol.md`。父任务 `11-25-lan-hires-multiroom`。依赖：`11-25-slimproto-squeezelite` 合入（复用它对局域网设备公共层的扩展方式），与 `11-25-slimproto-sync-groups` 共用“一个播放区输出到多台设备”的编组模型，先做者定义模型，后做者复用。

## 目标

ROOMusic 实现 Snapcast 的服务端协议。装了 snapclient 的设备（Home Assistant、DietPi、Volumio 插件、树莓派等）能连到 ROOMusic，作为托管局域网输出使用。多台编组时达到亚毫秒级同步，在同步精度上超过 RAAT（按逆向文档的分析，RAAT 为 1–4 ms）。

## 背景

- qlqq 2026-11-25 选择：SlimProto 的同步是毫秒级（`11-25-slimproto-sync-groups/research/sync-algorithm.md`），高精度多房间改由 Snapcast 承担，SlimProto 编组照常做。
- Snapcast 是 GPL-3 开源项目，协议有公开文档 `doc/binary_protocol.md`。客户端按服务器时间播放带时间戳的音频块，通过删除或复制单个采样点来校正；官方说明偏差通常低于 0.2 ms（`README.md:45-47`）。
- 支持 PCM、FLAC、Opus、Ogg 等编码，采样率由流参数决定；不支持 DSD。
- 源码已克隆到 `temp/research-slimproto/snapcast/`，只用来理解协议，不复制代码。

## 决定（草稿，主代理取默认，随交付请 qlqq 审阅）

| 编号 | 决定 | 理由 |
| --- | --- | --- |
| N1 | 局域网设备设置增加 `snapcast_enabled`，默认 true，受总开关约束 | 与其他协议一致 |
| N2 | ROOMusic 只实现服务端协议，不运行 snapserver 进程，不读管道 | 播放区、队列与渲染都在 ROOMusic 内，流直接来自渲染会话 |
| N3 | 交付编码用 FLAC（无损）。采样率与位深由管理员按设备配置，默认 48000 Hz/24 bit；组内取各设备配置的最小值。DSD 来源转 PCM | snapclient 的 Hello 不报告声卡能力；声卡设不上请求的采样率时它不报错、照样变调播放（调研 §10），所以只能人工配置，默认取几乎所有声卡都支持的格式 |
| N4 | 编组模型与 `11-25-slimproto-sync-groups` 的 G1、G3–G8 相同；同一组内只允许同一种协议 | 一致的使用方式；跨协议对齐不做 |
| N5 | 单台 snapclient 也可以作为普通托管输出使用 | 不必为了一台设备先建组 |
| N6 | 只接受局域网 IPv4 的连接 | 与其他局域网设备一致 |
| N7 | mDNS 公告 `_snapcast._tcp`，端口 1704。1704 被占用（同机已有 snapserver）时标记 `port_in_use`，不抢端口。文档说明：同一网络里另有 snapserver 时，snapclient 只连查到的第一个，需要在播放器上用 `-h` 指定 ROOMusic | 与 SlimProto 的端口处理一致（调研 §1） |
| N8 | 暂停、跳转、换曲：发新的 CodecHeader 清空客户端缓冲，再按新的起点时刻发块（协议没有暂停命令，调研 §7） | 唯一能立刻清空客户端缓冲的办法 |

## 验收（草稿）

- 真实 snapclient 在 Docker 测试网络里连上 ROOMusic，被发现、认领、播放 96 kHz/24 bit FLAC。
- 两台 snapclient 编组播放 10 分钟，按合成脉冲测得的出声偏差不超过 1 ms（目标 0.2 ms，按实测记录）；其中一台设 100 ppm 漂移时仍满足。测量方法：snapclient 用 `--player file:filename=stdout`，它每 50 ms 按实时节奏写出（调研 §12），在同一主机上比较两台输出里合成脉冲的时刻；100 ppm 漂移由 ROOMusic 的测试模式给其中一台按比例缩放服务器时钟来模拟。真实声卡和驱动延迟带来的误差不在这项测试范围内。
- 暂停、继续、跳转、换曲后仍同步；一台掉线时其余继续播放。

## 不做

- snapserver 的 JSON-RPC 控制接口、Snapweb、流插件。
- 跨协议编组。

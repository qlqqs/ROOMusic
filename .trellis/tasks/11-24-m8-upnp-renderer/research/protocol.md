# UPnP/DLNA 协议与生态（2026-11-24 调研）

## 发现
- M-SEARCH：`HOST: 239.255.255.250:1900`、`MAN: "ssdp:discover"`、`MX` 1–5、`ST: urn:schemas-upnp-org:device:MediaRenderer:1`；UDP 不可靠，发多次并至少等 MX 秒；已知地址可单播 M-SEARCH（UPnP Device Architecture 1.1 §1.3.2）。
- NOTIFY alive/byebye，`max-age` ≥1800；组播 TTL 默认 2（§1.2）。多网卡要按收到响应的网卡生成回调与流地址。
- huin/goupnp v1.3.1（BSD-2，2026-11 仍有发布）：只枚举 IPv4 网卡，搜索 2 秒发 3 次，记录每台设备的 `LocalAddr`；`dcps/av1` 含 AVTransport1、RenderingControl1、ConnectionManager1 生成代码（可按 URL 构造）；没有 GENA 订阅。

## Docker
- Jellyfin、Music Assistant、BubbleUPnP Server、upmpdcli 都要求 host 网络（或 macvlan）才能发现；Home Assistant 支持手动添加与轮询。
- bridge 下可以直接取 description.xml 与发 SOAP，但设备反连服务器（取流）要用宿主机的局域网地址。

## 动作
- AVTransport:1 必选：SetAVTransportURI、GetMediaInfo、GetTransportInfo、GetPositionInfo、Stop、Play（`Speed=1`）、Seek、Next、Previous；可选：SetNextAVTransportURI、Pause、GetCurrentTransportActions。InstanceID 用 0。
- RenderingControl:1 的 Get/SetVolume、Get/SetMute 全是可选，Channel 用 `Master`。
- ConnectionManager GetProtocolInfo：只能判断 MIME，判断不了采样率与位深。
- 播放顺序（Music Assistant）：Stop → SetAVTransportURI（带 DIDL-Lite）→ Play。
- DIDL-Lite：`object.item.audioItem.musicTrack`，`res protocolInfo="http-get:*:audio/flac:DLNA.ORG_OP=00;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"`；非 ASCII 转字符引用。
- SetNextAVTransportURI 多数设备不可靠；Music Assistant 默认把整条队列拼成一条连续流（flow 模式）。

## 状态
- 播放位置不推送，规范建议播放时每秒 GetPositionInfo；GENA 只推 LastChange。Music Assistant 订阅失败时退到轮询；多个设备的 NOTIFY 有格式问题。

## 流
- 连续流（转码、无长度）常见做法：`transferMode.dlna.org: Streaming`、`contentFeatures.dlna.org: DLNA.ORG_OP=00;DLNA.ORG_FLAGS=0170…`、`Accept-Ranges: none`，chunked；部分设备（Sonos 的 MP3）要假长度。FLAC/WAV 的 chunked 普遍可播。
- 设备常先 HEAD 或探测性 GET。
- 只用 http；URL 里带不可猜的令牌是行业常规（BubbleUPnP Server），有效期要覆盖整段播放。

## 设备差异
- Sonos：FLAC 最高 48k/24（建议走专用协议，不在本任务）；Kodi 的 Seek 只认 REL_TIME 且只在 PLAYING 时；WiiM 支持到 192k；DSD 经 DLNA 不可靠，一律转 PCM。

来源网址见调研代理原始汇报（upnp.org 规范 PDF、Jellyfin/Music Assistant/BubbleUPnP/Home Assistant 文档、huin/goupnp、go2tv、async_upnp_client）。

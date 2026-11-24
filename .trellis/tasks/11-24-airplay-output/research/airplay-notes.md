# AirPlay 控制握手与接收端差异

## 发现与会话

- 设备标识取自 AirPlay 的 TXT deviceid 或 RAOP 实例名的 MAC 前缀。特性字段先低 32 位、后高 32 位。位 12、14、51 声明认证能力；另一条认证路径可用时，同一设备仍可投放。固定版本的 shairport-sync 同时声明位 14、48，实测能完成 transient 配对。来源为 openairplay/airplay-spec 的 src/service_discovery.md、src/features.md、src/status_flags.md（仓库未声明统一许可，仅取线上字段事实），以及 shairport-sync 5.6.3 的 Docker 镜像公告（按文件混合许可）。[协议字段](https://openairplay.github.io/airplay-spec/features.html)
- PIN 与密码分别由状态标志表示，Apple TV 可以通过屏幕 PIN 建立长期身份。来源为 pyatv 的 pyatv/protocols/airplay/utils.py 与 auth/hap.py，MIT。[pyatv](https://github.com/postlund/pyatv/tree/master/pyatv/protocols/airplay)
- 配对请求可以在同一 TCP 连接使用 HTTP/1.1，控制请求使用 RTSP/1.0。会话内 DACP-ID 与 Client-Instance 一致，新版接收端会检查 AirPlay User-Agent 的版本。来源为 airplay2-sender-cpp 的 src/raop_sender.cpp（Apache-2.0）与 OwnTone issue 2042（项目 GPL-2.0-or-later，issue 仅取设备行为）。[发送端](https://github.com/akustikrausch/airplay2-sender-cpp/blob/main/src/raop_sender.cpp) · [接收端版本检查](https://github.com/owntone/owntone-server/issues/2042)
- 初次 SETUP 发送二进制 plist，不含 streams。PTP 会话可携带组 UUID、组长标志、发送端计时地址；响应给出监听中的 TCP eventPort。shairport-sync 5.6.3 支持此 PTP 会话请求，其原生 NTP 流处理尚未实现。来源为 shairport-sync 5.6.3 的 rtsp.c（该文件 MIT），以及 pyatv 的 pyatv/protocols/raop/protocols/airplayv2.py（MIT）。[接收端](https://github.com/mikebrady/shairport-sync/blob/5.6.3/rtsp.c) · [发送端](https://github.com/postlund/pyatv/blob/master/pyatv/protocols/raop/protocols/airplayv2.py)
- classic 的 OPTIONS 列出 ANNOUNCE。后者使用 application/sdp 描述 44100 Hz、16 位、双声道 ALAC，每包 352 帧。来源为 openairplay/airplay-spec 的 src/audio/rtsp_requests/announce.md（未声明统一许可，仅取 SDP 字段事实）以及 shairport-sync 5.6.3-classic 二进制握手（按文件混合许可）。[ANNOUNCE](https://openairplay.github.io/airplay-spec/audio/rtsp_requests/announce.html)

## 配对与控制加密

- transient 的 M1 声明临时配对，固定 PIN 为 3939。M4 是明文响应，其中的 SRP 证明用于校验接收端。从下一条请求开始，双方直接以 SRP 会话密钥派生控制密钥。来源为 pyatv 的 pyatv/protocols/airplay/auth/hap_transient.py（MIT）、shairport-sync 5.6.3 的 rtsp.c 与 pair_ap/pair_homekit.c（两文件均为 MIT）。[pyatv](https://github.com/postlund/pyatv/blob/master/pyatv/protocols/airplay/auth/hap_transient.py) · [接收端](https://github.com/mikebrady/shairport-sync/blob/5.6.3/pair_ap/pair_homekit.c)
- SRP 使用 3072 位群、生成元 5 与 SHA-512。计算 k、u 的整数采用群宽编码，证明与共享密钥的整数使用最短大端编码。来源为 RFC 5054 附录 A.4（IETF 规范，使用公开群参数）、airplay2-sender-cpp 的 src/airplay_crypto.cpp（Apache-2.0）与 shairport-sync 5.6.3 的 pair_ap/pair_homekit.c（MIT）。[群参数](https://www.rfc-editor.org/rfc/rfc5054#appendix-A.4) · [发送端](https://github.com/akustikrausch/airplay2-sender-cpp/blob/main/src/airplay_crypto.cpp)
- 屏幕 PIN 路径先请求显示 PIN；M5、M6 在加密 TLV 中交换并签名长期身份。重连发起独立的 X25519 交换，用长期 Ed25519 密钥核对临时公钥、身份及其顺序。正常支持此模式的接收端在 M4 后启用控制加密。来源为 pyatv 的 pyatv/protocols/airplay/auth/hap.py、pyatv/auth/hap_srp.py（MIT），以及 airplay2-sender-cpp 的 src/raop_sender.cpp（Apache-2.0）。[pyatv](https://github.com/postlund/pyatv/blob/master/pyatv/auth/hap_srp.py)
- 控制通道按方向派生独立密钥和计数器。每帧明文不超过 1024 字节，以两字节小端长度作为 AEAD 关联数据。来源为 pyatv 的 pyatv/auth/hap_session.py（MIT）。[帧格式](https://github.com/postlund/pyatv/blob/master/pyatv/auth/hap_session.py)
- shairport-sync 5.6.3 能返回长期配对 M6，重连也能返回 pair-verify M4。但该版本没有在长期验证完成后启用控制通道加密。额外实测中，下一次加密 GET /info 收到的前两字节为明文 RT；发送端按非法加密帧终止。故该镜像可验证长期配对消息交换，transient 路径用于验证加密 SETUP。来源为 shairport-sync 5.6.3 的 rtsp.c（MIT），并以两次独立容器握手对照。[接收端](https://github.com/mikebrady/shairport-sync/blob/5.6.3/rtsp.c)

## 固定测试环境

- 上游镜像分别提供 AirPlay 2 与 classic；前者内含 nqptp。bridge 内可在接收端网络命名空间启用 nqptp，发送端只需访问同网接收端。来源为 shairport-sync 的 README.md、docker/README.md、镜像内配置及二进制帮助（项目许可按 COPYING 分文件声明）；nqptp 仅运行镜像内二进制，项目 LICENSE 为 GPLv2。[镜像标签](https://hub.docker.com/r/mikebrady/shairport-sync/tags) · [nqptp 许可](https://github.com/mikebrady/nqptp/blob/master/LICENSE)

## 第二段媒体传输

- 真实 NTP 接收端在会话 SETUP 中可能先公布 eventPort、稍后才完成监听；第一次建连可返回 ECONNREFUSED。pyatv 的 pyatv/protocols/raop/protocols/airplayv2.py（MIT）记有同一现象；仅对此错误，在已公布的端口短时重试。[互操作记录](https://github.com/postlund/pyatv/blob/master/pyatv/protocols/raop/protocols/airplayv2.py)

- 原生实时流（type 96）依靠 UDP 控制通道的同步包给出 RTP 与 NTP 的对应关系。SETRATEANCHORTIME 是缓冲流的播放锚定方式，带网络时钟标识和 RTP 时间戳。shairport-sync 5.6.3（rtsp.c 文件 MIT）在 type 103 的 SETUP 后等待此方法，实时流直接进入录音与同步。因此本段的两条实时 NTP 路径均以同步包定锚。来源为 pyatv（MIT）的 pyatv/protocols/raop/protocols/airplayv2.py、shairport-sync 5.6.3 的 rtsp.c 和项目 issue 2303 中的设备行为记录。[实时流](https://github.com/postlund/pyatv/blob/master/pyatv/protocols/raop/protocols/airplayv2.py) · [缓冲流](https://github.com/mikebrady/shairport-sync/issues/2303)
- GET /info 中的 audioFormats 按流类型列出 audioInputFormats。type 96 的格式位图中 0x40000 对应立体声 44100/16 ALAC，0x200000 对应 48000/24。接收端省略实时流能力时，流级 SETUP 才决定协商是否成功。资料为 Vespertine PR 397 的 Packages/VespertineKit/Sources/VespertineAirPlay/AirPlayFormat.swift（项目 GPL-3.0，仅取字段事实），以及公开的 /info 抓包。[格式](https://github.com/vespertineapp/Vespertine/pull/397) · [公告样例](https://github.com/espes/Slave-in-the-Magic-Mirror/issues/12)

- 实时流的每个 RTP 包使用类型 96，首包和 FLUSH 后的第一包带 marker。序号按包递增，时间戳按立体声采样帧数递增。控制端口接受类型 85 的重传请求，并用类型 86 封装已经发出的原包；类型 84 的同步包绑定 NTP 时间、当前播放时间戳与发送时间戳。NTP 端口把类型 82 请求的发送时间回显于类型 83 响应。来源为 openairplay/airplay-spec 的 src/audio/rtp_streams.md（仓库未声明统一许可，仅使用字段事实）和 pyatv 的 pyatv/protocols/raop/stream_client.py（MIT）。[RTP 字段](https://openairplay.github.io/airplay-spec/audio/rtp_streams.html)
- ALAC 未压缩双声道帧以 CPE 开头，标明未压缩，将左右声道以最高位先行交错排列，最后写结束标记。源 PCM 为小端。默认协商每包 352 帧。来源为 airplay2-sender-cpp 的 src/raop_sender.cpp（Apache-2.0）。[发送端](https://github.com/akustikrausch/airplay2-sender-cpp/blob/main/src/raop_sender.cpp)
- RAOP 在 ANNOUNCE 的 SDP 中声明 ALAC 格式。RSA 模式按 AirPort 的公开模数、指数及 OAEP-SHA1 加密随机 AES-128 密钥，传递随机 IV；每个 RTP 包都从此 IV 开始，对载荷的完整块做 CBC，末尾不足一块的字节直传。SETUP 交换 UDP 端口，RECORD 和 FLUSH 使用 RTP-Info。来源为 libraop 的 src/raop_client.c（项目自有代码 MIT，见 LICENSE.txt）。[发送端](https://github.com/philippe44/libraop/blob/master/src/raop_client.c)
- 原生 AirPlay 2 实时模式完成配对与会话级 SETUP 后连接事件 TCP 端口，在 RECORD 后发第二次 SETUP：type=96、ct=2、spf=352、发送端 controlPort 和音频密钥 shk。接收端返回 dataPort、controlPort。音频密钥是本次密钥交换输出的首 32 字节。RTP 头的时间戳和 SSRC 用作 ChaCha20-Poly1305 关联数据，载荷附标签和八字节小端递增 nonce。事件通道采用 Events-Salt；接收端写入的事件用 Events-Write 密钥解密，应答用 Events-Read 加密。来源为 airplay2-sender-cpp 的 src/raop_sender.cpp（Apache-2.0）和 pyatv 的 pyatv/protocols/raop/protocols/airplayv2.py（MIT）。[发送端](https://github.com/akustikrausch/airplay2-sender-cpp/blob/main/src/raop_sender.cpp) · [NTP 与事件通道](https://github.com/postlund/pyatv/blob/master/pyatv/protocols/raop/protocols/airplayv2.py)
- SET_PARAMETER 使用 text/parameters 下发 dB 音量或静音。标题 minm、艺人 asar、专辑 asal 封入 mlit 容器，用 application/x-dmap-tagged 发送。单独发送的封面携带图片 MIME 和当前的 RTP-Info。来源为 pyatv 的 pyatv/support/rtsp.py（MIT）。[控制协议](https://github.com/postlund/pyatv/blob/master/pyatv/support/rtsp.py)
- 接收端可分别拒绝文本标签或封面。RTSP 的 405、415、451、501 表示方法、媒体类型或参数不受支持，454 表示指定会话已不存在。对于已开始播放的会话，前一组响应用于判断可选元数据能力，454 可以识别保持了 TCP 连接的会话终止。来源为 IETF RFC 2326 第 11 节（IETF 公开协议规范）。[RTSP 状态码](https://www.rfc-editor.org/rfc/rfc2326#section-11)

## 第二段接收端验证

- shairport-sync 5.6.3 随镜像提供的 /etc/shairport-sync.conf 与 --help 声明 pipe 后端能选择 FIFO、输出采样率、样本格式；classic 的 sessioncontrol.allow_session_interruption 可允许第二条实际播放连接接管。接收端许可见 COPYING（按文件混合许可），测试使用已发布的二进制。[镜像说明](https://hub.docker.com/r/mikebrady/shairport-sync)
- 替代接收端的公开说明有不同范围。UxPlay 的 README.md 说明其使用 AirPlay 2 legacy protocol（GPLv3）；openairplay/airplay2-receiver 的 README.md 列出实时音频并明确缺少精确 PTP/NTP 同步（仓库未声明统一许可）；Jiajun-Huang/protable-airplay 的 README.md 声明同时支持两种时钟，当前 GitHub Releases 没有 Linux 接收端二进制，仓库未声明统一许可。两个 shairport-sync 镜像使用 FIFO 检查托管播放，另行使用 Python 接收端验证原生流。[UxPlay](https://github.com/FDH2/UxPlay) · [Python 接收端](https://github.com/openairplay/airplay2-receiver) · [portable AirPlay](https://github.com/Jiajun-Huang/protable-airplay)
- 选接收端时还核对了原生会话的实际分支。protable-airplay 的 shared/airplay/airplay2.c 对 NTP 会话级 SETUP 返回 461（仓库未声明统一许可）。Max-Sum/AirPlay2Audio 的 handlers/setup.go 接受请求但声明原生定时使用 PTP（Apache-2.0）。metaneutrons/shairplay-rust 的 src/raop/handlers_ap2.rs 支持实时加密 ALAC，NTP 套接字用于 legacy 流（LGPL-3.0-or-later）。这些行为只能用于判断各接收端可以验证的范围。[protable-airplay](https://github.com/Jiajun-Huang/protable-airplay/blob/HEAD/shared/airplay/airplay2.c) · [AirPlay2Audio](https://github.com/Max-Sum/AirPlay2Audio/blob/HEAD/handlers/setup.go) · [shairplay-rust](https://github.com/metaneutrons/shairplay-rust/blob/HEAD/src/raop/handlers_ap2.rs)
- 进一步核对 openairplay/airplay2-receiver 提交 6c343d3 的 ap2-receiver.py、ap2/connections/{stream,audio,control}.py（仓库未声明统一许可，只记观察）：原生首轮 SETUP 返回 eventPort，timingPort 为零或不返回；实时流返回 dataPort/controlPort。控制端能解析 RTP 同步包中的 NTP 时间，音频端使用 PyAudio 的默认输出，打开设备时固定为每个样本两字节。能返回配置和实际使用 NTP 时钟校准是两个独立观察项。用于隔离试跑的 argyrop/docker-airplay2-receiver 是 2020 年的旧构建，上游仓库当前版本与其分开记录。[控制端](https://github.com/openairplay/airplay2-receiver/blob/6c343d3679ddb561c61566985acaaf587d0a3bd3/ap2/connections/control.py)
- Docker Hub 上的 argyrop/docker-airplay2-receiver（amd64，清单 sha256:9a33a51777f4636ee1444940a16a0780b5515f222fba9e4fb642a7a6a839d297；发布者未声明统一许可）接受原生 NTP SETUP、返回实时流数据端口，实际解密、解码 ALAC 并通过 ALSA file 插件输出匹配源的 44.1 kHz/16 位立体声。它在回复首轮 SETUP 时异步启动事件端口子进程，早期拨号有时返回 ECONNREFUSED。旧版 ap2/connections/control.py 的控制子进程收到 NTP SYNC 时因缺少 syncs 字段报 AttributeError，媒体子进程继续解码；ap2/connections/audio.py 只接受 ALAC 或 AAC-LC 的 44.1 kHz/16 位路径；镜像不能验证完整时钟同步、接收端重传或 48 kHz/24 位。发送端程序与该接收端在不同进程，仅检查输出文件与控制状态。[镜像](https://hub.docker.com/r/argyrop/docker-airplay2-receiver)

## 第三段原生会话

- shairport-sync 5.6.3 的 pair_ap/pair_homekit.c（MIT）把 128 位随机 SRP salt 当作大端整数，在 pair-setup M2 中使用最短编码；开头随机字节为零时盐会短于 16 字节。双方对实际线上字节计算挑战与 M4 证明。本轮测试出现一次挑战被拒，与先前只接受 16 字节的校验相符。[接收端](https://github.com/mikebrady/shairport-sync/blob/5.6.3/pair_ap/pair_homekit.c)

- 在支持 SETRATEANCHORTIME 的接收端，rate=1 时使用 rtpTime、networkTimeTimelineID、networkTimeSecs 和完整 64 位二进制秒小数 networkTimeFrac 标识一个共同的播放时刻。shairport-sync 5.6.3 的 rtsp.c（该文件 MIT）处理这些字段；saundo 的 docs/protocol.md（GPL-3.0-only）记录 HomePod 的实时流会在 plist 返回 status=-12782 忽略命令，由 0xd7 驱动。发送端按实际应答判断，首个锚点和 FLUSH 后的锚点均与同一批音频的 0xd7 对齐。单台 HomePod/Mac 的时钟同步由 RECORD 启动，接收端侧的主时钟应在流级 SETUP 之前锁定。[接收端处理](https://github.com/mikebrady/shairport-sync/blob/5.6.3/rtsp.c) · [设备实测](https://github.com/4202217/saundo/blob/main/docs/protocol.md)

- HomePod 冷启动时接收端主时钟可能要在 RECORD 后才进入稳定状态，等待窗口可能超过十秒。首轮会话可使用本地 clockID，发送音频前须取得该对端已锁定的 grandmaster 时间与偏移。行为参考 saundo 的 docs/devices.md（GPL-3.0-only，仅使用设备行为）。[HomePod 时钟](https://github.com/4202217/saundo/blob/main/docs/devices.md)

- 原生 PTP 会话的 SETUP 用 timingProtocol=PTP。timingPeerInfo 和 timingPeerList 描述发送端地址、ID 和本地 ClockID（plist 内的有符号 64 位整数）；流级 SETUP 前的 SETPEERS 请求携带接收端、发送端的 IP 数组。行为参考 OwnTone 的 src/outputs/airplay.c（GPL-2.0-or-later，仅用协议事实），以及 shairport-sync 5.6.3 的 rtsp.c（该文件 MIT）。此接收端让 nqptp 关注 RTSP 连接实际对端，因此发送端与 sidecar 共用地址能让接收端识别同一主时钟。[发送端](https://github.com/owntone/owntone-server/blob/master/src/outputs/airplay.c) · [接收端](https://github.com/mikebrady/shairport-sync/blob/5.6.3/rtsp.c)
- type 96 实时流通过 28 字节的 0xd7 控制包关联 RTP 时间和 PTP 时间。偏移 4 与 16 各有一个大端 RTP 时间戳，偏移 8 是八字节大端主时钟纳秒时间，偏移 20 是当前 grandmaster 的八字节标识，首包 0x90，其余包 0x80。44.1 kHz 下观察到 77175 帧的两个时间戳差，接收端还计入 11025 帧的渲染延迟。SETRATEANCHORTIME 在该接收端的 type 103 缓冲流承担启动/锚定，type 96 的时间来源是 0xd7 包。行为参考 shairport-sync 5.6.3 的 rtp.c、rtsp.c（两文件 MIT），以及 OwnTone 的 src/outputs/rtp_common.c（GPL-2.0-or-later，仅取线上布局）。[接收端](https://github.com/mikebrady/shairport-sync/blob/5.6.3/rtp.c) · [发送端](https://github.com/owntone/owntone-server/blob/master/src/outputs/rtp_common.c)

## 第三段时钟

- PTPv2 的 Sync 和 Delay_Req 使用 UDP 319；Follow_Up、Delay_Resp、Announce、Signaling 使用 UDP 320。双步 Sync/Follow_Up 共用源端口标识与序号。时间戳为 48 位秒、32 位纳秒；修正字段为带符号的 2⁻¹⁶ 纳秒。以 T1/T4 表示主钟发出 Sync、收到 Delay_Req 的时刻，T2/T3 表示本地收发时刻，扣除修正后，两向传播时间的平均值给出单程延迟。公开报文结构参考 facebook/time 的 ptp/protocol/{protocol,types,unicast,tlvs}.go（Apache-2.0）。[报文定义](https://github.com/facebook/time/tree/main/ptp/protocol)
- nqptp 监听两个固定端口并把接收端所选主时钟的 ID、本地参考时间、到主钟的偏移交给 shairport-sync。两个端口需独占。来源为 nqptp README.md 的 Firewall、Notes 与 Programming Notes（GPL-2.0，阅读文档并运行发行镜像）。[接收时钟说明](https://github.com/mikebrady/nqptp/blob/master/README.md)
- 单台 HomePod 与部分 Mac 会成为主钟，可能仅在 RECORD 后响应单播订阅；接收端时钟可从启动时开始计时。发送端按接收端地址订阅 Announce、Sync、Delay_Resp；立体声组由发送端提供 Announce、Sync、Follow_Up。来源为 saundo 的 docs/protocol.md 第 7、8 节（GPL-3.0，仅阅读文档所记录的真机行为）。[设备行为](https://github.com/4202217/saundo/blob/main/docs/protocol.md)
- 在独立 bridge 内实测 shairport-sync 5.6.3 + nqptp 选中的时钟 ID 与发送端只读接口相同（本次为 92596d0089244587），接收端 FIFO 跨两曲 RMS 误差为 0。两个 ROOMusic 时钟互联时选出同一 GM，从钟测得偏移 22,499 ns；停止主钟后，从钟改任主钟。另一次在两个隔离网段间设置已知的 −123 s 输入偏移，测得 −122,999,957,790 ns，另一网段收到携带同一 GM ID 的 Follow_Up，与观察端相差 −123,000,000,337 ns。均为软件收发时间戳的本机容器实测。

## 第三段接收端验证

- shairport-sync 5.6.3 发布时附带的 nqptp-shm-structures.h 记录了接收端共享时钟状态（nqptp，GPL-2.0-only）。版本 11 的共享区含两份依次更新的时钟读数；两个读数一致且主时钟 ID 与本次发送端 grandmaster 相同，能从真实接收端验证其选钟。验证在接收端容器自身读取 /dev/shm/nqptp，不使用宿主机共享内存。[接收端数据接口](https://github.com/mikebrady/shairport-sync/blob/5.6.3/nqptp-shm-structures.h)
- 隔离运行时，shairport-sync 5.6.3 的调试日志先以 SETRATEANCHORI Start playing 报告播放锚点时钟，随后以 NQPTP master clock 报告其对时时钟。两者与发送端的主时钟标识一致。同一场播放跨两首合成 FLAC 的接收端 FIFO 信号逐样本测得 RMS 误差为 0；暂停后连续静音至少 11025 帧，继续后单声道峰值恢复至 12000。[发行镜像](https://hub.docker.com/r/mikebrady/shairport-sync)
- 生产镜像演练在应用容器的网络命名空间独占 UDP 319/320 后启动对时服务。服务报告 port_in_use；真实 shairport-sync 拒绝原生 NTP SETUP（501），兼容 RAOP 会话仍输出匹配源的 PCM。合成的 AudioAccessory5,1 公告在设备列表标为「精确对时不可用」，且激活返回 409 device_unsupported。同网的项目 Cast 模拟接收端仍能发现、拉取音频并响应暂停。碰撞使用容器内 UDP 套接字，没有发布宿主机端口。

- 本地隔离演练中，正在播第一首曲目时停止发送端时钟服务。接收端先报告原生 NTP 请求无法处理，随后同一应用地址新建 Classic AirPlay 会话。播放器继续保持 play，管理员接口报告 NTP 和 clock_unavailable；第二曲 FIFO 振幅恢复为 12000（源振幅 12000）。来源为本项目的生产镜像演练与 shairport-sync 5.6.3 的运行日志（接收端按 COPYING 分文件许可）。

## 第三段设备差异

- shairport-sync 5.6.3 与 nqptp 的隔离播放中，接收端 `4A:5F:0B:AF:83:FA` 的共享区已选发送端主钟，发送端却曾短暂选中由接收端 MAC 派生的 `4a5f0bfffeaf83fa`，未得到同步测量。nqptp 的发行说明记载它用短暂发起再撤销的主钟选举唤醒静默发送端。其共享区协议指定 T 消息选择跟随的地址、B 消息标记正在播放。本轮接收端还记录了 SETRATEANCHORTIME 启播和 nqptp 锁钟的同一 ID `8240233e1345c57a`。在此型号和时钟身份下，须收到可用的 Sync 时间戳才采信其自荐的主钟。来源为 nqptp 的 RELEASE_NOTES.md（GPL-2.0）和 shairport-sync 5.6.3 附带的 nqptp 接口注释（GPL-2.0）。[唤醒行为](https://github.com/mikebrady/nqptp/blob/master/RELEASE_NOTES.md) · [时钟控制接口](https://github.com/mikebrady/shairport-sync/blob/5.6.3/nqptp-shm-structures.h)

- Unofficial AirPlay Specification 的 src/features.md 把 bit 41 定义为 SupportsPTP。它可以用于选择首选时钟，单凭此位无法判定设备是否接受 NTP。来源为 openairplay/airplay-spec 的协议文档（仓库未声明统一许可，此处仅记录协议字段事实）。[特性表](https://openairplay.github.io/airplay-spec/features.html)
- HomePod 公告用 AudioAccessory 型号族，单只 HomePod 即使应答 NTP 的首轮 SETUP，也可能忽略媒体流。来源为 saundo 的 docs/devices.md（GPL-3.0，公开实机行为描述）。[HomePod 观察](https://github.com/4202217/saundo/blob/main/docs/devices.md)
- Sonos Era 100 的 DNS-SD 使用 model=Era 100，用户报告能完成控制握手但未对时不会出声。同一报告中的 Roam 可以播放。按任务要求对 Era 系列采用必须 PTP 的策略，其他 Sonos 产品按公告能力协商。来源为 OwnTone issue 1853 的实机 DNS-SD 输出和复现记录（项目 GPL-2.0-or-later）。[Era 接收端记录](https://github.com/owntone/owntone-server/issues/1853)

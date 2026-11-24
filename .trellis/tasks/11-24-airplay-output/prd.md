# AirPlay 2 投放

父任务：`11-24-adoption-readiness`（第 5 项）。调研见 `../11-24-adoption-readiness/research/casting-protocols.md` 第 2、3.2 节。依赖：NAS 一键安装（Compose 改写）与 Chromecast（mDNS、设备种类泛化）合入之后。

## 目标

HomePod、Apple TV、Mac、Sonos、AirPlay 音箱与功放出现在播放设备列表里，选中后由服务器把播放推给它们；用法与已有局域网设备一致。

## 已定（qlqq）

- 完整 AirPlay 2：配对、加密、PTP 对时；参考成熟开源实现自己写，只用协议事实与可观察行为，**不翻译或照抄 GPL 代码**（OwnTone、cliairplay、saundo 只看文档与行为描述）；MIT/Apache 代码（pyatv、libraop、airplay2-sender-cpp、brutella/hap）可以参考实现思路，引用时注明出处。
- 主程序容器加固不放弃：PTP 放在独立的小容器，只给绑定低端口的权限。

## 主代理取的默认值

| 项 | 默认 |
| --- | --- |
| 协议档位 | 原生 AirPlay 2（transient 配对，Apple TV 走屏幕 PIN 配对并保存长期凭据）为主；接收端不支持原生时回退 AirPlay 2 兼容模式或 AirPlay 1（RAOP） |
| 计时 | PTP 可用时用 PTP；对时容器没起来或端口被占（如同机有 shairport-sync/nqptp）时回退 NTP，设备行注明“精确对时不可用”，HomePod/Sonos 等必须 PTP 的设备标为不可用 |
| 对时容器 | `ptp` 服务：同一镜像里的 `roomusic-ptp` 程序，root 身份但只有 `NET_BIND_SERVICE`，`cap_drop: [ALL]` 其余全去、`no-new-privileges`、只读根文件系统、host 网络；经 `127.0.0.1` 上的本地端口把时钟偏移交给应用 |
| 音频 | 从渲染会话进程内读取连续 PCM（复用渲染管线与音效），曲间天然无缝；默认 44.1 kHz/16 位（RAOP 固定此值）；AirPlay 2 设备可由管理员开 48 kHz/24 位；HomePod 默认 16 位 |
| 编码 | ALAC 未压缩帧（不引入编码器）；AirPlay 2 音频按协议加密 |
| 暂停 | 暂停期间持续发静音包（HomePod 需要），长时间暂停（10 分钟）后断开会话，继续时重建 |
| 不可用设备 | 需要 FairPlay（特性位 12/14/51）的设备在发现时标为“不支持” |
| User-Agent | `AirPlay/<兼容版本>`，避免新固件按版本拒绝 |
| 设备键 | `airplay:<deviceid>`；共享、分配、占用沿用局域网设备规则 |
| 开关 | 局域网设备设置里的第三个协议开关“AirPlay”，总开关打开后默认开 |
| 多房间 | 不做（一次只推给一台设备） |

## 要求

1. mDNS 发现 `_airplay._tcp`、`_raop._tcp`（复用 Chromecast 的 mDNS 模块），解析 TXT 与特性位，合并同一设备的两条公告。
2. RTSP 客户端、HAP 配对（SRP-6a、X25519、Ed25519、HKDF、ChaCha20-Poly1305）、加密控制通道、二进制 plist、SETUP/RECORD/SETRATEANCHORTIME/FLUSH/TEARDOWN、音量、元数据（标题、艺人、专辑、封面）、事件通道。
3. RTP 打包、发送节奏、重传请求处理；RAOP 路径（RSA/AES）。
4. `roomusic-ptp`：PTP（IEEE 1588 两步，作为 master 或 follower 按接收端需要），本地接口给应用读时钟；Compose 加 `ptp` 服务；部署合同更新。
5. 托管输出：与局域网设备一致的 Acquire/占用/接管/释放；被其他发送端顶掉时视为设备端停止。
6. 前端：设备列表“AirPlay”标签、Apple TV 的 PIN 输入流程、设置里的协议开关与单台设备格式；不支持的设备显示原因。
7. 匿名统计的 `devices_30d` 增加 `airplay`。

## 验收

- 测试用真实接收端：shairport-sync（AirPlay 2 构建，带 nqptp）与 shairport-sync classic（AirPlay 1），各自运行在独立的 Docker 网络里，发送端在同一网络的容器中：
  发现、配对、播放（接收端管道输出里有非静音的 PCM，且与源一致）、暂停（静音包）、继续、曲间无缝（跨曲目连续）、音量、被顶掉、离线。
- 生产镜像：主程序 `cap_drop: [ALL]` 不变；`ptp` 服务只有 `NET_BIND_SERVICE`；端口被占时回退并在界面显示。
- 出站只到局域网地址。
- 390 与 1440 截图核对设备列表、PIN 输入与设置。
- HomePod、Apple TV、Mac、Sonos 真机由 qlqq 试用。

## 不做

- 多房间同步、AirPlay 视频与屏幕镜像、FairPlay 设备。

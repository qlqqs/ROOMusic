# 设计：SlimProto 服务端（单台）

协议事实以 `research/slimproto-protocol.md` 为准（下文写作“调研 §n”）。实现时用自己的代码，不复制 Squeezelite 或 Lyrion Music Server 的源码。

## 1. 边界与包

| 位置 | 职责 |
| --- | --- |
| `backend/internal/devices/slimproto/`（新） | 纯协议与连接管理：帧编解码、HELO/STAT 解析、命令构造、UDP 发现应答、TCP 监听与每台播放器的连接、心跳与离线；`Output`（托管输出会话，对接 `zones.Subscription`），写法参照 `devices/cast/output.go` |
| `backend/internal/devices/upnp/` | 局域网设备的公共层（设置、可见性、分配、占用、格式存储）。新增 `slimproto.go`：注册表适配器与设备投影；`service.go`、`output.go`、`formats.go`、`store.go` 接入第四种协议 |
| `backend/internal/devices/upnp/httpapi` | 设置与格式接口接受新字段 |
| `backend/migrations/0078_slimproto_devices.sql` 与 `.down.sql` | 设置列与设备格式表 |
| `frontend/src/features/lan-devices/` | 开关、设备行、音质设置 |
| `scripts/test-slimproto.sh`、`scripts/slimproto/` | 真实 Squeezelite 的 Docker 测试，结构参照 `scripts/test-airplay.sh` 与 `scripts/airplay/` |

`slimproto` 包只依赖 `zones`（`Output` 需要）与 `devices/lan`（地址规则），不依赖 `upnp`。依赖方向与 `cast` 相同。

## 2. 协议层（`slimproto`）

### 2.1 帧

- 下行（服务器→播放器）：`u16 大端长度（= 4 + 载荷）` + 4 字节命令字 + 载荷，整帧不超过 4096 字节（播放器超限即断开，调研 §3）。构造函数超限返回错误，不截断。
- 上行（播放器→服务器）：4 字节命令字 + `u32 大端长度（只算载荷）` + 载荷。先读 8 字节头再校验长度：载荷超过 64 KiB 即断开（已知报文都远小于此；RESP 带 HTTP 头，按 64 KiB 上限）。处理半包与粘包。
- 只处理 `HELO`、`STAT`、`DSCO`、`RESP`、`SETD`；其他命令字读完丢弃，不断开（调研 §11）。

### 2.2 发现（UDP 3483）

- 监听 `0.0.0.0:3483`（UDP）。首字节 `e` 时回 `E`；首字节 `d` 时回 `D` 加 17 字节名称（调研 §1 给出字段）。名称用实例名（与 mDNS 公告同源；没有就用 `ROOMusic`）。
- 来源地址必须通过 `devices/lan` 的局域网 IPv4 规则，否则丢弃。应答单播回源地址与源端口，不发往广播或组播地址。
- 应答必须从播放器之后要连的那个 IP 发出（播放器只取源 IP）。监听用 `0.0.0.0` 时，回包取路由到来源的本机地址：对每个来源用 `net.DialUDP` 连一次求本机地址，再从绑定该地址的套接字发出；或每个局域网网卡各绑一个 `ip:3483` 套接字。实现时取后者：网卡集合用 `devices/lan` 已有的枚举，30 秒刷新一次，与 SSDP 的网卡刷新同节奏。

### 2.3 连接（TCP 3483）

- 监听 `0.0.0.0:3483`（TCP）。来源不是局域网 IPv4 的连接立即关闭。并发连接上限 64，超过的新连接直接关闭并记日志。
- accept 后 5 秒内必须收到 HELO，否则关闭。解析 HELO（调研 §2）：deviceid、revision、MAC、重连位、能力串。
- 能力串按逗号切分，取 `Model`、`ModelName`、`Firmware`、`MaxSampleRate`、`AccuratePlayPoints`，以及编解码标记（`flc` 等）。数值解析失败按缺省处理（`MaxSampleRate` 缺省 44100）。
- 设备键 `slim:<小写 MAC，冒号分隔>`。同一 MAC 再次 HELO 时，关闭旧连接，用新连接替换；对正在运行的托管输出表现为一次断线（见 3.4）。
- HELO 之后按顺序发：`aude 1,1`、`audg`（当前音量；无托管输出时按 1.0）、`setd` id 0（查询播放器名称）、`strm q`。收到 `SETD` id 0 时更新设备名称。
- 心跳：每 5 秒发一次 `strm t`（时间戳字段填 0）。每收到一个 STAT 刷新最后活动时间；15 秒没有 STAT，或读到 EOF、出错，判离线并关闭连接。
- 写：每条连接一个写协程与有界发送队列（64 条），队列满或写超时 5 秒即断开。读写都不在任何全局锁内进行。

### 2.4 STAT 与时钟

- 解析 53 字节载荷（调研 §6），载荷不足时丢弃该包，连接继续。
- jiffies 是 32 位毫秒计数，按回绕展开成 64 位。
- 时钟偏移：每个 STAT 计算 `服务器接收时刻 − jiffies`，取最小值作为该播放器时钟零点。窗口取最近 50 个样本、至少 10 个，每 30 秒重算一次最小值，用来跟住时钟漂移。单台播放时只用它来外推位置；多台同步（下一个子任务）会复用同一个估计器，所以写成独立类型并有单元测试。
- 位置：`本条流的起始偏移 + elapsed_ms / 1000`。播放中按 `现在 − 该 STAT 的服务器接收时刻` 外推，不超过时长。

### 2.5 命令

`strm` 固定 24 字节头加请求串（调研 §4）。本任务只用以下几种：

| 用途 | 命令 | 要点 |
| --- | --- | --- |
| 开流 | `s` | autostart `1`；格式 `f`，PCM 字段全填 `?`；threshold 255（KB）；采样率 ≥ 176400 时 output_threshold 20；transition `0`；flags 0；server_port 为 HTTP 监听端口；server_ip 为本连接的本机 IPv4；请求串为 `GET <path> HTTP/1.0\r\nHost: <ip:port>\r\n\r\n` |
| 暂停 / 继续 | `p` / `u` | 时间字段 0 |
| 停止 | `q` | — |
| 取消预取的接续流 | `f` | — |
| 心跳 | `t` | 时间字段 0 |

`audg`：16.16 定点增益（`0x10000` = 0 dB），新旧两种增益字段都填。音量 0–1 按对数曲线换算成增益，具体曲线在实现时取 Lyrion 的公开行为作参照，写成单元测试固定下来。静音发增益 0。设备设为 `fixed` 时 adjust 字节置 0（调研 §8）。

### 2.6 托管输出 `slimproto.Output`

接口与 `cast.Output` 相同：`Run(ctx, sub *zones.Subscription) zones.ReleaseOutputInput`。字段：`Zones`、`UserID`、`DeviceID`、`ZoneID`、`SessionID`、`BaseURL`、`Player`（连接句柄）、`Logger`、`Offline`。

状态机：

| 事件 | 动作 |
| --- | --- |
| 区要求播放、有当前流且未开 | 取 `ManagedStream` 的路径，发 `strm s`；记 `seq` 与起始偏移；回报 `loading` |
| STMs | 本条流开始出声，回报 `playing`，elapsed 起点归零 |
| 区要求暂停 / 继续 | `strm p` / `strm u`，以 STMp / STMr 为准回报 |
| 跳转、上一首、下一首（区给出新流） | `strm q`，再对新流 `strm s` |
| 当前流已交付完（`Delivered`），且区给出接续流 | 等该流的 STMd 后，才对接续流发 `strm s`；接续流的 STMs 到达时回报 `playing` 并切换 `seq`（与 UPnP 的接续晋升规则一致） |
| STMu（全部播完）且没有接续流 | 回报 `ended` |
| STMo（欠载） | 回报 `buffering`，记日志；播放器缓冲回升后由 STMs/STMr 或下一次 STAT 恢复 `playing` |
| STMn 或 DSCO 原因非 0 | 回报出错（沿用托管输出的 `stream_lost` 恢复路径：在区锁内记播放头，重新开流） |
| 连接断开或离线 | 调 `Offline`，返回当前位置供 `ReleaseOutput` 使用，播放区按既有离线路径暂停 |
| 音量变化 | 发 `audg` |

位置回报：每秒一次（播放中），用 2.4 的外推值。

### 2.7 取流

沿用托管输出的令牌拉流 `GET /upnp/streams/{token}`（`zones.Service.ManagedStream` 与现有 HTTP 处理）。需要核对并保证三点：

1. 请求行是 HTTP/1.0 时，响应不分块、写完关闭连接（Go `net/http` 对 HTTP/1.0 请求的默认行为；加一条测试固定）。
2. 暂停时播放器停止读取，写操作被 TCP 反压阻塞。这时不能因写超时断开；处理函数不设写截止时间，由会话结束或令牌失效关闭连接（与 UPnP 设备端长时间暂停后续播的规则一致）。
3. 按 TCP 对端校验来源（局域网 IPv4），与现有规则相同。

## 3. 公共层接入（`devices/upnp`）

### 3.1 设置

- `upnp_settings` 加列 `slimproto_enabled boolean NOT NULL DEFAULT true`。`Settings` 加字段 `SlimProtoEnabled`，JSON `slimproto_enabled`；`active("slimproto")` 同样要求总开关 `enabled`。
- `GET/PUT /settings` 的响应加只读字段 `slimproto_unavailable`：`null` 或 `"port_in_use"`。
- 3483（TCP 或 UDP）绑定失败不算设置失败：协议标记 `port_in_use`，`PUT` 仍返回 200，其他协议照常启动；之后每 30 秒重试绑定，成功后清除标记（同机的 Lyrion Music Server 停掉后自动可用）。

### 3.2 注册表适配器

`slimRegistry` 实现 `deviceRegistry`：

- `Start`：启动发现与 TCP 监听；`Stop`：关闭监听、断开所有播放器、等待协程退出。
- `List`、`Lookup`：在线播放器（HELO 成功且未离线）的快照；离线后从列表中移除（不保留离线行：设备由播放器主动连接，没有手动登记）。
- `Probe` 返回 `ErrUnsupported`（`Add` 对 `kind:"slimproto"` 映射为 `400 invalid_input`）；`Remember`、`Forget` 为空操作。

设备投影：`Device{Kind:"slimproto", UDN:"slim:<mac>", Name, Model, Address: 播放器 IP, LocalAddress: 连接本机地址, Capabilities{Pause:true, Volume:"number"|"fixed", StreamFormats:["flac"], FormatDeclared:true, MaxSampleRate, MaxBitDepth:24, SlimProto:{model_name, firmware, declared_max_sample_rate, unsupported}}}`。

- 能力串没有 `flc` 时 `unsupported:true`，`Acquire` 返回 `409 device_unsupported`（复用现有错误码）。
- `MaxSampleRate` 取格式设置与播放器声明的较小值，再封顶 384000：ffmpeg 的 FLAC 编码器不接受更高的采样率（实现时用 `scripts/ffmpeg-pinned.sh` 的 ffmpeg 实测确认；若实测支持更高，按实测值封顶，并在 spec 里记录）。

`deviceKind` 增加前缀 `slim:` → `slimproto`；`startProtocols` 的协议列表加 `slimproto`；`Acquire` 增加 `slimproto` 分支，启动 `slimproto.Output`，写法与 Cast 分支一致。

### 3.3 音质设置

- 新表 `slimproto_device_formats(udn text PRIMARY KEY, max_sample_rate integer NULL, volume text NOT NULL DEFAULT 'number' CHECK (volume IN ('number','fixed')))`。`max_sample_rate` 为 NULL 表示跟随播放器声明。
- `PUT /api/v1/admin/upnp/devices/{udn}/format`：SlimProto 设备接受 `{"codec":"flac","max_sample_rate":<int|null>,"volume":"number"|"fixed"}`。`max_sample_rate` 必须是 44100/48000/88200/96000/176400/192000/352800/384000 之一，且不超过播放器声明值，否则 `400 invalid_input`。设备行的 `format` 返回同样的对象。下一条流起生效；音量方式立即生效（重发 `audg`）。
- 设备离线时也可以设置（按 UDN 保存）。

### 3.4 断线与重连

- 控制连接断开或判离线：注册表移除该行，托管输出按既有路径释放并暂停播放区。
- 同一 MAC 重新连上：作为新的在线设备出现；成员需要重新选择（与其他托管设备离线后的行为相同）。

## 4. 播放区

- 协商沿用 `playback-zone-contracts.md` §6：设备能力只声明 `flac`，采样率上限按 3.2。DSD 来源按 `dsd-contracts.md` 转成 PCM；设备上限 ≥ 352800 时目标为 DXD。
- 不改 `PUT /devices/{id}/sample-rate`（仍只对 UPnP）。

## 5. 前端

- 局域网设备设置：协议开关列表加 SlimProto；`slimproto_unavailable` 为 `port_in_use` 时开关旁显示警示图标，提示文字说明“3483 端口已被占用（例如同机的 Lyrion Music Server）”。
- 设备行：`kind:"slimproto"` 用自己的图标；显示型号、固件和最高采样率。没有手动添加入口。
- 音质设置：采样率上限（选项不超过播放器声明值，另有“跟随设备”）与音量方式（可调 / 固定）。
- 解码器与 API 契约测试按新字段更新。遵循 `.trellis/spec/frontend/` 与记忆里的界面约定：图标优先，文字进提示与读屏名。

## 6. 测试设施

- 单元测试：帧编解码（半包、粘包、超长、未知命令）、HELO 与能力串、STAT、命令构造的字节、jiffies 回绕、时钟估计器、音量换算、发现应答的来源过滤。
- 进程内假播放器（`slimproto` 包内的测试辅助）：能完成 HELO、回 STAT、按 `strm s` 发 HTTP 请求并读流，用于 `upnp` 层与 `Output` 的集成测试，覆盖开流、暂停、继续、接续流、断线。
- 真实播放器：`scripts/test-slimproto.sh` 在独立 Compose 项目与网络里跑 ROOMusic 测试实例加一台 Squeezelite（Alpine 的 `squeezelite` 包）。Squeezelite 用 `-o - -r <采样率>` 输出到标准输出，接一个按名义采样率读取的计时程序（Python，记录读出的字节数与时刻）。覆盖：广播发现（不带 `-s`）、96k 与 192k 原采样率交付、上限调低、同格式无缝与换采样率接续、暂停继续、杀掉播放器后离线。
- 3483 占用：测试里先占住端口再启动，核对 `port_in_use` 与其他协议照常。

## 7. 兼容与回滚

- 迁移只加列与新表；`.down.sql` 删除新表与新列。
- 局域网设备总开关默认关；未开启的实例不监听 3483，行为不变。
- 回滚：回退代码并执行 down 迁移即可，没有数据转换。

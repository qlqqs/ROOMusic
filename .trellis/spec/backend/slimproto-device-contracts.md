# SlimProto 播放器合同

局域网里的 Squeezelite 类播放器经 SlimProto 连上 ROOMusic，作为托管局域网输出使用。设置、可见性、分配、占用与托管输出的公共规则见[局域网 UPnP 渲染器合同](./upnp-device-contracts.md)，下面只列不同之处。

来源：任务 `11-25-slimproto-squeezelite`（协议事实见任务调研 `research/slimproto-protocol.md`，由阅读 Squeezelite 与 Lyrion Music Server 源码整理，不复制源码）。

## 1. 范围

- 单台播放：发现、连接、FLAC 长流、暂停继续、跳转、音量、断线（§3）。多台同步编组见 §8 与[播放区协议：托管的局域网输出与编组 §11](./playback-zone-lan-contracts.md#11-编组slimproto-多台同步)。
- 不做：PCM 裸流、DoP/原生 DSD 直通（DSD 来源按 [DSD 合同](./dsd-contracts.md) 转成 PCM）、设备端暂停与继续的映射（Squeezelite 没有这类上行报文）、手动添加、备用端口、Lyrion 的 CLI 与 JSON-RPC。

## 2. 签名

| 位置 | 签名 |
| --- | --- |
| `internal/devices/slimproto` | `NewRegistry(name string, logger *slog.Logger, configure func(ctx, key string, declaredRate int) (fixed bool, err error)) *Registry`；`Start(ctx) error`、`Stop()`、`List() []Device`、`Lookup(key)`、`PortInUse() bool`、`Offline(key, *Player)`；`Output.Run(ctx, *zones.Subscription) zones.ReleaseOutputInput`，与 `cast.Output` 同形 |
| `internal/devices/upnp/slimproto.go` | 注册表适配器（实现 `deviceRegistry`）与设备投影 |
| 迁移 | `0078_slimproto_devices`：`upnp_settings.slimproto_enabled boolean NOT NULL DEFAULT true`；新表 `slimproto_device_formats(udn PK CHECK udn LIKE 'slim:%', declared_max_sample_rate, max_sample_rate NULL, volume 'number'|'fixed')`；`playback_devices` 的种类约束加入 `slimproto`，其采样率约束为 1–768000 |
| 测试替换点 | `listenPort`（默认 3483）、`refreshInterval`（30 秒）、`listLinks` |

## 3. 协议与运行合同

### 3.1 端口与发现

- UDP 与 TCP 都监听 3483。总开关 `enabled` 与 `slimproto_enabled` 都为 true 时才监听。
- 发现：每个局域网 IPv4 网卡各绑一个 `ip:3483`，另收发往 `255.255.255.255` 的广播，按入站网卡回包。首字节 `e` 回 `E`，`d` 回 `D` 加 17 字节名称。应答从入站网卡的单播地址发往请求的源地址和源端口；来源必须是局域网 IPv4 或回环，否则丢弃。网卡集合每 30 秒按差集刷新。
- 3483 绑定失败（例如同机运行 Lyrion Music Server）时，`PUT /settings` 仍返回 200，`slimproto_unavailable` 为 `"port_in_use"`，其他协议照常；每 30 秒重试绑定，成功后恢复为 `null`。

### 3.2 连接

- 来源不是局域网 IPv4 的 TCP 连接立即关闭；并发连接上限 64。
- 5 秒内必须收到 HELO。设备键 `slim:<小写 MAC>`；同一 MAC 再次 HELO 时替换旧连接。
- HELO 之后发 `aude 1,1`、`audg`、`setd` id 0、`strm q`。每 5 秒发 `strm t`；15 秒没有 STAT 判离线。
- 上行帧载荷超过 64 KiB 断开；下行帧整帧不超过 4096 字节。每条连接一个写协程，有界队列 64 条，写超时 5 秒。所有网络 IO 都不在 `upnp.Service.mu` 内。

### 3.3 开流与状态

- `strm s`：单台 autostart `1`（编组见 §8），格式 `f`，PCM 字段全 `?`，threshold 255 KB，server_port 为 HTTP 端口，server_ip 为本连接的本机 IPv4，请求串 `GET /upnp/streams/{token} HTTP/1.0`。
- `output_threshold`（载荷 `p[12]`）以 0.1 秒为单位。采样率 `rate` ≥ 176400 Hz 时取 `min(20, 3528000 / rate)`，使用整数除法。低于 176400 Hz 时该字段为 0。Squeezelite 1479 之前不按缓冲容量下调起播门槛，因此将其限制在默认输出缓冲 441000 帧的 80%（352800 帧）。
- STMs 回报 `playing`；`strm p`/`strm u` 以 STMp/STMr 为准；跳转与上下首为 `strm q` 后重新开流；取消预取发 `strm f`。
- 同格式曲目在一条长流里。格式变化时，只有当前流已交付完（`Delivered`）、收到 STMd、且播放区给出接续流，才对接续流发 `strm s`；接续流的 STMs 晋升 `seq`。STMu 且没有接续流时回报 `ended`。
- 位置：本条流起始偏移加 `elapsed_ms`（Squeezelite 已扣除声卡缓冲），播放中每秒回报。
- 断线或离线：注册表移除该行，按既有离线路径释放输出并暂停播放区。

### 3.4 取流

沿用托管输出的 `GET /upnp/streams/{token}`。请求是 HTTP/1.0 时，Go 不分块写出，写完关闭连接表示流结束。播放器暂停时停止读取，写操作被反压阻塞，处理函数不设写截止时间，只在请求、会话或流句柄结束时关闭。

### 3.5 音量

`audg` 用 16.16 定点增益，新旧两组字段都填；音量 0–1 按两段对数曲线换算（≤0.25 段与 >0.25 段），静音发增益 0。设备设为 `fixed` 时 adjust 字节为 0，播放器按原电平输出。

## 4. 接口

| 方法与路径 | 请求与响应 |
| --- | --- |
| `GET/PUT /api/v1/admin/upnp/settings` | 增加可写 `slimproto_enabled` 与只读 `slimproto_unavailable`（`null` 或 `"port_in_use"`）。样本 `settings.json`、`slimproto-port-in-use.json` |
| `GET …/devices` | `kind:"slimproto"` 的行：`capabilities.slimproto = {model_name, firmware, declared_max_sample_rate, unsupported}`，`stream_formats:["flac"]`，`max_bit_depth:24`；`format = {codec:"flac", max_sample_rate:int|null, volume}`。样本 `slimproto-list.json` |
| `PUT …/devices/{udn}/format` | `{"codec":"flac","max_sample_rate":<int|null>,"volume":"number"|"fixed"}`，样本 `slimproto-format.json` |
| `POST …/devices` | `kind:"slimproto"` 返回 `400 invalid_input`（播放器主动连接，不手动添加） |

## 5. 校验与错误

| 情形 | 结果 |
| --- | --- |
| `max_sample_rate` 不在 44100/48000/88200/96000/176400/192000/352800/384000/705600/768000 中，或超过播放器声明值 | `400 invalid_input` |
| `codec` 不是 `flac`、`volume` 不是两种之一 | `400 invalid_input` |
| 能力串没有 `flc` 的播放器被激活 | `409 device_unsupported`；设备行 `unsupported:true` |
| 3483 被占用 | 设置照常保存，`slimproto_unavailable:"port_in_use"` |
| 采样率上限 | `min(格式设置或声明值, 768000)`。前提是输出高于 48 kHz 时 FLAC 块大小固定为 4096 帧（`playback/render/encode.go` 的 `-frame_size 4096`；48 kHz 及以下 ffmpeg 默认最多 4608 帧，保持默认）：Squeezelite 2.0.0 为 FLAC 解码只预留 200 KiB，ffmpeg 默认块大小随采样率增大，352.8 kHz 起每块 32768 帧、解码要 256 KiB，播放器出声片刻即停（2026-11-25 实测，任务调研 `research/ffmpeg-flac-rates.md`） |

## 6. 测试

- 单元：帧编解码、HELO 与能力串、STAT 偏移、`strm`/`audg` 字节、jiffies 回绕、时钟估计器、发现的来源过滤与回包地址。
- 进程内假播放器：HELO 超时、同 MAC 替换、离线、`Output` 状态机（开流、暂停继续、接续流只在 STMd 后发出、`ended`、断线）。
- PostgreSQL 集成：设置字段、端口占用与恢复、手动添加拒绝、`device_unsupported`、格式接口、HTTP/1.0 取流不分块；迁移往返有 `rollbackFixtures[78]`。
- 真实播放器：`sg docker -c scripts/test-slimproto.sh`，Alpine 的 Squeezelite 用 `-o -` 接按名义采样率读取的计时程序。

## 7. 错误与正确做法

```text
错误：格式变化时一拿到接续流就发 strm s。播放器会丢掉当前流里还没解码的数据。
正确：等当前流交付完且收到 STMd，再对接续流发 strm s。
```

```text
错误：FLAC 用 ffmpeg 默认块大小。352.8 kHz 起每块 32768 帧，Squeezelite 解码缓冲装不下，出声片刻即停。
正确：输出高于 48 kHz 时固定 -frame_size 4096，每块解码只要 32 KiB；48 kHz 及以下保持默认（最多 4608 帧），不拉长低采样率的块时长。
```

```text
错误：前端按 codec 区分设备格式。Cast 与 SlimProto 都可能是 "flac"。
正确：按设备 kind 选格式解码器（frontend/src/features/lan-devices/api/decoders.ts）。
```

## 8. 编组同步

来源：任务 `11-25-slimproto-sync-groups`（机制调研 `research/sync-algorithm.md`，规划 `research/group-model-plan.md`）。接口、不变式与错误见[播放区协议 §11](./playback-zone-lan-contracts.md#11-编组slimproto-多台同步)。

### 8.1 结构

| 位置 | 职责 |
| --- | --- |
| `zones/group_dispatcher.go`、`group_stream.go`、`groups.go` | 一次渲染，共享分块缓冲上限为 `max(8 MiB, 30 秒码率)`，每台设备独立令牌与读位置。主设备直读；`Options.NewMemberStreamFilter` 为每条非主设备流创建过滤器，未注入时所有读者直读。`ManagedGroupStream` 返回成员的 `GroupMemberStream.Drift`；主设备为 nil。超限、8 秒未取流、过滤失败均单独剔除，二次 GET 得 `ErrStreamGone`。名册与回报走 `GroupMembers`、`ReportGroupMember`，占用走 `GroupOutputs{AcquireMember, ReleaseMember}` |
| `playback/render/member_filter.go`、`drift_frames.go`、`drift_sinc.go` | 首次 GET 共用渲染工具一次预约两个 ffmpeg 名额，按共享 FLAC 的 16/24 位整数 PCM 解码和重编码，沿用 FLAC 编码参数（高于 48 kHz 时块长 4096）。每个完整调整块长 L ≥ 2000 个输出帧，消耗 L±1 个源帧。第 i 帧按源位置 i×(L±1)/L 分声道插值，使用 32 抽头 Kaiser 窗 sinc（β=8、1024 个分相间隔，系数线性插值）。计算后四舍五入并裁剪回原位深。块间保留真实上下文，块外及不足一块的尾段逐位直通。块的右边界记录累计净调整，块内按最近整数换算播放点 |
| `devices/slimproto/group.go`、`group_members.go`、`group_timing.go` | 协调器在 `strm s` 时把采样率与补偿器存入对应流，接续流随 `STMs` 晋升。编组时替换主输出的单设备循环，独占各台的命令发送，排空各台事件通道 |
| `devices/upnp/group.go` | 成员占用进 `owners`（复用 `Acquire` 循环与接管）；`Acquire` 的“同一用户同一设备在用”判断不含成员占用；主输出关闭前等全部成员归还 |

### 8.2 时钟

- 每个 STAT 计算 `服务器接收时刻 − jiffies`（32 位回绕展开），更小的偏移立即采用为零点；漂移超过 1 ms 时按窗口上调（≤ 5 ms 或窗口满）。估计器在 `Player` 内，单台播放的位置外推也用它。
- 入组与起播前 2 秒内以 250 ms 间隔发 `strm t` 预热，之后每秒一次。
- `strm u` 的目标换算成该台 jiffies：不能为 0（为 0 时取 1），必须在 10 秒窗口内，否则不下发并重新开流。

- 成员每秒采一个新的播放点，以“源时间轴落后量 + 已播放净删帧的时长”为因变量。最近 60 秒内至少有 15 个不同播放点且跨度达到 20 秒时，带截距做线性回归，斜率乘以一百万得到 ppm。之后每隔至少 10 秒更新一次，限幅 ±500 ppm。新流发送 `strm s` 前立即对其调用 `SetRate(ppm float64)`；预取流也接收后续速率更新。
- 换流、暂停后继续、成员同步微调或成功发送硬修正时重新积累样本，已有 ppm 随成员保留。成员脱离租约或重新入组时从零估计。
- 前馈按输出帧数累积小数帧，满一帧后与 `Adjust(frames)` 的净欠量合并。偏移欠量用 2000 帧调整块，纯前馈用累计到下一整帧的间隔。实时成员流一次最多预读约 3 秒 PCM；更慢的速率先直通间隔前段，再在完整末段拉伸，保留小数余量。两类调整共用每 2000 个输出帧最多插删一帧的上限。

### 8.3 流程与取值

| 场景 | 做法 |
| --- | --- |
| 开新流 | 每台 `strm q` → `strm s` autostart `0`（各自令牌路径）→ 全部 `STMl` 后，T = 现在 + 300 ms + (最大微调 − 本台微调)，每台 `strm u (T 的本台 jiffies)`；8 秒内未到 `STMl` 的成员标 `dropped` |
| 维持同步 | 每秒以主设备的表观开始时刻为基准（播放点时刻 − 源 elapsed + 微调）。成员 elapsed 换成输出帧号，按 `Source(output)` 换回源时间轴。原始偏差绝对值 ≤ 20 ms 时，将落后帧数加上从播放点到成员渲染头预计累积的漂移，再扣除 `Pending(output)`（已请求未写出及已写出尚未播到的净调整）。补偿器的 `RenderState(output int64) (head, pending int64)` 同时读取这两个计数。剩余偏差绝对值 > 0.5 ms 才 `Adjust(frames)`，正值删帧追赶、负值插帧等待。原始偏差绝对值 > 20 ms 或成员无补偿器时，落后 10 ms–10 s 的成员收 `strm a` 跳过相应时长，超前 10 ms–10 s 的成员收带正毫秒间隔的 `strm p`，播放该时长的静音后自行继续，无需等待 `STMp`。任一命令发送成功后，抵消该播放点的净待调整量。主设备始终免调；任一所需播放点超过 3 秒未更新则跳过，同一成员修正间隔至少 1 秒，须得到新的播放点 |
| 暂停 / 继续 | 全组 `strm p`；继续时与开新流用同一套时刻：T = 现在 + 300 ms + (最大微调 − 本台微调)，每台 `strm u`；任一台没有确认暂停（STMp）时回报 `stream_lost` 重新开流。不按 STMp 的 elapsed 差错开：elapsed 按输出块累加、粒度粗，实测会把亚毫秒的差拉到 35 ms（2026-11-25） |
| 接续流（格式变化） | 全部活动成员都 Delivered 且都报 `STMd` 后，原成员 `strm s` autostart `1` 接在缓冲后面；在格式边界重新加入的成员没有旧缓冲，用 autostart `0` 按统一时刻接入 |
| 回报 | 参考成员为主设备；主设备连接仍在但当前流单独故障时，由剩余活动成员临时回报位置；全部 `STMu` 才回报 `ended`；单台 `STMn`/`DSCO`/`STMo` 只剔除那一台 |
| 掉线 | 成员 5 秒无 STAT 或连接断开即 `offline`，其余继续；下一次开流前对 `waiting`/`dropped`/`offline` 成员尝试重新入组，只认领空闲设备（被占用为 `busy`）；主设备掉线整组暂停 |

### 8.4 测试

- 单元（`testing/synctest` 驱动生产循环的时钟）：时钟估计、`strm u`/`strm a` 的取值、暂停继续、接续流门槛、单台故障剔除、主设备掉线。
- PostgreSQL 集成：编组接口、容量与角色守卫、成员区守卫、组音量、微调、重启恢复；分发器的字节一致与慢读者隔离；迁移 0079 往返。
- 真实播放器：`sg docker -c scripts/test-slimproto-sync.sh`，两台 Squeezelite 接按计划出声时刻计时的读取程序，比较每秒脉冲的时刻差。成员 +100 ppm 的验收从开播 60 秒后计稳态，要求 p95 ≤ 1 ms、峰值 ≤ 2 ms，预热期（开播 60 秒内）峰值 ≤ 15 ms，全程无跳帧或带间隔的静音命令。
- 跳帧可听性：`sg docker -c scripts/test-slimproto-skip.sh`，成员 +300 ppm，播放正弦、和弦、白噪声三段，核对主设备逐帧一致和成员调整块的跳变与 8 kHz 以上能量，导出代表性调整块的同时段 WAV 与 `report.json` 到 `temp/slimproto-skip/<时间戳>/`。Squeezelite 跳帧直接移动读指针、不淡化，2026-11-25 实测正弦与和弦切点的样本跳变高出段内 p99 12.7–25.3 dB、8 kHz 以上能量高出基线 74–91 dB，白噪声段无可测差别。
- 版本矩阵：`sg docker -c scripts/test-slimproto-versions.sh [MICRO_VERSION]`，从上游固定提交构建 piCorePlayer 9/10/11.1（1468/1476/1524）、moOde 10.3.7（1541）、Debian 13（1517）与现行 1613，逐个跑单台套件和 60 秒、+300 ppm 的编组套件。两个原入口接受 `ROOMUSIC_SLIMPROTO_PLAYER_IMAGE` 传入现成镜像，不传时自建自删。
- 读端计时（`scripts/slimproto/sync-clock.py`）：读线程晚醒后追读时，只把观察到空管道后实际等待 PCM 的时间计为欠供，累计超过 5 ms 才移动计划出声时刻。旧实现把调度迟到整段记成欠载并重置该台时钟，两台各自重置的差会伪装成同步偏差、触发硬修正（2026-11-25 定因：只暂停读进程 200 ms，1.6 ms 的实际阻塞被记成 178 ms）。1524 首轮的 44 ms 峰值与补偿分支早期的无漂移超限都属于此类。
- 验收调整（任务 `11-25-slimproto-drift-compensation` D8–D11）：带限插值的样本间峰值会让相邻差超过源的 p99，不作为咔哒判据；改为与同核理想分数延迟参考逐样本比对（16 位 ≤ 1 LSB、24 位 ≤ 4 LSB）加 8 kHz 以上能量 ≤ +6 dB。+300 ppm 超出补偿器在预热期（首估 20 秒 + 约 20 秒缓冲）的追赶能力，开播 90 秒内允许一次硬修正。

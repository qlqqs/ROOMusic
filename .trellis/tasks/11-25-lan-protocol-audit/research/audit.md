# 审查：AirPlay、Cast、UPnP 局域网实现（C1–C13）

- **依据**：`.trellis/tasks/11-25-lan-protocol-audit/prd.md` 的检查清单
- **范围**：`backend/internal/devices/{upnp,cast,airplay,mdns,lan}`、`backend/internal/ptp`、`backend/cmd/roomusic-ptp`、`backend/internal/zones` 中托管输出相关文件，部署文档 `deploy/production/README.md`、`docs/install-nas.md`
- **方式**：逐文件读代码；本轮运行了 `mise exec -- go vet ./internal/devices/... ./internal/ptp/... ./cmd/roomusic-ptp/`（无输出，通过）。没有运行测试。
- **日期**：2026-11-25
- **说明**：证据写成 `相对仓库根的路径:行号`。“确认缺陷”指从代码能说清触发条件与后果；“风险”指可能出问题，但要靠测试确认。

## 1. 总表

| 编号 | 结论 | 严重程度 | 一句话 |
| --- | --- | --- | --- |
| C1 | 有问题 | 中 | 各网卡分别加入组播、出站指定网卡已满足；但网卡只在启动时枚举一次，地址或网卡变化后不重建。启动时没有局域网网卡，mDNS 和 SSDP 会“启动成功”却一直收不到任何设备 |
| C2 | 待验证 | 中 | SSDP、RTP、PTP 都能收到回包。mDNS 定向查询从共享的 5353 端口发出，单播应答可能被本进程的浏览套接字或 avahi 收走 |
| C3 | 已满足 | — | CASTV2、RTSP、bplist、HAP、mDNS、SSDP、PTP 都先校验长度再分配，长度有上限，越界即丢弃 |
| C4 | 已满足 | — | 每个“发请求等回复”都有超时，或在关闭连接时释放 |
| C5 | 有问题 | 中 | 接管、撤权、登出、空闲释放、关开关这几条路径持有全局 `Service.mu`，同时等待旧输出的网络收尾，最长约 15 秒，期间所有用户的设备投影和命令都被卡住。另有两条风险：慢设备的订阅被 hub 静默关闭；mDNS 定向查询全进程串行 |
| C6 | 已满足 | — | 发送队列、重传缓存、事件通道、注册表都有上限；对端不读时由写期限或控制面失效把连接断开 |
| C7 | 有问题 | 中 | PTP 跟随端不挑往返最短的样本。一次不对称延迟超过 10 ms 就会重置偏移，时钟约 0.5 秒内显示失锁，可能被心跳读到，导致 HomePod 会话结束，或其他 PTP 播放区被重建 |
| C8 | 有问题 | 低 | 播放中从 PTP 降级为 NTP 时不写日志也不回报。另有一条风险：AirPlay 心跳一次超时 2 秒就拆掉会话 |
| C9 | 已满足 | — | 重建会话时会重发音量和元数据，换新的 RTP 传输与序号；Cast 用事件序号、RTSP 用 CSeq 丢弃旧数据 |
| C10 | 已满足 | — | 所有出站目的地都经过 `lan.IsLANAddress` 或来源一致性校验；子网广播地址虽然能通过校验，但内核会拒绝发出 |
| C11 | 有问题 | 低 | 归并按 UDN、Cast id、AirPlay deviceid 进行，已满足；但 Cast 和 AirPlay 输出在认领时就固定了地址，设备换地址后不重建，只能断开并暂停 |
| C12 | 有问题 | 低 | 停止和退出时套接字与协程都会释放；但 mDNS、SSDP、RTP 的接收循环遇到非超时错误会永久退出，也不写日志。只有 PTP 能自愈 |
| C13 | 有问题 | 低 | 部署文档只写了 8090 和 PTP 的 319/320 会占用端口，没有写入站防火墙需求（5353、1900 及其回包、AirPlay 计时与控制端口、PTP 319/320） |

## 2. 逐项结论

### C1 组播网卡选择与重建 —— 有问题（中）

**已满足的部分**

- mDNS 每张局域网网卡单独开一个 5353 套接字，并只在该网卡加入组：`backend/internal/devices/mdns/query.go:52-64`、`backend/internal/devices/mdns/socket.go:66-67`。出站用控制消息指定 `IfIndex` 与源地址：`backend/internal/devices/mdns/socket.go:88-94`。收包按 `IfIndex` 归到对应网卡，其他网卡上的组播被丢弃：`backend/internal/devices/mdns/socket.go:126-139`。
- SSDP 每张网卡一个搜索套接字，绑定到该网卡地址的临时端口，并设置 `SetMulticastInterface`；同时为每张网卡开一个通知套接字：`backend/internal/devices/upnp/ssdp.go:101-157`。
- PTP 在 0.0.0.0:319/320 上按网卡逐一加入组播：`backend/internal/ptp/network.go:62-92`。出站指定 `IfIndex` 与源地址：`backend/internal/ptp/network.go:149`。收包按 `cm.IfIndex` 过滤：`backend/internal/ptp/network.go:124`。

**缺陷 1（确认）：网卡只枚举一次，变化后不重建**

- mDNS：`Start` 只在开始时调用一次 `lanLinks`（`backend/internal/devices/mdns/query.go:52`），之后不再扫描网卡。Cast 注册表（`backend/internal/devices/cast/registry.go:171`）、AirPlay 注册表（`backend/internal/devices/airplay/registry.go:208`）和 PTP 服务（`backend/internal/ptp/clock.go:299`）都只调用一次。
- SSDP：`openLinks` 只在 `Registry.Start` 时调用（`backend/internal/devices/upnp/discovery.go:44`）。之后若网卡地址变了，搜索套接字仍绑在旧地址上，发送失败的错误被直接丢弃（`backend/internal/devices/upnp/discovery.go:106,111`）。
- PTP：只有读失败时才重开（`backend/internal/ptp/service.go:89-113`），不会主动发现新网卡。地址变化后，`links` 里的源地址已经过期，发送失败也没有反馈（`backend/internal/ptp/network.go:149-152`，调用方不检查错误）。

**缺陷 2（确认）：没有局域网网卡时“启动成功”**

- `lanLinks` 找不到网卡时返回 `nil, loop, nil`（`backend/internal/devices/mdns/socket.go:60-61`，`lastErr` 只在 `Addrs()` 出错时才有值）。`Start` 只在“套接字为零且 `lastErr != nil`”时才报错（`backend/internal/devices/mdns/query.go:66`），所以会返回一个没有任何工作协程的 stop。
- `openLinks` 同理：没有链接且 `lastErr == nil` 时返回 `nil, nil`（`backend/internal/devices/upnp/ssdp.go:132-135`），`discover` 便在空链接上空转。
- PTP 在这种情况下会返回错误并每 3 秒重试（`backend/internal/ptp/network.go:92-94`、`backend/internal/ptp/service.go:112`），所以 PTP 不受影响。

**次要问题（低）**

- 每张网卡只取第一个局域网地址（`backend/internal/devices/mdns/socket.go:52-57`、`backend/internal/devices/upnp/ssdp.go:129`、`backend/internal/ptp/network.go:89`）。网卡有第二个私网网段时，该网段设备看到的组播源地址和 `LocalAddress` 都是第一个地址。
- `LookupMatch` 只在 `links[0]` 或与目标地址相同的本机网卡上加入组播（`backend/internal/devices/mdns/query.go:114-120`）。目标在第二张网卡的网段上、并且用组播应答时，只有在别的套接字恰好已经在那张网卡加入组时（借助 Linux 默认的 `IP_MULTICAST_ALL`）才能收到。
- PTP 用一个套接字加入所有网卡的组（`backend/internal/ptp/network.go:81-85`），会受 Linux `igmp_max_memberships`（默认 20）限制。宿主机网络下 Docker 网桥很多时，排在后面的网卡加入会失败，代码会跳过它。**待验证**：在有 20 个以上 UP 网桥的机器上看 `ptp.ready` 日志中的 `interfaces` 数量。

**触发条件**：NAS 或容器在 DHCP 完成之前启动；设备开关打开后再插网线、换 Wi-Fi，或 DHCP 换了地址；USB 网卡重插后网卡编号变了。

**后果**：Cast、AirPlay 自动发现永远为空，UPnP 组播发现失效，PTP 在新网卡上不工作。只有重新开关协议或重启进程才能恢复。日志里看不到原因。

**修复方案**

1. `backend/internal/devices/mdns/query.go` `Start`：改为每个网卡一个 worker，各自持有自己的 cancel。另起一个协程，每 `searchInterval`（30 秒）重新调用 `lanLinks`，比较 `(ifindex, local)` 集合：新出现的网卡就开套接字和 worker，消失的就 cancel 并等它退出。启动时没有网卡不算失败，由这个协程等网卡出现。
2. `backend/internal/devices/upnp/discovery.go` `discover` 与 `backend/internal/devices/upnp/ssdp.go` `openLinks`：同样在每轮搜索前比对网卡集合，按差异增删链接（每条链接的读协程有独立的 WaitGroup）。零链接时不报错，按轮次重试。
3. `backend/internal/ptp/clock.go` `serve`：在 tick 里每 30 秒比对网卡集合，有变化就返回一个专门的错误，让 `Run` 的循环重开 `openWire`，这条路径已有，见 `backend/internal/ptp/service.go:89-113`。
4. 网卡枚举改为包级函数变量，例如 `var listLinks = lanLinks`，供测试替换。
5. 次要问题：`LookupMatch` 按目标地址所在网段选监听网卡，取包含目标的前缀；没有匹配的网段时再回退到 `links[0]`。

**复现测试**（在修复前应失败）

- `backend/internal/devices/mdns`：把网卡枚举函数替换成“第一次返回空，之后返回真实网卡”，把重扫间隔改为 100ms。用 `mdns.Advertise` 在本机局域网地址上公告一个服务，断言 `Start` 的回调在 2 秒内收到这条 Entry。修复前回调永远收不到。
- `backend/internal/devices/upnp`：同样替换枚举函数（先空后实），起一个 `fakerenderer` 响应 M-SEARCH，断言注册表在若干轮内出现该 UDN。

### C2 回包到临时端口 —— 待验证（中）

**已满足**

- SSDP 的 M-SEARCH 从搜索套接字的临时端口发出，读协程在同一套接字上读单播应答：`backend/internal/devices/upnp/ssdp.go:139`、`backend/internal/devices/upnp/discovery.go:94`。手动输入裸 IP 时用独立的临时套接字，有 3 秒期限：`backend/internal/devices/upnp/ssdp.go:174-199`。
- AirPlay 的计时与控制套接字绑定到 RTSP 本机地址的临时端口，并在同一套接字上收发：`backend/internal/devices/airplay/rtp/transport.go:52-69,183-233`。
- PTP 的单播 Delay_Resp 和 Signaling 发到 319/320，本服务就监听在 0.0.0.0:319/320：`backend/internal/ptp/network.go:28-51`。

**风险：mDNS 定向查询**

- `LookupMatch` 打开的查询套接字同样绑定 0.0.0.0:5353（`backend/internal/devices/mdns/query.go:121`、`backend/internal/devices/mdns/socket.go:67`）。Go 只设置 `SO_REUSEADDR`。目标设备对定向查询的单播应答发往本机 5353，Linux 只把它交给其中一个套接字。候选包括：本进程 `Start` 打开的各网卡浏览套接字（注册表运行时它们一直存在）、宿主机的 avahi，以及这个查询套接字。代码注释承认了这一点（`backend/internal/devices/mdns/query.go:19-21`），但 `lookupSlot` 只能让各次 `LookupMatch` 之间串行，挡不住同进程的浏览套接字和 avahi。
- Linux 在多个 `SO_REUSEADDR` 套接字之间选择时会按打分，其中包括 `sk_incoming_cpu` 加分，结果不确定。应答被浏览套接字收走时，设备仍会经自动发现上线；被 avahi 收走时这条应答就丢了。
- **后果**：手动登记的 Cast 或 AirPlay 设备复探偶发失败，并被标成离线，见 `backend/internal/devices/cast/registry.go:296-303`、`backend/internal/devices/airplay/registry.go:255-263`。管理员添加设备也会偶发“无法读取”。

**修复方案**：在 `backend/internal/devices/mdns/query.go` 的 `LookupMatch`/`search` 中，定向查询改用 RFC 6762 §6.7 的传统单播方式：另开一个 `udp4` 临时端口套接字，查询报文带随机 ID 发往 `target:5353`，在同一个临时套接字上收 ID 相同的单播应答。按 RFC，响应端必须单播回到源端口。对本机目标仍保留组播查询。改完后 `lookupSlot` 可以去掉。

**验证或复现测试**：把 mDNS 端口改为包级变量，测试中设成一个非特权测试端口。起一个假响应端监听在本机局域网地址的该端口，记录收到查询的源端口，并单播回到源地址。同时运行 `Start`，制造与浏览套接字的竞争。断言：(a) 查询的源端口不是共享端口；(b) 连续 50 次 `LookupMatch` 全部成功。修复前 (a) 必然失败；(b) 可能间歇失败。

### C3 报文解析 —— 已满足

| 协议 | 证据 |
| --- | --- |
| CASTV2 | 4 字节长度先做 `size == 0 \|\| size > 64KiB` 检查，再 `make`：`backend/internal/devices/cast/castv2/frame.go:36-40`。protowire 解码失败时返回 `n < 0`，代码丢弃整帧：`frame.go:46-85`。protowire v1.34.2 的 `ConsumeFieldValue` 带递归上限 10000 |
| RTSP | 状态行与头行用 `ReadSlice`，超过 bufio 缓冲直接报错；头总长不超过 64KiB；`Content-Length` 先限制在 1MiB 以内再分配：`backend/internal/devices/airplay/rtsp/client.go:165-217`。事件通道的请求体用 `CopyN` 丢弃，不分配内存：`rtsp/event.go:51-61`。HAP 加密帧长度上限 1024：`rtsp/record.go:76-82` |
| bplist | 总长不超过 1MiB，校验 trailer 字段，偏移表大小必须一致，深度上限 64，带环检测，元素数受剩余字节和总预算双重限制：`backend/internal/devices/airplay/bplist/decode.go:30-44,65-67,97,146-151` |
| HAP TLV8 | 每段长度都对剩余字节做校验，重复字段只允许按 255 字节分片续接：`backend/internal/devices/airplay/hap/tlv.go:45-61`。SRP 盐长度 1–16，公钥不小于模数时由 `bigmod` 报错：`hap/srp.go:66-73` |
| mDNS | 9000 字节缓冲，由 `dnsmessage.Unpack` 解析，失败即丢弃；每个来源最多 256 条记录，最多 256 个来源：`backend/internal/devices/mdns/query.go:168,199-210`、`mdns/cache.go:72-76` |
| SSDP | 读缓冲为 8193 字节，超过 8192 丢弃；`LOCATION` 的主机必须与 UDP 来源相同：`backend/internal/devices/upnp/ssdp.go:37,78,160`。描述与 SCPD 不超过 256KiB，SOAP 不超过 64KiB：`upnp/egress.go:22-23,122-126` |
| PTP | 先按报文类型确定最小长度，再要求 `长度 ≤ 实际字节`，之后才按偏移读取（Announce 要 64 字节，读到 `[63]`；Signaling 要 48 字节，读 `[44:]`，TLV 逐段校验）：`backend/internal/ptp/wire.go:84-111`、`ptp/clock.go:116-122`。时间戳溢出会被拒绝：`wire.go:67-74` |

附注（不改）：`mdns/cache.go:125` 用小写化后的服务名长度去截原始实例名。遇到非 ASCII 大小写变体时，截出的实例名可能不对，但长度不会越界，不会 panic；调用方会拒绝无效的 UTF-8。

### C4 等待回复 —— 已满足

| 等待点 | 释放方式 | 证据 |
| --- | --- | --- |
| UPnP SOAP、描述、SCPD | `http.Client.Timeout` 5 秒，包含读正文 | `backend/internal/devices/upnp/egress.go:24,99-103` |
| SSDP 单播搜索 | 套接字期限 3 秒，`ctx` 取消时关闭 | `backend/internal/devices/upnp/ssdp.go:181-185` |
| mDNS 定向查询 | 3 秒超时，`ctx` 取消时关闭套接字 | `backend/internal/devices/mdns/query.go:129`、`query.go:163-164` |
| CASTV2 连接与请求 | 拨号 5 秒；写期限 5 秒；请求等回执 10 秒或连接关闭；读循环由心跳在连续 3 次没有 PONG 后断开 | `backend/internal/devices/cast/castv2/client.go:75,120,199-201,223-235` |
| RTSP | 每次请求期限 10 秒，并与 `ctx` 取较早者；`ctx` 取消时立即设期限；出错就关闭连接 | `backend/internal/devices/airplay/rtsp/client.go:134-158` |
| AirPlay 时钟等待 | 25 秒 | `backend/internal/devices/airplay/clock.go:36-37` |
| AirPlay 降级重建 | 12 秒 | `backend/internal/devices/airplay/clock.go:86` |
| TEARDOWN | 1 秒 | `backend/internal/devices/airplay/media.go:284` |
| PTP 状态查询 | 1 秒 | `backend/internal/ptp/status.go:47` |
| AirPlay PCM 读取 | 关闭 reader 会关闭渲染会话，阻塞的读随之返回 | `backend/internal/zones/airplay_stream.go:12-15`、`upnp/output_airplay.go:52-59` |

附注：事件通道应答写（`rtsp/event.go:63`）和 UPnP 流写出（`backend/internal/zones/httpapi/upnp_stream.go:75-79`）都没有写期限。前者在会话关闭时 `Close`，后者在句柄 `Done` 时把写期限设为现在，所以都能被释放。

### C5 故障隔离 —— 有问题（中）

**缺陷（确认）：持有全局锁时等待旧输出的网络收尾**

- `Acquire` 在 `s.mu.Lock()` 下执行 `old.cancel(); <-old.done`：`backend/internal/devices/upnp/output.go:110,164-165`。
- `stopOutputs` 在调用方持锁时等待 `<-o.done`：`backend/internal/devices/upnp/output.go:196-213`。持锁的调用方有 `UpdateSettings`（`upnp/service.go:156`）、`Remove` 与 `SetGrants` 中的 `enforceGrants`（`upnp/service.go:334,347`）、`SessionRevoked`（`upnp/output.go:234,244`）、`ReleaseIdle`（`upnp/output.go:273,282-283`）。
- `done` 在旧输出收尾之后才关闭，收尾过程有网络 IO：
  - UPnP `finish` 用 `context.Background()` 发 SOAP Stop，最长 5 秒：`backend/internal/devices/upnp/output_playback.go:91-102`。
  - Cast 的 `stopOwnedMedia` 发 1–2 个请求，等待被限制在 2 秒，但发送受 5 秒写期限约束：`backend/internal/devices/cast/output.go:72-90`。
  - AirPlay 的 `Sender.Close` 要先拿 `Sender.mu`，而这把锁可能正被 `clockLost→replaceWithNTP` 占着，最长 12 秒；拿到后还要发 TEARDOWN，最长 1 秒：`backend/internal/devices/airplay/clock.go:68-81,86`、`media.go:284`。
- 持锁期间，`VisibleDevices` 要拿 `s.mu.RLock()`（`upnp/output.go:77`）。zones 的 `syncManaged` 每次命令、订阅以及每 5 秒一次的离线扫描都会调用它：`backend/internal/zones/managed_devices.go:72`、`zones/output_lifecycle.go:30`。

**触发条件**：用户 B 接管用户 A 正在用的设备，而这台设备正卡住或响应很慢，例如 UPnP 的 Stop 不回、HomePod 正在降级重建。管理员改开关或授权，或某用户登出时，他的输出里有慢设备，情况相同。

**后果**：所有用户的播放区命令、SSE 订阅、离线扫描都要等最多约 15 秒，属于有上界的全局停顿，不会永久挂死。

**修复方案**（`backend/internal/devices/upnp/output.go`）

1. `Acquire`：确认要接管后，在锁内把 `m.owners[udn]` 标记为“交接中”（可用一个占位 `deviceOutput`，或在 `outputManager` 里另设 `handover map[string]chan struct{}`），然后 `old.cancel()`，解开 `s.mu` 再 `<-old.done`，之后重新加锁，重新核对 `s.running()`、协议的 `ctx` 是否没变、设备是否仍可见、占位是否仍归本次调用，核对通过再 `ClaimOutput`。同一 UDN 的其他认领见到占位时返回 `DeviceBusyError`，或等待占位结束。
2. `stopOutputs` 拆成两步：`cancelOutputs(match)` 在锁内 cancel 并返回列表，`waitOutputs(list)` 在解锁后等待。`enforceGrants`、`SessionRevoked`、`ReleaseIdle` 改用“锁内 cancel、锁外等待”。`stopProtocol` 要保证“先停渲染器再关发现”的顺序（见 `output_playback.go:95` 的注释），可以保留在锁内，这只在管理员操作时发生。

**复现测试**：在 `backend/internal/app` 现有的 UPnP 接管集成测试旁（`upnp_access_integration_test.go:101` 的 `TestPostgreSQLUPnPSharingTakeoverAndRevocation`）新增用例。包装 `fakerenderer.Handler()`，让 `Stop` 这个 SOAP 动作睡 4 秒。A 播放后，B 带 `takeover` 激活同一设备；B 的请求进行中时，并发调用 C（第三个账号）的 `GET /api/zones` 快照或 `Service.VisibleDevices`，断言 1 秒内返回。修复前会等约 4 秒。

**风险 1（中）：慢设备的订阅被 hub 静默关闭**

- `Hub.Publish` 在缓冲（64 条）满时直接移除订阅：`backend/internal/zones/hub.go:34,79-84`。托管输出见到 `sub.Done` 就安静地结束并释放：`backend/internal/devices/upnp/output_playback.go:25`、`upnp/output_airplay.go:126`、`cast/output.go:106`，这里不写日志。
- 同一用户每个正在播放的托管输出每秒都会 `ReportOutput`，每次都 `publishZone`：`backend/internal/zones/outputs.go:179-188`、`upnp/output_poll.go`、`upnp/output_airplay.go:133-142`。一个输出卡在网络调用里时，例如 AirPlay 的 `OpenSender` 等时钟最长 25 秒并经过多次 RTSP（`upnp/output_airplay.go:288`），或 UPnP 推流连续 4 个 SOAP、每个最长 5 秒，它的订阅会积压“其他输出数 × 阻塞秒数”条事件，超过 64 条就被关掉。
- **后果**：同一账号多区播放时，一台变慢的设备会自己停播，播放区被标成离线并暂停，日志里看不到原因。
- **修复方案**：各输出在 `sub.Done` 分支先检查 `p.ctx.Err()`；不是主动取消时写一条 Warn 日志，并尝试重新 `SubscribeOutput`（会话 ID 不变，重订阅会拿到新快照）。另一种做法是让订阅中与本区无关的 zone 事件不入队：在 `Hub.Publish` 一侧过滤要动 zones，范围更大，不建议。
- **测试**：在 `backend/internal/zones` 用 `output_fixture_test.go` 的夹具认领一个托管输出，不读它的订阅；同一用户另一输出连续回报 65 次，断言前者的 `Subscription.Done` 已关闭。这一步证实机制，修复前后都会通过。再在 `upnp` 包断言收到 `Done` 后输出没有被释放，或已经重订阅。修复前这一条失败。

**风险 2（低）：mDNS 定向查询全进程串行**

- `lookupSlot` 容量为 1，每次查询最长 3 秒，AirPlay 每台设备要查两种服务：`backend/internal/devices/mdns/query.go:21,103-106,129`、`airplay/discovery.go:215-237`。
- 离线的手动设备一多，每轮复探就会很长（N 台 Cast 加 2N 次 AirPlay，每次 3 秒），管理员“添加设备”也要排队。
- 按 C2 改成临时端口查询后，这个串行就不再需要，不必单独修。

### C6 背压与内存 —— 已满足

| 位置 | 上限或断开方式 | 证据 |
| --- | --- | --- |
| RTP 重传缓存 | 固定 512 包，每次重传请求最多回 512 包 | `backend/internal/devices/airplay/rtp/transport.go:44,175,219` |
| 发送端待发尾帧 | 固定为一包的容量 | `backend/internal/devices/airplay/sender.go:51-55,205` |
| 播放进度记录 `flights` | 每包追加，播放过了就弹出，大致不超过延迟窗口（2 秒） | `backend/internal/devices/airplay/sender.go:91-104,168-171` |
| PCM 预读 | 无缓冲通道，一次只读一个 RTP 包 | `backend/internal/devices/upnp/output_airplay.go:29-51` |
| Cast 事件 | 64 条，满了就断开连接 | `backend/internal/devices/cast/castv2/client.go:83,170-174` |
| 订阅 | 64 条，满了就关闭订阅（副作用见 C5 风险 1） | `backend/internal/zones/hub.go:34,79-84` |
| UPnP 拉流 | 单个 32KiB 缓冲，写完才读下一块 | `backend/internal/zones/httpapi/stream.go:149-190` |
| 注册表与缓存 | UPnP、Cast、AirPlay 各 1024 条；mDNS 256×256；PTP 对端 1024；SSDP 刷新队列 64 | `upnp/discovery.go:14,49`、`cast/registry.go:244`、`airplay/registry.go:115`、`mdns/query.go:206`、`ptp/clock.go:39` |
| 零窗口或拔线 | RTSP 每次请求期限 10 秒，Cast 写期限 5 秒且心跳 15 秒，UPnP SOAP 5 秒；控制面连续失败就释放输出，释放时关闭流句柄，写期限随之生效 | 见 C4；`upnp/output_playback.go:59-64` |

附注（风险，低）：UPnP 渲染器的控制面正常、拉流的 TCP 却不再读时，`copyStream` 的写会一直阻塞，直到句柄关闭或内核 TCP 重传超时（约 15 分钟）。这期间轮询看到 RelTime 不动，就改用墙钟外推进度（`upnp/output_poll.go:22-27`），而 `Delivered` 一直为假，所以不会判定播完。内存不增长，只是进度会失真。这次不改。

### C7 时钟与延迟 —— 有问题（中）

**已满足的部分**

- RAOP 读取接收端 `Audio-Latency`（单位是采样），加一秒网络缓冲后写入传输层：`backend/internal/devices/airplay/raop/session.go:147-153`。
- AirPlay 2 使用约定的 2 秒延迟（`rtp/transport.go:53`），在 SETUP 中声明 `latencyMin/Max`（`media.go:234`）。PTP 同步包另计接收端的四分之一秒（`rtp/transport.go:254-255`）。
- 播放进度按 `Latency()` 推算：`sender.go:169`。
- 单位一致：采样换时间用 `rate`（`transport.go:108-112`、`sender.go:138-140`），PTP 用纳秒（`transport.go:125`），NTP 用 32.32 定点（`transport.go:135-137`），PTP 修正域除以 65536（`ptp/wire.go:110`）。

**缺陷（确认）：PTP 跟随端不挑最短往返，离群样本会让时钟失锁**

- `follower.measure` 只按路径延迟落在 [-1ms, 250ms] 内过滤（`backend/internal/ptp/follower.go:92`）。接下来，样本与当前估计相差超过 5ms 时，直接用这个样本覆盖偏移并把样本计数清零（`follower.go:96-100`）；否则做 1/4 的指数平滑（`follower.go:101`）。不记录往返时间，也不在窗口内挑往返最短的样本。
- 样本误差等于单向延迟不对称量的一半。所以只要一次单向延迟突发超过约 10ms（Wi-Fi 上常见），就会触发重置。
- 重置后要等 `samples >= 3` 且首末样本间隔至少 250ms（`follower.go:109`）。Delay_Req 每 250ms 发一次（`ptp/clock.go:273`），所以大约 500ms 内 `Available=false`。
- 发送端每 2 秒心跳一次，调用 `refreshClock`（`backend/internal/devices/airplay/sender.go:397-408`）。读到失锁时进入 `clockLost`：必须 PTP 的型号直接 `finish`（`airplay/clock.go:74-76`），其他型号走 `replaceWithNTP` 重建（`clock.go:78`）。
- 本机跟随远端主钟（例如正在播放的 HomePod）时，其他对端拿到的状态就是这个全局状态（`ptp/clock.go:94-108`），所以一次离群样本会同时影响所有 PTP 会话。

**触发条件**：HomePod 或 Mac 提供主钟，链路有单向延迟突发（Wi-Fi、拥塞），而心跳恰好落在约 500ms 的失锁窗口里，概率大约四分之一。

**后果**：HomePod 播放中断，播放区停止；Sonos Era 等跟随同一主钟的会话结束或降级。这类故障看起来是随机的。

**修复方案**（`backend/internal/ptp/follower.go`）

- 给 `follower` 加一个固定大小的环形窗口，例如最近 8 次交换，每项保存 `(rtt = backward - forward, sample, at)`。
- `measure` 先算出窗口内的最小往返，`rtt` 超过 `minRTT + 2ms` 的样本只放进窗口，不用来更新偏移。偏移取往返最短的那个样本，或往返最短的几个样本的平均。
- 只有连续多个低往返样本（例如 4 个）都偏离当前估计超过 5ms 时，才认定主钟真的跳变，这时再重置。
- 样本被拒绝时不清零 `samples`，所以 `Available` 保持不变。
- `status` 的判据不变。

**复现测试**：放在 `backend/internal/ptp/clock_test.go`，复用 `TestPTPFollowerOffsetCorrectionsAndReorderedFollowUp` 里的 `decodedArrival` 等辅助函数。先用对称延迟喂 6 组 Sync 加 Delay 交换，确认 `status().Available`。再喂一组 Delay_Resp 单向多 20ms 的交换，断言 `status().Available` 仍为真，`offset` 变化小于 1ms。修复前 `Available` 变成假，`offset` 跳动约 10ms。

### C8 兜底超时 —— 有问题（低）

**已满足的兜底**

- 跟随端处于 `clock_unlocked` 时允许协商，但交付 PCM 之前必须 `waitClock` 等到可用，否则报错或降级：`backend/internal/ptp/status.go:28-30`、`airplay/media.go:211-217,273-276`。
- 接收端不支持 SETRATEANCHORTIME 或元数据时显式跳过，状态码见 `airplay/clock.go:128-147`、`airplay/metadata.go:88-93`。
- UPnP 读不到 ProtocolInfo 时默认用 FLAC，但 `FormatDeclared=false` 会展示给界面：`upnp/description.go:173-180`。
- 时长未知且已经交付完时，把设备停止当作自然播完，注释写明了这一行为：`upnp/output_poll.go:70-88`。

**缺陷（确认，低）：播放中降级没有记录**

- `replaceWithNTP` 把会话从 PTP 重建为 NTP 后，只更新 `s.fallback` 和 `s.timing`（`backend/internal/devices/airplay/clock.go:99`）。
- 输出层只在 `OpenSender` 成功时记录一次 `fallback`（`backend/internal/devices/upnp/output_airplay.go:290`）。播放中降级之后，不写日志，也不回报界面。
- **后果**：多房间同步悄悄变成 NTP 精度，排查时没有线索。
- **修复**：在 `airplay.Sender` 上加一个降级通知，例如 `Fallbacks() <-chan error`，或在 `clockLost` 成功后调用 `SenderConfig.OnFallback` 回调。`upnp/output_airplay.go` 的 `serve` 收到后写一条 `airplay.output.fallback` Warn。
- **测试**：`backend/internal/devices/airplay/sender_test.go` 已有 PTP 场景，在其中让 `PeerStatus` 返回失锁，断言回调或通道收到一次通知。修复前没有这个接口，测试编译不通过，按失败计。

**风险（低–中）：心跳一次超时就拆掉会话**

- 心跳的 `OPTIONS` 或 `POST /feedback` 限时 2 秒（`backend/internal/devices/airplay/sender.go:397,415-421`）。RTSP 请求超时后连接会被关闭（`rtsp/client.go:157-158`），`finish` 随即结束会话。
- 链路短暂变差（单次往返超过 2 秒）就会断播。
- **修复**：心跳失败先计数，连续 2 到 3 次才结束会话。但请求超时已经关闭了控制连接，计数前要先区分“连接已关闭”和“回复迟到”。比较稳的做法是心跳期限放宽到 `requestTimeout`（10 秒），其他不变。
- **测试**：在 `sender_test.go` 的假接收端里让第一次 `/feedback` 延迟 2.5 秒才回复，断言会话仍然存活。修复前会话结束。

### C9 重连与旧数据 —— 已满足

- 长暂停后 `Resume` 会重新握手，并通过 `restoreControls` 重发音量和元数据：`backend/internal/devices/airplay/sender.go:289-301,308-318`。PTP 失锁后 `replaceWithNTP` 重建时同样恢复控制，并清空 `flights`、令 `played=sent`、`next` 归零：`airplay/clock.go:94-104`。
- 新建的 `rtp.Transport` 用随机的序号、时间戳和 SSRC：`rtp/transport.go:54-58`。FLUSH 时清空重传缓存并重新锚定：`rtp/transport.go:128-133`、`sender.go:320-338`。
- Cast 每个连接的事件都有序号，早于回执的旧广播会被丢弃：`cast/castv2/client.go:159-161`、`cast/output.go:171-178,347-350`。RTSP 校验 CSeq：`rtsp/client.go:199-203`。PTP 换主钟时重置跟随端，Sync 序号按半区间比较：`ptp/follower.go:29-41`。
- 托管输出重新认领时 `volume=-1`，`sentVolume=nil`，音量会重新下发：`upnp/output.go:179`、`cast/output.go:202`。

附注：Cast 和 AirPlay 的控制连接断开后不会在输出内部自动重连，而是释放输出、暂停播放区，等成员再次播放。这是现有设计，不作为缺陷；地址变化的情况见 C11。

### C10 目标地址 —— 已满足

- 判定函数 `lan.IsLANAddress` 只接受 IPv4 私网和链路本地地址，拒绝回环、未指定、组播和映射 IPv6：`backend/internal/devices/lan/address.go:6-8`，测试见 `lan/address_test.go`。
- 各出站点：
  - SSDP 的 LOCATION 主机必须等于 UDP 来源：`upnp/egress.go:57-66`。
  - SCPD 和控制地址必须与描述同主机：`upnp/description.go:154-163`。
  - 手动地址解析后只取局域网 IPv4：`upnp/egress.go:78-97`。
  - mDNS 只接受与来源相同的 A 记录：`mdns/cache.go:60-63`。收包时过滤非局域网来源：`mdns/socket.go:121-125`。
  - Cast 拨号：`cast/castv2/client.go:71-74`。RTSP 拨号：`rtsp/client.go:67-71`。RTP：`rtp/transport.go:49-51`，计时和控制只回给对端：`transport.go:192,213`。
  - PTP 不回本机地址，只回局域网来源：`ptp/network.go:124-128`。PTP 状态的 peer 参数：`ptp/service.go:42-47`。
  - UPnP 拉流只接受私网和回环客户端：`zones/httpapi/upnp_stream.go:40-52`。
- 附注（低，不改）：子网广播地址（例如 192.168.1.255）能通过 `IsLANAddress`，但这些套接字都没有设置 `SO_BROADCAST`，内核会拒绝 UDP 发送（EACCES），TCP 连接也会被拒（ENETUNREACH），所以不会真的发出去。mDNS 和 SSDP 又都要求公告地址等于报文来源，广播地址无法冒充来源。

### C11 身份归并与地址变化 —— 有问题（低）

**已满足的部分**

- UPnP 按 UDN 归并；LOCATION 变化时重新拉描述并更新记录：`upnp/discovery.go:134-165,188-221`。输出每轮轮询都从注册表取最新的 `Device`，包括新的控制地址：`upnp/output_playback.go:36-41`。
- Cast 按 TXT `id` 归并，地址或端口变化就重新校验：`cast/registry.go:54-71,184-195`。
- AirPlay 按 deviceid 归并 `_airplay` 与 `_raop`（RAOP 实例名中的 MAC 必须一致），并按来源（服务、实例、IP、网卡）分别保存公告：`airplay/discovery.go:97-157,184-205`、`airplay/registry.go:42-57,107`。

**缺陷（确认，低）：Cast 和 AirPlay 输出不随地址重建**

- 认领时设备地址就固定下来：Cast 见 `backend/internal/devices/upnp/output.go:188` 的 `Address: device.Address`，AirPlay 见 `output.go:184` 的 `receiver`。长暂停后 `Resume` 仍用 `s.device` 的旧地址（`backend/internal/devices/airplay/sender.go:290`）。
- 设备 DHCP 换了地址后，Cast 要等心跳断开，再 `Offline`；AirPlay 要等 RTSP 失败后释放，见 `upnp/output_airplay.go:86-95`。两者都只能暂停播放区。AirPlay 暂停超过 10 分钟后若换了地址，“继续”会直接失败。
- **修复**：
  - Cast（`backend/internal/devices/cast/output.go` 的 `run` 定时分支）：给 `Output` 加 `Address func() string`。地址变化时返回一个专门的错误，让 `Acquire` 侧的协程以当前进度重新 `Run`：重新 Dial、LAUNCH、LOAD，复用同一个 `SessionID`。
  - AirPlay：`Sender.Resume` 之前先从注册表取当前的 `Device`。可以在 `airplayOutput.applyState` 中检测到 `paused` 并要 `Resume` 时，用 `p.registry.Lookup` 的结果更新 `p.receiver`，再调用新增的 `Sender.SetDevice`。
  - 改动规模：中。
- **测试**：在 `cast/output_test.go` 已有的 fakecast 测试中，起两个 fakecast 端口，播放中把注册表地址从 A 切到 B，并关闭 A。断言输出连上 B、发出 LOAD，播放区没有暂停。修复前播放区会被暂停。

**风险（低）**：Cast 注册表在同一 id 的公告 IP 来回变化时，会把 `verified` 清零（`cast/registry.go:190-193`）。设备同时有两个 IPv4 并且都在公告时，会在线和离线之间抖动。**待验证**：用两个 `mdns.Advertise` 以相同 id、不同 IP 公告，观察 `Online` 的值。

### C12 资源释放与自愈 —— 有问题（低）

**已满足（释放）**

- UPnP：`Stop` 取消发现，关闭所有链接套接字，并等待读协程和刷新协程：`upnp/discovery.go:61-88`。
- Cast：`Stop` 取消并停掉 mDNS，等待刷新协程：`cast/registry.go:209-240`。AirPlay 同理：`airplay/registry.go:195-237`。
- mDNS `stop` 会取消并等待各个 worker；`ctx` 取消时关闭套接字：`mdns/query.go:83-86,163-164`。
- 关闭协议时先停输出：`upnp/service.go:128-138`。进程退出时调用 `lanDevices.Close()`：`backend/internal/app/app.go:184-186`。
- 发送端收尾时关闭事件连接、控制连接和 RTP，并等待读协程：`airplay/media.go:281-294`、`rtp/transport.go:73-80`。
- PIN 配对流程随协议的 `ctx` 或 2 分钟期限关闭连接：`upnp/pair.go:49-63`。

**缺陷（确认，低）：接收循环遇到非超时错误会永久退出，且不写日志**

- mDNS `run` 遇到非超时错误就 `return err`（`backend/internal/devices/mdns/query.go:187-194`），`Start` 的 worker 随之结束，丢弃这个错误，也不重启（`query.go:75-81`）。
- SSDP 的 `readAnnouncements` 遇到任何错误都直接返回（`upnp/ssdp.go:159-165`）。
- RTP 的计时和控制循环同样如此（`rtp/transport.go:186-190,208-212`）。
- 对比之下，PTP 会把错误交给 `failures`，让外层重开（`ptp/network.go:114-119`、`ptp/service.go:89-113`）。
- 在 Linux 上，未连接的 UDP 套接字读错误基本只在关闭时出现，所以触发概率低。一旦发生，那张网卡的发现会永久停止，而且看不到任何迹象。
- **修复**：mDNS worker 与 SSDP 读协程在 `ctx` 未取消时写一条 Warn 日志，并通知 C1 新增的网卡对比协程把这张网卡当作消失，下一轮重开。RTP 读循环出错时调用 `Sender.finish`，即通过 `mediaSession.events` 报告，让输出走断开路径，不要只剩下发送。
- **测试**：可以与 C1 的测试合并。在 mDNS 测试中拿到 worker 的套接字并直接 `Close()`（模拟读错误），断言下一轮扫描后该网卡恢复接收。修复前不会恢复。

### C13 部署文档的入站端口 —— 有问题（低）

- 现状：
  - `deploy/production/README.md:3` 写了默认使用 host 网络发现渲染器，`:138` 写了 PTP 在 host 网络使用 UDP 319/320，以及端口被占用时的情况。
  - `docs/install-nas.md:43` 只写了“开启了 DSM 防火墙时，放行 8090 端口”。
  - 两份文档都没有列出防火墙入站需求。
- 实际需要的入站端口：
  - UDP 5353：mDNS 组播和单播应答。
  - UDP 1900：SSDP NOTIFY 组播。
  - M-SEARCH 的单播回包：从设备的 1900 发到本机临时端口。请求是发往组播地址的，有状态防火墙不会把这些回包当作已建立连接。
  - AirPlay 计时端口和控制端口：绑定在临时端口（`rtp/transport.go:52-61`）。AirPlay 1 和 NTP 计时请求由接收端主动发来。
  - UDP 319/320：PTP。
  - TCP `ROOMUSIC_HTTP_PORT`：UPnP 和 Cast 从这里拉流。
- **后果**：开着 DSM 等主机防火墙时，自动发现、AirPlay NTP 计时和 PTP 都会悄悄失败。
- **修复**：在 `deploy/production/README.md` 第 3 节的 host 网络说明后加一张“局域网设备所需入站端口”表，在 `docs/install-nas.md` 的群晖段落写明“开启防火墙时允许来自局域网网段的上述 UDP 端口，AirPlay 计时与控制使用随机高端口，需放行局域网来源的 UDP”。只改文档，不需要测试。

## 3. 建议修复清单（按严重程度排序）

| 顺序 | 严重程度 | 项目 | 改动位置 | 规模 |
| --- | --- | --- | --- | --- |
| 1 | 中 | C7：PTP 跟随端改为窗口内挑最短往返；离群样本只拒绝不重置，连续偏离才认定跳变 | `backend/internal/ptp/follower.go` `measure` | 小 |
| 2 | 中 | C5：`Acquire` 接管时放开 `Service.mu` 等待旧输出；`enforceGrants`、`SessionRevoked`、`ReleaseIdle` 改为锁内 cancel、锁外等待 | `backend/internal/devices/upnp/output.go` | 中 |
| 3 | 中 | C1 与 C12：mDNS、SSDP 按轮次比对网卡集合并增删链接；零网卡时不算启动成功也不报错；PTP 网卡变化时重开 wire；接收循环出错写日志并在下一轮重开 | `backend/internal/devices/mdns/query.go`、`mdns/socket.go`、`backend/internal/devices/upnp/discovery.go`、`upnp/ssdp.go`、`backend/internal/ptp/clock.go` | 中–大 |
| 4 | 中 | C2：mDNS 定向查询改用临时端口的传统单播查询（同时可以去掉 `lookupSlot`，解决 C5 风险 2） | `backend/internal/devices/mdns/query.go` `LookupMatch`、`search` | 小–中 |
| 5 | 中（风险） | C5 风险 1：托管输出收到 `sub.Done` 而不是主动取消时，写日志并重新订阅 | `backend/internal/devices/upnp/output_playback.go`、`upnp/output_airplay.go`、`backend/internal/devices/cast/output.go`（Cast 的重订阅在 `upnp/output.go:189` 的协程里做） | 中 |
| 6 | 低–中（风险） | C8：AirPlay 心跳期限放宽到 `requestTimeout`，或改为多次失败才结束 | `backend/internal/devices/airplay/sender.go` `heartbeat` | 小 |
| 7 | 低 | C8：播放中 PTP→NTP 降级时通知输出层并写日志 | `backend/internal/devices/airplay/clock.go`、`airplay/sender.go`、`backend/internal/devices/upnp/output_airplay.go` | 小 |
| 8 | 低 | C11：Cast 和 AirPlay 输出在设备换地址时重建连接 | `backend/internal/devices/cast/output.go`、`backend/internal/devices/upnp/output.go`、`backend/internal/devices/airplay/sender.go`、`upnp/output_airplay.go` | 中 |
| 9 | 低 | C1 次要问题：`LookupMatch` 按目标网段选监听网卡 | `backend/internal/devices/mdns/query.go` | 小 |
| 10 | 低 | C13：部署文档补入站端口表 | `deploy/production/README.md`、`docs/install-nas.md` | 小 |

没有发现“高”级别的缺陷：所有网络等待都有上界，长度字段驱动的分配都有上限，所有缓存都有上界。C3、C4、C6、C9、C10 不改代码（PRD 的 R4）。

## 4. 待验证项汇总

- C2：mDNS 单播应答被同端口其他套接字收走。用“源端口不是 5353”的确定性断言来验证。
- C1：PTP 单套接字加入组播是否受 `igmp_max_memberships` 限制。在 Docker 网桥超过 20 个的主机上看 `ptp.ready` 日志中的 `interfaces`。
- C11：Cast 设备同 id 多 IP 时 `Online` 是否抖动。用两个公告复现。
- C6：UPnP 拉流 TCP 停止读取而控制面正常时进度失真。这次不改，只记录。

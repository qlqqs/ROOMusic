# 局域网 UPnP 渲染器合同

## 设置与持久化

`app.New` 为每个实例装配一个 `upnp.Service` 与一个 `Registry`。迁移 `0069_upnp_devices` 采用实例设置的单行表模式。

| 表 | 内容 |
|---|---|
| `upnp_settings` | `id=1`，`enabled` 默认 false，`shared` 默认 true |
| `upnp_manual_devices` | UDN 主键、描述地址、最后登记的名称、型号和能力 |
| `upnp_device_grants` | 主键为 `(udn,user_id)`，包括自动发现设备的分配，账号外键指向 `users(id)` |

运行中开启发现或重启已开启的实例时读取手动设备，创建 UDP 套接字和发现工作协程。关闭时取消探测，关闭套接字并等待发现工作退出。初始关闭不启动设备工作协程，不发 SSDP 或访问设备。关闭及重启保留数据库里的设置、手动设备和分配。

`ROOMUSIC_LAN_URL` 可留空。配置时须为含主机与显式端口的 HTTP 来源，例如 `http://192.168.1.10:8090`。端口为 1–65535，可带单个末尾斜杠，不接受路径、用户信息、查询串或片段。读取值在 `config.Server.LANURL`。设备对象的 `LocalAddress` 保存单播搜索响应所在网卡地址，手动探测与组播公告取描述 HTTP 连接的本机地址。

## 发现与注册表

- 在有局域网 IPv4 的活动组播网卡上，启动时及每 30 秒向 `239.255.255.250:1900` 发送三次 MediaRenderer M-SEARCH，MX 为 2。另向该网卡自己的私网地址发送一次单播搜索，用于组播无法回送时的同机渲染器。
- 单播响应与 `ssdp:alive` / `ssdp:update` 带来的 LOCATION 通过出站校验后取描述，核对描述内 MediaRenderer 的 UDN 与 USN。有效公告按 `Cache-Control: max-age` 续期；到期后读取快照即为离线。同一来源的 `ssdp:byebye` 立即下线，旧探测结果不得使设备重新上线。
- 注册表以 UDN 合并行，保存名称、型号、地址、能力和在线状态。已登记的手动地址每 30 秒重新探测，探测失败撤销手动在线状态；同时被 SSDP 发现的设备仍按公告有效期判断。删除手动登记后，仍有有效公告的设备可以继续出现在发现列表。
- `Registry.List()`、`Registry.Lookup(udn)` 返回实例快照。`Service.Visible(ctx, actor)` 返回在线且对该账号可见的行。共享开启时所有成员可见，关闭时管理员可见全部，其余成员只见分配给自己的 UDN。

## 出站地址与控制

`DiscoverDescription(ctx, location, source)` 只接受主机为 UDP 来源 IPv4 字面地址的 HTTP URL。手动输入裸 IP 时向该 IP 的 UDP 1900 端口单播搜索。手动输入 HTTP 描述地址时，`ManualDescription` 可解析管理员给定的主机名。每次探测仅选用 10/8、172.16/12、192.168/16 或 169.254/16 的 IPv4，固定 TCP 目的 IP，并保留原 HTTP Host。

按 URLBase 或描述 URL 解析 SCPD 和控制地址。URLBase 及解析出的地址都必须与描述 URL 同主机，端口可以不同。HTTP 客户端直连，不使用环境代理，不跟随重定向；单请求限时 5 秒，DNS 解析限时 5 秒，描述与 SCPD 各限 256 KiB，SOAP 限 64 KiB。

| 服务 | 动作及语义 |
|---|---|
| AVTransport | SetAVTransportURI、Play（Speed=1）、Pause、Stop、GetTransportInfo、GetPositionInfo，InstanceID=0。SCPD 缺少 Pause 时调用 Stop。GetPositionInfo 返回 RelTime 时长，设备报告 NOT_IMPLEMENTED 时返回零 |
| RenderingControl | GetVolume、SetVolume 使用 Master 通道，在 0–100 与 SCPD 声明的设备范围、步长之间换算。SetVolume 能力决定音量类型 `number` 或 `fixed` |
| ConnectionManager | GetProtocolInfo 读取 Sink。可交付 FLAC 时优先 FLAC，其次 WAV，未声明可用格式时尝试 FLAC 并设 `format_declared=false`。默认上限 48000 Hz、24 位 |

设置 URI 时传入 `BuildDIDL(TrackMetadata{Title, Artist, Album, ArtURI, URI, MIMEType})` 生成的 DIDL-Lite。资源的 DLNA 标志对应连续转码流；非 ASCII 元数据以 XML 数字字符引用编码。

## 管理员接口

统一前缀 `/api/v1/admin/upnp`。读取要求管理员会话，写入还需通过实例来源校验。成功响应及错误信封见 `backend/internal/app/testdata/api-samples/upnp/`。

| 方法与路径 | 请求 | 成功响应 |
|---|---|---|
| GET /settings | 无 | 200，`{enabled,shared}` |
| PUT /settings | 至少一个布尔字段 `enabled`、`shared` | 200，完整设置 |
| GET /devices | 无 | 200，`{items:[设备行]}`，按 UDN 排序 |
| POST /devices | `{"address":"http://192.168.1.25:8080/description.xml"}` 或裸 IP | 200，手动设备行。相同 UDN 更新登记 |
| DELETE /devices/{udn} | 无 | 204，同时清除该 UDN 的分配 |
| PUT /devices/{udn}/grants | `{"user_ids":["账号 UUID"]}` | 200，`{user_ids:[...]}`。事务内全量替换、去重排序，空数组清空 |

设备行含 `udn,name,model,address,capabilities,online,manual,user_ids`。其中 `address` 为描述 URL，`capabilities` 含 `pause,volume,stream_formats,format_declared,max_sample_rate,max_bit_depth`。

## 错误与边界样例

| 情形 | 结果 |
|---|---|
| 初始设置、发现列表为空 | `{"enabled":false,"shared":true}`、`{"items":[]}` |
| 发现来源为 192.168.1.25，LOCATION 的主机也是 192.168.1.25 | 接受描述，按同主机规则检查后续地址 |
| 来源为 192.168.1.25，LOCATION 指向 192.168.1.26 | 丢弃公告，即使两端都属于私网 |
| 描述 URL 合法，SCPD、URLBase 或控制指向另一主机 | 拒绝设备；手动添加返回 400 `invalid_address` |
| 关闭状态添加设备 | 409 `upnp_disabled` |
| 非法设备地址或被拒的地址范围 | 400 `invalid_address` |
| 手动设备未响应或响应无法解析 | 422 `device_unreachable` |
| 未知设备授权或移除未登记的设备 | 404 `not_found` |
| 非法 JSON、未知字段 | 400 `invalid_json` |
| 设置缺少布尔字段、分配缺少数组或引用不存在的账号 | 400 `invalid_input` |
| 发现监听初始化失败 | 503 `discovery_unavailable` |

## 验证接点

`fakerenderer.New(Options)` 提供可挂载的 HTTP handler 与完整的 `Serve`（SSDP + HTTP）。使用合成 WAV 或 FLAC，经发现、SetAVTransportURI 和 Play 后，核对 GetPositionInfo 与 `/_fake/state` 的 URI、状态和拉流字节。`-rel-time zero` 模拟拉流正常但位置停在零。权限和迁移检查需要独立 PostgreSQL 数据；可见性断言覆盖共享开启、关闭、分配、撤销及管理员。

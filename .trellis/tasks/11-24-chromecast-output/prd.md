# Chromecast 投放

父任务：`11-24-adoption-readiness`（第 4 项）。调研见 `../11-24-adoption-readiness/research/casting-protocols.md` 第 1、3.1 节。

## 目标

家里的 Chromecast、Google TV、Nest 音箱与内置 Google Cast 的音箱/功放出现在播放设备列表里，选中后由服务器把播放推给它们，
用法与已有的 UPnP 局域网设备一致：音效照常生效、可调音量、占用与接管、管理员控制是否共享。

## 主代理取的默认值

| 项 | 默认 |
| --- | --- |
| 开关 | 沿用“局域网设备”总开关；设置里分别列出 UPnP/DLNA 与 Google Cast 两种协议的开关，总开关打开后两者默认都开 |
| 发现 | mDNS 查询 `_googlecast._tcp`（不需要绑定 5353 以外的特权端口），加按 IP 手动添加；发现层写成通用的 mDNS 模块，AirPlay 复用 |
| 协议 | 自写最小 CASTV2 客户端（TLS 8009、protobuf 帧、connection/heartbeat/receiver/media 四个命名空间），启动默认媒体接收端 `CC1AD845`，不注册应用；不引入第三方 Cast 库 |
| 取流 | 复用托管输出的令牌拉流（`/upnp/streams/{token}` 与封面路径），对端地址规则不变 |
| 格式 | 默认 MP3 320 kbps、最高 48 kHz（新增 MP3 编码分支，固定的 ffmpeg 有 libmp3lame）；管理员可对单台设备改为 FLAC（最高 96 kHz/24 位，默认上限 48 kHz/24 位）或 AAC |
| 分曲 | 每首一个流，设备 `IDLE/FINISHED` 后加载下一首；不保证无缝，界面在设备行注明 |
| 状态 | 以设备推送的 `MEDIA_STATUS`/`RECEIVER_STATUS` 为准，每 5 秒 `GET_STATUS` 兜底；设备上的应用被换掉即视为被接管，释放输出 |
| 音量 | 接收端 `SET_VOLUME`；`controlType=fixed` 的设备按固定音量处理（与 UPnP 固定音量一致） |
| 设备键 | `cast:<TXT id>`；共享、分配、占用、成员设备行沿用局域网设备的表与规则 |
| 组 | Cast 音箱组按普通设备出现，不做多房间同步的专门处理 |

## 要求

1. mDNS 发现模块（IPv4，局域网网卡，定期查询与缓存过期）；Cast 设备注册表与手动添加。
2. CASTV2 客户端：连接、心跳、启动接收端、LOAD（`streamType: BUFFERED`，带标题、艺人、专辑、封面）、PLAY/PAUSE/STOP/SEEK 不做（设备端跳转不映射，与 UPnP 一致）、音量、状态。
3. 托管输出：与 UPnP 相同的 Acquire/占用/接管/释放/事件；播放区层不再把设备种类写死为 `upnp`。
4. 渲染编码增加 MP3；格式与采样率选择按设备设置。
5. 管理员接口与前端：局域网设备设置里显示协议开关、Cast 设备、手动添加（IP）、单台设备的格式；播放设备列表显示 Cast 设备与“Google Cast”标签。
6. 开发用假 Cast 接收端（与 `roomusic-fake-renderer` 对应）：实现足够的 CASTV2 子集，供测试与手工试用；匿名统计的 `devices_30d` 增加 `cast`。

## 验收

- 用假接收端：发现、手动添加、播放、暂停、继续、下一首（流切换）、音量、设备端停止、被其他发送端接管、离线，各自产生正确的播放区状态与事件。
- 一首曲目的流长度不受累计影响（每首一个流）。
- 出站只到局域网地址；令牌拉流的对端规则不变。
- 生产镜像在 `cap_drop: [ALL]` 下能发现并控制假接收端（同机 host 网络）。
- 390 与 1440 宽度截图核对设备列表与设置。
- 真实 Chromecast / Nest 设备由 qlqq 试用。

## 不做

- 多房间同步、Cast 屏幕上的自定义界面（自定义接收端应用）、视频。

## 实施与验收记录（2026-11-24）

- 实现（Codex gpt-6.2-astra）：`internal/devices/{mdns,cast,lan,fakecast}`、`cmd/roomusic-fake-cast`、zones 设备种类泛化与 `SingleTrack`、MP3 编码、迁移、接口与样本、统计 schema 2。审阅后追加：非 Cast 设备行输出 `"format": null`。
- 测试（Codex gpt-6.2-sol）：7 个测试文件（帧、客户端、状态机、mDNS、地址规则、MP3 与采样率、假接收端端到端、共享与格式、协议关闭、迁移夹具、统计端 schema 1/2）。发现 1 个缺陷：被其他发送端接管时没有发出设备端 pause 事件；实现代理已修，`TestPostgreSQLCast` 4 项通过。
- 前端（主代理）：协议开关、设备类型标签、手动添加选类型、Cast 编码格式对话框、播放设备列表的 Google Cast 标签（成员不显示采样率上限）；接口契约表登记匿名统计与 Cast 样本；登录页源代码链接改为不进查询缓存（修复退出后缓存非空的 3 个用例）。
- 合入时：main 已有 `0074_artist_web_search`，本任务迁移改为 `0075_cast_devices`，回退测试里本任务的分支改为 75。
- 本轮实际运行：`ROOMUSIC_TEST_RUN='Cast|UPnP|Migration|Insights|APISamples' sg docker -c ./scripts/test-integration.sh` 通过（含逐个迁移往返）；前端 `typecheck`、`lint`、全部 vitest（205 个文件、4837 项）通过；
  e2e `lan-devices.spec.ts` 5 项通过；390/1440 截图核对设置页与编码格式对话框。
- 未做：真实 Chromecast / Nest 试用；生产镜像在 `cap_drop: [ALL]` 下发现并控制假接收端的演练留到 AirPlay 子任务的部署演练一并做；`-race` 因本机没有 C 编译器未运行。

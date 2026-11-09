# 技术设计：服务端播放区与远程输出

需求与裁决以 [prd.md](./prd.md) 为准。本文写结论、架构、各需求的判定、上线回滚、子任务拆分与待裁决项，以及与音频算法线的接口（§14）。
**实时通道（§2）、输出端协议（§3）、数据模型与迁移（§4）、界面与文案（§6）、其他输出端（§7）、偏离参考稿（§9）、测试范围（§10）**在 [design-protocol.md](./design-protocol.md)；
接口合同（协议 v1 的 DTO、端点、事件、错误码）在 [design-contract.md](./design-contract.md)。章节编号三份文件共用。
参考稿正本在本机 `~/.local/share/roomusic-reference/reference-design.md`（不进仓库），本文只引用其章节号与概括；本文的 §N 指本设计的章节，参考稿章节写作“参考稿 §N”。

> 三份设计文件都控制在 32 KB 以内：Trellis 给子 agent 注入上下文时，单个文件超过 32,768 字节的部分会被截断。

## 0. 结论

- 服务端新增能力 `zones`（播放区）：持有设备、播放区、队列和传输状态。内存里是工作副本，每条被接受的命令先写库再广播。
- **只有一种交付：服务端渲染流**。一个播放区一条跨曲目的连续流，解码、电平化、无缝、淡化、抖动、编码都在服务端（算法线的渲染会话），分无损档与有损档。
  输出端只播放流、回报流内位置；服务端把位置换算成“哪首、第几秒”，据此推进队列、判定计一次播放。
- 通道选 **SSE + REST 命令**。输出端对齐服务端给出的期望状态（当前流、接续流、播放意图），回报的响应里就带最新状态，事件流被挂起时照样能对齐。
- 锁屏不断播：换曲在流内完成，媒体元素自己持续拉流，不需要页面脚本；回报由媒体事件驱动，播放中每 10 秒一次，既是心跳也是位置。断流时从断开处续一条新流。
- **协议与客户端无关**（R14）：版本 v1、文档化的 JSON 消息与样本；认领时声明能解的流格式与上限，服务端协商；多标签页协调等浏览器内部的事不进协议。
- 前端最终只剩**出声**（播放流、换流、回报位置）、**系统接口**（锁屏控件、媒体键、音量、自动播放手势）和**界面**；现有的队列、持久化、预检、预取、增益、交叉渐变、计次逻辑全部退役。

## 1. 架构与边界

### 1.1 总体

```text
        控制端（任意设备、任意标签页）                         输出端（每台设备一个）
   ┌───────────────────────────────┐                 ┌──────────────────────────────────┐
   │ UI ── controller ── zone store │                 │ UI ── controller ── zone store    │
   └──────┬───────────────▲────────┘                 │ output（<audio> 播放流）＋系统接口 │
          │REST 命令      │SSE 事件                    └──┬───────▲──────────────┬───────┘
          ▼               │                                │回报   │SSE 事件       │GET 渲染流
   ┌─────────────────────────────────────────────────────────────────────────────────┐
   │ zones/httpapi ── zones.Service（播放区、队列、按会话事件推进状态、计次、协商）         │
   │                        │ 端口 Renderer / RenderSession（算法线 dsp-pipeline 实现）    │
   │                        ▼                                                           │
   │                 渲染会话：解码 → 电平化 → 处理链 → 无缝/淡化 → 抖动 → 编码            │
   └─────────────────────────────────────────────────────────────────────────────────┘
```

播放区是播放会话唯一的所有者（参考稿 §4.1）；渲染会话是它的“播放器”（参考稿的 ZonePlayer），输出端只是把流变成声音的终端。

### 1.2 后端包与依赖方向

| 包 | 职责 | 允许导入 |
| --- | --- | --- |
| `internal/zones/queue` | 队列纯函数：四种模式、插入、移除、移动、前进后退、“下一条”（随机模式预选）；从 `frontend/.../engine/queue.ts` 逐条移植，测试表一并移植 | 无 |
| `internal/zones` | 设备与播放区服务：命令、输出端会话与租约、hub、持久化、清理；渲染会话的开启、重建与事件应用；服务端计次；协商的调用 | `catalog`、`platform/uuid`、`zones/queue` |
| `internal/zones/httpapi` | 鉴权、解码、错误映射、SSE 写出、渲染流写出 | `identity`、`platform/httpserver`、`platform/uuid`、`zones` |
| `internal/app` | 装配：把算法线的 `Renderer` 实现与 `listening.Service`（经端口 `PlayRecorder`）交给 `zones.NewService` | 增加上面三个 |

- `zones` 不导入 `listening` 与算法线的包：计次经消费方端口 `PlayRecorder`，渲染经消费方端口 `Renderer`（§14），都由 `app` 装配。`dependency_rules_test.go` 增加三条规则。
- 曲目的显示信息只经 `catalog.Store.LoadTrackSummaries` 取得，队列行只存 `track_id`（参考稿 §11.1）；艺人治理之后队列重新读取就能拿到新名字。
- 命名用 `zones` 而不是 `playback/zones`：`playback` 是媒体交付与分析，播放区是控制面，二者生命周期和权限都不同。

### 1.3 服务端运行时

- **单进程假设**：hub、在线状态、输出端会话、渲染会话、失败标记都在内存里；不支持多个 app 实例连同一个库（写进 spec）。
- **每个播放区一把互斥锁**。命令在锁内：读内存副本 → 调用 `zones/queue` → 一个事务写库 → 更新内存 → 需要时开或重建渲染会话 → 解锁后广播。渲染会话的事件也在同一把锁里应用。
- 内存副本在第一次访问该用户时从库里加载。位置随会话的 `time` 事件更新，最多每 10 秒写一次库。只在内存的：播放意图、状态、故障、失败标记、连续失败计数、在线与租约、随机预选、渲染会话。
- **转移**锁两个播放区，按 zone ID 升序加锁，同一个事务。
- **关停**：事件流与渲染流 handler 监听应用生命周期，结束时立即返回；渲染会话随之关闭（ffmpeg 进程由算法线回收）。

### 1.4 前端：留下什么，退役什么

| 留下（`features/player/`） | 退役（随切换一并删除） |
| --- | --- |
| **output**：两个 `<audio>` 元素播放渲染流、按 §3.5 换流、回报位置、释放；按流的分段表在本地切换锁屏信息 | `engine/player-engine.ts` 及其测试：预检与 202 等待、派生、预取、失败分类、交叉渐变与 audio-graph、电平化选择 `leveling.ts` |
| **系统接口**：Media Session（按钮发命令给本机播放区）、媒体键、音量（写元素 `volume`；iPhone/iPad 声明不可调）、自动播放手势（本机首次点击时同步解锁两个元素） | `queue.ts` 的纯函数、`persistence.ts` 的队列保存与恢复、`queue-restore.ts`（R11 导入后只保留一次性读取） |
| **zones 客户端**：api 与解码、事件流、zone store（时钟偏移、进度外推）、controller（命令方法名与现在的引擎一致）、设备身份、R11 导入 | `listen-tracker.ts` 与 `features/listening` 的播放上报器（`POST /me/plays` 后端保留给旧标签页） |
| **界面**：播放栏、全屏、队列抽屉、歌词、设备列表、设置页（组件少改，`usePlayer()` 仍返回 `PlayerSnapshot` 形状） | `capability.ts` 的 `canPlayType` 判定（可播与否改看服务端格式表，见 §5.10） |

不再先做“行为保持的拆分”：留下的部分与现有引擎几乎没有可复用的代码（没有预检、没有队列、没有增益），拆分只会产出马上要删的门面。直接新写 output 模块，在切换子任务里删除旧引擎。

## 5. 各需求的判定

### 5.1 R1 设备身份与多标签页

- 设备 ID 由服务端在登记时生成；浏览器存在 `localStorage`（按账号命名，合同 §8）。同一浏览器的标签页共用它；首次登记用 Web Lock 串行。认领得到 404（被清理或删除）就重新登记。退出登录不删除设备 ID。
- 谁出声：每个标签页请求 Web Lock `roomusic:output:<deviceId>`，拿到的创建 output 并认领；其他标签页是纯控制端，不创建音频元素。出声的标签页关闭时锁释放，排队的下一个自动拿到。
- 接手：排队拿到锁的标签页认领时带 `takeover: true`。原会话释放后 10 秒内、且播放区因释放而暂停时，服务端从原位置开新流并恢复播放。普通刷新带 `false`，刷新后保持暂停。接手的页面没被点过时按“自动播放被拒”处理（prd 的验收前提）。
- 会话栅栏：认领生成新会话，旧会话的回报得 `409 output_superseded` 并停声。不需要 BroadcastChannel：所有标签页都从服务端拿状态。以上全是浏览器客户端的实现，协议里只有通用的 `takeover` 字段。

### 5.2 R3 控制与实时同步

- 所有命令走 `POST /zones/{id}/commands`，**不带期望版本号**，服务端按播放区串行执行：同一账号几台设备之间几乎没有并发编辑；按条目 ID 的操作本身不会误伤别的条目，条目已不存在时是无操作；“下一首”按两次就前进两首。
- **不接入现有幂等框架**：播放命令不进操作记录，客户端不自动重试命令。计次由服务端判定，幂等键在服务端生成（§3.8）。导入（R11）靠“播放区为空且没导入过”天然只生效一次。
- 发命令的标签页直接用响应里的新状态更新 store；其他标签页经事件流更新；按 `revision` 丢弃旧状态。
- 进度：控制端按 `position.seconds + (服务端现在 − at)` 外推，播放中每 250 ms 刷新；输出端自己用流内位置与分段表算，更精确。

### 5.3 R4 控制目标与回退

- 控制目标存在浏览器的 `localStorage`（合同 §8），同一浏览器的标签页共用。没有或无效时是本机播放区。
- **有效目标** = 存的目标仍在、设备在线，并且（正在播放，或暂停不超过 **15 分钟**）；否则是本机，并清掉存的目标。按 `paused_at` 与时钟偏移计算，每分钟与页面重新可见时重算。
- 播放类命令（立即播放、播放、播放此项）总是发给有效目标；转移成功后控制目标跟到目标播放区。

### 5.4 R5 转移

`transfer {target_zone_id}`，同一事务、两个播放区按 ID 升序加锁：目标必须在线（否则 `409 zone_offline`）、与源不同，源可以离线；
删除目标原有条目，把源的条目整体改到目标（条目 ID 不变），当前条目、位置、模式、历史、随机袋一起搬；播放设置不搬（属于播放区），音量属于设备也不搬。
源关闭渲染会话、清空、意图暂停；目标**无论源是否暂停都直接播放**（用户裁决），从原位置开新流。两个播放区的 `revision` 与 `queue_revision` 都加一。
已接受的限制：转移前在源上累计的计次不带过去。

### 5.5 R6 离线、删除与清理

- 在线判定、关页面立即离线、锁屏仍在播的处理见 §3.7。离线设备的播放区留在列表里，标“离线”，队列与位置都在。
- **删除**只能删离线设备（在线时 `409 device_online`），级联删除播放区与队列，界面先确认。**自动清理**：30 天没上线（用户裁决），每小时扫一次。
- 离线播放区上会让它播放的命令返回 `409 zone_offline`；改队列、改模式、改设置照常接受。

### 5.6 R9 音量

- 音量属于设备，服务端存一份；本机与远端都经 `PUT /devices/{id}/volume` 改。输出端在本机拖动时先立即作用于元素，再按 125 ms 节流发出（参考稿 §10.1）；远端同样节流，并乐观显示 1 秒。
- 能否调音量是认领时声明的能力 `volume`（浏览器用“写 0.5 读回”判定）；`fixed` 时 `PUT volume` 返回 `409 volume_fixed`，控制端禁用音量控件。
- 音量只作用于输出端的元素，不进渲染流（渲染流里的增益只有电平化与处理链）。

### 5.7 R10 不退化

- 各种格式可播：渲染会话现场解码（ALAC、APE、WavPack、AIFF 等），不再需要派生缓存。
- 电平化：增益选择与施加都在渲染会话（算法线 P6），规则与现有前端相同；播放区只把“自动模式要的同发行上下文”交给会话（§14）。
- 交叉渐变、无缝：在渲染流里（算法线 gapless-crossfade），设置归播放区。
- 计一次播放：服务端按会话派发的位置判定（§3.8），规则不变；写入后推送 `play_recorded`，浏览器据此刷新历史查询。
- 歌词同步：输出端用本地位置，控制端用外推位置。Media Session：输出端按分段表在曲目边界换锁屏信息。失败原因：会话打开来源时沿用现有安全打开的错误分类，事件带原因。

### 5.8 R2 重启恢复

- 播放区第一次被加载时：意图暂停、位置取库里的值、设备全部离线，并记 `offline_paused`。
- 重启时浏览器手里的流随连接断开而中断（先放完已缓冲的部分）；输出端回报得 `404 output_unknown`，重新认领时带上正在放的条目与曲目内位置；条目还在队列里就从那里开新流、接着放（用户裁决）。
  没有设备回来的播放区保持暂停、位置保留。

### 5.9 R11 迁移旧队列

- 浏览器每个标签页在登记完成后读自己的旧 `sessionStorage` 队列（沿用现有解码），有内容就向本机播放区发 `import`，之后删除这个键。
- 服务端只在本机播放区为空且没导入过时导入；查不到的曲目丢弃，当前条目被丢弃时取其后第一个保留下来的、位置从 0 开始（同现有 `rebuildQueue`）。导入后保持暂停，公告 `queue_restored`。
- 多个旧标签页各自的队列只有第一个导入的生效。

### 5.10 R7 名称、R8 隔离与可播判定

- 自动命名由浏览器按 User-Agent 生成“系统 · 浏览器”，服务端在同名时追加“ 2”“ 3”；改名 1–64 个字符。
- 隔离：所有接口只作用于已鉴权的用户；不属于该用户的设备、播放区、会话、流一律 `404 not_found`；事件流只推本人的数据。管理员也只看自己的。
- 可播判定：曲目能不能放只看服务端格式表 `GET /playback/formats`（渲染会话能解码的组合，由算法线维护），与设备无关；前端的 `canPlayType` 判定退役。

### 5.11 R14 客户端无关

- 控制接口与输出端协议按“任何客户端都能实现”来定：版本 v1、只做增加；消息是 JSON，事件流是标准 SSE；实现时整理成 spec，并附由真实 handler 生成的样本（合同 §1）。
- 输出端认领时声明能力：能解的流格式（编码、容器、最高采样率、位深、声道）、DSD 方式、独占输出、音量；服务端按能力与档位协商流格式（§3.9）。
- 浏览器特有的东西（Web Locks、`keepalive`、sessionStorage 导入、UA 命名、手势解锁）只在浏览器客户端里；协议里对应的只是通用字段。
- 鉴权不排除原生客户端：协议不规定凭据形式，流地址里不放凭据；以后加令牌鉴权只改 `Guard`，Cookie 会话的来源校验不套用到令牌请求上。

## 8. 兼容、上线与回滚

- **上线方式**：本线与算法线的 dsp-pipeline、gapless-crossfade、dither 作为第一批，在集成分支上合齐、通过 E 之后**一次合入 main 并发版**，没有过渡期的直出版本。
- **派生退役**（实现归 dsp-pipeline P10）：删除派生管理器与预检里的 202 等待；`ROOMUSIC_MEDIA_CACHE_MAX_MIB` 不再使用，README 删掉这一行（仍设置也只是被忽略）。
  启动时把 `<ROOMUSIC_DATA_DIR>/media-cache` 整个删掉，只删一次、限定在数据目录内（沿用现有“缓存目录与音乐根不重叠”的检查），日志 `playback.derivation.cache_removed` 带删掉的字节数；README 写一句“升级后会自动删除旧的 ALAC 转码缓存”。
- **旧前端访问新后端**：升级时已经打开的旧标签页还在用浏览器本地播放。`GET /tracks/{id}/playback` 与 `/audio` 本批**保留**：浏览器能直接解码的格式（FLAC、MP3 等）照常能放；
  原来要派生的格式（ALAC、APE、WavPack、AIFF）得到现有的 `not_directly_playable`，显示“不能直接播放”。旧标签页刷新后即换成新前端，并导入自己的旧队列。
  `POST /me/plays` 保留（旧标签页仍在上报）。这三个接口在下一个版本删除，届时一并删掉 `playback-contracts.md` 中对应的合同。
- **新前端访问旧后端**（只在开发或回滚时）：`/me/playback/*` 得 404，界面显示播放不可用，不做兼容层。
- **升级**：迁移只建表，迁移前照常自动备份。**降级**到第一批之前的版本：回退 SQL 删三张表、服务端队列丢失（新前端已删掉旧 sessionStorage 队列，所以各设备队列为空）；
  旧版本的派生缓存目录不存在，按需重新转换。README“升级与降级”加一句“降级后各设备的播放队列会清空”。
- **回滚点**：第一批的合入提交。revert 后回到浏览器本地播放；新表留着无害；已删除的派生缓存由旧版本按需重建。

## 11. 子任务拆分

不再有 F1（行为保持的拆分），理由见 §1.4。

| 子任务 | 范围 | 验收要点 | 依赖 |
| --- | --- | --- | --- |
| **B1 数据与队列语义**（后端） | 迁移与回退 SQL；`zones/queue` 移植（含随机预选的“下一条”）；存储层 | 移植的队列用例全过；迁移升级/回退；依赖规则 | — |
| **B2 控制面**（后端） | 设备、快照、队列读取、全部命令、转移、导入、清理、hub、隔离；`Renderer` 端口与**测试用假渲染器**（输出静音 WAV、按脚本发事件） | 合同 §3 按错误矩阵工作；转移与导入正确；命令按假渲染器开/重建会话 | B1 |
| **B3 实时与输出端**（后端） | 事件流；认领（协议版本、能力）、回报、释放；租约与离线；重启恢复；渲染流端点；会话事件的应用（换曲、失败、接续流）；服务端计次与 `PlayRecorder`；消息样本；README 反代说明 | 合同 §4、§5；离线 45 秒与释放立即离线；锁屏断流续流；重启后接着放；计次恰好一次 | B2 |
| **F2 播放区客户端**（前端） | zones 客户端、output（播放流、换流、回报、手势解锁、Media Session、音量）、Web Locks 多标签页、R11 导入；删除旧引擎与 §1.4“退役”列 | 对着假渲染器的后端：两个标签页只有一个出声、关掉后另一个接着放；导入旧队列；锁屏信息随分段切换 | B3（联调）；按合同可与 B2/B3 并行编码 |
| **F3 设备与远程控制界面**（前端） | 设备列表、控制目标与回退、转移、改名删除、远端音量、离线显示、“设置 › 播放”（电平化、无缝、交叉渐变、音质档位）作用于控制目标、文案 | 五种宽度与 axe；文案符合约束；对着假渲染器走通手机控制电脑、转移、离线三条的界面流程 | F2 |
| **E 第一批验收**（测试） | 真实服务（真实渲染器 + Chromium + 合成素材）覆盖 PRD 验收各条；iOS 真机手工核对；spec 更新 | 验收全过 | B3、F3，以及 dsp-pipeline、gapless-crossfade、dither 合入集成分支 |

与算法线的依赖：

- **B2 定下 `Renderer` 端口之后**，dsp-pipeline 就可以按 §14 实现真实渲染器（它的 iOS 交付小验证可以更早，与 B1 并行）。gapless-crossfade、dither 只在渲染会话内部，不依赖本线的代码。
- **联调具备条件**：B3 + dsp-pipeline 的渲染器能出一条可播放的无损流时，F2 换成真实渲染器联调本机播放；在此之前 F2、F3 对着假渲染器开发与测试。
- **E 具备条件**：本线 B1–B3、F2、F3 与算法线三项都合入集成分支之后。“不退化”中的无缝、交叉渐变、电平化、ALAC 等格式只有那时才能验。

可并行：B1 ∥ dsp-pipeline 的 iOS 验证；B2→B3 ∥ F2（按合同编码）；dsp-pipeline 在 B2 后实现渲染器，gapless-crossfade、dither 在它的处理链骨架上并行；F3 在 F2 后；E 最后。
每个子任务的测试由测试 agent 编写与执行，实现 agent 不写新测试。

## 12. 已裁决（设计审阅）

- “设置 › 播放”作用于**正在控制的那台设备**，标题旁显示设备名（2026-11-08 用户）。

## 14. 与音频算法线的接口

第一批里算法线负责渲染会话本身（dsp-pipeline：解码、预缓冲、电平化选择与施加、处理链、编码、无损/有损档、未来事件队列、iOS 交付；gapless-crossfade；dither）。
本线负责播放区、协议与会话的生命周期。两边只经下面的端口交互（Go，`zones` 定义，算法线实现，`app` 装配）：

```go
// Renderer 开渲染会话。Negotiate 按输出端能力与档位选编码（参考稿 §1.2）。
type Renderer interface {
	Negotiate(caps Capabilities, quality string) (StreamFormat, error) // 只填 Codec、MimeType、Quality；没有可用格式时返回错误，播放区报 fault: no_stream_format
	Open(ctx context.Context, spec RenderSpec) (RenderSession, error)  // 只在渲染器不可用或忙（并发上限）时返回错误，zones 按 transient 处理；曲目级问题都经事件
}

type StreamFormat struct {
	Codec, MimeType, Quality       string
	SampleRate, BitDepth, Channels int // Open 确定首曲后由 RenderSession.Format() 给出（对应合同 Stream.format）；有损档 BitDepth 为 0
	Delivery                       string `json:"-"` // stream（长流）| hls；Negotiate 按 (codec, mime_type) 配对填写，zones 据此给 Stream.url
}

type RenderSpec struct {
	UserID, ZoneID string
	Seq            int64        // 播放区的流序号
	Start          Entry        // 从这一条开始
	StartSeconds   float64      // 曲目内的起始秒数
	Settings       ZoneSettings
	Format         StreamFormat // Negotiate 的结果（只有前三项）
	Cursor         Cursor       // 会话要准备下一首时向播放区取
}

type ZoneSettings struct {
	Leveling           string // off | auto | track | album
	Gapless, Crossfade bool
	CrossfadeSeconds   int
	Quality            string   // lossless | lossy
	Headroom           Headroom // 列由 dsp-pipeline 的迁移加（P7），本线迁移不建
	// 算法线第二批在此增加处理项
}

type Headroom struct {
	Enabled       bool
	DB            float64
	ClipIndicator bool
}

type Entry struct {
	EntryID, TrackID string
	AlbumContext     bool // 自动电平化用：不是随机模式，且列表里前一项或后一项与它同一发行
}

// Cursor 由 zones 实现。Next 按自然前进给出 after 之后的条目（跳过已失败的、单曲循环给自己、随机给预选结果），取走即视为“已准备”。
// 会话在自己的协程里调用 Next；Next 会取播放区锁，因此不能回调会话。
type Cursor interface{ Next(afterEntryID string) (Entry, bool) }

type RenderSession interface {
	MimeType() string
	Format() StreamFormat          // 具体格式，Open 返回后即可取
	Body() io.Reader               // 长流：只能被读一次；zones/httpapi 原样写给输出端；Close 后返回 EOF。HLS 会话立即 EOF
	HLS() HLSMedia                 // Delivery 为 hls 时非 nil；长流为 nil
	Observe(sample PlayheadSample) // 输出端回报的播放头：流内秒数、采样时刻、是否在放
	NextChanged()                  // 已取走的下一条失效：还没进入已发出的字节就悄悄换掉，否则发 rebuild_needed
	Events() <-chan Event          // 带缓冲；zones 持续读到通道关闭
	Close()                        // 可重复调用；之后 Body 返回 EOF、Events 关闭
}
// Observe、NextChanged、Close 不阻塞，可以在播放区锁内调用。

// HLSMedia 是 HLS 会话的播放列表与分片（合同 §4.5；格式与保留规则见 dsp-pipeline design-delivery.md §5），可重复读取。
type HLSMedia interface {
	Playlist(ctx context.Context) ([]byte, error) // 不足 3 片且流没结束时等待，最多 10 秒
	Init() ([]byte, error)
	Segment(n int64) ([]byte, error)              // 没产出或已删除：ErrSegmentMissing；会话已关闭或出错：ErrStreamGone
}

// Event.Kind 分两类（zones 不能假定所有事件都按播放头到达）：
// 到达播放头才派发（参考稿 §7.4）——time（每 200 ms，带 EntryID 与曲目内秒数）、
//   track_started（带 Transition：gapless 即 OnToNext、crossfade、gap；起始条目不发，zones 开流时自己产生）、
//   track_failed（带 Reason；起始条目打不开也发，随后会话经 Cursor 跳到下一首）、stream_ended、带分段的 signal；
// 立即发出——format_boundary（带 NextEntryID 与 Reason：format 下一首格式不同，not_ready 下一首超时没准备好；可重复发送，
//   zones 以最新一条替换 next_stream）、rebuild_needed、削波与处理速度的 signal 更新。
// 另带 Segments：已渲染、尚未播完的曲目分段，供输出端切换锁屏信息。
```

| 算法线的需要 | 落在本设计的哪里 |
| --- | --- |
| 只有渲染流一种交付，无损/有损两档 | 合同 §2.2 的 `stream`、`next_stream`、`Stream{seq,url,mime_type,quality,format,segments}`；§4.4 长流端点、§4.5 HLS 端点；设置 `quality`（本线迁移建列） |
| 能力协商 | 合同 §2.6 `Capabilities.stream_formats` 等；`Renderer.Negotiate` 选编码，采样率、位深、声道由 `RenderSession.Format()` 在首曲确定后给出；协商失败报 `fault: no_stream_format` |
| 位置换算、OnToNext、计次 | 会话按播放头派发 `time`、`track_started`、`track_failed`、`stream_ended`，`format_boundary` 与 `rebuild_needed` 立即发出；`zones` 应用 `track_started`（自然前进，不重建）、`time`（位置与计次）、`track_failed`（失败计数、跳过）、`format_boundary`（以最新一条替换接续流）（design-protocol.md §3.4） |
| 不需要协议层前瞻 | 下一首经 `Cursor` 在服务端内部取，会话按参考稿 §7.3 预缓冲；协议里只有格式边界时的“接续流” |
| 重建流 | 定位、改设置（300 ms 去抖）、切模式或改队列使已准备的下一首失效（`NextChanged` → 必要时 `rebuild_needed`）、暂停超过 5 秒后恢复；不要求样本级衔接，短暂换流可接受（同 Roon） |
| 处理设置按播放区，IR 与 EQ 预设按成员 | `playback_zones` 已有电平化、交叉渐变、`gapless`（默认 true）、`quality`；`headroom` 的列由 dsp-pipeline 的迁移加（P7），第二批的处理项同样由算法线迁移加；预设与 IR 表带 `user_id`，`set_settings` 经算法线端口核对属于播放区主人（design-protocol.md §4） |
| 开流失败与起始条目 | `Open` 返回错误只表示渲染器不可用或忙：记 `fault: transient`、意图暂停，`retry` 可重试；起始条目的 `track_started` 由 zones 开流时产生，打不开时会话发 `track_failed` 并自己跳到下一首 |
| 长流只能被取一次，HLS 可重复取 | 同一条长流的第二个连接得 `410 stream_gone`，输出端按 `stream_lost` 续流（合同 §4.4）；HLS 的播放列表与分片在流有效期内可重复取（合同 §4.5） |
| 派生退役 | §8（实现归 dsp-pipeline P10） |
| 信号路径 | 会话的 `signal` 事件原样放进播放区状态的 `signal` 字段；结构与界面归 dsp-pipeline P11 |
| iOS 交付 | 已定（2026-11-09）：iPhone/iPad 用 HLS（`HLS() HLSMedia`、合同 §4.5），桌面用长流；浏览器取值见 dsp-pipeline design-delivery.md §3、§5.7，协议形状不变 |

# M2a 技术设计

依据 [prd.md](prd.md) 的裁决 K1–K3 与 ADR 0004。本文定义接口合同、模块边界、数据流和测试方法；
执行顺序见 [implement.md](implement.md)。不新增迁移，不引入 FFmpeg 运行时依赖。

## 1. 边界

### 后端

新增能力包 `backend/internal/playback`（不含 HTTP）与 `backend/internal/playback/httpapi`。
ADR 0004 §3 要求媒体用例通过窄合同消费身份、catalog 与 library，不直接读其他模块的私有表：

| 合同 | 所属 | 内容 |
| --- | --- | --- |
| `catalog.Store.LoadTrackSource(ctx, trackID)` | catalog（新增） | 只返回 `source_status='present'` 的曲目：root ID、相对路径、`source_kind`、`codec`、扫描时的大小与修改时间、内容指纹；不存在或不可见时 `ErrTrackNotFound` |
| `library.LoadRootLocation(ctx, queryer, rootID)` | library（新增） | 根目录的绝对路径与是否启用（`RootLocation{Path, Active}`）；不存在时 `ErrRootNotFound` |
| `library.ValidatePath(path, allowedRoots)` | library（已有） | 播放时复核根目录仍在配置允许的范围内、不是符号链接 |
| `identity.Guard.Authenticate(request)` | identity（已有） | 会话有效、用户未停用；初次请求与 10 秒复核都用它 |

依赖规则（`backend/internal/app/dependency_rules_test.go`）新增：
`"playback": {imports: ["catalog", "library"]}`、
`"playback/httpapi": {imports: ["identity", "playback", "platform/httpserver", "platform/uuid"], http: true}`，
`app` 增加 `playback`、`playback/httpapi`。包名不用 `media`，因为 `media` 已是 Medium 表名。

### 前端

新增 `frontend/src/features/player/`（引擎、接口、组件、hook），公开入口只导出外壳与页面组合所需的内容。
catalog 组件不依赖 player：`ReleaseMedia` 增加 `renderTrackActions` 与 `currentTrackId`，`AlbumHeader` 增加 `actions` 插槽，
由 `app/routes/album-detail-page.tsx` 把 player 的组件放进去。player 只经 catalog 的公开入口取 DTO 类型和发行详情查询。

## 2. 后端接口合同

全部需要登录；JSON 响应沿用统一错误信封与 `Cache-Control: no-store`。检查顺序固定，先鉴权再确认资源，
未登录时不泄露曲目是否存在、文件长度或格式：

`401 unauthorized` → `400 invalid_id` → `404 not_found`（曲目不存在或不可见）→ `409 root_disabled` →
`409 not_directly_playable`（CUE 虚拟轨或不在直出格式表中）→ `409 source_unavailable` / `409 source_changed` → 音频接口的 Range 检查（`416`）。
数据库错误为 `503 database_unavailable`。

### 2.1 `GET /api/v1/playback/formats`

返回批准直出的格式表，前端据此判断“服务端是否愿意直出”，再用浏览器能力判断“能否解码”：

```json
{ "items": [ { "source_kind": "flac_vorbis_comment", "codec": "flac", "mime_type": "audio/flac", "codecs": null } ] }
```

格式表是代码里的静态表，**只有拿到验证证据的组合才保留**（K2）：

| `source_kind` / `codec` | `mime_type`、`codecs` | 需要的证据 |
| --- | --- | --- |
| `flac_vorbis_comment` / `flac` | `audio/flac`、无 | 自动化（16/44.1、24/48）加三组手工 |
| `m4a` / `alac` | `audio/mp4`、`alac` | macOS Safari 手工实播；失败则移除，ALAC 在 M2a 处处不可播 |
| `mp3_id3v2` / `mp3` | `audio/mpeg`、无 | 自动化加手工 |
| `wav_info` / `pcm` | `audio/wav`、`1` | 自动化（16 bit）。24 bit 的 WAV 被常见工具（包括 FFmpeg）写成 WAVE_FORMAT_EXTENSIBLE，扫描记为 `wav_extensible`，不在表中 |
| `m4a` / `aac` | `audio/mp4`、`mp4a.40.2` | 自动化（AAC-LC 样本）；解析器不区分 AAC 档次，HE-AAC 若解码失败走运行时错误路径 |
| `ogg_vorbis_comment` / `vorbis` | `audio/ogg`、`vorbis` | 自动化 |
| `opus_tags` / `opus` | `audio/ogg`、`opus` | 自动化 |

不在表中：`cue_virtual`（需要精确切分，M2b）、`ieee_float`/`wav_extensible`/其他 WAV 编码、未知组合。

### 2.2 `GET /api/v1/tracks/{id}/playback`

播放前的核对。执行与音频接口完全相同的检查（含打开文件、比对版本、识别容器），然后关闭文件：

```json
{ "track_id": "…", "representation": { "kind": "original", "url": "/api/v1/tracks/…/audio", "mime_type": "audio/flac", "codecs": null, "size_bytes": 25165824 } }
```

`kind` 目前只有 `original`；M2b 可加入派生表示及其等待状态，前端不自己拼接音频地址。

### 2.3 `GET|HEAD /api/v1/tracks/{id}/audio`

- 检查通过后用 `http.ServeContent` 输出（`GET` 路由在 Go 1.22+ 同时匹配 `HEAD`）。修改时间传零值，不发 `Last-Modified`。
- 响应头：`Content-Type`（格式表的 `mime_type`，不带参数）、`Cache-Control: private, no-store`、强 `ETag`、`Accept-Ranges: bytes`、
  `X-Content-Type-Options: nosniff`。`ETag` 为 `SHA-256(track_id | 内容指纹 | 大小 | 修改时间纳秒)` 的前 32 位十六进制；
  文件不变时稳定，变化时必然不同（变化本身会先被 `source_changed` 拒绝）。
- Range：先自行解析。只接受一个 `bytes=` 范围（含 `bytes=N-`、`bytes=-N`）；语法错误或多个范围返回 `416 range_not_satisfiable`（JSON），
  范围不可满足时同样 `416` 并带 `Content-Range: bytes */<大小>`；合法的单范围交给 `ServeContent`，由它处理 `If-Range`（强比较）与 206。
- 响应头发出之后出错只中止连接，不在音频尾部追加 JSON。

### 2.4 错误码与文案

| 状态 | `code` | 服务端 `message` | 触发 |
| --- | --- | --- | --- |
| 404 | `not_found` | 曲目不存在 | 曲目不存在或不再 `present` |
| 409 | `root_disabled` | 曲目所在的目录已停用 | 根目录 `status='disabled'`（K3） |
| 409 | `not_directly_playable` | 这首曲目不能直接播放 | CUE 虚拟轨、不在格式表中 |
| 409 | `source_unavailable` | 音频文件无法读取 | 文件或根目录缺失、权限不足、不是普通文件、链接逃出根目录、根目录不再被配置允许 |
| 409 | `source_changed` | 音频文件在上次扫描后有变化，请重新扫描 | 大小或修改时间与扫描记录不符（或记录为空），容器标识与格式不符 |
| 416 | `range_not_satisfiable` | 请求的范围无效 | 见 2.3 |

## 3. 打开文件与版本核对

1. `library.LoadRootLocation` 取根路径与状态；`ValidatePath(root.Path, allowedRoots)` 必须原样返回该路径，否则 `source_unavailable`。
2. `os.OpenRoot(root.Path)` 后 `root.OpenFile(relativePath, O_RDONLY|O_NONBLOCK, 0)`：`os.Root` 拒绝 `..` 与逃出根目录的链接；
   `O_NONBLOCK` 让检查后被换成 FIFO 的文件也不会阻塞打开。随后对**已打开的句柄** `Stat`：必须是普通文件。
3. 句柄的大小、修改时间（截到微秒，与 `TIMESTAMPTZ` 一致）必须等于 `tracks.source_size`、`source_modified_at`；任一为空或不等即 `source_changed`。
   这是“严格”策略：改了标签而没重扫的文件暂时不能播，提示重新扫描；好处是不会用旧曲目信息播放已被替换的内容。
4. 用 `ReadAt` 读文件头识别容器，接受范围与扫描器的解析器一致，避免扫描成功的文件在这里被误判：FLAC 为 `fLaC`（解析器不收录前置 ID3v2 的 FLAC）、
   MP4 为顶层有 `ftyp`（解析器允许它之前有别的 atom，最多往后找 8 个）、MP3 为 `ID3` 或帧同步、WAV 为 `RIFF…WAVE`、Ogg 为 `OggS`；
   与格式表不符即 `source_changed`，读文件头本身失败为 `source_unavailable`。
5. 根目录句柄用完即关；文件句柄在响应结束时关闭。源文件始终只读。

## 4. 已建立传输的复核（K3）

- 新请求（包括每个 Range 请求与预检）都完整鉴权。
- `ServeContent` 读取的是包装后的 `recheckingReader`：距上次复核满 10 秒时，在下一次 `Read` 里同步复核
  `Guard.Authenticate(request)` 与 `playback.StillPlayable(ctx, trackID, rootID)`（曲目仍 `present`、根目录仍 `active`）。
  任一失败或数据库出错（失败即关闭）时 `Read` 返回错误，`ServeContent` 停止写出；响应长度不足，net/http 关闭连接。
- 进程关停时（应用生命周期上下文结束）同样中止，避免 `Shutdown` 等满 10 秒。
- 中止时记一条 `playback.stream.aborted` 日志（原因：`session_invalid`、`track_unavailable`、`root_disabled`、`check_failed`、`shutdown`），
  只含曲目 ID，不含路径。复核间隔是包内常量，测试可注入更短的间隔与假时钟。
- 合同措辞：撤销后新请求立即被拒绝；已建立的传输最迟约 10 秒（加上网络缓冲）被服务端切断；浏览器已缓存的音频无法收回。
  同一浏览器内退出登录时，前端立即停止播放并清空队列，不依赖服务端切断。
- 停用目录同时停止播放（K3）：专辑仍可浏览；播放时返回 `root_disabled`。停用确认框与结果提示的文案同步改为
  “停用后，扫描会跳过这个目录，其中的曲目也不能播放；已入库的信息保留，恢复后即可播放”。

## 5. 前端：播放引擎

### 5.1 生命周期与状态

- `PlayerEngine` 是不依赖 React 的类，持有一个分离的 `HTMLAudioElement`（`new Audio()`，不放进 DOM）、队列、当前条目、模式与音量；
  对外提供 `subscribe`/`getSnapshot`，React 用 `useSyncExternalStore` 读取不可变快照。
- `PlayerProvider` 放在 `AuthenticatedLayout` 的会话代次子树内（`frontend/src/app/shell/authenticated-layout.tsx:13`）：
  换用户、退出或会话失效时由会话 `onEnded` 同步调用 `endSession()`，停止音频、清空 `src` 并 `load()` 中断网络、清除 Media Session 与本标签页保存的队列。
  普通卸载只 `detach()` 中断工作并保留刷新恢复记录，不能等到卸载才区分是否注销（2026-10-05 续接时确认的入口合同）。
- 状态：`idle`、`loading`（预检与加载）、`playing`、`paused`、`buffering`（`waiting`，收到 `playing` 回到播放）、`error`；进度、时长、可拖动范围都只取自媒体事件。
  `stalled` 只说明网络暂时没送来数据，已缓冲的音频照常出声且之后不保证有 `playing`，不改变状态（2026-10-05 审查修正）。
  `loading`、`playing`、`buffering` 都是“要播放”：播放/暂停按钮写“暂停”，按下即暂停，加载中则放弃这次加载。
  只在“曲目切换”和“出错”时向读屏公告，不按进度播报。

### 5.2 队列与模式（`engine/queue.ts`，纯函数）

- 条目 `{ entryId, item }`；`entryId` 每次入队新生成，同一曲目可多次出现，移除与移动按 `entryId`。
- 立即播放（整张或从某首开始）：用该版本全部可播放曲目替换队列，从选中项开始；下一首播放：插在当前项之后；加入队列：追加到队尾，不打断当前播放。
- 模式四选一：顺序（末项自然结束后停在暂停）、列表循环（回到首项）、单曲循环（只影响自然结束，手动上一首/下一首照常切换）、
  随机（剩余条目随机排列，多于一项时不连续重复；“上一首”沿实际播放历史返回，离开的条目放回本轮，之后仍会轮到）。
- 移除当前项：停止并切到下一项（没有则停在空闲）；清空队列需确认。队列上限 10,000 条（与发行详情的曲目上限一致），超出时拒绝并提示。

### 5.3 播放一首曲目

1. 预检 `GET /tracks/{id}/playback`，再把 `audio.src` 设为返回的 `url`，用户意图为播放时调用 `play()`。
2. 预检失败：`401` 交给会话边界（结束会话、清理）；`404`/`409` 把该条目标为失败并显示原因，按模式自动前进到下一项
   （全部失败则停止）；网络错误或 `503` 停在暂停并提供“重试”。
3. 媒体 `error` 事件：重新预检以区分原因；预检成功则判定为解码失败（“浏览器无法解码这首曲目”），标为失败并前进；其余同第 2 步。
4. `play()` 被自动播放策略拒绝时停在暂停并提示“请按播放”，不显示“正在播放”。
5. 拖动只在时长有限且 `seekable` 非空时启用；未知时长显示“—:—”。

### 5.4 能力判定

- `GET /playback/formats` 由 Query 缓存（会话内不过期，会话结束随 Query 清空）。
- 一首曲目可播放 = 不是 `cue_virtual` 且（`source_kind`, `codec`）在格式表中，且 `canPlayType(mime; codecs)` 不为空字符串，
  且浏览器提供 `mediaCapabilities.decodingInfo` 时其 `supported` 不为 `false`（API 缺失或抛错时只看 `canPlayType`）。结果按类型参数缓存。
- 不可播放的原因与文案：CUE 虚拟轨“CUE 分轨曲目需要切分，目前不能播放”；不在格式表“这种格式目前不能直接播放”；
  浏览器不支持“当前浏览器不能解码 ALAC”（编码名取自曲目事实）。目录停用、文件变化只能在预检时得知，作为播放失败原因显示。
- 整张播放与加入队列只放入可播放的曲目，并提示跳过的数量；一首都不能播放时按钮禁用并在专辑头部说明原因。

### 5.5 刷新后恢复（K1）

- 存 `sessionStorage`（只在本标签页、刷新后仍在、关闭标签页即消失，多个标签页互不覆盖），键 `roomusic:player:v1:<userId>`。
  只存标识：`{ version: 1, entries: [{ entryId, trackId, releaseId, groupId }], currentEntryId, mode, positionSeconds, history }`，
  不存标题、路径或凭据（ADR 0001 不把私有服务器数据放进浏览器持久层）。
- 写入时机：队列变化、暂停、切歌、`pagehide`，播放中每 5 秒一次；写失败只是不保存，不提示“已保存”。
  定位生效前（元数据或可定位范围未到）保存或出错重试时用待定位的位置；在队首按“上一首”从头重播时作废这个位置；队列未变时复用已序列化的条目。
- 恢复：解码失败或版本未知即丢弃；按 `releaseId` 去重后经发行详情查询（并发 4）重建条目；已不存在的曲目丢弃并提示数量；
  恢复后保持暂停，首次按播放时先预检再定位到保存的位置。
- 会话结束（退出、失效、换用户）时删除该键；音量与静音是设备偏好，存 `localStorage` 的 `roomusic:player-volume:v1`，不随会话清除。

### 5.6 系统媒体键

Media Session：曲名、艺人、专辑与封面（`/api/v1/artworks/{id}` 的绝对地址，取不到就没有封面）；处理播放、暂停、上一首、下一首、
停止、定位、快退/快进 10 秒；播放中节流调用 `setPositionState`。浏览器不支持时跳过。

## 6. 前端：界面

- **外壳**：队列非空时显示底部播放栏（桌面高 68px，横跨侧栏与主内容之下，侧栏与主内容的高度相应减少）；队列为空时不留空行。
  `<768px` 时播放栏固定在底部，只放封面、曲名、播放/暂停、下一首和“队列”入口，主内容底部留白按实际高度（`ResizeObserver` 写入 CSS 变量）加安全区计算。
- **导航**：按原型分为“聆听”（概览、专辑、正在播放）与“资料库”（扫描目录、成员）；新增 `/now-playing`。
- **播放栏**：封面 44px、曲名与“艺人 — 专辑”（专辑链接到 `/albums/$groupId?release=`）；上一首、播放/暂停（32px 圆形）、下一首；
  进度条与“已播/总长”；格式标签（如“FLAC 16/44.1”，链接到正在播放页的信号路径）；模式按钮（名称写明当前模式）；静音与音量；“队列（N）”链接到正在播放页的队列段。
  所有控件是带可访问名称的 `button`/`input type=range`，进度条的 `aria-valuetext` 为“1:23 / 4:56”。
- **正在播放页** `/now-playing`：封面（`minmax(200px, min(400px, 42%))`，窄屏单列）、“正在播放/已暂停 · 第 X 首 / 共 Y 首”、
  曲名与专辑链接、完整控制（窄屏的拖动、模式、音量在这里）、信号路径、队列（当前项标注，逐项“播放此项 / 上移 / 下移 / 移除”，“清空队列”需确认）。
  队列为空时显示空态与“去专辑”链接。
- **信号路径**：源文件（容器与编码、位深、采样率、声道，缺失写“未知”）→ 服务端（原文件直出，字节不改动、不转码）→
  浏览器（当前解码与播放状态；说明浏览器可能按输出设备重新采样）→ 系统输出（未知：网页无法得知声卡的实际格式与音量链路）。不出现“Bit-perfect”。
- **专辑页**：头部增加“播放”“加入队列”；每行增加“从这首开始播放”按钮与“更多”菜单（下一首播放、加入队列）；
  不可播放的行禁用播放按钮并显示原因；当前曲目在行内以文字和图标标注真实状态（正在播放、已暂停、正在加载、正在缓冲），
  暂停时也保留标注。专辑卡片不加播放按钮（M1b 已裁决）。
- **共享组件**：新增 `shared/ui` 的 `MenuButton`（按 WAI-ARIA 菜单按钮模式：Enter/空格/下箭头打开，方向键移动，Esc 关闭并把焦点还给按钮）。
  不用 Popover API（ADR 0001 的最低浏览器版本不全支持）。

## 7. 测试

### 后端（`go test -race` 与 PostgreSQL 集成）

- 单元：格式表与文件头识别、ETag 稳定与变化、Range 解析（单范围、`N-`、`-N`、多范围、语法错误、越界）、
  `recheckingReader`（未到间隔不复核、复核失败与关停都中止、`Seek` 透传）。
- 集成（`TestPostgreSQL…`，临时根目录加数据库行）：成功；曲目不存在；根目录停用；CUE 虚拟轨；不在格式表；文件缺失；链接逃逸；
  FIFO（不阻塞）；大小或修改时间不符；容器标识不符；根目录不再被允许。
- HTTP 集成：未登录不返回长度或 `Content-Range`；200 全量的全部响应头；206；后缀范围；`If-Range` 命中与不命中；多范围与越界的 416 JSON；
  HEAD 无正文；预检 200 与各 409；格式表；传输中撤销会话、停用目录、关停时连接在测试间隔内被关闭（慢速读取的客户端）。
- 接口样本（`backend/internal/app/testdata/api-samples/`）：`playback/formats.json`、`tracks/playback.json`，以及
  `errors/` 下的 `root-disabled.json`、`not-directly-playable.json`、`source-unavailable.json`、`source-changed.json`、
  `not-found-track.json`、`range-not-satisfiable.json`；前端 decoder 测试读取它们。

### 前端

- Vitest：decoder（读样本）；队列与四种模式的全部语义；能力判定（格式表、`canPlayType`、`decodingInfo` 缺失或拒绝）；
  恢复（版本、损坏、存储不可用、按账号命名、会话结束清除）；引擎（假 `HTMLAudioElement` 驱动事件：预检成功与各种失败、媒体错误的再预检分类、
  网络错误后重试、`401`、自然结束按模式前进、自动播放被拒、恢复后首播定位）；Media Session；组件（播放栏各状态、长标题、未知时长、
  不可播放原因、专辑页动作、队列键盘操作、正在播放空态、信号路径不含“Bit-perfect”）；外壳有无播放栏的布局；导航分组；停用目录文案。
- Playwright（现有路由模拟）：五种宽度下播放栏可见时无横向溢出、不遮挡主内容最后一行；正在播放页与带播放动作的专辑页通过 axe；
  键盘完成“播放 → 下一首播放 → 队列上移/移除”；音频请求用路由返回合成 FLAC。
- **真实服务自动化**（K2，新增）：`scripts/playback-e2e.sh` 启动 `roomusic-test` PostgreSQL，构建前端与 Go，用合成样本组成临时音乐根
  （含一个整轨镜像加多轨 CUE 的虚拟轨专辑），启动服务后运行 `frontend/playwright.real.config.ts`：扫描 → 播放 FLAC（16/44.1、24/48）→
  按真实时间推进 → 拖动 → 结束后前进；MP3、WAV、AAC、Vorbis、Opus 可播放；ALAC 在 Chromium 中标为不可播放；CUE 虚拟轨标注；
  刷新后暂停恢复；另一个标签页退出后本页停止并清空；管理员在播放中撤销该用户会话后传输在复核间隔内被切断；停用目录后下一首显示“所在目录已停用”；
  修改文件后显示“需要重新扫描”。CI 新增作业运行它。
- **测试素材**：用固定摘要的 FFmpeg 容器一次性生成 0.5–3 秒的立体声正弦音（FLAC 16/44.1 与 24/48、ALAC 16/44.1 与 24/48、MP3、AAC-LC、
  WAV 16 与 24 bit、Ogg Vorbis、Opus，均带专辑/曲名/音轨号标签），提交到 `backend/testdata/playback/`（合计不超过 1 MB，实际约 0.7 MB；
  `README.md` 逐个说明），生成命令与镜像摘要写在 `scripts/gen-playback-fixtures.sh`（输出带 bitexact，重复生成字节相同）。
  24 bit WAV 经扫描是 `wav_extensible`，作为“不在格式表中”的反例留在真实服务自动化里。
  FFmpeg 只用于生成素材，不进入运行时、部署或 CI。
- **手工实播**（K2）：三组（macOS Safari、Windows Chrome 或 Edge、任一系统 Firefox）按 `research/manual-acceptance.md` 的清单执行，
  记录操作系统与浏览器版本、格式参数、播放与拖动结果。

## 8. 取舍

- **预检加音频两个接口**，而不是只给音频地址：媒体元素读不到错误正文，预检能在出声前给出确切原因，失败后也能再次预检分类（ADR 0004 §5）；
  M2b 可在预检里加入派生表示与等待状态。代价是每首多一次很小的请求。
- **格式表由后端下发**，而不是前端写死映射：批准直出的范围只有一份，后端同时据此拒绝。
- **严格的版本核对**（第 3 节）优先保证“播放的就是列表里那首”；代价是改标签后要重扫才能播。
- **sessionStorage 恢复**只承诺刷新恢复（K1），不跨标签页、不跨浏览器会话，避免多个标签页互相覆盖与长期留存；需要更长的保留时再议。
- **单个音频元素**，不预加载下一首，不承诺无缝衔接。多个标签页各自独立播放，不做跨标签页协调。
- **复核用周期轮询**而不是进程内即时撤销（K3 选择），实现简单、对多实例也成立，上界明确。

## 9. 兼容与回退

开发阶段不做兼容层。没有迁移；后端接口是新增的，前端改动集中在新 feature、外壳与专辑页插槽。
按提交逐步回退即可；回退前端时需要同时回退内嵌产物 `backend/cmd/roomusic/web`。

## 10. 续接实现的公开入口记录（2026-10-05）

用户在续接时确认：`features/player/index.ts` 公开 provider、离散/进度订阅、能力查询、曲目转换与队列合同。
条目字段为 `entryId,track`，`playTracks` 返回 `added|queue_full`，超限拒绝而不静默截断。
`endSession` 同步清理，普通卸载 `detach` 保留刷新恢复；生成条目 ID 复用 `newIdempotencyKey`，兼容局域网非安全 HTTP。
全样本覆盖清单补齐两项成功/六项播放错误样本，防止仅局部 decoder 测试通过而遗漏跨层门禁。
第二轮审查修复另公开 `usePlayerEngine`（只发命令）、`usePlayerSelector`（只订阅选出的基本值）与 `useCurrentTrackMarker`
（专辑页当前曲目标记），大列表不订阅整份快照；问题与修复见[审查修复记录](research/review-fixes.md)。

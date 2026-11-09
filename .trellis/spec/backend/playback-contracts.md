# 播放格式表与旧直出接口合同

## 0. 现状（播放区第一批之后）

- 播放改由服务端播放区统一处理：客户端只取服务端渲染流，协议见[播放区协议](./playback-zone-contracts.md)。
- **旧直出接口只保留到下一版**：`GET /api/v1/tracks/{id}/playback`、`GET|HEAD /api/v1/tracks/{id}/audio` 与 `POST /api/v1/me/plays`
  （[播放历史合同](./listening-contracts.md)）只为升级时仍开着的旧标签页保留，新前端不再调用；下一版连同本文 §2–§7 的对应合同一起删除。
  旧接口只认直出表的六项，原来经派生交付的格式（ALAC、APE、WavPack、AIFF 等）一律 `409 not_directly_playable`。
- **派生缓存已退役**：`playback/derivation`、预检的 202 等待与 `derivation_*` 错误码已删除，`ROOMUSIC_MEDIA_CACHE_MAX_MIB` 不再读取（仍设置也只是被忽略）。
  启动时若 `<ROOMUSIC_DATA_DIR>/media-cache` 存在就整个删除一次（先核对它与音乐根互不包含），日志 `playback.derivation.cache_removed{bytes}`。
  配置与清理细节见[服务端渲染合同](./playback-render-contracts.md) §11。
- **`GET /api/v1/playback/formats` 现在等于可渲染格式表**：服务端渲染能解码的（来源类别、编码）组合，包括直出六项、原派生各项、浮点 WAV、`wav_extensible` 与 `cue_virtual`；
  只发布 ffmpeg 自检有对应解码器的项，渲染器不可用时为空表。字段形状不变：直出六项仍是 `delivery: original` 与原 MIME，其余是 `delivery: derived`、`audio/flac`、`codecs: null`
  （含义变为“服务端处理后交付”，使旧标签页解码不出错）。新前端只用它判断“能不能放”，与设备无关。表的构成见[服务端渲染合同](./playback-render-contracts.md)。

以下 §1–§7 是旧直出接口（M2a）的合同，安全打开与停机中止的规则仍被渲染来源复用。

## 1. 范围与触发

M2a 在既有同源会话与只读音乐源之上新增播放格式表、预检与音频直出。核心包
`backend/internal/playback` 消费 catalog、library 的公开合同，不含 HTTP、私有表查询或对音乐目录的写入。
`playback/httpapi` 负责鉴权、输入解码、错误映射与流式响应；`app` 装配生命周期与日志。
设计与验证边界见 [M2a 任务](../../tasks/archive/2026-10/10-03-classical-m2a-direct-playback/prd.md)及
[ADR 0004](../../../docs/architecture/adr/0004-media-delivery.md)。三组手工实播未完成前不能宣称整个 M2a 已验收。


## 2. 签名

- `GET /api/v1/playback/formats`：可渲染格式表（§0）。
- `GET /api/v1/tracks/{id}/playback`：预检，安全打开后立即关闭句柄，只返回 200 直出表示（下一版删除）。
- `GET|HEAD /api/v1/tracks/{id}/audio`：直出原字节，支持单范围（下一版删除）。
- `catalog.Store.LoadTrackSource(ctx, trackID)`：公开曲目来源合同；非 `present` 与不存在统一为 `catalog.ErrTrackNotFound`。
- `library.LoadRootLocation(ctx, querier, rootID)`：返回登记根目录与启用状态。
- `playback.NewService(database, allowedRoots, options...)`：`WithRenderDecoders(...)` 装配已自检的解码器清单，启用 `RenderFormats` 与 `OpenForRender`；不传时格式表为空。
- `playback.Service.Prepare(ctx, trackID)`：旧接口入口，只查直出表，返回 `Prepared{TrackID, Format, File, Size, ETag}`，调用方负责关闭文件。
- `playback.NewLifecycleReader(file io.ReadSeeker, lifecycle context.Context) *playback.LifecycleReader`：可定位读取器，
  只因应用生命周期结束而中止读取；`Aborted() bool` 供 handler 在响应结束后判断是否记停机日志。

## 3. 请求、响应与资源合同

所有接口先执行 `Guard.Authenticate`，包括 HEAD、Range 与条件请求；仅同源 HttpOnly Cookie，不引入 URL token。
格式表 JSON 为 `{ "items": [{ "source_kind": string, "codec": string, "delivery": "original"|"derived", "mime_type": string, "codecs": string|null }] }`（样本 `api-samples/playback/formats.json`）。
预检 200 为 `{ "track_id": UUID, "representation": { "kind": "original", "url": 同源音频路径,
"mime_type": string, "codecs": string|null, "size_bytes": integer } }`。

直出格式表：FLAC、MP3、16 bit PCM WAV、AAC-LC/MP4、Ogg Vorbis、Ogg Opus，都经真实 Chromium 解码验证，不以解析器能识别代替；
旧接口对其他组合（含 `m4a/alac`、`cue_virtual`、`wav_extensible`、浮点 WAV 与未知组合）一律拒绝。

安全打开顺序：

1. 曲目可见且来源 `present`；读取登记 root，先检查启用状态与格式白名单。
2. `library.ValidatePath` 核对登记目录仍被配置允许且规范路径未变。
3. `os.OpenRoot` 后以 `O_RDONLY|O_NONBLOCK` 打开相对路径；对已打开的句柄 `Stat`，必须是普通文件。
4. 大小与修改时间（截到微秒）严格等于扫描记录，任一记录缺失也拒绝；再用 `ReadAt` 核对容器头。
   MP4 允许 `ftyp` 前有其他顶层 atom（最多检查 8 个）；APE 是可选 ID3v2 前缀后的 `MAC `（标签头与标签体合计最多 16 MiB，含页脚标志时再跳过 10 字节），
   WavPack 偏移 0 为 `wvpk`，AIFF 为 `FORM` 加 `AIFF`/`AIFC`；不凭扩展名判断类型。
5. 原文件始终只读，句柄响应结束关闭。严格核对不是防止保留大小/mtime 的恶意原地改写的完整内容快照证明。

音频成功响应：`Cache-Control: private, no-store`、`X-Content-Type-Options: nosniff`、已验证的
`Content-Type`、`Accept-Ranges: bytes`、强 ETag，无 `Last-Modified`。ETag 为
`SHA-256(track_id|content_fingerprint|size|mtime_ns)` 前 32 个十六进制字符，经实体标签包装。
`http.ServeContent` 在全部核对后调用，修改时间传零值。

Range 仅接受单个 `bytes=N-M`、`bytes=N-` 或 `bytes=-N`；畸形、多范围、不可满足统一 JSON 416，
带 `Content-Range: bytes */<大小>`。先判断 If-Range：只有当前强 ETag 逐字命中才保留范围，
不匹配、弱标签或日期使 Range 被忽略并返回完整表示，不应先拒绝其 Range。

2026-11-03 C1 按 R3 删除在途周期复核：每个新请求仍鉴权并核对来源；已经建立的流不再查询会话、成员或目录状态。
应用生命周期结束时，读取器返回上下文错误且不交付该次字节；`ServeContent` 停止写出，响应长度不足使 `net/http` 关闭连接。
读取前后都检查生命周期，不强制打断底层阻塞读取；底层返回后若已取消则丢弃该次字节。
头已经发出后只切断连接，绝不在音频尾部追加 JSON。
每次停机中止只记录一条 `playback.stream.aborted`，字段为安全的曲目 ID、请求 ID 与 `reason=shutdown`，不含路径。
客户端先断开不记录停机中止日志。

根停用同时禁止新的播放请求，但已入库专辑仍可浏览。停用成员、撤销会话或停用目录后，已建立传输不因此切断，
后续预检与音频请求按原错误合同拒绝。浏览器已缓存的音频无法收回；前端本地会话结束仍须立即停播并清空。

## 4. 验证与错误矩阵

检查优先级：鉴权 → UUID → 曲目可见 → root → 格式 → 文件核对 → Range。
错误信封与 `no-store` 沿用 [平台运行合同](./core0-runtime-contracts.md)。

| 情况 | 状态与错误码 | 用户结果 |
| --- | --- | --- |
| 未登录/已撤销/成员停用 | 401 `unauthorized` | 不泄露长度、格式或 `Content-Range` |
| 非 UUID | 400 `invalid_id` | 曲目标识无效 |
| 不存在或非 present | 404 `not_found` | 曲目不存在 |
| root disabled | 409 `root_disabled` | 目录已停用 |
| 不在直出表（含 ALAC 等原派生格式与 CUE 虚拟轨） | 409 `not_directly_playable` | 不能直接播放 |
| 根/文件缺失、链接逃逸、FIFO、非普通文件、根不被允许 | 409 `source_unavailable` | 文件无法读取，不泄露路径 |
| 大小/mtime/容器变化或缺少扫描版本 | 409 `source_changed` | 提示重新扫描 |
| Range 无效/多范围/越界 | 416 `range_not_satisfiable` | JSON 错误，不生成 multipart |
| 数据库核对失败 | 503 `database_unavailable` | 安全失败，不当成文件问题 |

## 5. 正常、边界与失败案例

- 正常：登录后预检 FLAC 成功，200 原字节、206 子范围与后缀范围一致；HEAD 无正文。
- 边界：过期 If-Range 配畸形 Range 仍按完整表示 200；ALAC、24 bit extensible WAV 与 CUE 虚拟轨在旧接口上明确不可直出（它们只经渲染流交付）。
- 失败：停用目录后新预检与音频请求拒绝，已建立的长连接不因此切断；更新标签但未重扫提示 `source_changed`。
- 缓冲：短素材通常早已下载完成，管理员撤销后音频可能仍在浏览器缓冲中；不能声称即时收回。

## 6. 必需测试与断言

- `go test -race ./internal/playback/... ./internal/app/... -run 'Test(CheckRange|Lifecycle|PackageDependency)'`：
  范围、生命周期取消与依赖方向。格式/容器、ETag、安全打开与素材测试同样在能力包内。
- 配置隔离的 `ROOMUSIC_TEST_DATABASE_URL` 后运行
  `go test -race ./internal/playback/... ./internal/app/... -run 'TestPostgreSQLPlayback' -count=1`：
  查找顺序、来源变化、链接逃逸/FIFO、HTTP 头、Range、预检、新请求拒绝及真实连接停机中止。
- `TestLifecycleReaderAbortsAndStaysAbortedOnShutdown` 与 `TestLifecycleReaderDiscardsBytesWhenShutdownOccursDuringRead`
  证明取消后读取返回错误、不再交付字节；`TestPostgreSQLPlaybackRealConnectionIsClosedOnShutdown` 验证真实连接提前结束与唯一的
  `reason=shutdown` 日志；`TestPostgreSQLPlaybackStreamDoesNotLogAClientDisconnectAsAServerAbort` 验证客户端先断开不记录。
- 保留停用成员、撤销会话后新预检与音频请求 401，以及停用目录后 409 `root_disabled` 的用例；删除在途撤销切断用例，不跳过。
- `TestPostgreSQLAPIResponseSamples` 守住格式表与预检样本；前端 decoder 读取同一批样本。
- 浏览器端到端只走播放区（`./scripts/playback-e2e.sh`，见[播放区协议](./playback-zone-contracts.md)），旧直出路径不再有浏览器用例。
- 三组手工实播另记录 OS/浏览器版本与逐格式结果，不用“预期可播”充当通过。

## 7. 错误与正确做法

错误：先按文件路径读取并计算长度，再检查 Cookie；让 `ServeContent` 默认生成多范围或纯文本错误；
把新请求鉴权扩展成在途周期复核，或把客户端断开误记为停机。

正确：以曲目 ID 查当前权限与来源，安全打开后对句柄核对，应用先分类单范围错误，
分别验证真实浏览器解码、新请求拒绝与已建立连接的停机中止。源文件、私有路径、凭据不进入前端持久化或普通日志。

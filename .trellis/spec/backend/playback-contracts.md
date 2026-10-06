# M2a 鉴权直出与播放合同

## 1. 范围与触发

M2a 在既有同源会话与只读音乐源之上新增播放格式表、预检与音频直出。核心包
`backend/internal/playback` 消费 catalog、library 的公开合同，不含 HTTP、私有表查询、转码或文件写入。
`playback/httpapi` 负责鉴权、输入解码、错误映射与流式响应；`app` 装配生命周期与日志。
设计与验证边界见 [M2a 任务](../../tasks/10-03-classical-m2a-direct-playback/prd.md)及
[ADR 0004](../../../docs/architecture/adr/0004-media-delivery.md)。三组手工实播未完成前不能宣称整个 M2a 已验收。

## 2. 签名

- `GET /api/v1/playback/formats`：当前服务端直出白名单。
- `GET /api/v1/tracks/{id}/playback`：预检，安全打开后立即关闭句柄。
- `GET|HEAD /api/v1/tracks/{id}/audio`：原字节输出，支持单范围。
- `catalog.Store.LoadTrackSource(ctx, trackID)`：公开曲目来源合同；非 `present` 与不存在统一为 `catalog.ErrTrackNotFound`。
- `library.LoadRootLocation(ctx, querier, rootID)`：返回登记根目录与启用状态。
- `playback.Service.Prepare(ctx, trackID)`：返回 `Prepared{TrackID, RootID, Format, File, Size, ETag}`，调用方负责关闭文件。
- `playback.Service.StillPlayable(ctx, trackID, rootID)`：复核来源仍 `present`、仍属于原 root，root 仍启用。
- `playback.NewRecheckingReader(...)`：可定位读取器，生产默认每 10 秒在下一次读取前复核。

## 3. 请求、响应与资源合同

所有接口先执行 `Guard.Authenticate`，包括 HEAD、Range 与条件请求；仅同源 HttpOnly Cookie，不引入 URL token。
格式表 JSON 为 `{ "items": [{ "source_kind": string, "codec": string, "mime_type": string, "codecs": string|null }] }`。
预检为 `{ "track_id": UUID, "representation": { "kind": "original", "url": 同源音频路径,
"mime_type": string, "codecs": string|null, "size_bytes": integer } }`。
前端只使用已解码的返回路径，不根据文件名或来源路径构造 URL。

直出格式表：FLAC、MP3、16 bit PCM WAV、AAC-LC/MP4、Ogg Vorbis、Ogg Opus，都经真实 Chromium 解码验证，不以解析器能识别代替；
`m4a/alac`、`cue_virtual`、`wav_extensible`、浮点 WAV 与未知组合拒绝。ALAC 只有 Safari 能解码，
macOS Safari 手工实播通过后才加入格式表（M2a 收尾时尚无证据，2026-10-06 移出，见 ADR 0004 决策记录）。
浏览器能力探测不能保证某个具体文件可播，运行时仍可报解码失败。

安全打开顺序：

1. 曲目可见且来源 `present`；读取登记 root，先检查启用状态与格式白名单。
2. `library.ValidatePath` 核对登记目录仍被配置允许且规范路径未变。
3. `os.OpenRoot` 后以 `O_RDONLY|O_NONBLOCK` 打开相对路径；对已打开的句柄 `Stat`，必须是普通文件。
4. 大小与修改时间（截到微秒）严格等于扫描记录，任一记录缺失也拒绝；再用 `ReadAt` 核对容器头。
   MP4 允许 `ftyp` 前有其他顶层 atom（最多检查 8 个）；不凭扩展名判断类型。
5. 原文件始终只读，句柄响应结束关闭。严格核对不是防止保留大小/mtime 的恶意原地改写的完整内容快照证明。

音频成功响应：`Cache-Control: private, no-store`、`X-Content-Type-Options: nosniff`、已验证的
`Content-Type`、`Accept-Ranges: bytes`、强 ETag，无 `Last-Modified`。ETag 为
`SHA-256(track_id|content_fingerprint|size|mtime_ns)` 前 32 个十六进制字符，经实体标签包装。
`http.ServeContent` 在全部核对后调用，修改时间传零值。

Range 仅接受单个 `bytes=N-M`、`bytes=N-` 或 `bytes=-N`；畸形、多范围、不可满足统一 JSON 416，
带 `Content-Range: bytes */<大小>`。先判断 If-Range：只有当前强 ETag 逐字命中才保留范围，
不匹配、弱标签或日期使 Range 被忽略并返回完整表示，不应先拒绝其 Range。

已经建立的流在读取前每 10 秒同步复核会话/成员与 `StillPlayable`；任一失败（含数据库错误）即关闭。
应用生命周期结束也中止。头已经发出后只切断连接，绝不在音频尾部追加 JSON。
记录 `playback.stream.aborted`，字段为安全的曲目 ID、请求 ID 与
`session_invalid|track_unavailable|root_disabled|check_failed|shutdown`，不含路径。
客户端主动断连不记录成服务端撤销。

根停用同时禁止播放，但已入库专辑仍可浏览。新请求立即拒绝；在途传输复核上界不包含网络缓冲，
浏览器已缓存的音频无法收回。前端本地会话结束须立即停播并清空，不等待服务端 10 秒复核。

## 4. 验证与错误矩阵

检查优先级：鉴权 → UUID → 曲目可见 → root → 格式 → 文件核对 → Range。
错误信封与 `no-store` 沿用 [平台运行合同](./core0-runtime-contracts.md)。

| 情况 | 状态与错误码 | 用户结果 |
| --- | --- | --- |
| 未登录/已撤销/成员停用 | 401 `unauthorized` | 不泄露长度、格式或 `Content-Range` |
| 非 UUID | 400 `invalid_id` | 曲目标识无效 |
| 不存在或非 present | 404 `not_found` | 曲目不存在 |
| root disabled | 409 `root_disabled` | 目录已停用 |
| CUE 虚拟轨或非白名单 | 409 `not_directly_playable` | 不能直接播放 |
| 根/文件缺失、链接逃逸、FIFO、非普通文件、根不被允许 | 409 `source_unavailable` | 文件无法读取，不泄露路径 |
| 大小/mtime/容器变化或缺少扫描版本 | 409 `source_changed` | 提示重新扫描 |
| Range 无效/多范围/越界 | 416 `range_not_satisfiable` | JSON 错误，不生成 multipart |
| 数据库核对失败 | 503 `database_unavailable` | 安全失败，不当成文件问题 |

## 5. 正常、边界与失败案例

- 正常：登录后预检 FLAC 成功，200 原字节、206 子范围与后缀范围一致；HEAD 无正文。
- 边界：过期 If-Range 配畸形 Range 仍按完整表示 200；24 bit extensible WAV 与 CUE 虚拟轨明确不可直出。
- 失败：停用目录后预检拒绝，已建立的长连接按复核切断；更新标签但未重扫提示 `source_changed`。
- 缓冲：短素材通常早已下载完成，管理员撤销后音频可能仍在浏览器缓冲中；不能据此断言复核失效或声称即时收回。

## 6. 必需测试与断言

- `go test -race ./internal/playback/... ./internal/app/... -run 'Test(CheckRange|Rechecking|PackageDependency)'`：
  范围、复核、关停与依赖方向。格式/容器、ETag、安全打开与素材测试同样在能力包内。
- 配置隔离的 `ROOMUSIC_TEST_DATABASE_URL` 后运行
  `go test -race ./internal/playback/... ./internal/app/... -run 'TestPostgreSQLPlayback' -count=1`：
  查找顺序、来源变化、链接逃逸/FIFO、HTTP 头、Range、预检及真实慢速连接撤销。
- `TestPostgreSQLPlaybackRealConnectionIsClosedAfterRevocation` 与流式读取测试是长连接撤销的证据，
  短素材浏览器测试不能替代它们。
- `TestPostgreSQLAPIResponseSamples` 守住接口样本；前端 decoder 读取同一批样本。
- `./scripts/playback-e2e.sh`：临时合成媒体、真实 Go 与 Chromium 解码/定位/恢复；不使用真实 `music/`。
- 三组手工实播另记录 OS/浏览器版本与逐格式结果，不用“预期可播”充当通过。

## 7. 错误与正确做法

错误：先按文件路径读取并计算长度，再检查 Cookie；让 `ServeContent` 默认生成多范围或纯文本错误；
用短音频的暂停状态证明服务器撤销长连接。

正确：以曲目 ID 查当前权限与来源，安全打开后对句柄核对，应用先分类单范围错误，
分别记录真实浏览器解码与慢速连接的撤销断言。源文件、私有路径、凭据不进入前端持久化或普通日志。

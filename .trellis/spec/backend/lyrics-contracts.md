# 歌词合同（内嵌标签与外置 .lrc）

## 1. 范围与触发

扫描时从音频标签或同名外置 `.lrc` 文件提取歌词原文，保存在曲目记录里；接口读取时把原文解析成可显示的行；前端只做分组、定位与显示。
修改歌词的提取规则、`tracks.lyrics` 两列、歌词接口、LRC 解析规则或前端歌词面板的数据合同时，先读本文件并同步更新。
需求与取舍见歌词任务（本机归档 `.trellis/tasks/archive/2026-10/10-09-classical-m2b-lyrics/prd.md`）与它的技术设计（本机归档 `.trellis/tasks/archive/2026-10/10-09-classical-m2b-lyrics/design.md`）；
前端的面板与布局规则见[播放器设计规范](../frontend/player-design-guidelines.md)。

来源：音频内嵌标签，以及与音频同目录、同主名的外置 `.lrc`（2026-11-15 起，外置优先，见第 3a 节）。不联网、不提供编辑
（M3 的修正覆盖层只覆盖发行的八个字段，不含歌词）、不做逐字高亮、不读 ID3 `SYLT`、不读 `.txt` 等其他歌词文件。
音乐文件与歌词文件始终只读，歌词不进入任何派生文件。外置歌词的需求与取舍见本机归档 `.trellis/tasks/archive/2026-11/11-15-external-lrc/`。

## 2. 签名

- `parser.AudioObservation.Lyrics`、`LyricsSource`：规范化后的原文与来源键；没有有效歌词时两者都为空。解析器不解析 LRC。
- `metadata.NormalizeLyrics(raw) (string, bool)`；常量 `metadata.MaxLyricsBytes = 64 KiB`（在 `catalog/metadata`：解析器与自动整理共用）；来源键常量 `parser.LyricsSource*`（含外置文件 `LyricsSourceLRCFile = "lrc_file"`）。
- `parser.DecodeExternalLyrics(data) (string, bool)`、常量 `MaxExternalLyricsBytes = 256 KiB`；`parser.ApplyExternalLyrics(observation, text)` 把外置歌词与随之抽取的署名应用到观察副本。
- `library/sidecar_lyrics.go`：每个目录建一次 `.lrc` 文件名索引，按第 3a 节匹配并有界读取。
- `ingest.SourceObservation.Lyrics`、`LyricsSource`（JSON `lyrics`、`lyrics_source`，`omitempty`），随暂存观察进入整理与曲目写入。
- `tracks.lyrics`、`tracks.lyrics_source`（迁移 `0015_track_lyrics.sql`，`0050_track_lyrics_file.sql` 增加来源键 `lrc_file`）。
- `catalog.ParseLyrics(raw) (Lyrics, bool)`：纯函数；`catalog.Store.LoadTrackLyrics(ctx, trackID) (TrackLyrics, error)`，曲目不存在或非 `present` 为 `catalog.ErrTrackNotFound`。
- `GET /api/v1/tracks/{id}/lyrics`，注册在 `catalog/httpapi`。

## 3. 提取规则（解析器）

| 来源 | 规则 | `LyricsSource` |
| --- | --- | --- |
| Vorbis 注释（FLAC、Ogg Vorbis、Opus） | 键不区分大小写；`LYRICS` 优先于 `UNSYNCEDLYRICS` 与 `UNSYNCED LYRICS`；同一类键取第一个有效值，无效的被跳过（空白、非法 UTF-8、超限） | `vorbis_lyrics`、`vorbis_unsyncedlyrics` |
| MP4 | `©lyr` 里第一个类型为 1（UTF-8）的 `data`；其他类型忽略；不进入 `RawAtoms` | `mp4_lyr` |
| ID3v2.3 与 v2.4（MP3） | `USLT` 帧：编码字节 0 ISO-8859-1、1 带 BOM 的 UTF-16（缺 BOM 按小端）、2 UTF-16BE、3 UTF-8，跳过 3 字节语言与内容描述；多个帧时取内容描述为空的帧（仅有 BOM 也算空），否则取第一个有效帧 | `id3_uslt` |

规范化：去掉 UTF-8 BOM；`\r\n`、`\r` 改为 `\n`；删除 NUL 与除 `\n`、`\t` 外的 C0 控制字符；去掉首尾空白。结果为空、不是合法 UTF-8 或超过
`MaxLyricsBytes`（65,536 字节，按规范化后计）时不提取，也不记诊断——歌词缺失不影响曲目可用性。USLT 的歌词文本解码前另有 256 KiB 的上限，
避免异常大的帧分配数倍于它的临时内存。

ID3 帧的读取按版本进行（歌词是第一个会读到很长帧的字段，所以不能沿用“大小一律按普通整数”）：

- 帧大小：v2.3 是普通大端整数；v2.4 规定为 synchsafe 整数。有些写入器在 v2.4 里仍写普通整数，所以 v2.4 的大小两种读法都可能成立时，
  取“下一个帧头位置有效”（标签体末尾、填充的开头，或由大写字母与数字组成的帧标识）的那种，都无效时按规范取 synchsafe；有字节不小于 0x80 的只能是普通整数。
- 同步消除：v2.3 的标签头标志对整个标签体生效（含帧头），先整体还原；v2.4 看每个帧自己的标志。USLT 另按帧格式标志跳过分组标识与数据长度指示，
  压缩或加密的帧无法解读，跳过。
- v2.2（`ULT`）不读取：解析器目前不读 v2.2 的任何帧（帧头是 6 字节），歌词不单独例外。
- Ogg 与 Opus 逐页流式拼包，注释包可以跨页，单包上限 16 MiB（见[扫描流程合同](./library-scan-pipeline.md)）；Vorbis 注释单条上限 1 MiB。

CUE 虚拟轨没有歌词：它的观察由 CUE 事实构造，不复制父文件的标签，整张镜像里的歌词对应整个文件，不能分给单条曲目；整轨镜像旁的 `.lrc` 同样不进入虚拟轨。
逐轨抄本（物理文件加只含一个轨的 CUE）的曲目仍是物理轨，`mergeSingleCueEvidence` 合并 CUE 字段时保留它的歌词（内嵌或按物理音频文件名匹配的外置歌词）。

## 3a. 外置 `.lrc`（扫描器）

- 只在物理音频分支查找（`prepareCueParent` 不查）。主名是音频文件名去掉最后一个扩展名（`a.b.flac` → `a.b`）。在音频所在目录：
  1. 有名字恰为 `主名.lrc` 的项就用它（它不可用时不再尝试其他变体）；
  2. 否则主名经 NFC 后不区分大小写相等、扩展名不区分大小写为 `.lrc` 的项恰好一个时用它；零个或多个时没有外置歌词。
  3. 套用扫描过滤（隐藏文件不取）；`Lstat` 只接受普通文件，拒绝符号链接与目录。不支持 `Song.flac.lrc`、按标题匹配或子目录中的歌词。
- 读取：大小为 0 或超过 256 KiB 放弃（不截断）。解码：UTF-32 BOM 放弃；UTF-8 BOM 去掉后须为合法 UTF-8；UTF-16LE/BE BOM 解码并校验代理对；
  无 BOM 的合法 UTF-8 直接用；否则按 GB18030 解码，结果含 `U+FFFD` 放弃。不做 Big5、Shift-JIS 探测。解码结果再经 `NormalizeLyrics`。
- 任何放弃都静默回落到解析器给出的内嵌歌词，不记诊断，不影响曲目可用性。外置生效时 `LyricsSource = lrc_file`。
- 外置歌词不进解析缓存：每轮扫描（含缓存命中）都读取，在 `scanPipeline.apply` 写入缓存之后、构造暂存观察的副本上覆盖；缓存里仍是解析器原始输出。
  不升 `parser.CommonVersion`、`ingest.GroupingVersion`。只增删改 `.lrc` 时由候选签名察觉（见[增量扫描合同](./library-scan-incremental.md)），音频不重新解析。
- 歌词署名随生效的歌词走：外置生效时去掉从内嵌歌词抽出的 `Source="lyrics"` 署名，改从 `.lrc` 开头抽取；标签署名优先不变；
  `InferredFields["title"]` 为真（标题来自文件名补位）时按空标题处理，不据此跳过题头。

## 4. 保存

迁移 `0015_track_lyrics.sql` 增加两个可空列与约束 `tracks_lyrics_ck`：两列同为 NULL；或 `lyrics` 非空、不超过 65,536 字节，
`lyrics_source` 非空且属于来源键（0015 的四个内嵌来源，0050 起加 `lrc_file`）。约束里 `lyrics_source IS NOT NULL` 必须显式写出：CHECK 在结果为未知（NULL）时放行，
只靠 `IN` 挡不住“有歌词、没有来源”（集成测试覆盖这一格）。迁移只增加列与约束，不改写已有数据。0050 按原结构重建约束；它的下行迁移先把 `lrc_file` 的两列置 NULL（重扫可重建），再恢复四值约束。

`catalog/ingest/persist_track.go` 的 INSERT 与 UPDATE 都写这两列，观察没有歌词时写 NULL，所以重扫会更新或清空歌词；曲目身份、Release 匹配与内容指纹不受影响。
已入库的曲目迁移后没有歌词，重扫一次才会有。暂存的观察 JSON 带歌词（最多 64 KiB，远小于 24 MiB 的暂存上限）。

## 5. 接口

`GET /api/v1/tracks/{id}/lyrics`：所有登录成员可读（不限管理员）。检查顺序：未登录 401 `unauthorized` → 非 UUID 400 `invalid_id` →
曲目不存在或非 `present` 404 `not_found` → 数据库读取失败 503 `database_unavailable` → 200。响应 `Cache-Control: no-store`。
不检查目录是否停用（歌词是已入库的目录事实，与专辑浏览一致），也不触碰文件系统。

```json
{ "track_id": "…", "lyrics": null }
{ "track_id": "…", "lyrics": { "kind": "synced", "source": "embedded", "lines": [ { "start_ms": 12340, "text": "…" } ] } }
{ "track_id": "…", "lyrics": { "kind": "plain", "source": "embedded", "lines": [ { "start_ms": null, "text": "…" } ] } }
{ "track_id": "…", "lyrics": { "kind": "synced", "source": "external", "lines": [ { "start_ms": 1000, "text": "…" } ] } }
```

没有歌词、或去掉元标签后没有可显示的行是 200 加 `lyrics: null`，不是 404。`synced` 的每行 `start_ms` 是 0 到 24 小时之间的整数且非递减；
`plain` 的每行都是 `null`，`text` 可以是空串（分段）。`source` 是 `embedded`（内嵌标签）、`external`（`lyrics_source = lrc_file`，音频旁的同名文件）或 `online`（自动整理从外部源取来的，存在 `track_external_lyrics`）；`ParseLyrics` 不变，来源由 `LoadTrackLyrics` 设置。前端不显示来源。

**本地优先，外部补空缺**（M7 子任务 B2）：本地歌词能解析出可显示的行时用本地的；本地没有、或有但解析不出可显示的行时，用外部的。
外部歌词不写进 `tracks.lyrics`，扫描不清除它；写入、撤销与不再提出见[自动整理合同](./curation-contracts.md)第 14 节。

## 6. 解析规则（`catalog.ParseLyrics`）

1. 按 `\n` 分行。整行匹配 `[字母或#:内容]` 的是元标签，全部去掉；其中 `offset`（键不区分大小写）的整数毫秒值（可带正负号，超过 24 小时视为无效）
   生效，多个 `offset` 取第一个有效的。
2. 行首连续的时间戳 `[mm:ss]`、`[mm:ss.f]`、`[mm:ss.ff]`、`[mm:ss.fff]`、`[mm:ss:ff]`（分钟 1–3 位，秒 1–2 位且小于 60，小数一位是十分之一秒、
   两位是百分之一秒、三位是毫秒）展开成多行。至少有一行有合法时间戳时是 `synced`：文本是去掉时间戳与逐字标记 `<mm:ss.xx>` 后的剩余部分
   （去首尾空白，可为空串，表示间奏）；`start_ms = 时间戳 − offset`，小于 0 取 0，超过 24 小时的行丢弃；没有时间戳的行丢弃；
   按 `start_ms` 稳定排序（同一时间的行保持原顺序，双语对照靠它分组）。
3. 否则是 `plain`：去掉元标签行，行尾去空白（保留行首缩进），去掉首尾空行，连续空行合并为一行；行首的合法时间戳也一并去掉。
   `synced` 过滤后一行不剩时同样按 `plain` 处理。整份没有可显示的行时返回 false。
4. 最多 2,000 行（`synced` 先排序再截），单行最多 1,000 个字符（按字符不是字节截断，不切开多字节字符）。

不规范的时间戳（秒不小于 60、分钟超过 3 位、超过 24 小时）使该行被忽略或整份退为纯文本，不报错。

## 7. 前端的数据合同

- 解码器（`features/player/api/decoders.ts`）在字段形状之外按形态检查：`synced` 每行有开始时间且非递减，`plain` 每行没有；违反则整份拒绝，
  界面不会拿到顺序错乱的“同步歌词”。上限与后端一致（2,000 行、文本 2,000 个 UTF-16 码元、开始时间 24 小时）。
- `fetchTrackLyrics` 要求响应的 `track_id` 与请求一致；404 与 401 是错误，不当成“没有歌词”。
- 查询键 `viewerQueryKey(generation, "player", "lyrics", trackId)`，`staleTime: Infinity`：歌词只在重扫、自动整理补上或撤销时变化，会话结束随代次清空；修改发行数据之后的统一刷新（`useReleaseDataRefresh`）让当前会话的全部歌词查询失效，撤销自动补的歌词后不再显示旧的。
- 分组与定位是纯函数（`engine/lyrics.ts`）：同一 `start_ms` 的相邻行成一组（主行在前，对照行在后），没有文字的组是间奏；
  当前组是最后一个“开始时间不晚于播放位置 + 40 毫秒”的组（容差让点击一行后略早于该行的位置也算轮到它）。

## 8. 必需测试与断言

- 解析器（`go test ./internal/library/parser/`）：`NormalizeLyrics` 表；`DecodeExternalLyrics` 的各编码、BOM、代理对、`U+FFFD`、256 KiB/64 KiB 边界；`ApplyExternalLyrics` 的署名替换、标签优先、补位标题；FLAC、Ogg、Opus、MP4、ID3 的各来源与优先级；USLT 的四种编码、
  描述优先、分组/数据长度/同步消除/压缩/加密标志、v2.4 的 synchsafe 与普通整数写入器、畸形帧；素材事实测试核对真实 ffmpeg 写出的歌词。
- `go test ./internal/library/`：外置歌词匹配（精确优先、大小写与 NFC、歧义、精确项不可用、隐藏、符号链接、`Song.flac.lrc`、超限）。
- `go test ./internal/catalog/`：`ParseLyrics` 表驱动（时间戳各形式、元标签与 offset、逐字标记、间奏、同时间顺序、24 小时、纯文本分段、上限、全部无效）与 JSON 形状。
- 配置隔离的 `ROOMUSIC_TEST_DATABASE_URL` 后运行 `go test ./internal/library/ ./internal/app/ -run 'TestPostgreSQL'`：
  扫描写入、重写标签后的更新与清空、外置优先与各种回落、只增删改 `.lrc` 的增量扫描（`FilesParsed == 0`、曲目 ID 不变、缓存仍为内嵌）、外置署名、CUE 两种场景、`tracks_lyrics_ck` 的全部格（含 `lrc_file`）与 0050 往返；接口的 401、400、404、200 与 `null`；`TestPostgreSQLAPIResponseSamples`
  的同步、纯文本、没有歌词、外置四份样本（前端解码器读取同一批样本）。
- 前端与真实服务的用例见[前端验证分层](../frontend/quality-verification-layers.md)：面板各状态、跟随与暂停跟随、键盘、五种宽度、axe；
  `./scripts/playback-e2e.sh` 经真实扫描核对 FLAC 与 ALAC 素材的歌词、CUE 虚拟轨无歌词，以及真实点击后的播放位置。

## 9. 错误与正确做法

错误：播放时现读音频文件解析歌词；把整张 CUE 镜像的歌词复制给每条虚拟轨；用 `IN` 单独充当“来源必填”的约束；
在前端重新解析 LRC，让两处规则分叉；用 `aria-live` 逐行播报当前行；为放大当前行而改字号。

正确：扫描时提取并保存，接口读取时解析，规则只在后端有一份并由接口样本约束；缺失如实显示“这首曲目没有歌词”，不生成占位歌词。

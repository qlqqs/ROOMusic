# Research: 扫描读取同名外置 .lrc 歌词

- **Query**: 扫描时读取与音频同名的 `.lrc`，外置优先于内嵌；编码 BOM → 合法 UTF-8 → GB18030；回答遍历、附属文件先例、增量判定、缓存、入库链路、接口与前端、CUE、同名匹配八个问题，并给出推荐实现方案
- **Scope**: internal（代码与 spec；外部仅核对 `golang.org/x/text` 解码行为）
- **Date**: 2026-11-15

相关 spec：`.trellis/spec/backend/lyrics-contracts.md`（§1 第 10 行明确“不读外置 `.lrc`”，本任务要改）、`library-scan-incremental.md`（解析缓存、候选签名 `g/k/d/m/s/a/l` 行）、`library-scan-tags.md:39-44`（歌词开头署名）、`library-scan-pipeline.md:19`（`finalizeTags` 顺序）、`database-guidelines.md:170-172`（迁移与回退文件规则）、`frontend/player-design-guidelines.md:51`（“没有内嵌歌词”文案）。

---

## 1. 遍历、扩展名分流与过滤

| 位置 | 事实 |
|---|---|
| `backend/internal/library/scanner.go:348` | `filepath.WalkDir(root.Path, …)` 单协程遍历，按目录项字节序访问；子目录在遇到时递归，所以同一目录的文件可能被子目录递归打断（例如 `A.flac`、`Sub/`、`Z.flac`）。 |
| `scanner.go:371-385` | 目录：`filter.skipDir` 命中则 `fs.SkipDir`；否则 `cache.enterDirectory`。 |
| `scanner.go:388-390` | 文件：`filter.skipFile(name)` 只判断是否以 `.` 开头（`scan_filter.go:39`）。 |
| `scanner.go:391-394` | `extension := strings.ToLower(filepath.Ext(path))`；不是 `.cue`、不是支持格式（`scan_filter.go:41-48`）、也不是候选格式（`scan_filter.go:50-57`）的文件直接 `return nil`——**`.lrc` 目前在这里被静默跳过**，不计数、不保住、不诊断。 |
| `scanner.go:400-405` | 音频/CUE 是符号链接时 `EvalSymlinks` 并要求目标在根内（`isWithin`，`scanner.go:1002-1006`），否则保住为 `broken_or_escaped_link`。 |
| `scanner.go:406-412` | `os.Stat` + `IsRegular`。 |
| `scanner.go:421-428` | 只有支持格式进 `pipeline.prepareAudio`（`scan_pipeline.go:66`），之后 `submit`。 |
| `scan_progress.go:205-207,249` | 预计数用同一组扩展名（`isScanCandidate`）与 `skipFile`，`.lrc` 不计入总数。 |

过滤对同目录 `.lrc` 的影响：
- 排除子目录与内置忽略目录（`scan_filter.go:13-16,26-37`）作用于整个目录；音频所在目录被排除时音频本身也不扫，因此不会出现“音频被扫、同目录 `.lrc` 被排除”的情况。
- 隐藏文件规则：`.Song.lrc` 会被 `skipFile` 判为隐藏。音频 `.Song.flac` 本身也被跳过，所以只需在查找 `.lrc` 时同样套用 `skipFile` 即可保持一致（封面列举也这样做，`scanner.go:1218`）。
- 结论：遍历不会把 `.lrc` 交给任何处理；外置歌词必须由“处理音频文件”的那一步主动去同目录找。由于 `Song.flac` 的字节序在 `Song.lrc` 之前，不能依赖遍历顺序捕获 `.lrc` 目录项，需要单独 `ReadDir`（或直接 `Lstat` 精确名）。

## 2. 最接近的先例：文件夹封面与抓轨日志

两者都**不在遍历阶段**处理，而是在“保存候选”阶段按候选的目录集合重新列举：

- 封面名单与档位唯一入口 `listFolderArtworkCandidates`（`scanner.go:1162-1188`）→ `listDirectoryArtworkCandidates`（`scanner.go:1190-1277`）：每目录一次 `os.ReadDir`；精确名按 `folderArtworkNames`（`scanner.go:1116`）匹配，`folderArtworkCandidates`（`scanner.go:1279-1290`）先取大小写完全一致的名字，再按字节序取小写后相等的变体；套用 `filter.skipFile`；不跟随目录符号链接。
- 实际读取 `readFolderArtworkFile`（`scanner.go:1292-1309`）：`os.Lstat`，**拒绝符号链接**与非普通文件，大小 `0 < size ≤ 16 MiB`。
- 抓轨日志 `discoverCandidateRipLogEvidence`（`scanner.go:844-902`）：目录 `Lstat` 拒绝符号链接、`isWithin` 校验；`.log` 扩展名 `EqualFold`；文件 `Lstat` 拒绝符号链接；内容只读前 `maxRipLogHeaderBytes = 64 KiB`（`scanner.go:702,929-945`）。
- 增量判定：二者都进入候选签名（`candidate_signature.go:59-67` 的 `a` 行、`:68-91` 的 `l` 行），只用 `Lstat` 的大小与微秒 mtime，不读内容；集成测试 `scan_incremental_integration_test.go:296-330` 的“替换封面”“新增EAC日志”断言 `FilesParsed==0`、只重写一个候选；单测 `candidate_signature_test.go:34-111` 覆盖附件大小/mtime 改变、新增、无关文件不影响。

与 `.lrc` 的差别：封面与日志属于**候选（发行）级**事实，签名只需元信息，内容在写入时才读；歌词是**曲目级观察**，`persist_track.go` 每次写曲目都从观察取 `Lyrics`（见第 5 节），候选一旦因任何原因重写，观察里必须已经带着正确的歌词，因此 `.lrc` 内容必须在暂存观察之前读到，不能像封面那样延迟到保存阶段。

## 3. 增量扫描如何判断要重新解析 / 重写

两层判定互相独立（`library-scan-incremental.md` 开头）：

1. **解析缓存**（只决定要不要重新解析音频）：`parseCache.lookupObservation`（`parse_cache.go:118-133`）命中条件是 根 + 规范相对路径、音频 `size`、mtime 截到微秒、`parser.CacheVersion(ext)` 全等。`.lrc` 不参与，改动 `.lrc` 不会使音频重新解析。
2. **候选签名**（决定候选是否跳过写入）：`candidateSignature`（`candidate_signature.go:20-93`）。关键是 `d` 行（`:27`）= `ingest.CandidateMetadataSignature`（`catalog/ingest/candidate_state.go:13-32`），对候选及全部成员观察整体做 JSON SHA-256，只去掉 `Artwork`、`ArtworkDeferred`、`JoinGroupID`——**`Lyrics/LyricsSource/Credits` 都在摘要里**（spec 也写明）。之后 `CandidateUnchanged`（`candidate_state.go:37-`）还要求曲目沿用等条件。

结论：
- 只要外置歌词在**每轮扫描**（包括解析缓存命中时）都被读入并写进暂存观察，`.lrc` 的新增、修改、删除都会改变观察的 `Lyrics`/`LyricsSource`（以及歌词署名），从而改变 `d` 行，候选自动重写；音频仍走缓存（`FilesParsed` 保持 0）。**现有机制足够，不需要给候选签名新增一行**。
- 反过来，如果把 `.lrc` 放进解析缓存（只在音频变化时读），`.lrc` 单独变化时观察不变，`d` 行不变，现有机制**发现不了**；此时就得把 `.lrc` 的大小/mtime 并入解析缓存命中条件，改动面更大且污染“缓存只保存 parser 原始输出”的合同（`library-scan-incremental.md` 缓存节第 1 条）。
- 额外加一条 `.lrc` 的 `Lstat` 签名行（仿 `l` 行）在“每轮都读内容”的方案下是冗余的：内容已经进 `d` 行。只有当实现选择“未变时不读内容”时才需要，但那样候选因其他原因重写时观察里就没有外置歌词，会被写成内嵌/NULL，所以不可行。
- 每轮读取的成本：每个有 `.lrc` 的音频一次 `Lstat` + 一次有界读取（建议上限见第 8 节），每目录至多一次 `ReadDir`。
- 报告：`scan_feedback_snapshot.go:69` 把 `t.lyrics`、`t.lyrics_source` 计入曲目元数据摘要，因此只改 `.lrc` 会让扫描报告显示该曲目“已更新”，与改内嵌歌词一致。

最小改动涉及的函数（详见末节）：`scanner.go` 的物理音频分支 / `scanPipeline.prepareAudio`（找并读 `.lrc`），`scanAudio` 增加外置歌词字段，`scanPipeline.apply`（`scan_pipeline.go:143-186`）在 `cache.applyAudio` 之后、`buildPhysicalSourceObservation` 时用外置歌词覆盖。候选签名、`CandidateUnchanged`、解析缓存都不用改。

## 4. 解析缓存的内容与外置歌词的位置

- 缓存值：`parser.AudioObservation` 去掉 `Artwork` 字节后的 JSON（`parse_cache.go:153-160`），≤ 1 MiB（`:18,160`），只在解析成功且版本非空时写（`:135-151`）。`AudioObservation` 已含内嵌 `Lyrics/LyricsSource`（`parser/parser.go:47-50`）和由内嵌歌词派生的歌词署名（`Credits` 中 `Source="lyrics"`）。
- spec 约束：缓存只保存 parser 原始输出，不能把 organizer 或 CUE 覆盖后的观察写回缓存（`library-scan-incremental.md` 缓存节）。
- 建议放在**缓存外**：`apply` 里 `pipeline.cache.applyAudio(…, audio)`（`scan_pipeline.go:153`）用的是 `audio.observation` 原值写缓存，覆盖必须发生在构造暂存观察的副本上（`:168` 之后），保证缓存里仍是内嵌歌词。
- `parser.CacheVersion`（`parser/version.go:9,28-34`）何时升：spec 规定 `AudioObservation` 字段/类型/含义或共用解析规则改变时升 `CommonVersion`。按缓存外方案，parser 原始输出不变，**不需要升**。只有在以下情况才要升：给 `AudioObservation` 加字段、改变内嵌歌词或歌词署名的解析规则。`ingest.GroupingVersion` 也不需要升：变化来自观察本身，`d` 行已覆盖（spec：只改同样观察的归组规则才升归组版本）。

歌词署名（需要决定）：`finalizeTags`（`parser/tag_finalization.go:10-12`）在每种格式读完标签后、文件名兜底前调用 `addLyricsCredits`（`parser/lyrics_credits.go:14-70`），从**内嵌**歌词前 30 行抽取 `Source="lyrics"` 的署名；某角色已有 tag 署名时整角色丢弃（`:16-21,62-64`）。外置歌词覆盖内嵌后若不处理：
- 内嵌歌词抽出的署名会残留（与显示的外置歌词不对应）；
- 外置 `.lrc` 开头的“作词：…”不会成为署名。
可选做法：在 parser 包导出一个纯函数（例如 `ApplyExternalLyrics(observation *AudioObservation, text string)`）：删掉 `Source=="lyrics"` 的署名 → 设置 `Lyrics/LyricsSource` → 重新调用 `addLyricsCredits` 与 `metadata.CanonicalCredits`。`CanonicalCredits`（`catalog/metadata/text.go:44-66`）同键时 tag 优先，`Source="lyrics"` 的条目可单独剔除。注意 spec（`library-scan-tags.md:41`）要求题头跳过只用解析出的 `Title`、不用文件名补位；在 parser 之外重跑时 `observation.Title` 可能已被 `applyFilenameFallback`（如 `parser/flac.go:60`）填成文件名，需在 `InferredFields["title"]` 为真时按空标题处理。这是共用解析规则的扩展，但函数作用于缓存之外，仍不影响缓存版本。

## 5. 入库数据流与新增来源键

链路（每一步都原样透传，没有来源键白名单校验，除了数据库约束）：
1. `parser.AudioObservation.Lyrics/LyricsSource`（`parser/parser.go:49-50`）；来源键常量 `parser/lyrics.go:11-17`（四个值）。
2. `buildPhysicalSourceObservation`（`scanner.go:554-556`，末尾 `Lyrics: observation.Lyrics, LyricsSource: observation.LyricsSource`）→ `ingest.SourceObservation.Lyrics/LyricsSource`（`catalog/ingest/observation.go:53-54`，JSON `lyrics`/`lyrics_source`，`omitempty`）→ 暂存 JSON。
3. CUE 虚拟轨 `buildCueVirtualObservation`（`scanner.go:558-660`）返回的结构**不含** `Lyrics`/`LyricsSource`/`Credits`（`:615-659`）。
4. `catalog/ingest/persist_track.go:76-82` INSERT/UPDATE 都写 `nullableString(observation.Lyrics), nullableString(observation.LyricsSource)`。
5. `tracks.lyrics_source` 约束 `tracks_lyrics_ck`（`backend/migrations/0015_track_lyrics.sql:6-21`）：`lyrics_source IN ('vorbis_lyrics','vorbis_unsyncedlyrics','mp4_lyr','id3_uslt')`，并显式 `IS NOT NULL`。
6. 其他读取者：`scan_feedback_snapshot.go:69`（只做摘要比较）、`catalog/lyrics.go:53`（只读 `lyrics`，不读 `lyrics_source`）。

新增来源键（如 `lrc_file`）需改：
- `parser/lyrics.go` 增加常量（或放在 library 包，看归属决定），并更新注释“四个值”。
- 新迁移放宽 `tracks_lyrics_ck`：**最新迁移是 `0049_playback_zone_convolution`，新编号 0050**（`backend/migrations/` 列表；合入前需再核对 main 是否已有 0050）。
- 迁移惯例：版本 ≥ 31 必须有同名 `.down.sql`（`database-guidelines.md:171-172`）；回退首行可写 `-- roomusic:irreversible <理由>`。改 CHECK 的先例 `0034_scan_file_filter.sql:16-17` 用 `DROP CONSTRAINT … ; ADD CONSTRAINT …`；其 `.down.sql` 先 `DELETE` 掉使用新值的行，再恢复旧约束。对应到歌词：down 先 `UPDATE tracks SET lyrics=NULL, lyrics_source=NULL WHERE lyrics_source='lrc_file'`，再恢复四值约束（外置歌词是可由重扫重建的派生事实）。0015 用 `DO $$ IF NOT EXISTS` 包装，0034 直接 DDL；新迁移在同一事务内执行，直接 DROP/ADD 即可。
- 回退往返测试 `internal/platform/postgres/migrations_rollback_integration_test.go:97`（`TestPostgreSQLMigrationRollbackRoundTrip`）遍历全部带回退的迁移，新迁移自动纳入，无需改表。
- `scanner_lyrics_integration_test.go:160`（`TestPostgreSQLTrackLyricsConstraint`）覆盖约束各格，需要加 `lrc_file` 合法格。

## 6. 接口 `source` 字段与前端

- 路由：`catalog/httpapi/handlers.go:86` `GET /api/v1/tracks/{id}/lyrics`。
- `source` 生成：`catalog.ParseLyrics` 把 `Source` 硬编码为 `LyricsSourceEmbedded = "embedded"`（`catalog/lyrics.go:18,132,155`）；`LoadTrackLyrics`（`catalog/lyrics.go:50-66`）只 `SELECT id::text, lyrics`，**不读 `lyrics_source`**。要区分外置，需让 `LoadTrackLyrics` 一并读 `lyrics_source`，在 `lrc_file` 时把结果的 `Source` 改成新值（如 `"file"`），`ParseLyrics` 保持纯函数。
- 前端解码器 `frontend/src/features/player/api/decoders.ts:36-41` `source: d.literal("embedded")`，类型 `:53-55`；`decoders.test.ts:52,60` 目前**断言 `source: "file"` 会被拒绝**，必须同步改。
- 歌词面板 `frontend/src/features/player/components/lyrics-panel.tsx` 不使用 `source`（只看 `null` 与 `kind`）；“没有内嵌歌词”文案在 `lyrics-panel.tsx:27`，测试 `lyrics-panel.test.tsx:359,369,380`，文档 `.trellis/spec/frontend/player-design-guidelines.md:51` 与 `.trellis/spec/backend/lyrics-contracts.md:109`。支持外置后文案应改为不带“内嵌”。
- 其他前端用到 `"embedded"` 的地方：`lyrics-panel.test.tsx:237`（夹具）。
- 接口样本：`backend/internal/app/api_samples_integration_test.go:44`（`TestPostgreSQLAPIResponseSamples`），歌词样本由 `recordLyricsSamples`（`:969-978`）按标题录制 `tracks/lyrics-synced|plain|none`，素材在 `:615-638`（`double-album` 三个 FLAC）；样本文件 `backend/internal/app/testdata/api-samples/tracks/lyrics-*.json`；前端契约测试 `frontend/src/test/api-contract.test.ts:374-376` 逐个映射解码器。新增外置样本需同时加样本名与映射；在已有曲目旁放 `.lrc` 会改变现有样本，宜放在不影响其他样本的曲目旁并避免署名头行（署名会进入专辑详情样本）。

## 7. CUE 虚拟轨与逐轨抄本

现有合同（`lyrics-contracts.md` §3 末段）：CUE 虚拟轨没有歌词；逐轨抄本的曲目仍是物理轨，`mergeSingleCueEvidence` 保留它的歌词。代码依据：
- 虚拟轨观察不含歌词（第 5 节第 3 条）；`prepareCueParent`（`scan_pipeline.go:79-91`）复用 `prepareAudio`，但 CUE 槽位的结果只走 `applyCue`（`:188-253`）。
- CUE 父音频同时会作为普通物理文件被遍历暂存。organizer 的 `resolveCuePhysicalCoexistence`（`catalog/ingest/organizer.go:1309-1334`）：父文件对应 >1 条虚拟轨（整轨镜像）时丢弃物理观察；≤1 条（逐轨抄本）时丢弃虚拟轨、保留物理轨，再经 `mergeSingleCueEvidence`（`organizer.go:1158-`）合并 CUE 字段，不触碰 `Lyrics`。
- 测试 `scanner_lyrics_integration_test.go:124`（`TestPostgreSQLCueVirtualTracksHaveNoLyricsButTranscriptTracksKeepThem`）。

建议：
- 只在**物理音频分支**（`scanner.go:421-428` 那条路径）查找并应用外置 `.lrc`，`prepareCueParent` 不查。这样：整轨镜像 `image.flac` 旁的 `image.lrc` 只附在随后被丢弃的物理观察上，虚拟轨仍无歌词，不把整张镜像的歌词分给单轨；逐轨抄本 `01.flac` 旁的 `01.lrc` 附在保留下来的物理轨上，与“保留内嵌歌词”一致。
- 不按 CUE 的 `TRACK`/`TITLE` 去匹配 `NN Title.lrc` 之类的名字（与“虚拟轨没有歌词”的合同冲突，且需要新的匹配规则）。
- 测试在上述 CUE 用例中补两个 `.lrc`（镜像旁、抄本旁），断言虚拟轨仍为 NULL、抄本轨来源为 `lrc_file`。

## 8. 同名匹配细节

- 多个点：`filepath.Ext("a.b.flac") == ".flac"`，主名 `a.b` → `a.b.lrc`。CUE 先例同样用 `strings.TrimSuffix(base, filepath.Ext(base))`（`parser/cue_path.go:36`）。`Song.flac.lrc` 这种“带音频扩展名”的写法当前没有任何先例，若不支持需在合同里写明。
- 扩展名大小写：扫描本身把扩展名小写比较（`scanner.go:391`），抓轨日志用 `EqualFold`（`scanner.go:875`）。`.lrc`/`.LRC`/`.Lrc` 应都认。
- 主名比较：CUE 引用用 `cueNameEqual` = NFC 后 `EqualFold`（`parser/cue_path.go:29-31`），并在多个候选同时匹配时判为歧义（`:44-64`）；封面用“完全一致优先，其次小写相等变体按字节序”（`scanner.go:1279-1290`）。两种先例：
  - 建议：先找名字完全等于 `主名 + ".lrc"` 的项；没有则在同目录里找“主名 NFC 后相等、扩展名 `EqualFold` `.lrc`”的项，恰好一个才采用；多个（如同时有 `Song.LRC` 与 `Song.Lrc` 且没有精确的 `Song.lrc`）视为歧义，不取外置、回退内嵌，不记诊断（歌词缺失不记诊断的既有规则）。同时存在 `Song.lrc` 与 `Song.LRC` 时取精确的 `Song.lrc`。主名是否也大小写不敏感需拍板：CUE 先例是不敏感，但同目录 `Song.flac` 与 `song.flac` 并存时会共用一个 `.lrc`。
  - NFC：macOS 拷贝来的文件名可能是 NFD，与 `.lrc` 的 NFC 形式字节不同；沿用 `norm.NFC`（`golang.org/x/text` 已是依赖，`go.mod:14`）。
- 同主名多格式：`01.flac` 与 `01.mp3` 并存时都会用同一个 `01.lrc`，各版本都显示外置歌词，属预期。
- 查找方式：每个目录最多一次 `os.ReadDir` 建索引（遍历对同一目录可能因子目录递归而“进出”两次，可像 `parseCache.enterDirectory` 一样按当前目录重建）；也可先 `Lstat` 精确名命中即用，未命中再 `ReadDir`。
- 符号链接与文件类型：附属文件先例（封面 `scanner.go:1297-1299`、日志 `:885-889`）都用 `Lstat` 并**拒绝符号链接**与非普通文件；建议 `.lrc` 同样只接受根内普通文件，不跟随链接（音频本身允许根内链接，`.lrc` 按音频链接所在目录找，而不是链接目标目录）。
- 读取上限：规范化后仍沿用 `parser.MaxLyricsBytes = 64 KiB`（`parser/lyrics.go:20`，与约束 `OCTET_LENGTH(lyrics) <= 65536` 一致）。原始字节建议沿用 USLT 先例 `maxLyricsFrameTextBytes = 4 × 64 KiB = 256 KiB`（`parser/lyrics.go:22-24,113`）：GB18030 汉字 2 字节 → UTF-8 3 字节、UTF-16 → UTF-8 至多 1.5 倍，规范化后 ≤ 64 KiB 的歌词原文不会超过 256 KiB。`Lstat` 大小超过上限直接放弃（不截断，截断会切开字符或得到半份歌词），读取用 `io.LimitReader(file, limit+1)` 防止读取期间文件变大。
- 编码：
  - 现有先例 `decodeCueText`（`parser/cue_text.go:19-77`）：UTF-32 BOM 拒绝 → UTF-8 BOM → UTF-16 LE/BE BOM（含代理对校验）→ 合法 UTF-8 → GBK/Shift-JIS/Big5 打分择优。外置歌词按任务要求是 BOM → 合法 UTF-8 → GB18030，可复用其中 BOM 与 UTF-16 分支的写法，但不要复用打分回退，也不要复用 `validCueText`（它拒绝控制字符，而 `NormalizeLyrics` 会直接删掉控制字符）。
  - 外部核对：`golang.org/x/text@v0.41.0/encoding/simplifiedchinese/gbk.go:69-135` 的解码器遇到非法字节写入 `utf8.RuneError` 且**不返回错误**，所以 GB18030 回退后必须检查结果里有无 `U+FFFD`（`cue_text.go:63` 同样这样检查），有则放弃外置歌词。GB18030 是 GBK 的超集，GBK 编码的 `.lrc` 能正确解码。
  - 解码后交给 `parser.NormalizeLyrics`（`parser/lyrics.go:28-48`），规则不变（去 BOM、换行统一、删控制字符、去首尾空白、空/超限视为无）。规范化结果为空或无效时回退内嵌歌词。

---

## 推荐实现方案

### 行为
- 物理音频文件：同目录同主名 `.lrc` 存在、可安全读取、解码并规范化后非空 → 观察的 `Lyrics` 用外置原文、`LyricsSource="lrc_file"`；否则保持解析器给出的内嵌歌词。读取或解码失败不记诊断、不影响曲目。
- CUE 虚拟轨不读；逐轨抄本按物理轨处理。
- 每轮扫描（含解析缓存命中）都读；外置歌词不进解析缓存；不升 `CommonVersion`、不升 `GroupingVersion`、不改候选签名。

### 要改的文件与函数
| 文件 | 改动 |
|---|---|
| `backend/internal/library/parser/lyrics.go` | 新常量 `LyricsSourceLRCFile = "lrc_file"`；导出解码函数（BOM/UTF-16/UTF-8/GB18030 → `NormalizeLyrics`）与原始上限常量；如决定处理署名，导出“应用外置歌词”的纯函数（剔除 `Source="lyrics"` 署名、设置歌词、重跑 `addLyricsCredits` + `CanonicalCredits`，`InferredFields["title"]` 时不用标题跳题头）。新增 parser 单测文件。 |
| `backend/internal/library/`（新文件，如 `sidecar_lyrics.go`） | 同目录 `.lrc` 查找（精确名优先、NFC + 扩展名不敏感、歧义放弃、`skipFile`、`Lstat` 拒绝链接与非普通文件、大小上限、根内校验）与有界读取；每目录索引。 |
| `backend/internal/library/scan_pipeline.go` | `scanAudio` 增加外置歌词字段；物理分支调用 `prepareAudio` 之后填入（`prepareCueParent` 不填）；`apply`（`:143-186`）在 `cache.applyAudio` 之后、`buildPhysicalSourceObservation` 时用外置歌词覆盖副本。 |
| `backend/internal/library/scanner.go` | 物理音频分支（`:421-428`）调用查找；`buildPhysicalSourceObservation` 不变或接收覆盖后的观察。 |
| `backend/migrations/0050_track_lyrics_file.sql` / `.down.sql` | 上行：`ALTER TABLE tracks DROP CONSTRAINT tracks_lyrics_ck; ADD CONSTRAINT tracks_lyrics_ck CHECK (… IN (四值, 'lrc_file'))`，保留显式 `IS NOT NULL` 与 64 KiB 限制；下行：先把 `lyrics_source='lrc_file'` 的两列置 NULL，再恢复四值约束。编号合入前再核对。 |
| `backend/internal/catalog/lyrics.go` | 新 API 来源常量（如 `LyricsSourceFile = "file"`）；`LoadTrackLyrics` 读 `lyrics_source`，`lrc_file` 时设置 `Source`。 |
| `backend/internal/app/api_samples_integration_test.go` + `testdata/api-samples/tracks/` | 新样本 `tracks/lyrics-file`。 |
| `frontend/src/features/player/api/decoders.ts` | `source` 改为 `"embedded" | "file"` 的枚举，类型同步。 |
| `frontend/src/features/player/components/lyrics-panel.tsx` | 文案去掉“内嵌”。 |
| `frontend/src/test/api-contract.test.ts` | 新样本映射。 |
| spec | `lyrics-contracts.md`（§1 第 10 行、§2、§3、§4 约束值、§5 `source`、§8 测试、§9 文案）、`library-scan-incremental.md`（说明外置歌词不进缓存、经 `d` 行触发重写）、`frontend/player-design-guidelines.md:51`。 |

### 测试应覆盖的场景
- parser 单测（`go test ./internal/library/parser/ -run <新用例>`）：UTF-8 无 BOM、UTF-8 BOM、UTF-16 LE/BE BOM（含奇数长度、孤立代理）、GB18030/GBK、非法字节回退失败（含 `U+FFFD` 检查）、UTF-32 BOM 拒绝、原始超 256 KiB 放弃、规范化后超 64 KiB 放弃、规范化后为空；如处理署名：外置署名替换内嵌署名、tag 署名优先、文件名补位标题不参与题头跳过。
- 查找单测（library 包，无数据库）：精确名、`Song.LRC` 单个变体、`Song.lrc` 与 `Song.LRC` 并存取精确、两个变体无精确 → 放弃、`a.b.flac` → `a.b.lrc`、NFD/NFC 主名、隐藏 `.lrc`、符号链接 `.lrc` 拒绝、目录名为 `Song.lrc` 拒绝、子目录中的同名 `.lrc` 不算。
- PostgreSQL 集成（`go test ./internal/library/ -run 'TestPostgreSQL…'`）：外置优先于内嵌（来源 `lrc_file`）；外置无效时回退内嵌；只新增/修改/删除 `.lrc`、音频不变 → `FilesParsed==0`、`ParseCacheHits` 不变、只重写所在候选、曲目 ID 不变、歌词与来源随之更新或回退；解析缓存里仍是内嵌歌词；`full` 模式结果一致；CUE 整轨镜像旁 `.lrc` 不进虚拟轨、逐轨抄本旁 `.lrc` 生效；`tracks_lyrics_ck` 接受 `lrc_file`、仍拒绝未知值与“有歌词无来源”。
- 迁移：`TestPostgreSQLMigrationRollbackRoundTrip` 自动覆盖 0050 往返；下行后 `lrc_file` 行被清空。
- catalog/app：`LoadTrackLyrics` 对 `lrc_file` 返回 `source:"file"`，其余仍 `embedded`；`TestPostgreSQLAPIResponseSamples` 新样本。
- 前端：解码器接受 `file`、拒绝其他值（改 `decoders.test.ts:60`）；面板新文案（`lyrics-panel.test.tsx:359,369,380`）；契约测试读取新样本。

## Caveats / Not Found

- 未找到任何现有读取 `.lrc` 的代码；`.lrc` 仅在 `lyrics-contracts.md:10` 作为“不读”被提及。
- 歌词署名是否随外置歌词重算、主名大小写是否不敏感、API `source` 的取值名（`file`/`external`）、是否支持 `Song.flac.lrc`——代码里没有现成答案，需要在 PRD 里决定。
- 迁移编号 0050 基于当前 main 的 `backend/migrations/`（最新 0049）；并行任务可能占用，合入前需核对。
- 在遍历协程里同步读取 `.lrc` 最简单；若要把读取放进 worker，需要让有 `.lrc` 的缓存命中槽位也走 `pool.Submit`（`scan_pipeline.go:118-141` 目前缓存命中走 `pool.Ready`）。

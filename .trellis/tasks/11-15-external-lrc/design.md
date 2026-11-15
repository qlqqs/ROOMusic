# 技术设计：读取外置 LRC 歌词

依据：`research/scan-sidecar.md`（file:line 证据都在那里，本文不重复）。

## 1. 行为边界

- 只在物理音频分支查找外置歌词（`scanner.go:421-428` 一路进入 `prepareAudio` 的文件）；`prepareCueParent` 不查。整轨镜像旁的 `.lrc` 只附在随后被 `resolveCuePhysicalCoexistence` 丢弃的物理观察上，虚拟轨仍无歌词；逐轨抄本的物理轨保留外置歌词。
- 每轮扫描都读取（含解析缓存命中）；外置歌词不进解析缓存，在 `scanPipeline.apply` 中 `cache.applyAudio` 之后、构造暂存观察的副本上覆盖。
- 不升 `parser.CommonVersion`、`ingest.GroupingVersion`，不改候选签名：外置歌词进入观察后由候选签名的 `d` 行（观察整体摘要）触发重写，音频不重新解析。

## 2. 匹配规则

主名 = 音频文件名去掉最后一个扩展名（`a.b.flac` → `a.b`）。在音频所在目录（不是符号链接目标目录）中：

1. 存在名字恰为 `主名 + ".lrc"` 的项 → 采用。
2. 否则收集“主名经 NFC 后 `EqualFold` 相等、扩展名 `EqualFold` `.lrc`”的项：恰好一个 → 采用；零个或多个 → 无外置歌词。
3. 套用 `filter.skipFile`（隐藏文件不取）；`Lstat` 只接受普通文件，拒绝符号链接与目录。
4. 不支持 `Song.flac.lrc`、按标题匹配或子目录中的歌词。

实现：每个目录最多一次 `os.ReadDir` 建索引（遍历因子目录递归可能“进出”同一目录，按当前目录重建即可，仿 `parseCache.enterDirectory`）；读取在遍历协程中同步进行。

## 3. 读取与解码

- `Lstat` 大小为 0 或超过 `MaxExternalLyricsBytes = 256 KiB` → 放弃（不截断）；读取用 `io.LimitReader(limit+1)`，读到超限同样放弃。
- 解码：UTF-32 BOM → 放弃；UTF-8 BOM → 去 BOM 后须为合法 UTF-8；UTF-16LE/BE BOM → 解码并校验代理对；无 BOM 合法 UTF-8 → 直接用；否则 GB18030 解码，结果含 `U+FFFD` → 放弃。
- 解码结果交给 `parser.NormalizeLyrics`；空或超过 64 KiB → 放弃。
- 任何放弃都回落到解析器给出的内嵌歌词，不记诊断。

解码函数放在 `parser` 包（与 `NormalizeLyrics`、`decodeCueText` 同处，可复用 BOM/UTF-16 写法，不复用 CUE 的多编码打分与 `validCueText`）；目录查找与文件读取放在 `library` 包新文件 `sidecar_lyrics.go`。

## 4. 歌词署名

`finalizeTags` 会从内嵌歌词开头抽取 `Source="lyrics"` 的署名。外置歌词生效时，在 `parser` 导出纯函数 `ApplyExternalLyrics(observation *AudioObservation, text string)`：

1. 删除 `Source == "lyrics"` 的署名；
2. 设置 `Lyrics = text`、`LyricsSource = LyricsSourceLRCFile`；
3. 对新歌词重跑 `addLyricsCredits` 与 `metadata.CanonicalCredits`（tag 署名优先的规则不变）；`InferredFields["title"]` 为真时按空标题处理题头跳过（标题来自文件名补位，不能用来跳题头，见 `library-scan-tags.md:41`）。

作用于缓存之外的副本，缓存里仍是解析器原始输出。

## 5. 数据与接口

- 来源键 `lrc_file`（`parser.LyricsSourceLRCFile`）。
- 迁移 `0050_track_lyrics_file.sql`：`DROP CONSTRAINT tracks_lyrics_ck` 后按原结构重建，`IN` 列表加 `'lrc_file'`，保留显式 `lyrics_source IS NOT NULL` 与 65,536 字节上限。`.down.sql`：先把 `lyrics_source = 'lrc_file'` 的两列置 NULL（重扫可重建），再恢复四值约束。合入前核对编号未被占用。
- `catalog.LoadTrackLyrics` 一并读取 `lyrics_source`：`lrc_file` 时结果 `Source = "external"`（新常量），其余 `"embedded"`；`ParseLyrics` 保持纯函数。
- 接口 `source` 取值 `embedded | external`；样本新增 `tracks/lyrics-external`，素材放在不影响现有样本的曲目旁，`.lrc` 不含署名头行。
- 前端：`decoders.ts` 的 `source` 改为两值枚举；`lyrics-panel.tsx` 无歌词文案改为“这首曲目没有歌词”；面板不显示来源。

## 6. 成本与风险

- 每个音频多一次目录索引查询；有 `.lrc` 的曲目多一次 `Lstat` 与不超过 256 KiB 的读取。
- 风险：GB18030 误判（合法 UTF-8 优先，误判只可能发生在本就不是 UTF-8 的文件上）；`Song.flac` 与 `song.flac` 并存时共用 `song.lrc`（与 CUE 先例一致，可接受）。
- 回滚：还原提交并执行 0050 的下行迁移；外置歌词是可由重扫重建的派生事实。

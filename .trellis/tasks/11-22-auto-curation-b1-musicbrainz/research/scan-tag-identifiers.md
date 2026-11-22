# Research: 扫描器解析并保存标签里的外部标识

- **Query**: B1 让扫描器解析并保存各格式标签里的外部标识（MusicBrainz release / release group / recording / release track / artist / album artist ID、标签 ISRC、AcoustID）所涉及的解析入口、缓存与版本、落库链路、表结构、候选签名、分组键、测试与样本、实例设置与密钥、包依赖规则
- **Scope**: internal（只读；代码、迁移、规范、测试）
- **Date**: 2026-11-22
- **路径约定**: 路径相对仓库根。上一轮调研 `.trellis/tasks/11-22-auto-curation-sources/research/scan-evidence-and-sources.md` 第 1 节已确认的内容不重复，只在与本次发现不一致处注明。

## 结论（15 行以内）

1. 标签键有三个互不相通的入口：通用入口 `setAudioTag`（Vorbis、APEv2 全量经过）、ID3 的帧白名单加 TXXX 描述白名单、M4A 的自由格式名单。**只在 `setAudioTag` 加键，MP3/WAV-ID3/AIFF/DSF/DFF 与 M4A 读不到**。
2. 对上一轮调研 1.1 的补充：MP3 的 TXXX 并非任意描述都进 `setAudioTag`，`parseID3TXXX` 有自己的描述白名单（`mp3.go:449-454`），不在名单的描述整帧丢弃；M4A 自由格式同理（`m4a.go:638-649`）。
3. 键规范化只有“转大写、去 `TXXX:`/`----:` 前缀”，不去空格或下划线；Vorbis 的 `MUSICBRAINZ_ALBUMID` 与 TXXX/M4A 的 `MUSICBRAINZ ALBUM ID` 规范化后是两个不同的键。
4. 没有 `UFID` 帧的解析代码；`UFID` 不在帧白名单（`mp3.go:84`）。
5. 值上限：单值 4096 字节（`metadata.BoundedText` 截断）；`TSRC` 等走 `decodeID3Text` 的帧只取第一个 NUL 之前的值；`setAudioTag` 里 `ISRC` 是后值覆盖前值。
6. 解析缓存存的是 `parser.AudioObservation` 的 JSON（无 json 标签，键为 Go 字段名）。升 `CommonVersion` 后，下一次任何模式的扫描（含定时增量）都会重新解析全部文件并重写全部候选，不需要用户触发完整扫描；内容指纹照常复用。
7. 标签 ISRC 在 `buildPhysicalSourceObservation`（`scanner.go:557`）被丢掉：`SourceObservation` 没有对应字段。
8. 暂存行用 `INSERT … ON CONFLICT` 逐行写，载荷是 JSONB 列 `observation`（整个 `SourceObservation`），另有身份规划用的专用列；JSON 加字段不需要迁移暂存表。
9. 曲目行是“规划沿用则 UPDATE、否则 INSERT”，扫描从不删除曲目行；曲目 ID 在重扫、同根内改名移动（内容指纹两侧唯一配对）后保持。逐轨子表的既有写法是“按 track_id 先删后插”（`track_observations`、`track_credits`、`artist_tag_evidence`）。
10. 候选签名的 `d` 行是整个候选（含全部成员 `SourceObservation`）的 JSON 摘要，`m` 行含成员大小、mtime、`ParserVersion`；`SourceObservation` 里带 JSON 标签的新字段自动进入签名。
11. 分组键是“目录范围 + 专辑名与专辑艺人的规范化分区”，标签里的 release ID 不参与；`backend/internal/library`、`catalog`、`curation` 下搜索 `musicbrainz`/`mbid`/`acoustid`/`UFID` 无命中。
12. 多轨 CUE 时物理整轨被隐藏、虚拟轨存活；文件级标签靠 `buildCueVirtualObservation` 从父观察逐字段抄到每条虚拟轨（当前不抄 `Credits`、`Lyrics`、`Compilation`、标签 ISRC）。
13. `curation_settings` 单行表只有开关与开启者；`steward/secret.Store` 的 `Seal/Open(userID, purpose, …)` 把两个字符串带长度前缀后作 AEAD 关联数据。
14. `internal/platform/*` 现有 8 个包（另有 2 个子包）；非测试代码里 import `steward/egress` 的只有 `steward/web`、`steward/provider`、`steward/httpapi` 三个包共 7 个文件。

---

## 1. 解析器

### 1.1 `AudioObservation` 全部字段

`backend/internal/library/parser/parser.go:13-55`：

| 字段 | 类型 | 行 |
|---|---|---|
| `Title, Artist, Album` | string | :14 |
| `AlbumArtist` | string | :15 |
| `Compilation` | bool | :16 |
| `TrackNumber, DiscNumber` | int | :17 |
| `SourceKind` | string | :18 |
| `Inferred` | bool | :19 |
| `InferredFields` | map[string]bool | :20 |
| `Artwork` / `ArtworkMIME` | []byte / string | :21-22 |
| `DurationSeconds` | float64 | :23 |
| `Codec` | string | :24 |
| `BitDepth, SampleRate, Channels, Bitrate` | int | :25-28 |
| `Year` | int | :29 |
| `Genres` | []string | :30 |
| `Work, MovementName` / `MovementNumber` | string / int | :31-33 |
| `Catalog` | string | :34 |
| `ISRC` | string | :35 |
| `SourceType, MediaType, Edition, Label, Barcode` | string | :36-40 |
| `Credits` | []metadata.Credit | :41 |
| `ArtistTagEvidence` | []metadata.ArtistTagEvidence | :42 |
| `RawAtoms` | map[string]string | :43 |
| `ContentFingerprint` | string | :46 |
| `Lyrics, LyricsSource` | string | :49-50 |
| `originalYears`、`workTags`、`artworkFront`（未导出，不进 JSON） | [8]int / workTags / bool | :52-54 |

结构没有 json 标签；缓存 JSON 的键就是 Go 字段名。分派入口 `ParseAudioFile`（`parser.go:75-103`）按小写扩展名选格式。

### 1.2 各格式读取标签键值的入口

| 格式 | 入口函数与位置 | 键值如何到达 | `SourceKind` |
|---|---|---|---|
| FLAC | `parseFLAC`（`flac.go:12-61`）块类型 4 → `parseVorbisComments`（`flac.go:92-139`） | 每条注释按第一个 `=` 切开（:116），`setAudioTag(key, value)`（:134） | `flac_vorbis_comment`（:22） |
| OGG Vorbis / Opus | `parseOggStream`（`ogg.go:16-83`）→ `parseOggPacket`（:143-176）：`\x03vorbis` 包（:155-158）与 `OpusTags` 包（:160-164）都调 `parseVorbisComments` | 同 FLAC | `ogg_vorbis_comment`（:23）/ `opus_tags`（:161） |
| MP3 | `parseMP3`（`mp3.go:16-47`）→ `parseID3Frames`（`mp3.go:50-157`） | 见 1.3 | `mp3_id3v2`（:22） |
| WAV 内嵌 ID3 | `parseWAV`（`wav.go:12-111`）块 `id3 `/`ID3 `（:71-81）→ `parseID3Chunk`（`aiff.go:191-204`）→ `parseID3Frames` | 与 MP3 同一套帧规则 | `wav_info`（`wav.go:30`） |
| WAV INFO | `LIST` 块（`wav.go:60-70`）→ `parseWAVInfo`（`wav.go:171-201`） | **不经过 `setAudioTag`**；只取 `INAM`、`IPRD`、`IPRO`、`IWRI`（:191-198），另把四字符键交给 `captureArtistTag`（:183，`IART` 等）。读完后 `mergeWAVInfo`（:114-144）只补标题、专辑、署名 | `wav_info` |
| AIFF / AIFC | `parseAIFF`（`aiff.go:19-91`）块 `ID3 `/`id3 `（:51-63）→ `parseID3Chunk`；`NAME`、`AUTH` 文本块只在标题、艺人为空时经 `setAudioTag("TITLE"/"ARTIST")` 兜底（:78-87） | 与 MP3 同一套帧规则 | `aiff_id3`（:29） |
| DSF | `parseDSF`（`dsf.go:9-11`）→ `readDSFTags`（`dsf.go:13-34`）→ `readDSDID3`（`dsd.go:72-99`）→ `parseID3Frames` | 与 MP3 同一套帧规则 | `dsf_id3`（`dsd.go:30`） |
| DFF | `parseDFF`（`dff.go:12-14`）→ `readDFFTags`（`dff.go:16-61`）：`ID3 ` 块（:21-26）→ `readDSDID3`；`DIIN` 的 `DITI`/`DIAR` 只作标题、艺人兜底（:27-60） | 与 MP3 同一套帧规则 | `dff_diin`（`dsd.go:32`） |
| M4A | `parseM4A`（`m4a.go:19-89`）→ `walkM4AAtoms`（:172-230）在 `ilst` 下对每个子原子调 `parseM4AItem`（:462-529） | 普通原子 → `parseM4AItemValue`（:531-607）；`----` → `parseM4AFreeformValue`（:609-658）。见 1.4 | `m4a`（:34） |
| APE | `parseAPE`（`ape.go:17-43`，在 :38 调用）→ `readAPEv2`（`apev2.go:75-114`） | 文本项（kind 0）逐值 `setAudioTag`（:102-106） | `ape_apev2`（`ape.go:32`） |
| WavPack | `parseWavPack`（`wavpack.go:26` 起，在 :113 调用）→ `readAPEv2` | 同 APE | `wavpack_apev2`（`wavpack.go:36`） |

共用 ID3 帧解析的格式有五个：MP3、WAV（内嵌）、AIFF、DSF、DFF，都落在 `parseID3Frames`（`mp3.go:50`）。APE 文件开头的 ID3v2 只用来算音频起点（`apeStreamStart`，`ape.go:45-70`），不解析其中的帧。所有格式读完标签后调用 `finalizeTags`（`tag_finalization.go:10-37`）再调用 `applyFilenameFallback`（`tags.go:118-148`）。

### 1.3 通用入口 `setAudioTag` 与键规范化

`backend/internal/library/parser/tags.go:40-101`，处理顺序：

1. 人员表键（`personnelTagKind`，`credits.go:108-116`）→ `addPersonnelCredits`（:41-43）。
2. 作品与乐章键（`collectWorkTag`，`work_tags.go:20-51`）（:44-46）。
3. 歌词键（`setAudioLyrics`，`lyrics.go:106-124`）（:47-49）。
4. 艺人键原文证据（`captureArtistTag`，`artist_evidence.go:46-48`）（:50-52）。
5. `GENRE` 累加（:53-56）。
6. `key = strings.ToUpper(key)`（:58），`collectOriginalYear`（:59），去掉 `TXXX:`、`----:` 前缀（:60），`value = metadata.BoundedText(value)`（:61）。
7. `switch key`（:62-98）：`TITLE/TRACKTITLE`、`COMPILATION/ITUNESCOMPILATION/TCMP`、`ALBUM/ALBUMTITLE`、`TRACKNUMBER`、`DISCNUMBER`、`DATE/YEAR`、`CATALOG/CATALOGNUMBER/CATALOGNO`、`LABEL/PUBLISHER/ORGANIZATION`、`BARCODE/UPC/EAN`、`EDITION/VERSION/SUBTITLE/RELEASEVERSION`、`ISRC`、`SOURCE/SOURCETYPE`、`MEDIA/MEDIATYPE`。没有 default 分支，其余键不保存。
8. `addTagCredits(observation, key, raw)`（:99）。

规范化只做“转大写 + 去两种前缀”，不折叠空格、下划线或连字符。同一含义的不同写法靠逐个列举，例如专辑艺人列了 `ALBUMARTIST`、`ALBUM_ARTIST`、`ALBUM ARTIST` 三种（`artist_evidence.go:16`；`tag_finalization.go:39-51`）。

按任务给出的三个键名走一遍现有代码：

| 键 | 到达位置 | 结果 |
|---|---|---|
| Vorbis / APEv2 的 `MUSICBRAINZ_ALBUMID` | `setAudioTag`，规范化后为 `MUSICBRAINZ_ALBUMID` | 落在 `switch` 之外，不保存（`tags.go:62-98`） |
| ID3 的 `UFID:http://musicbrainz.org` | `parseID3Frames` 的帧白名单 | `UFID` 不在名单，`continue`（`mp3.go:84-86`）；仓库里没有解析 UFID 载荷（所有者字符串加二进制标识）的函数 |
| ID3 的 `TXXX:MusicBrainz Album Id` | `parseID3TXXX`，描述转大写为 `MUSICBRAINZ ALBUM ID`（`mp3.go:438`） | 不在描述白名单，返回 `"", nil`（`mp3.go:452-453`），帧被丢弃 |
| M4A 的 `----:com.apple.iTunes:MusicBrainz Album Id` | `parseM4AFreeformValue`，名字转大写为 `MUSICBRAINZ ALBUM ID`（`m4a.go:614`） | `default: return nil`（`m4a.go:647-648`） |

### 1.4 MP3 帧白名单与 TXXX 描述白名单

`backend/internal/library/parser/mp3.go`：

- 帧头固定按 10 字节读、帧标识 4 字符（:56-60）；`id3FrameSize`（:161-172）区分 v2.3 普通整数与 v2.4 synchsafe。没有 3 字符帧标识（ID3v2.2）的分支。
- `APIC`（:66-73）、`USLT`（:74-83）单独处理。
- **帧白名单**在 :84：`TIT1 MVNM MVIN TIT2 TPE1 TPE2 TPE3 TALB TRCK TPOS TCON TDRC TYER TSRC TCOM TPUB TXXX TCMP TDOR TORY TEXT TIPL IPLS TMCL`，其余 `continue`（:85）。
- `TIPL/IPLS/TMCL` → `addID3PairedCredits`（:90-97）。
- `TIT1/MVNM/MVIN/TXXX/TCMP/TDOR/TORY`（:98-116）：经 `id3FramePayload`（:195-224，处理分组标识、数据长度指示、v2.4 逐帧同步消除；压缩或加密帧返回 false）取载荷；TXXX 调 `parseID3TXXX`，其余调 `decodeID3ArtistValues`；每个值各调一次 `setAudioTag(key, raw)`（:110-114）。
- 艺人角色帧（`artistTagRole` 命中，:117-131）与 `TCON`（:132-137）也用 `decodeID3ArtistValues`。
- 其余白名单帧走 `decodeID3Text(frame)`（:138）后进 `switch`（:139-154）：`TIT2`、`TALB`、`TRCK`、`TPOS`、`TDRC/TYER`、`TSRC`（→ `observation.ISRC`，:150-151）、`TPUB`（→ `observation.Label`，:152-153）。这一路直接用原始帧字节，不经过 `id3FramePayload`。
- **TXXX 描述白名单**在 `parseID3TXXX`（:433-455）：描述 `strings.ToUpper(strings.TrimSpace(parts[0]))`（:438）；放行的只有人员表键（:439-445）、`creditTagRoles` 认识的署名键（:446-448；键表在 `credits.go:13-37`）、以及 :450 列出的 `ALBUMARTIST, ALBUM_ARTIST, ALBUM ARTIST, ALBUMPERFORMER, FEATURING, COMPILATION, ORIGINALDATE, ORIGINALYEAR, WORK, MOVEMENTNAME, MOVEMENT, MOVEMENTNUMBER, MOVEMENTTOTAL`；其余返回 `"", nil`（:452-453）。放行的键以 `"TXXX:" + 描述` 的形式交给 `setAudioTag`。

与上一轮调研 1.1 的差异：那里写“MP3 的 TXXX 都走这个入口（`mp3.go:111`）”。调用点没错，但到达 :111 之前有 :449-454 的描述白名单，`TXXX:CATALOGNUMBER`、`TXXX:BARCODE`、`TXXX:LABEL`、`TXXX:ISRC` 这类描述目前到不了 `setAudioTag`。

### 1.5 M4A 原子名单

`backend/internal/library/parser/m4a.go`：

- `parseM4AItem`（:462-529）：一个条目最多 64 个子原子（:475-477）；收集全部 `data` 子原子（:483-495），`mean`、`name` 的载荷上限 `metadata.MaxTextBytes+4`（:501，超出时 `readM4AAtomPayload` 报 `M4A metadata too large`，:237-239，整个文件解析失败，:65-67）；然后**对每个 `data` 值各调一次**值处理函数（:517-527）。
- 普通原子名单 `parseM4AItemValue`（:531-607）：`trkn/disk`（:535-547）、`©mvi/©mvc`（:548-553）、`cpil`（:554-559）、`covr`（:560-570）、`©lyr`（:571-575）、艺人角色原子经 `captureArtistTag`（:576-582）、文本原子的 `switch`（:590-605：`©wrk ©mvn ©nam ©alb ©gen ©day ©wrt ©con ©prf ©cmt`）。所有能解出文本的普通原子都记进 `RawAtoms`，最多 64 项（:587-589；`parser.go:70`）。
- **自由格式名单** `parseM4AFreeformValue`（:609-658）：`mean` 必须等于 `com.apple.iTunes`（:610）；`name` 转大写去首尾空白（:614）；依次是人员表键 → `setAudioTag`（:615-617）、艺人与署名键（:618-627）、作品键（:628-630）、`COMPILATION/ORIGINALDATE/ORIGINALYEAR` → `setAudioTag`（:631-633）；最后 `switch name` 只有 `LABEL`（:639-642）与 `UPC`（:643-646），`default: return nil`（:647-648）。只有这两项会以 `----:<NAME>` 记进 `RawAtoms`（:650-656）。自由格式不是整体转交 `setAudioTag`，`----:CATALOGNUMBER`、`----:BARCODE`、`----:ISRC` 目前都读不到。
- 文本解码：`decodeM4AText`（:660-662）= 去首尾空白后 `decodeM4ARawText` 再 `BoundedText`；`decodeM4ARawText`（:664-694）支持 UTF-8 与带 BOM 或类型码 2 的 UTF-16，非法编码返回空串。

### 1.6 多值处理

| 情形 | 行为 | 依据 |
|---|---|---|
| Vorbis 同一键出现多次 | 每条注释各调一次 `setAudioTag`；落到哪取决于字段：`Catalog`、`ISRC`、`Title`、`Album`、`SourceType`、`MediaType` 是**后值覆盖**，`Label`、`Barcode`、`Edition` 是**首个非空**，`GENRE` 累加，艺人键逐条进 `ArtistTagEvidence` | `flac.go:107-137`；`tags.go:53-97` |
| APEv2 一个条目内多值 | 文本条目的值按 NUL 切开，逐值调 `setAudioTag` | `apev2.go:102-106` |
| ID3v2.4 的 NUL 分隔（`TXXX`、`TIT1`、艺人角色帧、`TCON` 等） | `decodeID3ArtistValues` 按帧编码解码后 `strings.Split(…, "\x00")`，去尾部 NUL；UTF-16 带 BOM 的后续值去掉重复 BOM | `mp3.go:377-430`（切分在 :422） |
| ID3 的 `TSRC`、`TPUB`、`TIT2`、`TALB`、`TRCK`、`TPOS`、`TDRC`、`TYER` | `decodeID3Text` 在**第一个 NUL 处截断**（ISO-8859-1 与 UTF-8 见 :324-326、:341-343；UTF-16 在码元为 0 处停，:365-367），只保留第一个值 | `mp3.go:138`、:316-374 |
| TXXX 的多个值 | `parts[1:]` 全部返回，每个值各调一次 `setAudioTag`；`INVOLVEDPEOPLE` 例外，重新用 NUL 拼成一个值 | `mp3.go:434-448`、:110-114 |
| ID3v2.3 的 `/` | 帧解析层不按 `/` 切。艺人与署名的 ` / `、紧凑 `/` 等分隔在 `metadata.CanonicalArtistNames` 里处理；`TRCK/TPOS` 的 `n/m` 由 `parseNumber` 取 `/` 前一段（`tags.go:35-38`） | `.trellis/spec/backend/library-scan-tags.md:7-15` |
| M4A 一个条目多个 `data` | 每个 `data` 各处理一次 | `m4a.go:517-527` |
| WAV INFO | 每个子块一个值，去尾部 NUL | `wav.go:175-199` |

### 1.7 长度上限与截断

- 单个文本值：`metadata.BoundedText`（`backend/internal/catalog/metadata/text.go:26-41`）去首尾空白、丢弃控制字符、在不切断多字节字符的前提下截到 `MaxTextBytes = 4096` 字节（`text.go:10-11`）。`setAudioTag` 的值（`tags.go:61`）、`decodeID3Text`（`mp3.go:331`、:347、:373）、`decodeM4AText`（`m4a.go:661`）、`collectWorkTag`（`work_tags.go:48`）都经过它。
- 艺人原文证据不截断而是报错：单项超过 4096 字节或非法 UTF-8、或每轨超过 100 项时返回错误（`backend/internal/catalog/metadata/artist_evidence.go:8-9`、:21-31；`backend/internal/library/parser/artist_evidence.go:25-44`），该文件解析失败。
- 容器级上限：FLAC 元数据块 16 MiB（`flac.go:36-38`）；Vorbis 单条注释超过 1 MiB（图片注释除外）返回 `invalid Vorbis comment`（`flac.go:118-121`）；OGG 单包 16 MiB（`ogg.go:53-55`）；ID3 标签 16 MiB（`mp3.go:29-31`；`dsd.go:88-90`；`wav.go:72-74`；`aiff.go:52-54`）；APEv2 标签体 16 MiB、条目数 1024，超出时整份标签忽略（`apev2.go:12-15`、:80-82）；APEv2 键必须是 0x20–0x7E 的 ASCII、文本值必须是合法 UTF-8，任一条目不合法则整份标签忽略（`apev2.go:129-142`、:87-90）；M4A 单原子 16 MiB、元数据合计 32 MiB、原子数 10000（`parser.go:58-62`）。

### 1.8 新增一批键需要改的位置

只列现有代码里键必须出现的位置，不含方案。

| 位置 | 作用 | 覆盖的格式 |
|---|---|---|
| `parser.go:13-55` | `AudioObservation` 的字段 | 全部 |
| `tags.go:62-98` | `setAudioTag` 的 `switch` | FLAC、OGG、Opus、APE、WavPack（Vorbis 与 APEv2 全量经过） |
| `mp3.go:84` | 帧白名单（`UFID` 目前不在） | MP3、WAV 内嵌 ID3、AIFF、DSF、DFF |
| `mp3.go:98-116` 或 :139-154 | 帧到字段的分派；`UFID` 的载荷格式没有现成解码函数 | 同上 |
| `mp3.go:449-451` | TXXX 描述白名单 | 同上 |
| `m4a.go:631-649` | 自由格式名单 | M4A |
| `version.go:9` | `CommonVersion`（见第 2 节） | 全部 |

WAV 的 RIFF INFO（`wav.go:171-201`）与 AIFF/DFF 的文本块不经过上述任何名单。

---

## 2. 解析缓存与版本

### 2.1 版本组成

`backend/internal/library/parser/version.go`：

- `CommonVersion = 3`（:9），注释“在观察结构或共用解析规则改变时递增，使所有格式的缓存失效”（:8）。
- `formatVersions`（:12-25）：`.ape .wv .aif .aiff .flac .mp3 .ogg .opus .wav .m4a .dsf .dff` 全为 1。
- `CacheVersion(extension)`（:28-34）返回 `c<Common>.f<Format>`，现为 `c3.f1`；未知扩展名返回空串，不缓存。

### 2.2 缓存内容

- 表 `library_parse_cache`（`backend/migrations/0041_incremental_scan.sql:1-12`）：`root_id, relative_path, directory, source_size, source_modified_at, parser_version, observation JSONB, seen_scan_id, updated_at`，主键 `(root_id, relative_path)`。
- `observation` 是 **`parser.AudioObservation`** 的 `json.Marshal` 结果，写入前把 `Artwork` 置空（`backend/internal/library/parse_cache.go:153-169`）；超过 1 MiB 不缓存（:16-19、:160-162）。读取时 `json.Unmarshal` 回 `parser.AudioObservation`（:124-127）。
- 结构没有 json 标签，导出字段全部进入缓存，现有缓存行里已经含 `ISRC`、`RawAtoms`；未导出字段不进缓存。
- 缓存保存的是解析器原始输出（`finalizeTags` 与 `applyFilenameFallback` 之后），不含 organizer 或 CUE 覆盖后的值（`.trellis/spec/backend/library-scan-incremental.md:20`、:24）。CUE 虚拟轨本身不进缓存，父音频进（:16）。

### 2.3 版本递增后的行为

- 命中条件：增量模式、版本非空、路径、大小、微秒 mtime、`parser_version` 全部相等（`parse_cache.go:118-133`，比较在 :123）。版本不等即未命中。
- 未命中的文件在 worker 里调用 `parser.ParseAudioFile`（`backend/internal/library/scan_pipeline.go:130-141`），成功后按新版本覆盖缓存行（`parse_cache.go:147-150`）。
- 候选签名的成员行含 `ParserVersion`（`backend/internal/library/candidate_signature.go:40`），物理轨取自身扩展名的版本（`scanner.go:557`），CUE 虚拟轨取父音频的版本（`scanner.go:657`）；版本变了签名就变，候选不被跳过而重写。
- 所以升 `CommonVersion` 等于：**下一次扫描（手动增量、定时增量或完整）对全部文件重新解析标签，并重写全部候选**。不需要用户触发完整扫描；增量扫描下自动重解析。`StartScan` 省略选项时为手动增量（`backend/internal/library/scans.go:331-361`），定时扫描触发增量（`backend/internal/library/scan_scheduler.go:104`）。
- 只升某一格式的版本时，只有该格式的文件重新解析、它们所在的候选重写。集成测试 `TestPostgreSQLParseCacheFormatVersionUpgrade`（`backend/internal/library/scan_version_incremental_integration_test.go:23-25`）用 `go test -overlay` 在子进程里把 `.flac` 版本加一，断言 `files_parsed=1`、`parse_cache_hits=1`、`candidates_written=1`、`candidates_skipped=1`，且曲目 ID 与发行 ID 不变（:114-137、:152-186）。该测试用正则 `("\.flac"\s*:\s*)(\d+)` 定位 `version.go` 里的版本声明（:69）。
- 内容指纹不重算：同来源身份且大小、mtime 未变时复用 `tracks.content_fingerprint`（`backend/internal/catalog/ingest/identity_plan.go:220-232`）。
- 重新解析读盘的范围：多数格式只读文件头与标签区；OGG/Opus 的解析会顺序读完整个文件以顺带算指纹（`ogg.go:29-68`）。
- 完整扫描（`full`）绕过缓存读取、候选跳过和指纹借用，仍写缓存（`parse_cache.go:65`、:122；`.trellis/spec/backend/library-scan-incremental.md:42`）。

### 2.4 规范对“改解析规则”的要求

`.trellis/spec/backend/library-scan-incremental.md`：

- :33 “`AudioObservation` 字段、类型、含义或共用解析规则（兜底、分隔、多值、歌词、日期以及 parser 内编号推断）改变，升 `CommonVersion`，使所有格式重新解析；只改某格式读取，升该格式版本；新增格式只加版本 1，不使其他格式失效。”
- :34 `ingest.GroupingVersion=3` “覆盖同样观察所产生的 Release 图：organizer、folder_metadata、抓轨证据、字段决定、归组证据、署名/艺人、流派、作品、搜索投影字段与封面选择。只改这些规则而不改 parser，不升解析器版本，只升归组版本；升归组版本使所有候选重写而不使解析缓存失效。”定义在 `backend/internal/catalog/ingest/version.go:3-5`。
- :35 历次核对的先例：“T ⑥ 升 `CommonVersion` 与 `GroupingVersion`；T ⑦/⑧ 按格式或共用解析改动升解析器版本，并升归组版本”；以及“本次⑦仅补 parser 入口与人员表文本形态，未改同样观察的写入规则，`GroupingVersion` 保持 1；候选元数据摘要已包含 Credits（含 detail/source）、艺人证据，成员签名也包含 `ParserVersion`，无需再升候选签名或分组版本。”
- :32 记录了 `CommonVersion` 每次升级的原因（1→2 署名标签补齐，2→3 内嵌封面解析）。这一行是版本号现值的规范记录，升版本时与代码一起变。
- :16 `SourceObservation` 新增字段“与现有字段按字母序排列”（:14、:15 同样写明）。
- :20 缓存值的定义与 1 MiB 上限。

`.trellis/spec/backend/library-scan-tags.md`：是“各容器读哪些键”的合同文本，按容器列键（专辑艺人各级键 :17-19；署名键 :28-31；合辑键 :47-49；原始年份键 :52-53；作品与乐章键的表 :60-64）。目前没有外部标识的条目。

测试与样本方面规范里能找到的条文：

- `.trellis/spec/backend/library-scan-verification.md:43-55`、:72 列出 parser/organizer 单元测试的覆盖面（容器有效性、各入口的署名键、作品标签“Vorbis、ID3、M4A 的…读取”、“scanner 验证物理/CUE 观察传递”）。
- `.trellis/spec/backend/library-scan-checks.md:70` “大小、微秒 mtime 或解析器版本变化 | 重新解析；完整模式即使相同也重新解析”。
- `.trellis/spec/backend/quality-guidelines.md:16-20`：改动任何 REST 响应字段时，`TestPostgreSQLAPIResponseSamples`（`backend/internal/app/api_samples_integration_test.go:44`）会因样本漂移失败；用 `ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 重写样本，同一提交同步前端 decoder；样本只用合成素材。
- `.trellis/spec/backend/database-guidelines.md:227`：每个新迁移同时提交 `.down.sql`。
- 现有的版本下限断言：`TestCreditRoleCommonVersion`（`backend/internal/library/parser/version_test.go:46-50`）断言 `CommonVersion >= 2`；`TestCacheVersionKnownExtensions`（:9-22）列了 10 个扩展名（不含 `.dsf`、`.dff`）。

---

## 3. 从解析到落库的链路

### 3.1 步骤

1. 遍历与解析：`scanRootWithOptions`（`backend/internal/library/scanner.go:298-544`）建 `scanPipeline`（:343）；音频文件 `prepareAudio`（:425；`scan_pipeline.go:67-78`）先查缓存，CUE 文件 `prepareCue`（`scanner.go:416`；`scan_pipeline.go:94-117`）。未命中的在 worker 里 `ParseAudioFile`（`scan_pipeline.go:130-141`）。
2. 应用（主 goroutine 按序）：`apply`（`scan_pipeline.go:144-190`）。物理轨：外置歌词覆盖（:169-171）→ `buildPhysicalSourceObservation`（:172）→ 填 `ArtworkDeferred`、`SourceSize`、`SourceModifiedAt`（:173-174）→ `stage`（:178）。
3. `buildPhysicalSourceObservation`（`scanner.go:556-558`）：一个结构字面量，把 `AudioObservation` 的字段逐个抄到 `ingest.SourceObservation`。
4. `stageObservation`（`scanner.go:709-727`）：校验艺人证据与相对路径 → `ingest.StageObservation`（`backend/internal/catalog/ingest/staging.go:37-86`）。
5. 遍历结束后：`AlignCueStagingScopes`（`scanner.go:467`）、`MarkDiscParentScopes`（:474）、`planRootIdentities`（:482）→ `ingest.PlanStagedRoot`（`backend/internal/catalog/ingest/identity_plan.go:79-137`）。
6. 落库：`persistStagedCandidatesWithOptions`（`scanner.go:733-831`）逐 scope `LoadStagedScope`（:753）→ `OrganizeObservations`（:761）→ 签名与跳过判断（:784-801）→ `persistOneCandidate`（:809；:980-995）→ `ingest.PersistCandidateWithFeedback`（`backend/internal/catalog/ingest/persist.go:37-177`）→ `persistTrack`（`persist.go:99`；`persist_track.go:68-92`）。

### 3.2 标签 ISRC 在哪一步丢掉

在第 3 步。`buildPhysicalSourceObservation`（`scanner.go:557`）的字面量里没有 `observation.ISRC`；`ingest.SourceObservation`（`backend/internal/catalog/ingest/observation.go:16-70`）也没有承接它的字段，只有 `CueISRC`（:37）。因此解析缓存里有标签 ISRC，暂存行与 `tracks` 里没有。

`SourceObservation` 的全部字段（`observation.go:17-69`，按字母序）：`Album, AlbumArtist, Artist, ArtistTagEvidence, Artwork, ArtworkDeferred, ArtworkMIME, ArtworkRelativePath, ArtworkScope, Barcode, BitDepth, Bitrate, CatalogNumber, Channels, Codec, Compilation, ContentFingerprint(json:"-"), Credits, CueEndFrames, CueEndPresent, CueISRC, CueIndexFrames, CueIndexPresent, CueParentRelativePath, CueReferencedFile, CueSheetPath, Directory, DiscNumber, DiscParentExclusive, DurationSeconds, Edition, FieldSources, Genres, InferredFields, Label, Lyrics, LyricsSource, MediaType, MovementName, MovementNumber, ParserVersion, RelatedSourceRefs, RelativePath, SampleRate, SourceKind, SourceModifiedAt(json:"-"), SourceSize(json:"-"), SourceType, Title, TrackNumber, Work, Year`。

### 3.3 `scan_observations` 暂存行

- 列：建表 `id BIGSERIAL, scan_run_id, root_id, organization_scope, relative_path, observation JSONB, observed_at`，`UNIQUE(scan_run_id, root_id, relative_path)`（`backend/migrations/0011_scan_staging_and_identity.sql:2-11`）；0013 加 `source_identity, fingerprint_path, source_size, source_modified_at, content_fingerprint, cue_index_frames, cue_end_frames, candidate_key, matched_track_id, planned_track_id, content_changed, withheld, planned`（`0013_track_content_fingerprints.sql:39-52`）；0014 加 `planned_release_id`（`0014_release_anchor_location.sql:7`）；0037 加 `previous_release_id`（`0037_scan_observation_previous_release.sql:1`）。
- 写法：**逐行 `INSERT … ON CONFLICT(scan_run_id,root_id,relative_path) DO UPDATE`**，不是 COPY（`staging.go:74-84`）；冲突时清空规划列。
- 可扩展的 JSON 列：`observation` 是整个 `SourceObservation` 的 `json.Marshal`（`staging.go:56-62`），单行上限 24 MiB（:17、:60-62）。带 json 标签的新字段直接进这一列；`ContentFingerprint`、`SourceSize`、`SourceModifiedAt` 是 `json:"-"`，走专用列（`observation.go:14`、:33、:63-64）。
- 读回：`LoadStagedScope`（`staging.go:131-172`）`json.Unmarshal` 回 `SourceObservation`，再从专用列还原大小与时间。
- 暂存层会直接改 JSON：`AlignCueStagingScopes` 追加 `original_organization_scope`（`staging.go:200-214`），`MarkDiscParentScopes` 追加 `disc_parent_exclusive`（:218-244）；封面去重查询用 `observation->>'artwork_scope'`（:43-46）。

### 3.4 CUE 虚拟轨

- 生成：`applyCue`（`scan_pipeline.go:192-257`）。对 CUE 里每条可暂存的轨（`cueTrackCanBeStaged`，`scanner.go:552-554`）取它引用的父音频的解析结果 `audio.observation`（`scan_pipeline.go:245`），调 `buildCueVirtualObservation(audio.observation, document, cue, slot.relative)`（:249），再填 `ArtworkDeferred`、`SourceSize`、`SourceModifiedAt`（父文件的值，:250-251）并暂存（:252）。一个父文件对应多条暂存行，各自的 `RelativePath` 是虚拟路径 `<sheet>#track-<n>@<父相对路径>#index-<帧>`（`scanner.go:616`）。
- 文件级标签怎样传到各虚拟轨：`buildCueVirtualObservation`（`scanner.go:560-661`）先 `observation := parent` 复制父观察（:561），用 CUE 的值覆盖专辑、专辑艺人、流派、厂牌编号、年份、时长、标题、艺人、轨号（:566-612），然后用一个结构字面量把字段**逐个抄进** `SourceObservation`（:617-660）。字面量里出现的父级字段：`SourceType, MediaType, Genres, CatalogNumber, Work, MovementName, MovementNumber, Edition, Label, Barcode, Year, DiscNumber, Codec, BitDepth, SampleRate, Channels, Bitrate, Artwork, ArtworkMIME, ContentFingerprint`，以及筛过的 `ArtistTagEvidence`（`cueArtistTagEvidence`，:664-681）。**没有出现**的：`Credits`、`Lyrics/LyricsSource`、`Compilation`。`observation.ISRC = cue.ISRC`（:584）把父标签的 ISRC 换成 CUE 轨的 ISRC，输出字段是 `CueISRC: cue.ISRC`（:653）。
- 物理整轨与虚拟轨共存时的取舍在 organizer：`resolveCueCandidateGroups`（`backend/internal/catalog/ingest/organizer.go:1062-1156`）→ `resolveCuePhysicalCoexistence`（:1309-1334）。一个父文件有多条虚拟轨时保留虚拟轨、隐藏物理整轨；只有一条虚拟轨时保留物理轨，并由 `mergeSingleCueEvidence`（:1158-1257）把 CUE 的标题、艺人、专辑、轨号、碟号、专辑艺人、流派、厂牌编号、CUE 区间与 `CueISRC`（:1252-1255）并入物理观察。被隐藏的观察没有候选键，不参与落库（`identity_plan.go:139-140`）。
- 现有单元测试：`TestBuildCueVirtualObservationClonesMutableProvenancePerTrack`、`TestBuildCueVirtualObservationDistinguishesSheetAndTrackPerformer`（`backend/internal/library/scanner_observation_test.go:79`、:134）。

---

## 4. `tracks` 与 `releases` 表

### 4.1 `tracks` 当前全部列

没有单一的“最新定义”文件，列由以下迁移累加；最后一次改 `tracks` 结构的是 0050（只换歌词 CHECK）。

| 列 | 迁移 |
|---|---|
| `id, medium_id (→media ON DELETE CASCADE), position, title, artist, disc_number, source_root_id, relative_path, source_status ('present'/'missing'), observed_at` | `backend/migrations/0002_core_slice.sql:49-61`（文件内 `CREATE TABLE tracks`） |
| `source_kind, source_identity, duration_seconds, codec, sample_rate, channels, bitrate` | `0010_album_organization.sql:6-12`；`tracks_source_identity_uq` 唯一索引（:14） |
| `cue_sheet_path, cue_parent_relative_path, cue_referenced_file, cue_index_frames, cue_end_frames, cue_isrc, bit_depth` | `0011_scan_staging_and_identity.sql:19-25`；CHECK 在 :146-162 |
| `content_fingerprint, source_size, source_modified_at` | `0013_track_content_fingerprints.sql:3-5`；`(source_root_id, relative_path)` 的唯一约束在 :29-31 改为仅对有来源身份的行生效的索引；`tracks_root_fingerprint_index` 在 :34-35 |
| `lyrics, lyrics_source` | `0015_track_lyrics.sql:4-5`；CHECK 现值在 `0050_track_lyrics_file.sql:2-8` |
| `work_title, work_catalog, work_index, movement_number, movement_title, work_source` | `0039_classical_works.sql:2-8` |

`persistTrack` 的 INSERT 列清单（`backend/internal/catalog/ingest/persist_track.go:76`）与上表一致，共 35 列（`source_status`、`observed_at` 写常量）。

### 4.2 `releases` 当前全部列

| 列 | 迁移 |
|---|---|
| `id, group_id, title, artist, created_at` | `0002_core_slice.sql:34-40` |
| `group_id` 外键 → `release_groups`；`source_root_id, relative_directory` | `0003_catalog_observations.sql:6-22` |
| `candidate_anchor, candidate_kind, album_artist, year` | `0010_album_organization.sql:2-5`（锚点唯一索引在 `0014_release_anchor_location.sql:4` 删除） |
| `source_type, media_type, genre, catalog_number` | `0011_scan_staging_and_identity.sql:27-30` |
| `edition, label, barcode` | `0012_track_credits_and_release_metadata.sql:3-5`；CHECK :14-31 |
| `edit_revision` | `0017_governed_operations.sql:39` |
| `genre` 改名 `genres TEXT[] NOT NULL DEFAULT '{}'` | `0032_release_genres.sql:4-7` |
| `auto_revision` | `0058_auto_curation.sql:23` |

读取侧不直接读 `releases`，而是读视图 `release_effective`（`backend/internal/catalog/internal/releasesql/release.go:4-7`）。视图最新定义在 `backend/migrations/0058_auto_curation.sql:25-68`，**逐列列举** `releases` 的列并叠加 `release_corrections` 与 `release_auto_values`；`releases` 新增的列不会自动出现在视图里。

`release_groups`：`id, created_at`（`0003:1-4`），`default_release_id, edit_revision`（`0018_release_group_default.sql:8-10`）。`media`：`id, release_id (ON DELETE CASCADE), position, title`（`0002:42-47`），`(release_id, position)` 唯一（`0011_scan_staging_and_identity.sql:112-113`）。

### 4.3 `tracks.cue_isrc` 的读取方与接口位置

| 读取方 | 位置 |
|---|---|
| 发行详情的曲目投影 | `backend/internal/catalog/read.go:186`（`NULLIF(BTRIM(t.cue_isrc),'')`）→ `rows.Scan`（:217 起）读进 `isrc` → :261-265 构造 `ReleaseCue` |
| 响应类型 | `backend/internal/catalog/projection.go:23-29` `ReleaseCue{IndexFrames, EndFrames, StartSeconds, EndSeconds, ISRC *string json:"isrc"}` |
| 扫描反馈的曲目摘要 | `backend/internal/catalog/ingest/scan_feedback_snapshot.go:69`（`COALESCE(t.cue_isrc,'')` 进 md5，决定曲目是否算“元数据有变化”） |
| 逐轨字段观察 | `backend/internal/catalog/ingest/observation.go:84` 把 `CueISRC` 写成 `track_observations` 的 `isrc` 字段 |
| 前端解码 | `frontend/src/features/catalog/api/decoders.ts:153-159`（`cueDecoder` 的 `isrc: d.nullable(d.string())`） |

接口里 `isrc` 的位置是 `media[].tracks[].cue.isrc`；`cue` 对象只在来源种类含 `cue`、或 CUE 区间、`cue_isrc` 任一有值时出现，否则为 `null`（`read.go:261`）。

含 `isrc` 的接口样本只有一个：`backend/internal/app/testdata/api-samples/releases/get-cue.json:73`、:97（值 `XXAAA0500001`、`XXAAA0500002`），由 `recordAlbumDetailSamples` 里的合成 CUE 生成（`backend/internal/app/api_samples_integration_test.go:657-698`）。其余 `releases/*.json` 的曲目 `cue` 都是 `null`。

### 4.4 写 `tracks`/`releases`/`media` 的全部 SQL（非测试代码）

| 语句 | 位置 | 说明 |
|---|---|---|
| `INSERT INTO tracks(…35 列…)` | `backend/internal/catalog/ingest/persist_track.go:76-79` | 规划没有指定沿用曲目时新建 |
| `UPDATE tracks SET medium_id=…, …全部事实列… WHERE id=` | `persist_track.go:81-91` | 规划沿用时更新；同一条语句也完成“曲目换到别的发行”（改 `medium_id`） |
| `UPDATE tracks SET source_identity=NULL, source_status=…` | `persist_track.go:97-121`（`vacateSourceIdentity`） | 腾出被别的曲目占用的来源身份 |
| `UPDATE tracks SET observed_at=NOW()` | `backend/internal/catalog/ingest/candidate_state.go:74-81`（`TouchTracks`）；`held_sources.go:28` | 候选未变跳过、保住来源 |
| `UPDATE tracks SET source_status='missing'` | `backend/internal/catalog/ingest/persist.go:303-336`（`MarkMissingSources`）；`scan_feedback_missing.go:43` | 完整成功扫描后标记消失 |
| `UPDATE tracks t SET artist=v.artist` | `backend/internal/catalog/internal/artistgraph/facts.go:177` | 艺人图回写展示名 |
| `INSERT INTO releases(…17 列…)` / `UPDATE releases SET …14 列…` | `persist.go:195-199`（`persistRelease`） | 扫描写发行 |
| `UPDATE releases SET group_id / edit_revision / auto_revision` | `backend/internal/catalog/governance/claim.go:601`、:610、:629；`revert.go:125`、:248；`issue_review.go:167`；`auto_values.go:157`；`group_change.go:160`；`claim_revert.go:61`；`corrections.go:438` | 治理只改组归属与版本号，不动曲目 |
| `INSERT INTO media` / `UPDATE media SET title` / `DELETE FROM media …（没有曲目的碟）` | `persist.go:81-87`、:127-130 | 扫描维护碟 |

要点：

- **没有 `DELETE FROM tracks`，也没有 `DELETE FROM releases`**（全后端非测试代码搜索无命中）。曲目消失只改 `source_status`；发行不删除。
- 曲目行是**按规划 UPDATE 或 INSERT**，不是先删后写；没有使用 `ON CONFLICT` 的 upsert 语句。
- “发行拆分时曲目移动”没有独立的 SQL：重扫时 organizer 分出新候选，`allocateReleases`（`identity_plan.go:431-459`）按“严格过半成员”决定候选沿用哪个发行，`persistTrack` 的 UPDATE 把曲目的 `medium_id` 指到新发行的碟，曲目 ID 不变。治理侧的组变更、认领只改 `releases.group_id`。
- 逐轨的附属数据由 `PersistCandidateWithFeedback` 在 `persistTrack` 之后同事务维护，写法都是“按 `track_id` 先删后插”：`track_observations`（`persist.go:103-110`）、`track_credits`（:111-118）、`artist_tag_evidence`（`persistArtistEvidence`，`backend/internal/catalog/ingest/artist_projection.go:36-50`；表定义 `backend/migrations/0026_artist_catalog.sql:88-94`，`track_id … ON DELETE CASCADE`，主键 `(track_id, role, tag_key, occurrence_index)`），随后 `searchindex.RefreshTrack`（`persist.go:122-124`）。这段循环（`persist.go:91-125`）只在候选被写入时执行；候选未变被跳过时（`scanner.go:786-800`）只 `TouchTracks`。
- 引用 `tracks(id)` 的现有子表及删除规则：`track_observations`、`track_credits`、`track_analyses`、`artist_tag_evidence`、`catalog_search_values`、`artist_credits` 等为 `ON DELETE CASCADE`（`0003:27`、`0012:38`、`0033:2`、`0026:89`、`0025:67`、`0026:23`）；收藏、歌单、艺人指派覆盖为 `ON DELETE RESTRICT`（`0021:8`、`0022:17`、:44、:48、`0026:40`）；播放历史与播放区无动作子句（`0016:10`、`0043:42`）。

### 4.5 曲目 ID 的稳定性

规则写在 `backend/internal/catalog/ingest/identity_plan.go:11-19` 的注释里，实现是 `PlanStagedRoot`（:79-137）：

- 先按来源身份匹配（:87-92）。来源身份的定义在 `SourceIdentityForObservation`（`backend/internal/catalog/ingest/identity.go:11-21`）：物理轨 `<root>:physical:v1:<规范相对路径>`；CUE 虚拟轨 `<root>:cue:v1:<sheet>:<父路径>:<轨号>:<INDEX 01 帧>`。
- 同一来源身份下两边指纹都已知且不同 → `content_changed`，不沿用（:101-109）。只改标签不改变指纹（指纹只取决于音频负载），所以改标签后重扫沿用原曲目 ID。
- 遍历完整且无指纹失败时，`pairVanishedTracks`（:330-383）把没匹配到身份的观察与本根本轮消失的曲目按 `(指纹, CUE 起点, CUE 终点)` 配对，**两侧都恰好一条**才沿用原曲目 ID；多对多记 `identity_ambiguous`，不重绑定（:343-362）。配对限定在同一个根内（`vanishedTracksCTE`，:307-316，`t.source_root_id=$2`）。
- 遍历不完整时不重绑定，疑似移动或换内容的 scope 整体暂缓（`withholdSuspiciousScopes`，:388-420）。
- 发行 ID：候选的保留曲目中严格过半当前属于同一个既有发行时沿用它（`allocateReleases`，:431-459）。
- 集成测试：`backend/internal/library/identity_rebinding_integration_test.go`、`release_membership_integration_test.go`。规范：`.trellis/spec/backend/identity-contracts.md`。

---

## 5. 候选签名

- 计算函数：`candidateSignature(root, candidate)`（`backend/internal/library/candidate_signature.go:20-93`），SHA-256 小写十六进制。存储 `scan_candidate_states(root_id, candidate_key, release_id, signature, updated_at)`（`backend/migrations/0041_incremental_scan.sql:14-21`）。
- 输入（按写入哈希的顺序）：
  1. `g` 行 `ingest.GroupingVersion`，`k` 行 `CandidateIdentity(root.ID, candidate.Anchor)`（:26）。
  2. `d` 行 `ingest.CandidateMetadataSignature(candidate)`（:21-27）。
  3. `m` 行：按相对路径排序的每个成员的 `RelativePath, SourceSize, SourceModifiedAt(微秒 UTC), ParserVersion`（:38-41）。
  4. `s` 行：成员引用的 CUE sheet 的路径、Lstat 大小、mtime（:50-59）。
  5. `a` 行：`listFolderArtworkCandidates` 列出的全部文件夹封面候选的路径、大小、mtime（:60-67）。
  6. `l` 行：`candidateSourceDirectories` 各目录里普通 `.log` 文件的目录、名字、大小、mtime（:68-91）。
- `CandidateMetadataSignature`（`backend/internal/catalog/ingest/candidate_state.go:13-32`）：把 `JoinGroupID` 置空、把每个成员观察的 `Artwork` 置 nil、`ArtworkDeferred` 置 false，然后对**整个 `OrganizedCandidate`**（`organizer.go:48-73`，含 `Mediums` 里每条 `OrganizedTrack.Observation`）做 `json.NewEncoder(hash).Encode`。
- 新增标识字段是否需要另外进入签名：
  - `SourceObservation` 里带 json 标签的字段会随 `d` 行自动进入签名；`json:"-"` 的字段不会（现有三个是内容指纹、来源大小、修改时间，分别由身份规划与 `m` 行覆盖，`candidate_state.go:11-12`）。
  - 给文件新加标签通常改变文件大小或 mtime：解析缓存未命中而重新解析（第 2 节），同时 `m` 行变化，候选重写。
  - 标签编辑保留了大小与 mtime 时，解析缓存命中，不重新解析，签名也不变；规范给出的做法是管理员发 `{"mode":"full"}`（`.trellis/spec/backend/library-scan-verification.md:87`）。这与现有全部标签字段的行为相同。
  - 升 `CommonVersion` 后 `ParserVersion` 变化，`m` 行变化，全部候选重写一次。
- 跳过判断：`ingest.CandidateUnchanged`（`candidate_state.go:37-71`）除签名相同外还要求全部成员沿用原曲目与原发行、曲目 `present` 等；命中后只 `TouchTracks`（`scanner.go:786-800`）。
- 现有测试：`TestCandidateMetadataSignatureCreditsAndArtwork`（`backend/internal/catalog/ingest/candidate_metadata_signature_test.go:9-72`，表驱动：逐项改一条观察字段，断言签名是否变化）；`backend/internal/library/candidate_signature_test.go:12-113`（成员输入、附属文件、封面与日志）。

---

## 6. 发行身份（分组键）

确认：标签里的 release ID 不参与归组。`backend/internal/library`、`backend/internal/catalog`、`backend/internal/curation` 下不区分大小写搜索 `musicbrainz`、`mbid`、`acoustid`、`UFID` 无命中。

分组键的定义位置：

| 层次 | 位置 | 内容 |
|---|---|---|
| 整理范围（暂存行的 `organization_scope`） | `OrganizationScope`，`backend/internal/catalog/ingest/scope.go:10-23` | 碟目录 → `disc-parent:<父目录>`；注册根下的文件 → 有专辑名时 `root-album:<专辑证据的 SHA-256 前 12 字节>`，否则 `root-source:<路径>`；其余 `directory:<目录>` |
| 目录与碟目录识别 | `locateObservation`，`organizer.go:287-297` | 目录名能解析出碟标记时是碟目录 |
| 专辑分区 | `albumPartition`，`organizer.go:504-517`；`albumEvidence` :519-522；`compatibleAlbumPartition` :540-550 | `规范化专辑名(去碟标记)|规范化专辑艺人`；推断出来的专辑名、专辑艺人视为空 |
| 候选桶 | `OrganizeObservations`，`organizer.go:626` 起；桶键 `kind \x00 scope \x00 partition`（:694-701）；基础分组（:705-726）；根目录散曲、格式切分、同目录专辑多数决与拆分（:732-790 起） | 候选种类见 `CandidateKind`（:14-26） |
| 稳定锚点 | `CandidateAnchor{Kind, Scope, Partition}`（:28-31）；`stableAnchorPartition`（:1288-1304）；`CandidateIdentity`（:1347-1351） | 写入 `releases.candidate_anchor` 的格式 `<root>:v2:<kind>:<scope>:<partition>`；锚点只是位置信息，发行沿用按曲目成员关系决定（`identity_plan.go:17`） |
| 同专辑的格式版本 | `VersionGroup = "format:" + scope`（`organizer.go:819`）；`ReleaseGroupKey`（:816） | 供新建发行加入已有组 |

规范：`.trellis/spec/backend/library-scan-grouping.md`。

---

## 7. 测试与样本

### 7.1 解析器测试文件

`backend/internal/library/parser/` 下：

| 文件 | 内容 |
|---|---|
| `parser_test.go` | FLAC/MP3 基本读取与拒绝无效输入（:14）；流派多值（:102、:116、:131） |
| `parser_repair_test.go` | M4A 容器与自由格式名单（`TestParseM4AWriterAndAllowlistedFreeformMetadata` :128，断言 `LABEL`、`UPC` 被映射而 `PRIVATE_NOTE` 不进入）；FLAC 发行字段（`TestParseFLACRetainsReleaseMetadataAndTrackCredits` :188，含 `LABEL`、`UPC`、`EDITION`）；MP3 UTF-16（:223）；WAV（:313）；OGG/Opus（:370）；CUE（:406 起） |
| `artist_evidence_test.go` | 跨格式的艺人证据；合辑与原始年份（:275）；TXXX 专辑艺人（:251） |
| `credits_test.go` | 署名键各入口；人员表 |
| `work_tags_test.go` | 作品与乐章（Vorbis、ID3、M4A） |
| `lyrics_test.go` | 歌词；ID3 帧大小与同步消除（:421、:451） |
| `lossless_test.go` | APE、WavPack、AIFF 的参数与标签（`TestAPEv2Tags` :169；`TestAIFFTagsPreferID3ToTextChunks` :142） |
| `merge_tag_finalization_test.go` | APE、WavPack 与 ID3 容器对同一组标签得到相同结果 |
| `dsd_test.go` | DSF/DFF 的标签优先级 |
| `artwork_priority_test.go` | 内嵌封面；WAV 内嵌 ID3（:190、:204） |
| `version_test.go` | 缓存版本 |
| `fingerprint_test.go`、`ogg_stream_test.go`、`mp3_vbr_test.go` | 指纹与流事实 |
| `real_music_readonly_test.go` | 只在设置 `ROOMUSIC_REAL_MUSIC_ROOT` 时运行（:15-17、:53-55），本次未读取任何真实曲库 |

### 7.2 构造标签的测试辅助函数

导出的共用夹具包 `backend/internal/library/parser/parsertest/`（依赖规则里是叶子包，`dependency_rules_test.go:79`）：

| 函数 | 位置 | 用途 |
|---|---|---|
| `FLACWithComments(t, comments)` | `parsertest.go:12-24` | 只含 VORBIS_COMMENT 的最小 FLAC，`comments` 形如 `KEY=value` |
| `FLACWithAudio(t, md5, frames, comments...)` | `parsertest.go:46-53` | 带 STREAMINFO 与帧字节，有内容指纹 |
| `APEv2(version, withHeader, items...)`、`APEv2Item{Key, Value, Flags}` | `lossless.go:58-86` | APEv2 标签，APE 与 WavPack 共用 |
| `APE`、`APELegacy`、`WavPackBlock`、`WavPackMetadata` | `lossless.go:9-55`、:89-115 | 音频流部分 |
| `AIFFChunk`、`AIFF` | `lossless.go:118-147` | AIFF 容器与块 |
| `ID3v23(frames)` | `lossless.go:150-154` | 给已有帧加 v2.3 标签头 |

包内（`_test.go`）辅助函数：

| 目的 | 函数与位置 |
|---|---|
| 带 TXXX 帧的 MP3 | `id3Tag(major, tagFlags, frames...)`（`lyrics_test.go:208-213`）加 `id3Frame(major, id, flags, payload)`（:216-226）；TXXX 的写法见 `credits_test.go:161`：`id3Tag(4, 0, id3Frame(4, "TXXX", [2]byte{}, append([]byte{3}, []byte(key+"\x00"+raw)...)))` |
| 任意帧标识的 ID3 帧（也可用于 `UFID` 这类二进制载荷） | `repairMP3BinaryFrame(frameID, frame)`（`parser_repair_test.go:746-755`，返回带标签头的完整标签，取 `[10:]` 得到帧）；`id3PlainSizeFrame`（`lyrics_test.go:229-233`） |
| 按 `[][2]string{帧, 文本}` 生成 v2.3 标签 | `workMP3Fixture`（`work_tags_test.go:82-91`） |
| 固定几种文本帧的 MP3 | `createMP3Fixture`（`parser_test.go:76-100`，v2.4，五种帧）；`repairMP3Frames`（`parser_repair_test.go:729-744`，v2.3，含 `TSRC`） |
| 带自由格式原子的 M4A | `repairM4AFreeform(name, value)`（`parser_repair_test.go:780-784`，`mean` 固定 `com.apple.iTunes`）；外层用 `lyricsM4A(codec, extraItems)`（`lyrics_test.go:161-166`）或 `repairM4AMoovWithItems(…, extraItems)`（`parser_repair_test.go:791` 起）；只测条目解析可直接 `parseM4AAtoms(repairM4AAtom("ilst", items), &observation)`（`parser_repair_test.go:133-134`） |
| M4A 普通原子与 data | `repairM4AAtom`（`parser_repair_test.go:700-706`）、`repairM4AData(typeCode, value)`（:773-778） |
| Vorbis 注释的 FLAC / OGG / Opus | `repairFLACComments`（`parser_repair_test.go:717-727`）、`lyricsVorbisStream`（`lyrics_test.go:114`）、`lyricsOpusStream`（:125）、`testVorbisComments`（`fingerprint_test.go:511`） |
| WAV | `testWAVChunk`、`testWAVFormat`、`testWAV`（`fingerprint_test.go:480-510`）；带内嵌 ID3 的 `artworkWAV(metadata...)`（`artwork_priority_test.go:277`） |
| DSF / DFF | `dsdTestID3`、`dsdTestDSF`、`dsdTestDIIN`、`dsdTestDFF`、`dsdTestDFFChunk`（`dsd_test.go:171-235`） |
| 写夹具并解析 | `parseLyricsFixture(t, name, content)`（`lyrics_test.go:57`）、`writeFingerprintFixture`（`fingerprint_test.go:405`）、`parseArtworkFixture`（`artwork_priority_test.go:311`） |

扫描与接口层的合成素材：`audioFile` 与 `writeAudio`（`backend/internal/library/identity_rebinding_integration_test.go:20-39`，FLAC，标签只有标题、艺人、专辑、轨号）、`writeFixtureFile`（:41 起）、`writeLyricsMP3`（`backend/internal/library/scanner_lyrics_integration_test.go:26`）。

### 7.3 可扩展的表驱动测试

没有一张“全部格式 × 全部标签键”的统一对照表；最接近的几处：

| 测试 | 位置 | 形状 |
|---|---|---|
| `artistFixture(format, values)` 加 `TestArtistEvidenceRetainsRepeatedRawTagsAcrossFormats` | `artist_evidence_test.go:16-50`、:61 起 | 按 `flac/ogg/opus/mp3/m4a/wav` 六种格式用同一组值造夹具；键固定为艺人键 |
| `TestM4AFreeformAndID3TXXXCreditRoleEntrances` | `credits_test.go:141-184` | `{key, raw, want}` × `{m4a, mp3}`：同一个键分别以自由格式原子与 TXXX 写入，含一个应被忽略的未知键 |
| `TestContainerCreditAliasEntrances` | `credits_test.go:186-226` | `{format, key, role, source}` |
| `parsePersonnelCreditFixture(t, format, key, raw)` | `credits_test.go:228-246` | `flac/m4a/mp3` 三种入口写同一个键 |
| `TestAudioParsersReadWorkAndMovementTags` | `work_tags_test.go:11-65` | 每种容器一个夹具函数，断言同一组字段 |
| `TestAPEv2Tags` | `lossless_test.go:169-214` | APEv2 版本 × 有无头 × `.ape/.wv` × 有无尾部 ID3v1 |
| `TestLosslessParsersFinalizeMergedTags` | `merge_tag_finalization_test.go:11` 起 | 同一组标签分别以 APEv2 与 ID3 写进多种容器 |

### 7.4 接口样本

`backend/internal/app/testdata/api-samples/` 里含曲目 `isrc` 的只有 `releases/get-cue.json`（:73、:97，位置 `media[].tracks[].cue.isrc`）。其余带曲目的发行样本（`get.json`、`get-single.json`、`get-work.json`、`get-corrected.json`、`get-claimed.json`、`get-dsf.json`、`get-dff.json`）的 `cue` 为 `null`；`tracks/` 下的样本（`analysis-*.json`、`lyrics-*.json`、`playback.json`）与 `search/` 下的曲目样本不含 `isrc`。样本由 `TestPostgreSQLAPIResponseSamples`（`backend/internal/app/api_samples_integration_test.go:44`）生成并比对，前端契约测试 `frontend/src/features/catalog/api/decoders.test.ts` 逐个解码。

---

## 8. 实例级设置与密钥

### 8.1 `backend/internal/curation/settings.go`

表 `curation_settings`（`backend/migrations/0058_auto_curation.sql:103-110`）：`id SMALLINT PRIMARY KEY CHECK (id = 1)`、`enabled BOOLEAN NOT NULL DEFAULT false`、`enabled_by UUID REFERENCES users(id)`、`enabled_at TIMESTAMPTZ`、`updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`；迁移里插入了 `id=1` 的一行（:110）。

Go 接口：

| 名称 | 位置 | 签名与行为 |
|---|---|---|
| `Writer` / `NewWriter(db *sql.DB)` | `settings.go:13-15` | 持有 `*sql.DB` |
| `Enabler{ID, Username}` | :17-20 | |
| `Settings{Enabled bool; EnabledBy *Enabler; EnabledAt *time.Time; Active bool; InactiveReason *string}` | :21-27 | `Active` 与 `InactiveReason` 是读取时算出的，不是表列 |
| `loadSettings(ctx, tx) (Settings, error)`（未导出） | :30-59 | `SELECT … FROM curation_settings s LEFT JOIN users u … WHERE s.id=1 FOR SHARE OF s`；开启者经 `identity.IsEnabledAdmin` 判断是否仍是启用状态的管理员；原因码 `disabled`、`enabler_unavailable` |
| `activeEnabler(ctx, tx) (string, error)`（未导出） | :61-73 | 开关关着或开启者不可用时返回 `operations.Conflict("curation_disabled", …)` |
| `(*Writer).Settings(ctx) (Settings, error)` | :75-86 | 开事务读 |
| `(*Writer).SetSettings(ctx, actorID string, enabled bool) (Settings, error)` | :88-105 | `UPDATE curation_settings SET enabled=…, enabled_at=…, enabled_by=…, updated_at=now() WHERE id=1` 后重读 |

其他调用点：`backend/internal/curation/batches.go:31`、`writer.go:57`（事务外直接读 `enabled_by`）、`writer.go:74`。HTTP：`GET`/`PUT /api/v1/admin/curation/settings`（`backend/internal/curation/httpapi/handlers.go:30-31`，仅管理员）。装配：`backend/internal/app/app.go:146`、:155。合同：`.trellis/spec/backend/curation-contracts.md:33`、:132-133。

表里没有外部源的开关、密钥或其他键值列。

### 8.2 实例密钥库

包路径 `backend/internal/steward/secret`（包注释“管理管家的实例密钥与按用户、用途绑定的认证加密”，`secret.go:1`）。依赖规则里是不依赖任何内部包的叶子（`dependency_rules_test.go:48`）。

| 名称 | 位置 | 签名与行为 |
|---|---|---|
| `New(path string) *Store` | `secret.go:27` | 只记路径，构造时不读写文件 |
| `(*Store).Seal(userID, purpose string, plaintext []byte) ([]byte, error)` | :31-42 | 首次使用时加载或**创建**密钥文件（`load(true)`）；输出为 1 字节版本号（值 1，:13）+ nonce + 密文与认证标签；AES-256-GCM（:70-86） |
| `(*Store).Open(userID, purpose string, ciphertext []byte) ([]byte, error)` | :45-59 | 不创建密钥文件（`load(false)`）；任何失败都返回 `ErrCannotOpen` |
| `ErrCannotOpen` | :16 | “无法解密密钥，请重新填写” |
| `LastFour(plaintext string) string` | :62-68 | 末四个 Unicode 字符 |
| `SearchKeyPurpose = "steward.search"` | :96 | 导出的用途常量 |
| `loadKey(path, create)`（未导出） | `key.go:10-53` | 密钥必须恰好 32 字节；创建时目录 0700、临时文件 0600、写入后 rename |

绑定“用户加用途”的方式：`associatedData(userID, purpose)`（`secret.go:88-93`）把两个字符串各加 8 字节大端长度前缀后拼接，作为 AEAD 的关联数据；解密时两者必须与加密时逐字节相同。

现有用途与调用点：模型密钥用途 `modelKeyPurpose = "steward.model"`（`backend/internal/steward/httpapi/settings.go:19`）；`Seal` 在 `settings.go:242`；`Open` 在 `settings.go:299`、:468、:485、`probe.go:56`、`backend/internal/steward/web/search.go:79`。密钥文件路径在装配处拼出：`secret.New(filepath.Join(dataDirectory, "steward", "instance.key"))`（`backend/internal/app/steward.go:84`）。

关于“实例加用途”：`Seal`/`Open` 的第一个参数类型是普通 `string`，代码里不校验它是用户 ID，也不查库；现有调用全部传的是用户 ID。没有不带用户参数的方法，也没有实例级用途的常量或调用点。import 该包的非测试代码：`backend/internal/steward/httpapi/settings.go:16`、`handlers.go:14`、`backend/internal/steward/web/search.go:15`、`service.go:17`、`backend/internal/app/steward.go:40`；依赖规则里允许 import `steward/secret` 的包是 `steward/web`、`steward/httpapi`、`app`（`dependency_rules_test.go:41`、:43、:123）。

---

## 9. 包依赖规则

### 9.1 规则写法

`backend/internal/app/dependency_rules_test.go`：

- 规则类型（:17-21）：

  ```go
  type packageRule struct {
  	imports []string
  	http    bool
  }
  ```

- 规则表 `packageRules`（:27-129），键与 `imports` 都是相对 `internal/` 的路径。表上的注释（:23-26）：“平台包是叶子；能力核心（catalog、library、parser）不含 HTTP；跨能力只经公开合同；各 httpapi 包只依赖本能力、identity 与 platform，且彼此不导入；只有 app 可以装配全部。新增包必须先在这里声明规则，测试才会通过。”
- 几条现有规则原文：

  ```go
  "steward/web":                       {imports: []string{"identity", "platform/buildinfo", "platform/uuid", "steward/egress", "steward/runtime", "steward/secret", "steward/tool"}, http: true},   // :41
  "steward/provider":                  {imports: []string{"steward/egress"}, http: true},   // :42
  "steward/secret":                    {},                                                  // :48
  "steward/egress":                    {http: true},                                        // :49
  "platform/config":                   {imports: []string{"platform/weborigin"}},           // :57
  "platform/httpserver":               {http: true},                                        // :58
  "curation":                          {imports: []string{"operations", "identity", "catalog", "catalog/governance", "platform/uuid"}},   // :74
  "curation/httpapi":                  {imports: []string{"curation", "identity", "operations", "platform/httpserver"}, http: true},      // :75
  "library/parser":                    {imports: []string{"catalog/metadata", "catalog/classical", "playback/dsdio"}},                    // :78
  "library":                           {imports: []string{"catalog", "catalog/governance", "catalog/artwork", "catalog/ingest", "catalog/metadata", "library/parser", "platform/uuid", "platform/workpool"}},   // :80
  ```

- 检查逻辑 `TestPackageDependencyDirections`（:131-186）：遍历 `internal/` 下每个含 Go 文件的目录，用 `build.ImportDir(path, 0)` 取**非测试** import（:138）；目录没有声明规则则报错（:146-149）；import 了 `net/http` 而规则 `http` 为假则报错（:161-163）；import 了规则 `imports` 之外的内部包则报错（:164-169）；`dsp/` 下的包不得 import `internal` 之外的项目包（:158-160）；规则表里有而目录不存在的条目也报错（:176-185）。测试文件的 import 不在检查范围内。
- 规范：`.trellis/spec/backend/directory-structure.md:122`（“`platform` owns mechanisms shared by the process, never feature policy.”）、:155、:157、:195；管家包的依赖表在 `.trellis/spec/backend/steward-runtime-contracts.md:342-345`。

### 9.2 `internal/platform/*` 现有包

目录（`backend/internal/platform/`）：`buildinfo`、`config`、`diagnostics`、`httpserver`、`postgres`、`uuid`、`weborigin`、`workpool`。另有两个子包：`diagnostics/stewardtools`、`postgres/postgrestest`。

规则（`dependency_rules_test.go`）：`platform/diagnostics` 依赖 `platform/buildinfo`、`platform/postgres`（:28）；`platform/diagnostics/stewardtools` 依赖 `platform/diagnostics`、`steward/tool`（:29）；`platform/buildinfo`、`platform/uuid`、`platform/weborigin`、`platform/workpool`、`platform/postgres` 无内部依赖（:53-56、:59）；`platform/config` 依赖 `platform/weborigin`（:57）；`platform/httpserver` 无内部依赖、可用 `net/http`（:58）；`platform/postgres/postgrestest` 依赖 `platform/uuid`（:60）。

### 9.3 import `internal/steward/egress` 的位置（全仓库）

非测试 Go 文件（7 个文件，3 个包）：

| 包 | 文件:行 |
|---|---|
| `steward/web` | `backend/internal/steward/web/search.go:14`；`fetch.go:20`；`service.go:15`；`musicbrainz.go:13` |
| `steward/provider` | `backend/internal/steward/provider/client.go:11` |
| `steward/httpapi` | `backend/internal/steward/httpapi/errors.go:10`；`outbound.go:10` |

测试文件（4 个，都在 `steward/web`）：`export_test.go:4`、`c8_http_integration_test.go:21`、`c8_round2_test.go:15`、`c8_test.go:21`。`steward/egress` 自己目录下还有 `client_test.go`、`c8_round2_test.go`（包内测试）。

依赖规则里出现 `steward/egress` 的行：`dependency_rules_test.go:41`、:42、:43（三个允许 import 它的包）与 :49（它自己的规则）。`app` 的规则（:118-128）不含 `steward/egress`。

文档与规范里按路径提到该包的位置：`.trellis/spec/backend/steward-runtime-contracts.md:342`、:344、:345、:969、:1089、:1108、:1126、:1127；`.trellis/spec/backend/steward-web-contracts.md:302`；`docs/architecture/adr/0006-auto-curation-and-sources.md:52`；`.trellis/tasks/11-22-auto-curation-sources/prd.md:58`、`design.md:136`。

包的对外接口（`backend/internal/steward/egress/client.go`）：`Policy`（:16）与三个取值（:18-25）、`Options`（:28）、`NewClient(Options) (*http.Client, error)`（:44）、`IsPublicIP(netip.Addr) bool`（:132）、`CheckEndpoint(ctx, client, endpoint) error`（:172）；`errors.go`：`ErrDenied`（:6）、`ErrorCode`（:9）、`Error`（:21）。包注释写“提供管家统一的出站 HTTP 客户端与连接地址策略”（`client.go:1`）。

---

## 未找到与注意事项

- **各格式里 MusicBrainz、AcoustID 标识的完整键名对照表**（release group、release track、artist、album artist、AcoustID 在 Vorbis、ID3、MP4、APEv2 里各叫什么）仓库内没有：代码与规范都没有这些键，本机归档的参考设计里 `musicbrainz` 只在一个标签键名里出现一次（上一轮调研第 10 节）。本文只按任务说明给出的三个键名核对了现有代码的行为；任务限定不访问网络，未查外部资料。
- **没有 `UFID` 帧的解析代码**，也没有任何读取“所有者字符串加二进制标识”这种载荷的函数；ID3v2.2 的 3 字符帧标识没有处理分支（`mp3.go:56-60`）。
- **MP3 文件尾部的 APEv2 与 ID3v1 标签不解析标签内容**：`parseMP3` 只读文件开头的 ID3v2（`mp3.go:23-41`）；尾部标签只在算音频区间时被跳过（`TestMP3FingerprintStripsLeadingAndTrailingTags`，`fingerprint_test.go:173`）。
- **WAV 的 RIFF INFO 不经过任何通用键入口**（`wav.go:171-201`），只认四个固定块加艺人键。
- `tracks`、`releases` 的“当前全部列”是按迁移文件逐个累加整理的，没有连接数据库核对实际 schema（任务限定不读开发数据）。
- 上一轮调研 1.1 的一处表述需要按本文 1.4 理解：MP3 的 TXXX 只有描述白名单内的键到达 `setAudioTag`。
- `TestCacheVersionKnownExtensions` 的扩展名清单不含 `.dsf`、`.dff`（`version_test.go:10`），而 `formatVersions` 含这两项（`version.go:23-24`）；仅记录现状。
- `scan_feedback` 的曲目变化摘要只列了固定的列（`scan_feedback_snapshot.go:67-70`）；不在清单里的列变化不会计入扫描反馈的“曲目有变化”。扫描反馈的其余实现没有逐行核对。
- `RawAtoms` 进入解析缓存 JSON，但在解析器之外仍没有读取方（上一轮调研已确认，本次复核 `scanner.go:557`、:617-660 均不引用）。
- 未运行任何测试；未修改任何代码、规范或配置；未读取 `temp/dev` 或任何真实曲库数据；未访问网络。

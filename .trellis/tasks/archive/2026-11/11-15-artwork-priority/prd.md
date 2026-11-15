# 封面来源优先级与内嵌封面解析修复

## Goal

让每张发行版本自动选中的封面符合用户意图：用户在专辑目录里明确放置的封面优先；目录归属有歧义时只用曲目自带的内嵌封面；补齐当前读不到的内嵌封面（FLAC、Ogg/Opus、WAV）；某个来源无效时继续尝试下一个来源，而不是直接失败。

## Background（已确认的现状）

- 现规则（`library-scan-grouping.md` F4）：内嵌优先，按碟序、曲序取第一首带内嵌封面的曲目，没有才找文件夹封面（`backend/internal/library/scanner.go:1014` `saveArtworkForRelease`）。
- 文件夹查找分四轮：原目录精确名 → 原目录 `front` 子串 → 附属子目录（scans/artwork/art/covers）精确名 → 附属子目录 `front` 子串（`scanner.go:1078-1190` `listFolderArtworkCandidates`）。原目录指发行目录加各曲目目录（`ingest/persist.go:240` `ArtworkDirectories`），查找不越出发行目录，Box 叶子不查父目录。
- 各格式内嵌封面现状：
  - FLAC 只解析 STREAMINFO 与 VORBIS_COMMENT，不读 PICTURE 块（`parser/flac.go:43`）。
  - FLAC 与 Ogg/Opus 都不解析 `METADATA_BLOCK_PICTURE`。单条 Vorbis 注释超过 1 MiB 时，整个文件直接报解析错误（`parser/flac.go:107`）。
  - WAV 只读 `fmt `、`LIST`、`data` 三种块，不读 `id3 ` 块（`parser/wav.go:47`）。
- ID3 APIC 不看图片类型，后一帧覆盖前一帧（`parser/mp3.go:66`）。AIFF、DSF、DFF 经 `parseID3Frames` 共用这段逻辑。M4A `covr` 同样是后者覆盖（`parser/m4a.go:560`）。
- 受管存储要求声明的 MIME 与探测出的 MIME 完全一致（`catalog/artwork/artwork.go:145`）。因此声明为 `image/jpg` 的图片，以及 M4A 类型码 0 被映射成 `application/octet-stream` 的图片，都会被拒，记 `artwork_failure`。
- 内嵌字节存在但无效时不回退文件夹封面（`scanner.go:1031` 只在没有字节时才回退）。
- 暂存时每个 `OrganizationScope` 只保留一份内嵌封面（`ingest/staging.go:38`）。scope 取值是目录或 `disc-parent:<父目录>`（`ingest/scope.go:9`）。后果：同一目录里有多张专辑（`same_dir_split`）时，只有第一张拿得到内嵌封面；同一 Box 下的多个叶子也只有第一个拿得到。
- 候选类型已经能区分目录归属是否有歧义（`ingest/organizer.go:17-23`）：
  - `same_dir_split`、`loose_album`、`loose_unknown` 表示同一目录（或注册根）里有多张专辑。
  - `format_split` 是同一专辑的不同格式。
- 封面选择规则受 `ingest.GroupingVersion` 管控，解析规则受 `parser.CommonVersion` 与各格式版本管控（`library-scan-incremental.md` 版本号规则）。

## Requirements

### R1 来源分档

按下列顺序取第一张有效封面：

| 档 | 来源 |
|---|---|
| 1 | 原目录（发行目录 → 各曲目目录）里的精确文件名，名单与顺序沿用现有 `folderArtworkNames` |
| 2 | 内嵌封面（按碟序、曲序；同一首曲目内正面封面优先，见 R2.3） |
| 3 | 原目录 `front` 子串 → 附属子目录精确名 → 附属子目录 `front` 子串 |
| 4 | 仅限 `box_leaf`：Box 父目录里的精确文件名 |

- R1.1 就近，不向上继承：除第 4 档外，不使用发行目录以外的父目录图片。
- R1.2 目录归属有歧义时只用第 2 档。歧义指候选类型为 `same_dir_split`、`loose_album`、`loose_unknown`。这种情况下不使用任何文件夹图片，没有内嵌封面就不绑定封面。（用户决定，2026-11-15）
- R1.3 某个候选无效（不安全、超限、格式或尺寸无效、读不了）时，继续尝试本档的下一个候选，再往下一档。全部找完仍没有可用封面、且途中出现过错误时，记 `artwork_unreadable` 并保留原关系，与现有语义一致。

### R2 内嵌封面解析

- R2.1 FLAC 读取 PICTURE 块（类型 6）。
- R2.2 FLAC 与 Ogg/Opus 读取 Vorbis 注释里的 `METADATA_BLOCK_PICTURE`（Base64 编码的 FLAC PICTURE 结构）。这个键的长度上限放宽到外层容器的上限：FLAC 元数据块 16 MiB，Ogg 包 16 MiB。其他注释仍然保持 1 MiB 上限。
- R2.3 同一个文件有多张图时，图片类型为正面封面（类型 3）的优先，没有则取第一张。适用于 ID3 APIC（MP3/AIFF/DSF/DFF/WAV）、FLAC PICTURE 和 `METADATA_BLOCK_PICTURE`。M4A `covr` 没有图片类型，取第一张。
- R2.4 内嵌封面的暂存去重从“每个 scope 一份”改为“每个目录 + 专辑一份”，让同一目录里的不同专辑、同一 Box 下的不同叶子都能各自保留内嵌封面。
- R2.5 WAV 读取 `id3 ` / `ID3 ` 块，复用现有 ID3 帧解析（标签、歌词、署名、封面），合并规则与 AIFF 一致：ID3 为准，`LIST/INFO` 只补缺失值。（用户同意纳入，2026-11-15）

### R3 格式判定

- R3.1 MIME 以字节探测为准，声明值只作参考，与探测结果不一致时不再拒绝。接受的格式仍然是 JPEG/PNG/GIF/WebP，大小上限仍然是 16 MiB。

### R4 生效与兼容

- R4.1 升解析器公共版本和归组版本，使下一次扫描（包括增量扫描）重新解析文件、重新选择封面。
- R4.2 不加数据库迁移，`source_type` 仍然只有 `embedded` / `folder` 两个值。重扫后会出现两类变化，需要写进更新说明：
  - 一部分发行的封面会从内嵌换成文件夹图片；
  - 歧义目录里没有内嵌封面的发行，原先显示的文件夹封面会被移除。

## Acceptance Criteria

全部用合成样本验证，不读真实音乐库。

- [ ] AC1 发行目录里同时有 `cover.jpg` 和内嵌封面：选中 `cover.jpg`，`source_type=folder`。（R1）
- [ ] AC2 只有 `frontier.jpg`（命中 `front` 子串）和内嵌封面：选中内嵌封面。（R1，第 3 档低于第 2 档）
- [ ] AC3 （R1.1）
  - 多碟布局 `A/cover.jpg` + `A/CD1/cover.jpg`：选中 `A/cover.jpg`。
  - `Artist/cover.jpg` + `Artist/AlbumA/cover.jpg`：AlbumA 选中自己目录里的图，父目录的图不被任何发行使用。
- [ ] AC4 同一目录里两张专辑（`same_dir_split`），目录里有 `cover.jpg`：（R1.2、R2.4）
  - 两张专辑各带不同的内嵌封面时，各用自己的内嵌封面；
  - 其中一张没有内嵌封面时，那一张不绑定封面，`cover.jpg` 不被使用。
- [ ] AC5 （R1 第 4 档、R2.4）
  - Box 的两个叶子各带不同的内嵌封面：各用各的。
  - 某个叶子既没有自己的封面也没有内嵌封面，而 Box 父目录里有 `cover.jpg`：使用父目录的图。
- [ ] AC6 （R1.3）
  - 精确名图片损坏，但存在内嵌封面：选中内嵌封面，不记失败。
  - 内嵌封面无效，目录里有 `scans/front.jpg`：选中 `scans/front.jpg`。
- [ ] AC7 以下三种都能取到封面：带 PICTURE 块的 FLAC、带 `METADATA_BLOCK_PICTURE` 的 FLAC、带大于 1 MiB 的 `METADATA_BLOCK_PICTURE` 的 Opus。（R2.1、R2.2）
- [ ] AC8 （R2.3）
  - MP3 有两帧，先是封底（类型 4）后是封面（类型 3）：取封面。
  - 两帧都是类型 0：取第一帧。
  - FLAC 有两个 PICTURE 块：同样的规则。
- [ ] AC9 带 `id3 ` 块的 WAV 能读到标题和封面；ID3 与 INFO 都有标题时用 ID3 的，只有 INFO 有的字段保留。（R2.5）
- [ ] AC10 APIC 声明 `image/jpg`、实际字节是 JPEG：保存成功，`mime_type=image/jpeg`。M4A 类型码 0 的 JPEG `covr` 同样能保存。（R3.1）
- [ ] AC11 对已有库做一次增量扫描：上述规则对没有改动过的文件同样生效。（R4.1）

## Out of Scope

- 多张封面（封底、书册、碟面）的存储与展示，每个发行仍只存一张。
- 新配置项（如封面文件名白名单），以及管理员手动指定封面。
- 网络封面获取，BMP/TIFF 等新格式。
- 修改 `front` 子串匹配本身，这次只调低它的档位。
- 把 Ogg 包上限提高到 16 MiB 以上。

## Decision Record

- 规划决策路线：(a) 由用户审批最终规划摘要。选择人：qlqq。
- 已定事项：
  - 采用文件夹优先的分档方案；
  - 遵循就近原则，父目录图片不向下继承；
  - 歧义目录只用内嵌封面，不用文件夹图片兜底；
  - Box 叶子用父目录图片作最后一档兜底；
  - WAV `id3 ` 块纳入本任务。

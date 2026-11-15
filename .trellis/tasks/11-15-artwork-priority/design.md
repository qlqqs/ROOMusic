# 技术设计：封面来源优先级与内嵌封面解析

需求见 `prd.md`。本文只写边界、契约和取舍。

## 1 边界

| 层 | 包 | 改动 |
|---|---|---|
| 解析 | `backend/internal/library/parser` | FLAC PICTURE、`METADATA_BLOCK_PICTURE`、APIC 图片类型、M4A 取首张、WAV `id3 ` 块、`CommonVersion` 升级 |
| 暂存 | `backend/internal/catalog/ingest` | 内嵌封面去重键改为“目录 + 专辑”；`GroupingVersion` 升级；按碟序列出全部内嵌候选 |
| 选择 | `backend/internal/library`（`scanner.go`、`candidate_signature.go`） | 分档选择、歧义候选只用内嵌、Box 父目录兜底、逐档回退；签名列举与选择共用一个入口 |
| 存储 | `backend/internal/catalog/artwork` | MIME 以探测为准，`Save` / `SaveWithFeedback` 去掉 `mimeType` 参数 |

不改 REST、前端、数据库结构。

## 2 解析层

### 2.1 多图选择

`AudioObservation` 增加一个不导出字段 `artworkFront bool`。它不进 JSON，解析缓存本来也会丢掉图片字节。新增统一入口：

```go
// offerArtwork 按“正面封面优先，否则先到先得”接收一张候选图；pictureType<0 表示格式不带类型。
func offerArtwork(o *AudioObservation, data []byte, mime string, pictureType int)
```

规则：
- 当前没有图：接收。
- 当前图不是正面封面、新图 `pictureType==3`：替换。
- 其他情况：保留当前图。

空数据和超过 16 MiB 的数据不接收。APIC、FLAC PICTURE、`METADATA_BLOCK_PICTURE`、M4A `covr`、APEv2 都经这个入口。M4A 和 APEv2 传 `-1`，结果就是取第一张。

### 2.2 FLAC PICTURE 结构

新增 `parseFLACPicture(b []byte) (pictureType int, mime string, data []byte, ok bool)`。字段按大端顺序：

- 类型 u32
- MIME 长度 u32，MIME
- 描述长度 u32，描述
- 宽、高、色深、色数，各 u32
- 数据长度 u32，数据

每个长度都要先检查不越界，越界返回 `ok=false`。MIME 只用来传递声明值，最终格式由存储层探测决定（见第 4 节）。

用到它的两个地方：
- `parseFLAC` 遇到 `case 6` 时调用。
- `parseVorbisComments` 遇到 `METADATA_BLOCK_PICTURE` 键（不区分大小写）时，先做标准 Base64 解码，再调用。

### 2.3 Vorbis 注释长度

单条注释的上限保持 1 MiB。超过 1 MiB 的条目，先读出 `=` 之前的键名：

- 是 `METADATA_BLOCK_PICTURE`：允许读到外层块的剩余长度。外层上限已经有了：FLAC 块 16 MiB（`flac.go:36`），Ogg 包 16 MiB（`ogg.go` 第 53 行附近）。
- 不是：照旧报 `invalid Vorbis comment`。

`METADATA_BLOCK_PICTURE` 不交给 `setAudioTag`，避免把 Base64 原文当成普通标签。图片结构不合法时静默忽略这一条，与其他畸形标签的处理一致。

### 2.4 WAV `id3 ` 块

`parseWAV` 增加对 `id3 ` / `ID3 ` 块的处理。块体就是一个完整的 ID3v2 标签，复用 AIFF 的 `parseAIFFID3`（可改名为 `parseID3Chunk`），块大小上限与 AIFF 相同。

合并规则与 AIFF 一致：ID3 为准，`LIST/INFO` 只补缺失值。块在文件里的顺序不固定，所以 INFO 先解析到临时观察，循环结束后再补进主观察。

### 2.5 版本

`parser.CommonVersion` 从 2 升到 3。涉及的格式几乎覆盖全部，统一升公共版本，不逐格式升。

## 3 暂存去重（R2.4）

`SourceObservation` 增加 `ArtworkScope string`，JSON 键为 `artwork_scope,omitempty`，按字母序排在 `ArtworkRelativePath` 之后。它只属于内部观察合同，不进 REST。

`StageObservation` 计算去重键：

```
loc = locateObservation(o)
album = albumEvidence(o)
if loc.dir == "" && album == ""  → key = "path:" + NormalizedPath(o.RelativePath)   // 与旧 root-source 一样，逐文件
else                             → key = "dir:" + loc.dir + "\x00" + album
```

`loc.dir` 对碟目录取碟目录本身，不取父目录。查重 SQL 在现有 `organization_scope` 条件上再加 `observation->>'artwork_scope'=$4`，继续走现有索引前缀。

结果：
- 同一目录里的不同专辑、同一 Box 下的不同叶子，各保留一份。
- 严格多碟专辑每张碟保留一份，数量以碟数为上限。
- 普通目录里没有专辑标签的文件仍然只保留一份。

接受的边界：`same_dir_split` 里没有专辑标签、被逐文件拆开的散曲（`|@<path>` 分区）共享 `dir + ""` 这个键，只有第一首保留内嵌封面。这类散曲本来就没有可靠的专辑归属，不为它们放开内存上限。

`ingest.OrganizedCandidate` 增加 `EmbeddedArtworks() []SourceObservation`，按碟序、曲序列出全部带图（有字节或 deferred）的观察。`EmbeddedArtwork()` 保留，改为返回其中第一个。

`GroupingVersion` 从 1 升到 2。

## 4 存储层（R3）

`describe` 不再比较声明的 MIME，直接使用 `ValidateImage` 探测出的 MIME。`Save` 和 `SaveWithFeedback` 去掉 `mimeType` 参数，唯一的调用方是 `scanner.go`。`ValidateImage` 是导出函数，供选择层在调用 Save 之前判断候选是否有效。

## 5 选择层（R1）

### 5.1 统一候选列举

把 `listFolderArtworkCandidates(rootPath, directories, filter)` 改为：

```go
type artworkTier int // 1 精确名；3 front/附属子目录；4 Box 父目录
func listFolderArtworkCandidates(root registeredRoot, candidate ingest.OrganizedCandidate) iter.Seq2[folderArtworkCandidate, error]
// folderArtworkCandidate{RelativePath string; Tier artworkTier}
```

- 歧义候选（`same_dir_split`、`loose_album`、`loose_unknown`）：什么都不产出。
- 第 1 档：原目录精确名。
- 第 3 档：原目录 `front` 子串 → 附属子目录精确名 → 附属子目录 `front` 子串，即现有第二到第四轮，顺序不变。
- 第 4 档：仅 `box_leaf`，并且 Box 父目录 `path.Dir(Anchor.Scope)` 不是注册根时，列出父目录里的精确名。父目录是注册根时不兜底，因为注册根里的图片归属无法判断。

迭代是惰性的，各档顺序不变。候选签名（`candidate_signature.go`）照旧消费全部条目，于是 Box 父目录里的封面变化也会让签名变化。

### 5.2 选择流程

```
firstErr = nil
for c in 第 1 档候选: try(c)                    → 成功即保存为 folder
for o in candidate.EmbeddedArtworks():         → 成功即保存为 embedded
    bytes = o.Artwork，deferred 时现读（读失败记 artwork_unreadable，与现状一致，然后继续）
    ValidateImage 失败 → 记入 firstErr，继续
for c in 第 3、4 档候选: try(c)                 → 成功即保存为 folder
无图：firstErr != nil → 记 artwork_unreadable、保留原关系；否则 Remove
```

- `try(c)` 复用 `readFolderArtworkFile`：安全检查、16 MiB 上限、`ValidateImage`。
- 存储、绑定、清理失败仍包装成 `errArtworkBinding` 向上返回，语义不变。
- 第 1 档命中时不读 deferred 内嵌封面，`artwork_deferred_loads` 计数随之减少，这是预期行为。

## 6 兼容与回滚

- 下次扫描时，解析缓存因版本号变化全部失效，所有候选都会重写一次，这是一次性的扫描时长成本。
- 无迁移。回滚就是还原提交；回滚后版本号不同，会再触发一次全量重解析，数据仍然正确。
- 有意改变的既有行为需要同步修改规范和测试：
  - `library-scan-grouping.md` F4 的“内嵌优先”“Box 叶子不查父目录”；
  - `library-scan-verification.md` 第 5 节示例与第 6 节必需测试里的“Box 叶子不取父目录”“内嵌优先”；
  - `library-scan-incremental.md` 的去重描述、版本号和签名第 4 项；
  - `library-scan-pipeline.md` 的格式说明（加 WAV `id3 `）。

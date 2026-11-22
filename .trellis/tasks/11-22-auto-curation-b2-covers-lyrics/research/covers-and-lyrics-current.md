# Research: 封面与歌词的现状（B2 设计依赖的事实）

- **Query**: B2“外部封面与歌词”设计所依赖的十二项现状事实（封面表与存储、扫描选封面、封面读取、校验与存储函数、歌词表、歌词读取、歌词解析、管家的封面清单、egress 与 Requester、数据目录与备份、曲目有效值、迁移版本与接口样本）
- **Scope**: internal（只读代码与合同文档；没有做外部检索，Cover Art Archive 与 LRCLIB 的接口行为未对真实服务核实）
- **Date**: 2026-11-22

路径都相对仓库根目录；`文件:行号` 以当前 `main`（`bf1527c8`）为准。

---

## 0. 速查

| 项 | 结论 |
| --- | --- |
| 封面表 | 只有 `release_artworks` 一张，主键 `release_id`（一发行一张），`source_type IN ('embedded','folder')` |
| 封面文件 | `<数据目录>/artwork/<sha256 十六进制>.<jpeg\|png\|gif\|webp>`，平铺，没有缩略图与多尺寸 |
| 封面校验 | 不超过 16 MiB、文件头识别四种格式、从头部读出的宽高大于 0；**没有像素上限，没有完整解码** |
| 扫描删除封面 | `library/scanner.go:1031`、`:1036`，SQL 在 `catalog/artwork/artwork.go:209` 与 `scan_feedback.go:72` |
| 封面读取点 | 摘要全部经 `releasesql` 的两个片段；二进制经 `artwork.Lookup`；扫描反馈快照另有一处子查询 |
| 歌词存放 | `tracks.lyrics`、`tracks.lyrics_source` 两列；没有单独的歌词表 |
| 歌词覆盖 | `catalog/ingest/persist_track.go:76-82` 的 INSERT 与 UPDATE 都写这两列，没有歌词写 NULL |
| 接口里的 `source: "external"` | **已被占用**，现在的含义是同名外置 `.lrc`（`lyrics_source = 'lrc_file'`） |
| 管家的封面清单 | `steward/web/musicbrainz.go:121-175`，自己建 egress 客户端，不经限速器、不缓存 |
| `sources.Requester` | 只有 `GetJSON`；不跟随重定向；读取上限 2/8 MiB；缓存体是 JSON 里的字符串 |
| 最大迁移版本 | `0060_curation_sources` |

---

## 1. 封面的表、约束与磁盘布局

### 表定义

全部定义只有两处，之后没有迁移再改这张表（`grep -i artwork backend/migrations` 只命中 0005 与 0011）。

`backend/migrations/0005_release_artwork.sql:1-10`：

```sql
CREATE TABLE IF NOT EXISTS release_artworks (
    release_id UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/jpeg','image/png','image/gif','image/webp')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    storage_key TEXT NOT NULL UNIQUE,
    source_type TEXT NOT NULL CHECK (source_type IN ('embedded','folder')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`backend/migrations/0011_scan_staging_and_identity.sql:32-34` 去掉了 `storage_key` 的唯一约束，换成普通索引：

```sql
-- 内容寻址文件可被多个 Release 复用；storage_key 不是关系身份。
ALTER TABLE release_artworks DROP CONSTRAINT IF EXISTS release_artworks_storage_key_key;
CREATE INDEX IF NOT EXISTS release_artworks_storage_key_index ON release_artworks(storage_key);
```

- **一发行几张**：主键是 `release_id`，一个发行至多一行。
- **来源取值**：`source_type` 只有 `embedded`、`folder` 两种。
- 没有名为 `covers`、`artwork`、`release_covers` 的表。专辑（release group）、曲目、艺人都没有自己的封面表；专辑的封面是代表版本那一行发行的封面。
- 非测试代码里没有 `DELETE FROM releases`（`grep -iE "delete\s+from\s+(releases|release_groups|media|tracks)"` 只命中 `catalog/ingest/persist.go:130` 的 `media`），所以 `ON DELETE CASCADE` 在生产路径上不触发。

### 磁盘布局

- 根目录：`<数据目录>/artwork`（`backend/internal/catalog/artwork/artwork.go:34-36`）。
- 文件名就是 `storage_key`：`<内容 SHA-256 的十六进制>.<扩展名>`，扩展名由 MIME 去掉 `image/` 得到，即 `jpeg`、`png`、`gif`、`webp`（`artwork.go:143-144`）。
- 平铺在一个目录里，不按前缀分子目录。目录权限 `0750`、文件 `0640`（`artwork.go:161`、`:186`）。
- 写入方式：同目录临时文件 `.artwork-*` 写完后 `rename`；目标已存在且字节相同时不重写（`artwork.go:158-204`）。
- **没有缩略图，没有多尺寸**：后端没有任何图片解码或缩放代码（`grep -i "image\.Decode\|thumbnail\|resize"` 在非测试代码里只命中管家清单的 `thumbnails` 字段）。

---

## 2. 扫描怎样选、校验、写入与删除本地封面

### 调用链

`persistOneCandidate`（`backend/internal/library/scanner.go:988-1003`）在候选的目录写入事务提交之后调用 `saveArtworkForRelease`（`:1020-1039`），后者先 `selectArtwork`（`:1042-1100`）取字节，再交给 `artwork.Store`。封面失败包装成 `errArtworkBinding`（`:999-1001`），诊断种类 `artwork_failure`（`:704-707`）。

### 选择顺序

`selectArtwork`（`scanner.go:1042-1100`）按档取第一张有效的：

1. 文件夹里的精确名（`artworkTierExact`）：`folderArtworkNames`（`:1127`）= `cover.jpg`、`cover.jpeg`、`cover.png`、`cover.webp`、`folder.jpg`、`folder.jpeg`、`folder.png`、`front.jpg`、`front.jpeg`、`front.png`，按小写匹配。
2. 内嵌封面：`candidate.EmbeddedArtworks()`（`backend/internal/catalog/ingest/persist.go:230-239`）按碟序、曲序逐个尝试；同一文件内正面封面（图片类型 3）优先（`backend/internal/library/parser/artwork.go:4-15`）。缓存命中的音频封面是延迟读取的，由 `readDeferredArtwork`（`scanner.go:1103-1124`）重新解析原音频。
3. 附属图片（`artworkTierSupplementary`）：原目录里名字含 `front` 的图片，再是直接子目录 `scans`、`artwork`、`art`、`covers`（`:1130`）里的精确名与含 `front` 的图片。
4. Box 父目录的精确名（`artworkTierBoxParent`，仅 `candidate.BoxParentArtwork` 成立时，`:1183-1196`）。

歧义候选（`same_dir_split`、`loose_album`、`loose_unknown`）不列举任何文件夹图片（`:1175-1178`），只用内嵌封面。查找的目录来自 `candidate.ArtworkDirectories()`（`persist.go:253-264`）。

### 校验

- 文件夹图片：`readFolderArtworkFile`（`scanner.go:1302-1318`）要求 `Lstat` 不是符号链接、是普通文件、大小在 1 字节到 16 MiB 之间，读出后过 `artwork.ValidateImage`。
- 内嵌图片：解析器在 `offerArtwork` 里先挡掉空的与超过 16 MiB 的（`parser/artwork.go:5`），`selectArtwork` 再过 `artwork.ValidateImage`（`scanner.go:1064`）。
- `artwork.ValidateImage`（`artwork.go:121-134`）只做三件事：
  - 字节数不超过 `16<<20`（`:122`）；
  - `SniffMIME` 按文件头识别 jpeg、png、gif、webp（`:297-311`），识别不了就拒绝；声明的 MIME 不参与；
  - `dimensions` 从头部字段读宽高（`:239-294`），任一不大于 0 就拒绝。
- **没有像素上限**，**没有完整解码**，也不检查文件是否截断在头部之后。宽高的上界只在别处出现：列类型是 `INTEGER`；读取投影拒绝超过 `2^31-1` 的值（`backend/internal/catalog/read.go:551-554`）；前端解码器拒绝超过 100,000 的值（`frontend/src/features/catalog/api/decoders.ts:19-24`）。

### 写入

- 带扫描反馈时走 `SaveWithFeedback`（`backend/internal/catalog/artwork/scan_feedback.go:23-41`）：先 `prepare`（校验加落盘，`artwork.go:149-156`），再在一个短事务里写关联并记反馈（`bindWithFeedback`，`scan_feedback.go:55-91`），提交后清理不再被引用的旧文件。
- 不带反馈时走 `Save`（`artwork.go:84-112`）。
- 两处的 SQL 相同，都是按 `release_id` 的 upsert（`artwork.go:97`、`scan_feedback.go:74-77`）：

```sql
INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
VALUES($1::uuid,$2,$3,$4,$5,$6,$7)
ON CONFLICT(release_id) DO UPDATE SET content_hash=EXCLUDED.content_hash, mime_type=EXCLUDED.mime_type,
  width=EXCLUDED.width, height=EXCLUDED.height, storage_key=EXCLUDED.storage_key, source_type=EXCLUDED.source_type
```

### “选不出本地封面时删除关联”的确切位置

`backend/internal/library/scanner.go:1029-1038`：

```go
if len(feedbacks) > 0 && feedbacks[0] != nil {
    if len(data) == 0 {
        return store.RemoveWithFeedback(ctx, executor, releaseID, feedbacks[0])   // :1031
    }
    return store.SaveWithFeedback(ctx, executor, releaseID, data, sourceType, feedbacks[0])
}
if len(data) == 0 {
    return store.Remove(ctx, executor, releaseID)                                 // :1036
}
return store.Save(ctx, executor, releaseID, data, sourceType)
```

对应的 SQL：

- `RemoveWithFeedback` → `bindWithFeedback(prepared == nil)`，`scan_feedback.go:72`：`DELETE FROM release_artworks WHERE release_id=$1::uuid`
- `Remove`，`artwork.go:209`：`DELETE FROM release_artworks WHERE release_id=$1::uuid RETURNING storage_key`

触发条件与不触发条件：

- **删除**：`selectArtwork` 返回 `nil, "", nil`，即没有任何候选、也没有任何读取或校验错误（`scanner.go:1096-1099`）。
- **保留原关联**：有候选但全部不可读或无效时，返回 `errArtworkSource`，只记诊断 `artwork_unreadable`，不删不改（`scanner.go:1022-1024`）。
- **不经过这段代码**：增量扫描里签名未变的候选在 `scanner.go:793-808` 直接 `continue`，不调用 `persistOneCandidate`，关联原样保留。候选签名包含文件夹封面候选的路径、大小与修改时间（`backend/internal/library/candidate_signature.go:60-67`）。全量扫描每个候选都会走到上面的分支。

### 孤儿文件清理

- 只有“按引用清理”，没有周期性的全目录清扫。`removeUnreferencedArtwork`（`artwork.go:224-236`）在替换、删除关联、写关联失败三种情况下被调用；`SaveWithFeedback`/`RemoveWithFeedback` 经 `cleanupArtwork`（`scan_feedback.go:94-104`）在另一个事务里做同一件事。
- 引用计数的 SQL 只看这一张表（`artwork.go:226`）：

```sql
SELECT COUNT(*) FROM release_artworks WHERE storage_key=$1
```

  计数为 0 就 `os.Remove` 文件。别的表引用同一个 `storage_key` 时，这条语句看不到。
- 清理失败向上返回（包装成 `remove replaced artwork` / `remove released artwork` / `clean artwork`），扫描把它当作封面处理失败。

### 扫描反馈里的封面

扫描反馈的前后快照把 `storage_key` 当作发行的封面指纹（`backend/internal/catalog/ingest/scan_feedback_snapshot.go:79`）：

```sql
COALESCE((SELECT storage_key FROM release_artworks a WHERE a.release_id=r.id),'')
```

前后不同就记 `ChangeArtwork`（`backend/internal/catalog/ingest/scan_feedback_changes.go:16-19`）。`artwork.ScanFeedback` 接口在 `scan_feedback.go:17-20`，实现在 `ingest/scan_feedback.go:163-184`。

---

## 3. 封面的读取路径

### 二进制接口

- 路由：`GET /api/v1/artworks/{id}`（`backend/internal/catalog/httpapi/handlers.go:85`），处理函数 `artworkResource`（`:300-327`）。
- `{id}` 是 `storage_key`（内容哈希加扩展名）。**没有尺寸参数**，没有按发行 ID 取封面的接口。
- 顺序：未登录 401 → `artwork.Lookup`（只查库，`artwork.go:47-56`）找不到或键不安全 404 → `If-None-Match` 命中 304（不读文件）→ `artwork.Read`（`artwork.go:59-64`）读不到 404 → 200。
- `Lookup` 的 SQL（`artwork.go:49`）：`SELECT storage_key,mime_type FROM release_artworks WHERE storage_key=$1`。只有这张表里有行的键才读得到。
- 缓存头（`setArtworkCacheHeaders`，`handlers.go:330-333`）：`Cache-Control: private, no-cache`，`ETag: "<storage_key>"`；200 与 304 相同。`Content-Type` 取库里的 `mime_type`。ETag 的比较在 `backend/internal/platform/httpserver/etag.go:9-21`（弱比较）。
- `Handlers` 自己用数据目录构造一个 `artwork.Store`（`handlers.go:46`）；数据目录由 `catalogapi.New(database, guard, settings.DataDirectory, …)` 传入（`backend/internal/app/app.go:157`）。

### 摘要里的封面字段

所有发行摘要的封面都来自 `backend/internal/catalog/internal/releasesql/release.go` 的两个片段：

- 投影（`:28-31`）：`release_artwork.storage_key, release_artwork.mime_type, release_artwork.width, release_artwork.height`
- 连接（`:48-49`）：`LEFT JOIN release_artworks release_artwork ON release_artwork.release_id = releases.id`

投影被拼进 `ReleaseSummaryProjection`（`release.go:34-45`）与 `BatchSummaryProjection`（`summary_batch.go:19-22`）；连接以 `ReleaseSummaryArtworkJoin` 导出，并被拼进 `BatchSummaryJoins`（`summary_batch.go:24-25`）。四列由 `catalog.ReleaseSummaryColumns`（`backend/internal/catalog/read.go:477-525`）统一扫描，`releaseArtworkFromProjection`（`read.go:527-561`）校验：四列要么全空（`artwork: null`）要么全有；键非空、不超过 255 字节、不含分隔符与控制字符；MIME 属于四种；宽高为正且不超过 `2^31-1`。校验不过时整条摘要读取报错。

全部读取点（用到上述连接的语句）：

| 位置 | 函数 | 用在哪里 |
| --- | --- | --- |
| `catalog/read.go:102` | `listReleasesLegacy` | 发行列表 |
| `catalog/read.go:172` | `LoadReleaseSummary` | 发行详情 |
| `catalog/groups.go:74` | `listReleaseGroupsLegacy` | 专辑列表（代表版本） |
| `catalog/groups.go:103`、`:113` | `LoadReleaseGroup` | 专辑详情与成员版本 |
| `catalog/recent_additions.go:29` | `ListRecentlyAddedReleaseGroups` | 最近添加 |
| `catalog/scan_added_albums.go:92` | `loadScanAddedGroupSummaries` | 扫描报告的新增专辑 |
| `catalog/search_added_at.go:14` | `searchGroupsByAddedAt` | 搜索结果按添加时间排序 |
| `catalog/search_complete.go:58`、`:79` | `loadSearchGroups`、`loadSearchReleases` | 搜索结果 |
| `catalog/track_placement.go:84-85` | `LoadTrackPlacements` | 按曲目找专辑（最近播放、渲染） |
| `catalog/track_summary.go:48-50` | `LoadTrackSummaries`（经 `BatchSummaryJoins`） | 曲目摘要：队列、播放历史、歌单、搜索的曲目、艺人曲目、管家工具 |
| `catalog/personal_reads.go:88-90` | `LoadTrackFacts`（经 `BatchSummaryJoins`） | 个人数据（收藏、歌单封面拼图） |
| `catalog/governance/claims.go:93-95` | `LoadClaimSuggestions` | 认领建议 |
| `catalog/governance/issue_read.go:140-144`、`:296-298` | `ListIssues`、`LoadReleaseIssues` | 待处理问题 |

`LoadTrackSummaries` 的下游：`zones/service.go:225`（队列）、`listening/history.go:85`、`personal/projection.go:70`、`catalog/search_complete.go:18`、`catalog/artists_collections.go:178`、`catalog/httpapi/track_summaries.go:28`（`POST /api/v1/tracks/summaries`）、`catalog/stewardtools/details.go:87`。`LoadTrackPlacements` 的下游：`listening/recent.go:55`、`app/renderer.go:89`。歌单的 `cover_groups[].artwork` 取自代表版本的摘要（`backend/internal/personal/playlist_reads.go:300`）。

除这两个片段、`artwork.Lookup` 与扫描反馈快照（第 2 节）之外，非测试代码里没有别处读 `release_artworks`。艺人的摘要与详情没有封面字段。

### 前端拿到的字段

- JSON 形状（`backend/internal/catalog/projection.go:63-68`）：`artwork: null | { resource_id, mime, width, height }`，挂在 `ReleaseSummary.Artwork`（`projection.go:20`）上。`resource_id` 就是 `storage_key`。没有来源字段。
- 前端解码器 `artworkDecoder`（`frontend/src/features/catalog/api/decoders.ts:19-24`）：`resource_id` 1–255 个字符，`mime` 1–100，`width`、`height` 1–100,000。
- 地址：`artworkPath(resourceId)` → `/api/v1/artworks/<encodeURIComponent(resourceId)>`（`frontend/src/features/catalog/api/endpoints.ts:94-96`）。
- 组件 `Cover`（`frontend/src/features/catalog/components/cover.tsx:23-36`）以 `resource_id` 作 React key；播放器的曲目带 `artworkId = release.artwork?.resource_id ?? null`（`frontend/src/features/player/engine/tracks.ts:17`、`:53`）。

---

## 4. 校验与存储函数的签名，以及包依赖规则

包：`backend/internal/catalog/artwork`（`artwork.go`、`scan_feedback.go`）。它只导入标准库。

### 导出的

```go
type Executor interface {                                                    // artwork.go:20-23
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Queryer interface { QueryRowContext(context.Context, string, ...any) *sql.Row }   // :26-28
type TxBeginner interface { BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) } // scan_feedback.go:12-14
type ScanFeedback interface {                                                // scan_feedback.go:17-20
    CaptureArtworkBefore(context.Context, *sql.Tx, string) error
    CaptureArtworkAfter(context.Context, *sql.Tx, string) error
}
type Store struct{ /* directory */ }                                         // artwork.go:31
type Resource struct { ID, MIMEType string }                                 // :40-43

func NewStore(dataDirectory string) *Store                                   // :34
func (s *Store) Lookup(ctx, database Queryer, resourceID string) (Resource, error)      // :47
func (s *Store) Read(resource Resource) ([]byte, error)                      // :59
func (s *Store) Save(ctx, executor Executor, releaseID string, data []byte, sourceType string) error   // :84
func (s *Store) Remove(ctx, executor Executor, releaseID string) error       // :207
func (s *Store) SaveWithFeedback(ctx, database TxBeginner, releaseID string, data []byte, sourceType string, feedback ScanFeedback) error   // scan_feedback.go:23
func (s *Store) RemoveWithFeedback(ctx, database TxBeginner, releaseID string, feedback ScanFeedback) error                              // scan_feedback.go:44
func ValidateImage(data []byte) (mimeType string, err error)                 // artwork.go:121
func SniffMIME(data []byte) string                                           // :297
```

### 没有导出的

| 名字 | 位置 | 作用 |
| --- | --- | --- |
| `(*Store).describe(data)` | `artwork.go:137-146` | 校验字节，算出哈希、键、MIME、宽高、路径；不碰文件与数据库 |
| `(*Store).prepare(data)` | `:149-156` | `describe` 加落盘 |
| `(*Store).writePrepared(data, *preparedArtwork)` | `:158-204` | 内容寻址落盘（已存在且相同则不写），记下这次是否真的写了文件 |
| `preparedArtwork` | `:114-118` | `hash, key, mimeType, path, width, height, wroteManagedFile` |
| `dimensions(data, mimeType)` | `:239-294` | 从头部读宽高 |
| `safeStorageKey(key)` | `:68-79` | 只接受字母、数字、点、下划线、连字符，非空、不超过 255 字节、不是 `.` 或 `..` |
| `removeUnreferencedArtwork(ctx, Queryer, path, key)` | `:224-236` | 按 `release_artworks` 的引用数决定是否删文件 |
| `cleanupArtwork(ctx, TxBeginner, path, key)` | `scan_feedback.go:94-104` | 在独立事务里调用上一项 |

所以“校验图片字节”有导出入口（`ValidateImage`，但它不返回宽高）；“内容寻址写入”目前只在 `Save` / `SaveWithFeedback` 内部发生，而这两个函数都同时写 `release_artworks`，`sourceType` 受该表 CHECK 约束。没有“只落盘、不写关联”的导出函数，也没有导出的“取宽高”函数。`Store` 的目录字段是私有的，根目录固定为 `<数据目录>/artwork`。

### 包依赖规则

`backend/internal/app/dependency_rules_test.go`（`TestPackageDependencyDirections`，`:136-188`）要求每个 `internal/` 下的包都在 `packageRules` 里声明；只统计非测试文件的导入；导入 `net/http` 需要 `http: true`。

| 包 | 规则 | 行 |
| --- | --- | --- |
| `catalog/artwork` | `{}`：不得导入任何内部包，不得导入 `net/http` | `:72` |
| `library` | 可导入 `catalog/artwork`（以及 `catalog`、`catalog/governance`、`catalog/ingest`、`catalog/metadata`、`library/parser`、`platform/uuid`、`platform/workpool`） | `:82` |
| `catalog/httpapi` | 可导入 `catalog/artwork`（以及 `catalog`、`catalog/governance`、`identity`、`operations`、`platform/httpserver`、`platform/uuid`） | `:116` |
| `catalog` | **不能**导入 `catalog/artwork`、`library/parser` | `:74` |
| `curation` | `sources`、`operations`、`identity`、`catalog`、`catalog/governance`、`platform/uuid`；**不含** `catalog/artwork`、`library/parser` | `:76` |
| `sources` | `platform/egress`、`platform/buildinfo`，`http: true` | `:49` |
| `sources/musicbrainz` | `sources`、`platform/uuid`；没有 `http: true` | `:50` |
| `steward/web` | `identity`、`platform/buildinfo`、`platform/uuid`、`platform/egress`、`sources`、`sources/musicbrainz`、`steward/runtime`、`steward/secret`、`steward/tool`，`http: true` | `:41` |
| `app` | 外部源相关的只列了 `curation`、`curation/httpapi`、`sources/musicbrainz` | `:120-121` |

目前导入 `catalog/artwork` 的只有 `library`（`scanner.go`）与 `catalog/httpapi`（`handlers.go`）。新增的包（例如 `sources/` 下的新子包）不声明规则时，这个测试会失败（`:153-156`）。

---

## 5. 歌词的表、约束与扫描的整体覆盖

### 存放

没有单独的歌词表。歌词是 `tracks` 上的两个可空列（`backend/migrations/0015_track_lyrics.sql:4-5`）：

- `tracks.lyrics TEXT`：规范化后的原文，LRC 与纯文本都原样存在这一列；
- `tracks.lyrics_source TEXT`：来源键。

**同步与纯文本不分开存**，也没有“是否同步”的列：形态是读取时由 `catalog.ParseLyrics` 判断的（第 7 节）。

### 约束

当前生效的是 `backend/migrations/0050_track_lyrics_file.sql:2-8` 重建后的 `tracks_lyrics_ck`：

```sql
CHECK (
    (lyrics IS NULL AND lyrics_source IS NULL)
    OR (lyrics IS NOT NULL AND lyrics <> '' AND OCTET_LENGTH(lyrics) <= 65536
        AND lyrics_source IS NOT NULL
        AND lyrics_source IN ('vorbis_lyrics', 'vorbis_unsyncedlyrics', 'mp4_lyr', 'id3_uslt', 'lrc_file'))
)
```

“五种本地来源”就是这五个键，常量在 `backend/internal/library/parser/lyrics.go:15-21`：

| 键 | 含义 |
| --- | --- |
| `vorbis_lyrics` | Vorbis 注释 `LYRICS`（FLAC、Ogg Vorbis、Opus） |
| `vorbis_unsyncedlyrics` | Vorbis 注释 `UNSYNCEDLYRICS` / `UNSYNCED LYRICS` |
| `mp4_lyr` | MP4 `©lyr` |
| `id3_uslt` | ID3v2.3/2.4 `USLT` |
| `lrc_file` | 与音频同目录同主名的外置 `.lrc`（0050 新增） |

0050 的下行迁移先把 `lrc_file` 的两列置 NULL 再恢复四值约束（`0050_track_lyrics_file.down.sql:2-9`）；它在回退往返测试里有夹具（`backend/internal/platform/postgres/migrations_rollback_integration_test.go:55`、`:292`）。

### 整体覆盖的确切位置

`backend/internal/catalog/ingest/persist_track.go` 的 `persistTrack`（`:68-` 起）：

- 新建曲目：`:76-78` 的 `INSERT INTO tracks(… lyrics, lyrics_source, …)`，参数 `nullableString(observation.Lyrics)`、`nullableString(observation.LyricsSource)`；
- 沿用曲目：`:81-82` 的 `UPDATE tracks SET … lyrics=$24, lyrics_source=$25, … WHERE id=$32::uuid`，同样的两个参数。

`nullableString` 把空白串变成 NULL（`persist.go:281-`），所以这一轮观察没有歌词时两列被写成 NULL。非测试代码里写这两列的只有这两条语句（`grep "lyrics\s*=" backend/internal` 只命中 `persist_track.go:81`）。

观察里的歌词从哪来：

- 解析器把内嵌歌词放进 `parser.AudioObservation.Lyrics`、`LyricsSource`（`backend/internal/library/parser/parser.go:48-51`）；
- 扫描器对物理音频查同名 `.lrc`（`backend/internal/library/sidecar_lyrics.go:59-93`，调用在 `scanner.go:429`），命中时在暂存前用 `parser.ApplyExternalLyrics` 覆盖（`backend/internal/library/scan_pipeline.go:169-171`）；
- `buildPhysicalSourceObservation` 把两个字段复制进 `ingest.SourceObservation`（`scanner.go:557`；字段定义在 `backend/internal/catalog/ingest/observation.go:54-55`）；CUE 虚拟轨没有歌词。

与封面相同，增量扫描里签名未变的候选不重写曲目（`scanner.go:793-808`），签名变了或全量扫描时才会走到上面的 UPDATE。

扫描反馈的曲目指纹包含这两列（`backend/internal/catalog/ingest/scan_feedback_snapshot.go:69`：`COALESCE(t.lyrics,''),COALESCE(t.lyrics_source,'')`），歌词变化会被记成曲目的元数据变化。

---

## 6. 歌词的读取路径

- 路由：`GET /api/v1/tracks/{id}/lyrics`（`backend/internal/catalog/httpapi/handlers.go:86`），处理函数 `trackLyrics`（`:253-273`）。
- 顺序：未登录 401 → 非 UUID 400 `invalid_id` → 曲目不存在或非 `present` 404 `not_found` → 读库失败 503 `database_unavailable` → 200。所有登录成员可读。
- 存储读取 `catalog.Store.LoadTrackLyrics`（`backend/internal/catalog/lyrics.go:51-73`），唯一的 SQL 在 `:56`：

```sql
SELECT id::text, lyrics, COALESCE(lyrics_source = 'lrc_file', false)
FROM tracks WHERE id=$1::uuid AND source_status='present'
```

  读到原文后调用 `ParseLyrics`；`lyrics_source = 'lrc_file'` 时把 `Source` 改成 `external`（`:66-68`）。
- 返回结构（`lyrics.go:30-46`）：

```go
type LyricsLine  struct { StartMS *int64 `json:"start_ms"`; Text string `json:"text"` }
type Lyrics      struct { Kind string `json:"kind"`; Source string `json:"source"`; Lines []LyricsLine `json:"lines"` }
type TrackLyrics struct { TrackID string `json:"track_id"`; Lyrics *Lyrics `json:"lyrics"` }
```

  `kind` 是 `synced` 或 `plain`；`source` 是 `embedded` 或 `external`（常量在 `lyrics.go:15-20`）；没有歌词是 200 加 `lyrics: null`。
- **`source: "external"` 现在的含义是本地的外置 `.lrc` 文件**，不是外部源。前端解码器同样只接受这两个值（`frontend/src/features/player/api/decoders.ts:36-41`，注释写明“external 是与音频同名的外置 .lrc 文件；面板不显示来源”），类型 `LyricsSource = "embedded" | "external"`（`:53`）。
- 前端：`fetchTrackLyrics`（`frontend/src/features/player/api/endpoints.ts:15-22`）要求响应的 `track_id` 与请求一致；`decodeTrackLyrics`（`decoders.ts:63-`）按形态检查；`useTrackLyrics`（`frontend/src/features/player/hooks/use-track-lyrics.ts:10-16`）取 `response.data.lyrics`，查询键 `playerKeys.lyrics(generation, trackId)`，按合同 `staleTime: Infinity`。
- **曲目列表里没有“有无歌词”的摘要字段**：`ReleaseTrack`（`backend/internal/catalog/projection.go:39-56`）与 `TrackSummary`（`backend/internal/catalog/track_summary.go:27-37`）都没有；后端与 `frontend/src` 里搜不到 `has_lyrics` / `hasLyrics`。
- `LoadTrackLyrics` 与 `ParseLyrics` 的调用方各只有一个（处理函数与 `LoadTrackLyrics` 自己）；管家工具不读歌词。

---

## 7. 歌词解析、规范化与大小上限

### 规范化与上限：包 `backend/internal/library/parser`（`lyrics.go`）

```go
const MaxLyricsBytes = 64 << 10                          // :24  规范化之后的上限，与列约束的 65536 一致
const MaxExternalLyricsBytes = 256 << 10                 // :27  外置 .lrc 解码前的原始字节上限
func NormalizeLyrics(raw string) (string, bool)          // :36
func DecodeExternalLyrics(data []byte) (string, bool)    // :59
func ApplyExternalLyrics(observation *AudioObservation, text string)   // :87
```

- `NormalizeLyrics`（`:36-56`）：非法 UTF-8 返回 false；去 UTF-8 BOM；`\r\n`、`\r` 改 `\n`；删除 NUL 与除 `\n`、`\t` 外的 C0 控制字符；去首尾空白；结果为空或超过 `MaxLyricsBytes` 返回 false。纯函数，不解析 LRC。
- `DecodeExternalLyrics`（`:59-84`）：空或超过 256 KiB 返回 false；按 BOM、合法 UTF-8、GB18030 的顺序解码，再经 `NormalizeLyrics`。
- `ApplyExternalLyrics`（`:87-102`）：把文本写进观察、来源键设为 `lrc_file`，并重抽歌词开头的署名；它操作的是 `parser.AudioObservation`，与扫描绑定。
- 这个包的依赖规则：`library/parser` 可导入 `catalog/metadata`、`catalog/classical`、`playback/dsdio`（`dependency_rules_test.go:80`）；能导入它的只有 `library`（`:82`）。

### LRC 解析：包 `backend/internal/catalog`（`lyrics.go`）

```go
func ParseLyrics(raw string) (Lyrics, bool)              // :92
```

- 纯函数，无依赖（只用标准库 `regexp`、`sort`、`strconv`、`strings`）。
- 至少一行有合法的行首时间戳时返回 `Kind = "synced"`（行按 `StartMS` 稳定排序，`:124-140`）；否则 `Kind = "plain"`（`:142-161`）；去掉元标签后没有可显示的行时返回 false。
- 返回值的 `Source` 固定是 `embedded`（`:138`、`:161`），由调用方改写。
- 时间戳形式 `[mm:ss]`、`[mm:ss.f|ff|fff]`、`[mm:ss:ff]`（`lyricsTimestampPattern`，`:77`）；整行元标签 `[字母或#:内容]`（`:79`），其中第一个有效的 `offset` 生效；逐字标记 `<mm:ss.xx>` 被去掉（`:81`）。
- 上限是私有常量（`:22-27`）：`maxLyricsLines = 2000`、`maxLyricsLineRunes = 1000`、`maxLyricsStartMS = 24 小时`。超出是截断或丢行，不是报错。
- 私有辅助：`splitLyricsTimestamps`（`:165`）、`parseLyricsOffset`（`:188`）、`truncateLyricsLine`（`:197`）。
- **没有导出的“这段文本是不是同步歌词”的判断函数**；现有的判断方式是 `ParseLyrics(raw)` 后看 `Kind`。
- `curation` 可以导入 `catalog`（`dependency_rules_test.go:76`），所以能调用 `ParseLyrics`；它不能导入 `library/parser`，调不到 `NormalizeLyrics` 与 `MaxLyricsBytes`。`sources` 及其子包两者都不能导入（`:49-50`）。

测试：`backend/internal/catalog/lyrics_test.go`（`TestParseLyricsSynced` `:48`、`TestParseLyricsOffset` `:80`、`TestParseLyricsPlain` `:103`、`TestParseLyricsWithNothingToShow` `:127`、`TestParseLyricsLimits` `:142`、`TestLyricsJSONShape` `:199`）。

---

## 8. 管家的 Cover Art Archive 清单

### 代码

全部在 `backend/internal/steward/web/musicbrainz.go`：

- 基础地址是包级变量：`var coverArtBaseURL = "https://coverartarchive.org"`（`:16`），测试直接改它。
- 入口：`Service.GetMusic(ctx, entity, mbid, include)`（`:61-111`）。`include` 里的 `cover-art` 只在 `entity == "release"` 时生效，是本地选项，不传给 MusicBrainz（`:82-85`）；先经适配器 `s.music.Lookup` 读元数据，成功后再调 `s.coverArt(ctx, strings.ToLower(mbid))`（`:103-109`）。封面清单失败时整个工具调用失败。
- 清单读取：`func (s *Service) coverArt(ctx context.Context, mbid string) (covers []Cover, err error)`（`:121-175`）。
  - 请求地址：`coverArtBaseURL + "/release/" + mbid + "/"`（`:122`），GET。
  - 时限：`context.WithTimeout(ctx, requestTimeout)`，`requestTimeout = 20 * time.Second`（`service.go:23`）。
  - 客户端：每次调用 `s.newClient(egress.Options{Policy: egress.PublicOnly, MaxRedirects: 5, Timeout: requestTimeout})`（`:127`），用完 `CloseIdleConnections`（`:131`）。`s.newClient` 默认是 `egress.NewClient`（`service.go:47`），是测试的注入点。
  - 重定向：把客户端自带的 `CheckRedirect` 包一层（`:132-146`）。先调原检查（协议与次数），再要求目标通过 `validatePageURL`（无用户名密码、http/https、端口为空或 80/443、长度不超过 2048，`fetch.go:35-44`）、主机是 `archive.org`、`*.archive.org` 或 `coverartarchive.org`、路径以 `/index.json` 结尾；否则返回 `source_unavailable`。通过后把请求头清空，只设 `Accept: application/json` 与 `User-Agent`。
  - 请求头：`request(ctx, GET, address, nil, musicUserAgent())`（`service.go:101-109`）设 `User-Agent`（`sources.UserAgent()`，`musicbrainz.go:25-27`）与 `Accept: application/json`。
  - 结果：404 返回空切片（`:156-158`）；非 200、JSON 读不出或 `images` 为 null 都是 `source_unavailable`（`:167-169`）；响应读取上限 `maxBodyBytes = 2 << 20`（`service.go:22`、`readJSON` `:111-120`）。
  - **不经过任何限速器，不缓存**。MusicBrainz 的每秒一次限速只在 `s.music`（适配器）里。每用户的额度 `musicMetadataPerMinute = 20`（`limiter.go:10`）是工具层的，与出站限速无关。
  - 日志：`s.logRequest(ctx, "cover_art_archive", address, …)`（`:124`），事件 `steward.web.request.finished`，只记来源、域名、耗时、条数、错误码（`service.go:85-99`）。来源键是带下划线的 `cover_art_archive`。

### 返回给模型的结构

```go
type MusicDetails struct {            // musicbrainz.go:55-58
    Metadata Metadata `json:"metadata"`
    Covers   []Cover  `json:"covers,omitempty"`
}
type Cover struct {                   // :114-119
    URL        string            `json:"url"`
    Front      bool              `json:"front"`
    Back       bool              `json:"back"`
    Thumbnails map[string]string `json:"thumbnails"`
}
```

`Cover` 由清单 `images[]` 的 `image`、`front`、`back`、`thumbnails` 四个字段直接映射（`:159-173`），不下载图片，不过滤地址。工具定义在 `tools.go:73-84`（`get_music_metadata`，`include` 枚举含 `cover-art`）。

### 测试

| 文件 | 用例 | 断言方式 |
| --- | --- | --- |
| `backend/internal/steward/web/c8_test.go:337-371` | `TestC8CoverArtRedirectOfficialOnly` | 把 `coverArtBaseURL` 改成 `http://coverartarchive.org`，本地 `httptest` 服务按 `r.Host` 回 307 或 `{"images":[]}`；五个跳转目标（`archive.org/…/index.json`、`ia800.archive.org/…/index.json` 放行；`evil.invalid`、`archive.org/image.jpg`、`archive.org.evil.invalid` 拒绝）。放行断言无错误且命中 2 次；拒绝断言错误码 `source_unavailable`（`c8Code`）且只命中 1 次。直接调私有方法 `s.coverArt(ctx, "synthetic")` |
| `backend/internal/steward/web/c8_round2_test.go:125-179` | `TestC8FixedSourceClientsPublicOnly` 的 `cover_art_archive` 子用例 | 在 `s.newClient` 里断言 `o.Policy == egress.PublicOnly`；第二轮把固定域名解析成 `127.0.0.1`，断言返回错误且服务没有再被命中 |

辅助：`c8LocalClient`（`c8_test.go:40-68`）把解析结果固定成公网地址 `8.8.8.8`、实际拨号指向本地测试服务，并在每次 `RoundTrip` 前用 `egress.CheckEndpoint` 走一遍真实的地址策略；`c8Code`（`c8_test.go:69-`）断言 `tool.Error` 的错误码；`export_test.go` 的 `C8TestClient` 给包外测试替换 `newClient`。

没有找到的用例：清单 404 得到空列表、`Cover` 四个字段的映射、带 `cover-art` 的 `get_music_metadata` 整条链路（测试文件里搜不到 `cover-art`、`thumbnails`、非空的 `images`）。

---

## 9. `platform/egress` 的 Options 与 `sources.Requester`

### egress

`backend/internal/platform/egress/client.go`：

```go
type Options struct {                                    // :28-40
    Policy       Policy          // PublicOnly（零值）| PublicOrAllowlist | Unrestricted
    Allowlist    func(host string, port int) bool        // 仅 PublicOrAllowlist 使用
    LookupNetIP  func(ctx, network, host string) ([]netip.Addr, error)
    MaxRedirects int             // 零表示不允许跟随
    Timeout      time.Duration   // 覆盖重定向与响应读取的总超时
}
func NewClient(options Options) (*http.Client, error)    // :44
func IsPublicIP(ip netip.Addr) bool                      // :132
func CheckEndpoint(ctx, client *http.Client, endpoint string) error   // :172
```

- `PublicOnly`：每次拨号前解析主机，解析结果里只要有一个非公网地址就拒绝（`addresses`，`:213-236`）；重定向后的每一跳都重新经过 `RoundTrip` → `dialContext`，同样检查。
- 重定向策略（`CheckRedirect`，`:62-70`）：只检查协议是 http/https 与 `len(via) > options.MaxRedirects`。`MaxRedirects` 为 0 时第一次重定向就返回 `&Error{Code: CodeTooManyRedirects}`，`client.Do` 以错误结束（不是把 3xx 响应交给调用方）。
- **没有“允许的重定向目标主机白名单”这个选项**。`Allowlist` 是“哪些主机端口可以绕过公网限制”，与重定向无关。现有的两处主机限制都是调用方包一层 `client.CheckRedirect`：`steward/web/musicbrainz.go:132-146`（封面清单）与 `steward/web/fetch.go:73-83`（网页读取）。
- 不读环境变量，不走代理；错误类型 `*egress.Error`，`errors.Is(err, egress.ErrDenied)` 可识别（`errors.go:6`、`:21-41`）。

### sources.Requester

`backend/internal/sources/request.go`：

```go
type ClientFactory func(egress.Options) (*http.Client, error)        // :19
type Options struct {                                                 // :21-28
    DataDirectory, BaseURL string
    Logger    *slog.Logger
    NewClient ClientFactory
    Limiter   *Limiter
    Now       func() time.Time
    After     func(time.Duration) <-chan time.Time
}
func NewRequester(d Declaration, opts Options) *Requester            // :37
func (r *Requester) Limiter() *Limiter                               // :57
func (r *Requester) StartCacheCleanup(ctx context.Context)           // :59
func UserAgent() string                                              // :77
func (r *Requester) GetJSON(ctx, address string, decode func([]byte) (empty, valid bool)) error   // :103
```

导出的请求方法只有 `GetJSON`。它的现状（与二进制下载有关的部分）：

| 方面 | 现状 | 位置 |
| --- | --- | --- |
| 请求头 | 固定 `Accept: application/json` 与 `User-Agent` | `:121-122` |
| 客户端 | 每次出站 `r.newClient(egress.Options{Policy: egress.PublicOnly, Timeout: 20s})`，**没有 `MaxRedirects`**，所以任何重定向都以 egress 错误结束 | `:206-207` |
| 重定向出错后的处理 | `errors.Is(err, egress.ErrDenied)` → 不重试，对话得 `ErrUnavailable`、后台得 `ErrTryLater` | `:237-243`、`:168-170` |
| 响应读取上限 | 对话 2 MiB、后台 8 MiB（封面校验的上限是 16 MiB） | `:108-113` |
| 处理的状态码 | 只读 200 与 404 的响应体；429、503 标记可重试；其他一律失败 | `:246-258` |
| 缓存内容 | `entry{StoredAt, ExpiresAt, Status, Body string}` 写成 JSON 文件，`Body` 是字符串 | `backend/internal/sources/cache.go:18-23` |
| 缓存键 | `"GET " + 规范化 URL` 的 SHA-256 | `cache.go:34-37` |
| 缓存清理 | 只处理 `<数据目录>/sources/cache/<源 ID>/` 下以 `.json` 结尾的普通文件 | `cache.go:135-162` |
| 限速 | `r.limiter.acquire(ctx, background)` 取许可、`release(outbound)` 归还并记下发出时刻 | `:203`、`:209`、`:214`、`:221` |
| 调用身份与放行回调 | 从 ctx 取 `caller`，调用开始与每次出站前 `c.check(ctx)` | `:104-106`、`:213` |
| 重试 | 后台至多 5 次尝试，`Retry-After` 或指数退避，单次至多 60 秒；用尽后 `cache.fail(key)` 记 10 分钟 | `:143-180`、`:189-200` |
| 日志 | `sources.request.finished`：源、域名、耗时、状态或 `request_failed`、第几次尝试 | `:223-231` |

私有、包外调不到的部分：`(*Requester).attempt`（`:202`）、`fetch`（`:235`）、`response`（`:95-100`）、`retryDelay`（`:189`）、`caller.check`（`:85`）、`cache` 的全部方法、`(*Limiter).acquire` 与 `release`（`backend/internal/sources/limiter.go:52`、`:98`）。也就是说，`sources` 包之外的代码拿到 `*Limiter` 也没法自己取许可，只能经 `Requester` 的方法出站。

按上表，用现有 `GetJSON` 做不到的事有四件：跟随重定向（连 Cover Art Archive 的 JSON 清单都会 307 到 archive.org 的 `index.json`，见第 8 节的测试）、对重定向目标主机做限制、读取超过 8 MiB 或非 JSON 的响应体、把二进制结果落到缓存之外的位置。`Requester` 目前没有这些的钩子或参数。

### 限速器与“共用”

`backend/internal/sources/limiter.go`：

```go
func NewLimiter(interval time.Duration, now func() time.Time, after func(time.Duration) <-chan time.Time) *Limiter   // :20
func DefaultLimiter(d Declaration) *Limiter                                                                          // :35
```

- `DefaultLimiter` 按 `d.ID` 在进程内的全局表里取或建（`:30-44`），间隔取第一次创建时的 `d.MinInterval`。两个声明的 `ID` 不同就是两个限速器。
- `NewRequester` 在 `opts.Limiter == nil` 时用 `DefaultLimiter(d)`（`request.go:38-40`）；也可以经 `Options.Limiter` 传入别的实例。MusicBrainz 适配器把自己的限速器经 `(*musicbrainz.Client).Limiter()` 暴露出来（`backend/internal/sources/musicbrainz/client.go:36`），声明是 `musicbrainz.Declaration()`（`client.go:17-19`，`ID: "musicbrainz"`、`MinInterval: time.Second`）。
- 对话调用排队超过 10 秒返回 `ErrRateLimited`；有对话在等时后台不取许可（`limiter.go:52-96`）。

### 能力与闸门的现状

- `sources.Capability` 只有三个常量：`release_lookup`、`recording_lookup`、`artist_lookup`（`backend/internal/sources/sources.go:12-16`）；能力接口只有 `ReleaseFinder`、`RecordingFinder`、`ArtistFinder`（`:25-42`）。没有封面与歌词的能力接口。
- `curation.SourceRegistration` 只有 `Declaration`、`Releases`、`Recordings`、`Artists` 四个字段（`backend/internal/curation/sources.go:12-17`）；闸门的取能力方法只有 `ReleaseFinder`、`RecordingFinder`、`ArtistFinder`（`backend/internal/curation/source_finders.go:25`、`:75`、`:109`），每个方法经 `guardedFinders.background`（`:15-21`）先 `Check` 再 `sources.WithBackground(ctx, permit)`。
- `Sources.Check`（`curation/sources.go:118-`）与 `Sources.List` / `SetEnabled`（`:71`、`:92`）按注册的声明工作；源开关表 `curation_source_settings.source` 与 `external_links.source` 的形状约束都是 `^[a-z0-9._-]{1,64}$`（`backend/migrations/0060_curation_sources.sql:2`、`:10`）。
- 受治理操作的类型约束 `operations_type_ck` 目前的取值（`backend/migrations/0058_auto_curation.sql:76-78`）：`release_correction`、`release_auto_correction`、`release_group_change`、`release_group_default`、`release_issue_review`、`release_claim`、`revert`、`artist_merge`、`artist_split`、`artist_rename`、`artist_candidate_ignore`。`release_auto_values.field` 只接受八个发行字段（`0058:10`），不含封面与歌词。

---

## 10. 数据目录、装配与备份

### 数据目录下的子目录

数据目录来自 `ROOMUSIC_DATA_DIR`，缺省 `./data`（`backend/internal/platform/config/config.go:44-47`）。代码里用到的子路径：

| 路径 | 内容 | 位置 |
| --- | --- | --- |
| `<数据目录>/artwork/` | 受管封面，内容寻址 | `backend/internal/catalog/artwork/artwork.go:35` |
| `<数据目录>/sources/cache/<源 ID>/<键前两位>/<键>.json` | 外部源的响应缓存 | `backend/internal/sources/request.go:50-53`、`cache.go:39` |
| `<数据目录>/steward/instance.key` | 管家的实例密钥 | `backend/internal/app/steward.go:86` |
| `<数据目录>/media-cache/` | 旧版派生缓存，启动时删除 | `backend/internal/app/media_cache.go:16-63` |

除此之外没有别的子目录（DSP 的脉冲响应文件存在数据库的 `bytea` 列里，`backend/migrations/0048_dsp_ir_files.sql:12-13`）。

### `app.go` 的装配

`backend/internal/app/app.go`（函数 `New`，`:95` 起）：

- `:115` `library.NewService(lifecycle, database, settings.DataDirectory, logger)`：扫描服务保存数据目录（`backend/internal/library/service.go:16`、`:33-37`），每次保存封面时 `artwork.NewStore(service.dataDirectory)`（`scanner.go:1028`）。
- `:147-150` `music := musicbrainz.New(musicbrainz.Options{DataDirectory: settings.DataDirectory, Logger: logger})`；`lifecycle != nil` 时 `music.StartCacheCleanup(lifecycle)`。
- `:151` `sourceGate := curation.NewSources(database, curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: music, Recordings: music, Artists: music})`。
- `:152` `curationWriter := curation.NewWriter(database)`；`:153-154` 的 `operationKinds` 列出全部受治理操作类型（首项 `governance.AutoCorrectionOperations(curationWriter)`）。
- `:157` `catalogapi.New(database, guard, settings.DataDirectory, logger, …)`：封面的 HTTP 读取。
- `:161` `curationapi.New(curationWriter, curation.NewReverter(database, operationKinds...), sourceGate, guard, logger)`。
- `:174` `registerSteward(…, settings.DataDirectory, …, stewardConfig{music: music, …})`；在 `steward.go:87-100` 里 `web.New(…)` 后 `webService.WithMusic(configuration.music)`，管家与后台共用同一个适配器实例。

`artwork.Store` 没有在 `app.go` 里集中构造：扫描与目录接口各自 `NewStore(数据目录)`，指向同一个目录。

### 备份

- 备份是数据库的 `pg_dump --format=custom`（`backend/internal/backup/service.go:262`），产物是备份目录里的 `roomusic-*.dump`（`backend/internal/backup/files.go:19`）。
- **备份不含数据目录**：部署合同写明“不备份 `/data` 或音乐文件”（`.trellis/spec/backend/deployment-environment-contracts.md:39`）；ADR 0006 的约束表同样写“备份不含数据目录”（`docs/architecture/adr/0006-auto-curation-and-sources.md:54`），默认 7 写“外部下载的封面、指纹与查询缓存放在数据目录，可以重新获取，不进备份”（`:36`、`:214`）。
- 由此的现状：`release_artworks` 的行随数据库备份，封面文件不随。库里有行而文件不在时，摘要仍返回 `artwork` 对象，二进制接口返回 404（`handlers.go:318-322`）；本地封面在下一次会重写该候选的扫描里重建。
- 数据目录格式变化的约定：`.trellis/spec/backend/database-guidelines.md:238`（“封面（`<DATA_DIR>/artwork/`）……换新的版本子目录或新文件名，不原地改写旧版本会读的文件”）。

---

## 11. 曲目的标题、艺人、专辑名、时长的“有效值”

### 各字段的来源

| LRCLIB 需要 | 库里的有效值 | 说明 |
| --- | --- | --- |
| `track_name` | `tracks.title` | 曲目没有修正层与自动层（搜不到 `track_corrections`、`track_auto*`、`track_effective`）；这一列就是显示值 |
| `artist_name` | `tracks.artist` | 写入时经 `metadata.CanonicalTrackArtist`（`backend/internal/catalog/ingest/persist_track.go:74`）；这是显示用的艺人串。结构化的艺人引用另由 `releasesql.SelectedTrackArtistRefsCTE` / `TrackArtistRefsProjection` 给出 |
| `album_name` | 视图 `release_effective` 的 `title` | 修正 → 自动层 → 扫描值依次取（`backend/migrations/0058_auto_curation.sql:25-` 的 `COALESCE(c_title.text_value, a_title.text_value, r.title)`）；读取代码以 `releasesql.ReleaseSource = "release_effective releases"` 引用（`backend/internal/catalog/internal/releasesql/release.go:7`）。路径：`tracks.medium_id → media.release_id → release_effective.id` |
| `duration` | 视图 `track_display_durations.duration_seconds`（秒，`float8`） | 有与当前文件一致的分析结果、且与扫描时长差得够多时取分析时长，否则取 `tracks.duration_seconds`（`backend/migrations/0036_track_analysis_loudness_range.sql:5-18`） |

发行层的艺人：`release_effective.artist` 与 `album_artist` 同样叠加了修正与自动层（`0058:29`、`:35`）。

### 现成的查询

- **一次取齐四项**：`catalog.Store.LoadTrackSummaries(ctx, trackIDs []string) (map[string]TrackSummary, error)`（`backend/internal/catalog/track_summary.go:61-131`，SQL 在 `:39-57`）。返回的 `TrackSummary`（`:27-37`）含 `Title`、`Artist`、`DurationSeconds *float64`（经 `track_display_durations`，非正或非有限时为 nil，`:112-114`）、`Present`、`Release ReleaseSummary`（`Title`、`Artist`、`AlbumArtist` 都是有效值）。一次最多 1,000 个不同的曲目 ID（`maxSummaryTrackIDs`，`:14`），包含 `missing` 的曲目，无效 UUID 被跳过。
- 只要时长：`catalog.Store.LoadTrackDuration(ctx, trackID) (float64, error)`（`backend/internal/catalog/track_source.go:79-96`），只读 `present` 曲目，未知时为 0。
- 发行详情里的曲目表：`catalog.Store.LoadReleaseMedia(ctx, releaseID)`（`backend/internal/catalog/read.go:181-`），每条曲目有 `Title`、`Artist`、`DurationSeconds`（同样经 `track_display_durations`）。
- 只要发行的有效标题与艺人：`catalog.Store.LoadReleaseSummary(ctx, releaseID)`（`read.go:170-`）。

`curation` 的依赖规则允许导入 `catalog`（`dependency_rules_test.go:76`），上面这些方法它都能调用；`sources` 及其子包不能。

没有找到按“缺歌词的 present 曲目”或“缺封面的发行”列举的现成查询。

---

## 12. 迁移版本与接口样本

### 迁移

- 当前最大版本：**0060**（`backend/migrations/0060_curation_sources.sql` 与 `0060_curation_sources.down.sql`）。0058 是自动整理，0059 是曲目标识，0060 是源开关与外部对应。
- 回退往返测试 `TestPostgreSQLMigrationRollbackRoundTrip`（`backend/internal/platform/postgres/migrations_rollback_integration_test.go:136`）：迁移改变旧约束或枚举时必须在 `rollbackFixtures[版本]` 里补新取值的数据，否则失败（`:276-279`）；现有条目里 `50` 是歌词的 `lrc_file`（`:55`），`60` 是源开关（`:17`）。`:257-272` 是按版本号预置数据的写法（59、60 各有一段）。
- 约定（`.trellis/spec/backend/database-guidelines.md:234-238`）：上行与下行同在一个事务，不得含 `CONCURRENTLY` 等事务外语句；进入过发布标签的迁移文件不得修改。

### `TestPostgreSQLAPIResponseSamples`

测试在 `backend/internal/app/api_samples_integration_test.go:44`，样本目录 `backend/internal/app/testdata/api-samples/`，前端的 `frontend/src/test/api-contract.test.ts` 读同一批文件。

歌词样本（四份，由 `recordLyricsSamples` 记录，`api_samples_integration_test.go:974-984`）：

| 样本名 | 文件 | 素材 |
| --- | --- | --- |
| `tracks/lyrics-synced` | `testdata/api-samples/tracks/lyrics-synced.json` | 曲目 `Allegro` |
| `tracks/lyrics-plain` | `testdata/api-samples/tracks/lyrics-plain.json` | 曲目 `Andante` |
| `tracks/lyrics-none` | `testdata/api-samples/tracks/lyrics-none.json` | 曲目 `Finale` |
| `tracks/lyrics-external` | `testdata/api-samples/tracks/lyrics-external.json` | 作品专辑第一轨旁的 `01.lrc`（夹具写在 `:652-655`），响应 `source: "external"` |

前端登记在 `api-contract.test.ts:394-397`，都用 `decodeTrackLyrics`。

封面样本：

- 名字带封面的只有一份：`release-groups/list-one-with-artwork`（`testdata/api-samples/release-groups/list-one-with-artwork.json`）。夹具是直接往 `release_artworks` 插一行（`storage_key = 'sample-cover.webp'`、`source_type = 'embedded'`、640×480，`api_samples_integration_test.go:301-305`），前端登记在 `api-contract.test.ts:494`。
- 另一处夹具在 `recordListeningSamples`（`:741-744`，`storage_key = 'sample-listening-cover.webp'`）。
- `artwork` 非空的样本文件共十份（`grep -rl '"artwork": {' backend/internal/app/testdata/api-samples`）：`release-groups/list-one-with-artwork`、`release-groups/get-default`、`release-groups/get-multiple`、`releases/get-single`、`releases/get-corrected`、`listening/recent-releases`、`listening/history-queue`、`listening/history-queue-truncated`、`listening/history-tracks`、`listening/history-tracks-missing`。带 `"artwork"` 键（含 null）的样本文件共 150 份。
- 封面的二进制接口没有 JSON 样本（记录器要求 `Content-Type: application/json`，`:1117-1119`）；它的断言在 `backend/internal/app/artwork_cache_integration_test.go`（`TestPostgreSQLArtworkCachingUsesETagAndRevalidation` `:48`、`TestPostgreSQLArtworkServesFreshBytesWhenTheTagDiffers` `:106`、`TestPostgreSQLArtworkRejectsUnsafeStorageKeys` `:117`）。

与外部源有关的样本：`curation/sources`（`testdata/api-samples/curation/sources.json`，目前只有 `musicbrainz` 一项，`capabilities` 是三个查找能力）与 `curation/source-updated`，由 `recordCurationSamples` 记录（`backend/internal/app/curation_samples_integration_test.go:11`，在 `api_samples_integration_test.go:391` 调用）。前端把它们放在 `unconsumedContracts`（`api-contract.test.ts:833-842`），还没有解码器。注册的源或能力变化时这两份样本的内容随之变化。

---

## 13. 与合同文档不一致、或容易误读的地方

1. **任务描述里的“像素上限、解码”在代码里不存在。** 封面校验只有字节数上限 16 MiB、文件头格式识别与头部宽高大于 0（`backend/internal/catalog/artwork/artwork.go:121-134`）。ADR 0006 第 8 节的说法是“沿用现有封面的校验（大小、格式）”（`docs/architecture/adr/0006-auto-curation-and-sources.md:216`），与代码一致；不一致的是任务描述的假设。
2. **外部源合同第 8 节的测试位置与实际不符。** 合同写缓存、重试与错误的用例在“`backend/internal/sources` 的缓存与请求用例”（`.trellis/spec/backend/sources-contracts.md:117-118`）。实际上 `backend/internal/sources/` 下只有限速器的测试（`limiter_test.go`、`limiter_priority_test.go`），缓存有效期、目录、失败记忆、重试、读取上限、日志隐私都在 `backend/internal/sources/musicbrainz/request_policy_test.go`（`:28`、`:89`、`:163`、`:196`、`:252`、`:271`、`:306`、`:333`）。
3. **管家联网合同列出的封面清单断言点，有三项找不到对应用例。** 合同第 8 条要求覆盖“Cover Art Archive 的 404、清单 307 到官方 index.json、禁止跳图片/其他域名、不下载图片；User-Agent 与头部无用户/实例信息”（`.trellis/spec/backend/steward-web-contracts.md:233`）。现有用例只覆盖重定向目标的放行与拒绝、`PublicOnly` 策略与私网解析（第 8 节的表）。清单 404 得空列表、`Cover` 字段的映射、封面清单请求头的内容没有断言。
4. **`source: "external"` 的含义。** 歌词合同第 5 节写明 `external` 对应 `lyrics_source = lrc_file`（`.trellis/spec/backend/lyrics-contracts.md:89`），代码一致（`backend/internal/catalog/lyrics.go:56`、`:66-68`）。这不是文档与代码的出入，但与 ADR 0006 所说的“外部歌词”（外部源取来的）是同一个词的两个含义；接口样本 `tracks/lyrics-external` 与前端类型 `LyricsSource` 用的是前一个含义。
5. **处理函数的注释比行为窄。** `trackLyrics` 的注释写“返回曲目的内嵌歌词”（`backend/internal/catalog/httpapi/handlers.go:251`），实际也返回外置 `.lrc` 的歌词。
6. **数据库规范提到的 `media-cache/v1/` 已不存在。** `.trellis/spec/backend/database-guidelines.md:238` 把“派生缓存（`<DATA_DIR>/media-cache/v1/`）”与封面并列为数据目录的内容；现在的代码在启动时删除整个 `media-cache/`（`backend/internal/app/media_cache.go:18-63`），部署合同也是这样写的（`deployment-environment-contracts.md:18`）。
7. **外部源合同第 2 节的依赖表与规则测试一致**（`sources-contracts.md:19-23` 对 `dependency_rules_test.go:49-51`），但表里没有“谁可以导入 `sources`”这一列；现状是 `curation`、`steward/web`、`sources/musicbrainz` 三个包（`:76`、`:41`、`:50`）。
8. **自动整理合同第 11 节说闸门“没有生产调用方”**（`.trellis/spec/backend/curation-contracts.md` 第 11 节末条），代码一致：`Sources` 的三个取能力方法在非测试代码里没有调用方，只有 `curation/httpapi` 用到 `List` 与 `SetEnabled`。

没有发现不一致的：封面表的来源约束与一发行一张（ADR 0006 `:52` 对迁移 0005）；歌词的五种来源与整体覆盖（歌词合同第 4 节对迁移 0050 与 `persist_track.go`）；封面清单不经限速器、留待 B2 迁移（管家联网合同 `:167` 对 `musicbrainz.go:121-175`）；缓存的路径、键与有效期（外部源合同第 5 节对 `request.go`、`cache.go`）；备份不含数据目录。

---

## Caveats / Not Found

- 没有做外部检索：Cover Art Archive 的重定向链（`coverartarchive.org` → `archive.org` → `*.us.archive.org`）与 LRCLIB 的接口参数、返回结构都没有对真实服务或官方文档核实。代码里能证实的只有“清单接口可能 307 到 archive.org 的 `index.json`”这一条测试假设（`c8_test.go:341`）。
- 没有运行任何测试；行号与结论来自静态阅读。
- 没有找到：封面的缩略图或多尺寸、周期性的孤儿文件清扫、曲目列表里的“有无歌词”字段、按“缺封面/缺歌词”列举对象的查询、导出的“只落盘不写关联”的封面函数、导出的“是否同步歌词”判断函数、egress 的重定向主机白名单选项、`Requester` 的二进制下载方法。
- `LoadTrackSummaries` 的 `Artist` 是 `tracks.artist` 的显示串；多艺人曲目里这个串的具体写法（分隔符、顺序）由 `metadata.CanonicalTrackArtist` 决定，本次没有展开它的规则（`backend/internal/catalog/metadata/artist.go:154`）。

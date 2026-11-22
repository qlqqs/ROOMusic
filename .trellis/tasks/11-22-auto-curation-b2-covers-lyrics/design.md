# 自动整理 B2：技术设计

需求与默认值见 `prd.md`；现状锚点见 `research/covers-and-lyrics-current.md`，外部接口事实见 `research/caa-and-lrclib-api.md`。
本文定边界、数据结构、函数形状与规则；函数的内部写法由实现决定，签名可以在不改变语义的前提下调整，调整处写进实现报告。

## 一、边界

```text
sources                      能力接口（新增封面、歌词）、Requester（新增重定向策略与取字节）
sources/coverartarchive      清单、选图、下载（新）
sources/lrclib               精确查询与搜索（新）
steward/web                  get_music_metadata 的封面清单改走 coverartarchive
catalog/artwork              只落盘的导出入口；两张表一起计引用、一起查找
catalog/metadata             NormalizeLyrics 与大小上限（从 library/parser 挪来）
catalog（读取）              摘要里的封面取 本地 → 外部；歌词取 本地 → 外部
operations                   Outcome.AfterCommit（提交后回调）
curation                     外部封面与外部歌词的写入、撤销、展示；闸门增加两种能力
app                          装配：两个适配器、三个源的注册、两个新的操作类型
```

依赖规则的变化（`dependency_rules_test.go`）：

| 包 | 变化 |
| --- | --- |
| `sources/coverartarchive`（新） | 可导入 `sources`、`sources/musicbrainz`（只为取它的声明以共用限速器）、`platform/uuid` |
| `sources/lrclib`（新） | 可导入 `sources` |
| `curation` | 增加 `catalog/artwork`、`catalog/metadata`（`app` 不直接导入 `catalog/artwork`：`NewWriter` 自己按数据目录构造存储） |
| `steward/web` | 增加 `sources/coverartarchive` |
| `app` | 增加 `sources/coverartarchive`、`sources/lrclib` |
| `library/parser` | 不变（已可导入 `catalog/metadata`） |

`app` 这一条规则只有外部源线会改；存储线不动它。

`sources` 及其子包仍然不依赖 `curation`、`catalog`、数据库。

## 二、`sources`：能力与请求

### 能力接口与中立类型

```go
const (
    CoverArt   Capability = "cover_art"
    LyricsLookup Capability = "lyrics"
)

// 封面：目标是外部的发行或 release group。
type CoverTarget struct {
    Kind string // "release" | "release_group"
    ID   string
}
type CoverImage struct {
    ID        string            // 源里的图片编号（十进制数字串）
    ReleaseID string            // 图片实际所属的外部发行（release group 的清单由此指明来源；合并后的发行由此得到新 ID）；清单没给时为空
    Front, Back bool
    Types     []string
    URL        string            // 原图地址（清单原样，只用于展示）
    Thumbnails map[string]string // 清单原样，只用于展示；清单里是 null 时保持 nil
}
type CoverFile struct {
    Image CoverImage
    Size  string // "1200" | "500" | "250" | "original"
    Data  []byte
}
type CoverFinder interface {
    Covers(ctx context.Context, target CoverTarget) ([]CoverImage, error) // 没有任何图片：空切片
    FrontCover(ctx context.Context, target CoverTarget) (CoverFile, error) // 没有主封面或取不到任何一档：ErrNotFound
}

// 歌词。
type LyricsQuery struct {
    Title, Artist, Album string
    DurationSeconds      int // 0 表示不带时长
}
type Lyrics struct {
    ID                   string // 源里的记录编号
    Title, Artist, Album string
    DurationSeconds      float64
    Instrumental         bool
    Synced, Plain        string // 原样；没有时为空串
}
type LyricsFinder interface {
    LyricsFor(ctx context.Context, query LyricsQuery) (Lyrics, error)      // 精确；没找到 ErrNotFound
    SearchLyrics(ctx context.Context, query LyricsQuery) ([]Lyrics, error) // 候选，可为空
}
```

图片内容的校验不在 `sources`：它只保证“字节数不超过上限、来自官方主机”，格式与宽高由落库的一方（`catalog/artwork`）校验。

### `Requester` 的扩展

```go
type Options struct {
    // 现有字段不变……
    MaxRedirects  int                    // 0（默认）不跟随，与现在相同
    AllowRedirect func(*url.URL) bool    // MaxRedirects > 0 时必填；返回假时这次请求以“地址策略拒绝”结束
}

// GetBytes 取一个二进制响应：不读写响应缓存；限速、调用身份、放行回调、重试、错误与失败记忆的规则与 GetJSON 相同
// （失败记忆同样要求有数据目录）。单次请求的时限是 60 秒（GetJSON 仍是 20 秒）。
// 404 返回 ErrNotFound；Content-Length 或实际读到的字节超过 maxBytes 按“响应结构不对”处理（不重试）。
func (r *Requester) GetBytes(ctx context.Context, address string, accept string, maxBytes int64) (data []byte, contentType string, err error)
```

- 跟随重定向时，每一跳之前：先过 egress 自带的检查（协议、次数），再要求目标是 `https`（基础地址本身是 `http` 时允许 `http`，测试用）、
  不带用户名密码、端口为空或 443（同上，测试用的本地端口由客户端工厂的拨号替换，不放宽这条）、`AllowRedirect` 返回真。
  每一跳只带 `Accept` 与 `User-Agent` 两个请求头。egress 的 `PublicOnly` 对每一跳照常生效。
- `GetJSON` 在 `MaxRedirects > 0` 时同样跟随；缓存键仍是最初请求的地址。
- 一次调用（含全部跳转）占一个限速许可；`sources.request.finished` 的域名记最初请求的域名。
- `AllowRedirect` 拒绝、协议或端口不合要求时返回 `*egress.Error`（`errors.Is(err, egress.ErrDenied)` 成立），所以不重试。
- 包装 `CheckRedirect` 时复制客户端工厂返回的 `http.Client` 再改（测试的工厂每次返回同一个实例，原来的 `CheckRedirect` 可能是 nil）。

## 三、Cover Art Archive 适配器

声明：`{ID:"coverartarchive", Name:"Cover Art Archive", Capabilities:[cover_art], DefaultEnabled:true, MinInterval:1s}`。基础地址 `https://coverartarchive.org`。

两个 `Requester`：

| 用途 | 限速器 | 重定向 | 缓存 |
| --- | --- | --- | --- |
| 清单 | `sources.DefaultLimiter(musicbrainz.Declaration())`（与 MusicBrainz 同一个；`Options.ManifestLimiter` 可注入） | 至多 5 跳；目标主机是 `coverartarchive.org`、`archive.org` 或 `*.archive.org`，且路径以 `/index.json` 结尾 | 有（目录 `sources/cache/coverartarchive/`；200 存 30 天，404 存 1 天） |
| 图片 | `sources.DefaultLimiter(本源的声明)`（`Options.Limiter` 可注入） | 至多 5 跳；目标主机同上，路径不限 | 不缓存响应；数据目录照样传入，失败记忆因此可用 |

- `Covers(target)`：`GET <base>/<release|release-group>/<mbid>/`。MBID 校验 UUID 外形并转小写（大写会被服务拒绝）。
  解析 `images[]`：`id` 接受数字或字符串，规范成十进制数字串，不是纯数字时 `ID` 留空（这一项仍然返回，只是不能下载）；
  `ReleaseID` 取顶层 `release` 地址最后一段，不是 UUID 或没有这个字段时留空；`thumbnails` 是 null 时保持 nil，否则只保留值为字符串的键。
  结构校验只有一条：顶层 `images` 是数组（与管家现有的判断相同）。404 → 空切片（不是错误）。
- 选图（`FrontCover`）：只在 `ID` 非空的项里选。`Front` 为真的第一张；没有则 `Types` 含 `Front`（不分大小写）的第一张；都没有 `ErrNotFound`。
  `ReleaseID` 为空时：目标是 release 就用目标的 ID，目标是 release group 则 `ErrNotFound`（不知道图片属于哪个发行）。
- 下载：地址**由适配器自己拼**，不用清单里的地址（旧清单里是 `http://`）：`<base>/release/<ReleaseID>/<ID>-1200`，404 依次试 `-500`、`-250`、
  `<base>/release/<ReleaseID>/<ID>`（原图）。每一档上限 16 MiB（`artwork` 的现有上限，常量在本包重复定义并注明来源）；
  原图的最终响应 `Content-Type` 是 `application/pdf` 或内容以 `%PDF` 开头时视为没有（`ErrNotFound`）。四档都 404 → `ErrNotFound`。
  非 404 的失败按 `Requester` 的规则原样返回，不再试下一档。
- 管家用的入口就是 `Covers`：它需要的四个字段都在 `CoverImage` 里。
- 图片的第一跳仍是 `coverartarchive.org`（MetaBrainz 的网关），所以对这个网关的合计上限是每秒 2 次（MusicBrainz 加清单 1 次，图片 1 次）。
  Cover Art Archive 的文档写明目前没有限速规则；父任务 V8 约束的是 MusicBrainz 与清单。这一点是明确接受的。
- `StartCacheCleanup(ctx)`：同 MusicBrainz 适配器。

发给它的数据：只有外部 ID。

## 四、LRCLIB 适配器

声明：`{ID:"lrclib", Name:"LRCLIB", Capabilities:[lyrics], DefaultEnabled:true, MinInterval:1s}`。基础地址 `https://lrclib.net`，不跟随重定向。

| 方法 | 请求 | 说明 |
| --- | --- | --- |
| `LyricsFor` | `/api/get?track_name=&artist_name=`，专辑非空加 `album_name`，时长在 1–3600 秒内加 `duration`（四舍五入的整数） | 标题或艺人为空 `ErrInvalidQuery`；404 → `ErrNotFound`（缓存 1 天）；结构校验：有数字 `id` |
| `SearchLyrics` | `/api/search?track_name=&artist_name=`，专辑非空加 `album_name` | 标题为空 `ErrInvalidQuery`；返回至多 20 条；结构校验：顶层是数组、每项有 `id` |

- 响应字段映射：`id`→`ID`（十进制串）、`trackName`、`artistName`、`albumName`、`duration`、`instrumental`、`syncedLyrics`、`plainLyrics`（`null` 按空串）。
- 搜索的一次响应约 200 KB，后台上限 8 MiB、对话上限 2 MiB 都够。
- 时长的筛选、写法差异的容忍不在适配器里：适配器只转达外部的结果，取舍在子任务 C。

发给它的数据：曲目标题、艺人、专辑名、时长。

## 五、管家的封面清单

- `steward/web.Service` 增加 `covers *coverartarchive.Client` 与 `WithCovers(client)`；`web.New` 的签名不变，默认构造一个不缓存的实例
  （数据目录为空；客户端工厂是闭包 `func(o) { return s.newClient(o) }`，所以测试在 `New` 之后替换 `s.newClient` 仍然生效）。`app` 里注入与后台共用的实例。
- `Service.coverArt` 改为调用 `covers.Covers(ctx, {release, mbid})` 并投影成现有的 `Cover{URL, Front, Back, Thumbnails}`；
  错误映射沿用 `musicError`。包级变量 `coverArtBaseURL` 删除，测试改为
  `WithCovers(coverartarchive.New(Options{BaseURL, NewClient: s.newClient, ManifestLimiter: 零间隔的限速器}))`。
  现有两个封面用例里的 `"synthetic"` 换成一个合成的 UUID（适配器校验 MBID 外形）；断言不变。
  `thumbnails` 为 null 的图片输出仍是 `null`。
- 行为差别只有两处：清单请求现在排队（对话优先，至多等 10 秒，超时 `rate_limited`）；应用装配的实例有缓存。
- 日志：管家的 `steward.web.request.finished`（来源 `cover_art_archive`）保留；出站另有 `sources.request.finished`。

## 六、数据模型（迁移 0061）

```sql
CREATE TABLE release_external_artworks (
    release_id UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/jpeg','image/png','image/gif','image/webp')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    storage_key TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX release_external_artworks_storage_key_idx ON release_external_artworks (storage_key);

CREATE TABLE track_external_lyrics (
    track_id UUID PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    lyrics TEXT NOT NULL CHECK (lyrics <> '' AND octet_length(lyrics) <= 65536),
    content_hash TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- `operations_type_ck` 增加 `release_external_artwork`、`track_external_lyrics`。操作目标的约束不变（两类操作的目标都是发行）。
- `curation_suppressions` 不改结构。每次撤销写两条标记，一条按内容、一条按外部身份（同一张图的另一档缩略图内容不同，只按内容挡不住）：

  | 对象 | `target_type` | `subject` | `value_hash` |
  | --- | --- | --- | --- |
  | 封面的内容 | `release` | `artwork` | 图片字节的 SHA-256 |
  | 封面的外部身份 | `release` | `artwork_source` | `source + "\x00" + external_id` 的 SHA-256 |
  | 歌词的内容 | `track` | `lyrics` | 规范化后文本的 SHA-256 |
  | 歌词的外部身份 | `track` | `lyrics_source` | `source + "\x00" + external_id` 的 SHA-256 |

- 回退脚本的顺序（都在一个事务里）：
  1. `UPDATE steward_plan_items SET operation_id = NULL`，范围是这两类操作及它们的撤销记录（这个外键立即检查、不级联；管家能经方案撤销这两类操作）；
  2. 删除四种 `subject` 的不再提出标记；删两张表；
  3. 删除这两类操作的撤销记录，再删除这两类操作；
  4. `SET CONSTRAINTS ALL IMMEDIATE`，然后恢复 `operations_type_ck`（不先落实延迟约束就改不了表）。

  回退是有损的；已下载的图片文件留在 `artwork/` 里成为无人引用的文件（可手工删除，不影响读取）。
- 回退往返测试的夹具（版本 61）：两张表各一行、两类操作各一条及一条撤销、四条标记、一条引用撤销记录的方案项。
  测试对回退前后做整库数据比对，所以夹具不能改回退脚本不恢复的既有表（不改 `releases.auto_revision`，不建 `curation_batches` 的行，不动 `curation_settings`）；
  用户、发行、曲目、方案项这些前置数据放在升级前的按版本预置段里。

`external_id` 的含义：封面是 `<外部发行 ID>/<图片编号>`；歌词是源里的记录编号。

## 七、读取

### 封面

- `releasesql/release.go`：连接片段改成两个左连接，投影对四列各取 `COALESCE(本地, 外部)`：

  ```sql
  LEFT JOIN release_artworks release_artwork ON release_artwork.release_id = releases.id
  LEFT JOIN release_external_artworks release_external_artwork
         ON release_external_artwork.release_id = releases.id AND release_artwork.release_id IS NULL
  ```

  两张表的四列都是非空的，所以四列要么全来自本地、要么全来自外部、要么全空；`releaseArtworkFromProjection` 不用改。
  片段的导出名与别名 `release_artwork` 的现有引用点一并核对（调研文档第 3 节列了全部读取点）。
- `artwork.Store.Lookup`：按 `storage_key` 在两张表里找（`UNION ALL … LIMIT 1`）。
- 孤儿清理的引用计数（`removeUnreferencedArtwork`）：两张表合计。
- 扫描反馈的封面指纹（`scan_feedback_snapshot.go`）不变：它描述的是扫描自己的结果。
- 目录的 EXPLAIN 门禁与 `TestPostgreSQLCatalogHTTPPerformance` 因摘要多一个主键左连接要重跑（验证命令见 `implement.md`）。

### 歌词

`catalog.Store.LoadTrackLyrics` 的查询改为同时取外部的：

```sql
SELECT t.id::text, t.lyrics, COALESCE(t.lyrics_source = 'lrc_file', false), e.lyrics
FROM tracks t LEFT JOIN track_external_lyrics e ON e.track_id = t.id
WHERE t.id=$1::uuid AND t.source_status='present'
```

本地的非空且能解析出可显示的行时用本地的；否则外部的非空且能解析时用外部的，`Source` 为新常量 `LyricsSourceOnline = "online"`。
（本地歌词存在但解析后没有可显示的行，等同没有歌词，这时用外部的。）“本地有歌词”在读取与写入两处用同一个判断：`ParseLyrics` 成功。

### 规范化

`NormalizeLyrics` 与 `MaxLyricsBytes` 挪到 `catalog/metadata`（新文件 `lyrics.go`）；`library/parser` 里的调用点与测试引用改成新的位置，行为不变。

## 八、写入：`curation.Writer`

`Writer` 需要封面存储与日志：`curation.NewWriter(db, dataDirectory, logger)`，内部 `artwork.NewStore(dataDirectory)`
（`NewWriter` 与 `Writer` 在 `curation/settings.go`；现有三个调用点一并改）。

```go
type ArtworkRequest struct {
    ReleaseID  string
    Data       []byte
    Source     Source // Kind 必须是 "source"；Provider 是源的标识，按 ^[a-z0-9._-]{1,64}$ 校验（新表的约束不允许冒号）
    ExternalID string // 1–128 字节
    BatchID, Key, RequestID string
}
type LyricsRequest struct {
    TrackID    string
    Text       string // 外部给的原文（同步或纯文本）
    Source     Source
    ExternalID string
    BatchID, Key, RequestID string
}
type ExternalApplied struct {
    OperationID string `json:"operation_id"`
    Skipped     string `json:"skipped,omitempty"` // local_present | occupied | suppressed
}
func (w *Writer) ApplyArtwork(ctx context.Context, request ArtworkRequest) (ExternalApplied, error)
func (w *Writer) ApplyLyrics(ctx context.Context, request LyricsRequest) (ExternalApplied, error)
```

共同流程（与 `Apply` 相同的部分不重复：幂等键、开启者在事务外先读、`operations.Run`、核对设置、批次）：

1. 事务外只校验、不落盘。封面：`artwork.Store.Describe(data)` 得到键、哈希、MIME、宽高；不合格 `400 invalid_input`。
   歌词：`metadata.NormalizeLyrics`，再 `catalog.ParseLyrics` 必须有可显示的行；不合格 `400 invalid_input`。哈希是规范化后文本的 SHA-256。
2. `operations.Run`，类型 `release_external_artwork` 或 `track_external_lyrics`。加锁：
   - 封面：`governance.LockAutoRelease`（目录写锁、发行行锁，要求发行可见）。
   - 歌词：`governance.LockAutoReleaseOfTrack(ctx, tx, trackID)`：先取目录写锁，再按曲目解析所在发行（曲目必须 `present`，否则 `not_found`），再锁发行行。
     先取写锁再解析，是因为扫描会把曲目改挂到别的发行。
3. 依次判断，命中即跳过，返回 `409 nothing_to_apply`，`ExternalApplied.Skipped` 给出原因，不产生记录：
   - `local_present`：发行在 `release_artworks` 有行／曲目的本地歌词能解析出可显示的行；
   - `occupied`：外部表里已有这一行（不论内容是否相同）。封面例外：这一行指向的文件已经不在磁盘上时（恢复数据库备份而没有数据目录），
     视同没有外部封面——删除旧行，按新写入继续（旧行的操作此后不再“仍是它写的”，撤不了，记录保留）；
   - `suppressed`：内容哈希或外部身份的不再提出标记命中。
4. 封面：`Store.Write(ctx, tx, data, described)`——在事务里取这个存储键的锁（见第十节），文件不在就写入。然后插入外部表的行
   （`operation_id` 为本次操作），发行的 `auto_revision` 加一。
5. 记录：目标是发行，版本号用 `auto_revision`；前态 `{"artwork": null}` 或 `{"track_id":…, "lyrics": null}`
   （替换了文件缺失的旧行时，前态记旧行的 `{storage_key, content_hash, source, external_id, operation_id}`，只作记录，撤销不恢复它）；
   后态封面是 `{"artwork": {storage_key, content_hash, mime_type, width, height, source, external_id}}`，
   歌词是 `{"track_id", "track_title", "lyrics": {content_hash, kind, lines, source, external_id}}`（不存歌词全文）；`origin` 与 `batch_id` 同 `Apply`。
6. 封面：执行器写了文件而事务最后没有提交时，`Run` 返回后调用 `Store.Discard`（它自己数引用，见第十节）。
   跳过与同键重放都到不了第 4 步，不落盘。

- 指纹：类型、发行或曲目、内容哈希、`provider`、`external_id`；不含 `basis`。
- 需要 `governance` 提供的导出入口（都在 `catalog/governance`，因为目录写锁与可见性谓词是 `catalog/internal` 的）：
  `(*LockedAutoRelease).Bump(ctx) (expected, result int64, err error)`（只执行 `auto_revision+1`，不刷新派生）、`ID()`、`EditRevision()`；
  `LockAutoReleaseOfTrack`；撤销用的 `LockAutoReleaseForRevert(ctx, tx, releaseID)`（不要求可见，读出 `edit_revision`）；
  `LoadOperationReleases(ctx, q, ids) (map[string]operations.ReleaseInfo, error)`（现有私有的 `loadReleaseInfos` 导出）。

## 九、撤销与展示

两个新的 `operations.Kind` 放在 `curation` 包（它同时需要封面存储与不再提出的写入）：
`curation.ExternalArtworkOperations(writer)`、`curation.ExternalLyricsOperations(writer)`，在 `app` 的 `operationKinds` 里注册。
注册之后，HTTP 的单条撤销、按批与按来源的 `Reverter`（不按类型取候选）、管家的 `revert_operation` 都自动覆盖它们。

撤销：

- `LockAutoReleaseForRevert` 按**记录的目标发行**加锁，不要求可见。歌词的曲目之后被改挂到别的发行也照样成立：行按 `track_id` 找。
- 外部表这一行存在且 `operation_id` 是原操作：删除这一行，`auto_revision` 加一，写两条不再提出（内容与外部身份；重复的忽略）。
  `value` 封面存 `{source, external_id, mime_type, width, height}`，歌词存 `{source, external_id, kind, lines}`；`created_by` 是执行撤销的人。
  行不存在或属于别的操作：`409 revert_conflict`。
- 封面：`Outcome.AfterCommit` 在提交后调用 `Store.Discard(storage_key)`。删除失败只记日志。
- 撤销响应的 `revision` 是发行当前的 `edit_revision`（`ResponseRevision`），与自动层的撤销一致。

`operations.Outcome` 增加 `AfterCommit func()`：`Run` 在提交成功后调用一次；重放与失败时不调用。`RevertExecutor` 原样传递。

`Describe`：

```jsonc
// release_external_artwork
{"kind": "release_external_artwork", "source": "coverartarchive", "external_id": "…",
 "artwork": {"resource_id": "<storage_key>", "mime": "image/jpeg", "width": 1200, "height": 1200}}
// track_external_lyrics
{"kind": "track_external_lyrics", "source": "lrclib", "external_id": "…",
 "track": {"id": "…", "title": "…"}, "lyrics_kind": "synced", "lines": 42}
```

- `Release` 填目标发行的展示信息；`revertible` = 外部表这一行仍属于这条操作。撤销记录的 `Describe` 用被撤销操作的后态（与现有类型一致）。
  `track.title` 取记录里保存的标题（记录描述当时的情况）。
- 读不懂的状态不返回错误，只给空的 `Changes`（与自动层的 `Describe` 相同；否则一条坏记录会让整页列表失败）。
- 两个 `Kind` 都实现 `operations.RevertPreviewer`，管家 `revert_operation` 的预览给出一行：字段名 `artwork` 或 `lyrics`，
  前是 `{source, external_id}`（歌词另带曲目标题），后是 null。

已知的展示限制（写进合同）：歌词操作记在曲目当时所在的发行上，曲目之后被改挂到别的发行时，按新发行筛选操作记录看不到它；
歌词的不再提出标记按曲目存，按发行筛选标记的接口看不到它。两者都留给 E1 的整理记录页按批次展示。

## 十、`catalog/artwork` 的改动

外部写入是这个目录的第二个写者，“数引用再删文件”与“落盘再写行”之间必须互斥：两个发行拿到同一张外部图是常态。

- **存储键的锁**：新的事务级 advisory lock，键空间与现有的迁移、扫描、目录、操作锁都不同（两参数形式，类别取 ASCII 的 `ARTW`，第二个参数是存储键的 32 位哈希）。
  永远最后取（在目录写锁与发行行锁之后）。
- 导出入口：

  ```go
  type Stored struct { Key, Hash, MIMEType string; Width, Height int }
  func (s *Store) Describe(data []byte) (Stored, error)                                  // 校验（现有三条）并算出键；不碰文件与数据库
  func (s *Store) Write(ctx context.Context, tx *sql.Tx, data []byte, stored Stored) error // 取键锁；文件不在或内容不同就写入
  func (s *Store) Exists(key string) bool                                                 // 文件在不在（给“旧行的文件已丢失”的判断用）
  func (s *Store) Discard(ctx context.Context, database TxBeginner, key string) error     // 开事务、取键锁、两张表合计引用为 0 时删文件、提交
  ```

- 扫描路径改成同一套规则：`Save` 与 `bindWithFeedback` 在写关联之前取键锁并确认文件在（手里有字节，缺了重写）；
  `removeUnreferencedArtwork` 与 `cleanupArtwork` 取键锁后数两张表。扫描的外部可见行为不变。
- `Lookup` 按 `storage_key` 在两张表里找。

## 十一、闸门与装配

- `curation.SourceRegistration` 增加 `Covers sources.CoverFinder`、`Lyrics sources.LyricsFinder`；`Sources` 增加 `CoverFinder(id)`、`LyricsFinder(id)`，
  包装方式与现有三种能力相同（每个方法先 `Check`，再 `sources.WithBackground(ctx, permit)`）。
- `app.go`：构造 `coverartarchive.New(Options{DataDirectory, Logger})` 与 `lrclib.New(Options{DataDirectory, Logger})`，启动各自的缓存清理；
  按 MusicBrainz、Cover Art Archive、LRCLIB 的顺序注册；`stewardConfig` 带上封面适配器；`operationKinds` 加两项；
  `curation.NewWriter(database, settings.DataDirectory, logger)`。
- 源开关接口的返回多两项，样本 `curation/sources` 更新。

## 十二、前端

- 歌词解码器与类型接受 `online`（`frontend/src/features/player/api/decoders.ts`）；面板不显示来源，界面不变。
- 歌词查询是永不过期的（原来的前提是“歌词只在重扫时变化”）：撤销成功后让歌词查询失效；封面随目录查询一起失效（现有行为）。
- 操作记录：`OPERATION_TYPES` 加两项，`governance/api/decoders.ts` 接受两种新的 `changes.kind`，测试替身（`src/test/governance-data.ts`）同步，`change-summary.ts` 给出摘要文字（“补了封面，来自 Cover Art Archive”“给《曲目》补了歌词，来自 LRCLIB”），
  撤销对话框沿用自动层撤销的说明。源的显示名先在前端按标识映射三个已知的源，未知的显示标识本身。
- 接口合同测试登记新样本。

## 十三、兼容与回退

- 没开自动整理的实例：两张新表是空的，读取结果与现在逐项相同；摘要查询多一个命不中的主键左连接。
- 管家工具：输出不变；清单请求受限速（对话优先）。
- 回退 0061 是有损的（见第六节）。关掉总开关即不再写入；已写入的可以按来源整体撤销。

## 十四、主要取舍

| 取舍 | 选择 | 放弃的做法与原因 |
| --- | --- | --- |
| 外部封面放哪 | 单独一张表，文件与本地封面同一个内容寻址目录 | 放宽 `release_artworks.source_type` 并改成一发行多行：扫描的删除与替换都要改，且“扫描不清除”要靠运行时判断 |
| 读取优先顺序在哪实现 | SQL 片段里两个左连接加 `COALESCE` | 视图：`UNION` 形式的视图进入十几处连接后计划不可控 |
| 撤销后的文件清理 | `Run` 的提交后回调，加按存储键的锁 | 定期清扫整个目录：要遍历目录并靠文件年龄判断，仍然需要同一把锁 |
| 是否允许替换 | 不允许，先撤销再写（文件已丢失的旧行除外） | 允许替换：前态要保存上一张图并保证文件还在 |
| 图片地址 | 适配器按清单里的编号自己拼 | 用清单里的地址：旧清单是 `http://`，还要再校验一次主机 |
| 歌词操作的目标 | 发行 | 新增曲目目标：要改目标约束、展示信息与前端，留给子任务 I |

## 十五、已知风险

- Cover Art Archive 没有公开的限速规则；清单与 MusicBrainz 合用每秒 1 次是保守取法。archive.org 一侧的限流返回码没有核实，
  非 404 的失败按 `Requester` 的规则处理（后台重试后“稍后再试”）。
- 合并后的发行、缺失的缩略图只在真实服务上才会遇到，自动化测试用受控服务模拟，父任务收尾的真实试用里核对。
- LRCLIB 的精确匹配对简繁体、全角标点、专辑名写法敏感，命中率要到真实试用才知道；怎样放宽由子任务 C 决定。

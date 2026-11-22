# 自动整理 B1：技术设计

需求与默认值见 `prd.md`；外部事实见 `research/musicbrainz-api-and-tags.md`，代码现状见 `research/scan-tag-identifiers.md`。
本文给出边界、合同与取舍；函数的内部写法由实现决定，与本文的合同冲突时回到本文修订。

## 1. 包与依赖

```text
platform/egress            出站客户端与地址策略（从 steward/egress 原样迁入）
sources                    声明、能力接口与中立类型、限速器、文件缓存、带重试的 JSON 请求
sources/musicbrainz        MusicBrainz 适配器：实体级读取与查询 + 三种能力的实现
steward/web                两个音乐资料工具改用 sources/musicbrainz（其余不变）
curation                   各源开关、闸门、外部对应的存储
curation/httpapi           各源开关的管理员接口
library/parser、catalog/metadata、catalog/ingest、library   标签标识的解析与落库
app                        装配：一个 MusicBrainz 适配器实例同时交给管家与闸门
```

依赖规则（`backend/internal/app/dependency_rules_test.go`）：

| 包 | 允许的内部依赖 | `net/http` |
| --- | --- | --- |
| `platform/egress` | 无 | 是 |
| `sources` | `platform/egress`、`platform/buildinfo` | 是 |
| `sources/musicbrainz` | `sources`、`platform/uuid` | 按实现需要 |
| `steward/web` | 现有各项里的 `steward/egress` 换成 `platform/egress`，增加 `sources`、`sources/musicbrainz` | 是 |
| `steward/provider`、`steward/httpapi` | `steward/egress` 换成 `platform/egress` | 是 |
| `curation` | 现有各项，增加 `sources` | 否 |

`sources` 及其子包不依赖 `curation`、`steward`、`catalog`、数据库：它们只管“怎样向外部问”。
“现在能不能问”由 `curation` 的闸门决定（第 5 节）。删除 `steward/egress` 的规则条目。

## 2. 出站客户端迁移

`backend/internal/steward/egress` 整个目录移到 `backend/internal/platform/egress`，包名、导出标识、行为与测试都不变，
只改包注释里“管家统一的”这一表述与 7 个非测试文件、4 个测试文件的 import 路径。规范与文档里按路径提到它的地方一并改
（位置清单见 `research/scan-tag-identifiers.md` 第 9.3 节）。

## 3. `sources`：共用部分

### 3.1 声明与能力

```go
type Capability string // "release_lookup" | "recording_lookup" | "artist_lookup"

type Declaration struct {
    ID             string       // ^[a-z0-9._-]{1,64}$，与操作记录 origin.provider 用同一个标识，如 "musicbrainz"
    Name           string       // 显示名
    Capabilities   []Capability
    DefaultEnabled bool
    MinInterval    time.Duration // 全实例两次请求的最小间隔
}

type ReleaseFinder interface {
    ReleaseByID(ctx, id string) (Release, error)
    ReleasesByBarcode(ctx, barcode string) ([]Release, error)
    ReleasesByCatalog(ctx, catalogNumber, label string) ([]Release, error) // label 可空
    ReleasesByDisc(ctx, toc DiscTOC) (DiscMatch, error)
    SearchReleases(ctx, query ReleaseQuery) ([]Release, error)             // 标题、艺人、曲目数
}
type RecordingFinder interface {
    RecordingByID(ctx, id string) (Recording, error)
    RecordingsByISRC(ctx, isrc string) ([]Recording, error)
    SearchRecordings(ctx, query RecordingQuery) ([]Recording, error)       // 标题、艺人、时长
}
type ArtistFinder interface {
    ArtistByID(ctx, id string) (Artist, error)                             // 含别名
    SearchArtists(ctx, name string) ([]Artist, error)
}
```

中立类型只含匹配与补全用得上的字段：

- `Release`：`ID`、`Title`、`Disambiguation`、`Artists []Credit`、`Date`、`Country`、`Barcode`、`Status`、
  `Labels []{Name, CatalogNumber}`、`Group {ID, Title, PrimaryType, FirstReleaseDate}`、
  `Media []{Position, Format, Title, TrackCount, DiscIDs []string, Tracks []Track}`、`Complete bool`。
  `Complete` 为真表示来自按 ID 读取，带曲目表；查找与搜索的结果只有每张碟的 `TrackCount`，`Tracks` 为空，调用方要再按 ID 读取。
- `Track`：`ID`、`Position`、`Number`、`Title`、`LengthMS *int64`、`Artists []Credit`、`RecordingID`、`RecordingTitle`、`ISRCs []string`。
- `Recording`：`ID`、`Title`、`LengthMS`、`Artists`、`ISRCs`、`Releases []Release`（不完整的发行，至多 25 条）。
- `Artist`：`ID`、`Name`、`SortName`、`Disambiguation`、`Type`、`Country`、`Aliases []{Name, Locale, Primary}`。
- `Credit`：`ArtistID`、`Name`（署名写法）、`JoinPhrase`。
- `DiscTOC`：`Offsets []int`（各轨起点，单位 CD 帧，含 150 帧 lead-in）、`Leadout int`。
  `DiscMatch`：`Exact bool`（disc ID 命中）、`Releases []Release`。模糊结果至多 25 条，不知道是否被截断。

查找与搜索的返回顺序沿用外部源的顺序；搜索的 `score` **不进入**中立类型：它只是本次结果内的相对排名，不能当把握程度。

### 3.2 调用的两种身份

`sources.WithBackground(ctx)` 把这次调用标成后台。没有标记的是对话调用（保持管家现有的行为）。

`sources.WithBackground(ctx, permit)` 的 `permit func(context.Context) error` 是放行回调：**每次出站之前**（含每次重试）调用一次，
返回错误时这次调用立刻以该错误结束，不出站。闸门用它保证“排队或等待重试期间开关被关掉”时请求不会照发（第 5 节）。

| | 对话 | 后台 |
| --- | --- | --- |
| 限速排队 | 优先；排队超过 10 秒返回 `ErrRateLimited` | 让对话先走；一直等到轮到或 ctx 结束 |
| 503、429、网络错误 | 不重试，返回 `ErrUnavailable` | 按 `Retry-After` 等待；没有该头时 2 秒起指数退避加抖动，单次至多 60 秒；至多重试 4 次，之后返回 `ErrTryLater` |
| 其他非 200（404 除外）、响应解析不了 | `ErrUnavailable` | `ErrTryLater`，不重试 |
| 单次请求时限 | 20 秒 | 20 秒 |
| 单个响应的读取上限 | 2 MiB（与现在相同） | 8 MiB（大型套装的曲目表） |

错误是 `sources` 导出的哨兵值：`ErrNotFound`（外部明确说没有）、`ErrRateLimited`、`ErrUnavailable`、`ErrTryLater`、`ErrInvalidQuery`。

**哪些算“没匹配上”**：只有 `ErrNotFound` 与成功返回的空结果。后台调用**不会**得到 `ErrUnavailable`：断网、外部繁忙、响应异常一律是
`ErrTryLater`，子任务 C 不得把它记成一次失败的尝试——否则一次断网会把整个曲库标成匹配不上。ctx 取消原样返回 ctx 的错误。

### 3.3 限速器

默认每个源一个，按 `Declaration.ID` 取，进程内全局：同一进程里构造了几个适配器实例都共用它。一个许可串行放行，
两次放行的间隔不小于 `MinInterval`；有对话调用在等时，后台调用不取许可。重试的等待期间不占许可。
`sources` 导出可注入时钟与等待函数的限速器构造函数，适配器的构造参数可以换掉默认的限速器；测试不靠真实时间。

### 3.4 缓存

- 位置：`<数据目录>/sources/cache/<源 ID>/<键的前两位>/<键>.json`。键是“方法 + 规范化后的完整 URL”的 SHA-256 十六进制。
  **缓存目录为空字符串时不缓存**（管家包自己构造的默认实例、数据目录为空的测试装配都是这种情况）。
- 文件内容：`{stored_at, expires_at, status, body}`；`status` 是 200 或 404，`body` 是响应原文。写入先落临时文件再 rename。
- 文件里只有两种结果（B1-P4）：200 且有内容，30 天；404 与零结果，1 天。**写入发生在适配器确认响应结构可用之后**，
  结构不对的 200 不进缓存。
- 失败不落盘，只在进程内记 10 分钟，并且**只属于后台**：后台用尽重试后才记，期间后台的同一请求不出站、直接得到 `ErrTryLater`；
  对话调用既不写也不读它，每次都真实尝试。ctx 取消与放行回调的拒绝不记。
  （2026-11-22 实现审阅时修订：原先写的是对话也受它影响；MusicBrainz 的 503 很常见且通常一秒后就好，让对话里的重试十分钟内必然失败不合适。）
- 对话与后台共用。命中缓存不占限速许可；后台命中缓存之前仍先过放行回调。
- 清理：启动时与之后每 24 小时删除过期文件。读写缓存失败只记日志，按未命中处理。
- 这些文件可以随时删除，不进备份。有结果的缓存有效期内，对话读不到 MusicBrainz 上更新的编辑。

### 3.5 请求与日志

共用的请求函数做：放行回调（后台）→ 查缓存 → 限速 → 再过一次放行回调（后台）→ 经 `platform/egress`（`PublicOnly`，不跟随重定向）
发 GET → 按 3.2 处理状态与重试 → 解析 JSON → 适配器校验结构 → 写缓存。客户端工厂的形状保持 `func(egress.Options) (*http.Client, error)`，
每次请求调用一次并在用完后关闭空闲连接，与现在相同（现有测试靠它计数与替换）。
User-Agent 为 `ROOMusic/<构建版本或 dev> ( <buildinfo.ProjectURL> )`，`Accept: application/json`。

每次出站结束记一条日志：事件 `sources.request.finished`，字段只有源、域名、耗时、HTTP 状态或错误码、第几次尝试；缓存命中记
`sources.cache.hit`（源）。**不记**查询词、URL 路径与查询串、响应内容。

## 4. `sources/musicbrainz`

基础地址 `https://musicbrainz.org/ws/2`，所有请求带 `fmt=json`。构造参数：缓存目录（可空）、日志，以及测试用的客户端工厂、
基础地址与限速器；都不给时得到一个不缓存、使用全局限速器的实例。

### 4.1 实体级接口（管家的两个工具用）

```go
func (c *Client) Lookup(ctx, entity, mbid string, inc []string) (Entity, error)
func (c *Client) Search(ctx, entity, query string, limit int) ([]Entity, error)
```

`Entity` 是现在 `steward/web/metadata.go` 里 `mbEntity` 的超集（多出 ISRC、别名、碟的 disc ID 等字段）。实体与 include 的校验留在管家包里。

### 4.2 能力的实现

| 方法 | 请求 |
| --- | --- |
| `ReleaseByID` | `/release/<id>?inc=recordings+isrcs+artist-credits+labels+release-groups+media+discids` |
| `ReleasesByBarcode` | `/release?query=barcode:<12 位> OR barcode:<13 位>&limit=25`；输入先去掉非数字，12 位时补出前导 0 的 13 位，13 位且以 0 开头时补出 12 位，其他长度只查原值 |
| `ReleasesByCatalog` | `/release?query=catno:"<编号>"`，`label` 非空时加 `AND label:"<厂牌>"`；`limit=25` |
| `ReleasesByDisc` | `/discid/<本地算出的 disc ID>?toc=<TOC>&cdstubs=no&inc=artist-credits+labels+release-groups`；返回顶层有 `id` 时是精确命中，有 `release-count` 时是模糊命中；404 为空结果 |
| `SearchReleases` | `/release?query=release:"<标题>" AND artist:"<艺人>" AND tracks:<曲目数>`，空的条件不出现；`limit=10` |
| `RecordingByID` | `/recording/<id>?inc=artist-credits+isrcs+releases` |
| `RecordingsByISRC` | `/isrc/<ISRC>?inc=artist-credits+releases`；输入先规范化成 12 位大写，格式不对返回 `ErrInvalidQuery`；404 为空结果 |
| `SearchRecordings` | `/recording?query=recording:"<标题>" AND artist:"<艺人>"`，有时长时加 `AND dur:[<毫秒−3000> TO <毫秒+3000>]`；`limit=10` |
| `ArtistByID` | `/artist/<id>?inc=aliases` |
| `SearchArtists` | `/artist?query=artist:"<名字>"&limit=10` |

- 查询词放进引号前转义 Lucene 的特殊字符（`\ + - && || ! ( ) { } [ ] ^ " ~ * ? : /`）。
- MBID 输入只校验 36 位的 UUID 外形（不校验版本位：真实数据里有版本位为 3 的），转小写；不合法返回 `ErrInvalidQuery`，不出站。
- disc ID：把首轨号 1、末轨号、lead-out、99 个轨偏移（不足补 0）按 `%02X%02X%08X` 加 99 个 `%08X` 拼成大写十六进制 ASCII，
  做 SHA-1，Base64 后把 `+ / =` 换成 `. _ -`。导出为纯函数 `DiscID(DiscTOC) (string, error)`；TOC 的校验与服务端相同
  （1 到 99 轨、偏移严格递增、lead-out 大于最后一轨）。调研核实的例子（`research/musicbrainz-api-and-tags.md` A4）是测试的基准值。
- 条码的 `OR` 写法、`/isrc/` 的返回结构没有对真实服务核实过（调研的未确认项 1、12）：实现按文档与服务端源码写，
  父任务收尾的真实试用里核对。

### 4.3 管家工具的改接

`SearchMusic` 与 `GetMusic` 改为调用 `Client.Search`、`Client.Lookup`（对话身份），再投影成现有的 `Metadata`。
工具的名字、参数模式、输出字段、每用户每分钟 20 次的额度不变；新增的实体字段不进入工具输出。

`web.New` 的签名不变（它还被 `steward/httpapi` 与一处集成测试调用）：内部构造一个默认的适配器实例（不缓存）；
`app` 用 `(*Service).WithMusic(client)` 换成带缓存的共用实例。错误码的对应：

| `sources` 的错误 | 工具错误码 |
| --- | --- |
| `ErrRateLimited` | `rate_limited` |
| `ErrNotFound`、`ErrUnavailable`、`ErrTryLater` | `source_unavailable` |
| ctx 取消 | 原样返回 ctx 的错误 |

`cover-art` 的清单读取留在管家包，仍用 `platform/egress` 直接请求（B1-P6）。`Service` 里的 `musicLimiter`、包内的
`musicBrainzBaseURL` 与 `requestLimiter` 随之删除。日志事件 `steward.web.request.finished` 保留。

现有测试的处理：

- 工具级的用例（请求的地址与参数、输出、错误码、每用户额度、日志）只改注入方式（基础地址、客户端工厂、限速器经适配器的构造参数给），
  **断言不改**。这些用例用不缓存的实例，所以“同一查询发两次、出站两次”之类的断言照旧成立。
- 直接读写限速器未导出字段的白盒用例（`c8_test.go`、`c8_round2_test.go` 里排队、超时、间隔的几条）随限速器搬到 `sources` 包，
  断言的内容不变，只按新的类型名与构造方式改写。

## 5. 各源开关与闸门（`curation`）

### 5.1 数据

迁移 0060：

```sql
CREATE TABLE curation_source_settings (
    source TEXT PRIMARY KEY CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    enabled BOOLEAN NOT NULL,
    updated_by UUID NOT NULL REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

没有行时取声明的 `DefaultEnabled`。源的清单来自装配时注册的声明，不存库。

### 5.2 闸门

```go
type Sources struct{ … } // curation.NewSources(db, declarations, finders…)

func (s *Sources) Check(ctx, sourceID string) error
func (s *Sources) ReleaseFinder(sourceID string) (sources.ReleaseFinder, bool)
func (s *Sources) RecordingFinder(sourceID string) (sources.RecordingFinder, bool)
func (s *Sources) ArtistFinder(sourceID string) (sources.ArtistFinder, bool)
```

- 取到的能力是包了一层的实现：每个方法先 `Check`，再用 `sources.WithBackground(ctx, permit)` 把 ctx 标成后台，然后调用适配器；
  `permit` 就是对这个源的 `Check`。于是核对发生在：调用开始时、读缓存之前、每次出站之前（含排队之后与每次重试之前）。
- `Check` 在一个普通事务里核对：总开关开着、开启者是启用状态的管理员（原样复用 `loadSettings`，资格由 `identity.IsEnabledAdmin` 判断）、
  这个源的开关开着。（2026-11-22 实现审阅时修订：原先写的是只读事务，那样用不了带共享锁的 `loadSettings`，实现里出现了第二份“启用状态的管理员”判断；
  改回单一定义，共享锁到事务结束即释放。）
  前两项不满足返回 `operations.Conflict("curation_disabled", …)`（与 `Writer.Apply` 相同的错误码），源的开关关着返回
  `operations.Conflict("source_disabled", …)`；都不调用适配器。调用方据此区分全停还是只停这个源。
- 后台代码只从 `Sources` 取能力；`app` 不把适配器直接交给后台的调用方。本任务里 `Sources` 没有生产调用方，用测试里的调用验证。
- 管家持有适配器本身，不经过闸门（B1-P2）。

### 5.3 接口

| 接口 | 说明 |
| --- | --- |
| `GET /api/v1/admin/curation/sources` | `{items:[{id, name, capabilities, enabled, active, inactive_reason}]}`，按声明的注册顺序；`enabled` 是这个源自己的开关，`active` 是“后台现在能不能用它”，`inactive_reason` 为 `source_disabled`、`curation_disabled`、`enabler_unavailable` 或 null |
| `PUT /api/v1/admin/curation/sources/{id}` | `{enabled}`，返回同上的一项；未知的源 `404 not_found` |

管理员、同源检查、错误映射与现有的 `curation/httpapi` 相同。

## 6. 外部对应的存储（`curation`）

迁移 0060：

```sql
CREATE TABLE external_links (
    id UUID PRIMARY KEY,
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    object_type TEXT NOT NULL CHECK (object_type IN ('release','release_group','track','artist')),
    object_id UUID NOT NULL,
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
    method TEXT NOT NULL CHECK (method ~ '^[a-z0-9_]{1,32}$'),
    confidence TEXT NOT NULL CHECK (confidence ~ '^[a-z0-9_]{1,32}$'),
    operation_id UUID REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, object_type, object_id)
);
CREATE INDEX external_links_external_idx ON external_links (source, object_type, external_id);
```

- 表里的每一行都是生效的对应；ADR 0006 所说的“状态”在这里是“行在不在”。被撤销的对应留在操作记录里，子任务 C 需要别的状态时再加列。
- 一个本地对象在一个源里对应什么类型的外部实体由源决定：对 MusicBrainz，发行 → release，专辑（本地的 release group）→ release group，
  曲目 → recording，艺人 → artist。
- `method` 与 `confidence` 的取值表由子任务 C 的把握规则定义，这里只约束形状。
- `object_id` 不设外键：四种对象在四张表里，非测试代码不删除这四类对象。艺人合并靠重定向，被并入的艺人身上的对应仍在：
  子任务 C 按外部 ID 反查艺人时要解析重定向。
- 扫描、认领、人工修正的语句都不涉及这张表。

事务内的函数（没有生产调用方，供子任务 C 的执行器使用）：

```go
type Link struct{ Source, ObjectType, ObjectID, ExternalID, Method, Confidence, OperationID string }

func PutLink(ctx, tx, link Link) (previous *Link, err error) // 写入或替换，返回被替换的那一行
func RemoveLink(ctx, tx, source, objectType, objectID string) (previous *Link, err error)
func LinksOf(ctx, q, objectType string, objectIDs []string) ([]Link, error)
func ObjectsLinkedTo(ctx, q, source, objectType, externalID string) ([]string, error)
```

## 7. 标签里的标识

### 7.1 种类与键

| 种类 | 归一化后的键名 | 值 |
| --- | --- | --- |
| `mb_release` | `musicbrainzalbumid` | MBID |
| `mb_release_group` | `musicbrainzreleasegroupid` | MBID |
| `mb_recording` | `musicbrainztrackid`、`musicbrainzrecordingid`；ID3 的 `UFID` 帧且 owner 为 `http://musicbrainz.org` | MBID |
| `mb_track` | `musicbrainzreleasetrackid` | MBID |
| `mb_artist` | `musicbrainzartistid` | MBID，多值 |
| `mb_album_artist` | `musicbrainzalbumartistid` | MBID，多值 |
| `mb_work` | `musicbrainzworkid` | MBID，多值 |
| `mb_disc` | `musicbrainzdiscid` | disc ID |
| `acoustid` | `acoustidid` | UUID，多值 |
| `isrc` | `isrc`；ID3 的 `TSRC` 帧 | ISRC，多值 |

- **键名归一化只用于这张表**：去掉所有非字母数字字符并转小写后比对，所以 `MUSICBRAINZ_ALBUMID`、`MusicBrainz Album Id`、
  `Musicbrainz_Albumid` 等价。现有标签键的匹配规则不变。
- “Track Id” 是录音 ID，这是 MusicBrainz 标签的历史命名，不是笔误。
- 值的规则：
  - MBID 与 AcoustID：在值里用 UUID 外形的正则提取**全部**匹配（兼容 NUL、`/`、`; ` 与重复出现），转小写，丢弃全 0。
  - ISRC：按 NUL、`/`、`;`、`,` 与空白切分，去掉连字符，转大写，必须匹配 `^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$`。
  - disc ID：必须匹配 `^[A-Za-z0-9._-]{27}-$`。
  - 不合规则的值丢弃，不让文件解析失败。同一种类内去重并保持出现顺序，每条曲目每个种类至多 32 个。
- 不保存 AcoustID 指纹（B1-P7）。

### 7.2 解析入口

| 入口 | 改动 |
| --- | --- |
| `setAudioTag`（Vorbis、APEv2） | 在现有分派之前先按 7.1 收集标识；`ISRC` 键仍照旧写 `observation.ISRC` |
| ID3 帧白名单 | 增加 `UFID`：载荷是“owner 字符串 + NUL + 二进制标识”，只认 owner `http://musicbrainz.org`，标识按 ASCII 读 |
| ID3 `TSRC` | 收集全部值（现在只取第一个 NUL 之前的）；`observation.ISRC` 仍取第一个 |
| ID3 `TXXX` 描述白名单 | 归一化后命中 7.1 的描述只收集标识（`TXXX:ISRC` 也只进标识，不写 `observation.ISRC`）；另放行 `BARCODE`、`UPC`、`EAN`、`CATALOGNUMBER`、`CATALOG`、`CATALOGNO`（B1-P8），交给 `setAudioTag` 写条码与厂牌编号 |
| M4A 自由格式名单 | 归一化后命中 7.1 的名字只收集标识（`----:ISRC` 同上）；另放行 `BARCODE`、`EAN`、`CATALOGNUMBER`、`CATALOG`、`CATALOGNO`（`LABEL`、`UPC` 已有） |

ID3 的入口同时覆盖 MP3、WAV 内嵌 ID3、AIFF、DSF、DFF。WAV 的 RIFF INFO 没有这些标识的位置，不改。
厂牌不动：ID3 里 `TPUB` 是无条件赋值（`mp3.go:155`），不新增 `TXXX:LABEL`。
Vorbis 与 APEv2 的 `ISRC` 键照旧写 `observation.ISRC`（后值覆盖前值），同时全部值进标识。

### 7.3 结构与链路

- `catalog/metadata` 新增 `Identifier{Kind, Value string}` 与规范化函数（7.1 的规则放在这里，解析器与落库共用）。
- `parser.AudioObservation.Identifiers []metadata.Identifier`；`ingest.SourceObservation.Identifiers`，JSON 标签
  `identifiers,omitempty`，按字母序插在现有字段之间。
- `buildPhysicalSourceObservation` 原样传递。`buildCueVirtualObservation` 只传专辑级的种类
  （`mb_release`、`mb_release_group`、`mb_album_artist`、`mb_disc`）：父文件的录音、曲目、艺人、作品 ID、ISRC 与 AcoustID 属于整轨，不属于某一条虚拟轨。
  CUE 轨自己的 ISRC 仍在 `tracks.cue_isrc`。只有一条虚拟轨、保留物理轨的情况沿用物理观察，不需要另外处理。
- 落库：`PersistCandidateWithFeedback` 在 `persistTrack` 之后，与 `track_observations` 相同的写法——按 `track_id` 先删后插。

迁移 0059：

```sql
CREATE TABLE track_identifiers (
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('mb_release','mb_release_group','mb_recording','mb_track','mb_artist',
        'mb_album_artist','mb_work','mb_disc','acoustid','isrc')),
    value TEXT NOT NULL CHECK (length(value) BETWEEN 1 AND 64),
    ordinal SMALLINT NOT NULL,
    PRIMARY KEY (track_id, kind, value)
);
CREATE INDEX track_identifiers_value_idx ON track_identifiers (kind, value);
```

这张表归扫描所有：只有落库写它，自动整理只读。

### 7.4 版本与升级后的行为

- `parser.CommonVersion` 3 → 4（观察结构与共用解析规则都变了）。规范 `library-scan-incremental.md` 里记录版本现值与原因的那一行同步改。
- `ingest.GroupingVersion` 不变：成员签名含 `ParserVersion`，升解析器版本已经让全部候选重写一次。
- 升级后的第一次扫描（手动、定时增量或完整都算）重新解析全部文件的标签并重写全部候选；内容指纹照常复用，曲目与发行的 ID 不变。
  这一次扫描比平时慢，之后恢复。
- 候选签名：`Identifiers` 带 JSON 标签，自动进入签名的元数据摘要；用了 `omitempty`，没有标识的观察摘要不变。
- 扫描反馈里“曲目有变化”的摘要不纳入标识，否则升级后的第一次扫描会把每条带标识的曲目都报成有变化。
- B1-P8 的后果：这类 MP3 与 M4A 所在发行的条码、厂牌编号可能从空变成有值，字段决定与“需要关注”随之变化。

## 8. 装配

`app` 里：用数据目录构造一个 `musicbrainz.Client`（数据目录为空时不缓存）并启动缓存清理；经 `WithMusic` 交给管家的 `web.Service`，
并交给 `curation.NewSources`（闸门）；`curationapi` 多接收 `Sources`。进程关停时清理协程随应用的 context 退出。

## 9. 兼容与回退

- 迁移 0059、0060 只新增表。回退脚本删除这三张表；`track_identifiers` 可由重扫重建，`external_links` 与源开关在本任务里没有生产写入方。
- 回退代码后，解析缓存里的版本号比代码新，下一次扫描按版本不等重新解析一次，没有其他影响。
- 缓存目录可以整个删除。

## 10. 取舍

| 取舍 | 选择 | 放弃的做法与原因 |
| --- | --- | --- |
| 外部对应的写入 | 本任务只建存储 | 现在就做成受治理操作：操作的粒度取决于匹配流程，容易返工 |
| 开关管不管对话工具 | 不管 | 一并管：默认关的开关会关掉 M6 已有的功能 |
| 缓存的位置 | 数据目录的文件 | 数据库表：会进备份，与 ADR 的默认 7 不符 |
| 标签标识放哪 | 逐轨的子表 | `tracks` 上加列：多值的种类放不下；`releases` 上加列：同一发行里各文件可能不一致，这种不一致本身是之后拆分发行的依据 |
| 键名比对 | 归一化后比对，只用于标识 | 逐个列举写法：各工具的大小写、空格与下划线组合太多 |
| 能力接口的范围 | 只定义有实现的三种 | 六种一次定完：没有实现的接口无从验证 |
| 重试 | 只有后台重试 | 对话也重试：一次工具调用会卡住几十秒 |

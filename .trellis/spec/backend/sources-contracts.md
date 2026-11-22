# 外部源合同

## 1. 范围与触发

M7 子任务 B1（2026-11）把向外部音乐数据源发请求这件事从管家里分出来：出站客户端迁到平台层，新增外部源的共用部分
（`backend/internal/sources`）与第一个适配器 MusicBrainz（`backend/internal/sources/musicbrainz`）。管家的两个音乐资料工具与
后台自动整理共用同一个适配器实例。本任务**没有后台调用方**：匹配与自动写入在子任务 C。

新增或修改以下内容时必须阅读并更新本文件：`backend/internal/platform/egress`、`backend/internal/sources` 及其子包、新的外部源、
能力接口、限速、缓存、重试、外部源的日志。各源的开关、闸门与外部对应的存储在[自动整理合同](./curation-contracts.md)第 11、12 节；
管家工具的输出在[管家联网合同](./steward-web-contracts.md)；标签里的外部标识在[扫描标签合同](./library-scan-tags.md)。

决策依据：[ADR 0006](../../../docs/architecture/adr/0006-auto-curation-and-sources.md) 第 6、8、10 节。

## 2. 包与依赖

| 包 | 职责 | 允许的内部依赖 |
| --- | --- | --- |
| `platform/egress` | 出站 HTTP 客户端与地址策略（原 `steward/egress`，行为不变） | 无 |
| `sources` | 声明、能力接口与中立类型、调用身份、限速器、文件缓存、带重试的 JSON 请求、日志 | `platform/egress`、`platform/buildinfo` |
| `sources/musicbrainz` | 实体级读取与查询、三种能力的实现、disc ID | `sources`、`platform/uuid` |

`sources` 及其子包**不依赖** `curation`、`steward`、`catalog` 与数据库：它们只管怎样向外部问。“现在能不能问”由 `curation.Sources`
（闸门）决定。新增一个源就是在 `sources/` 下加一个子包并在 `app` 里注册，不改调用方。

## 3. 声明与能力

```go
type Declaration struct {
    ID             string        // ^[a-z0-9._-]{1,64}$；与操作记录 origin.provider 是同一个标识
    Name           string
    Capabilities   []Capability  // release_lookup | recording_lookup | artist_lookup
    DefaultEnabled bool
    MinInterval    time.Duration // 全实例两次出站的最小间隔
}
```

能力是三个窄接口：`ReleaseFinder`（按 ID、条码、厂牌编号、光盘目录、标题艺人曲目数）、`RecordingFinder`（按 ID、ISRC、标题艺人时长）、
`ArtistFinder`（按 ID、名字）。程序层只面向这些接口与中立类型（`Release`、`Track`、`Recording`、`Artist`、`Credit`、`DiscTOC`、`DiscMatch`）。

- `Release.Complete` 为真表示来自按 ID 读取，带曲目表；查找与搜索的结果只有每张碟的 `TrackCount`，要曲目表得再按 ID 读取。
- `DiscMatch.Exact` 为真表示 disc ID 命中；为假是按时长的模糊结果（至多 25 条，不知道是否被截断）。两者的可靠程度差别很大，不能混用。
- 搜索的 `score` 不进入中立类型：它只是一次结果内的相对排名。
- 条码与 ISRC 在外部都不唯一，返回的是列表。
- 指纹识别、封面、歌词的能力接口随子任务 B2、B3 增加。

## 4. 调用的两种身份

没有标记的调用是**对话**（管家工具）；`sources.WithBackground(ctx, permit)` 标成**后台**。`permit` 是放行回调，
在调用开始时、读缓存之前、每次出站之前（排队之后、每次重试之前）各调用一次，返回错误时这次调用以该错误结束，不出站。

| | 对话 | 后台 |
| --- | --- | --- |
| 限速排队 | 优先；排队超过 10 秒返回 `ErrRateLimited` | 让对话先走；一直等到轮到或 ctx 结束 |
| 503、429、网络错误 | 不重试，`ErrUnavailable` | 按 `Retry-After`（秒数或 HTTP 日期）等待，没有时指数退避加抖动，单次至多 60 秒；至多重试 4 次，之后 `ErrTryLater` |
| 其他非 200（404 除外）、响应结构不对、地址策略拒绝 | `ErrUnavailable` | `ErrTryLater`，不重试 |
| 单次请求时限 | 20 秒 | 20 秒 |
| 响应读取上限 | 2 MiB | 8 MiB |
| 失败的进程内记忆 | 不读不写，每次都真实尝试 | 用尽重试后记 10 分钟，期间同一请求直接 `ErrTryLater` |

哨兵错误：`ErrNotFound`、`ErrRateLimited`、`ErrUnavailable`、`ErrTryLater`、`ErrInvalidQuery`。

**哪些算“没匹配上”**：只有 `ErrNotFound` 与成功返回的空结果。后台调用不会得到 `ErrUnavailable`；断网、外部繁忙、响应异常一律是
`ErrTryLater`，调用方不得把它记成一次失败的尝试。ctx 取消与放行回调的拒绝原样返回，不记入任何缓存。

## 5. 限速、缓存与请求

- **限速器**：默认每个源一个，按 `Declaration.ID` 取，进程内全局；同一进程里构造几个适配器实例都共用它。一个许可串行放行，
  两次出站的间隔不小于 `MinInterval`（从发出时起算）；有对话在等时后台不取许可；重试的等待不占许可。
  `sources.NewLimiter(interval, now, after)` 供测试注入时钟。
- **缓存**：`<数据目录>/sources/cache/<源 ID>/<键的前两位>/<键>.json`，键是 `GET ` 加规范化 URL 的 SHA-256。
  文件内容 `{stored_at, expires_at, status, body}`，只存两种结果：200 且结构可用（30 天；空结果 1 天）与 404（1 天）。
  **写入发生在适配器确认结构可用之后**。数据目录为空字符串时不缓存（管家包自己构造的默认实例与部分测试装配）。
  命中缓存不占限速许可。启动时与之后每 24 小时清理过期文件。读写失败只记日志，按未命中处理。
  这些文件可以随时删除，不进备份；有结果的缓存有效期内，对话读不到外部更新的编辑。
- **请求**：`platform/egress` 的 `PublicOnly`，不跟随重定向；`Accept: application/json`；
  User-Agent 为 `ROOMusic/<构建版本或 dev> ( <buildinfo.ProjectURL> )`，不含实例信息。
  客户端工厂的形状是 `func(egress.Options) (*http.Client, error)`，每次出站调用一次并在用完后关闭空闲连接。
- **日志**：出站结束记 `sources.request.finished`（源、域名、耗时、HTTP 状态或固定错误码、第几次尝试）；缓存命中记
  `sources.cache.hit`（源）；缓存读写失败记 `sources.cache.error`。**不记**查询词、URL 路径与查询串、响应内容。

## 6. MusicBrainz 适配器

声明：`{ID:"musicbrainz", Name:"MusicBrainz", 三种能力, DefaultEnabled:true, MinInterval:1s}`。
基础地址 `https://musicbrainz.org/ws/2`，所有请求带 `fmt=json`。

| 方法 | 请求 |
| --- | --- |
| `ReleaseByID` | `/release/<id>`，`inc=recordings isrcs artist-credits labels release-groups media discids` |
| `ReleasesByBarcode` | `/release?query=barcode:<12 位> OR barcode:0<12 位>`，`limit=25`；输入先去掉非数字，13 位且以 0 开头时去掉前导 0；其他长度只查原值 |
| `ReleasesByCatalog` | `/release?query=catno:"…"`，厂牌非空时加 `AND label:"…"`；`limit=25` |
| `ReleasesByDisc` | `/discid/<本地算出的 disc ID>?toc=…&cdstubs=no`，`inc=artist-credits labels release-groups`；顶层有 `id` 是精确命中，有 `release-count` 是模糊命中；404 为空结果 |
| `SearchReleases` | `/release?query=release:"…" AND artist:"…" AND tracks:<n>`，空的条件不出现；`limit=10` |
| `RecordingByID` | `/recording/<id>`，`inc=artist-credits isrcs releases` |
| `RecordingsByISRC` | `/isrc/<ISRC>`，`inc=artist-credits releases`；输入规范化成 12 位大写，格式不对 `ErrInvalidQuery`；404 为空结果 |
| `SearchRecordings` | `/recording?query=recording:"…" AND artist:"…"`，有时长时加 `dur:[毫秒−3000 TO 毫秒+3000]`；`limit=10` |
| `ArtistByID` | `/artist/<id>`，`inc=aliases` |
| `SearchArtists` | `/artist?query=artist:"…"`，`limit=10` |

- 查询词放进引号前转义 Lucene 的特殊字符。没有任何条件的查询返回 `ErrInvalidQuery`，不出站。
- MBID 只校验 36 位的 UUID 外形并转小写（真实数据里有版本位为 3 的），不合法返回 `ErrInvalidQuery`。
- `DiscID(DiscTOC)` 是纯函数：首轨号 1、末轨号、lead-out、99 个轨偏移（不足补 0）按大写十六进制拼接后做 SHA-1，
  Base64 并把 `+ / =` 换成 `. _ -`。TOC 的偏移单位是 CD 帧（1/75 秒），含 150 帧 lead-in；1 到 99 轨、偏移严格递增、lead-out 大于最后一轨。
- 实体级接口 `Lookup(entity, mbid, inc)` 与 `Search(entity, query, limit)` 返回 `Entity`（外部响应里用得上的字段的并集），
  只给管家的两个工具用：它们是 MusicBrainz 专用工具，自由查询串与 include 列表表达不成中立的能力。
- 结构校验：按 ID 读取的结果必须有 `id`；带曲目表读取发行时每张碟必须有 `tracks`；列表结果的每一项必须有 `id`。不满足的 200 不进缓存。
- **没有对真实服务核实的两点**（父任务收尾的真实试用里核对）：条码的 `OR` 写法；`/isrc/` 的返回结构（按服务端源码写）。

## 7. 发给外部的数据

MusicBrainz 收到的只有：外部 ID、条码、厂牌与编号、ISRC、标题与艺人的查询词、曲目数、由各轨时长推出的光盘目录。
不发送音频、文件路径与实例标识。外部返回的内容一律是不可信文本，进入模型之前按管家的规则加来源标记。

## 8. 必需测试与断言点

| 断言点 | 位置 |
| --- | --- |
| 限速：间隔、对话优先、对话排队超时、后台等待 | `backend/internal/sources` 的限速器用例 |
| 缓存：三种有效期、不落盘的失败、目录为空、目录不可写、结构不对的 200 | `backend/internal/sources` 的缓存与请求用例 |
| 重试与错误：`Retry-After`、退避、上限、后台的断网是“稍后再试”、对话不重试 | 同上 |
| 十个能力方法的请求形式与解析；disc ID 的基准值 | `backend/internal/sources/musicbrainz` |
| 管家工具的输出与错误码不变 | `backend/internal/steward/web` 现有用例 |
| 闸门、源开关接口、外部对应的存储 | 见[自动整理合同](./curation-contracts.md)第 13 节 |
| 包依赖方向 | `TestPackageDependencyDirections` |

自动化测试一律用本地的受控服务，不请求真实的外部源；限速、缓存与重试的等待用注入的时钟。

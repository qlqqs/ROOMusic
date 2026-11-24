# 外部源合同

## 1. 范围与触发

M7 子任务 B1（2026-11）把向外部音乐数据源发请求这件事从管家里分出来：出站客户端迁到平台层，新增外部源的共用部分
（`backend/internal/sources`）与第一个适配器 MusicBrainz（`backend/internal/sources/musicbrainz`）。管家的两个音乐资料工具与
后台自动整理共用同一个适配器实例。子任务 B2 加了封面与歌词两种能力、请求器的重定向与取字节，以及 Cover Art Archive、LRCLIB 两个适配器
（第 7、8 节）。后台调用方是子任务 C 的程序层（[自动整理合同](./curation-contracts.md)第 15 节）。

新增或修改以下内容时必须阅读并更新本文件：`backend/internal/platform/egress`、`backend/internal/sources` 及其子包、新的外部源、
能力接口、限速、缓存、重试、重定向、外部源的日志。各源的开关、闸门、外部对应与外部封面歌词的存储在[自动整理合同](./curation-contracts.md)第 11、12、14 节；
管家工具的输出在[管家联网合同](./steward-web-contracts.md)；标签里的外部标识在[扫描标签合同](./library-scan-tags.md)。

决策依据：[ADR 0006](../../../docs/architecture/adr/0006-auto-curation-and-sources.md) 第 6、8、10 节。

## 2. 包与依赖

| 包 | 职责 | 允许的内部依赖 |
| --- | --- | --- |
| `platform/egress` | 出站 HTTP 客户端与地址策略（原 `steward/egress`，行为不变） | 无 |
| `sources` | 声明、能力接口与中立类型、调用身份、限速器、文件缓存、带重试的 JSON 请求、日志 | `platform/egress`、`platform/buildinfo` |
| `sources/musicbrainz` | 实体级读取与查询、三种能力的实现、disc ID | `sources`、`platform/uuid` |
| `sources/coverartarchive` | 图片清单、选主封面、下载图片字节 | `sources`、`sources/musicbrainz`（只取它的声明，用来共用限速器）、`platform/uuid` |
| `sources/lrclib` | 歌词的精确查询与候选搜索 | `sources` |

导入 `sources` 及其子包的有 `curation`（闸门）、`steward/web`（MusicBrainz 与 Cover Art Archive）与 `app`（装配）。

`sources` 及其子包**不依赖** `curation`、`steward`、`catalog` 与数据库：它们只管怎样向外部问。“现在能不能问”由 `curation.Sources`
（闸门）决定。新增一个源就是在 `sources/` 下加一个子包并在 `app` 里注册，不改调用方。

艺人资料的五个子包及其请求见第 8b 节。它们依赖 `sources`，其中按 MBID 请求的 Wikidata、Fanart.tv 与 TheAudioDB 还依赖 `platform/uuid`。

## 3. 声明与能力

```go
type Declaration struct {
    ID             string        // ^[a-z0-9._-]{1,64}$；与操作记录 origin.provider 是同一个标识
    Name           string
    Capabilities   []Capability  // release_lookup | recording_lookup | artist_lookup | cover_art | lyrics | fingerprint_lookup | artist_image | artist_bio
    DefaultEnabled bool
    OptionalKeys   []string      // Keys 中可以缺省的密钥名
    MinInterval    time.Duration // 全实例两次出站的最小间隔
    Keys           []string      // 需要管理员填写的密钥名（有序）；各源名称与是否必填见对应来源小节
}
```

发行、录音、艺人、封面和歌词的窄接口为`ReleaseFinder`（按 ID、条码、厂牌编号、光盘目录、标题艺人曲目数）、`RecordingFinder`（按 ID、ISRC、标题艺人时长）、
`ArtistFinder`（按 ID、名字）、`CoverFinder`（`Covers` 读图片清单、`FrontCover` 选出主封面并下载）、`LyricsFinder`（`LyricsFor` 精确查询、
`SearchLyrics` 候选）。程序层只面向这些接口与中立类型（`Release`、`Track`、`Recording`、`Artist`、`Credit`、`DiscTOC`、`DiscMatch`、
`CoverTarget`、`CoverImage`、`CoverFile`、`LyricsQuery`、`Lyrics`）。

- `Release.Complete` 为真表示来自按 ID 读取，带曲目表；查找与搜索的结果只有每张碟的 `TrackCount`，要曲目表得再按 ID 读取。
- `DiscMatch.Exact` 为真表示 disc ID 命中；为假是按时长的模糊结果（至多 25 条，不知道是否被截断）。两者的可靠程度差别很大，不能混用。
- 搜索的 `score` 不进入中立类型：它只是一次结果内的相对排名。
- 条码与 ISRC 在外部都不唯一，返回的是列表。
- `CoverImage.ReleaseID` 是图片实际所属的外部发行（release group 的清单由它指明来源，合并过的发行由它得到新 ID），清单没给时为空；
  `ID` 是源里的图片编号，规范成十进制数字串，不是数字时为空（这一项仍然返回，只是不能下载）。
- `sources` 不校验图片内容：它只保证字节数不超过上限、来自放行的主机。格式与宽高由落库的一方（`catalog/artwork`）校验。
- `Lyrics` 原样转达外部的记录：同步与纯文本两份文本、纯音乐标志、记录自己的标题、艺人、专辑与时长。取舍（时长筛选、写法差异）不在适配器里。
- 指纹识别（B3）：`FingerprintFinder.LookupFingerprint(ctx, Fingerprint{Value, DurationSeconds})` 返回 `[]FingerprintMatch{ID, Score, Recordings}`；
  `FingerprintRecording` 带 `ID`、`Title`、`LengthMS`、`Artists`、`ReleaseIDs` 与 `Releases`（`FingerprintRelease{ID, Title, Date, Country, Artists, TrackCount, MediumCount}`，
  后两项外部没给时为 0）。录音可能是外部已合并掉的旧 ID，调用方要用 MusicBrainz 按 ID 读回正式数据再判定。
- 密钥错误：`*InvalidKeyError{Name}`，`errors.Is(err, ErrInvalidKey)` 成立；`Name` 指出是哪一把（闸门只把那一把标为无效）。

## 4. 调用的两种身份

没有标记的调用是**对话**（管家工具）；`sources.WithBackground(ctx, permit)` 标成**后台**。`permit` 是放行回调，
在调用开始时、读缓存之前、每次出站之前（排队之后、每次重试之前）各调用一次，返回错误时这次调用以该错误结束，不出站。

| | 对话 | 后台 |
| --- | --- | --- |
| 限速排队 | 优先；排队超过 10 秒返回 `ErrRateLimited` | 让对话先走；一直等到轮到或 ctx 结束 |
| 503、429、网络错误 | 不重试，`ErrUnavailable` | 按 `Retry-After`（秒数或 HTTP 日期）等待，没有时指数退避加抖动，单次至多 60 秒；至多重试 4 次，之后 `ErrTryLater` |
| 其他非 200（404 除外）、响应结构不对、地址策略拒绝 | `ErrUnavailable` | `ErrTryLater`，不重试 |
| 单次请求时限 | `GetJSON` 20 秒，`GetBytes` 60 秒 | 同左 |
| 响应读取上限 | `GetJSON` 2 MiB | `GetJSON` 8 MiB；`GetBytes` 由调用方给 |
| 失败的进程内记忆 | 不读不写，每次都真实尝试 | 用尽重试后记 10 分钟，期间同一请求直接 `ErrTryLater` |

哨兵错误：`ErrNotFound`、`ErrRateLimited`、`ErrUnavailable`、`ErrTryLater`、`ErrInvalidQuery`、`ErrInvalidKey`（B3，不重试、不缓存、不记失败记忆）。

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
- **请求**：默认用 `platform/egress` 的 `PublicOnly`；`Accept: application/json`（`GetBytes` 由调用方给）；
  User-Agent 为 `ROOMusic/<构建版本或 dev> ( <buildinfo.ProjectURL> )`，不含实例信息。
  客户端工厂的形状是 `func(egress.Options) (*http.Client, error)`，每次出站调用一次并在用完后关闭空闲连接。
- **重定向**：默认不跟随。`Options.MaxRedirects > 0` 时跟随，`Options.AllowRedirect(目标地址)` 决定每一跳是否放行。每一跳之前依次检查：
  egress 自带的协议与次数；目标是 `https`（上一跳本身是 `http` 时允许 `http`，测试用）；不带用户名密码；端口为空、443 或精确匹配配置的 mock 主机和有效端口；`AllowRedirect` 返回真；
  后台的放行回调。任何一项不满足，这次调用以所属身份的失败哨兵结束（同时 `errors.Is(err, egress.ErrDenied)` 成立），**不重试、不记失败记忆**。
  每一跳带 `Accept` 与 `User-Agent`，维基媒体请求还保留 `Accept-Encoding` 和设置过的 `Accept-Language`；当前出站策略对每一跳照常生效；整条跳转链占一个限速许可；缓存键是最初请求的地址；
  日志里的域名是最初请求的域名。包装 `CheckRedirect` 时复制工厂返回的客户端，不原地修改。
- **`GetBytes(ctx, address, accept, maxBytes)`**：取二进制响应，不读写响应缓存；限速、调用身份、放行回调、重试、错误与失败记忆的规则与 `GetJSON` 相同
  （失败记忆同样要求有数据目录）。`Content-Length` 或实际读到的字节超过 `maxBytes` 按“响应结构不对”处理。404 是 `ErrNotFound`。
- **`PostForm(ctx, address, cacheInput, form, decode)`**（B3）：POST、`application/x-www-form-urlencoded`、`Content-Encoding: gzip`；
  `form` 在每次出站前（含每次重试）调用一次重新取表单（密钥在这里读，换了密钥的重试用新值），请求体每次重新压缩。
  缓存键是 `POST ` 加规范化 URL、NUL、`cacheInput`：`cacheInput` 只含决定结果的参数（AcoustID 是时长与指纹，**不含密钥**）。
  `decode(status, body)` 收到包括非 200 在内的响应，返回 `(empty, retry, err)`：`err == nil` 才写缓存（有效期规则同 `GetJSON`）；`retry` 为真按后台的重试规则退避；
  其余错误原样返回。GET 的缓存键不变（`GET ` 加规范化 URL），已有缓存文件继续有效。
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
| `ArtistLinks` | `/artist/<id>`，`inc=url-rels`，从 wikidata、wikipedia 关系投影 Q 号与中文、英文条目标题 |
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

## 7. Cover Art Archive 适配器（子任务 B2）

声明：`{ID:"coverartarchive", Name:"Cover Art Archive", [cover_art], DefaultEnabled:true, MinInterval:1s}`。基础地址 `https://coverartarchive.org`。
它的每个端点都是三跳：`coverartarchive.org` 307 → `archive.org` 302 → `*.archive.org` 200（第三跳的主机名不固定）。

适配器有两个请求器：

| 用途 | 限速器 | 重定向的放行范围 | 缓存 |
| --- | --- | --- | --- |
| 清单 | **MusicBrainz 的限速器**（`sources.DefaultLimiter(musicbrainz.Declaration())`；`Options.ManifestLimiter` 可注入） | 至多 5 跳；主机是 `coverartarchive.org`、`archive.org` 或 `*.archive.org`，且路径以 `/index.json` 结尾 | 200 存 30 天，404 存 1 天 |
| 图片 | 本源自己的限速器（`Options.Limiter` 可注入） | 至多 5 跳；主机同上，路径不限 | 不缓存响应；有数据目录时使用失败记忆 |

- `Covers(target)`：`GET <base>/<release|release-group>/<mbid>/`。MBID 校验 UUID 外形并转小写（大写会被服务拒绝），不合法 `ErrInvalidQuery`。
  结构校验只有一条：顶层 `images` 是数组。`id` 接受数字或字符串；`ReleaseID` 取顶层 `release` 地址的最后一段；`thumbnails` 是 null 时保持 nil。
  404 是空切片，不是错误。
- `FrontCover(target)`：用 `sources.FrontImage` 选（程序层下载前查标记也用它，保证选的是同一张）——只在 `ID` 非空的项里选，`front` 为真的第一张，没有则类型含 `Front` 的第一张，都没有 `ErrNotFound`。
  清单没给 `ReleaseID` 时：目标是 release 就用目标的 ID，目标是 release group 则 `ErrNotFound`。
- 下载地址**由适配器自己拼**，不用清单里的地址（旧清单里是 `http://`）：`<base>/release/<ReleaseID>/<ID>-1200`，404 依次试 `-500`、`-250`、
  不带后缀的原图。每一档上限 16 MiB（与 `catalog/artwork` 的上限相同）。原图的 `Content-Type` 是 `application/pdf` 或内容以 `%PDF` 开头时视为没有。
  四档都 404 是 `ErrNotFound`；非 404 的失败原样返回，不再试下一档。缩略图恒为 JPEG；1200 档可能尚未生成。
- **限速的取法**：清单与 MusicBrainz 合计每秒 1 次（父任务的验收要求）。图片的第一跳也经过 MetaBrainz 的网关，所以对这个网关合计每秒 2 次；
  Cover Art Archive 的文档写明目前没有限速规则，这一点是明确接受的。
- 管家的 `get_music_metadata` 带 `cover-art` 时调用同一个适配器的 `Covers`（见[管家联网合同](./steward-web-contracts.md)）。
- 没有对真实服务核实的：合并过的发行、缺失的缩略图、archive.org 一侧的限流返回码（父任务收尾的真实试用里核对）。

## 8. LRCLIB 适配器（子任务 B2）

声明：`{ID:"lrclib", Name:"LRCLIB", [lyrics], DefaultEnabled:true, MinInterval:1s}`（文档的上限是带应用标识的 User-Agent 每分钟约 90 次）。
基础地址 `https://lrclib.net`，不跟随重定向。

| 方法 | 请求 | 说明 |
| --- | --- | --- |
| `LyricsFor` | `/api/get?track_name=&artist_name=`，专辑非空加 `album_name`，时长在 1–3600 秒内加 `duration` | 标题或艺人为空 `ErrInvalidQuery`；404 是 `ErrNotFound`（缓存 1 天）；结构校验：有数字 `id` |
| `SearchLyrics` | `/api/search?track_name=`，艺人、专辑非空时加 `artist_name`、`album_name` | 标题为空 `ErrInvalidQuery`；至多 20 条；结构校验：顶层是数组、每项有 `id` |

- 外部的精确查询是“规范化后全等”加时长 ±2 秒：只处理 ASCII 标点与大小写，简繁体、全角标点、专辑名的写法差异都会导致查不到。
  怎样放宽（去掉专辑、改用搜索再按时长筛选）由调用方决定，不在适配器里。
- 纯音乐的记录 `Instrumental` 为真，两份歌词都是空串。

## 8a. AcoustID 适配器（子任务 B3）

声明：`{ID:"acoustid", Name:"AcoustID", [fingerprint_lookup], DefaultEnabled:true, MinInterval:334ms, Keys:[user]}`；没有内置应用密钥的构建为 `Keys:[application, user]`。
条款：每秒不超过 3 次；仅限非商业免费；每个应用注册自己的应用密钥；2027-01-01 起在 acoustid.org 注册的应用查询还必须带用户密钥（上游 2026-11-19 公告）。
按上游推荐的做法（B3 补充）：ROOMusic 作为一个应用注册，发布镜像内置这个应用的密钥，管理员只填自己的用户密钥。基础地址 `https://api.acoustid.org`，不跟随重定向。

- **内置应用密钥**：未初始化的包级 `var bundledClientKey string`，由链接参数 `-X …/sources/acoustid.bundledClientKey=` 注入（见[部署环境合同](./deployment-environment-contracts.md)）；
  仓库与测试构建里为空，即“没有内置”。有内置时表单的 `client` 用它，不读管理员的应用密钥；库里早先存的 `application` 一行保留、不读不显示，
  `DELETE …/keys/application` 返回 404，换回没有内置的构建时照旧生效。
- **内置密钥被拒**（有内置时码 4、17）：管理员无法修复。客户端在本进程内记下（`BundledKeyRejected()`），只在第一次记 error 级日志
  `sources.acoustid.bundled_key_rejected`（不含密钥），这次仍返回 `InvalidKeyError{Name:"application"}`；闸门不标任何一把（`application` 不在声明里），
  源随之以 `bundled_key_rejected` 不生效，直到换上新镜像重启。

- 请求：`PostForm` 到 `/v2/lookup`，表单恰好六项：`client`（应用密钥）、`user`（用户密钥）、`duration`（整首时长，整数秒）、`fingerprint`、
  `meta=recordings releases`、`format=json`。`Options.Keys(ctx)` 每次出站前取声明里的密钥。
- 映射：只接受 `status=ok`；去掉没有录音的结果；录音按 ID 去重，取最高分；发行带回曲目数与碟数。
- 错误：HTTP 429 或码 14、HTTP 503 或码 13、码 5 → 退避重试，用尽 `ErrTryLater`；码 3、8 → `ErrInvalidQuery`（空结果）；
  码 4、17 → `InvalidKeyError{Name:"application"}`（有内置时另见上文）；码 6 → `InvalidKeyError{Name:"user"}`；其余 → `ErrTryLater`。
- 密钥状态的回写（`unchecked`→`valid`、某一把→`invalid`）由闸门做，见[自动整理合同](./curation-contracts.md)第 16 节；缓存命中不验证密钥。

## 8b. 艺人资料的来源

`ArtistLinkFinder.ArtistLinks(ctx, links)` 补全 `ArtistLinks` 中的 MBID、WikidataID、CommonsFile、WikipediaZH 和 WikipediaEN。两个 Wikipedia 字段存条目标题。程序层调用 `ArtistImageFinder.ArtistImage(ctx, links)` 取得图片字节、条目 ID 与署名，调用 `ArtistBioFinder.ArtistBio(ctx, links, lang)` 取得纯文本、条目 ID、语言与署名。署名含来源、原页面链接，以及来源给出的作者、许可、许可链接。

| 来源 | GET 请求 | 能力 | 最小间隔 |
| --- | --- | --- | --- |
| Wikidata | `/w/api.php`，按 P434 搜索或按 Q 号取 P18、中文和英文 sitelinks | `artist_lookup` | 1 秒 |
| Wikimedia Commons | `/w/api.php`，`prop=imageinfo&iiurlwidth=960`，再下载返回的缩略图 | `artist_image` | 1 秒 |
| Wikipedia | `https://{zh,en}.wikipedia.org/api/rest_v1/page/summary/<标题>` | `artist_bio` | 1 秒 |
| Fanart.tv | `/v3/music/<MBID>`，再下载头像 | `artist_image` | 1 秒 |
| TheAudioDB | `/api/v1/json/<密钥>/artist-mb.php?i=<MBID>`，取图时再下载头像 | `artist_image`、`artist_bio` | 2 秒 |

### Wikidata

无 Q 号时用 `list=search` 和 `srsearch=haswbstatement:P434=<MBID>`，唯一匹配才继续。按 Q 号用 `action=wbgetentities&props=claims|sitelinks&sitefilter=zhwiki|enwiki` 查询。P18 跳过 deprecated，优先 preferred，再取第一张普通照片。使用与维基百科、Commons 相同的维基媒体策略，每个来源同时最多一个请求，带项目 User-Agent 和 `Accept-Encoding: gzip`。429 和 503 的后台重试遵循 Retry-After，缺失时至少等待 5 秒。

### Wikimedia Commons

`titles=File:<文件名>`、`iiprop=url|size|mime|extmetadata`、`formatversion=2`。许可取 LicenseShortName，为空时跳过下载。作者从 Artist、Attribution、Credit 依次选取并清理 HTML，页面链接取文件描述页，许可链接取 LicenseUrl。只下载查询返回的 960 宽度档。下载地址与各次跳转在本源主机、upload.wikimedia.org、thumb.wikimedia.org 和配置的 mock 主机范围内检查，最大 16 MiB。

### Wikipedia

中文请求和它的跳转都带 `Accept-Language: zh-cn`。两种语言各自只可跳到同语言的官方主机或配置的 mock 主机。导语取 extract，消歧义页跳过。记录页面链接、语言和 CC BY-SA 4.0 许可链接。简介清理 HTML、数字脚注与多余空白，取首个非空导语段，最多 4000 个 Unicode 码点。

### Fanart.tv

选择 artistthumb 中 likes 最大的一张，同票取第一张。保留图片 ID 与艺人原页面链接。API 请求头 api-key 是管理员填写的 project，缺省时读取发布时链接参数 `-X …/sources/fanarttv.bundledProjectKey=` 注入的项目密钥。client-key 来自可选的 personal。仓库 secret `fanarttv_project` 可选，发布缺少它会打印警告。管理员与构建均无项目密钥时为 `key_missing`。图片下载与重定向限于 webservice.fanart.tv、assets.fanart.tv 和配置的 mock 主机。图片请求与跳转不带密钥头。

内置项目密钥被拒绝时，进程记录 `bundled_key_rejected`。管理员填写项目密钥后，闸门改用所填密钥的状态。

### TheAudioDB

管理员填写 api_key 后可用。出站前读取密钥并放入 URL 路径，缓存键使用未填密钥的地址。图片取 strArtistThumb，用地址的 SHA-256 标识图片，简介取 strBiographyCN 或 strBiography，使用艺人条目 ID 标识同一来源条目。两种资料的来源链接均使用艺人原页面。下载与重定向限于 theaudiodb.com、www.theaudiodb.com、r2.theaudiodb.com 和配置的 mock 主机。

## 9. 发给外部的数据

| 源 | 发出的内容 |
| --- | --- |
| MusicBrainz | 外部 ID、条码、厂牌与编号、ISRC、标题与艺人的查询词、曲目数、由各轨时长推出的光盘目录 |
| Cover Art Archive | 外部 ID |
| Wikidata | MusicBrainz 艺人 MBID 或 Wikidata 条目 ID |
| Wikimedia Commons | Commons 文件名、960 宽度档及文件缩略图地址 |
| Wikipedia | 语言与条目标题 |
| Fanart.tv | MusicBrainz 艺人 MBID、项目密钥及已填写的个人密钥、所选头像地址 |
| TheAudioDB | MusicBrainz 艺人 MBID、管理员填写的 API 密钥及所选头像地址 |
| LRCLIB | 曲目标题、艺人、专辑名、时长 |
| AcoustID | 由音频本地算出的声学指纹、整首时长，以及应用密钥（发布镜像里是 ROOMusic 内置的，标识这个应用；没有内置的构建是管理员填入的）与管理员填入的用户密钥（标识管理员在 AcoustID 的账号） |

不发送音频、文件路径与实例标识。外部返回的内容一律是不可信文本，进入模型之前按管家的规则加来源标记。

## 10. 必需测试与断言点

| 断言点 | 位置 |
| --- | --- |
| 限速：间隔、对话优先、对话排队超时、后台等待 | `backend/internal/sources` 的限速器用例 |
| 缓存：三种有效期、不落盘的失败、目录为空、目录不可写、结构不对的 200 | `backend/internal/sources/musicbrainz/request_policy_test.go` |
| 重试与错误：`Retry-After`、退避、上限、后台的断网是“稍后再试”、对话不重试 | 同上 |
| 重定向：放行；主机、协议降级、端口、带凭据、次数各自被拒且不重试；每跳的请求头；一条链一个许可；两跳之间放行回调拒绝 | `backend/internal/sources` 与 `sources/coverartarchive` 的用例 |
| `GetBytes`：404、超限、重试、不写响应缓存、失败记忆 | 同上 |
| Cover Art Archive：清单解析、两种目标、选图、四档回退、PDF、两个限速器各自计数、默认装配与 MusicBrainz 共用同一个限速器 | `backend/internal/sources/coverartarchive` |
| LRCLIB：两种请求的参数、字段映射、纯音乐、404、429 的重试 | `backend/internal/sources/lrclib` |
| AcoustID：六项表单与 gzip、间隔、各错误码的分类、两把密钥各自无效、不缓存密钥错误、缓存键不含密钥、映射与去重 | `backend/internal/sources/acoustid`（见自动整理合同 16.7） |
| `PostForm`：经 AcoustID 的用例覆盖（重试、非 200 的分类、缓存）；GET 缓存键不变由现有 MusicBrainz 缓存用例覆盖 | `backend/internal/sources/acoustid`、`sources/musicbrainz` |
| 十个能力方法的请求形式与解析；disc ID 的基准值 | `backend/internal/sources/musicbrainz` |
| 管家工具的输出与错误码不变 | `backend/internal/steward/web` 现有用例 |
| 闸门、源开关接口、外部对应的存储、外部封面与歌词的写入与撤销 | 见[自动整理合同](./curation-contracts.md)第 13、14 节 |
| 包依赖方向 | `TestPackageDependencyDirections` |

自动化测试一律用本地的受控服务，不请求真实的外部源；限速、缓存与重试的等待用注入的时钟。

## 外部接口 mock

`ROOMUSIC_UPSTREAM_MOCK_URL` 由组合根读取。设置后，MusicBrainz、CAA、LRCLIB、AcoustID 的基础地址分别追加
`/musicbrainz/ws/2`、`/coverartarchive`、`/lrclib`、`/acoustid`，Tavily 使用 `/tavily/search`。
艺人资料另加 `/wikidata`、`/commons`、`/wikipedia`、`/fanarttv`、`/theaudiodb` 五个前缀，Wikipedia 前缀后接语言段。
这些客户端使用 `PublicOrAllowlist`，额外放行项只有配置地址的主机名（忽略大小写）和有效端口（HTTP 默认 80，HTTPS 默认 443）。
CAA 的清单和图片重定向也接受此主机与端口，清单仍须跳到以 `/index.json` 结尾的地址。其他地址按原来的公网与重定向规则检查。
未配置时沿用各服务的官方地址和 `PublicOnly`。管家的模型端点、SearXNG 和网页读取各自沿用原策略。

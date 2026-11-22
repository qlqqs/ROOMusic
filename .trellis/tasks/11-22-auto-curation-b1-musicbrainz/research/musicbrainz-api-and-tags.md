# MusicBrainz 接口用法与标签里的外部标识（调研）

调研日期：2026-11-22。依据：MusicBrainz 官方文档、musicbrainz-server / mb-solr / Picard 的公开源码、Picard 文档，以及对真实服务的 5 次验证请求（见 A4 末尾的请求记录）。
标注约定：**[文档]** 官方文档原文；**[源码]** 官方仓库 master 分支当日内容；**[实测]** 本次验证请求看到的结果；**[未确认]** 没查到或没验证。

## 结论

1. 一次读取就能拿全发行详情：`/ws/2/release/<MBID>?inc=recordings+isrcs+artist-credits+labels+release-groups+media+discids&fmt=json`，返回曲目表（时长、曲目 MBID、录音 MBID、ISRC）、厂牌与编号、条码、release group、署名与艺人 MBID [实测]。要艺人别名再加 `aliases`，但响应会明显变大。
2. `isrcs` 在 release 上必须和 `recordings` 一起带，否则服务端校验不通过 [源码]。
3. 搜索接口（`?query=`）返回的发行没有曲目表，只有每张碟的格式、曲目数、disc ID 数；有条码、厂牌编号、release group、署名 [实测]。命中后要再按 MBID 读取。
4. 搜索的 `score` 是“相对本次结果第一名的百分比”（第一名恒为 100），不是绝对把握程度 [源码]。实测按条码搜出 26 条全是 100 分，其中混有标题完全不同的发行。
5. 条码在 MusicBrainz 里不唯一，也存在 12 位与前补 0 的 13 位两种写法 [实测]；条码命中后仍要核对标题、曲目数。
6. `/ws/2/isrc/<ISRC>` 要求 12 位大写无连字符；返回录音列表，可带 `inc=releases`，但每条录音的 releases 最多 25 条 [源码]。本次该请求遇到 503，未拿到实测返回。
7. TOC 字符串：`1+曲目数+lead-out 偏移+各轨偏移`，单位 CD 帧（1/75 秒），偏移含 150 帧 lead-in（第一轨通常是 150）[文档]。
8. 模糊 TOC 只比较“曲目数完全相同 + 各轨时长在容差内”（每轨 ±10 秒，6 轨以上按 6 段累加），不看绝对偏移；最多返回 25 个发行，不返回距离，实测按 MBID 排序 [源码][实测]。它只能缩小到“这张专辑的若干 CD 版本”，定不到具体版本。
9. 由曲目数和各轨时长构造 TOC 可行：实测用时长重建的 TOC 与库里的 disc ID 逐位一致。建议先本地算 disc ID 走精确查询，同一请求带 `toc=` 和 `cdstubs=no`，查不到时服务端自动转模糊。
10. 限速：每 IP 平均 1 次/秒，超了全部 503；必须带可联系的 User-Agent [文档]。实测 503 带 `Retry-After: 1`；即使按 2 秒间隔请求也会遇到全局繁忙的 503，必须重试。
11. 浏览接口 `limit` 最大 100、默认 25；带 `inc=recordings` 浏览发行时整页最多约 500 首曲目。读取时内嵌的关联实体最多 25 条 [文档]。单次读取 release 的曲目表源码里没有截断 [源码]，超大发行未实测。
12. 标签键名的最大陷阱：Vorbis 的 `MUSICBRAINZ_TRACKID`、MP4 的 `MusicBrainz Track Id`、ID3 的 `UFID:http://musicbrainz.org` 都是**录音 ID**；真正的曲目 ID 是 `MUSICBRAINZ_RELEASETRACKID` / `MusicBrainz Release Track Id` [文档]。
13. 各工具键名大小写、空格、下划线不一致（Picard 在 ID3 写 `MusicBrainz Album Id`，foo_musicbrainz 写 `MUSICBRAINZ_ALBUMID`）。解析时把键名去掉非字母数字并转小写再比对；MBID 值用 UUID 正则逐个提取，可同时兼容 `/`、`; `、NUL 等分隔写法。
14. MBID 不要校验 UUID 版本位：真实数据里有版本位为 3 的 MBID [实测]。ISRC 规范形式是 12 位、大写、无连字符。
15. 用户给的 Picard 映射表地址 `…/en/appendices/tag_mapping.html` 现在是 404，有效地址是 `…/en/latest/appendices/tag_mapping.html`。

---

## A1. 按 MBID 读取与 `inc` 参数

接口根地址 `https://musicbrainz.org/ws/2/`；默认 XML，加 `fmt=json` 或 `Accept: application/json` 得到 JSON（两者都有时以 `fmt` 为准）[文档]。多个 `inc` 用 `+`（或空格）连接；写成多个 `inc=` 参数会报错 [源码]。

各实体读取时允许的 `inc`（[源码] 各 Controller 的 lookup 定义，比文档列得全）：

| 实体 | 允许的 `inc` |
|---|---|
| release | `artists` `labels` `recordings` `release-groups` `aliases` `artist-credits` `discids` `media` `tags` `genres` `ratings` `collections` `annotation` 各类 `*-rels` `recording-level-rels` `release-group-level-rels` `work-level-rels`；带 `recordings` 后还可加 `isrcs` |
| release-group | `artists` `releases` `artist-credits` `aliases` `annotation` `tags` `genres` `ratings` `*-rels`；带 `releases` 后还可加 `media` `discids`，并可用 `status=`、`type=` 过滤 |
| recording | `artists` `releases` `release-groups` `artist-credits` `isrcs` `aliases` `annotation` `tags` `genres` `ratings` `*-rels` `work-level-rels`；带 `releases` 后还可加 `media` `discids` |
| artist | `recordings` `releases` `release-groups` `works` `aliases` `annotation` `tags` `genres` `ratings` `*-rels`；带 `releases` 后可加 `media` `discids` `various-artists` |

要点：

- **依赖关系** [源码 Validator.pm 的 `%extra_inc`]：`isrcs`、`artist-credits` 可随 `recordings` 使用；`artist-credits`、`discids`、`media` 可随 `releases` 使用。release 的读取定义里没有单独的 `isrcs`，所以 **release 级 `inc=isrcs` 必须同时带 `recordings`**，否则返回错误“isrcs is not a valid option for the inc parameter for the release resource unless you specify one of the following other inc parameters: recordings”。文档的浏览一节也写作 `isrcs (with recordings)`。
- **内嵌关联实体最多 25 条** [文档]：“the number of linked entities returned is always limited to 25”，要全量得用浏览接口；并且“always ordered alphabetically by gid”。这条影响 `recording?inc=releases`、`release-group?inc=releases`、`artist?inc=releases` 等，不影响 release 自己的曲目表。
- **别名**：`aliases` 给出 artist、label、area、work 的别名，文档提示“treat these as a set, as they are not deliberately ordered”。

一次拿全发行的组合 [实测，请求 2]：

```
GET /ws/2/release/<MBID>?inc=recordings+isrcs+artist-credits+labels+release-groups+media+discids+aliases&fmt=json
```

返回的顶层键：`id` `title` `status` `status-id` `date` `country` `release-events` `barcode` `asin` `packaging` `quality` `disambiguation` `text-representation{language,script}` `cover-art-archive` `artist-credit` `label-info` `release-group` `media` `aliases`。

- `label-info[]`：`catalog-number` + `label{id,name,type,aliases…}`（厂牌可为 null、编号可为 null，搜索结果里见过两种情况）。
- `release-group`：`id` `title` `primary-type` `secondary-types` `first-release-date` `artist-credit` `aliases`。
- `artist-credit[]`：`name`（署名写法）`joinphrase` `artist{id,name,sort-name,type,country,disambiguation,aliases[]}`。
- `media[]`：`id` `position` `format` `title` `track-count` `track-offset` `discs[]` `tracks[]`。
  - `discs[]`：`id`（disc ID）`sectors`（lead-out 偏移）`offsets[]`（各轨偏移）`offset-count`。
  - `tracks[]`：`id`（曲目 MBID）`position` `number`（字符串）`title` `length`（毫秒）`artist-credit` `recording{id,title,length,isrcs[],artist-credit,first-release-date,video,disambiguation,aliases}`。
- 曲目的 `length` 与录音的 `length` 是两个值，可以不同（实测第 1 轨 300893 与 301133）。有 disc ID 的 CD，曲目时长等于 TOC 偏移差换算的毫秒数（实测逐轨吻合）。
- 12 首曲目带 `aliases` 的响应约 27 KB；别名挂在每个艺人、厂牌、release group 上，重复很多。只为匹配时可以不带 `aliases`，艺人别名另用 `/ws/2/artist/<MBID>?inc=aliases` 读取。

MBID 合并后的行为 [文档 MusicBrainz_Identifier]：“An entity can have more than one MBID. When an entity is merged into another, its MBIDs redirect to the other entity.” 用旧 MBID 读取时返回体里的 `id` 可能与请求的不同。[未确认] 本次没有对合并过的 MBID 做实测。

来源：
- https://musicbrainz.org/doc/MusicBrainz_API
- https://musicbrainz.org/doc/MusicBrainz_API/Examples
- https://musicbrainz.org/doc/MusicBrainz_Identifier
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/WebService/Validator.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/Release.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/ReleaseGroup.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/Recording.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/Artist.pm

## A2. 搜索接口

形式：`/ws/2/<实体>?query=<Lucene 查询>&limit=<1..100>&offset=<n>&fmt=json` [文档]。

- `limit` 只允许 1 到 100，默认 25；`offset` 翻页。
- `query` 支持完整 Lucene 语法；字段写法 `字段:值`，短语用引号，可用 `AND` / `OR`。不写字段时在 release 索引里默认搜 `release`（标题）。
- 字面量里的 Lucene 特殊字符要转义，再做 URL 编码（文档例子 `ac%5C%2Fdc`）。
- `dismax=true` 改用普通解析器，自动转义特殊语法字符，适合纯文本查询。
- 查“字段为空”：`-字段:*`；数量类字段可查 `0`。

release 索引的字段 [文档原文含义]：

| 字段 | 含义 |
|---|---|
| `barcode` | 发行的条码 |
| `catno` | 发行的任一厂牌编号（不区分大小写，忽略空格和分隔符） |
| `label` | 任一厂牌名称（的一部分） |
| `laid` | 任一厂牌的 MBID |
| `tracks` | 整个发行的曲目总数 |
| `tracksmedium` | 发行里任意一张碟的曲目数 |
| `mediums` | 发行的碟数 |
| `discids` / `discidsmedium` | 整个发行 / 任意一张碟挂的 disc ID 数量 |
| `date` | 发行日期（例 `1980-01-22`） |
| `country` | 任一发行国家的两字母代码（ISO 3166-1 alpha-2） |
| `format` | 任一碟的格式（不区分大小写，忽略空格和分隔符） |
| `release` / `releaseaccent` | 标题（忽略 / 保留变音符号） |
| `artist` | 整条署名文字（含连接词） |
| `artistname` / `creditname` | 任一署名艺人的名字 / 在本发行上的署名写法 |
| `arid` / `reid` / `rgid` / `mediumid` | 艺人 / 发行 / release group / 碟的 MBID |
| `status` `primarytype` `secondarytype` `type` | 发行状态、release group 的主类型、次类型、旧式合并类型 |
| `lang` / `script` | 语言（ISO 639-3）/ 文字（ISO 15924） |
| `asin` `packaging` `quality` `comment` `alias` `tag` | 见文档 |

对应到本任务的依据：

- ② 条码：`/ws/2/release?query=barcode:0720642442524&fmt=json`。
- ② 厂牌加编号：`query=catno:DGCD24425 AND label:"DGC"`（或用 `laid:<厂牌 MBID>`）。`catno` 忽略大小写、空格、分隔符，所以 `DGCD-24425`、`DGCD 24425` 等价。
- ⑥ 标题、艺人、曲目数：`query=release:"Nevermind" AND artist:"Nirvana" AND tracks:12`；多碟时 `tracks` 是总数，`tracksmedium` 是任意一碟的数，`mediums` 是碟数。

`score` 的含义：

- 文档没有定义。[源码 mb-solr 的 MBXMLWriter.adjustScore] 计算式是 `(int)(本条得分 / 本次结果最高分 * 100)`，即相对第一名的百分比，第一名恒为 100。
- 所以它**不能当作把握程度**：换一个查询，同样的 80 分含义不同；结果只有一条时必为 100。
- [实测，请求 1] `barcode:720642442524` 返回 `count: 26`，前 5 条全部 `score: 100`，其中有 *Grunge Is Dead*、*Wired*、*1999‐07‐25: Woodstock '99* 这类标题不同、但库里登记了同一条码的发行。
- JSON 里 `score` 的类型：实测是数字 `100`；文档 2017 年的样例是字符串 `"100"`。解码时两种都接受。

搜索结果与按 MBID 读取相比 [实测，请求 1]：

- 有：`id` `score` `title` `status` `date` `country` `release-events` `barcode` `label-info[{catalog-number,label{id,name}}]` `artist-credit[{name,artist{id,name,sort-name}}]` `release-group{id,title,primary-type}` `text-representation` `track-count`（总曲目数）`media[{id,format,disc-count,track-count}]`。
- 没有：曲目表、各曲时长、录音 MBID、ISRC、disc ID 的值（只有数量）、艺人别名的完整列表。
- 搜索索引相对数据库有延迟（独立的搜索服务器）；[未确认] 延迟长度官方未写明。

另外：recording 索引有 `isrc` 字段（“any ISRC linked to the recording”），结果里带录音所在的 releases 及对应曲目位置（文档样例）。用 `query=isrc:(A OR B OR C)` 可以一次查多个 ISRC，减少请求数。[未确认] 这个用法本次没有实测。

来源：
- https://musicbrainz.org/doc/MusicBrainz_API/Search
- https://github.com/metabrainz/mb-solr/blob/master/mb-solr/src/main/java/org/musicbrainz/search/solrwriter/MBXMLWriter.java

## A3. 按 ISRC 查录音

- 形式：`/ws/2/isrc/<ISRC>?inc=<INC>&fmt=json` [文档]：“An isrc lookup returns a list of recordings, the 'inc=' arguments supported are identical to a lookup request for a recording.”
- 路径里的 ISRC 必须符合 `^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$` [源码 Validation.pm `is_valid_isrc`]：12 位、大写、无连字符。不符合返回“Invalid isrc.”，库里没有则 404。
- 返回体 [源码 Serializer/JSON/2/ISRC.pm]：`{ "isrc": "<ISRC>", "recordings": [ … ] }`。同一个 ISRC 可以对应多条录音（文档：“because clashes sometimes occur, each of these lookups return a list of entities”）。
- 实际允许的 `inc` [源码 ISRC.pm]：`artists` `releases` `isrcs` `artist-credits` `aliases` `tags` `ratings` `*-rels`；带 `releases` 后可加 `media` `discids`。**`release-groups` 不在允许列表里**，和文档说的“与 recording 读取相同”不一致，以源码为准。
- 能带出录音所在的 releases：`inc=releases`。但每条录音的 releases 最多 25 条 [源码 Recording.pm `$MAX_ITEMS = 25`]。文档里“there is no limit, all linked entities will be returned”说的是录音列表本身。热门录音要全量时用浏览：`/ws/2/release?recording=<录音 MBID>&limit=100`。
- 带 `media` 时 releases 里只给出包含该录音的那张碟和曲目位置 [源码 `load_with_medium_for_recording`]。

[未确认] 本次的 ISRC 验证请求（请求 5）返回 503，没有拿到实际返回体；以上返回结构来自源码，没有实测。

来源：
- https://musicbrainz.org/doc/MusicBrainz_API （Non-MBID Lookups）
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/ISRC.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/WebService/Serializer/JSON/2/ISRC.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Validation.pm

## A4. 光盘目录（TOC）与 disc ID

### TOC 字符串格式

[文档] `toc` 由四部分组成，空格分隔（URL 里写成 `+`）：

1. 首轨号（恒为 1）
2. 曲目总数
3. lead-out 的扇区偏移（碟的结尾）
4. 各轨的扇区偏移，从第 1 轨开始（第 1 轨一般是 150）

例：`toc=1+12+267257+150+22767+41887+58317+72102+91375+104652+115380+132165+143932+159870+174597`

- 单位是 CD 逻辑块，1 块 = 1/75 秒 [文档 Disc_ID_Calculation]。
- 偏移**含 150 帧 lead-in**：文档说 LBA 从 0 起算，但第一轨实际在 00:02:00，“we need to add 150 logical blocks to each LBA offset”。
- 服务端校验 [源码 DurationLookup.pm `_parse_toc`]：全是数字；首轨必须是 1；曲目数 1 到 99；偏移个数等于曲目数；偏移严格递增；lead-out 大于最后一轨偏移。不满足返回“Invalid TOC”。

### disc ID 的算法

[文档] 把“首轨号（1 字节）、末轨号（1 字节）、lead-out 偏移（4 字节）、99 个轨偏移（各 4 字节，不足补 0）”按大写十六进制 ASCII（`%02X`、`%08X`）拼接后做 SHA-1，再做 Base64，并把 `+ / =` 换成 `. _ -`。结果固定 28 个字符。

- 带数据轨的多段碟（CD-Extra）：数据轨不参与计算，lead-out 取“数据轨偏移 − 11400”，也就是最后一条音频轨的结尾 [文档]。
- 文档提示：同一张专辑的不同压片可能有不同的 disc ID；极少数不同的 CD 会有相同的 disc ID。

### 查询行为

形式：`/ws/2/discid/<discid>?toc=<TOC>&inc=<INC>&cdstubs=no&media-format=all&fmt=json`

[文档 + 源码 DiscID.pm] 处理顺序：

1. `<discid>` 是合法格式且库里有：返回 disc 对象 `{id, sectors, offsets[], offset-count, releases[]}`，releases 是挂了这个 disc ID 的发行。此时不做模糊查询。
2. 否则，若没传 `cdstubs=no` 且有同 ID 的 CD stub：返回 CD stub（结构不同，没有署名信息），**不再做模糊查询**。
3. 否则，若带了 `toc`：做模糊 TOC 查询，返回 `{release-count, release-offset, releases[]}`。
4. 都没有：404。

- `/ws/2/discid/-?toc=…`：用 `-`（或任何非法占位）当 disc ID，直接走模糊查询 [文档]。
- `cdstubs=no`：不查 CD stub。要模糊查询又不要 stub 时必须带 [文档]。取值只能是 `yes` 或 `no`。
- `media-format=all`：模糊查询默认只查“格式可以有 disc ID 的碟，或格式为空的碟”（文档表述为格式是 CD 的碟；源码条件是 `m.format IS NULL OR mf.has_discids`），加这个参数查所有格式。
- `inc` 与 release 读取相同；服务端强制带上 `media` 和 `discids`。不带 `recordings` 时结果里没有曲目表 [实测]。
- 单次最多 25 个发行（`limit` 上限被压到 25）[源码]。

### 模糊匹配的规则

[源码 DiscID.pm、DurationLookup.pm、CreateFunctions.sql]

- 服务端先把 TOC 换成各轨时长：`int((下一偏移 − 本轨偏移) × 1000 / 75)` 毫秒。**只用偏移差，不用绝对偏移**，所以第一轨是不是 150 对模糊查询没有影响。
- 曲目数必须完全相等（`track_count_matches_cdtoc`）：库里那张碟的曲目数要等于 TOC 曲目数加上“预间隙曲目（position = 0）和数据轨”的数量。
- 时长比较用 6 维向量：曲目数小于 6 时每轨一维；大于等于 6 时第 i 轨归入第 `floor(6 × (i−1) / N) + 1` 段，段内时长相加。
- 容差 `DURATION_LOOKUP_RANGE = 10000` 毫秒：每一段允许的偏差是 ±10 秒 × 该段的曲目数。
- 按 6 维欧氏距离从近到远取前 25 个发行；距离不出现在返回里。
- 只有满足这些条件的碟才进索引：总时长小于 4 800 000 毫秒（80 分钟）、每首曲目都有时长、不超过 99 首。
- 匹配单位是“碟”，返回的是包含这张碟的发行；多碟发行要每张碟各查一次。

### 验证请求记录

User-Agent 均为 `metadata-research/0.1 ( research )`，间隔 ≥ 2 秒，共 5 次，之后没有再请求。

| # | 请求 | 结果要点 |
|---|---|---|
| 1 | `GET /ws/2/release?query=barcode:720642442524&fmt=json&limit=5` | 200；`count: 26`；前 5 条 `score` 全是 100，含多个标题不同的发行；结果无曲目表；响应头 `x-ratelimit-limit: 400`、`x-ratelimit-remaining: 391` |
| 2 | `GET /ws/2/release/43d27d70-3e4f-46f0-b40e-ef4467145b0c?inc=recordings+isrcs+artist-credits+labels+release-groups+media+discids+aliases&fmt=json` | 200，约 27 KB；*Nevermind*（阿根廷版，12 轨，1 个 disc ID `bDCCdX8vZbnGiGD4tcN8aGNdkZc-`，`sectors: 267125`，`offsets: [150, 22717, …, 174551]`）；每轨带曲目 MBID、录音 MBID、时长、ISRC；`x-ratelimit-limit: 1900` |
| 3 | `GET /ws/2/discid/-?toc=1+12+267125+150+22717+41837+58273+72053+91327+104601+115341+132131+143885+159823+174551&fmt=json` | 200；`release-count: 25`；25 个全是 *Nevermind* 的 12 轨 CD（不同国家、年份、条码），按 MBID 字典序排列；目标发行在其中；每个发行带 `media[].discs`，无曲目表、无署名、无厂牌 |
| 4 | 同 3，但 TOC 由“各轨时长四舍五入到整秒”重建：`toc=1+12+267150+150+22725+41850+58275+72075+91350+104625+115350+132150+143925+159900+174600` | 200；返回与请求 3 完全相同的 25 个发行 |
| 5 | `GET /ws/2/isrc/USGF19942501?inc=releases+isrcs&fmt=json` | **503**；响应头 `retry-after: 1`、`x-ratelimit-zone: global`、`x-ratelimit-who: global`；响应体 `{"error": "The MusicBrainz web server is currently busy. Please try again later."}` |

本地核对（不产生请求）：用请求 2 返回的 12 个曲目时长（毫秒），按“第一轨 150，逐轨加 `round(时长秒 × 75)`”重建 TOC，得到的偏移、lead-out 与库里那个 disc 完全相同，按文档算法算出的 disc ID 也是 `bDCCdX8vZbnGiGD4tcN8aGNdkZc-`。

观察：

- 模糊结果在热门专辑上会顶到 25 条上限，`release-count` 也只报 25，无法知道被截掉多少，也翻不了页（SQL 内部就是 `LIMIT 25`）。目标版本可能不在返回里。
- 请求 3 传的是库里已有 disc ID 对应的精确 TOC，但因为路径用了 `-`，服务端不会去做精确匹配。要利用精确匹配，必须自己算出 disc ID 放进路径。

### 判断：只有曲目数和各曲时长时能否构造 TOC

前提（整轨抓的 CD、无隐藏轨、无数据轨、第一轨从 150 帧开始）成立时**可以可靠构造**：

- 44.1 kHz 的无损整轨抓取，每轨样本数是 588 的整数倍（588 样本 = 1 帧）。帧数 = 样本数 / 588；用秒算就是 `round(秒 × 75)`。
- 偏移：第 1 轨 150，之后逐轨累加帧数；lead-out = 最后一轨偏移 + 最后一轨帧数。
- 实测重建结果与库里 disc ID 逐位一致（见上）。
- 可操作的判别：一张碟的所有文件都是 44.1 kHz 且样本数都能被 588 整除时，才当作“CD 整轨抓取”去算精确 disc ID；否则只当作模糊查询的输入。如果扫描器只保存了秒级或毫秒级时长，毫秒精度足够还原帧数（1 帧约 13.3 毫秒），整秒精度只能用于模糊查询。

建议的请求形式：`/ws/2/discid/<本地算出的 disc ID>?toc=<TOC>&cdstubs=no&fmt=json`。disc ID 命中时得到精确结果，未命中时同一个请求自动转模糊。两种返回的顶层结构不同（`{id, sectors, offsets, releases}` 与 `{release-count, release-offset, releases}`），要按结构区分“精确”和“模糊”，两者的把握程度差别很大。

会失败或降级的情况：

| 情况 | 对精确 disc ID | 对模糊 TOC |
|---|---|---|
| 预间隙并入上一轨（EAC、XLD 的默认做法） | 不受影响：轨边界仍在 INDEX 01，就是 TOC 偏移 | 不受影响 |
| 预间隙并入下一轨或被丢弃 | 失败：边界或总长变了 | 一般仍在容差内（间隙通常只有几秒） |
| HTOA（第一轨前的隐藏音频） | 失败：真实第一轨偏移大于 150，所有偏移整体平移 | 不受影响（只看时长）；若隐藏轨被抓成第 0 轨文件，要先去掉它，否则曲目数对不上 |
| 增强 CD，数据轨在末尾（CD-Extra） | 理论上不受影响：算法用的 lead-out 就是最后一条音频轨的结尾。[未确认] 未实测 | 不受影响；库里的数据轨不计入曲目数 |
| 混合模式 CD，数据轨在第一轨 | [未确认] disc ID 是否包含这条数据轨，文档没写 | 音频轨数相同即可 |
| 多碟 | 必须先正确分碟，每碟各算一次；分碟错了曲目数就错 | 同左；曲目数不等则零结果 |
| 有损转码（MP3、AAC 等） | 基本必然失败：时长含编码延迟和填充，不是整帧，累加后偏移漂移 | 可用，但结论只是“时长像这张 CD” |
| 非 CD 来源（数字发行、高采样率下载） | 不适用 | 时长相近的 CD 版本会被返回，属于误配风险；默认还查不到 Digital Media 格式的碟，除非加 `media-format=all` |
| 末轨被刻录或抓取软件补了静音 | 失败 | 仍在容差内 |
| 单碟超过 80 分钟、超过 99 轨，或库里该碟有曲目缺时长 | 精确匹配仍可用 | 查不到（没有进索引） |
| 库里没人给这个版本挂过 disc ID | 未命中，自动转模糊 | 可用 |

把握程度的含义：

- 精确 disc ID 命中：强证据说明“曲目表与时长对应这张碟”；但同一个 disc ID 可以挂在多个发行上，同一个发行也可以挂几十个 disc ID（实测有发行挂了 31、32 个），所以对“具体是哪个版本”只是中等证据。
- 模糊 TOC 命中：只说明曲目数相同、时长相近，结果是同一专辑的一批版本，还要靠条码、编号、国家、年份等再区分。

来源：
- https://musicbrainz.org/doc/MusicBrainz_API （discid 一节）
- https://musicbrainz.org/doc/Disc_ID_Calculation
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/DiscID.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Data/DurationLookup.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/admin/sql/CreateFunctions.sql （`create_cube_from_durations`、`create_bounding_cube`、`track_count_matches_cdtoc`）
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/WebService/Serializer/JSON/2/CDTOC.pm

## A5. 使用要求：限速、User-Agent、503

[文档 Rate_Limiting，规则标注为“As of 2012-01-08”，并声明“We may change the blocking/throttling rules at any time”]

- **每 IP**：“Currently that rate is (on average) 1 request per second.” 超过后不是按比例放行：“we decline 100% of them, until the rate drops to 1 per second or lower”。
- **每 User-Agent**：只有特定的 UA 被限制（headphones、`python-musicbrainz/0.7.3`、匿名 UA，各约 50 次/秒，全体用户合计）；其他 UA 不限。匿名 UA 指：空、`Java`、`Python-urllib`、`Jakarta Commons-HttpClient`、旧版 `Apache-HttpClient`。
- **全局**：“We allow through 300 requests each second (on average), and decline (http 503) the rest.”
- 三项按 UA、IP、全局的顺序检查，任何一项超限都返回 **503 Service Unavailable**。
- API 主文档另写：“All users of the API must ensure that each of their client applications never make more than ONE call per second”，否则“your IP address may be blocked”。
- **User-Agent**：每个请求都要带，且要有能联系到维护者的信息。建议格式 `Application name/<version> ( contact-url )` 或 `Application name/<version> ( contact-email )`，强烈建议带版本号。
- **调度**：不要让程序在固定钟点（例如整点、凌晨 3 点）集中发请求；后台任务应当“make calls at random intervals throughout the day”。
- **不要轮询变更**：文档明确说不要为了发现元数据变化去轮询。
- 不需要 API key；非商业使用免费，商业使用要另谈。

退避方式与 `Retry-After`：

- [文档] 没有给出退避算法，也没有提到 `Retry-After`。
- [实测，请求 5] 503 响应带 `retry-after: 1`、`x-ratelimit-zone: global`、`x-ratelimit-who: global`，响应体是 JSON 的 `error` 字段。这次 503 发生在与上一次请求间隔超过 20 秒的情况下，属于全局繁忙，不是本机超速。
- [实测] 200 响应带 `x-ratelimit-limit`、`x-ratelimit-remaining`、`x-ratelimit-reset`（Unix 秒）。`remaining` 在 3 秒内从 1752 掉到 784，明显不是按本 IP 计数，不能拿来做本机节流。这些响应头官方文档没有说明。
- 据此的做法建议（不是官方规定）：全进程串行，间隔不低于 1 秒；遇 503 按 `Retry-After` 等待，没有该头时从 1 到 2 秒起指数退避并加随机抖动；重试次数设上限。

搜索与读取的限速是否相同：

- [文档] 没有区分，1 次/秒的规则针对整个 API。
- [实测] 搜索请求的 `x-ratelimit-limit` 是 400，读取和 discid 请求是 1900，说明服务端内部对搜索有单独的全局配额。[未确认] 官方没有文档说明这两个数字的含义。客户端仍应把搜索和读取合在一起按 1 次/秒计。

Cover Art Archive：

- [文档 Cover_Art_Archive/API]：“There are currently no rate limiting rules in place at http://coverartarchive.org.”
- 同一文档的各接口又列了“503 if the user has exceeded their rate limit”，两处说法不一致。
- 它是独立域名，图片请求 307 跳转到 archive.org。按官方说法不与 MusicBrainz 共用 1 次/秒的限速。[未确认] 实际是否有未公开的限制。

来源：
- https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting
- https://musicbrainz.org/doc/MusicBrainz_API （Application rate limiting and identification）
- https://musicbrainz.org/doc/Cover_Art_Archive/API

## A6. 浏览接口、分页上限、截断

- 浏览形式：`/ws/2/<结果实体>?<关联实体>=<MBID>&limit=&offset=&inc=` [文档]。只有浏览支持分页：“the default limit is 25, and you can increase that up to 100”。
- release 可按 `area` `artist` `collection` `label` `track` `track_artist` `recording` `release-group` 浏览；release-group 可按 `artist` `collection` `release`；recording 可按 `artist` `collection` `release` `work`。
- 浏览 release 时可用的 `inc`：`artist-credits` `labels` `recordings` `release-groups` `media` `discids` `isrcs`（需同时带 `recordings`），可用 `status=`、`type=` 过滤。
- **浏览 release 的曲目上限** [文档]：整页的发行合计不超过 500 首曲目，但至少返回一个完整发行，不返回“半个”发行。所以 `limit=100` 时实际返回可能少于 100 个，翻页要“increment offset by the number of releases you get from each response”。源码里这条只在带 `inc=recordings` 时生效（`limit_releases_by_tracks`）。
- 浏览结果的顺序固定但不保证有意义，要排序得自己取全后排。
- 搜索：`limit` 1 到 100，默认 25。
- 读取时内嵌的关联实体：最多 25 条，不能翻页（见 A1）。
- disc ID 查询：最多 25 个发行（见 A4）。

单次读取 release 时曲目很多的发行是否截断：

- [源码 Release.pm `release_toplevel`] 带 `recordings` 时加载所有碟的所有曲目，没有数量上限；500 首的限制只用于浏览和模糊 TOC 结果。唯一与数量有关的是：录音数超过 500 时不再加载 `recording-level-rels`（本任务不用）。
- [文档] 没有提到读取会截断。
- [未确认] 没有对上千首曲目的发行做实测；这类响应会很大，超时风险未知。

来源：
- https://musicbrainz.org/doc/MusicBrainz_API （Browse、Paging）
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Controller/WS/2/Release.pm
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/ControllerBase/WS/2.pm

---

## B. 标签里的外部标识：各格式的键名

以 Picard 的标签映射表为准。注意：任务里给的地址 `https://picard-docs.musicbrainz.org/en/appendices/tag_mapping.html` 当前返回 404，有效地址是 https://picard-docs.musicbrainz.org/en/latest/appendices/tag_mapping.html 。

容器与标签格式的对应：

- ID3v2：MP3、AIFF、DSF；WAV 里 Picard 也是写 ID3v2 块（`WAVFile` 继承 ID3 实现），RIFF INFO 只补少量字段 [源码 picard/formats/wav.py]。
- Vorbis comment：FLAC、Ogg Vorbis、Opus。
- APEv2：Monkey's Audio、WavPack。
- MP4：自由格式原子 `----:com.apple.iTunes:<name>`。
- ASF：WMA。

### MusicBrainz 各类 ID

| 标识（Picard 内部名） | ID3v2 | Vorbis | APEv2 | MP4（`----:com.apple.iTunes:` 之后） | ASF | RIFF INFO |
|---|---|---|---|---|---|---|
| Release ID（`musicbrainz_albumid`） | `TXXX:MusicBrainz Album Id` | `MUSICBRAINZ_ALBUMID` | `Musicbrainz_Albumid` | `MusicBrainz Album Id` | `MusicBrainz/Album Id` | 无 |
| Release Group ID（`musicbrainz_releasegroupid`） | `TXXX:MusicBrainz Release Group Id` | `MUSICBRAINZ_RELEASEGROUPID` | `Musicbrainz_Releasegroupid` | `MusicBrainz Release Group Id` | `MusicBrainz/Release Group Id` | 无 |
| **Recording ID**（`musicbrainz_recordingid`） | `UFID:http://musicbrainz.org` | `MUSICBRAINZ_TRACKID` | `musicbrainz_trackid` | `MusicBrainz Track Id` | `MusicBrainz/Track Id` | 无 |
| **Release Track ID**（`musicbrainz_trackid`） | `TXXX:MusicBrainz Release Track Id` | `MUSICBRAINZ_RELEASETRACKID` | `musicbrainz_releasetrackid` | `MusicBrainz Release Track Id` | `MusicBrainz/Release Track Id` | 无 |
| Artist ID（`musicbrainz_artistid`，多值） | `TXXX:MusicBrainz Artist Id` | `MUSICBRAINZ_ARTISTID` | `Musicbrainz_Artistid` | `MusicBrainz Artist Id` | `MusicBrainz/Artist Id` | 无 |
| Album Artist ID（`musicbrainz_albumartistid`，多值） | `TXXX:MusicBrainz Album Artist Id` | `MUSICBRAINZ_ALBUMARTISTID` | `Musicbrainz_Albumartistid` | `MusicBrainz Album Artist Id` | `MusicBrainz/Album Artist Id` | 无 |
| Work ID（`musicbrainz_workid`） | `TXXX:MusicBrainz Work Id` | `MUSICBRAINZ_WORKID` | `Musicbrainz_Workid` | `MusicBrainz Work Id` | `MusicBrainz/Work Id` | 无 |
| Disc ID（`musicbrainz_discid`） | `TXXX:MusicBrainz Disc Id` | `MUSICBRAINZ_DISCID` | `Musicbrainz_Discid` | `MusicBrainz Disc Id` | `MusicBrainz/Disc Id` | 无 |

说明：

- **历史命名陷阱**：文件里叫 “Track Id / TRACKID” 的键装的是**录音 ID**；曲目 ID 用带 “Release Track” 的键。Picard 内部名正好反过来（`musicbrainz_trackid` 是曲目 ID，`musicbrainz_recordingid` 是录音 ID），读它的源码或文档时要注意。
- ID3 的录音 ID 在 `UFID` 帧里：owner 是 `http://musicbrainz.org`（没有结尾斜杠），数据是 36 字节 ASCII 的 UUID。Picard 只写第一个值（`bytes(values[0], 'ascii')`），读取时按 ASCII 解码。一个文件可以有多个不同 owner 的 UFID 帧，要按 owner 筛。
- 值的格式都是 UUID（Disc ID 除外，它是 28 字符的 Base64 变体串）。
- 可能多值：Picard 文档明确标为多值的是 Artist ID、Album Artist ID（以及 Original Artist ID）。Work ID 实际也会多值（一条录音可关联多个作品），Picard 文档没有标注。Release ID、Release Group ID、Recording ID、Track ID、Disc ID 按单值处理。
- Disc ID 只有在 Picard 用 “Lookup CD” 取信息时才写入。

### ISRC

| | ID3v2 | Vorbis | APEv2 | MP4 | ASF | RIFF INFO |
|---|---|---|---|---|---|---|
| ISRC（`isrc`） | `TSRC` | `ISRC` | `ISRC` | `----:com.apple.iTunes:ISRC` | `WM/ISRC` | 无 |

- ID3v2.4 规范里 `TSRC` 的定义是“ISRC (12 characters)”。
- 一条录音可以有多个 ISRC（MusicBrainz 返回的 `isrcs` 是数组），所以可能多值。ID3 的多值写法见下一小节：v2.4 是一个 `TSRC` 帧里用 NUL 分隔多个字符串，v2.3 是用分隔符拼成一个字符串。
- 实际文件里的值可能带连字符、小写或前缀 `ISRC`，规范化见 C 节。

### AcoustID

| | ID3v2 | Vorbis | APEv2 | MP4 | ASF | RIFF INFO |
|---|---|---|---|---|---|---|
| AcoustID（`acoustid_id`） | `TXXX:Acoustid Id` | `ACOUSTID_ID` | `Acoustid_Id` | `----:com.apple.iTunes:Acoustid Id` | `Acoustid/Id` | 无 |
| AcoustID Fingerprint（`acoustid_fingerprint`） | `TXXX:Acoustid Fingerprint` | `ACOUSTID_FINGERPRINT` | `Acoustid_Fingerprint` | `----:com.apple.iTunes:Acoustid Fingerprint` | `Acoustid/Fingerprint` | 无 |

- AcoustID 是 UUID（AcoustID 官方接口样例：`9ff43b6a-4f16-427c-93c2-92307ca505e0`）。Picard 文档：多个相似的指纹可以对应同一个 AcoustID。
- 指纹是 Chromaprint 生成的 URL 安全 Base64 长串；AcoustID 官方样例里的一条指纹是 1631 个字符，实际常见上千到数千字符。
- Picard 默认**不写**指纹：“this option is disabled by default because the fingerprint can always be calculated again from the audio file, and it can add a rather long data tag to the file”，该选项自 Picard 2.7 才有（源码默认值 `save_acoustid_fingerprints = False`）。
- 是否值得保存：指纹可以随时从音频重算，文件里出现的概率低，用它查 AcoustID 还要配合时长。若本任务不接 AcoustID 查询，可以只保存 `ACOUSTID_ID`（36 字符），不保存指纹。这是建议，不是外部事实。

### 可作佐证的键（简表）

| 标识（内部名） | ID3v2 | Vorbis | APEv2 | MP4 | ASF | RIFF INFO | 值 |
|---|---|---|---|---|---|---|---|
| Barcode（`barcode`） | `TXXX:BARCODE` | `BARCODE` | `Barcode` | `----:…:BARCODE` | `WM/Barcode` | 无 | 数字串 |
| Catalog Number（`catalognumber`） | `TXXX:CATALOGNUMBER` | `CATALOGNUMBER` | `CatalogNumber` | `----:…:CATALOGNUMBER` | `WM/CatalogNo` | 无 | 多值 |
| Record Label（`label`） | `TPUB` | `LABEL` | `Label` | `----:…:LABEL` | `WM/Publisher` | 无 | 多值 |
| Release Country（`releasecountry`） | `TXXX:MusicBrainz Album Release Country` | `RELEASECOUNTRY` | `Releasecountry` | `----:…:MusicBrainz Album Release Country` | `MusicBrainz/Album Release Country` | `ICNT` | 两字母代码，只写第一个国家 |
| Release Status（`releasestatus`） | `TXXX:MusicBrainz Album Status` | `RELEASESTATUS` | `MUSICBRAINZ_ALBUMSTATUS` | `----:…:MusicBrainz Album Status` | `MusicBrainz/Album Status` | 无 | official、promotional、bootleg、pseudo-release |
| Release Type（`releasetype`） | `TXXX:MusicBrainz Album Type` | `RELEASETYPE` | `MUSICBRAINZ_ALBUMTYPE` | `----:…:MusicBrainz Album Type` | `MusicBrainz/Album Type` | 无 | 多值（主类型加次类型） |
| Media（`media`） | `TMED` | `MEDIA` | `Media` | `----:…:MEDIA` | `WM/Media` | `IMED` | 如 `CD` |
| Script（`script`） | `TXXX:SCRIPT` | `SCRIPT` | `Script` | `----:…:SCRIPT` | `WM/Script` | 无 | ISO 15924 |
| ASIN（`asin`） | `TXXX:ASIN` | `ASIN` | `Asin` | `----:…:ASIN` | `ASIN` | 无 | |
| Original Date（`originaldate`） | `TDOR`（v2.4）/ `TORY`（v2.3，只有年） | `ORIGINALDATE` | 无 | 无 | `WM/OriginalReleaseTime` | 无 | `YYYY-MM-DD`，取 release group 里最早的发行 |
| Original Year（`originalyear`） | 无 | `ORIGINALYEAR` | `Originalyear` | 无 | `WM/OriginalReleaseYear` | 无 | `YYYY` |
| Release Date（`date`） | `TDRC`（v2.4）/ `TYER`+`TDAT`（v2.3） | `DATE` | `Year` | `©day` | `WM/Year` | `ICRD` | |

### 各格式的大小写与多值规则

| 格式 | 键名大小写 | 多值的存法 |
|---|---|---|
| ID3v2.4 | `TXXX` 的 description 是普通字符串，规范不规定大小写；Picard 读取 MusicBrainz 相关的 description 时**按原样精确比较** | 一个文本帧里多个字符串，用该编码的结束符（NUL）分隔（规范：“All text information frames supports multiple strings, stored as a null separated list”） |
| ID3v2.3 | 同上 | 规范不支持多值。Picard 把多值用分隔符拼成一个字符串，分隔符可配置，源码默认 `/`，文档建议改成 `; `。有些工具在 v2.3 里也塞 NUL 分隔 |
| Vorbis comment | 规范规定字段名不区分大小写（A–Z 与 a–z 等价）；惯例写大写 | 同名字段重复出现 |
| APEv2 | 规范上区分大小写，但建议读取时不区分，并禁止只差大小写的重复键；Picard 读取时统一转小写 | 一个条目的值里用 NUL 分隔 |
| MP4 | `mean` 通常是 `com.apple.iTunes`；`name` 是普通字符串；Picard 对 MusicBrainz 相关的名字**按原样精确比较** | 一个 `----` 原子里放多个 `data` 子原子；值是 UTF-8 文本，读到后要去掉首尾的 NUL |
| ASF | 属性名是普通字符串；Picard 对这些键按原样精确比较 | 同名属性重复出现 |
| RIFF INFO | 四字符块名 | 不支持；没有 MusicBrainz ID、ISRC、AcoustID 的位置 |

### 其他打标工具的常见变体

| 工具 | 与 Picard 的差别 | 依据 |
|---|---|---|
| beets（mediafile） | ID3、MP4、ASF 的键名与 Picard 相同；录音 ID 同样写 `UFID:http://musicbrainz.org`、`MUSICBRAINZ_TRACKID`、`MusicBrainz Track Id`（字段名 `mb_trackid`，同样是录音 ID）。Vorbis 里发行状态、类型同时认 `RELEASESTATUS` / `MUSICBRAINZ_ALBUMSTATUS`、`RELEASETYPE` / `MUSICBRAINZ_ALBUMTYPE`。ASF 的 ASIN 写 `MusicBrainz/ASIN`。条码还读 `UPC`、`EAN`、`UPN`、`EAN/UPN`；编号还读 `CATALOGID`、`DISCOGS_CATALOG`。读 ID3v2.3 时对艺人 ID、专辑艺人 ID、类型、编号按 `/` 拆分（`split_v23=True`） | 源码 |
| Mp3tag | 字段名用大写下划线（`MUSICBRAINZ_ALBUMID` 等），写 ID3 和 MP4 时映射成 Picard 的写法（`TXXX:MusicBrainz Album Id`、`----:com.apple.iTunes:MusicBrainz Album Id`）；`MUSICBRAINZ_TRACKID` 映射到 `UFID:http://musicbrainz.org` 和 `MusicBrainz Track Id`（即录音 ID），`MUSICBRAINZ_RELEASETRACKID` 映射到 `Release Track Id`。Vorbis 与 APEv2 **不做映射，按字段原名写**。映射表之外的自定义字段在 ID3 里写成 `TXXX:<字段名>`，原样保留大小写，所以会出现 `TXXX:MUSICBRAINZ_xxx` 这类非 Picard 写法。映射表里没有 ASF 的 MusicBrainz 键 | 官方映射文档 |
| foobar2000 + foo_musicbrainz | 所有格式统一用大写下划线名，MP3 和 M4A 也是：`TXXX:MUSICBRAINZ_ARTISTID` 等。录音 ID 写 `TXXX:MUSICBRAINZ_TRACKID`，不写 UFID。`LABEL`、`MEDIA`、`RELEASECOUNTRY` 也写成同名 `TXXX`，不用 `TPUB`、`TMED`。原始发行日期在 Vorbis 里写 `ORIGINAL RELEASE DATE`（带空格） | 组件说明页 |
| foobar2000 本体 | [未确认] 官方的 ID3 映射页（Hydrogenaudio wiki）本次访问被拒（403）。社区帖子称 foobar2000 不读写 UFID 这类二进制帧 | 未核实 |
| Jaikoz / SongKong（jaudiotagger） | Vorbis 键名与 Picard 一致（`MUSICBRAINZ_ALBUMID`、`MUSICBRAINZ_TRACKID`、`MUSICBRAINZ_RELEASETRACKID`、`MUSICBRAINZ_RELEASEGROUPID`、`ACOUSTID_ID`、`BARCODE`、`CATALOGNUMBER`、`ISRC`），发行状态、类型用 `MUSICBRAINZ_ALBUMSTATUS`、`MUSICBRAINZ_ALBUMTYPE`，另有一批 `MUSICBRAINZ_WORK_*` 键。[未确认] ID3、MP4、ASF 的映射本次未查到源文件 | 源码镜像（仅 Vorbis 部分） |
| MusicBee | [未确认] 没有找到官方的键名对照。社区反映它写自定义标签时可能出现非标准的 description | 未核实 |

据此的解析建议（供定规则时参考）：

1. 键名比对前做归一化：去掉所有非字母数字字符并转小写。`MusicBrainz Album Id`、`MUSICBRAINZ_ALBUMID`、`Musicbrainz_Albumid`、`MusicBrainz/Album Id` 都归一成 `musicbrainzalbumid`。
2. 归一化后的含义表：`musicbrainztrackid` 是**录音 ID**；`musicbrainzreleasetrackid` 是曲目 ID；`musicbrainzrecordingid` 也当录音 ID（名字无歧义；[未确认] 没有查到哪个主流工具把它直接写进文件）。ID3 里优先取 `UFID:http://musicbrainz.org`，没有时再看 `TXXX`。
3. 发行状态、类型、国家要同时认两套名字：`releasestatus` / `musicbrainzalbumstatus`、`releasetype` / `musicbrainzalbumtype`、`releasecountry` / `musicbrainzalbumreleasecountry`。
4. MBID 类字段不要按某个分隔符切分，而是在值里用 UUID 正则提取全部匹配。这样 v2.4 的 NUL、v2.3 的 `/` 或 `; `、同名字段重复都能一并处理。ISRC 的切分见 C 节。

来源：
- https://picard-docs.musicbrainz.org/en/latest/appendices/tag_mapping.html
- https://picard-docs.musicbrainz.org/en/latest/variables/tags_basic.html
- https://picard-docs.musicbrainz.org/en/latest/config/options_tags_compatibility_id3.html
- https://picard-docs.musicbrainz.org/en/latest/config/options_fingerprinting.html
- https://github.com/metabrainz/picard/blob/master/picard/formats/id3.py
- https://github.com/metabrainz/picard/blob/master/picard/formats/vorbis.py
- https://github.com/metabrainz/picard/blob/master/picard/formats/apev2.py
- https://github.com/metabrainz/picard/blob/master/picard/formats/mp4.py
- https://github.com/metabrainz/picard/blob/master/picard/formats/asf.py
- https://github.com/metabrainz/picard/blob/master/picard/formats/wav.py
- https://github.com/metabrainz/picard/blob/master/picard/options.py
- https://github.com/beetbox/mediafile/blob/master/mediafile/__init__.py
- https://docs.mp3tag.de/mapping/
- https://kbuffington.github.io/foo_jscript_panel/foo_musicbrainz.html
- https://github.com/Adonai/jaudiotagger/blob/master/src/org/jaudiotagger/tag/vorbiscomment/VorbisCommentFieldKey.java （jaudiotagger 的第三方镜像）
- https://github.com/id3/ID3v2.4/blob/master/id3v2.4.0-frames.txt
- https://xiph.org/vorbis/doc/v-comment.html
- https://mutagen.readthedocs.io/en/latest/api/ape.html
- https://mutagen.readthedocs.io/en/latest/api/mp4.html
- https://acoustid.org/webservice

## C. 多值与格式校验

### MBID

- [文档] “an MBID is a 36 character Universally Unique Identifier”，分配给 artist、release group、release、recording、work、label、area、place、URL，以及 track。
- 服务端的校验 [源码 Validation.pm `is_guid`]：长度 36；转小写后匹配 `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`；全 0 的 UUID 判为无效。
- 应接受的格式：`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`，先去掉首尾空白和 NUL，保存时统一转小写，拒绝全 0。
- **不要校验版本位和变体位**：实测返回里有版本位是 3 的 MBID（release group `1b022e01-4da6-387b-8658-8678046e4cef`、medium `03419f0a-a6a2-3dca-9e21-7e498b1ab584`）。
- 多值：按 B 节的建议用正则在值里逐个提取。

### ISRC

- [文档 ISRC] “The ISRC is a 12-byte alphanumeric string (only uppercase Latin letters and Arabic numerals, [A-Z0-9]) of the form CCOOOYYSSSSS”：`CC` 两位字母的国家代码；`OOO` 三位字母或数字的登记者代码；`YY` 两位数字的年份；`SSSSS` 五位数字的序号。
- 连字符：“While hyphens are not part of ISRCs, they are sometimes included so that they can be read more clearly. An example would be CC-OOO-YY-SSSSS.”
- 服务端的规范化与校验 [源码]：`format_isrc` 去掉空白和连字符并转大写；`is_valid_isrc` 是 `^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$`。
- 应采用的规则：去掉空白和连字符，转大写，再按上面的正则校验；保存 12 位无连字符的大写形式。查询 `/ws/2/isrc/` 时必须用这个形式。
- 多值：先按 NUL、`/`、`;`、`,` 和空白切分，再逐个规范化。连字符不能当分隔符。
- [未确认] 有的文件值带 `ISRC` 前缀或冒号（例如 `ISRC:USGF19942501`）；官方文档没有提到，是否要剥掉前缀由实现决定。

### AcoustID

- UUID，格式规则与 MBID 相同（AcoustID 官方接口返回的 `id` 样例是标准 36 字符 UUID）。
- 指纹不是 UUID，是长的 Base64 变体串，不做格式校验。

### Disc ID

- [源码 `is_valid_discid`] `^[A-Za-z0-9._-]{27}-`：28 个字符，最后一个是 `-`。

### 条码

- [源码] `is_valid_barcode` 只要求全是数字；提交接口只接受 8、12、13、14 位且校验位正确的 GTIN。
- [实测] 库里同一条码有 `720642442524`（12 位）和 `0720642442524`（13 位，前补 0）两种写法，也有 14 位的。按条码查时两种写法都要试，例如 `barcode:(720642442524 OR 0720642442524)`。[未确认] 这个 OR 查询本次没有实测。

来源：
- https://musicbrainz.org/doc/MusicBrainz_Identifier
- https://musicbrainz.org/doc/ISRC
- https://github.com/metabrainz/musicbrainz-server/blob/master/lib/MusicBrainz/Server/Validation.pm
- https://acoustid.org/webservice

---

## 未确认与注意事项

未确认：

1. `/ws/2/isrc/<ISRC>` 的实际返回体没有实测（验证请求遇到 503，按约定没有再发）。返回结构、`inc` 列表、releases 最多 25 条都来自源码。
2. 文档说 ISRC 查询的 `inc` 与 recording 读取相同，源码里 ISRC 接口不接受 `release-groups`，两者不一致；以源码为准，但没有实测。
3. 用旧 MBID（已合并）读取时的具体返回没有实测。
4. 上千首曲目的发行在单次读取时是否会超时或被网关截断，没有实测；源码里没有截断逻辑。
5. 搜索与读取在服务端有不同的配额数字（400 与 1900），含义没有官方说明。
6. Cover Art Archive 的文档一处说没有限速，另一处列了“超过限速返回 503”，互相矛盾。
7. 搜索索引相对数据库的延迟没有查到官方数字。
8. 混合模式 CD（数据轨在第一轨）的 disc ID 是否包含数据轨，文档没有写。
9. CD-Extra 用文件时长重建 TOC 能否对上 disc ID，只有文档算法上的推断，没有实测。
10. foobar2000 本体、MusicBee，以及 Jaikoz 在 ID3、MP4、ASF 上的键名没有查到可核实的官方来源。
11. Mp3tag 多值的写法（界面里的 `\\` 分隔符如何落到各格式）在它的映射文档里没有说明。
12. `recording?query=isrc:(A OR B)` 批量查 ISRC、`barcode:(A OR B)` 两种写法，以及错误响应（400、404）的 JSON 结构，都没有实测。

注意事项：

1. 限速规则页写明是 2012 年的规则，并声明随时可改；实现上不要把 1 次/秒以外的数字写死成依赖。
2. 遵守 1 次/秒仍然会遇到 503（本次第 5 个请求就是），重试是必需的，不是异常路径。
3. User-Agent 要写能联系到维护者的信息；这是自托管软件，所有实例共用同一个应用名，一旦行为不当会按 UA 整体被限。联系方式用什么由主代理和用户定。
4. 模糊 TOC 的结果上限是 25 个且不告知是否截断；精确 disc ID 命中和模糊命中的返回结构不同，把握程度也不同，接口上要分开表达。
5. 搜索的 `score` 只能用来在同一次结果内排序，不能跨查询比较，也不能直接换算成把握程度。
6. 条码、ISRC 在 MusicBrainz 里都不保证唯一：同一条码挂在多个发行上，同一 ISRC 可以对应多条录音。
7. 本次验证请求的 User-Agent 用的是通用名字，没有带任何个人或项目信息；共 5 次，间隔均不少于 2 秒。
8. 调研依据的源码是各仓库 master 分支在 2026-11-22 的内容，线上服务的部署版本可能略有差异。

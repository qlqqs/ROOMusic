# Research: Cover Art Archive 与 LRCLIB 的接口、限速与同类做法

- **Query**: 为 Go 后端内置的两个外部源适配器（Cover Art Archive 封面、LRCLIB 歌词）查清接口、重定向、状态码、限速与条款、数据特点、同类项目做法
- **Scope**: external 为主（官方文档、官方服务源码、同类项目源码、少量只读实测），附本仓库相关位置
- **Date**: 2026-11-22

## 0. 取证方式与标注

- 实测：2026-11-22，通用 User-Agent `api-research/1.0`，只读请求，间隔 ≥1.5 秒；coverartarchive.org 10 次、lrclib.net 10 次（含文档页 2 次）。
  本机出站经过 HTTP 代理，耗时与 archive.org 落到的节点主机名会受代理出口位置影响，耗时都是单次样本。
- 源码引用都固定到提交号。行号以该提交为准。
- 标注含义：**[文档]** 官方文档原文；**[源码]** 官方服务或同类项目源码；**[实测]** 本次请求看到的结果；**[推断]** 由源码或文档推出、没有实测；**[未核实]** 没找到依据。
- 实测用的公开例子：Portishead《Dummy》release `76df3287-6cda-33eb-8e9a-044b5e15ffdd`、release-group `48140466-cff6-3222-bd55-63c27e43190d`（两者都是官方文档里的例子）；
  官方文档例子里的 PNG 图 `release/f268b8bc-2768-426b-901b-c7966e76de29/12750224075.png`；LRCLIB 文档例子 `I Want to Live`（id 3396226）。

---

## 第一部分：Cover Art Archive

### 1. 接口与清单结构

来源：[Cover Art Archive / API](https://musicbrainz.org/doc/Cover_Art_Archive/API)（页面注明取自修订 #77795）；
重定向服务源码 [metabrainz/artwork-redirect `request.py`](https://github.com/metabrainz/artwork-redirect/blob/c07cada5b7a1325d6859987461712b46144e18a8/artwork_redirect/request.py)。

| 路径 | 方法 | 成功 | 说明 |
| --- | --- | --- | --- |
| `/release/{mbid}/` | GET、HEAD | 307 → `index.json` | release 的全部图片清单 **[文档][实测]** |
| `/release/{mbid}/front`、`/back` | GET、HEAD | 307 → 原图 | 社区选定的主封面 / 主封底 **[文档][实测]** |
| `/release/{mbid}/{id}` | GET、HEAD | 307 → 原图 | 指定图片；`{id}` 取自清单 **[文档][实测]** |
| `/release/{mbid}/({id}\|front\|back)-(250\|500\|1200)` | GET、HEAD | 307 → 缩略图 | 文档原话：跳转后的请求“可能因缩略图不存在而 404” **[文档]** |
| `/release-group/{mbid}/` | GET、HEAD | 文档写 200；实测是 307 → `index.json` | 见下文“release-group” |
| `/release-group/{mbid}/front[-(250\|500\|1200)]` | GET、HEAD | 307 | release-group 只支持 `front`，其他文件名返回 400 **[源码]** `request.py` L445-L456 |

细节：

- 路径里的后缀可有可无：`/front`、`/front.jpg`、`/front-500`、`/front-500.jpg` 等价 **[源码]** 测试 [`test/test_all.py` L86-L93](https://github.com/metabrainz/artwork-redirect/blob/c07cada5b7a1325d6859987461712b46144e18a8/test/test_all.py)。
- MBID 只接受小写十六进制：正则 `[a-f0-9]{8}-…`，大写返回 400 **[源码]** `request.py` L68、L103-L108；**[实测]** `GET /release/76DF3287-6CDA-33EB-8E9A-044B5E15FFDD/front-500` → `400`，响应体是 HTML，正文 `invalid MBID specified`。
- 全部端点支持 OPTIONS，返回 `Allow: GET, HEAD, OPTIONS` **[文档][源码]** L392-L429。
- 所有响应带 `Access-Control-Allow-Origin: *` **[源码]** [`server.py` L121](https://github.com/metabrainz/artwork-redirect/blob/c07cada5b7a1325d6859987461712b46144e18a8/artwork_redirect/server.py)；**[实测]** 一致。

#### 清单 JSON（`index.json`）

顶层两个键：`images`（有序数组）、`release`（MusicBrainz release 的地址）。**[文档][实测]**

`images[]` 每项的键（实测 9 个：`approved`、`back`、`comment`、`edit`、`front`、`id`、`image`、`thumbnails`、`types`）：

| 键 | 类型 | 含义 |
| --- | --- | --- |
| `types` | 字符串数组，可为空 | 图片类型，可多值。实测例子里同时出现 `["Front"]` 与 `["Front","Booklet"]` |
| `front` | 布尔 | 是否“主封面”，即 `/front` 返回的那一张。与“`types` 含 Front”不是一回事：实测例子里第二张图的 `types` 是 `["Front","Booklet"]`，但 `front` 为 `false`，6 张图里只有 1 张 `front` 为 `true` |
| `back` | 布尔 | 是否“主封底” |
| `approved` | 布尔 | 这张图的编辑是否已通过 MusicBrainz 编辑系统 |
| `image` | 字符串 | 原图地址，形如 `…coverartarchive.org/release/{mbid}/{id}.{后缀}` |
| `thumbnails` | 对象 | 键 `250`、`500`、`1200`，以及已弃用的 `small`（=250）、`large`（=500） |
| `id` | 数字或字符串 | archive.org 侧的文件编号，上传时由 MusicBrainz 分配、不再变化 |
| `comment` | 字符串 | 自由文本，可为空串 |
| `edit` | 数字 | MusicBrainz 编辑号 |

需要留意的不一致：

- **`id` 的 JSON 类型不固定**：官方文档的例子是字符串（`"id":"829521842"`），**[实测]** 同一 release 现在返回的是数字（`"id": 829521842`）；
  现行索引器直接写数据库里的整数 **[源码]** [artwork-indexer `handlers_base.py` L365-L381](https://github.com/metabrainz/artwork-indexer/blob/2b1739be5adecf4515849f1e2dc91c3403a76c9a/handlers_base.py)。字符串形式是否仍存在于旧清单 **[未核实]**。
- **清单里的地址可能是 `http://`**：**[实测]** 《Dummy》的清单（`Last-Modified` 2019-11-12）里 `image` 与 `thumbnails` 全是 `http://coverartarchive.org/…`，`release` 是 `https://musicbrainz.org/…`；
  现行索引器的模板是 `https://{domain}/…` **[源码]** `handlers_base.py` L41。即新旧清单的协议不同。
- `thumbnails` 的键：文档说明为 `250`、`500`、`1200` 加两个弃用别名；文档的第二个示例里有一项只有 `small`、`large`，没有数字键 **[文档]**；
  现行索引器五个键都写 **[源码]** `handlers_base.py` L372-L378。只含别名的旧清单现在是否还存在 **[未核实]**。
- 清单里的缩略图地址后缀恒为 `.jpg`（索引器写死 `'jpg'`）**[源码]** `handlers_base.py` L373-L377。
- 类型全集（18 种）：Front、Back、Booklet、Medium、Tray、Obi、Spine、Track、Liner、Sticker、Poster、Matrix/Runout、Top、Bottom、Panel、Watermark、Raw/Unedited、Other
  **[文档]** [Cover Art / Types](https://musicbrainz.org/doc/Cover_Art/Types)。
- “主封面”的定义：类型含 Front、且排序最靠前的那张 **[文档]** [How to Add Cover Art](https://musicbrainz.org/doc/How_to_Add_Cover_Art)（“The image with type front which is closest to the first position will be used as the release front cover”）；
  **[源码]** `request.py` L220-L252：`is_front = true … ORDER BY ordering ASC LIMIT 1`。

#### release-group

- 清单就是被选中的那个 release 的 `index.json`：**[实测]** `/release-group/48140466-…/` → 307 → `https://archive.org/download/mbid-76df3287-…/index.json`，
  与 `/release/76df3287-…/` 的最终地址、内容（4445 字节）完全相同。
- **指明来源 release 的方式**：顶层 `release` 字段（`https://musicbrainz.org/release/{release mbid}`），以及每个 `image` / `thumbnails` 地址里的 `/release/{release mbid}/` 路径段。
  清单里没有 release-group 的 MBID。**[文档][实测]**
- 清单包含那个 release 的全部图片（不只是封面）**[实测]**（6 张：Front×2、Booklet×3、Back、Medium）。
- 选哪个 release **[源码]** `request.py` L178-L218：
  1. 只在“有主封面（`is_front`）”的 release 里选；
  2. 社区为该 release-group 指定过封面的 release 优先（`release_group_cover_art`）；
  3. 其次主封面不带 `Raw/Unedited` 类型的优先；
  4. 再按发行日期从早到晚。
  都没有则 404。
- 文档写 `/release-group/{mbid}/` 成功返回 200、方法不支持返回 501；**[实测]** 首跳是 307（与 release 相同），源码也是同一个 `handle_dir`（L384-L390、L445-L448）。

### 2. 重定向链（实测）

`GET https://coverartarchive.org/release/76df3287-…/front-500`：

| 跳 | 主机 | 状态 | 关键响应头 |
| --- | --- | --- | --- |
| 1 | `coverartarchive.org`（HTTP/2，头里有 `x-mb-gateway`） | 307 | `Location: https://archive.org/download/mbid-{mbid}/mbid-{mbid}-{id}_thumb500.jpg`；`Content-Type: text/plain`；正文 `See: <地址>` |
| 2 | `archive.org`（HTTP/1.1，nginx） | 302 | `Location: https://dn711103.ca.archive.org/0/items/mbid-{mbid}/mbid-{mbid}-{id}_thumb500.jpg`；带 `Strict-Transport-Security` |
| 3 | `dn711103.ca.archive.org` | 200 | `Content-Type: image/jpeg`、`Content-Length: 35043`、`ETag`、`Last-Modified`、`Accept-Ranges: bytes` |

- 清单同样三跳：`coverartarchive.org` 307 → `archive.org/download/mbid-{mbid}/index.json` 302 → `dn*.ca.archive.org/0/items/mbid-{mbid}/index.json` 200，
  `Content-Type: application/json`、`Content-Length: 4445` **[实测]**。
- 第三跳的主机名不固定：本次看到 `dn711103.ca.archive.org`、`dn711107.ca.archive.org`；官方文档的旧示例里是 `s3.us.archive.org` 与 `archive.org/download/…`；
  `ia*.us.archive.org` 形式本次没有出现 **[实测][文档]**。可确定的共同点只有“`archive.org` 或其子域”，子域层数与命名都会变。节点的选择规则 **[未核实]**。
- 文件命名：原图 `mbid-{mbid}-{id}.{jpg|png|gif|pdf}`，缩略图 `mbid-{mbid}-{id}_thumb{250|500|1200}.jpg` **[源码]** `request.py` L40、L470-L485；**[实测]** 一致。
- **Content-Length**：最终 200 响应都提供（清单、缩略图、原图，GET 与 HEAD 都有）**[实测]**。HEAD 可以沿整条链使用 **[实测]**。
- **HTTPS**：以 `https://` 请求时三跳全程 HTTPS **[实测]**。第一跳的 `Location` 协议跟随请求协议（配置里是 `//archive.org/download`，按请求协议或 `X-Forwarded-Proto` 补全）
  **[源码]** [`config.default.ini`](https://github.com/metabrainz/artwork-redirect/blob/c07cada5b7a1325d6859987461712b46144e18a8/config.default.ini)、`server.py` L72-L88。
  但旧清单里写的图片地址是 `http://`（见上），照抄清单里的地址请求就不是 HTTPS 起步。
- 耗时（单次、经代理）：清单链 1.5 秒，`front-500` 链 1.7 秒，release-group 清单链 3.0 秒 **[实测]**。

### 3. 无封面、合并、限速与条款

**无封面 / 错误** **[文档][源码][实测]**

| 情况 | 状态 | 说明 |
| --- | --- | --- |
| release 不存在，或存在但没有任何图片 | 404 | 两种情况不区分；**[实测]** 不存在的 UUID → 404，`Content-Type: text/html`，正文 `No cover art found for release {mbid}` |
| 有图片但没有主封面，请求 `/front` | 404 | 正文 `No front cover image found for…` **[源码]** 测试 L77-L80 |
| release-group 没有可用封面 | 404 | `No cover art found for release group…` |
| MBID 不是合法（小写）UUID | 400 | HTML 正文 |
| release-group 请求 `front` 以外的文件名 | 400 | **[源码]** L452-L456 |
| 方法不是 GET/HEAD | 405（release-group 文档写 501） | **[文档]** |
| `Accept` 无法满足 | 406 | **[文档]**（只列在 `/release/{mbid}/`） |
| 超出限速 | 503 | **[文档]** 各端点都列了这一条 |
| 服务内部异常 | 500 | 正文 `Whoops. Our bad.` **[源码]** `server.py` L113-L119 |
| 缩略图尚未生成或不存在 | 第一跳 307 正常，后面 archive.org 一侧 404 | **[文档]** |

错误响应体是 HTML 或纯文本，不是 JSON **[实测]**。

**release 被合并（MBID 重定向）**

- 服务会解析合并产生的 MBID 重定向：查询里先查 `{entity}_gid_redirect`，再查实体表 **[源码]** `request.py` L125-L136；
  仓库 [README](https://github.com/metabrainz/artwork-redirect/blob/c07cada5b7a1325d6859987461712b46144e18a8/README.md) 也写明“taking into account MBID redirects caused by release and event merges”。
- 清单请求 `/release/{旧mbid}/`：307 的目标用的是**合并后**的 MBID（`mbid = self.resolve_release_cover_index(mbid)` 后再拼地址）**[源码]** L431-L434、L138-L156。
  客户端看到的只是普通的 307，新 MBID 出现在 `Location` 的桶名和清单的 `release` 字段里。
- `/release/{旧mbid}/front*` 与 `/{id}`：图片编号按合并后的 release 解析，但拼 archive.org 地址时用的是**请求里的旧 MBID**（`handle_redirect(request, mbid, filename)` 的 `mbid` 没有被替换）**[源码]** L436-L443、L476-L485。
  而合并时索引器会把图片复制到新桶、并从旧桶删除 **[源码]** [artwork-indexer README](https://github.com/metabrainz/artwork-indexer/blob/2b1739be5adecf4515849f1e2dc91c3403a76c9a/README.md)（`copy_image`、`delete_image` 两个事件）。
  **[推断]** 因此旧 MBID 走 `/front*` 捷径可能在 archive.org 一侧 404，而走清单再取清单里的地址不受影响。**这一点没有实测**（手头没有带封面的已合并 MBID，且请求额度已用完）。
- release-group 的 MBID 重定向同样被解析，且后续用的是解析出的 release MBID **[源码]** L203、L445-L451。
- 合并后清单更新有没有延迟（索引器是事件队列）**[未核实]**。

**限速与条款**

- 官方文档原文：“There are currently no rate limiting rules in place at http://coverartarchive.org.” **[文档]** [API 页 “Rate limiting rules”](https://musicbrainz.org/doc/Cover_Art_Archive/API)。
  同一页每个端点又都列了 “503 if the user has exceeded their rate limit”。
- **是否与 MusicBrainz 每秒 1 次共用**：[MusicBrainz API / Rate Limiting](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) 讲的是 MusicBrainz 的 Web Service（按来源 IP 平均每秒 1 次，超出时该 IP 的请求全部 503），
  全文没有提到 coverartarchive.org **[文档]**。Picard 把 `coverartarchive.org` 与 `https://archive.org` 的最小请求间隔都设为 0 **[源码]**
  [`caa.py` L88-L89](https://github.com/metabrainz/picard/blob/741cbfd77d7830d7d04dd531d0624e8c1a585322/picard/coverart/providers/caa.py)。
  第一跳确实由 MetaBrainz 的网关提供（响应头 `x-mb-gateway`）**[实测]**；网关对 coverartarchive.org 是否套用同一套按 IP 计数 **[未核实]**。
- **User-Agent**：CAA 的 API 文档没有提 User-Agent **[文档]**；MusicBrainz 限速页要求对 MusicBrainz 的请求带可联系的 User-Agent（建议 `应用名/版本 ( 联系地址 )`），
  空 UA 与若干库默认 UA 被视为“匿名”而限流 **[文档]**。这条要求是否适用于 coverartarchive.org **[未核实]**。本次用通用 UA 的 10 次请求全部正常 **[实测]**。
- **503/429**：文档只提 503。本次没有遇到 503 或 429 **[实测]**。archive.org 一侧（第 2、3 跳）的限流规则、返回码与 `Retry-After` **[未核实]**（没找到公开的规则文档）。
- **使用条款**：[Cover Art Archive](https://musicbrainz.org/doc/Cover_Art_Archive) 的 Policy 一节：图片为存档目的收集、向公众开放，“Use the images at your own risk”，
  并请使用者尊重艺人与厂牌的权利；另指向 Internet Archive 自己的政策 **[文档]**。图片本身的版权归属不因收录而改变；没有统一的授权协议 **[文档未给出]**。

### 4. 图片格式与大小

- **可上传的格式**：“gif, jpg, jpeg, png, htm, html, jpe, jfif and pdf”（原文如此）；**“There's no size limit”**；建议扫描用 600dpi **[文档]**
  [How to Add Cover Art](https://musicbrainz.org/doc/How_to_Add_Cover_Art)。
- **原图实际落盘的后缀只有四种**：`jpg`、`gif`、`png`、`pdf` **[源码]** `request.py` L40；数据库表 `cover_art_archive.image_type(mime_type, suffix)` 与 `cover_art.mime_type`
  [musicbrainz-server `CreateTables.sql` L16-L34](https://github.com/metabrainz/musicbrainz-server/blob/42caf9ae663cef43ec489853786b866f3dbe6316/admin/sql/caa/CreateTables.sql)（具体行数据没读到，**[未核实]** 四种 MIME 的精确写法）。
  PDF 确实存在：Picard 专门跳过或单独处理 `.pdf` **[源码]** `caa.py` L327-L330、L377-L390；类型说明里也写数字版 booklet 常是 PDF **[文档]** Cover Art / Types。
  PDF 主要出现在 Booklet 类；主封面是 PDF 的比例 **[未核实]**。
- **缩略图恒为 JPEG**：重定向服务把任何 `-250|500|1200` 请求改写成 `_thumb{size}.jpg`，不论原图后缀 **[源码]** L470-L474；索引器写的缩略图地址后缀固定 `jpg` **[源码]**；
  **[实测]** PNG 原图的 `-1200` 返回 `Content-Type: image/jpeg`。GIF、PDF 原图的缩略图本次没有实测，按源码也是 `.jpg`。
- **大小的实测样本**（只有两张图，不构成分布）：

  | 图 | 原图 | 250 | 500 | 1200 |
  | --- | --- | --- | --- | --- |
  | 《Dummy》主封面（2012 年上传的 JPEG） | 76,106 字节 `image/jpeg` | 未测 | 35,043 字节，494×500 | 40,684 字节 |
  | 文档例子的 PNG（Back 类） | **5,306,633 字节** `image/png` | 未测 | 未测 | 215,423 字节 `image/jpeg` |

  《Dummy》的 1200 档只比 500 档大一点、且小于原图，**[推断]** 原图边长不足 1200 时缩略图不放大（没有读图片尺寸核实）。
- **原图大小的整体分布 [未核实]**：没有找到官方统计。可确定的只有：上传不限大小、鼓励高分辨率扫描（“Higher quality/resolution images trump lower quality images”）、
  数据库为每张图记录 `filesize` 与三档缩略图的大小（`CreateTables.sql` L30-L33，说明官方有数据但没公开成统计页）。几十 MB 的 PNG 扫描件“可以存在”，是否常见没有依据。
- 缩略图的边长含义（最长边还是宽）**[未核实]**；实测 500 档是 494×500（高为 500）。
- 1200 档是后加的：《Dummy》的 `_thumb1200.jpg` 的 `Last-Modified` 是 2018-03-16，而原图是 2012-04-30 **[实测]**；文档说缩略图可能不存在 **[文档]**。

**下载哪一档（按上面事实整理的依据，不是官方建议）**

- 官方没有推荐档位 **[文档]**。
- 三档缩略图：格式固定 JPEG、体积可预期（样本里 1200 档 40KB–215KB）；原图：格式四选一（含 PDF）、大小无上限（样本里 5.3MB）。
- 本仓库现有封面校验认 jpeg、png、gif、webp 四种文件头（`backend/internal/catalog/artwork/artwork.go` L296-L308），PDF 不在其中。
- 同类项目的默认：Picard 默认 500；beets 只有用户设的 `maxwidth` 恰为 250/500/1200 时取缩略图，否则取原图；Jellyfin 插件登记原图地址、用 500 做预览（见第 5 节）。
- 缩略图可能缺失（跳转后 404），Picard 的做法是从目标档往更小的档回退，再回退到原图（`caa_url_fallback_list`，但代码里留有 “FIXME: try other urls in case of 404”）。

### 5. 同类项目的做法

| 项目 | 取哪一档 | release 与 release-group | 怎样筛封面 | 来源 |
| --- | --- | --- | --- | --- |
| **Picard** | 默认 500（可选 250/500/1200/原图）；按“不大于所选档”从大到小找可用的缩略图键（数字键没有时用 `large`/`small`），都没有或是 PDF 时用原图 | 两个 provider，默认顺序是 release-group 在前、release 在后；release-group 这个只在“还没找到主封面”时启用，它的清单 404 被忽略 | 默认只要类型 `front`，并排除 `matrix/runout`、`raw/unedited`、`watermark`；`approved` 默认不要求；类型为空的图当作 `unknown`；先看 MusicBrainz 返回的 `cover-art-archive` 节点判断有没有图再请求清单 | [`caa.py`](https://github.com/metabrainz/picard/blob/741cbfd77d7830d7d04dd531d0624e8c1a585322/picard/coverart/providers/caa.py) L75-L116、L232-L262、L312-L400；[`caa_release_group.py`](https://github.com/metabrainz/picard/blob/741cbfd77d7830d7d04dd531d0624e8c1a585322/picard/coverart/providers/caa_release_group.py)；[`const/defaults.py` L74-L93](https://github.com/metabrainz/picard/blob/741cbfd77d7830d7d04dd531d0624e8c1a585322/picard/const/defaults.py)；[`options.py` L220-L233](https://github.com/metabrainz/picard/blob/741cbfd77d7830d7d04dd531d0624e8c1a585322/picard/options.py) |
| **beets（fetchart）** | `maxwidth` 恰为 250/500/1200 时取对应缩略图（兼容 `small`/`large`），否则取原图 `image` | 先 release（记为精确匹配），再 release-group（记为兜底）；`match_by` 可配 | 清单里 `types` 含 `"Front"` 的每一张都作为候选（不看 `front` 布尔、不看 `approved`）；下载后只接受 `image/jpeg`、`image/png` | [`fetchart.py` L48-L53、L510-L589](https://github.com/beetbox/beets/blob/458390ea297496ea7f0a8629e5453dee59b722d9/beetsplug/fetchart.py) |
| **Jellyfin** | 登记的下载地址是原图 `image`；预览用 `large`（500），没有则 `small`（250） | 先 release；没有结果再 release-group | release：第一张 `types` 恰好只有 Front 的图；release-group：第一张 `types` 含 Front 的图；`approved` 只换算成评分 1/0，不筛选 | 独立插件 [jellyfin-plugin-coverartarchive `CoverArtArchiveImageProvider.cs` L73-L139](https://github.com/jellyfin/jellyfin-plugin-coverartarchive/blob/5f4bd64dea6647f0ec655c2b9b06d5bcfea13a03/Jellyfin.Plugin.CoverArtArchive/CoverArtArchiveImageProvider.cs)（用 MetaBrainz.MusicBrainz.CoverArt 库）。Jellyfin 核心是否内置 **[未核实]** |
| **Navidrome** | 示例插件把缩略图的每个数字键都作为一档返回（250/500/1200），没有数字键时用 `large`/`small`，再没有才用原图 | 只用 `/release/{mbid}`，不查 release-group | 先找 `front == true`；没有则找 `types` 含 `"Front"`；再没有取第一张 | Navidrome 核心没有内置 CAA：默认外部代理是 `deezer,lastfm,listenbrainz`，封面优先级默认 `cover.*, folder.*, front.*, embedded, external`（[`configuration.go` L1009、L1077](https://github.com/navidrome/navidrome/blob/4cd7ab1c60e0e5cf9644c4f5dfad96a10a38120a/conf/configuration.go)）；CAA 只作为插件示例 [`plugins/examples/coverartarchive-py`](https://github.com/navidrome/navidrome/blob/4cd7ab1c60e0e5cf9644c4f5dfad96a10a38120a/plugins/examples/coverartarchive-py/plugin/__init__.py) |

共同点：四家都走“取清单再自己挑”，没有一家用 `/front-500` 这类捷径端点。

---

## 第二部分：LRCLIB

主要来源：官方文档 [https://lrclib.net/docs](https://lrclib.net/docs)（无 JavaScript 版本 [docs.md](https://lrclib.net/docs.md)，本次读的是这份）；
服务端开源代码 [tranxuanthang/lrclib](https://github.com/tranxuanthang/lrclib/tree/ffdfed4dd8288f9065f5562508c13705cbe0436c)（README 称它是 lrclib.net 的源码；线上部署与仓库是否逐行一致 **[未核实]**，下文凡标 [源码] 的行为都以实测或文档交叉核对过的为准，没交叉核对的会注明）。

> 与旧资料的差别（容易踩）：现行文档已经有明确的限速表、把 User-Agent 写成“require”、新增 `hasWordSync` / `lyricsfile` 字段和 `/api/v2/search`，
> 并且不再有 `/api/get-cached`。网上很多说明（“没有限速”“`/api/get` 会同步回源外部”）是旧版本的。

### 6. 接口

基址 `https://lrclib.net`，全部 GET，开放 CORS。**[文档]**

#### `GET /api/get`

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `track_name` | 是 | 不能为空串 |
| `artist_name` | 是 | 不能为空串 |
| `album_name` | 否 | 文档写“recommended” |
| `duration` | 否 | 秒，必须在 1–3600 之间，否则 400 |

- **时长容差 ±2 秒**：文档原话“only when the duration matches the record in LRCLIB's database, or at least with a difference of ±2 seconds”；
  **[源码]** `tracks.duration >= dur-2 AND tracks.duration <= dur+2`
  [`track_repository.rs` L186-L192](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/core/src/repositories/track_repository.rs)；
  **[实测]** 以 269 秒查询命中库里 270 秒的记录。不传 `duration` 就不加时长条件；不传 `album_name` 就不加专辑条件 **[源码]** L186-L198。
- **匹配方式是规范化后的全等**：标题、艺人、专辑各自规范化后与库里的 `name_lower`、`artist_name_lower`、`album_name_lower` 做 `=` 比较 **[源码]** L177-L198。
  规范化规则 [`core/src/text.rs` L6-L33](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/core/src/text.rs)：
  去变音符并转小写；把 `` ` ~ ! @ # $ % ^ & * ( ) _ | + - = ? ; : " , . < > { } [ ] \ / `` 与换行替换成空格；删除 `'` 与 `’`；合并连续空白。
  例：`Don't Stop!` → `dont stop`，`AC/DC` → `ac dc`。只处理这些 ASCII 标点，**全角标点（（）、，：等）与简繁体都不处理**。
- **多条命中时取 id 最小的一条**（`ORDER BY tracks.id LIMIT 1`）**[源码]** L202-L205。不按“有没有同步歌词”排序。
- 专辑名是硬条件：传了 `album_name` 而库里那条记录的专辑名写法不同，就查不到。

#### `GET /api/get/{id}`

按记录 id 取，返回结构与 `/api/get` 相同；不存在返回 404 `TrackNotFound` **[文档][源码]**。

#### `GET /api/get-cached`

**现在不存在**：现行文档没有这个端点，开源路由表里也没有（[`router.rs` L34-L59](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/router.rs)）；
**[实测]** 带文档示例参数请求 `/api/get-cached` → `404`，响应体为空、没有 `Content-Type`。

#### `GET /api/search`

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `q` | 与 `track_name` 二选一 | 在标题、艺人、专辑任一字段里找关键词；**提供时优先，结构化参数被忽略** |
| `track_name` | 与 `q` 二选一 | 标题关键词 |
| `artist_name` | 否 | 艺人关键词 |
| `album_name` | 否 | 专辑关键词 |

- 两者都没给时返回空数组 `[]`（200）**[文档]**。
- **最多 20 条，不分页** **[文档][源码]** [`search_index.rs` L24](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/search_index.rs)；**[实测]** 两次搜索都正好 20 条。
- **没有时长参数**，也不按时长过滤或排序；结果按检索得分排 **[源码]** `search_index.rs` L163。
- 每条结果带完整歌词与 `lyricsfile`，响应很大：**[实测]** 20 条约 200–227 KB。
- 匹配规则 **[源码]** [`search_index/query.rs` L40-L93](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/search_index/query.rs)：
  `q` 的每个词都必须出现（任一字段）；结构化时标题与专辑按**短语**匹配、艺人按**词集合**匹配（顺序无关，夹在中间的 `feat`/`ft`/`featuring` 可缺）。
  分词器是“按非字母数字切分 + 小写”（`search_index.rs` L297-L302），所以**一串连续的汉字是一个词，不做子串匹配**：
  **[实测]** `/api/v2/search?track_name=七里` 返回 0 条（库里有《七里香》）。

#### `GET /api/v2/search`（beta）

参数与 `/api/search` 相同，同样最多 20 条；只返回摘要，不含歌词文本：`id`、`trackName`、`artistName`、`albumName`、`duration`、`instrumental`、`hasWordSync`、`hasPlainLyrics`、`hasSyncedLyrics`、`hasLyricsfile`；
文档说明它是 beta、字段可能变、不在 v1 的兼容承诺内，取歌词再调 `/api/get/{id}` **[文档]**；**[实测]** 端点存在，返回 JSON 数组。

#### 返回字段（`/api/get`、`/api/get/{id}`、`/api/search` 的每条）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | number | 记录的稳定编号 |
| `name` | string | `trackName` 的别名，为兼容保留 |
| `trackName`、`artistName`、`albumName` | string | 源码里是可空类型；**[实测]** `albumName` 出现过空串 |
| `duration` | number | 秒，**可能带小数**（实测 `233.0`、`269.746667`、`316.138667`） |
| `instrumental` | boolean | 为真表示没有歌词 |
| `hasWordSync` | boolean | `lyricsfile` 里含逐字时间时为真 |
| `plainLyrics` | string \| null | 纯文本歌词 |
| `syncedLyrics` | string \| null | LRC 格式、行级时间 |
| `lyricsfile` | string | Lyricsfile YAML 原文；文档称每条记录都有 |

来源：**[文档]** “Lyrics record fields”；**[源码]** [`get_lyrics_by_metadata.rs` L31-L45](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/routes/get_lyrics_by_metadata.rs)；**[实测]** 字段齐全。

#### 没找到与错误

| 状态 | `name` | 说明 |
| --- | --- | --- |
| 404 | `TrackNotFound` | **[实测]** 响应体 `{"message":"Failed to find specified track","name":"TrackNotFound","statusCode":404}`，`Content-Type: application/json` |
| 400 | `ValidationError` | 例 `{"message":"duration: must be between 1 and 3600","name":"ValidationError","statusCode":400}` **[文档]** |
| 400（纯文本） | — | 缺少必填查询参数时由框架直接拒绝，正文是纯文本，如 `Failed to deserialize query string: missing field \`artist_name\`` **[文档]** |
| 401 | `InvalidAppIdError` / `MissingApiKeyError` / `InvalidApiKeyError` | 只在带了无效的 `Lrclib-App-Id` / `Lrclib-Api-Key` 时出现 **[文档]** |
| 429 | `RateLimitExceeded` | 带 `Retry-After`（秒）与 `scope`（`ip` 或 `application`）**[文档]** |
| 500 | `UnknownError` | **[文档]** |
| 503 | `ServerOverloaded` | 带 `Retry-After`（通常 1）**[文档][源码]** [`errors.rs` L78-L92](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/errors.rs) |

- 错误体里状态码的键是 **`statusCode`**（不是旧资料里的 `code`）**[文档][实测]**。
- `/api/search` 没结果是 `200` + `[]`，不是 404 **[实测]**。
- 文档提醒：先看状态码与 `Content-Type`，不要假定响应体一定是 JSON（网络边缘的拒绝可能不是 JSON）**[文档]**。

### 7. 限速与使用条款

- **不需要密钥**，匿名可用；注册是可选的 **[文档]**。
- **限速（现行文档，注明是近似值、不属于接口合同、可随时调整）** **[文档]**：

  | 请求怎样标识自己 | 约略额度 |
  | --- | --- |
  | 通用客户端（没有 User-Agent、浏览器、`curl`、或常见 HTTP 库的默认 UA） | 每 IP 约 45 次/分钟 |
  | 标明应用的 User-Agent | 每 IP 约 90 次/分钟 |
  | 注册的客户端应用（`Lrclib-App-Id`，公开标识） | 每 IP 约 180 次/分钟，另有应用级约 6000 次/分钟 |
  | 注册的服务端应用（`Lrclib-Api-Key`，保密） | 只有应用级约 6000 次/分钟 |

  按 IP 计（IPv6 按 /64），匀速补充、带小的突发余量。Go 默认的 `Go-http-client/…` 是否被算作“常见 HTTP 库” **[未核实]**（文档只举了 `curl` 为例）。
- **User-Agent**：文档写“We require you to identify your client in requests”，格式为应用名、版本、主页或邮箱，示例 `LRCGET v0.2.0 (https://github.com/tranxuanthang/lrcget)`；
  改不了 `User-Agent` 时可用 `X-User-Agent` 或 `Lrclib-Client` **[文档]**；服务端取值顺序是 `Lrclib-Client` → `X-User-Agent` → `User-Agent` **[源码]** `router.rs` L66-L81。
  本仓库现有约定的 `ROOMusic/<版本> ( <项目地址> )`（`.trellis/spec/backend/sources-contracts.md` 第 77 行）包含应用名、版本与项目地址三要素，写法是 MusicBrainz 的斜杠括号式，与 LRCLIB 示例的 `名称 v版本 (地址)` 排版不同；LRCLIB 是否校验具体排版 **[未核实]**。
- **429 的处理要求**：必须遵守 `Retry-After`，至少等那么多秒，并加一点随机抖动（文档举例为延迟的 30% 以内）；无视 `Retry-After` 继续请求“may result in a temporary ban” **[文档]**。
- **节流建议**：串行发送，请求之间留 200–500 毫秒，尤其是扫描整库这类批量操作 **[文档]**。
- **`/api/get` 与外部回源**：现行文档写“Missing tracks can be picked up by the background lyrics fetching service and may become available in a later request”——
  没命中时**当次直接 404**，由后台服务之后补，不在请求里同步等待 **[文档]**；**[实测]** 查一个不存在的曲目 0.39 秒返回 404（命中的请求 0.38–0.40 秒，搜索 0.58–0.65 秒，均经代理、单次）。
  开源代码的请求路径里只有查库，`missing_tracks` 表“no code writes to it anymore”（[ARCHITECTURE.md](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/ARCHITECTURE.md)）**[源码]**；
  线上“后台补歌词服务”的来源、触发条件与补齐所需时间 **[未核实]**。旧版本“`/api/get` 同步请求外部源、可能很慢”的说法在现行文档与代码里都找不到，**[未核实]** 其历史细节。
- **服务端对未命中的缓存**：`/api/get` 的结果（含“没找到”）按“规范化的标题、艺人、专辑、四舍五入后的时长”缓存，存活 24 小时、闲置 4 小时过期 **[源码]**
  `get_lyrics_by_metadata.rs` L117-L126、[`state.rs` L94-L98](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/state.rs)；代码里没有看到在新歌词发布时清这个缓存。
  **[推断]** 刚 404 过的同一组参数，短时间内重试大概率还是 404（线上是否同此 **[未核实]**）。
- **服务不可用时的表现**：
  - 过载：`503` + `Retry-After: 1` + JSON `ServerOverloaded`。机制是并发加载许可（默认 32）排队超过 250 毫秒即拒绝 **[源码]** `state.rs` L53-L81。
  - 限流：`429` + `Retry-After`。
  - 搜索索引未就绪或重建中：搜索请求失败（走 `UnknownError` 即 500），非搜索端点照常 **[源码]** ARCHITECTURE.md 第 229 行、[`search_lyrics.rs`](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/server/src/routes/search_lyrics.rs)。
  - 站点前面是 Cloudflare（响应头 `server: cloudflare`）**[实测]**；极端情况下边缘直接拒绝、响应体不保证是 JSON **[文档]**。Cloudflare 侧的 5xx 页面形态与历史宕机情况 **[未核实]**。
- 本次 10 次请求没有遇到 429 或 503，响应头里没有 `X-RateLimit-*` 一类的剩余额度头 **[实测]**。

### 8. 数据许可与内容特点

**许可**

- 服务端代码是 MIT（[LICENSE](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/LICENSE)）。
- **歌词数据本身的许可 [未核实]**：现行 API 文档全文没有 license 字样，只在 `/api/flag` 里把“copyright violation”列为可举报的理由 **[文档]**。
  数据是任何人匿名提交的（发布只需算力证明，不需账号）**[文档]**，歌词文本的版权归属不因收录而改变。数据库转储的页面与其条款本次没有读取。

**`syncedLyrics` 的 LRC 格式**

- 行格式 `[mm:ss.xx] 文本`：分钟两位、秒两位、**小数两位（百分秒）**，标签后有一个空格 **[实测]**（三个样本共 142 行，141 行是这个形态）；
  服务端由 Lyricsfile 生成 LRC 时写的就是 `[{分:02}:{秒:02}.{百分秒:02}]`，毫秒被截成百分秒 **[源码]**
  [`lyricsfile.rs` L461-L468](https://github.com/tranxuanthang/lrclib/blob/ffdfed4dd8288f9065f5562508c13705cbe0436c/core/src/lyricsfile.rs)。
- 但直接以 `syncedLyrics` 提交的旧式内容按原样保存（只判空，不重写）**[源码]** `lyricsfile.rs` L184-L218、L387-L391，所以可能出现别的写法。
  服务端自己解析 LRC 时接受的范围可作为“可能出现的形态”的参考：分钟位数不限、秒 1–2 位、小数 1–3 位、小数点可以是 `.` 或 `:`、一行开头可连着多个时间标签、可以有 `[ar:…]` 这类 ID 标签行
  **[源码]** L9-L15、L393-L446。这些变体在库里的实际占比 **[未核实]**。
- **标签后的空格不一致**：**[实测]** 同一份歌词里 50 行是 `[..] 文本`，最后一行是 `[04:26.63]`（无空格、无文本）。
- **空行**：`syncedLyrics` 里没有真正的空行，段落间隔与结尾用“只有时间标签、没有文本”的行表示（样本里 44 行中有 2 行、47 行中有 7 行）；
  结尾常有一行 `[mm:ss.xx] ` 作为结束标记 **[实测]**。`plainLyrics` 则用真正的空行分段（51 行里 9 个空行）**[实测]**。
- 行分隔是 `\n`，样本里没有 `\r` **[实测]**；末尾有没有换行不固定（文档示例的 `plainLyrics` 以 `\n` 结尾）。
- **增强 / 逐字标签**：样本的 `syncedLyrics` 里没有 `<mm:ss.xx>` 逐字标签 **[实测]**。逐字时间放在 `lyricsfile`（YAML）的 `lines[].words[]` 里，`hasWordSync` 指的就是它；
  从 Lyricsfile 导出 `syncedLyrics` 时每行只写行首时间、把各词文本拼接 **[源码]** L243-L256、L448-L459。
  旧式直接提交的 `syncedLyrics` 里是否有人写了 A2 逐字标签 **[未核实]**（服务端不拦）。
- **`syncedLyrics` 与 `lyricsfile` 的内容可以不一致**：**[实测]** 《晴天》id 7920568（`hasWordSync: true`）的 `syncedLyrics` 第一行是 `[00:28.90] 故事的小黃花`，
  而 `lyricsfile` 第一行是“故事的小黃花從出生那年就飄著”、`start_ms: 29250`——分行与时间都不同。
- `lyricsfile` 的结构（供辨认，不是本任务必须用）：`version: '1.0'`、`metadata`（`title`、`artist`、`album`、`duration_ms`、`offset_ms`、`language`、`instrumental`）、
  `lines[]`（`text`、`start_ms`、`end_ms`、可选 `words[]`）、可选 `plain` **[源码]** L53-L82；**[实测]** `language` 出现过 `z1` 这样的非标准值。
  格式说明在 [LYRICSFILE_CONCEPT.md](https://github.com/tranxuanthang/lrcget/blob/main/LYRICSFILE_CONCEPT.md)（Navidrome 源码注释里引用的地址，本次没有读全文）。

**`instrumental` 为真时**

- `plainLyrics` 与 `syncedLyrics` **都是 `null`** **[文档]**（字段表）；**[源码]** L203-L205 把两者置空；
  **[实测]** `GET /api/get?track_name=Intro&artist_name=The xx&album_name=xx&duration=128` → id 13939，`instrumental: true`、`plainLyrics: null`、`syncedLyrics: null`、
  `lyricsfile` 为 `…instrumental: true\nlines: []\n`。
- 判定方式：提交时三个歌词字段都为空即标为纯音乐 **[文档]**；或旧式 LRC 里带 `[au: instrumental]` 标签（不区分大小写）**[源码]** L352-L357。
- 反过来不成立：`instrumental: false` 的记录也可能两个字段都是 `null`（只有曲目信息、还没人贡献歌词）——beets 的源码注释专门写了这一点
  [`lyrics.py` L333-L344](https://github.com/beetbox/beets/blob/458390ea297496ea7f0a8629e5453dee59b722d9/beetsplug/lyrics.py)；也可能只有 `plainLyrics`、`syncedLyrics` 为 `null`
  **[实测]**（周杰倫《晴天》id 24316052）。

**华语歌曲：覆盖印象与写法差异**（以下只基于《晴天》这一首热门歌的两次搜索与一次精确查询，不能代表冷门曲目）

- 热门歌的记录很多：`track_name=晴天&artist_name=周杰伦` 与 `…&artist_name=周杰倫` 各返回 20 条（到上限），两组记录**完全不重叠** **[实测]**。
  即**简体与繁体的艺人名是两套互不相通的记录**；规范化不做简繁转换 **[源码]** `text.rs`。
- 同一首歌的专辑名写法五花八门 **[实测]**：`叶惠美`、`葉惠美`、`叶美惠`（错字）、`叶惠美 台湾首版`、`叶惠美（台湾百佳唱片172）`、`晴天`、空串、`Optional("")`、`9208万次观看`、
  `华语热门歌手大全`、`《周杰伦-这一刻》2CD-2` 等。`/api/get` 带专辑名时必须与其中某一条规范化后全等才命中。
- 时长也有脏数据 **[实测]**：同名记录里出现 `11.0`、`182.0`、`220.0`、`2886.89` 秒，以及正常的 269–271 秒（录音室版）、299 秒（现场版）、316–319 秒（其他来源）。
- **歌词文字的简繁与艺人名的简繁无关** **[实测]**：艺人写作简体 `周杰伦` 的记录，歌词是繁体（“故事的小黃花”）；艺人写作繁体 `周杰倫` 的记录，歌词反而是简体（“故事的小黄花”）。
- 歌词内容本身也有版本差异 **[实测]**：同一句在不同记录里是 `Re So So Si Do Si La` 与注音符号 `ㄖㄨㄟ ㄙㄡ ㄙㄡ ㄒ一 ㄉㄛ ㄒ一 ㄌㄚ` 两种写法。
- 精确查询可以命中 **[实测]**：`track_name=晴天&artist_name=周杰伦&album_name=叶惠美&duration=269` → id 17788（库里时长 270），0.40 秒。
  同样条件下库里还有时长 11 秒与 2886 秒的同名同专辑记录，被 ±2 秒的条件排除。
- 对精确匹配的影响（由上面的事实直接得出）：
  1. 标题、艺人、专辑任一项的简繁、错字、附加说明（版本、括注）与库里不同，`/api/get` 就是 404；
  2. 全角括号、全角逗号等不在规范化的标点表里，`歌名（Live）` 与 `歌名 (Live)` 规范化后不相等 **[源码]**；
  3. 艺人别名（中文名与拉丁名，如 `周杰伦` / `Jay Chou`）之间没有任何映射 **[源码]**；`Jay Chou` 写法下的记录数本次没查 **[未核实]**；
  4. 搜索按整词匹配，中文标题不能用前缀或片段搜（`七里` 搜不到《七里香》）**[实测]**；
  5. 多艺人：`/api/get` 的艺人是规范化后的整串全等。`A/B`、`A & B`、`A, B` 规范化后相同（分隔符都变成空格，结果都是 `a b`），而 `A、B` 里的顿号不在标点表里、保持原样，与前几种不相等；艺人的先后顺序不同也不相等 **[源码]** `text.rs`；
     `/api/search` 的艺人是词集合、顺序无关 **[源码]**；
  6. 搜索的分词按“非字母数字”切分，全角标点同样是分隔符，所以 `/api/search` 对全角与半角标点的差别不敏感，而 `/api/get` 敏感 **[推断]**（由 `text.rs` 与分词器的规则推出，没有实测）。
- 冷门华语歌曲的覆盖率 **[未核实]**。

### 9. 同类项目怎样用 LRCLIB

| 项目 | 查询顺序 | 时长的用法 | 其他 | 来源 |
| --- | --- | --- | --- | --- |
| **beets（lyrics 插件）** | **先 `/search`（只带艺人与标题），再 `/get`（带时长，有专辑才带专辑）**；注释说明原因：`/get` 有时返回不准或没有同步歌词的结果，`/search` 的候选更合适 | 候选的时长差必须 ≤ 目标时长的 **5%**（`DURATION_DIFF_TOLERANCE = 0.05`，240 秒的歌即 ±12 秒）；在合格候选里先选有同步歌词的，再选时长最接近的 | 纯音乐记为特殊文本；没有任何歌词且非纯音乐的候选无效；`plainLyrics` 为空时由同步歌词去掉时间标签得到纯文本；同步歌词每行去首尾空白 | [`lyrics.py` L282、L328-L405、L408-L467](https://github.com/beetbox/beets/blob/458390ea297496ea7f0a8629e5453dee59b722d9/beetsplug/lyrics.py) |
| **Jellyfin（LrcLib 插件）** | 二选一、不串联：默认“严格”只调 `/api/get`，且标题、艺人、专辑、时长缺一即放弃；关闭严格后只调 `/api/search`（可配置不带艺人、不带专辑） | 严格模式把时长（可带小数）交给服务端的 ±2 秒；搜索模式不按时长过滤，只把有同步歌词的排前面 | 进程内全局闸门：请求间隔至少 200 毫秒；429 时按 `Retry-After` 等待后重试 1 次（没有该头按 5 秒，上限 10 分钟）；艺人取第一个曲目艺人，没有则取第一个专辑艺人 | [`LrcLibProvider.cs` L60-L293](https://github.com/jellyfin/jellyfin-plugin-lrclib/blob/c4117d612efb7849823173e49190cfb072e252d7/Jellyfin.Plugin.LrcLib/LrcLibProvider.cs)；[`LrcLibRateLimiter.cs` L17-L25](https://github.com/jellyfin/jellyfin-plugin-lrclib/blob/c4117d612efb7849823173e49190cfb072e252d7/Jellyfin.Plugin.LrcLib/LrcLibRateLimiter.cs)；[`PluginConfiguration.cs`](https://github.com/jellyfin/jellyfin-plugin-lrclib/blob/c4117d612efb7849823173e49190cfb072e252d7/Jellyfin.Plugin.LrcLib/Configuration/PluginConfiguration.cs) |
| **LRCGET（LRCLIB 作者的官方客户端）** | 批量下载用 `/api/get`：标题与艺人必填，专辑非空才带，时长四舍五入成整数且在 1–3600 内才带；搜索是另一个由用户手动触发的功能（已支持 v2 摘要） | 交给服务端的 ±2 秒 | 请求超时 10 秒；批量任务最多尝试 4 次，对 429、503、5xx、网络错误重试，遵守 `Retry-After`（503 没有该头时按 1 秒），退避 `500ms × 2^n`、上限 8 秒，加 30% 以内抖动；交互请求最多 3 次且不为长时间限流等待；有同步歌词时若 `plainLyrics` 为空则由同步歌词去时间标签得到 | [`get.rs` L57-L112](https://github.com/tranxuanthang/lrcget/blob/58eba220a4fe0d288c09dd98978bffea25ee5a73/src-tauri/src/lrclib/get.rs)；[`client.rs` L17-L20、L35-L40、L104-L123、L185-L203、L431-L471](https://github.com/tranxuanthang/lrcget/blob/58eba220a4fe0d288c09dd98978bffea25ee5a73/src-tauri/src/lrclib/client.rs) |
| **Navidrome** | 核心不内置 LRCLIB：歌词来源默认 `.ttml,.yaml,.yml,.elrc,.lrc,.srt,.txt,embedded`，在线歌词通过插件能力提供 | — | 能解析 LRCLIB 的 Lyricsfile YAML（含逐字时间）；插件查询结果（含未命中）会缓存，理由是客户端会为队列里每首歌都来问 | [`configuration.go` L1015](https://github.com/navidrome/navidrome/blob/4cd7ab1c60e0e5cf9644c4f5dfad96a10a38120a/conf/configuration.go)；[`model/lyrics_lyricsfile.go`](https://github.com/navidrome/navidrome/blob/4cd7ab1c60e0e5cf9644c4f5dfad96a10a38120a/model/lyrics_lyricsfile.go)；[`core/lyrics/sources.go` L72-L98](https://github.com/navidrome/navidrome/blob/4cd7ab1c60e0e5cf9644c4f5dfad96a10a38120a/core/lyrics/sources.go) |

关于“先 `/api/get` 精确、再 `/api/search` 兜底”：

- 本次读到的三家里**没有一家恰好是这个顺序**：beets 是先搜索后精确；Jellyfin 是两种模式二选一；LRCGET 的自动流程只用 `/api/get`。
  其他播放器或工具里是否有“先 get 后 search”的实现，本次没有读到源码 **[未核实]**。
- 用搜索兜底时，时长只能在客户端过滤（`/api/search` 没有时长参数）。读到的客户端容差只有 beets 的“目标时长的 5%”一例；服务端 `/api/get` 自己是固定的 ±2 秒。
- 搜索上限 20 条且按相关度排，热门歌的同名记录可能超过 20 条（《晴天》两次都顶到上限），正确时长的那条不保证在返回的 20 条里 **[实测 + 推断]**。
- 一次 v1 搜索约 200 KB；v2 搜索只有摘要，可先按 `duration`、`hasSyncedLyrics` 挑出一条再 `GET /api/get/{id}`，但 v2 是 beta **[文档]**。

---

## 本仓库里的相关位置

### Files Found

| File Path | Description |
| --- | --- |
| `backend/internal/steward/web/musicbrainz.go` | 现有的 CAA 清单读取（管家用）：L16 固定基址 `https://coverartarchive.org`；L121-L160 `coverArt` 请求 `/release/{小写mbid}/`，`PublicOnly`、最多 5 次重定向；L133-L139 只放行 `coverartarchive.org`、`archive.org` 及其子域且路径以 `/index.json` 结尾的跳转；L156 把 404 当作无封面 |
| `backend/internal/sources/`（`sources.go`、`limiter.go`、`cache.go`、`request.go`、`musicbrainz/`） | B1 落地的外部源公共层与 MusicBrainz 适配器 |
| `backend/internal/platform/egress/client.go` | 出站客户端；L36-L37、L62-L67 `MaxRedirects`（0 表示不跟随） |
| `backend/internal/catalog/artwork/artwork.go` | 现有封面处理；L296-L308 `SniffMIME` 只认 jpeg、png、gif、webp |

### Related Specs

- `.trellis/spec/backend/sources-contracts.md` — 外部源合同；第 76-77 行：请求用 `PublicOnly`、**不跟随重定向**、`Accept: application/json`、UA 为 `ROOMusic/<版本> ( <项目地址> )`。
  与本调研相关的事实：CAA 的每个端点都必须跟随两次跳转（跨到 archive.org 及其子域）；图片与 CAA 的错误体都不是 JSON。
- `.trellis/spec/backend/steward-web-contracts.md` — 第 167 行写明 CAA 清单读取仍在 `steward/web`、不经过限速器、由 B2 迁移；第 178-179 行是现行清单合同（404 表示无封面；只允许跳到官方 `index.json`；不下载图片）。
- `.trellis/spec/backend/lyrics-contracts.md` — 现有歌词合同（内嵌与外置 `.lrc`、`MaxLyricsBytes = 64 KiB`、来源键、LRC 解析规则）。
- `docs/architecture/adr/0006-auto-curation-and-sources.md` — 第 197-198 行两个源默认开；第 208-209 行“允许官方的重定向目标”“MusicBrainz 与 Cover Art Archive 的清单请求共用每秒 1 次”；第 223-224 行发给两个源的数据范围。

### External References

- [Cover Art Archive / API](https://musicbrainz.org/doc/Cover_Art_Archive/API) — 端点、状态码、清单字段、限速说明
- [Cover Art Archive](https://musicbrainz.org/doc/Cover_Art_Archive) — 使用政策
- [How to Add Cover Art](https://musicbrainz.org/doc/How_to_Add_Cover_Art) — 可上传格式、无大小上限、主封面的确定方式
- [Cover Art / Types](https://musicbrainz.org/doc/Cover_Art/Types) — 18 种图片类型
- [MusicBrainz API / Rate Limiting](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) — MusicBrainz 的每秒 1 次与 User-Agent 要求（未提及 CAA）
- [metabrainz/artwork-redirect](https://github.com/metabrainz/artwork-redirect/tree/c07cada5b7a1325d6859987461712b46144e18a8) — coverartarchive.org 的重定向服务源码
- [metabrainz/artwork-indexer](https://github.com/metabrainz/artwork-indexer/tree/2b1739be5adecf4515849f1e2dc91c3403a76c9a) — 生成 `index.json`、合并时搬移图片
- [LRCLIB API 文档](https://lrclib.net/docs)（[Markdown 版](https://lrclib.net/docs.md)）— 端点、字段、错误、限速、User-Agent
- [tranxuanthang/lrclib](https://github.com/tranxuanthang/lrclib/tree/ffdfed4dd8288f9065f5562508c13705cbe0436c) — LRCLIB 服务端源码（MIT）
- [tranxuanthang/lrcget](https://github.com/tranxuanthang/lrcget/tree/58eba220a4fe0d288c09dd98978bffea25ee5a73) — 官方客户端
- Picard、beets、Navidrome、Jellyfin 两个插件 — 具体文件与行号见第 5、9 节表格

## Caveats / Not Found

- **合并后的 MBID 走 `/front*` 是否会 404**：只有源码推断，没有实测（见第 3 节）。清单路径的合并行为同样只读了源码。
- **CAA 是否与 MusicBrainz 共用按 IP 的每秒 1 次、是否要求 User-Agent**：官方文档没有写；archive.org 一侧的限流规则没有找到公开文档。
- **原图大小与格式的整体分布**：没有统计来源，只有两张图的样本与“上传无大小上限”的文档原话。缩略图遇到小原图是否放大、边长按哪条边算，没有核实。
- **GIF / PDF 原图的缩略图**、**只含 `small`/`large` 键的旧清单**、**字符串类型的 `id`** 现在是否还存在：没有实测。
- archive.org 第三跳的主机名规则不明；本次只见到 `dn*.ca.archive.org`，且受代理出口影响。
- **LRCLIB 歌词数据的许可**没有找到；数据库转储页面没有读。
- LRCLIB 线上部署与开源仓库是否完全一致没有核实；“后台补歌词服务”的细节、未命中在服务端缓存多久（线上）没有核实。
- LRCLIB 的限速数字是文档声明的近似值，本次没有压到触发 429；Go 默认 UA 属于哪一档没有核实。
- 华语部分只查了《晴天》一首热门歌，冷门曲目覆盖率、拉丁写法艺人名下的记录情况都没有查。
- Jellyfin 核心是否内置这两个源没有核实（读的是 jellyfin 组织下的两个独立插件）。“先 get 后 search”的现成实现没有读到。
- `LYRICSFILE_CONCEPT.md` 只从 Navidrome 源码注释里拿到地址，没有读全文。

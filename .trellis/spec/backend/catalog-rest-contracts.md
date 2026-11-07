# Catalog REST 合同

## 1. 范围与触发

本合同适用于发行版本列表、详情、按组只读读取、封面资源读取和曲目歌词读取。新增或修改 Release 摘要字段、可见性、
搜索、筛选、分页、按组投影或封面投影时必须同步阅读并更新本文件。Catalog 是读取 owner；扫描器仍
负责写入 Release Graph 与 `release_artworks`，音乐源保持只读。M3 起读取到的发行值是**有效值**（扫描值叠加管理员的修正，见第 3 节
“有效值投影”）；修正、人工归组、撤销与操作记录的接口见[受治理操作合同](./governance-contracts.md)。M4 起代表版本可以是管理员指定的默认版本，
另有版本对比的只读接口，见[默认版本与版本对比合同](./default-version-contracts.md)。

M5 ④新增规范化搜索与在线投影：完整规则见[搜索合同](./search-contracts.md)。搜索发布后旧两列表有 q 默认按相关性排序，组列表显式加入时间排序仍保留；未发布沿用 ILIKE，无 q 原排序不变。

## 2. 接口与数据签名

- `GET /api/v1/catalog/status`、`GET /api/v1/search/suggestions`、`GET /api/v1/search`：专辑/曲目/艺人三栏，状态、Match、安全曲目摘要和 1000/gte 专用分页见[搜索合同](./search-contracts.md)。
- `GET /api/v1/releases?q=<string>&attention=required&page=<int>&page_size=<int>`
- `GET /api/v1/releases/{id}`
- `GET /api/v1/releases/{id}/evidence`：仅管理员。
- `GET /api/v1/release-groups?q=<string>&attention=required&page=<int>&page_size=<int>&sort=title|added_at_desc`
- `GET /api/v1/release-groups/recently-added`：所有登录成员，固定最近八张，无查询参数，返回 `{items:[组摘要]}`。
- `GET /api/v1/release-groups/{id}`
- `GET /api/v1/release-groups/{id}/comparison`：版本对比（M4，所有登录成员），响应与规则见[默认版本与版本对比合同](./default-version-contracts.md)第 6 节。
- `GET /api/v1/artworks/{resource_id}`：认证后读取受管封面；响应带 `ETag`，支持 `If-None-Match` 的 `304`（见第 3 节）。
- `GET /api/v1/tracks/{id}/lyrics`：认证后读取曲目的内嵌歌词（所有登录成员；非 UUID 400、不存在或非 `present` 404；没有歌词是 200 加 `lyrics: null`）。
  它与发行接口同属 catalog 的只读投影，提取、保存、解析规则与响应形状见[内嵌歌词合同](./lyrics-contracts.md)。
- 历史、收藏与歌单消费的批量安全摘要、上限、缺失语义和同快照约定见[目录批量摘要合同](./catalog-summary-contracts.md)。
- 管理员写接口（M3，合同见[受治理操作合同](./governance-contracts.md)）：`GET|PATCH /api/v1/releases/{id}/corrections`、
  `POST /api/v1/releases/{id}/group`、`GET /api/v1/operations`、`POST /api/v1/operations/{id}/revert`；M4 增加
  `POST /api/v1/release-groups/{id}/default`（设定或清除默认版本，见[默认版本合同](./default-version-contracts.md)）。
- Release 摘要包含 `id`、标题/艺人、nullable 年份/来源/介质、Medium/Track/attention
  计数，以及 `artwork: null | {resource_id,mime,width,height}`。
- `artwork` 是在既有 Release 摘要上追加的向后兼容（additive）字段；真实音频播放仍
  明确延期，不属于当前 Catalog REST 合同。
- ReleaseGroup 摘要：`{id, release_count, attention_count, added_at, representative}`，`representative`
  是代表版本的完整 Release 摘要（含封面）。组详情在摘要上加 `releases`（成员 Release 摘要数组）、`revision`（专辑的人工修改版本号，M4）
  与 `default_release_id`（生效中的默认版本，M4；失效或没有设定时为 `null`，非空时等于 `releases[0].id`）；组摘要（列表）没有这两个字段。
  两个接口都只读；人工归组由治理合同的 `POST /releases/{id}/group` 提供，默认版本由 `POST /release-groups/{id}/default` 提供。
- Release 详情在摘要之外加 `revision`（发行的人工修改版本号，写操作用它做预期版本）与 `corrected_fields`（有人工修正的字段名，固定顺序，
  没有时是空数组）；所有登录成员可见，不含修正值的来源与操作者。
- Release 详情与版本对比的 `genres` 为有序文本数组，无流派为 `[]`；不再返回单值 `genre`。修正字段键仍为 `genre`，见[多流派合同](./genre-contracts.md)。
- Release 详情的 `edition`、`label`、`barcode` 为 nullable string；每条 Track 的
  `credits` 项为 `{role,name,artist_refs}`，`role` 只允许
  `composer | conductor | performer | producer`。每轨最多 100 条、单个 Release 最多
  10,000 条 Track credits，超限后端 fail closed，前端 decoder 同样拒绝。

## 3. 请求与响应合同

- 列表只返回至少含一个 `present` Track 的 Release；默认 `page=1`、`page_size=50`，
  `page_size` 最大 100；搜索词最多 200 字节，`attention` 只允许 `required`。
- 列表与详情复用同一 Release 摘要投影。封面按 `release_id` 主键一次 `LEFT JOIN`，
  禁止在列表 handler 中逐 Release 查询详情或封面。
- 没有封面关系时必须显式返回 `"artwork": null`，不能省略字段或隐藏 Release。
- 有封面时只返回最长 255 字节的安全 basename `resource_id`、白名单 MIME 和 PostgreSQL
  正整数宽高；不返回图片字节、`content_hash`、`source_type`、受管目录或音乐源路径。
- MIME 只允许 `image/jpeg | image/png | image/gif | image/webp`。资源字节仍通过认证的
  artwork endpoint 返回。

### 发行列表、详情与 evidence 权限

- 发行列表只返回至少含一个 `present` Track 的 Release，支持严格 allowlist 的
  `attention=required`。详情返回有界 Medium/Track、credits、音频/CUE facts 和
  普通用户安全 evidence 摘要；完整候选、reason code 和安全相对来源只由管理员
  evidence 端点返回，普通用户必须得到 403。
- 恢复 catalog 时，摘要 `artwork` 仍需严格解码（白名单 MIME、正整数宽高、有界
  basename resource ID），封面只按 `/api/v1/artworks/{resource_id}` 同源鉴权读取。
  选中详情和筛选的 URL 责任见 [状态规范](../frontend/state-management.md)，具体新路由
  形式尚待批准。M1 不恢复演示队列或模拟计时器；未实现的播放动作隐藏或明确禁用，
  不以“演示模式”标记替代真实能力边界。

### 封面响应与缓存（F3）

- 200 响应：`Content-Type` 为记录的 MIME，`Cache-Control: private, no-cache`，`ETag: "<resource_id>"`。资源 ID 是内容
  SHA-256 加扩展名，同一个 ID 的字节永不改变，所以它本身就是强校验值。
- 处理顺序固定：未登录 `401` → `artwork.Lookup` 只查记录并校验存储键（不存在或不安全 `404`）→ `If-None-Match` 命中则 `304`，
  同一个 `ETag`、没有响应体、**不读文件** → `artwork.Read` 读文件，返回 `200`。所以未登录时即使 `If-None-Match` 匹配也不是 304，
  不存在的资源即使带着看似匹配的标签也是 404；删除文件之后，带匹配标签的请求仍是 304，没有标签的请求才发现文件不见了（`404`）。
- `If-None-Match` 按 RFC 9110 的弱比较：忽略 `W/` 前缀，逐个比较逗号分隔的标签，`*` 命中任何存在的资源，无法解析的值视为不命中并按
  正常的 `200` 处理。标签不匹配时返回完整响应。
- 存储键只接受字母、数字、点、下划线与连字符，非空、不超过 255 字节、不是 `.` 或 `..`（`artwork.safeStorageKey`）：既不会逃出数据目录，
  也不会破坏作为 `ETag` 写进响应头的引号语法。不满足的记录按“不安全”处理，返回 `404` 而不回显路径。
- 选择 `private, no-cache`：每次显示前带凭据重新验证，退出登录后验证得到 `401`，旧响应不再被使用；未变时代价只是一次查库加一个 304。
  不用 `no-store`（每次整张重新下载），也不用 `max-age`（退出后一段时间内仍可从浏览器缓存直接读到）。依据与局限见
  [运行合同](./core0-runtime-contracts.md)的“HTTP 缓存”。

### 有效值投影（M3）

- 所有读取（列表、详情、搜索、排序、分页、按组读取、代表版本规则、最近播放的曲目位置）的发行来源是视图 `release_effective`
  （常量 `releasesql.ReleaseSource`，别名仍叫 `releases`）：标题、专辑艺人、年份、流派、厂牌、编号、版本说明、条码在有修正时取修正值（设空为空），
  展示艺人 `artist` 跟随专辑艺人的修正。扫描与身份规划读写 `releases` 本表。**没有修正时与扫描值逐项一致。**
- `attention_count` 不计已有修正的字段（`artist` 由专辑艺人的修正覆盖），分组原因与缺失曲目照旧；详情的专辑级署名里，专辑艺人有修正时用修正值。
- M4 第二项起 `attention_count` 是三类**待处理**问题之和（拿不准的字段、同一目录的归组冲突、缺失曲目），被仍然有效的核对压下的不计；
  没有核对时与上面的算法逐项相同。算法、依据与核对见[元数据问题与核对合同](./issue-review-contracts.md)。
  完整规则与视图定义见[受治理操作合同](./governance-contracts.md)第 5 节。
- 不可见的旧版本（没有 present 曲目）仍是列表、详情与按组读取的 `404`；M4 第三项起管理员可经 `GET /api/v1/release-claims` 看到其中留有人工修改的，并把修改认领给可见版本（[认领合同](./claim-contracts.md)）。

### 按组读取（阶段 I，只读）

- **共同参数与错误码**与 `/releases` 相同，组另有独立的 `sort` 白名单：`page`、`page_size`（默认 1/50，最大 100）、`q`（最多 200 字节）、
  `attention=required`，错误码 `invalid_pagination`、`invalid_query`、`invalid_attention`；详情的非 UUID 为
  `400 invalid_id`。响应是 `{items:[组摘要], pagination:{page,page_size,total}}`，`total` 是满足条件的组数。
- **可见性**：成员 Release 的可见条件与 `/releases` 相同（至少一条 `present` 曲目）。组至少有一个可见成员才出现；
  `release_count` 只数可见成员；只有不可见成员的组、不存在的组，详情返回 `404 not_found`。
- **代表版本**：管理员指定的默认版本（M4）排在最前，但只在它是这个组的可见成员时生效，否则静默失效、回到自动规则；其余是可见成员中年份最早者
  （没有年份排最后），其次创建最早，最后 ID 最小。规则是常量 `releasesql.ReleaseGroupMemberOrder`，生效与失效的细节见[默认版本合同](./default-version-contracts.md)第 3 节。
  组详情的 `releases` 与版本对比按同一规则排序，最多 100 条，`release_count` 仍是完整数量；`representative` 与 `releases[0]` 相同。
  **没有设定默认版本时，代表版本、顺序与接口样本和 M3 逐项一致。**
- **搜索**：任一可见成员命中 `/releases` 的搜索条件（同一个 SQL 谓词），组即命中；只命中不可见成员的不算。
- **attention**：`attention_count` 是全部可见成员 attention 之和（不可见成员不计）；`attention=required` 取其大于 0 的组，
  与代表版本自己的 `attention_count` 无关。
- **无 q 的排序（以及新搜索未发布时的旧 q 回退）**：省略或 `sort=title` 取代表版本的 `LOWER(title)`、`LOWER(artist)`、ID，与 `/releases` 一致；
  `sort=added_at_desc` 取 `added_at DESC, group_id DESC`。空值、未知值、重复 `sort` 参数均为 `400 invalid_sort`。
  `/releases` 不增加此排序能力。新搜索已发布且带 q 时，默认/标题入口按搜索相关性排序；显式 `sort=added_at_desc` 保留加入时间倒序，匹配仍使用完整规范化搜索。
  标题浏览下没有人工归组时每组恰好一个版本，
  此时 `/release-groups` 与 `/releases` 的总数、顺序与代表版本逐项对应，`release_count` 为 1，
  `attention_count` 等于该版本的 `attention_count`。
- 摘要投影、封面 `LEFT JOIN` 与搜索谓词复用 `/releases` 的同一批 SQL 常量，不逐组查询。

### 最近新增与扫描新增项（M5 扫描反馈）

- `added_at` 在组摘要、组详情、最近新增与报告当前卡片中均为 RFC 3339 时间，取**当前组内所有可见发行**的最早 `created_at`，
  不是组创建时间或代表版本创建时间；先计算窗口 `MIN`，再筛选搜索，命中较新成员不会改写组加入时间。
- 可见性继续复用 `ReleaseVisiblePredicate`，不额外按 root 是否禁用过滤；默认版本仍只控制代表版本。
  归组、认领、撤销或最早成员不可见可改变加入时间；恢复旧成员采用原创建时间，不当作新加入。
- 最近新增固定 `added_at DESC, group_id DESC LIMIT 8`，不执行 COUNT；空列表必须是 `[]`，不可见组不返回。
  任何查询参数（包括 sort/limit/page/page_size/q/attention）均为 `400 invalid_query`；认证先于参数检查。
  读取失败为 `503 database_unavailable`，不能假装成空列表。索引 `releases(group_id, created_at, id)` 在迁移 0024 建立，
  不能覆盖跨表可见性与全组时间聚合；性能需以实际 EXPLAIN 为准。
- `catalog.NewStore(queryer).ListScanAddedAlbums(ctx, scanID, page, pageSize)` 是 library 管理员报告的公开读取入口。
  返回 `ScanAddedAlbumPage{Items,Total}`；页大小 1–100，页码正数，检查 offset 溢出。
  历史来源是 `scan_added_releases`，按 `added_at DESC, release_id DESC` 分页，total 按历史关联计数，不因当前状态过滤或归组去重。
  `group_id_at_creation/title_at_creation/artist_at_creation` 映射为 DTO 的 `group_id_at_scan/title_at_scan/artist_at_scan`。
  每项同时返回冻结的 `added_at/release_id`；当前发行可见时批量补 `current_group_id` 和 `album`（共用组摘要投影），
  当前发行不可见或已不存在时这两项均为 null，即使它所属组仍有其他可见发行，也不提供误导的播放卡片。
- `governance.NewStore(queryer).LoadClaimHintsBatch(ctx, releaseIDs)` 批量补管理员专属的 `claim_hints: [{id,title}]`。
  最多 100 个输入 ID，非法 UUID 报错，小写去重，空输入不查库；不存在、不可见或无提示的目标返回非 nil 空切片。
  同一条集合 SQL 按目标分区、每个目标最多三条；复用待认领谓词、旧路径集合和最后观察时间排序，路径命中或同目录任一成立即可，
  不受正向推荐五条上限限制。认领后提示消失，撤销后按当前条件恢复；读取故障传回调用方安全 503，不冒充没有提示。
- library 在鉴权之后开启**短、只读 REPEATABLE READ**，报告表、上述两种 Store 共享同一个 Queryer，最后统一提交；
  这些读取入口不另开事务，不读取文件。library 负责扫描状态/报告可用性与最终 DTO 的分页及提示组合，catalog 不读扫描私有状态。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 未认证 | `401 unauthorized` |
| 分页、搜索或 attention 非法 | 对应稳定 `400` 错误码 |
| Release 不存在或仅含 `missing` Track | 详情返回 `404 not_found` |
| ReleaseGroup 不存在，或没有任何可见成员 | 组详情与版本对比返回 `404 not_found` |
| 组详情或版本对比的 ID 不是 UUID | `400 invalid_id` |
| 版本对比的读取失败 | `503 database_unavailable` |
| 组列表 sort 空值、未知或重复 | `400 invalid_sort` |
| 最近新增带任意查询参数 | `400 invalid_query` |
| 最近新增查询或投影失败 | `503 database_unavailable` |
| 组列表的读取失败 | `503 database_unavailable`（查询无法执行）或 `503 database_error`，不回显 SQL |
| 无 `release_artworks` 关系 | 列表与详情返回显式 `artwork: null` |
| 封面四列完整且合法 | 列表与详情返回相同 artwork 对象 |
| 封面列部分为空，ID 超过 255 字节或含路径分隔符/点目录/控制字符，MIME 或尺寸非法 | fail closed 为既有 `503` 安全错误 envelope；不得回显存储值、SQL 或路径 |
| artwork 资源不存在、记录不安全或文件不可读 | `404 not_found`，不返回主机路径；带 `If-None-Match` 也不是 304（数据库读取错误同样归为 404，既有合同） |
| artwork 未认证 | `401`；即使 `If-None-Match` 匹配也不是 304 |
| artwork 的 `If-None-Match` 命中当前资源 | `304`，同一个 `ETag`，没有响应体，不读文件 |
| artwork 的 `If-None-Match` 不匹配或无法解析 | `200` 完整响应，带当前 `ETag` |

## 5–7. 案例、必需测试与示例

正确/基准/错误案例（第 5 节）、必需测试与断言点（第 6 节）、错误与正确示例（第 7 节）见[Catalog REST 验证与示例](./catalog-rest-verification.md)，节号不变。

## 8. M5 艺人增量

艺人列表/详情/名字/专辑/角色曲目/队列、原署名 refs 与整理期状态见[艺人合同](./artist-contracts.md)。`ArtistRef={artist_id,name,display_name}`；ReleaseSummary 增加 `artist_refs/album_artist_refs`，TrackSummary/ReleaseTrack/ReleaseCredit 增加 `artist_refs`，数组必传、每项最多100；未就绪为 `[]`，旧文本与播放能力不变。

I1/I2/I4/I6已补：管理员merged-sources、albums participation=any/q、成员POST tracks/summaries、album_artist筛选并集；外形与错误见[艺人合同](./artist-contracts.md#8-实施裁决-i1--i4管理员恢复来源与成员批量摘要)。

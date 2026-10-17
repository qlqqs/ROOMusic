# Catalog REST 合同

## 1. 范围与触发

本合同适用于发行版本列表、详情、按组只读读取、封面资源读取和曲目歌词读取。新增或修改 Release 摘要字段、可见性、
搜索、筛选、分页、按组投影或封面投影时必须同步阅读并更新本文件。Catalog 是读取 owner；扫描器仍
负责写入 Release Graph 与 `release_artworks`，音乐源保持只读。M3 起读取到的发行值是**有效值**（扫描值叠加管理员的修正，见第 3 节
“有效值投影”）；修正、人工归组、撤销与操作记录的接口见[受治理操作合同](./governance-contracts.md)。M4 起代表版本可以是管理员指定的默认版本，
另有版本对比的只读接口，见[默认版本与版本对比合同](./default-version-contracts.md)。

## 2. 接口与数据签名

- `GET /api/v1/releases?q=<string>&attention=required&page=<int>&page_size=<int>`
- `GET /api/v1/releases/{id}`
- `GET /api/v1/releases/{id}/evidence`：仅管理员。
- `GET /api/v1/release-groups?q=<string>&attention=required&page=<int>&page_size=<int>`
- `GET /api/v1/release-groups/{id}`
- `GET /api/v1/release-groups/{id}/comparison`：版本对比（M4，所有登录成员），响应与规则见[默认版本与版本对比合同](./default-version-contracts.md)第 6 节。
- `GET /api/v1/artworks/{resource_id}`：认证后读取受管封面；响应带 `ETag`，支持 `If-None-Match` 的 `304`（见第 3 节）。
- `GET /api/v1/tracks/{id}/lyrics`：认证后读取曲目的内嵌歌词（所有登录成员；非 UUID 400、不存在或非 `present` 404；没有歌词是 200 加 `lyrics: null`）。
  它与发行接口同属 catalog 的只读投影，提取、保存、解析规则与响应形状见[内嵌歌词合同](./lyrics-contracts.md)。
- 播放历史（阶段 P）只经 catalog 的两个公开读取函数使用目录：`Store.LoadTrackDuration`（present 曲目的时长，未知为 0）与
  `Store.LoadTrackPlacements`（present 曲目的标题、组与所在发行摘要，摘要与列表同一个投影），不读 catalog 的私有表；规则见[播放历史合同](./listening-contracts.md)。
- 管理员写接口（M3，合同见[受治理操作合同](./governance-contracts.md)）：`GET|PATCH /api/v1/releases/{id}/corrections`、
  `POST /api/v1/releases/{id}/group`、`GET /api/v1/operations`、`POST /api/v1/operations/{id}/revert`；M4 增加
  `POST /api/v1/release-groups/{id}/default`（设定或清除默认版本，见[默认版本合同](./default-version-contracts.md)）。
- Release 摘要包含 `id`、标题/艺人、nullable 年份/来源/介质、Medium/Track/attention
  计数，以及 `artwork: null | {resource_id,mime,width,height}`。
- `artwork` 是在既有 Release 摘要上追加的向后兼容（additive）字段；真实音频播放仍
  明确延期，不属于当前 Catalog REST 合同。
- ReleaseGroup 摘要：`{id, release_count, attention_count, representative}`，`representative`
  是代表版本的完整 Release 摘要（含封面）。组详情在摘要上加 `releases`（成员 Release 摘要数组）、`revision`（专辑的人工修改版本号，M4）
  与 `default_release_id`（生效中的默认版本，M4；失效或没有设定时为 `null`，非空时等于 `releases[0].id`）；组摘要（列表）没有这两个字段。
  两个接口都只读；人工归组由治理合同的 `POST /releases/{id}/group` 提供，默认版本由 `POST /release-groups/{id}/default` 提供。
- Release 详情在摘要之外加 `revision`（发行的人工修改版本号，写操作用它做预期版本）与 `corrected_fields`（有人工修正的字段名，固定顺序，
  没有时是空数组）；所有登录成员可见，不含修正值的来源与操作者。
- Release 详情的 `edition`、`label`、`barcode` 为 nullable string；每条 Track 的
  `credits` 项为 `{role,name}`，`role` 只允许
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
  （常量 `releaseSource`，别名仍叫 `releases`）：标题、专辑艺人、年份、流派、厂牌、编号、版本说明、条码在有修正时取修正值（设空为空），
  展示艺人 `artist` 跟随专辑艺人的修正。扫描与身份规划读写 `releases` 本表。**没有修正时与扫描值逐项一致。**
- `attention_count` 不计已有修正的字段（`artist` 由专辑艺人的修正覆盖），分组原因与缺失曲目照旧；详情的专辑级署名里，专辑艺人有修正时用修正值。
- M4 第二项起 `attention_count` 是三类**待处理**问题之和（拿不准的字段、同一目录的归组冲突、缺失曲目），被仍然有效的核对压下的不计；
  没有核对时与上面的算法逐项相同。算法、依据与核对见[元数据问题与核对合同](./issue-review-contracts.md)。
  完整规则与视图定义见[受治理操作合同](./governance-contracts.md)第 5 节。

### 按组读取（阶段 I，只读）

- **参数与错误码**与 `/releases` 完全相同：`page`、`page_size`（默认 1/50，最大 100）、`q`（最多 200 字节）、
  `attention=required`，错误码 `invalid_pagination`、`invalid_query`、`invalid_attention`；详情的非 UUID 为
  `400 invalid_id`。响应是 `{items:[组摘要], pagination:{page,page_size,total}}`，`total` 是满足条件的组数。
- **可见性**：成员 Release 的可见条件与 `/releases` 相同（至少一条 `present` 曲目）。组至少有一个可见成员才出现；
  `release_count` 只数可见成员；只有不可见成员的组、不存在的组，详情返回 `404 not_found`。
- **代表版本**：管理员指定的默认版本（M4）排在最前，但只在它是这个组的可见成员时生效，否则静默失效、回到自动规则；其余是可见成员中年份最早者
  （没有年份排最后），其次创建最早，最后 ID 最小。规则是常量 `releaseGroupMemberOrder`，生效与失效的细节见[默认版本合同](./default-version-contracts.md)第 3 节。
  组详情的 `releases` 与版本对比按同一规则排序，最多 100 条，`release_count` 仍是完整数量；`representative` 与 `releases[0]` 相同。
  **没有设定默认版本时，代表版本、顺序与接口样本和 M3 逐项一致。**
- **搜索**：任一可见成员命中 `/releases` 的搜索条件（同一个 SQL 谓词），组即命中；只命中不可见成员的不算。
- **attention**：`attention_count` 是全部可见成员 attention 之和（不可见成员不计）；`attention=required` 取其大于 0 的组，
  与代表版本自己的 `attention_count` 无关。
- **排序**：代表版本的 `LOWER(title)`、`LOWER(artist)`、ID，与 `/releases` 一致。没有人工归组时每组恰好一个版本，
  此时 `/release-groups` 与 `/releases` 的总数、顺序与代表版本逐项对应，`release_count` 为 1，
  `attention_count` 等于该版本的 `attention_count`。
- 摘要投影、封面 `LEFT JOIN` 与搜索谓词复用 `/releases` 的同一批 SQL 常量，不逐组查询。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 未认证 | `401 unauthorized` |
| 分页、搜索或 attention 非法 | 对应稳定 `400` 错误码 |
| Release 不存在或仅含 `missing` Track | 详情返回 `404 not_found` |
| ReleaseGroup 不存在，或没有任何可见成员 | 组详情与版本对比返回 `404 not_found` |
| 组详情或版本对比的 ID 不是 UUID | `400 invalid_id` |
| 版本对比的读取失败 | `503 database_unavailable` |
| 组列表的读取失败 | `503 database_unavailable`（查询无法执行）或 `503 database_error`，不回显 SQL |
| 无 `release_artworks` 关系 | 列表与详情返回显式 `artwork: null` |
| 封面四列完整且合法 | 列表与详情返回相同 artwork 对象 |
| 封面列部分为空，ID 超过 255 字节或含路径分隔符/点目录/控制字符，MIME 或尺寸非法 | fail closed 为既有 `503` 安全错误 envelope；不得回显存储值、SQL 或路径 |
| artwork 资源不存在、记录不安全或文件不可读 | `404 not_found`，不返回主机路径；带 `If-None-Match` 也不是 304（数据库读取错误同样归为 404，既有合同） |
| artwork 未认证 | `401`；即使 `If-None-Match` 匹配也不是 304 |
| artwork 的 `If-None-Match` 命中当前资源 | `304`，同一个 `ETag`，没有响应体，不读文件 |
| artwork 的 `If-None-Match` 不匹配或无法解析 | `200` 完整响应，带当前 `ETag` |

## 5. 正确、基准与错误案例

- 正确：一页 50 个 Release 由一次列表查询获得封面摘要；有封面对象与详情一致。
- 基准：无封面的 Release 仍参与相同搜索、排序、分页和 attention 结果，并返回 `null`。
- 错误：为了显示 50 张封面再发 50 个详情请求，或把 `storage_key` 当主机路径返回。
- 错误：部分空或非法封面元数据仍返回 2xx，迫使客户端猜测数据是否可信。

## 6. 必需测试与断言点

- 单元测试：四列全空、部分空、四种 MIME、空/超过 255 字节/点目录/分隔符/控制字符/
  非法 UTF-8 ID、零/负/越界尺寸，以及摘要 JSON 的显式 `null`。
- PostgreSQL REST 集成：有封面、无封面、列表/详情一致、present-only、搜索/分页、
  attention、未认证、隐藏 Release、非法资源 ID 的安全 503 和无路径/SQL 泄露。
- 按组读取（`internal/app/release_groups_api_integration_test.go`）：每组一个版本时与 `/releases` 的总数、顺序、
  代表版本逐项对应（含搜索、attention、分页、超出范围的页、present-only）；直接插入多成员组，覆盖代表版本规则
  （年份、无年份排后、创建时间、ID）、`release_count` 与 `attention_count`（不可见成员不计）、搜索命中非代表成员、
  attention 只来自非代表成员、成员数超过 100 的截断；401、400、404、非法参数。
- 默认版本与版本对比（M4）：代表版本规则里的默认项、生效与失效、组详情的两个字段与对比接口的测试见[默认版本合同](./default-version-contracts.md)第 9 节；
  既有按组读取测试与样本不改即通过（只多出组详情的 `revision` 与 `default_release_id`）。
- 有效值（M3，`internal/catalog/effective_integration_test.go`）：无修正时视图逐列等于表；逐字段叠加与清除；读取（列表、详情、搜索、排序、按组）返回有效值；
  署名跟随专辑艺人的修正；“需要关注”不计已修正的字段。
- 封面缓存（F3，`internal/app/artwork_cache_integration_test.go`）：200 带 `ETag` 与 `private, no-cache`；强标签、弱标签、列表与
  `*` 都得到 304 且没有响应体；删除文件后带匹配标签仍是 304；标签不匹配返回新字节；未认证 401、未知资源 404（都不是 304）；
  不安全的存储键返回 404。`catalog/artwork` 的单元测试覆盖 `safeStorageKey` 与 `Read`，`platform/httpserver` 的 `etag_test.go` 覆盖弱比较。
- 运行 `go test ./internal/catalog/... ./internal/app -run 'Release|Catalog|Artwork|Group|Routes|Default|Comparison' -count=1`（M4 的默认版本与对比用例名不一定含 `Group`）；涉及 SQL 投影时，
  还需通过隔离 PostgreSQL 18 的 `./scripts/test-integration.sh`。
- 发行 REST/前端：present-only 列表、稳定分页/搜索、attention allowlist、详情事实、
  管理员 evidence 权限、路径脱敏、nullable/enum/数组上限和 malformed payload；按组接口与 `/releases` 逐项对应。

## 7. 错误与正确示例

错误：

```go
for _, release := range releases {
	release.Artwork, _ = loadReleaseArtwork(ctx, release.ID)
}
```

正确：列表与详情把同一投影和关联一起传给 `scanReleaseSummary`，由一个函数解释 nullable
列并校验安全边界：

```go
query := "SELECT " + releaseSummaryProjection + " FROM releases" + releaseSummaryArtworkJoin
summary, err := scanReleaseSummary(db.QueryRowContext(ctx, query+" WHERE releases.id=$1::uuid", id))
```

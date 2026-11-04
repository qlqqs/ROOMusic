# M5 ② 收藏与 ③ 歌单：技术设计

## 0. 状态与依据

2026-11-04，后端规划交付。依据本目录定稿 `prd.md`、`../11-04-classical-m5-playlists/prd.md`、父任务 PRD，以及 `../11-04-classical-m5-search/design.md` §11 的艺人收藏合同。本设计覆盖②与③的实现边界，⑤仅定义接入合同，不提前建立艺人实体或实施艺人功能。

本轮按 `trellis-brainstorm` 做代码证据核对，**只写本文件**。不修改 PRD、代码、迁移、spec、任务状态或 git 提交；不启动数据库，不连接或操作用户的 `roomusic-postgres-1` 容器及 `roomusic` 实例。保持 planning，不运行 `task.py start`。执行计划与上下文清单由主会话另行补齐。

### 0.1 已核实的依据

| 依据 | 对本设计的约束 |
| --- | --- |
| `/home/qlqq/workspace/AGENTS.md`、仓库 `AGENTS.md` | 简体中文文档、最小相关验证、真实服务隔离 |
| 历史任务 `../archive/2026-11/11-04-classical-m5-history/design.md`、backend `listening-contracts.md` | 本人数据、缺失保留、曲目当前位置、安全摘要、会话代次；收藏按本次裁决先过滤missing，再取前10,000首，不照搬历史的1,000候选窗口 |
| backend `catalog-rest-contracts.md`、`database-guidelines.md`、`error-handling.md`、`directory-structure.md` | catalog 公开读取、强外键、显式事务、稳定错误码与依赖白名单 |
| frontend `state-management.md`、`directory-structure.md`、`player-design-guidelines.md` | Query 唯一服务器缓存、app 插槽组合、不反向依赖 player；追加到现有队列仍整批拒绝，播放全部先限量再替换 |
| ADR `docs/architecture/adr/0003-governed-operations.md` §“不同操作流不得混用”及个人数据验收项 | 个人收藏/歌单不接 `operations.Run`；仍需归属、资源、输入、并发及重试校验 |
| `backend/internal/catalog/track_summary.go` | `LoadTrackSummaries` 最多 1,000 个不同有效 ID，包含 missing；每发行物化一次安全摘要 |
| `backend/internal/catalog/read.go`、`groups.go` | `catalog.NewStore` 接受 `Queryer`，可用同一 `*sql.Tx`；专辑详情是可见版本的投影，单版曲目最多 10,000 |
| `backend/internal/platform/httpserver/response.go`、frontend `shared/api/envelope.ts` | **实际错误信封是 `{error:{code,message},request_id}`，request_id 在顶层**；不复制历史设计中的简写 |
| frontend `shared/api/request.ts` | 当前方法词表没有 PUT；实施时须做 additive 扩展，不能绕过共享请求层 |
| `backend/internal/app/dependency_rules_test.go`、`app.go` | 新包必须登记精确依赖；app 负责装配与注册 |

2026-11-04 主会话已按“用户用起来方便、人性化”裁决：稳定但不可用的身份仍可收藏；撤销按当前状态恢复而非因后续修改冲突；播放全部过滤missing后取前10,000首并报告省略数；整张加入取当前所选版本，收据保留至用户删除，卡片性能实施时实测。主会话随后补充裁决：撤销可临时超过10,000首，超限期间禁止新增，不预留容量名额。本文据此修订；与先前PRD/设计中的“播放全部超限全拒绝”“修改后撤销冲突”措辞不一致时，以本次明确裁决为准。本轮只改本文件，不代改PRD或宣布实施启动。

## 1. 模块归属与依赖

### 1.1 选择：新建 `personal`，不扩大 `listening`

- 后端新建 `backend/internal/personal/` 与 `personal/httpapi/`。前者拥有收藏、歌单、排序、本人撤销和幂等结果；后者只解析/守卫/呈现，不写 SQL。
- `listening` 保持播放记录、计次和历史语义，旧路由、旧表、原有队列候选上限均不变。收藏不是一次播放，歌单也不是历史记录，不能为复用“我的音乐”导航而混入 listening。
- ②③在同一实现线，共用本人鉴权约定、曲目读取端口、错误映射及前端动作，使用一个聚焦的 personal 包即可；暂不额外建 personal 下的 domain/application/repository 层，也不抽出通用个人数据平台。
- 建议文件：`service.go`、`favorites.go`、`favorite_reads.go`、`favorite_queue.go`、`playlists.go`、`playlist_reads.go`、`playlist_items.go`、`playlist_order.go`、`playlist_retry.go`、`playlist_undo.go`、`errors.go`、`projection.go`；HTTP 按收藏/歌单分文件。只在实际需要时建立，不预建空文件。
- 所有 catalog SQL 留在 catalog，包括曲目存在性、当前发行/组、有效值、封面安全投影。personal SQL **不 JOIN tracks/media/releases/release_groups 或艺人私表，不 import catalog/internal/**；跨模块 FK 只作完整性约束，不是联表授权。
- personal 声明消费端窄接口，默认构造时包装 `catalog.NewStore(database)`；需要一致快照时在用例事务中使用 `catalog.NewStore(tx)`。测试可替换端口，app 不向 handler 暴露私表。

### 1.2 `dependency_rules_test.go` 的实施变更

| 包 | 允许的内部依赖 | net/http |
| --- | --- | --- |
| `personal`（新增） | `catalog`, `platform/uuid` | 否 |
| `personal/httpapi`（新增） | `identity`, `personal`, `platform/httpserver`, `platform/uuid` | 是 |
| `app`（现有） | 保留原白名单，追加 `personal`, `personal/httpapi` | 原值不变 |

catalog 核心仍只用其公开允许的内部包；不反向导入 personal。personal 不依赖 operations、library、playback、listening 或其 HTTP 包。⑤的事务内解析/投影刷新通过 catalog 公开入口和 app 注入端口实现，见 §8；不能为接入艺人收藏开放 internal/writegate 依赖。

## 2. 数据模型与生命周期

### 2.1 通用规则

- UUID 存 PostgreSQL UUID，输入校验后转小写；时间用 `timestamptz`，服务端赋值、UTC RFC3339 输出，不接受客户端收藏时间/修改时间。
- 普通个人数据查询一律带会话 `user_id`，管理员没有个人数据越权分支；唯一跨用户系统派生刷新例外严格限于 §8 的窄事务端口。账号停用只影响登录，保留数据；未来物理删除用户时级联清除个人数据。
- 曲目、专辑组、艺人不会物理删除；它们的个人引用使用 `ON DELETE RESTRICT`。重扫只改变可用性/当前位置，不删除收藏或歌单条目、不自动换成另一版本曲目。
- 修订号为 BIGINT，wire 使用正的 JS 安全整数；收藏集合初值可为 0，歌单初值为 1。数据库约束上界 `9007199254740991`；递增必须检查，不能溢出后回绕。排序键不对外暴露。

### 2.2 ② 的三张表

选择**各类收藏独立 typed 表**，不采用无外键的 `target_kind/target_id` 多态表。曲目、组、艺人有不同可见性/合并语义，独立表能由数据库验证真实引用；通用按钮的目标 union 是 API/前端抽象，不要求数据库多态。

| 表/列 | 类型与约束 |
| --- | --- |
| `personal_favorite_state.user_id` | UUID 主键，FK users `ON DELETE CASCADE` |
| `personal_favorite_state.revision` | BIGINT NOT NULL，默认 0，非负安全整数 |
| `favorite_tracks.user_id` | UUID NOT NULL，FK users `ON DELETE CASCADE` |
| `favorite_tracks.track_id` | UUID NOT NULL，FK tracks `ON DELETE RESTRICT` |
| `favorite_tracks.created_at` | TIMESTAMPTZ NOT NULL；首次真正插入时赋值 |
| `favorite_tracks` 主键 | `(user_id, track_id)` |
| `favorite_albums.user_id` | UUID NOT NULL，FK users `ON DELETE CASCADE` |
| `favorite_albums.group_id` | UUID NOT NULL，FK release_groups `ON DELETE RESTRICT` |
| `favorite_albums.created_at` | TIMESTAMPTZ NOT NULL |
| `favorite_albums.saved_title`, `saved_artist` | TEXT NOT NULL；首次收藏时记录的安全显示回退值，不是 catalog 权威；允许空字符串 |
| `favorite_albums` 主键 | `(user_id, group_id)` |

索引：`favorite_tracks_user_created_idx (user_id, created_at DESC, track_id ASC)`；`favorite_albums_user_created_idx (user_id, created_at DESC, group_id ASC)`。主键负责去重/心形批量状态，时间索引负责分页与队列构建时的内部keyset遍历。引用端按 `track_id`、`group_id` 单列建 FK 检查索引，避免未来显式清理目录身份时扫描全部用户；不建标题搜索或收藏数统计索引。

`personal_favorite_state` 不是收藏计数表：它给收藏分页与响应提供本人集合的一致性标记，并防止旧状态响应覆盖新心形；播放全部改为一个只读快照内构建，不再依赖跨请求游标。首次写时插入 state（冲突忽略）再锁本人 state 行；真正增加/删除一条收藏才将 revision 加一。同一事务修改收藏与 state。重复 PUT 不改时间/排序、不递增；重复 DELETE 无行时也不递增。所有收藏类型共用此版本，专辑变化使曲目列表分页版本重读是保守且可接受的代价。普通读没有 state 时视为 revision=0，不为 GET 创建记录。

专辑可见时显示当前代表版本与有效值；不可见时保留已存标题/艺人、灰态和占位封面，不借收藏接口开放不可见版本详情。组变空后收藏仍指向原 group，不跟着某个发行自动转到新组。当前组无安全名称且没有保存值时使用“暂时不可用的专辑”。

### 2.3 ③ 的四张表

| 表 | 列与约束 |
| --- | --- |
| `personal_playlists` | `id UUID PK`, `user_id UUID NOT NULL FK users CASCADE`, `name TEXT NOT NULL`, `revision BIGINT NOT NULL DEFAULT 1`, `track_count BIGINT NOT NULL DEFAULT 0`, `created_at/updated_at TIMESTAMPTZ NOT NULL`；`UNIQUE(user_id,id)`；名字去首尾空白后 1–100 Unicode 码点、不含控制字符，数据库 `char_length(name) BETWEEN 1 AND 100` 且非空白；允许重名；`track_count BETWEEN 0 AND 9007199254740991`；10,000是普通创建/加入上限，撤销允许临时超过，不能保留会令合法撤销失败的CHECK≤10000 |
| `personal_playlist_items` | `id UUID PK`, `user_id UUID NOT NULL`, `playlist_id UUID NOT NULL`, `track_id UUID NOT NULL FK tracks RESTRICT`, `order_key BIGINT NOT NULL`, `added_at TIMESTAMPTZ NOT NULL`；`FK(user_id,playlist_id) -> personal_playlists(user_id,id) ON DELETE CASCADE`；`UNIQUE(user_id,playlist_id,track_id)`；`UNIQUE(user_id,playlist_id,order_key) DEFERRABLE INITIALLY IMMEDIATE` |
| `personal_playlist_requests` | `user_id UUID FK users CASCADE`, `idempotency_key UUID`, `command TEXT`, `playlist_id UUID`, `request_hash BYTEA`（32 字节 SHA-256）, `response_version INTEGER=1`, `response_status INTEGER IN(200,201)`, `response_body JSONB`, `created_at TIMESTAMPTZ`；`PK(user_id,idempotency_key)`；全部列NOT NULL；command限制为create/rename/delete/add_items/remove_item/restore_item/move_item；response_version默认1且CHECK=1，request_hash用octet_length=32，response_body为对象且序列化大小≤64 KiB；不保存 Cookie/原始 HTTP 请求 |
| `personal_playlist_undo` | `user_id UUID NOT NULL`, `playlist_id UUID NOT NULL`, `token UUID NOT NULL`；`PK(user_id,playlist_id,token)`，复合FK歌单 `ON DELETE CASCADE`；`item_id UUID NOT NULL`, `track_id UUID NOT NULL FK tracks RESTRICT`, `order_key BIGINT NOT NULL`, `added_at TIMESTAMPTZ NOT NULL`, `original_position BIGINT NOT NULL`（正安全整数）, `previous_track_id UUID NULL FK tracks RESTRICT`, `removal_revision BIGINT NOT NULL`, `removed_at TIMESTAMPTZ NOT NULL`, `restore_request_key UUID NULL`；`UNIQUE(user_id,token)`；空restore_request_key表示尚未恢复，非空指向首次成功恢复的本人幂等收据 |

歌单/条目/撤销均以带 owner 的复合关系防止串用户。items 和 undo 经歌单间接随用户级联，requests 直接随用户级联。requests 的 `playlist_id` **故意不设歌单 FK**：它是成功收据的目标，不是活动资源关系；删除歌单后必须仍能重放创建/删除收据，不能复活歌单。所有成功收据已裁决保留至用户删除，清理/归档策略见§3.5，不以TTL丢弃去重依据。undo改为按token保存已发出的移除凭证；界面只展示当前操作页面最近一次移除，不提供历史撤销列表，但不能因别的标签页又移除了前驱就注销已发出的凭证。歌单删除时undo级联；普通编辑不删凭证。restore_request_key是同owner收据定位值、不作为新授权凭证，首次恢复结果与收据在同一事务写入，不能指向其他用户。

索引：

- `personal_playlists_user_updated_idx (user_id,updated_at DESC,id ASC)`：列表/本人配额计数。
- items 的唯一排序索引 `(user_id,playlist_id,order_key)`：详情、前后邻居、队首/队尾；唯一曲目索引负责批量去重。
- `personal_playlist_items_user_track_idx (user_id,track_id,playlist_id)`：一首曲目在哪些本人歌单；`track_id` 单列索引服务 FK 检查。
- undo 的 track_id、previous_track_id各建单列FK检查索引（可空前驱用非空部分索引）；user/token唯一索引用于恢复凭证定位。前驱引用tracks身份而非playlist_items，防止前驱移除后凭证被级联删除；不将前驱改成null以掩盖“原来有前一首”。requests不预建JSONB GIN或TTL清理索引。

`track_count` 是本能力事务内维护的数量，不是 catalog 缓存；每次增删校验与实际条目数一致。总时长/封面/曲目当前位置**不持久化到歌单权威行**，否则重扫、修正、归组后会陈旧；读取策略见 §7、§10。

### 2.4 上限、原子性与迁移

- 每用户最多 1,000 个歌单：创建与删除都取 personal 专属“用户歌单配额”事务锁；创建在锁后用新语句计数，999 时并发两次只能一次达到 1,000。不锁/查询 users 私表来借用配额锁。
- 普通创建/批量加入最多10,000个不同track_id：锁歌单后只按真正新增数计配额，全批可容纳才写，不截断。**已裁决的例外是撤销**：允许临时超过10,000，不预留名额、不挤掉其他曲目；超限时任何新增成员的普通加入返回409 playlist_track_limit_reached，全部已存在的no-op仍成功，改名/排序/移除及继续撤销不受此配额阻止。降到≤10,000后按剩余容量恢复加入；多个合法撤销可能超过10,001，不能又加CHECK≤10001。
- 原始 `track_ids` 数组上限 10,000，去重保留首次出现。规范化前后均有界；不存在的任意曲目导致整批 404、无部分成功。missing不等于不存在，已有稳定身份允许新增个人关系，见已裁决§5.1/§12。
- 名字长度按 Unicode 码点而非 UTF-8 字节或 JS UTF-16 长度；组合字符不按视觉字素数计算。前端用同一规则预检，后端为权威。
- **未来迁移仅定名字，不占编号**：② `favorites.sql`；③ `playlists.sql`；⑤ `artist_favorites.sql`（艺人实体迁移之后，§8）。由主会话合并时统一分配连续编号；不改现有迁移、不创建空艺人表。
- 全部是 additive 建表/索引，不导入旧浏览器收藏与歌单。应用回退关闭新写入口、保留新表和收据；不以 DROP 表伪装可逆回滚。

## 3. 排序、修订号、幂等与撤销

### 3.1 选择 BIGINT 间隔键，不用连续 position 全表重排

- 初始/重整间距 `STEP=2^32`，第 i 项键为 `i*STEP`；允许负键。列表始终 `ORDER BY order_key ASC`，唯一约束防重位。wire 的 position 是从 1 开始的当前序号，不是可写的存储键。
- 移到最前/最后取 `min-STEP` / `max+STEP`，通常只改一行。拖到某项前，先从逻辑序列移走源项，再在目标前后键间取整数中点；尾部插入按末尾规则。上移/下移也换算成同一套邻居定位，不做两套算法。
- 中点没有整数空位，或边缘加减会超 BIGINT 时，锁内按当前顺序统一重整键，再执行移动；普通歌单最多改10,000行；撤销导致临时超限时按实际N项重整，保持item_id、track_id、added_at不变，不得拒绝恢复。重整步长取min(STEP, floor(MAX_INT64/(N+1)))以保证整数键不溢出，不把N写死为10,000。只在该事务延迟排序唯一约束，提交前恢复检查；不用临时非法键绕过约束。
- 同一歌单的所有写操作先锁歌单行，两个操作不会各取同一间隙后互相覆盖。重整及用户移动共用一次 revision 递增，不对外暴露内部整理事件。
- 不选连续 position：每次“移到最前”要更新O(N)行（普通歌单至多10,000，撤销临时超限时更多）。也不选浮点分数：存在精度耗尽、跨端比较和序列化问题。间隔整数仍需极少数重整，但常用动作是 O(log N) 索引定位、O(1) 行修改；客户端不计算或提交存储键。
- 拖动在当前已加载页内定位，菜单“上移/下移”跨页仍按全歌单相邻项移动，“最前/最后”直达全歌单两端；不为拖动加载 10,000 个 DOM 行。完成后重读受影响页和 revision；若未来要求跨页拖放另加交互，不变后端 before-anchor 合同。

### 3.2 歌单 revision，而非 HTTP ETag

除新建和已裁决的restore外，歌单写命令必须带读取时的expected_revision。restore只带服务端移除token与幂等键，锁内按最新状态执行恢复政策，不提交或比较客户端expected_revision。仍采用JSON修订号，不同时引入If-Match/412；恢复实际插入也递增revision，让其他基于旧顺序的普通写命令仍能检测冲突。

普通写命令锁后先比较revision，再执行业务；不相等为 `409 revision_conflict`。名字、成员或顺序实际变化时 revision 加一，updated_at 在锁后由服务端赋值；全批已存在、名字未变、首项上移/末项下移等有效 no-op 不递增、不改变最近修改顺序。**要求expected_revision的普通命令不能因为预计是no-op而绕过版本校验；restore是已裁决的明确例外，不因后续修改报revision_conflict。** 扫描改变摘要不是用户编辑，不递增歌单 revision。

### 3.3 所有歌单写命令的稳定重试

- 新建、改名、删除、批量加入、移除、恢复与移动均要求 `Idempotency-Key: UUID`。客户端为一次用户意图生成一个键；网络失败/无效响应/5xx 只能用**同键、同请求**人工重试对账，不自动重发、不换键猜测提交结果。
- 指纹包含 schema_version、命令类型、路由目标（含playlist/item ID）、适用时的expected_revision与规范化完整参数；restore按token参与指纹，不人为补入读取时的旧revision。UUID 小写、名字规范化；track_ids 保留顺序及输入重复项，因此返回计数不同的请求不会被误认同义。不用 Go map 随机遍历拼指纹。
- READ COMMITTED 事务先取 personal 专属“user+幂等键”事务级 advisory lock，再用**新语句**读收据。已有且 hash 相同，返回首次 status/body；不同为 409 idempotency_conflict。作用域跨歌单命令但不跨用户，不复用 OPER/ROOT/CATP/迁移锁命名空间；建议采用两个int的事务锁，类别分别为PKEY（幂等）/PQUO（配额），第二键取稳定hash，具体常量在实施时集中声明并测试互异。锁 hash 碰撞至多串行无关请求，不替代完整主键与 hash 比较。
- 无收据时：创建/删除取得用户配额锁；有目标则 `WHERE user_id=$viewer AND id=$playlist FOR UPDATE`；锁成功后另起语句读条目与数量，避免 READ COMMITTED 带 JOIN 锁等待后仍看见旧关联快照。普通命令再比较revision；restore改为校验token并按最新顺序恢复。随后写修改与成功收据，一起提交。失败尝试不留成功记录。
- 锁顺序：幂等键 →（创建/删除时）用户配额 → 歌单行 → 本能力条目/撤销。不在拿到歌单锁后再请求配额锁。一个命令只写一个歌单，不做多歌单批处理。
- 重放检查在目标是否存在/当前 revision 之前：已成功删除可重复成功；已成功创建后歌单又被删也只回放原收据，**不会重新创建**。收据不是最新详情；前端收到重放后仍重新读取。每次重放先同源和会话校验，停用/退出后不能读取旧收据。
- requests 随用户删除、不随歌单删除；不缓存当前 HTTP request_id。数据库/投影故障为安全 503，事务回滚；提交结果不明只走同键对账。

### 3.4 已裁决：最近一次移除的撤销不因后续修改失败

移除事务保存原item_id、track_id、order_key、added_at、**original_position（1-based）与previous_track_id（移除当时紧邻的前一首，首项为null）**，生成不可猜token，记录removal_revision。前驱按曲目身份定位，不能只存可能已变化的order_key，也不让浏览器提交旧位置或任意前驱。

界面只展示最近一次移除的一步撤销；不同标签页各自已有的提示仍可用。后端保留已发token，不因改名、移动、加入或另一次移除而失效，不设置短TTL；这不是新增面向用户的多步历史撤销功能。

恢复请求仅含token并带新的Idempotency-Key。先按§3.3查重放收据，再锁本人歌单行、读取最新成员与凭证，在同一事务按以下顺序执行：

1. token归属本人且属于该歌单。伪造/未知/跨用户/跨歌单token统一404 not_found，不透露归属；正常鉴权、歌单已删除、基础设施故障仍分别按403/401/404/503处理。“总是成功”是有效凭证的业务恢复保证，不是绕过鉴权或宣称网络永不失败。
2. 若该token已经完成恢复（restore_request_key非空），读取首次恢复的本人收据，回放成功结果；若当前请求是新的幂等键，也为其记相同成功结果。不要把第二次点击变成第二次插入；同键重试仍先走通用收据。
3. 尚未完成的token所指track_id已经重新在歌单里：返回200 `outcome=already_present`及当前条目的ItemRef，保留其现有位置/added_at，不重排、不重复添加、不递增歌单revision；记录token的首次恢复结果，界面提示“已在歌单里”，不是错误。
4. 曲目尚不在歌单且当前revision=removal_revision：恢复原item_id、order_key与added_at，`placement=original`。
5. 此后歌单有其他修改：先按previous_track_id查当前歌单；若仍在，就插在它**当前所在位置之后**，`placement=after_previous`（即使其item_id因移除后再加入改变，track_id仍可定位）。前驱为null或已不在，则按original_position插入：令目标序号=min(original_position,当前数量+1)，超过末尾就追加，`placement=original_index`或`end`。不能因歌单已变就409，也不一律塞末尾。
6. 适应新位置时重用原item_id/added_at，但order_key按当前邻居用间隔键算法重新生成；间隙耗尽正常重整，不能盲插已被别项占用的旧键。只新增这一项、不改其他项的相对顺序；track_count与revision各加一、updated_at更新。
7. 撤销插入**豁免普通10,000首加入配额**。例如已有10,000首时恢复A，变为10,001；这不是失败/部分成功，不删B也不预留位置。响应返回over_limit_count；超限期间禁止普通新增，允许移除、改名、排序、只含已存在曲目的no-op和其他合法撤销。用户移除至上限内后恢复正常容量检查。
8. 更新undo.restore_request_key并写首次恢复的稳定成功收据，一起提交；失败回滚不消费token、不留下部分重排。并发的普通编辑与恢复都受同一歌单行锁串行化：普通编辑若后到仍按expected_revision冲突，restore若后到则适应最新状态成功，不偷改普通命令的并发规则。

响应200统一为 `{playlist:PlaylistState,outcome:restored或already_present,item:ItemRef,placement:original或after_previous或original_index或end或unchanged}`；already_present固定placement=unchanged。客户端以服务端ItemRef.position定位和播报，不能保留旧页码假装还原到原槽。恢复成功后隐藏该提示；其他改动不能提前把按钮隐藏为“已过期”。

### 3.5 已裁决的收据保留与体量清理策略

personal_playlist_requests的成功收据保留至用户物理删除，不因歌单删除、普通编辑或时间经过而删除；undo凭证同样不做会破坏有效撤销的短TTL。成功结果仅小型状态/计数/ItemRef，不保存完整曲目摘要、原始请求、Cookie或日志副本；64KiB约束是硬保护，不是目标平均行大小。

实施监测本人/全表收据数、平均/最大行字节、表与索引/TOAST大小、增长率与查询延迟。第一层清理是用户物理删除时按FK级联删除其收据，以及正常VACUUM/统计维护回收死元组空间，不是删除活用户的旧幂等键。需要降低热表体量时，可在后续迁移中做透明冷存储/分区：完整保留user+key、指纹、status和原响应；查询和唯一性检查覆盖冷热两层，同一PKEY锁下迁移/重放，查不到热记录不能当作新命令执行。归档不可用必须503，不能重做原创建或恢复。

不能仅留hash而丢失原响应，也不能把旧响应改成当前资源状态；两者都会破坏原status/body重放。后台归档分批、可中断、可回滚，验收删除歌单后重放、归档中并发同键、旧token恢复和用户删除穿透冷热层。当前版本只需小收据+监测+数据库物理维护，不提前加入归档任务或改迁移数量；体量达到实测阈值后再实施透明归档，不重新打开“保留到用户删除”的产品裁决。

## 4. 公共 REST、DTO 与错误合同

以下所有路径以 `/api/v1` 为前缀，表中省略前缀；不接受客户端 user_id，成功响应直接是业务 JSON，不再套 data 信封。响应 `Cache-Control: no-store`；不暴露路径、文件身份、SQL、跨用户计数或幂等收据原文。日期均为 UTC RFC3339。

### 4.1 守卫、输入与隔离顺序

- GET：会话鉴权 → 路径/查询参数 → 本人资源查询。所有 PUT/POST/PATCH/DELETE（包括只读 POST lookup）：**同源 403 → 会话 401 → 路径、幂等键、JSON 与字段 400 → 本人资源/业务检查**。登录态失效、停用及鉴权查库失败沿现有 guard 返回 401；鉴权后数据库故障才是 503。不得先解析 UUID/JSON、检查资源是否存在或读取收据。
- 歌单必须 `WHERE user_id=$viewer AND id=$id` 找资源；条目、移动锚点、undo token 再限定同一歌单。别人的 ID 与随机不存在 ID 同样 404，不增加管理员例外。不得仅凭不可猜 UUID 授权。
- 有 JSON 的请求限 1 MiB，拒绝非对象、缺必填、错误类型、未知字段、尾随第二个 JSON；重复对象键也拒绝，避免指纹与客户端理解不同。`expected_revision` 为 1 至安全整数上界的整数。无 body 的心形 PUT/DELETE 允许空 body 或 `{}`，其他字段非法。
- 现有 `httpserver.DecodeJSON` 只限制大小并 Decode 一次，**尚不提供上述严格性**。实施在 personal/httpapi 局部加严格解码器并测试，不假称共享层已有保证，也不借本项改变其他端点兼容性。GET 不解码 body，非法/未知查询项按端点 allowlist 拒绝。
- UUID 先校验再小写；数组输入上限在去重前检查。GET 页码使用现有 `ParsePagination` 的溢出保护。可选读revision有值时必须为安全整数，收藏允许0、歌单从1开始，错误400 invalid_input；缺省表示从当前版本开始。方法不匹配沿现有 `APINotFoundHandler` 返回405 `method_not_allowed`、Allow及统一JSON信封；403→401→400 顺序指匹配的业务方法。

### 4.2 精确 DTO 词表

下面是类型记法，不是额外的 API 包装；`T?` 表示 JSON `null | T`，不是省略字段。数组始终输出 `[]` 而不是 null。

```text
Pagination = {page:正整数, page_size:正整数, total:非负安全整数}
Artwork = {resource_id:string, mime:string, width:正整数, height:正整数}
ReleaseSummary = {id:UUID,title:string,artist:string,album_artist:string?,
  year:integer?,source_type:string?,media_type:string?,medium_count:integer,
  track_count:integer,attention_count:integer,artwork:Artwork?}
TrackSummary = {track_id:UUID,title:string,artist:string,group_id:UUID,
  present:boolean,duration_seconds:正有限数?,release:ReleaseSummary,
  source:{source_kind:string,codec:string?,bit_depth:正整数?,
    sample_rate:正整数?,channels:正整数?,bitrate:正整数?}}
FavoriteTrack = {track:TrackSummary,created_at:时间}
PersonalAlbum = {group_id:UUID,available:boolean,title:string,artist:string,
  release_count:非负整数,representative:ReleaseSummary?}
FavoriteAlbum = {album:PersonalAlbum,created_at:时间}
PlaylistState = {id:UUID,name:string,revision:正安全整数,track_count:非负安全整数,
  track_limit:10000,over_limit_count:非负安全整数,
  created_at:时间,updated_at:时间}
PlaylistSummary = PlaylistState + {duration_seconds:非负有限数,
  unknown_duration_count:非负整数,missing_count:非负整数,
  cover_groups:[{group_id:UUID,artwork:Artwork?}]}
PlaylistItem = {id:UUID,position:正安全整数,added_at:时间,track:TrackSummary}
AddCounts = {added_count:非负整数,existing_count:非负整数,duplicate_count:非负整数}
```

- `TrackSummary`/`ReleaseSummary` 复用 catalog 现有安全合同，source_kind/codec 保留既有词表，不凭扩展名猜可播性；⑤扩展安全 artist refs 时复用同一个 decoder。无效封面资源 ID/尺寸/MIME 等仍由 catalog 投影拒绝，personal 不把存储路径转为 URL。
- `PersonalAlbum.available=false` 时 representative=null、release_count=0，标题/艺人取已存安全回退，封面占位、详情链接禁用；这是个人关系摘要，不放宽 `/release-groups/{id}` 的 404 可见性规则。
- 歌单时长是所有条目已知正时长之和，**包括 missing 的上次事实**；未知时长不当成真实 0，单独计 unknown_duration_count。UI 有未知时显示“已知总时长 …（N 首时长未知）”。missing_count 是当前缺失条目数，不是格式不支持数。空歌单时长0、两类计数0。
- `cover_groups` 按歌单顺序取前四个不同的当前 group_id（含无封面者），最多4项；4项拼图，1–3项仅显示第一项封面，0项/第一项无封面用占位。不得跳过前面的无封面组而偷偷改变“前四张不同专辑”含义。
- PlaylistState始终返回track_limit=10000及over_limit_count=max(track_count-track_limit,0)。临时超限不能使decoder失败或把count截成10,000；新增入口解释“歌单因撤销超过上限，请先移除N首后再添加”，只禁普通新增，不禁播放、排序或继续撤销。
- 写命令仅返回小的 PlaylistState 和计数/动作结果；不把10,000摘要写入幂等收据。读详情/列表才计算 PlaylistSummary。

### 4.3 错误矩阵

实际统一信封示例：

```json
{"error":{"code":"revision_conflict","message":"歌单已在其他页面修改，请刷新后再试。"},"request_id":"sample-request"}
```

| HTTP | code | 条件与客户端行为 |
| --- | --- | --- |
| 400 | `invalid_input` | UUID、名字、必填、类型、幂等键、重复键/未知字段、非法动作或超大的输入数组；输入长度限制不是业务配额 |
| 400 | `invalid_json` | 非法/超大小 JSON、尾随内容；请求未执行业务 |
| 400 | `invalid_pagination` | 页码/大小/偏移非法；queue不接受cursor或其他未定义参数，传入按invalid_input拒绝 |
| 401 | `unauthorized` | 沿 identity guard；清理会话代次，不把它当空收藏/空歌单 |
| 403 | `origin_required`, `origin_forbidden` | 沿同源 guard（缺来源/不可信来源）；不继续鉴权或解析 |
| 404 | `not_found` | 目标不存在，或歌单/条目不属本人/同一歌单；不透露是哪种 |
| 405 | `method_not_allowed` | 注册路径的方法不支持，沿API fallback并带Allow |
| 409 | `revision_conflict` | 普通歌单写命令expected_revision或读分页revision已过期；restore不使用此冲突 |
| 409 | `favorites_changed` | 收藏列表携带的集合版本过期；重读列表。queue单请求快照不使用此冲突 |
| 409 | `idempotency_conflict` | 同用户同键不同命令、路由或参数；不自动换键重发 |
| 409 | `playlist_limit_reached`, `playlist_track_limit_reached` | 1,000歌单配额或普通新增将超过10,000；超限期间普通新增也拒绝。restore豁免曲目配额，响应200并报告超出数量 |
| 503 | `database_unavailable` | 鉴权后SQL、事务、投影坏值或缺失被强FK保证的摘要；不返回部分成功，不泄露原始错误 |

不另用 422 表示同一类字段校验。revision 即将越过安全上界时拒绝写入并告警，归安全503，禁止回绕。客户端的 `queue_full` 是 player 命令结果/本地预检，不伪装成服务端 HTTP 错误。⑤额外的准备态见 §8。


## 5. 收藏接口与播放全部

### 5.1 路由、分页与切换

| 方法与路径（省略 /api/v1） | 请求 | 成功状态与完整响应形状 |
| --- | --- | --- |
| GET `/me/favorites/tracks` | `page=1&page_size=50`，可带 `revision` | 200 `{items:FavoriteTrack[],pagination:Pagination,revision}` |
| GET `/me/favorites/albums` | 同上，两栏独立页码 | 200 `{items:FavoriteAlbum[],pagination:Pagination,revision}` |
| PUT `/me/favorites/tracks/{track_id}` | 空 body 或 `{}`；不要求幂等键 | 200 `{target_kind:"track",target_id,favorited:true,created_at,revision}` |
| DELETE `/me/favorites/tracks/{track_id}` | 同上 | 200 `{target_kind:"track",target_id,favorited:false,created_at:null,revision}` |
| PUT `/me/favorites/albums/{group_id}` | 同上 | 200 `{target_kind:"album",target_id,favorited:true,created_at,revision}` |
| DELETE `/me/favorites/albums/{group_id}` | 同上 | 200 `{target_kind:"album",target_id,favorited:false,created_at:null,revision}` |
| POST `/me/favorites/lookup` | §5.2 JSON，不要求幂等键 | 200 §5.2 JSON |
| GET `/me/favorites/tracks/queue` | 无body/查询 | 200 §5.3 JSON；一次返回过滤missing后的前10,000首及精确省略计数，不接受分页或cursor |

列表 page_size 默认50、最大100。`ORDER BY created_at DESC, target_id ASC`，total是本人该类精确数，空/越界页200空数组并保留total。一次只读 REPEATABLE READ 事务读 revision、计数、页内关系及 catalog 摘要；读取前一批 rows 完全关闭后才能复用同一连接。

为使“分页不重不漏”在并发编辑时不静默失效，第一页返回 revision，客户端接着翻页携带它；服务端发现版本变化返回409 `favorites_changed`，客户端清掉本次分页版本、重新读目标页并提示列表已更新，不能把两版结果拼接。直接打开地址中的第N页可不带revision，从当前版本开始。数据静止时稳定且不重不漏；不承诺编辑中维持跨请求数据库快照。浏览器后退复用的旧缓存也须按集合版本检查。

PUT 在本人 state 行锁内先看是否已收藏，已有即返回原时间，无需因目标暂时不可用拒绝重复 PUT；没有关系才经 catalog 批量/公开存在性校验并写入。DELETE 不需读取 catalog；合法 UUID 即使不存在也返回 false，实现重复取消天然幂等。收藏不存在目标的 **PUT** 才404，避免与重复DELETE不报错相矛盾。每次返回的revision代表该次事务观察到/写成的本人集合；不得将一次单目标响应当作全量心形快照。

**已裁决的资源政策（§12）**：对全体成员公开的目录，已存在但 missing 的曲目、已存在但暂无可见版本的组，允许按稳定身份加入个人关系；不可用不等于跨用户/无权资源，不向其开放隐藏详情或媒体。空组首次收藏只能保存空的安全回退、展示“暂时不可用的专辑”；已有收藏读取仍用其保存值。若已取消的灰态行立即点回，采用同一PUT，不要求新增恢复API。真正不存在或未来有授权屏障的目标必须404。主会话已确认将“个人安全摘要可读”与“目录详情可见”分开：本规则允许收藏已有稳定身份，但不豁免授权、不开放不可见详情或媒体。

### 5.2 一页一个请求的批量心形状态

```json
{"track_ids":["10000000-0000-4000-8000-000000000001"],"group_ids":["20000000-0000-4000-8000-000000000001"]}
```

```json
{"revision":8,"tracks":[{"track_id":"10000000-0000-4000-8000-000000000001","favorited":true}],"albums":[{"group_id":"20000000-0000-4000-8000-000000000001","favorited":false}]}
```

- 两数组可省略，缺省为 `[]`；显式null非法；原始track_ids最多10,000、group_ids最多1,000，重复ID保留首次顺序输出一次。空数组返回空集合及本人revision。无须为一张10,000轨专辑发送10次心形查询。
- 在一个只读快照中读state和两张收藏表，按主键批量查本人关系；不调用 `LoadTrackSummaries`，因此它的1,000上限不限制状态查询。合法但未知ID返回 false，不枚举目录存在性，不返回其他用户的收藏。
- 前端查全部曲目ID及头部组ID一次，按 target 构造查找表供每行消费，不把一页N个useQuery变成N次请求。⑤在同一路由 additive 加artist_ids（最多1,000）及artists结果，艺人请求与曲目/专辑请求分开，详见 §8。

### 5.3 收藏曲目队列：先过滤缺失，再取前10,000首替换

**主会话已裁决**：播放全部不是向旧队列追加。服务端按收藏时间倒序、track_id升序遍历本人全部关系，先排除missing，再保留前10,000首present候选；超出部分计入omitted_count并返回200，不能再以超过10,000为由整批拒绝。播放完成选择后用一次playTracks替换队列，旧队列即使已满也不影响容量。

```json
{"items":[],"total":0,"revision":0,"missing_count":0,"eligible_count":0,"returned_count":0,"omitted_count":0,"limit":10000,"truncated":false}
```

- items为TrackSummary[]，只含present，按过滤后的原顺序、最多10,000项；它不是格式/浏览器一定可播的承诺。
- total是本次快照的全部曲目收藏数；missing_count是全部缺失数；eligible_count=total-missing_count（即present数）。returned_count=min(eligible_count,limit)=items.length；omitted_count=eligible_count-returned_count；truncated严格等于omitted_count>0。所有计数非负安全整数，即使空集合也全部返回。
- 例：总收藏12,005首、缺失2,000首，则eligible_count=10,005、returned_count=10,000、omitted_count=5。缺失不占窗口，也不重复算作“因上限省略”。服务端必须统计完整本人集合，不能仅取10,001条就把省略数猜成1。
- catalog只知道present，不知道用户浏览器能力。前端对返回窗口再次按capability过滤；窗口内unsupported单独计数，不从窗口外补位，不把omitted_count说成“另外N首保证可播”。三类反馈是“已加入N首；跳过M首缺失、U首格式不支持；另有O首因10,000首上限未纳入本次播放”。

采用**单次HTTP、单个短只读REPEATABLE READ事务**：读取本人state → 时间索引keyset分批读收藏ID（每批最多10,000，rows读完关闭）→ catalog.LoadTrackFacts同批判断present并累计精确计数，仅保留前10,000候选ID → 对保留ID按1,000分批LoadTrackSummaries（最多10批）→ 按原序组装响应。personal不JOIN catalog私表；不会把10万条完整摘要加载进Go/浏览器。强FK保证的关系缺facts/摘要属于投影错误503，不把坏项算missing。实现可把分批循环封装为用例内部函数，不对外暴露cursor或维持跨HTTP事务。

服务端峰值保留一批轻量事实、10,000选中ID及最多10,000完整摘要；遍历总成本仍与本人收藏数成正比，精确省略数不能靠仅查第一页获得。响应大小和事务时长在§10实测，context取消即结束事务；扫描/收藏变化不会把不同快照拼成一份队列。revision用于标记本次集合版本，不要求读取后阻止用户继续修改。

客户端取得capability table与当前会话代次后按需读取，检查全部计数恒等式，再调用一次playTracks。只要有可播项，输入长度必定≤10,000，不以旧队列已满显示queue_full；保留shuffle/repeat设置。空集合、窗口全不可播、读取/解码失败、取消或身份变化均保留当前队列，旧回调不能提交。既有播放预检继续处理选择后文件再次变化的情况。

“下一首播放/加入队列”仍通过playNext/enqueue按**当前剩余容量**整批检查，放不下就整批拒绝，不能为了模仿播放全部而截断追加。收藏数量本身不设10,000上限；只有本次替换队列的候选窗口有限量。

## 6. 歌单接口

### 6.1 路由全集

所有写请求带UUID `Idempotency-Key`；除创建与restore外必须带下表所列expected_revision。`ItemRef={id,track_id,position,added_at}` 是不带catalog摘要的小回执，position是本次动作后的1-based序号。所有成功响应数组均非null。

| 方法与路径（省略 /api/v1） | 请求 | 成功状态与完整响应形状 |
| --- | --- | --- |
| GET `/me/playlists` | `page=1&page_size=20`，最大20 | 200 `{items:PlaylistSummary[],pagination}` |
| POST `/me/playlists` | `{name,track_ids?}` | 201 `{playlist:PlaylistState,added_count,existing_count,duplicate_count}` |
| GET `/me/playlists/{playlist_id}` | 无body/查询 | 200 `{playlist:PlaylistSummary}` |
| PATCH `/me/playlists/{playlist_id}` | `{expected_revision,name}` | 200 `{playlist:PlaylistState,changed:boolean}` |
| DELETE `/me/playlists/{playlist_id}` | `{expected_revision}` | 200 `{playlist_id,deleted:true}` |
| GET `/me/playlists/{playlist_id}/items` | `page=1&page_size=50`，最大100，可带`revision` | 200 `{playlist:PlaylistState,items:PlaylistItem[],pagination,page_after_item_id:UUID?}` |
| POST `/me/playlists/{playlist_id}/items` | `{expected_revision,track_ids}` | 200 `{playlist:PlaylistState,added_count,existing_count,duplicate_count}` |
| DELETE `/me/playlists/{playlist_id}/items/{item_id}` | `{expected_revision}` | 200 `{playlist:PlaylistState,removed:ItemRef,undo:{token,removal_revision}}`；removed.position是移除前序号 |
| POST `/me/playlists/{playlist_id}/restore` | `{token}`，不带expected_revision | 200 `{playlist:PlaylistState,outcome:restored或already_present,item:ItemRef,placement:original或after_previous或original_index或end或unchanged}` |
| POST `/me/playlists/{playlist_id}/items/{item_id}/move` | §6.4 判别JSON | 200 `{playlist:PlaylistState,moved:ItemRef,changed:boolean}` |
| GET `/me/playlists/{playlist_id}/queue` | 无body/查询 | 200 `{playlist:PlaylistState,items:TrackSummary[],total,missing_count,eligible_count,returned_count,omitted_count,limit:10000,truncated}` |
| GET `/me/playlists/containing-track/{track_id}` | 无body/查询 | 200 `{track_id,items:[{playlist_id,revision}]}` |

GET列表以updated_at倒序、id升序打平；名字重复不合并。不用返回所有1,000张卡片；对话框分页/加载更多沿同一接口。列表一次快照内total和本页一致，翻页间有修改时允许最近修改顺序变化，写后失效重新读取，不合并两次查询作为永久列表。详情条目读可携带revision保证分页不拼旧顺序；同一只读快照读歌单行、页内条目与catalog，revision不符409。空页200，错误归 §4，成功重放状态码与首次相同。

`containing-track` 只查本人items与本人playlists，可按playlist_id升序返回至多1,000项，不分页、不做catalog查询。合法未知track_id得到空数组，非法UUID400；对话框以ID集合标“已在歌单里”，revision仅是读取时的版本。批量选择不称“全部在内”：本接口专用于一首，整张/队列由加入回执报告计数。静态路由containing-track须在路由测试覆盖，避免当成playlist UUID。

### 6.2 新建、改名、删除

```json
{"name":"晚间聆听","track_ids":["10000000-0000-4000-8000-000000000001"]}
```

```json
{"playlist":{"id":"30000000-0000-4000-8000-000000000001","name":"晚间聆听","revision":1,"track_count":1,"track_limit":10000,"over_limit_count":0,"created_at":"2026-11-04T12:00:00Z","updated_at":"2026-11-04T12:00:00Z"},"added_count":1,"existing_count":0,"duplicate_count":0}
```

创建track_ids缺省/空数组建空歌单；对话框“新建并加入”和“存为歌单”用同一创建命令，**新建+所有曲目一次事务**，避免建成空歌单后第二请求失败。初始完整状态revision=1，不逐首递增。名字trim后必填、最长100 Unicode码点，拒绝控制字符；不自动截断。UI日期预填使用操作者本地日期，如“队列 2026-11-04”，服务器不强制名称模板。

改名请求例：`{"expected_revision":1,"name":"周末聆听"}`；规范化后同名是200 changed=false，仍先校验revision。删除请求例：`{"expected_revision":2}`；UI先确认“歌单《X》会被删除，不能恢复。”，删除连同items/undo级联，requests保留。相同键删除重放200；换新键删除已不存在歌单404，不谎称删除命令与收藏DELETE相同。

### 6.3 批量加入、移除、撤销

```json
{"expected_revision":2,"track_ids":["10000000-0000-4000-8000-000000000001","10000000-0000-4000-8000-000000000002","10000000-0000-4000-8000-000000000002"]}
```

设原始长度L、去重后首次出现序列U：`duplicate_count=L-|U|`；`existing_count`是U中请求前已在目标歌单里的不同曲目数；`added_count=|U|-existing_count`。三者相加=L。上例若第一首原已存在，回执added=1/existing=1/duplicate=1；UI“已加入1首，1首已在歌单里”，必要时另说输入1项重复已忽略，不能把同批重复冒充此前已存在。创建existing_count始终0。

加入track_ids必须1..10,000项；合法missing可按§5.1已裁决政策加入，真正不存在项令全批404。新增依U顺序接到末尾，原有项位置、added_at不变。普通expected_revision校验后，若全批已存在，先返回200 added=0/revision不变（即使当前已临时超限）；只有added_count>0才检查最终容量并批量插入，不做10,000次往返。达到或因撤销超过10,000时，仅重复加入仍成功，包含任一真正新项则409；restore不是普通加入，按§3.4豁免配额。

整张专辑（已裁决）：取详情中**当前所选发行版本**的可见曲目，按碟序、轨序、稳定ID顺序传track_ids，不合并release group全部版本；“整张”指UI正在看的版本，不改变专辑收藏按组的身份。队列：按当前队列顺序截取一次快照、去重保留第一次，最多10,000不同项；队列继续播放/重排不修改已打开的提交快照。过期catalog可见性仍由后端ID检查和安全摘要权威处理。

移除body只有expected_revision，不额外提交旧position；成功removed回执+undo是局部提示所需的全部。示例形状：

```text
{playlist:PlaylistState,
 removed:{id:UUID,track_id:UUID,position:3,added_at:时间},
 undo:{token:UUID,removal_revision:4}}
```

恢复请求 `{"token":"40000000-0000-4000-8000-000000000001"}`，生成新的Idempotency-Key，不提交expected_revision。按§3.4读取当前歌单适应恢复：原位/前驱后/原序号或末尾；已重新加入则200 already_present，绝不报undo_conflict。token保留原位置事实，removal_revision仅用于选择定位分支，不是拒绝恢复的前置条件。成功响应的ItemRef给最终位置，超限由PlaylistState.over_limit_count说明。

### 6.4 拖动与四项菜单移动

请求严格为以下两种之一；不能同时传action与位置索引，不接受绝对position/order_key：

```json
{"expected_revision":5,"action":"up"}
```

```json
{"expected_revision":5,"action":"before","before_item_id":"50000000-0000-4000-8000-000000000002"}
```

- 菜单action允许up/down/first/last且禁止before_item_id。up/down是全序列相邻，first/last是全序列两端。
- 拖动action=before必须带before_item_id，UUID代表移到该条目前，null代表全歌单最后。拖到当前页最后时使用本页返回的page_after_item_id：下一页还有项则传其首项ID，真到全歌单末尾才传null；不能把当前页末尾误作全歌单末尾。
- 为避免额外大列表请求，items响应新增有界边界信息 `page_after_item_id:UUID?`，取本页多读一条得到下一行ID，空/末页为null；表格中的items响应始终包含此字段。它只是定位锚点，不赋权；服务器仍检查expected_revision和锚点归属。
- 源项=锚点、已经紧邻锚点、已在首尾又移首尾都是200 changed=false；无效action400，源/锚点不在本人该歌单404。锁后检查revision优先于业务no-op。
- 成功moved.position为完整序列中的最终位置；一次移动只加一次revision。前端乐观行序只作用当前页，服务端返回后重读页边界/统计与最新revision，不自行猜其他页的缓存内容。

### 6.5 歌单播放队列

同收藏已裁决的“先过滤missing、按当前顺序最多取10,000首再替换”规则。一份只读REPEATABLE READ快照内读本人歌单并按order_key分批遍历全部条目，total=track_count；每批最多10,000个ID经LoadTrackFacts区分missing，累计精确数并只保留前10,000个present ID，再按每批1,000调用LoadTrackSummaries，按条目顺序组装。即使撤销临时超限也不能只统计前10,000条。缺facts/摘要整体503，不能误算missing。

响应在收藏queue计数形状之外带playlist，不单独返回revision（用playlist.revision）；恒等式同§5.3：total=missing_count+eligible_count，returned_count=items.length=min(eligible_count,limit)，omitted_count=eligible_count-returned_count，truncated=(omitted_count>0)。普通歌单最多10,000项，所以通常omitted_count=0/truncated=false；撤销导致临时超限时，按过滤后的present数返回真实omitted_count/truncated，不能写死为0/false。空歌单所有计数0、items=[]。

前端再过滤返回窗口内的格式不支持项并提示，至少一首可播才一次playTracks替换当前队列；旧队列满不构成拒绝理由，无可播/读取失败不清空当前队列。整歌单“加入队列”（若提供）仍走enqueue剩余容量整批检查，不能截若干首追加。扫描后的真正可播性依旧由播放预检兜底；不因缓存复用绕过会话检查。

## 7. catalog 的公开读取增量与补全策略

### 7.1 最小新增端口

以下为建议公开函数名/语义，不在本轮创建代码；消费端接口位于personal，app装配默认适配器。接受既有事务指通过 `catalog.NewStore(tx)` 或明确的公开事务入口，不在Store内部另开事务。

| 公开函数 | 输入边界与输出 | 用途/不可越界点 |
| --- | --- | --- |
| 现有 `LoadTrackSummaries(ctx, ids)` | 至多1,000不同有效UUID；map[id]TrackSummary，含missing，不存在无键 | 列表50/100项一次；大队列≤10批。保持当前上限，不改成任意大输入；personal边界已拒绝非法ID，不能依赖内部跳过无效UUID替代校验 |
| 新增 `LoadTrackFacts(ctx, ids)` | 至多10,000不同UUID；map[id]{track_id,release_id,group_id,present,duration_seconds}，不存在无键 | 批量加入存在性、卡片统计、播放全部先过滤missing并精确计数。只读当前身份/位置和安全时长，不计算完整ReleaseSummary，不带路径/存储身份；missing与不存在明确区分 |
| 新增 `LoadReleaseGroupSummaries(ctx, ids)` | 至多1,000不同UUID；map[id]{group_id,available,release_count,representative:ReleaseSummary?}，存在但无可见成员仍有键、available=false/代表null；真正不存在无键 | 收藏专辑存在性/灰态、可用时标题和封面、歌单前4不同group封面；复用组代表排序与可见性。不得逐组调用详情，或把private SQL拷贝到personal |
| ⑤ `LoadArtistSummaries(ctx, ids)` | 至多1,000，返回原输入ID、canonical ID、available及安全显示摘要，不存在无键 | 只读列表/批量状态；与CATP写锁解析入口分离，见 §8 |

函数命名由主会话同搜索线统一，不能出现两个同名不同可见性的ReleaseGroupSummary DTO。这里新组函数返回**带availability的包装**，不改变现有 `catalog.ReleaseGroupSummary.representative` 非null合同。不可用组不回传不可见发行详情，personal自身用保存值回退。

轨道/组不存在是业务404（仅写入校验）；读取被FK保证的已有关系却无摘要为完整性故障503，不让列表total悄悄大于有效行数。空输入直接空map不访问数据库；每批关闭rows并检查rows.Err，不能一边挂起游标一边在同一连接再查询。

### 7.2 歌单卡片：实时事实而非陈旧缓存

一页最多20个歌单，流程在一个只读快照完成：

1. 只读本人本页歌单行及total，不预加载1,000歌单的全部条目。
2. 只读本页playlist IDs的items，按playlist_id/order_key做内部keyset分批，每批最多10,000条轻量引用；只关联本能力表且全部限定user_id。普通满额页最多20×10,000=200,000条引用；撤销临时超限时按实际条目数处理，不能截前10,000算时长，也不能让整个超限集合进入一个无界map。
3. 每批关闭rows、对ID批内去重，再经LoadTrackFacts读轻量事实，增量更新最多20份统计。普通最大页至多20批facts；跨歌单重复轨可能跨批重复读取，可用有界缓存优化，但不以全库缓存换取少几个查询。临时超限按ceil(本页实际引用数/10,000)增加批次，context可取消。
4. 按每歌单顺序累计已知时长/未知/缺失及前4不同group；跨歌单再去重这些group，最多80个，一次批量读组安全摘要/封面，不可用组artwork=null。
5. 输出最多20张小卡片，包含真实track_count、over_limit_count。详情同样按批统计一个歌单，items分页只补全本页摘要；不能因为总条目数大就将已裁决的撤销状态解释成数据库坏数据。

总时长、封面随扫描/改归组/修正自然更新，不持久化为陈旧权威；无catalog→personal全量缓存同步。**主会话已裁决在实现时实测大歌单卡片性能**：普通满额页20万引用，另测撤销后10,001及更多条目的情况，记录往返/内存/响应时长，不声称300ms SLA。若成为瓶颈，先优化轻量批次/有界缓存；缩小UI页或新增轻量选择器等改变另按实施评审处理，不以仅统计前10,000项或显示陈旧时长蒙混。

10,000完整TrackSummary的JSON可能很大，歌单queue须测响应字节数与解码时间；不应为了提升首屏而预取它。只有用户点播放/加入时读取，支持context取消。完整队列补全保持输入顺序，即使不同批次重复发行也不全库扫描；必要优化只能在catalog owner内部。


## 8. ⑤ 艺人收藏的后续接入合同

本节落实搜索设计 §11 已裁决的锚点语义，不重新讨论“合并后要不要保留收藏”。②交付的表不引用尚不存在的artists。⑤在④/⑤实现线、艺人实体迁移之后增加**一个** `artist_favorites.sql` 迁移（只定名字，编号由主会话合并分配），新增下表/索引即可；不重写favorite_tracks/favorite_albums、不迁移旧本地数据。

### 8.1 独立typed表与派生身份

```text
artist_favorites(
  user_id UUID NOT NULL REFERENCES users ON DELETE CASCADE,
  artist_id UUID NOT NULL REFERENCES artists ON DELETE RESTRICT,
  resolved_artist_id UUID NOT NULL REFERENCES artists ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(user_id,artist_id)
)
```

这是结构说明，不是本轮迁移。artist_id是用户意图的**原锚点，合并永不改写**；resolved_artist_id是可重建的当前canonical解析列，不是第二份收藏意图。**不对(user_id,resolved_artist_id)加唯一约束**：否则合并双方两条意图会永久丢失。艺人合并/拆开/缺曲目都不物理删除锚点，用户删除才CASCADE。

索引建议：`(user_id,resolved_artist_id,created_at DESC,artist_id)` 支持本人等价查询/取消/聚合；`(artist_id,user_id)` 支持受影响锚点刷新与FK检查；`(resolved_artist_id)`支持该FK检查。PK支持本人原锚点查询。聚合后的MAX时间排序无法由原表created_at索引完全替代，不用“加时间索引就无排序”掩盖成本。

列表在**本人集合内先GROUP BY resolved_artist_id，再取MAX(created_at)，再ORDER BY时间DESC、canonical ID ASC，最后LIMIT/OFFSET**；同一快照COUNT聚合结果得精确total。页内canonical IDs最多100个才批量补充catalog摘要。不得先LIMIT原锚点再去重、在Go无限拉全本人收藏，或让前端去重后出现短页/错total。

### 8.2 原锚点的完整行为例

- 用户先收藏A和B，时间分别t1/t2；A合并到B后两条原记录仍在，只把resolved均指B，列表一条B，created_at=max(t1,t2)。
- 此时PUT B（或请求旧A经公开解析到B）若已有任一等价收藏则no-op：不新增B锚点、不更新旧时间。没有等价收藏的新用户收藏时只插入当前canonical B。
- DELETE canonical B删除本人所有resolved=B的原锚点；别人不受影响。以后拆开不恢复已主动取消的意图。
- 治理恢复旧A身份后，未取消者的A锚点resolved回A，原收藏自然重新显示；只在合并后新收藏B者不凭空获得A。拆到新C也不复制全体用户收藏。
- 失去present曲目的艺人安全身份仍在，available=false，收藏保留、可取消；未来重新可用自动恢复。原始锚点数组永不向普通列表展开。

### 8.3 API扩展与准备态

| 方法与路径 | 请求/成功响应 |
| --- | --- |
| GET `/me/favorites/artists` | 同收藏page默认1/50、最大100、可选revision；200 `{items:[{artist:ArtistSummary,created_at,available}],pagination,revision}`；artist.id是canonical，created_at是聚合MAX |
| PUT/DELETE `/me/favorites/artists/{artist_id}` | 空body或{}；200 `{target_kind:"artist",target_id:canonical_id,favorited,created_at:时间或null,revision}`；PUT重复保留等价组的MAX时间，DELETE后null |
| POST `/me/favorites/lookup` | additive `artist_ids?:UUID[]`，原始最多1,000；响应增加 `artists:[{artist_id:输入ID,resolved_artist_id:UUID或null,favorited:boolean}]`，按首次输入顺序；合法未知ID为null/false |

ArtistSummary沿搜索设计 §10：`{id,name,initial_group,album_count,track_count,roles,available}`；initial_group为A–Z/kana/#，两计数为非负安全整数，roles为该设计的七项角色词表，数组非null。列表外层available与artist.available必须一致。准备就绪后PUT不存在艺人404；DELETE合法但不存在艺人仍200 false/null并返回输入ID作为target_id，保持天然幂等，不为未知ID制造canonical身份。

⑤前的DTO不接受artist_ids，⑤发布后旧tracks/albums字段和含义不变；⑤响应artists始终数组，未请求则空数组。artist_ids非空时在catalog公开端口批量解析canonical，再查本人等价收藏状态；不把favorited塞入catalog/search共享DTO。当前用户对任何等价锚点有收藏，输入旧ID与canonical都应返回true。

艺人准备状态沿搜索线：读艺人收藏列表或带非空artist_ids的lookup返回503 `artist_catalog_preparing`及`Retry-After: 3`；艺人PUT/DELETE返回409 `artist_catalog_not_ready`，不伪装成空/未收藏。无artist输入的曲目/专辑lookup和歌单照常工作。app把艺人批量状态与曲目/专辑分开请求；混合非空请求在未ready时整体失败，不返回真假混合的部分结果。所有状态检查仍在同源/鉴权/输入之后。

### 8.4 解析入口、刷新端口与锁序

- catalog提供写用公开入口，逻辑为 `ResolveArtistFavoriteTargets(ctx, tx, ids)`：接受调用者事务，**先取得与治理相同的CATP gate**，检查artists_state=ready，再读取最新canonical；单批≤1,000，gate持有到事务结束。内部如何加锁由catalog负责，personal不能导入catalog/internal/writegate。读取用LoadArtistSummaries不拿写gate，而是在同一只读快照检查准备态与摘要。
- 艺人收藏PUT/DELETE：同源→鉴权→输入→开启READ COMMITTED事务→catalog公开入口取CATP及解析→确保/锁本人favorite_state→读本人等价锚点→写关系/版本→提交。**不能先锁favorite_state再等CATP**；曲目/专辑收藏不拿CATP，也不在锁state后调用艺人解析。
- catalog/governance声明消费端窄回调，app注入personal的 `RefreshArtistResolution(ctx, tx, mapping)`。mapping必须含所有受影响**原ID**的新canonical（包括redirect链中传递影响），不是只传一个A→B然后要求personal查询艺人私表。⑤经公开解析生成映射（每次解析≤1,000），将一次治理的完整mapping集合交给owner刷新一次；owner内部再分批SQL，不能每一小批分别调用刷新并提交/重新递增用户版本。全部批次在原治理事务中完成。
- 治理的顺序为其operations幂等锁→CATP→艺人相关行→personal受影响用户state（UUID升序）→artist_favorites派生列。personal刷新实现先汇集所有批次涉及的用户并统一排序锁state，再更新派生列；不能每批重新按局部顺序加锁造成锁序倒转。CATP阻止并发艺人收藏改变锚点集合；普通track/album收藏最多等待一个state，不反向等CATP。
- 回调只改resolved_artist_id，**不改artist_id、created_at，不增删用户意图**。实际解析变化的每个用户favorite_state.revision在同一治理事务仅递增一次，无变化不递增。CATP + 固定state锁序保障刷新与本人CRUD不竞态；收藏分页因共用revision可能保守重读；单快照queue不跨请求拼接，不需要因此重启。
- catalog/governance不直接UPDATE个人表、不返回跨用户收藏详情/数量给管理员、不经管理员REST调用该回调。该系统投影维护不是以管理员身份代用户收藏，不接personal的CRUD审计，也不把个人CRUD交给operations.Run。刷新失败令艺人治理、解析列、state版本与操作记录全部回滚；禁止提交后异步更新。

后续算法升级的派生列重建由⑤准备阶段调用同一owner端口、经公开catalog解析、分批完成；artists_state保持未ready直到一致性检查通过，readiness只门控艺人能力，不阻塞应用ready或其他个人数据。首次⑤迁移没有艺人收藏可回填；不能据此假设整个收藏数据库为空。实际重建期间新增艺人写已被准备态拒绝，原锚点与时间仍保留。系统投影端口也必须有可回滚失败测试，不能把跨用户写权限泛化成公开管理功能。


## 9. 前端合同与共享组件归属

### 9.1 feature边界、DTO与地址

新增 `frontend/src/features/personal/`，内部按favorites/playlists文件区分，不按数据库行建React全局store。API层拥有 §4–§6 的请求/响应decoder及mutation；catalog纯数据模块拥有公共TrackSummary/ReleaseSummary解码；app负责player映射与跨feature动作组合。personal可消费catalog公开只读类型/封面和shared组件，不导入player、listening、search、artists、governance；那些feature也不直接导入personal界面。

新增/修改建议文件（实施时在最近已有模式下建立，不本轮创建）：

| 文件或目录 | 归属与公开合同 |
| --- | --- |
| `features/personal/api/favorites.ts`, `playlists.ts`, `decoders.ts` | 类型化endpoint与DTO；只经shared/api/request，不直接fetch |
| `features/personal/query-keys.ts`、`hooks/` | 查询键、批量状态、收藏mutation、分页、歌单命令和只读queue加载 |
| `features/personal/components/favorite-button.tsx` | `target:{kind,id,title}`，kind为track或album（⑤加artist）、状态/动作来自范围hook；Heart与aria-pressed、旁边role=status |
| `features/personal/components/favorite-status-scope.tsx` | 一批ID查一次，提供按target读取的memoized上下文；不每行构造整批ID数组 |
| `features/personal/components/add-to-playlist-dialog.tsx` | `trackIds`快照、单轨membership标记、分页选择、新建并加入，返回成功反馈；不读player engine |
| `features/personal/components/create-playlist-dialog.tsx` | 名字、可选initialTrackIds、初始名字、提交/重试key与不确定状态 |
| `features/personal/components/favorites-view.tsx`, `playlists-view.tsx`, `playlist-detail-view.tsx` | 展示和本feature交互，播放/跨feature行操作由props插槽注入 |
| `features/catalog/api/track-summary.ts` | 纯类型/decoder，不经含CSS组件的barrel给Node样本测试导入；历史、搜索、艺人、personal复用 |
| `app/routes/track-actions.tsx`、`track-playback.ts` | 统一安全摘要→PlayerTrack映射、播放/下一首/入队、心形/添加歌单/页面特有动作组合 |
| `app/routes/personal-playback.tsx` | 收藏/歌单有界队列及省略计数交给player，窗口内能力过滤、替换队列、取消与会话代次保护 |
| `app/player-personal-actions.tsx` | 底栏当前轨心形、队列存为歌单，拿player公开数据后调用personal公开组件 |
| `app/routes/favorites-page.tsx`, `playlists-page.tsx`, `playlist-detail-page.tsx` | URL、页面组合与页签；现有me-layout只additive加入口 |
| `shared/ui/toggle-icon-button.tsx`（确有复用再抽） | 纯通用按钮，无target/收藏API/路由；优先复用Button+aria-pressed，禁止把FavoriteButton放shared |

`shared/api/request.ts` 的method union需追加PUT并补最小测试；这是实施共享热点，不用绕过共享认证/信封处理。response decoder验证必填、nullable、数组上限、非负计数和安全整数，接受未来additive字段；对queue检查unique轨道、items全部present、returned_count=items.length及§5.3计数恒等式，对列表不强制items.length=total。损坏响应按现有invalid_response处理、不写Query。

URL：`/me/favorites?tab=albums|tracks&page=N`（⑤加artists）；`/me/playlists?page=N`；`/me/playlists/{id}?page=N`。缺省tab=albums/page=1不必写查询串；切tab回第一页，非法参数按现有validateSearch显式归一为undefined。两收藏栏各自query缓存独立；越界页仅在真实新响应到达后replace为末页，不能拿keepPreviousData占位判断。

### 9.2 查询键与失效矩阵

沿用 `viewerQueryKey(generation, ...)`，所有键均以 `["viewer", generation, "personal", …]` 开始。下面省略前三段；idsKey为小写去重后排序的稳定数组，page输入固定且纳入键；调用API仍可保持输入第一次顺序，按ID消费不靠顺序配对。

```text
favorites/all       → [favorites]
favorites/list      → [favorites,kind,list,{page,pageSize,revision}]
favorites/lookup    → [favorites,lookup,{trackIds,groupIds}]
favorites/queue     → [favorites,tracks,queue]
⑤ artist lookup    → [favorites,lookup,{artistIds}]
playlists/all       → [playlists]
playlists/list      → [playlists,list,{page,pageSize}]
playlists/detail    → [playlists,id,detail]
playlists/items     → [playlists,id,items,{page,pageSize,revision}]
playlists/queue     → [playlists,id,queue]
playlists/containing→ [playlists,containing-track,trackId]
```

revision未提供时键中显式用null，不能与字符串undefined混用。队列是用户触发的有界单请求，不后台预取；使用短生命周期/设gcTime=0，只缓存最多10,000候选与完整计数，不把全收藏摘要堆在Query里。集合version只约束集合，不阻止catalog事实更新。

| 成功动作/外部变化 | 立即更新与失效 |
| --- | --- |
| 任一心形切换 | 同代次所有已缓存lookup中该target的boolean精确patch；失效favorites前缀（两栏/队列/⑤艺人），让其他已挂载列表与底栏读到服务端；不把favorited写进catalog/search DTO |
| 新建/删除歌单 | playlists列表前缀失效；创建详情可用小回执种状态但摘要仍重读；删除移除该ID详情/items/queue；所有containing-track失效 |
| 改名 | list、该id detail失效；该id items/queue也更新revision或失效，避免用旧revision提交 |
| 加入/移除/恢复/移动 | list、该id detail/items所有页/queue、containing-track前缀失效；移动虽不改成员也统一处理，最多本人1,000歌单，不维护隐藏成员表 |
| 扫描终态/目录修正/归组刷新 | app沿已有catalog刷新协调同时使personal可见摘要/列表/队列失效；个人关系不重写；⑤catalog epoch变化再使艺人状态与第三栏失效 |
| 退出/401/换用户 | 代次变更，取消请求/动作回调、清理显示保留层/对话框/undo、旧key与幂等重试草稿不可用于新用户 |

每目标成功回执的revision不能被用来给整份旧lookup统一贴“已是新版本”：其他target未得到确认。取消尚在飞行的旧查询、结合本地mutation序号防迟到结果覆盖，仅确认该target；随后refetch完整响应收敛。

同一QueryClient里的跨页面同步靠上述前缀，**不误称它天然跨浏览器标签页**。建议app增加仅传 `{scope:"favorites"|"playlists"}` 的同源BroadcastChannel失效桥：不传user_id、收藏内容、认证或mutation载荷；接收方只失效自己的当前代次查询，不转发避免循环。旧浏览器/未连接标签页在focus时主动刷新personal前缀（不被30秒staleTime挡住）。它仅是刷新提示，数据库revision仍为并发权威，不新增跨用户数据共享或持久本地收藏。

### 9.3 乐观更新、失败回滚与保留行

- 心形按 `generation+kind+id` 串行，同目标跨组件共享进行中状态；pending时禁用同一目标的再次切换，避免PUT晚于DELETE抵达。onMutate取消相关旧query，记录**仅该target**之前的状态与本次序号，然后精确patch；失败只回滚仍属本次的patch，不能回滚整个缓存覆盖别的并发收藏。旁边role=status报错。
- 网络/503/invalid_response可能发生在提交之后：先按PRD把心形回到原显示并标“无法确认，正在刷新”，失效状态对账；用户主动重试必须保持同一PUT/DELETE意图，不能把不确定响应当成功后反转。等对账前不允许快速再次toggle。
- 收藏页取消保留行：页面本地保存当前已展示行的顺序/显示摘要作为**展示保留层**，被取消项留原槽、心形为false；服务器Query仍反映真实列表，不能往其items塞伪收藏。背景refetch/window focus/其他页失效不得清掉此保留层；同一页面显式刷新按钮、浏览器重载、离开该tab/page才丢弃。有保留行时背景新增不自动挤入当前行槽，显示“列表已更新，可刷新”；total仍来自服务器，可注明保留的已取消项不计入。成功点回同一ID解除取消标志，等刷新再按新的收藏时间排序。
- 歌单创建/删除/加入默认悲观，按钮pending且同意图保留幂等键，成功后提示精确计数；5xx/网络中断进入“结果待确认”，保留请求和同键重试入口。**重试不能重新取新revision或重建trackIds**；改请求就是新意图，先对账旧意图。
- 移动/移除可乐观更新当前页的显示序列，保留受影响页快照、revision与焦点；每歌单本标签页串行，其他页靠服务端409。失败回滚本次序列再重读，409提示“歌单已更新，请重试”，不自动把旧move套新revision提交。移除成功才展示server token；恢复是例外动作，只传token，不传expected_revision或客户端猜的邻居/位置，实际结果以服务端锁内适应规则为准。
- 撤销提示只展示该操作页面最近一次移除，但后续改名、移动、加入或别页删除不令已有token失效；点击restore不携带旧revision。按outcome提示“已恢复”或“已在歌单里”，按服务端最终position重新定位/必要时导航到所在页，不能在后台数据更新时把撤销按钮判为过期。临时超限显示over_limit_count并禁普通新增，不禁继续撤销、移动或移除。菜单四项可键盘操作，拖拽用已获准的@dnd-kit和触屏把手，本轮不安装依赖。

### 9.4 player插槽与全页面接入

player不导入personal/search/artists界面，也不从引擎调用个人数据API。app在既有渲染入口组合：

- `PlayerBar` additive `renderCurrentTrackActions(track)`（或同等ReactNode插槽）用于当前轨心形；没有当前轨不调用。app传入批量范围中的FavoriteButton，切曲只换目标、不复制身份状态。
- `PlayerQueue` additive `renderQueueActions(queueSnapshot)` 显示“存为歌单”，可选 `renderTrackActions(track)` 给每行心形/添加；NowPlaying透传相同插槽。app读取快照后给CreatePlaylistDialog传去重ID及本地日期默认名，player不解析歌单DTO。
- 专辑详情已有头部/行action插槽；头部心形目标group_id，添加目标当前选中release曲目。为整版一次lookup，catalog可新增纯 `renderReleaseScope(release, children)` 插槽包住头部及曲目区域，app放FavoriteStatusScope；catalog不导入其实现。
- 历史、收藏、歌单、④搜索结果、⑤艺人曲目页都由app对整页曲目做一次范围lookup，再传统一track-actions。个人页面的移除/排序等局部动作作为附加项传入同一菜单，不重复三个更多按钮。
- 播放适配统一沿 `playerTrackOfHistoryItem`：trackId/releaseId/groupId、当前有效title/artist回退、duration、artwork、六项source转camelCase；⑤ArtistRef与文本回退同步。不要复制四份converter，也不因metadata缺失把缺失曲目当可播。
- 心形`aria-pressed`、读屏名“收藏《标题》”、触屏目标与现有token一致；错误/成功在邻近role=status，无全局toast系统。专辑不可用/曲目missing只禁播放/详情，不禁个人移除与排序。


## 10. 性能预期与必须验证的 EXPLAIN

**本轮没有启动数据库、执行EXPLAIN或测出延迟；本节全部是实施验证要求，不是性能通过声明。** 基准至少10万首catalog、多用户干扰、本人2万曲目收藏（另设10万极端集）、大歌单10,000项、本人1,000歌单配额边界。包括大量曲目共享发行、每首不同发行、missing占多数、相同收藏时间、空/无封面组；不要只用十条数据验证索引。

| 查询/操作 | 预期计划与应检查的点 |
| --- | --- |
| 收藏首/中/深页与准确total | 按user时间索引范围取页，PK/index-only或bitmap取本人精确计数；total仍O(F_user)，OFFSET深页O(offset+size)，不得宣称O(page_size)。检查other-user过滤量、heap fetch、全表Seq Scan是否因夹具太小或统计缺失 |
| 收藏queue内部遍历 | 同一快照用用户时间索引keyset分批取ID，无OFFSET；完整遍历O(F_user)轻量事实得精确missing/省略数，只投影前10,000个present摘要。验证不重复COUNT整集合、不漏同时间UUID边界，不加载尾部完整摘要；测10万收藏下总事务时间、内存与取消 |
| lookup 10,000轨/1,000组 | `(user_id,target_id)`主键的批量ANY/join输入ID计划；索引访问或受限bitmap均可，不按ID发循环SQL。不能扫描其他用户、不能JOIN catalog；查0/1/100/10,000混合命中 |
| 歌单列表/配额 | user/updated索引取最多20行，配额计数仅本人≤1,000；并发999→1,000时配额锁等待与提交结果一致 |
| 歌单页/queue | items复合order索引按本人+歌单取顺序，页多一项边界；queue按批扫描全部N条轻量引用（普通N≤10,000，撤销可暂超），过滤missing、计全量省略数，仅补全前10,000个present摘要。位置计算允许O(N)，不声称常数 |
| 批量加入与反查 | unique(user,playlist,track)查存在，`(user,track,playlist)`找membership；检查1,000个歌单反查最多1,000行，避免遍历每个歌单做EXISTS |
| 移动/移到首尾 | 锁一条歌单行，邻居LIMIT 1走order索引，通常只UPDATE一条items；中点耗尽重整才O(N)写。记录rows affected、WAL/锁等待，不仅看SELECT速度 |
| 适应撤销/重复键 | undo按user/token定位；前驱track_id走items唯一索引，原序号回退按order索引定位，重整按实际N。并发锁后读取新快照；同键/已完成token只回放收据，记录写行数与超限计数，收据小于64KiB |
| catalog轻量facts与组摘要 | WHERE id ANY受限批次；组代表候选只来自所请求组，不全库求代表。测1,000曲目同发行/不同发行、10,000轻量facts和80组封面，查看重复聚合、子计划loops、JIT |
| 歌单卡片最坏页 | 普通20万本页引用及撤销超限集均按10,000轻量批次处理；统计连接往返、峰值内存、p50/p95和响应字节数，核对总数/超出数、未知时长/移动后封面；不截断超限数据 |
| ⑤ canonical聚合/刷新 | user范围后聚合再排序再LIMIT，精确distinct total；合并前后跨页计数一致。查看HashAggregate/Sort内存与spill，多锚点归一的大组；mapping批量更新走artist_id索引、不扫描所有用户全表 |

实施在隔离库运行实际应用SQL的 `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`，准备统计后测冷/热缓存，记录PostgreSQL版本、夹具规模、估算/实际行数、loops、共享buffer hit/read、排序内存/temp落盘、规划/执行/JIT耗时。索引命中只是证据之一，不用disable_seqscan或统一关闭JIT制造“通过”。历史设计已有1,000轨摘要的测量，可作风险提示，不拿其结果替本项验收。

写语句若用ANALYZE必须在**专用可回滚测试事务**，或只EXPLAIN不执行；并发锁测试使用两个独立连接、有界context/同步屏障，不靠sleep碰运气。请求取消后检查事务与advisory lock释放。完整HTTP测量还应包含JSON编码、网络响应大小、前端decoder/排序和DOM数量；10,000队列不是10,000行同时渲染的许可。

暂无个人页绝对毫秒SLA；先记录各规模p50/p95与最大响应/内存，再由主会话确定交互门槛。④搜索首屏指标不能偷换成本项承诺。若大歌单卡片实测成为主要瓶颈，优先落实 §7 轻量批次和有界页；引入跨模块统计缓存需单独评审可靠失效，不在本轮顺便扩scope。

## 11. 测试计划、接口样本与0 SKIP门槛

### 11.1 后端断言矩阵

| 组别 | 必须覆盖的断言 |
| --- | --- |
| 鉴权/隔离 | A/B/管理员两两读取与修改，只见本人；所有本人列表、lookup、queue、membership、undo、重放均覆盖；他人歌单/条目/锚点与随机ID同404。每个写方法及POST lookup同时构造跨源+未登录+坏JSON，依次断言403→401→400；GET先401再400；停用/会话撤销后重放也401 |
| schema/生命周期 | 关系唯一/跨owner复合FK/排序键重复被DB拒绝；track_count允许撤销后10,001及更多、不得有CHECK≤10000；前驱移除不删undo，曲目/组/艺人引用RESTRICT；用户删除级联state/favorites/playlists/items/undo/requests，停用保留；歌单删除不删收据；fresh/rerun/checksum及最终迁移编号验证 |
| 收藏幂等与分页 | 重复PUT原时间/版本不变，重复DELETE成功且不增版本；同目标并发只有一条；平局UUID、首/尾/越界页、各栏total；读revision变更409；missing→恢复、ID不变换发行/组读取当前位置、空组灰态与代表变更、不可用目标立即点回政策 |
| 收藏queue | 0/1/1,000/1,001/10,000/10,001个present候选，missing穿插于限量窗口前后；先过滤再取前10,000，省略数精确、同时间顺序稳定；一个快照内并发收藏/扫描不混版；旧队列已满仍替换成功，格式跳过与missing/omitted不重复计数，无可播/失败/取消/换身份保留旧队列；追加操作满额仍整批拒绝 |
| 歌单CRUD/配额 | Unicode名字100/101码点、仅空白、控制字符、可重名；999歌单并发创建只有一个达到1,000；创建含tracks原子性、不存在项全批回滚、同键重试不额外建空歌单；10000/10001不同轨、输入超长与业务超限区别 |
| 加入/移除/撤销 | 输入去重计数与原子性；无后续改动恢复原ID/键/时间；改名/移动/加入后仍恢复到前驱当前之后；前驱被另一标签移除、null或再加入换item ID时按track_id/原序号/末尾规则；已重新加入返回200 already_present且不移动；同键/同token双击不重复；保留已发凭证、失败不消费 |
| 排序性质 | 随机生成up/down/first/last/before序列与内存参考列表逐步比较；一项/空歌单、self-anchor、相邻no-op、跨页菜单、页末page_after_item_id、键距耗尽、负键、BIGINT边界重整；每步unique轨道、unique键、count和连续展示position一致 |
| 并发/revision | 普通写同revision仅一次真实修改成功，过期no-op仍409；restore不带expected_revision，后到也按最新状态成功，先到则令后续普通旧revision写409；满额→移除A→加入B→撤销A成功10,001，连续合法撤销可更高；超限普通新增拒绝、已存在no-op成功，移除至上限内恢复容量校验，所有失败不留下部分重排 |
| 幂等/故障 | 同用户同键同请求并发执行一次；同键不同命令/路由/排序数组/expected_revision冲突；不同用户同键独立；提交成功但丢响应后同键复放原status/body；删除后创建回放不复活；无效decoder/数据库故障后对账；取消释放锁、不留部分收据 |
| 查询边界与投影 | spy/集成计数确认无N+1、摘要分1000且顺序保留、每批rows关闭；合法不存在ID与missing分离；NULL/NaN/Inf时长、坏封面安全失败、不泄露路径；纯personal SQL不跨能力联表；依赖方向测试包含新包 |
| ⑤专项（由S线实现） | 收藏双方合并去重、聚合后分页total、MAX时间、旧IDlookup映射、等价PUT不添锚点、取消所有等价原锚点、split恢复旧锚点而不复制到新ID；CATP并发PUT/DELETE/合并/拆开、投影失败全回滚、按用户锁序及版本仅加一次、准备态不阻塞②③ |

### 11.2 接口样本清单

全部放在 `backend/internal/app/testdata/api-samples/`，每个JSON文件沿既有 `{status,body}` 外壳，由**真实app handler+合成隔离库**产生，规范化UUID/时间/request_id，不手写假服务端答案。建议新增独立 `personal_samples_integration_test.go` 的 `TestPostgreSQLPersonalAPIResponseSamples`，复用现有recorder，避免重生成无关全库样本。HTTP headers（no-store、Allow、Retry-After）另在handler测试断言，不假称样本外壳已包含它们。

下列文件均为待生成清单，本轮不创建：

```text
favorites/tracks.json
favorites/tracks-empty.json
favorites/tracks-missing.json
favorites/tracks-out-of-range.json
favorites/albums.json
favorites/albums-empty.json
favorites/albums-unavailable.json
favorites/track-put.json
favorites/track-put-repeated.json
favorites/track-delete.json
favorites/track-delete-repeated.json
favorites/album-put.json
favorites/album-delete.json
favorites/lookup.json
favorites/lookup-empty.json
favorites/queue.json
favorites/queue-empty.json
favorites/queue-missing.json
playlists/list.json
playlists/list-empty.json
playlists/list-unknown-duration.json
playlists/create.json
playlists/create-with-tracks.json
playlists/create-replayed.json
playlists/detail.json
playlists/detail-over-limit.json
playlists/rename.json
playlists/rename-noop.json
playlists/delete.json
playlists/delete-replayed.json
playlists/items.json
playlists/items-next-page.json
playlists/items-missing.json
playlists/items-empty.json
playlists/items-out-of-range.json
playlists/add-items.json
playlists/add-items-all-existing.json
playlists/add-items-input-duplicates.json
playlists/remove-item.json
playlists/restore-item.json
playlists/restore-after-previous.json
playlists/restore-original-index.json
playlists/restore-at-end.json
playlists/restore-already-present.json
playlists/restore-over-limit.json
playlists/move-before.json
playlists/move-up.json
playlists/move-down.json
playlists/move-first.json
playlists/move-last.json
playlists/move-noop.json
playlists/queue.json
playlists/queue-empty.json
playlists/queue-missing.json
playlists/containing-track.json
playlists/containing-track-empty.json
errors/personal-invalid-input.json
errors/personal-invalid-json.json
errors/personal-invalid-pagination.json
errors/personal-unauthorized.json
errors/personal-origin-required.json
errors/personal-origin-forbidden.json
errors/personal-not-found.json
errors/personal-method-not-allowed.json
errors/personal-revision-conflict.json
errors/personal-idempotency-conflict.json
errors/personal-favorites-changed.json
errors/personal-playlist-limit.json
errors/personal-playlist-track-limit.json
errors/personal-database-unavailable.json
```

⑤在同目录后续补：`favorites/artists.json`、`artists-merged.json`、`artists-unavailable.json`、`artist-put.json`、`artist-delete.json`、`lookup-artists.json`，及 `errors/artist-catalog-preparing.json`、`artist-catalog-not-ready.json`。合并/拆开完整变化过程另以真实集成测试证明，样本不能替代事务测试。

每个成功DTO和错误信封均由前端纯decoder读取样本往返；故意删必填/null数组/负计数/错误revision/非法资源ID的损坏样本只在前端测试里派生。10,000条完整摘要不必全部提交为巨型JSON：边界由生成式集成与前端队列测试验证，提交小型代表样本、超限歌单详情与恢复回执即可。正omitted_count场景由真实handler生成10,000项响应，在测试内交给decoder并断言计数，不必把巨型响应重复提交为golden；内部轻量分批不是外部cursor合同。新增样本生成模式只能针对明确命名测试，禁止顺带改写历史/catalog样本。

### 11.3 前端、浏览器与真实服务

- 单元/组件：DTO样本往返、查询键含全部输入/代次、10,000状态一次请求；跨组件心形同步、两目标并发精确回滚、超时对账、旧会话晚到响应、保留行被背景refetch后仍在、明确刷新后消失。
- 歌单：对话框单首membership、Unicode校验、原子新建加入、重复计数/同键重试；普通拖动/菜单冲突重读；恢复不因后续编辑失败，覆盖前驱/原序号/末尾、already_present、双标签移除与超限提示/禁新增/降回上限解禁；时长/封面包含临时超限的完整成员。
- app/player：slots无反向依赖、底栏切曲不串目标、保存队列保第一次；收藏与临时超限歌单先滤missing再取前10,000、校验omitted_count，旧队列满仍成功替换；unsupported仅统计窗口内且不补位，无可播/失败不清队列；next/enqueue剩余容量不足仍全批拒绝；跨标签失效不传播个人载荷。
- Playwright沿现有五宽度 **375/768/1024/1280/1440**，含千字标题、100字符名字、错误提示、空态/灰态；axe无违规，纯键盘完成收藏、取消、播放、新建、加入、四种移动、移除/撤销与删除确认；鼠标/触屏拖动及拖拽公告。1440/375截图包括拖动中、菜单、弹窗，不只空白页截图。
- 真实服务只用合成音频：收藏一首刷新仍在、取消后刷新消失；建歌单加两首、换序、刷新顺序保持；移除后另一标签改序仍可撤销、前驱消失回退、已重新加入的成功提示；两用户隔离、普通并发冲突与超限撤销/播放反馈。沿 `scripts/playback-e2e.sh` 传入新增real spec路径，仅跑相关用例，不碰真实音乐目录。

### 11.4 实施时的验证入口与0 SKIP

**以下是将来实施后的命令范围，不是本轮已执行记录。** 新测试名字/文件随实现建立，再显式核对匹配测试数，防止命令拼错而“0 tests通过”。

```bash
# 前置：由本次独占隔离测试启动器生成并验证 ROOMUSIC_TEST_DATABASE_URL，不能复用shell里来历不明的值。
# 在 backend 下，仅运行新增能力与相关公开合同（用 go test -json 保存输出并检查skip/run数量）。
go test -count=1 -json ./internal/personal ./internal/personal/httpapi
go test -count=1 -json ./internal/catalog -run 'Test(LoadTrackFacts|LoadReleaseGroupSummaries|LoadTrackSummaries)'
go test -count=1 -json ./internal/app -run 'Test(PackageDependencyDirections|PostgreSQLPersonalAPIResponseSamples|Personal)'
# 前端先跑具体相关文件，最终组合最小回归集合；不默认npm test全量。
cd ../frontend
npm run typecheck
npm test -- src/features/personal src/features/catalog/api/track-summary.test.ts src/app/personal.test.tsx
# 浏览器依赖须在同一个shell加载，real spec文件为实施时新增。
source /home/qlqq/.local/share/playwright-deps/env.sh
npm run test:e2e -- e2e/personal.spec.ts
cd ..
./scripts/playback-e2e.sh e2e/real/personal.spec.ts
```

SQL/Go集成启动器必须：检查 `roomusic-test` 无既有占用（有则停下，不接管/清理别人测试）；互斥锁；使用 `docker compose --env-file /dev/null -f /home/qlqq/workspace/ROOMusic/compose.test.yaml -p roomusic-test`；绑定空闲端口、随机库名、专用测试凭据；只把该URL传给测试；EXIT/INT/TERM清理本次资源并 `down -v`。不运行或连接 `roomusic-postgres-1`/`roomusic`，不读项目.env，不仅靠“不同schema”就称隔离。直接运行当前 `scripts/test-integration.sh` 会扩大全量范围且未体现本次全部环境约束，因此不作为本任务默认入口。

0 SKIP是验收条件：Go JSON中任何 `Action=skip`（包括子测试）即失败；每组应有预期命名test的run/pass、非零断言集；缺测试URL导致postgrestest.Skip不算通过。Playwright明确选中real项目与用例，reporter中skipped=0，缺数据库/音频依赖必须报告未完成，不能skip成绿。生成样本先在隔离库通过命名测试，更新模式再核查文件差异；迁移按主会话最终编号执行fresh/rerun/相关回归。只改文档的本轮不运行上述测试、不起服务。


## 12. 跨实现线接触点、已裁决与交付边界

### 12.1 P / S / F 线的责任划分

| 接触面 | P线（②③）负责 | S线（④⑤）接入/主会话集成 |
| --- | --- | --- |
| personal后端/前端 | 新personal包、两类收藏、歌单、状态/队列接口与DTO，通用FavoriteButton/AddToPlaylistDialog/范围查询 | ④不另建收藏API；⑤在同owner内追加artist文件、后续迁移、第三栏/decoder，不拷贝两套个人数据逻辑 |
| 搜索/艺人曲目行 | app层统一track-actions、单首和批量加入歌单合同 | 搜索结果、艺人各角色页按整页批量心形状态接入；缺失行为与②一致，不向player添加feature依赖 |
| catalog公共读取 | 提议轻量track facts、带availability的group summaries及公共TrackSummary纯decoder | ④⑤复用、additive扩展ArtistRef；主会话统一命名、nullability及摘要安全检查，不各造一份共享类型 |
| 艺人合并/拆开 | 接受 §8 的不可变原锚点、canonical归并、事务刷新owner端口 | ⑤建artists后用一个artist_favorites迁移接入；负责CATP公开入口、准备态、映射生成及治理回滚。app注入窄回调，catalog不反向依赖personal |
| app与player挂点 | 在app组合心形/加歌单/存队列，player只加中立slots | ④⑤再注入艺人链接与各页动作，不竞争替换整个PlayerBar/PlayerQueue；主会话合并slots签名 |
| ⑥扫描反馈 | 无业务依赖，不等⑥新表/路由/计数 | F线不读取个人表，不新增个人操作通知；仅已有扫描终态刷新机制在app通知personal重读摘要，不是跨线写事务 |

共享热点由主会话安排单owner或在P合入后变基：`backend/internal/app/app.go`、`dependency_rules_test.go`、catalog公开投影/新增读取、公共样本recorder；`frontend/src/app/router.tsx`、runtime/刷新接线、me-layout、album-detail-page/history-playback、PlayerBar/PlayerQueue/NowPlaying的slots、shared/api/request的方法词表、catalog公共decoder。P线新personal文件及personal样本优先独立提交；S线扩展尽量新建artist-favorites相关文件，不重排P整套exports/样本。

迁移只在本设计定 `favorites.sql`、`playlists.sql`、`artist_favorites.sql` 名字；**不在此抢占或承诺编号**。主会话按父任务合入顺序分配正式编号、重新跑迁移与集成。⑤只需加一张typed收藏表及派生列/索引，不需要改②两类收藏的结构或语义。艺人实体本身及其建表迁移仍由⑤设计负责，不能把“一个后续收藏迁移”误读成⑤只能有一个总迁移。

### 12.2 主会话已裁决（2026-11-04）

以下为主会话按“用户用起来方便、人性化”的明确决定，均已写入前面的数据、接口、前端和测试合同，不再列为待决：

| 项 | 已裁决结果 | 落地章节 |
| --- | --- | --- |
| D1：missing/不可用身份 | 已有稳定身份的曲目、专辑仍可收藏，误点取消后可立即点回；不开放不可见详情或媒体，不存在/无授权仍404 | §4/§5.1/§11 |
| D2：撤销定位 | 针对最近一次移除的有效凭证，总是按业务规则成功：无后续改动原位；有改动则原前一首当前之后；前驱不在按原序号，超过末尾放最后；已重新加入返回200 already_present，不报冲突、不重复添加 | §2.3/§3.2–3.4/§6.3/§9/§11 |
| D2补充：撤销与容量 | 主会话在容量边界追问中明确选择“撤销可临时超过10,000首，超限期间禁止新增”。不预留名额、不驱逐其他曲目；数据库/DTO允许真实超限数，只对普通新增校验上限，播放全部仍取最多10,000 | §2.3–2.4/§3.4/§4.2/§6.5/§11 |
| D3：播放全部 | 收藏与歌单都先过滤missing，按当前顺序取前10,000首替换队列；200响应含精确omitted_count等统计。旧队列满不阻止替换；格式不支持在返回窗口内另计；下一首/加入队列仍按剩余容量整批拒绝 | §5.3/§6.5/§7/§9–11 |
| D4：整张加入 | 取页面当前所选发行版本，按碟/轨顺序加入；专辑收藏身份仍是release group，不混合所有版本 | §6.3 |
| D5：收据保留 | 幂等收据保留至用户删除；体量靠小回执、监测、物理维护，必要时透明归档完整去重依据与原响应，不TTL删除活用户旧键 | §2.3/§3.3/§3.5 |
| D6：大歌单卡片 | 实现时实测普通满额和撤销临时超限场景；轻量批处理保持精确统计，不承诺未测SLA，不偷截统计范围 | §7.2/§10/§11 |

收藏PRD中“播放全部”默认已更新；A5若仍沿用“队列满不截断”的旧合并表述，按本次裁决仅适用于追加，不用于播放全部。歌单普通上限与并发规则也不能抵消本次明确的restore例外。本轮按要求不编辑PRD，主会话后续同步文字即可，不是重新打开产品决定。

稳定分页、前端同代次失效、跨标签无个人载荷的刷新桥、前四不同专辑封面及⑤原锚点合同保持不变。此前D1/D2已获得明确裁决，不再要求主会话重复确认；后续性能测量和共享文件装配是执行工作，不是未决产品问题。

### 12.3 本轮交付与规划选择记录

- **目标/范围**：完成②③技术设计，覆盖模块、schema、全部请求/响应、事务与重试、分页/排序/播放、前端状态/插槽、⑤艺人接入、性能与测试样本；不做实现。
- **范围外**：分享/协作、文件夹、标签、导入导出、智能歌单、同歌单重复曲目、自定义封面/描述、收藏推荐统计、⑥业务改动；不增加通用治理审批或本地持久个人数据。
- **验收映射**：两PRD的A1→§4/§11隔离；收藏A2–A5→§2/§5/§9；歌单A2–A5→§3/§6/§9；双方A6→§10/§11。⑤附加合同→§8。
- **规划路线**：人工选择。选择者为主会话（代表用户），2026-11-04已明确裁决§12.2各项及撤销临时超限边界；本轮选择结果是**采用这些已裁决规则并完成文档修订**。理由是误点可恢复、并发后可预测恢复、播放全部有可用结果与准确省略提示，同时保留授权和普通新增配额。裁决文档不等于授权本轮改代码，仍不启动实施。
- **产物状态**：本轮仅写本design.md，不修改定稿PRD、implement.md/jsonl、spec、代码、迁移或task状态；主会话负责后续执行计划、PRD文字同步、上下文与启动门槛。并未因文档存在而运行task.py start。
- **验证状态**：只做文档结构、JSON示例、术语/路由与所引源码合同核对、空白错误检查；未启动数据库、未跑应用测试/EXPLAIN、未提交git。任何实施性能、0 SKIP和真实服务结论均待按§11取得证据。

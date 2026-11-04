# 本人收藏与歌单合同（M5 P 线）

## 1. 范围与触发

`backend/internal/personal` 拥有本人曲目/专辑收藏与歌单；`personal/httpapi` 只做守卫、严格输入校验和呈现。变更个人关系、排序、撤销、重试、分页、队列或个人摘要时先读本合同。产品裁决以[设计 §12](../../tasks/11-04-classical-m5-favorites/design.md)为准。

- 不扩大 listening，不改变播放历史的 1,000 候选队列，不调用 `operations.Run`。
- SQL 仅访问本人收藏、歌单、条目、撤销和收据表；不 JOIN catalog/identity 的私表。目录读取由消费端 `personal.Catalog` 端口提供，同一用例事务使用 `catalog.NewStore(tx)`。
- core 只依赖 `catalog`、`platform/uuid`；HTTP 只依赖 `identity`、`personal`、`platform/httpserver`、`platform/uuid`，app 组合。精确白名单由 `TestPackageDependencyDirections` 验证。
- 艺人收藏由 S 线后续接入：新增 typed 表，沿用 `personal_favorite_state`；不预建 artists 表，不把原锚点改成 canonical 身份。后续 CATP→本人 state 的锁序、事务内派生刷新和准备态遵守设计 §8。

## 2. 签名与数据权威

### 2.1 应用端口

```go
personal.NewService(*sql.DB) *personal.Service
SetFavorite(ctx, userID, kind, targetID string, favorited bool) (FavoriteResult, error)
FavoriteTracks(ctx, userID string, page Page) (FavoriteTracksPage, error)
FavoriteAlbums(ctx, userID string, page Page) (FavoriteAlbumsPage, error)
LookupFavorites(ctx, userID string, trackIDs, groupIDs []string) (FavoriteLookup, error)
FavoriteQueue(ctx, userID string) (FavoriteQueue, error)
Playlists(ctx, userID string, page Page) (PlaylistsPage, error)
Playlist(ctx, userID, playlistID string) (PlaylistDetail, error)
PlaylistItems(ctx, userID, playlistID string, page Page) (PlaylistItemsPage, error)
PlaylistQueue(ctx, userID, playlistID string) (PlaylistQueue, error)
ContainingTrack(ctx, userID, trackID string) (ContainingTrackResult, error)
Execute(ctx, userID, idempotencyKey string, command Command) (Result, error)
```

`Page={Number,Size int; Revision *int64}`；分页返回 `{page,page_size,total}`。`Command.Kind` 为 `create/rename/delete/add_items/remove_item/restore_item/move_item`；`Result.Status` 与 `Body json.RawMessage` 是首次成功回执，重放原样返回，不重新查当前目录或重新执行业务。HTTP 不向应用传入角色，也不接受用户 ID 字段。

### 2.2 迁移与生命周期

本分支迁移为 `0021_favorites.sql`、`0022_playlists.sql`；主会话合入时可以按合并顺序改号，不编辑已部署迁移。

- 收藏三表：`personal_favorite_state`、`favorite_tracks`、`favorite_albums`。typed FK 引用 tracks/release_groups，`ON DELETE RESTRICT`；按 user/target 唯一，按 user/created_at DESC/UUID ASC 分页，目标单列索引用于 FK 检查。专辑首次收藏保存安全标题/艺人回退。
- 歌单四表：`personal_playlists`、`personal_playlist_items`、`personal_playlist_requests`、`personal_playlist_undo`。owner 复合 FK 防串用户；条目曲目唯一，BIGINT 排序键唯一约束可延迟；条目 id/added_at 不随排序改变。
- 歌单 `revision` 初值 1、收藏集合初值 0，最大 `9007199254740991`。实际变化才递增；越界失败并回滚，不回绕。
- `track_count` 是本事务维护的实际条目数，增删后验证与 COUNT 一致。允许合法撤销临时超过 10,000，不加 `CHECK <= 10000` 或 `<=10001`。总时长/封面不持久化到权威行。
- 用户物理删除级联全部个人表；停用保留。歌单删除级联 items/undo，**不删除成功收据**。requests 的 playlist_id 故意无歌单 FK。
- 收据含 SHA-256 指纹、版本 1、原 200/201 状态、对象 JSONB（≤64 KiB），保留到用户删除，无 TTL。undo token 保留到歌单删除；`restore_request_key` 用同 owner 的延迟 FK 指向首次恢复收据。

## 3. REST 与事务合同

### 3.1 守卫和严格 JSON

所有路径前缀 `/api/v1`。GET 先会话，匹配的写方法（含 POST lookup）先同源→会话→输入→业务；身份查库失败沿现有 guard 为 401，业务查库失败 503。管理员没有个人数据越权分支。响应 `Cache-Control: no-store`，数组非 null，nullable 字段必须输出 null，日期 UTC RFC3339。

personal 局部解码器限制 1 MiB、合法 UTF-8、单个 JSON 对象、拒绝重复键（含转义后相同键）、未知字段、尾随 JSON、null 代替必填。无 body 的心形允许空或 `{}`；GET 不解 body，查询键 allowlist 拒绝未知/重复/空项。UUID 校验后小写；名字 trim 后 1–100 Unicode 码点且无控制字符；不按 UTF-8 字节计数。

静态 `tracks/queue` 和 `containing-track/{track_id}` 使用局部子 mux 隔离方法/通配符冲突，不改变任何外部路径。各子 mux 复用 `APINotFoundHandler`，方法错误仍为 405、统一 JSON、准确 Allow。平台完成日志在请求结束时读 ServeMux 的实际 `Request.Pattern`，保留叶子模板，不记录私人 UUID/原始 URL；fallback 映射 `<unmatched>`。

### 3.2 收藏

| 方法和路径 | 输入与完整响应形状 |
| --- | --- |
| GET `/me/favorites/tracks` | page 默认1/page_size默认50最大100/可选 revision；`{items:[{track:TrackSummary,created_at}],pagination,revision}` |
| GET `/me/favorites/albums` | 同上；`{items:[{album:PersonalAlbum,created_at}],pagination,revision}` |
| PUT/DELETE `/me/favorites/tracks/{track_id}` | `{target_kind:"track",target_id,favorited,created_at,revision}` |
| PUT/DELETE `/me/favorites/albums/{group_id}` | `{target_kind:"album",target_id,favorited,created_at,revision}` |
| POST `/me/favorites/lookup` | `{track_ids?:UUID[],group_ids?:UUID[]}` → `{revision,tracks:[{track_id,favorited}],albums:[{group_id,favorited}]}` |
| GET `/me/favorites/tracks/queue` | 无查询 → 队列计数及 `revision` |

PUT/DELETE 不需幂等键。READ COMMITTED 事务先确保本人 state，再锁 state；已有 PUT 保留原收藏时间/版本，重复 DELETE 成功且不增版本。DELETE 不查目录；未知合法 UUID 也返回 false。PUT 新关系经 catalog 验证稳定身份，missing 曲目或无可见版本的既有组仍可收藏；真正不存在 404。不可用组不泄露隐藏发行，`PersonalAlbum={group_id,available,title,artist,release_count,representative}`，不可用时 count=0/representative=null，优先保存名称，无保存值用“暂时不可用的专辑”。

两栏 `created_at DESC, UUID ASC`，同一只读 REPEATABLE READ 快照读 revision、total、关系、目录摘要；携带旧 revision 为 409 favorites_changed。空/越界页 200，保留 total。lookup 原数组最多10,000轨/1,000组，省略为 []、显式 null 拒绝；去重保留首次顺序，合法未知 ID 为 false；只读个人主键关系，不补目录摘要。

### 3.3 歌单

| 方法和路径 | 请求 | 成功 |
| --- | --- | --- |
| GET `/me/playlists` | page/page_size，默认及最大20 | `{items:PlaylistSummary[],pagination}` |
| POST `/me/playlists` | `{name,track_ids?}` | 201 `{playlist:PlaylistState,added_count,existing_count,duplicate_count}` |
| GET `/me/playlists/{playlist_id}` | 无查询 | `{playlist:PlaylistSummary}` |
| PATCH 同路径 | `{expected_revision,name}` | `{playlist,changed}` |
| DELETE 同路径 | `{expected_revision}` | `{playlist_id,deleted:true}` |
| GET `.../{playlist_id}/items` | page/page_size 默认50最大100/可选revision | `{playlist,items:PlaylistItem[],pagination,page_after_item_id:UUID或null}` |
| POST `.../{playlist_id}/items` | `{expected_revision,track_ids}` | `{playlist,added_count,existing_count,duplicate_count}` |
| DELETE `.../{playlist_id}/items/{item_id}` | `{expected_revision}` | `{playlist,removed:ItemRef,undo:{token,removal_revision}}` |
| POST `.../{playlist_id}/restore` | `{token}` | `{playlist,outcome,item:ItemRef,placement}` |
| POST `.../{playlist_id}/items/{item_id}/move` | `{expected_revision,action,before_item_id?}` | `{playlist,moved:ItemRef,changed}` |
| GET `.../{playlist_id}/queue` | 无查询 | `{playlist,...队列计数}`，不另返 revision |
| GET `/me/playlists/containing-track/{track_id}` | 无查询 | `{track_id,items:[{playlist_id,revision}]}`，最多本人1000个 |

`PlaylistState={id,name,revision,track_count,track_limit:10000,over_limit_count,created_at,updated_at}`；`ItemRef={id,track_id,position,added_at}`；`PlaylistItem={id,position,added_at,track:TrackSummary}`。页边界多读一行获得 `page_after_item_id`；position 是全歌单1-based序号，不接受写入 order_key/position。

`PlaylistSummary` 在 State 上加 `duration_seconds,unknown_duration_count,missing_count,cover_groups:[{group_id,artwork}]`。统计包含 missing 上次的正有限时长；未知单独计数。封面是当前顺序前四个不同组，含无封面组，不跳过以凑四张。每页最多20张，按10,000轻量引用批次读取全部成员，最多80组一次补封面，不为20万条目加载完整摘要。扫描/归组后自然读当前事实，不递增用户 revision。每批按歌单切分，调用 catalog `LoadTrackCollectionStats` 聚合计数/时长，逐批核对 `MatchedCount`；不传输整个批次逐曲事实。封面只对尚不足四组的歌单按顺序补 facts：从32项窗口起、倍增至批内最多10,000，遇到四个不同组即停止；missing、无封面、不可用组都占自己的顺位。跨批保留已发现组与统计，超限成员仍完整累计；不得用 GROUP BY/window 全量排序来找这四组。

### 3.4 稳定重试、配额和排序

- 每个歌单写请求必须 UUID `Idempotency-Key`。指纹含 schema_version、命令、路径目标、适用 revision、规范化完整参数；曲目数组保留顺序与输入重复项。创建缺省/空数组一致；名字和 UUID 规范化一致。
- READ COMMITTED 锁序：`PKEY(0x504b4559,user+key)`→创建/删除时 `PQUO(0x5051554f,user)`→本人歌单行→条目/undo。第二整数取 SHA-256 前4字节，碰撞只串行，不代替完整主键/指纹检查。与 OPER/ROOT/CATP/迁移互不共用空间。
- 等幂等锁后**新语句**查收据，重放在资源存在性/revision 之前。同键异义409；成功修改和小收据同事务提交。提交结果不明只能同键同请求对账，不自动换键重做。首响应从写入后的 JSONB 取回，和重放同字节。
- 普通写锁后先比较 expected_revision，即使 no-op 也不能绕过；创建与 restore 是例外。有效 no-op 不增版本、不改更新时间。改名、移除、移动保持单次用户动作只增一次。
- 用户≤1,000歌单，创建/删除取本人配额锁，创建锁后重新计数。创建带 tracks 为一次原子事务。普通加入原数组1..10,000，去重后存在项计 existing、同批重复计 duplicate、真正新增计 added；三者之和=原数组长。全已存在成功无变化；不存在目标整批404；真正新增超过容量整批409，不截断。
- BIGINT 初始步长 `2^32`。前后邻居索引定位，中点/边缘通常只改一项；无间隙/边界溢出时锁内统一重整，步长 `min(2^32,MAX_INT64/(N+1))`，唯一约束仅该事务临时 deferred。重整先在来源CTE按user/playlist限定并MATERIALIZED合法主键集合，再以全局主键驱动UPDATE；不要在目标端重复owner常量或owner索引连接，fresh无统计时会错选Nested Loop逐项重扫整歌单。来源集合就是授权边界，回归必须确认其他歌单不变和fresh/analyzed无超线性重复过滤。不能常规加载/重写全歌单来移动一项。
- action=up/down/first/last 不许 before_item_id；action=before 必须出现 before_item_id，UUID 为锚点、null 为全歌单末尾。同项/相邻/端点 no-op 成功；跨页菜单定位全序列。

### 3.5 有效撤销总是按当前状态恢复

移除保存原 item_id/track_id/order_key/added_at、原序号、原前驱 track_id 和移除后的 revision。凭证不因改名、加入、其他移除或移动失效，无短 TTL。

1. token 必须属于本人和该歌单，否则404；歌单删除或基础设施故障仍按正常404/503。
2. token 已完成恢复：返回首次成功收据；新幂等键也保存同一成功结果，不二次插入。
3. 曲目已重新加入：200 `already_present/unchanged`，保留当前位置/身份/时间/版本。
4. 当前 revision 等于 removal_revision：原 item_id/key/time 恢复，`placement=original`。
5. 此后有改动：前驱 track_id 仍在则插其当前位置之后（即使已换 item_id），`after_previous`；否则按 min(原序号,N+1) 恢复，`original_index`，原序号越过末尾为 `end`。
6. 实际恢复重用原身份/added_at、重算间隔键，count/revision+1。**豁免普通加入配额**，可到10001及更高，返回真实 over_limit_count，不删其他成员、不预留容量。
7. 记录首次恢复收据与 token 引用同事务提交；失败回滚不消费 token。超限期仍允许排序、改名、移除、继续撤销和全已存在的加入。

### 3.6 队列与快照

收藏和歌单播放全部都在单次 HTTP/单个只读 REPEATABLE READ 中遍历全部引用；每批≤10,000轻量 facts，先过滤 missing，只保留前10,000 present ID，再每1,000补完整 TrackSummary。强 FK 引用缺 facts/摘要是503，不是跳过。单连接上读完并关闭 rows 才发下一查询。延迟敏感个人只读事务开始后执行 `SET LOCAL jit = off`，失败回滚并返回查询错误；所有写事务和连接默认设置保持原样，生命周期/失败测试遵循[数据库规范](./database-guidelines.md#延迟敏感只读事务与-jit)。

收藏续批采用两个索引 seek：同一 created_at 的较大 UUID，再取更早 created_at；避免混向复合索引的 OR 条件从头过滤。SQL 输出 `id::text` 时 ORDER BY 必须限定原表 UUID 列，不能意外按 text 别名排序。

队列固定字段：`items,total,missing_count,eligible_count,returned_count,omitted_count,limit:10000,truncated`。恒等式：total=missing+eligible；returned=items.length=min(eligible,limit)；omitted=eligible-returned；truncated=(omitted>0)。收藏另有 revision，歌单另有 playlist。超限歌单也遍历全体，不把省略数写死0。

服务端不推断浏览器格式能力；前端对返回窗口再过滤格式不支持，不从窗口外补位。播放全部替换旧队列，旧队列已满不阻止；下一首/加入队列仍按剩余容量整批拒绝，这是 player 的独立合同。

## 4. 校验与错误矩阵

信封沿平台：`{error:{code,message},request_id}`，request_id 在顶层。不得返回 SQL、路径、Cookie、收据原文或跨用户计数。

| 状态 | code | 条件 |
| --- | --- | --- |
| 400 | invalid_input | UUID/必填/类型/null/重复键/未知字段/动作/名字/数组超长/非法revision |
| 400 | invalid_json | 非法 JSON、无效UTF-8、尾随内容、超过1MiB |
| 400 | invalid_pagination | 页码、大小、偏移非法 |
| 401 | unauthorized | 未登录、失效、停用、鉴权查询失败 |
| 403 | origin_required/origin_forbidden | 写请求缺来源或跨源；先于鉴权/输入 |
| 404 | not_found | 目标不存在或不属本人/同歌单；不得区分 |
| 405 | method_not_allowed | 正确 Allow，静态queue/反查不可被通配符吞掉 |
| 409 | favorites_changed/revision_conflict | 收藏版本变化/普通歌单写或分页版本变化；restore不用旧revision |
| 409 | idempotency_conflict | 同用户同键不同完整意图 |
| 409 | playlist_limit_reached/playlist_track_limit_reached | 普通新增配额，不用于有效撤销 |
| 503 | database_unavailable | 事务、投影、完整性或安全整数耗尽；安全失败无部分成功 |

## 5. 正确、边界与错误用例

- 收藏 missing→取消→再次 PUT：都允许；不存在身份 PUT404，未知合法 DELETE200 false。
- `[A,B,B]`，A原已在歌单：added=1/existing=1/duplicate=1；输入重复不能冒充此前存在。
- 10000首移除A、加入B、撤销A：10001首成功；继续有效撤销可以10002；新C拒绝，全部重复加入成功。
- 同键创建后删除歌单，再重放创建：原201回执，数据库不复活；新键删除不存在歌单404。
- 移除第二首后将其前驱移到末尾，再撤销：恢复到前驱当前之后，不409、不回旧索引。
- 12006收藏、2001 missing：eligible=10005，returned=10000，omitted=5；不能截原始前10000再过滤。

## 6. 必需测试与证据

- `personal`：收藏幂等/并发、稳定分页/快照、批量lookup不访问catalog、两类不可用身份、队列边界和完整计数、跨owner隔离；歌单原子创建、输入计数、过期no-op、随机排序参考序列、键耗尽/负值/BIGINT边界重整、全部撤销分支、重复token、持续超限恢复、配额竞争、幂等锁等待/取消、生命周期、迁移fresh/rerun。
- `personal/httpapi` 与 `app`：每个写方法403→401→400、未知/重复/尾JSON、静态路由405/Allow、全部70份设计§11.2真实 handler 样本、重放字节稳定、安全503与完整计数。样本目录 `app/testdata/api-samples/favorites/`、`playlists/`、`errors/personal-*`。
- 样本套件各管范围：旧 recorder 的清理/对比跳过上述personal范围，personal recorder只管理自己70个；更新任一套不能删除另一套。
- catalog 新读测试：10,000 facts/1,000组上限、missing/空组、当前归属、无效时长、安全封面、单连接/重复ID。
- 依赖规则和race/vet按本任务指定范围；集成必须实际运行，0 SKIP。性能诊断启用 `ROOMUSIC_CATALOG_EXPLAIN=1`，不能把无URL的Skip当通过。
- 数据库仅隔离 Compose 项目 `roomusic-test-m5p`，空闲端口、随机库名、`--env-file /dev/null`，本次URL传测试，用完 `down -v`；不得操作业务数据库或其他实现线项目。具体实测证据见[个人性能报告](../../tasks/11-04-classical-m5-favorites/research/m5-personal-performance.md)和[Catalog 执行计划报告](../../tasks/11-04-classical-m5-favorites/research/m5-personal-catalog-explain.md)。

## 7. 错误做法与正确做法

- 错误：handler 联表 tracks 算可播/时长；正确：个人关系分页后通过同事务 catalog 窄端口批量读事实。
- 错误：后到 restore 比 expected_revision 返回409；正确：token 证明原移除事实，在最新锁内适应位置恢复。
- 错误：收据按30天TTL删、删除歌单顺带删；正确：保留完整成功去重依据至用户删除，未来归档也必须透明重放。
- 错误：只读取10001项把 omitted猜成1；正确：轻量遍历全体精确计数，只保留10,000完整摘要窗口。
- 错误：`SELECT id::text ... ORDER BY id` 意外对文本排序；正确：限定权威 UUID 列，EXPLAIN 验证实际索引和排序节点。

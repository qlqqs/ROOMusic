# 目录批量摘要合同

从 Catalog REST 合同的第2节原样拆出，维护历史、收藏与歌单的公共读取端口；HTTP、权限与其余读取仍见[Catalog REST 合同](./catalog-rest-contracts.md)。

## 公共签名与边界

- 播放历史（阶段 P / M5）只经 catalog 的三个公开读取函数使用目录：`Store.LoadTrackDuration`（present 曲目的时长，未知为 0）与
  `Store.LoadTrackPlacements`（present 曲目的标题、组与所在发行摘要，摘要与列表同一个投影），以及 M5 的
  `Store.LoadTrackSummaries(ctx, trackIDs) (map[string]TrackSummary, error)`（**含 missing** 的标题、艺人、present、时长、当前组/发行与安全音频事实），不读 catalog 私有表。
  新函数无效 UUID 跳过、小写去重，空输入不查库，最多 1,000 个不同有效 ID，超限错误，不存在者无键；旧 Placements 仍仅返回 present，不改概览语义。
  `TrackSummary` 为 `TrackID,Title,Artist,GroupID string; Present bool; DurationSeconds *float64; Release ReleaseSummary; Source TrackAudioFacts`；
  `TrackAudioFacts` 为 `SourceKind string; Codec *string; BitDepth,SampleRate,Channels,Bitrate *int`，wire 名称见[播放历史合同](./listening-contracts.md)。
  时长未知/非正/非有限为 null，codec 空白为 null，音频整数未知/非正为 null；source_kind 非空，不返回路径、root ID、指纹或文件时间。
  曲目与按不同发行物化的摘要 CTE 同一语句快照，每发行只算一次摘要，复用有效值来源和封面安全投影；不查文件系统。
  M5 批量优化把请求所涉发行的 present 介质数、present 曲目数与未核对缺失来源数合并为一次分组聚合，不再每个发行重复三次计数。
  `releasesql.BatchSummaryCountsCTE` 只消费本批去重的 `summary_release_ids`；`BatchSummaryProjection` 与单发行投影共用有效值/封面列，
  缺失来源去重及核对依据表达式、字段/归组 attention 规则沿用原实现。两种投影仍由同一 `ReleaseSummaryColumns` 解释，不改外形、可见性、上限或完整摘要计数。
  **缺失摘要例外**：历史保留 missing 曲目的当前发行/组和上次扫描事实，即使该发行全部 missing、present 计数为 0；只在此批量函数不加发行可见性谓词，
  不扩大列表/详情可见性（详情仍可能 404）。缺少历史引用对象或安全投影失败时 listening 整体 503，不静默丢项。
- 收藏与歌单（M5）的新增公开批量读取位于 `personal_reads.go`、`track_collection_stats.go`，消费者不联表 catalog 私表：
  - `Store.LoadTrackCollectionStats(ctx context.Context, trackIDs []string) (TrackCollectionStats, error)`：至多 10,000 个不同有效 UUID；与 facts 相同地跳过无效 ID、小写去重，空输入不查库，超限先失败。返回 `MatchedCount,MissingCount,UnknownDurationCount int64; KnownDurationSeconds float64`，不存在身份不计入匹配数，调用方与强引用数核对并 fail closed。missing 保留上次正有限时长；NULL/非正/NaN/±Inf 计未知，累计溢出报错。仅对所请求 tracks 做一条计数/时长聚合，依靠既有强 FK 保证目录身份完整，不加载逐曲行、发行摘要或封面；支持 `NewStore(tx)` 共享调用方快照，无跨请求缓存。使用 PostgreSQL DOUBLE PRECISION 聚合，数值等价测试对小数采用容差，不承诺与逐项 Go 累加逐位相同。
  - `Store.LoadTrackFacts(ctx context.Context, trackIDs []string) (map[string]TrackFact, error)`：至多 10,000 个不同有效 UUID；
    `TrackFact` 为 `TrackID,ReleaseID,GroupID string; Present bool; DurationSeconds *float64`，含 missing，读取当前发行/组。
    时长 NULL、非正、NaN、正负无穷均为 nil；不计算完整发行摘要，不读取封面、路径或存储身份。
  - `Store.LoadReleaseGroupSummaries(ctx context.Context, groupIDs []string) (map[string]PersonalReleaseGroupSummary, error)`：至多 1,000 个不同有效 UUID；
    包装为 `GroupID string; Available bool; ReleaseCount int; Representative *ReleaseSummary`。存在但无可见成员的组仍有键，
    对应 `false / 0 / nil`；真正不存在者无键。此包装不改变既有 `ReleaseGroupSummary.Representative` 非空合同，也不开放不可见发行详情。
  - 上述 facts 与 group 两者均沿既有批量摘要约定跳过无效 UUID、小写去重；HTTP/消费端仍必须自行拒绝非法用户输入。空输入返回非 nil 空 map、不查库，超限在查询前报错。
    每次各一条 SQL、同一语句快照，支持 `NewStore(tx)` 参加调用者的事务；不自行开启事务，不逐轨/逐组查详情，返回前关闭 rows 并检查迭代错误。
  - 组的候选排名只针对请求组，复用 `ReleaseVisiblePredicate`、`ReleaseGroupMemberOrder`，只给可见代表计算
    `BatchSummaryProjection`，由 `ReleaseSummaryColumns` 校验有效值与安全封面。`ReleaseCount` 是完整可见成员数，不套组详情的 100 成员截断。
    不可用组不泄露隐藏成员或坏封面；可见代表封面损坏则整批失败，不返回部分结果。查询执行失败保留 `ErrQuery` 与原始错误链；上层负责安全错误信封。

## M5 艺人引用

上述 ReleaseSummary 增加 `artist_refs/album_artist_refs`，TrackSummary 增加 `artist_refs`；均为必传数组。形状与就绪/快照规则见[艺人合同](./artist-contracts.md)。`releasesql.BatchSummaryArtistRefsCTE` 与 `SelectedTrackArtistRefsCTE` 仅聚合本次ID集合，同一SQL检查代次并加载有效关联，不能按字符串猜身份。各数组最多100项，读101项用于检测超限，超限或profile损坏整批失败。


## 成员批量摘要HTTP入口（I4）

`POST /api/v1/tracks/summaries`：同源校验沿POST读接口，已登录成员可调用（不要求管理员）。请求必须为`{track_ids:[UUID...]}`，逐项拒绝非法UUID；小写去重后1–1000个，空/null/超限/错误形状为400 `invalid_input`，JSON语法错误为400 `invalid_json`。响应`{items:[TrackSummary]}`保持首次输入顺序，未知ID省略，missing保留并标记`present=false`；不暴露路径、root、指纹等私有事实。HTTP只校验和重排，直接复用`LoadTrackSummaries`，不复制SQL或逐发行读详情。artist_refs在未ready时仍为空数组，不把艺人整理误报为摘要不可用；真正查询失败为503 `database_unavailable`。

回归覆盖同源/跨源/缺Origin、401、普通成员成功、0/1000/1001个去重ID、重复大小写及顺序、未知/missing、refs、坏投影安全失败。样本由真实app handler生成到`testdata/api-samples/artists/adjudication/`，不是手写DTO。

## 资料库统计接口（2026-11-08）

`GET /api/v1/catalog/stats`，注册在 `catalog/httpapi`，所有登录用户只读：401 `unauthorized` → 503 `database_unavailable` → 200；`Cache-Control: no-store`。
实现 `catalog.ReadCatalogStats` 在一个只读可重复读事务里读完四个数，口径与各列表页一致：

| 字段 | 口径 |
| --- | --- |
| `albums` | 与 `GET /release-groups` 不带筛选的 `pagination.total` 相同：`WITH releaseGroupRankedCTE(false) SELECT COUNT(*) FROM ranked WHERE member_rank = 1` |
| `tracks` | `count(*) FROM tracks WHERE source_status = 'present'`：跨版本不去重，CUE 虚拟轨也算（present 曲目所在发行必然可见） |
| `artists` | 与 `GET /artists` 不带筛选的 `total` 相同：`ListArtists` 与本接口共用 `Store.artistListCount(ctx, query, initial, role)`，此处传空参数 |
| `composers` | 同上，`role = 'composer'`，等于 `/artists?role=composer` 的总数 |

艺人数据未就绪（`ErrArtistCatalogPreparing`）时 `artists`、`composers` 为 null，不报错。样本 `internal/app/testdata/api-samples/catalog/stats.json` 与 `stats-artists-preparing.json`；
测试 `go test ./internal/app/ -run 'TestPostgreSQL(CatalogStats|APIResponseSamples)' -count=1`（四个数与对应列表总数相等、艺人未就绪为 null）。

# M5 ① 历史页面：后端接口合同（设计稿）

2026-11-04。按 `trellis-brainstorm` 核对代码；遵循本任务 PRD 的按曲目汇总、最近播放排序及 A1–A8，父任务的用户隔离要求见 `.trellis/tasks/11-04-classical-m5/prd.md:33-37`。本轮只设计，不实施；不做次数排序、按天时间线、共享治理或播放时版本快照。下述为新增合同，已有记录/最近发行/清除接口兼容保留。

## 1. 接口、数据与错误

只取当前会话用户，管理员也不越权；GET 仅鉴权，DELETE 先同源→鉴权→参数；handler 不写 SQL。沿用 `no-store` 和 `{error:{code,message,request_id}}`，不泄露路径/SQL（`backend/internal/listening/httpapi/handlers.go:43-112`；`backend/internal/platform/httpserver/response.go:21-39`）。

| 路径 | 成功响应（200）与规则 |
| --- | --- |
| `GET /api/v1/me/plays/tracks` | `{items:HistoryItem[], pagination:{page,page_size,total}, has_history}`。默认 `page=1,page_size=50`，页大小 1–100；total 是本人不同曲目数，含 missing，`has_history = total > 0`。超末页返回原页码、真实 total 与 `items:[]`。 |
| `GET /api/v1/me/plays/queue` | `{items:HistoryItem[],total,limit:1000,truncated,has_history}`。无业务参数，忽略额外查询参数；固定返回同序最新最多 1,000 个候选，`truncated = total > 1000`，不受当前页影响。过滤口径见下文待确认项。 |
| `DELETE /api/v1/me/plays/tracks/{track_id}` | 无请求体、无需幂等键；`{deleted:N}`，N 为删掉的播放记录数。合法 UUID 不存在、missing 或本人无记录均允许；无记录为 0，不查 catalog、不报 404。 |

分页复用 `ParsePagination(request,50,100)`：空/缺省用默认、page≥1、整数解析和偏移乘法防溢出，非法为 `400 invalid_pagination`，重复参数处理也沿用它。不开放排序参数。**扫描默认 20，操作记录默认 50**，这里选后者；两者上限均 100（`backend/internal/platform/httpserver/pagination.go:9-30`；`backend/internal/library/httpapi/scans.go:38-61`；`backend/internal/operations/httpapi/handlers.go:21-23,85-88,152-155`）。

**完整条目**（所有字段必传；`?` 表示可为 null，而非可省略）：

```text
HistoryItem = {track_id:UUID, last_played_at:UTC RFC3339, play_count:正整数,
  present:boolean, title:string, artist:string, duration_seconds:number?,
  group_id:UUID, release:ReleaseSummary,
  source:{source_kind:string, codec:string?, bit_depth:integer?,
          sample_rate:integer?, channels:integer?, bitrate:integer?}}
ReleaseSummary = {id,title,artist,album_artist,year,source_type,media_type,
  medium_count,track_count,attention_count,artwork}
```

`release` 复用 catalog 摘要/解码器：ID、标题、艺人非 null，三个 count 非负；album_artist/year/source_type/media_type/artwork 可 null；封面是 `null | {resource_id,mime,width,height}`，保留安全校验（`backend/internal/catalog/projection.go:5-17,51-56`；`backend/internal/catalog/read.go:469-510`）。**missing 仅使 present=false，不清空标题、艺人、发行/组或上次扫描事实**；标题/艺人可空字符串。时长未知、非正或非有限值为 null；codec 空白、音频数值未知/0 为 null，其余数值有限且非负，source_kind 非空。关系由外键保留，历史阻止曲目级联删除；不是“缺失就无元数据”（`backend/migrations/0002_core_slice.sql:42-60`；`backend/migrations/0016_play_history.sql:7-15`）。

`PlayerTrack` 映射：trackId←track_id、releaseId←release.id、groupId←group_id、albumTitle←release.title、albumArtist←release.album_artist ?? release.artist、artworkId←release.artwork?.resource_id ?? null；标题空白用“（无标题）”、艺人空白退回 albumArtist；时长及 source 六字段转 camelCase。与现有映射一致（`frontend/src/features/player/engine/tracks.ts:5-25`；`frontend/src/features/player/engine/queue.ts:14-37`）。source 是事实对象，**不是**发行曲目 DTO 的文件名标签；不返回路径、root ID、指纹或文件时间。

**播放全部建议口径，待主会话确认**：先截取 1,000 个历史候选，再由前端按 present 与能力表过滤，不向更旧历史补位；`skipped = items.length - 可播放数`，截断数量不算 skipped。独立 queue 避免突破分页 100，一次读取带齐队列字段，无逐首/逐发行补请求；已有能力表仍可另行请求/缓存，后端不承诺浏览器 `playable`（`frontend/src/features/player/components/album-playback-actions.tsx:12-31`）。全不可播放时不清空队列；行内播放只传这一首，不照搬专辑“从这首开始播放整张”。

| 条件 | GET tracks | GET queue | DELETE tracks/{track_id} |
| --- | --- | --- | --- |
| 未登录/失效/停用；现有 Guard 鉴权查库失败 | `401 unauthorized` | 同左 | 同左，先检查 Origin |
| 缺 Origin / 不可信 Origin | 不检查 | 不检查 | `403 origin_required` / `403 origin_forbidden` |
| 非法分页 / 非法 UUID | `400 invalid_pagination` | 无此校验（固定上限） | `400 invalid_id` |
| 鉴权后查库/删除/解码/安全投影失败 | `503 database_unavailable` | 同左 | 同左 |
| 空历史 / 合法 UUID 无本人记录 | 200 空列表 | 200 空列表 | 200 `{deleted:0}`，无 404 |

Origin/401 沿用 `backend/internal/identity/guard.go:54-64,84-120`；`invalid_id` 是已有资源 ID 错误码（`backend/internal/operations/httpapi/handlers.go:90-96`）。错误方法沿用全局 `405 method_not_allowed`（`backend/internal/platform/httpserver/fallback.go:31`）。清除全部仍用 `DELETE /api/v1/me/plays`、`{deleted:N}`，同一 403→401→503 矩阵但无 ID 校验。删除幂等指最终状态相同，首次 N、重试通常 0，不承诺数字相同。

## 2. SQL 与索引

listening 只查本人 play_history。两个 GET 共用草图，分别绑定 `$2=page_size,$3=(page-1)*page_size` 或 `$2=1000,$3=0`；结果读完关闭 rows 后再批量调 catalog：

```sql
WITH grouped AS MATERIALIZED (
  SELECT track_id, MAX(played_at) AS last_played_at, COUNT(*) AS play_count
  FROM play_history WHERE user_id=$1::uuid GROUP BY track_id
), page_rows AS (
  SELECT track_id,last_played_at,play_count FROM grouped
  ORDER BY last_played_at DESC,track_id ASC LIMIT $2 OFFSET $3
)
SELECT totals.total,p.track_id::text,p.last_played_at,p.play_count
FROM (SELECT COUNT(*) AS total FROM grouped) totals
LEFT JOIN page_rows p ON TRUE
ORDER BY p.last_played_at DESC,p.track_id ASC;
DELETE FROM play_history WHERE user_id=$1::uuid AND track_id=$2::uuid;
```

total 与本页同一语句快照；空页的 LEFT JOIN 元数据行不输出为条目，避免窗口计数在超末页丢 total。次数是原始记录数，先汇总后 LIMIT。稳定排序仅保证**无并发变更**时跨页不重不漏；新播放/删除改变 OFFSET 位置，A2 用静态数据集验证，不引入跨请求快照。

**不新增迁移**：`(user_id,track_id,played_at DESC)` 已覆盖用户筛选、曲目聚合、计数及删除；现有查询也是同类聚合（`backend/migrations/0016_play_history.sql:18-19`；`backend/internal/listening/recent.go:88-106`）。另建 `(user_id,played_at DESC,track_id)` 不能免除聚合排序/准确计数，不凭“M5”注释预建索引。待实现时确认：隔离库准备重复播放密集/曲目密集、多用户、1,000 边界及深页，用 `EXPLAIN (ANALYZE, BUFFERS)` 看扫描行数、聚合、排序/落盘、heap fetch 与耗时，并测 catalog 摘要；DELETE 仅 EXPLAIN，或在可回滚测试事务中 ANALYZE。个人规模仍需 O(H) 扫描本人记录，1,000 只限制返回/补全，不建汇总表。当前最新为 `0020_release_claims.sql`；若测量证明需迁移，从届时最新编号之后追加（当前 0021），不改 0016（`.trellis/spec/backend/database-guidelines.md:31-43,126-138`）。

## 3. catalog 公开合同与分层

新增函数，不改变旧 `LoadTrackPlacements` 的 present-only 语义，否则概览行为改变（`backend/internal/catalog/track_placement.go:12-54`）：

```go
func (s *Store) LoadTrackSummaries(ctx context.Context, trackIDs []string) (map[string]TrackSummary, error)
```

`TrackSummary`：`TrackID,Title,Artist,GroupID string; Present bool; DurationSeconds *float64; Release ReleaseSummary; Source TrackAudioFacts`。`TrackAudioFacts`：`SourceKind string; Codec *string; BitDepth,SampleRate,Channels,Bitrate *int`；对应上述 wire 字段，无历史统计。输入沿用无效 UUID 跳过、小写去重、空输入不查库、不同 ID 最多 1,000、超限错误；不存在者无 map 项，**missing 必须有完整项**。listening 只传本人历史 ID，按聚合顺序组装；缺少任何引用对象整体 503，不静默丢行。

SQL 全在 catalog：选中 tracks（不筛 present）→media→当前发行/组；曲目事实取现有详情列/nullable 规则（`backend/internal/catalog/read.go:172-235`）。用曲目 CTE 与按不同 release ID 物化（MATERIALIZED）的摘要 CTE 在一个语句快照返回；每发行算一次摘要，复用有效值 `ReleaseSource`、封面投影/校验，**不加发行可见性谓词**（`backend/internal/catalog/internal/releasesql/release.go:4-44`）。不逐首调 `LoadTrackDuration`（仅 present）或把带路径的 `LoadTrackSource` 暴露出去（`backend/internal/catalog/track_source.go:15-47,69-83`）；不查文件系统。

扩展 listening 消费端 `Catalog` 接口即可（`backend/internal/listening/service.go:46-67`）。**`dependency_rules_test.go` 无需改**：仍 listening→catalog，httpapi→identity/listening/platform，catalog→私有 releasesql；不新增包、不让 listening 联表目录或引入 playback/operations（`backend/internal/app/dependency_rules_test.go:34-53`）。前端保持 listening 不导入 player，由 app 组合公开动作，避免现有 player→listening 反向成环（`.trellis/spec/frontend/directory-structure.md:105-108,123-128`）。

## 4. 接口样本与待同步规范

实施时在 `backend/internal/app/testdata/api-samples/listening/` 新增以下 `.json`：`history-tracks`（多次播放/封面）、`history-tracks-missing`（保留专辑/未知值）、`history-tracks-empty`、`history-tracks-out-of-range`（空项但 total>0）、`history-queue`、`history-queue-empty`、`history-queue-truncated`（1,000 项、total>1,000）、`track-plays-deleted`、`track-plays-deleted-empty`。沿用 `{status,body}`，真实服务生成，前端 decoder 对 body 往返核对（`backend/internal/app/api_samples_integration_test.go:422-426`；`frontend/src/features/listening/api/decoders.test.ts:7-102`）。另测 100/101、1,000/1,001、缺字段/非法值、用户隔离、missing 恢复/换发行、删除与概览联动。

`listening-contracts.md`：§1 将完整历史页/按曲目移除移入范围（仍不提供删除某次播放）；§2 加签名/路由；§3 注明表和计次不变；§4 加接口/错误矩阵；§5 分开概览 present-only 与历史含 missing；§6 解除“全部历史”链接禁令，加入映射和上报/移除/清除后的本人历史、最近发行缓存失效及会话隔离；§7 补 A1–A8/样本断言；§8 改“缺失不显示”“禁止全部历史”的笼统反例（旧禁令见 `.trellis/spec/backend/listening-contracts.md:11-12,95-100,121-124`）。同时同步 catalog REST §2 公开函数清单及缺失摘要例外；前端状态/目录/播放器规范补入口、组合和缓存责任。本轮均不修改。

## 5. 风险与主会话确认

- **版本**：按 A3 返回曲目当前发行/组，链接 `/albums/{group_id}?release={release.id}`；同组多版本不合并不同 track_id、不改用默认版本。PRD 第 15 行“播放过的版本”与第 39 行有措辞张力，推荐以 A3 为准，不存当时快照。
- **不可见发行**：整张 missing 仍有历史摘要（present 曲目数可为 0），但既有发行/组详情可能 404（`.trellis/spec/backend/catalog-rest-contracts.md:90-98`）；本设计不扩大可见性，若要求缺失专辑链接一定可打开需另裁决。
- **1,000 上限**：PRD 第 16/42 行未明确过滤顺序。推荐候选先截断，恰好适配 catalog 一次批量；若要求补满 1,000 首可播放曲目，浏览器需分批继续读，不能保证一次请求。截断且跳过时“只放入了最近的 1,000 首”不等于实际入队数，建议改为“仅处理最近 1,000 首”，另报加入/跳过数，**需主会话确认**。
- **并发**：A4“重复结果相同”按最终效果而非 deleted 数字理解；继承清除后在途上报可重写的限制（`.trellis/spec/backend/listening-contracts.md:83-84`）。历史与 catalog 不承诺共同快照，重扫后刷新；present 不是磁盘/根目录可用保证，播放仍走原预检。

**规划记录：路线=人工选择；选择者=主会话转交用户确认；结果=候选合同，1,000 过滤/提示口径待确认；理由=技术事实已核对，不代替用户改动 PRD。** 保持 planning；本轮只写 design.md。主会话确认后再在 PRD 记录决定并补 implement.md/上下文清单，本交付不是实施授权。

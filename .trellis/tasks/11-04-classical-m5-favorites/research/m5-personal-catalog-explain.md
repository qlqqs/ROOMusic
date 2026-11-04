# M5 Catalog 批量读取与执行计划验证

日期：2026-11-04。本节为提交前追加验收的当前计划；后半保留原批量摘要优化与单发行极端风险，不把历史计划当当前完整HTTP性能。对应[设计](../design.md) §7、§10。

## 本轮：真实形状与聚合式公开读取

新增 `LoadTrackCollectionStats`，至多10,000不同有效UUID，仅从catalog自有tracks表返回匹配数、missing数、正有限时长和未知时长数；personal只消费公开结果。当前卡片使用完整集合聚合+前四组有界位置facts，不再为全部200,000引用传完整事实。没有同页20万输入API、全组window排序、持久缓存或新Go依赖。

独立诊断夹具100,000轨/10,000发行/8,000组、每发行10轨、5%missing；修正、核对、依据、封面辅助表均为空。先核心表有统计、辅助表 `reltuples=-1`，再辅助表ANALYZE后 `reltuples=0`；测试显式核查两种状态。查询均使用应用真实SQL常量，`EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`。每条first/warm各一次，未清缓存，不计算p95；完整HTTP的320有效样本见[个人性能报告](m5-personal-performance.md)。

默认连接JIT=on；摘要与组另在只读事务执行 `SET LOCAL jit = off` 作对照，结束核对同连接默认值恢复。除获批局部JIT外不改planner/会话/全局配置；ANALYZE只在诊断准备阶段，不在业务请求。PostgreSQL18.6，28份原始计划未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/catalog-explain/realistic-cards/`，可用下文命令重新生成当前实现的计划。

### 热测执行计划摘要

单位ms，规划/执行分别列出；shared hit顺序为未ANALYZE/ANALYZE，所有计划shared read=0、temp read/write=0。

| 查询 | 空辅助表未ANALYZE：规划 / 执行 | 辅助表ANALYZE后：规划 / 执行 | shared hit | temp read / write |
| --- | ---: | ---: | ---: | ---: |
| 10,000轨统计聚合 | 8.364 / 10.710 | 7.581 / 10.415 | 3107 / 3107 | 0 / 0 |
| 封面前缀32轨facts | 0.572 / 0.248 | 0.680 / 0.333 | 207 / 207 | 0 / 0 |
| 10,000轨facts | 8.575 / 23.027 | 11.049 / 24.352 | 3482 / 3482 | 0 / 0 |
| 1,000发行完整摘要，默认JIT | 3.018 / 143.047 | 2.483 / 39.966 | 7969 / 5053 | 0 / 0 |
| 同查询，事务局部JIT off | 2.987 / 37.505 | 2.341 / 44.769 | 7969 / 5053 | 0 / 0 |
| 80组摘要，默认JIT | 2.692 / 17.242 | 2.246 / 17.045 | 2992 / 2588 | 0 / 0 |
| 同查询，事务局部JIT off | 2.375 / 17.098 | 2.391 / 18.508 | 2992 / 2588 | 0 / 0 |

- **统计聚合**：`tracks_pkey`受请求UUID限定，子节点估算/实际10,000/10,000、loops=1；根Aggregate输出1行、cost 1,176.36，两状态均无JIT、无排序或临时I/O。不会为了四张封面求整个集合的组窗口。
- **位置事实**：前缀32条只补当前组ID，热测0.248/0.333ms；完整10,000 facts为23.027/24.352ms，卡片不再对每个成员都付此传输/投影成本。缺少身份仍fail closed，四组不足时窗口扩展至实际需要的完整范围。
- **完整摘要**：空辅助表未ANALYZE时根cost 128,455.80，默认JIT149函数、编译106.944ms，执行143.047ms；局部关闭后37.505ms，同样7,969 shared hits，无temp I/O。ANALYZE后cost 5,456.40，默认未触发JIT、执行39.966ms。局部JIT只去掉编译，不掩盖查询节点的额外读取；仍记录默认计划以发现估算问题。
- **组摘要**：只消费请求80组和其代表，热测默认17.242/17.045ms；两状态均不触发JIT。完整HTTP卡片用同一读取快照批量补前四组，未N+1查询每条曲目。
- **未保留试验**：将20万引用一次聚合/排序或传facts的方案，实际出现temp I/O或仍超1s，已撤回。它们的诊断JSON不代表最终SQL。当前10,000聚合和有界前缀在真实HTTP两种统计状态下均达标。

### 实际命令与边界

```bash
source /tmp/roomusic-m5p-test-env
ROOMUSIC_CATALOG_EXPLAIN=1 ROOMUSIC_CATALOG_EXPLAIN_DIR="/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/catalog-explain" go -C backend test ./internal/catalog -run '^TestPostgreSQLTrackCollectionStatsExplain$' -count=1 -v
```

非race诊断通过（9.858s、0 SKIP）。最终范围race还复验新聚合的输入上限、NULL/NaN/±Infinity/小数/溢出、missing、无身份、取消、RR快照、单连接释放、1,001轨跨批完整摘要等价；结果见[交付报告](m5-personal-backend-handoff.md)。外部DTO/路由/状态码、默认版本/有效值/核对/封面语义均未改变。冷缓存、非空巨量辅助表、并发压测和浏览器成本不在本次验收结论内。

## 首轮历史计划与极端形状风险

**下面的1–6节是此前实施轮记录。** “本次”“当前”等描述指该轮的实现/测量；极端单发行十万轨与80%missing按用户要求保留为风险，不追该形状，也不把旧服务+JSON结果冒充新实现完整HTTP复测。

日期：2026-11-04。极端单发行的追加结论见第 6 节；首轮统计充分的夹具不能替代该结果。对应[设计](../design.md) §7、§10、§11。

### 1. 范围与环境

本线新增 `Store.LoadTrackFacts` 与 `Store.LoadReleaseGroupSummaries`。主会话获知既有完整摘要的 JIT 瓶颈后，授权在 catalog owner 内优化共享摘要；本次只改 SQL 形状，不改变 `TrackSummary`、`ReleaseGroupSummary`、默认版本、有效值、missing、核对或封面合同，不改迁移/数据库配置。

- 仅使用主会话的 `roomusic-test-m5p` 隔离库；通过 `source /tmp/roomusic-m5p-test-env` 获取 URL，没有自行启动或停止数据库。每个测试经 `postgrestest.NewSchema` 创建并清理独立 schema。
- PostgreSQL 18.6，x86_64-pc-linux-musl，Alpine gcc 15.2.0，64 位；当前 worktree 的迁移包含 0021/0022。
- 合成夹具：100,000 首曲目、1,001 个发行/组、1,001 个介质、501 个安全封面；2,000 首 present、98,000 首 missing。前 1,000 首共用一个发行，接下来 1,000 首各在不同发行，其余均匀分到后面 1,000 个发行；10% 时长未知。
- 建夹具后对查询涉及的表执行 `ANALYZE`；不修改 `enable_seqscan`、JIT 或其他优化器参数。
- 非 race 诊断调用包内真实 SQL 常量执行 `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`，不是近似 SQL。每项两次，断言输出行数。
- “首次”是建夹具与 ANALYZE 后第一次测量，**没有清缓存，不等于冷缓存**；第二次为重复热测，不能用两个样本宣称 p50/p95 或 SLA。往返墙钟只含 Go 调用 EXPLAIN 并接收 JSON，不是产品 HTTP 延迟。

### 2. 首轮批量优化测量

单位：毫秒。以下首轮四条查询均未触发 JIT，没有通过关闭 JIT 制造结果；temp read/write 均为 0/0。

| 实际查询 | 次数 | 规划 | 执行 | 往返墙钟 | 输出行数 | shared hit/read |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| facts 10,000 | 首次 | 11.646 | 17.781 | 50.949 | 10,000 | 801 / 3 |
| facts 10,000 | 热测 | 8.978 | 16.994 | 43.214 | 10,000 | 804 / 0 |
| groups 80 | 首次 | 11.171 | 38.321 | 69.163 | 80 | 8,977 / 16 |
| groups 80 | 热测 | 3.162 | 34.584 | 40.653 | 80 | 8,988 / 0 |
| track summaries 同发行 1,000 | 首次 | 3.113 | 8.357 | 15.937 | 1,000 | 1,240 / 0 |
| track summaries 同发行 1,000 | 热测 | 2.942 | 8.167 | 14.922 | 1,000 | 1,240 / 0 |
| track summaries 不同发行 1,000 | 首次 | 3.017 | 376.526 | 383.375 | 1,000 | 103,470 / 0 |
| track summaries 不同发行 1,000 | 热测 | 2.782 | 371.614 | 378.226 | 1,000 | 103,470 / 0 |

#### 2.1 计划形状与界限

- **facts**：根 Hash Join 估算/实际均 10,000 行、loops=1，成本 3,573.34。`tracks_pkey` Bitmap Index/Heap Scan 受 UUID 请求集合限定；media 与 releases 两张小表各扫描 1,001 行一次后 hash join。没有完整摘要/封面/attention 子计划。最大单个 hash 71 KB，没有排序或落盘。
- **groups**：请求组走 `release_groups_pkey` Bitmap Scan。代表 CTE 的 Unique 估算/实际 80 行、loops=1，不先给全库生成代表；`summary_release_ids` 只有本批 80 个代表。`summary_counts` 分组聚合一次，估算 1,001/实际 80 行；轨道索引访问 loops=80、每次平均 110.26 行。完整 summaries CTE 估算/实际 80 行一次；有效值视图底层 releases 在候选和摘要阶段各扫描 1,001 行一次。根成本 3,383.56；排序均在内存（代表排序 30 KB，介质排序 71 KB）。
- **同发行摘要**：selected_tracks 1,000 行，去重发行 CTE、计数 CTE、summaries CTE 实际均只有 1 行。轨道计数索引只访问一次、读 1,000 行，摘要字段/attention 也只计算一次。根 Merge Join 仍估算 5,000/实际 1,000 行，但成本已从 1,052,167.86 降到 25,518.02，不触发 JIT。最大排序 172 KB，无 temp I/O。
- **不同发行摘要**：去重发行与 summaries CTE 均为 1,000 行，计数 CTE 估算 1,001/实际 1,000 行、loops=1。轨道索引访问 loops=1,000、每次 99 行，而非原来的三套重复计数；字段问题聚合 loops=1,000。根估算 5,000/实际 1,000 行，成本 25,518.02。最大排序 219 KB，全在内存。
- **保留的成本**：缺失核对仍按轨比较既有依据，本夹具空核对表在 groups 80 中 loops=7,742、不同发行摘要中 loops=98,000。该成本解释了不同发行/大量 missing 场景仍需约 372 ms；不是 SQL 往返 N+1。上述排序/hash 数字是单个计划节点的内存，不是应用峰值内存。

### 3. 优化前后证据

#### 3.1 组代表的估算误差

初版组查询使用 `ROW_NUMBER() ... member_rank=1`。代表估算 1 行、实际 80 行，摘要阶段底层 releases 顺序扫描 loops=80，热测 99.049 ms。

改为 `DISTINCT ON (releases.group_id)`，继续复用 `ReleaseGroupMemberOrder`，在 DISTINCT 前窗口计算完整可见成员数，代表 CTE 明确物化：估算/实际均 80 行，摘要阶段底表只扫描一次，热测 44.826 ms。后续共享计数优化进一步降到 34.584 ms。集成覆盖默认失效、年份/时间/UUID 顺序，以及 123 个成员的完整计数；没有扩大旧详情的可见性或截断合同。

#### 3.2 共享摘要的 JIT 与重复计数

优化前每个发行的 present 介质数、present 曲目数、未核对缺失来源数为三个独立相关聚合。不同发行时各 loops=1,000，计划成本超过百万，触发 157 个 JIT 函数及内联/优化。

| 热测 | 优化前执行 | 优化前 JIT | 优化后执行 | 优化后 JIT | shared hit 前→后 |
| --- | ---: | ---: | ---: | --- | --- |
| 同发行 1,000 轨 | 1,632.957 | 1,624.660 | 8.167 | 未启用 | 1,304 → 1,240 |
| 不同发行 1,000 轨 | 2,000.236 | 1,570.604 | 371.614 | 未启用 | 317,296 → 103,470 |

当前 `releasesql.BatchSummaryCountsCTE` 只消费本批 `summary_release_ids(id)`，把三个轨道相关计数合并为一次按发行分组的聚合；只覆盖本批所涉发行，不生成全库统计。`BatchSummaryProjection` 与原投影共享有效值/封面列，继续由 `ReleaseSummaryColumns` 解释。缺失来源去重、核对依据、字段/归组 attention 直接复用原 SQL 表达式。

等价性测试把新批量摘要逐字段对照旧单发行投影，覆盖：多碟 present 数、全 missing 发行、同一旧来源多轨去重、非空绑定身份、修正覆盖、有效核对、单轨 observed_at 改变、字段/归组依据改变。事务仍由调用方拥有，曲目、去重发行、计数及摘要都在一个语句快照中。

#### 3.3 持久化原始计划

原始计划未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/catalog-explain/`；可用第4节命令重新生成当前实现的计划。历史优化前和已撤回试验的计划依赖对应实现，不保证用当前代码逐字复现。目录结构保持不变：

```text
/home/qlqq/.local/share/roomusic-evidence/m5-personal/catalog-explain/
  facts-10000-{first,warm}.json
  groups-80-{first,warm}.json
  summaries-shared-1000-{first,warm}.json
  summaries-distinct-1000-{first,warm}.json
  before/                 # 共享计数优化前，同样八份计划
  initial-groups/          # ROW_NUMBER 初版的两份组计划
```

全部是合成 UUID/素材，无测试库 URL 或凭据。最终报告与原始计划对应非 race 运行；主会话后续 race 门禁默认不覆盖这些结果，除非显式指定同一 `ROOMUSIC_CATALOG_EXPLAIN_DIR`。

### 4. 实际验证命令

先在 worktree 根执行 `source /tmp/roomusic-m5p-test-env`。下列测试均已实际运行，最终通过；指定集成与显式诊断均 **0 SKIP**。未跑全仓测试，因为仅涉及 catalog 读取，主会话统一负责 personal/app/前端及全线 race 验收。

进入 `backend` 执行：

```bash
go test -race ./internal/catalog -run 'Test(PersonalReads|LoadTrackSummariesInputBounds|PostgreSQL(LoadTrackFacts|LoadReleaseGroupSummaries|LoadTrackSummaries.*|PersonalReadsBoundsAndSnapshot|PersonalReleaseGroupOrderAndFullCount|PersonalBatchSummaryParity|LoadTrackPlacements.*|IssueReviewsExcludeReviewedIssuesFromAttention)|LoadTrackPlacementsWithoutUsableInputDoesNotQuery)' -count=1 -v

go test -race ./internal/catalog/internal/releasesql -count=1
go vet ./internal/catalog ./internal/catalog/internal/releasesql
go build ./internal/catalog ./internal/catalog/internal/releasesql

ROOMUSIC_CATALOG_EXPLAIN=1 \
ROOMUSIC_CATALOG_EXPLAIN_DIR="/home/qlqq/.local/share/roomusic-evidence/m5-personal/catalog-explain" \
go test ./internal/catalog -run '^TestPostgreSQLPersonalCatalogExplain$' -count=1 -v
```

另经 `gofmt` 与定向 `git diff --check`；诊断默认跳过是显式 opt-in 的设计，普通跳过不等于性能证据。

### 5. 后续风险与边界

1. 本夹具是 missing 占多数、1,001 个发行的十万轨库；没有模拟十万个发行、修正/核对表大量非空、冷缓存或并发负载，不作全场景性能保证。
2. 不同发行完整摘要仍约 372 ms/千轨；队列最多十批，应结合主会话的整条请求、JSON 字节数、前端解码和取消测试确定交互门槛，不能用轻量 facts 延迟代替队列响应。
3. 20 个歌单/20 万引用、撤销临时超限统计、内存与 p50/p95 由 personal 性能验证负责。本报告不宣称这一用户可见流程已达标。
4. 未改已有 `ReleaseGroupSummary` 或共享 HTTP/wire 外形；新增包装才允许 `Representative=nil`。没有启动/停止容器、提交、改 frontend/web 或操作主仓库。

### 6. 追加：十万轨单发行与空元数据表统计

#### 6.1 目的与控制变量

主会话报告个人夹具（100,000 轨全同发行、80% missing）的收藏 20,000/返回 4,000 队列约 10.5 秒、歌单 10,000/返回 2,000 队列约 5.44 秒。首轮“同发行 1,000 轨”只对应一个 1,000 轨且全 present 的发行，**不能覆盖十万轨单发行**。

追加 `TestPostgreSQLPersonalCatalogLargeReleaseExplain`：一个发行、一个介质、100,000 轨（20,000 present / 80,000 missing），额外 1,000 个空组，请求 1,000 个 present 曲目的完整摘要及一个组摘要。仍为真实 SQL、非 race、隔离 schema，不改优化器参数。两阶段在同一份夹具中依次运行：

1. `default-stats`：仅 ANALYZE tracks/media/releases/release_groups，空的修正、核对、依据与封面表保持建表后的默认统计；贴近 personal 性能夹具的分析范围。
2. `analyzed`：额外 ANALYZE release_artworks/release_corrections/release_issue_reviews/release_field_decisions/release_grouping_evidence。

测量期间其他工作者可能在同一测试实例运行，因此墙钟不当作 SLA；计划形状、行数估计、loops、JIT 与 I/O 是主要定位证据。

#### 6.2 冻结版本的实际结果

以下是**最终保留的生产 SQL**（原始计划 `single-release-100000/before/`），时间单位毫秒：

| 场景 | 首次执行 | 热测执行 | 热测 JIT | 热测计划成本 | shared hit/read | temp read/write |
| --- | ---: | ---: | ---: | ---: | --- | --- |
| 默认元数据统计，1,000 摘要 | 2,684.558 | 2,494.799 | 2,073.545 | 1,329,105.74 | 163,593 / 0 | 1,297 / 1,300 |
| 默认元数据统计，一个组 | 2,577.160 | 2,340.478 | 1,924.946 | 1,327,745.75 | 162,254 / 0 | 1,297 / 1,300 |
| 元数据表也 ANALYZE，1,000 摘要 | 396.086 | 399.013 | 无 | 26,169.96 | 3,577 / 0 | 1,298 / 1,301 |
| 元数据表也 ANALYZE，一个组 | 400.151 | 401.281 | 无 | 26,267.47 | 2,235 / 0 | 1,298 / 1,301 |

**结论：主会话的秒级结果已复现，主要差异是元数据表统计状态触发的 JIT，而不只是曲目数量。**

- `summary_release_ids`、`summary_counts` 与 `summaries` 实际各一行、loops=1。单条 SQL 内没有为 1,000 个请求轨重复计算 1,000 次整个发行。
- 但是每个 1,000 轨批次都会重新统计这个十万轨发行；现有消费端分四批返回 4,000 轨、分两批返回 2,000 轨，因而分别重复支付约四次、两次 JIT/统计成本。四次 × 2.495 秒约 9.98 秒、两次约 4.99 秒，与个人测试量级一致；这是基于独立 SQL 测量的解释，**不是重新测得的完整个人请求时间**。
- 默认统计下，`release_issue_reviews` 按主键查找 loops=80,000、每次实际零行，计数聚合估算成本约 848,570；该阶段不是跨网络 N+1，但每轨核对的执行及估算成本真实存在。
- 空修正/依据表缺统计还使 summaries CTE **估算 11,610 行，实际一行**，估算成本约 475,289。根计划触发 173 个 JIT 函数，热测 JIT 占总执行时间约 83%。
- 完整 ANALYZE 后仍需扫描这个十万轨发行；去重聚合存在临时 I/O（约 1,300 个 8 KiB 块读写），热测约 399 ms。不存在“补统计后可按每千轨约 8 ms 算超大单发行”的结论。

#### 6.3 局部试验与冻结决定

按授权做过一个仅在 catalog 内部的等价试验：将缺失核对 EXISTS 子计划改成按唯一 `(release_id,issue)` 的 LEFT JOIN，NULL 依据视为不匹配，保留所有 DTO。相关等价性 race 测试通过，原始计划保存在 `single-release-100000/after/`。

试验将 80,000 次核对查找降为单次关联，但默认统计下 summaries CTE 仍估为 11,610 行，因此 **JIT 没有消失**：摘要热测 2,175.008 ms，其中 JIT 1,714.961 ms；完整 ANALYZE 后热测 435.253 ms、temp 1,396/1,399 块，未优于原方案的 399.013 ms。它没有解决主要剩余问题，也增加聚合行宽与临时 I/O。

**为本轮收尾已撤回这个关联试验，生产 SQL 恢复并冻结为第 6.2 节版本；没有启动新的共享投影重构或修改 wire。** `after/` 是未保留试验的证据，不能作为已交付性能宣称。此轮只新增极端诊断与报告；此前获准的批量计数优化继续保留。

后续可单独评估空元数据表统计维护、有效值视图的批量基数估计，以及同一只读事务内跨批复用发行摘要；目前不为此引入缓存或扩公开接口，也不关闭 JIT、不以修改测试统计范围掩盖默认统计风险。

#### 6.4 验证与复现

新增极端诊断非 race 运行通过、0 SKIP；关联试验的下列 race 验证也通过，但试验已撤回，不作为新生产变更交付：

```bash
source /tmp/roomusic-m5p-test-env

go -C backend test -race ./internal/catalog \
  -run 'TestPostgreSQL(PersonalBatchSummaryParity|LoadTrackSummaries.*|PersonalReleaseGroupOrderAndFullCount|IssueReviewsExcludeReviewedIssuesFromAttention)' -count=1 -v

ROOMUSIC_CATALOG_EXPLAIN=1 \
ROOMUSIC_CATALOG_EXPLAIN_DIR="/home/qlqq/.local/share/roomusic-evidence/m5-personal/catalog-explain/single-release-100000/recheck" \
go -C backend test ./internal/catalog -run '^TestPostgreSQLPersonalCatalogLargeReleaseExplain$' -count=1 -v
```

持久化目录 `single-release-100000/before/` 中 `default-stats-*` 和 `analyzed-*` 为双阶段对照；其中没有阶段前缀的四份文件是最初完整 ANALYZE 的探测。`after/` 保留已撤回的局部关联试验。代码冻结后的统一 race 由主会话执行，不自行再次启动长性能用例。

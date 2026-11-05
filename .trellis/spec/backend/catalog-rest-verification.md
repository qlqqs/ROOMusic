# Catalog REST 验证与示例

本文从[Catalog REST 合同](./catalog-rest-contracts.md)的第 5–7 节原样移出（2026-11-05，控制单文件体积），节号不变，内容未改；接口、投影与错误矩阵仍以 Catalog REST 合同为准。

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
- M5 卡片统计（`track_collection_stats*_test.go`）：10,000 输入上限、空输入/去重/未知身份、missing 与全部非有限/非正时长、累计溢出、单条 SQL/单连接释放、取消错误链、同一 RR 快照；不能为卡片统计批量计算完整摘要或把组封面排序放到 20 万行聚合中。
  `TestPostgreSQLTrackCollectionStatsExplain` 测真实形状十万轨/一万发行/八千组、5% missing，空辅助表未 ANALYZE/ANALYZE 两种状态；保持默认计划，并按[数据库规范](./database-guidelines.md#延迟敏感只读事务与-jit)增加只读事务局部 JIT off 对照。
  跨批摘要等价测试保留1,001轨边界；模拟同一旧来源时使用 `source_identity=NULL` 和相同root/path，不能重复非空来源身份（唯一约束仍有效）。
- M5 个人数据批量读取（`personal_reads*_test.go`）：空输入、大小写去重、10,000/10,001 轨与 1,000/1,001 组边界；missing 与不存在分离、未知时长安全化、
  换发行/归组/恢复反映当前位置；空组及全缺失组保留身份而旧详情仍 404；有效修正、默认版本失效、代表排序、完整成员计数、坏封面安全失败。
  单连接批量读取确认 rows 释放，查询计数确认无 N+1；只读 REPEATABLE READ 事务与取消场景验证共享快照及错误传播。
  显式诊断 `ROOMUSIC_CATALOG_EXPLAIN=1 go test ./internal/catalog -run '^TestPostgreSQLPersonalCatalogExplain$' -count=1 -v`
  使用调用方提供的隔离库（`ROOMUSIC_TEST_DATABASE_URL`），建立十万曲目夹具，对实际 SQL 测 10,000 facts、80 组、1,000 同发行/不同发行完整摘要；
  `ROOMUSIC_CATALOG_EXPLAIN_DIR` 可保存 JSON 执行计划。不启动容器、不禁用顺序扫描或 JIT；诊断默认跳过，不能把跳过当作性能证据。测量与优化前后证据见
  [M5 catalog 执行计划报告](../../tasks/archive/2026-11/11-04-classical-m5-favorites/research/m5-personal-catalog-explain.md)；`TestPostgreSQLPersonalCatalogLargeReleaseExplain`
  另测十万轨单发行、80% missing，区分空元数据表默认统计与完整 ANALYZE；默认统计仍有 JIT 风险，不能用首轮千轨发行的结果代替。额外的摘要等价测试覆盖多碟、同来源缺失去重、有效/过期核对与修正。
- 运行 `go test ./internal/catalog/... ./internal/app -run 'Release|Catalog|Artwork|Group|Routes|Default|Comparison' -count=1`（M4 的默认版本与对比用例名不一定含 `Group`）；涉及 SQL 投影时，
  还需通过隔离 PostgreSQL 18 的 `./scripts/test-integration.sh`。
- 发行 REST/前端：present-only 列表、稳定分页/搜索、attention allowlist、详情事实、
  管理员 evidence 权限、路径脱敏、nullable/enum/数组上限和 malformed payload；按组接口与 `/releases` 逐项对应。

- 扫描反馈定向验证：`recent_additions_integration_test.go` 覆盖空/八张/多版本 MIN、默认代表、搜索、稳定翻页、禁用 root、缺失/恢复、历史项不丢失与跨连接快照；
  `httpapi/recent_additions_integration_test.go` 覆盖两种角色、401 优先、参数拒绝与安全 503；
  `governance/claim_hints_batch_integration_test.go` 覆盖批量与单条等价、每目标上限、反向不受五条限制、认领/撤销与同一快照；
  `recent_additions_explain_integration_test.go` 按环境变量显式启用规模测量，保留生产 SQL 的实际计划，不作为普通单元测试的必跑性能负载。

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
query := "SELECT " + releasesql.ReleaseSummaryProjection + " FROM releases" + releasesql.ReleaseSummaryArtworkJoin
summary, err := scanReleaseSummary(db.QueryRowContext(ctx, query+" WHERE releases.id=$1::uuid", id))
```

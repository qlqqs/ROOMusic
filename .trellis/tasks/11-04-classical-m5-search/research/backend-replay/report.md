# S 线后端重放验证

## 边界

2026-11-04，仅把 `1a11784` 重放到 `a7f9dba`。备份 `m5-search-artists-pre-rebase` 保留 `53ba7c3`；原 `m5-search-artists` 不移动。不重放 `53ba7c3`，不改前端源码/嵌入产物、不运行前端或 Playwright、不推送。搜索/艺人任务尚未全部完成，不归档任务或创建额外收尾提交。

## 冲突与自动合并复核

| 文件 | 处理 |
| --- | --- |
| `.trellis/spec/backend/catalog-rest-contracts.md` | 保留 P 的个人音乐读取、F 的加入时间与最近新增以及 S 的规范化搜索合同；默认带 q 按相关性，显式加入时间排序保留 |
| `backend/internal/app/dependency_rules_test.go` | 保留 personal/httpapi、扫描反馈既有依赖，追加逐包 textnorm/searchindex/writegate/maintenance 规则，未使用通配白名单 |
| `backend/internal/catalog/groups.go` | 使用 S 的 legacy 方法名，保留 F 的 `MIN(created_at)`、`added_at` 扫描映射和加入时间排序 |
| `backend/internal/catalog/ingest/persist.go` | 先 CATP，再反馈前态、核心图写入与逐轨搜索刷新、发行搜索刷新、反馈后态，最后同一事务提交；旧 missing 入口保留 present 条件和搜索可见性刷新 |

自动合并文件逐项检查了 app 装配、API 样本入口、routes/handlers、read.go、scanner、persist_track.go、治理挂接、依赖/目录/spec 增量：P 路由与装配、F 反馈调用和终态冻结均保留。`frontend/**`、`backend/cmd/roomusic/web/**` 相对基点没有改动，原后端提交也未触及它们。

三项语义补合：

1. F 实际调用 `MarkMissingSourcesWithFeedback`，而不是 S 修改的旧 missing 入口。新入口先取 CATP，256 行键集更新后仅返回该批 ID，在反馈保存点以外刷新可见性完成标记；失败回滚全根事务，反馈损坏则核心继续。联合测试覆盖同次新增/更新/消失、最近新增快照、规范化查询、revision、epoch、投影失败回滚、反馈降级和第二批失败全回滚。
2. 搜索补全的 ranked CTE 补 `added_at`，按所有可见成员而非代表计算。搜索组列表保留显式 `added_at_desc`、完整规范化匹配、attention、精确计数和分页。F 已开启 RR 的读取调用快照内公共查询，不嵌套搜索事务。真实搜索扫描测试同时检查三次扫描的报告计数。
3. `searchSnapshot` 复用既有 `readWithLocalJITOff`。catalog 生产代码只剩一处 `SET LOCAL jit = off`。状态、计数、页、补全仍共用只读 RR，`ErrQuery`、底层错误链、失败零值和配置失败短路不变；事务诊断前缀统一采用共享助手。覆盖成功、begin/configure/state/read/commit 故障及真实连接设置恢复。

独立静态复核覆盖自动合并、写锁顺序、F 并发屏障、JIT 错误链与排序摘要，除本报告列出的已修复问题外未发现额外阻断项。

## 迁移

- `0021_catalog_search.sql` 改名 `0025_catalog_search.sql`；原 SQL SHA-256 不变：`e678b3fa2936b160b5dc52916630ef5fdbe1b191d6a6060b51c05bbaaf71b071`。
- 全仓 `git grep` 检索文件名、搜索迁移号和迁移总数；更新 `search-contracts.md`、`step1-backend-files.txt`、`step1-backend-report.md`、`step1-backend-scope.md`、`step1-search-review.md`。P/F 的 0021–0024 引用保持原义。
- 文件系统断言迁移恰好连续 1–25；新增内置迁移顺序回归，原 fresh/rerun/漂移/回滚测试仍以动态文件数量验证全部迁移。没有改旧迁移字节。

## 首轮失败及根因

首轮指定范围 race：617 个顶层测试通过、1 个失败、0 测试跳过（其中 PostgreSQL 361 通过、1 失败）。唯一失败是 F 的 `TestPostgreSQLCatalogHTTPPerformance`：其他包更新了其 schema 的统计，`analyze_count` 从 1 变为 2，不是性能阈值失败。

根因类别为测试覆盖缺口与隐含假设：S 的两处策略测试和一处规模 benchmark 使用裸 `ANALYZE`，它不受 `search_path` 限制。并行共享同一个一次性库时会污染其他 schema。没有放松 F 的统计断言、没有用串行参数掩盖问题；统一使用 `postgrestest.AnalyzeSchema`，仅枚举并分析当前 schema 的转义限定表名。新增双 schema 回归，同时更新数据库规范。原失败结果保留在 `race-first-summary.json`。

## 隔离环境与命令

完整启动/清理脚本见 `check-isolated.sh`：全程持有 `/tmp/roomusic-test-m5s.lock`，已有该项目容器/卷则退出；每轮空闲端口和随机数据库/密码。仅使用：

```bash
docker compose --env-file /dev/null \
  -f /home/qlqq/.paseo/worktrees/2i6geckb/m5-search-artists/compose.test.yaml \
  -p roomusic-test-m5s up -d --wait postgres
# 退出 trap 执行同一 Compose 的 down -v --remove-orphans
```

从未连接、启动、停止或 exec 用户 `roomusic-postgres-1` / `roomusic` 库。可选测试开关全部显式配置：固定缓存 FFmpeg、`ROOMUSIC_CATALOG_EXPLAIN=1`、EXPLAIN/HTTP 输出路径。`ROOMUSIC_REAL_MUSIC_ROOT` 指向仓库的 **合成** `backend/testdata/playback`，不是用户真实音乐库；输出目录为 `/tmp/roomusic-m5s-replay-results`。race 的规模 HTTP 测试只验证正确性，不声称本轮进行了正常构建的性能验收。

实际命令（后端目录）：

```bash
go build ./...
go vet ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/...
gofmt -l $(find internal/catalog internal/library internal/app internal/platform internal/personal -name '*.go') cmd/roomusic/main.go
ROOMUSIC_UPDATE_API_SAMPLES=1 go test -race ./internal/app -run '^TestPostgreSQLSearchResponseSamples$' -count=1 -json
go test -race ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... -count=1 -json
```

样本生成只写 search 子树：11 个样本共 21 处增加 F 的 `added_at`；没有重写其他样本或前端文件。未运行全仓库测试，按用户指定后端影响范围验证。

## 最终结果

- 最终完整指定范围 race：**619 个顶层测试 PASS、0 FAIL、0 测试 SKIP**，其中 **363 个 PostgreSQL 顶层集成测试 PASS**；含子测试/种子共 1,784 个通过事件，22 个有测试包通过。`library/parser/parsertest` 是无测试文件的辅助包，其包级事件不算跳过测试。详见 `race-final-summary.json`。
- `go build ./...`、相关包 `go vet` 均退出 0；指定 Go 文件范围 `gofmt -l` 输出为空。search 样本定向生成 1 项通过，最终 suite 的两种样本门禁再次只读通过。
- 同次真实扫描的搜索与反馈计数、联合 ingest 故障/批次回滚、显式加入时间搜索、JIT 设置与错误链、迁移连续性及 fresh/rerun 均通过。
- 三轮隔离项目（样本、首轮 race、最终 race）都通过 EXIT trap `down -v --remove-orphans`；最终按项目标签查询容器、卷、网络均为空，随机数据库随卷删除。没有保留测试凭据文件。
- 原始日志保留于 `/tmp/roomusic-m5s-replay-results`；没有运行前端、Playwright 或全仓库 `go test ./...`。

既有文档限制：基点 `catalog-rest-contracts.md` 已为 27,509 字节，超过规范建议的 24 KB；本轮按要求保留 P/F/S 并集，不借重放重构既有合同文档。

空白检查：除随原提交原样重放的 OpenCC LICENSE 末尾空行警告外，`git diff --cached --check` 无其他问题；许可证原始字节与已测试哈希保持不变，不为格式检查改写上游许可。

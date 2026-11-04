# M5 F 线步骤1后端交付

日期：2026-11-04。仅 worktree `m5-scan-feedback`；未提交、未stash/reset/checkout，未改主仓库或其他工作树。前端与内嵌web产物由并行前端agent负责，不纳入本报告。

## 实现与文件

完整文件清单见 [backend-files.txt](./backend-files.txt)。核心改动：

- `library/scan_progress.go`：全根目录项计数、固定根权重、单调基点、分批刷新与角色呈现数据；`scanner.go` 只接编排与窄反馈入口。
- `library/scan_report.go`、`scans.go`、`httpapi/scans.go`：报告冻结、降级、读取一致性、权限错误、恢复与暂存清理。
- `catalog/ingest/scan_feedback*.go`：结构化业务前后态、变更原因/互斥分类、来源腾位、曲目改挂、终态missing及有界记账；`persist.go`保留原路径只接反馈，不改身份算法。
- `catalog/artwork/scan_feedback.go`：候选提交后的独立封面关联事务记账，文件准备/清理不持发行和组锁。
- `catalog/recent_additions.go`、`scan_added_albums.go`、`governance/claim_hints_batch.go`及httpapi：最近新增、组排序/added_at、历史页和实时认领提示。
- 增量测试、真实handler样本与依赖规则；后端四份要求的合同已同步，新增集中扫描反馈合同，更新索引/公开依赖说明。

迁移：

1. `backend/migrations/0023_scan_feedback.sql`
2. `backend/migrations/0024_catalog_recent_additions.sql`

无新增 Go 依赖。`persist_track.go`无需改写：事务前态覆盖来源占有者，现有 `vacateSourceIdentity` 的变化由同事务后态捕获。

## 验证命令与结果

数据库仅使用本次新建的 `roomusic-test-m5f`，随机数据库名和空闲loopback端口；所有 PostgreSQL 测试自行建立并清理schema。初始化命令：

```bash
docker compose --env-file /dev/null \
  -f /home/qlqq/.paseo/worktrees/2i6geckb/m5-scan-feedback/compose.test.yaml \
  -p roomusic-test-m5f up -d --wait postgres
```

实际最终命令（backend目录）：

```bash
source /tmp/roomusic-m5f-env
export ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg
export ROOMUSIC_REAL_MUSIC_ROOT=/home/qlqq/.paseo/worktrees/2i6geckb/m5-scan-feedback/backend/testdata/playback
export ROOMUSIC_CATALOG_EXPLAIN_OUTPUT=/home/qlqq/.paseo/worktrees/2i6geckb/m5-scan-feedback/.trellis/tasks/11-04-classical-m5-scan-feedback/verification/catalog-explain.md

go test -race -json ./internal/library/... ./internal/catalog/... ./internal/app -count=1
go test -race -json ./internal/library/httpapi ./internal/app \
  -run 'ScanFeedbackPresenter|ScanFeedbackReportDatabaseErrors' -count=1
go vet ./internal/library/... ./internal/catalog/... ./internal/app
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1

ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app \
  -run '^TestPostgreSQL(APIResponseSamples|ScanFeedbackAPIResponseSamples)$' -count=1
go test ./internal/app \
  -run '^TestPostgreSQL(APIResponseSamples|ScanFeedbackAPIResponseSamples)$' -count=2

git diff --check -- backend .trellis/spec/backend
```

结果：指定范围 **434个顶层测试全部通过，其中272个PostgreSQL集成测试，0个测试SKIP**；后续新增的2个HTTP/呈现回归也以race通过（其中1个PostgreSQL集成）。合计 **436个顶层测试、273个PostgreSQL集成，0失败、0测试SKIP**。无测试文件的辅助包输出不计入跳过用例。vet、依赖规则、样本两次重复读取与空白检查通过。计数与各测试名见 [test-results.json](./test-results.json)。

初轮未启用可选媒体/EXPLAIN环境，出现7个测试SKIP，未作为验收结果；补齐已有固定FFmpeg、仓库合成fixture与EXPLAIN开关后，上述完整范围重跑为0测试SKIP。没有访问用户真实音乐库；环境变量名虽含REAL，实际指向已提交的合成测试音频目录。未运行全仓库 `go test ./...`、前端、Playwright或播放E2E，原因是本步只要求指定后端最小范围。

## 锁顺序与确定性并发矩阵

扫描顺序：收集全部既有发行 → UUID升序FOR UPDATE → 独立语句重读归属/可见性 → 组升序FOR SHARE；组后不追加既有发行锁。扫描不读取/锁治理operation或人工覆盖。治理仍是operation→发行→组；finalize额外先持scan_runs行锁。

app矩阵42个叶场景全部通过，均以独立连接和SQL锁屏障控制，不靠任意sleep安排交错：

| 场景 | 证据 |
| --- | --- |
| 候选、封面、missing × 修正/默认/清默认/归组/核对及各自撤销 | `ManualState` 27场景，精确报告分类、原组归集、目录图和人工revision/覆盖/default不被扫描改写 |
| 三入口 × 认领/撤销认领 | `ClaimAndRevert` 6场景；missing使核对basis改变时明确409且无人工副作用 |
| 扫描先/治理先归组 | `Regroup`，锁后读取新归属而非混合快照 |
| 来源腾位/曲目改挂 × UUID反向顺序认领 | `SourceVacateClaim`，旧轨missing和新/旧组正确归集 |
| 候选先提交、后置封面独立事务 | `Artwork` |
| 全根missing与终态原子 | `FinalizeMissing` |
| 反馈组锁超时 | `FeedbackLockTimeout`，报告unavailable、核心提交、反馈新增锁释放、重扫无重复 |
| SQL取消导致holder失效，候选提交前/后 | `CancelHolderLossReleasesLocks`，worker退出、holder PID消失、锁释放、恢复incomplete/unavailable、进度冻结、staging清理、下一扫描成功 |

ingest另有集合失效强制回归 `TestPostgreSQLScanFeedbackGovernanceChangedLockSetFallsBack`：发行等待时把track改挂到未收集发行；锁后复检发现集合变化，反馈保存点释放旧发行锁（第三连接NOWAIT证明），核心继续、报告永久Broken、无残留变化行。还覆盖锁后重读、治理夹在扫描两个提交之间不污染missing、600轨有界missing原子性。

## 查询计划

> 以下为首次交付的历史结果；提交前已按用户要求扩至10万曲目并仅在读取事务内关闭JIT，最新达标结果与完整复验见 [读取性能补验](./catalog-read-followup.md)。

实际 `EXPLAIN (ANALYZE, BUFFERS)` 与参数见 [catalog-explain.md](./catalog-explain.md)。合成2千组/4千版本/4万曲目，1800可见组。最近8项约239ms，加入时间排序首页50项约219ms；JIT约194/169ms占主要开销。排序全部在内存，无落盘；最近新增不做COUNT，但仍需可见性判断、组窗口和全局排序，不能宣称只读8行。使用 `releases(group_id,created_at,id)` 候选索引，未加重复的tracks部分索引。仅为该规模暖缓存证据，不承诺更大库或并发负载延迟。

## 接口样本

`backend/internal/app/testdata/api-samples/scan-feedback/`：59份新增真实handler响应，另更新17份既有扫描/组摘要样本。

覆盖阶段、管理员/成员脱敏、多根边界、零根/零文件、漂移/不完整、启动/复用/取消、终态与恢复、ready/partial/unavailable/legacy及权限/分页错误、新增历史分页/实时认领移组、recent空/1/8项和sort。状态窗口由隔离数据库fixture或SQL锁固定，wire通过真实鉴权、查询与presenter，不手写JSON。前端decoder往返属于后续步骤3，本步未改前端文件。

## 内部选择、偏差与待裁决

对外合同无偏差，无待裁决事项。以下是合同允许的内部选择：

- 反馈锁集合失效零次重试，立即释放反馈锁、降级报告并继续核心，避免逆序补锁与无限重试。
- 分母SQL失败只冻结进度，报告健康独立；进度短超时采用服务端statement_timeout，不用短客户端超时主动牺牲holder。
- 前后态显式列举扫描业务投影，SQL临时表比较规范化摘要；不把歌词全文写入历史表，署名position是canonical role/name排序的派生值。
- holder真的失效时不能承诺当场写canceled；遵循既有协调合同，退出后在取得锁的恢复/下次Start转incomplete/unavailable。正常holder存活取消仍按持久意图canceled收口。

未模拟OS级硬杀进程；已用真实SQL取消导致物理holder连接消失覆盖执行权丢失、锁释放、恢复和已提交/未提交边界。

## 清理

全部验证后已执行同一绝对路径Compose命令 `down -v`；按项目过滤确认无残留容器、卷。未连接或操作 `roomusic-postgres-1`、用户 `roomusic` 库或其他线项目。任务仍in_progress，步骤2–4及主会话审阅/提交不在本次完成范围，未归档或自动写会触发commit的journal。

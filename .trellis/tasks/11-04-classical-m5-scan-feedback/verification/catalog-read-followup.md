# 读取延迟提交前补验

日期：2026-11-04。继续既有步骤1，无新任务、无提交；未改前端、web构建产物、database-guidelines.md、Go依赖或迁移。

## 改动

完整本轮文件清单见 [catalog-read-files.txt](./catalog-read-files.txt)。生产部分仅 `catalog/read_transaction.go` 与两个HTTP入口：recent 与 added_at_desc 通过只读 REPEATABLE READ 短事务调用原 Store，事务开始后 `SET LOCAL jit = off`；begin/config/read/commit失败都保留原因并分类为ErrQuery，HTTP返回503 database_unavailable。标题排序不受影响。

集成测试验证事务内 SHOW jit=off/read_only=on/isolation=repeatable read；成功、读取失败回滚及配置失败后，同一物理连接 SHOW jit仍为原默认值；配置失败不执行业务读取。没有设置会话/全局配置或 enable_*，没有业务内ANALYZE。查询形状与索引不变，未引入新依赖。

## 性能

100,000曲目/10,000发行/8,000组，5,000曲目缺失；混合整版和局部缺失，共7,800可见组。真实普通成员HTTP，5次热身+40正式样本/状态/端点，不剔除异常值。正常构建服务端p95：

| 辅助空表状态 | recent | added_at_desc首页50项 |
| --- | ---: | ---: |
| 未 ANALYZE | 100.959ms | 194.115ms |
| 已 ANALYZE | 104.354ms | 229.816ms |

均通过150ms/300ms门槛；只关事务内JIT即已达标，无需增加SQL形状优化。计划、环境、首读与测量边界见 [catalog-explain.md](./catalog-explain.md)；原始正常构建数据见 [catalog-http-performance-jit-only.md](./catalog-http-performance-jit-only.md)。race报告仅用于规模正确性，不当作性能验收。

## 复现命令

在指定worktree根加载环境，仅连接隔离Compose `roomusic-test-m5f`，随机数据库 `m5f_perf_aa4b2bec2b`、端口36847：

```bash
source /tmp/roomusic-m5f-env
export ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg
export ROOMUSIC_REAL_MUSIC_ROOT="$PWD/backend/testdata/playback"
VERIFY="$PWD/.trellis/tasks/11-04-classical-m5-scan-feedback/verification"
cd backend

ROOMUSIC_CATALOG_EXPLAIN_OUTPUT="$VERIFY/catalog-explain.md" \
  go test ./internal/catalog -run '^TestPostgreSQLRecentAdditionsExplain$' -count=1 -v
ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT="$VERIFY/catalog-http-performance.md" \
  go test ./internal/app -run '^TestPostgreSQLCatalogHTTPPerformance$' -count=1 -parallel=1 -v

export ROOMUSIC_CATALOG_EXPLAIN_OUTPUT="$VERIFY/catalog-explain-race.md"
export ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT="$VERIFY/catalog-http-performance-race.md"
go test -race -json ./internal/library/... ./internal/catalog/... ./internal/app -count=1
go vet ./internal/library/... ./internal/catalog/... ./internal/app
go test -json ./internal/app -run '^TestPackageDependencyDirections$' -count=1
go test -json ./internal/app \
  -run '^TestPostgreSQL(APIResponseSamples|ScanFeedbackAPIResponseSamples)$' -count=2
```

`ROOMUSIC_REAL_MUSIC_ROOT` 实际指向仓库合成fixture，没有读取用户音乐。性能与EXPLAIN正常构建单独执行；完整范围race启用两个规模开关以确保0测试SKIP，输出到独立race文件，不覆盖性能验收。未跑全仓库、前端或Playwright，遵守用户指定步骤1范围。

## 验证结果

- 完整指定范围race：**440个顶层测试、276个PostgreSQL集成测试，0测试SKIP、0失败**。app耗时227.411s；治理42个叶场景通过。
- 最后补强JIT回归前置断言（隔离库默认必须on，不能以原本off掩盖错误），再运行 `go test -race -json ./internal/catalog -run '^TestPostgreSQLCatalogReadLocalJIT' -count=1`：2个既有集成测试再次通过、0SKIP；未增加唯一用例总数。
- 同范围 `go vet` 通过（补强断言后再次运行）；依赖规则1项通过。
- 两套真实handler样本无更新模式 `-count=2`：4次顶层测试执行全部通过、0SKIP；覆盖59份新样本及既有样本，不重写样本。
- gofmt无输出、`git diff --check`通过；未改database-guidelines.md、go.mod、go.sum；无对外形状偏差或待裁决项。
- 逐项测试结果和原始日志SHA256见 [catalog-read-test-results.json](./catalog-read-test-results.json)。无测试文件辅助包的Go package skip不计入“测试SKIP”。

隔离环境已执行 `docker compose --env-file /dev/null -f /home/qlqq/.paseo/worktrees/2i6geckb/m5-scan-feedback/compose.test.yaml -p roomusic-test-m5f down -v`；按项目标签核查，无残留容器或卷。未操作其它Compose项目或用户库。

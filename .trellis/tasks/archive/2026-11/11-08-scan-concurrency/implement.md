# ⑤ 并发解析 · 实施计划

依据：[prd.md](./prd.md)、[design.md](./design.md)。S 线第 4 项，在 ④ 合入 S 线分支之后开始；只有后端与测试，无前端；子 agent 不提交 git。

## 通用约束（派活时原样写进 prompt）

- 测试只做必要的：只补改动直接影响的测试，跑下面的最小相关集；不跑全量，不做演练和重复验证；不为不可能的边界写测试；不为“以防万一”加 recover、重试或回退分支。
- 数据库测试只用隔离的 `roomusic-test` 项目；集成测试必须 PASS 而不是 SKIP。

## 后端（GPT-6-Astra，high）

1. `internal/platform/workpool`：`Ordered`、`Map`（design 3.2）；在 `internal/app/dependency_rules_test.go` 声明新包规则，并允许 `library`、`catalog/ingest` 导入。
2. 配置：`platform/config` 解析 `ROOMUSIC_SCAN_WORKERS`（1–16，默认 4，非法启动失败）；`app` 传入 `library.Service`；`compose.prod.yaml`、`.env.prod.example` 增加该变量（如 `scripts/check-production-compose.sh` 校验清单，同步）。
3. 遍历流水线：`library/scan_pipeline.go`（`scanSlot` 与应用函数）；`scanner.go` 遍历回调改为“生成槽位 → 提交或直接就绪 → 按序应用”，CUE sheet 在遍历 goroutine 解析、父音频未命中缓存时作为任务提交（design 3.3）。
4. 指纹并行：`PlanOptions.Concurrency`；`resolveFingerprints` 每页用 `workpool.Map`，按路径顺序写回（design 3.4）。
5. OGG/Opus 流式解析（design 3.5）；逐个核对其他解析器没有整体读入。
6. 合同同步：`library-scan-contracts.md`、`core0-runtime-contracts.md`、`scan-feedback-contracts.md`、`directory-structure.md`（design 3.7）。

1、2、5 可并行；3、4 依赖 1。

## 测试（GPT-6.1-Sol，与实现分开的 agent）

- 单测：`workpool.Ordered`（结果按提交顺序应用、窗口满时阻塞、`apply` 出错后停止、取消后 worker 退出且不再应用）、`workpool.Map`（顺序、取消）——用 `go test -race`。
- 配置单测：空值默认 4、`1`、`16`、`0`/`17`/`abc` 启动失败。
- OGG：现有 OGG/Opus 样本改动前后观察与指纹逐字段相同；一个大 OGG 的基准或内存测试（`testing.AllocsPerRun` 或 `runtime.MemStats` 峰值）证明内存不随文件大小线性增长。
- 集成（PostgreSQL）：同一份带解析失败、CUE、不支持格式的测试库，分别以并发 1 与 4 扫描，暂存行相对路径序列、诊断序列（含 100 条截断）、扫描结果完全相同；扫描中取消能在一个文件的读取时间内结束且 worker 归零。
- 已有扫描、身份、反馈集成测试不改断言即通过。

### 验证命令（最小相关集）

```bash
cd backend
go test -race ./internal/platform/workpool/ -count=1
go test ./internal/platform/config/ -run 'ScanWorkers' -count=1
go test ./internal/library/parser/ -run 'Ogg|Opus' -count=1
go test ./internal/app/ -run 'TestPackageDependencyDirections' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test -race ./internal/library/ -run 'Concurrent|ScanPipeline|ScanLifecycle|ScanFeedback' -count=1
go vet ./internal/library/... ./internal/catalog/ingest/ ./internal/platform/...
```



### 真实库只读 smoke

在本机隔离实例上对 `music/` 首扫（八号机真实库与 `music/` 的文件清单完全相同，不另在八号机跑）：清空缓存后并发 1 与 4 各一次，记录耗时，用于确认默认值 4 是否合适。

## 风险文件与回滚点

- 风险文件：`library/scanner.go`（遍历回调改为流水线）、`library/parser/ogg.go`。
- 回滚点：第 1–2 步；第 3–4 步；第 5 步独立。部署后可设 `ROOMUSIC_SCAN_WORKERS=1` 立即回到串行。

## 合入前核对

- [ ] worker 只调用解析函数，不访问数据库、不写共享状态（代码审阅确认）。
- [ ] 并发 1 与 4 的暂存与诊断序列一致的集成测试通过。
- [ ] `go test -race` 覆盖工作池与扫描流水线。
- [ ] `compose.prod.yaml`、`.env.prod.example`、运行合同已加 `ROOMUSIC_SCAN_WORKERS`。
- [ ] 与 M 线 ⑩、T 线的约定（新解析器流式、无包级可变状态）已写进其合入核对。

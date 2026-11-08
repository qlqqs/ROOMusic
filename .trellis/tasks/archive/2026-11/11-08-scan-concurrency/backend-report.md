# ⑤ 后端实施回报（2026-11-08）

## 范围与状态

后端实施计划 1–6 已完成；未新增测试、未改已有测试断言（仅更新依赖规则清单）、未启数据库或连接八号机/生产、未提交、未运行前端构建。④ 第 6 步候选签名未触碰。任务保持进行中，新增测试与数据库验收由测试 agent 接手。

## 改动文件

- `backend/internal/platform/workpool/ordered.go`、`map.go`：有界有序池、Flush 与 Map。
- `backend/internal/platform/config/config.go`：ROOMUSIC_SCAN_WORKERS 默认 4、范围 1–16、非法启动失败。
- `backend/internal/app/app.go`、`dependency_rules_test.go`：配置装配与精确依赖白名单。
- `backend/internal/library/service.go`：正式服务默认 4，零值服务保持 1；WithScanConcurrency。
- `backend/internal/library/scanner.go`、新增 `scan_pipeline.go`、`parse_cache.go`：缓存查询、任务提交、按序应用三段分离。
- `backend/internal/library/identity_plan.go`、`backend/internal/catalog/ingest/identity_plan.go`：指纹并发度传递、每页 Map、按路径顺序写回。
- `backend/internal/library/parser/cue.go`：ParseCueSheet 与 ResolveCueDurations 两阶段，同步入口保持原行为。
- `backend/internal/library/parser/ogg.go`、`fingerprint.go`：OGG 两个文件读取入口都改为流式。
- `compose.prod.yaml`、`.env.prod.example`：部署变量与说明；检查脚本没有变量清单，未改。
- `.trellis/spec/backend/library-scan-contracts.md`、`core0-runtime-contracts.md`、`scan-feedback-contracts.md`、`directory-structure.md`：按第 6 步原地同步，未拆分扫描合同。
- 本任务 `design.md`：补用户接受的内存局限、常见输入估算和实施核对；本回报文件。

## 流水线结构

1. 遍历 goroutine 做过滤、链接/普通文件校验和缓存查询；失败/跳过/缓存命中槽位直接 Ready，只有未命中音频提交解析任务。
2. Ordered 固定 workers 个 goroutine，在途窗口为 `2 × workers`，默认 8。FIFO 头未就绪不越过；窗口满等待头部。worker 只调用 ParseAudioFile，不访问数据库或共享计数。
3. 应用仍由遍历 goroutine 依次执行：缓存计数/写入、构造观察、暂存、诊断、HoldSource、跳过与 processed。解析失败保住来源，其余文件继续；基础设施错误沿用原规则。
4. 同一父音频尚在途时，后续物理/CUE 条目先 Flush，再查询缓存，避免缓存命中随并发时机变化。Flush 不关闭 worker；普通无依赖条目不插屏障。
5. CUE sheet 在遍历者读取；唯一父音频先查缓存，未命中项交给一个任务读取；应用时按原 FILE 标识补齐区间、诊断、虚拟观察。没有改聚合方式。
6. 取消后停止提交和应用、丢弃未应用结果，Close 等待已启动解析返回；解析器没有 context，等待当前文件读取结束，不承诺固定毫秒时限。独立指纹读取响应 context；SQL 的 WithoutCancel 和持锁连接合同不变。

## OGG 一致性依据

- `parseOggPacket`、Vorbis 注释处理与文件名兜底规则未改；逐段组装出的完整包及处理顺序不变，继续执行 16 MiB 单包限制。
- 指纹仍按首页段表后的原始前缀识别编码，跳过 Vorbis 三包或 Opus 两包，只哈希后续段负载；页头和分页方式不参与，空音频仍为空指纹。
- 旧解析器未设置 `DurationSeconds`，实际保持零值，不新增 granule 时长。
- 静态输入的字段/指纹一致性有代码审阅依据，现有 parser race 测试已通过；**本次未新增旧/新实现逐字段对照或大文件内存测试**，不能据此宣称该专门验收已完成。
- 已知差别：前段格式错误与后段 I/O 错误并存时，新流式可能先报格式错误；动态半页/半包短读可能从底层 UnexpectedEOF 变为格式截断错误，失败时可能携带部分观察。仍返回失败；页边界短读检查剩余声明长度，不能误报成功。
- 其余格式审计：FLAC 按有界元数据块、MP3 ID3 16 MiB/帧搜索 64 KiB、M4A 单原子 16 MiB/总预算 32 MiB、WAV 有界元数据；其余指纹使用固定缓冲读取。

## 与原设计的差别及裁决

- 新增 Ordered.Flush 和 CUE 两阶段接口，是保持缓存命中确定性及 worker 无数据库访问所需的实现补充。
- 原设计误认为 oggFingerprint 已流式，实际连同该入口一起修复。
- 原设计写最后 granule 计算时长，实际为保留旧结果而不新增时长；动态短读的底层错误顺序差异见上。
- 用户已裁决：OGG 重复注释包累计元数据、极多 FILE 的 CUE 聚合占用属于**已知局限，接受**；保留旧接受范围、不加累计预算、不截断、不改变 CUE 聚合，不列为未完成项。约 130 MiB 只适用于普通单音频槽位估算；常见少量 FILE 的 CUE 要额外计入对应父事实。

## 实际校验命令与结果

以下 Go 命令在 `backend/` 执行：

```bash
gofmt -l .
go build ./...
go vet ./internal/platform/... ./internal/library/... ./internal/catalog/... ./internal/app/...
env -u ROOMUSIC_TEST_DATABASE_URL go test -race ./internal/platform/workpool/ ./internal/library/... ./internal/platform/config/ -count=1
env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

全部退出 0，gofmt 无输出。race 中 library、library/httpapi、library/parser、platform/config 均通过；workpool 与 parsertest 为 `[no test files]`。没有已有测试失败，不需要测试 agent 修正旧断言。数据库变量显式移除，数据库集成测试未验证，不把 SKIP 当作通过。

仓库根目录：

```bash
scripts/check-production-compose.sh
git diff --check
```

Compose 仅用假值解析配置，输出“生产 Compose 静态校验通过”；diff 检查无空白错误。`backend/cmd/roomusic/web/` 未改动，无需还原。

未跑全量测试、真实库 smoke 或数据库演练，按本次最小验证与环境限制执行。构建全包是用户指定门禁，不代表执行全量测试。

## 测试 agent 交接

- 补 workpool Ordered/Map：顺序、Ready 混排、窗口、Flush 后继续提交、apply 首错、取消后未应用且 worker 退出；用 race。
- 补配置：空值/default 4、1/16、0/17/abc 与固定错误字符串。
- PostgreSQL 比较 workers 1/4：冷/热缓存与 full，暂存顺序、诊断及 100 条截断、解析/缓存命中计数、HoldSource、反馈与结果。重点覆盖 CUE 在父文件前后、跨目录、多个 sheet 共父、混合诊断及取消。
- 指纹注入桩须并发安全；验证每页有界并行、结果/失败诊断按路径顺序写回。
- OGG/Opus 新旧逐字段与指纹对照：跨页、多包/空包、未知首包、16 MiB 包边界、截断段表/负载、未结束包与尾部垃圾组合、动态短读始终失败。
- 对普通结构大 OGG 的解析入口和独立指纹入口分别测峰值内存与取消；不要把已接受的重复注释包累计或极多 FILE CUE 当作本次待修复项。流式算法累计分配量可随读取长度增长，峰值/存活内存才是这里的验证目标。

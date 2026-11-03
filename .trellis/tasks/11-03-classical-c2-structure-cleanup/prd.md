# C2 结构整理

## 来源

2026-11-03 [路线调整](../../../docs/architecture/core-1-roadmap.md#2026-11-03-路线调整)的 C2：代码评估认为可读性的主要短板是
复杂度集中——`internal/catalog` 约 9.2k 行、五项治理功能挤在一个包里，`player-engine.ts` 1281 行靠一组布尔标记维持隐式状态，
`library/parser/parser.go` 2117 行。本任务是**纯重构**，为以后的功能降低成本。

## 范围

| 编号 | 交付 |
| --- | --- |
| S1 | `internal/catalog` 拆成三个包：`catalog`（读取核心）、`catalog/ingest`（扫描写入）、`catalog/governance`（修正、归组、默认版本、问题核对与认领）；只供 catalog 子树使用的 SQL 片段放进 `catalog/internal/releasesql`；依赖规则测试同步 |
| S2 | 播放引擎改成显式状态（可辨识联合），并把与状态无关的部分拆成独立模块；先补特征测试再改实现 |
| S3 | `library/parser/parser.go` 在同一个包内按格式与职责拆成多个文件，导出符号零变化 |

## 验收

| 编号 | 验收 |
| --- | --- |
| A1 | 行为不变：`backend/internal/app/testdata/api-samples` 的接口样本逐字比较通过；后端 `go test -race` 覆盖改动涉及的包，PostgreSQL 集成用例在隔离库上实际运行（PASS 而非 SKIP） |
| A2 | `catalog` 不再导入 `operations` 与 `catalog/metadata`；`catalog` 不导入 `ingest`、`governance`；依赖规则测试为每个新包声明规则并通过 |
| A3 | `catalog` 的公开面只为拆分增加必要的符号（目标十个左右），SQL 片段不出现在 catalog 子树之外可导入的包里 |
| A4 | 播放引擎：`attached`/`finished`/`restoreStarted`、`restoring`/`restoreRetry`、加载管线的六个字段被显式状态取代；`player-engine.ts` 的公开导出、`EngineDeps`、`PlayerSnapshot` 与事件不变；新增的特征测试先在旧实现上通过，重构后照样通过 |
| A5 | 前端 lint、typecheck、全量 Vitest、Playwright（路由模拟）通过；内嵌产物重新构建并与提交一致 |
| A6 | `parser.go` 拆分后包外无任何改动，解析器测试通过 |
| A7 | 规范与文档里引用的旧文件路径、`catalog.X` 符号同步到新位置；改动的 Markdown 无断链；路线图 C2 行更新 |

## 已知、但本任务不修的行为

特征测试会把它们按**现状**固定下来，修正另行决定：

- 第一次 attach 之前就有命令时，恢复会停在 `restoring = true`，保存一直被挡住，直到下一次操作。
- 恢复进入可重试状态后调用 `retry()`，如果保存的队列已经没了，快照里会留着旧的 fault。
- 单曲循环重播被浏览器拒绝自动播放后，状态已是 paused，定时保存仍在运行。

## 范围外

任何行为修复、接口或 SQL 改动、`catalog/httpapi` 的拆分、新的测试框架或测试专用包。

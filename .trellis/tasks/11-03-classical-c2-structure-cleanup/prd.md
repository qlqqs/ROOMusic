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

## 实施结果（2026-11-03）

后端由 `codex/gpt-6-astra` 实施，前端重构由 `claude/claude-opus-5-5` 实施，特征测试与验收测试由 `codex/gpt-6-astra` 编写和执行；主会话逐步审阅 diff 与测试输出后提交。

| 提交 | 内容 |
| --- | --- |
| `207d1aa` | 播放引擎的 16 个特征测试（F1） |
| `7de47c4` | B1：拆出 `catalog/ingest`（10 个文件、4 个测试） |
| `eb4ef50` | B2：`fields.go` 下沉字段词表；`catalog/internal/releasesql` 收 21 个 SQL 片段、2 个限额常量与 7 个格式化函数 |
| `654c8d7` | B3：移出 `catalog/governance`（10 个文件、12 个测试），`governance.Store` 承接 9 个治理读取方法 |
| `3b20141` | 播放引擎显式状态与模块拆分（F2、F3），重建内嵌产物 |
| `b99c246` | B4：`parser.go` 拆成 10 个文件 |
| `a472208` | B5：12 份文档的路径与符号同步 |
| `4fec4b0` | 真实服务用例同步 C1 的播放合同（收尾验证发现） |

| 编号 | 结果 |
| --- | --- |
| A1 | 每一步都在隔离 PostgreSQL 上跑 `go test -race`（catalog、library、app，B3 加 governance），214 个集成用例 PASS、零 SKIP；140 个接口样本 SHA-256 不变；B2、B3 的声明语法树与字符串字面量在统一名字后一致 |
| A2 | `go list` 确认 `catalog` 只导入 `catalog/internal/releasesql` 与 `platform/uuid`，不再导入 `operations`、`metadata`、`ingest`、`governance`；依赖规则测试为三个新包声明规则并通过 |
| A3 | `catalog` 为拆分只新增 6 个顶层导出（`CorrectableField`、`CorrectableFields`、`LoadCorrectionIntents`、`MaxEvidenceSourceRefs`、`MaxReleaseEvidence`、`ReleaseSummaryColumns`）；SQL 片段只在 `catalog/internal/releasesql`。catalog 根目录从 18 个文件 5099 行降到 8 个文件 1534 行 |
| A4 | `Lifecycle`、`RestorePhase`、`Pipeline`（status 收进各变体）取代 `attached`、`finished`、`restoreStarted`、`restoring`、`restoreRetry` 与管线六个字段；`player-engine.ts` 1281 → 1159 行，另拆出 5 个模块；`player-engine.test.ts` 的原有断言零改动，16 个特征测试在重构前后都通过 |
| A5 | 导出树上：lint、typecheck、全量 Vitest 78 个文件 1649 项、构建通过，内嵌产物 119 个文件与提交逐个一致；Playwright（路由模拟）246 项通过 |
| A6 | `parser.go` 拆分前后声明、注释与字符串逐字节一致，`go doc -all` 输出不变；解析器 241 项测试通过 |
| A7 | 12 份文档的旧路径与符号已同步，157 个相对链接与 10 个锚点有效 |

收尾验证（导出树，`a472208`）：后端 6 组包 477 个顶层测试通过，其中 245 个 PostgreSQL 集成用例零 SKIP（只有 3 个真实音乐的 opt-in 用例跳过）。真实服务用例第一次失败 2 个：撤销会话的用例仍要求 C1 已删除的在途切断，另一个是它引起的素材计数连锁失败。修正测试后，在 `206d4e0` 加修正的导出树上 44 项全部通过（播放 23、治理 4、默认版本 6、问题 5、认领 6）。另有一次服务启动超时，重跑后没有复现，原因未查明。

与设计的偏差：
- F2、F3 合成一个提交，因为两步是在同一份工作区里连续完成的。
- 订阅回调里同步调用 `pause()` 时，旧实现仍会发出一次结果会被丢弃的预检，新实现直接中止。界面不会在回调里发命令，走不到这条路径。
- 核心的 comparison 测试改用核心自己的夹具，默认版本改用 SQL 直接准备。

验证中看到、但不属于本任务的问题：`npm ci` 报告 17 项依赖漏洞（13 moderate、4 high）；主 JS 包 559.57 kB，超过 500 kB 的构建警告。

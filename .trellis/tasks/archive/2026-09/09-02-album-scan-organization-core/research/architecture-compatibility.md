# V0 整理范围与当前架构兼容性

## 结论

建议范围与当前长期架构没有根本冲突，但必须区分三类行为：

1. V0 coverage-first 扫描整理母线与当前 Core 0 的确定性 scanner、evidence-linked Release Graph 完全同向。
2. 字段级 metadata 人工确认、overlay 和 revert 符合既定 Change Set/Operation Journal 方向，但超出了已完成 Core 0 的阶段范围，当前代码也尚无 overlay 存储与 resolved projection，需要作为新的可验收能力实现。
3. Release/Medium/Track 的人工重新归组、ReleaseGroup merge/split 属于图谱拓扑治理，不是普通字段 overlay。它虽可在长期架构内实现，但当前规格明确延后，不应混入本任务首个闭环。

用户最终选择本任务只恢复 V0 自动整理。因此选定边界是：coverage-first 地生成可用 base graph，持久化 evidence/uncertainty，并提供只读整理结果和问题视图；不实现字段级人工 overlay/revert，也不实现结构归组 hard merge/split 或 AI 一键整理。这个范围是三类行为中与当前 Core 0 架构最直接兼容、依赖最少的一层。

## 架构证据

- 产品目标明确要求确定性 parsing、identity、Release grouping 与 Music Steward 分离；可靠规则仍由程序拥有（`.trellis/spec/guides/product-goals.md:51-55`）。
- Core 0 已将只读扫描、evidence-linked Release Graph、PostgreSQL authority 与版本化 REST 固化为当前合同（`.trellis/spec/guides/product-goals.md:76-91`）。
- 后端永久拥有 authorization、transaction、revision、idempotency 与 recovery；音乐源保持只读，source evidence 必须可检查（`.trellis/spec/guides/product-goals.md:96-113`）。
- 模块化单体已经分别定义 Library/Scanner、Release Graph 与 Operations 的所有权；scanner 不应绕过 catalog invariants 直接写图谱（`.trellis/spec/guides/modular-design.md:22-37`、`:104-110`）。
- 所有用户发起的持久化管理变更应经过 Change Set/Operation Journal；`metadata_overlay_apply` 已被列为未来受控 capability 示例（`.trellis/spec/backend/agent-and-operation-guidelines.md:75-97`、`:99-120`）。
- 已完成的 Core 0 PRD 明确把人工 metadata overlay 与图谱 merge/split 排除在当时阶段之外（`.trellis/tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md:69-72`、`:118-137`）。这属于阶段范围限制，不是长期架构禁止。

## 当前实现缺口

- `releases`、`media`、`tracks` 目前直接保存 scanner 计算出的展示值，没有独立 base/effective metadata 或可编辑 aggregate revision（`backend/migrations/0002_core_slice.sql:34-61`）。
- `track_observations` 只有 track 级 `field_name/value/source_kind/inferred`，尚不能承载 V0 的 candidate、confidence、rule id、action、confirmation status 与 correction path，也没有 release/medium 级 evidence（`backend/migrations/0003_catalog_observations.sql:25-32`、`0004_field_provenance.sql:1-6`）。
- 已实现的 operation journal 仅是目录专用 `library_root_operations`，不能直接冒充 metadata Change Set（`backend/migrations/0007_root_operations.sql:4-23`）。
- Release 列表和详情直接查询 base tables，没有 resolved/effective projection（`backend/cmd/roomusic/application.go:315-366`、`:423-475`）。
- 当前 scanner 在遍历时直接查写 Release Graph 表（`backend/cmd/roomusic/scanner.go:371-425`），与规划中的模块所有权边界仍有差距；迁移 V0 整理母线时应顺势建立 parser evidence、candidate grouping、catalog persistence 的窄合同，不能在现有大函数中继续叠加 overlay 分支。

这些都是需要新增或重构的实现基础，不构成架构冲突。

## 兼容的数据流

```text
只读音乐文件
  -> parser observations
  -> V0 coverage-first grouping / field decisions
  -> scanner-owned base graph + immutable/rebuildable evidence
  -> catalog resolved projection
       = base value + active field overlay（若存在）
  -> REST DTO
  -> 整理工作台
```

未来字段修正应走另一条受控写入路径，但不属于本任务：

```text
管理员修正意图
  -> typed REST command
  -> auth + entity/field validation
  -> expected revision + idempotency + base evidence stale guard
  -> transaction: overlay + before/after + operation journal
  -> resolved projection
```

本任务没有人工 overlay；重扫根据最新源文件、parser 和整理规则重建或更新 base graph/evidence。未来若加入人工 overlay，重扫不得覆盖有效人工值，人工 revert 也必须通过明确逆操作完成。

## 必须避免的冲突实现

- 直接 UPDATE scanner-owned `releases.title`、`tracks.artist` 等 base 字段作为人工修正；重扫会覆盖或混淆来源。
- 把人工值伪装成 tag observation，或改写历史 evidence 来让结果“看起来正确”。
- 把 V0 GraphQL、Redis/asynq、Meilisearch、AI worker 或旧 metadata repository 整体搬入当前 REST/PostgreSQL-only 闭环。
- 复用 `library_root_operations` 保存 metadata 操作，导致目录与 catalog operation 语义耦合。
- 将字段 overlay 扩张成 Track 移动、Release merge/split 或跨目录 ReleaseGroup hard merge，却没有 topology revision、影响范围、stale guard 和逆操作合同。

## 范围拆分结论

用户选择自动整理后，本任务可作为一个跨层纵向任务完成，不应提前创建 overlay 或 topology 子任务。内部实施仍按以下依赖顺序验收：

1. **V0 自动整理母线与 evidence**：恢复 parser -> candidate grouping -> field decision -> graph persistence；产出稳定 base graph 和问题投影。
2. **只读 REST 投影**：依赖第 1 项的稳定 entity/field identity，投影 base value、evidence、uncertainty、diagnostic 与扫描状态。
3. **现有浏览界面的只读整理入口**：依赖第 2 项的 REST 合同，在 Release 列表、详情和管理员扫描区展示结果、不确定项及“修正源文件后重扫”的建议，不建设独立工作台。

字段 overlay、图谱拓扑人工治理与 AI 一键整理分别另立后续任务，并在其任务文档中显式声明依赖本任务产出的稳定 entity/field identity 与 evidence contract。

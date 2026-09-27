# M0 执行计划

## 实施前审阅门禁

- [x] 创建父子任务，加载工作流和前后端规范索引。
- [x] 保存仓库事实、上一轮候选路线与原型差异。
- [x] 编写 PRD、文档设计、执行计划及非空上下文清单。
- [x] 用户于 2026-09-27 回复“批准”，确认 R1–R6 的文档整理范围。
- [x] 已在批准后运行 `task.py start`；范围若有实质变化，更新规划后再次审阅。

## 有序工作清单

- [x] 逐页核对现有 API，能力矩阵落于 `docs/architecture/core-1-roadmap.md`；分开当前事实与候选能力。
- [x] 完成 `frontend/DESIGN.md` 的原型对照、组件/状态与视觉证据边界审查。
- [x] 形成 Core 1 范围提案及较小替代；M1 使用现有 API、B0 保持行为、M4 依赖写路径，M6 不自动纳入。
- [x] 四类 ADR 草案完成官方事实核验与交叉一致性检查，仍明确待批准。
- [x] 对齐 frontend 规范、产品阶段、架构摘要、运行合同及 README 的现状描述，保留安全合同。
- [x] 记录旧任务与队列语义承接，不归档旧任务、不改设计源文件。
- [x] 更新父任务映射并通过全范围文档审查，提交用户决策，不进入 M1/B0。

默认由主会话协调、决策沟通和规范更新；若任务实施需分派，遵循当时生效的 Trellis
上下文加载与审查流程。创建阶段未分派；本轮批准实施后可按不重叠文档边界分派，主会话负责规范同步和集成。

## 实施期验证命令与人工检查（归档前记录）

```bash
python3 .trellis/scripts/task.py validate .trellis/tasks/09-27-classical-core-1
python3 .trellis/scripts/task.py validate .trellis/tasks/09-27-classical-m0-alignment
python3 .trellis/scripts/task.py list
python3 .trellis/scripts/task.py current --source
git diff --check
```

- 检查父子双向关系、父任务 `planning` / M0 `in_progress` 状态、上下文条目路径、Markdown 相对链接和新文档空白错误。
- 搜索 `frontend/DESIGN.md`、`月刊`、`design-previews`、`frontend/src/main.tsx` 等引用，区分历史说明与错误的当前指令，不要求机械零匹配。
- 按 R1–R6 复核：未把候选 ADR 标为采纳，未误改旧任务完成状态，未产生业务代码或构建产物。
- 对照 Git 变更范围；新增未跟踪文件同样检查，不能只依赖 `git diff --check`。
- 仅文档变更不运行 Go/npm/数据库全量测试；若出现业务代码修改，应退回范围审阅而非继续扩大验证。

## 风险文件与回退点

- `product-goals.md` 与运行合同：必须保留只读源文件、后端权威、保守分组及完整扫描才作缺失对账。
- 前端状态/播放器规范：迁移队列语义，不把旧模拟实现写成真实播放方案。
- 旧任务：保留其历史状态和批准记录，归档需单独确认。
- 每一类文档修改保持可单独审阅；有未决产品问题时标为提案并停在决策边界。

## 实施结果与收尾

R1–R6 的文档交付及最终独立检查已完成，证据见 [验证记录](verification.md)。
用户于 2026-09-27 回复“提交”，已确认 [提交计划](commit-plan.md)。工作提交 `850d274`
已完成，M0 已归档为 `completed`；四类 ADR 与 Core 1 范围仍待采纳，不启动 M1/B0。
归档后的验证路径和收尾证据见 [验证记录](verification.md)。

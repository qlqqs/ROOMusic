# 执行计划：界面交互与细节第二轮整理

纯前端任务。按用户 2026-11-14 同意的分工：五个界面区由 `trellis-implement` 子代理并行实现（各自独立工作目录与分支，只写实现、不写测试、不提交 Git），主代理审阅后派 `trellis-check` 子代理补测试，再由主代理提交并依次合入任务分支。

- 任务分支：`feat/ui-interaction-polish`，工作目录 `../ROOMusic-ui-interaction-polish`。
- 分区分支：`feat/uip-a-album`、`feat/uip-b-player`、`feat/uip-c-shell`、`feat/uip-d-overview`、`feat/uip-e-settings`，工作目录 `../ROOMusic-uip-<区>`，都从任务分支的规划提交拉出。
- 工具链：`mise exec --`；各工作目录的 `frontend/node_modules` 链接到主仓库的同一份依赖（依赖不变）。

## 阶段

### 阶段 1：分区实现（并行）

- [x] A 专辑页与列表：第 1、5、9、12、13、14、16、20、21、22、23 条（design.md 第 2 节）
- [x] B 播放控件与全屏：第 4、32–35 条（第 3 节）
- [x] C 侧栏与手机导航：第 3、24–27 条（第 4 节）
- [x] D 概览：第 11、15、29–31 条（第 5 节）
- [x] E 设置与文案：第 6–8、10、17–19 条（第 6 节）
- [x] 第 2 条：真实后端复现并记录结论（第 7 节）

每区完成时：`npm run lint`、`npm run typecheck`、受影响的单元测试通过；桌面 1440 与手机 390 截图自查。

### 阶段 2：每区审阅与测试

- [x] 主代理审阅各区改动（对照预览与 design.md）。
- [x] 每区派一个 `trellis-check`：更新受影响的单元测试与 e2e（读屏名、角色、结构变化），为新增行为补测试；只重跑受影响的测试。
- [x] 主代理提交该区，合入任务分支，解决冲突。

### 阶段 3：收尾

- [x] 更新 `frontend/DESIGN-components.md`、`frontend/DESIGN.md`、`.trellis/spec/frontend/component-catalog.md`、`player-design-guidelines.md`、`interface-layout-guidelines.md`。
- [x] 合并后跑一次全量单元测试与 e2e；首次失败修复后仅定向复验，详情见最终验收报告。
- [x] `npm run build` 更新 `backend/cmd/roomusic/web` 产物并提交。
- [x] 清理分区工作目录与分支。

## 验证命令

```bash
cd frontend
mise exec -- npm run lint
mise exec -- npm run typecheck
mise exec -- npx vitest run <受影响的测试文件>
mise exec -- npx playwright test <受影响的 spec>   # 阶段 2 起
mise exec -- npm run test && mise exec -- npm run test:e2e   # 阶段 3，合并后一次
mise exec -- npm run build
```

## 风险与回退点

- 迷你播放栏与底部标签栏（B、C 交界）：位置只由 C 改，B 不动迷你栏纵向位置。
- `track-actions.tsx`、`album-card.tsx` 被多页使用：只由 A 改，D 沿用。
- 每区独立提交，出问题可单独回退。

## 2026-11-15 合并进度

A 区已完成检查并与 B–E 集成，最近新增卡片播放已接通。A 区规范已同步，合并交界的 129 条单测与 16 条浏览器测试通过，生产构建产物更新。完整命令与未完成项见 [A 区合并验证](verification/a-merge.md)；阶段 3 的整体门禁与剩余规范同步尚未完成，任务保持进行中。

## 2026-11-15 最终验收

功能及自动化门禁完成，生产构建与前端规范已同步。单测 4026 通过、1 条条件跳过，模拟后端浏览器 592 通过，相关真实服务 15/15 通过；计数复用全量基线与定向复验。35 项已逐条截图核对，与样机的落地差异明确保留。见 [最终验收汇总](verification/final-acceptance.md)。主分支集成、推送及分区清理尚未执行，任务保持进行中。

## 2026-11-15 主分支集成收尾

用户要求合入 main 并推送，已通过合并提交 `f0b8c0fe` 完成并推送。合并没有冲突，前后端文件与最终验收提交完全一致，复用既有测试结果。六个任务及分区工作目录和对应本地分支已清理；日志、截图和主工作区原有未跟踪任务资料保留在 `temp/archive/2026-11-15/ui-interaction-polish/`。任务随本次收尾归档。

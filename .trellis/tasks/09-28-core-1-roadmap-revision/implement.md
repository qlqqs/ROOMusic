# 执行计划

## 启动前门禁

- [x] 创建任务并挂到父任务 `09-27-classical-core-1`；归档评估记录。
- [x] D1–D6 由用户于 2026-09-28 裁决，记入 PRD。
- [x] `design.md`、本文件与 `implement.jsonl`（6 条）、`check.jsonl`（5 条）就绪，`task.py validate` 通过。
- [x] 用户于 2026-09-28 回复“批准”后，已运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/09-28-core-1-roadmap-revision`。

## 有序清单

主会话负责文档修改与决策同步；全部完成后按 Trellis 派发协议调用 `trellis-check` 子代理做全范围一致性检查，派发提示以 `Active task: .trellis/tasks/09-28-core-1-roadmap-revision` 开头。

1. [x] **路线图**：按 [design.md](design.md) 第一部分改写 `docs/architecture/core-1-roadmap.md`：阶段表、Core 1 截止范围、依赖与验收出口、ADR 门禁状态列、旧任务节，并链接评估记录与架构图。
2. [x] **ADR**：0001–0004 各加“决策记录”，并改写受影响段落（0002 的第四、六、八、九、十节；0001 第 8 节清单；0003 第九节第 1 项；0004 第 11 节第 1 项）。
3. [x] **规范与设计依据**：`product-goals.md:109`、`hook-guidelines.md:11`–`13`、`frontend/DESIGN.md:113`–`116` 与 `:270`、`architecture-decisions.md`（Core 1 节 + Extension boundary 两步走）。DESIGN.md 另在文首注明 M1、M2 的拆分，并修正第 170、172、288 行与裁决冲突的表述。
4. [x] **架构图**：更新状态编码与文案，新增“06 扩展边界”；用 Playwright 无头 Chromium 在 1440 与 820 宽度渲染，没有 `pageerror`，连线数与定义一致（9/5/8/13/7），节点内容无溢出。
5. [x] **父任务**：`prd.md` 阶段映射（含 R9）与决策记录、`design.md` 兼容节、`implement.md` 门禁、`task.json` 备注。
6. [x] **旧任务**：实际命令（`set-meta` 的格式是 `<目录> <键> <值>`，不是 `键=值`）：
   ```bash
   python3 ./.trellis/scripts/task.py set-meta .trellis/tasks/09-04-music-client-experience outcome superseded
   python3 ./.trellis/scripts/task.py set-meta .trellis/tasks/09-04-music-client-experience superseded_by 09-27-classical-core-1
   python3 ./.trellis/scripts/task.py archive 09-04-music-client-experience --no-commit
   ```
   其 `prd.md` 顶部已加取代说明（链接使用归档后的相对路径）；归档时只有“分支已不存在”的警告；
   归档后把其 `implement.jsonl`、`check.jsonl` 中的路径改为归档位置，`task.py validate` 通过。
7. [x] **新建子任务**：已用 `--no-start` 创建 `09-28-classical-b0-package-split`、`09-28-classical-i-identity-groups`、
   `09-28-classical-m1a-shell-admin`、`09-28-classical-m1b-album-browsing`、`09-28-classical-f1-api-json-404`、
   `09-28-classical-f2-root-op-idempotency`，当前任务未被切走。各自 PRD 初稿取自 design.md 第二部分；
   另为每个子任务预填了上下文清单种子，使 `task.py validate` 通过，各子任务规划时再调整。
8. [x] **全范围检查**：下方验证命令全部通过；134 条相对链接有效（DESIGN.md 中两条 `%20` 编码链接解码后存在）。
   派发的 `trellis-check` 子代理因额度限制在读取 diff 时中断，未改动任何文件；改由主会话按同一清单自查：
   四份 ADR 状态均为“部分批准”；Core 1 范围表述在路线图、架构摘要、product-goals、父任务与架构图中一致；
   无私有路径；父任务链接的六个子任务目录均存在；无代码、依赖或 CI 改动。自查中修正了 `frontend/DESIGN.md`
   末尾“仍待确认”清单的第 1、5 项，使之反映 D3、D4、D5。
9. [ ] **收尾（Phase 3）**：写 `commit-plan.md` 请用户确认提交；提交后按流程归档本任务。归档后，把六个子任务和父任务中指向 `../09-28-core-1-roadmap-revision/` 的链接改为 `../archive/2026-09/09-28-core-1-roadmap-revision/`。

## 验证命令

```bash
python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-28-core-1-roadmap-revision
python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-27-classical-core-1
python3 ./.trellis/scripts/task.py list
git diff --check
git status --short
grep -rn -E '解析器 → 平台|M1 先沿用 Release API|尚未安装\s*或采纳|最小播放历史' docs .trellis/spec frontend/DESIGN.md .trellis/tasks/09-27-classical-core-1
```

- 对本次修改和新增的 Markdown 做相对链接检查，确认目标文件存在。
- 最后一条 `grep` 应当只剩历史说明，不能出现在“当前指令”的位置。
- `git status` 只能包含 `docs/`、`.trellis/`、`frontend/DESIGN.md`，不能出现代码、锁文件、CI 或 Compose 的改动。
- 纯文档任务，不运行 Go、npm 或数据库测试。

## 风险与回退点

- ADR 只标注被裁决的项，不能整体改成“已采纳”；未裁决项保持“待批准”。
- 旧任务归档后状态字段会显示 `completed`，必须依靠 meta 与 PRD 说明表达“已取代”，不能写成已交付。
- 新建子任务一律使用 `--no-start`；如有误建，用 `task.py remove-subtask` 解除关联后删除目录。
- 每类文件都可用 `git checkout -- <文件>` 单独回退；归档可通过 git 恢复目录与 `task.json`。

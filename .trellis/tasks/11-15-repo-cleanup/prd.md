# 仓库整洁（意见 9、10、11）

## Goal

公开仓库只保留实际使用的 AI 工具配置与长期文档，过程性的 Trellis 归档留在本机；设计原型目录名拼写正确、文件名不含空格。

## 裁决

- 归档处理（2026-11-15 用户选择，人工裁决）：现有 `.trellis/tasks/archive/` 取消跟踪并加入 `.gitignore`，本机文件保留；Git 历史不改写。
- 删除 `.cursor`：全局约定只使用 Claude 与 Codex（用户批准“1-11 一起做”）。

## 已确认事实

- `.claude/skills/trellis-brainstorm/SKILL.md` 缺少 `.agents/skills/` 同名文件中的 “Final Planning Decision” 一节（2026-09-02 提交 `docs(trellis): require explicit planning decisions` 只改了一份）；`trellis-update-spec` 的差异是平台命令写法（`/trellis:x` 与 “Trellis command”），属于平台差异，不算漂移。
- `.trellis/.template-hashes.json` 含 52 条 `.cursor` 条目。
- Trellis 归档时对被忽略路径 `git add` 失败会告警并跳过自动提交（`.trellis/scripts/common/safe_commit.py`），源任务目录的删除需手动提交。
- spec、docs、README、`frontend/*.md` 中有 44 处链接指向 `tasks/archive`。
- `frontend-desgin/` 被 `.trellis/spec/frontend/player-design-guidelines.md`、`.trellis/spec/frontend/index.md`、`frontend/DESIGN.md`、`frontend/src/styles/tokens.css`、`docs/architecture/architecture-decisions.md`、`docs/architecture/core-1-roadmap.md`、`docs/architecture/adr/0001-frontend-foundations.md` 引用。

## Requirements

1. `git rm -r .cursor`；删除 `.trellis/.template-hashes.json` 中的 `.cursor` 条目。
2. 把 `.agents/skills/trellis-brainstorm/SKILL.md` 的 “Final Planning Decision” 一节及第 9 步的对应改写同步到 `.claude/skills/trellis-brainstorm/SKILL.md`；逐个比较 `.claude/skills`、`.agents/skills`、`.codex` 中同名技能，除平台命令写法外的内容差异一并同步，并在汇报中列出。
3. `git rm -r --cached .trellis/tasks/archive`；`.gitignore` 用 `/.trellis/tasks/archive/` 替换现有的单个归档子目录忽略行。
4. 指向归档的链接改为不带链接的路径文字，并注明“本机归档”，例如：`本机归档 .trellis/tasks/archive/2026-10/10-09-classical-m2b-lyrics/prd.md`。不删除引用信息本身。
5. 在 `docs/development-environment.md` 增加一小段：归档不入库；`task.py archive` 跳过自动提交时，手动提交源任务目录的删除（`git add -A .trellis/tasks/<任务目录>` 后提交）。
6. `frontend-desgin/` 改为 `docs/design/`，带空格的文件名改为英文短横线名（`ROOMusic 原型.dc.html` → `roomusic-prototype.dc.html`，`ROOMusic 设计探索.dc.html` → `roomusic-design-exploration.dc.html`）；用 `git mv` 保留历史；更新上列全部引用，以及原型文件之间、对 `support.js`、`_ds/` 的相对引用。

## Acceptance Criteria

- [ ] `git ls-files .cursor .trellis/tasks/archive` 为空，本机 `.trellis/tasks/archive/` 文件仍在。
- [ ] `git ls-files | grep -c ' '` 中不再有设计原型文件；`grep -rn 'frontend-desgin'`（排除 `.git` 与本机归档）无结果。
- [ ] `grep -rn '](.*tasks/archive'` 在已跟踪文件中无结果。
- [ ] `.claude/skills/trellis-brainstorm/SKILL.md` 含 “Final Planning Decision” 一节。
- [ ] `python3 ./.trellis/scripts/task.py list` 正常工作。
- [ ] 打开 `docs/design/roomusic-prototype.dc.html` 时引用的 `support.js` 与 `_ds/` 路径有效（检查相对路径即可）。

## Out of Scope

- 改写 Git 历史、清理 `.trellis/workspace` 日志。

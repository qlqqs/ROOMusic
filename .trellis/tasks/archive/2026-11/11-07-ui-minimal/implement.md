# 执行计划：界面极简

纯前端子任务，只写 `prd.md` 和本计划，不另写 `design.md`。具体数值（字阶、间距、按钮外观）由实施者在第 1、2 步定下，写进 `frontend/DESIGN.md`，再通过截图审阅。

## 分工

| 工作 | 执行者 |
| --- | --- |
| 实现，以及 typecheck、lint、所改文件已有的 Vitest | 前端 Opus（`claude/claude-opus-5-5`，high） |
| 补改测试、跑受影响的 Vitest 和 Playwright、拍 1440 和 375 截图 | Sol（`codex/gpt-6.1-sol`，high，full-access） |
| 审阅 diff 和截图、提交 | 主会话 |

## 步骤

1. **设计变量与字体**（`frontend/src/styles/tokens.css`、`base.css`、`fonts.ts`、`package.json`、`package-lock.json`）：
   - 删除三个 `@fontsource-variable/*` 依赖和 `fonts.ts` 里的引入；
   - `--font-heading`、`--font-body` 改成无衬线字体栈，时长、计数这类数字加 `font-variant-numeric: tabular-nums`；
   - 重定字阶：正文 14px，次要文字 12–13px，页面标题不超过 24px；
   - 收紧间距和控件高度，触屏和窄屏仍保持 44px；
   - 同步 `frontend/DESIGN.md` 第 3.2、3.3 节。
2. **共享组件**（`frontend/src/shared/ui`）：
   - `Button` 分三种外观：主要（保留按钮外观）、次要文字按钮（无边框，悬停或按下才有浅底）、只有图标（无边框，必须有读屏名，悬停有提示）；
   - `MenuButton` 支持只有图标的触发按钮；
   - `StatusTag` 去边框，改淡底色；
   - `Panel`、卡片和分隔线变淡，`Page` 标题用新字阶；
   - 同步 `frontend/DESIGN-components.md`。
3. **曲目行**：
   - `frontend/src/app/routes/track-actions.tsx` 改成三个图标按钮：播放、心形、更多。读屏名和悬停提示保留原意，例如“从这首开始播放”“更多操作”；
   - 各列表的行布局改成图标在标题行右侧、时长左边，涉及专辑详情（`features/catalog/components/release-media.tsx`）、收藏、歌单详情、历史、艺人详情、搜索结果、正在播放；
   - 窄屏下标题省略或换行，图标和时长不折行。
4. **播放条**（`features/player/components/player-controls.tsx` 及样式）：
   - 播放模式改成一个图标按钮，按“顺序 → 列表循环 → 单曲循环 → 随机”循环切换，读屏名和悬停提示是当前模式名；
   - 静音按钮图标 16px、无边框；
   - 上一首、下一首无边框，播放键可以保留实心圆，作为播放条的主要操作。
5. **艺人概况**：`features/catalog/components/artist-meta.tsx` 去掉身份部分；改完核对艺人列表、艺人详情、搜索结果和候选页四处的显示。
6. **新建成员弹窗**：
   - 成员页放一个“新建成员”按钮，点开 `ModalDialog`，里面是 `create-member-form`；
   - 成功后关闭弹窗，在列表上方提示；失败时把错误显示在弹窗里。
7. **逐页审视主要操作**：每个区块只留一个主要按钮，其余用次要外观。例如专辑详情的“播放整张专辑”、成员页的“新建成员”、扫描目录的“添加”。
8. **构建**：`npm run build` 更新 `backend/cmd/roomusic/web`，和源码放在同一个提交里。

## 验证

- 实施者：`cd frontend && npm run typecheck && npm run lint`，再跑所改组件的 Vitest 文件。
- Sol 只跑受影响的用例，不跑全量：
  - Vitest：曲目动作、播放模式循环、静音按钮、成员弹窗、艺人概况、`shared/ui` 中改动的组件；
  - Playwright：`e2e/player.spec.ts`、`e2e/members.spec.ts`、`e2e/albums.spec.ts`、`e2e/a11y.spec.ts`、`e2e/responsive.spec.ts`，以及因选择器或文字变化失败的其他 spec；
  - 用临时 spec 拍 1440 和 375 截图，与 `../11-07-ui-minimal-and-catalog-fixes/research/baseline/` 对照。截图放 `research/after/`，临时 spec 不提交。
- 运行 Playwright 前先 `source /home/qlqq/.local/share/playwright-deps/env.sh`；结束后 `git status` 除了预期改动之外必须干净。

## 提交

- 第 1、2 步一个提交（样式基础），第 3–7 步一个提交（组件与页面）；每个提交都带重建的 web 产物，用中文提交信息，`git diff --cached --check` 干净。
- 回滚点：样式基础那个提交可以单独回退，恢复原来的字体和字阶。

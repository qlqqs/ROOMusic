# 执行计划：侧栏与我的音乐重组

纯前端子任务，只写 `prd.md` 和本计划。必须在 [ui-minimal](../11-07-ui-minimal/implement.md) 提交之后开始。

## 分工

| 工作 | 执行者 |
| --- | --- |
| 实现，以及 typecheck、lint、所改文件已有的 Vitest | 前端 Opus（`claude/claude-opus-5-5`，high） |
| 补改测试、跑受影响的 Vitest、Playwright 和 real 用例 | Sol（`codex/gpt-6.1-sol`，high，full-access） |
| 审阅、提交 | 主会话 |

## 步骤

1. **导航分组**：
   - `frontend/src/app/shell/nav-items.ts` 改成三组：聆听、我的音乐、资料库；
   - `primary-nav.tsx` 支持“我的歌单”：名字是链接，旁边有展开收起按钮（`aria-expanded`）。展开后列出前 20 个歌单，超过时末尾是“全部歌单”；
   - 展开状态存在 `localStorage`，默认展开；歌单数据和歌单页共用 `features/personal` 的查询键，增删改名后自动刷新。
2. **路由**（`frontend/src/app/router.tsx`）：
   - 新增 `/me/liked`（我喜欢的音乐：收藏曲目和“播放全部”）；
   - `/me/favorites` 只剩专辑、艺人两栏；
   - `/me` 转到 `/me/liked`，`/me/favorites?tab=tracks` 转到 `/me/liked`，页码跟着转；
   - 去掉 `me-layout.tsx` 的页签，每页用自己的 `Page` 标题。
3. **拆分页面**：`features/personal/components/favorites-view.tsx` 拆成曲目页和专辑、艺人页，翻页和保留行的行为不变。
4. **改名**：
   - “元数据问题”改成“元数据”，“可能是同一位”改成“重复艺人”；
   - 范围包括侧栏、页面标题、操作记录的类型名、其他页面里指向它们的链接文字；
   - 不改路由路径 `/issues`、`/artist-candidates`。
5. **规范**：
   - 更新 `.trellis/spec/frontend/personal-guidelines.md`、`playback-and-personal-state.md`、`artist-guidelines.md`、`state-management.md` 里的地址、页签和名字；
   - 更新 `frontend/DESIGN.md` 第 4 节的导航。
6. **构建**：`npm run build` 更新 `backend/cmd/roomusic/web`，和源码放在同一个提交里。

## 验证

- 实施者：`cd frontend && npm run typecheck && npm run lint`，再跑所改文件的 Vitest。
- Sol 只跑受影响的用例，不跑全量：
  - Vitest：导航分组和可见性、“我的歌单”展开收起与记忆、旧地址跳转、收藏与历史页面、改名相关断言；
  - Playwright：`e2e/personal.spec.ts`、`e2e/history.spec.ts`、`e2e/artists-governance.spec.ts`、`e2e/issues.spec.ts`、`e2e/responsive.spec.ts`，以及因名字或地址变化失败的其他 spec；
  - `frontend/e2e/real/` 与 `scripts/playback-e2e.sh`：只用 grep 更新引用的名字和地址，真实服务运行留到父任务收尾时统一做。

## 提交

- 一个提交，带重建的 web 产物，用中文提交信息。

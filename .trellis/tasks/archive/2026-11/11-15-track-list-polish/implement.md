# 执行计划

分支 `feat/track-list-polish`，在 `feat/player-controls-merge` 合入 main 后切出；纯前端，由主代理实现，另起子代理更新测试，子代理不提交。

1. `useMenu.openAt` 与 `context-menu.tsx`。
2. `track-actions.tsx` 抽 `useTrackMenuActions`，各列表行挂右键。
3. `data-table` 列表（搜索、历史、艺人页）与 `track-table`（收藏、歌单）加艺人列、去标题下艺人。
4. 专辑页表格化分栏、作曲栏、作品分组行。
5. 选中状态、`TrackInfo`、`release-facts` 顺序与去重、窄版面底部面板。
6. 测试（子代理）：受影响组件测试与 e2e `album`、`personal`、`search`、`history`、`artists`、`responsive`、`a11y`。
7. 规范：`interface-layout-guidelines.md`（专辑详情、列表）、`component-catalog.md`。

## 验证

- `cd frontend && mise exec -- npm run typecheck && mise exec -- npm run lint`
- `mise exec -- npx vitest run <受影响文件>`；`mise exec -- npx playwright test <受影响 spec>`
- 截图核对 1440 / 1024 / 768 / 390。

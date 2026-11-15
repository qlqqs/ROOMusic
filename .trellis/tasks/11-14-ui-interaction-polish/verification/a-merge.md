# A 区合并验证（2026-11-15）

## 恢复的验证证据

原 A 检查代理已完成验证；账户接口错误发生在汇报阶段。本次由 `trellis-check` 复核原始记录与工作区，未发现阻塞合并问题，未重复运行已通过的分区测试。

在 A 区工作目录的 `frontend` 下通过 mise 执行：

- `npm run lint`、`npm run typecheck`：通过。
- `npx vitest run src/shared/ui src/app/albums.test.tsx src/app/album-sort.test.tsx src/app/album-detail.test.tsx src/app/artists-step5.test.tsx src/app/artist-governance-step5.test.tsx src/app/artist-player-metadata-step5.test.tsx src/app/artist-links-search-step5.test.tsx src/app/routes/track-actions.test.tsx src/app/routes/album-card-play.test.tsx src/app/routes/artist-playback-step5.test.tsx src/features/catalog src/features/player/components/player-ui.test.tsx`：39 文件、586 用例通过。
- `npx playwright test -c playwright.local.config.ts albums keyboard a11y artists responsive`：337 通过。
- `npx playwright test -c playwright.local.config.ts claims governance default-version issues player search search-step4 scan-feedback history personal library auth artists-governance`：201 通过、1 失败。失败是 `personal.spec.ts` 触屏拖动用例写死 4173，而临时服务使用 4211，报连接拒绝；不能记为通过。
- `git diff --check`：通过。

临时 Playwright 配置与分区构建产物已保存在分区工作目录的 `temp/archive/2026-11-15/a-check/`，不提交。

## 合并处理

- 共用菜单同时保留危险操作分组、警示色与 A 的右侧说明、无标题分组。
- 真实服务辅助代码保留设备选择逻辑和曲目悬停修正。
- 概览最近新增通过 `renderPlay` 复用卡片播放，键盘顺序为链接、播放键交替。
- 合并后的 `mise exec -- npm run lint`、`mise exec -- npm run typecheck` 通过；最后更新的浏览器 Tab 断言通过单文件 ESLint。

## 合并后的定向验证

在任务工作目录的 `frontend` 下执行：

```bash
mise exec -- npx vitest run \
  src/app/overview-resume.test.tsx \
  src/app/recently-added.test.tsx \
  src/app/albums.test.tsx \
  src/shared/ui/menu-button.test.tsx \
  src/features/catalog/components/album-grid.test.tsx \
  src/features/player/components/player-ui.test.tsx --maxWorkers=2

mise exec -- npx playwright test \
  e2e/keyboard.spec.ts e2e/scan-feedback.spec.ts \
  e2e/albums.spec.ts:81 e2e/personal.spec.ts:384 \
  --grep '键盘|390px：计数|1440px：计数|能看进度|卡片封面上的播放键|触屏把手' \
  --workers=2 --retries=0 --reporter=list
```

- 单元测试 6 文件、129 用例全部通过；新增概览最近新增播放接线测试，覆盖整张队列、从首曲起播和停留概览。
- 浏览器测试 16 用例全部通过；默认 4173 端口下，原触屏拖动用例已通过。覆盖桌面与手机最近新增键盘顺序、布局和 axe。
- 浏览器运行设置 `CI=1`，复用项目空间已有浏览器动态库与字体配置，localhost 排除代理；日志在 `temp/merge-a-check/`。
- 复用全量 lint 结果；新增、修改的两个测试文件经定向 ESLint 检查通过，最终 `mise exec -- npm run typecheck` 通过。
- Playwright 启动前的 `npm run build` 通过，内嵌产物已更新；未重复构建。
- `git diff --check`、`git diff --cached --check` 通过；未解决冲突列表为空。

## 尚未完成的整体任务验收

- 合并后全量单元测试与 e2e。
- `e2e/real/*` 定位器已更新，真实服务测试尚未运行；第 2 条刷新行为的既有真实服务核实见 PRD。
- B–E 规范与整体设计文档同步、整体视觉验收及分区目录清理仍按执行计划收尾。

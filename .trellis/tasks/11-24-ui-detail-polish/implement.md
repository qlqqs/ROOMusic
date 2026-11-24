# 实施

1. 专辑详情：release-media（艺人/作曲栏、多碟表头、跳到、CUE、窄版对齐）、album-header（去掉正在播放、CUE 概况）、release-facts 与 format（流派、空侧栏）、album-detail-view 样式。
2. 卡片与封面：format.albumMeta、album-card 样式、cover。
3. 概览：overview-playback、recent-plays-panel、各摘要行、空曲库说明。
4. 音乐目录：settings-sections、library-page、scan-section、root-table、root-command-notice、root-operations-section、scan-status。
5. 搜索与表格：search-results-view、track-results、match-note、data-table 样式、播放栏样式。
6. 播放器：player-bar、fullscreen-player。
7. 艺人：artists-list-view 样式与计数文字。

每一步后在走查实例上截图核对，跑受影响的 Vitest 用例；最后跑 lint、typecheck、生产构建与受影响的 Playwright 冒烟。

验证命令：

```bash
cd frontend
npm run lint
npm run typecheck
npx vitest run <受影响的用例>
npm run build
npx playwright test e2e/albums.spec.ts e2e/search.spec.ts e2e/player.spec.ts e2e/responsive.spec.ts e2e/shell.spec.ts e2e/library.spec.ts
```

# 执行计划

分支：`feat/player-controls-merge`（独立分支；主代理实现前端，另起子代理更新测试，子代理不提交）。

1. `player-controls.tsx`：`PlayModeButton`、`VolumeButton`、`TransportControls` 插槽；`player-labels.ts` 模式标签。
2. `signal-path.tsx` 空位外层。
3. `player-bar.tsx` / `.module.css` 重排。
4. `fullscreen-player.tsx` / `.module.css` 宽屏与窄屏重排。
5. `library-stats.tsx` / `.module.css` 卡片；`overview-page.tsx` / `.module.css` 右栏重排。
5a. 封面遮罩（`player-bar.tsx` / `.module.css`）。
5b. 滚动条：全局工具样式；应用到 `lyrics-panel`、`signal-path`、`up-next-list`、`device-picker`、`fullscreen-player`（`.left`、窄屏 `.layout`）；全屏 `color-scheme: dark`。
6. 更新测试（子代理）：`player-ui.test.tsx`、`device-picker.test.tsx`、`signal-path*.test.tsx`、`overview-page.test.tsx` 及引用旧名称的 app 测试；e2e `player`、`responsive`、`a11y`、`zones-devices`、`personal`、`dsp-pipeline`、`support/*`。
7. 规范：`interface-layout-guidelines.md`（播放栏、信号路径、全屏、概览）、`player-design-guidelines.md` 相关句、`component-catalog.md`（如列出了被删组件）。

## 验证

- `cd frontend && mise exec -- npm run typecheck && mise exec -- npm run lint`（以 package.json 实际脚本为准）
- 受影响的 vitest 文件：`mise exec -- npx vitest run <上列文件>`
- e2e：上列 spec（`npx playwright test <files>`），含五种宽度与 axe
- 截图自查：宽屏 1440、1024、768、390 的播放栏、全屏、概览

# 最终自动化验收（2026-11-15）

范围：A–E 合并后的前端单元测试、模拟后端 Playwright、lint 与类型检查。真实后端播放与逐项视觉验收由独立报告记录。本轮只使用合成数据，未操作现有实例。

## 执行与结果

以下命令从 `frontend/` 执行，日志保存在项目的 `temp/final-acceptance/`。Playwright 使用本机环境说明中的浏览器依赖和字体配置，设置 `CI=1`、本地地址绕过代理，两个 worker、零重试；默认配置先运行生产构建，再在 4173 端口提供预览。

| 命令 | 实际结果 | 日志 |
| --- | --- | --- |
| `mise exec -- npm run test -- --maxWorkers=2` | 170 文件，4023 通过、2 失败、1 跳过；两处失败均为新增侧栏入口后“新建歌单”定位范围过宽 | `unit.log` |
| `mise exec -- npx vitest run src/app/playlists.test.tsx --maxWorkers=2` | 修正定位后 31/31 通过 | `unit-playlists.log` |
| `mise exec -- npx vitest run src/shared/ui/menu-button.test.tsx src/features/player/components/player-ui.test.tsx --maxWorkers=2` | 最终菜单修复及新增回归后 59/59 通过 | `unit-menu-player-final.log` |
| `mise exec -- npm run test:e2e -- --workers=2 --retries=0` | 590 条中 589 通过、1 失败；失败是设备选中行的真实对比度不足 | `e2e.log` |
| `mise exec -- npx playwright test e2e/zones-devices.spec.ts e2e/system-backups.spec.ts e2e/keyboard.spec.ts --workers=2 --retries=0 --reporter=list` | 菜单与空队列入口修复后 34 通过；设备选中行对比度仍失败 | `e2e-fixes.log` |
| `mise exec -- npx playwright test e2e/zones-devices.spec.ts --grep '375px 迷你栏' --workers=1 --retries=0 --reporter=list` | 诊断运行确认实际前景色、背景色及对比度，1 条失败 | `e2e-contrast-diagnostic.log` |
| `mise exec -- npx playwright test e2e/zones-devices.spec.ts e2e/checks.spec.ts --workers=2 --retries=0 --reporter=list` | 修复后 19/19 通过，包括选中行悬停时的 axe 检查与检查工具自检 | `e2e-contrast-final.log` |
| `mise exec -- npm run lint` | 通过 | `lint-final.log` |
| `mise exec -- npm run typecheck` | 通过 | `typecheck-final.log` |

按全量基线加受影响用例复验计算：单元测试有效结果为 **4026 通过、1 跳过**（新增 1 条菜单回归）；模拟后端 Playwright 为 **592 通过**（新增空队列设备入口及菜单底边回归各 1 条）。这不是一次全量运行直接全绿：首次失败、修复和复验均保留在上表；已通过且未受后续修改影响的测试按项目约定复用，没有再次全量运行。

唯一跳过项是 `src/test/api-contract.test.ts` 的外部万曲艺人队列样本：未提供 `ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR`，不能计为通过。

## 修复与证据

1. 歌单页测试原先在全页面统计“新建歌单”，侧栏新增同名入口后失效。将按钮与列表链接查询限制在“全部歌单”区域，保留原有页面内容断言。
2. 手机本机空队列没有封面，无法通过全屏入口选择其他设备。迷你栏现在在存在可控制的在线设备或可接手的离线队列时保留设备入口；有曲目且控制本机时仍按方案隐藏。显示条件使用明确属性，去掉本轮新增的 `:has()` 依赖。新增 375px 用例验证从空队列选中在线设备、标题变化、读屏名和无横向溢出；不宣称已实际运行旧版 Firefox。
3. 手机备份行按钮位于视口底边时，浏览器先将按钮滚入视口，迟到的 `scroll` 事件会立即关闭刚打开的菜单。旧构建上的专门复现失败，记录在 `menu-repro.log`。菜单现在先校正位置，再以 `preventScroll` 聚焦，并只在锚点实际移动时响应滚动关闭；菜单内部滚动和打开前滚动的迟到事件不再关闭菜单。新增单元测试及浏览器回归覆盖打开、首项聚焦、方向键和 Escape。最终浏览器回归通过；视觉报告另保留实际自然点击路径的截图。
4. 设备列表选中行叠加半透明悬停底色后，背景成为 `#d9d2ca`；名称 `#7d5411` 的对比度为 **4.45:1**，状态 `#605d5d` 为 **4.35:1**，均低于小字所需的 4.5:1。诊断日志记录 axe 原始说明，失败截图为 `temp/final-acceptance/contrast-before.jpeg`。现沿用侧栏已选项的优先规则，仅未选中设备行使用悬停底色，不更改全局文字色。回归用例明确悬停选中设备后执行 axe，最终通过。检查工具的失败信息补充 `failureSummary`，今后可以直接看到色值及比率。

## 构建与边界

最后一轮 Playwright 已完成 `npm run build`，更新 `backend/cmd/roomusic/web/` 的内嵌产物；当时的资源为 `index-CCdCN_Gm.js`、`index-YlbnVzrF.css`。构建只有已有的大 chunk 提示，无构建失败。单元测试中的 jsdom canvas 提示不影响断言结果。

本报告范围内没有待修复的 lint、类型或模拟后端测试失败。规范同步、真实播放验收、截图核对及最终提交由主代理汇总；本检查代理未提交代码。

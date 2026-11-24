# 手机网页可安装

父任务：`11-24-adoption-readiness`（第 2 项）。

## 目标

手机（iOS Safari、Android Chrome）与桌面 Chrome/Edge 能把 ROOMusic “添加到主屏幕”或“安装”，从主屏幕图标全屏打开，像一个应用；
锁屏与通知栏能看到正在播放并控制。原生 App 之前，先让网页版在手机上好用。

## 现状（仓库）

- `frontend/index.html` 只有 SVG favicon，没有 Manifest、`apple-touch-icon`、`theme-color`，`viewport` 没有 `viewport-fit=cover`。
- 已有 Media Session 绑定（`features/player/output/media-session-binding.ts`）与若干 `env(safe-area-inset-*)` 样式。
- 后端把 `web/dist` 根目录下的非 `assets/` 文件原样返回并每次验证（`internal/platform/httpserver/frontend.go`）。

## 主代理取的默认值

| 项 | 默认 |
| --- | --- |
| Manifest | `public/manifest.json`（用 `.json` 扩展名，内容类型不依赖服务端注册新类型）；`display: standalone`，`start_url` 与 `scope` 为 `/`，`id` 为 `/` |
| 图标 | 由现有品牌标志生成 PNG：192、512、512 可遮罩（安全区 80%）、180 的 `apple-touch-icon`；浅底，与浅色主题背景一致 |
| 主题色 | 浅色、深色分别按 `--color-bg` 设 `theme-color` |
| Service Worker | 不加：Chrome 已不要求它才能安装；离线缓存与登录态、播放流冲突，离线不在范围 |
| 刘海与底部横条 | `viewport-fit=cover`，顶部栏与底部栏补安全区内边距 |
| 外链 | 外部链接在新窗口打开（独立窗口里会交给浏览器） |

## 要求

1. Manifest、图标、`apple-touch-icon`、`theme-color`、`apple-mobile-web-app-*` 元信息。
2. 独立窗口下没有浏览器的前进后退按钮：核对各页面都有界面内的返回路径（详情页、设置子页、全屏播放器、对话框）。
3. 安全区：独立窗口全屏时顶栏不被状态栏遮住、底部标签栏与迷你播放栏不被横条遮住。
4. 文档：README 与部署说明里加“装到手机主屏幕”的几步，并写明 iOS 上独立窗口需要重新登录（与 Safari 不共享 Cookie）。

## 验收

- Chromium 的安装条件检查通过：Manifest 可解析、图标可取到、`display` 为 `standalone`（用 Playwright 读取 `manifest` 与图标请求结果）。
- 构建产物里有 `manifest.json` 与图标，后端返回 200 且内容类型正确。
- 模拟独立窗口（`display-mode: standalone`）与带安全区的视口，390 宽度截图核对顶栏、底栏、迷你播放栏与全屏播放器没有被遮挡。
- 真实设备（iOS、Android）的安装、锁屏控制与后台播放由 qlqq 试用，列在交付说明里。

## 不做

- 离线播放、Service Worker 缓存、推送通知。

## 实施与验收记录（2026-11-24）

- 合入：分支 `feat/installable-web-app`（`80f2228c`）。
- 实际运行：`npm run lint`、`npm run typecheck` 通过；`npx playwright test e2e/installable.spec.ts` 通过（Manifest、四个图标 200 且为 `image/png`，CDP `Page.getInstallabilityErrors` 为空——Chromium 不要求 Service Worker）。
- 截图：390×844 触屏视口，用 CDP `Emulation.setSafeAreaInsetsOverride` 模拟顶部 47px、底部 34px 安全区，核对概览、专辑页与全屏播放器：顶栏与全屏播放器的顶部让开了刘海，底栏与迷你播放栏在横条之上；无安全区时与原来一致。
- 返回路径：窄屏始终显示底部标签栏，没有界面内返回按钮的子页面（歌单详情、设置子页）可经标签栏离开；专辑、艺人、扫描详情已有返回链接；全屏播放器与对话框有关闭按钮。未加新的返回按钮。
- 期间发现另一个会话同时在 4173 端口跑 e2e，Playwright 的 `reuseExistingServer` 会连到对方的构建；本任务的最终结果是在临时改用 4183 端口、不复用服务器的配置下得到的（临时配置已删除）。
- 未验证（需要真机）：iOS 与 Android 的实际安装、独立窗口的锁屏控制与后台播放。

# 实施计划：手机网页可安装

全部是前端与静态资源，主代理实施；工作目录 `~/workspace/.worktrees/ROOMusic/installable-web-app`，分支 `feat/installable-web-app`。

1. 用 Playwright 的 Chromium 把品牌标志渲染成各尺寸 PNG（生成脚本放 `frontend/scripts/`，产物提交到 `frontend/public/icons/`）。
2. `public/manifest.json`；`index.html` 的 link 与 meta。
3. 核对独立窗口下的返回路径与安全区，按需补样式。
4. 单测：Manifest 字段与图标文件存在（vitest 读文件）；Playwright 用例：Manifest 与图标可取到、独立窗口截图。
5. 文档与 README。

## 验证命令

- `cd frontend && mise exec -- npm run lint && npm run typecheck && npx vitest run <新测试>`
- `cd frontend && mise exec -- npm run build`，再用 Go 后端或 `vite preview` 取 `manifest.json` 与图标
- Playwright：`npx playwright test <新用例>`（`LD_LIBRARY_PATH` 与 `NO_PROXY` 按开发环境说明）

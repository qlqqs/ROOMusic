# 技术设计：前端构建产物不入库

## 目录与内嵌

```
backend/cmd/roomusic/web/
  README.md        已跟踪：说明产物由 vite build 写入 dist/，不提交
  dist/            Git 忽略；vite build 输出（index.html、assets/…）
```

- `vite.config.ts` 的 `build.outDir` 改为 `../backend/cmd/roomusic/web/dist`，保留 `emptyOutDir: true`（只清空 `dist`，不碰 `README.md`）。
- `main.go` 保持 `//go:embed web`（`README.md` 保证至少有一个文件可内嵌，`dist` 存在时一并内嵌）。`frontendSub()` 改为取 `web/dist`；
  `dist/index.html` 不存在时返回 `nil` 并由调用方记录 `platform.frontend.missing` 日志、挂载一个对非 `/api` 路径返回 503 与中文纯文本提示的处理器。
- `.gitignore` 增加 `/backend/cmd/roomusic/web/dist/`。

## 测试

- `TestProductionFrontendServesRootFallbackAndAsset404`、`TestProductionFrontendCachePolicy` 改为用 `fstest.MapFS` 构造带 `index.html` 与一个哈希命名资产的文件系统，直接测试 `httpserver.Frontend`，与真实构建解耦。
- `TestEmbeddedAssetsAreContentHashed` 读取真实内嵌的 `web/dist`，不存在时 `t.Skip`；Dockerfile 的 Go 阶段运行 `go test ./cmd/roomusic/ -run TestEmbeddedAssetsAreContentHashed`，并在之前 `test -f cmd/roomusic/web/dist/index.html`，确保镜像里的检查不会被跳过。
- 增加一个测试：没有产物时前端路径返回 503 提示，`/api/...` 不受影响。

## 镜像

```
FROM --platform=$BUILDPLATFORM node:24.22.1-alpine<ver>@sha256:<digest> AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build        # outDir 相对路径落在 /src/backend/cmd/roomusic/web/dist

FROM golang… AS build
COPY backend/ ./
COPY --from=web /src/backend/cmd/roomusic/web/dist cmd/roomusic/web/dist
RUN test -f cmd/roomusic/web/dist/index.html && go test ./cmd/roomusic/ -run TestEmbeddedAssetsAreContentHashed && go build …
```

- Node 镜像 tag 与 digest 在实现时用 `docker buildx imagetools inspect` 取当前值，版本号必须与 `.mise.toml` 的 `node` 一致；CI 用 `grep -qF "node:$(sed -n 's/^node = "\(.*\)"/\1/p' .mise.toml)-"` 校验。
- `npm run build` 包含 `tsc --noEmit`，镜像构建即做类型检查，与本地一致。
- 构建上下文是仓库根，新增 `.dockerignore`：`.git`、`**/node_modules`、`temp`、`frontend/coverage`、`frontend/test-results`、`frontend/playwright-report`、`backend/cmd/roomusic/web/dist`、`.env*`（保留 `!.env.*.example` 无需进入镜像，可整体排除）、`.trellis`、`.claude`、`.agents`、`.codex`。

## CI 与发布

- Frontend job 删除 “Check embedded asset drift”，保留 `npm run build`（验证构建本身）。
- 新增 `image` job：`docker/setup-buildx-action` + `docker/build-push-action`（`push: false`，`platforms: linux/amd64`，不登录仓库），超时 30 分钟。
- `release-check.sh` 删除第 74–75 行及对应测试用例；发布流程本身不变（release.yml 的构建自动包含 Node 阶段）。

## 兼容与回滚

- 运行时行为与镜像内容等价（同一 Vite 构建，同一内嵌路径语义）。
- 回滚：还原本任务提交并重新构建、提交产物即可，无数据迁移。
- 风险：Node 阶段让发布构建变慢（`npm ci` 约 1–2 分钟，只在构建平台跑一次）；`npm ci` 需要访问 npm 源，与 CI 现状一致。

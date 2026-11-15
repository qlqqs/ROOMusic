# 前端构建产物不入库（意见 5）

## Goal

仓库不再提交带哈希的前端产物，前端改动的分支合并不再因产物冲突；生产镜像在构建阶段生成前端并内嵌，镜像内容与现在等价。

## 已确认事实

- `frontend/vite.config.ts:14` 输出到 `backend/cmd/roomusic/web`，`backend/cmd/roomusic/main.go:31` 用 `//go:embed web`，`frontendSub()` 取 `web` 子树（`main.go:159`）。
- 依赖已提交产物的地方：CI Frontend job 的 “Check embedded asset drift”；`scripts/release-check.sh:74-75` 及 `scripts/tests/test_release_check.py`；`backend/cmd/roomusic/main_test.go` 三个测试（缺产物时失败并提示提交产物）；`deploy/production/Dockerfile`（注释“不安装 Node”，`test -f cmd/roomusic/web/index.html`）；README “开发”一节的加粗提示；spec `.trellis/spec/backend/deployment-environment-contracts.md`、`.trellis/spec/backend/core-slice-tooling.md`、`.trellis/spec/frontend/index.md`、`.trellis/spec/frontend/quality-guidelines.md`；`docs/architecture/adr/0001-frontend-foundations.md`、`docs/architecture/core-1-baseline.md`、`docs/architecture/architecture-decisions.md` 有相关表述。
- `scripts/playback-e2e.sh:174,183` 已排除仓库产物、自行构建到临时目录；`frontend/playwright.config.ts` 先构建再 `vite preview`。
- 仓库没有 `.dockerignore`；发布用 `docker/build-push-action`，上下文为仓库根，构建 amd64 与 arm64。
- CI 目前不构建生产镜像。

## Requirements

1. 产物输出到 `backend/cmd/roomusic/web/dist/`（Git 忽略）；`backend/cmd/roomusic/web/` 下保留一个已跟踪的说明文件，使 `go build` 与 `go vet` 在没有前端产物时仍能编译。
2. 没有前端产物时，后端照常启动，访问前端路径返回明确的“前端未构建”提示（开发使用 Vite 5173，不受影响），接口不受影响；启动日志记录一条提示。
3. 生产镜像：Dockerfile 增加钉死版本与 digest 的 Node 构建阶段（版本与 `.mise.toml` 一致，`--platform=$BUILDPLATFORM` 只构建一次），`npm ci` 后构建前端，再交给 Go 阶段内嵌；Go 阶段在内嵌后校验 `dist/index.html` 存在与 `assets/` 文件名带哈希。增加 `.dockerignore` 排除 `node_modules`、`temp`、`.git` 等。
4. CI：删除产物漂移检查；新增一个 job 构建生产镜像（仅 amd64、不推送）作为 Dockerfile 回归；“Validate Compose configuration” 增加 Node 版本与 `.mise.toml` 一致的检查。
5. `scripts/release-check.sh` 删除产物检查及对应 Python 测试用例，保留其余检查。
6. `main_test.go`：响应与缓存策略测试改用合成文件系统，始终运行；哈希命名检查在产物存在时运行、缺失时跳过，并由镜像构建阶段的校验兜底。
7. `git rm -r --cached` 现有产物；README、上列 spec 与文档改为新流程；ADR 0001 以追加修订记录的方式说明变更，不改写原裁决文字。

## Acceptance Criteria

- [ ] 干净检出（无 `dist`）下 `cd backend && go build ./... && go vet ./... && go test ./cmd/roomusic/` 通过。
- [ ] 本地 `docker build -f deploy/production/Dockerfile .`（amd64）成功；用临时 Compose 项目启动该镜像（合成数据、唯一项目名、结束后清理）访问 `/` 返回前端 index，`/assets/*` 带 immutable 缓存头，`/readyz` 正常。
- [ ] `git ls-files backend/cmd/roomusic/web` 只剩说明文件；修改前端源码后 `git status` 不出现产物变化。
- [ ] `scripts/release-check.sh` 与 `python3 scripts/tests/test_release_check.py` 通过；`actionlint` 通过。
- [ ] `./scripts/playback-e2e.sh` 与 `npm run test:e2e` 在新输出路径下通过。

## Out of Scope

- 多架构镜像在 CI 中构建（仍只在发布时构建）、镜像签名与 SBOM。

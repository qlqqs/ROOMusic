# 执行计划：前端构建产物不入库

前置：`11-15-backend-hygiene` 先合入（同改 `ci.yml`）。`11-15-frontend-route-split`、`11-15-external-lrc` 在本任务之后合入，不再提交产物。

1. 前端：`vite.config.ts` 改 `outDir`；`.gitignore` 加 `dist` 忽略；新建 `backend/cmd/roomusic/web/README.md`。
2. 后端（Codex 实现）：`main.go` 的 `frontendSub()` 与缺产物处理；`main_test.go` 按设计改写并补缺产物测试。
3. 镜像：Dockerfile 加 Node 阶段与 Go 阶段校验；新建 `.dockerignore`。
4. CI 与脚本：`ci.yml` 删漂移检查、加 `image` job 与 Node 版本一致性检查；`release-check.sh` 与 `scripts/tests/test_release_check.py`；检查 `scripts/playback-e2e.sh` 的 `--exclude` 与 `--outDir` 是否需改为 `web/dist`。
5. `git rm -r --cached backend/cmd/roomusic/web/assets backend/cmd/roomusic/web/index.html` 等现有产物。
6. 文档：README、PRD 列出的 spec 与 docs、ADR 0001 修订记录、Dockerfile 头部注释。

## 验证命令

```bash
(cd backend && go build ./... && go vet ./... && go test ./cmd/roomusic/)      # 无 dist
(cd frontend && npm run build) && (cd backend && go test ./cmd/roomusic/)       # 有 dist，哈希检查运行
sg docker -c 'docker build -f deploy/production/Dockerfile -t roomusic-webbuild-check:local .'
python3 scripts/tests/test_release_check.py
sg docker -c 'docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.12 -color'
./scripts/playback-e2e.sh
(cd frontend && npm run test:e2e)
```

镜像启动核对：在 `temp/` 下建唯一目录与 Compose 项目名（`roomusic-compose-test-webbuild-<随机>`），用合成音乐目录与测试库启动，核对 `/`、`/assets/*` 缓存头与 `/readyz`，结束后只清理本次项目的容器、网络、卷与目录，并删除本次构建的本地镜像标签。

## 回滚点

- 第 5 步之前可整体丢弃；合入后回滚为还原提交并重新构建提交产物。

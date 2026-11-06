# ROOMusic

ROOMusic 是给个人、家庭和小型私有群体用的自托管音乐库。它按 `专辑 → 版本 → 碟 → 曲目` 理解你的音乐，而不只是把文件夹列出来；
一个管理员负责整理，成员各自听歌、收藏和建歌单。整个应用是一个 Go 程序加 PostgreSQL，前端由同一个端口提供。

## 能做什么

- **浏览与搜索**：专辑列表、详情、多版本对比，艺人页面；搜索不分繁简与写法。
- **播放**：浏览器直接播放常见格式；ALAC 在服务端无损转成 FLAC 后播放；内嵌歌词逐行同步显示。
- **个人数据**：每个成员有自己的播放历史、收藏和歌单，互不影响。
- **整理（管理员）**：修正发行信息、调整归组、指定默认版本、处理元数据问题、认领重扫后的新版本、合并或拆开艺人；
  修改都可以撤销，重扫不会覆盖，记录在“操作记录”页。
- **扫描**：只读扫描允许的音乐目录，有进度和报告；不会写入或改动音乐文件。
- **账号**：管理员添加成员、重置成员密码；成员自己改密码；管理员忘记密码时在服务器上用命令恢复。

## 生产部署

生产只需 `app` 与 PostgreSQL 两个容器，应用内置每日备份和固定版本的 ffmpeg，镜像从 GHCR 拉取。
从 GitHub Release 下载 `compose.yaml` 和 `example.env` 到独立目录，将后者复制为 `.env`，
填好数据库密码与音乐目录后执行 `docker compose up -d --wait`，默认访问端口为 `8090`。

升级、降级、备份恢复、反代配置及已有实例的过渡步骤见 [生产部署说明](deploy/production/README.md)。

## 开发

工具链由 [mise](https://mise.jdx.dev/) 管理：Go 1.25.10、Node.js 24.22.1。开发时 Go 和 Node 在宿主机运行，PostgreSQL 等依赖由
根目录的 `compose.yaml` 提供，只监听 `127.0.0.1`；Redis 和 Meilisearch 留给后续功能，当前代码不依赖它们。能力和配置只在真正需要时才加。

```bash
mise install
cp .env.example .env.dev    # 填写本地 PostgreSQL 密码
make dev                    # 启动 PostgreSQL、Go 后端（8080）和 Vite（5173）
```

React 修改会热更新，Go 或迁移文件修改会自动重启后端，`Ctrl-C` 同时停止两者。开发和生产可以同时运行，配置、端口、数据卷和网络各自独立，
只共享只读的音乐目录。`make dev-reset` 会清空开发库的业务数据（保留迁移），生产配置下会拒绝执行；
开发环境重置密码用 `./scripts/reset-password.sh <用户名>`。开发环境播放 ALAC 需要 ffmpeg：运行 `./scripts/ffmpeg-pinned.sh`
取出固定版本，或用系统包，把路径写进 `ROOMUSIC_FFMPEG_PATH`。

### 测试与 CI

```bash
(cd backend && go test ./...)                                     # 后端单元测试；没有测试库时集成测试会跳过
./scripts/test-integration.sh                                     # 启动一次性 PostgreSQL 18 跑集成测试
(cd frontend && npm ci && npm run lint && npm run typecheck && npm run test)
(cd frontend && npm run build)                                    # 输出到 backend/cmd/roomusic/web
(cd frontend && npm run test:e2e)                                 # Playwright，不需要启动后端
```

**改动前端源码的提交要重新构建，并把 `backend/cmd/roomusic/web` 一起提交**，否则 CI 的漂移检查会失败。
[GitHub Actions](.github/workflows/ci.yml) 在 Pull Request 和推送到 `main` 时运行后端、前端和集成三组检查。
本地按改动只跑相关的最小测试集即可。

## 文档

- [生产部署说明](deploy/production/README.md)：配置、运维、备份与恢复的完整细节
- [Core 1 路线](docs/architecture/core-1-roadmap.md)：能力矩阵、各阶段范围与 ADR
- [前端设计依据](frontend/DESIGN.md)、[前端规范](.trellis/spec/frontend/index.md)、[后端规范](.trellis/spec/backend/index.md)
- [无损派生合同](.trellis/spec/backend/playback-derivation-contracts.md)、[内嵌歌词合同](.trellis/spec/backend/lyrics-contracts.md)

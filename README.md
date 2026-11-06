# ROOMusic

自托管音乐库，适合个人、家庭和小型私有群体。它按 `专辑 → 版本 → 碟 → 曲目` 理解你的音乐，而不只是列出文件夹。
一个管理员负责整理音乐库，成员各自听歌、收藏、建歌单。

## 功能

- **浏览与搜索**：专辑、版本对比、艺人页面；搜索不区分繁简和写法。
- **播放**：浏览器直接播放常见格式，ALAC 由服务端无损转成 FLAC；内嵌歌词逐行同步。
- **个人数据**：每个成员有自己的播放历史、收藏和歌单。
- **整理**：管理员可以修正发行信息、调整归组、合并或拆分艺人。所有修改都能撤销，重新扫描不会覆盖。
- **扫描**：只读扫描音乐目录，从不改动音乐文件。
- **账号**：管理员添加成员、重置成员密码；管理员自己忘了密码，可以在服务器上用命令重置。
- **备份**：每天自动备份数据库，升级或降级前也会自动备份；管理员可以在网页上立即备份、下载和删除备份，并设置自动清理（保留几天、保留几份）。

## 快速开始

需要 Docker 和 Docker Compose v2。

部署文件从 `main` 下载，镜像默认使用 `latest`。

```bash
mkdir roomusic && cd roomusic
curl -fL -o compose.yaml https://raw.githubusercontent.com/qlqqs/ROOMusic/main/compose.prod.yaml
curl -fL -o .env https://raw.githubusercontent.com/qlqqs/ROOMusic/main/.env.prod.example
chmod 600 .env
```

编辑 `.env`，填两项：

```ini
ROOMUSIC_DB_PASSWORD=一串字母和数字   # 可以用 openssl rand -hex 24 生成
ROOMUSIC_MUSIC_DIR=/path/to/music     # 音乐目录的绝对路径
```

启动：

```bash
docker compose up -d --wait
```

打开 `http://<主机>:8090`，创建管理员，在“扫描目录”添加 `/music`，扫描完成后就可以听歌了。

## 升级与降级

默认使用 `latest`，升级时运行：

```bash
docker compose pull && docker compose up -d --wait
```

要固定或降级版本，把 `.env` 里的 `ROOMUSIC_VERSION` 改成具体版本（如 `0.1.0`，不带 `v`），再执行同样的命令。

数据库结构变化前会先自动备份。最低可以降到 `0.1.0`；遇到无法自动回退的版本，应用不会启动，日志会给出恢复备份的命令。

端口、备份目录、反向代理、备份恢复等更多内容见[部署说明](deploy/production/README.md)。

## 开发

工具链由 [mise](https://mise.jdx.dev/) 管理（Go 1.25.10、Node.js 24.22.1）。Go 和 Node 在宿主机运行，PostgreSQL 由根目录的
`compose.dev.yaml` 提供，只监听 `127.0.0.1`。

```bash
mise install
cp .env.dev.example .env.dev # 填写本地 PostgreSQL 密码
make dev                    # PostgreSQL + Go 后端（8080）+ Vite（5173）
```

前端修改热更新，Go 和迁移文件修改会自动重启后端，`Ctrl-C` 一起停止。开发和生产可以在同一台机器上同时运行，互不影响。

- `make dev-reset`：清空开发库的业务数据，保留迁移。
- `./scripts/reset-password.sh <用户名>`：开发环境重置密码。
- 播放 ALAC 需要 ffmpeg：运行 `./scripts/ffmpeg-pinned.sh` 取出固定版本，或使用系统自带的 ffmpeg，并把路径写进 `ROOMUSIC_FFMPEG_PATH`。

### 测试

```bash
(cd backend && go test ./...)                 # 后端单元测试，没有测试库时跳过集成测试
./scripts/test-integration.sh                 # 用一次性 PostgreSQL 18 跑集成测试
(cd frontend && npm ci && npm run lint && npm run typecheck && npm run test)
(cd frontend && npm run test:e2e)             # Playwright，不需要启动后端
```

**改了前端源码，要运行 `(cd frontend && npm run build)`，并把 `backend/cmd/roomusic/web` 一起提交**，否则 CI 的漂移检查会失败。

### 发布

```bash
scripts/release-check.sh vX.Y.Z               # 只读检查
git tag -a vX.Y.Z -m "ROOMusic vX.Y.Z" && git push origin vX.Y.Z
```

推送标签后，GitHub Actions 会确认该提交在 `main` 上的 CI 已经通过，然后构建 amd64 和 arm64 镜像，推送到
`ghcr.io/qlqqs/roomusic`，并发布 Release 说明。

## 文档

- [部署说明](deploy/production/README.md)：配置、备份恢复、反向代理、排查
- [Core 1 路线](docs/architecture/core-1-roadmap.md)：能力矩阵、各阶段范围与 ADR
- [前端设计依据](frontend/DESIGN.md)、[前端规范](.trellis/spec/frontend/index.md)、[后端规范](.trellis/spec/backend/index.md)

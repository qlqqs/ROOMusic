# ROOMusic

自托管音乐库，适合个人、家庭和小型私有群体。它按 `专辑 → 版本 → 碟 → 曲目` 理解你的音乐，而不只是列出文件夹。
一个管理员负责整理音乐库，成员各自听歌、收藏、建歌单。

## 功能

- **浏览与搜索**：专辑、版本对比、艺人页面；搜索不区分繁简和写法。
- **播放**：服务器统一解码后输出连续的音频流，ALAC、DSD（DSF/DFF）和 CUE 分轨在浏览器里都能直接播放；支持无缝播放、交叉渐变、音量均衡；可以在一个页面上遥控另一台设备的播放；歌词逐行同步，来自内嵌标签或同名 `.lrc` 文件。
- **音效处理**：每人可设参数均衡（可导入耳机均衡曲线）、卷积、采样率转换与 Headroom，信号路径实时显示每一步。
- **局域网设备**：管理员打开后，服务器自动找到家里支持 UPnP/DLNA 或 Google Cast 的功放、音箱、电视或播放软件（包括 Chromecast、Google TV 与 Nest 音箱），把播放直接推给它们，音效处理照常生效；可以设置成员间是否共享设备。
- **个人数据**：每个成员有自己的播放历史、收藏和歌单。
- **整理**：管理员可以修正发行信息、调整归组、合并或拆分艺人，在艺人页上传图片、编辑简介或指定 MusicBrainz / Wikidata 条目。所有修改都能撤销，重新扫描不会覆盖。
- **自动整理**：打开后从 MusicBrainz 等外部来源自动补封面、歌词、空着的信息与艺人的图片和简介；拿不准的由模型判断。开放资料库里查不到的艺人可以交给“网络搜索”：模型联网认人，或整理出带引用网页的图片与简介。每一项都能撤销。可以安装第三方数据源插件，插件在沙箱里运行。
- **管家**：接入你自己的模型服务后，可以用对话查曲库、放歌、整理资料；修改前可以先确认，也可以交给另一次模型调用审查。Claude Code 等 MCP 客户端也能连进来。
- **扫描**：只读扫描音乐目录，从不改动音乐文件；可以定时扫描。
- **账号**：管理员添加成员、重置成员密码；管理员自己忘了密码，可以在服务器上用命令重置。
- **备份**：每天自动备份数据库，升级或降级前也会自动备份；管理员可以在网页上立即备份、下载和删除备份，并设置自动清理（保留几天、保留几份）。
- **匿名统计**：默认每天向项目发送一次匿名统计（版本、曲库规模的分档、用到了哪些功能），不含曲目、文件名、账号或 IP；可以在设置里关闭，发送的内容见[匿名统计说明](docs/insights.md)。

## 快速开始

需要 Docker 与 Docker Compose v2。用 NAS 的 Docker 界面安装（群晖、飞牛、绿联、威联通、TrueNAS、Unraid 等）见[在 NAS 上安装](docs/install-nas.md)，不需要命令行。

命令行安装：部署文件从 `main` 下载，镜像默认使用 `latest`。

```bash
mkdir roomusic && cd roomusic
curl -fL -o compose.yaml https://raw.githubusercontent.com/qlqqs/ROOMusic/main/compose.prod.yaml
curl -fL -o .env https://raw.githubusercontent.com/qlqqs/ROOMusic/main/.env.prod.example
chmod 600 .env
```

编辑 `.env`，填写音乐目录；读取音乐的用户不是 uid 1000 时，同时填写它的用户与组 ID：

```ini
ROOMUSIC_MUSIC_DIR=/path/to/music     # 音乐目录的绝对路径
# PUID=1000
# PGID=1000
```

启动：

```bash
docker compose up -d --wait
```

数据库首次启动时自动生成密码。持久化目录为 `secrets/`、`postgres/`、`data/` 和 `backups/`，都在部署目录里，由 Docker 自动创建。默认固定容器名为 `roomusic-app` 和 `roomusic-postgres`，可在 `.env` 中修改。

打开 `http://<主机>:8090`，创建管理员，在“扫描目录”添加 `/music`，扫描完成后就可以听歌了。

### 装到手机主屏幕

ROOMusic 的网页可以装成手机上的应用，从主屏幕图标全屏打开，锁屏界面能看到正在播放并控制：

- **iPhone、iPad**：用 Safari 打开 ROOMusic，点分享按钮，选“添加到主屏幕”。从主屏幕打开的窗口和 Safari 不共享登录状态，第一次要重新登录。
- **Android**：用 Chrome 打开，点菜单里的“安装应用”或“添加到主屏幕”。
- **电脑**：Chrome、Edge 地址栏右侧的安装按钮。

## 升级与降级

默认使用 `latest`，升级时运行：

```bash
docker compose pull && docker compose up -d --wait
```

要固定或降级版本，把 `.env` 里的 `ROOMUSIC_VERSION` 改成具体版本（不带 `v`），再执行同样的命令。

升级后会自动删除旧的 ALAC 转码缓存。

数据库结构变化前会先自动备份。旧版本使用其对应的 Compose 和凭据配置；遇到无法自动回退的版本，应用不会启动，日志会给出恢复备份的命令。

端口、备份目录、反向代理、备份恢复等更多内容见[部署说明](deploy/production/README.md)。

## 开发

工具链由 [mise](https://mise.jdx.dev/) 管理（Go 1.25.10、Node.js 24.22.1）。Go 和 Node 在宿主机运行，PostgreSQL 由根目录的
`compose.dev.yaml` 提供，只监听 `127.0.0.1`；Vite 与 Go 后端监听 `0.0.0.0`，局域网设备可以访问。

```bash
mise install
cp .env.dev.example .env.dev # 填写本地 PostgreSQL 密码
make dev                    # PostgreSQL + Go 后端（8080）+ Vite（5173）
```

前端修改热更新，Go 和迁移文件修改会自动重启后端；后端启动后很快退出时按 1、2、4…秒退避重启，最长间隔 30 秒。`Ctrl-C` 一起停止。开发和生产可以在同一台机器上同时运行，互不影响。

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

前端构建产物不提交：`npm run build` 写入被忽略的 `backend/cmd/roomusic/web/dist`，生产镜像构建时会重新生成。本地直接运行 Go 后端而没有构建前端时，接口照常可用，页面返回“前端未构建”的提示。

### 发布

```bash
scripts/release-check.sh vX.Y.Z               # 只读检查
git tag -a vX.Y.Z -m "ROOMusic vX.Y.Z" && git push origin vX.Y.Z
```

推送标签后，GitHub Actions 会确认该提交在 `main` 上的 CI 已经通过，然后构建 amd64 和 arm64 镜像，推送到
`ghcr.io/qlqqs/roomusic`，并发布 Release 说明。

## 文档

- [部署说明](deploy/production/README.md)：配置、备份恢复、反向代理、排查
- [用 MCP 客户端连接](docs/steward-mcp.md)：访问令牌与 Claude Code 的接入
- [插件开发说明](docs/plugins.md)：数据源插件的清单、调用约定与打包
- [匿名统计说明](docs/insights.md)：发送哪些字段、什么时候发、怎么关闭
- [开发与测试环境](docs/development-environment.md)：工具链、外部接口 mock、UPnP 假渲染器
- [架构决策](docs/architecture/architecture-decisions.md)：长期规则与 ADR 索引
- [前端设计依据](frontend/DESIGN.md)
- [参与贡献](CONTRIBUTING.md)：提交改动的要求与贡献者许可协议

## 许可证

Copyright (C) 2026 qlqqs

ROOMusic 以 [GNU Affero 通用公共许可证第 3 版](LICENSE)（AGPL-3.0-only）发布：可以自由使用、修改和分发；
分发修改版，或者把修改版作为网络服务提供给他人使用时，需要以同一许可公开修改后的源码。随附的第三方程序与数据见[第三方组件与许可](THIRD-PARTY-NOTICES.md)。

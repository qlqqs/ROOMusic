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

生产由三个容器组成：`app`（应用，内含固定版本的 ffmpeg）、`postgres`（PostgreSQL 18）和 `backup`（每日备份）。
不需要 Redis 或 Meilisearch。数据库不对外发布端口，应用只在 IPv4 上发布一个端口。

镜像只从 Git 提交构建：默认 `main`，也可以用 `REF=` 指定。工作区里未提交的改动不会进入镜像；构建不需要宿主机装 Go 或 Node，
前端用仓库里已提交的构建产物。

完整的配置说明、备份恢复细节和本机实例的具体参数见 [生产部署说明](deploy/production/README.md)。

### 准备

宿主机需要 Git、Bash、Make、Docker（带 BuildKit）和 Docker Compose v2。构建时要能访问基础镜像、Go 模块和 Alpine 包仓库；
宿主机已设置的 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 会透传给构建。

准备两个目录，都要让 uid 1000 能访问：

- 音乐目录：可读即可，生产以只读方式挂载为容器内的 `/music`；
- 备份目录：可写，放在仓库之外。

### 首次部署

```bash
cp .env.prod.example .env.prod
chmod 600 .env.prod
# 编辑 .env.prod：替换数据库密码（可用 openssl rand -hex 24 生成），填好端口、音乐目录、备份目录和子网
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod config --quiet
make prod
make prod-status
```

主要配置：

| 变量 | 说明 |
| --- | --- |
| `ROOMUSIC_DB_PASSWORD` | 必填，只用字母和数字。首次启动后再改这里不会改库里的密码 |
| `ROOMUSIC_HTTP_PORT` | 必填，应用对外端口，例如 `8090` |
| `ROOMUSIC_MUSIC_DIR` | 必填，宿主机音乐目录的绝对路径 |
| `ROOMUSIC_BACKUP_DIR` | 必填，宿主机备份目录的绝对路径 |
| `ROOMUSIC_SUBNET` | 必填，固定的 Docker 子网，例如 `10.203.50.0/24` |
| `ROOMUSIC_ALLOWED_ORIGINS` | 可选，见下文[访问地址与反代](#访问地址与反代) |
| `ROOMUSIC_TIMEZONE` / `ROOMUSIC_BACKUP_HOUR` / `ROOMUSIC_BACKUP_KEEP_DAYS` | 可选，备份时区、每天几点后备份、保留天数，默认 `Asia/Shanghai`、`3`、`14` |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | 可选，ALAC 转换缓存上限，默认 `10240`，最小 `256` |

值写成 `KEY=值` 的字面量，不要用 `${变量}` 或命令替换。不要填 `ROOMUSIC_SECURE_COOKIES` 或 `ROOMUSIC_PUBLIC_URL`，
生产里填了会拒绝启动。

**`.env.prod` 不在 Git 里，请另存一份。** `git clean -x` 会把它和被忽略的 `music/` 一起删掉，不要在部署目录里运行。

### 首次使用

**先初始化管理员，再开放公网反代**，免得有人抢先创建管理员。

1. 打开 `http://<服务器地址>:<端口>`，按页面提示创建管理员。
2. 在“扫描目录”里添加 **`/music`**（或它的子目录），这是容器内的路径，不是宿主机路径。
3. 开始扫描，扫描完就能浏览和播放。
4. 在“成员”页添加家人或朋友的账号。

### 日常运维

| 操作 | 命令 |
| --- | --- |
| 启动或升级（构建 → 备份 → 换版本 → 等待健康） | `make prod` |
| 回退到指定提交 | `make prod REF=<提交>` |
| 查看状态、运行版本和地址 | `make prod-status` |
| 跟随最近 200 行日志 | `make prod-logs` |
| 立即备份 | `make prod-backup` |
| 重置密码 | `make prod-reset-password U=<用户名>` |
| 停止（保留容器和数据） | `make prod-stop` |

**不要对生产项目运行 `docker compose … down -v`，它会删除数据库。** 所有 `make prod-*` 命令都不会删数据卷。
启动和升级只用 `make prod`，不要手工 `docker compose up`：脚本会用目标提交里的 Compose 文件，并清理 shell 里残留的
`ROOMUSIC_*`、`COMPOSE_*` 变量。手工 `docker compose` 只用来查询、`exec` 或排查，写法见[生产部署说明](deploy/production/README.md#日常运维)。

### 升级与回退

代码合进 `main` 并提交后执行 `make prod`。脚本会先构建镜像，再对当前数据库做一次备份，**备份失败就不换版本**；
镜像构建失败也不影响正在运行的实例。同一提交的镜像已存在时跳过构建。

数据库迁移在应用启动时自动执行，不提供降级迁移。如果新版本已经跑过新迁移，回退到旧版本会因为不认识新迁移而启动失败，
这时用升级前的备份恢复后再回退。旧镜像不会自动清理，确认不再需要后按标签删除。

### 备份与恢复

`backup` 容器每天在设定时间后备份一次 PostgreSQL，保留 14 天，文件为备份目录下的 `roomusic-<时间>.dump`
（手动备份带 `-manual` 后缀）。超过 26 小时没有成功备份时，`backup` 的健康检查会失败。

备份只包含数据库。音乐文件本身、封面和 ALAC 转换缓存都不在里面；转换缓存可以重新生成。备份和数据库在同一台机器上，
需要的话请另外做异地备份。备份文件含账号等私人数据，权限为 600，请妥善保管。

```bash
# 恢复到临时库核对，不动生产
deploy/production/restore.sh check <备份目录>/roomusic-<时间>-manual.dump
# 恢复到生产库，会覆盖现有数据；脚本会先自动备份当前库，并要求输入 roomusic-prod 确认
deploy/production/restore.sh production <备份目录>/roomusic-<时间>-manual.dump
```

恢复失败时应用保持停止，脚本会打印用安全备份再恢复一次的命令；不要在失败后自行启动应用。

### 访问地址与反代

默认不需要配置：浏览器地址栏里的网址和请求的主机（含端口）一致，就允许登录、收藏等写操作，http 和 https 都可以。
登录 Cookie 在 https 下带 `Secure`，http 下不带。局域网用 IP、公网用域名访问时，两边各自登录。

经过反代时，反代必须原样保留浏览器的 Host（含端口）和 Origin。nginx 写成：

```nginx
proxy_set_header Host $http_host;
```

不要用 `$host`，它会丢掉端口。Host 被改写时页面能浏览，但写操作会返回 `403 origin_forbidden`，日志里有 `identity.origin.rejected`。
修不了反代时，在 `.env.prod` 里列出所有允许的完整网址，再 `make prod`：

```dotenv
ROOMUSIC_ALLOWED_ORIGINS=http://192.168.1.10:8090,https://music.example.com
```

每一项都是 `http(s)://主机[:端口]`，不能带路径或通配符。应用不读取 `X-Forwarded-*` 或 `Forwarded` 头；
登录限速按 TCP 对端地址计算，经过反代的用户会共用一个限速来源。

### 忘记密码

成员忘记密码时，管理员在“成员”页点这一行的“重置密码”设一个新密码告诉对方，对方登录后可以在侧栏“修改密码”里改掉。

管理员自己忘记密码时，在服务器上运行：

```bash
make prod-reset-password U=<用户名>
```

在终端里输入两次新密码（不显示）。不需要停服务；成功后这个账号在所有设备上退出登录。重置后如果还提示尝试次数太多，等一分钟再试。

### 播放与歌词

浏览器大多不能解码 ALAC。服务端第一次播放 ALAC 曲目时用 ffmpeg 把它无损转成 FLAC，缓存在应用的数据卷里，等待时播放栏显示“正在准备”；
缓存超过上限时按最近使用淘汰。启动时会做一次转换自检，失败时应用照常运行，ALAC 曲目会标注暂时不能播放，日志里有
`playback.derivation.unavailable`。

歌词取自音频文件的内嵌标签，不读外置 `.lrc`，也不联网获取。从不带歌词的版本升级上来后，需要重新扫描一次才有歌词。

### 日志与健康检查

应用输出结构化 JSON 日志，每个请求结束时写一条 `http.request.completed`，包含请求 ID、路由模板、状态码和耗时，
不记录查询字符串、请求体、Cookie、密码或完整的音乐路径。健康检查使用 `/readyz`。容器日志按 10 MB × 5 个文件轮转。

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

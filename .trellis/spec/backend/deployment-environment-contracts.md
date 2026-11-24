# 部署环境与 CI 合同

运行合同总览见 [Core 0 当前运行合同](./core0-runtime-contracts.md)。

### 环境与资产

- Node/Go 版本由 `.mise.toml` 管理，依赖安装使用 `npm ci`；`package-lock.json`
  是前端可重复安装依据，`package.json` 的直接依赖全部精确固定（M1a）。
- Vite 的生产构建目标是 `backend/cmd/roomusic/web/dist`（Git 忽略，不提交、不手工编辑）；`web/` 下只跟踪说明文件，
  `//go:embed web` 在没有 `dist` 时仍能编译。没有产物时后端照常启动，记一条 `platform.frontend.missing`，非 `/api` 路径返回 503 中文提示。
  生产镜像在 Node 阶段构建前端（2026-11-15 起，此前产物随源码提交并由 CI 检查漂移）。开发
  `vite.config.dev.ts` 才允许自定义 Host 和 API 代理。
- 开发 Vite 通过公网反代访问时，`allowedHosts` 只放行 `ROOMUSIC_PUBLIC_URL` 的主机名，
  不得使用 `allowedHosts: true`；后端在非生产且未配置 `ROOMUSIC_ALLOWED_ORIGINS` 时按 `ROOMUSIC_PUBLIC_URL`、Origin scheme/host
  和 `ROOMUSIC_SECURE_COOKIES` 校验写请求；生产未配置清单时按主机一致校验，只比较规范化的 Origin 与请求 Host；
  配置清单后只按清单。主机一致与清单模式的 Cookie `Secure` 都跟随请求来源协议，任何模式都不读转发头。
- `.env.dev`、`.env.prod`、`.env` 是本机配置，必须被 Git 忽略；仓库只跟踪不含真实值的
  `.env.dev.example` 与 `.env.prod.example`。开发脚本默认读 `.env.dev`，不 source 根目录 `.env`；生产可在仓库根目录用 `.env.prod`（`docker compose -f compose.prod.yaml --env-file .env.prod up -d --wait`），或从 `main` 下载部署文件，在仓库外部署目录用 `.env` 与 `compose.yaml`。部署文件默认使用 `latest`。`backend/data/` 等运行期数据（含封面缓存；旧版本的 `media-cache/` 在启动时自动删除）同样不得提交。
- 服务端渲染需要 ffmpeg：生产镜像构建时从固定摘要镜像复制，开发由部署者安装（项目不单独分发二进制）；`ROOMUSIC_FFMPEG_PATH`（绝对路径，空则在 `PATH` 中查找）。
  找不到或自检失败时应用照常启动、记 `playback.render.unavailable`，格式表为空、起播得 `503 renderer_unavailable`。`ROOMUSIC_MEDIA_CACHE_MAX_MIB` 已不再读取。
  `scripts/ffmpeg-pinned.sh` 取出按摘要固定的 ffmpeg 7.1.1，测试、CI 与推荐部署共用。详见[服务端渲染合同](./playback-render-contracts.md) §11。
- 声学指纹需要 `fpcalc`（M7 B3）：固定 chromaprint 上游官方构建 1.6.2（`linux-x86_64`、`linux-arm64` 两个压缩包，按 sha256 核对）。
  x86_64 包是静态构建；arm64 包链接 glibc 2.28 以上（`libc`、`libm`、`libstdc++`、`libgcc`，FFmpeg 已静态链入），在 Alpine 上直接运行报 `fpcalc: not found`
  （v0.8.0 首次发布时 arm64 构建因此失败），`gcompat` 缺 `fcntl64` 等符号也跑不起来。arm64 上由 `glibc` 阶段从固定摘要的 `debian:12-slim` 取加载器与这四个库，
  放进 `/opt/fpcalc/lib`，真正的二进制在 `/opt/fpcalc/fpcalc`，`/usr/local/bin/fpcalc` 是经该加载器（`--library-path /opt/fpcalc/lib`）启动它的包装脚本；
  这些库不进入 Alpine 的系统库路径。x86_64 上 `/usr/local/bin/fpcalc` 就是静态二进制。
  生产镜像的 `fpcalc` 阶段在**目标架构**上（不用 `--platform=$BUILDPLATFORM`）用 `ADD --checksum` 取两包、按 `TARGETARCH` 解开、核对 `fpcalc version 1.6.2`；
  运行镜像放在 `/usr/local/bin/fpcalc`（约 5.4 MB），构建时再核对版本，并按应用的调用方式（ffmpeg 生成合成正弦波、s16le 管道）算一次指纹；`compose.prod.yaml` 设 `ROOMUSIC_FPCALC_PATH`（绝对路径，空则在 `PATH` 中查找）。
  `scripts/fpcalc-pinned.sh` 下载并缓存到 `${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/fpcalc-1.6.2/`，核对 sha256 与版本后打印路径；`--pins` 打印两架构的地址与摘要，CI 逐行在 Dockerfile 里核对。
  启动时与 ffmpeg 一起自检（合成 PCM 走一遍管道）；通过记 `playback.fingerprint.ready`（带 fpcalc 版本），不通过只记 `playback.fingerprint.unavailable`，AcoustID 显示 `fingerprint_unavailable`，其余功能不受影响。
- 内置 AcoustID 应用密钥（ADR 0006 决策记录“AcoustID 内置应用密钥”）：生产镜像 Go 构建的 `RUN` 挂载可选 BuildKit secret `acoustid_client`；文件非空才读，内容只许字母数字（否则构建失败），
  经 `-X github.com/qlqq/roomusic/backend/internal/sources/acoustid.bundledClientKey=` 链接进二进制。没有 secret 时构建照常，结果是要管理员填两把密钥的镜像。
  镜像历史只记未展开的命令；密钥在二进制与 `go version -m` 的链接参数里可见（客户端应用密钥都如此）。secret 内容不参与构建缓存键：本地先无后有地构建要 `--no-cache`，
  发布流水线以后若加构建缓存要另行处理。发布流水线从仓库 secret `ACOUSTID_CLIENT_KEY` 传入；CI 与冒烟构建不传。
  指纹只在自动整理走到这一步时按需计算，与 `ROOMUSIC_AUDIO_ANALYSIS` 无关（关掉分析时指纹仍会解码音频的前 120 秒）。
  许可：上游二进制未修改，LGPL 2.1；内含 `acoustid/ffmpeg-build` v8.0-1 的纯音频 FFmpeg 8.0 静态库（不带 `--enable-gpl`）。源码：
  `https://github.com/acoustid/chromaprint/tree/v1.6.2`、`https://github.com/acoustid/ffmpeg-build/tree/v8.0-1`。
- 开发 Compose 项目名 `roomusic-compose-dev`，只有 PostgreSQL；`scripts/dev.sh` 确保 PostgreSQL 后在宿主机运行 Go 和 Vite。
  后端启动后 10 秒内退出时按 1、2、4…秒退避重启（上限 30 秒），文件变更或运行满 10 秒后退避归零。开发的 `mise run env-*` 显式使用 `--env-file .env.dev`。
- 端口：应用端口（Vite、Go 后端）绑 `0.0.0.0`，开发与测试的 PostgreSQL 只绑 `127.0.0.1`。
- 开发与测试的 PostgreSQL 镜像与 `compose.prod.yaml` 的 `x-postgres-image` 是同一个钉死字符串，CI 校验三者一致。
- 生产镜像固定 Node、Go、Alpine、PostgreSQL 基础镜像版本与摘要，ffmpeg 与 `scripts/ffmpeg-pinned.sh` 同一摘要；
  Node 阶段（`--platform=$BUILDPLATFORM`，版本与 `.mise.toml` 一致，CI 校验）`npm ci` 后 `npm run build`，Go 阶段校验 `dist/index.html`、
  运行 `TestEmbeddedAssetsAreContentHashed` 后内嵌；运行镜像不含 Node。构建上下文的排除规则在 `deploy/production/Dockerfile.dockerignore`。应用以 `PUID:PGID`（默认 1000:1000）运行，音乐只读挂载。
  生产 Compose 包含 postgres（健康检查）→ app 与独立的 ptp 服务，顶层 `name: roomusic-server`。固定容器名默认为 `roomusic-app`、`roomusic-postgres`，分别通过 `ROOMUSIC_APP_CONTAINER_NAME`、`ROOMUSIC_POSTGRES_CONTAINER_NAME` 配置。ptp 默认名为 `roomusic-ptp`，由 `ROOMUSIC_PTP_CONTAINER_NAME` 配置。同机多实例使用不同名称。首次安装直接 up，升级 pull 后 up。应用镜像仓库由 `ROOMUSIC_IMAGE` 指定，默认 `ghcr.io/qlqqs/roomusic`。必填仅 `ROOMUSIC_MUSIC_DIR`。
  `./secrets`、`./postgres`、`./data`、`./backups` 全部为 bind mount；PostgreSQL 18 挂载点为 `/var/lib/postgresql`，不创建命名或匿名卷。
  postgres 服务启动时只将属主为 root 的 secrets/data/backups 顶层目录改归 `PUID:PGID`，目录权限 0700。密码文件属主为 `PUID:PGID`、权限 0600。首次生成 32 字节随机十六进制密码，已有密码继续复用。PGDATA 非空但密码文件缺失或为空时停止。
  PostgreSQL 官方入口以 root 读取 `POSTGRES_PASSWORD_FILE`，导出密码并清除文件变量后降权；应用通过 `ROOMUSIC_DB_PASSWORD_FILE` 在 config.DatabaseURL 中读取，使用 URL 编码组装，服务和所有 CLI 共用。未配置文件时保留原 DATABASE_URL 行为；文件模式不接受 URL 内另一份密码，读取错误不输出密码或路径。
  生产应用默认使用 host 网络，监听 `0.0.0.0:${ROOMUSIC_HTTP_PORT:-8090}`。数据库保留独立项目网络，只发布 `127.0.0.1:${ROOMUSIC_DB_PORT:-55439}`，应用按该地址连接。健康检查访问实际监听端口；同机多实例的两个端口均需独立。
  ptp 与 app 使用同一镜像，入口为 `/app/roomusic-ptp`，在 host 网络收发 UDP 319/320。ptp 以 `0:0` 和只读根运行，丢弃 ALL 后仅保留 NET_BIND_SERVICE，启用 no-new-privileges。app 丢弃 ALL，以 PUID:PGID 运行。应用经 `ROOMUSIC_PTP_ADDR`（默认 `127.0.0.1:55440`）查询本地时钟；UDP 端口占用或时钟不可达时，兼容设备使用 NTP，必须 PTP 的设备显示“精确对时不可用”。生产镜像对时演练使用独立网络命名空间。
  `deploy/production/compose.bridge.yaml` 配合主文件将应用接回项目网络，恢复容器内 8080 及服务名 postgres，并清除数据库发布端口与 ptp 服务。bridge 部署不支持 AirPlay 原生精确对时。bridge 用户手动添加渲染器并设置 `ROOMUSIC_LAN_URL`；host 模式默认采用发现设备的网卡地址。主文件和无锚点的 NAS 单文件支持 Compose v2.20.1，bridge 覆盖文件需要 v2.24.4。
  `scripts/check-production-compose.sh` 检查 host、bridge 配置及 NAS 单文件与主文件的等价性。备份目录可用 `ROOMUSIC_BACKUP_DIR` 指定，postgres 和 app 挂载同一路径。子网默认由 Docker 分配，可通过 override 设置；不同开发测试项目使用独立网络和目录。
  入口等数据库接受连接后启动，`/readyz` 用于镜像健康检查。生产的 `ROOMUSIC_ALLOWED_ORIGINS` 可选，不配时按主机一致模式，反代须保留 Host（含端口）与 Origin；格式与冲突由后端校验。
- `ROOMUSIC_TIMEZONE`（默认 `Asia/Shanghai`）同时用于备份与定时扫描；启动时校验为有效时区，经 `config.Server.Backup.Location` 传入调度器。定时扫描默认每天 4 点、备份默认 3 点；扫描开关与整点由数据库设置接口管理，详见[扫描协调合同](./scan-coordination-contracts.md)第 9 节。
- 备份内置在应用（`backend/internal/backup`）：镜像含与数据库同主版本的 `postgresql18-client`；按 `ROOMUSIC_TIMEZONE` 每天
  `ROOMUSIC_BACKUP_HOUR` 点后 `pg_dump -Fc`，成功后按保留规则清理（天数与份数两个上限，最新一份不删；规则存于备份目录，
  从未在网页保存时用 `ROOMUSIC_BACKUP_KEEP_DAYS` 天，见[备份管理](./backup-management-contracts.md)），另有手动、迁移前与恢复前备份；
  写临时文件经 `pg_restore --list` 校验后原子发布，advisory lock `ROOMBACK` 串行；不备份 `/data` 或音乐文件。
  停止使用 `stop` 或 `down`，目录数据保留；密码恢复用 `docker compose exec app /app/roomusic reset-password <用户名>`。
  运维与恢复步骤见[部署说明](../../../deploy/production/README.md)。

- `ROOMUSIC_SCAN_WORKERS`：空值默认 4，整数 1–16；非整数或越界启动失败，固定错误 `ROOMUSIC_SCAN_WORKERS must be an integer between 1 and 16`。`config.Server.ScanWorkers` 经 app 的 `library.NewService(...).WithScanConcurrency(...)` 传给解析池与指纹规划；设为 1 串行读盘，数据库始终单写入者。生产 compose 使用 `${ROOMUSIC_SCAN_WORKERS:-4}`，`.env.prod.example` 提供注释示例，不要求配置者额外填写。

### CI 质量门禁

- `.github/workflows/ci.yml` 在 Pull Request、推送到 `main` 和手动触发时运行，权限
  固定为 `contents: read`，同一分支的新运行会取消旧运行。
- 工作流使用 `.mise.toml` 中的 Go `1.25.10` 和 Node.js `24.16.0`。后端门禁从
  `backend/` module 运行 `gofmt`、`go test`、`go test -race`、`go vet` 和 `go build`；
  前端作业使用 `frontend/package-lock.json` 依次执行 `npm ci`、lint、typecheck、`npm run test`
  （Vitest）与 `npm run build`，没有“入口缺失”分支。
- 生产构建之后 `npx playwright install --with-deps chromium` 与 `npm run test:e2e`（对生产构建的浏览器冒烟、无障碍与
  响应式检查，失败时上传报告）。
  集成 job 逐个执行 `bash -n` 检查 `scripts/*.sh` 与 `deploy/production/*.sh`，校验开发、测试 Compose，运行
  `scripts/check-production-compose.sh`（假值、只解析不启动：必填中文提示、服务与端口、默认与自定义备份目录、无数据卷、无初始化服务及依赖、固定容器名参数、override 子网），
  检查 ffmpeg、Go 与 PostgreSQL 客户端主版本一致性（CI 不构建镜像），运行 `python3 scripts/tests/test_release_check.py`，
  并运行 `./scripts/test-integration.sh`（时限 15 分钟，job 30 分钟）；后者先经 `scripts/ffmpeg-pinned.sh`、`scripts/fpcalc-pinned.sh` 取出固定版本的 ffmpeg 与 fpcalc（`ROOMUSIC_TEST_FFMPEG`、`ROOMUSIC_TEST_FPCALC`；取不到即失败，
  派生用例不得静默跳过），真实启动 PostgreSQL 18 并运行 `TestPostgreSQL|TestFFmpeg`，结束时清理专用容器和数据卷。
  真实播放 job 运行 `./scripts/playback-e2e.sh`，同样经该脚本取得同一个 ffmpeg。
- **API 响应样本**：集成测试 `TestPostgreSQLAPIResponseSamples`（`backend/internal/app/api_samples_integration_test.go`）
  经真实 handler 走固定流程，把响应写成 `backend/internal/app/testdata/api-samples/` 下的 JSON 样本（只含状态码与响应体）：
  UUID 与时间戳规范化为固定值、请求 ID 固定、按键排序。默认**逐字比较**，不一致或目录里有本次没有产出的文件都失败；
  确认是有意的合同变更后用 `ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 重写（会删除多余文件），并同步前端
  decoder。样本由集成作业把关漂移，前端作业读取它们（`frontend/src/test/api-contract.test.ts`，Playwright 的受控响应也取自样本）。
  测试断言样本不含允许根目录的绝对路径、临时目录前缀、`password`、`roomusic_session`、会话令牌与测试密码；扫描素材是临时目录里的
  合成文件，不读取真实音乐。
- CI 不启动或依赖 Redis/Meilisearch，不读取生产 `.env.prod`，也不使用真实音乐目录。普通
  测试因未设置 `ROOMUSIC_TEST_DATABASE_URL` 而跳过时，不得被视为 PostgreSQL 集成门禁
  通过。


## 外部接口 mock 地址

所有环境均可设置 `ROOMUSIC_UPSTREAM_MOCK_URL`。值为带主机的 HTTP(S) 绝对基础地址，可带路径前缀，不能含凭据、查询或片段。
加载时检查协议、主机与有效端口，地址无效则启动失败。空值沿用官方外部源及 Tavily 的公网出站策略。
启用后启动日志以 WARN 记录 `sources.mock.enabled` 和目标地址。仅四个外部音乐源与 Tavily 增加这个主机与有效端口的放行项；
CAA 的重定向同时接受该目标，成员端点放行名单和网页抓取策略照常。运行方式见 [开发环境](../../../docs/development-environment.md#外部接口-mock)。

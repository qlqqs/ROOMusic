# 部署环境与 CI 合同

运行合同总览见 [Core 0 当前运行合同](./core0-runtime-contracts.md)。

### 环境与资产

- Node/Go 版本由 `.mise.toml` 管理，依赖安装使用 `npm ci`；`package-lock.json`
  是前端可重复安装依据，`package.json` 的直接依赖全部精确固定（M1a）。
- Vite 的生产构建目标仍为唯一的 `backend/cmd/roomusic/web`，不手工编辑该目录；产物随源码提交，
  CI 运行真实构建与资产漂移检查（M1a 恢复）。开发
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
- `scripts/dev.sh` 默认只确保 PostgreSQL、Go 和 Vite；Redis/Meilisearch 是
  Compose/mise 可选服务。开发的 `mise run env-*` 显式使用 `--env-file .env.dev`。
- 生产镜像固定 Go、Alpine、PostgreSQL 基础镜像版本与摘要，ffmpeg 与 `scripts/ffmpeg-pinned.sh` 同一摘要；
  使用已提交的前端，不依赖 Node。应用以 uid 1000 运行，音乐只读挂载；仅首次安装以 root 在宿主机运行 `scripts/production-init.sh`，镜像不包含初始化脚本。
  生产 Compose 仅包含 postgres（健康检查）→ app，顶层 `name: roomusic-server`。固定容器名默认为 `roomusic-app`、`roomusic-postgres`，分别通过 `ROOMUSIC_APP_CONTAINER_NAME`、`ROOMUSIC_POSTGRES_CONTAINER_NAME` 配置。同机多实例使用不同名称。Compose 不引用、不执行、不依赖初始化脚本；升级直接 pull 后 up。必填仅 `ROOMUSIC_MUSIC_DIR`。
  `./secrets`、`./postgres`、`./data`、`./backups` 全部为 bind mount；PostgreSQL 18 挂载点为 `/var/lib/postgresql`，不创建命名或匿名卷。
  宿主机脚本依赖 Python 3 解析 Compose JSON 配置，不执行 dotenv 内容；只调整 secrets/data/backups 顶层权限；密码文件属主 uid 1000、0600，secrets/data/backups 目录 0700。新建 postgres 挂载根目录为 0755，允许 PostgreSQL 降权后访问；不修改已有数据库目录权限。首次生成 32 字节随机十六进制密码，也可通过 `ROOMUSIC_DB_PASSWORD` 指定；后续启动直接读取，重复执行脚本复用，显式不同密码报错。数据库目录非空但密码文件缺失时停止。
  PostgreSQL 官方 root 入口读取 `POSTGRES_PASSWORD_FILE` 后降权；应用通过 `ROOMUSIC_DB_PASSWORD_FILE` 在 config.DatabaseURL 中读取，使用 URL 编码组装，服务和所有 CLI 共用。未配置文件时保留原 DATABASE_URL 行为；文件模式不接受 URL 内另一份密码，读取错误不输出密码或路径。
  端口默认 8090（IPv4），备份目录可用 `ROOMUSIC_BACKUP_DIR` 指定，首次安装脚本和 app 使用同一路径。子网默认由 Docker 分配，可通过 override 设置；不同开发测试项目使用独立网络和目录。
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
- 生产构建之后执行 `git diff --exit-code -- backend/cmd/roomusic/web`，并检查该目录没有未跟踪生成文件；
  随后 `npx playwright install --with-deps chromium` 与 `npm run test:e2e`（对生产构建的浏览器冒烟、无障碍与
  响应式检查，失败时上传报告）。
  集成 job 逐个执行 `bash -n` 检查 `scripts/*.sh` 与 `deploy/production/*.sh`，校验开发、测试 Compose，运行
  `scripts/check-production-compose.sh`（假值、只解析不启动：必填中文提示、服务与端口、默认与自定义备份目录、无数据卷、无初始化服务及依赖、固定容器名参数、override 子网），
  运行 `python3 scripts/tests/test_production_init.py`（合成配置，替换 id/chown 验证权限调用，其余文件操作与配置解析真实执行），
  检查 ffmpeg、Go 与 PostgreSQL 客户端主版本一致性（CI 不构建镜像），运行 `python3 scripts/tests/test_release_check.py`，
  并运行 `./scripts/test-integration.sh`（时限 15 分钟，job 30 分钟）；后者先经 `scripts/ffmpeg-pinned.sh` 取出固定版本 ffmpeg（取不到即失败，
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


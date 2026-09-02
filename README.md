# ROOMusic

ROOMusic 当前提供可运行的 Core 0 本地开发与同源生产闭环，不迁移 V0 中已经膨胀的应用容器、AI worker、operator、release overlay 或业务配置。

## 软件目标

ROOMusic 的长期目标是面向个人、家庭和小型私有群体的自托管音乐库管家。系统以 `ReleaseGroup -> Release -> Medium -> Track` 为核心理解发行版本，而不是只把音乐呈现为文件夹列表。后续能力可以覆盖浏览、搜索、播放、metadata 整理和受控 Agent 操作，但必须建立在稳定身份、来源可解释、后端权限、事务、操作记录和恢复边界之上。

当前 Core 0 先交付最小可用闭环：初始化和登录、允许目录内的只读扫描、保守的 Release Graph、Web 浏览、基础搜索，以及目录配置的 Change Set/Operation Journal。Redis、Meilisearch、GraphQL、播放、文件写入和可运行的 Agent runtime 都不是 Core 0 的强制依赖。

长期只保留一个产品 Agent：`Music Steward`。`Assistant`、`Steward`、`Operator` 是三种审批模式：Assistant 的危险操作由用户批准，Steward 由独立 Review Subagent 审查，Operator 由管理员显式进入后跳过审批直接执行。三种模式仍统一经过后端注册工具、权限和范围校验、结构化日志、Operation Journal 及适用的恢复机制；Operator 不等于无限制 shell、任意 SQL 或任意宿主机文件访问。

运行日志不承担“业务 Git”的职责。可恢复变更由 `Change Set + Operation Journal + Checkpoint + Reversible Executor` 描述；Core 0 只实现目录配置的窄闭环，不引入完整 Event Sourcing 或文件级恢复系统。

## 环境组成

- Go 1.25.10
- Node.js 24.16.0
- PostgreSQL 18
- Redis 8
- Meilisearch 1.45.0

Go 与 Node.js 在宿主机运行；PostgreSQL、Redis 和 Meilisearch 由 Docker Compose 承载。三个服务的端口只绑定到 `127.0.0.1`。

## 首次使用

安装工具链：

```bash
mise install
```

如需重建本地开发配置：

```bash
cp .env.example .env.dev
```

启动依赖：

```bash
mise run env-up
```

检查环境：

```bash
mise run env-check
```

## 快捷开发工作流

默认复制 `.env.example` 为 `.env.dev`（或通过 `ROOMUSIC_ENV_FILE` 指定配置）并填写本地 PostgreSQL 密码，可用一条命令启动 PostgreSQL、Go 后端和 Vite 前端：

```bash
./scripts/dev.sh
```

前端开发地址为 `http://localhost:5173`，后端地址为 `http://localhost:8080`。React 修改由 Vite 热更新；Go 或迁移文件修改会自动重启后端。按 `Ctrl-C` 会同时停止两个开发进程，PostgreSQL 容器保持运行。也可使用 `make dev` 作为便利入口。

生产环境使用无后缀 `.env`，由 Go 单体直接提供 `8080`：

```bash
./scripts/prod.sh
```

生产配置必须设置 `ROOMUSIC_SECURE_COOKIES=true`。生产脚本不会启动 Vite，也不要求安装 Make。

如需重新体验首次初始化流程，开发环境可清空业务数据：

```bash
make dev-reset
# 或 CONFIRM=1 make dev-reset（跳过交互确认）
```

该命令会删除用户、会话、目录、扫描结果和操作日志，但保留数据库迁移；`ROOMUSIC_ENV=production` 时会拒绝执行。

查看日志或停止依赖：

```bash
mise run env-logs
mise run env-down
```

## PostgreSQL 集成测试

普通 `go test ./...` 不会隐式启动数据库；未设置
`ROOMUSIC_TEST_DATABASE_URL` 时，PostgreSQL 集成测试会明确跳过。提交或 CI
门禁应使用独立入口启动一次性的 PostgreSQL 18 容器：

```bash
./scripts/test-integration.sh
# 或 mise run test-integration
```

该脚本只启动 `compose.test.yaml` 中的 PostgreSQL，默认监听
`127.0.0.1:55432`，每个测试使用独立 schema，结束后删除测试容器和数据卷。
可通过 `ROOMUSIC_TEST_PG_PORT`、`ROOMUSIC_TEST_PG_DATABASE`、
`ROOMUSIC_TEST_PG_USER`、`ROOMUSIC_TEST_PG_PASSWORD` 调整连接参数；设置
`ROOMUSIC_TEST_KEEP_DB=true` 可在本地调试时保留容器。CI 应显式执行该入口，
不能把集成测试跳过当作数据库门禁通过。

## 数据库迁移执行器

应用启动会从 Go `embed.FS` 读取 `backend/migrations/` 中连续编号的 SQL，使用
固定事务级 `pg_advisory_xact_lock`（`0x524f4f4d55534943`）防止多实例并发升级。
每个成功版本在 `schema_migrations` 保存文件名、原始字节 lowercase SHA-256 和
应用时间；重复启动只校验记录，不重放已完成 SQL。0008 迁移为旧记录增加元数据，
并兼容历史执行器只记录 0001、0006、0007 的数据库：0002--0005 会被一次性基线
补记，不会重复执行。

迁移 SQL、DDL 和记录在一个事务中提交；锁等待取消、校验和漂移、未知版本或任一
SQL 失败都会回滚并阻止服务进入 ready。项目不提供 down migration，生产回退请
使用数据库备份或新增 forward-fix 迁移，禁止修改已经共享的迁移文件。

## 迁移边界

本次只迁移可复用的开发工具链和基础数据服务配置。以下 V0 内容不会自动迁移：

- PostgreSQL、Redis、Meilisearch 的旧数据卷
- `.env` 中的生产密码、Token 或 API key
- `app`、`worker-ai`、`operator` 等应用服务
- AI provider、MusicBrainz、认证、扫描和发布配置

后续每引入一个实际能力，再同步增加对应依赖与环境变量，避免配置先于功能膨胀。

# 部署体验改进：备份内置、配置精简、版本化镜像与双向迁移

## 目标

让任何人（包括本机生产实例）部署 ROOMusic 时只需要 app + PostgreSQL 两个服务、只填数据库密码和音乐目录，
升级和降级都只要改 compose 里的镜像版本号再 `docker compose up -d`。

## 背景与已确认事实（2026-11-06 调研）

详见 [后端调研](research/backend-facts.md)、[前端调研](research/frontend-facts.md)。

- 备份现在是独立的 `backup` 服务 + tools 镜像（`deploy/production/backup.sh`、`restore.sh`），app 镜像里的
  `postgresql17-client` 只给 `entrypoint.sh` 的 `pg_isready` 用，导不了 PG 18（pg_dump 主版本须不低于服务端）。
- 生产 compose 用 `:?` 强制 5 项：`DB_PASSWORD`、`HTTP_PORT`、`MUSIC_DIR`、`BACKUP_DIR`、`SUBNET`（`deploy/production/compose.yaml`）。
  `SUBNET` 的来由是本机 Docker 默认地址池下一个 `192.168.48.0/20` 会覆盖局域网 `192.168.50.0/24`，是本机特例。
- 镜像只能由 `make prod` 从 git 提交本地构建，标签为 12 位提交号，`pull_policy: never`；二进制、接口和页面都没有版本号；
  CI 没有任何镜像构建或发布。仓库在 `github.com/qlqqs/ROOMusic`，还没有任何 git tag。
- 迁移只能向前：全部待执行迁移在同一事务执行（`migrate.go:157`）；校验和覆盖整个 `.sql` 文件（`migrate.go:121`）；
  库里有不认识的版本时报 `unknown version`（`migrate.go:443`）；迁移失败时进程不退出而进入降级模式，`/readyz` 返回 503（`main.go:44-49`）。
  规范 `.trellis/spec/backend/core0-runtime-contracts.md:124` 明写"不提供 down migration"，本任务要改这条合同。
- 现有 30 个迁移里 0011 不可逆，0001/0008 是执行器基础。**但第一个带回退机制的版本就已包含 0001–0030，降级永远不会低于 0030，
  所以现有迁移都不需要回退 SQL**；只有之后新增的迁移需要。

## 已定的决定

| 编号 | 决定 | 来源 |
| --- | --- | --- |
| D1 | 备份内置进 app：定时备份、手动备份、管理页查看状态和下载；恢复保留命令行；每次迁移（升级或降级）前自动备份 | 用户 2026-11-06 采纳推荐 |
| D2 | 子网、端口、备份目录改为可选；只有数据库密码与音乐目录必填；本机生产实例平滑过渡 | 用户 2026-11-06 采纳推荐 |
| D3 | 镜像发布到 GHCR，公开 | 用户 2026-11-06 |
| D4 | 降级：每个新迁移附带回退 SQL，执行迁移时存进 `schema_migrations`；旧版本启动发现库里有不认识的新迁移时，先自动备份再倒序执行库里的回退 SQL；无法回退的迁移明确拒绝并提示 | 用户 2026-11-06 采纳推荐 |
| D5 | 只发布正式版本号：用户说“发布”时由主会话打 `vX.Y.Z` 标签并推送，GitHub Actions 构建推到 GHCR；不发布 main 尝鲜镜像；去掉 `make prod` 本地构建路径，本机生产实例也改为改版本号拉取 | 用户 2026-11-06 |
| D6 | 镜像同时提供 amd64 与 arm64 | 用户 2026-11-06 |
| D7 | 管理员导航新增“系统”组，其中“备份”页显示当前版本号、最近一次成功备份、立即备份按钮和备份列表（含下载） | 用户 2026-11-06 |

技术取舍（设计 agent 推荐、主会话裁决，详见各子任务 design.md 的“裁决结果”）：增加“恢复前”备份来源；迁移前备份文件名带迁移前版本号；
开发与测试经 docker 包装的 PG 18 工具；回退所需两列由执行器添加、不新增迁移 0031；回退文件发布后不可修改；arm64 在 GitHub 原生 runner 上验证、本机不装 binfmt；
公开 compose 默认项目名 `roomusic-server`；固定子网用同目录 `compose.override.yaml`。

## 子任务

| 子任务 | 交付 | 顺序 |
| --- | --- | --- |
| [11-06-builtin-backup](../11-06-builtin-backup/prd.md) | app 内置备份、恢复命令、“系统 › 备份”页 | 第一 |
| [11-06-versioned-release](../11-06-versioned-release/prd.md) | 版本号注入、双向迁移、GHCR 发布流程 | 第二（迁移前备份调用 builtin-backup） |
| [11-06-config-trim](../11-06-config-trim/prd.md) | 精简后的生产 compose、示例配置、部署说明；删除旧部署组件 | 第三（需要前两项的镜像与代码） |

执行顺序、联合演练、收尾门禁、发布与本机切换见 [执行计划](implement.md)。

## 跨子任务验收

- [ ] X1 从 Release 下载 compose 与示例配置到空目录，只填两项，`docker compose up -d --wait` 后可以初始化管理员。
- [x] X2 升降级演练 A → B → A → B 全部 healthy、数据不丢、每次迁移前都有备份；不可回退时拒绝并指向正确的备份。
- [ ] X3 本机生产实例切换到 `ghcr.io/qlqqs/roomusic:0.1.0`：计数一致、端口 8090、反代 `https://music.qlqqs.com:8888` 可登录、
      备份继续写入 `/home/qlqq/roomusic-backups`、页面显示版本 0.1.0。

## 不做

- 网页上恢复或删除备份、异地备份、备份 `/data` 与音乐文件；发布 main 尝鲜镜像；修改宿主机 Docker 默认地址池。

## 规划方式

- 需求与产品决定由用户在 2026-11-06 的对话中确定（D1–D7）；技术设计由 Opus 子 agent 起草，主会话审阅（打回一处迁移前备份死锁问题）并按推荐裁决技术取舍。
- 进入实施的途径：用户批准最终规划摘要（2026-11-06 用户回复“开始执行”）。

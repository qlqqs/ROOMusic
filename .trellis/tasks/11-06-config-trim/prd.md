# 生产配置精简

父任务：[部署体验改进](../11-06-deploy-ux/prd.md)（决定 D2、D3、D5）。依赖 [builtin-backup](../11-06-builtin-backup/prd.md)
去掉 backup 服务后再定稿 compose。现状依据：[后端调研](../11-06-deploy-ux/research/backend-facts.md) §4。

## 目标

别人拿到一份 `compose.yaml` 和示例配置，只填数据库密码和音乐目录就能 `docker compose up -d`；
其余都有合理默认值，和常见的 Docker 项目一样不需要关心网络子网。

## 需求

- **R1 必填只剩两项**：`ROOMUSIC_DB_PASSWORD`、`ROOMUSIC_MUSIC_DIR`。缺失时 compose 给出一句中文提示。
- **R2 端口可选**：`ROOMUSIC_HTTP_PORT` 默认 8090，仍只发布 IPv4。
- **R3 子网可选**：默认不指定子网，由 Docker 自动分配。需要固定子网（如本机 Docker 地址池会撞上局域网）时，用 Docker Compose 惯用的方式
  额外指定，不要求所有人填写。
- **R4 备份目录可选**：不填时备份存在 Docker 命名卷里；填写宿主机绝对路径时改为挂载该目录。
- **R5 镜像来自 GHCR**：compose 使用 `ghcr.io/qlqqs/roomusic` 的版本标签，版本号可在示例配置里改；不再有 `pull_policy: never`、
  tools 镜像或本地构建标签。
- **R6 部署目录与仓库分离**：部署只需要 compose 文件和配置文件，放在任意目录即可，不依赖仓库工作区；
  去掉 `make prod*` 与 `scripts/production.sh` 的生产构建与启动路径。日常运维（状态、日志、立即备份、重置密码、恢复）
  在部署说明里写成普通的 `docker compose` 命令。开发环境（`make dev`、`.env.dev`）不受影响。
- **R7 本机生产实例平滑过渡**：给出本机从现在的 `roomusic-prod`（`make prod`、仓库根 `.env.prod`、固定子网 `10.203.50.0/24`、
  备份在 `/home/qlqq/roomusic-backups`）切换到新部署方式的步骤：Compose 项目名、数据库卷 `roomusic-prod_postgres-data`、
  数据卷 `roomusic-prod_app-data`、端口 8090、子网与备份目录保持不变，数据不迁移、不重建。
  实际切换在父任务收尾、首个版本发布后进行，切换前需用户确认。
- **R8 示例配置与说明**：示例配置文件只列必填项和常用可选项，每项一句话；部署说明改写为“下载 → 填两项 → 启动 → 升级/降级 → 备份与恢复”。
  文档用简体中文。

## 验收标准

- [ ] A1 在空目录只放 compose 文件和只填了两项的配置文件，`docker compose config --quiet` 通过；用本地构建的同结构镜像
      `docker compose up -d --wait` 后 app 与 postgres 都 healthy，可以初始化管理员。
- [ ] A2 缺少任一必填项时 `docker compose config` 报出该项的中文提示。
- [ ] A3 指定固定子网的方式在演练项目（`10.203.51.0/24`）中实际生效；“不指定时由 Docker 自动分配”只做静态核对（`docker compose config` 中没有 ipam），
      因为本机自动分配的网段会覆盖局域网，**本机不得创建未固定子网的网络**。
- [ ] A4 备份目录不填时写入命名卷；填宿主机目录时写入该目录，uid 1000 可写。
- [ ] A5 本机过渡步骤在演练项目上按原样执行一遍：沿用旧项目的卷与子网，切换后行数一致、备份目录继续产出新备份。
- [ ] A6 仓库中不再有指向 `make prod`、`scripts/production.sh`、`.env.prod.example` 旧必填项的引用（`git grep` 检查），CI 的 compose 校验改为新文件。

## 不做

- 修改宿主机 Docker 守护进程的默认地址池；反代、证书与域名配置。

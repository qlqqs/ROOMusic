# NAS 一键安装

父任务：`11-24-adoption-readiness`（第 3 项）。调研见 `../11-24-adoption-readiness/research/nas-install.md`。

## 目标

不会命令行的 NAS 用户也能装上：在 NAS 的 Docker 界面里建文件夹、粘贴一份 Compose、填音乐目录，启动即可。不再需要 sudo、Python 和首次安装脚本。
国内用户能顺利拉到镜像。

## 已定（qlqq）

- 主程序容器加固不放弃；NAS 的 uid 差异用 `PUID`/`PGID` 解决。
- 镜像加发 Docker Hub，文档默认用 Docker Hub 地址。

## 主代理取的默认值

| 项 | 默认 |
| --- | --- |
| 数据库密码 | postgres 服务首次启动时生成 32 字节随机密码写入 `secrets/postgres_password`（逻辑与原脚本相同：数据库目录非空而密码文件缺失时报错退出）；不新增服务 |
| 目录 | `secrets/`、`postgres/`、`data/`、`backups/` 由 Docker 自动创建；postgres 服务启动时（它本来以 root 启动）把 `secrets/`、`data/`、`backups/` 的**顶层**属主改为 `PUID:PGID`（只在属主是 root 时改，不递归，不碰音乐目录） |
| 应用身份 | `user: "${PUID:-1000}:${PGID:-1000}"`；仍是 `cap_drop: [ALL]`、`no-new-privileges`、只读根文件系统 |
| 首次安装脚本 | 删除 `scripts/production-init.sh` 及其测试；`ROOMUSIC_DB_PASSWORD` 不再支持（已有安装的密码文件照常使用） |
| 兼容 | 主 Compose 兼容 Compose v2.20.1（群晖）与 Docker Engine 24；不用 `!reset`、`configs.content`、`start_interval` |
| 单文件版本 | `deploy/nas/compose.yaml`：不依赖 `.env`，变量写成字面默认值，路径用 `/path/to/...` 占位并注明要改；由主文件派生，检查两者展开后等价 |
| 镜像地址 | Compose 用 `${ROOMUSIC_IMAGE:-ghcr.io/qlqqs/roomusic}`；发布流水线在配置了 Docker Hub 的 secret 与变量时同时推送 Docker Hub；qlqq 建好 Docker Hub 仓库后把默认值与文档切到 Docker Hub |
| 平台说明 | `docs/install-nas.md`：群晖、绿联、飞牛、极空间、威联通、TrueNAS、Unraid（用 Compose Manager 插件），每节写建文件夹、权限与 PUID/PGID、粘贴 Compose、端口、更新、国内拉取；纯文字，不放截图占位 |
| Unraid 模板、TrueNAS 应用目录、飞牛应用包 | 不做：单容器模板表达不了数据库依赖；TrueNAS 目录要求初始化容器 |

## 验收

- 空目录里只放 Compose 与 `.env`（只填音乐目录），`docker compose up -d --wait` 成功，能创建管理员并扫描。
- 模拟 NAS 目录属主（1026:100、1000:10、99:100、568:568）与 Docker 自动创建的 root 目录，以对应 `PUID`/`PGID` 启动都能写入 `data/` 与 `backups/`。
- 数据库目录非空而密码文件缺失时 postgres 退出并给出明确信息；重建容器复用已有密码。
- 从 0.8.0（用旧脚本装的）升级：pull + up 后数据保留、能登录。
- Compose v2.20.1 对主文件与单文件版本 `config` 通过；单文件版本不带 `.env` 展开后与主文件等价（路径占位替换后）。
- 主程序容器加固不回退（`scripts/check-production-compose.sh` 检查）。
- 发布流水线：没有 Docker Hub 配置时行为与现在一致；有配置时两处都推送同一摘要。

## 不做

- 各家 NAS 的真机验证（交给 qlqq 与用户）；群晖 DSM 虚拟机（许可只允许群晖硬件）。

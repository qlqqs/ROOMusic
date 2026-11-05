# 部署上线：局域网与公网同时可用

## 目标

把 ROOMusic 正式部署在本机（局域网地址 `192.168.50.206`），家里的设备用 `http://` 局域网地址直接访问，
外面的设备经外部反代（由用户在别处配置，本项目不提供反代配置）用 `https://` 域名访问；两种入口同时可以登录和使用。
部署后能自动拉起、每天备份、出问题能恢复。

## 已确认的事实（2026-11-05 检查）

- 功能闭环已具备（Core 1 与 M5 收口，见 [Core 1 路线](../../../docs/architecture/core-1-roadmap.md)）；缺的是部署层面。
- **同一实例现在不能同时服务 http 与 https**：
  - `backend/internal/platform/config/config.go:71` 生产模式强制 `ROOMUSIC_SECURE_COOKIES=true`，会话 Cookie 带 `Secure`，浏览器在 http 局域网地址上不保存它。
  - `backend/internal/identity/guard.go:103-110` 写请求的 `Origin` 协议只能是 http 或 https 之一，由同一个开关决定。
  - `backend/internal/identity/guard.go:111-122` 设置 `ROOMUSIC_PUBLIC_URL` 后只认一个主机名；不设置时与 `request.Host` 比较，外部反代可能改写 Host。
- 登录没有任何限速（`backend/internal/identity/httpapi/handlers.go:137` 起）。
- 配置文件：开发用 `.env.dev`（`scripts/dev.sh` 读取）；根目录 `.env` 原定给生产用，现在只有两行注释，`scripts/prod.sh`、`scripts/reset-password.sh`、`scripts/dev-reset.sh` 都读它；
  `.gitignore` 忽略 `.env` 与 `.env.*`，只放行 `.env.example`（开发模板）。
- `scripts/prod.sh` 用 `go run` 现场编译，没有应用镜像、自动拉起或备份。
- `compose.yaml` 里的 Redis 与 Meilisearch 后端代码没有使用。
- 本机开发库（`roomusic-postgres-1`）停在迁移 0012，只有 1 个账号、68 张专辑，生产库从零开始，不迁移。
- 音乐在 `/home/qlqq/workspace/ROOMusic/music`（7.6G，属主 `qlqq` uid 1000，权限 755）；8080、8090 端口空闲，生产用 8090（用户 2026-11-05 确认，8080 留给开发）。
- 本机 Docker 没有自定义地址池，已占用 `192.168.0.0/20`、`192.168.16.0/20`、`192.168.32.0/20`，
  下一个默认分配的 `192.168.48.0/20` 会覆盖局域网 `192.168.50.0/24`，**新 Compose 网络必须固定子网**。
- 应用二进制自带 `reset-password` 子命令；`scripts/ffmpeg-pinned.sh` 固定了 ffmpeg 镜像摘要；
  `deploy/smoke/current.Dockerfile` 是仅供 smoke 的镜像写法，可参考。

## 已定的决定

| 编号 | 决定 | 来源 |
| --- | --- | --- |
| D1 | 局域网走 http，公网由外部反代提供 https，项目内不配置反代或证书 | 用户 2026-11-05 |
| D2 | 用 Docker Compose 运行（应用镜像 + PostgreSQL） | 用户 2026-11-05 |
| D3 | 部署在本机，音乐用现有目录只读挂载 | 用户 2026-11-05 |
| D4 | 登录限速在上线前完成 | 用户 2026-11-05 |
| D5 | 不信任 `X-Forwarded-*`：来源与 Cookie 判断只看浏览器带来的 `Origin` 与配置的清单；限速按“用户名 + TCP 对端地址”计数，不读请求头，阈值见登录限速子任务 | 讨论结论；限速方式用户 2026-11-05 选定 |
| D6 | 生产库从零开始；生产与开发的 Compose 项目、数据卷、端口、网络都分开，互不影响 | 检查结论 |
| D7 | 同一个工作区放两套配置：`.env.dev`（开发）与 `.env.prod`（生产）；两套直接同时运行，不设开关、不互斥，开发用 `make dev`，生产用 `make prod` | 用户 2026-11-05（先后考虑过开关切换与“同时只跑一套”，最终定为直接部署两套） |

## 子任务

| 子任务 | 交付 | 顺序 |
| --- | --- | --- |
| [11-05-deploy-origins](../11-05-deploy-origins/prd.md) | 来源清单、会话 Cookie 按来源设置 | 先做 |
| [11-05-deploy-login-throttle](../11-05-deploy-login-throttle/prd.md) | 登录限速 | origins 之后，可与 packaging 并行 |
| [11-05-deploy-packaging](../11-05-deploy-packaging/prd.md) | 镜像、生产 Compose、备份与恢复、本机上线演练 | 配置项依赖 origins；上线演练在三项都完成后 |

收尾（三个子任务都通过后）：另派一个后端实现 agent 在最终提交的导出树上跑一次与 CI 等价的门禁（含全量 Vitest、Playwright、
`-race` 与 `./scripts/test-integration.sh`，各一次）；若与已测过的树字节相同则复用结果。然后写实施结果、归档子任务与父任务、修相对链接、记日志。

## 规划方式

- 需求由用户在 2026-11-05 的讨论中确定（D1–D4 与限速计数方式）；技术设计由 Opus 子 agent 起草，主会话审阅并按推荐裁决技术取舍（见各子任务 PRD 的“规划裁决”）。
- 规划裁决途径：用户批准最终规划摘要（2026-11-05 用户回复“开始执行”；之前确认 Redis、Meilisearch 因后端未使用而不进生产）。

## 跨子任务验收

- [ ] X1 在本机按部署说明从零部署演练实例：`docker compose up -d` 后初始化管理员、添加音乐目录、扫描、播放 FLAC 与 ALAC 都成功；
  演练后正式实例以空库运行，由用户自己初始化管理员。
- [ ] X2 同一实例上，`http://192.168.50.206:<端口>` 与一个 https 来源（演练时用本机临时 TLS 反代模拟外部反代）都能登录、写操作（如收藏）成功、退出。
- [ ] X3 不在来源清单里的来源写请求被拒绝；连续输错密码会被限速，提示一句话说清楚。
- [ ] X4 重启机器或 `docker compose restart` 后生产自动恢复；演练与正式启动全程不碰开发用的 `roomusic-postgres-1`，它与其数据卷不变。
- [ ] X5 有一份当天的备份，能按恢复说明恢复到一个临时库并核对数据。
- [ ] X6 README 的“开发环境”“生产环境”两部分按两套配置重写；不再有读根目录 `.env` 的脚本。

## 范围外

- 反代、证书、域名、防火墙或路由器配置；异地备份；监控告警；多实例。
- 三组手工实播（仍在 `10-06-m2a-acceptance-followup`）。
- Redis、Meilisearch 的生产部署。

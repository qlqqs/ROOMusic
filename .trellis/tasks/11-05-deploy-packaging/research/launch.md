# 正式启动记录（2026-11-05，UTC）

## 范围与边界

- 依据 packaging `implement.md` 第 5 步、`design.md` §10.2 第 1–7 步、origins `design.md` §9 及最新部署说明；主机一致模式已提交。
- 开始时 `main` / HEAD 为 `06450d0ce2d721e01e412f74ea3927b8d256ffe1`，工作区干净。
- 仅启动 Compose 项目 `roomusic-prod`；端口 8090，固定子网 `10.203.50.0/24`；音乐只读挂载。
- 只新增忽略的 `.env.prod` 和本任务 `research/launch.md` / `research/launch/` 证据，不修改部署、应用代码或任务指针，不提交 Git。
- 数据库密码由 `openssl rand -hex 24` 直接写入权限 600 的 `.env.prod`，不在终端、日志、记录中输出；不读取 `.env` / `.env.dev` 的值。
- 开发库仅重复演练第 0 步允许的只读检查，不停止、重启、删除或写入开发资源；不运行开发启动/重置命令。
- 不初始化管理员，不调用 `POST /api/v1/setup/admin`；仅做两次指定的 probe 登录冒烟，不保存请求体或密码。任何应用/部署步骤失败立即停止。
- 预检：生产容器/卷/网络及 `.env.prod` 尚不存在；8090 无监听；备份目录尚不存在。Docker 现有子网不与 `10.203.50.0/24` 重叠；本机 Playwright 环境与模块可用。

## 执行进度

以下逐步追加实际结果；未记录为通过的步骤不视为已完成。

## 第 1 步：创建正式配置，通过

实际执行：`mkdir -p /home/qlqq/roomusic-backups`；用 Python 以 `O_EXCL` / 600 新建 `.env.prod`，逐行复制 `.env.prod.example`，数据库密码行仅写键名后，将 `openssl rand -hex 24` 的标准输出直接接到该文件描述符；随后 `chmod 600 .env.prod`。
没有把随机值放进命令行、shell 回显、日志或文档，也没有 source 配置。

配置白名单核对：端口 8090，音乐目录 `/home/qlqq/workspace/ROOMusic/music`，备份目录 `/home/qlqq/roomusic-backups`，固定子网 `10.203.50.0/24`；时区 Asia/Shanghai，备份时刻 3，保留 14 天，缓存上限 10240 MiB。`ROOMUSIC_ALLOWED_ORIGINS` 没有有效赋值行，采用主机一致模式。`.env.prod` 权限为 600，`git check-ignore -q .env.prod` 通过，`git status --short --untracked-files=all -- .env.prod` 无输出。备份目录属主 uid 1000。

```bash
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod config --quiet
```

退出 0，证据：`launch/step-01-config.log`。未记录完整 Compose 配置。

## 第 2 步：开发库基线，通过

命令：`python3 research/launch/dev-check.py baseline`（仓库内完整路径见 `launch/step-02-dev-baseline.log`）。
只白名单提取 `POSTGRES_USER` / `POSTGRES_DB`；读取容器状态/启动时间、卷创建时间，执行三项既定只读聚合。未读取其他环境变量或配置。
基线采集于 `2026-11-05T16:54:35.769774+00:00`；状态 running，启动时间 `2026-10-26T03:13:45.497579973Z`，卷创建时间 `2026-09-01T09:54:51Z`，迁移最大版本 12、users 1、releases 68。结构化证据：`launch/dev-baseline.json`。

## 第 3 步：dry-run 命令检查，通过

```bash
scripts/production.sh --dry-run stop
scripts/production.sh --dry-run up
```

均退出 0。所有 Compose 命令为 `-p roomusic-prod`；stop 仅停，不含 down/-v；up 顺序为导出构建 → config --quiet → 启动数据库 → 升级前备份 → 整体启动 → ps。没有真正执行 dry-run 打印的命令。
证据：`launch/step-03-dry-stop.log`、`launch/step-03-dry-up.log`、`launch/step-03-check.log`。
首次自编内联断言漏算末尾的 ps，误期待总共 5 条 Compose 命令（实际连 stop 共 6 条），在只读检查阶段失败。按用户既有授权修正自编检查，`launch/dry-check.py` 按完整动作列表核对通过；未改部署或应用代码。

## 第 4 步：首次 make prod 与版本核对，通过

```bash
make prod
make prod-status
git rev-parse --short=12 main
```

从 main 的 `06450d0ce2d721e01e412f74ea3927b8d256ffe1` 导出构建；tools 与 app 镜像构建成功，app 构建上下文 14.56 MB，使用已提交的内嵌前端，没有现场 Node 构建。
启动顺序为 PostgreSQL healthy → 启动前备份成功 → app/backup 启动并 healthy。首份备份为 `roomusic-20261106-005546-manual.dump`，839 字节，权限 600，uid 1000。首次备份发生在应用迁移前的空数据库，因调用 `backup now`，文件同样带 `-manual`，不是完整初始化后的数据备份。

状态/版本核对：

| 服务 | 镜像 | 状态 |
| --- | --- | --- |
| app | `roomusic-prod-app:06450d0ce2d7` | running / healthy |
| postgres | `postgres:18.1-alpine3.23`（固定摘要） | running / healthy |
| backup | `roomusic-prod-tools:06450d0ce2d7` | running / healthy |

当前运行版本 `06450d0ce2d7` 与 `git rev-parse --short=12 main` 一致。三项服务均 `restart: unless-stopped`；只有 app 发布 `0.0.0.0:8090->8080/tcp`，数据库与 tools 的 `5432/tcp` 仅为镜像暴露信息，无宿主机端口发布。
正式命名卷为 `roomusic-prod_postgres-data`、`roomusic-prod_app-data`，网络 `roomusic-prod_roomusic` / `10.203.50.0/24`；音乐挂载 `RW=false`；容器内 `ROOMUSIC_ENV=production`、`ROOMUSIC_ALLOWED_ORIGINS` 为空。未读取完整容器环境。

证据：`launch/step-04-first-prod.log`、`launch/step-04-status.log`、`launch/step-04-verify.log`、`launch/prod-first.json`。

## 第 5 步：手动备份、再次启动，通过

```bash
make prod-backup
make prod
```

- 显式手动备份：`roomusic-20261106-005716-manual.dump`，169997 字节；日志 `backup.completed`，退出 0。
- 第二次启动明确打印“同一提交的镜像已存在，跳过构建。”，没有再次 docker build。
- 第二次启动仍先备份，生成 `roomusic-20261106-005718-manual.dump`，169997 字节，随后三项服务全部 healthy。
- 最终三份备份均在 `/home/qlqq/roomusic-backups`，非空、权限 600、属主 uid 1000。备份文件名采用配置的 Asia/Shanghai 时间，所以文件名日期为 **2026-11-06**，执行记录 UTC 日期仍为 **2026-11-05**。
- 启动前备份及显式手动备份均成功；本次没有执行恢复测试，也未声称每日计划时刻已经验收。

证据：`launch/step-05-manual-backup.log`、`launch/manual-backup.json`、`launch/step-05-second-prod.log`、`launch/second-start-backup.json`、`launch/prod-final.json`。

## 第 6 步：开发库前后核对，通过

实际执行与基线相同的允许范围只读检查：`python3 research/launch/dev-check.py after`（完整命令见日志）。
基线时间 `2026-11-05T16:54:35.769774+00:00`，复核时间 `2026-11-05T16:58:26.836235+00:00`。

| 项目 | 启动前 | 启动后 | 结果 |
| --- | --- | --- | --- |
| `roomusic-postgres-1` 状态 | running | running | 不变 |
| 启动时间 | `2026-10-26T03:13:45.497579973Z` | `2026-10-26T03:13:45.497579973Z` | 不变 |
| `roomusic_postgres-data` 创建时间 | `2026-09-01T09:54:51Z` | `2026-09-01T09:54:51Z` | 不变 |
| 迁移最大版本 | 12 | 12 | 不变 |
| users 行数 | 1 | 1 | 不变 |
| releases 行数 | 68 | 68 | 不变 |

证据：`launch/dev-baseline.json`、`launch/dev-after.json`、`launch/step-02-dev-baseline.log`、`launch/step-06-dev-after.log`。
没有停止、重启、删除或写入开发容器、数据卷和 Compose 项目。

## 第 7 步：初始化页面与首份备份，通过

```bash
source /home/qlqq/.local/share/playwright-deps/env.sh
node .trellis/tasks/11-05-deploy-packaging/research/launch/check-setup.cjs
```

无头 Chromium 实际打开 `http://192.168.50.206:8090`，返回 200，显示“创建管理员”。`GET /api/v1/setup/status` 返回 200，`setup_required=true`。
浏览器检查脚本只做 GET/HEAD，并对其他方法加拦截保护；没有输入用户名或密码、没有点击初始化按钮、没有发送任何写请求（拦截计数也是 0）。截图前遮罩密码框。
截图已人工查看：`launch/setup-uninitialized.png`；证据：`launch/step-07-setup.log`、`launch/setup-result.json`。
首份备份存在，文件名和大小见第 4 步。未初始化管理员，留给用户自行操作。**请先从局域网初始化管理员，再开放公网反代，避免未初始化窗口被他人抢先创建管理员。**

## 主机一致模式冒烟：通过（恰好两次登录 POST）

实际执行 `python3 .trellis/tasks/11-05-deploy-packaging/research/launch/smoke-origin.py`，目标 `http://192.168.50.206:8090/api/v1/auth/login`。
两次均为用户名 probe、内存随机密码，无 Cookie；密码不写命令行或记录，请求体不保存。HTTPConnection 不重试、不自动跳转，没有第三次登录请求。

| 用例 | Origin | Host | 状态 / 错误码 | request_id |
| --- | --- | --- | --- | --- |
| a：主机一致 | `http://192.168.50.206:8090` | `192.168.50.206:8090` | 401 / invalid_credentials | `launch-a-ee19bbb1528a45f7a42960d8aa5ddf66` |
| b：主机不一致 | `https://music.qlqqs.com:8888` | `192.168.50.206:8090` | 403 / origin_forbidden | `launch-b-3aa8247d00df4f7caee3684bc574d4f5` |

以对应请求 ID 白名单提取正式 app 日志，仅保留诊断字段：

```json
{"time": "2026-11-05T16:58:28.534695174Z", "level": "INFO", "request_id": "launch-a-ee19bbb1528a45f7a42960d8aa5ddf66", "source": "192.168.50.206", "event": "identity.login.failed"}
{"time": "2026-11-05T16:58:28.534839115Z", "level": "WARN", "event": "http.request.completed", "request_id": "launch-a-ee19bbb1528a45f7a42960d8aa5ddf66", "method": "POST", "route_template": "POST /api/v1/auth/login", "status": 401}
{"time": "2026-11-05T16:58:28.535997723Z", "level": "WARN", "event": "identity.origin.rejected", "request_id": "launch-b-3aa8247d00df4f7caee3684bc574d4f5", "mode": "same-host", "origin": "https://music.qlqqs.com:8888", "host": "192.168.50.206:8090"}
{"time": "2026-11-05T16:58:28.536042959Z", "level": "WARN", "event": "http.request.completed", "request_id": "launch-b-3aa8247d00df4f7caee3684bc574d4f5", "method": "POST", "route_template": "POST /api/v1/auth/login", "status": 403}
```

a 正确进入凭据核对；b 在主机一致来源守卫拒绝，日志明确 `mode=same-host`，没有进入登录核对。两次均没有 Set-Cookie，不创建会话；初始化状态检查仍为 `setup_required=true`。只改变进程内一次失败计数，不写账号或会话业务数据。
日志来自 `docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod logs --no-color --since <本次开始时间> app`，原始日志仅内存解析，不保存请求体、Cookie 或其他无关日志。
证据：`launch/step-08-origin-smoke.log`、`launch/smoke-result.json`。

## 最终状态与验证边界

正式实例保持运行；最后一次 `make prod-status` 仍为三项 healthy、版本 `06450d0ce2d7`，地址 `http://192.168.50.206:8090`；最后只读状态/备份核对见 `launch/final-status.log`、`launch/final-verify.log` 和 `launch/prod-final.json`。

实际验证命令包括 `config --quiet`、两次 dry-run、`make prod`（两次）、`make prod-status`、`make prod-backup`、开发库只读基线/核对、无头 Chromium 初始化页面检查、两次同主机/异主机登录冒烟及定向日志提取。
本轮不改应用/部署代码，不运行全量 Go、前端或集成测试；仅执行正式启动所需的最小相关验证。未运行重启、恢复或封面卷测试。没有提交 Git。

### 记录安全与局部质量检查

最终运行 `python3 .trellis/tasks/11-05-deploy-packaging/research/launch/final-check.py`：只在内存读取生产数据库密码，对本次记录、日志、脚本、截图扫描，不输出秘密或其散列；确认 `.env.prod` 权限 600、Git 忽略且没有有效来源清单赋值。
同时核对证据中的健康状态、三份备份、开发库对照、未初始化状态与恰好两次冒烟；新增 Python 脚本用内存 `compile` 检查，浏览器脚本执行 `node --check`，执行 `git diff --check`，核对部署和应用代码无 diff。
最终检查结果以 `launch/final-check.log` 为准。唯一遇到的问题是第 3 步自编检查漏算 ps，修正后通过；没有应用或部署故障。

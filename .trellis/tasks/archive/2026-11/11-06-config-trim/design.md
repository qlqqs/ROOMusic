# 生产配置精简：技术设计

- 父任务：[部署体验改进](../11-06-deploy-ux/prd.md)；需求：[prd.md](./prd.md)（R1–R8、A1–A6）
- 依据：[后端调研](../11-06-deploy-ux/research/backend-facts.md) §4、§9
- 衔接：[builtin-backup 设计](../11-06-builtin-backup/design.md)（app 内置备份、`/backups`）、[versioned-release 设计](../11-06-versioned-release/design.md)（发布工作流使用本任务的 compose 与示例配置）
- 日期：2026-11-06；起草：设计子 agent（未改任何产品代码，未碰生产实例与 `.env.prod`）

## 0. 三个子任务共用的名字

与 [builtin-backup 设计 §0](../11-06-builtin-backup/design.md) 的表相同，本任务涉及的部分：

| 名字 | 含义 |
| --- | --- |
| `deploy/production/compose.yaml` | 唯一的生产 compose；仓库里镜像标签默认值为占位符 `__RELEASE_VERSION__`，发布时由 release 工作流替换 |
| `deploy/production/example.env` | 示例配置，复制为部署目录里的 `.env`；同样含 `__RELEASE_VERSION__` |
| `ROOMUSIC_VERSION` | compose 插值变量：镜像标签（`X.Y.Z`，不带 `v`）；与 Docker 构建参数 `ROOMUSIC_VERSION` 同义 |
| `ROOMUSIC_BACKUP_DIR` | compose 层：宿主机绝对路径或卷名，默认命名卷 `backups`；容器内应用变量固定 `/backups` |
| 备份来源与文件名 | `roomusic-YYYYmmdd-HHMMSS[-manual\|-pre-migration-<N>\|-pre-restore].dump` |
| 演练项目 | `roomusic-drill`，子网 `10.203.51.0/24` |

## 1. 边界

本任务只改部署文件、文档、Makefile、脚本与 CI 校验，不改 Go 或前端代码。builtin-backup 先落地（app 内置备份、删除 tools 阶段与 backup 服务），
本任务再定稿 compose 并删除 `make prod*` 路径。两者之间不要运行 `make prod`（旧脚本会构建已删除的 `tools` 阶段）；生产实例保持运行，不受影响。

## 2. 新 compose（完整草稿：`deploy/production/compose.yaml`）

```yaml
# ROOMusic 生产部署：和 .env（由 example.env 复制）放在同一个目录，执行 docker compose up -d。
# 项目名默认 roomusic-server；本机生产实例在 .env 里用 COMPOSE_PROJECT_NAME 指定为 roomusic-prod。
name: roomusic-server

x-postgres-image: &postgres-image postgres:18.1-alpine3.23@sha256:aa6eb304ddb6dd26df23d05db4e5cb05af8951cda3e0dc57731b771e0ef4ab29
x-logging: &logging
  driver: json-file
  options: { max-size: "10m", max-file: "5" }

services:
  app:
    image: "ghcr.io/qlqqs/roomusic:${ROOMUSIC_VERSION:-__RELEASE_VERSION__}"
    environment:
      ROOMUSIC_ENV: production
      ROOMUSIC_HTTP_ADDR: ":8080"
      ROOMUSIC_DATABASE_URL: "postgres://roomusic:${ROOMUSIC_DB_PASSWORD:?请在 .env 里填写 ROOMUSIC_DB_PASSWORD（数据库密码）}@postgres:5432/roomusic?sslmode=disable"
      ROOMUSIC_ALLOWED_ORIGINS: "${ROOMUSIC_ALLOWED_ORIGINS:-}"
      ROOMUSIC_ALLOWED_LIBRARY_ROOTS: /music
      ROOMUSIC_DATA_DIR: /data
      ROOMUSIC_BACKUP_DIR: /backups
      ROOMUSIC_FFMPEG_PATH: /usr/local/bin/ffmpeg
      ROOMUSIC_TIMEZONE: "${ROOMUSIC_TIMEZONE:-Asia/Shanghai}"
      ROOMUSIC_BACKUP_HOUR: "${ROOMUSIC_BACKUP_HOUR:-3}"
      ROOMUSIC_BACKUP_KEEP_DAYS: "${ROOMUSIC_BACKUP_KEEP_DAYS:-14}"
      ROOMUSIC_MEDIA_CACHE_MAX_MIB: "${ROOMUSIC_MEDIA_CACHE_MAX_MIB:-10240}"
    ports:
      # 只发布 IPv4，避免公网 IPv6 绕过外部反代。
      - "0.0.0.0:${ROOMUSIC_HTTP_PORT:-8090}:8080"
    volumes:
      - type: bind
        source: "${ROOMUSIC_MUSIC_DIR:?请在 .env 里填写 ROOMUSIC_MUSIC_DIR（音乐目录的绝对路径）}"
        target: /music
        read_only: true
        bind: { create_host_path: false }
      - app-data:/data
      # 不填时是命名卷 backups；填宿主机绝对路径时改为挂载该目录。
      - "${ROOMUSIC_BACKUP_DIR:-backups}:/backups"
    read_only: true
    tmpfs: ["/tmp:size=64m,mode=1777"]
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    depends_on:
      postgres: { condition: service_healthy, restart: true }
    networks: [roomusic]
    restart: unless-stopped
    logging: *logging

  postgres:
    image: *postgres-image
    environment:
      POSTGRES_DB: roomusic
      POSTGRES_USER: roomusic
      POSTGRES_PASSWORD: "${ROOMUSIC_DB_PASSWORD:?请在 .env 里填写 ROOMUSIC_DB_PASSWORD（数据库密码）}"
    volumes: [postgres-data:/var/lib/postgresql]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U roomusic -d roomusic"]
      interval: 5s
      timeout: 5s
      retries: 12
    stop_grace_period: 60s
    networks: [roomusic]
    restart: unless-stopped
    logging: *logging
    # 数据库不发布端口。

networks:
  # 默认由 Docker 分配子网；需要固定子网时在同目录放 compose.override.yaml（见部署说明）。
  roomusic:
    driver: bridge

volumes:
  postgres-data:
  app-data:
  backups:
```

### 2.1 已在 design-probe 项目里核实的写法（Compose v2.40.3，Docker 29.1.3）

| 写法 | 结果 |
| --- | --- |
| `"${ROOMUSIC_BACKUP_DIR:-backups}:/backups"`，变量未设置 | 解析为 `type: volume, source: backups`，卷名 `<项目>_backups`；首次挂载复制镜像里 `/backups` 的属主与权限（镜像预建 `1000:1000 0700`），uid 1000 可写 |
| 同一行，变量为绝对路径 | 解析为 `type: bind`；未使用的命名卷 `backups` **不会被创建** |
| `${ROOMUSIC_DB_PASSWORD:?中文提示}` 缺失 | `docker compose config` 退出 1：`required variable ROOMUSIC_DB_PASSWORD is missing a value: 请在 .env 里填写 …` |
| `.env` 里的 `COMPOSE_PROJECT_NAME` 与顶层 `name:` | `COMPOSE_PROJECT_NAME` 优先（`-p` > `COMPOSE_PROJECT_NAME` > 顶层 `name` > 目录名） |
| 同目录 `compose.override.yaml` 只写 `networks.roomusic.ipam.config` | 自动合并，网络得到指定子网，其余不变 |

- 绑定目录的短语法默认 `create_host_path: true`：目录不存在时 Docker 以 root 身份创建，uid 1000 写不进去，备份失败并在页面提示“备份目录无法写入”。
  长语法可以关掉自动创建，但不能和“不填就用命名卷”写在同一行，所以部署说明要求**先建目录并 `chown 1000:1000`**。
- 值不是绝对路径也不像卷名（如 `./backups`）时 Compose 按相对 compose 文件目录的绑定处理；说明里只写“绝对路径”。

### 2.2 为什么顶层 `name: roomusic-server`

本机开发项目叫 `roomusic`，卷是 `roomusic_postgres-data`（`compose.yaml:1`、`:53`）。如果公开 compose 不写 `name:`，放进名为 `roomusic` 的目录就会得到同名项目并**挂上开发库**；
写 `name: roomusic` 则必然撞上。`roomusic-server` 与 `roomusic`、`roomusic-prod`、`roomusic-drill`、`roomusic-test` 都不同。本机生产实例在 `.env` 写
`COMPOSE_PROJECT_NAME=roomusic-prod` 沿用旧卷；部署目录也叫 `roomusic-prod`，即使 `.env` 漏写这一行也不会碰开发卷（会得到一个全新的 `roomusic-server` 项目并因 8090 端口被占而启动失败，不会改动任何数据）。

## 3. 示例配置（完整草稿：`deploy/production/example.env`）

```dotenv
# 复制为 .env，和 compose.yaml 放在同一个目录，再执行 chmod 600 .env。
# 必填：数据库密码，只用字母和数字，可以用 openssl rand -hex 24 生成。第一次启动后再改这里不会改数据库里的密码。
ROOMUSIC_DB_PASSWORD=
# 必填：音乐目录的绝对路径，只读挂载。
ROOMUSIC_MUSIC_DIR=
# 镜像版本。升级或降级时改这一行，再执行 docker compose up -d。
ROOMUSIC_VERSION=__RELEASE_VERSION__
# 可选：网页端口，默认 8090。
# ROOMUSIC_HTTP_PORT=8090
# 可选：把备份放到宿主机目录（绝对路径，需要 uid 1000 可写）；不填时放在 Docker 卷里。
# ROOMUSIC_BACKUP_DIR=/srv/roomusic-backups
# 可选：每天几点后自动备份（0–23），默认 3。
# ROOMUSIC_BACKUP_HOUR=3
# 可选：备份按哪个时区计时，默认 Asia/Shanghai。
# ROOMUSIC_TIMEZONE=Asia/Shanghai
# 可选：备份保留几天，默认 14。
# ROOMUSIC_BACKUP_KEEP_DAYS=14
# 可选：经反代访问且反代改写了 Host 时，列出允许的完整网址，用逗号分隔。
# ROOMUSIC_ALLOWED_ORIGINS=https://music.example.com
```

- 密码留空：没填就 `up` 时 compose 直接报中文提示，不会用示例里的假密码建库（旧示例 `ReplaceWithRandomHexBeforeDeploy` 有被直接用上的风险）。
- `ROOMUSIC_MEDIA_CACHE_MAX_MIB`、`COMPOSE_PROJECT_NAME` 不进示例，只写在部署说明的“更多设置”表里。

## 4. 固定子网（R3）

推荐 Compose 的惯用做法：同目录放 `compose.override.yaml`，`docker compose` 会自动合并，不需要 `-f`：

```yaml
# compose.override.yaml：Docker 默认地址池会撞上局域网时才需要。
networks:
  roomusic:
    ipam:
      config:
        - subnet: 10.203.50.0/24
```

备选是保留 `ROOMUSIC_SUBNET` 变量：Compose 不支持“变量为空就不写 ipam”，只能给一个默认子网，而默认子网本身就可能和别人的网络冲突，所以不采用。
已存在的网络改子网要先 `docker compose down`（不加 `-v`）再 `up`，说明里写明。

## 5. 仓库文件改动

| 文件 | 动作 | 说明 |
| --- | --- | --- |
| `deploy/production/compose.yaml` | 改写 | §2 草稿 |
| `deploy/production/example.env` | 新增 | §3 草稿 |
| `deploy/production/README.md` | 改写 | §7 大纲 |
| `deploy/production/backup.sh`、`restore.sh` | 删除 | builtin-backup R10 执行，本任务确认无残留 |
| `deploy/production/Dockerfile`、`Dockerfile.dockerignore` | 不由本任务改 | builtin-backup（PG 18 客户端、`/backups`、去 tools）与 versioned-release（多架构、ldflags） |
| `deploy/production/entrypoint.sh` | 不变 | |
| `.env.prod.example` | 删除 | 由 `deploy/production/example.env` 取代 |
| `.gitignore:5` | 删除 `!.env.prod.example` 一行 | `.env.*` 仍忽略，本机仓库根的 `.env.prod` 在过渡期继续被忽略 |
| `scripts/production.sh` | 删除 | 构建、启动路径整体去掉（R6） |
| `Makefile:1`、`:6-13` | 删除 `prod`、`prod-stop`、`prod-status`、`prod-logs`、`prod-backup`、`prod-reset-password` 及 `.PHONY` 里的名字 | `dev`、`dev-reset`、`backend-*`、`frontend-*` 不变 |
| `scripts/reset-password.sh:12` | 改提示 | “生产请在部署目录执行 docker compose exec app /app/roomusic reset-password <用户名>” |
| `scripts/check-production-compose.sh` | 新增 | CI 与本地共用的 compose 校验（§6） |
| `README.md`（根）第 39–143 行附近的生产段 | 改写为简短说明并链接部署说明 | |
| `.trellis/spec/backend/core0-runtime-contracts.md:42`、`:65-66`、`:139-151` | 改写为新部署方式、内置备份、发布镜像 | spec 更新在收尾阶段做 |
| `.trellis/spec/backend/identity-contracts.md:108` | 运维入口改为 `docker compose exec app /app/roomusic reset-password <用户名>` | |
| `.github/workflows/ci.yml:110-121` | 改 compose 校验与一致性 grep | §6 |
| `deploy/smoke/*` | 不受影响 | V0 对照冒烟自带 compose 与 Dockerfile，不引用生产文件（`git grep` 已确认） |
| `compose.yaml`、`compose.test.yaml`、`.env.example`、`scripts/dev.sh` | 本任务不改 | 开发环境不受影响（`dev.sh` 的 `ROOMUSIC_PG_BIN_DIR` 改动属于 builtin-backup） |

## 6. CI 改动（`ci.yml` integration job）

```yaml
      - name: Validate Compose configuration
        env:
          PG_PASSWORD: ci-placeholder
          MEILI_MASTER_KEY: ci-placeholder
        run: |
          docker compose config --quiet
          docker compose -f compose.test.yaml config --quiet
          scripts/check-production-compose.sh
          for script in deploy/production/*.sh; do bash -n "$script"; done
          grep -qF "$(scripts/ffmpeg-pinned.sh --image)" deploy/production/Dockerfile
          grep -qF "golang:$(sed -n 's/^go = "\(.*\)"/\1/p' .mise.toml)-" deploy/production/Dockerfile
          pg_major=$(sed -n 's/^x-postgres-image: &postgres-image postgres:\([0-9][0-9]*\)\..*/\1/p' deploy/production/compose.yaml)
          test -n "$pg_major"
          grep -qF "postgresql${pg_major}-client" deploy/production/Dockerfile
          grep -qF "PostgreSQL) ${pg_major}\\." deploy/production/Dockerfile
          grep -qF "$(sed -n 's/^x-postgres-image: &postgres-image //p' deploy/production/compose.yaml)" scripts/pg-client-pinned.sh
```

`scripts/check-production-compose.sh`（只用假值，`env -i PATH="$PATH" HOME="$HOME"` 隔离调用者的 `ROOMUSIC_*` / `COMPOSE_*`，每次显式 `--env-file` 指向临时文件，所以不会读到部署目录里的 `.env`）：

1. 只给 `ROOMUSIC_DB_PASSWORD`、`ROOMUSIC_MUSIC_DIR` 两项 → `config --quiet` 成功。
2. 缺密码 → 失败且 stderr 含 `ROOMUSIC_DB_PASSWORD`；缺音乐目录 → 失败且 stderr 含 `ROOMUSIC_MUSIC_DIR`（A2）。
3. `config --format json` 用 `jq` 只取非敏感字段断言：发布端口 `8090` 且 `host_ip` 为 `0.0.0.0`；`networks.roomusic.ipam` 不存在；`/backups` 挂载类型为 `volume`；
   设 `ROOMUSIC_BACKUP_DIR=/srv/x` 后为 `bind`；`services` 只有 `app`、`postgres`；项目名 `roomusic-server`；不含 `pull_policy`。
4. 加一个临时 `compose.override.yaml`（子网 `10.203.51.0/24`）后 `networks.roomusic.ipam.config[0].subnet` 为该值（A3 的静态部分）。
5. 仓库文件仍含 `__RELEASE_VERSION__`（防止有人把发布版本号提交回仓库）。

脚本**从不打印**完整的 `config` 输出（里面有密码）。

## 7. 部署说明大纲（`deploy/production/README.md`，简体中文）

1. **下载**：从 GitHub Release 下载 `compose.yaml` 与 `example.env` 到任意目录（如 `/srv/roomusic`），`cp example.env .env && chmod 600 .env`。
2. **填两项**：`ROOMUSIC_DB_PASSWORD`、`ROOMUSIC_MUSIC_DIR`；可选项表（端口、备份目录、备份时刻与时区、保留天数、来源清单、派生缓存上限、`COMPOSE_PROJECT_NAME`）。
   备份放宿主机目录时先 `mkdir -p <目录> && sudo chown 1000:1000 <目录>`。
3. **启动**：`docker compose up -d --wait`，打开 `http://<主机>:8090` 创建管理员（先初始化再开放公网反代），添加音乐目录 `/music`。
4. **升级 / 降级**：改 `.env` 的 `ROOMUSIC_VERSION` → `docker compose pull && docker compose up -d --wait`；每次结构变化前 app 自动做一份“版本切换前”备份；
   降级到不能自动回退的版本时 app 不会就绪，日志写明用哪份备份恢复（详见 versioned-release）。最低只能降到首个正式版 0.1.0。
5. **备份与恢复**：页面“系统 › 备份”；常用命令表：

| 操作 | 命令 |
| --- | --- |
| 状态 | `docker compose ps` |
| 日志 | `docker compose logs -f --tail 200 app` |
| 版本 | `docker compose exec app /app/roomusic --version` |
| 立即备份 | `docker compose exec app /app/roomusic backup` |
| 恢复 | `docker compose stop app`，`docker compose run --rm app restore <备份文件名>`，`docker compose up -d --wait` |
| 重置密码 | `docker compose exec app /app/roomusic reset-password <用户名>` |
| 停止（保留数据） | `docker compose stop` |

6. **固定子网**：§4 的 `compose.override.yaml`。
7. **反代与来源**：沿用现有 README 的 Host / Origin 说明（`deploy/production/README.md:55-71`），把“改 `.env.prod` 再 `make prod`”换成“改 `.env` 再 `docker compose up -d`”。
8. 醒目警告：不要对生产项目运行 `docker compose down -v`。

## 8. 本机生产实例过渡（R7）

**前提**：首个版本（0.1.0）已发布且匿名可拉取；§9 的演练已按原样走通；切换前用户确认。全部命令由用户执行，或经用户逐条同意后由主会话执行。
**不变**：Compose 项目名 `roomusic-prod`、卷 `roomusic-prod_postgres-data` / `roomusic-prod_app-data`、网络 `roomusic-prod_roomusic`（`10.203.50.0/24`）、端口 8090、
备份目录 `/home/qlqq/roomusic-backups`、音乐目录 `/home/qlqq/workspace/ROOMusic/music`。数据不迁移、不重建。
**部署目录**：`/home/qlqq/roomusic-prod/`（仓库外）。

### 8.1 切换步骤

> 2026-11-06 修订：C1 删除 `make prod*` 后，最终可执行步骤以 `deploy/production/README.md` §8 为准（从运行中镜像的 revision 提取历史 Compose）；下面是规划时的草稿，保留作依据。

1. 记录现状（不打印配置内容）：
   ```bash
   cd /home/qlqq/workspace/ROOMusic && make prod-status
   docker inspect -f '{{.Config.Image}}' $(docker ps -q --filter label=com.docker.compose.project=roomusic-prod --filter label=com.docker.compose.service=app) > /home/qlqq/roomusic-prod-old-image.txt
   docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod exec -T postgres psql -X -qAt -U roomusic -d roomusic \
     -c "SELECT 'users', count(*) FROM users UNION ALL SELECT 'release_groups', count(*) FROM release_groups UNION ALL SELECT 'tracks', count(*) FROM tracks UNION ALL SELECT 'favorite_tracks', count(*) FROM favorite_tracks UNION ALL SELECT 'favorite_albums', count(*) FROM favorite_albums UNION ALL SELECT 'max_migration', max(version) FROM schema_migrations" \
     > /home/qlqq/roomusic-prod-counts-before.txt
   ```
   （必须在切换前、用切换前的提交执行；`make prod-*` 只在旧提交里存在。）
2. 旧方式做一份手动备份：`make prod-backup`，确认 `/home/qlqq/roomusic-backups` 出现新的 `-manual.dump`。
3. 准备部署目录：
   ```bash
   mkdir -p /home/qlqq/roomusic-prod && cd /home/qlqq/roomusic-prod
   gh release download v0.1.0 -R qlqqs/ROOMusic -p compose.yaml -p example.env
   cp example.env .env && chmod 600 .env
   ```
4. 写 `.env`：把 `ROOMUSIC_DB_PASSWORD=` 与 `ROOMUSIC_MUSIC_DIR=` 两个空行删掉，然后：
   ```bash
   # 从旧配置复制，不在终端显示内容。
   grep -E '^(ROOMUSIC_DB_PASSWORD|ROOMUSIC_ALLOWED_ORIGINS|ROOMUSIC_TIMEZONE|ROOMUSIC_BACKUP_HOUR|ROOMUSIC_BACKUP_KEEP_DAYS|ROOMUSIC_MEDIA_CACHE_MAX_MIB)=' \
     /home/qlqq/workspace/ROOMusic/.env.prod >> .env
   printf '%s\n' 'COMPOSE_PROJECT_NAME=roomusic-prod' 'ROOMUSIC_MUSIC_DIR=/home/qlqq/workspace/ROOMusic/music' \
     'ROOMUSIC_BACKUP_DIR=/home/qlqq/roomusic-backups' >> .env
   ```
   端口用默认 8090，不写。
5. 固定子网：写 §4 的 `compose.override.yaml`（子网 `10.203.50.0/24`）。
6. 只看非敏感字段核对解析结果：
   ```bash
   docker compose config --quiet
   docker compose config --format json | jq '{name, network: .networks.roomusic.name, subnet: .networks.roomusic.ipam.config[0].subnet,
     volumes: [.volumes[].name], image: .services.app.image, ports: .services.app.ports, backups: (.services.app.volumes[] | select(.target=="/backups") | {type, source})}'
   ```
   期望：`name=roomusic-prod`、网络 `roomusic-prod_roomusic`、子网 `10.203.50.0/24`、卷含 `roomusic-prod_postgres-data` 与 `roomusic-prod_app-data`、
   镜像 `ghcr.io/qlqqs/roomusic:0.1.0`、端口 `0.0.0.0:8090`、`/backups` 为 bind `/home/qlqq/roomusic-backups`。
7. 先拉镜像（失败不影响运行中的实例）：`docker compose pull app`。
8. 切换：`docker compose up -d --wait --remove-orphans`。app 换成新镜像；旧 `backup` 容器作为孤儿被删除；postgres 配置等价，若被重建也只是短暂重启，卷不变。
9. 核对：
   - `docker compose ps` 两个服务 healthy；`docker compose exec app /app/roomusic --version` 为 0.1.0。
   - 用 `docker compose exec -T postgres psql …`（同第 1 步的 SQL）得到的计数与 `roomusic-prod-counts-before.txt` 一致。
   - `docker compose exec app /app/roomusic backup` 产出新的 `-manual.dump`；页面“系统 › 备份”列出旧文件与新文件，版本显示 0.1.0。
   - `docker network inspect roomusic-prod_roomusic` 子网仍是 `10.203.50.0/24`，网络创建时间未变（未重建）。
10. 观察一天：次日凌晨 3 点后出现新的定时备份。之后由用户决定何时删除仓库根的 `.env.prod`、旧镜像 `roomusic-prod-app:*` / `roomusic-prod-tools:*`。

> 0.1.0 不带新迁移时（预期如此：三个子任务都没有新建业务表，回退所需的两列由执行器加在 `schema_migrations` 上，见 versioned-release §3.3），
> 切换时不会做迁移前备份，旧版本程序仍能直接使用这个数据库。若 0.1.0 实际带了新迁移，第 8 步会先产出 `-pre-migration-30.dump`，回退就要走 8.2 的 B 路线。

### 8.2 回退步骤

- **第 7 步之前失败**：什么都没变，删掉 `/home/qlqq/roomusic-prod` 即可。
- **第 8 步之后要回到旧方式**：
  1. `cd /home/qlqq/roomusic-prod && docker compose stop app`（不要 `down -v`）。
  2. A 路线（库仍是迁移 30，正常情况）：直接启动旧应用与旧 backup 服务：
     ```bash
     cd /home/qlqq/workspace/ROOMusic
     old=<切换前的提交>
     git show "$old:deploy/production/compose.yaml" > /tmp/roomusic-old-compose.yaml
     img=$(cat /home/qlqq/roomusic-prod-old-image.txt)                  # 例如 roomusic-prod-app:<sha12>
     env -u COMPOSE_PROJECT_NAME ROOMUSIC_IMAGE="$img" ROOMUSIC_TOOLS_IMAGE="${img/-app:/-tools:}" \
       docker compose -p roomusic-prod -f /tmp/roomusic-old-compose.yaml --env-file .env.prod up -d --wait --remove-orphans
     ```
     旧镜像在本地仍存在（切换时没有删除）。`--remove-orphans` 会删掉新 compose 留下的、旧文件里不存在的资源引用（不会删卷）。
  3. B 路线（0.1.0 带了新迁移、库已经高于 30）：先用新镜像恢复迁移前备份，再走 A 路线：
     `docker compose run --rm app restore <roomusic-…-pre-migration-30.dump>`。
  4. 核对计数与第 1 步一致。旧方式期间产生的新数据以恢复点为准。

## 9. 演练（A1、A3、A4、A5）

**本机网络约束（硬性）**：本机任何实验、测试与演练都不得创建未固定子网的 Docker 网络。按 2026-11-06 的网络占用，Docker 自动地址池的下一个网段是 `192.168.48.0/20`，
会覆盖局域网 `192.168.50.0/24`。因此：

- 演练项目 `roomusic-drill` 在第一次 `up`（包括 `docker compose run`，它同样会建网络）之前就放好 `compose.override.yaml`，子网 `10.203.51.0/24`，并先用
  `docker compose config --format json | jq '.networks.roomusic.ipam'` 确认；A5 里用旧 compose 起的旧式演练栈同样通过 `ROOMUSIC_SUBNET=10.203.51.0/24` 固定。
- 临时容器（如 A4 查看卷内容的 `docker run --rm -v roomusic-drill_backups:/b alpine ls -ln /b`）只用默认 bridge，不指定新网络。
- “不指定子网时由 Docker 分配”只做静态核对（`check-production-compose.sh` 第 3 条），不在本机实际创建。

- 演练目录 `/tmp/roomusic-drill/`（仓库外），项目 `roomusic-drill`，端口 `18090`，音乐目录用合成曲库（`scripts/gen-playback-fixtures.sh` 产物的副本），
  备份目录 `/tmp/roomusic-drill/backups`（`chown 1000:1000`）。镜像为本地构建并打上 `ghcr.io/qlqqs/roomusic:0.1.0-drill-a` 标签（不推送），`.env` 写 `ROOMUSIC_VERSION=0.1.0-drill-a`。
- **A1**：空目录只放 compose、`.env`（两项必填 + 版本 + 项目名）和子网 override → `docker compose up -d --wait`，两个服务 healthy，`POST /api/v1/setup/admin` 建管理员成功。
- **A3**：`docker network inspect roomusic-drill_roomusic` 子网为 `10.203.51.0/24`；无 override 的情况由 `check-production-compose.sh` 静态核对。
- **A4**：不填 `ROOMUSIC_BACKUP_DIR` → `docker compose exec app /app/roomusic backup` 后 `docker run --rm -v roomusic-drill_backups:/b alpine ls -ln /b` 看到属主 1000 的文件；
  改填 `/tmp/roomusic-drill/backups` 后 `up -d` 再备份，文件出现在宿主机目录。
- **A5**（过渡步骤原样演练）：
  1. 用切换前提交构建旧镜像：`git archive <旧提交> | tar -x -C /tmp/roomusic-drill/old`，
     `docker build -f …/deploy/production/Dockerfile --target tools -t roomusic-drill-tools:old …`、`docker build -f … -t roomusic-drill-app:old …`；
     旧 compose + 演练用旧式 env（`ROOMUSIC_SUBNET=10.203.51.0/24`、`ROOMUSIC_HTTP_PORT=18090`、`ROOMUSIC_BACKUP_DIR=/tmp/roomusic-drill/backups`、
     `ROOMUSIC_IMAGE` / `ROOMUSIC_TOOLS_IMAGE`）以 `-p roomusic-drill` 启动；建管理员、扫描、收藏，旧 backup 服务产出旧命名备份。
  2. 在 `/tmp/roomusic-drill/new/` 按 §8.1 第 3–9 步原样执行（项目名 `roomusic-drill`、子网 `10.203.51.0/24`、版本 `0.1.0-drill-a`），断言卷与网络沿用、计数一致、旧备份列出、新备份产出。
  3. 按 §8.2 A 路线回退一次，再切回新方式一次，计数一致。
  4. 清理：`docker compose -p roomusic-drill down -v --remove-orphans`，删除 `roomusic-drill-*`、`ghcr.io/qlqqs/roomusic:0.1.0-drill-*` 本地镜像与 `/tmp/roomusic-drill`。
- **A6**：`git grep -nE 'make prod|production\.sh|\.env\.prod\.example|ROOMUSIC_SUBNET|ROOMUSIC_TOOLS_IMAGE'` 在归档任务与 `.trellis/workspace` 之外无结果；CI 改为 §6。

## 10. 取舍

| 问题 | 选择 | 放弃的方案 |
| --- | --- | --- |
| 部署配置文件 | 部署目录里的 `.env`（Compose 自动读取，命令都是普通 `docker compose …`） | `--env-file .env.prod`：每条命令都要带参数 |
| 子网 | `compose.override.yaml` | 可选变量：Compose 无法“空值就不写 ipam”；默认子网又可能冲突 |
| 备份目录 | 一行同时支持卷与绑定 | 两个服务定义或 profile：增加理解成本 |
| 项目名 | `name: roomusic-server` + 本机 `.env` 覆盖 | 不写 `name`：目录叫 `roomusic` 时会挂上开发卷 |
| 示例里的密码 | 留空，缺失时报错 | 假密码：可能被直接使用 |
| 运维入口 | 文档里的 `docker compose` 命令 | 保留 Makefile 包装：依赖仓库工作区（违背 R6） |

## 11. 运维与回滚

- 回滚本任务的仓库改动 = 回退提交；生产切换的回退见 §8.2。
- 本机仓库根的 `.env.prod` 在切换后仍保留，作为 §8.2 A 路线的配置；稳定后由用户删除。

## 12. 测试方案

| 验收 | 方法 | 命令 |
| --- | --- | --- |
| A1 | 演练 §9 A1 | 见 §9 |
| A2 | `check-production-compose.sh` 第 2 条（CI 每次运行） | `scripts/check-production-compose.sh` |
| A3 | §9 A3（带 override 的实际网络）+ 脚本第 3、4 条（静态） | |
| A4 | §9 A4 | |
| A5 | §9 A5 | |
| A6 | `git grep` + CI 新校验 | 见 §9 A6 |

## 13. 裁决结果（主会话 2026-11-06）

1. **采纳**顶层 `name: roomusic-server`；本机生产实例在 `.env` 里用 `COMPOSE_PROJECT_NAME=roomusic-prod`。
2. **采纳**示例配置 `deploy/production/example.env`，发布附件同名，部署时复制为 `.env`。
3. **采纳** A3 中“不指定子网时由 Docker 分配”改为静态核对；带子网的情况在 `roomusic-drill`（`10.203.51.0/24`）实测。本机不得创建未固定子网的网络（§9）。
4. **采纳** builtin-backup 与本任务在同一发布周期内落地，落地期间不运行 `make prod`。
5. **采纳**仓库根 `.env.prod` 与旧镜像 `roomusic-prod-app:*` / `roomusic-prod-tools:*` 在切换稳定后由用户自行删除。

## 14. 待核实

| 事项 | 核实方法 |
| --- | --- |
| 换用新 compose 文件后 `roomusic-prod_roomusic` 网络不被重建（ipam 相同） | §9 A5 演练里对比 `docker network inspect -f '{{.Created}}'` 前后值 |
| postgres 容器是否因 compose 标签变化被重建（不影响数据，只影响几秒可用性） | 同上，对比容器 ID |
| `docker compose up --remove-orphans` 删除旧 `backup` 容器后不留下需要手工清理的资源 | 演练后 `docker ps -a --filter label=com.docker.compose.project=roomusic-drill` |
| 本机 Docker 默认地址池的下一个网段 | 只读：`cat /etc/docker/daemon.json`（不创建网络） |

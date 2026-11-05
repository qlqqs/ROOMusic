# 设计：Docker Compose 部署打包与上线演练

需求见 [prd.md](./prd.md)，父任务决定 D2、D3、D6、D7 见[部署上线](../11-05-deploy-launch/prd.md)。
配置项依赖[来源清单](../11-05-deploy-origins/design.md)（`ROOMUSIC_ALLOWED_ORIGINS`）；上线演练（P9）在[登录限速](../11-05-deploy-login-throttle/design.md)完成后进行。
开发与生产两套配置同时运行（P11）集中写在第 16 节；为了不打乱其他文档对第 5、10、15 节的引用，它排在最后。

## 1. 本机核查结果（2026-11-05，只读命令）

| 项目 | 结果 | 对设计的影响 |
| --- | --- | --- |
| Docker | Server 29.1.3，Compose 2.40.3，`Firewall Backend: iptables`，没有 `/etc/docker/daemon.json`（userland-proxy 为默认开启），宿主机有 `docker-proxy` 进程；BuildKit 为默认构建器 | 见第 5 节来源地址；`Dockerfile.dockerignore` 由 BuildKit 读取 |
| 已占用子网 | `172.17–172.30` 各 /16、`172.31.251.0/24`、`172.31.252.0/24`、`192.168.0.0/20`、`192.168.16.0/20`、`192.168.32.0/20`；局域网 `192.168.50.0/24`；`ip route` 中没有 `10.0.0.0/8` 的路由 | 选 `10.203.50.0/24`（第 4.3 节） |
| 监听端口 | 8080、8090、18090、18443 都空闲；8080 是 `scripts/dev.sh` 的后端端口，Vite 也把 `/api` 代理到 `localhost:8080` | 生产用 8090（第 4.4 节，用户已确认） |
| IPv6 | 网卡有公网 IPv6（`240e:…/128`）；现有项目的发布端口同时监听 `0.0.0.0` 与 `[::]` | 端口只发布在 IPv4 上（第 5 节） |
| 时区 | 宿主机 `Etc/UTC` | 备份时刻按 `TZ` 显式配置 |
| 磁盘 | 根分区 293G，已用 216G，剩 65G；Docker 数据与家目录在同一分区 | 备份与数据库同盘，异地备份在范围外 |
| 开发库 | 容器 `roomusic-postgres-1`，卷 `roomusic_postgres-data`，网络 `roomusic_default`（172.18.0.0/16）；开发 Compose 还定义了 `redis`、`meilisearch`（`restart: unless-stopped`）；集成测试用项目 `roomusic-test` | 生产项目名 `roomusic-prod`，演练 `roomusic-drill`，都不复用这些名字；生产与演练的任何命令都不碰这些容器与卷 |
| 仓库 | 内嵌前端 `backend/cmd/roomusic/web` 已提交（124 个文件），CI 有漂移检查；`git archive main` 约 27 MB；`.gitattributes` 没有 `export-ignore`，没有子模块；根目录没有 `.dockerignore` | 从 `main` 导出即可构建，不需要 Node（第 3.1 节） |
| 根目录配置 | `.env` 现在只有注释；`.env.dev` 里有 `PG_*`、`MEILI_MASTER_KEY`、`ROOMUSIC_ENV` 等变量（只看了变量名）；`.mise.toml` 的 `env-up/env-down/env-logs/env-check` 直接 `docker compose`，靠 Compose 隐式读根目录 `.env` 插值 | 根目录 `.env` 不再被任何脚本读取（第 16.5 节）；mise 任务改为显式读 `.env.dev`（第 12 节） |
| Go 应用 | 只用 `os.Getenv` 读配置，不自己加载任何 `.env` 文件 | 根目录 `.env` 不会被应用读到 |

## 2. 文件布局

```text
deploy/production/
├── README.md                 # 部署说明（P4–P7，给不参与开发的人看）
├── Dockerfile                # 应用镜像（默认目标）与 tools 镜像（备份、恢复脚本）
├── Dockerfile.dockerignore   # 只放行构建需要的文件（BuildKit 读取与 Dockerfile 同名的忽略文件）
├── entrypoint.sh             # 等数据库可连后 exec 应用
├── compose.yaml              # 生产 Compose：app、postgres、backup，以及 tools 配置下的 restore-check
├── backup.sh                 # 备份服务脚本：loop / now / check 三种用法（打进 tools 镜像）
└── restore.sh                # 宿主机上运行：check（恢复到临时库核对）/ production（恢复到生产库）；容器内分支打进 tools 镜像
```

根目录与脚本：

| 文件 | 改动 |
| --- | --- |
| `.env.prod.example` | 新增，生产配置模板（第 9 节）；原设计的 `deploy/production/.env.example` 不再存在 |
| `.env.prod` | 不提交；部署时由模板复制，`chmod 600` |
| `.env` | 不改不删（用户的占位文件，只有注释）；不再被任何脚本读取（第 16.5 节） |
| `.gitignore` | `!.env.example` 后加 `!.env.prod.example` |
| `scripts/production.sh` | 新增，只服务生产：从提交导出构建镜像、备份后启动、停止、状态、日志、备份、重置密码（第 3.1、16.3 节）。不叫 `prod.sh`，避免与被删的旧脚本混淆 |
| `Makefile` | `prod` 改为 Docker 方式；新增 `prod-stop`、`prod-status`、`prod-logs`、`prod-backup`、`prod-reset-password`（第 16.4 节）；`dev`、`dev-reset` 不变 |
| `scripts/prod.sh` | 删除（origins 子任务先改它的 `SECURE_COOKIES` 检查，本任务再整体删除） |
| `scripts/dev.sh` | 只去掉“`ROOMUSIC_ENV_FILE` 等于 `.env` 时再 source `.env`”那段（第 12 节） |
| `scripts/dev-reset.sh` | 改读 `.env.dev`（第 12 节） |
| `scripts/reset-password.sh` | 默认读 `.env.dev`，只服务开发环境（第 12 节） |
| `.mise.toml` | `env-*` 任务显式 `--env-file .env.dev`（第 12 节） |
| `.env.example` | origins 子任务在里面加的“生产必填，见 …”指向改为 `.env.prod.example` |
| `scripts/ffmpeg-pinned.sh` | 注释里的同步清单加上 `deploy/production/Dockerfile` |
| `.github/workflows/ci.yml` | 第 11 节 |
| `README.md`、`.trellis/spec/backend/core0-runtime-contracts.md` | 第 12 节 |

演练用的 Caddyfile、演练 env 文件、记录与截图放在任务目录 `research/drill/` 下（记录要提交，env 文件与证书不提交），不进 `deploy/`。

## 3. 镜像（P1）

```dockerfile
# syntax=docker/dockerfile:1
# 构建上下文是从 Git 提交导出的源码树根目录（见 scripts/production.sh build-image）；
# Dockerfile.dockerignore 只放行 backend/ 与本目录的脚本。
FROM golang:1.25.10-alpine3.22@sha256:<实现时固定> AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN test -f cmd/roomusic/web/index.html \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/roomusic ./cmd/roomusic

FROM mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c AS ffmpeg

# 备份与恢复：与生产数据库同一个 PostgreSQL 镜像（pg_dump 版本一致），脚本随提交进镜像，不从工作区挂载。
FROM postgres:18.1-alpine3.23@sha256:<与 compose.yaml 的 x-postgres-image 相同> AS tools
COPY --chmod=0755 deploy/production/backup.sh deploy/production/restore.sh /opt/roomusic/

FROM alpine:3.22@sha256:<实现时固定> AS app
RUN apk add --no-cache ca-certificates postgresql17-client \
 && addgroup -S -g 1000 roomusic && adduser -S -D -H -u 1000 -G roomusic roomusic \
 && mkdir -p /app /data && chown roomusic:roomusic /data
COPY --from=build /out/roomusic /app/roomusic
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --chmod=0755 deploy/production/entrypoint.sh /app/entrypoint.sh
USER 1000:1000
WORKDIR /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/entrypoint.sh"]
```

要点：

- **Go 版本**与 `.mise.toml`（`go = "1.25.10"`）一致；基础镜像都用“标签 + 摘要”固定。实现时用 `docker buildx imagetools inspect <标签>` 取摘要，
  若 `1.25.10-alpine3.22` 标签不存在，选同一 Go 补丁版本的其他 Alpine 变体，Go 版本不能变。
- **ffmpeg** 用与 `scripts/ffmpeg-pinned.sh` 完全相同的镜像引用 `COPY --from`，应用通过 `ROOMUSIC_FFMPEG_PATH=/usr/local/bin/ffmpeg` 使用；
  启动自检照常进行。两处引用的一致性由 CI 检查（第 11 节）。
- **两个镜像，一个 Dockerfile**：默认目标（最后一个阶段 `app`）是应用；`--target tools` 是备份与恢复用的镜像。
  这样运行时不再从工作区挂载任何仓库文件，`backup.sh`、`restore.sh` 与应用一样只来自被部署的提交（P7）。
  `tools` 阶段的 PostgreSQL 镜像引用必须与 `compose.yaml` 的 `x-postgres-image` 完全相同，由 CI 比对。
- **前端**用已提交的内嵌产物，构建阶段只检查 `index.html` 存在，不需要 Node（C1）。
- **uid 1000**：与音乐目录属主一致，只读挂载能读；`/data` 预先 chown，命名卷第一次创建时继承这个属主。
- **健康检查**用 `/readyz`：数据库没连上时应用进入降级模式（`/healthz` 仍是 200），用 `/readyz` 才能在 `docker ps` 里看出不健康。
- **`postgresql17-client`** 只为入口脚本里的 `pg_isready`；它的协议与 PostgreSQL 18 兼容，Alpine 3.22 没有 18 的客户端包。若选用的 Alpine
  版本有 `postgresql18-client`，用 18。
- `Dockerfile.dockerignore` 用白名单：

  ```text
  *
  !backend/
  backend/data/
  !deploy/production/entrypoint.sh
  !deploy/production/backup.sh
  !deploy/production/restore.sh
  ```

  上下文是导出目录，本来就没有 `music/`、`node_modules/` 与未跟踪文件；白名单再把 `frontend/`、`docs/`、`.trellis/` 等挡在外面，
  上下文只有几 MB。BuildKit 按 `-f` 指向的 Dockerfile 路径找同名忽略文件，所以 `-f` 必须指向**导出目录里**的 Dockerfile（第 3.1 节）。

### 3.1 从提交导出构建（P7、C1、C6）

`scripts/production.sh build-image [--ref <提交>] [--prefix <镜像名前缀>]`，`make prod` 内部调用它：

1. `ref` 默认 `main`。`sha=$(git -C <仓库根> rev-parse --verify --quiet "<ref>^{commit}")`，失败则打印“找不到提交：<ref>”，退出码 2。
2. 打印“将部署：<12 位短 sha> <提交标题>”；若当前有正在运行的生产应用，同时打印“当前运行：<短 sha>”（取容器上的 revision 标签）。
   `ref` 不在 `main` 的历史上时多打印一句“注意：这个提交不在 main 上”。工作区有未提交改动或当前分支不是 `main` 时打印一句
   “工作区的未提交改动和当前分支不会进入镜像”，只提示不阻止。
3. 前缀默认 `roomusic-prod`。若 `<前缀>-app:<短 sha>` 与 `<前缀>-tools:<短 sha>` 都已存在且 `org.opencontainers.image.revision` 标签等于完整 sha，
   跳过构建，只把两个 `:current` 标签移到它们上（`docker tag`）。
4. 否则：`tmp=$(mktemp -d "${TMPDIR:-/tmp}/roomusic-build.XXXXXX")`（`trap` 删除）→
   `git -C <仓库根> archive --format=tar "$sha" | tar -x -C "$tmp"` → 检查 `$tmp/backend/cmd/roomusic/web/index.html` 与
   `$tmp/deploy/production/Dockerfile.dockerignore` 存在 → 依次：

   ```bash
   docker build -f "$tmp/deploy/production/Dockerfile" --target tools \
     --label org.opencontainers.image.revision="$sha" \
     -t "$prefix-tools:$short" -t "$prefix-tools:current" "$tmp"
   docker build -f "$tmp/deploy/production/Dockerfile" \
     --label org.opencontainers.image.revision="$sha" \
     -t "$prefix-app:$short" -t "$prefix-app:current" "$tmp"
   ```

   构建失败时 `:current` 不会移动，正在运行的实例不受影响。
5. 生产启动用的 `compose.yaml` 也取自同一个提交：`git -C <仓库根> show "$sha:deploy/production/compose.yaml" > "$tmp/compose.yaml"`（见第 16.4 节）。
   Compose 文件里没有相对路径（绑定挂载的源都来自 `.env.prod` 的绝对路径，没有 `build:` 段），放在临时目录里也能用。

- 只用 `git archive`：只包含提交里跟踪的文件，`music/`、`.env*`、`node_modules/`、未提交改动都不会进来。`.gitattributes` 没有 `export-ignore`；
  以后若加，要确认不会排除 `backend/` 或 `deploy/production/`。
- 内嵌前端 `backend/cmd/roomusic/web` 是已提交的（CI 有漂移检查），导出树里一定有；第 4 步的 `index.html` 检查兜底。
- 旧版本镜像（`roomusic-prod-app:<旧 sha>` 等）不会自动删除，回退时可以直接复用。部署说明给出查看与按标签删除的命令（`docker image ls roomusic-prod-app`、
  `docker image rm roomusic-prod-app:<旧 sha> roomusic-prod-tools:<旧 sha>`），不用 `-f`，不按镜像 ID 删。

### 入口脚本（为什么需要）

应用启动时连不上数据库会进入降级模式且**不再重连**（`cmd/roomusic/main.go` 的 `openDatabase` 只试一次，降级是 Core 0 合同的一部分）。
Compose 的 `depends_on: service_healthy` 只在 `docker compose up` 时生效；机器重启后由 Docker 守护进程按重启策略拉起容器，不保证顺序，
应用很可能比 PostgreSQL 先起来而永久降级，X4 就过不了。

`entrypoint.sh`：

```sh
#!/bin/sh
# 等 PostgreSQL 接受连接再启动应用：机器重启后容器没有先后顺序，应用只在启动时连一次数据库。
set -eu
waited=0
until pg_isready -q -d "$ROOMUSIC_DATABASE_URL" -t 3; do
  [ $((waited % 30)) -eq 0 ] && echo "{\"level\":\"INFO\",\"event\":\"platform.database.waiting\",\"module\":\"platform\",\"waited_seconds\":$waited}"
  sleep 2; waited=$((waited + 2))
done
exec /app/roomusic "$@"
```

- 一直等，不设上限：数据库起不来时容器保持 `starting/unhealthy`，`docker ps` 一眼能看出；数据库一好就自动继续。
- `pg_isready -d` 接受连接串，日志里不打印它（含密码）。
- `docker compose exec app /app/roomusic reset-password …` 直接执行二进制，不经过入口脚本。
- 不选“改后端为启动时重试数据库”：那会改变已写进合同的降级行为，而且是后端任务；部署层面的等待已经足够。

## 4. Compose（P2、P3）

### 4.1 草图

```yaml
name: roomusic-prod

x-postgres-image: &postgres-image postgres:18.1-alpine3.23@sha256:<实现时固定>
x-logging: &logging
  driver: json-file
  options: { max-size: "10m", max-file: "5" }

services:
  app:
    # 镜像由 scripts/production.sh build-image 从 Git 提交构建；这里不写 build:，也不从镜像仓库拉取。
    image: "${ROOMUSIC_IMAGE:-roomusic-prod-app:current}"
    pull_policy: never
    environment:
      ROOMUSIC_ENV: production
      ROOMUSIC_HTTP_ADDR: ":8080"
      ROOMUSIC_DATABASE_URL: "postgres://roomusic:${ROOMUSIC_DB_PASSWORD:?请在 .env.prod 中设置 ROOMUSIC_DB_PASSWORD}@postgres:5432/roomusic?sslmode=disable"
      ROOMUSIC_ALLOWED_ORIGINS: "${ROOMUSIC_ALLOWED_ORIGINS:?请在 .env.prod 中设置 ROOMUSIC_ALLOWED_ORIGINS}"
      ROOMUSIC_ALLOWED_LIBRARY_ROOTS: /music
      ROOMUSIC_DATA_DIR: /data
      ROOMUSIC_FFMPEG_PATH: /usr/local/bin/ffmpeg
      ROOMUSIC_MEDIA_CACHE_MAX_MIB: "${ROOMUSIC_MEDIA_CACHE_MAX_MIB:-10240}"
    ports:
      # 只发布在 IPv4 上：容器能看到局域网设备的真实地址，也不会经公网 IPv6 绕过外部反代。
      - "0.0.0.0:${ROOMUSIC_HTTP_PORT:?请在 .env.prod 中设置 ROOMUSIC_HTTP_PORT}:8080"
    volumes:
      - type: bind
        source: "${ROOMUSIC_MUSIC_DIR:?请在 .env.prod 中设置 ROOMUSIC_MUSIC_DIR}"
        target: /music
        read_only: true
        bind: { create_host_path: false }
      - app-data:/data
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
      POSTGRES_PASSWORD: "${ROOMUSIC_DB_PASSWORD:?请在 .env.prod 中设置 ROOMUSIC_DB_PASSWORD}"
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
    # 不发布端口

  backup:
    image: "${ROOMUSIC_TOOLS_IMAGE:-roomusic-prod-tools:current}"
    pull_policy: never
    user: "1000:1000"
    entrypoint: ["/bin/sh", "/opt/roomusic/backup.sh"]
    command: ["loop"]
    environment:
      PGHOST: postgres
      PGUSER: roomusic
      PGDATABASE: roomusic
      PGPASSWORD: "${ROOMUSIC_DB_PASSWORD:?请在 .env.prod 中设置 ROOMUSIC_DB_PASSWORD}"
      TZ: "${ROOMUSIC_TIMEZONE:-Asia/Shanghai}"
      ROOMUSIC_BACKUP_HOUR: "${ROOMUSIC_BACKUP_HOUR:-3}"
      ROOMUSIC_BACKUP_KEEP_DAYS: "${ROOMUSIC_BACKUP_KEEP_DAYS:-14}"
    volumes:
      - type: bind
        source: "${ROOMUSIC_BACKUP_DIR:?请在 .env.prod 中设置 ROOMUSIC_BACKUP_DIR}"
        target: /backups
        bind: { create_host_path: false }
    healthcheck:
      test: ["CMD", "/bin/sh", "/opt/roomusic/backup.sh", "check"]
      interval: 10m
      timeout: 10s
      start_period: 15m
      start_interval: 10s
    depends_on:
      postgres: { condition: service_healthy }
    networks: [roomusic]
    restart: unless-stopped
    logging: *logging

  restore-check:
    profiles: [tools]
    image: "${ROOMUSIC_TOOLS_IMAGE:-roomusic-prod-tools:current}"
    pull_policy: never
    user: postgres
    network_mode: none
    entrypoint: ["/bin/sh", "/opt/roomusic/restore.sh", "_inside"]
    tmpfs: ["/tmp:size=4g"]

networks:
  roomusic:
    driver: bridge
    ipam:
      config:
        - subnet: "${ROOMUSIC_SUBNET:?请在 .env.prod 中设置 ROOMUSIC_SUBNET}"

volumes:
  postgres-data:
  app-data:
```

（`restore-check` 如何挂入备份文件由第 7 节决定，这里只表明它是 `tools` 配置下、无网络的一次性服务。）

- `pull_policy: never`：本地没有对应镜像时立即报错，而不是去 Docker Hub 找一个不存在的 `roomusic-prod-app`。
- `start_interval: 10s`：启动期内每 10 秒检查一次备份健康，`make prod` 在启动前刚做过一次备份，`--wait` 很快就能看到 `healthy`，
  不必等满 10 分钟的常规间隔。
- `stop_grace_period: 60s`：`make prod-stop`（`docker compose … stop`）时给 PostgreSQL 足够的时间正常关闭。

### 4.2 命名与隔离（D6、X4、C3）

- 顶层 `name: roomusic-prod`，与开发的 `roomusic` 不同。卷不写死 `name:`，由 Compose 按项目名生成：`roomusic-prod_postgres-data`、
  `roomusic-prod_app-data`。镜像 `roomusic-prod-app:<短 sha>|current`、`roomusic-prod-tools:<短 sha>|current`。
  演练用 `-p roomusic-drill` 时卷、网络换成 `roomusic-drill_*`，镜像由演练 env 文件指向 `roomusic-drill-app`、`roomusic-drill-tools`，
  与生产、开发都不相交。
- **所有手工生产 Compose 命令都从仓库根目录执行，写成同一种形式**（下文记作 `<生产 compose>`）：

  ```bash
  docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod …
  ```

  - `--env-file` 按当前目录解析，所以要在仓库根目录执行；给了 `--env-file` 后 Compose 不再隐式读取根目录的 `.env`。
  - 日常操作用 `make prod-*`（第 16.4 节），部署说明里的手工命令只用于 `ps`、`logs`、`exec`、`stop` 一类；**启动与升级只用 `make prod`**，
    因为它用的是被部署提交里的 `compose.yaml`（第 3.1 节第 5 步），手工 `up` 会用工作区里的版本。
- Compose 插值时**宿主机 shell 里的同名环境变量优先于 env 文件**。`scripts/production.sh` 在调用生产 Compose 前清掉自身环境里所有
  `ROOMUSIC_*` 与 `COMPOSE_*` 变量，只设置它自己要传的 `ROOMUSIC_IMAGE`、`ROOMUSIC_TOOLS_IMAGE`，所以 `.env.prod` 是唯一来源。
  手工命令没有这层保护，部署说明写一句提醒；演练用 `--env-file` 指定独立文件，并在执行前用 `env | grep ^ROOMUSIC_` 确认 shell 里没有残留。

### 4.3 固定子网

- 生产：`10.203.50.0/24`；演练：`10.203.51.0/24`。
- 依据：`10.0.0.0/8` 不在 Docker 默认地址池（`172.17.0.0/16`–`172.31.0.0/16`、`192.168.0.0/16` 切 /20）里，以后别的项目自动分配的网络
  不会和它撞上；本机路由表没有 `10/8`；与局域网 `192.168.50.0/24` 和所有现有网络都不重叠。/24 足够（只有 3 个容器）。
- 子网做成必填变量而不是写死：演练与生产可能同时存在，两者必须不同。
- 顺带提醒用户（范围外，不在本任务改）：Docker 默认池的下一个 `192.168.48.0/20` 会覆盖局域网，其他项目新建网络时可能出事，
  建议日后在 `daemon.json` 配 `default-address-pools`。

### 4.4 应用端口

- 生产用 **8090**（用户 2026-11-05 确认）：两套同时运行，8080 是 `scripts/dev.sh` 后端的固定端口，Vite 代理也写死 `localhost:8080`，
  生产占用 8080 开发就起不来。其余开发端口（Vite 5173、开发库 `127.0.0.1:5432` 等）生产都不占用（第 16.2 节）。
- 外部反代也访问这个端口（P3）。
- 演练：`18090`（http）与 `18443`（临时 TLS 反代）。

### 4.5 音乐目录与数据卷

- 宿主机 `ROOMUSIC_MUSIC_DIR=/home/qlqq/workspace/ROOMusic/music` 只读挂到容器 `/music`，`ROOMUSIC_ALLOWED_LIBRARY_ROOTS=/music` 写死在 Compose 里。
  管理员在“添加目录”里填 `/music`（或它下面的子目录），不是宿主机路径；部署说明写清楚。
- `create_host_path: false`：路径写错时启动失败，而不是让 Docker 以 root 身份新建一个空目录。
- 音乐目录在开发工作区下面且被 Git 忽略：`git clean -x` 会连它和 `.env.prod` 一起删掉。部署说明在提醒 `.env.prod` 时一并写上（第 16.6 节）。
  以后若要把音乐挪走，只改 `.env.prod` 这一项。
- `app-data`（`/data`）放封面（从音乐目录里的图片复制而来，按内容寻址）与派生缓存；`postgres-data` 放数据库。两个都是命名卷。
- `app-data` 不在每日备份里（P5 只要求数据库）。派生缓存可以重建；封面能否靠重新扫描补回由演练顺带确认（见第 10 节步骤 12），
  结论写进部署说明。

## 5. 容器能否看到真实的局域网来源地址

**结论：IPv4 能，IPv6 不能，所以只在 IPv4 上发布端口。**

- 本机是默认配置（userland-proxy 开、iptables 后端）。这种配置下 Docker 为发布端口加 DNAT 规则：从网卡进来的 IPv4 连接被改写目标地址后
  直接路由到容器，**源地址保持不变**，容器看到的是手机、电脑的 `192.168.50.x`。
- `docker-proxy` 只处理 DNAT 规则覆盖不到的连接：宿主机自己连 `127.0.0.1:端口`，以及 IPv6 连接转到只有 IPv4 的容器网络。经过它的连接，
  容器看到的是网桥网关（如 `10.203.50.1`）。本机有公网 IPv6，若端口发布在 `[::]` 上，IPv6 访问不仅看不到真实地址，还会**绕过外部反代
  直接从公网进来**。因此端口写成 `0.0.0.0:<端口>:8080`。家里人用 `http://192.168.50.206:<端口>` 访问，走的是 IPv4。
- 外部反代：如果它在这台机器上并连 `127.0.0.1:<端口>`，容器看到网关地址；如果在别的机器上，看到那台机器的局域网地址；如果在本机连
  `192.168.50.206:<端口>`，看到本机地址。三种情况都是“一个固定的、与家里设备不同的地址”，正符合 T1“经反代的请求共用一个来源”。
  唯一要避免的是反代与家里人常用的某台设备是同一个 IP。
- 不能用 `sudo` 读 iptables（需要密码），以上是按 Docker 的已知行为推断，**必须在演练中实测**（第 10 节步骤 8）：
  1. 宿主机上的 Playwright 经 `http://192.168.50.206:18090` 输错一次，日志 `identity.login.failed` 的 `source` 记下（预期 `192.168.50.206`）；
  2. 经临时 TLS 反代（上游 `127.0.0.1:18090`）输错一次，`source` 预期是 `10.203.51.1`；
  3. **请用户用手机或另一台电脑**打开 `http://192.168.50.206:18090` 输错一次密码，`source` 必须是那台设备的 `192.168.50.x`
     （不是 `.206`、也不是 `10.203.51.1`）。这一步无法由 agent 单独完成。
- 若第 3 步看到的是网关地址（真实地址丢失），替代方案按推荐顺序：
  1. 应用改用 `network_mode: host`（容器直接用宿主机网络，看到的就是真实连接），PostgreSQL 发布到 `127.0.0.1:<端口>` 供应用连接，
     仍不对局域网开放；代价是偏离 P2 的“PostgreSQL 不发布端口”（只到回环地址）且应用直接监听宿主机端口。需要回到规划改 P2。
  2. 关掉守护进程的 userland-proxy（`daemon.json`，需要 root，影响本机所有项目），不推荐。

## 6. 备份（P5）

### 6.1 备份服务

- 用 tools 镜像（基于与数据库相同的 PostgreSQL 18 镜像，`pg_dump` 版本与服务器一致），以 uid 1000 运行，备份文件属主是 `qlqq`。
- 宿主机目录：生产 **`/home/qlqq/roomusic-backups`**（在仓库之外，`git clean` 碰不到；不需要 sudo）。部署前 `mkdir -p` 并确认属主是 1000。
- 格式：`pg_dump -Fc`（自带压缩，用 `pg_restore` 恢复，可单表恢复）。先写 `.roomusic-<时间>-<用法>-<pid>.partial`，用 `pg_restore --list` 确认能读，
  再改名为 `roomusic-YYYYmmdd-HHMMSS.dump`（手动备份加 `-manual` 后缀）。写失败不会留下看似完整的文件；临时名带用法与 pid，
  `loop` 与 `now` 同一秒运行也不会互相覆盖。

`backup.sh` 三种用法：

| 用法 | 行为 |
| --- | --- |
| `loop`（服务默认） | 启动时若 24 小时内没有成功备份，立即备份一次（首次部署当天就有备份）；之后每 10 分钟检查：本地时间已过 `ROOMUSIC_BACKUP_HOUR` 点且今天还没有成功备份，就备份。失败后下一次检查不早于 1 小时后重试 |
| `now` | 立即备份一次（`-manual`），成功退出 0，失败非 0；用于升级前、恢复前和手动备份 |
| `check` | 健康检查：最后一次成功在 26 小时内则 0，否则 1 |

- 成功后才清理：删除修改时间超过 `ROOMUSIC_BACKUP_KEEP_DAYS`（14）天的 `roomusic-*.dump`。备份连续失败时不会删旧文件。
- 成功写 `/backups/.last-success`（时间戳文件），`check` 看它的修改时间。
- 日志：每次一行 JSON 写到标准输出（成功 `backup.completed`，含文件名与字节数）或标准错误（失败 `backup.failed`，含 `pg_dump` 的退出码，
  不含密码与连接串）。**失败可见**的三个出口：`make prod-logs`（或 `<生产 compose> logs backup`）、`make prod-status` 里 backup 变成 `unhealthy`、备份目录没有新文件。
- 每次 `make prod` 启动前先做一次手动备份；`loop` 启动时发现 24 小时内已有成功备份就不重复。
- 时区：备份容器用 `TZ`（默认 `Asia/Shanghai`），默认每天 3 点后。实现时先确认镜像里有时区数据
  （`docker run --rm -e TZ=Asia/Shanghai <镜像> date` 显示 `CST`）；没有的话改用 POSIX 写法 `CST-8`。

### 6.2 手动立即备份

```bash
make prod-backup            # 等价于下一行
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod exec backup /bin/sh /opt/roomusic/backup.sh now
ls -l /home/qlqq/roomusic-backups
```

## 7. 恢复（P6）

`deploy/production/restore.sh` 在宿主机上运行（从仓库根目录执行），两种用法。脚本内调用 Compose 时：项目名取 `COMPOSE_PROJECT_NAME`，
未设置则 `roomusic-prod`；env 文件取 `COMPOSE_ENV_FILES`，未设置则 `.env.prod`；Compose 文件固定 `deploy/production/compose.yaml`。
脚本先把默认值写进这两个变量再调用 `docker compose`，不在命令行上另加 `-p`、`--env-file`，所以演练可以用这两个变量指向
`roomusic-drill` 与演练 env 文件，确认词也随之变成实际的项目名。容器内分支（`_inside`）用的是 tools 镜像里同一提交的 `restore.sh`。

### 7.1 恢复到临时库核对：`deploy/production/restore.sh check <备份文件>`

1. 用 `docker compose --profile tools run --rm` 启动 `restore-check`：无网络、数据目录在 tmpfs、备份文件只读挂入。
2. 容器内：`initdb`（信任本地连接）→ `pg_ctl start`（`listen_addresses=''`，只走 Unix socket）→ `createdb roomusic` →
   `pg_restore --no-owner --exit-on-error` → 打印核对表 → `pg_ctl stop`。容器退出即删除，不留任何数据。
3. 核对表：`schema_migrations` 最大版本与条数，`users`、`sessions`、`library_roots`、`releases`、`tracks`、`release_artworks` 的行数。
4. 与生产当前值比较：生产在运行时，脚本同时用 `docker compose exec -T postgres psql -At` 读一遍生产的同一组数，并排打印；
   生产没在运行时只打印备份这一列，并说一句“生产没有在运行，只显示备份里的数”。
   备份之后才发生的变化会让数字略有不同，部署说明写清怎么看。

### 7.2 恢复到生产库：`deploy/production/restore.sh production <备份文件>`

前提：生产的 postgres 容器在运行（应用可以是起不来的状态，例如回退后旧版本拒绝启动）；postgres 没运行时脚本提示“先 `make prod`”并退出。

顺序：

1. 显示备份文件名、时间、大小；要求输入项目名 `roomusic-prod` 确认，否则退出。
2. 立即做一次手动备份（恢复前的现状），失败则中止。
3. `docker compose stop app backup`（停应用与备份，数据库继续运行）。
4. `docker compose exec -T postgres psql -U roomusic -d postgres -c 'DROP DATABASE roomusic WITH (FORCE)' -c 'CREATE DATABASE roomusic OWNER roomusic'`。
5. `docker compose exec -T postgres pg_restore -U roomusic -d roomusic --no-owner --exit-on-error < <备份文件>`。
6. `docker compose start backup app`；应用启动时执行迁移（备份比代码旧时会补上新迁移；备份比代码新时应用拒绝启动，脚本提示先 `make prod REF=<对应提交>`）。
7. 等 `app` 变为 healthy，打印核对表。
8. 第 5 步失败：脚本不启动应用，打印“用第 2 步的备份再恢复一次”的完整命令。

## 8. 运维命令（P7）

### 8.1 构建来源：不另建生产检出目录

- 生产只有一份工作区：开发工作区 `/home/qlqq/workspace/ROOMusic`。生产配置 `.env.prod` 放在它的根目录，生产镜像与启动用的 `compose.yaml`
  都从 `main`（或回退时指定的提交）用 `git archive` / `git show` 取出（第 3.1 节），**不读工作区里的源码与部署文件**。
  所以开发中的未提交改动、别的分支、`music/`、`.env.dev`、`node_modules/` 都不会进入生产（P7）。
- 运行时生产只依赖工作区里的三样东西：`.env.prod`（启动时读）、音乐目录（只读挂载）、`deploy/production/restore.sh`（手动恢复时运行）。
- 升级流程：在开发工作区把改动合进 `main` → `make prod`。不需要 `git fetch`、`git pull` 或切分支。

### 8.2 首次部署

来源配置以 [origins design 第 9 节](../11-05-deploy-origins/design.md#9-主机一致模式d82026-11-05-增补) 为准：默认不填清单，反代保留 Host 与 Origin；启动后先初始化管理员，再开放公网反代。

```bash
cd /home/qlqq/workspace/ROOMusic
mkdir -p /home/qlqq/roomusic-backups
cp .env.prod.example .env.prod && chmod 600 .env.prod
# 编辑 .env.prod：数据库密码（openssl rand -hex 24 生成）、端口等；不填来源清单（主机一致模式）
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod config --quiet
make prod
make prod-status
```

然后打开 `http://192.168.50.206:8090`，按页面提示创建管理员，在“音乐目录”里添加 `/music` 并扫描。开发环境不受影响，照常 `make dev`。

### 8.3 升级与回退

```bash
cd /home/qlqq/workspace/ROOMusic
git log --oneline <当前运行的短 sha>..main   # 看看这次有哪些改动；当前运行的版本见 make prod-status
make prod
```

`make prod` 依次：从 `main` 构建镜像（同一提交已构建过则跳过）→ 启动数据库 → 升级前手动备份（失败则中止，不换版本）→
用新镜像启动并等到健康（第 16.4 节）。

回退：`make prod REF=<提交>`。若新版本已执行了新迁移，旧版本会拒绝启动（迁移执行器对未知版本 fail closed），
这时 `make prod REF=<旧提交>` 会在等待健康时超时报错（数据库已在运行、升级前备份已做），再用升级前的手动备份按 7.2 恢复。部署说明写清这一点。

### 8.4 其他

| 用途 | 命令（仓库根目录） | 对应的手工命令 |
| --- | --- | --- |
| 看状态 | `make prod-status`（容器状态与正在运行的版本） | `<生产 compose> ps` |
| 看日志 | `make prod-logs` | `<生产 compose> logs -f --tail 200 app`（或 `backup`） |
| 忘记密码 | `make prod-reset-password U=<用户名>`（终端里输两次，不回显） | `<生产 compose> exec app /app/roomusic reset-password <用户名>` |
| 手动备份 | `make prod-backup` | 见 6.2 |
| 重启 | `make prod` | `<生产 compose> restart` |
| 停止 | `make prod-stop`（只停，容器与数据都保留；`make prod` 继续） | `<生产 compose> stop` |
| 改配置后生效 | `make prod`（镜像已存在会跳过构建） | — |
| 加公网域名 | 默认不填来源清单，反代保留浏览器的 Host（含端口）与 Origin 即可直接用；主动使用清单时再追加公网来源并 `make prod` | — |

部署说明用醒目的一句话提醒：**不要**运行 `docker compose … down -v`，它会删掉数据库卷；`make prod*` 的任何命令都不会删卷。
重置密码后若登录仍提示次数太多，等一分钟再试（见限速设计第 6 节）。

## 9. `.env.prod.example`（P4）

放在仓库根目录，与开发模板 `.env.example` 并列；`.gitignore` 用 `!.env.prod.example` 放行。

| 变量 | 必填 | 说明（模板里每项一句） |
| --- | --- | --- |
| `ROOMUSIC_DB_PASSWORD` | 是 | 数据库密码，只用字母和数字（会放进连接地址），可用 `openssl rand -hex 24` 生成；首次启动后再改不会改数据库里的密码 |
| `ROOMUSIC_ALLOWED_ORIGINS` | 否 | 默认不填，走主机一致模式，反代须保留 Host（含端口）与 Origin；只想放行固定网址或反代无法保留 Host 时再填清单 |
| `ROOMUSIC_HTTP_PORT` | 是 | 局域网访问端口，填 8090 |
| `ROOMUSIC_MUSIC_DIR` | 是 | 宿主机上的音乐目录，容器里只读挂成 `/music` |
| `ROOMUSIC_BACKUP_DIR` | 是 | 宿主机上的备份目录，需先创建 |
| `ROOMUSIC_SUBNET` | 是 | 容器网络的固定子网，填 `10.203.50.0/24`，不要和家里的网络重叠 |
| `ROOMUSIC_TIMEZONE` | 否 | 备份时间按哪个时区算，默认 `Asia/Shanghai` |
| `ROOMUSIC_BACKUP_HOUR` | 否 | 每天几点后备份，默认 3 |
| `ROOMUSIC_BACKUP_KEEP_DAYS` | 否 | 保留几天的备份，默认 14 |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | 否 | ALAC 转换缓存上限（MiB），默认 10240 |

- 模板里的值要能让 `docker compose -f deploy/production/compose.yaml --env-file .env.prod.example config --quiet` 通过（密码用占位串）。
- 模板不写 `ROOMUSIC_ENV`（Compose 里固定为 `production`）、`ROOMUSIC_IMAGE`、`ROOMUSIC_TOOLS_IMAGE`（由 `make prod` 传入；演练 env 文件自己写）。
- `make prod` 启动前检查 `.env.prod` 存在（不存在时提示“先从 .env.prod.example 复制”）、权限不宽于 600（否则只提示一句）、
  `ROOMUSIC_MUSIC_DIR` 与 `ROOMUSIC_BACKUP_DIR` 两个目录存在。这两项逐行解析读出（只认 `KEY=值` 行，不 `source`）。

## 10. 上线演练（P9）与正式启动（P10）

由测试 agent 执行，主会话验收；需要用户参与的只有步骤 8.3（手机输错一次密码）。记录（命令、结果、关键截图）写入
`.trellis/tasks/11-05-deploy-packaging/research/drill/`（C2）。

### 10.1 演练

演练不用 `make prod*`，直接用 `-p roomusic-drill` 的 Compose 命令，全程不碰用户的开发库（C3）。

| 项 | 演练取值 |
| --- | --- |
| 执行目录 | 仓库根目录 `/home/qlqq/workspace/ROOMusic`（处于 `main`，记录 `git status --short` 与 `git rev-parse main`） |
| 项目名 | `roomusic-drill` |
| env 文件 | `/tmp/roomusic-drill/drill.env`：清单 `http://192.168.50.206:18090,https://localhost:18443`，`ROOMUSIC_HTTP_PORT=18090`，`ROOMUSIC_SUBNET=10.203.51.0/24`，`ROOMUSIC_BACKUP_DIR=/tmp/roomusic-drill/backups`，`ROOMUSIC_IMAGE=roomusic-drill-app:current`，`ROOMUSIC_TOOLS_IMAGE=roomusic-drill-tools:current` |
| 端口 | 应用 `18090`；临时 TLS 反代 `127.0.0.1:18443` |
| 卷 / 镜像 | `roomusic-drill_postgres-data`、`roomusic-drill_app-data` / `roomusic-drill-app:*`、`roomusic-drill-tools:*` |

下文 `$D` 指 `docker compose -p roomusic-drill -f deploy/production/compose.yaml --env-file /tmp/roomusic-drill/drill.env`（仓库根目录执行）。

步骤：

0. **开发库基线**：记录 `docker inspect -f '{{.State.Status}} {{.State.StartedAt}}' roomusic-postgres-1`、
   `docker volume inspect -f '{{.CreatedAt}}' roomusic_postgres-data`，以及
   `docker exec roomusic-postgres-1 psql -U <开发库用户> -d <开发库名> -Atc 'select max(version) from schema_migrations; select count(*) from users; select count(*) from releases'`
   （用户与库名从 `docker inspect` 的 `POSTGRES_USER`/`POSTGRES_DB` 取，只 grep 这两项，不打印密码）。预期 12、1、68。
   确认 `env | grep ^ROOMUSIC_` 为空。
1. **构建与启动**：`scripts/production.sh build-image --prefix roomusic-drill`（从 `main` 导出构建）；记录打印的提交、构建时间、上下文大小
   （BuildKit 输出的 `transferring context`，应只有几 MB）与镜像大小；`docker image inspect roomusic-drill-app:current` 的
   `org.opencontainers.image.revision` 等于 `git rev-parse main`；`$D config --quiet`；`$D up -d --wait`；`$D ps` 三个服务 healthy。
1b. **构建只取 `main`（C6）**：在临时 clone 里制造“别的分支 + 未提交改动”，不动用户工作区：
    ```bash
    git clone -q /home/qlqq/workspace/ROOMusic /tmp/roomusic-drill/clone && cd /tmp/roomusic-drill/clone
    git switch -q -c drill-side
    echo 'LABEL drill.marker=branch' >> deploy/production/Dockerfile && git commit -qam 'drill: side branch'
    echo 'LABEL drill.marker=dirty'  >> deploy/production/Dockerfile          # 不提交
    scripts/production.sh build-image --prefix roomusic-drill-c6
    docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "drill.marker"}}' roomusic-drill-c6-app:current
    ```
    预期：revision 等于用户仓库 `git rev-parse main`，`drill.marker` 为空。再 `--ref drill-side` 构建一次，预期 revision 是 side 分支的 sha、
    `drill.marker=branch`（证明 `--ref` 生效且仍不含未提交改动）；`--ref no-such-ref` 退出码 2 并提示“找不到提交”。
    结束后回到仓库根目录，`docker image rm` 按标签删除 `roomusic-drill-c6-app:*`、`roomusic-drill-c6-tools:*`（不用 `-f`），`rm -rf /tmp/roomusic-drill/clone`。
2. 镜像检查：`docker run --rm --entrypoint /usr/local/bin/ffmpeg roomusic-drill-app:current -hide_banner -version` 首行是 `ffmpeg version 7.1.1`；
   `$D exec app id` 是 uid 1000；app 日志里派生自检通过；`docker run --rm --entrypoint ls roomusic-drill-tools:current /opt/roomusic` 有两个脚本。
3. 用 Playwright（无头 Chromium，按 `/home/qlqq/workspace/AGENTS.md` 加载环境）打开 `http://192.168.50.206:18090`，初始化管理员。
4. 添加目录 `/music`，扫描完成；记录专辑数。
5. 播放一首 FLAC 与一首 ALAC（ALAC 走派生），各播放到能确认在走进度；截图。
6. 启动临时 TLS 反代：
   ```bash
   docker run -d --name roomusic-drill-tls --network host \
     -v /tmp/roomusic-drill/Caddyfile:/etc/caddy/Caddyfile:ro caddy:2-alpine
   ```
   Caddyfile：
   ```text
   {
     admin off
     auto_https disable_redirects
     skip_install_trust
   }
   https://localhost:18443 {
     bind 127.0.0.1
     tls internal
     reverse_proxy 127.0.0.1:18090
   }
   ```
   Playwright 用 `ignoreHTTPSErrors: true`。Caddy 会带 `X-Forwarded-*` 且 Host 是 `localhost:18443`，正好验证后端不看它们。
7. **两种来源**：分别经 `http://192.168.50.206:18090` 与 `https://localhost:18443` 登录、收藏一张专辑、退出；用 Playwright 读 Cookie 确认
   http 的 `roomusic_session` 不带 Secure、https 的带。再用 `curl` 带 `Origin: http://evil.example` 发一次写请求，得到 `403 origin_forbidden`（X2、X3）。
8. **限速与来源地址**：
   1. 经 http 入口用同一用户名输错 5 次，第 6 次页面显示“登录失败次数太多，请过几分钟再试。”；日志 `identity.login.throttled` 的 `source`；
   2. 同时经 https 入口用同一用户名和正确密码能登录（来源不同，B1b）；记录两个 `source`；
   3. 请用户用手机打开 `http://192.168.50.206:18090` 输错一次，记录 `source`，判断标准见第 5 节。
9. **重启恢复**：`$D restart`，全部恢复 healthy、已登录的页面刷新后仍可用；再单独 `$D restart postgres`，应用经
   `depends_on.restart` 跟着重启并恢复。另做一次“乱序”模拟：`$D stop` → 用 `docker start roomusic-drill-app-1` 先起应用（绕过 Compose 的依赖顺序），
   再 `docker start roomusic-drill-postgres-1`，应用日志出现
   `platform.database.waiting` 后正常就绪（模拟开机时的顺序问题；不实际重启机器）。
10. **备份**：`$D exec backup /bin/sh /opt/roomusic/backup.sh now` 得到文件；`backup` 服务的 `check` 为 0；日志有 `backup.completed`。再把 `PGPASSWORD` 改错跑一次
    （`$D run --rm -e PGPASSWORD=wrong backup now`），确认失败日志与非零退出码、没有留下 `.dump` 文件。
11. **恢复核对**：`COMPOSE_PROJECT_NAME=roomusic-drill COMPOSE_ENV_FILES=/tmp/roomusic-drill/drill.env deploy/production/restore.sh check <备份>`，
    核对表与演练库一致。再用同样的两个变量演练一次 `restore.sh production <备份>`（对演练项目，确认词是 `roomusic-drill`），确认顺序与结果。
12. **封面卷**：`$D stop` 后删掉演练的 `roomusic-drill_app-data` 卷，`$D up -d --wait` 并重新扫描，看封面是否回来，把结论写进部署说明（第 4.5 节）。
13. **清理**：确认命令里是 `-p roomusic-drill` 后执行 `$D down -v`；`docker image rm roomusic-drill-app:current roomusic-drill-app:<短 sha> roomusic-drill-tools:current roomusic-drill-tools:<短 sha>`
    （按标签删除，不用 `-f`，不碰 `roomusic-prod-*`）；`docker rm -f roomusic-drill-tls`；`rm -rf /tmp/roomusic-drill`；
    确认 `docker volume ls`、`docker network ls`、`docker ps -a`、`docker image ls` 里没有 `roomusic-drill`、`roomusic-drill-c6` 与 `roomusic-drill-tls`（C3）。
14. **开发库核对**：重复步骤 0，状态仍是 running、启动时间、卷创建时间、三项计数都不变（X4、C3）。

### 10.2 正式启动（P10）

全程不碰开发环境：不运行 `make dev`、`make dev-reset`，不对 `roomusic-postgres-1` 执行任何停止、重启或写操作。

1. 在仓库根目录按 8.2 创建 `.env.prod`（`ROOMUSIC_HTTP_PORT=8090`，不填来源清单（主机一致模式），密码随机生成且不打印，`chmod 600`），
   `mkdir -p /home/qlqq/roomusic-backups`；`<生产 compose> config --quiet`。
2. **开发库基线**：重复 10.1 步骤 0（状态、启动时间、卷创建时间、三项计数）。
3. **命令检查（只打印，不执行）**：`scripts/production.sh --dry-run stop` 的输出是 `docker compose -p roomusic-prod … stop`，没有 `down`、`-v`；
   `scripts/production.sh --dry-run up` 依次是构建、启动数据库、备份、启动。
4. `make prod`：输出含要部署的提交；`make prod-status` 显示 app、postgres、backup healthy，版本等于 `git rev-parse --short=12 main`。
5. `make prod-backup` 得到一个 `-manual` 备份文件；再执行一次 `make prod`：提示镜像已存在、跳过构建，仍先做备份，最后 healthy。
6. **开发库核对**：重复第 2 步，全部不变（X4）。
7. **收尾**：确认 `http://192.168.50.206:8090` 打开初始化页面、备份目录在启动后出现第一份备份（C4）。**不**替用户初始化管理员。

## 11. CI

在 `integration` 任务的“Validate Compose configuration”步骤追加（几秒，不拉镜像、不构建）：

```bash
docker compose -f deploy/production/compose.yaml --env-file .env.prod.example config --quiet
bash -n deploy/production/*.sh
# 固定版本一致性：ffmpeg 镜像、Go 版本、tools 阶段与 Compose 的 PostgreSQL 镜像
grep -qF "$(scripts/ffmpeg-pinned.sh --image)" deploy/production/Dockerfile
grep -qF "golang:$(sed -n 's/^go = "\(.*\)"/\1/p' .mise.toml)-" deploy/production/Dockerfile
grep -qF "$(sed -n 's/^x-postgres-image: &postgres-image //p' deploy/production/compose.yaml) AS tools" deploy/production/Dockerfile
```

`Set up shell checks` 的 `bash -n scripts/*.sh` 保持不变，它已经覆盖新增的 `scripts/production.sh` 与改动的 `dev.sh`、`dev-reset.sh`、`reset-password.sh`。
CI 不构建镜像：要拉三个基础镜像、耗时且与现有门禁无关；“从 `main` 导出能构建”由演练步骤 1、1b 与每次 `make prod` 证明。

## 12. 脚本、README 与合同（P8、X6）

- **删除 `scripts/prod.sh`**：生产只保留 Docker 一种方式，`make prod` 改为调用 `scripts/production.sh up`。origins 子任务先改它的
  `SECURE_COOKIES` 检查，本任务再整体删除。
- **`scripts/dev.sh`**：只去掉“`ROOMUSIC_ENV_FILE` 等于 `.env` 时再 source `.env`”那段；它前面的 `requested_environment`/`requested_database_url`
  两行与后面的两行恢复只为这段服务，一并删除（行为不变）。仍默认读 `.env.dev`，仍支持 `ROOMUSIC_ENV_FILE`；其余不动。
- **`scripts/dev-reset.sh`**：改为读 `${ROOMUSIC_ENV_FILE:-.env.dev}`（缺失时报“缺少开发配置”）；保留“shell 里的 `ROOMUSIC_ENV`、`ROOMUSIC_DATABASE_URL`
  优先于文件”的现有写法；结果为 `production` 时照旧拒绝。它只作用于开发库。
- **`scripts/reset-password.sh`**：默认读 `${ROOMUSIC_ENV_FILE:-.env.dev}`，提示文字改为“缺少开发配置”；文件里的 `ROOMUSIC_ENV` 为 `production` 时拒绝，
  提示“生产请用 make prod-reset-password U=<用户名>”。生产忘记密码只走 `docker compose exec`（第 16.4 节）。
- **`.mise.toml`**：`env-up`、`env-down`、`env-logs`、`env-check` 的 `docker compose` 加 `--env-file .env.dev`。现在它们靠 Compose 隐式读根目录 `.env`
  取 `PG_PASSWORD` 等值，而根目录 `.env` 从来没有这些值。其余不变。
- **README**：
  - “开发环境”：配置 `.env.dev`（由 `.env.example` 复制）；`make dev` 启动；开发库重置 `make dev-reset`；`mise run env-*` 照旧可用（已显式读 `.env.dev`）。
  - “生产环境”：一句话说明两套同时运行（开发 8080、生产 8090，配置分别是 `.env.dev`、`.env.prod`）、`make prod*` 命令表（第 16.4 节的简化版），
    详细步骤链接 `deploy/production/README.md`。删除原“生产环境使用无后缀 `.env` … `./scripts/prod.sh` … `ROOMUSIC_SKIP_BUILD`”段落。
  - “忘记密码”：开发 `./scripts/reset-password.sh <用户名>`，生产 `make prod-reset-password U=<用户名>`；不再说“读取与 `scripts/prod.sh` 相同的 `.env`”。
- **`core0-runtime-contracts.md`**：
  - “接口签名”的“生产启动”改为：`make prod`（`scripts/production.sh`）从 `main` 导出构建镜像，以该提交的 `deploy/production/compose.yaml` 启动（项目 `roomusic-prod`）。
  - “环境与资产”：`.env.dev`、`.env.prod`、`.env` 是本机配置，必须被 Git 忽略；仓库只跟踪 `.env.example` 与 `.env.prod.example`。
    补一条生产部署形态：镜像固定版本、ffmpeg 与 `scripts/ffmpeg-pinned.sh` 同一摘要、端口只发布 IPv4、固定子网、每日 `pg_dump`、与开发同时运行互不影响。
  - 删去“`scripts/prod.sh` 不启动 Node 服务”。

## 13. 考虑过但不选

| 方案 | 不选的原因 |
| --- | --- |
| 镜像里构建前端 | P1 要求不依赖 Node；内嵌产物已提交且有 CI 漂移检查 |
| 运行时用 distroless | 入口脚本需要 shell 与 `pg_isready`，健康检查需要 `wget` |
| ffmpeg 通过构建参数传入摘要 | 构建参数容易漏传；写死并由 CI 比对更稳 |
| 备份用宿主机 cron | 需要改用户 crontab，不随 Compose 一起启动停止，失败也不会反映在 `docker compose ps` |
| 备份用镜像里的 `crond` | 需要 root 运行，备份文件属主变成 root；10 分钟轮询能补上停机错过的时刻 |
| `backup.sh` 从工作区绑定挂载 | 工作区可能在别的分支或有未提交改动，生产会悄悄用上；改为随提交打进 tools 镜像 |
| PostgreSQL 发布到 127.0.0.1 方便调试 | P2 要求不发布；`docker compose exec postgres psql` 足够 |
| 独立的生产检出目录（clone 或 worktree） | 多一份要同步的仓库；从 `main` 导出构建已能保证只用已提交代码 |
| 从工作区直接 `docker build` | 未提交改动和当前分支会进入镜像，违反 P7 |
| 根目录 `.env` 开关与“同一时间只跑一套”的互斥 | 用户 2026-11-05 最终决定两套同时运行、不要开关与互斥；两套的端口、项目、卷、网络本来就分开 |
| 停生产用 `docker compose down` | 删容器与网络；只停就够，`make prod` 直接接着用 |
| 子网用 `172.31.250.0/24` | 紧挨 sub2api 的两个 /24，以后那边扩网段容易撞；且在 Docker 默认池范围内 |
| 生产用 8080 | 与 `dev.sh` 冲突（4.4） |

## 14. 回滚与风险

回滚：

- 打包本身：回退本任务的提交（恢复 `scripts/prod.sh`、`dev.sh`、`dev-reset.sh`、`reset-password.sh`、Makefile、`.mise.toml`、`.gitignore`、README、CI、spec，
  删除 `deploy/production/`、`scripts/production.sh`、`.env.prod.example`），不影响应用代码。
- 正式实例：`make prod-stop` 即停止，容器与卷都保留；彻底撤掉时由用户确认后再删卷、`roomusic-prod-*` 镜像与备份目录。
- 开发环境：从头到尾不受影响。

风险（部署说明写给用户）：

- **`git clean -x` 会删除 `.env.prod`**（以及被忽略的 `music/`）：建议把 `.env.prod` 另存一份。
- 两套同时运行时，同时扫描同一个音乐目录会各读一遍文件，磁盘读取加倍；只影响扫描速度，不影响数据。
- 旧版本镜像不会自动删除，占用磁盘；部署说明给出按标签删除的命令。

## 15. 验证命令（最小相关集）

```bash
# 仓库根目录
docker compose -f deploy/production/compose.yaml --env-file .env.prod.example config --quiet
bash -n scripts/production.sh scripts/dev.sh scripts/dev-reset.sh scripts/reset-password.sh deploy/production/*.sh
grep -qF "$(scripts/ffmpeg-pinned.sh --image)" deploy/production/Dockerfile
git check-ignore -q .env.prod && ! git check-ignore -q .env.prod.example
test ! -e scripts/prod.sh
scripts/production.sh --dry-run stop | grep -q ' stop' && ! scripts/production.sh --dry-run stop | grep -Eq ' down|-v( |$)'
scripts/production.sh --dry-run up
scripts/production.sh build-image --prefix roomusic-verify   # 一次，确认能从 main 导出构建；结束后按标签删除 roomusic-verify-*
```

- `--dry-run` 需要 `.env.prod` 才能走到生产 Compose 那一步；实现 agent 没有 `.env.prod` 时，复制 `.env.prod.example` 到 `mktemp -d` 下，
  用 `ROOMUSIC_PROD_ENV_FILE=<临时文件>`（第 16.3 节）指过去，不在仓库根目录创建 `.env.prod`。
- 改动 `dev.sh`、`dev-reset.sh`、`reset-password.sh` 后不实际运行（会碰用户的开发库），只做 `bash -n` 与代码审阅。
- 其余验证就是第 10 节的演练与正式启动；打包任务不跑 Go 或前端测试（没有改应用代码）。

## 16. 两套配置（P11）

### 16.1 文件与职责

| 文件 | 内容 | 谁读 |
| --- | --- | --- |
| `.env.dev` | 开发配置（由 `.env.example` 复制） | `dev.sh`、`dev-reset.sh`、`reset-password.sh`、`.mise.toml` 的 `env-*` 任务（`--env-file .env.dev`） |
| `.env.prod` | 生产配置（由 `.env.prod.example` 复制，`chmod 600`） | 生产 Compose（`--env-file .env.prod`）；`scripts/production.sh` 只逐行读出两个目录路径与端口做预检和显示 |
| `.env` | 用户的占位文件（只有注释），不改不删 | 没有任何脚本读取；只有不带 `--env-file` 的开发 Compose 会隐式读它（第 16.5 节） |

三个文件都被 `.gitignore` 的 `.env`、`.env.*` 规则忽略。

### 16.2 两套同时运行

| 项 | 开发 | 生产 |
| --- | --- | --- |
| 启动 | `make dev`（前台：`go run` + Vite，开发库容器） | `make prod`（后台容器，`restart: unless-stopped`） |
| 配置 | `.env.dev` | `.env.prod` |
| 代码来源 | 工作区当前文件（含未提交改动） | `main`（或 `REF=`）导出的提交 |
| Compose 项目 / 文件 | `roomusic` / 根目录 `compose.yaml` | `roomusic-prod` / 被部署提交的 `deploy/production/compose.yaml` |
| 对外端口 | 后端 `:8080`、Vite `:5173`；开发库 `127.0.0.1:5432`，Redis `127.0.0.1:6379`，Meilisearch `127.0.0.1:7700`（按 `.env.dev`） | 只有 `0.0.0.0:8090`；数据库不发布端口 |
| 数据库容器 / 卷 | `roomusic-postgres-1` / `roomusic_postgres-data` | `roomusic-prod-postgres-1` / `roomusic-prod_postgres-data` |
| 数据目录 | `backend/data/`（宿主机） | `roomusic-prod_app-data` 卷 |
| 网络 | `roomusic_default`（Docker 自动分配，172.18.0.0/16） | `roomusic-prod_roomusic`（固定 `10.203.50.0/24`） |
| 备份 | 无 | 每日 + `make prod-backup`，`/home/qlqq/roomusic-backups` |
| 重启机器后 | 开发库容器自动起来；`dev.sh` 不会 | 自动恢复（入口脚本等数据库，第 3 节） |

- 两套没有共享的端口、容器、卷、网络或配置文件，启动、停止、升级任何一套都不影响另一套；演练（`roomusic-drill`，18090/18443、`10.203.51.0/24`）
  与两者也都不相交。
- 唯一共享的是音乐目录：开发直接读宿主机路径，生产只读挂载；两边都不写它。

### 16.3 `scripts/production.sh`

```text
scripts/production.sh [--dry-run] <命令> [参数]

命令：
  up [--ref <提交>]                              构建（已有则跳过）→ 启动数据库 → 备份 → 启动并等到健康
  stop                                           停止（docker compose stop，只停不删）
  status                                         容器状态与正在运行的版本
  logs                                           看日志（-f --tail 200）
  backup                                         立即备份
  reset-password <用户名>                        重置密码
  build-image [--ref <提交>] [--prefix <前缀>]   从提交导出构建镜像（默认前缀 roomusic-prod）
```

- 仓库根由脚本自身位置推出，所有 Compose 调用都先 `cd` 到仓库根。env 文件默认 `<仓库根>/.env.prod`；环境变量 `ROOMUSIC_PROD_ENV_FILE` 可指向别的文件，
  只供验证与测试使用（第 15 节），部署说明不提。
- 调用生产 Compose 前清掉自身环境里的 `ROOMUSIC_*`（`ROOMUSIC_PROD_ENV_FILE` 读出后再清）与 `COMPOSE_*`（第 4.2 节）。
- `--dry-run`：只执行只读查询（`git rev-parse`、`docker image inspect`、`docker ps`），把会改变状态的命令逐行以 `+ ` 开头打印而不执行。
- 退出码：0 成功；1 执行失败；2 用法错误或找不到提交。
- `backup`、`reset-password` 在生产没在运行时提示“生产没有在运行，先 make prod”，退出 1；`status` 照常打印容器列表并加这一句，退出 0。

### 16.4 命令对照

`<生产 compose>` 见第 4.2 节；`<部署 compose>` 是同样的参数但 `-f` 指向从被部署提交取出的临时 `compose.yaml`（第 3.1 节第 5 步）。

| Make 目标 | 实际执行 |
| --- | --- |
| `make prod [REF=<提交>]` | `scripts/production.sh up [--ref <提交>]`：预检 `.env.prod`（存在、权限、两个目录）→ `build-image`（同一提交已有镜像则跳过）→ `<部署 compose> up -d --wait postgres` → `<部署 compose> run --rm --no-deps -T backup now`（失败则中止，不换版本）→ `ROOMUSIC_IMAGE=roomusic-prod-app:<短 sha> ROOMUSIC_TOOLS_IMAGE=roomusic-prod-tools:<短 sha> <部署 compose> up -d --wait --wait-timeout 300` → 打印 `ps` 与地址 |
| `make prod-stop` | `<生产 compose> stop`（app、backup 先停，postgres 最后，遵守 `stop_grace_period`）；**绝不** `down`、`-v` |
| `make prod-status` | `<生产 compose> ps`、app 容器的 `org.opencontainers.image.revision`、地址 `http://192.168.50.206:<ROOMUSIC_HTTP_PORT>` |
| `make prod-logs` | `<生产 compose> logs -f --tail 200` |
| `make prod-backup` | `<生产 compose> exec backup /bin/sh /opt/roomusic/backup.sh now` |
| `make prod-reset-password U=<名>` | `<生产 compose> exec app /app/roomusic reset-password <名>`；`U` 为空时打印“用法：make prod-reset-password U=<用户名>”，退出 2 |
| `make dev` | 不变：`./scripts/dev.sh` |
| `make dev-reset` | 不变：`./scripts/dev-reset.sh`（读 `.env.dev`） |

- 升级前备份总是做、失败就中止：首次部署时 `POSTGRES_DB` 已建好空库，`pg_dump` 能正常导出一个很小的文件，不需要“首次跳过”的分支；
  升级时备份失败说明数据库或备份目录有问题，更不该换版本。用 `run --rm --no-deps` 而不是 `exec`，生产此前停着时也能备份。
- 用短 sha 标签启动（而不是 `:current`）：Compose 看到镜像名变化一定会重建容器；`:current` 留给手工命令与默认值。
- Makefile：

```make
prod:
	./scripts/production.sh up $(if $(REF),--ref '$(REF)')

prod-stop prod-status prod-logs prod-backup:
	./scripts/production.sh $(patsubst prod-%,%,$@)

prod-reset-password:
	./scripts/production.sh reset-password '$(U)'
```

  `.PHONY` 补上这些目标；原 `prod: ./scripts/prod.sh` 删除。

### 16.5 开发 Compose 与根目录 `.env`

- 不给 `--env-file` 时，Compose 会隐式读取项目目录（仓库根）下的 `.env` 做变量插值。`dev.sh`、`dev-reset.sh` 里的 `docker compose` 仍是这样调用的。
- 根目录 `.env` 现在只有注释，对开发 Compose 没有影响：`compose.yaml` 需要的 `PG_PASSWORD`、`MEILI_MASTER_KEY` 由 `dev.sh`、`dev-reset.sh` 先 `source .env.dev`
  导出到环境里，环境变量优先于隐式 `.env`；隐式 `.env` 只用于插值，不会注入容器（`compose.yaml` 没有 `env_file:`）。
- 生产的所有 Compose 调用都带 `--env-file .env.prod`，不会读根目录 `.env`；Go 应用也不加载任何 `.env` 文件。

### 16.6 部署说明要写清的几句

- 开发和生产同时运行：开发 `http://192.168.50.206:8080`（或 Vite 5173），生产 `http://192.168.50.206:8090`；两边的数据各自独立。
- 生产只用 `main` 上已提交的代码：改动合进 `main` 后 `make prod` 才会生效；回退 `make prod REF=<提交>`。
- `make prod-stop` 只停不删；不要运行 `docker compose … down -v`。
- `.env.prod` 不在 Git 里，`git clean -x` 会删掉它（以及 `music/`），建议另存一份。

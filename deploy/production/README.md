# 部署说明

ROOMusic 生产环境只有两个容器：`app` 和 PostgreSQL 18。应用自带备份和 ffmpeg，数据库不对外开放端口。
宿主机只需要 Docker 和 Docker Compose v2，不需要克隆仓库。下文命令都在部署目录里执行。

**不要运行 `docker compose down -v`，它会删除数据库。**

## 1. 安装

部署文件从 `main` 下载，镜像默认使用 `latest`：

```bash
mkdir -p /srv/roomusic && cd /srv/roomusic
curl -fL -o compose.yaml https://raw.githubusercontent.com/qlqqs/ROOMusic/main/compose.prod.yaml
curl -fL -o .env https://raw.githubusercontent.com/qlqqs/ROOMusic/main/.env.prod.example
chmod 600 .env
```

编辑 `.env`，必填两项：

| 变量 | 填写内容 |
| --- | --- |
| `ROOMUSIC_DB_PASSWORD` | 数据库密码，只用字母和数字，可以用 `openssl rand -hex 24` 生成。第一次启动后再改不会改数据库里的密码 |
| `ROOMUSIC_MUSIC_DIR` | 音乐目录的绝对路径，需要 uid 1000 可读，以只读方式挂载到 `/music` |

启动：

```bash
docker compose up -d --wait
```

打开 `http://<主机>:8090`，**先创建管理员，再开放公网访问**。在“扫描目录”添加 `/music` 或其子目录，扫描完成后即可浏览和播放；
在“成员”页添加其他账号。

`.env` 含密码，请另存一份，不要公开。要检查配置，运行 `docker compose config --quiet`，不要分享完整输出。

### 可选设置

都有默认值，需要时再填：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ROOMUSIC_VERSION` | `latest` | 镜像版本；固定或降级时填具体版本，不带 `v` |
| `ROOMUSIC_HTTP_PORT` | `8090` | 网页端口 |
| `ROOMUSIC_BACKUP_DIR` | Docker 卷 `backups` | 填绝对路径则把备份放在宿主机目录，需要 uid 1000 可写 |
| `ROOMUSIC_BACKUP_HOUR` | `3` | 每天几点后自动备份，0–23 |
| `ROOMUSIC_TIMEZONE` | `Asia/Shanghai` | 自动备份使用的时区 |
| `ROOMUSIC_BACKUP_KEEP_DAYS` | `14` | 网页上从未设置过自动清理时，备份保留几天 |
| `ROOMUSIC_ALLOWED_ORIGINS` | 空 | 反向代理改写了 Host 时使用，见第 5 节 |
| `COMPOSE_PROJECT_NAME` | `roomusic-server` | 项目名，决定数据卷和网络的名称；已有实例不要改 |

把备份放到宿主机目录时，先创建目录并设置权限：

```bash
mkdir -p /srv/roomusic-backups && sudo chown 1000:1000 /srv/roomusic-backups
```

然后在 `.env` 写 `ROOMUSIC_BACKUP_DIR=/srv/roomusic-backups`。

运行命令的终端里不要设置同名的 `ROOMUSIC_*` 或 `COMPOSE_*` 环境变量，它们会覆盖 `.env`。

## 2. 日常命令

| 操作 | 命令 |
| --- | --- |
| 状态 | `docker compose ps` |
| 日志 | `docker compose logs -f --tail 200 app` |
| 版本 | `docker compose exec app /app/roomusic --version` |
| 立即备份 | `docker compose exec app /app/roomusic backup` |
| 重置密码 | `docker compose exec app /app/roomusic reset-password <用户名>` |
| 停止（保留数据） | `docker compose stop` |
| 启动 | `docker compose up -d --wait` |

重置密码时输入两次新密码，不需要停服务，成功后该账号在所有设备上退出登录。提示尝试次数太多时，等一分钟再试。

## 3. 升级与降级

默认使用 `latest`，升级时运行：

```bash
docker compose pull && docker compose up -d --wait
docker compose exec app /app/roomusic --version
```

要固定或降级版本，把 `.env` 里的 `ROOMUSIC_VERSION` 改成具体版本（如 `0.1.0`，不带 `v`），再执行同样的命令。

应用启动时会比较自己的数据库结构和数据库里的现状：

- **结构没变**：直接启动。
- **升级需要改结构**：先自动备份，再升级数据库；备份失败则不升级、不启动。
- **降级**：先自动备份，再把新版本做过的结构改动撤回。撤回会删掉新功能产生的数据，这些数据仍保留在刚才的备份里。
- **无法自动撤回**：应用不会启动，数据库保持不变，日志会给出要恢复的备份文件和完整命令。按第 4 节恢复，或者把版本改回去。

降级到不支持服务端播放区的版本后，各设备的播放队列会清空；降级前的队列保留在自动备份里。

最低可以降到 `0.1.0`。

`0.1.0` 不认识网页上设置的自动清理，只按 `ROOMUSIC_BACKUP_KEEP_DAYS` 清理旧备份。如果在网页上把保留天数设成了“不限”或比这个值更长，
降级前先把 `ROOMUSIC_BACKUP_KEEP_DAYS` 调大，不然降级后第一次备份完成时会按它删掉更早的备份。再升级回来，网页上的设置照常生效。

## 4. 备份与恢复

管理员在“设置 › 备份”可以查看备份状态、立即备份、下载和删除备份文件（可以勾选多份一起删除）。默认每天上海时间 3 点后自动备份一次，保留 14 天；
每次升级或降级改动数据库前也会备份一次。

同一页的“自动清理”可以设置保留天数和保留份数，两项都可以选“不限”：超过天数的、多出份数的旧备份会在每次备份完成后删除，
最新的一份始终保留。保存时如果会删掉已有的备份，会先确认要删几份。网页上设置过之后，`ROOMUSIC_BACKUP_KEEP_DAYS` 就不再起作用；
设置保存在备份目录里，重启或恢复数据库都不会改变它，换了备份目录则回到默认值。

备份只包含数据库，不含音乐文件、封面和 ALAC 转码缓存；需要异地保护时，把备份文件另存到其他机器。
备份里有账号等私人数据，请妥善保管，只恢复可信来源的文件。

### 恢复

把备份文件放进备份目录（自动备份已经在里面了），然后只用文件名执行，例如 `roomusic-20261106-030000-manual.dump`：

```bash
docker compose stop app
docker compose run --rm app restore <备份文件名> && docker compose up -d --wait
```

命令会先显示备份信息，输入 `roomusic` 回车确认。恢复前会先备份当前数据库，所以恢复操作本身也可以撤回；
恢复后，备份时间点之后的数据会被替换。恢复失败时应用保持停止，按输出的提示处理后再启动。

## 5. 反向代理

反向代理要原样转发浏览器的 Host（包括端口）和 Origin。nginx 写法：

```nginx
proxy_set_header Host $http_host;
```

不要用 `$host`，它会丢掉 `:8888` 这样的端口。Host 被改写时，页面能打开，但登录、收藏等操作会失败，
页面提示“请用正确的网址打开页面后重试。”，局域网直连不受影响。

无法修正反向代理时，在 `.env` 列出所有访问入口，再执行 `docker compose up -d`：

```ini
ROOMUSIC_ALLOWED_ORIGINS=http://192.168.1.10:8090,https://music.example.com:8888
```

每个入口都要写完整网址，不支持通配符和路径。应用不读取 `X-Forwarded-*` 或 `Forwarded` 头，改它们没有用。

Cookie 的 Secure 属性会跟随访问协议自动设置，不需要额外配置。局域网 IP 和公网域名各自登录。
经反向代理访问的用户共用同一个登录限速来源。

### 播放区事件流与渲染流

事件流 `/api/v1/me/playback/events` 与渲染流 `/api/v1/me/playback/zones/{zone_id}/streams/{seq}` 都是长响应：

- 禁止响应缓冲。应用会发送 `X-Accel-Buffering: no`；代理不能忽略它，其他代理需要显式关闭缓冲。
- 上游读超时至少 **60 秒**（事件流每 15 秒发送心跳）。
- 不压缩 `text/event-stream`，避免压缩器积攒事件导致延迟。

可在现有 nginx 播放区代理 location 中加入下列设置（沿用原有 `proxy_pass` 与 Host 转发）：

```nginx
proxy_buffering off;
proxy_read_timeout 60s;
gzip off;
```

其他代理也须设置等效选项。若别的设备上的播放变化很久才出现，先检查事件流是否被缓冲或压缩。

## 6. 固定子网

Docker 默认自动分配子网，可能和局域网地址冲突。需要固定时，在部署目录新建 `compose.override.yaml`：

```yaml
networks:
  roomusic:
    ipam:
      config:
        - subnet: 10.203.50.0/24
```

选一个不冲突的网段。已经启动过的实例要改子网，先 `docker compose down`（不要加 `-v`），再 `docker compose up -d --wait`。

## 7. 排查

- **日志**：应用输出 JSON 日志，每个请求一条 `http.request.completed`，带请求 ID、路由、状态码和耗时；
  不记录查询参数、请求体、Cookie、密码和完整音乐路径。容器日志按 10 MB × 5 个文件轮转。
- **健康检查**：`/readyz`。
- **什么都放不了**：多半是启动时 ffmpeg 自检失败，看日志里 `playback.render.unavailable` 的 `reason`。
  所有播放都经服务器处理，渲染器不可用时任何格式都放不了，浏览和其他功能照常。
- **登录或收藏报 403**：多半是反向代理改写了 Host，看日志里 `identity.origin.rejected` 的 `origin` 和 `host`，按第 5 节处理。
- **升级后应用一直不就绪**：看日志里的数据库迁移提示，按第 3、4 节处理。

`ROOMUSIC_AUDIO_ANALYSIS=off` 可关闭后台音频分析（默认 `on`）；ffmpeg 路径沿用 `ROOMUSIC_FFMPEG_PATH`。

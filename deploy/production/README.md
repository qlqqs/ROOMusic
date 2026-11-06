# 生产部署与恢复

生产只运行 `app` 与 PostgreSQL 18，应用内置备份与 ffmpeg，数据库不发布端口。
宿主机需要 Docker 和 Docker Compose v2（支持 `start_interval`、`up --wait`）；不需要克隆仓库或安装 Go、Node、Make。
下文命令均在部署目录执行。

**不要对生产项目运行 `docker compose down -v`，它会删除数据库卷。**

## 1. 下载

从 GitHub Release 下载同一版本的 `compose.yaml` 与 `example.env` 到任意独立目录，例如 `/srv/roomusic`。
用 curl 下载指定版本（以 `0.1.0` 为例，需该版本已发布）：

```bash
mkdir -p /srv/roomusic
cd /srv/roomusic
curl -fLO https://github.com/qlqqs/ROOMusic/releases/download/v0.1.0/compose.yaml
curl -fLO https://github.com/qlqqs/ROOMusic/releases/download/v0.1.0/example.env
cp example.env .env
chmod 600 .env
```

Release 附件已写入版本号；仓库里的文件保留 `__RELEASE_VERSION__`，不直接用于启动。

## 2. 填两项

编辑 `.env`：

| 变量 | 填写内容 |
| --- | --- |
| `ROOMUSIC_DB_PASSWORD` | 数据库密码，只用字母和数字，可用 `openssl rand -hex 24` 生成；首次启动后改此项不会改库内密码 |
| `ROOMUSIC_MUSIC_DIR` | 已存在的音乐目录绝对路径，需 uid 1000 可读；只读挂载到 `/music` |

`.env` 请另存一份，不要公开；检查配置使用 `docker compose config --quiet`，不要分享含密码的完整输出。

### 更多设置

不填以下可选项即可使用默认值：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ROOMUSIC_VERSION` | Release 对应版本 | 镜像标签，不带 `v`，升级或降级时修改 |
| `ROOMUSIC_HTTP_PORT` | `8090` | 网页端口，只发布 IPv4 |
| `ROOMUSIC_BACKUP_DIR` | 命名卷 `backups` | 填绝对路径时改为宿主机目录，需要 uid 1000 可写 |
| `ROOMUSIC_BACKUP_HOUR` | `3` | 每天几点后备份，整数 0–23 |
| `ROOMUSIC_TIMEZONE` | `Asia/Shanghai` | 备份时区 |
| `ROOMUSIC_BACKUP_KEEP_DAYS` | `14` | 备份保留天数，正整数 |
| `ROOMUSIC_ALLOWED_ORIGINS` | 空 | 反代无法保留 Host 时填允许的完整网址，见下文 |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | `10240` | ALAC 派生缓存上限（MiB），最小 256 |
| `COMPOSE_PROJECT_NAME` | `roomusic-server` | 项目名，决定卷与网络名称；已有实例须保持原名 |

要把备份放在宿主机目录，先创建目录并设置权限，再在 `.env` 填 `ROOMUSIC_BACKUP_DIR=/srv/roomusic-backups`：

```bash
mkdir -p /srv/roomusic-backups
sudo chown 1000:1000 /srv/roomusic-backups
```

运行命令的 shell 不要设置同名 `ROOMUSIC_*` 或 `COMPOSE_*` 变量，以免覆盖 `.env`。

## 3. 启动

需要固定子网时先按第 6 节创建 override。

```bash
docker compose config --quiet
docker compose up -d --wait
docker compose ps
```

打开 `http://<主机>:8090`，**先初始化管理员，再开放公网反代**。在“扫描目录”添加 `/music` 或其子目录，扫描后即可浏览和播放。
在“成员”页添加其他账号。开发环境继续使用仓库里的 `make dev`，与生产各用独立的数据库、卷和网络。

## 4. 升级与降级

修改 `.env` 中的 `ROOMUSIC_VERSION`，然后：

```bash
docker compose pull
docker compose up -d --wait
docker compose exec app /app/roomusic --version
```

应用在每次数据库结构变化前自动保存一份“版本切换前”备份，备份失败不会继续迁移。
支持自动回退的迁移会在降级时执行；不能自动回退时 app 不会就绪，日志会说明要恢复哪份备份，按下一节恢复后再启动目标版本。
最低可降到首个正式版 `0.1.0`；旧的按提交构建的实例按文末专用步骤回退。

## 5. 备份与恢复

管理员打开“系统 › 备份”可以查看备份状态、立即备份和下载文件。默认上海时间每天 3 点后自动备份，保留 14 天。
备份只包含 PostgreSQL，不含音乐、封面与 ALAC 派生缓存；需要异地保护时，将备份另存到其他机器。
备份含账号等私人数据，文件权限为 600；只恢复可信来源的文件。

### 日常命令

| 操作 | 命令 |
| --- | --- |
| 状态 | `docker compose ps` |
| 日志 | `docker compose logs -f --tail 200 app` |
| 版本 | `docker compose exec app /app/roomusic --version` |
| 立即备份 | `docker compose exec app /app/roomusic backup` |
| 重置密码 | `docker compose exec app /app/roomusic reset-password <用户名>` |
| 停止（保留数据） | `docker compose stop` |
| 再次启动 | `docker compose up -d --wait` |

重置密码时在终端输入两次新密码，不回显、不必停服务；成功后该账号在所有设备退出。
若仍提示尝试次数太多，等一分钟再试。

### 恢复数据库

先从“系统 › 备份”选定文件，文件须位于当前挂载的备份目录中。参数只填文件名，例如 `roomusic-20261106-030000-manual.dump`：

```bash
docker compose stop app
docker compose run --rm app restore <备份文件名> && docker compose up -d --wait
```

恢复命令会显示备份信息并要求确认，先备份当前库，再重建数据库并恢复；恢复点之后的数据会被替换。
失败时保持 app 停止，按输出的安全备份恢复命令处理，成功后再启动。

## 6. 固定子网

默认由 Docker 自动分配子网。需要避开局域网地址池时，在部署目录添加 `compose.override.yaml`，Compose 会自动合并：

```yaml
networks:
  roomusic:
    ipam:
      config:
        - subnet: 10.203.50.0/24
```

选择与现有网络不冲突的网段。修改已存在网络的子网，需要先 `docker compose down`（不加 `-v`），再 `docker compose up -d --wait`。

## 7. 反代与来源

不要填写 `ROOMUSIC_SECURE_COOKIES` 或 `ROOMUSIC_PUBLIC_URL`；
生产的 Cookie 安全属性跟随请求的来源协议，http 不带 Secure，https 带 Secure。默认不填清单，写请求的 Origin 与请求 Host
规范化后一致即放行；反代须原样保留浏览器的 Host（含端口）与 Origin。nginx 的 Host 转发配置为：

```nginx
proxy_set_header Host $http_host;
```

不要用 `$host`（会丢掉 `:8888` 等端口），也不要把 Host 改成上游地址。若 Host 被改写，页面仍能浏览，但登录、收藏等写操作会返回
`403 origin_forbidden`，页面提示“请用正确的网址打开页面后重试。”；局域网直连不受影响。查看 `identity.origin.rejected` 日志的
`mode`、`origin`、`host`（各最多 256 字节）可定位差异。两种解决办法：

1. 修正反代，使 Host（含端口）与 Origin 原样转发。
2. 做不到时，在 `.env` 填写 `ROOMUSIC_ALLOWED_ORIGINS`，包含所有允许的入口（如 `http://192.168.50.206:8090,https://music.example.com:8888`），
   再 `docker compose up -d`。清单模式不看 Host，但仍须保留 Origin；来源不允许通配或路径。

不要靠 `X-Forwarded-Host`、`X-Forwarded-Proto` 或 `Forwarded` 修复，应用在任何模式都不读取这些转发头。

局域网 IP 与公网域名分别使用自己的 Cookie。登录限速使用 TCP 对端来源，经反代的用户共享该来源。

### 日志与排查

应用输出 JSON 日志，每个请求一条 `http.request.completed`，包含请求 ID、路由模板、状态码和耗时，
不记录查询字符串、请求体、Cookie、密码或完整音乐路径。健康检查使用 `/readyz`；容器日志按 10 MB × 5 个文件轮转。

ALAC 首次播放时无损转成 FLAC，缓存在应用数据卷中。启动时 ffmpeg 自检失败，应用照常运行，但 ALAC 曲目暂不能播放，
日志会记录 `playback.derivation.unavailable`。

## 8. 本机旧生产实例过渡

本节只用于现有 `roomusic-prod`。前提：`0.1.0` 已发布且镜像可匿名拉取，隔离演练已通过，用户确认切换。
以下是待执行的运维步骤，不代表仓库文件更新后生产已经切换。

| 保持不变 | 值 |
| --- | --- |
| 项目 | `roomusic-prod` |
| 数据库卷 | `roomusic-prod_postgres-data` |
| 应用数据卷 | `roomusic-prod_app-data` |
| 网络 / 子网 | `roomusic-prod_roomusic` / `10.203.50.0/24` |
| 端口 | `8090` |
| 音乐目录 | `/home/qlqq/workspace/ROOMusic/music` |
| 备份目录 | `/home/qlqq/roomusic-backups` |
| 新部署目录 | `/home/qlqq/roomusic-prod` |

数据不迁移、不重建，仓库根 `.env.prod` 与旧镜像保留用于回退。

### 8.1 切换步骤

1. **记录旧镜像、提交与网络创建时间，提取旧 Compose。** 在同一 Bash 会话中执行本节命令：

   ```bash
   cd /home/qlqq/workspace/ROOMusic
   app_id=$(docker ps -q --filter label=com.docker.compose.project=roomusic-prod --filter label=com.docker.compose.service=app)
   docker inspect -f '{{.Config.Image}}' "$app_id" > /home/qlqq/roomusic-prod-old-image.txt
   docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$app_id" > /home/qlqq/roomusic-prod-old-commit.txt
   docker network inspect -f '{{.Created}}' roomusic-prod_roomusic > /home/qlqq/roomusic-prod-network-created.txt
   old=$(cat /home/qlqq/roomusic-prod-old-commit.txt)
   git show "$old:deploy/production/compose.yaml" > /home/qlqq/roomusic-prod-old-compose.yaml
   img=$(cat /home/qlqq/roomusic-prod-old-image.txt)
   old_compose() {
     env -u COMPOSE_PROJECT_NAME ROOMUSIC_IMAGE="$img" ROOMUSIC_TOOLS_IMAGE="${img/-app:/-tools:}" \
       docker compose -p roomusic-prod -f /home/qlqq/roomusic-prod-old-compose.yaml \
       --env-file /home/qlqq/workspace/ROOMusic/.env.prod "$@"
   }
   old_compose ps
   counts_sql="SELECT 'users', count(*) FROM users UNION ALL SELECT 'release_groups', count(*) FROM release_groups UNION ALL SELECT 'tracks', count(*) FROM tracks UNION ALL SELECT 'favorite_tracks', count(*) FROM favorite_tracks UNION ALL SELECT 'favorite_albums', count(*) FROM favorite_albums UNION ALL SELECT 'max_migration', max(version) FROM schema_migrations"
   old_compose exec -T postgres psql -X -qAt -U roomusic -d roomusic -c "$counts_sql" \
     > /home/qlqq/roomusic-prod-counts-before.txt
   ```

   此处只使用运行中旧镜像对应的历史 Compose，不使用仓库里已改写的新文件。

2. **用旧服务备份。** 执行后确认旧备份目录中出现新的 `-manual.dump`：

   ```bash
   old_compose exec backup /bin/sh /opt/roomusic/backup.sh now
   ```

3. **准备新部署目录。**

   ```bash
   mkdir -p /home/qlqq/roomusic-prod
   cd /home/qlqq/roomusic-prod
   curl -fLO https://github.com/qlqqs/ROOMusic/releases/download/v0.1.0/compose.yaml
   curl -fLO https://github.com/qlqqs/ROOMusic/releases/download/v0.1.0/example.env
   cp example.env .env
   chmod 600 .env
   ```

4. **沿用旧密码及常用配置。** 删除示例中的两个必填空行后复制，不在终端显示配置内容：

   ```bash
   sed -i '/^ROOMUSIC_DB_PASSWORD=$/d; /^ROOMUSIC_MUSIC_DIR=$/d' .env
   grep -E '^(ROOMUSIC_DB_PASSWORD|ROOMUSIC_ALLOWED_ORIGINS|ROOMUSIC_TIMEZONE|ROOMUSIC_BACKUP_HOUR|ROOMUSIC_BACKUP_KEEP_DAYS|ROOMUSIC_MEDIA_CACHE_MAX_MIB)=' \
     /home/qlqq/workspace/ROOMusic/.env.prod >> .env
   printf '%s\n' 'COMPOSE_PROJECT_NAME=roomusic-prod' 'ROOMUSIC_MUSIC_DIR=/home/qlqq/workspace/ROOMusic/music' \
     'ROOMUSIC_BACKUP_DIR=/home/qlqq/roomusic-backups' >> .env
   ```

   端口使用默认 `8090`，不另写配置。

5. **固定子网。** 将第 6 节的 `compose.override.yaml` 放入新部署目录，子网保持 `10.203.50.0/24`。

6. **静态核对，只显示非敏感字段。**

   ```bash
   docker compose config --quiet
   docker compose config --format json | jq '{name, network: .networks.roomusic.name, subnet: .networks.roomusic.ipam.config[0].subnet,
     volumes: [.volumes[].name], image: .services.app.image, ports: .services.app.ports, backups: (.services.app.volumes[] | select(.target=="/backups") | {type, source})}'
   ```

   项目、网络、子网、卷名应与上表一致；镜像为 `ghcr.io/qlqqs/roomusic:0.1.0`，端口为 `0.0.0.0:8090`，
   `/backups` 为 bind 挂载 `/home/qlqq/roomusic-backups`。

7. **先拉镜像。** 失败不影响运行中的旧实例：

   ```bash
   docker compose pull app
   ```

8. **切换。**

   ```bash
   docker compose up -d --wait --remove-orphans
   ```

   app 换成新镜像，旧 `backup` 容器作为孤儿删除；PostgreSQL 若因配置标签变化重建，会短暂重启，但使用原数据卷。

9. **核对数据、备份与网络。**

   ```bash
   docker compose ps
   docker compose exec app /app/roomusic --version
   docker compose exec -T postgres psql -X -qAt -U roomusic -d roomusic -c "$counts_sql" \
     > /home/qlqq/roomusic-prod-counts-after.txt
   diff -u /home/qlqq/roomusic-prod-counts-before.txt /home/qlqq/roomusic-prod-counts-after.txt
   docker compose exec app /app/roomusic backup
   docker network inspect -f '{{(index .IPAM.Config 0).Subnet}}' roomusic-prod_roomusic
   docker network inspect -f '{{.Created}}' roomusic-prod_roomusic > /home/qlqq/roomusic-prod-network-created-after.txt
   diff -u /home/qlqq/roomusic-prod-network-created.txt /home/qlqq/roomusic-prod-network-created-after.txt
   ```

   两个服务均应 healthy，版本为 `0.1.0`，业务行数一致，子网与网络创建时间不变。
   页面“系统 › 备份”应列出旧文件与新手动备份。如果首版新增迁移，`max_migration` 会变化，业务计数仍须一致。

10. **观察一天。** 次日上海时间凌晨 3 点后确认有新的定时备份，再由用户决定何时删除仓库根 `.env.prod` 与旧应用、tools 镜像。

### 8.2 回退步骤

- **切换前（第 8 步尚未执行）失败**：旧实例未改变，保留旧配置与记录；新部署目录可稍后重试或删除。
- **切换后需要回退**：先停止新应用，保留数据库与备份：

  ```bash
  cd /home/qlqq/roomusic-prod
  docker compose stop app
  docker compose exec -T postgres psql -X -qAt -U roomusic -d roomusic -c 'SELECT max(version) FROM schema_migrations'
  ```

  **A 路线：数据库仍是迁移 30。** 首版未新增迁移时走此路线，直接恢复旧应用与旧备份服务：

  ```bash
  cd /home/qlqq/workspace/ROOMusic
  old=$(cat /home/qlqq/roomusic-prod-old-commit.txt)
  git show "$old:deploy/production/compose.yaml" > /home/qlqq/roomusic-prod-old-compose.yaml
  img=$(cat /home/qlqq/roomusic-prod-old-image.txt)
  env -u COMPOSE_PROJECT_NAME ROOMUSIC_IMAGE="$img" ROOMUSIC_TOOLS_IMAGE="${img/-app:/-tools:}" \
    docker compose -p roomusic-prod -f /home/qlqq/roomusic-prod-old-compose.yaml --env-file .env.prod \
    up -d --wait --remove-orphans
  ```

  **B 路线：数据库高于迁移 30。** 先在新部署目录用新镜像恢复切换前的备份，成功后再执行 A 路线：

  ```bash
  cd /home/qlqq/roomusic-prod
  docker compose run --rm app restore <roomusic-时间-pre-migration-30.dump>
  ```

  最后用旧 Compose 执行 8.1 第 1 步的计数 SQL，并与切换前记录核对；恢复备份时，数据以该恢复点为准。
  旧镜像与旧 `.env.prod` 必须保留到过渡确认完成。

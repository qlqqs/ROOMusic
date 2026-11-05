# 生产部署与恢复

本文命令都在仓库根目录 `/home/qlqq/workspace/ROOMusic` 执行。需要 Git、Bash、Docker（BuildKit）、Docker Compose v2
（支持 `start_interval`、`up --wait`）与 Make。镜像构建不需要宿主机 Go 或 Node，前端使用提交里的内嵌产物。
构建需能访问基础镜像、Go 模块和 Alpine 包仓库；若宿主机已配置标准 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`（或小写形式），
构建命令按变量名透传，不打印代理值，不另行配置代理。
本文不配置反代、证书、域名或路由器；数据库和备份在同一台机器上，不能代替异地备份。

## 两套环境同时运行

| 项目 | 开发 | 生产 |
| --- | --- | --- |
| 配置 | `.env.dev`，来自 `.env.example` | `.env.prod`，来自 `.env.prod.example` |
| 启动 | `make dev` | `make prod` |
| 代码 | 工作区（包含未提交改动） | `main` 上的提交，或 `REF=` 指定的提交 |
| 入口 | 后端 `http://192.168.50.206:8080`，Vite 5173 | `http://192.168.50.206:8090` |
| Compose 项目 | `roomusic` | `roomusic-prod` |
| 数据卷 | `roomusic_postgres-data` | `roomusic-prod_postgres-data`、`roomusic-prod_app-data` |
| 网络 | `roomusic_default` | `roomusic-prod_roomusic`，固定 `10.203.50.0/24` |

两边的数据各自独立，不设切换开关。唯一共享的是音乐文件：生产只读挂载，开发也不写音乐；同时扫描会读两遍，可能变慢。
生产只包含 app、PostgreSQL 18 和 backup，不依赖 Redis、Meilisearch。PostgreSQL 不发布端口，应用只发布 IPv4 `0.0.0.0:8090`。
服务带 `restart: unless-stopped`，机器重启后自动恢复；入口脚本等待数据库接受连接再运行应用，健康检查使用 `/readyz`。

**不要运行生产项目的 `docker compose … down -v`，它会删除数据库卷。`make prod-stop` 只停不删，所有 `make prod-*` 命令都不删除卷。**

## 首次部署

先确认部署文件已提交到 `main`；首次部署不能指定早于部署打包功能的提交。

```bash
cd /home/qlqq/workspace/ROOMusic
mkdir -p /home/qlqq/roomusic-backups
cp .env.prod.example .env.prod
chmod 600 .env.prod
# 编辑 .env.prod，尤其要替换数据库密码；可在自己的终端执行 openssl rand -hex 24 生成。
```

配置说明：

| 变量 | 必填 / 默认 | 含义 |
| --- | --- | --- |
| `ROOMUSIC_DB_PASSWORD` | 必填 | 仅字母数字，建议随机 48 位十六进制；首次启动后改配置不会改库内密码 |
| `ROOMUSIC_ALLOWED_ORIGINS` | 可选 / 不填 | 默认主机一致模式；只想放行固定网址或反代无法保留 Host 时，填逗号分隔的完整来源 |
| `ROOMUSIC_HTTP_PORT` | 必填 | 填 `8090`，开发保留 `8080` |
| `ROOMUSIC_MUSIC_DIR` | 必填 | 已存在的绝对目录，本机为 `/home/qlqq/workspace/ROOMusic/music` |
| `ROOMUSIC_BACKUP_DIR` | 必填 | 已存在且 uid 1000 可写的绝对目录，本机为 `/home/qlqq/roomusic-backups` |
| `ROOMUSIC_SUBNET` | 必填 | 生产 `10.203.50.0/24`；演练用不同项目和 `10.203.51.0/24` |
| `ROOMUSIC_TIMEZONE` | `Asia/Shanghai` | 备份时区，与宿主机时区无关 |
| `ROOMUSIC_BACKUP_HOUR` | `3` | 每天几点后备份，整数 0–23 |
| `ROOMUSIC_BACKUP_KEEP_DAYS` | `14` | 保留天数，正整数；仅成功备份后清理旧文件 |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | `10240` | ALAC 派生缓存上限（MiB），最小 256 |

目录和端口按 `KEY=值` 写字面量，不用 `${变量}` 或命令替换；路径可加成对引号。音乐目录需要 uid 1000 可读，备份目录需要 uid 1000 可写。
路径不存在时启动失败，不会自动创建 root 所有的空目录。不要填写 `ROOMUSIC_SECURE_COOKIES` 或 `ROOMUSIC_PUBLIC_URL`；
生产的 Cookie 安全属性跟随请求的来源协议，http 不带 Secure，https 带 Secure。默认不填清单，写请求的 Origin 与请求 Host
规范化后一致即放行；反代须原样保留浏览器的 Host（含端口）与 Origin。nginx 的 Host 转发配置为：

```nginx
proxy_set_header Host $http_host;
```

不要用 `$host`（会丢掉 `:8888` 等端口），也不要把 Host 改成上游地址。若 Host 被改写，页面仍能浏览，但登录、收藏等写操作会返回
`403 origin_forbidden`，页面提示“请用正确的网址打开页面后重试。”；局域网直连不受影响。查看 `identity.origin.rejected` 日志的
`mode`、`origin`、`host`（各最多 256 字节）可定位差异。两种解决办法：

1. 修正反代，使 Host（含端口）与 Origin 原样转发。
2. 做不到时，在 `.env.prod` 填写 `ROOMUSIC_ALLOWED_ORIGINS`，包含所有允许的入口（如 `http://192.168.50.206:8090,https://music.example.com:8888`），
   再 `make prod`。清单模式不看 Host，但仍须保留 Origin；来源不允许通配或路径。

不要靠 `X-Forwarded-Host`、`X-Forwarded-Proto` 或 `Forwarded` 修复，应用在任何模式都不读取这些转发头。

```bash
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod config --quiet
make prod
make prod-status
```

**启动后先初始化管理员，再开放公网反代**，避免未初始化窗口被他人抢先创建管理员。

打开 `http://192.168.50.206:8090`，按页面提示创建管理员，再添加音乐目录 **`/music`**（或其子目录），不是宿主机路径。
首次库为空，不迁移开发库。生产镜像已包含固定版本的 ffmpeg，启动时仍会进行 ALAC 派生自检。

**`.env.prod` 不在 Git 里；`git clean -x` 会删除它和被忽略的 `music/`。请把配置另存一份，并避免在这个工作区运行该清理命令。**
不要把含密码的 `docker compose config` 完整输出贴到日志或提交里，校验只用 `config --quiet`。

## 日常运维

| 操作 | 命令 |
| --- | --- |
| 启动或升级 | `make prod` |
| 回退到指定提交 | `make prod REF=<提交>` |
| 查看状态、版本、地址 | `make prod-status` |
| 跟随最近 200 行日志 | `make prod-logs` |
| 立即备份 | `make prod-backup` |
| 忘记密码 | `make prod-reset-password U=<用户名>` |
| 停止（保留容器和卷） | `make prod-stop` |
| 停止后继续运行 / 配置生效 | `make prod` |

密码在终端输入两次且不回显，重置成功后该账号在所有设备退出；不跑迁移、不必停服务。
重置后若仍提示尝试次数太多，等一分钟再试。

所有日常操作优先使用 Make。需要按服务排查时，手工命令统一写法如下：

```bash
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod ps
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod logs --tail 200 backup
docker compose -p roomusic-prod -f deploy/production/compose.yaml --env-file .env.prod exec postgres psql -U roomusic -d roomusic
```

手工命令前确认 shell 没有残留的 `ROOMUSIC_*`、`COMPOSE_*` 配置（只查看变量名，不打印密码）；它们可能覆盖文件。
`production.sh` 会主动清理这些继承变量。手工命令仅用于查询、exec、停启已有容器等运维；**启动或升级不要手工 `up`，只用 `make prod`**，
因为它会提取目标提交里的 Compose，而不是使用开发中的文件。

### 升级、回退与清理镜像

把代码合进 `main` 并提交后：

```bash
make prod-status
git log --oneline <当前运行的短sha>..main
make prod
```

脚本解析提交，从 `git archive` 导出的树构建 app 与 tools 镜像，并用该提交里的 Compose 启动。
无论工作区是否干净、当前是否在其他分支，未提交文件都不会进入镜像；`--ref` 不在 main 历史中时会明确提示。
同一提交的两个镜像都已存在且 revision 匹配则跳过构建。两个构建都成功才更新 `:current` 标签，实际部署使用 12 位提交标签。

每次 `make prod` 的顺序是：构建 → 启动并等待数据库 → 手动备份 → 换应用和备份服务版本 → 等待健康。
首次空库也备份，**备份失败就停止，不换应用版本**。镜像构建失败不改变运行实例。

回退用 `make prod REF=<提交>`。若新版本已经跑过新迁移，旧版本会拒绝未知迁移，健康等待可能超时；
此时数据库仍运行，用升级前的备份按下节恢复，再观察旧版本是否健康。不要改已发布的迁移 SQL。

旧镜像不自动清理。确认不再需要回退、容器没有使用该镜像后，按标签删除，不使用 `-f` 或按镜像 ID 删除：

```bash
docker image ls roomusic-prod-app
docker image ls roomusic-prod-tools
docker image rm roomusic-prod-app:<旧sha> roomusic-prod-tools:<旧sha>
```

### 日后增加公网入口

用户自行配置外部反代和 HTTPS 后，默认主机一致模式下，反代保留浏览器的 Host（含端口）与 Origin 即可直接用，不需要修改配置；
若主动使用清单模式，则把完整公网来源加入清单后 `make prod`。

局域网 IP 与公网域名分别使用自己的 Cookie。应用不信任 `X-Forwarded-*`；登录限速使用 TCP 对端来源，经反代的用户共享该来源。
不要把应用端口另行发布到 IPv6；局域网真实来源地址仍须在上线演练中从另一台设备实测。
本机 Docker 默认地址池可能覆盖 `192.168.50.0/24`；本配置使用固定子网，其他项目的地址池调整不在本文操作范围内。

## 备份

backup 服务以 uid 1000 运行，默认上海时间每天 3 点后备份，每 10 分钟检查一次。启动时若 24 小时内没有成功备份就立即补备份，
失败至少一小时后重试。`make prod` 的启动前备份算成功备份，不会紧接着重复。保留最近 14 天；连续失败不会清理旧备份。

```bash
make prod-backup
ls -l /home/qlqq/roomusic-backups
```

正常文件为 `roomusic-YYYYmmdd-HHMMSS.dump`，手动为 `roomusic-YYYYmmdd-HHMMSS-manual.dump`。
先写隐藏的 `.partial`，`pg_restore --list` 校验通过才原子发布；失败不会留下看起来完整的 `.dump`。
日志含 `backup.completed` 或 `backup.failed` 及阶段/退出码，不含密码。
`.last-success` 记录最近成功时间，超过 26 小时服务健康检查失败；请结合日志、状态与新文件判断故障。

只备份 PostgreSQL；`/data` 的派生缓存和封面不在其中，音乐文件本身也不备份。派生缓存可以重新生成；
**丢失 `/data` 后重扫能否补回全部封面，尚待上线演练确认，不应把未验证的重建能力当作备份保证。**
备份含账号等私有数据，默认权限 600，请妥善保管。只恢复可信来源的备份。

## 恢复到临时库核对（不改生产）

```bash
deploy/production/restore.sh check /home/qlqq/roomusic-backups/roomusic-<时间>-manual.dump
```

tools profile 启动无网络的一次性 PostgreSQL，数据放 4 GiB tmpfs，只读挂载备份副本，恢复后删除容器与临时文件。
副本位于宿主机权限 700 的临时目录中，文件临时设为 644 供容器 postgres 用户读取，原备份权限不变。
核对表含迁移最大版本/条数及 users、sessions、library_roots、releases、tracks、release_artworks 行数；空库未建表时显示“缺失”。
生产数据库运行时并排显示当前值；没有运行就只显示备份值。备份之后发生的登录、扫描或新操作可能让当前值略有不同，需要按时间判断。
库大于临时空间时核对会失败，不会切换到生产库。

## 恢复到生产库（覆盖现有数据）

先核对备份，并确认 postgres 容器运行。app 可以处于故障或停止状态。不要用这个脚本恢复开发项目。

```bash
deploy/production/restore.sh production /home/qlqq/roomusic-backups/roomusic-<时间>-manual.dump
```

脚本显示文件时间、大小，要求输入 `roomusic-prod` 确认，然后：

1. 对当前数据库立即备份；失败不继续。
2. 停 app 与 backup，保留 postgres。
3. 删除并重建 roomusic 数据库，恢复所选文件。
4. 成功才重新启动 backup、app，等健康并输出核对表。

恢复失败时应用与计划备份保持停止，脚本打印使用第 1 步安全备份再次恢复的完整命令。不要在失败后自行启动应用写入半恢复的库。
备份较旧时应用会补迁移；备份较新时旧应用可能拒绝启动，先 `make prod REF=<对应提交>` 再核对日志和状态。

演练使用独立配置文件、镜像、卷、端口和固定子网，恢复入口可通过 `COMPOSE_PROJECT_NAME=roomusic-drill`
与 `COMPOSE_ENV_FILES=<演练env绝对路径>` 指定演练项目，确认词也变为 `roomusic-drill`。开发项目 `roomusic` 被脚本拒绝。

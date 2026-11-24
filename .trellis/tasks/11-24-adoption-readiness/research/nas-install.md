# Research: 降低安装门槛（NAS 用户，国内与海外）

- **Query**：不依赖 sudo 与 Python 首次安装脚本；各 NAS 平台能否直接导入 Compose、host 网络、目录权限、第三方应用商店；最小交付物与无真机可验证项
- **Scope**：mixed（仓库内部 + 外部文档/社区实例）
- **Date**：2026-11-24
- **说明**：本机没有可用的网页搜索工具，外部资料来自直接抓取的官方文档页、GitHub 仓库与 issue（用 `gh api` 检索）。凡未在真机或官方文档上核实的条目都标了“未证实”。

---

## 1. 现状：首次安装脚本做了什么

### 相关文件

| 文件 | 作用 |
|---|---|
| `scripts/production-init.sh` | 首次安装脚本（sudo 运行） |
| `compose.prod.yaml` | 生产 Compose，app 走 host 网络，postgres 走项目 bridge |
| `.env.prod.example` | 部署用 `.env` 模板，必填只有 `ROOMUSIC_MUSIC_DIR` |
| `deploy/production/compose.bridge.yaml` | bridge 覆盖文件，用到 `!reset`（Compose ≥ 2.24.4） |
| `deploy/production/Dockerfile` | 应用镜像，`USER 1000:1000`（第 90 行），镜像内 `/data`、`/backups` 已属 1000（第 77–78 行） |
| `deploy/production/entrypoint.sh` | 等数据库接受连接后启动 |
| `backend/internal/platform/config/config.go:196-225` | `DatabaseURL()`：读 `ROOMUSIC_DB_PASSWORD_FILE`，要求 URL 有 host 和用户名 |
| `backend/internal/backup/pgtools.go:107-136` | 备份/恢复把 URL 交给 pg_dump，要求 `parsed.Host != ""`，查询参数只保留 sslmode 等白名单 |
| `scripts/tests/test_production_init.py`、`scripts/tests/test_production_deployment.py`、`scripts/check-production-compose.sh` | 现有的脚本单测、真实部署测试、Compose 静态校验 |
| `.trellis/spec/backend/deployment-environment-contracts.md:44-50` | 部署合同（禁止初始化容器、无数据卷、目录权限约定） |

### 脚本逐步拆解（`scripts/production-init.sh`）

| 行 | 动作 | 为什么要做 | 容器首次启动时能否自己做（不引入初始化容器） |
|---|---|---|---|
| 5–8 | 要求 root | 第 66 行要 `chown` | 不需要单独 root，见下文 |
| 13–37 | `docker compose config` 校验后再导出 JSON，**用 Python 3** 解析出 `ROOMUSIC_DB_PASSWORD` 和 `ROOMUSIC_BACKUP_DIR` | 读取 `.env` 时不执行其中文本 | 去掉脚本后整步都不需要 |
| 38 | `mkdir -p ./secrets ./data $backup_dir` | 建目录 | **能**。短写法 bind mount 的源路径不存在时，Docker daemon 会以 root 建出 `0755 root:root` 目录；问题只在属主（见第 66 行） |
| 40–42 | `./postgres` 不存在时以 `0755` 新建 | 脚本 `umask 077` 下 mkdir 会得到 0700，降权后的 postgres 进不去；Docker 自己建的就是 0755 | **能**，而且不需要任何动作。官方入口以 root 启动，`mkdir -p $PGDATA`、`chmod 700`、`find $PGDATA ! -user postgres -exec chown postgres`，再 `gosu postgres`（docker-library/postgres 18 alpine 入口第 39–59、343 行）。PG18 的 `PGDATA=/var/lib/postgresql/18/docker`，挂载点是父目录 `/var/lib/postgresql`（官方 Dockerfile 第 205–208 行） |
| 43–65 | 生成或复用 `secrets/postgres_password`（32 字节随机 hex）；密码文件缺失但数据库目录非空时停止 | 应用和数据库共用同一份密码文件 | **能，但要改 Compose 或代码**，有三种做法，见 §2 |
| 66–68 | `chown 1000:1000` secrets、密码文件、data、backups；`chmod 0700/0600` | 应用以 uid 1000 运行，`cap_drop: [ALL]`，写不了 root 建的目录 | **能，但要改镜像或 Compose**，见 §2 |

### 结论：真正需要宿主机侧处理的只有这些

1. **音乐目录对应用的 uid/gid 可读**。这由 NAS 共享文件夹权限决定，容器不能也不应该去改用户音乐的属主。各 NAS 默认首个用户的 uid 都不是 1000（群晖 1026:100、Unraid 99:100、TrueNAS 568:568，见 §3），所以写死 uid 1000 在 NAS 上读音乐最容易出问题。用户在 NAS 界面里设置权限即可，不需要 root shell。
2. **`ROOMUSIC_BACKUP_DIR` 指向已有目录，或者指向网络文件系统时**（NFS root_squash、SMB），容器里的 root 也 chown 不了，需要用户在 NAS 界面授权。
3. **能执行 Docker**。NAS 的 Docker 图形界面本身以 root 调用 Compose，这一条在 NAS 上不是用户负担。

除此以外，脚本的每一步要么由 Docker daemon 和 postgres 官方入口完成，要么可以挪进现有两个服务自身的启动过程。

---

## 2. 不用初始化容器时，可以把脚本步骤挪到哪里

项目规则：禁止初始化容器；Compose 不依赖宿主机脚本；升级只做 pull 加 up（`AGENTS.md`、规范第 45 行）。下面几种做法都只改现有 `app`、`postgres` 两个服务的启动方式，不新增服务。只列做法和影响，不替主代理选定。

### 2.1 数据库密码

| 做法 | 机制 | 影响 |
|---|---|---|
| A. postgres 服务启动时生成 | 覆盖 postgres 的 `entrypoint`/`command` 为一段 sh：`secrets/postgres_password` 不存在且数据目录为空时，从 `/dev/urandom` 生成并写入（chown 给应用 uid），数据目录非空但密码缺失时报错退出，然后 `exec docker-entrypoint.sh postgres`。postgres 容器启动时是 root，能写 Docker 建的 root 目录。app 本来就 `depends_on: service_healthy`，读文件时文件已存在 | postgres 挂 `./secrets` 要改为可写（app 仍只读）。Compose 里内联 shell 时 `$` 要写成 `$$`。逻辑与脚本第 43–65 行一致，原有测试可以照搬 |
| B. 不用密码，走 Unix socket | 两个容器 bind mount 同一个宿主机目录到 `/run/postgresql`。官方镜像 initdb 不带 `--auth`，`local` 连接是 trust；入口只为 `host` 行追加 scram（入口第 266–286 行）。socket 目录由入口 `chmod 03775`（第 44–45 行），socket 文件默认 0777，uid 1000 能连 | 不再需要密码文件和 `secrets/`，也不再占用回环端口 55439（同机多实例少一个要错开的端口）。但**要改代码**：`config.DatabaseURL()` 要求 `parsed.Host != ""`（config.go:208），备份 `connectionArguments` 同样要求 host 且丢掉 `host=` 查询参数（pgtools.go:109、124-128）。另外 bind 目录里放 socket 在 Unraid `/mnt/user`（FUSE）、macOS Docker Desktop 上是否可用**未证实** |
| C. 用户在 Compose 里写密码 | `POSTGRES_PASSWORD` 加上应用 URL 里带密码，或者两边都用 `.env` 里的变量 | 最简单，但密码明文出现在 NAS 的 YAML 编辑框里。不能读 `.env` 的平台（QNAP、TrueNAS、可能还有 UGOS）只能写成字面值 |

### 2.2 `data/`、`backups/` 的写权限

| 做法 | 机制 | 影响 |
|---|---|---|
| A. 应用镜像以 root 启动，修正属主后降权 | 与 postgres 官方入口相同：入口以 root 检查 `/data`、`/backups` 顶层属主，不对就 chown，然后用 su-exec/setpriv 切换到 `PUID:PGID`（默认 1000）。`no-new-privileges` 不影响降权 | `cap_drop: [ALL]` 要改为至少 `cap_add: [CHOWN, SETUID, SETGID]`，可能还要 `DAC_OVERRIDE`/`FOWNER`；镜像要去掉 `USER 1000:1000` 或改入口。`docker compose exec app ...` 的命令（备份、重置密码）的执行身份要一并考虑 |
| B. 支持 `user: "${PUID}:${PGID}"`，目录由用户在 NAS 界面预先建好 | NAS 文件管理器建出的目录属于该 NAS 用户，应用以同一 uid 运行就能读写，也能读该用户可见的音乐。postgres 也支持任意 `--user`（入口第 80 行有 nss_wrapper 分支），但此时父目录不能是 root 建的 | 这是 NAS 生态最常见的约定（linuxserver 系 PUID/PGID）。缺点：目录必须事先存在且属主正确，忘建时 Docker 会建出 root 目录，又写不进去 |
| C. 命名卷 | 新命名卷首次挂载时会复制镜像里 `/data` 的属主（镜像里已是 1000） | **违反现有规则**（规范第 46 行：不创建命名或匿名卷）。群晖上 postgres 放命名卷能避开共享文件夹 ACL 的 chmod 问题（见 §3.1），但数据不在用户可见的文件夹里 |

A 和 B 可以同时做：默认走 A，NAS 用户可以通过 `PUID`/`PGID` 指定身份，读音乐也更顺。

---

## 3. 各 NAS 平台

### 总表

| 平台 | 导入 Compose | 读同目录 `.env` | host 网络 | 默认用户 uid:gid | 第三方商店 / 模板 |
|---|---|---|---|---|---|
| 群晖 DSM 7.2+ Container Manager | 能，“项目”里选文件夹或粘贴 | **能**（Immich 官方指南做法） | 能 | 1026:100（首个用户:users） | 没有 Docker 应用商店，只能写文档 |
| 绿联 UGOS Pro | 能，Docker → 项目 → 创建，粘贴 | **可能不读**（未证实） | 能（社区 compose 在用，未证实） | 1000:10（admin 组） | 未找到第三方上架途径 |
| 极空间 ZOS | 容器图形向导有 host/bridge；Compose 功能**未证实** | 未证实 | 能（向导里有 host） | 未证实 | 未找到公开途径 |
| 飞牛 fnOS | 能，Docker → Compose → 新增项目（名称 + 路径）→ 构建 | 未证实 | 能（社区实例） | 1000:?（路径形如 `/vol1/1000/...`，gid 未证实） | **有**：官方 fpk（`fnpack`，Docker 模板）；社区 fpk 商店 `conversun/fnos-apps` |
| Unraid 6.12 / 7.x | 原生没有 Compose；Docker Compose Manager 插件；CA 已有 Compose 字段 | 插件用自己的栈目录 | 能（模板 `<Network>host`） | 99:100（nobody:users） | **有**：Community Applications，`ca.unraid.net/submit`，可自动通过 |
| TrueNAS SCALE 24.10+（CE） | 能，Apps → Discover → ⋮ → Install via YAML | **不能**（只有一个 YAML 文本框） | 能 | 568:568（apps） | **有**：`truenas/apps` community train（提 PR） |
| QNAP Container Station 3 | 能，Applications → Create，粘贴 YAML | **不能**（Immich 官方 QNAP 指南） | 能（社区实例） | 未证实（admin 账户需启用） | QPKG 另一套，没有 Docker 应用商店 |

### 3.1 群晖 DSM 7.2+ Container Manager

- **导入方式**：Container Manager → 项目 → 新增 → 选择项目路径（如 `/volume1/docker/roomusic`）；路径里已有 `docker-compose.yml` 时提示直接使用，然后构建并启动。`.env` 先用 File Station 上传到同一文件夹再改名；群晖文本编辑器打不开点开头的文件，要先改成 `.txt` 编辑再改回。来源：[Immich Synology 指南](https://github.com/immich-app/immich/blob/main/docs/docs/install/synology.md)。
- **版本**：Container Manager 24.0.2 系列，Docker Engine 24.0.2（API 1.43），**Compose v2.20.1**（`2.20.1-6047-g6817716`）。24.0.2-1543 起可以在命令行用 `docker compose`；24.0.2-1535 起默认镜像源里去掉了 Aliyun Hub；最新 24.0.2-1706/1707 要求 DSM 7.4。来源：[Container Manager 发行说明](https://www.synology.com/en-global/releaseNote/ContainerManager)，[DS216+II 实测记录](https://github.com/Paisans-Software/paisans-cli/issues/350)，[Compose 2.20 过旧导致失败的实例](https://github.com/selfhostit/calio/pull/9)，[Docker API 1.43 限制](https://github.com/beatkind/watchtower/issues/132)。
  - 对本项目：`compose.prod.yaml` 用到的特性（`name:`、`x-` 锚点、`depends_on.restart`（2.17+）、`bind.create_host_path`）在 2.20.1 上都有；`compose.bridge.yaml` 的 `!reset` 需要 2.24.4，**群晖上不能用 bridge 覆盖文件**。镜像 HEALTHCHECK 的 `--start-interval` 需要 Engine 25，Engine 24 会忽略（Immich Unraid 文档也提到 Engine 24 不支持 `start_interval`）。
  - 同一份实测：`bind.create_host_path: false` 在源路径不存在时拒绝启动，加密共享文件夹锁定时不会写进空目录（与本项目音乐挂载的写法一致）。
- **端口**：DSM 自带 nginx 监听所有地址的 80/443/5000/5001；**DSM 自己的 PostgreSQL 监听 `127.0.0.1:5432`**（同一实测）。本项目的 8090 和 55439 不冲突，但不能把 `ROOMUSIC_DB_PORT` 改成 5432。
- **内核**：较老机型仍是 3.10.108（DS216+II、DSM 7.4.1），常见 4.4/5.10。PG18 与 Alpine 3.23 在 3.10 内核上能否运行**未证实**。
- **架构**：Container Manager 支持 x86 和部分 ARMv8（20.10.23-1437 起加入 DS220j、DS120j），ARMv7 机型不支持。DS220j 只有 512 MB 内存，跑 PG + 应用 + ffmpeg 很紧（未实测）。
- **权限**：首个 DSM 用户 uid 1026，users 组 gid 100（[aimm 群晖说明](https://github.com/BoPeng/ai-marketplace-monitor/pull/427)，[InvoiceShelf 实例](https://github.com/InvoiceShelf/InvoiceShelf/issues/642)）。共享文件夹默认启用 Windows ACL，容器内 chown/chmod 常报 `Operation not permitted`（InvoiceShelf 同一 issue）。postgres 数据目录放在 `/volume1/docker/...` 下时，initdb 的 `chmod` 失败，见 [VWsFriend #736](https://github.com/tillsteinbach/VWsFriend/issues/736)（PG 旧版本、挂载在 `.../data`）。PG18 的 PGDATA 是挂载点下由入口新建的子目录，是否同样会遇到 ACL 问题**未证实，需要真机**。
- **防火墙**：开启 DSM 防火墙时要放行应用端口；bridge 模式下容器网段访问也可能需要规则（Immich 指南第 4 步）。
- **商店**：Container Manager 没有第三方 Docker 应用商店。套件中心第三方源（SynoCommunity）是 SPK 原生包，不是 Docker（aimm #417 的结论也是“群晖、QNAP 只能写文档”）。

### 3.2 绿联 UGOS Pro

- **导入方式**：Docker 应用 → 项目 → 创建，粘贴整份 Compose（[vantage #134](https://github.com/levi-xy/vantage/pull/134)、[navidrome-nas #1](https://github.com/chalvis05/navidrome-nas/pull/1)）。
- **`.env`**：vantage 认为“项目框可能不读 `.env`”，因此不用 `${...}` 变量（**未证实**）。
- **限制**：UGOS Docker 应用**拒绝挂载 `/`**（vantage #134）。`network_mode: host` 被社区文件采用，但作者注明未在真机上验证。
- **权限**：NAS 用户 `PUID=1000 / PGID=10`（[solverr #38](https://github.com/rpeters1430/solverr/pull/38) 按此身份测试；[hermes-agent #15290](https://github.com/NousResearch/hermes-agent/issues/15290) 用户自述）。hermes 的 issue 里还有一个现象：容器以 root 运行时，新写的文件随后也读不了，怀疑 UGOS 的 ACL 与原子写入（rename）有冲突，**未定论**。
- **端口**：UGOS 网页 9999（HTTP）/9443（HTTPS）（[nas-homelab #24](https://github.com/Achar0/nas-homelab/pull/24)），与本项目不冲突。
- **商店**：未找到面向第三方开发者的上架途径。
- **Docker/Compose 版本**：未查到。

### 3.3 极空间 ZOS

- **图形向导**：Docker → 镜像 → 仓库 → “自定义拉取”（国内教程用 `m.daocloud.io/docker.io/...` 前缀）→ 本地镜像 → 添加到容器；创建页有“文件夹路径”“端口”等标签，网络可选桥接或 host。来源：[xiaomusic 极空间安装教程](https://github.com/hanxi/xiaomusic/issues/297)。同一教程注明 Z2 Pro 是 ARM 机型，镜像要支持 arm64。
- **Compose**：没有找到可靠来源确认 ZOS Docker 能导入 Compose（**未证实**）。如果只能用图形向导，两个容器就要分别创建，并手工填写网络和环境变量。
- **路径**：社区反馈“添加目录一直失败”（[JavBoss #203](https://github.com/Solr159/JavBoss/issues/203)），说明在极空间上，图形向导里的目录映射本身就是用户的一个难点。
- **端口**：网页默认 5055（[frpc 教程](https://github.com/u0w0n/u0w0n.github.io/issues/6)提到）。
- **常见冲突**：同类国内音乐项目 xiaomusic 的默认端口也是 **8090**（[xiaomusic 文档汇总](https://github.com/hanxi/xiaomusic/issues/211)），在国内 NAS 用户中可能撞端口。
- **商店**：未找到公开的第三方上架途径。

### 3.4 飞牛 fnOS

- **Compose 项目**：Docker → Compose → 创建项目（名称、路径任意，建议单独建文件夹）→ 粘贴 `docker-compose.yml` → 构建。社区实例使用 `network_mode: host`、ghcr 镜像和 `/vol1/1000/...` 路径。来源：[rtp2httpd fnOS 教程](https://github.com/stackia/rtp2httpd/issues/249)。
- **官方应用包（fpk）**：[飞牛应用开放平台](https://developer.fnnas.com/)。
  - `fnpack create <app> --template docker` 会生成 `app/docker/docker-compose.yaml`、`manifest`、`config/privilege`、`config/resource`、`cmd/` 生命周期脚本和 `wizard/` 表单（[fnpack](https://developer.fnnas.com/docs/cli/fnpack/)，[Docker 应用案例](https://developer.fnnas.com/docs/examples/docker/)）。
  - Compose 里可以用 `TRIM_SERVICE_PORT`、`TRIM_APPDEST`、`TRIM_PKGVAR`、`TRIM_DATA_SHARE_PATHS` 等变量；`config/resource` 的 `docker-project` 声明 Compose 项目（[应用资源](https://developer.fnnas.com/docs/core-concepts/resource/)）。
  - 安装后数据在 `/vol{n}/@appdata/{appname}` 等位置（[应用框架](https://developer.fnnas.com/docs/core-concepts/framework/)）。`data-share` 声明的共享目录使用 **Windows ACL 模型**，系统会自动给应用用户授权；用户其他目录要在“应用设置 → 访问权限”里授权（TrimACL，见 [fnos-apps #347](https://github.com/conversun/fnos-apps/issues/347)）。
  - 生命周期脚本（`install_init`/`install_callback` 等）可以选 root 模式执行特权准备（[应用权限](https://developer.fnnas.com/docs/core-concepts/privilege/)），也就是说，生成密码、建目录可以交给飞牛的安装流程，不需要初始化容器。
  - **上架**：开发者后台上线前，通过“应用中心开发者先锋交流群”（微信群）提交 fpk、图标、截图（[上架应用](https://developer.fnnas.com/docs/quick-started/publish-application/)）。
  - 社区第三方 fpk 商店：[conversun/fnos-apps](https://github.com/conversun/fnos-apps)（约 970 星，175 款应用，接受 App Request）。
- **端口**：fnOS 网页 5666/5667（[fnmedia-ext #101](https://github.com/yujian0815/fnmedia-ext/issues/101)）。
- **未证实**：fnOS Compose 项目是否读 `.env`；Docker/Compose 版本；默认用户 gid。

### 3.5 Unraid

- **模板（单容器）**：Community Applications 模板是 `<Container version="2">` XML，必填 `<Name>`、`<Repository>`，可以设 `<Network>host</Network>`、`<ExtraParams>`（CA 解析器会检查不安全的值），`<Config>` 描述路径、端口和变量。提交：仓库公开、根目录有 OSI 许可证、`ca_profile.xml`，在 [ca.unraid.net/submit](https://ca.unraid.net/submit) 运行 Validate 和 Scan。来源：[Submission Help](https://ca.unraid.net/submit/help)，[Repository XML](https://ca.unraid.net/submit/help/repository-xml)，[起步仓库](https://github.com/unraid/unraid-community-apps-starter)。实例：aimm 2026-10-09 提交后自动通过，约两天进入公开源（[aimm #417](https://github.com/BoPeng/ai-marketplace-monitor/issues/417)）。
- **Compose 包**：CA 字段表已有 `<AppTemplateType>compose`、`<ComposeProjectPath>`、`<ComposeSourceRepository>`、`<ComposeSourceCommit>`（[XML 字段参考](https://ca.unraid.net/submit/help/xml-field-reference)）。帮助索引称这是为 Unraid 8 准备的“不可变 Compose 归档”，但该说明页目前 404，**细节未证实**。
- **现有做法**：Unraid 6.12/7.x 原生没有 Compose，Immich 文档用 [Docker Compose Manager 插件](https://forums.unraid.net/topic/114415-plugin-docker-compose-manager/)。CA 模板路线要求用户先另装一个 PostgreSQL 容器，再在应用模板里填写连接信息（[Immich Unraid 指南](https://github.com/immich-app/immich/blob/main/docs/docs/install/unraid.md)）。Unraid 6.12.10 自带 Docker 24.0.9。
- **权限与路径**：`PUID=99 / PGID=100`，应用数据放 `/mnt/user/appdata/<app>`（aimm 模板，[Unraid 文档](https://docs.unraid.net/unraid-os/using-unraid-to/run-docker-containers/managing-and-customizing-containers/)）。数据库放 `/mnt/user`（FUSE 合并层）与放缓存盘或独占共享，性能和兼容性的差别**未在本次核实**。
- **对本项目**：两个容器意味着 CA 要发两个模板，或者让用户使用现成的 postgres 模板，或者等 Compose 包。单容器模板拿不到“postgres 首启生成密码文件”的效果，密码只能由用户在两个模板里填同一个值（§2.1 C）。

### 3.6 TrueNAS SCALE / Community Edition（24.10 Electric Eel 起基于 Docker）

- **自定义 Compose**：Apps → Discover Apps → ⋮ → Install via YAML，粘贴 Compose（可以用 `name:`、`services:`、`include:` 开头）。没有 `.env`；目录要事先用数据集或 `storage filesystem mkdir` 建好；自定义向导里可以选 Host Network，默认运行身份是 568:568（apps）。来源：[Installing Custom Apps](https://apps.truenas.com/managing-apps/installing-custom-apps/)。
- **ACL**：SMB/NFSv4 ACL 数据集上，容器内 chmod 需要把 ACL Mode 设为 Passthrough（[Immich TrueNAS 指南](https://github.com/immich-app/immich/blob/main/docs/docs/install/truenas.md)）。TrueNAS 文档按 Debian 镜像写“postgres 卷默认 uid 999”；本项目用的是 **Alpine 镜像，postgres uid 是 70**（官方 Dockerfile 第 13–14 行），由入口 chown，文档里的 999 不适用。
- **商店**：PR 到 [truenas/apps](https://github.com/truenas/apps) 的 `ix-dev/community/`（[CONTRIBUTIONS.md](https://github.com/truenas/apps/blob/master/CONTRIBUTIONS.md)）。按要求先提 new-app issue；默认端口要全目录唯一（用脚本分配，30xxx）；用他们的 Jinja 库生成，库自带 `deps_postgres` 和**权限初始化容器**（`tpl.deps.perms()`）。这是 TrueNAS 框架自身的机制，与本项目“禁止初始化容器”的规则如何协调需要决定。
- **可在虚拟机测试**：aimm 在 Proxmox 虚拟机里的 TrueNAS CE 上用 Install via YAML 实测通过（aimm #417）。

### 3.7 QNAP Container Station 3

- **导入方式**：Applications → Create，粘贴 YAML。YAML 保存在 Container Station 自己的应用目录，**不处理 `env_file`，也不读旁边的 `.env`**，变量要内联进 YAML；相对路径 `./data` 会解析到那个应用目录。建议写 `/share/Container/...` 绝对路径（[Immich QNAP 指南](https://github.com/immich-app/immich/blob/main/docs/docs/install/qnap.md)，[aimm #427](https://github.com/BoPeng/ai-marketplace-monitor/pull/427)）。
- **前提**：默认 admin 账户必须启用才能运行容器；使用其他共享文件夹时要开启“高级文件夹权限”并给 admin 读写（Immich QNAP 指南）。
- **校验器**：有一个社区实例为了兼容“更严格的 Container Station 校验器”刻意不用 `x-*` 锚点（[hotpot #4](https://github.com/dwightmulcahy/hotpot/pull/4)，**未证实**）。本项目 `compose.prod.yaml` 第 6–7 行用了 `x-postgres-image`、`x-logging`。
- **端口**：QTS 管理界面默认 8080/443（通用常识，本次未抓到官方页面）。本项目 host 模式下应用在 8090，bridge 模式下容器内 8080 不占宿主机端口，均不冲突。
- **商店**：没有 Docker 应用商店（aimm #417 结论）。

### 3.8 其他平台（与目标用户相关）

| 平台 | 途径 | 备注 |
|---|---|---|
| 1Panel（国内 VPS/NAS 常用） | PR 到 [1Panel-dev/appstore](https://github.com/1Panel-dev/appstore)（[提交说明](https://github.com/1Panel-dev/appstore/wiki/How-to-submit-your-own-application)） | 收录标准是知名、活跃、装机量大、有官方镜像，新项目未必能被接受 |
| CasaOS / ZimaOS | PR 到 [IceWhaleTech/CasaOS-AppStore](https://github.com/IceWhaleTech/CasaOS-AppStore) | Compose 加 `x-casaos` 元数据，支持 `$PUID/$PGID/$AppID` |
| Umbrel、Cosmos、Portainer 模板 | 各自仓库提 PR | aimm #418 给出了全套文件，可作参考 |

---

## 4. 中国大陆拉取镜像

- 应用镜像只发布在 `ghcr.io/qlqqs/roomusic`（`.github/workflows/release.yml:16`），postgres 来自 Docker Hub（`postgres:18.1-alpine3.23@sha256:...`）。
- NAS 镜像加速一般配置为 `registry-mirrors`，**只对 docker.io 生效**；DaoCloud 明确说明“不要把 docker.io 之外的站点配置给 docker 的 registry-mirrors”（[DaoCloud public-image-mirror README](https://github.com/DaoCloud/public-image-mirror)）。因此 ghcr 镜像在国内 NAS 上大概率走不了加速。
- DaoCloud：`ghcr.io` 对应 `ghcr.m.daocloud.io`，但自 2024-06-18 起实行白名单，ghcr 镜像要逐个申请；`docker.io/*`（官方 library 单层镜像，包括 postgres）已在白名单。有 IP 限流，只服务中国大陆（[白名单与限流说明 #2328](https://github.com/DaoCloud/public-image-mirror/issues/2328)，[allows.txt](https://github.com/DaoCloud/public-image-mirror/blob/main/allows.txt)）。DaoCloud 同时建议作者把镜像放到阿里云或华为云镜像仓库。
- NAS 图形界面里的“搜索镜像”通常搜的是 Docker Hub。aimm 为此把镜像同步到 Docker Hub（[aimm #418/#417](https://github.com/BoPeng/ai-marketplace-monitor/issues/417)）。
- postgres 按 digest 固定，经镜像站拉取时 digest 不变；如果在 Compose 里给 postgres 加镜像站前缀，CI 的“三处镜像字符串一致”校验（规范第 41 行）要一起考虑。

---

## 5. 最小交付物建议（按照主代理的要求列出）

1. **一份不依赖脚本的 Compose**（可以就是 `compose.prod.yaml` 本身）：
   - 密码：§2.1 A（postgres 服务首启生成）或 B（socket，无密码，要改代码），二选一。
   - 目录写权限：§2.2 A，可以叠加 B 的 `PUID/PGID`。
   - 用 Docker daemon 自动建 `./postgres`、`./data`、`./backups`、`./secrets`，首次安装只剩“填音乐目录 → up”。
   - 保留 `bind.create_host_path: false`，只适用于音乐目录。
   - 继续兼容 Compose 2.20.1（群晖），不要依赖 `!reset`、`configs.content`（2.23.1）、`start_interval`（Engine 25）。
2. **面向“不能读 `.env`”平台的单文件版本**（QNAP、TrueNAS、可能还有 UGOS、fnOS）：变量全部写成字面默认值，路径用绝对路径占位（如 `/share/Container/roomusic/...`、`/mnt/<pool>/roomusic/...`），不使用 `${VAR:?}`；`x-` 锚点是否展开取决于 QNAP 的实测结果。可以由主文件派生，在 CI 里用 `docker compose config` 比较两者展开后是否等价。
3. **分平台图文说明**（截图位先留占位）：群晖、绿联、飞牛、极空间、QNAP、TrueNAS、Unraid 各一节。每节写明：去哪里建文件夹、给哪个用户什么权限、在哪里粘贴 YAML、端口与防火墙、怎样更新（停止 → 拉取 → 构建）、默认 uid/gid、国内拉取镜像的方法。
4. **Unraid CA 模板**：`roomusic.xml` 加根目录 `ca_profile.xml`。数据库可以另发一个 postgres 模板，也可以引导用户使用现成模板，或者等 CA 的 Compose 包（`AppTemplateType=compose`）。
5. **可选**：fnOS fpk（Docker 模板，用 `install_callback` 准备目录和密码）、TrueNAS community train PR、Docker Hub 镜像副本，以及阿里云/华为云镜像仓库或 DaoCloud 白名单申请。

## 6. 没有真机时能验证什么

| 项 | 做法 |
|---|---|
| 群晖的 Compose/Engine 版本兼容 | 用 Compose v2.20.1 二进制（[release](https://github.com/docker/compose/releases/tag/v2.20.1)）跑 `config`；用 `docker:24.0.2-dind` 起一个 Engine 24 跑完整的 `up --wait` |
| 首次安装不需要脚本 | 扩展 `scripts/tests/test_production_deployment.py`：空目录直接 `up`，覆盖首次生成、重建后复用、数据库非空但缺密码时拒绝、升级 pull + up |
| 各平台的 uid/gid 与目录状态 | 在临时目录模拟：Docker 自动建的 root 目录、属主 1026:100 / 1000:10 / 99:100 / 568:568 的目录、带 `setfacl` 默认 ACL 的目录、只读音乐目录；分别以对应的 `PUID/PGID` 启动 |
| 不读 `.env` 的单文件版本 | `env -i docker compose -f single.yaml config`（不带 env 文件），与主文件展开结果比较 |
| Unraid 模板 | XML 解析；在 CA 提交流程里 Validate + Scan；在本机用 `docker run` 复现模板参数 |
| TrueNAS | 免费的 TrueNAS CE 装进 QEMU/Proxmox 虚拟机，测 Install via YAML（aimm 的做法） |
| 飞牛 | fnOS 免费提供 x86 安装镜像，可以装进虚拟机；`fnpack` 在本机打包 |
| CasaOS / Umbrel | `dockur/casa`、`dockur/umbrel` 在容器里运行这两个系统（aimm 的做法） |
| 群晖 DSM | `vdsm/virtual-dsm` 能在容器里跑 DSM，但其 README 写明 **Synology EULA 只允许在群晖硬件上运行**，不适合在非群晖机器上测试 |
| 绿联、极空间、QNAP | 没有可用的虚拟机或容器版（QNAP 的 QuTScloud 收费），只能找有设备的用户验证 |
| 端口冲突 | 静态检查：默认端口不落在 80/443/5000/5001/5432/5055/5666/5667/8080/9999/9443 |

## Caveats / Not Found

- **未证实（需真机）**：UGOS、fnOS 的 Compose 项目是否读 `.env`；极空间 ZOS 是否支持导入 Compose；UGOS、fnOS、极空间的 Docker/Compose 版本；fnOS 默认用户 gid；QNAP 默认用户 uid；QNAP 是否拒绝 `x-` 锚点；群晖共享文件夹 ACL 下 PG18 子目录 PGDATA 是否会 chmod 失败；PG18/Alpine 3.23 能否在 DSM 3.10 内核上运行；在 bind 目录里放 Unix socket 在 Unraid FUSE 上是否可用；NAS 自带的 DLNA/SSDP 服务与应用的 1900 组播监听能否共存（`backend/internal/devices/upnp/ssdp.go:151` 用 `ListenMulticastUDP`）。
- 群晖知识中心页面要靠 JS 渲染，本次抓不到正文，群晖的端口和项目说明改用社区实测与 Immich 官方指南作为来源。
- Unraid CA 的 Compose 包说明页（`/submit/help/compose-packages`）返回 404，只确认了字段存在。
- 绿联、极空间没有找到第三方上架的公开途径，不代表没有。
- §2 里的各种做法都会触及现有合同（规范第 44–50 行的权限、`cap_drop`、密码文件约定）以及 `scripts/check-production-compose.sh`、`scripts/tests/test_production_init.py` 的断言，改动时需要同步。

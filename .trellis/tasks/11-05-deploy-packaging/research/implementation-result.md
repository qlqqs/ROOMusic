# 第 1 步实施与验证记录（2026-11-05）

## 边界与结论

已实现部署打包文件，未提交 Git，未启动生产或演练实例，未运行开发脚本。
没有访问开发数据库，没有操作 `roomusic` Compose 项目或其容器、卷，也没有创建 Docker 网络。
没有创建 `.env.prod`，没有读取或打印 `.env`、`.env.dev` 的值，没有改动它们。
本 agent 未编辑 backend、frontend、内嵌 web 或 `identity-contracts.md`，这些路径的工作区改动属于并行 agent。

应用源码固定来自 `db4b4f32f04d8523a2dded9c7448491ca4ba0b9d`。
该提交尚无部署文件，经主会话明确授权：本步仅在临时 Git 导出树中覆盖本次 `deploy/production/`，使用临时镜像标签构建验证。
正式 `production.sh` 不包含此覆盖逻辑，仍只用目标提交中的部署文件。

## 文件清单

| 状态 | 文件 | 说明 |
| --- | --- | --- |
| 新增 | `deploy/production/Dockerfile` | 固定基础镜像摘要，提供 app/tools 多阶段目标，内嵌 ffmpeg，应用 uid 1000 与 readyz 健康检查。 |
| 新增 | `deploy/production/Dockerfile.dockerignore` | 白名单放行 backend 与三个运行脚本，阻止其他目录进入构建上下文。 |
| 新增 | `deploy/production/entrypoint.sh` | 等 PostgreSQL 接受连接后 exec 应用，等待日志不含连接串。 |
| 新增 | `deploy/production/compose.yaml` | 独立项目、IPv4 端口、固定子网、独立卷、只读音乐、数据库/备份服务与隔离恢复 profile。 |
| 新增 | `deploy/production/backup.sh` | 每日轮询、立即备份、健康检查、原子发布、失败退避、成功后清理保留期。 |
| 新增 | `deploy/production/restore.sh` | 临时恢复核对、确认后恢复生产、安全备份、失败停机与完整恢复提示。 |
| 新增 | `deploy/production/README.md` | 简体中文首次部署、升级回退、备份恢复、命令与风险说明。 |
| 新增 | `.env.prod.example` | 可通过 Compose 配置校验的生产模板，每项有说明，无真实密钥。 |
| 新增 | `scripts/production.sh` | 提交导出构建、版本标签校验、备份后启动及 stop/status/logs/backup/reset-password 命令。 |
| 修改 | `Makefile` | 增加生产运维目标，prod 改走新脚本。 |
| 修改 | `.gitignore` | 放行 `.env.prod.example`，实际 `.env.prod` 仍忽略。 |
| 修改 | `.mise.toml` | 开发 env-* 命令显式读取 `.env.dev`。 |
| 修改 | `scripts/dev.sh` | 删除根目录 `.env` 的二次 source 与专用保存/恢复变量，其余行为不动。 |
| 修改 | `scripts/dev-reset.sh` | 默认读取 `.env.dev`，保留 shell 环境优先与生产拒绝规则。 |
| 修改 | `scripts/reset-password.sh` | 默认读取 `.env.dev`，拒绝生产配置并提示容器运维命令。 |
| 修改 | `.env.example` | 移除生产模板“待新增”的过时说明。 |
| 修改 | `scripts/ffmpeg-pinned.sh` | 注释补充生产 Dockerfile 的摘要同步要求。 |
| 修改 | `.github/workflows/ci.yml` | 加生产 Compose、逐脚本语法和 ffmpeg/Go/PostgreSQL 固定版本一致性检查，不构建镜像。 |
| 修改 | `README.md` | 重写开发/生产两套配置、命令、密码恢复与 CI 说明。 |
| 修改 | `.trellis/spec/backend/core0-runtime-contracts.md` | 更新生产命令、部署/配置/备份边界、错误行为与最小验证合同。 |
| 删除 | `scripts/prod.sh` | 删除旧 go run 生产入口。 |
| 新增 | 本目录 `check-packaging.py` | 可复跑的安全局部检查，使用临时目录和数据库命令替身，不启动容器。 |
| 新增 | 本文件 | 记录本步验证、例外和待演练事项。 |

## 验证命令与结果

所有命令从仓库根目录执行。下面记录本步实际执行的门禁，不代表演练已经通过。

### 静态与安全局部检查

```bash
python3 .trellis/tasks/11-05-deploy-packaging/research/check-packaging.py
for script in scripts/*.sh deploy/production/*.sh; do bash -n "$script"; done
for script in deploy/production/*.sh; do busybox sh -n "$script"; done
scripts/production.sh --help
deploy/production/restore.sh --help
make -n prod prod-stop prod-status prod-logs prod-backup prod-reset-password U=example
```

结果：通过。逐个检查了 14 个 shell 脚本；`shellcheck` 未安装，明确跳过，没有将其记为通过。
开发脚本只做语法检查与 diff 审阅，未执行。

`check-packaging.py` 在自动清理的临时目录内覆盖了：

- 临时 env 文件上的 `ROOMUSIC_PROD_ENV_FILE=<临时文件> scripts/production.sh --dry-run stop`，打印 `-p roomusic-prod … stop`，无 down/-v。
- 故意继承开发项目名和错误端口，确认生产脚本清理这些变量，使用文件配置。
- 缺少配置、非法端口、空用户名、配置中的命令替换文本：正确退出且不执行文本。
- BusyBox 下的备份成功与 600 权限；pg_dump/pg_restore-list 失败、错误脱敏、清理 partial、不更新成功时间、不清旧备份。
- 连续备份不覆盖，成功后清理过期文件，26 小时健康阈值，非法时间/保留天数，loop 启动补备份和 TERM 退出。
- 入口先等数据库后 exec、参数透传与连接串脱敏；恢复脚本帮助和拒绝开发项目。

另用临时 Python 检查了恢复失败提示的 shell 参数引用，含空格、单引号、命令替换字面量，结果通过。
这些替身检查不代替 PostgreSQL 恢复或真实调度演练。

### Compose、CI 新增检查与文件规则

```bash
docker compose -f deploy/production/compose.yaml --env-file .env.prod.example config --quiet
grep -qF "$(scripts/ffmpeg-pinned.sh --image)" deploy/production/Dockerfile
grep -qF "golang:$(sed -n 's/^go = "\(.*\)"/\1/p' .mise.toml)-" deploy/production/Dockerfile
grep -qF "$(sed -n 's/^x-postgres-image: &postgres-image //p' deploy/production/compose.yaml) AS tools" deploy/production/Dockerfile
git check-ignore -q .env.prod && ! git check-ignore -q .env.prod.example
test ! -e scripts/prod.sh
git diff --check -- .env.example .github/workflows/ci.yml .gitignore .mise.toml Makefile README.md scripts .trellis/spec/backend/core0-runtime-contracts.md
```

结果：全部通过；CI 的新增检查已在本地等价执行。
另读取 `docker compose … config --format json` 的内存输出核对项目名、IPv4 8090、数据库无端口、只读挂载、禁止自动建目录、
backup uid 1000、固定子网、重启策略和独立卷，结果通过，未打印配置值。
初次辅助断言把 Compose 省略的默认 false 字段当成必有字段而失败，按归一化输出修正断言后通过；部署配置未因此改变。

### 固定镜像摘要

实际执行 `docker buildx imagetools inspect <以下标签>`，标签均存在：

| 标签 | 固定摘要 |
| --- | --- |
| `golang:1.25.10-alpine3.22` | `sha256:26b4d7113039cd51356bd7930ecafd1031d2975dc3b6940ec8ed09457e17cf95` |
| `alpine:3.22` | `sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f` |
| `postgres:18.1-alpine3.23` | `sha256:aa6eb304ddb6dd26df23d05db4e5cb05af8951cda3e0dc57731b771e0ef4ab29` |

ffmpeg 摘要保持与 `scripts/ffmpeg-pinned.sh --image` 完全相同。

### 导出树构建（只构建）

```bash
verify=$(mktemp -d /tmp/roomusic-verify.XXXXXX)
git archive --format=tar db4b4f3 | tar -x -C "$verify"
mkdir -p "$verify/deploy/production"
cp -a deploy/production/. "$verify/deploy/production/"
# 先确认两个临时标签原本不存在，避免覆盖他人镜像。
docker build --progress=plain \
  --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY \
  --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy \
  -f "$verify/deploy/production/Dockerfile" --target tools -t roomusic-verify:tools "$verify"
docker build --no-cache --progress=plain \
  --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY \
  --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy \
  -f "$verify/deploy/production/Dockerfile" --target app -t roomusic-verify:app "$verify"
docker image inspect -f '{{join .RepoTags ","}} {{.Size}}' roomusic-verify:app roomusic-verify:tools
```

最终两次构建成功，使用默认构建网络，无业务容器运行。app 构建上下文实际为 **14.49 MB**（设计白名单包含整个 backend 与内嵌前端），tools 为 **10.43 kB**。

| 临时标签 | 大小（字节） | 大小（MiB） |
| --- | ---: | ---: |
| `roomusic-verify:app` | 71,646,440 | 68.33 |
| `roomusic-verify:tools` | 111,970,333 | 106.78 |

只读 inspect 核对 app 的 `Config.User=1000:1000` 和 `/readyz` 健康检查配置，通过。
使用 `docker image save -o "$verify/tools-image.tar" roomusic-verify:tools`，以 Python 只读解析镜像层，确认：

- 存在有效 `Asia/Shanghai` 时区数据（TZif），不必更换 `CST-8`。
- 镜像里的 backup.sh、restore.sh 与最终工作区文件字节一致。

初次辅助解析用流式 tar 不能解引用时区硬链接；改为可寻址读取后通过，没有启动容器。
`TZ=Asia/Shanghai date` 的真实容器运行检查仍留给演练。

构建重试记录：首次未传代理时 tools 成功，app 的 `go mod download` 访问 proxy.golang.org 超时；
传入已有代理并使用构建期 `--network host` 后两个镜像成功。随后在正式脚本中只增加预定义代理参数的按名透传，
用**默认构建网络且 app --no-cache**再次完整构建成功。最终脚本没有 `--network host`，没有新建 Docker 网络或设置新代理。

### 清理

```bash
docker image rm roomusic-verify:app roomusic-verify:tools
```

两个临时标签已删除；只清理本次 `mktemp` 目录（含导出源码和镜像归档），并验证路径与标签不再存在。
未使用强制删除、全局 prune，未清理其他人的镜像或 Docker 构建缓存。

## 相对设计的实施细节

1. 验证导出树覆盖部署文件与完整流程延期，按主会话本轮明确批准执行；正式部署路径无例外。
2. `bash -n scripts/*.sh` 实际只把第一个文件当脚本，后续是参数；CI 改为逐文件循环，确保新增和原有脚本真正被检查。
3. 两个镜像都构建成功后才移动 current；比草图中的边构建边打 current 更符合“构建失败不移动 current”的文字合同。
4. 构建按变量名透传 Docker 预定义代理参数，解决实测模块下载超时；不输出代理值、不设新代理、不改基础镜像或模块来源。
5. 本机 Compose 2.40.3 的 `start --help` 不支持 `--wait`；恢复遵循设计先 start 已有容器，再只读轮询 app 健康，超时 300 秒。
6. BusyBox mktemp 模板要求随机字符位于末尾，临时文件使用 `.partial.<随机>`；最终 dump 以同目录硬链接原子发布，防止同秒备份互相覆盖。
7. 临时恢复使用权限 700 目录里的 644 副本，让容器 postgres 用户能读原本 uid 1000/600 的备份，原文件权限不变；退出清理副本。
8. `/data` 封面重建尚未实测，部署说明明确标注待演练，不声称已经证明。

## 留到提交后的演练

- `production.sh build-image`、完整 `--dry-run up`、真正 `up`，提交与脏工作区隔离、缓存复用、revision、回退。
- PostgreSQL 真实健康依赖、迁移、乱序重启、应用/备份自动恢复、UID 与 ffmpeg 运行时版本及派生自检。
- 真实 pg_dump、损坏凭据/备份的失败行为、升级前备份失败不换版本、临时库核对、生产恢复及恢复失败停机。
- 跨 http/https 登录与写操作、限速、外部设备的真实来源地址、FLAC/ALAC 播放、封面卷重建。
- 正式空库启动、用户自行初始化、开发库前后基线核对及计划时刻备份。

未运行全量 Go/前端/集成测试：本任务不改应用代码，按 §15 只跑最小部署验证，且本步禁止启动实例。

## 主会话集成事项

无新增产品合同裁决请求。请主会话审阅提交后再安排演练。
`identity-contracts.md` 的“命令行恢复”段仍有 `scripts/reset-password.sh` 读取 `.env` 的旧描述；该文件由并行登录限速 agent 所有，
本 agent 按约束没有修改，请主会话协调改成“开发 `.env.dev`，生产 `make prod-reset-password`”。

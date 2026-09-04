# 将 ROOMusic 开发环境迁移到 qlqq：技术设计

## 1. 设计目标与边界

迁移采用“并行搭建、备用端口验收、独立任务切换、失败自动回滚”的蓝绿方式。root
环境是蓝环境，只读保留；qlqq 环境是绿环境，先在 `6768` 验证，再接管 `6767`。

本任务迁移开发工具、项目内容、必要凭据和 daemon 配置，不迁移运行中的进程，也不把
root 的历史运行时数据库当成可移植配置。ROOMusic 产品代码、业务数据库、Docker volume
和真实音乐内容都不发生语义变更。

工具链、项目副本与 Paseo 服务共享同一个最终切换和回滚边界，因此使用一个顺序任务，
不拆成可独立结束的子任务，避免出现“服务已切换但项目/凭据尚未迁移”的半完成状态。

## 2. 目标目录与状态分类

| 来源 | 目标 | 迁移方式 | 失败时处理 |
| --- | --- | --- | --- |
| `/root/workspace/ROOMusic` | `/home/qlqq/workspace/ROOMusic` | `rsync` 到全新受控目标，保留 Git、未跟踪文件和本地数据，目标统一为 `qlqq:qlqq` | root 源目录不变，停止使用目标副本 |
| `/root/workspace/ROOMusic-V0` | `/home/qlqq/workspace/ROOMusic-V0` | 完整复制并校验，只作为当前 Smoke 任务的历史代码输入 | root V0 保留 |
| `/root/workspace/ROOMusic-migration.tar.gz` | `/home/qlqq/workspace/ROOMusic-migration.tar.gz` | 逐字节复制并核对固定 SHA-256 | root 归档保留 |
| root mise/NVM 安装清单 | `/home/qlqq/.local` | 版本固定、校验下载、以 qlqq 重新安装 | 删除操作不自动执行；保留失败副本供诊断 |
| `/root/.gitconfig`、`/root/.config/gh` | qlqq 对应路径 | 只复制必要配置/凭据，权限收紧 | root 配置不变；qlqq 可重新登录 |
| root Claude 设置与全局 skills | `/home/qlqq/.claude`、service env | 提取必要 provider 环境，复制非会话配置/skills；不复制历史 sessions/projects | qlqq 重新认证；root 登录状态不变 |
| `/root/.codex/config.toml`、skills | `/home/qlqq/.codex` | 复制配置/skills，定向替换可信项目路径 | 不复制 SQLite/WAL/锁/历史；root 数据不变 |
| `/etc/paseo*.env` | `/home/qlqq/.config/paseo/service.env` | 以 `0600` 合并必要变量，不输出值 | root env 文件不变 |
| `/root/.paseo/config.json` | `/home/qlqq/.paseo/config.json` | 迁移运行设置，敏感 provider env 归一到 service env | root Paseo home 完整保留 |
| root Paseo projects/agents/log/PID | 不导入 | qlqq 通过 CLI 注册新项目和工作区，历史状态留在 root | 回滚后 root 历史立即可用 |
| 系统 Docker daemon/volumes | 原位复用 | 将 qlqq 加入 `docker` 组 | 不改 volume；必要时可另行移除组成员资格 |

### 必须逐字节或语义等价迁移的内容

- Git HEAD、当前分支、index、tracked/untracked 内容和 remote 配置；
- `.env`、`.env.dev`、`.trellis` 任务/规范/日志、项目级 Agent 配置；
- `music/`、`backend/data/` 和存在时的 `.roomusic-smoke/`；
- `ROOMusic-V0` 与固定 V0 归档；
- 其余项目源码与文档。

### 可重新生成的内容

- `frontend/node_modules/`、`frontend/dist/` 和内嵌前端构建产物；初始复制用于保留现场，
  随后仅在 qlqq 副本中用 lockfile 验证/重建。
- mise 下载缓存、npm cache、CLI cache。

### 不迁移的易冲突运行时内容

- `.trellis/.runtime/` 的 root session 指针、临时 marker 和锁；
- root Codex 的 SQLite、WAL/SHM、队列、thread locks、shell snapshots、logs 和 history；
- root Claude 的 sessions、projects、session-env、backups；
- root Paseo 的 PID、daemon log、历史 Agent 状态、旧 project/workspace 注册表；
- root shell 历史、无关缓存和不存在的 SSH 私钥。

这些内容并未删除，仍留在 `/root` 作为回滚/审计资料。

## 3. 工具链设计

### 3.1 系统共享工具

保留当前 `/usr/bin` 中的 Git、GitHub CLI、Docker CLI/Compose plugin、curl、jq、rsync、
编译器、systemd 和 SHA-256 工具。补装 qlqq 当前缺失、Agent 与 Makefile 直接需要的
`make` 和独立 `ripgrep`；安装前先检查包状态，不做无关系统升级。

### 3.2 qlqq 用户工具

1. 以 `MISE_VERSION=2026.8.16`、`MISE_INSTALL_PATH=/home/qlqq/.local/bin/mise`
   运行官方安装器；安装器校验发布包 SHA-256。
2. 从迁移后项目 `.mise.toml` 安装 Go `1.25.10` 与 Node.js `24.16.0`，并设为 qlqq
   全局默认，确保项目外的 Paseo provider 诊断也能解析 Node CLI。
3. 在 mise 管理的 Node `24.16.0` 内按 npm `dist.integrity` 安装固定版本：
   `@getpaseo/cli@0.6.1`、`@anthropic-ai/claude-code@2.1.251`、
   `@openai/codex@0.151.0`、`@github/copilot@1.0.82`、
   `@mindfoldhq/trellis@0.6.16`。
4. qlqq 的 `.profile` 始终添加 `~/.local/bin` 和 mise shims；交互式 `.bashrc` 再执行
   mise 激活。Paseo systemd unit 不依赖 shell 初始化，直接使用绝对 ExecStart 和显式
   PATH。

## 4. 项目复制与一致性证明

### 4.1 目标保护

- 三个目标必须精确为 `/home/qlqq/workspace/ROOMusic`、
  `/home/qlqq/workspace/ROOMusic-V0` 和
  `/home/qlqq/workspace/ROOMusic-migration.tar.gz`；如果已存在且没有本任务创建的
  sentinel，立即停止，不覆盖、不合并。同级 `sub2api` 和 synthetic smoke 临时目录
  不属于 ROOMusic 迁移范围。
- 初次复制到同一文件系统上的临时目标 `/home/qlqq/workspace/ROOMusic.migrating`，
  校验后原子改名为最终目录。
- 所有复制操作显式写出源和目标，不使用 `$HOME`、`~`、未解析 glob 或宽目录递归删除。

### 4.2 两阶段同步

1. 在线初始同步复制主仓库、V0 目录和固定归档，但主仓库排除
   `.trellis/.runtime/` 和已知 PID/lock 文件。
2. qlqq preview 验收完成并停止后，切换任务再执行一次增量同步，把规划/实施期间 root
   工作区的新变化带到目标；只有通过 sentinel、realpath、所有权和 Git remote 校验后，
   才允许对该任务创建的目标使用 `--delete-delay`。

### 4.3 校验

- 在源和目标分别记录 HEAD、分支、`git status --porcelain=v1 -z` 的 SHA-256、
  `git diff --binary` 的 SHA-256，以及未跟踪文件集合摘要。
- 目标运行 `git fsck --full`。
- 固定归档必须保持 SHA-256
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`；V0 目录
  使用同样的私有树摘要比较，不在报告输出内部文件名。
- 对关键本地资产生成权限为 `0600` 的路径/类型/大小/mode/mtime/内容摘要 manifest；
  报告只保存总数、总字节数和 manifest 总摘要，不输出真实音乐文件名。
- 用 `rsync --checksum --dry-run --itemize-changes` 做最终差异门禁，差异明细只写入 root
  的 `0700` 迁移状态目录，聊天和 Git 只报告差异数量。
- 复制后把 `.env`、`.env.dev` 和本地凭据收紧为 `0600`，敏感目录为 `0700`。

## 5. 身份、凭据和 Docker 边界

### 5.1 Docker

执行 `usermod -aG docker qlqq`，随后重启无业务进程的 qlqq user manager，让新
supplementary group 生效，再启用 linger。验收必须证明 qlqq 可连接现有 daemon，看到
原 `roomusic-postgres-1` 与 `roomusic_postgres-data`，且容器 ID、卷名、端口和健康状态
与迁移前一致。不得运行 `docker compose down -v`、volume rm、数据库导入或 reset。

用户已经接受：Docker 组授予近似 root 的宿主机能力，本任务解决进程用户归属而非强隔离。

### 5.2 Git 与 GitHub

复制 Git identity/credential-helper 配置和 GitHub CLI 主机配置，文件权限收紧。使用
`gh auth status` 与 `git ls-remote origin HEAD` 验证；失败时停在切换前由 qlqq 重新登录，
不把 token 放进参数或日志。

### 5.3 Agent 凭据

- 将现有 Paseo 密码、OpenAI provider 环境和 Claude provider 环境合并到
  `/home/qlqq/.config/paseo/service.env`，目录 `0700`、文件 `0600`。systemd unit 本身
  只引用文件，不含 secret。
- qlqq Codex config 保留自定义 provider，但把 `/root/workspace/ROOMusic` 的可信项目和
  hook state key 定向改为 qlqq 路径。官方所述 `auth.json` 当前不存在，因此不伪造或
  复制登录缓存。
- Claude 使用同一主机内迁移的 provider 认证环境；若 qlqq 的 `claude auth status`
  或 Paseo diagnostic 不是 Ready，则显式重新登录。不得让 qlqq 进程读取 `/root`。

## 6. Paseo 双阶段服务

### 6.1 qlqq user service 公共合同

两个 user unit 均使用：

- `User` 由 user manager 隐式确定为 qlqq；
- `HOME=/home/qlqq`；
- `PASEO_HOME=/home/qlqq/.paseo`；
- 绝对 `ExecStart` 指向 qlqq mise Node 安装中的 Paseo；
- 显式 PATH 包含 qlqq Node/Go、mise、mise shims 和系统目录；
- `EnvironmentFile=/home/qlqq/.config/paseo/service.env`；
- `Restart=on-failure`，并设置合理启动/停止超时。

同一 `PASEO_HOME` 同时最多一个 daemon。unit 不包含密码或 API key。

### 6.2 Preview

`paseo-preview.service` 监听 `127.0.0.1:6768`，启用 Web UI、禁用 relay。它在 root
daemon 保持运行时验证：

1. `/api/health`；
2. qlqq owner、HOME、cwd 与 PATH；
3. Claude/Codex provider diagnostic 的 resolved path、版本、认证和 Ready；
4. 通过 CLI 注册 `/home/qlqq/workspace/ROOMusic`；
5. 分别创建一个只读 Claude 和 Codex 验收 Agent，输出仅包含 UID、工作目录是否匹配、
   `AGENTS.md` 是否可读和 Git HEAD，不输出环境变量或文件清单；
6. qlqq 对现有 Docker daemon、Git/GitHub 和项目最小测试的访问。

Preview 任一步失败都不停止 root 服务。

### 6.3 最终切换

当前会话属于 root Paseo cgroup，不能让它直接停止自己的父服务后继续报告。最终切换由
root systemd manager 启动的独立一次性 unit 执行，它不属于 `paseo.service` cgroup：

1. 获取专用 `flock`，拒绝并发切换；
2. 确认 preview 已停止、qlqq final unit 已启用但未运行、源/目标最终同步与校验通过；
3. 再次确认除了触发本次切换的会话外没有 `running` Agent；
4. 停止 root `paseo.service`；
5. 启动 qlqq `paseo.service` 接管 `0.0.0.0:6767`；
6. 在限定时间内轮询 `/api/health`，并验证 final MainPID UID 为 `1000`；
7. 成功后禁用 root system service，保留其 unit/env/home；失败则停止 qlqq 服务、重新
   启用并启动 root service；
8. 把不含 secret 的结果、时间和失败阶段写到 qlqq `0600` 状态文件。

切换会使当前浏览器/WebSocket 短暂断开。新 server ID 可能要求客户端删除旧主机后按
原地址和密码重新连接；这不影响 `/root/.paseo` 的回滚身份。

## 7. 验证与完成条件

### 环境门禁

- `id`、路径与固定版本检查；
- `paseo daemon status --json` 与 Claude/Codex provider diagnostics；
- `gh auth status`、`git ls-remote`、`git fsck`、源/目标 Git 状态摘要；
- qlqq `docker version`、`docker compose ps`、Compose 配置校验；
- Go 普通测试/构建和前端 lint/typecheck/test/build；Shell 语法检查。

本任务不修改产品代码，不运行真实音乐扫描、PostgreSQL 集成测试或全量 CI；这些会产生
与用户迁移无关的数据库/真实资产 I/O。项目单元构建门禁用于证明 qlqq 工具链可用。

### 最终运行态

- `loginctl show-user qlqq -p Linger` 为 `yes`；
- qlqq user Paseo enabled/active，root system Paseo disabled/inactive；
- 6767 listener 及其 Claude/Codex 子进程均为 UID 1000；
- 活动项目/工作区只指向 `/home/qlqq/workspace/ROOMusic`；
- 现有 Docker 容器/卷身份不变；
- `/root/workspace/ROOMusic`、`/root/.paseo` 和 root systemd/env 文件原样保留。

## 8. 回滚

自动回滚由切换 unit 在 qlqq 健康或 UID 验证失败时执行。人工回滚顺序固定为：

1. 停止并禁用 qlqq user Paseo；
2. 确认 6767 已释放；
3. 启用并启动 root system Paseo；
4. 验证 root `/api/health`、server ID、owner 和项目注册；
5. 保留 qlqq 项目/工具/日志用于诊断，不自动删除；
6. Docker 组成员资格默认保留，因为它不影响 root Paseo；若用户明确要求完全撤销，再
   单独移除 qlqq 的 docker supplementary group，并重启其 user manager。

任何回滚都不删除项目副本、不重置 Git、不删除 Docker volume，也不修改真实音乐。

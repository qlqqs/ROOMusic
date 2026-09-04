# 将 ROOMusic 开发环境迁移到 qlqq

## 目标

把当前以 `root` 运行的 ROOMusic 开发环境完整迁移到非 root 用户 `qlqq`：在
`/home/qlqq` 下重新安装并锁定项目要求的工具链，迁移项目、开发配置和必要的
Paseo/Claude Code/Codex 认证配置，随后让 Paseo 以 `qlqq` 身份运行并能在迁移后的
ROOMusic 工作区启动 Claude Code。迁移必须可验证、可回滚，不能破坏当前 root 环境、
当前 Docker 数据卷或工作区中的未提交改动。

## 用户价值

- Claude Code 不再由 root 运行，Paseo 创建的终端和 Agent 进程拥有正确的用户归属。
- 项目文件、Node/Go 工具链、Git 身份和 Agent 配置归属于 qlqq，后续开发不再依赖
  `/root` 私有目录。
- 迁移有明确的快照、验收和回滚点；失败时可以恢复 root Paseo，而不重置项目或数据库。

## 已确认事实

- 主机为 Ubuntu 24.04.4 LTS；`qlqq` 为 UID/GID `1000`，具备 `sudo`，当前没有
  Docker socket 权限。Docker daemon 由系统服务运行，`/var/run/docker.sock` 属于
  `root:docker`，现有 `docker` 组为 GID `110`。
- 当前项目为 `/root/workspace/ROOMusic`，工作区约 7.8G，其中 `music/` 约 7.6G，
  `frontend/node_modules` 约 126M，`backend/data` 约 4M。项目目录及文件当前由
  `root:root` 持有，qlqq 不能穿越 `/root` 访问它。
- Git 当前分支为 `task/real-library-smoke-v0-comparison`。其他 Agent 于 2026-09-04
  收尾后，源仓库只剩本迁移任务目录尚未跟踪；迁移仍不得以重新 clone 代替现场复制，
  且实施前后都必须重新记录实际 HEAD、分支、tracked/untracked 状态。
- 当前 Smoke 任务还依赖 `/root/workspace/ROOMusic-V0`（约 71M）和固定归档
  `/root/workspace/ROOMusic-migration.tar.gz`（约 21M，SHA-256 为
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`）。
  两者位于 qlqq 无法穿越的 `/root` 下，必须作为 ROOMusic 配套资产迁移；同级的
  `sub2api` 和临时 synthetic smoke 目录不属于本任务。
- 项目要求由 `.mise.toml` 声明 Go `1.25.10` 和 Node `24.16.0`；当前 root 已有
  mise、NVM 和全局工具，可作为版本和包清单参考，但 qlqq 侧应重新安装，不直接依赖
  root 的 PATH。
- root 侧当前工具版本/包包括：Claude Code `2.1.251`、Codex CLI `0.151.0`、
  GitHub Copilot CLI `1.0.82`、Paseo CLI `0.6.1`、Trellis `0.6.16`；Paseo CLI
  安装在 mise 管理的 Node `24.16.0` 全局目录中。
- root systemd 单元 `/etc/systemd/system/paseo.service` 以 `User=root`、
  `HOME=/root`、`PASEO_HOME=/root/.paseo` 启动 Paseo，监听 `0.0.0.0:6767`；
  drop-in `codex.conf` 还引用 root 的 NVM PATH 和 `/etc/paseo-codex.env`。
- Paseo root 配置包含 Claude/Codex/Copilot 终端配置、`https://app.paseo.sh` 的
  CORS/base URL，以及项目注册 `/root/workspace/ROOMusic` 和多个历史工作区记录。
  配置、daemon keypair、push token、Agent 状态、Claude 会话和 GitHub CLI 凭据都
  属于敏感用户状态，不能在任务文档或日志中输出明文。
- 现有 Compose 项目 `roomusic` 正在使用 root Docker daemon 的 PostgreSQL 18 容器，
  数据卷为 `roomusic_postgres-data`，绑定 `127.0.0.1:5432`；Redis/Meilisearch
  可能按需启动。迁移不复制、重建、删除或重置这些数据卷。
- root 项目的 `.env`、`.env.dev` 是本地敏感配置，当前权限过宽；新工作区必须收紧
  为 qlqq 所有、`0600`，不能把值写进 Git 或迁移文档。
- qlqq 的登录 shell 目前只有 Ubuntu 默认配置，没有 mise、NVM、Claude Code、
  Codex、Paseo 或 rootless Docker；其 systemd user manager 尚未启用 linger。系统已有
  Git、gh、Docker CLI、curl、jq、rsync 和编译器，但 qlqq 缺少 `make` 与独立 `rg`。
- 已检索项目代码、Compose、Paseo 配置和历史会话，没有发现既有的 rootless Docker/
  qlqq 迁移决策；用户已选择复用系统 Docker daemon，并将 qlqq 加入 `docker` 组。

## 需求

### R1. 迁移前快照和安全边界

1. 在任何停机、权限变更或配置复制前，记录 root Paseo 单元、运行状态、监听地址、
   项目 Git 状态、文件树聚合摘要、Docker 容器/卷清单和工具版本；敏感值只做存在性
   和类型记录，不写入报告。
2. 为 root 用户状态、systemd unit/drop-in/env 文件、Paseo 注册表、Claude/Codex/
   Copilot/GitHub CLI 配置和项目工作区创建权限受限的回滚快照；快照不能进入 Git。
3. 迁移采用“复制并验证，再切换”的方式。root 目录、root 工具和 root Paseo 在
   qlqq 验收完成前保持不变；禁止使用 `git reset`、`git clean`、`rm -rf` 或重置业务库。

### R2. qlqq 工具链和 shell 环境

1. 以 qlqq 身份安装独立 mise，并从项目 `.mise.toml` 安装 Go `1.25.10` 和 Node
   `24.16.0`；`mise current`、Go、Node、npm 的路径和版本必须指向 `/home/qlqq`。
2. 以锁定版本重新安装 Claude Code、Codex CLI、GitHub Copilot CLI、Paseo CLI 和
   Trellis；全局 npm 安装目录属于 qlqq，不能引用 `/root/.nvm` 或 `/root/.local`。
3. 更新 qlqq 的登录/交互 shell PATH 和 mise 激活，使 SSH、Paseo 终端、非交互脚本
   都能解析同一套工具；保留系统 `/usr/bin` 工具作为后备。
4. 安装结果必须可由 qlqq 非 root shell 重现，且 `claude --version`、`codex --version`、
   `paseo --version`、`trellis --version` 均成功。

### R3. 项目、Git 和本地数据迁移

1. 将 `/root/workspace/ROOMusic` 完整复制到推荐的新路径
   `/home/qlqq/workspace/ROOMusic`，包括 `.git`、所有未跟踪文件、`.trellis`、
   `.claude`、`.codex`、`.cursor`、`frontend/node_modules`、`backend/data` 和
   `music/`；只排除 `.trellis/.runtime` 等明确的 root session/PID/lock 临时状态，
   不得通过重新 clone 丢失持久工作区状态。
2. 将 `/root/workspace/ROOMusic-V0` 和固定 `ROOMusic-migration.tar.gz` 分别复制到
   `/home/qlqq/workspace/` 对应名称；复核归档 SHA-256，且不得修改 V0 代码或归档内容。
3. 复制后所有项目文件由 `qlqq:qlqq` 持有；`.git`、本地配置、脚本和数据目录的
   权限满足最小需要，`.env*` 等敏感文件至少为 `0600`。用 Git 状态、对象校验、
   文件数量/大小/内容摘要验证源与目标一致。
4. 迁移 root 的 Git 用户名、邮箱、GitHub CLI 主机配置和有效认证状态到 qlqq；不
   复制无关 root shell 历史。若凭据不能安全复用，停止在切换前提示重新登录，不能
   把 token 写入命令行、日志或任务文档。
5. Compose 项目名、端口、卷名和数据库连接默认保持兼容；qlqq 只能以当前用户
   权限使用 Docker，不能改变现有卷的所有权或数据。

### R4. Paseo/Claude Code/Codex 切换

1. 为 qlqq 创建新的 `PASEO_HOME=/home/qlqq/.paseo`，迁移终端 profile、CORS、
   base URL、Agent provider 开关、非敏感运行设置和必要 provider auth；新 daemon
   自行生成 server ID/keypair，不导入 root PID、日志、Agent 历史或项目注册表。root
   identity/push 状态原样保留用于回滚，迁移过程不能泄露任何敏感值。
2. 将 `/etc/paseo-codex.env` 和 `/etc/paseo.env` 的必要变量迁移到 qlqq 专用、
   权限为 `0600` 的用户环境文件或 systemd `EnvironmentFile`；不让 qlqq 服务读取
   root HOME，也不把 secret 写入 world-readable unit。
3. 新 Paseo 服务以 `User=qlqq`、`Group=qlqq`、`HOME=/home/qlqq`、
   `WorkingDirectory=/home/qlqq/workspace/ROOMusic` 启动，监听地址默认保持
   `0.0.0.0:6767` 以兼容现有客户端；切换前必须确认旧服务已停止，避免端口抢占和
   两个 daemon 同时处理同一注册状态。
4. 优先使用 qlqq 的 user systemd service，并启用 `loginctl enable-linger qlqq` 保证
   无图形登录时可自启动；若系统策略不允许 linger，必须明确记录为运维前置条件，
   不能假装系统服务迁移已完成。
5. 在新的 qlqq Paseo 中注册 qlqq 项目/工作区；其活动注册不得指向 `/root`。历史
   root 工作区记录留在 root home 作为只读回滚资料，不导入 qlqq daemon。
6. 从 Paseo 创建一次最小 Claude Code Agent/终端，确认其进程 UID 为 `1000`、cwd
   为 qlqq 项目、能读取 `AGENTS.md` 并执行只读 Git 命令；同时确认 Codex 可启动。

### R5. 验收、切换和回滚

1. 切换前用 qlqq 运行项目级 `mise run env-check`、后端构建/测试、前端 lint/typecheck/
   test/build、Shell 语法和 Compose 配置检查；优先使用与迁移直接相关的最小验证集。
2. 切换后验证 `systemctl --user --machine=qlqq@ status paseo`、`curl` 健康/登录入口、
   Paseo Web UI/客户端连接、Claude Code/Codex Agent、项目 Git 状态和 Docker Compose
   连接；确认新进程和子进程全部非 root。
3. 验证现有 PostgreSQL 容器和卷的容器 ID、卷名、挂载、端口及健康状态与切换前一致；
   禁止为证明迁移而查询业务表，或对当前 ROOMusic 数据库执行迁移、清空、导入操作。
4. 若任一验收失败，先停止 qlqq Paseo，恢复 root unit/drop-in/env、root Paseo
   配置和 root 项目注册，再启动 root Paseo；回滚不得删除 qlqq 新副本或 root 快照。
5. 只有 qlqq Paseo 连续通过进程 UID、工具解析、Claude/Codex 启动和项目只读验证后，
   才可将 root Paseo 标记为备用；root 文件和快照至少保留一个观察窗口。

## 验收标准

- [x] AC1：迁移前快照存在且权限受限；root Paseo、项目 Git 状态、Docker 容器/卷和
      敏感配置均可在不泄露明文的情况下恢复。
- [ ] AC2：qlqq shell 中 mise、Go `1.25.10`、Node `24.16.0`、npm 及五个 CLI 均由
      `/home/qlqq` 提供，版本与锁定清单一致，不依赖 root PATH。
- [x] AC3：`/home/qlqq/workspace/ROOMusic` 与源工作区的 Git HEAD、分支、未提交改动、
      未跟踪文件、文件聚合摘要和本地数据一致；V0 目录和固定归档也已迁移且归档 hash
      不变；所有权和敏感权限正确，真实音乐内容未被修改。
- [x] AC4：qlqq 能以非 root 身份运行 Docker Compose；现有 `roomusic` 容器、卷、端口
      和数据库数据未被重置或替换。
- [ ] AC5：Paseo 以 qlqq user service 运行在 `6767`，配置中的活动项目路径均指向
      `/home/qlqq/workspace/ROOMusic`，不再引用 `/root`；服务重启后可自动恢复。
- [x] AC6：通过 qlqq Paseo 启动 Claude Code 和 Codex 的最小 Agent 验收成功，子进程
      UID 为 `1000`，Claude Code 不再因 root 环境失败。
- [x] AC7：切换失败可按记录的回滚点恢复 root Paseo；迁移任务未执行破坏性删除、
      `git reset/clean`、Docker 卷删除或数据库重置。

收尾时保留两项已知偏差：AC2 的安装阶段锁定版本曾通过验收，但 Claude Code 与 Codex
随后自动升级为更高版本；AC5 的非 root 切换、ROOMusic cwd 和无 `/root` 引用均已满足，
但 qlqq Paseo 还保留一个迁移范围外的 `CLIProxyAPI` 活动工作区，因此不把两个严格条件
勾选为完全通过。详细证据和用户批准跳过的复测项见 `migration-report.md`。

## 范围外

- 不修改 ROOMusic 产品代码、业务 schema、迁移 SQL、真实音乐标签/CUE/封面或扫描结果。
- 不迁移或重置 Docker volume 内容，不将 PostgreSQL/Redis/Meilisearch 改成用户态服务。
- 不恢复 V0 runtime、历史 golden 数据、生产部署或外部 NAS 权限。
- 不自动清理 root 历史 Paseo Agent、Claude 会话、Codex 数据库或日志；它们只作为回滚
  和审计资料保留，清理需另行审批。
- 不把 root 的 shell 历史、无关缓存、SSH 私钥或未确认的第三方凭据无差别复制到 qlqq。

## 风险与延期

- 用户已选择将 qlqq 加入系统 `docker` 组；该组成员实际拥有近似 root 的 Docker
  宿主机能力。rootless Docker 所提供的强隔离延期到独立任务，本次不同时维护两套 daemon。
- Paseo 当前是系统级服务且保存了大量历史 Agent 状态；直接复制状态可能产生路径、UID、
  socket 或 session 冲突，应先以新目录验证，再切换端口/服务。
- 复制约 7.8G 项目需要额外磁盘和较长 I/O；必须使用校验摘要，不能用“复制命令成功”
  代替一致性证明。
- Claude Code、Paseo provider auth 和 GitHub CLI token 可能过期或绑定 root 环境；
  验收失败时改走 qlqq 重新登录，不在日志中回显凭据。
- root Paseo 保留大量 idle/closed 历史 Agent；它们不会导入新 daemon。当前规划会话是
  唯一 running Agent，切换时会断开；由独立 systemd 任务执行切换和自动回滚。
- qlqq daemon 使用新 server ID。直连 Web UI 地址与密码保持不变，但缓存旧 server ID
  的客户端可能需要删除旧 host 后重新添加。

## 关键决策

### D1. qlqq 的 Docker 访问模式（已决策）

- 选择结果：将 `qlqq` 加入现有 `docker` 组，复用系统 Docker daemon、Compose 项目、
  网络、端口和卷。
- 选择者：用户，于 2026-09-03 明确确认。
- 依据：保留现有开发环境兼容性、避免迁移数据库卷，并缩短 Paseo 切换停机窗口。
- 已接受风险：`docker` 组成员实际拥有近似 root 的 Docker 宿主机能力；这解决的是
  Paseo/Compose 的用户归属问题，不构成强隔离。rootless Docker 不在本任务中实施。

### D2. 切换时运行中 Agent 的处置（已决策）

- 选择结果：用户于 2026-09-04 确认其他 Agent 已全部停止，可以进入维护窗口。
- 复核结果：37 个历史 Agent 为 `idle`、12 个为 `closed`，唯一 `running` 项是当前规划
  对话。最终切换会中断当前对话，属于预期行为。
- 实施策略：先用 qlqq preview daemon 完成验证；最终由 root Paseo cgroup 外的独立
  systemd 一次性任务延迟切换。任务在当前 turn 结束并变为 idle 后才停止 root 服务；
  若仍发现其他 running Agent，则 fail closed，不切换。

## 规划决策记录

- 路线：human selection。
- 选择者：用户，D1 与 D2 均已确认。
- 已选结果：qlqq 加入现有 `docker` 组；在其他 Agent 停止后，以 preview 预验收和独立
  systemd 健康探针完成 Paseo 切换；root 环境保留用于回滚。
- 选择依据：该路径不迁移 Docker volume，能验证非 root Claude/Codex 后再产生短暂停机，
  并在 qlqq daemon 不健康时自动恢复 root 服务。
- 实施批准：用户于 2026-09-04 在最终规划摘要后的后续消息中明确回复“批准实施”。
  规划门禁已满足，可以运行 `task.py start` 并按已批准方案实施。

# 将 ROOMusic 开发环境迁移到 qlqq：执行计划

## 执行原则

- 只有用户审阅最终规划摘要并在后续消息明确批准后，才运行 `task.py start` 和本计划。
- 所有系统命令先做只读目标解析；遇到路径、owner、hash、端口或服务状态不符时 fail
  closed，不猜测、不覆盖。
- root 工作区、root Paseo home、root CLI 安装和现有 Docker volume 在整个观察期保留。
- 不输出 `.env`、Paseo/Claude/Codex/GitHub 凭据、完整音乐路径或 Agent 会话正文。
- 每完成一个阶段即写入权限为 `0600` 的本地迁移状态记录；只有标记为切换点的阶段会
  影响当前 Paseo 连接。

## Phase 0：实施门禁

- [x] 启动前确认当前任务为 `.trellis/tasks/09-03-migrate-roomusic-to-qlqq`、状态为
      `planning`，且三份规划材料和上下文 manifest 均存在。
- [x] 在最终规划摘要后的新一条用户消息中取得明确实施批准。
- [x] 运行 `python3 ./.trellis/scripts/task.py validate 09-03-migrate-roomusic-to-qlqq`。
- [x] 运行 `python3 ./.trellis/scripts/task.py start 09-03-migrate-roomusic-to-qlqq`。
- [x] 通过 Trellis `trellis-implement` 执行预切换阶段；主会话负责系统切换、敏感状态
      协调和最终提交。子 Agent 不独自停止其父 Paseo daemon。
- [x] 再次记录源仓库 HEAD、分支和实际 dirty 状态；dirty 状态允许存在，但必须完整
      镜像，不得 reset/clean/stash。

实施记录（2026-09-04）：用户已明确批准实施，任务随后完成校验并进入 `in_progress`；
规划材料、上下文 manifest、迁移前 Git 状态和源/目标一致性证据均已落盘。最终收口时
`task.py list` 仍能识别该任务为 `in_progress`，但 `task.py current` 没有当前任务指针；
这与迁移时未复制 `.trellis/.runtime/` 的运行态设计一致。最终归档应显式指定任务目录，
不能依赖当前任务指针。

## Phase 1：只读基线与回滚快照

### 1.1 路径和容量门禁

- [x] 解析并逐字比较以下 realpath：
  - 源：`/root/workspace/ROOMusic`
  - 主目标：`/home/qlqq/workspace/ROOMusic`
  - V0 源/目标：`/root/workspace/ROOMusic-V0` →
    `/home/qlqq/workspace/ROOMusic-V0`
  - 归档源/目标：`/root/workspace/ROOMusic-migration.tar.gz` →
    `/home/qlqq/workspace/ROOMusic-migration.tar.gz`
- [x] 确认 `/home/qlqq` 与源在预期文件系统，剩余容量高于源数据总量加 20% 余量。
- [x] 若任何最终目标已经存在且不是本任务创建，立即停止并报告，不覆盖。

### 1.2 创建权限受限的状态目录

使用 `mktemp -d` 在明确父目录下创建一次性状态目录，例如
`/root/roomusic-qlqq-migration/state.XXXXXX`；父目录和子目录均为 `0700`。状态目录保存：

实施状态目录为 `/root/roomusic-qlqq-migration/state.P2ovv4`；目录权限为 `0700`，其中
文件统一为 `0600`。

- source/destination realpath；
- Git HEAD、branch、status/diff/untracked 集合的摘要；
- root Paseo status、server ID、owner、版本、监听和 Agent 状态计数；
- root/qlqq 用户组、linger、systemd service enable/active 状态；
- Docker daemon、`roomusic-postgres-1` 容器 ID/镜像/端口/健康状态、Compose project 和
  volume 名称；
- 工具版本与 npm `dist.integrity`；
- V0 固定归档 SHA-256；
- 敏感配置文件只记录路径、owner、mode、size 和 SHA-256，不记录值。

### 1.3 静态快照

- [x] 用 `umask 077` 归档下列小型控制面文件：
  - `/etc/systemd/system/paseo.service` 与 drop-in；
  - `/etc/paseo.env`、`/etc/paseo-codex.env`；
  - `/root/.paseo/config.json`、server identity/keypair/push metadata 和 projects 注册表；
  - `/root/.claude.json`、`/root/.claude/settings.json`；
  - `/root/.codex/config.toml`；
  - `/root/.gitconfig`、`/root/.config/gh`。
- [x] 快照包与 manifest 均为 `0600`，验证 `tar -tf` 和整体 SHA-256。
- [x] 不重复归档 7.8G 项目；root 源目录本身就是回滚副本，直到观察期结束不删除或改名。

**回滚点 A：** 此阶段无运行态变更；删除动作不是恢复所必需，保留快照即可。

## Phase 2：qlqq 主机权限与基础工具

- [x] 仅安装缺失包：`make`、`ripgrep`。运行 apt 前记录候选版本；不做 distribution
      upgrade，不移除包。
- [x] 执行 `usermod -aG docker qlqq`，复核 `getent group docker` 和 `id qlqq` 包含
      GID `110`，且未移除 qlqq 现有 `sudo`、`adm`、`lxd` 等组。
- [x] 确认 qlqq 没有进程后，重启其 user manager/登录会话以载入 supplementary group。
- [x] 执行 `loginctl enable-linger qlqq`；复核 `Linger=yes`、
      `/run/user/1000` 和 user bus 可用。
- [x] 以全新的 qlqq 登录上下文运行 `docker version` 和只读 `docker ps`；确认能连接
      `/var/run/docker.sock`，不使用 `sudo docker`。

**回滚点 B：** 此时 root Paseo 未受影响。基础包与 Docker 组成员资格默认保留；若用户
要求完全撤销，单独执行 `gpasswd -d qlqq docker` 并重启 qlqq user manager，不触碰卷。

## Phase 3：复制 ROOMusic 工作区与配套资产

### 3.1 初始镜像

- [x] 创建 `/home/qlqq/workspace/ROOMusic.migrating`，owner 为 `qlqq:qlqq`，写入
      仅本任务识别的 sentinel；若路径已存在则停止。
- [x] 用 root 读取源、以 `rsync -aHAX --chown=qlqq:qlqq` 同步主仓库。保留 symlink、
      hardlink、mtime、xattr/ACL，并排除：
  - `.trellis/.runtime/`；
  - 已知 PID、socket、lock 和临时原子写文件；
  - 不存在或明显属于其他项目的同级目录。
- [x] `music/`、`.git/`、tracked/untracked 文件、`.env*`、`.roomusic-smoke/`（若存在）、
      `backend/data/` 和当前 `frontend/node_modules/` 均纳入初始镜像。
- [x] 分别把 `ROOMusic-V0` 与固定归档复制到带 `.migrating` 后缀的明确目标，统一 owner
      为 qlqq；不修改 V0 内部内容。

### 3.2 一致性与权限

- [x] 对主仓库比较 HEAD、branch、Git status/diff/untracked 摘要，并在目标运行
      `git fsck --full`。
- [x] 对源/目标执行同排除规则的 `rsync --checksum --dry-run --itemize-changes`；终端只
      输出差异数量，完整差异文件放在 root `0700` 状态目录。
- [x] 对 `music/` 生成包含相对路径的私有 manifest，比较文件数量、总大小、mode、mtime、
      类型与内容摘要；任务报告只记录总数/总字节/总摘要。
- [x] 验证归档 SHA-256 为
      `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`。
- [x] 设置目标 `.env`、`.env.dev` 为 `0600`，`backend/data`、`.roomusic-smoke` 等本地
      敏感目录不授予 group/other 写权限；全树 owner 必须为 `qlqq:qlqq`。
- [x] 校验通过后，把三个 `.migrating` 目标分别原子改名为最终名称；不覆盖已存在目标。

**回滚点 C：** root 数据尚未变更；qlqq 最终路径可停止使用但不自动删除。

## Phase 4：在 qlqq 下重装工具链

### 4.1 mise 与语言运行时

- [x] 将 `https://mise.run` 下载到 `mktemp` 文件，确认来源与脚本支持
      `MISE_VERSION`/`MISE_INSTALL_PATH` 后执行；不直接复用 root binary。
- [x] 以 qlqq、`HOME=/home/qlqq` 安装 mise `2026.8.16` 到
      `/home/qlqq/.local/bin/mise`，验证 binary owner、mode 和版本。
- [x] 在 qlqq 项目中信任 `.mise.toml`，运行 `mise install` 安装 Go `1.25.10` 与
      Node `24.16.0`，再设为 qlqq 全局默认。
- [x] 验证 `mise current`、`go version`、`node --version`、`npm --version`，所有用户态
      路径必须位于 `/home/qlqq`，不得出现 `/root`。

### 4.2 Agent CLI

- [x] 在 qlqq mise Node `24.16.0` 中一次性安装固定 npm 包：

```text
@getpaseo/cli@0.6.1
@anthropic-ai/claude-code@2.1.251
@openai/codex@0.151.0
@github/copilot@1.0.82
@mindfoldhq/trellis@0.6.16
```

- [x] 对 npm lock/cache 返回的包 integrity 与 Phase 1 基线逐项比较。
- [x] 验证 `npm prefix -g` 位于 qlqq mise Node 目录，五个 CLI 的 resolved path 都在
      `/home/qlqq`，版本完全匹配。

### 4.3 Shell 初始化

- [x] 用带开始/结束标记的幂等小块更新 qlqq `.profile` 与 `.bashrc`：登录 shell 添加
      `/home/qlqq/.local/bin` 和 mise shims；交互 shell 激活 mise。
- [x] 不复制 root `.bash_history`、`.profile`、`.bashrc` 或 NVM 初始化。
- [x] 分别在 `runuser -u qlqq -- bash -lc` 和新交互 login shell 中验证 PATH；重复加载
      不得产生重复 PATH 或错误输出。

**回滚点 D：** qlqq 工具完全独立；root NVM/mise/CLI 仍可原样工作。

## Phase 5：迁移用户配置与凭据

所有操作先设置 `umask 077`。复制后先验证 owner/mode，再运行任何会读取凭据的 CLI。

### 5.1 Git/GitHub

- [x] 复制 Git 用户名、邮箱和 `/usr/bin/gh auth git-credential` helper 配置到
      `/home/qlqq/.gitconfig`。
- [x] 安全复制 `/root/.config/gh` 到 qlqq，文件保持 `0600`；不回显 YAML。
- [x] 以 qlqq 运行 `gh auth status` 和 `git ls-remote origin HEAD`。失败时暂停切换并
      由 qlqq 重新登录，不在命令参数中传 token。

### 5.2 Claude Code

- [x] 复制 root 的非会话 Claude skills/用户级指令；不复制 `sessions/`、`projects/`、
      `session-env/`、`backups/` 和历史日志。
- [x] 把 Claude provider 所需环境键安全迁入 qlqq service env；不把值写入项目或 unit。
- [x] qlqq 的 Claude 配置/凭据文件设为 `0600`、目录 `0700`。运行认证状态命令只记录
      logged-in/auth-method，不输出 token。

### 5.3 Codex

- [x] 复制 `/root/.codex/config.toml` 和 `skills/`；定向把 trusted project 与 hook state
      key 中的 root ROOMusic 路径替换为 qlqq 路径，保留相同 hook hash。
- [x] 不复制 `*.sqlite*`、`sessions/`、`history.jsonl`、`session_index.jsonl`、queue、
      locks、tmp、logs 或 shell snapshots。
- [x] 当前没有 root `auth.json`，不创建伪登录缓存。将 custom provider 的
      `OPENAI_API_KEY`/`OPENAI_BASE_URL` 放入 service env，通过 provider diagnostic
      验证；若改用 Codex file auth，严格按照官方 stdin 登录方式生成 `0600` auth 文件。

### 5.4 Paseo

- [x] 创建 `/home/qlqq/.config/paseo/service.env`：合并现有 Paseo 密码、OpenAI 与 Claude
      provider 所需键；只检查键名集合和非空状态，文件 `0600`。
- [x] 创建全新 `/home/qlqq/.paseo`（`0700`），迁移 root `config.json` 的终端 profiles、
      provider 设置、CORS/base URL、MCP、browser、relay 和 Web UI 配置；provider secret
      从 JSON 移到 service env，配置文件 `0600`。
- [x] 不复制 root PID、logs、agents、projects/workspaces、schedules runtime、server ID、
      keypair 或 push token。qlqq daemon 生成新 identity；root identity 保留供回滚。
- [x] 使用 JSON schema/`jq` 校验配置，并扫描目标用户配置中的 `/root` 引用；任何运行
      路径引用 `/root` 都必须在 preview 前清零。

**回滚点 E：** 所有 qlqq 凭据副本只对 qlqq/root 可读；root 配置仍未修改。

## Phase 6：安装 qlqq Paseo user units

### 6.1 最终 unit

文件：`/home/qlqq/.config/systemd/user/paseo.service`，owner `qlqq:qlqq`、mode `0644`。
等价合同如下，实际绝对路径由安装后验证结果填入：

```ini
[Unit]
Description=Paseo coding agent daemon (qlqq)
After=network-online.target
Wants=network-online.target
Conflicts=paseo-preview.service

[Service]
Type=simple
Environment=HOME=/home/qlqq
Environment=PASEO_HOME=/home/qlqq/.paseo
Environment=PATH=/home/qlqq/.local/share/mise/installs/node/24.16.0/bin:/home/qlqq/.local/share/mise/installs/go/1.25.10/bin:/home/qlqq/.local/bin:/home/qlqq/.local/share/mise/shims:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
EnvironmentFile=/home/qlqq/.config/paseo/service.env
WorkingDirectory=/home/qlqq/workspace/ROOMusic
ExecStart=/home/qlqq/.local/share/mise/installs/node/24.16.0/bin/paseo daemon start --foreground --web-ui --no-relay --listen 0.0.0.0:6767 --hostnames true
Restart=on-failure
RestartSec=5
TimeoutStartSec=60
TimeoutStopSec=30
UMask=0022

[Install]
WantedBy=default.target
```

### 6.2 Preview unit

创建同等环境的 `paseo-preview.service`，但：

- `Conflicts=paseo.service`；
- `ExecStart` 使用 `--listen 127.0.0.1:6768`；
- 不 enable，只手动启动；
- 使用同一个 qlqq `PASEO_HOME`，因此 preview 停止且进程完全退出后才允许启动 final。

- [x] `systemctl --user --machine=qlqq@ daemon-reload`。
- [x] 只 enable final unit，不启动；确认 root system service 仍在 6767。
- [x] `systemd-analyze --user verify` 两个 unit，无 unknown directive 或路径错误。

阻塞期安全状态（2026-09-04）：Phase 7 遇到 Claude 上游 `502` 后，已临时 disable
qlqq final unit，final/preview 均保持 inactive/disabled，避免等待上游期间主机重启后与
仍启用的 root 服务争抢 6767。配置与 unit 文件均保留；Phase 9 正式切换前再重新 enable。

## Phase 7：qlqq Preview 验收（root Paseo 不停机）

- [x] 启动 `paseo-preview.service`，等待 `http://127.0.0.1:6768/api/health` 成功；验证
      listener PID、父 cgroup、UID/GID、HOME、PASEO_HOME 和 cwd 均属于 qlqq。
- [x] 以 qlqq service env 调用：
  - `paseo provider diagnostic claude --json --host 127.0.0.1:6768`
  - `paseo provider diagnostic codex --json --host 127.0.0.1:6768`
  - `paseo provider diagnostic copilot --json --host 127.0.0.1:6768`
- [x] Claude/Codex 必须为 Ready，resolved path 位于 `/home/qlqq`，版本与基线相同；
      daemon PATH 不得包含 `/root`。
- [x] 注册 `/home/qlqq/workspace/ROOMusic` 并创建 local workspace；项目清单中不得出现
      root 路径。
- [x] 动态读取 provider 默认模型/模式后，分别运行一个 Claude 与 Codex 只读 Agent，
      prompt 仅要求返回：`id -u` 是否为 1000、cwd 是否为目标、`AGENTS.md` 是否可读、
      Git HEAD。设置有限 wait timeout，禁止写文件、读取 env 或递归枚举音乐。
- [x] 用 `ps`/cgroup 交叉验证两个 provider 子进程均由 UID 1000 启动；Claude Code 不再
      出现 root/bypass 拒绝。
- [x] 停止 preview；等待所有 preview provider 子进程退出，确认 6768 释放。

实施记录（2026-09-04）：第一次验收时 Claude 上游消息 API 持续返回 `502`，因此按
fail-closed 门禁暂停。上游恢复为 HTTP 200 后重新启动 preview；Claude、Codex 和
Copilot diagnostic 均为 Ready，Claude 与 Codex 只读 Agent 均完整通过 UID、cwd、
`AGENTS.md` 和 Git HEAD 验收，两个 provider 子进程均实测为 UID 1000。验收 Agent 已
归档，preview 已停止且 6768 已释放；root 6767 全程保持健康。

Preview 任一步失败：保留 root 6767，不进入 Phase 8；修正 qlqq 环境后重跑本阶段。

## Phase 8：qlqq 项目与 Docker 最小门禁

在 `/home/qlqq/workspace/ROOMusic`、qlqq 登录环境中执行：

- [x] `mise current`；
- [x] 加载 `.env.dev` 后运行 `docker compose config --quiet` 与 `docker compose ps`；
- [x] 核对 `roomusic-postgres-1` 容器 ID、volume 名、端口和健康状态与 Phase 1 一致；
- [x] `(cd backend && go test ./... -count=1 && go build ./...)`；
- [x] `(cd frontend && npm ci && npm run lint && npm run typecheck && npm run test -- --run && npm run build)`；
- [x] `bash -n scripts/*.sh`；
- [x] `git diff --check`，并确认 build 没有产生未解释的 tracked/untracked 漂移。

不运行真实音乐扫描、PostgreSQL 集成测试或全量 CI，因为本任务未修改产品行为；如普通
测试暴露环境问题，只扩展到能定位该环境问题的最小测试。

实施记录（2026-09-04）：`mise run env-check`、Compose 配置与只读状态、PostgreSQL
容器身份/volume/端口/健康状态、后端测试与构建、前端干净安装/lint/typecheck/test/build、
Shell 语法和 `git diff --check` 均通过。第一次依赖下载因错误地按纯文本读取带引号的
systemd 代理值而失败；改为由 Bash 解析并在子进程前移除所有非代理变量后重跑成功。
构建前后 Git status 摘要一致，无未解释漂移。

## Phase 9：最终增量同步与独立切换

### 9.1 最终同步门禁

- [x] root 6767 保持 active，qlqq preview/final 均 inactive。
- [x] 使用 Phase 3 sentinel、精确 realpath、owner 与 Git remote 证明目标由本任务创建。
- [x] 再次 rsync root 主仓库到 qlqq 目标；允许对受控目标使用 `--delete-delay`，但排除
      `.trellis/.runtime/`、qlqq 重建的 `frontend/node_modules/` 和已知 runtime/lock。
- [x] 同步 V0 与固定归档，重新执行 Git/rsync/music/归档摘要门禁。
- [x] 重新检查 qlqq 配置/units/service env 不含 `/root` 运行路径且权限正确。

实施记录（2026-09-04）：最终增量同步前通过 sentinel、realpath、owner、Git remote 和
服务状态门禁；主仓库仅有 13 项预期的文档、Git 元数据、敏感文件权限与构建产物差异，
V0 和固定归档无需更新。同步后主仓库与 V0 的 checksum dry-run 均为零差异，music/V0
元数据 manifest、Git HEAD/分支/status/diff/untracked、`git fsck` 与固定归档 SHA-256
全部通过；目标 owner 和敏感权限正确，qlqq 运行配置中的 `/root` 引用为零。

### 9.2 切换 helper

安装一个 root-owned、不可由 qlqq 修改的一次性 helper。helper 必须：

1. 用明确 lock file 获取 `flock`；
2. 轮询 root `paseo ls --json`，只在所有 Agent 均为 idle/closed 时继续；超时即退出，
   root 服务保持原状；
3. 记录 root service enabled/active 状态；
4. `systemctl stop paseo.service`；
5. 确认 6767 释放；
6. `systemctl --user --machine=qlqq@ start paseo.service`；
7. 最多等待 60 秒，要求 `/api/health` 成功、user unit active、MainPID UID=1000、
   PASEO_HOME/cwd 正确；
8. 成功后 `systemctl disable paseo.service`（系统 scope），写入 `success` 状态；
9. 任一步失败则停止 qlqq unit、重新 enable/start root unit、验证 root health，并写入
   `rolled_back` 与失败阶段；helper 自身返回非零。

helper 已安装到 root 权限受限的迁移状态目录，owner 为 `root:root`、mode 为 `0700`，
并通过 `bash -n`。helper 后续已由独立 transient unit 触发；最终状态文件记录切换成功，
qlqq final unit 为 enabled/active，root Paseo 为 disabled/inactive。

### 9.3 触发

- [x] 以 root system manager 的 transient unit 调用 helper，并设置约 30 秒延迟，让当前
      Agent turn 完成并进入 idle。不要在 `paseo.service` cgroup 中同步调用 stop。
- [x] 触发命令返回 transient unit 名称与状态文件路径后，向用户说明 WebSocket 将短暂
      断开，以及成功/回滚时应访问的地址。

当前会话随后中断是预期事件。切换任务独立运行，即使 root Paseo 被停止也能完成健康
检查或自动回滚。

实施记录（2026-09-04）：切换状态文件
`/home/qlqq/.local/state/roomusic-migration/phase9-switch.status` 为 `qlqq:qlqq`、`0600`，
记录 `status=success`、`failed_stage=none`，完成时间为 `2026-09-04T03:52:08Z`。重连后的
实时 systemd 与健康检查结果和该状态一致。

## Phase 10：重连后的最终验收

用户从同一 `http://<host>:6767` Web UI 重新连接；若客户端缓存旧 server ID，则删除旧
host 后用原地址/密码重新添加。新 qlqq Paseo 会话从目标项目继续本任务。

- [x] 检查切换状态文件为 `success`；若为 `rolled_back`，读取脱敏失败阶段并回到相应
      Phase，不宣告完成。
- [x] `paseo daemon status --json`：home 为 `/home/qlqq/.paseo`、owner UID 1000、
      listen 为 `0.0.0.0:6767`、CLI/daemon 版本 `0.6.1`。
- [x] qlqq user service enabled/active；root system service disabled/inactive；linger=yes。
- [ ] **用户批准跳过：** 不重跑 Claude/Codex diagnostics，也不各自新建切换后的只读
      UID/cwd/Git smoke。Phase 7 的 preview smoke 历史证据仍保留，但不伪装成本轮重跑。
- [ ] 原定“全部 active cwd 都位于 ROOMusic”这一严格条件当前不成立：Paseo 当前有
      ROOMusic 和 `CLIProxyAPI` 两个非 root 项目/工作区；两者均不含 `/root`，且 ROOMusic
      cwd 正确。额外项目不属于迁移范围，本轮未擅自删除或归档。
- [x] 6767 listener、Paseo supervisor/daemon、terminal worker 和当前 provider 进程均为
      UID 1000。
- [x] Docker 容器 ID、volume 名、端口与健康状态在本轮表面检查中正常；结合 Phase 9
      已完成的一致性门禁，现有 PostgreSQL 容器仍为 `roomusic` 项目、使用既有 volume、
      绑定 `127.0.0.1:5432` 且健康。本轮未查询或修改业务表。
- [ ] **用户批准按表面检查收口：** 本轮确认 `/root` 仍为 `root:root`、`0700`，且 root
      systemd unit/drop-in/env 回滚控制文件仍存在、owner/mode 正确；qlqq 无权穿越
      `/root` 且无免密 sudo，因此未重新读取或计算 root 工作区、Paseo home、工具目录和
      私有快照的 hash。Phase 9 已有完整校验证据，本轮不把它伪装成重新通过。
- [x] 在任务目录写入只含聚合结果和实际命令的中文 `migration-report.md`；不写 secret、
      真实音乐路径清单或 Agent 正文。
- [x] 按用户要求完成受限版 Trellis 表面复核；本次是一次性主机迁移，没有新增产品代码
      约定或可执行合同，不更新项目 spec。
- [x] 由主会话完成提交和 finish-work。提交不得包含 qlqq home、快照、env、音乐或
      Docker 数据。

收尾记录（2026-09-04）：用户已明确接受报告中的 CLI 自动升级与额外非 root 工作区
偏差，并批准按当前表面检查结果提交、归档和记录 journal；不推送远端。

最终表面检查另发现两项非破坏性偏差：Claude Code 当前版本为 `2.1.260`、Codex CLI 为
`0.153.2`，均高于规划锁定的 `2.1.251`、`0.151.0`；两个 binary 仍由 qlqq 的 mise
Node `24.16.0` 提供，不依赖 `/root`。本轮文档收口不擅自降级工具，偏差记录在
`migration-report.md`，由主会话决定是否作为后续维护事项。

## 人工回滚命令顺序

若客户端无法连接且自动回滚也失败，从现有 SSH/root 控制通道按顺序执行：

1. `systemctl --user --machine=qlqq@ stop paseo.service`
2. 确认 `ss` 显示 6767 已释放。
3. `systemctl enable paseo.service`
4. `systemctl start paseo.service`
5. 访问 `http://127.0.0.1:6767/api/health` 并运行 root
   `paseo daemon status --json`。

不得删除 `/home/qlqq/workspace`、`/home/qlqq/.paseo`、root 快照或任何 Docker volume。

## 计划验证命令清单

实际执行后，`migration-report.md` 与最终说明必须逐项列出结果：

```bash
id qlqq
loginctl show-user qlqq -p Linger -p State -p RuntimePath
runuser -u qlqq -- bash -lc 'mise current && go version && node --version && npm --version'
runuser -u qlqq -- bash -lc 'claude --version && codex --version && paseo --version && trellis --version'
runuser -u qlqq -- docker version
runuser -u qlqq -- docker compose -p roomusic ps
git -C /home/qlqq/workspace/ROOMusic fsck --full
git -C /home/qlqq/workspace/ROOMusic status --short --branch
sha256sum /home/qlqq/workspace/ROOMusic-migration.tar.gz
runuser -u qlqq -- env XDG_RUNTIME_DIR=/run/user/1000 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus systemd-analyze --user verify /home/qlqq/.config/systemd/user/paseo.service /home/qlqq/.config/systemd/user/paseo-preview.service
curl --fail --silent --show-error http://127.0.0.1:6768/api/health
paseo provider diagnostic claude --json --host 127.0.0.1:6768
paseo provider diagnostic codex --json --host 127.0.0.1:6768
(cd /home/qlqq/workspace/ROOMusic/backend && go test ./... -count=1 && go build ./...)
(cd /home/qlqq/workspace/ROOMusic/frontend && npm ci && npm run lint && npm run typecheck && npm run test -- --run && npm run build)
bash -n /home/qlqq/workspace/ROOMusic/scripts/*.sh
git -C /home/qlqq/workspace/ROOMusic diff --check
curl --fail --silent --show-error http://127.0.0.1:6767/api/health
systemctl --user --machine=qlqq@ status paseo.service --no-pager
systemctl status paseo.service --no-pager
```

需要密码的 Paseo CLI 命令在实现时通过 `0600` service env 注入；实际报告不展示其值。

# ROOMusic 迁移到 qlqq：最终表面检查报告

## 结论

检查时间：2026-09-04（最后一次取证时间 `2026-09-04T05:26:22Z`）。

Paseo 已完成从 root system service 到 qlqq user service 的切换。持久状态记录为
`success`，qlqq 服务 enabled/active，root 服务 disabled/inactive，`6767` 健康检查
返回 HTTP 200；Paseo daemon、监听进程和当前 cgroup 内可见进程均为 UID 1000，工作
目录不在 `/root`。现有 PostgreSQL 容器保持健康，既有 volume 与端口仍在使用。目标
工作区、V0 目录、固定归档和关键配置均存在，敏感文件权限符合预期。

本报告是用户于 2026-09-04 明确要求的“只做表面检查、不再测试”收口证据，不等价于
重新执行完整质量门禁。核心迁移运行态正常，但有两项不影响非 root 切换本身的偏差：

- Claude Code 当前为 `2.1.260`、Codex CLI 当前为 `0.153.2`，高于规划锁定的
  `2.1.251`、`0.151.0`；解析路径仍全部位于 `/home/qlqq`。
- Paseo 当前除 ROOMusic 外还有一个 `CLIProxyAPI` 项目/工作区；所有项目和工作区均无
  `/root` 引用，ROOMusic cwd 正确，但原计划“全部 active cwd 均为 ROOMusic”的严格
  条件不成立。本轮未改动这个迁移范围外的项目。

## 聚合检查结果

| 检查项 | 结果 |
| --- | --- |
| 切换状态 | `status=success`，`failed_stage=none`，完成于 `2026-09-04T03:52:08Z`；状态目录 `0700`、文件 `0600`，owner 为 `qlqq:qlqq` |
| systemd | qlqq `paseo.service` enabled/active；preview disabled/inactive；root `paseo.service` disabled/inactive；`Linger=yes` |
| Paseo daemon | home `/home/qlqq/.paseo`，owner `1000@qlqq`，监听 `0.0.0.0:6767`，CLI/daemon 均为 `0.6.1`，local daemon running 且 reachable |
| 监听与健康 | `6767` 由 UID 1000 的 Paseo 进程监听，HTTP 200；`6768` 无监听且连接失败，符合 preview 已停止的预期 |
| 进程归属 | user unit MainPID、Paseo listener、supervisor/daemon、terminal/provider 可见进程均为 UID 1000；Paseo cgroup 的 `/root` cwd 计数为 0 |
| 项目/工作区 | 共 2 个项目、2 个 active workspace；无 `/root` 路径。ROOMusic cwd 正确，另有迁移范围外的 `CLIProxyAPI` |
| 用户与工具链 | `qlqq` 为 UID/GID 1000，属于 `docker` 组；mise `2026.8.16`、Go `1.25.10`、Node `24.16.0`、npm `11.13.0`、Paseo `0.6.1`、Copilot `1.0.82`、Trellis `0.6.16`；所有路径位于 `/home/qlqq` |
| CLI 版本偏差 | Claude Code `2.1.260`、Codex CLI `0.153.2`，与任务锁定基线不一致；本轮未擅自降级 |
| Docker | qlqq 可直接连接 daemon；`roomusic-postgres-1` 为 `roomusic` Compose 项目，使用 `roomusic_postgres-data`，绑定 `127.0.0.1:5432`，状态 healthy；未访问业务表 |
| 工作区与配套资产 | ROOMusic、`.git`、`music/`、`backend/data/`、`frontend/node_modules/`、ROOMusic-V0 和固定归档均存在且为 `qlqq:qlqq`；未枚举真实音乐文件 |
| 固定归档 | SHA-256 为 `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`，与规划基线一致 |
| 敏感权限 | 项目 `.env`、`.env.dev`、Paseo/Codex 配置与 service env 为 `0600`；对应私有目录为 `0700`；未读取或输出任何值 |
| 回滚控制面 | `/root` 为 `root:root`、`0700`；`/etc` 下 root Paseo unit、drop-in 与两个 env 文件仍存在，owner/mode 符合记录。qlqq 无权穿越 `/root`，因此未重算 root 私有资产 hash |
| Git/Trellis | HEAD `38d545ee3dc785315da16ddfec0138e328662a50`，分支 `task/real-library-smoke-v0-comparison`；迁移任务目录仍未跟踪。`task.py list` 显示任务 `in_progress`，`task.py current` 无当前指针 |
| 任务文档校验 | `task.py validate 09-03-migrate-roomusic-to-qlqq` 通过；`implement.jsonl` 与 `check.jsonl` 各 4 项，均有效 |
| Git 轻量完整性 | `git diff --check` 通过；`git fsck --full --no-dangling` 通过，未发现缺失或损坏对象 |
| 配置 root 引用 | Paseo config、两个 user unit、Codex config、Claude settings 与 qlqq Git config 的定向检查均未发现 `/root` |

## 实际执行的轻量检查命令

以下命令只读取状态；对 JSON 仅抽取非敏感字段，对配置只检查权限或 `/root` 字面引用：

```bash
id qlqq
loginctl show-user qlqq -p Linger -p State -p RuntimePath

python3 ./.trellis/scripts/get_context.py
python3 ./.trellis/scripts/get_context.py --mode phase
python3 ./.trellis/scripts/get_context.py --mode packages
python3 ./.trellis/scripts/task.py current
python3 ./.trellis/scripts/task.py list
jq '{status, assignee, title, slug}' .trellis/tasks/09-03-migrate-roomusic-to-qlqq/task.json

git rev-parse --verify HEAD
git branch --show-current
git status --short --branch
git diff --check
git fsck --full --no-dangling
python3 ./.trellis/scripts/task.py validate 09-03-migrate-roomusic-to-qlqq

systemctl --user is-enabled paseo.service
systemctl --user is-active paseo.service
systemctl --user is-enabled paseo-preview.service
systemctl --user is-active paseo-preview.service
systemctl is-enabled paseo.service
systemctl is-active paseo.service
systemctl --user show paseo.service -p MainPID -p ActiveState -p SubState -p UnitFileState -p FragmentPath -p WorkingDirectory -p ControlGroup
systemctl --user show paseo-preview.service -p MainPID -p ActiveState -p SubState -p UnitFileState -p FragmentPath
systemctl show paseo.service -p MainPID -p ActiveState -p SubState -p UnitFileState -p FragmentPath

awk -F= '$1=="status" || $1=="completed_at" || $1=="failed_stage" || $1=="root_service_active" || $1=="root_service_enabled" || $1=="qlqq_service_active" || $1=="qlqq_service_enabled" {print}' /home/qlqq/.local/state/roomusic-migration/phase9-switch.status
stat -c '%n|%a|%U:%G|%s' /home/qlqq/.local/state/roomusic-migration /home/qlqq/.local/state/roomusic-migration/phase9-switch.status

ss -H -ltnp 'sport = :6767'
ss -H -ltnp 'sport = :6768'
curl --fail --silent --show-error --output /dev/null --write-out '%{http_code}\n' http://127.0.0.1:6767/api/health
curl --silent --show-error --output /dev/null --write-out '%{http_code}\n' --connect-timeout 2 http://127.0.0.1:6768/api/health
ps -o uid=,gid=,pid=,ppid=,comm= -p <MainPID或监听PID>
readlink -f /proc/<MainPID或监听PID>/cwd
find /sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/paseo.service -type f -name cgroup.procs -exec sed -n '/^[0-9][0-9]*$/p' {} +

bash -lc 'command -v mise go node npm claude codex copilot paseo trellis'
bash -lc 'mise --version; mise current; go version; node --version; npm --version; claude --version; codex --version; copilot --version; paseo --version; trellis --version; npm prefix -g'
bash -lc 'npm list --global --depth=0 --json | jq <仅选择五个迁移 CLI 的包名和版本>'
bash -lc 'paseo daemon status --json | jq <仅选择 home/owner/listen/version/node/连接状态>'
bash -lc 'paseo project ls --json | jq <仅聚合数量和路径归属>'
bash -lc 'paseo workspace ls --json | jq <仅聚合数量和 cwd 归属>'

docker version --format 'client={{.Client.Version}} server={{.Server.Version}}'
docker ps --filter label=com.docker.compose.project=roomusic --format '{{.ID}}|{{.Names}}|{{.Image}}|{{.Ports}}|{{.Status}}'
docker inspect roomusic-postgres-1 --format '<仅选择容器 ID、健康、Compose 项目、volume 与端口>'
docker volume inspect roomusic_postgres-data --format 'name={{.Name}} driver={{.Driver}}'

stat -Lc '%n|%F|%a|%U:%G|%s' <明确列出的目标工作区、V0、固定归档与关键配置路径>
stat -Lc '%n|%F|%a|%U:%G|%s' /etc/systemd/system/paseo.service /etc/systemd/system/paseo.service.d/codex.conf /etc/paseo.env /etc/paseo-codex.env
sha256sum /home/qlqq/workspace/ROOMusic-migration.tar.gz
rg -q --fixed-strings '/root' <明确列出的 qlqq 非敏感运行配置文件>
```

尝试通过 `sudo -n` 只读列出 root 私有迁移状态目录时，系统按预期要求密码；本轮没有
绕过该权限边界。root 内部工作区、Paseo home、工具目录及私有快照的全量 hash/owner
复核因此未重跑，沿用 Phase 9 已记录的完整一致性证据。

## 按用户要求未运行的验证

以下项目在本轮明确标记为“用户批准跳过”，不计为通过：

- `mise run env-check`。
- 后端 `go test ./... -count=1`、`go build ./...`。
- 前端 `npm ci`、`npm run lint`、`npm run typecheck`、`npm run test -- --run`、
  `npm run build`。
- `bash -n scripts/*.sh`、`systemd-analyze --user verify` 与 Compose 配置校验。
- Claude/Codex provider diagnostics，以及切换后新建 Agent 的 UID/cwd/Git smoke。
- 全量测试、全量 CI、PostgreSQL 集成测试、业务表查询和真实音乐扫描。

Phase 7、Phase 8 和 Phase 9 中已有的历史验证结果保留在 `implement.md`，但本报告没有把
这些历史结果表述为本轮重新通过。

## 收尾边界

本轮未修改产品代码、Docker 数据、真实音乐、用户配置、systemd 状态或 Trellis spec；
也未提交、归档任务或写 developer journal。主会话可基于本报告决定是否接受上述版本与
额外工作区偏差，然后显式提交并完成 Trellis finish-work。

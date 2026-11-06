# 备份内置到应用：执行计划

设计：[design.md](design.md)。分工按 `~/.claude/CLAUDE.md`：后端 `codex/gpt-6-astra`（high，`full-access`），前端 `claude/claude-opus-5-5`（high），
测试编写与执行 `codex/gpt-6.1-sol`（high，`full-access`）。实现 agent 只写代码，只跑 typecheck、vet、build 和已有测试；新测试由 Sol 编写，Sol 发现实现缺陷时报回，不自己修。
每一步通过主会话验收后，由主会话单独提交一次（中文提交信息，不推送）。

## 所有派活 prompt 都要写明的约束

- 不碰生产 `roomusic-prod` 的容器与卷、`/home/qlqq/roomusic-backups`、仓库根 `.env.prod`，也不碰开发库 `roomusic-postgres-1`。
- 本机**不得创建未固定子网的 Docker 网络**；需要网络时用 `10.203.51.0/24`，或 `docker run` 的默认 bridge、`--network host`。
- 数据库测试只用 `roomusic-test` 隔离项目（`--env-file /dev/null`、绝对 compose 路径、空闲端口、随机库名，结束后 `down -v`），确认集成测试是 PASS 而不是 SKIP。
- 改前端源码要重建 `backend/cmd/roomusic/web` 并与源码一起提交；mock Playwright 跑完 `git status` 必须干净。
- 界面文案遵守 `~/.claude/CLAUDE.md`“前端界面文案”三条（写进前端 prompt 原文）。

## 步骤

| 步骤 | 执行者 | 内容 | 设计章节 |
| --- | --- | --- | --- |
| B0 | astra（随 B1 一起） | `compose.test.yaml` 的 postgres 服务加 `network_mode: bridge`，测试库改用 Docker 已有的默认 bridge，不再创建项目网络（本机自动分配的网段会落在 `192.168.48.0/20`、覆盖局域网，现有 `test-integration.sh` 与 `playback-e2e.sh` 下一次运行就会触发）；核对两个脚本与 `playback-e2e.sh` 的残留检查在没有项目网络时仍正常。**在任何测试运行之前完成** | — |
| B1 | astra | 配置项（`ROOMUSIC_BACKUP_DIR`、`ROOMUSIC_TIMEZONE`、`ROOMUSIC_BACKUP_HOUR`、`ROOMUSIC_BACKUP_KEEP_DAYS`、`ROOMUSIC_PG_BIN_DIR`）；`internal/platform/buildinfo`（只有读取函数与 ldflags 变量，注入由 versioned-release 做）；`internal/backup`（调度纯函数、Service、文件命名与列表、状态文件、advisory lock、pg_dump/pg_restore 调用含 `--lock-wait-timeout`）；`postgres.Open` 选项与 `MigrationBackups` 接口及迁移前备份调用点（遵守“备份前只持有 AccessShareLock”规则）；`application_name=roomusic`；HTTP 四个接口；`roomusic backup`、`roomusic restore` 子命令；Dockerfile 换 `alpine:3.23` + `postgresql18-client`、预建 `/backups`（1000:1000 0700）、HEALTHCHECK 宽限 5 分钟；`scripts/pg-client-pinned.sh`，`scripts/dev.sh` 与 `scripts/test-integration.sh` 导出 pg 工具目录；`main.go` 装配与停机等待 | §1.1、§2、§3、§5、§7 |
| B2 | Opus（与 B1 并行） | 路由 `/system/backups`、导航“系统”组、`BackupsPage`（状态区、备份文件表、立即备份、下载直链）、`useBackupOverview` / `useStartBackup` / `useBuildInfo`、解码器、`shared/lib/bytes.ts`、文案按 §4.3；重建内嵌产物 | §1.2、§4 |
| B3 | Sol | 按 §8 编写并运行测试：A2 调度表驱动、A3 备份集成、A4 旧文件名、A5 迁移前备份（“库缺回退列 + 有待执行迁移时不挂起”属于 versioned-release V2）、A6 接口与路径穿越、A7 Vitest 与 mock Playwright、A8 恢复集成；生成 API 样本；产出 1440 与 375 截图交主会话 | §8 |
| B4 | 主会话 | 审阅 diff、测试输出与截图（多余或难懂的说明当作问题打回）；通过后提交 | — |

R10 的文件删除、compose 改写与 A9 的残留检查放在 [config-trim](../11-06-config-trim/implement.md) 的 C1。

## 验证命令（最小集）

```bash
cd backend && gofmt -l . && go vet ./... && go test ./internal/backup/... ./internal/platform/... ./cmd/roomusic/ ./internal/app/
./scripts/test-integration.sh            # 须看到 TestPostgreSQLBackup*、TestPostgreSQLMigrationPreBackup* 为 PASS
cd frontend && npm run lint && npm run typecheck \
  && npx vitest run src/app/system-backups.test.tsx src/shared/lib/bytes.test.ts src/app/shell/nav-items.test.ts src/test/api-contract.test.ts \
  && npm run test:e2e -- system-backups && git status --short
```

全量 Vitest、全量 Playwright 与 `-race` 只在父任务收尾的导出树门禁里跑一次。

## 回滚点

- B1、B2 各自独立提交；任何一步不通过就打回原 agent，不提交。生产在本任务期间不变（仍是旧镜像），不需要运行时回滚。

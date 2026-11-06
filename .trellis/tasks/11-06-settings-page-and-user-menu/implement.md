# 执行计划：设置页、侧边栏用户菜单与删除备份

> 合同与结构见 `design.md`；需求与验收见 `prd.md`。分工按 `~/.claude/CLAUDE.md`：后端实现 `codex/gpt-6-astra`，前端实现 `claude/claude-opus-5-5`，编写与执行测试 `codex/gpt-6.1-sol`（codex 需显式 `modeId: "full-access"`）。子 agent 不提交 git，主会话验收后提交。

## 步骤

### S1 实现（后端、前端并行）

- **S1-B 后端（astra）**：`design.md` §1、§1b、§1c。
  - `backend/internal/backup/types.go`：`ErrNotWritable`，Overview 加 `retention`。
  - `backend/internal/backup/files.go`：`DeleteBackup`、`DeleteBackups`。
  - `backend/internal/backup/retention.go`（新）：规则文件读写（原子替换）、`retentionPlan` 纯函数、`retain()` 迁入并按规则清理（`retentionMu`、已不存在静默跳过）、保存规则 + 立即清理。
  - CLI `roomusic backup` / `restore` 路径若调用清理，同样读规则文件。
  - `backend/internal/backup/httpapi/handlers.go`：`DELETE /api/v1/system/backups/{name}`、`POST /api/v1/system/backups/deletions`、`PUT /api/v1/system/backups/retention`。
  - 只跑 `go build ./... && go vet ./internal/backup/...` 与 `go test ./internal/backup/...`（现有测试），不写新测试。
- **S1-F 前端（opus）**：`design.md` §2–§5、§7。
  - `shared/ui/menu-button.tsx` 抽 `useMenu`（向上弹出、图标、对话框内渲染），`MenuButton` API 不变。
  - `app/shell/user-menu.tsx` 替换 `identity-panel.*`；`app-shell.tsx` 两处替换。
  - `app/shell/nav-items.ts` 删四项与“系统”分组。
  - `app/routes/settings-sections.ts`、`settings-layout.tsx`、`account-page.tsx`；`router.tsx` 新路由树 + 旧地址重定向；`styles/tokens.css` 新 token。
  - §2.3 列出的全部站内链接改新地址；注释文案“系统 › 备份”→“设置 › 备份”。
  - `features/system`：解码器加 `retention`、删除 / 批量删除 / 保存规则接口与 hooks、`backup-files-section.tsx` 删除列、勾选列、“全选” + “删除所选（n）”与确认框（部分失败只重试剩下的）、新“自动清理”区段（§3.7）、`backup-messages.ts`。
  - `src/test/system-data.ts`、`src/test/fake-system.ts` 替身支持三个新接口（供后续测试用）。
  - 用户文档：`README.md`、`deploy/production/README.md`、`deploy/production/example.env`（含降级提示）。
  - 只跑 `npm run typecheck`（或项目等价命令）与 lint，不改测试文件。
  - 界面文案约束写进 prompt。
- **验收门槛**：主会话审 diff；grep 门槛 `rg -n '"/(library|members|operations|system/backups)\b|`/(library|members|operations)\b' frontend/src --glob '!*.test.*'` 只剩 `router.tsx` 的重定向定义。

### S2 后端测试与提交（sol）

- 按 `design.md` §6.1：`DeleteBackup` / `DeleteBackups`、`retentionPlan`（天数、份数、不限、最新一份永不删）、规则文件读写与损坏、`retain` 用例；`system_api_integration_test.go` 三个新接口的授权 / 来源 / 路径安全 / 校验 / 确认语义（不带 confirm 不改任何东西）/ 不可写；重启与“恢复数据库”后规则不变；`system_samples_integration_test.go` 新录样本并重录因 Overview 加字段而变化的现有 system 样本。
- 数据库测试只用隔离的 `roomusic-test` Compose 项目（`--env-file /dev/null`、随机库名、`down -v`），确认集成测试是 PASS 不是 SKIP。
- 命令：`go test ./internal/backup/...`；`./scripts/test-integration.sh` 限定到 `TestPostgreSQLSystem*`（含 `ROOMUSIC_UPDATE_API_SAMPLES=1` 生成样本后再跑一次无更新）。
- 实现缺陷回报主会话转给 astra，不自行修产品代码。
- 通过后主会话提交：`feat(backup): 删除、批量删除备份与可设置的保留规则`。

### S3 前端测试与提交（sol）

- 按 `design.md` §6.2–§6.3 更新受影响 Vitest 与模拟后端 Playwright，并新增 `settings.test.tsx`、`user-menu.test.tsx`、`settings-sections.test.ts`、`menu-button.test.tsx` 追加、`system-backups.test.tsx` 追加、样本解码。
- 只跑受影响文件：`npx vitest run <文件列表>`；`npx playwright test <受影响 spec 列表>`（含 `player.spec.ts` 中菜单相关用例，覆盖 `MenuButton` 回归）。
- 前端缺陷回报主会话转给 opus。
- 通过后重建嵌入资源 `backend/cmd/roomusic/web`，与前端源码同一提交：`feat(web): 设置页、侧边栏用户菜单与删除备份`。

### S4 收尾

1. 真实服务：sol 更新 `frontend/e2e/real/` 中 `account`、`claims`、`governance`、`scan-feedback`、`playback` 五个文件并跑 `scripts/playback-e2e.sh`（先确认自己的 `roomusic-test` 已 `down`）。
2. 导出树检查：astra 在 `git checkout-index -a --prefix=<tmp>/` 导出的树里跑 S2/S3 的相关测试一遍；`git diff --cached --check` 干净。
3. 截图：1440 与 375，场景为设置 › 账号、设置 › 备份（含删除确认框、批量选择、自动清理区段与其确认框）、用户菜单展开（宽屏）、窄屏对话框内用户菜单展开；主会话先自审布局（文字行数、元素位置），有问题打回 opus 并补回归断言。截图附在结果里给用户看。
4. 规格同步（`design.md` §8）：`core0-runtime-contracts.md`、`component-guidelines.md`、`frontend/DESIGN.md`、`DESIGN-components.md`。
5. PRD 写实施结果 → `task.py archive` → 链接修正 + journal 同一提交。

## 回滚点

- 每个步骤单独提交，`git revert` 对应提交即可；无迁移、无配置。
- 风险文件：`shared/ui/menu-button.tsx`（波及播放器模式菜单与曲目更多菜单）、`router.tsx`（全站路由）。

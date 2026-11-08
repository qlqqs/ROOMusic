# 执行计划：歌单与专辑页面布局统一、图标化按钮与管理员改密

需求见 [prd.md](./prd.md)，设计见 [design.md](./design.md)（§8 为主会话裁决）。

## 分工（按 `~/.claude/CLAUDE.md`）

| 角色 | Paseo 参数 | 负责 |
| --- | --- | --- |
| 后端实现 | `codex/gpt-6.1-sol`，`thinkingOptionId: high`，`modeId: full-access` | 步骤 1 |
| 前端实现 | `claude/claude-opus-5-5`，`thinkingOptionId: high`，`modeId: bypassPermissions` | 步骤 2–6，同一个 agent 依次做 |
| 测试 | `codex/gpt-6.1-sol`（独立于后端实现 agent），`high`，`full-access` | 每步写/改测试并运行；收尾截图与真实服务用例 |

- 实现 agent 只写代码，跑 typecheck / vet / lint；测试 agent 发现实现问题回报，不自己改实现。
- 子 agent 不提交 git；主会话验收后按步提交（中文提交信息，前端改动同一提交里重建并提交 `backend/cmd/roomusic/web`）。
- 派活 prompt 写明：界面文案约束、测试只做必要的（只跑本步列出的测试，不跑全量，不加假想场景测试，不堆兜底）、改行为合同时 grep `frontend/e2e/real/` 与 `scripts/playback-e2e.sh`。
- 步骤 1 与步骤 2 之后的前端步骤可以并行（文件不重叠）；前端各步串行。提交时只暂存本步文件。

## 步骤

### 1. R1 后端：管理员改自己的密码不核对当前密码
- 实现：`backend/internal/identity/passwords.go` 新增 `ChangeAdminOwnPassword`（抽出锁会话小函数）；`httpapi/passwords.go` 按 `user.Role == "admin"` 分派；`.trellis/spec/backend/identity-contracts.md` 按 design §1.3 更新。
- 测试：`identity_api_integration_test.go` 新增管理员不带/带错当前密码都 200 的用例；`login_throttle_integration_test.go` 两个限速用例改用成员。
- 验证：`go vet ./...`；隔离测试库（`roomusic-test` Compose 项目，`--env-file /dev/null`、绝对路径、空闲端口、随机库名、结束 `down -v`）上 `go test ./internal/identity/... ./internal/app/ -run 'ChangeOwnPassword|AdminChangeOwnPassword|PasswordThrottle'`，确认是 PASS 不是 SKIP。
- 提交：`feat(identity): 管理员修改自己的密码不再需要当前密码`。

### 2. R1 前端：改密对话框按角色隐藏“当前密码”（依赖步骤 1 合入）
- 实现：`features/auth` 的 `endpoints.ts`、`account-rules.ts`、`change-password-dialog.tsx`（design §1.4）。
- 测试：`account-rules.test.ts`、`account-password.test.tsx`、`src/test/fake-api.ts`；e2e `support/api-mock.ts`、`support/account-password.ts`、`auth.spec.ts` 自助改密一组。
- 验证：`npx vitest run` 上述文件；`npx playwright test e2e/auth.spec.ts --grep 密码`。

### 3. R3 快速悬停提示 + 专辑页管理按钮图标化
- 实现：`shared/ui/tooltip.tsx(+css)`、`button.tsx` 去 `title` 接 `useTooltip` 与 `tooltip` prop、`favorite-button.tsx`、`primary-nav.tsx`、`release-admin-actions.tsx`（图标、busy 的 `LoaderCircle`、“查看问题”图标链接）。
- 测试：新 `tooltip.test.tsx`（4 条）、`favorite-interactions.test.tsx` 的 title 断言、`e2e/governance.spec.ts` 加一条悬停提示 + 页面无 `button[title]`。
- 验证：相关 Vitest 文件；`npx playwright test e2e/governance.spec.ts`。

### 4. R5 专辑概况单行
- 实现：`formatSpokenDuration` 移到 `shared/lib/duration.ts`（personal 改引用，单位统一“分钟”）、`album-header.tsx` 的 `factsOf` 与样式（design §5）。
- 测试：`duration.test.ts`、`playlist-text.test.ts`、`album-detail.test.tsx`、`e2e/albums.spec.ts:175`。
- 验证：上述 Vitest；`npx playwright test e2e/albums.spec.ts`。

### 5. R2 统一曲目表格
- 实现：`features/personal/components/track-table.tsx(+css)`，接入 `liked-tracks-view.tsx`、`playlist-detail-view.tsx`（design §2）。
- 测试：`favorites.test.tsx`、`playlists.test.tsx`（行查询、列头顺序各一条）、`e2e/personal.spec.ts` 的行查询。
- 验证：上述 Vitest；`npx playwright test e2e/personal.spec.ts`。

### 6. R4 网易云式顶部
- 实现：`Page.header`、`collection-hero.tsx`、`shared/ui/avatar.tsx`（用户菜单改用它）、`LikedCover`、`useArtworkTint`、`formatDate`、`PersonalPlayAll.trailing`、“歌曲 N”与焦点目标，路由文件去掉外层 Page（design §4）。
- 测试：`favorites.test.tsx`、`playlists.test.tsx`、`user-menu.test.tsx` 中受影响断言；`e2e/personal.spec.ts`。
- 验证：上述 Vitest；`npx playwright test e2e/personal.spec.ts`。

### 7. 收尾
- 测试 agent：`npx tsc --noEmit`；受影响 Playwright 规格一次（auth、governance、albums、personal，加 `responsive`/`a11y` 中相关场景 `--grep`）；`e2e/real/personal.real.ts` 一次；1440 与 375 截图（账号改密对话框、专辑详情含管理操作区、我喜欢的音乐、歌单详情），用临时脚本，不提交。`git status` 确认构建产物无漂移。
- 主会话审截图，布局问题打回前端 agent；之后 Phase 3：规范同步（design §7 末段列出的文件）、PRD 实施结果、归档、链接修正、日志。

## 回滚点

每步一个提交，`git revert` 单个提交即可回退；步骤 2 依赖 1，步骤 6 依赖 3、4、5。

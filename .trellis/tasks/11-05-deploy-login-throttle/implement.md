# 执行计划：登录限速

依据：[prd.md](./prd.md)、[design.md](./design.md)。在 origins 提交后开始；可与 packaging 第 1 步并行（改动文件不重叠）。

## 步骤

| 步 | 执行者 | 内容 | 产出 |
| --- | --- | --- | --- |
| 1a | 后端实现（`codex/gpt-6-astra`，high，full-access） | 按 design §1–§9、§11 实现：限速器（可注入时钟、内存上限）、登录流程（先计数后核对、用户不存在的等价 bcrypt、数据库错误 503）、密码哈希指纹解除、改密码核对限速（T9）、`429 too_many_attempts` 与 `Retry-After`、日志事件、`identityapi.New` / `app.New` 装配；更新身份合同与错误码表 | 改动文件清单；`gofmt`、`go vet`、现有 identity 与 app 包测试结果 |
| 1b | 前端实现（`claude/claude-opus-5-5`，high） | 按 design §10：登录表单与改密码对话框的 `too_many_attempts` 文案映射、`api-contract.test.ts` 登记；重建 `backend/cmd/roomusic/web`。文案只用那一句日常话，不解释原因、不写分钟数、不提安全 | 改动文件；lint、typecheck、相关 Vitest、build 结果 |
| 2 | 测试（`codex/gpt-6.1-sol`，high，full-access） | 按 design §12 写测试：限速器单元测试（注入时钟，覆盖 B1–B4、B5c）、登录与改密码的集成用例、接口错误样本、前端登录表单与改密码对话框的 Vitest（B5、B5b）；`rg auth/login` 复核现有集成与真实服务测试不会被误限速（含 `frontend/e2e/real/`、`scripts/playback-e2e.sh`）。发现实现缺陷时回报 | 新增测试清单、命令与结果 |
| 3 | 主会话 | 审阅、对照 B1–B6 验收；通过后提交一次（含重建的前端产物） | 提交哈希 |

## 验证命令（只跑这些）

```bash
(cd backend && test -z "$(gofmt -l ./internal/identity ./internal/app)")
go -C backend test ./internal/identity/... -count=1
go -C backend vet ./internal/identity/... ./internal/app
# 集成：roomusic-test 隔离库，只跑身份与接口样本相关用例（具体 -run 见 design §12）
(cd frontend && npm run lint && npm run typecheck && npx vitest run <登录表单、改密码对话框、api-contract 相关测试文件>)
(cd frontend && npm run build) && git status --short backend/cmd/roomusic/web
```

## 注意

- 1a 与 1b 并行：合同已在 design 固定（`429`、`too_many_attempts`），1b 不等 1a。
- 不跑全量 Vitest、全量 `-race` 或 Playwright；这些留到父任务收尾一次。
- 子 agent 不提交 git。

## 回滚点

单个提交；回退即去掉限速。没有迁移，状态只在进程内存。

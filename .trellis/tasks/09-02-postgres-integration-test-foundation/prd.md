# PostgreSQL 集成测试基础设施

## Goal

建立可重复的 PostgreSQL 集成测试环境，覆盖 Core 0 事务、目录和扫描关键行为。

## Requirements

- TBD

## Acceptance Criteria

- [ ] TBD

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
# PostgreSQL 集成测试基础设施

## 目标

建立可重复、可隔离的 PostgreSQL 集成测试入口，让 Core 0 的事务、权限、目录操作和扫描持久化行为在真实数据库上持续回归，而不是因未设置环境变量被静默跳过。

## 已确认事实

- 后端集成测试位于 `backend/cmd/roomusic/auth_root_operations_integration_test.go`，通过 `ROOMUSIC_TEST_DATABASE_URL` 连接 PostgreSQL。
- 每个测试创建独立 schema，测试结束后删除；应用连接使用该 schema 的 `search_path`。
- `openDatabase` 会执行嵌入式迁移并恢复中断扫描。
- Compose 提供 PostgreSQL 服务，但还包含 Redis、Meilisearch；当前测试只需要 PostgreSQL。
- `.mise.toml` 提供 `env-up` 等本地依赖任务，Makefile 目前仅包装基础 Go 测试。

## 范围内

1. 提供只启动 PostgreSQL 的可重复测试环境或脚本入口，并明确默认连接 URL、凭据和清理方式。
2. 将集成测试命令纳入项目文档与质量检查约定；缺少测试数据库时必须有清晰提示，不能误报完整通过。
3. 补充真实 PostgreSQL 回归用例，至少覆盖：用户禁用与会话撤销的原子性、最后管理员保护、未知用户、目录重复注册/修订冲突、多 Medium 扫描归属。
4. 保持测试 schema 隔离、测试后清理和现有 API wire 契约不变。

## 范围外

- 不在本任务重写迁移执行器的生产治理（迁移锁、校验和、按版本跳过另立任务）。
- 不实现持久化扫描取消 API、Redis/Meilisearch 测试或端到端浏览器测试。
- 不改变生产数据库 schema 或业务 API 行为，仅为测试可验证性补充必要的测试辅助代码。

## 验收标准

- AC1：从干净环境按文档命令启动 PostgreSQL 后，可执行明确的集成测试命令并连接成功。
- AC2：未配置测试数据库时，测试输出明确标识“跳过集成测试”及原因；单独的集成测试门禁不会伪装成通过。
- AC3：启用数据库后，现有集成测试和新增回归测试全部通过，测试之间不会共享数据或互相污染。
- AC4：事务失败或冲突场景验证数据库无半状态；最后管理员和会话撤销行为在真实 PostgreSQL 约束下可回归。
- AC5：README、Trellis 后端质量规范和任务文档记录命令、环境变量、清理策略及 CI 建议，且与实现一致。

## 关键决策

- 推荐使用 Compose 的独立 PostgreSQL 服务配置（临时项目名/端口或独立 compose 文件），不启动无关依赖。
- 测试继续使用随机 schema 隔离，而不是为每个测试创建数据库，减少权限要求并保持现有辅助函数兼容。
- 集成测试默认显式 opt-in，避免普通 `go test ./...` 在开发机上隐式依赖 Docker；CI 门禁单独执行并在缺失配置时失败。

## 待后续技术债

- 将迁移执行器改为版本跟踪、互斥锁和事务化应用。
- 增加扫描取消端点及可持久化的取消传播。

## 未决问题

无。实现前需按本 PRD 产出技术设计并经用户批准。

# Subsonic 鉴权与协议骨架：实施计划

分支 `task/subsonic-foundation`，基线 `main`，用 Orca worktree。

## 步骤

1. 【Codex 实现，astra】
   - 移动 `steward/secret` 到 `platform/secret`；
   - 写迁移；
   - `identity` 的 `PasswordObserver`；
   - `subsonic/protocol`（外壳、错误、XML/JSON 编码、参数）；
   - `subsonic/auth`；
   - `ping`、`getLicense`、`getOpenSubsonicExtensions`、`getMusicFolders`、`getUser`；
   - `/api/v1/me/subsonic*` 与 `/api/v1/admin/subsonic`；
   - 在 `app.go` 注册，更新 `routes_test.go` 和脱敏规则。

   实现代理不写新测试。
2. 【Codex 测试，sol】
   - 编码单测：外壳与错误码，XML/JSON 对照协议示例。
   - 集成测试覆盖 AC1–AC6、AC8：三种登录方式、错误码、开启前已有用户、关闭删副本、改密、日志不含凭据。
3. 【主代理前端】
   - 账号页一行、管理开关、API 解码；
   - 单测覆盖 AC7。
4. 主代理验收：审阅改动有无越界，核对子代理的验证结果，不复跑。更新规范 `.trellis/spec/backend/` 新增 `subsonic-contracts.md`，提交并合入 `main`。

## 验证命令

- `go test ./internal/subsonic/... ./internal/identity/... ./internal/platform/secret/... ./internal/app/ -run 'Route|Subsonic'`
- 集成测试按仓库约定的 `ROOMUSIC_TEST_RUN` 收窄到 Subsonic 用例。
- 前端：`npm run typecheck` 与改动涉及的测试文件。

## 回滚点

- 迁移有 down。
- `secret` 包的移动单独提交，便于回退。

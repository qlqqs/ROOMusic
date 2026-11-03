# F1：未知 /api/ 路径返回 JSON 错误

> 父任务：[Classical 重设计 / Core 1](../../2026-11/09-27-classical-core-1/prd.md)。2026-09-28 由[路线修订任务](../09-28-core-1-roadmap-revision/design.md)创建，2026-09-29 完成规划。轻量任务，只有 PRD。

## 目标与用户价值

`/api/` 下的请求无论成功还是失败，都应返回 JSON。现在未知路径和错误方法都落到前端兜底，返回 200 和 `index.html`，
前端 decoder 只能报出难以诊断的解析错误。修复后，M1a 可以依赖稳定的错误信封区分“路径不存在”“方法不对”“服务不可用”。

## 已核实事实

- 统一错误信封由 `writeAPIError`（`backend/cmd/roomusic/application.go:35`）输出；已登记的错误码见 `.trellis/spec/backend/error-handling.md:18`–`28`，含 `not_found`（404）与 `unavailable`（503）。
- `application.go:79` 把 `/` 交给 `productionFrontendHandler`（`main.go:114`），它匹配所有方法：
  - 未知的 `/api/...` 返回 200 与 `index.html`；
  - 已知接口用错误方法（如 `DELETE /api/v1/releases/{id}`）时，Go 1.22 ServeMux 优先选择同时满足方法与路径的 `/`，同样返回页面，不会出现 405。
- 数据库不可用时，`main.go:92` 的 `buildHandler` 只注册健康检查和 `/`，此时所有 `/api/` 请求也返回页面。
- 完成日志合同规定未匹配请求的 `route_template` 为 `<unmatched>`（`.trellis/spec/backend/logging-guidelines.md:80`、`core0-runtime-contracts.md:196`）。
- 现有路由相关测试位于 `core_acceptance_test.go` 与 `auth_root_operations_integration_test.go`。

## 已裁决

| 编号 | 事项 | 选择 | 选择者与日期 |
| --- | --- | --- | --- |
| D1 | 已知接口被错误方法调用 | 返回 405 JSON 错误信封，新增错误码 `method_not_allowed` 并登记到 error-handling 规范，`Allow` 头列出该路径支持的方法 | qlqq，2026-09-29 |

## 需求与验收

| 编号 | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | 未知 API 路径 | `GET /api/v1/不存在`、`POST /api/nope` 返回 404 与错误信封（`code=not_found`，带请求 ID），`Content-Type` 为 JSON；`/api` 与 `/api/` 同样处理 |
| R2 | 已知路径、错误方法 | `DELETE /api/v1/releases/{id}` 返回 405 与错误信封（`code=method_not_allowed`），`Allow` 头包含 `GET`（及随 GET 生效的 `HEAD`）；已登记的方法行为不变 |
| R3 | 降级模式 | 数据库不可用时（`buildHandler`），任何 `/api/` 请求返回 503 与错误信封（`code=database_unavailable`），不返回页面 |
| R4 | 前端兜底不受影响 | 非 API 路径（如 `/some/page`）仍返回 `index.html`；缺失的 `/assets/...` 仍为 404；`/healthz`、`/readyz` 行为不变 |
| R5 | 日志合同 | 落入 `/api/` 兜底的请求，完成日志的 `route_template` 记为 `<unmatched>`，状态码为实际的 404/405/503 |
| R6 | 规范同步 | `error-handling.md` 登记 `method_not_allowed`/405；`core0-runtime-contracts.md` 写明 `/api/` 兜底行为，并把技术债中对应条目标为已修复 |

## 技术说明

- 在两个 ServeMux 上注册 `/api/` 与 `/api` 的兜底 handler；它们比 `/` 更具体，已注册的具体路由仍然优先。
- 兜底 handler 用同一个 mux 以其他方法（`GET`、`POST`、`PUT`、`PATCH`、`DELETE`）探测当前路径：若有方法命中非兜底模式，则返回 405 并设置 `Allow`；否则返回 404。
- 完成日志中间件把兜底模式映射为 `<unmatched>`，不把 `/api/` 当作路由模板。
- 不新增依赖，不改已有接口的 JSON 形状。

## 验证方式

`backend/` 下运行相关 handler 测试（`go test ./cmd/roomusic -run '<新增测试名>' -count=1`），再运行 `go test ./... -count=1`、`go vet ./...`、`gofmt -l .`。
本任务不涉及数据库语义，不强制运行 PostgreSQL 集成测试；若改动触及集成测试文件，则补跑 `./scripts/test-integration.sh`。

## 范围外

其他路由调整；前端改动；除 `method_not_allowed` 外的新错误码；B0 的拆包（若同期进行，需协调 `application.go` 与 `main.go` 的修改顺序）。

## 最终规划决策记录

- 路线：人工选择；选择者：用户（qlqq）。
- D1 于 2026-09-29 选定；其余细节（降级模式用 503、日志记 `<unmatched>`）由现有规范推出。
- 实施修正：R3 的错误码由规划时的 `unavailable` 改为 `database_unavailable`。原因：其余接口数据库不可用时一律返回
  `503 database_unavailable`（`core0-runtime-contracts.md` 校验与错误矩阵），降级模式正是这一原因；error-handling
  规范允许功能码细化 `unavailable` 类，HTTP 语义不变。
- 状态：用户于 2026-09-29 明确批准最终规划摘要，进入实施。

## 实施结果（2026-09-29）

- `main.go`：新增 `registerAPIFallback`、`apiNotFoundHandler`、`allowedAPIMethods`，降级 handler 注册 503 兜底；
  完成日志中间件把 `/api/`、`/api` 兜底模式记为 `<unmatched>`。`application.go` 在 `/` 之前注册兜底。
- 测试：`core_acceptance_test.go` 新增 `TestAPIFallbackReturnsJSONErrors`（8 个用例覆盖 R1–R3、R5）与
  `TestAPIFallbackKeepsFrontendAndHealthRoutes`（R4，含 `/apiary` 不被误判）。撤掉修复后 9 项失败，恢复后通过。
- 规范：`error-handling.md`、`core0-runtime-contracts.md`、`logging-guidelines.md` 已同步（R6）。
- 验证：`go test ./cmd/roomusic -run 'TestAPIFallback|TestProductionFrontend|TestRequestIDMiddleware' -count=1`、
  `gofmt -l .`、`go vet ./...`、`go test ./... -count=1` 均通过；未改集成测试文件，未运行 PostgreSQL 集成测试。

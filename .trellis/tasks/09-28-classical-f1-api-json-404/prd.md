# F1：未知 /api/ 路径返回 JSON 404

> 状态：planning，未启动。2026-09-28 由[路线修订任务](../09-28-core-1-roadmap-revision/design.md)（第二部分）创建，本文是 PRD 初稿；
> 启动前需按 Trellis 流程完成规划并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

## 目标与用户价值

修正现有行为：未匹配的 `/api/...` 请求落入前端兜底，返回 200 和 `index.html`（`backend/cmd/roomusic/application.go:79`、
`main.go:114`），前端 decoder 因此报出难以诊断的错误。

## 依据

- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 阶段表的 F1 行；[ADR 0001](../../../docs/architecture/adr/0001-frontend-foundations.md) 第 8 节第 6 项。
- `.trellis/spec/backend/error-handling.md` 与 `.trellis/spec/backend/core0-runtime-contracts.md`。

## 范围

为 `/api/` 前缀注册兜底，返回既有错误信封的 JSON 404（含请求 ID）；非 API 路径的 SPA 兜底保持不变；
HTTP 完成日志的路由模板按合同记录；补回归测试，并同步运行合同文档。

## 前置依赖

无，可随时插入。若与 B0 同期进行，需要协调 `application.go` 的修改顺序。

## 验收标准（初稿）

- `GET /api/v1/不存在` 返回 404 JSON 错误信封；`GET /some/page` 仍返回 `index.html`；缺失的 `/assets/...` 仍为 404。
- 已知路由收到错误方法时，按设计处理并有测试。

## 验证方式

相关 handler 测试（`go test ./cmd/roomusic -run '<相关测试名>' -count=1`），以及 `gofmt`、`go vet`。

## 风险

与 B0 的文件冲突；方法不匹配时，ServeMux 的 405 与兜底路由的交互需要明确。

## 范围外

其他路由调整；前端改动。

## 待规划事项

- 错误码名称（沿用既有的 `not_found`）。
- 方法不匹配时的预期状态码。

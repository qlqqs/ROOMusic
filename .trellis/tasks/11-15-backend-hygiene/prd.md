# 后端依赖与服务器超时（意见 3、7）

## Goal

`go.mod` 如实区分直接与间接依赖并由 CI 防漂移；HTTP Server 回收空闲 keep-alive 连接。

## Requirements

1. 在 `backend/` 执行 `go mod tidy`，不升级任何依赖版本（只调整 `// indirect` 标注与分组）；`go.sum` 只允许 tidy 产生的变化。
2. `.github/workflows/ci.yml` 的 Backend job 在格式检查后增加 `go mod tidy -diff`。
3. `backend/cmd/roomusic/main.go:83` 的 `http.Server` 增加 `IdleTimeout: 120 * time.Second`。不设 `ReadTimeout`、`WriteTimeout`（音频流式响应与上传需要长连接，现有行为不变）。
4. 模块路径 `github.com/qlqq/roomusic/backend` 不改（只内部引用，改名波及全部 import，收益为零）。

## Acceptance Criteria

- [ ] `go mod tidy -diff` 无输出；`go list -m all` 的版本与改动前一致。
- [ ] `go build ./...`、`go vet ./...`、`go test ./cmd/roomusic/` 通过。
- [ ] CI 文件中新步骤位于 Backend job，`actionlint` 通过。

## Out of Scope

- 依赖升级、模块路径改名。

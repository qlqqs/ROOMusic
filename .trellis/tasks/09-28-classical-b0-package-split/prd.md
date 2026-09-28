# B0：后端拆包（catalog 先拆）

> 状态：planning，未启动。2026-09-28 由[路线修订任务](../09-28-core-1-roadmap-revision/design.md)（第二部分）创建，本文是 PRD 初稿；
> 启动前需按 Trellis 流程完成规划（brainstorm、design/implement、上下文清单）并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

## 目标与用户价值

把 `backend/cmd/roomusic` 的单个 `package main`（非测试代码约 8,074 行）按能力拆到 `backend/internal/`，
为阶段 I 的身份规则和后续 M3 提供明确的修改落点。纯重构，对外行为零变化。

## 依据

- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 的 B0 行；[后端目录结构规范](../../spec/backend/directory-structure.md) 的目标布局。
- [ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 第八节：B0 不借拆包改变身份和重扫语义。

## 范围（按步骤，每步单独提交、单独验证）

1. **`internal/catalog`**：organizer（`organizer.go`：候选组织、锚点、字段决策）；Release 图写入（`scanner.go:912` 的 `persistOneCandidate`，至 `:1041`）；catalog 读取查询（`catalog_api.go` 的列表、详情、证据、封面投影）。本步结束时 catalog 包不依赖 `net/http`，HTTP handler 暂留 `package main`，改为调用导出函数。
2. **`internal/library` 与 `internal/library/parser`**：扫描运行、取消、诊断（`scans.go` 与 `scanner.go` 其余部分）、目录操作（`root_operations.go`）；解析器（`audio_parser.go`、`folder_metadata.go`）。`artist_normalization.go` 按实际调用方归属。
3. **`internal/platform`**：配置（`config.go`）、数据库与迁移执行器（`database.go`）、HTTP 中间件与完成日志、JSON 与错误信封（`main.go` 相关部分）；随后把各 handler 移入对应能力的 `httpapi` 子包。
4. **identity**（`security.go` 与 `application.go` 中的用户、会话部分）：成本低则同批完成，否则另列，不阻塞阶段 I。

## 前置依赖

无，可与 M1a 并行。阶段 I 依赖本任务第 1 步。

## 验收标准（初稿）

- 每一步之后，`gofmt`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`、`./scripts/test-integration.sh` 全部通过。
- REST 路由、JSON 形状、状态码、SQL 语义与迁移文件不变，由现有集成测试和 catalog REST 合同测试证明。
- `cmd/roomusic` 只保留入口与装配；依赖方向为 handler → 能力包 → platform，没有循环；catalog 不导入 library 的私有实现。

## 风险

- diff 大、导出命名需要斟酌；原 `package main` 内的集成测试要随包拆分。每步单独提交，出问题按步回退。

## 范围外

新增端点、schema 或迁移、业务规则、身份规则改动（阶段 I）、`/api/` 404（F1）、目录幂等指纹（F2）、性能优化。

## 待规划事项

- 各包导出的最小接口；测试文件的归属与共享测试工具的位置。
- identity 拆分是否纳入本任务。

# 实施：补到新证据后重开搁置的待定项

分支 `fix/reopen-shelved-on-new-evidence`，工作目录 `../ROOMusic-wt/reopen-shelved`。

1. 实现（Codex `gpt-6.2-sol`）：`backend/internal/curation/work_states.go` 的 `UpsertPendingItem`；如候选排序不稳定，改 `program/match*.go` 的生成处；同步 `.trellis/spec/backend/` 的 curation 合同。
   验证：`cd backend && go build ./... && go vet ./internal/curation/...`。
2. 测试（另一个 Codex `gpt-6.2-sol`）：在 `backend/internal/curation` 现有的 PostgreSQL 集成测试旁新增 `TestPostgreSQLCurationShelvedReopensOnNewCandidates`，覆盖 PRD 的三个用例
   （新候选重开、相同候选不重开、人工不采纳不再提出），并做一次变异检查（把 `WHERE` 恢复原样，新用例应失败）。
   验证：`ROOMUSIC_TEST_RUN='TestPostgreSQLCurationShelved|TestPostgreSQLCurationExceptionAcceptDecline|TestPostgreSQLCurationProgram' scripts/test-integration.sh`，以及 `go test ./internal/curation/...`。
3. 主代理审阅、在分支上提交，合入 `main`（不推送）。

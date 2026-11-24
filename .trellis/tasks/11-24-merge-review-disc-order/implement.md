# 实施：发行并入的审查证据补上合并后的碟序

分支 `feat/merge-review-disc-order`，工作目录 `../ROOMusic-wt/merge-review`。

1. 实现（Codex `gpt-6.2-astra`，跨 governance、curation、app、steward runtime）：
   `catalog/governance/release_merge.go`（如需抽出计划函数）、`curation/release_merge_evidence.go`、`app/steward.go` 的证据读取、
   `steward/runtime/review.go` 的 `ReviewEvidenceFields`、规范 `steward-review-contracts.md`、`curation-contracts.md` 第 20 节。
   验证：`go build ./... && go vet ./internal/curation/... ./internal/catalog/governance/... ./internal/steward/... ./internal/app/...`；
   `go test ./internal/steward/runtime/... ./internal/curation/... ./internal/catalog/governance/...`（含 `review_policy_spec_test.go` 的逐字比对）。
2. 测试（另一个 Codex `gpt-6.2-sol`）：扩展 `TestPostgreSQLStewardReviewPolicyV4MergeEvidence`（或在同文件新增用例），断言审查收到的 `layout` 与执行后的发行碟序、曲序一致；
   新增超过 300 首时 `tracks_omitted` 的单元或集成用例；`TestPostgreSQLCurationModelMergeReleases` 中受控审查通过时自动执行、按批撤销回到原样（已有则只核对）。变异检查一次（例如把 `position` 改用原碟号）。
   验证：`ROOMUSIC_TEST_RUN='TestPostgreSQLStewardReviewPolicyV4MergeEvidence|TestPostgreSQLCurationModelMergeReleases|TestPostgreSQLCurationExceptionMergeAcceptDecline|TestPostgreSQLCurationMergeRevert|TestPostgreSQLCurationMergeBlocked' scripts/test-integration.sh`。
3. 主代理审阅、提交、合入 `main`（不推送）。真实模型复测：V12 实例已清理，本轮不做，在交付说明里写明。

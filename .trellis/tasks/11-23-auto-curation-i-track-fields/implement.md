# I 实施

分支 `feat/auto-curation-i`。三次派发：1、2 用 `gpt-6.2-astra` 实现，各自另派测试代理；3 是前端，主代理自己做。每次派发验收越界后在分支上提交。

1. 数据与人工层：`migrations/0065*`、`catalog/{fields,read,projection,track_summary,track_placement,search_sql,curation_evidence}.go`、`catalog/internal/{searchindex,artistgraph}`、`catalog/maintenance/artist_batch.go`、`catalog/governance/{track_corrections,track_revert}.go`、`catalog/httpapi`、`app/app.go`（注册 Kind）、API 样本与规范（catalog/governance 合同、database-guidelines 的视图约定）。验证：`go test ./internal/catalog/...`；集成 `TestPostgreSQLMigration|TestPostgreSQLAPIResponseSamples|TestPostgreSQLTrack|TestPostgreSQLArtist|TestPostgreSQLSearch|TestPostgreSQLCatalog`（按实际名收窄）。
2. 自动层与 C、D：`catalog/governance/track_auto*.go`、`curation/{writer_track,suppressions,external_lyrics}.go`、`curation/program/{enrich,enrich_media,pass,digest,match}.go`、`curation/stewardtools`、`curation/model/{exceptions,scheduler}.go`、`catalog/governance/stewardtools`、`catalog/stewardtools/{review,details}.go`、`app/{curation_model,steward}.go`、样本、`curation-contracts.md` 新增第 18 节。验证：`go test ./internal/curation/... ./internal/catalog/...`；集成 `TestPostgreSQLCuration|TestPostgreSQLB3|TestPostgreSQLStewardHTTPC7|TestPostgreSQLTrack`。
3. 前端：design 第 8 节；typecheck、eslint、相关 Vitest、Playwright 冒烟。
最后：后端单元全量、集成全量串行一次（影响面大：读取路径全换视图）。

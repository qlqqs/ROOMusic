# 实施：同一目录里的零散单曲不归成一张专辑

分支 `feat/loose-singles-in-directory`，工作目录 `../ROOMusic-wt/loose-singles`。

1. 实现（Codex `gpt-6.2-astra`，核心归组规则与 ID 稳定性）：
   `catalog/ingest/{organizer,scope,filename_numbers,version}.go` 及必要的辅助文件；如需要，`library/scan_pipeline.go`（推断专辑的清除）；
   规范 `library-scan-grouping.md`、`library-scan-incremental.md`（如版本号有登记）。
   验证：`cd backend && go build ./... && go vet ./internal/catalog/ingest/... ./internal/library/... && go test ./internal/catalog/ingest/... ./internal/library/...`（单元）。
2. 测试（另一个 Codex `gpt-6.2-sol`）：
   - `organizer_test.go` 新增判定表：不同艺人的零散单曲拆开；同一艺人、无曲序的单曲拆开；只有一个文件拆开；全无标签的目录、带曲序（标签或文件名）的目录、目录名识别为专辑的目录、带一张真实专辑标签的目录保持一个发行；按格式拆分后再拆单曲。既有的 `TestOrganizeSameDirectoryFilenameDecisionTable` 不变。
   - PostgreSQL 集成：扫描一个零散单曲目录得到各自的 `loose_unknown` 发行，每首保留自己的内嵌封面；重扫 ID 不变；旧版本归组（一个目录名发行）升级后拆开，键最小的单曲保留旧 ID；自动整理对拆开的单曲走单曲模式，ISRC 能匹配的匹配上（受控 MusicBrainz）。
   - 变异检查一次（例如去掉第 3 条）。
   验证：`go test ./internal/catalog/ingest/...`；`ROOMUSIC_TEST_RUN='TestPostgreSQLLooseSingles|TestPostgreSQLSplitRelease|TestPostgreSQLScanner|TestPostgreSQLStaging|TestPostgreSQLScanVersion|TestPostgreSQLAPIResponseSamples|TestPostgreSQLCurationStreamingSingle' scripts/test-integration.sh`（按实际用例名收窄）。
3. 主代理审阅、提交、合入 `main`（不推送）。

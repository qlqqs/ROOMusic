# Backend Quality Guidelines

## Status

当前后端使用 Go module 和测试套件；核心门禁为 `gofmt`、`go test ./...`、
`go vet ./...`、`go build ./...`，数据库集成测试通过
`ROOMUSIC_TEST_DATABASE_URL` 控制是否连接 PostgreSQL。未配置该变量时测试会
明确跳过，不能把跳过结果当作事务证据。

集成门禁使用 `./scripts/test-integration.sh`（或 `mise run test-integration`）。
该入口只启动 `compose.test.yaml` 中的 PostgreSQL 18，默认使用
`127.0.0.1:55432`，测试结束删除专用容器和数据卷；CI 必须显式执行入口，不能
把未配置数据库导致的跳过视为通过。测试可通过 `ROOMUSIC_TEST_KEEP_DB=true`
保留容器用于本地调试，但不得复用生产 `.env` 或业务数据卷。

**API 响应样本**：改动任何 REST 响应的字段、状态码或错误码时，集成测试 `TestPostgreSQLAPIResponseSamples`
（`internal/app/api_samples_integration_test.go`）会因样本漂移失败。确认是有意的合同变更后，用
`ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 重写 `internal/app/testdata/api-samples/`，同一提交里同步前端 decoder
（前端契约测试逐个解码这些样本）。新增端点或错误码时，把它加进该测试的固定流程与覆盖清单。样本只用合成素材，测试断言其中
没有绝对路径、密码、Cookie 与令牌。
前端解码器缺键即报错，遇到不认识的枚举值也报错，所以样本只录正常状态时，契约测试拦不住两类不一致：
- 可空字段在部分状态下缺键：Go 结构里 `omitempty` 会把空值连键一起省掉，可空字段要明确写 `null`；
- 只在异常或暂停状态才出现的取值，例如暂停原因、不可用原因、单曲才有的 `recording`。
新增这类字段或取值时，要把会出现它们的状态也录成样本。识别结论、整理进度的 `*-matched`、`*-recording-conflict`、`progress-album-retry`、`progress-model-*` 就是这样补的。

### 整理固定样本回归

在仓库根目录运行一次即可取得纯程序和程序加脚本模型两份结果。入口使用一次性 PostgreSQL、真实扫描、固定合成音频和回环模拟上游。命令输出按样本列出预期、实际对应、自动字段、扫描差异、各接口请求次数和识别用时。

```sh
ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

样本和脚本在 `backend/internal/app/testdata/curation_regression/`。每种模式以全部样本为分母计算每张请求数，含零请求样本，模型调用也计入。正确识别计到指定版或组且完成该层必需写入的张数。正确未匹配、有冲突和不查可在明细中核对。缺写、待名字核对和认对张数下降分别报告。认错按样本去重，逐条用模式、样本、对象、字段和错值比对 `fixed-baseline.json`；已有错认标红并列负责子任务，新增错认使常规命令失败。全部任务合入后的验收要求认错为零。

已知偏差（2026-11-27 决定暂不处理，都不影响认对和认错）：
- `spoken-podcast` 预期不查，实际查询后未匹配。样本写的播客标记是 `PODCAST=1`，读文件只认 `PCST`、`ISPODCAST` 和 MP3 流派首项 `Podcast`。
- `missing-position` 结论正确，但评分把缺曲号也当作推断，多记一条扫描差异（`curation_fixed_report_integration_test.go`）。

两张都不计入正确负例。两种模式的正确负例因此比反例总数少 2，不代表退步。

用 `ROOMUSIC_CURATION_METRICS_SAMPLES` 指定逗号分隔的样本 ID 做定向核查。选择混放目录时包含该目录的所有样本。首次基线采集使用 `ROOMUSIC_CURATION_METRICS_SUITE=fixed-capture`，将 `ROOMUSIC_CURATION_METRICS_OUT` 指到本轮临时 JSON；相对路径从 `backend/internal/app/` 起算。日常回归只读基线。独立的程序计时运行保留真实限速、60 秒成员静默、确认扫描和队列轮询，输出请求区间、定时器、扫描和队列观测。

```sh
ROOMUSIC_CURATION_METRICS_SUITE=idle ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/idle.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

## Design Requirements

- Keep capability policy cohesive and behind narrow, consumer-owned interfaces.
- Keep dependency direction inward; wire concrete adapters only in the
  composition root.
- Keep functions at one abstraction level with explicit I/O and side effects.
- Pass context to I/O boundaries and make clock/ID generation injectable where
  deterministic tests need control.
- Decode external input once, then pass typed values. Re-check authorization and
  physical constraints at their authoritative backend boundaries.
- Wrap and classify errors; never discard them or expose internal causes.
- Make persistent changes transactional, revision-aware, and idempotent when the
  API promises retry safety.
- Preserve read-only source libraries and PostgreSQL-only Core 0 operation.

Small functions are judged by cohesion and cognitive load, not a line limit. A
single readable pure transformation may be longer than a handler that mixes
decoding, authorization, SQL, and presentation and therefore must be split.

## Forbidden Patterns

- Cross-module access to private tables, adapters, row structs, or helpers.
- Import cycles, a global service locator, or package-level mutable business
  state.
- A broad `Repository`/`Service` interface spanning unrelated capabilities.
- HTTP handlers containing SQL or filesystem traversal.
- Business logic in migrations, middleware, database triggers, or React clients
  without a matching backend owner.
- `panic` for user input or ordinary infrastructure errors; `log.Fatal` outside
  the process entrypoint.
- Unbounded goroutines, channels, result sets, request bodies, diagnostics, or
  directory work queues.
- Starting background work without cancellation, ownership, concurrency limits,
  and observable completion.
- Silent fallback from PostgreSQL to Redis/Meilisearch or from REST to GraphQL.
- Default source-file writes, tag updates, moves, deletes, or directory symlink
  traversal.
- Copying V0 code without revalidating assumptions and tests against Core 0.
- Letting an Agent, model adapter, Review Subagent, browser, or plugin execute
  SQL, shell, or filesystem mutations outside a registered backend tool.
- Treating Operator mode as a reason to skip authorization, argument validation,
  revision/idempotency checks, journaling, or recovery classification.
- Treating the main Agent's own output as independent Steward approval.

## Test Ownership

| Behavior | Minimum evidence |
| --- | --- |
| Pure domain/parser rule | Table-driven unit tests including malformed and boundary inputs |
| PostgreSQL adapter | Integration test with PostgreSQL 18, constraints, rollback, and query result mapping |
| REST endpoint | Request/response, auth, safe error, content type, and correlation assertions |
| Session lifecycle | Setup closure, login/logout, expiry, revocation, disabled user, token-hash storage, cookie flags |
| Path boundary | traversal, relative/absolute normalization, symlink escape/broken target, and allowed-root containment |
| Scanner | initial formats, CUE, cancellation, partial failure, complete-only missing reconciliation, repeat identity, content-fingerprint rebinding (rename/move/swap, ambiguity, deferral on incomplete or canceled walks, replaced content) and Release membership matching |
| Directory mutation | transaction, expected revision, idempotent retry, key/input conflict including the same key on another target, overlapping same-key retries forced to overlap by a test-held lock, Change Set, and inverse action |
| Future Agent tool | mode/approval matrix, independent review binding, backend recheck, tool scope, idempotency, journal, and recovery/irreversible marker |
| Concurrency-sensitive code | deterministic contention test and race detector where practical |

Tests assert behavior, not implementation call counts. Mocks are appropriate for
narrow ports in application tests; they do not replace PostgreSQL or filesystem
integration tests for physical guarantees. Fixtures must be small, legal to
store, and contain no private user library data.

## Planned Gates

Once the Go module exists, every backend change runs the applicable subset of:

```bash
gofmt
go test ./...
go vet ./...
go build ./...
```

Concurrency-heavy changes also run `go test -race ./...` in a supported
environment. Database changes run migrations against a disposable PostgreSQL 18
instance and repository integration tests. Deployment changes validate
`docker compose config`.

Do not invent a linter or coverage threshold here. If static analysis, migration,
or vulnerability tools are selected, pin/configure them in the repository,
expose one repeatable command, and update this guide. A tool gate is useful only
when local and CI behavior agree.

## Review Checklist

- [ ] The change is the smallest complete behavior at the owning capability.
- [ ] No unrelated refactor or speculative adapter is bundled.
- [ ] Public interfaces and exported names are narrower than their implementations.
- [ ] Security decisions remain backend-authoritative.
- [ ] Transactions, conflict, cancellation, and partial failure are explicit.
- [ ] Errors and logs are classified, correlated, and redacted.
- [ ] Representative success and dangerous failure paths are tested.
- [ ] The production Core 0 loop still works with Redis and Meilisearch absent.
- [ ] No music-source mutation or raw path disclosure was introduced.
- [ ] Any future Agent or Operator path uses the shared authority/executor and
      cannot bypass it through a model adapter, plugin, CLI, or internal route.
- [ ] Documentation/specs changed when a contract or selected tool changed.

## Anti-Example

A patch adding `POST /scan` that starts an unbounded goroutine, accepts an
arbitrary path, logs that path, and returns 200 has few lines but is not a small
change. The complete change belongs to the library module and includes admin
authorization, allowed-root identity, bounded scheduling, durable scan state,
safe errors/logs, and tests.

See [Engineering Principles](../guides/engineering-principles.md),
[Modular Design](../guides/modular-design.md), and the
Core 0 PRD（本机归档 `.trellis/tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md`）.

固定样本 one-char 和 romanized 的脚本回复使用 B2 名字线索，分别检验错字与罗马字标题。以实际 MusicBrainz 请求和外部版、录音对应评分。

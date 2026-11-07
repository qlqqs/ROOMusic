# 执行计划：专辑多流派

接口合同与裁决见 [design.md](./design.md)（第 2–6 节是合同与规则，第 10 节是裁决：拆分符号加上顿号“、”）。迁移编号 0032，必须在重复艺人规则（0031）提交之后。
后端和前端放在同一个提交里，因为 `genre` 改名 `genres` 以后，旧的前端解码器会报缺字段。

## 分工

| 工作 | 执行者 |
| --- | --- |
| 后端：`SplitGenres`、`GenreKey`、解析器多值、`chooseGenres`、写入与读取、修正与撤销兼容（含 `sameIntent`）、迁移 0032 及回退、冒烟脚本；只跑 `go vet` 和不需要数据库的单元测试 | Sol（`codex/gpt-6.1-sol`，high，full-access，单独一个后端实施 agent） |
| 前端：解码器、`formatGenres`、发行信息、版本对比、编辑对话框里的流派标签输入、变更摘要与认领详情、模拟数据 | Opus（`claude/claude-opus-5-5`，high） |
| 集成测试（迁移 0032 专门用例、修正状态、往返）、重录接口样本、前端 Vitest 和 `e2e/governance.spec.ts` | Sol（`codex/gpt-6.1-sol`，high，full-access） |

## 步骤

1. **后端**（后端实施 Sol，父任务第 4 步，和 sidebar-my-music 并行）：
   - 先确认重扫会不会重新读取没有变化的文件。如果会复用已存的观察值，读出时也做一次 `SplitGenres`（design.md 第 10 节 R4）；
   - 按 design.md 第 1–5 节实现；
   - 写 design.md 第 7 节里不需要数据库的单元测试；
   - 不录样本，不跑 DB 集成测试。
2. **后端测试**（Sol，父任务第 6 步）：
   - 新增 `TestPostgreSQLMigration0032GenreLists`；
   - `TestPostgreSQLCorrectionStateTransitions` 加流派：设两项、留空、撤销；
   - 登记 `rollbackFixtures[32] = ""` 并注明理由；
   - 重录 `TestPostgreSQLAPIResponseSamples`，审阅 diff，只含预期变化。
3. **前端**（Opus，和第 2 步同时）：按 design.md 第 6 节实现，不加说明段落；`npm run build`。
4. **前端测试**（Sol）：跑 design.md 第 7 节列出的 Vitest 文件，再加 `e2e/governance.spec.ts`。
5. 主会话审阅后提交：`feat(catalog): 专辑支持多个流派`。

## 验证命令

```bash
cd backend && go vet ./... && go test ./internal/catalog/metadata ./internal/library/parser ./internal/catalog/ingest ./internal/catalog/governance \
  -run 'Genre|Organize|ParseCorrection|CorrectionFingerprint|RestoredIntent' -count=1
ROOMUSIC_TEST_RUN='TestPostgreSQLMigration0032GenreLists|TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQLCorrectionStateTransitions|TestPostgreSQLAPIResponseSamples' ./scripts/test-integration.sh
cd frontend && npx vitest run src/features/catalog src/features/governance src/app/release-corrections.test.tsx
cd frontend && npx playwright test e2e/governance.spec.ts
```

不跑全量。回退后旧版本能启动，由往返测试的结构快照一致来证明，不另做演练。

## 回滚点

回退这个提交；数据库执行 0032 的回退 SQL，回退后每张专辑只保留第一个流派。

# 执行计划：重复艺人规则

接口合同与裁决见 [design.md](./design.md)（第 2–4 节是合同与规则，第 8 节是裁决）。迁移编号 0031。
后端和前端放在同一个提交里，因为后端重录样本以后，前端的契约测试要一起更新。

## 分工

| 工作 | 执行者 |
| --- | --- |
| 后端：删除 `same_track_role`，`bracket.go`，`refreshAliasSignals`，读取时的 UNION 和反向排除，迁移 0031 及回退，投影版本升到 2；只跑 `go vet` 和 `TestBracketAliases` | Sol（`codex/gpt-6.1-sol`，high，full-access，单独一个后端实施 agent） |
| 前端：解码器加两个可选字段，`reasonText` 按 kind 分支，未知 kind 不显示 | Opus（`claude/claude-opus-5-5`，high） |
| 改写和补充集成测试、`rollbackFixtures[31]`、重录接口样本、前端 Vitest | Sol（`codex/gpt-6.1-sol`，high，full-access） |

## 步骤

1. **后端**（后端实施 Sol，在父任务第 1 步和 ui-minimal 并行）：
   - 写迁移前，先在测试库用 `\d artist_candidate_signals` 确认 CHECK 约束名；
   - 按 design.md 第 3、4 节实现，写 `bracket_test.go`；
   - 不录样本，不跑 DB 集成测试，这两项由 Sol 做。
2. **后端测试**（Sol，父任务第 3 步）：
   - 按 design.md 第 5 节 2–5 项改写和新增集成测试：用户的例子（同曲同身份共同署名，没有提示）、跨专辑括号别名、`rollbackFixtures[31]`、重录 `TestPostgreSQLArtistAPIResponseSamples` 的样本；
   - 只用隔离的 `roomusic-test` 项目。
3. **前端**（Opus，和第 2 步同时）：按 design.md 第 2 节的前端部分实现，文案就是那两句；`npm run build`。
4. **前端测试**（Sol）：理由文案（inner、outer、专辑规则、未知 kind），更新 `src/app/artist-governance-step5.test.tsx` 里的正则，`api-contract.test.ts` 用新样本。
5. 主会话审阅后提交：`feat(artists): 重复艺人改用括号别名规则，同曲同身份署名不再提示`。

## 验证命令

```bash
cd backend && go vet ./internal/catalog/... && go test ./internal/catalog/internal/artistgraph -run TestBracketAliases -count=1
ROOMUSIC_TEST_RUN='TestPostgreSQLArtistGraphReviewRawEvidence|TestPostgreSQLArtistCandidate|TestPostgreSQLArtistIgnoredCandidates|TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQLArtistAPIResponseSamples' ./scripts/test-integration.sh
cd frontend && npx vitest run src/features/governance src/app/artist-governance-step5.test.tsx src/test/api-contract.test.ts
```

不跑全量。不另写迁移清理测试（收窄 CHECK 本身就会检查），也不写合并、拆开、改名的刷新测试（这些路径没有改）。

## 回滚点

回退这个提交；数据库执行 0031 的回退 SQL。旧版本看到投影版本不一致，会自己重新整理。

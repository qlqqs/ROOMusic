# 执行计划：读取外置 LRC 歌词

分工：后端实现由 Codex（gpt-6-astra，high）完成，不写新测试；后端测试由另一个 Codex（gpt-6.1-sol，high）编写；前端与 spec 由主代理完成。
工作目录：独立 worktree，分支 `feat/external-lrc`，基于 `main`。

## 顺序

1. 后端实现
   - `parser/lyrics.go`：`LyricsSourceLRCFile`、`MaxExternalLyricsBytes`、`DecodeExternalLyrics`、`ApplyExternalLyrics`。
   - `library/sidecar_lyrics.go`：目录索引、匹配、`Lstat` 与有界读取。
   - `library/scan_pipeline.go`、`library/scanner.go`：物理分支填入外置歌词，`apply` 中在缓存写入之后覆盖副本。
   - `migrations/0050_track_lyrics_file.sql` 与 `.down.sql`。
   - `catalog/lyrics.go`：`LoadTrackLyrics` 读 `lyrics_source`，新常量 `external`。
2. 后端测试（按 `research/scan-sidecar.md` “测试应覆盖的场景”）
   - parser 解码与署名单测；library 匹配单测；PostgreSQL 集成：优先级、回退、增量三态（`FilesParsed == 0`）、缓存仍为内嵌、CUE 两种情况、约束各格；catalog 接口来源；接口样本 `tracks/lyrics-external`。
3. 前端（主代理）：`decoders.ts` 与测试、`lyrics-panel.tsx` 文案与测试、`api-contract.test.ts` 样本映射。
4. spec（主代理）：`lyrics-contracts.md`、`library-scan-incremental.md`、`library-scan-tags.md`（署名来源）、`frontend/player-design-guidelines.md:51`。

## 验证命令（只覆盖本任务）

```bash
cd backend
go test ./internal/library/parser/ ./internal/catalog/ ./internal/library/
# 隔离测试库（scripts/test-integration.sh 的方式），按名收窄：
ROOMUSIC_TEST_RUN='TestPostgreSQL(TrackLyrics|ExternalLyrics|CueVirtualTracksHaveNoLyrics|Incremental|MigrationRollbackRoundTrip|APIResponseSamples)' ../scripts/test-integration.sh
cd ../frontend && npx vitest run src/features/player src/test/api-contract.test.ts && npm run lint && npm run typecheck
cd .. && ./scripts/playback-e2e.sh   # 歌词面板真实扫描链路
```

具体的 `-run` 名以测试代理实际新增的用例名为准，汇报时列出。

## 回滚点

- 迁移 0050 合入前核对编号；若 `main` 已有 0050，顺延编号并同步 spec。

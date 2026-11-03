# C2 执行计划

分工按 `~/.claude/CLAUDE.md`：后端实现与测试用 `codex/gpt-6-astra`（high），前端实现用 `claude/claude-opus-5-5`（high），编写测试和执行前端测试用 `codex/gpt-6-astra`（high）。
子代理不提交；主会话验收通过后逐步提交到 `main`。后端各步串行（共用隔离测试库，且 S1 第 3 步要改 `app`，须等 C1 合入）；前端线与后端线并行（文件不重叠）。

## 后端线

| 步 | 内容 | 验证 |
| --- | --- | --- |
| B1 | 拆出 `catalog/ingest`：`git mv` 10 个文件与 4 个测试，改包名，`StagingDB` 不再嵌入 `Queryer`；改 library 的导入；依赖规则 | `gofmt`、`go vet`、`go test -race ./internal/catalog/... ./internal/library/... ./internal/app/...`（隔离库，集成用例 PASS），接口样本 |
| B2 | 核心内准备：`fields.go` 下沉字段词表；建 `catalog/internal/releasesql` 并迁入 SQL 片段与纯格式化函数；改调用点 | 同上（catalog 与 app） |
| B3 | 移出 `catalog/governance`：10 个文件与 12 个测试，`governance.Store`，复制夹具；改 `catalog/httpapi`、`app`；依赖规则去掉 catalog 对 `operations`、`metadata` 的依赖 | 同上，另加 `./internal/catalog/governance/...`；依赖规则测试；接口样本逐字 |
| B4 | `parser.go` 拆文件 | `go test -race ./internal/library/parser/...` |
| B5 | 文档：`directory-structure.md` 与各合同里的旧路径、`catalog.X` 符号；路线图 C2 行 | 改动 Markdown 的相对链接检查 |

## 前端线

| 步 | 执行者 | 内容 | 验证 |
| --- | --- | --- | --- |
| F1 | Astra | 只补 design.md 列出的 9 组特征测试，不改实现 | 新测试在旧实现上通过；`npx vitest run src/features/player` |
| F2 | Opus | 拆出 `playback-failure.ts`、`media-session-binding.ts`、`prefetcher.ts`、`queue-restore.ts`（后者补单测） | 自检：`npm run typecheck`、`npx vitest run src/features/player` |
| F3 | Opus | `Lifecycle` 与 `RestorePhase` 替换对应布尔值；临时的 DEV 不变量断言；`Pipeline` 替换管线字段，最后把 `status` 收进 `Pipeline`、删掉临时断言 | 同上 |
| F4 | Astra | 验收：`npm run lint`、`npm run typecheck`、`npm run test`（全量 Vitest）、`npm run build`（刷新内嵌产物）、`npm run test:e2e`（路由模拟） | 全部通过，报告数目 |

F2、F3 分别提交；内嵌产物 `backend/cmd/roomusic/web` 在 F4 构建后随 F3 的提交一起提交（F2 单独提交时也要构建，保证每个提交的内嵌产物与源码一致）。

## 收尾

- 收尾时在导出的提交树上跑一次后端相关包与前端全量检查（R5：只做一次）。
- PRD 记录实施结果与验收对照；归档；链接修正与日志合成一个提交。

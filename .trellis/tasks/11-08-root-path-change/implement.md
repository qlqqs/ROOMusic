# ⑫ 目录改路径保留 ID · 实施计划

依据：[prd.md](./prd.md)、[design.md](./design.md)。S 线第 2 项，在 ① 合入 S 线分支之后开始；子 agent 不提交 git。

## 通用约束（派活时原样写进 prompt）

- 测试只做必要的：只补改动直接影响的测试，跑下面的最小相关集；不跑全量，不做演练和重复验证；不为不可能的边界写测试；不堆防御代码。
- 前端文案：不写防御性说明（不写“ID 不变”“收藏不受影响”之类）；按钮和对话框本身说清楚的不再加说明；错误提示用一句日常话。
- 数据库测试只用隔离的 `roomusic-test` 项目；集成测试必须 PASS 而不是 SKIP。

## 后端（GPT-6-Astra，high）

1. 迁移 `backend/migrations/0035_root_relocate.sql` 与 `.down.sql`：类型约束增加 `relocate`（design 3.4）。
2. `library/scans.go`：新增 `TryLockScanCoordination(ctx, tx) (bool, error)`，复用 `scanAdvisoryLockClass` 与 `hashtext(current_schema())`。
3. `library/httpapi/root_operations.go`：`relocateRoot` handler（design 3.1 矩阵，顺序：同源 → 管理员 → ID → 键 → 解码 → `ValidatePath` → `beginRootOperation` → 扫描锁探测 → `FOR UPDATE` → revision → 新路径冲突 → 更新与记录）；`handlers.go` 注册 `POST /api/v1/library-roots/{id}/relocate`。
4. `restoreRoot` 的前态查询按 design 3.3 放宽。
5. 合同同步：`library-scan-contracts.md`、`scan-coordination-contracts.md`、`error-handling.md`（design 3.5）。

1→3 有先后；2、4 可与 3 并行。

## 前端（Claude Opus 5.5，新开一个子 agent，做完即结束）

后端第 1 步后即可按 design 第 3、4 节并行。

1. `endpoints.ts`：`relocateRoot(id, path, expectedRevision, key)`；响应复用 `decodeRootChanged`。
2. `use-root-commands.ts`：改路径命令；成功后调用开始扫描并按 202/200 处理（复用 `useStartScan` 与扫描区段的跳转逻辑）。
3. `root-table.tsx`：“改路径”图标按钮；对话框内嵌 `FolderBrowser`；`root-messages.ts` 增加三条错误文案。

## 测试（GPT-6.1-Sol，与实现分开的 agent）

- 集成（新增，PostgreSQL + `httptest`）：
  - 改路径成功：revision 加一、操作记录类型 `relocate`、响应不含绝对路径。
  - 幂等回放与同键异义 409；revision 冲突 409；新路径已登记 409；越界或含符号链接 400；未知目录 404。
  - 扫描在跑时 409 `scan_running`（用独立连接持有扫描锁模拟）。
  - 停用 → 改路径 → 恢复 成功（版本链修正）。
  - 把目录内容复制到新位置（保留 mtime）→ 改路径 → 扫描：曲目 UUID、Release UUID 不变，missing 为 0。
- 前端（Vitest）：改路径命令成功后发起扫描；三种错误的文案映射。
- API 样本：新增 `library-roots/relocate` 成功与 `errors/scan-running` 两个样本。

### 验证命令（最小相关集）

```bash
cd backend
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/library/... ./internal/app/ -run 'Relocate|RootOperation|Restore' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/platform/postgres/ -run 'TestPostgreSQLMigrationRollbackRoundTrip' -count=1
go vet ./internal/library/...
cd ../frontend
npx vitest run src/features/library
npm run typecheck
```

## 风险文件与回滚点

- 风险文件：`library/httpapi/root_operations.go`（恢复逻辑改动）。
- 回滚点：迁移单独；后端 2–4 一组；前端独立。

## 合入前核对

- [ ] 扫描锁探测使用的键与 `acquireScanExecution` 完全相同（常量复用，不复制数字）。
- [ ] 响应与操作记录列表都不含绝对路径。
- [ ] “停用 → 改路径 → 恢复”通过。
- [ ] 改路径流程截图（1440/375）由主会话复核；文案无多余说明。

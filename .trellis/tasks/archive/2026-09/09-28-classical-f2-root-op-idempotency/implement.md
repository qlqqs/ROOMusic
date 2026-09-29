# F2 执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。每一步完成后打勾。

## 步骤

1. [x] **先写复现测试，在未修改的代码上运行，确认会失败。** 在 `auth_root_operations_integration_test.go` 中新增两个测试：
   - `TestPostgreSQLRootOperationIdempotencyBindsTarget`，覆盖 R1、R4、R5：
     - 停用 A（键 `shared-disable`）后，用同键同 body 停用 B → 409，B 仍为 active/1，没有停用记录；
     - 恢复 A（键 `shared-restore`）后，B 先用其他键停用，再用同键同 body 恢复 B → 409，B 仍为 disabled/2；
     - 两个非法 ID 请求都返回 400 `invalid_id`；
     - 另建一个管理员执行停用，随后把该管理员降为 user，用原键重放 → 403 `forbidden`。
   - `TestPostgreSQLRootOperationConcurrentRetriesConverge`，覆盖 R3，新增、停用各一个子测试。需要强制重叠：
     - 测试事务先 `LOCK TABLE library_roots IN ACCESS EXCLUSIVE MODE`，新增与停用都会卡在这张表上；
     - 然后启动 8 个带同键的并发请求，轮询 `pg_stat_activity` 中 `wait_event_type='Lock'` 的会话，数量达到 8 后再释放测试事务；
     - 响应通过 channel 收回，在测试 goroutine 里断言，不在子 goroutine 中调用 `t.Fatal`。本包测试没有使用 `t.Parallel`，按等待会话计数不会受其他测试干扰。
   - 预期失败：
     - 跨目标停用/恢复返回 200（重放了 A 的结果）；
     - 非法 ID 返回 503；
     - 并发停用出现 409 `revision_conflict`；
     - 并发新增出现 409 `idempotency_conflict`。
   - R5 在修改前就能通过，它是结构调整后的回归保护。
2. [x] 在 `root_operations.go` 中实现以下改动：
   - 新增 `rootOperationLockClass` 和 `beginRootOperation`；
   - 删除 `replayOperation`；
   - 停用/恢复加入 ID 校验、小写化，并把 `root_id` 放进指纹；
   - `application.go` 的 `addRoot` 改用 `beginRootOperation`。
3. [x] 重跑第 1 步的测试和现有目录操作测试，全部通过；用 `-race` 再跑一次并发测试（重复 10 次）；做变异检查：去掉 advisory 锁后并发测试失败。
4. [x] 按 R7 同步文档：
   - `core0-runtime-contracts.md`：目录幂等合同、错误矩阵、技术债标为已修复，写明 D1 的升级影响；
   - `database-guidelines.md`：补充“指纹含路由目标、同键请求串行”；
   - ADR 0003：第一节缺口段落与测试行、第七节渐进迁移第 1 项、第八节两行验收、第九节第 4 项、决策记录；
   - `core-1-roadmap.md`：第 39 行、F2 行、风险段落；
   - 父任务 PRD 表、`core-1-architecture.html` 的 F1·F2 节点标签。
5. [ ] 运行完整验证（见下文），在 PRD 末尾记录实施结果，提交到 main，归档任务并修正归档后的相对链接。

## 验证命令

```bash
# 启动并保留测试数据库，同时运行全部 TestPostgreSQL* 集成测试
ROOMUSIC_TEST_KEEP_DB=true ./scripts/test-integration.sh

# 定向运行目录操作测试，并发测试开启 race
cd backend && ROOMUSIC_TEST_DATABASE_URL='postgres://roomusic_test:roomusic_test@127.0.0.1:55432/roomusic_test?sslmode=disable' \
  go test -race ./cmd/roomusic -run 'TestPostgreSQLRootOperation' -count=1

# 包内单元测试与静态检查（本任务只改 cmd/roomusic）
cd backend && go test ./cmd/roomusic -count=1 && go vet ./... && gofmt -l .

# 清理测试容器
docker compose -f compose.test.yaml -p roomusic-test down -v
```

不运行 `go test ./...` 全量：其余包（`roomusic-smoke`、`roomusic-smoke-cli`、`migrations`）不受影响。

## 风险与回退点

- **高风险文件**：`root_operations.go` 和 `application.go`。B0 会移动这两个文件，所以 F2 必须在 B0 之前完成并提交。
- **测试稳定性**：并发测试依赖锁等待会话计数。计数设 10 秒超时，超时即失败并打印当前 `pg_stat_activity` 快照，不静默跳过。
- **回退**：改动只涉及代码，撤销提交即可，没有 schema 或数据需要处理。

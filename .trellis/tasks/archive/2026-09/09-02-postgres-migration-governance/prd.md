# PostgreSQL 迁移执行器治理

## Goal

让 ROOMusic 在单实例或多实例启动时以可追踪、可串行、可回滚的方式升级
PostgreSQL schema。迁移失败必须让应用启动失败并保持数据库处于升级前状态；
已经应用的迁移不应在每次启动时重复执行，迁移文件被篡改时必须 fail closed。

## Background And Confirmed Facts

- 迁移所有者是 `backend/migrations/`，通过 Go `embed.FS` 提供给
  `backend/cmd/roomusic/database.go`；当前没有 ORM 或外部迁移工具。
- 当前执行器按文件名排序后每次启动直接执行全部 SQL（见
  `backend/cmd/roomusic/database.go:49-65`），没有按版本跳过、并发锁或校验和。
- 现有 `schema_migrations` 只有 `version` 和 `applied_at`；历史 0001、0006、0007
  写入版本，0002--0005 没有写入记录，但在正常升级到 0007 时已经执行。
- 现有迁移使用 PostgreSQL 可事务化 DDL，集成测试可通过
  `ROOMUSIC_TEST_DATABASE_URL`/`scripts/test-integration.sh` 使用 PostgreSQL 18。
- Core 0 要求 PostgreSQL 是唯一业务权威，迁移必须是显式、有序、可审查的 SQL；
  已共享的 0001--0007 不得重写。

## Requirements

### R1. 版本发现与记录

1. 从嵌入文件读取迁移，解析唯一的正整数版本和文件名，按版本排序；重复、
   缺失或格式非法的迁移在执行前拒绝。
2. 每个成功应用的版本在 `schema_migrations` 中保留文件名、SHA-256 校验和和
   应用时间。重复启动只校验记录，不重复执行已成功版本。
3. 已存在的旧库必须兼容升级：根据旧表中的最高连续历史版本将 0001--0007
   建立一次性基线，并补齐 0002--0005 的记录；不能因它们历史上没有行而重复
   运行可能包含非幂等语句的旧迁移。
4. 数据库中存在当前程序未提供的未来版本，或已记录迁移的名称/校验和与文件
   不一致时，执行器必须拒绝启动并给出带版本上下文的内部错误。

### R2. 并发与事务

1. 所有迁移在一个 PostgreSQL 事务中执行，并在事务内取得固定命名空间的
   `pg_advisory_xact_lock`；多个进程同时启动时只能有一个迁移事务运行。
2. 任意 SQL、记录写入、锁等待取消或提交失败都回滚该事务及其 DDL；不得留下
   半套 schema 或半套迁移记录。
3. 成功提交后再执行现有的中断扫描恢复逻辑；迁移错误不得被吞掉或降级为 ready。

### R3. 可验证性与运维合同

1. 新增一个显式迁移扩展 `schema_migrations` 元数据列；不修改已共享的
   0001--0007，也不引入 Redis、Meilisearch 或第三方迁移服务。
2. 校验和算法固定为对迁移文件原始字节计算的 lowercase SHA-256，并在规范和
   测试中说明。
3. 错误保留底层原因供启动日志诊断，但不向 HTTP 客户端暴露 SQL、连接串、
   密钥或完整本地路径。

## Acceptance Criteria

- [x] 全新 PostgreSQL schema 启动后，所有发布迁移恰好各有一条记录，记录包含
      正确文件名和 SHA-256；应用可正常启动。
- [x] 对同一 schema 再次启动不会重新执行已应用迁移，`applied_at` 和业务数据
      保持不变；新迁移只执行一次。
- [x] 从仅有旧版 `schema_migrations` 行（包括只有 1、6、7 的真实历史形态）
      升级时，业务表和数据保留，0001--0007 的元数据被补齐，0008 只执行一次。
- [x] 人为修改已记录校验和或文件名时，启动返回错误并不执行后续迁移；未来
      未知版本也被拒绝。
- [x] 两个独立数据库连接并发执行迁移时，一个等待 advisory lock，最终都成功
      或按同一错误退出，数据库没有重复对象、死锁或重复记录。
- [x] 注入一个失败的后续迁移后，之前迁移创建的表、列和记录全部回滚；取消
      lock 等待也不留下事务或连接泄漏。
- [x] 现有 Core 0 行为回归通过：`go test ./...`、`go vet ./...`、`go build ./...`，
      并在可用环境执行 PostgreSQL 18 集成门禁。

## Out Of Scope

- 不实现持久化扫描取消、任务队列、HTTP 完整结构化日志或用户 Operation Journal。
- 不编辑 0001--0007 的内容，不添加 down migration、在线 schema 编辑器或新的
  ORM/迁移 CLI。
- 不改变 REST wire contract、业务表语义、扫描策略或前端代码。
- 不承诺从人为破坏的 schema 或不连续的手工历史记录中自动修复；此类情况
  fail closed，并要求备份恢复或显式 forward-fix。

## Risks And Deferred Decisions

- 旧历史缺少 0002--0005 的逐版本记录，只能在最高已记录版本可信的前提下建立
  基线；基线动作必须在日志和文档中明确是一次性兼容处理。
- 单事务会让大体量未来迁移持有锁较久；当前迁移规模小，后续若出现长迁移需
  单独设计在线/分阶段升级，不在本任务偷偷改变边界。
- 不提供自动 down migration；生产回退依赖数据库备份或新增 forward-fix 迁移。

## Planning Decision

- 路由：human selection（人类选择）。
- 选择者：用户。
- 候选结果：批准“单事务 + `pg_advisory_xact_lock` + SHA-256 元数据 + 0008
  旧库基线兼容 + PostgreSQL 18 集成测试”的方案后进入实现。
- 理由：该方案直接关闭每次启动重放、并发迁移和漂移不可见三项生产风险，保留
  Core 0 的 PostgreSQL-only 与显式 SQL 边界，且能复用现有真实集成测试基础设施。
- 当前状态：用户已明确批准实现；任务已进入 `in_progress`，代码、测试和规范同步
  已完成，等待最终提交与归档。

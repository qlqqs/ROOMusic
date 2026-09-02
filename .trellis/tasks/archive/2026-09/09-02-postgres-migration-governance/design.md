# PostgreSQL 迁移执行器技术设计

## 1. 边界与所有权

迁移治理归属 Platform/Database 启动边界，入口仍是
`backend/cmd/roomusic/database.go`；SQL 的唯一来源仍是
`backend/migrations/`。执行器只负责发现、校验、串行化和提交 schema 变更，
不承载业务规则，也不让 HTTP、扫描器或前端直接访问迁移表。

## 2. 数据模型与兼容策略

新增 `backend/migrations/0008_migration_metadata.sql`，以显式 SQL 为
`schema_migrations` 增加 `name TEXT` 与 `checksum TEXT` 列。列在升级语句中
保持可回滚（先允许 NULL），由执行器在同一事务中为所有已确认版本补齐值；以后
写入的成功记录必须同时有版本、完整文件名和 lowercase SHA-256。旧列
`version`/`applied_at` 与现有查询保持兼容，不新增业务表。

启动时先观察表和元数据列是否存在：

1. 全新库先执行 0001 建表，再按顺序执行其余迁移；0008 成功后回填 1--8 的
   元数据。
2. 旧库若只有旧版表，读取已有版本的最高值。对不高于该值且没有逐条行的
   0002--0005 视为历史已执行，只补元数据，不重放 SQL；高于最高值的版本照常
   执行。该假设只覆盖正常旧执行器产生的历史形态。
3. 若元数据列已存在但缺少 0008 行，列的原子存在性证明 0008 的 DDL 已提交；
   将 0008 作为一次性基线记录，不再次执行。已有非空名称/校验和必须与当前
   文件完全相同；旧 NULL 值只在首次治理时填入当前文件值。

数据库中高于当前发布集合的版本直接报错。版本重复由主键和文件发现校验共同
拒绝。执行器不尝试猜测手工损坏的 schema。

## 3. 执行流程

```text
embed.FS
  -> loadMigrations (版本/名称/原始字节/SHA-256)
  -> BeginTx
  -> pg_advisory_xact_lock(固定 key)
  -> 读取旧 tracking 状态与已应用行
  -> 逐版本：校验/基线跳过，或 ExecContext SQL
  -> 记录/补齐 schema_migrations 元数据
  -> Commit
  -> recoverInterruptedScans
```

`applyMigrations` 保留现有调用签名，内部委托可注入 `fs.FS` 的辅助函数，方便
单元测试构造失败、重复和漂移场景。`database/sql.Tx` 是唯一执行迁移 SQL 和
记录元数据的句柄；不能在事务中混用池连接。锁使用固定、文档化的 `int64` 命名
空间 key，并通过 `pg_advisory_xact_lock` 绑定事务生命周期；上下文取消由
`ExecContext` 传递，随后 rollback。

迁移描述符使用文件名（例如 `0008_migration_metadata.sql`）作为不可变 name，
校验和计算覆盖 embed 文件的全部原始字节。记录写入采用冲突安全的 upsert，
不更新既有 `applied_at`；在更新 NULL 元数据前先比较任何已有非空值。由于迁移
SQL 运行在同一事务中，记录前会重新读取完整 tracking 表，再次拒绝迁移期间写入
的未知版本、重复行或漂移。

tracking 表探测使用 `to_regclass('schema_migrations')` 和当前连接的
`search_path`，不得通过字符串拼接 `current_schema()` 生成未转义的限定名；这样
带大写或点号的合法 schema 名与实际迁移 SQL 保持一致。

## 4. 失败、启动与回滚

- 文件发现、版本解析、未知版本、名称/校验和漂移、锁、SQL、查询、记录或提交
  失败均返回带版本上下文的 wrapped error。
- `openDatabase` 在迁移错误时关闭连接并返回错误，因此应用不会构造 ready 状态；
  只有迁移提交后才进行扫描运行恢复。
- PostgreSQL 事务化 DDL 保证失败时先前的表/列/记录一起回滚。迁移文件不得
  包含 `COMMIT`、`ROLLBACK` 或依赖事务外副作用；这作为迁移编写合同记录在规范。
- 不实现 down migration。若生产需要回退，使用备份恢复或新增 forward-fix，
  不修改已共享文件。

## 5. 测试设计

新增后端迁移测试（可拆成无数据库的发现/校验单元测试和 PostgreSQL 集成测试）：

- fresh schema：验证 1..8 记录、名称/哈希和业务表。
- rerun/upgrade：验证时间戳稳定、旧 1/6/7 形态补齐、0008 不重复执行。
- drift/unknown：篡改记录或插入未来版本，验证 fail closed 且无后续变更。
- rollback：注入失败 SQL，验证此前 DDL/记录不可见。
- contention：两个连接同时执行，验证 advisory lock 串行、最终唯一记录，必要时
  用 `pg_locks`/受控 sleep 证明等待行为。
- cancellation：取消锁等待上下文，验证返回及时、第二次执行仍可成功。

## 6. 兼容性与运维

生产仍只需 PostgreSQL；Compose、环境变量、REST 和前端不变。README、数据库
规范和 Core 0 运行合同会把迁移命令、锁、哈希算法、旧库基线和回退方式更新为
当前实现，删除“尚未治理”的描述，但保留不提供 down migration 的限制。

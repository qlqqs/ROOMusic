# F2 技术设计

需求与裁决见 [prd.md](prd.md)。

## 边界

- 负责能力：目录（library roots）的新增、停用、恢复。
- 改动文件：`root_operations.go`，以及 `application.go` 的 `addRoot`。
- 不改的内容：
  - 表结构、迁移、唯一约束、接口的 JSON 形状；
  - 以下既有错误码：`invalid_input`、`revision_conflict`、`not_found`、`operation_failed`、`database_unavailable`。
- 新增行为只有三项：
  - 停用/恢复在 ID 格式错误时返回 400 `invalid_id`；
  - 指纹包含目标；
  - 同键请求串行。

## 改动后的请求流程

```text
同源与管理员校验（不变，先于任何重放，满足 R5）
→ 停用/恢复：root ID 用 isValidIdentifier 校验，转小写；格式错误返回 400 invalid_id
→ 解码与输入校验（不变）
→ 计算指纹
→ beginRootOperation：
     幂等键为空 → 400 invalid_input（与现在一致）
     BeginTx（显式 READ COMMITTED）
     SELECT pg_advisory_xact_lock(rootOperationLockClass, fnv32a(actor:type:key))
     在事务内按 actor/type/key 查记录：
       指纹一致 → 回滚，重放已存响应（新增 201，其余 200）
       指纹不一致 → 回滚，返回 409 idempotency_conflict
       没有记录 → 把 tx 交给调用方
→ 业务执行，与现在相同：
     FOR UPDATE / upsert → revision 与状态校验 → 更新 → 写操作记录 → 提交
```

`beginRootOperation` 替换原来的“`replayOperation` 加 `BeginTx`”两步，`replayOperation` 删除。
调用方继续 `defer tx.Rollback()`，后续代码不变。
这是本任务唯一的结构调整：三个写入口共用同一套“锁 → 查记录 → 执行”协议，目标绑定和并发语义只在一处实现。

## 指纹

| 操作 | 规范化输入 |
| --- | --- |
| create | `{"path": 规范化安全路径}`（不变） |
| disable | `{"root_id": 小写 root ID, "status": ..., "expected_revision": ...}` |
| restore | `{"root_id": 小写 root ID, "expected_revision": ...}` |

`encoding/json` 按键名排序输出 map，结果是确定的。操作类型已经在查询条件里，所以不放进指纹。
停用/恢复的响应 `id` 同时改用小写 ID，与其他接口里 PostgreSQL 输出的 `id::text` 一致。

## 并发语义

- **锁**：用两参数的 `pg_advisory_xact_lock(int4, int4)`。
  - 第一个参数是固定类别常量 `rootOperationLockClass = 0x524f4f54`（ASCII “ROOT”）；第二个参数是 `actor:type:key` 拼接串的 32 位 FNV-1a 哈希，在 Go 里计算，不依赖 PostgreSQL 的内部函数 `hashtext`。
  - 根据 PostgreSQL 文档，两参数锁与单个 bigint 键的锁不共用键空间。迁移锁（`database.go:22`）和扫描锁（`scans.go:12`）用的是 bigint 键，因此不会互相干扰。
  - actor 是 UUID，type 取值固定，所以拼接串没有歧义。哈希碰撞最多让两个请求多排一次队，是否重放仍按完整的 actor/type/key 精确判断。
  - PostgreSQL 的 text 不能含 NUL，分隔符用 `:`。
- **可见性**：事务显式使用 READ COMMITTED，每条语句都会取新快照。拿到锁之后的查询语句能看到上一个持锁事务已提交的记录，同义请求因此得到重放；不能用 REPEATABLE READ，否则快照在加锁语句开始时就已固定。
- **加锁顺序**：所有目录写操作都先取 advisory 锁，再取行锁或唯一索引锁，顺序一致，目录操作之间不会死锁。
  不同键的请求不共用 advisory 锁，它们对同一行的竞争仍由 `FOR UPDATE` 和 revision 处理，行为与现在相同。
- **等待时长**：同键的后续请求要等前一个事务结束。事务只做单行读写，时间很短。
  请求被取消时，pgx 会中断等待，返回既有的 503 `database_unavailable`。连接池不设上限（`openDatabase` 未调用 `SetMaxOpenConns`），等待不会占满连接槽。
- **唯一约束冲突**：同一 actor/type/key 已经被串行，写记录时不会再触发 23505。
  `writeRootOperationPersistenceError` 仍保留 23505 → 409 的映射作为防御，不需要修改。

## 旧记录（D1）

不迁移，不改写。改动前写下的停用/恢复记录，在改动后用原键重试时，因指纹不一致返回 409，不会被重放，也不会重复执行。
新增的指纹没有变化，旧的新增记录仍能正常重放。
旧记录继续用于历史列表，以及恢复操作查找对应停用记录的前态（`root_operations.go:247`，该查询不涉及指纹）。

## 回退

改动只涉及代码，没有 schema 变化，撤销提交即可回退。
本任务写下的新指纹记录，在旧代码下重试会因指纹不一致返回 409，仍然安全。

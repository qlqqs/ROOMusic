# CATP 并发测试复核

## 1. 根因类别

C（变更传播遗漏）与 E（隐式假设）：新增业务行锁之前的 CATP gate 后，旧 `TestPostgreSQLSetDefaultAndDetachQueueOnTheSameRelease` 仍用 SQL 文本 `FOR UPDATE` 筛出两个等待者。实际一个请求已持 CATP 等发行行，另一个请求等 CATP，只有一个符合旧筛选。不是两个业务操作必须并行取得 CATP 的要求。

## 2. 首轮证据

2026-11-04 相关包 `-race`：该断言超时（期望 2，得到 1），失败后的 schema 清理与未等待的业务事务交叉，又报告清理死锁。其他治理业务结果没有被放宽。

## 3. 修正与预防

- 以测试持锁连接 PID 为根，递归查询 `pg_blocking_pids` 的真实阻塞链，确认两个操作确实重叠；不把其他 schema 的等待者算进来。
- 失败也先释放测试锁并等待两个操作返回，再让 fixture 清理 schema。
- 保持结果断言：移出必须成功；设定要么先成功，要么随后返回 `not_in_group`；原组不把移出的版本当生效默认。
- 搜索合同记录此约束；相关治理/maintenance/app 集成继续验证 operations 记录锁 → CATP → 业务行的顺序。

## 4. 扩展检查

核对撤销与认领的既有并发测试在 operations 记录层强制重叠，仍保留其 KEY SHARE/FOR UPDATE 条件，没有为了新 gate 广泛改写这些测试。扫描挂接保持只增加原终态事务前的 gate。

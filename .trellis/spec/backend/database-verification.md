# 数据库验证

执行器与迁移作者合同见[数据库规范](./database-guidelines.md)。

## 验证范围

仓库集成测试使用支持版本的隔离 PostgreSQL，覆盖约束、回滚、冲突、幂等和代表性查询。扫描测试必须证明不完整扫描不能把来源标记为 missing。

迁移治理测试还必须覆盖全新库、重复启动时间戳稳定、旧 1/6/7 历史升级、名称/
校验和漂移、未知版本、事务回滚、并发 advisory lock 和锁等待上下文取消；集成
入口为 `./scripts/test-integration.sh`，使用隔离 schema 和 PostgreSQL 18。
回退相关：每个带回退的真实迁移由 `migrations_rollback_integration_test.go` 做升级 → 回退 → 再升级往返并比对 schema 快照；
执行器测试覆盖升降级往返、拒绝不改库、回退 SQL 失败原子回滚、迁移前备份失败、回退副本不同只告警、旧库补列在事务内；
`internal/backup/migration_backup_integration_test.go` 覆盖库缺回退列与降级前的迁移前备份不被锁住。

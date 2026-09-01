# Core 0 权限与目录事务验收测试设计

## 测试边界

- HTTP 层通过真实 `buildApplicationHandler`、`httptest` 和会话 Cookie 驱动。
- 持久化层连接隔离 PostgreSQL，执行现有迁移后创建管理员、普通用户、会话和目录 fixture。
- 每个测试使用独立 schema 或事务清理，避免依赖执行顺序。

## 测试矩阵

1. 身份状态：有效管理员、有效普通用户、禁用用户、撤销 session、过期 session、无 session。
2. 端点类别：共享读取、用户管理、目录读写、扫描启动、诊断、操作历史。
3. 目录操作：create、disable、restore、same-key replay、different-payload conflict、stale revision、missing inverse record。
4. 数据断言：root 状态/revision、operation row 数量与 before/after、响应状态和敏感字段缺失。

## 环境与回退

- 测试通过环境变量读取测试数据库 URL；未配置时显式 skip 数据库集成测试，CI/最终验收必须配置并实际执行。
- 不修改开发数据库中的业务数据；fixture 使用唯一 schema 并在测试结束后删除。

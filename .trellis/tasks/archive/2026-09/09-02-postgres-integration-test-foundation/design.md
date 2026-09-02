# 技术设计

## 边界

测试基础设施放在 `scripts/` 与 `backend/cmd/roomusic` 测试辅助中；生产启动与迁移代码不改。Compose 增加 PostgreSQL-only 测试入口，避免拉起 Redis/Meilisearch。

## 数据流

测试入口启动临时 PostgreSQL 容器并导出 `ROOMUSIC_TEST_DATABASE_URL`；Go 测试通过现有 `newIntegrationTestApplication` 打开管理连接，创建随机 schema，应用迁移后运行 HTTP handler，清理时删除 schema。

## 测试合同

- 集成测试使用真实 `pgx` 驱动，不使用 mock SQL。
- 每个测试独立 schema，schema 名称只使用安全标识符。
- 测试数据库凭据来自专用环境变量或脚本生成的临时配置，不读取生产 `.env`。
- 单元测试命令与集成测试命令分离；CI 显式设置数据库并在连接失败时失败。

## 回滚与兼容

新增脚本/Compose 配置可独立移除；现有 `ROOMUSIC_TEST_DATABASE_URL` 和随机 schema 机制保持兼容。若宿主机无 Docker，文档保留手工 PostgreSQL URL 入口。


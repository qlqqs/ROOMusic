# Core 0.1 实施计划

1. 完成 `ROOMUSIC_PUBLIC_URL` 文档、配置校验、来源校验测试，并更新 `.env.example`。
2. 增加数据库重置脚本/Make 目标，覆盖确认、防误用和重置后初始化状态测试。
3. 增加开发编排脚本，启动 PostgreSQL、Go watcher 和 Vite，并记录端口与清理方式。
4. 将用户管理、目录操作历史和初始化入口接入 React，补 DTO、错误态和角色可见性测试。
5. 更新 README 开发与反代说明，执行后端/前端质量门禁和生产构建。

验证命令：`go test ./...`、`go vet ./...`、`npm run lint`、`npm run typecheck`、`npm run test -- --run`、`npm run build`、`docker compose config --quiet`、`git diff --check`。

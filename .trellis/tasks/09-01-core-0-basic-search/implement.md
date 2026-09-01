# Core 0 PostgreSQL 基础搜索执行计划

1. 为现有 Release 列表 handler/API client 建立无查询参数的回归基线。
2. 增加 `q` 参数解析、长度/分页校验、参数化 SQL 过滤和一致 COUNT 查询。
3. 增加后端 handler 测试：标题、艺术家、Track 标题、大小写、空白、特殊字符、无结果、401、非法分页和稳定排序。
4. 更新前端 API 类型与浏览页：URL 查询状态、提交/清空、loading/empty/error/retry。
5. 增加前端 decoder/API 测试和搜索交互测试，确认刷新可复现查询且不保存 token。
6. 运行跨层回归和性能检查；确认封面、搜索引擎和扫描写模型未被引入。

验证命令：`GOCACHE=/tmp/roomusic-go-cache go test ./...`、`go vet ./...`、`go build ./...`、前端 `npm run lint`、`npm run typecheck`、`npm run test`、`npm run build`，以及 `docker compose config --quiet`。

回退点：先回退前端搜索入口，再回退 q 查询分支；不回滚既有 Release Graph 或迁移。

# 实施计划

1. [x] 检查现有集成测试并提取共享 fixture，确保测试结束关闭连接并删除 schema。
2. [x] 增加 PostgreSQL-only 测试启动入口，提供等待健康状态、连接 URL 输出和清理 trap。
3. [x] 增补用户事务、目录冲突和多 Medium 扫描的 PostgreSQL 回归测试。
4. [x] 更新 README、后端质量规范和 `.mise.toml`/脚本说明，区分普通测试与集成门禁。
5. [x] 运行前端不相关变更不要求全量；执行 Go 集成/单元测试、脚本语法、Compose 配置、Trellis 校验和 `git diff --check`。

## 验证记录

- `./scripts/test-integration.sh`：临时 PostgreSQL 18 容器启动成功，全部 `TestPostgreSQL` 通过，退出后容器、卷和网络清理完成。
- `cd backend && gofmt -l . && go test ./... -count=1 && go vet ./... && go build ./...`：通过。
- `bash -n scripts/test-integration.sh`、`python3 ./.trellis/scripts/task.py validate ...`、`git diff --check`：通过。

## 风险

- Docker 不可用时只能验证手工 URL；不得把跳过结果当作集成通过。
- 并发测试可能受 PostgreSQL 连接池和 schema search_path 影响，失败时优先收紧连接生命周期。
- 测试脚本不得复用生产数据卷或生产 `.env`。

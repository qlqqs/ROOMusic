# Core 0 权限与目录事务验收测试执行计划

1. 检查 Compose PostgreSQL 可用性和现有测试结构，建立隔离数据库 fixture。
2. 增加权限矩阵、禁用/撤销即时生效测试。
3. 增加目录操作成功、幂等、revision 与恢复冲突测试。
4. 若测试暴露合同缺陷，进行最小修复并保留回归测试。
5. 运行后端测试/vet、前端 lint/typecheck/test/build、Compose config 和 diff 检查。
6. 更新父任务验收结论，提交、归档补测子任务，并在所有验收通过后归档父任务。

## 验证命令

- `go test ./...`
- `go vet ./...`
- `npm run lint && npm run typecheck && npm run test -- --run && npm run build`
- `docker compose config --quiet`
- `git diff --check`

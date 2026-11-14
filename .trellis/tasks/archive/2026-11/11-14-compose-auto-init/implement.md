# 执行清单

- [x] 初始化脚本、镜像打包、Compose 挂载及依赖。
- [x] Go 密码文件读取及边界单元测试。
- [x] 更新 Compose 静态检查，新增隔离部署验证脚本。
- [x] 更新安装、迁移、备份文档与运行合同。
- [x] go test ./internal/platform/config、go vet ./internal/platform/config、Compose 静态检查及隔离容器验证。
- [x] 记录实际结果与未完成项；不混入主工作区的历史任务改动。

## 实际验证（2026-11-14）

- `mise exec -- go test ./internal/platform/config`：通过。
- `mise exec -- go vet ./internal/platform/config`：通过。
- `bash scripts/check-production-compose.sh`：通过，密码可省略、无卷、依赖链、端口、自定义备份目录及 override 子网均覆盖。
- `sh -n deploy/production/init.sh`、`bash -n scripts/check-production-compose.sh`：通过。
- `sg docker -c 'docker build -f deploy/production/Dockerfile -t roomusic-compose-test:local .'`：通过，使用真实生产 Dockerfile 构建任务专用镜像。
- `sg docker -c 'python3 scripts/tests/test_production_deployment.py --image roomusic-compose-test:local'`：通过。验证自动密码、UID/权限、无命名及匿名卷、重建复用、真实备份恢复、停机复制部署目录、丢失密码拒绝、特殊字符密码与冲突拒绝；测试容器、网络和目录已清理。
- `mise exec -- go test ./cmd/roomusic -run 'Test(ResetPasswordCommand|RestoreCommand|RestoreFailure)' -count=1`：通过，Go 命令均从 backend 执行。
- `git diff --check`、任务 JSON/JSONL 及拆分规范的链接/大小检查：通过。
- 未跑全量业务测试：未改业务逻辑或数据库表，最小配置测试与真实部署链路覆盖本次改动。

## 收尾边界

生产实例未操作，未发版或推送。新 Compose 配套的新镜像仍待正式发布；本次只用任务专用镜像验收。主工作区历史归档改动保持原样。

本次曾从仓库根调用 Go 命令，因不在 module 目录而未执行测试；改为 backend 目录后通过。实现与验证已完成，当前保留未提交供审阅，尚未合入 main。

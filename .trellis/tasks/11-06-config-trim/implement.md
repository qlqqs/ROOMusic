# 生产配置精简：执行计划

设计：[design.md](design.md)。依赖 builtin-backup 与 versioned-release 已提交（app 内置备份、带版本参数的 Dockerfile）。
分工与通用约束同 [builtin-backup 执行计划](../11-06-builtin-backup/implement.md)“所有派活 prompt 都要写明的约束”。

## 步骤

| 步骤 | 执行者 | 内容 | 设计章节 |
| --- | --- | --- | --- |
| C1 | astra | 改写 `deploy/production/compose.yaml`（含 `__RELEASE_VERSION__` 占位）、新增 `deploy/production/example.env`；删除 `deploy/production/backup.sh`、`restore.sh`、`scripts/production.sh`、`.env.prod.example`，去掉 Makefile 的 `prod*` 目标与 `.gitignore` 的放行行；`scripts/reset-password.sh` 提示；新增 `scripts/check-production-compose.sh`；`ci.yml` integration job 的 compose 校验与版本一致性检查；改写 `deploy/production/README.md`（含固定子网的 `compose.override.yaml`、本机过渡与回退步骤）与根 `README.md` 生产段；`git grep` 清理旧引用（builtin-backup A9 与本任务 A6） | §2–§8 |
| C2 | Sol | 运行 `scripts/check-production-compose.sh`（A2、A3 静态部分）；本机构建 amd64 镜像后在 `roomusic-drill` 项目（`compose.override.yaml` 子网 `10.203.51.0/24`）验证 A1、A3、A4；残留引用检查 | §9、§12 |
| C3 | 主会话 | 审阅 diff 与文档（中文、无多余说明）；通过后提交 | — |

过渡演练（A5）在父任务 P4 与升降级演练一起做。真实切换在父任务 P8，切换前须用户确认。

## 验证命令（最小集）

```bash
scripts/check-production-compose.sh
for s in deploy/production/*.sh scripts/*.sh; do bash -n "$s"; done
git grep -nE 'make prod|production\.sh|\.env\.prod\.example|ROOMUSIC_SUBNET|backup\.sh|restore\.sh|roomusic-prod-tools|ROOMUSIC_TOOLS_IMAGE|restore-check' -- ':!.trellis/tasks/archive'
```

## 回滚点

- C1 一次提交。生产实例在切换前仍跑旧镜像与旧 compose（由运行中的容器保持），不受仓库文件删除影响；需要旧方式时按 design §8.2 A 路线从旧提交取 compose。

# 执行计划：Docker Compose 部署打包与上线演练

依据：[prd.md](./prd.md)、[design.md](./design.md)。第 1 步在 origins 提交后开始，可与 throttle 并行；第 2 步等三个子任务的代码都提交后进行。

## 步骤

| 步 | 执行者 | 内容 | 产出 |
| --- | --- | --- | --- |
| 1 | 后端实现（`codex/gpt-6-astra`，high，full-access） | 按 design 编写 `deploy/production/` 下的 Dockerfile、dockerignore、`entrypoint.sh`、`compose.yaml`、`backup.sh`、`restore.sh`、`README.md`（部署说明）与根目录 `.env.prod.example`；新增 `scripts/production.sh`（从 `main` 导出构建应用镜像与 tools 镜像、用被部署提交里的 `compose.yaml` 启动）并改 Makefile（`prod`、`prod-*` 目标）、`.gitignore`、`.mise.toml`；删除 `scripts/prod.sh`，调整 `dev.sh`、`dev-reset.sh`、`reset-password.sh` 读取的配置文件；CI 增加配置与版本一致性检查；README 开发与生产部分、`core0-runtime-contracts.md` 更新 | 改动文件；§15 的验证命令结果（含一次 `docker build`） |
| 2 | 主会话 | 审阅并提交第 1 步 | 提交哈希 |
| 3 | 测试（`codex/gpt-6.1-sol`，high，full-access） | 按 design §10 执行上线演练第 0–14 步，记录写入 `research/drill/`；第 8.3 步暂停并回报，由主会话请用户用手机或另一台电脑操作；任何一步失败时停下回报，不自己改部署文件 | 每步命令、结果、截图；清理与开发库核对结果 |
| 4 | 主会话 | 审阅演练记录；有问题打回第 1 步的 agent 修改后重新演练相关步骤；通过后提交演练记录 | 提交哈希 |
| 5 | 测试（同第 3 步的 agent） | P10：按部署说明在仓库根目录创建 `.env.prod`（端口 8090，来源清单 `http://192.168.50.206:8090`，数据库密码随机生成，不打印），`make prod`；核对 C4；按 design §10.2 核对生产命令（C6）。不替用户初始化管理员 | 正式实例状态、首份备份文件名 |

## 验证命令

见 design §15；其余验证就是演练本身。本子任务不跑 Go 或前端测试（不改应用代码）。

## 注意

- 演练与正式启动都不能动用户的 `roomusic-postgres-1` 与 `roomusic_*` 数据卷；第 0 步与第 14 步对比，正式启动后再对比一次。
- `.env.prod` 不提交，内容不出现在任何日志或回报里；`.env.dev` 只看变量名。
- 第 1 步改 `dev.sh`、`dev-reset.sh` 后不实际运行它们（会碰用户的开发库），只做 `bash -n` 与代码审阅。
- 子 agent 不提交 git。

## 回滚点

- 第 1 步的提交可单独回退，不影响应用代码。
- 正式实例：`make prod-stop`（保留卷）。删除卷或备份目录前必须经用户确认。

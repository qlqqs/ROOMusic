# 技术设计

## 环境分层
- `.env.dev`：本地开发唯一默认配置，`ROOMUSIC_ENV=dev`、非安全 Cookie、公开开发 URL 和本地数据库。
- `.env`：生产部署配置，由运维填写真实值；脚本拒绝缺失文件并要求生产安全 Cookie。
- `.env.example`：不含秘密的生产/开发变量参考，明确复制目标。

## 启动边界
`scripts/dev.sh` 负责加载 `.env.dev`、启动 Compose 开发依赖、Go 后端和 Vite；不读取用户当前 shell 中可能污染环境的旧值，命令行显式变量可覆盖非敏感端口。`scripts/prod.sh` 负责加载 `.env`、执行前端生产构建（可选跳过）、启动 Go 二进制/`go run`，不启动 Vite。

## Vite 配置
`vite.config.ts` 保持生产构建与输出目录；`vite.config.dev.ts` 引用基础配置并添加 `server.port=5173`、`allowedHosts=true`、API proxy。`package.json` 的 dev 脚本显式传入开发配置，build 使用默认配置。

## 兼容与回滚
不改变后端环境变量名称和 API。旧 `.env` 用户可继续手动运行后端；脚本切换后若需回滚只需恢复脚本与 package 配置。Makefile 保留现有目标并转调脚本。

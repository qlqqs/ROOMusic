# 执行计划

1. 创建 `.env.dev`、更新 `.env.example`，补充生产配置说明。
2. 拆分 `vite.config.ts` 与 `vite.config.dev.ts`，更新 `frontend/package.json`。
3. 编写 `scripts/prod.sh`，调整 `scripts/dev.sh` 的环境文件加载和无 Make 启动路径。
4. 更新 Makefile 与 README，说明直接脚本和 8080/5173 端口。
5. 运行 `npm run typecheck`、`npm run lint`、`npm run build`，执行 Shell 语法和 Compose 配置检查。
6. 检查生产配置不包含开发放行项，并记录回滚方式。

风险文件：`.env.example`、`scripts/dev.sh`、`scripts/prod.sh`、`frontend/vite.config.ts`、`frontend/package.json`、`Makefile`、`README.md`。

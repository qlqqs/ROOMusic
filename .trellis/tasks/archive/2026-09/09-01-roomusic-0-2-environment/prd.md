# ROOMusic 0.2 环境配置

## 目标
建立清晰的开发与生产整体环境配置，让项目不依赖 Make 也能启动，并避免把开发域名放行、调试参数或不安全 Cookie 设置带入生产。

## 已确认事实
- 当前开发入口为 `scripts/dev.sh`，会启动 Compose 依赖、Go 后端和 Vite。
- Go 后端通过 `ROOMUSIC_ENV`、`ROOMUSIC_SECURE_COOKIES`、`ROOMUSIC_PUBLIC_URL` 等环境变量控制运行行为。
- 生产前端应由 Go 单体在 `8080` 同源提供；Vite 仅用于开发。
- 用户选择采用 Go 单体直连生产策略，PostgreSQL 由外部或 Compose 提供。
- 当前系统未安装 `make`，运行方式不能依赖 Make。

## 范围内
- 增加 `.env.dev` 开发配置和更新 `.env.example` 模板。
- 保留无后缀 `.env` 作为生产配置约定，补充安全注释与变量边界。
- 拆分 Vite 开发/生产配置，开发配置允许自定义 Host，生产构建不包含开发服务器放行项。
- 增加无需 Make 的开发与生产启动脚本；脚本明确加载对应环境文件。
- 更新 Makefile 为可选包装，并更新 README 的启动说明。
- 提供开发 Compose 配置复用现有依赖，生产不强制启动 Redis/Meilisearch。

## 范围外
- 新增生产容器镜像、反向代理、TLS 证书自动化或云平台部署文件。
- 修改业务 API、数据库迁移、认证协议或前端功能。

## 验收标准
1. `./scripts/dev.sh` 默认读取 `.env.dev`，启动开发依赖、Go `8080` 和 Vite `5173`；通过任意开发 Host 可访问。
2. `./scripts/prod.sh` 读取 `.env`，只构建并启动 Go 单体，生产环境强制安全 Cookie，不启动 Vite。
3. `npm run dev` 使用开发 Vite 配置；`npm run build` 使用生产构建配置，生成 Go 可嵌入资产。
4. 未安装 Make 时，直接运行脚本仍可完成启动；Makefile 仅作为便利入口。
5. 生产配置不包含 `allowedHosts: true`、开发数据库密码示例或 `ROOMUSIC_SECURE_COOKIES=false`。
6. 文档说明端口、环境文件、启动命令和停止方式，且新增/修改文档使用简体中文。
7. 相关 Shell、Vite、Go 构建与配置校验命令通过。

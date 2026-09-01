# Core 0.1 技术设计

## 来源校验

`serverConfig` 新增 `PublicURL`，启动时解析为无路径的绝对 origin。写请求的 `Origin` 必须同时满足协议与公网 host 校验；未配置时保留现有请求 Host 回退行为。生产环境要求 HTTPS 与安全 Cookie。

## 开发工作流

前端继续使用 Vite dev server（5173），通过代理转发 `/api` 到 Go（8080）。Go 使用文件监听重启工具（优先 air，不可用时提供轮询脚本）。Compose 仅负责 PostgreSQL；统一脚本负责依赖启动、环境加载和进程清理。

## 数据库重置

新增 `make dev-reset`/脚本，检查 `ROOMUSIC_ENV` 非 production 后，在事务中清空业务表并保留 `schema_migrations`，要求交互确认或 `CONFIRM=1`。重置后 `setup_state` 为空。

## 前端管理体验

增加用户管理、目录操作历史和初始化状态入口；DTO 与后端错误码保持严格解码。管理员控件按角色显示，后端授权仍是最终边界。

## 回退与兼容

未配置 `ROOMUSIC_PUBLIC_URL` 的开发环境沿用现有 Host 校验。Vite 与内嵌生产前端共用 API 契约；生产构建仍生成 Go embed 资源。

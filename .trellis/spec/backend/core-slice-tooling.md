# Core 0 首切片工具选型

本切片后端使用 Go 标准库 `net/http` 提供版本化 REST，使用 `database/sql`
和 `pgx/v5` 连接 PostgreSQL，并通过 `embed.FS` 内置有序 SQL 迁移。身份密码
采用带随机盐的 SHA-256 摘要，session 使用随机 opaque token，数据库只保存
token 摘要。前端使用 React、TypeScript、Vite、原生 `fetch`（Cookie credentials）
和 Vitest；不引入 Redis、Meilisearch、GraphQL 或文件写入依赖。

本阶段验证命令：后端在安装 Go 1.25 后运行 `gofmt -w .`、`go test ./...`、
`go vet ./...`、`go build ./...`；前端运行 `npm run lint`、`npm run typecheck`、
`npm run test` 和 `npm run build`。当前执行环境未安装 Go，因此后端门禁需在具备
Go 工具链的环境补验。

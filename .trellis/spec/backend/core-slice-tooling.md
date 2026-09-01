# Core 0 首切片工具选型

本切片后端使用 Go 标准库 `net/http` 提供版本化 REST，使用 `database/sql`
和 `pgx/v5` 连接 PostgreSQL，并通过 `embed.FS` 内置有序 SQL 迁移。身份密码
采用 bcrypt 哈希，session 使用随机 opaque token，数据库只保存
token 摘要。前端使用 React、TypeScript、Vite、原生 `fetch`（Cookie credentials）
和 Vitest；不引入 Redis、Meilisearch、GraphQL 或文件写入依赖。

本阶段验证命令：后端运行 `gofmt -w .`、`go test ./...`、`go vet ./...`、
`go build ./...`；前端运行 `npm run lint`、`npm run typecheck`、`npm run test`
和 `npm run build`。数据库变更另需使用 PostgreSQL 执行迁移并运行集成测试。

格式扩展合同：OGG、Opus、WAV parser 必须先验证容器/codec magic，再提取有限
标签；`.opus` 必须包含 `OpusHead`，未知或损坏分页记录诊断。CUE 仅接受
UTF-8/UTF-16、单一已知音频 `FILE`、`TRACK nn AUDIO` 与合法 `INDEX 01`，所有
数字和引用路径错误都归类为 unsupported 诊断。CUE 虚拟来源键必须包含规范化
引用文件与 track 编号，确保 FILE 变更不会复用旧 Track 身份；解析失败不得触发
missing 对账或阻塞其他合法文件。

搜索合同：`GET /api/v1/releases` 的可选 `q` 参数先 trim，最多 200 字节；非空值
通过参数化 `ILIKE` 匹配 Release 标题、艺术家和关联 Track 标题，并转义 `%`、`_`
和 `~`。COUNT 与分页列表必须复用同一过滤条件，结果保持 `title, artist, id` 稳定
排序；查询失败返回安全错误和 request ID，不改变扫描或目录状态。

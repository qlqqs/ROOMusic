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
标签；`.opus` 必须包含 `OpusHead`，未知或损坏分页记录诊断。M4A 使用
`io.ReaderAt` 按 ISO-BMFF atom 边界读取，以共享的 atom 数量和 metadata 字节预算
限制递归；媒体 payload 大小不得成为拒绝文件的理由。CUE 支持 UTF-8/BOM、
UTF-16 BOM、GBK、Shift-JIS、Big5、多 `FILE`、sheet/track 元数据、ISRC 和多个
`INDEX`；每个引用都必须独立完成根目录 containment、存在性和 symlink 校验。
CUE 虚拟来源键包含规范化 sheet、父来源、track 编号和 `INDEX 01`，同时兼容首次
修复扫描复用旧身份。语法、引用或音频解析失败写入有界诊断，使扫描进入
`incomplete`，但不得阻止其它合法候选提交，也不得触发 missing 对账。
扫描器只解析 containment 内的普通文件；FIFO、设备和不安全 symlink 必须产生分类
诊断。parser 的默认 track/disc 值必须标记为 inferred，organizer 才能用明确的
Disc/CD/Disk 目录和稳定路径位置补齐，而不得覆盖显式标签。CUE sheet 与 track 的
PERFORMER 来源分别记录为 `cue_sheet`、`cue_track`，逐轨构造 observation 时必须复制
可变 provenance map，禁止轨间污染。只有前 64 KiB 内含明确 EAC/XLD 产品签名的普通
`.log` 文件才能补充 `source_type=CD`、`media_type=CD`；不得从 codec、采样率或目录名
反推发行介质。

搜索合同：`GET /api/v1/releases` 的可选 `q` 参数先 trim，最多 200 字节；非空值
通过参数化 `ILIKE` 匹配 Release 标题、艺术家和关联 Track 标题，并转义 `%`、`_`
和 `~`。COUNT 与分页列表必须复用同一过滤条件，结果保持 `title, artist, id` 稳定
排序；查询失败返回安全错误和 request ID，不改变扫描或目录状态。

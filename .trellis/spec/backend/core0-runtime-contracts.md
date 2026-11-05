# Core 0 当前运行合同

## 1. 范围与触发

本合同记录已落地的 Go 启动、`/api/v1` REST、PostgreSQL 用户与目录状态、扫描终态、
发行/逐轨元数据、真实音乐只读 Smoke 与同源资产边界。后端仍位于过渡单体
`backend/cmd/roomusic`（入口）与 `backend/internal/*`（能力代码，B0 于 2026-09-30 完成拆分）。

2026-09-27 删除了旧前端；2026-10-01 的 M1a 按 Classical 设计重建 `frontend/src/`（外壳、会话、目录与扫描管理、
成员管理、简版概览），Go 内嵌的是它的生产构建。本文件涉及前端 transport、decoder 与前端测试的内容，现在由
`frontend/src/shared/api` 与各 feature 的 `api/` 实现，前端规范见 [前端规范索引](../frontend/index.md)。
Classical 设计见 [DESIGN.md](../../../frontend/DESIGN.md)，阶段与依赖见
[Core 1 路线](../../../docs/architecture/core-1-roadmap.md)（2026-09-28 已批准）。
M1a 没有改变后端 API、数据库或运行边界，后端只新增了生成 API 响应样本的集成测试与其 testdata。
2026-10-02 的 F3 补齐了 M1a 暴露的后端缺口（没有迁移）：管理员的扫描记录列表、身份响应带用户 `id`、身份与成员接口的错误映射和
密码长度规则、静态资源/JSON/封面的缓存头；合同见 [身份与成员合同](./identity-contracts.md)、下文“HTTP 缓存”和各专题文件的错误矩阵。

### 专题文件

本合同按主题拆分，每个规范文件不超过 24 KB（规则见 [后端规范索引](./index.md)）。本文件保留平台层合同；后续技术债在 [core0-tech-debt.md](./core0-tech-debt.md)；
其余主题各有一个文件，沿用同样的七节结构，没有内容的节省略：

| 主题 | 文件 | 内容 |
| --- | --- | --- |
| 身份与成员 | [identity-contracts.md](./identity-contracts.md) | 密码规则、初始化与成员接口、用户停用事务、写入失败映射 |
| 目录与扫描流程 | [library-scan-contracts.md](./library-scan-contracts.md) | 目录新增与操作幂等、扫描完整性、暂存与身份规划、曲目与 Release 身份、CUE、抓轨日志、艺人与 AlbumArtist、封面 |
| 扫描协调 | [scan-coordination-contracts.md](./scan-coordination-contracts.md) | 扫描状态与记录、持久化取消、跨进程协调锁 |
| 真实库 Smoke | [real-library-smoke-contracts.md](./real-library-smoke-contracts.md) | 真实音乐只读验收、V0 对照 |
| 发行 REST | [catalog-rest-contracts.md](./catalog-rest-contracts.md) | 发行列表、详情、evidence、封面摘要与按组只读接口 |
| 鉴权直出 | [playback-contracts.md](./playback-contracts.md) | M2a 格式表、预检、音频、Range、来源版本与撤销复核；M2b 的 `delivery`、202 与派生错误码 |
| 无损派生 | [playback-derivation-contracts.md](./playback-derivation-contracts.md) | M2b ALAC → FLAC：ffmpeg 与缓存配置、启动自检、转换与核对、上限与取消、测试 |
| 内嵌歌词 | [lyrics-contracts.md](./lyrics-contracts.md) | M2b 歌词：标签提取、`tracks.lyrics`、歌词接口与 LRC 解析规则、测试 |
| 播放历史 | [listening-contracts.md](./listening-contracts.md) | 阶段 P：`play_history`、计次规则、记录与最近播放与清除接口、按用户隔离、前端计次与上报、测试 |
| 受治理操作 | [governance-contracts.md](./governance-contracts.md) | 阶段 M3：操作记录与 `operations.Run`、发行字段修正与有效值视图、人工归组、撤销、操作记录接口、测试 |
| 默认版本与版本对比 | [default-version-contracts.md](./default-version-contracts.md) | M4 第一项：组的默认版本、生效规则、设定与撤销、版本对比、测试 |
| 元数据问题与核对 | [issue-review-contracts.md](./issue-review-contracts.md) | M4 第二项：“需要关注”的三类拆分、核对与撤销、问题读取、测试 |
| 认领 | [claim-contracts.md](./claim-contracts.md) | M4 第三项：待认领的判定与推荐、预览与令牌、认领与撤销、读取接口、测试 |

## 2. 接口签名

- 生产启动：`make prod`（`scripts/production.sh`）从 `main`（或 `REF=`）导出构建应用与 tools 镜像，
  使用该提交的 `deploy/production/compose.yaml` 启动，项目为 `roomusic-prod`；升级前总是备份，失败不换应用版本。
- 开发启动：`./scripts/dev.sh`，默认读取 `.env.dev`，Go 运行在 `backend/`，
  Vite 通过开发配置代理 `/api` 到 `:8080`。
- HTTP：`POST /api/v1/users`、`PATCH /api/v1/users/{id}`、
  `POST /api/v1/library-roots`、`POST /api/v1/scans`、管理员专用的 `GET /api/v1/scans`、
  `GET /api/v1/scans/active`、`GET /api/v1/scans/{id}`、
  `POST /api/v1/scans/{id}/cancel`、`GET /api/v1/scans/{id}/diagnostics`、
  `GET /api/v1/releases`、`GET /api/v1/releases/{id}`、管理员专用
  `GET /api/v1/releases/{id}/evidence`、按组只读的 `GET /api/v1/release-groups` 与
  `GET /api/v1/release-groups/{id}`，以及已登录用户可读的 `GET /api/v1/artworks/{id}` 与 `GET /api/v1/tracks/{id}/lyrics`（见[内嵌歌词](./lyrics-contracts.md)）。
- M2a：`GET /api/v1/playback/formats`、`GET /api/v1/tracks/{id}/playback` 与
  `GET|HEAD /api/v1/tracks/{id}/audio`，具体合同见[鉴权直出](./playback-contracts.md)；M2b 在同样三个接口上增加派生交付
  （预检可返回 202），见[无损派生](./playback-derivation-contracts.md)。
- P：`POST /api/v1/me/plays`、`GET /api/v1/me/plays/recent-releases` 与 `DELETE /api/v1/me/plays`，只作用于当前会话用户，见[播放历史](./listening-contracts.md)。
- M3（管理员）：`GET|PATCH /api/v1/releases/{id}/corrections`、`POST /api/v1/releases/{id}/group`、`GET /api/v1/operations`、
  `POST /api/v1/operations/{id}/revert`，见[受治理操作](./governance-contracts.md)。
- M4：`POST /api/v1/release-groups/{id}/default`（管理员）、`GET /api/v1/release-groups/{id}/comparison`，见[默认版本](./default-version-contracts.md)；问题与核对、认领（`GET /api/v1/release-claims`、`GET /api/v1/releases/{id}/claim-preview`、`POST /api/v1/releases/{id}/claim` 等）见上表。
- 前端请求形态为 `apiRequest(spec, decode)`（`frontend/src/shared/api/request.ts`）：成功响应经边界 decoder（第二个参数是状态码，
  同一路径 200 与 202 形状不同时用它核对响应体与状态码相符），
  非 2xx 经统一的错误信封 decoder；响应不是 JSON、信封不合法或 decoder 失败时归为 `invalid_response`，不进入缓存。
  函数名不构成架构限制，合同是“边界解码一次、失败安全分类”。

- 部署运维：`scripts/production.sh [--dry-run] up [--ref <提交>]`、`stop|status|logs|backup`、`reset-password <用户名>`、
  `build-image [--ref <提交>] [--prefix <前缀>]`；恢复：`deploy/production/restore.sh check|production <备份>`。

## 3. 合同

### 请求与响应

- 状态变更请求必须通过 Origin 校验、HttpOnly session 和后端角色授权；前端
  角色仅控制展示。来源规则见[身份合同](./identity-contracts.md)“来源与会话 Cookie”。
- 错误 envelope 为 `{ "error": { "code": string, "message": string }, "request_id": string }`。
  消息不得包含 SQL、绝对路径、密码、token 或数据库 URL。
- 前端 transport 沿用 Cookie credentials（`same-origin`）与 JSON 请求体的 Content-Type
  合同，请求带客户端生成的 `X-Request-ID`；浏览器不读取或存储 session token，不为媒体资源强加 JSON 解码。
- JSON 请求体上限 1 MiB；搜索词最多 200 字节；发行分页默认 `page=1`、
  `page_size=50`，最大 100；扫描记录分页默认 `page=1`、`page_size=20`，最大 100；扫描诊断最多 100 条。
  分页参数由 `httpserver.ParsePagination` 统一解析（`page` 至少 1，`page_size` 在 1 到上限之间，偏移不得溢出 `int`），
  不合法的值返回 `400 invalid_pagination`；超出末页返回空 `items` 而总数照常。
- 请求体的解码与校验分两步：不是合法 JSON 的请求体由 `DecodeJSON` 写出 `400 invalid_json` 并返回 `false`，调用方必须
  直接返回，不得再写第二个错误信封（曾因 `!DecodeJSON(...) || 校验失败` 的写法让一个响应带两个 JSON 文档）。
- 发行列表/详情的封面摘要与按组只读接口的合同见 [Catalog REST 合同](./catalog-rest-contracts.md)。

### HTTP 缓存

| 响应 | `Cache-Control` | 说明 |
| --- | --- | --- |
| `assets/` 下存在的文件 | `public, max-age=31536000, immutable` | Vite 构建的文件名带内容哈希，内容变化就换名 |
| `/` 与 SPA 回落页 | `no-cache` | `text/html`，不加 `ETag`（约 1 KB，验证收益小） |
| `assets/` 以外存在的文件（`licenses/` 下的许可文本等） | `no-cache` | `http.FileServer` 的既有行为；内嵌文件的修改时间为零，没有 `Last-Modified` 与 `ETag` |
| `assets/` 下缺失的文件、`assets` 目录及其子目录 | 无 | `404 text/plain`，不回落到入口页，不列目录 |
| 其他目录路径（`/licenses/` 等） | `no-cache` | 回落到入口页，不列目录 |
| 经 `httpserver.WriteJSON` 写出的 JSON（含错误信封与 `/healthz`） | `no-store` | 随会话与数据变化，浏览器与中间缓存都不保留 |
| `GET /api/v1/artworks/{resource_id}` 200 | `private, no-cache` | `ETag: "<resource_id>"`；每次显示前带凭据重新验证 |
| 曲目原文件音频 200/206/HEAD | `private, no-store` | 已验证媒体类型、强 ETag、`nosniff`，无 `Last-Modified`；先鉴权与核对来源 |
| 封面带匹配的 `If-None-Match` | `private, no-cache` | `304`，同一个 `ETag`，没有响应体，不读文件 |

- 请求路径先经 `path.Clean`，再判断是否在 `assets/` 下：`/assets/../licenses/x.txt` 清理后不在 `assets/` 里，不会因为字面上带着
  `assets/` 就被长期缓存。
- `immutable` 只能给带哈希的文件。`cmd/roomusic` 的测试断言内嵌 `assets/` 下每个文件名都含哈希段；给名字固定的文件加
  `immutable` 会让浏览器拿不到新版本。
- 封面的资源 ID 是内容 SHA-256 加扩展名，同一 ID 的字节永不改变，所以 `ETag` 就是资源 ID（强校验值）。处理顺序固定：
  未登录 `401`（即使 `If-None-Match` 匹配也不是 304）→ 查记录并校验存储键，不存在或不安全 `404`（即使带着 `If-None-Match`
  也不是 304）→ `If-None-Match` 命中则 `304`（不读文件）→ 读文件返回 `200`。`artwork.Lookup` 只查记录，`artwork.Read` 才读文件。
  `If-None-Match` 按 RFC 9110 的弱比较：忽略 `W/` 前缀，逐个比较逗号分隔的标签，`*` 命中任何存在的资源，无法解析的值视为不命中。
- 选择 `private, no-cache` 而不是 `no-store`：所有登录用户本来就能读所有封面，验证主要防的是“退出登录后从浏览器缓存直接显示”；
  `no-store` 会让每次显示都整张重新下载。退出后重新验证得到 `401`，旧响应不再被使用。`no-store` 不追溯清除已保存的数据
  （见 [ADR 0001](../../../docs/architecture/adr/0001-frontend-foundations.md)）。未来的音频缓存由
  [ADR 0004](../../../docs/architecture/adr/0004-media-delivery.md)另行约束。
- 浏览器端的缓存行为（重新验证、退出后的封面）由响应头合同与集成测试保证；Playwright 跑在 `vite preview` 上，不经过 Go 服务。

### 数据库与迁移

- PostgreSQL 是 Core 0 唯一业务权威，连接实现为 `database/sql + pgx/v5`。
- 启动迁移由 `backend/internal/platform/postgres`（`migrate.go`）负责：嵌入的
  `backend/migrations/*.sql` 必须从版本 1 连续排序，原始字节使用 lowercase
  SHA-256；执行器在一个事务中持有 `pg_advisory_xact_lock(0x524f4f4d55534943)`，
  成功版本在 `schema_migrations` 中记录文件名、校验和与时间。
- `0008_migration_metadata.sql` 为迁移记录增加 `name`/`checksum`。旧版数据库
  可能只有 0001、0006、0007 三条记录；确认这段连续历史后，0002--0005 只做一次
  元数据基线，不重放 SQL。名称或校验和漂移、未发布版本和无法证明连续历史均
  fail closed；不提供 down migration，生产回退使用备份或 forward-fix。
- 迁移 SQL、DDL 和记录写入同一事务；锁等待取消或任一步失败都会回滚，只有
  提交成功后才执行中断扫描恢复并进入 ready。

### 环境与资产

- Node/Go 版本由 `.mise.toml` 管理，依赖安装使用 `npm ci`；`package-lock.json`
  是前端可重复安装依据，`package.json` 的直接依赖全部精确固定（M1a）。
- Vite 的生产构建目标仍为唯一的 `backend/cmd/roomusic/web`，不手工编辑该目录；产物随源码提交，
  CI 运行真实构建与资产漂移检查（M1a 恢复）。开发
  `vite.config.dev.ts` 才允许自定义 Host 和 API 代理。
- 开发 Vite 通过公网反代访问时，`allowedHosts` 只放行 `ROOMUSIC_PUBLIC_URL` 的主机名，
  不得使用 `allowedHosts: true`；后端在未配置 `ROOMUSIC_ALLOWED_ORIGINS` 时按 `ROOMUSIC_PUBLIC_URL`、Origin scheme/host
  和 `ROOMUSIC_SECURE_COOKIES` 校验写请求；配置清单后只按清单，Cookie 的 `Secure` 跟随请求来源协议。
- `.env.dev`、`.env.prod`、`.env` 是本机配置，必须被 Git 忽略；仓库只跟踪不含真实值的
  `.env.example` 与 `.env.prod.example`。开发脚本默认读 `.env.dev`，生产显式读 `.env.prod`，不再 source 根目录 `.env`。`backend/data/` 等运行期数据（含封面缓存与 `media-cache/` 派生缓存）同样不得提交。
- ALAC 派生需要 ffmpeg：生产镜像构建时从固定摘要镜像复制，开发由部署者安装（项目不单独分发二进制）；`ROOMUSIC_FFMPEG_PATH`（绝对路径，空则在 `PATH` 中查找）与
  `ROOMUSIC_MEDIA_CACHE_MAX_MIB`（默认 10240，最小 256）；找不到或自检失败时应用照常启动，ALAC 保持“不能直接播放”。
  `scripts/ffmpeg-pinned.sh` 取出按摘要固定的 ffmpeg 7.1.1，测试、CI 与推荐部署共用。详见[无损派生合同](./playback-derivation-contracts.md)。
- `scripts/dev.sh` 默认只确保 PostgreSQL、Go 和 Vite；Redis/Meilisearch 是
  Compose/mise 可选服务。开发的 `mise run env-*` 显式使用 `--env-file .env.dev`。
- 生产镜像固定 Go、Alpine、PostgreSQL 基础镜像版本与摘要，ffmpeg 与 `scripts/ffmpeg-pinned.sh` 同一摘要；
  使用已提交的前端，不依赖 Node。uid 1000 运行，音乐只读挂载，缓存与数据库卷分离。
  应用只发布 IPv4 8090，生产固定子网 `10.203.50.0/24`（演练 `10.203.51.0/24`），与开发同时运行互不影响。
  入口等数据库接受连接后启动，`/readyz` 用于镜像健康检查。生产要求非空 `ROOMUSIC_ALLOWED_ORIGINS`，格式与冲突由后端校验。
- tools 镜像含随提交打包的备份/恢复脚本，PostgreSQL 版本与数据库相同；每日 `pg_dump -Fc`，保留 14 天，失败有日志，
  超过 26 小时无成功备份即不健康；不备份 `/data` 或音乐文件。停止只用 `stop`、不删卷，密码恢复用 `make prod-reset-password U=<用户名>`。
  运维与恢复步骤见[部署说明](../../../deploy/production/README.md)。

### CI 质量门禁

- `.github/workflows/ci.yml` 在 Pull Request、推送到 `main` 和手动触发时运行，权限
  固定为 `contents: read`，同一分支的新运行会取消旧运行。
- 工作流使用 `.mise.toml` 中的 Go `1.25.10` 和 Node.js `24.16.0`。后端门禁从
  `backend/` module 运行 `gofmt`、`go test`、`go test -race`、`go vet` 和 `go build`；
  前端作业使用 `frontend/package-lock.json` 依次执行 `npm ci`、lint、typecheck、`npm run test`
  （Vitest）与 `npm run build`，没有“入口缺失”分支。
- 生产构建之后执行 `git diff --exit-code -- backend/cmd/roomusic/web`，并检查该目录没有未跟踪生成文件；
  随后 `npx playwright install --with-deps chromium` 与 `npm run test:e2e`（对生产构建的浏览器冒烟、无障碍与
  响应式检查，失败时上传报告）。
  集成 job 逐个执行 `bash -n` 检查 `scripts/*.sh` 与 `deploy/production/*.sh`，校验开发、测试、生产三个 Compose
  及 ffmpeg、Go、PostgreSQL 固定版本一致性（CI 不构建镜像），并运行 `./scripts/test-integration.sh`；后者先经 `scripts/ffmpeg-pinned.sh` 取出固定版本 ffmpeg（取不到即失败，
  派生用例不得静默跳过），真实启动 PostgreSQL 18 并运行 `TestPostgreSQL|TestFFmpeg`，结束时清理专用容器和数据卷。
  真实播放 job 运行 `./scripts/playback-e2e.sh`，同样经该脚本取得同一个 ffmpeg。
- **API 响应样本**：集成测试 `TestPostgreSQLAPIResponseSamples`（`backend/internal/app/api_samples_integration_test.go`）
  经真实 handler 走固定流程，把响应写成 `backend/internal/app/testdata/api-samples/` 下的 JSON 样本（只含状态码与响应体）：
  UUID 与时间戳规范化为固定值、请求 ID 固定、按键排序。默认**逐字比较**，不一致或目录里有本次没有产出的文件都失败；
  确认是有意的合同变更后用 `ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 重写（会删除多余文件），并同步前端
  decoder。样本由集成作业把关漂移，前端作业读取它们（`frontend/src/test/api-contract.test.ts`，Playwright 的受控响应也取自样本）。
  测试断言样本不含允许根目录的绝对路径、临时目录前缀、`password`、`roomusic_session`、会话令牌与测试密码；扫描素材是临时目录里的
  合成文件，不读取真实音乐。
- CI 不启动或依赖 Redis/Meilisearch，不读取生产 `.env.prod`，也不使用真实音乐目录。普通
  测试因未设置 `ROOMUSIC_TEST_DATABASE_URL` 而跳过时，不得被视为 PostgreSQL 集成门禁
  通过。

### HTTP 完成日志

请求中间件在每个请求结束后恰好写出一个 `http.request.completed` 结构化 JSON 事件。
事件字段为：

| 字段 | 合同 |
| --- | --- |
| `event` | 固定为 `http.request.completed` |
| `module` | 固定为 `platform` |
| `message` | 固定为 `http request completed` |
| `request_id` | 有界且经过字符集校验，并回写 `X-Request-ID` |
| `method` | HTTP 方法 |
| `route_template` | ServeMux 注册的路由模板；未匹配或落入 `/api` 兜底时为 `<unmatched>` |
| `status` | 最终状态码；隐式响应默认为 200 |
| `response_bytes` | 实际写入字节数 |
| `duration_ms` | 非负耗时（毫秒） |
| `actor_id` | 可选稳定用户 ID，仅认证成功时出现 |

JSON `slog` handler 另外提供 UTC `time` 和 `level`。状态小于 400 记录为 `INFO`，
400--499 为 `WARN`，500 及以上为 `ERROR`。日志中间件不查询数据库、不执行权限判断；
日志失败不得改变响应、事务或 panic 语义。事件不得包含 query string、请求体、Cookie、
Authorization/session token、密码、数据库 URL、完整 NAS 路径或音频/封面内容；匿名请求
不填充 `actor_id`。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 部署参数错误 / 找不到提交 | 退出 2；执行或备份失败退出 1，不执行后续换版本 |
| 生产未运行 | backup / reset-password 退出 1；status 提示但退出 0 |
| 恢复前备份失败 / 恢复失败 | 前者不覆盖库；后者保持应用停止并打印安全备份的恢复命令 |
| session 缺少/未知 `role` | decoder 拒绝，不能默认为 admin |
| 请求体不是合法 JSON | `400 invalid_json`，响应里只有一个错误信封 |
| 未登录访问需要登录的接口 | `401 unauthorized`，message“需要登录”，与角色无关 |
| 封面：未登录（即使带匹配的 `If-None-Match`） | `401`，不是 304 |
| 封面：资源不存在、记录或存储键不安全（即使带 `If-None-Match`） | `404 not_found`，不是 304 |
| 封面：`If-None-Match` 命中当前资源 | `304`，同一个 `ETag`，不读文件（文件被删除后仍是 304） |
| `assets/` 下缺失的文件、`assets` 及其子目录 | `404 text/plain`，不回落到入口页，没有目录列表 |
| 未注册的 `/api`、`/api/...` 路径 | `404 not_found` JSON 错误信封，不回落到前端 `index.html` |
| 已注册的 `/api/` 路径使用未注册方法 | `405 method_not_allowed`，`Allow` 头列出该路径注册的方法（注册 `GET` 时含 `HEAD`） |
| 启动时数据库不可用（降级 handler） | 所有 `/api` 请求返回 `503 database_unavailable`；`/healthz`、`/readyz` 与前端页面照常 |
| 前端非 2xx 或 malformed JSON | `ApiRequestError` 安全回退并保留 request ID |

## 5. 正确、基准与错误案例

- 错误：把 `role` 缺失解释为 admin。
- 部署：正确是导出提交后构建且备份成功才换版本；错误是直接从脏工作区构建或备份失败仍继续。
  同一提交的两镜像 revision 匹配时可复用，仍须备份；停止用 `stop`，不用 `down -v`。

## 6. 必需测试

- 部署：逐脚本语法检查、生产模板 Compose 校验、三项版本一致性、导出树的 app/tools 构建；
  演练验证脏工作区隔离、备份失败不换版本、恢复核对/失败停机、乱序重启，以及开发卷不变。只打印命令不等于实际部署通过。

- 前端 decoder/transport 测试随 feature 边界组织（M1a 已建立）：每个 decoder 有样本与反例（缺字段、错类型、未知枚举、超界）、
  请求层覆盖内容类型、损坏 JSON、HTML 回落页、信封不合法、网络失败与取消；所有后端样本由契约测试逐个解码并往返比较。
- HTTP 中间件：隐式 200、显式状态、错误响应的状态/字节统计，路由模板、耗时、日志
  级别、请求 ID 回写、可选 actor_id 以及敏感字段脱敏；每个请求只能有一个完成事件。
- HTTP 缓存（F3）：`platform/httpserver` 的 `frontend_test.go`（`fstest.MapFS`：各类路径的缓存头、目录不列、缺失资源 404、HEAD）、
  `etag_test.go`（弱比较、列表、`*`、无法解析的值）、`response_test.go`（`WriteJSON` 与健康检查 `no-store`）；
  `cmd/roomusic/main_test.go` 对真实内嵌产物断言 `assets/` 文件名都带哈希段、缓存策略生效；
  `internal/app/artwork_cache_integration_test.go`（封面 200/304/401/404、删除文件后仍 304、标签不匹配返回新字节、
  不安全存储键、JSON 不可缓存）；`catalog/artwork` 的存储键与读取单元测试。真实二进制的 `curl -I` 冒烟记录在任务
  `10-02-classical-f3-backend-gaps` 的 `research/cache-headers-smoke.md`（只用合成数据）。
- 运行门禁：`npm run lint`、`npm run typecheck`、`npm run test`、
  `gofmt -l .`、`go test ./... -count=1`、`go vet ./...`、`go build ./...`、
  `go test -race ./... -count=1`、逐个脚本的 `bash -n`、三个 Compose 配置与镜像版本一致性校验、
  `./scripts/test-integration.sh` 和 `git diff --check`（按改动取最小集合）。

## 后续技术债

后续技术债已移到 [core0-tech-debt.md](./core0-tech-debt.md)（总览接近 24 KB 的上限；内容原样搬移）。

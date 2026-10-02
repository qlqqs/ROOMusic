# F3 技术设计

> 需求与验收见 [prd.md](prd.md)，执行顺序见 [implement.md](implement.md)。本文只写实现边界、合同和取舍。

## 1. 改动边界

| 需求 | 行为所在位置 | 改动文件（预期） |
| --- | --- | --- |
| R1 扫描记录 | 扫描读取归 `library`，传输层在 `library/httpapi`；页面归前端 `features/library` | `backend/internal/library/scans.go`、`library/httpapi/{handlers,scans}.go`、`platform/httpserver/pagination.go`（新）、`catalog/httpapi/handlers.go`（改用共享解析）；前端 `features/library/{api,hooks,components}`、`app/router.tsx`、`app/routes/library-page.tsx`、`shared/ui/pagination.tsx`（新） |
| R2 用户 ID | 响应形状在 `identity/httpapi`；查看者模型在前端 `features/auth` | `identity/httpapi/handlers.go`；前端 `features/auth/api/decoders.ts`、`session/session-controller.ts`、`features/members/components/{member-table,member-management}.tsx` |
| R4 错误映射 | `identity/httpapi` 与 `identity/guard.go` | `identity/httpapi/handlers.go`、`identity/guard.go`；前端 `features/auth/account-rules.ts` 与成员错误文案 |
| R5 HTTP 缓存 | 静态资源在 `platform/httpserver`，JSON 响应在 `httpserver.WriteJSON`，封面在 `catalog/httpapi` 与 `catalog/artwork` | `platform/httpserver/{frontend,response}.go`、`catalog/httpapi/handlers.go`、`catalog/artwork/artwork.go` |

不新增数据库迁移，不新增依赖，不改模块依赖规则（`internal/app/dependency_rules_test.go`）。

## 2. 接口合同

### 2.1 `GET /api/v1/scans`（新增）

- 鉴权：`guard.RequireAdmin`（401 `unauthorized` / 403 `forbidden`）。只读，不要求同源头。
- 参数：`page`（默认 1，≥1）、`page_size`（默认 20，1–100）；非法时 400 `invalid_pagination`，与 `/releases` 的错误码和文案一致。
- 响应 200：

  ```json
  {"items": [<Scan>], "pagination": {"page": 1, "page_size": 20, "total": 3}}
  ```

  `<Scan>` 与 `GET /scans/{id}` 相同（`id`、`scan_run_id`、`status`、`started_at`、`finished_at`、`cancel_requested_at`、`diagnostics`）。
- 排序：`started_at DESC, id DESC`；超出末页返回空 `items`，`total` 照常。总数与当前页是两次查询，不保证快照一致（与 catalog 列表相同）。
- 读取失败：503 `database_unavailable`，不回显 SQL。

### 2.2 身份响应

- `GET /auth/me` 200、`POST /auth/login` 200、`POST /setup/admin` 201 的响应体统一为 `{id, username, role}`（`id` 是小写 UUID）。
- 错误变化：

| 接口 | 条件 | 现状 | 改后 |
| --- | --- | --- | --- |
| `POST /users` | 用户名唯一约束冲突（SQLSTATE `23505`） | 409 `username_taken` | 不变 |
| `POST /users` | 其他插入失败 | 409 `username_taken` | 503 `database_unavailable`（“无法创建用户”） |
| `POST /setup/admin` | 唯一约束冲突（并发初始化写 `setup_state` 或同名） | 409 `setup_closed` | 不变 |
| `POST /setup/admin` | 读取初始化状态、其他插入或提交失败 | 409 `setup_closed` | 503 `database_unavailable` |
| `POST /setup/admin`、`POST /users` | 密码超过 72 字节 | 500 `internal_error` | 400 `invalid_input` |
| `PATCH /users/{id}`、`POST /users/{id}/sessions/revoke` | ID 不是 UUID | 503 | 400 `invalid_id`（“用户标识无效”），不访问数据库 |
| `POST /users/{id}/sessions/revoke` | 用户不存在 | 200 `{revoked:true}` | 404 `not_found`（“用户不存在”） |
| `GET /users` | 读行或遍历失败 | 静默跳过 | 503 `database_unavailable` |
| 任意需登录接口 | 未登录 | 401，message “需要管理员登录” | 401，message “需要登录” |

- `invalid_input` 文案：“用户名不能为空，密码需要 12 到 72 个字节”。规则仍按 UTF-8 字节（Go 的 `len`），只是加上 bcrypt 的上限。
- 唯一约束判断沿用 `library/httpapi/root_operations.go:17`–`:20` 的写法（`errors.As` 到 `*pgconn.PgError` 且 `Code == "23505"`），
  不新增共享 helper，也就不必改依赖规则。

### 2.3 缓存头

| 响应 | `Cache-Control` | 其他 |
| --- | --- | --- |
| `assets/` 下存在的文件 | `public, max-age=31536000, immutable` | 文件名带内容哈希（Vite 构建），内容变化即换名 |
| `/`、SPA 回落、`index.html` | `no-cache` | 不加 `ETag`（约 1 KB，验证收益小） |
| 其他根目录文件（`licenses/*.txt`） | `no-cache` | |
| 缺失的 `assets/` 资源、`assets/` 下的目录 | —（404） | 目录不再列出 |
| 其他目录（如 `/licenses/`） | `no-cache` | 回落到 `index.html`，由前端显示“页面不存在” |
| 所有 JSON（`httpserver.WriteJSON`，含错误信封） | `no-store` | |
| 封面 200 | `private, no-cache` | `ETag: "<resource_id>"`；资源 ID 是内容哈希加扩展名，天然是强校验值 |
| 封面 304 | `private, no-cache` | 同一 `ETag`；无响应体 |

封面处理顺序：鉴权（401）→ 按资源 ID 查 `release_artworks`（不存在或不安全 404）→ `If-None-Match` 与 `"<resource_id>"` 匹配
（含 `*` 与逗号分隔列表，按 RFC 9110 的弱比较）→ 304，不读文件；否则读文件返回 200。`catalog/artwork.Store` 拆出“查记录”和“读文件”
两步，`Load` 保留为两步的组合，现有调用方不变。

取舍：`private, no-cache` 仍允许浏览器存下图片（只是每次使用前要验证），不能追溯擦除已有缓存（ADR 0001 引用的 RFC 9111）；
`no-store` 每次都要整张重新下载，代价过高。所有登录用户本来就能读所有封面，验证主要防的是“退出后从缓存直接显示”。

## 3. 后端实现要点

- **分页解析**：`httpserver.ParsePagination(request, defaultPageSize, maxPageSize) (page, pageSize int, err error)` 搬自
  `catalog/httpapi.parsePagination`（含页偏移溢出检查）；catalog 以 `(50, 100)` 调用，行为不变（现有测试覆盖）；扫描记录以 `(20, 100)` 调用。
  错误响应仍由各 handler 写 `invalid_pagination`。
- **`library.Service.ListScans(ctx, page, pageSize) (ScanPage, error)`**：
  - `SELECT COUNT(*) FROM scan_runs`；
  - `SELECT scanSelectColumns FROM scan_runs ORDER BY started_at DESC, id DESC LIMIT $1 OFFSET $2`，复用 `scanRow`；
  - 一次分组查询取本页全部诊断计数：`WHERE scan_run_id = ANY($1::uuid[]) GROUP BY scan_run_id, kind`，每个扫描的种类上限沿用 100，
    `safeDiagnosticKind` 规范化；不逐条调用 `loadScanDiagnosticsSummary`。
  - 不加索引：每次扫描一行，规模小；若将来需要再随迁移评估。
- **身份响应**：`me` 用 `guard.Authenticate` 返回的 `User.ID`；`login` 用查询出的 `userID`；`setupAdmin` 用新建的 `userID`。
- **用户路由 ID**：`uuid.Valid` 在进入事务前校验；撤销会话先 `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1::uuid)`，不存在返回 404，
  再执行撤销（没有删除用户的接口，存在性检查与撤销之间不会出现用户消失）。
- **密码长度**：`setupAdmin` 与 `createUser` 共用一个“12–72 字节”的检查函数，在调用 bcrypt 之前拒绝；`HashPassword` 仍保留错误返回。

## 4. 前端实现要点

- **查看者**：`viewerDecoder` 增加 `id: d.uuid()`；`sameViewer` 改为比较 `id` 与 `role`；成员表与确认框以 `member.id === viewer.id` 判断“你”。
  `decoders.ts` 顶部“用户名是唯一标识”的注释改为现状。
- **账号规则**：`account-rules.ts` 增加 `MAX_PASSWORD_BYTES = 72`，提示改为“需要 12 到 72 个字节（英文字符占 1 个字节，中文字符占 3 个字节）”。
- **成员错误**：撤销会话收到 404 `not_found` 时，按停用/启用已有的处理方式提示“该成员已不存在”并刷新列表；`invalid_id` 不会由界面产生，
  归入通用失败文案。
- **扫描记录**：
  - `fetchScanPage`、`decodeScanPage`（`items` 上限 100，复用 `scanDecoder`）、`libraryKeys.scanHistory(generation, page)`、`useScanHistory(page)`
    （`placeholderData` 保留上一页，翻页不闪空）。
  - `ScanHistorySection` 用 `DataTable`：开始时间、状态（`ScanPhaseTag`）、结束时间（运行中显示“进行中”）、诊断数，末列“查看详情”链接；
    链接的可访问名称带开始时间。空状态：“还没有扫描记录。”
  - 刷新：启动扫描成功、扫描详情到达终态时，除现有失效外，再让扫描记录查询失效；扫描记录本身不轮询。
  - 页码：`/library` 的 `validateSearch` 接受 `scan_page`（≥2 的整数，第 1 页不写），不合法显式返回 `undefined`；总数非零而页码超出末页时，
    用替换历史的方式改到末页（与 state-management 规范一致）。
- **`shared/ui` 的 `Pagination`**：只负责结构与可访问语义——`<nav aria-label=…>`、“上一页/下一页”、“第 x / y 页”，当前页 `aria-current="page"`；
  链接由调用方传入的渲染函数生成（路由 `Link`），组件不依赖路由。M1b 的专辑列表复用它。只有一页时不渲染。

## 5. 测试与样本

- **Go 单元测试**：`httpserver` 的 `Frontend` 用 `fstest.MapFS` 覆盖缓存头、目录路径、缺失资源、回落；`WriteJSON` 的 `no-store`；
  `ParsePagination` 的默认值、上限、非法值与溢出；`catalog/artwork` 的两步读取与 `If-None-Match` 匹配规则。
- **PostgreSQL 集成测试**（`./scripts/test-integration.sh`）：
  - 扫描记录：多次扫描（直接写入 `scan_runs`，含相同 `started_at`）的排序、分页、`total`、超出末页、诊断计数与详情一致、401/403/400；
  - 身份：三处响应带 `id` 且与用户列表一致；`createUser`/`setupAdmin` 的唯一约束冲突与触发器注入的其他错误；非 UUID 400；撤销未知用户 404；
    `listUsers` 读行失败 503（测试库里去掉 `username` 的 `NOT NULL` 后写入空值，使 `Scan` 失败）；密码 11/12/72/73 字节的边界；401 文案；
  - 封面：200 带 `ETag` 与 `private, no-cache`；匹配的 `If-None-Match` 返回 304 且删掉文件后仍是 304（证明没读文件）；未登录 401；
    未知资源带 `If-None-Match` 仍 404。
- **接口样本**（`TestPostgreSQLAPIResponseSamples`）：重写 `auth/*` 与 `errors/unauthorized`；新增 `scans/list`、`scans/list-empty`、
  `errors/not-found-user`、`errors/invalid-id-user`（若与现有 `errors/invalid-id` 文案不同）。前端 `api-contract.test.ts` 同步登记。
- **Vitest**：查看者 decoder；“你”按 ID 判断（构造用户名相同而 ID 不同的替身数据）；账号规则 72 字节边界；扫描记录的加载、空、失败、
  翻页、URL 规范化与失效刷新；`Pagination` 组件。
- **Playwright**：`e2e/support/api-mock.ts` 增加扫描列表；`a11y.spec.ts` 与 `responsive.spec.ts` 加入“扫描记录有数据”的状态；
  键盘路径覆盖翻页与进入详情。

## 6. 兼容与回退

- 不做兼容（开发阶段）：前端 decoder 要求 `id`，旧后端不会与新前端搭配运行（同一二进制内嵌前端）。
- 没有迁移，每个步骤一个提交，回退即 `git revert` 对应提交；缓存头回退后，已按 `immutable` 缓存的带哈希资源不受影响（文件名随内容变化）。
- 风险：`immutable` 只能用于带哈希的文件。构建产物中 `assets/` 以外的文件（`index.html`、`licenses/`）一律 `no-cache`；
  测试断言 `assets/` 下的每个文件名都带哈希段，防止将来把不带哈希的文件放进 `assets/`。

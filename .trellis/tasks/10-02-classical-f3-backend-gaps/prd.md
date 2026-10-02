# F3：M1a 遗留的后端缺口

> 状态：in_progress（2026-10-02 创建；同日用户批准最终规划摘要，启动实施）。父任务：[Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。
> 来源：M1a 记入[运行合同技术债](../../spec/backend/core0-runtime-contracts.md)的后端缺口；用户 2026-10-02 要求规划其中五项：扫描历史列表、
> `/auth/me` 不返回用户 ID、成员写操作没有幂等、`createUser` 把任何插入失败都当成 409、静态资源没有缓存头。
> 跨前后端，按复杂任务管理：技术设计见 [design.md](design.md)，执行顺序与验证命令见 [implement.md](implement.md)。
> 先于 [M1b](../09-28-classical-m1b-album-browsing/prd.md) 实施：M1b 显示封面前需要的封面缓存头结论（ADR 0001 第 6 项）与分页组件在本任务落定。

## 目标与用户价值

- 管理员在扫描目录页能找回已结束的扫描，不必记住扫描详情的 URL。
- 成员页按用户 ID 识别“你”，不依赖用户名。
- 数据库故障不再伪装成“用户名已存在”或“初始化已完成”，格式不对的 ID 不再报“服务暂不可用”，过长的密码不再报内部错误。
- 页面资源能被浏览器长期缓存，发布新版本后又能立即生效；封面每次显示前都经过鉴权核对，代价只是一次 304。

## 已核实事实（2026-10-02）

**身份与成员**（`backend/internal/identity/httpapi/handlers.go`）

- `/auth/me` 只返回 `{username, role}`（`:133`–`:139`）；登录（`:119`）与初始化管理员（`:92`）的响应形状相同，但两处手里都有用户 ID，
  `guard.User` 也带 ID（`backend/internal/identity/guard.go:38`）。用户名有唯一约束（`backend/migrations/0002_core_slice.sql:3`）。
  前端因此以用户名为唯一标识（`frontend/src/features/auth/api/decoders.ts:6`）：成员表按用户名标出“你”
  （`frontend/src/features/members/components/member-table.tsx:29`），确认框按用户名判断是否为自己（`member-management.tsx:56`），
  会话核对按用户名与角色判断是否同一查看者（`frontend/src/features/auth/session/session-controller.ts:55`）。
- `createUser` 把任何插入失败都映射成 409 `username_taken`（`:183`–`:186`）。`setupAdmin` 同理：查询初始化状态、插入用户、写入初始化状态或提交
  失败，一律返回 409 `setup_closed`（`:74`–`:90`）。
- `updateUser`、`revokeUserSessions` 不校验路由 ID（`:206`、`:295`），格式不对时 `$1::uuid` 转换失败，返回 503 `database_unavailable`；
  撤销会话不检查用户是否存在，未知 ID 也返回 200。目录、发行版本与扫描接口对同类输入返回 400 `invalid_id`。
- `listUsers` 读取某行失败时静默跳过（`:156`–`:159`），也不检查遍历错误，列表会少人而不报错。
- 密码规则按字节（`len(password) < 12`），后端文案却写“12 个字符”（`:59`–`:60`、`:173`–`:174`）；前端提示已按字节说明
  （`frontend/src/features/auth/account-rules.ts:1`–`:15`）。密码哈希用 bcrypt（`guard.go:129`–`:135`），`golang.org/x/crypto` v0.55.0 对
  超过 72 字节的密码返回 `ErrPasswordTooLong`，初始化与新建成员因此返回 500 `internal_error`。
- 401 的 message 一律是“需要管理员登录”（`guard.go:85`），普通用户也收到这句；前端按错误码选文案规避。
- 成员写操作（创建、停用/启用、撤销会话）没有 `Idempotency-Key`：停用/启用设定绝对状态，重复执行结果相同；撤销会话重复执行只是再撤销一次；
  创建重复执行得到 409，不会建出第二个账号。M1a 前端对结果不明的成员操作不重试，刷新列表让管理员核对
  （`frontend/src/features/members/hooks/use-member-commands.ts:24`、`:57`）。[ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md)
  把成员管理是否纳入受治理操作留给 M3（`:249`、`:306`）。

**扫描**

- 没有扫描列表接口（`backend/internal/library/httpapi/handlers.go:38`–`:42`）。`scan_runs` 有 ID、状态、开始/结束时间与取消字段
  （`backend/migrations/0002_core_slice.sql:26`–`:32`、`0009_scan_cancellation.sql`），只有运行中扫描的部分索引（`0009:4`–`:6`）；
  每次扫描一行，表的规模很小。
- `GET /scans/active`、`GET /scans/{id}` 已登录即可读，诊断仅管理员（`backend/internal/library/httpapi/scans.go:38`、`:60`、`:105`）。
- 前端扫描目录页（仅管理员）只显示活动扫描；结束的扫描只能通过 `/library/scans/$scanId` 找回。
- 分页参数的解析只在 catalog 里（`backend/internal/catalog/httpapi/handlers.go` 的 `parsePagination`，默认 50、上限 100，错误码 `invalid_pagination`）。

**HTTP 缓存**

- `httpserver.Frontend` 不设任何缓存头（`backend/internal/platform/httpserver/frontend.go:11`–`:29`）。内嵌文件的修改时间为零，
  `http.FileServer` 因此既不发 `Last-Modified` 也不发 `ETag`，浏览器无从协商，每次重新加载都要整份重新下载 JS、CSS 与字体
  （`assets/` 下 114 个文件，其中字体合计约 6.0 MB，浏览器按 `unicode-range` 只取用到的子集）。构建产物的文件名带内容哈希。
- `/assets/`、`/licenses/` 这类目录路径在内嵌文件系统里存在，会落到 `http.FileServer` 的目录列表。
- API 的 JSON 响应不设 `Cache-Control`（`backend/internal/platform/httpserver/response.go:36`–`:40`）；前端请求一律带 `cache: "no-store"`。
- 封面响应是 `Cache-Control: private, max-age=86400`（`backend/internal/catalog/httpapi/handlers.go:230`）。资源 ID 是内容 SHA-256 加扩展名
  （`backend/internal/catalog/artwork/artwork.go:76`–`:77`），同一 ID 的字节永不改变。所有登录用户都能读所有封面；现状的风险是退出登录后，
  封面一天内仍可从同一浏览器的 HTTP 缓存直接读到。[ADR 0001](../../../docs/architecture/adr/0001-frontend-foundations.md) 建议受保护响应评估
  `private, no-store` 或每次重新验证（`:138`–`:142`），后端缓存头调整须独立立项（第 8 节第 6 项，`:205`–`:206`）。

## 已裁决

| 编号 | 事项 | 选择 | 依据 |
| --- | --- | --- | --- |
| D1 | 成员写操作的幂等 | 本轮不做，延到 M3 随受治理操作一起决定；前端保持“结果不明时刷新列表核对”，技术债条目注明此裁决 | qlqq，2026-10-02：“延到 M3（推荐）” |
| D2 | 扫描记录接口的权限与形状 | 仅管理员（唯一的使用方是管理员页面）；`page` 默认 1，`page_size` 默认 20、上限 100；按开始时间倒序、ID 倒序；条目与 `GET /scans/{id}` 的 DTO 相同 | 推荐，随规划摘要批准 |
| D3 | 用户 ID 的返回位置 | `/auth/me`、登录与初始化管理员三处响应都加 `id`，前端的查看者统一带 ID | 推荐：三处响应同形，前端用同一个 decoder |
| D4 | 密码长度规则 | 规则不变（按 UTF-8 字节，至少 12），后端文案改为按字节；另加 bcrypt 的上限 72 字节，超过时返回 400 `invalid_input`，前端同步提示 | 推荐：前端提示已按字节；改成按字符计会让后端与前端都改规则 |
| D5 | 封面缓存策略 | `Cache-Control: private, no-cache` 加 `ETag: "<资源 ID>"`：每次显示前带凭据重新验证，未变返回 304、不读文件；退出登录后重新验证得到 401，旧封面不再显示 | 推荐：ADR 0001 建议的“每次重新验证”；资源 ID 本身就是内容哈希，验证成本只有一次查库 |
| D6 | 静态资源与 API 的缓存策略 | `assets/` 下的文件 `public, max-age=31536000, immutable`；`index.html`（含回落）与其他根目录文件 `no-cache`；所有 JSON 响应 `no-store`；目录路径不再列目录 | 推荐：文件名带内容哈希才能永久缓存，入口页必须每次验证才能拿到新版本 |

## 需求与验收

| 编号 | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | 扫描记录 | • `GET /api/v1/scans` 仅管理员：未登录 401、普通用户 403；`page`、`page_size` 非法返回 400 `invalid_pagination`<br>• 按开始时间倒序（同时间按 ID 倒序）分页，`total` 为扫描总数；超出末页返回空 `items`；每条的诊断计数与 `GET /scans/{id}` 一致<br>• 扫描目录页新增“扫描记录”：开始时间、状态、结束时间、诊断数，每行可进入扫描详情；空状态有说明；页码在 URL（`scan_page`），超出末页时替换为末页<br>• 启动扫描或扫描结束后记录自动刷新；窄屏按行堆叠，无横向溢出，通过 axe 检查 |
| R2 | 用户 ID | • `/auth/me`、登录、初始化管理员的响应含 `id`（UUID）<br>• 前端成员表与确认框按 ID 判断“你”，会话核对按 ID 与角色判断是否同一查看者；有测试证明判断依据是 ID 而不是用户名 |
| R3 | 成员写操作幂等（D1） | 不改代码。运行合同技术债与 ADR 0003 决策记录写明“延到 M3” |
| R4 | 成员接口与认证的错误映射 | • 新建成员只有用户名唯一约束冲突返回 409 `username_taken`，其他数据库错误返回 503 `database_unavailable`；初始化管理员只有唯一约束冲突返回 409 `setup_closed`，其他错误返回 503<br>• `PATCH /users/{id}`、`POST /users/{id}/sessions/revoke` 对非 UUID 返回 400 `invalid_id`，撤销未知用户的会话返回 404 `not_found`<br>• `listUsers` 读行或遍历失败返回 503，不再静默少人<br>• 密码少于 12 字节或超过 72 字节返回 400 `invalid_input`，文案按字节说明；前端在提交前给出同样的提示<br>• 401 的 message 与角色无关<br>• 每项都有 PostgreSQL 集成测试；数据库错误用测试库里的触发器注入（与 `auth_root_operations_integration_test.go:310` 的做法相同），不依赖真实故障 |
| R5 | HTTP 缓存 | • `assets/` 下的文件带 `Cache-Control: public, max-age=31536000, immutable`；`/`、SPA 回落页与 `licenses/` 文件带 `no-cache`；缺失的 `assets/` 资源仍是 404<br>• `/assets/`、`/licenses/` 等目录路径不返回目录列表（`assets/` 下 404，其余回落到 `index.html`）<br>• 所有 JSON 响应（含错误信封）带 `Cache-Control: no-store`<br>• 封面 200 响应带 `Cache-Control: private, no-cache` 与 `ETag`；`If-None-Match` 匹配时返回 304 且不读文件；未登录时即使 ETag 匹配也返回 401；不存在的资源即使带 ETag 也返回 404 |
| R6 | 文档同步 | 运行合同（扫描记录接口、错误矩阵、缓存策略、技术债）、Catalog REST 合同（封面缓存）、ADR 0001（第 6 项与决策记录）、ADR 0003（D1 记录）、Core 1 路线（F3 行与矩阵）、前端规范（分页组件、`scan_page`）、父任务表与 M1b 的 PRD 写明新合同；改动文档的相对链接逐个检查 |

## 范围外

- 成员写操作的幂等键（D1，延到 M3）；成员操作的持久审计记录。
- 成员列表分页、创建管理员或修改角色（`editor` 是 M3 候选）、允许根目录的查询接口：仍记在运行合同技术债。
- 用户名首尾空白的规范化（目前只拒绝全空白，保存原值）：不是本轮的缺口，不改。
- 扫描进度、暂停/续跑、定时与监听扫描（另立增强任务）。
- 封面缩略图（M1b 的风险项，见 M1b 的 PRD）；专辑列表与详情（M1b）。
- 浏览器端的缓存验证：Playwright 跑在 `vite preview` 上，不经过 Go 服务；缓存头由 Go 测试验证。

## 最终规划决策记录

- 路线：人工批准最终规划摘要。
- 选择者：用户（qlqq）。
- 批准：2026-10-02，用户在最终规划摘要后回复“批准这两份规划”（F3 与 M1b）。D1 由用户明确选择“延到 M3”，D2–D6 随摘要批准。
- 理由：先补齐 M1a 遗留的后端缺口并定下封面缓存结论，M1b 才能在稳定的合同上显示封面与分页。
- 状态：本任务获准启动并实施；M1b 在本任务归档后另行启动。

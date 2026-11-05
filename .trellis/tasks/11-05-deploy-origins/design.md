# 设计：来源清单与按来源设置会话 Cookie

需求见 [prd.md](./prd.md)，父任务决定 D1、D5 见[部署上线](../11-05-deploy-launch/prd.md)。

## 1. 边界与改动文件

只改后端的配置与身份守卫，不改前端代码，不改任何接口的 JSON 形状和错误码。

| 文件 | 改动 |
| --- | --- |
| `backend/internal/platform/weborigin/weborigin.go`（新） | 来源的解析与规范化，配置和守卫共用 |
| `backend/internal/platform/weborigin/weborigin_test.go`（新） | 规范化表驱动测试 |
| `backend/internal/platform/config/config.go` | 读 `ROOMUSIC_ALLOWED_ORIGINS`；改生产校验；增加冲突校验 |
| `backend/internal/platform/config/config_test.go` | 新增配置用例；`setRequired` 清掉三个相关变量 |
| `backend/internal/identity/guard.go` | 来源策略分“清单模式”和“旧模式”；`SetSessionCookie` / `ClearSessionCookie` 改签名 |
| `backend/internal/identity/guard_test.go`（新） | 两种模式的来源与 Cookie 单元测试 |
| `backend/internal/identity/httpapi/handlers.go` | 登录、退出的两处 Cookie 调用多传 `request` |
| `backend/internal/app/app.go` | 按配置选守卫构造函数 |
| `backend/internal/app/identity_api_integration_test.go` | 清单模式的 PostgreSQL 集成用例 |
| `scripts/prod.sh` | 把 `SECURE_COOKIES=true` 检查换成“清单非空”检查 |
| `.env.example`、`README.md` | 配置说明 |
| `.trellis/spec/backend/identity-contracts.md`、`core0-runtime-contracts.md` | 合同（见第 7 节） |

`NewGuard(database, secureCookies, publicURL)` 的签名和行为不变，现有约 15 处测试调用不用动。

## 2. 配置合同

### 2.1 `ROOMUSIC_ALLOWED_ORIGINS`

- 语法：用逗号分隔的来源列表，每一项前后的空白去掉；去掉空白后是空的项（例如末尾多一个逗号）忽略。
- 每一项必须是 `scheme://host[:port]`，用 `weborigin.ParseConfigured` 解析：
  - `scheme` 只能是 `http` 或 `https`，不区分大小写，规范化为小写；
  - 必须有主机；主机规范化为小写；IPv6 字面量必须带方括号（`http://[fd00::1]:8090`）；
  - 主机只能是 ASCII（国际化域名写 punycode，跟浏览器发出的 `Origin` 一致）；不允许 `*` 通配；
  - 端口可选，必须是 1–65535 的十进制数；与协议默认端口相同时去掉（`http:80`、`https:443`）；
  - 不允许用户信息、查询、片段；路径只允许为空或单独一个 `/`（规范化时去掉，照顾从地址栏复制来的末尾斜杠），其他路径都报错。
- 规范化结果是 `scheme://host` 或 `scheme://host:port`；重复项去重，保持首次出现的顺序。
- 出错时 `config.Load` 返回错误，进程按现有 `platform.config.invalid` 事件退出。错误信息指出配置项和出错的那一项，例如：
  `ROOMUSIC_ALLOWED_ORIGINS item "https://music.example.com/app" must be http(s)://host[:port] without path, query or credentials`。
  （来源不是秘密，可以原样写进错误；现有配置错误都是英文，保持一致。）

### 2.2 两种模式与 `SECURE_COOKIES` / `PUBLIC_URL` 的唯一解释

用“清单是否非空”区分两种模式，同一时刻只有一种生效：

| 情况 | 结果 |
| --- | --- |
| 清单非空（**清单模式**） | 来源判断和 Cookie `Secure` 只看清单与请求的 `Origin`；后端不读 `ROOMUSIC_SECURE_COOKIES`，也不用 `ROOMUSIC_PUBLIC_URL` 做任何判断 |
| 清单模式下 `ROOMUSIC_SECURE_COOKIES` 非空（不论值） | 拒绝启动：`ROOMUSIC_SECURE_COOKIES cannot be combined with ROOMUSIC_ALLOWED_ORIGINS; the Secure flag follows each origin's scheme` |
| 清单模式下设置了 `ROOMUSIC_PUBLIC_URL` | 仍按现有规则校验格式；规范化后必须等于清单中的某一项，否则拒绝启动（`ROOMUSIC_PUBLIC_URL must be one of ROOMUSIC_ALLOWED_ORIGINS`）。它在后端只是被核对，不参与判断；它继续给 Vite 的 `allowedHosts` 用 |
| 清单为空且非生产（**旧模式**） | 与现在逐项一致：协议由 `SECURE_COOKIES` 决定，设了 `PUBLIC_URL` 比较它的主机，否则比较 `request.Host`；Cookie `Secure` 取 `SECURE_COOKIES` |
| 清单为空且 `ROOMUSIC_ENV=production` | 拒绝启动：`ROOMUSIC_ALLOWED_ORIGINS must list at least one origin in production` |

- 删除“生产必须 `SECURE_COOKIES=true`”。生产只剩“必须有清单”，于是生产必然是清单模式；`SECURE_COOKIES` 在生产里出现就报错。
- 清单只含 http 来源时也能启动（A3）。
- `config.Server` 增加 `AllowedOrigins []string`（已规范化）；`SecureCookies`、`PublicURL` 字段保留，只在旧模式下有意义。
- 为什么冲突就报错而不是“清单优先、静默忽略”：运维者照旧模板写了 `SECURE_COOKIES=true` 再加 http 局域网来源，静默忽略会让他以为
  Cookie 一直带 `Secure`。报错只多一次改配置，换来不存在两种读法。

### 2.3 环境变量示例

```dotenv
# 生产（仓库根目录 .env.prod，见部署打包设计第 16 节）
ROOMUSIC_ALLOWED_ORIGINS=http://192.168.50.206:8090,https://music.example.com
# 不设置 ROOMUSIC_SECURE_COOKIES 与 ROOMUSIC_PUBLIC_URL
```

开发 `.env.dev` 不改：不设清单就是旧模式。若想在开发中试清单模式，清单里要写 Vite 的来源 `http://localhost:5173`，并删掉
`ROOMUSIC_SECURE_COOKIES`。

## 3. 守卫合同

### 3.1 结构

```go
// guard.go
type Guard struct {
	database       *sql.DB
	allowedOrigins map[string]bool // 非 nil 即清单模式；键是 weborigin 规范化结果
	secureCookies  bool            // 仅旧模式
	publicURL      string          // 仅旧模式
}

// NewGuard 返回旧模式守卫（行为与现在一致），供开发、测试与未配置清单的环境使用。
func NewGuard(database *sql.DB, secureCookies bool, publicURL string) *Guard

// NewGuardWithAllowedOrigins 返回清单模式守卫；origins 必须已由 weborigin 规范化（config.Load 保证），为空时 panic（装配错误）。
func NewGuardWithAllowedOrigins(database *sql.DB, origins []string) *Guard
```

`app.New` 中：`len(settings.AllowedOrigins) > 0` 用 `NewGuardWithAllowedOrigins`，否则 `NewGuard(...)`（旧调用不变）。

### 3.2 `RequireSameOrigin`（签名不变）

```text
origin := request.Header.Get("Origin")
origin == ""                         → 403 origin_required（两种模式相同）
清单模式：
  normalized, err := weborigin.ParseRequest(origin)
  err != nil 或 !allowedOrigins[normalized] → 403 origin_forbidden
  否则通过
旧模式：现有代码原样保留（协议、PUBLIC_URL 主机、request.Host）
```

- `ParseRequest` 比 `ParseConfigured` 严格一点：路径必须为空（浏览器发出的 `Origin` 不带斜杠，带 `/` 就拒绝）；`null`、
  `file://`、没有主机、带用户信息或查询的一律解析失败。规范化规则（大小写、默认端口）与配置相同，所以
  `HTTPS://Music.Example.com:443` 与清单里的 `https://music.example.com` 相等。
- 清单模式**不看** `request.Host`，**不读** `X-Forwarded-Host`、`X-Forwarded-Proto`、`Forwarded`（D5）。外部反代改写 Host 不影响判断。
- 错误信封、状态码、message 都不变（O6）。

### 3.3 会话 Cookie

签名改为带 `request`：

```go
func (guard *Guard) SetSessionCookie(w http.ResponseWriter, r *http.Request, token string)
func (guard *Guard) ClearSessionCookie(w http.ResponseWriter, r *http.Request)
```

`Secure` 由内部的 `guard.cookieSecure(r)` 决定：

- 旧模式：返回 `guard.secureCookies`（与现在相同）。
- 清单模式：`ParseRequest(r.Header.Get("Origin"))` 成功、在清单里且协议是 `https` 时为 `true`，其余为 `false`。
  调用方都已先通过 `RequireSameOrigin`，所以实际只有“https 来源 → true / http 来源 → false”两种结果；万一将来有调用方
  没先校验，未通过校验的来源得到 `false`，但这种请求本来就不应走到写 Cookie，第 6 节的单元测试锁定“先校验”的调用顺序。

其他属性（`HttpOnly`、`SameSite=Lax`、`Path=/`、24 小时 `Expires`、清除时 `MaxAge=-1`）不变（O3）。

为什么传 `request` 而不是让 `RequireSameOrigin` 返回已校验的来源：`RequireSameOrigin` 有几十个调用点分布在各能力的 handler 里，
改返回值会牵动全部；而写 Cookie 只有登录和退出两处。重新解析一次 `Origin` 的成本可以忽略，规则与校验用的是同一个函数。

### 3.4 其他调用点

- `handlers.go` 的 `login`、`logout` 各改一行，多传 `request`。
- 其他能力的 handler 只调用 `RequireSameOrigin`，签名不变，无需改动。
- `cmd/roomusic/reset_password_test.go` 与各包测试用 `NewGuard(..., false, "")` 或 `config.Server{PublicURL: ...}`，都是旧模式，结论不变。
- `scripts/playback-e2e.sh` 设 `PUBLIC_URL` + `SECURE_COOKIES=false`、不设清单，仍是旧模式，不用改。
- `frontend/e2e/real/` 经同一个 playback-e2e 服务运行，不用改。

## 4. 兼容性

- 不配清单的开发、测试环境逐项不变（O5）：`./scripts/dev.sh`、Vite 代理、集成测试、Playwright 都不改配置。
- 生产环境旧配置（只有 `SECURE_COOKIES=true`）升级后会拒绝启动并指明要配 `ROOMUSIC_ALLOWED_ORIGINS`。目前没有在运行的生产实例
  （父任务事实），不需要迁移步骤。
- `scripts/prod.sh` 的检查改为 `[[ -n "${ROOMUSIC_ALLOWED_ORIGINS:-}" ]]`，提示“生产环境必须设置 ROOMUSIC_ALLOWED_ORIGINS”；
  其余格式校验交给后端，不在 shell 里重复。
- 浏览器侧：同一主机名上 http 与 https 的 Cookie 互不覆盖的问题不会出现，因为局域网用 IP、公网用域名，主机不同、Cookie 各自独立。

## 5. 考虑过但不选

| 方案 | 不选的原因 |
| --- | --- |
| 清单优先，静默忽略 `SECURE_COOKIES` / `PUBLIC_URL` | 同一份配置有两种读法，违背 O4 |
| 直接删除 `SECURE_COOKIES` / `PUBLIC_URL` | 开发经公网反代访问 Vite 的现有流程依赖它们（O5 要求不改） |
| 按 `X-Forwarded-Proto` 决定 `Secure` | D5 明确不信任转发头；局域网直连的人也能伪造这个头 |
| Cookie 始终不带 `Secure` | 公网 https 上失去 Secure 保护，不必要地降级 |
| 允许通配（`https://*.example.com`） | 家用单实例只有两三个固定入口，通配增加误放行面 |
| `RequireSameOrigin` 返回已校验来源 | 牵动所有能力的 handler（见 3.3） |
| 把规范化函数放在 `config` 或 `identity` 里 | 两边都要用；放 `identity` 会让 `config` 反向依赖业务包，放 `config` 会让守卫依赖进程配置包。独立的 `platform/weborigin` 有两个真实使用方、语义相同 |

## 6. 测试清单（最小相关集）

单元测试（不需要数据库）：

- `weborigin_test.go`：表驱动——大小写、默认端口 80/443 去掉、非默认端口保留、IPv6 方括号、配置允许末尾 `/` 而请求不允许、
  路径/查询/片段/用户信息、`null`、`file://`、`ftp://`、空主机、端口 0 与 65536、非 ASCII 主机、`*` 通配。
- `config_test.go`：`setRequired` 增加清空 `ROOMUSIC_ALLOWED_ORIGINS`、`ROOMUSIC_SECURE_COOKIES`、`ROOMUSIC_PUBLIC_URL`；
  用例：清单解析与去重；生产不配清单报错且信息含配置项名；生产只配 http 来源能启动；生产不设 `SECURE_COOKIES` 能启动（原来报错）；
  清单 + `SECURE_COOKIES` 非空报错；清单 + `PUBLIC_URL` 不在清单中报错、在清单中通过；格式错误的项报错且信息含该项；
  不配清单的开发环境 `SecureCookies` / `PublicURL` 读取结果与现在相同。
- `guard_test.go`（`httptest`，`NewGuard*(nil, ...)`）：
  - 清单 `http://192.168.50.206:8080,https://music.example.com`：两个来源通过；`http://music.example.com`、`https://192.168.50.206:8080`、
    其他主机、带路径、带用户信息、`null` 得到 `origin_forbidden`，缺少得到 `origin_required`（A2）；
    `Host: evil.example` 加允许的来源仍通过，`Host` 与来源相同但来源不在清单里仍拒绝；带 `X-Forwarded-Host/Proto` 不改变结论。
  - `SetSessionCookie` / `ClearSessionCookie`：https 来源带 `Secure`，http 来源不带；`HttpOnly`、`SameSite=Lax`、`Path=/` 不变（A1 的单元部分）。
  - 旧模式：现有三条路径（`secureCookies` 决定协议、`publicURL` 主机、`request.Host`）各一条正反例，锁定 O5。

PostgreSQL 集成测试（`internal/app/identity_api_integration_test.go`，新增一个用清单配置装配应用的用例）：

- 用 `config.Server{AllowedOrigins: [...]}` 装配；从两个来源各登录一次，检查 `Set-Cookie` 的 `Secure`；各做一次写操作
  （`POST /auth/password` 或退出），成功；退出的 `Set-Cookie` 同样按来源。
- 不被允许的来源登录：403 且 `sessions` 表没有新增行（A2 的“不写库”）。

运行命令（实现与测试 agent 按需执行，不扩大到全量）：

```bash
(cd backend && test -z "$(gofmt -l ./internal/platform ./internal/identity ./internal/app)")
go -C backend test ./internal/platform/weborigin ./internal/platform/config ./internal/identity/... -count=1
go -C backend vet ./internal/platform/... ./internal/identity/... ./internal/app
# 集成：在 roomusic-test 隔离库上只跑身份相关用例
docker compose -f compose.test.yaml -p roomusic-test --env-file /dev/null up -d --wait postgres
ROOMUSIC_TEST_DATABASE_URL=postgres://roomusic_test:roomusic_test@127.0.0.1:55432/roomusic_test?sslmode=disable \
  go -C backend test ./internal/app -run 'TestPostgreSQL.*(Identity|Login|Origin|Password|Session)' -count=1
docker compose -f compose.test.yaml -p roomusic-test --env-file /dev/null down -v
```

接口 JSON 样本不应变化（A5）：不运行 `ROOMUSIC_UPDATE_API_SAMPLES=1`；样本比对用例随上面的集成测试运行即可。
A4 的 `./scripts/dev.sh` 登录可用由测试 agent 手动启动一次确认（不配清单）。

## 7. 需要更新的 spec 段落

- `identity-contracts.md`：第 1 节范围加“来源校验与会话 Cookie”；第 3 节新增“来源与会话 Cookie（2026-11-05）”小节，写入 2.1、2.2 的表、
  3.2 的判定顺序与 3.3 的 `Secure` 规则；第 4 节错误矩阵加“清单模式下来源不在清单 / 缺少来源”两行；第 6 节必需测试加第 6 节的用例。
- `core0-runtime-contracts.md`：“请求与响应”第一条补一句“来源规则见身份合同”；“环境与资产”中开发 Vite 那一条（现第 130–132 行）改为
  “后端在未配置 `ROOMUSIC_ALLOWED_ORIGINS` 时按 `ROOMUSIC_PUBLIC_URL`、Origin scheme/host 和 `ROOMUSIC_SECURE_COOKIES` 校验；配置清单后只按清单”；
  `scripts/prod.sh` 那一条补“要求 `ROOMUSIC_ALLOWED_ORIGINS`”。
- `.env.example`：在 `ROOMUSIC_SECURE_COOKIES` / `ROOMUSIC_PUBLIC_URL` 上方说明它们只用于开发；新增注释掉的
  `# ROOMUSIC_ALLOWED_ORIGINS=` 并写一句“生产必填，见 .env.prod.example”（该模板由部署打包任务新增）。

## 8. 回滚

单个提交即可回退：恢复 `guard.go`、`config.go`、`handlers.go`、`app.go` 和 `prod.sh`，删除 `weborigin` 包与新测试。没有迁移、没有数据变化。
已经按新规则发出的 Cookie 回滚后照常可用（名字、值、路径不变）。

## 9. 主机一致模式（D8，2026-11-05 增补）

需求见 [prd.md](./prd.md) 的 O8、A6；决定见父任务 D8。演练实测（packaging `research/drill/README.md`）：真实反代转发的写请求带
`Origin: https://music.qlqqs.com:8888`、`Host: music.qlqqs.com:8888`，TCP 对端 `192.168.50.1`；局域网直连是
`Origin: http://192.168.50.206:18090`、`Host: 192.168.50.206:18090`。两者 Origin 与 Host 都一致。

### 9.1 模式选择

`config.Load` 一次算出模式，写进 `config.Server.OriginMode`；`app.New` 只按这个字段选守卫，不再自己看 `Environment` 或清单长度。

```go
// config.go
type OriginMode string

const (
	OriginModeLegacy    OriginMode = "legacy"     // 非生产且清单为空：与现在逐项一致
	OriginModeAllowList OriginMode = "allow-list" // 清单非空
	OriginModeSameHost  OriginMode = "same-host"  // 生产且清单为空
)
```

完整的组合表（“非空”指去掉首尾空白后非空；`ROOMUSIC_PUBLIC_URL` 的格式校验照旧最先进行，格式错误一律拒绝启动，下表不再重复）：

| # | 清单 | `ROOMUSIC_ENV` | `SECURE_COOKIES` | `PUBLIC_URL` | 结果 |
| --- | --- | --- | --- | --- | --- |
| 1 | 非空 | 任意 | 非空（任何值） | 任意 | 拒绝启动（现有）：`ROOMUSIC_SECURE_COOKIES cannot be combined with ROOMUSIC_ALLOWED_ORIGINS; the Secure flag follows each origin's scheme` |
| 2 | 非空 | 任意 | 空 | 空 | 清单模式 |
| 3 | 非空 | 任意 | 空 | 规范化后在清单内 | 清单模式；`PUBLIC_URL` 只被核对，不参与判断（现有） |
| 4 | 非空 | 任意 | 空 | 不在清单内 | 拒绝启动（现有）：`ROOMUSIC_PUBLIC_URL must be one of ROOMUSIC_ALLOWED_ORIGINS` |
| 5 | 空 | `production` | 非空（任何值） | 任意 | 拒绝启动（新）：`ROOMUSIC_SECURE_COOKIES is not used in production; the Secure flag follows each origin's scheme` |
| 6 | 空 | `production` | 空 | 非空 | 拒绝启动（新）：`ROOMUSIC_PUBLIC_URL is not used in production; list origins in ROOMUSIC_ALLOWED_ORIGINS to restrict access` |
| 7 | 空 | `production` | 空 | 空 | **主机一致模式** |
| 8 | 空 | 非 `production` | 任意 | 任意 | 旧模式，与现在逐项一致（O5） |

- 检查顺序固定：`PUBLIC_URL` 格式 → 清单各项格式 → 清单非空时第 1、4 行 → 清单为空且生产时第 5、6 行。同时命中第 5、6 行时报第 5 行。
- 删除错误 `ROOMUSIC_ALLOWED_ORIGINS must list at least one origin in production`，不再出现。
- 第 5、6 行为什么拒绝而不是忽略：与第 1 行同理，照旧模板写了 `SECURE_COOKIES=true` 的人会以为 Cookie 一直带 `Secure`；
  写了 `PUBLIC_URL` 的人会以为只放行这一个地址。生产 Compose 本来就不把这两个变量传进容器，报错只影响绕过 Compose 手工运行的情况。
  第 6 行不选“把 `PUBLIC_URL` 当成只有一项的清单”：那样同一个变量在开发和生产有两种读法，想限制地址就写清单。
- `SecureCookies`、`PublicURL` 字段保留，只在旧模式有意义；`AllowedOrigins` 只在清单模式非空。
- 开发 `.env.dev`（`ROOMUSIC_ENV=dev`）、`scripts/playback-e2e.sh`、集成测试与 Playwright 都落在第 8 行，不改配置。
- 任何模式都不读 `X-Forwarded-Host`、`X-Forwarded-Proto`、`X-Forwarded-For`、`Forwarded`（D5）。

### 9.2 守卫结构

```go
type Guard struct {
	database       *sql.DB
	allowedOrigins map[string]bool // 非 nil 即清单模式。
	sameHost       bool            // 主机一致模式。
	secureCookies  bool            // 仅旧模式。
	publicURL      string          // 仅旧模式。
}

// NewGuardWithSameHost 返回主机一致模式守卫：Origin 与请求 Host 规范化后一致就放行，协议不限。
func NewGuardWithSameHost(database *sql.DB) *Guard
```

`app.New` 按 `settings.OriginMode` 三选一：`allow-list` → `NewGuardWithAllowedOrigins`，`same-host` → `NewGuardWithSameHost`，
`legacy` → `NewGuard(database, settings.SecureCookies, settings.PublicURL)`；其他值 panic（装配错误）。`NewGuard`、
`NewGuardWithAllowedOrigins` 签名与行为不变。

清单模式与主机一致模式共用一个内部函数，`RequireSameOrigin` 和 `cookieSecure` 都调它，保证校验与 Cookie 用同一结论：

```go
// trustedOrigin 返回已规范化且被当前模式接受的来源；仅用于清单模式与主机一致模式。
func (guard *Guard) trustedOrigin(request *http.Request) (string, bool)
```

### 9.3 比较规则

`RequireSameOrigin` 判定顺序（签名不变）：

```text
origin := request.Header.Get("Origin")
origin == ""                                   → 403 origin_required（三种模式相同）
主机一致模式：
  normalized, err := weborigin.ParseRequest(origin)
  err != nil                                   → 403 origin_forbidden
  scheme := normalized 的协议（http 或 https）
  host, err := weborigin.FromHost(scheme, request.Host)
  err != nil 或 host != normalized             → 403 origin_forbidden
  否则通过
清单模式、旧模式：不变
```

- **Origin**：用现有 `weborigin.ParseRequest` 规范化。`Origin: null`、`file://`、带路径（含末尾 `/`）、查询、片段、用户信息、
  非 ASCII 主机、`*` 都解析失败 → `origin_forbidden`。
- **Host 取值**：只用 Go 解析好的 `request.Host`（HTTP/1.1 是 Host 头，请求行是 absolute-form 时是其中的主机；HTTP/2 是 `:authority`），
  不读任何其他头。
- **Host 规范化**：新增 `weborigin.FromHost(scheme, host string) (string, error)`：
  - `scheme` 必须是 `http` 或 `https`（调用方传 Origin 规范化后的协议），否则报错；
  - `host` 为空 → 报错；含空白、控制字符或 `/ \ ? # @ %` 中任一字符 → 报错（防止拼成 URL 后被解析成路径、用户信息或转义）；
  - 其余交给与 `ParseRequest` 相同的解析：主机转小写；IPv6 必须带方括号、不允许 zone，不带方括号的 IPv6 报错；
    非 ASCII、`*` 报错；端口必须是 1–65535 的十进制数，`host:` 空端口报错；
  - **默认端口按 Origin 的协议去掉**：`https` 下 `:443`、`http` 下 `:80` 去掉；所以 `Origin: https://music.example.com` 与
    `Host: music.example.com:443` 一致，`Origin: http://music.example.com` 与 `Host: music.example.com:443` 不一致；
  - 返回 `scheme://host[:port]`，与规范化后的 Origin 逐字节比较。
  - 不做别的等价：末尾点（`music.example.com.`）不去掉，IPv6 不改写成规范文本（浏览器在 Origin 与 Host 里写的是同一种文本）。
- **缺少 Host**：HTTP/1.1 缺 Host 时 Go 服务器直接回 400，到不了守卫；HTTP/1.0 等情况 `request.Host` 为空，`FromHost` 报错 →
  `origin_forbidden`（不是 `origin_required`，后者只表示缺 `Origin`）。
- **协议不限**：只要求 Origin 的主机和端口与 Host 一致；`http://192.168.50.206:8090` 与 `https://music.example.com:8888` 都能过，
  `https://192.168.50.206:8090` 配 `Host: 192.168.50.206:8090` 也能过（这个端口上不存在 TLS，浏览器发不出这样的请求，放行也无害）。
- 错误信封、状态码、message 不变（O6）；前端已有的提示“请用正确的网址打开页面后重试。”不改。
- 只读请求（GET）在任何模式都不做来源校验，与现在相同。

### 9.4 会话 Cookie 的 Secure

`cookieSecure(r)`：旧模式返回 `secureCookies`；清单模式与主机一致模式都是 `trustedOrigin(r)` 成功且协议为 `https` 时 `true`，否则 `false`。
登录、退出先过 `RequireSameOrigin`，所以实际只有“https 来源带 Secure / http 来源不带”两种结果。其他属性不变（O3）。

### 9.5 拒绝时的诊断日志（主会话 2026-11-05 裁决：做）

反代改写 Host 时，用户只看到“请用正确的网址打开页面后重试。”，排查要靠日志。推荐在清单模式与主机一致模式拒绝 `origin_forbidden` 时
写一条 WARN `identity.origin.rejected`，字段 `mode`、`origin`、`host`（各截到 256 字节，结构化日志不需另行转义），请求 ID 照常带上。
旧模式不加，保持 O5 逐项一致。来源和主机不是秘密；日志轮转已有上限（packaging Compose 的 `max-size: 10m`、`max-file: 5`）。
不做的代价：用户反馈问题时只能靠抓包或 curl 复现。

### 9.6 风险

**DNS 重绑定。** 攻击者让自己的域名（如 `evil.example`）解析到 `192.168.50.206`，再诱导家里的人用浏览器打开
`http://evil.example:8090/`。浏览器发出的写请求带 `Origin: http://evil.example:8090`、`Host: evil.example:8090`，主机一致，通过来源校验，
页面脚本还能读到响应。但浏览器只按主机带 Cookie，本站在 `192.168.50.206` 或公网域名下的 `roomusic_session` 不会发给 `evil.example`，
所以只能访问不需要登录的写接口：

| 接口 | 能做什么 | 评估 |
| --- | --- | --- |
| `POST /api/v1/setup/admin` | 实例尚未初始化时创建管理员；初始化后返回 `409 setup_closed` | 唯一有实际危害的一项，只存在于“启动后、初始化前”的窗口。配了公网反代时，这个窗口本来就对整个公网开放，重绑定没有多出能力。可接受，前提是部署说明写明：启动后立即初始化管理员，初始化完成前不开放公网反代 |
| `POST /api/v1/auth/login` | 猜密码 | 受登录限速保护，按“用户名 + TCP 对端”计数，对端是受害设备的局域网地址；最多把这台设备的登录暂停一段时间。公网反代入口本来就允许任何人这样试。可接受 |
| `POST /api/v1/auth/logout` | 无 Cookie 时不写库，只回一个清除 `evil.example` 下 Cookie 的响应 | 无影响 |
| 其他写接口 | 先过来源校验，再因没有会话返回 `401 unauthorized` | 无影响 |

不需要登录的只读接口（`GET /api/v1/setup/status`、`/readyz`、前端静态文件）在任何模式下都不校验来源，重绑定本来就能读，本设计不改变。
旧模式不设 `PUBLIC_URL` 时也是比较 Host，开发环境一直是这个暴露面。想彻底排除时配置清单（第 2、3 行）。

**反代改写 Host。** 例如 nginx 默认 `proxy_set_header Host $proxy_host`（改成上游地址），或用了 `$host`（会丢掉 `:8888` 端口）。
现象：经这个反代打开的页面能正常浏览，但登录、收藏等所有写操作都返回 `403 origin_forbidden`，页面提示“请用正确的网址打开页面后重试。”；
局域网直连不受影响；若做了 9.5，日志里 `identity.origin.rejected` 的 `host` 是被改写后的值。部署说明写法：

- 反代必须原样转发浏览器的 `Host`（含端口）和 `Origin`；nginx 写 `proxy_set_header Host $http_host;`，Caddy 的 `reverse_proxy` 默认保留，无需设置。
- 做不到时，在 `.env.prod` 里写清单（含反代对外的完整网址），清单模式不看 Host。
- 不要靠 `X-Forwarded-Host`：应用不读它。

### 9.7 合同与文档改动

| 文件 | 改动 |
| --- | --- |
| `backend/internal/platform/weborigin/weborigin.go` | 新增 `FromHost` |
| `backend/internal/platform/config/config.go` | `OriginMode` 类型与字段；按 9.1 表计算模式；删旧的生产缺清单错误，加第 5、6 行错误 |
| `backend/internal/identity/guard.go` | `sameHost` 字段、`NewGuardWithSameHost`、`trustedOrigin`；`RequireSameOrigin` 与 `cookieSecure` 走 9.3、9.4；9.5 的日志 |
| `backend/internal/app/app.go` | 按 `OriginMode` 三选一 |
| `.trellis/spec/backend/identity-contracts.md` | “来源与会话 Cookie”：模式表换成 9.1 的表；加 `NewGuardWithSameHost`、`FromHost` 签名与 9.3 判定顺序、9.4 规则；错误矩阵加“主机一致模式下来源解析失败、Host 缺失或不一致 → `403 origin_forbidden`”；必需测试加 9.8；反例一节改为“任何模式都不按转发头放行；清单模式不看 Host，主机一致模式只比较 `request.Host`” |
| `.trellis/spec/backend/core0-runtime-contracts.md` | 第 134–136 行补“生产未配置清单时按主机一致校验”；第 147 行“生产要求非空 `ROOMUSIC_ALLOWED_ORIGINS`”改为“生产清单可选，不配时按主机一致模式，反代须保留 Host” |
| `README.md` “来源配置” | 改写为三种模式各一段：生产默认不配，地址栏网址与请求主机一致就放行、Cookie 跟随协议、反代保留 Host；需要只放行固定网址时配清单（原有格式说明保留在这里）；开发旧模式一段不变 |
| `.env.example` | 第 21 行改为“生产可选，见 .env.prod.example” |
| `.env.prod.example` | 清单一行改为注释掉的示例，注释写：可选；不填时用浏览器打开的网址访问即可，反代须保留 Host；只想放行固定网址，或反代改写了 Host 时再填 |
| `deploy/production/compose.yaml` | `ROOMUSIC_ALLOWED_ORIGINS: "${ROOMUSIC_ALLOWED_ORIGINS:-}"`；空字符串在后端等于未配置 |
| `deploy/production/README.md` | 配置表该行改为“可选”；第 55–56 行改为 Cookie 跟随来源协议、反代须保留浏览器的 Host 与 Origin（附 nginx 一行写法），改写 Host 时的现象与两种解决办法；“日后增加公网入口”改为：反代保留 Host 即可直接用，不需要改配置；加一句“启动后先初始化管理员，再开放公网反代” |
| `.trellis/tasks/11-05-deploy-packaging/design.md` | §8.2 注释、§8.4“加公网域名”一行、§9 表中该变量、§10.2 第 1 步改为“不填清单”；加一句指向本节 |

- CI 的 `docker compose -f deploy/production/compose.yaml --env-file .env.prod.example config --quiet`：变量改为 `:-` 后缺省为空串，
  模板里注释掉清单也能通过；`production.sh` 内的 `compose config --quiet` 同理。`scripts/production.sh` 没有来源检查，不用改。
- 前端不改；接口 JSON 形状与错误码不变。

### 9.8 测试

单元测试：

- `weborigin_test.go`：`FromHost` 表驱动——大小写；`https`+`:443`、`http`+`:80` 去掉，`http`+`:443`、`https`+`:80` 保留；非默认端口保留；
  `[fd00::1]:8090` 通过、`fd00::1`、带 zone 失败；空串、`a/b`、`a?b`、`a#b`、`u@h`、`a\b`、`a b`、`a%41`、控制字符、非 ASCII、`*`、
  `h:`、`h:0`、`h:65536`、`h:x` 失败；协议不是 http/https 失败。
- `config_test.go`：9.1 表每行一例，断言 `OriginMode` 或错误信息（含配置项名）；第 5、6 行同时命中报第 5 行；第 5 行对 `SECURE_COOKIES=false` 同样报错；
  生产且三项都空 → `same-host`；旧生产缺清单的错误不再出现；非生产不配清单的 `SecureCookies`、`PublicURL` 读取结果与现在相同。
- `guard_test.go`（`NewGuardWithSameHost(nil)`，`httptest`）：
  - 通过：`http://192.168.50.206:18090` + `Host: 192.168.50.206:18090`；`https://music.qlqqs.com:8888` + `Host: music.qlqqs.com:8888`；
    Host 大写；`https://music.example.com` + `Host: music.example.com:443`；IPv6 带方括号。
  - `origin_forbidden`：Host 不同主机；`https://music.qlqqs.com:8888` + `Host: music.qlqqs.com`（丢端口）；`http://x` + `Host: x:443`；
    空 Host；`Origin: null`；Origin 带路径或用户信息；Host 不带方括号的 IPv6。
  - `origin_required`：缺 Origin。
  - 转发头：Host 与 Origin 不一致但 `X-Forwarded-Host`/`Forwarded` 等于 Origin 的主机 → 仍拒绝；一致时加任意 `X-Forwarded-Proto`/`Forwarded` → 仍通过。
  - Cookie：https 来源 `Secure=true`、http 来源 `false`；`HttpOnly`、`SameSite=Lax`、`Path=/` 不变；清除 Cookie 同样规则。
  - 旧模式、清单模式的现有用例不改，结论不变。
- 9.5：拒绝时有一条 `identity.origin.rejected`，含 `mode`、`origin`、`host`；通过与旧模式不写。

PostgreSQL 集成测试（`identity_api_integration_test.go`，新增 `newSameHostIntegrationTestApplication`，用 `OriginMode: same-host` 装配）：

- 两个来源 `http://192.168.50.206:18090`（`request.Host = "192.168.50.206:18090"`）与 `https://music.qlqqs.com:8888`
  （`request.Host = "music.qlqqs.com:8888"`）各自登录、做一次写操作（`POST /auth/password` 或收藏）、退出，都成功；
  登录与退出的 `Set-Cookie` 前者无 `Secure`、后者有。
- Host 不一致（Origin `https://music.qlqqs.com:8888`、Host `10.0.0.1:8080`）登录：`403 origin_forbidden`，`sessions` 不增加、没有 `Set-Cookie`。
- 同一不一致请求加 `X-Forwarded-Host: music.qlqqs.com:8888`、`X-Forwarded-Proto: https`、`Forwarded: host=music.qlqqs.com:8888;proto=https`：结论不变。
- 已有清单模式与旧模式的集成用例照常运行。

命令沿用第 6 节，集成测试的 `-run` 加上新用例名（`TestPostgreSQLSameHost.*`）。

### 9.9 演练

本次不重跑演练，正式启动时按部署说明核对。

### 9.10 回滚

单个提交可回退，回到“生产必须配清单”。回退后不配清单的生产会拒绝启动，需在 `.env.prod` 补清单再 `make prod`。没有迁移、没有数据变化，
已发出的 Cookie 照常可用。

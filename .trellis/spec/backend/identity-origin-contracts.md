# 来源与会话 Cookie 合同

## 1. 范围与触发

涉及 `platform/weborigin`、`platform/config` 的来源配置、`identity.Guard`、`app.New` 或生产反代时读取本合同。
本专题由[身份与成员合同](./identity-contracts.md)拆出，避免单文件超过 24,000 字节；其他身份接口与错误信封不变。
依据为 [origins design 第 9 节](../../tasks/11-05-deploy-origins/design.md#9-主机一致模式d82026-11-05-增补)。

## 2. 接口签名

- `weborigin.ParseConfigured(origin string) (string, error)` 用于配置，`ParseRequest(origin string) (string, error)` 用于请求。
- `weborigin.FromHost(scheme, host string) (string, error)` 按 Origin 协议规范化请求 Host。
- `config.OriginMode string`：`OriginModeLegacy = "legacy"`、`OriginModeAllowList = "allow-list"`、`OriginModeSameHost = "same-host"`。
  `config.Load() (Server, error)` 一次算出 `Server.OriginMode`，`AllowedOrigins []string` 保存规范化后的清单。
- `NewGuard(database *sql.DB, secureCookies bool, publicURL string) *Guard` 保留旧行为。
- `NewGuardWithAllowedOrigins(database *sql.DB, origins []string) *Guard` 要求已规范化的非空清单，空清单 panic。
- `NewGuardWithSameHost(database *sql.DB) *Guard` 构造主机一致模式；`Guard.sameHost bool` 与非 nil 的 `allowedOrigins` 区分两种新模式。
- `(*Guard).WithLogger(logger *slog.Logger) *Guard` 仅在装配时调用，注入应用 logger；未注入时使用 `slog.Default()`。
- `(*Guard).trustedOrigin(request *http.Request) (string, bool)` 返回规范化来源与可信结论，仅用于清单、主机一致模式。
- `(*Guard).RequireSameOrigin(w http.ResponseWriter, r *http.Request) bool` 签名不变。
- `(*Guard).SetSessionCookie(w http.ResponseWriter, r *http.Request, token string)` 与 `ClearSessionCookie(w http.ResponseWriter, r *http.Request)`
  必须在 `RequireSameOrigin` 通过后调用。

`app.New` 仅按 `settings.OriginMode` 三选一，不再按环境或清单长度猜测；`legacy` 与零值 `""` 都按旧模式装配，其他未知值 panic。
零值兼容用于满足 O5，让直接构造 `config.Server{}` 的现有旧模式测试夹具不必修改；`config.Load` 总会显式写出模式，生产不会落到零值。

## 3. 配置与请求合同

### 配置格式

| `ROOMUSIC_ALLOWED_ORIGINS` 格式 | 规则 |
| --- | --- |
| 列表 | 逗号分隔，去掉每项首尾空白、忽略空项；规范化后去重并保持首次出现顺序 |
| 协议与主机 | 仅 http/https，必须有主机，两者都规范化为小写；主机仅 ASCII，国际化域名写 punycode，不允许 `*` 通配 |
| IPv6 | 字面量必须带方括号，如 `http://[fd00::1]:8090` |
| 端口 | 可省略；显式端口为 1–65535 的十进制数，http 的 80 与 https 的 443 去掉，其他端口保留 |
| 路径与附加信息 | 不允许用户信息、查询、片段；配置路径仅允许空或单独 `/`（去掉），请求路径必须为空 |
| 错误 | 拒绝启动，错误指出 `ROOMUSIC_ALLOWED_ORIGINS` 和出错项；进程沿用 `platform.config.invalid` 事件 |

`ROOMUSIC_PUBLIC_URL` 的旧格式检查仍不允许路径（包括末尾 `/`）；清单的末尾斜杠宽容规则不改变旧配置合同。
来源配置格式错误继续沿用 `platform.config.invalid` 事件，不引入新响应结构。

### 三种模式的唯一解释

表内“非空”指去掉首尾空白后非空；清单忽略空项后决定是否为空。

| # | 清单 | `ROOMUSIC_ENV` | `SECURE_COOKIES` | `PUBLIC_URL` | 结果 |
| --- | --- | --- | --- | --- | --- |
| 1 | 非空 | 任意 | 非空（任何值） | 任意 | 拒绝启动：`ROOMUSIC_SECURE_COOKIES cannot be combined with ROOMUSIC_ALLOWED_ORIGINS; the Secure flag follows each origin's scheme` |
| 2 | 非空 | 任意 | 空 | 空 | 清单模式 |
| 3 | 非空 | 任意 | 空 | 规范化后在清单内 | 清单模式；`PUBLIC_URL` 只被核对，不参与判断 |
| 4 | 非空 | 任意 | 空 | 不在清单内 | 拒绝启动：`ROOMUSIC_PUBLIC_URL must be one of ROOMUSIC_ALLOWED_ORIGINS` |
| 5 | 空 | `production` | 非空（任何值） | 任意 | 拒绝启动：`ROOMUSIC_SECURE_COOKIES is not used in production; the Secure flag follows each origin's scheme` |
| 6 | 空 | `production` | 空 | 非空 | 拒绝启动：`ROOMUSIC_PUBLIC_URL is not used in production; list origins in ROOMUSIC_ALLOWED_ORIGINS to restrict access` |
| 7 | 空 | `production` | 空 | 空 | **主机一致模式** |
| 8 | 空 | 非 `production` | 任意 | 任意 | 旧模式，与现在逐项一致（O5） |

顺序固定：`PUBLIC_URL` 原有格式 → 清单各项格式 → 清单非空时第 1、4 行 → 生产无清单时第 5、6 行；
第 5、6 行同时命中报第 5 行。不再报“生产必须配清单”，纯 http 清单仍可启动。
`SecureCookies`、`PublicURL` 在后端仅旧模式参与请求判断；清单内的 `PUBLIC_URL` 仅供 Vite `allowedHosts` 使用。
旧模式的 `SecureCookies` 仍只在环境变量原始值恰为 `true` 时开启，不额外去空白或转换大小写。

### 来源与 Host 判定

三种模式都先检查 `Origin`：空字符串返回 `403 origin_required`。只读 GET 不做来源校验。

- 清单模式：`ParseRequest` 失败或规范化结果不在清单即拒绝；不看请求 Host。
- 主机一致模式：先 `ParseRequest(Origin)`；失败即拒绝。取规范化来源的 http/https 协议，调用 `FromHost(scheme, request.Host)`；
  失败或结果与规范化 Origin 不逐字节一致即拒绝；一致即通过，不限制协议。
- 旧模式：协议仍由 `secureCookies` 决定；设 `publicURL` 则比较其主机，否则比较请求 Host；不改变大小写、端口等既有比较行为。
- 任何模式都不读取 `X-Forwarded-Host`、`X-Forwarded-Proto`、`X-Forwarded-For` 或 `Forwarded`。

`ParseRequest` 拒绝 `null`、file/ftp、空主机、路径（含单独 `/`）、查询、片段、用户信息、非 ASCII、通配等非法来源。
`FromHost` 只接受小写 `http` / `https` 协议；Host 为空或含空白、控制字符、`/`、`\`、`?`、`#`、`@`、`%` 即报错。
其他规则复用 `ParseRequest`：主机转小写；IPv6 必须带方括号且无 zone；禁止非 ASCII、通配；端口须为 1–65535 十进制，空端口非法。
默认端口按 Origin 协议去掉：http 的 80、https 的 443；交叉端口与非默认端口保留。
不去主机末尾点、不重写 IPv6 文本。返回 `scheme://host[:port]`。
只取 Go 的 `request.Host`，包括 absolute-form 请求行的主机或 HTTP/2 `:authority`；不自行读取另一个 Host 头。
HTTP/1.1 缺 Host 一般由 Go 直接返回 400；已到守卫的空 Host（如 HTTP/1.0）返回 `origin_forbidden`，不是 `origin_required`。

### Cookie 与拒绝日志

`cookieSecure(r)` 在旧模式返回全局 `secureCookies`；清单、主机一致模式都调用 `trustedOrigin(r)`，仅可信 https 来源返回 true，其他为 false。
登录和退出均先校验来源，拒绝不得写会话或 Cookie。Cookie 名 `roomusic_session`、`HttpOnly`、`SameSite=Lax`、`Path=/` 不变，
登录 `Expires` 为 24 小时，退出 `MaxAge=-1`。没有 CORS、Cookie 改名或转发头信任机制。
清单、主机一致模式返回 `origin_forbidden` 时恰好写一条 WARN `identity.origin.rejected`，含 `module=identity`、经过中间件处理的
`request_id` 与 `mode` / `origin` / `host`（三者各截到 256 字节，再清理不完整或非法 UTF-8）；使用结构化字段，不拼接原始输入。
通过、缺 Origin 与旧模式不写此事件；Cookie 计算本身不记录重复事件。

生产默认不填清单，反代须保留浏览器 Host（含端口）与 Origin；nginx 用 `proxy_set_header Host $http_host;`，不要用丢端口的 `$host`。
Host 被改写时页面能浏览但写请求返回 403；修正反代，或配置明确清单（清单模式不看 Host）。
主机一致模式不防同主机 DNS 重绑定，部署必须先初始化管理员再开放公网反代；需要限制固定入口时用清单。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 写请求缺少 Origin（三种模式） | `403 origin_required`，message“请求缺少来源校验” |
| 清单模式来源非法或不在清单 | `403 origin_forbidden`，message“请求来源不受信任” |
| 主机一致模式来源非法、Host 缺失/非法/不一致 | 同上 |
| 旧模式协议或既有主机比较失败 | 同上，不新增日志 |
| 配置非法或冲突 | 启动失败，错误指出配置项，按第 3 节表与顺序 |

所有来源拒绝都不写库、不写 Cookie；错误信封与错误码不变。

## 5. 正确、基准与错误案例

- 正确：生产不填清单，`http://192.168.50.206:18090` 与 `https://music.qlqqs.com:8888` 分别携带对应 Host 均通过，Cookie 跟随协议。
- 正确：清单同时含局域网 http 与公网 https，反代改写 Host 也不影响清单匹配。
- 基准：非生产无清单仍按旧协议开关、`PUBLIC_URL` 主机或请求 Host 工作，Vite 无须改配置。
- 错误：生产无清单仍设置 `SECURE_COOKIES=false` 或 `PUBLIC_URL`，必须拒绝，不能静默忽略。

## 6. 必需测试

- `weborigin`：大小写；http:80 / https:443 去掉，http:443 / https:80 保留；非默认端口；IPv6 方括号通过，裸 IPv6 与 zone 拒绝；
  空值、`a/b`、`a?b`、`a#b`、`u@h`、`a\b`、空白、`a%41`、控制字符、非 ASCII、`*`、`h:`、`h:0`、`h:65536`、`h:x`、非法协议失败；
  保留原有配置/请求解析测试（含配置接受而请求拒绝末尾 `/`）。
- `config`：模式表每行一例，断言模式或配置项错误；第 5、6 行同时命中报第 5 行，`SECURE_COOKIES=false` 也冲突；
  空/全空项清单进入 same-host，旧生产缺清单错误不再出现；清单规范化去重及错误项信息、PUBLIC_URL 格式优先、旧模式字段读取不变。
- `guard`：双来源、Host 大写、默认端口、IPv6 通过；主机不同、丢非默认端口、交叉默认端口、空 Host、null、路径、用户信息、裸 IPv6 拒绝；
  缺 Origin 返回 `origin_required`；伪造转发头不能放行，匹配请求加任意转发头仍通过；旧模式与清单既有结论不变。
- Cookie：登录/清除在可信 https 时 Secure=true，可信 http 时 false；拒绝或非法来源不产生 true；其他属性与有效期不变。
- 日志：新两模式拒绝时一条 WARN，字段与 request_id 正确、输入截到 256 字节；通过、缺 Origin、旧模式与 Cookie 计算不写。
- PostgreSQL：显式 same-host 装配，双来源分别登录、写操作、退出成功并核对 Cookie；Host 错配登录（含伪造转发头）403、
  sessions 不增加且无 Set-Cookie；保留清单与旧模式集成用例，隔离库运行，不重写接口 JSON 样本。

## 7. 错误与正确示例

错误：用 `X-Forwarded-Host` 修复 Host 错配，或把非空的未知 `OriginMode` 静默回退到旧模式。
正确：配置加载时确定唯一模式；清单模式只查清单，主机一致模式只规范化比较 `request.Host`，任何模式都不按转发头放行。
登录/退出先调用 `RequireSameOrigin(w, r)`，失败立即返回；成功后再创建会话、调用 `SetSessionCookie(w, r, token)` 或 `ClearSessionCookie(w, r)`。
校验与 Cookie 共用 `trustedOrigin`，避免两处策略漂移。

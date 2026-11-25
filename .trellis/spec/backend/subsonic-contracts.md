# Subsonic 兼容接口合同

`backend/internal/subsonic` 让 Symfonium、Feishin 等 Subsonic/OpenSubsonic 客户端用 ROOMusic 的账号连接。本文记录骨架（2026-11-25，迁移 0081）：协议外壳、鉴权、登录密码副本、实例开关与网页端设置接口。浏览、播放、收藏等接口由后续子任务追加到本文。产品裁决见任务 `.trellis/tasks/11-25-opensubsonic-api/`。

## 1. 范围与触发

改动以下任一项时先读本文：`/rest/` 路由与分发、响应外壳与错误码、客户端鉴权、密码副本的写入与删除时机、`identity.PasswordObserver`、实例开关、`/api/v1/me/subsonic*` 与 `/api/v1/admin/subsonic`、`subsonic_endpoint` 日志字段与诊断脱敏。

## 2. 包与签名

| 包 | 职责 | 允许依赖 |
| --- | --- | --- |
| `subsonic/protocol` | 请求解析（`.view` 后缀、GET 与 POST 表单、`f`）、外壳、错误码、XML/JSON 双标签编码 | 标准库，可用 `net/http` |
| `subsonic/auth` | 实例开关、客户端凭据校验、成功校验缓存、密码副本写入 | `identity`、`platform/secret`、`subsonic/protocol` |
| `subsonic/httpapi` | `/rest/` 分发与各接口处理器、网页端设置接口 | `identity`、`platform/httpserver`、`subsonic/auth`、`subsonic/protocol` |
| `platform/secret` | 实例密钥与 AES-GCM 加解密，从 `steward/secret` 原样移来，密钥文件仍是 `<数据目录>/steward/instance.key` | 无 |

```go
// identity：在已锁定账号的事务里接收已核对或即将生效的密码。
type PasswordObserver interface {
    ObservePassword(ctx context.Context, tx *sql.Tx, userID, password string) error
}
func CheckLogin(ctx, db, throttle, username, source, dummyInput string, verify func(User, string) (bool, error)) (User, string, *Attempt, error)
func ConfirmPassword(ctx, db, throttle, userID string, tokenHash []byte, password string, onVerified func(context.Context, *sql.Tx, string, string) error) error

// subsonic/auth
func New(db *sql.DB, secrets *secret.Store, throttle *identity.LoginThrottle, logger *slog.Logger) *Service
func (s *Service) Authenticate(ctx, request protocol.Request, source string) (identity.User, error)
func (s *Service) ObservePassword(ctx, tx, userID, password string) error // 实现 PasswordObserver
func (s *Service) Confirm(ctx, user identity.User, tokenHash []byte, password string) error
func (s *Service) SetEnabled(ctx, enabled bool) error
func (s *Service) Forget(userID string) // 会话撤销后清这个用户的校验缓存
```

`app` 用 `identityapi.New(...).WithPasswordObserver(subsonic)` 注入，`identity` 不导入 `subsonic`。

## 3. 数据（迁移 0081）

```sql
subsonic_settings(id smallint PRIMARY KEY CHECK (id = 1), subsonic_enabled boolean NOT NULL DEFAULT true) -- 单行
subsonic_credentials(user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, sealed bytea NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())
TRIGGER invalidate_subsonic_password AFTER UPDATE OF password_hash ON users -- 哈希变化即删该用户副本
```

- 新装与升级的实例都默认开启。
- `sealed` 用实例密钥加密，用途 `"subsonic-password"`，附加数据绑定 userID。解密失败（密钥换了、数据被篡改）视同没有副本。
- 触发器覆盖只连数据库的命令行 `reset-password`：副本被删，用户之后要重新登录网页或在账号页确认一次密码。

### 副本写入时机

| 入口 | 行为 |
| --- | --- |
| 网页登录成功、本人改密（成员与管理员）、管理员建用户、管理员重置密码 | 同一事务里先改密码哈希（触发器删旧副本），再经 `ObservePassword` 写新副本 |
| 实例关闭 | 不写 |
| 加密失败（实例密钥读不出或写不进） | 跳过写入，记 Warn `event=subsonic.credential.seal_failed`、`module=subsonic`、`user_id`、`error_kind=secret_unavailable`；入口本身照常成功 |
| 数据库错误 | 原样返回，整个事务回滚 |
| `PUT /api/v1/admin/subsonic {enabled:false}` | 同一事务里删除全部副本 |
| 命令行重置、管家工具改密 | 没有观察者，只由触发器删旧副本 |

Subsonic 的 `p=` 登录成功**不**补写副本：只有网页入口知道该不该保存。

## 4. 协议外壳与接口

- 路由：mux 只注册 `/rest/`，按去掉 `.view` 后缀的接口名分发；GET 与 POST 表单（体积上限 64 KiB）都接受。
- 外壳：`status`、`version="1.16.1"`、`type="roomusic"`、`serverVersion=<构建版本>`、`openSubsonic=true`。
  - XML 命名空间 `http://subsonic.org/restapi`；
  - JSON 顶层 `{"subsonic-response":{...}}`；
  - `f` 为空或 `xml`、`json`，其他值（含 `jsonp`）返回错误 0。
- 已实现接口：

| 接口 | 响应 |
| --- | --- |
| `ping` | 空外壳 |
| `getLicense` | `license{valid=true}` |
| `getOpenSubsonicExtensions` | 只列已实现的扩展，骨架阶段只有 `formPost` v1；后续子任务实现 `songLyrics`、`transcodeOffset` 时再追加 |
| `getMusicFolders` | 一个虚拟文件夹 `id=1`、`name="ROOMusic"` |
| `getUser` | 只能查本人；`streamRole`、`downloadRole`、`jukeboxRole` 为 true，`folder=[1]` |

## 5. 鉴权

1. 实例关闭 → 40（先于参数检查，不泄露原因）。
2. 缺 `u`、`v`、`c` 任一，或既没有 `p` 也没有完整的 `t`+`s` → 10。
3. `identity.CheckLogin`：先查来源限流，再按用户名查未停用的账号，再查账号限流；与网页登录共用同一个 `LoginThrottle` 和计数。找不到、停用、限流都返回 40，与密码错误不可区分。
4. `p`：有 `enc:` 前缀时按 hex 解码（解码失败按密码错误处理），再按 bcrypt 校验。
5. `t`+`s`：解密副本，计算 `md5(密码+s)`，与 hex 解码后的 `t` 常量时间比较；没有副本或解密失败返回 40，计入失败次数。
6. 成功校验缓存（只用于 `p`）：按用户存 `sha256(当前密码哈希)` 与 `sha256(哈希 + "\x00" + 密码)`，5 分钟过期，每分钟清扫一次。改密后哈希变化，缓存自然失效；会话撤销时 `Forget`，关闭实例时清空。每个请求仍会查一次账号，停用立即生效。
7. 成功后 `httpserver.SetActor` 记 `actor_id`，用户放进请求上下文。不建会话、不写 cookie，`/rest` 不做同源检查。

## 6. 网页端接口

守卫与严格 JSON 解码同其他 `/me`、`/admin` 接口（未登录 401，成员访问 admin 403，写请求同源检查）。

| 方法与路径 | 输入 | 成功 | 失败 |
| --- | --- | --- | --- |
| GET `/api/v1/me/subsonic` | 无 | `{enabled, ready, username}`，`ready` 表示副本存在且能解密；关闭时 `ready=false` | 503 |
| POST `/api/v1/me/subsonic/confirm-password` | `{password}` | 204 | 403 `current_password_incorrect`；429 `too_many_attempts` 带 Retry-After；409 `subsonic_disabled`；400 `invalid_input`；加密失败 503 `database_unavailable` |
| GET `/api/v1/admin/subsonic` | 无 | `{enabled}` | 503 |
| PUT `/api/v1/admin/subsonic` | `{enabled}` | `{enabled}`；关闭时同事务删除全部副本 | 400 `invalid_input`、503 |

确认密码用 403 而不是 401：前端请求层把 401 当作会话结束。它与成员自助改密的当前密码核对共用限流与计数。样本在 `app/testdata/api-samples/subsonic/`，前端契约测试 `frontend/src/test/api-contract.test.ts` 逐份登记。

## 7. 错误矩阵（`/rest`）

| 条件 | HTTP | `error.code` |
| --- | --- | --- |
| 实例关闭（任何接口，含未知接口） | 200 | 40 |
| 缺少凭据参数 | 200 | 10 |
| 用户不存在、停用、密码错、令牌错、没有副本、限流 | 200 | 40 |
| `getUser` 缺 `username` / 查别人 | 200 | 10 / 50 |
| 未知接口 | 404 | 0 |
| 不支持的方法、`f` 值，表单解析失败 | 200 | 0 |
| 数据库等内部故障 | 200 | 0（`Service unavailable`） |

## 8. 日志与隐私

- 请求完成日志的 `route_template` 固定为 `/rest/`；接口名记在 `subsonic_endpoint`，只取已注册的接口名，其他一律记 `unknown`，不回显客户端输入。
- 查询串不进日志（中间件原有行为）。`subsonic` 包内的日志不写 `u`、`p`、`t`、`s` 的值。
- 诊断导出：`subsonic_endpoint` 列为安全字段，`/rest/` 列为安全路由；凭据正则覆盖 `u=`、`p=`、`t=`、`s=`。

## 9. 正确与错误的做法

- **错误**：让副本写入失败中断网页登录。这会让副本这个附带功能拖垮核心入口，实例密钥一出问题，所有人都登录不了网页。
  **正确**：只有显式的确认密码入口在加密失败时报 503，其余入口记 Warn 后跳过（第 3 节）。
- **错误**：在 Subsonic 的 `p=` 登录成功后补写副本。
  **正确**：副本只由网页入口写，管理员关闭后 `p=` 也写不进去。
- **错误**：改密后靠清缓存来让旧密码失效。
  **正确**：缓存绑定当前密码哈希，哈希一变就不会命中；`Forget` 只是额外的清理。

## 10. 测试

| 用例 | 断言 |
| --- | --- |
| `TestSubsonicProtocolResponseEncoding` | XML/JSON 外壳、命名空间、错误外壳、各接口字段名与类型 |
| `TestSubsonicPasswordCacheExpiryAndBinding` | 5 分钟过期、绑定哈希与候选密码、`Forget`/清空后不命中 |
| `TestPostgreSQLSubsonicAuthenticationAndPayloads` | `p=`、`enc:`、`t+s` 对 `ping.view` 成功，POST 表单，五个接口载荷 |
| `TestPostgreSQLSubsonicProtocolErrors` | 第 7 节错误矩阵 |
| `TestPostgreSQLSubsonicSharesWebLoginThrottle` | Subsonic 失败触发的限流对网页登录生效，反之亦然 |
| `TestPostgreSQLSubsonicExistingMembers` | 开启前已有的用户：`p=` 可用且不补写副本，`t+s` 40；确认或重新登录后可用 |
| `TestPostgreSQLSubsonicDisableClearsCredentials` | 关闭后副本表为空、`/rest` 全 40；再开启仍需重新登录或确认 |
| `TestPostgreSQLSubsonicPasswordRotation` | 改密、重置、只改数据库哈希后旧密码三种方式都失败（含缓存期内），新密码 `t+s` 立即可用 |
| `TestPostgreSQLSubsonicDiagnosticsHideCredentials` | 请求日志与诊断导出不含 `p`、`t`、`s` 的值，有 `subsonic_endpoint` |
| `TestPostgreSQLSubsonicSealFailurePreservesIdentity` | 密钥损坏时登录与改密成功、Warn 不含密码与密文，确认密码 503 |
| `TestPostgreSQLSubsonicWebSettings` | 网页端四个接口的守卫与第 6 节状态码，样本录制 |
| `TestPostgreSQLSubsonicMigrationRoundTrip` | 0081 up → down → up |

```bash
cd backend
mise exec -- go test ./internal/subsonic/... ./internal/app/ -run 'Subsonic|Route|Dependency'
ROOMUSIC_TEST_RUN='Subsonic' ../scripts/test-integration.sh
```

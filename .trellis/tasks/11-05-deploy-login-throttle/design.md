# 设计：登录限速

需求见 [prd.md](./prd.md)，父任务决定 D4、D5 见[部署上线](../11-05-deploy-launch/prd.md)。
本任务排在 [来源清单](../11-05-deploy-origins/design.md) 之后：两者都改 `identity/httpapi/handlers.go` 的 `login`，后做的一方在前者的基础上改。

## 1. 边界与改动文件

| 文件 | 改动 |
| --- | --- |
| `backend/internal/identity/throttle.go`（新） | 进程内限速器：数据结构、算法、上限与清理；可注入时钟 |
| `backend/internal/identity/throttle_test.go`（新） | 算法单元测试（假时钟） |
| `backend/internal/identity/login.go`（新） | 登录用例：查用户 → 限速 → 核对密码（含等价 bcrypt）→ 建会话；SQL 从 handler 移到这里，与 `passwords.go` 同样的分层 |
| `backend/internal/identity/login_test.go`（新，可选） | 等价 bcrypt 与来源解析的单元测试 |
| `backend/internal/identity/httpapi/handlers.go` | `login` 改为调用 `identity.Login` 并映射错误；`New` 增加限速器与 logger 参数 |
| `backend/internal/identity/httpapi/passwords.go` | 改密码核对当前密码时使用同一限速器（第 9 节，T9） |
| `backend/internal/app/app.go` | 构造限速器；新增测试用 `WithLoginThrottle` 选项 |
| `backend/internal/app/identity_api_integration_test.go`、`api_samples_integration_test.go` | 集成用例与新错误样本 |
| `backend/internal/app/testdata/api-samples/errors/too-many-attempts.json`（新） | 错误样本 |
| `frontend/src/features/auth/components/login-form.tsx` | 限速文案 |
| `frontend/src/test/api-contract.test.ts` | 登记新样本 |
| 登录表单的 Vitest（新或扩展） | 限速时显示那句话、不跳转 |
| spec：`identity-contracts.md`、`error-handling.md`、`frontend/session-lifecycle.md`、`logging-guidelines.md`（事件示例可选） | 第 10 节 |

不新增表、迁移或依赖（T5）。

## 2. 来源地址（T1）

`identity.SourceOf(remoteAddr string) string`，只读 `request.RemoteAddr`，不读任何请求头：

1. `netip.ParseAddrPort(remoteAddr)`；失败时再试 `netip.ParseAddr`（没有端口的情况）；都失败返回固定值 `"unknown"`（所有无法解析的连接共用一个桶，偏严格）。
2. 去掉 IPv6 zone；`Unmap()` 把 `::ffff:a.b.c.d` 变回 IPv4。
3. IPv4 用完整地址；IPv6 取 `/64` 前缀（`netip.Prefix.Masked()` 的文本，如 `2001:db8:1:2::/64`）。

IPv6 按 /64 聚合的取舍：一台设备通常独占一个 /64，可以随意换地址，按单个地址计数等于没有来源维度；代价是同一 /64 下的家庭设备
共用一个来源桶。按部署设计（[packaging](../11-05-deploy-packaging/design.md) 第 5 节）生产端口只发布在 IPv4 上，
局域网用 IPv4 地址访问，所以这个代价在本次部署中实际不会出现；规则仍写进合同，换部署方式时不会退化。

应用内没有改写 `RemoteAddr` 的中间件（已核对 `platform/httpserver/middleware.go`），也不得新增。

## 3. 用户名的规范化

**不做大小写折叠，不去空白，按请求里的原样字节作为键。**

- 核对结果：`users.username` 是 `TEXT NOT NULL UNIQUE`（`0002_core_slice.sql`），登录 `WHERE username=$1`、命令行重置
  `ResetPasswordByUsername` 都是精确匹配（身份合同写明“用户名精确匹配，与登录一致”）。前端登录表单提交前 `trim()`。
- 能登录成功的只有与库中完全相同的那个字符串，所以攻击者换大小写或加空格得到的是另一个“不存在的用户名”，永远不会成功，
  也就不需要把它们并进同一个计数。若折叠大小写，反而会让 `Alice` 的失败锁住 `alice`，多出误伤。
- 为限制内存，键里存 `sha256(username)` 的 32 字节，不存原文（请求体上限 1 MiB，原文做键可被撑大）。
- PRD T1 写的“规范化后”在本设计里就是“与数据库匹配规则一致的原样字符串”，已按此裁决（见 PRD“规划裁决”）。

## 4. 限速器

### 4.1 位置与接口

放在 `identity` 包（身份能力拥有登录策略）。状态只在进程内存，单实例部署，重启清空（T5）。

```go
// throttle.go
type LoginThrottle struct {
	mu        sync.Mutex
	now       func() time.Time
	accounts  map[accountKey]*accountState // 键：sha256(用户名) + 来源
	sources   map[string]*sourceState      // 键：来源
	lastSweep time.Time
}

func NewLoginThrottle(now func() time.Time) *LoginThrottle

// Begin 在核对密码之前调用：被限速时返回 *ThrottledError（含 RetryAfter），不计数；
// 否则先把这次尝试记为失败（预先计数），返回的 Attempt 在密码正确时调用 Succeeded 撤销。
func (t *LoginThrottle) Begin(username, source string, fingerprint [8]byte) (*Attempt, error)

func (a *Attempt) Succeeded()            // 删除这个账号键的状态；从来源窗口里撤掉这次尝试
func (a *Attempt) Failures() int          // 供日志：这个账号键的连续失败次数
func (a *Attempt) LockedFor() time.Duration // 这次失败触发的等待；0 表示没有触发

type ThrottledError struct {
	Scope      string        // "account" 或 "source"
	RetryAfter time.Duration
}
```

### 4.2 为什么“先计数、成功再撤销”

如果在核对密码之后才记失败，攻击者可以在锁定生效前并发发出大量请求（每个请求要做约 50–100 ms 的 bcrypt），一个锁定周期内能试
远多于 5 次。先计数让第 5 次尝试一开始就设好锁，之后的并发请求直接被拒绝。正确密码的那次请求结束后清掉状态，所以正常用户感觉不到差别。

### 4.3 账号键的算法（T1、T4）

常量：`accountThreshold = 5`、`baseWait = 1 * time.Minute`、`maxWait = 15 * time.Minute`、`accountForget = 24 * time.Hour`。

```text
state := accounts[key]
如果 state 存在且 state.fingerprint != fingerprint：删除 state（密码已在别处改过，见第 6 节）
如果 state 存在且 now >= state.lastFailure + accountForget：删除 state（隔了一天，重新计数）
如果 state 存在且 now < state.lockedUntil：拒绝，RetryAfter = lockedUntil - now，不计数
否则：
  state.failures++；state.lastFailure = now；state.fingerprint = fingerprint
  如果 state.failures >= accountThreshold：
     state.lockedUntil = now + min(baseWait << (state.failures - accountThreshold), maxWait)
```

| 第几次连续失败 | 之后需要等 |
| --- | --- |
| 1–4 | 不等 |
| 5 | 1 分钟 |
| 6 | 2 分钟 |
| 7 | 4 分钟 |
| 8 | 8 分钟 |
| 9 及以后 | 15 分钟 |

- 等待期内的尝试直接拒绝、不核对密码、不计数（被拒绝不会把等待继续拉长）。
- 等待结束后再失败一次立刻进入下一档（“之后每再失败一次等待翻倍”）。移位前先判断 `failures - 5 >= 4` 直接取上限，避免溢出。
- 登录成功：删除**这个用户名 + 这个来源**的状态（B2）。不清除同一用户名在其他来源上的计数：局域网成功登录不应顺带替公网上的试探者清零。
  PRD T4 写的是“清零这个用户名的失败计数”，这里按“这个组合”理解，与 B2 的验收一致，已按此裁决（见 PRD“规划裁决”）。
- 用户不存在、已停用与密码错误走同一条路径、同样计数（T4、B4）。

### 4.4 来源上限的算法（T2）

常量：`sourceLimit = 30`、`sourceWindow = 1 * time.Minute`、`sourcePause = 1 * time.Minute`。
每个来源保存最近一分钟内已计数尝试的时间戳（滑动窗口，最多 30 个）和 `pausedUntil`：

```text
src := sources[source]
如果 now < src.pausedUntil：拒绝（Scope="source"），RetryAfter = pausedUntil - now
丢掉 src.stamps 中早于 now - sourceWindow 的时间戳
如果 len(src.stamps) >= sourceLimit：src.pausedUntil = now + sourcePause；拒绝
否则：append(now)（与账号键同时预先计数）
```

- 含义：同一来源一分钟内已有 30 次失败（含正在核对的）时，下一次尝试被拒绝，并从这一刻起暂停 1 分钟；其他来源不受影响（B3）。
  PRD 写“超过 30 次时暂停”，这里第 31 次是被拒绝的那次而不是又一次真正的失败，已按此裁决（见 PRD“规划裁决”）。
- 成功时从 `stamps` 里删掉这次尝试的时间戳（`Attempt` 记着它），正确登录不占来源额度。
- 来源检查在查数据库**之前**做（见第 5 节），批量换用户名的请求在暂停期间不会打到数据库，也不做 bcrypt。
- 经外部反代的所有公网请求共用一个来源，公网上有人批量试时，公网登录会一起暂停 1 分钟；局域网直连不受影响。这是 T1 已接受的取舍。

### 4.5 内存上限与清理（T5）

- 上限：`maxAccounts = 10000`、`maxSources = 4096`。账号条目约 100 字节（32 字节哈希 + 来源文本 + 时间与计数），满载约 1 MB；
  来源条目最多 30 个时间戳，满载约 1 MB。
- 清理不起后台 goroutine：每次 `Begin` 时如果距上次清理超过 1 分钟，就遍历两张表删掉过期项（账号：未锁定且距最后失败超过 24 小时，
  或锁已过期且距最后失败超过 24 小时；来源：时间戳全部过期且暂停已结束）。
- 表满时先做一次清理；仍满则淘汰 `lastFailure` 最早的一条（O(n) 扫描，只在满时发生）。
- 为什么淘汰最旧的而不是拒绝记新条目：拒绝记录等于对新用户名不限速；淘汰最旧的会让攻击者有机会把目标账号的条目挤出去，但要灌满
  1 万条，同一来源受 30 次/分钟的限制，至少要 5.5 小时，换来目标账号 5 次尝试，比直接等 15 分钟的锁更慢，所以可以接受。

### 4.6 时钟

`now` 由构造函数注入，生产用 `time.Now`。所有比较用同一次 `now()` 的结果；测试用可推进的假时钟，不 `sleep`。

## 5. 登录流程（`identity.Login`）

```go
// login.go
type LoginResult struct{ User User; Token string }
var ErrInvalidCredentials = errors.New("invalid credentials")

func Login(ctx context.Context, db *sql.DB, throttle *LoginThrottle, username, password, source string) (LoginResult, *Attempt, error)
```

返回的 `*Attempt` 在 401 时也非空，供 handler 写日志（连续失败次数、是否刚触发等待）；`ThrottledError` 另带 `Started bool`，
只在来源暂停“刚开始”的那次为真。具体返回形状可由实现微调，但 SQL 与限速策略留在 `identity`，日志写在 handler。

handler 的顺序：

1. `RequireSameOrigin`（不变）→ `DecodeJSON`（不变）。
2. `source := identity.SourceOf(request.RemoteAddr)`。
3. `identity.Login(...)` 内部：
   1. **来源检查**（不访问数据库）：来源在暂停期 → `*ThrottledError{Scope:"source"}`。
   2. 查用户：`SELECT id::text,password_hash,role FROM users WHERE username=$1 AND disabled_at IS NULL`。
      `sql.ErrNoRows` → `found=false`；**其他数据库错误 → 返回原错误，映射 `503 database_unavailable`，不计数**（现状把所有错误都当 401，
      数据库故障时会把正常用户算成输错并锁住，必须分开）。
   3. `fingerprint`：找到用户时取 `sha256(password_hash)` 前 8 字节；没找到时为全零。
   4. `throttle.Begin(username, source, fingerprint)`：被限速 → `*ThrottledError{Scope:"account"}`。
   5. 核对：找到用户时 `VerifyPassword(hash, password)`；没找到时 `bcrypt.CompareHashAndPassword(dummyHash, password)` 并视为失败。
      `dummyHash` 用 `sync.Once` 在第一次需要时以 `bcrypt.DefaultCost` 对随机串生成，成本与真实哈希一致，两种失败耗时相近（T4）。
   6. 失败 → `ErrInvalidCredentials`（计数已在 4 完成）。
   7. 成功 → `attempt.Succeeded()`，插入会话（SQL 同现在），返回用户与令牌；插入失败返回数据库错误（503）。
4. handler 映射：

| 结果 | 响应 |
| --- | --- |
| 成功 | 200 查看者 + `SetSessionCookie`（同现在） |
| `ErrInvalidCredentials` | `401 invalid_credentials`“用户名或密码错误”（同现在） |
| `*ThrottledError` | `429 too_many_attempts`“登录失败次数太多，请过几分钟再试”，带 `Retry-After: <向上取整的秒数>` |
| 其他错误 | `503 database_unavailable`“服务暂不可用” |

- 用户名不存在、停用、密码错误在被限速前都是同一个 401，被限速后都是同一个 429，提示与计数完全相同（B4）。
- 第 5 次失败本身返回 401（那次已核对密码），第 6 次起返回 429。
- `Retry-After` 只告诉客户端等多久，不透露账号是否存在（不存在的用户名同样计数、同样等待）。前端不读它，文案里不写具体分钟数。
- 旧版 SHA-256 哈希的账号核对很快，耗时与 bcrypt 不同；这类哈希只存在于 bcrypt 迁移前建的账号，生产库从零开始不会有，不额外处理。

### 错误码

新码 `too_many_attempts`，HTTP 429。取通用名而不是 `login_throttled`：若第 9 节的改密码限速被采纳，两处共用一个码，前端各自覆盖文案。
不用 401（前端请求层会当成会话结束），也不用 403（与权限混淆）。

## 6. 命令行重置后的解除（T6）

`reset-password` 在另一个进程里运行，无法直接清内存。设计用**密码哈希指纹**自然解除：账号条目记下最后一次计数时该用户密码哈希的指纹，
下一次登录时查到的指纹不同（管理员在命令行或成员页重置过、或本人在别的设备上改过）就丢弃旧状态、重新计数，新密码立即可以登录。

- 不需要等待或重启，也覆盖网页上的管理员重置，不必在 `passwords.go` 里加清除调用。
- 代价：账号锁定期间的请求也要查一次用户（主键唯一索引，无 bcrypt）。来源暂停在查库之前拦截，所以批量请求不会放大数据库压力。
- 来源暂停不随重置解除，最多 1 分钟。部署说明里写一句：重置密码后如果还提示次数太多，等一分钟再试。

## 7. 日志（T7）

| 事件 | 级别 | 时机 | 字段 |
| --- | --- | --- | --- |
| `identity.login.failed` | INFO | 每次核对失败（401） | `module=identity`、`request_id`、`source`、`username`、`consecutive_failures` |
| `identity.login.throttled` | WARN | 某次失败触发了账号等待，或来源达到上限开始暂停（只在开始时记一次，被拒绝的后续请求不逐条记） | `module=identity`、`request_id`、`scope`（`account`/`source`）、`source`、`username`（仅 account）、`consecutive_failures`（仅 account）、`retry_after_seconds` |

- `username` 记录请求里的用户名，截到前 64 字节并保证是合法 UTF-8；不记密码、哈希、指纹、Cookie。用户名可能是攻击者随意构造的串，
  JSON handler 会转义，不会破坏日志格式。
- `source` 是第 2 节的来源文本，部署演练用它确认容器看到的是真实局域网地址。
- 被拒绝的请求已有 `http.request.completed`（status 429），不再额外记录，避免攻击时刷屏。
- `identityapi.New` 增加 `logger *slog.Logger` 参数（`nil` 时丢弃，与 `app.New` 一致），`request_id` 从请求头读取（中间件已写入）。

## 8. 装配

- `identityapi.New(database, guard, throttle, logger)`；`throttle` 为 `nil` 时用 `identity.NewLoginThrottle(time.Now)`。
  现有调用点：`app.go` 与 `identity_api_integration_test.go:743`（传 `nil, nil`）。
- `app.New` 新增选项 `WithLoginThrottle(*identity.LoginThrottle)`（与 `WithPlaybackPreflightWait` 同类，只给测试用），默认用真实时钟。
- 每个 `app.New` 有自己的限速器，所以每个集成测试用例互不影响。已核对：现有集成测试与真实服务测试里每个应用实例的失败登录都远少于
  5 次同名、30 次同来源（`httptest` 的 `RemoteAddr` 固定为 `192.0.2.1:1234`，所有用例共用一个来源，实现时再 `rg auth/login` 复核一遍）。

## 9. 改密码一起限速（已裁决：要，T9）

`POST /auth/password` 只有已登录会话能调，但会话可能被盗（公共电脑忘了退出、Cookie 泄露）。拿到会话的人不能直接改密码，
却可以每秒十几次地试当前密码；试中后就知道了真实密码，会话过期后还能再登录，别处用同一密码的账号也受影响。

建议：复用同一个 `LoginThrottle`，键用 `"password:" + 用户 ID`（不分来源：只有持有会话的人能触发，误伤只会落在本人身上，而且只是
“晚点再改”）；阈值与等待和登录相同；不做来源上限。被限速时返回 `429 too_many_attempts`，对话框显示“当前密码输错次数太多，请过几分钟再试。”；
改成功后清除。指纹规则同第 6 节。

不采纳也可以：在合同里写明“改密码不限速，依赖会话 24 小时到期与管理员撤销会话”。改动量约 20 行后端 + 一条前端文案 + 两个测试。

## 10. 前端

- `login-form.tsx` 的 `FailureNotice` 覆盖文案增加一项：`too_many_attempts: "登录失败次数太多，请过几分钟再试。"`。
  只这一句，不解释原因、不写分钟数、不提来源或安全。
- 不放进 `failure-text.ts` 的全局表：只有登录（和可能的改密码）会收到它，各自的句子不同；全局表里放一句泛泛的话反而会出现在不该出现的地方。
- 登录接口已是 `session: "none"`，429 不会结束会话或跳转；`shouldRetryQuery` 只重试查询，登录是写操作，不会自动重试。都不用改。
- 若第 9 节采纳：`change-password-dialog.tsx` 的错误映射增加 `too_many_attempts: "当前密码输错次数太多，请过几分钟再试。"`，显示在对话框
  现有的错误位置（不放在字段旁，因为不是这个字段填错了）。
- `api-contract.test.ts` 登记 `"errors/too-many-attempts": { status: 429, code: "too_many_attempts" }`。

## 11. 需要更新的 spec

- `identity-contracts.md`：新增“登录限速（2026-11-05）”小节，写入第 2–6 节的规则、常量表、流程顺序与 `Retry-After`；错误矩阵加 429 与
  “查用户的数据库错误 → 503，不计数”两行；必需测试加第 12 节用例；命令行恢复一节补一句“重置后限速随新密码失效（指纹规则）”。
- `error-handling.md`：分类表加一行 `too_many_attempts | 429 | 同一账号或来源的尝试过多，稍后再试`。
- `frontend/session-lifecycle.md`：“401 与 403”一条后补“登录的 429 `too_many_attempts` 同样不是会话失效”。
- `logging-guidelines.md`：可选，在“HTTP、扫描和操作事件”后补两个身份事件的字段表；不改也行，合同在身份合同里。

## 12. 测试清单

后端单元（`identity`，假时钟，不需要数据库）：

- 阈值与翻倍：第 1–4 次不锁；第 5 次后 1 分钟内拒绝且不计数；到点后再失败一次等 2 分钟；依次 4、8、15，之后保持 15（B1）。
- 成功清零：失败 4 次后成功，再失败 4 次仍不锁（B2）；成功只清本来源的键，另一来源的计数不变。
- 来源隔离：A 来源锁定时同一用户名从 B 来源可以尝试（B1b）。
- 来源上限：同一来源 30 个不同用户名各失败一次后第 31 次被拒，Scope 为 `source`，1 分钟后恢复；另一来源不受影响（B3）；
  窗口滑动（第 1 次失败过 61 秒后不再算）；成功的尝试不占额度。
- 并发：在 `Begin` 与结束之间同时发起的第 6 个尝试被拒（预先计数）；`-race` 下跑。
- 指纹：锁定期间指纹变化 → 状态丢弃，可以尝试（T6）。
- 遗忘：最后一次失败 24 小时后计数归零。
- 上限：灌满 `maxAccounts` 后新键能记录，被淘汰的是最旧的一条；过期项在下一次清理时消失。
- `SourceOf`：IPv4 带端口、IPv6 带方括号和端口、带 zone、`::ffff:` 映射、IPv6 聚合到 /64、无法解析得到 `unknown`。

PostgreSQL 集成（`internal/app/identity_api_integration_test.go`，用 `WithLoginThrottle` 注入假时钟）：

- 同一用户名连续输错 5 次 → 第 6 次即使密码正确也是 `429 too_many_attempts` 且带 `Retry-After`；推进 1 分钟后正确密码能登录（B1）。
- 不存在的用户名与密码错误的响应体、计数相同（B4）。
- 用不同 `RemoteAddr` 构造请求，验证 B1b、B3。
- 查用户时数据库出错（关闭连接池或用触发器注入）→ 503 且不计数。
- 锁定后用 `identity.ResetPasswordByUsername`（模拟命令行）改密码，新密码立即可以登录（T6）。
- `api_samples_integration_test.go` 记录 `errors/too-many-attempts`。

前端（Vitest，只跑相关文件）：

- 登录表单收到 429 `too_many_attempts` 时显示“登录失败次数太多，请过几分钟再试。”，仍在登录页、表单可再次提交（B5）。
- `api-contract.test.ts` 的样本登记。

命令（不扩大到全量）：

```bash
(cd backend && test -z "$(gofmt -l ./internal/identity ./internal/app)")
go -C backend test -race ./internal/identity/... -count=1
go -C backend vet ./internal/identity/... ./internal/app
# 集成：roomusic-test 隔离库，只跑身份与样本
ROOMUSIC_TEST_DATABASE_URL=... go -C backend test ./internal/app -run 'TestPostgreSQL.*(Identity|Login|Throttle|Password|APISamples)' -count=1
ROOMUSIC_UPDATE_API_SAMPLES=1 ...   # 只在新增样本时运行一次，并核对只多了 too-many-attempts.json
cd frontend && npx vitest run src/features/auth src/test/api-contract.test.ts
cd frontend && npm run typecheck && npm run lint
```

`frontend/e2e/real/account.real.ts` 有一次错误密码登录，不会触发限速；实现时 `rg "auth/login" frontend/e2e/real scripts` 复核一遍，
真实服务测试留到父任务收尾时统一跑一次。

## 13. 考虑过但不选

| 方案 | 不选的原因 |
| --- | --- |
| 只按用户名计数 | 公网上的试探会把家里人在局域网的登录也锁住（T1 已选用户名 + 来源） |
| 读 `X-Forwarded-For` 区分公网用户 | D5 不信任转发头；局域网直连的人可以伪造它绕过限速 |
| 状态存数据库 | T5 要求不新增表；单实例够用 |
| 核对后再计数 | 并发请求能在锁生效前试很多次（4.2） |
| 后台清理 goroutine | 需要生命周期管理；按访问顺带清理足够 |
| 命令行重置后要求重启服务 | 指纹规则不重启也能解除，覆盖面更大 |
| 401 + 特殊 message | 前端按码分支；401 会被当成会话结束 |
| 用户名大小写折叠 | 数据库按原样匹配，折叠只会增加误伤（第 3 节） |

## 14. 回滚

一个后端提交 + 一个前端提交，可分别回退。回退后端时同时删除 `too-many-attempts.json` 样本与前端登记；限速状态在内存里，回退后随进程消失，
没有数据要清理。

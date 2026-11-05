# 账户与密码：技术设计与接口合同

需求见 [prd.md](prd.md)（A1–A5、K1–K3、V1–V5 已裁决，本文只写怎么实现）。前后端都按第 1 节的合同实现。

## 0. 边界一览

| 需求 | 后端 | 前端 | 迁移 |
| --- | --- | --- | --- |
| A1 自助改密码 | 新接口 `POST /api/v1/auth/password`，逻辑在 `identity` | 侧栏身份区“修改密码”对话框（auth feature） | 无 |
| A2 管理员重置 | 新接口 `POST /api/v1/users/{id}/password`，逻辑在 `identity` | 成员表“重置密码”对话框（members feature） | 无 |
| A3 命令行恢复 | `roomusic reset-password <用户名>`：分派在 `cmd/roomusic`，业务复用 `identity` | — | 无 |
| A4 已忽略候选 | `GET /api/v1/artist-candidates?ignored=true`，恢复沿用 `candidate-ignore` | 候选页“待处理 / 已忽略”切换与“恢复提示” | 无 |
| A5 文档 | 合同、路线图、README（第 7 节） | 前端规范 | — |

不改任何已有接口的请求、响应或错误；不加表、不加列。

---

## 1. 接口合同

通用规则沿用 [Core 0 运行合同](../../spec/backend/core0-runtime-contracts.md)：错误信封 `{ "error": { "code", "message" }, "request_id" }`、
`Cache-Control: no-store`、`DecodeJSON` 解码失败只写一个 `400 invalid_json` 信封。写接口先做同源校验。

新增的两个密码接口与已有成员写操作一样：**没有 `Idempotency-Key`，不写操作记录**（G6 / R4 照旧）。

### 1.1 A1 自助改密码 `POST /api/v1/auth/password`

- 路径放在 `/auth` 下（identity 所有），不放 `/me`（那是 personal 的前缀）。
- 请求体：

  ```json
  { "current_password": "旧密码", "new_password": "新密码" }
  ```

  “再输入一次”只在前端比较，不传给后端。未知字段按 `DecodeJSON` 现状忽略，与登录、新建成员一致。
- 成功：`200 { "password_changed": true }`。不重新设置 Cookie，当前会话的令牌和到期时间都不变。
- 新密码可以和当前密码相同：这时效果就是“退出其他设备”，没有坏处，不单独拒绝。

#### 当前密码错误用 `403 current_password_incorrect`

- **不用 401。** 前端请求层在 `status === 401 && code === "unauthorized"` 时触发会话边界（`shared/api/request.ts:87`），
  401 在本项目里统一表示“需要登录”。当前密码输错并不意味着会话失效，用 401 容易被后续改动误当成掉线，把用户踢回登录页。
- **不用 400。** 400 在这里表示“请求本身不合规”（`invalid_input` / `invalid_json`）；当前密码是合规的输入，只是不对。
- **用 403。** [会话生命周期规范](../../spec/frontend/session-lifecycle.md)已经约定“403 只拒绝当前操作或页面，不结束会话”，语义正合适。
  错误码 `current_password_incorrect` 和 `forbidden`（需要管理员权限）不同，前端按错误码选文案，不会混淆。

#### 处理顺序与事务

1. `RequireSameOrigin`：失败时 `403 origin_required / origin_forbidden`。
2. `Authenticate`：失败时 `401 unauthorized`“需要登录”。这是真的掉线，前端按会话失效处理。
3. `DecodeJSON`：失败时 `400 invalid_json`。
4. 调用 `identity.ChangeOwnPassword(ctx, db, user.ID, HashSessionToken(cookie), current, next)`，见第 3 节，步骤如下：
   1. `ValidPassword(next)` 不通过时返回 `ErrInvalidPassword`，不调用 bcrypt，不访问数据库。
   2. 在事务外读出 `password_hash`（`WHERE id=$1 AND disabled_at IS NULL`）。没有这一行就返回 `ErrSessionEnded`。
      再用 `VerifyPassword` 校验当前密码（兼容旧 SHA-256 哈希），不对返回 `ErrWrongCurrentPassword`。然后 `HashPassword(next)`。
      两次 bcrypt 都放在事务外，不在持有行锁时做慢计算。
   3. `BEGIN`，按“先用户行、后会话行”的固定顺序加锁，与停用事务一致：
      - `SELECT password_hash FROM users WHERE id=$1 AND disabled_at IS NULL FOR UPDATE`：没有这一行就返回 `ErrSessionEnded`。
      - `SELECT 1 FROM sessions WHERE token_hash=$2 AND user_id=$1 AND revoked_at IS NULL AND expires_at>NOW() FOR UPDATE`：
        没有这一行就返回 `ErrSessionEnded`，表示当前会话在这期间被撤销了。
      - 锁内的 `password_hash` 和第 2 步读到的不同，说明并发中已经被改过：返回 `ErrWrongCurrentPassword`。
        两个请求同时用同一个当前密码改，最多成功一个。
      - `UPDATE users SET password_hash=$new WHERE id=$1`，要求 `RowsAffected == 1`。
      - `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL AND token_hash<>$2`。
      - `COMMIT`。任何一步失败都回滚：密码和会话要么一起变，要么都不变。

#### A1 错误矩阵

| 条件 | 响应 | 副作用 |
| --- | --- | --- |
| 缺少 Origin / Origin 不可信 | `403 origin_required` / `403 origin_forbidden` | 无 |
| 没有会话、会话过期或已撤销、用户已停用 | `401 unauthorized`“需要登录” | 无 |
| 请求体不是 JSON | `400 invalid_json`（只有一个信封） | 无 |
| 新密码不在 12–72 位，或含可打印 ASCII 以外的字符 | `400 invalid_input`“新密码需要 12 到 72 位英文字母、数字或符号” | 不调用 bcrypt，不写库 |
| 当前密码错误，或在并发中被别处改过 | `403 current_password_incorrect`“当前密码不正确” | 不写库，会话全部保留 |
| 事务进行中当前会话被撤销，或用户被停用 | `401 unauthorized` | 回滚 |
| bcrypt 生成失败 | `500 internal_error`“无法修改密码” | 无 |
| 其他数据库错误（含提交失败） | `503 database_unavailable`“无法修改密码” | 回滚 |

两条 400 的检查顺序：先校验新密码，再核对当前密码。两项都错时返回 `invalid_input`，这样不必为一个注定失败的请求跑 bcrypt。

### 1.2 A2 管理员重置 `POST /api/v1/users/{id}/password`

- 请求体：`{ "password": "新密码" }`。成功时返回 `200 { "password_reset": true }`。

#### 为什么用独立端点，不扩展 `PATCH /users/{id}`

- `PATCH /users/{id}` 的合同是“只接受 `disabled`，返回 `{ disabled }`”，前端有专门的 decoder 和样本。
  往里加 `password` 就要改它的请求校验、响应形态和 decoder，还要定义“同时传两个字段时撤销几次会话、哪个先生效”。
- 重置会撤销全部会话，是一个有副作用的动作，和已有的 `POST /users/{id}/sessions/revoke` 同类。用独立端点时，
  完成日志里的 `route_template` 能直接区分这次操作，路由表测试也更好固定。
- 用 POST 不用 PUT：与 `sessions/revoke`、`logout` 等动作端点保持一致。前端对成员写操作不自动重试，选哪个方法都不影响重放语义。

#### 对自己调用

返回 `403 self_password_reset`“修改自己的密码需要提供当前密码”，不写库。

- 理由：管理员改自己的密码必须走 A1，要验证当前密码。否则一个被盗用的管理员会话不需要知道密码，就能把管理员自己锁在门外。
- 界面上自己那一行本来就没有入口，这条规则只约束直接调用接口的情况。
- 比较时用 `strings.EqualFold(id, actor.ID)`，因为路由里的 UUID 可能是大写。

#### 停用成员

允许重置，重置后仍保持停用：`disabled_at` 不动。该成员的会话在停用时已经撤销，这里再撤销一次也无害。

#### 处理顺序与事务

1. `RequireSameOrigin`，失败时 `403 origin_*`。
2. `RequireAdmin`，失败时 `401 unauthorized` 或 `403 forbidden`。
3. `userIDFromPath`，失败时 `400 invalid_id`，不访问数据库。
4. 目标是自己时 `403 self_password_reset`。
5. `DecodeJSON`，失败时 `400 invalid_json`。
6. 调用 `identity.ResetPassword(ctx, db, id, password)`，步骤如下：
   1. 校验新密码，不合规时返回 `ErrInvalidPassword`。
   2. `HashPassword`，在事务外完成。
   3. `BEGIN`。
   4. `SELECT disabled_at IS NOT NULL FROM users WHERE id=$1 FOR UPDATE`：没有这一行就返回 `ErrUserNotFound`。
   5. `UPDATE users SET password_hash=...`，要求 `RowsAffected == 1`。
   6. `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL`。
   7. `COMMIT`。

   这里不锁管理员集合：重置密码不改变“启用的管理员数量”，没有最后一个管理员的问题。

#### A2 错误矩阵

| 条件 | 响应 | 副作用 |
| --- | --- | --- |
| 缺少 Origin / Origin 不可信 | `403 origin_required` / `403 origin_forbidden` | 无 |
| 未登录 | `401 unauthorized` | 无 |
| 普通成员 | `403 forbidden`“需要管理员权限” | 无 |
| 路由 ID 不是 UUID | `400 invalid_id`“用户标识无效” | 不访问数据库 |
| 目标是自己 | `403 self_password_reset` | 无 |
| 请求体不是 JSON | `400 invalid_json` | 无 |
| 新密码不合规 | `400 invalid_input`“新密码需要 12 到 72 位英文字母、数字或符号” | 不调用 bcrypt，不写库 |
| 用户不存在 | `404 not_found`“用户不存在” | 回滚 |
| bcrypt 生成失败 | `500 internal_error`“无法重置密码” | 无 |
| 其他数据库错误 | `503 database_unavailable`“无法重置密码” | 回滚，密码与会话都不变 |

### 1.3 A4 已忽略候选 `GET /api/v1/artist-candidates?ignored=true`

#### 参数

- `ignored`：不传或传 `false` 时返回待处理候选，与现在完全相同；传 `true` 时返回已忽略的候选。
  其他值、或这个参数重复出现时，返回 `400 invalid_filter`“候选筛选条件无效”。
- `page`、`page_size`：沿用 `ParsePagination(r, 50, 100)`，不合法时 `400 invalid_pagination`。
- 鉴权、整理期间的 `503 artist_catalog_preparing`（带 `Retry-After`）和读取失败的 `503 database_unavailable` 都与现状相同。

#### 返回形态

与待处理列表相同，都是 `ArtistPage[ArtistCandidate]`：`{ items: [{ left, right, key_pairs, reasons, evidence_count }], pagination }`。
不增加字段，前端复用 `decodeArtistCandidatePage`。

#### 筛选与排序（`catalog/governance/artist_candidates.go`）

- 现有的 `artistCandidatesGroupedSQL` 改成按 `ignored` 生成的两个常量（或一个小函数）：
  - 待处理：`visible` 里是 `NOT EXISTS (… artist_candidate_ignores …)`，SQL 一字不改。
  - 已忽略：同一位置换成 `EXISTS (…)`。
  - 其余部分共用：`resolved`、`left_artist_id<>right_artist_id`、按艺人对分组、每 100 个名字对分一块。
- **两位艺人已合并时不出现。** 合并后两边的名字键解析到同一位艺人，`left_artist_id<>right_artist_id` 不成立，
  这一对在两个列表里都不会出现。忽略记录留在表里，不删也不影响什么。
  某位艺人的曲目全部 missing、或者被作用域覆盖时，`resolved` 本身就会排除，与待处理列表的规则一致。
- **排序。**
  - 已忽略列表按“最近忽略的在前”排：每个名字对经 `artist_candidate_ignores.operation_id` 关联 `operations.created_at`，
    取这一组的最大值作为忽略时间。
  - 排序键依次是 `ignored_at DESC, evidence_count DESC, first_artist, second_artist, chunk`。
  - 待处理列表的排序不变。
  - 理由：想找回的通常是最近点错的那一条。
- **分页。** 先数总数，再按页取，与待处理列表一样在同一个只读 RepeatableRead 事务里完成。
  已忽略列表的分块只统计被忽略的名字对。同一对艺人如果一部分名字对被忽略、一部分没有，会在两个列表里各出现一次，
  `key_pairs` 也各不相同。
- **Go 接口。** 现有的 `ListArtistCandidates(ctx, db, page, size)` 签名不变（已有 7 处调用）。新增
  `ListIgnoredArtistCandidates(ctx, db, page, size)`，两者都委托给内部的 `listArtistCandidates(…, ignored bool)`。
  handler 先解析 `ignored`，再选择调用哪一个。

#### 恢复

沿用现有的 `POST /api/v1/artists/{left.id}/candidate-ignore`，请求体如下：

```json
{
  "expected_revision": "<left 当前版本>",
  "other_artist_id": "<right.id>",
  "other_expected_revision": "<right 当前版本>",
  "key_pairs": "<这一条的 key_pairs>",
  "ignored": false
}
```

执行器校验名字对时用的 `resolved` 不考虑忽略表，所以已忽略列表里的名字对一定能通过“仍属于这两位艺人”的检查。
恢复后，这条忽略记录被删除，下一次读取时这一对回到待处理列表。不改执行器，不改合同。

### 1.4 新增接口样本

样本由真实 handler 写出，格式是 `{status, body}`，UUID 和时间已规范化。在后端登记位置写出样本后，前端在
`frontend/src/test/api-contract.test.ts` 的映射里逐个登记 decoder 或错误码；漏登记会让往返测试失败。

| 样本名 | 状态 | 前端映射 | 后端登记位置 |
| --- | --- | --- | --- |
| `auth/password-changed` | 200 | `decodePasswordChanged`（auth） | `api_samples_integration_test.go` 的身份段 |
| `users/password-reset` | 200 | `decodePasswordReset`（members） | 同上 |
| `errors/current-password-incorrect` | 403 | code `current_password_incorrect` | 同上 |
| `errors/self-password-reset` | 403 | code `self_password_reset` | 同上 |
| `errors/invalid-input-password` | 400 | code `invalid_input`（新密码文案） | 同上 |
| `governance/artist-candidates-ignored` | 200 | `decodeArtistCandidatePage` | `artist_samples_integration_test.go` 的候选段，并加入 `artistRequiredSamples` |
| `governance/artist-candidates-ignored-empty` | 200 | `decodeArtistCandidatePage` | 同上 |
| `artists/errors/invalid-candidate-filter` | 400 | code `invalid_filter` | 同上 |

录制要点：

- **身份段。** 放在 `users/revoke-sessions` 之后：
  - 用 archivist 账号先改一次、再改回原密码，或者另建一个后续不再登录的账号，不要影响后面用 `sampleListenerPassword` 登录的步骤。
  - 新密码字符串要 `samples.forbid(...)`，确保样本里不出现密码。
- **候选段。** 位置在现有 `ignore("governance/artist-ignore", true)` 之后，依次录：
  1. `?ignored=true`，应有 1 条；
  2. 恢复以后的 `?ignored=true`，应为空；
  3. `?ignored=yes` 的 400。

---

## 2. 命令行 A3：`roomusic reset-password <用户名>`

### 2.1 分派与放置

- `cmd/roomusic/main.go`：在 `main()` 最前面判断 `len(os.Args) > 1`。有参数时执行 `os.Exit(runCommand(os.Args[1:], …))`，不再走启动服务的分支。
  不带参数时行为与现在完全相同，`scripts/prod.sh` 和 `dev.sh` 都不带参数。
  - `reset-password`：执行 `runResetPassword`。
  - `help`、`-h`、`--help`：用法写到 stdout，退出码 0。
  - 其他任何参数：stderr 输出“未知命令：<参数>”和用法，退出码 2。
- `cmd/roomusic/reset_password.go`（新文件）只放命令行的胶水代码：参数、读密码、连库、把错误映射成退出码与文案。它依赖注入，方便测试：

  ```go
  type resetPasswordEnv struct {
      stdin        io.Reader
      stdout       io.Writer
      stderr       io.Writer
      isTerminal   func() bool                       // 默认 term.IsTerminal(int(os.Stdin.Fd()))
      readSecret   func(prompt string) (string, error) // 默认：提示写到 stderr，term.ReadPassword 读取
      databaseURL  func() (string, error)              // 默认 config.DatabaseURL
      connect      func(ctx context.Context, url string) (*sql.DB, error) // 默认 postgres.Connect
  }
  func runResetPassword(ctx context.Context, args []string, env resetPasswordEnv) int
  ```

- 业务部分（校验、哈希、更新、撤销会话）调用 `identity.ResetPasswordByUsername`，见第 3 节，和 HTTP 共用同一套实现。
- `cmd/roomusic` 不属于 `internal`，不受 `dependency_rules_test.go` 约束；`identity` 也不引入终端依赖。

### 2.2 隐藏输入与非终端输入

- **依赖。** `go.mod` 目前没有 `golang.org/x/term`，需要新增 **`golang.org/x/term v0.45.0`**。它会连带引入 `golang.org/x/sys v0.47.0`。
  - 选这个版本的原因：v0.45.0 的 `go.mod` 要求 `go 1.25.0`，与本项目的 `go 1.25.0` 和本机 `go1.25.10` 一致；
    v0.46.0 及以上要求 go 1.26，会迫使项目升级 Go 版本。
  - 两个模块都已经在本机模块缓存里。
  - x/term 和已在使用的 `golang.org/x/crypto` 同属 Go 官方扩展库，没有其他传递依赖。
  - 不自己写 ioctl：那样要按平台分文件，维护成本更高。
  - 实现时用 `go get golang.org/x/term@v0.45.0`，不跑会重排整个 `go.mod` 的 `go mod tidy`，避免无关 diff。
    如果为了把 `// indirect` 标记改正而跑了 tidy，在回报里说明。
- **终端（stdin 是 TTY）。**
  - 提示语写到 stderr：先“新密码：”，再“再输入一次：”。用 `term.ReadPassword` 读取，不回显；每次读完补一个换行。
  - 两次不一致时报错退出，不重试。
  - 读之前用 `term.GetState` 保存终端状态，并注册 SIGINT/SIGTERM 处理。中断时先 `term.Restore`，再以退出码 130 退出。
    这样可以避免按下 Ctrl-C 后终端一直不回显。
- **非终端（管道或重定向）。**
  - 从 stdin 读一行：最多 4 KiB，用 `bufio.Reader.ReadString('\n')`；读到 EOF 时也接受已读到的内容。
  - 只去掉末尾的 `\n` 或 `\r\n`。**不要 `TrimSpace`**：空格是合法的密码字符。
  - 不要求输入两次。读到空行或空输入时，报“没有读到密码。”。
- 密码只存在局部变量里，不写日志，不出现在错误文案中。

### 2.3 连库：只连接，不迁移，不启动 HTTP

- 现状：`postgres.Open` 会连接、ping 并**自动应用迁移**（`internal/platform/postgres/migrate.go:53-67`）；
  `main.go` 的 `openDatabase` 还会执行 `InitializeCatalog` 和 `RecoverInterruptedScans`。
- 子命令不能复用这条路径。如果用新版本的二进制对着正在运行的旧库跑一次恢复命令，就会顺带迁移数据库、恢复扫描，
  这超出了“改一个密码”的预期。
- 做法如下：
  - 在 `platform/postgres` 新增 `Connect(ctx, url) (*sql.DB, error)`，只做 `sql.Open("pgx")` 和 `PingContext`。
    `Open` 改成先 `Connect` 再 `applyMigrations`，行为不变。
  - 子命令只调用 `Connect`。`users` 和 `sessions` 两张表自 0006 起结构稳定，不需要检查迁移版本。
  - 库从没初始化过时（没有 `users` 表），按“数据库不可用”处理。
- **配置。** `config.Load()` 还要求 `ROOMUSIC_ALLOWED_LIBRARY_ROOTS`，生产环境还要求安全 Cookie，这些与改密码无关。
  - 在 `config` 新增 `DatabaseURL() (string, error)`：读取 `ROOMUSIC_DATABASE_URL` 并 `TrimSpace`，为空时返回错误。
  - `Load` 改为调用它，行为不变。
  - 子命令只调用 `DatabaseURL`。
- 整个命令用一个 30 秒超时的 context；连库失败、超时都按“无法连接数据库”处理。

### 2.4 退出码与输出

| 情况 | 退出码 | 输出（stderr，成功写 stdout） |
| --- | --- | --- |
| 成功 | 0 | “已重置“<用户名>”的密码，这个账号已在所有设备上退出登录。” |
| 成功，但账号是停用的 | 0 | 先输出上一行，再输出“这个账号现在是停用的，要在成员页重新启用后才能登录。” |
| 参数个数不对 | 2 | “用法：roomusic reset-password <用户名>” |
| 未知子命令 | 2 | “未知命令：<参数>”和用法 |
| 两次输入不一致 | 1 | “两次输入的密码不一致。” |
| 没有读到密码 | 1 | “没有读到密码。” |
| 密码不合规（在连库之前检查） | 1 | “密码需要 12 到 72 位英文字母、数字或符号。” |
| 缺少 `ROOMUSIC_DATABASE_URL` | 1 | “没有设置 ROOMUSIC_DATABASE_URL。” |
| 连不上数据库 / 超时 | 1 | “无法连接数据库。” |
| 用户不存在 | 1 | “找不到用户“<用户名>”。” |
| 其他数据库错误 | 1 | “数据库出错，密码没有修改。” |
| 被中断 | 130 | 无，只恢复终端状态 |

- **不输出底层错误。** pgx 的连接错误里可能有主机、端口、库名和用户名，所以一律只输出上表的固定文案，不打印 `err`。
  密码和数据库地址都不会出现在输出里，测试会断言这一点。
- 用户名按原样精确匹配，与登录的 `username=$1` 一致，不去空白、不改大小写。

### 2.5 运维入口（建议）

生产环境用 `scripts/prod.sh`，它会 source `.env` 再执行 `go run ./cmd/roomusic`。建议新增 `scripts/reset-password.sh <用户名>`：

- 按 prod.sh 的方式 source `${ROOMUSIC_ENV_FILE:-.env}`；
- 然后 `exec go -C backend run ./cmd/roomusic reset-password "$@"`；
- 不打印环境变量；stdin 原样传给子进程，终端里照样隐藏输入。

README 写这一条命令即可（见第 7 节）。如果不想多一个脚本，README 就写等价的三行 `set -a; source .env; set +a; go -C backend run …`。

---

## 3. 后端结构

### 3.1 identity 包新增 `identity/passwords.go`

```go
var (
    ErrInvalidPassword      = errors.New("password does not meet the rules")
    ErrWrongCurrentPassword = errors.New("current password is incorrect")
    ErrUserNotFound         = errors.New("user not found")
    ErrSessionEnded         = errors.New("session is no longer active")
)

// PasswordRuleMessage 是新密码不合规时的提示，HTTP 与命令行共用。
var PasswordRuleMessage = fmt.Sprintf("新密码需要 %d 到 %d 位英文字母、数字或符号", MinPasswordLength, MaxPasswordLength)

// ResetResult 报告被重置账号此刻是否停用（命令行据此多说一句）。
type ResetResult struct{ Disabled bool }

func ChangeOwnPassword(ctx context.Context, db *sql.DB, userID string, sessionTokenHash []byte, current, next string) error
func ResetPassword(ctx context.Context, db *sql.DB, userID, next string) (ResetResult, error)
func ResetPasswordByUsername(ctx context.Context, db *sql.DB, username, next string) (ResetResult, error)
```

- 三个函数都先校验新密码，再在事务外哈希，最后进入事务；它们共用一个内部函数
  `setPasswordTx(ctx, tx, userID, hash, keepTokenHash []byte)`，负责更新哈希、检查 `RowsAffected` 并撤销会话。
  `keepTokenHash` 为 nil 时撤销全部会话，否则保留这一个。事务细节见 1.1 和 1.2。
- 除哨兵错误外，其他错误都用 `%w` 包装原始的数据库错误后返回。调用方把哨兵以外的错误一律映射为 503 或“数据库出错”，
  bcrypt 失败映射为 500。
- 用包级函数，不挂在 `Guard` 上：命令行没有 Cookie 配置，不应该构造 Guard。
- `identity` 只依赖 `platform/httpserver`。新增文件只用 `database/sql`、`errors`、`fmt`，依赖规则不变。

### 3.2 identity/httpapi

- `Register` 新增两条路由：`POST /api/v1/auth/password` 交给 `changePassword`，`POST /api/v1/users/{id}/password` 交给 `resetPassword`。
- handler 只做同源校验、鉴权、路由 ID 校验、自我检查、解码，再调用 identity 函数，最后用 `errors.Is` 映射错误。**新增代码里没有 SQL。**
- 当前会话令牌的哈希由 handler 从 Cookie 读出后计算：`identity.HashSessionToken(cookie.Value)`。Authenticate 刚成功，Cookie 一定存在。
- `routes_test.go` 固定这两条新路由的 `route_template`。

### 3.3 B0 技术债：本任务不抽已有的成员 SQL

`handlers.go` 头注释和 `directory-structure.md` 都记着“成员写入 SQL 留在 handler”。建议**只做最小范围**：

- 新增的两条密码路径从一开始就把 SQL 放在 identity。
- 已有的 `createUser`、`updateUser`、`revokeUserSessions`、`setupAdmin`、`login` 原样不动。

理由：

- 抽出已有 SQL 属于纯重构，需要重做停用事务、初始化 409/503 映射等一整套回归，与本任务的验收无关，会把 diff 和审查面放大一倍以上。
- R4 已经撤销了成员写操作接入幂等的计划，没有其他任务逼着现在动这部分。

收尾时把 `handlers.go` 头注释改成“新增的密码接口已在 identity，其余成员写入 SQL 仍是 B0 技术债”，并同步 `directory-structure.md` 的那一段。

### 3.4 其他后端改动

- `platform/postgres`：新增 `Connect`，`Open` 改为调用它（2.3）。
- `platform/config`：新增 `DatabaseURL`，`Load` 改为调用它（2.3）。
- `catalog/governance/artist_candidates.go`：新增 `ListIgnoredArtistCandidates`，SQL 参数化（1.3）。
- `catalog/httpapi/artist_governance.go` 的 `candidates`：解析 `ignored` 后选择调用哪个函数；解析方式参照 `artistReadEnum`，只接受单个 `true` 或 `false`。

---

## 4. 前端结构

### 4.1 A1：侧栏“修改密码”（auth feature）

- **api。**
  - `endpoints.ts` 新增 `changeOwnPassword({ currentPassword, newPassword })`，请求 `POST /api/v1/auth/password`。
    它保持默认的 `session: "required"`：真正的 401 `unauthorized` 照常结束会话。
  - `decoders.ts` 新增 `decodePasswordChanged`，形态是 `{ password_changed: literal(true) }`。
- **规则（`account-rules.ts`）。**
  - 把 `validateNewAccount` 里的密码与确认检查抽成 `validateNewPassword(password, confirmation)`，两处共用。
  - 新增 `validatePasswordChange({ current, password, confirmation })`，当前密码为空时提示“请输入当前密码。”。
  - 新增常量 `newPasswordRuleMessage`，对应后端的 `invalid_input`。
- **hook。**
  - `hooks/use-password-change.ts` 用普通 `useMutation` 实现。成功或失败的回调都先检查 `scope.isCurrent()`，确认仍在发起时的会话里。
  - **成功后什么都不清：** 查看者没变（id、role 都一样），不增加代次，不清查询缓存，不调用 `router.invalidate`，也不在
    `roomusic:session` 频道广播。
  - 理由：同一浏览器的其他标签页共用这个 Cookie，会话仍然有效；其他浏览器的会话已经被后端撤销，它们下一次请求会得到 401，
    由自己的会话边界回到登录页，这是现有机制。
- **组件。**
  - `components/change-password-dialog.tsx` 照 `rename-artist-dialog.tsx` 的方式写：`ModalDialog` 加 `<form>`，提交中禁止 Esc 和取消。
  - 字段：
    - 当前密码：`autoComplete="current-password"`；
    - 新密码：`new-password`，说明文字用 `passwordHint`；
    - 确认新密码：`new-password`。
  - 另放一个隐藏的 `autoComplete="username"` 只读输入，值是查看者的用户名，让密码管理器把新密码记到正确的账号下。
- **失败的处理。**
  - `403 current_password_incorrect`：错误显示在“当前密码”字段旁并聚焦，对话框不关，其余输入保留。
  - `400 invalid_input`：错误显示在“新密码”字段旁。
  - 其他确定的失败：对话框里用 `Notice` 加 `describeFailure(context: "write")` 显示。
  - 结果不确定（连接断开、5xx、无效响应）：对话框里显示“结果未确认”。不自动重试；用户可以再点一次提交。
- **入口。**
  - `app/shell/identity-panel.tsx` 在“退出登录”上方加一个文字按钮“修改密码”，图标用 `KeyRound`。
  - 成功后对话框关闭，身份区显示一条带“关闭”按钮的成功 `Notice`。
  - `features/auth/index.ts` 导出 `ChangePasswordDialog`（或组合好的 `ChangePasswordButton`）。

### 4.2 A2：成员表“重置密码”（members feature）

- **api。** `endpoints.ts` 新增 `resetMemberPassword(id, password)`，请求 `POST /api/v1/users/{id}/password`；
  decoder 是 `decodePasswordReset`，形态 `{ password_reset: literal(true) }`。
- **命令。**
  - `use-member-commands.ts` 的 `MemberCommand` 增加一种：`{ kind: "reset-password"; member: MemberTarget & { disabled: boolean }; password: string }`。
  - 照旧只有一个成员命令在进行，结束后无论成败都刷新列表，不自动重试。
  - 密码会暂存在 mutation 的 variables 里，这与新建成员相同；会话变化时整个缓存清空，可以接受。
- **表格（`member-table.tsx`）。**
  - 新增 `onResetPassword` 回调。只有 `member.id !== viewerId` 的行显示“重置密码”按钮，启用和停用的成员都显示。
  - 按钮顺序：启用的行是“停用 / 撤销会话 / 重置密码”，停用的行是“重新启用 / 重置密码”。
  - 自己那一行不显示这个按钮。
- **对话框。**
  - 新建 `components/reset-password-dialog.tsx`：`ModalDialog` 加 `<form>`。
  - `ConfirmDialog` 只有说明文字、没有表单，不适合这里；但标题、说明、按钮的样式与 `member-action-dialog` 保持一致，
    取消按钮获得初始焦点的方式也一样。
  - 字段：新密码和确认新密码，都用 `autoComplete="new-password"`，与新建成员表单一致。
  - 本地校验复用 `validateNewPassword`。
  - 与已有的成员对话框一样，命令结束（成功或失败）时关闭对话框，结果显示在列表上方的 `MemberCommandNotice` 里。
- **结果文案。**
  - `MemberCommandNotice.successText` 增加 `reset-password` 分支，启用与停用的成员各一句，见 4.4。
  - 失败时按命令种类选择文案表：`reset-password` 用 `{ ...memberFailureMessages, invalid_input: newPasswordRuleMessage }`，
    不能用提到“用户名”的 `invalidAccountMessage`。
- **页面。** `member-management.tsx` 增加 `resetting` 状态，接上对话框。

### 4.3 A4：候选页“待处理 / 已忽略”（governance feature）

- **地址状态。**
  - `/artist-candidates?status=ignored&page=2`。
  - `status` 只在看“已忽略”时出现，与问题页的 `status=reviewed` 写法一致。
  - `router.tsx` 的 `validateSearch` 返回 `{ page, status }`：`status` 只接受 `"ignored"`，其他值写成 `undefined`。
  - `ArtistCandidatesPage` 把 `status` 传给 view。
- **数据。**
  - `fetchArtistCandidates` 增加 `ignored: boolean` 参数，为 true 时在查询串里加 `ignored=true`。
  - `governanceKeys.artistCandidatePage` 的 filter 增加 `ignored`，仍在 `artistCandidates` 前缀下。
  - `useArtistCandidates(page, ignored)`。
  - 合并、忽略、恢复之后，`use-artist-commands` 已经让整个 `governanceKeys.all` 失效，**两个列表都会重新读**，不需要新增失效逻辑。
- **界面（`artist-candidates-view.tsx`）。**
  - 顶部加筛选导航，`aria-label="候选状态"`，包含“待处理”和“已忽略”两个链接，当前项带 `aria-current="page"`。
    写法照抄 `IssueFilters` 的状态那一行，样式放进本组件自己的 css module。
  - 不显示数目：只有当前列表的总数，仍然放在 Panel 标题右侧。
  - 切换时不带 `page`，回到第一页。
  - 分页链接和越界纠正时的 `navigate` 都要保留 `status`。
  - 已忽略的条目复用 `CandidateItem` 的展示（两位艺人和理由），操作只有一个“恢复提示”按钮，调用现有的
    `ignore(candidate, false)`：先读两边的最新版本号，再提交 `ignored: false`。忙碌状态的判断与“忽略”相同。
  - 已忽略列表里**不提供“合并…”**，与 PRD 的“逐条恢复”一致；要合并的话，先恢复，再到待处理里操作。
  - 恢复成功后，`ArtistCommandNotice` 显示已有的文案“已恢复‘甲’和‘乙’的提示。”。这一条从已忽略列表里消失，出现在待处理列表里。
  - 待处理视图里刚忽略之后的“恢复提示”按钮保持不变。
- **顺带说明。** 现有条目的 React key 是 `left.id:right.id`。同一对艺人超过 100 个名字对时会分块，key 会重复。这是已有问题，
  本任务不改；实现时如果顺手改，可以把 key 换成第一个名字对的两个 key id，并在回报里说明。

### 4.4 新增文案草稿

按全局文案规则：不解释权限、安全或实现细节；数据本身已经说清的不加说明；必要的说明只用一句日常话。

| 位置 | 文案 |
| --- | --- |
| 侧栏按钮 | 修改密码 |
| 改密码对话框标题 | 修改密码 |
| 改密码对话框说明（唯一一句，告诉用户其他设备会退出） | 改好后，其他设备需要重新登录。 |
| 字段 | 当前密码 / 新密码 / 确认新密码 |
| 新密码说明 | 至少 12 位，可用英文字母、数字和符号。（沿用 `passwordHint`） |
| 主按钮 / 提交中 | 修改密码 / 正在修改… |
| 当前密码为空 | 请输入当前密码。 |
| 当前密码错误（403） | 当前密码不对。 |
| 新密码不合规（前端） | 沿用现有的：密码只能用英文字母、数字和符号。 / 密码至少要 12 位。 / 密码太长了，请短一些。 / 两次输入的密码不一致。 |
| 新密码不合规（后端 400） | 新密码至少 12 位，只能用英文字母、数字和符号。 |
| 改密码成功 | 密码已修改，其他设备已退出登录。 |
| 改密码结果不确定 | 标题“结果未确认”，正文：如果再试时提示当前密码不对，说明新密码已经生效。 |
| 成员行按钮（可访问名称） | 重置密码（`重置成员 {用户名} 的密码`） |
| 重置对话框标题 | 重置“{用户名}”的密码 |
| 重置对话框说明（启用的成员） | 对方的所有设备都会退出登录，需要用新密码重新登录。 |
| 重置对话框说明（停用的成员） | 对方仍是停用状态，重新启用后才能登录。 |
| 字段 | 新密码 / 确认新密码 |
| 主按钮 / 提交中 | 重置密码 / 正在重置… |
| 重置成功（启用） | 已重置“{用户名}”的密码，对方需要用新密码重新登录。 |
| 重置成功（停用） | 已重置“{用户名}”的密码。 |
| 候选筛选 | 待处理 / 已忽略 |
| 已忽略 Panel 标题 | 已忽略 |
| 已忽略为空 | 没有忽略过的候选。 |
| 已忽略条目按钮（可访问名称） | 恢复提示（`恢复“甲”和“乙”的提示`） |
| 恢复成功 | 已恢复“甲”和“乙”的提示。（已有） |

后端 message 文案（前端按错误码选文案，以下仅供日志和直接调用接口时阅读）：

- `current_password_incorrect`：当前密码不正确
- `self_password_reset`：修改自己的密码需要提供当前密码
- `invalid_input`：新密码需要 12 到 72 位英文字母、数字或符号
- `invalid_filter`：候选筛选条件无效

---

## 5. 测试计划

遵守 `AGENTS.md` 的测试范围约束：每一步只跑相关的最小集合，收尾时跑一次真实服务用例。

### 5.1 后端集成测试（PostgreSQL）

文件为 `internal/app/identity_api_integration_test.go`，除非另行注明。

| 测试名 | 覆盖 | 对应 |
| --- | --- | --- |
| `TestPostgreSQLChangeOwnPasswordKeepsCurrentSessionAndRevokesOthers` | 同一用户登录两次（A、B），另有一个其他用户的会话 C。用 A 改密码后：A 的 `/auth/me` 是 200；B 是 401；C 是 200；旧密码登录得到 `401 invalid_credentials`；新密码能登录；响应不带 `Set-Cookie` | V1 |
| `TestPostgreSQLChangeOwnPasswordRejectsWrongCurrentPassword` | `403 current_password_incorrect`，code 不是 `unauthorized`；库里的哈希不变；A、B 会话都还有效 | V1 |
| `TestPostgreSQLChangeOwnPasswordValidatesNewPassword` | 新密码为 11 位、73 位、含中文、含制表符时都是 `400 invalid_input`，哈希和会话都不变；12 位和 72 位通过；非法 JSON 只写一个信封 | V1 |
| `TestPostgreSQLChangeOwnPasswordRequiresSessionAndOrigin` | 没有 Cookie 得到 401 `unauthorized`；缺少 Origin 得到 `403 origin_required`；已停用用户的会话得到 401 | V1 |
| `TestPostgreSQLChangeOwnPasswordIsAtomic` | 用测试触发器让 `UPDATE sessions` 抛 `P0001`，得到 503；密码哈希没变（与会话撤销一起回滚） | V1 |
| `TestPostgreSQLConcurrentOwnPasswordChangesSucceedOnce` | 用同一个当前密码并发提交两次，一次 200、一次 403 | V1 |
| `TestPostgreSQLAdminResetPasswordRevokesAllSessions` | 成员有两个会话，重置后两个都是 401；新密码能登录，旧密码不能；管理员会话不受影响 | V2 |
| `TestPostgreSQLAdminResetPasswordKeepsDisabledMemberDisabled` | 重置停用的成员得到 200，成员仍是停用；用新密码登录得到 `401 invalid_credentials`；重新启用后能登录 | V2 |
| `TestPostgreSQLAdminResetPasswordRejectsInvalidRequests` | 普通成员 403 `forbidden`；非 UUID 得到 400 `invalid_id`，且不访问数据库；未知用户 404；对自己调用 403 `self_password_reset`；新密码不合规 400；缺少 Origin 403；未登录 401；注入触发器后 503，密码和会话都不变 | V2 |

另有：

- `routes_test.go`：新增两条路由。
- `api_samples_integration_test.go`：录制 1.4 列出的 5 个身份样本。

候选部分在 `internal/catalog/governance/artist_integration_test.go`：

| 测试名 | 覆盖 | 对应 |
| --- | --- | --- |
| `TestPostgreSQLArtistIgnoredCandidatesListAndRestore` | 忽略后，待处理为空、已忽略有 1 条，`key_pairs` 与原来相同；恢复后，已忽略为空、待处理有 1 条；重扫后仍然保持 | V4 |
| `TestPostgreSQLArtistIgnoredCandidatesExcludeMergedPairs` | 忽略以后再合并两位艺人，这一对在两个列表里都不出现 | V4 |
| `TestPostgreSQLArtistIgnoredCandidatesOrderAndPaging` | 两对先后忽略，最近忽略的排在前面；`page_size=1` 分两页，总数正确 | V4 |

- `artist_samples_integration_test.go`：录制 3 个候选样本，并加入 `artistRequiredSamples`。
- handler 层：`ignored=yes` 和重复的 `ignored` 参数都返回 400 `invalid_filter`，由样本覆盖。

### 5.2 命令行测试（`cmd/roomusic/reset_password_test.go`）

单元测试（不需要数据库；注入的 `connect` 一旦被调用，测试就失败）：

- `TestResetPasswordCommandUsage`：没有参数、两个参数时退出码 2；`help` 时退出码 0；未知命令时退出码 2。
- `TestResetPasswordCommandRejectsInvalidPasswordBeforeConnecting`：stdin 里是 11 位、中文或空行时，退出码 1，并且没有连库。
- `TestResetPasswordCommandReadsOneLineFromPipe`：`"pass word 1234\r\n"` 读出来是 `"pass word 1234"`，空格保留。
- `TestResetPasswordCommandTerminalMismatch`：注入 `isTerminal=true`，`readSecret` 两次返回不同的值时退出码 1；提示语写在 stderr。
- `TestResetPasswordCommandMissingDatabaseURL`：退出码 1，文案固定。

PostgreSQL 集成测试（用 `postgrestest.NewSchema` 建隔离 schema，先用 `postgres.Open` 迁移，再通过 `app.New(...).Handler()` 登录）：

- `TestPostgreSQLResetPasswordCommandRevokesSessionsAndChangesPassword`：管理员先登录得到会话，用命令行重置后，旧会话是 401；新密码能登录，旧密码不能；退出码 0。（V3）
- `TestPostgreSQLResetPasswordCommandUnknownUserChangesNothing`：用户不存在时退出码 1；所有用户的哈希和会话都不变。（V3）
- `TestPostgreSQLResetPasswordCommandOutputHasNoSecrets`：在成功、失败和连不上数据库三种情况下，把 stdout 和 stderr 合起来检查：
  不含密码，也不含数据库 URL 及其中的主机、端口、库名、用户名。连不上的情况用 `127.0.0.1:1` 这样的地址。（V3）
- `TestPostgreSQLResetPasswordCommandDoesNotMigrate`：对一个空 schema 运行，退出码 1，而且 schema 里没有生成迁移表。

`internal/platform/postgres`：在已有的 `migrate_test.go` 里补一条用例，确认 `Connect` 不建迁移表。

### 5.3 前端 Vitest

- `account-rules.test.ts`：`validatePasswordChange` 和 `validateNewPassword` 的边界：空当前密码、11、12、72、73 位、中文、确认不一致。
- `auth/api/decoders.test.ts` 和 `members/api/decoders.test.ts`：新增 decoder 的正例（来自样本）与反例。
- `src/test/api-contract.test.ts`：登记 1.4 的 8 个样本。
- 身份区与改密码对话框（app 层测试，用 `renderApp` 和 fake API；`src/test/fake-api.ts` 增加 `/auth/password`）：
  - 成功后提示出现，仍停留在当前页，没有请求 `/auth/me` 以外的会话重建，代次不变；
  - 403 时错误显示在字段旁，对话框不关，**不跳转到登录页**；
  - 401 `unauthorized` 时回到登录页；
  - 提交中按 Esc 无效。
- 成员页（`fake-members.ts` 增加 `POST /users/{id}/password`）：
  - 自己那一行没有“重置密码”，停用的成员那一行有；
  - 对话框做本地校验；
  - 成功后提示文案按启用和停用区分；
  - 404 和结果不确定时刷新列表；
  - 请求不带 `Idempotency-Key`。
- 候选页（`fake-artists.ts` 支持 `ignored=true`，只把 `/artist-candidates` 的已忽略集合单独存放）：
  - `status=ignored` 时请求带 `ignored=true`；
  - 切换链接带 `aria-current`；
  - “恢复提示”先读两位艺人的详情，再提交 `ignored:false`；
  - 成功后两个列表都重新读取；
  - 分页链接保留 `status`；
  - `validateSearch` 丢弃非法的 `status`。

### 5.4 Playwright（模拟后端，`e2e/`）

- `auth.spec.ts`：
  - 用纯键盘打开“修改密码”，故意输错当前密码，看到字段错误；改对后成功，页面没有掉线。
  - 新密码不合规时，本地拦下，不发请求。
- `members.spec.ts`：重置一个成员（含停用的成员）的密码，确认自己那一行没有入口，请求体只有 `password`。
- `artists-governance.spec.ts`：用纯键盘依次完成：忽略、切到“已忽略”、恢复提示、回到待处理能看到这一条，刷新后保持。
- `responsive.spec.ts`：以下界面在五种宽度下不溢出：身份区（含成功提示）、改密码对话框、成员表（三个按钮的行）、重置对话框、
  候选页的两个视图。
- `a11y.spec.ts`：上面几个状态用 axe 检查，不能有违规。截图按质量规范复核。

### 5.5 真实服务用例（收尾跑一次，`scripts/playback-e2e.sh` 的隔离环境）

新增 `e2e/real/account.real.ts`：

- **不改 `playback-admin` 的密码**：其他 real 用例要用它登录。
- 管理员通过接口新建一个用户名带随机后缀的成员。
- 成员在两个浏览器上下文（A、B）里登录。在 A 的页面上改密码后：A 不掉线，B 的 `/auth/me` 是 401；旧密码不能登录，新密码能登录。
- 管理员在成员页上给这个成员重置密码后：该成员所有上下文都是 401，新密码能登录。
- 管理员自己那一行没有“重置密码”；普通成员直接调用重置接口得到 403。
- 结束时停用这个成员（没有删除接口）。

候选不做真实服务用例，见第 8 节的裁决记录。

---

## 6. 兼容与回滚

- **无迁移**：不加表、不加列、不加约束。
- **旧接口不变**：
  - `PATCH /users/{id}`、`sessions/revoke`、登录、初始化的请求、响应、错误码都不变；
  - `GET /artist-candidates` 不带 `ignored` 参数时，结果和 SQL 都与现在相同；
  - `candidate-ignore` 不改。
- **进程入口**：不带参数时行为不变；以前带参数启动时参数会被忽略，现在会被当成子命令。仓库里的启动脚本都不带参数。
- **依赖**：新增 `golang.org/x/term v0.45.0` 与 `golang.org/x/sys v0.47.0`，`go` 版本不变。
- **回滚**：按提交 revert 即可。
  - 没有数据结构变化，旧版本的二进制可以直接用当前的库。
  - 回滚之前通过新接口或命令行改过的密码仍然是 bcrypt 哈希，旧版本照常校验。
  - 回滚后，新增的样本文件和前端映射一起被撤回，往返测试保持一致。

---

## 7. A5 文档与规范同步清单（收尾时做）

- `.trellis/spec/backend/identity-contracts.md`：
  - §2 接口签名补上两个新接口；
  - §3 补自助改密码和重置的规则（K2、对自己调用返回 403、停用的成员也能重置、不带幂等键）；
  - §4 错误矩阵补 1.1 和 1.2 的条目；
  - §6 必需测试补 5.1 和 5.2；
  - 加一节“命令行恢复”。
- `.trellis/spec/backend/artist-contracts.md`：写明 `ignored` 参数、已合并的不出现、按最近忽略排序、恢复沿用 `ignored:false`。
- `.trellis/spec/backend/directory-structure.md`：
  - `cmd/roomusic` 多了 `reset_password.go` 子命令；
  - `platform/postgres` 的 `Connect` 与 `Open` 分工；
  - B0 技术债一段的措辞（3.3）。
- `.trellis/spec/frontend/session-lifecycle.md`：改密码成功后不增代次、不清缓存、不广播；当前密码错误返回 403，不结束会话。
- `.trellis/spec/frontend/governance-components.md` 与 `artist-guidelines.md`：候选页的“待处理 / 已忽略”。
- `docs/architecture/core-1-roadmap.md:34`：成员一行补上“改密码、重置密码、命令行恢复”。
- `README.md`：
  - 新增“忘记密码”一节：成员找管理员在成员页重置；管理员在服务器上执行 `./scripts/reset-password.sh <用户名>`（或等价命令）。
  - 写明这条命令读同一个 `.env`、会让该账号在所有设备上退出登录、不需要停服务。

---

## 8. 裁决记录（qlqq，2026-11-05）

1. 候选的真实服务用例：选 (b)，不做真实浏览器用例；V4 由 Go 集成测试与模拟 Playwright 覆盖，验收记录写明原因（PRD K6）。
2. 新增 `scripts/reset-password.sh`（PRD K4）。
3. 已忽略列表按最近忽略的在前（PRD K5），按 1.3 的排序键实现。

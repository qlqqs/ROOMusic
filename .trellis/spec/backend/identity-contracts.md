# 身份与成员合同

## 1. 范围与触发

当代码涉及初始化管理员、登录与当前用户（查看者）响应、成员创建/停用/会话撤销、改密码与重置密码、命令行恢复密码、密码规则，以及这些接口的写入失败映射时，
遵守本合同。请求与响应的通用规则（错误信封、JSON 请求体、分页）、HTTP 缓存与数据库迁移见
[Core 0 当前运行合同](./core0-runtime-contracts.md)；目录新增与停用、恢复的幂等见 [目录与扫描流程合同](./library-scan-contracts.md)。

## 2. 接口签名

- 用户更新请求：`{ "disabled": boolean }`；响应：`{ "disabled": boolean }`。
- 查看者：初始化管理员（`POST /setup/admin`，201）、登录（`POST /auth/login`）与当前用户（`GET /auth/me`）三处响应同形，
  `{ "id": UUID, "username": string, "role": "admin" | "user" }`。前端用 `id` 与 `role` 判断是否同一查看者、成员表里哪一行是“你”，
  用户名只用于显示。
- 自助改密码（2026-11-05）：`POST /auth/password`，请求 `{ "current_password": string, "new_password": string }`，
  响应 `200 { "password_changed": true }`，不重新设置 Cookie。“再输入一次”只在前端比较。
- 管理员重置密码（2026-11-05）：`POST /users/{id}/password`，请求 `{ "password": string }`，响应 `200 { "password_reset": true }`。
  用独立端点，不扩展 `PATCH /users/{id}`（那里只接受 `disabled`）。

## 3. 合同

### 身份与成员接口

- 初始化管理员与新建成员的密码只允许 12–72 位可打印 ASCII（U+0020–U+007E，含空格），由
  `identity.ValidPassword` 与 `MinPasswordLength`、`MaxPasswordLength` 统一校验。字符数与字节数一致，且不超过 bcrypt 的 72 字节上限。
  不合规返回 `400 invalid_input`，不调用 bcrypt、不写库；提示“用户名不能为空，密码需要 12 到 72 位英文字母、数字或符号”。登录不检查新密码规则，兼容已有账号。
- 写入失败只按 PostgreSQL 唯一约束冲突（`*pgconn.PgError`，SQLSTATE `23505`）判断“已存在”：新建成员映射 `409 username_taken`，
  初始化管理员（插入用户、写入初始化标记或提交）映射 `409 setup_closed`。其他数据库错误一律 `503 database_unavailable`，
  不伪装成冲突。
- `PATCH /users/{id}` 与 `POST /users/{id}/sessions/revoke` 先校验路由 ID 是 UUID，否则 `400 invalid_id` 且不访问数据库；
  撤销未知用户的会话返回 `404 not_found`，不再对任何 ID 都返回 200。
- `GET /users` 读某一行或遍历失败时返回 `503 database_unavailable`，不静默跳过：列表不能少人而不报错。
- 401 的 message 一律是“需要登录”，与角色无关（403 的“需要管理员权限”不变）；前端仍按错误码选文案。
- 成员写操作（创建、停用/启用、撤销会话）没有 `Idempotency-Key`，也不写操作记录。F3 裁决（2026-10-02）把它延到 M3 决定；M3 裁决（G6，2026-10-13）
  **不纳入 M3**（见 [ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md) 与[受治理操作合同](./governance-contracts.md)第 11 节）：
  重复创建已被用户名唯一约束挡住，家庭场景价值低，接入会牵动身份接口里 SQL 的抽取，单独做更好审查。原计划作为独立小任务接入，
  2026-11-03 的路线调整（R4，只有一个管理员）撤销该任务，不再做。现状：停用/启用设定绝对状态，重复执行
  结果相同；撤销会话重复执行只是再撤销一次；重复创建得到 `409 username_taken`，不会建出第二个账号。前端对结果不明的成员操作
  不重试，刷新列表让管理员核对。
- `Guard.Usernames(ctx, ids)`（M3）批量返回用户 ID（小写 UUID）到用户名，不存在的用户没有键；操作记录页用它显示“谁”，只返回用户名，不返回角色或其他字段。

### 改密码与重置密码（2026-11-05）

- 业务都在 `identity/passwords.go`：`ChangeOwnPassword`、`ResetPassword`、`ResetPasswordByUsername` 共用 `setPasswordTx`；
  handler（`identity/httpapi/passwords.go`）里没有 SQL。新密码沿用 `ValidPassword`，不合规时不调用 bcrypt、不访问数据库；
  bcrypt 都在事务外完成，不在持有行锁时做慢计算。
- 自助改密码：当前会话保留（令牌与到期时间不变），本人其他会话全部撤销。事务内先锁用户行、再锁当前会话行，
  锁内的哈希和事务外读到的不同就按当前密码错误处理，两个并发请求最多成功一个。新密码可以和当前密码相同（效果是退出其他设备）。
- 当前密码错误返回 `403 current_password_incorrect`，不用 401：前端请求层把 `401 unauthorized` 当作会话结束，
  而输错当前密码不代表掉线（见[会话生命周期规范](../frontend/session-lifecycle.md)）。
- 管理员重置：撤销该成员的全部会话；停用的成员也可以重置，`disabled_at` 不动；不锁管理员集合（不改变启用管理员数量）。
  对自己调用返回 `403 self_password_reset`：改自己的密码必须验证当前密码，被盗用的管理员会话不能把管理员锁在门外。
  比较用 `strings.EqualFold`，路由里的 UUID 可能是大写。
- 两个接口同成员写操作一样不带 `Idempotency-Key`、不写操作记录（R4）。前端对结果不明的改密码与重置不自动重试。

### 命令行恢复（2026-11-05）

- `roomusic reset-password <用户名>`（`cmd/roomusic/reset_password.go`，运维入口 `scripts/reset-password.sh`，读取同一个 `.env`）。
  不带参数启动时仍是服务；`help`、`-h`、`--help` 输出用法；其他参数是未知命令。
- 只读 `config.DatabaseURL()`，只调用 `postgres.Connect`（连接与 ping，不迁移、不初始化目录、不恢复扫描、不启动 HTTP）。
- 终端里隐藏输入、输入两次；不在终端时从 stdin 读一行（最多 4 KiB，只去掉末尾换行，不 `TrimSpace`）。读密码不计时；
  读完并校验后，连库与重置共用 30 秒超时。用户名精确匹配，与登录一致。成功后该账号全部会话撤销，停用状态不变。
- 退出码：成功 0；参数错误或未知命令 2；读不到密码、两次不一致、密码不合规、缺少 `ROOMUSIC_DATABASE_URL`、连不上数据库、
  用户不存在、其他数据库错误都是 1；被中断 130，先恢复终端状态。输出只用固定文案，不打印底层错误，不出现密码和数据库地址。
- 包装脚本先编译到临时目录再运行，原样透传退出码（`go run` 会把 2 改成 1）。

### 用户停用事务

- 用户禁用/启用在同一事务中锁定启用管理员集合和目标用户，检查最后管理员，
  更新 `disabled_at`，并在禁用时撤销该用户未撤销 session。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 用户目标不存在 | `404 not_found`，事务回滚 |
| 禁用最后一个启用管理员 | `409 last_admin`，不改用户或 session |
| 用户 SQL/事务失败 | `503 database_unavailable` 或分类错误，不返回成功 |
| 初始化管理员或新建成员时用户名为空白，或密码不在 12–72 位范围内，或包含可打印 ASCII 之外的字符 | `400 invalid_input`，文案说明长度与字符范围；不调用 bcrypt，不写库 |
| 新建成员的用户名唯一约束冲突（SQLSTATE `23505`） | `409 username_taken` |
| 新建成员的其他数据库错误（触发器异常、约束以外的失败） | `503 database_unavailable`，不返回 409 |
| 初始化管理员：已初始化，或插入/写标记/提交时的唯一约束冲突（另一次初始化抢先） | `409 setup_closed` |
| 初始化管理员的其他数据库错误 | `503 database_unavailable`，不返回 409 |
| `PATCH /users/{id}`、撤销会话的路由 ID 不是 UUID | `400 invalid_id`，不访问数据库 |
| 撤销未知用户的会话 | `404 not_found`，不是 200 |
| `GET /users` 读某一行或遍历失败 | `503 database_unavailable`，不返回少了人的列表 |
| 改密码或重置时新密码不合规 | `400 invalid_input`“新密码需要 12 到 72 位英文字母、数字或符号”；先于核对当前密码，不调用 bcrypt，不写库 |
| 自助改密码：当前密码错误，或并发中已被别处改过 | `403 current_password_incorrect`，不写库，会话全部保留 |
| 自助改密码：没有会话、会话失效、用户停用（含事务进行中被撤销或停用） | `401 unauthorized`，回滚 |
| 重置：普通成员 / 目标是自己 | `403 forbidden` / `403 self_password_reset`，不写库 |
| 重置：路由 ID 不是 UUID / 用户不存在 | `400 invalid_id`，不访问数据库 / `404 not_found`，回滚 |
| 改密码或重置：bcrypt 生成失败 / 其他数据库错误 | `500 internal_error` / `503 database_unavailable`，密码与会话都不变 |

## 5. 正确、基准与错误案例

- 正确：管理员状态变更锁定同一组管理员行，session 撤销与 `disabled_at` 一起提交；
  两个并发禁用请求至多成功一个。
- 错误：忽略 `Exec` 错误后返回 200。

## 6. 必需测试

- 身份与成员（F3，`internal/app/identity_api_integration_test.go`，PostgreSQL）：三处查看者响应带 `id` 且与成员列表一致；
  初始化管理员与新建成员只把唯一约束冲突映射为 409（用测试库里的触发器注入 `RAISE EXCEPTION`，SQLSTATE 是 `P0001` 而不是
  `23505`，必须得到 503 且没有部分写入）；密码 11/12/72/73 位 ASCII 的边界；非 UUID 与未知用户 ID；`listUsers` 遇到读不了的行
  返回 503；401 文案与角色无关；非法 JSON 只写一个信封。`identity/password_test.go` 另测中文、全角、emoji 与控制字符（含制表符、换行）拒绝，
  空格与各种符号接受，以及 72 位 ASCII 与 bcrypt 的上限一致。
- 改密码与重置（2026-11-05，同一文件）：当前会话保留、其他会话撤销、旧密码失效；当前密码错误是 403 且不改任何东西；
  11/73 位与中文新密码不写库；原子性与并发只成功一次；重置撤销全部会话、普通成员 403、非 UUID 400、未知用户 404、对自己 403、
  停用成员重置后仍停用。接口样本 `auth/password-changed`、`users/password-reset` 与三个错误样本在 `api_samples_integration_test.go`。
- 命令行（`cmd/roomusic/reset_password_test.go`）：依赖注入覆盖退出码 0/1/2/130 与固定文案；隔离库上重置后旧会话失效、新密码能登录；
  用户不存在与密码不合规时数据不变；输出不含密码和数据库地址；首尾空格保留。`platform/postgres` 测 `Connect` 不迁移。

## 7. 错误与正确示例

### Wrong

```go
_ = db.ExecContext(ctx, "UPDATE users SET disabled_at=NOW() WHERE id=$1", id)
writeJSON(w, 200, map[string]bool{"disabled": true})
```

这会吞掉数据库错误，也没有最后管理员保护或 session 原子撤销。

### Correct

```go
tx, err := db.BeginTx(ctx, nil)
// lock enabled admins and target, validate last-admin invariant
// update user and revoke sessions in the same tx
if err == nil {
    err = tx.Commit()
}
if err != nil {
    _ = tx.Rollback()
    writeAPIError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法更新用户")
    return
}
writeJSON(w, http.StatusOK, map[string]bool{"disabled": requested})
```

前端的用户更新响应同样必须经过所属 feature 的唯一 decoder（`features/members/api/decoders.ts`），禁止把 `unknown`
直接 cast 成 `UserDTO`。

### 写入失败与请求体错误

错误：把任何插入失败都当成“已存在”，数据库故障就变成了“用户名已存在”；`DecodeJSON` 已经写出 `invalid_json` 之后，
同一个表达式又写了第二个错误信封，一个响应里有两个 JSON 文档。

```go
if !httpserver.DecodeJSON(w, r, &in) || strings.TrimSpace(in.Username) == "" {
	httpserver.WriteError(w, r, 400, "invalid_input", "...") // 解码失败时是第二次写
	return
}
if _, err := db.ExecContext(ctx, insertUser, id, in.Username, hash); err != nil {
	httpserver.WriteError(w, r, 409, "username_taken", "用户名已存在")
	return
}
```

正确：解码与校验分两步；只认 SQLSTATE `23505`，其余如实报告为不可用。

```go
if !httpserver.DecodeJSON(w, r, &in) {
	return
}
if strings.TrimSpace(in.Username) == "" || !identity.ValidPassword(in.Password) {
	httpserver.WriteError(w, r, 400, "invalid_input", invalidAccountMessage)
	return
}
if _, err := db.ExecContext(ctx, insertUser, id, in.Username, hash); err != nil {
	if isUniqueViolation(err) { // errors.As(*pgconn.PgError) 且 Code == "23505"
		httpserver.WriteError(w, r, 409, "username_taken", "用户名已存在")
		return
	}
	httpserver.WriteError(w, r, 503, "database_unavailable", "无法创建用户")
	return
}
```

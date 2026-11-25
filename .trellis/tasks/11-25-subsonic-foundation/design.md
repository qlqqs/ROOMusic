# Subsonic 鉴权与协议骨架：设计

## 数据

- **迁移 `00xx_subsonic.sql`**：
  - 实例设置 `subsonic_enabled boolean NOT NULL DEFAULT true`（新装与升级的实例都默认开启）。放进现有的实例级设置表；没有合适的表就新建 `subsonic_settings` 单行表。实现前先查现有实例设置的存放方式，沿用它。
  - `subsonic_credentials(user_id uuid PRIMARY KEY REFERENCES users ON DELETE CASCADE, sealed bytea NOT NULL, updated_at timestamptz NOT NULL)`。
- **加密**：
  - 复用实例密钥 `steward/secret.Store`（`<数据目录>/steward/instance.key`，AES-GCM，按 userID 与用途绑定附加数据），用途为 `"subsonic-password"`；
  - 把 `internal/steward/secret` 原样移到 `internal/platform/secret`，只改导入路径，不改行为和密钥路径，供 `identity` 与 `subsonic` 共用；
  - 解密失败（实例密钥换了）视同没有副本。

## 写入副本的时机

- **`identity` 新增一个回调接口 `PasswordObserver`**，在以下时机调用：密码校验成功（`Login`）、本人改密、管理员创建用户、管理员重置密码。`identity` 不直接依赖 `subsonic`。
- **`subsonic` 实现这个接口**：
  - 实例开启时，把密码 Seal 后 upsert 进副本表；
  - 关闭时什么都不做；
  - 管理员重置密码时，先删除旧副本，再按新密码写入。
- **新增 `POST /api/v1/me/subsonic/confirm-password`**（同源、已登录）：
  - 请求体 `{password}`；
  - 校验走 `identity` 的密码校验与限流，成功后写入副本，返回 204；
  - 密码错误返回 `invalid_credentials`。
- **`GET /api/v1/me/subsonic`**：返回 `{enabled, ready, username}`。`ready` 表示副本存在且能解密。
- **`GET`/`PUT /api/v1/admin/subsonic`**：`{enabled}`。从开启改为关闭时，在同一事务里删除全部副本。

## 校验流程（`subsonic/auth`）

1. 解析 `u`、`v`、`c`，以及 `p` 或 `t`+`s`；缺少任何一个返回 10。
2. 实例未开启时返回 40。
3. 按用户名找用户；找不到、已停用都返回 40，与密码错误不可区分。限流先于密码比较。
4. `p`：
   - 有 `enc:` 前缀时按 hex 解码；
   - 用 `identity` 现有的密码校验（bcrypt，限流计数）。
5. `t`+`s`：
   - 解密副本，计算 `md5(密码+s)`，与 `t` 小写后做常量时间比较；
   - 没有副本或解密失败时返回 40，计入失败次数。
6. 成功后把 `identity.User` 放进请求上下文，供后续处理器使用。

不建会话，不写 cookie；`/rest` 不做同源检查。

## 前端

- **`features/subsonic`**：
  - 账号设置组：
    - 一行“第三方客户端”：显示服务器地址（当前 origin 加 `/`）和用户名，各带复制按钮；
    - 有状态标签“已就绪”或“需确认密码”；需确认时，用小表单输入密码并提交 confirm-password。
  - 管理设置组：“Subsonic 兼容接口”开关，带说明；关闭前确认一次。

## 风险

- **可还原的密码副本扩大了数据库泄露的影响面。** 缓解：管理员可关闭，关闭即删除；实例密钥放在数据目录，不进数据库；管理设置里明确说明。
- **限流**：bcrypt 每次约 50ms。客户端用 `p=` 时每个请求都要校验一次，扫描封面会发很多请求，会拖慢服务器。
  - 对策：成功校验的结果按 (用户, 密码哈希的 SHA-256) 在进程内缓存 5 分钟；
  - 改密、停用、关闭开关时清空缓存；
  - `t+s` 每次的 salt 不同，不缓存，但它只做 md5，代价很低。

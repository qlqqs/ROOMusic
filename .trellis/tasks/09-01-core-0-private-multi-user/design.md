# Core 0 私有多用户技术设计

## 架构边界

- 新增数据库迁移，为 `users` 增加 `role`、`disabled_at`，为 sessions 保持 token hash 存储并增加按用户撤销所需索引。
- 认证上下文返回用户 ID、用户名和角色；认证查询同时过滤过期、撤销和禁用用户。
- 在 HTTP handler 层提供 `requireAuthenticated` 与 `requireAdmin`，管理端点统一使用后者；共享查询端点使用前者。

## API 与数据流

- `GET /api/v1/auth/me` 返回 `username` 与 `role`。
- 管理员端点：`GET/POST /api/v1/users`、`PATCH /api/v1/users/{id}`（启用/禁用）和 `POST /api/v1/users/{id}/sessions/revoke`。
- 用户列表 DTO 仅返回 id、username、role、disabled、created_at，不返回密码哈希、原始路径或诊断详情。
- 登录查询拒绝 disabled 用户；禁用操作在事务内更新用户并将其 sessions 设置 `revoked_at`。

## 前端

- 会话状态保存角色；管理员显示用户管理、目录注册和扫描控件，普通用户只显示浏览、搜索与详情。
- 管理操作失败显示 API 错误消息；成功后刷新用户列表与当前会话。

## 兼容性、迁移与回退

- 迁移为既有用户填充 `admin`，保证单管理员环境可升级；未知角色默认拒绝管理权限。
- 旧客户端忽略 `role` 字段仍可浏览；服务端授权不依赖前端。
- 回退仅需停止使用新增端点并回滚应用版本；数据库迁移保持向前兼容，不删除现有字段。

## 风险

- 自助禁用当前管理员会造成锁定风险，因此端点禁止禁用最后一个启用管理员。
- 并发禁用/启用使用事务和条件更新，测试覆盖重复请求和会话即时失效。

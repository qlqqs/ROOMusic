# Session Lifecycle

本文从[状态管理规范](./state-management.md)的 `## Session Lifecycle` 一节原样移出（2026-11-05，控制单文件体积），内容未改；状态归属的通则仍以状态管理规范为准。

会话控制器（`features/auth/session/session-controller.ts`）是普通 TypeScript 对象，状态为
`checking | unavailable | setup_required | anonymous(notice) | authenticated(viewer, generation)`，所有转换串行执行，登录与
退出不会交错。浏览器端没有 token：凭据只在 HttpOnly Cookie 与后端会话里，不进入 localStorage、sessionStorage、IndexedDB、
URL、查询键或全局 store。

- **启动**：先查 `GET /setup/status`，需要初始化则进入 `/setup`，否则用 `GET /auth/me` 确认会话；启动失败（如 503）显示可重试的
  整页错误。不从本地角色快照恢复私有页面。
- **代次**：进入 `authenticated` 与离开它都使代次递增。查看者是 `{ id, username, role }`：`/auth/me`、登录与初始化管理员的响应都带用户 `id`
  （F3，2026-10-02），“同一查看者”按 `id` 与 `role` 判断（`sameViewer`），用户名只用于显示；成员表标出“你”、停用与撤销时判断目标是否为自己，
  同样按 `id`。
- **清理顺序**：离开已登录状态或查看者变化时，`cancelQueries()` → `clear()`（查询与写操作缓存）→ `router.invalidate()`
  重新执行守卫 → 导航。三重防护避免旧身份的数据回流：查询键带代次；已登录布局以代次为 `key` 重新挂载（丢弃表单、对话框等私有
  状态）；写操作回调检查代次。
- **401 与 403**：`session: "required"`（默认）的请求发出时读取代次，401 `unauthorized` 到达时带回，控制器忽略属于旧会话的 401，
  旧会话的请求不能结束新登录的会话。初始化状态、初始化与登录接口标为 `"none"`，登录的 401 `invalid_credentials` 不是会话失效。
  403 只拒绝当前操作或页面，不结束会话。
- **跨标签页**：`BroadcastChannel("roomusic:session")` 在登录、退出、会话失效后只广播 `{ type: "changed" }`（不含凭据与用户
  数据）；其他标签页收到广播，以及页面重新可见且距上次核对不少于 30 秒时，用 `/auth/me` 核对：同一查看者（`id` 与 `role` 都相同）不处理，用户不同、角色变了或 401
  时按上文清理。浏览器不支持 BroadcastChannel 时只依赖重新可见时的核对。广播不构成授权，后端继续鉴权每个请求。
- **退出**：`POST /auth/logout` 由后端确认后才清理；失败时留在当前页并提示重试，不假装已退出。
- **回跳**：登录成功后进入 `redirect` 指定的站内路径，否则进入概览。在页面里主动退出时登录页**不带**回跳（下一个登录的人
  不应被送到上一个人停留的页面）；重新载入后（没有“刚退出”的记忆）直接访问受保护页，或会话被撤销后刷新，都会带回跳。
- **初始化**：成功后用同一凭据自动登录；自动登录失败则回到登录页并预填用户名，提示“管理员已创建，请登录”。

Production requests are same-origin with the Go server. Development uses the
documented proxy/credentials setup, not a second auth scheme.

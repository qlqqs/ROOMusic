# ROOMusic Core 0 技术设计

## 1. 设计目标与边界

Core 0 是一个单体 Go 后端加 React/TypeScript 前端的最小可运行音乐库。生产环境由 Go 进程同时托管 REST API 和前端静态资源；运行时只要求 PostgreSQL。Redis、Meilisearch、GraphQL、Agent runtime 和文件写入工具留给后续阶段。

设计优先级：先保证本地音乐数据可读、来源可解释、扫描失败不误删、权限可撤销、核心操作可测试；不为未来 Agent 或完整治理系统提前建立抽象。

## 2. 模块边界

建议采用单仓库、清晰分层的 Go 结构：

- `cmd/roomusic`：进程入口、配置加载、数据库连接、HTTP 服务组装。
- `internal/config`：环境变量和配置校验，包括 `allowed_library_roots`、数据库 URL、监听地址和 data 目录。
- `internal/auth`：setup、用户、opaque session、Cookie、CSRF/Origin 检查和角色授权。
- `internal/library`：领域模型、扫描编排、Release Graph 规则和 scan run 生命周期。
- `internal/scanner`：只读目录遍历、符号链接规则、音频标签/CUE 解析、封面发现和来源观察。
- `internal/database`：迁移、事务、repository 和 PostgreSQL 查询；不把扫描或 Agent 流程塞进 repository。
- `internal/operations`：Change Set、Operation Journal、resource revision、幂等和目录配置恢复。
- `internal/httpapi`：版本化 REST handler、请求解析、响应错误契约和静态资源托管。
- `web/`：React/TypeScript 应用、页面、API client 和前端状态。

Core 0 只实现必要边界；未来 Assistant/Steward/Operator runtime 复用 `operations` 与统一后端工具执行边界，但不在本阶段加入模型、Review Subagent、模型 gateway 或 Operator runtime。三种模式只改变审批路径，不创建三套执行权威：Assistant 等待当前用户批准，Steward 绑定独立 Review Subagent 的结构化审查结果，Operator 跳过审批但仍执行管理员鉴权、工具白名单、参数/范围/revision/幂等校验以及 Journal 记录。

## 3. 数据模型与数据流

### 3.1 权威数据

PostgreSQL 保存：

- 单一 setup 状态和管理员用户。
- 用户、角色、禁用状态、会话 token hash、过期时间和撤销时间。
- `library_roots` 及其启用状态、revision 和配置操作关联。
- `scan_runs`、每个 root 的扫描状态、诊断和统计。
- `release_groups`、`releases`、`media`、`tracks`、文件来源和字段来源观察。
- release-level artwork 元数据及 data 目录的 storage key、hash、MIME 和尺寸。
- `change_sets`、operation items/events、before/after、幂等键和恢复引用。

大图片二进制不进入 PostgreSQL，音乐目录保持只读；封面副本只写入 ROOMusic 管理的 data 目录。

### 3.2 扫描流程

```text
管理员注册 root
  -> root allowlist + realpath 校验
  -> 创建或复用全局 scan_run
  -> 按稳定顺序串行扫描 roots
  -> 目录遍历与标签/CUE/封面解析
  -> 在事务中幂等保存 Release Graph 与 source observations
  -> 记录 root 状态和诊断
  -> 所有 roots 完整成功时执行 missing 对账
  -> scan_run 完成
```

一个注册发行目录默认创建一个 ReleaseGroup 和一个 Release；目录中的多碟结构创建多个 Medium。跨目录相似项只可记录非权威诊断，不改变图谱关系。

扫描状态必须区分 `running`、`succeeded`、`failed`、`canceled` 和 `incomplete`。只有全局扫描满足完整成功门槛时才允许将未见来源标记为 `missing`。失败、取消、离线、权限错误或遍历不完整不得执行负向对账。

同一 root、同一规范化相对路径的来源重扫保持 Track ID；rename/move 不自动继承旧 ID。目录符号链接默认不跟随；同根合法文件符号链接可读取，越界、断链和循环风险记录诊断。

### 3.3 查询流程

```text
Browser Cookie session
  -> REST auth middleware
  -> role/scope check
  -> read repository
  -> PostgreSQL query
  -> response DTO
```

普通用户只获得音乐库展示数据；管理员额外获得 root、scan diagnostics 和 change history。响应 DTO 不暴露原始服务器路径、session token、数据库信息或内部凭据。

## 4. REST API 合同

统一前缀为 `/api/v1`，错误响应使用稳定结构，例如：

```json
{
  "error": {
    "code": "invalid_library_root",
    "message": "The library directory is outside the allowed roots.",
    "request_id": "..."
  }
}
```

首批资源：

- `GET /api/v1/setup/status`
- `POST /api/v1/setup/admin`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/me`
- `GET/POST /api/v1/users`、`PATCH /api/v1/users/{id}`（管理员）
- `GET/POST /api/v1/library-roots`、`POST /api/v1/library-roots/{id}/disable`、`POST /api/v1/library-roots/{id}/restore`
- `POST /api/v1/scans`、`GET /api/v1/scans/{id}`、`GET /api/v1/scans/{id}/diagnostics`
- `GET /api/v1/releases`、`GET /api/v1/releases/{id}`、`GET /api/v1/search`
- `GET /api/v1/artwork/{resource-id}`
- `GET /api/v1/change-sets`、`GET /api/v1/change-sets/{id}`（管理员）

Core 0 不提供 metadata mutation、Agent tool call、GraphQL、任意路径浏览或文件写 API。

## 5. 会话和权限

初始化接口只在数据库尚未完成 setup 时开放，并在同一事务中创建唯一管理员和 setup completion 状态。登录生成高熵随机 token，Cookie 保存原 token，数据库只保存 hash。Cookie 使用 `HttpOnly`，生产配置使用 `Secure`，设置明确的 `SameSite`；状态变更请求执行同源/Origin 检查或等价 CSRF 保护。

权限最小化为 `admin` 和 `user`：admin 管理 root、扫描、用户和完整诊断；user 只能浏览和搜索。禁用用户或撤销 session 后，每次请求重新检查数据库状态。

## 6. 变更管理设计

目录配置新增、停用和恢复使用统一的 Change Set 服务：

1. 在事务中读取当前资源和 revision。
2. 检查 idempotency key；同 key 同 payload 返回原结果，不同 payload 返回冲突。
3. 写入 operation planned/running、before/after 和 resource revision。
4. 提交目录配置变化及操作事件。
5. 恢复动作使用 recorded before state 和 expected revision；期间有新修改则 fail closed。

扫描的派生数据不创建逐字段 Change Set，而使用 scan run 与 source observation 解释。Core 0 不实现文件 quarantine、tag backup、文件逆操作或完整 Event Sourcing。

## 7. 前端设计

开发环境使用 Vite 或等价 React dev server，通过代理访问 Go `/api`；生产构建输出静态资源，由 Go `embed` 或静态目录托管。页面最小集合：setup/login、library list、release detail、scan status/diagnostics、admin users/roots 和 change history。

前端 API client 只使用 Cookie session，不把认证 token 写入 localStorage。页面必须处理 loading、empty、error 和权限拒绝状态；metadata 和 artwork 展示使用 API DTO，不直接读取服务器文件。

## 8. 回滚、失败和可观测性

- 数据库迁移在启动前执行并可重复；迁移失败阻止服务宣称 ready。
- 扫描事务失败只回滚当前保存单元，不触发缺失对账。
- 全局扫描通过进程内互斥和 PostgreSQL 状态避免重复运行；进程退出后的运行恢复属于后续持久化队列能力，Core 0 至少把未完成 run 标记为可诊断的 incomplete。
- 每个 HTTP 请求创建 `request_id`；扫描和变更分别带 `scan_run_id`、`operation_id`，输出 `slog` JSON。
- 日志只记录事件、状态、计数、错误分类和关联 ID，不记录密码、token、数据库 URL 或完整原始路径。
- 只读音乐目录不需要文件恢复；封面 data 文件按 hash key 存放，可安全重建。

## 9. 主要取舍

- PostgreSQL 查询替代 Meilisearch：初代依赖少，代价是大库搜索性能需要后续基准验证。
- 进程内串行扫描替代 Redis/asynq：一致性和实现清晰，代价是吞吐有限。
- REST 替代 GraphQL：调试和契约简单，代价是复杂图谱查询以后可能需要新增查询层。
- opaque Cookie session 替代 JWT：立即撤销更简单，代价是未来独立客户端需要额外认证方案。
- 保守身份和 ReleaseGroup 分组替代启发式合并：避免误合并，代价是首版不会自动识别跨目录版本关系。

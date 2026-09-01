# Core 0 首个可浏览纵向切片技术设计

## 1. 目标与设计边界

本子任务建立一个最小但完整的模块化单体：Go 后端、React/TypeScript 前端和 PostgreSQL 共同完成 setup、登录、目录注册、FLAC/MP3 扫描、Release 列表与详情浏览。设计只创建当前闭环需要的模块与接口，不创建 search、artwork、operations、Agent 或 plugin 基础设施。

## 2. 工程与模块结构

后端以 `backend/` 为根：

- `cmd/roomusic`：进程入口。
- `internal/app`：唯一 composition root、路由和生命周期组装。
- `internal/platform`：配置、HTTP server、PostgreSQL、迁移和结构化日志机制。
- `internal/identity`：setup、管理员凭据、session 与认证中间件。
- `internal/library`：allowed-root 注册、路径安全、scan run、遍历、FLAC/MP3 parser 与诊断。
- `internal/catalog`：Release Graph、来源、missing 对账和列表/详情读模型。
- `migrations`：显式有序迁移。

前端以 `frontend/` 为根，仅创建 `auth`、`library`、`catalog` 和必要的 `app/shared` 边界。初始 scaffold 必须记录包管理器、Vite 或等价构建工具、router、REST 数据机制、runtime decoder、样式方案和测试工具；这些实现工具在 Phase 2 开始前由实现者基于当前工具链选择并写入 spec，不从 V0 静默继承。

## 3. 权威与依赖方向

```text
REST transport -> capability application -> domain + consumed ports
                                             ^
                                             |
                         PostgreSQL/filesystem adapters
```

- HTTP 只负责解码、认证上下文、调用 use case 和映射响应。
- identity、library 和 catalog 分别拥有自己的规则与数据写入。
- library 通过 catalog 发布的窄写入合同提交扫描观察，不直接写 catalog 私有表。
- composition root 注入 PostgreSQL、filesystem、clock、ID 和 logger；业务代码不查询全局 registry。

## 4. 最小数据模型

第一组迁移只建立当前闭环需要的表族：

- identity：setup 状态、users、sessions。
- library：library roots、scan runs、每个 root 的 scan 状态、bounded diagnostics。
- catalog：release groups、releases、media、tracks、track sources、关键字段 observations。

精确表名、主键类型、迁移工具和 PostgreSQL driver 在首次实现评审时固化。数据库必须保证：唯一 setup 完成状态、唯一初始管理员约束、session token hash、同 root/relative path 的来源唯一性、Release Graph 外键完整性以及有界列表所需索引。

扫描不得在遍历目录或读取标签期间持有长事务。每个稳定批次保存观察，扫描最终状态在事务中决定是否允许 missing 对账。

## 5. 身份与 HTTP 安全

setup 在事务中检查尚未完成、创建管理员并关闭 setup。登录生成高熵随机 token，Cookie 只携带原 token，数据库保存单向 hash。每次受保护请求检查 session 未过期且未撤销。

状态变更端点执行同源/Origin 校验。Cookie 的生产 `Secure` 由明确的运行环境配置控制；服务拒绝不安全的矛盾生产配置。统一错误 mapper 输出安全的 `/api/v1` error envelope 与 `request_id`。

## 6. 目录和扫描数据流

```text
POST library root
  -> admin session
  -> config allowlist
  -> clean + absolute + realpath containment
  -> persist safe root identity

POST scan
  -> return existing running scan or create one
  -> roots in stable order
  -> filesystem walk without following directory symlinks
  -> validate file symlink target in same allowed root
  -> FLAC/MP3 parser or bounded diagnostic
  -> catalog observation batches
  -> complete-success gate
  -> optional missing reconciliation
  -> terminal scan state
```

HTTP 请求接受扫描后，运行由进程生命周期拥有，不随客户端断开而取消。进程重启时遗留 `running` scan 被标记为 `incomplete` 并保留诊断；持久化任务恢复后移。

路径错误的外部响应与普通日志不得暴露 allowlist 或完整源路径。授权管理诊断可返回安全的相对路径与错误分类。

## 7. Release Graph 和来源规则

- 发行目录默认产生一个独立 ReleaseGroup 和 Release。
- 没有明确多碟结构时产生一个 Medium；有限、确定的常规 disc 子目录识别规则必须用 fixture 固化。
- 每个物理音频来源由 library root ID 与规范化相对路径定位。
- 同一来源再次观察时更新现有 Track 关联；新路径创建新来源和 Track，不做弱匹配继承。
- title、artist、album、track/disc number 仅在实际可解析时保存当前值与 observation 元数据；缺失值可用确定性 filename fallback，并标记 inferred。
- 不建立 Artist 实体治理、跨目录合并、overlay 或编辑 API。

## 8. REST 与前端合同

首批 REST 资源：

- `GET /api/v1/setup/status`
- `POST /api/v1/setup/admin`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/me`
- `GET /api/v1/library-roots`
- `POST /api/v1/library-roots`
- `POST /api/v1/scans`
- `GET /api/v1/scans/{id}`
- `GET /api/v1/scans/{id}/diagnostics`
- `GET /api/v1/releases`
- `GET /api/v1/releases/{id}`

列表必须稳定排序并显式分页。前端 API 边界对所有 REST payload 做一次 runtime decode，使用 Cookie credentials，不保存 token。scan status 可以有界轮询，切换页面或 terminal state 时停止。

## 9. 失败、回滚与可观测性

- 迁移或必要配置失败：进程不 ready。
- setup 事务失败：不留下半初始化用户。
- 单文件失败：记录 bounded diagnostic，继续其他文件。
- root 遍历不完整或关键错误：scan 为 incomplete/failed，不执行 missing。
- 批次 catalog 保存失败：本批回滚，scan 不进入完整成功状态。
- 产品代码可按工程、identity、library/catalog、frontend 四个检查点独立回退；迁移一旦共享，不修改历史文件而增加修正迁移。

日志使用稳定事件名和 request/scan/root ID，聚合优先；不逐 Track 输出 info 日志，不记录 token、数据库 URL 或完整 NAS 路径。

## 10. 主要取舍

- 首切片包含全栈闭环而不是仅做工程骨架：尽早验证真实用户价值和跨层合同，代价是任务仍较复杂，需要严格阶段门。
- 保留完整成功才 missing：这是数据安全底线，不能后移。
- 仅 FLAC/MP3：减少 parser 与 fixture 风险，同时证明扩展 seam。
- 暂不实现 search/artwork/multi-user/operations：避免读模型、图片存储和通用恢复抽象阻塞首个可浏览结果。

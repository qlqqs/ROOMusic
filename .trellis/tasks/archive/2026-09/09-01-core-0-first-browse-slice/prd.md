# Core 0 首个可浏览纵向切片

## Goal

在全新环境中建立 ROOMusic 的首个可用端到端纵向切片：用户可以创建唯一初始管理员并登录，注册允许范围内的只读音乐目录，扫描 FLAC/MP3 文件，并在 Web UI 中浏览最小 `ReleaseGroup -> Release -> Medium -> Track` 结构和安全的扫描状态。

## User Value

用户无需等待完整 Core 0 的所有格式、搜索、封面、普通用户和变更治理能力，即可验证 ROOMusic 最关键的本地音乐库价值：安全读取自己的音乐目录，并以发行版本结构浏览真实扫描结果。

## Confirmed Constraints

- 这是父任务 `08-31-roomusic-core-0-rebuild` 的首个实际实现子任务，后续子任务依赖其工程、迁移、身份、扫描和图谱合同。
- 生产运行时只依赖 PostgreSQL；Redis 和 Meilisearch 不参与启动、扫描或浏览。
- 产品 API 使用 `/api/v1` 版本化 REST；不建立 GraphQL 链路。
- React 开发使用独立 dev server；生产资源由同一个 Go 应用托管。
- 音乐目录只读；扫描器不得修改、移动或删除源文件。
- 只实现一个管理员账号，不实现普通用户创建或管理。
- 首批仅解析 FLAC 和 MP3；其他音频格式与 CUE 必须产生明确的 unsupported 诊断，不能静默导入。

## Requirements

### R1. 可启动工程

- 建立 Go 后端、React/TypeScript 前端、PostgreSQL 迁移与本地开发入口。
- 后端提供 readiness/health 行为；迁移失败时不得宣称 ready。
- 生产构建由 Go 同源提供 Web UI 与 `/api/v1`。

### R2. 单管理员初始化与会话

- 数据库未初始化时允许原子创建唯一管理员；成功后永久关闭公开 setup。
- 登录使用高熵 opaque token；Cookie 保存原 token，PostgreSQL 只保存 hash、到期和撤销状态。
- Cookie 使用 `HttpOnly`、明确 `SameSite`，生产配置使用 `Secure`；状态变更请求执行同源/Origin 检查或等价 CSRF 防护。
- 支持登录、登出、会话过期和撤销；浏览器代码不得读写 bearer token。

### R3. 受限目录注册

- 部署必须配置至少一个 `allowed_library_root`。
- 管理员只能直接输入并注册允许根或其真实子目录；本切片不提供服务器路径浏览器。
- 后端执行规范化、realpath 和 containment 校验，拒绝路径穿越、白名单外目录和越界符号链接。
- 目录符号链接默认不跟随；文件符号链接只有真实目标仍位于同一允许根时才读取。
- 本切片允许新增和列出 library root；停用、恢复、revision、幂等和操作审计由后续治理子任务处理。

### R4. FLAC/MP3 只读扫描

- 同一时刻只运行一个全局进程内 scan run；扫描中重复触发返回当前 `scan_run_id`。
- 多个已注册目录按稳定顺序串行扫描。
- 解析 FLAC、MP3 的基础标签和文件事实；解析失败、权限错误、断链和 unsupported 格式形成安全诊断并继续处理其他合法文件。
- 扫描状态至少区分 `running`、`succeeded`、`failed`、`canceled` 和 `incomplete`。
- 只有所有目标根完整成功时才能把本次未观察到的既有来源标记为 `missing`；其他状态不得执行负向对账。
- 同一 root 的同一规范化相对路径重扫保持 Track 身份；rename/move 形成旧来源 `missing` 与新来源，不自动继承旧 Track ID。

### R5. 最小 Release Graph 与来源

- 一个包含受支持音频的发行目录默认创建独立 ReleaseGroup 和 Release。
- 普通单碟目录创建一个 Medium；明确的常规多碟子目录可以创建多个 Medium，但本切片不解析 CUE 虚拟 Track。
- Track 关联本地来源，并为标题、artist、album、track/disc number 等实际支持的关键字段保存来源类型、推断状态、scan run 和观察时间。
- 不进行跨目录 ReleaseGroup 自动归组、metadata 人工编辑或文件写回。

### R6. REST 与 Web 浏览闭环

- 提供 setup status/setup admin、login/logout/me、library root 新增/列表、scan 触发/状态、Release 列表和 Release 详情所需的 REST 资源。
- 使用稳定错误结构和 `request_id`；普通浏览响应不暴露完整服务器原始路径。
- Web UI 覆盖 setup/login、目录注册、扫描触发与状态、Release 列表、Release 详情和登出。
- 页面覆盖 loading、empty、error、权限拒绝，以及扫描失败或不完整的可恢复展示。

## Acceptance Criteria

- [ ] 全新环境只启动 PostgreSQL 即可完成迁移、后端启动、前端开发和生产构建 smoke test；停止 Redis/Meilisearch 不影响闭环。
- [ ] 首次 setup 原子创建唯一管理员；第二次 setup 被拒绝，登录、登出、过期和撤销有自动化验证，数据库不保存明文 session token。
- [ ] Cookie 与状态变更请求满足约定的 HttpOnly、SameSite、生产 Secure 和 CSRF/Origin 防护。
- [ ] 允许根内目录可注册；路径穿越、白名单外路径、目录链接和越界文件链接被拒绝或形成诊断，合法同根文件链接不阻塞扫描。
- [ ] 代表性 FLAC 和 MP3 fixture 能形成 ReleaseGroup、Release、Medium、Track 和字段来源；unsupported 文件产生明确诊断。
- [ ] 同路径重扫保持 Track ID；rename/move 不错误继承身份。
- [ ] 完整成功扫描可以标记 missing 并在来源重新出现时恢复；失败、取消、离线、权限错误或不完整扫描不能标记既有来源 missing。
- [ ] 扫描中重复触发返回相同 `scan_run_id`；多个目录按稳定顺序处理。
- [ ] 管理员可以从 Web UI 完成 setup、登录、注册目录、触发扫描、查看状态、浏览 Release 列表和 Medium/Track 详情。
- [ ] REST 响应和结构化日志具有相关 ID，不泄露密码、token、数据库 URL或不必要的完整音乐路径。
- [ ] Go format/build/test/vet、前端 lint/typecheck/test/build、迁移集成测试和生产静态资源 smoke test 全部通过。

## Out of Scope

- OGG、Opus、WAV、CUE 和复杂多碟/CUE 变体。
- PostgreSQL 搜索、Meilisearch、封面、播放、歌词和 PWA。
- 普通用户创建、禁用和角色管理；本切片只有唯一管理员。
- library root 停用/恢复、revision、幂等键、Change Set、Operation Journal 和通用恢复执行器。
- Music Steward runtime、Review Subagent、三模式审批状态机、Tool Registry、Plugin Host 和 Capability Registry。
- metadata 人工编辑、tag write-back、rename/move/delete 和任意文件操作。

## Dependencies

- 无产品子任务前置依赖；这是后续 Core 0 子任务的工程和合同基础。
- 继承父任务的永久安全与产品不变量，但以本 PRD 的收窄范围作为本子任务验收权威。

## Planning Status

- Blocking open questions: none.
- 这是跨后端、前端、数据库和文件系统的复杂子任务，开始前必须完成 `design.md`、`implement.md` 和上下文清单，并通过用户最终规划审查。

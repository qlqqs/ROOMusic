# ROOMusic Core 0 初代核心重构规划

## Goal

作为 Core 0 的版本级父任务，从 `ROOMusic-V0` 继承已确认的产品目标、数据语义和安全边界，并通过可独立规划、实现和验收的子任务，在当前最小仓库中重新构建一个精简、完整、可运行的前后端初代核心。本任务维护源需求、子任务边界、跨任务不变量和最终集成验收，不直接作为产品代码实现目标。

## User Value

用户可以在自己的 NAS 或本地环境中启动 ROOMusic，注册音乐目录，安全地进行只读扫描，并通过 Web 前端浏览以发行版本为中心的音乐库。系统应能解释关键元数据来自哪里，并能在后续扩展中安全承载 Assistant、Steward 和 Operator 三种不同权限模式，而不是依赖无法恢复的黑盒操作。

## Delivery Map

子任务按照下列顺序交付；父子关系表达范围归属，前置依赖以此处和各子任务 PRD 为准：

1. `09-01-core-0-first-browse-slice`：首个端到端纵向切片，交付单管理员初始化与登录、受限目录注册、FLAC/MP3 只读扫描、最小 Release Graph 和 Web 浏览详情。
2. `09-01-core-0-basic-search`：在稳定读模型上增加 PostgreSQL 基础搜索与筛选。
3. `09-01-core-0-format-cue-expansion`：增加 OGG、Opus、WAV、常见 CUE 和扩展多碟样本。
4. `09-01-core-0-release-artwork`：增加 release-level 封面发现、受控存储与鉴权展示。
5. `09-01-core-0-private-multi-user`：增加普通用户管理、禁用、会话撤销和只读权限矩阵。
6. `09-01-core-0-root-operation-governance`：基于真实目录操作增加 revision、幂等、审计事件和具体恢复语义。

首个纵向切片通过后才开始后续能力。后续子任务可按依赖和风险进一步拆分，不得为了满足父任务一次性验收而回到单次大交付。

## Background and Confirmed Facts

- 当前 `/root/workspace/ROOMusic` 只有 PostgreSQL、Redis、Meilisearch 的本地开发依赖和 `mise` 工具链配置，尚无业务后端或前端工程。
- `/root/workspace/ROOMusic-V0` 已包含较完整的 Go 后端、扫描器、Release Graph、鉴权、搜索、metadata 和 AI foundation，但同时包含大量后续阶段、迁移、worker、operator 和历史兼容设计。
- 新项目继承 V0 的产品方向和必要决策，不直接复制 V0 的全部代码、数据库迁移、AI worker、operator、复杂队列或完整路线图。
- Core 0 采用 REST-first 产品 API；初始化、鉴权、扫描、库浏览、详情和基础搜索先通过清晰的 REST 资源接口完成，GraphQL 等复杂图谱查询能力在后续版本按真实需求引入。
- Core 0 的产品运行时只强制依赖 PostgreSQL；扫描任务先由 Go 进程内调度并把运行状态保存到 PostgreSQL，基础搜索先使用 PostgreSQL。现有 Compose 中的 Redis 和 Meilisearch 作为后续扩展或开发依赖保留，但不进入 Core 0 的产品闭环。
- Core 0 首批扫描格式确定为 `FLAC`、`MP3`、`OGG`、`Opus`、`WAV`，并支持常见 CUE sheet 解析为虚拟 Track；`AAC`、`DSD`、`APE` 和更复杂 CUE 变体后续扩展，暂不支持的格式必须记录可解释的跳过原因。
- Core 0 开发时由 React/TypeScript 独立开发服务器提供热更新；生产构建产物由 Go 后端统一托管，最终只暴露一个 ROOMusic 应用进程和一个应用端口。
- Core 0 使用 PostgreSQL 服务器会话：客户端通过 `HttpOnly`、生产环境 `Secure`、适当 `SameSite` 策略的 Cookie 携带随机 opaque token，数据库只保存 token hash、用户、到期时间和撤销状态；禁用用户、登出和撤销会话必须立即生效。
- Core 0 实现最小私有多用户：首次安装创建唯一初始管理员；管理员可以创建、禁用普通用户并撤销其会话。所有用户共享同一音乐库；普通用户只能登录、浏览和搜索，只有管理员可以管理目录、触发扫描、查看完整扫描诊断和执行持久化管理操作。
- 核心音乐语义继续采用 `ReleaseGroup -> Release -> Medium -> Track`，并保留本地文件与发行版本之间的关系。
- 默认音乐目录只读；初代核心不直接写回音频标签，不默认修改、移动或删除用户原始音乐文件。
- 产品 Agent 统一称为 `Music Steward`，Assistant、Steward、Operator 是执行模式，不是三个互相复制的产品 Agent。
- Core 0 不包含可运行的 Music Steward Agent、Review Subagent 或模型调用；本阶段只定义未来 Agent 所需的执行模式、工具边界和变更管理基础，实际 Agent runtime 从 Core 0.1 开始规划。
- 所有副作用都必须经过后端注册工具和统一的执行边界；Agent、模型或子 Agent 不能直接获得数据库连接、任意 shell 或无限制文件系统访问。
- 日志、操作历史、变更集、恢复点和可逆执行器各自承担不同职责；不采用完整 Event Sourcing 作为 Core 0 的基础。
- Core 0 实现窄而真实的变更管理闭环：用户发起的持久化管理操作进入 `Change Set + Operation Journal`；以音乐库目录配置的新增、停用和恢复作为首个可逆样例。扫描派生数据由 scan run 和字段来源追踪，不为每个扫描字段创建变更集；文件隔离区和文件级 Checkpoint 等到真正开放文件写工具时再实现。
- Core 0 重扫采用保守的软缺失策略：只有完整成功的目录扫描才能把未发现来源标记为 `missing`；失败、取消、权限错误、目录离线或不完整扫描不得改变既有来源的可用状态。缺失数据继续保留并可诊断，同一来源重新出现时恢复；Core 0 不自动物理删除 Release Graph 数据，也不定时 purge。
- Core 0 只保证同一注册目录中同一相对路径重扫、以及同路径来源重新出现时保留 Track 身份。文件重命名或跨目录移动表现为“旧来源 `missing` + 新来源”，可以记录候选诊断，但不自动继承 Track ID；可信文件系统 identity、内容摘要或音频指纹移动识别后续实现。
- 部署配置必须声明一个或多个只读 `allowed_library_roots`；管理员只能注册这些根目录本身或其规范化子目录。后端必须解析真实路径和符号链接，拒绝路径穿越、越界目录和白名单之外的路径。容器部署默认只读挂载 `/music`，本地开发可配置等价根目录。
- Core 0 采用保守 ReleaseGroup 分组：一个识别出的发行目录形成一个 `Release`，其多碟子目录可以组成多个 `Medium`；每个 Release 初始拥有独立 ReleaseGroup。系统不凭相似标题、艺术家或目录名自动跨目录合并，只允许记录非权威的潜在同组诊断。
- Core 0 包含只读的 release-level 默认封面闭环：扫描明确命名的同目录图片（如 `cover.*`、`folder.*`、`front.*`）和音频内嵌封面，按简单、确定的优先级选择默认封面；受控副本或单一展示缩略图保存到 ROOMusic 自己的 data 目录，PostgreSQL 只保存来源、hash、MIME、尺寸和存储引用。图片通过受鉴权的资源 ID 提供，不暴露原始服务器路径。
- Core 0 默认不跟随目录符号链接；文件符号链接只有在解析后的真实目标仍位于同一个 `allowed_library_root` 内时才读取。越界、断链和循环风险记录为可解释诊断，后续再按真实 NAS 需求增加显式开启的目录链接跟随策略。
- Core 0 的进程内扫描调度采用全局串行策略：同一时刻只允许一个全局 scan run，多个注册目录按稳定顺序处理；扫描中重复触发返回当前 `scan_run_id`，不创建重复运行。单个目录或文件错误记录诊断并继续其他目录，但只要存在未完成根目录或关键遍历错误，整次扫描不得执行负向 `missing` 对账。

## Requirements

### R1. 可运行的前后端核心

- 提供可启动的 Go 后端和 React/TypeScript 前端。
- 前端开发模式允许独立 dev server；生产模式必须由 Go 后端托管编译后的静态资源，并让 Web UI 与 REST API 使用同一来源。
- 后端提供版本化 REST API；Core 0 不要求 gqlgen、GraphQL schema、resolver、dataloader 或 GraphQL 客户端生成链路。
- 提供管理员初始化、登录和最小的管理员/普通用户权限边界。
- 首次安装只允许创建一个初始管理员；系统完成初始化后必须关闭公开 setup 入口，后续普通用户只能由管理员创建。
- 管理员可以创建和禁用普通用户、撤销用户会话；普通用户共享全局音乐库的只读视图，不能管理目录、触发扫描、读取敏感诊断或执行持久化管理操作。
- 登录态不得依赖浏览器可读的 bearer token；Cookie 鉴权的状态变更请求必须具备同源/Origin 检查或等价的 CSRF 防护。
- 提供音乐库目录注册和只读扫描入口。
- 音乐目录注册必须受部署级 `allowed_library_roots` 限制；规范化路径和符号链接解析后的真实目标仍须位于允许根内，API 不得充当任意服务器路径浏览器。
- 提供基础音频标签、文件信息和 CUE 解析能力；扫描结果能够形成最小 Release Graph。
- 首批扫描必须覆盖 `FLAC`、`MP3`、`OGG`、`Opus`、`WAV` 和常见 CUE；对暂不支持的格式或解析失败保留跳过/失败诊断，不得静默丢弃。
- 扫描运行必须区分成功、失败、取消和不完整状态；只有完整成功的扫描可以执行负向对账并标记 `missing`。
- `missing` 来源及其派生实体继续保留并在管理界面中提供诊断；同一来源重新出现时恢复原有关系，Core 0 不自动清除缺失实体。
- Track 身份在同一 library root 和同一规范化相对路径内稳定；rename/move 不得仅凭文件名、大小、时长或标签自动继承旧 Track ID。
- 对疑似 rename/move 可以保存非权威候选诊断，但候选不得改变 Track 身份或自动恢复旧来源。
- 歌词不属于 Core 0 的扫描、存储或前端详情范围。
- Core 0 不提供 Release、Track、Artist 的人工编辑或 metadata overlay 写入；目录配置的变更管理不等同于音乐 metadata 编辑。
- 每个识别出的发行目录建立独立 Release 和 ReleaseGroup；同一发行目录下明确的多碟结构建立多个 Medium。跨目录版本归组不得由弱启发式自动执行。
- 潜在 ReleaseGroup 同组关系可以作为非权威诊断保存，但 Core 0 不提供 merge、split、确认或自动应用。
- 扫描器只读发现 release-level folder artwork 和音频内嵌封面；默认优先级必须确定且可测试，相同输入重扫得到相同默认封面。
- 封面内容复制或生成单一展示缩略图时只能写入 ROOMusic 管理的 data 目录，不得修改音乐目录；数据库不得保存大图片二进制。
- 封面 API 必须使用受鉴权的资源 ID，提供正确 MIME 和缓存语义，并对普通用户隐藏原始服务器路径和管理诊断。
- 默认不跟随目录符号链接；文件符号链接的真实目标必须通过同根白名单校验，循环、越界和断链必须可诊断且不能阻塞其他合法文件扫描。
- 同一时刻只允许一个全局扫描运行；重复触发必须幂等返回当前运行；多个根目录按稳定顺序处理并在同一 scan run 中区分各目录状态。
- 提供音乐库列表、Release 详情、Medium/Track 展示和基础搜索或筛选。
- 前端必须覆盖登录、库浏览、详情、扫描状态、loading、empty 和 error 等基础状态。

### R2. 证据优先但保持精简

- 关键元数据至少保留当前值、来源类型（如 `tag`、`filename`、`cue`、`manual`）、是否为推断值、关联扫描运行和观察时间。
- 系统能够解释关键字段的来源，不要求 Core 0 实现 V0 中完整的 evidence packet、AI ledger、多源优先级或复杂置信度治理。
- 扫描器负责确定性解析和整理；能由程序稳定判断的内容不交给 AI。

### R3. 三种 Music Steward 执行模式契约

- Core 0 只固化三种模式的产品契约和后端权限边界，不实现可运行的主 Agent、Review Subagent、模型调用或自动 Agent 调度。
- `Assistant` 模式：可以查询、解释和生成操作计划；危险操作必须等待当前用户明确批准后才能执行。
- `Steward` 模式：可以调用独立职责的 Review Subagent 审查操作计划；只有结构化审查通过后，才允许提交执行。
- `Operator` 模式：管理员显式进入后，可以跳过用户批准和 Review Subagent，直接提交操作执行。
- 三种模式都必须使用后端白名单工具、参数校验、目标校验、权限校验和资源范围校验。
- Operator 的“直接执行”不等于无限制 shell、任意数据库操作或任意宿主机文件访问；危险工具仍然必须由后端显式注册并限制作用域。
- Review Subagent 的职责是独立检查目标、证据、风险、影响范围、策略、批量限制和恢复路径，不能只重复主 Agent 的结论。

### R4. 业务变更管理与恢复

- 系统使用 `Change Set` 描述一次完整业务变更，至少关联操作者、模式、目标、动作、执行状态和恢复可用性。
- 系统持久化 `Operation Journal`，记录操作发起者、工具、目标、状态、时间、错误分类和关联 request/task/agent 标识；它不是普通运行日志的替代品。
- Core 0 中所有用户发起的持久化管理操作必须通过变更管理边界执行；首个完整闭环是音乐库目录配置的新增、停用和恢复。
- 音乐库目录配置变更必须保存 before/after、资源 revision、幂等键、操作事件和明确的恢复动作，并对过期 revision 或重复但不一致的幂等请求返回冲突。
- 扫描产生的 Release Graph 和字段来源属于可重建派生数据，通过 scan run、source observation 和扫描诊断追踪，不为每个扫描字段创建独立 Change Set。
- 对需要恢复的操作提供 `Checkpoint` 或等价的执行前状态，保存受影响数据、文件清单、版本或 hash 等必要信息，而不是复制整个音乐库。
- 由 `Reversible Executor` 执行明确的逆操作；不能只保存自然语言描述并依赖 Agent 猜测恢复步骤。
- 数据库变更必须具备事务、幂等和版本冲突保护。
- 文件删除默认进入隔离区而非立即物理删除；永久清除必须明确标识为不可逆操作。
- Core 0 不直接实现完整 Event Sourcing、Git-like 分支合并或全量数据版本树。

### R5. 可观测性、质量与安全底线

- 使用结构化 JSON 日志，至少支持 `request_id`、`scan_run_id`、`task_id` 和 `operation_id` 等关联字段。
- 日志和持久化操作记录不得泄露密码、Token、数据库连接串、完整敏感路径或外部 Provider secret。
- 为标签解析、CUE 解析、扫描入库、鉴权、关键 API 和前端核心流程建立与范围匹配的测试。
- 提供最小质量门禁：格式化、编译/typecheck、单元测试、关键集成测试、Compose 配置校验和静态检查。
- 质量门禁用于防止重构失控，不以追求全覆盖或建立复杂质量平台为目标。

## Initial Out of Scope

- V0 的完整 AI provider、LangGraph 或其他 Agent runtime；可运行的 Music Steward 从 Core 0.1 另行规划。
- GraphQL 产品 API、GraphQL Subscriptions、gqlgen 生成代码和对应客户端文档生成链路。
- JWT access/refresh token 轮换体系、前端 token 持久化和 Redis session store。
- 多 Agent 协作网络、复杂 memory、AI ledger、provider fallback、AI batch lease 和 circuit breaker。
- MusicBrainz/Discogs/Last.fm/QQ Music 多源 enrichment。
- Release/Artist merge、split、topology governance 和不可逆实体身份改写。
- 基于相似 album title、artist、year 或目录名的自动跨目录 ReleaseGroup 归组。
- 音频播放、转码、HTTP Range、播放队列、playlist 和 PWA 离线能力。
- track-level artwork、人工选择默认封面、封面冲突编辑、多尺寸衍生治理和外部封面下载。
- 用户播放历史、评分、收藏、playlist、用户偏好和细粒度权限；Core 0 只实现 admin/user 两级最小权限。
- 默认的 tag write-back、文件 rename/move/delete、任意 shell、任意 SQL 或 backup/restore 操作。
- 文件隔离区、文件级 Checkpoint、tag 原文件备份和通用文件恢复执行器；这些能力在首次开放文件写工具时一并设计。
- 自动物理删除缺失 Track/Release、定时 purge 和基于不完整扫描的负向对账。
- 基于文件系统 identity、全文件摘要、Chromaprint 或弱特征的自动 rename/move 身份继承。
- 完整运维 UI、复杂搜索 outbox、dead-letter 系统和完整 Event Sourcing。
- Redis/asynq 持久化任务队列、Meilisearch 专用搜索、搜索 outbox 和跨服务最终一致性。
- 歌词扫描、歌词存储、同步歌词展示、歌词来源冲突和歌词 API。
- Release、Track、Artist 的人工 metadata 编辑、overlay、pin、冲突处理和 metadata 回滚。
- 生产环境中的独立前端服务、独立前端容器和必须依赖 CORS 的跨来源部署。
- 注册或浏览 ROOMusic 进程可见的任意宿主机绝对路径；Core 0 只允许配置白名单中的音乐目录。
- 默认跟随目录符号链接、跨允许根读取文件、以 inode visited set 管理重复来源，或在首版承诺复杂链接目录归属。
- 多根目录并行扫描、按目录独立排队、跨进程扫描协调和 Redis/asynq 扫描任务恢复。

## Acceptance Criteria

- [ ] 全新环境能够按项目文档启动后端、前端和 PostgreSQL，不依赖旧 `ROOMusic-V0` 的运行时代码或数据库数据；Redis 和 Meilisearch 不应成为 Core 0 启动的必要条件。
- [ ] 管理员能够完成初始化和登录，普通用户权限不会越过管理员边界。
- [ ] 首次初始化结束后不能再次公开创建管理员；管理员可以创建/禁用普通用户并撤销其会话，普通用户只能浏览和搜索共享音乐库。
- [ ] 登录、登出、会话过期、会话撤销和用户禁用具有自动化验证；数据库不保存 session token 明文，浏览器脚本无法读取认证 Cookie。
- [ ] 管理员能够注册一个只读音乐目录并触发扫描；扫描结果可在前端浏览。
- [ ] 音乐目录注册拒绝路径穿越、符号链接越界和白名单外路径；容器中的音乐目录保持只读挂载。
- [ ] 默认目录符号链接不会被跟随；合法同根文件符号链接可读取，越界/断链/循环只产生诊断且不影响其他合法文件。
- [ ] 多个注册目录按稳定顺序由一个进程内 scan run 串行处理；扫描中重复触发返回同一 `scan_run_id`，不会产生重复运行。
- [ ] 完整成功扫描可以把未发现来源标记为 `missing`；失败、取消、权限错误和目录离线不会改变既有来源状态；同一来源重新出现后能够恢复。
- [ ] 同一路径重扫保持 Track ID；rename/move 不会凭弱证据错误继承 Track ID，而是形成旧来源 `missing`、新来源和可选诊断。
- [ ] 初始化、登录、扫描、库浏览、Release 详情和基础搜索能够通过版本化 REST API 完成，并具有稳定的错误响应契约。
- [ ] 开发环境支持前端热更新；生产构建由单个 Go 应用进程同时提供 Web UI 和 REST API，不需要独立前端服务。
- [ ] 代表性音频文件、CUE 和多碟目录能够形成可查询的最小 `ReleaseGroup -> Release -> Medium -> Track` 图谱。
- [ ] 每个发行目录默认形成独立 ReleaseGroup/Release，多碟目录形成多个 Medium；相似的跨目录发行不会被弱规则自动合并。
- [ ] 首批格式 `FLAC`、`MP3`、`OGG`、`Opus`、`WAV` 和常见 CUE 均有代表性样本验证；`AAC`、`DSD`、`APE` 暂不支持时会显示明确原因。
- [ ] 明确命名的目录图片和音频内嵌图片能够形成稳定的 Release 默认封面；图片由鉴权资源 API 返回，音乐目录保持只读且响应不泄露原始路径。
- [ ] 关键字段能够展示来源、扫描关联和推断状态。
- [ ] Core 0 的扫描 metadata 在 API 和前端中保持只读；普通用户和管理员都不能通过 Core 0 修改 Release、Track、Artist 字段。
- [ ] 扫描、鉴权和核心 API 具有可重复的自动化验证；前端关键页面至少通过类型检查、lint 和核心交互验证。
- [ ] 关键运行链路输出带关联 ID 的结构化日志，且不输出 secret 和不必要的敏感路径。
- [ ] 已确定的变更管理基线能够描述操作状态、保存必要的执行前信息，并为支持的操作提供明确的逆操作或不可逆声明。
- [ ] 管理员新增、停用和恢复音乐库目录配置时会产生可查询的 Change Set 与 Operation Journal；恢复操作能够在 revision 未冲突时还原前一状态，重复请求不会产生重复副作用。
- [ ] Assistant、Steward、Operator 三种模式的审批差异、后端执行边界和失败行为在产品契约中有明确可测试定义。

## Open Questions Blocking Final Planning

- None at this stage. Product scope decisions recorded above are ready for technical design review.

## Planning Notes

- 这是版本级父任务，不直接执行产品代码；每个复杂子任务必须拥有自己的 `prd.md`、`design.md` 和 `implement.md`。
- Assistant、Steward、Operator 在 Core 0 中只作为长期权限和执行边界约束，不创建 Agent runtime、Mode Kernel、Tool Registry 或审批状态机。
- 当前仍处于 Trellis Phase 1 planning；应审查并启动拥有下一项实际交付的子任务，而不是启动父任务。

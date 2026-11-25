# ROOMusic Architecture Decisions

## Status

本文档是架构摘要，记录长期规则并区分当前实现与目标边界。目标结构的图示见
[目标架构图](architecture.html)。2026-09-28 已批准首批范围与部分 ADR 项，
其余新增能力与技术决策以各 ADR 的决策记录和后续批准为准，各 ADR 的状态见 [ADR 索引](#adr-索引)。

## 文档语言

项目新增或修改的文档必须使用简体中文。范围包括 README、架构文档、ADR、
API 文档、任务文档、说明性注释和运维文档。代码标识符、文件名、命令、
协议名称、产品名称和必须保留的原文引用可以使用英文。与当前任务无关的已有
英文内容不需要翻译，除非用户明确要求。

## System shape

- ROOMusic is a modular monolith: one Go backend process, a React/TypeScript
  development frontend, and PostgreSQL as the required business authority.
- Production Web UI and `/api/v1` REST are served from the same Go application.
- Redis, Meilisearch, GraphQL, PWA offline behavior, and external production
  frontend services are optional future work, not runtime dependencies.
- The repository tree expresses large capability boundaries. Concrete Go and
  TypeScript files are chosen from the actual use cases and may evolve within
  their owning module.

## 当前实现树

2026-09-30 后端已按能力分包（仍是单进程模块化单体）；2026-10-01 前端重建（外壳与管理页）：

```text
backend/cmd/roomusic/main.go # 进程入口与 HTTP 生命周期，不含业务策略
backend/internal/*           # app（组合根）、platform、identity、library（含 parser）、catalog（含 metadata、artwork）；
                             # 各能力的 REST 传输层在 <能力>/httpapi
backend/migrations/*.sql     # embed.FS 迁移；已共享文件不改写
backend/cmd/roomusic/web/    # Go 内嵌目录：dist/ 是前端生产构建（Git 忽略，生产镜像的 Node 阶段生成）
frontend/src/               # 2026-10-01 重建的 Web 客户端（布局见前端目录结构规范）
frontend/e2e/               # Playwright 冒烟、无障碍与响应式检查
frontend/DESIGN.md          # 设计依据（第 1–4 节）；标为“已实现”“落地值”的条目是实际采用的值
frontend/DESIGN-*.md        # 设计依据的第 5–7 节（components）与第 8–10 节（acceptance）
docs/design/                # 已入库原型及设计工具资产，内容保持原样
```

`backend/internal/*` 是现状（布局见[后端目录结构](../../.trellis/spec/backend/directory-structure.md)）；
`frontend/src/` 的实际布局见[前端目录结构](../../.trellis/spec/frontend/directory-structure.md)，下方
`features/*` 示意图中尚未建立的目录仍只是目标能力边界。
前端重建用现有 API 恢复了外壳与管理界面；后端分包拆出了 catalog、library 与 platform；
身份匹配（2026-09-30）在拆出的 catalog 上实施身份规则与按组只读接口，专辑页（2026-10-03）随后使用了它。
不为目录对称性生成空层，也不把 API 行为修复、新迁移或媒体服务夹入纯重构。

## Repository-level modules

```text
backend/       Go modular monolith and backend-owned migrations
frontend/      React/TypeScript Web UI
deploy/        Compose, containers, and production setup
extensions/    Stable typed capability contracts for future extensions
plugins/       Future concrete plugin implementations
docs/          Architecture, API, ADR, security, and operations documentation
scripts/       Development, database, fixture, and verification helpers
```

`backend/migrations/` is the canonical migration location. Do not create a
second root-level `migrations/` directory unless an explicit design decision
changes the migration owner.

迁移由 Platform/Database 启动边界统一治理：应用使用
`database/sql + pgx/v5`，先验证嵌入文件的连续版本和原始字节 SHA-256，再在
一个 PostgreSQL 事务中持有固定的事务级 advisory lock
`0x524f4f4d55534943`（`ROOMUSIC` 命名空间）。`schema_migrations` 保存不可变的
文件名、校验和和应用时间；名称/校验和漂移或未知未来版本会阻止启动。0001--0007
属于已共享迁移，只能通过追加迁移修正；旧库中历史上缺少 0002--0005 记录时，
执行器按已验证的 0001、0006、0007 形态建立一次性元数据基线，不重放旧 SQL。
迁移失败整体回滚。2026-11-06 起迁移 0031 及以后必须附带回退 SQL（或显式标记不可回退），
回退 SQL 随正向迁移存进 `schema_migrations`；旧版本启动时先做迁移前备份，再在同一事务里倒序回退库里不认识的迁移，
不可回退时拒绝启动并指向要恢复的备份。降级底线为 0.1.0（迁移 30），合同见
[database-guidelines.md](../../.trellis/spec/backend/database-guidelines.md)。

## Backend capability ownership

```text
backend/internal/
├── app/          composition root, route mounting, lifecycle
├── platform/     config, HTTP mechanics, PostgreSQL, clock, observability
├── identity/     setup, users, sessions, roles, authentication authority
├── library/      allowed roots, filesystem safety, scanner, parsers, diagnostics
├── catalog/      ReleaseGroup, Release, Medium, Track, provenance, read model
├── search/       query semantics and rebuildable search projections
├── operations/   Change Set, Operation Journal, revision, idempotency, recovery
└── agent/        Assistant/Steward/Operator modes, plans, review, tool authority
```

上图是能力边界示意，实际的包以[后端目录结构](../../.trellis/spec/backend/directory-structure.md)为准；
不按目录图预建包或运行时。拆包时每一步保留既有 API 和安全/事务/扫描行为。

Each capability owns its policy, application use cases, ports, adapters, and
tests. Cross-capability calls use published typed contracts or narrow
consumer-owned ports. A capability must not import another capability's private
adapter, transport, database row type, table, or helper.

## Frontend capability ownership

```text
frontend/src/
├── app/             routes, providers, authenticated shell
├── features/auth/   setup, login, session, logout
├── features/library/ roots, scans, diagnostics
├── features/catalog/ releases, media, tracks, artwork presentation
├── features/search/ query, filters, results
├── features/operations/ status, history, recovery UI
├── features/agent/  Assistant/Steward/Operator UI
└── shared/          HTTP transport, policy-free UI, neutral mechanics
```

2026-10-01 前端重建时按实际页面建立了 `app`、`auth`、`members`、`library`、`catalog` 和 `shared`。
上图中的搜索、通用操作和 Agent 目录只是候选；后来的 `player`、`curation`、
`artists`、`personal`、`steward` 等目录的命名与责任在实现时确定，不因示意图批量生成。
各 feature 拥有 REST DTO、decoder、hook、view model、组件和路由；`shared` 不能导入
`features` 或 `app`。Router/Query 已于 2026-09-28 获批（ADR 0001 决策记录），2026-10-01 已安装并锁定版本。

The frontend flow is:

```text
route → feature component → feature hook → feature API/decoder
      → shared HTTP transport → /api/v1
```

Browser code uses the HttpOnly session cookie and never reads or stores a bearer
token. Frontend role checks only control presentation; backend authorization is
the authority.

## Dependency direction

```text
HTTP transport → application use case → domain + consumed ports
                                             ↑
                              PostgreSQL/filesystem adapters
```

The composition root creates concrete adapters and injects them into application
services. Domain code stays deterministic where possible. Transport decodes and
maps; application code coordinates authority and business rules; adapters
enforce physical storage constraints.

目标边界要求 library scanner 通过窄 catalog 合同提交观察，不直接写 catalog 私有表
或替代其图不变量。分包之后 library 只经 `ingest.PersistCandidate`、`ingest.MarkMissingSources`
提交，图表的 SQL 只在 catalog 内；分包只迁移职责并保持行为，不夹带人工关系保护等新规则。

## Core safety invariants

- Setup atomically creates one initial administrator and then closes public setup.
- Sessions use high-entropy opaque tokens; PostgreSQL stores only token hashes,
  expiry, and revocation state.
- Library roots are canonicalized and checked with realpath containment against
  configured `allowed_library_roots`.
- Directory symlinks are not followed by default. File symlinks are readable only
  when their resolved target remains within the same allowed root.
- The music library is read-only. Core code never modifies, moves, deletes, or
  writes tags to source files.
- Only a complete successful scan may perform negative `missing` reconciliation.
- PostgreSQL remains authoritative; any future search index or provider output is
  a rebuildable projection, candidate, observation, or explicitly authorized
  execution result.
  管理员开启自动整理后，外部源与模型的产出写入独立的自动层，属于这里的“明确授权的执行结果”：
  它排在人工修正之后、扫描值之前，有记录并可撤销（[ADR 0006](adr/0006-auto-curation-and-sources.md)）。
- Ordinary REST responses and logs must not expose passwords, tokens, database
  URLs, allowlists, or unnecessary absolute music paths.

## Extension boundary

`extensions/` describes typed capability contracts. `plugins/` contains future
implementations such as additional parsers, metadata providers, artwork
providers, agent providers, or execution providers. A plugin cannot grant itself
capabilities, bypass backend authority, or mutate core state directly.

Capability Registry and Plugin Host are future runtime infrastructure. Do not
introduce a service locator, dynamic plugin loader, RPC layer, or WASM runtime
until an approved feature requires that boundary.

### 扩展边界的两步走（指导原则，2026-09-28；2026-11-22 修订）

以下是评估后的指导原则，不是已批准的插件方案；未来插件立项时须复核。

2026-11-22 立项外部数据源与自动整理时复核，“元数据与封面提供方的产出只作为候选或证据”一句由下面六条原则替换
（[ADR 0006](adr/0006-auto-curation-and-sources.md) 第 3 节）：

1. 三层分工：程序判定能确定的 → 模型判断程序拿不准的 → 人只处理模型上交或判断不了的。
2. 默认自动，例外才问人：程序和模型能定的直接生效，不进待确认队列。
3. 安全靠事后可撤，不靠事前确认：每次自动修改有记录、依据与来源，可以单条撤销，也可以按批或按来源撤销。
4. 人工修正永远优先：用户亲手改过的，程序和模型都不再改。
5. 音乐文件只读。
6. 能力由宿主分配：外部源只拿到自己那类能力的口子，不能自行取得其他权限。

2026-11-25 整理链重构修订原则 1、2，另加三条（ADR 0006 第 16.1 节，分步实施，尚未实现）：结局只有三种（自动写入、保持本地原样、稍后自动重试），
不再有等人处理的例外；本地能定的不联网，程序能定的不用模型，模型只做可核对的事；核对代替确认；中断安全；错了要看得见。

- 扩展从两个口子进入：**数据侧**（格式解析器、元数据与封面提供方；外部源的产出经后端的匹配规则或审查之后写入自动层，
  不覆盖人工修正）和**意图侧**（Agent 与审查插件只产出 Change Set；执行插件只能由 operations
  在授权包裹下调用）。任何扩展都不能绕过 `operations.Run` 直接写有效值，也不能给自己授权。
- **第一步（没有第三方扩展需求时）**：只做“插件形状”，不建插件基础设施。各模块由使用方定义窄接口，
  外部依赖实现为同一二进制内的适配器，例如 ALAC 无损转换用的 ffmpeg、外部数据源和管家的模型服务。
  外部数据源与自动整理的外部源按这一步做：内置适配器加统一的能力接口与声明，设置页可开关（ADR 0006 第 8 节）。
- **第二步（出现第三方或仓库外扩展需求时；2026-11-24 由 [ADR 0007](adr/0007-source-plugins.md) 落地为 WebAssembly 沙箱，取代下面“优先独立进程”的说法）**：把已稳定的接口升级为 `extensions/` 下的类型化
  合同，再建能力注册表与插件宿主。插件优先运行在独立进程并通过类型化 RPC 调用；不采用 Go 的
  `plugin` 包（要求工具链与依赖版本完全一致、仅支持 Linux/macOS、不能卸载）；只有需要运行
  不受信任的第三方代码时才考虑 WASM 沙箱。
- 执行插件排在最后：必须在受治理操作与恢复策略成熟之后，并继续服从音乐源默认只读的不变量。

## ADR 索引

批准的是选型与范围；安装库、建表、扩权或变更生产媒体路径以实际实施为准。部分批准的 ADR 只有“决策记录”列明的项获批，其余仍待批准。

| ADR | 主题 | 状态 | 要点 |
| --- | --- | --- | --- |
| [0001](adr/0001-frontend-foundations.md) | 前端路由、数据请求、样式、字体与依赖治理 | 已批准，已实施 | Router + Query、CSS Modules、Fontsource 可变字体自托管、JSON 样本防漂移（2026-09-28 批准）；会话代次与跨标签页失效、精确依赖与浏览器矩阵、测试工具（2026-10-01 实施）；封面与静态资源缓存头单独批准并实施（2026-10-02） |
| [0002](adr/0002-metadata-overlays.md) | 扫描观察、人工修正、有效值与稳定身份 | 部分批准 | 成员关系匹配 + 同 root 保守重绑定；独立修正意图 + 有效值视图，一个发行一个人工修改版本号（2026-10-13）；管理员逐张指定的默认版本存在组上，失效时回到自动规则（2026-10-14）；问题的“已核对”是独立的核对记录，依据变了重新提醒（2026-10-17）；认领把不可见旧版本上的四类人工状态迁给新版本，新版本已有的以新版本为准（2026-10-17）；2026-11-22 由 ADR 0006 修订：自动层、首批外部提供方、强证据自动归组、发行的合并与拆分；Group 字段继承、`editor` 仍待批准 |
| [0003](adr/0003-governed-operations.md)（[附录](adr/0003-governed-operations-appendix.md)） | 通用变更集、审批、角色、检查点与受限逆操作 | 部分批准 | 最小共同合同 `operations.Run` 与单条撤销，首批是字段修正与 ReleaseGroup 成员调整（2026-10-13，只有管理员、没有审批队列）；设定默认版本（2026-10-14，以专辑为目标、组自己的版本号）、核对问题（2026-10-17，以发行为目标、带依据令牌）、认领（2026-10-17，目标是新版本，带两个版本的版本号与预览令牌）；新写操作的轻量标准（2026-11-03）；管家的边界随管家对话实施（2026-11-16）；2026-11-22 由 ADR 0006 修订：自动整理的来源与批次、按批与按来源撤销、艺人操作可撤销、Release 合并与拆分；审批队列、链式恢复、`editor` 仍是草案 |
| [0004](adr/0004-media-delivery.md)（[附录](adr/0004-media-delivery-appendix.md)） | 受保护串流、浏览器能力、ffmpeg 与派生缓存 | 部分批准；派生缓存已由服务端渲染取代 | 首发只做直出（2026-09-28）；ALAC 无损派生（2026-10-09 批准并实施）；2026-11-03 删除播放中的在途复核，只在建立传输时鉴权；2026-11-09 起所有播放改由服务端渲染交付，CUE 精确分轨随之交付，现行合同见[服务端渲染合同](../../.trellis/spec/backend/playback-render-contracts.md) |
| [0005](adr/0005-steward-data-and-egress.md) | 管家发给模型的数据、不可信文本、出站请求、密钥与令牌 | 已批准，已实施 | 出站地址规则、密钥加密存库、发给模型的数据范围、`operator` 的剩余风险（2026-11-16 批准并随管家对话实施）；2026-11-22 增加后台自动整理与外部数据源的规则 |
| [0006](adr/0006-auto-curation-and-sources.md) | 外部数据源与自动整理：三层分工、自动层与撤销、强证据、外部源与发出的数据、后台运行的身份与审查、例外与用量 | 已批准，已实施（发行拆分未做） | 六条原则（见上文“扩展边界的两步走”）；自动层排在人工修正之后、扫描值之前，每条自动修改可单条、按批、按来源撤销；外部源是内置适配器，总开关关闭时不发外部请求；修订 ADR 0002、0003、0005（2026-11-22 批准，2026-11-24 实施）；2026-11-25 批准整理链重构（第 16 节）：结局只有三种、核对代替确认、程序按曲目结构判定文字搜索结果、艺人认别名、三档识别兜底与模型巡检、多来源队列，分步实施中 |
| [0007](adr/0007-source-plugins.md) | 第三方数据源插件：插件范围、WebAssembly 沙箱、唯一出网通道与密钥注入、能力、作为外部源的接入、安装与完整性、资源上限 | 提议（待审阅），已实施 | 插件只扩展数据源，是进程内 WebAssembly 沙箱里的模块；唯一出网通道是宿主函数，密钥由宿主注入；作为普通外部源接入，写入与撤销规则与内置来源相同；管理员上传安装并记录 SHA-256；修订 ADR 0006 第 8 节与上文“扩展边界的两步走”的第二步（2026-11-24） |
| [0008](adr/0008-instance-insights.md) | 匿名实例统计：默认开启、只发分档数字与功能开关、统计端开源自建、不记录 IP | 已批准，已实施 | 每天一次心跳，新实例满 24 小时后开始；管理员首次登录看到说明并可一键关闭；部署层可在首次启动前关闭；汇总公开（2026-11-24） |

当前安全和部署合同继续生效：只读源文件、后端最终授权、Cookie 同源、PostgreSQL 权威；
扫描观察不是用户逐字段操作，不能直接生成“回滚到某次扫描”的承诺。具体 URL、表结构、
版本、风险等级与播放支持矩阵在实现前确认后才成为可执行合同。

## Source documents

- `docs/architecture/architecture.html`（目标架构图，含扩展边界）
- `.trellis/spec/guides/modular-design.md`
- `.trellis/spec/backend/directory-structure.md`
- `.trellis/spec/frontend/directory-structure.md`

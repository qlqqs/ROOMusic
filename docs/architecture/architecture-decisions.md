# ROOMusic Architecture Decisions

## Status

本文档是供 Trellis 任务上下文读取的架构摘要，记录长期规则并区分当前实现与目标边界。
Canvas 保留历史可视化参考，不证明相关模块已存在；当前目标结构的图示见
[Core 1 目标架构图](core-1-architecture.html)。2026-09-28 已批准 Core 1 范围与部分 ADR 项，
其余新增能力与技术决策仍以文末各 ADR 的决策记录和后续批准为准。

## 文档语言

项目新增或修改的文档必须使用简体中文。范围包括 README、架构文档、ADR、
API 文档、Trellis 任务文档、说明性注释和运维文档。代码标识符、文件名、命令、
协议名称、产品名称和必须保留的原文引用可以使用英文。与当前任务无关的已有
英文内容不需要翻译，除非用户明确要求。

## System shape

- ROOMusic is a modular monolith: one Go backend process, a React/TypeScript
  development frontend, and PostgreSQL as the required business authority.
- Production Web UI and `/api/v1` REST are served from the same Go application.
- Redis, Meilisearch, GraphQL, playback, PWA offline behavior, and external
  production frontend services are optional future work, not Core 0 runtime
  dependencies.
- The repository tree expresses large capability boundaries. Concrete Go and
  TypeScript files are chosen from the actual use cases and may evolve within
  their owning module.

## Core 0 当前实现树

2026-09-30 后端已按能力分包（B0，仍是单进程模块化单体）；2026-10-01 前端由 M1a 重建（外壳与管理页）：

```text
backend/cmd/roomusic/main.go # 进程入口与 HTTP 生命周期，不含业务策略
backend/internal/*           # app（组合根）、platform、identity、library（含 parser）、catalog（含 metadata、artwork）；
                             # 各能力的 REST 传输层在 <能力>/httpapi
backend/migrations/*.sql     # embed.FS 迁移；已共享文件不改写
backend/cmd/roomusic/web/    # 前端生产构建（随源码提交，由 Go 内嵌；CI 检查漂移）
frontend/src/               # M1a 重建的 Classical 客户端（布局见前端目录结构规范）
frontend/e2e/               # Playwright 冒烟、无障碍与响应式检查
frontend/DESIGN.md          # Classical 设计依据（第 1–4 节）；标注“M1a 实现”的条目是实际采用的值
frontend/DESIGN-*.md        # 设计依据的第 5–7 节（components）与第 8–10 节（acceptance）
frontend-desgin/            # 已入库原型及设计工具资产，保持原样
```

`backend/internal/*` 是现状（布局见[后端目录结构](../../.trellis/spec/backend/directory-structure.md)）；
`frontend/src/` 的实际布局见[前端目录结构](../../.trellis/spec/frontend/directory-structure.md)（M1a，2026-10-01），下方
`features/*` 示意图中尚未建立的目录仍只是目标能力边界。
M1a 已用现有 API 恢复外壳与管理界面；B0 已拆出 catalog、library 与 platform；
阶段 I 在拆出的 catalog 上实施身份规则与按组只读接口，M1b 的专辑页（2026-10-03）随后使用了它。
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
迁移失败整体回滚，生产回退使用备份或 forward-fix，不提供 down migration。

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

当前身份、目录/扫描、catalog、基础 PostgreSQL 搜索和窄目录操作能力仍在 `package main`
中实现；上图不表示这些包已经存在。尤其 `agent` 与通用操作治理是后续能力，不能按目录图
预建运行时。B0 每一步保留既有 API 和安全/事务/扫描行为。

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

M1a 按实际页面建立了 `app`、`auth`、`members`、`library`、`catalog` 和 `shared`。
上图中的搜索、通用操作和 Agent 目录只是候选；Classical 后续 `player`、`curation`、
`artists`、`personal`、`steward` 的命名与责任由相应子任务落实，不因示意图批量生成。
各 feature 拥有 REST DTO、decoder、hook、view model、组件和路由；`shared` 不能导入
`features` 或 `app`。Router/Query 已于 2026-09-28 获批（ADR 0001 决策记录），M1a 已安装并锁定版本。

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
或替代其图不变量。B0 之后 library 只经 `catalog.PersistCandidate`、`catalog.MarkMissingSources`
提交，图表的 SQL 只在 catalog 内；B0 只迁移职责并保持行为，人工关系保护等新规则留待后续获批任务。

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

### 扩展边界的两步走（指导原则，2026-09-28）

以下是评估后的指导原则，不是已批准的插件方案；未来插件立项时须复核。

- 插件只能从两个口子进入：**观察层**（格式解析器、元数据与封面提供方的产出只作为候选或证据，
  不覆盖人工修正）和**意图侧**（Agent 与审查插件只产出 Change Set；执行插件只能由 operations
  在授权包裹下调用）。任何插件都不能直接写有效值结果层，也不能给自己授权。
- **第一步（Core 1 期间）**：只做“插件形状”，不建插件基础设施。各模块由使用方定义窄接口，
  外部依赖实现为同一二进制内的适配器，例如 M2b 的 ffmpeg、以后的元数据提供方和 M6 的模型服务。
- **第二步（出现第三方或仓库外扩展需求时）**：把已稳定的接口升级为 `extensions/` 下的类型化
  合同，再建能力注册表与插件宿主。插件优先运行在独立进程并通过类型化 RPC 调用；不采用 Go 的
  `plugin` 包（要求工具链与依赖版本完全一致、仅支持 Linux/macOS、不能卸载）；只有需要运行
  不受信任的第三方代码时才考虑 WASM 沙箱。
- 执行插件排在最后：必须在受治理操作与恢复策略成熟之后，并继续服从音乐源默认只读的不变量。

## Classical / Core 1 决策状态

[Core 1 路线](core-1-roadmap.md)记录各阶段的依赖、验收和旧任务承接。2026-09-28 用户（qlqq）
批准 Core 1 截止到 M4（B0、I、M1a、M1b、M2a、M2b、P、M3、M4，外加 F1、F2），M5、M6 后续；2026-10-02 起另有 F3（M1a 遗留的后端缺口）。
下表 ADR 均为**部分批准**：只有各自“决策记录”列明的项获批，其余仍待批准；批准的是选型与范围，
不是已经安装库、建表、扩权或变更生产媒体路径。

| ADR | 主题 | 涉及阶段 | 已批准的项 |
| --- | --- | --- | --- |
| [0001](adr/0001-frontend-foundations.md) | 前端路由、数据请求、样式、字体与依赖治理 | M1a/M1b | Router + Query、CSS Modules、Fontsource 可变字体自托管、JSON 样本防漂移；会话代次与跨标签页失效、精确依赖与浏览器矩阵、测试工具（2026-10-01，M1a 已实施）；封面与静态资源缓存头（2026-10-02，F3 已实施） |
| [0002](adr/0002-metadata-overlays.md) | 扫描观察、人工修正、有效值与稳定身份 | I/M3/M4 | 成员关系匹配 + 同 root 保守重绑定；Core 1 仅人工确认归组；M3（2026-10-13 批准并实施）：独立修正意图 + 有效值视图，一个发行一个人工修改版本号；M4 第一项（2026-10-14 批准并实施）：管理员逐张指定的默认版本存在组上，失效时回到自动规则；M4 第二项（2026-10-17 批准并实施）：问题的“已核对”是独立的核对记录，依据变了重新提醒；M4 第三项（2026-10-17 批准并实施）：认领把不可见旧版本上的四类人工状态迁给新版本，新版本已有的以新版本为准 |
| [0003](adr/0003-governed-operations.md)（[附录](adr/0003-governed-operations-appendix.md)） | 通用变更集、审批、角色、检查点与受限逆操作 | M3/M4/M6 | M3 试点：字段修正 + ReleaseGroup 成员调整，最小共同合同 `operations.Run` 与单条撤销（2026-10-13 批准并实施，只有管理员、没有审批队列）；成员写操作的幂等与记录不纳入 M3，独立小任务；M4 第一种版本关系操作“设定默认版本”（2026-10-14 批准并实施，以专辑为目标、组自己的版本号）；“核对问题”（2026-10-17 批准并实施，以发行为目标、带依据令牌）；“认领”（2026-10-17 批准并实施，目标是新版本，带两个版本的版本号与预览令牌） |
| [0004](adr/0004-media-delivery.md)（[附录](adr/0004-media-delivery-appendix.md)） | 受保护串流、浏览器能力、ffmpeg 与派生缓存 | M2a/M2b | “仅直出”作为 M2a 首发范围；2026-10-09 批准 M2b 的 ALAC 无损派生（K1–K6） |

当前安全和部署合同继续生效：只读源文件、后端最终授权、Cookie 同源、PostgreSQL 权威；
扫描观察不是用户逐字段操作，不能直接生成“回滚到某次扫描”的承诺。具体 URL、表结构、
版本、风险等级与播放支持矩阵在对应子任务批准后才成为可执行合同。

## Source documents

- `docs/architecture/core-1-architecture.html`（Core 1 目标架构图，含扩展边界）
- `docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx`
- `docs/architecture/roomusic-full-architecture-tree.canvas.tsx`
- `docs/architecture/core-0-file-structure.canvas.tsx`
- `.trellis/spec/guides/modular-design.md`
- `.trellis/spec/backend/directory-structure.md`
- `.trellis/spec/frontend/directory-structure.md`

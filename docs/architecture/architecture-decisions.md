# ROOMusic Architecture Decisions

## Status

本文档是供 Trellis 任务上下文读取的架构摘要，记录长期规则并区分当前实现与目标边界。
Canvas 保留历史可视化参考，不证明相关模块已存在。2026-09-27 的 Classical M0 仅获批
整理文档，新增能力与技术决策仍以文末草案和后续批准为准。

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

2026-09-27 的后端仍为过渡单体，前端源码已删除：

```text
backend/cmd/roomusic/*.go    # HTTP、身份、扫描、目录操作和 catalog 的当前实现
backend/migrations/*.sql     # embed.FS 迁移；已共享文件不改写
backend/cmd/roomusic/web/    # 当前为前端重置占位资产
frontend/package.json       # 保留工具链/依赖；src/ 不存在
frontend/DESIGN.md          # 本轮恢复的 Classical 设计依据，不是界面实现
frontend-desgin/            # 已入库原型及设计工具资产，保持原样
```

下方 `backend/internal/*` 与 `frontend/src/features/*` 仅是目标能力边界。
M1 候选先用现有 API 恢复客户端；B0 候选按真实能力分步拆包，两者可并行。
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

客户端恢复时按实际页面建立 `app`、`auth`、`library`、`catalog` 和必要 `shared`。
上图中的搜索、通用操作和 Agent 目录只是候选；Classical 后续 `player`、`curation`、
`artists`、`personal`、`steward` 的命名与责任由相应子任务落实，不因示意图批量生成。
各 feature 拥有 REST DTO、decoder、hook、view model、组件和路由；`shared` 不能导入
`features` 或 `app`。新增 Router/Query 仍须先通过依赖 ADR。

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
或替代其图不变量。当前 `scanner.go` 仍直接写图；这是待拆分的现状，不是上述边界已实现
的证明。B0 只迁移职责并保持行为，人工关系保护等新规则留待后续获批任务。

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

## Classical / Core 1 决策草案

[能力矩阵与路线](core-1-roadmap.md)记录 M0–M6/B0 的依赖、验收和旧任务承接。
推荐 Core 1 截止 M4、M5/M6 后续交付，但该范围仍待用户确认；也可先交付 M1+必要 B0+M2。
以下四份文档均为**草案／待批准**，不授权安装库、建表、扩权或变更生产媒体路径。

| ADR | 主题 | 涉及阶段 |
| --- | --- | --- |
| [0001](adr/0001-frontend-foundations.md) | 前端路由、数据请求、样式、字体与依赖治理 | M1 |
| [0002](adr/0002-metadata-overlays.md) | 扫描观察、人工修正、有效值与稳定身份 | M3/M4 |
| [0003](adr/0003-governed-operations.md) | 通用变更集、审批、角色、检查点与受限逆操作 | M3/M4/M6 |
| [0004](adr/0004-media-delivery.md) | 受保护串流、浏览器能力、ffmpeg 与派生缓存 | M2 |

当前安全和部署合同继续生效：只读源文件、后端最终授权、Cookie 同源、PostgreSQL 权威；
扫描观察不是用户逐字段操作，不能直接生成“回滚到某次扫描”的承诺。具体 URL、表结构、
版本、风险等级与播放支持矩阵在对应子任务批准后才成为可执行合同。

## Source documents

- `docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx`
- `docs/architecture/roomusic-full-architecture-tree.canvas.tsx`
- `docs/architecture/core-0-file-structure.canvas.tsx`
- `.trellis/spec/guides/modular-design.md`
- `.trellis/spec/backend/directory-structure.md`
- `.trellis/spec/frontend/directory-structure.md`

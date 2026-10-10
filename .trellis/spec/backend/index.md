# Backend Development Guidelines

## Status

当前仓库已有 Go Core 0 应用，入口位于 `backend/cmd/roomusic`，能力代码按 `backend/internal/` 分包
（B0，2026-09-30 完成，见[目录结构](./directory-structure.md)），使用
`net/http`、`database/sql + pgx/v5`、`embed.FS` 迁移、bcrypt 和 `log/slog`。
进程仍是单体；`domain/application/ports/adapters` 细分层只在出现第二个实现或需要隔离持久化时才建。

Core 0 is a modular monolith: one Go application, versioned REST, in-process scan
coordination, PostgreSQL as the only required business authority, and a read-only
music library.

## 后续阶段说明

Classical / Core 1 的能力路线与四类 ADR 位于
[Core 1 路线](../../../docs/architecture/core-1-roadmap.md)（2026-09-28 已批准阶段顺序与范围）。M0 与路线修订仅更新文档，
不改变本层现有 API、表结构或权限。B0（行为保持拆包）与阶段 I（身份稳定、按组只读接口）已完成；
修正覆盖层、通用审批、editor 角色和媒体处理的实施细节须经对应子任务与 ADR 项批准后再实施。Core 0 限制不是未来阶段的默认授权。

## Pre-Development Checklist

Before writing or reviewing backend code:

- [ ] Read the shared [guides index](../guides/index.md), then read
      [Product Goals](../guides/product-goals.md),
      [Modular Design](../guides/modular-design.md), and
      [Engineering Principles](../guides/engineering-principles.md).
- [ ] Read the layer guides below that govern the change. Read database,
      error, and logging rules whenever a backend workflow crosses those
      boundaries; read the Music Steward/operation guide for any Agent, tool,
      persistent operation, approval, or recovery work; read the quality guide
      for every implementation task.
- [ ] Identify the owning capability, its published contract, the input
      boundary, and the backend authority for the requested behavior.
- [ ] Confirm the change fits the Core 0 PostgreSQL-only, versioned REST,
      opaque-session, read-only-library scope before introducing an adapter or
      dependency.

## Layer Guides

| Guide | Contract |
| --- | --- |
| [Directory Structure](./directory-structure.md) | Capability ownership, package layout, and dependency direction |
| [Database Guidelines](./database-guidelines.md) | PostgreSQL ownership, transactions, migrations, revisions, and idempotency |
| [Error Handling](./error-handling.md) | Classified application errors and stable REST responses |
| [Logging Guidelines](./logging-guidelines.md) | Structured JSON events, correlation, and redaction |
| [Core 0 当前运行合同](./core0-runtime-contracts.md) | 总览：平台层 REST 通则、HTTP 缓存、迁移、环境、CI、完成日志，以及后续技术债和专题文件表 |
| [身份与成员合同](./identity-contracts.md) | 密码规则、初始化与成员接口、用户停用事务、写入失败映射 |
| [目录与扫描流程合同](./library-scan-contracts.md) | 目录幂等、扫描完整性、暂存与身份规划、CUE、抓轨日志、封面发现 |
| [扫描协调合同](./scan-coordination-contracts.md) | 扫描状态与记录、持久化取消、跨进程协调锁 |
| [真实库 Smoke 合同](./real-library-smoke-contracts.md) | 真实音乐只读验收与 V0 对照 |
| [Catalog REST 合同](./catalog-rest-contracts.md) | Release 列表/详情/evidence、按组接口、封面摘要、可见性和错误合同 |
| [播放直出合同](./playback-contracts.md) | M2a 格式表、预检、音频 Range、安全打开与周期复核；M2b 的 `delivery`、202 与派生错误码 |
| [无损派生合同](./playback-derivation-contracts.md) | M2b ALAC → FLAC 无损派生：配置与启动、缓存、转换与核对、上限与取消、测试 |
| [Music Steward And Operation Guidelines](./agent-and-operation-guidelines.md) | Agent modes, Review Subagent, tools, Change Sets, and recovery |
| [Quality Guidelines](./quality-guidelines.md) | Focused code, forbidden coupling, tests, and gates |

## Quality Check

Before declaring backend work complete:

- [ ] 按[后端质量规范](./quality-guidelines.md)运行改动影响到的现有门禁；Go module
      和测试套件已经存在。仅文档修改检查文档，不把未运行或因数据库缺失而跳过当作通过。
- [ ] Verify the behavior is owned by one capability, ports remain narrow, and
      no private table, adapter, or helper became a cross-module API.
- [ ] Verify request decoding, authorization, path safety, transaction,
      revision/idempotency, classified error, correlation, and redaction
      responsibilities remain at their correct boundaries.
- [ ] Test the representative success path and the relevant dangerous failure
      path, including cancellation or incomplete scans when applicable.
- [ ] Update this spec or a linked guideline when a Core 0 contract or selected
      implementation tool changes.
- [ ] 每个 `.trellis/spec` 文件不超过 24,000 字节（Trellis 给子代理注入上下文时，单个文件超过 32,768 字节的部分会被截断）。
      接近 24 KB 时，先按主题拆成新文件并更新上面的层级表，再继续增补；拆分只搬移内容、不改写，并核对没有丢失或重复。

## Core 0 Boundaries

- Transport decodes requests and maps responses. It does not own business policy
  or issue SQL.
- Application services authorize and coordinate use cases. Domain code contains
  deterministic invariants. Consumer-owned ports describe needed effects.
- PostgreSQL and filesystem packages implement ports. They never call back into
  HTTP or invent domain behavior.
- Identity, library/scanner, catalog, search, and operations capabilities may
  consume another capability's published contract only. Private tables,
  packages, and helpers are not integration APIs.
- Setup is closed after the first administrator. Authentication uses random
  opaque tokens in HttpOnly cookies; PostgreSQL stores only token hashes and
  revocation state.
- Every protected backend endpoint performs authorization. Frontend route guards
  are not an authority boundary.
- Library root input is canonicalized and checked against configured
  `allowed_library_roots`. Core 0 does not follow directory symlinks by default
  and never modifies source music.
- Only a complete successful scan can mark absent sources `missing`.
- User-initiated persistent management changes use Change Set and Operation
  Journal boundaries with revision and idempotency protection.
- Redis, Meilisearch, GraphQL, Agent runtime, runtime plugins, playback, and file
  mutation are future work and cannot become hidden Core 0 dependencies.

## Planned Request Shape

A protected write follows this flow:

```text
REST handler
  -> session + CSRF/origin + role middleware
  -> typed application command
  -> domain rules and narrow ports
  -> PostgreSQL transaction / constrained filesystem adapter
  -> typed result
  -> REST presenter
```

An anti-pattern is a handler that trusts a UI-supplied role, canonicalizes a path
itself, runs a repository update, and returns the database row directly.

## Source Evidence

- [Core 0 requirements](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md)
- [Current environment README](../../../README.md)
- [Current Compose services](../../../compose.yaml)
- [Architecture canvas](../../../docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx)

The Compose file includes Redis and Meilisearch as development/future services;
the Core 0 PRD explicitly makes them optional.

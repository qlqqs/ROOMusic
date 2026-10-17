# Product Goals And Phase Boundaries

## Product Identity

ROOMusic is a self-hosted, AI-native music library steward for individuals,
families, and small private groups with large NAS collections. Its core value is
release-aware knowledge management: music is modeled as
`ReleaseGroup -> Release -> Medium -> Track`, not as a folder browser with a chat
feature attached.

The long-term product should let users inspect, search, play, organize, and
govern their library with evidence-backed assistance. An AI capability may
propose or execute only through fixed identity, permission, confirmation,
audit, transaction, and recovery boundaries. It never becomes the source of
authority merely because it is called an Agent or plugin.

This repository-local guide preserves the relevant conclusions from the V0
product definition, requirements, roadmap, and Roon phase-one alignment
research. The original V0 planning workspace is historical input, not a
required sibling checkout, current source code, or a mandate to restore the V0
runtime.

## Long-Term Steward Model

The product has one user-facing Agent concept: **Music Steward**. Assistant,
Steward, and Operator are execution modes with different approval paths; they
are not three independent Agents and must not grow three separate authority
implementations.

| Mode | Approval path | Intended use | Core authority |
| --- | --- | --- | --- |
| `assistant` | The current user explicitly approves the proposed operation | Explain the library, prepare plans, and perform safe user-scoped actions | Cannot approve its own dangerous proposal |
| `steward` | An independent Review Subagent returns a structured approval, subject to policy | Autonomous evidence-backed organization and bounded maintenance | The main Agent cannot act as its own reviewer |
| `operator` | No user confirmation or AI review step | Explicit administrator-controlled direct execution | Still limited to registered tools and backend validation |

The approval path is the product distinction. Execution is shared: every mode
submits a typed command to the backend authority, which checks identity,
capability, target scope, physical safety, revision, and idempotency before any
side effect. Operator mode skips approval, not validation, logging, or recovery
metadata. "Direct execution" never means unrestricted shell, arbitrary SQL, or
unbounded host filesystem access.

The Review Subagent is a separate reviewer role, not a second general-purpose
Assistant. It receives a bounded operation proposal and safe evidence summary,
then returns `approved`, `rejected`, or `needs_human_confirmation` with reasons,
risk, scope, and recovery requirements. Its output is an approval input, never
the authority to write data. The backend re-evaluates the proposal and policy;
an Agent or model cannot manufacture approval by placing an approval field in
its own payload.

The deterministic library pipeline remains separate from Music Steward. Parsing,
identity assignment, conservative Release grouping, and source observation are
program-owned behavior. AI is used for unresolved interpretation and user
assistance, not as a replacement for deterministic rules that are already
reliable.

## Long-Term Change Management

ROOMusic does not use runtime logs as a substitute for Git. Logs are ephemeral
operational evidence; durable business recovery uses four separate concepts:

- **Change Set**: the intent and complete scope of one business operation.
- **Operation Journal**: immutable lifecycle events, actor, mode, tool, status,
  and correlation identifiers.
- **Checkpoint**: the minimum before-state, revision, manifest, or hash needed
  to recover the affected resources.
- **Reversible Executor**: typed inverse actions that can restore a supported
  operation, rather than an Agent guessing how to undo natural-language history.

Core 0 uses this model for directory configuration changes first; M3 extends the
operation record and single-step inverse to release field corrections and manual
regrouping (an `operations` table plus typed inverse executors; no Change Set
approval queue yet). It does not
implement full Event Sourcing, Git-like branches, or a full file recovery
system. Future file and tag mutation must add an explicit recovery strategy
before the capability is enabled. A physical purge may be irreversible, but
the operation must state that fact instead of claiming rollback support.

## Core 0 Outcome

Core 0 is the smallest useful local-library loop:

1. Initialize one administrator and authenticate private users with opaque,
   server-side sessions carried by secure cookie settings.
2. Register only a configured, allowed library root or one of its descendants.
3. Scan music read-only and parse the initial supported formats: FLAC, MP3, OGG,
   Opus, WAV, and common CUE sheets.
4. Build a conservative, evidence-linked Release Graph and expose browse,
   detail, scan status, and basic search through versioned REST.
5. Serve the production web build and REST API from the Go application, using
   PostgreSQL as the only required business authority.
6. Route user-initiated persistent management changes through Change Set and
   Operation Journal contracts; directory add, disable, and restore are the
   first reversible example.

The authoritative acceptance behavior is in the current
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).

## 2026-09-28 当前状态与 Core 1 范围

上文 Core 0 Outcome 是阶段目标和继承合同，不代表当前所有界面都已可用。Go 后端、迁移与 REST 保留；
`frontend/src/` 于 2026-09-27 删除，2026-10-01 由 M1a 重建了外壳、初始化与会话、目录与扫描管理、成员管理和简版概览，
2026-10-03 由 M1b 增加了专辑浏览（列表、搜索、详情与来源证据）；播放（M2）及之后的界面尚未实现。Classical 文档以 [frontend/DESIGN.md](../../../frontend/DESIGN.md) 为落地依据。

2026-09-28 用户批准 Core 1 = M0、B0、I、M1a、M1b、M2a、M2b、P（服务端播放历史）、M3、M4，
外加独立修复 F1、F2：依次是恢复客户端、后端拆包与身份稳定、真实播放和受控的元数据/版本管理。
M5（搜索规范化、艺人、收藏与歌单）与 M6（Steward）是后续阶段。完整能力矩阵、依赖、验收和
ADR 裁决状态见 [Core 1 路线](../../../docs/architecture/core-1-roadmap.md)。

M1a 只消费现有 API，M1b 使用阶段 I 的 ReleaseGroup 只读接口，两者都不新增元数据治理写路径；
初始化/会话、目录配置、扫描和成员管理的既有写操作并未被“只读客户端”称呼禁止。服务端播放
历史（P）排在阶段 I 之后，只承接真实播放的最小用户隔离记录，不提前实现全部个人库；P 已于 2026-10-11 实施（概览的“最近播放”与清除自己的历史），
历史页面、收藏与歌单仍属 M5。M3 已于 2026-10 实施受控的资料库写路径：管理员可以修正发行的八个字段、调整版本所属的专辑并撤销自己的最近一次修改，
修正存放在独立的表里、重扫不会覆盖，每次成功的修改写入只有管理员可读的操作记录（合同见[受治理操作合同](../backend/governance-contracts.md)）；
没有审批队列、`editor` 角色与成员写操作的记录，认领、整组合并拆分与默认版本属于 M4。M4 的第一项已于 2026-10 实施：管理员可以为多版本专辑指定默认版本
（成为专辑的代表版本，可撤销，失效时回到“年份最早”的自动规则），所有成员可以对比一张专辑的各个版本（合同见[默认版本与版本对比合同](../backend/default-version-contracts.md)）；
第二项（问题处理）同月实施：管理员的“元数据问题”页按版本列出问题与依据，可以把扫描无法消除的问题标记为已核对，情况变了重新提醒（合同见[核对合同](../backend/issue-review-contracts.md)）；
认领、扫描反馈与整组合并拆分仍分别立项。后续播放、
角色、审批或依赖仍须分别通过所属子任务与 ADR 中对应项的批准。

下面的永久约束跨阶段继续生效。不得因某能力不属于 Core 0 而永远禁止未来获批阶段，
也不能因未来路线提到它就把它变成当前必需依赖。

## Permanent Invariants

These rules survive later phases and adapter changes:

- The backend owns authentication, authorization, path safety, transactions,
  revision conflicts, idempotency, and recovery semantics.
- Music roots are read-only by default. Core 0 never writes tags, renames,
  moves, or deletes source music files.
- Source evidence remains inspectable. Paths describe a local observation; they
  are not the business identity of a Release.
- Versions remain distinct, and multi-disc releases use multiple Medium
  entities. Weak title, artist, year, or folder similarity cannot perform an
  authoritative cross-directory merge.
- A failed, canceled, offline, permission-denied, or incomplete scan cannot
  mark previously observed sources missing. Only a complete successful scan can
  perform negative reconciliation.
- Plugins, providers, Agents, and execution adapters use published capabilities;
  they cannot bypass core authority or mutate private module storage.

The inherited Roon alignment conclusion is that local files become an object
graph, versions remain distinct, and tags and source evidence remain
inspectable. Those conclusions are captured by the Core 0 outcome and permanent
invariants above so a standalone clone does not depend on external planning
files.

## Current Supersessions

| Historical V0 choice | Core 0 contract | Reason |
| --- | --- | --- |
| GraphQL-first product API | Versioned REST | Deliver a narrow, inspectable first loop |
| PostgreSQL + required Redis/Meilisearch | PostgreSQL-only product loop | Avoid queues and projections before a real need |
| JWT access/refresh tokens | Opaque server-side sessions in HttpOnly cookies | Immediate revocation and no browser-readable bearer token |
| Following directory symlinks | Do not follow directory symlinks by default; file targets must remain inside the same allowed root | Fail closed on cycles and boundary escapes |
| Runnable Agent and operator foundation | Mode and authority contracts only | Agent runtime begins after Core 0 |
| Playback and file operations | Deferred | The first loop is stewardship and read-only browsing |

The current [Compose file](../../../compose.yaml) contains Redis and Meilisearch
for development and future adapters. Their presence is not evidence that Core 0
may depend on them. The [README](../../../README.md) explicitly says capabilities
and configuration are added only when needed.

## Long-Term Direction, Not Current Scope

Later phases may add external metadata providers, dedicated search, durable job
queues, Music Steward runtime and review, playback, notifications, PWA behavior,
and tightly scoped file or tag execution. Each is an optional adapter or feature
behind the permanent invariants above. No roadmap entry pre-approves a library,
protocol, database bypass, or unsafe file capability.

Core 0 explicitly does not include GraphQL, Redis sessions, Meilisearch search,
runtime plugins, model calls, playback, transcoding, file mutation, automatic
cross-directory ReleaseGroup merges, or full Event Sourcing.

## Scope Test

A proposed Core 0 change is aligned when it directly closes the initialize,
authenticate, register, scan, browse, search, or reversible-directory-management
loop and preserves the invariants. It is misaligned when it adds infrastructure
only because the V0 repository once had it or because a future plugin might use
it.

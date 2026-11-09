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
不改变本层现有 API、表结构或权限。B0（行为保持拆包）、阶段 I（身份稳定、按组只读接口）、M2a/M2b（播放与派生）、P（播放历史）与 M3
（修正覆盖层与受治理操作，见[受治理操作合同](./governance-contracts.md)）已完成；M4 的第一项（默认版本与版本对比，见[默认版本合同](./default-version-contracts.md)）与第二项（问题处理，见[核对合同](./issue-review-contracts.md)）已实施；
M4 的第三项（认领，见[认领合同](./claim-contracts.md)）同月实施；M4 的其余各项（扫描反馈、合并拆分）、通用审批、editor 角色和 Agent 的实施细节须经对应子任务与 ADR 项批准后再实施。Core 0 限制不是未来阶段的默认授权。

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
| [数据库验证](./database-verification.md) | 隔离库、执行器与迁移回退往返验证 |
| [Database Guidelines](./database-guidelines.md) | PostgreSQL ownership, transactions, migrations, revisions, and idempotency |
| [Error Handling](./error-handling.md) | Classified application errors and stable REST responses |
| [Logging Guidelines](./logging-guidelines.md) | Structured JSON events, correlation, and redaction |
| [Core 0 当前运行合同](./core0-runtime-contracts.md) | 总览：平台层 REST 通则、HTTP 缓存、迁移、环境、CI、完成日志，以及专题文件表（后续技术债在 [core0-tech-debt.md](./core0-tech-debt.md)） |
| [身份与成员合同](./identity-contracts.md) | 密码规则、初始化与成员接口、用户停用事务、写入失败映射 |
| [来源与会话 Cookie 合同](./identity-origin-contracts.md) | 三模式配置、Host 规范化、守卫装配、Cookie Secure 与来源拒绝日志 |
| [音乐目录浏览合同](./library-folder-contracts.md) | 管理员逐级浏览允许范围内的子文件夹、登记标记、错误与截断 |
| [目录与扫描流程合同](./library-scan-contracts.md) | 范围、接口签名、目录幂等、路径过滤、排除子文件夹、保住登记；拆分文件目录 |
| [扫描流程](./library-scan-pipeline.md) | 扫描完整性、暂存、身份、CUE 与封面，根保存后的收藏转移，并发解析（⑤），搜索投影挂接 |
| [增量扫描](./library-scan-incremental.md) | 增量扫描（④）：解析缓存、延迟封面、版本号与升版本、借用指纹、候选签名与跳过 |
| [归组与发行](./library-scan-grouping.md) | 发行候选、抓轨与封面，碟号与独占合成，文件名推断与同目录归组（③），MP3 时长，新格式指纹 |
| [标签与古典作品](./library-scan-tags.md) | 标签多值、署名与合辑（⑥⑦），古典作品与乐章（⑧） |
| [扫描校验矩阵与测试](./library-scan-checks.md) | 扫描合同第4–6节：校验与错误矩阵、案例、必需测试 |
| [目录与扫描流程验证与示例](./library-scan-verification.md) | 扫描合同第5–7节的补充案例、测试与错误示例，②③验证 |
| [扫描反馈合同](./scan-feedback-contracts.md) | M5：全根计数、逐根加权进度、报告、最近新增、反馈降级与治理锁顺序 |
| [扫描协调合同](./scan-coordination-contracts.md) | 扫描状态与记录、持久化取消、跨进程协调锁 |
| [真实库 Smoke 合同](./real-library-smoke-contracts.md) | 真实音乐只读验收与 V0 对照 |
| [搜索规范化与在线投影合同](./search-contracts.md) | M5 ④字符规范化、内置 OpenCC、后台回填、事务双写、两栏搜索和有界计数 |
| [艺人身份与浏览合同](./artist-contracts.md) | M5 ⑤：名字键、署名与治理、整理期、引用、队列及收藏端口 |
| [目录批量摘要合同](./catalog-summary-contracts.md) | 历史/个人数据安全摘要、批量上限和缺失语义；资料库统计接口 `catalog/stats` |
| [Catalog REST 合同](./catalog-rest-contracts.md) | Release 列表/详情/evidence、按组接口、封面摘要、可见性和错误合同 |
| [专辑多流派合同](./genre-contracts.md) | 0032：多值标签、流派合集、读取数组、修正与历史撤销兼容、双向迁移 |
| [Catalog REST 验证与示例](./catalog-rest-verification.md) | Catalog REST 的正确/基准/错误案例、必需测试与断言点（含 M5 统计与个人数据批量读取、EXPLAIN 诊断）、错误与正确示例 |
| [播放直出合同](./playback-contracts.md) | M2a 格式表、预检、音频 Range、安全打开与停机中止；M2b 的 `delivery`、202 与派生错误码 |
| [无损派生合同](./playback-derivation-contracts.md) | M2b ALAC → FLAC 无损派生：配置与启动、缓存、转换与核对、上限与取消、测试 |
| [内嵌歌词合同](./lyrics-contracts.md) | M2b 歌词：标签提取与规范化、`tracks.lyrics` 与约束、歌词接口、LRC 解析规则、测试 |
| [后台音频分析合同](./audio-analysis-contracts.md) | 0033：`track_analyses` 与待分析判定、单线程低优先级工作者、ffmpeg 命令与自检、`ROOMUSIC_AUDIO_ANALYSIS`、按 Roon 的增益公式、分析结果与进度接口、测试 |
| [播放区数据与队列合同](./zones-data-contracts.md) | B1：0043 三表、纯队列语义、用户隔离读取与调用方事务写入 |
| [播放区控制面合同](./playback-zone-contracts.md) | B2/B3：设备与命令、渲染端口、SSE、输出端租约、单次取流、事件应用与服务端计次 |
| [播放历史合同](./listening-contracts.md) | 阶段 P：`play_history`、计次规则与服务端校验、三个接口、最近播放读取规则、前端计次与上报、测试 |
| [本人收藏与歌单合同](./personal-contracts.md) | M5 P 线：typed 收藏、歌单间隔键/revision、稳定收据、适应撤销、队列限量与精确统计 |
| [受治理操作合同](./governance-contracts.md) | 阶段 M3：操作记录与共同写入流程 `operations.Run`、发行字段修正与有效值视图、人工归组、撤销、操作记录接口、错误码、测试与真实服务用例 |
| [默认版本与版本对比合同](./default-version-contracts.md) | M4 第一项：迁移 0018、组的默认版本与版本号、生效与失效规则、设定接口与执行器、撤销与展示、版本对比接口、测试 |
| [元数据问题与核对合同](./issue-review-contracts.md) | M4 第二项：迁移 0019、“需要关注”的三类拆分、依据与依据令牌、核对接口与执行器、撤销与展示、问题列表与版本的问题、测试 |
| [认领合同](./claim-contracts.md) | M4 第三项：迁移 0020、待认领的判定与推荐、预览与令牌、认领的执行器与撤销、读取接口、测试 |
| [备份管理合同](./backup-management-contracts.md) | 删除与批量删除备份、保留规则（天数 / 份数上限、备份目录里的规则文件、未确认不删的保存语义）、测试 |
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
- [Current Compose services](../../../compose.dev.yaml)
- [Architecture canvas](../../../docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx)

The Compose file includes Redis and Meilisearch as development/future services;
the Core 0 PRD explicitly makes them optional.

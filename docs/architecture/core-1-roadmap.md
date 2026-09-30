# Classical / Core 1 能力边界与实施路线

## 状态与阅读方式

核对日期：2026-09-28；代码基线：`66678d6`。已批准的是 M0 文档整理，以及 2026-09-28 的路线修订
裁决 D1–D6（选择者 qlqq，记录于任务 `09-28-core-1-roadmap-revision` 的 PRD，评估依据见该任务的
`research/architecture-evaluation.md`）。**批准的是阶段顺序、范围和所列方案，不是一次性实施下列全部
功能或依赖。** 设计规则见 [DESIGN.md](../../frontend/DESIGN.md)，架构总览见
[架构决策摘要](architecture-decisions.md)，图示见 [Core 1 目标架构图](core-1-architecture.html)。

- **当前事实**：以实际注册 API、数据库迁移、源码和 CI 为准，原型不构成运行证据。
- **保留合同**：只读源文件、后端鉴权/路径/事务权威、来源可解释、保守分组及完整扫描才进行缺失对账。
- **已批准决策**：D1–D6 及各 ADR“决策记录”中列明的项；每个阶段仍需在自己的子任务中完成规划与验收。
- **候选方案**：ADR 中仍标为待批准的项，须在对应阶段规划时另行批准才能实施。
- **历史资料**：旧视觉、本地模拟播放器和本地个人库只用于提取适用经验，不自动继承其实施授权。

## 当前基线

后端入口位于 `backend/cmd/roomusic`，能力代码位于 `backend/internal/`（B0 于 2026-09-30 完成拆分，见
[后端目录结构](../../.trellis/spec/backend/directory-structure.md)）。阶段 I（2026-09-30）已实施身份稳定：扫描暂存表归 catalog，
`catalog.PlanStagedRoot` 在写入前规划曲目与 Release 的沿用——曲目按来源身份与音频内容指纹延续，同一 root 遍历完整时
改名、移动、互换按指纹唯一 1:1 保守重绑定；Release 按曲目成员关系匹配，`candidate_anchor` 只是位置信息，
每个新 Release 仍新建一个 ReleaseGroup；`GET /api/v1/release-groups` 与 `/{id}` 提供按组只读读取
（M3 人工归组之前每组一个版本）。生产部署仍由一个 Go 应用提供 REST 与同源 Web 资产，PostgreSQL
是唯一必需业务权威。

`frontend/src/` 已删除，保留工具链配置和依赖声明；Go 内嵌资源是重置占位页。
`.github/workflows/ci.yml` 在没有 `frontend/src/main.tsx` 时跳过真实构建与资产漂移检查，
所以 CI 通过不代表已有可用客户端。Classical 原型与 `_ds` 设计系统已经入库，源目录不搬迁。

## 页面 / API / 缺口矩阵

下列路径均相对于 `/api/v1`，是已注册的现有接口；表中没有的新能力不通过假数据补齐。

| 页面或用户动作 | 当前 API / 数据证据 | M1 可交付的真实范围 | 缺口与所属阶段 |
| --- | --- | --- | --- |
| 初始化与会话 | `GET /setup/status`、`POST /setup/admin`、`POST /auth/login`、`POST /auth/logout`、`GET /auth/me` | M1a：初始化关闭、登录/退出、失效恢复和 401/403 区分 | 无需新增认证方案；不引入浏览器 bearer token |
| 专辑库、基础搜索 | `GET /releases` 的 `q`、`attention=required`、`page`、`page_size` | M1b：使用阶段 I 的按组只读接口，列表、筛选、分页和可恢复 URL 状态；一组一个版本时自然退化 | Core 1 不做自动跨版本归组（仅人工确认，M3 起）；搜索规范化、别名和艺人属于 M5 |
| 发行详情与封面 | `GET /releases/{id}`、`GET /artworks/{resource_id}` | M1b：组详情与版本标签（单版本退化）、Medium/Track 层级、现有字段/credits、封面空态与错误态 | 默认版本与版本对比属于 M4；作词/编曲角色需额外数据合同；歌词在 M2b |
| 来源与待关注信息 | 管理员 `GET /releases/{id}/evidence`，普通详情含安全摘要 | M1b：按现有权限查看证据；仅 `attention` 筛选不能冒充问题工单 | 忽略/修正/审批等处理状态属于 M3/M4 |
| 目录管理 | `GET/POST /library-roots`、`PATCH /library-roots/{id}`、`POST /library-roots/{id}/restore` | M1a：管理员新增、停用、恢复；保留 revision/幂等与安全路径合同 | “只读客户端”不禁用这些已有管理操作；音乐文件仍不写入；幂等指纹的目标绑定已由 F2 修复（2026-09-29） |
| 扫描目录 | `POST /scans`、`GET /scans/active`、`GET /scans/{id}`、`POST /scans/{id}/cancel`、`GET /scans/{id}/diagnostics` | M1a：活动扫描、运行/终态、取消请求和受限诊断 | 无完成百分比、已扫描文件进度、暂停/续跑、定时/监听、完整历史列表或新增专辑差量；增强单独拆分 |
| 成员 | `GET/POST /users`、`PATCH /users/{id}`、`POST /users/{id}/sessions/revoke` | M1a：当前 admin/user 范围内的创建、禁用和会话撤销 | `editor` 是 M3 候选，不在前端单方面授予 |
| 日志 | `GET /library-root-operations` | M1a：如展示，只称目录操作历史，沿用当前有界结果 | 不是通用日志、审批队列或任意检查点恢复；M3 扩展 |
| 概览 | 可组合已公开的 Release 分页总数、活动扫描和有权限的目录状态 | M1a：有真实来源的摘要与跳转，缺少数据就省略 | 不生成“本次新增 N 张”“全部扫描历史”“收藏总数”或 AI 便笺等不存在的统计 |
| 正在播放、队列、歌词 | 当前无曲目串流或歌词接口 | 隐藏或明确禁用，不能启动模拟时钟或记录假历史 | M2a：直出播放与浏览器能力判定；M2b：ffmpeg 派生、CUE 分轨与歌词；P：阶段 I 之后的服务端播放历史 |
| 元数据整理、版本学 | 扫描有字段候选/决策；曲目按内容指纹、Release 按成员关系保持 ID（阶段 I）；新 Release 各自一个 group，已有按组只读接口 | 只显示已有 Release 与证据，不自动归并为“同专辑” | 阶段 I 已完成（2026-09-30）；M3：修正覆盖层与组成员调整（人工确认）；M4：问题、版本工具，之后 Release 合并/拆分 |
| 我的音乐、艺人、Steward | 当前没有个人库、艺人实体/别名或 Steward 运行时接口 | 不把原型入口伪装可用 | P：播放历史（Core 1）；M5：收藏、歌单、历史页面、艺人与搜索规范化；M6：受控 Agent；外部元数据源另立项 |

接口依据：`backend/internal/app/app.go` 与各 `httpapi` 包的 `Register`（路由表）、`internal/library/scans.go` 的 `Scan`、
`internal/catalog/projection.go`、`internal/library/httpapi/root_operations.go`、`0006_multi_user.sql` 和
`0012_track_credits_and_release_metadata.sql`。精确请求/错误合同继续由
[运行合同](../../.trellis/spec/backend/core0-runtime-contracts.md)与
[Catalog REST 合同](../../.trellis/spec/backend/catalog-rest-contracts.md)维护，不复制为第二份 DTO 定义。

## Core 1 范围（已批准）

2026-09-28 由 qlqq 裁决（D4）：**Core 1 = M0、B0、I、M1a、M1b、M2a、M2b、P（服务端播放历史）、
M3、M4**，外加两项独立修复 F1、F2。阶段完成结果是“可用客户端 → 真实播放 → 可审计的元数据与
版本管理”。收藏、歌单、搜索规范化与艺人（M5）和 Music Steward（M6）进入后续阶段；外部元数据源
不作为 Core 1 前置。M0 曾列出的较小替代（先发 M1 + 必要 B0 + M2）未被选择，但 M1 与 M2a 仍须能
独立交付，不等待整条路线完工。

服务端播放历史（P）排在阶段 I 之后：它引用曲目 ID，必须等身份规则稳定；它只是真实播放的最小
用户隔离记录，历史页面、收藏和歌单属于 M5。

## 阶段、依赖与验收出口

| 阶段 | 独立交付 | 前置与允许并行 | 验收出口 | 任务 |
| --- | --- | --- | --- | --- |
| M0 | 设计依据、能力矩阵、四类 ADR 草案、规范/旧任务承接 | 本轮文档授权 | 现状与提案区分清楚，没有业务变更 | 已完成（`850d274`） |
| B0 | 纯重构：先拆 `internal/catalog`（organizer、Release 图写入、读取查询），再拆目录与扫描（含解析器），最后拆 platform | 无；不阻塞 M1a | 对外接口、扫描、安全与事务行为不变；每步 `-race` 测试与 PostgreSQL 集成测试通过 | `classical-b0-package-split`（2026-09-30 已完成，`b4cda28`–`e452105`） |
| I | 身份稳定：音频指纹、同 root 保守重绑定、按曲目成员关系匹配 Release、ID 稳定性集成测试；ReleaseGroup 只读接口 | B0 的 catalog 部分 | 重扫、增删文件、同 root 内改名/移动后 UUID 不变；歧义不重绑定；`/releases` 行为不变 | `classical-i-identity-groups`（2026-09-30 已完成，`7260178`–`75ef107` 加文档同步） |
| M1a | Classical 外壳、初始化/会话、现有目录/扫描/成员管理、简版概览；恢复 CI 构建与内嵌资产检查 | ADR 0001 第 1–3 项已批准；可与 B0、I 并行 | 真实 API 主路径、权限/错误/空态、键盘/窄屏、实际构建及内嵌资产检查通过 | `classical-m1a-shell-admin` |
| M1b | 专辑列表与详情（按组形态，单版本自然退化） | 阶段 I 的按组接口；M1a 的外壳与数据层 | 同 M1a 质量门禁，另覆盖长标题、缺封面、单版本、多 Medium | `classical-m1b-album-browsing` |
| M2a | 鉴权直出串流、播放引擎、浏览器能力判定；不可播放的曲目明确标注 | M1a 外壳；ADR 0004 第 1 项的实播矩阵在 M2a 规划时确定 | 约定浏览器/格式可播放、拖动/取消/错误可恢复、切页不断播，不宣称硬件 Bit-perfect | 未立项 |
| M2b | ffmpeg 无损派生缓存、CUE 精确分轨、歌词 | M2a；ADR 0004 第 2–7 项（含许可与部署）获批 | ALAC 与 CUE 曲目在约定浏览器可播放；缓存有上限、可取消 | 未立项 |
| P | 服务端播放历史（按用户隔离） | 阶段 I；M2a 产生真实播放 | 只记录真实播放；重扫与改名后仍指向同一曲目 | 未立项 |
| M3 | 修正覆盖层、受治理操作、通用日志/审批/检查点与候选角色扩展；试点为字段修正与 ReleaseGroup 成员调整 | 阶段 I；ADR 0002、0003 相关待批项获批；与 M2 无必然顺序 | 重扫不覆盖修正、越权拒绝、并发冲突、幂等与支持的撤销有证据 | 未立项 |
| M4 | 本地可解释问题、版本对比与默认版本、扫描反馈；之后再做 Release 合并与拆分 | M3 | 证据可追溯，结构变更走统一执行；无自动弱相似合并 | 未立项 |
| M5 | 搜索规范化、艺人实体与别名、收藏、歌单、历史页面 | M4（别名修改走受治理操作） | 按用户隔离、真实服务端持久化，不重建本地演示系统 | Core 1 之后 |
| M6 | 只读 Steward → 受控提案 → 独立审查 | M3、M4 的工具、权限与执行边界；另需覆盖提示注入与数据外发的 ADR | Agent 只提供意图/证据，不能自批危险操作或绕过后端 | Core 1 之后 |
| F1 | 未知 `/api/` 路径返回 JSON 404 | 无，随时可插入 | 非 API 路径仍回落到 `index.html`；有回归测试 | `classical-f1-api-json-404`（2026-09-29 已完成） |
| F2 | 目录操作的幂等指纹纳入目标目录 ID；同键并发串行；旧记录不兼容 | 无；建议先于或与 M1a 同期完成 | 跨目标同键返回冲突且无副作用；同目标重放返回原结果；同键并发只执行一次 | `classical-f2-root-op-idempotency`（2026-09-29 已完成） |

“任务”列中的名称是父任务 `09-27-classical-core-1` 下的子任务；除标注已完成的外，均为 planning 状态，尚未启动。
扫描进度/报告、定时/增量扫描是 M4 周边候选，不是版本治理的逻辑前置；创建子任务时应
按最小可验收能力拆开。B0 不夹带新增端点、schema、业务规则或行为修复；`/api/` JSON 404 与
目录幂等指纹分别由 F1、F2 单独修复。

## 静态审查发现的现有风险

`internal/library/httpapi/root_operations.go` 的停用/恢复指纹原先没有包含路由 root ID，重放查询只按 actor、操作类型
和幂等键查记录；同键同 body 用于不同目录时会返回前一个目标的结果。已记录于
[ADR 0003](adr/0003-governed-operations.md)与运行合同技术债，**2026-09-29 由 F2 动态复现并修复**：
指纹含目标，同键请求串行，旧记录不做兼容。修复没有与纯拆包混合，也没有等通用操作平台。

## ADR 索引与决策门禁

| ADR | 决策主题 | 裁决状态（2026-09-28） | 必须在哪一阶段前解决其余待批项 |
| --- | --- | --- | --- |
| [0001 前端基础](adr/0001-frontend-foundations.md) | Router/Query、样式/字体分发、版本固定与状态责任 | 部分批准：第 1–3 项与接口防漂移做法（D3） | 第 4–6 项在 M1a 规划时确定 |
| [0002 元数据覆盖层](adr/0002-metadata-overlays.md) | 稳定身份、观察/覆盖/有效值、重扫与重分组 | 部分批准：第 2 项身份策略（D2）、第 4 项归组依据（D6） | 第 1、3、5–7 项在阶段 I 与 M3 规划时确定 |
| [0003 受控操作](adr/0003-governed-operations.md) | 角色、风险、审批、revision/幂等、检查点与逆操作 | 部分批准：第 1 项中 M3 首批试点范围（D1） | 第 2–7 项在 M3 规划时确定 |
| [0004 媒体交付](adr/0004-media-delivery.md) | 浏览器能力、串流、ffmpeg、缓存、部署与许可核验 | 部分批准：第 1 项中“仅直出”作为 M2a 首发范围（D1） | 实播矩阵在 M2a 规划时确定；第 2–7 项在 M2b 之前确定 |

文档批准与技术采纳分开记录。截至本次修订，没有新库安装、锁文件变化、数据库迁移、媒体处理程序、
权限扩展或 Agent 运行时；已批准的方案在对应子任务中实施，具体版本与支持矩阵以子任务验证结果为准。

## 旧任务承接与原型处理

`09-04-music-client-experience` 已于 2026-09-28 按 D5 标记为已取代（meta：`outcome=superseded`、
`superseded_by=09-27-classical-core-1`）并归档。归档工具会把状态写成 `completed`，但该任务的代码从未
提交，不代表已交付；原批准与验证记录保留在归档目录。下面说明的是新路线的承接方式，不是旧任务的验收结果。

| 旧内容 | 承接方式 |
| --- | --- |
| 月刊/唱片店布局与 hash 路由 | Classical 设计锚替代旧视觉指令；M1a 使用 TanStack Router 的路径路由，旧 hash 链接是否兼容由 M1a 决定 |
| 模拟时钟、假音量、演示入口 | 不恢复；M1 不伪装播放，M2a 以真实媒体事件驱动 |
| 浏览器本地收藏/歌单/历史 | 不作为权威存储；播放历史在 P 由服务端按用户隔离保存，收藏与歌单在 M5；旧本地数据不迁移 |
| 立即播放/下一首/加入队列、循环/随机 | 保留为未来真实播放器的行为约定，见[播放器规范](../../.trellis/spec/frontend/player-design-guidelines.md) |
| 账号隔离、损坏存储、焦点/键盘、长标题 | 保留安全和可访问性要求，不把旧实现视为现存代码 |

`frontend-desgin/` 拼写、原始 `.dc.html`、`_ds` 与 `support.js` 全部保留。是否改名为设计归档目录、
是否移除工具运行时，仍需独立确认。原型中的样例数据和功能承诺通过 DESIGN 的落地差异表处理，不直接改源稿。

## 验证与后续任务创建

- 文档修订：检查 Markdown 链接、ADR 状态、task JSON/JSONL、父子关系、变更白名单和空白；不跑全量业务测试。
- B0 及以后：在各自 PRD/执行计划中列出真实测试名和最小命令，含相关失败路径；不能用跳过数据库测试充作事务验证。
- 只为下一个获批的独立交付创建任务。2026-09-28 已为 B0、I、M1a、M1b、F1、F2 创建子任务；先后依赖写在各子任务中。
- 在任何迁移、依赖安装或代码实现前，确认其所属阶段与相关 ADR 项已批准；路线图不是一次性执行所有阶段的指令。

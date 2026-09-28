# 2026-09-27/28 架构评估记录

本文归档 2026-09-27 至 09-28 围绕 Classical 原型与 Core 1 路线的讨论。代码事实以基线
`66678d6` 为准；“提案”与“待确认”条目在用户裁决前不构成实施授权。

## 一、时间线与来源

| 时间 | 事件 | 产物 |
| --- | --- | --- |
| 09-27 | 依据 `frontend-desgin/` 原型和当时代码，给出第一轮架构与 M0–M6 路线建议 | 会话记录 |
| 09-27 | M0 落地：新 `frontend/DESIGN.md`、spec 对齐、ADR 0001–0004 草案、能力矩阵与路线提案 | 提交 `850d274`，任务已归档 |
| 09-27 | 用户转来外部设计工具 AI 的三轮建议（基于 GitHub 快照，`frontend-desgin/github.md` 记录同步时间为 `2026-09-27T15:03:44Z`），逐条对照仓库核实 | 本文第三节 |
| 09-28 | 制作架构图；澄清后端尚未模块化；澄清最终结构为“模块化单体 + 受控扩展” | `docs/architecture/core-1-architecture.html`（未跟踪） |

外部工具的快照早于删除旧 DESIGN.md（`2175e24`，15:10:38）和 M0 提交（18:11:14），
这是它若干事实判断过时的原因。

## 二、已核实的仓库事实

| 事实 | 依据 |
| --- | --- |
| 后端全部位于一个 `package main`：26 个文件，非测试代码约 8,074 行；没有 `backend/internal/` | `backend/cmd/roomusic/*.go` |
| `roomusicApplication` 挂 52 个方法，分布在身份、扫描、catalog 读取等 5 个文件 | `application.go`、`security.go`、`scans.go`、`scanner.go`、`catalog_api.go` |
| SQL 分散在 7 个文件，包括 HTTP handler；例如 `listUsers` 直接查询 `users` 表 | `application.go:340`、`:344` |
| 全部非测试代码只有 2 个接口，都是局部测试缝，不是模块端口 | `scanner.go:25`（`scanExecutor`）、`scans.go:323`（`scanDiagnosticsQuerier`） |
| `release_groups` 表及外键早已存在，但只有 `id`、`created_at`；扫描器给每个新 Release 新建一个组，没有跨版本归组写路径 | `0003_catalog_observations.sql`、`scanner.go:929` |
| organizer 的 `ReleaseGroupKey` 只是候选分桶键，持久化时没有用它归组 | `organizer.go:633` |
| 扫描器更新已有 Release 时直接覆盖标题、艺人、厂牌等字段，但不改 `group_id` | `scanner.go:934` |
| 物理曲目身份 = root + `physical:v1` + 相对路径 | `scanner.go:882` |
| Release 锚点 = root + `v2` + 候选种类 + 范围 + 分区 | `organizer.go:1076` |
| 零散文件、同目录拆分候选的分区取组内排序最小的相对路径，增删文件就可能换 Release ID | `organizer.go:1006`–`1018`；ADR 0002 第四节已记录 |
| 未匹配的 `/api/` 请求落到前端兜底，返回 200 和 `index.html` | `application.go:79`、`main.go:114` |
| 搜索是 `ILIKE` 子串匹配；列表逐行计算待关注数 | `catalog_api.go:207`–`215`、`catalog_api.go:37` |
| 远端只有 `main`；旧任务 `09-04-music-client-experience` 的代码从未提交；`frontend/src/` 不存在 | `git ls-remote --heads origin`、旧任务 `verification.md` |
| 扩展边界已有原则：插件只能通过已发布能力，不能自授权或直接改核心状态；能力注册表与插件宿主是远期设施；`extensions/`、`plugins/` 尚不存在 | `docs/architecture/architecture-decisions.md` “Extension boundary” |

## 三、对外部建议的逐条评估

| 建议 | 结论 | 说明 |
| --- | --- | --- |
| 身份先行：重扫、目录改名、增删文件后 ID 都不变 | 采纳方向，细化方案（待确认） | 需要“按曲目成员关系匹配 Release”加“同一 root 内唯一且音频完全相同才重绑定”；与 ADR 0002 验收表中“重命名、移动 → 新旧身份分离”冲突，需修订 ADR |
| 元数据分观察 / 覆盖 / 结果三层 | 与 ADR 0002 一致 | 覆盖挂在实体 UUID 上，不挂锚点或来源身份字符串 |
| 趁早做通用变更日志 | 与 ADR 0003 一致；不采纳其线性状态链 | 批准状态与执行状态分开保存（`state-management.md` 语义示例含 `not_required`）；Change Set 不替代后端工具白名单 |
| 拆包先拆 catalog | 采纳（待确认） | 替代原“解析器先拆”；B0 仍是不改行为的纯重构，身份规则改动放入阶段 I |
| ReleaseGroup 前移 | 只前移只读部分（待确认） | 组属性与按组接口前移，让 M1 专辑页一次成形；合并、拆分仍走受治理操作 |
| 播放先直出 | 采纳为 M2a（ADR 0004 “替代 A”，待确认） | Chrome/Firefox 下 ALAC 与 CUE 虚拟轨在 M2a 不可播，必须明确标注；ffmpeg 派生与歌词放 M2b |
| 个人数据等身份稳定后上服务端 | 采纳 | 播放历史也一并移到阶段 I 之后 |
| Steward 最后做 | 采纳，补充 | 标签、文件名、歌词是不可信文本（提示注入入口）；元数据发往云端模型是隐私决定；两者需在 M6 的 ADR 覆盖 |
| 搜索：pg_trgm + 规范化字段 + `artist_aliases` | 部分采纳 | trigram 只加速子串；异体字靠规范化；译名需要 ReleaseGroup 标题别名 |
| 待关注数在扫描结束时预先算好 | 采纳，归入 M4 问题投影 | 受治理操作生效时也要刷新；当前规模不急 |
| M3 试点：字段修正 + 艺人合并 | 修正（待确认） | 艺人尚无实体；改为“字段修正 + ReleaseGroup 成员调整”，后者只改 `group_id`，天然不被重扫冲掉 |
| 前端：拆 `main.tsx`、换 hash 路由、`LibraryRepository` 接缝 | 前提不成立 | 前端从零搭建，第一天按目标结构组织；个人数据直接做服务端 |
| 前端：Router + Query、播放引擎放在 React 之外 | 与 ADR 0001 一致 | 产品内不做模拟播放，fake 只用于测试 |
| 由 Go 端生成 OpenAPI | 暂不采纳 | 手写 net/http handler 没有生成来源；M1 先用“Go 集成测试产出 JSON 样本 → 前端 decoder 测试读取 → CI 检查漂移”，M6 设计工具 schema 时再评估 |
| 事实判断：无 `release_groups` 表、DESIGN.md 冲突、代码在 feat 分支、需补 `index.html` 兜底 | 过时或错误 | 见第一、二节 |

## 四、对本会话早先建议的更正

- 修正覆盖层不应以 `candidate_anchor` 或 `source_identity` 为主键，它们随路径变化；应挂实体 UUID，并由阶段 I 保证 UUID 稳定。
- B0 顺序由“解析器 → 平台 → 身份 → 目录/扫描 → catalog”改为“catalog 先拆，再拆目录与扫描”。
- ReleaseGroup 只读接口从 M4 前移到阶段 I。

## 五、实测记录

在开发库 `roomusic-postgres-1` 中以 `BEGIN … ROLLBACK` 测试，扩展未残留。环境为
PostgreSQL 18.6（Alpine/musl），locale provider 为 libc，`datctype` 为 `en_US.utf8`。

- `show_trgm` 能从“千と千尋の神隠し”“久石譲”“周杰伦”中切出 trigram。
- `similarity('久石让','久石譲') = 0.33`，`similarity('千与千寻','千と千尋の神隠し') = 0.08`。
- 结论：换镜像或 locale 可能改变行为，搜索阶段应加一条断言中日文 trigram 可切分的测试。

## 六、扩展边界结论

- 最终结构：不可替换内核（身份与权限、变更管理、工具授权、持久化）+ 第一方模块（同一二进制内的 `internal/` 包）+ 远期扩展层（能力注册表 → 插件宿主 → 格式解析器 / 元数据与封面提供方 / Agent 与审查 / 执行插件）。
- 插件只能从两个口子进入：观察层（解析器与提供方的产出只作候选或证据）和意图侧（Agent 只产出 Change Set，执行插件只能由 operations 在授权包裹下调用）。任何插件都不能直接写结果层或给自己授权。
- 两步走：Core 1 期间只做“插件形状”，即模块自有窄接口加进程内适配器（ffmpeg、MusicBrainz、模型服务）；出现第三方或仓库外扩展需求时再建插件宿主，优先独立进程加类型化 RPC。不用 Go `plugin` 包（要求工具链与依赖版本完全一致、仅支持 Linux/macOS、不能卸载）；只有运行不受信任的第三方代码时才考虑 WASM。执行插件排在最后，并受音乐目录默认只读的不变量约束。

## 七、架构图

`docs/architecture/core-1-architecture.html` 是自包含 HTML，共五节：系统分层、元数据三层与身份、
受治理写路径、前端结构、路线与依赖。边框样式区分“已有 / 草案 / 待确认 / 后续”，悬停方框可
高亮相关依赖。已在 1440、1100、820 宽度下用无头 Chromium 渲染，无脚本错误。后端方框已标注
“能力已有 · 未拆包”。尚未提交，也还没有“扩展边界”一节。

## 八、修订后的阶段顺序（提案）

| 阶段 | 内容 | 硬依赖 |
| --- | --- | --- |
| B0 | 纯重构：catalog 先拆，再拆目录与扫描 | 无 |
| I | 身份稳定（成员关系匹配、同 root 保守重绑定、ID 稳定性集成测试）与 ReleaseGroup 只读接口 | B0 的 catalog |
| M1 | 外壳、认证、目录、扫描、成员、概览；专辑页最后做，直接使用按组接口 | 专辑页依赖 I，其余可与 B0、I 并行 |
| M2a / M2b | 直出播放与能力判定 / ffmpeg 派生缓存、CUE 分轨、歌词 | M2a 依赖 M1 外壳；M2b 依赖 ADR 0004 的许可与部署审核 |
| M3 | 修正覆盖层与受治理操作；试点为字段修正和 ReleaseGroup 成员调整 | I |
| P | 服务端个人数据：播放历史、收藏、歌单 | I；播放历史还依赖 M2a |
| M4 | 问题投影、版本对比与默认版本、扫描反馈；之后再做 Release 合并与拆分 | M3 |
| M5 | 搜索规范化、艺人实体与别名 | M4 |
| M6 | Music Steward：只读问答 → 提案 → 独立审查 | M3、M4，另需覆盖提示注入与数据外发的 ADR |
| 独立修复 | 未知 `/api/` 返回 JSON 404；目录操作幂等指纹补上目录 ID | 无，随时可插入 |

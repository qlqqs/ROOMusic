# 技术设计

本任务只改文档与任务记录。第一部分是修改设计，第二部分是下一批子任务的详细规划，
在实施阶段转写为各子任务的 PRD 初稿。裁决编号 D1–D6 见 [PRD](prd.md)。

## 一、修改范围

| 文件 | 修改内容 | 依据 |
| --- | --- | --- |
| `docs/architecture/core-1-roadmap.md` | 状态节改为 09-28 核对；阶段表改为 B0 / I / M1a / M1b / M2a / M2b / P / M3 / M4 / M5 / M6 / 独立修复；Core 1 截止范围改为已批准；B0 顺序改为 catalog 先拆；播放历史移出 M2（第 38、58、67、68 行等）；ADR 门禁表加状态列；旧任务承接节写明已取代归档；链接评估记录与架构图 | D1、D4、D5 |
| `docs/architecture/adr/0001-frontend-foundations.md` | 增“决策记录”：第 1–3 项已裁决，新增接口防漂移（JSON 样本）；第 4–6 项仍待批（第 181 行所在清单逐项标注） | D3 |
| `docs/architecture/adr/0002-metadata-overlays.md` | 增“决策记录”：身份采用成员关系匹配 + 同 root 保守重绑定，改写第四节推荐规则与第九节验收矩阵中“重命名、移动 → 新旧身份分离”一行；ReleaseGroup 初版仅人工确认，只读接口在阶段 I；第八节阶段安排改为“B0 不改身份，阶段 I 实施”；第十节第 2、4 项标已裁决 | D1、D2、D6 |
| `docs/architecture/adr/0003-governed-operations.md` | 增“决策记录”：M3 首批试点为字段修正 + ReleaseGroup 成员调整（第九节第 1 项部分裁决）；其余待 M3 规划 | D1 |
| `docs/architecture/adr/0004-media-delivery.md` | 增“决策记录”：接受“仅直出”为 M2a 首发范围（第 11 节第 1 项后半）；实播矩阵及第 2–7 项待 M2a/M2b 规划 | D1 |
| `docs/architecture/architecture-decisions.md` | “Classical / Core 1 决策草案”节改为“部分批准”并链接架构图；Extension boundary 补充两步走指导原则（标明为指导原则，插件立项时复核） | D1–D6、评估记录第六节 |
| `docs/architecture/core-1-architecture.html` | 状态编码改为“已批准（待实施）”；P 改为播放历史，收藏与歌单归入 M5；归组写明仅人工确认；新增“06 扩展边界”；刊头与页脚说明更新 | D1、D4、D6 |
| `.trellis/spec/guides/product-goals.md:109` | “M2 的播放历史”改为阶段 I 之后的 P | D1 |
| `.trellis/spec/frontend/hook-guidelines.md:11`–`13` | TanStack Query 由“候选”改为“已批准、M1a 安装” | D3 |
| `frontend/DESIGN.md:113`–`116`、`:270` | 字体改为已批准自托管（许可在 M1a 复核）；播放历史改为阶段 I 之后设计 | D1、D3 |
| 父任务 `prd.md`（第 40–41 行起的阶段表、决策记录）、`design.md:45`、`implement.md` 门禁 | 与修订后的阶段、截止范围和新子任务对齐 | D1、D4 |
| 旧任务 `09-04-music-client-experience` | `set-meta` 写入 `outcome=superseded`、`superseded_by=09-27-classical-core-1`；PRD 顶部加取代说明；归档 | D5 |
| 新建子任务 ×6 | B0、I、M1a、M1b、F1、F2，状态 planning，PRD 初稿取自第二部分 | D1、D4 |

### 决策记录格式

每份 ADR 在文首“状态”之后加一节“决策记录”，逐项写：日期、选择者、已批准的内容与范围、
仍待批准的内容、依据链接（本任务 PRD 的 D 编号）。ADR 整体状态改为“部分批准”，不写成“已采纳”。

### 架构图的状态编码

| 状态 | 样式 | 含义 |
| --- | --- | --- |
| 已有 | 深灰实线 | 能力已存在（后端仍注明“未拆包”） |
| 已完成 | 金色实线 + 浅底 | 已交付的阶段（M0、本任务） |
| 已批准（待实施） | 金色实线 | D1–D6 覆盖的阶段与方案 |
| 草案 | 灰色虚线 | ADR 中仍待批准的内容 |
| 后续阶段 | 点线 | Core 1 之外（M5、M6、插件宿主） |

“06 扩展边界”一节画三层结构（内核 / 第一方模块 / 扩展层），以及两个接入口：解析器与提供方插件
→ 观察层（候选与证据）；Agent 与审查插件 → Change Set；operations → 执行插件（授权包裹）；
另标出“插件不能写结果层、不能自授权”。时间线标注 Core 1 只做进程内适配器。

## 二、下一批子任务规划

### B0：拆包（纯重构，catalog 先拆）

- **目标**：建立 catalog 与 library 的包边界，让后续身份规则只改 catalog；对外行为零变化。
- **步骤**
  1. `internal/catalog`：organizer（候选组织、锚点、字段决策）、Release 图写入（从 `scanner.go:912` 的 `persistOneCandidate` 抽出，该函数至 `:1041`）、catalog 读取查询（列表、详情、证据、封面投影）。本步结束时 catalog 包不依赖 `net/http`，HTTP handler 暂留 `package main` 调用导出函数。
  2. `internal/library` 与 `internal/library/parser`：扫描运行、取消、诊断、目录操作；`audio_parser.go`、`folder_metadata.go` 进入 parser。`artist_normalization.go` 按实际调用方归属。
  3. `internal/platform`：配置、数据库与迁移执行器、HTTP 中间件与完成日志、JSON 与错误信封；随后把各 handler 移入对应能力的 `httpapi` 子包。
  4. identity 拆分成本低则同批完成，否则另列，不阻塞阶段 I。
- **约束**：不改 SQL 语义、JSON 形状、路由或迁移；不修 `/api/` 404（F1）；每步单独提交，搬迁尽量不改代码行。
- **验收**：每步后 `gofmt`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`、`./scripts/test-integration.sh` 全部通过；`cmd/roomusic` 只剩入口与装配；依赖方向为 handler → 能力包 → platform，无循环。
- **风险**：diff 大、导出命名；原 `package main` 内的集成测试需要随包拆分。按步提交，出问题逐步回退。

### I：身份稳定与 ReleaseGroup 只读

- **前置**：B0 第 1 步（`internal/catalog` 已存在）。
- **目标**：Track、Release 的 UUID 在重扫、增删文件、同 root 内改名或移动后保持不变；提供按组读取接口。
- **内容**
  1. 音频指纹：新增迁移，为 `tracks` 增加指纹及其种类/版本列。FLAC 取 STREAMINFO 的音频 MD5（非零时）加总样本数、采样率、声道、位深（`audio_parser.go:1570` 已解析该块）；其他格式的指纹在本阶段研究后确定，要求与标签无关，且只在候选配对时计算；CUE 虚拟轨 = 父文件指纹 + 区间。
  2. 同 root 保守重绑定：只在完整成功扫描中进行；候选集 = 本轮没有身份匹配的新观察 × 本轮将被置为缺失的旧曲目；只有唯一 1:1 指纹相等才重绑定（更新相对路径与来源身份，保留 UUID）；否则保持分离并记录诊断，留待人工认领；取消、失败、不完整的扫描一律不重绑定。
  3. Release 成员关系匹配：候选先解析出所含曲目的 UUID（含重绑定结果），复用“包含其多数曲目、且没有被重叠更高的候选占用”的既有 Release；锚点降为定位信息；平局规则确定且可测试。
  4. 按组只读接口：`GET /api/v1/release-groups`（分页、`q`、`attention`、present-only 语义与 `/releases` 对齐；组摘要 = 代表版本字段 + 版本数 + 封面）与 `GET /api/v1/release-groups/{id}`（组摘要 + 成员 Release 摘要）。代表版本规则：默认版本（M4 前为空）→ 最早年份 → 最早创建。Core 1 不做自动跨版本归组（D6），此前每组一个版本。
- **ID 稳定性集成测试矩阵（PostgreSQL）**：未变化重扫；新增排序更靠前的文件；删除最小路径文件；目录改名；同 root 内移动文件；两份音频相同的文件（不重绑定）；取消 / 不完整扫描；跨 root 移动（新身份）；同路径换内容（指纹不同时不静默沿用）。
- **验收**：矩阵全部通过；`catalog-rest-contracts.md` 增补按组接口；`/releases` 行为不变；可选一次显式、只读的真实库冒烟，对比重扫前后 ID。
- **风险**：锚点语义变化需兼容旧数据（参照 0011 的保守升级方式）；指纹计算成本；CUE 边界。

### M1a：外壳与管理

- **前置**：D3 已批准；可与 B0、I 并行；建议 F1、F2 先于或同期完成。
- **内容**：脚手架（TanStack Router + Query、CSS Modules、Classical 变量、Fontsource 字体及许可复核、依赖精确锁版本）；`shared/api` 传输与错误信封 decoder；认证（初始化、登录、登出、会话恢复，区分 401/403）；应用外壳（侧栏；播放条等未实现能力隐藏或明确禁用）；目录管理（新增、停用、恢复，携带 revision 与幂等键）；扫描（启动、取消、状态轮询、诊断）；成员（列表、新建、停用、撤销会话）；概览（只展示有真实来源的数据）；CI 删除重置兜底，恢复前端构建与内嵌资产漂移检查；JSON 样本防漂移。
- **留给 M1a 规划确定**：ADR 0001 第 4–5 项（会话代次、跨标签页失效、浏览器矩阵、重试策略）。
- **验收**：真实 API 主路径；权限、错误、空态；键盘与焦点；375 / 900 / 1440 宽度无遮挡；`npm run lint`、`typecheck`、`test`、`build` 通过；内嵌资产无漂移。

### M1b：专辑浏览

- **前置**：阶段 I 的按组接口；M1a 的外壳与数据层。
- **内容**：专辑列表（按组卡片、封面、搜索、待关注筛选、分页、URL 状态）；专辑详情（组头、版本标签（单版本时退化）、Medium 分段、曲目与署名、厂牌/编号/条码、管理员证据）；播放、收藏、版本对比、Steward 等入口隐藏或明确禁用。
- **验收**：同 M1a 的质量门禁，另加长标题、缺封面、单版本、多 Medium 场景。

### F1：未知 `/api/` 返回 JSON 404

- **内容**：为 `/api/` 前缀注册兜底，返回既有错误信封的 404；SPA 兜底不受影响；回归测试。
- **验收**：`GET /api/v1/不存在` 得到 JSON 404；非 API 路径仍返回 `index.html`；已知路由的错误方法按设计处理并有测试。

### F2：目录操作幂等指纹绑定目录 ID

- **内容**：先复现“同一幂等键加相同 body 用于不同目录时，返回前一目标的结果”（`root_operations.go`，路线图“静态审查发现的现有风险”）；把路由中的 root ID 纳入指纹；定义旧记录重放的兼容策略；回归测试。
- **验收**：跨目标重放被拒或按目标区分；同目标重放仍返回原结果；旧记录按兼容策略处理。

### 路线级阶段（本任务不细化）

M2a、M2b、P（播放历史）、M3、M4、M5、M6 只在路线图中更新定义、依赖与门禁，各自立项时再做详细规划。

## 三、兼容与回退

- 纯文档与任务记录变更，每类文件可单独回退。
- 旧任务归档是目录移动加 `task.json` 修改，可用 git 回退。
- 新建子任务尚未开始、没有代码，如需撤销，用 `task.py remove-subtask` 解除关联后删除目录。
- 公开仓库：指纹方案与测试矩阵只描述机制，不写真实文件名、路径或曲目清单。

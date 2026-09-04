# 现状研究：扫描结果展示

## 研究目的

为“完善前端音乐库展示”规划提供代码、规范和历史任务证据，不改变产品代码。

## 已确认的后端能力

- `GET /api/v1/releases` 已支持认证、分页、`q` 搜索和 `attention=required`；列表只投影含 present Track 的 Release。
- `GET /api/v1/releases/{id}` 已返回 Release 元数据、Medium、Track、credits、evidence 和 nullable artwork。
- `GET /api/v1/artworks/{id}` 已在 `backend/cmd/roomusic/application.go:75-78` 注册，资源通过受控 storage key 和认证读取。
- artwork 数据表 `release_artworks` 以 Release 为主键，已存在迁移 `backend/migrations/0005_release_artwork.sql`；本任务不需要新增表。
- 当前列表投影位于 `backend/cmd/roomusic/catalog_api.go:54-70`，统一的 `scanReleaseSummary` 位于 `296-320`，因此增加列表 artwork 需要同步 SQL 列顺序、Go DTO 和 REST 测试。

## 已确认的前端现状

- Release 查询状态、详情 generation、搜索 URL、扫描联动和演示播放器状态都在 `frontend/src/main.tsx:65-105`。
- 列表请求有 AbortController、页码规范化和 stale response 保护（`frontend/src/main.tsx:140-177`）。
- 详情通过 `/api/v1/releases/{id}` 请求，现有展示从 `frontend/src/main.tsx:317-341` 和 `499-569` 开始。
- 当前卡片在 `477-487` 使用标题首字占位，只有详情封面（`501-503`）使用 artwork URL。
- 当前搜索仅有输入和清除按钮（`440-446`），attention、分页和管理员扫描控件在 `453-496`。
- 当前演示队列/底栏只保存一个 Track（`97-98`、`383-386`、`573-610`），不能代表真实音频播放。

## 规范与历史决策

- `.trellis/spec/guides/product-goals.md`：Release Graph 是核心对象；播放、PWA、Meilisearch、Agent runtime 和文件写入不是 Core 0 前置条件。
- `.trellis/spec/frontend/player-design-guidelines.md`：深色工作台、发行信息高密度、桌面三栏/移动堆叠、固定播放器预留空间、语义按钮和键盘可操作。
- `.trellis/spec/frontend/state-management.md`：查询/筛选/分页属于 URL state；server state 不复制到全局 store；播放队列和 disclosure 属于本地 UI state。
- `.trellis/spec/frontend/type-safety.md`：REST payload 在 decoder 一次解码；Release 详情嵌套项有界；不得使用绝对路径或 raw cast。
- `.trellis/tasks/archive/2026-09/09-01-music-player-admin-ui/prd.md`：既有工作台已经选择侧栏、媒体库、队列、管理中心和演示播放器的整体视觉方向，但明确延期真实音频。
- `.trellis/tasks/archive/2026-09/09-01-core-0-release-artwork/design.md`：封面优先级、受控 storage key 和资源 ID 鉴权已经落地。

## 方案比较

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| 发行列表摘要增加 artwork | 一次分页请求即可渲染真实封面；与详情 DTO 一致；无前端 N+1 | 需要同步后端 SQL/decoder/测试 | 推荐 |
| 前端逐 Release 请求详情取封面 | 后端列表合同不变 | N+1、加载抖动、扫描大库时放大请求 | 禁止 |
| 只保留首字占位 | 零跨层改动 | 无法满足扫描后快速识别音乐的目标 | 仅作封面缺失回退 |
| 新增艺术家/曲目聚合页 | 导航选择丰富 | 新 API、分页、权限和状态模型；超出当前需求 | 延期 |

## 结论

最小完整方案是：为列表补充安全 artwork 摘要，前端以 Release 优先提供网格/列表两种视图、可关闭详情抽屉、Medium/Track 展示和本地演示队列；不引入真实播放或新的基础设施。


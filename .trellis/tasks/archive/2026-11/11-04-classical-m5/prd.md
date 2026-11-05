# M5：个人音乐与检索

## 目标

Core 1 之后的第一个阶段：让每个成员有自己的历史、收藏与歌单，并让搜索与艺人能按人和写法找到音乐。出口沿用路线：按用户隔离、服务端真实持久化，不重建本地演示系统（`docs/architecture/core-1-roadmap.md:96`）。本任务是父任务，只管顺序、边界与跨项验收；每一项作为子任务分别立项、规划与验收（R5：一般一页 `prd.md`）。

## 现状（2026-11-04 核对）

- **播放历史**：只有 `play_history` 表（`backend/migrations/0016_play_history.sql`，索引 `(user_id, track_id, played_at DESC)`，迁移注释说倒序完整列表到 M5 再加索引）和三个接口：记录、最近 8 个发行、全部清除（`backend/internal/listening/httpapi/handlers.go:31-33`）。没有逐条倒序分页、没有单条删除、没有历史路由；合同明确延后到 M5 的有完整历史页、删除单条、继续聆听、暂停记录、统计推荐（`.trellis/spec/backend/listening-contracts.md:11`）。
- **收藏与歌单**：没有任何表、接口、路由或导航入口（`frontend/src/app/shell/nav-items.ts:14`）。设计规则：收藏按曲目身份去重、不跨用户继承、旧本地数据不迁移（`frontend/DESIGN-components.md:87,92,139`）；个人数据只做归属授权，不进共享治理（ADR 0003）。原型有专辑级收藏按钮，§06 有“三栏账簿”与“聆听日记”两种我的音乐布局，没有歌单排序、封面、分享的规则。
- **搜索**：`ILIKE '%q%'` 匹配发行标题、艺人、专辑艺人与曲目标题（`backend/internal/catalog/read.go:26-39`），不搜作曲/指挥等 credits；没有任何全半角、繁简、变音符、假名处理，没有 pg_trgm/unaccent。“规范化”在文档里没有定义。
- **艺人**：只有字符串（`releases.artist`、`tracks.artist`、`album_artist`、`track_credits` 四种角色），没有实体、排序名；扫描时有一条硬编码别名（`backend/internal/catalog/metadata/artist.go:27-29`）。`operations` 的 target_type 只允许 release 与 release_group（`0018_release_group_default.sql:18`）。DESIGN 要求不把同名字符串自动合并成人物，不做传记与合作网络。
- **扫描反馈**（原 M4 ④）：只有名称，没有定义；候选缺口是进度、扫描报告与“本次新增”差量（`core-1-roadmap.md:33,104`）。
- **文档容量**：ADR 0002/0003 正文接近 Trellis 注入上限，决策行写在 `*-decisions.md`。

## 子项与顺序

2026-11-04 用户裁决：先做个人数据，再做搜索与艺人（理由：历史已有数据、收藏与歌单不依赖其他项，风险小、马上能用；别名要进搜索，艺人一项最大且要改扫描流程，放后面时对真实库也更熟）。

| 顺序 | 子项 | 依赖 | 规模估计 |
| --- | --- | --- | --- |
| 1 | 历史页面（2026-11-04 完成，[子任务](../11-04-classical-m5-history/prd.md)） | 现有 `play_history` | 小：按曲目汇总的分页接口（未加索引）、按曲目删除、历史页与导航 |
| 2 | 收藏（2026-11-04 完成，[子任务](../11-04-classical-m5-favorites/prd.md)） | 无 | 中：专辑与曲目收藏、各处曲目行心形、收藏页；艺人收藏由 ⑤ 接入 |
| 3 | 歌单（2026-11-04 完成，[子任务](../11-04-classical-m5-playlists/prd.md)） | 播放器的队列命令 | 中到大：增删、拖动排序、整张专辑与队列存为歌单 |
| 4 | 搜索规范化与全局搜索（2026-11-05 完成，[子任务](../11-04-classical-m5-search/prd.md)） | 无 | 中到大：规范化（含繁简）与索引、扩大字段、侧栏全局搜索 |
| 5 | 艺人与别名（2026-11-05 完成，[子任务](../11-04-classical-m5-artists/prd.md)） | ④ 的规范化函数 | 大：艺人实体、自动归一、受治理的合并/拆开/改名（R4）、扫描对接、艺人页与列表 |
| 6 | 扫描反馈（2026-11-04 完成，[子任务](../11-04-classical-m5-scan-feedback/prd.md)） | 无 | 中：计数与进度、扫描报告、概览“最近新增” |

### 一次规划、并行实现（2026-11-04 用户要求）

② 到 ⑥ 一次规划完，再多子 agent 并行实现。用户同日要求选项的推荐按使用方便与人性化来定，不按实现难度。按依赖与共享文件分三条实现线，各在独立的 git worktree 里开发，主会话逐线审阅后合入 `main`：

| 实现线 | 子项 | 合同 | 合入顺序 |
| --- | --- | --- | --- |
| P 个人音乐 | ② 收藏、③ 歌单 | [design.md](../11-04-classical-m5-favorites/design.md) | 1 |
| F 扫描反馈 | ⑥ | [design.md](../11-04-classical-m5-scan-feedback/design.md) | 2 |
| S 检索 | ④ 搜索、⑤ 艺人（④ 先交付规范化函数） | [design.md](../11-04-classical-m5-search/design.md) | 3 |

- 每条线内：后端 agent 先按合同实现，前端 agent 按合同并行；测试 agent 补测试并跑最小相关集。
- 迁移编号：各线在自己的基线上用下一个空号开发，主会话合入时按合入顺序重新编号（未发布的迁移可以改名），合入后重跑迁移与集成测试。
- 共享的前端件（曲目行心形、“添加到歌单”）由 P 线建立；S 线的搜索结果与艺人页在 P 合入后变基接入。⑤ 的艺人收藏在 P 的收藏表上以后续迁移加入。

## 入口与页面结构

2026-11-04 用户裁决：侧栏“聆听”组加一个“我的音乐”入口，页面内用页签切换历史、收藏、歌单，地址分别是 `/me/history`、`/me/favorites`、`/me/playlists`。页签随子项上线逐个出现（历史先上，只有一个页签时也保留页签结构）；不做原型 §06 的三栏账簿。

## 跨项验收

- 个人数据（历史、收藏、歌单）按用户隔离：SQL 一律带当前用户，管理员也只能读写自己的；停用成员的数据保留。
- 共享数据的写操作（艺人、别名）按 R4：接入 `operations.Run`，有反向操作的不另做撤销。
- 每个子项收尾跑 Playwright 与真实服务用例；界面改动做截图复核。

## 实施结果

2026-11-05 六个子项全部完成并归档。① 历史直接在 `main` 上交付；② ③ 经 P 线合入（`a7973f9`，迁移 0021–0022），⑥ 经 F 线合入（`899cba6`，0023–0024），④ ⑤ 经 S 线合入（`4323b55`，0025–0030）。

跨项验收（2026-11-05 对 `main` 只读核对）：

- 个人数据隔离：历史、收藏、艺人收藏与歌单的路由都在 `/api/v1/me/...` 下，用户只取自会话（`backend/internal/personal/httpapi/handlers.go:79-91`、`backend/internal/listening/httpapi/handlers.go`），`listening`、`personal` 两包的 SQL 全部带 `user_id`。两处有意例外：歌单重排的外层 UPDATE 按已按用户限定的 CTE 主键匹配（`personal/playlist_order.go:15-20`）；艺人合并、拆分与后台回填时刷新收藏的解析投影（`personal/artist_resolution.go`），不改用户收藏本身。停用只写 `disabled_at` 并撤销会话（`identity/httpapi/handlers.go:305,333`），不删数据；个人表的 `ON DELETE CASCADE` 只在删除用户行时触发。
- 共享写入：艺人合并、拆开、改名与忽略候选四个写接口都经 `operations.Run` 记录（`catalog/httpapi/artist_governance.go:145`），别名只随合并、拆开改变归属；四种操作有反向操作，不另做撤销（`governance/artist_describe.go:163-202`）。扫描时的自动整理与启动后的回填不是 HTTP 入口，按设计不进操作记录。
- 收尾验证：六个子任务的实施结果都记录了模拟 Playwright、真实服务用例与截图复核（search、artists 的通过数与截图记录在核对时补入）。

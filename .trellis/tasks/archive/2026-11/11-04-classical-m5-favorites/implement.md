# M5 ② 收藏与 ③ 歌单：执行计划

需求见 [② prd](prd.md) 与 [③ prd](../11-04-classical-m5-playlists/prd.md)，合同见 [design.md](design.md)。属于父任务的 **P 线**（[父任务](../../../11-04-classical-m5/prd.md)“一次规划、并行实现”），合入顺序第 1。分工按 `~/.claude/CLAUDE.md`：后端与所有测试由 GPT-6-Astra，前端由 Opus 5.5；子 agent 不提交，主会话审阅后在本线 worktree 里按步骤提交。

## 并行环境

- 工作目录：独立 git worktree，分支 `m5-personal`，基于当前 `main`。
- 隔离库：Compose 项目名 `roomusic-test-m5p`（`--env-file /dev/null`、绝对路径 `compose.test.yaml`、`-p roomusic-test-m5p`、空闲端口、随机库名、用完 `down -v`）；不得碰用户的 `roomusic-postgres-1`。
- Playwright 与 `scripts/playback-e2e.sh` 用 `flock /tmp/roomusic-m5-e2e.lock <命令>` 与其他线串行。
- 迁移：`favorites.sql`、`playlists.sql` 在本线基线上用下一个空号；本线第一个合入，编号通常不用改。
- 新增前端依赖 `@dnd-kit`（用户已同意）；锁文件随前端提交。

## 步骤

| 步骤 | 执行者 | 内容 | 验证 | 提交 |
| --- | --- | --- | --- | --- |
| 1 后端 | Astra | design.md §1–§8、§10：新 `personal` 与 `personal/httpapi` 包及依赖规则；两类收藏表、歌单四张表（间隔键排序、revision、幂等收据、撤销记录）；收藏切换、两栏分页、批量状态 lookup、收藏队列（超过 10,000 取前 10,000 并返回省略数）；歌单 CRUD、批量加入（去重计数）、移除与总是成功的撤销、移动、队列、`containing-track`；catalog 新公开读取（轻量曲目事实、带可用性的专辑摘要）；`shared` 请求方法加 PUT 由前端处理；接口样本（§11.2）；`EXPLAIN` 项（§10，含 1 万首歌单卡片）；新建 `personal` 合同 spec 并更新 `listening-contracts.md` 范围外条目、`catalog-rest-contracts.md` | `go test -race ./internal/personal/... ./internal/catalog/... ./internal/app -count=1`（集成 0 SKIP）；`go vet` 同范围；依赖规则测试；样本由真实服务生成 | `feat(personal): 收藏与歌单接口` |
| 2 前端 | Opus（与 1 并行，按 design.md §9） | `features/personal`（API、解码器、查询键、`FavoriteButton`、批量状态范围、添加到歌单与新建对话框、收藏与歌单视图）；`app/routes/track-actions.tsx` 统一曲目行动作（播放、心形、“更多”），历史页改用它；专辑头部心形与“添加到歌单…”、每行心形；`PlayerBar`/`PlayerQueue`/正在播放的中立插槽（当前曲目心形、“存为歌单”）；`/me/favorites`、`/me/playlists`、`/me/playlists/{id}` 与页签；拖动排序（`@dnd-kit`）与菜单移动；移除后的“撤销”；跨标签页的心形同步（无载荷广播 + 聚焦重读）；同步前端 spec 与 DESIGN 条目。界面文案遵守 `~/.claude/CLAUDE.md`“前端界面文案” | lint、typecheck、build（重建 `backend/cmd/roomusic/web`） | 等 3 一起提交 |
| 3 测试 | Astra | 解码器对样本往返；Vitest（心形乐观更新与回滚、收藏页保留行、播放全部计数与省略提示、对话框去重计数、撤销位置、移动、并发冲突后重读）；Playwright（五种宽度、axe、纯键盘收藏/新建/加入/排序/移除/播放，拖动用鼠标与触屏各一）；真实服务用例（收藏一首刷新仍在、取消后消失；建歌单加两首交换顺序刷新保持） | `npx vitest run`（相关）、`flock … npx playwright test <相关 spec>`、`flock … scripts/playback-e2e.sh` | 与 2 合为 `feat(frontend): 收藏与歌单` |
| 4 收尾 | Astra + 主会话 | 导出树验证一次；1440 与 375 截图（收藏两栏、歌单列表与详情、拖动中、添加到歌单对话框、播放器栏心形；合成数据，临时 spec 不入库），主会话复核 | 同上 | 结果写入两个 PRD；合入 `main` 后通知 F、S 线变基 |

## 注意

- 历史页的行动作改由 `track-actions.tsx` 组合，改动时搜索 `frontend/e2e/real/` 与 `scripts/playback-e2e.sh` 里依赖旧菜单结构的断言。
- 共享热点（`app.go`、`dependency_rules_test.go`、`router.tsx`、`me-layout.tsx`、`album-detail-page.tsx`、`history-playback.tsx`、播放器插槽、`shared/api/request.ts`）由本线先改，F、S 线变基时接入，不各自另造。
- 回滚点：步骤 1 与步骤 2+3 各是独立提交；前端提交连同重建的内嵌产物与锁文件。

## 步骤 1 提交前追加验收（2026-11-04）

主会话已审阅接口、迁移与测试，批准以下后端内部性能优化；不改变外部合同、不新增 Go 依赖、不动前端及内嵌产物，不提交。

1. 将三份阶段性后端报告移到本任务 `research/`，修正引用；最终结论与关键数字写入 design §10“实测结果”。
2. 独立合成 100,000 首 / 10,000 个发行 / 8,000 个组（每发行 10 首、5% missing）的真实形状；元数据辅助表空且未 ANALYZE、全部 ANALYZE 两阶段均测。20 张各 10,000 首卡片、20,000 首收藏及 10,000 首歌单，规模不得缩减。
3. 真实 app 经本机 TCP，包含认证、服务端 SQL/投影、JSON 编码及完整响应发送；单独记录客户端收完 body 与 Go 解码时间。非 race 每场景至少 20 个有效样本，以 nearest-rank p95 验收：列表首屏 ≤1,000ms、两类队列 ≤2,000ms、收藏页与歌单详情首屏 ≤300ms。
4. 未达标时在 catalog 公开读取与 personal 有界读取内优化，保留事务快照、完整统计、权限和封面顺序；仅经实测证明 JIT 编译影响延迟后，允许在延迟敏感只读事务开始后执行 `SET LOCAL jit = off`，并按数据库规范集成验证事务内设置与事务外默认值不变；仍禁止关闭其他 planner 开关、改优化器参数或在业务事务执行 ANALYZE。极端十万轨单发行/80% missing 保留既有证据，不作为本次优化目标。
5. 重跑步骤 1 的指定 race/vet/依赖规则和两组样本回放，集成 0 SKIP；保留更新前后样本 SHA-256 对比。隔离项目仍为 `roomusic-test-m5p`，唯一启动器负责随机库及空闲端口，结束后 `down -v`。


追加验收已完成：真实形状双统计状态共320个有效HTTP样本，全部p95门槛通过；最终race 315个顶层/930项（集成223个顶层/591项），0 FAIL、0 SKIP，vet/依赖规则/样本回放通过。219份golden（含personal70份）SHA不变。三份报告已迁入research；本轮文件与隔离库清理证明见[后端交付](research/m5-personal-backend-handoff.md)及[验收清单](research/backend-followup-verification.json)。仅步骤1追加验收完成，不代表步骤2–4或整个任务完成，未提交。

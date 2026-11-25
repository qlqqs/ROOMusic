# Subsonic 与网页端评分：实施计划

- 分支：`task/subsonic-rating`
- 基线：合入 foundation 后的 `main`
- 工作目录：Orca worktree

## 步骤

1. 【Codex 实现，astra】：涉及整理事务和锁序，按复杂任务处理。
   - 迁移（含 down）。
   - `personal`：`SetRating`、`LookupRatings`、`HighestRatedAlbums`，以及 `/me/ratings/*` 的 HTTP 处理器。
   - 跟随整理：
     - 扫描转移：搬收藏和评分在同一次加锁里完成；
     - 治理合并的前态与撤销：合并记录新增 `Ratings` 字段；
     - 艺人解析刷新。
   - Subsonic：`setRating`、`highest`、`userRating`。browse 未合入时，只做 `setRating` 和 `HighestRatedAlbums` 查询；接到转换函数上的工作，由后合入的一方负责。
   - 在 `app.go` 注册路由，更新 `routes_test.go`。
   - 实现代理不写新测试。
2. 【Codex 测试，sol】集成测试：
   - AC1–AC3：网页端一侧用 `/api/v1/me/ratings/*` 核对；
   - AC4：合并、撤销，以及合并后改评分的情况；
   - AC5：扫描转移和失败回滚；
   - AC6：艺人合并与拆分；
   - AC8：收藏的 `revision` 不受影响。
3. 【主代理前端】
   - 先出星级控件的可交互设计稿：头部、菜单浮层、播放条三处；
   - 实现组件、接口解码与跨标签页同步；
   - 单测覆盖 AC7。
4. 主代理验收：
   - 审阅改动有无越界：`governance` 只允许改合并的写入和撤销两处；
   - 核对子代理的验证结果，不复跑；
   - 更新 `personal-contracts.md`、`subsonic-contracts.md`；
   - 提交并合入。

## 验证命令

- `go test ./internal/personal/... -run 'Rating'`
- `go test ./internal/catalog/governance/ -run 'ReleaseMerge'`：只在合并相关文件有改动时运行。
- `go test ./internal/library/ -run 'FavoriteMove|Rating'`
- `go test ./internal/subsonic/... -run 'Rating|Highest'`
- `go test ./internal/app/ -run 'Route'`
- 集成测试用 `ROOMUSIC_TEST_RUN` 收窄到评分用例。
- 前端：`npm run typecheck`，以及星级组件和受影响页面的测试文件。

## 风险与回滚点

- **扫描转移的锁集合合并**：收藏和评分分两次加锁会造成死锁风险。实现时必须先合并用户集合再一次加锁，测试里要包含两张表都有数据的用户。
- **合并记录新增字段**：旧记录没有 `Ratings` 字段，按空处理。撤销旧合并的行为不变，测试要覆盖一条旧格式记录。
- 迁移有 down；整理相关的改动单独提交，便于回退。

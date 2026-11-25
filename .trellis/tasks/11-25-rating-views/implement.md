# 我的评分页与管家评分工具：实施计划

- 分支：`task/rating-views`
- 基线：合入 `11-25-subsonic-rating` 后的 `main`
- 工作目录：Orca worktree

## 步骤

1. 【Codex 实现，sol】边界清楚，都在 `personal` 内，按一般任务处理。
   - 补索引的迁移（含 down）；
   - `RatedTracks/Albums/Artists`、`RatedTrackQueue`，以及四个 GET 处理器；
   - 两个管家工具；
   - 在 `app.go` 注册路由，更新 `routes_test.go`；
   - 实现代理不写新测试。
2. 【Codex 测试，sol，与实现不是同一个代理】
   - 集成测试覆盖 AC1–AC4、AC6；
   - 列表的两种排序各做一次 EXPLAIN，确认用到了索引；
   - 管家工具的测试放在 `personal/stewardtools/tools_integration_test.go`。
3. 【主代理前端】
   - 先出可交互设计稿：桌面和 320px，三栏，含空状态；
   - 实现路由与地址校验、`RatingsView`、星级筛选、排序切换、播放全部、导航项；
   - 单测覆盖 AC5。
4. 主代理验收：
   - 检查改动有无越界：只允许改 `personal`、`app` 的路由和管家注册，以及前端；
   - 核对子代理的验证结果，不复跑；
   - 更新三份规范；
   - 提交并合入。

## 验证命令

- `go test ./internal/personal/... -run 'Rated|RatingList|RatingQueue'`
- `go test ./internal/personal/stewardtools/ -run 'Rating'`
- `go test ./internal/app/ -run 'Route|Dependency'`
- 集成测试用 `ROOMUSIC_TEST_RUN` 收窄到本任务的用例。
- 前端：`npm run typecheck`，以及评分页和筛选控件的测试文件。

## 风险与回滚点

- **艺人合并后的分页**：要先合并等价锚点，再筛选和分页，否则会出现重复行或者总数不对。测试里要有合并过的艺人。
- **列表里改评分**：改完后条目会移位或被筛掉。前端要让本页查询失效后重新读取，不能只在本地改值。
- 本任务不改评分表的结构，迁移只加索引，可以单独回退。

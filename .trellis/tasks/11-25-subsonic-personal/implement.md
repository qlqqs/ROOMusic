# Subsonic 收藏、歌单与歌词：实施计划

分支 `task/subsonic-personal`，基线为合入 foundation 后的 `main`，用 Orca 建 worktree。这个子任务只做映射，不新增数据模型，所以没有单独的 design.md，设计要点写在下面。

## 设计要点

- **收藏**
  - 批量写入放在一个事务里：先用 `LookupFavorites` 的同类查询检查 ID 是否全部存在，再逐个 `SetFavorite`。
  - 如果 `personal` 没有接受外部事务的入口，就补一个批量设置函数。
- **歌单写入**
  - 一律先读出当前歌单（含版本号与曲目 ID 列表），在内存里算出新的曲目列表，再以“整体替换”命令写入。
  - 幂等键用请求参数的哈希加用户 ID。
  - 遇到 `revision_conflict` 时重读一次再写。
- **歌词**
  - 用 `catalog.LoadTrackLyrics`。`lang` 有就给，没有就是 `und`。
  - `getLyrics` 用 `catalog.Search` 的曲目搜索取第一首，按标题与艺人做规范化后完全匹配，匹配不上就返回空。
- **`starred` 字段**
  - 浏览类接口批量载入歌曲时，用 `LookupFavorites` 一次查回本人的收藏状态。
  - 这一处在 browse 子任务的转换函数上扩展；两个子任务谁后合入，谁负责接上。

## 步骤

1. 【Codex 实现，sol】：收藏、歌单、歌词各接口，以及 `starred` 字段。
2. 【Codex 测试，sol】：集成测试覆盖 AC1–AC5，网页端一侧通过现有的 `/api/v1/me/*` 接口核对。
3. 主代理验收：审阅改动，更新 `subsonic-contracts.md`，提交并合入。

## 验证命令

- `go test ./internal/subsonic/... ./internal/personal/... -run 'Subsonic|Star|Playlist|Lyrics'`
- 集成测试用 `ROOMUSIC_TEST_RUN` 收窄。

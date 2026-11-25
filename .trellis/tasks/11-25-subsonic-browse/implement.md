# Subsonic 浏览、搜索与封面：实施计划

- **分支**：`task/subsonic-browse`，基线是合入 foundation 后的 `main`。
- **工作目录**：Orca worktree。

## 步骤

1. 【Codex 实现，astra】
   - `catalog` 新增的排序、随机曲目、流派统计；
   - `listening` 的按专辑聚合；
   - 歌曲、专辑、艺人的转换函数；
   - 各浏览接口；
   - `getCoverArt` 的缩放与缓存。
2. 【Codex 测试，sol】
   - 合成曲库的集成测试，覆盖 AC1–AC4；
   - 用现有的规模基准方法做 AC5 的检查。
3. 主代理验收：
   - 审阅改动，核对验证结果；
   - 更新 `subsonic-contracts.md` 的浏览部分；
   - 提交并合入。

## 验证命令

- `go test ./internal/subsonic/... ./internal/catalog/... -run 'Subsonic|AlbumList|RandomTracks|Genre'`
- 集成测试用 `ROOMUSIC_TEST_RUN` 收窄。
- 规模基准按 `catalog_scale_benchmark_test.go` 的方式单独跑一次。

## 风险

- 随机排序在大库上可能很慢。用 `TABLESAMPLE` 或先随机抽 ID 再载入，以基准结果为准。

# 我的评分页与管家评分工具：设计

评分表、写入、锁与整理规则见 `.trellis/spec/backend/personal-contracts.md` §9（评分子任务已合入）。迁移编号用 0083。本文只写列表、页面和管家工具。

## 服务（`personal`）

```go
type RatedListParams struct {
    MinRating int    // 1–5，默认 1
    Sort      string // "rating"（默认）或 "recent"
    Page      Page
}
RatedTracks(ctx, userID string, p RatedListParams) (RatedTracksPage, error)
RatedAlbums(ctx, userID string, p RatedListParams) (RatedAlbumsPage, error)
RatedArtists(ctx, userID string, p RatedListParams) (RatedArtistsPage, error)
RatedTrackQueue(ctx, userID string, minRating int, sort string) (QueueTracks, error)
```

- 读取方式照搬 `FavoriteAlbums`：
  - 只读 REPEATABLE READ 快照；
  - 先在评分表里取总数和本页引用；
  - 再用曲库公开投影补摘要：曲目用 `orderedTrackSummaries`，专辑用 `LoadReleaseGroupSummaries` 加 `personalAlbum`，艺人沿用收藏艺人的读取。
- 排序：
  - `rating`：`rating DESC, rated_at DESC, 目标 ID ASC`；
  - `recent`：`rated_at DESC, 目标 ID ASC`。
- 艺人：先按 `resolved_artist_id` 取 `rated_at` 最新的一行，再筛选、排序、分页；`total` 是合并后的数量。
- 列表不带 `revision`，不返回 409。页码超出范围时返回空页并保留 `total`，同收藏。
- 索引：
  - 核心任务已建 `rating_albums(user_id, rating DESC, rated_at DESC, group_id)`；
  - 本任务给 `rating_tracks` 和 `rating_artists` 补同样的索引；
  - 再给三张表各加 `(user_id, rated_at DESC, 目标 ID)`，供 `recent` 使用；
  - 用 EXPLAIN 核对，结果写进验收记录。
- 队列：只取 present 的曲目，顺序同列表，最多 10,000 首，超出时 `truncated=true`。返回格式同 `FavoriteQueue`，但不带 `revision`。

## 网页端接口

前缀 `/api/v1`，守卫同收藏。

| 方法和路径 | 查询参数 | 响应 |
|---|---|---|
| GET `/me/ratings/tracks` | `min_rating`、`sort`、`page`、`page_size`（默认 50，最大 100） | `{items:[{track:TrackSummary,rating,rated_at}],pagination}` |
| GET `/me/ratings/albums` | 同上 | `{items:[{album:PersonalAlbum,rating,rated_at}],pagination}` |
| GET `/me/ratings/artists` | 同上 | `{items:[{artist,available,rating,rated_at}],pagination}` |
| GET `/me/ratings/tracks/queue` | `min_rating`、`sort` | 同收藏队列，不带 `revision` |

- 参数校验：
  - `min_rating` 不是 1–5 的整数，返回 `400 invalid_rating`；
  - `sort` 不在白名单，返回 `400 invalid_sort`；
  - 重复的参数、未知参数，返回 `400 invalid_query`；
  - 分页错误沿用收藏的错误码。

## 前端

- **路由**：`/me/ratings?tab=tracks|albums|artists&min=1..5&sort=rating|recent&page=N`。默认值不写进地址，校验方式同 `album-search.ts`。
- **组件**：
  - `features/personal` 新增 `RatingsView`，结构照 `FavoritesView`；
  - 曲目栏复用 `LikedTracksView` 的行和 `TrackTable`；
  - “播放全部”经插槽传入，`PersonalPlayAll` 增加来源 `{kind:"ratings", minRating, sort}`。
- **控件**：
  - 筛选：五颗星的 `radiogroup`，选中第 N 颗表示“N 星及以上”。前面的星实心、后面的星空心，形状与 `RatingStars` 相同，但是一个独立的筛选控件，不与评分控件共用状态；
  - 排序：一个图标按钮，在“评分”和“最近评分”之间切换；读屏名写成“排序：按评分”。
  - 两者都放在栏标题的同一行，窄屏时换到标题下方。
- **每项**：显示本人星级（`RatingStars` 的紧凑尺寸），可以直接改。改完后，使本页查询失效并按新值重新读取。
- **同步**：沿用 `PersonalScope` 的 `"ratings"` 通知，评分页的查询键也放在这个范围下。
- **导航**：在“我的音乐”组中“我的收藏”之后加“我的评分”，图标用星。
- 按习惯先出可交互设计稿：桌面和 320px 两种宽度，三栏，含空状态和筛选后为空的状态。

## 管家工具

在 `personal/stewardtools/tools.go` 里追加两个定义，模式同 `list_favorites` 和 `set_favorite`：

| 工具 | 范围 | 输入 | 调用 |
|---|---|---|---|
| `list_ratings` | read | `kind`*、`min_rating`（1–5）、`sort`（rating/recent）、`page`、`page_size`（1–50，默认 20） | `RatedTracks/Albums/Artists` → `{items,pagination}` |
| `set_rating` | personal | `kind`*、`target_id`*、`rating`*（0–5 的整数，0 表示清除） | `SetRating` → `{target_kind,target_id,rating,rated_at}` |

- 两个工具都是 `MinRole=member`、`Untrusted=true`、`Revertible=false`。
- 目标不存在时返回 `tool.CodeTargetNotFound`。
- 更新 `steward-runtime-contracts.md` 第 3 节的工具清单。

## 规范

- `personal-contracts.md` 的“评分”一节补上列表、队列接口和索引；
- `steward-runtime-contracts.md` 补上两个工具；
- `frontend/personal-guidelines.md` 补上“我的评分”页和星级筛选控件。

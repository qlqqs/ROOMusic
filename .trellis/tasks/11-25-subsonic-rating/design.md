# Subsonic 与网页端评分：设计

共用协议约定见父任务 `design.md`，收藏的合同见 `.trellis/spec/backend/personal-contracts.md`。本文只写评分特有的部分。

## 数据

新迁移（编号按合入顺序定）：

```sql
rating_tracks(user_id, track_id, rating smallint CHECK (rating BETWEEN 1 AND 5), rated_at timestamptz,
              PRIMARY KEY(user_id, track_id))
rating_albums(user_id, group_id, rating, rated_at, saved_title, saved_artist, PRIMARY KEY(user_id, group_id))
rating_artists(user_id, artist_id, resolved_artist_id, rating, rated_at, PRIMARY KEY(user_id, artist_id))
```

- 外键：
  - `user_id` 引用用户，`ON DELETE CASCADE`；
  - 目标列引用 `tracks`、`release_groups`、`artists`，`ON DELETE RESTRICT`，与收藏一致；
  - 目标列各建单列索引，供外键检查和整理时按目标查找。
- `highest` 查询需要索引 `rating_albums(user_id, rating DESC, rated_at DESC, group_id)`。
- 艺人表的结构同 `artist_favorites`：保存原锚点和解析 ID，不在 `(user, resolved)` 上加唯一约束。
- 专辑表保存评分时的 `saved_title`、`saved_artist`，专辑组变得不可用时用来显示，做法同 `favorite_albums`（`personal/projection.go:49`）；搬迁和撤销时随行一起复制。

## 服务（`personal`）

```go
SetRating(ctx, userID, kind, targetID string, rating int) (RatingResult, error) // rating=0 表示清除
LookupRatings(ctx, userID string, trackIDs, groupIDs, artistIDs []string) (RatingLookup, error)
HighestRatedAlbums(ctx, userID string, offset, size int) ([]RatedAlbum, error)
```

- 写事务：
  - 艺人：先持 CATP，并检查艺人目录已就绪（同艺人收藏）；
  - 再确保并锁本人的 `personal_favorite_state`；
  - 写评分时**不增加** `revision`。
- 设置时校验目标存在，规则同收藏：缺失的曲目可以评分，真正不存在的目标返回 not found。清除不查目录。
- 艺人：
  - 读取时按 `resolved_artist_id` 分组，取 `rated_at` 最新的一行；
  - 设置时，改写本人所有解析到同一艺人的锚点，请求的锚点不存在就插入；
  - 清除时删除本人所有等价锚点。

## 跟随整理

- **扫描转移**：`MoveAlbumFavorites` 所在的事务里同时调用 `MoveAlbumRatings(ctx, tx, moves)`。
  - 加锁的用户集合要包括来源组上有评分的用户，并按 UUID 升序合并到同一次加锁里；不能分两次加锁，否则会和收藏的锁序冲突。
  - 插入目标时 `ON CONFLICT DO NOTHING`，然后删除来源行。
  - library 的注入点由一个改为同时搬收藏和评分；同一事务，失败整体回滚。
- **治理合并**：
  - `release_merge_write.go` 在搬收藏的同一锁内调用 `MoveMergedAlbumRatings`，前态 `[]MergedAlbumRating{user_id, rating, rated_at, target_existed}` 写进合并记录的新字段 `Ratings`（`omitempty`，旧记录没有这个字段时按空处理）。
  - 撤销时 `RestoreMergedAlbumRatings`：恢复来源组的行；对 `target_existed=false` 的目标行，只有 `(rating, rated_at)` 仍等于复制时的值才删除。
  - 合并预览和例外比对（`compareChanges`）不加评分字段，避免改变现有令牌的比对结果。
- **艺人解析**：在 `RefreshArtistResolution` 里同时刷新 `rating_artists.resolved_artist_id`，沿用同一批次、同一锁序。

## 网页端接口

路径前缀 `/api/v1`，守卫与严格 JSON 解码同收藏（`personal-contracts.md` §3.1）。

| 方法和路径 | 输入 | 响应 |
|---|---|---|
| PUT `/me/ratings/tracks/{id}`、`/albums/{group_id}`、`/artists/{id}` | `{rating:1..5}` | `{target_kind,target_id,rating,rated_at}`；艺人另带 `resolved_artist_id` |
| DELETE 同上 | 无 | `{target_kind,target_id,rating:null,rated_at:null}`，重复删除也成功 |
| POST `/me/ratings/lookup` | `{track_ids?,group_ids?,artist_ids?}`，数量上限同收藏 lookup | `{tracks:[{track_id,rating}],albums:[{group_id,rating}],artists:[{artist_id,resolved_artist_id,rating}]}`，没有评分时 `rating=null` |

PUT 是幂等的，不需要幂等键。

## Subsonic

- `setRating`：
  - 按顺序在曲目、专辑组、艺人中查找这个 UUID，找不到返回 70；
  - `rating` 不是整数返回 10，不在 0–5 内返回 0。
- `userRating`：browse 的转换函数在批量载入时调用一次 `LookupRatings` 填上。和 personal 子任务的 `starred` 一样，browse 和本任务谁后合入，谁负责接上。
- `highest`：`HighestRatedAlbums` 取出专辑组 ID 后，用 browse 的专辑转换函数输出。browse 先合入时，`highest` 返回空列表，本任务合入后再接上。

## 前端

- **组件**：`features/personal` 新增：
  - `RatingStatusScope`：一页一次 lookup，模式同 `FavoriteStatusScope`；
  - `RatingStars`：头部的五颗星；
  - `useRatingMenuAction`：给“更多操作”和播放条用，选中后弹出小浮层显示星级。
- **交互**：
  - 星级是 `role="radiogroup"` 加五个 `radio`；
  - 方向键改变选择，悬停时预览；
  - 点当前值、或按 Delete 清除；
  - 先按新值显示，失败时恢复原值，并在旁边说一句。
- **同步**：`PersonalScope` 增加 `"ratings"`，写入后通过 BroadcastChannel 通知其他标签页；窗口重新聚焦时一并重新读取。
- **放置**：
  - 专辑详情、艺人详情头部的心形旁边；
  - 曲目行的“更多操作”；
  - 播放条当前曲目的个人操作；
  - 曲目行本身不显示星级，保持一行三个图标。

## 规范

在 `personal-contracts.md` 新增“评分”一节：表结构、锁序、不增加 `revision`、整理时的搬迁与撤销规则。在 `subsonic-contracts.md` 记录 `setRating`、`userRating` 和 `highest`。

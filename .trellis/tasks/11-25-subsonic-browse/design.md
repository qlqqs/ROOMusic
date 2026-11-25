# Subsonic 浏览、搜索与封面：设计

## 读取

- **处理器**：放在 `subsonic/httpapi/browse*.go`，只调用 `catalog`、`personal`、`listening` 的公开函数。
- **新增的 catalog 读取函数**：
  - 加在 `catalog` 包，沿用 `ListReleaseGroups` 的参数风格，新增 `Sort` 取值：
    - `random`：带种子，同一请求内稳定；
    - `year`：带升降方向与年份区间；
    - `artist`：按专辑艺人的排序名；
    - `genre`：按流派过滤。
  - 全库随机曲目和流派统计另写函数，都走 `track_effective`，以及 `release_genres` 的有效值。
- **收藏、最近、最常播放**：
  - 先从 `personal.FavoriteAlbums` 或 `listening` 取出有序的专辑组 ID，再批量载入专辑组摘要，避免 N+1。
  - `listening` 按专辑聚合播放次数的查询，没有就补一个。
- **多碟**：`getAlbum` 展开默认版本的全部碟，曲目按（碟、序号）排序。`discTitles` 只在碟有标题时给出。
- **批量转换**：`TrackSummary` 到 Subsonic 歌曲的转换集中在一个函数里，`browse`、`personal`、`stream` 三个子任务共用。

## 封面

- 用 `artwork.Store` 读原图；缩放在 Go 内用 `golang.org/x/image/draw` 做（CatmullRom），输出 JPEG（质量 85）。原图是 PNG 且有透明时输出 PNG。
- **缓存**：
  - 位置：`<数据目录>/cache/subsonic-art/<资源ID>-<档位>.<ext>`，写入时先写临时文件再改名。
  - 原图的指纹变了，缓存键随之变化（文件名带指纹前 8 位）。
  - 同一缓存键并发请求只缩放一次（singleflight）。
- **艺人图**：没有时回退到该艺人一张专辑的封面。都没有时返回 70，客户端会显示自己的占位图。
- **响应头**：`Cache-Control: private, max-age=31536000, immutable`。ETag 用缓存键，因为 ID 加档位已经决定内容。

## 依赖

- `golang.org/x/image` 若尚未引入，属于新增 Go 依赖。它是纯 Go 库，不涉及系统依赖，在交付说明中列出。

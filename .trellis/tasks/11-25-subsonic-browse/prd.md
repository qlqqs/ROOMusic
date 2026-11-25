# Subsonic 浏览、搜索与封面

父任务：`11-25-opensubsonic-api`。共用约定见父任务的 `design.md`。依赖 `11-25-subsonic-foundation`。

## 目标

客户端能按艺人、专辑浏览整个曲库，能用各种专辑列表（最新、随机、按年份、按流派等）和搜索，列表封面能快速加载。

## 需求

- R1 接口：
  - `getArtists`：专辑艺人按首字母分组，`ignoredArticles` 为空。
  - `getArtist`：艺人及其专辑。
  - `getAlbum`：专辑及默认版本的曲目。
  - `getSong`：单首曲目。
  - `getAlbumList2`：
    - 支持的 `type`：`newest`、`alphabeticalByName`、`alphabeticalByArtist`、`random`、`starred`、`recent`、`frequent`、`byYear`（`fromYear`/`toYear`，可倒序）、`byGenre`。
    - `highest` 由 `11-25-subsonic-rating` 实现；本任务先于它合入时返回空列表。
  - `search3`：三类各自的 `count`/`offset`；空查询返回全库分页，部分客户端用它同步全库。
  - `getRandomSongs`：`size`、`genre`、`fromYear`、`toYear`。
  - `getGenres`：流派名、专辑数、歌曲数。
  - `getArtistInfo2`、`getAlbumInfo2`：有简介和外链时返回，没有就返回空对象。
- R2 `getCoverArt`：
  - 接受 `al-`/`ar-`/`pl-` 前缀与 `size` 参数；
  - 缩放后的结果按（资源、尺寸档）缓存在数据目录；
  - 尺寸向上取到固定档位（64/128/256/512/1024），原图比档位小时直接给原图；
  - 带 ETag，可长期缓存。
- R3 `catalog` 补读取函数：
  - 专辑组列表的随机、按年份、按流派、按艺人名排序；
  - 全库随机曲目；
  - 流派统计；
  - 收藏、最近播放、最常播放三种列表，从 `personal` 与 `listening` 读取。

## 验收标准

- AC1 合成曲库（含多碟、CUE 分轨、多艺人、有/无封面、有/无流派）上：
  - 每个接口的 XML 与 JSON 结构符合协议示例；
  - 歌曲的 `id`、`parent`、`albumId`、`artistId`、`coverArt` 相互引用一致。
- AC2 `getAlbumList2` 每个 type 的结果、排序与分页正确；`size>500` 截到 500。
- AC3 `search3` 的分页正确；艺人索引未就绪时艺人部分返回空，不报错。
- AC4 `getCoverArt`：
  - `size=128` 返回不大于 128 的图；
  - 第二次请求命中缓存，不再缩放；
  - 带 `If-None-Match` 时返回 304；
  - 不存在的 ID 返回 70。
- AC5 一万张专辑规模下，`getAlbumList2`、`search3` 单次在 300ms 内（沿用现有的规模基准方法）。

## 不做

- `getIndexes`、`getMusicDirectory`、`getTopSongs`、`getSimilarSongs`。

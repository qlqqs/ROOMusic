# Subsonic 收藏、歌单与歌词

父任务：`11-25-opensubsonic-api`。共用约定见父任务 `design.md`。依赖 `11-25-subsonic-foundation`。

## 目标

客户端里的收藏、歌单与网页端互通；客户端能显示同步歌词。

## 需求

- R1 `star`、`unstar`：
  - 参数 `id`（曲目）、`albumId`（专辑组）、`artistId`（艺人）都可以重复给多个；
  - 写入 `personal` 的收藏；
  - 同一次请求里有任何一个 ID 不存在，整体返回 70，不做部分写入。
- R2 `getStarred2`：返回全部收藏的艺人、专辑和曲目，带收藏时间 `starred`。
- R3 歌单读取：
  - `getPlaylists` 只列本人的歌单；`username` 参数是别人时返回 50。
  - `getPlaylist` 返回曲目与封面。
- R4 歌单写入：
  - 接口：`createPlaylist`（新建，或给了 `playlistId` 时整体替换曲目）、`updatePlaylist`（改名、追加 `songIdToAdd`、按位置删除 `songIndexToRemove`）、`deletePlaylist`。
  - 都映射到 `personal.Execute` 的命令，版本号由服务端读取当前值后带上。
  - 并发冲突时重试一次，仍冲突返回 0。
- R5 歌词：
  - `getLyricsBySongId`（OpenSubsonic `songLyrics`）返回同步或纯文本歌词，含 `lang`、`synced`、`line[{start,value}]`；
  - 旧接口 `getLyrics`（按 `artist`、`title`）匹配一首后返回纯文本。
- R6 字段：歌曲对象中的 `starred` 字段反映本人的收藏状态，三个浏览子任务共用。

## 验收标准

- AC1 在客户端收藏的曲目、专辑和艺人，网页端随即显示为已收藏；反过来也一样。`getStarred2` 的内容与网页端“收藏”一致。
- AC2 歌单：在客户端新建、改名、追加、删除第 2 首、整体替换、删除之后，网页端看到的结果一致，曲目顺序正确。
- AC3 别人的歌单读不到也改不了，返回 50 或 70。
- AC4 有同步歌词的曲目，`getLyricsBySongId` 返回 `synced=true` 且时间戳正确；只有纯文本时 `synced=false`；没有歌词时返回空的 `lyricsList`。
- AC5 `star` 的 ID 列表里有一个不存在时，什么都不写，返回 70。

## 不做

- 评分：由 `11-25-subsonic-rating` 负责。

- 公开歌单与共享（`public` 参数接受但忽略，始终私有）、书签、播放队列同步。

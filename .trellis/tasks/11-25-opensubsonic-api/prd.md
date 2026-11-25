# OpenSubsonic 兼容接口

## 目标

提供 OpenSubsonic 兼容的 `/rest` 接口，让 Symfonium、Feishin、Amperfy 等现成的 Subsonic 客户端连上 ROOMusic：浏览、搜索、在客户端本机播放（复用服务端处理链）、收藏、评分、歌单与歌词，并能用点唱机模式遥控家里的播放区。手机和桌面不必再只靠网页端。

## 子任务

| 子任务 | 内容 | 依赖 |
|---|---|---|
| `11-25-subsonic-foundation` | 客户端凭据、`/rest` 路由、XML/JSON 响应外壳、`ping`/`getLicense`/`getOpenSubsonicExtensions`/`getMusicFolders`/`getUser`、实例开关、日志脱敏、账号设置与管理设置界面 | 无 |
| `11-25-subsonic-browse` | `getArtists`/`getArtist`/`getAlbum`/`getSong`/`getAlbumList2`/`search3`/`getRandomSongs`/`getGenres`、带尺寸缩放的 `getCoverArt` | foundation |
| `11-25-subsonic-stream` | 客户端登记为播放设备；逐曲目 `stream` 复用服务端处理链（解码、均衡、房间校正、编码），无处理时直发原文件；`download`；`scrobble` | foundation |
| `11-25-subsonic-personal` | `star`/`unstar`/`getStarred2`、歌单读写、`getLyricsBySongId` | foundation |
| `11-25-subsonic-jukebox` | `jukeboxControl` 映射到播放区：客户端当遥控器 | foundation |
| `11-25-subsonic-rating` | 曲目、专辑、艺人的 1–5 星个人评分：网页端可看可改，`setRating`、`userRating`、`highest`，整理合并时随收藏搬迁 | foundation |
| `11-25-rating-views` | “我的评分”页（按星级筛选、按评分或时间排序、曲目播放全部）与管家的 `list_ratings`、`set_rating` 工具 | subsonic-rating |

foundation 合入后，browse、personal、stream、jukebox、rating 五个可并行；rating-views 等 rating 合入后开工。

## 已确认的事实（仓库调查）

- `/rest` 下目前没有任何路由，会落到前端处理器；路由在 `backend/internal/app/app.go` 注册，`routes_test.go` 固定每条路由的模板。
- ID 全是 UUID；专辑是 `release_groups`（专辑组）→ `releases`（版本）→ 碟 → 曲目，读取走 `track_effective`（含 ADR 0002 修正）。
- 登录密码是 bcrypt（`identity/guard.go`），Subsonic 的 `t=md5(密码+salt)` 无法对它校验。
- 已有个人访问令牌（`backend/internal/steward/token/`，表 `0052_steward_access_tokens.sql`），MCP 按 Bearer 校验。
- 所有播放现走服务端分区渲染；旧的 `/api/v1/tracks/{id}/audio` 已废弃，只支持 6 种直放格式。ffmpeg 在 `playback/render/ffmpeg`。
- 封面 `GET /api/v1/artworks/{id}` 只给原图，无缩放。
- 收藏（曲目/专辑组/艺人）、歌单（带版本号的幂等写入）、听歌记录（门槛规则、幂等键）、同步歌词都已有；评分没有。
- 专辑列表只有“标题”“添加时间”两种排序；全库随机曲目没有现成查询。
- 成员可看全部曲库，没有按文件夹的权限。

## 已定的决定

- 后端实现与测试由 Codex 子代理执行（全局分工）。
- **客户端登录用 ROOMusic 的用户名和登录密码**（用户 2026-11-25 选定，未采用独立客户端密码）：
  - `p=` 明文或 `enc:` 方式直接按 bcrypt 校验；
  - `t=md5(密码+salt)` 方式需要服务端能还原密码：实例开启 Subsonic 后，在网页登录成功、修改或重置密码时，用实例密钥加密保存一份登录密码；
  - 实例默认开启（用户 2026-11-25 指定）；管理员关闭时删掉全部加密副本，之后不再保存；
  - 已有用户在开启后要在网页登录一次（或在账号页确认一次密码），令牌方式才可用；
  - 第一版不做 `apiKey` 扩展。

## 已定方案

- 第一版不做：按文件夹浏览（`getIndexes`/`getMusicDirectory`）、播客、分享、网络电台、播放队列同步、用户管理接口。
- 分享不做的原因（用户 2026-11-25 同意）：公开链接需要开放免登录的播放入口，安全模型要重新设计；不对公网开放的部署里链接也打不开。以后作为 ROOMusic 自己的“公开分享链接”功能单独规划，Subsonic 接口只是顺带对接。
- 评分加入第一版（用户 2026-11-25 同意），做成 `personal` 领域的功能，网页端与客户端互通，见 `11-25-subsonic-rating`。
- 网页端按评分筛选排序和管家的评分工具也加入第一版（用户 2026-11-25 要求），见 `11-25-rating-views`。
- 专辑组对应 Subsonic 的 album，曲目取默认版本；不暴露版本选择。
- 音乐文件夹只给一个虚拟文件夹。
- 第三方客户端播放复用服务端处理链（用户 2026-11-25 要求复用原有功能，不只直推）：每个客户端登记为一台播放设备，可单独设置均衡与校正；默认不做处理、直发原文件。
- 加入点唱机模式（用户 2026-11-25 选定）。

## 规划决定记录

- 路线：用户确认。2026-11-25 用户看过最终规划摘要（含评分与“我的评分”页两项增补）后回复“开始”，批准按此实施，从 `11-25-subsonic-foundation` 开工。

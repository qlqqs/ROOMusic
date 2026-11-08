# 八号机生产库只读统计（2026-11-08）

主会话在八号机 `roomusic` 的 PostgreSQL 上用只读事务执行（`default_transaction_read_only=on`），只记聚合结果。音乐文件与本机 `music/` 完全相同（见父任务 PRD 背景）。

| 项 | 结果 | 用于 |
| --- | --- | --- |
| 艺人 / 名字键 / 有效署名 | 67 / 68 / 646 | ⑥ ⑦ 前后对比基线 |
| 人工意图：`artist_name_bindings`、`artist_assignment_overrides`、`artist_overrides` | 0 / 0 / 0 | ⑥ R6、⑦：没有会“失联”的人工意图 |
| 收藏：`favorite_albums`、`artist_favorites`、`favorite_tracks` | 0 / 0 / 1 | ② R5：现有数据上收藏转移不会触发 |
| `release_corrections` | 0 | ② ③：落选 Release 上没有修正 |
| 含逗号的署名原文 | `RADWIMPS, 陣内一真` 一类日文/中文名单 6 种；没有拉丁“姓, 名” | ⑥ R2 |
| 合辑占位（发行或曲目艺人） | 0 | ⑥ R3 |
| `track_credits` 角色分布 | 只有 composer 190 | ⑦ |
| 带括号的演奏者 | 0 | ⑦ R4 |
| 带歌词的 present 曲目 | 134；作词行开头 38、含编曲行 38、含制作人/监制行 23 | ⑦ R2b |
| 发行流派 | 国语流行乐、Pop、台语流行乐、Mandopop、世界音乐、民谣、Anime；没有古典 | ⑧：真实库上不应出现任何作品分组 |

结论：⑥ ⑦ 的“人工意图失联清单”、② 的收藏转移在现有生产数据上都为空，部署前后不需要再到生产库对比；各子项的真实库 smoke 在本机隔离实例上对 `music/` 跑即可。

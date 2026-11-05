# ⑤ API 样本名称与验收映射

以下路径均相对 `backend/internal/app/testdata/api-samples`；没有将全就绪状态冒充搜索单独就绪状态。

| 设计场景 | 实际样本 | 业务断言与来源 |
| --- | --- | --- |
| `catalog/status-legacy` | `search/catalog-status-legacy.json` | 现有真实 handler 样本：legacy + preparing；保留旧命名，不删文件 |
| `catalog/status-search-ready` | `catalog/status-search-ready.json`（本批新增） | 真实 maintenance.Run 已发布搜索，在 RefreshArtistResolutions 回调暂停艺人发布，另一连接经 HTTP GET status 读取 normalized + preparing；记录后释放 barrier，未用 SQL 伪造状态 |
| 全就绪 | `catalog/status-all-ready.json` | normalized + ready；原有 `search/catalog-status-ready.json` 同为全就绪，继续保留，但不替代上一行 |
| 艺人整理中 | `catalog/status-artists-preparing.json` | 初始整理状态；不替代 normalized + preparing 中间态 |
| M3 legacy 提交即命中 | `search/artist-correction-legacy.json`、`search/artist-correction-legacy-{albums,tracks}-{new,old}.json`（本批新增） | 真实 PATCH 修正 title/album_artist，紧接搜索；新词仅命中本组、本发行的两首 present 曲目，旧词 items/total 均为0 |
| M3 normalized 提交即命中 | `search/artist-correction-normalized.json`、`search/artist-correction-normalized-{albums,tracks}-{new,old}.json`、`search/artist-correction-normalized-artists.json`（本批新增） | 同一夹具真实发布后再 PATCH；提交与读之间不重扫、不运行维护。另断言新艺人有效UUID、原名、两个发行级角色、1专辑/2曲目 |
| 超限队列 | 仓外 `artists/queue-truncated.json` | 完整10000项 `{status,body}`；路径和生成/比较命令见 `artist-large-samples.md`，不计入仓内小样本 |

96个仓内小样本及1个仓外巨型样本均已真实生成并无UPDATE逐字比较，race PASS、0 SKIP。证据为仓外artist-complete-samples-update.json、artist-complete-samples-compare.json；此前84个样本数已被本轮96个取代。

生成与比较（需主控释放数据库；不会清理其他 owner 的样本）：

```sh
source /tmp/roomusic-m5-artists-test.env
export ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR=/home/qlqq/.local/share/roomusic-evidence/m5-artists/api-samples
cd backend
ROOMUSIC_UPDATE_API_SAMPLES=1 go test -race ./internal/app -run '^TestPostgreSQLArtistAPIResponseSamples$' -count=1 -json
ROOMUSIC_UPDATE_API_SAMPLES=0 go test -race ./internal/app -run '^TestPostgreSQLArtistAPIResponseSamples$' -count=1 -json
```

新中间态路径和 `search/artist-correction-*` 由 artist-increment recorder 独占；默认 API 与旧 search recorder 排除它们，不因旧套件 UPDATE 而清理。


## 主会话裁决 I1 / I2 / I4 / I6 增量（A）

新增35个真实app handler样本，目录 `backend/internal/app/testdata/api-samples/artists/adjudication/`。前端可立即按以下清单核对替身；不改变旧96个样本及外置队列所有权。`apiSampleRecorder.ownsSample`先判断该子目录，仅`scope=artists/adjudication`拥有，默认/artist-increment/宽artists scope均不得清理或认领。以下每项均为 `{status,body}`，不是手写DTO。

- I1：`merged-sources*.json`，真实merge、链式读取契约、恢复后空列表、再合并后拆给新身份导致空键、401/403/400/404/preparing。
- I2：`albums-*.json`，默认只album_artist与any全部角色、非参与代表保留、q按专辑名、分页、空结果、参数错误与整理态。
- I4：`tracks-summaries*.json`，输入顺序、unknown省略、missing、准备态空refs、治理后refs、数量/ID/Origin/鉴权边界。
- I6：`role-album-artist.json`、`role-release-artist.json`，并集筛选与仅release_artist区别，DTO角色词表不变。

精确文件清单：

```text
albums-any-empty.json
albums-any-page.json
albums-any-preparing.json
albums-any-query.json
albums-any.json
albums-default-after-any.json
albums-default-query.json
albums-default.json
albums-empty-participation.json
albums-invalid-pagination.json
albums-invalid-participation.json
albums-long-query.json
albums-repeated-participation.json
albums-repeated-query.json
merged-sources-after-restore.json
merged-sources-empty-keys.json
merged-sources-empty.json
merged-sources-forbidden.json
merged-sources-invalid-id.json
merged-sources-not-found.json
merged-sources-preparing.json
merged-sources-unauthorized.json
merged-sources.json
role-album-artist.json
role-release-artist.json
tracks-summaries-after-merge.json
tracks-summaries-empty.json
tracks-summaries-invalid-id.json
tracks-summaries-origin-forbidden.json
tracks-summaries-origin-required.json
tracks-summaries-over-limit.json
tracks-summaries-preparing.json
tracks-summaries-unauthorized.json
tracks-summaries-unknown.json
tracks-summaries.json
```

生成/复验命令（隔离库env由主控提供）：

```sh
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLArtistAdjudicationResponseSamples$' -count=1 -v
go test -race ./internal/app -run '^Test(PostgreSQLArtistAdjudicationResponseSamples|ArtistAdjudicationSampleOwnership)$' -count=1 -v
```

两轮均PASS、0 SKIP；日志：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/adjudication-a/generate.log
/home/qlqq/.local/share/roomusic-evidence/m5-artists/adjudication-a/verify-race.log

先前口头预估38项为计数错误，代码声明`adjudicationSampleNames`及实际目录均35项；没有为过测试删减场景。

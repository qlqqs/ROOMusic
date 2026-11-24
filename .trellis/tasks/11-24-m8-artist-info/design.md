# 设计：艺人图片与简介

## 分层

```
sources/                       （不依赖 curation、catalog、数据库）
  sources.go                   新能力 artist_image、artist_bio；中立类型 ArtistLinks、ArtistImage、ArtistBio、Attribution；
                               窄接口 ArtistLinkFinder、ArtistImageFinder、ArtistBioFinder
  musicbrainz/                 ArtistLinks(mbid)：/artist/<id>?inc=url-rels，投影 wikidata、wikipedia 链接
  wikidata/                    ArtistLinks 补充：按 Q 号取 P18、sitelinks；按 P434 反查 Q 号
  commons/                     ArtistImage：由文件名取 imageinfo + extmetadata，下载标准宽度缩略图
  wikipedia/                   ArtistBio：summary 接口，zh 带 Accept-Language: zh-cn
  fanarttv/                    ArtistImage：/v3/music/<mbid>，api-key / client-key 请求头；内置项目密钥（ldflags，可空）
  theaudiodb/                  ArtistImage、ArtistBio：/api/v1/json/<key>/artist-mb.php?i=<mbid>
curation/
  artist_media.go              ApplyArtistImage、ApplyArtistBio：以 TargetArtist 为目标；三种跳过（occupied、suppressed、无许可）
  artist_media_revert.go       两种 Kind 的撤销、Describe、PreviewRevert；撤销写抑制标记
  program/artist_media.go      新的后台 subject `artist_media`：按缺口挑艺人 → 取链接 → 按优先级取图与简介 → 写入
catalog/
  artwork/artwork.go           Lookup、Discard 并入 artist_external_artworks
  artists_reads.go/types.go    读取 image、bio（解析重定向：有效艺人没有时取被并入方的，按合并时间最近的一个）
```

## 数据

迁移（编号接现有最大号）：

- `artist_external_artworks(artist_id PK, storage_key, mime, width, height, source, source_url, author, license, license_url, operation_id, created_at)`
- `artist_external_bios(artist_id PK, text, lang, source, source_url, license, license_url, operation_id, created_at)`
- `operations_type_ck` 增加 `artist_external_artwork`、`artist_external_bio`。
- `curation_work_states.subject` 的 CHECK 增加 `artist_media`。
- 对应的 down 迁移与往返测试。

抑制标记沿用 `curation_suppressions`：图片按 `(artist, artist_image, <source>:<外部条目 ID 或图片 URL 的哈希>)`，简介按 `(artist, artist_bio, <source>:<页面或条目 ID>)`。

## 后台流程（每个艺人）

1. 前提：艺人有 `external_links` 的 MusicBrainz 对应（解析重定向到有效艺人后去重），且图片或简介至少缺一项。
2. 取链接：MusicBrainz url-rels → Wikidata Q 号、维基百科链接；没有 Q 号时 Wikidata 按 P434 反查；有 Q 号时从 Wikidata 取 P18、zhwiki/enwiki 的 sitelinks。
3. 图片（缺时）：Fanart.tv → TheAudioDB → Commons，跳过被抑制的条目；下载后由 `catalog/artwork` 校验图片。
4. 简介（缺时）：维基百科 zh → TheAudioDB CN → 维基百科 en → TheAudioDB EN；去 HTML、去 `[1]` 一类脚注、截断到 4000 字。
5. 写入用同一批次；每项一条操作记录。工作状态的输入摘要包含：MBID 集合、各源的启用与密钥状态、抑制标记集合；摘要不变时不重做，
   源的 `try_later` 按现有退避处理。

各源限速：维基媒体三个源每源 1 次/秒（并发 1）；Fanart.tv 1 次/秒；TheAudioDB 2 秒 1 次（免费 30 次/分）。缓存沿用 `Requester` 的规则。

## 读取接口

```jsonc
// ArtistSummary
"image": "<resource id>" | null
// ArtistDetail
"image": { "resource": "<id>", "source": "fanarttv|theaudiodb|commons", "source_url": "...",
           "author": "...|null", "license": "...|null", "license_url": "...|null" } | null,
"bio":   { "text": "...", "lang": "zh|en", "source": "wikipedia|theaudiodb", "source_url": "...",
           "license": "CC BY-SA 4.0|null", "license_url": "...|null" } | null
```

图片资源 ID 走 `/api/v1/artworks/{id}`，缓存头不变。

## 前端

- `ArtistMonogram` 接受可选图片：有图时 `<img>`（`object-fit: cover`，圆形），加载失败回到缩写圆章。
- `ArtistHeader`：名字下方简介段，默认 4 行截断加“展开/收起”；简介下方一行小字署名：“简介：维基百科（CC BY-SA 4.0）· 图片：作者 / 许可 / Commons”，均为外链（`rel="noopener noreferrer"`）。
- 艺人列表行的小圆章传入图片。
- 外部源卡片：新能力文案（“艺人图片”“艺人简介”），“发给外部的数据”：MusicBrainz 艺人 ID、Wikidata 条目 ID、维基百科条目名、Commons 文件名。
- 治理：两种新操作类型的标题、变更摘要与撤销对话框。

## mock 服务器

加五个前缀：`/wikidata/w/api.php`、`/commons/w/api.php` 与缩略图路径、`/wikipedia/{lang}/api/rest_v1/page/summary/{title}`、`/fanarttv/v3/music/{mbid}`、`/theaudiodb/api/v1/json/{key}/artist-mb.php`；
合成数据为内置虚构艺人补图片与简介（中文与英文），其中一个艺人只有 Commons、一个只有 TheAudioDB、一个什么都没有。
后端的 `ROOMUSIC_UPSTREAM_MOCK_URL` 对五个新源同样生效。

## 兼容与回退

- 没有打开自动整理的实例不发请求、新字段为 `null`；旧前端忽略新字段。
- 回退：down 迁移删除两张表与新类型；文件由 `Discard` 按引用清理。

# 外部来源接口事实（2026-11-24 调研，含只读实测）

| 来源 | 图片 | 简介 | 密钥 | 限速 | 许可与署名 |
|---|---|---|---|---|---|
| Wikidata | P18 文件名（图在 Commons） | 只有短描述 | 不要；UA 必须含联系方式 | 合规 UA 的未登录客户端 200 次/分；Action API 未登录并发 1、<5 次/秒 | CC0 |
| Commons | 原图与标准宽度缩略图 | — | 同上 | 媒体下载并发 ≤2 | 按文件：extmetadata 的 `LicenseShortName`、`LicenseUrl`、`Artist`（HTML）、`Credit`、`Attribution`、`AttributionRequired` |
| 维基百科 summary | 来自 Commons | 导语首段 | 同上 | REST 未登录并发 3、<5 次/秒 | CC BY-SA 4.0：链接原条目、附许可链接 |
| Fanart.tv | artistthumb 1000×1000、artistbackground 1920×1080、logo、banner | — | 必须有项目密钥 `api_key`；个人密钥 `client_key` 可选、只是附加（新图更早可见） | 无官方数字，处理 429 | 不声称版权，无署名要求 |
| TheAudioDB | Thumb 700×700、Fanart、Logo 等 | `strBiography`（英）、`strBiographyCN` 等 15 种（CN 繁简混杂、带脚注残留） | 免费测试 `123`（定价页称按 MBID 查是付费功能，实测免费可用）；付费 €8/月 | 免费 30 次/分 | 可复制改用，不得删版权声明；付费用图须注明来源；免费密钥不得用于上架应用商店 |
| Last.fm | 2019 年起不提供，条款禁止 | 截断 300 字 | 要 | — | 不采用 |

接口要点：
- Wikidata：`w/api.php?action=wbgetentities&ids=Q..&props=claims|sitelinks&sitefilter=zhwiki|enwiki`；P18 是不带 `File:` 的文件名；按 MBID 反查 `action=query&list=search&srsearch=haswbstatement:P434=<MBID>`。
- Commons：`action=query&titles=File:<名>&prop=imageinfo&iiprop=url|size|mime|extmetadata&iiurlwidth=<w>&iiextmetadatafilter=...&formatversion=2`；缩略图只能用标准宽度 20/40/60/120/250/330/500/960/1280/1920/3840，非标准宽度直接请求返回 400；缩略图主机为 `upload.wikimedia.org` 与 `thumb.wikimedia.org`。
- 维基百科：`https://{lang}.wikipedia.org/api/rest_v1/page/summary/{title}`；字段 `extract`、`extract_html`、`wikibase_item`、`content_urls`、`revision`；zh 用 `Accept-Language: zh-cn` 得简体；重定向返回 302。
- Fanart.tv：`https://webservice.fanart.tv/v3/music/{艺人 MBID}`，`api_key`、`client_key` 可放请求头 `api-key`、`client-key`；不带密钥 401。
- TheAudioDB：`https://www.theaudiodb.com/api/v1/json/{key}/artist-mb.php?i={MBID}`；图片在 `r2.theaudiodb.com`，加 `/medium` 取预览。
- 维基媒体请求带 `Accept-Encoding: gzip`；429/503 没有 `Retry-After` 时至少等 5 秒。
来源网址见调研代理原始汇报（Wikimedia User-Agent Policy、Robot policy、Rate limits 2026、Page Content Service、CommonsMetadata、fanart.tv Apiary 文档与条款存档、theaudiodb free_music_api 与 terms、last.fm api/tos）。

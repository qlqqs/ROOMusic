# 仓库现状（2026-11-24 调研）

## 艺人实体
- `artists` 只有 `id, initial_name, edit_revision, created_at`（`backend/migrations/0026_artist_catalog.sql:6-9`）；没有图片与简介。
- 读接口：`GET /api/v1/artists`、`/artists/{id}` 等（`backend/internal/catalog/httpapi/handlers.go:55-62`）；类型 `ArtistSummary`、`ArtistDetail`（`backend/internal/catalog/artists_types.go:11-19, 52-60`），读详情 `LoadArtist`（`artists_reads.go:81`）。
- 样本 `backend/internal/app/testdata/api-samples/artists/`，前端 `frontend/src/test/api-contract.test.ts` 核对；前端解码忽略未知字段（`frontend/src/shared/api/decode.ts:2`）。
- 规范 `artist-contracts.md`：旧的重定向 ID 仍返回 200；目录未就绪时 503/409；写锁顺序 OPER → CATP → 发行 → 组 → 艺人。

## 艺人与外部的对应（M7 G）
- `external_links`（`0060_curation_sources.sql:8-21`）：`source='musicbrainz', object_type='artist', external_id=<艺人 MBID>`，只由程序层写入（`curation/external_link.go:82-84`、`curation/program/artist_links.go`）。可撤销，撤销写抑制标记。
- 被并入的艺人身上的对应仍在，按外部 ID 反查时要解析重定向（`curation-contracts.md` §12）。
- 标签里的艺人 MBID 落在 `track_identifiers`（`mb_artist`、`mb_album_artist`），只作 G 对位的佐证。

## 外部源框架
- 能力 6 种（`sources/sources.go:11-20`）；`Declaration{ID, Name, Capabilities, DefaultEnabled, Keys, MinInterval}`；`ArtistFinder`（:44-47）目前没有生产调用方。
- `Requester`（`sources/request.go`）：`GetJSON`、`GetBytes`，限速器按源 ID，缓存 `<数据目录>/sources/cache/`，重定向逐跳由 `AllowRedirect` 判断（CAA 的写法可照搬给 Commons）。
- 开关 `curation_source_settings`；密钥 `curation_source_keys`（实例密钥加密，`curation/source_keys.go`）；声明里写 `Keys` 就自动得到 `key_missing` 状态与前端密钥行。设置接口 `/api/v1/admin/curation/sources`。
- MusicBrainz 客户端不解码 `relations`，`ArtistByID` 只带 `inc=aliases`（`musicbrainz/finders.go:132-138`、`entity.go`）。

## 可照搬的 B2 外部封面
- 程序层 `curation/program/enrich_media.go:27-101`；写入 `curation/external_artwork.go:97-170`（锁、三种跳过 local_present/occupied/suppressed、`WriteIfChanged`、表 `release_external_artworks`、失败清理）。
- 存储 `catalog/artwork/artwork.go`：内容寻址、`ARTW` 锁；`Lookup`、`Discard` 按两张表找引用，要并入新表。
- 图片接口 `/api/v1/artworks/{id}`（鉴权、`ETag`、`private, no-cache`、304）可直接给艺人图用。
- 阻碍：`externalOutcome` 固定以发行为目标（`external_artwork.go:80-86`），艺人要 `TargetArtist`；`operations_type_ck`（`0067:28-31`）与 `curation_work_states.subject` 的 CHECK（`0062:2`）要扩。

## 前端
- 路由 `/artists/$artistId`（`frontend/src/app/router.tsx:172-188`），页头 `features/artists/components/artist-header.tsx`，圆章 `artist-monogram.tsx`（注释“目前没有艺人图片”），列表 `artists-list-view.tsx:74`。
- 资料来源卡片 `features/curation/components/curation-sources-section.tsx`，能力枚举严格（`features/curation/api/decoders.ts:28,46`），新增能力必须同时改前端；文案 `curation-text.ts`。
- 新操作类型要改 `features/governance/` 的 decoders、change-summary、revert dialog。

## 管家
- 没有艺人资料相关工具；后台模型白名单 `app/curation_model.go:113-121`。

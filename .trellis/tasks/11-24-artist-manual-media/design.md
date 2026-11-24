# 设计：艺人手动资料与指定对应

锚点均见调研 `../11-24-artist-web-search/research/repo.md`（A–H 节）。

## 数据（迁移 0072）

- `artist_manual_artworks(artist_id UUID PK REFERENCES artists, storage_key, mime, width, height, operation_id UUID REFERENCES operations DEFERRABLE INITIALLY DEFERRED, updated_at)`。
- `artist_manual_bios(artist_id UUID PK REFERENCES artists, text CHECK 1..4000 字符, lang CHECK IN ('zh','en'), operation_id, updated_at)`。
- `operations_type_ck` 增加 `artist_manual_image`、`artist_manual_bio`、`artist_link`。
- down 迁移删表并恢复约束（有新类型的操作行时先删除这些行，照现有 down 的写法）。

## 读取

- `catalog/artists_media.go` 的 `readArtistMedia` 与 `artists_summary_sql.go` 的摘要图片：在同一家族 CTE（`ArtistMediaFamilyCTE`）内先取人工表，没有再取外部表；排序沿用 `artistMediaOrder`。
- `ArtistImage`/`ArtistBio` 的 `Source` 增加 `manual`（人工层的 `source_url`、`author`、`license`、`license_url` 为空/null；`source_url` 在 DTO 中改为可空，样本同步）。
- `ArtistDetail` 新增 `Links{MusicBrainz, Wikidata *string}`：取家族内有效身份的 `external_links(source, 'artist')`，有效身份没有时取最近并入方的。
- `artwork.Lookup` 与 `Discard` 的引用统计加入 `artist_manual_artworks`。

## 写入（新文件放在 `curation`；HTTP 放在 `curation/httpapi`，路由挂在 `/api/v1/artists/{id}/…`）

授权仿 `catalog/httpapi/artist_governance.go` 的 `authorize`：同源 → 管理员 → UUID → 写操作要 `Idempotency-Key`；人工操作不带 Origin，经 `operations.Run`。

| 接口 | 说明 |
| --- | --- |
| `PUT /api/v1/artists/{id}/image` | multipart，唯一字段 `image`；仿 `dspsettings/httpapi/ir.go` 的读期限、`MaxBytesReader`、逐 part 读取；`artwork.ValidateImage` + `Describe` + `WriteIfChanged`。操作 `artist_manual_image`，Before/After = 人工图片快照或 null |
| `PUT /api/v1/artists/{id}/bio` | JSON `{text, lang}`；去首尾空白，按码点计长度；操作 `artist_manual_bio` |
| `POST /api/v1/artists/{id}/media/reject` | JSON `{field: "image"|"bio"}`；锁内读当前显示层：人工层 → 写一条 `artist_manual_*` 清除操作；自动层 → 对该行的 `operation_id` 走现有撤销（`artistMediaKind.Revert`，写抑制）。返回 `{operation_id, layer}`；没有可处理的资料 → 409 `nothing_to_apply` |
| `POST /api/v1/artists/{id}/link-preview` | JSON `{url}`；只读，不要幂等键 |
| `PUT /api/v1/artists/{id}/links` | JSON `{source: "musicbrainz"|"wikidata", external_id}`；操作 `artist_link` |

锁顺序：操作幂等锁 → CATP（`catalog.AcquireArtistWriteGate`）→ `ResolveArtists` 解出有效身份 → 艺人行 `FOR UPDATE`（复用 `lockArtistMedia`）。写入目标总是有效身份；请求的 ID 已被并入时按有效身份处理并在响应里返回实际 ID。

### 预览与对应

- 输入解析：`musicbrainz.org/artist/<uuid>`（含 `beta.` 子域、尾部路径）、裸 UUID → MusicBrainz；`wikidata.org/wiki/Q\d+`、`wikidata.org/entity/Q\d+`、裸 `Q\d+` → Wikidata。其他 400 `invalid_input`。
- MusicBrainz：`ArtistByID`（经 `curation.Sources` 的闸门，源关闭或不可用返回 409 `source_inactive`）取名字、消歧义短语、别名；url-rels 给出 Q 号。
- Wikidata：在 `sources/wikidata` 增加按 Q 号读 `labels|aliases|descriptions|claims`（zh、en）的方法，解析 P434（MBID）、P31。
- 名字一致：`textnorm.Text` 比较条目的名字与别名和本地艺人的全部名字（`artist names` 读取）。
- `artist_link` 操作：
  - 输入 MusicBrainz → 写 `(musicbrainz, artist, <有效身份>) = mbid`；条目的 url-rels 有 Q 号时同时写 `wikidata`。
  - 输入 Wikidata → 写 `wikidata`；P434 唯一时同时写 `musicbrainz`。
  - 已有同源对应时替换。Before/After 保存两源对应的完整行（含原 `operation_id`）；撤销恢复 Before。
  - 新增一个 `operations.Kind`（Revert/Describe/PreviewRevert）；撤销时只动 `operation_id` 仍是本操作的行，否则 `revert_conflict`。
  - 现有发行对应的撤销已按归属只删自己的行（`external_link_revert.go:117`）；补测试确认被人工替换后不会误删。
  - 不进 `checkArtistEvidence`，因此不触发自动合并。

## 自动整理接手

- `program/artist_media_pass.go` 的入口从“有 MusicBrainz 艺人对应”放宽为“有 MusicBrainz 或 Wikidata 艺人对应”；`artistEvidence` 增加 `WikidataIDs`，并纳入输入摘要（新对应 → 摘要变化 → 重跑）。
- `program/artist_media.go` 的 `artistLinks`：MBID 照旧；只有 Q 号时造一条 `ArtistLinks{WikidataID, Name}` 交给 wikidata 补 Commons 文件与维基标题。
- `artistMediaWrite.apply` 的依据从 `MBID` 泛化为 `Link{Source, ExternalID}`（musicbrainz 或 wikidata）；事务内复查“家族内仍有这条对应”，`Basis` 写 “MusicBrainz 艺人 …” 或 “Wikidata …”。指纹与 `artistMediaValue` 相应调整，旧操作的 Describe 仍可读。

## 规范更新（实现代理负责）

- `.trellis/spec/backend/artist-contracts.md`：读取字段（`manual`、`links`、可空 `source_url`）。
- `.trellis/spec/backend/curation-contracts.md`：人工层、`artist_link`、标记不对、入口放宽。
- `.trellis/spec/backend/sources-contracts.md`：Wikidata 按 Q 号读取的请求与外发数据。
- 接口样本（`backend/internal/app/testdata/api-samples`）。

## 风险

- 引用计数漏表会误删文件：`Lookup`、`Discard` 同改，测试覆盖人工与自动共享内容键。
- 旧前端解码：`source` 新值与 `links` 字段会让旧前端解码失败；前端子任务同步修改，合入顺序为后端先、前端紧随。

## 评审修订（规划子代理评审，2026-11-24，全部采纳）

以下条目优先于上文。

- R-A1 标记不对的锁顺序：在事务外读出当前显示层与其 `operation_id`，据此定 `Spec.Type` 与指纹后再进 `operations.Run`；自动层分支包一层撤销执行器（先锁原操作行，再 CATP、艺人行），锁内复查“仍没有人工层、该行仍归这条操作”，不满足返回 409 `revert_conflict`。不在 CATP 之后再去锁操作行（`operations/gate.go:14`、`kind.go:68,83`）。
- R-A2 图片撤销可恢复：新表 `artwork_retained(storage_key, operation_id, PRIMARY KEY(storage_key, operation_id))`，人工图片被替换或清除、`artist_link` 清掉自动图片时，把旧内容键登记给本操作；`artwork.Lookup`/`Discard` 计入这张表。登记行不清理（数量受管理员操作约束），在规范中写明。
- R-A3 改对应后清掉旧资料：两张外部表加 `basis_source`、`basis_id`（迁移回填：现有行为 `musicbrainz` + 当时的 MBID，取自操作的指纹或 Basis；取不到时按艺人当前唯一的 MusicBrainz 对应）。`artist_link` 在同一事务内删除依据已不在新对应集合里的自动行，Before 记录它们（图片内容键按 R-A2 保留），撤销时恢复。
- R-A4 发行对应撤销：`external_link_revert.go` 对归属 `artist_link` 的行改为跳过（不让整条发行操作变得不可撤销），Describe 说明跳过的数量。`artist_link` 写入与现有对应完全相同时返回 `nothing_to_apply`，不改写 `operation_id`（避免抹掉自动合并证据 `artist_merge.go:85`）。
- R-A5 与治理接口一致：图片、简介、对应三个写接口带 `expected_revision`（与艺人详情的 `revision` 相同），锁有效身份并递增 `edit_revision`；版本不符返回现有的版本冲突错误。`media/reject` 同样带 `expected_revision`。
- R-A6 给网络搜索子任务留接口：`artist_link` 的执行器接受人工（无 Origin）与模型来源（Origin，含批次与会话）两种写入者；模型来源遇到已有同源对应返回 `occupied`，不替换；模型来源的撤销写 `external_link` 抑制标记（复用现有 subject）。只有 Wikidata 对应时跳过需要 MBID 的 Fanart.tv 与 TheAudioDB。mock 的 Wikidata 按 Q 号读取（labels、aliases、descriptions、P31、P434）在本子任务中实现。
- R-A7 拆分：人工资料与 `external_links(artist)` 留在写入时的有效身份上，拆分恢复出的身份不带走（与自动资料一致），写入 `artist-contracts.md`。

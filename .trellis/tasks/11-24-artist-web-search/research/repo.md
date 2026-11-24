# Research: 艺人资料补全的仓库现状（A–H）

- **Query**: 手动资料与指定对应、模型联网认人与模型整理、插件按名字查、来源开关、前端锚点、迁移与 mock
- **Scope**: internal
- **Date**: 2026-11-24

路径相对仓库根目录；`B=backend/internal`，`F=frontend/src`。

---

## A. 艺人媒体现状

### 表与迁移
- `backend/migrations/0068_artist_media.sql:3-30`：`artist_external_artworks`、`artist_external_bios`，**主键都是 `artist_id`**（每位艺人各一行）。图片列：storage_key/mime/width/height/source/source_url/author/license/license_url/operation_id；简介列：text(1..4000)/lang(zh|en)/source/source_url/license/license_url/operation_id。`commons` 必须带 license（:16）。
- 0068 还加了 `artists.auto_revision`（:1）、操作类型 `artist_external_artwork|artist_external_bio`（:32-36）、工作状态 subject `artist_media`（:38-40）。
- `0071_source_plugins.sql:19-22`：source CHECK 改为 `IN (内置) OR ~ '^plugin:[a-z0-9-]{3,40}$'`。

### source 枚举的三处
| 层 | 位置 | 取值 |
|---|---|---|
| DB CHECK | `0068:9,24` → `0071:20,22` | 图片 fanarttv/theaudiodb/commons/plugin:*；简介 wikipedia/theaudiodb/plugin:* |
| Go 写入 | `B/curation/artist_media.go:53-63`（图片）、`:99`（简介） | 同上，plugin 用 `externalProviderPattern`（`external_artwork.go:30`） |
| 前端 decoder | `F/features/artists/api/decoders.ts:64-65,69,81` | `ARTIST_IMAGE_SOURCES=["fanarttv","theaudiodb","commons"]`、`ARTIST_BIO_SOURCES=["wikipedia","theaudiodb"]`，**不含 plugin:***；展示名表 `F/features/artists/components/artist-bio.tsx:5-10` |
| 合同 | `.trellis/spec/backend/artist-contracts.md:73-82` | 同前端（未写 plugin） |

注意：插件来源写入的艺人资料在前端 `d.oneOf` 会解码失败（现存缺口，新来源要一并改）。

### 写入链 `Writer.ApplyArtistImage/ApplyArtistBio`（`B/curation/artist_media.go`）
- 请求体 `ArtistImageRequest/ArtistBioRequest{ArtistID, MBID, BatchID, Key, RequestID, Image|Bio}`（:16-24）。
- 图片：`w.artworks.Describe` 校验（:64）→ 事务内 `WriteIfChanged` 落盘（:74）→ INSERT（:78）；失败且写过文件则 `discardArtistImage`（:84-95）。
- 公共 `artistMediaWrite.apply`（:113-164）：
  1. `uuid.Valid(mbid)`、`SourceURL`、`ExternalID` 必填（:114）——**没有 MBID 直接 invalid_input**。
  2. `Source{Kind:"source", Provider:value.Source, Basis:"MusicBrainz 艺人 "+mbid}`（:117），指纹含 type/artist_id/mbid/value（:118），`w.sourceSpec`（`external_artwork.go:44-59`，只认 Kind source|model，actor=开启者或 ModelRun.ActorID）。
  3. 事务内锁序：`lockArtistMedia`（:166-182：`catalog.AcquireArtistWriteGate` CATP → `ResolveArtists` → `SELECT … FOR UPDATE` 艺人行）→ `checkExternalWriter`（`external_artwork.go:61-73`：开启者未变、批次仍开）。
  4. 一条 SQL 算三个布尔（:133-140），范围是 `catalog.ArtistMediaFamilyCTE`（`B/catalog/artists_media.go:11-26`，有效身份+并入方）：`linked`=族内有 `musicbrainz/artist` 且 external_id=mbid；`occupied`=族内已有该表行；`suppressed`=族内 `curation_suppressions(target_type='artist', subject=artist_image|artist_bio, value_hash=identityHash)`。
  5. `!linked`→409 `evidence_changed`；occupied/suppressed→`nothing_to_apply`+Skipped（:144-151，`skip` 在 `external_artwork.go:75`）。
  6. `artistMediaOutcome`（:184-189）`auto_revision+1`，Before/After = `{image|bio: value}`，target=artist（operations 自动写 `operations_artist_targets`，`B/operations/operations.go:348`）。
- 快照 `artistMediaValue`（:27-39）= `externalIdentity{source,external_id}`+出处+资源/文本。

### 撤销与抑制（`B/curation/artist_media_revert.go`）
- `artistMediaKind{kind,subject,field,table}`：图片 `artist_image/image/artist_external_artworks`，简介 `artist_bio/bio/artist_external_bios`（:19-24）；注册在 `B/app/app.go:291`。
- `Revert`（:50-77）：`lockArtistMedia` → 只删 `operation_id=原操作` 的行（否则 `revert_conflict`）→ `insertSuppression(artist, id, subject, identityHash, {source,external_id})`（:69）→ AfterCommit 清图片文件。
- `Describe`（:79-129）展示 artwork 规格或 lang/length，`Revertible` 仅当该行仍归原操作。
- 抑制工具函数：`B/curation/suppressions.go:148-160`（`insertSuppression`:148、`identityHash = sha256(source\0external_id)`:158），`suppressionExists`:142；`curation_suppressions.operation_id` 可空（`0058_auto_curation.sql:91-102`），`ClearSuppression` 按 ID 删（:82-115）。
- 程序层读标记：`program/artist_media.go:52-55`（`e.Markers` 内比对 `subject:hash`）。

### 读取
- `catalog.Store.readArtistMedia`（`B/catalog/artists_media.go:30-54`）按 `artistMediaOrder`（:28，自身优先→最近并入→ID）各取一行；由 `LoadArtist` 调用（`artists_reads.go:97`）。
- 类型 `ArtistImage{Resource,Source,SourceURL,Author,License,LicenseURL}`、`ArtistBio{Text,Lang,Source,SourceURL,License,LicenseURL}`、`ArtistDetail{Image,Bio,Artist,Revision,NameSource,Names,…,RedirectArtistID}`（`B/catalog/artists_types.go:53-81`）。**ArtistDetail 不含外部对应/MBID**。
- 摘要里的 `artist.image`：`B/catalog/artists_summary_sql.go:28-29`（同一族与排序）。
- 图片资源读：`B/catalog/artwork/artwork.go:41-53 Lookup`，清理引用计数 `Discard` :193-218 —— 两处都硬编码 `release_artworks/release_external_artworks/artist_external_artworks` 三表。

### 程序层 pass
- 调度：`B/curation/program/worker.go:243` 在发行两轮 walk 之后调 `walkArtistMedia(ctx, checkPriority)`，之后 `AdvanceReleaseMergeQueues`。
- 分页输入 `artistMediaPage`（`program/artist_media_pass.go:24-61`）：**`page` CTE 只从 `external_links source='musicbrainz' object_type='artist'` 起步（:26-27）**——这就是没 MBID 的艺人进不来、插件也不会被调用的根因。每行 `artistEvidence{ID,Name,MBIDs,Image,Bio,Markers}`（:16-21）。
- 来源状态摘要 `artistProviders`（:68-103）：固定名单 musicbrainz/wikidata/commons/wikipedia/fanarttv/theaudiodb + 带 artist_image/bio 能力的 plugin。
- `processArtistMedia`（:147-187）：`needsWork(subject,state,input…)`，`unit{subject:"artist_media"}`，状态 done/no_match/try_later 退避。
- 补全 `enrichArtist`（`program/artist_media.go:141-160`）：`artistLinks`（:26-50，每个 MBID 经 musicbrainz、wikidata 的 `ArtistLinks` 补 Q 号/Commons 文件/维基标题，最后 `entry.Name=e.Name`）→ `artistImage`（:76-105，顺序 fanarttv→theaudiodb→commons→plugins）→ `artistBio`（:107-139，内置 zh/en 后再插件）。写入走 `unit.writeArtist`（:57-74，先 `sources.Check(provider)`，`evidence_changed` 映射为 `errReleaseChanged`）。

---

## B. 曲库里的“人工层”模式

- 发行字段：人工层 `release_corrections`（`0017_governed_operations.sql:44`），自动层 `release_auto_values`（`0058_auto_curation.sql:1`）。人工写入 `governance.CorrectionExecutor`（`B/catalog/governance/corrections.go:403`），曲目 `TrackCorrectionExecutor`（`track_corrections.go:125`）。
- 人工优先的写入侧判定：`Writer.applyFields`（`B/curation/writer.go:130-140`）模型写入且有 ExpectedRevision 时，字段已有人工修正→`manual_override`；`OnlyEmpty` 时人工或自动已有→`occupied`（`filterAutoFields` :177-228）。
- **人工封面上传不存在**：全仓 multipart 只出现在 `B/dspsettings/httpapi/ir.go:73-130`（IR 上传，可照搬：读期限、`MaxBytesReader`、`MultipartReader` 逐 part、字段白名单）与 `B/plugins/httpapi/handlers.go:29,49`（插件包）。
- 图片校验/受管存储可直接复用：`artwork.ValidateImage`（16 MiB、MIME 嗅探、尺寸，`artwork.go:90-104`）、`Store.Describe`（内容寻址键，:106-115）、`WriteIfChanged`（键锁 ARTW + 原子落盘，:138-187）、`Discard`（:193-218）。`curation.Writer` 已持有 `w.artworks`（`settings.go:23-31`）。
- 管理员守卫与人工操作写法：`B/catalog/httpapi/artist_governance.go`
  - 路由 :33-39（`POST /api/v1/artists/{id}/merge|split|split-preview|candidate-ignore`、`PATCH …/name`）。
  - `authorize`（:41-63）：`RequireSameOrigin` → `RequireAdmin` → id UUID → 写操作要 `Idempotency-Key`。
  - `decodeArtistCommand`（:64-80，64 KiB、DisallowUnknownFields）。
  - `run`（:144-151）：`operations.Run(Spec{ActorID, Key, Type, Fingerprint, RequestID}, executor)`，**人工操作不带 Origin**。
  - 执行器样板：`governance.RenameExecutor`（`B/catalog/governance/artist_rename.go:10-60`：CATP 锁→`lockArtists`→版本核对→写 `artist_overrides`→`incrementArtists`（edit_revision）→返回 Outcome target=artist）。
- operations 包：`Kind` 接口 Type/Revert/Describe/PreviewRevert（`B/operations/kind.go:11-…`），类型常量 `operations.go:49-50,59`，`Origin` 字段 `operations.go:139-150`。新操作类型需改 `operations_type_ck`（最近一次在 0068:32-36）。

---

## C. 外部对应的手动写入

- **没有任何管理员手动建立/修改 external_links 的接口**（curation 路由见 `B/curation/httpapi/handlers.go`：sources/progress/exceptions/settings/batches/reverts/suppressions；catalog 路由无 link）。`curation.PutLink`（`links.go:41`）无外部调用。
- `external_links` 表：`0060_curation_sources.sql:8-20`，object_type 含 `artist`，`UNIQUE(source,object_type,object_id)`，source 只要匹配 `^[a-z0-9._-]{1,64}$`（`wikidata` 可写），object_id 无外键。
- `Writer.ApplyLinks`（`B/curation/external_link.go:164-237`）是**发行作用域**：锁 `LockAutoRelease`、`validateLinkMembers` 要求艺人在该发行署名（:94-119）；艺人对应只接受 `Source{Kind:"source",Provider:"musicbrainz"}`（:83-85）；`Source.Kind` 全局只认 `source|model`（`writer.go:74`、`external_artwork.go:50`），**没有 human**。人工采纳由 `ConfirmedBy` 字段表达（`model_run.go:47-56,90-104`），而非 Kind。
- 撤销 `externalLinkKind.Revert`（`external_link_revert.go:29-68`）锁发行、逐条删并写 `external_link` 抑制；target 必须是 release。
- 艺人对应 → 自动合并入口：`program/artist_links.go:96-128 writeArtistLinks`（ApplyLinks 后 `ObjectsLinkedTo(musicbrainz,artist,x)` ≥2 则 `mergeLinkedArtists`）→ `Writer.MergeArtists`（`artist_merge.go:40-72`）。强证据 `checkArtistEvidence`（:78-92）要求两侧对应都归属**未撤销的 `external_link` 操作、target_type=release、after_state.source=musicbrainz**。因此用新的艺人级操作类型写手动对应时，不会被当作自动合并证据（自然避开）；若想复用合并需另行放宽。
- Wikidata：`B/sources/wikidata/client.go:35-111`。有 `links.WikidataID` 时跳过 P434 搜索，直接 `wbgetentities ids=Q… props=claims|sitelinks`，**只解析 P18 与 zh/enwiki sitelinks（:66-78），不读 P434**。MBID→Q 走 `srsearch=haswbstatement:P434=<mbid>`（:41）。Q→MBID 需在同一 `Claims` 结构里加 `P434`（数据值是字符串 MBID）。MusicBrainz 侧 `musicbrainz/artist_links.go:11-41`（url-rels 取 Q 号与维基标题），`ArtistByID`/`SearchArtists`（`musicbrainz/finders.go:132-155`）。

---

## D. 模型层

### 待定项
- DDL：`0062_curation_program.sql:18-35`：**`target_type CHECK (='release')`、`target_id REFERENCES releases(id)`**，kind 最新 CHECK 在 `0067_release_merges.sql:33-35`（release_match/recording_match/release_merge）；唯一索引 `(target_type,target_id,kind) WHERE status<>'resolved'`。0064:10-18 加会话/领取/次数/搁置列。
- Go：`PendingItem` 结构 `B/curation/work_states.go:80-92`；建项 `UpsertPendingItem` :94-111（**VALUES 写死 `'release'`**）；`ResolvePendingItems` :113-117（写死 release）。调用处：`program/pass.go:492`、`release_merge_discovery.go:145`。kind 判断散落：`model/exceptions.go:397`、`program/model_matcher.go:149`、`program/pass.go:484`。
- 领取/计数都 JOIN 可见发行：`pending_items.go:61-66 ClaimPendingItems`、`model/scheduler.go:137-139`（是否有到期项）、`model/status.go:85-89`（进度计数）。**artist 目标的项不会被领取或计数**，需改这三处。
- `checkWrite`（`model_run.go:78-87`）对 ModelRun.PendingItemID 核对 `target_id=releaseID`；艺人媒体写入的 `artistMediaWrite.apply` 目前不核对待定项归属。

### 调度与额度
- `model.Scheduler`（`B/curation/model/scheduler.go`）：`poll`:113-156（先续审到期会话，再新建会话领 `BatchSize`=5 项）；首条消息 `firstMessage`:158-165；`runBatch`:178-260（批次懒开、`BeforeStep` 复查条件、`Usage` 记账、`Escalate`:213-228 按工具名把例外绑到本批项——**只认识 resolve_release_match/merge_releases（按 item_id）与 correct_*（按 release_id），其余返回 ErrStopped**）；收尾 `finish`:281-312。
- 条件与每日额度：`model/status.go:12-77`（开关/开启者/暂停/`sources.Check("musicbrainz")`/`launcher.Availability`/`DailyTokenLimit` vs `TodayUsage`→`limit_reached`/provider 退避）。用量 `B/curation/model_usage.go:14,20`。

### 后台会话与工具
- 启动：`B/app/curation_model.go:61-101 backgroundLauncher.Start` → `stewardapi.StartBackground`（`B/steward/httpapi/background.go:50-74`），`Caller.Source=tool.SourceCuration`、`SkipMemory`、`SystemSuffix`（curation_model.go:65）。**未设置 `RunParams.NativeSearch`**（交互对话在 `steward/httpapi/events.go:147` 按 `settings.SearchSource=="native" && effectiveNativeSearch(...)==Supported` 设置；`effectiveNativeSearch` 在 `steward/httpapi/settings.go:512`；runtime 透传 `runtime/run.go:313`、字段 `run_types.go:49`）。`backgroundConfig` 已拿到开启者的 `runtime.UserSettings`（background.go:27-37），可就地判断。
- 白名单：`backgroundToolAllowed`（`curation_model.go:113-121`），**不含 `web_search`、`fetch_page`**；`withBackgroundPolicy`（:124-174）在列出与调用时套白名单，并给 `search_music_metadata/get_music_metadata` 套 MusicBrainz 后台闸门（:157-171）。注册：`B/app/steward.go:139`（web 工具进普通注册表）、:159-168（白名单内的进 backgroundRegistry，另加 `curationtools.StewardTools`）。
- web 工具：`B/steward/web/tools.go:11-115`。`web_search` 的 `Available=s.searchAvailable`（`web/search.go:30-33`：**按调用者本人的 `SearchSource` 是 searxng 或 tavily**；后台 Caller.User 就是开启者，所以天然“按开启者的管家设置”）；`fetch_page` 无 Available、`egress.PublicOnly`（`web/fetch.go:54-80`）；每用户限速 :92-108。SearchSource 取值 none/searxng/tavily/native（`steward/httpapi/settings.go:168`）。native 搜索结果投影 `steward/provider/search.go`（Citation{URL,Title}）。
- 后台专用工具 `B/curation/stewardtools/tools.go:26-38`：`read_pending_items`（ScopeRead，输出 item_id/kind/release_id/conflicts/evidence/candidates :112-116）、`resolve_release_match`（ScopeShared+Preview+Revertible；输入 `{item_id, external_id, basis.note}`；`plan`→`matcher.Plan`，`resolve`→`matcher.Apply(ApplyOptions{Source:modelSource, Key, Batch, PendingItemID})`）、`shelve_pending_item`（ScopePersonal）。`merge_releases` 在 `release_merge.go`，`correct_*` 在 `correct.go`。共用件：`claimed`（:55-69，核对 ModelRun 与开启者）、`selectItem`（:70-84）、`bindItem`（:85-89）、`modelSource`（:152-158，Provider=`NormalizeProvider(model)`，Basis≤500B）、`toolError`（:40-49）。
- 独立审查：Scope=shared 且有 Preview 的工具调用先进方案（`steward/runtime/run_tools.go:33-41`，后台每步≤20 项），由审查模型裁决（`runtime/run_review.go`；未登记证据的工具一律 `needs_human_confirmation`/policy，:58-62；后台转人工走 `Escalate` :176-182）。ScopeRead/Personal 直接执行、不审查——“模型整理的图片与简介不经审查”可用非 shared 作用域实现。
- 审查证据：`B/app/steward.go:169-208 evidence`、`reviewTargetInput`（:285-327，按工具名 switch；新工具需加 case 或在 `catalog/stewardtools/review.go:47-55 SupportsReviewEvidence` 登记）；`ReadReviewTargets` 已支持 `artist_id`（review.go:58-68），`reviewExternal` 对 artist 目标附带其外部对应（steward.go:349-391）。

### 例外
- DDL `0064_auto_curation_model.sql:20-37`，tool CHECK 最新在 `0067:37-39`。
- Go：`ModelException`（`B/curation/model_exceptions.go:30-48`，含 `Release ExceptionRelease`、`Kind`）；`CreateException`:50-82；**`exceptionSelect`（:80-83）`JOIN release_effective r ON r.id=p.target_id`——艺人项会被列表丢掉**。
- 采纳/不采纳：`model/exceptions.go:61-101 Accept`（:76-83 按 tool switch）、`Decline`:349-；`classifyChange`:218-。
- REST：`GET/POST /api/v1/admin/curation/exceptions…`（`curation/httpapi/handlers.go`）。

---

## E. 插件

- `plugins.Source.ArtistImage`（`B/plugins/source.go:159-176`）调用参数 `{"mbid","name","wikidata"}`；`ArtistBio`（:178-194）`{"mbid","name","lang"}`。**已传 name**（来自 `ArtistLinks.Name`，程序层在 `program/artist_media.go:46` 填）。文档 `docs/plugins.md:38-39`。来源写成 `plugin:<id>`（source.go:156）。图片下载限于清单 upstream（`plugins/fetch.go:137-…`）。
- 注册为 curation 源：`B/app/app.go:279-287`（插件变化回调 `ReplacePlugins`，`SourceRegistration{Declaration:p.Declaration(), Covers/Lyrics/ArtistImages/ArtistBios: p, Unavailable: p.Reason}`）；`Declaration()` ID=`plugin:<manifest.id>`（source.go:13-28）。`Sources.PluginIDs(cap)`（`curation/sources.go:70-80`）按安装顺序。守卫包装 `curation/source_artist_media.go:20-54`（`withSourceKeys`）。
- 为何没有 MBID 就不调：入口分页只取有 MB 艺人对应的艺人（`program/artist_media_pass.go:26-27`）；`artistLinks` 只对 `e.MBIDs` 逐个造 `ArtistLinks`（`program/artist_media.go:30-48`），MBIDs 为空则 links 为空，图片/简介循环里 `for _, link := range links` 不执行；即便调到，`apply` 也会因 `!uuid.Valid(mbid)` 拒绝（`artist_media.go:114`）并在事务里要求 `linked`（:144）。

---

## F. 来源设置

- 后端形状 `SourceSettings{upstreams?, id, name, capabilities[], enabled, active, keys?, inactive_reason}`（`B/curation/sources.go:30-39`）；注册结构 `SourceRegistration`（:15-28）；`sourceState`（:92-132：总开关→开启者→源开关→`Unavailable()`→密钥）；`List`（:134-153）、`SetEnabled`（:155-174，upsert `curation_source_settings`）、`Check`（:211-258）。默认开关取 `Declaration.DefaultEnabled`。
- 内置注册列表 `B/app/app.go:255-277`。密钥 `source_keys.go`（只对声明了 Keys 的源）。
- DB：`curation_source_settings.source` CHECK `^[a-z0-9._-]{1,64}$ OR plugin:`（`0071:11-12`），`web_search` 合法。
- 能力枚举 Go `B/sources/sources.go:13-22`；前端 `SOURCE_CAPABILITIES`（`F/features/curation/api/decoders.ts:28`，decoder 要求 capabilities 至少 1 项且在枚举内 :46-56），`SOURCE_INACTIVE_REASONS` :31。
- 前端：`F/features/curation/components/curation-sources-section.tsx:37-140`（每行 Switch + 写自有改动的源才有“更多”菜单 :93-97；`sourceDescription` :24-35）；文案 `F/features/curation/curation-text.ts:37-79`（`capabilitiesText`、`sourceInactiveText`、`writesOwnChanges`、`isPluginSource`、`sentToSourceText` 及 `sentToSource` 表）。
- 程序层来源摘要固定名单在 `program/artist_media_pass.go:76-82`，加新源要进名单才会影响 artist_media 输入摘要。
- 最小改法（仅事实层面的落点）：在 `app.go:255-277` 再加一条 `SourceRegistration{Declaration: sources.Declaration{ID:"web_search", Name:"网络搜索", Capabilities:[artist_image, artist_bio], DefaultEnabled:false}}`（无 finder），即可复用列表/开关/`Check("web_search")`/按来源撤销；`Unavailable` 可返回开启者管家未配置搜索的原因（需新的 inactive_reason，前端 `SOURCE_INACTIVE_REASONS` 同步）。前端只需 `sentToSource` 加文案；`writesOwnChanges` 按能力会显示“更多”。

---

## G. 前端锚点

- 艺人页组合：`F/app/routes/artist-detail-page.tsx`（`AdminArtistHeaderActions` :37-53，“更多”= `useArtistAdminActions`；`isAdmin` :62；`renderHeaderActions` :68-71）。管理菜单 `F/features/governance/components/artist-admin-actions.tsx:28`；结果提示 `governance/components/artist-command-notice.tsx`。
- `F/features/artists/components/artist-header.tsx:20-60`（圆章 `image?.resource ?? artist.image` :41；角色 `roleLabelsOf` :11-13；`ArtistBio` :57）。
- `artist-bio.tsx`（`sourceNames` :5-10、`safeUrl` :13、`Credit` :34、`ArtistBio` :53-90）。
- `artist-monogram.tsx`（`monogramOf` :15、`ArtistMonogram` :36-，图片失败回落缩写）。
- `artist-detail-view.tsx`：`ArtistHeader` 调用 :102；track_role 分区 `trackRolesOf` :212-214、`TracksSection` :222-227、`RoleTracks` :229-335（角色切换链接 :290-300、表头文案 :306、`DataTable caption` :323）。
- decoders：`F/features/artists/api/decoders.ts:61-110`（image/bio/detail）。
- `artistRoleLabels` 定义 `F/features/catalog/api/artist-summary.ts:28`（release_artist 与 album_artist 都是“专辑艺人”），使用处：`artist-header.tsx:12`、`artist-detail-view.tsx:297,306,323`、`artist-filters.tsx:47`、`artists-list-view.tsx:17`、`artist-summary.ts:75`。
- 例外列表 `F/features/curation/components/curation-exceptions-section.tsx`：列固定为“发行”（`releaseName` :168-171，依赖 `exception.release`）、`ChangeLines`（:174-，按 `kind==="recording_match"` 区分“录音/发行”）、采纳/不采纳文案按 `tool` 分支（:135-146）。decoder `F/features/curation/api/decoders.ts:150-178`（`kind` oneOf 三项 :161、`tool` oneOf 四项 :175、`release` 必填 :165）；文案函数 `curation-text.ts:216,251,294,300`。
- 抑制列表文案：`curation-text.ts:32-33`（artist_image/artist_bio 已有）、:130-160。
- 管家搜索设置：`F/features/steward/settings/agent-search.tsx`（search_source）。
- 共享 UI（`F/shared/ui/index.ts`）：`useTooltip`（tooltip.tsx:22）、`MenuButton`/`useMenu`（menu-button.tsx:71,187）、`ConfirmDialog`、`ModalDialog`、`Notice`、`FailureNotice`、`TextField`、`Switch`、`SettingRow/SettingsGroup`、`Button`、`Avatar`。**没有 toast**（结果提示用 `Notice` 与 governance 的 command-notice）。
- e2e：`frontend/e2e/artist-media.spec.ts`、`artists.spec.ts`、`artists-governance.spec.ts`、`curation.spec.ts`、`plugins.spec.ts`、`steward-web.spec.ts`、`steward-review.spec.ts`；支撑 `e2e/support/artist-media-seed.ts`、`artist-seed.ts`、`artists.ts`、`plugins.ts`、`steward.ts`、`api-mock.ts`；真实栈 `e2e/real/artists.real.ts`、`curation.real.ts`。单测样本 `F/test/artist-media-data.ts`、`artist-media-operations.ts`、`api-samples.ts`（读 `backend/internal/app/testdata/api-samples`，由 `B/app/api_samples_integration_test.go` 生成）。

---

## H. 迁移与 mock

- 当前最大迁移 **0071**（`0071_source_plugins.sql`/`.down.sql`），新迁移从 0072 起。`B/platform/postgres/migrations_rollback_integration_test.go:444,475` 对 71 有特判，`B/app/migration_version_integration_test.go` 也涉及版本。
- mock 路由 `B/upstreammock/server.go:58-84`：musicbrainz `ws/2/{entity}[/{id}]`；`/wikidata/w/api.php`（`artist_media.go:43-75`：`query+haswbstatement:P434=`、`wbgetentities` 只回 P18 与 sitelinks，**不回 P434**）；`/commons/…`、`/wikipedia/{lang}/api/rest_v1/page/summary/{title}`、`/fanarttv/…`、`/theaudiodb/…`；`/plugins/lyrics-demo/main/{kind}`；`/searxng/search`、`/tavily/search`（`search.go`，结果来自 `s.data.Search`）；模型 `/openai/v1/responses`、`/openai[/chat]/v1/chat/completions`、`/anthropic/v1/messages`（`model.go`，脚本驱动 `script.go`，models 接口声明 `server_tools.web_search.supported=false` :21、native web_search 请求返回 400 :59）。数据 `B/upstreammock/testdata/catalog.json`；场景 `PUT /_mock/scenarios/{service}`、请求记录 `GET /_mock/requests`。

---

## 建议的最小改法与风险

**手动资料（人工层）**
- 存储：现表主键是 `artist_id`，人工与自动无法并存。最小做法是新建 `artist_manual_artworks`/`artist_manual_bios`（或给现表加 `layer` 列并改主键为 `(artist_id, layer)`）。读取三处要一起改优先级：`catalog/artists_media.go:30-54`、`artists_summary_sql.go:28-29`、`artwork.go` 的 `Lookup`/`Discard` 引用计数。
- 写入：仿 `artist_governance.go` 的 `authorize`/`run` 加艺人路由，执行器仿 `RenameExecutor`（CATP→行锁→`edit_revision`），新操作类型 `artist_manual_image/bio`（改 `operations_type_ck`），Kind 的 Revert/Describe 仿 `artistMediaKind`。图片上传仿 `dspsettings/httpapi/ir.go` 的 multipart，加 `artwork.Describe/WriteIfChanged`。
- “标记不对”：对自动层资料直接复用现有撤销（`artistMediaKind.Revert`（artist_media_revert.go:50-77）已写 `artist_image/artist_bio` 抑制）；前端调现有 `POST /api/v1/admin/curation/reverts` 或单条撤销接口即可。
- 指定对应：没有现成接口。`ApplyLinks` 是发行作用域并只接受 MusicBrainz 程序来源，不适合直接复用。建议新的艺人级操作（写 `external_links(musicbrainz, artist)`，target=artist），Wikidata Q 号先经 `wikidata` 客户端补读 P434 换成 MBID。写入后把该艺人的 `artist_media` 工作状态标为需重做（`needsWork` 看输入摘要，新 MBID 会改变摘要，自然重跑）。这种新操作类型不满足 `checkArtistEvidence` 的 `external_link/release` 条件，不会触发自动合并；要不要合并需另外决定。

**网络搜索（模型层）**
- 待定项：`curation_pending_items` 需放开 `target_type`（加 `'artist'`）并去掉 `target_id → releases` 外键（或拆列）；改 kind CHECK；`UpsertPendingItem`/`ResolvePendingItems` 去掉写死的 `'release'`；领取（`ClaimPendingItems`）、到期判断（scheduler.go:137）、进度计数（status.go:85）改成“可见发行或有效艺人”。`curation_work_states` subject CHECK 若新增 subject 也要改。
- 工具：在 `curationtools.StewardTools` 加 `resolve_artist_identity`（shared+Preview→独立审查）与 `submit_artist_media`（非 shared，直接写自动层）；同步改 `backgroundToolAllowed`、`Scheduler.Escalate` 的工具分支、`reviewTargetInput`/`SupportsReviewEvidence`、`ExceptionResolver.Accept/Decline` 的 switch、例外 tool CHECK。
- 联网：后台白名单加 `web_search`/`fetch_page`（`web_search` 的 Available 已按开启者 SearchSource 判断）；native 需在 `StartBackground` 按开启者设置设 `NativeSearch`。“网络搜索”源开关应在建项与工具调用前用 `sources.Check("web_search")` 拦截。
- 模型整理的写入：`artistMediaWrite.apply` 写死 MBID 必填、`linked` 校验和 `Kind:"source"`；需要一个不依赖 MBID 的分支（Source.Kind=model、provider=web_search 或模型名、external_id 用引用网址哈希），并在 DB/Go/前端三处 source 枚举加新值。图片 URL 下载要走 `egress.PublicOnly` 加 `artwork.ValidateImage`，现在没有通用的“任意公网图片下载”函数（`sources.Requester.DownloadArtistImage` 只放行白名单主机）。

**插件按名字查**
- 入口分页（`artist_media_pass.go:24-61`）要加“没有 MB 对应的主署名艺人”分支，并给 `ArtistLinks{Name}` 一个空 MBID 的 entry；`apply` 的 MBID 与 `linked` 校验要改成允许无 MBID 的情况（换别的复查条件）。

**风险**
- 例外列表（`exceptionSelect` 内连接 `release_effective`）和前端例外 decoder（`release` 必填、kind/tool 用 oneOf）都假定目标是发行。艺人例外不改这两处会直接消失或解码失败。
- 前端艺人 decoder 现在就不认 `plugin:*`，新来源值不加进去整页详情会解码失败（`api-contract.test.ts` 的样本测试会先报错）。
- `artwork.Discard` 只统计三张表的引用；新加人工图片表不纳入的话，撤销自动图片可能误删共享同一内容键的人工图片文件。
- 后台额度：web 搜索结果计入模型 token，`DailyTokenLimit` 自动生效；web 工具自身有每用户每分钟限速（`web/tools.go:92-108`），与开启者交互对话共享名额。
- 发行 `checkWrite` 的待定项归属核对写死了 `target_id=releaseID`（`model_run.go:78-87`）。艺人工具要自己做同样的归属核对，否则模型能写批外的艺人。

## Caveats / Not Found

- 未找到手动封面上传、手动 external_links 接口、toast 组件，以上都按“不存在”记录。
- 未读 `program/model_matcher.go` 的 Plan/Apply 细节和 `run_review.go` 的审查策略全文，只记了入口。
- 没有做外部检索（Wikidata P434 的数据形状按现有 P18 解析方式推断：`claims.P434[].mainsnak.datavalue.value` 是字符串）。

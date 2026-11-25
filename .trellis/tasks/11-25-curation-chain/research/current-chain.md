# 现状：自动整理的逻辑链（2026-11-25 读代码所得）

只读调查，没有运行程序。标“疑点”的条目是按代码推断、没有用例验证的。路径相对仓库根目录下的 `backend/internal/`。

## 1. 触发与调度

- 程序整理随服务启动：`app/app.go:370-374`。默认轮询 1 分钟、扫描让路 30 秒、全量间隔 6 小时：`curation/program/worker.go:51-59`。
- 开跑条件 `ready()`：总开关开、开启者是启用中的管理员、未暂停、没有扫描：`worker.go:111-130`。
- 不是扫描回调触发，而是轮询变化标记 `LoadProgramChanges`：`curation/work_states.go:30-47`，`worker.go:264-279`。
- 一轮的顺序（单个 goroutine，`worker.go:191-281`）：
  1. 判定声纹是否可用；
  2. 全部发行：识别，缺封面时紧接着取封面（`pass.go:125-133`）；
  3. 全部曲目的歌词（`pass.go:84-124`）；
  4. 全部艺人的图片与简介、艺人身份待定项（`artist_media_pass.go:185-245`）；
  5. 推进发行合并队列。
- 发行按 `created_at DESC, id DESC` 分页，每页 100，没有按缺口排的优先级：`catalog/curation_evidence.go:19-40`。
- 歌词与艺人阶段每分钟检查发行变化标记，有变化回到发行阶段（`worker.go:212-237`）；发行阶段本身按页往下走，中途新入库的发行要等这一轮结束。
- 扫描插入时关批次、等 30 秒、从当前页继续：`pass.go:33-38`。

## 2. 是否重做（needsWork，`pass.go:155-177`）

- 规则版本不等、证据摘要不同、`try_later` 到期：重做。
- 发行摘要是整份 `CurationEvidence` 的指纹：`curation/release_merge_evidence.go:16`。哪些字段变化会触发重查没有逐项核对。
- `StoredRuleVersion = 101`（规则版本 1 × 100 + `textnorm.Version` 1）：`worker.go:17-20`。任一升级都会让全部发行、封面、歌词、艺人资料重做。
- `no_match`、`uncertain` 不定期重查；只在之前声纹不可用、现在可用时重开。

## 3. 外部请求

- MusicBrainz 每个进程一个限速器，两次发出之间至少 1 秒，且串行：`sources/musicbrainz/client.go:18`，`sources/limiter.go:30-96`，`sources/request.go:411-417`。
- Cover Art Archive 的清单请求挂在 MusicBrainz 的限速器上：`sources/coverartarchive/client.go:53-56`。
- AcoustID 334 ms，LRCLIB 1 s，TheAudioDB 2 s。
- 磁盘缓存：200 响应 30 天，404 或空结果 24 小时：`sources/cache.go:105-110`。
- 429、503、网络错误在后台最多重试 5 次，之后同一请求地址 10 分钟内直接返回 `ErrTryLater`：`sources/request.go:293-354`，`sources/cache.go:58-96`。是按请求地址暂停，不是整条队列暂停。
- `try_later` 的退避：10 分钟起翻倍，最多 24 小时：`pass.go:302-332`。
- MusicBrainz 地址只能在测试里替换，没有用户设置：`app/app.go:228`。

## 4. 专辑查询顺序与请求数

顺序（`curation/program/match_lookup.go:219-376`）：标签里的 MB 发行编号 → 条码 → 目录号 → ISRC（最多 4 首）→ 碟片 ID → 声纹 → 标题加艺人搜索。

- 条码、目录号各一次搜索；ISRC 每首一次 `/isrc/`；命中后候选不超过 3 个时每个再 `ReleaseByID` 一次：`match_lookup.go:188-217`。
- 碟片 ID 查询的 `inc` 不含曲目：`sources/musicbrainz/disc.go:48`。
- 标题加艺人搜索：1 次，`limit 10`，带曲目数：`sources/musicbrainz/finders.go:78-85`。
- 典型 1–3 个 MB 请求，最坏 9 个，另加最多 4 个 AcoustID 请求。
- 艺人搜索用 `artist:"名字"`：`sources/musicbrainz/finders.go:140-146`。这种写法查不到别名（见 `external-services.md` 的实测）。

## 5. 判定规则

- 恰好一个候选通过才算对上：`curation/program/match_album.go:27-46, 245-305`。
- **文字搜索的结果一律标为 `search_only`，程序不判定**：`match_album.go:254-256`。
- 时长容差：`max(3 秒, 远端 × 1%)`，至少一半曲目可比且可比的全部在容差内：`match.go:167-173`，`match_album.go:214`。
- 散放单曲走录音模式，文字搜索的结果同样一律 `search_only`：`curation/program/match_single.go:18-19`。
- 模型提交编号时的复核（`ModelMatcher.readMatch`）只查碟数、位置冲突、曲目数相等，不比时长与文字；单曲只查编号存在：`curation/program/model_matcher.go:147-196`。
- 搜索只取前 N 条（发行文字搜索 10 条），返回的总数被丢弃：`sources/musicbrainz/client.go:79-97`。
- 标题与艺人比较用 `textnorm.Loose(textnorm.Text(s))`。`textnorm.Text` 做 NFKC、大小写折叠、拉丁附加符清理、假名与繁简转换：`catalog/textnorm/textnorm.go:15-21`。
- 国家、日期不参与判定。
- 冲突代码全集见 `match_album.go:61-218`、`match_lookup.go`、`fingerprint.go:178-213`、`match_single.go:12-57`。其中 `suppressed`（被撤销过）与 `existing_link`（已有别的对应）在模型提交时同样会被后端拒绝：`curation/program/model_matcher.go:87-110`。

## 6. 什么会进待定项交给模型

`savePending`（`pass.go:481-494`）：

- 对上的：收尾已有待定项。
- 带 `insufficient_evidence` 的：不建待定项。
- **其余全部建待定项**，包括候选为零的 `no_match`、`search_only`、`suppressed`、`existing_link`。

`insufficientEvidence`（`match_lookup.go:379-398`）：标题与艺人都是占位，且没有条码、目录号、ISRC、任何标识、无法重建碟片目录。

**疑点（未用例验证）**：没有艺人标签的文件被填成“未知艺术家”，来源记为 `display_fallback`（`library/parser/tags.go:130-134`，`library/scanner.go:966-983`）；占位判断只认 `default_fallback`（`catalog/curation_evidence.go:205-208`）。目录名兜底的专辑名来源是 `folder_fallback`（`catalog/ingest/organizer.go:895`），同样不算占位。后果是无标签文件会拿“目录名 + 未知艺术家”去搜索，再进待定项。现有集成测试直接插入 `default_fallback` 夹具（`app/curation_b3_program_integration_test.go:175-190`），没有覆盖真实扫描路径。

扫描阶段已经会解析文件名：去掉曲序前缀，并在艺人缺失、标题里恰有一个“ - ”时拆出艺人与曲名，来源记为 `filename_fallback`：`catalog/ingest/filename_numbers.go:158-176`。

## 7. 对上之后写什么（`curation/program/enrich.go:182-303`）

- 外部对应：发行、曲目的录音、发行组。
- 艺人对应：本地署名与 MB 署名按位置对齐，要求人数相同，名字规范化后相同或同位置的 MB 艺人标签一致：`curation/program/artist_links.go:16-36`。不看 MB 的别名。两个本地艺人对上同一个 MB 艺人时自动合并：`artist_links.go:96-163`。
- 发行字段只补空（`enrich.go:75-103`）：标题只在占位时；专辑艺人、厂牌、目录号、条码、年份在本地为空时。不补流派与版本说明。
- 曲目标题在占位或由文件名推出时补，曲目艺人在占位时补：`curation/program/track_fields.go:17-62`。
- 取封面要求发行级对应：`curation/program/enrich_media.go:19-21`；归组的强证据检查要求发行级对应的操作记录：`curation/join_group.go:78-92`；自动合并艺人只接受来源为 MusicBrainz：`curation/artist_merge.go:45`。
- 归组与发行合并发现：`enrich.go:110-157, 298-332`。
- 程序从不改已有的值；改已有值只有模型的 `correct_release`、`correct_track`。

## 8. 模型环节

- 调度器每分钟轮询，每批 5 项：`curation/model/scheduler.go:23-25, 70-77`。
- 首条消息只有待定项编号、类型、冲突代码，模型要先调 `read_pending_items`：`scheduler.go:162-168`。
- 后台可用工具（`app/curation_model.go:116-124`）：读本地库、`search_music_metadata`、`get_music_metadata`、`web_search`、`fetch_page`、`resolve_release_match`、`resolve_artist_identity`、`submit_artist_media`、`merge_releases`、`correct_release`、`correct_track`、`shelve_pending_item`。四个联网工具在“网络搜索”来源可用时出现：`curation_model.go:134-140`。
- 模型自己查 MB 走同一个限速器：`curation_model.go:168-180`。
- `resolve_release_match` 不限于候选，任何 MB 编号都可以，后端重新取数并复核：`curation/stewardtools/tools.go:39-40, 127-142`，`curation/program/model_matcher.go:63-140`。
- 每次写入经独立审查（另一次模型调用）：`steward/runtime/run_review.go`。后台运行里“需人工确认”转为例外：`run_review.go:176-182`，`curation/model/scheduler.go:226-250`。
- 审查不可用：按 5 分钟、30 分钟、2 小时重试，之后隔 24 小时：`scheduler.go:304-324`。
- 模型服务出错：整体退避 10 分钟到 6 小时，不计失败次数：`scheduler.go:291-301`。
- 一项第三次未处理后搁置：`curation/pending_items.go:83-88`。
- 每日 token 上限默认 1000 万：迁移 `0064_auto_curation_model.sql:43-46`；达到后状态 `limit_reached`：`curation/model/status.go:93-94`。
- 系统提示词：`steward/runtime/run_content.go:11, 235`，后台附加段 `app/curation_model.go:68`，审查提示 `steward/runtime/review.go:21-56`。

## 9. 艺人身份与资料

- 已有 MB 或 Wikidata 对应的艺人：程序直接取图片与简介，不进模型：`curation/program/artist_media.go:27-175`。
- 艺人身份待定项的条件：没有任何对应、有在库主署名、图文未齐、“网络搜索”来源可用：`curation/program/artist_media_pass.go:247-354`。
- 程序不直接按艺人名查 MB 建对应；艺人的 MB 对应只从发行匹配派生。

## 10. 数据分层

- 有效值 = 人工 → 自动 → 扫描：视图 `release_effective`、`track_effective`（迁移 `0058_auto_curation.sql:1-30`、`0065_track_corrections.sql:28-41`）。
- 重新扫描只写扫描列，扫描与入库代码里没有对自动层的任何引用。
- 自动层一个字段一个值，后写的替换先写的（ADR 0006 第 4 节）。
- **疑点（未用例验证）**：自动值不随扫描值变化而作废。标签后来补上或改了值，显示的仍是自动值。

## 11. 已有的中断处理

- 整理状态逐项落库（`curation_work_states`），重启后已完成的跳过。
- 模型运行中断：启动时 `RecoverInProgress`、`CloseOpenBatches`：`curation/model/scheduler.go:83-89`。
- 文件改名或移动后曲目身份按内容指纹延续：`catalog/ingest/identity.go:8-10`。
- 人工修改与自动写入的冲突靠版本号与证据摘要：`curation/model_run.go:63-80`，`curation/program/model_matcher.go:72-74`。
- 没有“目录稳定后再整理”的机制（`library/` 下检索不到）。
- 根目录暂时不可用时整理结果是否保留，没有核实。

## 12. 既有决定

- ADR 0006《外部数据源与自动整理》：原则（第 3 节）、分层（第 4 节）、撤销（第 5 节）、匹配依据与“唯一候选加一项独立佐证”（第 6 节）、强证据与发行合并由模型提出（第 7 节）、后台模型与审查（第 9 节）、默认关与不反复重试（第 10 节）。
- 产品合同的永久约束：弱相似不能做跨目录的权威合并（`.trellis/spec/guides/product-goals.md` “Permanent Invariants”）。
- 归档任务 `11-22-auto-curation-sources` 及其子任务是本链路的来源。

# 扫描归组技术设计

依据是本任务 PRD 的范围表、父任务 A1.4、A1.5（S4、S6）、A4，以及 ADR 0009 第 3.3、3.4 节。实施入口是扫描产生发行候选并保存的两遍流程。第一遍规划曲目与发行 ID，第二遍按同一范围重建候选、比较签名、落库。两遍必须看到相同的成员、锚点和碟序。

## 范围和数据流

| PRD 事项 | 落点 | 结果 |
| --- | --- | --- |
| 混放目录 | 下文“目录与曲序”第 2、3 步，`ingest/continuity.go`、`ingest/organizer.go` | 整目录 → 各格式 → 仅对失败子集按名拆 |
| 碟片子目录 | 第 1 步，`ingest/discmarker.go`、`scope.go`、`staging.go` | 上提到父目录后参加同一轮判定，碟标题写入介质 |
| 专辑名比较 | “名称与字段”第 1 条 | 共用合并键，删去两条别名补丁 |
| 没写专辑艺人时 | “名称与字段”第 2 条 | 逐曲计票，区分过半、并列和群星 |
| 版本词 | “版本词和附属文件”第 1 条 | 括号限定词单独存在扫描层 |
| 附属文件 | 同节第 2、3 条 | 以发行和根内相对路径绑定图片、文档 |
| 排序键 | “列表排序” | 按有效艺人名、有效专辑名在分页前排序 |
| 缺碟号、缺曲号 | “目录与曲序”第 4 步 | 保留明确编号，内部顺序可用，缺号无曲号事实 |
| 同一字段几首不一致 | “名称与字段”第 3 条 | 最高票唯一才选值，并列保留冲突证据 |
| 归组规则版本 | “升级和回滚” | 当前代码 7 → 8，只升一次 |
| 扫描规范 | “文件边界”末行 | 同步归组、签名和版本合同 |

扫描沿用 `library` 的发现、解析或缓存命中 → `ingest.SourceObservation` 暂存 → CUE 范围对齐 → 目录归一 → `PlanStagedRoot` → `OrganizeObservations` → `CandidateUnchanged` 或 `PersistCandidateWithFeedback` → 封面保存、候选状态记录。源文件身份仍由注册根、相对路径、内容指纹及 CUE 区间决定。文件里的 MusicBrainz 编号走原曲目标识写入路径。归组过程只消费扫描观察，模型归组记录由子任务 8 另行接入。

## 目录与曲序

1. **先确定逻辑专辑目录。** `locateObservation` 对音频所在目录的最后一段复用 `parseDiscMarker`，`Disc 2`、`Disk II`、`CD1 - 早期作品` 都上提一层。没有碟号的独立附加碟目录名也要识别，如 `专辑/Bonus`、`专辑/特典`；沿用已有附加词及其词边界，避免把普通专辑名中的子串当整目录标记。普通子目录仍各有范围。正碟使用目录明写编号，带名碟的 Suffix 写入 `MediumTitles[disc]`；附加碟按现有 `composeDiscParent` 的标题、原编号、路径顺序从最大正碟号后排（父目录直放的正轨也占用其碟号），纯附加目录从 1 起。保留原观察的标签碟号作证据。去除独占、必须有两碟、连续碟号、标签名相容等上提前提；删除 `CandidateMultiDisc` 直接成册跳过 S4 的捷径。格式拆分和按名拆后的候选仍携带原父目录的碟号和标题。

   暂存目前将 `A/CD1` 放在 `disc-parent:A`、`A` 的直接音频放在 `directory:A`。保留这两个初始范围。将独占标记步骤替换为 `AlignDiscStagingScopes`：对本次 scan/root 中存在的非空碟父范围，把对应 `directory:A` 的直属观察对齐到 `disc-parent:A`；按完整 scope 相等匹配，普通兄弟目录不动。两遍 organizer 都按逻辑父路径把这些观察放在同组。这样复用未读文件的 `disc-parent:` 稳定性判断，也能识别已有多碟发行。注册根下的碟目录使用专用父范围，普通根散曲继续走 `root-album` / `root-source`，不吞并整棵曲库。CUE 先对齐 sheet/父文件，按 sheet 所在目录取范围，先解决同源虚轨与整轨，再做连续性计数。原 `MarkDiscParentScopes` 仅为旧 Box 规则服务；无剩余生产者时一并删除独占标志、扫描调用和封面第 4 档死分支。已有 `box_leaf` 行随身份规划重算，新扫描按新逻辑目录取封面。

2. **S4 四次均检验整份成员。** 触发条件为同一逻辑目录出现多个不同的专辑名合并键。沿用 `continuousTracks` 的每碟集合判定，每首必须有正曲号，每碟集合恰为 1..该碟曲数，重号或缺号即失败。按以下顺序试，成功就收下这一份。只有一个标题的普通目录保留已有散曲及完整多格式版本规则。

   | 顺序 | 碟、轨的读法 |
   | --- | --- |
   | 1 | 标签/CUE 明写的碟、轨；上提目录用已确定的介质号区分各碟 |
   | 2 | `prepareFilenameEvidence` 的文件名碟、轨，缺文件名碟号用已知目录碟号，否则用逻辑第一碟 |
   | 3 | 逐字段使用明确值，缺的字段才由已有文件名证据补上；仍缺碟时用目录或逻辑第一碟 |
   | 4 | 所有成员放在单碟，只用文件名曲号，对整个子集要求 1..N |

   标签读法需排除 parser 的 `default_fallback`（其值可能是 1）、后续文件名补值及排序位置。明确黑胶分面先按现有 `applyVinylPositions` 得到同碟曲位，整个处理只换算一次。四种读法只是判组依据，不拿胜出的试读结果覆盖其他明确标签。整目录沿用至少两轨的连续性证据，按格式检验时单首编号 1 的版本也可成立。

3. **格式阶段按份决策。** 整组未通过时用现有 `observationFormat`（Codec 优先）分桶。FLAC 桶通过即可成候选，即使 MP3 桶失败；失败的桶单独回落。若只有一种格式，沿用本次失败结果。全员有专辑标签时仅按专辑名合并键分；有人缺失时，按（真实专辑艺人，否则真实曲目艺人，专辑名键或空）分。目录兜底专辑名不算标签，艺人与专辑都无证据的成员沿用按来源路径区分，不能并到票数最多的专辑。单一已知标题混入缺名曲时也不能靠多数吞掉缺名曲，先试连续结构。删 `directoryAlbumAliases` 的“一首异名”和“括号后缀”两条补丁。沿用 `stableAnchorPartition` 的路径锚，格式候选额外以格式区分；同时存在多张标题子集时各用自己的成员路径锚，不把所有同目录标题挂进同一版本组。原有同专辑多格式、只含散曲的子目录、根散曲与 CUE 有各自的对照样本。

4. **分开真实曲号和排列次序。** 明确的标签、CUE、专辑或目录中的 CD2、文件名 `1-03`、黑胶 `A1/B2` 照用。显式分隔的碟轨对不应因只有一个文件被降为首段数字；紧凑写法仍复用已有反年份、批量采信规则。去掉 `inferSequenceDiscs`，同碟出现 1,2,1,2 时保留重复曲号。无碟证据用第一个容器，不制造标签碟号。对默认轨号或被判冲突的文件名号清空候选副本的曲号；`fieldObservations` 只为实际采信的正曲号写观察，不写 0、默认 1 或 `path_order_fallback`。子任务 2 可独立更换解析缓存，旧缓存也走相同判定。

   `OrganizedTrack.Position` 和现有整数 `tracks.position` 表示曲号，0 是现有读接口可表达的缺值。新增 `OrganizedTrack.SortPosition` 和可空正整数 `tracks.sort_position` 表示碟内顺序。有明确曲号的按号排序，同号及未编号曲目使用 `naturalLess` 的文件名自然序、再按规范相对路径，未编号排在明确编号后。排序后从 1 给出内部次序。保存和读取有序轨表都用这个次序，含 `persist.go` 的 `orderedTracks`（作品和内嵌封面共用）；旧行或既有直接写者用 `COALESCE(sort_position,position)`。专辑详情在 `track_effective` 上读有效标题、艺人及曲号，并取视图中的排序列；整理、队列、Subsonic 读取时只把真实曲号对外传递。前端缺号展示由主代理接手。

## 名称与字段

1. **与子任务 5 共用的接口为 `textnorm.MergeKey(raw string) string`。** 实现在 `backend/internal/catalog/textnorm/keys.go`，先合入的一方提供。按 Unicode 完整大小写折叠、NFD 分解、删除组合变音、标点/符号及所有空白，最后 NFC 合成；不音译、不作简繁或假名转换，也不移动 The。空输入得到空键，空键不能证明同名。例：`Beyoncé — Live`、`BEYONCE LIVE` 都得到 `beyoncelive`，`The Who` 与 `Who` 仍不同。归组、根范围/专辑证据以及标题投票使用同一等价关系；展示和曲目观察保留原文，等价写法内取票数最多的拼法，同票取稳定原文。子任务 5 比较专辑和艺人时直接传入双方原文。现有 `textnorm.Text/Loose` 还服务搜索和艺人身份，有额外折叠或空白语义，不能替代新入口。
2. **艺人分布。** 保留明确专辑艺人、明确的合辑标志与群星占位处理。只有缺真实专辑艺人再按实际保留的曲目计数。复用 `firstTrackArtistCredit`、`PrimaryArtistCounts` 的真实首位艺人规则，每首至多一票，客串不增加人数。分母是候选实际曲数。某人严格过半选他，没人过半且有 2–4 位时按票数降序、同票按名称稳定排序，保留原始大小写，用 ` / ` 连接；5 位及以上选现有群星语义；零票或仅一位但缺证据过半，沿用原单值/缺值回退。补出的发行艺人和专辑艺人标明 `artist_distribution`，曲目艺人与署名原证据保留。并列由已有署名投影识别为多位，群星沿用合辑占位排除逻辑。整理侧也调用 `PrimaryArtistCounts.IsCompilation`，把分界改在共用判定，不在程序整理另写一套。
3. **字段相持。** `chooseValue` 沿用真实来源优先、可用值逐曲计票，唯一最高票取胜（如 2:1:1）。若最高票相同，产生空值、`uncertain_apply`、原因 `tied_candidates` 和现有有界原值列表；来源取自本轮投票的证据类型。分别处理平票与完全缺值。可选字段也把平票决定收入 `Decisions`，`release_field_decisions.selected_value` 写 SQL NULL；发行可空列相应为 NULL。必填文本列用既有空串或“未知专辑”展示占位，决定仍无选值，目录兜底、抓轨补值及专辑艺人分布均不得覆盖真实平票。沿用现有字段问题通道显示“有冲突”，不重复增加 grouping attention。多值流派继续合并，发行艺人分布依第 2 条处理。

## 版本词和附属文件

1. 新增 `OrganizedCandidate.VersionTerms []string` 与 `releases.scan_version_terms text[] NOT NULL DEFAULT '{}'`。候选从归一后的发行目录基名、各不同的原始专辑标签取配对圆、方、花、尖括号的叶内容；四位纯年份、EP、Single/单曲、OST/SDTK/Soundtrack/原声（带）排除。`(Remastered 2009)` 整体保留。先目录、再按稳定来源路径，依合并键去重，存第一次的原文。现有标签和固定目录模式写出的 `edition` 继续走原字段决定。此数组单独写入扫描发行行，进入候选元数据签名；版本词本身不加入锚、格式版本组、指纹、外部编号或显示字段。对完整原专辑名计算 MergeKey，保留括号里的全部内容，包括 Deluxe、Remastered。整目录不连续且回落到按名拆时，Album 与 Album (Deluxe) 各自成组。
2. 新增扫描自有表 `release_attachments(release_id uuid REFERENCES releases(id) ON DELETE CASCADE, relative_path text, kind text, image_format text, width integer, height integer)`，主键 `(release_id,relative_path)`。路径相对 release 对应的注册根。图片要求格式和正尺寸，文档三列为空。扫描集中在 `library`：逻辑专辑目录直属文件及其直接的 Art、Artwork、Scans、Extras（不区分大小写），不递归。按设计稿 9.3 支持 JPEG、PNG、TIFF、BMP、GIF、PDF，加上项目已有的 WebP；图片用 `image.DecodeConfig` 和已有 `x/image` 解码器读取实际格式、尺寸，PDF 只登记路径。复用根过滤、普通文件和非符号链接检查。按目录建一次扫描内索引，范围内多个候选可共享一份目录清单；同目录拆出的发行可各自关联同一文件。没有专辑目录的注册根散曲跳过，CUE 取 sheet 的逻辑专辑目录。音乐目录移动时随新的相对路径重建。封面挑选仍由 `saveArtworkForRelease` 单独负责，登记一张图不会提升它的封面优先级。
3. `candidateSignature` 接受同一份已列举清单，按相对路径序将路径、大小、微秒 mtime 加入签名。只改 PDF 或非封面图片也会重写该目录候选。命中时只 touch；未命中时才读图片头。用独立写入参数传递已读的附件集（nil 表示保留旧集，成功得到的空集表示清空），避免图片尺寸成为只有写入后才可得的候选签名输入。在原事务中按 release 整集替换。目录不可读、图片读头失败时保留已有绑定并记扫描诊断；此次不记录成功签名，下次重试。数据库失败回滚候选。已经成功列举为空时清掉旧关联，只有被完整观测的缺失才算删除。内容不复制，不增查看 API。

## 列表排序

排序在后端取有效名之后、LIMIT/OFFSET 之前完成。采用一个数据库函数 `catalog_list_sort_key(text) RETURNS text` 供专辑查询及艺人资料写入共同使用。顺序为修剪、大小写折叠、NFD、移除组合变音字符、去标点和符号、折叠空白、NFC，把词首的英文 `the ` 移到末尾；只有整词英文 The 适用，`Theatre`、`Les` 仍按原开头。SQL 用 PostgreSQL 18 的 `normalize`、`casefold` 与 Unicode 排序规则下的字符类；组合变音字符范围显式列出，结果用 `COLLATE "C"` 加现有 UUID 判平。不引入额外数据库扩展。

- 专辑读取 `release_effective` 的标题及艺人。普通版本列表和组列表共用标题、艺人、ID 次序；按组取代表版本后再排序。艺人页专辑列表、兼容浏览中的同类标题排序也使用该键。按加入时间、随机、搜索相关性的入口保留各自定义。
- 艺人取有效显示名生成列表键和 `InitialGroup(排序键)`，故 `The Beatles` 出现在 B 栏。现有 `artist_profiles.sort_key` 同时被文本过滤和 Subsonic 使用，增设 `artist_profiles.list_sort_key text NOT NULL DEFAULT ''` 供排序，保留原搜索文本键。以有效名生成列表键后送入已有首字母函数，仍使用项目拼音表。改名和撤销走现有资料刷新事务。迁移使艺人资料进入既有下一代回填（复用 maintenance 中换代状态更新），处理旧库全部艺人，覆盖未参与本轮扫描的手工姓名；down 同样重建旧首字母。搜索键、搜索投影版本及艺人身份键沿用原定义。

## 表、升级和回滚

新增一对 `backend/migrations/NNNN_scan_grouping.sql`、`backend/migrations/NNNN_scan_grouping.down.sql`，**合入时按当时最大号顺延**（本分支最大号 0097）。正向迁移给 releases、tracks、artist_profiles 各增一列，按数据库合同同步重建 track_effective 视图，建立 release_attachments、排序函数并启动艺人资料回填。已有曲目在下一次实际扫描前仍按原位置可读，已有发行没有版本词和附属关系。迁移不根据旧显示名猜专辑，亦不从旧补序反推真实曲号。

`backend/internal/catalog/ingest/version.go` 实际为 7，增量规范所列 5 已滞后。全部归组改动同批调到 8。单独升级本任务时，解析缓存仍可命中，签名中的归组版本强制对每个候选重写。子任务 2 同版发布时，由其 `CommonVersion` 触发一次重读。后续无变动扫描恢复跳过。标题拼写、版本词和附属内容可以导致重写，但不作为成员或发行身份。

曲目复用现有来源配对。发行沿用 `allocateReleases` 的严格过半方案；争用时依次按重叠数、原锚、候选键判定。扫描读 `origin_release_id` 保护手动并入及显示归属，照常处理旧组腾空后的收藏转移。已有外部对应、人工修正继续挂原 UUID；等量旧发行合并时没有任何一张过半，新发行获得新 UUID。旧记录保留在原 UUID，冲突的对应和人工修正不自动复制；只有沿用原 UUID 的对应、修正继续对该 UUID 生效。

降级先用现有迁移前备份和版本配套的回退执行器。down 对新版缺号行将仅用于排序的数值回填为旧版位置，删除本批新增列、表、函数，准备重建旧艺人列表。下次旧版扫描以版本 7 签名重算图；回退 DDL 只能恢复兼容结构，不能逆推出已重分的成员、收藏归属或原锚。要精确恢复升级前状态须一并恢复升级前数据库备份及对应版本。源音频和文档始终只读。

## 文件边界

下表目录均相对仓库根。花括号内是同一目录的一组文件。

| 目录 | 计划改动 |
| --- | --- |
| `backend/internal/app/dependency_rules_test.go` | 在 catalog/ingest 的 imports 中登记 catalog/textnorm |
| `backend/internal/catalog/textnorm`、`backend/internal/catalog/metadata` | 新增 `textnorm/keys.go`；调整 `metadata/compilation.go` 的共用计数结果 |
| `backend/internal/catalog/ingest` | `{organizer,continuity,discmarker,filename_numbers,scope,staging,observation,compilation,persist,persist_track,version}.go`，新增 `version_terms.go`。组织范围、碟序、字段决定、曲序、版本词和附件事务。身份算法与解析器是复用边界 |
| `backend/internal/library` | `scanner.go`、`candidate_signature.go`、新增 `attachments.go`。扫描枚举、签名、写入和已有封面分支衔接 |
| `backend/internal/catalog` | `{read,recent_additions,artists_reads,artists_collections,browse_albums,browse_artists,browse_tracks,curation_evidence}.go`、`governance/issue_read.go`、`internal/artistgraph/profiles.go`。涵盖专辑列表、艺人列表、轨序、问题和整理证据读取 |
| `backend/migrations` | 一对 `NNNN_scan_grouping` 正反迁移。测试代理在 `backend/internal/platform/postgres/migrations_rollback_integration_test.go` 的自动往返框架中补本次数据断言 |
| 前端交接（由主代理实施） | `catalog/labels.ts` 的冲突原因翻译，`catalog/components/{album-detail-view,track-info}.tsx` 和 `governance/components/release-issues-detail.tsx` 的缺号文案，沿用既有界面 |
| `.trellis/spec/backend` | 必改 `library-scan-grouping.md`、`library-scan-incremental.md`。同步 `artist-contracts.md`、`catalog-rest-contracts.md` 的列表与曲号说明、`database-guidelines.md` 的迁移归属。目录浏览和搜索匹配规则由原合同负责 |

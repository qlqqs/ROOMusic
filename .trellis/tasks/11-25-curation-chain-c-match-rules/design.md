# 整理链 C · 程序匹配与终止状态

已审阅，2026-11-25。适用范围为父任务 R5、R6、R7、R22、R23。程序分别回答“对上哪一个对象”和“哪些字段有证据可补”。同组的两个版本即使曲序、时长完全相同，也只能共同证明发行组。此区别贯穿取证、写入、撤销和离线重判。[父任务第 3 节](../11-25-curation-chain/design.md)　[实测记录](research/mb-query-probes-2026-11-25.md)

## 一、查询计划

沿用 `curation.Sources` 的后台授权回调、MusicBrainz 客户端及现有缓存。保留旧 finder 方法及调用签名，给整理增加带页信息的能力，分页协议定义在 `curation/search_pages.go`，具体来源由 `app/curation_musicbrainz.go` 接入，在 `curation/source_finders.go` 包装，每次调用先过闸门。模型工具现行的返回格式及条数仍由其原方法决定。线上与离线共用判定函数，只有线上查询函数持有 finder。

1. **直接标识。** 专辑多个标签发行 ID 立即返回 `mixed_release_tags`。唯一 ID 读取一次完整发行，存在则在本步核对，404 才继续。散放单曲用唯一标签录音 ID。查不到、源关闭和源暂不可用沿用各自的返回路径。
2. **合并发行搜索。** 取存在的子句，首尾加括号，以 OR 拼接。例如 `(barcode:743218940323 OR barcode:0743218940323) OR (catno:"74321894032" AND label:"Alfa Music") OR (release:"范特西" AND arid:a223958d-5c56-4b2c-a30a-87e357bc121b AND tracks:10)`。文字沿用 `field` 转义；条码含 CUE MCN，去重、沿用 UPC/EAN 等价形式；厂牌只修饰目录号子句。曲数仅限制文字子句。标题、署名均有可搜索值才加文字子句，合辑可用 VA 编号。没有子句就省略该请求。每次取 100，保留原始页。按返回的条码、同一 label-info 项的目录号/厂牌在本地重建每个候选命中的分支；Lucene 的分数和 OR 命中本身均不证明标识相符。优先核对有条码实证的一批，再是目录号；强标识命中但有反证即在该步停下。纯文字行留到标识路径走完，复用这页。
3. **ISRC。** 按现行 `sampleFour` 取最多四个曲位，同位的标签和 CUE ISRC 去重、大写、去空格连字符并校验。以 `isrc:A OR isrc:B …` 做一次录音搜索，取 100，按返回的 `isrcs` 精确分回每个曲位。同位取发行并集、不同位取交集；核对详情时仍要有同位 ISRC 佐证。先记录返回规模，再做现行的嵌套发行 25 条边界和去重。顶层总数超页、嵌套列表达到原上限记 `list_truncated`，不从不全的交集判定。录音页实测会含重复发行及上百个发行；不能先裁剪再反推完整。有效录音缺少分桶所需 ISRC/发行信息时，按父设计退回现有逐码 `RecordingsByISRC`，同码只查一次。503、超时进入退避，不通过改写查询规避限流。
4. **碟片和声纹。** 每碟按可重建的原 TOC 请求 `discid/<id>?toc=…&cdstubs=no&inc=recordings+artist-credits+labels+release-groups`。精确结果验证各碟曲数、曲位、曲目与录音投影后标为完整，跨碟取精确交集，直接进核对。模糊结果保留 `disc_fuzzy`；无共同发行只留候选。disc 端点不照抄发行读取的 inc。若本地有需核对的 ISRC 而此响应缺少相应录音的 ISRC，可按已有发行编号读取补齐。声纹沿用现行采样、分数与稀疏/交集规则，放在强标识之后。已有文字页在声纹之后才作最终文字判定；这样既保留强证据优先，也避免重复同一搜索。
5. **文字来源。** 增加只读查询输入 `CanonicalNames(releaseID, trackID?) → {title?, artist?, artist_mbid?}`，D 提供巡检结果，缺省无值。使用规范写法，保留原标签作为不同写法的第二次尝试；规范名只改变检索，不能把推断值升级为真实。已有可信艺人对应/唯一核实的主身份用 `arid:`；否则先用 `artist:"署名"`，避免每张专辑先查艺人。实测发行 `artist:"Jay Chou"` 有两个英语署名结果，`arid:` 有 17 个结果。故只有近似结果、无本地同名同结构项也要允许按已解析的艺人 ID 再找，不能以 HTTP 非空为成功。艺人名解析按名字共享；解析出多个同名身份不任选其一。原值与规范值相同时只发一次。简繁实测的五个发行逐一相同，因此常规查询用原有写法、本地用现有繁简规范化；模拟服务的 chinese 样本须模拟此特性。D 给出的另一种拼写仍可用。
6. **完整性。** 保存 `count, offset, limit, returned`（returned 是响应原始行数），先核对 offset=0、ID 有效、ID 集合完整，再筛选。仅 count 已知且与实际覆盖的不同 ID 数相等才证明全收齐，count=100 且收齐 100 条可以使用。缺失、重复、截断都不能据此认定唯一。失败响应不作为空页。分别记录每次检索的用途、查询字段、是否放宽及本页元数据；不跨不同检索简单相加 count。多次用于作证的文字页，按 ID 并集并保留各页完整性。真实实测已证明三路并集及录音合并，故采用合并写法。父设计的发行备选是“标识一搜、文字一搜”，只在合并语法确实不可用时启用，不因服务繁忙自动切换。

## 二、专辑四级判定

本地事实取 `LoadCurationEvidence` 的在库曲目、扫描与人工修正。文字键用非空的 `textnorm.Loose(textnorm.Text(s))`；比较署名时也接受已核实的同位主名/登记别名或可信艺人编号，不能把任意搜到的艺人当同一个人。标题明确区分发行标题和发行组标题。合辑识别用 `metadata.IsCompilationPlaceholder`、扫描的 compilation 来源，或缺少有效专辑艺人且至少两首、各首有效主署名互不相同；以 `arid:89ad4ac3-39f7-470e-963a-56509c546377` 代替占位艺人。古典用任一在库轨有作品字段，或流派命中现行 `ingest/works.go` 的古典判别；投影作品存在位，共用该词表。

| 优先级 | 可以定案的精确条件 | 结果 |
| --- | --- | --- |
| 1 · 标识 | 下表的方法条件成立，至少一对曲位且没有相反证据；强标识要认定到版本须恰好一个通过；同一步多个通过时，以现成详情独立核对第 3 级，同组且确有结构佐证才可降到组级，否则仍为 ambiguous。 | 版本级，配对录音按原规则写入。 |
| 2 · 发行组唯一 | 同时满足五项：① 本地标题、采用的艺人均为 real，检索未放宽；② 用于判定的第一页完整；③ 既非合辑也非古典；④ 本地超过 4 首；⑤ 本地总曲数、按碟顺序的曲数向量、标题与艺人规范化后均相同的剩余集合非空，且每个候选有同一非空组 ID。核查摘要中可见的反证、已有对应、抑制；有无法从摘要核对的强标识时先查结构。 | 发行组级。即使只剩一个摘要也不声称是该版本。通常一个搜索即止。 |
| 3 · 结构吻合 | 本地先按已知曲数和碟结构排除不可能者；缺项保留为待核实。文字页必须足以排除未见候选。至多 3 个待比较版本才逐个核对完整曲目，使用实际配对、同碟曲数相等、总曲数相等、时长通过且无硬冲突。曲数 ≤4 时另要求可用标题与艺人均等；合辑短盘用真实标题及 VA 的身份核对。推断查询、放宽查询只可走带时长的判定。 | 恰好一个通过为版本级；多个通过且同一非空组为发行组级；跨组或组未知为待定。 |
| 4 · 定不下来 | 有可用候选，证据缺失、相互矛盾、多个组通过、或剩余候选超读取上限。 | 保留候选、证据和逐项原因，交识别第一档。零命中和死路项另见第五节。 |

组唯一成立但有**非真实曲目标题**时继续尝试第 3 级；厂牌、目录号、条码或年份缺失均不触发额外读取。能独定一版才按版本补空。同组多个通过的，按已知发行日期从早到晚、同日期按 MBID 字典序选一个（缺日期排末，全部缺日期按 ID）；只用该版的对位曲名补可写的曲名缺口。没有通过的版本但第 2 级五项仍成立且没有新发现的组身份冲突，可保留已证实的组对应，缺曲名留空。若本地已有版本对应，须与该版本及其组相容，不能借降级绕过冲突。

| 方法 | 原有必要佐证 |
| --- | --- |
| tag_id | 唯一发行标签，位置对应，曲数相等或时长通过。曲号确定可按精确碟位/曲位匹配子集。另加下述文字反证。 |
| barcode | 曲数相等，且时长、标题、艺人至少一项吻合。 |
| catalog_number | 曲数相等，且厂牌或时长吻合。 |
| isrc | 曲数相等且至少一对同位 ISRC 一致。 |
| disc_id | 所有碟的精确交集，曲数相等，标题或艺人吻合。 |
| acoustid | 曲数、时长吻合，至少一对采样录音 ID 实证吻合；保留稀疏、分散和相反 ID 的限制。 |

时长可比指本地整秒与远端毫秒都大于零。阈值含等号，差值 ≤ max(3 秒, 远端时长 × 1%)，至少 ⌈本地配对数/2⌉ 对可比，且每个可比对都在容差内。曲号为推断时要求全数可比且通过。时长越界一对已有反证。若本次实际采用的条码、目录号/厂牌或文字来自 inferred，方法原条件之外还须满足曲数与时长结构；不借同一请求中其它真实子句给推断依据增信。原有标识方法对单碟、子集、推断曲位的细则沿用 `compareTracks`；不能用搜索摘要重写它。搜索摘要缺失的字段是未知值。例如没有 discids 时不能仅凭其缺失产生 `disc_tag_mismatch`。

**标签编号的文字反证。** 两个真实、可比的专辑字段（本地标题与艺人）都和该发行不等时，取已按位置配好的曲目中本地标题真实、远端有曲名的 K 对；每对用该版曲名，缺时用该录音标题。令 H 为规范化后相等的对数。本稿取 **K ≥ 1 且 H = 0** 时记新代码 `tag_text_mismatch`，强制待定，不沿用时长或曲序自动写任何对应/字段；两组 wrong-id 样本均应落在这里。K=0 是没有曲名佐证；若已有标题与艺人双重明确相反，也停为待定（`title_artist_mismatch`）。任一专辑字段确实相等或 H≥1，只撤销本条否决，其它方法条件、相反证据照常核对。占位、文件名推断和空远端名不作相反文字，保留贫标签、部分专辑原有的编号核对。已登记的艺人别名应先对齐再统计。保存 K、H 和两个专辑字段的比较结果供复核。

**冲突口径。** 继续使用 `mixed_release_tags, barcode_mismatch, catalog_mismatch, title_artist_mismatch, release_tag_mismatch, group_tag_mismatch, artist_tag_mismatch, disc_tag_mismatch, recording_tag_mismatch, track_tag_mismatch, isrc_mismatch, media_count_mismatch, media_format_mismatch, pseudo_release, position_conflict, track_count, duration_mismatch, inferred_positions, list_truncated, too_many, isrc_spread, disc_fuzzy, acoustid_sparse, acoustid_spread, acoustid_mismatch, ambiguous, title_mismatch, suppressed, existing_link, insufficient_evidence`。新增 `tag_text_mismatch` 见上，`duration_missing` 指文字结构/单曲判定未达可比数量；`artist_mismatch` 指要求署名相等的文字判定仅署名不符。旧 `search_only` 可读，新搜索写具体原因。每个候选独立保留冲突；汇总沿用现行频次排序，死路判定逐候选检查，不能由汇总票数推导。日期、国家和分数不参与唯一性。先排除被抑制/被占用候选，不能拿被撤销的那一版改以组级重建；已确定的不同发行组也是反证。

**请求数。** 普通组级搜索用 1 次；版本级通常不超过 2 次。按父设计，先做结构筛选，同名跨组有 2–3 个可用候选时，最多 1 次搜索加 3 次详情，计 4 次；度量列出实际请求数。强标识未命中、别名解析、来源重试亦按实计数。第 28 条才是正确版的 truncated-01/02 各有 28 个同名同曲数版本、8 个组，属于程序不能独定的标注；保留全部 28 个给 E，不以年份挑一个定案，也不声称已用 3 次详情比较过 28 个版本。

## 三、结果、外部对应与消费边界

`MatchResult.Mode` 继续区分 album/single，`Status` 表示工作结论，`Method` 表示证据来源。增加 `Level = release | release_group | recording | artist`，另记 `rule = identifier | unique_group | structure | recording_text | common_credit`。对应的载荷各自独立：版本有完整 Release 与 Pairs；组有 GroupID、已确认署名，以及可选的“本地曲目 ID → 待补曲名/取值版本 ID”；录音有 Recording 与一个 Pair；仅艺人有共同的有序署名。组级与仅艺人的 Release、Recording、Pairs 均为空，写入端按 Level 分支。持久化沿用 `status=matched`，在 result 增加 level、rule；`external_id` 只承载已有类型的版本/录音 ID，组号放 `external_group_id`。

| 粒度 | external_links 与操作 | 可消费的证据 |
| --- | --- | --- |
| 版本 | 沿用 `(musicbrainz,release,本地发行ID,外部发行ID)`，后态的该项带 external_group_id；按实际配对写 track → recording；按原计划写组、艺人。 | 原发行补空、版本与组封面、归组、按发行及录音证据发现并入。 |
| 发行组 | 增加 `object_type=release_group_match`，object_id 为**这张本地发行**的 ID，external_id 为外部组 MBID。每张发行独立留下由 `external_link` 操作持有的行。同一事务内按 planGroup 需要另写既有 `(musicbrainz,release_group,本地组ID,外部组ID)`。 | 可作艺人对应、组封面与版本归组证据。只有通过结构判定的参考版可按上一节补曲名。禁止用组号作版本 MBID、补版本事实字段、写录音对应、建立曲目归属或发现发行并入。 |
| 录音 / 仅艺人 | 单曲唯一时按 track → recording 写入。多个录音仅署名相同时只写 artist → artist，并在结果保留署名证据。 | 单曲不申请发行对应、封面、归组及发行并入；仅艺人也不写曲目标题或录音编号。 |

单靠本地组的公共链接无法证明新加入的那一张也被识别过，尤其是在准备从另一组迁入时。新类型给这一张保留独立写入者、幂等键和撤销依据；`ApplyLinks` 只允许程序 MusicBrainz 来源使用它，要求 object_id 等于锁住的发行。它有自己的一行占用/抑制键，沿用 `(source,object_type,object_id)` 唯一约束；已有版本与新组的后态必须在锁内核实相容。相同位置的历史操作可以回放，撤销逐项核对 operation_id、删除并写 `external_link` 抑制，预览/描述能显示“发行组对应”。同批撤销归组和身份仍遵循既有逆序。

- **封面。** catalog 分开投影版本 ID、本发行查实的组 ID、本地组公共对应。前两者任一存在且有封面缺口才工作。有版本先查版本、再查非空组；只有组时直接用 `CoverTarget{Kind:"release_group", ID:组号}`。沿用已有图片、撤销、文件与摘要检查。本地组其它成员的公共对应以及已撤销操作的历史 result 不能单独授权取图。
- **归组。** `checkGroupEvidence` 在写事务锁住当前对应和目标本地组的对应。原有“本发行版本行的有效操作后态含该外部组”路径继续有效；新增“本发行的 release_group_match 行仍属于未撤销的 external_link，后态的对象类型、本发行 ID、外部组号逐项相同”。任一路径都要与目标组当前的 MusicBrainz 组号一致。`planGroup` 和成员一致性投影同时认两种证据，继续遵守单一可见目标、人工归组、抑制与并发占用。
- **写后与回填。** `afterMatch` 只承认本计划预期的对应、组变更和补空，保存写后摘要。按粒度设置 `track_fields/artist_links/release_merge` 的适用性；仅组时不调用 `ReleaseByID(组号)`。延期艺人对位从已存组署名恢复。发现发行并入的查询及执行器仍要求真实 release 行、原操作和录音对应；仅组或共同艺人的结果都不能触发。

## 四、散放单曲和艺人

`candidate_kind=loose_unknown` 使用唯一在库曲目。顺序为标签录音、ISRC、声纹、文字。前三级保留现行单曲必要条件：时长可比且通过，ISRC 时长筛选后恰好一个再核对曲名，声纹继续做正式录音复核和曲名反证。所有写入先核对现有录音链接和抑制。

文字搜索取 100 条保留 count。查询用可用曲名、署名或可信 arid，有时长才加 dur 区间；区间必须覆盖本地判定的全部容差。对本地 L 毫秒，远端 R 的 1% 容差可接受区间为 [L/1.01,L/0.99]，与 [L−3000,L+3000] 取并集的外界并向外取整，最终仍在本地精确核对。按录音 ID 去重、核实本地标题、同位艺人及已知时长。真实标题和艺人、完整页、恰一个通过即写录音；多个通过且所有录音的有序外部艺人 ID 向量完整并逐位相同，只对齐这些艺人。署名显示串相同而 MBID 不同是歧义。多个通过但共同署名无法有效对位，保留给第一档。推断文字允许检索且必须有时长、可用文字一致、恰一个通过；不走“共同艺人”。标题或艺人缺少可搜值时优先用能核实的标识/声纹；两项皆占位且无标识时进入信息不足分类；占位串不发给服务。

艺人对位延用非自动层的 `LoadCurationArtistCredits`，按发行艺人/专辑艺人、已配对曲目的曲艺人分别取有序署名。组级只用已确认的专辑署名；多个版的有序 MBID 不同时，仅保留一致且可归属的署名组，不能从随手选的版本推曲艺人。沿用人数与位置检查、同位且整组等长的显式艺人标签、公共身份过滤、单一本地身份出现多种 MBID 时不写冲突链接。先比较当前署名和 MB 主名；仍不同才按 MBID 读 `artist/<id>?inc=aliases+url-rels`，比较登记的非空别名。相同就写受治理的 `artist_credit` 对应，后续 `MergeArtists` 仍需两方各有有效操作支持同一 MBID 及原有拆分/撤销保护。V6 的程序责任到两边专辑对上并建立同一艺人身份，显示名和索引的巡检工作在 D。

`ArtistByID` 与 MusicBrainz `ArtistLinks` 共用同一条组合读取、同样顺序的 inc 和 URL 缓存键，两个入口各自投影结果；一次处理内按 MBID 去重。沿用后台正缓存 30 天、空结果/404 缓存 1 天；限流、坏响应不当“无别名”。艺人存在性使用无字段的带引号转义短语，如 `"Jay Chou"`；按规范化输入与实际查询写法复用同一检索缓存，异名记录中保留实际 MBID 及佐证。搜索结果不能只凭第一名/最高分写艺人对应。

**名字不同也不是登记别名。** 等人数、同位置但不能证实的每一对写为候选修改，已对齐的其它位置可单独处理。人数不同整组不产生提案；合辑/古典若能从署名角色证实演奏者与作曲家错位，跳过该对。投影本地 composer/performer 等已有角色作为比较事实；外部 artist-credit 仅指该位置的署名，不能仅凭“古典”、位置、名字不同或未知角色断定错位。候选保留未知角色给 E，公共 MBID/空 ID 沿用无身份写入规则。程序不因未登记的译名自动改名。

在新迁移建 `curation_artist_credit_proposals`，与识别结论同事务 upsert；主键按本地发行、轨或发行作用域、角色、位置、本地艺人、外部 MBID、识别摘要派生，去重可回放。行内保存匹配粒度、来源对象、名字和别名快照、依据的操作 ID（已有链接时取原持有人）、摘要、状态；新输入/撤销使旧佐证失效。模型侧按有效摘要和仍存在的来源操作取 `open` 行；采纳、拒绝与模型写入由 E 处理，已有拒绝不因无关缺口重提。该表独立于 release_match 待定项，已识别专辑出现译名分歧也能消费。单条输入示意，示例值是占位符：

```json
{"kind":"artist_credit_identity","proposal_id":"…","release_id":"…","identity_digest":"…",
 "local":{"artist_id":"…","role":"track_artist","track_id":"…","position":1,"name":"本地署名"},
 "external":{"provider":"musicbrainz","artist_id":"…","credit_name":"外部署名","primary_name":"主名","aliases":[]},
 "basis":{"level":"recording","external_id":"…","operation_id":"…","rule":"recording_text","role_evidence":[]}}
```

## 五、零候选、终止与重新检查

“零候选”指有效查询成功但没有发行/录音候选。有近似候选且有真实分歧是待定；把整页时长不符的项误当作零会丢掉第一档的证据。抑制、已有其它对应造成的全体不可采用单独处理。

1. 初查与规范/原值检索均未找到后，最多两次**有实际变化**的放宽：去掉末尾版本说明及限定性括号（如 Deluxe、Single、EP、Remastered），保留可用标题；再在同一写法上去掉 tracks 子句。单曲没有 tracks 子句，只用适用的放宽。保存每个不同查询；放宽后只认带时长的结构，短盘仍核对标题/艺人，不能用删去的曲数、版本文字证明唯一。
2. 仍零时按名称/别名查一次艺人。此前已解析的同一名字、有效对应或 VA 常量可直接复用。搜索返回的主名/登记别名需规范化一致；同名多位说明库里有这个名字，但不能替用户选一个 arid。若此时才通过登记别名唯一核实到 arid，且尚未按它检索，沿用该结果补一次原题精查，在得到候选时回到四级判定；仅查询条件有实质变化才补查。截断且无精确项只能记存在性未知，按可继续查的第二档处理；服务故障走稍后再试。
3. 未配置模型（沿用整理的模型设置）落 `unlisted`，不建待定项。暂时暂停、单日预算耗尽不能当未配置。
4. 艺人确定不在外部库，使用现有 `web_search` 开关和实际依赖就绪状态；可用才存联网路线，否则落 `unlisted`。不经第二档。
5. 艺人在库或存在性未决，建/更新待定项，记录 `route=lookup`；已试查询、本地缺口分类与艺人证据一并交 E。本地完整项排在有缺口者之后，B/E 按该分类取项。已有候选的普通待定项是 `route=select`，联网路线为 `route=web`。在 C 先行合入期间，后两档待消费的 lookup/web 使用专用 `waiting_route` 状态；现有模型只领 waiting_model，避免把零候选送去第一档。E 接入时按 route 领取；web 也可用同种发行待定记录，终止时由下面的接口结清。有效候选的 select 可继续由原消费者处理。

| 工作状态 | 谁写、何时再取 |
| --- | --- |
| matched | 携带第三节的粒度；相同识别依据保留结论。 |
| uncertain | 有可交给模型的候选或有效的 lookup/web 路由。继续保存原有搁置、认领规则。 |
| no_usable_candidates | 对本发行/录音逐项检查已有不同对应、撤销标记。候选全集可核实且每项都被这两类原因阻止，或本地已有对应足以整体阻止本次写入时，同事务关闭原待定项并保存阻止依据；零新增待定、零模型调用。混合候选只交仍可处理者；截断页不能推断未见项也全被撤销。以该目标的阻止链接/抑制签名变化为重开条件，普通时间到期和声纹可用均不重开。 |
| unlisted | 首次进入登记 `next_attempt_at = now + 90 days`。届时同一识别摘要**仅自动重查一次**：只运行程序检索/核对，已证实则正常写入，否则保留未收录及原因，清空到期日，记已用掉本次机会。仅检索与核对已有候选所需详情，跳过声纹计算、模型分流与联网；整个重查路径不建模型待定或候选修改。手动重查同样只运行程序。 |
| try_later | 503、429、超时等按现有退避，不增加识别失败/结论次数；保留未收录重查模式，成功取得有效响应后才消费机会。来源关闭等恢复，B 接手队列级退避。 |
| 信息不足 | 保留 A 的 `no_match + insufficient_evidence` 及声纹可用后的既有机会；本地证据变化重分类。 |

`Writer.FinishUnlisted(itemID, expectedIdentity, conversation, reason)` 是 E 的结清接口：校验认领和输入，在同一事务将无结果的第二档/联网项置 resolved、记工作状态与 90 天日期。第二档放弃但 web 可用先转 web。手动入口定为管理员 `POST /api/v1/admin/curation/releases/{id}/recheck`；只接受可见且未收录的发行，锁状态行，标记仅程序重查、due=now，返回 202，同一未消费请求重复调用合并。不可见/不存在 404，状态不符 409，权限沿用现行 admin。暂停期间只登记，恢复后取项。手动提前重查成功出结论即消费该摘要本轮机会；之后仍能手动再查。自动和手动共用已登记的请求，遇并发按摘要、代次和状态更新时间比较后写。查询遵从现行缓存 TTL；90 天已超过正/负缓存期限。C 只扩展既有 `needsWork` 与到期标记查询，不增工作循环。未收录首次时间、已重查时间及仅程序模式跨重启保留；新识别摘要作为新任务输入可重新走五步。

## 六、规则版本、候选快照与升级

`MatchRuleVersion: 101 → 102`。LocalRuleVersion=2、PatrolPromptVersion=1，资料/并入的 resourceRuleVersion=101；旧 rule_version、input_digest 与新匹配版本列继续双写。旧 matched 不因升级重查或覆盖旧操作，也不批量撤销先前的 tag_id。回归样本验证的是新判定。A 的整份指纹仍用于写前校验、模型预览和回放。

| 证据 | 101 实际保存 | 102 的来源与离线用途 |
| --- | --- | --- |
| 结果集 | 候选按上游顺序先裁 10，再按 ID 排；无原始 count/offset，也没有分支覆盖。 | 每一页保存查询和 count/offset/returned/limit、分支、是否放宽和完整性；100 条内先保留原始 ID 集合，再保存各行筛选理由。绝不拿 candidates 长度、未出现 list_truncated 或旧缓存 URL 来补 count。 |
| 发行 | 摘要已有 external_id、标题、署名显示串、组号、日期、国家、状态、条码、目录号与 track_count。旧 track_count 来自累加 media，默认 0 不能解作“确有零首”。 | 搜索总曲数取响应顶层 track-count，各碟取 media[].track-count；保留字段有无和顺序。两者冲突时不给结构证明。完整读取另外保存碟位、曲位、标题、长度、录音/曲目 ID、ISRC、discids、格式、厂牌与编号配对、完整署名和 `complete` 位；只保存实际读到的详情。 |
| 录音/艺人 | 旧录音候选只有 ID、标题、署名显示串等摘要，没有 length_ms、按位置的外部艺人 ID、可比时长、主名/别名佐证；发行摘要亦无这些署名细节。 | 存本次核对过的录音投影及有序署名、已读别名；按曲位保留 ISRC/声纹的候选来源；每一候选存冲突/可用性，不把未知当不匹配。 |
| 本地 | A 已在 evidence 中保存发行/曲目真实性、track_counts、曲位与整秒时长等。 | 离线重读当前可见目录证据，核实识别摘要、当前已有对应和抑制。以本地在库轨的数量/分碟计数对远端曲数，不能用本地曲数填缺失的远端字段。 |

候选 JSON 保留旧字段并加可选的结构化投影，快照版本、各页及别名依据随 evidence 的 `match_snapshot` 保存；`refreshMatchItem` 刷新本地证据时合并并保留该块。行/目录号/冲突顺序规范化；查询返回顺序若需展示，保存在页中，不作为“新候选”的判据。搁置项比较有效 ID 集合、相关证据和识别输入，不因相同候选换序或补上序列化版本号重开。

离线入口沿用 `prepareRelease → reviewPendingVersion`，把当前只返回 keep/requery 的 `reviewSavedCandidates` 改为可返回 MatchResult 的纯判定。读取快照后不调用任何外部 finder、别名读取或封面路径。快照足够即可按上述同一规则给出组级/版本/录音/仅艺人结果；死路结清；有候选而证据不足（特别是 101 的未知 count、碟形或录音时长）保留原候选和具体缺证给第一档，不为每个旧待定项立即补抓。第 28 项不在旧快照内就无法离线恢复它。若旧记录只有明确的截断前缀且该缺口需要重新检索，或有效候选数为零，按 A 的 requery_match_version=102 登记普通优先级一次补查，升级轮本身零出站；离线仍可用的待定项不反复排队。历史 `insufficient_evidence` 先本地重分类，不浪费检索。

在原事务锁定待定项和工作状态，认领中/待人工的项沿用 A 的 `waitForClaim`；已由人决议的不翻转。离线得出结论后走与在线识别相同的 unit.write、applyMatch、saveConclusion/afterMatch 和待定项收尾顺序；读候选的事务结束后复用在线函数。单纯刷新版本不增加尝试、代次或操作；真正产生新结论才推进一次。旧无候选 `no_match` 不能一律迁为未收录，先走五步；新终止状态写入时同时处理同目标的旧待定项。

识别专用摘要在 102 补入专辑的原始曲目标题及其来源、用于合辑的曲艺人/来源、古典判别的原始作品/流派证据、本发行的组级对应及相关抑制。101 的 `IdentityDigest` 对专辑未哈希曲目标题；不能只改常量，否则换一个正确曲名仍可能沿用 wrong-id 结论。升级轮先按 101 算法比较旧摘要，再建立 102 基线，防止所有旧行因哈希格式变化出站；旧无摘要仍走 A 的整份指纹基线逻辑。新取证投影采用单独的识别输入或 `json:"-"` 避免仅结构体扩字段就改变既有整份指纹；新规则写入额外校验 102 识别摘要。角色、艺人对应、候选修改还要在写入事务核对当前署名及佐证归属。仅封面、歌词、年份缺口变化仍不重查身份。

## 七、修改位置与验收差异

以下路径相对于仓库根目录，列的是审阅通过后的修改面。

| 位置 | 具体工作 |
| --- | --- |
| `backend/internal/sources/musicbrainz/{client,entity,finders,search_page,disc,artist_links}.go` | 增带原始页信息的发行/录音查询、字段转义和组合、完整碟片投影、统一的艺人读取。保留旧调用入口。 |
| `backend/internal/curation/{source_finders,search_pages,sources}.go`，`backend/internal/app/{app,curation_musicbrainz}.go` | 为程序能力加相同的检查/后台调用包装，复用同一个已注册 MB 实例；接入规范名及模型/联网可用性读取。 |
| `backend/internal/catalog/{curation_evidence,curation_artist_credits}.go`，`backend/internal/catalog/metadata/`，`backend/internal/catalog/ingest/works.go` | 取证增加作品、合辑/古典与历史署名角色、发行自身的组证据与抑制；提取现行古典流派判词供两端复用。 |
| `backend/internal/curation/program/{match,match_lookup,match_search,match_snapshot,match_album,match_single,fingerprint,artist_links}.go` | 分支来源、候选快照、层级与判定、规范名输入、艺人别名及候选修改。 |
| `backend/internal/curation/program/{enrich,enrich_media,track_fields,pass,release_state,release_route,release_digests,unit,worker}.go` | 各粒度的写入/取封面、写后摘要、离线复核与幂等恢复、未收录重查。 |
| `backend/internal/curation/{external_link,external_link_revert,join_group,suppressions}.go` | 对象范围、后态、锁内强证据、撤销/预览和抑制读取支持新对应。发行并入发现与执行器仅验证，不扩大接受类型。 |
| `backend/internal/curation/{work_states,pending_items,progress,unlisted,release_identity,artist_credit_proposals}.go`、`backend/internal/curation/httpapi/handlers.go` | 终止与到期条件、离线复核、路由/提案存取、E 的结清入口、管理员重查。进度保留旧计数字段，并分别提供未收录/无可用候选数，避免已处理项从统计消失。 |
| `backend/migrations/0087_curation_match_rules.sql` 及配套 down | 追加新迁移；扩大 external_links 对象种类、工作/待定状态约束，加入到期索引、路由所需字段与艺人提案表。只扩展有约束的对应/抑制字段；不回写历史操作 JSON。撤销已写入的结论走受治理操作；down 迁移按原 schema 撤销本次结构变更。 |

**预计需要调整的旧断言。** `TestAlbumCorroboration/search`、`TestSingleRules` 的“一律 search_only”被实际证据取代；`TestMatchCandidatesKeepFirstTenAndSortByID`、`TestLookupBounds` 中的 10 条保存边界要更新。`TestLookupProjections/artist`、`TestSearchQueries/artist` 及 `TestDiscTOC` 的 inc、查艺人语法或完整性预期要更新；旧 finder 的普通发行/录音搜索条数仍按原合同测，新的整理入口另验 100。精确 disc 查询的 mock 需带曲目。旧 `TestAlignCredits`、`TestPostgreSQLCurationArtistLinks` 中异名跳过须区分真别名、同位标签、候选修改、角色错位。`TestPostgreSQLCurationProgramRelink` 现按 suppressed 查 waiting_model 项，须改断言无可用候选且未领取；`TestPostgreSQLCurationProgramLibrary` 和 `TestPostgreSQLCurationProgramRealRescan` 的 search_only 或零候选待定断言须按输入与模型/网络/艺人状态分流。`TestPostgreSQLCurationAuthenticityFromScan` 对推断搜索一律待定的断言、`TestPostgreSQLCurationExternalRuleUpgradePending` 的离线结果应按其实际保存字段区分。相同输入不出站、旧 matched 操作归属、真实的时长与方法反证、搁置换序、领取与人工决议继续作为回归条件。

测试代理后续用受控服务验证 V1/V18 的粒度、V4 的分流、V6 双边署名、短页截断、组对应可取图/可归组/不能并入、90 天仅一次、离线零出站。两种模型模式沿用 A 的 92 样本，逐项核对 wrong-id、truncated、chinese、collection/compilation 和 stream-03/04/07/08/11/12/15。A 的 Oracle/First candidate 错连为 12/97、错并 0/0、错改 2/4，程序可定样本进模型均为 44；分别比较和记录各端点的真实请求数。V17 中模型仍可选错近似候选的复核归 E。本稿引用的请求数来自查询实验；102 的两种模型模式度量由测试代理执行。

# 程序整理技术设计

依据为同目录的 `prd.md`、父任务 `.trellis/tasks/11-26-curation-chain-review/prd.md` 的 A2、A4，以及 `docs/architecture/adr/0009-curation-chain-review.md` 第 4、5 节。下文的字段和表名是拟实施合同。

## 入口与查找（哪些不查、查找顺序）

目录证据 → 本地辅助已写入的可搜索值 → 识别准入与排队 → 当前线索的候选 → 核对 → 受治理写入或保存来源结论。程序查的是 MusicBrainz，AcoustID 提供录音与发行线索。一次查询失败由子任务 6 接手，只有成功取得的空结果才能推进搜索。

- `backend/internal/catalog/curation_classification.go` 提供按专辑、散曲分别取标题和艺人的准入判断。使用真实性和 `curation.LocalSearchEvidence` 对应的可搜值，认可标签和可辨认的目录名。合辑用 Various Artists 的 MBID。只把本轮能实际使用的编号算作线索，不能把宽泛的 `HasSearchID`、任意艺人编号或“有其他标识”当作可查专辑的依据。
- 名字缺一半且没有编号时，不发请求、不登记平台结论。双占位且没有编号时只在声纹可用时走声纹。散曲用曲名、曲目艺人，保留录音 MBID、ISRC 与声纹直查。子任务 8 补全后重算准入。播客和有声书的标记与排除由子任务 2 提供，此处消费排除结果。
- `backend/internal/curation/program/queue_projection.go` 仅把可查的发行投影为待识别，取项执行前复核；即使不查也保留歌词等独立队列所需的父投影。文件作废自动值、补已有对应等本地维护仍能消费。手动请求优先；普通自动任务中，本地齐全且无直接编号的排在缺口之后，包括刚入库的齐全专辑。沿用同档新入库在前、齐全档未对上艺人在前。只调整识别排序；共用的 `QueueRankSQL` 在 `backend/internal/curation/model_queue.go`，避免连带调整模型或来源的调度策略。

| 顺序 | 现有入口与改法 | 进入下一步的条件 |
| --- | --- | --- |
| 1 文件里的发行 MBID | `program/match_search.go` 直接读指定版本；多个不同发行编号记 `mixed_release_tags`。散曲的录音编号走 `singleIdentifiers`。 | 没有可查编号或按该编号确实查无记录 |
| 2 光盘目录 | `program/match_lookup.go` 的 `discCandidates` 在条码前，复用 `catalog.CurationEvidence.DiscTOCs` 和精确多碟交集；模糊命中、非空但互不相交的碟记录保留冲突证据。本轮仅使用现有 CUE 镜像 TOC。 | 没有 TOC 或全空 |
| 3 条码 | 只传 `ReleaseSearch.Barcodes`，保留条码规范化、占位过滤和分页证据。 | 无条码或零候选 |
| 4 目录号 | 独立传 `CatalogNumber` 与本地可用的 `Label`，两者均有时查询使用 AND，在同一条厂牌记录上核对；缺厂牌的目录号沿用目录号加结构佐证。 | 无目录号或零候选 |
| 5 ISRC | 用 `sampleISRCTracks/sampleFour` 对至多四首作合并查询，同首取并、不同首取交。查到录音却指向不相交的发行，是冲突。 | 无可查 ISRC 或全空 |
| 6 声纹 | `program/fingerprint.go` 用现有至多四首的抽样、AcoustID 的录音和关联发行。普通识别和到期重查使用同一个步骤；子任务 6 提供预查结果、不可用与暂时错误的区分。 | 无可用声纹能力或成功查无结果 |
| 7 名字 | 名字齐全且没有可用于编号查询的线索时运行。先用专辑名、艺人、曲数；空页才清理版本或格式字样（如“豪华版”、320k、FLAC），同一次放宽去掉曲数；再空才搜艺人，完整艺人页中只有一个符合名字或别名的身份时，用其 arid 查发行。最后一次以艺人编号为搜索条件，不再限定原文标题；曲数可限定，命中仍需结构核对。散曲用录音查询及已有时长范围。相同查询跳过。简单 finder 没有 arid 查询能力，只执行前两次文字搜索；仅分页 finder 执行第③步。 | 全部查无结果，记普通未匹配 |

`program/` 在本文表示 `backend/internal/curation/program/`。步骤 1–6 收到候选就完成本步判定，不以判定失败、候选被撤销或分页不完整作为放宽理由。使用了本地编号但该编号查空，可继续余下的编号与声纹步骤，到名字步骤前结束，落实“有编号的不再按名字补查”。声纹自身的空结果不排除无编号专辑的名字搜索。

去掉 `program/worker.go` 的 `CanonicalNames` 搜索装配、`program/release_route.go` 对巡检名的读取，以及 `match_search.go` 中为规范名、原名、带或不带艺人编号、非空但曲数不合再搜的分支。`program/match_lookup.go` 的简单 finder 与生产分页 finder 使用同一顺序。`backend/internal/sources/musicbrainz/search_page.go` 现有 `titleArtistTerms` 要求标题，需支持第七步末次仅按艺人编号限定的查询，并由 `backend/internal/app/curation_musicbrainz.go` 的适配器透传。保留查询页顺序、完整性、方法和候选详情给子任务 9。名字阶段的四次上限指全空时两次发行搜索、一次艺人搜索、一次按 arid 搜索；分别统计候选详情和写入期的补全请求。指定 MBID 已有详情或同组信息齐全的样本可一次结束。

## 候选判定（有冲突、同组认到组、部分吻合）

| 情形 | 判定与证据 |
| --- | --- |
| 得到候选 | `program/match_album.go`、`match_single.go` 保留单候选规则。名字搜发行要每碟曲数吻合、至少半数可比时长且每首在 3 秒或 1% 容差内；短专辑还核对专辑、艺人。编号的佐证按方法取，文件发行 MBID 仍允许有明确碟序的子集。 |
| 有冲突（S9） | 当前线索命中但没有通过者，或几个合名候选分属不同发行组，停止并记录 `conflict`。记录方法、候选 ID、组 ID 以及已有的 `track_count`、`duration_mismatch` 等原因；跨组用 `different_release_groups`。完整页先处理撤销/占用并排除有明确信息可排除的无关候选；页截断时保留 `list_truncated`，不得声称全部撤销或发行组唯一。配了模型时移交识别待定项，无模型时保留本地。 |
| 同组多版（A2.3） | `program/match_snapshot.go`、`match_lookup.go` 和 `match_album.go` 共用判定，在线查询与 `replayMatch` 使用相同证据。普通专辑的完整候选页中，至少两版均有相同非空组 ID，专辑名、艺人和本地合并键一致，曲数已知且一致，无已知反证或未排除的异组候选，直接 `matched/release_group`。不为证明时长逐版读取；可见的碟曲数、时长或编号反证仍有效。一个具体版本按现有单候选规则核对。 |
| 补推断曲名 | 按 `earlierEdition`（已知发行日期优先、同日按 MBID）选最早候选。需要且可以写入推断曲名时只读这版详情，按已有碟位、曲序和时长配对。只交回 `TrackTitles`、`RepresentativeID`，不产生该版或录音对应。若详情证实结构矛盾，记实际冲突。 |
| 逐版例外 | 合辑、古典、短专辑保留逐版核对，沿用代码与用例的四首及以下边界。去掉发行路径的 `len > 3 → too_many` 和妨碍例外逐版核对的三次详情上限；保留完整页界限和单曲判定。单版通过认版，同组多版通过认组，跨组则冲突。 |
| 部分吻合 | `program/match_partial.go` 在现有子集或超集判定前核对专辑、艺人两个非空合并键。子集仍须至少 max(4, 外部曲数的一半向上取整)、每首曲名时长一一配对；超集仍须完整前缀，仅末尾可多曲。只认唯一组，不建录音对应。不读取已知名称不符的部分候选详情。模型补读沿用同一筛选，仅在 `program/model_editions.go` 的 `checkModelPartialEditions` 调用处传本地证据，保留可能完整吻合的异组核对。 |

与子任务 3 共用 `backend/internal/catalog/textnorm.MergeKey(raw string) string`，由先合入者在该包提供，主代理合并时去重。对字符串做 Unicode 规范分解、大小写折叠、去变音标记、标点和空白，再规范组合；保留符号和括号内的正文。例：`Béla – LIVE` 与 `bela live` 同键，`Live (Deluxe)` 与 `Live` 异键。不做 OpenCC 转换、合作词重写、版本词删除，不修改显示值或现有 `Text/Loose` 搜索投影版本。合并键只用于决定中点名的比较。确定性拆艺人用原署名拼接比较。

## 结论与读接口（未匹配、重查）

`curation_work_states` 主键是工作主题和本地对象，一行驱动一次识别；程序路线、模型待定项与“某一来源查到了什么”分开维护。新增 `curation_release_match_results`，主键 `(release_id, source)`，发行外键指向 `releases.id`。本次写 `musicbrainz`，字段为 `status`（matched、unmatched、conflict）、`checked_at timestamptz`、`reason`（含 `candidates_revoked`）、`conflicts text[]`。保存最近一次该来源结论；各来源独立 upsert。没有查询的发行没有来源行。等待和查询错误写工作状态，由子任务 6 实现；旧的已查时间不能被排队或失败时间覆盖。

`program/pass.go` 的成功保存事务在重新取证后，同时写来源行、工作状态、相应待定项。沿用待定项 → 工作行锁序、generation 和输入摘要复核。肯定结果只在受治理写入成功后登记。已撤销/占用要在完整候选集上证明全部受阻，再记 `unmatched/candidates_revoked`，保留抑制标记及阻断原因。没配模型也写程序结论；配了模型的初次或周期重查仍未匹配、有冲突时，均交给子任务 9，工作状态和进度不因此变成“拿不准”。子任务 9 的成功采用应走同一来源保存入口，模型放弃只结束待定项，不改平台查询时间。

| 上一次结果或触发 | 本轮路径 |
| --- | --- |
| 普通未匹配 | `next_attempt_at = checked_at + 90 天`。每次仍查空都登记下一次，时间以本次成功查询为准。到期重查跑全部适用 S8（含声纹），程序仍定不了且已配置模型时，沿用初次识别判断入口。现有手动程序重查保留仅程序语义。成功可变 matched、conflict 或撤销类未匹配。 |
| 候选已撤销、有冲突、查询出错 | 不设 90 天计时。文件变化或识别规则升级才自动重查。撤销过的同一值继续受抑制。 |
| 未匹配的文件变化、规则升级 | 立即按新输入运行；与普通 90 天触发区分。已生效的本地辅助补全是一次显式的识别入口，由子任务 8 唤醒，避免当作物理文件变化。 |
| 周期重查被中断 | 周期触发沿用初次识别的路由，等待恢复后仍可交识别判断。去除 `release_route.go` 构造声纹步骤及 `match_search.go` 调用声纹步骤的两处 `ProgramOnly` 屏蔽。子任务 6 接续该轮工作，不改变它的重试、等待、断点和限流实现。 |

后端数据交接给主代理做界面。下表路径是 HTTP 路由。

| 接口 | 字段与兼容 |
| --- | --- |
| `GET /api/v1/admin/curation/progress` | `releases.matched/unmatched/conflict` 按发行计数。撤销类计入 unmatched 一次；不把无查询记录的“不查”算作未匹配。保留 `total/processed` 语义，进度计入已完成的跳过项时能封顶。`unlisted/no_usable_candidates/no_match/uncertain` 是旧界面的分类；同一发布切换 decoder 和展示，本次随发布将四个旧字段从新响应和 decoder 一起移除，新客户端只使用新计数。历史迁移保留原证据，降级时由旧版接口按恢复后的工作行重新计数。`try_later` 等由 6 对接。 |
| 拟增 `GET /api/v1/admin/curation/releases/{id}/match-results` | 沿用管理员鉴权、发行可见性。已存在发行返回 `{results:[{source,status,checked_at,reason,conflicts}]}`，按 source 排序，未查返回空数组，不可见的发行返回 404。UTC RFC 3339 时间，历史确无时间用 null；`source=musicbrainz` 对应标签 MusicBrainz，`reason=candidates_revoked` 提示“候选已撤销”，冲突码映射为“曲数不同”等。避免把逐张记录塞进轮询全库的进度响应。 |
| `POST /api/v1/admin/curation/releases/{id}/recheck` | 继续返回 202。将 `RecheckUnlisted` 改为普通未匹配的重查入口，面向人的错误文案使用“未匹配”；旧错误码 `not_unlisted` 可保持兼容。阻断项没有周期重查。 |
| 前端交接 | `frontend/src/features/curation/api/decoders.ts` 解码计数，`api/endpoints.ts`、`hooks/use-curation.ts` 接逐张数据；`components/curation-progress-section.tsx` 目前同时显示没对上、拿不准、外部库未收录、无可用候选。主代理改计数、平台标签和时间提示，在展示逐张结果时按需取该发行的来源记录；同步契约校验 `frontend/src/test/api-contract.test.ts`，以及 `frontend/src/test/curation-data.ts`、`frontend/src/features/curation/curation.test.ts`、`frontend/src/app/curation-settings.test.tsx`、`frontend/e2e/curation.spec.ts` 的旧字段。后端同步 `backend/internal/app/testdata/api-samples/curation/progress.json` 和新响应样本。 |

## 写入（A2.5）

1. **发行组首发年份。** `program/match.go` 的 `MatchResult` 增加组首发日期，组级判定从 `sources.Release.Group.FirstReleaseDate` 传入。`program/enrich.go` 的 `planMatch` 在组级仅对空年份取合法四位年，沿用自动层级 4、人工优先、抑制标记和原有字段写入口。若摘要未带日期且本地确实缺年，只按组 ID 补读组实体的 first-release-date，不展开版本列表；底层复用 MusicBrainz 实体客户端和来源闸门。无法取得年份则留空。最早候选版本的日期不能代替组首发日期。组封面、组对应、可证的专辑艺人对应沿用；厂牌、目录号、条码属于具体版本。代表版补曲名走 `program/track_fields.go`，组级不写录音对应。
2. **拼得出的署名。** `program/artist_links.go` 保留人数相同的原位置判定。在人数不同的角色上，取该发行或已配对曲目的完整本地署名文本，与 `credits(external)` 按 MusicBrainz 顺序拼出 `Name + JoinPhrase` 的全文比较。以本地实际显示文本（包括连接词）为准，只做已有字段的首尾清理；两者相等且每个位置有可对应的有效艺人编号才拆。例：拆开本地“Jay-Z & Kanye West”，显示仍为这串原文；没有对应连接词的中文别名保持原署名，留给 8。组级只用各版一致的专辑署名，曲目角色必须有版本/录音依据。
3. **让拆出的身份可重建、可撤销。** 当前 `catalog/internal/artistgraph/facts.go` 从字段文字重新解析，`refresh.go` 按字段层选有效署名；只往 `artist_effective_credits` 写行会在重扫后消失。新增目录拥有的明细 `release_match_credit_parts`，每个生效依据对应行 ID、局部角色（发行艺人、专辑艺人、曲目艺人）、本地对象 ID、位置一行，保存本地完整文字、对位的姓名、艺人 MBID、写入操作 ID。唯一键按对象、角色、位置；发行／曲目用对应外键，记录具体的发行、发行组或录音依据行。由公开的 catalog 方法在已有事务内创建有序名字事实并解析本地艺人 ID，沿用现有外部艺人对应、去重、同 MBID 合并和治理检查。已有同值艺人对应也可作为支撑。原字段和 scan 层保留，人工修正优先；在同一角色上有有效拆分依据时从明细重建 auto 位置。事务记录来源、批次、幂等键、原依据。撤销该操作/依据或扫描使本地全文失效时撤去该拆分、重算本地署名及仅靠它支撑的艺人对应；共用的独立对应继续有效。`program` 通过 Writer 使用此合同，不直接导入 artistgraph。
4. **同一外部版的两张本地专辑。** `curation/release_merge_discovery.go` 在现有 `ObjectsLinkedTo`、目标顺序、活动对应和可见性检查之后先做三分。比较依据是本次匹配已经读出的完整 MB 版的有序「碟、轨位、录音 ID」与两张的曲目对应。保存对应的 `ExternalSummary` 目前只有总碟/曲数，不足以证明完整覆盖。发现时传入同一份详情，存量回填从缓存或按已对上的版 ID 读一次；网络请求在事务外。
   - 有相同已对应录音或重复占据同一外部轨位时，两份保留，走 `planGroup/JoinGroup` 的已有同版本组路径。尊重人工归组和撤销抑制。此分支必须在现有 `ReleaseMergeEvidence.check` 排除 overlap 之前识别。
   - 若两张各自对应不同的整碟，无重复，合并预览的碟序、每碟曲序和录音 ID 对应目标版全部碟轨，确定直接并入。不凭“总数刚好相加”决定。沿用现有目标顺序和 `governance.CurationReleaseMergeExecutor`（媒体归属、收藏、可见性、撤销）。给 `curation/release_merge.go` 增加程序来源的窄入口；模型专用的 `MergeReleases` 尚要求模型会话和待定项。程序在目录发现事务结束后以来源批次、双方活动对应及预览令牌执行；执行事务再核对双方对应、布局、人工分组和抑制，避免在已持目录锁时另开事务等自己。幂等重放和下一对的发现复用现有顺序。
   - 其余证据沿用模型并入的既有准入，有模型才建项；无模型不改本地。旧待定项若被程序解决要结项，避免下一轮模型重复并入。程序只自动处理本次能证明的一对。

## 规则升级、迁移及回滚

**版本与输入。** `program/worker.go` 的 `MatchRuleVersion` 从 104 升到 **105** 一次。本次默认不复查已匹配。物理文件变化是独立的工作状态基线；`ReleaseDigest` 和 `GapDigest` 仍分别承担当前证据防陈旧、本地补全职责。不能把 `LocalSearchEvidence`、归组算法、人工/自动字段、艺人对应、抑制标记或匹配规则版本混入物理文件摘要。

- 以已持久化的文件事实组成摘要：扫描的成员签名含音频和 CUE 文本的根、相对路径和大小；再纳入去重、排序后的实际音频路径及微秒修改时间。多轨共用同一父音频只计一次。CUE 文本同尺寸改动检测不到，列作已知局限。扫描重读只改变解析结果时沿用文件基线。
- 新增 `curation_work_states.file_digest` 保存最近结论所用的文件摘要。`program/release_state.go`、`release_digests.go`、`queue_projection.go`、`pass.go` 对识别排队使用相同的文件/规则比较；写入前仍以当前完整证据核对。清理 `curation/release_identity.go` 的 101/102 比较入口和按旧冲突码列举的升级规则；模型项的现行证据校验单独保留，不能把“文件未变”用作旧模型写入的授权。`program/unit.go` 的 `unit.write` 首次写前，对发行主题且有原取证的对象，在已加载的目录证据上比较文件摘要，不同时返回既有的 errReleaseChanged。来源重查结论只在同一代仍有效时提交，过期项用已有 superseded/领取检查收口。
- 默认版本升级只让未匹配（含候选已撤销）、有冲突和子任务 6 的查询出错走 S8，利用现有 `requery_match_version` 保证只消费一次。旧 `too_many` 随未定结果排回程序。已匹配升投影版本，不搜名字、不重新判版；已有对应的独立缺口按原补建流程处理。将来修正可能认错的问题时，在该次规则变更上声明“自哪个版本起需复核已匹配”，仅对跨过该版本、持有具体版 ID 的对象走 `ReleaseByID(已对应ID)` 核对，可复用有效详情；失败记实际矛盾，不改用 S8 搜索。正常文件变化下已持有具体版的也优先复核该版。未来明确升级复核时，录音级只读原录音，组级和艺人级保留原结论，适用级别由该次升级声明。

**迁移合同。** 在 `backend/migrations/` 增一对 `NNNN_curation_program_match.sql` 和 `NNNN_curation_program_match.down.sql`，基线最后为 0097，合入时与其它子任务分配连续序号。迁移包含来源结论表、拆署名明细、文件基线列和工作状态约束/到期索引。工作行的新结论值为 matched、unmatched、conflict；不准入且已处理的发行用已有 done，与其他工作主题同名但不产生 MusicBrainz 结论。到期谓词取普通 unmatched 的非空 next_attempt_at。本任务的迁移只新增 unmatched、conflict；主代理合入时合并子任务 6 的 query_error。旧迁移不改。保留现有模型、批次、操作历史，升级只映射 release 主题的识别状态。

| 旧数据 | 升级处理 |
| --- | --- |
| unlisted、no_usable_candidates | 分别映射到 unmatched、unmatched + candidates_revoked；后一类清掉到期时间。前一类以已有 rechecked_at、unlisted_since 的最近查询证据恢复 90 天；缺时间的不使用迁移时刻冒充查询时间。 |
| uncertain、no_match、matched | 有候选反证或旧 too_many 的按待复核冲突迁移；有查空依据的为 unmatched；insufficient_evidence 且未查询的不建来源行，投影后按准入处理。matched 从实际对应确定来源。没有可靠历史查询时间时为 null。旧未定且缺足够证据的登记一次 105 程序复核，避免把历史模型待定误报为已查空。 |
| try_later、模型处理中等 | 保留等待、领取和批次语义；6 维护错误状态。旧会话只能按原代次与证据结项。对既有结果从升级前已持久的扫描事实建立首个文件基线，在重新扫描前执行，避免空摘要让所有 matched 重查。 |

新代码通过同一 Writer 处理手动重查、程序轮及模型结项（旧 `unlisted.go` 仍有后者的写点）。核对 `work_states.go` 的到期读取、保存时并发手动请求保护、`program/queue_query.go` 的到期谓词和部分索引，以及 `patrol_submit.go` 旧状态谓词在子任务 9 清理前的兼容。旧审计记录里的术语保留。读写上线与前端改名同一发布。

**回滚。** 停用新识别工作者后用旧版的迁移执行器执行配套 down。先将 release 的 unmatched（按 reason 分回 unlisted/no_usable_candidates）、conflict（uncertain）与到期时间恢复为旧版可消费的形态；下次不应把 105 的请求标记当作 104 的待补查。清除拆署名明细及仅靠该证据的派生对应，用当前本地文字重建受影响艺人投影，再恢复旧约束/索引、删除新增表列。down 会失去逐来源结论、拆分依据、精确查询时间。已执行并入或自动补年属于受治理数据，管理员可按操作或批次撤销；需要连同状态精确恢复到升级前时用迁移前备份。不得直接删除并入记录或清空现有对应。

## 规范与修改位置

| 改动边界 | 实施文件（相同目录下简写） |
| --- | --- |
| 目录证据及拆署名 | `backend/internal/catalog/{curation_classification.go,curation_evidence.go,curation_artist_credits.go}`、`internal/artistgraph/{facts.go,refresh.go}`（后两项位于 catalog 下）、`backend/internal/catalog/textnorm/textnorm.go` |
| 查找、判定及写入 | `backend/internal/curation/program/{match.go,match_search.go,match_lookup.go,match_album.go,match_snapshot.go,match_partial.go,model_editions.go,match_single.go,fingerprint.go,enrich.go,track_fields.go,artist_links.go,release_route.go}`；`backend/internal/sources/musicbrainz/{search_page.go,release_group.go}`、`backend/internal/curation/{search_pages.go,source_finders.go}`、`backend/internal/app/curation_musicbrainz.go` |
| 持久化、调度、治理 | 上述迁移；`backend/internal/curation/{work_states.go,unlisted.go,progress.go,release_identity.go,external_link.go,external_link_revert.go,field_artist_links.go,release_merge.go,release_merge_discovery.go,release_merge_evidence.go,model_queue.go,patrol_submit.go,httpapi/handlers.go}`；`program/{worker.go,queue_projection.go,queue_query.go,release_state.go,release_digests.go,pass.go,unit.go,model_matcher.go}`。新增来源结论读写集中在 `backend/internal/curation/match_results.go`。模型文件只接统一保存入口。 |
| 整理规范 | `.trellis/spec/backend/curation-contracts.md` 第 15、16、18–20、22、23、25、27.2、28.2/3/6 节中受影响的规则与接口；`sources-contracts.md` 的查询输入；`artist-contracts.md` 的署名事实；`release-merge-contracts.md` 的证据和写入来源；`database-guidelines.md` 的新增迁移。局部替换旧规则，交 11 汇总。 |
| 并行交接 | 3 共用合并键；6 在 pass 的错误分支、来源等待与 AcoustID 队列实现，本任务只提供触发原因、成功结论与版本条件；8 交本地名字，9 接候选、冲突与来源结论，主代理接前端及固定样本基线。 |

## 已知局限

CUE 文本只保存路径和大小，等长修改无法触发识别重查。光盘目录步骤采用现有 CUE 镜像取证，抓轨日志 TOC 不在本轮范围。

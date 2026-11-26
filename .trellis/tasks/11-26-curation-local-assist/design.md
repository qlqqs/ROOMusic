# 本地辅助技术设计

依据父任务 B1、A1.5、A2.1、A2.5、A4 和 ADR 0009 第 6.1、6.2 节。扫描提供本地事实，模型解释这些事实，后台核对答案。发行识别继续由程序执行。本文的接口、表名和版本 4 为实施设计。

## 1. 哪些情况进入本地辅助

从已稳定且可整理的发行开始，按扫描后的实际结果判定，播客、有声书沿用 `curation_eligible_releases` 的排除规则。同一目录同时满足几条时合成一次输入。模型可明确回答定不了。

| 本地情况 | 触发依据和可交回的内容 |
| --- | --- |
| 只有文件名，没有名字标签 | 曲目标题来自文件名、艺人缺失或为展示占位。将文件名原文和目录名交给模型，允许填曲名、艺人及相应发行字段。去曲号、固定格式目录名等已确定部分沿用扫描值。 |
| 旧编码有多种读法 | 采用子任务 2 在扫描候选内求交得到的 `tracks.scan_tags.legacy_encodings`。至少两种时按整张发行请求一次编码。唯一解已由扫描处理。显式空数组表示无解，保留乱码且不作搜索文字。 |
| 艺人名含 & 或逗号 | 对尚未按明确合作词拆开的原署名逐个标位置，提供原连接词及空白。可拆，也可保留一人。扫描已确定的合作词、紧贴的斜线沿用 A1.2。 |
| 同目录多专辑名且曲号不连续 | 取子任务 3 经 `continuousEvidence` 和格式桶检验后实际按名分开的成员。只有多种原专辑名合并键且连续性失败的桶请求归组。连续混放、完整多格式版本各保留原判断。 |
| 认不出的碟目录名 | 已知 CD、Disc、Bonus 等先由扫描归组。普通兄弟目录在同一父目录下仍分开时，疑似同套多碟的范围交模型看全体文件。候选父目录的界线见末节。 |
| 缺碟号或曲号 | 读真实和已采信的编号观察。曲号为 0 表示缺失，同一介质曲号重新开始且碟号未定时提供整组。自然排序位、默认容器 1 都不能证明文件写了号。正常单碟缺碟标签的界线见末节。 |
| 专辑名混有艺人名 | 扫描标题或目录组成的标题含可分出的艺人部分，如标签“堤灯社 - 微雨塘”。真实复合标签也可请求提取，允许返回标题“微雨塘”。其余真实标签不因本项成为可改字段。 |
| A2.1 名字只有一半 | 没有可用编号，只有专辑名或歌名、缺艺人时请求从本地补全。合辑按 A2.1 已有艺人处理。缺的部分仍找不到时用原有可用线索，名字单独不触发文字查询。 |
| A2.5 署名拼不回来 | 程序已核对发行、依 MB 的人名和连接词仍拼不出本地署名时，在结算该匹配的事务中投影仅含本地署名目标的新请求。只拆本地文字。B1 不决定译名、艺人身份或外部编号。已完成的发行识别独立结案。 |

平票以 `release_field_decisions` 的 `uncertain_apply/tied_candidates`、空选值和各轨原文传递。单独的年份平票不构成新增入口，也不授权写年份。

## 2. 一个目录的输入、领取和放行

1. 保留 `projectReleasePage → ProjectLocalItems`。按页找候选目录，再按根 ID、根内相对目录一次读全目录和所含发行，不用本页的发行数作为成员上限。已识别的多碟目录沿用扫描逻辑父目录。未知碟目录须先确定同一候选父范围。根下直属音频按既有来源范围，避免递归取整个曲库。S4 从同一目录的原专辑名合并键及归组结果识别：多名且该桶落为 `same_dir_split` 时进入，已通过连续性检验的普通或 `format_split` 桶沿用其结果。这样读者无需重做四套连续性试验。扫描原文来自逐字段观察、原标签署名、`scan_tags`，需要文件名/CUE 等原始位置时使用来源身份和解析缓存。只读取本次范围。
2. 建议每个完整逻辑目录建一项 `kind=local_cleanup`。保留发行型待办，用成员发行中稳定排序的首个 ID 作展示目标。`evidence.release_ids` 是实际归属和程序等待的全体。目录键由根和范围派生，幂等 ID 含目录键、规则版本和本代本地输入。投影、收旧项按完整目录执行。跨页不能用当前页漏掉的兄弟发行结旧项。模型目标互斥从全部成员展开，建项、领取和写入前核实全部成员仍稳定。每项独立结算，旧同目录多项按升级处理。
3. `evidence` 提供 `directory_key,release_ids,reasons,releases,directories`。发行列出当前归组、请求的字段/角色、冲突、可选编码，目录列出相对路径和全部成员。每个可拆的原署名另列 `source_ref,role,raw` 及标签出现序，每个可写目标列 `target_id,fields`。每个成员含 `file_id`（根内来源身份的稳定引用）、`track_id,release_id,path`、本地标签及其来源、重复署名的出现顺序、`comments:[{key,value}]`、各候选编码与逐字段读法、碟轨来源、时长。CUE 保留父文件和虚轨边界，同一物理文件只列一次，可在其下列原有虚轨。用 `track_observations.legacy_bytes_*` 和 `ScanTags.LegacyArtistParts` 的原字节在服务端求读法。发送已解析的本地值，保留标签、注释全文及根内路径。排除人工或外部自动值作为原文证据。投影为本地专用结构，避免将来源查询史、规范名、外部候选或内部提交计划塞进消息。
4. 复用子任务 7 的 `localQueue`、`PeekModelKind → prepareBatch → ClaimPreparedKind` 和逐项收尾。当前运行上下文为 `PatrolAndLocal`，本项实际只暴露 `submit_local_cleanup`。在 `prepareItem` 为该种类直接准备本地证据，跳过 `AnnotatePendingEvidence/AddCanonicalNames`。提示词把输入作为不可信资料。共用开启者的主模型、现有额度、暂停和重试。估算与实发使用同一完整目录、提示词、schema 与预留输出。超限用已有 `content_too_long`，既不截本地资料也不跨目录拼批。服务端只向有效模型发请求，B1 没有外部查询工具或原生联网能力。
5. 在 `program.Options` 注入只读的有效模型配置判定，由 app 接同一开启者、模型覆盖及 `Availability`，先完成装配再启动 worker。程序领取 SQL 的 `dependencies_ready` 同时检查本发行所属的活动 B1 项。发起首次 MB 查询前对当前输入复核。没配模型时跳过此依赖，七类维持扫描结果，程序用其余合法依据照常运行。已配置但正限额、冷却或重试时，相关发行等待，其他发行、封面、歌词依旧走现有队列。配置移除后解除等待。已结案、明确不能判断或终止搁置后放行，按当前状态重新读取 `LocalSearchEvidence`。成功提交归组时等下一次目标目录归组可见后再查。模型结案和有效配置变化触发重新盘点成员，新可用名字可重开原来缺条件的查询。身份已匹配的不重搜。排队计数和出队共用依赖条件，等候不消耗程序尝试，也不占着识别执行位。
6. 以稳定排序的文件事实、请求目标、A2.5 的本地原署名和保护状态构造 `input_digest`。与子任务 7 配合，`identity_digest` 标识本地文件代次，用于拒绝/空答的重开。自身的推断写入、结构物化、请求时间和项状态不算文件变化。提交时再核对当前成员、人工状态和领取摘要。过期交已有 `item_stale`。本地知识以外的拼写、年份、艺人、曲目表丢弃。识别仍无结论时由子任务 9 接程序结论，不转送被拒答案。

## 3. 回包与写前核对

沿用后台工具和管理员范围，在同一 `LocalInput` 中用 `item_id` 和 `decision` 区分 `apply` 与 `undetermined`。后者带非空 `reason` 并调用 `FinishUndetermined`。前者至少有一类提交，未知字段由工具 schema 拒绝。下表数组允许一次提交多个独立目标，必须属于本项实际请求的范围。文件、角色、字段和候选引用均由服务端分配。

| 回包字段 | 后台核对和产出 |
| --- | --- |
| `texts:[{target_id,field,value}]` | 发行只准 title、album_artist，曲目只准 title、artist。沿用对应层的字段长度、规范化与保护判断。对值及来源的单一连续片段先用 `textnorm.Text`，再把 Unicode 标点映为空格并折叠空白。允许从曲名前后去编号与扩展名。例如从 `01_the.dark_side.flac` 取 “The Dark Side”。按单一片段规范键的连续包含关系核对，目标规范键不能为空。不能拼接两段、添加新字、音译或补错字。曲名有本轨文件名/标签及所属目录可核对，发行值取本发行的本地材料。乱码字段只能走编码分支，不允许借文字分支混用读法。 |
| `encodings:[{release_id,encoding}]` | 一张只准一个编码名，须精确属于该张的候选交集。逐个原字节调用 `metadata.LegacyReadings` 取该编码的值。多段署名按 `LegacyArtistParts` 的次序保留 Unicode 段并解其余字节，复用扫描的拼接/曲目艺人处理。全部可以解才接受整张选择，按原扫描字段归属分发到待整理发行和曲目字段。模型无逐格填译文入口。 |
| `splits:[{target_id,role,source_ref,parts,joins}]` | `source_ref` 精确指向本项列出的一个原署名（含标签出现序）。两段以上，连接词数等于段数减一。各段非空，按原顺序与原连接符交织，按 UTF-8 原文逐字节相等，空格和标点也要相等。例如“棱镜 & 林, 海”允许分为“棱镜”和“林, 海”，连接为“ & ”。保留一个人的结论无需写操作。保留显示原文，投影有序艺人段，不将“林, 海”再次按逗号拆，也不由模型建 MB 对应。 |
| `grouping:{albums:[{files:[{file_id,disc,track}]}]}` | albums 的每个非空数组元素代表一张专辑，序号仅在本答复内有效。文件集合须恰等于本次可归组成员集，正整数碟号、曲号，目录内一个文件一次，同一专辑同碟曲号唯一，跨碟可重号。保留确定的曲位和 CUE 原有拆轨，模型只补扫描定不了的结构。同一 CUE 父文件带多首虚轨时可在它唯一的文件行中交 `tracks:[{track_id,disc,track}]`，替代单曲文件的 disc、track。内部虚轨须与本地清单一一对应，CUE 已给出的顺序和边界继续有效。逐组输出稳定成员键。未知 ID、缺项、重复、矛盾均使整个归组无效，不保存残缺记录。 |

同一字段给出相互冲突的值时丢弃该字段。无效拆分丢弃该署名，无效编码丢弃整张的选择。独立的有效结果沿用现有逐字段采纳规则。只有非法结果或只有 item_id 的空答交子任务 7 的无有效回答处理。可信的明确保留决定以 `undetermined` 结案。数据库错误和失去领取分别走事务失败和过期路径。

保留 `SubmitLocal` 首次核对后固定 `local_submission` 的做法，存已解出的值、拆分、归组和写入者，每一笔治理操作使用从项 ID 和目标派生的幂等键。各写事务持目录写锁复核原文件依据，保留同一项已经成功写入的目标，重启沿用原计划。对每张编码决策预先核实全部字节，固定一个选择再派生各目标，不允许重试时前后使用两个编码。计划里的目标都处理后才调用一次 `FinishModelItem`，全程无审查。

## 4. 写入、结构记录与撤销

**文字与编码**沿用 `release_auto_values`、`track_auto_values`、`operations.Run` 和现有治理撤销。来源 `Kind=model`、`Provider=实际模型`、`origin.mode=local_cleanup`、`writer_level=LevelModelLocal(1)`，搜索来源为 `model_inferred`。用真实扫描值作 `scan_basis`。MB 核对成功后，`missingFields`、曲目计划与 writer 的 `OnlyEmpty` 须三处一致：在原有可补字段之外允许替换当前由 B1 持有的字段，事务再次核对 owner/级别。已有人工值或别的高等级自动值继续受保护。只采用本次匹配级别允许写的 MB 字段。覆盖后的操作归属由 MB 持有。

**拆分**沿用字段自动值持有者和相同的撤销执行器。为本轮产生的署名操作新增附属表 `local_credit_parts(operation_id, target_id, role, source_ref, raw, parts, joins)`，主键为操作、目标、角色、原文位置，operation_id 使用可延迟外键。字段层保存显示原文。同一目标的多个原署名分别存段，未拆的仍按原有规则投影。将拆分含义加入幂等指纹、前后态和撤销抑制比较。同样的显示串可以从一人变成两人。投影在写入同一事务中取当前自动值 owner 对应的段，逐段进入既有名字解析。人工层、原文件事实或更高层 MB 署名生效后不读旧 owner 的段。单条、批次、来源撤销还原原 owner 并重建有效署名。A2.5 将 B1 的结构作为推断输入，外部建对应仍走程序独立核对。

**模型归组**单独使用一种受治理操作 `local_model_grouping`，单次答案产生一条记录。新增 `local_model_groupings(root_id,directory_key,operation_id,source_digest,directories,assignments)`，主键为根和目录键，operation_id 唯一并延迟关联操作。assignments 保留服务端核对过的来源身份/相对路径、专辑成员集合、碟号和曲号。source_digest 覆盖原文件成员、大小、修改时间及 CUE 边界。记录及操作快照保存推断来源、模型、级别 1 和批次。快照存前后态和受影响的所有来源目录，缺前态表示撤销后普通归组。撤销恢复前一条或删除，校验现 owner。通过现有抑制表以 target_type=library_root、target_id=root_id、subject=local_model_grouping 及目录键和规范化分配的哈希存标记，防止重放已撤归组。派生的发行 ID 可能随合并改变，抑制查找须以稳定范围为准。操作列表沿用发行目标摘要，纳入现有单条、按批次和按来源撤销注册表。

提交只登记方案并登记相关目录待归组。复用目录确认扫描的持久化唤醒和局部扫描入口，使服务重启后也能处理。扫描先取这些目录和方案的同一快照，扩展到方案列出的全部兄弟目录，CUE/碟范围对齐后再对齐模型范围。身份规划和保存阶段共用同一套“扫描候选 + 有效分配”函数、同一份快照。沿用原归组的字段投票、封面、附件、曲目来源身份及发行严格过半复用。模型只改变成员、介质和待定曲位，写明 `model_inferred`，原文件号和解析缓存仍为本地证据。增量候选签名加入有效分配摘要。新分配或撤销即使文件未变，也必须重新规划并保存。完整发布后重新盘点实际成员发行。撤销同样登记局部归组，下一轮用恢复后的方案或普通扫描规则。文件变更导致 source_digest 不符时退回扫描候选。

投影可写目标、登记、消费三处都核对手动结构，覆盖涉及的源发行、并入目标及曾经手动分开的成员。复用 `release_merges`、`media.origin_release_id`、人工归组操作和撤销标记查来源，包含人工确认的操作。已由人工决定的成员保留所属关系。其他文件可以构成独立可归组集合，禁止以一个模型组间接带走受保护成员。消费时再次检查，扫描期间的新人工决定优先。已有 MB 核实的发行结构优先于模型后续改动。目标目录若跨出本次扫描覆盖，等完整覆盖后整份处理。

## 5. 文件、迁移、升级与降级

实施文件如下。每个单元格里的文件名相对于本行目录。

| 目录 | 文件与责任 |
| --- | --- |
| `backend/internal/catalog/` | `curation_evidence.go`、`curation_legacy.go`、`curation_artist_credits.go`，新增 `curation_local_evidence.go` 读取整目录扫描证据及可用本地推断。 |
| `backend/internal/catalog/metadata/`、`backend/internal/catalog/ingest/` | 前者 `legacy_encoding.go` 抽取给定候选的共用解码。后者 `legacy_encoding.go` 使用它，`staging.go`、`identity_plan.go`、`organizer.go` 及新增 `model_grouping.go` 接目录方案。 |
| `backend/internal/catalog/governance/`、`backend/internal/catalog/internal/artistgraph/` | 前者 `auto_values.go`、`track_auto_values.go`、`auto_revert.go`、`track_auto_revert.go` 处理署名快照、还原、抑制，新增 `local_grouping.go` 提供结构锁下的登记/还原。后者 `facts.go` 在已有字段自动层重建有序署名。 |
| `backend/internal/library/` | `scanner.go`、`scan_scope.go`、`curation_observations.go`、`candidate_signature.go` 处理两遍归组、覆盖、确认及增量签名。 |
| `backend/internal/curation/` | `local_items.go`、`local_submit.go`、`local_search.go`、`writer.go`、`model_targets.go` 和新增 `local_grouping.go`。负责投影、核对、持有者、优先级、结构操作类型。 |
| `backend/internal/curation/program/` | `worker.go`、`queue_projection.go`、`queue_query.go`、`pass.go`、`release_state.go`、`enrich.go`、`track_fields.go`、`artist_links.go`。仅接 B1 门槛、A2.5 交接及 B1 值被已核对的 MB 值替换。 |
| `backend/internal/curation/model/`、`backend/internal/curation/stewardtools/`、`backend/internal/app/` | `model/item.go` 的本地准备与消息、`stewardtools/patrol.go` 的本地工具、app 的 `curation_patrol_prompt.go`、`curation_model.go`、`app.go` 装配。与子任务 9 仅合并本地分支，6 的声纹和来源暂停、10 的艺人联网均由各自任务维护。 |
| `backend/migrations/` | 下一连续版本的 `NNNN_curation_local_assist.sql`、`NNNN_curation_local_assist.down.sql`。合入时按目标分支取号。 |
| 样本和文档 | `backend/internal/upstreammock/script.go`、`backend/internal/app/testdata/curation_regression/fixed-script.json`。旧指标驱动和已存在的 Go 调用点由实现代理在 `backend/internal/app/` 的 `curation_metrics_model_integration_test.go`、`curation_local_directory_integration_test.go`、`curation_patrol_integration_test.go`、`curation_live_corpus_integration_test.go` 换用新结构，保留原有断言意图。规范限 `.trellis/spec/backend/` 下 `curation-contracts.md` §24.1、27.3、28 相关条款，以及 `steward-runtime-contracts.md`、`library-scan-grouping.md`、`governance-contracts.md`、`database-guidelines.md` 中本次改动的接口与存储。 |

正向迁移创建上述两表、外键和操作类型约束，复用原待办、操作批次、治理快照及抑制表。归组操作登记/撤销时在同一事务标记受影响发行需局部确认，并保留待确认的目录集合直到成功覆盖。具体复用 `releases.curation_changed_at/curation_confirmed` 和 `claimConfirmations`，写操作将当时所有成员发行纳入确认。尚未消费便撤销时，成员仍属于这些发行。消费后再撤销时，按快照来源身份反查当前发行，标记当前持有者。确认查询由 `curation_scan_files` 展开成员目录，并并入操作快照中的来源目录。降级按依赖顺序移除新类型的撤销和操作、归组/拆分专属抑制及两表，恢复原约束。清理依赖新回包的活动 B1 待办让旧代码重投影。已写的普通自动值可继续显示、撤销。回退前先用新版本撤销本次结构操作并完成局部归组，重建署名投影。需要逐项还原升级当刻的成员 UUID、历史和待办时使用迁移前备份。回退演练用一次性库。

`program.LocalRuleVersion` **3 → 4 一次**，投影传入该常量，去掉 `ProjectLocalItems` 的硬编码 1。仅当前未结束的旧 B1 项按旧摘要结为输入变更，回收旧领取，旧协议/旧计划不能向新项写入。处理中已成功的操作保留。已处理且文件未变的有效决定保留，只对新规则新增的待办目标或新文件输入排项，已覆盖/已抑制的旧目标不重问。重开无结果/被拒等遵循子任务 7。识别版本、扫描及 textnorm 版本不因本任务改变，已匹配发行无需重搜。A2.5 新出现的拆分原因单独计入请求，释放门槛只涉及相应署名工作。

## 6. 固定样本和验收交接

`fixed-script.json` 的旧编码两条现在直接交标题，且短匹配串是长串的子串。改为按当前本地目标的编码候选、样本特有输入及工具匹配。`legacy-many` 只回编码 GBK，`legacy-one` 的唯一读法在扫描时解决，不发 B1 请求。`embedded-artist` 从真实复合标签提取“微雨塘”，程序随后按“微雨塘”和“堤灯社”核对到指定版并补厂牌。另测目录名复合、半缺名字。固定清单中脚本应保留的 `filename-only`、`unknown-disc-a/b`、`missing-position` 等用明确保留答复。七类的写入成功及拒绝反例在单项合成脚本中补齐，不改评分基线。

`script.go` 保留按序首条命中和已有 `$current_item`。扩展本地项绑定，将脚本中的 `$release:<成员相对路径>`、`$track:<相对路径>`、`$file:<相对路径>` 及 `$source:<相对路径>#<角色>#<标签键>/<出现序>` 从本次目录的实际 evidence 取出。用局部深拷贝替换嵌套引用，不修改规则模板，不查库、不读评分清单。默认 B1 回包从裸 item_id 改为 `undetermined` 加理由。只处理 B1 规则和分支，B2 的文本/线索由子任务 9 更新。按固定回归分别记录纯程序、程序加脚本的认对数、错认数、各样本请求（含模型）与用时，对照 `fixed-baseline.json` 写差值。已知基线错项也计入“认错 0 张”验收。

## 已定口径

1. 未识别碟目录只检查注册根内的非根父目录。直属音频子目录共同含一个专辑标签，或署名相同且多个子目录的曲号从 1 重启时，向模型提供完整候选目录。
2. 缺碟号只在曲号重启、总碟数大于 1 或已有多碟目录迹象时入队。普通单碟缺 DISCNUMBER 不入队，缺曲号照常入队。
3. 子任务 5 在确定性拆分失败处提供发行、曲目、角色及原署名引用；子任务 8 的入口只消费本地证据。匹配结论保持有效。

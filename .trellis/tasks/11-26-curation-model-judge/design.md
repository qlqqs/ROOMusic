# 识别判断与名字核对技术设计

父任务 B2、B3、B4.2 和 ADR 0009 §6.3–6.4 决定模型可以提供哪些线索。A2.3 决定程序可以写入哪个外部对象。这里沿用 0102 的逐来源结论、0103 的查询等待和 0101 的模型调用规则。短专辑的 B2 名字线索使用末节已批准的独立佐证判据。

## 一、程序与模型的交接

1. 取可见且稳定的发行，和 `curation_eligible_releases` 取交集。子任务 8 对需要辅助的目录先完成 B1，再由程序读取当前 `LocalSearchEvidence` 并跑 S8。未配模型时按程序准入执行。扫描带播客或有声书标记的发行直接排除。模型凭知识提供的错字、译名和原文写法从 B2 进入，绝不作为 B1 的推断值。
2. 程序按 5 的顺序查文件 MBID、TOC、条码、目录号与厂牌、ISRC、声纹、名字，按 A2.3 提前停止。保持程序同组直接认组、部分吻合只认组、跨组冲突的边界。实际查询得到 `unmatched` 或 `conflict` 时先保存 MusicBrainz 的状态、时间和原因，有有效模型再投影一个 `release_match` 或 `recording_match`。没有候选也走同一个识别入口。准入不足、候选全已撤销或占用、步骤等待、查询出错和已匹配沿用各自出口。普通查询错误不产生新的来源结论。
3. `modelKindSQL`、`taskKind` 和可运行顺序统一为 `local → select → verify`。B2 用 select，B3 用 verify。按具体回包类型分批。`release_merge` 仍消费 5 的“其余”并入项，沿用 verify 优先级、外部对应和结构预览，独立成包。肯定结论用已有可撤销执行器，否定或定不了则结案。已由程序处理的整碟和重复拷贝不转问。移除为整理投影和领取 `artist_identity` 的路径。子任务 10 另有用户启动的艺人队列和共享用量入口。
4. `ReadyModelKinds → PeekModelKind → prepareBatch → ClaimPreparedKind` 各步共用目标资格。让取项、事务内再领取、进度计数、恢复后的领取及待办查询均使用同一个资格谓词。单张看目标发行，合并项看两张，B1 目录看 8 提供的全部成员。继续核对原可见性、稳定性及 7 的目标互斥。扫描后变成播客的旧行不会被计为待办或发送。收到回复时校验 `ModelClaimDigest()`、当前文件摘要、开启者和证据代次。过期关闭旧项，按当前文件重新盘点。
5. 保留 7 的每日总额、同时会话数和“单次最多发送”（默认 128000 token）。估算与发送用同一消息、预留输出和同一候选集。单项过长先裁较远候选，完整本地证据保留，再超记 `content_too_long`。领取前遇到所需来源暂停只延后该项。一次有效回复后不再开模型会话补问。请求被拒、配置错、上游额度、临时故障、空或坏回复仍按 7 处理，批量普通 400 拆单，批内按 item_id 独立结算，明确“定不了”走 `FinishUndetermined`。来源执行错误不计模型空答。

## 二、识别请求与回包

用 `pendingMatchEvidence`、`MatchSnapshot`、`SearchPage` 和 S8 已经取得的 `Candidate` 准备请求。显式投影本地标签、每个文件的根内相对目录和文件名、曲序、碟号、时长、署名角色、`tracks.scan_tags.comments[{key,value}]`。读取扫描原文，另列 B1 理顺的检索名及其来源。程序的每步实查方法、参数、命中数、分页完整性、查过的编号和失败的结构条件随项发送。现有 `Pages` 有分页证据，但不会完整保留空编号查询及多位同名艺人的资料，需在 S8 实际调用点补记，再随原断点保存，不能靠猜测重建历史。携带已有候选详情、艺人 MBID、名字、别名、备注、国家和结构比较。准备阶段去掉 `PrepareSelection` 为所有候选补读详情的循环，缺失的外部明细标为未读，在确需核对时再按预算取。用户内容作为不可信资料传入。

后台沿用一次无工具的 `runtime.Selection` 读取完整文本及 app 的逐项解析。返回一个 `items` 数组，每行只属于发送过的 item_id。例如同一行可先选候选，失败后再试两组线索。

```json
{"items":[{"item_id":"本批项","decision":"judge","selected_id":"本项候选编号","groups":[{"name":{"release":"枝航谱","artist":"潮芦社"}},{"name":{"release":"枝航谱"},"version":{"country":"JP","medium":"CD"}}]}]}
```

`selected_id` 可省，`groups` 允许空数组，依次表示把握递减。有效的空选择和零组等价于“定不了”，可显式用 `decision=undetermined,reason`。明确的定不了保留理由。批内缺行、重复或格式无效依 7 逐项处理。仅承认实发项、候选和艺人引用，禁止拿未发送或已裁掉的 MBID 当选择。服务端的解析动作只提交本行答案，模型侧工具列表为空、原生搜索关闭。B2 不进入写入工具的计划与审查循环。

| 组内可选字段 | 如何取得及使用 |
| --- | --- |
| `name{release,artist,track,composer,work}` | 名字可用模型知识。按 S8 第七步构造查询，先名字加艺人及曲数，零结果才放宽，再按已有艺人查询分支。散曲按曲名、艺人及时长查录音。古典用作品名、作曲家作查询词并保留本地角色。模型文字只作检索参数。 |
| `artist_id` | 必须属于本项程序列出的同名艺人集合，按该 MBID 查发行，可与本组名字、版本条件合用。只返回一个候选的情形仍须核对结构。 |
| `identifier{kind,value}` | 仅 barcode、catalog_number。以原始串在单个本地标签值、注释值、目录名或文件名中作连续、区分大小写的原样包含比较。不可跨字段拼接、简繁转换、删标点或从外部候选取证。通过后复用 S8 第三、四步查询，目录号携带已知本地厂牌。无法定位的编号只丢该线索，纯无效编号组零请求，有独立有效条件则继续。 |
| `version{year,country,edition,medium}` | 取自本地版本描述或扫描元数据，结合已知发行组的版本日期、地区、版本说明、介质筛选。组内唯一且经结构核对才具体到版。单独给版本线索时从本项已知组及其候选开始。 |
| `album_type` | soundtrack、compilation、live 可限定类型或筛掉相反候选，合辑使用 MusicBrainz 群星。podcast、audiobook 停止本轮查询并结案，保留现有扫描字段和曲库。 |

同组各条件描述同一次查找。先按有原文的编号，再按艺人编号或名字取得候选，已有候选可直接按组、版本或类型筛选。组内共同条件继续用于过滤，不能把不相容的条件拆成多次猜测。只有类型/版本且没有查询依据时只筛现有候选。结果直接引用已读的 MB 实体，不用模型填写厂牌、年份或录音对应。

## 三、逐项查证、上限和结论

- 先核对 `selected_id` 所指的一版或录音，使用 S8 记录的来源方法、该候选的真实详情及当前本地结构。通过就提交，未通过再按组处理。命中编号时使用该类编号的 A2.3 判据，按名字找到时使用名字路径。复用 `decideAlbum`、曲目配对、时长容差、散曲及部分匹配的校验。保留本地硬编号冲突、人工对应、抑制和占用保护。现有 `ModelMatcher.readMatch`、`proof.Direct` 加审查的旧判据不能代替 A2.3。
- 每组从实际分页和详情计算通过核对的集合。查无或全部明确不符可试下一组。一张通过且该组其余已排除，采用该版并立刻停止。A2.3 只支持部分匹配到组时仅按组写。两张及以上结构通过且本组区分不了，立即记 `conflict / ambiguous_editions`，保存通过的候选，终止后续组和模型领取。这是 B2 逐组查证的终点。S8 已直接认到同组的一张不会再为分版进入 B2。列表截断或请求上限导致还没排除其余版本时，不把已读的首张当成唯一结果。
- 六次是每张、每轮 S8 之后的**额外 MusicBrainz 实际出站**。选中候选读详情、查名字、艺人/组或版本详情、短重试均共用六次。缓存命中、使用已有详情、模型调用和用户后来点选分别记账。不要在组循环以 finder 调用次数估算。给现有 `sources.Requester` 的识别上下文携带可选的发前扣数回调，在缓存未命中、通过来源闸门并将发 HTTP 时调用，每一跳/重试共用，最多六次。普通 S8、其他来源和互动查询沿用原调用。已耗尽时仍可判读缓存，若需第七发，保留先前有效来源结论并记录原因 `request_limit`。若最后一次出站是查询故障，保留错误或等待事实，不能写成查无。
- 一份有效答案先随原待办持久化，再由**程序识别工作者**执行。用 `curation_pending_items.evidence.judge` 保存提交时的选项、线索和来源会话。`curation_work_states.result.judge` 保存待办 ID、文件摘要、识别版本、本轮 generation、当前所选候选或组下标、组内步骤、已取的候选/证据、已占用请求数。两者的交接、结清模型领取和唤醒发行在同一事务。模型队列只取未提交答案的项，程序对有答案的发行只运行该断点。模型日额用尽也可继续处理已提交的答案。通过输入代次和行条件原子扣数，持久化后才出站，崩溃窗口可能少用一次，不能重启后多发。缓存与已完成的组复用，重放写操作使用同一代派生的幂等键。
- 通过 6 的共享 `QueuePolicy`、`WithIdentification`、`result.retry` 和来源恢复唤醒保存执行阶段和子步骤，显示等待来源。暂停不推进组下标，解析错误按该发行计次，连续三次进 `query_error`，出错不更新逐来源“查无”。若恢复时需要第七次实际请求，在断点记 request_limit 和最后一次错误，结束本轮额外查证，沿用最后成功的逐来源结论。只有符合 6 的三次项级错误规则才把工作行标为 query_error。文件或识别版本变化使旧断点失效。工作者恢复先判 `judge` 再选普通 S8，避免从头问模型。
- 写入沿用 `planMatch/writeMatch` 和既有 A2.5 治理操作。逐步骤写同一批次，写前锁内复核本地摘要、已核对的对象与撤销状态，完成事务更新 `SaveReleaseMatchResult`、工作状态及名字比较。已写部分以操作幂等键恢复。当前 ModelMatcher.Apply 假设项仍是会话内 in_progress。新查证分支在同一目录写锁内检查已提交答案所属项、游标代次、文件摘要和有效领取来源，人选分支检查当前有冲突的快照。保留既有写入保护和幂等回放，避免让已结案的模型项伪装成活动会话。保留模型来源、模型、MBID 和结构依据，审查 ID 可空。终止未匹配或冲突时保留来源、时间、本地层及证据，其他来源行不受影响。定不了不升档。后续文件变化、规则升级、普通未匹配的 90 天到期均先跑 S8。普通 S9 冲突沿用文件/规则触发；B2 多版留给人选的冲突按父任务 B2 末条加 90 天到期。撤销/占用的例外仍按 A2.6。

## 四、名字比较和艺人身份

名字比较以结构已核实的具体发行/录音、有效对应及曲目配对为依据。已有只认到组的组名证据可展示，不能据此生成具体版或录音的纠错。对专辑标题、专辑艺人、曲名、曲目艺人读取扫描值、有效值与对应的 MB 原文。人工保护和自动层资格在排队和写入两处检查。把证据保存、匹配状态和可执行 B3 项落在匹配收尾事务，专辑的完成计数读 matched，B3 无模型、被拒、未结均不改变识别完成状态。

| 分类顺序 | 处理 |
| --- | --- |
| 大小写、标点、空格、全半角、变音、简繁等价 | 用已有 textnorm 比较，等价结束。保留外部原文供专辑页。 |
| 一侧中日韩文字、另一侧拉丁字母 | 直接保留。本地与 MB 查询名的罗马字差异不转 B3。 |
| 一侧只多版本、现场或合作署名后缀 | 只从结尾剥离明确的 Remastered 年份、Live、feat. X 等，再比较完整主体。只出现相同单词而主体不同不能判成后缀。 |
| 同一文字只差一两个字 | 对规范串按 Unicode 字符的编辑距离 1–2 建 verify_name 项。先做以上分类，再算距离。模型回 typo、keep 或 undetermined 及理由。typo 通过来源对应、原字段、人工修订版本、抑制及领取复核后，用 `ApplyVerification` 写 MB 原值到可撤销的模型外部依据层。 |
| 其余差很多或模型拿不准 | 保留本地，直接结案，不产生建议。混合文字先去共同的数字/标点，比较两侧实际改变的文字，避免用英文后缀把整首日文歌判为跨文字。 |

艺人核对单独判断身份。沿用 `identifyArtists` 对名字/别名、位置与角色的判断。命中同名或别名直接对应，作曲者与演奏者对调直接结束。无法确定的单署名创建 verify_artist_credit，同一 MB 艺人对应两个证据不足的本地身份创建 verify_artist_merge。只投影实际生效的结构依据，含本地署名、目标对象、MB 主名/署名/别名/国家/备注、曲目及角色和支持对应的操作。B1 处理多人署名拆分。模型同批逐项回复 `{item_id,verdict,reason}`，身份用 same、different、undetermined。same 在锁内重复检查双方身份、依据操作、未手工分开及抑制条件，复用 ApplyLinks 或受治理的艺人合并与撤销。来源 method/basis 用 `model_identity`，由操作关联读回 model、operation_id。艺人页在当前对应及合并来源上据此显示“由模型判断”。旧审查标志不充作新来源。

different 的目标是“同一对名字不再问”。新表 `curation_artist_negative_pairs(name_low,name_high,reason,model,decided_at)` 主键取双方经现有名字规范化后、按确定顺序排列的两个非空名字键；优先使用实际提交的身份对的两种写法。键与发行、位置、方向、模型及规则版本无关。投影、从署名提案物化、合并排项、领取前均查此表。以同样的无序键进入 7 的目标互斥，事务内 UPSERT 否定结论、结清本项及同对仍有效的重复项。写艺人肯定结果前再查否定表。换库内名字形成新对才可再问，拿不准只结案当前证据。否定模型判断只抑制再次询问，不否决以后程序取得的确定对应。同名异人共用同一名字对是按 PRD 指定以名字去重的代价。

## 五、供界面调用的接口

| 入口 | 合同 |
| --- | --- |
| 整理页冲突列表 | 新增管理员 `GET /api/v1/admin/curation/conflicts?page=1&page_size=20`，沿用现有分页封装。每行含 release_id、本地标题、source、checked_at、reason、conflicts、snapshot_id、candidates、selection。已有逐张 `GET /api/v1/admin/curation/releases/{id}/match-results` 在相应来源行返回同一组三字段。候选含 external_id、entity、title、artist、release_group_id、date、country、medium、track_count、结构核对结果及冲突码。S9 显示证据，包括没有可选项的具体原因。B2 多个通过的候选均可选。可见性、已撤销/占用项和当前输入由服务端过滤，活动候选外不接受自由输入编号。 |
| 选定候选 | 新增管理员同源 `POST /api/v1/admin/curation/releases/{id}/match-selection`，JSON 传 source、snapshot_id、external_id，Idempotency-Key 请求头传 UUID。按当前对应文件、候选快照及 A2.3 重验选中的具体候选，通过才写入。人工选择区分多版，不能绕过曲数、时长或已有硬编号反证。用 confirmed_by 记录人，成功 200 含同一来源的新结果和 `selection{external_id,selected_by,selected_at,operation_ids}`。重复同键返回原结果，两人竞争只认第一次有效选择。过时代次/已被其他选择替换/结构不符返回 409 并给原因，来源临时不可用返回 503；客户端可刷新。历史挑选仅在当前对应仍生效时显示。 |
| 专辑页 MB 写法 | 在 `GET /api/v1/releases/{id}` 增加 `musicbrainz_names[]`，按发行和碟轨顺序返回 target_type、target_id、field、local_value、effective_value、musicbrainz_value、basis_operation_id、external_id、revision 和 can_adopt。读取已核实且仍生效的名称快照，包含程序直接保留、模型保留的项，不依赖待办存在。既有具体版在首次读时可从仍生效的对应、缓存的已匹配详情物化，必要时通过 app 注入的既有 MB 读取能力按已对上编号补详情。外部读取在事务外，保存快照前重验依据行；临时不可用时专辑页仍可读取本地资料。 |
| 一键改用 | 新增管理员同源 `POST /api/v1/admin/curation/releases/{id}/adopt-name`，JSON 传 target_id、field、basis_operation_id、expected_revision，Idempotency-Key 请求头传 UUID。按当前有效对应重取 MB 值、核对对象属于本发行及修订，在原治理事务中用发行/曲目 correction 写人工层，回 200 含字段、有效值、新 revision、operation_id。同键重放不产生新操作，过期 409。后台改错字仍走自动层。 |
| 艺人页 | 扩展原艺人详情/合并来源读取，在当前有效 MB 对应和合并来源分别给 `judged_by_model` 与 `operation_id`，撤销后按当前生效来源重新投影。 |

## 六、升级、移除与回滚

下一对连续 SQL 迁移在同一事务完成，当前最后一版为 0103，具体号与 8、10 合入顺序协调。识别规则 `MatchRuleVersion 105 → 106`，用于新识别/断点、S8 优先重开旧未匹配、有冲突和查询出错项。已匹配保留，文件变化走 5 的重核。B3 新待办 `rule_version=2`，只重分类未结项，新字段依据使用 v2。历史明确结论保留。删除 `PatrolPromptVersion` 和版本读接口的巡检字段。本地辅助版本由 8 管理，沿用 6 的资料、扫描版本。

- 为 `curation_release_match_results` 增加 `candidates jsonb`（默认空数组）、可空的 `snapshot_id` 与 `selection jsonb`。快照标识绑定发行、来源、文件、判定代次和候选内容；保存新查询结果时原子替换该来源的快照及选择状态。来源错误保留上一有效结果，点选时始终核对当前文件。无模型的程序冲突也要持久化候选。S8 证据不足的版本可展示原因，只有具备候选身份且可重新结构核对的才提供动作。
- 新增上述名字对表、`curation_match_names(release_id,target_type,target_id,field,mb_value,external_id,basis_link_id,basis_operation_id)` 名称快照表。后者以 (release_id,target_type,target_id,field,basis_link_id) 唯一，basis_link_id 外键指向 external_links，依据删除级联清理，当前本地有效值、人工保护及修订在读写时联查。存证与是否排 B3 分开。B2 游标利用现有 JSONB，查询等待沿用现有工作状态，不新增调度服务。
- 先从仍可识别的历史 verify 结案提取明确的 different 和相应两个名字，幂等填入否定表。不能从含糊理由、审查拒绝或用户未采用推断“不同人”。保留 resolved 或带明确模型结论的历史证据、已采纳操作、修正/自动层、外部对应、艺人合并、抑制、批次及总用量。保留已完成的巡检结论作为只读历史，断开产生和读取规范名的运行路径。
- 按 `kind=name_patrol`、`route=web` 或旧整理 `kind=artist_identity` 清掉尚待处理的旧项。先处理外键依赖，已写入/部分写入的项只关闭剩余工作，保留其审计引用。清理 `curation_model_exceptions` 中未采用的建议及观察（含待采用、已拒绝），保留已采纳记录及历史操作。中断的 accepting 依据已提交的 operation_ids 和实际治理操作对账后再清。旧 lookup/select、待重审项撤下旧领取，待新 S8 代次重新投影 B2，B3 用有效对应重新分类，不重新执行旧 plan。只取消关联 kind=curation 会话里尚 pending 的后台方案，保留对话/审查记录。历史存储可保留旧枚举以解码旧行。旧终态不能重新领取，历史表不再进入排队或 API。一次性升级重查为新 generation 建新待办，保留旧结案行及其证据；旧记录的明确结论不因周期盘点复活。
- 移除巡检投影、调度、prompt、工具、重巡 API 和进度，删除 `LookupTierReady`、第二档/联网工具、七天等待、每项旧档位限额、建议读写/采用/拒绝 API、整理专用 plan/escalate/review 重试及 `ReviewBackoff`。将 `LookupTierContext` 的共享来源限速单独提为程序查证上下文。涵盖新旧模型入口、恢复及资料缺口的自动联网触发。B1 只保留 8 的本地提交。后台核对动作由服务器按领取项执行，整理模型会话各协议均不装 MB、网页、MCP 或原生搜索，不走 reviewPlan。可达性以真实背景调用计数验证。互动 runner、独立审查模型设置及子任务 10 的用户发起功能使用自己的入口。
- 回退配套程序和 down SQL。先关闭新执行入口，对已保存答案与游标的活动发行收回领取并转交旧程序重投影，清本任务的执行游标，保留原 S8 断点；回到旧版可读的 route、期限和 rule_version。还原触碰的旧索引/约束，删除新字段、新表。已有普通对应、自动/人工值和操作仍可按旧格式读取和撤销。精确还原已清的旧待办、建议、否定记忆及新的人选记录，须配合升级前备份。隔离库验证 up → down → up。

## 七、实施文件与任务归属

每行文件相对于左列目录。新增文件标明用途。前端渲染、8 的目录归组/本地推断和 10 的艺人联网由对应任务交付。

| 目录 | 文件 |
| --- | --- |
| `backend/internal/catalog/` | `curation_evidence.go`、`projection.go`、`httpapi/handlers.go`、`artists_types.go`、`artists_reads.go`、`artists_media.go`，新增 `curation_match_names.go`。读扫描事实、名称、有效艺人依据。采用接口复用 `governance/corrections.go` 及现有曲目修正执行器。 |
| `backend/internal/curation/program/` | `worker.go`、`pass.go`、`release_route.go`、`release_state.go`、`queue_query.go`、`queue_projection.go`、`queue_progress.go`、`queue_recovery.go`、`model_queue.go`；`match.go`、`match_search.go`、`match_lookup.go`、`match_snapshot.go`、`model_matcher.go`、`model_editions.go`、`model_verification.go`、`name_verification.go`、`artist_links.go`、`artist_media_pass.go`。新增 `model_hints.go` 执行有序组。与 5 共用判定和写入，与 6 共用断点，与 8 合并发行依赖条件。 |
| `backend/internal/curation/` | `pending_items.go`、`model_api.go`、`model_run.go`、`model_queue.go`、`model_targets.go`、`model_exceptions.go`、`progress.go`、`work_states.go`、`match_results.go`、`verification.go`、`artist_credit_proposals.go`、`artist_merge.go`、`release_merge.go`、`release_merge_discovery.go`、`suppressions.go`。移除 `patrol.go`、`patrol_submit.go` 及 `fact_suggestions.go` 的旧业务，`local_submit.go` 的共用辅助代码交接 8。艺人建议/联网旧调用与 10 在合入时定向断开，保留它依赖的共享代码。 |
| `backend/internal/curation/model/`、`backend/internal/curation/stewardtools/` | 前者 `item.go`、`queue.go`、`scheduler.go`、`status.go`、`exceptions.go`；后者 `tools.go`、`verification.go`、`release_merge.go`、`patrol.go`。只取消整理链旧工具和审查，无工具回复到本地提交的适配接在原 Selection 上。 |
| `backend/internal/curation/httpapi/`、`backend/internal/app/` | 前者 `handlers.go` 及新增 `match_selection.go`；后者 `app.go`、`curation_model.go`、`curation_patrol_prompt.go`、`api_samples_integration_test.go`、`testdata/api-samples/curation/` 对应响应。HTTP 通过装配注入窄的挑选/采用接口，底层不导入 app。 |
| `backend/internal/sources/`、`backend/internal/steward/` | `sources/request.go`、`sources/queue.go` 负责真实出站与共享恢复；`steward/httpapi/background.go`、`steward/runtime/run.go`、`steward/runtime/run_types.go` 仅接整理运行方式、答案提交和结束条件。 |
| 数据、固定回归及规范 | `backend/migrations/NNNN_curation_model_judge.sql`、同号 `.down.sql`。 `backend/internal/upstreammock/script.go`、`backend/internal/app/testdata/curation_regression/fixed-script.json`、`backend/internal/app/curation_fixed_assertions_integration_test.go` 及引用旧接口的现有验收驱动。规范为 `.trellis/spec/backend/curation-contracts.md` §17–20、22–28 的相关段落、`steward-runtime-contracts.md` 后台运行、`steward-review-contracts.md` 整理审查部分、`artist-contracts.md` 身份来源、`database-guidelines.md` 迁移合同、`quality-guidelines.md` 固定样本。 |

## 八、PRD 范围对照

| PRD 行 | 对应设计 |
| --- | --- |
| 识别判断 | 一的交接、二的完整输入与单次回包 |
| 五种线索 | 二的字段与查询表 |
| 程序按线索查 | 三的结构核对、组内唯一性、六发共用预算和断点 |
| 让用户挑 | 五的冲突列表、逐张结果与选择写入 |
| 定不了 | 一的逐项结算、三的来源结论及重查 |
| 去掉 | 一的调度类别、六的运行入口与互动审查边界 |
| 升级清理 | 六的数据分类、恢复和回退 |
| 名字差异 | 四的五类决策、五的专辑页和采用接口 |
| 艺人是不是同一人 | 四的程序分流、不同人记忆与五的来源展示 |
| 规范 | 七的条款与实施计划中的合同门禁 |

## 已确认的短专辑判据

仅在 B2 名字线索产生唯一候选时，四首及以下专辑可由独立本地证据补足标题差异。模型的查询标题须命中外部版，本地艺人、所有碟轨位置、真实本地曲名与可比时长须逐一吻合，并通过原有硬反证检查。普通 S8 和部分吻合继续采用 A2.3 原判据。

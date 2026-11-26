# 限信息 oracle 五张未达标专辑的归因 · 2026-11-26

28 张中 23 张符合且完整，错连、错并、错改均为零。观察在 07:32:27 UTC 达到 22 分钟上限。《Ambient》和《Dummy》当时仍在等来源重试；《菲主打》《千のナイフ》《Mezzanine》此前已经以未收录结案。延长同一次观察只给前两张提供继续执行的机会。[全轮评分](oracle.json) · [重试时刻](oracle-retry.json) · [五张待定项和单张复现](oracle-gaps-evidence.json)

| 专辑 | 全轮最后的发行项 | 决定性证据 |
|---|---|---|
| 菲主打 | web，resolved/unlisted，0 个候选 | 巡检未提出正确题名，按错字或宽泛元数据未取得参考版本。单张复现中长网页含正确题名，oracle 没有把它用于下一次检索。 |
| Ambient | lookup，waiting_route，尝试 0 | 正确编号与标题均已见且有结构佐证，重试排在 07:35:31 UTC。 |
| 千のナイフ | web，resolved/unlisted，0 个候选 | 本地为罗马字，缺同一张日文发行的可核对版本证据。 |
| Mezzanine | web，resolved/unlisted，29 个候选 | 正确组编号已见。6 曲子集的组唯一性未获确认，联网项已累计 308,710 个估算 token。 |
| Dummy | lookup，waiting_route，尝试 0 | 22 个候选保留时长反证，组编号已见，重试排在 07:34:42 UTC。 |

## 菲主打的路径变化

上次真实模型 v6 的结果是正确版本 `732119e7-64bf-4110-932f-edc24f3f3adf`、14 个录音对应。证据快照保留了原始扫描名「菲主答」、巡检后发行项的 `patrol_requery`、对应方法 `search` 和标题核实项的 `model_verified`。结构核对 14/14 首时长吻合，审查后显示名为「菲主打」。上次清单 oracle 在同一编号上也是版本级完整达标。其评分只保存逐专辑结论，不能仅凭汇总将真实模型的每一次路由也归给 oracle。旧开卷驱动可以拿标注作规范题名或参考编号；本轮限信息驱动要求本次领取先取得该值。[v6 逐张结果](../../../11-25-curation-chain/research/acceptance/live-real-responses-v6.json) · [v6 巡检重查及字段快照](../../../11-25-curation-chain/research/acceptance/live-real-responses-v6-evidence.json) · [旧 oracle 逐张结果](../../../11-25-curation-chain/research/acceptance/live-oracle-after-v7.json)

本轮原始快照只有已办结的巡检和发行识别项，发行项候选数 0，联网档 11 次工具、257,033 个估算 token，以 `unlisted` 结束。未产生标题核实项。用相同版本、同一张 14 曲文件、独立新库、限信息 oracle 和长结果回环复现，也得到未收录。单张程序初搜为「菲主答＋王菲＋14 曲」，随后去掉曲数仍查「菲主答＋王菲」。首轮两个巡检工具提交的 `issues` 都为空。第二档按「菲主答」搜到 0 条；按「王菲」、首曲「容易受伤的女人」、厂牌「Cinepoly Records」分别返回 10 条，结果中均无参考版本编号。联网搜索及正文实际给出「菲主打」，oracle 随后又按同样四组词检索，没有按已取得的「菲主打」发起版本搜索，最后主动放弃。[单张工具计数、结果编号与网页题名观测](oracle-gaps-evidence.json)

直接原因落在测试 oracle 的检索策略。虽然 `backend/internal/app/curation_live_corpus_integration_test.go:207` 把错字映射交给模拟器，但 `curation_metrics_model_integration_test.go:520` 只允许已见的规范值成为巡检提案，首次巡检不产生上轮的重查。到了联网档，`backend/internal/upstreammock/oracle.go:289` 的网页处理寻找编号；`backend/internal/app/curation_metrics_model_integration_test.go:658` 把后续检索交还 `Lookup`。后者在 `backend/internal/upstreammock/oracle.go:251` 从首条消息和本项的本地回读整理查询词，第 283 行每次取至多 10 条。由成功网页结果提取的正式题名缺少进入这组查询词的路径。拿到题名后仍需用元数据搜索及详情核实版本，入口为 `backend/internal/steward/web/musicbrainz.go:40`、第 61 行。单张用例的进程退出 0 表示队列排空，其专辑评分为失败。

W5 的去曲数查找确实执行了。第 466 行放宽的仍是同一题名，在原有零候选场景也会执行。R11 在本地和外部曲数相同时退出；此处为完整 14 曲且发行项没有候选。发行署名回填需要已有版本对应，而本轮先在取得版本编号之前停止。测试新建空库，没有升级前的 matched 行或历史字段可供 104 回填。与记录相符的首处分歧是规范题名没有进入巡检重查，后续观察到题名之后也未再以它查询。[曲数放宽](../../../../../backend/internal/curation/program/match_search.go#L466) · [部分结构](../../../../../backend/internal/curation/program/match_partial.go#L43) · [旧对应回填](../../../../../backend/internal/curation/program/pass.go#L175)

## Ambient 和 Dummy 的观察窗口

- **Ambient**。本地标题是「Ambinet」。全轮逐项证据同时确认参考版本 `dd9e8a79-e72f-3b7c-8841-6e31fddca7ef` 和规范题名「Ambient」已见，两个检查的 `evidence_complete` 均为 true。失败后第二档保留下一次领取，排在结束后 3 分 4 秒。归因为 `source_unavailable`；本轮无法观察重试后是否成功纠正标题。
- **Dummy**。本地是 11 曲原版加末尾合成附轨。发行项保留 22 个候选及 `duration_mismatch`。正确组 `48140466-cff6-3222-bd55-63c27e43190d` 已见，结构证据检查未通过。最后一次仍为 lookup 并排在结束后 2 分 15 秒重试。全轮归因为 `source_unavailable`。原版组、11 曲成员详情、相对本地 12 曲的完整前缀仍须在后续领取中核实。
- 两项均保留尝试次数 0。来源退避保留路线由 `backend/internal/curation/model/item.go:189` 实现，归因器在 `backend/internal/app/curation_failure_attribution_test.go:454` 附近优先处理有来源错误且仍待重试的最后一次领取。运输记录只保存端点类别与状态，无法把全轮 24 次 503 逐次分给某张。[等待项快照](oracle-retry.json) · [全轮逐项检查](oracle.json)

## 千のナイフ

公开参考为坂本龍一的 6 曲发行 `f09ba29c-bc61-4965-aa44-e6dddbeafe1b`，本地标签为「Sen no Naifu」「Ryūichi Sakamoto」。全轮候选数 0，联网发行项 11 次工具、248,719 个估算 token，已办结为未收录。参考版本编号的检查为 `value_seen=false`、`evidence_complete=false`。缺的是把原文题名或日文曲名和这张发行连接起来的版本（或可证实的发行组）及其曲目详情。仅凭本地读音和艺人名字不能完成对应。应由 `backend/internal/steward/web/musicbrainz.go:40` 返回同版或同组的可查编号，再由第 61 行提供成员版本、录音和时长。模拟器从本地提取首曲的入口是 `backend/internal/app/curation_metrics_model_integration_test.go:536`，查询项和数量见 `backend/internal/upstreammock/oracle.go:251`、第 283 行。清单同时允许 release 与 release_group，当前 oracle 仅对“只允许组”的样本填写组目标（`backend/internal/app/curation_live_corpus_integration_test.go:212`），因此没有尝试用允许的同组其他版本证明结果。后续单张证据见下节。

## Mezzanine

6 首本地曲目是 11 曲参考版的子集。全轮得到 29 个候选，发行项冲突记录为 `track_count`。组编号 `6f9f6899-c0d3-311d-ae87-a10ae6bc53a9` 已见，评分没有找到同次领取中可验证的成员和结构。第二档 6 次工具、149,500 个估算 token；联网档 9 次、308,710 个估算 token，已经超过生产联网项 300,000 门槛。结束前该项已是 resolved/unlisted。这里还存在单项预算问题：单靠增加全局 22 分钟时限无法推进已结案的项。`backend/internal/curation/model/item.go:41` 判断预算，`model/scheduler.go:370`、第 402 行截断后续执行，`model/item.go:205` 为发行项登记未收录。

需要的是本地 6 首逐首与原版不同曲目的题名、时长配对，以及排除其他发行组结构吻合的证据。程序去曲数后已找到多个组的摘要。W4 公开查询快照中同一标题与艺人有 29 条、7 个组，不能用相同组编号的三个先到版本证明其他组均不吻合。程序读取详情的预算与候选挑选见 `backend/internal/curation/program/match_snapshot.go:206`，组唯一性检查在 `match_partial.go:167`。模型侧可见的组成员和版本曲表应由 `backend/internal/steward/web/musicbrainz.go:61` 及第 129 行分页提供。评分也有边界：`backend/internal/app/curation_failure_attribution_test.go:287` 的结构检查要求本地外部轨数完全相等，组检查在第 372 行复用它。6 对 11 不能通过这一完整结构检查。故原始 `info_missing` 可证明评分没有核实组唯一性，却不足以证明模型从未看过原版的曲表。

## 三份单张复现的补证

- 《菲主打》和《千のナイフ》都能在单张长网页回环读到正确题名。后者在两个档位都执行了四次元数据搜索。「Sen no Naifu」与首曲「サウザント・ナイブス」分别返回 0 条，艺人「Ryūichi Sakamoto」和厂牌「Columbia」分别返回 10 条，都没有参考版编号。成功网页结果中的题名没有变成下一次发行查询词。此处缺少从**已经出现的题名到源上发行编号**的检索，不应将原因笼统记为 MB 不知道这张专辑。评分用 `curation_failure_attribution_test.go:195` 对独立字符串作相等判断，嵌在「菲主打 · 王菲」或段落中的可见题名不会自动算作字段相等。第 436 行取其中一个领取作评分检查；原轮《菲主打》《千のナイフ》输出的代表项实际上是巡检项。报告对发行路径的判断另用发行待定项和工具明细。
- 《Mezzanine》的单张复现实际读到参考 11 曲版，随后把正确版本编号提交给 `resolve_release_match`。工具明确返回 `track_count` 及「曲数不同，现有候选也不能确认唯一的部分吻合发行组」。这将原因收窄到**对其他候选组的结构核实**，参考版编号和自身轨表均已取得。入口 `backend/internal/curation/program/model_editions.go:23` 将选中版本和已有候选合并，第 27 行再次验证部分结构，第 29 行返回上述错误。模型工具对同一编号重复尝试，未补足能排除其他组的曲表。应由候选准备或核验入口向这一判定提供其他相关组的完整候选证据，或记录哪些组仍未能排除。单张观察里「已取得原版」和「无法证成唯一组」同时成立。
- 全轮使用 28 张的长结果回环；单张回环只有当前一张，结果顺序、数目、503 和单项用量可以不同。原轮引用 `oracle.json` 的会话／工具评分投影、07:28:08 UTC 的待定项快照及排定重试。其隔离库结束时按项目清理，未保留完整会话正文。单张复现的逐次查询、读取详情、提交错误与网页题名观测单独标记。三次独立进程均自然排空，三个单张评分仍为 `unlisted`。本次没有重跑全库。[原轮待定项与单张工具明细](oracle-gaps-evidence.json)

串行真实模型轮于 07:58:51 UTC 取证并按指令停止。最近进度为 1,568 秒，28 张均经程序处理。已完成 110 项，42 项等待模型或其路线重试。已落库 118 次带 usage 的模型调用，合计 488,254 token，独立审查记录为零。MB 最近进度累计 221 次请求，含 29 次 503。真实轮尚无终态评分。该轮及三次诊断的容器、网络和卷均按各自 Compose 项目标签核验。端点和密钥经定串 grep 核查，结果为零匹配。[串行真实轮部分快照](real-serial-partial.json)

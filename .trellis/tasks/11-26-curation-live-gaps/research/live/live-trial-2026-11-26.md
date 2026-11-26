# 28 张公开专辑整理试用 · 2026-11-26

最终真实模型轮在 `d14cf881` 上运行一次。到达 40 分钟硬上限，用时 40.00 分钟。按 R18 计，符合预期 21/28，完整达标 20/28。错连、错并、错改分别为 0、0、0。评分识别出来源不可用时结案的记录 5 条，等来源重试 3 项，其他未结 1 项。V6 的 100% 符合门槛未达到。[最终评分](real.json)

程序轮与限信息 oracle 轮沿用原运行数据，符合数按修订后的 accept 与 R18 复核。[评分复核](oracle-gaps-2026-11-26.md)

| 轮次 | 符合／完整 | 错连／并／改 | MB 请求／503 | 耗时 |
|---|---|---|---|---|
| program | 16/28、15/28 | 0/0/0 | 67/4 | 184.6 秒 |
| oracle | 27/28、23/28 | 0/0/0 | 211/24 | 1320.4 秒 |
| 最终真实轮 | 21/28、20/28 | 0/0/0 | 441/42 | 2400.0 秒 |

oracle 在 22 分钟时限结束，留有两个排定重试的发行项。[重试快照](oracle-retry.json)

| 专辑 | 程序终态／完整 | 限信息 oracle 终态／完整 |
|---|---|---|
| 唱遊大世界王菲香港演唱會98–99 | release_group／否 | release_group／是 |
| 菲主打 | unlisted／否 | unlisted／否 |
| 菲賣品 | release／是 | release／是 |
| Il canto delle pellicole | release_group／是 | release_group／是 |
| Goodbye | release／是 | release／是 |
| Boys for Pele | unlisted／否 | release_group／是 |
| The Division Bell | release／是 | release／是 |
| Gilgamesh | release／是 | release／是 |
| The Belly of an Architect | release_group／是 | release_group／是 |
| Amadeus: Special Edition: The Director’s Cut | release／是 | release／是 |
| Ambient | unlisted／否 | uncertain／否 |
| M.elle le "Gladiator" | release_group／是 | release_group／是 |
| …I Care Because You Do | unlisted／否 | release_group／是 |
| soundtrack from thirtysomething | release／是 | release／是 |
| Arise | release／否 | release／是 |
| Dive Into Disney | release／是 | release／是 |
| The Bower of Despair | release_group／是 | release_group／是 |
| Powerslave | release／是 | release／是 |
| Gettin’ In Over My Head | release／否 | release／是 |
| Elysian Fields | release／是 | release／是 |
| Alive 2007 | release_group／是 | release_group／是 |
| Music Has the Right to Children | uncertain／否 | release_group／是 |
| The Matrix | uncertain／否 | release_group／是 |
| My Life in the Bush of Ghosts | release_group／是 | release_group／是 |
| 千のナイフ | unlisted／否 | unlisted／否 |
| Mezzanine | uncertain／否 | unlisted／否 |
| Dummy | uncertain／否 | uncertain／否 |
| 浮躁 | unlisted／否 | release_group／是 |

## 最终真实模型轮

本轮运行于 main 的 `d14cf881`，早于 R25/R26 对模型接口 429/503 的请求内重试和从 5 秒起步的短冷却改动。

新建隔离库，主模型和独立审查均为 `gpt-6.2-sol`。启用整理前已核对 `model_concurrency=4`，总日额和联网日额均为 `null`。网页搜索及正文使用 long 回环，D16 使用生产设置。服务地址 <Responses 端点>，凭据 <略>。

MusicBrainz 请求 441 次，响应状态及次数为 200（397）、404（2）、503（42）。回环网页搜索 98 次，取正文 1 次。会话 198 个，其中 0 个没有消息。

### 模型调用与实报用量

| 口径 | 调用数 | 输入 token | 输出 token | 合计 token |
|---|---:|---:|---:|---:|
| Responses 全部（含审查） | 513 | 4,057,755 | 72,250 | 4,130,005 |
| local_cleanup | 3 | 5,674 | 157 | 5,831 |
| 其中第二档主调用 | 32 | 440,351 | 5,081 | 445,432 |
| patrol | 89 | 89,223 | 11,537 | 100,760 |
| 其中独立审查 | 26 | 31,474 | 5,011 | 36,485 |
| select | 8 | 306,756 | 698 | 307,454 |
| verify | 47 | 212,496 | 5,319 | 217,815 |
| 其中联网主调用 | 308 | 2,971,781 | 44,447 | 3,016,228 |

实报取 Responses 返回的 usage，连接探测剔除。缺少完整 usage 的调用 1 次。数据库按生产计费归属记录总用量 4,130,005，其中联网 3,052,713（输入 3,003,255、输出 49,458）。上表按调用类型单列独立审查。

### 排空速度与并发

计时从启用整理开始，分子统一为已 `resolved` 的模型待定项。排队、来源退避和等待重试均计入耗时。

| 轮次 | 已完成项数 | 观察秒数 | 项/分钟 |
|---|---:|---:|---:|
| 串行部分快照 | 110 | 1,568.0 | 4.21 |
| 并发、每日限额部分快照 | 113 | 1,237.8 | 5.48 |
| 最终并发轮 | 151 | 2,400.0 | 3.77 |

相同时长复核：串行快照在第 1,568 秒完成 110 项，本轮截至该时点完成 141 项。限额并发快照在第 1,237.8 秒完成 113 项，本轮截至该时点完成 140 项。[串行快照](real-serial-partial.json) · [并发限额快照](real-concurrent-partial.json)

按 steward_messages 同一会话首末消息取区间，与并发部分快照口径一致。下表单位为秒，逐分钟分布位于 real.json 的 final_observations.concurrency_by_minute。

| 启用后时间 | 0 路 | 1 路 | 2 路 | 3 路 | 4 路 |
|---|---:|---:|---:|---:|---:|
| 0–600 秒 | 0.8 | 17.0 | 18.5 | 428.7 | 135.0 |
| 600–1200 秒 | 290.8 | 243.9 | 65.3 | 0.0 | 0.0 |
| 1200–1800 秒 | 345.4 | 228.1 | 26.6 | 0.0 | 0.0 |
| 1800–2400 秒 | 488.7 | 56.7 | 15.6 | 31.8 | 7.2 |
| 0–2400 秒 | 1125.8 | 545.6 | 126.0 | 460.5 | 142.2 |

### 五分钟采样与截止状态

`Test真实曲库队列试用` 退出码为 1。评分器将 3 张未完整达标样本归为 `info_missing`，队列在 40 分钟时仍有在途项。截止时 State 为 `running`，UnavailableReason 为 `null`，运行 1/4，待模型 3。

| 启用后秒数 | 运行中 | 待模型 | State | UnavailableReason |
|---:|---:|---:|---|---|
| 300.5 | 3 | 42 | running | null |
| 600.5 | 1 | 15 | running | null |
| 900.0 | 2 | 12 | running | null |
| 1200.1 | 1 | 10 | running | null |
| 1500.1 | 0 | 10 | idle | null |
| 1800.3 | 2 | 5 | running | null |
| 2100.5 | 0 | 6 | idle | null |
| 2400.0 | 1 | 3 | running | null |

快照中已结 151 项，待处理 4 项。

| 未完成项 ID | 类型／路线 | 状态 | 下次执行 UTC | 评分 |
|---|---|---|---|---|
| 2f9563a7-9069-4b47-9b2b-804ca7ee8e81 | artist_identity／web | in_progress | — | 其他未完成项 |
| 7210a803-072c-4ba5-8a0c-4496f57be85e | release_match／lookup | waiting_route | 2026-11-26T09:58:52.958122+00:00 | 已按 R18 排定来源重试 |
| 7d3a1262-b011-4a7f-a6f5-76cfb4104e54 | artist_identity／web | waiting_route | 2026-11-26T10:07:25.762886+00:00 | 已按 R18 排定来源重试 |
| edb64279-1b6b-54e5-b5c8-0dc975260548 | verify_name／web | waiting_route | 2026-11-26T09:59:08.086813+00:00 | 已按 R18 排定来源重试 |

### D16 单项峰值

| 档位 | 项数 | 工具次数峰值／上限 | token 峰值／上限 |
|---|---:|---:|---:|
| lookup | 5 | 7／20 | 113,914／150,000 |
| web | 37 | 21／30 | 198,725／300,000 |

两个档位的工具次数和实报 token 峰值均低于生产门槛。[生产门槛](../../../../../backend/internal/curation/model/item.go#L36)

### 逐张结果

| 专辑 | 终态 | 符合 | 完整 | 评分依据或差异 |
|---|---|---|---|---|
| 唱遊大世界王菲香港演唱會98–99 | release_group | 是 | 是 | 正确发行组对应 |
| 菲主打 | release | 是 | 是 | 参考发行及录音对应完整 |
| 菲賣品 | release | 是 | 是 | 参考发行及录音对应完整 |
| Il canto delle pellicole | release_group | 是 | 是 | 正确发行组对应 |
| Goodbye | release | 是 | 是 | 参考发行及录音对应完整 |
| Boys for Pele | release_group | 否 | 否 | 来源不可用时结案：artist_identity/suggestion_recorded |
| The Division Bell | release | 是 | 是 | 参考发行及录音对应完整 |
| Gilgamesh | release | 是 | 是 | 参考发行及录音对应完整 |
| The Belly of an Architect | release_group | 否 | 否 | 来源不可用时结案：artist_identity/suggestion_recorded |
| Amadeus: Special Edition: The Director’s Cut | release | 是 | 是 | 参考发行及录音对应完整 |
| Ambient | release_group | 否 | 否 | 专辑显示名 "Ambinet"，期望 "Ambient"；来源不可用时结案：verify_name/suggestion_recorded；已按 R18 排定来源重试 |
| M.elle le "Gladiator" | release_group | 是 | 是 | 正确发行组对应 |
| …I Care Because You Do | release_group | 是 | 是 | 正确发行组对应 |
| soundtrack from thirtysomething | release | 是 | 否 | 已按 R18 排定来源重试 |
| Arise | release | 是 | 是 | 参考发行及录音对应完整 |
| Dive Into Disney | release | 是 | 是 | 参考发行及录音对应完整 |
| The Bower of Despair | release_group | 是 | 是 | 正确发行组对应 |
| Powerslave | release | 是 | 是 | 参考发行及录音对应完整 |
| Gettin’ In Over My Head | release | 是 | 是 | 参考发行及录音对应完整 |
| Elysian Fields | release | 是 | 是 | 参考发行及录音对应完整 |
| Alive 2007 | release_group | 否 | 否 | 待定项未结：artist_identity/in_progress |
| Music Has the Right to Children | release_group | 是 | 是 | 正确发行组对应 |
| The Matrix | release_group | 否 | 否 | 来源不可用时结案：artist_identity/suggestion_recorded |
| My Life in the Bush of Ghosts | release_group | 是 | 是 | 正确发行组对应 |
| 千のナイフ | waiting_source_retry | 否 | 否 | 来源不可用时结案：artist_identity/suggestion_recorded；缺少清单中的发行或发行组对应；已按 R18 排定来源重试 |
| Mezzanine | release_group | 是 | 是 | 正确发行组对应 |
| Dummy | release_group | 是 | 是 | 正确发行组对应 |
| 浮躁 | release_group | 否 | 否 | suggestion_can_accept submit_verification，期望 false，实际 true |

### 原 20 张重点项与《Mezzanine》

- 《Ambient》终态 release_group，不符合，未完整达标。最终显示名「Ambinet」。
- 《Boys for Pele》终态 release_group，不符合，未完整达标。最终显示名「Boys for Pele」。
- 《…I Care Because You Do》终态 release_group，符合，完整达标。最终显示名「…I Care Because You Do」。
- 《菲主打》终态 release，符合，完整达标。最终显示名「菲主打」。
- 《Mezzanine》终态 release_group，符合，完整达标。

### 未完整样本的归因

评分器输出 info_missing 3、model_judgment 3、source_unavailable 2。

《Boys for Pele》归为 model_judgment。
- 项 `262da76d-148a-4238-8e3a-ea54b9851b2d` 的 release 目标为 4c31aef1-177e-4bba-9a56-180e21a0d043，value_seen=true，evidence_complete=true。

《The Belly of an Architect》归为 info_missing。
- 项 `99280112-fd4c-4f44-9c1e-a65ccfc9250d` 的 release 目标为 89803205-72dc-42dd-8897-f294ff33d1b9，value_seen=false，evidence_complete=false。

《Ambient》归为 source_unavailable。
- 项 `8f40097c-475d-4eef-b5c3-b9270c81e720` 的 title 目标为 Ambient，value_seen=true，evidence_complete=true。

《soundtrack from thirtysomething》归为 info_missing。
- 项 `f136a8ad-ef4c-4997-8b21-6cbf3d8c3e04` 的 release 目标为 8add377d-8c16-4fc9-99ef-c3a7f5462ae0，value_seen=false，evidence_complete=false。

《Alive 2007》归为 info_missing。
- 项 `ed2dbfcc-15c7-4b34-b261-02347d527efb` 的 release 目标为 def847d5-48a6-4bb6-8887-ae4f7f945e6c，value_seen=false，evidence_complete=false。

《The Matrix》归为 model_judgment。
- 项 `1d1acfe7-40b2-4be6-8d1d-e408e4a24b39` 的 release 目标为 b0492de5-8bda-3f4a-91ac-a562eb1c0648，value_seen=true，evidence_complete=true。

《千のナイフ》归为 source_unavailable。
- 项 `7210a803-072c-4ba5-8a0c-4496f57be85e` 的 release 目标为 f09ba29c-bc61-4965-aa44-e6dddbeafe1b，value_seen=true，evidence_complete=false。

《浮躁》归为 model_judgment。
- 项 `99ec99cf-fa11-4727-a7d6-aa7a4bd129ad` 的 release 目标为 45e62a56-76c8-35be-b3f3-c5ed3781e2d4，value_seen=true，evidence_complete=true。

完整逐项检查和查询证据位于 [real.json](real.json)。

## 真实并发轮部分快照

提交 `cda28f2e`，并发数 4，两模型均为 gpt-6.2-sol，网页回环 long。第 516 秒已完成 110 项，串行快照第 1,568 秒完成 110 项。随后每日总额度 3,000,000 token 阻止新项领取。实际停止前已完成 113 项、待处理 34 项，应用记账 3,264,354 token，其中联网 2,853,334。MusicBrainz 最近进度累计请求 218 次，含 43 次 HTTP 503。第二档单项峰值为 2 次工具、6,414 token，联网档为 18 次、300,701 token，超过生产联网 token 门槛 701。本轮按指令停止，未产生 28 张的终态评分。[并发部分快照](real-concurrent-partial.json)

## 待报告缺陷／后续事项

### 第 550 秒的并发与待领取项

从启用整理到第 550 秒，观察到 4 个同时运行的会话共 224.39 秒。按时间加权的分布如下。每个区间从会话首条用户消息开始到运行结束，包含工具和独立审查耗时。

| 窗口 | 0 路 | 1 路 | 2 路 | 3 路 | 4 路 |
|---|---:|---:|---:|---:|---:|
| 0-550 秒 | 0.89 秒 | 4.97 秒 | 18.11 秒 | 301.64 秒 | 224.39 秒 |
| 500-550 秒 | 0.00 秒 | 4.69 秒 | 12.34 秒 | 30.63 秒 | 2.34 秒 |
| 510-570 秒 | 0.00 秒 | 24.69 秒 | 12.34 秒 | 21.69 秒 | 1.29 秒 |

503 会延后部分项，550 秒时实际先命中每日用量门槛。第 506 秒用量为 2,941,841 token，第 516 秒为 3,009,803 token，隔离库日额为 3,000,000。已经领取的运行继续完成。第 547 秒和第 557 秒的队列采样均为 34 项等待、1 项在途，其间 Monster Magnet 的艺人身份项从第 480.10 秒持续至第 689.54 秒。进度日志的“运行中”取自数据库在途项数；第 550 秒没有留存当次完整 API 响应，单路运行由上述会话起止独立核实。调度器先认定日额用尽，在尝试普通路线及第二档就绪检查之前返回。该时点没有新项可领的直接原因是日额。

34 项中另有 22 项的重试时间晚于第 550 秒。其余 12 项的重试时间已到或未设置，其中 The Matrix 的巡检和发行匹配共享在途艺人的互斥键。第二档项目不会执行到 LookupTierReady，此时没有证据把它们的主阻塞归给 MB 退避。这一观测没有显示 R24 调度失效。[额度判定](../../../../../backend/internal/curation/model/status.go#L120) · [领取顺序](../../../../../backend/internal/curation/model/queue.go#L118) · [互斥键](../../../../../backend/internal/curation/model_targets.go#L18) · [第二档来源闸门](../../../../../backend/internal/curation/program/model_queue.go#L12) · [逐项取证及会话区间](real-concurrency-evidence.json)

| 等待目标 | 项／路线 | 下次领取 UTC | 同时存在的条件（全部受日额限制） |
|---|---|---|---|
| 02 - 王菲 - 菲主打 | verify_name／web | 08:34:48 | 重试尚未到期 |
| 02 - 王菲 - 菲主打 | verify_name／web | 08:32:53 | 重试尚未到期 |
| 06 - Tori Amos - Boys for Pele | release_match／lookup | 未排定 | 已可到期检查 |
| 11 - Moby - Ambient | verify_name／web | 08:31:14 | 重试尚未到期 |
| 13 - Aphex Twin - …I Care Because You Do | release_match／select | 08:26:45 | 已可到期检查 |
| 22 - Boards of Canada - Music Has the Right to Children | release_match／select | 08:26:45 | 已可到期检查 |
| 23 - Various Artists - The Matrix | name_patrol／patrol | 未排定 | 已可到期检查，与在途项共用艺人 |
| 23 - Various Artists - The Matrix | release_match／select | 08:24:54 | 已可到期检查，与在途项共用艺人 |
| 25 - Ryūichi Sakamoto - Sen no Naifu | release_match／lookup | 08:34:44 | 重试尚未到期 |
| 26 - Massive Attack - Mezzanine | release_match／select | 08:26:45 | 已可到期检查 |
| 27 - Portishead - Dummy | release_match／lookup | 未排定 | 已可到期检查 |
| 28 - 王菲 - 浮躁 | release_match／select | 08:26:45 | 已可到期检查 |
| 28 - 王菲 - 浮躁 | verify_name／web | 08:33:20 | 重试尚未到期 |
| Beauty Queen | artist_identity／web | 08:32:18 | 重试尚未到期 |
| Ben | artist_identity／web | 08:31:06 | 重试尚未到期 |
| Boards of Canada | artist_identity／web | 未排定 | 已可到期检查 |
| Busta Rhymes | artist_identity／web | 08:33:56 | 重试尚未到期 |
| Deftones | artist_identity／web | 08:35:50 | 重试尚未到期 |
| Gabrielle | artist_identity／web | 08:34:06 | 重试尚未到期 |
| Jason | artist_identity／web | 08:29:02 | 重试尚未到期 |
| Lunatic Calm | artist_identity／web | 08:36:32 | 重试尚未到期 |
| Marilyn Manson | artist_identity／web | 08:35:16 | 重试尚未到期 |
| Massive Attack | artist_identity／web | 未排定 | 已可到期检查 |
| Meat Beat Manifesto | artist_identity／web | 08:36:11 | 重试尚未到期 |
| Prodigy | artist_identity／web | 08:35:45 | 重试尚未到期 |
| Propellerheads | artist_identity／web | 08:36:10 | 重试尚未到期 |
| Rammstein | artist_identity／web | 未排定 | 已可到期检查 |
| Rob D | artist_identity／web | 08:36:12 | 重试尚未到期 |
| Ryūichi Sakamoto | artist_identity／web | 未排定 | 已可到期检查 |
| Stardust | artist_identity／web | 08:33:53 | 重试尚未到期 |
| T. Bangalter | artist_identity／web | 08:34:55 | 重试尚未到期 |
| Tsutchie Feat. Mayu Kitaki | artist_identity／web | 08:32:40 | 重试尚未到期 |
| W.G. Snuffy Walden and Jay Gruska | artist_identity／web | 08:32:46 | 重试尚未到期 |
| W.G. Snuffy Walden and Stewart Levin | artist_identity／web | 08:33:11 | 重试尚未到期 |

### 空会话

本轮 137 个会话都有消息。旧 oracle 轮的 1,312 个会话中有 1,154 个空会话，日志中 `curation.model.failed error=try_later` 也出现 1,154 次。旧版（fb253487）在 `backend/internal/curation/model/scheduler.go:168` 先创建会话，在第 322 行构造首条消息。名称核实要重新读取 MusicBrainz 证据，来源暂缓可能在写入首条消息前返回。第 329 行释放领取，没有设置下一次领取时间，也没有删除空会话；相同待定项可被再次领取并创建新会话。当前对应入口见下方。旧库已清理，现存记录只能核实汇总与错误条件，不能把这 1,154 行逐一关联到具体待定项。本次只记录此缺陷。[创建会话](../../../../../backend/internal/curation/model/queue.go#L132) · [准备核实证据](../../../../../backend/internal/curation/model/item.go#L87) · [首条消息失败后的释放](../../../../../backend/internal/curation/model/scheduler.go#L410) · [释放领取](../../../../../backend/internal/curation/model/scheduler.go#L566)

# 28 张公开专辑整理试用 · 2026-11-26（真实轮含两份部分快照）

已在独立库完成程序轮和限信息 oracle 轮。真实串行轮依指令提前停止。已落库 118 次有实报用量的模型调用，输入 472,896、输出 15,358 token。MB 最近观测为 221 次，其中 29 次 HTTP 503。这份串行快照用于速度对照。[串行部分快照](real-serial-partial.json) · [五张 oracle 归因](oracle-gaps-2026-11-26.md)

| 轮次 | 符合／完整 | 错连／并／改 | MB 请求／503 | 耗时 |
|---|---|---|---|---|
| program | 15/28、15/28 | 0/0/0 | 67/4 | 184.6 秒 |
| oracle | 23/28、23/28 | 0/0/0 | 211/24 | 1320.4 秒 |

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

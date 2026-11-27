# 管家单张专辑整理工具与技能

## 目标

管理员在专辑页打开管家对话，让管家自己调查、比对并处理这一张专辑：看本地文件、查 MusicBrainz、重新归组、建立外部对应。写入结果和自动整理链写的一致，可以撤销。整理链定不了的专辑，有一条由管家处理的路。

## 已确认的事实

管家现状：
- 能读专辑、版本、曲目、证据（含音乐目录下的相对路径，最多 100 条）、问题和操作记录；能查 MusicBrainz（`search_music_metadata`、`get_music_metadata`，不依赖联网搜索设置，每用户每分钟 20 次，与整理共用每秒 1 次限速，`backend/internal/steward/web/limiter.go:9-11`）；能修正字段、调整专辑组、整张并入、认领、合并拆分艺人、撤销。
- 不能：建立 MusicBrainz 对应；重新归组；列目录；读本地文件内容。
- 没有技能机制，系统提示是固定字符串（`backend/internal/steward/runtime/run_content.go:11`，模式附加句在 :244，拼装在 `runtime/run.go:505-515`）。
- 每次运行最多 24 次模型请求、40 万 token（`runtime/run.go:19-21`），用户可用"继续"接着跑；没有按对话累计的工具调用上限。
- 工具框架：`tool.Definition`（`steward/tool/definition.go:107`）；ScopeRead 直接执行，ScopeShared 走确认（`runtime/run_tools.go:40`）；注册在 `app/steward.go:108-163`，同一注册表暴露给 MCP；输出上限 64 KiB（`tool/registry.go:14`）；`Untrusted` 输出包成 `<roomusic-source>`。

建立对应（路径相对 `backend/internal/curation`）：
- 管理员挑冲突候选走 `ModelMatcher.SelectMatch`（`program/match_selection.go:17`）：发行级 advisory 锁、`item_stale` 多处复核、`prepareSelected`（:145）重取发行详情后走 `decideAlbum`/`partialAlbumMatch`，不过返回 `insufficient_corroboration`；写入经 `Apply`（`program/model_matcher.go:346`）→ `writeMatch`（`program/enrich.go:353`）：外部对应、自动字段、曲目字段、加入专辑组、整张并入发现，最后保存结论与工作状态。每步是独立可撤销操作，幂等键派生，可中断重放。
- 限制：只接受 `conflict` 状态且所选 ID 在快照候选里（:72、:86）；不做署名拆分与艺人对应（之后由队列 `backfillMatch` 补，`program/track_fields.go:103`）；拒绝只认到发行组的结论。
- 没有"给一个任意 MBID"的入口；`ModelMatcher.Plan`（:179）能自己 Lookup，但必须挂在一条待定项上。散曲（单轨 `loose_unknown`）走录音分支（`program/model_matcher.go:280-299`、`program/enrich.go:326-339`）。
- 后续处理靠程序 worker 轮询"最新一条非整理批次的操作"自动接住（`work_states.go:43`、`queue_worker.go:42`），封面、歌词、艺人补全会照常排队。
- 并发：worker 保存结论时比对 generation 和状态，被外部写入抢先就重来（`program/pass.go:423-427`）；`ResolvePendingItems` 只结束等待中的项，不动处理中的（`work_states.go:172`）。
- `resolve_release_match` 在聊天和后台都没注册（`stewardtools/tools.go:35-42`）。

重新归组：
- 本地辅助的归组写入：`checkGrouping`（`local_grouping.go:28-99`，整体覆盖、文件不重复、同碟曲号不重复、已有真实碟轨号必须一致）→ `storeGroup` → `requestGroupScan`；表 `local_model_groupings`（`migrations/0104_curation_local_assist.sql:11-16`）。
- 输入是 `{albums:[{files:[{file_id,disc,track,tracks?}]}]}`，`file_id = Fingerprint([root_id, relative_path])`；范围是一组相互连通的目录（`localScopeKeys`，`local_items.go:245`），不能跨不相关的文件夹。
- 写入后标记发行未确认，满 60 秒后确认扫描生效；文件大小、修改时间、CUE 区间变了或目录多出文件时整份失效（`ingest/staging.go:225-247`）。
- 发行 ID：严格过半曲目属于同一旧发行时沿用，其余新建（`ingest/identity_plan.go:489`）；曲目 ID 不变，曲目修正跟着曲目走。
- 撤销：删除记录、恢复前态、再扫一次，并记"不再提出"（`local_grouping.go:207`）。
- 保护：有人工结构操作（人工合并、人工调整专辑组）或已有外部对应的发行不参与归组（`local_items.go:84`、:310）。
- 现有提交入口要求一条处理中的 `local_cleanup` 待定项和后台会话（`local_submit.go:279-292`），聊天要另写入口。

文件访问：
- 根与排除：`library_roots`、`library_root_exclusions`；`Service.ListRoots`（`library/root_info.go:52`）只给根的末段名和排除列表；内置忽略目录与隐藏文件在 `library/scan_filter.go:13-39`；逐段判定祖先是否被排除的逻辑未导出（`scan_scope.go:36`）。
- 安全打开先例：`os.OpenRoot` + `root.OpenFile`（`playback/open.go:67`、`backup/files.go:94-108`）。
- 解析：`parser.ParseAudioFile(path)`（`library/parser/parser.go:80`）按路径打开；CUE 有 `ParseCueDocument`（`cue.go:80`）；文本编码识别 `decodeCueText`（`cue_text.go:19`，BOM → UTF-8 → GBK/Shift-JIS/Big5）未导出；图片尺寸用 `image.DecodeConfig`（`attachments.go:94`）。
- 反查：`tracks(source_root_id, relative_path, ...)`，CUE 曲目是虚拟路径，按 `cue_parent_relative_path`/`cue_sheet_path` 反查；附件在 `release_attachments`。
- 现有测试断言管家输出不含 `relative_path`（`catalog/stewardtools/review_c7_integration_test.go:46`），新工具要明确划为例外。

散曲：只有没有专辑标签的单轨算散曲，整理链只认录音；有专辑标签的单曲按专辑处理，曲数不全时经部分对应最多认到发行组（`program/match_partial.go:186`）。

ADR 0009 去掉了后台整理里"模型自己查 MusicBrainz"和"联网找编号"两档（第 433、434 行）；本任务是管理员点名处理单张，不改后台整理链。

## 已定的决定

用户：
- 管家自己决定怎么查；写外部对应时后端按整理链标准核对，确认是否弹出照对话模式。
- 用户排除的文件夹在列目录和读文件里完全不显示。
- 网页来源的字段写入按现状：只在技能里要求"先给用户看、同意再用修正写入"，不做硬性拦截。
- 已有外部对应或人工结构操作的发行保持受保护：管家先撤销原对应或人工合并，再重新归组。
- "整理不了的专辑"列表本任务不做，以后一起优化。
- 不把散曲合并成专辑；不同文件夹里同一专辑的单曲不合并。

主代理（技术细节，见 design.md）：
- 建立对应只接受尚无 MusicBrainz 对应的发行；已对应的先撤销。"不再提出"里的 ID 拒绝，并提示去设置里清除。
- 允许组级结论（部分专辑、多版本），与后台整理链一致。
- 管家的重新归组算人工结构，之后本地辅助不再改它；确认扫描不等 60 秒静默。
- 列目录与读文件只给相对路径；读文件不给封面字节和歌词；文本上限 64 KiB。
- 技能随后端打包，只读加载，按身份过滤。

## 需求

- R1 建立对应工具 `match_release_external`：给发行和 MusicBrainz 发行 ID（散曲给录音 ID），复用 `prepareSelected` 核对与 `Apply`/`writeMatch` 写入，持发行锁并绑定当前 file_digest。
- R2 重新归组工具 `regroup_local_files`：给一组连通目录里的文件分组，复用 `checkGrouping`、`storeGroup`、`requestGroupScan` 与撤销。
- R3 列目录工具 `list_library_directory`：读磁盘并标上扫描归属，排除的完全不显示。
- R4 读文件工具 `read_library_file`：小文本全文并识别编码；音频给解析后的标签；图片给格式尺寸；在根内安全打开。
- R5 技能机制 `load_skill` 与"整理一张专辑"技能（要点见 design.md 第 6 节）。
- R6 与后台整理链协调：写入经操作记录让 worker 接住后续处理；并发靠锁与 generation；结束该发行等待中的待定项。
- R7 前端工具名标签。

## 验收标准

- A1（R1）对一张未对应、结构吻合的专辑调用建立对应后：外部对应、空字段补全、工作状态为已匹配；之后署名、封面任务自动排队；每步操作可撤销。
- A2（R1）曲数、碟序或时长对不上时返回 `insufficient_corroboration` 和具体原因，不写任何东西；已对应返回 `already_matched`；"不再提出"里的 ID 返回 `suppressed`。
- A3（R1）有专辑标签、曲数不全的专辑认到发行组；散曲认到录音。
- A4（R1、R6）后台 worker 与管家同时写同一发行，只有一方成功，另一方得到明确的冲突。
- A5（R2）混放目录分成两张、并错的 Bonus 拆成独立一张：扫描后归属正确，曲目 ID 不变；受保护发行返回 `protected`；四类非法归组各自被拒；之后本地辅助不改它；撤销恢复原样。
- A6（R3、R4）`..`、绝对路径、越界链接、内置忽略、用户排除路径都读不到，被排除路径返回 `not_found`；输出不含绝对路径；非管理员被拒。
- A7（R4）GBK 与 UTF-8 文本正确解码；音频结果不含封面字节和歌词；超限返回 `too_large`。
- A8（R5）非管理员看不到整理技能；加载未知技能报错；系统提示里有技能列表。
- A9 依赖门禁、改动包整包单测、相关集成测试通过；合同文档更新。

## 不做

- 整理不了的专辑列表与"交给管家"入口。
- 修改后台整理链的调度与规则。
- 网页来源写入的硬性确认。
- 用户自写技能。
- 散曲合并成专辑；跨不相关文件夹的归组。
- 管家直接重新归组受保护的发行。

## 规划路线

人工批准：2026-11-27 用户在选择框里选"批准，现在开始实施"，批准 R1–R7 与三批实施计划。

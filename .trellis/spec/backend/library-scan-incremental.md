# 增量扫描（④）

从[目录与扫描流程合同](./library-scan-contracts.md)拆出：§3 的增量扫描（存储与观察、缓存与延迟封面、版本号与升版本规则、借用指纹与完整重扫、候选签名与跳过）；章节编号沿用原合同。

### 增量扫描（④）

解析缓存省去未变文件的解析；候选签名与状态判定省去未变候选的写入、封面读取和抓轨日志内容读取。两者分别判断，不依赖彼此命中；完整重扫绕过两种跳过但仍更新缓存与候选状态。不增加开关。

#### 存储与观察

- `0041_incremental_scan.sql`（合入编号，接 main 的 0040）增加 `library_parse_cache`、`scan_candidate_states` 和 `scan_runs.scan_mode`；下行删除两表与列。均不回填缓存，不迁移曲目或 Release 身份，不增加环境变量。
- `library` 拥有 `library_parse_cache`：主键 `(root_id, relative_path)`，路径使用 `ingest.NormalizedPath`；列为 `directory/source_size/source_modified_at/parser_version/observation/seen_scan_id/updated_at`，目录索引 `(root_id,directory)`；根外键级联删除。
- `catalog/ingest` 拥有 `scan_candidate_states`：主键 `(root_id,candidate_key)`，列 `release_id/signature/updated_at`，签名约束为 64 位小写十六进制。`release_id` **不加外键**，避免 KEY SHARE 与发行/组锁顺序交叉。
- `SourceObservation.ArtworkRelativePath string`（`artwork_relative_path,omitempty`）记录根内真实封面来源文件：物理/CUE 观察构建时填写，organizer 向 survivor 迁移内嵌封面时与 `ArtworkDeferred` 一起复制，延迟读取使用该路径而非 survivor 自身路径；仅属内部观察合同、不进入 REST，字段按字母序排列（2026-11-08 用户批准补充）。
- `SourceObservation` 增加 `ArtworkDeferred bool`（`artwork_deferred,omitempty`）和 `ParserVersion string`（`parser_version,omitempty`），与现有字段按字母序排列。物理轨记录本文件解析器版本，CUE 虚拟轨记录父音频版本；CUE 虚拟轨本身不进入解析缓存。

#### 缓存与延迟封面

- 缓存值为 `parser.AudioObservation` 去掉 `Artwork` 字节后的 JSON，保留 `ArtworkMIME`；超过 1 MiB 不缓存。缓存只保存解析器原始输出，不能把 organizer 或 CUE 覆盖后的观察写回缓存。
- 增量命中必须同时满足 root/规范相对路径、文件大小、mtime 截到微秒、`parser.CacheVersion(ext)` 相等；未知格式版本为空，不缓存。不做整秒降精度、Unicode 身份合并或内存目录树缓存。
- `ParseCueDocument(path, duration CueDurationReader, containmentRoot ...string)` 的父音频时长由调用方注入（`CueDurationReader func(string) (float64,error)`，直接解析实现为 `parser.AudioDuration`）：scanner 使用同一解析缓存，未命中才解析、计入 `files_parsed` 并写缓存；parser 其他调用方显式传入直接解析的默认实现，不保留旧签名包装，CUE sheet 每轮解析和末轨区间语义不变（2026-11-08 用户批准补充）。
- 物理文件通过过滤、安全校验和 Stat 后才查缓存；CUE sheet 每次重新解析以保留诊断，父音频复用同一缓存。进入目录查询 `LIMIT 2001`，内存至多保留 2000 条；溢出部分及跨目录 CUE 父文件逐条查询。
- 缓存只保存 parser 原始观察，不保存 organizer 推断结果；命中后仍经槽位按序构造并暂存观察，排空后执行 CUE 对齐、碟父目录标记和 organizer。文件名轨号、碟号、艺人每轮重新推断，不因解析缓存冻结；附属目录封面候选在保存阶段按当前过滤规则列举，只有候选需要重写时才读取封面。
- 解析成功在槽位按序应用时于扫描 holder 上 upsert，写 `seen_scan_id=本次`；不等待候选提交，因为缓存只取决于源文件与解析器版本。失败不缓存；命中项应用时登记 seen，按有界批次更新，目录切换及根排空后刷新。
- 遍历结束、未取消且无 `walk_error` 时清理本根 `seen_scan_id IS DISTINCT FROM 本次` 的缓存项，涵盖过滤、排除、删除和读失败来源；取消或目录读失败不清理。缓存读写/清理失败只记 WARN `library.scan.parse_cache_failed`，按未命中处理，不改变扫描完整性，不额外重试或引入替代存储。
- 缓存命中且 MIME 非空时，观察置 `ArtworkDeferred=true`；`StageObservation` 的 scope 内封面去重把 `artwork` 或 `artwork_deferred` 都算封面，后来者同时清除字节、MIME 与 deferred。`EmbeddedArtwork()` 保持原碟序/曲序，接受有字节或 deferred 的观察。
- 保存候选封面时，deferred 观察经路径安全校验后现读音频取封面；读取失败记 `artwork_unreadable`，或已无封面时，继续原文件夹封面查找。受管封面存储/绑定/清理失败仍阻断完整成功。候选被跳过时不触发 deferred 读取。

#### 版本号与升版本规则

- `parser.CommonVersion=2`：⑦ 署名标签补齐是 ④ 合入 `94623b5` 后首次共用解析规则变更，从 1 升到 2，使已有缓存重新解析；`formatVersions` 的 `.flac/.mp3/.ogg/.opus/.wav/.m4a/.ape/.wv/.aif/.aiff` 均保持 1。`CacheVersion(extension)` 返回如 `c2.f1`，未知扩展名返回空串。`artistgraph.Version=4`、`searchindex.ProjectionVersion=2` 不变，不参与解析缓存命中判定。
- `AudioObservation` 字段、类型、含义或共用解析规则（兜底、分隔、多值、歌词、日期以及 parser 内编号推断）改变，升 `CommonVersion`，使所有格式重新解析；只改某格式读取，升该格式版本；新增格式只加版本 1，不使其他格式失效。
- `ingest.GroupingVersion=1`，覆盖同样观察所产生的 Release 图：organizer、folder_metadata、抓轨证据、字段决定、归组证据、署名/艺人、流派、作品、搜索投影字段与封面选择。只改这些规则而不改 parser，不升解析器版本，只升归组版本；升归组版本使所有候选重写而不使解析缓存失效。
- ④ 合入之后的合入核对：O ②/③ 升 `GroupingVersion`（同批合入合计加一）；T ⑥ 升 `CommonVersion` 与 `GroupingVersion`；T ⑦/⑧ 按格式或共用解析改动升解析器版本，并升归组版本；T ⑬ 搜索独立回填不升；M ⑩ 新格式加 1，共用解析变化另升 `CommonVersion`，CUE 回退扩展名表无须另升；M ⑨ MP3 时长规则升 `.mp3` 版本，分析器自有版本另行维护。④ 之前已合入的规则不追补版本。本次⑦仅补 parser 入口与人员表文本形态，未改同样观察的写入规则，`GroupingVersion` 保持 1；候选元数据摘要已包含 Credits（含 detail/source）、艺人证据，成员签名也包含 `ParserVersion`，无需再升候选签名或分组版本。

#### 借用指纹与完整重扫

- `PlanOptions.Mode` 传入模式；仅 `WalkComplete` 且增量模式，才在 `resolveFingerprints` 计算前借用指纹。旧侧为本根 present、已知指纹/大小/mtime、未被任何本轮暂存 `matched_track_id` 引用且未被保住的文件；CUE 取 `COALESCE(cue_parent_relative_path,relative_path)`。
- 新侧为保留、未暂缓、未匹配且无指纹、`fingerprint_path` 非空的暂存观察；按文件名、大小、mtime 配对，两侧各恰好一个不同文件路径才借用。CUE 多轨按父文件去重计数，并将借得指纹用于该父文件的暂存行。文件名改变或任一侧有多个路径，照常计算；不使用目录名或标签推测身份。
- 借用不代替曲目配对：后续仍要求指纹与 CUE 区间两侧唯一，沿用原 UUID 与 Release 严格过半规则。`RootPlan.FingerprintsBorrowed` 汇入 `library.scan.identity_resolved` 的计数，不记录路径。
- `full` 绕过解析缓存读取、候选跳过和指纹借用，仍将成功解析写缓存；保留既有的“同来源身份且 size/mtime 未变则复用已存指纹”。扫描仍走同一协调锁、身份规划、反馈与 missing 流程。HTTP 空请求体或空对象 `{}` 默认增量；可选请求体的严格校验与所有角色 `ScanDTO.mode` 见[扫描协调合同](./scan-coordination-contracts.md)。
- 每根结束 INFO `library.scan.incremental_summary`：`scan_run_id/library_root_id/mode/files_parsed/parse_cache_hits/parse_cache_writes/candidates_written/candidates_skipped/artwork_deferred_loads`。只记标识、模式与计数；`candidates_skipped` 只在未变判定命中且 touch 成功后加一，解析缓存命中本身不算候选跳过。

#### 候选签名与跳过

- `library.candidateSignature(root, candidate) (string,error)` 计算 SHA-256 小写十六进制。行类型为 `g/k/d/m/s/a/l`，同一行字段以 NUL 分隔；输入固定为：
  1. `ingest.GroupingVersion` 与 `CandidateIdentity(root.ID,candidate.Anchor)`；
  2. 按相对路径字节序排列的成员：路径、大小、微秒 UTC mtime、`ParserVersion`；`LoadStagedScope` 从暂存专用列还原大小与时间，不能从不含这两项的 JSON 得到零值；
  3. 成员引用的 CUE sheet 去重排序后的相对路径、Lstat 大小与微秒 UTC mtime；
  4. 共用 `listFolderArtworkCandidates` 顺序列出的全部封面候选相对路径、Lstat 大小与微秒 UTC mtime；
  5. `candidateSourceDirectories()` 各目录内按名字字节序排列的普通 `.log`：目录、名字、Lstat 大小与微秒 UTC mtime；扩展名不区分大小写，不读日志内容，不含符号链接或其它非普通文件。
- `d` 行为 `ingest.CandidateMetadataSignature(candidate) (string,error)`：对候选及其全部成员观察 JSON 作 SHA-256，map 键固定排序、切片保留次序。覆盖 `Artist/AlbumArtist/ArtistTagEvidence`、`Compilation`、`Credits{Role,Name,Detail,Source}`、`Work/MovementName/MovementNumber`、`Year/Lyrics/LyricsSource` 以及全部其他公开观察、字段决定、流派与碟轨位置。仅移除封面字节、`ArtworkDeferred` 和只用于新发行的 `JoinGroupID`；封面来源大小/mtime 仍由文件系统签名覆盖，内容指纹与身份由规划及 `CandidateUnchanged` 校验。摘要失败不跳过；复制成员后归一，不修改原候选。
- `AssignWorks` 仅在候选写入时运行；签名覆盖其作品标签、标题、流派、碟轨顺序输入。未变跳过只 touch，保留已存作品列；任一输入变化就重写作品列，不额外在跳过路径调用作品写入。
- `listFolderArtworkCandidates(rootPath,directories,filter) iter.Seq2[string,error]` 是封面名字规则的唯一入口；不做文件安全检查或内容读取。依次列原目录精确名 → 原目录 front → 附属子目录精确名 → 附属子目录 front，保留原目录次序、精确名单次序、大小写优先级与 `scanFilter`。签名消费全部条目，包括 `Scans/box - front.png`；`discoverFolderArtworkWithReader` 惰性消费到首张可用图片即停，仍逐项安全检查/读取/识别，全部失败才返回首错，后续有效图可消解前错。不能复制另一套名单或提前合并错误而改变首错顺序。
- 列举目录失败（不存在除外）、sheet/封面/日志 Lstat 失败时不跳过，也不记录不完整签名；照常走候选写入与原诊断流程。
- `ingest.CandidateUnchanged(ctx,db,scanID,rootID,candidateKey,signature) (trackIDs []string,ok bool,err error)` 只在以下全部成立时命中：状态签名相同；候选非空且全部已规划、未暂缓的暂存行 `planned_track_id=matched_track_id`、两者非空、`NOT content_changed`、`planned_release_id=state.release_id`；所有曲目当前都是 present 且其 Medium 仍属于状态 Release；曲目已存 `source_size/source_modified_at` 均非 NULL，且本轮已规划观察的 `content_fingerprint` 非 NULL 时，曲目已存指纹也必须非 NULL（`o.content_fingerprint IS NULL OR t.content_fingerprint IS NOT NULL`）。物理轨和 `cue_virtual` 均适用，后者使用父文件事实；无音频负载的退化文件允许指纹为 NULL，不能按 `source_kind` 整类豁免。任何一项不符都重写，不把部分成员当作整个候选命中；治理或其他候选改变曲目归属后必须重写。缺失事实走正常写入回填，不要求标签重新解析：解析缓存只比对自身保存的文件状态与版本，不依赖 `tracks` 的上述字段。
- 保存入口先做版本组规划，再在增量模式下判断跳过。跳过仅 `TouchTracks(ctx,db,trackIDs)` 更新 present 曲目的 `observed_at`，不改 `source_size/source_modified_at`、决定、证据、署名、搜索投影或封面，不读抓轨日志内容；反馈零变化、保存阶段进度照常推进，不触发音频分析失效。touch 失败按候选写入失败处理，禁止完整成功与 missing。
- O 线配合：`PlannedVersionGroup` 从整批暂存规划（包含被跳过候选）读取已有组 ID，供其它新版本加入；`ReleaseGroupOf` 仍在新发行提交后为后续版本补组 ID，不搬动已有发行。跳过不进入候选写入与反馈记账，不改曲目归属；根结束沿用 `EmptiedGroupMoves` 的真实归属差异与旧组腾空判据，不因跳过触发收藏转移。
- `RecordCandidateState(ctx,db,rootID,candidateKey,releaseID,signature)` 只在候选及封面成功后 upsert（`artwork_unreadable` 不视为失败）；完整重扫也记录但不判断跳过。状态读取失败只 WARN 后正常写入，记录失败只 WARN、留待下轮；均不重试。安全事件 `library.scan.candidate_state_failed` 只含扫描/root ID 与 `operation=read/record/cleanup`，不输出来源路径或 SQL。
- `CleanupCandidateStates(ctx,db,scanID,rootID)` 用 `NOT EXISTS scan_observations` 删除本次暂存中未见的候选键；调用方在规划完成、根遍历完整且未取消时执行，再清暂存。取消、`walk_error`、空根保护或规划失败不清；清理失败只 WARN，不改变曲目对账。清理不要求本次所有候选写入成功，仍见到的失败候选保留旧状态。

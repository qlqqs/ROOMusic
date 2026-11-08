# 扫描流程：完整性、身份、CUE、封面与并发解析

从[目录与扫描流程合同](./library-scan-contracts.md)拆出：§3 的扫描完整性、暂存、身份、CUE 与封面，根保存后的专辑收藏转移，并发解析（⑤），以及 M5 ④搜索投影挂接；章节编号沿用原合同。

### 扫描完整性、暂存、身份、CUE 与封面

- 支持扩展名为 `.flac .mp3 .ogg .opus .wav .m4a .ape .wv .aif .aiff`；扩展名只决定尝试，解析器仍核对魔数与结构。
  APE 为 `ape_apev2/ape`；WavPack 为 `wavpack_apev2`，编码区分 `wavpack`、`wavpack_hybrid`、`wavpack_float`、`wavpack_dsd`；
  AIFF/AIFF-C 为 `aiff_id3`，整数 PCM 为 `aiff`，浮点为 `aiff_float`，其他压缩为 `aifc_<压缩类型小写去空白>`。
  扫描保留这些音频参数和标签，不在此拒绝浮点、DSD 或特殊位深；播放白名单与位深关卡另行决定能否交付。
  `wavpack_dsd` 的位深记 1，采样率记原生 DSD 采样率（基础率 × `0x0e` 子块的倍率 × 8），时长为头部总样本数（每个计数打包 8 个 DSD 位）× 8 ÷ 原生采样率；仍只入库、浏览，不播放。
  WavPack 总样本数已知时只读取取得参数所需的首个声道组，有声道信息子块则首块即可停止；未知时才遍历全部块头累计样本数。
- `.aac .dsf .dff .wma .mka .tak` 只进入不支持格式诊断，不解析、不入库；TAK 不再静默忽略。
  扩展名表统一由 `library/scan_filter.go` 维护；不支持格式按下表诊断、计跳过并保住旧来源，不令 root 不完整。
- APE/WavPack 共用 APEv2 页脚标签（可位于 ID3v1 之前），文本多值逐个交给 `setAudioTag`，`TRACK`/`DISC` 映射到位置键；
  前封面只接收 JPEG/PNG，畸形标签整段忽略。AIFF 读取 ID3 块，`NAME`/`AUTH` 只补缺失值；缺标签统一使用文件名兜底。
  三种格式在全部标签读完后、文件名兜底之前统一调用 `finalizeTags`，与 FLAC/MP3 共用专辑艺人、作品/乐章、歌词头部署名和原始年份回退规则；APEv2 的同名作品与署名键直接复用 `setAudioTag`，不另建映射逻辑。

- 扫描只读取 active roots；本地文件或子目录失败按下表诊断并登记保住，不再单独令 root 不完整。只有全根完整的 `succeeded` 才做 missing 对账；成功且 `skipped_files>0` 照常对账。

| 情况 | 诊断 | 跳过数与保住 | root |
| --- | --- | --- | --- |
| 隐藏项、名单、用户排除、非候选扩展名 | 无 | 不计、不保住 | 完整 |
| 候选断链或越界链接 | `broken_or_escaped_link` | 1，file（CUE 为 cue_sheet） | 完整 |
| 候选 Stat 失败 / 非普通文件 | `permission_error` / `non_regular_file` | 1，file（CUE 为 cue_sheet） | 完整 |
| 不支持格式 / 音频解析失败 | `unsupported_format` / `parse_failure` | 1，file | 完整 |
| 子目录读不了 | `walk_error` / “文件夹读不了” | 每目录 1，directory | 完整 |
| CUE 语法或编码失败 | `unsupported_cue` | 每 sheet 1，cue_sheet | 完整 |
| CUE unsafe/unchecked 引用 | `cue_reference` | 每 sheet 1，cue_sheet | 完整 |
| CUE 父音频解析或 Stat 失败 | `parse_failure` / `permission_error` | 每 sheet 1，cue_sheet | 完整 |
| CUE 缺目标、缺 INDEX 01、宽松匹配歧义 | `cue_reference` | 不计、不保住 | 完整 |
| 文件夹封面源文件不安全、为空、超限、格式无效或读不了 | `artwork_unreadable` | 不计、不保住，保留旧封面关系 | 完整 |
| 根不可用或根本身读不了 | `root_unavailable` | 不计 | 不完整 |
| 空根保护触发 | `root_empty` | 不计 | 不完整 |
| 暂存读写清理、规划、候选保存、受管封面存储/绑定/旧 key 清理、数据库或指纹失败 | 原分类 | 不计 | 不完整 |
| 取消、进程重启 | 原合同 | 原合同 | 不做 missing |

- 同一文件或失败 CUE 只登记、计跳过一次，数量不受诊断最多 100 条影响。`scan_runs.skipped_files` 为非负 JS 安全整数或 null，随进度刷新与终态写入；历史 null，不伪造为零。
- 空根保护：遍历未取消，候选数（含不支持格式与 CUE）为零，未遇子目录读取错误、未命中用户排除项，且本根尚有 present 曲目，则记 `root_empty` / “目录是空的，可能还没挂载”，不规划、不保存、不对账。新根空目录不触发；排除了唯一子目录不触发，旧曲目应 missing。
- CUE FILE 精确路径不存在时，先在同目录做 NFC + `strings.EqualFold` 文件名匹配；恰好一普通文件才采用，多匹配为 missing 并记歧义诊断。零匹配再按 `.flac`、`.mp3`、`.ogg`、`.opus`、`.wav`、`.m4a`、`.ape`、`.wv`、`.aif`、`.aiff` 次序（原有顺序不变），以同一比较函数匹配 stem 和扩展名，每扩展名同样要求唯一；遇歧义即停止。目录部分必须逐字节存在，采用后照常做 containment/symlink 检查并改成实际文件名，扫描来源身份不做 Unicode/大小写归一化。
- 缺少 INDEX 01 或确定不存在的父来源只跳过虚拟轨，不伪造父来源或零偏移；失败 sheet 的已有虚拟轨原样保留。
- `0011_scan_staging_and_identity.sql` 增加有界暂存、候选锚点、物理/CUE 身份、音频/CUE 列、字段/归组证据、署名及元数据。
  0010 旧来源身份不兼容（见下方规则）；0032 的列表整理见[多流派合同](./genre-contracts.md)。
- `0012_track_credits_and_release_metadata.sql` 增加 Release `edition`/`label`/`barcode`
  以及 `track_credits`。`0038_credit_roles` 扩为 `(track_id,role,name,detail,source,position)`，逐轨角色固定为
  `lyricist → composer → arranger → conductor → performer → producer`。`detail` 为 NULL 或非空且≤256字节的文本，
  `source` 为 `tag|lyrics`（默认 `tag`）；`UNIQUE NULLS NOT DISTINCT(track_id,role,name,detail)`。
  `CanonicalCredits` 按角色序、细分（空在前）、名字排序，按角色+名字+细分去重；重扫整体替换，不按扫描次数累计。
  down 删除新增角色（含艺人事实、原文证据和搜索值），同轨同角色同名保留 position 最小的一条后删新列并恢复旧约束；有效署名级联删除。
- `0013_track_content_fingerprints.sql` 为 `tracks` 增加 `content_fingerprint`（种类与版本随值保存，≤256 字节）、
  `source_size`、`source_modified_at`，并把 `(source_root_id, relative_path)` 唯一性改为只约束仍持有
  `source_identity` 的行（部分唯一索引），使内容被替换、失去来源身份的旧曲目可与同一路径上的新曲目并存；
  `scan_observations` 增加身份规划列（来源身份、指纹依据、候选键、匹配/计划曲目、换内容/暂缓/已规划标记）。
  `0014_release_anchor_location.sql` 取消 `releases_candidate_anchor_uq`（锚点只是位置信息），并给暂存行增加
  `planned_release_id`。两个迁移都只放宽约束、增加列，不改写已有数据。
- scanner 先把 observation 写入 `scan_observations`（连同来源身份、来源文件大小与修改时间、解析时带出的
  指纹；表的读写归 `catalog/ingest/staging.go`，路径安全校验仍在 library）。遍历结束、未被取消且没有致命错误后，先做
  **身份规划** `ingest.PlanStagedRoot`：逐 scope 有界读取并运行纯 organizer，为保留下来的观察写入候选键；按来源身份
  匹配在库曲目；复用大小与修改时间都没变的已存指纹，其余经 catalog/ingest 定义的 `ingest.Fingerprinter` 端口（library 在注册
  root 内校验后调用 parser）计算；分类为同内容匹配、换内容、未匹配；遍历完整时配对重绑定，不完整时暂缓可疑 scope；
  最后为每个候选分配要沿用的 Release。规划只做 SQL 与读文件，不持有长事务，结果写在暂存行上。随后按 organization
  scope 有界读取，每个 candidate 使用独立短事务按规划写入 Release Graph（只写已规划的观察，被暂缓的 scope 不写入），
  解析失败不阻止其它有效 candidate 提交。无论成功或失败都清理本次 staging；只有全局 `succeeded` 才执行 missing 对账。
- root 遍历开始前的 staging 清理失败必须立即中止该 root，结束时的兜底清理失败必须
  令 root 保持不完整并写入 `staging_write_failure`。每个 scope 最多读取 10,000 条
  observation，单条 JSON 最多 24 MiB，scope 名按 256 条分页遍历；不得退回 root 级
  无界内存 slice。
- **曲目身份**（阶段 I）：曲目 UUID 在同一 root 内随音频内容延续，不再随路径改变。物理 Track 的来源身份是 root +
  规范化相对路径，CUE Track 是 sheet、父来源、track 和 `INDEX 01`，它们只表示来源“此刻在哪里”。内容指纹与标签无关
  （`<种类>-v1:<值>`）：FLAC 用 STREAMINFO 音频 MD5 加样本数与格式（MD5 全零时哈希帧数据），M4A 哈希全部 `mdat`
  负载，MP3 哈希去掉首尾标签后的帧数据，OGG/Opus 哈希头部包之后的音频包，WAV 哈希 `fmt` 与 `data` 块；CUE 虚拟轨
  取父文件指纹，配对键另含起止帧；没有音频负载的退化文件指纹为空（未知），不与其他文件比较。规划规则：
  (1) 曲目先按当前来源身份匹配；两边指纹都已知且不同，视为同一路径换了内容，新内容建新曲目，原曲目失去来源身份，
  没有配对到别处时置为 `missing`，记 `content_replaced`；(2) 只有该 root 遍历完整（未取消，没有根不可用、暂存、指纹或 scope 失败）时，没有匹配到身份的观察才与本 root 中本轮消失（present、有指纹、未被计划沿用且未被保住）的曲目
  按指纹键配对，两侧都恰好一条才沿用 UUID 并更新路径与来源身份，否则不重绑定，歧义键记 `identity_ambiguous`；
  (3) 遍历不完整时不配对，含疑似移动或换内容观察的 scope 整体暂缓写入并记 `identity_deferred`，留给下一次完整遍历，
  其余 scope 照常写入；完整增量遍历还可在计算前按文件名/大小/mtime 两侧唯一借用旧指纹（详见[增量扫描](./library-scan-incremental.md)），不改变配对条件；(4) 跨 root 从不重绑定；相似度、标题、时长从不用于推断身份。写入前先腾出被别的曲目持有的
  来源身份（互换、轮换与处理顺序无关）。重绑定按候选逐个短事务提交，依据的是完整的遍历证据；取消后已提交的保留，没写到
  的候选保持原状。每次候选写入替换 current decisions、grouping evidence、credits 和 track observations，不按重扫次数追加。
- **Release 身份**（阶段 I）：候选已规划沿用的曲目中严格过半当前属于同一个既有 Release 时沿用它；多个候选争同一个
  Release 时重叠多者得，平局时候选键等于该 Release 当前锚点者（留在原位置的一方）优先，再按候选键升序；落选或无倾向的
  候选写入时新建 Release；普通候选另建 ReleaseGroup，`format_split` 新版本按下述同组规则选组。`candidate_anchor` 只记录最近一次的位置，不参与查找、不要求唯一。因此改名、
  移动、增删少量文件、最小来源变化都不换 Release ID；新增文件多于原有（如 2 轨加 3 轨）或整体被替换时新建 Release。
- 身份规划的诊断写入 `scan_diagnostics`（沿用每次扫描最多 100 条的上限，安全相对路径，无绝对路径）：
  `identity_ambiguous`、`content_replaced`、`identity_deferred`、`fingerprint_failure`；每个 root 规划结束记一条聚合
  INFO 日志 `library.scan.identity_resolved`，只含计数（含增量扫描的 `FingerprintsBorrowed`）。
- CUE 引用可以位于 sheet 的父目录或兄弟目录，只要仍在显式注册根 containment 内；
  staging 必须先按规范化 `cue_parent_relative_path` 与物理 observation 对齐 scope，
  organizer 再仅在同一 candidate 和明确父子关系内消除虚拟/物理重复。不得仅按 sheet
  所在目录、标题或时长模糊去重。扫描前使用解析 symlink 后的 `Stat` 验证媒体节点为
  regular file，FIFO、设备和逃逸链接不得传给 parser。
- parser 默认的 track/disc `1` 必须带 inferred 标记；非合成候选的碟号依次取标签、CUE、`album_title`、
  `folder_structure`、`filename_fallback`、默认 1；合成多碟的介质位置一律按目录，显式标签原证据不覆盖。轨号优先取标签（含 CUE），
  再取经同介质冲突保护的 `filename_fallback`，最后以自然序补最小未占用号（`path_order_fallback`）。CUE sheet 与
  track PERFORMER 的 provenance 分别为 `cue_sheet` 和 `cue_track`，构造每条虚拟 Track
  时复制 `InferredFields`/`FieldSources` map。单轨 CUE 合并到物理观察时，仅 `cue.TrackNumber > 0` 且不是 inferred、
  同时物理轨号缺失或 inferred，才覆盖为 CUE 轨号、清除该字段 inferred 并记来源 `cue_track`；物理显式标签优先，虚拟 CUE 不变。
- **已知边界**：CUE 虚拟轨不继承父音频的 `Credits` / `Compilation`；本次合并保留该既有行为，不改变继承规则。

### 根保存后的专辑收藏转移

- 迁移 `0037_scan_observation_previous_release.sql` 增加暂存列 `previous_release_id UUID`，无外键/索引，down 删除列；回退测试表使用版本 37。`StageObservation` 冲突更新清空该列，身份规划在 `allocateReleases` 后按沿用曲目保存原发行。
- 本根保存后、已规划且未取消时用原连接开短事务：`ingest.LockProjectionWrites`（CATP）→ `ingest.EmptiedGroupMoves(ctx,db,scanID,rootID)` → 注入的 `AlbumFavoriteMover` → 提交；nil mover 不执行，空映射直接提交。
- 按实际移动计算：原组所有发行均无任何曲目（包括 missing）才转移；按原组汇总各接收 Release 的移入数，最多者胜，平局按接收发行 created_at、ID 升序。仅转专辑收藏，不搬修正/认领/默认/歌单/历史，不删旧组/发行。
- `SetAlbumFavoriteMover` 由 app 注入 `personalService.MoveAlbumFavorites`；收藏去重、保留时间与名称、revision 规则见[个人合同](./personal-contracts.md)。失败回滚转移事务，只记 Warn `library.scan.favorite_transfer_failed`（仅扫描 ID），不影响扫描完整性或已保存候选。写完根后、转移前取消不补偿，下次扫描也不会重放已经消失的移动证据。

### 并发解析（⑤）

- `library/scan_pipeline.go` 将候选、过滤和失败条目放入有序槽位；缓存命中直接就绪，只有未命中音频进入 `platform/workpool.Ordered`。窗口为 `2 × workers`（默认 8），窗口满时等待头部；结果只能在遍历 goroutine 按 WalkDir 次序应用。构造观察、缓存写入、暂存、诊断、HoldSource、跳过与 processed 计数都不在 worker 中执行；诊断 100 条截断及暂存顺序与并发 1 一致。
- `NewOrdered[T](ctx, workers, window, apply)` 要求正数并发与窗口；`Submit(func(context.Context) T)` / `Ready(T)` 排队并应用已就绪头部；`Flush()` 应用当前队列但保留 worker，`Drain()` 应用后关闭，`Close()` 取消并等待、不应用。调用端串行使用；apply 首错停止流水线，取消丢弃未应用结果，等待已启动解析返回，不捕获 panic、不重试。解析器不接收 context，关闭等待当前文件读取结束；单文件内存有界不等于读取耗时有界。
- 缓存查询仍在持锁的遍历 goroutine；后续条目引用尚在途的同一规范父音频路径时，先 `Flush()` 应用早先的缓存写入再查缓存，不能以 worker 完成快慢决定命中。缓存成功、失败与 full 模式沿用④，不引入根级结果缓存。
- `parser.ParseCueSheet(path, containmentRoot ...string) (CueDocument,error)` 只读 sheet、校验引用；每张 sheet 的唯一父音频先查缓存，未命中项在一个任务内读取。应用阶段调用 `ResolveCueDurations(document CueDocument, duration CueDurationReader) (CueDocument,error)` 一次，按原 FILE 标识完成区间、诊断和元数据；既有同步 `ParseCueDocument` 依次调用这两阶段，返回值不保留临时 FILE 标识。CUE 父事实仍受单 sheet 的 FILE 上限约束，不能把每槽位理解为固定只有一张封面。
- `PlanOptions.Concurrency int` 小于等于零按 1；`resolveFingerprints` 每页最多 100 个路径，用 `workpool.Map(ctx, workers, items, fn)` 并行只读，再按输入路径顺序写回与记 `fingerprint_failure`。Fingerprinter（包括测试桩）必须允许并发调用。SQL 仍用同一 holder、原 `WithoutCancel` 合同；不增加写入者或并行根。
- OGG/Opus 标签入口逐页逐段读取，只保留固定页头/段缓冲和当前最多 16 MiB 的包，音频包即时入哈希；独立 `oggFingerprint` 同样流式，不能按文件大小分配。标签、音频字节哈希、静态输入格式错误顺序保持；原 `DurationSeconds=0` 保持，不新增 granule 时长语义。流式读取不保证复现“前段格式错误与后段 I/O 错误并存”时整文件预读的底层错误优先级，读取失败仍失败。
- 内存估算只针对普通音频和少量 FILE 的常见 CUE，不是全局硬上限。重复 OGG 注释包仍可累积元数据，极多 FILE 的 CUE 仍聚合父事实；2026-11-08 用户裁决为“已知局限，接受”，保留旧接受范围，不新增累计预算、不截断、不改变 CUE 聚合。
- 配置与 app 装配见运行合同：正式服务默认 4；直接构造的零值 Service 与零值 PlanOptions 仍串行。设为 1 只改变读盘并发，不改变来源、缓存或反馈语义。未增加迁移、REST 字段或解析器版本变化。

## M5 ④搜索投影挂接

扫描候选及修正/清除/撤销/claim 双方在同事务刷新目标代次搜索投影；CATP 先于业务行锁，失败回滚，后台持锁重读避免覆盖新值。扫描不写治理表，missing 可见性读取实时过滤。详见[搜索合同](./search-contracts.md)。

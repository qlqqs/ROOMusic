# 目录与扫描流程合同

## 1. 范围与触发

目录生命周期（含改路径保留 ID）、幂等及扫描（遍历、暂存、身份规划、Release Graph、CUE、抓轨、署名与封面）遵守本合同。扫描状态、持久化取消与跨进程协调见
[扫描协调合同](./scan-coordination-contracts.md)；发行读取接口见 [Catalog REST 合同](./catalog-rest-contracts.md)；内嵌歌词的提取与保存见[歌词合同](./lyrics-contracts.md)；
通用请求与响应、迁移执行器与 CI 见 [Core 0 当前运行合同](./core0-runtime-contracts.md)。

M5 新增全根预计数、逐根加权进度与事务内变更记账；候选/封面/终态 missing 均遵循“全部既有发行升序锁 → 独立重读 → 组升序共享锁”，不得持组补发行锁。反馈锁失败降级不阻断扫描，详见 [扫描反馈合同](./scan-feedback-contracts.md)。

## 2. 接口签名

- 目录新增响应：`{ "id": string, "name": string, "status": "active" | "disabled", "revision": integer }`。
- 目录列表项目：`id`、安全 basename `path`/`name`、`status`、`revision`、时间戳与必需的 `excluded_paths: string[]`（无排除时 `[]`，相对路径按字节升序）；
  不返回原始绝对路径。
- `POST /api/v1/library-roots/{id}/relocate`：同源、管理员、必需 `Idempotency-Key`；请求为
  `{"path":"/music/nas-new/Music","expected_revision":4}`，成功返回 `200`。
  响应严格为以下六字段，不附带目录列表的时间戳；`name`/`path` 都是安全 basename，`excluded_paths` 为原样保留的相对路径数组（无排除时 `[]`）：

```json
{"id":"uuid","name":"Music","path":"Music","status":"active","revision":5,"excluded_paths":[]}
```

## 3. 合同

### 目录新增与操作幂等

- 目录新增对已存在路径执行幂等 upsert，但不隐式恢复 disabled 状态；响应和
  Operation Journal 使用数据库真实 `status`/`revision`。
- 目录新增、停用、恢复、改路径及排除项增删必须带 `Idempotency-Key`，缺失返回 `400 invalid_input`。键的作用域是
  actor 加操作类型；请求指纹包含目标身份：新增为规范化路径，停用/恢复为小写路由 root ID 加
  请求参数（`expected_revision` 等），改路径为 `{"root_id":<小写 ID>,"path":<ValidatePath 后的路径>,"expected_revision":n}`。这些入口共用 `beginRootOperation`：先开启 READ COMMITTED
  事务并取得该键的事务级 advisory lock，再在事务内查记录，因此同键请求串行执行。
  指纹一致时返回首次的状态码和响应（新增 `201`，其余 `200`），不重复执行；指纹不一致时返回
  `409 idempotency_conflict` 且无副作用；同键并发只执行一次，其余请求回放首次记录。
  路由 root ID 格式错误返回 `400 invalid_id`，不进入幂等流程。只有成功的操作写记录，失败的
  尝试不占用幂等键。2026-09-29 起指纹含目标；此前记录的停用/恢复键不做兼容，升级后重试
  返回 `409 idempotency_conflict`（开发阶段裁决，见 F2 任务）。
- 改路径操作类型为 `relocate`，复用 `library.ValidatePath`（Abs + Clean、真实目录、允许根内、任一层不得为符号链接）。处理顺序固定为：
  同源 → 管理员 → 路由 ID → 幂等键 → 解码/参数校验 → `ValidatePath` → `beginRootOperation`（键锁及回放检查）
  → `library.TryLockScanCoordination(ctx, tx)` → `SELECT … FOR UPDATE` 锁目录行 → revision 校验 → 新路径冲突检查 → 更新与记录 → 提交。
  回放先于扫描锁探测，不因后来开始的扫描改报 `scan_running`；入口参数与路径校验仍先执行。
- 改路径允许 `active` 和 `disabled`，保持当前状态；新路径已被其他根登记（包括 disabled）时拒绝，不额外限制不同根之间的路径嵌套。
  路径实际变化时只更新根路径、revision 加一及更新时间，不改 root ID；规范化后等于当前路径时仍校验 revision、取得扫描锁，返回当前 DTO、revision 不变，照常写成功记录。
- 扫描互斥使用与执行器同键的事务级 advisory lock，未取得时 `409 scan_running`，不写记录、不占用幂等键；取得后持有至提交或回滚。
  禁止只查活动扫描行或进程内状态，避免检查后扫描读到旧路径；helper 与常量归属见[扫描协调合同](./scan-coordination-contracts.md)。
- 改路径成功收据与路径更新在同一事务；`expected_revision` 为请求版本，`result_revision` 为结果版本。
  `before_state` / `after_state` 分别存 `{"path":<旧/新绝对路径>,"revision":n}`，仅留在数据库内，不由任何接口返回。
  回放 `response` 只存上述安全六字段；目录响应、操作记录列表及错误不得返回绝对路径或前后态。
- 恢复仍先校验当前目录为 `disabled` 且 `expected_revision` 相符；按目录取最近一条成功的 `disable` 记录，并要求其 `before_state.status='active'`。
  不再要求 `result_revision = 当前 revision` 或 `before_state.revision = 当前 revision - 1`，所以“停用 → 改路径 → 恢复”合法。
  恢复只将状态改回 `active`、当前 revision 加一，保留新路径和排除项，不恢复旧路径或旧版本；没有有效停用前态仍为 `409 revision_conflict`。
- 改路径不迁移来源身份、曲目、Release、收藏、历史、歌单或修正；根 ID 与相对路径不变时来源身份不变。
  新位置内容相同，下一次扫描沿用曲目 UUID 与 Release UUID；内容不同沿用现有换内容、移动配对及 missing 规则。相对排除项不改写。
  前端在改路径提交成功后另行调用 `POST /api/v1/scans`，新扫描返回 `202`、已有扫描返回 `200`；改路径事务本身不启动扫描。
- `0035_root_relocate.sql`（合入时重新核对编号）只放宽 `library_root_operations_operation_type_check`，允许
  `create / disable / restore / exclude / unexclude / relocate`，不回填或迁移业务身份数据。
  `0035_root_relocate.down.sql` 先删除 `operation_type='relocate'` 的记录，再恢复不含 `relocate` 的原五类约束。
  下行会丢弃改路径收据，但不会把 `library_roots.path` 改回旧值，也不回退 revision；不提供操作记录撤销入口，改回位置须再次调用改路径接口。

### 路径过滤

- `library/scan_filter.go` 拥有 `newScanFilter(exclusions []string)`、`skipDir(relativeDir, name string)`、`skipFile(name string)` 和现有扩展名表。每根构造一次，预计数与正式遍历共用该过滤；`relativeDir` 是当前目录的根内相对路径。只看注册根之后的段，注册根自身不参与过滤。
- 任一根内段以 `.` 开头就忽略；目录名按 `strings.EqualFold` 整段匹配 `@eaDir`、`#recycle`、`@Recycle`、`@Recently-Snapshot`、`#snapshot`、`$RECYCLE.BIN`、`System Volume Information`、`lost+found`、`__MACOSX`、`@appmeta`、`@team`。名单只作用于目录；不做子串匹配，`my.recycle`、`recycle`、`Music@eaDir2` 不命中。
- 用户排除按 `/` 分隔的根内相对目录路径逐字节相等。命中目录用 `fs.SkipDir` 跳过整棵子树；隐藏文件直接跳过。过滤不记诊断、不计预计数或跳过数，也不登记保住；该范围旧来源在成功扫描后正常标 missing。
- 文件按“过滤 → 候选扩展名（含 CUE）→ 链接解析与 containment → Stat/普通文件 → 解析”处理；非候选附件及其断链直接忽略。

### 排除子文件夹

- `POST /api/v1/library-roots/{id}/exclusions` 接收 `{"path":"/music/Artist/Extras"}`；必须是 `ValidatePath` 接受的真实子目录，等于登记根也拒绝。存 `filepath.Rel` 的 `/` 形式，最多 4096 字节。
- `DELETE /api/v1/library-roots/{id}/exclusions?path=<URL 编码相对路径>`；只校验非空、非绝对、不含 `..` 且规范，不要求目录仍存在。两者同源、管理员、必需 `Idempotency-Key`，指纹含小写 root ID 和请求 path；操作类型 `exclude` / `unexclude`，沿用 `beginRootOperation`。
- 都返回 `200` 的当前目录列表项（含 `excluded_paths`）。添加已有项、删除不存在项同样成功。每根最多 100 项，超过为 `409 exclusion_limit` / “不扫描的文件夹太多了”；先锁 root，再独立读取集合，防止并发越限。
- 不改 root 的 revision；收据 `expected_revision` 为空，`result_revision` 为当前版本，`after_state={"relative_path":…}`。列表与扫描根快照用一条 LEFT JOIN 聚合读排除项，不逐根查；保存后下一次扫描生效。
- 非法 ID 为 `400 invalid_id`；未知根 `404 not_found`；添加路径失败 `400 invalid_library_path` / “打不开这个文件夹”，删除格式失败 `400 invalid_input`；数据库失败 `503 database_unavailable`。目录内浏览见[目录浏览合同](./library-folder-contracts.md)。

### 保住登记

- `catalog/ingest/held_sources.go` 拥有 `HoldSource(ctx, db, scanID, rootID, kind, relativePath)`、`TouchHeldSources(ctx, db, scanID, rootID) (int64,error)`、`RootHasPresentTracks(ctx, db, rootID) (bool,error)`。
- `scan_held_sources` 以 scan/root/kind/path 唯一登记失败范围：`file` 使用现有入库的 `ingest.NormalizedPath` 规范后精确匹配 `tracks.relative_path`（不做 Unicode 或大小写合并）；`directory` 用 `left(t.relative_path,length(h.relative_path)+1)=h.relative_path||'/'` 匹配子树（含 CUE 虚拟路径）；`cue_sheet` 精确匹配 `tracks.cue_sheet_path`。三类共用 SQL 谓词，同时用于 `vanishedTracksCTE` 排除与 touch，保住曲目不被其它同指纹文件抢走 UUID。
- 候选保存之后、暂存清理之前且未取消时，touch 只把命中的 present 曲目的 `observed_at` 更新为 NOW()，不改事实、状态、指纹或搜索投影，不恢复原本 missing 的曲目；观察时间不算报告更新。来源读取失败不缓存，下轮自然重读。
- Hold 写失败为 `staging_write_failure`；touch 失败为 `catalog_write_failure`，root 不完整。`ClearStagedObservations` 与 `ClearScansStaging` 同时清理 observation 和 held；取消与恢复继续沿用原协调合同。
- 迁移暂定 `0034_scan_file_filter.sql` / `.down.sql`：增加 `library_root_exclusions`、`scan_held_sources`、可空 `scan_runs.skipped_files` 和操作类型；历史不回填。下行先删除新操作类型的记录，再收回约束并删除两表一列，合入时统一编号。

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
  以及 `track_credits`。0034 扩为 `(track_id,role,name,detail,source,position)`，逐轨角色固定为
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
  其余 scope 照常写入；完整增量遍历还可在计算前按文件名/大小/mtime 两侧唯一借用旧指纹（详见“增量扫描”），不改变配对条件；(4) 跨 root 从不重绑定；相似度、标题、时长从不用于推断身份。写入前先腾出被别的曲目持有的
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

### 标签多值、署名与合辑（⑥⑦）

- 曲目艺人、专辑艺人及六类曲目署名共用 `metadata.CanonicalArtistNames`：
  先按 `\r\n`、`\n`、`\r`、NUL 切段，各段完整重走规则，再删其余控制字符与首尾空白。明确分隔符
  `; `、`；`、` / `、` feat. `、` ft. `、` with `、`、` 区分大小写；` & ` 两侧各含空格时先拆。
  随后保护固定组合 `Simon & Garfunkel`、`Earth, Wind & Fire`、`AC/DC` 与拉丁“姓, 名”，最后依次拆紧凑 `/`、`,`、`&`；嵌套段也遵守姓名保护。
  “姓, 名”须恰有一个逗号，两侧各有拉丁字母，仅含拉丁字母、组合附加符、空格及 `. - ' ’`，左侧至多3词、右侧至多4词；
  各词小写后不得以前缀 `orchest/philharm/symphon/sinfon/ensemble/quartet/quatuor/trio/choir/chorus/chor/consort/singers/players/band` 开头。
  其余逗号照拆，`Tom & Jerry` 照拆；`Queen, David Bowie` 整体保留是已接受局限。
  按原写法精确去重，曲目展示排序后以 ` / ` 连接；不按别名改写事实、不猜人。
  解析署名先拆原文、再逐名 `BoundedText`，不得先删除换行。原始证据与人工意图见[艺人合同](./artist-contracts.md)。
- `metadata.JoinArtistValues(values ...string) string` 只展开换行/NUL，逐值 `BoundedText`、丢空、原写法去重并保留首次顺序，以 ` / ` 拼接。
  专辑艺人在 `finalizeTags` 中按级取首个非空级的全部值，之后才调用 `applyFilenameFallback`：
  Vorbis 为 `ALBUMARTIST → ALBUM_ARTIST → ALBUM ARTIST`；ID3 为 `TPE2 → TXXX:ALBUMARTIST → TXXX:ALBUM_ARTIST/TXXX:ALBUM ARTIST`；
  M4A 为 `aART → ----:ALBUMARTIST → ----:ALBUM_ARTIST/----:ALBUM ARTIST`。同级合并，全部键记入 `album_artist` 原文证据。
  ID3 文本帧保留全部 NUL 值；TXXX 按编码解出描述与值，描述去空白转大写后交给通用标签入口。
- **署名角色表**：`metadata.Credit{Role,Name,Detail,Source}` 的 `detail/source` 随暂存 JSON 保存（空 source 等于 tag）。
  `MapCreditRole` 与 `MapInstrument` 的表版本为 `CreditRoleTableVersion=1`；匹配先去首尾空白、小写、压连续空白，再去结尾 ` by`，不做相似度匹配。
  作词/剧本归 lyricist，作曲归 composer，编曲/配器归 arranger，指挥归 conductor，制作人/监制归 producer；
  `词曲/songwriter/writer` 等一人双产出 lyricist 与 composer。混音、母带、录音、重混、DJ混音、编程归 producer，
  detail 分别为 `mix/mastering/recording/remix/dj_mix/programming`；剧本为 `libretto`，声乐/和声及乐器归 performer。
  乐器写法和别名只取 `metadata/credit_roles.go` 的小表，不搬外部大表、不展开方括号修饰。
- **署名来源**：Vorbis、M4A `com.apple.iTunes` freeform、ID3 TXXX 支持
  `COMPOSER/LYRICIST/WRITER/ARRANGER/CONDUCTOR/PRODUCER/MIXER/ENGINEER/REMIXER/MIXARTIST/DJMIXER/PERFORMER/PERFORMERS/SOLOIST/SOLOISTS/ENSEMBLE/ORCHESTRA/LIBRETTIST`；
  `ENSEMBLE/ORCHESTRA` 为 performer + orchestra，未知键忽略。ID3 `TCOM/TEXT/TPE3` 分别为作曲/作词/指挥，M4A `©wrt` 为作曲。
  `TIPL/IPLS` 按 NUL 成对读取工种、人名；`TMCL` 按 NUL 成对读取乐器、人名，落单尾值丢弃。
  未知 TIPL/IPLS 工种归 producer，未知 TMCL 乐器归 performer，detail 保留去首尾空白后的原写法（≤256字节）。
  署名键原文照常记艺人证据，多角色取第一个；成对入口的 tag_key 为 `TIPL:<工种>`、`IPLS:<工种>`、`TMCL:<乐器>`（≤128字节），歌词不写标签证据。
- **演奏括号**：`PERFORMER(S)/SOLOIST(S)` 先用 `metadata.SplitExplicitArtistValues` 按换行/NUL及⑥明确分隔符列表切段（与 `artist.go` 共用来源），再逐段解析末尾中英文圆括号，最后对拆出的名字执行完整姓名拆分。第1步按中英文圆括号 `()`/`（）` 的深度只切括号外的明确分隔符，括号内内容完整交给第2步按逗号、顿号、` & `、` and ` 拆乐器或角色；不使用占位符、不扩展多段括号语法，TMCL 成对入口不变。括号内按原乐器规则切段，
  先查乐器表，再查角色表；未知且只含拉丁字母、空格、连字符时保留为 performer 的原写法细分。
  任一段未知且含其他文字则整串保留为姓名（如 `测试艺人（测试别名）`）；成功拆出的人名再按共用姓名规则拆分。
- **歌词开头署名**：全部标签读完、歌词规范化后在 `finalizeTags` 读取最多前30行，不改变歌词原文或显示。
  每行去行首连续 LRC 方括号标签及首尾空白，空行/纯标签行跳过；其余须为1–16字符标签加中英文冒号和值。
  署名块开始前最多跳过一行媒体题头作为元信息：去标签及首尾空白后，行等于解析出的非空 `Title`，或以 `Title + 空白 + - + 空白` 开头（也接受 `–`、`—`，标题比较忽略大小写）；`Title` 为空不跳过，不使用文件名补位，已读到署名行后不再跳过题头，其它非署名行仍结束署名块。
  标签按 `/、&` 拆子标签，先整体查角色/乐器表，未命中再按连续中文段、拉丁段查表；至少一项命中才是署名行，首次非署名行结束整个块。
  所有命中项均产出；没有细分的角色 detail 为空，带细分的工种与乐器保留命中子标签原文（如“混音工程师”“Guitar”），不改成标签来源的规范键。
  名字按共用规则拆分并去掉 `@` 起的团体/工作室后缀（去后为空则保留原文），`Source="lyrics"`；某角色已有 tag 署名时，该角色全部歌词署名丢弃。
  ④ 首次合入以全部 M/O/T 规则为版本 1 基线；从尚无解析缓存的 main 升级后首扫重新解析。艺人图/搜索投影版本为4/2，不改 textnorm.Version。
- `AudioObservation.Compilation bool` 经 scanner 原样传给 `SourceObservation.Compilation`（JSON `compilation,omitempty`）。
  Vorbis 的 `COMPILATION/ITUNESCOMPILATION`、ID3 的 `TCMP/TXXX:COMPILATION`、M4A 的 `----:COMPILATION` 去空白、小写后任一为
  `1/yes/true/compilation` 即为真；M4A `cpil` 为单字节非零整数时为真，0为假。
  organizer 保留真实专辑艺人；否则发行艺人是合辑占位词或非 `folder` 观察的标志严格过半时，决定 `artist="群星"`、清除专辑艺人决定，
  保留原 artist 候选，写 `source=compilation`、`rule_id=compilation_v1`、`confidence=high`、`action=auto_apply`。不改变 partition/分组。
- 本版 `Year==0` 时才按 `ORIGINALDATE → ORIGINALYEAR → TDOR → TORY → TXXX:ORIGINALDATE → TXXX:ORIGINALYEAR → ----:ORIGINALDATE → ----:ORIGINALYEAR`
  取首个可解析年份；不另存原始年份字段。流派仍遵守[多流派合同](./genre-contracts.md)，不随艺人规则改变。

### 发行候选、抓轨与封面

- 缺失 `AlbumArtist` 表示未知，不是与唯一明确值冲突：相同权威 album 下只有一个明确
  `AlbumArtist` 时，缺失值与该 partition 兼容；出现多个明确值时仍按多数决或稳定拆分。
  生成 Release artist/credit 时，只要存在明确 `AlbumArtist`，就先在这些值之间决策，
  不允许多数 Track Artist 覆盖它。根目录 staging scope 先按权威 album 聚合，再由
  organizer 决定 album artist partition，避免在决策前永久拆散兼容 observation。
- `.log` 抓轨证据只读取候选来源目录中普通、非 symlink 文件的前 64 KiB；只有明确
  EAC/XLD 产品签名才补 `source_type=CD`、`media_type=CD`。它可以替换此前仅由
  `folder` 产生的补充值和 provenance，但不得覆盖 tag、CUE、音频规格或其它明确来源；
  替换后每个字段仍只能有一条 current decision。field uncertainty、grouping inconsistency 和可见 missing 来源
  在 `attention_count` 中按语义去重；同目录拆分使用独立
  `same_directory_conflict` reason。
- 封面只在 Release ID 确定后写入 ROOMusic 管理目录并绑定；读取、校验或数据库绑定
  失败产生独立诊断，不修改源音乐或源封面。命名 folder artwork 存在但不安全、为空、
  超限或格式无效时必须 fail closed 并保留原关系，不能当作“来源已删除”。复用由 hash
  命名的受管文件前核对实际字节，损坏时原子替换；数据库查询、绑定失败后的新文件清理、
  旧 key 引用检查和文件删除错误都必须向上返回，使扫描进入 `incomplete`；发现阶段错误包装 `errArtworkSource`，只记 `artwork_unreadable`，保留原关系并继续。
- 封面来源与文件夹封面发现（F4，2026-10-03）：
  - 内嵌封面优先：按碟序、碟内按位置取第一首带内嵌封面的曲目；没有才找文件夹封面。
  - 文件夹封面按目录顺序查找（`ingest.OrganizedCandidate.ArtworkDirectories`，相对注册根）：先是发行目录 `Anchor.Scope`，
    再按碟序、曲序列出各曲目观察的 `Directory`，去重。CUE 虚拟轨的 `Directory` 是 sheet 所在目录，不能从虚拟路径
    `sheet.cue#track-N@parent#index-M` 用 `filepath.Dir` 推出。严格多碟专辑的发行目录是各碟的父目录，所以顺序是
    `A` → `A/CD1` → `A/CD2`；Box 叶子只查自己的目录，不查 Box 父目录；CUE 的发行目录是 sheet 目录，不查跨目录的音频目录。
    查找不会越出发行自己的目录，避免把盒装或公共目录里的封面绑给每张专辑。
  - 查找分四轮，前一轮命中即返回：①原目录精确名单；②原目录中基名包含 `front` 的图片；③附属子目录精确名单；④附属子目录中基名包含 `front` 的图片。
    原目录顺序不变；每目录只读一次。精确名单仍为 `cover.{jpg,jpeg,png,webp}`、`folder.{jpg,jpeg,png}`、`front.{jpg,jpeg,png}`，
    不区分大小写，同名变体全小写优先，其余字节序。包含规则只认 `jpg/jpeg/png/webp`，按文件名字节序。
    附属目录仅限直接子目录 `scans/artwork/art/covers`（不区分大小写），按原目录顺序、子目录名字节序，不递归、不跟随目录符号链接，不含 `extras` 或中文名。
    第二至第四轮的包含式文件查找与附属目录发现复用该根的 `registeredRoot.filter`：目录按根内相对路径和目录名调用 `skipDir`，文件调用 `skipFile`；不进入用户排除、隐藏或系统目录，不采用 `.front.jpg` 等被过滤文件，不另建过滤规则。四轮优先级与注册根自身不参与过滤的约定不变。
  - 每个命名文件仍要通过安全检查：不是符号链接、是普通文件、大小 1 字节到 16 MiB、读取成功、`artwork.ValidateImage` 复用受管存储的大小、格式与尺寸校验。
    无效的命名文件记下第一个错误后继续找；全部目录找完仍没有可用封面时才返回 `errArtworkSource` 包装的错误（扫描诊断 `artwork_unreadable`）。
    目录不存在不算错误，其他读目录错误按同样方式记录。
  - 没有迁移：重扫会对每个候选重新发现封面，下一次扫描即绑定或移除。

### 碟号、独占合成与介质标题

- `ingest.parseDiscMarker(name) (discMarker,bool)` 输出 `Number/Prefix/Suffix/AtStart`：从左向右取首个有效 `disc/disk/cd`（忽略大小写），支持括号、`CD(1)`、`of N`/`/N`；非 ASCII 单词边界，编号限 1–99，认整数、英文 one–twenty、规范罗马数字。无效编号继续向右找；纯数字目录、中文碟号、`[CD]`、`{…, CD}`、`BACD-0001` 不认。
- Prefix/Suffix 去空白、分隔符与落单括号，保留配对括号。`stripAlbumDiscMarker` 在标记开头时取 Suffix，否则取 Prefix（截断尾部）；结果空则不剥。只改 `albumPartition` 和 Release 标题比较值，不改 observation 的原始 album。物理观察缺少权威碟号时补剥出的编号，来源 `album_title`，仍 inferred；不改 parser。
- 碟目录仍进 `disc-parent:<父>` scope。顺序必须是 `AlignCueStagingScopes` → `MarkDiscParentScopes(ctx,db,scanID,rootID)` → `PlanStagedRoot` → 保存。标记把 `disc_parent_exclusive:true` 写入 JSON（`SourceObservation.DiscParentExclusive`）：非空父目录整棵子树没有其它 scope 的音频观察才独占，包含父目录直接音频和碟目录更深层音频；只有图片不影响。本次 scan/root 的 `scan_held_sources`（`file`、`directory`、`cue_sheet`）也参与判断：父目录子树内不属于这些碟目录本身的保住来源或未知目录范围阻止独占，不因未成功暂存而被忽略；碟目录更深层的范围仍属于其它目录，不能按整棵碟目录子树豁免。CUE 对齐在暂存 JSON 中保留 `original_organization_scope`（重复对齐不覆盖）；只有原 scope 与当前 scope 都属于该碟父目录、且 held 所在目录与观察目录精确相等时才豁免，避免跨目录 CUE 的物理父来源把普通目录伪装成碟目录。比较路径按段边界使用 `left` / 相等判断，不用需转义的 LIKE。失败记 `staging_write_failure`，根不完整。
- 不独占或父为注册根时，仅 Prefix/Suffix 均空的目录走原严格多碟：≥2 个不同编号、每号一个目录、1..max 连续、兼容权威 partition ≤1。其它各自 `box_leaf`，不查父目录封面；仅一个碟目录且无权威 album 时可用 Prefix 兜底（`disc_dir_prefix_fallback_v1`）。
- 独占时，权威 partition 冲突或非附加碟 Prefix（小写、压空白）不止一种，全部 `box_leaf`。附加碟按 Prefix/Suffix 中 Bonus/Extra/Special 或特典等判定；全是附加碟则全部当正碟。正碟重号时仅在无 Suffix 的正碟非空且无重号时把其它碟改作附加碟，否则不合成。
- 合成条件：正碟 1..max 连续且 max≥2，或一个正碟加附加碟，或整个父目录只有一碟；缺号时要求每个碟目录至少一条权威 album 且 partition 唯一。正碟保留目录号；附加碟按规范化 Suffix（空则 Prefix）、编号、路径排序，从正碟最大号后顺排。
- 合成候选仍为 `strict_multidisc`，Scope 为父目录，Partition 为空；无权威 album 时用唯一 Prefix 或父目录名兜底（`multidisc_parent_fallback_v1`）。字段证据仍取首个物理碟目录，不取父目录；独占合成、严格多碟和 `box_leaf` 的碟目录证据统一只解析 `parseDiscMarker` 的 Prefix，Prefix 为空不产生目录证据，非碟目录不变。`OrganizedCandidate.MediumTitles map[int]string`：标题取 Suffix，附加碟空时取 Prefix；去包裹整名的括号；`PersistCandidate` 新增或更新 `media.title`，无碟名恢复 `Medium`。REST 形状不变，候选键仍 `v2`、归组证据仍 `organizer_v2`。

- 归组版本：④ 尚未进入 main，本次整合②③后 `GroupingVersion` 仍从 1 起；后续已发布规则变化按下方升版本合同执行。候选签名与封面发现共用四轮候选列举。

### 文件名推断与同目录归组（③）

- 规则由 `catalog/ingest` 拥有，不改 parser 或 `SourceObservation` 结构。`OrganizeObservations` 在 CUE 分组解析、专辑名碟号预处理之后，按同一 scope 内的物理 `Directory` 推断文件名；CUE 虚拟轨不参与文件名预处理或目录多数计数。推断证据保留供连续性检验，主字段仅填 inferred 的缺失值。
- 文件名去扩展名后的 stem 按以下五级依次尝试，只有得到正轨号才命中，零值视为没有：
  1. 去首尾空白后为 1–2 位纯数字，直接作轨号。
  2. `^\s*(?:(\d{1,2})[\s_\-\.]+)?(\d{1,2})(?:\D.*)?$`：可选第 1 组为碟、第 2 组为轨；也允许只有前导轨号。
  3. 跳过前导空白，连续数字恰好 3 位拆为 1 位碟 + 2 位轨；恰好 4 位且首位为 `0` 才拆为 2 位碟 + 2 位轨。`1999`、`2046` 这类年份不拆。
  4. 去尾部空白后末尾独立的 1–2 位数字，前一个字符不能是数字，不把更长数字截成两位。
  5. 不区分大小写的 `(?:^|[^a-z])track[\s_.\-]*(\d{1,2})(?:\D|$)`，允许 `Track 07`。
- 第 2 级带碟号、第 3 级的三位与四位分别计为三种写法；至少 2 个文件、且超过目录物理文件的一半使用同一种写法才采信。不满足时，第 2 级降为第 1 组作轨号，第 3 级作废并继续尝试第 4、5 级；不按碟目录数量限制编号。
- 推断成功的轨号、碟号与艺人沿用来源 `filename_fallback`，仍标 inferred；文件名碟号不能覆盖 `album_title`。最终非合成候选碟号优先级为标签 → CUE → 专辑名 → 碟目录 → 文件名 → 1。一个介质内文件名轨号彼此重复，或与标签轨号重复时，该介质**全部**文件名轨号作废后补位，标签不动。
- `naturalLess` 比较连续数字段和非数字段：数字去前导零后先比长度、再比字面，不转整数；数值相同则前导零少者在前；非数字按字节比较。补位按 `RelativePath` 自然序，相等再按 `observationSortKey`，保留显式轨位与有效文件名轨位。
- 标题仅在 inferred 且来源为 `filename_fallback` 时清洗：去采信编号前缀及紧随的空白、点、下划线、短横线，再去首尾空白，结果空则保留 stem。纯数字前缀为整个 stem；第 2、3 级取到编号末尾；第 5 级只在匹配从开头开始时去前缀；第 4 级不去末尾数字。
- 在上述文件名标题中，仅当艺人 inferred 且来源为 `display_fallback`，并恰好含一个 ` - `、两边非空且左边不全为数字时，拆为艺人与标题。两者仍 inferred；写入展示复用 `metadata.CanonicalTrackArtist`，不复制艺人拆分规则。已有标题/艺人标签不被覆盖。`artistgraph` 接受来源为 `filename_fallback` 的 inferred 曲目艺人，其他 inferred 曲目艺人仍跳过，文件名艺人可以进入艺人页与收藏。判定使用 `track_observations` 中 `field_name='artist'` 那行的 `source_kind`：预处理写 `FieldSources["artist"]`，经 `fieldObservations()` 取为 `field.SourceKind`，由 `PersistCandidate` 写入该列；不是 `tracks.source_kind` 的物理/CUE 来源；O/T 两线分别从 2 升到 3，合并后统一 `artistgraph.Version=4`，确保两线的艺人投影变化均触发重整。
- `continuousAlbum(pairs []discTrack) bool`：至少 2 首，轨号均正；碟号小于 1 按 1，每碟轨号恰好 1..n，无缺无重，必须有第 1 碟，允许缺中间碟号。两种取数任一通过即可：混合模式按上述碟号优先级、轨号标签优先再文件名（冲突保护前）；文件名模式只取文件名轨号、碟号取文件名再碟目录再 1。不用自然序补位结果作归组证据。
- 仅 `ordinary_directory`、`box_leaf` 做同目录新判定，不含注册根散曲或已合成多碟。先检查格式切分；不切分时保留现有多数决；仅原规则会按 partition 拆成 `same_dir_split` 时用整目录连续性合并，包括缺少专辑标签的观察。字段仍按 `chooseValue` 多数决并保留 `inconsistent_candidates`。
- 格式键为小写 `Codec`，空则取相对路径扩展名小写并去前导点（如 `.flac` 归为 `flac`，与已有编码键一致）。至少两种格式、每个格式子集连续（同上两种取数，但允许 1 首）、整组存在重复碟轨，三者同时成立才切分。候选种类 `CandidateFormatSplit = "format_split"`，`Anchor.Scope = 目录`、`Anchor.Partition = "format:<格式键>"`；`stableAnchorPartition` 保留格式 partition，`VersionGroup = "format:" + 目录`。REST 的 `candidate_kind` 白名单接受 `format_split`，响应形状不变。格式版本按切分前的候选种类选择标题兜底：原 `box_leaf` 继承 Box 叶子的 Prefix 兜底及 `disc_dir_prefix_fallback_v1`，原普通目录仍走普通目录规则。
- `OrganizedCandidate.VersionGroup string` 标识本次整理的版本批；`JoinGroupID string` 由 scanner 填，不由 organizer 生成。`PlannedVersionGroup(ctx context.Context, database queryer, scanID, rootID string, candidates []OrganizedCandidate) (string,error)` 返回已规划沿用 Release 的候选中候选键最小者所在的组，全部新建则为空；`ReleaseGroupOf(ctx context.Context, database queryer, releaseID string) (string,error)` 返回发行的组。查询错误向上传递，不伪造空组。
- scanner 对 scope 内每个 `VersionGroup` 先定目标组，逐候选填 `JoinGroupID`、独立短事务写入；目标为空时从第一个成功写入发行取组，供后续新建版本使用。`persistOneCandidate` 返回 Release ID；仅 `plannedReleaseID == "" && JoinGroupID != ""` 时新建 Release 引用已有组而不新建组。沿用 Release 的组永不移动，即使管理员已把两个版本分到不同组。目标组读取或候选保存失败按 `catalog_write_failure` 令根不完整，已提交候选保留。
- 新建版本引用已有组只取外键共享锁，不改组的 `default_release_id` / `edit_revision`，不额外调整代表版本或搬移人工修正；既有发行锁与反馈锁顺序仍按[扫描反馈合同](./scan-feedback-contracts.md)。没有新增迁移；② 的腾空旧组收藏转移照常生效。

### 古典作品与乐章（⑧，迁移 0039）

- `catalog/classical` 只依赖标准库；`ParseCatalogs` 返回 `CatalogEntry{System,Number,Partial,Start,Length}`，位置按 rune；`CatalogSortKey` 只替换 `:/-` 为空格并折叠连续空格，不裁首尾。内置 Op/Opus/作品、WoO、H/Hob、BWV、KV/K、D、RV、HWV；无数字不解析，串首不匹配，每体系只留首条，共享抹白数组。编号与左边界只认 ASCII 字母数字，避免吞中文“号”；卷号保留 `I:94` 形式。
- `AudioObservation` 与暂存 `SourceObservation` 新增 `Work/MovementName/MovementNumber`，JSON 为 `work/movement_name/movement_number,omitempty`。文本经 `BoundedText`，同级取首个非空值，读完全部标签后按优先级选取，不受文件内键顺序影响。

| 容器 | 作品 | 乐章名 | 乐章号 |
| --- | --- | --- | --- |
| Vorbis、通用扩展标签 | WORK | MOVEMENTNAME，其次非数字 MOVEMENT | MOVEMENTNUMBER，其次数字或 n/m 的 MOVEMENT |
| ID3 | TIT1，其次 TXXX:WORK | MVNM | MVIN（n/m 取 n） |
| M4A | ©wrk，其次 ----:WORK | ©mvn | ©mvi（16 位整数） |

- TXXX 与 `com.apple.iTunes` freeform 复用通用键入口；原生键优先。TIT1 只有同文件存在非空 MVNM/MVIN，或能解析出作品编号时才作作品，否则用 WORK 回退。MOVEMENTTOTAL、©mvc、分母不保存；非正数或超出 INTEGER 的乐章号不保存。
- `AssignWorks(OrganizedCandidate) map[string]WorkAssignment` 在 `PersistCandidateWithFeedback` 开头计算一次，按碟号、轨位排序；键为规范化相对路径。`WorkAssignment{Title,Catalog,Index,MovementNumber,MovementTitle,Source}` 交给 `persistTrack`，规划阶段不计算，不改变发行/曲目身份或 organizer。
- 有 WORK 总按标签写 `Source="tag"`，不受流派限制；乐章名缺失时只剥曲目标题开头的“作品名 + `: `、`：`、` - `、`, `、`，`”。其余连续无 WORK 段，仅扫描候选流派经 `GenreKey` 等于 `classical/classic/classique/klassik/clásica/clasica/classica/クラシック/클래식` 或包含“古典”时调用 `SplitWorks`，来源为 `title`；人工修正流派不触发重算，Classical Crossover、Neo-Classical 不在名单。
- `SplitWorks` 返回 `WorkSplit{Work,Movement,Group}`。括号归一化只用于判定，切分取原文；保留空格闸门、调性/缩写/时间戳例外，补全角分隔符闸门及罗马、阿拉伯、“第N乐章”的反向定位。`Concerto: 1. Allegro / 2. Adagio` 由已确认的原文前导定位作品，不放宽公开 `Segments` 的闸门。单曲推断组不产出任何字段。
- 相邻作品名去首尾空白、小写相同且来源相同才可成组；标签乐章号都有值时须递增，相邻非空乐章名重复则断组，`promenade` 除外；无名不算同名。标题来源以 `SplitWorks.Group` 为准。只给至少两轨的组分配发行内从 1 递增的 Index；单轨标签仍保存作品供搜索。CUE 虚拟轨按 CUE TITLE 参与。重复规则按相邻语义，不采用参考伪码仅首曲更新 lastPart 的错误。
- Catalog 取 `ParseCatalogs(Title)` 第一项的原文片段并裁首尾空白。六列 `work_title/work_catalog/work_index/movement_number/movement_title/work_source` 重扫整体替换，无作品全部 NULL；两个标题非空且≤4096字节，编号非空且≤256字节，序号正数，来源 `tag|title`。作品名与来源同空同有，无作品名不得有其他作品列；违反 CHECK 走既有候选写入失败路径。
- down 先删 `work_title/work_catalog` 搜索值、恢复字段 CHECK，再删作品约束与六列。④尚未合入，不新增或提升解析/归组版本；⑥已提升的 `ProjectionVersion=2` 不再升，`textnorm.Version=1` 不变。读取形状见[REST合同](./catalog-rest-contracts.md)，搜索见[搜索合同](./search-contracts.md)，验证内容见[扫描验证](./library-scan-verification.md)。

### 根保存后的专辑收藏转移

- 迁移 `0037_scan_observation_previous_release.sql` 增加暂存列 `previous_release_id UUID`，无外键/索引，down 删除列；回退测试表使用版本 37。`StageObservation` 冲突更新清空该列，身份规划在 `allocateReleases` 后按沿用曲目保存原发行。
- 本根保存后、已规划且未取消时用原连接开短事务：`ingest.LockProjectionWrites`（CATP）→ `ingest.EmptiedGroupMoves(ctx,db,scanID,rootID)` → 注入的 `AlbumFavoriteMover` → 提交；nil mover 不执行，空映射直接提交。
- 按实际移动计算：原组所有发行均无任何曲目（包括 missing）才转移；按原组汇总各接收 Release 的移入数，最多者胜，平局按接收发行 created_at、ID 升序。仅转专辑收藏，不搬修正/认领/默认/歌单/历史，不删旧组/发行。
- `SetAlbumFavoriteMover` 由 app 注入 `personalService.MoveAlbumFavorites`；收藏去重、保留时间与名称、revision 规则见[个人合同](./personal-contracts.md)。失败回滚转移事务，只记 Warn `library.scan.favorite_transfer_failed`（仅扫描 ID），不影响扫描完整性或已保存候选。写完根后、转移前取消不补偿，下次扫描也不会重放已经消失的移动证据。

### MP3 扫描时长

`parseMP3` 调用 `applyMP3StreamFacts(reader io.ReaderAt, fileSize int64, observation *AudioObservation)`（`parser/mp3_vbr.go`）。
复用 `mp3AudioRange` 去掉叠加 ID3v2 和末尾 ID3v1/`TAG+`、APEv2、Lyrics3v2，在音频区间前 64 KiB 找首个有效 Layer III 帧；读取失败不填音频参数。
Xing/Info 位于首帧 + 4 + 边信息长度：MPEG-1 双/单声道为 32/17，MPEG-2/2.5 为 17/9；按大端标志依次取帧数和字节数。
没有 Xing/Info 时读首帧 + 36 的 VBRI。帧数大于 0 时，时长 = 帧数 × 每帧样本数 ÷ 采样率；MPEG-1 为 1152，MPEG-2/2.5 为 576。
码率按头部正字节数（缺失时取首帧至音频区间结尾）× 8 ÷ 时长 ÷ 1000，四舍五入为 kbps；没有帧数时以同一音频字节数除以首帧码率作 CBR 浮点估算，不截整秒。
不解析 TOC、不逐帧计数、不扣 LAME 编码延迟与补齐；没有头的 VBR 残余偏差由[分析时长视图](./audio-analysis-contracts.md)校正。
标签、`mp3-frames-v1` 指纹、曲目身份均不变。本次④尚未进入 main，MP3 格式版本仍从 1 起；缓存发布后再改时长规则须递增 MP3 格式版本。
验证覆盖 Xing/Info/VBRI、版本与声道偏移、缺字节数和 CBR 标签扣除；真实 Xing VBR 扫描与解码时长差小于 0.1 秒。
错误：用含封面的整个文件大小除以码率并截整秒；正确：先限定音频区间，有帧数优先用帧数，无帧数保留浮点精度。

### 新格式内容指纹

`parser/fingerprint.go` 按扩展名选择种类；旧格式种类和版本不变。新增：

| 种类 | 输入 |
| --- | --- |
| `ape-md5-v1` | 非零描述块 MD5，加总块数、采样率、声道和位深 |
| `ape-frames-v1` | 上述参数与帧数据的 SHA-256；旧头或描述块 MD5 全零时使用 |
| `wavpack-blocks-v1` | 从第一个 `wvpk` 起连续的完整块字节的 SHA-256 |
| `aiff-ssnd-v1` | `COMM` 的声道、帧数、位深、80 位采样率、压缩类型，与 `SSND` 声音数据的 SHA-256 |

APE 新头按帧起点与声明帧长度取负载；旧头从 seek 表首项加 ID3v2 前缀偏移起，到尾部标签之前结束。
WavPack 在首个非 `wvpk` 位置结束。AIFF 的 `SSND` 跳过 offset/blockSize 八字节及 offset 指定的填充。
读取使用 `hashSection` 的分段取消检查，不整读音频。无音频负载返回空指纹，不与其他文件比较。
APEv2、ID3、`NAME`/`AUTH` 标签的变化不改变指纹；帧数据或关键参数变化会改变指纹，APE 原生 MD5 路径信任描述块的摘要。
播放转换前通过同一 `ContentFingerprintOf` 分派重算，不另建播放专用身份算法。

### 并发解析（⑤）

- `library/scan_pipeline.go` 将候选、过滤和失败条目放入有序槽位；缓存命中直接就绪，只有未命中音频进入 `platform/workpool.Ordered`。窗口为 `2 × workers`（默认 8），窗口满时等待头部；结果只能在遍历 goroutine 按 WalkDir 次序应用。构造观察、缓存写入、暂存、诊断、HoldSource、跳过与 processed 计数都不在 worker 中执行；诊断 100 条截断及暂存顺序与并发 1 一致。
- `NewOrdered[T](ctx, workers, window, apply)` 要求正数并发与窗口；`Submit(func(context.Context) T)` / `Ready(T)` 排队并应用已就绪头部；`Flush()` 应用当前队列但保留 worker，`Drain()` 应用后关闭，`Close()` 取消并等待、不应用。调用端串行使用；apply 首错停止流水线，取消丢弃未应用结果，等待已启动解析返回，不捕获 panic、不重试。解析器不接收 context，关闭等待当前文件读取结束；单文件内存有界不等于读取耗时有界。
- 缓存查询仍在持锁的遍历 goroutine；后续条目引用尚在途的同一规范父音频路径时，先 `Flush()` 应用早先的缓存写入再查缓存，不能以 worker 完成快慢决定命中。缓存成功、失败与 full 模式沿用④，不引入根级结果缓存。
- `parser.ParseCueSheet(path, containmentRoot ...string) (CueDocument,error)` 只读 sheet、校验引用；每张 sheet 的唯一父音频先查缓存，未命中项在一个任务内读取。应用阶段调用 `ResolveCueDurations(document CueDocument, duration CueDurationReader) (CueDocument,error)` 一次，按原 FILE 标识完成区间、诊断和元数据；既有同步 `ParseCueDocument` 依次调用这两阶段，返回值不保留临时 FILE 标识。CUE 父事实仍受单 sheet 的 FILE 上限约束，不能把每槽位理解为固定只有一张封面。
- `PlanOptions.Concurrency int` 小于等于零按 1；`resolveFingerprints` 每页最多 100 个路径，用 `workpool.Map(ctx, workers, items, fn)` 并行只读，再按输入路径顺序写回与记 `fingerprint_failure`。Fingerprinter（包括测试桩）必须允许并发调用。SQL 仍用同一 holder、原 `WithoutCancel` 合同；不增加写入者或并行根。
- OGG/Opus 标签入口逐页逐段读取，只保留固定页头/段缓冲和当前最多 16 MiB 的包，音频包即时入哈希；独立 `oggFingerprint` 同样流式，不能按文件大小分配。标签、音频字节哈希、静态输入格式错误顺序保持；原 `DurationSeconds=0` 保持，不新增 granule 时长语义。流式读取不保证复现“前段格式错误与后段 I/O 错误并存”时整文件预读的底层错误优先级，读取失败仍失败。
- 内存估算只针对普通音频和少量 FILE 的常见 CUE，不是全局硬上限。重复 OGG 注释包仍可累积元数据，极多 FILE 的 CUE 仍聚合父事实；2026-11-08 用户裁决为“已知局限，接受”，保留旧接受范围，不新增累计预算、不截断、不改变 CUE 聚合。
- 配置与 app 装配见运行合同：正式服务默认 4；直接构造的零值 Service 与零值 PlanOptions 仍串行。设为 1 只改变读盘并发，不改变来源、缓存或反馈语义。未增加迁移、REST 字段或解析器版本变化。

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

- `parser.CommonVersion=1`；`formatVersions` 的 `.flac/.mp3/.ogg/.opus/.wav/.m4a/.ape/.wv/.aif/.aiff` 均从 1 起。`CacheVersion(extension)` 返回如 `c1.f1`，未知扩展名返回空串。本次整合前④尚未进入 main，不追补 M/O/T 规则版本；`artistgraph.Version=4`、`searchindex.ProjectionVersion=2` 由各自维护流程回填，不参与解析缓存命中判定。已有试验 S 分支的版本 1 缓存不能代表合并后基线，验证时应使用新隔离库或完整重扫。
- `AudioObservation` 字段、类型、含义或共用解析规则（兜底、分隔、多值、歌词、日期以及 parser 内编号推断）改变，升 `CommonVersion`，使所有格式重新解析；只改某格式读取，升该格式版本；新增格式只加版本 1，不使其他格式失效。
- `ingest.GroupingVersion=1`，覆盖同样观察所产生的 Release 图：organizer、folder_metadata、抓轨证据、字段决定、归组证据、署名/艺人、流派、作品、搜索投影字段与封面选择。只改这些规则而不改 parser，不升解析器版本，只升归组版本；升归组版本使所有候选重写而不使解析缓存失效。
- ④ 合入之后的合入核对：O ②/③ 升 `GroupingVersion`（同批合入合计加一）；T ⑥ 升 `CommonVersion` 与 `GroupingVersion`；T ⑦/⑧ 按格式或共用解析改动升解析器版本，并升归组版本；T ⑬ 搜索独立回填不升；M ⑩ 新格式加 1，共用解析变化另升 `CommonVersion`，CUE 回退扩展名表无须另升；M ⑨ MP3 时长规则升 `.mp3` 版本，分析器自有版本另行维护。④ 之前已合入的规则不追补版本。

#### 借用指纹与完整重扫

- `PlanOptions.Mode` 传入模式；仅 `WalkComplete` 且增量模式，才在 `resolveFingerprints` 计算前借用指纹。旧侧为本根 present、已知指纹/大小/mtime、未被任何本轮暂存 `matched_track_id` 引用且未被保住的文件；CUE 取 `COALESCE(cue_parent_relative_path,relative_path)`。
- 新侧为保留、未暂缓、未匹配且无指纹、`fingerprint_path` 非空的暂存观察；按文件名、大小、mtime 配对，两侧各恰好一个不同文件路径才借用。CUE 多轨按父文件去重计数，并将借得指纹用于该父文件的暂存行。文件名改变或任一侧有多个路径，照常计算；不使用目录名或标签推测身份。
- 借用不代替曲目配对：后续仍要求指纹与 CUE 区间两侧唯一，沿用原 UUID 与 Release 严格过半规则。`RootPlan.FingerprintsBorrowed` 汇入 `library.scan.identity_resolved` 的计数，不记录路径。
- `full` 绕过解析缓存读取、候选跳过和指纹借用，仍将成功解析写缓存；保留既有的“同来源身份且 size/mtime 未变则复用已存指纹”。扫描仍走同一协调锁、身份规划、反馈与 missing 流程。HTTP 可选请求体与所有角色 `ScanDTO.mode` 见[扫描协调合同](./scan-coordination-contracts.md)。
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

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 同一幂等键用于另一个目录，或同一目录的不同请求参数 | `409 idempotency_conflict`；不执行、不写记录，也不返回前一个请求的结果 |
| 同一幂等键的并发请求（新增、停用、恢复、改路径及排除项增删） | 串行处理，只执行一次，其余请求回放首次记录的状态码和响应 |
| 停用/恢复/改路径的路由目录 ID 不是 UUID 形式 | `400 invalid_id`，不进入幂等流程，不写操作记录 |
| 重复注册 disabled root | 返回 `status=disabled` 与真实 revision，不隐式恢复 |
| 改路径未登录 / 非管理员 / 来源不符 | `401` / `403` / 来源拒绝；无副作用，鉴权先于资源读取 |
| 改路径缺幂等键、缺 path 或 `expected_revision < 1` | `400 invalid_input`；不写记录 |
| 改路径越界、不存在、非目录或任一层为符号链接 | `400 invalid_library_path` / “目录不在允许的音乐根目录内”；不进入幂等流程，不泄露允许根或系统错误 |
| 改路径时本进程或其他进程持有扫描锁 | `409 scan_running` / “正在扫描，稍后再试”；不修改、不写记录 |
| 改路径目录不存在 / revision 不符 | `404 not_found` / `409 revision_conflict`；不修改、不写记录 |
| 新路径已被其他根登记（含停用根），或并发新增触发 `23505` 且约束为 `library_roots_path_key` | `409 path_already_registered` / “这个文件夹已经添加过了”；回滚，不把其他唯一约束错误误判为路径冲突 |
| 改路径为当前规范路径，且 revision 相符、取得扫描锁 | `200` 安全六字段 DTO，revision 不变，写成功记录 |
| 改路径数据库失败 / 操作记录写入失败 | `503 database_unavailable` / `500 operation_failed`；整个事务回滚 |
| 停用后改路径导致版本不再紧邻停用记录 | 恢复取最近成功 disable 的 active 前态；仍校验当前 disabled 与请求版本，保留新路径 |
| unsupported、音频解析失败、CUE 失败、子目录读不了 | 见完整性矩阵；诊断并保住失败来源，其余来源正常对账 |
| 隐藏项、内置名单和用户排除 | 不诊断、不保住；成功后按消失处理 |
| 候选为零且已有 present 来源，无目录读取失败或排除命中 | `root_empty`，不规划不写入、不做 missing |
| CUE 引用目标不存在或 Track 缺少 `INDEX 01` | 写 `cue_reference` 诊断并跳过不可用虚拟 Track；不单独令扫描 incomplete，也不伪造父来源/offset |
| 初始或终态 staging 清理失败 | root 不完整并记录 `staging_write_failure`；旧 observation 不得混入本次候选 |
| 媒体节点不是 regular file | 不调用 parser，记录 `non_regular_file` 并保住该文件，root 仍完整 |
| CUE 显式父来源跨 sheet 目录但仍在注册根内 | 对齐到父来源 scope，保留稳定虚拟身份并按明确关系去重 |
| rip log 无明确签名、签名晚于 64 KiB 或为 symlink | 不产生 CD 语义，不读取超出预算的内容 |
| 明确 rip log 与 `folder` 补充值并存 | 选定 CD 并把 decision provenance 提升为 `rip_log`；不得保留重复字段决定 |
| 明确 rip log 与 tag/CUE/音频规格决定冲突 | 保留明确来源，不允许抓轨日志覆盖 |
| 艺人/credit 含换行或 NUL | 先切段再清理，不能把两个名字粘连；固定组合与拉丁姓名保护按第3节执行 |
| TIT1 没有乐章标签且解析不出作品编号 | 忽略分组值，仍可回退到 WORK |
| 非古典扫描流派且没有 WORK | 不从标题推断作品；有 WORK 不受流派限制 |
| 标签作品只有一轨 | 保存作品供搜索，work_index 为 NULL，REST work 为 null |
| 乐章号非正数或超出 INTEGER | 不保存序号，不使合法作品名失效 |
| 作品列违反非空、长度、正数或关联 CHECK | 候选事务回滚，按既有 catalog_write_failure 记录；不吞错 |
| TIPL/IPLS/TMCL 奇数个值 | 只丢最后落单项，已成对的署名保留 |
| 未知工种或乐器 | TIPL/IPLS 归制作人、TMCL 归演奏并保留细分；未知普通标签键忽略 |
| 演奏括号含未知非拉丁文字 | 整个原文作为姓名，不把别名猜成乐器 |
| 歌词头部首个非署名行 | 停止提取，之后的署名形状行不读取；歌词原文不变 |
| 同角色同时有标签与歌词署名 | 全部采用标签来源，不合并该角色的歌词署名 |
| 专辑艺人高优先级为空或多值 | 回退到首个非空级，展开该级全部值，不采用最后写入值 |
| TXXX 描述/值编码无效、缺终止符，或帧压缩/加密 | 跳过该帧，不使整个文件解析失败；证据超预算仍按既有规则失败 |
| 无真实专辑艺人，合辑标志恰好半数 | 不因标志判合辑；必须严格过半，目录观察不计票 |
| 有真实专辑艺人且合辑标志为真 | 保留真实专辑艺人，不改发行决定 |
| 本版年份存在且原始年份更早 | 保留本版年份，不覆盖 |
| 相同 album 的部分 Track 缺少 AlbumArtist，且其它 Track 只有一个明确值 | 保持同一候选，明确 AlbumArtist 优先于 Track Artist |
| 命名 folder artwork 非普通文件、为空、超限或格式损坏，且所有查找目录里没有别的可用封面 | `artwork_unreadable`，root 仍完整，保留已有关系，不解释为封面删除 |
| 前一个目录里的命名封面无效，后一个目录里有可用封面 | 用后一个目录的封面，不报错 |
| 查找目录不存在 | 跳过该目录，不报错 |
| 读查找目录失败（非“不存在”，如权限） | 记为错误并继续找下一个目录；全部找完仍没有可用封面才 `artwork_unreadable`；root 仍完整 |
| 发行目录与曲目目录都有封面 | 先按四轮优先级，再按目录顺序；两者均命中精确名单时发行目录胜出，曲目目录精确名优先于发行目录附属子目录封面 |
| 候选带内嵌封面，目录里也有文件夹封面 | 内嵌封面，`source_type=embedded` |
| 文件名只是大小写不同（`Cover.JPG`、`FOLDER.png`） | 与全小写名同样被发现；优先级不受大小写影响 |
| content-addressed 受管文件与其 hash 内容不符 | 写临时文件并原子替换，随后才确认数据库关系 |
| 受管封面查询或旧文件清理失败 | 返回分类错误并令扫描不完整，不静默吞错 |
| 单个 candidate/封面写入失败 | 已提交候选保持可见，记录分类诊断并使扫描不完整，禁止 `missing` |
| 同一路径的音频内容指纹变化 | 新内容建新曲目（新 UUID）；原曲目失去来源身份，没有配对到别处时置为 `missing`；记 `content_replaced` |
| 遍历完整的 root 中，无身份匹配的观察与本轮消失的曲目指纹相同且各恰好一条 | 沿用原曲目 UUID 并更新路径；任一侧多于一条时不重绑定，逐键记 `identity_ambiguous` |
| 遍历不完整或被取消时出现疑似移动或换内容的来源 | 不重绑定、不因此新建身份、不置 `missing`；所在 scope 暂缓写入并记 `identity_deferred`，其余 scope 照常写入 |
| 来源文件无法完整读取以计算指纹 | 记 `fingerprint_failure`，该 scope 暂缓写入，root 不完整，本轮不配对 |
| 规划失败，或候选的观察没有规划 | 记 `catalog_write_failure`，root 不完整；不写入、不猜测身份 |
| 候选已规划沿用的曲目严格过半属于同一 Release | 沿用该 Release UUID；不过半（如 2 轨加 3 轨、1 轨加 1 轨）则新建 Release；格式版本可加入已有组，其他情况新建 ReleaseGroup |
| 扫描成功且所有 root 完整 | 允许负向 `missing` 对账 |
| 非音频附件 | 忽略，不改变扫描完整性 |
| 并发流水线 apply 失败或取消 | 停止提交；取消丢弃未应用结果并等待 worker 退出，root 不完整，不做 missing |
| 缓存表读写失败、缓存内容不能解码 | WARN，按未命中解析，不改变 root 完整性 |
| 大小、微秒 mtime 或解析器版本变化 | 重新解析；完整模式即使相同也重新解析 |
| 延迟内嵌封面读取失败 | `artwork_unreadable`，继续原文件夹封面查找 |
| 同名/同大小/同时间的移动来源任一侧不唯一，或 full 模式 | 不借用指纹，照常计算或按原来源规则复用 |

## 5. 案例

T 线标签、署名与作品的补充验证见[目录与扫描流程验证与示例](./library-scan-verification.md)。以下保留目录、文件与归组验证。

- 基准：重复 POST 已停用目录返回 disabled；管理员随后使用 restore 端点显式恢复。
- 正确：NAS 新位置包含同样的相对路径与音频，改路径后 root ID 不变，下一次成功扫描沿用 Track/Release UUID，个人数据与修正不迁移。
- 基准：用新幂等键提交当前路径且 revision 相符，返回 `200`、revision 不变，收据仍可回放。
- 正确：revision 4 的目录停用到 5、改路径到 6，再按 `expected_revision=6` 恢复到 7；使用最近成功停用的 active 前态，路径保持新位置。
- 错误：把停用根的已登记路径视为空闲，或让扫描锁冲突的失败请求占用幂等键。
- 正确：多碟来源按 `disc_number=1/2` 形成两个 Medium，重扫保持 Track ID。
- 正确：`sheets/album.cue` 引用 `../media/image.flac` 时，只要两者仍在同一注册根内，
  虚拟轨会与显式父来源对齐；每轨 provenance 独立，track PERFORMER 不污染相邻轨。
- 基准：历史 CUE 指向已不存在的原始 WAV，或个别 Track 没有 `INDEX 01` 时，保留
  `cue_reference` 诊断并跳过这些虚拟 Track；同目录其它有效物理来源仍可完成扫描。
- 基准：只有 EAC/XLD 明确签名位于 `.log` 前 64 KiB 时补充缺失的 CD 语义；无签名
  日志保持未知。
- 正确：目录名先补了 WEB/CD 等来源值，但同 candidate 存在明确 EAC/XLD 日志时，
  抓轨规则可以替换 `folder` 决定；tag 或 CUE 的明确来源仍保持不变。
- 正确：`Artist Beta / Artist Alpha` 作为 Track artist 被拆分后以稳定顺序展示；已登记的
  别名不再改写事实显示名，固定组合名不会因包含 `/`、`,` 或 `&` 被误拆。
- 正确：一张专辑只有一轨提供 `AlbumArtist`、其余轨只提供各自 Artist 时仍形成一个
  candidate，Release artist 与 credit 使用明确 `AlbumArtist`；缺失轨不伪造自己的 tag。
- 正确：已存在的 content-addressed 封面文件被破坏后，重扫按期望字节修复；旧 key
  清理失败返回 `artwork_failure`，不报告成功。
- 正确：CUE 专辑目录里的 `cover.png` 被绑定；严格多碟专辑父目录里的 `Cover.JPG` 被绑定；Box 叶子目录没有封面时，
  Box 父目录里的封面不会绑给它；封面后来才放进目录，下一次扫描即绑定。
- 正确：unsupported 文件诊断并保住其已有曲目，扫描 succeeded，真正删除的其它曲目照常 missing。
- 错误：把 root 原始路径放入列表 JSON；把失败来源当消失用于移动配对；把 FIFO 交给 parser；按标题删除 CUE 轨。

## 6. 必需测试

- ⑤ 单元：Ordered 混排/窗口/Flush 复用/首错/取消退出与 Map 输入顺序，race；配置默认 4、边界 1/16、非法 0/17/abc；OGG/Opus 新旧观察逐字段和指纹一致，大文件内存不随大小增长，截断仍失败。
- ⑤ PostgreSQL：并发 1/4 对比缓存命中与解析计数、暂存顺序、诊断（含 100 条截断）、HoldSource 和反馈；覆盖 CUE 在物理父文件前后、多个 sheet 共父与跨目录父音频、缓存冷热/full、取消丢弃在途结果且 worker 退出。测试另由测试 agent 实施，不把无数据库时的 SKIP 当作通过。

- ④ 单元：当前支持格式/未知扩展名的缓存版本、deferred 封面选取与暂存去重；HTTP 空请求体默认增量、两种合法模式、未知字段/缺 mode/非法 mode 拒绝，所有角色/入口包含 `mode`。
- ④ PostgreSQL：连续扫描未变文件命中、mtime/size/格式版本变化重新解析、full 绕过读取但仍写缓存；CUE sheet 每轮读取且父音频共用缓存；超 2000 条目录的逐条查询、seen 批量刷新、删除/过滤清理与取消/walk_error 不清理；缓存故障不改变完整性。
  deferred 候选重写仍取内嵌封面；目录改名借用且 Track UUID 不变，任一侧多文件歧义不借用、保住范围不被借用，full 不借用。0041 升级/下行/再升级覆盖两表一列及历史模式默认值。
- ④ 候选跳过：成员、版本、CUE、封面四轮候选与抓轨日志每类输入使签名失效，无关文件不影响；未变候选零写入/零反馈变化/零 deferred 读取，治理改归属后重写，升归组版本全部重写，touch 不改变音频分析输入快照。另验封面共享列举保持命中即停/首错顺序/过滤；被跳过版本仍为新增同组版本提供组 ID，重复扫描不误触发 group moves/收藏转移；完整遍历才清候选状态，取消与 walk_error 不清，touch 失败阻断 missing，状态表故障只降级。

- 改路径 PostgreSQL/HTTP：成功保持 root ID/status/excluded_paths、revision 仅在路径变化时加一；成功响应及回放严格六字段，响应/操作记录列表不含绝对路径，前后态仅库内可见。
  覆盖同键回放、同键异义（含目标/规范路径/版本变化）、同键并发只执行一次、相同路径不增版本仍写收据、revision 冲突、未知/非法 ID、缺参数/键与鉴权无副作用。
  已登记新路径（含 disabled）、越界/不存在/非目录/符号链接应返回对应安全错误；独立连接持有扫描锁时拒绝且不写记录，释放后同键可重试。
  “停用 → 改路径 → 恢复”成功并保留新路径；复制同内容到新位置后改路径重扫，曲目/Release UUID 不变、missing 为零。
- 0035 迁移往返：含真实 `relocate` 成功记录的夹具执行升级 → 下行 → 再升级，确认下行删除新类型收据并恢复五类约束，根路径与 revision 保持改后值；不把结构回退当作目录改路径撤销。
- 后端单元：终态映射、同一过滤覆盖预计数与遍历、系统名大小写整段匹配且不检查注册根自身、用户排除子树、unsupported 候选识别、来源身份与 containment；CUE NFC/NFD/大小写唯一匹配和歧义停止回退。
- parser/organizer 单元：M4A/WAV 容器有效性、CUE 多 FILE 与跨目录 containment、
  missing reference/missing `INDEX 01` 非阻断跳过、`unsafe`/`unchecked` 引用保住 sheet、
  sheet/track performer provenance、逐轨 map 隔离、inferred track/disc fallback、
  明确标签优先、缺失 AlbumArtist 兼容与 AlbumArtist 决策优先级、艺人/credit 多分隔符、
  官方别名/固定组合名，rip log 64 KiB 预算、签名和仅覆盖 `folder` 的优先级，封面目录顺序（`ArtworkDirectories`：普通、CUE、
  CUE 跨目录、严格多碟含碟号 10、Box 叶子、根目录散曲、去重与按曲序）与内嵌封面选取（`EmbeddedArtwork`），
  文件夹封面发现（大小写变体、全小写优先、名字优先级不受大小写影响、多目录顺序、前一个目录无效时用后一个、目录不存在跳过、
  读目录失败报告、符号链接/无效/读取失败），以及各格式音频内容指纹（同音频不同标签
  相同、音频变化不同、FLAC MD5 为零的回退、MP3 首尾标签、OGG 注释包分页、读取中取消）。
- PostgreSQL 集成（配置 `ROOMUSIC_TEST_DATABASE_URL` 时）：用户禁用与 session
  原子性、最后管理员/并发保护、未知用户 404、目录幂等与真实状态、扫描 incomplete
  禁止 missing、文件/目录/CUE 保住与反抢 UUID、空根保护和排除命中例外、排除接口幂等与上限、多 Medium 与 Track 身份稳定；另需覆盖 staging 清理、候选短事务
  回滚/部分提交、跨目录 CUE 父来源对齐、regular-file 拒绝、身份规划（改名/移动/互换/相同音频、不完整与取消、换内容、跨 root、CUE、
  指纹复用与失败）与 Release 成员关系匹配、
  current evidence/track credits 替换、Track artist canonical 展示、受管封面内容重校验、
  旧 key 清理错误传播和绑定失败清理，封面发现（CUE 含重扫、严格多碟父目录、父目录与碟目录都有、回退到碟目录、
  Box 叶子不取父目录、内嵌优先）。

## 7. 错误与正确示例

### 并发缓存的错误与正确示例

错误：worker 直接写缓存或暂存；CUE 的后续物理条目在早先父音频仍在途时直接查缓存，导致命中数取决于调度。

正确：worker 只返回解析结果，主 goroutine 按序应用；同路径在途依赖先 Flush 再查缓存，无依赖的文件继续使用有界窗口并行。

### 增量扫描的错误与正确示例

基准：空缓存的第一次扫描解析并写缓存与候选状态；第二次读取同路径同大小同微秒 mtime 同版本的文件时命中解析缓存，候选签名与当前归属也符合时仅 touch，报告零变化。只命中解析缓存而附属文件变化时仍须重写候选。

正确：标签编辑保留 mtime 时，管理员发送 `{"mode":"full"}` 强制重新解析；缓存照常更新，同来源的音频指纹仍可按旧规则复用。

错误：缓存 SQL 失败令 root 不完整；把无封面字节的缓存观察当成从来没有内嵌封面；full 模式借用改名来源指纹。

正确：缓存失败 WARN 后解析，MIME 标记传递为 `ArtworkDeferred` 并在保存封面时现读，只有完整增量遍历才借用指纹。

### 改路径与恢复的错误与正确示例

错误：查到 `scan_runs` 没有 running 就直接更新路径；恢复查询限定停用记录的 `result_revision` 必须等于当前 revision；把前后态直接当作响应。

正确：先幂等回放检查，再持同键扫描锁修改路径并写安全收据；恢复按最近成功 disable 验证 active 前态，不限定版本紧邻。

### CUE 诊断错误与正确示例

错误：把所有 CUE diagnostic 都提升为 root incomplete；这会让缺失的历史 sidecar
引用阻断同目录中已经确定的物理音频，也无法完成真实库重扫。

```go
for range document.Diagnostics {
	complete = false
}
```

正确：诊断始终保留；unsafe/unchecked 引用保住本 sheet，根仍完整；missing 是确定不存在，跳过对应虚拟 Track，成功后允许正常 missing 对账。

```go
if cueReferenceNeedsHold(reference.Status) {
    // 整张 sheet 只登记、计跳过一次；失败时不写该 sheet 的虚拟轨。
    return hold(ingest.HeldCueSheet, sheetRelativePath)
}
if reference.Status != "present" {
    continue
}
```

### 抓轨证据优先级错误与正确示例

错误：只看最终字符串是否为空，会把先到达的目录补充值误当作权威值。

```go
if candidate.SourceType != "" {
	return
}
candidate.SourceType = "CD"
```

正确：检查选定 decision 的 provenance；仅替换 `folder`，tag/CUE 等其它来源不变。

```go
if decision.Source == "folder" {
	candidate.SourceType = "CD"
	replaceDecision("source_type", ripLogDecision)
}
```

停用目录拒绝新的播放请求，已建立传输不因此切断，专辑仍可浏览；具体语义见[播放合同](./playback-contracts.md)。

## M5 ④搜索投影挂接

扫描候选及修正/清除/撤销/claim 双方在同事务刷新目标代次搜索投影；CATP 先于业务行锁，失败回滚，后台持锁重读避免覆盖新值。扫描不写治理表，missing 可见性读取实时过滤。详见[搜索合同](./search-contracts.md)。

## ② 文件夹名规则验证

- 解析单元：覆盖数字、英文数词、规范罗马数字、括号与总碟数、首个无效标记后继续查找；纯数字、中文碟号、介质标签与唱片编号不得误认。专辑名从非开头标记处截断，剥空不改，观察原文不丢失。
- 归组单元：独占/不独占、父为注册根、单碟归父、Prefix 冲突、标签冲突、无标签连续多碟、缺号仅全目录有标签可合、附加碟及重复编号；断言候选种类/锚点、碟号/碟名、标题兜底及非合成候选的标签→CUE→专辑名→目录优先级。
- 封面单元：原目录精确名单必须优先于原目录 front 和附属子目录；附属目录大小写、名字字节序、拒绝符号链接；坏图后仍可用其它目录的有效封面，全部失败才报第一个错误。
- 隔离 PostgreSQL：CUE 对齐后独占标记看到最终 scope，父目录直接音频、旁边专辑和碟目录内更深层音频均阻止独占；保存/去掉碟名后介质标题恢复 `Medium`。
- 收藏集成：两个旧叶子合成新发行后曲目 UUID 保持，旧组腾空才转移；其它版本仍有曲目（含 missing）时不转；按接收发行计数，平局按创建时间/ID。覆盖目标已收藏、多来源合流、保留时间/保存名称、仅受影响用户 revision 加一、重复扫描不再转移、回调失败仅 Warn 且事务回滚。
- 正确：独占 `A/CD1` 锚在 `A`，旁边另有专辑时锚回碟目录；`CD1/CD2/CD1 (Live)` 的介质为 1、2、3，第三碟名为 `Live`。
- 错误：未对齐 CUE 就算独占，或按旧 Release 而非旧组是否腾空来转收藏；正确：最终 scope 判断独占、整组无任何曲目才转移。

以上是必需断言，不表示已执行；本项实现会话不新增测试，数据库验证由专门测试会话执行。

## ③ 文件名与同目录归组验证

- 单元断言：五级编号、零值、三/四位目录多数与降级、年份保护、标题清洗及艺人拆分；标签/CUE不变；自然序数字段、前导零与长数字；同介质文件名冲突整批补位及碟号优先级。
- 归组断言：连续、缺轨、重轨、无第 1 碟、允许缺碟、单曲；仅在原本会拆时连续性合并；两格式各连续且重复才切、单曲双格式可切、不完整子集不切，锚点与 `VersionGroup` 稳定。
- 隔离 PostgreSQL 断言：首扫双格式同组、旧混合发行一个沿用一个新建同组、重复扫描稳定；两个已有版本被人工分组后扫描不搬组、不改默认版本或组 revision；文件名 inferred 艺人进入图，普通占位艺人不进入。数据库验证由测试会话执行，本实现会话不启动数据库。
- 正确：无标签合成文件名 `01 - 示例艺人 - 示例曲.flac` 得轨 1、艺人与干净标题；同目录 flac 与 mp3 各自 1..2 得两个同组版本；单一格式原有正确归组保持不变。
- 错误：把 `2046` 拆为碟轨、用自然序补出的 1..n 证明不同专辑能合并、把沿用发行移进新选组；正确：四位先验首位与目录多数、连续性只用标签/文件名证据、只为新建发行选组。

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
- CUE FILE 精确路径不存在时，先在同目录做 NFC + `strings.EqualFold` 文件名匹配；恰好一普通文件才采用，多匹配为 missing 并记歧义诊断。零匹配再按 `.flac`、`.mp3`、`.ogg`、`.opus`、`.wav`、`.m4a` 次序，以同一比较函数匹配 stem 和扩展名，每扩展名同样要求唯一；遇歧义即停止。目录部分必须逐字节存在，采用后照常做 containment/symlink 检查并改成实际文件名，扫描来源身份不做 Unicode/大小写归一化。
- 缺少 INDEX 01 或确定不存在的父来源只跳过虚拟轨，不伪造父来源或零偏移；失败 sheet 的已有虚拟轨原样保留。
- `0011_scan_staging_and_identity.sql` 增加有界暂存、候选锚点、物理/CUE 身份、音频/CUE 列、字段/归组证据、署名及元数据。
  0010 旧来源身份不兼容（见下方规则）；0032 的列表整理见[多流派合同](./genre-contracts.md)。
- `0012_track_credits_and_release_metadata.sql` 增加 Release `edition`/`label`/`barcode`
  以及 `track_credits(track_id,role,name,position)`。逐轨角色由数据库 CHECK allowlist
  约束，`track_id + role + name` 唯一；重扫先替换该 Track 的 current credits，不能按扫描
  次数累计。
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
  其余 scope 照常写入；(4) 跨 root 从不重绑定；相似度、标题、时长从不用于推断身份。写入前先腾出被别的曲目持有的
  来源身份（互换、轮换与处理顺序无关）。重绑定按候选逐个短事务提交，依据的是完整的遍历证据；取消后已提交的保留，没写到
  的候选保持原状。每次候选写入替换 current decisions、grouping evidence、credits 和 track observations，不按重扫次数追加。
- **Release 身份**（阶段 I）：候选已规划沿用的曲目中严格过半当前属于同一个既有 Release 时沿用它；多个候选争同一个
  Release 时重叠多者得，平局时候选键等于该 Release 当前锚点者（留在原位置的一方）优先，再按候选键升序；落选或无倾向的
  候选写入时新建 Release 与 ReleaseGroup。`candidate_anchor` 只记录最近一次的位置，不参与查找、不要求唯一。因此改名、
  移动、增删少量文件、最小来源变化都不换 Release ID；新增文件多于原有（如 2 轨加 3 轨）或整体被替换时新建 Release。
- 身份规划的诊断写入 `scan_diagnostics`（沿用每次扫描最多 100 条的上限，安全相对路径，无绝对路径）：
  `identity_ambiguous`、`content_replaced`、`identity_deferred`、`fingerprint_failure`；每个 root 规划结束记一条聚合
  INFO 日志 `library.scan.identity_resolved`，只含计数。
- CUE 引用可以位于 sheet 的父目录或兄弟目录，只要仍在显式注册根 containment 内；
  staging 必须先按规范化 `cue_parent_relative_path` 与物理 observation 对齐 scope，
  organizer 再仅在同一 candidate 和明确父子关系内消除虚拟/物理重复。不得仅按 sheet
  所在目录、标题或时长模糊去重。扫描前使用解析 symlink 后的 `Stat` 验证媒体节点为
  regular file，FIFO、设备和逃逸链接不得传给 parser。
- parser 默认的 track/disc `1` 必须带 inferred 标记；organizer 只对缺失或 inferred
  位置应用 Disc/CD/Disk 目录与稳定路径 fallback，显式标签永远优先。CUE sheet 与
  track PERFORMER 的 provenance 分别为 `cue_sheet` 和 `cue_track`，构造每条虚拟 Track
  时复制 `InferredFields`/`FieldSources` map。
- Track artist 与 composer/conductor/performer/producer 使用同一组保守拆分规则：明确的
  `; `、`；`、` / `、`feat.`、`ft.`、`with`、`、` 可拆分；紧凑 `/`、`,`、`&`
  只按已固化规则处理，`Simon & Garfunkel`、`Earth, Wind & Fire`、`AC/DC` 等固定组合
  保持整体。M5 ⑤移除硬编码别名，只按原写法精确去重、排序并以 ` / ` 连接；名字键与主显示名由artistgraph解析，禁止相似度猜人。
  原始重复标签、种子、同事务解析及重扫不写人工意图的完整规则见[艺人合同](./artist-contracts.md)。
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
  - 每个目录读一次（`os.ReadDir`），文件名不区分大小写：名字表与优先级不变（`cover.{jpg,jpeg,png,webp}`、`folder.{jpg,jpeg,png}`、
    `front.{jpg,jpeg,png}`），大小写不同的同名文件里全小写的优先，其余按字节序。
  - 每个命名文件仍要通过安全检查：不是符号链接、是普通文件、大小 1 字节到 16 MiB、读取成功、`artwork.ValidateImage` 复用受管存储的大小、格式与尺寸校验。
    无效的命名文件记下第一个错误后继续找；全部目录找完仍没有可用封面时才返回 `errArtworkSource` 包装的错误（扫描诊断 `artwork_unreadable`）。
    目录不存在不算错误，其他读目录错误按同样方式记录。
  - 没有迁移：重扫会对每个候选重新发现封面，下一次扫描即绑定或移除。

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
| 多艺人/credit 含明确分隔符或明确别名 | 按保守规则拆分、原写法去重；身份同键归一由后端投影负责，固定组合名不得误拆 |
| 相同 album 的部分 Track 缺少 AlbumArtist，且其它 Track 只有一个明确值 | 保持同一候选，明确 AlbumArtist 优先于 Track Artist |
| 命名 folder artwork 非普通文件、为空、超限或格式损坏，且所有查找目录里没有别的可用封面 | `artwork_unreadable`，root 仍完整，保留已有关系，不解释为封面删除 |
| 前一个目录里的命名封面无效，后一个目录里有可用封面 | 用后一个目录的封面，不报错 |
| 查找目录不存在 | 跳过该目录，不报错 |
| 读查找目录失败（非“不存在”，如权限） | 记为错误并继续找下一个目录；全部找完仍没有可用封面才 `artwork_unreadable`；root 仍完整 |
| 发行目录与曲目目录都有封面（如严格多碟的父目录与 `CD1/`） | 先找到的发行目录（父目录）胜出 |
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
| 候选已规划沿用的曲目严格过半属于同一 Release | 沿用该 Release UUID；不过半（如 2 轨加 3 轨、1 轨加 1 轨）则新建 Release 与 ReleaseGroup |
| 扫描成功且所有 root 完整 | 允许负向 `missing` 对账 |
| 非音频附件 | 忽略，不改变扫描完整性 |

## 5. 正确、基准与错误案例

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
- 正确：`Artist Beta, Artist Alpha` 作为 Track artist 被拆分后以稳定顺序展示；已登记的
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

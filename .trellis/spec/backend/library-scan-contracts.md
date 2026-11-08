# 目录与扫描流程合同

## 1. 范围与触发

目录生命周期、幂等及扫描（遍历、暂存、身份规划、Release Graph、CUE、抓轨、署名与封面）遵守本合同。扫描状态、持久化取消与跨进程协调见
[扫描协调合同](./scan-coordination-contracts.md)；发行读取接口见 [Catalog REST 合同](./catalog-rest-contracts.md)；内嵌歌词的提取与保存见[歌词合同](./lyrics-contracts.md)；
通用请求与响应、迁移执行器与 CI 见 [Core 0 当前运行合同](./core0-runtime-contracts.md)。

M5 新增全根预计数、逐根加权进度与事务内变更记账；候选/封面/终态 missing 均遵循“全部既有发行升序锁 → 独立重读 → 组升序共享锁”，不得持组补发行锁。反馈锁失败降级不阻断扫描，详见 [扫描反馈合同](./scan-feedback-contracts.md)。

## 2. 接口签名

- 目录新增响应：`{ "id": string, "name": string, "status": "active" | "disabled", "revision": integer }`。
- 目录列表项目：`id`、安全 basename `path`/`name`、`status`、`revision`、时间戳；
  不返回原始绝对路径。

## 3. 合同

### 目录新增与操作幂等

- 目录新增对已存在路径执行幂等 upsert，但不隐式恢复 disabled 状态；响应和
  Operation Journal 使用数据库真实 `status`/`revision`。
- 目录新增、停用、恢复必须带 `Idempotency-Key`，缺失返回 `400 invalid_input`。键的作用域是
  actor 加操作类型；请求指纹包含目标身份：新增为规范化路径，停用/恢复为小写路由 root ID 加
  请求参数（`expected_revision` 等）。三个入口共用 `beginRootOperation`：先开启 READ COMMITTED
  事务并取得该键的事务级 advisory lock，再在事务内查记录，因此同键请求串行执行。
  指纹一致时返回首次的状态码和响应（新增 `201`，其余 `200`），不重复执行；指纹不一致时返回
  `409 idempotency_conflict` 且无副作用；同键并发只执行一次，其余请求回放首次记录。
  路由 root ID 格式错误返回 `400 invalid_id`，不进入幂等流程。只有成功的操作写记录，失败的
  尝试不占用幂等键。2026-09-29 起指纹含目标；此前记录的停用/恢复键不做兼容，升级后重试
  返回 `409 idempotency_conflict`（开发阶段裁决，见 F2 任务）。

### 扫描完整性、暂存、身份、CUE 与封面

- 扫描只读取 `active` roots。unsupported 音频候选、CUE 语法/编码错误、
  `unsafe`/`unchecked` 引用、权限和遍历错误将 root 标记为不完整；只有全局
  `succeeded` 才允许 `missing` 对账。已经安全解析但目标不存在的 CUE 引用，以及
  缺少 `INDEX 01` 的 CUE track，只写有界 `cue_reference` 诊断并跳过不可用虚拟 Track，
  不单独阻断其它确定输入完成扫描；不得为其伪造父来源或零偏移 Track。非音频附件
  （例如 `.jpg`、`.png`、`.txt`）不产生 unsupported 音频诊断。
- CUE `FILE` 目标不存在时，先在同目录按同名 stem 依次查找 `.flac`、`.mp3`、`.ogg`、
  `.opus`、`.wav`、`.m4a`（扩展名不区分大小写），命中即视为 `present`，
  `CueFileReference.Path` / `CueTrack.ReferencedFile` 改为实际文件并照常执行包含根与
  symlink 检查；都找不到才按“目标不存在”处理。用于抓轨 CUE 写 `.wav`、实际已转为 FLAC 的情况。
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
  没有配对到别处时置为 `missing`，记 `content_replaced`；(2) 只有该 root 遍历完整（未取消，没有遍历、解析、暂存失败，
  也没有指纹或 scope 失败）时，没有匹配到身份的观察才与本 root 中本轮消失（present、有指纹、未被计划沿用）的曲目
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
  标签按 `/、&` 拆子标签，先整体查角色/乐器表，未命中再按连续中文段、拉丁段查表；至少一项命中才是署名行，首次非署名行结束整个块。
  所有命中项均产出；没有细分的角色 detail 为空，带细分的工种与乐器保留命中子标签原文（如“混音工程师”“Guitar”），不改成标签来源的规范键。
  名字按共用规则拆分并去掉 `@` 起的团体/工作室后缀（去后为空则保留原文），`Source="lyrics"`；某角色已有 tag 署名时，该角色全部歌词署名丢弃。
  现分支尚无④增量扫描，下一次扫描重新解析；不新增 CommonVersion，不再提升⑥已升到3/2的艺人图/搜索投影版本，不改 textnorm.Version。
- `AudioObservation.Compilation bool` 经 scanner 原样传给 `SourceObservation.Compilation`（JSON `compilation,omitempty`）。
  Vorbis 的 `COMPILATION/ITUNESCOMPILATION`、ID3 的 `TCMP/TXXX:COMPILATION`、M4A 的 `----:COMPILATION` 去空白、小写后任一为
  `1/yes/true/compilation` 即为真；M4A `cpil` 为单字节非零整数时为真，0为假。
  organizer 保留真实专辑艺人；否则发行艺人是合辑占位词或非 `folder` 观察的标志严格过半时，决定 `artist="群星"`、清除专辑艺人决定，
  保留原 artist 候选，写 `source=compilation`、`rule_id=compilation_v1`、`confidence=high`、`action=auto_apply`。不改变 partition/分组。
- 本版 `Year==0` 时才按 `ORIGINALDATE → ORIGINALYEAR → TDOR → TORY → TXXX:ORIGINALDATE → TXXX:ORIGINALYEAR → ----:ORIGINALDATE → ----:ORIGINALYEAR`
  取首个可解析年份；不另存原始年份字段。流派仍遵守[多流派合同](./genre-contracts.md)，不随艺人规则改变。
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
  旧 key 引用检查和文件删除错误都必须向上返回，使扫描进入 `incomplete`。
- 封面来源与文件夹封面发现（F4，2026-10-03）：
  - 内嵌封面优先：按碟序、碟内按位置取第一首带内嵌封面的曲目；没有才找文件夹封面。
  - 文件夹封面按目录顺序查找（`ingest.OrganizedCandidate.ArtworkDirectories`，相对注册根）：先是发行目录 `Anchor.Scope`，
    再按碟序、曲序列出各曲目观察的 `Directory`，去重。CUE 虚拟轨的 `Directory` 是 sheet 所在目录，不能从虚拟路径
    `sheet.cue#track-N@parent#index-M` 用 `filepath.Dir` 推出。严格多碟专辑的发行目录是各碟的父目录，所以顺序是
    `A` → `A/CD1` → `A/CD2`；Box 叶子只查自己的目录，不查 Box 父目录；CUE 的发行目录是 sheet 目录，不查跨目录的音频目录。
    查找不会越出发行自己的目录，避免把盒装或公共目录里的封面绑给每张专辑。
  - 每个目录读一次（`os.ReadDir`），文件名不区分大小写：名字表与优先级不变（`cover.{jpg,jpeg,png,webp}`、`folder.{jpg,jpeg,png}`、
    `front.{jpg,jpeg,png}`），大小写不同的同名文件里全小写的优先，其余按字节序。
  - 每个命名文件仍要通过安全检查：不是符号链接、是普通文件、大小 1 字节到 16 MiB、读取成功、`artwork.SniffMIME` 识别格式。
    无效的命名文件记下第一个错误后继续找；全部目录找完仍没有可用封面时才返回该错误（`artwork_failure`）。
    目录不存在不算错误，其他读目录错误按同样方式记录。
  - 没有迁移：重扫会对每个候选重新发现封面，下一次扫描即绑定或移除。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| 同一幂等键用于另一个目录，或同一目录的不同请求参数 | `409 idempotency_conflict`；不执行、不写记录，也不返回前一个请求的结果 |
| 同一幂等键的并发请求（新增、停用、恢复） | 串行处理，只执行一次，其余请求回放首次记录的状态码和响应 |
| 停用/恢复的路由目录 ID 不是 UUID 形式 | `400 invalid_id`，不进入幂等流程，不写操作记录 |
| 重复注册 disabled root | 返回 `status=disabled` 与真实 revision，不隐式恢复 |
| unsupported 音频、CUE 语法/编码、`unsafe`/`unchecked` 引用或遍历错误 | 有界诊断，扫描 `incomplete`/`failed`，禁止 `missing` |
| CUE 引用目标不存在或 Track 缺少 `INDEX 01` | 写 `cue_reference` 诊断并跳过不可用虚拟 Track；不单独令扫描 incomplete，也不伪造父来源/offset |
| 初始或终态 staging 清理失败 | root 不完整并记录 `staging_write_failure`；旧 observation 不得混入本次候选 |
| 媒体节点不是 regular file | 不调用 parser，记录 `non_regular_file` 并令扫描不完整 |
| CUE 显式父来源跨 sheet 目录但仍在注册根内 | 对齐到父来源 scope，保留稳定虚拟身份并按明确关系去重 |
| rip log 无明确签名、签名晚于 64 KiB 或为 symlink | 不产生 CD 语义，不读取超出预算的内容 |
| 明确 rip log 与 `folder` 补充值并存 | 选定 CD 并把 decision provenance 提升为 `rip_log`；不得保留重复字段决定 |
| 明确 rip log 与 tag/CUE/音频规格决定冲突 | 保留明确来源，不允许抓轨日志覆盖 |
| 艺人/credit 含换行或 NUL | 先切段再清理，不能把两个名字粘连；固定组合与拉丁姓名保护按第3节执行 |
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
| 命名 folder artwork 非普通文件、为空、超限或格式损坏，且所有查找目录里没有别的可用封面 | `artwork_failure`，保留已有关系，不解释为封面删除 |
| 前一个目录里的命名封面无效，后一个目录里有可用封面 | 用后一个目录的封面，不报错 |
| 查找目录不存在 | 跳过该目录，不报错 |
| 读查找目录失败（非“不存在”，如权限） | 记为错误并继续找下一个目录；全部找完仍没有可用封面才 `artwork_failure` |
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

## 5–7. 案例、必需测试与示例

见[目录与扫描流程验证与示例](./library-scan-verification.md)，保留原第5–7节编号。

停用目录拒绝新的播放请求，已建立传输不因此切断，专辑仍可浏览；具体语义见[播放合同](./playback-contracts.md)。

## M5 ④搜索投影挂接

扫描候选及修正/清除/撤销/claim 双方在同事务刷新目标代次搜索投影；CATP 先于业务行锁，失败回滚，后台持锁重读避免覆盖新值。扫描不写治理表，missing 可见性读取实时过滤。详见[搜索合同](./search-contracts.md)。

# 归组与发行：候选、碟号、文件名、时长与指纹

从[目录与扫描流程合同](./library-scan-contracts.md)拆出：§3 的发行候选、抓轨与封面，碟号、独占合成与介质标题，文件名推断与同目录归组（③），MP3 扫描时长，新格式内容指纹；章节编号沿用原合同。

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

- 归组版本：④ 尚未进入 main，本次整合②③后 `GroupingVersion` 仍从 1 起；后续已发布规则变化按[升版本合同](./library-scan-incremental.md#版本号与升版本规则)执行。候选签名与封面发现共用四轮候选列举。

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

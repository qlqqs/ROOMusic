# 归组与发行：候选、碟号、文件名、时长与指纹

从[目录与扫描流程合同](./library-scan-contracts.md)拆出：§3 的发行候选、抓轨与封面，碟目录与介质标题，文件名推断与同目录归组（③），MP3 扫描时长，新格式内容指纹；章节编号沿用原合同。

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
- 封面来源与文件夹封面发现（F4，2026-10-03；2026-11-15 改为文件夹优先分档）：
  - 分档取第一张有效封面（`library.saveArtworkForRelease` → `selectArtwork`）：
    1. 原目录精确名单；
    2. 内嵌封面：`ingest.OrganizedCandidate.EmbeddedArtworks()` 按碟序、碟内位置逐个尝试，同一文件内正面封面（图片类型 3）优先，见 parser 规则；
    3. 原目录 `front` 子串 → 附属子目录精确名 → 附属子目录 `front` 子串；
  - 歧义候选（`same_dir_split`、`loose_album`、`loose_unknown`，同一目录或注册根里有多张专辑）只用第 2 档；不使用任何文件夹图片，没有内嵌封面就不绑定。同一混放目录分出多张专辑时，格式候选也沿用此规则。
  - 暂存内嵌封面时，根目录无专辑证据的文件按路径保留。子目录物理文件无专辑证据且标题、曲目艺人均来自标签时，也按路径保留，供每首散曲独立取用。其余按目录、专辑与格式去重。
  - 原目录（`ingest.OrganizedCandidate.ArtworkDirectories`，相对注册根）：先是发行目录 `Anchor.Scope`，
    再按碟序、曲序列出各曲目观察的 `Directory`，去重。CUE 虚拟轨的 `Directory` 是 sheet 所在目录，不能从虚拟路径
    `sheet.cue#track-N@parent#index-M` 用 `filepath.Dir` 推出。严格多碟专辑的发行目录是各碟的父目录，所以顺序是
    `A` → `A/CD1` → `A/CD2`；CUE 的发行目录是 sheet 目录，不查跨目录的音频目录。
    就近不继承：不越出发行自己的目录，艺人目录等上级目录里的图片不绑给下面的专辑。
  - 文件夹候选由 `listFolderArtworkCandidates(root, candidate) iter.Seq2[folderArtworkCandidate, error]` 惰性列出，每项带 `Tier`（1/3）。
    原目录顺序不变；每目录只读一次。精确名单仍为 `cover.{jpg,jpeg,png,webp}`、`folder.{jpg,jpeg,png}`、`front.{jpg,jpeg,png}`，
    不区分大小写，同名变体全小写优先，其余字节序。包含规则只认 `jpg/jpeg/png/webp`，按文件名字节序。
    附属目录仅限直接子目录 `scans/artwork/art/covers`（不区分大小写），按原目录顺序、子目录名字节序，不递归、不跟随目录符号链接，不含 `extras` 或中文名。
    第 3 档的包含式文件查找与附属目录发现复用该根的 `registeredRoot.filter`：目录按根内相对路径和目录名调用 `skipDir`，文件调用 `skipFile`；不进入用户排除、隐藏或系统目录，不采用 `.front.jpg` 等被过滤文件，不另建过滤规则。注册根自身不参与过滤。
  - 每个命名文件仍要通过安全检查：不是符号链接、是普通文件、大小 1 字节到 16 MiB、读取成功、`artwork.ValidateImage` 复用受管存储的大小、格式与尺寸校验；内嵌字节同样先经 `ValidateImage`。
    任一候选无效（含 deferred 现读失败）记下第一个错误后继续本档下一个、再下一档；全部找完仍没有可用封面时才返回 `errArtworkSource` 包装的错误（扫描诊断 `artwork_unreadable`，保留原关系）。
    MIME 以字节探测为准，声明值（如 `image/jpg`、M4A 类型码 0 映射的 `application/octet-stream`）不再导致拒绝；`artwork.Store.Save` / `SaveWithFeedback` 不接收 MIME 参数。
    目录不存在不算错误，其他读目录错误按同样方式记录。
  - 没有迁移：重扫会对每个候选重新发现封面，下一次扫描即绑定或移除。

### 碟目录与介质标题

- `ingest.parseDiscMarker(name) (discMarker,bool)` 输出 `Number/Prefix/Suffix/AtStart`：从左向右取首个有效 `disc/disk/cd`（忽略大小写），支持括号、`CD(1)`、`of N`/`/N`；非 ASCII 单词边界，编号限 1–99，认整数、英文 one–twenty、规范罗马数字。无效编号继续向右找；纯数字目录、中文碟号、`[CD]`、`{…, CD}`、`BACD-0001` 不认。
- Prefix/Suffix 去空白、分隔符与落单括号，保留配对括号。`stripAlbumDiscMarker` 只去结构碟标记，保留其两侧的非空文字（含版本或副标题）；结果空则不剥。只改 `albumPartition` 和 Release 标题比较值，不改 observation 的原始 album。物理观察缺少权威碟号时补剥出的编号，来源 `album_title`，仍 inferred；不改 parser。
- 编号碟目录和单独的 Bonus、特典等附加碟目录进入 `disc-parent:<父>`。先用 `AlignCueStagingScopes` 对齐虚轨与父来源，再用 `AlignDiscStagingScopes` 合入非空父目录的直属观察。注册根的散曲仍有自己的范围，普通兄弟目录独立。
- 正碟采用目录号，附加碟按标题、原号和路径排序，从最大正碟之后依次排列。介质标题取碟目录后缀，附加碟可用前缀。来源观察保留明确标签号。碟目录无须独占父目录，成员与普通专辑一起接受连续性检验。缺专辑名时采用唯一的目录前缀或逻辑父目录名。
- 同一逻辑专辑目录直属以及 Art、Artwork、Scans、Extras 的直接子文件纳入 `release_attachments`。遵守根过滤、只取普通文件，PDF 保存相对路径，JPEG、PNG、TIFF、BMP、GIF、WebP 另外保存解码格式及像素尺寸，详情见增量扫描合同。

### 文件名推断与同目录归组（③）

- 规则由 `catalog/ingest` 拥有，不改 parser 或 `SourceObservation` 结构。`OrganizeObservations` 在 CUE 分组解析、专辑名碟号预处理之后，按同一 scope 内的物理 `Directory` 推断文件名；CUE 虚拟轨不参与文件名预处理或目录多数计数。推断证据保留供连续性检验，主字段仅填 inferred 的缺失值。
- 文件名去扩展名后的 stem 按以下五级依次尝试，只有得到正轨号才命中，零值视为没有：
  1. 去首尾空白后为 1–2 位纯数字，直接作轨号。
  2. `^\s*(?:(\d{1,2})[\s_\-\.]+)?(\d{1,2})(?:\D.*)?$`：可选第 1 组为碟、第 2 组为轨；也允许只有前导轨号。
  3. 跳过前导空白，连续数字恰好 3 位拆为 1 位碟 + 2 位轨；恰好 4 位且首位为 `0` 才拆为 2 位碟 + 2 位轨。`1999`、`2046` 这类年份不拆。
  4. 去尾部空白后末尾独立的 1–2 位数字，前一个字符不能是数字，不把更长数字截成两位。
  5. 不区分大小写的 `(?:^|[^a-z])track[\s_.\-]*(\d{1,2})(?:\D|$)`，允许 `Track 07`。
- 第 2 级使用短横线、点或下划线明确分隔的碟轨直接采信，包括单文件 `1-03`。仅空白分隔的第 2 级、第 3 级的三位与四位分别计为三种写法；至少 2 个文件、且超过目录物理文件的一半使用同一种写法才采信。不满足时，第 2 级降为第 1 组作轨号，第 3 级作废并继续尝试第 4、5 级；不按碟目录数量限制编号。
- 推断成功的轨号、碟号与艺人沿用来源 `filename_fallback`，仍标 inferred；文件名碟号不能覆盖 `album_title`。最终非合成候选碟号优先级为标签 → CUE → 专辑名 → 碟目录 → 文件名 → 1。一个介质内文件名轨号彼此重复，或与标签轨号重复时，该介质**全部**文件名轨号作废，继续保留真实标签号。
- `naturalLess` 比较连续数字段和非数字段：数字去前导零后先比长度、再比字面，不转整数；数值相同则前导零少者在前；非数字按字节比较。同碟内先按真实曲号，缺号排在最后，同号或无号按文件名自然序、来源路径排。顺序保存至 `tracks.sort_position`，缺号的 `tracks.position=0`，省略曲号观察。
- 标题仅在 inferred 且来源为 `filename_fallback` 时清洗：去采信编号前缀及紧随的空白、点、下划线、短横线，再去首尾空白，结果空则保留 stem。纯数字前缀为整个 stem；第 2、3 级取到编号末尾；第 5 级只在匹配从开头开始时去前缀；第 4 级不去末尾数字。
- 在上述文件名标题中，仅当艺人 inferred 且来源为 `display_fallback`，并恰好含一个 ` - `、两边非空且左边不全为数字时，拆为艺人与标题。两者仍 inferred；写入展示复用 `metadata.CanonicalTrackArtist`，不复制艺人拆分规则。已有标题/艺人标签不被覆盖。`artistgraph` 接受来源为 `filename_fallback` 的 inferred 曲目艺人，其他 inferred 曲目艺人仍跳过，文件名艺人可以进入艺人页与收藏。判定使用 `track_observations` 中 `field_name='artist'` 那行的 `source_kind`：预处理写 `FieldSources["artist"]`，经 `fieldObservations()` 取为 `field.SourceKind`，由 `PersistCandidate` 写入该列；不是 `tracks.source_kind` 的物理/CUE 来源；O/T 两线分别从 2 升到 3，合并后统一 `artistgraph.Version=4`，确保两线的艺人投影变化均触发重整。
- 混放目录依次读四套完整碟轨证据：标签/CUE、文件名、显式字段逐项补文件名、单碟文件名曲号。每碟必须恰好 1..N，第一碟存在，整目录至少两首；其中一套通过即收为一张。整目录失败再按格式独立复验（允许单曲），只拆失败的格式桶。全有真实名按专辑名分，含缺名曲按真实专辑艺人（缺时用曲目艺人）与专辑名分，两者皆缺按来源路径。文件内的 MBID 不参与归组。
- `textnorm.MergeKey` 对完整专辑名折叠大小写、去组合变音、符号、标点和空白；保留括号内全部文字。Album 和 Album (Deluxe) 在按名拆时不同。目录和原专辑名的括号叶词按路径稳定去重，存 `releases.scan_version_terms`；纯年份、EP、单曲及原声标记排除，数组独立于锚点与展示字段。
- 不按重启的曲号序列推碟。黑胶 A/B、C/D 仍按对分碟，后面的曲位接在前一面的最大曲号后。
- 标量字段以真实证据优先。唯一最高票取胜；最高票相等保留候选值，决定 `selected_value` 为 SQL NULL，`uncertain_apply/tied_candidates` 表示有冲突。目录与抓轨证据不覆盖平票。多流派取并集。缺明确专辑艺人时每曲首位真实艺人一票，严格过半选该艺人，否则两至四位按票数、姓名组成联合署名，五位起用群星；补值标记 `artist_distribution`。
- 目录支持“艺人 - 年份 - 专辑”“年份 - 专辑”“[年份] 专辑”。目录与文件名产生的推断文字清除独立的码率、采样率与位深组合，如 320kbps、24bit-96kHz、16-44。WEB、APE、WAV、MP3、FLAC、ALAC、CD-Rip 等词仅在方括号、圆括号内整段都是格式标记，或与另一格式标记相邻时清除。保留标题内的单词及版本字样。原标签保留原文。
- `GroupingVersion=8`。已有曲目按来源配对，发行只有满足严格过半才沿用 UUID。等量旧发行合为新 UUID 时，旧对应和人工修正保留在原 UUID；这些记录仅在其原 UUID 上继续生效，冲突值不自动复制。旧组腾空时沿用收藏转移合同。
- 格式键取小写 Codec 或扩展名。单标题目录沿用整组重号、各格式均完整时才切版本的规则，锚点为 `format:<格式>`。混放目录按各份判定，同目录不同专辑的格式批次以成员路径区分。已沿用的发行保留自己的组。
- 普通子目录先沿用现有格式切分，再在各组内判断散曲。整组文件均无真实专辑标签，且每首的标题与曲目艺人均来自标签时，核对曲序标签和 `prepareFilenameEvidence` 采信的文件名轨号：整组均无编号，或每首都有相同编号（例如全为 1）才可拆分。任一文件的两种编号冲突也保留原归组。目录名被 `parseFolderMetadata` 识别、CUE 虚拟轨或合入 CUE 的物理轨、多碟和 `same_dir_split` 均沿用既有归组。
- 满足条件时，每个文件各成一个 `loose_unknown` 发行，单文件目录也适用。锚点 Scope 为目录，Partition 为 `source:<规范化相对路径>`。标题沿用根目录散曲的“未知专辑”，各单曲独立建组。旧目录发行重扫拆开时，各单曲与旧发行重叠一轨，候选键最小者沿用旧发行 ID 和组，曲目沿用原 ID。
- `OrganizedCandidate.VersionGroup string` 标识本次整理的版本批；`JoinGroupID string` 由 scanner 填，不由 organizer 生成。`PlannedVersionGroup(ctx context.Context, database queryer, scanID, rootID string, candidates []OrganizedCandidate) (string,error)` 返回已规划沿用 Release 的候选中候选键最小者所在的组，全部新建则为空；`ReleaseGroupOf(ctx context.Context, database queryer, releaseID string) (string,error)` 返回发行的组。查询错误向上传递，不伪造空组。
- scanner 对 scope 内每个 `VersionGroup` 先定目标组，逐候选填 `JoinGroupID`、独立短事务写入；目标为空时从第一个成功写入发行取组，供后续新建版本使用。`persistOneCandidate` 返回 Release ID；仅 `plannedReleaseID == "" && JoinGroupID != ""` 时新建 Release 引用已有组而不新建组。沿用 Release 的组永不移动，即使管理员已把两个版本分到不同组。目标组读取或候选保存失败按 `catalog_write_failure` 令根不完整，已提交候选保留。
- 新建版本引用已有组只取外键共享锁，不改组的 `default_release_id` / `edit_revision`，不额外调整代表版本或搬移人工修正；既有发行锁与反馈锁顺序仍按[扫描反馈合同](./scan-feedback-contracts.md)。腾空旧组的收藏转移照常生效。

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

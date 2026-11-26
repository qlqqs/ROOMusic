# 扫描读文件技术设计

以 `prd.md` 的范围表为交付边界。扫描读出文件事实，按原有候选成员判定整张专辑的旧编码，随曲目事务保存。新增事实只有播客和有声书标记参与整理入队判断。设计依据为父任务 `.trellis/tasks/11-26-curation-chain-review/prd.md`、同目录 `research/scan-design-draft.md` 第 11.3、14、15.2 节及 `docs/architecture/adr/0009-curation-chain-review.md` 第 3、4.1 节。

## 文件发现、稳定读取与身份（A1.1、A1.2）

1. `backend/internal/library/scan_filter.go` 统一候选判定，供 `scan_progress.go` 的预计数和 `scanner.go` 的正式遍历调用。先处理隐藏项、排除目录、链接边界与普通文件。音频候选取现有支持表、现有仅诊断表和空扩展名。CUE 走原来的文本解析；图片、文本等其余后缀跳过音频探测。封面和外置歌词仍由各自入口读取。CUE 明确引用的父文件也经过音频候选预筛。预计数只看文件名。
2. 在 `backend/internal/library/parser` 新增 `format.go`，按有限的头部读取返回规范格式键（沿用带点的格式版本键）。识别 FLAC 的 fLaC、MP3 帧头、OggS 首个识别包的 Vorbis/Opus、RIFF/WAVE、受支持的 ftyp 品牌、MAC 、wvpk、FORM/AIFF 或 AIFC、DSD 空格、FRM8/DSD。复用现有 MPEG 头、APE 的 ID3 前缀长度、MP4 atom 与 Ogg 包解析；ID3 前缀后先辨有效负载，防止把带 ID3 的 APE 或 ADTS 当作 MP3。完整的 ID3v2 标签恰好到达文件尾时，继续按 MP3 元数据解析器接受空音频负载。标头已识别、结构损坏时由对应解析器报错。读不到足以识别的字节且正常到达文件尾，计 `unsupported_format`；打开或实际读失败、已识别的坏流计 `permission_error` 或 `parse_failure`；已有 `ErrUnsupportedEncoding` 仍计 `unsupported_format`。沿用每轮 100 条诊断和保住来源的机制。
3. 保留 `ParseAudioFile(path)`、`ContentFingerprint(ctx,path)` 和已打开句柄的 `ContentFingerprintOf` 调用合同；成功观察带真实格式键。指纹分派与标签解析使用同一探测结果，对已打开的文件用 ReaderAt，保留各格式现有指纹算法及前缀。物理、CUE 父文件、延迟内嵌封面重读都走同一判型。路径仍按实际相对路径标识。归组需要的 Codec 正常来自解析器，格式分组策略归子任务 3。
4. `scan_pipeline.go` 的 worker 在未命中缓存时紧贴实际读取取一次路径 Stat，读完（含报错）再取一次，比较大小和完整精度的 ModTime；写暂存与缓存使用这次读前的 FileInfo。变化优先于半成品引起的解析错误：本槽位不给缓存和暂存写新观察，按已有 `HeldFile` 或 `HeldCueSheet` 保住旧来源，计一次跳过，直接等下次扫描。CUE 文本读取也前后比较；父音频变化则整张 sheet 的虚拟轨一起保住。变化没有成功结果可发布，沿用缓存的未见项清理。共用父文件在本轮已判为变化时，后续物理或 CUE 引用沿用该跳过结果。监听、轮询、重扫触发由子任务 4 提供。
5. 身份规划可能在暂存后另读指纹；`library/identity_plan.go` 与 `catalog/ingest/identity_plan.go` 扩展现有 Fingerprinter 边界，传入暂存文件状态，在读取前核对已暂存的大小、微秒 mtime，读取前后再比完整 mtime。若变化，丢弃该路径关联的暂存结果（含父文件的 CUE sheet），登记保住，失效本轮该路径解析缓存；在受影响 scope 内重新用剩余观察规划候选后才分配身份。普通指纹 I/O 错误沿用已有失败路径。不能把变化当空指纹成功写入。延迟封面同样校验所选来源状态，读中变化则保留既有绑定；没有创建半张封面的写入。

## 文件事实与存储（A1.2、B2 注释）

`backend/internal/catalog/metadata/scan_tags.go` 定义有类型的 `ScanTags`，由 `parser.AudioObservation` 和 `ingest.SourceObservation` 共用。路径为解析器 → 原始观察缓存 → 物理或 CUE 观察 → 暂存 JSON → 候选事务中的曲目。以 `tracks.scan_tags JSONB`（可空）保存当前文件的事实快照，插入与更新都整体替换，删除标签后旧键随之消失。CUE 虚拟轨所带的快照解释为 `cue_parent_relative_path` 的文件标签，不把整轨的总曲数、注释或同步歌词当成某一首虚拟轨的显示数据。单轨 CUE 合并时取实际物理来源的快照。现有 `tracks.lyrics`、`year`、表示载体的 `MediaType` 继续走各自路径。

选 JSONB 是因为这些稀疏值与曲目同寿命，包含部分日期、列表和三态布尔，只有媒体标志需要做集合查询。逐字段观察的 value 是文本，且整理已经读取其中的现有字段；用单独的 JSON 列可避免长歌词和注释混入该输入。缓存有大小预算并会清理，不能作为长期存储。暂存、缓存和候选元数据签名已有 JSON 编解码，直接带入新增字段；不存全量标签字典。数据库查询不向通用 API 暴露此列。

| JSON 键 | 读取和写入约定 |
| --- | --- |
| `track_total`、`disc_total` | 整数。按稿 14.6 先取编号对的分母（TRCK/TRACK/TRACKNUMBER；TPOS/DISCNUMBER/DISC/MEDIA/DISC #），再取 TRACKTOTAL、TRACKCOUNT、TOTALTRACKS 或 DISCTOTAL、DISCCOUNT、TOTALDISCS、DISCC，首个非空别名解析失败即止。规范 TRACKCOUNT/MEDIACOUNT 按规范层覆盖；MP4 trkn/disk 同时取大端编号和总数。0 表示未提供；只有提供了非零总碟数且小于碟号才上调。读取保留原始总数，文件明确的碟号（含按既有规则识出的碟号）确定后再校正持久值。 |
| `release_date`、`original_release_date`、`recording_date`、`recording_start_date`、`recording_end_date` | 对象保存 year 和实际提供的 month、day。依稿 14.8，发行取 TDOR/TDRC/TDRL/RELEASED/DATE/YEAR，规范 RELEASEDATE 覆盖；MP4 ©day 对应 DATE，旧年份键 TYER 衔接 YEAR。原始发行按 ORIGINALDATE/TDOR/TORY/ORIGINALRELEASEDATE 逐值覆盖。录制与演出的 DATE、START DATE、END DATE 各取稿中的四种带空格及紧凑写法。按四级解析区分缺键与坏值：月名/缩写/数字月在前、四位年在后（空格、斜杠、冒号），再试四位年，然后按固定英语区域解析标准年月日及带时间日期，丢弃时分秒，失败为空日期。年/月精度不补日，两段式数字月遵循稿 14.8.1。各键的失败/覆盖规则独立于已有 Year 的选择。 |
| `single`、`live`、`bootleg`、`ep`、`soundtrack`、`primary_version` | 可选布尔。按 ISSINGLE、LIVE、BOOTLEG、ISEXTENDEDPLAY、ISSOUNDTRACK、PRIMARYVERSION/BESTVERSION；末组取首个存在。Trim 转小写后只有 1、yes、true 为真，显式假与缺键分存。MEDIATYPE、MEDIA_TYPE、RELEASETYPE、RELEASE_TYPE、MUSICBRAINZ ALBUM TYPE 值等于 bootleg 可给出私录真，显式 BOOTLEG 再覆盖。 |
| `track_gain`、`album_gain`、`track_peak`、`album_peak` | 可选有限浮点，保留 0；键为 REPLAYGAIN_TRACK/ALBUM 与 _GAIN/_PEAK 的组合。按稿 14.9 对两种增益去 dB 后缀和空白；两种峰值读无单位数，按固定区域解析。 |
| `synced_lyrics` | SYLT、SYNCED LYRICS、LYRICS、©lyr、UNSYNCED LYRICS、USLT 等按稿 14.4.1 的键序逐值分类。文本沿用歌词编码、长度与换行处理；每个非空行 Trim 后检查行首的整数时间段 `[分:秒.小数]`，仅在 `4 × 命中数 > 3 × 非空行数` 时保存同步原文，同类后值覆盖。原生 ID3 SYLT 必须按帧规范解析描述、编码、时间单位、带时间的文本事件（毫秒，或按 MPEG 帧采样数和采样率换算）再构造带时间的歌词；不把二进制帧当文本。现有普通歌词、外置 LRC 优先级与署名提取另循原合同；新同步字段只存。 |
| `comments` | 按读取次序存有序的键和值，保留重复标签和换行。ID3 COMM 按编码/语言/描述终止符取正文；Vorbis COMMENT、DESCRIPTION，MP4 ©cmt，APEv2 Comment 分别接入。MP4 现有 RawAtoms 的单值摘要不是完整注释，取 data 原文。复用各容器现有读取上限与文本解码，避免普通名字的截断吞掉注释里的条码。 |
| `podcast`、`audiobook` | 可并存。原始 PCST、ISPODCAST 存在或 TCON 首值 Trim 后精确为 Podcast 命中前者；PCSp、ISAUDIOBOOK 存在或首值为 Literature 命中后者。存在性不依赖内容真假，包括 MP4 pcst 原子。比较 TCON 区分大小写，先取原始值，后做流派归一。只移除作类型使用的首值，其余按既有多值规则解析。ID3 TCON 的纯编号及括号编号按现有 id3v1Genres（0–79）翻译，如 (17) 为 Rock；越界原文保留。 |

格式适配在 `backend/internal/library/parser/{mp3,m4a,tags,lyrics,id3_legacy,tag_finalization}.go`。新增 `scan_tags.go` 集中收集上述键的必要状态并收尾，用固定键优先级，使容器遍历顺序不改变不同别名的选择。ID3 TXXX 和 MP4 iTunes 自由键须过现有白名单入口；FLAC/Ogg/Opus 的 Vorbis 注释及 APE/WavPack 的 APEv2 文本共用 setAudioTag。捕获新事实要早于普通文本截断、去空值、歌词优先返回；同名重复键的现有显示语义照父任务 A1.2。

## 文字与候选（S2、S5、S6、A1.3、A1.5）

- `backend/internal/catalog/metadata/artist.go` 将 CanonicalArtistNames 的切段收敛到已有显式分隔器：半角分号无需尾空格，全角分号、两侧有空格的斜杠、有独立词边界的 feat. / ft. / with、顿号、换行、NUL。沿用括号保护、逐段长度控制、去重和既有展示排序。删除保护名单、姓氏/团体猜测、多词 & 规则与紧凑分隔递归。原始多值标签依然按 occurrence 保存。曲目、专辑艺人、署名和 artistgraph 共用这一入口。
- `metadata/genre.go` 将古典、古典音乐、古典乐加入现有 GenreKey 精确名单；古典跨界、印度古典、新古典均不命中。作品标签与其他语言名单仍按原规则。`ingest/filename_numbers.go` 删除文件名回退末尾的“艺人 - 标题”推断，保留开头曲号识别和 StripFormatNoise；如 `01 甲 - 乙 [FLAC].flac` 的曲名为“甲 - 乙”，曲目艺人为“未知艺术家”。目录专辑名及固定结构的目录证据仍由原路径提供。
- 编码选择以 organizer 已定的一个候选为单位，跨碟可共享，混放同目录的不同候选各自求解。仅在 `ingest/organizer.go` 的桶归属、锚点确定后、字段决定和署名派生前调用新增的 `ingest/legacy_encoding.go`；这个窄调用点与子任务 3 对接，身份规划和实际保存两次 OrganizeObservations 走同一逻辑。修改观察副本，原始缓存中保留原字节。
- 扩展 `metadata/legacy_encoding.go` 的无损往返检查，返回有序的“编码名、解出文字”对；保留现有 GBK、Big5、Shift-JIS、西文排除和供旧调用者取不同文字的接口。按编码身份求所有有效疑似字段候选集的交集；相同文字由不同编码产生时不能提前去重。输入取实际使用的 title、album、artist、album_artist 的原字节；按标签 occurrence 保存参与组合的艺人字节，ASCII 和正常 Unicode 字段不参加投票。CUE 覆盖父字段时移除对应的父编码证据。
- 唯一编码时在该候选内按此编码逐值解码；重新生成曲目/专辑署名及其字段来源，记录可搜索的 `legacy_encoding` 扫描来源，保留原标签证据。多解或交集为空时原样显示，受影响字段仍标 `garbled`；复用 `FieldAuthenticity` 屏蔽这些字段的整理搜索，编号等独立依据仍可用。将未决字段的原字节与可行编码名（含空交集）保存在扫描事实中，沿用 `legacy_bytes_*` 供已有消费者读取。子任务 8 可按整张专辑重建候选读法。
- `backend/internal/curation/program/queue_projection.go` 移除事务提交后的 LegacyFields 循环，删除 `backend/internal/curation/local_legacy.go` 及 `writer.go` 的专用写入通道。历史修正、撤销收据和既有人工层仍按原有依据校验/失效流程处理。扫描自身不依赖整理开关或模型设置，亦不新增整理写入。

## 播客、有声书的队列边界（A2.1）

在同一迁移增加仅供整理使用的关系视图 `curation_eligible_releases`，按现有可见发行条件并排除“任一 present 曲目 scan_tags.podcast 或 audiobook 为真”的发行。以 releases 为起点，通过关联 EXISTS 检查至少有一首现存曲目，通过 NOT EXISTS 排除带上述标记的现存曲目。两处均按 media.release_id 检查当前成员，分页和队列可按发行 ID 限定查询。旧曲目列为 NULL 时沿用可整理行为，首次重读得到标记后即生效。普通曲库可见性仍由 catalog 的现有视图决定。

本次只有两个消费者。`backend/internal/catalog/curation_evidence.go` 的 PageCurationReleases 使用资格视图分页；`backend/internal/curation/program/queue_query.go` 的公共候选查询使用同一视图，使取项和计数同时过滤已有工作记录。再次扫描清除标记后，发行自动恢复资格。模型队列由子任务 7、9 接入视图；写入前的稳定性复核沿用已有合同。

## 迁移、版本与回滚（A1.2、A4）

现有最大号为 `backend/migrations/0097_curation_source_retry.sql`。新文件 `backend/migrations/NNNN_scan_file_tags.sql` 和同名 `.down.sql` **合入时按当时最大号顺延**。上行加可空列和资格视图，无历史回填；下行先删依赖视图，再删列。库内原始文件、手工操作和曲目身份均不属于下行删除对象。

`backend/internal/library/parser/version.go` 仅把 CommonVersion 从 5 升到 6 一次；M4A 为 c6.f2，其余现有格式为 c6.f1。GroupingVersion 由子任务 3 单独管理。缓存写入真实格式及对应版本，查缓存先用路径、大小和微秒 mtime 定位，再按缓存里记录的真实格式校验版本；旧观察缺少真实格式时重读。若稳定读取返回 ErrUnknownFormat，在同一行用版本 `c<CommonVersion>.unknown` 和空 JSON 对象记录判型结果。大小、微秒 mtime 和通用版本匹配时直接重放 unsupported_format 诊断及来源保住，按缓存命中计数并登记 seen；任一项变化时重新判型。权限、I/O、已识别容器的解析错误和读取中变化均不写该结果。命中无需开文件头，未命中才探测。两类 SourceObservation 均携带真实格式的 ParserVersion，进入候选签名；解析一次、按已有身份规划保存，保留可延续的曲目/专辑编号、对应、收藏、手动修改。

回退旧程序时由既有迁移执行器使用库内 down SQL，旧 CommonVersion 的第一次扫描重新解析并重做候选；新字段在回退时丢弃，再升级可由源文件重建。只在新版识别出的错扩展名、无扩展名文件在旧扫描中分别按原来的失败或 missing 策略处理。发布说明由集成时注明首次扫描耗时与此降级影响。

## 规范交付

实施时同步 `.trellis/spec/backend/library-scan-pipeline.md`（DSF/DFF 已支持、候选与诊断、变化保住）、`library-scan-incremental.md`（更正原文 c4、注明基线 c5 和新 c6、真实格式校验）、`library-scan-tags.md`（字段表、编码、拆名、文件名、古典）、`library-scan-contracts.md`（路径过滤次序）。扫描调度、归组决策以及模型输入输出协议由对应子任务维护。

## 已定兼容决定

升级后第一次扫描重读一次，实际读取量因格式及缓存而异。已有 Ogg 包遍历、内容指纹和身份算法沿用现行实现；主代理更新父任务 A4。纯 ID3v2 文件按上述空负载规则兼容，现有合成正样本继续使用。

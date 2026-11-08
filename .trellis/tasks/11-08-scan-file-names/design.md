# ③ 文件名规则与同目录归组：技术设计

依据：[prd.md](./prd.md)；父任务 [prd.md](../11-08-scan-reference-port/prd.md)（复刻原则、跨项裁决“落选 Release 的数据”）；
[gap-analysis.md](../11-08-scan-reference-port/research/gap-analysis.md) G6、G7；参考设计 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)
12.2（1274–1340）、12.3（1341–1399）、21.12（2515–2567）、22.9（3396–3445）。前置：[② design.md](../11-08-scan-folder-names/design.md)（碟号解析器、碟号优先级、介质位置、收藏转移）。

## 1. 边界与归属

| 部分 | 归属 | 说明 |
| --- | --- | --- |
| 文件名推断编号、标题清洗与拆艺人 | `catalog/ingest/filename_numbers.go`（新） | 纯函数，organizer 入口预处理调用 |
| 自然序比较 | `catalog/ingest/natural_order.go`（新） | 补位排序 |
| 连续性检验 | `catalog/ingest/continuity.go`（新） | 归组证据 |
| 同目录归组（连续性、按格式切分）、位置冲突保护 | `catalog/ingest/organizer.go` | O 线独占 |
| 同组写入 | `catalog/ingest/persist.go`（`persistRelease`）、`catalog/ingest/version_group.go`（新）、`library/scanner.go`（`persistStagedCandidates`） | |
| 新候选种类的读取与展示 | `catalog/read.go`（种类白名单）、`frontend/src/features/catalog/labels.ts` | 唯一的前端改动 |
| 合同 | `library-scan-contracts.md`、`default-version-contracts.md` 第 7 节 | |

**不改 `library/parser/`（包括 `tags.go`）**。理由：文件名推断需要同目录其他文件做防误判（第 3.3 节）、碟号需要目录信息，这些只在 organizer 里看得到；parser 现有的兜底（标题 = 去扩展名的文件名、轨号/碟号 = 1，都标 inferred）正好作为 organizer 的输入。
这样本项与 T 线（⑥⑦⑧）、M 线（⑩）在 `parser/` 没有冲突，也不需要升解析器版本号。唯一可能碰到 T 线的是第 9 节（用户已裁决）里的 `artistgraph/facts.go` 一处条件。

共享文件：`catalog/ingest/observation.go` 本项**不改**（新增字段 `VersionGroup`、`JoinGroupID` 在 `OrganizedCandidate`，位于 `organizer.go`）；与 S 线 ④ 共改的是 `catalog/ingest/persist.go`（本项改 `persistRelease`，④ 改 `EmbeddedArtwork`，不重叠）与 `library/scanner.go`（本项改 `persistStagedCandidates`、`persistOneCandidate`）。

依赖 ② 的接口：`locateObservation(o)` 返回的 `isDiscDir`、`disc`（由 `parseDiscMarker` 识别目录最后一段），以及 ② 定下的碟号优先级与“合成候选按目录定介质”。③ 不直接调用 `parseDiscMarker`。

## 2. 数据流

```
OrganizeObservations
  排序 → resolveCueCandidateGroups（不变）
  → 预处理（② 专辑名碟号证据；③ 本项：按目录做文件名编号推断、标题清洗、拆艺人）
  → 定位与碟目录归组（②）
  → 同目录分组：③ 按格式切分 → 现有多数决 → ③ 连续性合并 → 现有拆分
  → 组装介质：碟号按优先级；轨位 = 标签 > 文件名（同介质冲突保护）> 自然序补位
scanner.persistStagedCandidates
  → 同一 VersionGroup 的候选先定目标组 → PersistCandidate（新建 Release 时加入该组）
```

## 3. R1 文件名推断编号

### 3.1 单文件规则（stem = 文件名去扩展名）

按顺序，**取到轨号的第一级生效**（只给出碟号、没有轨号的一级不算，继续下一级）；任何结果为 0 都视为没有（21.12 ⑥）。

| 级 | 规则 | 例 | 结果 |
| --- | --- | --- | --- |
| ① | `stem` 去首尾空白后是 1–2 位数字 | `07` | 轨 7 |
| ② | `^\s*(?:(\d{1,2})[\s_\-\.]+)?(\d{1,2})(?:\D.*)?$`，第 1 组是碟、第 2 组是轨 | `1-03 Song`、`02 03 Song`、`2.03 Song`、`1_02_track`、`1.01 - 酸欠少女` | 碟 1 轨 3 等 |
|  | 同上，没有第 1 组 | `12. Zoltraak`、`01 - A - B` | 轨 12、轨 1 |
| ③ | 跳过前导空白后连续数字恰好 3 位：1 位碟 + 2 位轨；恰好 4 位且首位是 `0`：2 位碟 + 2 位轨 | `103 Song`、`0103 Song` | 碟 1 轨 3 |
| ④ | 去尾部空白后，末尾是**独立的** 1–2 位数字（前一个字符不是数字） | `Song 2`、`Interlude 05` | 轨 2、轨 5 |
| ⑤ | `(?i)(?:^\|[^a-z])track[\s_.\-]*(\d{1,2})(?:\D\|$)` | `Track 07 - Title`、`track05` | 轨 7、轨 5 |

### 3.2 编号前缀（供标题清洗）

①：整个 stem；②：从开头到第 2 组末尾，再加紧随的 `[\s._\-]*`；③：数字串加紧随的分隔符；⑤：只有匹配从开头开始时，到数字末尾再加分隔符；④：没有前缀（末尾数字不去）。

### 3.3 防误判（同目录）

对同一目录里的物理文件（同一个 scope 内按 `Directory` 分），先逐个算第 3.1 节结果，再做目录级判断：

- **碟-轨写法**（② 带第 1 组、③ 的 3 位与 4 位）只有在同一目录里**至少 2 个文件、并且超过一半的物理文件**都是同一种写法时才采信。
  - 不采信时：② 带第 1 组的降为“只有轨号 = 第 1 组”，前缀到第 1 组及其后分隔符为止（`01 99 Luftballons` → 轨 1，标题 `99 Luftballons`）；③ 的结果作废，继续试 ④、⑤。
- **4 位前导数字**要求首位为 `0`（`1999 - 标题`、`2046.flac` 永远不拆）。PRD 举例的“拆出的碟号不超过同 scope 的碟目录数”不采用：多数文件不在碟目录里，碟目录数为 0 会把正常的 `0103` 写法也挡掉；用“首位为 0 + 同目录多数”同样挡住年份，且不依赖目录结构（定稿，主会话 2026-11-08 审阅接受）。
- **同介质冲突保护**（组装介质时）：一个介质里来自文件名的轨号如果彼此重复、或与标签轨号重复，这个介质里**所有**来自文件名的轨号都作废，改走自然序补位。理由：重复说明这批文件名不是编号方案（如 `Symphony No. 5`、`Symphony No. 9` 碰到标签轨号）。

### 3.4 写到哪里、优先级

预处理只处理物理观察（不是 CUE 虚拟轨），且只填 inferred 的字段：

- 轨号 inferred（标签缺失）且推断出轨号 → `TrackNumber = 推断值`，`FieldSources["track_number"] = "filename_fallback"`，仍标 inferred。
- 碟号 inferred、且来源不是 `album_title`（②）、且推断出碟号 → `DiscNumber = 推断值`，`FieldSources["disc_number"] = "filename_fallback"`，仍标 inferred。

组装介质时：

- **轨位**：标签（含 CUE）> `filename_fallback`（经第 3.3 节冲突保护）> 自然序补位（`path_order_fallback`）。organizer 现在把 inferred 轨号一律当 0，改为“inferred 但来源是 `filename_fallback`”的保留。
- **碟号**（非合成候选，接 ② 第 5 节）：标签 > CUE > `album_title` > 碟目录（`folder_structure`）> `filename_fallback` > 1。
  合成候选（② 第 4.2 节）由目录定介质，文件名碟号不参与。
  这里**碟目录排在文件名之前**，与 PRD“文件名 > 目录结构”的文字相反，按参考设计 12.2（1314–1318：碟号先从所在目录名取，“碟号尚未确定”时才用文件名第 1 组）。理由：碟目录是结构证据，`CD2/1-03.flac` 这类冲突里目录更可信；且 ② 已让合成候选按目录定介质，两处一致。定稿：按复刻原则照参考设计（主会话 2026-11-08 审阅接受）。

## 4. R2 自然序补位

`assignFallbackTrackPositions` 的排序键从 `observationSortKey` 字节序改为：`naturalLess(RelativePath)`，相等再按 `observationSortKey`。
`naturalLess`：把字符串切成连续数字段与非数字段逐段比；数字段去前导零后先比长度再比字面（不转整数，不会溢出），数值相等时前导零少的在前；非数字段按字节比。`1.flac`、`2.flac`、`10.flac` → 1、2、10。

## 5. R3 标题清洗与拆艺人

预处理里，对物理观察：

1. 标题 inferred 且来源是 `filename_fallback`（scanner 给 parser 兜底标题的来源，`library/scanner.go` `observationFieldSources`）时：去掉第 3.2 节的编号前缀（第 3.3 节采信后的那一种），再去首尾空白；结果为空则保留 stem。来源与 inferred 标记不变。
2. 艺人 inferred 且来源是 `display_fallback`（即 parser 兜底的 `未知艺术家`）、清洗后的标题里**恰好一个** ` - `、两边去空白后都非空、且左边不全是数字时：艺人 = 左边，标题 = 右边；两者都标 inferred，艺人来源改为 `filename_fallback`。
   写入时 `tracks.artist` 照旧经 `metadata.CanonicalTrackArtist` 展示（“现有拆分规则”，⑥ 改拆分规则后自然跟随）。

| 文件名（无标签） | 轨号 | 标题 | 艺人 |
| --- | --- | --- | --- |
| `12. Zoltraak.flac` | 12 | `Zoltraak` | 未知艺术家 |
| `1.01 - 酸欠少女.flac`（同目录都是这种写法） | 碟 1 轨 1 | `酸欠少女` | 未知艺术家 |
| `01 - 周杰伦 - 晴天.flac` | 1 | `晴天` | `周杰伦` |
| `成龙,金喜善 - 无尽的爱.flac` | — | `无尽的爱` | `成龙,金喜善`（展示按现有拆分规则） |
| `Track 07 - Title.mp3` | 7 | `Title` | 未知艺术家 |
| `07.flac` | 7 | `07`（清洗后为空，保留） | 未知艺术家 |
| `1999 - Title.flac` | — | `1999 - Title` | 未知艺术家（左边全是数字，不拆） |
| `2046.flac` | — | `2046` | 未知艺术家 |
| `A - B - C.flac` | — | `A - B - C` | 未知艺术家（不止一个 ` - `） |
| `01 99 Luftballons.flac`（同目录其他是 `02 xx`） | 1 | `99 Luftballons` | 未知艺术家 |
| 有标题标签的文件 | 照旧 | 标签值 | 标签值 |

## 6. R4 轨号连续性作归组证据

### 6.1 检验

```go
// discTrack 是一首曲目用于连续性检验的碟号与轨号；Track 为 0 表示没有，Disc 小于 1 按 1 计。
type discTrack struct{ Disc, Track int }

// continuousAlbum 报告 pairs 是否像一张完整专辑：至少 2 首；每首都有轨号；按碟分后每碟的轨号恰好是 1..n、无缺无重；
// 碟号可以有缺口，但必须有第 1 碟。
func continuousAlbum(pairs []discTrack) bool
```

一组曲目按两种取数各检验一次，**任一通过即连续**：

- 混合（12.3 c）：碟号按第 3.4 节优先级（不含合成候选规则），轨号 = 标签，缺失时用文件名推断值（未经冲突保护）；
- 只看文件名（12.3 b）：碟号 = 文件名碟号，没有时碟目录编号，再没有为 1；轨号 = 文件名推断值。

### 6.2 用在哪里

同目录分组（`ordinary_directory`、`box_leaf`，不含注册根下的散曲）现在“非 `|` partition 多于 1 种且没有严格多数”时按 partition 拆成 `same_dir_split`。改为：拆之前先检验整组连续性，连续就作为**一个**候选（种类不变），字段仍按多数决（`chooseValue` 照常给出 `inconsistent_candidates`）；不连续才按现在的规则拆。
没有专辑标签的文件（`|`）在拆分时现在是一首一个候选，连续时它们也一起并入。

## 7. R5 同目录按格式切成同组两个版本

### 7.1 判定

同目录分组（同第 6.2 节范围）在多数决**之前**先判断：

1. 格式键：`strings.ToLower(Codec)`，为空时用相对路径扩展名小写。整组格式少于 2 种 → 不切。
2. 按格式分成子集；**每个**子集都满足“每碟轨号 1..n 无缺无重、有第 1 碟”（与 `continuousAlbum` 同义，但允许只有 1 首，照顾单曲的 flac + mp3），取数同第 6.1 节两种任一；
3. 整组合在一起有重复的（碟，轨）。
4. 三条都满足 → 每个子集一个候选，种类 `format_split`，`Scope = 目录`，`Partition = "format:<格式键>"`，`VersionGroup = "format:" + 目录`。之后不再走多数决与连续性。

格式各自连续但合起来也连续（flac 1–5、mp3 6–10）→ 不切，照旧一张。有一个子集不连续 → 不切，照旧走后面的规则。

### 7.2 放进同一个 ReleaseGroup

```go
type OrganizedCandidate struct {
	// ……
	// VersionGroup 非空时，同一次整理里取值相同的候选是同一专辑的不同版本，写入时放进同一个 ReleaseGroup。
	VersionGroup string
	// JoinGroupID 由调用方填写：规划没有指定沿用 Release、需要新建时，把新 Release 放进这个已有的组而不新建组。organizer 不填。
	JoinGroupID string
}

// PlannedVersionGroup 返回 candidates 里已规划沿用 Release 的、候选键最小者所在的组；都没有时返回空串。
func PlannedVersionGroup(ctx context.Context, database queryer, scanID, rootID string, candidates []OrganizedCandidate) (string, error)

// ReleaseGroupOf 返回 Release 所在的组。
func ReleaseGroupOf(ctx context.Context, database queryer, releaseID string) (string, error)
```

- `scanner.persistStagedCandidates`：一个 scope 的候选里按 `VersionGroup` 归拢（只有 `format_split` 会有）；对每批先 `PlannedVersionGroup` 取目标组，逐个写入前设 `JoinGroupID = 目标组`；目标组为空时，第一个写完后用 `ReleaseGroupOf` 取它的组作为后面的目标。
  `persistOneCandidate` 改为返回 Release ID（现在只返回 error）。
- `persistRelease`：`plannedReleaseID == ""` 且 `JoinGroupID != ""` 时不插 `release_groups`，`releases.group_id = JoinGroupID`。
- **已有 Release 的组从不移动**：两个版本都沿用了旧 Release、且旧 Release 在不同组（比如管理员拆开过），保持原样。扫描只在新建 Release 时选组，与“扫描不碰组行、归组属于人工”一致。
- 默认版本与版本对比不需要改：组里多出一个成员，代表版本按现有规则（默认版本 → 年份最早 → 创建时间）。
- 锁：新建 Release 引用已有组只取外键共享锁，与扫描反馈的“组升序共享锁”同类；归组与默认版本先锁发行再锁组，不成环。

典型首扫：原来 flac + mp3 混在一个 20 首的 Release `R` 里。切开后两个候选各 10 首、都与 `R` 重叠 10 首、都严格过半；平局时都不在原锚点，按候选键升序 `…:format:flac` 胜，沿用 `R`；`mp3` 新建 Release 并加入 `R` 的组。`R` 没被腾空，收藏不动。

## 8. 接口合同

| 项 | 变化 |
| --- | --- |
| 迁移 | 无 |
| REST 读取 | 形状不变。`candidate_kind` 多一个值 `format_split`：`catalog/read.go` `normalizeCandidateKind` 白名单加入（否则显示为旧值）；前端 `candidateKindLabel` 加 `format_split: "同一目录按格式分开"`。曲目 `title`、`artist`、`position` 对无标签文件会变 |
| 字段来源 | 不新增来源值：沿用已有的 `filename_fallback`（前端已有“文件名（推断）”）作为 PRD 所说的独立来源标记 `filename`，用于轨号、碟号与拆出的艺人；与 `path_order_fallback`、`default_fallback` 可区分。Release 字段证据里艺人决定的来源可能变成 `filename_fallback` |
| Go 接口 | `OrganizedCandidate.VersionGroup`、`JoinGroupID`；`ingest.PlannedVersionGroup`、`ingest.ReleaseGroupOf`；`CandidateFormatSplit = "format_split"`；`stableAnchorPartition` 对 `format_split` 返回 `format:<键>` |
| 依赖 ② | `locateObservation` 的 `isDiscDir`/`disc`；碟号优先级；`MediumTitles`、收藏转移按 ② 的实现直接生效 |
| 版本号 | 只改 organizer 与写入路径、不改 parser，所以**只升 `ingest.GroupingVersion`**，`parser.CommonVersion` 与各格式版本都不升。④ 设计第 8 节对照表写的是“③ 升 `CommonVersion` + `GroupingVersion`”（理由是“`parser/tags.go` 从文件名推断编号”），这与本设计不符：③ 不改 `tags.go`，文件名推断在 organizer 里，解析结果不变，不需要升 `CommonVersion`（由主会话通知 S 线改表）。② 与 ③ 在 O 线一起合入 `main`，`GroupingVersion` 合并为加 1 次。**④ 先合入**：O 线 rebase 到 `main` 后在 `ingest/version.go` 把 `GroupingVersion` 加 1（与 ② 共用这一次）。**O 线先合入**：还没有版本常量，现状每次扫描都重写全部候选，下一次扫描即生效；④ 以 O 线规则为基线，`GroupingVersion = 1` 不另加 |

## 9. 文件名拆出的艺人进艺人页（2026-11-08 用户裁决）

现在艺人整理（`backend/internal/catalog/internal/artistgraph/facts.go`）跳过 inferred 的曲目艺人，Release 艺人只在来源是 `default_fallback` 时才跳过，两边会不一致。
按用户裁决：`facts.go` 对来源为 `filename_fallback` 的 inferred 曲目艺人也建艺人事实（一处条件），能点进艺人页、能收藏。其他 inferred 来源仍跳过。
`facts.go` 属于 T 线共享文件（⑥ ⑦ 也改），合入时由主会话协调先后；这一处条件改变艺人整理结果，O 线合入 main 时把 `artistgraph.Version` 加 1；T 线合入时照常再加 1（每条线合入 main 各升一次，保证先部署的那条线的变化也会触发整理）。

## 10. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 12.2 ① 1–2 位纯数字 → 轨号（1312） | 照搬 | |
| 12.2 碟号同时从所在目录名取（1314–1315） | 改写 | 目录碟号由 ② 的 `locateObservation` 给出，并入碟号优先级，不在文件名推断里重复解析 |
| 12.2 ② 路径正则 `^\s*(?:(\d{1,2})[\s_\-\.]+)?(\d{1,2})(?:\D.*)?$`（1283、1317–1321） | 照搬 + 同目录多数保护 | 正则原样；第 1 组作碟号只在同目录多数文件同写法时采信，挡住 `01 99 Luftballons` |
| 12.2 ③ 3 位 1+2、4 位 2+2（1323–1325、2557–2559） | 改写 | 4 位要求首位为 0（挡年份）；3 位与 4 位都要同目录多数同写法 |
| 12.2 ③ 跳过前导空白（1323、1337） | 照搬 | |
| 12.2 ④ 末尾数字，两位优先（1327–1330） | 改写 | 要求是独立的 1–2 位数字；否则 `2046`、`Blink-182` 会取到 46、82 |
| 12.2 ⑤ `track` 后紧跟数字（1332–1334） / 21.12 ⑤ 要求 p+5 是数字（2561–2562） | 改写 | 允许中间有空白或分隔符，PRD 要求认 `Track 07` |
| 21.12 ⑥ ≤0 置空（2563） / 12.2 零值归空 | 照搬 | |
| 21.12 回写边界：文件名碟号/轨号不补进主字段（2566） | 改写 | PRD 要求用文件名编号排序；写进主字段但保持 inferred、来源 `filename_fallback`，优先级低于标签与 CUE |
| 12.2 只给出碟号没有轨号的一级也算命中（隐含于 ③） | 改写 | 只有取到轨号的一级才生效，避免 `100` 这类留下孤立碟号 |
| 12.3 分桶 A 用户指定归组标识（1347、1359） | 不采用 | ROOMusic 的人工归组走受治理操作，扫描不读 |
| 12.3 分桶 B 外部服务专辑 ID（1348、1360） | 不采用 | PRD“不做” |
| 12.3 分桶 C 标签不足 → 按目录（1349、1353、1361） | 照搬（现状即如此） | ROOMusic 本来就先按目录分组 |
| 12.3 第 4 步 a 标签碟轨（1369–1373） | 照搬（并入 c） | a 通过时 c 必通过、a 失败时 c 可能通过，“a 或 b 或 c”与“b 或 c”等价 |
| 12.3 第 4 步 b 文件名碟轨（1375–1379） | 照搬 | |
| 12.3 第 4 步 c 混合（1381–1385） | 照搬 | PRD R4 的语义 |
| 12.3 第 4 步 d `add(1, 文件名碟号 ?? 文件名轨号)` 不允许缺碟（1387–1390） | 不采用 | 把文件名碟号当轨号的写法像整理时的笔误，含义不明；b 已覆盖“只看文件名” |
| 12.3 先整组检验，失败再按格式切分（1363、1394） | 改写 | 顺序反过来：先判断按格式切分。两个格式各自 1..N 时整组一定不连续，先切与后切结果相同；但现有规则在整组标签一致时根本不会走到连续性，而 PRD 要求标签一致的 flac + mp3 也切开 |
| 12.3 连续性优先于按名归组（1396） | 改写 | ROOMusic 只在“按名归组会拆开”时才用连续性合并；标签一致或严格多数的照旧，不让连续性改变现在正确的结果 |
| 12.3 第 6 步专辑名规范化键共识、第 7 步艺人+专辑（1364–1365、1398） | 照搬（现状） | 对应现有 `compatibleAlbumPartition` 多数决与拆分 |
| 12.4 归组标识 = 成员集合摘要（1400–1421） | 不采用 | 父任务“不复刻” |
| 22.9 位图连续性检验器、容量 128（3398–3426） | 改写 | 用排序/计数实现同一语义，不设 128 上限（目录内曲目数已由暂存上限约束） |
| 22.9 “连续且唯一”条目数 2..99（3427–3442） | 改写 | 归组用列表版语义（3444）：至少 2 首，不设 99 上限；按格式切分的子集允许 1 首 |
| 22.9 列表版“允许缺碟”“首行必须是 01-01”（3444） | 照搬 | 必须有第 1 碟，每碟从 1 开始 |
| 12.7 合并键（标点、大小写、变音、连字）（1486–1514） | 不采用 | 不在本项范围；分组键仍是 `metadata.NormalizeValue` |

## 11. 规则对照表（同目录分组）

“标签一致”指专辑与专辑艺人标签相同；轨号来自标签，缺失时来自文件名。

| 输入（同一目录） | 现在 | 期望 |
| --- | --- | --- |
| 10 首 flac 1–10、标签一致 | 一张 | 一张（不变） |
| 10 首 flac + 10 首 mp3，都是 1–10、标签一致 | 一张，轨位重复 | 两张 `format_split`，同一组 |
| 1 首 flac + 1 首 mp3 单曲，轨号都是 1 | 一张，轨位重复 | 两张 `format_split`，同一组 |
| flac 1–5 + mp3 6–10 | 一张 | 一张（合起来连续，不切） |
| flac 1–10 + mp3 1–7 | 一张，轨位重复 | 两张 `format_split`（各自连续） |
| flac 1–10 + mp3 1、3、5 | 一张，轨位重复 | 不切（mp3 子集不连续），走后面的规则 |
| 1–10，其中 4 首专辑标签写成别的、没有严格多数（如 5 : 5） | `same_dir_split` 两张 | 一张（连续），字段按多数决、标注不一致 |
| 1–10，专辑标签 5 : 5，但轨号 1–5 各出现两次 | `same_dir_split` 两张 | 两张（不连续，照旧拆） |
| 5 首标专辑 A、5 首标专辑 B、2 首没有专辑标签，都没有轨号标签，文件名 `01`–`12` | A、B 各一张，没有标签的每首一张 | 一张（文件名轨号连续） |
| 无任何标签，文件名 `1.flac`、`2.flac`、`10.flac` | 一张，顺序 1、10、2 | 一张，轨号来自文件名 1、2、10 |
| 无任何标签，文件名 `a.flac`、`b10.flac`、`b2.flac` | 一张，`b10` 排在 `b2` 前 | 一张，轨号来自文件名（④ 末尾数字）：`b2` 2、`b10` 10，`a` 自然序补到 1 |
| `Symphony No. 5.flac`、`Symphony No. 9.flac`，标签轨号 5、9 已有 | 照标签 | 照标签（标签优先） |
| 无标签 `Symphony No. 5.flac`、`Symphony No. 5 (Live).flac` | 补位 1、2 | ④ 都取 5 → 同介质重复 → 文件名轨号作废，自然序补位 1、2 |

## 12. 兼容与已有数据

- 第一次按新规则扫描：曲目 UUID 不变；无标签文件的轨位、标题、艺人可能变化（写入时替换 current observations，不累计）。
- 原来被 `same_dir_split` 拆开、现在因连续性合成一张：新候选与最大的旧 Release 重叠过半就沿用它，其余旧 Release 被腾空，它们组上的专辑收藏按 ② 的规则转过来；不过半则新建 Release，所有旧组的收藏都转过来。
- 原来 flac + mp3 混在一张：一个版本沿用旧 Release，另一个新建并加入同一组（第 7.2 节）；收藏、默认版本、修正都还在旧 Release / 组上。
- 已有的人工归组（版本在组之间移动过）不受影响：扫描不移动已有 Release 的组。

## 13. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| 以数字开头的歌名被当成编号（`7 Rings`、`99 Luftballons`） | 只作用于没有轨号标签的文件；碟-轨写法要同目录多数；同介质冲突保护；真实库 smoke 列出标题被清洗的曲目逐条确认 |
| 文件名拆艺人拆错（歌名里本来有 ` - `） | 只在没有艺人标签且恰好一个 ` - ` 时；左边不能全是数字；仍标 inferred，可在页面修正 |
| 连续性把两张不同专辑合成一张 | 只在“现有规则会拆开”的同目录里用，且要求每碟 1..n 无缺无重 |
| 按格式切分误切 | 要求两种以上格式、每个子集连续、合起来有重复，三条同时成立 |
| 新建 Release 加入已有组时与人工归组并发 | 外键共享锁；归组先锁发行后锁组，不成环 |

回滚：代码回退即恢复旧归组与旧补位，下一次扫描按旧规则重写（Release 仍按严格过半沿用）。已经加入同一组的 `format_split` 版本回退后会被合回一张，另一个 Release 变空隐藏；已转移的收藏不回退。无迁移，无数据结构回滚。

## 14. 八号机统计（已执行）

已执行，聚合结果与结论见 [research/m8-filename-stats.md](./research/m8-filename-stats.md)：第 3.1 节 ③ 的 3 位/4 位分支照参考设计保留（真实库没有这种写法，不会误伤，年份由第 3.3 节挡住）；按格式切分在真实库上不会触发，靠单测覆盖；拆艺人只对没有艺人标签的文件生效，真实库 smoke 列出实际被拆的文件逐条确认。

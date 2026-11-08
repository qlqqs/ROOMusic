# ⑧ 古典作品与乐章：技术设计

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 3 项，在 ⑦ 之后。需求见 [prd.md](./prd.md)。
参考设计只当数据读：[reference-design.md](../11-08-scan-reference-port/research/reference-design.md)，用 21.4、22.1–22.7、22.12；不用作曲家配置（22.6）、古典性打分（22.11）、乐器编制（22.8）。

## 1. 边界与归属

| 归属 | 本项改动 |
| --- | --- |
| `catalog/classical`（新叶子包，只依赖标准库） | 作品编号解析（组合子 + 抹白）、排序规范化、标题段切分与作品/乐章拆分；全部纯函数 |
| `library/parser` | 读作品、乐章名、乐章号标签 |
| `catalog/ingest` | `SourceObservation` 增加三个字段；新文件 `works.go`：`AssignWorks` 在写入路径为候选的每首曲目算作品信息；`persist_track.go` 写列 |
| 迁移（合入编号 0039） | `tracks` 增加作品与乐章列；`catalog_search_values` 字段 CHECK 增加两个曲目字段 |
| `catalog` 读取、`searchindex`、搜索 SQL | `ReleaseTrack.work`；作品名与作品编号进搜索投影，并像曲目标题一样传播到专辑结果 |
| 前端 | 专辑页曲目列表按作品分组；搜索命中说明增加两个字段名 |

不改 organizer 的分组与碟号规则（O 线），不加作品页面与筛选。

## 2. 数据流

```
标签 WORK / MOVEMENTNAME / MOVEMENT… ──parser──► AudioObservation.Work, MovementName, MovementNumber
   ▼ scanner.go 转换（S 线共享文件，加三个字段）
SourceObservation（JSON work / movement_name / movement_number，omitempty）
   ▼ organizer（不改）
OrganizedCandidate（Genres 已有）
   ▼ persist：works := AssignWorks(candidate)    // ingest/works.go，按 碟→轨位 顺序
   │   1. 有作品标签的曲目：标签结果（source=tag）
   │   2. 其余曲目：候选流派是古典时，对连续无标签曲目段跑 22.7 拆分（source=title）
   │   3. 作品编号：classical.ParseCatalogs(作品名) 取第一个条目的原文片段
   │   4. 相邻同作品编成一组，组内 ≥2 首才给 work_index
   ▼ persist_track 写 tracks.work_* 列（重扫整体替换）
   ▼ searchindex：work_title、work_catalog 两个曲目字段
REST ReleaseTrack.work = {index, title, movement} | null
前端：同一 medium 内相邻同 index 的曲目归在作品小标题下
```

## 3. 接口合同

### 3.1 `catalog/classical` 包

```go
type CatalogEntry struct {
    System  string // 规范体系名：Op、WoO、Hob、BWV、K、D、RV、HWV
    Number  string // 编号文本，如 "67"、"27/2"、"I:94"
    Partial bool   // 卷号式只有卷号
    Start, Length int // 在输入串中的 rune 下标区间（含副号 no./nr.）
}
func ParseCatalogs(s string) []CatalogEntry   // 元数据侧：不允许串首匹配；每个体系只留第一条
func CatalogSortKey(text string) string        // 22.4：: / - 换空格，压连续空格
func Segments(title string) []string           // 22.7.3 段切分（含 §3.3 中文扩展）
type WorkSplit struct{ Work, Movement string; Group int } // Group 为 0 表示不归入作品
func SplitWorks(titles []string) []WorkSplit   // 22.7.2–22.7.8，Group 从 1 起
```

### 3.2 作品编号解析（照搬 22.1–22.3 的组合子与抹白）

- 原子解析器的 14 步、`查找` 的三个后果、编号收集（卷号模式单分隔符）、副号 `no`/`nr` 嗅探与回滚、三类起始判定、尾部裁剪全部照搬，22.1 的九条行为与 22.12 第 1–8 条不变式全部成立。
- 字符串按 rune 数组处理（参考是 UTF-16 char 数组，ROOMusic 用 `[]rune` 以免中文切坏）。
- **改写**：编号字符与“字母数字”判定只认 ASCII 字母与数字（参考用 `char.IsLetterOrDigit`，会把 `作品67号` 的“号”吞进编号）；左边界判定同样只认 ASCII。
- 内置组合（无作曲家配置，固定一套）：

```
内置 = 顺序全跑(
  短路择一(基元("Op","Op",副号), 基元("Op","Opus",副号), 基元("WoO","WoO",副号), 基元("Op","作品",副号)),
  两者都跑(基元("Hob","H",罗马|卷号), 基元("Hob","Hob",罗马|卷号)),
  基元("BWV","BWV"), 基元("K","KV"), 基元("K","K"), 基元("D","D"), 基元("RV","RV"), 基元("HWV","HWV"),
)
```

  `KV` 排在 `K` 前（抹白后 `K` 不会再吃 `KV 525` 的编号）；`WoO` 与 `Op` 共用“默认”短路择一以保持 22.2 的语义（命中一个即停）。触发条件照搬 22.5：串中没有数字直接返回空。
- 结果按体系去重（24.7 元数据侧“每体系只留 1 条”）。
- 作品编号显示片段 = 原作品名中 `[Start, Start+Length)` 的原文去首尾空白（如 `Op. 67`、`Op. 27 No. 2`、`BWV 1007`、`Hob. I:94`）。

### 3.3 标题拆分（照搬 22.7，补中文写法）

照搬：22.7.1 两个独立括号计数器的归一化（只用于判定）；22.7.2 是否需要拆分；22.7.3 段切分状态机的四个分支与七条规则；22.7.4 公共前缀与取段（8 字符截断表、`N==0` 返回整串、相同或包含返回 0）；22.7.5 罗马序号反向定位；22.7.6 分组归并与目录号兼容判定；22.7.7 白名单 `promenade`；22.7.8 单曲组丢弃、相邻同名乐章另起一组。22.12 第 9–14 条不变式全部成立。

改写与扩展（只在古典流派专辑里运行，见 §3.4）：

1. **全角分隔符**：大分隔符集合与连续吞并集合加入 `：`、`，`、`；`、`）`、`／`、`＼`、`－`、`—`；截断标点表与“乐章分隔符”集合同样加入对应全角字符。
2. **空格闸门**：除“空格 + 字母/数字”外，遇到第一个全角大分隔符时也打开（中文标题常不带空格）。闸门在该分隔符处先打开再判断切分。
3. **序号反向定位扩展**：期望第 g+1 个乐章时，当前标题以下列任一开头即触发：罗马 `{Roman(g+1)}.`（照搬）、阿拉伯 `{g+1}.`、中文 `第{g+1}乐章`（数字写作阿拉伯数字或一到九十九的中文数字）。组首标题中对应的“第一乐章”标记是 `I.`、`1.`、`第一乐章`/`第1乐章`。前导前缀取组首标题中该标记起点之前的原文（含分隔符与空白）——对罗马形式这与参考“取前 N 段再 TrimEnd('I')”结果相同。
4. **段切分的时间戳例外、前 4 字符不切、缩写例外**照搬；缩写例外的前两位比较只对 ASCII。

### 3.4 `AssignWorks`（`catalog/ingest/works.go`）

输入候选全部曲目，按（碟号，轨位）排序，输出 `map[相对路径]WorkAssignment{Title, Catalog, Index, MovementNumber, MovementTitle, Source}`。

1. **标签作品**：`Work` 非空的曲目 → `Title=Work`，`MovementTitle=MovementName`；`MovementName` 为空时，若曲目标题以“作品名 + 分隔符（`: `、`：`、` - `、`, `、`，`）”开头，取其后的部分，否则为空。`MovementNumber` 取标签。
2. **标题拆分**：候选流派判定为古典时（下条），把**连续的无作品标签曲目**切成段，每段调用 `SplitWorks(标题)`；`Group>0` 的曲目得到 `Title=Work`、`MovementTitle=Movement`、`Source="title"`。不是古典时不拆。
   - 古典判定：`OrganizedCandidate.Genres` 任一项经 `GenreKey` 后等于 `classical`、`classic`、`classique`、`klassik`、`clásica`、`clasica`、`classica`、`クラシック`、`클래식`，或包含 `古典`。`classical crossover`、`neo-classical` 不算（按整项比较）。只看扫描得到的流派，人工修正流派不触发重算（已裁决）。
3. **编号**：`Catalog` = `ParseCatalogs(Title)` 第一条的原文片段，没有为空。
4. **分组序号**：按顺序扫描，与上一首“同一作品”时沿用组，否则新组。同一作品 = 两首都有作品、作品名去空白小写相等、来源相同，并且：
   - 两首都有乐章号时，本首乐章号大于上一首；
   - 两首乐章名相同（去空白小写）且不在白名单时不算同一作品（22.7.8）；
   - 标题拆分来源以 `SplitWorks` 的 `Group` 为准。
   组内曲目 ≥2 首才写 `Index`（发行内从 1 递增）；只有 1 首的组 `Index` 为空，但仍保存 `Title`、`Catalog`、乐章字段供搜索（标题拆分的单曲组按 22.12 第 13 条不产出任何字段）。
5. CUE 虚拟轨照常参与（标题来自 CUE `TITLE`）。

调用位置：`persistCandidate` 开头调用一次，把结果交给 `persistTrack`。不在 organizer 里调用，避免与 O 线冲突；因此规划阶段不跑它。

### 3.5 解析器读取

| 字段 | Vorbis | ID3 | M4A |
| --- | --- | --- | --- |
| 作品 `Work` | `WORK`（`TXXX:WORK` 同入口） | `TIT1`；无 `TIT1` 时 `TXXX:WORK` | `©wrk`；`----:WORK` |
| 乐章名 `MovementName` | `MOVEMENTNAME`；`MOVEMENT` 不是数字时作乐章名 | `MVNM` | `©mvn` |
| 乐章号 `MovementNumber` | `MOVEMENTNUMBER`；`MOVEMENT` 是数字或 `n/m` 时 | `MVIN`（`n/m` 取 n） | `©mvi`（16 位整数） |

`MOVEMENTTOTAL`、`©mvc`、`MVIN` 的 `/m` 只用于识别格式，不保存（没有显示用途）。逐级空判断：同一字段多个键取第一个非空。值经 `BoundedText`。ID3 帧白名单加 `TIT1`、`MVNM`、`MVIN`。

注意 `TIT1` 在 iTunes 12.5 之前表示“分组”：只有当同一文件**同时**有 `MVNM` 或 `MVIN`，或者 `TIT1` 能解析出作品编号时才当作品读（§7 风险）。

### 3.6 迁移（合入编号 0039）

```sql
ALTER TABLE tracks
  ADD COLUMN work_title TEXT CHECK (work_title IS NULL OR (btrim(work_title) <> '' AND octet_length(work_title) <= 4096)),
  ADD COLUMN work_catalog TEXT CHECK (work_catalog IS NULL OR (btrim(work_catalog) <> '' AND octet_length(work_catalog) <= 256)),
  ADD COLUMN work_index INTEGER CHECK (work_index IS NULL OR work_index > 0),
  ADD COLUMN movement_number INTEGER CHECK (movement_number IS NULL OR movement_number > 0),
  ADD COLUMN movement_title TEXT CHECK (movement_title IS NULL OR (btrim(movement_title) <> '' AND octet_length(movement_title) <= 4096)),
  ADD COLUMN work_source TEXT CHECK (work_source IS NULL OR work_source IN ('tag','title')),
  ADD CONSTRAINT tracks_work_ck CHECK (
    (work_title IS NULL) = (work_source IS NULL)
    AND (work_title IS NOT NULL OR (work_catalog IS NULL AND work_index IS NULL AND movement_number IS NULL AND movement_title IS NULL)));
-- catalog_search_values_field_ck：曲目字段增加 'work_title','work_catalog'
```

`.down.sql`：删除 `field IN ('work_title','work_catalog')` 的搜索值，恢复字段 CHECK，删约束与列。`rollbackFixtures` 补一行带作品的曲目与一条 `work_title` 搜索值。

### 3.7 REST 形状变化

`ReleaseTrack`（`GET /releases/{id}` 的 `media[].tracks[]`）增加必传字段：

```json
"work": null
"work": {"index": 1, "title": "Symphony No. 5 in C minor, Op. 67", "movement": "I. Allegro con brio"}
```

- 只有 `work_index` 非空时为对象，否则为 `null`。`index ≥ 1`，同一发行内相邻且 `index` 相同的曲目是同一作品。
- `title` 1–4096 字节；`movement` 为 `string | null`，null 时行内显示曲目标题。
- 作品编号、乐章号、来源不进 REST（界面不用；编号已包含在作品名里）。`TrackSummary` 等其它摘要不变。

搜索：

- 曲目字段 `work_title`（原文 = 作品名）、`work_catalog`（原文 = 编号片段）。`work_catalog` 与 `catalog_number` 一样走编号分支（`textnorm.CatalogNumber`），SQL 里 `field='catalog_number'` 的判断改为 `field IN ('catalog_number','work_catalog')`。
- 专辑结果：present 子轨的这两个字段像 `track_title` 一样传播到发行（曲目署名不传播兄弟轨的规则不变）。
- `match.field` 增加 `work_title`、`work_catalog`；前端命中说明“作品：…”“作品编号：…”。
- `BWV 1007`、`bwv1007`、`Op. 67`、`op.67` 在本项即可命中；`op67`（去掉点）要靠 ⑬ 的去标点键（同线随后合入）。

### 3.8 前端显示

- `features/catalog/api/decoders.ts`：track 增加 `work: d.nullable(d.object({ index: d.integer({min:1}), title: …, movement: d.nullable(…) }))`。
- `release-media.tsx` 的 `TrackList`：同一张介质里相邻且 `work.index` 相同的曲目归为一组，渲染为一个列表项：一行作品小标题（左：作品名；右：该组总时长，任一曲时长未知时显示未知，写法与现有时长一致），下面是嵌套的曲目列表；组内每行标题位置显示 `work.movement ?? track.title`，序号、播放标记、CUE 标签、操作、时长、曲目艺人、署名照常。作品名不在每行重复。
- `work` 全为 null 的专辑与现在完全一样。分组逻辑写成纯函数（`groupTracksByWork`），方便单测。
- 不加作品页面、筛选与任何说明文字。样式遵守 `frontend/DESIGN.md`、`DESIGN-components.md`（紧凑、无边框、小标题字号用现有层级）。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 21.4 作品键 `WORK→COMPOSITION→©WRK→MVNM`（2258） | 改写 | `MVNM` 按 ID3 规范是乐章名（原文 21.16 第 10 条自认作品/分部映射交叉）；作品读 `WORK`/`TIT1`/`©wrk`；`COMPOSITION` 不常见，不读 |
| 21.4 分部键 `PART→MOVEMENT→©MVN→MVIN`（2259） | 改写 | 乐章名读 `MOVEMENTNAME`/`MVNM`/`©mvn`；`MOVEMENT` 按 Picard/Mp3tag 习惯是乐章号，非数字时才当名字；`MVIN` 是乐章号 |
| 21.4 `SECTION`、`WORKID`（2260–2261） | 不采用 | 没有展示用途 |
| 22.1 组合子类型、五种组合方式、抹白（2680–2710） | 照搬 | |
| 22.1 原子解析器 14 步与“查找”三后果（2712–2749） | 照搬 | |
| 22.1 编号收集、副号嗅探、三类判定、尾部裁剪（2751–2808） | 照搬，字母数字判定改为只认 ASCII | 避免把中文“号”等吞进编号 |
| 22.1 九条必须复刻的行为（2810–2822） | 照搬 | 第 3 条（`MINOR` 全大写逃过排除）一并照搬 |
| 22.2 内置-默认、内置-卷号式（2824–2839） | 照搬 | |
| 22.3 卷-号子解析器与罗马 I/V/X（2841–2874） | 照搬 | |
| 22.4 排序规范化（2876–2883） | 照搬 | `CatalogSortKey`；本项只用于测试与兼容判定，不入库 |
| 22.5 触发条件：无数字预筛（2885–2895） | 照搬 | 无作曲家配置分支 |
| 22.6 作曲家配置 3023 位（2897–2935） | 不采用 | 数据表不搬；改为固定一套内置体系（PRD R3 列出的 8 种 + 中文“作品”） |
| 24.7 元数据侧按体系去重（4520–4527） | 照搬 | |
| 22.7.1 两个独立括号计数器（2941–2957） | 照搬 | |
| 22.7.2 是否需要拆分（2959–2971） | 照搬 | |
| 22.7.3 段切分状态机与七条规则（2973–3048） | 照搬 + 全角分隔符与闸门扩展 | 中文标题常用全角冒号且不带空格 |
| 22.7.4 公共前缀与取段（3050–3080） | 照搬，截断表加全角 | |
| 22.7.5 罗马序号反向定位（3082–3138） | 照搬 + 阿拉伯与“第N乐章”扩展 | 中文专辑常写“第二乐章 …” |
| 22.7.6 分组归并与目录号兼容（3140–3164） | 照搬 | |
| 22.7.7 白名单 `promenade`（3166–3174） | 照搬 | |
| 22.7.8 作品标识分裂（3176–3197） | 改写 | 作品标识用发行内递增序号 `work_index` 代替 GUID：重扫稳定、无需身份表；分裂语义不变 |
| 22.7.9 演算样例（3199–3210） | 照搬为单测 | |
| 22.12 第 1–14 条（3592–3605） | 照搬 | 第 13 条：单曲组不产出 |
| 22.12 第 15–18、20 条（3606–3611） | 不采用 | 乐器编制、连续性位图、原声带判定不在本项 |
| 22.8 乐器编制、22.11 古典性打分（3212–3588） | 不采用 | PRD 不做；古典判定改用流派（用户裁决） |
| 23.14 流派古典性按流派树（4136–4148） | 改写 | 没有流派树；按整项流派名单比较，`Classical Crossover` 等不算 |

## 5. 规则输入输出对照

作品编号（`ParseCatalogs`）：

| 输入 | 结果 |
| --- | --- |
| `Symphony No. 5 in C minor, Op. 67` | Op 67，片段 `Op. 67` |
| `Piano Sonata No. 14 in C-sharp minor, Op. 27 No. 2` | Op 27/2，片段 `Op. 27 No. 2` |
| `Cello Suite No. 1 in G major, BWV 1007` | BWV 1007 |
| `Piano Sonata No. 11 in A major, K. 331` | K 331 |
| `Serenade No. 13 in G major, KV 525` | K 525，片段 `KV 525` |
| `Symphony No. 94 in G major, Hob. I:94` | Hob I:94 |
| `Piano Sonata in B-flat major, D. 960` | D 960 |
| `Concerto in D major, RV 93` | RV 93（`in D major` 不命中 D） |
| `Water Music Suite No. 1, HWV 348` | HWV 348 |
| `Für Elise, WoO 59` | WoO 59 |
| `c小调第五交响曲，作品67` | Op 67，片段 `作品67` |
| `Op. 10 No. 3` | 无（串首不匹配，22.12 第 3 条） |
| `Prelude in C major` | 无（无数字预筛） |
| `Symphony No. 2 in D, Op. 73` | Op 73（`D,` 不命中 D） |
| `Op..27` | 无（只吃一个点） |

标题拆分（`SplitWorks`）：22.7.3 的 8 行段切分表与 22.7.9 的 A–E、C′ 原样作为测试；补充：

| 输入（同一张专辑相邻曲目） | 作品 | 乐章 |
| --- | --- | --- |
| `第五交响曲 c小调 作品67：第一乐章 有活力的快板` / `…：第二乐章 稍快的行板` | `第五交响曲 c小调 作品67` | `第一乐章 有活力的快板` / `第二乐章 稍快的行板` |
| `第五交响曲：第一乐章` / `第二乐章` / `第三乐章` | `第五交响曲` | 三个乐章，同一组 |
| `Concerto: 1. Allegro` / `2. Adagio` / `3. Presto` | `Concerto` | 三个乐章 |
| `晴天` / `七里香`（古典流派但不是作品） | 无 | 无（两组各 1 首，丢弃） |
| 非古典流派的 `Live: Intro` / `Live: Song A` | 不拆 | 不拆 |

标签作品分组（`AssignWorks`）：

| 曲目（WORK / MOVEMENTNAME / MOVEMENT） | index |
| --- | --- |
| W/I/1、W/II/2、W/III/3 | 1,1,1 |
| W/I/1、W/II/2、W/I/1、W/II/2（同一作品两个录音） | 1,1,2,2 |
| W/I/1、X/–/–、W/II/2 | 无、无、无（各组 1 首；`work_title` 仍保存） |
| W/Promenade、W/Gnomus、W/Promenade | 1,1,1 |

## 6. 兼容与已有数据

- 迁移只加列，已有曲目作品列为 NULL，REST `work=null`，专辑页不变。下一次扫描后出现作品分组（④ 已在 main 时靠 `CommonVersion`、`GroupingVersion` 各 +1 触发，见 §7）。
- 不涉及艺人与人工意图；不改曲目、发行身份。
- 样本库：流派只有 国语流行乐、世界音乐、Pop、Mandopop、民谣、台语流行乐、Anime，没有作品标签 → **预期没有任何专辑出现作品分组**，用来验证“非古典专辑不变”。生产库同样没有古典流派（§9）。古典拆分的正例只靠单测；真实库 smoke 在本机隔离实例上对 `music/` 跑，只验证没有任何专辑出现作品分组。

## 7. 版本号与风险

| 版本 | 变化 |
| --- | --- |
| `parser.CommonVersion` | 计入 T 线的 +1（作品与乐章标签跨三种容器，加了 `AudioObservation` 字段） |
| `ingest.GroupingVersion` | 计入 T 线的 +1（`AssignWorks` 在写入路径，同样的观察会写出作品列） |
| `searchindex.ProjectionVersion` | 计入 T 线的 1→2 |
| `artistgraph.Version`、`textnorm.Version` | 不变 |

常量名按 ④ 定稿（`11-08-scan-incremental/design.md` 3.1、3.2、§8）。T 线一起合入 main 时各版本合计只升一次。合入顺序：④ 先合入 main → T 线 rebase 后在 main 当时的值上 `CommonVersion` +1、`GroupingVersion` +1；T 线先合入 → 不升，由 ④ 以合入后的规则为基线。`ProjectionVersion` 与顺序无关。

| 风险 | 处理 |
| --- | --- |
| 古典流派专辑里的非作品曲目被误拆 | 照搬参考的保守条件：需相邻多首有公共前缀段；单曲组丢弃；真机清单逐条确认 |
| `TIT1` 被当成“分组”写入 | 只有同时有乐章标签或能认出编号时才当作品 |
| 单字母体系 `D`、`K`、`H` 误认 | 只在作品名与古典专辑的标题上运行；串首不匹配、左边界非字母数字、编号必须以数字（或罗马数字）开头 |
| 作品分组改变专辑页结构，影响定位与 content-visibility | 行仍是同一个组件，定位按曲目 ID；前端单测覆盖分组函数，截图复核 |

回滚：down 迁移删列与搜索值；代码回退后投影版本回落，后台重建。

## 8. 裁决

已裁决（2026-11-08，见 prd.md）：是否从标题拆只看扫描得到的流派，人工修正流派不触发重算；古典正例靠单测覆盖。

## 9. 生产库统计

已执行，见 [m8-production-stats.md](../11-08-scan-reference-port/research/m8-production-stats.md)：发行流派只有国语流行乐、Pop、台语流行乐、Mandopop、世界音乐、民谣、Anime，没有古典。真实库上不应出现任何作品分组。

## 2026-11-08 合入 main 对齐

O 线与 T 线各自已将 `artistgraph.Version` 提升到 3；整合后统一为 4，避免已有版本 3 的投影漏回填。`searchindex.ProjectionVersion=2`、`textnorm.Version=1` 保持不变。下述线内升级/回滚说明是原实施背景，合并后的回滚须连同 O 线规则判断，不能只回退版本常量。

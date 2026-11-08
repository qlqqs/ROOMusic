# ⑥ 标签多值与合辑：技术设计

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 1 项。需求见 [prd.md](./prd.md)。
参考设计原文只当数据读：[reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。下文“参考 21.3（2220–2244）”指该文件章节与行号。

## 1. 边界与归属

| 归属 | 本项改动 | 不改 |
| --- | --- | --- |
| `catalog/metadata`（叶子包，只依赖标准库） | 拆分规则：换行/NUL 先拆；拉丁“姓, 名”整体保留；合辑占位词表与判定；多值拼接助手 | `SplitGenres`（流派合同另有裁决） |
| `library/parser` | 专辑艺人回退键与多值拼接；ID3 `TXXX` 按规范解析；读合辑标志（`COMPILATION`/`TCMP`/`cpil`）；原始年份回退键；署名改用原文拆分（不先删换行） | 其它字段的读取、指纹、歌词 |
| `catalog/ingest` | `SourceObservation.Compilation`；新文件 `compilation.go` 的合辑判定，organizer 只加一行调用 | 分组、partition、scope、Release 沿用规则 |
| `catalog/internal/artistgraph` | 合辑占位的发行级署名不建事实；`Version` 2→3 触发后台按新拆分重新整理 | 名字键、人工意图表、治理执行器 |
| `catalog/internal/searchindex` | 不改代码；`ProjectionVersion` 1→2（多名值按新拆分重建，与 ⑦⑧⑬ 共用一次升级） | 匹配与排序 |
| 前端 | `evidenceSourceLabel` 增加 `compilation: "合辑"` 一项 | 其余无改动（见 §3.4） |

本项**不改 REST 形状、不加迁移**。

## 2. 数据流

```
文件标签 ──parser──► AudioObservation
   │  a. 艺人类原文先按 换行/NUL 切，再走拆分规则（metadata.CanonicalArtistNames）
   │  b. AlbumArtist：按回退键取第一个非空键，展开该键全部值，按出现序去重，用 " / " 连接
   │  c. Compilation：任一标志值为真 → true
   │  d. Year 为 0 时用 ORIGINALDATE/TDOR/TORY 回退
   ▼
scanner.go 转换 ──► SourceObservation（新增 compilation，JSON omitempty，随暂存表）
   ▼
organizer：artistDecision / albumArtistDecision 算完后调用 applyCompilation（compilation.go）
   │  合辑 → artist="群星"、source="compilation"、rule_id="compilation_v1"；album_artist 决定丢弃
   ▼
persist：releases.artist="群星"，releases.album_artist=NULL，不写 release_credits 的 album_artist
   ▼
artistgraph.Refresh：发行级原文是合辑占位 → 不建 release_artist/album_artist 事实（无 artist_refs，前端是纯文字）
                     曲目艺人照常（§8，已裁决）
```

已有数据：`artistgraph.Version` 升到 3 后，后台按清单对全部发行重跑 `Refresh`，从已存原文（`releases.artist`、`releases.album_artist`、
`artist_tag_evidence`、`track_credits`）按新规则重拆；`track_credits` 里已经按旧规则拆开的名字（如作曲 `Bach` + `Johann Sebastian`）要等下一次扫描重新解析才改过来（§6）。

## 3. 接口合同

### 3.1 拆分规则（`metadata.splitArtistNames`，顺序固定）

1. 按 `\r\n`、`\n`、`\r`、`\x00` 切分；多于一段时每段**完整重走本规则**后合并。
2. 删除其余控制字符、去首尾空白；空则无名字。
3. 明确分隔符（不变）：`"; "`、`"；"`、`" / "`、`" feat. "`、`" ft. "`、`" with "`、`"、"`，区分大小写。
4. ` & ` 两侧都含空格才拆（不变）。
5. 固定组合白名单（不变）：`Simon & Garfunkel`、`Earth, Wind & Fire`、`AC/DC`。
6. **新增**拉丁“姓, 名”：整串恰好一个逗号，且满足下列全部条件时整体保留：
   - 逗号两侧去空白后都非空，且只含拉丁字母（`unicode.Latin`）、组合附加符（`unicode.Mn`）、空格、`.`、`-`、`'`、`’`，两侧各至少一个拉丁字母；
   - 左侧不超过 3 个词，右侧不超过 4 个词；
   - 两侧都不含团体词（按词小写前缀匹配）：`orchest`、`philharm`、`symphon`、`sinfon`、`ensemble`、`quartet`、`quatuor`、`trio`、`choir`、`chorus`、`chor`、`consort`、`singers`、`players`、`band`。
7. 紧凑 `/`、`,`、`&` 依次拆（不变；`&` 的固定组合保护不变）。

第 3 步拆出的各段经 `flattenArtistNames` 递归，每段同样先过第 5、6 步再过第 7 步。规则对所有走拆分的字段生效：曲目艺人、专辑艺人、作曲、指挥、演奏、制作人（⑦ 之后含作词、编曲）。

调用约束（修复“先删换行再拆”）：

- `parser.artistEvidenceDisplay` 直接把证据原文交给 `CanonicalArtistNames`（证据入库前已校验 ≤4096 字节、合法 UTF-8），不再先 `BoundedText`。
- `setAudioTag`、`parseID3Frames`、`parseM4AFreeformValue` 中写署名的分支把**原文**交给 `addObservationCredits`（拆分后每个名字再 `BoundedText`），不再传已删换行的值。
- 新增 `metadata.JoinArtistValues(values ...string) string`：每个值先按第 1 步切开，`BoundedText`，丢空，按原写法去重保留首次顺序，用 `" / "` 连接（参考 21.3 的专辑艺人单值拼接）。专辑艺人与 `ID3 TPE2` 多值都用它。

### 3.2 解析器读取（键名不区分大小写）

专辑艺人（逐级空判断：按优先级取第一个有非空值的键，**展开该键的全部值**用 `JoinArtistValues` 连接；同一优先级的多个键视为同一级）：

| 级 | Vorbis（FLAC/OGG/Opus） | ID3（MP3） | M4A |
| --- | --- | --- | --- |
| 1 | `ALBUMARTIST` | `TPE2`（按 NUL 取全部值） | `aART` |
| 2 | `ALBUM_ARTIST` | `TXXX:ALBUMARTIST` | `----:ALBUMARTIST` |
| 3 | `ALBUM ARTIST` | `TXXX:ALBUM_ARTIST`、`TXXX:ALBUM ARTIST` | `----:ALBUM_ARTIST`、`----:ALBUM ARTIST` |

所有这些键都按 `album_artist` 角色记入 `artist_tag_evidence`（`tag_key` 写原键，如 `TXXX:ALBUMARTIST`、`----:ALBUM ARTIST`）。实现上每个键带一个级别，解析完所有标签后再决定 `AlbumArtist`（不能“最后一个覆盖”）。

ID3 `TXXX`：帧体 = 编码字节 + 描述（按编码以 `00` 或 `00 00` 结束）+ 值（v2.4 可按 NUL 多值）。描述去首尾空白转大写后作为键，交给与 Vorbis 相同的键表处理（专辑艺人、合辑、原始年份；⑦⑧ 复用同一入口）。不认识的描述忽略。帧压缩/加密沿用 `id3FramePayload` 的跳过规则。

合辑标志（参考 21.7 真值：去空白小写后 ∈ {`1`,`yes`,`true`,`compilation`} 为真，其它值为假）：

| 容器 | 键 |
| --- | --- |
| Vorbis | `COMPILATION`、`ITUNESCOMPILATION` |
| ID3 | `TCMP`、`TXXX:COMPILATION` |
| M4A | `cpil`（整数 1 字节，非零为真）、`----:COMPILATION` |

任一键为真 → `AudioObservation.Compilation = true`。

原始年份：`Year` 仍取 `DATE`/`YEAR`/`TDRC`/`TYER`/`©day`；全部缺失（`Year==0`）时依次用 `ORIGINALDATE`、`ORIGINALYEAR`、`TDOR`、`TORY`、`TXXX:ORIGINALDATE`、`TXXX:ORIGINALYEAR`、`----:ORIGINALDATE`、`----:ORIGINALYEAR` 的第一个可解析年份。不新增字段、不显示（已裁决）。

ID3 帧白名单增加 `TXXX`、`TCMP`、`TDOR`、`TORY`（⑦⑧ 再加各自的帧）。

### 3.3 合辑判定（`catalog/ingest/compilation.go`）

```go
// IsCompilationPlaceholder 在 metadata 包：整串比较，大小写与全半角不敏感。
func IsCompilationPlaceholder(raw string) bool
const CompilationDisplayName = "群星" // metadata 包

// applyCompilation 在 organizer 算出 artistDecision、albumArtistDecision 之后调用。
func applyCompilation(observations []SourceObservation, artist, albumArtist *FieldDecision)
```

占位词比较前的折叠（只用标准库）：全角 ASCII（U+FF01–U+FF5E）减 0xFEE0、U+3000 → 空格、`strings.ToLower`、连续空白压成一个、去首尾空白。

占位词表（折叠后整串相等）：

```
various, various artists, various artist, v.a., v.a, va, v/a, compilation,
various composers, various recording, various recordings, various soundtrack,
multiple artists, diverse artists, artisti vari, artistes divers, artistas variados,
blandade artister, verschillende artiesten,
群星, 羣星, 华语群星, 華語群星, 多位艺人, 多位藝人, 多位艺术家, 多位藝術家,
オムニバス, ヴァリアス・アーティスト
```

判定（`physical` 指 `decisionObservations` 中 `SourceKind != "folder"` 的观察）：

1. `albumArtist.Value` 非空且不是占位词 → 有真实专辑艺人，**不改**（即使带合辑标志）。
2. 否则若 `artist.Value` 是占位词，或 `physical` 中 `Compilation=true` 的严格过半 → 合辑：
   - `*artist = FieldDecision{Field:"artist", Value:"群星", Source:"compilation", Confidence:"high", Action:"auto_apply", RuleID:"compilation_v1", Candidates: 原 artist.Candidates}`；
   - `*albumArtist = FieldDecision{}`（Value 为空，organizer 已有逻辑不写入空决定，`c.AlbumArtist` 为空 → `releases.album_artist=NULL`、不写 `release_credits`）。
3. 其余不变。

organizer.go 只在 `albumArtistDecision := ...` 之后加一行 `applyCompilation(decisionObservations, &artistDecision, &albumArtistDecision)`。不改 partition 与分组：占位写法不同（`VA` 与 `Various Artists` 混在同目录）时仍按现有规则处理。

### 3.4 artistgraph 与读取

- `refreshFacts`：`release_artist` 的 scan 事实在原有 `inferred` 条件之外，`metadata.IsCompilationPlaceholder(r.artist)` 时也跳过；`album_artist` 的 scan 事实在 `IsCompilationPlaceholder(r.album)` 时跳过；correction 层值是占位词时同样跳过（管理员把专辑艺人改成“群星”即手动标为合辑）。曲目艺人与各署名角色不受影响。
- `artistgraph.Version` 2→3（`state.go`）。`releasesql.ArtistRefsReady` 已取共享常量，不得硬编码。
- 读取侧不改：合辑发行 `artist="群星"`、`album_artist=null`、`artist_refs=[]`、`album_artist_refs=[]`；前端 `albumArtistOf` 得到“群星”，`ArtistNames` 在 refs 为空时显示纯文字，不能点进艺人页。
- 字段证据新出现 `source="compilation"`、`rule_id="compilation_v1"`；前端 `evidenceSourceLabel` 增加 `compilation: "合辑"`。REST 形状不变（来源与规则是开放字符串）。

### 3.5 版本号

版本常量名按 ④ 定稿（`11-08-scan-incremental/design.md` 3.1、3.2、§8）：`parser.CommonVersion`、`parser` 的 `formatVersions`（按扩展名）、`ingest.GroupingVersion`。

| 版本 | 本项变化 | 理由 |
| --- | --- | --- |
| `parser.CommonVersion` | 计入 T 线的 +1 | 多值拆分、专辑艺人回退、TXXX、合辑标志、原始年份都是共用规则或跨格式改动；不单独改 `formatVersions` |
| `ingest.GroupingVersion` | 计入 T 线的 +1 | organizer 加了 `applyCompilation`，同样的观察会写出不同的字段决定 |
| `artistgraph.Version` | 2 → 3 | 已存原文要按新拆分与占位规则重新整理 |
| `searchindex.ProjectionVersion` | 1 → 2 | 多名搜索值按新拆分重建 |
| `textnorm.Version` | 不变 | 见 ⑬ 设计：升它会让全部艺人名字键换新 |

T 线四项一起合入 main，各版本合计只升一次：`CommonVersion` +1（⑥ 多值与合辑标志、⑦ 新键、⑧ 作品标签）、`GroupingVersion` +1（⑥ organizer 的一行、⑧ 写入路径的 `AssignWorks`）、`artistgraph.Version` 2→3、`ProjectionVersion` 1→2。

两种合入顺序：

- **④ 先合入 main**：T 线 rebase 到 main 后，在 main 当时的值上把 `CommonVersion`、`GroupingVersion` 各加 1；下一次普通扫描即按新规则重新解析、重写候选。
- **T 线先合入 main**：此时还没有这两个常量，不升；④ 合入时以 T 线合入后的规则为基线（版本 1）。T 线合入后到 ④ 合入前，每次扫描都全量解析、重写，规则在下一次扫描生效。

`artistgraph.Version` 与 `ProjectionVersion` 的升级与合入顺序无关，部署后由后台整理自动生效，不依赖扫描。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 21.1 两层解析，规范键覆盖帧名键（2176–2192） | 改写 | 参考的“规范层”是它自己写回的标签格式；ROOMusic 只读外部标签，按容器原生键优先、变体键回退（§3.2） |
| 21.1 单值取第一个、Trim 后空串视为不存在（2191） | 照搬 | 逐级空判断的基础 |
| 21.2 键名统一大写比较（2196–2200） | 照搬 | 现有 `strings.ToUpper` 已如此 |
| 21.2 TXXX `[键]值` 位置算术解包（2203–2218） | 不采用 | 原文自认只在特定布局下正确且静默失败；按 ID3 规范解析描述与值 |
| 21.3 艺人类分隔符只有 `;` 与换行（2220–2235） | 改写 | 换行照搬；ROOMusic 已有更细的明确分隔符；逗号按用户裁决“拉丁姓, 名不拆、其余照拆”（样本库 `RADWIMPS, 陣内一真`、`陣内一真, 野田洋次郎` 必须拆） |
| 21.3 不切逗号的四条理由（2237–2242） | 改写 | 只在拉丁“姓, 名”成立时整体保留，并用团体词排除乐团名 |
| 21.3 流派分隔符含逗号（2225） | 不采用 | 流派合同已裁决不拆 `,`，不在本项 |
| 21.3 专辑艺人单值拼接：首个有值键、展开、去重、`" / "` 连接（2244） | 照搬 | `JoinArtistValues` |
| 21.4 专辑艺人回退链 `TPE2→ALBUMARTIST→ALBUM_ARTIST→ALBUM ARTIST`（2255） | 照搬（按容器分级） | 每种容器只出现自己的键 |
| 21.4 专辑艺人继续回退到 `ARTIST→TPE1→PERFORMER`（2255） | 不采用 | ROOMusic 合同规定缺失专辑艺人是“未知”，由 organizer 对曲目艺人多数决，不在解析层冒充明确值 |
| 21.4 逐级空判断 vs 逐键覆盖（2272） | 照搬（逐级空判断） | 专辑艺人、原始年份都用首个非空 |
| 21.7 布尔真值 `{1,yes,true}`，其它为假（2381） | 照搬 | |
| 21.7 合辑：`ISCOMPILATION/COMPILATION/TCMP`，值为 `compilation` 也为真（2391） | 照搬并补 M4A `cpil`、`ITUNESCOMPILATION`、`TXXX:COMPILATION` | 样本库 84 个 M4A 都带 `cpil=0`，必须读成假 |
| 21.8 原始发行日期 `ORIGINALDATE→TDOR→TORY`（2409） | 改写 | 只作 `Year` 缺失时的回退，不新增字段（已裁决） |
| 21.8 发行日期链以 `TDOR` 开头（2407） | 不采用 | 会让原始年份覆盖本版年份 |
| 22.10 合辑占位 22 条、整串忽略大小写相等（3450–3459） | 改写 | 整串比较照搬；去掉 `unknown`、`unknown artist`（与“未知艺术家是真实署名”的艺人合同冲突）、`misc`、`sampler`、`karaoke`（可能是真名）；补 `v/a`、`v.a` 与中日文写法；全半角折叠 |
| 22.10 未知作曲家、非人名 160 条（3461–3471） | 不采用 | 数据表不搬；ROOMusic 不按字符串黑名单判断“是不是人” |
| 22.12 第 19 条三张表整串相等、null 为假（3610） | 照搬（占位词表） | |
| 23.14 合辑三路或：标志 ∨ 原声带 ∨ 艺人==群星（4152） | 改写 | 标志按候选内严格过半；原声带不算合辑（原声带多有真实专辑艺人）；真实专辑艺人优先 |
| PRD 不做：超过 4 位艺人归为合辑 | 不采用 | 违反“不猜人” |

## 5. 规则输入输出对照

拆分（`CanonicalArtistNames` 输入 → 输出，顺序为拆分后、去重前）：

| 输入 | 输出 | 说明 |
| --- | --- | --- |
| `成龙,金喜善` | `成龙`、`金喜善` | 网易云紧凑逗号（样本库文件名写法） |
| `知更鸟,HOYO-MiX,Chevy` | `知更鸟`、`HOYO-MiX`、`Chevy` | 多于一个逗号照拆 |
| `RADWIMPS, 陣内一真` | `RADWIMPS`、`陣内一真` | 右侧非拉丁（样本库专辑艺人） |
| `周杰伦, 费玉清` | `周杰伦`、`费玉清` | |
| `Bach, Johann Sebastian` | `Bach, Johann Sebastian` | 拉丁姓, 名 |
| `Karajan, Herbert von` | `Karajan, Herbert von` | 姓氏前缀 |
| `Dvořák, Antonín` | `Dvořák, Antonín` | 变音字母 |
| `Bach, J.S.` | `Bach, J.S.` | |
| `Berliner Philharmoniker, Herbert von Karajan` | `Berliner Philharmoniker`、`Herbert von Karajan` | 团体词排除 |
| `London Symphony Orchestra, Colin Davis` | 两人 | 团体词排除 |
| `Bach, Johann Sebastian; Karajan, Herbert von` | 两个整体 | 先按 `; ` 拆再逐段判定 |
| `Bach, Johann Sebastian, Gould, Glenn` | 四段 | 多逗号照拆（已知局限） |
| `A\nB`、`A\r\nB`、`A\x00B` | `A`、`B` | 换行/NUL 作分隔 |
| `Bach, Johann Sebastian\nGould, Glenn` | 两个整体 | 换行先切，再逐段判定 |
| `Tom & Jerry` | `Tom`、`Jerry` | 已裁决：拆开，除非在固定组合表里 |
| `Simon & Garfunkel`、`AC/DC`、`Earth, Wind & Fire` | 整体 | 固定组合不变 |
| `张卫帆 & 郑宜农` | `张卫帆`、`郑宜农` | 不变（样本库） |
| `Queen, David Bowie` | 整体 | 已知误判，见 §7 |

专辑艺人（解析结果 `AlbumArtist`）：

| 标签 | 结果 |
| --- | --- |
| Vorbis `ALBUMARTIST=A`、`ALBUMARTIST=B` | `A / B` |
| Vorbis 只有 `ALBUM_ARTIST=A` | `A` |
| Vorbis `ALBUM ARTIST=X` 与 `ALBUMARTIST=Y` | `Y`（级 1 优先） |
| ID3 `TPE2="A\0B"` | `A / B`（现状只取 `A`） |
| ID3 无 `TPE2`，`TXXX:ALBUMARTIST=A` | `A` |
| M4A `aART` 缺失，`----:ALBUMARTIST=A` | `A`（现状不参与） |
| Vorbis `ALBUMARTIST="A\nB"` | `A / B`（现状 `AB`） |

合辑：

| 候选 | artist | album_artist | 艺人实体 |
| --- | --- | --- | --- |
| 专辑艺人 `Various Artists`，曲目艺人各异 | 群星 | null | 只有各曲目艺人 |
| 专辑艺人 `ＶＡ`（全角） | 群星 | null | 同上 |
| 无专辑艺人，`COMPILATION=1` 全部曲目 | 群星 | null | 同上 |
| 无专辑艺人，10 轨中 3 轨 `TCMP=1` | 曲目艺人多数决 | null | 不变 |
| 专辑艺人 `周杰伦`，`cpil=1` | 周杰伦 | 周杰伦 | 不变 |
| `cpil=0`（样本库 M4A） | 不变 | 不变 | 不变 |
| 无专辑艺人，曲目艺人多数是 `群星` | 群星 | null | 曲目艺人“群星”仍建实体（已裁决） |
| 管理员把专辑艺人修正为 `群星` | 群星（修正值） | 群星 | 发行级不建实体 |

## 6. 兼容与已有数据

### 6.1 生效路径

- 部署（`artistgraph.Version=3`）后后台整理重跑全部发行：发行级与曲目艺人按新规则从已存原文重拆；合辑占位的发行级署名消失。整理期间艺人页 503 `artist_catalog_preparing`、艺人名是纯文字（现有协议）。
- `track_credits` 的名字是解析时拆好的，旧拆分（`Bach` + `Johann Sebastian`）要等下一次扫描（④ 已在 main 时靠 `CommonVersion` +1 触发重新解析，见 §3.5）。
- `releases.artist` 显示“群星”、新合辑判定、TPE2 多值、TXXX 回退都在下一次扫描后生效。

### 6.2 人工意图

人工意图都按**名字键**绑定（`artist_name_bindings`、`artist_assignment_overrides`、作用域拆分）或按艺人 ID（`artist_overrides`、收藏）。规则只改变“一个原文拆成哪些名字”，不改名字键算法（`textnorm.Version` 不变），所以：

- 新拆分下仍出现的名字（绝大多数）：同一名字键，意图原样生效。
- 新拆分下不再出现的旧名字（如 `bach`、`johann sebastian` 被 `bach, johann sebastian` 取代，`ab` 被 `a`、`b` 取代，`various artists` 不再建发行级事实）：旧名字键与艺人保留，没有 present 参与后从列表消失（艺人合同“只列有 present 参与的身份”），收藏仍在、显示为暂不可用。意图不自动搬到新名字（不猜人）。
- 新出现的名字：按现有规则建键与艺人，同键自动归一。

扫描与整理不写任何人工意图表（艺人合同既有约束）。生产库上人工意图（绑定、指定、艺人修改）与艺人收藏都是 0（§9），现有数据上没有会失联的意图，部署前后不需要再到生产库对比。

### 6.3 样本库上会变化的名字

用 `/tmp` 下的只读脚本解出样本库 199 个 FLAC、84 个 M4A 的标签（不在仓库内）核对：

- 多值与分隔：`成龙/金喜善`、`知更鸟/HOYO-MiX/Chevy`、`张碧晨; 兰天奇`、`RADWIMPS, 陣内一真`、`陣内一真, 野田洋次郎`、`张卫帆, 严云农 & 陈品伶` 等**拆分结果不变**。没有拉丁“姓, 名”写法，没有换行或 NUL 多值，没有 `TXXX`、`ALBUM_ARTIST` 变体。
- 合辑：84 个 M4A 的 `cpil` 全是 0，没有合辑占位写法 → 不会出现“群星”发行。
- 生产库（§9）与此一致：含逗号的原文只有 `RADWIMPS, 陣内一真` 一类日文/中文名单，没有拉丁“姓, 名”，没有合辑占位。
- 结论：艺人数与名字**预期不变**；真实库 smoke 在本机隔离实例上对 `music/` 跑（八号机文件清单与 `music/` 完全相同），以“前后艺人名字集合相同、合辑数为 0”为准。

## 7. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 拉丁两人用 `, ` 连写（`Queen, David Bowie`、`Beyoncé, Jay-Z`）被当成一个人 | 用户已裁决的规则代价；团体词排除乐团名。管理员可用艺人改名，或给 `knownFixedArtistGroups` 的反向表补例外（后续需要时再加） |
| `Tom & Jerry` 类组合名仍被拆开 | 已裁决：照现状拆，组合名靠固定组合表补 |
| 艺人整理一次全库重跑 | 现有后台协议：分批、可续跑、整理期 503；I9 已接受回填耗时 |
| `TXXX` 解析读到异常帧 | 只按规范读，描述或值无效就跳过该帧，不让整个文件失败 |
| 合辑误判 | 只认整串占位词或过半标志；真实专辑艺人优先；管理员可修正专辑艺人（修正值不是占位词时恢复建实体） |

回滚：本项无迁移。回退代码后 `artistgraph.Version` 回到 2，后台按旧规则重新整理；`releases.artist="群星"` 的合辑在下一次扫描后恢复为占位原文或曲目艺人多数决。

## 8. 裁决

已裁决（2026-11-08，见 prd.md）：

- 原始年份（`ORIGINALDATE`、`TDOR` 等）只在本版年份缺失时作回退，不单独保存、不显示。
- 带空格的 ` & ` 照现状拆开，组合名靠固定组合表补；`Tom & Jerry` 按“拆开，除非在固定组合表里”对照。
- 曲目艺人写成合辑占位词（如合唱曲的“群星”）时照常建艺人；只有专辑艺人和发行艺人是占位词时不建实体（§3.4）。

## 9. 生产库统计

已执行，见 [m8-production-stats.md](../11-08-scan-reference-port/research/m8-production-stats.md)。与本项相关：艺人 67、名字键 68、有效署名 646（前后对比基线）；人工意图与艺人收藏都是 0；含逗号的原文只有日文/中文名单 6 种，没有拉丁“姓, 名”；合辑占位 0。

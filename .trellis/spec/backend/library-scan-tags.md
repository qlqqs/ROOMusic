# 标签、署名与古典作品（⑥⑦⑧）

从[目录与扫描流程合同](./library-scan-contracts.md)拆出：§3 的标签多值、署名与合辑（⑥⑦），古典作品与乐章（⑧）；章节编号沿用原合同。

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
  M4A 为 `aART → ----:ALBUMARTIST → ----:ALBUM_ARTIST/----:ALBUM ARTIST`；三种入口最后均回退到 `ALBUMPERFORMER`（ID3/M4A 保留 `TXXX:`/`----:` 前缀）。同级合并，全部键记入 `album_artist` 原文证据，`ALBUMPERFORMER` 不当署名。
  `FEATURING` 在三种入口记为 `track_artist`，与 `feat.` 拆出的艺人共用姓名拆分、去重及曲目艺人路径，不归 performer。
  ID3 文本帧保留全部 NUL 值；TXXX 按编码解出描述与值，描述去空白转大写后交给通用标签入口。
- **署名角色表**：`metadata.Credit{Role,Name,Detail,Source}` 的 `detail/source` 随暂存 JSON 保存（空 source 等于 tag）。
  `MapCreditRole` 与 `MapInstrument` 的表版本为 `CreditRoleTableVersion=1`；匹配先去首尾空白、小写、压连续空白，再去结尾 ` by`，不做相似度匹配。
  作词/剧本归 lyricist，作曲归 composer，编曲/配器归 arranger，指挥归 conductor，制作人/监制归 producer；
  `词曲/songwriter/writer` 等一人双产出 lyricist 与 composer。混音、母带、录音、重混、DJ混音、编程归 producer，
  detail 分别为 `mix/mastering/recording/remix/dj_mix/programming`；剧本为 `libretto`，声乐/和声及乐器归 performer。
  乐器写法和别名只取 `metadata/credit_roles.go` 的小表，不搬外部大表、不展开方括号修饰。
- **署名来源**：Vorbis、M4A `com.apple.iTunes` freeform、ID3 TXXX 支持
  `COMPOSER/LYRICIST/WRITER/ARRANGER/CONDUCTOR/PRODUCER/MIXER/ENGINEER/REMIXER/MIXARTIST/DJMIXER/PERFORMER/PERFORMERS/SOLOIST/SOLOISTS/ENSEMBLE/ORCHESTRA/LIBRETTIST/LIBRETTO/VOCALS/VOCALIST/REMIXED BY/AUTHOR`；
  `ENSEMBLE/ORCHESTRA` 为 performer + orchestra，`LIBRETTO` 为 lyricist + libretto，`VOCALS/VOCALIST` 为 performer + vocals，`REMIXED BY` 为 producer + remix，`AUTHOR` 在键入口归为 lyricist；未知键忽略。
  ID3 `TCOM/TEXT/TPE3` 分别为作曲/作词/指挥；M4A `©wrt/©con/©prf` 分别为作曲/指挥/演奏；WAV RIFF INFO `IPRO/IWRI` 分别为制作人/作词。
  `PERSONNEL`（Vorbis/freeform/TXXX）每值逐行依次找制表符、两个空格、` - `，前段人名、后段工种/乐器去首尾空白且均非空才保留；工种先查角色表、再查乐器表，都不中归 performer + 原写法，不按逗号拆工种。
  `INVOLVEDPEOPLE`（Vorbis/freeform/TXXX）与 ID3 `TIPL/IPLS/TMCL` 保留 NUL 成对读取（工种/乐器在前、人名在后，落单尾值丢弃）。非 NUL 值逐行依次尝试：`名字(工种)` 取首个匹配并保持顺序；字面串 `0x00` 分隔且段数为偶数；分号分段且每段含冒号（首个冒号前工种、后人名）；分号分段且段数为偶数（工种、人名成对）。不展开方括号语义。
  TIPL/IPLS/INVOLVEDPEOPLE 走角色表，TMCL 走乐器表；未知前者归 producer，未知后者归 performer，detail 保留原写法（≤256字节）。
  署名键原文照常记艺人证据，多角色取第一个；人员表的 tag_key 为 `<来源键>:<工种/乐器>`（保留 `TXXX:`/`----:` 前缀，≤128字节），歌词不写标签证据。
- **演奏括号**：`PERFORMER(S)/SOLOIST(S)` 先用 `metadata.SplitExplicitArtistValues` 按换行/NUL及⑥明确分隔符列表切段（与 `artist.go` 共用来源），再逐段解析末尾中英文圆括号，最后对拆出的名字执行完整姓名拆分。第1步按中英文圆括号 `()`/`（）` 的深度只切括号外的明确分隔符，括号内内容完整交给第2步按逗号、顿号、` & `、` and ` 拆乐器或角色；不使用占位符、不扩展多段括号语法，TMCL 的 NUL 成对规则保留，文本形态见上条。括号内按原乐器规则切段，
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

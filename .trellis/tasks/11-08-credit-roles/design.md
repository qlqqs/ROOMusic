# ⑦ 署名角色扩充：技术设计

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 2 项，在 ⑥ 之后（共用拆分规则与 TXXX 入口）。需求见 [prd.md](./prd.md)。
参考设计只当数据读：[reference-design.md](../11-08-scan-reference-port/research/reference-design.md)，只借鉴 21.5、23.1–23.7 的结构，不搬数据表。

## 1. 边界与归属

| 归属 | 本项改动 |
| --- | --- |
| `catalog/metadata` | `Credit` 增加 `Detail`、`Source`；新文件 `credit_roles.go`：规范角色、角色映射表、乐器与工种表、表版本；`CanonicalCredits` 按固定角色序排序并按角色+名字+细分去重 |
| `library/parser` | 新键读取（Vorbis/freeform/TXXX、ID3 `TEXT`/`TIPL`/`IPLS`/`TMCL`）；`名字 (乐器)` 拆分；新文件 `lyrics_credits.go` 读歌词开头署名 |
| 迁移（合入编号 0038） | `track_credits` 角色白名单、`detail`、`source`、唯一键；`artist_credits`、`artist_tag_evidence`、`catalog_search_values` 的角色 CHECK |
| `catalog/ingest` | `track_credits` 写入 `detail`、`source` |
| `catalog/internal/artistgraph` | 新角色进事实；同一曲目同人同角色去重；括号别名排除乐器词；`Version` 计入 T 线的一次升级 |
| `catalog/internal/searchindex`、`catalog` 读取与 HTTP | 角色白名单与排序扩展；`ReleaseCredit.detail` |
| 前端 | 曲目行署名按细分显示；角色词表、筛选、艺人页分栏、搜索命中说明增加作词、编曲 |

## 2. 数据流

```
标签键 / TIPL·TMCL 成对值 / PERFORMER "名字 (乐器)" / 歌词开头署名行
   │  metadata.MapCreditRole(raw) → []RoleAssignment{Role, Detail}
   ▼
AudioObservation.Credits []Credit{Role, Name, Detail, Source}
   │  名字经 ⑥ 的拆分规则；同一角色若已有 tag 署名，歌词来源的该角色全部丢弃
   ▼
SourceObservation.Credits（暂存 JSON 自然带上新字段）
   ▼
persist：DELETE + INSERT track_credits(track_id, role, name, detail, source, position)
   ▼
artistgraph.refreshFacts：按 (track, role, name) 去重后写 artist_credits（不带 detail）
searchindex：credit 值按角色写入（detail 不进搜索）
   ▼
REST ReleaseTrack.credits[] = {role, name, detail, artist_refs}
前端：有 detail 的按细分名分组显示（“小提琴：Hilary Hahn”），否则按角色名
艺人页：按 role 分栏，演奏仍在“演奏”，作词、编曲新增两栏
```

## 3. 接口合同

### 3.1 规范角色与顺序

曲目级署名角色由 4 个扩为 6 个，固定顺序（排序、显示、筛选、SQL 的 `CASE` 都用它）：

`lyricist`（作词）→ `composer`（作曲）→ `arranger`（编曲）→ `conductor`（指挥）→ `performer`（演奏）→ `producer`（制作人）

艺人角色词表由 7 个扩为 9 个：`release_artist`、`album_artist`、`track_artist`、`lyricist`、`composer`、`arranger`、`conductor`、`performer`、`producer`。
`artistRoleOrder`（`catalog/artists_personal.go`）、`searchRoleOrder`（`catalog/search_sql.go`）、`artists_summary_sql.go` 的角色 VALUES、`releasesql/artist_refs.go` 的 `credits.role IN (...)` 同步。

`CanonicalCredits` 改为按上面的角色序 → `detail`（空在前）→ 名字排序，按 `role+name+detail` 去重；`position` 因此重排，重扫时整体替换，不影响身份。

### 3.2 映射表（`metadata/credit_roles.go`，`CreditRoleTableVersion = 1`）

匹配键：原始串去首尾空白 → 小写 → 连续空白压成一个 → 去掉结尾的 ` by`（`arranged by` → `arranged`）。只做这一种归一（参考 23.5 只小写），其余写法逐条列在表里。一个原始串可产出多个 `(role, detail)`。

角色表（`MapCreditRole`）：

| 原始写法（归一后） | 角色 | 细分 detail |
| --- | --- | --- |
| `lyricist`、`lyrics`、`words`、`text`、`作词`、`词`、`填词` | lyricist | — |
| `librettist`、`libretto` | lyricist | `libretto` |
| `composer`、`composed`、`music`、`作曲`、`曲` | composer | — |
| `songwriter`、`written`、`writer`、`written-by`、`composerlyricist`、`词曲`、`作词作曲`、`词曲创作` | lyricist + composer | — |
| `arranger`、`arranged`、`arrangement`、`orchestrator`、`orchestration`、`orchestrated`、`编曲`、`配器` | arranger | — |
| `conductor`、`conducted`、`指挥` | conductor | — |
| `producer`、`produced`、`co-producer`、`executive producer`、`制作人`、`制作`、`监制` | producer | — |
| `mix`、`mixer`、`mixing`、`mixed`、`mixing engineer`、`混音`、`混音师`、`混音工程师` | producer | `mix` |
| `mastering`、`mastered`、`mastering engineer`、`母带`、`母带处理`、`母带工程师` | producer | `mastering` |
| `engineer`、`recording engineer`、`recording`、`recorded`、`录音`、`录音师`、`录音工程师` | producer | `recording` |
| `remixer`、`remix`、`remixed`、`重混` | producer | `remix` |
| `dj-mix`、`dj mix`、`djmixer` | producer | `dj_mix` |
| `programming`、`programmed`、`编程` | producer | `programming` |
| `performer`、`soloist`、`演奏` | performer | — |
| `vocals`、`vocal`、`vocalist`、`lead vocals`、`演唱`、`主唱` | performer | `vocals` |
| `backing vocals`、`background vocals`、`和声`、`和音` | performer | `backing_vocals` |

乐器表（`MapInstrument`，用于 TMCL 与 `名字 (乐器)`，结果一律 `role=performer`）：

| detail | 写法 | 中文名 |
| --- | --- | --- |
| `violin` | violin, violins, 小提琴 | 小提琴 |
| `viola` | viola, 中提琴 | 中提琴 |
| `cello` | cello, violoncello, 大提琴 | 大提琴 |
| `double_bass` | double bass, contrabass, upright bass, 低音提琴 | 低音提琴 |
| `bass` | bass, bass guitar, electric bass, 贝斯, 贝司 | 贝斯 |
| `guitar` | guitar, acoustic guitar, electric guitar, classical guitar, 吉他 | 吉他 |
| `piano` | piano, pianoforte, 钢琴 | 钢琴 |
| `keyboards` | keyboard, keyboards, 键盘 | 键盘 |
| `organ` | organ, pipe organ, 管风琴 | 管风琴 |
| `harpsichord` | harpsichord, 羽管键琴, 大键琴 | 羽管键琴 |
| `synthesizer` | synthesizer, synth, 合成器 | 合成器 |
| `drums` | drums, drum kit, drum set, 鼓, 架子鼓 | 鼓 |
| `percussion` | percussion, 打击乐 | 打击乐 |
| `flute` | flute, 长笛 | 长笛 |
| `clarinet` | clarinet, 单簧管 | 单簧管 |
| `oboe` | oboe, 双簧管 | 双簧管 |
| `bassoon` | bassoon, 巴松管, 大管 | 巴松管 |
| `saxophone` | saxophone, sax, alto saxophone, tenor saxophone, 萨克斯, 萨克斯风 | 萨克斯 |
| `trumpet` | trumpet, 小号 | 小号 |
| `trombone` | trombone, 长号 | 长号 |
| `horn` | horn, french horn, 圆号 | 圆号 |
| `harp` | harp, 竖琴 | 竖琴 |
| `erhu` | erhu, 二胡 | 二胡 |
| `pipa` | pipa, 琵琶 | 琵琶 |
| `guzheng` | guzheng, 古筝 | 古筝 |
| `dizi` | dizi, 笛子, 竹笛 | 笛子 |
| `vocals` | vocals, vocal, voice, singer, 演唱, 主唱 | 演唱 |
| `backing_vocals` | backing vocals, background vocals, 和声, 和音 | 和声 |
| `choir` | choir, chorus, 合唱 | 合唱 |
| `orchestra` | orchestra, ensemble, 乐团 | 乐团 |

工种细分的中文名：`mix` 混音、`mastering` 母带、`recording` 录音、`remix` 重混、`dj_mix` DJ 混音、`programming` 编程、`libretto` 剧本。

映射不到时（参考 23.5 兜底二“保留原始串”）：

- `TIPL`/`IPLS` 的未知工种 → `producer`，`detail` = 原写法（去空白，≤256 字节）。TIPL 是“参与人员表”，多为制作侧工种。
- `TMCL` 的未知乐器、`PERFORMER` 括号里的未知内容（拉丁写法）→ `performer`，`detail` = 原写法。TMCL 是“乐手表”。
- 键名映射不到的 Vorbis/freeform/TXXX 键：不读（不是署名键）。

`detail` 合同：`null`，或已知细分键（小写 ASCII 与下划线），或原写法文本。前端对已知键显示中文名，其余原样显示（与现有词表“未知值原样显示”一致）。

### 3.3 读取来源

| 容器 | 键 → 角色 |
| --- | --- |
| Vorbis、M4A freeform（`com.apple.iTunes`）、ID3 `TXXX`（⑥ 的统一入口） | `COMPOSER`、`LYRICIST`、`WRITER`、`ARRANGER`、`CONDUCTOR`、`PRODUCER`、`MIXER`、`ENGINEER`、`REMIXER`、`MIXARTIST`、`DJMIXER`、`PERFORMER`、`PERFORMERS`、`SOLOIST`、`SOLOISTS`、`ENSEMBLE`、`ORCHESTRA`、`LIBRETTIST`：键名本身经角色表映射；`ENSEMBLE`/`ORCHESTRA` 为 performer + `orchestra` |
| ID3 | `TCOM` composer；`TEXT` lyricist；`TPE3` conductor；`TIPL`（v2.4）与 `IPLS`（v2.3）按 NUL 成对 `(工种, 人名)` 走角色表；`TMCL` 成对 `(乐器, 人名)` 走乐器表。奇数个值时丢掉最后一个落单值 |
| M4A | `©wrt` composer；其余走 freeform |

`名字 (乐器)`：只对 performer 角色的值（`PERFORMER`、`PERFORMERS`、`SOLOIST(S)`）做，正则 `^(.+?)\s*[(（]([^()（）]+)[)）]\s*$`。括号内按 `,`、`、`、` & `、` and ` 切开，每段先查乐器表（命中 → performer + 该细分），再查角色表（命中 → 该角色，如 `conductor`），都未命中且该段只含拉丁字母、空格、`-` → performer + 原写法；任一段未命中且含其它文字（如 `杨璐（羊鹿鹿）`）→ **整串不拆**，当名字。人名部分再走 ⑥ 的拆分规则。

艺人原文证据：新键照 `artistTagRole` 记入 `artist_tag_evidence`，角色取映射后的规范角色（多角色时取第一个）；`TIPL`/`TMCL` 的 `tag_key` 写 `TIPL:<工种原写法>`、`TMCL:<乐器原写法>`（截到 128 字节）。歌词来源不写证据。

### 3.4 歌词开头署名（`parser/lyrics_credits.go`）

在全部标签读完、`Lyrics` 已规范化之后执行（与 ⑥ 的 `finalizeTags` 同一位置）：

1. 按 `\n` 切行，最多看前 30 行。每行先去掉开头连续的 LRC 方括号标签（`[mm:ss.xx]`、`[ti:..]` 等），再去首尾空白（含全角空白 U+3000）。
2. 空行、只有方括号标签的行跳过，不打断。
3. 尚未读到任何署名行时，最多跳过一行媒体题头，视为与 `[ti:]`/`[ar:]` 相同的元信息：按第1步处理后，该行等于解析出的曲目 `Title`，或以 `Title + 空白 + - + 空白` 开头（也接受 `–`、`—`，标题比较忽略大小写）；`Title` 为空则不跳过，不使用文件名补位，不改变 `finalizeTags` 时序。其它非署名行仍结束署名块。
4. 其余行匹配 `^(标签)\s*[:：]\s*(值)$`，标签 1–16 个字符且不含 `:`/`：`。标签解析：按 `/`、`、`、`&` 切成多个子标签，每个去空白小写后先整体查表；查不到时取其中连续的中文字符段查表，再取连续的拉丁段查表。“表”= §3.2 的角色表加乐器表。
5. 只要有一个子标签命中，该行算署名行；第一个**不是**署名行的行出现即结束。没命中的子标签忽略（`吉他间奏/录音/制作人` 读录音与制作人）。
6. 命中的子标签**全部读成署名**（PRD R2b，用户已裁决）：
   - 表里没有细分的角色（作词、作曲、编曲、指挥、制作人、监制、`词曲` 双产出）→ 该角色，`detail` 为 null；
   - 表里带细分的（混音、母带、录音、重混、编程、演唱、和声）和乐器表命中的（吉他、鼓、贝斯、Guitar、Cello 等）→ 映射到的角色（制作人或演奏），`detail` 存**歌词里的原写法**（命中的那段子标签原文，如 `混音`、`混音工程师`、`Guitar`），曲目行显示原写法（“混音：某某”“Guitar：赵米”）。标签来源的细分仍存规范键（如 `MIXER` → `mix`，显示“混音”）。
7. 值按 ⑥ 的拆分规则拆名字；每个名字去掉从 `@` 起到结尾的所属团体或工作室后缀（`怪兽@五月天` → `怪兽`、`时兵@Push Studio` → `时兵`），去掉后为空则保留原文。`Source="lyrics"`。
8. 观察里某角色已有 `Source="tag"` 的署名时，歌词来源的该角色全部丢弃。

### 3.5 迁移（合入编号 0038）

```sql
ALTER TABLE track_credits DROP CONSTRAINT track_credits_role_check;
ALTER TABLE track_credits ADD CONSTRAINT track_credits_role_check
  CHECK (role IN ('lyricist','composer','arranger','conductor','performer','producer'));
ALTER TABLE track_credits ADD COLUMN detail TEXT
  CHECK (detail IS NULL OR (btrim(detail) <> '' AND octet_length(detail) <= 256));
ALTER TABLE track_credits ADD COLUMN source TEXT NOT NULL DEFAULT 'tag'
  CHECK (source IN ('tag','lyrics'));
ALTER TABLE track_credits DROP CONSTRAINT track_credits_track_id_role_name_key;
ALTER TABLE track_credits ADD CONSTRAINT track_credits_track_role_name_detail_key
  UNIQUE NULLS NOT DISTINCT (track_id, role, name, detail);
-- artist_credits：轨级角色 CHECK 增加 lyricist、arranger（发行级不变）
-- artist_tag_evidence：role CHECK 增加 lyricist、arranger
-- catalog_search_values_role_ck：credit 角色增加 lyricist、arranger
```

约束名按 PG 默认生成，实施前在隔离测试库应用到 0033 后用 `\d track_credits`、`\d artist_credits`、`\d artist_tag_evidence` 核对（0026 的表级 CHECK 没有显式名字）。

`.down.sql`：删除 `role IN ('lyricist','arranger')` 的 `track_credits`、`artist_credits`（及级联的 `artist_effective_credits`）、`artist_tag_evidence`、`catalog_search_values` 行；同一 `(track_id, role, name)` 多个 detail 的只留 `position` 最小的一行；删列、恢复原 CHECK 与唯一键。回退后由旧程序发现投影版本不一致自行重建。`rollbackFixtures` 补一条带新角色与 detail 的数据。

### 3.6 REST 形状变化

| 位置 | 变化 |
| --- | --- |
| `ReleaseTrack.credits[]`、`ReleaseDetail.credits[]` | 增加必传 `detail: string \| null`（发行级署名恒为 null）。`role` 允许值增加 `lyricist`、`arranger`。数量上限不变 |
| `ArtistSummary.roles`、艺人详情 `role_counts[].role`、`/artists/{id}/albums?participation=any` 的 `roles` | 词表 7 → 9 |
| `GET /artists?role=` | 允许值 7 → 9 |
| `GET /artists/{id}/tracks?role=` | 允许值 5 → 7（增加 lyricist、arranger） |
| 搜索 `match.role` | 增加 `lyricist`、`arranger` |
| 不变 | 搜索不按 detail 匹配；`catalog/stats` 的“作曲家”仍只数 composer |

示例：

```json
{"role":"performer","name":"Hilary Hahn","detail":"violin","artist_refs":[{"artist_id":"…","name":"Hilary Hahn","display_name":"Hilary Hahn"}]}
{"role":"lyricist","name":"方文山","detail":null,"artist_refs":[…]}
{"role":"producer","name":"某某","detail":"mix","artist_refs":[…]}
```

前端解码器是严格枚举（`artistRoles`、`artistTrackRoles`、`matchRoles`），**必须与后端同批合入**，否则旧前端遇到新角色整页解码失败。

### 3.7 前端显示

- 曲目行 `CreditsLine`：分组键 = `detail ?? role`；组名 = `detail` 非空时 `creditDetailLabel(detail)`（已知键中文名，其余原样），否则 `creditRoleLabel(role)`；组顺序按接口顺序（后端已按 §3.1 排好）。例：“作词：方文山；作曲：周杰伦；编曲：林迈可；小提琴：Hilary Hahn；混音：某某”。
- 角色中文名：`lyricist` 作词、`arranger` 编曲，加进 `creditRoleLabel`、`artistRoleLabels`、`roleNames`（搜索命中说明）。
- 艺人页按角色分栏、艺人列表角色筛选：词表加两项，仍只列有曲目的角色（`DESIGN-components.md:155` 改为“无数据的角色不显示”）。
- 不加任何解释性文字。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 21.5 角色列表映射：同字段多键全部累积、值不存在才加入（2301） | 照搬 | 多键累积，按 `role+name+detail` 去重 |
| 21.5 键表：COMPOSER/TCOM、CONDUCTOR/TPE3、PRODUCER、LYRICIST、ARRANGER、MIXER、ENGINEER、REMIXER、DJMIXER、SOLOIST、ENSEMBLE、ORCHESTRA、LIBRETTIST（2303–2323） | 照搬（映射到 6 个规范角色） | 用户裁决只新增作词、编曲；其余并入最接近的角色并保留细分 |
| 21.5 `©CON`、`IPRO`、`IWRI`、`AUTHOR`、`FEATURING`、`VOCALS` 键（2309–2323） | 不采用 | 样本库与常见写入器（Picard、foobar2000、iTunes）不写；`FEATURING` 已由曲目艺人 `feat.` 处理 |
| 21.5 `PERSONNEL` 三级分隔（2327–2338） | 不采用 | 非标准键，原文自认逗号无效 |
| 21.5 TIPL/TMCL 四种形态（2340–2347） | 改写 | 只实现 ID3 规范的 NUL 成对形态；括号形态只用于 PERFORMER 值；分号/冒号形态不采用（不是规范写法） |
| 23.1 角色表结构：闭集登记 + 改写规则，键空间互斥，加载期硬校验（3619–3656） | 改写 | 表放 Go 代码，单元测试保证键唯一、目标都在规范角色内；不搬 6496/2312 条数据 |
| 23.2 命名空间路径与修饰级（3658–3707） | 不采用 | 来自外部数据源的写法，本地标签不出现 |
| 23.3 方括号修饰三类语义逐条枚举（3709–3730） | 不采用 | PRD 不做；本地标签少见 |
| 23.4 规范角色分类（3732–3763） | 改写 | 只保留 6 个规范角色 + 细分；视觉、行政类工种不进表，出现在 TIPL 时按兜底进制作人并保留原写法 |
| 23.5 匹配只做 `ToLowerInvariant`（3765–3782） | 改写 | 小写 + 压空白 + 去结尾 ` by`，减少逐条枚举 |
| 23.5 兜底一：方括号截断重查（3786–3804） | 不采用 | 不采用方括号语义 |
| 23.5 兜底二：未知角色保留原始串、每人至少一个角色（3797–3807） | 照搬 | 未知工种/乐器不丢，`detail` 保留原写法 |
| 23.5 词序有意义、不按词集合归一（3809） | 照搬 | 表按字面写法 |
| 23.6 双产出（3811–3822） | 照搬（只用于“词曲”“songwriter”类） | 一人两职 |
| 23.7 可过滤判定与 `role=="Composer"` 缺陷（3824–3849） | 不采用 | ROOMusic 艺人筛选按规范角色，作词、编曲正常可筛 |
| 23.7 去重三元组 人+角色+类型（3851–3857） | 改写 | 曲目署名按 人+角色+细分 去重；艺人图按 人+角色 去重 |
| 23.8 展示分组与专辑类型派发（3864–3898） | 不采用 | PRD 不做 |

## 5. 规则输入输出对照

| 输入 | 署名（role / name / detail / source） |
| --- | --- |
| Vorbis `LYRICIST=方文山` | lyricist / 方文山 / – / tag |
| Vorbis `ARRANGER=林迈可; 黄雨勋` | arranger / 林迈可、黄雨勋 |
| Vorbis `MIXER=某某` | producer / 某某 / mix |
| Vorbis `PERFORMER=Hilary Hahn (violin)` | performer / Hilary Hahn / violin |
| Vorbis `PERFORMER=Yo-Yo Ma (cello, conductor)` | performer / Yo-Yo Ma / cello；conductor / Yo-Yo Ma |
| Vorbis `PERFORMER=Clara Rockmore (theremin)` | performer / Clara Rockmore / theremin（不在表里，原写法） |
| Vorbis `PERFORMER=杨璐（羊鹿鹿）` | performer / 杨璐（羊鹿鹿） / –（括号内是中文，不拆） |
| Vorbis `PERFORMER=Wiener Philharmoniker (orchestra)` | performer / Wiener Philharmoniker / orchestra |
| ID3 `TEXT="方文山"` | lyricist / 方文山 |
| ID3 `TIPL="producer\0周杰伦\0mix\0某某"` | producer / 周杰伦；producer / 某某 / mix |
| ID3 `TIPL="art direction\0某某"` | producer / 某某 / art direction |
| ID3 `TMCL="violin\0A\0theremin\0B"` | performer / A / violin；performer / B / theremin |
| ID3 `TIPL="producer\0A\0mix"` | producer / A（落单的 `mix` 丢弃） |
| Vorbis `WRITER=某某` | lyricist / 某某；composer / 某某 |
| 歌词 `[00:00.00]作词 : 冥凰\n[00:01.00]作曲 : 兰天奇\n[00:02.00]编曲 : 兰天奇/高艺蒙\n[00:03.00]制作人 : 梦帆` | lyricist / 冥凰；composer / 兰天奇；arranger / 兰天奇、高艺蒙；producer / 梦帆（均 source=lyrics） |
| 同上，且标签有 `COMPOSER=周杰伦` | 作曲只保留 tag 的周杰伦；作词、编曲取歌词 |
| 歌词 `词 Lyricist：球球` / `编曲Arranger：李瀚洋` / `Rap作词 : 严艺丹` | lyricist / 球球；arranger / 李瀚洋；lyricist / 严艺丹 |
| 歌词 `词曲:宇桐非` | lyricist / 宇桐非；composer / 宇桐非 |
| 歌词 `编曲/混音：茶语music` | arranger / 茶语music；producer / 茶语music / 混音 |
| 歌词 `监制：某某` | producer / 某某 |
| 歌词 `混音工程师：欧姆`、`录音师：within_轶名` | producer / 欧姆 / 混音工程师；producer / within_轶名 / 录音师 |
| 歌词 `吉他 : 怪兽@五月天`、`Guitar：赵米` | performer / 怪兽 / 吉他；performer / 赵米 / Guitar |
| 歌词 `和声：叶里` | performer / 叶里 / 和声 |
| 歌词 `吉他间奏/录音/制作人：周成` | producer / 周成 / 录音；producer / 周成（吉他间奏不在表里，忽略） |
| 歌词署名块中出现 `协力 : 李汪哲` | 该行不在表里，署名块到此结束，后面的行不读 |
| 歌词有 `制作人：甲`，标签有 `PRODUCER=乙` | 只保留标签的 producer / 乙；歌词其它角色照读 |
| `Title=晴天`，歌词题头 `晴天 - 周杰伦` 后接 `作词：周杰伦` | lyricist / 周杰伦 / – / lyrics |
| 歌词第一行就是正文 `我们的爱…`，后面才有 `作词：…` | 不读（署名块在第一行已结束） |
| 歌词 `[ti:晴天]\n[ar:周杰伦]\n[00:00.00]作词：周杰伦` | lyricist / 周杰伦（元信息标签行跳过） |
| 歌词正文中间的 `合：爱怎么做 怎么错` | 不读（不在开头署名块；且“合”不在表里） |

## 6. 兼容与已有数据

- 迁移只放宽约束、加列，不改写已有行；已有 `track_credits` 的 `detail` 为 NULL、`source='tag'`。
- 新角色与细分要在下一次扫描重新解析后出现（④ 已在 main 时靠 `CommonVersion` +1 触发，见 §7）。
- 人工意图：新增角色只增加事实，已有名字键与意图不变。会变化的名字只有带乐器括号的演奏者（`hilary hahn (violin)` → `hilary hahn`），旧键失去参与后从列表消失，意图不自动搬（同 ⑥ §6.2）。生产库没有带括号的演奏者，人工意图为 0（§10），现有数据上没有会失联的意图。
- 括号别名：`artistgraph/bracket.go` 的排除词增加乐器表全部英文与中文写法，避免 `甲 (piano)`、`乙（钢琴）` 这类曲目艺人生成别名候选；属于候选信号规则，计入 artistgraph 的版本升级。
- 样本库预期变化：样本库没有 `LYRICIST`/`ARRANGER`/`TIPL`/`TMCL`/`PERFORMER` 键；变化全部来自歌词开头署名。134 个带歌词的文件中，开头署名行统计（`/tmp` 只读脚本）：`作曲` 105、`作词` 50、`编曲` 40、`词` 21、`曲` 21、`制作人` 16。生产库（§10，文件与 `music/` 相同）：134 首带歌词，作词行开头 38、含编曲行 38、含制作人或监制行 23；`track_credits` 现在只有 composer 190 条。预期新增作词、编曲各约 38 首曲目的署名，制作人或监制约 23 首；混音、录音、母带、乐器等按署名块规则实际命中（以本机 smoke 结果为准）；作曲只在没有 `COMPOSER`/`©wrt` 的曲目上新增。艺人数会增加（新作词、编曲人），曲目艺人与专辑艺人不变。真实库 smoke 在本机隔离实例上对 `music/` 跑。

## 7. 版本号

常量名按 ④ 定稿（`11-08-scan-incremental/design.md` 3.1、3.2、§8）。

| 版本 | 本项变化 |
| --- | --- |
| `parser.CommonVersion` | 计入 T 线的 +1：新键、TIPL/TMCL/TEXT、`名字 (乐器)`、歌词署名都是共用规则或跨格式改动；不单独改 `formatVersions` |
| `ingest.GroupingVersion` | 本项不需要（署名随观察进入写入，观察变化已让候选重写）；T 线因 ⑥⑧ 合计 +1，一并覆盖 |
| `artistgraph.Version` | O/T 整合后 3→4（新角色、去重、括号排除词） |
| `searchindex.ProjectionVersion` | 计入 T 线的 1→2（credit 角色） |
| `textnorm.Version` | 不变 |
| `CreditRoleTableVersion` | 新常量 1；以后改表时升它，并同时升 `parser.CommonVersion` |

合入顺序：④ 先合入 main → T 线 rebase 后在 main 当时的值上 `CommonVersion` +1、`GroupingVersion` +1（T 线合计一次）；T 线先合入 → 不升，由 ④ 以合入后的规则为基线。`artistgraph.Version`、`ProjectionVersion` 与顺序无关。

## 8. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 歌词正文第一行恰好像“标签：值” | 标签必须命中角色表或乐器表才算署名行；第一个不命中的行即结束署名块 |
| 歌词署名把同一人按工种拆成多条（如同一人编曲、混音、制作） | 照读；曲目行按组显示，艺人页同一人同角色只计一次 |
| 双语名字 `郑宜农 Enno Cheng` 与 `郑宜农` 成两位艺人 | 现有同键规则不认；可由“重复艺人”提示或管理员合并处理，不在本项猜 |
| `artist_tag_evidence` 每轨 100 条上限被大量 TMCL 撑满 | 现有合同：超限 fail closed 记诊断；不为假想规模放宽 |
| 新前端与旧后端或反之 | 前后端同批合入；解码器严格枚举在 PR 内一起改 |

回滚：down 迁移删除新角色数据与新列；代码回退后版本回到旧值，后台重建投影与艺人图。

## 9. 裁决

已裁决（2026-11-08，见 prd.md）：

- 映射不到的原始工种按来源归——TIPL（参与人员表）归制作人，TMCL 与演奏括号归演奏，曲目行显示原写法（§3.2）。
- 歌词开头的其他署名行一起读：按映射表认得的工种都读，制作人、监制归制作人，混音、录音、母带等归到最接近的角色，细分存原写法，曲目行显示原写法（§3.4）。

## 10. 生产库统计

已执行，见 [m8-production-stats.md](../11-08-scan-reference-port/research/m8-production-stats.md)。与本项相关：`track_credits` 只有 composer 190；带括号的演奏者 0；人工意图 0；带歌词的 present 曲目 134（作词行开头 38、含编曲行 38、含制作人或监制行 23）。

## 2026-11-08 合入 main 对齐

O 线与 T 线各自已将 `artistgraph.Version` 提升到 3；整合后统一为 4，避免已有版本 3 的投影漏回填。`searchindex.ProjectionVersion=2`、`textnorm.Version=1` 保持不变。下述线内升级/回滚说明是原实施背景，合并后的回滚须连同 O 线规则判断，不能只回退版本常量。

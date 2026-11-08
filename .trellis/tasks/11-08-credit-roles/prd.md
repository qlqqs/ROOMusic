# ⑦ 署名角色扩充

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 2 项。参考设计 21.5、23.1–23.7 章（只借鉴结构，不搬数据表）。

## 目标

作词、编曲等常见署名能从标签里读出来，在曲目行和艺人页上显示；“名字 (乐器)”这类演奏署名拆成人和乐器，人名不再带着括号。

## 现状（`B/` = `backend/internal/`）

- 角色白名单：`track_credits` 只有 composer、conductor、performer、producer（`backend/migrations/0012:39`）；`artist_credits` 另有 release_artist、album_artist、track_artist（`0026:27-28`）；`artist_tag_evidence` 六项（`0026:90`）；搜索 credit 四项（`0025:80`）。
- 键到角色（`B/library/parser/artist_evidence.go:11-27`）：composer ← `COMPOSER`、`TCOM`、`©wrt`；conductor ← `CONDUCTOR`、`TPE3`；performer ← `PERFORMER`、`PERFORMERS`；producer ← `PRODUCER`。不读 `LYRICIST`、`ARRANGER`、`TEXT`、`TIPL`、`TMCL`。
- `Hilary Hahn (violin)` 整体当名字，可能生成误报的括号别名候选（`B/catalog/internal/artistgraph/bracket.go:15`）。
- 前端：曲目行渲染“角色：甲、乙；角色：丙”（`frontend/src/features/catalog/components/release-media.tsx:86-128`）；角色中文名在 `frontend/src/features/catalog/labels.ts:11-17`、`frontend/src/features/artists/artist-summary.ts:18-26`；艺人页按五个角色分栏（`artist-detail-view.tsx:215-297`）；艺人列表有角色筛选（`artist-filters.tsx:69-80`）。
- 路线图写明“作词/编曲角色需额外数据合同”（`docs/architecture/core-1-roadmap.md:30`）；设计规则“无数据不补编曲/作词等角色”（`frontend/DESIGN-components.md:155`）。

## 需求

- **R1 新角色**：在现有四个署名角色之外增加作词（lyricist）、编曲（arranger）两个角色，数据库白名单、艺人证据、搜索 credit、前端角色名同步扩展。混音、录音、母带、和声、乐器等原始工种不单独成角色：按映射表归到最接近的角色，原写法存进署名的细分（`detail`），曲目行按细分显示（如“混音：某某”），艺人页按角色分栏（design 第 3 节）。
- **R2 读取来源**：
  - Vorbis：`LYRICIST`、`ARRANGER`、`MIXER`、`ENGINEER`、`REMIXER` 等同名键；
  - ID3：`TEXT`（作词），`TIPL`（成对的“角色、人名”），`TMCL`（成对的“乐器、人名”）；
  - M4A：同名 freeform 键。
- **R2b 歌词头部署名**：歌词开头连续的“作词 : 某某”“作曲 : 某某”“编曲 : 某某”“制作人 : 某某”“混音 : 某某”一类行（按 R3 映射表认得的工种都读）（中英文冒号、全半角空白都认，只看开头连续的署名行）读成对应署名，来源单独标记；同一角色标签里已有的优先，不重复。歌词本身照常保存和显示。
- **R3 角色映射表**：自编一张小映射表，把原始角色写法（英文、大小写、常见中文如“作词”“编曲”）映射到规范角色；映射不到的原始工种不丢弃：TIPL（参与人员表）归制作人，TMCL 与演奏括号归演奏，细分保留原写法。表放在代码或数据文件里，带版本号。
- **R4 名字 (乐器)**：`PERFORMER=名字 (乐器)` 与 TMCL 的乐器拆成人名与乐器两部分，人名进艺人图，乐器作为署名的附加信息保存，曲目行署名按乐器显示（如“小提琴：Hilary Hahn”），艺人页仍归在“演奏”角色下；括号别名候选不再把乐器当别名。
- **R5 前端**：曲目行署名显示新角色与乐器；艺人页、艺人列表筛选支持新角色；没有数据的角色不显示（沿用 `DESIGN-components.md:155`）。
- **R6 已有数据**：规则改变后由 `parser.CommonVersion`（④ 已合入时）或下一次全量扫描触发重新解析，艺人图经 `artistgraph.Version` 重整；新增角色不影响已有署名的人工意图。
- **R7 合同同步**：更新 `library-scan-contracts.md`、`artist-contracts.md`、路线图中“作词/编曲需额外数据合同”的说明。

## 验收

- 单测：各容器的新键读取、TIPL/TMCL 成对解析、映射表、`名字 (乐器)` 拆分、括号别名不误报。
- 前端：曲目行、艺人页、筛选显示新角色，截图复核；界面文案按 CLAUDE.md 前端文案规则。
- 真实库只读 smoke：统计各角色署名数量前后变化。

## 不做

- 参考设计的 6496 条角色表、方括号修饰的全部语义、展示分组与专辑类型派发（23.8）。

## 已裁决

- 2026-11-08 用户裁决：新增作词、编曲两个角色（R1）。
- 2026-11-08 用户裁决：乐器显示在曲目行署名里（R4）。
- 2026-11-08 用户裁决：从歌词开头读取作词、作曲、编曲（R2b）。
- 2026-11-08 用户裁决：歌词开头的其他署名行（制作人、监制、混音、录音、母带等）也一起读，按 R3 的映射表归类：制作人、监制归制作人，其余归到最接近的角色并在曲目行显示原写法（R2b）。
- 2026-11-08 主会话裁决（设计审阅）：映射不到的原始工种按来源归：TIPL（参与人员表）归制作人，TMCL 与演奏括号归演奏，曲目行显示原写法（R3）。

## 依赖

T 线第 2 项，在 ⑥ 之后（共用拆分规则）。有前后端接口依赖，先由设计 agent 起草接口合同。

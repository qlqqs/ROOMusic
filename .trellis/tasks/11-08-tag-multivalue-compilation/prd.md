# ⑥ 标签多值与合辑

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 1 项。参考设计 21.2–21.4、21.7、22.10、23.8 章。

## 目标

多位艺人写在一个标签里时拆得对：该拆的拆开，不该拆的不拆，不再把两个名字粘在一起；“群星”“Various Artists”这类合辑写法不再变成一个叫“群星”的艺人；标签用了别的键名写专辑艺人、合辑标志时也能读到。

## 现状（`B/` = `backend/internal/`）

- 拆分规则（`B/catalog/metadata/artist.go`）按顺序：
  1. 删掉所有控制字符（`:22-28`）。换行和 ID3v2.4 多值用的 NUL 都会被删掉，`A\nB` 变成 `AB`。
  2. 明确分隔符 `"; "`、`"；"`、`" / "`、`" feat. "`、`" ft. "`、`" with "`、`"、"`，区分大小写（`:13,32-36`）。
  3. ` & ` 两侧都含空格才拆（`:38-44`）。
  4. 固定组合白名单只有 3 个（`:15-19`）。
  5. 紧凑 `/`、`,`、`&` 依次拆（`:48-57`）：`Bach, Johann Sebastian` 被拆成两个人，`Tom & Jerry` 也会被拆开。
- 曲目艺人在解析阶段就拆（`B/library/parser/artist_evidence.go:67-83`），作曲、指挥、演奏、制作人走同一规则（`B/library/parser/tags.go:105,120-122`）；专辑艺人到艺人图和搜索投影时才拆（`B/catalog/internal/artistgraph/facts.go:32,80`）。
- 样本库网易云格式写成 `成龙,金喜善`、`知更鸟,HOYO-MiX,Chevy`，紧凑逗号必须继续拆。
- 没读的键：`ALBUM_ARTIST`、`COMPILATION`、`TCMP`、`cpil`、`ORIGINALDATE`、`TDOR`、`TXXX`（全仓无命中）。ID3 的 TPE2 只取第一个 NUL 值（`B/library/parser/mp3.go:105-107`）；M4A 的 `----:ALBUMARTIST` 不参与专辑艺人选择（`m4a.go:610`）。
- 合辑：全仓不处理 Various Artists、VA、群星。Release artist 在有明确专辑艺人时对专辑艺人多数决，否则对曲目艺人多数决（`B/catalog/ingest/organizer.go:343-354`）。前端专辑艺人取 `album_artist ?? artist`（`frontend/src/features/catalog/format.ts:17-20`）。
- 拆分规则没有版本号，改规则不会触发存量回填；重扫时每个候选都会调用 `artistgraph.Refresh`（`B/catalog/ingest/persist.go:156`）。名字键绑定人工意图（`artist_name_bindings`、`artist_assignment_overrides`，`0026:33,39`）。

## 需求

- **R1 换行与 NUL 作分隔**：多值标签里的换行（`\n`、`\r\n`）和 NUL 当作明确分隔符处理，再删其余控制字符；TPE1、TPE2 等 ID3 文本帧按 NUL 读出全部值。
- **R2 逗号规则**：紧凑逗号与 `, ` 照拆（`成龙,金喜善`、`周杰伦, 费玉清`）；只有一个逗号、两边都是拉丁字母（含常见变音字母与姓氏前缀 `von`、`de` 等）时当作“姓, 名”整体保留（`Bach, Johann Sebastian`、`Karajan, Herbert von`），适用于所有走拆分规则的字段。
- **R3 合辑写法**：内置一份合辑占位词（至少 `Various Artists`、`Various`、`VA`、`V.A.`、`群星`、`华语群星`、`多位艺人`、`Various Artist`，不区分大小写与全半角），出现在专辑艺人或 Release artist 时，不建艺人实体、不产生署名，该专辑标为合辑；曲目艺人照常。专辑艺人统一显示“群星”，不能点进艺人页；每首曲子的艺人照常显示。
- **R4 合辑标志**：读取 `COMPILATION`（Vorbis）、`TCMP`（ID3）、`cpil`（M4A）；为真时该专辑标为合辑，不让多数曲目艺人当专辑艺人。
- **R5 补读回退键**：专辑艺人增加 `ALBUM_ARTIST`、ID3 `TXXX:ALBUMARTIST` 等变体与 M4A `----:ALBUMARTIST`；原始年份读 `ORIGINALDATE`、`TDOR`；`TXXX` 帧按 ID3 规范解析描述与值。按参考设计 21.4 的“逐级空判断”语义，第一个非空值胜出。
- **R6 已有艺人**：规则改变后，艺人按新拆分重新整理（`artistgraph.Version` 升级触发后台重整；解析阶段拆好的署名名字经 `parser.CommonVersion` 或下一次全量扫描更新）；已被人工合并、改名、指定归属的名字，在新拆分下仍能对上的保持人工意图。对不上的旧名字保留原艺人与意图，失去全部参与后从列表消失，不自动搬到新名字（不猜人，design 6.2）；生产库现在没有人工意图，清单为空。
- **R7 合同同步**：更新 `library-scan-contracts.md` 署名拆分规则与 `artist-contracts.md`。

## 验收

- 单测：R1–R5 的输入输出对照（含样本库的网易云写法、`Bach, Johann Sebastian`、`Tom & Jerry`、换行与 NUL 多值、各合辑写法与标志）。
- 真实库只读 smoke：列出艺人数量与拆分变化最大的名字，逐条确认；合辑专辑不再出现“群星”艺人。

## 不做

- 参考设计的 6496 条角色表、“超过 4 位艺人归为合辑”（违反“不猜人”）、标点全剥的合并键。

## 已裁决

- 2026-11-08 用户裁决：拉丁“姓, 名”不拆，其余逗号照拆（R2）。
- 2026-11-08 用户裁决：合辑的专辑艺人统一显示“群星”（R3）。
- 2026-11-08 用户裁决：曲目艺人本身写成合辑占位词（如合唱曲写“群星”）时照常建艺人；占位词规则只作用于专辑艺人与 Release artist（R3）。
- 2026-11-08 主会话裁决（设计审阅）：原始年份（`ORIGINALDATE`、`TDOR`）只在本版年份缺失时作回退，不单独保存、不显示（参考设计 21.4 的逐级回退语义）。
- 2026-11-08 主会话裁决（设计审阅）：带空格的 ` & ` 照现状拆开，组合名靠固定组合表补；依据是样本库里“甲 & 乙”都是两个人。验收里的 `Tom & Jerry` 按“拆开，除非在固定组合表里”对照。
- 2026-11-08 主会话核实：生产库没有人工意图（绑定、指定、艺人修改都是 0），R6 的“对不上清单”在现有数据上为空，部署前后不需要再到生产库对比（父任务 research/m8-production-stats.md）。

## 依赖

T 线第 1 项。与 ③（O 线）都改 `parser/tags.go`，合入时协调。⑦ 在本项之后。

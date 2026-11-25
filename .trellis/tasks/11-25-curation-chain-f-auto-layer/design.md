# F 自动层规则

## 现状

基线的 `backend/migrations/0058_auto_curation.sql:25` 和 `backend/migrations/0065_track_corrections.sql:28` 定义的有效值视图直接连接自动行。当时 `backend/internal/curation/writer.go` 的 `filterAutoFields` 只核对占用、抑制和意图相等；`backend/internal/catalog/governance/{auto_values,track_auto_values}.go` 的写入只保存意图与操作编号。扫描更新基础表后没有比较步骤。现成的 `TestPostgreSQLCurationRescan` 和 `TestPostgreSQLCurationTrackAutoSurvivesRescan` 均已在修改前运行通过，确认改变标签后仍显示旧自动值。

## 写入

0088 只在两张自动表各增 `writer_level SMALLINT`、`scan_basis JSONB`。依据取基础表对应字段的原始 JSON 值，保留年份、流派顺序及 JSON null。SQL NULL 表示升级前未记录依据；旧行继续展示，直到实际写入有依据的新值。旧程序行按来源认作 4，历史模型行无法从来源还原具体依据，按可能的最高模型级别 3 保护。旧操作前态沿用相同读法。

| writer_level | 后端确定的依据 | 写入方 |
| --- | --- | --- |
| 4 | 程序核对外部源 | 程序匹配后的发行补空、曲目字段、按已有对应回填 |
| 3 | 模型选择了后端核实的外部源 | ModelMatcher 的发行补空，包括采纳匹配例外；E 核实外部源后传此级 |
| 2 | 库内写法或带引用网页 | E、D 在核实相应依据后显式传此级 |
| 1 | 本地文字整理 | 现有 correct_release、correct_track 没有可核实的结构化引用，预览和执行按此级；D 本地整理传此级 |

级别由后端通过 `curation.Source.WriterLevel` 传给 `Apply` 或 `ApplyTrack`。E、D 的模型预览向 `CheckModelCorrection` 或 `CheckModelTrackCorrection` 传相同级别。补空计划携带读取时对应字段的输入，写入事务复核，一旦变化就返回 `item_stale`。自动表写入共用 `Writer.applyFields` 的事务，在目录锁、发行及曲目行锁内逐字段判定。低级别遇到高级别返回 `skipped[field]="lower_priority"`；其余可写字段照常提交。全跳过返回 409 `nothing_to_apply` 及逐字段原因。补空仍遵循 OnlyEmpty；普通替换允许同级或更高级别，相同意图在更高级别实际接管时记录新归属。自动操作的前后态保存写入者、级别及扫描依据，普通撤销逐格恢复。

## 作废与撤销

整理读取发行和在库曲目证据时按字段比较依据。只读证据和缺口分类使用当前适用的自动值。后台程序及模型的独立证据读取将差异交给 Writer，执行受治理操作后重读快照。在目录写锁内重查归属与扫描值，删除过期自动行、递增发行自动版本并刷新搜索及署名投影。有效值视图随删除回到人工值或当前扫描值。原扫描事务只维护扫描数据。

操作目标是发行，曲目操作附曲目标识。作废归当前开启者，来源为 `curation / source / scan`，按来源可以选中；证据读取独立于补全批次。为使迁移严格只增列，数据库沿用已有的 `release_auto_correction`、`track_auto_correction`；作废的展示类型分别为 `release_auto_invalidation`、`track_auto_invalidation`。每条记录一个目标的过期字段，记录删除前后的自动值、有效值和作废时的扫描值。撤销作废时核对字段仍空、扫描值仍匹配，再恢复原值、级别及写入者，并将管理员确认时的扫描值作为新依据。作废与其逆操作均不写“不再提出”；用户另行撤销原值时沿用原抑制规则。

## 摘要

沿用 A 的三种输入。自动占用退出后，缺口摘要反映当前人工、自动与扫描状态：扫描值充足时显示新标签，仍缺时重新补全。发行自动占用参与整份指纹，曲目自动占用只参与缺口摘要。级别、依据与作废候选均不序列化进整份指纹。识别摘要继续使用识别字段本身；仅作废自动值不触发外部重新识别。

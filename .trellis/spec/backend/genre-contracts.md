# 专辑多流派合同（0032）

## 1. 范围与触发

扫描解析归 `library/parser`，共用规则归 `catalog/metadata`，整理与写入归 `catalog/ingest`，有效值与读取归 catalog，修正与撤销归 governance。
修改流派解析、整理、读取、修正、撤销或迁移时读取本合同。它替代旧的单值流派规则；不改身份规划、搜索索引、其他字段多数决或问题核对表。依据：[多流派任务设计](../../tasks/11-07-multi-genre/design.md)，第 10 节裁决优先。

## 2. 接口与存储签名

- 读取：`GET /api/v1/releases/{id}`、`GET /api/v1/release-groups/{id}/comparison` 返回 `genres: []string`。
- 修正：`PATCH /api/v1/releases/{id}/corrections` 保持字段键 `genre`，例如 `{"expected_revision":3,"changes":{"genre":{"mode":"set","value":["Jazz","Pop"]}}}`。
- 存储：`releases.genres TEXT[]`，`release_corrections.list_value TEXT[]`；规则入口为 `SplitGenres(values ...string) []string`、`GenreKey(value string) string`、`chooseGenres(observations []SourceObservation) FieldDecision`。

## 3. 拆分、整理与读写合同

- `metadata.SplitGenres(values ...string) []string` 拆 `;`、`；`、`、`，不拆 `/` 和 `,`；逐项 `BoundedText`、`NormalizeValue`，丢空并按键去重，保留首次写法。
- `GenreKey` 为 `NormalizeValue` → NFKC → Unicode case fold，不使用 textnorm、不合并繁简。
- 重复 GENRE 标签追加；ID3 TCON 按 NUL 取多值；M4A ©gen 拆分；CUE 保留原始字符串，到 scanner 转换时拆分，非空 CUE 列表覆盖内嵌列表。
- `SourceObservation.Genres`、`OrganizedCandidate.Genres` 为数组；`track_observations` 仍每轨一条 `genre` 观察，以 `"; "` 连接多值并沿用来源，读取时经 SplitGenres 拆回；不改其唯一索引。
- `chooseGenres` 每轨每键一票；显示写法取出现次数最多的，同票取字符串较小者。按曲目数降序、显示写法升序，最多 20 项。
- 决定的字段名 `genre`，`value` 为首项、`candidates` 为完整列表，`confidence=high`、`action=auto_apply`、`rule_id=genre_union_v1`、无 reason；无流派不写决定。其他字段仍按原规则报告不一致。
- 重扫无条件重新解析标签；size/mtime 未变只复用音频内容指纹，不复用旧观察。反馈快照用 `to_jsonb(r.genres)`。

### 读取与人工意图

- `releases.genres TEXT[] NOT NULL DEFAULT '{}'`；`release_effective.genres` 无修正取扫描列表，null 修正取空列表，set 修正取 `list_value`。
- 详情、版本对比返回 `genres: []string`，无值为 `[]`，不并存旧 `genre`。SQL 经 `to_jsonb(...)::text` 与 JSON 解码读取数组，不依赖驱动数组扫描。
- 可修正字段键与八字段顺序不变，仍用 `genre`。修正状态的 `scan_value/effective_value` 为非空数组或 null，`uncertain` 恒为 false。
- set 仅接收字符串数组；逐项 NormalizeValue、丢空、按 GenreKey 去重，保留管理员顺序，**不拆数组项里的分隔符**。每项无控制字符、≤4096 字节，去重后 1–20 项。
- 非数组报 `invalid_correction / 流派必须是文本列表`；清理后空报“流派至少填一个；要清空请选择留空”；超限报“流派最多 20 个”。null/clear 语义不变。
- 指纹记录规范化后的完整列表；sameIntent 用列表逐项比较，顺序不同是不同意图。
- 历史操作不迁移。`genreListFrom` 将旧字符串转换成单项列表；新列表保持顺序。撤销修正、撤销认领共用 restoredIntent；修正记录展示统一输出数组或 null。认领详情读取当前修正的 list_value，认领摘要不输出意图值。

### 迁移

0032 改名并转换原列，保持列序；已有非空值转单项列表，空白转空列表。旧不一致决定的候选首项与已存值一致时，按候选原序补全；不做大小写或全半角合并，下次重扫纠正。
流派 uncertain_apply 改为自动采用，旧流派问题随升级消失。流派修正从 text_value 移到 list_value，CHECK 只校验列表形状，逐项文本校验由应用负责。

回退恢复原列名、类型、默认值、可空性、修正 CHECK 与有效视图，扫描和修正均只保留第一项；不恢复旧不一致决定、不改操作记录。降级后旧程序无法撤销新数组记录是已接受风险。

## 4. 校验与错误矩阵

| 输入 | 错误 |
| --- | --- |
| 流派值非字符串数组 | 400 invalid_correction / 流派必须是文本列表 |
| 清理后没有流派 | 400 invalid_correction / 流派至少填一个；要清空请选择留空 |
| 去重后超过 20 项 | 400 invalid_correction / 流派最多 20 个 |
| 单项有控制字符或超 4096 字节 | 400 invalid_correction / 流派含有不允许的字符、流派超过 4096 字节 |

## 5. 正确、基准与错误案例

- 正确：曲目分别提供国语流行乐、台语流行乐，列表包含两项且没有流派不一致问题。
- 基准：没有流派时详情 `genres: []`，修正状态的扫描值和有效值为 null。
- 错误：把 `R&B/Soul` 或 `Folk, World, & Country` 拆成多个流派；或给管理员列表重新按多数决排序。

## 6. 必需测试

非 DB 单测覆盖拆分、标签多值、曲目计票与顺序/上限、其他字段仍报问题、修正校验与指纹、列表比较、旧记录恢复。
DB 门禁由隔离测试覆盖 0032 数据转换、结构往返、流派 set/null/撤销及 API 样本；不得把未跑 DB 视为集成通过。

## 7. 错误与正确写法

错误：保持 `track_observations_current_uq(track_id,field_name)` 却逐项插入 genre，多流派时会触发唯一冲突。
正确：每轨写一条 `strings.Join(genres, "; ")`；读取该流派观察时用 `metadata.SplitGenres(value)` 还原。此合同保留原表结构，回退的旧程序将它当成一个字符串流派。

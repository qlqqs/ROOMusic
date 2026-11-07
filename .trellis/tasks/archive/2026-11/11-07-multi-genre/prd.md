# 专辑多流派

## 目标

一张专辑可以有多个流派；曲目之间流派不同，不再被当成元数据问题。

对应用户反馈 4a（见 [父任务](../11-07-ui-minimal-and-catalog-fixes/prd.md)）：“国语流行乐、台语流行乐”被报“候选值互相不一致”，用户问“就不能一张专辑多个流派吗？”

## 依赖

- 后端可以和 [ui-minimal](../11-07-ui-minimal/prd.md) 并行，前端在 ui-minimal 之后实施。
- 迁移 0032 在重复艺人规则的 0031 之后。
- 接口合同和裁决见 [design.md](./design.md)，执行步骤见 [implement.md](./implement.md)。

## 已确认事实

- **解析**：曲目观察 `SourceObservation.Genre` 是单个字符串，各格式解析器分别写入（`backend/internal/library/parser/tags.go:60`、`mp3.go:125`、`m4a.go:587`、`cue.go`）。多值标签现在怎么处理，留给设计时核对。
- **存储**：
  - 专辑流派存在 `releases.genre TEXT`（迁移 `0011_scan_staging_and_identity.sql:29`）；
  - 管理员修正存在 `release_corrections`，`field='genre'` 时用 `text_value`；
  - 有效值视图在迁移 `0017_governed_operations.sql:82,92`。
- **整理规则**：`backend/internal/catalog/ingest/organizer.go:566` 用 `chooseValue` 取出现最多的值。值多于一个时，决定为 `uncertain_apply` / `inconsistent_candidates`，元数据页上显示为“候选值互相不一致”（`organizer.go:110-111`）。
- **接口**：`backend/internal/catalog/projection.go:80` 是 `Genre *string json:"genre"`；版本比较（`comparison.go`）和修正（`governance/corrections.go`）也读写这个字段。
- **双向迁移合同**：新迁移要附带回退 SQL，回退后旧版本能正常启动。

## 需求

- R1 **专辑流派是一个有序列表**：
  - 取各曲目流派的合集，包含它的曲目多的排前面，一样多时按名字排；
  - 只差大小写或全角半角的写法算同一个流派。
- R2 **多值标签**：一首曲目的流派标签有多个值时（多个 GENRE 字段，或用分号 `;` `；`、顿号 `、` 分隔），每个值都算一个流派。斜杠和逗号不拆，因为 “R&B/Soul”“Folk, World, & Country” 这类本身就是一个流派名。
- R3 **不再报问题**：
  - 曲目之间流派不同，不再产生“候选值互相不一致”；
  - 升级后，现有的流派不一致问题消失；
  - 其他字段（年份、厂牌等）的不一致照旧报。
- R4 **显示**：
  - 专辑详情显示所有流派，例如“国语流行乐 · 台语流行乐”；
  - 版本比较按整个列表比较流派。
- R5 **修改**：
  - 管理员修改专辑流派时可以填多个，可以添加和删除；
  - 全部清空表示没有流派（自定义里把流派删光，就按“留空”保存）；
  - 撤销后恢复之前的列表。
- R6 **迁移**：
  - 升级时，现有的单个流派变成只有一项的列表，现有修正一起转换；
  - 回退时取列表第一项，旧版本能正常启动。
- R7 **接口合同**：读取字段 `genre` 改名为数组 `genres`，没有流派时是 `[]`；修正接口的字段键仍叫 `genre`，值改成数组（见 design.md 第 2 节、第 10 节裁决）。同步更新接口样本和前端解码器。

## 约束

- 测试只补改动直接影响的行为：整理规则、迁移升级和回退、修正与撤销、前端显示和修改；只跑受影响的包和用例，不跑全量。
- DB 测试只用隔离的 `roomusic-test` Compose 项目，不碰开发库 `roomusic-postgres-1`。

## 验收

- [x] A1 扫描一张专辑，曲目流派分别是“国语流行乐”和“台语流行乐”：专辑流派两个都有，元数据页没有这张专辑的流派问题。
- [x] A2 修改流派为多个值后再撤销，前后都正确。
- [x] A3 迁移升级、回退在测试库验证通过，回退后旧版本能启动。
- [x] A4 受影响的 Go 测试和前端测试通过。

## 实施结果（2026-11-07）

- 提交 `f82ee63`：`SplitGenres` 按 `;` `；` `、` 拆分，`GenreKey` 做 NFKC 与大小写折叠；专辑流派取各曲目的合集；迁移 0032 把 `releases.genre` 改成 `genres TEXT[]` 并转换已有修正（`list_value`），回退取第一项；接口改为 `genres` 数组，前端显示“甲 · 乙”，编辑对话框用标签输入。
- A1：整理规则单元测试与集成测试覆盖两首曲目流派不同的专辑，专辑得到两个流派、不再产生“候选值互相不一致”；真实库的效果随下次发布到八号机后可见。
- A2–A4：`TestPostgreSQLMigration0032GenreLists`、修正状态（设两项、留空、撤销）、迁移往返测试与重录样本通过；前端 catalog / governance Vitest 与 `e2e/governance.spec.ts` 通过。

## 不做

- 按流派浏览或筛选专辑。
- 显示曲目自己的流派。

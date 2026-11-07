# 专辑多流派：技术设计与接口合同

需求见 [prd.md](prd.md)。本文是起草稿，“待裁决”一节的各点由主会话决定后再派实现。

## 1. 边界与归属

| 层 | 改动 | 文件 |
| --- | --- | --- |
| 共用规则 | 新增 `SplitGenres`、`GenreKey`（拆分、合并键） | `backend/internal/catalog/metadata/text.go`（新增 `genre.go` 亦可） |
| 解析 | `AudioObservation.Genre string` → `Genres []string`；同名字段追加，不再覆盖 | `library/parser/parser.go`、`tags.go:59`、`mp3.go:124`、`m4a.go:586`、`cue.go`（保持原始字符串，赋值时拆分）、`library/scanner.go:540,558,605` |
| 整理 | `SourceObservation.Genre` → `Genres []string`（JSON `genres`）；流派不再走 `chooseValue`，改用新的 `chooseGenres`；`OrganizedCandidate.Genre` → `Genres` | `catalog/ingest/observation.go`、`organizer.go:186,566,583,874` |
| 写入 | `releases.genres=$13` 直接传 `[]string`；`track_observations` 每个流派一行（`field_name='genre'`） | `catalog/ingest/persist.go:99,181,185`、`scan_feedback_snapshot.go:76` |
| 迁移 | `0032_release_genres.sql` 与 `.down.sql` | `backend/migrations/` |
| 读取 | 详情、版本对比改读 `genres` | `catalog/projection.go:80`、`read.go:79,125-145`、`comparison.go:43,67,99-108`、`httpapi/handlers.go:187` |
| 修正 | 流派修正的值改成列表；撤销、认领、操作描述兼容旧记录里的字符串 | `catalog/fields.go`（`LoadCorrectionIntents`）、`governance/corrections.go`、`revert.go`、`claim.go:225`、`claim_revert.go` |
| 冒烟脚本 | 从库里取流派改成 `array_to_string(r.genres,' / ')` | `scripts/real-library-smoke-runner.sh:591`；`cmd/roomusic-smoke/smoke.go:766` 注释改为“current 保存列表” |
| 前端 | 解码器、发行信息、版本对比、编辑对话框、变更摘要、认领详情 | 见第 6 节 |
| 规范 | 同步字段名与规则 | `governance-contracts.md:32,35`、`catalog-rest-contracts.md:87`、`default-version-contracts.md:79`、`library-scan-contracts.md`（新增流派规则一段）、`frontend/DESIGN-components.md:36` |

不改：身份规划与归组（不读流派）、搜索索引（不含流派）、问题核对表 `release_issue_reviews`（只有 grouping / missing_tracks / source_type / media_type，没有流派核对行）。

## 2. 接口合同

### 2.1 读取：`genre` 改为 `genres`（推荐直接替换，不并存）

前端随后端一起嵌入发布，没有别的客户端，所以不设过渡期。`genres` 总是数组，没有流派时是 `[]`，不再是 `null`。

`GET /api/v1/releases/{id}`（`ReleaseDetail`）与 `GET /api/v1/release-groups/{id}/comparison` 里的每个版本：

```json
{ "genres": ["国语流行乐", "台语流行乐"] }
```

Go：`Genres []string \`json:"genres"\``，读取时 `to_jsonb(releases.genres)::text` 再 `json.Unmarshal`（与 `read.go` 读 `candidates` 的写法一致），保证空列表输出 `[]`。

证据（`GET /api/v1/releases/{id}/evidence`）形状不变：流派那一行 `value` 是第一项，`candidates` 是完整列表，`rule_id` 为 `genre_union_v1`，`action` 恒为 `auto_apply`。

### 2.2 修正：字段键仍叫 `genre`，值改成列表

可修正字段的键、顺序、`corrected_fields`、`release_corrections.field` 都不变（仍是 `genre`），只有值的类型变。这样表约束里的字段枚举、问题与操作记录里的键都不用迁移。

读取 `GET /api/v1/releases/{id}/corrections`，流派一项：

```json
{
  "field": "genre",
  "scan_value": ["国语流行乐", "台语流行乐"],
  "correction": { "mode": "set", "value": ["国语流行乐"] },
  "effective_value": ["国语流行乐"],
  "uncertain": false
}
```

- `scan_value` / `effective_value`：非空数组，或 `null`（没有流派）。与其他字段“没有值时是 null”的约定一致。
- `correction`：`null`（没有修正）、`{"mode":"set","value":[...]}`（至少一项）、`{"mode":"null"}`（明确没有流派）。
- `uncertain` 对流派恒为 `false`。

修改 `PATCH /api/v1/releases/{id}/corrections`：

```json
{ "expected_revision": 3, "changes": { "genre": { "mode": "set", "value": ["国语流行乐", "台语流行乐"] } } }
```

服务端处理：每项 `NormalizeValue`，丢掉空项，按 `GenreKey` 去重（保留第一次出现的写法），**保留管理员给的顺序**（不重新排序，管理员可以把主流派放第一）。比较“有没有变化”（`no_changes`）按整个列表逐项比较。

错误码沿用 400 `invalid_correction`，新增的说明：

| 情况 | message |
| --- | --- |
| `value` 不是字符串数组 | `流派必须是文本列表` |
| 去掉空项后为空 | `流派至少填一个；要清空请选择留空` |
| 超过 20 项 | `流派最多 20 个` |
| 某项含控制字符 / 超过 4096 字节 | 沿用现有的 `流派含有不允许的字符` / `流派超过 4096 字节` |

其余（`clear`、`null`、版本冲突 409 `revision_conflict`、`no_changes`）不变。请求指纹里流派的 `value` 记规范化后的列表。

### 2.3 操作记录与撤销

操作记录 `before_state` / `after_state` 里流派的意图值和 `effective` 从现在起写数组。升级前的记录里是字符串，读取时统一转换，**不改历史记录**：

- 新增 `genreListFrom(value any) ([]string, bool)`：字符串 → 一项列表；`[]any` 全是非空字符串 → 列表；其他无效。
- `restoredIntent`（撤销修正、撤销认领共用）对 `genre` 走它；
- `correctionKind.Describe` 与认领详情输出前把流派值过一遍它，所以前端只会收到数组或 `null`。

`sameIntent` 现在用 `==` 比较 `any`，值变成切片后会 panic，改为按字段类型比较（流派用 `slices.Equal`）。

### 2.4 接口样本

用 `ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' ./scripts/test-integration.sh` 重写样本，预期变化：

- `releases/get*.json`、`release-groups/comparison.json`、`search/artist-correction-*.json`：`genre` → `genres`；
- `corrections/get*.json`、`patched.json`：流派的 `scan_value` / `effective_value` / `correction.value` 变数组；
- `releases/evidence.json`：流派行 `rule_id` 变 `genre_union_v1`。

样本夹具里把一张发行的两首曲目流派设成不同值（如 `Classical` 与 `Baroque`），让 `genres` 有两项、`patched.json` 是两项的修正。前端 `decoders.test.ts` 用这些样本做契约测试，不另写样本。

## 3. 数据与迁移（0032）

0031 留给重复艺人子任务，本迁移编号 0032。原则：`releases.genre` **改名加换类型**而不是删列重建，列位置不变，回退能逐项还原结构快照（快照比较列序号，见 `postgrestest/snapshot.go:33`）。

### 3.1 升级 `0032_release_genres.sql`

```sql
-- 专辑流派改成有序列表（2026-11-07）。releases.genre 改名为 genres 并换成 TEXT[]，列位置不变；
-- 流派修正存进新列 list_value；有效值视图随之重建，列仍与 releases 同名。
DROP VIEW release_effective;

ALTER TABLE releases RENAME COLUMN genre TO genres;
ALTER TABLE releases ALTER COLUMN genres TYPE TEXT[]
    USING CASE WHEN NULLIF(BTRIM(genres), '') IS NULL THEN '{}'::text[] ELSE ARRAY[BTRIM(genres)] END;
ALTER TABLE releases ALTER COLUMN genres SET DEFAULT '{}', ALTER COLUMN genres SET NOT NULL;

-- 扫描当时流派不一致的发行：候选已按曲目数、名字排好，第一项就是现在的流派，直接补成完整列表。
UPDATE releases r SET genres = ARRAY(
        SELECT e.value FROM jsonb_array_elements_text(d.candidates) WITH ORDINALITY AS e(value, n) ORDER BY e.n)
FROM release_field_decisions d
WHERE d.release_id = r.id AND d.field_key = 'genre' AND d.reason = 'inconsistent_candidates'
  AND jsonb_typeof(d.candidates) = 'array' AND d.candidates ->> 0 = r.genres[1];
-- 流派不再算问题：现有的不一致决定改成自动采用，元数据页上的流派问题随之消失。
UPDATE release_field_decisions
SET confidence = 'high', action = 'auto_apply', reason = NULL, rule_id = 'genre_union_v1'
WHERE field_key = 'genre' AND action = 'uncertain_apply';

ALTER TABLE release_corrections ADD COLUMN list_value TEXT[];
ALTER TABLE release_corrections DROP CONSTRAINT release_corrections_value_ck;
UPDATE release_corrections SET list_value = ARRAY[text_value], text_value = NULL WHERE field = 'genre' AND mode = 'set';
ALTER TABLE release_corrections ADD CONSTRAINT release_corrections_value_ck CHECK (
    (mode = 'null' AND text_value IS NULL AND year_value IS NULL AND list_value IS NULL)
    OR (mode = 'set' AND field = 'year' AND text_value IS NULL AND list_value IS NULL AND year_value BETWEEN 1 AND 9999)
    OR (mode = 'set' AND field = 'genre' AND text_value IS NULL AND year_value IS NULL
        AND cardinality(list_value) BETWEEN 1 AND 20 AND array_position(list_value, NULL) IS NULL)
    OR (mode = 'set' AND field NOT IN ('year', 'genre') AND year_value IS NULL AND list_value IS NULL
        AND text_value IS NOT NULL AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
);

CREATE VIEW release_effective AS
SELECT ...                                  -- 其余列与 0017 原文逐字相同
    CASE WHEN c_genre.release_id IS NULL THEN r.genres
         WHEN c_genre.mode = 'null' THEN '{}'::text[]
         ELSE c_genre.list_value END AS genres,
    ...;                                    -- JOIN 部分与 0017 相同
```

单项的长度、控制字符由应用校验，约束只管形状，不在 CHECK 里逐项检查。

### 3.2 回退 `0032_release_genres.down.sql`

```sql
-- 回退：流派列表取第一项，流派修正写回 text_value，结构恢复到 0031。
DROP VIEW release_effective;

ALTER TABLE release_corrections DROP CONSTRAINT release_corrections_value_ck;
UPDATE release_corrections SET text_value = list_value[1] WHERE field = 'genre' AND mode = 'set';
ALTER TABLE release_corrections DROP COLUMN list_value;
ALTER TABLE release_corrections ADD CONSTRAINT release_corrections_value_ck CHECK (
    (mode = 'null' AND text_value IS NULL AND year_value IS NULL)
    OR (mode = 'set' AND field = 'year' AND text_value IS NULL AND year_value BETWEEN 1 AND 9999)
    OR (mode = 'set' AND field <> 'year' AND year_value IS NULL AND text_value IS NOT NULL
        AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
);

ALTER TABLE releases ALTER COLUMN genres DROP NOT NULL, ALTER COLUMN genres DROP DEFAULT;
ALTER TABLE releases ALTER COLUMN genres TYPE TEXT USING genres[1];
ALTER TABLE releases RENAME COLUMN genres TO genre;

CREATE VIEW release_effective AS ...;       -- 0017 原文逐字复制
```

### 3.3 现有数据怎么处理

| 数据 | 升级 | 回退 |
| --- | --- | --- |
| `releases.genre` | 有值 → 一项列表；空或空白 → `{}`；当时不一致的用决定候选补全 | 取第一项；`{}` → `NULL` |
| 流派修正（set） | `text_value` 移到 `list_value` 成一项列表 | 取第一项写回 `text_value` |
| 流派修正（null） | 不变 | 不变 |
| 流派的 `inconsistent_candidates` 决定 | 改成 `auto_apply` / `genre_union_v1`，问题消失 | 不还原：旧版本照常读，下次扫描按旧规则重写 |
| 问题核对记录 | 没有流派核对行，不处理 | 同左 |
| 操作记录 | 不改，读取时把字符串当一项列表（2.3） | 不改，见风险 R3 |
| 正在进行的扫描暂存（`scan_observations`） | 不改；升级时中断的扫描若续跑，旧暂存行的流派丢失，下次扫描补上 | 同左 |

回退往返测试：0032 改了 `release_corrections_value_ck`，`rollbackNeedsFixture` 会要求 `rollbackFixtures[32]`。回退是转换数据而不是删除，带数据的夹具会让“升级→回退”的数据快照对不上，所以登记为空串并注明理由；转换本身由第 7 节的专门用例覆盖。

## 4. 规则

### 4.1 拆分（`metadata.SplitGenres(values ...string) []string`）

- 每个原始值按 `;` 和全角 `；` 拆开；`/`、`、`、`,` 都不拆（“R&B/Soul”是一个流派）。
- 每项 `BoundedText` + `NormalizeValue`，空项丢弃；同一首曲目内按 `GenreKey` 去重，保留第一次的写法。
- 来源：FLAC/Vorbis/APE 每个 `GENRE` 字段都追加（`tags.go` 现在是后写覆盖前写）；ID3 `TCON` 按 v2.4 的 `\x00` 分隔取多个值（复用 `decodeID3ArtistValues` 的拆法）；M4A `©gen`、CUE `REM GENRE` 是单个字符串，只做分号拆分。
- CUE 覆盖规则不变：CUE 里有流派时整张 CUE 的流派列表取代内嵌标签的（`scanner.go:558`、`organizer.go:874` 由“取非空字符串”改为“取非空列表”）。
- 不处理 ID3v1 数字流派 `(17)`：现在也不处理，不在本任务范围。

### 4.2 合并键（`metadata.GenreKey`）

`cases.Fold().String(norm.NFKC.String(NormalizeValue(s)))`：只合并大小写和全半角，正好是 PRD 的要求。不用 `textnorm.Text`，理由见裁决点 3。

### 4.3 专辑流派（`organizer.go` 新增 `chooseGenres`）

1. 对候选的每条观察（文件夹推断的观察没有流派，自然跳过），取它的流派键集合；每个键记“含它的曲目数”和各写法出现次数。
2. 每个键的显示写法取出现最多的，一样多取字符串较小的。
3. 排序：曲目数多的在前；一样多按显示写法的字符串顺序（与 `chooseValue` 的并列规则一致）。
4. 最多保留 20 项（`maxDecisionCandidates`）。
5. 决定：`Field "genre"`、`Value` 第一项、`Candidates` 完整列表、`Confidence "high"`、`Action "auto_apply"`、`RuleID "genre_union_v1"`、`Source` 沿用现在的取法。没有流派时不写决定（与现在一样）。

流派从此不会产生 `inconsistent_candidates`；其他字段仍走 `chooseValue`，不受影响。

## 5. 修正与撤销

| 操作 | 存储 | 有效值 |
| --- | --- | --- |
| 改成多个流派 | `mode='set'`、`list_value` 为规范化后的列表 | 该列表 |
| 全部清空 | `mode='null'`（与其他字段的“留空”相同） | `[]`，接口里 `effective_value: null`、详情 `genres: []` |
| 恢复扫描值 | 删掉修正行（`clear`） | 扫描的 `releases.genres` |
| 撤销 | 按操作记录的前态意图写回（`restoredIntent`），旧记录里的字符串当一项列表 | 撤销前的列表 |

重扫只写 `releases.genres`，从不碰修正行，管理员改过的流派不会被扫描覆盖（现有结构保证）。

## 6. 前端

前端在 ui-minimal 之后实施，沿用它的样式：无衬线字体，主要操作之外的文字按钮无边框，状态标签无边框淡底色，图标按钮无边框。

- **解码器**：`features/catalog/api/decoders.ts:125,228` 改为 `genres: d.array(d.string({ min: 1 }), { max: 20 })`；`features/governance/api/decoders.ts` 的 `fieldValueDecoder` 增加数组分支（`d.array(d.string({ min: 1, max: 4096 }), { min: 1, max: 20 })`），类型变为 `string | number | string[]`。
- **显示**：新增 `formatGenres(genres) = genres.join(" · ")`。`album-header.tsx:26`、`format.ts:87`（`releaseFacts`）用它，空列表不显示这一项。例：“国语流行乐 · 台语流行乐”。
- **版本对比**：`comparison.ts:73` 的流派行 `lines` 返回 `[formatGenres(...)]` 或 `[]`，按整个列表判断是否不同（顺序不同也算不同，与后端一致）。
- **编辑对话框**（`edit-release-dialog.tsx`、`field-draft.ts`）：
  - 三种方式不变：使用扫描值 / 自定义 / 留空。扫描值一行显示“国语流行乐 · 台语流行乐”。
  - 选“自定义”时显示流派标签输入：已填的流派是一排淡底色小标签，每个右侧一个无边框的 × 图标按钮（`aria-label="删除 国语流行乐"`）；后面跟一个输入框，占位文字“添加流派”。回车或输入 `;` `；` 时把输入框里的内容加成一个标签；粘贴“a; b”拆成两个；输入框为空时按退格删掉最后一个；重复的（不分大小写、全半角）不再添加。切到“自定义”时预填扫描值的列表。
  - `FieldDraft` 增加 `list: string[]`（只有流派用），`text` 仍是输入框里还没加成标签的内容；保存时未提交的内容也算一项。
  - 自定义里一个标签都没有时按“留空”提交（`{mode:"null"}`），不报错。校验错误只沿用现有短句：“含有不允许的字符。”“内容太长了。”，新增“最多 20 个。”。
  - “确认扫描值”：扫描值是列表时预填成同样的列表并提交 `set`。
  - 不加任何说明段落。
- **变更摘要与认领详情**：`change-summary.ts` 的 `displayValue`、`claim-detail.tsx` 显示修正值时遇到数组用 `formatGenres`。
- **模拟数据**：`src/test/catalog-data.ts:200,338`、`governance-data.ts:114,127,273`、`claim-data.ts:39`、`e2e/governance.spec.ts:140`（`"Jazz"` → `["Jazz"]`）。`e2e/real/governance.real.ts:168` 用的是 `null` 模式，不用改。

## 7. 测试计划（只测改动直接影响的部分）

后端单元（不需要数据库）：

| 测试 | 位置 | 覆盖 |
| --- | --- | --- |
| `TestSplitGenres` | `catalog/metadata/text_test.go`（新增） | 分号与全角分号拆、斜杠不拆、空项丢弃、大小写全半角去重 |
| 多个 `GENRE` 字段追加 | `library/parser/parser_test.go` 加一例 | `setAudioTag` 两次 GENRE 得两项 |
| `TCON` 多值 | 同上加一例 | `\x00` 分隔得两项 |
| A1 规则 | `catalog/ingest/organizer_test.go` 加一例 | 国语流行乐×2、台语流行乐×1、`Pop`/`ＰＯＰ` 合并 → 顺序正确、决定 `auto_apply`、无 `inconsistent_candidates`；同一候选里厂牌不一致仍是 `uncertain_apply` |
| 修正请求 | `governance/corrections_integration_test.go` 的 `TestParseCorrectionChanges`、`TestCorrectionFingerprintCoversTheCompleteSemantics` 各加流派用例 | 列表规范化去重、空列表 / 非数组 / 超 20 项报错 |
| 旧记录兼容 | `restoredIntent` 加一例 | 字符串 → 一项列表 |

后端集成（隔离 `roomusic-test`，经 `scripts/test-integration.sh`）：

- 新增 `TestPostgreSQLMigration0032GenreLists`（`platform/postgres/`）：在 0031 插入一张流派不一致的发行（含决定与一条流派修正）→ 升级 → 断言 `genres` 补全、决定变 `auto_apply`、修正变 `list_value`；改成两项修正 → 回退 → 断言 `genre` 与 `text_value` 是第一项。
- `TestPostgreSQLMigrationRollbackRoundTrip`：现有用例自动覆盖 0032 的结构往返（登记 `rollbackFixtures[32] = ""`）。
- `TestPostgreSQLCorrectionStateTransitions` 加流派：设两项 → 有效值两项；留空 → `null`；再撤销 → 回到两项（A2）。
- `TestPostgreSQLAPIResponseSamples`：重写样本（2.4），审阅 diff 只含预期变化。

前端（vitest）：`catalog/api/decoders.test.ts`、`governance/api/decoders.test.ts`（样本契约，自动随样本更新）、`format.test.ts`（多流派连接、空列表不显示）、`comparison.test.ts`（列表不同 / 相同）、`field-draft.test.ts`（列表的 set、空列表 → null、未变不提交）、`app/release-corrections.test.tsx` 加一例（加两个标签、删一个、保存的请求体）、`change-summary.test.ts`（把现有流派值改成数组）。Playwright 只跑 `e2e/governance.spec.ts`。

命令：

```bash
cd backend && go test ./internal/catalog/metadata ./internal/library/parser ./internal/catalog/ingest ./internal/catalog/governance \
  -run 'Genre|Organize|ParseCorrection|CorrectionFingerprint|RestoredIntent' -count=1
ROOMUSIC_TEST_RUN='TestPostgreSQLMigration0032GenreLists|TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQLCorrectionStateTransitions|TestPostgreSQLAPIResponseSamples' \
  ./scripts/test-integration.sh
cd frontend && npx vitest run src/features/catalog src/features/governance src/app/release-corrections.test.tsx
cd frontend && npx playwright test e2e/governance.spec.ts
```

不跑全量；“回退后旧版本能启动”由往返测试的结构快照一致来证明，不另做演练。真实服务用例在父任务收尾（X2）统一跑。

## 8. 风险与回滚

- **R1 迁移编号**：0032 依赖 0031 已存在。若本任务先合入，要么等重复艺人子任务的 0031 先合，要么两者对调编号；合入前由主会话确认顺序。
- **R2 回退丢信息**：回退只保留第一个流派，多个流派与多流派修正的其余项丢失；这是降级的预期代价。重新升级后，修正只剩一项，扫描值在下次扫描时恢复。
- **R3 降级后撤销**：旧版本读不懂升级后新写的数组记录，撤销这类操作会失败（报错，不会改坏数据）。降级极少发生，接受；不在回退 SQL 里改写操作记录。
- **R4 升级补全不做合并**：迁移里用旧候选补全时不合并大小写、全半角，可能出现 `Pop`、`pop` 并列，下次扫描该目录即纠正。
- **R5 扫描反馈**：`scan_feedback_snapshot` 的指纹改用 `to_jsonb(r.genres)`，只在同一次扫描内比较，不影响跨版本。
- 回滚方式：代码回滚到上一版本即走 0032 的回退 SQL（执行器先做迁移前备份）。

## 9. 待主会话裁决

1. **读取字段名**：推荐 `genre` 直接改成 `genres`（数组，不为 null），不并存。
   取舍：名字和类型一起变，旧字段不会被误读；没有外部客户端，过渡期没有收益。保留 `genre` 名字只换类型会让单复数对不上。
2. **修正字段键**：推荐仍叫 `genre`，只把值改成数组。
   取舍：表约束枚举、`corrected_fields`、操作记录与前端标签都不用迁移；代价是读取叫 `genres`、修正叫 `genre`，在合同里写明即可。
3. **合并键**：推荐只做 NFKC 加大小写折叠。
   取舍：正好是 PRD 的“大小写、全半角”，扫描路径不依赖需要先加载字表的 `textnorm`。用 `textnorm.Text` 还会合并繁简（“國語流行樂”与“国语流行乐”算一个），更省心，但超出 PRD，而且扫描前必须保证字表已加载。
4. **升级时补全不一致的流派**：推荐用旧决定的候选补成完整列表。
   取舍：升级后不用重扫，A1 这类专辑马上显示两个流派；代价是一条 UPDATE 和 R4。只转一项的话更简单，但用户要等那张专辑的目录重扫才看到多流派。
5. **对话框里清空**：推荐“自定义”里删光标签就按“留空”提交。
   取舍：符合“全部清空表示没有流派”，用户不用再去点“留空”；代价是两条路径结果相同。另一种是删光时报错，提示改选“留空”，多一步操作。
6. **拆分符号**：推荐只拆 `;` 和 `；`。
   取舍：`/` 按 PRD 不拆；`、`、`,` 在流派名里少见，但拆了容易误伤（如 “Rock, Pop” 偶有当一个名字），先不拆，真遇到再加。
7. **数量上限**：推荐每张专辑最多 20 个流派（扫描与修正相同）。
   取舍：与决定候选上限一致，界面不会被撑爆；真实数据几乎达不到。
8. **降级后的撤销（R3）**：推荐接受旧版本撤销新记录会报错。
   取舍：回退 SQL 不碰操作日志，保持记录原样；改写日志里的 JSON 能让旧版本撤销成功，但要在回退里改审计数据，复杂度不值。

## 10. 裁决结果（主会话，2026-11-07）

| 点 | 裁决 | 理由 |
| --- | --- | --- |
| 1 读取字段 | 采纳：`genre` 改成 `genres`，总是数组 | 前端随后端一起发布，没有过渡期的必要 |
| 2 修正字段键 | 采纳：仍叫 `genre`，值改成数组 | 约束枚举、操作记录和前端标签都不用迁移 |
| 3 合并键 | 采纳：只做 NFKC 加大小写折叠 | 正好是 PRD 的要求，扫描不依赖字表 |
| 4 升级补全 | 采纳：用旧决定的候选补成完整列表 | 升级后不用重扫，就能看到多流派 |
| 5 对话框清空 | 采纳：自定义里删光就按“留空”提交 | 少一步操作 |
| 6 拆分符号 | **改**：拆 `;`、`；` 和顿号 `、`；`/` 和 `,` 不拆 | 中文标签常用“、”分隔几个流派，流派名里几乎不会出现顿号；“Folk, World, & Country”“R&B/Soul”本身就是一个流派名 |
| 7 数量上限 | 采纳：每张专辑最多 20 个 | 和决定候选的上限一致 |
| 8 降级后撤销 | 采纳：旧版本撤销新记录会报错，回退 SQL 不改写操作记录 | 降级很少发生，不改审计数据 |
| R4 下次扫描纠正 | 实施时先确认重扫会重新读取没有变化的文件；如果复用已存的观察值，读出时也要做一次 `SplitGenres` | 保证旧值里的分隔符和大小写在重扫后得到纠正 |

迁移编号固定：0031 给重复艺人规则，0032 给本任务；重复艺人规则先提交（见父任务执行计划）。

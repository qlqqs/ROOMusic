# 重复艺人规则 — 技术设计

需求见 [prd.md](./prd.md)；父任务决定 D3、D4 见 [父任务](../11-07-ui-minimal-and-catalog-fixes/prd.md)。

## 1. 边界与归属

| 部分 | 归属 | 改动 |
| --- | --- | --- |
| 信号生成 | `catalog/internal/artistgraph/candidates.go` | 删掉 `same_track_role` 循环（不再读 `artist_tag_evidence`）；新增 `refreshAliasSignals` |
| 括号解析 | 新文件 `artistgraph/bracket.go` | 纯函数 `bracketAliases(normalizedKey)`，规则只写在这里 |
| 读取与反向排除 | `catalog/governance/artist_candidate_ignore.go`（`artistCandidateResolvedSQL`）、`artist_candidates.go` | 两张信号表 UNION 读取；`visible` 中排除同曲同身份的共同署名；理由多两个字段 |
| 迁移 | `backend/migrations/0031_artist_alias_signals.sql` + `.down.sql` | 清理旧信号、收窄 CHECK、新建别名信号表 |
| 投影版本 | `artistgraph.Version` 1 → 2 | 升级后整理一次，为现有曲库补出括号别名信号（见裁决点 1） |
| 前端 | `governance/api/decoders.ts`、`components/artist-candidates-view.tsx` | 解码两个新的可选字段；按 kind 给文案；未知 kind 不显示句子 |

不变：忽略执行器（`CandidateIgnoreExecutor`）、`artist_candidate_ignores` 表、合并/拆开/改名执行器、`album_track_one_to_one` SQL、扫描器写 `artist_tag_evidence`。

## 2. 接口合同

`GET /api/v1/artist-candidates`（含 `ignored=true`）的响应形态不变，只有 `reasons[]` 变化：

- `kind` 取值：删除 `same_track_role`；保留 `album_track_one_to_one`；新增 `bracket_alias`。
- `bracket_alias` 理由没有 `track_id`、`role`，新增两个字段（其他 kind 不出现）：
  - `bracket_side`：`"left" | "right"`，表示 `left_name`、`right_name` 中哪一个是带括号的写法；
  - `alias_part`：`"inner" | "outer"`，另一个名字等于括号里面还是括号外面的部分。
- `release_id` 是带括号写法所在的发行。
- `left_name` / `right_name` 仍跟 `left` / `right` 艺人对应，与现有合同一致。
- `evidence_count` 对 `bracket_alias` 来说是“带括号写法所在发行 × 单独写法所在发行”的组合数。

```json
{
  "left": { "id": "…", "name": "羊鹿鹿", "…": "…" },
  "right": { "id": "…", "name": "杨璐（羊鹿鹿）", "…": "…" },
  "key_pairs": [{ "left_key_id": "…", "right_key_id": "…" }],
  "reasons": [
    { "kind": "bracket_alias", "release_id": "…", "left_name": "羊鹿鹿", "right_name": "杨璐（羊鹿鹿）",
      "bracket_side": "right", "alias_part": "inner" },
    { "kind": "album_track_one_to_one", "release_id": "…", "left_name": "Jay Chou", "right_name": "周杰伦" }
  ],
  "evidence_count": 3
}
```

前端：

- 解码器 `candidateReasonDecoder` 增加 `bracket_side: d.optional(d.oneOf(["left","right"]))` 和 `alias_part: d.optional(d.oneOf(["inner","outer"]))`；`kind` 仍按字符串解码。
- `reasonText` 按 kind 分支（b 是带括号的名字，a 是另一个）：
  - `bracket_alias` + `inner`：`“杨璐（羊鹿鹿）”括号里写着“羊鹿鹿”`
  - `bracket_alias` + `outer`：`“杨璐（羊鹿鹿）”去掉括号就是“杨璐”`
  - `album_track_one_to_one`：保留现有文案；
  - 其他 kind 返回空串，界面不显示这一条理由（现在的写法会把未知 kind 都显示成专辑文案，要改掉）。
- 页面名称“重复艺人”由 sidebar-my-music 子任务负责，本任务只改理由文案。

## 3. 数据与迁移（0031）

现有 `artist_candidate_signals` 按发行组织，唯一约束里没有第二个发行，不适合存放跨专辑的信号，所以新建一张表：

`backend/migrations/0031_artist_alias_signals.sql`

```sql
-- 同曲同身份的多值署名不再当作重复艺人信号；已忽略的名字对保持不动。
DELETE FROM artist_candidate_signals WHERE kind='same_track_role';
ALTER TABLE artist_candidate_signals DROP CONSTRAINT artist_candidate_signals_kind_check,
 ADD CONSTRAINT artist_candidate_signals_kind_check CHECK(kind='album_track_one_to_one');
-- 括号别名天然跨专辑：一行 = 带括号写法所在发行 × 单独写法所在发行；任一发行刷新时两边一起重算。
CREATE TABLE artist_alias_signals (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 left_key_id UUID NOT NULL REFERENCES artist_name_keys(id), right_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 bracket_key_id UUID NOT NULL REFERENCES artist_name_keys(id),
 alias_part TEXT NOT NULL CHECK(alias_part IN ('inner','outer')),
 release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
 alias_release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
 left_name TEXT NOT NULL, right_name TEXT NOT NULL,
 CHECK(left_key_id<right_key_id), CHECK(bracket_key_id IN (left_key_id,right_key_id)),
 UNIQUE(left_key_id,right_key_id,release_id,alias_release_id)
);
CREATE INDEX artist_alias_signals_release_idx ON artist_alias_signals(release_id);
CREATE INDEX artist_alias_signals_alias_release_idx ON artist_alias_signals(alias_release_id);
-- 刷新时只取以“)”结尾的名字键，避免每批都扫全部名字键。
CREATE INDEX artist_name_keys_bracket_idx ON artist_name_keys(normalization_version) WHERE normalized_key LIKE '%)';
```

`backend/migrations/0031_artist_alias_signals.down.sql`

```sql
DROP INDEX artist_name_keys_bracket_idx;
DROP TABLE artist_alias_signals;
ALTER TABLE artist_candidate_signals DROP CONSTRAINT artist_candidate_signals_kind_check,
 ADD CONSTRAINT artist_candidate_signals_kind_check CHECK(kind IN ('same_track_role','album_track_one_to_one'));
```

- CHECK 名是 PostgreSQL 给列约束自动起的 `artist_candidate_signals_kind_check`，实现时在测试库里用 `\d artist_candidate_signals` 确认一次。
- 收窄 CHECK 时 PostgreSQL 会检查现有行，所以清理少了一行就会让迁移失败，不需要单独再写清理测试。
- 只由 `same_track_role` 支撑的候选在升级后自然消失（读取只从信号出发）；同一名字对如果还有别的信号，候选还在，证据数变少。
- 回退：新表的数据随表丢弃；CHECK 放宽回原样，不需要先清理数据。被删的 `same_track_role` 信号不在回退 SQL 里恢复：旧版本启动时看到投影版本是 2、和自己的 1 不一样，会自己整理一遍，把旧信号重新生成（`maintenance.initializeArtists` 版本不一致就重建，两个方向都会触发）。
- 往返测试：CHECK 有变化，`rollbackFixtures[31]` 要有一条；内容写一行 `artist_alias_signals`（回退时随表删掉），用种子键 `a1000000-…-0001` / `-0002` 和一条测试发行。

## 4. 规则与刷新流程

### 4.1 括号解析 `bracketAliases(key string) []alias`

输入是已经规范化的名字键：NFKC 后全角括号都变成半角，大小写已折叠，繁体已转简体，空白已折叠。

1. 必须以 `)` 结尾，并且整串只有一个 `(` 和一个 `)`，`(` 不在开头。所以嵌套括号、多个括号、`(G)I-DLE` 这种开头带括号的都不算。
2. `outer = TrimSpace(key[:open])`，`inner = TrimSpace(key[open+1:len-1])`。两边都不能为空，并且 `outer != inner`。
3. 括号里是下面这些内容时整个名字不产生别名：
   - 合作标记开头：`feat`、`ft.`、`ft `、`featuring`、`with `、`vs`、`x `、`prod`、`cv`（角色名配声优）；
   - 版本或演出标记：拉丁词按整词匹配 `live`、`remix`、`mix`、`edit`、`version`、`ver`、`demo`、`acoustic`、`instrumental`、`inst`、`remaster`、`remastered`、`cover`、`karaoke`、`bonus`；中文按包含匹配 `现场`、`版`、`伴奏`、`演唱会`、`合唱`、`对唱`、`翻唱`、`原唱`、`纯音乐`、`混音`、`演奏`；
   - 只有数字、标点和空白，例如年份、编号；
   - `metadata.CanonicalArtistNames(inner)` 得到的不是正好一个名字，例如 `乙 / 丙`、`乙、丙`。
4. 返回 `{text: outer, part: "outer"}`、`{text: inner, part: "inner"}`；`text` 再过一遍 `textnorm.Text` 后按 `key_hash` 查名字键（这个函数是幂等的）。

排除词表放在 `bracket.go` 里的一个变量中，配一句注释说明；以后要加词只改这里。

### 4.2 信号生成（`refreshCandidates(ids)`，在 CATP 锁内，事务由调用方持有）

1. `DELETE FROM artist_candidate_signals WHERE release_id=ANY(ids)`，接着跑 `album_track_one_to_one` 的 SQL，保持原样。
2. `DELETE FROM artist_alias_signals WHERE release_id=ANY(ids) OR alias_release_id=ANY(ids)`。
3. 取全部带括号的名字键：`SELECT id,normalized_key FROM artist_name_keys WHERE normalization_version=$1 AND normalized_key LIKE '%)'`，在 Go 里逐个解析，按 `key_hash = ANY(...)` 一次查出别名键，得到 `(bracket_key, alias_key, part)` 名字对；查不到的别名直接跳过，两个键相同的也跳过。
4. 一条 SQL 写入：`occ` 是名字对里的键在“有效署名”里出现的发行，以及该发行里这个键最小的写法（曲目级署名要求曲目在库，发行级署名要求发行至少有一首在库曲目）。名字对两端各连一次 `occ`，只保留至少一端的发行在本批 `ids` 里的组合，再 `INSERT … ON CONFLICT DO NOTHING`。

这样，任意一边的发行刷新时，这对发行组合都会删掉重算，不管先扫到哪张专辑都一样；带括号写法和单独写法在同一张专辑时，`release_id = alias_release_id`。

### 4.3 刷新时机

| 事件 | 现有调用 | 括号别名信号 |
| --- | --- | --- |
| 扫描新增或修改发行 | `ingest/persist.go` → `artistgraph.Refresh` | 按本批发行两个方向重算；A 专辑先扫、B 专辑后扫时，B 刷新的反向查找会补上 |
| 曲目缺失 | `ingest.refreshMissingArtists` → `Refresh` | 发行没有在库曲目时那一端没有出现记录，信号不再生成 |
| 发行删除 | — | 外键 `ON DELETE CASCADE` |
| 合并、拆开、改名 | 治理执行器已经刷新受影响的发行 | 名字键不变，候选的艺人归属在读取时解析，不需要另加刷新；改名只写 `artist_overrides.display_name` |
| 升级 | 投影版本 1 → 2 触发整理 | 逐批刷新全部发行，生成现有曲库的括号别名信号 |

### 4.4 读取（`artistCandidateResolvedSQL`）

- `FROM artist_candidate_signals s` 换成两张表的 `UNION ALL` 子查询，统一列：`id, left_key_id, right_key_id, kind, release_id, alias_release_id, track_id, role, left_name, right_name, bracket_key_id, alias_part`。旧表的行补 `NULL`；新表的行 `kind` 固定为 `'bracket_alias'`。
- 作用域：对 `bracket_alias` 来说，发行级覆盖、发行内的曲目级覆盖和“发行至少有一首在库曲目”这几个条件，`release_id` 和 `alias_release_id` 都要检查；把现有的 `= s.release_id` 改成 `= ANY(ARRAY[s.release_id, COALESCE(s.alias_release_id, s.release_id)])`。只要有覆盖就排除这条信号，和现有的保守做法一致。
- 反向排除（R2）：在两个 `visible` CTE（待处理和已忽略）里加同一个片段：

```sql
AND NOT EXISTS (SELECT 1 FROM artist_effective_credits a
 JOIN artist_effective_credits b ON b.track_id=a.track_id AND b.role=a.role
 JOIN tracks t ON t.id=a.track_id AND t.source_status='present'
 WHERE a.artist_id=r.left_artist_id AND b.artist_id=r.right_artist_id)
```

  用的是解析后的艺人（已经算上合并重定向），并且只看曲目级署名（发行级署名的 `track_id` 是 NULL，连不上）。这对艺人在任何一首在库曲目上以同一身份共同署名，就不会出现在列表里，两种规则都一样。不同身份（一个作曲、一个演唱）不受影响。可以走现有的 `artist_effective_search_track_idx(artist_id,track_id) INCLUDE(release_id,role)` 索引。
- 忽略执行器仍用 `resolved` 校验名字对，不加反向排除：已忽略但现在被排除的名字对仍然可以恢复，不会报“范围已变化”。
- 理由 JSON 增加：`'bracket_side', CASE WHEN v.kind='bracket_alias' THEN CASE WHEN (v.bracket_key_id=v.left_key_id)=(v.left_artist_id=v.first_artist) THEN 'left' ELSE 'right' END END` 和 `'alias_part', v.alias_part`；值为 NULL 时由现有的 `jsonb_strip_nulls` 去掉。Go 的 `ArtistCandidateReason` 加 `BracketSide`、`AliasPart *string` 两个字段（`omitempty`）。

## 5. 测试计划（只覆盖改动）

后端（DB 用例走 `./scripts/test-integration.sh`，用 `ROOMUSIC_TEST_RUN` 缩小到下面几项）：

1. `artistgraph/bracket_test.go`（新增，单元测试，表格驱动）：`杨璐（羊鹿鹿）`、`杨璐 (羊鹿鹿)` 得到两个别名；`甲 (feat. 乙)`、`甲 (Live)`、`甲（现场版）`、`甲 (2019)`、`甲 (乙 / 丙)`、`甲(乙)(丙)`、`(G)I-DLE` 都不产生别名。
   `cd backend && go test ./internal/catalog/internal/artistgraph -run TestBracketAliases -count=1`
2. `artistgraph/refresh_review_integration_test.go` 的 `…RawEvidenceAndCandidates`：多个 ARTIST 标签现在断言不产生信号；新增一段跨专辑用例：A 专辑写“杨璐（羊鹿鹿）”，B 专辑写“羊鹿鹿”，只刷新 B → 生成一条 `alias_part='inner'` 信号；B 的曲目标成缺失再刷新 B → 信号消失。
3. `governance/artist_integration_test.go`：
   - 现在靠两个 ARTIST 标签造候选的用例（`CandidateIgnoreRescanAndRestore` 等）改成用括号别名造；703 行直接插 `same_track_role` 的改成 `album_track_one_to_one`；
   - 新增 `TestPostgreSQLArtistCandidatesExcludeSameRoleCoCredit`：同一首曲目的曲目艺人写“杨璐（羊鹿鹿）”和“羊鹿鹿” → 没有候选；把其中一人改到作曲 → 有候选；对应用户的例子（A1）。
4. 迁移往返：`rollbackFixtures[31]`，跑 `TestPostgreSQLMigrationRollbackRoundTrip`。
5. 接口样本：`app/artist_samples_integration_test.go` 的候选曲目从两个 ARTIST 标签改成“Alpha Singer (Beta Singer)” + 另一首 “Beta Singer”，“新名字对”那段改成换括号内容；用 `ROOMUSIC_UPDATE_API_SAMPLES=1` 只重写 `TestPostgreSQLArtistAPIResponseSamples` 的样本。

一条命令：`ROOMUSIC_TEST_RUN='TestPostgreSQLArtistGraphReviewRawEvidence|TestPostgreSQLArtistCandidate|TestPostgreSQLArtistIgnoredCandidates|TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQLArtistAPIResponseSamples' ./scripts/test-integration.sh`（重写样本时再加上 `ROOMUSIC_UPDATE_API_SAMPLES=1`）。

前端：

6. `artist-candidates-view` 的理由文案：inner、outer、专辑规则各一句，未知 kind 不显示；更新 `src/app/artist-governance-step5.test.tsx:356` 的正则。
7. `api-contract.test.ts` 用新样本解码，不改代码。
   `cd frontend && npx vitest run src/features/governance src/app/artist-governance-step5.test.tsx src/test/api-contract.test.ts`

不跑全量；不另写迁移清理的测试（收窄 CHECK 本身就会检查），也不写合并、拆开、改名的刷新测试（这些路径没有改）。

## 6. 风险与回滚

- **成员与组合的误报**：`Jin (BTS)` 会把 Jin 和 BTS 配成一对。只是提示，管理员忽略一次以后，这对名字就不会再出现。暂不按“组合名”排除。
- **同名歧义**：`甲 (乙)` 里的“乙”如果是另一个同名的人，也会提示。只是提示，不会自动合并。
- **刷新开销**：每批刷新都要取一遍带括号的名字键，有部分索引，家用曲库规模下可以忽略。
- **升级整理**：投影版本升到 2 以后，艺人页和候选页在整理期间返回 503“艺人信息正在整理”，播放不受影响。整理时间和一次回填相同。
- **回滚**：执行 0031 的回退 SQL，切回旧镜像；旧版本看到投影版本不一致会自己整理一遍，把 `same_track_role` 信号重新生成。已忽略的记录在两个方向都不动。

## 7. 待主会话裁决

1. **投影版本要不要升到 2**。推荐升：升级后现有曲库马上能看到括号别名提示（A2），回退时旧版本也会自己重新生成旧信号。代价是升级后要整理一次，艺人页暂时不可用。不升的话，只有被重新扫描的发行才会出现新提示。
2. **新建 `artist_alias_signals` 表，还是在旧表加 `alias_release_id` 列**。推荐新建表：不用改旧表的唯一约束（自动生成的约束名被截断，不好写回退），回退只需要删表。代价是读取要 UNION 两张表。
3. **理由里加 `bracket_side`、`alias_part` 两个字段，还是让前端自己找带括号的名字**。推荐加字段：文案能区分“括号里写着”和“去掉括号就是”，繁简写法不同也能判断对。代价是合同多两个可选字段。
4. **反向排除放在读取时、按艺人判断，还是放在生成信号时、按名字键判断**。推荐读取时按艺人：天然跨专辑，合并以后同一位艺人的其他写法也能排除，不用为它另设刷新。代价是列表查询多一个有索引的 `NOT EXISTS`。另外，发行级同一字段的共同署名（专辑艺人写“甲; 乙”）按 PRD 不排除；要排除的话再加一个片段。
5. **括号排除词表**（4.1 第 3 条）是否就用这些，`cv` 要不要排除。推荐按上面的列表，`cv` 排除：角色名和声优不是同一位艺人。
6. **收窄旧表的 CHECK**。推荐收窄：表结构和实际取值一致，迁移本身就能证明旧信号已经清理干净。代价是往返测试要补一条 fixture。

## 8. 裁决结果（主会话，2026-11-07）

| 点 | 裁决 | 理由 |
| --- | --- | --- |
| 1 投影版本 | 采纳：升到 2 | 升级后现有曲库马上能看到括号别名提示；回退时旧版本会自己重建旧信号。代价是升级后艺人页和候选页显示一会儿“正在整理”，可以接受 |
| 2 存储 | 采纳：新建 `artist_alias_signals` | 不动旧表的唯一约束，回退只需删表 |
| 3 理由字段 | 采纳：加 `bracket_side`、`alias_part` | 两种文案能分清；前端对象解码器会忽略多出来的字段，旧解码器不会因此报错 |
| 4 反向排除 | 采纳：读取时按艺人判断，只看曲目级同身份共同署名 | 发行级的共同署名不加排除：现有两条规则都产生不了这种名字对（`album_track_one_to_one` 要求专辑艺人只有一个键），等真有需要再加 |
| 5 排除词表 | 采纳第 4.1 节的词表，排除 `cv` | 角色名配声优不是同一个人 |
| 6 收窄 CHECK | 采纳 | 迁移本身就能证明旧信号已经清理干净 |
| CHECK 约束名 | 实施时先在测试库用 `\d artist_candidate_signals` 确认，再写迁移 | 设计里的名字是推断出来的 |

已知的误报，只作提示，不处理：`Jin (BTS)` 这种“成员（组合）”写法会把成员和组合配成一对，管理员忽略一次以后就不再出现。

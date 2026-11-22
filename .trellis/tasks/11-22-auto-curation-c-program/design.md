# 自动整理 C：技术设计

依据：ADR 0006 第 5–8、10 节；父任务 `design.md` 第六节；现状见 `research/program-curation-current.md`（下文“调研 §n”）。
本文的“松比较”指 `textnorm.Loose(textnorm.Text(x))` 相等：NFKC、大小写折叠、拉丁附加符、假名、繁简转换、去掉标点与符号、折叠空白（`catalog/textnorm/textnorm.go:15`、`:43`），
覆盖全半角、繁简与标点写法的差异。编号的比较用 `textnorm.CatalogNumber`。两者都要求进程已调用 `textnorm.LoadEmbedded`（应用启动时已加载，单元测试里自己加载）。

## 一、包与依赖

| 包 | 职责 | 新增依赖 |
| --- | --- | --- |
| `curation`（已有） | 新增两个受治理写入口：建立外部对应 `ApplyLinks`、强证据归组 `JoinGroup`；`Apply` 增加 `OnlyEmpty`；工作状态与待定项的存取；暂停；闸门的暂停与让路；进度的计数 | 无新包 |
| `catalog`（已有） | 新增只读入口 `catalog.LoadCurationEvidence`（新文件 `curation_evidence.go`）：按发行读匹配与补全需要的全部输入，含可见性与占位的判定 | 无 |
| `curation/program`（新） | 摘要、匹配与把握规则、各项补全、后台循环、批次 | `curation`、`sources`、`operations`、`catalog`（证据入口、`CorrectionIntent` 与字段名）、`catalog/textnorm`、`platform/uuid` |
| `catalog/governance`（已有） | `GroupChangeOperations` 改为接收“不再提出”的写入器：撤销来源为自动整理的归组时写标记 | 无 |
| `sources`（已有） | 把选主封面的规则从 `coverartarchive` 移到 `sources.FrontImage`，适配器与程序层共用；`Release` 补发行状态、介质格式与 disc ID、署名艺人 ID 中缺少的（3.3） | 无 |

- `program` 只面向能力接口（经闸门取得），不导入任何 `sources/<源>` 子包；不导入 `steward`；不导入 `catalog/internal/...`，也不直接读其他能力的私有表
  （`directory-structure.md`），证据一律经 `catalog.LoadCurationEvidence`，工作状态与待定项经 `curation` 的导出函数。`dependency_rules_test.go` 加上这条规则，
  并把 `curation/program` 加进 `app` 允许的依赖。
- `curation/httpapi` 不导入 `program`：后台循环的内存状态经 `curation.ProgramStatus` 接口交给接口层（照 `playback.AnalysisProgressProvider` 的做法）。

## 二、数据模型（迁移 0062）

```sql
CREATE TABLE curation_work_states (
    subject TEXT NOT NULL CHECK (subject IN ('release','artwork','lyrics')),
    target_id UUID NOT NULL,             -- release / artwork 是发行，lyrics 是曲目；不设外键（这些对象不被删除）
    rule_version SMALLINT NOT NULL,
    input_digest TEXT NOT NULL,          -- 十六进制 SHA-256
    status TEXT NOT NULL CHECK (status IN ('matched','uncertain','no_match','try_later','done')),
    attempts INTEGER NOT NULL DEFAULT 0, -- 得出结论的次数（try_later 不计）
    generation INTEGER NOT NULL DEFAULT 0, -- 代次：每次得出结论加一，幂等键由它派生（第四节）
    retry_count INTEGER NOT NULL DEFAULT 0, -- 连续 try_later 的次数，得出结论时清零
    next_attempt_at TIMESTAMPTZ,         -- 只有 try_later 有值
    result JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subject, target_id)
);
CREATE INDEX curation_work_states_progress_idx ON curation_work_states (subject, rule_version, status);
CREATE INDEX curation_work_states_due_idx ON curation_work_states (next_attempt_at) WHERE status = 'try_later';

CREATE TABLE curation_pending_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type TEXT NOT NULL CHECK (target_type = 'release'),
    target_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('release_match','recording_match')),
    status TEXT NOT NULL CHECK (status IN ('waiting_model','in_progress','resolved','waiting_human','shelved')),
    rule_version SMALLINT NOT NULL,
    input_digest TEXT NOT NULL,
    evidence JSONB NOT NULL,
    candidates JSONB NOT NULL DEFAULT '[]',
    conflicts JSONB NOT NULL DEFAULT '[]',
    resolution TEXT CHECK (resolution ~ '^[a-z_]{1,32}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX curation_pending_items_open_idx ON curation_pending_items (target_type, target_id, kind) WHERE status <> 'resolved';
CREATE INDEX curation_pending_items_status_idx ON curation_pending_items (status, created_at);

ALTER TABLE curation_settings ADD COLUMN paused BOOLEAN NOT NULL DEFAULT false;
-- operations_type_ck 增加 'external_link'
```

- 待定项的五种状态一次定全（D 用到后三种）；本任务只写 `waiting_model` 与 `resolved`。
- `result` 的形状（`subject='release'`）：`{mode:"album"|"single", method, external_id, external_group_id, conflicts:[...], group:"linked"|"joined"|"conflict"|"manual"|...}`。
  它只用于进度与排查；`JoinGroup` 的强证据不读它，读的是 `external_link` 操作记录的后态（6.2）。
- 回退（`.down.sql`）按 0061 的顺序：清 `steward_plan_items.operation_id` 对新类型操作及其撤销的引用；删除主题为 `external_link`、`group_join` 的不再提出标记；
  删除 `operation_id` 指向 `external_link` 操作的 `external_links` 行；删两张新表；删撤销记录，再删 `external_link` 操作；`SET CONSTRAINTS ALL IMMEDIATE` 后恢复类型约束；删 `paused` 列。
  有损：程序层的全部状态、待定项与它建的对应。自动整理产生的归组（`release_group_change`）是已有类型，保留。回退夹具登记版本 62：
  `group_join` 标记的 `operation_id` 用 NULL 或 `external_link` 撤销记录的 ID，夹具里不新建 `release_group_change` 操作（回退不删这一类型，快照会对不上）；
  像 61 那样先准备升级前的基础数据块；不改 `auto_revision`。

## 三、匹配

### 3.1 匹配单元与输入

单元是一个可见的发行。`candidate_kind='loose_unknown'` 的发行（根目录的零散单曲，一首曲目）走**单曲方式**，其余走**专辑方式**。

输入只取**人工修正与扫描值**（有修正行用修正，设空的修正就是空），不读自动层：自动层是匹配的产出，不能拿来证明自己。
全部输入经 `catalog.LoadCurationEvidence(ctx, q, releaseIDs)` 一次读出一页发行的：

- 标题、专辑艺人、显示艺人、条码、编号、厂牌、介质类型（`media_type`）；标题是否占位、艺人是否占位（判定照 `artistgraph/facts.go:59`：
  字段决定是 `default_fallback`，或者没有字段决定且值是“未知专辑”“未知艺术家”）。
- **CUE 的 `CATALOG` 是 MCN（条码）**：编号是 12 或 13 位纯数字（或字段来源为 `cue_sheet` 且是纯数字）时当作条码证据，不当编号；全 0 的忽略。
  本地没有条码时它就是条码；与条码不一致时两者都当作条码参与比较（任一等价即可）。
- 介质（位置）与在库的曲目（`source_status='present'`）：碟内位置、碟号、曲号是否推断、显示时长（取整到秒，未知为 0）、
  标签标识（`track_identifiers` 的 `mb_release`、`mb_release_group`、`mb_recording`、`mb_track`、`mb_artist`、`mb_album_artist`、`mb_disc`、`isrc`）与 `tracks.cue_isrc`、
  CUE 字段（`source_kind`、`cue_sheet_path`、父文件、`cue_index_frames`、`cue_end_frames`）、曲目标题与艺人、本地歌词是否存在。
- 指向这个发行、它的曲目、它所在专辑的不再提出标记（主题与哈希）；它们现有的外部对应（MusicBrainz）。
- 八个字段各自有没有修正行、有没有自动值；本地封面是否存在、外部封面行与文件是否存在、外部歌词行是否存在。
- 发行最近一条未被撤销的 `release_group_change` 的来源（4.3 与 6.2 用）。

**信息不足**：标题占位、艺人占位并且没有任何标识（标签 ID、条码、编号、ISRC、可用的 CUE）时，结果是 `no_match`，原因 `insufficient_evidence`，不查询外部，不建待定项。

**松比较的空键不算相等**：任一边规范化后为空串（如标题“?”）时，这一项比较不成立（既不是佐证，也不是相反证据）。

### 3.2 专辑方式的依据顺序

每一步“命中”指外部返回了至少一个候选。命中之后由这一步得出结论（有把握 / 拿不准），**不再往后走**；没有命中才进入下一步。

列表类查询的候选没有曲目表：先按 ID 读取完整发行（`ReleaseByID`）再做核对，每一步至多读取 3 个，候选多于 3 个直接拿不准（`too_many`）。
**列表可能被截断时拿不准**：条码、编号的结果达到 25 条，或 ISRC 结果里任何一个录音的发行数达到 25，记 `list_truncated`（真正的版本可能被截掉）。

| 步 | 方法（`method`） | 条件与候选 | 这一步要求的佐证 |
| --- | --- | --- | --- |
| 1 | `tag_id` | 在库曲目上的 `mb_release` 去重：没有则跳过；多于一个 → 拿不准（`mixed_release_tags`）；一个 → `ReleaseByID`，404 跳过 | 位置对得上（3.3 的子集核对），并且 `count_equal` 或 `durations_ok` |
| 2 | `barcode` | 有条码（含 MCN）→ `ReleasesByBarcode`（多个不同的条码逐个查，候选取并集）；读取后按 3.3 核对 | `count_equal`，并且 `durations_ok`、`title_match`、`artist_match` 至少一项 |
| 3 | `catalog_number` | 有编号 → `ReleasesByCatalog(编号, 厂牌)` | `count_equal`，并且 `label_match` 或 `durations_ok` |
| 4 | `isrc` | 有 ISRC 的曲目（标签与 CUE 合并）里取至多 4 首（首、尾与中间均匀取）逐个 `RecordingsByISRC`；候选 = 各次结果里发行 ID 集合的交集；交集为空 → 拿不准（`isrc_spread`） | `count_equal`，并且 `isrc_agree ≥ 1` |
| 5 | `disc_id` | 每张碟满足 3.2.1 时逐碟查 `ReleasesByDisc`；全部精确命中 → 候选为各碟命中发行的交集；任何一碟是模糊结果 → 拿不准（`disc_fuzzy`，候选只作参考） | `count_equal`，并且 `title_match` 或 `artist_match` |
| 6 | `search` | 标题与艺人都不是占位 → `SearchReleases(标题, 艺人, 总曲目数)`；有结果 → 拿不准（`search_only`）；没有 → `no_match` | 从不判定 |

曲目数不在列表阶段筛选（列表结果的曲目数不可靠，ISRC 嵌入的发行没有介质），一律在读取完整发行之后核对。
核对后一个都不剩时：拿不准，冲突点是剩下最多的那条相反证据（例如 `track_count`）。

声学指纹在子任务 B3 接到第 5、6 步之间。分轨抓取按各轨帧数累加重建目录留到以后的规则版本。

#### 3.2.1 光盘目录

一张碟建目录的条件：这张碟的在库曲目都是 `cue_virtual`，来自同一个 `cue_sheet_path` 与同一个父文件；碟内曲号连续为 1..n 且 n ≤ 99；
每轨有 `cue_index_frames`，末轨有 `cue_end_frames`；偏移严格递增。目录：第 i 轨偏移 = 150 + `cue_index_frames_i`，lead-out = 150 + 末轨 `cue_end_frames`，首轨号 1，末轨号 n。
只在镜像从光盘地址 0 开始时精确；丢了隐藏音轨或偏移不对时得到的是模糊结果，判拿不准，是安全的。任何一张碟不满足条件，这个发行不走第 5 步。

### 3.3 核对（对按 ID 读到的完整发行 M）

本地在库曲目按（碟位置、碟内位置）排好，M 的曲目按（介质位置、曲目位置）排好。本地（碟号、曲号）有重复时直接拿不准（`position_conflict`）。

- **整体核对**（第 2–5 步）：本地总曲目数 = M 的总曲目数；本地只有一张碟时按顺序拉平配对；本地多张碟时要求碟数与每碟曲目数都相等，逐碟按顺序配对。
  **本地有推断的曲号时**，要求每一对都可比较且都在容差内，否则拿不准（`inferred_positions`）：顺序配对没有时长核对就不可靠。
- **子集核对**（第 1 步）：本地曲号都不是推断的时候，允许本地是 M 的子集，按（碟号、曲号）找 M 里同一位置的曲目，每一首都必须找到；
  有推断的曲号时退回整体核对（含上面的要求）。

时长容差：|本地 − 外部| ≤ max(3 秒, 外部时长的 1%)；两边都有时长的一对才可比较。

佐证：
- `count_equal`：本地总曲目数 = M 的总曲目数（并满足上面的碟数要求）。
- `durations_ok`：可比较的对数 ≥ ⌈配对数/2⌉，并且全部在容差内。
- `label_match`：本地厂牌与 M 的任一厂牌名松比较相等。
- `title_match` / `artist_match`：本地标题与 M 的标题、本地专辑艺人（为空时用显示艺人）与 M 的艺人署名全文，松比较相等；占位的一边不参与。
- `isrc_agree`：配对中本地 ISRC 出现在 M 对应曲目的 ISRC 列表里的对数。

相反证据（出现任何一条就拿不准，`conflicts` 记下代码）：

| 代码 | 条件 |
| --- | --- |
| `duration_mismatch` | 任何一对可比较的时长超出容差 |
| `barcode_mismatch` | 本地与 M 都有条码，规范成数字后都不等价（12 位与前导 0 的 13 位视为相同） |
| `catalog_mismatch` | 本地与 M 都有编号，没有一个按 `textnorm.CatalogNumber` 相等 |
| `title_artist_mismatch` | 第 2–4 步：本地标题与艺人都不是占位，并且两者都不松比较相等（音译、翻译的专辑因此多交给模型） |
| `release_tag_mismatch` | 本地有 `mb_release` 标签而 M 的 ID 不在其中（第 1 步本身除外） |
| `group_tag_mismatch` | 本地的 `mb_release_group` 标签唯一，且不等于 M 的外部专辑 ID |
| `artist_tag_mismatch` | 本地的 `mb_album_artist`（没有时用 `mb_artist`）在所有曲目上唯一，且不在 M 的发行署名艺人 ID 里 |
| `disc_tag_mismatch` | 本地有 `mb_disc` 标签，且不在 M 任何介质的 disc ID 里 |
| `recording_tag_mismatch` | 某一对的本地 `mb_recording` 存在且不等于 M 对应曲目的录音 |
| `track_tag_mismatch` | 某一对的本地 `mb_track` 存在且不等于 M 对应曲目的 ID |
| `isrc_mismatch` | 某一对的本地 ISRC 存在、M 对应曲目的 ISRC 列表不空且不含它 |
| `media_count_mismatch` | 本地多于一张碟且与 M 的介质数不同 |
| `media_format_mismatch` | 本地介质类型是 CD（扫描由抓轨日志或 CUE 确定），而 M 没有任何 CD 类介质 |
| `pseudo_release` | M 的状态是 Pseudo-Release（音译或翻译的伪发行） |
| `suppressed` | 这个发行对 M 的外部对应被标记过不再提出 |
| `existing_link` | 这个发行已经对应到另一个外部发行 |

`sources.Release` 目前没有发行状态与介质格式以外的字段时，S1 在中立类型与 MusicBrainz 的投影里补上 `Status`（发行状态）与各介质的 `DiscIDs`、`Format`、署名艺人 ID（已有的不重复加）。

**把握足够** = 这一步核对后恰好剩一个候选通过 + 这一步要求的佐证成立 + 没有相反证据。多个候选都通过 → 拿不准（`ambiguous`）。

### 3.4 单曲方式

| 步 | 方法 | 条件与候选 | 判定 |
| --- | --- | --- | --- |
| 1 | `tag_id` | 曲目的 `mb_recording` 唯一 → `RecordingByID` | 外部时长已知且在容差内，并且没有 `isrc_mismatch` |
| 2 | `isrc` | 有 ISRC → `RecordingsByISRC`；按时长容差筛选 | 恰好一个，并且标题松比较相等（同一个 ISRC 被分给不同歌曲的数据错误真实存在） |
| 3 | `search` | 标题与艺人都不是占位 → `SearchRecordings(标题, 艺人, 时长)` | 从不判定 |

有把握时只建立曲目到录音的对应（不补发行字段、不取封面：单曲出现在很多外部发行里，挑一张是猜）。

### 3.5 规则版本

`program.RuleVersion = 1`。改动任何判定规则、输入的取法或摘要的组成时加一，存量全部重新判定（外部响应大多命中缓存）。
存进状态的版本号是 `RuleVersion × 100 + textnorm.Version`，松比较的规则变化也会触发重判。

## 四、有把握之后

一个发行在一个单元里依次做下面几步；每一步一个受治理的操作，带同一个批次。

**幂等键**：UUIDv5（主题、目标、规则版本、代次、步骤）。单元开始时代次取状态里的 `generation + 1`，得出结论时把它存回。
进程中断后代次不变，重做得到同一个键，`Run` 回放原结果；清除标记、撤销等变化之后代次已经前进，键随之不同，不会回放一条已撤销的操作。
程序把 `nothing_to_apply` 当作这一步已完成；遇到 `idempotency_conflict`（同键不同内容）时代次加一后重试一次。

### 4.1 建立外部对应（`ApplyLinks`，一条记录）

- 专辑方式：发行 → M（后态带 M 的外部专辑 ID）；配对可靠时每一对曲目 → 对应的录音；专辑（本地组）→ M 的外部专辑，条件见 4.3。
- 单曲方式：曲目 → 录音。
- `method` 是第三节的步名，`confidence` 固定为 `certain`（以后 D 写模型判断的对应时用别的值）。
- 已经对应到别的外部 ID 的对象（曲目改挂、专辑已有对应）跳过并记 `occupied`，不替换；所以这条操作从不替换别处写的对应。

### 4.2 补空缺（`Writer.Apply` 带 `OnlyEmpty`，一条记录，只在专辑方式）

字段是空缺 = 没有修正行 + 没有自动值 + 扫描值为空（标题是占位）。`OnlyEmpty` 让 `Apply` 在锁内再核对一次前两条，有了就跳过并记 `occupied`，
避免判断与写入之间被别人（D 上线之后）写入。只补下面这些，其余不动：

| 字段 | 取值 | 不补的情况 |
| --- | --- | --- |
| 标题 | M 的标题 | 扫描标题不是占位 |
| 专辑艺人 | M 的艺人署名全文（名字与连接词按外部的写法拼接） | 署名为空 |
| 年份 | M 的日期的前四位 | 日期为空或年份不合法 |
| 厂牌 | M 的厂牌名去重后恰好一个 | 多个不同的厂牌；只有“[no label]” |
| 编号 | M 的编号去重后恰好一个 | 多个不同的编号；只有“[none]” |
| 条码 | M 的条码 | 为空；本地已有当作条码的 MCN |

来源 `{Kind:"source", Provider:"musicbrainz", Basis:"MusicBrainz 发行 <ID>，依据<步名的中文>"}`。被标记不再提出的值由 `Apply` 自己挡掉。

### 4.3 归组（只在专辑方式）

记 X = M 的外部专辑 ID（没有则跳过），G = 发行所在的本地专辑，H = 已对应到 X 的其他本地专辑（经 `ObjectsLinkedTo`，只算有可见成员的）。

**人做过的结构决定优先**：发行最近一条未被撤销的 `release_group_change` 的来源不是自动整理（REST、对话、管家都算人），就不归组，`result.group="manual"`。

| 情形 | 做什么 |
| --- | --- |
| G 已对应到 X | 什么也不做 |
| H 恰好一个 | `JoinGroup`：把发行归入 H（被标记过不再归入 H 的跳过） |
| H 为空、G 没有对应 | 在 4.1 的那条记录里同时建立 G → X；条件是 G 里其他可见成员凡是已经对上的，都对应到外部专辑 X，否则 `result.group="conflict"`，不建 |
| 其他（H 多于一个；G 对应到别的外部专辑而 H 为空） | 什么也不做，`result.group` 记下原因 |

只做“归入”，不做“移出”：拆开扫描归错的组需要判断，留给模型。

### 4.4 封面（只在专辑方式，单独的工作状态 `artwork`）

缺口 = 发行对应到了外部发行 + 本地没有封面 + 外部封面的行不在或文件不在（`artwork.Exists`）。

1. `Covers({release, M})`，用 `sources.FrontImage` 选主封面；没有时 `Covers({release_group, X})` 再选。
2. 选到的图片若被标记过不再提出（`curation.ExternalIdentitySuppressed`，主题 `artwork`），记 `done`，不下载。
3. `FrontCover(同一目标)`（清单命中缓存，不占限速许可）→ `ApplyArtwork`，`ExternalID` 是图片编号。
4. 两次都选不到：`no_match`。

### 4.5 歌词（单独的工作状态 `lyrics`，按曲目，两种方式都做，不依赖匹配结果）

缺口 = 曲目在库 + 本地没有歌词 + 外部歌词的行不在 + 标题规范化后不空 + 艺人不是占位 + 显示时长 > 0。

1. `LyricsFor({标题, 艺人, 专辑标题（占位时不传）, 时长})`。
2. 精确查询没有，或结果不被接受时，`SearchLyrics({标题, 艺人})`，用同一条规则筛选；按歌词内容（规范化后的哈希）去重后恰好一份才接受，有同步歌词的记录优先。
3. 接受 = 不是纯音乐 + 有同步或纯文本歌词 + 外部时长 > 0 且 |外部 − 本地| ≤ 2 秒 + 标题松比较相等 + 艺人相合
   （松比较相等，或把两边按 `feat.`、`ft.`、`&`、`/`、`、`、`,`、`;`、`×` 切开后第一个名字松比较相等）。
4. 写入经 `ApplyLyrics`（`ExternalID` 是 LRCLIB 的记录编号），本地已有、外部已有、被标记不再提出由它自己挡。
5. 结果：写入或被挡 → `done`；没有可接受的 → `no_match`；多份不同的 → `uncertain`（不建待定项）。

## 五、待定项

- 拿不准、或没对上但信息足够（不是 `insufficient_evidence`）时，为发行建或更新一条待定项：专辑方式 `kind='release_match'`，单曲方式 `recording_match`。
- 内容：`evidence` 是 3.1 的本地证据摘要（标题、艺人、条码、编号、厂牌、碟数与每碟曲目数、时长、标识）；`candidates` 至多 10 个
  `{source, external_id, method, title, artist, date, country, status, track_count, barcode, catalog_numbers, release_group_id}`；`conflicts` 是代码列表。
  外部来的文本原样存放，读取方一律按不可信文本处理（ADR 0005 第 4 节的来源标记在 D 送进模型时加）。
- 同一发行同一种类至多一条未结束的（唯一索引）。已有的处于 `waiting_model` 时更新内容；处于其他未结束状态（D 正在处理或已上交人）时不动。
- 之后判定有把握时，把 `waiting_model`、`shelved` 的那条结束：`status='resolved'`、`resolution='program_matched'`。

## 六、受治理的写入口

### 6.1 `Writer.ApplyLinks`（类型 `external_link`）

```go
type LinkIntent struct{ ObjectType, ObjectID, ExternalID, ExternalGroupID string } // release | release_group | track；ExternalGroupID 只用于发行
type ExternalSummary struct{ Title, Artist, Date, Country string }                  // 展示用，不可信文本，各至多 500 字节
type LinkRequest struct {
    ReleaseID  string
    Links      []LinkIntent // 至少一项；每个对象至多一次
    Source     Source       // Kind 必须是 "source"
    Method, Confidence string
    External   ExternalSummary
    BatchID, Key, RequestID string
}
type LinkApplied struct {
    OperationID string
    Written     []LinkIntent
    Skipped     map[string]string // "<object_type>:<object_id>" → suppressed | unchanged | occupied
}
```

- 事务内顺序与 `Apply` 相同：幂等锁 → 目录写锁与 `LockAutoRelease` → 设置与开启者 → 批次 → 逐项过滤 → 写入 → 记录。
- 每个对象必须属于这个发行（发行本身、它的曲目、它当前所在的专辑），否则 `400 invalid_input`。
- 逐项：被标记不再提出（目标是这个对象，主题 `external_link`，哈希为 `源 NUL 外部 ID`）记 `suppressed`；已经是同一个外部 ID 记 `unchanged`；
  已经对应到别的外部 ID 记 `occupied`；其余 `PutLink`（`operation_id` 为本次）。没有写下任何一项 `409 nothing_to_apply`。
- 记录：目标是发行，版本号用 `auto_revision`（加一）；前态为空，后态是写下的各项（含发行那一项的 `external_group_id`）与 `External`；`origin` 与 `Apply` 相同。
- 指纹：类型、发行、按对象排序的各项、`provider`、`method`；不含 `External`、批次与版本号。
- `Describe`：`changes = {kind:"external_link", source, method, confidence, external:{title, artist, date, country},
  links:[{object_type, object_id, external_id, external_group_id?}]}`；`revertible` = 未被撤销 ∧ 不是撤销 ∧ 每一项现在仍是这条操作写的。
- 撤销：每一项的当前行仍是这条操作写的才整条撤，否则 `409 revert_conflict`（说明里带改写它的操作）。逐项删除，为每个被删的外部 ID 写不再提出标记，`auto_revision` 加一。
  撤销之后摘要随标记变化，后台会重新判定这个发行（得到 `suppressed` 而拿不准，建待定项）。
- `PreviewRevert` 照 B2 的做法给出将被去掉的对应。

### 6.2 `Writer.JoinGroup`（类型沿用 `release_group_change`）

```go
type JoinRequest struct{ ReleaseID, GroupID string; Source Source; BatchID, Key, RequestID string }
```

- `operations.Run` 的 `origin = {source:"curation", provider_kind:"source", provider:"musicbrainz", basis}` 与批次（按来源撤销靠 `provider` 取到它）；指纹不含版本号与批次。
- 执行器顺序：`LockAutoRelease`（目录写锁加发行行锁，写闸可重入）→ 设置、开启者、批次 → 强证据 → 人工结构 → 标记 → 读锁住之后的 `edit_revision`，
  调用 `governance.GroupChangeExecutor` 的归入。核对都在锁之后，与写入原子。
- 强证据（都在数据库里，不由调用方声称）：发行有 MusicBrainz 的发行对应 L；L 的 `operation_id` 那条 `external_link` 记录的后态里，发行那一项的 `external_group_id` = X 不空；
  目标专辑有对 X 的专辑对应。任一不满足 `409 no_strong_evidence`。以后 D 写的对应也带同样的后态。
- 人工结构：发行最近一条未被撤销的 `release_group_change` 的来源不是 `curation` → `409 manual_structure`。
- 被标记过不再归入（目标是发行，主题 `group_join`，哈希为目标专辑 ID）→ `409 nothing_to_apply`。
- 归组写 `edit_revision`（结构操作的撤销沿用现有的版本号规则，ADR 0006 第 5 节）。

### 6.3 归组撤销写标记

`governance.GroupChangeOperations(suppressions GroupSuppressionWriter)`，缺了在装配时失败。撤销成功、原操作 `origin.source="curation"` 且动作是归入时，
写 `group_join` 标记（目标发行、被撤销的目标专辑）。人工的归组撤销不写。`curation.Writer` 实现这个接口；现有测试的调用处传入测试里的空实现。

### 6.4 不再提出标记的列表

`GET /api/v1/admin/curation/suppressions?release_id=` 改为同时列出目标是这个发行的曲目与它所在专辑的标记（`external_link`、`lyrics` 等），否则曲目与专辑上的标记在界面上看不到、清不掉。

## 七、后台循环（`program.Worker`）

### 7.1 状态与调度

内存状态：`disabled`（总开关关或开启者失效）、`paused`、`waiting_for_scan`、`running`、`idle`。装配不了时接口报 `unavailable`。

```text
启动：收掉所有没关的 program 批次
循环：
  读设置 → 不是 active：disabled，等 1 分钟；paused：paused，等 1 分钟
  扫描进行中 → waiting_for_scan，等 30 秒
  第一轮：发行（release 与 artwork 主题）扫一遍
  第二轮：歌词（lyrics 主题）扫一遍
  idle：每分钟比较一次变化标记，变了或距上一轮 6 小时就再从第一轮开始
```

- 歌词单独一轮放在发行之后：15 万首曲目按 LRCLIB 每秒 1 次要四十多个小时，混在一起会把发行的匹配推迟好几天。第二轮进行中，第一轮的变化标记变了就先回第一轮。
- 变化标记 = （最近一次结束的扫描时间、`curation_settings.updated_at`、各源设置的最大 `updated_at`、不再提出标记的条数与最大创建时间、
  `max(operations.created_at)`（取一轮结束之后的值，覆盖人工修正、人工归组、撤销与 D 的写入）、有没有到期的 `try_later`）。比较的是标记，不是逐发行的数据。
- 等待间隔与时钟可注入（测试用）。

### 7.2 扫一遍

- 可见发行按（加入时间倒序、ID 倒序）**键集分页**（不用 OFFSET，扫的过程中新加的发行不会让页面偏移），每页 100 个：一次读出这一页的全部输入，算出摘要，读出工作状态。
- 需要处理 = 没有状态，或规则版本不同，或摘要不同，或 `try_later` 且到期。`release` 看发行的摘要；`artwork` 只在发行已对应、有封面缺口时；`lyrics` 按曲目，只在有歌词缺口时。
- 每个单元开始前读设置、查扫描；关了或暂停就结束这一轮，扫描进行就让路（之后从当前页继续）。
- **闸门也挡暂停与扫描**：`curation.Sources` 的放行检查（每次出站之前）增加两项：暂停 → `409 curation_paused`；扫描进行中（`app` 注入的回调）→ `409 scan_running`。
  所以单元进行到一半时暂停或开始扫描，下一次出站就停下，不用等单元结束。循环把这两个错误当作“停下这个单元，不写状态”。
- 单元内的外部调用都经闸门：源的开关关着（`source_disabled`）→ 跳过与它有关的主题，不写状态；总开关关了（`curation_disabled`）→ 结束这一轮。
- `ErrTryLater` → 这个主题记 `try_later`，`next_attempt_at = now + min(24 小时, 10 分钟 × 2^retry_count)`，`retry_count` 加一，`attempts` 与 `generation` 不变。
- `ErrInvalidQuery`（比如不合格式的 ISRC）→ 当作这一步没有命中。
- 得出结论 → 写状态（`attempts`、`generation` 加一，`retry_count` 清零）。保存的摘要在这个单元的写入完成之后重新读输入计算，避免自己写下的对应与自动值让下一轮再处理一次。
- 数据库错误 → 记日志，等 1 分钟后重新开始这一轮。

### 7.3 摘要

`release` 的摘要覆盖 3.1 列出的发行与曲目输入（含不再提出标记、现有对应、八个字段的修正行与自动值是否存在、最近一次归组的来源）；`artwork` 覆盖（发行对应的外部 ID、外部专辑 ID、
本地封面是否存在、外部封面行与文件是否存在、封面的不再提出标记）；`lyrics` 覆盖（标题、艺人、专辑标题、时长取整、本地歌词与外部歌词是否存在、歌词的不再提出标记）。
用固定顺序的规范化 JSON 做 SHA-256。时长取整到秒，音频分析修正时长的小数部分不会引起重判。

### 7.4 批次

- 第一次需要写入时 `OpenBatch("program")`；写满 500 条记录、开了 1 小时、这一轮结束、遇到关闭或暂停时 `CloseBatch`。没有写入就没有批次。
- 计数在内存里；启动时收掉遗留的批次（`tier='program' AND closed_at IS NULL`）。

### 7.5 装配

- `app` 在有数据库时创建并启动循环，退出时等待它结束；进度接口拿到它的状态。
- 增加装配选项关掉循环（照 `pauseCatalogMaintenance` 的做法）。`app` 包里共用的测试夹具默认关掉它：现有测试会打开总开关，循环一旦工作就会向真实外部服务发请求、
  产生额外的批次与记录。程序层的测试显式打开它，并经装配选项注入三个源的 `BaseURL` 与客户端工厂（`sources.Options`）、循环的时钟与间隔。

## 八、接口

全部要求管理员；写接口先做同源检查。

| 接口 | 变化 |
| --- | --- |
| `GET /api/v1/admin/curation/settings` | 增加 `paused` |
| `PUT /api/v1/admin/curation/settings` | 请求 `{enabled?, paused?}`，至少一项；`enabled` 从关到开时同时把 `paused` 置为 false；暂停不要求总开关开着。Go 侧新增 `SetPaused`，现有 `SetSettings` 签名不变 |
| `GET /api/v1/admin/curation/progress`（新） | `{state, rule_version, releases:{total, processed, matched, uncertain, no_match, try_later}, pending:{waiting_model}, batch:{id, opened_at, operations}\|null, last_pass_finished_at}` |
| `GET /api/v1/admin/curation/suppressions?release_id=` | 见 6.4 |

- `releases` 的各项只统计可见发行；`processed` 是规则版本为当前版本、状态不是 `try_later` 的可见发行数（不核对摘要，所以输入变了之后到重新处理之前仍算已处理）。
- `state` 取后台循环的内存状态；没装配循环时为 `unavailable`。
- 待定项的列表接口不在本任务（E2 与 D 一起定）。

## 九、前端

- `governance` 的解码与展示：`OPERATION_TYPES` 增加 `external_link`；`externalLinkChangesDecoder`；摘要“对上 MusicBrainz：《标题》— 艺人（依据条码）”，
  撤销后“解除与 MusicBrainz 的对应”；不能撤销的原因“对应已被改动”；撤销对话框的标题。
- 来源为自动整理的归组记录沿用现有的归组文案。
- 设置页与进度的界面在 E1。

## 十、兼容与回退

- 没有打开总开关时循环停在 `disabled`，不读证据、不发请求；现有接口样本除自动整理设置多出 `paused` 外逐项不变。
- 关掉总开关即停止全部后台活动；已写入的内容可以按来源 `musicbrainz`、`coverartarchive`、`lrclib` 整体撤销。
- 迁移回退见第二节；本任务合入后可以在此停下（里程碑一只差 E1 的界面）。

## 十一、主要取舍

| 取舍 | 选择 | 理由 |
| --- | --- | --- |
| 怎样发现“输入变了” | 每轮按页重算摘要并与状态比较，变化标记决定什么时候再扫 | 扫描的变动记录不覆盖标签标识、人工修正与不再提出标记；逐发行重算摘要只读数据库，不出站，正确性不依赖信号是否齐全 |
| 拿不准时是否用后面的依据补救 | 不用，命中即定 | ADR 第 6 节“前面的命中就不再用后面的”；组合判定难以证明不会错判。代价：同条码的 CD 与数字版歧义时整轨抓取也到不了光盘目录那一步，交给模型 |
| 一次匹配是一条记录还是每个对应一条 | 一条 | 撤销的单位与用户理解的“这张专辑对错了”一致；记录数少 |
| 是否替换已有的对应 | 从不替换 | 已有对应时判定为 `existing_link` 拿不准；曲目与专辑上已有的记 `occupied`。这样撤销只需删除，原记录永远撤得了 |
| 撤销对应是否连带撤销字段、封面、归组 | 不连带 | 每条自动修改独立可撤（ADR 第 5 节）；按批撤销已能一次撤完，界面上的“撤销这次匹配的全部改动”在 E1 用筛选实现 |
| 强证据从哪里读 | `external_link` 操作记录的后态 | 记录不可变、与对应同时写下；不依赖程序状态的写入时机 |
| 幂等键取自摘要还是代次 | 代次 | 摘要会因为清除标记回到旧值，回放已撤销的操作；代次只增不减 |
| 歌词是否以匹配为前提 | 不以 | 流媒体单曲没有发行可对，但标题、艺人、时长足够让 LRCLIB 精确查询；规则里时长与标题、艺人都要相合 |
| 匹配的输入是否含自动层 | 不含 | 防止自己证明自己；撤销自动值不影响判定 |

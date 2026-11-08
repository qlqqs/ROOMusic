# ④ 增量扫描 · 技术设计

需求见 [prd.md](./prd.md)。S 线第 3 项，以 [①](../11-08-scan-file-filter/design.md)（过滤、保住登记 `HoldSource`/`TouchHeldSources`）与 [⑫](../11-08-root-path-change/design.md)（改路径后 root ID 与相对路径不变）为前提。参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 部分 | 归属 | 新文件 / 表 |
| --- | --- | --- |
| 解析器版本号 | `library/parser` | `parser/version.go`：`CommonVersion`、`formatVersions`、`CacheVersion(ext)` |
| 解析结果缓存（跳过未变文件的解析） | `library` | `library/parse_cache.go`；表 `library_parse_cache` |
| 归组算法版本号 | `catalog/ingest` | `ingest/version.go`：`GroupingVersion` |
| 候选签名的文件系统输入（封面、抓轨日志、CUE sheet 的列举） | `library` | `library/candidate_signature.go` |
| 候选状态存储、未变判断、本轮见到标记 | `catalog/ingest` | `ingest/candidate_state.go`；表 `scan_candidate_states` |
| 移动/改名时借用指纹（R4） | `catalog/ingest` | `identity_plan.go` 的 `resolveFingerprints` |
| 延迟加载的内嵌封面 | `catalog/ingest`（标记与选择）+ `library`（读文件） | `SourceObservation.ArtworkDeferred` |
| 完整重扫入口 | `library` + `library/httpapi` | `scan_runs.scan_mode`，`POST /api/v1/scans` 可选请求体 |

不改：身份规划的匹配、配对、Release 严格过半沿用、missing 对账、扫描反馈的分类规则。

## 2. 数据流

```
StartScan(mode)                                  ┌── library_parse_cache（root_id, 相对路径）
scanRoot:                                        │
  进入目录 d：prefetch(d) ───────────────────────┘ 一次查询取出 d 下的缓存项（≤2000 条，其余逐条查）
  文件 f（通过 ① 的过滤与类型判断、Stat 成功）：
     mode=incremental 且 缓存[f] 的 size、mtime(µs)、parser_version 都等于当前
        → 命中：取缓存的 AudioObservation（无封面字节）→ 记入本目录 seen 批次
     否则 → parser.ParseAudioFile(f) → 成功则写缓存（upsert，seen_scan_id=本次）；失败按 ① 保住
     → buildPhysicalSourceObservation（同现在）+ ParserVersion + ArtworkDeferred → 暂存
  .cue：每次都解析 sheet（便宜，诊断照常产生）；父音频用同一缓存取 AudioObservation
  离开目录时：UPDATE seen_scan_id 批量标记本目录的命中项
  遍历结束（未取消、无 walk_error）：DELETE 本根 seen_scan_id≠本次 的缓存项
PlanStagedRoot：同现在 + 遍历完整时先借用指纹（R4）
persistStagedCandidates：对每个候选
     sig = 候选签名（版本号、候选键、成员、CUE sheet、封面与抓轨日志列举）
     mode=incremental 且 ingest.CandidateUnchanged(sig) → 跳过写入、跳过封面；ingest.TouchTracks(成员曲目)
     否则 → 照常写入 + 封面（ArtworkDeferred 时现读一次该文件取封面）→ 成功后 ingest.RecordCandidateState(sig)
  根结束（遍历完整、未取消）：删除本根本次没有出现的候选状态
finalize：同现在（被 touch 的曲目 observed_at 已更新，不会被标 missing）
```

## 3. 接口合同

### 3.1 解析器版本号（R2）

```go
// parser/version.go
const CommonVersion = 1 // AudioObservation 的结构或共用规则（tags.go 的兜底、分隔、歌词规范化等）变化时加一
var formatVersions = map[string]int{".flac": 1, ".mp3": 1, ".ogg": 1, ".opus": 1, ".wav": 1, ".m4a": 1}
// CacheVersion 返回扩展名对应的缓存版本，如 "c1.f1"；不在表里的扩展名返回 ""（不缓存）。
func CacheVersion(extension string) string
```

升版本规则（写进 `library-scan-contracts.md`）：

- 改了 `AudioObservation` 的字段（增删改名、类型、含义）或共用解析规则（兜底、分隔、多值、歌词、日期、从文件名/目录名推断编号）→ `CommonVersion` 加一，所有格式重新解析。
- 只改某个格式的读取（例如 FLAC 多读一个标签）→ 该格式的版本加一。
- 新增格式（⑩）→ 在表里加一项，起始值 1；不影响其他格式。
- 只改 parser 之外的东西（organizer、写入）→ 不升解析器版本，升归组算法版本。

### 3.2 归组算法版本号（R3）

```go
// ingest/version.go
// GroupingVersion 覆盖“同样的观察会写出什么”：OrganizeObservations、folder_metadata、抓轨日志证据、
// PersistCandidate 的写入（字段决定、归组证据、署名与艺人解析、流派、作品、搜索投影字段）以及封面选择规则。
const GroupingVersion = 1
```

升版本规则：上面任一规则变化导致同样的暂存观察会写出不同的 Release 图，就加一。升版本后的下一次增量扫描，所有候选的签名都变化，全部重写；解析缓存不受影响。

### 3.3 解析结果缓存（R1）

表见 3.9。规则：

- **键**：`(root_id, relative_path)`，相对路径与来源身份相同的规范形式（`ingest.NormalizedPath`）。CUE 虚拟轨不入缓存，只缓存其父音频文件。
- **值**：`parser.AudioObservation` 去掉 `Artwork` 字节后的 JSON（保留 `ArtworkMIME`，非空即表示文件有内嵌封面）；超过 1 MiB 的不缓存。
- **命中条件**：`mode=incremental`，且 `source_size` 相等、`source_modified_at` 截到微秒后相等（与 `staging.go:65-70` 同一截断）、`parser_version = parser.CacheVersion(ext)`。任一不同就重新解析并覆盖。
- **写入**：解析成功后立即 upsert（遍历 goroutine，持锁连接），`seen_scan_id=本次扫描`。解析失败不写、不删（大小或时间没变就不会再走到解析）。
- **读取**：进入一个目录时一次查询 `WHERE root_id=$1 AND directory=$2 LIMIT 2001`；超过 2000 条时该目录剩余文件逐条查。CUE 父音频不在当前目录时逐条查。
- **见到标记**：命中项按目录批量 `UPDATE … SET seen_scan_id=$1 WHERE root_id=$2 AND relative_path=ANY($3)`，在离开目录与根结束时执行。
- **清理**：本根遍历结束、未取消、没有 `walk_error` 时，`DELETE … WHERE root_id=$1 AND seen_scan_id IS DISTINCT FROM $2`。被过滤、被用户排除、已删除、读失败的文件的缓存项都在这里清掉。
- **缓存是可丢的**：读写缓存失败只记 WARN 日志（`library.scan.parse_cache_failed`），按未命中处理，不影响扫描完整性。表被清空等价于下一次全量解析。
- **为什么在解析时写，而不是在候选写入成功后写**：解析结果只取决于文件字节与解析器版本，与候选是否写入成功无关；在解析时写不需要跨 library/catalog 的事务，也让 CUE 父音频与物理曲目共用同一份缓存。PRD“上一次成功写入的观察结果”的目的（不重复解析没变的文件）不变。

### 3.4 延迟加载的内嵌封面

缓存不存封面字节（每首都存会让缓存比音乐元数据大几个数量级）。为此：

- `ingest.SourceObservation` 增加 `ArtworkDeferred bool \`json:"artwork_deferred,omitempty"\``：命中缓存且 `ArtworkMIME` 非空时为 true，`Artwork` 为空。
- `StageObservation` 的“每个 scope 只暂存一份内嵌封面”判断把 deferred 也算作封面：`observation ? 'artwork' OR observation ? 'artwork_deferred'`；已有时清掉后来者的 `Artwork`、`ArtworkMIME`、`ArtworkDeferred`。
- `OrganizedCandidate.EmbeddedArtwork()`（`persist.go:218`）把 `len(Artwork)>0 || ArtworkDeferred` 视为带封面。
- `saveArtworkForRelease`：选中的观察是 deferred 时，校验路径后调用 `parser.ParseAudioFile` 取封面；文件已没有封面或读取失败 → 按“没有内嵌封面”继续找文件夹封面（读取失败另记 `artwork_unreadable`，沿用 ① 的类别）。
- 这只在候选需要重写时发生；候选被跳过时不读任何文件。

### 3.5 未变候选跳过（R3）

**签名**（library 计算，SHA-256 十六进制）。按行拼接后哈希：

```
"g" GroupingVersion
"k" candidate_key
每个成员观察（按 relative_path 字节序）: "m" relative_path \0 source_size \0 mtime(µs, UTC) \0 parser_version
每个成员引用的 CUE sheet（去重排序）:   "s" sheet 相对路径 \0 size \0 mtime(µs)   —— 持久化阶段现 Lstat
listFolderArtworkCandidates(candidate) 返回的每个文件（按函数给出的顺序）:
                                          "a" 相对路径 \0 size \0 mtime(µs)   —— Lstat
candidateSourceDirectories() 的每个目录中 .log 普通文件（按名字排序）:
                                          "l" 目录 \0 文件名 \0 size \0 mtime(µs)
```

- 成员的 `parser_version` 由 library 在暂存时写入新字段 `SourceObservation.ParserVersion`（物理轨取本文件扩展名，CUE 虚拟轨取父文件扩展名）。这样升解析器版本后，即使重新解析出的观察相同，签名也会变化、候选会重写一次。
- **封面输入 = 文件夹封面查找会考虑的全部候选文件**。`library` 提供一个“列出封面候选文件”的函数 `listFolderArtworkCandidates`：按查找规则列出所有可能被选为文件夹封面的文件（不做安全检查与读取，只列名字），`discoverFolderArtwork` 在它的结果上按顺序做安全检查、读取与识别格式，签名对它的结果逐个 `Lstat` 取大小与修改时间。两处只有这一个函数，查找规则以后再改，签名自动跟随。
  - 现状规则下它列出 `ArtworkDirectories()` 各目录里命中现有封面名单的文件。
  - O 线 ② 定稿后的规则（[② design 第 6 节](../11-08-scan-folder-names/design.md)）是四轮的并集：原目录 × 原名单；原目录 × 去扩展名后小写包含 `front`；附属子目录（`scans`、`artwork`、`art`、`covers`，原目录的直接子目录）× 原名单；附属子目录 × 包含 `front`。签名覆盖全部四轮，所以往 `Scans/` 里新放 `box - front.png` 也会让候选重写。
  - 两种合入顺序：**④ 先合入**——④ 按现状规则实现这个共用函数，O 线合入时改它，签名自动覆盖四轮；**O 线先合入**——④ 实现时直接调用 ② 的列举函数（若 ② 没有单独拆出列举函数，④ 实现时从 ② 的查找代码里拆出来，查找与签名共用），不另写一套规则。
- 列举任一目录失败（不存在除外）、`Lstat` sheet 失败 → 不跳过，照常写入。

**未变判断** `ingest.CandidateUnchanged(ctx, db, scanID, rootID, candidateKey, signature) (trackIDs []string, ok bool, err error)`，全部成立才 ok：

1. `scan_candidate_states` 有 `(root_id, candidate_key)` 行且 `signature` 相等；
2. 该候选的全部暂存行（`candidate_key` 相等、`planned`、未暂缓）都满足 `planned_track_id = matched_track_id`（非空）、`NOT content_changed`、`planned_release_id = state.release_id`；
3. 这些曲目都是 `present`，且当前都挂在 `state.release_id` 的 Medium 下。

第 2 条排除了新增文件、重绑定、换内容；第 3 条排除了上次之后被治理操作（认领、人工归组）或别的候选改动过的情况——这些情况照常重写，与现在每次都重写的结果相同。

**跳过时**：`ingest.TouchTracks(ctx, db, trackIDs)`：`UPDATE tracks SET observed_at=NOW() WHERE id=ANY($1) AND source_status='present'`。不写 decisions/evidence/credits、不刷新搜索投影、不处理封面、不读抓轨日志内容；反馈记账没有任何变化（与重写一个没变的候选得到的“零变化”相同）。保存阶段进度照常 `scopeDone`。

**记录状态**：候选写入成功且封面处理成功（含 ① 的 `artwork_unreadable` 不算失败）后，`ingest.RecordCandidateState(ctx, db, rootID, candidateKey, releaseID, signature)` upsert。写入失败只记 WARN，下次重写。完整重扫也记录。

**清理**：本根遍历完整、未取消时，删除 `scan_candidate_states` 中候选键不在本次暂存里的行（`NOT EXISTS scan_observations … candidate_key`，两表都归 catalog/ingest）。

**`scan_candidate_states.release_id` 不加外键**：外键插入会对 `releases` 行取 KEY SHARE 锁，与扫描反馈合同第 3 节的发行/组锁顺序产生新的交叉；这里它只是比对值，Release 不在了第 3 条自然不成立。

### 3.6 移动与改名时借用指纹（R4）

在 `resolveFingerprints` 计算指纹之前、且 `PlanOptions.WalkComplete && Mode=incremental` 时执行一条 SQL：

- **旧侧**：本根 `present`、指纹已知、大小与修改时间已知、没有被任何暂存行 `matched_track_id` 引用、不在 ① 的保住范围内的曲目；取其文件路径 `COALESCE(cue_parent_relative_path, relative_path)`，按 **(文件名, 大小, 修改时间)** 分组。
- **新侧**：本次暂存中保留、未暂缓、`matched_track_id IS NULL`、`content_fingerprint IS NULL`、`fingerprint_path` 非空的观察，按 `fingerprint_path` 的 **(文件名, 大小, 修改时间)** 分组。
- 同一键两侧都**恰好一个不同的文件路径**时，新侧该 `fingerprint_path` 的所有暂存行借用旧侧的指纹（CUE 虚拟轨共用父文件指纹，所以按文件路径而不是按曲目计数）。
- 借用后照常进入配对：配对仍要求指纹（含 CUE 区间）两侧唯一，借来的指纹与原曲目相同，因此原曲目沿用 UUID、更新路径。
- 对不上（多个同名同大小同时间、或文件名变了）的照常计算指纹。`RootPlan` 增加 `FingerprintsBorrowed` 计数，写进 `library.scan.identity_resolved` 日志。

判定条件定稿为“文件名 + 大小 + 修改时间，两侧各唯一”，不用目录名或标签。理由：`mv`、目录改名、`cp -p`/`rsync -a` 都保留修改时间；三者同时相等而内容不同的概率可以忽略；配对本身仍按指纹唯一，借用只是省掉整段读文件。

### 3.7 完整重扫（R5）

**请求**：`POST /api/v1/scans`

- 空请求体（`Content-Length: 0` 或无请求体）等价于 `{"mode":"incremental"}`，保持现有调用方兼容。
- 有请求体时严格解码 `{ "mode": "incremental" | "full" }`，未知字段或其他值 → `400 invalid_input`。
- 已有扫描在跑：返回该扫描与 `200`，不论请求的模式（与现有行为一致）。

**响应**：`ScanDTO` 增加 `mode: "incremental" | "full"`（所有入口、所有角色）。迁移前的历史扫描为 `incremental`（列默认值）。

**语义**：`full` 时跳过解析缓存的读取（仍写入）、跳过候选未变判断（仍记录状态）、不借用指纹；保留 ④ 之前就有的“同来源身份且大小、时间都没变时复用已存指纹”（完整重扫要解决的是标签被改但修改时间没变的情况，与音频指纹无关；重算全部指纹会把 MP3、M4A 整段读一遍）。

**内部**：`Service.StartScan(ctx, StartOptions{Mode string})`；`runScan` 把模式传给 `scanRoot`、`PlanOptions`、`persistStagedCandidates`。

### 3.8 日志

每个根结束记一条 INFO `library.scan.incremental_summary`：`scan_run_id`、`library_root_id`、`mode`、`files_parsed`、`parse_cache_hits`、`parse_cache_writes`、`candidates_written`、`candidates_skipped`、`artwork_deferred_loads`，只含计数。真实库 smoke 以它统计“第二次扫描解析次数为 0”。

### 3.9 迁移（`0036_incremental_scan.sql` + `.down.sql`，合入时重新编号）

```sql
CREATE TABLE library_parse_cache (
    root_id            UUID        NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    relative_path      TEXT        NOT NULL,
    directory          TEXT        NOT NULL,
    source_size        BIGINT      NOT NULL CHECK (source_size >= 0),
    source_modified_at TIMESTAMPTZ NOT NULL,
    parser_version     TEXT        NOT NULL CHECK (parser_version <> ''),
    observation        JSONB       NOT NULL,
    seen_scan_id       UUID,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (root_id, relative_path)
);
CREATE INDEX library_parse_cache_directory_index ON library_parse_cache(root_id, directory);
CREATE TABLE scan_candidate_states (
    root_id       UUID        NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    candidate_key TEXT        NOT NULL,
    release_id    UUID        NOT NULL,
    signature     TEXT        NOT NULL CHECK (signature ~ '^[0-9a-f]{64}$'),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (root_id, candidate_key)
);
ALTER TABLE scan_runs ADD COLUMN scan_mode TEXT NOT NULL DEFAULT 'incremental'
    CHECK (scan_mode IN ('incremental','full'));
```

下行迁移删两表与列。两张表都是可重建的缓存，不需要回填。

### 3.10 扫描反馈口径（R7）

- “读取标签”阶段：`files_processed` 仍按每个候选文件推进一次，不论命中缓存还是解析，计数与百分比口径不变。
- 报告的文件、专辑、曲目计数与变化分类不变：跳过的候选没有变化，重写一个没变的候选也没有变化。
- 唯一可见差别是速度。`scan-feedback-contracts.md` 只需注明“命中缓存的文件同样计入已处理”。

### 3.11 合同同步

- `library-scan-contracts.md`：新增“增量扫描”一节（缓存键与命中条件、两个版本号与升版本规则、候选签名与跳过条件、借用指纹条件、完整重扫语义、延迟封面）；“曲目身份”段补借用指纹；必需测试补对应项。
- `scan-coordination-contracts.md`：`POST /api/v1/scans` 的可选请求体、`ScanDTO.mode`。
- `scan-feedback-contracts.md`：3.10 的一句。
- `core0-runtime-contracts.md`：无新环境变量，不改。

## 4. 前端

- 扫描区段：“开始扫描”旁增加次要按钮“完整重扫”（发 `{"mode":"full"}`），两者共用同一套 202/200 处理。不加说明文字、不加确认框。
- 扫描记录与扫描详情：`mode=full` 时显示标签“完整重扫”。
- `decoders.ts`：`ScanDTO.mode` 必需，只接受两个值；`endpoints.ts`：`startScan(mode?)`。

## 5. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 增量省的是遍历之后的处理，每轮仍逐层列目录（6.3，316–318） | 照搬 | 每次仍完整遍历；省掉的是解析、指纹与写入 |
| 时间戳未变只盖存活标记、不打开文件（6.6，371–372；20-11，2165–2166） | 照搬（改写比较字段） | 比较大小 + 修改时间 + 解析器版本；不用创建时间（Linux 上不可靠） |
| 降精度时间戳比较、整秒与除 10（6.6，352–364、386–394） | 不采用 | ROOMusic 只有一个读 mtime 的入口（`os.Stat`），两侧精度一致；截到微秒即可（gap-analysis §4） |
| 时间戳未变但上轮读取失败 → 重试（6.6，368–369） | 照搬 | 读失败不写缓存，下次自然重读 |
| 路径字节不同但 Unicode 等价 → 补录不算修改（6.6，365–367、396–400） | 不采用 | 来源身份按字节；NFC/NFD 变化在 Linux 上意味着真的改了名，走移动配对（且可借用指纹） |
| 存活标记：单调轮次号判定删除（6.7，402–415；20-1，2134–2135） | 改写 | 曲目用 `observed_at < started_at` 判 missing（已有）；解析缓存用 `seen_scan_id` 清理，同一思路 |
| 首次建立前先灌入已知状态，避免全判新增（4.1，157–159） | 改写 | 状态常驻 PostgreSQL，不需要灌入 |
| 强制重扫：清空待办后全树扫描（4.2，161–163） | 改写 | 完整重扫 = 忽略缓存与候选跳过、仍走同一入口与协调锁，记录里标明 |
| 归组增量跳过：成员、归组标识、附属文件都没变且版本非 0 才整组跳过（12.5，1422–1434） | 照搬（扩充条件） | 成员按大小/时间/解析器版本；附属文件 = 封面与抓轨日志的列举；另加“曲目仍沿用、Release 未被他处改动”，因为 ROOMusic 有治理操作 |
| 归组算法版本常量，递增即全量重算（12.5，1428–1430；20-10，2163–2164） | 照搬 | `ingest.GroupingVersion`；并增加按格式的解析器版本 |
| 确定性标识由成员摘要生成（12.4，1400–1420；20-2，2137–2138） | 不采用 | 与 Release 严格过半沿用冲突（gap-analysis §4）；签名只用来判断“没变”，不当身份 |
| 三点采样摘要（10.2，921–952） | 不采用 | 头块含标签，破坏“身份与标签无关”（PRD 不做） |
| 读取竞争防护：读前后比较修改时间，变了就丢弃结果（10.3，954–972） | 不采用 | 文件在扫描中被改时，缓存记的是读之前 `Stat` 的时间，下次扫描时间不同自然重读；不额外加复检 |
| 内存目录树缓存与换出（15，1814–1869） | 不采用 | 状态在 PostgreSQL（PRD 不做） |
| 内嵌封面提升为独立记录（9.5，843–869） | 改写 | 缓存不存封面字节，只记“有封面”；需要时现读 |

## 6. 兼容与已有数据

- 迁移后第一次扫描：缓存为空、候选状态为空，等价于现在的全量扫描，同时填好缓存与状态；第二次起生效。
- 已有曲目、Release、个人数据不变；跳过只是不重复写相同内容。
- 升解析器版本：受影响格式的文件下一次重新解析，签名变化，相关候选重写。升归组版本：所有候选重写一次。
- ⑫ 改路径后：键以 root ID 与相对路径为准，挂载不改 mtime 时缓存全部命中。
- 其他线（见第 8 节）在 ④ 合入之后改解析或归组规则，必须在同一提交里升对应版本号。
- 容量：每条缓存约等于一首曲目的标签 JSON（含歌词），真实库只有 284 个音频文件，不需要另估（第 9 节）。

## 7. 风险与回滚

| 编号 | 风险 | 应对 |
| --- | --- | --- |
| R-1 | 改了解析或归组规则却忘了升版本，库里留着旧结果 | 合入核对项；管理员可用“完整重扫”补救 |
| R-2 | 某个影响写入的文件系统输入没进签名，导致该变化被跳过 | 签名输入清单写进合同；集成测试覆盖封面替换、抓轨日志新增、CUE 修改 |
| R-3 | 标签被改但修改时间没变（少数标签编辑器保留 mtime） | 与参考设计相同的取舍（宁可滞后一轮）；完整重扫补救 |
| R-4 | 借用指纹配错（同名同大小同时间但内容不同） | 概率可忽略；配对仍需指纹唯一；完整重扫不借用 |
| R-5 | 缓存表变大 | 只存去封面的 JSON，超 1 MiB 不缓存；按 seen_scan_id 清理 |

回滚：代码回滚后两张表无人读写，扫描回到每次全量；`0036` 下行迁移删除两表与 `scan_mode`。

## 8. 线间关系与版本号约定

- 本线前置：① 的 `HoldSource`/`TouchHeldSources`、过滤；⑫ 的 root ID 不变保证。⑤ 在本项之后，把“未命中缓存的解析”和指纹计算放进工作池。
- **其他线如何升版本**（④ 合入 main 之后适用；④ 之前合入的不需要）：

| 线 / 子项 | 改动 | 需要升 |
| --- | --- | --- |
| O ② 文件夹名规则 | organizer、scope、封面名与附属目录 | `GroupingVersion` |
| O ③ 文件名规则 | 不改 parser；文件名推断编号、标题清洗、拆艺人、补位与切分都在 organizer | `GroupingVersion` |
| T ⑥ 标签多值与合辑 | parser 多值与合辑标志 → `CommonVersion`；艺人与合辑写入 | `CommonVersion` + `GroupingVersion` |
| T ⑦ 署名角色 | parser 读 TIPL/TMCL → 对应格式版本或 `CommonVersion`；credits 写入 | 解析器版本 + `GroupingVersion` |
| T ⑧ 古典作品 | parser 读 WORK/MOVEMENT；作品写入 | 解析器版本 + `GroupingVersion` |
| T ⑬ 搜索忽略标点 | 搜索投影回填，不经扫描 | 不升（自带回填） |
| M ⑩ 格式扩展 | 新格式解析；`cue_path.go` 的 `cueAudioExtensions` 追加 `.ape .wv .aif .aiff` | 在 `formatVersions` 加新扩展名，起始 1；若改了共用代码则 `CommonVersion`。CUE 回退扩展名不需要另升版本：CUE 文本每次扫描都重新解析，引用改指新格式文件时候选成员随之变化，候选签名（含成员与 CUE sheet）自然变化（⑩ 设计 3.8 按此对齐） |
| M ⑨ 音频分析与 MP3 时长 | MP3 时长改为读 Xing/VBRI 头（新文件 `parser/mp3_vbr.go`） | `.mp3` 的格式版本加一；分析器自有版本另行处理 |

- 同一条线一起合入 main 的多个子项只升一次：O 线 ② 与 ③ 一起合入时，`GroupingVersion` 合计加 1。
- T 线各行以 T 线设计定稿后的核对为准，暂按上表。
- 共享文件：`library/scanner.go`（遍历回调，① ④ ⑤ 先后改）、`catalog/ingest/{staging,identity_plan,persist,observation}.go`、`library/httpapi/scans.go`、前端扫描区段。`persist.go` 的 `EmbeddedArtwork` 与 O 线无交集；`observation.go`：④ 新增 `ArtworkDeferred`、`ParserVersion`，O 线 ② 新增 `DiscParentExclusive`，三者按字母序合并（`ArtworkDeferred`、`DiscParentExclusive`、`ParserVersion`）；T 线若也加字段同样按字母序。封面查找函数（`library/scanner.go` 的 `discoverFolderArtwork` 一带）与 O 线 ② 共改，见 3.5 的两种合入顺序。

## 9. 缓存容量

不另外估算：真实库只有 284 个音频文件（与本机 `music/` 的文件清单相同），缓存容量不用另外估。

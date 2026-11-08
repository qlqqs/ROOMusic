# ① 文件过滤与失败处理 · 技术设计

需求见 [prd.md](./prd.md)。父任务与复刻原则见 [scan-reference-port](../11-08-scan-reference-port/prd.md)。参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 部分 | 归属 | 说明 |
| --- | --- | --- |
| 路径过滤（内置清单 + 用户排除）、扩展名表、遍历中的失败分类、空根保护、跳过计数 | `library`（新文件 `scan_filter.go`，`scanner.go`、`scan_progress.go` 改调用） | 预计数与遍历调用同一个函数 |
| 排除子文件夹的存储与接口 | `library`（表 `library_root_exclusions`）+ `library/httpapi` | 沿用目录操作的幂等入口 `beginRootOperation` |
| “本轮保住”的来源登记、配对排除、本轮见到标记 | `catalog/ingest`（新文件 `held_sources.go`，表 `scan_held_sources`） | 与 `scan_observations` 同属暂存族，随暂存清理 |
| CUE 引用宽松匹配 | `library/parser`（`cue_path.go`） | 纯函数改动，不碰数据库 |
| 诊断类别名、成功时的跳过提示、排除子文件夹界面 | 前端 `features/library` | 文案按 CLAUDE.md 前端文案规则 |

不改：扫描协调锁、终态事务结构、身份规划的匹配与 Release 分配规则（只给“本轮消失”集合加一个排除条件）。

## 2. 数据流

```
loadRegisteredRoots ──(同一快照)──> 每个根: { path, exclusions[] }
        │
        ├─ countScanRoots: WalkDir + scanFilter（与遍历同一函数）
        │
        └─ scanRoot: WalkDir
             目录项: scanFilter.skipDir(rel, name) ──命中──> SkipDir（整棵子树，不记诊断）
             目录读不了(walkErr 且是子目录) ──> 诊断 walk_error + HoldSource(directory, rel) + skipped++
             文件项: scanFilter.skipFile(name) ──命中──> 忽略（不记诊断）
                     扩展名不是候选也不是 .cue ──> 忽略（先于链接解析，R2）
                     seen++（候选计数，供空根保护）
                     链接断/越界、Stat 失败、非普通文件、不支持格式、解析失败
                         ──> 诊断（原类别）+ HoldSource(file, rel) + skipped++，root 仍完整
                     .cue: 语法/编码失败、unsafe/unchecked 引用、父音频解析失败
                         ──> 诊断 + HoldSource(cue_sheet, rel) + skipped++，root 仍完整
             遍历结束:
               seen==0 且无 walk_error 且库中该根还有 present 曲目 ──> 诊断 root_empty，root 不完整，不规划不写入
               否则照常: AlignCue → PlanStagedRoot（vanished 排除 held）→ 写候选
               ingest.TouchHeldSources: held 命中的 present 曲目 observed_at=NOW()
        finalize: succeeded 才 missing 对账（被 touch 的曲目不会被标 missing）
```

关键点：

- **“本轮见到”用 `observed_at` 实现**。missing 对账条件是 `observed_at < scan_runs.started_at`（`backend/internal/catalog/ingest/persist.go:283-290`），所以保住一条曲目只需把它的 `observed_at` 更新为当前时间。扫描反馈合同已规定观察时间变化不算元数据更新（`scan-feedback-contracts.md` 第 3 节），报告不受影响，也不需要刷新搜索投影。
- **保住的曲目不参与移动配对**：`PlanStagedRoot` 的 `vanishedTracksCTE`（`identity_plan.go`）增加 `NOT EXISTS scan_held_sources` 条件。否则一首解析失败的旧曲目会被当成“本轮消失”，被同指纹的别处文件抢走 UUID。
- **目录读取失败只保住该目录**：`filepath.WalkDir` 在 `ReadDir` 出错时用同一路径第二次回调并带 `walkErr`，已读出的部分条目照常继续遍历；我们登记 `directory` 前缀，回调返回 `nil`（WalkDir 随后跳过该目录剩余部分）。根路径本身读不了仍按 R6 判为不完整（`root_unavailable` 路径不变）。
- **指纹计算失败保持现状**（`fingerprint_failure`，scope 暂缓，root 不完整）。它发生在规划阶段，scope 已经整理并分配了候选键，此时抽掉一个观察会让写入阶段重新整理出的候选与规划不一致。它只在文件能解析、却不能完整读出音频时出现，属罕见的 IO 故障；放进 R6 一类。

## 3. 接口合同

### 3.1 路径过滤规则（`library.scanFilter`）

构造：`newScanFilter(exclusions []string)`，在 `loadRegisteredRoots` 读到根时一次性构造（参考设计 14.1 “规则预归一化”）。判断只看**根之后**的相对路径段，注册根自身路径的各段不参与。

| 规则 | 判据 | 作用 |
| --- | --- | --- |
| 隐藏项 | 段名以 `.` 开头 | 目录跳过整棵子树；文件跳过。覆盖 `._x.flac`、`.Trash-1000`、`.DS_Store`、`.@__thumb`、`.Recycle.Bin` 等 |
| 内置名单 | 段名按 `strings.EqualFold` 等于其一：`@eaDir`、`#recycle`、`@Recycle`、`@Recently-Snapshot`、`#snapshot`、`$RECYCLE.BIN`、`System Volume Information`、`lost+found`、`__MACOSX`、`@appmeta`、`@team`（后两个是 fnOS 卷一级的系统目录，第 9 节已核实） | 只作用于目录段：目录跳过整棵子树 |
| 用户排除 | 目录相对路径（`/` 分隔）按字节等于某个排除项 | 目录跳过整棵子树 |

- 整段相等比较，不做子串匹配：`my.recycle`、`recycle`、`Music@eaDir2` 都不命中。
- 命中不记诊断、不计入跳过数、不计入预计数。
- 函数签名：`skipDir(relativeDir, name string) bool`、`skipFile(name string) bool`；`countScanRoots` 与 `scanRoot` 只调用这两个函数。

### 3.2 扩展名与类型判定（R2）

扩展名表（`isSupportedAudioExtension`、`isAudioCandidateExtension`、`.cue`）从 `scanner.go` 移到 `scan_filter.go`，内容不变。遍历顺序改为：过滤 → 扩展名（不是候选也不是 `.cue` 就忽略，不论是否链接）→ 链接解析与 containment → `Stat` 与普通文件检查 → 解析。

### 3.3 失败分类（R3–R6 的校验矩阵）

| 情况 | 诊断类别 | 跳过数 | 登记保住 | root 完整性 |
| --- | --- | --- | --- | --- |
| 隐藏项、内置名单、用户排除 | 无 | 否 | 否 | 不影响 |
| 非候选扩展名（含断链的 `cover.jpg`） | 无 | 否 | 否 | 不影响 |
| 候选文件链接断开或越界 | `broken_or_escaped_link` | 是 | `file` | 完整 |
| 候选文件 `Stat` 失败 | `permission_error` | 是 | `file` | 完整 |
| 候选文件不是普通文件 | `non_regular_file` | 是 | `file` | 完整 |
| 不支持的音频格式 | `unsupported_format` | 是 | `file` | 完整 |
| 音频解析失败 | `parse_failure` | 是 | `file` | 完整 |
| 子目录读不了 | `walk_error`（复用已有类别） | 是（每个目录计 1） | `directory` | 完整 |
| CUE 语法/编码失败 | `unsupported_cue` | 是 | `cue_sheet` | 完整 |
| CUE 引用 `unsafe`/`unchecked` | `cue_reference` | 是（每张 CUE 计 1） | `cue_sheet` | 完整 |
| CUE 父音频解析失败或 `Stat` 失败 | `parse_failure` / `permission_error` | 是（每张 CUE 计 1） | `cue_sheet` | 完整 |
| CUE 引用目标不存在、缺 `INDEX 01`、宽松匹配多个 | `cue_reference` | 否 | 否 | 完整（现状，见 3.6） |
| 文件夹封面文件不安全/为空/超限/格式无效（发现阶段） | `artwork_unreadable`（新） | 否 | 否 | 完整；保留原封面关系 |
| 根目录不可用、根路径本身读不了 | `root_unavailable` | 否 | — | 不完整 |
| 空根保护触发（3.4） | `root_empty`（新） | 否 | — | 不完整 |
| 暂存写入/读取/清理失败、规划失败、候选写入失败、受管封面存储或绑定失败、数据库错误、指纹计算失败 | 原类别（`staging_*`、`catalog_write_failure`、`artwork_failure`、`database_error`、`fingerprint_failure`） | 否 | — | 不完整（现状） |
| 取消、进程重启 | — | — | — | 现状 |

- 同一文件只登记一次、只计一次跳过。
- 封面那一行是对 R3 的延伸：坏掉的 `cover.jpg` 和坏掉的音频一样，是“单个文件的问题”。原合同已经要求 fail closed、保留原关系（`library-scan-contracts.md` 封面段），本设计只把它从“令扫描不完整”改成只记诊断；受管目录写入、数据库绑定、旧 key 清理失败仍是 `artwork_failure` 并令扫描不完整。区分方式：`discoverFolderArtwork` 返回的错误包装为 `errArtworkSource`，`saveArtworkForRelease` 遇到它时记 `artwork_unreadable` 并保留原关系、返回 nil。
- `walk_error` 原本只在 `safeDiagnosticMessage` 与前端名表里保留（`scans.go:273`），现在重新启用，含义是“子目录读不了”。

### 3.4 空根保护（R7）

遍历结束、未取消时判断：`seen == 0`（候选数，含不支持格式与 `.cue`，不含被过滤的项）且本根没有 `walk_error`、遍历中没有命中用户排除项，且 `EXISTS (SELECT 1 FROM tracks WHERE source_root_id=$1 AND source_status='present')`。成立时记 `root_empty` 诊断（消息“目录是空的，可能还没挂载”），`scanRoot` 返回错误，root 不完整；此时暂存为空，不规划、不写入。查询放在 `ingest.RootHasPresentTracks(ctx, db, rootID)`（`tracks` 归 catalog）。

不触发的情况：新登记的空目录（库里没有 present 曲目）；有候选但全部读失败（`seen>0`，由保住机制兜住）；目录里只剩用户排除的子文件夹（排除项下的曲目应按 R1b 标 missing，所以命中过排除项就不判空根）。

### 3.5 保住登记（`catalog/ingest`）

```go
// HeldSourceKind 是本轮读不了、已有曲目需要原样保留的来源范围。
const (
    HeldFile      = "file"      // relative_path 等于物理曲目的 relative_path
    HeldDirectory = "directory" // relative_path 为目录，前缀匹配 relative_path 与 CUE 虚拟路径
    HeldCueSheet  = "cue_sheet" // relative_path 等于 tracks.cue_sheet_path
)
func HoldSource(ctx context.Context, db Execer, scanID, rootID, kind, relativePath string) error
func TouchHeldSources(ctx context.Context, db Execer, scanID, rootID string) (int64, error)
func RootHasPresentTracks(ctx context.Context, db queryer, rootID string) (bool, error)
```

- `HoldSource` 写入失败按暂存写入失败处理（`staging_write_failure`，root 不完整）。
- `TouchHeldSources`：`UPDATE tracks SET observed_at=NOW() WHERE source_root_id=$2 AND source_status='present' AND EXISTS(<held 匹配>)`。在 `persistStagedCandidates` 之后、暂存清理之前调用；失败按 `catalog_write_failure` 处理（root 不完整，不会误标 missing）。
- 匹配谓词（同时用于 vanished 排除与 touch，写成一个 SQL 片段常量）：

```sql
EXISTS (SELECT 1 FROM scan_held_sources h
  WHERE h.scan_run_id=$1::uuid AND h.root_id=$2::uuid AND (
    (h.kind='file'      AND t.relative_path=h.relative_path) OR
    (h.kind='directory' AND left(t.relative_path, length(h.relative_path)+1)=h.relative_path||'/') OR
    (h.kind='cue_sheet' AND t.cue_sheet_path=h.relative_path)))
```

- `ClearStagedObservations` 与 `ClearScansStaging` 同时删除对应的 `scan_held_sources` 行。

### 3.6 CUE 引用宽松匹配（R8，`parser/cue_path.go`）

`validateCueReference` 在 `os.Stat(target)` 返回不存在时，按顺序：

1. **同名宽松匹配**：在 `target` 的目录（目录部分必须按字节存在）里列出条目，找文件名满足 `strings.EqualFold(norm.NFC.String(name), norm.NFC.String(base))` 的普通文件。恰好一个 → 采用；多于一个 → 状态 `missing`，诊断 `ambiguous FILE reference: <ref>`；零个 → 下一步。
2. **同 stem 换格式回退**（原 `matchingAudioFile`）：按 `cueAudioExtensions` 顺序，对每个扩展名统计 stem 用同一比较函数相等的普通文件。某个扩展名恰好一个 → 采用；多于一个 → `missing` + 歧义诊断；零个 → 下一个扩展名。
3. 都没有 → 现有“目标不存在”处理。

采用后与原回退相同：`normalized` 改成实际文件名，照常做 containment 与 symlink 检查。比较函数放在 `parser` 内的 `cueNameEqual(a, b string) bool`。依赖 `golang.org/x/text/unicode/norm`（`go.mod` 已有 `golang.org/x/text`，改为直接依赖）。

### 3.7 排除子文件夹（R1b）REST 合同

迁移见 3.9。所有写接口：同源、管理员、`Idempotency-Key` 必需，键作用域 actor + 操作类型，指纹含小写 root ID 与请求参数；走 `beginRootOperation`。**不改变目录的 `revision`**（排除项是集合，增删天然可重复；不碰 revision 也避免影响停用/恢复的版本链）。

**目录列表**：`GET /api/v1/library-roots` 每项增加 `excluded_paths: string[]`（相对该目录的路径，`/` 分隔，按字节升序；没有时为 `[]`）。用一条 `LEFT JOIN … array_agg(… ORDER BY relative_path)` 查询，不逐行查。

**添加**：`POST /api/v1/library-roots/{id}/exclusions`

```json
// 请求：path 是目录浏览器给出的绝对路径
{ "path": "/music/Some Artist/Bootlegs" }
// 200 响应：与目录列表项同形
{ "id": "uuid", "name": "music", "path": "music", "status": "active", "revision": 3,
  "excluded_paths": ["Some Artist/Bootlegs"] }
```

**删除**：`DELETE /api/v1/library-roots/{id}/exclusions?path=<URL 编码的相对路径>`，200，响应同上。

| 条件 | 响应 |
| --- | --- |
| 缺 `Idempotency-Key` | `400 invalid_input` |
| 路由 ID 不是 UUID | `400 invalid_id`，不进入幂等流程 |
| 目录不存在 | `404 not_found` |
| 添加：`ValidatePath` 失败，或不在该目录**之内**（等于目录本身也算） | `400 invalid_library_path`（“打不开这个文件夹”） |
| 删除：`path` 为空、绝对路径、含 `..` 或非规范 | `400 invalid_input` |
| 添加已存在的排除项 / 删除不存在的排除项 | `200`，返回当前状态（幂等，不报错） |
| 该目录排除项已达 100 条 | `409 exclusion_limit`（“不扫描的文件夹太多了”） |
| 同键不同参数 | `409 idempotency_conflict` |
| 数据库失败 | `503 database_unavailable` |

- 存储的是 `filepath.Rel(root.path, validated)` 的 `/` 形式，所以 ⑫ 改路径后排除项仍然有效。
- 操作记录写 `library_root_operations`，类型 `exclude` / `unexclude`，`expected_revision` 为空、`result_revision` 为当前 revision，`after_state` 含 `{"relative_path": …}`。
- 有扫描在跑时允许增删，下一次扫描生效（扫描开始时读一次快照）。

**目录浏览器的目录内模式**：`GET /api/v1/library-folders?root_id=<id>[&path=<绝对路径>]`

- 有 `root_id` 时：先 `RequireAdmin`，按 ID 读目录路径（`LoadRootLocation`）；`path` 缺省等于目录路径；`path` 必须等于目录路径或在其内，否则 `400 invalid_library_path`。
- 响应形状不变。差别：当前为目录本身时 `parent=null`、`can_go_up=false`；`selectable` 只对目录**之内**的路径为 true（目录本身为 false）。
- `root_id` 不是 UUID → `400 invalid_id`；目录不存在 → `404 not_found`。
- 实现为 `library.ListFoldersWithin(input, rootPath, allowedRoots, limit)`，内部复用 `ListFolders` 的根内分支。

### 3.8 扫描结果表达（R9）

- `scan_runs.skipped_files BIGINT NULL`：本次扫描登记“保住”的次数（每个文件、读不了的子文件夹、出问题的 CUE 各计 1；不受诊断 100 条上限影响）。worker 在内存里计数，随进度刷新与终态写入；迁移前的历史扫描为 `NULL`。
- `ScanDTO` 增加 `skipped_files: integer | null`（所有入口共用 presenter；普通成员也可见，只是数字）。前端 decoder 严格校验：非负整数或 null。
- 终态规则不变：只要 root 都完整就是 `succeeded`；`skipped_files>0` 的成功扫描照常做 missing 对账。

### 3.9 迁移（`0034_scan_file_filter.sql` + `.down.sql`，合入时重新编号）

```sql
CREATE TABLE library_root_exclusions (
    root_id       UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL CHECK (relative_path <> '' AND relative_path NOT LIKE '/%'
                                       AND octet_length(relative_path) <= 4096),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (root_id, relative_path)
);
CREATE TABLE scan_held_sources (
    scan_run_id   UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    root_id       UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('file','directory','cue_sheet')),
    relative_path TEXT NOT NULL,
    PRIMARY KEY (scan_run_id, root_id, kind, relative_path)
);
ALTER TABLE scan_runs ADD COLUMN skipped_files BIGINT CHECK (skipped_files BETWEEN 0 AND 9007199254740991);
ALTER TABLE library_root_operations DROP CONSTRAINT library_root_operations_operation_type_check;
ALTER TABLE library_root_operations ADD CONSTRAINT library_root_operations_operation_type_check
    CHECK (operation_type IN ('create','disable','restore','exclude','unexclude'));
```

约束名以实际库中名字为准（`0007` 用内联 CHECK，系统生成名应为 `library_root_operations_operation_type_check`，实现时先查 `pg_constraint` 核对）。下行迁移删两张表与列，并把约束收回三种类型（存在 `exclude`/`unexclude` 记录时先删除这些记录）。

不需要回填：内置过滤与失败语义从下一次扫描开始生效。

### 3.10 合同同步（R10）

- `library-scan-contracts.md`：第 3 节“扫描完整性”改写为 3.3 的矩阵；第 4 节校验矩阵对应行更新（unsupported、CUE unsafe、非普通文件不再令扫描不完整；新增过滤、空根、目录读不了、封面源文件）；新增“路径过滤”“排除子文件夹”“保住登记”三段；CUE 回退段补宽松匹配；错误示例“unsupported 文件仍让扫描 succeeded 并执行 missing”改为正确示例。
- `scan-feedback-contracts.md`：预计数“候选为 CUE、支持音频与不支持音频候选扩展名的并集”后补“经同一路径过滤”；`ScanDTO` 增加 `skipped_files`。
- `scan-coordination-contracts.md`：`ScanDTO` 字段清单补 `skipped_files`。
- `library-folder-contracts.md`：目录内模式（`root_id`）。
- `error-handling.md`：409 行补 `exclusion_limit`。

## 4. 前端

- `diagnostic-kinds.ts`：新增 `root_empty: "目录是空的"`、`artwork_unreadable: "封面文件读不了"`；`walk_error` 改为“文件夹读不了”。顶部注释的核对来源同步。
- `scan-status.ts`：`incomplete` 的说明改为“有目录没扫完，那里的曲目保持原样。”；成功且 `skipped_files>0` 时，在状态旁显示“跳过 N 个文件”，点击跳到该扫描的诊断区。不加别的说明。
- 目录表（`root-table.tsx`）每行增加“不扫描的文件夹”入口（图标按钮，`aria-label` 写全），展开后列出 `excluded_paths`，每项一个移除图标按钮，末尾“添加”打开目录内模式的 `FolderBrowser`（传 `rootId`，选中即提交）。没有排除项时不显示空状态说明。
- `api/decoders.ts`、`endpoints.ts`：`RootDTO.excluded_paths`、`ScanDTO.skipped_files`、两个新端点；`use-root-commands.ts` 增加添加/移除命令（生成幂等键，失败可重试）。
- 文案：不解释过滤规则、不解释“为什么跳过”；按钮与列表本身说清楚的不再加段落。

## 5. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 路径过滤三级：强制 / 默认 / 用户配置（14.1，1770–1786） | 改写 | ROOMusic 没有可关闭的默认层：内置名单一层（不可关闭）+ 每个目录的用户排除一层；需求只要求这两层 |
| 规则构造期一次归一化（14.1，1786；20-8，2161–2162） | 照搬 | 每个根扫描开始时构造一次 `scanFilter` |
| 运行时规则在根之后的部分做子串匹配（14.2-2，1795） | 改写为整段相等 | 子串会误杀（参考设计自己在 14.2 末尾承认需防误杀）；PRD 明确不做子串匹配 |
| 路径短于根直接忽略（14.2-1，1794） | 不采用 | WalkDir 从根出发，相对路径不可能短于根 |
| 回收站需多个特征目录同时存在（14.2-3/4，1796–1797、1811） | 不采用 | 根目录是显式登记的，回收站名按整段匹配不会误伤正常专辑；PRD“不做”已列 |
| 用户目录隐藏项、路径含“分隔符+点”（14.1 默认忽略，1781；14.2-5，1798） | 改写 | 统一为“任一相对段以 `.` 开头”，覆盖 `._*`、`.Trash-*`、`.@__thumb` |
| NAS 回收站/快照形态（14.2-8~11，1801–1804） | 改写 | 用固定名单 `#recycle`、`@Recycle`、`#snapshot`、`@Recently-Snapshot`、`@eaDir`，以及八号机核实的 fnOS 卷级目录 `@appmeta`、`@team`；fnOS 回收站 `.@#local/trash` 由隐藏项规则覆盖；不用正则 |
| 系统卷信息、哈希缓存、自身备份目录（14.1 强制，1774–1777） | 照搬（部分） | `System Volume Information`、`$RECYCLE.BIN`、`lost+found` 入名单；ROOMusic 的备份与数据目录不在音乐根内，不需要 |
| 目录被忽略只跳过本层文件，子目录到下一层再判（6.2，286–288） | 改写为跳过整棵子树 | 参考设计这样做是因为它的规则是子串式、怕误伤同名子目录；整段匹配下命中的目录本身就是系统目录，整棵跳过等价且更省 |
| 类型级判定只看扩展名、不读内容（6.4-③，330–333；20-6，2155–2156） | 照搬 | 并把扩展名判定提前到链接解析之前（R2） |
| 估算总数与正式扫描用不同判定导致进度到不了 100%（5.1，244–265） | 照搬其教训 | 预计数与遍历调用同一个 `scanFilter` |
| 跳过记录 + 原因码，不中断批次（8.3，715–725；16.4，1904–1912） | 改写 | 原因码复用现有诊断类别（含 `walk_error`），并按 PRD 增加“已有曲目保持原状、计为本轮见到、不参与移动配对” |
| 时间戳未变但上轮读取失败 → 重试（6.6，368–369） | 照搬语义 | 读失败的文件不缓存任何结果，下次扫描自然重读（④ 的解析缓存也不写失败项） |
| 目录列举失败：记诊断、进重试、以空集合继续（16.3，1894–1902；7.2，643） | 改写 | 不重试、不以空集合继续删除；该目录下已有曲目全部保住，其余目录照常对账。参考设计“空集合继续 + 逐目录即时删除”会把读取失败放大成删除（gap-analysis §4） |
| 可达但不存在 vs 不可达（16.2，1885–1892）；根扫出 0 项摘除后端并复查（26.6 消费侧，5788） | 改写 | 不可用沿用 `root_unavailable`；新增空根保护：可访问但一个候选都没有、而库里还有曲目时按未挂载处理，不周期复查（下一次扫描或定时扫描自然重试） |
| 路径比较两侧规范为 NFD，失败回退原串（26.7.3，5835–5840；13.7，1674–1694） | 改写 | 只用于 CUE 引用的同目录回退匹配，用 NFC + 大小写折叠，且要求恰好一个匹配；扫描的来源身份仍按字节，避免在 ext4 上把两个真实文件当成一个 |
| 大小写敏感性自动探测（26.7.2，5814–5833） | 不采用 | gap-analysis §4：ext4 上探测不出时默认不敏感会误合并 |
| 符号链接循环解析 100 层（4.5，206–215；26.5，5665–5713） | 不采用 | 现有规则：目录链接不跟随，文件链接 `EvalSymlinks` 后做 containment |

## 6. 兼容与已有数据

- 升级后第一次成功扫描：回收站、`@eaDir`、隐藏目录里已入库的曲目不再被见到，会被标 missing（这是想要的结果）；用户排除的子文件夹同理。missing 的曲目和 Release 保留，个人数据不丢；恢复（取消排除）后下一次扫描按来源身份恢复 present，UUID 不变（`persist_track.go:81`）。
- 过去因为 `._*` 或 `.ape` 一直“不完整”的库，第一次成功扫描会一次性执行积压的 missing 对账与移动配对。真实库 smoke 要对比前后 missing 数量（见 implement.md）。
- 历史扫描 `skipped_files=null`，前端不显示跳过提示。
- 无需回填，无需重扫。

## 7. 风险与回滚

| 编号 | 风险 | 应对 |
| --- | --- | --- |
| R-1 | 保住谓词漏匹配某类曲目（例如 CUE 虚拟轨），导致读失败时被误标 missing | 单测覆盖三种 kind × 物理/CUE；集成测试“损坏 FLAC + 已有曲目 → succeeded 且仍 present” |
| R-2 | 第一次成功扫描批量标 missing（回收站内容、积压的删除） | 预期行为；smoke 前后对比 missing 分布，异常时先停用新版本再查 |
| R-3 | 用户把目录里唯一的子文件夹排除后，空根保护误判为“未挂载” | 遍历时记 `excludedHit` 标志，命中过排除项就不触发空根保护（3.4）；单测覆盖 |
| R-4 | 内置名单误伤真实专辑目录（如专辑名恰为 `lost+found`） | 只整段相等，名单极短；可接受 |
| R-5 | 约束名与预想不同导致迁移失败 | 实现前查 `pg_constraint` |

回滚：代码回滚到合入前即可恢复旧语义；`0034` 下行迁移删除新表与列。已被标 missing 的曲目在旧版本下次扫描时按来源身份恢复。

## 8. 线间关系

- 本线共享文件：`library/scanner.go`、`library/scan_progress.go`、`library/scans.go`、`library/httpapi/{roots,folders,root_operations,handlers}.go`、`catalog/ingest/{staging,identity_plan}.go`、`parser/cue_path.go`、前端 `features/library/*`。
- **M 线 ⑩**：本项把扩展名表移到 `library/scan_filter.go`，⑩ 只改这张表和 `parser/cue_path.go` 的 `cueAudioExtensions`。⑩ 依赖本项“不支持格式只记诊断”的语义。建议合入顺序 S① 先于 M⑩；若 ⑩ 先合，本项合入时把 ⑩ 的扩展名改动搬到新文件。
- **⑫、④、⑤** 以本项的 `scanFilter`、`HoldSource`/`TouchHeldSources`、`skipped_files` 为前提。

## 9. 已核实（八号机 fnOS，2026-11-08 主会话只读核实）

- 回收站在 `/vol2/1001/.@#local/trash`，点开头，隐藏项规则已覆盖。
- 卷一级的系统目录 `@appmeta`、`@team` 已补进 3.1 的内置名单。
- `thumb` 名字太常见，可能是正常文件夹名，不加。
- 音乐目录里只有 1 个点开头的隐藏文件，没有 `._*`。

八号机真实库与本机样本库 `music/` 的文件清单完全相同。`music/` 的现状：1 个隐藏文件（`.0c45…42`）、44 个 `:Zone.Identifier` 附属文件（非候选扩展名，本来就忽略）、1 个 `.m3u`，没有 `._*`。

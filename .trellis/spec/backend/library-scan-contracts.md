# 目录与扫描流程合同

本合同按主题拆成以下文件，章节编号沿用原文：

| 文件 | 内容 |
| --- | --- |
| 本文件 | §1 范围与触发、§2 接口签名、§3 目录新增与操作幂等、路径过滤、排除子文件夹、保住登记 |
| [扫描流程](./library-scan-pipeline.md) | §3 扫描完整性、暂存、身份、CUE 与封面，根保存后的专辑收藏转移，并发解析（⑤），M5 ④搜索投影挂接 |
| [增量扫描](./library-scan-incremental.md) | §3 增量扫描（④）：存储与观察、缓存与延迟封面、版本号与升版本规则、借用指纹与完整重扫、候选签名与跳过 |
| [归组与发行](./library-scan-grouping.md) | §3 发行候选、抓轨与封面，碟号、独占合成与介质标题，文件名推断与同目录归组（③），MP3 扫描时长，新格式内容指纹 |
| [标签与古典作品](./library-scan-tags.md) | §3 标签多值、署名与合辑（⑥⑦），古典作品与乐章（⑧） |
| [校验矩阵与测试](./library-scan-checks.md) | §4 校验与错误矩阵、§5 案例、§6 必需测试 |
| [验证与示例](./library-scan-verification.md) | T 线补充的案例、测试与错误示例；§7 错误与正确示例；② 文件夹名规则验证；③ 文件名与同目录归组验证 |

## 1. 范围与触发

目录生命周期（含改路径保留 ID）、幂等及扫描（遍历、暂存、身份规划、Release Graph、CUE、抓轨、署名与封面）遵守本合同。扫描状态、持久化取消与跨进程协调见
[扫描协调合同](./scan-coordination-contracts.md)；发行读取接口见 [Catalog REST 合同](./catalog-rest-contracts.md)；内嵌歌词的提取与保存见[歌词合同](./lyrics-contracts.md)；
通用请求与响应、迁移执行器与 CI 见 [Core 0 当前运行合同](./core0-runtime-contracts.md)。

M5 新增全根预计数、逐根加权进度与事务内变更记账；候选/封面/终态 missing 均遵循“全部既有发行升序锁 → 独立重读 → 组升序共享锁”，不得持组补发行锁。反馈锁失败降级不阻断扫描，详见 [扫描反馈合同](./scan-feedback-contracts.md)。

## 2. 接口签名

- 目录新增响应：`{ "id": string, "name": string, "status": "active" | "disabled", "revision": integer }`。
- 目录列表项目：`id`、安全 basename `path`/`name`、`status`、`revision`、时间戳与必需的 `excluded_paths: string[]`（无排除时 `[]`，相对路径按字节升序）；
  不返回原始绝对路径。
- `POST /api/v1/library-roots/{id}/relocate`：同源、管理员、必需 `Idempotency-Key`；请求为
  `{"path":"/music/nas-new/Music","expected_revision":4}`，成功返回 `200`。
  响应严格为以下六字段，不附带目录列表的时间戳；`name`/`path` 都是安全 basename，`excluded_paths` 为原样保留的相对路径数组（无排除时 `[]`）：

```json
{"id":"uuid","name":"Music","path":"Music","status":"active","revision":5,"excluded_paths":[]}
```

## 3. 合同

### 目录新增与操作幂等

- 目录新增对已存在路径执行幂等 upsert，但不隐式恢复 disabled 状态；响应和
  Operation Journal 使用数据库真实 `status`/`revision`。
- 目录新增、停用、恢复、改路径及排除项增删必须带 `Idempotency-Key`，缺失返回 `400 invalid_input`。键的作用域是
  actor 加操作类型；请求指纹包含目标身份：新增为规范化路径，停用/恢复为小写路由 root ID 加
  请求参数（`expected_revision` 等），改路径为 `{"root_id":<小写 ID>,"path":<ValidatePath 后的路径>,"expected_revision":n}`。这些入口共用 `beginRootOperation`：先开启 READ COMMITTED
  事务并取得该键的事务级 advisory lock，再在事务内查记录，因此同键请求串行执行。
  指纹一致时返回首次的状态码和响应（新增 `201`，其余 `200`），不重复执行；指纹不一致时返回
  `409 idempotency_conflict` 且无副作用；同键并发只执行一次，其余请求回放首次记录。
  路由 root ID 格式错误返回 `400 invalid_id`，不进入幂等流程。只有成功的操作写记录，失败的
  尝试不占用幂等键。2026-09-29 起指纹含目标；此前记录的停用/恢复键不做兼容，升级后重试
  返回 `409 idempotency_conflict`（开发阶段裁决，见 F2 任务）。
- 改路径操作类型为 `relocate`，复用 `library.ValidatePath`（Abs + Clean、真实目录、允许根内、任一层不得为符号链接）。处理顺序固定为：
  同源 → 管理员 → 路由 ID → 幂等键 → 解码/参数校验 → `ValidatePath` → `beginRootOperation`（键锁及回放检查）
  → `library.TryLockScanCoordination(ctx, tx)` → `SELECT … FOR UPDATE` 锁目录行 → revision 校验 → 新路径冲突检查 → 更新与记录 → 提交。
  回放先于扫描锁探测，不因后来开始的扫描改报 `scan_running`；入口参数与路径校验仍先执行。
- 改路径允许 `active` 和 `disabled`，保持当前状态；新路径已被其他根登记（包括 disabled）时拒绝，不额外限制不同根之间的路径嵌套。
  路径实际变化时只更新根路径、revision 加一及更新时间，不改 root ID；规范化后等于当前路径时仍校验 revision、取得扫描锁，返回当前 DTO、revision 不变，照常写成功记录。
- 扫描互斥使用与执行器同键的事务级 advisory lock，未取得时 `409 scan_running`，不写记录、不占用幂等键；取得后持有至提交或回滚。
  禁止只查活动扫描行或进程内状态，避免检查后扫描读到旧路径；helper 与常量归属见[扫描协调合同](./scan-coordination-contracts.md)。
- 改路径成功收据与路径更新在同一事务；`expected_revision` 为请求版本，`result_revision` 为结果版本。
  `before_state` / `after_state` 分别存 `{"path":<旧/新绝对路径>,"revision":n}`，仅留在数据库内，不由任何接口返回。
  回放 `response` 只存上述安全六字段；目录响应、操作记录列表及错误不得返回绝对路径或前后态。
- 恢复仍先校验当前目录为 `disabled` 且 `expected_revision` 相符；按目录取最近一条成功的 `disable` 记录，并要求其 `before_state.status='active'`。
  不再要求 `result_revision = 当前 revision` 或 `before_state.revision = 当前 revision - 1`，所以“停用 → 改路径 → 恢复”合法。
  恢复只将状态改回 `active`、当前 revision 加一，保留新路径和排除项，不恢复旧路径或旧版本；没有有效停用前态仍为 `409 revision_conflict`。
- 改路径不迁移来源身份、曲目、Release、收藏、历史、歌单或修正；根 ID 与相对路径不变时来源身份不变。
  新位置内容相同，下一次扫描沿用曲目 UUID 与 Release UUID；内容不同沿用现有换内容、移动配对及 missing 规则。相对排除项不改写。
  前端在改路径提交成功后另行调用 `POST /api/v1/scans`，新扫描返回 `202`、已有扫描返回 `200`；改路径事务本身不启动扫描。
- `0035_root_relocate.sql`（合入时重新核对编号）只放宽 `library_root_operations_operation_type_check`，允许
  `create / disable / restore / exclude / unexclude / relocate`，不回填或迁移业务身份数据。
  `0035_root_relocate.down.sql` 先删除 `operation_type='relocate'` 的记录，再恢复不含 `relocate` 的原五类约束。
  下行会丢弃改路径收据，但不会把 `library_roots.path` 改回旧值，也不回退 revision；不提供操作记录撤销入口，改回位置须再次调用改路径接口。

### 路径过滤

- `library/scan_filter.go` 拥有 `newScanFilter(exclusions []string)`、`skipDir(relativeDir, name string)`、`skipFile(name string)` 和现有扩展名表。每根构造一次，预计数与正式遍历共用该过滤；`relativeDir` 是当前目录的根内相对路径。只看注册根之后的段，注册根自身不参与过滤。
- 任一根内段以 `.` 开头就忽略；目录名按 `strings.EqualFold` 整段匹配 `@eaDir`、`#recycle`、`@Recycle`、`@Recently-Snapshot`、`#snapshot`、`$RECYCLE.BIN`、`System Volume Information`、`lost+found`、`__MACOSX`、`@appmeta`、`@team`。名单只作用于目录；不做子串匹配，`my.recycle`、`recycle`、`Music@eaDir2` 不命中。
- 用户排除按 `/` 分隔的根内相对目录路径逐字节相等。命中目录用 `fs.SkipDir` 跳过整棵子树；隐藏文件直接跳过。过滤不记诊断、不计预计数或跳过数，也不登记保住；该范围旧来源在成功扫描后正常标 missing。
- 文件按“过滤 → 候选扩展名（含 CUE）→ 链接解析与 containment → Stat/普通文件 → 解析”处理；非候选附件及其断链直接忽略。

### 排除子文件夹

- `POST /api/v1/library-roots/{id}/exclusions` 接收 `{"path":"/music/Artist/Extras"}`；必须是 `ValidatePath` 接受的真实子目录，等于登记根也拒绝。存 `filepath.Rel` 的 `/` 形式，最多 4096 字节。
- `DELETE /api/v1/library-roots/{id}/exclusions?path=<URL 编码相对路径>`；只校验非空、非绝对、不含 `..` 且规范，不要求目录仍存在。两者同源、管理员、必需 `Idempotency-Key`，指纹含小写 root ID 和请求 path；操作类型 `exclude` / `unexclude`，沿用 `beginRootOperation`。
- 都返回 `200` 的当前目录列表项（含 `excluded_paths`）。添加已有项、删除不存在项同样成功。每根最多 100 项，超过为 `409 exclusion_limit` / “不扫描的文件夹太多了”；先锁 root，再独立读取集合，防止并发越限。
- 不改 root 的 revision；收据 `expected_revision` 为空，`result_revision` 为当前版本，`after_state={"relative_path":…}`。列表与扫描根快照用一条 LEFT JOIN 聚合读排除项，不逐根查；保存后下一次扫描生效。
- 非法 ID 为 `400 invalid_id`；未知根 `404 not_found`；添加路径失败 `400 invalid_library_path` / “打不开这个文件夹”，删除格式失败 `400 invalid_input`；数据库失败 `503 database_unavailable`。目录内浏览见[目录浏览合同](./library-folder-contracts.md)。

### 保住登记

- `catalog/ingest/held_sources.go` 拥有 `HoldSource(ctx, db, scanID, rootID, kind, relativePath)`、`TouchHeldSources(ctx, db, scanID, rootID) (int64,error)`、`RootHasPresentTracks(ctx, db, rootID) (bool,error)`。
- `scan_held_sources` 以 scan/root/kind/path 唯一登记失败范围：`file` 使用现有入库的 `ingest.NormalizedPath` 规范后精确匹配 `tracks.relative_path`（不做 Unicode 或大小写合并）；`directory` 用 `left(t.relative_path,length(h.relative_path)+1)=h.relative_path||'/'` 匹配子树（含 CUE 虚拟路径）；`cue_sheet` 精确匹配 `tracks.cue_sheet_path`。三类共用 SQL 谓词，同时用于 `vanishedTracksCTE` 排除与 touch，保住曲目不被其它同指纹文件抢走 UUID。
- 候选保存之后、暂存清理之前且未取消时，touch 只把命中的 present 曲目的 `observed_at` 更新为 NOW()，不改事实、状态、指纹或搜索投影，不恢复原本 missing 的曲目；观察时间不算报告更新。来源读取失败不缓存，下轮自然重读。
- Hold 写失败为 `staging_write_failure`；touch 失败为 `catalog_write_failure`，root 不完整。`ClearStagedObservations` 与 `ClearScansStaging` 同时清理 observation 和 held；取消与恢复继续沿用原协调合同。
- 迁移暂定 `0034_scan_file_filter.sql` / `.down.sql`：增加 `library_root_exclusions`、`scan_held_sources`、可空 `scan_runs.skipped_files` 和操作类型；历史不回填。下行先删除新操作类型的记录，再收回约束并删除两表一列，合入时统一编号。

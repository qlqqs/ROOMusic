# ⑫ 目录改路径保留 ID · 技术设计

需求见 [prd.md](./prd.md)。S 线第 2 项，以 [① 的设计](../11-08-scan-file-filter/design.md) 为前提（排除项存相对路径、`library_root_operations` 类型约束已在 ① 放宽过一次）。参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 部分 | 归属 |
| --- | --- |
| 改路径接口、幂等、revision、操作记录 | `library/httpapi`（`root_operations.go` 新增 handler，复用 `beginRootOperation`） |
| 路径校验 | `library.ValidatePath`（不改） |
| 扫描中拒绝 | `library`（新增 `library.TryLockScanCoordination(ctx, tx)`，与扫描锁同一键） |
| 前端“改路径” | `features/library`（目录表行操作 + 对话框 + `FolderBrowser`） |

不改：来源身份格式、身份规划、扫描流程。改路径只改 `library_roots.path`，其余一切以 root ID 为键的数据（曲目来源身份 `<rootID>:physical:v1:<相对路径>`、① 的排除项、④ 的解析缓存与候选状态、封面、派生、分析）都不需要迁移。

## 2. 数据流

```
管理员在目录表点“改路径” → FolderBrowser 选新位置
  → POST /api/v1/library-roots/{id}/relocate { path, expected_revision } + Idempotency-Key
      ValidatePath(path) → beginRootOperation（事务 + 键锁 + 回放检查）
      → pg_try_advisory_xact_lock(扫描锁两键) 失败 → 409 scan_running
      → SELECT … FOR UPDATE 目录行；校验 revision、新路径未被别的目录登记
      → UPDATE library_roots SET path, revision+1；写操作记录；提交（事务锁随之释放）
  → 前端收到 200 后调用 POST /api/v1/scans（与“开始扫描”同一入口）
      202 → 进入扫描详情；200（已有扫描在跑，极少）→ 提示已有扫描
下一次扫描：loadRegisteredRoots 读到新路径 → 同样的相对路径得到同样的来源身份 → 曲目与 Release 照常沿用
```

## 3. 接口合同

### 3.1 `POST /api/v1/library-roots/{id}/relocate`

同源、管理员、`Idempotency-Key` 必需。

```json
// 请求
{ "path": "/music/nas-new/Music", "expected_revision": 4 }
// 200 响应（与停用/恢复的响应同形，① 之后含 excluded_paths）
{ "id": "uuid", "name": "Music", "path": "Music", "status": "active", "revision": 5, "excluded_paths": [] }
```

- 幂等指纹：`{"root_id": <小写 ID>, "path": <ValidatePath 后的路径>, "expected_revision": n}`；操作类型 `relocate`。
- 停用状态的目录也可以改路径（NAS 换位置时管理员可能先停用）。
- 新路径等于当前路径：仍校验 `expected_revision`，成功时返回当前 DTO，revision 不变，照常写操作记录（便于回放）。
- 不额外限制新路径与其他目录嵌套（与新增目录的现行规则一致）。

| 条件 | 响应 | 副作用 |
| --- | --- | --- |
| 未登录 / 非管理员 / 来源不符 | 401 / 403 / 来源拒绝 | 无 |
| 路由 ID 不是 UUID | `400 invalid_id` | 不进入幂等流程 |
| 缺 `Idempotency-Key` | `400 invalid_input` | 无 |
| `path` 缺失或 `expected_revision < 1` | `400 invalid_input` | 无 |
| `ValidatePath` 失败（越界、不存在、不是目录、任一层是符号链接） | `400 invalid_library_path`（“目录不在允许的音乐根目录内”，与新增目录相同） | 不进入幂等流程 |
| 同键同参数重放 | 首次的状态码与响应 | 不重复执行 |
| 同键不同参数 | `409 idempotency_conflict` | 无 |
| 有扫描在跑（本进程或其他进程持有扫描锁） | `409 scan_running`（“正在扫描，稍后再试”） | 无，不写记录 |
| 目录不存在 | `404 not_found` | 无 |
| revision 不符 | `409 revision_conflict` | 无 |
| 新路径已被另一个目录登记（含停用的） | `409 path_already_registered`（“这个文件夹已经添加过了”） | 无 |
| 数据库失败 | `503 database_unavailable` / 记录写入失败 `500 operation_failed` | 回滚 |

### 3.2 扫描中拒绝的实现

扫描执行器用专用连接持有 session 级 advisory lock `(0x5343414e, hashtext(current_schema()))`（`scan-coordination-contracts.md` 第 2 节）。改路径事务内执行 `SELECT pg_try_advisory_xact_lock(1396916558, hashtext(current_schema()))`：

- 返回 false → 有扫描在跑 → `409 scan_running`。
- 返回 true → 本事务持有该锁直到提交，期间任何 `StartScan` 拿不到锁，会读取活动扫描；没有活动扫描时返回 `503 scan_coordination_unavailable`。窗口只有一个短事务，前端改完路径再发起扫描，不会撞上；定时扫描（⑪）撞上时按“本次跳过”处理并在下一次检查时重试。
- 键常量 `scanAdvisoryLockClass` 已在 `library/scans.go:30`，取锁函数放在同一文件，httpapi 通过 `library.TryLockScanCoordination(ctx, tx) (bool, error)` 调用，不在 handler 里复制键。

为什么不查 `scan_runs.status='running'`：查完到提交之间扫描可能开始，读到旧路径；用同一把锁才没有这个窗口。

### 3.3 恢复操作的版本链修正

现有恢复要求“存在一条 `result_revision = 当前 revision` 的停用记录”（`root_operations.go` 的 `restoreRoot`）。停用后再改路径，revision 已经前进，恢复会报“找不到可恢复的目录状态”。改为：取该目录**最近一条**成功的 `disable` 记录，要求其 `before_state.status='active'`；不再要求 `result_revision` 与当前 revision 相等，也不再要求 `before_state.revision = rev-1`。目录状态只有两种，恢复的目标永远是 `active`，这条记录只用于写操作记录的前态。

### 3.4 迁移（`0035_root_relocate.sql` + `.down.sql`，合入时重新编号）

只放宽 `library_root_operations` 的类型约束，增加 `relocate`（在 ① 已加的 `exclude`/`unexclude` 基础上）。下行迁移先删除 `relocate` 记录再收回约束。`before_state`/`after_state` 记录 `{"path": <绝对路径>, "revision": n}`；这两列只在库内，任何接口都不返回（操作记录列表只返回类型、状态、revision，`root_operations.go:117`）。

### 3.5 合同同步（R5）

- `library-scan-contracts.md`：“目录新增与操作幂等”一节加入改路径（指纹、扫描中拒绝、新路径冲突、等于当前路径的处理、恢复版本链的新规则）；第 4 节矩阵补对应行。
- `scan-coordination-contracts.md`：第 2 节说明扫描锁也被改路径事务以 `pg_try_advisory_xact_lock` 短暂探测。
- `error-handling.md`：409 行补 `scan_running`、`path_already_registered`。

## 4. 前端

- `root-table.tsx` 操作列增加“改路径”图标按钮（`aria-label="改路径 <目录名>"`），停用与启用状态都显示。
- 点开后在对话框里用现有 `FolderBrowser`（普通模式，从 `/` 开始，与新增目录一致）；选中后显示所选路径，“确定”提交。
- 成功后自动调用开始扫描：202 跳到扫描详情；200 显示与扫描区段相同的“已经有扫描在运行”提示。
- 错误文案用 `root-messages.ts` 的映射：`scan_running` →“正在扫描，稍后再试。”；`path_already_registered` →“这个文件夹已经添加过了。”；`revision_conflict` →“目录刚被改过，请再试一次。”（列表会自动刷新，用户直接重试即可）；路径被拒绝沿用“打不开这个文件夹。”。
- 不加任何关于“ID 不变”“收藏不受影响”的说明。

## 5. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 资源标识 = 相对存储根的路径，库可迁移（11.2，995–1018） | 照搬（已具备） | ROOMusic 来源身份本来就是 root ID + 相对路径；本项只补“改根路径”的入口 |
| 卷标识（分区 UUID）跨重挂载保持库归属（26.4.3，5607–5646） | 改写 | 不读分区 UUID、不探测挂载；由管理员显式改路径，root ID 充当“卷标识”。原因：容器里拿不到可靠的块设备信息，NAS 上是网络挂载 |
| 挂载事件 / 可用性轮询自动重新挂载（4.4，201–204；26.6 消费侧，5785–5796） | 不采用 | PRD“不做”：自动探测挂载点变化 |
| 双根模型：外部根与磁盘根分离、链接循环解析（26.5，5677–5713） | 不采用 | 现有规则不允许目录路径含符号链接（`ValidatePath`），只有一个根路径 |

## 6. 兼容与已有数据

- 无数据迁移。改路径之前的所有曲目、Release、个人数据、修正、默认版本、认领原样保留。
- 新位置内容与旧位置一致：下一次扫描全部按来源身份匹配，UUID 不变，missing 数为 0。④ 合入后，解析缓存以 `(root_id, 相对路径)` 为键、按大小与修改时间判断，同样的文件挂到新位置后仍命中（挂载不改 mtime 时）。
- 新位置内容不同：按现有身份规划处理（换内容、移动配对、missing）。
- 播放与分析在改路径后立即用新路径（每次按 root ID 读路径，`root_location.go`）；改路径到扫描完成之间，新位置缺失的文件播放会报文件不可用，与现有“文件被删”的行为相同。

## 7. 风险与回滚

| 风险 | 应对 |
| --- | --- |
| 管理员选错位置，扫描把大量曲目标 missing | 再改回原路径并扫描，按来源身份恢复 present，UUID 不变；① 的空根保护会挡住“选到一个空目录”的情况 |
| 恢复版本链放宽后误恢复 | 状态只有两种，恢复前仍校验 `expected_revision` 与当前为 `disabled` |
| 扫描锁探测与 `StartScan` 竞争 | 只在一个短事务内；`StartScan` 已有“拿不到锁 → 返回活动扫描或 503”的路径 |

回滚：代码回滚即移除入口；`0035` 下行迁移删除 `relocate` 记录。已改的路径保留在 `library_roots.path`，旧版本照常使用。

## 8. 线间关系

- 依赖 ①：排除项是相对路径，`library_root_operations` 类型约束在 ① 的基础上再加一种；目录 DTO 已含 `excluded_paths`。
- 为 ④ 提供前提：④ 的解析缓存、候选状态都以 root ID 为键，本项保证改路径后键不变。
- 为 ⑪ 提供约定：定时扫描遇到 `StartScan` 返回协调不可用时按跳过处理。
- 共享文件：`library/httpapi/root_operations.go`、`handlers.go`、前端 `root-table.tsx`、`root-messages.ts`、`endpoints.ts`、`decoders.ts`（与 ① 同线，顺序开发无冲突）。

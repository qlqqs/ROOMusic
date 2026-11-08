# ⑫ 目录改路径保留 ID

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，S 线第 2 项。参考设计 11.2（标识用相对路径）、26.4.3 章（卷标识跨重挂载保持库归属）。

## 目标

NAS 挂载点或音乐目录换了位置时，管理员可以直接改已登记目录的路径，收藏、历史、歌单、修正都不受影响，不用删了重加。

## 现状（`B/` = `backend/internal/`）

- `library_roots`：id、path（UNIQUE）、status（active/disabled）、revision、时间戳（`backend/migrations/0002_core_slice.sql:20-24`，`0007_root_operations.sql:1-3`）。操作只有新增、停用、恢复，记录在 `library_root_operations`，必须带 `Idempotency-Key`（`B/library/root_operations.go:58-104,186-197,256-268`）。代码里没有修改 path 的语句。
- `ValidatePath`（`B/library/roots.go:12-37`）：Abs + Clean；任一层不能是符号链接；必须是目录；必须在允许根内（生产为 `/music`）。
- 曲目来源身份是 `<rootID>:physical:v1:<相对路径>` 或 CUE 形式（`B/catalog/ingest/identity.go:11-21`），封面、派生缓存、分析都不含绝对路径；运行时由 root ID 查当前路径再拼相对路径（`B/library/root_location.go:26-38`）。所以只要 root ID 不变，改路径后同样的相对路径就是同一批曲目。
- 改名配对只在同一个 root 内查（`B/catalog/ingest/identity_plan.go:247`）；现在换路径只能新增一个目录，等于全新的曲目和 Release。
- 前端目录管理：`frontend/src/features/library/components/root-section.tsx`、`root-table.tsx:27-43`（停用、恢复）、`add-root-form.tsx` 内嵌 `folder-browser.tsx`。目录列表不返回绝对路径（`B/library/httpapi/roots.go:33`）。
- 目录操作不走 `operations.Run`，不能从操作记录撤销（`0026_artist_catalog.sql:113-116`）。

## 需求

- **R1 改路径接口**：新增“修改目录路径”操作，带 `Idempotency-Key` 与 `expected_revision`，校验同 `ValidatePath`，新路径不能已被其他目录登记；成功后 revision 加一，记录在 `library_root_operations`（新操作类型）。
- **R2 保留身份**：目录 ID 不变；下一次扫描按相对路径对上原曲目，曲目 UUID、Release 沿用、个人数据不变。新位置内容不同的部分按现有身份规划处理（换内容、移动、missing）。
- **R3 扫描中**：有扫描在跑时拒绝修改，提示稍后再试。
- **R4 前端**：目录列表每行增加“改路径”操作，用现有目录浏览器选新位置；改完后自动发起一次扫描。界面文案按 CLAUDE.md 前端文案规则。
- **R5 合同同步**：更新 `library-scan-contracts.md` 的目录操作与幂等部分。

## 验收

- 单测与集成测试：幂等回放、指纹冲突、revision 冲突、新路径已登记、新路径越界或是链接、扫描中拒绝；改路径后扫描曲目 UUID 不变。
- 前端：改路径流程截图复核。

## 不做

- 跨目录合并、自动探测挂载点变化、从操作记录撤销。

## 已裁决

- 2026-11-08 用户裁决：改完路径后自动开始扫描（R4）。
- 2026-11-08 主会话裁决（设计审阅）：停用的目录也能改路径（换盘时常先停用）；恢复操作改为取最近一条成功的停用记录，不再要求其 revision 紧接当前 revision。

## 依赖

S 线第 2 项，在 ① 之后、④ 之前（④ 的观察缓存以来源身份为键，本项保证改路径后来源身份不变）。

## 实施结果（2026-11-08）

提交：`f3d3d04`（`POST /library-roots/{id}/relocate`：幂等、revision 校验，扫描在跑或新路径已登记时拒绝，停用的目录也能改；恢复取最近一条成功的停用记录；目录表“改路径”图标按钮与对话框，改完自动开始扫描）。随 S1 合并 `c08c15e` 入 main，迁移 `0035_root_relocate`。

裁决与偏离：实施中没有新增，均按上文“已裁决”实现。

验收：

- 单测与集成：满足。`TestPostgreSQLRelocateRootOperationTransactionsAndReplay`（幂等回放、同键不同请求冲突）、`TestPostgreSQLRelocateRootRejectsInvalidRequestsWithoutChanges`（revision 冲突、新路径已登记、越界、目录链接与祖先链接）、`TestPostgreSQLRelocateRootScanLockRejects`、`TestPostgreSQLRelocateDisabledRootThenRestoreKeepsNewPath`、`TestPostgreSQLRelocateThenScanPreservesTrackAndReleaseUUIDs`（改路径后扫描曲目与专辑 UUID 不变）；前端 `library-roots.test.tsx` 覆盖改完自动扫描的 202 与 200 两种返回。
- 前端截图：满足。改路径对话框选中新位置后的 1440/375 截图经主会话目检。

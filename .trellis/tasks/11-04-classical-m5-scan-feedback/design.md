# M5 ⑥ 扫描反馈技术设计（主会话裁决修订版）

## 0. 状态、范围与规划选择

- 日期：2026-11-04。依据：本子任务 `prd.md`、父任务 `../11-04-classical-m5/prd.md`、下文列出的现有合同与实现。
- 初稿已执行 `trellis-start` 的上下文检查，本轮再次按 `trellis-brainstorm` 核对裁决与已有合同；**只修订本文件**，不修改 PRD、代码、迁移、spec 或任务状态，不提交，不启动实施，不连接任何数据库。
- 规划选择路线：**人工选择**；选择者为 qlqq（主会话于 2026-11-04 传达裁决）。选择结果：D1 采用全根计数后的逐根流水，D2–D4 接受报告口径、反馈失败降级与加入时间定义，详见 §10.2。理由：先扫完的根更早提供可见专辑，维持现有暂存峰值，同时保持总体进度单调与报告可核对。此设计裁决不是本轮实施授权。
- 本文选定方案：全根计数、逐根读取→整理→保存；总体按固定根文件数加权；数据库保存权威进度；扫描变更显式记账；报告历史冻结、认领提示实时读取；最近新增按可见发行的最早创建时间排序。
- 范围内：进度、管理员报告、最近新增与排序、前后端合同、最小验证及合并边界。范围外：暂停、续跑、调度、监听、预计剩余时间、报告导出、历史报告补算、自动认领、音乐文件修改、扫描全事务回滚。
- 本文件不是完整执行计划。主会话负责将已裁决的路线、选择者、结果、理由与事实修正同步 PRD，补齐 `implement.md`，核对 `implement.jsonl` / `check.jsonl`，再按工作流组织实施。**本轮维持 planning，只完成设计修订。**

## 1. 已核对的事实与约束

| 证据 | 当前事实及设计影响 |
| --- | --- |
| `backend/internal/library/scanner.go:75-213` | 一个 holder 持锁执行整次扫描；根按 `path,id` 排序；500 ms 查询持久化取消；终态事务先锁扫描行；只有成功候选且没有取消意图才执行全根缺失对账。 |
| `scanner.go:216-454` | 当前每个根各自 WalkDir、CUE 对齐、身份规划、候选保存，随后清暂存；不是全局四阶段。文件标签读取没有统一可取消的 parser context。 |
| `scanner.go:599-636,770-803` | scope 每页 256；保存仍会运行 organizer，发现抓轨日志并在候选事务提交后处理封面。候选失败可能已有部分目录结果提交。 |
| `library/identity_plan.go`、`catalog/ingest/identity_plan.go` | “整理”包括候选整理、指纹计算/复用、身份配对、发行分配，不只是内存分类；身份计数是观察/配对计数，不能当报告新增/更新数。 |
| `catalog/ingest/persist.go`、`persist_track.go` | 每个候选独立短事务；既有行通常无条件 UPDATE，`RowsAffected` 不等于发生有意义的更新；新发行同时新建组。 |
| `persist_track.go:90-103` | `vacateSourceIdentity` 可在候选事务中把确认被替换、且没有其他观察沿用的旧曲目标为 missing；它不是终态 `MarkMissingSources` 的负向对账。 |
| `catalog/ingest/staging.go` | 一个 scope 上限 10,000 条观察，单条 JSON 上限 24 MiB，scope 页 256；CUE 虚拟轨数不是文件数。 |
| `scanner.go:916-918`、`scans.go` | 诊断最多保留 100 条；摘要是已记录条目数。扫描列表只给管理员，活动和单次状态给所有登录用户。 |
| `catalog/governance/claims.go`、`claim_read.go`、`claim.go`、`claim_revert.go` | 已有同文件/同目录推荐与反向提示；旧版本必须不可见并有人工状态；认领后提示消失，撤销后可能重现。 |
| `catalog/groups.go`、`catalog/internal/releasesql/release.go` | 可见性只要求至少一条 present 曲目，不额外要求 root active；代表版本与排序分离，现有组列表默认按代表版本标题、艺人、ID 排序。 |
| 迁移 `0002`、`0009`、`0011`、`0013`、`0020` | 扫描尚无进度/报告；发行有创建时间、曲目没有；staging 会清理；认领不记录新旧发行映射。不能从这些表推断可靠历史差量。 |

必须保持：来源只读、跨 root 不重绑定、治理覆盖层和 `edit_revision` 不被扫描改写、失败不触发全根负向对账、取消意图优先、holder 失效不能继续写、分页与内存有界。`database-guidelines.md:96-98` 的分批写入要求适用于新进度，不能把音频读取包在事务里。

## 2. 扫描编排、计数与单调进度

### 2.1 全根计数后保留逐根流水（D1 已裁决）

保留现有 `runScan` → 逐根 `scanRoot` 的主干，不将所有根统一拆成读取、整理、保存三个全局循环：

1. 固定本次 active roots 快照与顺序；初始化每根的计数与完整性标志。
2. **counting（数文件）**：先逐根预遍历，只数候选目录项；全部根尝试结束后固定各根 `n_i` 和合计 `T = Σ n_i`，只存每根的小型计数状态，不写全根文件 manifest 或 observation。
3. 对第 i 根完整执行现有流水：**reading_tags（读取标签）** WalkDir、解析与暂存 → **organizing（整理专辑）** CUE scope 对齐与 `PlanStagedRoot` → **saving（保存）** 分页读取已规划 scope、重建候选、抓轨日志、候选短事务、封面绑定与暂存清理。
4. 当前根完成/失败清理后才进入第 i+1 根；完整性使用该根自己的计数与正式遍历结果，不把其他根失败误传给本根；该根计数不完整时不得把 WalkComplete 传成 true。
5. 全部根处理结束后做一次全根终态事务，仍只有完整成功才对账 missing；不是每个根单独做负向对账。

先完成根的候选提交后立即可被 catalog 读取，不必等待其他根；后续根取消/失败不撤回这些已提交结果。暂存峰值维持当前“一次一个根”，根退出的兜底清理照常执行；不为进度保留已处理根的 observation。

每次进入某根的读取/整理/保存阶段都重新检查根的安全可用性；指纹与封面访问继续使用现有 containment 校验。根在阶段之间下线/变成符号链接时跳过该根后续工作并标记不完整，不能因预遍历通过就信任后续路径。

“保存”仍有少量整理、日志与封面 I/O，不能声称该阶段只写 SQL。整理中的指纹读取也不能被算作第二遍“已处理文件”。

**F1 的验收口径：总体进度单调不减；每个根内阶段按读取→整理→保存顺序推进。** 阶段标签表示当前根，允许“第 1 根保存 → 第 2 根读取”；多根时同时显示“第 i / 共 R 个目录”。全根计数结束后，正式处理的根序号从 1 开始；它与计数遍历的根序号是两个遍历过程，不能据此判定进度倒退。

### 2.2 文件计数口径

- 计数单位为 **`(root_id, 相对路径)` 候选目录项**，不是 Track、音轨、字节或内容去重后的文件。多根总数是所有根的合计 `T = Σ root_count`，不是当前根数。重叠注册根按各自扫描身份分别计入。
- 与正式扫描共用一个纯扩展名判定：`.cue`，以及 `isSupportedAudioExtension` / `isAudioCandidateExtension` 的并集；扩展名统一小写。不只数支持的音频，否则 `.ape` 等被正式处理并报错的候选会漏出分母。
- `.jpg/.png/.txt/.log` 等附件不计；一张 CUE 计一个文件，父音频若也是 WalkDir 候选再计一个；虚拟曲目不额外计数，解析 CUE 时重复读取的父音频不重复推进文件数。
- 计数只使用 WalkDir 的目录项、名字和类型；不打开文件、不读标签、不计算指纹、不逐文件 Stat。目录符号链接不遍历。扩展名匹配的文件符号链接、非普通条目先计入候选，正式扫描仍须做 containment、Stat、普通文件与解析校验，拒绝也算完成一次文件处理尝试。
- 根自身在进入前复用 `registeredRootAvailable` 的安全校验。计数遇到不可用根：该根已知数为 0、标记计数不完整、写安全诊断；遍历中权限错误：保留已经数到的下界并标记不完整，继续其他可访问部分。不伪装成“空目录”。
- **任何计数不完整，本次全局不得 succeeded 或负向对账**，即使正式遍历碰巧恢复正常；允许其他可用输入提交。避免未经确认的低估总数与不完整来源共同被当成成功。
- 在每个 root/WalkDir 回调入口和离开处理单元时检查 `ctx.Err()`；沿用独立控制连接每 500 ms 轮询取消，计数阶段也执行。500 ms 是检查周期，**不是最坏取消时延保证**：阻塞的文件系统目录读取、现有无 context 的标签解析不能被 Go 强制中断。不得为此引入可泄漏的“每目录一个 goroutine”。

### 2.3 扫描期间文件变化

计数与正式 WalkDir 不是文件系统快照；不增加全文件路径 manifest，也不承诺发现同数量的替换或途中所有变化。

- `files_total` 在全根计数结束后固定；`files_processed` 是正式扫描完成尝试的真实候选数，可以大于 T，绝不把它压成假计数。
- 正式扫描多遇到文件：照常处理，分母不变，百分比计算中的比例最多 1；文案改为“已处理 A 个文件（计数时 T 个）”，不展示 A/T 像是后端坏数据。
- 少遇到文件：保留 A<T，不补造“已处理”；正常结束读取阶段时进入整理阶段。报告同时保留计数值与正式发现值，并标记 `files_changed=true`（各根计数与处理数不同）。数量相同只表示未从计数发现变化，不代表内容没变。
- 文件被 WalkDir 看到后消失/无法读取：计入处理尝试，按原逻辑记诊断、root 不完整，禁止全局对账。计数后、正式遍历前删除且正式遍历完整：按现有成功扫描规则处理，终态可以成功并对账；提示总量发生变化即可，不能把所有真实删除都变成不可成功。
- 处理中取消的单个候选未完成则不加 processed；已完成但解析失败、格式不支持、安全校验拒绝均加一次。路径错误发生在扩展名过滤之前的非候选诊断不算文件。
- 计数阶段取消时 `files_total=null`，只保留 `files_counted` 下界；不发布一个看似固定的全库分母。

### 2.4 按根文件数加权的总体进度

总体为**工作阶段进度**，不是耗时百分比；不估 ETA，不按时间自增。持久化整数基点 `percent_bp`，UI 展示 `floor(percent_bp / 100)`；所有 running 与非成功终态最多 9,900，成功才是 10,000。

全根计数占 0–500 基点：已结束计数尝试的根数为 C、根快照数为 R 时，`P_count = floor(500 × C/R)`。当前根内只增加 `files_counted`，不虚构目录内百分比。计数结束后固定 `n_i`、T 与权重；后续文件变化不重新分配份额。

当 T>0，第 i 根权重 `w_i = n_i/T`，根内完成比例为 `u_i`，前面已处理完或已明确失败跳过的根权重和为 `B_i = Σ(j<i) w_j`。正式处理总体进度：

```text
P = 500 + floor(9400 × (B_i + w_i × u_i))
percent_bp = max(此前持久值, min(P, 9900))
```

使用溢出安全的定点/有理数运算，仅在最终基点取整，不对每根单独取整再累加；不能用不受检验的 int64 连乘计算安全整数上限附近的文件量。

| 阶段 | `completed / total` | 单位 | 根内 u_i 与推进 |
| --- | --- | --- | --- |
| counting | 已结束计数尝试的根 C / R | `roots` | 全局独立预阶段，用 P_count，不使用根内权重。 |
| reading_tags | 当前根已完成文件尝试 A_i / 预计数 n_i | `files` | `0.60 × min(A_i/n_i,1)`；正常结束该阶段后进入 0.60。 |
| organizing | 当前根身份规划尝试已结束的标志（0 或 1）/ 1 | `roots` | `0.60 + 0.20 × completed`；规划、指纹与 SQL 过程中可停留，不能假造细粒度进度。 |
| saving | 当前根已完成保存尝试的 scope 数 / 当前根固定可保存 scope 数 S_i | `scopes` | `0.80 + 0.20 × completed/S_i`；同一 scope 下全部候选与封面尝试结束才加一。 |
| 全根终态提交 | 无额外工作单位，phase 仍 saving | — | 全部根处理完到 9,900；全根对账与 succeeded 同事务提交才到 10,000。 |

- 根内进入下一阶段时推进至对应区间起点；第 i 根结束时 `u_i=1`，第 i+1 根从 `u_(i+1)=0` 开始，两者总体边界严格相等，阶段标签变成读取不代表总体重置。数据库按快照序号防迟到覆盖（§3.2），**不能按 phase 枚举全局只增来过滤更新**。
- 权重示例：两根计数为 100 和 300，第一根保存结束 P=2,850；第二根开始读取仍为 2,850，第二根读到一半 P=4,965，全部根处理完 P=9,900。最后成功提交才是 10,000。
- T>0 且某根 n_i=0：该根权重为 0，仍照常检查与处理，展示其阶段/目录序号但不额外抬高百分比；若预计数后新增文件，真实 processed 增长并标记 changed，但不改权重。
- T=0 且 R>0：没有可用文件比例，固定采用 `w_i=1/R` 的等根权重作为零总数退化规则；仍需验证根、整理/清理与终态，不把“计数为零”当成功。R=0 时直接跳过工作阶段到终态流程，不做除法。
- n_i=0 的读取、S_i=0 的保存均不做除法：阶段入口比例分别为 0、0.80，只有该阶段实际结束才推进到 0.60、1。计数不完整的根仍按既有不完整规则处理，不能靠零分母变成成功。
- 当前根多发现文件时截断的是 `A_i/n_i` 的**计算比例**，不是 A_i；少发现时保留 A_i<n_i，在读取实际结束时进入整理。`files_processed = Σ 已完成文件尝试` 始终为全局累计，不等于当前阶段 completed。
- `files_changed` 在某根处理数超过 n_i 时立即置 true；少于 n_i 只在该根正式遍历结束后比较，不能把“还没扫完”误判为文件减少。一旦置 true 不回退；取消的未完成根不凭不足数量推断文件变化。
- 失败 root / scope 的“完成尝试”可推进工作进度，但不改变完整性结果；明确失败并跳过剩余工作的根在退出时结算该根权重，不能增加虚构文件数。因取消尚未完成/尚未进入的工作不结算，保留停下的位置。
- S_i 在**当前根** organizing 结束后查询 `planned AND NOT withheld` 的不同 scope，固定到该根保存结束；失败/暂缓的 scope 不进入分母。不提前等所有根整理完。
- `completed` 与 `total` 是当前根当前阶段的分子分母（counting 除外），切阶段/换根可重置；全局 percent_bp 和 files_processed 不倒退。界面同时区分“累计已处理 A / T 个文件”和“当前目录阶段”，不把局部分子当全库计数。
- phase 仅四个枚举；成功不是第五阶段。失败/取消/不完整冻结最后的根、phase、计数与百分比，重复取消及恢复不抬升它们。最终全根提交时无当前根，root_progress.index 为 null，文案为“保存扫描结果”。

## 3. 持久化与故障边界

### 3.1 数据结构（候选列名）

新增迁移只命名为 **`scan_feedback.sql`**、**`catalog_recent_additions.sql`**，不预分配编号。以下是设计字段，不是本轮要生成的 SQL。

**`scan_runs` 扩展**，由 library 拥有：

- `feedback_version SMALLINT NULL`：旧行为空，新版启动显式写 1；不要给历史行默认 1。
- `phase TEXT NULL`、`progress_bp INTEGER NULL`、`phase_completed BIGINT NULL`、`phase_total BIGINT NULL`、`progress_updated_at TIMESTAMPTZ NULL`、`progress_seq BIGINT NULL`；seq 是 worker 递增的反馈快照序号，用于拒绝旧快照，不暴露为业务版本号。
- `files_counted BIGINT NULL`、`files_total BIGINT NULL`、`files_processed BIGINT NULL`、`count_complete BOOLEAN NULL`、`files_changed BOOLEAN NULL`。
- `roots_total INTEGER NULL`、`current_root_index INTEGER NULL`、`current_root_id UUID NULL`、`current_root_name TEXT NULL`；名称为启动时 basename 安全显示快照，不保存新的绝对路径副本。无当前根时后三项为空，序号为 1 起。
- `report_state TEXT NULL`：新扫描初值 `pending`；正常收口后 `ready | partial | unavailable`。旧记录通过 version 为空投影为 `legacy`，无需回填。
- 约束：计数非负且不超过 JavaScript 安全整数 `9,007,199,254,740,991`；百分比 0–10,000；新记录非 succeeded 不允许 10,000；四阶段枚举白名单、root index 不超过 total。计数溢出视为反馈不可用，不截断成另一个真实数字。

**`scan_reports`**，由 library 拥有，一次扫描至多一行：`scan_run_id` 主键/外键、`schema_version`、`finalized_at`、文件计数、`albums_added/updated/missing`、`tracks_added/updated/missing`、`diagnostics_recorded`、`diagnostics_may_be_truncated`、`missing_reconciled`。列均有类型和非负约束，不把核心统计塞进 JSONB。

**`scan_catalog_changes`**，由 catalog/ingest 拥有的扫描变更记录：

- 主键 `(scan_run_id, entity_kind, entity_id)`，kind 为 `track | release | album`；扫描外键级联清理，实体 UUID 不做删除级联，避免将来实体维护悄悄改写历史报告。
- `existed_before`、`present_before`、`present_after`、`change_mask`（元数据、位置、归属、可用性、封面等有名常量）、`first_changed_at`、`last_changed_at`、`classification`。
- 本轮首次触及时的前态不覆盖，后续只合并变化原因、更新扫描操作造成的后态。仅有 `observed_at` 变化不生成变更行。
- 报告收口后 classification 冻结；再扫使用另一个 scan ID。该表不是新通用事件溯源系统，不复制歌词全文、封面或无限 track 数组。

**`scan_added_releases`**，由 catalog/ingest 拥有：主键 `(scan_run_id, release_id)`，列 `group_id_at_creation`、`added_at`、`title_at_creation`、`artist_at_creation`。在新发行的候选事务里记录，不能用时间窗补查。索引 `(scan_run_id, added_at DESC, release_id DESC)`。

新增专辑列表按该表的 `group_id_at_creation` 去重（当前一新发行一新组）；报告项保留其新发行 ID 作为后续实时跳转与认领提示的依据。分页默认 20、最大 100，不在一条 JSON 或内存 slice 中塞完整新专辑列表。

### 3.2 进度分批写与单写者

- 内存仅存累计计数、各根固定 n_i/完整性小型状态、已结算根权重与一个待提交快照；不用逐文件队列或全根 observation。无需重启续跑，因此不为全部根权重另建恢复表；崩溃后直接保留最后持久化百分比与当前根，不重算。
- 同一 worker、同一个 scan holder 串行写进度，不另启使用 holder 的后台 SQL goroutine，不与其事务/Rows 并发。
- 在安全边界，累计 **256 个文件/工作单元**或距上次尝试 **1 秒**，任一满足即刷新；阶段切换、root 切换与终态前强制刷新。一次 SQL 只更新一条 scan_runs，不逐文件写。计数阶段同样按候选数触发刷新。
- 不把 1 秒说成硬实时 SLA：长文件/长 SQL 中不能插入同 holder 更新；下一安全边界再写。前端 2/5 秒轮询允许看到暂时不变的值。
- 进度写使用短超时（建议 500 ms）；普通单条写失败只记限频结构化警告并保留最新内存快照，下批重试，不把扫描改成失败、不制造诊断洪水。连续失败仍按时间节流，不能每个文件重试一次。
- SQL 条件 `status='running' AND feedback_version=1 AND progress_seq < incoming_seq`；每次形成新快照递增 seq，失败后的最新快照沿用其更大序号，初始化为 0。百分比与全局累计文件数取较大值；phase、当前根与阶段分子分母原子替换为同一个新快照，不取逐列 MAX。允许新根 saving→reading_tags，以及 counting 的最后一根→正式读取第一根；拒绝更旧 seq 的迟到写。终态以后任何刷新影响 0 行。
- 进度写不推进 catalog revision，也不参与身份判断或 missing 判断。

### 3.3 报告准确性与“写失败不影响扫描结果”

**D3 已裁决：反馈写失败不改变核心扫描结果，允许显示“这次的报告不可用”；不要求在反馈丢失或崩溃后仍提供完整报告，也不返回貌似准确的零。**

1. 正常路径在候选事务内读取有界前态、执行原有 catalog 变化，再在同一事务写变更记录/新发行关联；提交成功才可计数，回滚不能留下报告变化。每次批量记录最多 256 行，大候选分块写，候选事务上限仍由现有 10,000 观察上限约束。
2. 报告专用的前态读取和追加记录各用短 `SAVEPOINT` 隔离；反馈 SQL 失败先回滚到 savepoint，再继续原 catalog 工作。不能吞掉 PostgreSQL aborted transaction 后继续提交。失败时本次 worker 永久标记 `reportBroken=true`，即使下一次成功也不能冒充全量报告。
3. 正常终态在既有 finalize 事务内，以 savepoint 尝试生成/冻结报告与新列表；失败则回滚**报告部分**，仍提交原来的业务对账与扫描终态，`report_state=unavailable`。`ready` 只给成功且记账完整的扫描；正常收口的 canceled/failed/incomplete 且记账完整为 `partial`，表示“已经提交的变更”，不是完整扫描结果。
4. 终态核心 UPDATE 本来就必须可靠；将有效 report_state 与终态一起写。若新增反馈列的更新本身失败，回滚该局部尝试，按原字段写终态；读取时“终态 + pending”明确投影为 unavailable，不能延迟补成 ready。若最后进度快照写失败但核心 succeeded 已提交，后端 presenter 以持久化 succeeded 为已完成全部工作的权威，投影 saving/100%（更新时间取 finished_at）；非成功仍只能返回最后已保存快照。前端不能自行推算这个补偿。
5. 崩溃丢失内存健康标志时，`RecoverInterruptedScans` 以及 `StartScan` 内的遗留恢复都把本次报告设为 unavailable；不靠零散记录推断完整性、不补跑负向对账。保留已经持久化的最后进度，管理员知道报告未能完成。
6. holder 丢失、数据库断连或核心 catalog 写失败仍服从既有协调/业务失败规则；“反馈写失败不改变结果”不等于可以在数据库不可用时继续写或把失败改成功。

报告与进度分开健康判断：一次进度刷新失败不使报告失效；一次变更记账失败不阻止后续进度。诊断写失败沿用原来的完整性行为，不在本任务顺带改掉。

### 3.4 终态冻结、清理与资源

- 成功：持久化取消检查 → 全根 missing → 报告聚合/冻结 → succeeded 与 100%，同一个 holder 事务；报告失败按上文降级，不拆开 missing 与业务终态提交。
- 非成功：不执行全根 missing；flush 最后可得进度，冻结在原百分比；可用报告只包含已经提交的变更。
- 维持逐根清理：当前根开始前清理其旧 staging，当前根退出（含失败/取消）的 defer 清理其所有 staging，完成清理后再进入下一根。计数不写 observation，尚未正式进入的根没有新暂存；不把已完成根的 observation 留到全局终态。清理失败仍按现有合同使扫描 incomplete，不假成功；无法确认清理完成时停止推进后续根，避免累积多根暂存。不得删除其他活扫描的 staging。
- 恢复清理必须先取得原协调锁，仅清理已转终态的本扫描暂存。变更记录与报告不是 staging，不跟着逐根清掉。
- 不设置自动删除报告的保留期；本项不新增历史清除接口。按扫描分页读取，磁盘增长需纳入运行观察。

## 4. 报告语义：不是“执行了多少次 UPDATE”

### 4.1 文件与问题

- `files.counted` / `files.total`：计数下界与固定分母；`files.processed`：正式扫描实际完成的候选尝试。成功的完整正式遍历里 processed 即“发现并处理的文件数”；非成功明确写“已处理”，不是完整发现量。
- 不承诺文件数等于新增+更新+不变曲目数：CUE 一文件多轨、重复物理/虚拟观察消除、不支持格式都使两者不同。
- “问题”指本扫描已记录的 **scan_diagnostics 条目**，不是全库 attention，也不是受治理的待核对问题总数。冻结 `COUNT(*)`，最多 100。
- `diagnostics_may_be_truncated = recorded >= 100`；到上限显示“至少 100 条（仅保留前 100 条）”。当前身份规划自身也有诊断上限，因此不能声称已知真实完整问题数。本项不为了精确统计全部问题而放宽诊断上限。

### 4.2 曲目：按 Track UUID，单次扫描互斥分类

| 分类 | 可实现定义 |
| --- | --- |
| 新增 | 本扫描候选事务实际 INSERT 的 Track UUID；不是路径首次看到，也不是恢复 present。 |
| 更新 | 既有 UUID 发生下述有意义的变化，且最终没有归为消失；同一曲目多次变动只计一次。 |
| 消失 | 扫描操作导致既有曲目从首次触及时的 present 变为最终 missing；原本已经 missing 的行不能因重复 UPDATE 再计消失。包含成功终态对账及 `vacateSourceIdentity` 的已确认内容替换。 |

更新比较正规化后的业务投影：标题、艺人、曲序/碟序、实际所属 Release、音频参数/时长、CUE 区间与来源类型、歌词及其来源、按 canonical 顺序的署名，以及安全来源位置。恢复 missing→present 计更新（原因 `restored`）。

排除：`observed_at`、`scan_run_id`、证据观察时间、文件 mtime/size 单独变化、未知指纹回填、只重新生成相同证据，以及无意义的中间 Medium UUID 变换。首次有有效指纹不算新增音乐；音频已知指纹不同仍由既有身份规则生成**新曲目+旧曲目消失**，不能把它误报成旧曲目更新。

优先级：新增 > 最终消失 > 更新。若同一次扫描新建又变不可用仍归新增，另外保留其状态供管理员理解，不重复计进消失。报告不是目录净增减恒等式。

### 4.3 专辑：按 ReleaseGroup，而不是发行数量

- `albums.added`：本扫描新建发行对应的 `group_id_at_creation` 去重数；当前实现每个新发行创建独立组，因而与新发行数相同，但 wire 不把两者永久绑定。
- `albums.missing`：扫描自己的曲目迁移/失效导致既有组从有可见发行变成完全不可见；组行保留，不叫“删除”。如果组里还有另一可见版本，不计整张消失。
- `albums.updated`：既有组中发生有意义的发行扫描字段、曲目内容/顺序/归属/位置/可用性、成员可见性、扫描封面变化，且不归为新增或最终消失。一次多个版本/曲目变化只算一张。
- 恢复既有不可见组计更新（`restored`），不算刚加入；新增发行造成旧发行的曲目全部迁走时，新组新增、旧组可能消失、迁走的旧 Track 是更新，不是 Track 新增或消失。
- 分类优先级同曲目。旧组出现部分曲目消失而仍可见时，专辑计更新、曲目计消失。
- “元数据变化”比较 **扫描拥有的基础字段**，不读修正/核对/默认版本/操作记录来判定更新；即使有效显示值被人工修正遮住，也应说明“扫描资料已更新”。管理员自己修正、认领、归组不计本次扫描更新。
- 报告是“本次扫描已提交的变更”归集，不是两个时间点全库快照的相减。其他管理员在扫描期间的治理操作不记入扫描事件；历史计数不能随后来认领而变化。

### 4.4 捕获变更的具体落点与并发

不能只改 `PersistCandidate` 返回几个数字：还必须覆盖旧曲目腾位、旧发行被抽空、封面后置事务、终态缺失对账。

1. `ingest/persist.go` / `persist_track.go`：先读取本候选计划引用的旧曲目、拟腾出的来源占有者、它们的原发行与组，以及目标发行；保留有界结构化前态；核心写后比较投影。`vacateSourceIdentity` 捕获真实 present→missing 及旧归属，不把所有 UPDATE 都算消失。
2. 记录 album 的扫描变更前后可见性；如果曲目改挂另一个 Release，要同时触及原发行/组与目标发行/组，不能只数返回的 releaseID。无改变的重扫应得到全零报告。
3. 为防止归组与认领穿插导致组计数读到撕裂状态，候选事务先收集全部将受影响的既有发行 ID，按 UUID 升序锁发行，再读取其当前组并按 UUID 升序对组取共享行锁；**所有发行锁在组锁之前**，组锁之后不再临时发现并锁新发行。需新增目标/腾位对象时在加组锁前完成集合计算；集合变化应重试该短事务而不是逆序补锁。
4. 这比当前“扫描不锁组行”增加了读取锁，因此实现时必须补治理并发回归并更新对应锁合同；不改组的 default/revision，不访问治理覆盖表。锁只覆盖数据库短事务，不能跨指纹、封面文件读取。
5. 封面在候选提交后处理：在 `artwork` 的绑定/清除数据库事务里比较旧新资源标识，只有实际变化才追加发行/组的 `artwork` 原因；文件读取在事务外。失败时保留已提交的候选变更，扫描按原规则 incomplete。
6. `MarkMissingSources` 改为只处理真正 `present` 且未观察的行，在 finalize 的同一事务中分块读取/更新/记账；涉及的发行、组按上述顺序先锁好，不能边更新曲目边逆序补发行锁。大量 ID 在 SQL/暂存侧组织，行结果每批最多 256，不把全库 `RETURNING` 拉进内存。全部根仍只有一次提交。
7. `scan_catalog_changes` 对同一实体只保留扫描首次前态与最后一次扫描后态、原因并集。同时记录是否真实新增，聚合时按 §4.2–4.3 分类。纯治理变化不调用记录器，冻结时不通过当前 `group_id` 重新归桶。

新增 catalog 公开的类型化反馈入口（如 `SummarizeScanChanges`、`ListScanAddedAlbums`），library 不直接查询 catalog 私有表；报告 SQL 不放 HTTP handler。新反馈 SQL 使用 scanID 并在同一事务运行，不通过全局 callback 或无限事件总线广播。

### 4.5 新增专辑列表与“可能接替了……”

- 历史来源是 `scan_added_releases`；列表里的 `group_id_at_scan`、首次标题/艺人、加入时间固定，不因之后人工归组或认领而丢项、去重合并或重计历史数量。
- 读取时批量补当前可见状态、当前 group ID 与当前专辑卡片摘要；当前不可见时摘要为空，保留历史文字与“当前不可见”，不提供会误导用户的播放入口。
- **认领标记实时计算，不在扫描事务中查治理表或冻结推荐。** 在管理员请求这一页报告时，由 catalog/governance 的公开批量读取入口复用 `releaseClaimablePredicate`、`claimPathSetFor` 与 `LoadClaimHints` 的条件，为当前仍可见的新发行返回最多 3 个 `{id,title}`。
- 条件：旧版本不可见且至少有一类人工状态；同 root 下旧曲目路径+最后归组证据的路径命中任意新 present 曲目，或相对目录相同。不是音频指纹相同、标题相似，也不是必须同时命中两个理由。
- 反向提示沿用现有合同：不受旧版本详情“推荐前 5 个”限制。批量取当前页，按新发行分区限制 3 个，不能按一页 100 项执行 100 次详情查询。
- 提示写“可能接替了《…》”，链接已有 `/claims?release=<旧版本UUID>&target=<本次新发行UUID>` 认领流程（`app/router.tsx` 与 `claims-search.ts` 已支持）；前端可复用现有提示导航，不自行创建认领命令。正式请求仍要重新取预览、revision 与令牌。
- 旧版本被认领后失去人工状态，下一次读取提示消失；撤销后满足条件则重现；旧版文件回来后可见也消失。历史新增计数/项目不变，链接用**当前** group ID。
- 普通用户不返回这些旧版本标题、路径或提示。提示读取故障不能伪装成“没有候选”，报告页可以局部显示不可用，或该报告请求安全 503；MVP 推荐后者以简化一次读取合同。

## 5. 最近新增与专辑库排序

### 5.1 加入时间定义

D4 已裁决：`album.added_at = MIN(releases.created_at)`，只取**当前组内可见发行**；它是 catalog 读取投影列，不新增一个易陈旧的 `release_groups.added_at` 实体列。

- 新组首次有音乐时自然有值；既有组多加一个新版本不会把整张专辑顶到最新。
- 老版本不可见、只剩新版本，最早可见创建时间可能变新；归组、认领和撤销也可能改变该值。这是“当前可见专辑的加入时间”，不是组 UUID 的诞生时间，也不是最近一次扫描时间。
- 恢复旧文件使用原发行创建时间，不当新专辑顶上来；旧系统已有发行也参加最近新增，不受“旧扫描无报告”限制。
- 可见性必须复用 `ReleaseVisiblePredicate`；禁用 root 不是本项另加的隐藏条件。卡片代表版本仍服从默认版本/年份规则，不能把最新发行强行设为代表版本。

主会话已接受上述动态加入时间语义；本项不另存“首次加入永不变化”的历史事件，也不以 group.created_at 代替。

### 5.2 接口与查询

- 新增 `GET /api/v1/release-groups/recently-added`，所有登录用户；固定最多 8 项，响应 `{items: ReleaseGroupSummaryWithAddedAt[]}`，无分页参数和任意 limit；空数组时概览整块隐藏。
- 扩展 `GET /api/v1/release-groups?...&sort=added_at_desc`；允许 `title`（默认，省略与显式等价）、`added_at_desc`。排序分别为现有标题顺序，或 `added_at DESC, group_id DESC`，保证同时间稳定翻页。未知值、空值、重复参数均 `400 invalid_sort`。
- `q`、`attention`、page/page_size 原合同不变；先从组所有可见成员计算 added_at，再判断是否有任意成员命中搜索，不能用搜索命中的那一个发行时间代替组加入时间。
- “查看全部”到 `/albums?sort=added_at_desc`；换排序回第一页，其他筛选保留。
- 为组摘要/详情新增 `added_at` RFC 3339 字段，recent 与普通列表共用投影；不是取 `representative.created_at`。新字段可能影响收藏等复用组摘要的 API，见 §9。
- `/releases` 暂不增加排序功能；共用参数解析器的 q/attention/分页仍共用，sort 在组接口单独解析。必须修订“组接口参数与 /releases 完全相同”的旧合同为共同参数一致、组另有 sort。
- 用窗口 `MIN(created_at) OVER (PARTITION BY group_id)` 合入现有 ranked CTE；最近 8 项可复用组列表的排序/摘要 SQL，但不执行无意义的全量 COUNT。
- 建候选索引 `releases(group_id, created_at, id)`，支撑按组时间与成员读取；已有 `media(release_id,position)`、`tracks(medium_id,position)` 支撑可见性。隔离数据 `EXPLAIN (ANALYZE, BUFFERS)` 验证后，可增加 `tracks(medium_id) WHERE source_status='present'`，不先堆叠重复索引。
- 普通索引不能直接覆盖跨表可见性和组级 MIN 的全局排序；此方案仍需聚合可见组，不能宣称 LIMIT 8 就只读 8 行。先测规模与排序内存，若不可接受再单独评估可重建物化投影，不在本项偷偷引入异步缓存或物化状态。

## 6. REST 合同与向后兼容

### 6.1 ScanDTO 的增量字段

原七个字段及五种 status 不变，增加如下形状（示意值，不是生成后的样本）：

```json
{
  "progress": {
    "version": 1,
    "phase": "reading_tags",
    "percent_bp": 4260,
    "completed": 600,
    "total": 1200,
    "unit": "files",
    "files_counted": 2400,
    "files_total": 2400,
    "files_processed": 1200,
    "count_complete": true,
    "files_changed": false,
    "updated_at": "2026-11-04T10:00:00Z"
  },
  "root_progress": {
    "index": 2,
    "total": 3,
    "id": "00000000-0000-4000-8000-000000000002",
    "name": "测试音乐"
  },
  "report_status": "pending"
}
```

- `progress: null` 表示历史扫描或尚无有效反馈快照；新扫描初始化时应提供 counting/0 快照，不靠前端补初值。计数尚未完成时 files_total 为 null；当前阶段 total 依单位决定。
- `progress`、`report_status` 和 `root_progress` 的数字 `index/total` 对所有登录用户可见；多根时概览与详情均能显示“第 i / 共 R 个目录”，说明当前根的阶段。根的 `id/name` 仅管理员响应包含，普通成员省略这两个键；无当前根时 index 为 null，管理员 id/name 也为 null；历史扫描 root_progress 为 null。任何角色都不返回绝对路径或当前文件路径。此处公开目录序号、仍限制身份/名称，落实 D1 的多根展示而不开放目录资源查询。
- 上例管理员响应对应三个根计数 600/1200/600，第一根已完成、第二根已读取 600；所以全局 files_processed=1200，局部 completed=600、total=1200、总体 42.60%。普通成员同一快照仅去掉 root_progress.id/name，不改变计算值。
- 所有创建、复用、列表、详情、active、取消响应统一经过带权限上下文的 presenter；不能在 `scanStatus` 单独脱敏后，让 `activeScan` 漏掉。服务内部值和 wire presenter 分开；列表项与同角色详情形状一致。
- `report_status` 枚举 `legacy | pending | ready | partial | unavailable`；pending 只用于 running，终态 pending 数据投影成 unavailable。终态 ready 只对应 succeeded；partial 对正常收口的非成功终态。
- 100% 只意味着核心扫描 succeeded，报告可能 unavailable；界面同时展示真实报告状态。未知 phase/status 不能强转或默认成成功。

### 6.2 管理员报告接口

`GET /api/v1/scans/{id}/report?page=1&page_size=20`。

响应形状：

```text
{
  scan_run_id, schema_version: 1,
  status: ready | partial,
  scan_status, finalized_at, missing_reconciled,
  files: { counted, total: integer|null, processed, count_complete, changed },
  albums: { added, updated, missing },
  tracks: { added, updated, missing },
  issues: { recorded, may_be_truncated },
  added_albums: {
    items: [{
      group_id_at_scan, added_at, title_at_scan, artist_at_scan,
      release_id, current_group_id: UUID|null,
      album: ReleaseGroupSummaryWithAddedAt|null,
      claim_hints: [{ id, title }]
    }],
    pagination: { page, page_size, total }
  }
}
```

当前实现每个新增组只有一个新发行，故每项一个 `release_id`；如未来允许扫描把多个新发行放同组，必须版本化此关联投影，不能默默丢失其他发行的提示。历史字段与动态字段明确区分：计数、快照标题/组/时间冻结；album、current_group_id、claim_hints 随当前目录状态更新。分页以历史项排序，不按当前可见项过滤，total 与冻结 added 一致。

读取报告表、变更列表页与动态投影使用只读一致性快照（短 REPEATABLE READ 事务，所有 store 经同一个 queryer）；不持有任何文件 I/O。避免一次响应刚读完可见性就遇到认领而组/提示互相矛盾。匿名/普通成员在查询报告存在性之前拒绝。

### 6.3 错误矩阵

| 情形 | 响应 |
| --- | --- |
| 未登录 | 401，复用 Guard 的 `unauthorized`。 |
| 非管理员读报告 | 403 `forbidden`。 |
| 非法 scan UUID | 400 `invalid_id`；鉴权先于 ID。 |
| 报告分页非法 | 400 `invalid_pagination`，页最大 100，沿用偏移溢出检查。 |
| 未知扫描 | 404 `not_found`。 |
| 旧扫描无报告 | 404 `scan_report_not_available`；ScanDTO 已给 legacy，正常 UI 不发此请求。 |
| 新扫描仍 running | 409 `scan_report_not_ready`；正常 UI 等终态后再发。 |
| 报告记录失败或崩溃恢复 | 503 `scan_report_unavailable`；提示“扫描结果已保留，报告未能生成”，不是整次扫描失败。 |
| 报告读取数据库故障 | 503 `database_unavailable`；安全 envelope，不回显 SQL/路径。 |
| recently-added 或组列表非法 sort | 组列表 400 `invalid_sort`；recent 若带 sort/limit/page/page_size 等非合同参数则 400 `invalid_query`，固定排序与 8 项上限。 |
| 最近新增查询故障 | 503 `database_unavailable`，不能当空数组隐藏故障。 |

所有错误沿用 `error-handling.md` 的 request_id 与分类；原 scan 启动/复用/取消状态码及 `scan_coordination_unavailable` 不变。

### 6.4 部署兼容

- 仅追加字段和路由、不改既有 status。旧前端 object decoder 忽略未知键，因此可继续读旧字段；新版 decoder 对过渡期缺失的 progress/root_progress/report_status/added_at 做有合同的 optional 处理（缺失表示旧服务能力，不伪造进度）。新服务的真实样本要求这些应有字段被保留并往返一致。
- 旧扫描返回 `progress:null`、`report_status:legacy`，root_progress 为 null；不补算报告，不用 started_at/finished_at 去重建“本次新增”。
- 服务与内嵌前端同批发布为主；旧后端不认识新 sort 可能忽略它，不能向用户显示一个实际上仍按标题排列的“最近新增”。新版前端在缺少 added_at 合同或新增路由不存在时隐藏新入口，部署混用不作为成功验收。
- 扩展迁移不能改写旧迁移 checksum；不做破坏性的 down。回退关闭新入口、使用可识别既有扫描锁的版本；新数据保留。新旧扫描器同时运行不保证都写报告，version 为空必须按无报告处理。

## 7. 前端接入合同（供前端 agent 使用）

### 7.1 DTO、轮询与缓存

- DTO/decoder 位于 `features/library/api`；最近新增及 added_at/sort 位于 `features/catalog/api`。只在边界解码 unknown；整数安全范围、nullable、四阶段单位映射、百分比上限、数组上限与角色省略字段都校验。
- 沿用概览/活动扫描 **5 秒**、详情 **2 秒**。running（含取消请求中）才轮询；页面后台暂停；重新可见刷新；终态停止。没有活动任务时不新增无限轮询，不引入 SSE/WebSocket。
- 当前 `use-active-scan.ts` 只让历史失效，需要补全：已知 active ID 消失/更换，以及 `useScan` 从 running 到任一终态，都让 catalog 专辑列表、组/发行详情、totals、recently-added 失效；非成功也可能有已提交变更，不能只在 succeeded 失效。
- 查询键继续带 `viewer` 代次：
  - `libraryKeys.scanReport(generation, scanId, page, pageSize)`，另设该 scan 的报告前缀以失效全部页；仅管理员且 ready/partial 时 enabled。
  - `catalogKeys.recentlyAdded(generation)` 放在 `catalogKeys.all` 下。
  - `catalogKeys.albumPage` 加正规化 sort，省略与 title 归同键；不得让最近新增与标题页复用缓存。
- 为使已扫完根的专辑更早出现在已打开的页面，沿用现有 5 s/2 s 轮询响应：观察到非 counting 阶段的根序号从 i 切到更大序号时，按根边界失效 catalog 列表、totals 与 recently-added；计数阶段的根切换不失效，最终根由终态路径失效。跳过轮询间隔内的多个根时一次失效即可，不逐文件刷新，不加新轮询器。catalog 已提交数据可立即读取，页面显示受现有轮询频率约束。
- 保留 diagnostics 到终态自动刷新一次、history 全页失效。report 不自己周期轮询，只在终态/手动重试/重新聚焦或治理完成时取数。
- 认领/撤销/归组改变报告中的动态链接与提示：除原 catalog、governance 失效外，让已缓存报告前缀失效；不改历史计数。退出/切账号清代次缓存，普通用户不创建报告或管理员目录资源查询。
- 概览新增列表成功且空时隐藏；加载与错误沿用 QueryContent，不能把错误伪装成没有新增。卡片复用 AlbumCard，最多 8 项。
- 无活动任务时不轮询意味着另一个管理员开始/结束的极短扫描可能在当前标签页没被看到；沿用窗口聚焦刷新/手动刷新，不为本项暗加全局扫描订阅。接受的实时性边界需在验收记录说明。

### 7.2 进度条无障碍

- 复用 library feature 的 `ScanProgress`，概览与管理详情同源；`role="progressbar"`、可访问名称“扫描进度”、`aria-valuemin=0`、`aria-valuemax=100`、`aria-valuenow=floor(percent_bp/100)`。
- `aria-valuetext` 包含当前根、阶段和全局文件累计，如“第 2 / 共 3 个目录，读取标签，累计已处理 1,200 / 2,400 个文件，整体进度 42%”；额外展示局部计数时明确“当前目录 600 / 1,200”。计数阶段用“正在数文件，已检查 1 / 3 个目录”；不要把根单位读成文件单位。目录序号所有成员可见，目录名称只在管理员详情展示。
- progress 为 null 时不用伪百分比；可显示无确定值的 progressbar，但省略 aria-valuenow，并说明“正在准备”或历史没有进度数据。
- 取消请求中仍显示进度与“取消请求中”，不能立即变为 canceled；失败/取消/不完整用状态文字和图标，停住数值，不动画补满。
- live region 只礼貌播报 `(计数/正式处理, 根序号, 阶段)` 的变化与终态；新根与旧根阶段相同也应播报目录切换，不逐文件/每次轮询读数字。该进度条是只读展示，不做 slider、不加键盘调整动作、不抢焦点。

### 7.3 后续必须改写的 spec / DESIGN 条目

以下是本次逐项检索出的**扫描相关“没有百分比”条目**；本轮仅列清单，不修改。

| 文件与行 | 当前文字/相关合同 | 实施时调整 |
| --- | --- | --- |
| `.trellis/spec/frontend/state-management.md:38-39` | “当前没有已扫描文件数或百分比进度” | 改为新增持久化合同、旧扫描无进度、诊断不是进度。 |
| 同文件 `:53-60,72-89` | 5 s/2 s、终态失效、专辑 URL/查询键 | 保留频率，补报告/recent/专辑失效、sort 与翻页规范化。 |
| `.trellis/spec/frontend/component-guidelines.md:49` | “不显示百分比或剩余时间” | 改为仅服务端确定进度可显示百分比；ETA 仍不做。 |
| 同文件 `:117-119,163-164` | “诊断计数不是百分比进度”、节制播报 | 保留禁止诊断推算；补 progressbar、当前根阶段与终态读屏，允许跨根阶段切换。 |
| `.trellis/spec/frontend/type-safety.md:65-67` | “当前没有已扫描文件数或百分比进度” | 新 DTO、optional/null、阶段枚举与数值范围；样本往返。 |
| `frontend/DESIGN-components.md:16` | “没有总量与进度合同就不用百分比…” | 记录本项建立合同，可显示真实加权工作进度，仍无 ETA。 |
| 同文件 `:69-70` | 运行/取消请求与四种终态 | 补停下时冻结、报告 partial/unavailable；区分全根对账与已确认替换。 |
| 同文件 `:133` | 概览不推算新增统计，缺的区块隐藏 | 增加真实最近新增 API，不生成“本周新增”等新统计。 |
| 同文件 `:141` | “无进度百分比…或漏斗报告合同” | 去掉已建立合同的禁令，增加管理员报告；暂停/定时/监听仍不支持。 |
| `frontend/DESIGN-acceptance.md:18` | “当前扫描 DTO 无相应进度合同” | 改为真实计数/单调/终态验收，继续禁止动画计时或诊断代替进度。 |
| 同文件 `:19` | 暂停/继续、每日增量、监听未支持 | **保留限制**，不要因上一行改写而把这些一起开放。 |
| 同文件 `:45` | 非确定进度不暴露伪百分比 | 保留；补只读扫描进度不适用播放 slider 的键盘调节要求。 |

后端也须同步 `scan-coordination-contracts.md`（DTO/终态/恢复）、`library-scan-contracts.md`（全根预计数、逐根流水/staging/替换与对账）、`database-guidelines.md`（批写/新表/锁）、`catalog-rest-contracts.md`（排序/加入时间）、`claim-contracts.md`（批量提示及扫描组读锁）、`governance-contracts.md`（锁合同指向）、错误合同与前端验证分层。现有“诊断不是进度”“不允许伪造百分比”的原则不应删除。

## 8. 最小测试与真实服务验证计划

本轮只做文档，不执行以下测试、不启动 Docker、不读取用户音乐。实施时所有数据库验证只能使用隔离 `roomusic-test`。

### 8.1 单元与隔离 PostgreSQL 场景

| 场景 | 必须核对 |
| --- | --- |
| 计数过滤 | 大小写、支持/不支持音频候选、CUE+父文件、附件、符号链接与特殊条目；预计数与正式处理单位一致。 |
| 多根/阶段 | 两个不等大小非空根+空根；全根合计与各根权重固定，根内读取→整理→保存；跨根 saving→reading_tags 总体边界不降，根序号/阶段原子切换；T=0 等权、n_i=0 零份额与没有 roots 均不除零，成功才 100%。另测溢出安全运算与取整边界。 |
| 逐根结果与暂存 | 阻塞第二根读取时，第一根专辑已可从 catalog/recent 查询、第一根 staging 已清；取消第二根不撤回第一根提交；同时不积存多根 observation。前端根切换失效让已打开列表更新，计数根切换不触发无用失效。 |
| 取消/不可用 | counting 中持久化取消，500 ms 轮询与边界停止；不可用根/权限错误不能成功对账；检查长阻塞操作不宣称 500 ms SLA。 |
| 文件变化 | 预计数后增加/删除；A>T、A<T，分母固定、文案/changed 正确、百分比不下降；同数量替换不承诺 changed=true。 |
| 进度故障 | 注入短超时/进度 UPDATE 失败；扫描结果与无故障基线相同；批量最多 256、时间触发、阶段/换根强制刷新、失败节流；高 seq 的新根读取可覆盖旧根保存，迟到旧 seq 不能覆盖根/局部计数/进度；终态后写入无效。 |
| 报告故障 | 在 before snapshot、变更账、汇总写入分别注错；savepoint 恢复、catalog 仍提交、report unavailable；不能返回零或部分账目冒充 ready。 |
| 新增基线 | 合成 A 专辑两轨+B 专辑一轨：第一次 added albums=2/tracks=3，其他为 0；原样重扫全部 0。检查真实 ID 与记录表关联，不改创建时间制造窗口。 |
| 标签与位置 | 只改音频标签、同内容同 root 改名/移动：Track/Release UUID 延续，曲目更新=1、专辑更新=1；只 mtime 变化或指纹回填为 0。 |
| 缺失与恢复 | 删除 A 一轨：tracks missing=1、albums updated=1；再删其最后一轨：albums missing=1；再扫不重复计 missing；恢复原身份时 updated，不是 added。 |
| 替换与改挂 | 同路径换音频：旧 Track missing、新 Track added；2 轨加 3 轨触发新发行时原 2 轨是 updated、新 3 轨 added、旧组可能 missing；不把换发行算所有轨新增。 |
| 多版本组 | 人工先归组，只有一个版本消失时组 updated、不 missing；整组不可见才 missing；多个受影响版本只算一次组。 |
| 非成功 | 依次在读取、整理、保存、终态前取消；根读取失败、候选失败、封面后置失败、停机恢复；已提交正向/替换变化保留，终态百分比<100，绝不新增全根负向对账。 |
| 终态竞态 | finalize 锁扫描行后发现取消；负向对账与 succeeded/100 一起提交/回滚；遗留恢复冻结最后快照且 report unavailable。 |
| 认领 | same_files、same_directory、旧版无人工状态不提示；认领后提示消失、撤销后回来；历史数量/分组不改，当前链接改；当前不可见新增项保留。 |
| 治理并发 | 按 §10.3 的确定性双连接矩阵验证候选保存、封面记录、终态对账与归组/认领/撤销/默认版本的锁等待、锁后重读、超时降级和数据不变量；不能只验证两边最终没有报错。 |
| 最近新增 | 9 张可见组最多 8；隐藏组排除；同时间 ID 稳定；最早可见成员不是代表版本；新版本入旧组、恢复、禁用根、认领/撤销均符合定义；排序+搜索+attention+分页组合。 |
| 权限/旧数据 | 管理员/普通/匿名所有入口、当前根脱敏、旧扫描无报告；403 先于报告存在性；未知字段兼容、非法值严格拒绝。 |

沿用并回归 `scan_coordination_integration_test.go`、`scan_lifecycle_integration_test.go`、`identity_rebinding_integration_test.go`、`release_membership_integration_test.go`、`scanner_persistence_integration_test.go`、`claims_rescan_integration_test.go` 里直接相关用例；不默认全量。

实施后候选命令（新增测试命名统一含 ScanFeedback/ScanReport/RecentlyAdded，确保筛选实际命中；未匹配测试不能算通过）：

```bash
cd /home/qlqq/workspace/ROOMusic/backend
go test ./internal/library ./internal/catalog/ingest -run 'ScanProgress|ScanFeedback|ScanReport|ScanCount|MissingReconciliation' -count=1
# 下列命令须先显式设置指向隔离随机库的 ROOMUSIC_TEST_DATABASE_URL。
go test ./internal/library ./internal/catalog ./internal/catalog/ingest ./internal/catalog/governance ./internal/app \
  -run 'TestPostgreSQL.*(ScanFeedback|ScanReport|RecentlyAdded|ScanCancellation|ScanRecovery|ScanCoordination|ClaimClues|ReplacedContent|RescanKeepsTrackIdentity|MovedFileKeepsTrackIdentity|ReleaseGroup)' -count=1
go test -race ./internal/library -run 'ScanProgress|ScanFeedback|ScanCoordination|ScanRecovery|ScanCancellation' -count=1
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLAPIResponseSamples$' -count=1
```

不直接运行现有 `scripts/test-integration.sh`：它会全量跑 PostgreSQL/FFmpeg，且当前没有显式 `--env-file /dev/null`，不满足本任务最小验证与隔离命令要求。隔离服务准备复用下一节的安全方式，再定向 go test；缺库导致 skip 必须报告为未验证。

### 8.2 接口样本清单

由真实 handler 扩充 `backend/internal/app/testdata/api-samples/` 与 `frontend/src/test/api-contract.test.ts`：

1. counting（total 文件数为 null）、reading_tags、organizing、saving；管理员 root_progress 含 id/name 与普通成员仅 index/total 各一组。
2. 多根不等权、第一根 saving→下一根 reading_tags 的连续快照与不同局部计数；无根/零文件、计数不完整、文件数漂移、旧扫描 progress=null。
3. 新建 202、已有活动 200、active=null；取消请求中 202、终态取消 200。
4. succeeded/100、failed/canceled/incomplete 冻结<100、崩溃恢复/unavailable。
5. ready 报告（非零增改失、分页、有认领提示）；partial 报告；空新增列表；历史新项当前已不可见；认领后当前 group 改变。
6. 报告 legacy 404、pending 409、unavailable 503、未知 ID、401、403、非法分页。
7. recent 空/1/8 项、组列表 sort 的两种值、非法/重复 sort，added_at 与代表版本时间不同。
8. 新字段往返保留、旧 payload 可解码、未知键忽略；未知 phase、负数/不安全整数、percent_bp>10000、非法 root index、超限数组均拒绝。

### 8.3 前端最小集

- Vitest：library decoder/状态/进度组件、扫描详情与概览页、catalog sort/recent、查询失效、API 样本；键盘与读屏名、终态冻结、错误保留上下文。
- `npm exec tsc -- --noEmit`；定向 Vitest 用实际文件名；Playwright 只跑扫描、概览/recent/sort 相关新增文件，不为此启动全量播放/治理用例。
- 360、390、768、1280、1440 五种宽度无溢出，axe 无违规；截图必须含计数中、读取中、非成功终态、报告（长标题/认领提示）、recent 区块。
- UI 阶段/进度可用受控响应保证截图稳定；真实服务用例不能用假进度代替后端。短扫描轮询可能看不到所有中间阶段，逐阶段断言交给带阻塞点的集成测试，不能人为给生产扫描 sleep。

### 8.4 scripts/ 的真实服务小目录用例

推荐新增 **`scripts/scan-feedback-e2e.sh`** 与 `frontend/e2e/real/scan-feedback.spec.ts`，借鉴现有 `scripts/playback-e2e.sh` 的隔离启动，不直接复用它的全套播放素材/ffmpeg/180 秒文件。建议入口：

```bash
./scripts/scan-feedback-e2e.sh
```

脚本未来须具备如下真实行为（本轮不创建、不执行）：

1. `flock` 与现有真实服务入口共用互斥约定；检查 `roomusic-test` 项目已有容器/卷即拒绝接管，绝不先 down 他人的资源。
2. 获取两个可用 loopback 端口（DB/HTTP），生成随机库名、临时受管目录与合成音乐目录。**Compose 数组必须为**：
   `docker compose --env-file /dev/null -f /home/qlqq/workspace/ROOMusic/compose.test.yaml -p roomusic-test`。
   显式设置 `ROOMUSIC_TEST_PG_PORT`、随机 `ROOMUSIC_TEST_PG_DATABASE`、测试用户名密码；禁止继承已有 `ROOMUSIC_DATABASE_URL` / `ROOMUSIC_TEST_DATABASE_URL`。
3. 只启动 postgres；复制白名单源码到临时树编译，前端构建关闭 envDir，不加载仓库 `.env`，不写仓库内嵌产物。服务用显式 `ROOMUSIC_DATABASE_URL`、`ROOMUSIC_ALLOWED_LIBRARY_ROOTS`、`ROOMUSIC_DATA_DIR`、随机本机 HTTP 地址运行；不连接用户实例。
4. 生成小的合法 WAV/FLAC 文件，两个目录分别组成 2 轨和 1 轨专辑；可沿用测试中的合成音频编码方式，文件内容应具有可区分指纹。初始化测试管理员、登录保留 cookie 与同源头；登记临时根并 POST scans。
5. 以 50–100 ms 测试轮询 GET scans/{id} 捕获真实快照（这不是 UI 的 2/5 秒合同）；断言相邻百分比单调、running 不到 100、最终 succeeded=100、文件总数与报告 2 张/3 轨一致。小目录可能首次 GET 已终态，此情况允许；至少一次中间态由可控较大素材专用用例证明。
6. 使用独立子场景做无变化重扫、改标签、删除文件与替换，核对 §8.1 的报告数字；普通成员看 recent/进度但报告 403，打开 UI 验证进度条 100 与报告显示。只有临时目录允许改删文件。
7. trap 按精确 PID 停服务；只有本轮成功创建的 compose 项目才执行同一个绝对路径数组的 `down -v`，删除临时文件。退出验证没有遗留本轮测试卷。端口绑定冲突直接失败/重新选择，不杀占端口进程。

**禁止访问 `roomusic-postgres-1`、生产 `roomusic` 服务或用户真实音乐目录；禁止 `docker compose` 无 `-p roomusic-test`、无绝对 `-f`、无 `--env-file /dev/null`。**

## 9. 与并行实现线的依赖与冲突

父任务合入顺序为 P（收藏+歌单）→ F（本项）→ S（搜索+艺人）。本项功能不依赖 P/S 的业务表，但并非无文件冲突；依赖必须在本子任务后续 implement.md 中显式写明。

| 接触面 | 约定与可能冲突文件 |
| --- | --- |
| ⑤ 艺人扫描解析 | 高风险：`backend/internal/library/scanner.go`、`identity_plan.go`、`catalog/ingest/persist.go`、`persist_track.go`。F 先加全根预计数与逐根阶段反馈；S 在 F 合入后变基，将艺人解析挂在候选数据库事务中，不能在进度计数遍历里解析艺人，也不能绕过同一候选的提交成功边界。 |
| 反馈与艺人计数 | 新增 artist ID、解析候选/别名关系本身不算曲目元数据更新；显示姓名/署名文本发生变化才按本项投影计。⑤ 因治理改别名不倒写历史扫描报告。两线先约定投影字段，不依赖 serializer 随结构体新增字段自动变化。 |
| 搜索与组读取 | `catalog/groups.go`、`read.go`、`httpapi/handlers.go` / `groups.go`、内部 SQL 常量、`projection.go`，以及前端 `album-search.ts`、catalog API/query-keys。④ 替换搜索谓词不能把 added_at 的 MIN 局限到命中的发行；sort allowlist 与规范化查询参数分别负责。 |
| 收藏/歌单 | Track/Group UUID 语义不变；本项不迁移个人关联、不自动把旧曲目收藏迁给替换曲目。增量摘要 added_at 可能影响 P 的收藏专辑卡片投影/decoder/样本，主会话合并时统一共用 DTO，避免同名摘要两种形状。 |
| 概览与卡片 | `frontend/src` 的概览页面、AlbumCard、catalog 查询键、`app/router.tsx`：P 的收藏按钮与 F recent 复用卡片，S 的全局搜索入口不能覆盖 F sort 参数。 |
| 样本与迁移 | `backend/internal/app/api_samples_integration_test.go`、`testdata/api-samples/`、前端 contract tests、迁移目录与 migration tests；每线新增样本分开命名，主会话按最终合入顺序分配迁移编号并统一重生受影响样本。 |
| 规范 | 共享 backend/frontend spec、DESIGN、PRD 由主会话统一收敛；本轮不改任何 spec。 |

建议 F 尽量把新逻辑放 `library/scan_progress.go`、`scan_report.go`、`catalog/ingest/scan_changes.go`、catalog 最近新增/报告读取专用文件；`scanner.go` 只保留清晰编排，`persist.go` 仅插入窄的记录边界。不要为避冲突创建跨层万能 service、重复 SQL 规则或全局 hook 注册器。

合并后最小跨线复核：一份含艺人/署名的合成小库 → 扫描 → 报告 → 最近新增 → 搜索命中 → 收藏/歌单引用原 UUID；重扫不虚报更新；替换后旧个人条目按 P 的 missing 合同保留，不自动跳到新音频。

## 10. PRD 事实修正、已裁决事项与实现风险

### 10.1 PRD 事实修正（主会话同步）

1. 初稿 PRD 曾把 `GET /scans` 与 active/detail 合写成“所有登录用户”，实际列表是管理员；设计保持现有权限。
2. 初稿 PRD 的“created_at 落在扫描时间窗内即本次新建”只可作为现状猜测，不能用于可靠报告：时间窗、事务时间、并发及以后治理均会使关联不可靠。本设计用显式 scan→release 关系，旧记录不回推。
3. “只有成功扫描才标记缺失”必须限定为**全根负向对账**。源码明确允许完整 root 证据下的候选替换使旧曲目先 missing；随后的取消/其他根不完整不回滚已经提交的替换。报告必须反映它。
4. 当前诊断最多 100 条，不能无条件给“真实问题总数”；本设计使用有上限/下界说明，不扩大诊断范围。
5. 500 ms 是取消轮询周期，不是文件系统或 parser 最坏中断时延；“只看目录项”也不能提供文件系统静态快照。

### 10.2 已裁决

人工选择路线；选择者 qlqq，主会话于 2026-11-04 传达，以下决定均已纳入正文。本轮不修改 PRD，由主会话同步裁决与事实修正。

| 编号 | 已选结果 | 理由与落实 |
| --- | --- | --- |
| D1 | **不采用全根四阶段**。先对所有根计数，再保留逐根读取→整理→保存；总体按各根计数权重推进，根内按阶段推进。 | 先扫完的根更早出现专辑，暂存峰值维持现状；F1 为总体单调、每根内阶段有序，多根显示第 i / 共 R 个目录。公式、DTO、缓存失效与验收见 §2、§6–8。 |
| D2 | 接受报告口径：Track UUID 互斥分类、专辑按组；恢复计更新，替换计新旧两条；归集本次已提交变化。 | 不把 SQL UPDATE 次数或全库快照差当真实变更；冻结历史计数，报告 partial 语义见 §4。 |
| D3 | 接受反馈写失败不影响扫描，报告可显示“这次的报告不可用”；崩溃保留持久进度，但不承诺报告完整。 | 业务结果优先、反馈不能伪报准确；savepoint、unavailable 与恢复策略见 §3.3。 |
| D4 | 接受专辑加入时间为当前组内可见发行的最早创建时间。 | 可见性、归组/认领影响时间的动态语义明确；共享投影与排序见 §5。 |

认领提示沿用实时规则、旧扫描无报告、根身份/名称限管理员、所有成员可读进度及目录序号。本轮不新增产品选项，不重新开放已裁决事项；代码实施仍由主会话按整体任务流程组织。

### 10.3 实现风险：新增发行/组行锁与治理并发

**本节是实现与验证风险；D1–D4 已按主会话裁决落实。** 当前治理合同依赖“操作记录 → 发行行 → 组行”的锁顺序，且现有扫描不锁组。§4.4 新增组共享行锁会阻塞归组、认领、撤销与默认版本的组写入；即使只是为了统计，也不能假定没有行为影响。

#### 锁顺序与故障边界

- 候选事务必须在写任何既有发行/曲目、取得组锁之前，收集目标发行、原曲目所属发行、拟腾位来源所属发行；对全部既有发行按 UUID 升序取得 `FOR UPDATE`，**单独下一条语句**重读归属与可见性，再对涉及的组按 UUID 升序取得 `FOR SHARE`。READ COMMITTED 下不能依赖带 JOIN 的同一条加锁查询在等待结束后刷新所有关联行。
- 组锁之后不可因 Track 改挂、来源腾位或报告补查而追加另一发行锁；否则扫描持组等发行、治理持发行等组即可死锁。发现集合变化必须释放当前尝试并重新计算，不以逆序补锁“修好”一次执行。新建发行/组是本事务私有对象，但外键隐式锁仍需纳入测试。
- 治理的操作记录锁仍先于发行锁（认领/撤销需要 KEY SHARE 或 FOR UPDATE）；扫描不读写 operations，不因记录报告而在持发行/组锁后获取操作记录锁，也不锁治理覆盖表。
- 组共享锁只能覆盖当前候选/封面数据库短事务；不跨 root、目录遍历、指纹或封面文件 I/O。最终 missing 仍在全根单事务内，新增锁集可能较大，需验证其等待时间与内存有界，不能为缩短锁时间擅自拆成逐根提交。
- 报告专用的集合读取/新增锁尝试需纳入 §3.3 的 savepoint 与短 `lock_timeout`（建议 500 ms）；取锁超时或锁后检查失败时回滚该报告尝试、释放其新增锁，设置 reportBroken 并按原业务路径继续，不能仅因反馈竞争改变扫描结果。不得把该短超时无意扩展到原有核心 SQL；若有限重试集合仍不稳定，也应放弃报告而非无限重试。
- 若已经执行了核心变化，不能回滚包含核心变化的 savepoint 来“只丢报告”；锁集合确定、锁后前态读取应在核心写之前完成，后置记账用单独 savepoint。业务 SQL 自身断连、死锁或失败仍按原扫描失败合同处理，不冒充反馈故障。
- 即使没有死锁，新增发行锁与组锁也可能延长管理员操作等待；以可控阻塞点验证锁释放与前后结果，而不是只增加超时掩盖竞争。没有通过以下回归不能合入 `scanner.go` / `ingest/persist.go` 的锁改动。

#### 必需回归矩阵（隔离库、两条独立连接）

| 并发场景 | 阻塞安排与断言 |
| --- | --- |
| 候选更新/曲目改挂 × 人工归组 | 两个发行分别在不同组；分别让扫描先锁、治理先锁，治理把发行移组后扫描必须锁后重读。组统计只包含扫描变化，不出现旧组/新组混合快照；两边最终图一致。 |
| 来源腾位/跨候选改挂 × 认领 | 使用 UUID 顺序与候选处理顺序相反的对象，认领同时需要来源/目标发行及多个组；确认所有发行锁先于组锁，无 `40P01`，不丢失旧曲目消失或原组被抽空的计数。 |
| 扫描 × 认领撤销/归组撤销 | 撤销先持操作记录锁，扫描先持发行锁的交错；扫描不再索要记录锁、持组后不索要其他发行锁；撤销按现有 revision/preview 合同成功或返回明确冲突，不能损坏人工状态。 |
| 扫描 × 默认版本/核对/修正及其撤销 | 覆盖“只改组默认”与“涉及目标发行”两种路径；新增共享锁释放后治理继续，扫描不递增人工 edit_revision、不覆盖修正/核对/default，语义更新不把治理操作算进报告。 |
| 封面后置记账、finalize missing × 上述治理路径 | 候选已提交后另起封面事务；另测终态持 scan_runs 行锁后取得 catalog 锁。覆盖所有记录入口而非仅 PersistCandidate；missing 与成功终态原子，取消优先不变。 |
| 反馈锁等待超时/集合失效 | 故意由另一连接持组写锁超过报告锁预算；回滚反馈 savepoint 后检查其新增锁已释放、核心目录提交与无反馈基线一致、报告 unavailable。重试不能重复新增或重复累计。 |
| 崩溃/取消/holder 丢失与锁释放 | 在等待锁、候选提交前后分别中止；事务/holder 最终释放，下一扫描能取得执行权；仅保留已提交目录结果，未完成扫描不做全根负向对账。 |

复用 `group_change_api_integration_test.go`、`group_default_api_integration_test.go`、`governance/claim_integration_test.go`、`claim_revert_integration_test.go` 与扫描生命周期相关用例；新增定向用例统一命名 `TestPostgreSQLScanFeedbackGovernance...`，纳入 §8 的 ScanFeedback 筛选。用 channel/数据库锁屏障控制交错，按场景设有限超时，不靠任意 sleep 碰运气；校验锁等待、响应、目录图、报告分类及治理状态，失败必须定位后再合入。

## 11. 本轮交付与验证记录

- 已交付：按主会话 D1–D4 人工裁决修订的设计，覆盖 F1–F5 的技术合同、逐根加权进度、验证计划、故障降级和并行接触点，并单列 §10.3 实现风险。
- 未实施、未迁移、未运行 Go/npm/Playwright/数据库测试；没有连接任何容器或数据库。纯文档规划优先静态核对，不运行全量测试。
- 本轮静态核对命令：`git diff --check -- .trellis/tasks/11-04-classical-m5-scan-feedback/design.md`；另用 `python3 -` 检查文档空白、代码围栏配对与必需章节，并以 Fraction 核对权重示例、跨根边界、零总数退化与单调性（新文件未跟踪时仅 git diff 不能覆盖内容）。
- 规划选择已记录：人工路线，qlqq 经主会话作出 D1–D4 裁决，结果与理由见 §0/§10.2；本轮只修订文档，不改任务状态或启动实施。

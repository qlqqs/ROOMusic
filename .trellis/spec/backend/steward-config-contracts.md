# 管家配置与诊断合同（C9）

## 1. 范围与触发

适用于 `platform/diagnostics`、各能力的 `stewardtools`、`app/steward.go` 和进程入口的日志装配。
提供具名诊断、应用配置和本人播放设置；工具不实现业务 SQL，不提供任意 SQL、shell、文件路径操作或环境变量入口。
目录与成员写入复用 C9 第 1 步服务，备份、扫描、分区和 PEQ 继续复用原服务。
无新增迁移、第三方依赖或前端页面。

不提供新建成员、密码重置、删除备份、恢复备份、目录迁移工具。管家只允许保存不会清理现有备份的保留规则；
预览与执行都按当时完整备份清单计算，若会删除则拒绝并提示到设置页操作。备份完成仍沿用既有自动清理规则。

关联：[运行时合同](./steward-runtime-contracts.md)、[独立审查合同](./steward-review-contracts.md)、
[日志规范](./logging-guidelines.md)、[备份管理合同](./backup-management-contracts.md)。

## 2. 服务签名与装配

- `diagnostics.NewLogRing(roots ...string) *LogRing`、`NewHandler(slog.Handler,*LogRing) *Handler`。
  `LogRing.SetRoots(...string)` 原子替换词法脱敏根，`Recent(LogFilter) []LogRecord` 返回独立快照。
- `diagnostics.Service.SystemStatus(ctx) (Status,error)`、`DiskUsage(ctx) ([]DiskUsage,error)`。
  Service 注入 DB、启动时间、日志环、配置中的数据/备份目录及 `MusicDirectories`、`CatalogStats` 窄回调；平台包不读取业务私有表。
- `postgres.CurrentMigrationVersion(ctx,*sql.DB) (int64,error)` 只查当前 schema 的已提交迁移版本。
- `backup.Service.PreviewRetention(ctx,Retention) (int,error)` 只读计算清理份数，使用原 `retentionPlan` 与锁，不写规则、不删除文件。
- 新工具入口：`diagnostics/stewardtools.StewardTools(service)`、`backup/stewardtools.StewardTools(service,db,logger)`、
  `identity/stewardtools.StewardTools(db,logger)`、`playback/stewardtools.StewardTools(service,catalogStore,progress)`、
  `dspsettings/stewardtools.StewardTools(service,zonesService,logger)`。
- 已有工具包新增 `library/stewardtools.ConfigTools(service,allowedRoots,logger)` 和
  `zones/stewardtools.SettingsTools(service,logger)`，不重复已有扫描/播放工具。
- `cmd/roomusic/main.go` 在建立 logger 时包上 Handler；配置加载后注入数据、备份与允许音乐根。
  `app.WithDiagnostics(logs,startedAt)` 将同一环传入应用。`app.New` 在分析工作者与进度服务装配后注册管家。
- `app/diagnostics.go` 通过 `library.ListRoots`、`LoadRootLocation` 获取音乐目录，通过 `catalog.ReadCatalogStats` 获取统计。
  磁盘读取同时把已登记根加入日志脱敏根；最长匹配根优先。
- `registerSteward` 的可选 `stewardConfig` 保留旧装配测试入口；生产 `app.New` 始终传完整配置。

新包的直接内部依赖（测试代理登记精确 allowlist）：

| 新包 | 内部导入 |
| --- | --- |
| `platform/diagnostics` | `platform/buildinfo`、`platform/postgres`；另使用已有 `golang.org/x/sys/unix` |
| `platform/diagnostics/stewardtools` | `platform/diagnostics`、`steward/tool` |
| `backup/stewardtools` | `backup`、`steward/tool` |
| `identity/stewardtools` | `identity`、`platform/uuid`、`steward/tool` |
| `playback/stewardtools` | `catalog`、`platform/uuid`、`playback`、`playback/analysis`、`steward/tool` |
| `dspsettings/stewardtools` | `dspsettings`、`platform/uuid`、`steward/tool`、`zones` |

标准库导入（除上表内部依赖外）：

| 新包 | 标准库 |
| --- | --- |
| `platform/diagnostics` | context、database/sql、encoding/json、log/slog、path/filepath、regexp、sort、strings、sync、sync/atomic、time |
| `platform/diagnostics/stewardtools` | context、encoding/json、strings、time |
| `backup/stewardtools` | context、database/sql、encoding/json、errors、log/slog |
| `identity/stewardtools` | context、database/sql、encoding/json、errors、log/slog、strings |
| `playback/stewardtools` | context、encoding/json、errors、strings |
| `dspsettings/stewardtools` | context、encoding/json、errors、log/slog、strings |

`app` 新增上述工具包及 `platform/diagnostics` 导入；`cmd/roomusic` 新增 `platform/diagnostics`。
原 `library/stewardtools`、`zones/stewardtools`、`steward/tool` 和 `platform/postgres` 无新增内部依赖。

## 3. 工具清单与请求响应

共 24 项：11 read、11 shared、2 personal。**全部 `Revertible=false`**；read 的该值无撤销含义。
“成员”包括管理员；“管理员”使用 `MinRole=RoleAdmin`，不另建身份机制。
所有输入为对象且拒绝未知键。UUID 校验后转小写；调用者、来源、幂等键只来自后端 Caller。
表中 `?` 表示可选，未标记字段必填。结果沿用注册表 64 KiB 上限与 `truncated`。

| 工具 | 范围 / 身份 | 输入 | 输出字段 |
| --- | --- | --- | --- |
| `get_system_status` | read / 管理员 | `{}` | `build{version,commit},started_at,uptime_seconds,database_connected,database_pool,migration_version,catalog,errors[]` |
| `get_disk_usage` | read / 管理员 | `{}` | `items[{kind,root_id?,name,total_bytes,available_bytes,error_code?}]` |
| `list_recent_logs` | read / 管理员 | `level?,module?,start?,end?,limit?` | `items[{time,level,fields}]` |
| `get_backup_overview` | read / 管理员 | `{}` | `status,retention,backups[{name,source,created_at,size_bytes}],total` |
| `start_backup` | shared / 管理员 | `{}` | `status,started` |
| `set_backup_retention` | shared / 管理员 | `keep_days,keep_count`（整数或 null） | `retention{keep_days,keep_count},deleted,failed` |
| `list_library_roots` | read / 管理员 | `{}` | `items[{id,name,path,status,revision,created_at,updated_at,excluded_paths[]}]` |
| `get_scan_schedule` | read / 管理员 | `{}` | `enabled,hour` |
| `add_library_root` | shared / 管理员 | `path` | `id,name,status,revision` |
| `set_library_root_enabled` | shared / 管理员 | `root_id,expected_revision,enabled` | `id,path,status,revision` |
| `add_root_exclusion` | shared / 管理员 | `root_id,path`（子目录绝对路径） | 同目录列表的单个条目 |
| `remove_root_exclusion` | shared / 管理员 | `root_id,path`（已有相对路径） | 同目录列表的单个条目 |
| `start_scan` | shared / 管理员 | `mode?` | `scan,started` |
| `cancel_scan` | shared / 管理员 | `scan_id` | `scan,requested` |
| `set_scan_schedule` | shared / 管理员 | `enabled,hour` | `enabled,hour` |
| `list_members` | read / 管理员 | `{}` | `items[{id,username,role,disabled,created_at}]` |
| `set_member_disabled` | shared / 管理员 | `member_id,disabled` | `member_id,disabled` |
| `revoke_member_sessions` | shared / 管理员 | `member_id` | `member_id,revoked` |
| `get_track_audio` | read / 成员 | `track_id` | `track_id,format,sample_rate,bit_depth,channels,duration_seconds,size_bytes,source_check,analysis_status,analysis,album` |
| `get_analysis_progress` | read / 管理员 | `{}` | `total,analyzed,failed,state` |
| `get_zone_settings` | read / 成员 | `zone_id` | `zone_id,settings` |
| `set_zone_settings` | personal / 成员 | `zone_id,settings`（非 null 设置补丁） | `zone_id,settings,server_time` |
| `list_peq_presets` | read / 成员 | `{}` | `items[{preset_id,name,peq,created_at,updated_at}]` |
| `apply_peq_preset` | personal / 成员 | `zone_id,preset_id` | `zone_id,preset_id,peq,server_time` |

### 诊断形状

- `build` 复用 `buildinfo.Current`，未注入版本时 `version=dev`；启动时间为 UTC，运行时长为非负整秒。
- 数据库探测使用 `db.PingContext`，独立 2 秒超时；失败仍返回进程事实和连接池，`database_connected=false`，
  `migration_version/catalog=null`，`errors=[database_unavailable]`，不返回驱动错误。
- `database_pool` 是 `sql.DBStats`，字段使用标准库名称：`MaxOpenConnections,OpenConnections,InUse,Idle,WaitCount,WaitDuration,MaxIdleClosed,MaxIdleTimeClosed,MaxLifetimeClosed`；
  `WaitDuration` 为纳秒。迁移/统计各自失败时对应值 null，errors 增加 `migration_unavailable` / `catalog_unavailable`。
- `catalog={albums,tracks,artists,composers}`，后两项可以为 null（艺人整理未完成），复用原目录统计事务。
- `kind=data|backup|music`；音乐目录带 root_id，只包括启用目录。容量为字节，available 使用 `statfs.Bavail`；
  每项 statfs 独立 2 秒超时（包括等待探测槽位），不可读取或超时返回两项 null 和 `disk_unavailable`，不返回 errno、路径或设备名。同一文件系统的不同用途各列一项。系统调用本身不能中断，全进程最多 4 个在途 statfs；完成后释放槽位，调用者取消照常传播。
- 日志最新在前，level 精确匹配 `DEBUG|INFO|WARN|ERROR`；module 精确匹配顶层模块；
  start/end 为 RFC3339（可带小数秒），包含边界；limit 默认 100、1–200；module 最多 128 字，时间最多 40 字。
- 日志环只保留 1000 条，进程重启清空。记录 `{time,level,fields}` 没有自由消息正文。

### 业务形状

- 备份 `status={running,directory,last_success_at,last_failure,next_scheduled_at}`；directory 是既有可写性状态枚举，**不是路径**。
  running 为 `{source,started_at}` 或 null；last_failure 为 `{at,stage,source}` 或 null。
  retention 为 `{keep_days,keep_count}` 或 null（规则不可读）；backups 最多 500，total 为完整数量。
- keep_days：1–3650；keep_count：1–1000；null 表示不限。无模型 `confirm` 参数：注册表的 shared 执行许可才允许调用保存。
  保存固定调用原 `SaveRetention(...,false,actorID)`；服务在同一临界区内重算，若返回 ConfirmationRequired，
  工具返回 `retention_would_delete_backups`，不写规则、不删除文件；正常保存 deleted/failed 均为 0。所有模式和 MCP 一致。
  原始规则损坏时预览 before=null；保存仍可修复规则，复用现有服务行为。
- 目录响应的 name/path 只含目录名，excluded_paths 只含根内相对路径。输入 path 长度 1–4096，
  添加目录在 Preview 与 Execute 都调用 `library.ValidatePath`，不回显 OS 错误，且拒绝文件系统根 `/`；
  预览目标显示相对允许根的完整层级（选最长匹配根，等于允许根时显示根名称）。排除真实校验继续由原服务负责。
  revision 为 1–9007199254740990。停用/恢复保持原状态与版本冲突语义，不添加幂等空操作。
- mode 默认 incremental，可选 full；trigger 固定 manual。scan 复用既有 `presentScan`，root_progress 不含名称或路径。
  started=false 表示已有活动扫描，requested 表示仍在运行且已提交取消意图。
- hour 为 0–23，使用实例已有扫描时区（与备份调度时区一致）；成员停用仍保护最后管理员并撤销登录会话。
- set_member_disabled 与 revoke_member_sessions 的 Preview/Execute 都先拒绝调用者本人，错误为 `self_target_not_allowed`，不影响原 HTTP 成员管理语义。
- 音频 `format={source_kind,codec,mime_type?}`；只有 Prepare 核对通过才带 mime_type。
  `source_check=verified|not_directly_playable|root_disabled|source_changed|source_unavailable`，
  后四者保留已存事实；不把 CUE/ALAC 等非直出格式当作未知曲目。Prepare 打开的句柄立即关闭。
- 音频 analysis 为 null 或 `{integrated_lufs,true_peak_dbtp,sample_peak_dbfs,loudness_range_lu,track_gain_db,duration_ms,leading_silence_ms,trailing_silence_ms}`；
  album 为 null 或 `{release_id,gain_db,peak_dbtp}`。不输出 waveform、源路径、ETag、文件句柄或流地址。
- settings 为原 `zones.PlaybackSettings`：`leveling,gapless,crossfade,crossfade_seconds,quality,headroom,sample_rate,downmix,peq,convolution`。
  补丁省略项保留，提供的嵌套对象须完整；复用 `ValidateSettingsShape`、SettingsPatch 解码与 `ValidateSettingsPatch`。
  leveling=off/auto/track/album，quality=lossless/lossy，crossfade_seconds=3/5/8/12；
  headroom={enabled,db,clip_indicator}，db 为 −30…0 的 0.1 步进。
  sample_rate={strategy,custom_rate,filter}、downmix={enabled}、convolution={enabled,ir_id,before_peq}；
  PEQ 为 `{enabled,preamp_db,bands,source?}`，详细形状及约束见[PEQ 合同](./peq-contracts.md)与原 zones 文档解析器。
- list_peq_presets 仅本人，现有上限 200；apply 按本人列表选预设，把来源替换为 `{kind:preset,preset_id,name}` 快照，
  保留预设 enabled 值，调用原 `zones.Service.Command(set_settings)`，不改变其它设置。

### 身份、确认与来源

- 所有 read 的管理员限制同时覆盖 runtime 模型清单、MCP 清单与 Registry.Invoke/PreviewCall；成员直接调用返回 permission_denied。
  备份未装配时通过 Available 同时隐藏并拒绝三个备份工具。MCP 原只读/读写令牌范围继续有效。
- `app/steward.go:withCurrentStewardUser` 包装所有工具的 Preview/Handler，每次调用前用 `Guard.EnabledUser` 重读角色与停用状态，
  再检查 MinRole、成员功能开关与 Available，把最新身份交给原工具。覆盖普通运行、确认后执行和 MCP；
  数据库不可用拒绝调用，停用/降级无需等待 SSE 的 60 秒凭据复查。运行时没有每轮身份刷新钩子，故在统一装配边界执行。
- 所有 shared 均提供 Preview、Revertible=false，assistant 每次确认，不可加入免确认名单；operator/MCP 读写令牌沿用直接执行规则。
- 扫描计划、目录状态、成员状态、保留规则给出旧值/新值；动作说明写入 `changes[{field:action,before:null,after:说明}]`。
  不能只填 Preview.Summary：C6 对外卡片只投影 target/changes。目录目标用服务提供的显示名，扫描 action 列出启用目录名；
  成员 target 显示用户名及角色。不会清理的保留策略只展示旧值/新值，会清理则预览直接拒绝。
- 已核对 `runtime/run_review.go`：先调用 reviewPolicy；不可撤销项直接 policy/needs_human_confirmation 后 continue，
  不调用证据读取器或审查模型。C9 不伪造 C6 的治理证据或 operations.Origin；来源写入结构化配置事件。
- 目录操作使用 Caller 的 ActorID、IdempotencyKey、RequestID，保留 `library_root_operations` 收据；
  其它配置仍沿用原服务的事务或任务语义，不新增 operations 记录、撤销执行器或幂等存储。
  **恢复限制**：C6 的只读对账仍只查 operations；配置成功而管家结果落库失败时，没有统一的配置收据对账。
  目录再次执行同键由原服务回放；非目录动作跨中断不承诺 exactly-once，不能把“不可撤销”当成“有持久化幂等记录”。

### 日志脱敏与事件

- Handler 的原输出先执行；环旁路使用 TryLock，竞争时丢弃本次记录，不等待锁、不排队、不启动协程。
  脱敏或入环 panic 被旁路捕获，原 Handler 的输出/返回值不变。读取返回独立快照。
- 只保留[日志规范](./logging-guidelines.md)的显式字符串/数值键白名单及 `_id` 标识键，未知键整项丢弃。
  msg 只保留固定事件文本并脱敏，动态正文不外发；命名组、任意容器、未知对象都不进入环。
- 凭据赋值、Authorization/Cookie、URI、裸密钥与 user:pass@host 整串脱敏；绝对路径整体替换（含空格），
  路由模板和 MIME 的安全斜杠保留。字段/深度/大小限制沿用日志规范，脱敏与大小校验都在环锁外。
- 配置成功后由 `tool.LogConfigChanged` 发 INFO `steward.config.changed`，固定 message/module，字段：
  tool、actor_id、source(chat/mcp)、target_id、conversation_id、token_id、request_id。只记录稳定 ID，不记录输入正文或配置值。
  chat 使用对话 ID，MCP 使用令牌记录 ID，均不是登录令牌明文；未适用项为空。
  日志环保留 conversation_id/token_id 等记录标识，丢弃 source。事件同时用于两个 personal 设置工具。
  started=false 或 requested=false 不写事件；目录 RecordFailure 在工具边界恢复与 HTTP 一致的 ERROR 日志。

## 4. 校验与错误矩阵

| 条件 | 工具错误码 / 行为 |
| --- | --- |
| 角色不符、Available=false | permission_denied；列表不出现 |
| 未获 shared 执行许可 | approval_required；不调用业务服务 |
| schema、UUID、时间范围、保留值、设置形状或值非法 | validation_failed |
| 目录/成员/扫描/曲目/本人分区/本人预设不存在 | not_found |
| 添加路径不在允许范围或排除路径校验失败 | invalid_library_path，不回显允许根或 OS 错误 |
| 根状态或 revision 已变化 | revision_conflict |
| 目录同键不同参数 | idempotency_conflict |
| 移除排除项的相对路径不合规 | invalid_input（沿用原服务） |
| 超过 100 条排除规则 | exclusion_limit |
| 以本人为目标停用/启用或吊销会话 | self_target_not_allowed（预览与执行一致） |
| 停用最后一个管理员（服务原有保护） | last_admin |
| 新保留策略会清理现有备份 | retention_would_delete_backups（预览与执行一致，提示设置页） |
| 扫描无法确认协调锁 | scan_coordination_unavailable |
| 根操作持久化失败 | 原服务 database_unavailable / database_error / operation_failed |
| 成员、扫描（含 get_scan_schedule）或音频数据库读取/写入失败 | database_unavailable；未分类错误经入口变成 tool_execution_failed |
| 备份忙 / 目录不可写 / 其它备份失败 | backup_busy / backup_directory_not_writable / backup_unavailable |
| 读取音乐根失败而无法生成磁盘清单 | diagnostics_unavailable |
| 分区离线 / 无渲染器 | zone_offline / renderer_unavailable（沿用原服务的适用条件） |
| context 取消 | 传递取消，由运行时结束本轮；不伪造成功 |

单目录 statfs、系统状态的局部探测失败是结果中的安全类别，不把“探测失败”伪装成 0 容量或数据库正常。
日志与错误均不暴露驱动错误、配置正文或服务器路径。

## 5. 正确、基线与错误案例

正确：管理员要求每天三点扫描，预览 enabled/hour 的旧新值，人工确认后 SaveScanSchedule 生效。
基线：已有 HTTP 目录、成员、备份、播放接口仍调用原服务；未装配备份时工具不可见。
正确：成员设置本人的 PEQ；管理员访问其他成员分区同样 not_found。
错误：将不可撤销标志解释为可以省略确认，或将磁盘无法探测显示成“空间为零”。

## 6. 测试交接与本轮验证

实现代理未新增测试、不修改 `_test.go`；以下由测试代理补齐：

1. 24 个定义注册、必填/未知字段、角色与各模式、runtime/MCP 列表过滤和直接调用；备份 Available。
2. 1000 条容量、最新在前、过滤与边界、默认 100/上限 200；并发写入/读取、WithAttrs/WithGroup、快照隔离、锁竞争不阻塞。
3. 合成 password/嵌套敏感键、DSN/URI/Bearer、消息正文、已知根/邻居目录/未知路径/Windows 路径；原 Handler 输出与错误不变。
4. 系统 ping 失败与超时、连接池/迁移/目录统计真实值；独立临时目录的 statfs 成功与失败，输出无绝对路径。
5. 所有 shared 的不可撤销与只读 Preview，steward 策略先转人工且不调用证据/模型，动作卡片能看到 action；保留策略导致清理时拒绝。
6. 允许范围之外的目录被原服务拒绝、根 revision/幂等/排除规则、最后管理员保护；各服务成功输出与日志来源。
7. 本人分区与预设隔离、完整设置文档校验、PEQ 套用保留 enabled；音频直出/非直出、关闭句柄、无路径/流地址/波形。
8. 第 1 步原 HTTP 集成回归；依赖规则登记上表新包和 app 导入。配置跨中断恢复限制单独核对，不宣称统一幂等。

2026-11-16 实现代理在 backend/ 下执行（均额外设置 `GOPROXY=off GOSUMDB=off`，禁止依赖下载）：

```sh
mise exec -- go build ./...
mise exec -- go vet ./internal/platform/diagnostics/... ./internal/backup/... ./internal/library/... ./internal/identity/... ./internal/playback/... ./internal/zones/... ./internal/dspsettings/... ./internal/steward/... ./internal/app/... ./cmd/...
env -u ROOMUSIC_TEST_DATABASE_URL mise exec -- go test ./internal/steward/tool/... ./internal/steward/runtime/... -count=1
```

三条命令均成功退出；tool/runtime 无数据库用例通过，数据库集成用例跳过不算通过。
最终实现 build、vet 已复核；后续日志/卡片调整不涉及 tool/runtime 代码，复用其成功结果，没有重复运行。
`git diff --check` 通过。未新增/修改测试、依赖文件、迁移或前端；未改依赖规则测试；未提交 Git。
不启动容器、不访问外部网络、不使用现有实例数据或配置。

### 与设计的细化及未做项

- 工具清单、范围、身份与 design.md 一致。design.md 将分区设置标为 C5 已有，但现有代码没有登记这两个工具，因此本次补齐。
- get_track_audio 的 Prepare 仅覆盖旧直出格式；非直出、停用或来源变动时保留已存音频事实并给 source_check，避免丢失 CUE/ALAC 等格式的信息。
- 日志环使用显式白名单，不保留自由正文或错误对象；会话/令牌记录 ID 可保留，凭据明文不可保留。
- PreviewRetention 先拒绝会删除的方案；SaveRetention(confirm=false) 在锁内重算并拒绝，避免预览与执行之间的清单变化绕过 D2。
- 动作说明同时放入 changes，适配 C6 对外字段；原工具 Preview.Summary 本身没有进入卡片字段。
- 依赖规则登记、新工具测试、日志并发/脱敏测试、数据库/HTTP/MCP 集成与前端标签交给测试代理和主代理；本轮没有执行。
- 未新增统一配置操作日志、撤销或跨中断收据对账，恢复限制见第 3 节；这属于设计“不补操作记录与撤销”的现有边界。


## 7. 错误与正确写法

错误：工具层直接 UPDATE users；把绝对目录或完整日志消息传给模型；调用 SaveRetention 生成“预览”。
正确：调用 `identity.SetUserDisabled` 保留事务保护；仅返回安全目录投影；日志先脱敏再入环；
使用只读 `PreviewRetention`，shared 获得后端许可后才调用实际保存。

### C9 审查修复补充验证

修复轮只运行用户指定 build、限定包 vet 与 diagnostics 测试，不运行数据库集成；实际结果见
`temp/c9-backend-tests/fix-report.txt`（本机忽略文件）。旧白名单/自身停用/清理策略断言需由测试代理调整，不能沿用上轮通过状态。
`main.go` 移除 slog.SetDefault：生产装配显式传入 logger；默认 logger 的 nil 回退及无生产调用的 zones.LoadUser 不构成运行依赖。

## 整理模型的设置与测试

整理读取开启者保存的有效端点、协议、凭据与能力设置，可用整理设置覆盖模型名。配置变更后唤醒模型队列。整理的管理员接口 `POST /api/v1/admin/curation/model/retry` 对该有效模型发送一次工具探测，使用预期工具并完整结束才放行。后台验证结果绑定有效配置，手动指定不支持工具时需要修改能力配置。本机上下文长度由 `context_hint` 提示管理员按服务实际加载值设置。

# 卷积校正后端合同

## 1. 范围与边界

`dspsettings` 拥有成员 IR 原文件、元数据与四份装载缓存；`dspsettings/irload` 拥有受限解码、cfg/zip 解析及目标率装载；`zones` 拥有不可变卷积设置、资源归属检查和删除编排；`playback/render` 通过消费方端口取得只读配置，创建会话独占的卷积器。`app` 组合适配器，`zones` 不导入资源包，`render` 不导入 `dspsettings`。

本合同落实任务设计 §1.3、§2–§7；不引入分区卷积、外部原文件存储或客户端算法。

## 2. 签名与存储

- 设置沿用 `SettingsDocument`：`DefaultConvolutionDocument()`、`NewConvolutionDocument(ConvolutionSettings)`、`ParseConvolutionDocument([]byte)`；`document.DecodeConvolution()` 返回独占配置，包含 `Enabled bool`、`IRID *string`、`BeforePEQ bool`。
- `zones.Options.IRReferences` 注入 `Check(ctx, tx *sql.Tx, userID, irID string) error`、`Delete(ctx, tx *sql.Tx, userID, irID string) error`、`Invalidate(userID, irID string)`。`Check` 可在预检时用 nil 事务，写入前必须在调用方事务内复核；`Delete` 必须接收事务且不自行提交。
- `zones.Service.DeleteIR(ctx, userID, irID string) error` 是唯一资源删除用例。app 将资源不存在和他人资源映射为 `zones.ErrNotFound`，不能包成数据库 503。
- `dspsettings.NewService(db)` 保持兼容，启动时调用 `ConfigureIR(irload.NewLoader(tool))`。资源接口为 `ListIRs`、`UploadIR`、`RenameIR`、`CheckIR`、`DeleteIR`、`InvalidateIR`、`LoadIR(ctx,userID,irID,rate,channels,layout)`。
- `render.Options.IRs` 注入 `Load(ctx,userID,irID string,format chain.Format) (render.LoadedIR,error)`，返回 `{Name,ResampledFrom,Config}`；app 将布局掩码转换为十六进制字符串作为资源缓存键。
- 0048 新建 `dsp_ir_files`：成员外键、原始 `content bytea`、32 字节 `sha256 bytea`、大小和几何元数据、`created_at/updated_at`、`UNIQUE(id,user_id)` 与成员索引。原文件随现有整库备份保存，名称不唯一。
- 0049 增加 `playback_zones.convolution jsonb NOT NULL`，默认 `{"enabled":false,"ir_id":null,"before_peq":false}`。引用由服务事务维护，不使用 `ON DELETE SET NULL`。回退先 0049 后 0048；回退会丢失选择与原文件，须先备份。

## 3. 请求与响应

所有接口使用 `/api/v1/me/dsp/` 前缀、本人鉴权和既有 Origin 守卫。

| 接口 | 合同 |
| --- | --- |
| GET `ir-files` | `{items:[IRFile]}`，按 `created_at` 升序，同时间按 ID 稳定排序 |
| POST `ir-files` | multipart，`name` 与 `file` 各一次；201 IRFile |
| PATCH `ir-files/{ir_id}` | `{name}`；200 IRFile |
| DELETE `ir-files/{ir_id}` | 204，无响应体；必须调用 zones 删除用例 |

IRFile 为 `{ir_id,name,file_name,container,sample_rate,channels,paths,taps,created_at,updated_at}`；`container` 是 `zip|wav|flac|aiff|m4a`。名称 trim 后为 1–64 字，可重复；非法名称与 PEQ 预设一致返回 `invalid_input`。

播放区 `settings.convolution` 始终为完整 `{enabled,ir_id,before_peq}` 对象；`ir_id` 可为 null。set_settings 收完整卷积文档，不接受缺键、null 对象或未知键。非空 ID 即使 `enabled=false` 也必须属于本人；plan/RegisterDevice 预检，事务内再核验。

信号步骤为 `kind:"convolution"`、`quality:"enhanced"`、`compat:false`；params 只有 `{name,taps,resampled_from?}`。taps 是目标率上的最长路径；仅实际重采样时给出原采样率 `resampled_from`。

## 4. 校验与错误矩阵

| 条件 | 状态与 code |
| --- | --- |
| 名称、完整设置文档或 UUID 形状无效 | 400 `invalid_input` |
| 不支持的容器或内容签名 | 400 `ir_format_unsupported` |
| cfg 语法无效 | 400 `ir_cfg_invalid`，message 固定为 `cfg 第 N 行看不懂` |
| 音频解码/内容无效 | 400 `ir_decode_failed` |
| 文件、解压、解码或几何超过上限 | 413 `ir_too_large` |
| 本人成员已有 50 个 IR | 409 `ir_limit_reached` |
| IR 不存在或属于他人 | 404 `not_found` |

错误使用既有 envelope，消息是安全的简短中文，不包含文件路径、cfg 原文、音频内容或 ffmpeg 诊断。

上传文件上限 32 MiB，请求上限另加 1 MiB multipart 开销；使用流式 multipart 和私有有界临时文件，不全量读取 HTTP 请求。上传体读取期限 60 秒，处理期限 120 秒；成功、失败和取消均关闭、删除临时文件。只在上传 handler 设置并清除读取 deadline，不改长音频响应的全局超时。

zip 最多 64 条目，实际解压总量最多 256 MiB；不将条目路径解压至磁盘，不允许 cfg 引用 zip 外部路径。上传验证所有 cfg 引用；播放按采样率、输入声道数和文件名选择。音频只解第一个音轨；输出有界 f64 WAV，最多 8 声道、32 路径、每条目标 IR 524288 抽头，重采样前先推算长度。裸浮点及解码样本必须有限。cfg 全部输入/输出延时线合计最多 256 MiB float64 存储；上传源率、装载目标率以及单路径复制后的实际配置均在分配前检查，超限返回 `ir_too_large`。此限制堵住极小 cfg 指定巨大延时导致内存耗尽的入口，不限制普通毫秒级对齐。

`ffmpeg.Tool.Independent(1)` 共享已探测的只读能力，独立管理进程名额；IR 装载不占播放配额。loader 同时只处理一次解压/解码/重采样，不持数据库事务或播放区锁。现有 `src.ResampleAll` 无 context；取消在每次调用前后检查，单次计算不可中断，不启动脱离生命周期的计算协程。

## 5. 正常、基线和失败场景

- 默认关闭或 ID 为 null：不插卷积节点。成功装入的单位 IR 仍声明修改样本。
- `Open` 保留既有来源元数据准备，只进行轻量格式规划，不查询 IR 表、不解码 IR、不生成 SRC/卷积频谱。固定交付格式发布后，会话协程装载 IR、构链，最后预留编码/来源解码进程；WAV 一个名额，其他编码两个。
- 准备中 `encoder=nil` 不等于来源失败。Body/HLS 等待和 Close 取消覆盖准备期；Close 不等待加载。准备失败通过流错误结束，普通 IR 装载失败仅跳过卷积并记一次脱敏警告，不改用户设置；会话取消不得降级继续播放。出口位深按启用设置保守规划，不在失败降级后改变已公布格式。
- 卷积使用 SRC、矩阵降混之后、Tail 之前的真实格式；默认 PEQ→卷积，`before_peq=true` 时卷积→PEQ，均在 `Spec.User`。节点用 `chain.Wrap`，固有 Latency 为路径最大绝对值抽头下标，链负责丢头/补尾；不额外延长曲目尾巴，不在换曲时 Reset。
- 四份最近使用缓存键包含成员、IR ID、内容摘要、目标采样率、声道数与布局；每次读取仍核对本人资源元数据。缓存配置只读，会话持有独占处理状态；提交后失效代次防止旧在途装载重新填回缓存。
- 删除锁序：用户写锁→按 zone_id 排序的 dispatch→区锁→数据库事务。用户运行态包含离线区；同一事务删除本人资源并清所有引用，`enabled=false,ir_id=null`，保留 before_peq，每区 revision 增一次；失败不改内存。提交后更新内存、清缓存、关闭旧会话并发布完整区更新。
- 普通设置/定位重建继续沿用既有 `session!=nil` 条件，暂停且保留会话时行为不变。仅 DeleteIR 已关闭会话后的专属重建要求在线、播放意图、当前项存在、无会话且代次不变，重试沿用同一条件。

## 6. 验证接点

本次已有门禁：`dsp/convolution`、`dspsettings/...`、`zones/...`、`playback/render`，以及 app 的 `TestPackageDependencyDirections`。真实渲染必须先设置 `ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。

数据库只运行 `TestPostgreSQLZones|TestPostgreSQL.*DSP|TestPostgreSQLMigrationRollbackRoundTrip`；recorder 只更新 DSP 和 Zones 两组。新增六种 IR 请求/响应样本（列表、上传、改名、cfg 无效、超限、跨成员 not_found），以及一份非默认卷积设置请求/响应。已有包含设置的 Zones 样本增加默认卷积字段。

独立测试任务仍须覆盖设计 §9 新接入断言：准备期取消/其他区不阻塞、跨成员选择、删除提交后 DB/缓存/内存一致并能继续命令、超限/取消清理、备份恢复小 IR、真实容器解码、多 cfg 全部校验和实际卷积信号/Latency。不得把本次通过的既有测试或金样当作这些新用例已经完成。

## 7. 错误写法与正确写法

- 错：HTTP 直接删资源，依赖 FK 自动清引用；对：经 `zones.Service.DeleteIR` 同事务清引用，提交后同步运行态和缓存。
- 错：disabled 时跳过资源归属检查；对：只要 IRID 非空就核验本人资源。
- 错：Open 持区锁解码、做 FFT，或提前占播放名额等待 IR；对：轻规划后在会话协程装载，准备完再 Reserve。
- 错：把最终立体声 Tail 出口作为卷积输入格式；对：使用 SRC/矩阵输出、Tail 之前格式。
- 错：以 encoder 是否为 nil 区分有效/失败来源；对：明确区分待准备计划和来源失败状态。

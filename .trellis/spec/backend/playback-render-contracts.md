# 服务端渲染合同（P-c、G-2、D-2、P-e、P-g）

## 1. 范围

`playback/render` 实现 `zones.Renderer`，通过消费方端口 `TrackSource` 获取已经安全打开的文件和分析结果；不导入 playback、catalog，不连接数据库。`render/ffmpeg` 是标准库叶子包。app 是唯一装配根。

G-2 在同一 PCM 入口接入无缝、交叉淡化、就绪超时与静音间隔，长流与 HLS 共用；D-2 / P-e 提供抖动出口、Headroom、削波指示与完整信号路径。P-g 放行 CUE 虚拟分轨，来源与截取合同见 §10；派生退役（P-d）另行处理。

## 2. 签名

- `ffmpeg.Open(ctx, path) (*Tool, error)`：定位、版本、解码/编码清单、soxr 探测及内嵌 ALAC → PCM → FLAC → PCM 自检。
- `Tool.Reserve(ctx, n) (*Reservation, error)` 原子预留多个名额；`Reservation.Start(ctx, StartOptions)`、`Tool.Start(ctx, StartOptions)` 返回 `*Process`。
- `Process` 实现 Read/Write/Wait/Close，另有 CloseInput、Stdout。Source 经 fd 3 借用，所有者仍须关闭原句柄。自然结束先 CloseInput、读到 EOF、Wait，最后必须 Close。
- `render.New(Options{FFmpeg, Sources, Clock, NewTicker, Logger}) *Renderer`；实现 zones 两个端口；`Renderer.Close()` 等待资源回收。
- `TrackSource.Prepare(ctx, userID, trackID) (Track, error)`；Track 包含源格式、IsFloat、文件、时长、发行、首尾静音、曲目/专辑增益。`CueStartFrame/CueEndFrame` 以源 PCM 帧计，用于 CUE 截取，不是数据库的 CUE 75 Hz 索引。
- `playback.WithRenderDecoders(RenderDecoders)`、`Service.RenderFormats() []Format`、`Service.OpenForRender(ctx, trackID) (*RenderSource, error)`；RenderDecoders 只含 HasDecoder。
- `app.OpenRenderer(ctx, settings, logger) (*ffmpeg.Tool, error)`；`app.WithRenderer(tool)` 注入工具，由 app 组合 playback 和 catalog 公开合同。

## 3. 生命周期与数据合同

- 工具实例内所有编码/解码共享 `3 × GOMAXPROCS` 名额，原子获取最多等 5 秒；进程 Wait 后归还，Reservation.Close 归还未用名额。生产只装配一个工具。
- 子进程自成进程组，取消杀整组，WaitDelay 为 2 秒。解码起始与实际阻塞读取各 10 秒超时；环满背压不计入停滞。首次读取可仅有 1 字节，消费方必须累计完整 PCM 帧。stdout EOF 后另用注入计时器最多等待退出 10 秒，超时取消并回收进程；WaitDelay 不能单独约束仍在运行的进程。
- 每首 10 秒原始字节环，33% 或 EOF 就绪；按帧拆为 planar float64，再选择并施加电平化，清空区域后进入共享链。整数未改动样本使用 Exact/SourceBits，经 `Chain.Quantize` 决定逐位直通。
- 量子为链采样率/5，最多领先播放头 20 秒；编码器输入与 Body 的管道传递背压。处理速度不计预缓冲、领先限制和编码输出等待。
- Negotiate 按设备偏好过滤可产出的编码，再选期望档位，无匹配档位时降到另一档。`StreamFormat.MaxSampleRate/MaxBitDepth/MaxChannels` 随值携带设备上限，标记 `json:"-"`；不能用渲染器全局可变缓存存最近一次协商能力。
- Open 确定不可变的具体格式：无损超过采样率上限同族减半，声道不超过能力（第一批 FLAC/Opus/AAC 使用最多八声道标准布局，WAV 保留能力上限）；透明整数源保持 16/24 位，其余按 24 位或设备上限；Opus 为 48 kHz，AAC 不超过 96 kHz。第一批的采样率转换与降混由 ffmpeg 完成。
- Cursor.Next 只在会话协程调用，不能在 Open 同步调用。Observe、NextChanged、会话 Close 不等待 Cursor 或子进程回收，允许持 zones 锁调用；渲染器 Close 在区锁释放后等待。
- 首曲准备失败时，由异步流程发 track_failed、跳过失败条目；找到成功曲后用 format_boundary 交给接续会话，不能在 Open 返回后更改 Format。
- time、track_started、track_failed、stream_ended 和带分段的 signal 按流帧号到播放头才派发；格式边界、重建与处理速度立即派发。time 按 200 ms 网格补齐，回退不重发，播放头不超过已写编码器的帧数。初始条目不发 track_started；未完成渲染时 time 不派发已写区间的右端点，防止同帧换轨尚未登记。尾部 time 先于 stream_ended。
- 边界一经宣布不可撤销为同流拼接；NextChanged 重选只重复宣布接续流，无后继则要求重建，避免控制面残留旧 next_stream。分段路径由内部 `segment_signal` 按曲目起点登记，到播放头时转换为 `signal`。每次对外发完整路径快照，速度与削波变化不能覆盖 `quality`、`steps`。
- Events 容量 64，满则等待，取消可解除等待；自然 EOF 后仍保留播放头派发，编码错误则停止派发、Body 以错误结束。显式 Close 后 Body 为 EOF。

## 4. 校验与错误

| 条件 | 结果 |
| --- | --- |
| 来源不存在或非 present | not_found |
| 目录停用 | root_disabled |
| 非可渲染组合或缺少对应解码器 | unsupported_format |
| 安全打开失败、文件非普通文件或逃逸 | source_unavailable |
| 扫描大小/修改时间/容器不符，或 CUE 父路径/区间记录不完整 | source_changed |
| 解码启动失败、超时、空样本、非零退出 | decode_failed |
| 数据库/取消等非曲目错误 | transient，不冒充曲目失败 |
| 获取进程名额超时 | ErrBusy |
| 无可协商格式 | ErrNoStreamFormat |

`RenderError` 与 `SourceError` 提供 FailureReason/Unwrap；只暴露安全分类。ffmpeg 的 stderr 有界保存在 ProcessError.Stderr，普通日志不得输出它。WAV 要核对实际 PCM 子类型、有效位深、布局和解码器；扩展浮点 WAV 必须用 IsFloat 区分，不能只看扫描 Codec。

## 5. 正常、基准、失败示例

正常：两首同格式整数 FLAC 在关闭电平化时拼入一条 FLAC，量化不加抖动。基准：第二台设备协商 96 kHz/16 bit，不会改变第一台已协商的 192 kHz/24 bit 上限。失败：首曲被删除，通过事件跳过，不能在持区锁的 Open 中调用 Cursor 而死锁。

## 6. 必需测试

独立测试 agent 覆盖 design.md §11 与 design-delivery.md §2：16/24 位 PCM MD5、33% 起播、事件补格与自然/异常结束、定位、背压超时排除、协商矩阵、NextChanged 与格式边界、Close 并发。来源用合成文件与假 TrackSource，不需要数据库。

注入点：`render.Options.Clock/NewTicker` 控制播放头与派发；`ffmpeg.OpenWithOptions` 的 NewTimer 控制 5/10 秒等待；TrackSource 错误实现 FailureReason；假 Cursor 控制顺序。真实进程素材用固定 ffmpeg，安全打开与 app 数据读取另做最小数据库测试，不在 P-c 实现自查里启动数据库。

## 7. 错误与正确做法

错误：同步 Open → Cursor.Next；会话 Close 等待持区锁的 Cursor；拿读取 Body 的速度当播放头；以 source.BitDepth 为 0 推断所有源都是整数；只 Wait 不 Close 进程。

正确：会话协程取下一首，非阻塞取消与同步资源回收分开；Observe 驱动播放头；源整数/浮点分类与量化精度分开；在所有结束路径 Close 进程和源文件。生产 ffmpeg 不可用时保留 nil Renderer，不用测试假渲染器顶替。

## 8. 量化出口与信号监测（D-2 / P-e）

- 无损出口每块按 `Exact && 0 < SourceBits && SourceBits <= bits` 判定直通；其余走 TPDF。流尾冲刷同样量化，FLAC、WAV 与 HLS ALAC 共用字节出口，不再另写取整逻辑。
- 链 ① 使用 `settings.headroom` 装配；即使 0 dB，启用后也清除 `Exact`。电平化恰为 0 dB 时沿用 `leveling.Apply` 保留 `Exact` 的合同，不为实际直通的曲目虚报抖动。
- 信号按来源、解码兼容转换/降混、电平化、共享链步骤、抖动、编码侧转换、编码的实际顺序计算。质量取最差档，降位深的抖动标兼容转换；交叉淡化窗口的临时抖动不展示。无损 HLS ALAC 编码与 FLAC/WAV 同属 `lossless`。Opus 的固定 48 kHz 由编码步骤的输出采样率表达，不单列设备兼容转换（父设计 §6）。
- 量化器始终返回钳位数；仅开启 `clip_indicator` 时登记块起点的内部 `clip`。有损出口仅在此开关开启时统计 `abs(sample) > 1`，仍把浮点交给编码器，不额外钳位。
- 派发器独占信号状态：播放头到达 `clip` 时亮起，独立的 250 ms 时钟复评，最后一次削波满 450 ms 后熄灭；关闭开关时省略 `clipping`。实时更新保留正在播放的曲目路径，不采用尚未听到的下一段路径。
- 处理速度沿用累计音频时长/处理墙钟时长，量化到 0.1，值改变且距上次上报至少 1 秒才更新；预缓冲、领先上限、编码器背压和事件队列等待均不计。速度通过派发器合入当前路径后发出。

## 9. G-2 换轨与淡化

实现与验收依据：[gapless-crossfade 设计 §1–§6](../../tasks/11-08-gapless-crossfade/design.md)。策略在 `player`，不进入编码器、交付层或共享链。

- 预取时依次判断源格式边界、交叉淡化、`gapless`、`gap`。源格式不同比较扫描采样率、位深、声道数与样本类型；第一批布局按声道数默认值。格式边界立即发布，旧流读到曲尾，不发布 `stream_ended`。
- 曲尾用循环补足当前量子，覆盖极短曲目，不设任意换轨次数上限。下一首 33% 预缓冲或终态就绪；曲尾重新准备、进程名额和预缓冲共用最多 5 秒的可取消等待，通过 `Options.NewTicker(5*time.Second)` 注入，超时立即发布 `format_boundary{reason: "not_ready"}`。等待不计处理速度。
- 准备失败及空解码按曲尾帧号（不含之后已输出的间隔静音）登记 `track_failed`，通过 `Cursor.Next(失败条目)` 重取，连续五首停止并结束流。只有确认新曲目有 PCM 才清零失败计数，不能由仍在播放的旧曲目清零。
- 淡化只对不同发行、双方有效时长均大于窗口三倍且未过截断点的曲目生效。截断点向下对齐八帧，来源先电平化，再用 `dsp/fade.Mix` 跨量子混音，窗口 `Exact=false`；新分段和 `track_started: crossfade` 在窗口首帧登记。
- 到截断点下一首未就绪，旧曲目继续到真实结尾，不在截断点等预缓冲。若下一首已跳过开头静音，真实曲尾从 0 重新准备，避免普通换轨漏掉曲首。
- `gapless=false` 且不淡化时插入半秒精确零，静音之后登记 `track_started: gap`。纯静音块保留 `Exact=true`、`SourceBits=1`，防止量化器把未知位深当作需要抖动。
- `NextChanged` 未预取时无事；预取未进入链时关闭旧来源并立即重取重判；已经宣布边界时只更新接续流；已编码但未播放的换轨要求 `rebuild_needed`。`NextChanged` 从原子快照锁存通知时的换轨状态，不因渲染协程处理前播放头推进而漏掉重建；仍不等待会话锁。编码结束后不能恢复追加 PCM；无后继的已宣布边界沿用重建语义。

测试由独立测试 agent 补充设计 §6 的逐位拼接、边界、条件表、五秒超时、间隔与队列变化。实现自查只运行 `go build ./...`、`go vet ./internal/playback/render/...`、固定 ffmpeg 下的 `go test ./internal/playback/render/... -count=1`，不把既有测试通过视作新增场景已经覆盖。


## 10. P-g CUE 虚拟分轨

依据：dsp-pipeline 的 prd.md P12、design.md §4.1 / §4.2 / §10.1、implement.md P-g。

- `catalog.TrackSource` 增加 `CueParentRelativePath *string`、`CueIndexFrames/CueEndFrames *int32`，沿用 `TrackAnalysisCandidate` 的来源字段与单位。扫描器直接保存 parser 的 75 Hz 索引，没有换成样本帧；大小、修改时间、编码与音频参数取自父音频。
- `cue_virtual` 只在渲染表放行已支持的父音频编码，并仍按解码器能力过滤，交付类型为 `derived`。旧直出表不变；未知父编码仍拒绝。信号路径 `source` 使用父音频的 `Codec`，不把 CUE 当作编码。
- 分析工作者与播放入口共用 `sourceAudioPath` 和 `openScannedSource`。渲染仍按来源 present → 目录启用 → 格式表 → 允许根 → 安全打开父文件 → 大小/微秒修改时间 → 容器的顺序核对；CUE 父路径缺失返回 `source_changed`，不尝试打开虚拟路径。分析保留自身的错误分类，不新增播放格式限制。
- 渲染来源边界把起、止索引**分别**换算为 `int64(index) * int64(sampleRate) / 75`，区间为 `[start,end)`。非整除采样率向下对齐到源样本边界；不得改为对持续时间取整后加到起点。比如 32 kHz 的索引 1、2 分别为样本 426、853，中间段长 427 帧；前轨终点与后轨起点始终相同。缺少起点、负起点、倒置区间或缺少采样率返回 `source_changed`；结束索引为 nil 则一直解到父音频 EOF。
- `render/decoder.go` 复用 `pcmRange`；CUE 使用输入侧 `-copyts -ss <coarseSeconds> -noaccurate_seek` 粗定位，保留实际解码块的源绝对时间戳。滤镜依次为 `asettb=1/<源采样率>`、`atrim=start_pts=<start>[:end_pts=<end>]`、`asetpts=PTS-STARTPTS`，最后才做需要的重采样。`start_pts/end_pts` 因时间基为一个源样本而直接使用绝对样本位置，不从粗定位落点重新计数；不使用 `-start_at_zero` 或在截取前重置 PTS。
- 粗定位为 `max(0, start / sampleRate - 1)` 整秒，即通常向前留 1–2 秒，靠近文件开头时取 0。此余量不是容器块长度的上限：FFmpeg 7.1.1 输入 seek 的最大时间戳就是请求落点，解复用器向前找可解码块，因此长块由容器索引自身处理。例如 APE extra-high 的 294912 样本块在 44.1 kHz 下约 6.69 秒，不必再人为预解码整张专辑或额外退回一个最大块。整数秒避免微秒换算触及精确切点；`-noaccurate_seek` 不让输入侧另做精确丢弃，真实起止统一由绝对 PTS 截取。没有从头解码的回退分支。
- CUE 内 seek 先把曲内秒数四舍五入为样本帧，加到 CUE 起点，再走同一粗定位和绝对样本截取路径。结束索引为 nil 时不设置 `end_pts`，读取父文件 EOF；普通曲目保持原输入侧 `-ss`。
- 相对 design.md §4.2 的差异：CUE 的输入侧 `-ss` 只负责提前到达附近，不承担样本精确定位；用绝对 PTS 截取替代按输出字节数截断，并让重采样器自然冲刷。取消此前从文件开头解码的实现，避免靠后分轨的预解码时间随整轨位置增长；现有 5 秒换轨等待与 10 秒读取超时不变。
- G-2 的源格式比较、同发行不淡化、整数精确量化直接复用，不增加 CUE 专用换轨路径。关闭处理且输出格式不变时，相邻无损整数轨的源样本连续，保持 `Exact`。
- 必需验收由测试 agent 补充：非零起点、相邻两轨完整渲染后 PCM MD5、非整除采样率的共享切点、无结束索引到 EOF、父文件变更/逃逸拒绝、信号路径父编码。既有用例通过不代表这些新增端到端场景已经覆盖。

### P-g 粗定位的临时验证（2026-11-09）

- 固定 `ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg`（7.1.1）。WAV、FLAC、ALAC、AIFF、WavPack × 32/44.1/96 kHz × 16/24 bit 共 30 组，原近零切点及实际触发粗定位的非零切点均逐字节相等；同时核对相邻区间拼接、无结束帧到 EOF、非整样本秒数的曲内 seek。全部使用临时合成素材，未新增仓库测试。
- 性能素材为 70 分钟、44.1 kHz/16 bit 双声道的合成正弦加噪声 WAV，编码为 APE 3990、extra-high（4000），每压缩块 294912 样本。目标起点为第 60 分钟。新命令另外核对了该位置的相邻区间、曲内 seek 以及接近整轨结尾的无结束帧截取，均逐字节等于 WAV 对应区间。
- 测量口径为打开源文件至固定 ffmpeg 输出首个完整 32 KiB PCM 块，包含子进程启动，不包含 catalog 查询和进程名额等待。改前/改后交替各运行三次，不清除系统文件缓存；直接运行解码命令，不套生产 10 秒超时，以便测出旧命令的真实等待时间（生产旧路径会先超时）。预缓冲列表示读出 3.3 秒 PCM，而不是数据库/API 端到端耗时。

| 命令 | 首块 PCM 三次耗时（秒） | 首块中位数（秒） | 3.3 秒 PCM 中位数（秒） |
| --- | --- | --- | --- |
| 改前：从头解码，`start_sample/end_sample` | 28.860 / 28.522 / 29.043 | 28.860 | 28.886 |
| 改后：粗定位、绝对 PTS 截取 | 0.03145 / 0.03166 / 0.02941 | 0.03145 | 0.05665 |

性能测量输出的 PCM 同时与原 WAV 核对；这组数字只代表本机与该合成 APE，不承诺所有容器或设备的延迟上限。完整播放区新增回归仍由测试 agent 负责。

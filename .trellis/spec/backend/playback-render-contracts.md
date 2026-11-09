# 服务端渲染骨架合同（P-c）

## 1. 范围

`playback/render` 实现 `zones.Renderer`，通过消费方端口 `TrackSource` 获取已经安全打开的文件和分析结果；不导入 playback、catalog，不连接数据库。`render/ffmpeg` 是标准库叶子包。app 是唯一装配根。

本阶段只含同格式短读拼接，不含交叉淡化、换轨就绪超时、关闭无缝时的静音间隔（G-2）、Headroom 与完整信号路径（P-e）、CUE 放行（P-g）、派生退役（P-d）。旧 `Formats()` 和 HTTP 交付保持不变。

## 2. 签名

- `ffmpeg.Open(ctx, path) (*Tool, error)`：定位、版本、解码/编码清单、soxr 探测及内嵌 ALAC → PCM → FLAC → PCM 自检。
- `Tool.Reserve(ctx, n) (*Reservation, error)` 原子预留多个名额；`Reservation.Start(ctx, StartOptions)`、`Tool.Start(ctx, StartOptions)` 返回 `*Process`。
- `Process` 实现 Read/Write/Wait/Close，另有 CloseInput、Stdout。Source 经 fd 3 借用，所有者仍须关闭原句柄。自然结束先 CloseInput、读到 EOF、Wait，最后必须 Close。
- `render.New(Options{FFmpeg, Sources, Clock, NewTicker, Logger}) *Renderer`；实现 zones 两个端口；`Renderer.Close()` 等待资源回收。
- `TrackSource.Prepare(ctx, userID, trackID) (Track, error)`；Track 包含源格式、IsFloat、文件、时长、发行、首尾静音、曲目/专辑增益。`CueStartFrame/CueEndFrame` 以源 PCM 帧计，是未来截取挂点，不是 CUE 75 Hz 索引。
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
- 边界一经宣布不可撤销为同流拼接；NextChanged 重选只重复宣布接续流，无后继则要求重建，避免控制面残留旧 next_stream。分段 signal 的 Signal 为 nil，速度更新只含 processing_speed，完整质量与步骤留给 P-e。
- Events 容量 64，满则等待，取消可解除等待；自然 EOF 后仍保留播放头派发，编码错误则停止派发、Body 以错误结束。显式 Close 后 Body 为 EOF。

## 4. 校验与错误

| 条件 | 结果 |
| --- | --- |
| 来源不存在或非 present | not_found |
| 目录停用 | root_disabled |
| 非可渲染组合、CUE 或缺少对应解码器 | unsupported_format |
| 安全打开失败、文件非普通文件或逃逸 | source_unavailable |
| 扫描大小/修改时间/容器不符 | source_changed |
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

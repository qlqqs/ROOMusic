# 技术设计：DSP 处理链与服务端渲染

需求以 [prd.md](./prd.md) 为准；跨子项的架构（包布局、缓冲、节点接口、增益区域、两段式装配、第一批的格式适配、端口补充建议）在[父任务 design.md](../11-08-audio-dsp-reference-port/design.md)，下文写作“父设计 §N”。
编码与交付、`Negotiate` 规则、iOS 交付小验证在 [design-delivery.md](./design-delivery.md)（章节号与本文共用）。播放区线的定稿设计称“zones 设计”。参考稿只引章节号，写作“参考稿 §N”。

## 0. 结论

- `playback/render` 实现 zones 的 `Renderer` / `RenderSession`，用 zones 的 `Cursor` 取下一首。一个会话 = 一条流 = 一条共享处理链 + 一个编码器 + 一个事件派发器，外加 1–2 个每曲来源段。
- 解码用 ffmpeg 子进程，输入是 `playback` 安全打开的文件描述符，输出整数源 `s32le`、浮点源 `f32le`；起始与停滞各 10 秒超时，全局进程数有上限。
- 每曲 10 秒预缓冲，33% 起播；渲染循环每次处理一个量子（`采样率/5`，200 ms），最多领先播放头 20 秒。
- 未来事件队列按“链输出帧号”登记，播放头 = 输出端回报 + 墙钟外推；`time` 事件按 200 ms 网格由分段表现场合成。
- 电平化选择照搬 `leveling.ts`，增益值沿用 `catalog` 现有算法；按增益区域在来源段末尾施加。
- Headroom 设置存在 `playback_zones.headroom`（本线迁移加的 jsonb 列）；削波与处理速度经 `signal` 事件上报。
- 派生缓存、预检 202 等待整体删除；格式表改为“渲染会话能解码的组合”。

## 1. 文件布局

| 文件（`backend/internal/playback/render/`） | 内容 |
| --- | --- |
| `renderer.go` | `New(Options)`、`Negotiate`、`Open`、`Close`（应用关停时关闭所有会话） |
| `source.go` | 消费方端口 `TrackSource`、曲目准备（读信息、安全打开、选增益） |
| `decoder.go` | 每曲来源段：ffmpeg 解码、预缓冲环、超时 |
| `player.go` | 取帧入口 `read(frames)`（参考稿 §7.1 的顺序）；换轨与淡化的挂点由 gapless-crossfade 实现 |
| `session.go` | 会话：渲染循环、链、量化、编码器、`Body`、生命周期 |
| `events.go` | 未来事件队列与派发器、分段表 |
| `encode.go`、`negotiate.go` | 编码器与协商（design-delivery.md §1、§2） |
| `signal.go`、`monitor.go` | 信号路径、削波与处理速度 |
| `playback/render/ffmpeg/` | 定位与探测、进程组、超时读取、进程上限（从 `playback/derivation` 搬来定位与解码器清单解析，补编码器清单与 soxr 探测） |

## 2. 会话内部结构

```text
                        Cursor.Next（取下一首）
                              │
   ┌─来源段 A（当前曲）───────────┐        ┌─渲染循环（会话唯一的处理协程）────────────────────────────┐
   │ ffmpeg 解码 ─► 预缓冲环 10 s │──┐     │ 等待：A 预缓冲 ≥33% 且 领先播放头 < 20 s                    │
   └─────────────────────────────┘  ├───► │ player.read(量子) ─► 链 ─► 量化/抖动 ─► 写编码器 stdin（阻塞=背压）│
   ┌─来源段 B（下一首，按需打开）──┐  │     │ 登记事件（帧号）、更新分段表、统计处理速度                       │
   │ ffmpeg 解码 ─► 预缓冲环 10 s │──┘     └───────────────────────────────────────────┬────────────┘
   └─────────────────────────────┘                                                  │
                                                      编码器 ffmpeg（WAV 时是 Go 写头）stdout ─► Body ─► zones/httpapi ─► 输出端
   派发器协程：Observe 更新播放头；按播放头派发队列里的事件 ─► Events()
```

- **协程**：每个来源段一个读协程（参考稿 §7.3 的“预缓冲线程”），一个渲染循环，一个派发器；编码器是子进程。渲染循环是唯一调用链与量化器的地方，所以链与量化器不加锁。
- **背压**：编码器 stdin 写满就阻塞渲染循环；HTTP 不读，编码器 stdout 就满。暂停 5 秒内恢复时流原样续上（zones 设计 §3.5）。
- **领先上限 20 秒**：播放头之前已渲染的时长超过 20 秒时渲染循环等待（每 100 ms 复查）。理由：浏览器可能缓冲很多分钟，领先太多会让“改了下一首”大多变成 `rebuild_needed`；参考稿的发送队列上限是 10 秒，这里给输出端留出 20 秒是为了网络抖动。还没有任何播放头回报时，以流开头为播放头。

## 3. 端口的实现

### 3.1 构造

```go
type Options struct {
	FFmpeg  *ffmpeg.Tool  // 已通过自检（§10.2）
	Sources TrackSource   // app 用 playback.Service + catalog.Store 实现
	Clock   func() time.Time
	Logger  *slog.Logger
}
func New(options Options) *Renderer
```

### 3.2 `Open` 的顺序

1. 取进程名额（§4.4）；忙时返回 `ErrBusy`（zones 按 `transient` 处理，父设计 §8 第 4 条）。
2. 准备起始条目（§4.1）：失败就登记 `track_failed`（帧 0）并 `Cursor.Next` 取下一首，连续 5 首都失败就结束（登记 `stream_ended`，`Body` 立即 EOF）。
3. 按首曲的源格式、`spec.Format` 与设置确定具体流格式（design-delivery.md §2.2）。
4. `chain.Build` 装配链（父设计 §5.2），启动编码器，打开首曲的来源段（从 `StartSeconds` 起）。
5. 启动渲染循环与派发器，返回。`Open` 不等预缓冲：`Body` 的第一次读会阻塞到首个量子编码完成。

### 3.3 方法语义

| 方法 | 语义 |
| --- | --- |
| `Format()` / `MimeType()` | 具体流格式（父设计 §8 第 1 条） |
| `Body()` | 编码器输出；只能被一个读者读一次；会话关闭或流结束时 EOF |
| `Observe(s)` | 记下播放头样本（流内秒数、采样时刻、是否在放），唤醒派发器；不阻塞 |
| `NextChanged()` | 置标志，由渲染循环在下一个量子前处理（gapless-crossfade §5）；不阻塞 |
| `Events()` | 带 64 条缓冲的通道；会话关闭时关闭 |
| `Close()` | 可重复；取消上下文，杀掉所有子进程（进程组），关闭 `Events`，`Body` EOF |

## 4. 解码（P3）

### 4.1 曲目来源端口

```go
type TrackSource interface {
	// Prepare 读扫描记录与分析结果，并安全打开文件（目录启用、在根内、仍是扫描时的文件）。错误按 zones 的失败原因分类。
	Prepare(ctx context.Context, userID, trackID string) (Track, error)
}
type Track struct {
	File                          *os.File // 调用方关闭
	SourceKind, Codec             string
	SampleRate, Channels, BitDepth int     // 扫描记录；BitDepth 0 表示浮点或未知
	DurationSeconds               float64
	ReleaseID                     string
	LeadingSilence, TrailingStart *float64 // 秒；分析没有时为 nil（淡化用）
	TrackGainDB, AlbumGainDB      *float64 // 分析结果；nil 表示未知（电平化用）
	CueStartFrame, CueEndFrame    *int64   // 只有 CUE 虚拟轨（父设计 §12 的裁决）
}
```

`app` 的实现：`playback.Service.OpenForRender`（复用 `resolve` 与 `openVerified`，再加一步“格式表里可渲染”）+ `catalog.Store.LoadTrackAnalysis`（`analysis.Version`）。失败原因映射：

| 错误 | `PlaybackFailureReason` |
| --- | --- |
| 曲目不存在或不是 present | `not_found` |
| 目录停用 | `root_disabled` |
| 不在可渲染格式表（§10.1） | `unsupported_format` |
| `ErrSourceUnavailable` | `source_unavailable` |
| `ErrSourceChanged` | `source_changed` |
| ffmpeg 起不来、超时、非零退出、没有样本 | `decode_failed` |

### 4.2 命令

```text
ffmpeg -nostdin -hide_banner -loglevel error -threads 1 -protocol_whitelist file,pipe
       [-ss <起始秒>] -i file:/dev/fd/3 -map 0:a:0 -vn
       [-af aresample=resampler=soxr:precision=28]      # 只在兼容性转换时（父设计 §6）
       -ar <链采样率> -ac <链声道数> -f s32le|f32le pipe:1
```

- 文件以 `ExtraFiles` 交给 fd 3（沿用分析器的做法），不经过路径再打开。偏离参考稿 §5.7 的理由见父设计 §10。
- 总是带 `-ar`/`-ac`：与源相同时 ffmpeg 不做任何转换（逐位不变）；扫描记录与实际不符时也保证输出与链的格式一致，而不是让链收到错误格式的样本。
- 整数源（FLAC、ALAC、APE、WavPack、整型 WAV/AIFF）用 `s32le`，缓冲 `Exact = true`、`SourceBits = 扫描位深`（0 时按 32 处理，量化时就会加抖动）；兼容性转换或降混时 `Exact = false`。浮点与有损源（MP3、AAC、Vorbis、Opus、浮点 WAV）用 `f32le`，`Exact = false`。
- **定位**：`StartSeconds > 0` 时用输入侧 `-ss`。定位不要求逐位，只要求准；无缝拼接的下一首总是从 0（或 CUE 起点）开始解。
- **CUE 虚拟轨**（若裁决通过）：`-ss` 到 `CueStartFrame` 对应的时刻，我们自己数帧，读满 `CueEndFrame − CueStartFrame` 帧就当作 EOF（不用 `-t`，避免 ffmpeg 的时间舍入）。
- 进程组与终止沿用 `analysis/process_unix.go` 的做法（`Setpgid`、取消时杀进程组、`WaitDelay` 2 秒）；优先级保持正常（渲染是实时的，不像分析那样降到最低）。

### 4.3 读取与格式换算

读协程按 4 字节/样本读 stdout，交错转 planar：`s32le` 为 `v / 2³¹`，`f32le` 直接转 float64。字节存进预缓冲环（§5.1），读出时再转换，环里不存 float64（省一半内存）。

### 4.4 超时与并发上限（参考稿 §14 第 4、5 条）

- **起始超时 10 秒**：进程启动后 10 秒内没有任何输出就杀掉，`decode_failed`。
- **停滞超时 10 秒**：读协程在等数据（环没满、没到 EOF）时 10 秒没读到字节就杀掉，`decode_failed`。环满造成的等待是背压，不计时。
- **进程上限**：`ffmpeg.Tool` 持有一个全局信号量，解码与编码进程共用，容量 `3 × GOMAXPROCS`（八号机 36，约 12 个同时出声的播放区，每个最多 2 个解码 + 1 个编码）。取名额最多等 5 秒；开流时取不到返回 `ErrBusy`，准备下一首时取不到就当作“下一首没准备好”（gapless-crossfade G3 的超时路径）。不做配置项：家用规模用不到，需要时再加。
- 停止时（曲目放完、会话关闭）先关读端再等进程退出，进程名额在 `Wait` 之后归还。

## 5. 预缓冲、拉取与量子（P2、P4）

### 5.1 预缓冲环

- 每个来源段一个环，容量 = 10 秒的原始字节（`采样率 × 声道 × 4 × 10`；44.1 kHz 立体声约 3.5 MB，192 kHz 约 15 MB）。
- **就绪**：环里 ≥ 33% 容量（3.3 秒），或解码已到 EOF（短曲目）。渲染循环在首曲就绪前不产出任何字节；这段等待不计入处理速度（参考稿 §7.3）。
- 环由读协程写、渲染循环读，一把互斥锁加两个条件变量，读写都按块拷贝。

### 5.2 渲染循环与量子

每次迭代：

1. 处理 `NextChanged` 标志（gapless-crossfade §5）。
2. 等待“领先 < 20 秒”。
3. `buf := player.read(rate/5)`。`read` 按参考稿 §7.1 的顺序：确保当前曲目就绪 → 格式检查 → 淡化截断 → 读样本 → 淡出/交叉混音 → 异常映射（解码失败登记 `track_failed`）→ 短读换轨并递归补读 → 登记事件。第 3、5、7 步由 gapless-crossfade 实现，dsp-pipeline 先给出“不淡化、曲末短读即结束或接下一首（格式相同才接）”的最小实现。
4. `chain.Process(buf)` → 量化（无损档）或转 `f32le`（有损档）→ 写编码器。
5. `read` 返回 0 帧且没有下一首：`chain.Drain()`、量化、写完、关闭编码器 stdin，登记 `stream_ended`（格式边界时不登记，见 gapless-crossfade §1）。

量子是 `链采样率/5` 帧（参考稿 §7.2）。短读拼接后一个量子可能跨两首，所以事件按帧号登记，不按量子。

### 5.3 什么时候取下一首

渲染位置进入当前曲目的最后 `10 秒 + 淡化时长`（不淡化时为 10 秒）时，调用 `Cursor.Next(当前条目)` 取下一首并打开它的来源段（预缓冲在当前曲目剩余的时间里填满）。`Cursor.Next` 返回 false 就记“没有下一首”。取走即“已准备”（zones 设计 §14）。

## 6. 未来事件队列（P5，参考稿 §7.4）

### 6.1 帧号

- **流帧号** = 链输出（编码器输入）累计的帧数，按链采样率计。延迟已在链内补偿（父设计 §3.2），所以“第几帧”与“流内第几秒”一一对应：`秒 = 帧 / 链采样率`。有损档的编码器重采样不改变时间。
- 每条登记的事件带帧号：`track_started`（下一首的第一帧进入链的位置，或交叉淡化的起点）、`track_failed`、`stream_ended`（最后一帧之后）、`segment_signal`（分段的信号路径，§9）、`clip`（§8.2）。

### 6.2 播放头

```text
播放头帧 = 最近一次 Observe 的 stream_seconds × 链采样率
          + (现在 − 采样时刻) × 链采样率        仅当 playing
          并且不超过已写入编码器的帧数
```

- 回报每 10 秒一次，中间靠墙钟外推；回报校正时播放头可能回退一点：已派发的事件不重发，派发位置只增不减。
- 有损档的编码器延迟（AAC 的前导约 2112 样本）不修正，位置偏差约 45 ms，可以接受。

### 6.3 派发

- 派发器在播放中每 50 ms 醒一次（`Observe` 时也立即醒），把帧号 ≤ 播放头的事件按顺序发到 `Events()`。
- **`time` 事件现场合成**：每当播放头越过一个 200 ms 网格点（流帧号 `k × rate/5`），按分段表算出“哪一条、曲目内第几秒”发一条 `time`。播放头一次跨过多个网格点（外推落后、回报校正向前跳）时逐个补发（参考稿“跨块补插”）。这与“把每条 time 预先放进队列”的派发结果完全相同，只是不存它们。
- 通道满（zones 没读）时派发器阻塞等待，不丢事件。

### 6.4 分段表

渲染循环每接上一首就追加 `{entry_id, stream_start_seconds, track_start_seconds}`；播放头越过下一段的起点后删掉上一段。事件带上当前的分段表（zones 设计 §3.4 的 `Segments`）。

### 6.5 立即事件

`format_boundary`、`rebuild_needed`、削波与处理速度变化的 `signal` 不进队列，产生时立即发出（父设计 §8 第 2 条）。

### 6.6 结束

- **自然结束**：编码器输出全部写完后，播放头继续外推，到达流尾时派发剩余事件（含 `stream_ended`）。输出端回报 `ended` 时 zones 收尾（zones 设计 §3.3）。
- **出错结束**（编码器异常退出、写失败）：清空队列，不再派发，`Body` 以错误结束；输出端会回报 `stream_lost`，zones 从播放头位置续一条新流。

## 7. 电平化（P6）

### 7.1 选择规则（`dsp/leveling`，移植 `frontend/src/features/player/engine/leveling.ts`）

```go
type Mode string // "off" | "auto" | "track" | "album"
const UnknownGainDB = -5.0
type Basis string // "track" | "album" | "unknown"
// Choose 返回要施加的增益；mode 为 off 时 ok 为 false（不施加）。
func Choose(mode Mode, trackGainDB, albumGainDB *float64, albumContext bool) (gainDB float64, basis Basis, ok bool)
func Linear(db float64) float64 // 10^(db/20)
```

- `track`：曲目增益，没有就 −5 dB（`unknown`）；`album`：专辑增益，没有（专辑没分析完）就退回曲目规则；`auto`：`albumContext` 为真按 `album`，否则按 `track`；`off`：不施加。
- 增益值本身沿用 `catalog` 的现有算法（`audio-analysis-contracts.md`）：曲目 `min(−14 − I, −0.5 − TP)`，专辑按时长加权的能量平均响度与最大真峰值同式计算；可以为正，不截断、不压限。
  与参考稿 §6.5 的差别（−23 基准加偏移等价于目标 −14；钳制留 0.5 dB 且衰减时也不越过）记在父设计 §10，第一批不改，避免升级后响度变化。
- `albumContext` 来自 `Entry.AlbumContext`（zones 计算）；“发行上下文”三条用例归 zones。

### 7.2 施加

来源段准备时算出这首的线性增益，读出的每块带一个覆盖整块的区域，来源段末尾 `leveling.Apply` 施加并清空（父设计 §4）。`off` 时不生成区域，样本与 `Exact` 都不变。

### 7.3 退役

`leveling.ts`、`audio-graph.ts` 与它们的测试随 zones F2 删除（zones implement.md F2 第 5 步）；本线只移植规则与用例。

## 8. Headroom、削波与处理速度（P7、P8）

### 8.1 Headroom

- 设置：`{enabled: false, db: −3.0, clip_indicator: false}`，`db` 取值 −30.0…0.0、步进 0.1（存一位小数的值，服务端按步进校验）。
- 存储：本线迁移 `00NN_playback_zone_headroom.sql`（排在 zones 的 0037 之后，合入 main 时顺延）给 `playback_zones` 加 `headroom jsonb NOT NULL DEFAULT '{"enabled":false,"db":-3.0,"clip_indicator":false}'`，带 `.down.sql`。用 jsonb 是为了与第二批的复杂设置同形（它们也会各占一列 jsonb）；写入前按范围与步进校验；列只由本程序写入，读取时直接解码，不另设回退。
- 节点：链 ① 的 `chain.Headroom`，乘 `10^(db/20)`，清 `Exact`。启用后整条流都不是逐位（父任务“启用后强制浮点链”在这里的含义就是：样本被改过，量化时加抖动）。
- 设置接口：`set_settings` 接受 `headroom` 键（zones 合同 §2.2 加键，父设计 §8 第 7 条）；改动由 zones 去抖重建。

### 8.2 削波指示（参考稿 §6.10）

- 统计点：无损档的量化器（dither T4，浮点转整型时钳位的样本数）；有损档在交给编码器前统计 |x| > 1 的样本（编码器内部会把它们削掉，等同于第一处）。DSD 的 SDM 是第三批。
- 只在 `clip_indicator` 开时统计。每块的削波数 > 0 时登记一条 `clip` 事件（帧号 = 块起点），0 不登记。
- 派发器收到 `clip` 记下“最后一次削波的时刻”；一个 250 ms 定时器复评：距最后一次 < 450 ms 为“在削波”。状态变化时立即发 `signal`（只改 `clipping` 字段）。效果：最后一次削波后约 0.45–0.7 秒熄灭，不闪。

### 8.3 处理速度

- 渲染循环累计“处理的音频时长”与“花在读样本、链、量化上的墙钟时长”；等预缓冲、等领先上限、等编码器背压的时间都不计。
- 速度 = 音频时长 / 墙钟时长，量化到 0.1；与上次上报的值不同且距上次 ≥ 1 秒才发 `signal`。

## 9. 信号路径（P11）

### 9.1 结构（zones 的 `signal` 字段原样携带）

```text
Signal = {
  quality: "lossless" | "enhanced" | "high" | "low",   // 各步里最差的一档
  steps: SignalStep[],
  clipping: boolean?,          // 只在削波指示开着时有
  processing_speed: number?    // 倍实时，一位小数
}
SignalStep = {
  kind: "source" | "convert" | "downmix" | "leveling" | "headroom" | "dither" | "encode",   // 第二批增加 peq、convolution、dsd_to_pcm、sample_rate
  quality: "lossless" | "enhanced" | "high" | "low",
  lossy: boolean,
  compat: boolean,             // 为了输出端能力做的兼容性转换
  params: object               // 按 kind，见下表
}
```

| kind | params | 评级 |
| --- | --- | --- |
| `source` | `{codec, sample_rate, bit_depth?, channels}` | 无损源 `lossless`，有损源 `low` |
| `convert` | `{from_rate, to_rate, by: "ffmpeg"}` | `high`，`compat: true` |
| `downmix` | `{from_channels, to_channels, by: "ffmpeg"}` | `high`，`compat: true` |
| `leveling` | `{gain_db, basis}` | `enhanced` |
| `headroom` | `{gain_db}` | `enhanced` |
| `dither` | `{bits, kind: "tpdf"}`（只在这首确实会被抖动时出现；降位深时 `compat: true`、`high`） | `enhanced` 或 `high` |
| `encode` | `{codec, sample_rate, bit_depth?, bitrate_kbps?}` | FLAC/WAV `lossless`，Opus/AAC `low`、`lossy: true` |

- 评级从好到差：`lossless`（逐位）→ `enhanced`（用户开的处理）→ `high`（为兼容做的转换）→ `low`（有损）。交叉淡化是瞬时的，不进信号路径。
- 信号路径**按分段**：每接上一首就算出这首的路径，登记一条 `segment_signal` 事件（帧号 = 这首的起点），播放头到达时作为 `signal` 派发，所以显示的总是正在放的那首。
- 输出设备那一步由前端按设备名补上，不进服务端结构。

### 9.2 界面（前端子步骤，F3 之后）

- 播放栏加一个信号路径图标按钮（颜色按 `quality`，无边框），点开是一列步骤：每行一个图标和一行短文字，如“FLAC 44.1 kHz 16 bit”“电平化 −3.2 dB”“TPDF 抖动 24 bit”“Opus 128 kbps”“iPhone · Safari”。有损的行标“有损”，兼容性转换的行标“兼容转换”。来源之后保留已上线的响度一行（积分响度、响度范围、峰值，没有分析结果时不显示，不参与评级）。处理速度一行（“处理速度 85.3×”）；削波时图标变成警示色。
- “设置 › 播放”加 Headroom：开关、增益滑块（−30…0 dB，0.1）、“削波指示”开关。
- 不写说明句（全局文案约束）。

## 10. 派生退役（P10）

### 10.1 格式表

- `playback/formats.go` 的直出表保留（旧标签页用的 `GET /tracks/{id}/playback`、`/audio` 下一版才删，zones 设计 §8）；`derivedFormatTable` 改为**可渲染表**：每项写来源类别、编码、解码 ffmpeg 要的解码器名，只发布 ffmpeg 有对应解码器的项。直出六项也都是可渲染的。
- `GET /api/v1/playback/formats` 返回可渲染的组合，字段不变：原来派生的几项仍是 `delivery: "derived"`（含义变为“服务端处理后交付”），这样升级时已打开的旧标签页解码不会出错。新前端只用它判断“能不能放”（zones 设计 §5.10）。
- 首批新增的可渲染组合只加 ffmpeg 自检能证明的：浮点 WAV、`wav_extensible` 的整数与浮点 PCM；其余未知组合仍不可渲染。CUE 虚拟轨按父设计 §12 的裁决。

### 10.2 启动

- `app.OpenDerivation` 换成 `app.OpenRenderer`：定位 ffmpeg、取版本、解码器与编码器清单、探测 soxr，再用内置的 `selftest.m4a`（搬到 `render/ffmpeg/testdata`，`go:embed`）走一遍“解码 → FLAC 编码 → 再解码”，核对 PCM 的 MD5 与直接解码相同。
  任一步失败记 `playback.render.unavailable{reason}`，应用照常启动，起播类命令得到 zones 的 `503`（zones implement.md B2 第 5 步）。成功记 `playback.render.ready{ffmpeg_version, encoders, soxr}`。
- 删除 `<ROOMUSIC_DATA_DIR>/media-cache`：只在目录存在时删一次，删除前沿用“缓存目录与音乐根不重叠”的检查，日志 `playback.derivation.cache_removed{bytes}`。
- `ROOMUSIC_MEDIA_CACHE_MAX_MIB` 从配置里删除；仍设置时忽略（不让配置加载失败）。README 删掉这一行，加一句“升级后会自动删除旧的 ALAC 转码缓存”。

### 10.3 删除

`playback/derivation/`、`playback/derived.go` 与测试、预检里的 202 等待与 `derivation_*` 错误码；`playback-derivation-contracts.md` 改写为“渲染格式表与 ffmpeg 自检”一节并入新的渲染合同（E 阶段的 spec 更新）。

## 11. 测试范围（只测改动直接影响的行为）

- `dsp/leveling`：把 `leveling.test.ts` 的 8 条选择用例与“未知 −5 dB、正增益不截断”移植成 Go 表驱动测试；`Apply` 一条（0 dB 时 `Exact` 保留，非 0 时清掉）。
- `dsp/chain`：适配器的延迟补偿（一个固定延迟的假处理器：输出与输入逐样本对齐、总帧数相等）；区域按绝对帧号搬运（2:1 的假变率处理器，边界不漂移）；链头遇到区域时报错。
- `playback/render`（假 `TrackSource` + ffmpeg 生成的合成文件，不需要数据库）：
  - **逐位一致**：电平化关、Headroom 关时，16 bit 与 24 bit FLAC 源渲染成 FLAC 流，再解码的 PCM MD5 等于源解码的 MD5（prd 验收第 1 条）。
  - 事件队列：给定播放头样本序列，`time` 每 200 ms 一条、跨多格时补齐、`track_started` 在第二首起点派发、自然结束时剩余事件都发出、出错结束时不再派发。
  - 预缓冲：首曲 33% 前 `Body` 不出字节。超时：一个不出数据的假 ffmpeg（脚本）10 秒后 `decode_failed`（时钟注入，不真等）。
  - Headroom：开启后信号路径出现 `headroom`；人为放大到削波时出现 `clipping: true`，约 0.5 秒后变回 false（时钟注入）。
  - 定位：`StartSeconds` 为 30 时第一条 `time` 的曲目秒数在 30 附近（±50 ms）。
  - 派生退役：旧缓存目录被删除且日志一条；格式表包含 ALAC 等。
- 不测：ffmpeg 自身的解码正确性、编码器的音质、浏览器行为（E 与 iOS 验证覆盖）。处理速度只测“预缓冲等待不计入”一条。

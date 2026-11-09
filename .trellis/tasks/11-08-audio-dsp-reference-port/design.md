# 技术设计：音频算法对标（父任务）

需求与裁决以 [prd.md](./prd.md) 为准。本文写跨子项的架构：包布局、样本与缓冲、节点接口、增益区域、处理链的装配与重建、第一批的格式适配、批次与上线顺序、与播放区线的对接、对照验证的做法。
各子项的细节在各自的 design.md：[dsp-pipeline](../11-08-dsp-pipeline/design.md)（渲染会话、解码、事件队列、电平化）与它的 [design-delivery.md](../11-08-dsp-pipeline/design-delivery.md)（编码交付、协商、iOS 验证）、
[gapless-crossfade](../11-08-gapless-crossfade/design.md)、[dither](../11-08-dither/design.md)。
参考稿正本在本机 `~/.local/share/roomusic-reference/reference-design.md`（不进仓库），本文只引用章节号与我们自己的概括，写作“参考稿 §N”；本文自己的章节写作“§N”。
播放区线的定稿设计（[design.md](../11-08-server-playback-zones/design.md) §14、[design-protocol.md](../11-08-server-playback-zones/design-protocol.md) §3、[design-contract.md](../11-08-server-playback-zones/design-contract.md)）是本线的接口约束，下文称“zones 设计”。

## 0. 结论

- 所有算法是 `backend/internal/dsp/<名字>` 下的**纯包**（不导入项目内任何包），样本一律 `float64`、按声道分开（planar），块长可变。
- 渲染会话在 `backend/internal/playback/render`，实现 zones 的 `Renderer` 端口；它把“每曲来源段（解码 → 预缓冲 → 电平化）→ 换轨与淡化 → 共享处理链 → 定点量化 → 编码”串成一条跨曲目的流。
- **两段式装配**：电平化按曲施加在混音之前（交叉淡化时两首各有各的增益）；处理链（参考稿 §6.1 的固定顺序）是整条流共用的一份，跨曲目连续运行，所以无缝衔接处滤波器状态不断。
- **链在会话内不变**：开流时按“首曲的源格式 + 协商出的流格式 + 播放区设置”装配一次；设置改动、定位、格式变化都由 zones 关旧会话开新会话（300 ms 去抖在 zones）。节点因此不需要处理格式变化。
- **固有延迟在链内补偿**：适配器在流头丢掉延迟样本、在流尾补零冲刷，链对外表现为零延迟，帧号与位置不需要任何偏移；这同时回答卷积 V7“是否申报延迟”。
- **逐位一致靠两条规则**：整数源经 float64 表示是精确的；缓冲带 `Exact` 标记，量化器只在“样本被改过或要降位深”时加抖动，否则逐位还原。
- **第一批没有自研采样率转换**：有损档的 48 kHz 由编码 ffmpeg 转，无损档超出输出端上限（第一批只有罕见的 352.8/384 kHz 源）由解码 ffmpeg 转，多声道超出上限由解码 ffmpeg 降混；都在信号路径标成“兼容性转换”。第二批的采样率转换与降混把它们换成链内的 ② ③ 节点（§6）。
- 对 zones 端口有 7 条小的补充建议（§8），都不改协议形状。

## 1. 包布局与依赖方向

| 包 | 职责 | 允许导入 | 批次 |
| --- | --- | --- | --- |
| `dsp/chain` | 格式、缓冲、增益区域、节点接口与适配器、§6.1 顺序的装配、Headroom 节点、信号路径步骤的描述 | `dsp/*` 的算法包 | 一 |
| `dsp/leveling` | 电平化增益选择（从 `frontend/.../engine/leveling.ts` 移植）与区域施加 | 无 | 一 |
| `dsp/fade` | 淡入淡出曲线、交叉混音、8 样本对齐 | 无 | 一 |
| `dsp/dither` | 31 位 LFSR、TPDF、定点量化与削波计数 | 无 | 一 |
| `dsp/src`、`dsp/peq`、`dsp/convolution`、`dsp/downmix`、`dsp/dsd` | 第二批算法 | 无 | 二 |
| `dsp/sdm` | PCM→DSD 调制 | 无 | 三 |
| `dsp/internal/refdata` | 只给测试用：定位本机参考数据目录，不存在就 `t.Skip`（§9） | 无 | 二 |
| `dsp/internal/fft` | 只给 `dsp/*` 用的实数 FFT（2 的幂长度）：卷积的分块重叠相加、SRC 最小相位核的倒谱 | 无 | 二 |
| `dspsettings` | 第二批处理设置的服务：PEQ 文档校验与换算、成员 PEQ 预设、OPRA 目录、IR 文件存取与装载、各设置的 jsonb 编解码；HTTP 在 `dspsettings/httpapi` | `dsp/peq`、`dsp/convolution`、`dsp/src`、`playback/render/ffmpeg`、`platform/*`（`zones`、`playback/render`、`app` 导入它） | 二 |
| `playback/render/ffmpeg` | 定位与探测 ffmpeg（版本、解码器、编码器、soxr）、进程组、超时、并发上限 | 无 | 一 |
| `playback/render` | `zones.Renderer` 的实现：会话、来源段、换轨与淡化、事件队列、量化、编码、协商、信号路径 | `zones`、`dsp/chain`、`dsp/leveling`、`dsp/fade`、`dsp/dither`、`playback/render/ffmpeg`、`platform/uuid` | 一 |
| `playback` | 保留格式表与安全打开；新增给渲染用的 `OpenForRender`；删除派生 | `catalog`、`library`、`playback/analysis` | 一（改） |
| `app` | 装配：曲目来源与分析读取的适配器交给 `render.New`，`render.Renderer` 交给 `zones.NewService` | 增加上面的包 | 一 |

- `render` 不导入 `playback` 与 `catalog`：它自己定义消费方端口 `TrackSource`（按曲目取格式、时长、发行、首尾静音、分析增益，并安全打开文件），由 `app` 用 `playback.Service` 与 `catalog.Store` 实现。这样渲染会话的测试用合成文件就能跑，不需要数据库。
- `render` 导入 `zones` 只为端口类型（消费方定义接口，实现方引用），不产生环：`zones` 不导入 `render`。
- `dependency_rules_test.go` 按上表加规则；`playback/derivation` 随派生退役删除（dsp-pipeline P10）。

## 2. 样本、缓冲与格式

```go
// dsp/chain
type Format struct {
	SampleRate int
	Channels   int
	Layout     uint32 // 声道布局掩码，位序即声道序号（降混 D2）；第一批按声道数取默认布局
}

type Buffer struct {
	Samples    [][]float64  // planar：len == Channels，每声道至少 Frames 个样本，满刻度为 ±1.0
	Frames     int
	Regions    []GainRegion // 尚未施加的增益区域（§4）
	Exact      bool         // 样本仍是源的原值：任何改动样本的节点都要清掉
	SourceBits int          // 整数源的有效位深（16/24/32）；浮点源为 0
}
```

- **为什么用 float64 也能逐位一致**：解码 ffmpeg 输出 MSB 对齐的 32 位整数（参考稿 §5.4 的整型表示），换成 `v / 2³¹`。任何 ≤32 位的整数都能被 float64（53 位尾数）精确表示，除以 2 的幂也精确；量化器在 `Exact` 时做反向换算，得到的整数与源相同。浮点源（MP3、AAC、Vorbis、Opus 的解码结果）用 32 位浮点输出，换成 float64 同样无损。
- **满刻度**：±1.0。超过的值在量化时钳位并计入削波（dither T4）。
- **缓冲归属**：调用方持有输入；节点返回的缓冲在下一次调用前有效，可以就是输入本身（就地处理）。链内复用缓冲，稳态不分配。
- **DSD**（第二批）：DSD 字节流不进 `Buffer`。DSD→PCM 是每曲来源段里的一步（字节进、`Buffer` 出），不是链节点；链只处理 PCM。

## 3. 处理器与节点接口

### 3.1 纯算法包对外的形状（第二、三批照此写）

```go
// 有状态处理器。算法包不导入 dsp/chain，按方法签名匹配即可。
type Processor interface {
	// in 是 planar，所有声道等长 n（n ≥ 0，每次可以不同）。返回值也是 planar，长度可以与 n 不同
	// （变率，或内部攒块）；返回的切片归处理器所有，下一次调用前有效；同率同声道的处理器可以就地改 in 并返回它。
	Process(in [][]float64) [][]float64
	// 固有延迟：输入内容在输出里推迟的帧数，按输出采样率计（线性相位 FIR 的群延迟、卷积的分块延迟等）。构造后不变。
	Latency() int
	// 清空历史与重叠，参数不变。
	Reset()
}
```

与派活时给出的约定（“处理一块、报告固有延迟、Reset”）一致，只把“处理一块”定成“输入一块、返回一块”：变率（采样率转换、DSD 抽取）与攒块（FFT 卷积）的处理器输出长度天然不等于输入，就地接口表达不了。
同率就地的处理器（PEQ、Headroom）照样可以就地实现、直接返回 `in`。**不需要 Flush 方法**：适配器知道变率比，在流尾喂零直到输出帧数达到 `总输入 × 比率`。
构造参数（采样率、声道、系数）由各包自己的构造函数定，不进接口。

### 3.2 链节点与适配器

```go
// dsp/chain
type Node interface {
	Process(b *Buffer) *Buffer
	Reset()
	Step() SignalStep // 信号路径上的一步（结构见 dsp-pipeline design.md §9）；不显示的内部节点返回零值
}
```

`chain.Wrap(p Processor, in, out Format, step SignalStep, modifies bool)` 把算法包变成节点，统一负责：

- **延迟补偿**：流头丢掉 `p.Latency()` 帧输出；流尾（`Chain.Drain`）补零，直到输出帧数等于“总输入帧 × 输出率 / 输入率”。所以整条链的输入帧 i 与输出帧 i×比率 对齐，位置事件不需要偏移（偏离参考稿 §14 第 7 条“只申报不补偿”，理由：补偿后定位与显示都没有偏差，代价只是流头多算几十毫秒）。
- **变长输出**：处理器少吐或多吐都可以；适配器只把结果包成 `Buffer`。下游节点、量化器与编码都接受任意长度。
- **区域搬运**（§4）与 `Exact`：`modifies` 为真时输出的 `Exact` 为假。
- **格式变化不在节点里处理**：会话内格式恒定；格式不同的下一首走 zones 的接续流（新会话、新链）。

### 3.3 链本身

`chain.Build(spec) (*Chain, error)` 按 §5.2 的顺序把启用的节点排好；`Chain.Process(b)` 依次调用；`Chain.Drain()` 在流尾冲刷；`Chain.Steps()` 给出信号路径。链没有自己的线程（参考稿 §6.1 的拉取式）：由渲染循环每个量子调用一次。

## 4. 增益区域（参考稿 §6.5）

```go
type GainRegion struct {
	Offset, Frames int     // 在本缓冲里的帧范围
	Gain           float64 // 线性增益
}
```

- **不变式**：按 `Offset` 升序、互不重叠、落在 `[0, Frames)` 内；没被覆盖的帧增益为 1。
- **产生**：每曲来源段读出一块时，按这首的电平化增益生成覆盖整块的一个区域（这首的增益在一条流内不变）。参考稿的区域还带峰值；ROOMusic 的峰值钳制已经在分析阶段算进增益（`catalog` 的 `track_gain = min(−14 − I, −0.5 − TP)`，见 dsp-pipeline §7），所以不带峰值字段。
- **搬运**：施加点之前的节点（第二批的 DSD→PCM 8:1 抽取、DSD128 以上降到 352.8 kHz 的采样率转换）由适配器搬运。规则只有一条：把区域边界换成**流内绝对帧号**，按 `floor(绝对输入帧 × 输出率 / 输入率)` 换到输出侧，再切到这次输出的缓冲上。因为延迟已在链内补偿，换算没有偏移；按绝对帧号算不会累积舍入误差。改声道的节点不动区域（增益对所有声道相同）。
- **施加**：`leveling.Apply(b)` 逐样本相乘后清空 `Regions`；有任何一个区域的 `Gain != 1` 就清掉 `Exact`。增益正好 0 dB（线性 1）时样本不变，`Exact` 保留。
- **施加点**：每曲来源段的末尾、混音之前（§5.1）。交叉淡化的混音器与共享链收到的缓冲都不应再有区域；链头遇到非空区域视为装配错误（开发期 panic，测试覆盖）。
- 第一批里施加点之前没有节点（格式适配在 ffmpeg 里做，§6），区域只是“生成后立即施加”；数据结构与搬运规则先定好，第二批的 DSD 直接用。

## 5. 处理链装配与重建

### 5.1 两段式

```text
 每曲来源段（一首一个；无缝时 1 个，准备下一首或交叉淡化时 2 个）       共享（整条流一份，跨曲目连续）
┌──────────────────────────────────────────────────┐   ┌──────────────┐   ┌──────────────────────────────┐
│ 解码 ffmpeg → 10 s 预缓冲 →［DSD→PCM，第二批］→ 电平化 │──►│ 换轨与淡化     │──►│ 处理链（§5.2）→ 量化/抖动 → 编码 │──► Body
└──────────────────────────────────────────────────┘   │ （短读拼接、    │   └──────────────────────────────┘
                                         下一首 ──────►│  交叉混音）     │
                                                        └──────────────┘
```

- 参考稿 §2.4 的数据流是“解码 → 预缓冲 → 音量 → DSP 链”，电平化在链之前，这里一致。
- 交叉淡化必须在电平化之后、共享链之前：两首的增益不同，只能各自施加后再混；共享链跨曲目连续，PEQ、卷积的状态在无缝接缝处不复位（复位会有咔声）。
- 解码、预缓冲属于 dsp-pipeline；换轨与淡化属于 gapless-crossfade；量化与抖动属于 dither；编码交付属于 dsp-pipeline。

### 5.2 链的顺序（参考稿 §6.1）

十步固定顺序照搬，按批次装配：

| 位置 | 节点 | 第一批 | 以后 |
| --- | --- | --- | --- |
| ① | Headroom（PCM） | 装（P7，默认关） | — |
| ② | 兼容性采样率转换 | 由 ffmpeg 临时完成，链上只记信号路径步骤（§6） | 第二批 `dsp/src` |
| ③ | 降混 | 同上（解码 ffmpeg 的 `-ac`） | 第二批 `dsp/downmix` |
| ④ | Headroom（DSD 升频后） | 不装 | 接 DSD 输出时 |
| ⑤ | 用户处理项链（PEQ、卷积，按用户顺序） | 不装 | 第二批 |
| ⑥ | 扬声器增益 | 不做（prd“不做”） | — |
| ⑦ | PCM→DSD | 不装 | 第三批库，接 DSD 输出时 |
| ⑧ | 位深转换与抖动 | 装（无损档；有损档把浮点直接交给编码器） | — |
| ⑨ | 声道映射 | 恒等（浏览器只有立体声，FLAC/WAV 与 ffmpeg 的声道序一致） | 原生客户端多声道时 |
| ⑩ | 扬声器延时与反相 | 不做 | — |

⑧ 严格说不是 `Node`：它把 `Buffer` 变成定点字节，是链的出口（`chain.Build` 返回链与量化器两部分）。

### 5.3 重建

- 会话内链不变。开始播放、改处理设置、定位、切模式使已准备的下一首失效且已发出、暂停超过 5 秒后恢复，都是 zones 关旧会话、开新会话（zones 设计 §3.5，300 ms 去抖在 zones）。
- 参考稿“自动模式下最小间隔 1 s”针对设备能力自动变化引起的重建；第一批没有这种来源（能力只在认领时变，认领本身就开新流），不实现，第二批若出现再加在 zones 的去抖里。
- 无缝与交叉淡化发生在**同一条链**上：链跨曲目运行，不在曲目边界复位。

## 6. 第一批的格式适配（不提前做采样率转换）

第一批会遇到三种“源格式不能原样交付”的情况：

| 情况 | 第一批的做法 | 信号路径 |
| --- | --- | --- |
| 有损档要 Opus（只收 48 kHz），源是 44.1 kHz 族 | 编码 ffmpeg 在编码前 `aresample` 到 48 kHz（整条流一个进程，跨曲目连续，没有接缝） | 编码一步里写“44.1 → 48 kHz”，本来就是有损档 |
| 无损档源采样率超过输出端声明的上限（第一批只有 352.8/384 kHz 的 PCM 源；DSD 在第二批） | 解码 ffmpeg 用 `aresample`（有 soxr 用 soxr，否则用 swr 高质量参数）按“同族减半直到不超过上限”降下来；链按降后的采样率运行 | “兼容性转换”，质量评级降一档（参考稿 §6.2） |
| 多声道源超过输出端的 `max_channels`（浏览器是 2） | 解码 ffmpeg `-ac 2`（ffmpeg 默认降混矩阵） | “降混（兼容性）” |

取舍：

| 方案 | 好处 | 代价 |
| --- | --- | --- |
| **第一批用 ffmpeg 临时适配（采用）** | 不增加第一批的工作量与风险；三种情况在第一批都很少见（浏览器解 FLAC 的上限通常不低于 192 kHz，iOS 的上限由验证确定）；ffmpeg 的 soxr 质量足够 | 解码端转换是每首一个进程，两首高采样率曲目无缝相接时接缝处有极轻的滤波边缘效应；矩阵与参考稿 §6.8 不同；第二批要换掉 |
| 把采样率转换的核心（多相 L/M 引擎 + 运行时生成的线性相位 precise 核）提前到第一批 | 链内一份，跨曲目连续；第二批只补预置族与策略 | 第一批多一个算法子任务，滤波器指标验证要提前做；与正在并行起草的第二批 `dsp/src` 设计要先对齐接口，容易返工 |

第二批的 `sample-rate-conversion` 与 `downmix` 落地时：解码 ffmpeg 不再带 `aresample`/`-ac`，改由链上 ②③ 节点完成；有损档的 48 kHz 也改由 ② 完成（编码 ffmpeg 只编码）。兼容性回退顺序（参考稿 §6.2，C2）第一批只用到“同族减半”，因为输出端只声明上限、不声明离散的采样率集合。

## 7. 批次、依赖与上线顺序

```text
第一批（与播放区线一起，集成分支 playback-baseline，E 通过后一次合入 main）
  纯包：dsp/chain、dsp/leveling、dsp/fade、dsp/dither ──┐（与 zones B1 并行，不依赖端口）
  iOS 交付小验证（独立小程序，与 B1 并行，最先做）──────────┤
  zones B2 定下端口 ─► render 骨架（解码→FLAC、事件队列、协商）─► gapless-crossfade 接入 ∥ dither 接入 ∥ Headroom/信号路径
                                   └─► 联调（F2 换真实渲染器）─► 派生退役 P10 ─► 前端信号路径与 Headroom 设置（F3 之后）─► E
第二批（各自独立合入 main，不再用集成分支）
  采样率转换 ──► 卷积（IR 重采样）、DSD（降到 352.8 kHz；另需扫描 ⑩ 合入）
  降混、PEQ（与采样率转换并行）
第三批：PCM→DSD 库（依赖 DSD 的 DSD→PCM 做往返验证）
```

- 第一批上线即新的播放基线：所有播放都走渲染流（父任务 A1），没有过渡期的直出版本（zones 设计 §8）。
- 第二批每项合入时信号路径多一种节点；采样率转换建议第二批最先做，因为它同时替换第一批的临时适配（§6），并是卷积与 DSD 的前置。
- 第二批的处理设置（PEQ、卷积、降混）由各子项的迁移加在 `playback_zones` 上；第一批的 Headroom 设置由 dsp-pipeline 的迁移加（dsp-pipeline design.md §8）。

## 8. 与播放区线的对接

zones 设计 §14 的端口照用。实现中发现需要补充的点（都不改协议形状，交主会话裁决后由 zones 的 B2 采纳）：

1. **流格式在 `Open` 之后才确定**：`Negotiate(caps, quality)` 不知道源格式，只能选出编码与上限；采样率、位深、声道要等首曲确定。建议 `RenderSession` 增加 `Format() StreamFormat`（具体格式，供 `Stream.format`），`StreamFormat` 定为 `{Codec, MimeType, Quality, SampleRate, BitDepth, Channels}`，`Negotiate` 的返回值只填前三项。
2. **事件分两类**：`time`、`track_started`、`track_failed`、`stream_ended`、带分段的 `signal` 到达播放头才派发；`format_boundary`、`rebuild_needed` 与削波、处理速度的 `signal` 更新**立即**发出（前者要让 zones 提前开接续流）。zones 不应假定所有事件都按播放头到达。
3. **`format_boundary` 增加 `Reason`**（`format`：下一首格式不同；`not_ready`：下一首超时没准备好，gapless-crossfade G3），并且**可以重复**：已宣布边界后下一首又变了，会话按新的下一首再发一次，zones 以最新一条替换 `next_stream`。
4. **起始条目不发 `track_started`**（zones 开流时自己产生）；起始条目打不开时会话发 `track_failed` 并经 `Cursor` 跳到下一首。`Open` 只在渲染器不可用或忙（并发上限，dsp-pipeline §4.4）时返回错误，zones 按 `transient` 处理；曲目级问题都走事件。
5. **`Body` 只能被读一次**：同一条流的第二个连接无法从中途接上（FLAC/WAV 的头只在开头）。建议合同 §4.4 的“新连接取代旧连接”改成“第二个连接得 `410 stream_gone`”，输出端按 `stream_lost` 续流；若 iOS 验证发现 Safari 会先发探测请求再正式请求，改用 design-delivery.md §1.4 的“头部重放”，仍不改协议形状。
6. **调用约束**：`Observe`、`NextChanged`、`Close` 不阻塞，可以在 zones 的播放区锁内调用；会话在自己的协程里调用 `Cursor.Next`，所以 `Cursor.Next` 不能回调会话；`Events()` 带缓冲，zones 持续读取直到通道关闭；`Close` 可重复调用，之后 `Body` 返回 EOF、`Events` 关闭。
7. `ZoneSettings` 增加 `Headroom{Enabled bool; DB float64; ClipIndicator bool}`；合同 §2.2 的 `settings` 增加键 `headroom: {enabled, db, clip_indicator}`（dsp-pipeline P7）。

另有两处需要 zones 的测试用例知道（不是端口变化）：自动电平化的“发行上下文”三条用例归 zones（它计算 `Entry.AlbumContext`）；`gapless: false` 时会话在曲间插入 0.5 秒静音，`transition` 为 `gap`（gapless-crossfade §4）。

## 9. 对照验证与知识产权

- **仓库只放我们自己的东西**：公式与公开方法（Cookbook、TPDF、LFSR 多项式、加窗 sinc、重叠相加）照常实现；SRC 核、SDM 系数由我们的生成脚本按规格生成，生成脚本与结果可以入库。参考稿、附录 F/G、`roomusic-src-kernels.csv` 不入库、不进镜像、不进 CI。
- **文档与代码注释**只写参考稿章节号与我们的概括，不摘录原文，不写任何参考系数的数值。
- **对照测试**：`dsp/internal/refdata` 提供 `Dir(t)`：读环境变量 `ROOMUSIC_REFERENCE_DIR`，缺省 `~/.local/share/roomusic-reference`；目录或文件不存在就 `t.Skip`；读 CSV 前核对 SHA-256，不符就失败。对照测试只断言我们关心的指标（频响差、直流增益、群延迟、噪声整形频谱），失败信息只打印指标与差值，**不打印、不写出参考系数**，也不生成由参考数据派生的金样文件。
- **CI**：没有参考目录，对照测试自动跳过；CI 只跑自给的指标测试（例如 SRC 的通带、阻带由我们的核自测）。
- 第一批的算法（电平化选择、淡化曲线、TPDF）都是公式或现有规则，没有需要对照的参考数据；对照测试从第二批开始。

## 10. 偏离参考稿的记录（跨子项）

| 参考稿 | 本设计 | 理由 |
| --- | --- | --- |
| 固有延迟只申报、位置不补偿（§14 第 7 条） | 链内补偿，对外零延迟 | 定位与显示没有偏差；流头只多算几十毫秒 |
| 外部解码输入永远是管道、定位重开解码器而不用 `-ss`（§5.7） | 输入是已安全打开的文件描述符（可定位），定位用输入侧 `-ss` | 本地文件可以定位，部分容器（`moov` 在末尾的 MP4）需要定位才能解；参考稿避免 `-ss` 是为网络流；无缝拼接的曲目都从 0 开始解，不受影响 |
| 外部解码无超时、无并发上限（§14 第 4、5 条） | 起始 10 秒、停滞 10 秒超时，全局进程上限（dsp-pipeline §4） | 参考稿自己的建议 |
| 增益区域带峰值（§6.5） | 不带峰值 | 峰值钳制已在分析阶段算进增益 |
| 电平化“只在提升时按峰值钳制”、目标可配（§6.5） | 沿用现有规则：目标 −14 LUFS、真峰值留 0.5 dB，提升与衰减都不越过 | 现有规则（`audio-analysis-contracts.md`），父任务 A5 不退化；目标可配以后再做 |
| 第一批的兼容性转换与降混 | 暂由 ffmpeg 完成（§6） | 第二批换成链内节点 |
| 交叉淡化“预热段 30%”未定义 | 解释为每曲处理的滤波器预热，ROOMusic 的结构里不需要单独实现；曲线按公式作用于整个窗口（gapless-crossfade §3.3） | 参考稿只给了数字；按公式逐点计算才满足验收 |

## 11. 测试范围总则

- 每个纯包只测自己的行为，用最小的向量：电平化移植现有用例；淡化曲线在若干点的数值与截断对齐；抖动的周期、分布、±范围、无改动时逐位直通。
- 渲染会话的测试用合成素材（ffmpeg `lavfi` 生成的正弦、静音）与假 `TrackSource`，不需要数据库；端到端只在 E 里跑（zones 设计 §11）。
- 不测框架与 ffmpeg 本身的行为，不做长时间演练；iOS 只做一次真机小验证（dsp-pipeline design-delivery.md §4）与 E 里一次锁屏核对。
- 实现 agent 不写新测试；测试由测试 agent 编写与执行，只跑改动直接相关的最小集合。

## 12. 已裁决（设计审阅）

1. **CUE 整轨（虚拟分轨）在第一批顺带能播**（2026-11-08 用户采纳推荐）。原问题：渲染会话的解码器已经按“起始帧、结束帧”截取，CUE 虚拟轨只差格式表放开一项与一组测试（相邻两轨无缝拼接逐位等于整轨）。
   推荐**顺带做**：用整轨 + CUE 收藏的用户第一次能听这些专辑，而且曲间无缝；代价是第一批多半天的实现与测试，出问题时只影响 CUE 曲目。不做则保持现在的“不能直接播放”。

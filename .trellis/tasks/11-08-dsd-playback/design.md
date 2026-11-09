# 技术设计：DSD 入库与播放

需求见 [prd.md](./prd.md)；父任务 [音频算法对标](../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§3.3、§5.4、§5.5、§6.7（DSD → PCM）。
DSF、DSDIFF 是公开的容器规范；位切片查表的 DSD→PCM 抽取是公开的做法（与公开的 dsd2pcm 结构相同）。FIR 系数由我们按 §3.2 的规格自己设计。

## 0. 结论

- **入库**：在扫描对标 ⑩ 已合入 main 的 `parser/` 分派上加 `dsf.go`、`dff.go`，与 APE/WavPack/AIFF 同样的结构（`ParseAudioFile` 按扩展名分派、`fingerprintFunc` 加两种指纹、扫描扩展名表从“候选”移到“支持”）。DST 压缩的 DFF 返回“不支持的音频格式”诊断。
- **转换**：纯包 `backend/internal/dsp/dsd`：96 抽头对称 FIR、8:1 抽取、6 张 256 项表、每输出 12 次查表、每声道 16 字节环形缓冲、“年龄 6”就地位反转；输出增益默认 +6 dB；最多 32 声道。
- **FIR 自己设计**：II 型线性相位加权最小二乘，频带权重让折叠回可闻带的成分压得最深（§3.2）；离线生成 48 个唯一系数入库。与公开 dsd2pcm 的 96 抽头 FIR（参考稿 §6.7 描述的就是这个结构）逐项对照，三项规格全部更好。
- **播放**：DSD→PCM 是**每曲来源段**里的一步（父设计 §2、§5.1）：读字节 → 转换 →（后置低通）→（降到 352.8 kHz）→ 电平化。来源段的适配器补偿这几步的延迟、按流内绝对帧号搬运增益区域（父设计 §4）。现场解码，不做派生缓存。
- **分析**：后台响度分析对 DSD 用同一个解码器出 PCM，经管道交给现有的 ffmpeg 分析命令，电平与播放一致。

## 1. 解析入库（S1）

### 1.1 识别

扩展名优先：`.dsf`、`.dff`；扩展名之外的文件不会进入解析。解析时核对签名：DSF 以 `DSD ` 开头，DFF 以 `FRM8` 开头、表单类型 `DSD `。签名不符按解析失败处理（现有 `parse_failure` 诊断）。

### 1.2 DSF（小端）

| 块 | 读取 |
| --- | --- |
| `DSD `（28 字节） | 文件总长、元数据指针（0 表示没有） |
| `fmt `（52 字节） | 格式版本 1、格式 ID 0（DSD 原始）、声道类型、声道数、采样率、每样本位数（1 = 字节内 LSB 先，8 = MSB 先）、每声道样本数、每声道块大小（4096） |
| `data` | 负载：按声道交错的 4096 字节块；最后一块补零 |
| 元数据指针处 | ID3v2 标签，用现有的 `parseID3Frames`（与 MP3、AIFF 共用） |

时长 = 每声道样本数 / 采样率。声道类型 → 布局掩码（供降混）：1 单声道、2 立体声、3 3.0（FL FR C）、4 quad（FL FR BL BR）、5 3.1（FL FR C LFE）、6 5.0（FL FR C BL BR）、7 5.1（FL FR C LFE BL BR）。

**错编码补偿**（§5.5；只处理下面四种实际存在的问题）：

1. 元数据指针为 0、越界或指向的不是 `ID3`，但 `data` 块结束处紧跟着 `ID3`：用后者。
2. 每声道样本数大于 `data` 负载能容纳的数：按负载算（`负载字节 / 声道数 × 8`）。
3. `data` 块长度超出文件：截到文件末尾。
4. 每样本位数为 8：字节内按 MSB 先读，不做位反转。

### 1.3 DFF（DSDIFF，大端，块按偶数字节对齐）

| 块 | 读取 |
| --- | --- |
| `FVER` | 版本 |
| `PROP`（`SND `） | `FS  ` 采样率、`CHNL` 声道数与声道 ID（`SLFT SRGT MLFT MRGT LS   RS   C    LFE ` → 布局掩码）、`CMPR`（`DSD ` 或 `DST `） |
| `DSD ` | 负载：按声道交错的字节，字节内 MSB 先 |
| `DST ` 或 `CMPR = DST ` | 返回 `parser.ErrUnsupportedEncoding`，扫描记 `unsupported_format` 诊断（与 TAK 同一种诊断），不入库 |
| `DIIN` | `DITI` 标题、`DIAR` 艺人 |
| `ID3 `（非官方但常见） | 用 `parseID3Frames` 解析，字段优先于 `DIIN` |
| `COMT` | 不读 |

时长 = `DSD ` 负载字节 / 声道数 × 8 / 采样率。

### 1.4 观察值与指纹

- `Codec` 记 `dsf` / `dff`（与 `aiff`、`ape` 的做法一致），`SampleRate` 记 DSD 采样率（2822400 …），`BitDepth` 记 1，`Bitrate` = 采样率 × 声道数。
- 内容指纹：`dsf-data-v1` = `data` 负载（按 1.2 补偿后的范围）的 SHA-256；`dff-dsd-v1` = `DSD ` 负载的 SHA-256。改标签（ID3、DIIN）不影响，改音频会变。沿用 `hashSection` 的流式哈希。
- 扫描：`isSupportedAudioExtension` 加 `.dsf`、`.dff`，`isAudioCandidateExtension` 去掉这两项；扫描在 `ParseAudioFile` 返回 `ErrUnsupportedEncoding` 时记 `unsupported_format`，其余解析错误照旧 `parse_failure`。
- 前端：采样率按 DSD 倍率显示（2822400 → “DSD64”，5644800 → “DSD128” …；48 kHz 族 3072000 → “DSD64（48k）”），编码显示 “DSD（DSF）”/“DSD（DFF）”。

## 2. 来源段里的读取

`dsdreader`（放在 `playback/render` 内部，与解码层并列；要读文件，所以不是纯 dsp 包）：

```go
type Info struct{ Rate, Channels int; Mask uint32; Frames int64 } // Frames 按每声道字节计
func Open(f *os.File, l DSDLayout) (*Reader, Info, error) // 文件已由 TrackSource 安全打开
func (r *Reader) Read(dst [][]byte) (n int, err error)              // 每声道 n 个字节，字节内统一为 MSB 先（DSF 的 LSB 先在这里查表反转）
func (r *Reader) Seek(byteFrame int64) error                        // DSF 先定位到 4096 字节块再块内偏移
```

- `DSDLayout`（负载起止、声道、采样率、DSF 块大小与位序）由 `render` 自己定义，`TrackSource` 从解析结果填写（父设计 §1：`render` 不导入 `library`）。
- 读取单位是“每声道字节数”（§5.5），一个字节恰好对应 8:1 抽取后的一个 PCM 帧。DSF 按块交错，读取时拆回各声道；DFF 按字节交错。
- 定位后不需要预读：`dsd.Converter.Reset` 把环形缓冲填成 DSD 静音 `0x69`（全 0 字节等于 −1 的直流，会产生开头的冲击）。
- CUE 虚拟分轨不适用于 DSD（DSF/DFF 没有 CUE 整轨的用法），起止按整个文件。

## 3. DSD → PCM（S2）

### 3.1 包接口

```go
package dsd // backend/internal/dsp/dsd

func New(channels int, gainDB float64) (*Converter, error) // channels ≤ 32；gainDB 默认由调用方传 6
func (c *Converter) Process(in [][]byte) [][]float64        // 每输入 1 字节输出 1 个样本（8:1）；返回的切片归 Converter 所有
func (c *Converter) Latency() int                           // 6：群延迟 47.5 个 DSD 样本 = 5.94 个输出样本，四舍五入
func (c *Converter) Reset()                                 // 缓冲填 0x69
```

输入是字节而不是 `[][]float64`，所以它不是父设计 §3.1 的 `Processor`，也不经 `chain.Wrap`；延迟与冲刷由来源段适配器处理（§4.1）。其余约定（返回切片归处理器、`Latency` 构造后不变、`Reset` 清历史）与 §3.1 相同。

### 3.2 FIR 设计

- 96 抽头、对称（48 个唯一系数）、直流增益 1.0（Σh = 1）。
- 方法：II 型线性相位**加权最小二乘**。基函数 `cos(ω(k + ½))`，k = 0…47，在 16384 点的频率网格（0 到 DSD64 奈奎斯特）上对下表的目标与权重做最小二乘，用 QR 分解求解（权重相差 10⁹，不能用正规方程），再按 Σh = 1 归一。

| 频带（以 DSD64 的 2.8224 MHz 计） | 目标 | 权重 | 作用 |
| --- | --- | --- | --- |
| 0–20 kHz | 1 | 1 | 通带 |
| 20–176.4 kHz | — | 0 | 过渡带，不约束 |
| 176.4–328.8 kHz | 0 | 1e−4 | 折叠到 24–176 kHz 的超声成分，只轻微压 |
| 328.8 kHz–奈奎斯特 | 0 | 1e5 | 折叠回 0–24 kHz 可闻带的成分（352.8 kHz 的整数倍 ± 24 kHz） |

- 权重的取法：可闻带混叠最要紧，给最大权重；折叠到超声的成分之后还会被后置低通与后续处理压掉，权重最小。
- **离线生成后入库**：生成器 `backend/internal/dsp/dsd/internal/gen`（main 包，只用标准库：Householder QR），`dsd` 包里 `//go:generate go run ./internal/gen -out fir_gen.go`，输出 48 个系数，注释写三项实测规格。CI 不重新生成，测试检查入库系数的规格。包初始化时用这 48 个数建 6 张查表（微秒级）。
- 同一 FIR 用于 DSD128 及以上：频带按倍率等比放大（例如 DSD128 通带 0–40 kHz），之后的 2 的幂降采样由 SRC 负责可闻带以上的抗混叠（§4）。

规格与对照（2026-11-08 本机 Python 原型；对照对象是公开 dsd2pcm 的 96 抽头 FIR，BSD-2 许可，参考稿 §6.7 描述的结构与它一致而参考资料本身没有给系数）：

| 项 | 我们 | dsd2pcm | 余量 | CI 判定 |
| --- | --- | --- | --- | --- |
| 0–20 kHz 波动（峰峰值） | 0.00011 dB | 0.00112 dB | 小 0.0010 dB | ≤ 0.0002 dB |
| 0–24 kHz 波动 | 0.00083 dB | 0.00208 dB | 小 0.0012 dB | 只打印 |
| ≥ 176.4 kHz 最差衰减 | 56.3 dB | 15.4 dB | 深 40.9 dB | ≥ 56 dB |
| ≥ 328.8 kHz 最差衰减 | 168.0 dB | 151.2 dB | 深 16.8 dB | ≥ 167 dB |
| −3 dB 点 | 90.3 kHz | 120.7 kHz | — | 只打印 |

- 对照方法：两组系数都按 Σh = 1 归一，用 2¹⁸ 点频响（采样率 2.8224 MHz）测上表各项，“不劣”指每一项都不比对照差。这是一次性的设计核对，结果记在这里；dsd2pcm 的系数不进仓库、不进测试。
- 代价：−3 dB 点从 120.7 kHz 降到 90.3 kHz，对 352.8 kHz 输出来说 90 kHz 以上本来就是 DSD 的整形噪声，不损失有用内容。

### 3.3 位切片查表

- 每个比特映射为 ±1（1 → +1，0 → −1）。一字节 8 个 DSD 样本，MSB 是这 8 个里最早的。
- 记最近 12 个字节为 B₀（最新）… B₁₁（最旧），FIR 输出 `y = Σ_{j=0}^{11} T_j[B_j]`，其中 `T_j[b] = Σ_{i=0}^{7} s_i(b) · h[8j + i]`，`s_i(b)` 是字节 b 里按时间从新到旧第 i 个比特对应的 ±1。
- 对称性 `h[k] = h[95−k]` 使 `T_{11−j}[b] = T_j[reverse(b)]`，所以只存 j = 0…5 的 **6 张 256 项表**；旧的 6 个字节在缓冲里**到“年龄 6”时就地位反转**，查表时直接用对应的那张表。每输出一个样本 **12 次查表**、11 次加法。
- 每声道一个 **16 字节**环形缓冲（2 的幂，取模用掩码）。
- 输出乘增益 `10^(gainDB/20)`，默认 +6 dB（“DSD 满刻度约等于 50% 调制”的电平约定）。DSD 内容超过 50% 调制时 PCM 会超过 1.0：链是浮点，越界在定点编码时按削波统计，要留余量用 Headroom。
- FIR 没有反馈，不会产生非规格化数。

### 3.4 后置低通

参考稿的 `filter=1/2/3`（2 或 4 段二阶低通）在来源段里接在转换之后，用 PEQ 包的 `ButterworthLowPass` 构造 `peq.IIR`，不放进本包（保持本包不依赖其他包）：

| filter | 段数 | 截止 |
| --- | --- | --- |
| 0 | 无 | — |
| 1（默认） | 2 段（4 阶，Q 0.54 / 1.31） | 70 kHz |
| 2 | 2 段 | 50 kHz |
| 3 | 4 段（8 阶，Q 0.51 / 0.60 / 0.90 / 2.56） | 30 kHz |

默认 1：20 kHz 处衰减 < 0.001 dB，把 DXD 输出里 DSD 噪声整形抬起来的超声噪声压下去。参考稿没写默认档与截止频率，这里是我们定的；不做成用户设置。

## 4. 来源段与播放（S3、S4）

### 4.1 来源段的组成与适配器

```text
dsdreader → dsd.Converter → peq.IIR（后置低通）→ src.Resampler（DSD128 起）→ leveling.Apply
```

`playback/render` 里的 DSD 来源段适配器负责（父设计 §3.2、§4 的规则落在 DSD 上）：

- **增益区域**：每读一块字节，按这首的电平化增益生成覆盖整块的一个区域，边界用流内**绝对字节帧号**（一个字节帧 = 8:1 后的一个 PCM 帧）。经过 SRC 时按 `floor(绝对输入帧 × 输出率 / 输入率)` 换到输出侧再切到这次输出的缓冲上；按绝对帧号算不会累积舍入误差。
- **延迟补偿**：三步的延迟换算到最终 PCM 率后相加（转换器 6 帧 × 降采样比例 + 低通 0 + SRC 的 `Latency()`），流头丢掉这么多帧。
- **流尾冲刷**：文件读完后喂 `0x69` 字节，直到输出帧数等于“总字节帧 × 输出率 / 输入率”。
- **`Exact`** 恒为假，`SourceBits` 为 0（DSD 转出来就是浮点）。

### 4.2 采样率

| 源 | 8:1 抽取后 | 降采样（SRC 设计 §4.4，固定 linear precise） |
| --- | --- | --- |
| DSD64（2.8224 MHz） | 352.8 kHz | 无 |
| DSD128 | 705.6 kHz | 单级 precise 降 2 |
| DSD256 | 1.4112 MHz | 两级：2s 降 2、precise 降 2 |
| DSD512 | 2.8224 MHz | 两级：2s 降 4、precise 降 2 |
| 48 kHz 族 DSD（3.072 MHz 起） | 384 kHz 及其 2 的幂倍 | 同上降到 384 kHz |

- 来源段输出 352.8（或 384）kHz 的 PCM；之后如果输出端达不到，由链上 ② 做兼容性转换（SRC 设计 §4.3），例如浏览器上限 192 kHz 时 352.8 → 176.4 kHz。
- 处理量子：DSD 侧按“DSD 采样率 / 5 / 8”字节读（200 ms），产生 PCM 侧 `采样率/5` 个样本，与父设计的量子一致。
- 无缝：曲目之间是否无缝按来源段输出的 PCM 格式判断（gapless-crossfade 的格式条件），DSD64 与 DSD64 之间可以无缝；DSD 曲目与同为 352.8 kHz 的 PCM 曲目之间也可以。

### 4.3 信号路径步骤

按 dsp-pipeline §9.1：`source` 步骤 `{codec: "dsf"|"dff", sample_rate: 2822400…, channels}`，`quality: "lossless"`；紧跟一个 `dsd_to_pcm` 步骤 `{from: "DSD64", dsd_rate, to_rate, filter}`，`to_rate` 是来源段的输出率（DSD128 起的降采样并在这一步里，不单列 `sample_rate`），`quality: "high"`，`compat: true`（为了输出端只收 PCM 做的转换）。显示为“DSD64 → PCM 352.8 kHz”。

## 5. 后台响度分析（S5）

现有分析命令用 ffmpeg 读文件跑 `ebur128`。对 DSD 曲目改为：本进程用 `dsdreader` + `dsd.Converter` + 后置低通（默认 +6 dB、filter 1）解出 PCM，以 `f32le` 写进 ffmpeg 的标准输入（`-f f32le -ar <352800|…> -ac <n> -i pipe:0`），其余滤镜图与输出解析不变。分析得到的响度与峰值和来源段的电平一致；用 ffmpeg 自带的 DSD 解码会差一个固定增益。分析不降采样（`ebur128` 接受 352.8 kHz）。

## 6. 测试（最小集）

后端（测试 GPT）：

1. 解析：测试里生成的四个小样本（各几 KB，不提交大文件）——正常 DSF、正常 DFF（含 `DIIN` 与 `ID3 `）、DST 压缩的 DFF、元数据指针错位的 DSF。检查采样率、声道、时长、标签；DST 返回 `ErrUnsupportedEncoding`；错位指针的标签被找回。
2. 指纹：同一样本改标签指纹不变、改一个音频字节指纹变化（DSF、DFF 各一次，表驱动）。
3. FIR：Σh = 1（误差 ≤ 1e−12）、对称、§3.2 表里三项 CI 判定。
4. 查表实现与直接卷积一致：随机比特流，按 ±1 直接做 96 抽头卷积再 8:1 抽取，与查表结果误差 ≤ 1e−12。
5. 已知信号：一阶 ΔΣ（测试里十几行实现）把 1 kHz、−6 dBFS 正弦调制成 DSD64，解码后频率正确、电平误差 ≤ 0.1 dB。PCM→DSD 子任务完成后由它的往返测试覆盖更高阶的情况。
6. 来源段适配器：DSD128 的一段已知信号经来源段后，输出帧数等于“字节帧 / 2”、峰值位置与输入对齐（延迟已补偿）、一个跨块边界的增益区域落在正确的输出帧上。
7. 扫描：`.dsf`/`.dff` 进入支持表、DST 记 `unsupported_format`（扩展现有扫描规则测试的表，一个用例）。

前端（测试 GPT）：DSD 采样率与编码的显示（一个用例）。

## 7. 分工与依赖

- 后端（GPT）：`parser/dsf.go`、`parser/dff.go`、指纹、扫描表；FIR 生成器与 `dsp/dsd`；`dsdreader` 与 DSD 来源段适配器；`dsd_to_pcm` 信号路径步骤；分析的管道输入。
- 前端（Opus）：DSD 采样率与编码的显示、`dsd_to_pcm` 步骤；量小，可并入同期其他前端 agent。
- 测试（GPT）：§6。
- 依赖：⑩（已合入 main）；第一批的渲染会话与来源段；SRC 子任务的纯包（DSD128 起的降采样）；PEQ 子任务的 `ButterworthLowPass`（后置低通）。解析入库（§1）与 `dsp/dsd` 不依赖第一批，可以先做；但入库后的曲目要等来源段上线才能播放，所以入库部分与播放一起上线，避免出现能看到却放不了的曲目。

## 8. 待裁决

无。后置低通默认档（filter 1、70 kHz）是技术选择，见 §3.4。

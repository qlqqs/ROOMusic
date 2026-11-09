与第一批代码对齐：2026-11-09

# 技术设计：DSD 入库与播放

需求见 [prd.md](./prd.md)；父任务 [音频算法对标](../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§3.3、§5.4、§5.5、§6.7（DSD → PCM）。
DSF、DSDIFF 是公开的容器规范；位切片查表的 DSD→PCM 抽取是公开的做法（与公开的 dsd2pcm 结构相同）。FIR 系数由我们按 §3.2 的规格自己设计。

接入以[公共骨架合同](../11-09-dsp-batch2-skeleton/design.md)为准；基线为 `main e29b65d`，新接口均为拟定。纯包与接入的启动条件见本任务 implement.md。前端由主会话写，不另派前端 agent。

## 0. 结论

- **入库**：在扫描对标 ⑩ 已合入 main 的 `parser/` 分派上加 `dsf.go`、`dff.go`，与 APE/WavPack/AIFF 同样的结构（`ParseAudioFile` 按扩展名分派、`fingerprintFunc` 加两种指纹、扫描扩展名表从“候选”移到“支持”）。DST 压缩的 DFF 返回“不支持的音频格式”诊断。
- **转换**：纯包 `backend/internal/dsp/dsd`：96 抽头对称 FIR、8:1 抽取、6 张 256 项表、每输出 12 次查表、每声道 16 字节环形缓冲、“年龄 6”就地位反转；输出增益默认 +6 dB；最多 32 声道。
- **FIR 自己设计**：II 型线性相位加权最小二乘，频带权重让折叠回可闻带的成分压得最深（§3.2）；离线生成 48 个唯一系数入库。与公开 dsd2pcm 的 96 抽头 FIR（参考稿 §6.7 描述的就是这个结构）逐项对照，三项规格全部更好。
- **播放**：DSD→PCM 是**每曲来源段**里的一步（父设计 §2、§5.1）：读字节 → 转换 →（后置低通）→（降到 352.8 kHz）→ 电平化。来源 PCM 流先补偿转换延迟，`decoder.read` 再生成并立即施加电平化；不存在穿过 SRC 的增益区域。现场解码，不做派生缓存。
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
| `DST ` 或 `CMPR = DST ` | 本项新增 `parser.ErrUnsupportedEncoding` 哨兵，扫描在 `errors.Is` 命中时记 `unsupported_format`，不入库；该哨兵当前不存在，不能只写调用端 |
| `DIIN` | `DITI` 标题、`DIAR` 艺人 |
| `ID3 `（非官方但常见） | 用 `parseID3Frames` 解析，字段优先于 `DIIN` |
| `COMT` | 不读 |

时长 = `DSD ` 负载字节 / 声道数 × 8 / 采样率。

### 1.4 观察值与指纹

- SourceKind 明确定为 `dsf_id3` / `dff_diin`（DFF 带 ID3 时仍用容器级 dff_diin，不随有无标签改身份）；CUE 仍为 cue_virtual。
- `Codec` 记 `dsf` / `dff`（与 `aiff`、`ape` 的做法一致），`SampleRate` 记 DSD 采样率（2822400 …），`BitDepth` 记 1，`Bitrate` = 采样率 × 声道数。
- 内容指纹：`dsf-data-v1` = `data` 负载（按 1.2 补偿后的范围）的 SHA-256；`dff-dsd-v1` = `DSD ` 负载的 SHA-256。改标签（ID3、DIIN）不影响，改音频会变。沿用 `hashSection` 的流式哈希。
- 扫描：`isSupportedAudioExtension` 加 `.dsf`、`.dff`，候选表继续覆盖它们（不移除候选准入，只不再作为“不支持格式”专门诊断）；扫描在 `ParseAudioFile` 返回 `ErrUnsupportedEncoding` 时记 `unsupported_format`，其余解析错误照旧 `parse_failure`。
- 前端：采样率按 DSD 倍率显示（2822400 → “DSD64”，5644800 → “DSD128” …；48 kHz 族 3072000 → “DSD64（48k）”），编码显示 “DSD（DSF）”/“DSD（DFF）”。

## 2. 中性来源读取与准入

### 2.1 中性包

容器结构与安全句柄读取放 `backend/internal/playback/dsdio`，不放 render 内部。它只用标准库，提供 `Probe(io.ReaderAt,size)` 得到 `Layout{容器,负载起止,Rate,Channels,Mask,DSFBlockSize,BitOrder,有效DSD样本数}`，以及 `Reader.Read(dst [][]byte)`、`Seek(byteFrame)`；每声道每字节 8 个比特，统一输出 MSB 先。DSF 的块交错/位反转与 DFF 的字节交错在此处理；扫描 parser、播放安全打开、后台分析共用这份容器事实，标签仍由 parser 拥有。

`playback/dsdpcm` 在此之上组合 Converter、后置低通、SRC 与延迟补偿，提供有界流式 PCM 读取；既不导入 render，也不导入 analysis/chain。读取器返回固定 352.8/384 kHz 的 f32le（与播放环相同），可复用到分析，生产文件由调用方安全打开，包内不自行按路径重开。

### 2.2 播放门控不能漏接

| 当前文件（相对 backend/internal/） | 本项改变 |
| --- | --- |
| `playback/formats.go` | 新增 containerDSF/containerDFF，不增加旧直出格式 |
| `playback/sniff.go` | matchesContainer 增加两种签名与必要头部核验 |
| `playback/render_formats.go` | 普通 SourceKind/Codec 与 cue_virtual/dsf、cue_virtual/dff 四行；RenderFormats 仍要求整体渲染可用，但原始 DSD 改用内置能力判断，不依赖 renderDecoderNames 中 ffmpeg 的 DSD decoder；DST 不放行 |
| `playback/render_source.go` | OpenForRender 在安全打开和版本核对后重新 Probe 负载布局（偏移未入库），仿照 verifyRenderWAV；带出 DSDLayout 与 Layout |
| `app/renderer.go`、`playback/render/source.go` | 传递 DSD 描述与有效来源格式，不让 render 导入 parser/catalog |
| `playback/render/renderer.go` | validateTrack 将原始 DSD 比特率与 PCM 范围分支，避免 >768k 一律拒绝；预留数按实际来源/编码进程：PCM 普通2、PCM WAV1，DSD 普通1、DSD WAV0；0 个不调用要求正数的 Reserve |

SourceKind、Codec、parser 缓存版本、指纹函数与扫描诊断在同一上线提交接齐。现有 `scan_pipeline.go` 对解析错误统一 parse_failure，需在普通及 CUE 父音频解析分支识别新增哨兵；TAK 的按扩展名诊断保持，不借改 DSD 重写其他错误。

### 2.3 CUE 与定位（已裁决必须支持）

扫描沿用 75 Hz 绝对索引与父文件安全路径；DSD 的 CUE 边界在 OpenForRender **直接**换到来源段输出 PCM 率：`floor(index75 * pcmRate / 75)`，传入 render 的 CueStartFrame/CueEndFrame 因而明确是有效来源 PCM 帧，不是 DSD 比特帧。DSDLayout 自己保留 bitRate；普通 PCM 合同不变。

- 单轨定位：`CueStartFrame + round(曲内秒数 * 来源PCM率)`；Reader 换算到父 DSD 位置，按抽取相位对齐，预读所需滤波历史再丢弃起点前样本。不能像旧设计那样 seek 后只 Reset(0x69) 就开始输出，否则分轨边界会有重复起始瞬态。文件起点才用静音历史。
- 相邻同父文件、同版本、前一 end 等于后一 start 的 CUE：共享连续 `dsdpcm` 生产者、滤波器与 byteRing，虚拟轨 end 只结束该段，不结束父生产者、不冲刷、不重置 IIR/SRC。next decoder 持有接续引用，current close 只释放段引用；最后一个引用/会话取消才停生产者。`player.prepareNext` 检测这一条件，next 不另解同一段；`decoder.read` 按本轨剩余帧限读，不能越界消费下一轨。改队列或跳非相邻轨解除共享、重开来源，仍由骨架的提交污染判断决定是否重建。
- 单独打开非零起点用有界前滚：覆盖 DSD FIR、SRC 有限历史并给 IIR 足够稳定时间，起点至少前移 1 秒，按转换整周期对齐；输出与整轨对应区间按浮点容差检查。相邻 CUE 连续读取的拼接则与同一转换器整轨输出逐样本一致，不把独立重置的两段称为无缝。
- 结束索引缺省读父 EOF；只在真实父文件 EOF 冲刷 `0x69`。用户看到的曲内位置从分轨起点计，父文件绝对帧只作读取坐标。

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

默认 1：20 kHz 处衰减 < 0.004 dB（实测 352.8 kHz 输入 0.0029 dB，2.8224 MHz 输入 0.0037 dB），把 DXD 输出里 DSD 噪声整形抬起来的超声噪声压下去。主会话 2026-11-09 裁定：保持共享的 `peq.ButterworthLowPass`、固定 Q 与 70 kHz 截止，不另造低通、不加增益补偿。参考稿没写默认档与截止频率，这里是我们定的；不做成用户设置。

## 4. 来源段与播放（S3、S4）

### 4.1 来源段组成与现有 decoder 复用

```text
dsdio → dsd.Converter → peq.IIR → src.Resampler → 来源延迟/长度补偿
      → f32le → 同一个 byteRing → decoder.read（电平化）→ player/crossfade → 共享链
```

`render/decoder.go` 新增 DSD 填充协程而不重写 player；d.integer=false、d.exact=false、sourceBits=0，format 是骨架 sourcePCM。`transition.go/waitNext/rejectEmptyNext`、`crossfade.go/beginCrossfade` 都直接读 ring，必须保留环的就绪、33% 预缓冲、终态、取消与帧对齐语义。`fill` 的 process.Close/Wait 改为生产者各自负责，DSD 没有 ffmpeg 进程，不得对 nil process 调用。下一首 DSD 不 Reserve 解码名额。

转换器并非 chain.Processor（输入是 [][]byte），来源适配器一次性补偿 Converter 的 6 帧及 SRC 的固有延迟，换到最终率求和后取整；SRC 在这里使用纯处理器，**不要再 Wrap 一次重复丢头**。IIR 延迟申报 0。EOF 喂静音字节到 `floor(有效DSD比特帧数 * 来源PCM率 / DSD比特率)` 个输出帧，排除 DSF 尾部块填充；来源转换只负责音频，不生成 GainRegion，交给现有 decoder.read 在 PCM 上生成并立即 Apply。

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
- 无缝：改造 `negotiate.go/sameSourceFormat` 为比较有效来源描述（率、声道、布局、精确整数类别/有效位深）并校验候选后继需要的输出计划。DSD64/128 同声道同布局归一后可无缝；DSD 与 352.8k **浮点** PCM 在相同有效格式/输出计划下可无缝；DSD 与整数 DXD 不自动承诺无缝，沿用类别边界。不得仅改 sample_rate 比较就放开所有情况。

### 4.3 信号路径步骤

按 dsp-pipeline §9.1：`source` 步骤 `{codec: "dsf"|"dff", sample_rate: 2822400…, channels}`，`quality: "lossless"`；紧跟一个 `dsd_to_pcm` 步骤 `{from: "DSD64", dsd_rate, to_rate, filter}`，`to_rate` 是来源段的输出率（DSD128 起的降采样并在这一步里，不单列 `sample_rate`），`quality: "high"`，`compat: true`（为了输出端只收 PCM 做的转换）。显示为“DSD64 → PCM 352.8 kHz”。

## 5. 后台响度分析（S5）

`playback/analysis_worker.go/analyze` 在安全 openSource 后按 dsf_id3/dff_diin 分支；cue_virtual 的 SourceKind 无法说明父编码，且 `catalog.TrackAnalysisCandidate` 当前无 Codec，故对 CUE 父安全句柄用 dsdio.Probe 判断，不能只比较候选 SourceKind。不新增数据库 Codec 列解决本来可嗅探的问题。

`playback/analysis/analysis.go/analyzeInput` 把 stdin 从 `[]byte` 改为 `io.Reader`，附可选输入描述 `{Format:"f32le",SampleRate,Channels}`；自检 WAV 用 bytes.NewReader 包装，原文件 Analyze 路径不变。新增 AnalyzePCM 入口，参数 `-f f32le -ar <来源PCM率> -ac <声道>` 必须在 `-i pipe:0` **之前**。dsdpcm 有界块读取与 cmd.Stdin 流式连接，不整轨入内存；取消/进程提前退出时关闭 pipe 两端、结束生产者，保留原低优先级/超时/错误分类。

DSD 分轨先在来源 PCM 读取器按 §2.3 截取，再送分析；不要给已截取 stdin 再加 ffmpeg 的 -ss/-t。播放与分析使用同一 +6 dB/filter1/DXD 输出，不加播放区 PEQ、卷积或电平化；现有 ebur128、8k 波形支路及结果测量保持。

## 6. 测试（最小集）

后端（测试 GPT）：

1. 解析：测试里生成的四个小样本（各几 KB，不提交大文件）——正常 DSF、正常 DFF（含 `DIIN` 与 `ID3 `）、DST 压缩的 DFF、元数据指针错位的 DSF。检查采样率、声道、时长、标签；DST 返回 `ErrUnsupportedEncoding`；错位指针的标签被找回。
2. 指纹：同一样本改标签指纹不变、改一个音频字节指纹变化（DSF、DFF 各一次，表驱动）。
3. FIR：Σh = 1（误差 ≤ 1e−12）、对称、§3.2 表里三项 CI 判定。
4. 查表实现与直接卷积一致：随机比特流，按 ±1 直接做 96 抽头卷积再 8:1 抽取，与查表结果误差 ≤ 1e−12。
5. 已知信号：一阶 ΔΣ（测试里十几行实现）把 1 kHz、−6 dBFS 正弦调制成 DSD64，解码后频率正确、电平误差 ≤ 0.1 dB。PCM→DSD 子任务完成后由它的往返测试覆盖更高阶的情况。
6. 来源段：DSD128 输出长度/峰位置、DXD 源时钟和链输出时钟正确，电平化在转换后只施加一次；用流式 reader 比较分析输入与播放的 pre-leveling PCM，取消不会遗留生产者。
7. CUE：同父相邻两轨共享转换状态，拼接等于整轨对应 PCM；非零起点/seek 与 nil 结束边界时间正确；不把 DSD bitRate 当 PCM rate。
8. 准入：DSF/DFF 与 cue_virtual 的格式表/内置 decoder 门控，DSD→WAV 零解码/编码进程及 DSD→FLAC 仅编码名额，各复用现有格式/会话表测。
9. 扫描：`.dsf`/`.dff` 进入支持表、DST 记 `unsupported_format`（扩展现有扫描规则测试的表，一个用例）。

前端（测试 GPT）：DSD 采样率与编码的显示（一个用例）。

## 7. 分工与依赖

后端实现、独立测试均 `codex/gpt-6-astra`（high），前端主会话写。FIR 生成器/dsp/dsd 现在可开工；中性容器解析可先准备，但扫描支持表、入库、播放和分析必须一起上线。依赖骨架 → SRC → 降混 → PEQ → 卷积按顺序接入，纯转换所需的 SRC/PEQ 可先取纯包提交；扫描 ⑩ 已在 main，不再等待它。

新增 `dsp/dsd/internal/gen`、`playback/dsdio`、`playback/dsdpcm` 的规则逐目录登记；parser/playback/render 的具体导入按骨架 §8。不新增 DSD 用户设置，不占迁移号。

## 8. 待裁决

无。后置低通默认档（filter 1、70 kHz）是技术选择，见 §3.4。

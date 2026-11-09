# DSD 入库、来源转换与分析合同

## 1. 范围与触发

2026-11-09 接入 DSF、原始 DFF 的普通曲目与 CUE 虚拟分轨。DST 不支持；不增加迁移、用户设置、直出格式或派生缓存。转换算法与容器事实来自已有 `dsp/dsd`、`playback/dsdio`、`playback/dsdpcm`，本合同规定其生产接线。参考稿只引用 §3.3、§5.5、§6.7，不复制原文或参考系数。

## 2. 签名与所有权

- `parser.ParseAudioFile(path)` 分派 `.dsf/.dff`；已存在的 `parser.ErrUnsupportedEncoding` 用 `errors.Is` 跨层识别。
- `dsdio.Probe(io.ReaderAt, size) (Layout, error)` 保存安全句柄上的负载范围、位序、比特率、有效样本数与声道布局。
- `dsdpcm.OutputRate(dsdRate int) (int, error)` 只做轻量倍率判定；44.1 kHz 族输出 352800，48 kHz 族输出 384000，不生成滤波核。
- `dsdpcm.New(ctx, source, layout)` 默认 +6 dB、filter=1；`SeekFrame(frame)` 按父来源绝对输出 PCM 帧定位，有界前滚与延迟补偿由纯包拥有。
- `playback.RenderSource.DSDLayout` 与 `render.Track.DSDLayout` 均为 `*dsdio.Layout`，由 app 原样传递，调用方不得修改。`SampleRate` 仍是文件 DSD 比特率，`BitDepth=1`，`IsFloat=false`，`Layout=Mask`。
- `analysis.Analyzer.AnalyzePCM(ctx context.Context, stdin io.Reader, input analysis.PCMInput) (analysis.Result, error)`；`PCMInput{Format:"f32le", SampleRate, Channels}`。可关闭的输入在取消、启动失败及 stdout 结束时关闭，先停止来源再 `cmd.Wait`；生产输入必须提供并发安全的 `Close`。
- `analysis` 不依赖 DSD 包；`playback` 组合安全打开、`dsdio/dsdpcm` 与分析器。`render` 只消费 Track，不反向导入 parser/catalog/playback 主包。

## 3. 数据与流程合同

### 扫描与准入

| 容器 | SourceKind | Codec | 指纹 |
| --- | --- | --- | --- |
| DSF | `dsf_id3` | `dsf` | `dsf-data-v1` |
| DFF | `dff_diin` | `dff` | `dff-dsd-v1` |

DSD 的 `sample_rate` 为 2822400 等文件采样率，`bit_depth=1`，`bitrate` 沿用既有 kbps 单位，为 `floor(sample_rate×channels/1000)`。指纹复用纯包的负载流式哈希，不包含标签。新增缓存版本为 `c2.f1`，不使其他格式缓存失效。普通扫描及 CUE 父解析均将不支持编码哨兵转成 `unsupported_format`；其他错误保留 `parse_failure` 与既有保住来源规则。CUE 扩展名回退表包含 `.dsf/.dff`。

渲染表新增 `dsf_id3/dsf`、`dff_diin/dff`、`cue_virtual/dsf`、`cue_virtual/dff` 四行，均为 `derived/audio/flac`。整体渲染未装配时仍返回空表；原始 DSD 不依赖 ffmpeg 的 DSD decoder 清单。安全打开之后重新 Probe 并校验容器、采样率、声道、位深及支持倍率，DST 不会转交 ffmpeg。

### CUE、来源时钟与生命周期

CUE 的起止索引分别换算 `floor(index75 * OutputRate / 75)`，不是先换成 DSD 比特帧，也不是对时长取整后加到起点。nil 终点保持到父 EOF。曲内 seek 为 `CueStartFrame + round(seconds * sourcePCM.SampleRate)`。

DSD 来源生产者输出 f32le 到原 `byteRing`，复用十秒容量、33% 预缓冲、终态和取消广播；`decoder.read` 完成 planar float64 换算与电平化，DSD 的 `integer=false`、`exact=false`、`sourceBits=0`。分轨 read 按剩余帧限读，段末返回 EOF，但不对父生产者冲刷或重置。

非交叉淡化的相邻 CUE 仅在以下条件全部满足时共享生产者、环与滤波历史：两个段都有 CUE 起点，前 end 等于后 start，DSD Layout 相同，安全句柄 `os.SameFile`、大小及修改时间相同。生产者保存打开时的 Stat 快照，因此父 EOF 关闭句柄后仍可核对同版本。引用只由 player 协程维护；关闭预取只释放段引用，最后引用释放或会话取消才停止生产者。非连续跳转重新打开并调用 `SeekFrame` 前滚；队列污染判断仍由共享链骨架负责。

有效来源格式比较输出 PCM 率、声道、布局、精确整数类别；整数额外比较有效位深。DSD64/128 可同格式接续，DSD 与同率浮点 PCM 可接续，整数 DXD 不因此自动放开。后继仍需通过现有出口精度检查。DSD 内部降采样不改变共享链事件/输出帧域合同。

| 来源与交付 | 预留进程数 |
| --- | --- |
| PCM → 非 WAV | 2 |
| PCM → WAV | 1 |
| DSD → 非 WAV | 1 |
| DSD → WAV | 0 |

零个不调用 `Reserve`；下一首 DSD 不预留解码进程。DSD 生产者没有 `ffmpeg.Process`，不能对 nil process 调用 Close/Wait。

### 信号合同

顺序为 source → dsd_to_pcm → 电平化 → 共享链 → 抖动 → 编码。DSD source 的 `quality="lossless"`，params 只含 `codec:"dsf"|"dff"`、DSD `sample_rate`、`channels`；曲目格式仍含 `bit_depth:1`。

```json
{
  "kind": "dsd_to_pcm",
  "quality": "high",
  "lossy": false,
  "compat": true,
  "params": {"from":"DSD64","dsd_rate":2822400,"to_rate":352800,"filter":1}
}
```

`filter` 是整数 0–3 的后置低通档位，当前生产默认 1，不是 SRC 滤波器字符串，也不是用户设置。`from` 按所属 44.1/48 kHz 族计算 DSD 倍率。DSD128 及以上到 DXD 的降采样合并在此步骤；设备能力限制导致的后续 DXD→PCM 降采样，才另列 `sample_rate`。

### 后台分析

普通 DSF/DFF 与 CUE 父音频都从安全句柄识别；CUE 候选无 Codec，不可只按 SourceKind 判断。非 DSD 保留原 Analyze 路径。DSD 复用 +6 dB/filter1/DXD 转换，不加播放区处理；CUE 先按来源 PCM 帧截取，再把流交给 AnalyzePCM，不重复使用 `-ss/-t`。

原始 PCM 的 `-f f32le -ar <rate> -ac <channels>` 必须放在 `-i pipe:0` 前。使用同步、有界读取器和 exec 的 stdin 复制，无额外整轨缓冲或生产 pipe 协程。关闭输入取消转换并关闭文件，解除底层 Read 阻塞；原 ebur128、8 kHz 波形、低优先级、超时与结果分类不变。

## 4. 校验与错误矩阵

| 条件 | 结果 |
| --- | --- |
| 扫描遇到 DST | `ErrUnsupportedEncoding` → `unsupported_format`，不入库 |
| 扫描签名不符、容器损坏 | `parse_failure` |
| 渲染器未装配、未知组合、不支持 DSD 倍率或 DST | `unsupported_format` |
| 安全打开/版本/容器与扫描不符 | 沿用 `source_unavailable` / `source_changed` |
| DSD 负载 Probe 失败 | 渲染 `source_unavailable`；分析 `parse` |
| 无效 CUE 记录 | 安全来源 `source_changed` |
| 无效解码定位、空输出、非完整帧、读取失败 | `decode_failed` |
| 分析 PCM 描述不是 f32le、率非正、声道不在 1–32 | `analysis.Failure{Category:"parse"}` |
| 分析取消/超时 | 取消传播；超时保留 `timeout`，关闭输入并回收进程 |

## 5. 正常、基准与错误案例

- 正常：DSF64 普通曲目转换到 352800 Hz，WAV 出口无 ffmpeg 进程；DSD128 的两段 CUE 共用父转换状态。
- 基准：普通 PCM 继续走原 ffmpeg 解码、量化与 CUE 绝对 PTS 截取，已有逐位一致测试保持通过。
- 错误：DST 虽然有 DFF 容器签名也不能发布为可解码来源；分析同样不回退到 ffmpeg 的另一套 DSD 电平。

## 6. 验证与独立测试边界

本次接入只运行用户规定的受影响包现有测试，并更新扫描表/分析桩等失配 fixture。设计 §6 的新增独立测试由另一个测试 agent 提供，不能把本次本机短样本运行当成完整独立验收。

必须覆盖：DSF/DFF 标签和指纹、DST 普通及 CUE 诊断；安全来源两端绝对帧换算；相邻 CUE 拼接逐样本等于连续转换；非零起点前滚；取消回收；DSD→WAV 零进程与 DSD→FLAC 单编码进程；DSD64/128/浮点 DXD 有效格式边界；分析输入与播放 pre-leveling PCM 一致。

API 样本由既有 `TestPostgreSQLAPIResponseSamples` 和 `TestPostgreSQLZonesAPIResponseSamples` 各自 recorder 拥有，不能手写响应 JSON。实际执行记录、成功/失败及未完成项见任务的 `backend-integration-report.md`。

## 7. 错误与正确做法

错误：把 2822400 当作 ring/player 的 PCM 时钟；每个 CUE 段单独 Reset；共享 next close 时取消父流；为 DSD WAV 调用 `Reserve(0)`；把已截取 stdin 再传 CUE 的 `-ss/-t`。

正确：文件事实保留 DSD 比特率，来源段使用 OutputRate；同父连续段共享状态，read 限段末，非连续跳转前滚；生产者引用归零才取消；零进程分支不预留；分析在 PCM 域只截取一次。

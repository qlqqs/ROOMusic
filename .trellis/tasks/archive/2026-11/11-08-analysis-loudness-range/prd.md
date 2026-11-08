# ⑨ 音频分析补充

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，M 线第 2 项。参考设计 25.4 章（一次调用多项产出）。

## 目标

曲目时长显示正确：MP3 扫描时就按 Xing/VBRI 头算出准确时长，其余头部给错时长的曲目在后台分析后按真实解码时长显示；同一次 ffmpeg 分析顺带保存响度范围与采样峰值，并在信号路径弹窗里显示。

## 现状（`B/` = `backend/internal/`）

- `track_analyses`（`backend/migrations/0033_track_analyses.sql`）存积分 LUFS、真峰值、解码时长 `duration_ms`、首尾静音点、波形（≤1000 点）与快照列；`analyzer_version`，当前 `Version=1`（`B/playback/analysis/analysis.go:20-25`）。
- ffmpeg 命令：`ebur128=peak=true:framelog=quiet` 加降采样 8 kHz 单声道 PCM（`analysis.go:112-123`）。没有取响度范围（LRA）与采样峰值。
- 后台 worker 单 goroutine，扫描时等待，两首之间间隔 max(本首耗时, 2 秒)（`B/playback/analysis_worker.go`）；版本号变化会让全库重新分析（`B/catalog/track_analysis.go:73-79`）。
- 解码时长已保存但只在分析内部用；曲目列表、版本总时长、资料库统计用扫描时解析出的时长，播放器的进度与总时长用浏览器的 `audio.duration`（`frontend/src/features/player/engine/player-engine.ts:965-975`）。
- MP3 时长 = 整个文件大小 × 8 ÷ 第一帧码率（`B/library/parser/mp3.go:231-261`）：不读 Xing/Info/VBRI 头，VBR MP3 的时长都是错的；文件大小含 ID3v2 标签，带大封面的 CBR MP3 也会多算几十秒；结果按整秒截断。浏览器会读 Xing 头，所以播放器显示的时长与列表不一致。
- 真实库（八号机，2026-11-08 只读统计）：199 FLAC、84 M4A、1 MP3，没有 APE/WavPack/AIFF。
- 前端不显示 LUFS 和真峰值的数值；信号路径弹窗显示“音量均衡 ±x dB”（`frontend/src/features/player/components/signal-path.tsx:49`）。

## 需求

- **R1 MP3 扫描时长**：解析 Xing/Info 与 VBRI 头的帧数，时长 = 帧数 × 每帧样本数 ÷ 采样率，码率按音频字节与时长算；没有这些头时按 CBR 估算，但只计音频字节（扣掉开头 ID3v2 与末尾 ID3v1/APEv2），不按整秒截断。④ 已合入时升 `.mp3` 的格式版本让已有 MP3 重新解析；④ 未合入时每次扫描本来就全量解析。
- **R1b 解码时长校正**：解码时长与扫描时长相差超过 max(1 秒, 1%) 或扫描时长未知时，读取接口给出的曲目时长（列表、版本总时长、资料库统计、播放上报门槛）以解码时长为准；CUE 虚拟轨按片段计算。不改源文件，不写回 `tracks`。播放器的进度与拖动仍跟浏览器（R1 修好后与列表一致；没有 Xing 头的 VBR MP3 在播放器里仍按浏览器的估算，不做前端换算）。
- **R2 LRA 与采样峰值**：同一次 ffmpeg 调用增加响度范围与采样峰值输出并保存，不增加解码遍数。
- **R3 版本**：分析器版本加一；已有结果在重算前继续使用，后台按现有节奏逐步重算。
- **R4 展示**：播放器信号路径弹窗增加一行，显示积分响度、响度范围与峰值；没有分析结果时不显示这一行。文案按 CLAUDE.md 前端文案规则。
- **R5 合同同步**：更新 `audio-analysis-contracts.md`、`library-scan-contracts.md`（MP3 时长规则）、`catalog-rest-contracts.md`（`duration_seconds` 是校正后的显示时长）。

## 验收

- 单测：Xing/Info/VBRI 帧数解析与时长；CBR 扣除标签字节；解析 ebur128 汇总中的 LRA 与采样峰值；时长校正的阈值判断；CUE 片段时长。
- 运行时生成的样本验证：带 Xing 头的 VBR MP3 扫描时长与 ffmpeg 解码时长相差不到 0.1 秒；没有 Xing 头的 VBR MP3 在分析完成后，发行详情接口给出解码时长。
- 只对改动相关的分析与读取接口跑最小测试集。

## 不做

- 频谱、逐声道峰值、DTS/MQA 标志、快/慢模式（参考设计 25.4 的其余产出，当前用不上）。

## 已裁决

- 2026-11-08 用户裁决：在信号路径弹窗里显示（R4）。
- 2026-11-08 主会话裁决（设计审阅）：响度一行的峰值显示真峰值（dBTP），采样峰值只保存。
- 2026-11-08 主会话裁决（设计审阅）：MP3 时长从解析器源头修（R1）；不做前端拖动换算。理由：没有 Xing 头的 VBR MP3 很少（LAME 写 VBR 默认带 Xing），真实库只有 1 首 MP3，前端换算要改交叉渐变、恢复播放、媒体会话的时间轴，风险与收益不相称。

## 依赖

M 线第 2 项，在 ⑩ 之后（新格式也要能被分析）。R1 的新代码放新文件（如 `parser/mp3_vbr.go`），`mp3.go` 只改 `applyMP3AudioFacts` 的调用处，避开 T 线对 ID3 帧解析的改动；MP3 解析器版本按 ④ 的版本机制加 1，合入时由主会话核对。

## 实施结果（2026-11-08）

提交：`098be1c`（MP3 时长读 Xing/Info、VBRI 帧数，CBR 估算扣除首尾标签字节；分析同一次 ffmpeg 调用产出 LRA 与采样峰值，`analysis.Version` 2；曲目时长改读显示时长视图；信号路径加“响度”一行）、`7494953`（真实服务信号路径断言按层名定位）。随 M 线合并 `6ee547b` 入 main，迁移 `0036_track_analysis_loudness_range`（M 线同步 S1 时在 `c4b7d28` 改号）。

裁决与偏离：实施中没有新增，均按上文“已裁决”实现。

验收：

- 单测：满足。`TestMP3StreamFactsUsesVBRFrameCounts`、`TestMP3CBRDurationExcludesLargeArtworkAndTrailingTags`、`TestEbur128SummaryParser`、`TestPostgreSQLAnalysisDisplayDurationsRespectThresholdAndSnapshot`（时长校正阈值）、`TestFFmpegAnalysisMeasuresLoudnessSilenceAndCUEInterval`（CUE 片段）。
- 运行时样本：满足。`TestFFmpegMP3XingDurationMatchesDecodedSamples`（带 Xing 头）；`TestPostgreSQLAnalysisCorrectsVBRWithoutXingDurationInReleaseHTTP`（没有 Xing 头，分析完成后发行详情给出解码时长）；旧结果重算前继续可用 `TestPostgreSQLAnalysisUpgradeKeepsDoneResultsAndPrioritizesUnanalyzedTracks`。
- 最小测试集：满足。M 线合入前只跑 `TestFFmpeg*`、分析与接口样本、迁移往返和前端接口合同；music/ 对比中唯一一首 MP3 扫描时长 290 → 261.12 秒，其余 283 首音频事实不变。

已知局限：没有 Xing 头的 VBR MP3 在播放器里仍按浏览器估算的时长显示，可能与列表不一致（R1b 已裁决接受）。

# 后台音频分析合同

2026-11-08 起（迁移 0033）。后端用 ffmpeg 在后台逐首慢速分析资料库里的曲目，得到 EBU R128 积分响度、响度范围、真峰值、采样峰值、波形与首尾静音点，
供前端的音量均衡（按 Roon）、交叉渐变与波形进度条使用。设计与取舍见[任务设计](../../tasks/archive/2026-11/11-08-player-overview-analysis/design.md)，
前端用法见[播放与个人状态](../frontend/playback-and-personal-state.md)。

## 1. 包与依赖方向

| 包 | 职责 |
| --- | --- |
| `playback/analysis` | 叶子包：定位并自检 ffmpeg、跑一首的分析、由 PCM 算波形与首尾点；不读数据库、不导入任何内部包、不用 `net/http`。常量 `Version`（分析器版本，当前 2）、`TargetLUFS = −14`、`PeakMarginDB = 0.5` |
| `catalog`（`track_analysis.go`） | 拥有表 `track_analyses`：`ListAnalysisCandidates`、`SaveTrackAnalysis`、`LoadTrackAnalysis`（含增益计算）、`AnalysisProgress`；读 `library_roots.status` 判断目录启用（与 `governance/claim_read.go` 同一先例） |
| `playback`（`analysis_worker.go`） | `AnalysisWorker` 调度循环、安全打开源文件、`AnalysisProgressProvider`；消费 catalog、library 公开合同与 `playback/analysis` |
| `catalog/httpapi` | `GET /api/v1/tracks/{id}/analysis`；**不导入** `playback/analysis`，分析器版本由 `app` 经 `catalogapi.WithAnalysisVersion(analysis.Version)` 注入 |
| `playback/httpapi` | `GET /api/v1/audio-analysis/progress`（`Options.AnalysisProgress`） |
| `app` | `OpenAnalysis` 探测、`WithAnalysis` 装配、启动 goroutine、`WaitForAnalysis`；“扫描是否在运行”端口由 `library.Service.ActiveScan` 装配 |

依赖规则由 `internal/app/dependency_rules_test.go` 守住：`playback/analysis` 无内部依赖，`playback` 允许导入它。

## 2. 表与“待分析”判定

`track_analyses`：主键 `track_id`（外键 `tracks`，级联删除），每首一行，**不存 pending 行**。列：`analyzer_version`；输入快照
`content_fingerprint`、`source_size`、`source_modified_at`、`cue_index_frames`、`cue_end_frames`；`status`（`done`/`failed`）；
`integrated_lufs`、`true_peak_dbtp`（`REAL`，可空）；0034 增加 `loudness_range_lu`（`REAL`，可空，非负）与 `sample_peak_dbfs`（`REAL`，可空）；`duration_ms`、`leading_silence_ms`（开头静音结束点）、`trailing_silence_ms`（结尾静音开始点，自曲目开头计）；
`waveform`（`BYTEA`，1–1000 字节）；`failure_category`（`source_unavailable|source_changed|ffmpeg_exit|parse|timeout`）；`attempts`；`analyzed_at`。
约束：`done` 必有波形与时长、无失败类别；`failed` 必有失败类别、无波形。0033 的 down 迁移只删表。

五项快照与 `tracks` 当前值逐项相同（`IS NOT DISTINCT FROM`）是共同前提，区分两种口径：

- **可用结果**：快照相同且 `status='done'`，不限制分析器版本。用于分析接口、专辑增益和显示时长；已有 v1 成功结果在重算前仍返回 `done`，两个新字段为 null。
- **当前版本结果**：快照相同且 `analyzer_version` 等于当前版本（2），包含成功与失败。用于候选判定及进度计数；旧版本失败返回 `pending`，由新版本重试。
- 新曲目没有行；内容、大小、修改时间或 CUE 区间变化让旧行不可用，读取返回 `pending`。升级只让结果不再属于当前版本，不让成功结果立即失效；不删除旧行。
- 改名或移动（按指纹重绑定，曲目 ID 不变）结果继续可用；CUE 虚拟轨的指纹、大小、修改时间是父文件的，靠起止帧区分各轨。
- 候选：`tracks.source_status='present'`、目录 `active`、扫描记录有大小与修改时间，且没有当前版本结果，或 `failed` 且 `attempts < 3` 且 `analyzed_at` 早于 24 小时前。
  顺序 `ORDER BY 有可用结果, (source_kind='cue_virtual'), release_id, disc_number, position, id`：没有结果者先做，升级重算在后；同类中按发行成组，CUE 虚拟轨最后。
- 写入是 UPSERT；同一快照与版本再次失败 `attempts + 1`，否则重置为 1。快照取自选中那一刻，分析期间曲目被改写则写入的行与当前值不符，下一轮自然重排，不加锁。

### 显示时长视图（0034）

`track_display_durations(track_id, duration_seconds)` 由 `tracks` 左联 `track_analyses`。扫描时长为 S，解码时长 D = `duration_ms / 1000.0`；
只有可用结果且 `duration_ms > 0` 时，S 未知（NULL、≤ 0、NaN、无穷）或 `ABS(D-S) > max(1 秒, 1% × D)` 才取 D，否则保持 S。
阈值只在视图定义，读取代码不重复判断，不写回扫描拥有的 `tracks.duration_seconds`。相差恰好阈值时保留扫描时长。
CUE 的分析时长本来就是 `-ss/-t` 解码的片段时长，末轨解码到父文件结尾，逐轨使用同一规则。

发行详情、批量曲目摘要、个人曲目事实、版本总时长、曲目集合统计和 `LoadTrackDuration` 均联此视图，只换时长来源，JSON 形状不变。
播放器进度和拖动仍用浏览器 `audio.duration`；无 Xing 头的 VBR MP3 可能与列表不同，不做前端时间轴换算。
0034 只加两个可空列和视图，不改写数据；down 先删视图再删两列，0033 的 down 才删整表。

## 3. 工作者

- 单 goroutine，一次一首，每批取 20 个候选。每首之前查“扫描在运行”：在运行就进入 `waiting_for_scan`，每 30 秒再查（正在跑的那一首让它跑完）。
- 没有候选进入 `idle`，等 1 分钟再查（新扫描的曲目最迟 1 分钟后开始）。数据库或读取扫描状态失败：记日志，等 1 分钟。
- 每首之后等 `max(2 秒, 本首耗时)`。单首失败写入 `failed` 行，不阻塞后续曲目；准备来源时遇到数据库错误不写行，等 1 分钟。
- 安全打开：读登记目录，未启用或不存在记 `source_unavailable`；`library.ValidatePath` 核对仍在允许范围；`os.OpenRoot` + `O_RDONLY|O_NONBLOCK`，必须是普通文件；
  大小与修改时间（截到微秒）等于扫描记录，否则 `source_changed`。CUE 虚拟轨打开 `cue_parent_relative_path`。不重算指纹、不嗅探容器。
- 进程：自成进程组（取消时 SIGKILL 整组，`WaitDelay` 2 秒），环境只给 `LC_ALL=C`；启动后 `setpriority` nice 19，Linux 上 `ioprio_set` 空闲 IO 类（失败忽略）；单首超时 10 分钟（`timeout`）。
- 生命周期：父 context 是应用生命周期；`main` 在 HTTP 停止、扫描结束之后 `WaitForAnalysis`，取消即杀进程组。
- 日志：`playback.analysis.ready`（`ffmpeg_version`）、`playback.analysis.unavailable`（`reason`：`disabled|not_found|version_failed|selftest_failed|unsupported_platform`）、
  `playback.analysis.failed`（`track_id`、`category`）、每 100 首 `playback.analysis.progress`（`analyzed`、`total`）、`playback.analysis.shutdown_timeout`；不含路径与 stderr。

## 4. ffmpeg 命令与自检

```text
ffmpeg -nostdin -hide_banner -nostats -loglevel info -threads 1 -protocol_whitelist file,pipe
       [-ss <index/75> [-t <(end-index)/75>]]          # 仅 CUE 虚拟轨；最后一轨没有 -t
       -i file:/dev/fd/3                               # 源句柄作为 fd 3；自检用 pipe:0
       -filter_complex "[0:a:0]asplit=2[l][w];[l]ebur128=peak=sample+true:framelog=quiet,anullsink;[w]aresample=8000,aformat=sample_fmts=s16:channel_layouts=mono[pcm]"
       -map "[pcm]" -f s16le pipe:1
```

- stdout 的 8 kHz 单声道 PCM 按 10 ms（80 个样本）取窗口峰值。波形分 `min(1000, 窗口数)` 段，每段取最大值，量化为 `round(峰值/32768×255)`。
  第一个与最后一个峰值大于 33（约 −60 dBFS）的窗口给出 `leading_silence_ms = 序号×10`、`trailing_silence_ms = (序号+1)×10`，全静音两者为 NULL；`duration_ms = 窗口数×10`。
- stderr 保留 64 KiB 尾部；同一次调用解析 `Integrated loudness: I: … LUFS`、`Loudness range: LRA: … LU`、`Sample peak: Peak: … dBFS` 和 `True peak: Peak: … dBFS`。
  真峰值和采样峰值分别匹配各自段落，不混用。积分响度 `-inf` 或 I ≤ −70 时 I 和 LRA 均为 NULL；峰值 `-inf` 记 NULL。
  缺少 LRA 或采样峰值段时对应字段为 NULL，不算失败；缺少积分响度或真峰值摘要、或没有 PCM 时记 `parse`。不增加解码遍数。
- 开关：`ROOMUSIC_AUDIO_ANALYSIS` 为 `on`（默认）/`off`，其他值配置加载失败；ffmpeg 路径复用 `ROOMUSIC_FFMPEG_PATH`（空则查 `PATH`）。
- `app.OpenAnalysis`：关闭时记 `unavailable reason=disabled`；否则定位、`-version`（5 秒）、自检（30 秒）：经 stdin 喂内存生成的 WAV（0.5 秒静音 + 1 秒 1 kHz −20 dBFS 正弦），
  要求 I 在 −23.3 ± 1.5 LUFS、开头静音 500 ± 30 ms，且能解析 LRA（不判数值）。任一步失败只记一条 `unavailable` 并不启动工作者，应用照常运行。

## 5. 增益公式（按 Roon）

- 曲目：`track_gain = min(−14 − I, −0.5 − TP)`，用存储精度计算后保留一位小数；可为正（提升安静曲目），提升会让真峰值超过 −0.5 dBTP 时减到正好不超过。
  I 或 TP 为 NULL 时 `track_gain_db` 为 null（前端按“响度未知”处理）。不用压限器或软削波。
- 留 0.5 dB：ebur128 的 4 倍过采样真峰值对接近奈奎斯特频率的内容可能低估约 0.5 dB，有损解码与浏览器重采样也会让样本间峰值略高于测量值。
- 专辑（同一 `release_id` 的 present 曲目）：每首必须有可用结果，或当前版本的不再重试失败，否则 `album` 是 null；**不再重试的失败与近乎静音的曲目跳过，不阻塞专辑增益**。
  `L_album = 10·log10(Σ dᵢ·10^(Iᵢ/10) / Σ dᵢ)`（dᵢ = `duration_ms`），`peak_album = max(TPᵢ)`，`album_gain = min(−14 − L_album, −0.5 − peak_album)`；
  没有一首可计入时为 null。增益在读取时计算、不落库，调整目标常量不必升分析器版本。

## 6. 接口

通则：先 `Guard.Authenticate`（401 `unauthorized`）；JSON `Cache-Control: no-store`，不加 ETag，前端由 Query 缓存；错误信封沿用平台合同。

**`GET /api/v1/tracks/{id}/analysis`**（所有登录用户）：400 `invalid_id`（非 UUID）→ 404 `not_found`（不存在或非 present）→ 503 `database_unavailable` → 200。

```json
{ "track_id": "…", "status": "done",
  "analysis": { "integrated_lufs": -21.8, "loudness_range_lu": 6.3, "true_peak_dbtp": -21.1, "sample_peak_dbfs": -21.3, "track_gain_db": 7.8, "duration_ms": 3000,
                "leading_silence_ms": 0, "trailing_silence_ms": 3000, "waveform": { "points": 300, "peaks": "<base64>" } },
  "album": { "release_id": "…", "gain_db": 7.8, "peak_dbtp": -21.1 } }
```

| `status` | 含义 | `analysis` / `album` |
| --- | --- | --- |
| `done` | 快照相同的任意版本成功结果 | 对象；LUFS、响度范围、两种峰值、增益、首尾点可为 null；`album` 按第 5 节可为 null |
| `pending` | 没有行、快照不符、旧版本失败，或当前版本失败后仍可重试 | 都是 null |
| `failed` | 当前版本不再重试（已到 3 次，或在 24 小时冷却内） | 都是 null；不暴露失败类别 |

响度范围（LU）和分贝值一位小数；`waveform.peaks` 是标准 base64，解码长度等于 `points`（1–1000）。

**`GET /api/v1/audio-analysis/progress`**（所有登录用户）：200 `{ "total", "analyzed", "failed", "state" }`，失败 503 `database_unavailable`。
`total` 是候选口径的全部曲目（present、目录 active、有大小与修改时间）；`analyzed` 是当前版本且快照相同的 `done`；`failed` 是当前版本且快照相同、不再重试的 `failed`。
升级后 `analyzed` 从 0 重新累计，表示重算进度；旧成功结果仍可读取。
`state` 为 `running | waiting_for_scan | idle | unavailable`（没有装配工作者时 `unavailable`，计数照常返回）。

## 7. 样本与测试

- 样本：`internal/app/testdata/api-samples/tracks/analysis-{done,pending,failed}.json`、`audio-analysis/progress.json`，由 `TestPostgreSQLAPIResponseSamples` 守住，前端 decoder 与 E2E 桩读同一批。
- 单元（不需要 ffmpeg 与数据库）：`go test ./internal/playback/analysis/`（窗口峰值、首尾点、波形、摘要解析）。
- 真实 ffmpeg：`ROOMUSIC_TEST_FFMPEG=<固定版本> go test ./internal/playback/analysis/ -run TestFFmpeg`。
- 隔离数据库：`go test ./internal/app/ -run 'TestPostgreSQL(Analysis|APIResponseSamples)' -count=1`（待分析判定与失败冷却、两个接口、曲目与专辑增益）；
  `go test ./internal/playback/ -run TestAnalysisWorker -count=1`（扫描让路、失败不阻塞、取消退出）；迁移上下行在 `internal/platform/postgres/migrations_rollback_integration_test.go`。

- ⑨ 单元补充：LRA、采样峰值、缺段与静音空值；真实 ffmpeg 用例核对两个新字段。
- ⑨ 隔离库补充：v1 成功仍为 done、新字段为 null；旧失败为 pending；无结果优先；阈值内、阈值边界、超阈值、未知时长及快照不符；CUE 片段与 `LoadTrackDuration` 同源；0034 上下行与 API 样本。

## 8. 正确、基准与错误案例

- 正确：扫描 80 秒、可用解码 100 秒，显示 100 秒；扫描 100.5 秒、解码 100 秒，保持 100.5 秒。
- 基准：升级到 v2 后，快照相同的 v1 成功行仍提供波形与增益，LRA、采样峰值为 null；进度不把它计入当前版本完成数。
- 错误：因版本变化隐藏已有成功结果，或把显示时长写回 `tracks`；正确做法是分开可用性与版本进度，经唯一视图读校正时长。

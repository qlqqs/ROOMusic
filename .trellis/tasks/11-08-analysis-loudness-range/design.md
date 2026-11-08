# ⑨ 音频分析补充：技术设计

需求见 [prd.md](./prd.md)；父任务见 [扫描子系统对标](../11-08-scan-reference-port/prd.md)。M 线第 2 项，在 ⑩ 合入线分支之后实施。
`B/` = `backend/internal/`，`F/` = `frontend/src/`；参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 包 / 文件 | 本项的职责 |
| --- | --- |
| `B/library/parser/mp3_vbr.go`（新文件） | MP3 时长与码率：Xing/Info、VBRI 帧数；没有这些头时按音频字节的 CBR 估算（R1） |
| `B/library/parser/mp3.go` | 只把 `parseMP3` 里对 `applyMP3AudioFacts` 的调用换成新函数，并删除随之不再使用的 `applyMP3AudioFacts`（同一段连续代码，不碰 ID3 帧解析） |
| `B/playback/analysis` | ebur128 改为同时取采样峰值；解析 LRA 与采样峰值；`Version` 1 → 2 |
| `B/catalog/track_analysis.go` | 保存两个新列；“可用结果”与“当前版本结果”分开（R3）；候选排序让没有结果的曲目先做；接口多两个字段 |
| `B/catalog` 读取（`read.go`、`track_summary.go`、`personal_reads.go`、`comparison.go`、`track_collection_stats.go`、`track_source.go`） | 时长改读视图 `track_display_durations`（R1b） |
| 迁移 `0036_track_analysis_loudness_range`（合入 S1 后由 0034 重编号为 0036） | 两个新列与时长视图 |
| `F/features/player/components/signal-path.tsx` | 新增“响度”一行（R4） |
| `F/features/player/api/decoders.ts` | 分析 DTO 两个新字段 |

不改：播放器的进度与拖动（仍用浏览器的 `audio.duration`，PRD R1b 已裁决）、分析工作者调度节奏、增益公式、波形与静音点算法、MP3 内容指纹、ID3 帧解析。
新解析代码是无状态纯函数，满足 ⑤ 并发解析的要求。

## 2. 数据流

```
扫描：parseMP3 → applyMP3StreamFacts：mp3AudioRange 得到音频区间 → 区间内找第一帧 → Xing/Info 或 VBRI 帧数 → 时长、码率
      （没有这些头 → 音频字节 × 8 ÷ 第一帧码率）→ tracks.duration_seconds
分析：工作者 → ffmpeg（同一次调用，ebur128=peak=sample+true）→ stderr 摘要：I、LRA、Sample peak、True peak；stdout PCM：波形、静音点、duration_ms
      → SaveTrackAnalysis（多存 loudness_range_lu、sample_peak_dbfs，analyzer_version=2）
读取：tracks ⟕ track_analyses → 视图 track_display_durations（阈值判定）→ 各读取接口的 duration_seconds
      GET /api/v1/tracks/{id}/analysis → 多返回 loudness_range_lu、sample_peak_dbfs
前端：列表与队列显示接口时长；播放器进度跟浏览器；信号路径弹层显示响度一行
```

## 3. 接口合同

### 3.1 MP3 扫描时长（R1）

新函数 `applyMP3StreamFacts(reader io.ReaderAt, fileSize int64, observation *AudioObservation)`，放在 `parser/mp3_vbr.go`；`parseMP3` 的调用处改为调用它（不再传 `audioStart`）。

1. **音频区间**：复用 `fingerprint.go` 的 `mp3AudioRange(reader, size)`，得到扣掉开头（可叠加的）ID3v2 与末尾 ID3v1/`TAG+`、APEv2、Lyrics3v2 后的 `[start, end)`。读取出错时不填音频参数（与现状一致）。
2. **第一帧**：在 `[start, min(start + 64 KiB, end))` 内逐字节找第一个能被现有 `decodeMP3FrameHeader` 接受的 Layer III 帧头（沿用现有搜索窗口与规则），记偏移 `frame`；
   采样率、声道、帧头码率取自它。MPEG 版本位与声道模式直接从帧头字节读：版本 `(b1>>3)&3`（3 = MPEG-1，2 = MPEG-2，0 = MPEG-2.5），单声道 = `b3>>6 == 3`。
3. **每帧样本数**：MPEG-1 Layer III 为 1152，MPEG-2/2.5 Layer III 为 576（只认 Layer III，与现有帧头判定一致）。
4. **Xing/Info**：位于 `frame + 4 + 边信息长度`；边信息长度 MPEG-1 双声道 32、单声道 17，MPEG-2/2.5 双声道 17、单声道 9。标识为 `Xing` 或 `Info`，其后 4 字节大端标志：
   `0x1` 有帧数（4 字节大端），`0x2` 有字节数（4 字节大端），依次排列。
5. **VBRI**：没有 Xing/Info 时查 `frame + 36` 处的 `VBRI`：版本（2）、延迟（2）、质量（2）、字节数（4）、帧数（4），均为大端。
6. **有帧数时**：时长 = 帧数 × 每帧样本数 ÷ 采样率（浮点，不截断）；帧数为 0 视为没有。码率（kbps，四舍五入取整）= 字节数 × 8 ÷ 时长 ÷ 1000，
   字节数取头里的值（大于 0 时），没有则取 `end − frame`。帧数不含信息帧本身。
   2026-11-08 用固定 ffmpeg 实测：10 秒的 LAME VBR 样本，Xing 帧数 384 → 10.031 秒，ffmpeg 解码 10.00 秒；差值是没扣的编码延迟与补齐（约 30 ms），在 0.1 秒验收线内。
7. **没有帧数时**（CBR 或不带头的 VBR）：时长 = `(end − frame) × 8 ÷ (帧头码率 × 1000)`，浮点不截断；码率取帧头码率。不带头的 VBR 在这里仍会偏差，由 R1b 在分析后校正。
8. 不做：LAME 头的编码延迟与补齐（gapless）、Xing 的 TOC、逐帧遍历计数。

现有 `applyMP3AudioFacts` 删除（它用含 ID3v2 的整个文件大小除以第一帧码率并按整秒截断，正是要修的缺陷）；`decodeMP3FrameHeader` 与码率表保留并被新代码复用。

**与 ④ 解析器版本的配合**（与 ⑩ design 3.8 同一写法）：

- 若 ④ 先合入 main：本项合入时 MP3 的解析器版本加 1（按格式记版本时加 `mp3`；全局版本时加全局版本），让已有 MP3 在下一次扫描重新解析。
- 若本项先合入：当时每次扫描都全量解析，下一次扫描即修正已有 MP3；④ 以合入后的解析结果为基线，无需额外动作。由主会话合入时核对。

内容指纹（`mp3-frames-v1`）不受影响，曲目身份不变；分析快照不含时长，已有分析结果继续有效。

### 3.2 ffmpeg 命令与解析（R2）

- 滤镜由 `ebur128=peak=true:framelog=quiet` 改为 `ebur128=peak=sample+true:framelog=quiet`，其余参数、解码遍数不变。ffmpeg 7.1.1 实测摘要格式：

  ```text
    Loudness range:
      LRA:        20.0 LU
    Sample peak:
      Peak:      -38.1 dBFS
    True peak:
      Peak:      -38.1 dBFS
  ```

- 新增两个正则：`Loudness range:\s*LRA:\s*(数值)\s*LU`、`Sample peak:\s*Peak:\s*(数值|-inf)\s*dBFS`；现有真峰值正则以 `True peak:` 开头，不会误取采样峰值。
- 积分响度为 NULL（静音或 ≤ −70 LUFS）时 LRA 也记 NULL；采样峰值 `-inf` 记 NULL。缺少这两段**不算解析失败**（仍以积分响度与真峰值为准判 `parse`）。
- 自检：沿用现有素材与判据，另要求摘要里能解析出 LRA（只要求存在，不判数值）。
- `analysis.Version = 2`；`Result` 增加 `LoudnessRangeLU`、`SamplePeakDBFS *float64`。

### 3.3 迁移

`0036_track_analysis_loudness_range.sql`：

```sql
ALTER TABLE track_analyses
    ADD COLUMN IF NOT EXISTS loudness_range_lu REAL CHECK (loudness_range_lu IS NULL OR loudness_range_lu >= 0),
    ADD COLUMN IF NOT EXISTS sample_peak_dbfs REAL;

CREATE OR REPLACE VIEW track_display_durations AS
SELECT t.id AS track_id,
       CASE WHEN a.status = 'done' AND a.duration_ms > 0
             AND a.content_fingerprint IS NOT DISTINCT FROM t.content_fingerprint
             AND a.source_size IS NOT DISTINCT FROM t.source_size
             AND a.source_modified_at IS NOT DISTINCT FROM t.source_modified_at
             AND a.cue_index_frames IS NOT DISTINCT FROM t.cue_index_frames
             AND a.cue_end_frames IS NOT DISTINCT FROM t.cue_end_frames
             AND (t.duration_seconds IS NULL
                  OR NOT (t.duration_seconds > 0 AND t.duration_seconds < 'Infinity'::float8)
                  OR ABS(a.duration_ms / 1000.0 - t.duration_seconds) > GREATEST(1.0, a.duration_ms / 100000.0))
            THEN a.duration_ms / 1000.0
            ELSE t.duration_seconds END::float8 AS duration_seconds
FROM tracks t LEFT JOIN track_analyses a ON a.track_id = t.id;
```

down：`DROP VIEW IF EXISTS track_display_durations; ALTER TABLE track_analyses DROP COLUMN IF EXISTS sample_peak_dbfs, DROP COLUMN IF EXISTS loudness_range_lu;`。
视图沿用 0017“有效值视图”的先例：阈值只在这一处定义，所有读取点 join 同一个名字。

### 3.4 解码时长校正（R1b）

- **阈值**：解码时长 `D = duration_ms / 1000` 与扫描时长 `S` 相差超过 `max(1 秒, 1% × D)`，或 `S` 未知（NULL、≤ 0、非有限）时，显示时长取 `D`；否则仍取 `S`（不让 10 ms 粒度的解码时长替换准确的标签时长）。
  R1 修好后，带 Xing/VBRI 头的 MP3 与 CBR MP3 的扫描时长已经准确，不会触发；主要剩下不带头的 VBR MP3 与其他头部写错时长的文件。
- **用哪条分析**：只看“可用结果”（3.5）：`done`、`duration_ms > 0`、五项快照与当前曲目相同；**不看分析器版本**，所以升版本后旧结果在重算前继续用于校正。
- **CUE 虚拟轨**：分析本来就按 `-ss/-t` 只解码该轨的片段，`duration_ms` 就是片段时长；最后一轨没有 `-t`，解码到父文件结尾。同一条阈值规则逐轨适用，无需特例。
- **读取点**（全部改为 `LEFT JOIN track_display_durations dd ON dd.track_id = t.id` 并读 `dd.duration_seconds`）：
  `read.go`（发行详情曲目）、`track_summary.go`（两处）、`personal_reads.go`、`comparison.go`（版本总时长）、`track_collection_stats.go`（资料库总时长）、`track_source.go` 的 `LoadTrackDuration`（播放上报门槛）。
  JSON 字段名与形状不变（仍是 `duration_seconds`），只是值可能被校正。
- 播放上报的门槛前后端都用校正后的时长（前端 `PlayerTrack.durationSeconds` 来自同一批接口），保持一致。
- 不写回 `tracks.duration_seconds`：该列归扫描所有，每次重扫都会重写。
- 播放器不做换算：进度与拖动跟浏览器的 `audio.duration`。不带 Xing 头的 VBR MP3 在播放器里仍按浏览器的估算显示，与列表可能不一致（PRD R1b 已裁决接受）。

### 3.5 “可用结果”与“当前版本结果”（R3）

| 概念 | 条件 | 用途 |
| --- | --- | --- |
| 可用结果 | 五项快照相同、`status='done'`，任意版本 | 分析接口返回 `done`；专辑增益；时长视图 |
| 当前版本结果 | 可用或失败，且 `analyzer_version = 当前版本` | 候选判定（是否需要重算）；进度接口的 `analyzed`/`failed` |

- **分析接口**：快照相同的 `done` 行，不论版本，返回 `status: "done"`；v1 行的 `loudness_range_lu`、`sample_peak_dbfs` 为 null。快照相同但版本旧的 `failed` 行返回 `pending`（新版本会重试）。
- **专辑增益**：“每首都有可用结果，或当前版本的不再重试失败”时计算，规则同现合同第 5 节，只把“当前有效行”换成“可用结果”。
- **候选**：判定不变（没有当前版本结果，或可重试失败）；排序改为 `ORDER BY 有可用结果, (source_kind='cue_virtual'), release_id, disc_number, position, id`：
  先做没有任何结果的新曲目，升版本带来的重算排在后面。
- **进度接口**：口径不变（当前版本），升版本后 `analyzed` 从 0 重新累计，表示重算进度。
- 增益不受影响：增益只依赖积分响度与真峰值，两版本的测量方法相同。

### 3.6 分析接口（`GET /api/v1/tracks/{id}/analysis`）

`analysis` 对象增加两个字段（一位小数，可为 null），其余不变：

```json
"analysis": { "integrated_lufs": -21.8, "loudness_range_lu": 6.3, "true_peak_dbtp": -21.1, "sample_peak_dbfs": -21.3, "track_gain_db": 7.8, … }
```

样本 `B/app/testdata/api-samples/tracks/analysis-done.json` 同步增加，前端 decoder 读同一份。

### 3.7 前端：信号路径弹层的响度一行（R4）

- 在“数据源”与“服务端”之间增加一层：名称 `响度`，图标用 lucide 的 `AudioLines`（或同类波形图标），取值如 `−14.2 LUFS · 范围 6.3 LU · 峰值 −0.8 dBTP`。
- 三项各自缺失时省略；分析结果不是 `done`、或三项都为 null 时整行不显示。数值一位小数，负号用 `−`（与增益写法一致）。峰值用真峰值（dBTP），采样峰值只保存、不显示（已裁决）。
- 数据来自现有 `useTrackAnalysis(trackId)`（与波形进度条共用缓存）。
- 不加任何解释文字（不写“EBU R128”“响度范围表示动态”之类）。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 25.4.1 #1 时长按实际解码样本统计，用于回填与交叉校验（4992） | 照搬 | 解码时长与扫描时长按阈值交叉校验，扫描时长未知时直接回填 |
| 参考设计之外：MP3 扫描时长读 Xing/Info、VBRI 帧数，CBR 估算只计音频字节 | 新增 | 参考设计没有这条（它的 MP3 参数来自原生解码层）；服务于 25.4.1 #1 同一个目标——时长准确。从源头修，绝大多数 MP3 不必等分析就显示正确时长，分析校正只兜底剩下的文件 |
| 25.4.1 #12 帧数，精确时长 = 帧数/采样率（5003） | 改写 | 分析侧沿用 8 kHz 单声道 PCM 的 10 ms 窗口计数，精度 ±10 ms，远小于 1 秒阈值；不为此增加一路全采样率输出 |
| 25.4.1 #5 积分响度（4996） | 照搬（现状） | 已有 |
| 25.4.1 #6 响度范围 LRA（4997） | 照搬 | 同一次 ebur128 调用产出，不增加解码遍数 |
| 25.4.1 #7 真峰值数组（每声道）（4998） | 改写 | 只存全声道最大值（现状）；PRD“不做”逐声道峰值 |
| 25.4.1 #8 采样峰值数组（每声道）（4999） | 改写 | 只存全声道最大值：ebur128 摘要给出的就是总体值，逐声道不在范围内 |
| 25.4.1 #2/#3 静音起止点（4993–4994） | 照搬（现状） | 已有 |
| 25.4.1 #4 DTS 标志、#11 MQA 标志（4995、5002） | 不采用 | PRD“不做” |
| 25.4.1 #9 分析声道数（5000） | 不采用 | 不存逐声道数组，不需要有效长度 |
| 25.4.1 #10 波形 2000 点（5001） | 改写（现状） | 沿用 ≤ 1000 点；改点数要升版本并改前端，没有收益 |
| 25.4.1 #13–#15 频谱（5004–5006） | 不采用 | PRD“不做” |
| 25.4.2 峰值数组按声道数截断（5017–5020） | 不采用 | 不存数组，没有残留值问题 |
| 25.4.3 取消回调，取消后产出全部作废（5022–5032） | 照搬（现状） | context 取消即杀进程组，不写任何行 |
| 25.4.3 慢速/快速两种模式（5034） | 不采用 | 只有一种模式：分析本来就在后台低优先级慢速进行 |
| 25.4.4 清理顺序：先解码器、再媒体文件、最后缓冲（5036–5045） | 改写（现状） | 等价顺序：先等 ffmpeg 进程（持有 fd 3）退出或被杀，再由调用方关闭源句柄；Go 的 defer 保证异常、取消、成功三条路径一致 |
| 父 PRD：分析器版本化、升版本全量重算 | 改写 | 升版本仍全量重算，但旧结果在重算前继续可用（R3），候选排序先做没有结果的曲目 |

## 5. 兼容与已有数据

- MP3：下一次（重新）解析后，`tracks.duration_seconds` 与 `bitrate` 变为新算法的值；曲目 UUID、指纹、Release 不变。真实库只有 1 首 MP3。
  扫描反馈的变更记账若比较时长，这些 MP3 会在那一次扫描里计为有变化，只发生一次。
- 已有 v1 分析行保留：分析接口仍返回 `done`（两个新字段为 null），波形、均衡、交叉渐变照常工作；工作者按现有节奏逐首重算为 v2。
- 进度接口在升版本后显示重算进度（`analyzed` 从 0 开始）。
- 时长：视图上线即生效，已有 v1 结果的曲目立刻按阈值校正显示时长；被校正的只是相差明显的曲目。
- 资料库统计与版本对比的总时长可能随校正改变；播放上报门槛随之改变（前后端同源，保持一致）。
- 迁移只加可空列与视图，不改写数据；down 迁移删除视图与两列。

## 6. 风险与回滚

| 风险 | 对策 |
| --- | --- |
| 第一帧搜索命中假同步，或 Xing 偏移算错，时长离谱 | 偏移规则以 ffmpeg `libavformat/mp3dec.c` 为对照；单测覆盖 MPEG-1/2 × 单/双声道的 Xing 位置与 VBRI；运行时生成的带 Xing VBR MP3 与 ffmpeg 解码时长相差 < 0.1 秒。仍有遗漏时由 R1b 在分析后校正显示 |
| 前端在分析完成之间读到不同时长，播放上报门槛与服务端不一致 | 只在分析刚完成的窗口内发生；服务端门槛有 1 秒容差，差异大到被拒的情形极少，接受 |
| 视图 join 拖慢全库统计 | 主键 LEFT JOIN；`track_collection_stats` 跑一次 `EXPLAIN ANALYZE` 对比（已有统计诊断用例） |
| 很短的曲目 LRA 数值无意义 | 照存测量值；前端只显示，不据此做任何判断 |
| T 线改 `mp3.go` 的 ID3 帧解析 | 本项在 `mp3.go` 只改一处调用并删除 `applyMP3AudioFacts` 这一段连续代码，与 ID3 帧解析不重叠 |
| T 线（⑥ ⑧）或 O 线改同一批读取 SQL | 本项只把 `t.duration_seconds` 换成 `dd.duration_seconds` 并加一行 join，合并冲突易解；见 implement.md |

回滚点：
- MP3 时长可单独回退（恢复 `mp3.go` 的调用与旧函数、删 `mp3_vbr.go`）；若 ④ 已在 main，回退时 MP3 版本同样要再加 1。
- 前端响度一行可单独回退。
- 后端分析部分回退时先下迁移（删视图与两列）再回退代码，旧代码按版本不符把 v2 行视为待分析并逐首重算回 v1，期间这些曲目暂时没有分析结果。

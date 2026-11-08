# ⑨ 音频分析补充：实施计划

设计见 [design.md](./design.md)。M 线第 2 项，在 ⑩ 合入 M 线分支之后开始；后端与前端可并行（接口形状在 design 3.6 已定）。
子 agent 不提交 git。`B/` = `backend/internal/`，`F/` = `frontend/src/`。

## 全局约定（写进每个派活 prompt）

- 前端文案：不写防御性说明，不加不必要的说明；确实需要的说明用一句日常话。响度一行只有名称与数值，不加任何解释。
- 测试只做必要的：只测改动直接影响的部分，跑下面列出的最小测试集；不跑全量，不为假想场景写测试，不做演练、不重复验证、不堆兜底代码。

## 后端（GPT-6-Astra，high，顺序执行）

1. **MP3 时长** 新文件 `B/library/parser/mp3_vbr.go`：`applyMP3StreamFacts`（design 3.1：`mp3AudioRange` 取音频区间、第一帧、Xing/Info 与 VBRI、每帧样本数、CBR 浮点估算）。
   `B/library/parser/mp3.go` 只改 `parseMP3` 里的一处调用，并删除不再使用的 `applyMP3AudioFacts`（连续一段，不碰 ID3 帧解析）。
2. **迁移** `backend/migrations/0034_track_analysis_loudness_range.sql` 与 `.down.sql`（design 3.3）；编号是 M 线基线上的下一个空号，合入时由主会话重新编号。
   若 `B/platform/postgres/migrations_rollback_integration_test.go` 逐个列出迁移，把 0034 加进去。
3. **分析器** `B/playback/analysis/analysis.go`：滤镜 `peak=sample+true`；两个新正则；`Result` 两个新字段；LRA 在积分响度为 NULL 时置 NULL；自检要求 LRA 可解析；`Version = 2`。
4. **存取** `B/catalog/track_analysis.go`：`TrackAnalysisResult`/`TrackAnalysisData` 加 `LoudnessRangeLU`、`SamplePeakDBFS`（JSON `loudness_range_lu`、`sample_peak_dbfs`）；
   `SaveTrackAnalysis` 写两列；`loadTrackAnalysis` 与 `albumAnalysis` 改用“可用结果”（快照相同的 `done`，任意版本；旧版本 `failed` 视为 `pending`）；
   `ListAnalysisCandidates` 排序首键改为“是否已有可用结果”；`AnalysisProgress` 不变。
5. **工作者** `B/playback/analysis_worker.go`：把新字段从 `analysis.Result` 传到 `catalog.TrackAnalysisResult`。
6. **时长读取**：`read.go`、`track_summary.go`（两处）、`personal_reads.go`、`comparison.go`、`track_collection_stats.go`、`track_source.go`（`LoadTrackDuration`）改读 `track_display_durations`。
7. **样本** `B/app/testdata/api-samples/tracks/analysis-done.json` 加两个字段。
8. **合同**：`audio-analysis-contracts.md`（命令、两列、版本 2、“可用结果/当前版本结果”、候选排序、视图与阈值、接口字段）；
   `library-scan-contracts.md`（MP3 时长规则）；`catalog-rest-contracts.md` 与 `listening-contracts.md` 注明 `duration_seconds` / 门槛时长是校正后的显示时长。中文书写。
9. **④ 版本**：若 ④ 已在 M 线基线里，MP3 解析器版本加 1（design 3.1）；否则不动，由主会话合入时核对。

## 前端（Opus，一个子 agent 一次做完）

1. `F/features/player/api/decoders.ts`：分析 DTO 加 `loudnessRangeLu`、`samplePeakDbfs`（可为 null），样本测试随后端样本更新。
2. `F/features/player/components/signal-path.tsx`：“响度”一行（design 3.7）。
3. `.trellis/spec/frontend/player-design-guidelines.md` 信号路径表加“响度”一层。

播放器引擎不改：进度与拖动仍用 `audio.duration`。

## 测试（GPT 测试 agent）

### 样本（运行时生成到 `t.TempDir()`，不提交）

用 `ROOMUSIC_TEST_FFMPEG` 指向的固定 ffmpeg 现场生成；未设置时这些用例跳过。两首都是 4 秒静音接 6 秒噪声的 LAME VBR（2026-11-08 已实测生成）：

```bash
F="$ROOMUSIC_TEST_FFMPEG"
SRC=(-f lavfi -i "anullsrc=r=44100:cl=stereo:d=4" -f lavfi -i "anoisesrc=r=44100:d=6:a=0.3"
     -filter_complex "[0:a][1:a]concat=n=2:v=0:a=1,aformat=channel_layouts=stereo" -c:a libmp3lame -q:a 0)
# 带 Xing 头（ffmpeg 默认写）：Xing 帧数 384 → 10.03 秒，ffmpeg 解码 10.00 秒
$F -hide_banner -loglevel error "${SRC[@]}" vbr-xing.mp3
# 不带 Xing 头：扫描只能按 CBR 估算（偏差很大），靠分析后的 R1b 校正
$F -hide_banner -loglevel error "${SRC[@]}" -write_xing 0 vbr-noxing.mp3
```

### 需要补的测试（最小集）

| 位置 | 内容 |
| --- | --- |
| `B/library/parser`（单元，合成字节） | Xing 帧数与字节数（MPEG-1 双声道、MPEG-2 单声道两种偏移）、`Info` 标识、VBRI；只有帧数没有字节数时码率按音频字节算；CBR：开头 ID3v2（带大封面）与末尾 ID3v1/APEv2 不计入时长，结果不按整秒截断 |
| `B/library/parser` `TestFFmpeg…` | `vbr-xing.mp3` 的扫描时长与 ffmpeg 解码时长（`-f null` 的样本数或 `duration_ms`）相差 < 0.1 秒 |
| `B/playback/analysis`（单元） | 摘要解析：含 LRA 与采样峰值；采样峰值 `-inf`；缺少两段时仍成功且为 null；积分响度为 NULL 时 LRA 为 null |
| `B/playback/analysis` `TestFFmpeg…` | 现有真实分析用例断言 LRA 与采样峰值非空（不新增用例） |
| `B/app` `TestPostgreSQLAnalysis…` | 旧版本 `done` 行返回 `done` 且新字段为 null；旧版本 `failed` 返回 `pending`；候选先排没有结果的曲目；时长视图四种情况（相差在阈值内用扫描时长、超过阈值用解码时长、扫描时长为空用解码时长、快照不符用扫描时长）；`LoadTrackDuration` 返回校正值 |
| `B/app` `TestPostgreSQLAnalysis…`（真实 ffmpeg） | `vbr-noxing.mp3` 扫描入库、分析完成后，发行详情接口的 `duration_seconds` 是解码时长（约 10 秒） |
| `B/app` `TestPostgreSQLAPIResponseSamples` | 分析样本 |
| 迁移 | 现有上下行回滚用例覆盖 0034 |
| 前端 Vitest | 信号路径响度一行（有值、部分缺失、没有结果时不显示）；分析 DTO 解码 |

不做浏览器验证：播放器没有改动，响度一行由组件测试覆盖。

### 验证命令

```bash
cd backend
go test ./internal/library/parser/ -run 'TestMP3' -count=1
go test ./internal/playback/analysis/ -count=1
ROOMUSIC_TEST_FFMPEG="$(../scripts/ffmpeg-pinned.sh)" go test ./internal/library/parser/ ./internal/playback/analysis/ -run TestFFmpeg -count=1
ROOMUSIC_TEST_FFMPEG="$(../scripts/ffmpeg-pinned.sh)" ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/app/ -run 'TestPostgreSQL(Analysis|APIResponseSamples)' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/playback/ -run TestAnalysisWorker -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/platform/postgres/ -run 'Rollback' -count=1
cd ../frontend && npx vitest run src/features/player/components/signal-path src/features/player/api/decoders
```

不跑全量：改动集中在 MP3 时长函数、分析器、分析存取、时长视图的读取点与信号路径一行；读取点只换了时长来源，上面的分析与样本用例已覆盖。
若 `catalog` 读取 SQL 的改动让发行详情或统计的现有用例失败，只补跑对应的 `TestPostgreSQL(Catalog|Stats)…` 定位，不扩大到全量。
现有 MP3 解析用例若断言了旧的整秒时长，按新规则更新期望值，不另加用例。

## 共享文件与冲突点

| 文件 | 本项改动 | 也会改的线 |
| --- | --- | --- |
| `B/library/parser/mp3.go` | `parseMP3` 一处调用；删除 `applyMP3AudioFacts` | T ⑥ ⑦（ID3 帧解析，如 TIPL/TMCL、多值），与本项改动段不重叠 |
| `B/library/parser/mp3_vbr.go`（新文件） | MP3 时长与码率 | 无 |
| `B/library/parser/fingerprint.go` | 不改，只复用 `mp3AudioRange` | ⑩（同线，已在前） |
| `B/catalog/read.go` | 曲目时长改读视图 | T ⑧（作品与乐章可能加到发行详情）、O ②（落选 Release 处理可能动读取） |
| `B/catalog/track_summary.go`、`personal_reads.go`、`comparison.go`、`track_collection_stats.go`、`track_source.go` | 时长来源 | 冲突可能性低 |
| `backend/migrations/` | 0034 | 各线都会加迁移，合入时由主会话按顺序重新编号 |
| `F/features/player/components/signal-path.tsx` | 响度一行 | ⑩（同线，已在前） |
| `.trellis/spec/backend/library-scan-contracts.md` | MP3 时长规则 | ① ② ③ ④ ⑤ ⑫ ⑩ 都会改 |
| `.trellis/spec/backend/catalog-rest-contracts.md` | 时长说明一句 | T 线可能改发行详情合同 |

## 风险文件与回滚点

- 风险最高：`mp3_vbr.go`（Xing 偏移与第一帧搜索）、`track_analysis.go`（可用结果与当前版本的口径）。
- 回滚点 1：只回退 MP3 时长（恢复 `mp3.go` 调用与旧函数、删新文件）；若 ④ 已在 main，MP3 版本再加 1。
- 回滚点 2：只回退前端响度一行。
- 回滚点 3：回退分析部分：先跑 0034 down，再回退代码。

## 合入前核对

- [ ] `mp3.go` 的改动只有一处调用与删除 `applyMP3AudioFacts`；带 Xing 的 VBR 样本扫描时长与解码时长相差 < 0.1 秒。
- [ ] ④ 的 MP3 解析器版本已按 design 3.1 的两种情况处理。
- [ ] 同一次 ffmpeg 调用产出 LRA 与采样峰值，没有增加解码遍数（命令只改 `peak=` 参数）。
- [ ] `analysis.Version` 为 2；旧结果在重算前仍返回 `done`；进度接口口径未变。
- [ ] 时长阈值只在视图里定义（`max(1 秒, 1%)`）；不带 Xing 的 VBR 样本分析后发行详情给出解码时长。
- [ ] 所有读取点的 JSON 形状未变；接口样本与前端解码一致。
- [ ] 信号路径响度一行没有解释文字，没有结果时不出现；播放器引擎没有改动。
- [ ] 迁移编号已按合入顺序重排，上下行通过。

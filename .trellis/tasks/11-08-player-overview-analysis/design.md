# 技术设计：全屏播放器、后台音频分析与概览改版

需求唯一来源是同目录 [prd.md](./prd.md)（R1–R7、D1–D5、A1–A10）。本文只写怎么做与为什么，不扩大、不缩小范围；需求本身有疑问的地方集中在第 9 节“需主会话裁决”。

## 0. 总览与关键结论

| 主题 | 结论 |
| --- | --- |
| 分析结果放哪 | 新表 `track_analyses`（迁移 0033，带 down），每首曲目一行；**不存 pending 行**，“待分析”由查询比对得出 |
| 失效键 | 行里保存分析时的输入快照：`content_fingerprint`、`source_size`、`source_modified_at`、`cue_index_frames`、`cue_end_frames`、`analyzer_version`；任一项与 `tracks` 当前值不同即视为待分析，读取接口同样按此判定，重新扫描后旧结果立即失效 |
| 工作者 | `playback` 里的单 goroutine，一次一首，ffmpeg 一次解码同时得到响度、真峰值与 8 kHz 单声道 PCM（Go 侧算波形与首尾静音）；nice 19 + 空闲 IO 类；两首间隔 `max(2s, 上一首耗时)`；扫描运行时让路；随应用生命周期取消；ffmpeg 不可用或 `ROOMUSIC_AUDIO_ANALYSIS=off` 时不运行 |
| 三个接口 | `GET /api/v1/tracks/{id}/analysis`、`GET /api/v1/audio-analysis/progress`、`GET /api/v1/catalog/stats`；所有登录用户只读，JSON `no-store`，不加 ETag，由前端 Query 缓存 |
| 音量均衡 | **按 Roon**（用户 2026-11-08 定）：EBU R128，目标 −14 LUFS（固定）；可以提升，按真峰值限幅到不削波（留 0.5 dB 余量）；不用压限器；模式 关 / 自动 / 按曲目 / 按专辑；响度未知的曲目固定 −5 dB；实际增益写在信号路径里 |
| Web Audio 与否 | **用 Web Audio**：`<audio>` → `MediaElementAudioSourceNode` → 均衡 `GainNode` → 渐变 `GainNode` → 用户音量 `GainNode` → `destination`；渐变用 `setValueCurveAtTime` 精确调度。均衡与渐变都关、iPhone/iPad、或 AudioContext 没能进入 running 时**不接入**，管线与现在完全一致（第 4.6 节） |
| 全屏开合状态 | 放在浏览器历史条目的 state 里（`router.history.push(当前地址, {...state, roomusicPlayer: 本次页面加载 ID})`），返回键自然收起，不碰各路由的 search 校验 |
| `/now-playing` | 改为根路由下的跳转：`redirect({ to: "/", replace: true })`；侧栏“正在播放”入口删除 |
| 播放设置 | `localStorage["roomusic:playback-settings:v1"]`，设备偏好（与音量同类），默认均衡“自动”、渐变关、5 秒，可选 3/5/8/12 秒；设置新增“播放”栏目，所有成员可见 |
| Android 后台 | 判断为可以继续出声（播放中的 AudioContext 不因切后台或锁屏被挂起），但没有实机确证；万一被中断，当页自动换回不接入的音频元素从原位置继续（第 4.6 节“中断退回”） |
| 实施拆分 | 合同（本文第 3 节）定稿后：后端 B1（统计接口）、B2（分析）与前端 F1（R1/R4 文案 + 概览）、F2（全屏 / 抽屉 / 弹层）可并行；F3（波形 + 引擎均衡与渐变 + 播放设置）在 F2 之后 |

## 1. 后端：数据模型与迁移 0033

### 1.1 归属

- 表 `track_analyses` 归 **catalog 核心**拥有（与 `tracks.lyrics` 一样是曲目的派生事实；待分析判定要 JOIN `tracks`，跨能力读私表不允许）。catalog 提供公开函数，SQL 只在 `backend/internal/catalog/track_analysis.go`。
- ffmpeg 调用与 PCM 计算在新叶子包 `backend/internal/playback/analysis`（不读数据库、不依赖任何内部包，同 `derivation` 的约束）。
- 调度工作者在 `backend/internal/playback/analysis_worker.go`：消费 catalog 与 library 的公开合同、`playback/analysis` 的分析器端口和“扫描是否在运行”的窄端口。
- `app` 负责探测、装配、启动 goroutine 与停机等待；`internal/app/dependency_rules_test.go` 增加 `playback/analysis` 的规则（无内部依赖、不用 `net/http`），`playback` 允许导入它。

### 1.2 表结构

```sql
-- 0033_track_analyses.sql
CREATE TABLE IF NOT EXISTS track_analyses (
    track_id            UUID PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    analyzer_version    SMALLINT    NOT NULL CHECK (analyzer_version > 0),
    -- 分析时的输入快照：与 tracks 当前值逐项比较（IS NOT DISTINCT FROM），任何一项不同即视为待分析
    content_fingerprint TEXT,
    source_size         BIGINT,
    source_modified_at  TIMESTAMPTZ,
    cue_index_frames    INTEGER,
    cue_end_frames      INTEGER,
    status              TEXT        NOT NULL CHECK (status IN ('done','failed')),
    integrated_lufs     REAL,        -- NULL：低于门限（近乎静音）
    true_peak_dbtp      REAL,        -- NULL：全静音
    duration_ms         INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
    leading_silence_ms  INTEGER CHECK (leading_silence_ms IS NULL OR leading_silence_ms >= 0),  -- 开头静音结束点
    trailing_silence_ms INTEGER CHECK (trailing_silence_ms IS NULL OR trailing_silence_ms >= 0), -- 结尾静音开始点（自曲目开头计）
    waveform            BYTEA CHECK (waveform IS NULL OR octet_length(waveform) BETWEEN 1 AND 1000),
    failure_category    TEXT CHECK (failure_category IS NULL OR failure_category IN
                          ('source_unavailable','source_changed','ffmpeg_exit','parse','timeout')),
    attempts            SMALLINT    NOT NULL DEFAULT 1 CHECK (attempts > 0),
    analyzed_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT track_analyses_status_ck CHECK (
        (status = 'done'   AND failure_category IS NULL AND waveform IS NOT NULL AND duration_ms IS NOT NULL) OR
        (status = 'failed' AND failure_category IS NOT NULL AND waveform IS NULL))
);
-- 0033_track_analyses.down.sql
DROP TABLE IF EXISTS track_analyses;
```

- 不另建索引：待分析查询是 `tracks` 对主键的反连接，2,500 首是毫秒级，10 万首也只是几十毫秒，而每首分析要数秒。
- `analyzer_version` 是 `playback/analysis.Version` 常量（初始 1）；改命令行、阈值、波形分辨率或目标响度算法时加一，全部曲目自然重排。

### 1.3 与内容指纹 / 文件身份的关系

| 情况 | 结果 |
| --- | --- |
| 新扫描进来的曲目 | 没有行 → 待分析 |
| 文件内容变了（重新扫描后指纹、大小或修改时间变化） | 快照不一致 → 读取接口立刻返回 `pending`，工作者重排；不需要删除旧行 |
| 只是改名 / 移动（扫描按指纹重绑定，曲目 ID 不变） | 指纹、大小、修改时间都不变 → 结果继续有效 |
| CUE 虚拟轨 | 指纹、大小、修改时间是父文件的；`cue_index_frames`/`cue_end_frames` 区分同一文件里的各轨，区间变化即失效 |
| 曲目被删 | 外键级联删除 |
| 曲目 `missing` 或目录停用 | 不进入待分析与总数；行保留，恢复后仍可用 |

快照取自工作者选中曲目那一刻的 `tracks` 行；分析期间若扫描改写了曲目，写入的行与当前值不一致，下一轮自然重排，不需要加锁。

## 2. 后端：分析工作者

### 2.1 一首曲目的分析（`playback/analysis`）

1. 安全打开（复用 `playback` 的做法，但不要求格式在播放格式表里）：曲目 present、目录 `active`、`library.ValidatePath` 通过、`os.OpenRoot` + `O_RDONLY|O_NONBLOCK`、普通文件、大小与修改时间等于扫描记录。CUE 虚拟轨打开 `cue_parent_relative_path`。不重算内容指纹、不嗅探容器（分析结果不是交付物，ffmpeg 自己探测格式）。打开或核对失败记 `source_unavailable` / `source_changed`。
2. 一次 ffmpeg，固定参数数组、不经 shell，源句柄作为 fd 3：

```text
ffmpeg -nostdin -hide_banner -nostats -loglevel info -threads 1 -protocol_whitelist file
       [-ss <index/75> [-t <(end-index)/75>]]          # 仅 CUE 虚拟轨；最后一轨没有 -t
       -i file:/dev/fd/3
       -filter_complex "[0:a:0]asplit=2[l][w];[l]ebur128=peak=true:framelog=quiet,anullsink;[w]aresample=8000,aformat=sample_fmts=s16:channel_layouts=mono[pcm]"
       -map "[pcm]" -f s16le pipe:1
```

   - stdout：8 kHz 单声道 s16le，Go 边读边按 10 ms（80 个样本）取窗口峰值，存 `[]uint16`（5 分钟约 3 万个窗口、60 KB，不缓存整段 PCM）。
   - stderr：64 KiB 尾部缓冲，结束后解析 ebur128 的 `Summary:` 块里 `Integrated loudness: I: <x> LUFS` 与 `True peak: Peak: <x> dBFS`；`-inf` 或 I ≤ −70 记 NULL。解析不到记 `parse`。
   - 进程：沿用派生的进程组回收（`Setpgid`、取消时 SIGKILL 进程组、`WaitDelay` 2 秒、环境只给 `LC_ALL=C`）；启动后立即 `syscall.Setpriority(PRIO_PROCESS, pid, 19)`，Linux 上再用 `ioprio_set` 设空闲 IO 类（失败忽略，只是少了保护）；单首超时 10 分钟（`timeout`）。进程辅助函数在 `playback/analysis` 里另写一份薄实现（约 60 行），**不改动** `derivation` 已验证的代码。
3. 由窗口峰值得到：
   - 波形：分成 `min(1000, 窗口数)` 段，每段取最大值，量化为 `round(peak / 32768 × 255)` 的 1 字节线性幅度。
   - `leading_silence_ms`：第一个峰值高于 −60 dBFS（|x| > 33）的窗口序号 × 10；`trailing_silence_ms`：最后一个高于门限的窗口（序号 + 1）× 10；全静音两者为 NULL。
   - `duration_ms`：窗口数 × 10。
4. 写入：`catalog.Store.SaveTrackAnalysis(ctx, input, result)` 以 UPSERT 写入；同一快照与版本的再次失败 `attempts + 1`，否则重置为 1。

### 2.2 调度循环（`playback.AnalysisWorker`）

```text
for 生命周期未结束:
    if 扫描在运行: state = waiting_for_scan; 等 30 秒; continue
    batch = catalog.ListAnalysisCandidates(version, 20)
    if batch 为空: state = idle; 等 5 分钟; continue      # 新扫描的曲目最迟 5 分钟后开始
    state = running
    for c in batch:
        if 生命周期结束: return
        if 扫描在运行: break                               # 正在跑的那一首让它跑完（nice 19，数秒）
        r = analyze(c)                                      # 失败也写入 failed 行，不阻塞后续
        save(r)
        等 max(2 秒, 本首耗时)                              # 占用不超过约半个核
数据库错误：记日志，等 1 分钟重试
```

- 待分析条件（`ListAnalysisCandidates`）：`tracks.source_status='present'`、所在 `library_roots.status='active'`（已有 `catalog/governance/claim_read.go` 读该表的先例）、扫描记录有大小与修改时间；且没有行、版本不同、快照不同，或 `status='failed' AND attempts < 3 AND analyzed_at < now() - interval '24 hours'`。失败最多再试两次、间隔一天，覆盖 NAS 临时离线；解码类失败三次后停下，直到文件变化或版本升级。
- 顺序：JOIN `media` 取 `release_id`，`ORDER BY (t.source_kind='cue_virtual'), m.release_id, t.disc_number, t.position, t.id` ——按发行成组分析，专辑增益尽早可用；CUE 虚拟轨排最后（第 9 节 Q4）。
- “扫描在运行”端口：`func(ctx) (bool, error)`，由 `app` 用 `library.Service.ActiveScan` 装配。
- 与派生不协调：派生是 nice 0、用户在等的工作，分析 nice 19 不会抢占它。

### 2.3 启动、关闭与开关

| 项 | 做法 |
| --- | --- |
| ffmpeg 路径 | 复用 `ROOMUSIC_FFMPEG_PATH`（空则查 `PATH`），不新增路径配置 |
| 开关 | 新环境变量 `ROOMUSIC_AUDIO_ANALYSIS`：`on`（默认）/ `off`，在 `platform/config` 解码，其他值配置加载失败 |
| 探测 | `app.OpenAnalysis(ctx, settings, logger)`：定位 ffmpeg、`-version`（5 秒）、自检（30 秒）：经 stdin 喂一段内存生成的 WAV（0.5 秒静音 + 1 秒 1 kHz −20 dBFS 正弦，44.1 kHz 立体声），要求 I 在 −23.3 ± 1.5 LUFS、开头静音 500 ± 30 ms。失败记一条 `playback.analysis.unavailable`（只含 `reason`）并不启动工作者，应用照常 |
| 启动 | 数据库可用且探测通过时，`app.New` 起 goroutine，父 context 是应用生命周期 |
| 停机 | `main` 在 HTTP 停止、扫描结束之后调用 `application.WaitForAnalysis(ctx)`（与 `WaitForCatalogMaintenance` 同形），取消即杀进程组 |
| 日志 | `playback.analysis.ready`（`ffmpeg_version`）、`playback.analysis.unavailable`（`reason`）、`playback.analysis.failed`（`track_id`、`category`）、每完成 100 首一条 `playback.analysis.progress`（`analyzed`、`total`）；不含路径与 stderr |

### 2.4 耗时估算

| 场景 | 估算 |
| --- | --- |
| 5 分钟 16/44.1 FLAC，单核空闲 | FLAC 解码 < 0.5 s；ebur128 真峰值（4 倍过采样）是大头，约 1.5–3 s；重采样与 Go 计算可忽略 → **约 2–4 s** |
| 24/96 或 ALAC | 约 2 倍，4–8 s |
| 2,500 首（平均 4 分钟），含两首间隔 | (3 s + 3 s) × 2,500 ≈ **4 小时**；8 号机这类低功耗 NAS CPU 约慢 2–3 倍 → **8–12 小时**，扫描期间暂停另计 |

### 2.5 曲目增益与专辑增益（按 Roon）

- 目标响度常量 `analysis.TargetLUFS = −14`（EBU R128 积分响度，不在界面暴露）；余量常量 `analysis.PeakMarginDB = 0.5`。
- 曲目增益：`track_gain = min(−14 − I, −0.5 − TP)`。可以为正（提升安静曲目）；若提升会使真峰值超过 −0.5 dBTP，就把增益减到正好不超过为止。不用压限器或软削波。I 为 NULL（近乎静音）时增益为 NULL，前端按“响度未知”处理。
- 留 0.5 dB 余量的理由：ebur128 的真峰值是 4 倍过采样估计，按 ITU-R BS.1770 附录 2，对接近奈奎斯特频率的内容最多会低估约 0.5–0.7 dB；有损格式解码、浏览器按输出设备重采样也会让样本间峰值略高于测量值。0.5 dB 的响度差几乎听不出，却能避开这类边缘削波。
- 专辑增益：对同一**发行**（`release_id`）的全部 present 曲目，**没有任何一首处于待分析**时才计算（失败或近乎静音的曲目跳过）：
  `L_album = 10·log10( Σ dᵢ·10^(Iᵢ/10) / Σ dᵢ )`（dᵢ = `duration_ms`，按能量与时长加权，近似整张连播的积分响度），`peak_album = max(TPᵢ)`，`album_gain = min(−14 − L_album, −0.5 − peak_album)`。
- 例：流行曲 I = −9.8、TP = −0.3 → `min(−4.2, −0.2) = −4.2 dB`；古典曲 I = −22.4、TP = −6.1 → `min(+8.4, +5.6) = +5.6 dB`（被真峰值限住）。
- 有曲目还没分析完时 `album = null`，前端按第 4.6 节的规则退回。
- 数值保留一位小数；在读取时计算，不落库（发行成员变化、单曲重新分析、目标常量调整都自然生效，不需要升分析器版本）。

## 3. 接口合同

通则：都注册为 GET；检查顺序 401 `unauthorized` →（有路径参数时）400 `invalid_id` → 404 `not_found` → 503 `database_unavailable` → 200；JSON 经 `httpserver.WriteJSON`，`Cache-Control: no-store`，**不加 ETag**（单首响应约 1.5 KB，前端 Query 缓存足够）。错误信封沿用平台合同。样本放 `backend/internal/app/testdata/api-samples/`（`tracks/analysis-done.json`、`tracks/analysis-pending.json`、`audio-analysis/progress.json`、`catalog/stats.json`、`catalog/stats-artists-preparing.json`），由 `TestPostgreSQLAPIResponseSamples` 守住，前端 decoder 测试与 E2E 桩读取同一批样本。

### 3.1 按曲目取分析结果

`GET /api/v1/tracks/{id}/analysis`，注册在 `catalog/httpapi`（与歌词同处）。曲目不存在或非 present 为 404。

```json
{
  "track_id": "5c0a2f8e-6d3b-4c1e-9a7f-0b1d2e3f4a5b",
  "status": "done",
  "analysis": {
    "integrated_lufs": -9.8,
    "true_peak_dbtp": -0.3,
    "track_gain_db": -4.2,
    "duration_ms": 314020,
    "leading_silence_ms": 120,
    "trailing_silence_ms": 312400,
    "waveform": { "points": 1000, "peaks": "AAECAwQF…（base64，points 个字节）" }
  },
  "album": { "release_id": "0e1f2a3b-4c5d-4e6f-8a9b-0c1d2e3f4a5b", "gain_db": -3.1, "peak_dbtp": -0.2 }
}
```

| `status` | `analysis` | 说明 |
| --- | --- | --- |
| `done` | 对象；`integrated_lufs`/`true_peak_dbtp`/`track_gain_db`/首尾点可为 `null`（近乎静音或全静音） | 快照与版本都与当前一致 |
| `pending` | `null` | 没有行、快照或版本不一致、或失败后仍可重试 |
| `failed` | `null` | 不再重试（到 3 次或在 24 小时冷却内）；不暴露失败类别 |

`album` 只在 `status=done` 且该发行没有待分析曲目时非空。`waveform.points` 1–1000，`peaks` 是标准 base64（带填充），解码后长度必须等于 `points`。

### 3.2 分析进度

`GET /api/v1/audio-analysis/progress`，注册在 `playback/httpapi`（工作者状态在 playback，计数经 `catalog.Store.AnalysisProgress(ctx, version)`）。

```json
{ "total": 2512, "analyzed": 1830, "failed": 4, "state": "running" }
```

- `total`：待分析口径下的全部曲目（present、目录 active、有大小与修改时间）；`analyzed`：其中当前有效的 `done`；`failed`：当前有效且不再重试的 `failed`。
- `state`：`running | waiting_for_scan | idle | unavailable`（探测失败或开关关闭为 `unavailable`，计数照常返回）。

### 3.3 资料库统计

`GET /api/v1/catalog/stats`，注册在 `catalog/httpapi`，在一个只读快照事务里读完四个数：

```json
{ "albums": 312, "tracks": 2512, "artists": 418, "composers": 57 }
```

| 字段 | SQL 口径（与列表页一致） |
| --- | --- |
| `albums` | 与 `GET /release-groups` 不带筛选的 `pagination.total` 完全相同：`WITH ` + `releaseGroupRankedCTE(false)` + `SELECT COUNT(*) FROM ranked WHERE member_rank = 1` |
| `tracks` | `SELECT count(*) FROM tracks WHERE source_status = 'present'`（present 曲目所在发行必然可见；跨版本不去重，见第 9 节 Q3） |
| `artists` | 与 `GET /artists` 不带筛选的 `total` 相同：把 `ListArtists` 里 `selected` CTE 的计数抽成 `artistListCount(ctx, query, initial, role)`，此处传空参数 |
| `composers` | 同上，`role='composer'`（等于 `/artists?role=composer` 的总数） |

艺人数据未就绪（`ErrArtistCatalogPreparing` 或能力不可用）时 `artists`、`composers` 为 `null`，不报错。

### 3.4 前端解码与查询键

| 接口 | feature / 解码器 | Query key | 缓存策略 |
| --- | --- | --- | --- |
| 分析结果 | `features/player/api`：`decodeTrackAnalysis`，`peaks` 解成 `Uint8Array` | `viewerQueryKey(g, "player", "analysis", trackId)` | `done`：`staleTime: Infinity`；`pending`/`failed`：5 分钟；会话结束随代次清空 |
| 进度 | `features/library/api`：`decodeAnalysisProgress` | `viewerQueryKey(g, "audio-analysis", "progress")` | `running`/`waiting_for_scan` 时每 60 秒轮询（页面隐藏暂停），其他状态停止 |
| 统计 | `features/catalog/api`：`decodeCatalogStats` | `catalogKeys.stats(g)`（在 `catalog` 前缀下，修改专辑数据后的 `useReleaseDataRefresh` 一并失效） | 默认 |

分析读取失败（网络、404、旧后端）一律按“没有分析结果”处理：进度条退回普通样式；均衡按“响度未知”施加 −5 dB；渐变仍可进行（首尾点退回媒体时长、不跳开头静音）；不提示用户。

## 4. 前端播放（R3–R6）

### 4.1 组件与状态归属

```text
AppShell（app/shell）
├─ PlayerBar(renderCurrentTrackActions, renderQueueActions)          features/player
│   ├─ ExpandArtworkButton（封面按钮 → 打开全屏）
│   ├─ WaveformSeek
│   ├─ SignalPathButton → SignalPathDialog
│   ├─ QueueButton → QueueDrawer → PlayerQueue（沿用）
│   └─ PlayerFaultInline（故障文字 + 重试图标按钮）
└─ FullscreenPlayer(renderCurrentTrackActions, renderQueueActions)   features/player
    ├─ VinylDeck（唱片 + 唱臂）  ⇄  LyricsPanel（沿用）
    ├─ 标题 / 专辑链接 / ArtistNames / 心形插槽
    └─ FullscreenControls：收起、SignalPathButton、Transport、ModeControl、QueueButton、WaveformSeek、VolumeControl
```

- 删除 `features/player/pages/now-playing-page.tsx(.module.css)`、`app/routes/now-playing-route.tsx`、`NowPlayingPage` 导出与 `nowPlayingRoute`；`SignalPath` 区块改为弹层内容。
- 插槽新去处：`AppShell` 里的模块级函数 `renderCurrentTrackActions`（心形；播放栏用 `message="screen-reader"`，全屏用完整提示）与 `renderQueueActions = (tracks) => <SaveQueueAsPlaylist tracks={tracks} />` 同时传给 `PlayerBar` 与 `FullscreenPlayer`，再经 `QueueButton → QueueDrawer → PlayerQueue` 交给队列标题旁。
- 侧栏 `nav-items.ts` 删除“正在播放”（`NavItem.to` 联合去掉 `/now-playing`）。
- 旧地址：`router.tsx` 根路由下 `createRoute({ path: "/now-playing", beforeLoad: () => { throw redirect({ to: "/", replace: true }) } })`，放进 `legacyRoutes`（不用 `legacyRedirectRoute`：它会把路径后缀与 `#queue` 原样拼到目标上）。

### 4.2 全屏开合

| 关注点 | 做法 |
| --- | --- |
| 状态在哪 | 历史条目 state：`roomusicPlayer === PLAYER_LOAD_ID`（模块加载时生成的随机串）。`useFullscreenPlayer()` 返回 `{ open, openPlayer, closePlayer }`，`open` 由 `useRouterState({ select: s => s.location.state.roomusicPlayer === PLAYER_LOAD_ID })` 得出。刷新后 ID 不同，不会自动重开 |
| 打开 | `router.history.push(location.href, { ...location.state, roomusicPlayer: PLAYER_LOAD_ID, playerFrom: <原条目的 __TSR_key> })`：地址不变，不触发外壳的“换页回顶并聚焦主区” |
| 收起 | 向下箭头按钮、Esc（`ModalDialog.onRequestClose`）都调用 `router.history.back()`；浏览器返回键原生生效 |
| 点专辑 / 艺人 | 普通导航（push 新条目，state 不带标记）→ 全屏自动收起；在新页面按返回会回到全屏播放器（与“返回到刚才的位置”一致） |
| 专辑页定位副作用 | `album-detail-view.tsx:198` 按 `__TSR_key` 判断“新到达”。打开全屏会产生新 key，须改为 `state.playerFrom ?? state.__TSR_key ?? …`，否则在带 `?track=` 的专辑页上打开全屏会重新滚动、抢焦点 |
| 队列被清空 | `FullscreenPlayer` 发现 `current === null` 时调用 `closePlayer()` |
| 容器 | 复用 `ModalDialog`（原生 `<dialog>` 模态：焦点约束、Esc、关闭后焦点回到封面按钮）；`initialFocus` 指向“收起”按钮；`aria-labelledby` 指向全屏里的曲目标题 |
| 过渡动画 | 打开时从播放栏封面位置放大淡入（打开前量出封面矩形写入 `--origin-x/--origin-y`，`transform-origin` 用它，220 ms）；收起反向 180 ms 后再真正关闭；`prefers-reduced-motion: reduce` 时都没有动画 |

### 4.3 全屏版面

- **背景**：底层是封面图（`artworkPath` 同源地址）放大 1.2 倍，`filter: blur(64px) saturate(1.2) brightness(0.45)`，上面再叠 `rgb(0 0 0 / 35%)`；没有封面用 `neutral-900`。全屏是唯一的深色面：在容器里局部改写文字变量（主文字 `neutral-100`、次要 `neutral-300`、强调 `accent-300`、分隔线白色 16%），`LyricsPanel` 与控件沿用变量，不另写一套组件；实现时逐项核对对比度 ≥ 4.5:1（用最亮封面截图验证）。
- **黑胶**：`VinylDeck` 纯 CSS + 内联 SVG：盘面 `repeating-radial-gradient` 画纹路，中心圆形 `PlayerArtwork`（约 62% 直径）；`status === "playing"` 时 `animation: spin 24s linear infinite` 运行，否则 `animation-play-state: paused`（停在当前角度）。唱臂是 SVG，绕右上方支点旋转：`isPlaybackActive(status)` 时落下（0°），否则抬起（−24°），`transition: transform 400ms`。减少动态效果：唱片不转，唱臂直接到位。盘面尺寸 `min(44vh, 38vw, 440px)`。
- **桌面（≥ 768px）**：上方两栏——左唱片，右侧依次是标题（`h2`，两行截断、`title` 给全称）、“专辑 · 艺人”链接行、心形、歌词面板（占满剩余高度、内部滚动）；底栏一行：左“收起”与信号路径圆点，中间传输控制与波形进度（两端时间），右播放模式、队列图标、音量。
- **窄屏（< 768px，含 375）**：单栏、只有一个滚动区。顶部“收起”；中间区域默认唱片，唱片本身是按钮（`aria-label="显示歌词"`），歌词视图右上角一个图标按钮（`Disc3`，`aria-label="显示唱片"`）切回；歌词行仍是定位按钮。下面标题、链接、心形；底栏两行：波形与时间一行，传输控制一行，信号路径圆点、播放模式、队列在同一行两端；窄屏不显示音量滑块（移动端用实体键）。不允许横向溢出（A4）。
- 状态文字（正在准备、读取失败等）复用 `PlayerFaultInline`，不显示第几首 / 共几首。

### 4.4 播放栏

- 封面变成 `ExpandArtworkButton`（`aria-label="打开全屏播放器"`，悬停提示同文）；标题改为纯文本（不再是链接），专辑链接与艺人链接保留。
- 原来的格式链接改为：信号路径圆点按钮 + 格式文字（纯文本）；“队列（N）”链接改为图标按钮（`ListMusic`，`aria-label="队列（N）"`，悬停提示“队列”），打开抽屉。
- 进度条换成 `WaveformSeek`。
- 故障：`transient` 写“读取失败”，`busy` 写“服务器正忙”，旁边一个无边框图标按钮（`RotateCcw`，`aria-label="重试播放"`）调用 `engine.retry()`；全屏里同样显示。`autoplay_blocked` 仍写“请按播放”。公告区文案见第 6 节。
- 清空队列后播放栏消失：外壳在 `hasPlayer` 由真变假时把焦点放到主区（抽屉随播放栏卸载，焦点不能落在 body）。

### 4.5 队列抽屉与信号路径弹层

**队列抽屉 `QueueDrawer`**
- `QueueButton` 自己持有 `open` 状态并渲染抽屉；播放栏和全屏各一个实例。全屏里打开时抽屉是嵌套在全屏之上的第二个 `<dialog>`，Esc 只关最上层（`ModalDialog` 已处理）。
- 抽屉：右侧贴边，`width: min(420px, 100vw)`、满高，标题“队列（N）”加关闭图标按钮（`X`，`aria-label="关闭队列"`）；内容就是现有 `PlayerQueue`（播放此项、上移、下移、移除、清空并确认、存为歌单插槽、随机时的那句提示），去掉 `id="queue"` 与页面区段语义，删除后焦点的兜底从 `#main` 改为抽屉标题。
- 点外面关闭：`ModalDialog` 增加可选 `closeOnBackdrop`（点击目标是 `<dialog>` 本身时调用 `onRequestClose`，内容包一层占满的容器）。关闭后焦点回到队列图标（`ModalDialog` 已有，A5）。
- 抽屉打开时滚动到当前项。

**信号路径弹层 `SignalPathDialog`**
- `SignalPathButton`：12 px 实心圆点放在 32 px 命中区里，`aria-label="信号路径：无损"`（或“有损”“信号路径”），悬停提示同文；打开同样用 `ModalDialog` + `closeOnBackdrop`：桌面定位在播放栏（或全屏底栏）上方靠近圆点处，窄屏为底部弹出层。
- 标题：`信号路径：无损` / `信号路径：有损` / 编码未知时只写 `信号路径`。
- 质量判定（只看源编码，D 规则来自 R4）：

| 编码（`codec`，小写） | 整体质量 | 圆点 |
| --- | --- | --- |
| `flac`、`alac`、`pcm` | 无损 | 实心 `--color-accent` |
| `mp3`、`aac`、`vorbis`、`opus` | 有损 | 实心 `neutral-500` |
| 其他 / 缺失 | 未知 | 空心 `neutral-600` 描边 |

- 四层，每层一个 lucide 图标 + 名称 + 一行取值（`<ol>`，不再有任何解释句）：

| 层 | 图标 | 取值拼法 |
| --- | --- | --- |
| 数据源 | `FileAudio` | 无损：`编码 采样率 位深 声道`，如 `FLAC 44.1kHz 16bit 2ch`；有损：`编码 码率 采样率 声道`，如 `MP3 320kbps 44.1kHz 2ch`；采样率去掉多余的 `.0`（`48kHz`）；缺的项省略，全缺写“未知格式” |
| 服务端 | `Server` | `原文件直出`；派生写 `ALAC → FLAC`；格式表未取到写 `读取中` |
| 浏览器 | `AppWindow` | 以“ · ”连接：`解码`；均衡实际施加时加 `音量均衡 +2.1 dB` / `音量均衡 −4.2 dB`（快照 `leveling.gainDb`，一位小数，总带正负号，0 写 `0.0 dB`；响度未知时写实际施加的 `音量均衡 −5.0 dB`）；渐变开着且当前已接入时加 `交叉渐变 5 秒`。没接入（均衡关、设备不支持、未能接入或已中断退回）时不写这两项 |
| 系统输出 | `Speaker` | `系统默认输出` |

### 4.6 引擎改造：均衡（按 Roon）与交叉渐变

**结论：用 Web Audio，按需接入。** 用户 2026-11-08 定“按照 Roon 的来”：安静曲目要能提升，`element.volume` 最大只有 1，做不到，所以均衡与渐变都放在 `GainNode` 上。

**音频图**（每个 `<audio>` 一条支路，两条支路共用总音量节点）：

```text
<audio> → MediaElementAudioSourceNode → levelGain（均衡）→ fadeGain（渐变）→ masterGain（用户音量 / 静音，共用）→ destination
```

- 用户音量：接入后元素的 `volume` 固定为 1、`muted` 为假，用户音量写 `masterGain.gain`（静音写 0）；没接入时照旧写 `element.volume` / `element.muted`。一个 `applyUserVolume()` 按是否接入分两支，音量滑块只反映用户音量（R6）。
- **删除 `volumechange` → `syncVolume` 回读**（接入后元素音量恒为 1，回读会把用户音量改坏）：`setVolume`/`setMuted` 直接更新快照并保存。

**接入与退出规则**

| 情况 | 行为 |
| --- | --- |
| 均衡为“关”且渐变关（页面加载时） | 不创建 AudioContext，元素不接入，管线与现在完全一致（R6 末条） |
| 设备不支持（`element.volume` 只读，即 iPhone / iPadOS；或没有 `AudioContext`） | 永不接入；两项不可用，设置页禁用并写 D7 那一句。iOS 上经 Web Audio 的声音在锁屏、切后台时会被系统中断，不能冒这个险 |
| 需要接入（均衡不是“关”或渐变开，且设备支持） | 在用户手势命令（`play`、`togglePlayback` 的播放分支、`playTracks`、`playEntry`、`next`、`previous`、`retry`）开头同步调用 `ensureAudioGraph()`：没有就 `new AudioContext({ latencyHint: "playback" })`，并调用 `ctx.resume()`。真正接入（`createMediaElementSource`）只在**加载一首曲目、设置 `src` 之前**进行，且要求此刻 `ctx.state === "running"`（预检耗时足够 resume 完成）；不是 running 就**这一首不接入、原样播放**（快照 `leveling` 为 null），下一次手势命令再试 |
| 播放已接入的元素前 ctx 不是 running（例如暂停期间被系统挂起） | 先 `await ctx.resume()`（最多 1 秒，仍在同一次手势的激活期内）；还不是 running → 走“中断退回” |
| 运行中把两项都关掉 | 保持接入，`levelGain`、`fadeGain` 归 1，作废渐变计划；到重新加载页面才完全退出（元素一旦接入无法拆出） |
| 运行中打开 | 下一次手势命令创建并 resume 上下文，从下一首加载起接入 |
| **中断退回** | 已接入且引擎在出声时，`ctx` 的 `statechange` 变为非 running：立即 `listen.suspend()`；本页标记 `audioRoute = "fallback"`，丢弃已接入的元素，用 `createAudio()` 新建一个不接入的元素，按现有 `load(当前条目, 当前位置, "continue")` 从原处继续（`play()` 若被拒，走现有 `autoplay_blocked`“请按播放”）。之后本页不再接入，均衡与渐变在本页失效，信号路径不再写增益 |

由此保证不会出现“元素在播却无声、聆听照算”：接入的元素只在 ctx 为 running 时才 `play()`；运行中被挂起会立刻暂停计数并换回直出元素。

**Android 锁屏 / 后台的判断与风险**
- 判断：Chrome for Android 上**会继续出声**。依据：Chrome 只在自动播放策略下让未经手势的 AudioContext 停在 suspended，不会因页面隐藏而挂起正在运行、正在出声的上下文；音频元素本身仍在播放，Chrome 的媒体通知、锁屏控件与媒体会话照常由它驱动，后台渲染进程按“有声音的媒体”保活。
- 风险：没有实机确证；厂商省电策略或其他浏览器（Firefox、三星浏览器）可能在后台挂起上下文。
- 应对：只用上面的“中断退回”，不再加别的防御。不安排专门的实机演练（主会话 2026-11-08 定，Q9）：用户日常在 Android 上使用时留意，若出现锁屏中断再讨论是否在 Android 上默认不接入。

**均衡**
- `EngineDeps` 增加：`loadAnalysis(trackId, signal): Promise<TrackAnalysis | null>`（provider 用 `queryClient.fetchQuery` 实现，与波形组件共用缓存；失败返回 null）、`createAudio(): AudioLike`（渐变与中断退回用的新元素）、`createAudioContext: (() => AudioContextLike) | null`（生产给 `() => new AudioContext(...)`，测试给假实现或 null）。
- `load()` 在发预检的同时请求分析（只在会接入时）；预检就绪后若分析还没回来，最多再等 1 秒，仍没有就按“响度未知”处理这一首。增益在 `play()` 之前用 `levelGain.gain.setValueAtTime(线性值, ctx.currentTime)` 设好，播放中不因分析迟到而跳变。当前曲目第一次出声时顺带预取下一首的分析。
- 增益选择（“响度未知”= 没有分析结果、分析未完成或失败、读取失败、1 秒内没回来，或 `track_gain_db` 为 null）：

| 模式 | 施加的增益 |
| --- | --- |
| 关 | 0 dB（因渐变而接入时 `levelGain` 为 1） |
| 按曲目 | `track_gain_db`；未知 → −5 dB |
| 按专辑 | `album.gain_db`；专辑没分析完（`album` 为 null）→ `track_gain_db`；曲目也未知 → −5 dB |
| 自动（默认） | 播放模式不是随机，且队列列表顺序里当前项的前一项或后一项与它 `releaseId` 相同 → 按“按专辑”；否则按“按曲目” |

- 增益可正可负，原样施加（后端已按真峰值限幅，见第 2.5 节），前端不再截断、不加压限。线性值 `10^(dB/20)`。
- 运行中切换模式或开关：对当前曲目用 `setTargetAtTime(新值, now, 0.05)` 平滑过去。
- 快照新增：`leveling: { gainDb: number; basis: "track" | "album" | "unknown" } | null`（实际施加时非空，信号路径据此显示）、`audioRoute: "direct" | "web_audio" | "fallback"`、`volumeControllable: boolean`（构造时把 `volume` 设为 0.5 再读回判断，随即恢复）。

**交叉渐变**（只在设置开启且当前曲目已接入时；否则走现有的“播完再加载下一首”）
- 判定点与条件（全部满足才渐变）：只在自然播放接近结尾时触发（手动下一首、上一首、播放此项、移除当前项都不经过这里）；模式不是单曲循环；按自然前进算出的下一项存在、不是当前项、未标记失败；**下一项的 `releaseId` 与当前不同**；当前曲目时长 ≥ 3 × 渐变时长。
- 时间点：`fadeStart = (当前 trailing_silence_ms ?? 媒体时长) − F`。到 `fadeStart − 15 s` 时生成“计划”：调用 `advance(queue, env, { reason: "natural" })` 得到下一步但**暂不接受**，为下一项发预检与分析（独立 AbortController）。队列、模式、设置变化，暂停或拖动当前曲目时作废计划。
- 到 `fadeStart`：预检已就绪才开始，否则放弃本次渐变。备用元素（`createAudio()`，第一次用时接入同一个上下文）设 `src`，等可定位后定位到下一首的 `leading_silence_ms`（> 10 s 视为异常，不跳），它的 `levelGain` 设为下一首的增益、`fadeGain` 设为 0，`play()`；收到它的 `playing` 后才**切换**：接受计划的队列步、活动元素对调、管线变为新条目的 media、`listen.begin(新条目)`、`raise(track_started)`、`afterQueueChange()`（媒体会话换成新曲目）。
- 曲线：切换时刻 `t0 = ctx.currentTime`，先对两个 `fadeGain` 调 `cancelScheduledValues(t0)`，再分别 `setValueCurveAtTime(cos 曲线, t0, F)`（旧）与 `setValueCurveAtTime(sin 曲线, t0, F)`（新），等功率，各 64 个采样点；由音频线程按采样精度执行，不用定时器改音量。`F` 秒后（或旧元素先 `ended`）卸载旧元素（`pause` + 去掉 `src` + `load()`，`fadeGain` 复位为 1 备用）；这个清理用一个普通定时器即可，迟到也只是多留一个已静音的元素。
- 中途：备用元素 `play()` 被拒或出错 → 丢掉备用元素，旧曲目照常播完，走现有路径；切换后新曲目出错 → 立即卸载旧元素，按现有失败分类处理。切换后用户暂停、拖动、切歌 → 取消调度（新元素 `fadeGain` 设为 1）、立即卸载旧元素，再执行命令。
- 旧元素的事件在切换后一律忽略（每个元素绑定自己的处理函数，先判断是不是活动元素）；`isCurrentMedia()` 改为比较活动元素。
- 聆听计数：切换那一刻旧曲目的访问结束，重叠部分只算新曲目。旧曲目此时已播放 `D − F − 尾部静音` 以上，因“D ≥ 3F”而必然超过门槛 `min(D/2, 240)`，不会少记。
- 现有行为说明：仓库里**没有无缝（gapless）播放**，`prefetcher.ts` 只为下一首派生曲目提前发预检；同专辑相邻曲目仍是现在的“播完再加载”，本任务不改。

**会变的现有测试与合同**
- `player-engine.test.ts`：依赖 `volumechange` 回读音量的用例改为断言 `setVolume` 直接更新快照与存储；新增接入、均衡与渐变用例（见第 8 节）。`AudioLike` 不变，`createAudioContext` 在现有用例里给 null（等同不接入），这些用例行为不变。
- `player-ui.test.tsx`、`player-provider.test.tsx`：队列、信号路径、正在播放页相关断言改为抽屉、弹层、全屏。
- 规范：`player-design-guidelines.md` 的信号路径条目与“正在播放页”描述、`playback-and-personal-state.md`（新存储键、分析查询键、两路音频与 Web Audio 接入规则）在收尾时更新。

### 4.7 波形进度条 `WaveformSeek`

- 结构：外层相对定位；底层 `<canvas>` 画波形，顶层是**透明的原生 `<input type="range">`** 覆盖整块——保留 `aria-label="播放进度"`、`aria-valuetext="1:23 / 4:56"`、`min/max/step/disabled` 与 `onChange → engine.seek` 不变，键盘（方向键、PageUp/Down、Home/End）、点击、拖动、触屏全由原生控件提供；焦点环画在外层（`:has(input:focus-visible)`）。
- 绘制：宽度 W 像素时画 `floor(W/3)` 根柱（2 px 柱 + 1 px 间隔），每根取对应区间峰值的最大值，高度按 `peak^0.6` 拉伸（安静段可见），最低 1 px；已播放部分 `--color-accent`，未播放 `neutral-400`（全屏里用局部变量的浅色）；按 `devicePixelRatio` 缩放，`ResizeObserver` 重绘；只订阅“播放比例”（`usePlayerProgressSelector`），约每秒 4 次重画一张 canvas。播放比例 = 位置 / 媒体时长。
- 高度：播放栏 28 px，全屏 40 px。
- 退回：当前曲目没有分析（`pending`/`failed`/读取失败/加载中）时渲染现在的普通 range，样式与布局占位一致，不跳动。

### 4.8 播放设置

| 项 | 取值 |
| --- | --- |
| 存储 | `localStorage["roomusic:playback-settings:v1"]` = `{ "version": 1, "leveling": "auto", "crossfade": false, "crossfadeSeconds": 5 }`，`leveling` 取 `"off" \| "auto" \| "track" \| "album"`；还没上线，直接用这个结构，不迁移旧值。模块 `features/player/engine/playback-settings.ts`（load / save，边界解码，损坏或未知版本用默认值，存储不可用安全降级） |
| 默认 | 均衡“自动”、渐变关、5 秒 |
| 可选时长 | 3、5、8、12 秒 |
| 归属 | 与音量相同：引擎持有并写入存储，快照带 `settings`，命令 `engine.setPlaybackSettings(patch)`；改动立即作用于当前曲目的增益，渐变关闭时作废进行中的计划；接入规则见第 4.6 节 |
| 页面 | 路由 `/settings/playback`（`settingsSections` 增加 `{ label: "播放", icon: AudioLines, adminOnly: false }`，排在“账号”之后）；`app/routes/playback-settings-page.tsx` 组合 `features/player` 的 `PlaybackSettingsForm` |
| 表单 | “音量均衡”：分段选择（单选组）关 / 自动 / 按曲目 / 按专辑；“交叉渐变”：开关（`role="switch"` 的复选框）；“渐变时长”：3 / 5 / 8 / 12 秒的分段选择，渐变关闭时禁用。不写说明句。`volumeControllable` 为假时三组控件都禁用，只加一句：“这台设备不能调节网页音量，这两项不会生效。”（D7） |

## 5. 概览（R7）

| 部分 | 归属 | 内容 |
| --- | --- | --- |
| 统计卡片 `LibraryStats` | `features/catalog`（新 `useCatalogStats`） | 四张卡：专辑（`Disc3`，链 `/albums`）、曲目（`Music2`，不链接）、艺术家（`MicVocal`，链 `/artists`）、作曲家（`Feather`，链 `/artists?role=composer`）。卡片 = 图标 + 数字（`formatCount`）+ 名称，链接卡整张可点；`null` 时显示“—”且不链接 |
| 最近播放 / 最近新增 | 不变（`features/listening`、`features/catalog`） | 主栏上下排列 |
| 扫描 | `features/library` 的 `ScanSummary`（不变） | 侧栏第一块 |
| 音频分析 `AnalysisSummary` | `features/library`（新 `useAnalysisProgress`） | “1,830 / 2,512”+ 细进度条（`<progress aria-label="音频分析进度">`）；`waiting_for_scan` 加“扫描结束后继续”；全部完成写“已完成”；`failed > 0` 加“4 首无法分析”；`unavailable` 只写“未运行” |
| 需要关注 `AttentionSummary` | `features/catalog`（沿用 `useLibraryTotals().attention`） | 数字链到 `/albums?attention=required`；所有成员可见（与现在一致） |
| 音乐目录 / 成员 | `features/library` / `features/members`（不变） | 仅管理员 |

- 原 `LibrarySummary` 拆成 `LibraryStats` 与 `AttentionSummary` 后删除。
- 版面（容器查询，按主内容宽度而不是视口）：顶部卡片行在容器 ≥ 640px 时 4 列、否则 2 列 2 行；其下主栏 `minmax(0, 1fr)` + 侧栏 `300px`（DESIGN 的概览侧列上限 320px），容器 ≥ 960px 时并排、否则侧栏排到主栏下面。375 宽时即“卡片 2×2 → 主栏 → 侧栏”，不横向溢出（A9）。
- 空资料库：`albums === 0` 时不显示四张卡，照现在一样一句话（管理员“资料库里还没有专辑。添加音乐目录并扫描后就会出现。”，成员“资料库里还没有专辑。”）；侧栏照常。

## 6. 文案改动点（R1、R4 及连带）

| 位置 | 现在 | 改为 |
| --- | --- | --- |
| `features/governance/change-summary.ts:134`（及 `change-summary.test.ts:90` 一带） | 撤销不能再撤销 | 已完成 |
| `features/player/components/signal-path.tsx` 第 10–11、29–30 行等 | “ALAC 转成 FLAC，位深、采样率与声道不变。”“原样发送，不转码。”“正在取得服务端的交付方式。”“由浏览器解码，可能按输出设备重新采样。”“网页看不到声卡的实际格式。” | 全部删除；弹层每层只有一行取值（第 4.5 节） |
| `player-bar.tsx` 故障文字 | 读取失败，进入正在播放页重试 / 服务器正忙于转换，进入正在播放页重试 | 读取失败 / 服务器正忙（旁边“重试播放”图标按钮） |
| `player-feedback.tsx` 公告 | ……请检查网络后到正在播放页重试。/ ……已暂停，可到正在播放页重试。 | 暂时无法读取音频，已暂停，可以重试。/ 服务器正忙于转换，已暂停，可以重试。 |
| `PlayerFeedback`（原正在播放页顶部）里的 `all_failed` | 队列里的曲目都不能播放，换一张专辑试试。 | 保留，在全屏里显示 |
| 原正在播放页的“队列是空的。去专辑选择一张唱片，开始聆听。”“请选择队列中的一项播放。” | — | 随页面删除（全屏只在有当前曲目时可打开） |

## 7. 兼容与回滚

| 项 | 做法 |
| --- | --- |
| 迁移 | 0033 只新增表，down 只删表；回滚代码后旧版本不认识该表也无影响 |
| 关闭后台分析 | `ROOMUSIC_AUDIO_ANALYSIS=off` 重启即可；接口照常（进度 `unavailable`，结果 `pending`），前端退回普通进度条；均衡若开着，所有曲目按“响度未知”统一 −5 dB（音量一致，只是整体略低） |
| 关闭均衡 / 渐变 | 设置 → 播放：均衡选“关”且渐变关后**重新加载页面**，不再创建 AudioContext，管线与现在完全一致；不重新加载时保持接入、增益归 1（第 4.6 节） |
| Web Audio 出问题 | 页内自动“中断退回”到不接入的元素；用户侧的兜底就是上一行 |
| 前端回滚 | 代码回退即可；`/now-playing` 旧书签在回滚前后都可用（回滚后又是页面） |
| 嵌入产物 | `backend/cmd/roomusic/web` 会与 `main` 上“歌单与专辑页面”任务冲突：合并时不手工解冲突，重新 `npm run build` 生成 |
| 迁移号 | `main` 上并行任务也可能占用 0033：合并时按先合入者保留，本任务重编号为下一个空号（文件名、`embed` 列表与迁移版本测试一起改） |
| API 样本 | 新增样本文件与 `main` 不冲突；若并行任务也改了 `api-samples` 的索引文件，合并时重新运行样本测试生成 |

## 8. 实施拆分与最小测试集

### 8.1 并行边界

合同（第 3 节 JSON、第 1.2 节表结构）由主会话确认后冻结；前端按样本文件开发，不等后端完成。

| 步骤 | 执行者 | 范围 | 依赖 |
| --- | --- | --- | --- |
| B1 | 后端 | `GET /catalog/stats`（`artistListCount` 抽取、样本、注册） | 合同 |
| B2 | 后端 | 迁移 0033、catalog 读写函数、`playback/analysis`、工作者、`OpenAnalysis` 与装配、配置开关、两个分析接口、样本、依赖规则 | 合同 |
| F1 | 前端 | R1 文案；R4 文案删除（先在现有区块上删句）；概览改版（`LibraryStats`、`AnalysisSummary`、`AttentionSummary`、版面） | 合同样本 |
| F2 | 前端 | 全屏播放器、播放栏改造、队列抽屉、信号路径弹层（不含均衡 / 渐变取值）、`ModalDialog.closeOnBackdrop`、`/now-playing` 跳转、侧栏删入口、专辑页 `playerFrom` 修正、相关 E2E | 无 |
| F3 | 前端 | `WaveformSeek`、播放设置（存储、栏目、表单）、引擎均衡与交叉渐变、信号路径浏览器层取值 | F2、合同样本 |

建议顺序：B1 ‖ B2 ‖ F1 ‖ F2 → F3 → 联调（真实后端 + 浏览器）→ 收尾截图（A10）。F1 与 F2 改的文件不重叠（F2 才动 `signal-path.tsx` 的结构，F1 只删句子；若担心冲突，可让 F2 一并做 R4 删句）。

### 8.2 每步最小测试集（只测改动直接影响的部分）

| 步骤 | 测试 | 命令（示例） |
| --- | --- | --- |
| B1 | 统计四个数与对应列表总数相等（含艺人未就绪时为 null）；样本 | `go test -race ./internal/app/ -run 'TestPostgreSQL(CatalogStats|APIResponseSamples)' -count=1` |
| B2 | 单元：窗口峰值 → 波形 / 首尾点、ebur128 摘要解析、专辑增益公式、工作者循环（假分析器：扫描让路、失败不阻塞、取消退出）；`TestFFmpegAnalysis…`（固定 ffmpeg，合成素材：响度误差 ±0.5 LU、开头静音、CUE 区间）；`TestPostgreSQL…`：待分析判定（新曲目 / 快照变化 / 版本变化 / 失败冷却）、两个接口与样本、迁移上下行；依赖规则 | `go test -race ./internal/playback/... ./internal/catalog/ -run 'Test(Analysis|Waveform|Ebur128|AlbumGain)'`；`ROOMUSIC_TEST_FFMPEG=… go test ./internal/playback/analysis -run TestFFmpeg`；`go test ./internal/app/ -run 'Test(PostgreSQLAnalysis|PostgreSQLAPIResponseSamples|PackageDependencyDirections|MigrationVersion)' -count=1` |
| F1 | `change-summary.test.ts`；`decodeCatalogStats`/`decodeAnalysisProgress` 读样本；概览组件测试（四卡、空库、侧栏管理员项） | `npx vitest run src/features/governance/change-summary.test.ts src/app/<概览相关>.test.tsx …` |
| F2 | `player-ui.test.tsx` 改写：封面打开全屏、Esc / 收起 / 返回关闭、抽屉焦点回到队列图标、弹层四层取值且无解释句；`nav-items.test.ts`；`/now-playing` 跳转；E2E 与 axe（1440、375） | `npx vitest run src/features/player/components src/app/shell/nav-items.test.ts …`；`npm run test:e2e -- player.spec.ts auth.spec.ts search-step4.spec.ts` |
| F3 | `playback-settings` 解码（四种 `leveling`）；引擎（假 `AudioContextLike`，可控 `state` 与 `statechange`）：两项都关时不创建上下文、元素不接入；需要时上下文在手势命令里创建并 resume，非 running 时本首不接入原样播放；运行中 `statechange` 变为非 running → 暂停计数并换新元素从原位置继续；增益选择表（关 / 按曲目 / 按专辑含退回 / 自动含随机，未知 −5 dB，正增益原样施加到 `levelGain`）；接入后用户音量写 `masterGain`、`setVolume` 不再依赖回读；渐变（同专辑、单曲循环、手动切歌不渐变；切换时两个 `fadeGain` 收到 `setValueCurveAtTime`；备用元素被拒退回；切换后计数只算新曲目）；`WaveformSeek`（有 / 无分析、键盘仍可用）；设置页（分段选择、iOS 禁用）。后端 B2 的增益公式单元测试覆盖正增益被真峰值限住 | `npx vitest run src/features/player/engine/player-engine.test.ts src/features/player/engine/playback-settings.test.ts src/features/player/components/waveform-seek.test.tsx …` |

不做：全量 Go / Vitest / E2E；为 iOS 或 Android 后台行为写模拟测试（Android 锁屏连播只在收尾时实机听一次）；为不可能的组合（例如同时 `done` 又无波形）写测试；重复的演练与预演。

### 8.3 需要同步更新的 E2E 与测试位置（grep 结果）

| 文件 | 位置 | 改法 |
| --- | --- | --- |
| `frontend/e2e/auth.spec.ts` | 122 | 主导航列表去掉“正在播放” |
| `frontend/e2e/player.spec.ts` | 13（`#queue > ol > li`）、130（`goto("/now-playing")`）、136–180（正在播放布局 / 队列不遮挡）、154、256、313、370、377、424（“队列（N）”链接）、239–262 与 305–319（信号路径“ALAC 转成 FLAC…”）、287（“进入正在播放页重试”）、421–425、444、461（正在播放页歌词布局） | 改为：封面按钮开全屏、队列图标开抽屉（定位器改到抽屉）、圆点开弹层并断言 `ALAC → FLAC`、故障文字与重试按钮、全屏歌词布局（1440 / 375）；`/now-playing` 断言跳到概览 |
| `frontend/e2e/search-step4.spec.ts` | 192 | “队列（1）”从 link 改为 button |
| `frontend/e2e/history.spec.ts` | 136 | 播放栏状态文字“正在播放”不变，确认无需改 |
| `frontend/e2e/real/playback.real.ts` | 76、152、220、459（点侧栏“正在播放”）、129–155（信号路径无损转换文案）、214–220（正在播放页歌词） | 改为打开全屏 / 弹层；断言 `ALAC → FLAC`；歌词在全屏里点行定位 |
| `scripts/playback-e2e.sh` | 无直接引用（运行 `playback.real.ts`） | 不改 |
| `frontend/e2e/support/api-mock.ts` | 接口分发 | 增加三个新接口的桩（读样本）；未登记曲目的分析返回 `pending` 样本 |
| 单元测试 | `features/player/components/player-ui.test.tsx`、`app/shell/nav-items.test.ts`、`app/player-personal-actions.test.tsx`、`app/artist-player-metadata-step5.test.tsx`、`features/player/engine/player-engine.test.ts`、`features/player/player-provider.test.tsx`、`features/governance/change-summary.test.ts` | 随对应步骤更新 |

收尾时需要更新的规范（Phase 3）：`player-design-guidelines.md`（信号路径、全屏、抽屉）、`playback-and-personal-state.md`（播放设置键、分析查询键、两路音频与 Web Audio 接入规则）、`frontend/DESIGN-components.md`（概览行与“正在播放”描述）、`frontend/DESIGN.md` 4 节导航列表、新增 `backend/audio-analysis-contracts.md` 并登记到后端索引。

## 9. 需主会话裁决

| # | 问题 | 推荐 | 理由 |
| --- | --- | --- | --- |
| Q1 | ~~均衡只衰减不提升~~ | **已定（用户 2026-11-08）：按 Roon** | −14 LUFS、可提升、按真峰值限幅、四种模式、未知 −5 dB，用 Web Audio 按需接入（第 2.5、4.6 节） |
| Q2 | ~~iPhone / iPad 上两项不可用~~ | **已定（D7）** | 以 `element.volume` 只读判定，不接入 Web Audio；设置页禁用并只写那一句 |
| Q9 | Android 锁屏 / 后台下 Web Audio 是否持续出声没有实机确证 | **已定（主会话）**按第 4.6 节判断默认接入，靠“中断退回”兜底；收尾时实机听一次，若被中断再改为 Android 默认不接入 | 不为未证实的问题提前放弃 Android 上的均衡；退回路径已足以避免无声计数 |
| Q3 | “曲目”数口径：没有曲目列表页可对齐；同一专辑的多个版本里的曲目是否去重 | **已定（D8）**不去重：全部 present 曲目（= 资料库里实际能放的文件数） | 去重需要跨版本认定“同一首”，没有可靠依据；文件数与用户对“我有多少首”的直觉一致 |
| Q4 | CUE 虚拟轨目前不能播放（`not_directly_playable`），分析结果暂时用不上，但 R2 要求按区间分析 | **已定（D9）**照做，但排在最后分析，并计入进度总数 | 符合 R2；排最后不耽误可播曲目；将来 CUE 能播时结果已就绪 |
| Q5 | ~~专辑没分析完时的均衡~~ | **已定（D10，按 Roon 细化）** | 退回曲目增益，曲目也未知用 −5 dB；整张分析完后从下一首起用专辑增益 |
| Q6 | 在全屏里点专辑 / 艺人后按返回 | **已定（D11）**回到全屏播放器（普通 push 导航） | 符合“返回刚才的位置”的直觉，也不需要改 `ArtistNames` 等通用链接 |
| Q7 | 信号路径圆点颜色：设计规范不允许擅自引入红绿状态色 | **已定（D12）**无损用金色实心、有损用灰色实心、未知空心；名称与标题写明，不只靠颜色 | 不扩展调色板，仍一眼可辨 |
| Q8 | 音频分析在概览侧栏对所有成员可见；不可用时只写“未运行”不说原因 | **已定（D12）**照此 | 遵守“不写实现细节”；管理员可从日志 `playback.analysis.unavailable` 查原因 |

## 10. 读过但与 PRD 冲突或需要修订的现有文档

- `frontend/DESIGN-components.md` 第 7 节“概览”行写“不推算全库容量、全部曲目数”——与 R7 直接冲突，R7 是更新的用户决定，收尾时改。
- `.trellis/spec/frontend/player-design-guidelines.md`“视觉与布局”要求服务端环节写“原样发送，不转码。”与“ALAC 转成 FLAC，位深、采样率与声道不变。”，“M2b 歌词面板”按正在播放页描述布局——与 R3、R4 冲突，收尾时改为全屏与弹层。
- R6 末条“现有播放行为（无缝预取……）”：代码里没有无缝播放，`prefetcher.ts` 只为下一首派生曲目提前预检。本文按“保持现有预检预取不变”理解，不新增无缝播放。
- `frontend/DESIGN.md` 第 4 节导航列表含“正在播放”、`DESIGN-components.md` 第 6 节写“正在播放页提供完整控制、信号路径与按条目 ID 操作的队列”——随 R3 修订。
- PRD D3“还没分析到的曲目原样播放”与“按 Roon”的“响度未知固定 −5 dB”冲突；D6（只衰减、不用 Web Audio）已被用户推翻。本文按 Roon 写，PRD 由主会话改。
- PRD“现状”说“播放栏有三处链到 `/now-playing`”，另有侧栏导航 `nav-items.ts` 的“正在播放”入口未列出；按“取消正在播放页”一并删除。

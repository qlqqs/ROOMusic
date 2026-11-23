# B3 技术设计

需求与代为决定的事项见 `prd.md`，核实记录见 `research/verification.md`（下文“核实 §n”）。
**第八节是规划审阅后的修订，与前七节冲突时以第八节为准。**

## 一、部署：固定的静态 fpcalc

- **固定**：chromaprint v1.6.2 的官方静态构建，按架构核对 sha256（核实 §1 的表）。版本、地址、两个摘要只在两处出现：
  `deploy/production/Dockerfile` 与新的 `scripts/fpcalc-pinned.sh`；两处由 `scripts/check-production-compose.sh`（或同等的发布检查）核对一致。
- **镜像**：新的构建阶段 `FROM --platform=$BUILDPLATFORM alpine:3.23@… AS fpcalc`，用 `ADD --checksum=sha256:…` 取两种架构的压缩包，
  按 `TARGETARCH`（`amd64`→`x86_64`、`arm64`→`arm64`）解开，`test "$(./fpcalc -version)"` 以 `fpcalc version 1.6.2` 开头；
  运行镜像 `COPY --from=fpcalc … /usr/local/bin/fpcalc`。`compose.prod.yaml` 设 `ROOMUSIC_FPCALC_PATH: /usr/local/bin/fpcalc`。
- **开发与测试**：`scripts/fpcalc-pinned.sh` 照 `ffmpeg-pinned.sh`：下载对应架构的压缩包到 `${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/fpcalc-1.6.2/`，
  核对 sha256 与版本后打印路径；`--url` 只打印固定的地址（Dockerfile 检查复用）。`scripts/test-integration.sh` 导出 `ROOMUSIC_TEST_FPCALC`；
  CI 里取 ffmpeg 的地方同样取 fpcalc。开发环境的 Compose 若有 ffmpeg 的挂载或变量，fpcalc 照同样方式加。
- **配置**：`config.Server.FPCalcPath`（`ROOMUSIC_FPCALC_PATH`，必须是绝对路径；空表示在 PATH 中查找），与 `FFmpegPath` 同样的校验。
- **许可**：镜像里是上游未修改的二进制（LGPL 2.1，内含 LGPL 构建的 FFmpeg）；部署合同写明来源、版本与许可，与 ffmpeg 一节并列。

## 二、指纹计算

### 2.1 位置与依赖

- 新包 `backend/internal/playback/fingerprint`：`Open(ctx, ffmpegPath, fpcalcPath) (*Fingerprinter, error)`，不可用时返回 `*UnavailableError{Reason}`（照 `analysis.Open`）。
  自检：两者的 `-version` 能运行且 fpcalc 是 1.6.x；用内置的 10 秒合成 PCM 跑一次管道，得到非空指纹。
- 取源复用 `playback` 里音频分析的那一套（允许的根、`openScannedSource` 核对大小与修改时间、`sourceAudioPath`、CUE 区间、DSD 转 PCM）。
  需要从 `analysis_worker.go`、`analysis_dsd.go` 抽出可共用的函数时，只做抽取，不改分析的行为（分析的现有测试不改、照常通过）。
- 指纹计算与 `ROOMUSIC_AUDIO_ANALYSIS` 无关：分析关掉时只要 ffmpeg 与 fpcalc 都自检通过，指纹仍可用。

### 2.2 管道

```
ffmpeg -nostdin -hide_banner -loglevel error -protocol_whitelist file,pipe -threads 1 [-ss 起点] -t min(120, 区间长度) -i file:/dev/fd/3 -map 0:a:0 -ac 1 -ar 11025 -f s16le pipe:1
  | fpcalc -format s16le -rate 11025 -channels 1 -length 120 -json -
```

- CUE 虚拟轨：起点与长度来自曲目的 CUE 帧（与分析相同的换算）；DSD：沿用分析的 PCM 流，ffmpeg 以 `pipe:0` 读原始 PCM。
- 单首超时 60 秒；进程用分析同样的低优先级设置。stderr 截尾保留用于日志，不进数据库。
- 送给 AcoustID 的 `duration` 用整首时长（整数秒，曲目的 `DurationSeconds`；CUE 虚拟轨是区间长度），不是解码的 120 秒（核实 §3）。
- 时长 < 10 秒、或 fpcalc 输出空指纹：记 `too_short`，不查询。

### 2.3 缓存（迁移 0063）

```sql
CREATE TABLE track_acoustic_fingerprints (
    track_id            UUID PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    pipeline_version    SMALLINT    NOT NULL CHECK (pipeline_version > 0),
    content_fingerprint TEXT,
    source_size         BIGINT,
    source_modified_at  TIMESTAMPTZ,
    cue_index_frames    INTEGER,
    cue_end_frames      INTEGER,
    status              TEXT NOT NULL CHECK (status IN ('done','failed')),
    fingerprint         TEXT CHECK (fingerprint IS NULL OR octet_length(fingerprint) BETWEEN 1 AND 65536),
    duration_seconds    INTEGER CHECK (duration_seconds IS NULL OR duration_seconds > 0),
    failure_category    TEXT CHECK (failure_category IS NULL OR failure_category IN ('too_short','decode_failed','timeout')),
    computed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT track_acoustic_fingerprints_status_ck CHECK (
        (status='done'   AND fingerprint IS NOT NULL AND duration_seconds IS NOT NULL AND failure_category IS NULL) OR
        (status='failed' AND fingerprint IS NULL AND failure_category IS NOT NULL))
);
```

- **有效**：行的 `pipeline_version` 等于当前版本（1；fpcalc 版本或管道参数变化时加一），且（内容指纹、大小、修改时间、CUE 两帧）与曲目当前的值逐项相同（照 `track_analyses` 的判断）。
  有效的 `done` 直接用；有效的 `failed` 直接当作这一首算不出（音频不变就不重试）。
- **不缓存**：取源失败（根不可用、文件变了、找不到）只让这一首在这次跳过，下次再试；进程被取消（上下文结束）不写。
- 存储与读取放在 `catalog`（与 `track_analysis.go` 并列，`catalog.Store` 的方法），计算在 `playback/fingerprint`，`program` 只看接口：

```go
// program 包
type Fingerprints interface {
    // ok=false 表示这一首这次算不出（原因已缓存或是暂时的）；err 只用于数据库等需要中止本轮的错误。
    Fingerprint(ctx context.Context, track catalog.CurationTrack) (fp sources.Fingerprint, ok bool, err error)
}
```

- `catalog.CurationTrack` 增加计算与缓存需要的字段（内容指纹、来源大小与修改时间等）时，**不进**发行摘要以外的新字段要谨慎：
  内容指纹进摘要是对的（音频变了应当重新判定），但只在指纹可用时才有意义——为避免升级后全库摘要变化而重做，**摘要不加新字段**，
  由“指纹这一步被跳过”的标记（第四节）与缓存的有效性分别负责。

## 三、AcoustID 适配器与密钥

### 3.1 能力与声明

- `sources.FingerprintLookup Capability = "fingerprint_lookup"`；

```go
type Fingerprint struct { Value string; DurationSeconds int }
type FingerprintMatch struct { ID string; Score float64; Recordings []FingerprintRecording }
type FingerprintRecording struct { ID, Title string; LengthMS *int64; Artists []Credit; ReleaseIDs []string }
type FingerprintFinder interface { LookupFingerprint(context.Context, Fingerprint) ([]FingerprintMatch, error) }
```

- 新包 `backend/internal/sources/acoustid`：`Declaration()` = `{ID:"acoustid", Name:"AcoustID", Capabilities:[fingerprint_lookup], DefaultEnabled:true, MinInterval:334ms}`；
  `New(Options)`，`Options` 照 MusicBrainz（地址可注入给受控服务、HTTP 客户端、缓存目录等），外加 `Key func(context.Context) (string, error)`：每次出站前取密钥。
- 请求：`POST https://api.acoustid.org/v2/lookup`，`Content-Type: application/x-www-form-urlencoded`，`Content-Encoding: gzip`，
  表单 `client`、`duration`、`fingerprint`、`meta=recordings releaseids compress`、`format=json`。User-Agent 与其他源相同（不含实例信息）。
- 走 `sources` 现有的请求、限速（每秒 3 次，全实例一个限速器）、重试与缓存；缓存键是（时长、指纹）的哈希，**不含密钥**；有结果缓存 30 天，空结果 7 天。
- 结果映射：只取 `status=ok`；去掉没有录音的结果；录音按 ID 去重（同一录音出现在多个结果里时取最高分）。外部文本按不可信文本处理（与其他源相同）。
- 错误（核实 §3）：HTTP 429 / 码 14、HTTP 503 / 码 13、码 5 → 按现有退避重试，超过上限 `ErrTryLater`；码 3、8 → `ErrInvalidQuery`；
  **码 4、17 → 新的 `sources.ErrInvalidKey`**（不重试、不缓存）；其余 → `ErrTryLater`。

### 3.2 密钥的保存

- 迁移 0063 给 `curation_source_settings` 加列：`key_ciphertext BYTEA`、`key_last_four TEXT NOT NULL DEFAULT ''`（≤ 4 个字符）、
  `key_status TEXT NOT NULL DEFAULT 'unchecked' CHECK (key_status IN ('unchecked','valid','invalid'))`，约束“没有密文时末四位为空、状态为 unchecked”。
  只填密钥而没改过开关时，插入的行 `enabled` 取声明的默认值。
- 加密用现有的 `steward/secret.Store`（同一个实例密钥文件），绑定（`"instance"`、`"curation.source_key." + 源 ID`）。`app` 把同一个 `Store` 交给 `curation.Sources`。
- 声明增加 `RequiresKey bool`（AcoustID 为真）。`curation.Sources` 负责：
  - `SetKey(ctx, id, actorID, key)`：去掉首尾空白，1..128 个可打印 ASCII，否则 `400 invalid_input`；源不需要密钥 `400 key_not_supported`；未知源 `404 not_found`。
    写密文、末四位、`key_status='unchecked'`。
  - `ClearKey(ctx, id, actorID)`：清空三列。
  - 给适配器的 `Key`：读密文并解密；没有密钥返回 `source_disabled` 类的冲突（不应走到，闸门先挡）；解密失败当作 `invalid`（与管家的 `ErrCannotOpen` 一致：需要重新填写）。
  - 适配器返回 `ErrInvalidKey` 时把 `key_status` 置为 `invalid`；第一次成功的响应把 `unchecked` 置为 `valid`（只在值变化时写）。
- 密钥的变更不是治理操作，不进操作记录（与管家的模型密钥相同）；只记一条不含密钥的日志。

### 3.3 源的状态与闸门

- `SourceSettings` 增加 `key`（只有需要密钥的源才有，其余省略，现有三项的输出不变）：`{"configured": bool, "last_four": "abcd", "status": "unchecked|valid|invalid"}`。
- `inactive_reason` 的判断顺序：`curation_disabled` → `enabler_unavailable` → `source_disabled` → `fingerprint_unavailable`（指纹工具不可用）→ `key_missing` → `key_invalid`。
- `curation.Sources` 通过注册时传入的 `Available func() bool`（或等价的字段）知道指纹工具是否可用；`app` 在 `fingerprint.Open` 失败时传“不可用”。
- `Check(ctx, "acoustid")` 在源开关之后依次检查这三条，返回 `409 source_disabled`（消息区分原因）：程序层对 `source_disabled` 的现有处理是跳过这一主题、不写状态，
  这里改为由匹配自己判断（见第四节），不会走到“跳过整个发行”。
- 新的取能力方法 `FingerprintFinder(id)`，包装方式与其他能力相同（每次调用先 `Check`，`WithBackground` 带放行检查）。
- 新的接口：

| 接口 | 说明 |
| --- | --- |
| `PUT /api/v1/admin/curation/sources/{id}/key` | `{key}` → 这个源的 `SourceSettings` |
| `DELETE /api/v1/admin/curation/sources/{id}/key` | → 这个源的 `SourceSettings` |

管理员、同源检查与现有的源接口相同。新错误码：`400 key_not_supported`。

## 四、匹配

### 4.1 接入

- `Match(ctx, e, f, rf, fp)`，`fp` 是 `*FingerprintStep{Finder sources.FingerprintFinder; Prints Fingerprints}`，**AcoustID 现在不可用时为 nil**。
  工作者每处理一个发行前调用 `w.sources.Check(ctx, "acoustid")`（只看结果，不中止）与指纹工具的可用性来决定传不传。
- 结果新增 `MatchResult.Fingerprint string`，写进工作状态的 `result`（字段名 `fingerprint`）：
  - `"used"`：走到了这一步，并且 AcoustID 可用（不论算没算出、查没查到）；
  - `"unavailable"`：走到了这一步，但 `fp == nil`；
  - `"not_reached"`：前面的某一步已经命中（不论结论），或者是信息不足以外的其他提前返回。
- 信息不足的提前返回：`fp != nil` 时不再提前返回（让它走到指纹这一步）；`fp == nil` 时照旧返回，并且 `fingerprint="unavailable"`。
- 这一步的查询返回 `ErrInvalidKey`，或闸门在这一步返回 `source_disabled`（密钥在途中被清除、源被关掉）：这一步当作不可用（`fingerprint="unavailable"`），
  照常往下走到搜索，**不让整个发行失败或跳过**；`ErrTryLater` 仍让整个发行记为稍后再试（与其他源相同）。

### 4.2 专辑方式（第 6 步 `acoustid`，在 `disc_id` 与 `search` 之间）

1. 抽样：按 `sortedTracks` 取至多 4 首（首、⌊(n−1+1)/3⌋、⌊(2(n−1)+1)/3⌋、末，与 `sampleISRCs` 的位置算法相同，但对全部曲目），逐首取指纹。
   算出的少于抽样数的一半（向上取整）→ 这一步不算命中，往下走到搜索。
2. 逐首查询；每首只保留分数 ≥ 0.9 的结果里的录音（去重），得到这一首的录音集合 R_t 与发行集合 U_t（R_t 里所有录音的 `ReleaseIDs` 的并集）。
   任何一首的 R_t 非空即**命中**（命中即定，不再搜索）。
3. 候选 = 各首（R_t 非空的那些）U_t 的交集；为空 → `acoustid_spread` 拿不准。交集多于 3 个 → `too_many`（`fullCandidates` 的现有规则）。
4. 按 ID 读取完整发行（≤ 3），`DecideAlbum(e, "acoustid", …)`。
5. `passes("acoustid")` = `count_equal` ∧ `durations_ok` ∧ 每一首有 R_t 的抽样曲目，它配对到的录音 ∈ R_t。
   配对到的录音 ∉ R_t → 新的相反证据 `acoustid_mismatch`。其余相反证据与第 2–4 步相同（`title_artist_mismatch` 也适用：标题与艺人都可比较却都对不上时拿不准）。

### 4.3 单曲方式（`isrc` 与 `search` 之间）

- 取这一首的指纹；算不出 → 往下走到搜索。查询，保留分数 ≥ 0.9 的录音（去重）；为空 → 不算命中，往下走。
- `DecideSingle(e, "acoustid", rows)`：现有的时长筛选与唯一性规则；另外，标题不是占位且与录音标题松比较不相等 → 拿不准（新代码 `title_mismatch`）。
- 候选里带上分数：`Candidate.Score float64 \`json:"score,omitempty"\``（其他方法的候选不带，现有输出不变）。

### 4.4 重新处理（不加规则版本）

- `program.RuleVersion` 保持 1。
- `needsWork` 对 `release` 主题增加一条：状态是 `no_match` 或 `uncertain`，`result.fingerprint` 是 `"unavailable"` **或者没有这个字段**（B3 之前写的状态），
  并且这一轮 AcoustID 可用 → 需要处理。其余主题与其余状态不变。
- 一轮开始时判断一次 AcoustID 是否可用（`Check` + 指纹工具），整轮共用；轮中途变得可用的，下一轮再处理。
- 结果：没填密钥的实例升级后不重做任何发行；填入密钥后，每个“没对上或拿不准、且没有用过指纹”的发行恰好重做一次（之后它的 `fingerprint` 是 `used` 或 `not_reached`）。
  已对上的发行从来不会因此重做；封面与歌词不受影响。
- 重做时代次照常加一，幂等键不冲突；之前建的待定项由现有的 `UpsertPendingItem` / 对上后的 `resolved` 处理。
- 设置与源的变化已经会触发新一轮（C 的变化标记里有“源设置”）；填写或清除密钥同样更新这个标记。

## 五、前端

- 解码器：`CurationSourceDTO` 增加可选的 `key`；`SourceCapability` 增加 `fingerprint_lookup`；`inactive_reason` 增加三个值；新的端点 `setCurationSourceKey`、`clearCurationSourceKey`。
- 外部源一组的 AcoustID 一行：
  - 说明行：能力“声学指纹”；不生效时写原因：`fingerprint_unavailable`“这台服务器缺少指纹工具（fpcalc）”、`key_missing`“填入 AcoustID 的应用密钥后可用”、`key_invalid`“密钥无效，请重新填写”。
    有密钥且有效或未验证时写“密钥 ····abcd”。
  - 行的菜单：`填写密钥`（没有时）/`更换密钥`、`清除密钥`（有时，清除前确认）；现有的“撤销这个来源”保留。
  - 填写对话框：密码输入框（`autocomplete="off"`），说明“在 AcoustID 网站注册一个应用即可免费获得，仅限非商业使用”，并链接 `https://acoustid.org/new-application`（新窗口）；保存失败就近提示。
  - 照管家设置里模型密钥的现有交互（`features/steward/settings/agent-model.tsx`）选用组件与文案风格。
- 打开总开关的确认框：`sentToSource.acoustid = "由音频算出的声学指纹（不能还原成音频）与时长"`；需要密钥而没有的源在名字后写“（填入密钥后）”。
  确认框原有的“不发送音频、文件路径与这台服务器的信息”保持。
- 进度与批次不变；操作记录里 `method="acoustid"` 的依据文案“声学指纹”。

## 六、文档

| 文档 | 修订 |
| --- | --- |
| `docs/architecture/adr/0005-steward-data-and-egress.md` | 决策记录追加：声学指纹由音频派生、不能还原音频，可以发给 AcoustID（若 X0 已补则只核对） |
| `docs/architecture/adr/0006-auto-curation-and-sources.md` | 第 6 节匹配依据第 5 条改为已接入；部署一节写静态 fpcalc；决策记录追加 B3 的代为决定事项 |
| `.trellis/spec/backend/sources-contracts.md` | AcoustID 适配器一节；能力表；发给外部的数据 |
| `.trellis/spec/backend/curation-contracts.md` | 新的第 16 节：指纹缓存、密钥、源状态、匹配第 6 步、重新处理规则、错误码、测试断言点；第 11、15 节的交叉引用 |
| `.trellis/spec/backend/deployment-environment-contracts.md` | fpcalc 的固定、取用脚本、环境变量、许可 |
| `.trellis/spec/backend/audio-analysis-contracts.md` | 取源函数被指纹共用（若有抽取） |
| 前端 spec（`component-catalog.md`、`governance-components.md` 或自动整理所在处） | 源的密钥行 |
| `docs/architecture/core-1-roadmap.md`、父任务 `implement.md` | B3 完成与交接记录 |

## 七、主要风险

- 真实的 AcoustID 结果里重复录音很多，单曲方式会有较多 `ambiguous`：按“宁可多交给模型”接受，V12 真实试用时统计。
- arm64 的 fpcalc 只能在发布流水线里验证（本机没有 arm64）；Dockerfile 的版本检查会在构建时失败而不是在运行时。
- 指纹计算读整首的前 120 秒，网络盘上会慢；单首 60 秒超时兜住，算不出只是跳过这一步。

## 八、规划审阅后的修订（2026-11-23，以本节为准）

审阅者：规划子代理（Plan，opus）。必须改 M1–M10 全部采纳；建议改 S1–S10 全部采纳（S5 部分调整，见 8.5）；可以不改的 D1–D5 记录在案，D1 写进部署合同。

### 8.1 证据与摘要（M1）

- **`catalog.CurationEvidence` 与 `catalog.CurationTrack` 不得新增导出字段**，曲目查询 `curationTrackSQL` 的标识种类也不加 `acoustid`：摘要是整个证据结构的哈希（`program/digest.go:25`），
  加任何字段都会让全库发行重做一次。第二节 2.3 末尾那段作废。
- 指纹接口改为按曲目取：

```go
// program 包
type FingerprintStatus int
const (
    FingerprintOK        FingerprintStatus = iota // 算出了
    FingerprintPermanent                          // 这一首算不出，音频不变就不必再试（too_short，或缓存里的失败还没到重试时间）
    FingerprintTransient                          // 这一次算不出（根不可用、文件变了、取源失败）
)
type TrackFingerprint struct { Value string; DurationSeconds int }
type Fingerprints interface {
    // err 只用于数据库错误与上下文结束等需要中止本轮的情况。
    Fingerprint(ctx context.Context, trackID string) (TrackFingerprint, FingerprintStatus, error)
}
```

  实现者按曲目 ID 自己读 `tracks` 的根、相对路径、大小、修改时间、内容指纹与 CUE 帧（照 `catalog/track_analysis.go:71-75` 的查询写法，放在 `catalog` 的导出方法里）。

### 8.2 包的划分与依赖（M6、M7、S9）

- `backend/internal/playback/fingerprint`：只有自检与管道（输入是已打开的 `*os.File` 加可选的 CUE 区间，或 PCM 读取器加格式），不导入仓库内其他包。
- `backend/internal/playback`：新的 `Fingerprints` 实现（读缓存 → 取源 → 调 `fingerprint` → 写缓存），复用包内的 `openScannedSource`、`sourceAudioPath`、DSD 的 PCM 流；
  返回 `playback` 自己的类型，由 `app` 包一层适配成 `program.Fingerprints`（`playback` 不导入 `program` 与 `sources`）。
- 缓存表的读写在 `catalog`（与 `track_analysis.go` 并列）。
- `app`：`main.go` 照 `OpenAnalysis` 增加 `OpenFingerprinter`（自检失败只记日志、返回 nil），经新的选项 `app.WithFingerprinter(...)` 传入；
  AcoustID 的受控服务地址经新的选项 `app.WithAcoustID(acoustid.Options)` 传入，**不改 `WithCurationSources` 的签名**。
- 密钥：`app.New` 里先建**唯一一个** `secret.Store`，交给 `registerSteward`（改签名）与 `curation.Sources`；`curation` 定义只有 `Seal`/`Open` 的小接口，不导入 `steward/secret`。
- `app/dependency_rules_test.go` 登记 `sources/acoustid`、`playback/fingerprint` 与 `playback` 新增的导入。

### 8.3 请求基础（M5）

- `sources` 新增一种请求方法：POST，带调用方给的缓存键，每次重试都重新构造 gzip 请求体，**非 200 的响应体也交给调用方分类**；
  `ErrInvalidKey` 不写缓存，也不写失败记忆。缓存的有效期沿用现有规则（有结果的长期，空结果 1 天）；第三节写的“30 天、7 天”作废。
- `sources-contracts.md` 第 4、5 节随之更新。

### 8.4 闸门、可用性与重开（M2、M3、M4、S2、S3）

- **可用性判断**：`Check(ctx, "acoustid")` 返回 `not_found`（没注册，例如现有测试夹具）或 `source_disabled`（含 `fingerprint_unavailable`、`key_missing`、`key_invalid` 三种原因）时算不可用；
  `curation_paused`、`scan_running`、数据库错误照常往上抛，不能吞掉，也不能因此写 `unavailable`。
- **两处判断**：一轮开始时判断一次，只用来决定 `needsWork`（需要改签名，传入主题与本轮是否可用）；
  处理一个**只因为指纹才被重开**的发行之前再判断一次，不可用就跳过，不写状态。普通的待处理发行在 `evaluate` 里照第四节 4.1 判断后传入 `fp`。
- **旧状态的重开条件**（替换 4.4 的“没有这个字段”）：状态是 `no_match` 或 `uncertain`，并且下面两条之一成立：
  - `result.fingerprint == "unavailable"`；
  - 没有 `fingerprint` 字段，`result.method ∈ {"", "search"}`，并且 `conflicts` 里没有 `track_count`。依据：`match_lookup.go:263`、`:309` 是走完前面各步后的空方法，
    `:340` 是信息不足，`:268` 是单曲的 `track_count`，属于没走到。
- **信息不足**：只因为指纹可用才继续往下走的发行，如果指纹这一步没命中，仍记 `no_match` + `insufficient_evidence`，不建待定项（合同 15.2）。
  命中了但拿不准时照常建待定项。
- **密钥状态回写**：只有填写与清除密钥时更新 `updated_at`（它是变化标记的来源，`work_states.go:35`）；`valid`/`invalid` 的回写不改 `updated_at`。
  写 `invalid` 时用 compare-and-set（`WHERE key_ciphertext = 本次请求用的密文`），防止旧请求把刚换上的新密钥标成无效；只有真正出站成功时才把 `unchecked` 改成 `valid`，缓存命中不算。

### 8.5 判定（S1、S4、S5、S6）

- **指纹的三种结果**：抽到的曲目里，算出的少于一半，且其中有因为暂时原因没算出的 → 这个发行记 `try_later`（沿用现有退避）。只有永久原因的 → 这一步不算命中，往下走。
  缓存里的失败照音频分析的规则重试（24 小时后，至多 3 次，`track_analysis.go:80`）；`too_short` 是永久的。缓存表因此增加 `attempts` 列（1..）。
- **专辑方式**：
  - 请求的 `meta` 改为 `recordings releases`（不带 `compress`），拿到每个发行的 `track_count` 与 `medium_count`。
  - 求交集之前，先去掉曲目数或碟数与本地不符的发行（两个值都存在时才比较）。
  - 抽样 ≥ 2 首时，至少 2 首有结果才可能对上；只有 1 首有结果 → 拿不准（新代码 `acoustid_sparse`）。
  - 候选用 AcoustID 返回的发行标题、艺人、日期与国家生成，给 D 用。
  - 其余照 4.2。
- **单曲方式**：时长筛选后剩下的录音（至多 3 个，多于 3 个 → `too_many`）逐个用 MusicBrainz 的 `RecordingByID` 取正式的 ID 与完整数据（AcoustID 的录音 ID 可能是已合并的旧 ID），
  再交给 `DecideSingle`。专辑方式里 `acoustid_mismatch` 也可能因为旧 ID 误报，结果只是拿不准，写进已知限制。
- **接入现有代码**：
  - `Match` 保持现有签名，指纹步骤经可变参数或选项结构传入（`match_rules_test.go` 的现有调用不改）。
  - `DecideAlbum` 保持现有签名，内部再加一个带每首录音集合的版本。
  - `passes` 加 `acoustid` 分支；`compareRelease` 的 `title_artist_mismatch` 方式列表加 `acoustid`。
  - `enrich.go` 的 `methodNames` 加 `"acoustid":"声学指纹"`。

### 8.6 部署（M8、M10、S8、S10）

- 管道照第二节修正后的参数（`file:/dev/fd/3`，可定位）；CUE 最后一首没有结束帧时 `-t 120`，`duration` 取 `DurationSeconds`。
- fpcalc 的构建阶段**不用** `--platform=$BUILDPLATFORM`（要在目标架构上执行 `-version`）。
- 版本一致性检查放在 CI 现有的 `grep` 检查处（`ci.yml:138` 附近）：`scripts/fpcalc-pinned.sh --pins` 打印两种架构的地址与 sha256，逐行 `grep` Dockerfile。
- 开发机的 `uname -m`：`x86_64` 不变，`aarch64` 换成 `arm64`。`dev.sh` 若导出 `ROOMUSIC_FFMPEG_PATH`，同样导出 `ROOMUSIC_FPCALC_PATH`。
- 新的集成测试名用 `TestPostgreSQL…` 或 `TestFFmpeg…` 前缀（`test-integration.sh:57` 的默认范围）；`ROOMUSIC_TEST_FPCALC` 为空时这些用例失败，不跳过（与 ffmpeg 相同）。
- 许可（已核实）：上游静态构建用 chromaprint v1.6.2 源码，链接 `acoustid/ffmpeg-build` 的 FFmpeg 8.0 纯音频静态库（`v8.0-1`，配置不带 `--enable-gpl`，不含 GPL 与 nonfree 组件），整体是 LGPL 2.1。
  部署合同写明两处源码地址：`https://github.com/acoustid/chromaprint/tree/v1.6.2`、`https://github.com/acoustid/ffmpeg-build/tree/v8.0-1`。

### 8.7 与 ADR 0006 的差异（M9），以及发出的数据（S10）

- **AcoustID 默认开**（ADR 0006 第 8 节的源表写的是默认关）。理由：U8 的原话是“在管理员填入应用密钥后启用”；默认开再加 `key_missing`，填密钥就是唯一的一步。
  默认关则要先填密钥再开开关，多一步，而且没有密钥时开着的开关本来就不生效。
- **指纹缓存放在数据库**（ADR 0006 第九节与“存放（默认 7）”写的是数据目录、不进备份）。理由：只为走到指纹这一步的曲目按需计算，量小；
  放在数据库可以用外键随曲目删除，有效性判断与音频分析的写法一致；文件形式要另做孤儿清理。
- 两处都在 ADR 0006 追加决策记录，并在原文处标注。
- **发给 AcoustID 的还有应用密钥本身**（`client`，它是这个实例注册的应用的标识）。以下几处都要写“声学指纹、时长，以及你填入的应用密钥”：
  - `sources-contracts.md` 第 9 节；
  - ADR 0005 第 67 行一带；
  - 确认框的 `sentToSource.acoustid`。

### 8.8 界面（S7）与前端的调整

- AcoustID 一行不显示“查看与撤销来自它的改动”：经它对上的对应记在 MusicBrainz 名下。说明行写“对上的结果记在 MusicBrainz 名下，可在操作记录里按依据查看”。
- 密钥不用对话框，改用管家模型密钥的行内写法（`features/steward/settings/agent-model.tsx:150-200`）：
  - AcoustID 之下一个从属行“应用密钥”（`nested`）；
  - 没有密钥时是密码输入框加“保存”；
  - 有密钥时显示“已保存，末四位 abcd”和“更换”“清除”两个按钮，清除不另确认（与管家一致）；
  - 说明里链接注册页（新窗口）。
- `key_invalid` 时在从属行上方用 `Notice`（warning）写“AcoustID 说这个密钥无效，请重新填写”。

### 8.9 AcoustID 的两把密钥（实施中发现，2026-11-23）

实现代理发现、主代理核实：AcoustID 于 2026-11-19 公告，2027-01-01 起在 acoustid.org 注册的应用查询时还必须带用户密钥 `user`
（`https://blog.acoustid.org/posts/2026-11-19-user-api-keys-required-for-lookups/`；acoustid.biz 的商业应用不受影响；只有用户密钥、没有应用时只给少量查询）。决定：

- AcoustID 需要两把密钥，都由管理员填写：应用密钥（`application`，在 `https://acoustid.org/new-application` 注册应用得到）与用户密钥（`user`，登录后在 `https://acoustid.org/api-key` 生成）。
  两把都填了才可用，现在就要求，不按日期切换。仓库仍不内置任何密钥（U8）。
- 声明用 `Keys []string` 列出密钥名；存储改为 `curation_source_keys(source, name, ciphertext, last_four, status, updated_by, updated_at)`，清除即删行，加密绑定 `curation.source_key.<源>.<名>`；
  变化标记也算这张表的 `updated_at`。
- 接口 `PUT|DELETE /api/v1/admin/curation/sources/{id}/keys/{name}`；DTO 的 `keys` 是按声明顺序的 `[{name, configured, last_four, status}]`，没有密钥的源省略。
- 表单六个字段：`client`、`user`、`duration`、`fingerprint`、`meta`、`format`。码 4、17 → 应用密钥无效，码 6 → 用户密钥无效，各自只标自己那一把。
- 发给 AcoustID 的数据因此还有用户密钥（它标识管理员在 AcoustID 的账号）；确认框与合同写“以及你填入的应用密钥与用户密钥”。
- 前端：AcoustID 之下两行从属行“应用密钥”“用户密钥”，各自链接到获取它的页面。
- 以后若项目以自己的名义注册 AcoustID 应用，可以改为内置应用密钥、管理员只填用户密钥（上游推荐的形态）；这需要维护者注册，留给用户决定。

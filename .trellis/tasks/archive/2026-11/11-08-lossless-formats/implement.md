# ⑩ 格式扩展：实施计划

设计见 [design.md](./design.md)。M 线第 1 项，在 M 线的 worktree 里实施；⑨ 在本项合入线分支之后开始。
子 agent 不提交 git；迁移无。`B/` = `backend/internal/`，`F/` = `frontend/src/`。

## 全局约定（写进每个派活 prompt）

- 前端文案：不写防御性说明，不加不必要的说明；确实需要的说明用一句日常话。本项前端只改编码名与无损/有损判定，不新增任何说明文字。
- 测试只做必要的：只测改动直接影响的部分，跑下面列出的最小测试集；不跑全量，不为假想场景写测试，不做演练、不重复验证、不堆兜底代码。

## 后端（顺序执行）

1. **APEv2 读取** `B/library/parser/apev2.go`：页脚定位（含 ID3v1 之前）、条目解析、`TRACK`/`DISC` 改名、多值逐个 `setAudioTag`、`Cover Art (Front)` 封面。不改 `tags.go`。
2. **APE 解析** `B/library/parser/ape.go`：可选 ID3v2 跳过、新旧两种头、参数与时长、APEv2 标签、`applyFilenameFallback`。
3. **WavPack 解析** `B/library/parser/wavpack.go`：块头、采样率表与自定义采样率子块、声道信息子块、总样本数未知时遍历、浮点/混合/DSD 编码区分。
4. **AIFF 解析** `B/library/parser/aiff.go`：块遍历、`COMM`（80 位采样率、AIFC 压缩类型 → `Codec`）、`ID3 `/`id3 ` 块调用现有 `parseID3Frames`、`NAME`/`AUTH` 兜底。
5. **分派**：`parser.go` 的 `ParseAudioFile` 增加 `.ape .wv .aif .aiff`。
6. **指纹**：`fingerprint.go` 增加四个种类常量与 `fingerprintFunc` 分派；实现 `apeFingerprint`（MD5 优先、帧数据回退）、`wavpackFingerprint`、`aiffFingerprint`，都用 `hashSection`。
7. **扫描扩展名**：`scanner.go` 两张表按 design 3.4 调整；`cue_path.go` 的 `cueAudioExtensions` 末尾追加四个扩展名。
8. **派生核心** `B/playback/derivation`：`Input` 类型与 `ConvertJob.Input`；`convertArguments` 按 `Input` 生成输入参数；`SourceVersion.Policy` 进缓存键（ALAC 传原常量）；
   `ProbeFFmpeg` 解析 `-decoders`，`HasDecoder` / `Manager.Supports`；失败记 `playback.derivation.decoders_unavailable`。
9. **播放** `B/playback`：`Format` 增加 `input`、`policy`、`decoder`；`derivedFormatTable` 追加四项；`Formats()` 按 `Supports` 筛选；位深关卡；`sniff.go` 三种容器。
10. **合同同步**：`library-scan-contracts.md`（支持格式、扩展名表、指纹种类、CUE 回退扩展名）、`playback-contracts.md`（格式表派生项、位深关卡进错误矩阵）、
    `playback-derivation-contracts.md`（多格式输入、策略串、解码器探测、位深关卡、测试命令）。中文书写。
11. **接口样本**：`B/app/testdata/api-samples/playback/formats.json` 加新派生项（假派生须声明支持这些解码器）。

## 前端

一个 Opus 子 agent 一次做完（改动很小）：

1. `F/features/player/components/signal-path.tsx`：无损/有损集合与编码短名表（design 3.7）。
2. `F/features/catalog/labels.ts`：`codecLabel` 新增七个取值。
3. 核对 `F/features/player/api/decoders.ts` 对格式表的解码不做枚举限制；格式表样本测试随后端样本更新。
4. `.trellis/spec/frontend/player-design-guidelines.md` 信号路径一节：圆点规则的编码集合与服务端一行的写法（`APE → FLAC` 等）。

## 测试（GPT 测试 agent）

### 样本生成（运行时生成到 `t.TempDir()`，不提交）

测试里用 `ROOMUSIC_TEST_FFMPEG` 指向的固定 ffmpeg 现场生成；未设置时这些用例跳过（与现有 `TestFFmpeg…` 一致）。命令（已在 7.1.1 上实测可用）：

```bash
F="$ROOMUSIC_TEST_FFMPEG"
# WavPack 16 / 24 位立体声
$F -hide_banner -loglevel error -f lavfi -i sine=frequency=440:sample_rate=44100:duration=0.3 -ac 2 -c:a wavpack -sample_fmt s16p w16.wv
$F -hide_banner -loglevel error -f lavfi -i sine=frequency=440:sample_rate=48000:duration=0.3 -ac 2 -c:a wavpack -sample_fmt s32p -bits_per_raw_sample 24 w24.wv
# WavPack 浮点（只测解析得到 wavpack_float）
$F -hide_banner -loglevel error -f lavfi -i sine=frequency=440:sample_rate=48000:duration=0.3 -ac 2 -c:a wavpack -sample_fmt fltp wf.wv
# AIFF 24 位（大端）与 AIFF-C sowt 16 位
$F -hide_banner -loglevel error -f lavfi -i sine=frequency=440:sample_rate=48000:duration=0.3 -ac 2 -c:a pcm_s24be a24.aiff
$F -hide_banner -loglevel error -f lavfi -i sine=frequency=440:sample_rate=44100:duration=0.3 -ac 2 -c:a pcm_s16le -f aiff s16.aifc
# 带标签：加 -metadata title=… -metadata artist=… -metadata album=… -metadata track=3（AIFF 写 ID3 块需 -write_id3v2 1）
```

**APE 样本**（2026-11-08 已裁决，见 PRD）：ffmpeg 没有 APE 编码器，提交一个 ≤ 16 KiB 的素材。用 Monkey's Audio 的 `mac`（例如 Alpine community 仓库的 `monkeys-audio` 包，
在固定摘要的容器里执行，实现时核实包名）把一段 0.25 秒 16 位 / 44.1 kHz 立体声 WAV（由固定 ffmpeg 生成）压成
`backend/internal/playback/derivation/testdata/ape-16-44100.ape`，生成命令写进 `scripts/gen-playback-fixtures.sh`（素材上限检查同自检素材，16 KiB），素材随源码提交；
APE 的解析核对与真实派生测试都读它。

旧版 APE（3.80–3.97）与 WavPack 混合/DSD 没有编码器可用，只用 Go 合成头单测。

### 需要补的测试（最小集）

| 位置 | 内容 |
| --- | --- |
| `B/library/parser`（合成头，`parsertest` 新文件 `lossless.go` 放构造函数，避免与其他线在 `parsertest.go` 冲突） | APE 新旧头、ID3v2 前缀；WavPack 标准/自定义采样率、多声道、未知总样本、三种标志；AIFF/AIFC 编码映射；APEv2 键改名、多值、封面、畸形标签被忽略；损坏文件返回错误；四种指纹“改标签不变、改音频变”、APE MD5 全零回退 |
| `B/library/parser` `TestFFmpeg…` | 用运行时生成的 WV/AIFF 核对解析出的采样率、位深、声道、时长与 ffmpeg 生成参数一致 |
| `B/library` 扫描单测 | 扩展名表（`.wv` 支持、`.tak` 进候选） |
| `B/playback/derivation` | `convertArguments` 按 `Input` 生成；`deriveKey` 的 ALAC 键不变（写死一个已知键值）；`-decoders` 输出解析；`TestFFmpeg…` 真实转换 WV 16/24、AIFF 24 与提交的 APE 素材 PCM 核对通过 |
| `B/playback` | 格式表按解码器筛选；位深关卡（32 位 → `ErrNotDirectlyPlayable`，0 与 16/24 放行）；三种容器核对 |
| `B/playback/analysis` `TestFFmpeg…` | 一首运行时生成的 WV 与一首 AIFF 能完成分析 |
| `B/app` | `TestPostgreSQLAPIResponseSamples`（格式表样本） |
| 前端 Vitest | 信号路径的质量判定与编码名；`codecLabel` 新取值；格式表样本解码 |
| 浏览器 | `./scripts/playback-e2e.sh` 临时媒体目录里加一首运行时生成的 WavPack 24 位，跑一次：派生后能解码、能拖动 |

### 验证命令

```bash
cd backend
go test ./internal/library/parser/ -run 'Test(APE|WavPack|AIFF|APEv2|Fingerprint)' -count=1
ROOMUSIC_TEST_FFMPEG="$(../scripts/ffmpeg-pinned.sh)" go test ./internal/library/parser/ ./internal/playback/derivation/ ./internal/playback/analysis/ -run 'TestFFmpeg' -count=1
go test -race ./internal/playback/... -run 'Test(Format|Derived|Key|Convert|Decoder|Sniff|Container|BitDepth)' -count=1
go test ./internal/library/ -run 'Test(Supported|Candidate)Extension' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/app/ -run 'TestPostgreSQLAPIResponseSamples' -count=1
cd ../frontend && npx vitest run src/features/player/components/signal-path src/features/catalog/labels src/features/player/api/decoders
cd .. && ./scripts/playback-e2e.sh   # 跑前停掉占用测试库的服务（见环境记忆）
```

不跑全量：改动局限在解析器、派生、格式表与两个前端组件，上面的测试集已覆盖全部改动行为。

### 生产镜像 ffmpeg 的实测方法

镜像与 `scripts/ffmpeg-pinned.sh` 是同一个摘要（`mwader/static-ffmpeg` 7.1.1）。2026-11-08 在本机取出的这个构建上已确认：解码器 `ape`、`wavpack`、`tak`、`pcm_s16be/s24be/s32be/s16le/s24le`、
封装 `ape`、`wv`、`aiff`、`tak` 都在；WavPack 与 AIFF 24 位转 FLAC 后 STREAMINFO 为 24 位、两遍 PCM MD5 相同；32 位 WavPack 被 FLAC 编码器降为 24 位（因此需要位深关卡）。复核命令：

```bash
F="$(scripts/ffmpeg-pinned.sh)"
$F -hide_banner -decoders | grep -E ' (ape|wavpack|pcm_s16be|pcm_s24be|pcm_s16le|pcm_s24le) '
$F -hide_banner -demuxers | grep -E ' (ape|wv|aiff) '
# 部署后在容器里同样执行：docker exec <roomusic 容器> /usr/local/bin/ffmpeg -hide_banner -decoders | grep -E ' (ape|wavpack) '
```

服务启动日志里没有 `playback.derivation.decoders_unavailable`、`GET /api/v1/playback/formats` 含 `ape`/`wavpack`/`aiff` 派生项，即说明生产实例可用。

## 真实库 smoke（只读）

按 [真实库 Smoke 合同](../../../../spec/backend/real-library-smoke-contracts.md) 在本机隔离实例上对 `music/` 跑改动前后各一次（八号机真实库与 `music/` 的文件清单完全相同，不另在八号机跑）：比较 `unsupported_format` 与 `parse_failure` 诊断数、发行数、曲目数；
八号机真实库与本机 `music/` 都没有 APE、WavPack、AIFF（2026-11-08 统计），smoke 只确认已有格式的发行数、曲目数与诊断分布不变；新格式的真实解码由提交的 APE 素材与运行时生成的 WV/AIFF 样本覆盖。

## 共享文件与冲突点

| 文件 | 本项改动 | 也会改的线 |
| --- | --- | --- |
| `B/library/scanner.go` | 两个扩展名函数 | S ①（不支持格式只记诊断的新语义）、④ ⑤（遍历回调） |
| `B/library/parser/parser.go` | `ParseAudioFile` 四个分支 | T ⑥ ⑦ ⑧、O ③（可能改 `applyFilenameFallback` 调用处） |
| `B/library/parser/fingerprint.go` | 种类常量与分派 | ④（指纹复用规则可能动调用方，不动本文件则无冲突） |
| `B/library/parser/cue_path.go` | `cueAudioExtensions` 一行 | S ①（R8 宽松匹配） |
| `B/library/parser/parsertest/` | 新文件 `lossless.go` | 各线可能加构造函数（用新文件避免冲突） |
| `B/playback/*`、`B/playback/derivation/*` | 见上 | 仅 M 线（⑨ 改 `analysis/`） |
| `F/features/player/components/signal-path.tsx` | 编码集合与短名 | ⑨（同线，后续） |
| `F/features/catalog/labels.ts` | `codecLabel` | T ⑦ 可能加角色标签（不同 labeller，冲突小） |
| `.trellis/spec/backend/library-scan-contracts.md` | 支持格式、指纹 | ① ② ③ ④ ⑤ ⑫ 都会改 |

合入顺序：本项依赖 ① 对不支持格式的语义；若 M 线先于 S 线合入，`scanner.go` 的扩展名表在 ① 合入时按 ① 的语义重放，`.ape/.aif/.aiff/.wv` 保持在支持表。

## 风险文件与回滚点

- 风险最高：`ape.go`（旧头）、`wavpack.go`（多声道与自定义采样率）、`derivation/ffmpeg.go`（参数数组）。
- 回滚点 1：只回退步骤 8–9（派生与格式表），新格式仍入库可浏览。
- 回滚点 2：回退整个提交（无迁移），新格式曲目在下次扫描后变 missing。

## 合入前核对

- [ ] 合成头单测与运行时生成样本的 `TestFFmpeg…` 通过；APE 素材已按裁决提交（≤ 16 KiB，生成命令在脚本里）。
- [ ] `alac` 的缓存键与改动前相同（测试写死）。
- [ ] 格式表样本与前端解码一致；信号路径显示 `APE → FLAC`、`WavPack → FLAC`、`AIFF → FLAC`，`wavpack_hybrid` 为有损圆点。
- [ ] 三份后端合同与前端播放器规范已更新。
- [ ] `formatVersions` 已含 `.ape .wv .aif .aiff`（④ 先合入时由本项加，本项先合入时由 ④ 加；design 3.8）。
- [ ] 与 ① 的 `scanner.go`、`cue_path.go` 合并结果复核：扩展名表与诊断语义同时正确。
- [ ] 本机 `music/` 只读 smoke 前后对比已记录。

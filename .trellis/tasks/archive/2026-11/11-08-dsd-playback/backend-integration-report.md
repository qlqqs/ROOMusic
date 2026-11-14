# DSD 后端接入回报

日期：2026-11-09。分支：`dsp/dsd-integration`。未提交 git；未修改 `frontend/`、`.trellis/workspace/` 或迁移。工作树中前端修改属于并行主会话，不属于本回报。

## 1. 状态

后端生产接线已完成，指定四组定向 Go 测试通过，DSD64/128 本机合成来源的分析与 WAV 播放通过。**样本更新已完成**：首次数据库运行中 zones recorder 通过、通用 recorder 的新增扫描失败；将 DSD 样本扫描隔离到自己的根后，按用户批准只补跑一次 `TestPostgreSQLAPIResponseSamples`，通过（31.87s）。样本差异严格为 1 个格式表修改及 3 个 DSD 新增，无其他样本漂移。独立新增测试与发版全套门禁仍由主会话统筹，不能据此宣称全部验收完成。

## 2. 改动文件

### 入库、准入与安全来源

- `backend/internal/library/parser/parser.go`：分派 DSF/DFF。
- `backend/internal/library/parser/version.go`：新增格式缓存版本 `c2.f1`，其他格式不失效。
- `backend/internal/library/parser/fingerprint.go`：接入已有 `dsf-data-v1/dff-dsd-v1`。
- `backend/internal/library/parser/cue_path.go`：CUE 扩展名回退补充 DSD。
- `backend/internal/library/scan_filter.go`：支持表加入 `.dsf/.dff`。
- `backend/internal/library/scan_pipeline.go`：普通与 CUE 父文件的 DST 哨兵映射 `unsupported_format`。
- `backend/internal/library/scan_rules_test.go`：更新既有扩展名 fixture。
- `backend/internal/playback/formats.go`、`sniff.go`、`render_formats.go`：容器、签名、四行格式与内置能力门控。
- `backend/internal/playback/render_source.go`：安全打开后 Probe、布局与有效 PCM CUE 帧。
- `backend/internal/app/renderer.go`、`backend/internal/playback/render/source.go`：`DSDLayout` 传递。
- `backend/internal/app/dependency_rules_test.go`：登记实际新增导入。

### 渲染

- `backend/internal/playback/dsdpcm/pipeline.go`：从既有倍率判定抽出轻量 `OutputRate`，不改算法。
- `backend/internal/playback/render/decoder_dsd.go`：新增父生产者、共享引用与环填充。
- `backend/internal/playback/render/decoder.go`：DSD 分派、有效帧定位、段末限读及独立关闭路径。
- `backend/internal/playback/render/negotiate.go`：有效来源格式与 DXD 规划。
- `backend/internal/playback/render/renderer.go`：DSD 比特率校验分支。
- `backend/internal/playback/render/session.go`、`encode.go`：实际来源/交付的 0/1/2 进程预留。
- `backend/internal/playback/render/transition.go`：相邻 CUE 接续、DSD 后继不预留解码进程。
- `backend/internal/playback/render/signal.go`：`dsd_to_pcm` 步骤与 DSD source 参数。

### 分析、样本与文档

- `backend/internal/playback/analysis/analysis.go`：流式 stdin、`AnalyzePCM`、输入关闭与参数位置。
- `backend/internal/playback/analysis_worker.go`、新增 `analysis_dsd.go`：安全句柄嗅探、CUE PCM 截取、取消生产者。
- `backend/internal/playback/analysis_worker_integration_test.go`：仅补既有分析桩接口。
- `backend/internal/app/api_samples_integration_test.go`、新增 `dsd_api_samples_integration_test.go`：既有通用 recorder 的 DSD 曲目格式素材与录制流程，没有新增 Test 入口。
- `backend/internal/app/zones_api_samples_integration_test.go`：既有 zones recorder 增加 DSD 信号事件样本。
- `backend/internal/app/testdata/api-samples/playback/formats.json`：录制更新的 DSD 格式表。
- `backend/internal/app/testdata/api-samples/releases/get-dsf.json`、`releases/get-dff.json`：真实扫描后生成的 DSD 曲目格式样本。
- `backend/internal/app/testdata/api-samples/playback-zones/events/zone-signal-dsd.json`：已生成的新信号样本。
- `.trellis/spec/backend/dsd-contracts.md`：最终生产合同。
- `.trellis/spec/backend/index.md`：登记合同。
- `.trellis/spec/backend/audio-analysis-contracts.md`：修正旧的“不嗅探容器”描述，链接 DSD 合同。
- 本文件。

## 3. 接口与合同

- `RenderSource.DSDLayout` → app → `render.Track.DSDLayout` 为 `*dsdio.Layout`；文件事实仍为 `codec=dsf/dff`、DSD `sample_rate`、`bit_depth=1`。`bitrate` 沿用仓库已有 kbps 单位；未修改纯包的观察值单位。
- `dsdpcm.OutputRate(int) (int,error)` 统一 44.1/48 kHz 族的 352800/384000 输出率。CUE 两端分别按 `floor(index75*pcmRate/75)` 换算。
- DSD 使用既有默认 +6 dB/filter1；`dsd_to_pcm` 的 `filter` 是整数，当前输出 1；主会话合同中的 0–3 档位和其他参数/quality/compat 不变。DSD128+ 内部降采样不额外生成 sample_rate 步骤。
- 同父、同 Stat 版本、同 Layout、前 end 等于后 start 的相邻 CUE 共用父生产者和滤波历史；read 按段末限读。非连续定位重新打开并前滚；预取关闭只释放引用，最后引用释放才停止父流。
- DSD→WAV 预留 0，DSD→其他交付预留 1；PCM 仍为 WAV 1、其他 2。不会调用 `Reserve(0)` 或 nil process 的 Close/Wait。
- `AnalyzePCM(ctx, io.Reader, analysis.PCMInput)` 接受 f32le，原始 PCM 描述放在 `-i pipe:0` 前。DSD/CUE 已截取输入不再加 `-ss/-t`；取消及提前退出关闭输入并等待进程。
- 复用已合入纯包中的 `parser.ErrUnsupportedEncoding`，不重复声明。没有迁移、用户设置或旧直出能力变化。

## 4. 实际执行的命令和结果

除注明外，Go 命令从 `backend/` 执行。真实渲染前在仓库根执行：

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
```

### 指定定向测试：全部通过

```bash
go test ./internal/playback/dsdio ./internal/playback/dsdpcm ./internal/playback/render ./internal/playback/analysis ./internal/library/parser -count=1
go test ./internal/playback -run 'Test.*(DSD|DFF|DSF|CUE|Render)' -count=1
go test ./internal/library -run 'TestScannerOnlyDiagnosesKnownUnsupportedAudioExtensions|Test.*DSD|Test.*DST' -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

第一组分别为 0.004s、1.026s、6.727s、0.457s、2.368s；后三组分别为 0.006s、0.009s、0.050s。未配置数据库的命令不能当作数据库用例通过的证据。

实现/审查子代理另执行并通过：

```bash
go build ./internal/playback/analysis ./internal/playback
go build ./internal/playback/render/...
go vet ./internal/playback/render/...
```

修改的 Go 文件执行 `gofmt -w` / `gofmt -l`；`git diff --check -- backend .trellis/spec/backend` 通过。

### 首次数据库与样本运行：部分失败（保留历史记录）

```bash
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-dsd \
ROOMUSIC_UPDATE_API_SAMPLES=1 \
ROOMUSIC_TEST_RUN='^(TestPostgreSQLAPIResponseSamples|TestPostgreSQLZonesAPIResponseSamples)$' \
scripts/test-integration.sh
```

- `TestPostgreSQLAPIResponseSamples`：失败（31.51s），末尾新增 DSD 样本扫描没有成功，未到 recorder.finish，通用格式表和两份 DSD 曲目样本未生成。原失败断言未打印扫描状态，不能据此断言是 DSD 解码问题；失败发生在通用样本已制造多种坏来源、目录变更之后的额外全根扫描。
- `TestPostgreSQLZonesAPIResponseSamples`：通过（2.91s），由 recorder 重写其 165 份样本，git 差异仅新增 DSD 信号事件。
- 随后将 DSD 样本扫描改为只启用本样本根，并补充 HTTP 状态/扫描终态诊断；经用户批准补跑成功，见下节。此次仅修 fixture 的全根扫描耦合，未改产品代码；首次日志不足以进一步定位其他根内的具体失败来源。
- 脚本自身枚举包但仅运行正则指定的两个用例；未运行全量测试。测试专用容器 `roomusic-test-dsd-postgres-1` 与卷已由脚本清理。没有操作 `roomusic-postgres-1`。
- 日志：`/tmp/dsd-api-samples.log`。未擅自重复启动数据库运行。

### 用户批准的精确补跑：一次，通过

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-dsd \
ROOMUSIC_UPDATE_API_SAMPLES=1 \
ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' \
scripts/test-integration.sh
```

- `TestPostgreSQLAPIResponseSamples` 通过，31.87s；recorder 生成 211 份样本。没有再跑 zones recorder，也没有失败后的继续试跑。
- 合成 DSF64/DFF128 已由真实扫描入库并经真实 release handler 录制：均为双声道、0.5 秒、bit_depth=1，采样率分别为 2822400/5644800 Hz。
- 日志：`/tmp/dsd-api-samples-retry.log`。隔离测试容器与卷已由脚本清理；未操作 `roomusic-postgres-1`。
- `git diff --name-only -- backend/internal/app/testdata/api-samples` 与 `git ls-files --others --exclude-standard backend/internal/app/testdata/api-samples` 联合检查范围；另用 `python3` 内联断言检查：四个文件集合精确匹配、格式表删除四个新增 DSD 项后与 HEAD 完全相同、曲目与信号参数符合合同，均通过。
- `git diff --check -- backend/internal/app/testdata/api-samples .trellis/tasks/archive/2026-11/11-08-dsd-playback/backend-integration-report.md` 通过。未运行全量测试：本轮仅刷新指定 recorder，遵守批准范围。

### 本机短素材验证：一次，通过

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
(cd backend && go run ./.tmp-dsd-validation)
```

临时程序与生成素材在验证后删除；日志 `/tmp/dsd-local-validation.log` 保留。不是持久化测试，也未引用本机参考稿或参考数据。

## 5. 样本文件列表与 git diff 范围

以 `backend/internal/app/testdata/api-samples/` 为根，全部变更仅有：

| 状态 | 文件 | 范围 |
| --- | --- | --- |
| 修改 | `playback/formats.json` | 只增加 `dsf_id3/dsf`、`dff_diin/dff`、`cue_virtual/dsf`、`cue_virtual/dff` 四项（28 行）；原有项目及顺序均不变 |
| 新增 | `releases/get-dsf.json` | DSF / DSD64 真实扫描后的曲目格式 |
| 新增 | `releases/get-dff.json` | DFF / DSD128 真实扫描后的曲目格式 |
| 新增 | `playback-zones/events/zone-signal-dsd.json` | 上一轮 zones recorder 产生的 DSD 信号步骤 |

`git diff` 的已跟踪样本修改只有 `playback/formats.json`；三个新文件当前未跟踪，已用 `git ls-files --others --exclude-standard` 一并核验。两个 recorder 虽分别重写自己的样本集，其他样本内容与 HEAD 均一致，无非 DSD 新增、删除或改写。样本均由 recorder 产生，没有手写 JSON。

前端合同登记由主会话负责。

## 6. 本机 DSD 验证结果

使用临时生成的 2 秒、双声道、997 Hz 一阶调制测试信号，实际运行 `ParseAudioFile → dsdpcm/AnalyzePCM → render.Renderer`，不是 ffmpeg 原生 DSD decoder。WAV 为 352800 Hz/24 bit；DSD128 用两个相邻 1 秒 CUE 段，后一段无结束索引。

| 来源 | 解析耗时 | 分析倍实时 | 渲染倍实时 | 分析时长 | 实际输出帧 |
| --- | --- | --- | --- | --- | --- |
| DSF / DSD64 / 2822400 Hz | 0.095 ms | 13.42× | 5.46× | 2000 ms | 705600 |
| DFF / DSD128 / 5644800 Hz | 0.125 ms | 1.59× | 1.34× | 2000 ms | 705600 |

两者分析为 −6.1 LUFS，source 元数据 `bit_depth=1`，实际渲染事件含 `dsd_to_pcm`。速度为单次墙钟口径：分析从 AnalyzePCM 调用到返回；渲染从 Renderer.Open 到完整 WAV 读完，包含准备和输出，不包含 ffmpeg 工具自检，不代表稳态算法基准。

**边界**：本机程序覆盖真实解析/分析/播放，但并未通过数据库扫描工作者串起整个端到端流程；数据库扫描已由本轮成功 recorder 单独证明，但两组证据不是同一次扫描→分析→播放的数据库端到端流程。CUE 帧长和成功接续不是“逐样本等于整轨”的独立证明，该断言仍由独立测试 agent 交付。未重新验证 FIR 规格或参考对照，纯包未改系数/生成算法。

## 7. 遗留问题与下一步

1. 通用格式表与 DSD 曲目样本已生成，隔离扫描 fixture 已通过；本次实现与样本更新无待修问题。
2. 设计 §6 的新增独立测试由另一个 agent 编写；特别是共享 CUE 逐样本一致、seek/取消、进程名额及分析/播放一致性，本次不抢写、不冒充完成。
3. 全量 Go/集成测试、Playwright、`scripts/playback-e2e.sh` 均未运行，遵守本轮测试约束；发版前由主会话统一执行。
4. 未提交、未归档任务、未修改 journal；后端与前端整组提交由主会话协调。

# 第二、三批设计与第一批代码的核对（2026-11-09，main e29b65d）

两个只读核对 agent 的结论汇总。行号以 e29b65d 为准，路径相对 `backend/` 或 `frontend/`。前端是 React 19（不是 Vue）：`src/features/player/components/{signal-path.tsx, playback-settings-form.tsx}`、`zones/{model,decoders,api,controller}.ts`。

## A. 共同的过时前提

- `chain.Spec` 只有 `Format/Headroom/OutputBits/Seed`（`dsp/chain/chain.go:10-15`），注释写“格式适配已在上游完成”；`Build` 只装 Headroom 与量化（:41-61）。②③⑤ 都要能接进来：建议 `Spec` 加用户节点列表（如 `User []Node`），由 render 组装，chain 包不导入 peq/卷积。`chain.Wrap` 签名与设计一致（`wrap.go:37-39`），且已实现流头丢弃与流尾补零冲刷（:39-96）。无延迟的节点可以像 Headroom 那样直接实现 `Node`。
- 电平化增益在 `decoder.read` 里施加（`render/decoder.go:201-204`），进链时增益区域必须为空，否则 panic（`chain.go:121-125`）。SRC §3.3/§6.2 与 DSD §4.1 的“按绝对帧号搬运增益区域”在链上用不到。
- `s.pcm` 一个格式同时用于解码、混音、编码：`events.go:68,107,195`、`player.go:84`、`session.go:435,494-507`、`monitor.go:19`、`newEncoder(..., pcm)`。链上变率或变声道后，要拆成“源格式”和“链输出格式”，播放头与时间换算跟着改。
- “有处理”的判定写死在两处：`signal.go:94-96` 的 `ditherStep` 只看 `d.exact` 与 Headroom；`negotiate.go:135/181` 选 16 位也只看 `!Headroom.Enabled`。加入 ②③⑤ 后必须统一成一个判定。
- `dspsettings` 包不存在。设置校验在 `zones/validation.go`（纯函数，无数据库上下文，:23）。
- “坏值按默认处理”不成立：`zones-data-contracts.md:29` 与服务合同第 62 行这么写，但 `store_read.go:90` 解码 headroom 失败直接让 `LoadUser` 报错。
- `set_settings` 用 `==` 判断没变化（`commands.go:382`）；`ZoneSettings`/`PlaybackSettings` 是可比较结构体。PEQ 的切片、SRC 的 `*int` 会编译失败或判错；`settingsOf`（`service.go:215`）与 DTO 会共享切片。需要规范化 JSON 表示，或深比较 + 克隆。
- zones 已有 300 ms 去抖重建（`render_runtime.go:166`），`set_settings` 已复用。每次提交都会开新流（新 seq），前端只能在松手/确认时提交（沿用 `GainSlider`）。
- zones HTTP 只有 `400 invalid_input`、不带字段路径（`playback-zone-contracts.md:237`）；`httpapi/input.go:125` 有设置键白名单，headroom 要求完整对象。
- 依赖规则：`app/dependency_rules_test.go:56-67` 要求每个有 Go 文件的目录登记规则；`zones` 只允许导入 `catalog/uuid/queue`，`render` 不允许导入 `dsp/peq`、`dspsettings`。
- 前端：未知的信号路径 kind 会被整步丢掉（`decoders.ts:57`、`model.ts:69`、`signal-path.tsx:34` 的 `stepIcons`）；新增或改名的 kind 前端要同步。

## B. 采样率转换（SRC）

- 解码侧变率在 `resampleFilter`（`decoder.go:88-89,112-118`）：有 soxr 用 soxr，否则 `filter_size=64`；`-ar`、`-ac` 每次都带（:94）。
- “源超上限时同族减半”只对无损档成立（`negotiate.go:169-174`）；有损档链采样率 = 源采样率（:161），变率全在编码进程。
- 编码进程在 `pcm≠f` 时套 `resampleFilter`（`encode.go:35-36`），Opus 另加 `-ar 48000`（:48），AAC 加 `-ar`（:53）。
- 信号路径 `convert` 由 `rateStep` 产生，可能出两次（解码侧、编码侧，`signal.go:49-50,66-67`）。
- 能力只有每种流格式的 `max_sample_rate *int`（`zones/store.go:19-25`），没有离散采样率集合。浏览器：FLAC/WAV/ALAC 192k、HLS-AAC 48k，Opus/AAC 未声明（`output/capabilities.ts`）。`Supported` 实际只能是 `rate ≤ max`，C2 的“换族”基本不会命中。
- 第一批已做 C2 第 3 步（同族减半）。

## C. 降混

- `concreteFormat` 从不填 `Layout`（`negotiate.go:207`）。解码层无 probe/ffprobe，目录库与解析器不存布局。唯一现成来源是 `sniff_wav.go:118` 的 WAV 掩码（未保留）。要传布局得加字段：`RenderSource` → `app/renderer.go:101` → `render.Track`。
- 有效声道上限 = `min(caps.MaxChannels, 32)`，非 WAV 再与 8 取小（`negotiate.go:157-168`）；`max_channels` 在设备级（`store.go:30`），浏览器声明 2。
- 第一批已用 `-ac` 降混（`decoder.go:94`），信号路径 `signal.go:52-53`。§5.4 去掉 `by` 时前端 `decoders.ts:61` 要求 `by` 必填，要同步改。

## D. DSD

- `parser.ErrUnsupportedEncoding` 不存在；扫描对所有解析错误记 `parse_failure`（`scan_pipeline.go:160-163`）；TAK 的 `unsupported_format` 按扩展名判（`scanner.go:421-422`）。
- `SourceKind` 未定（现有 `aiff_id3`、`ape_apev2` 风格）。
- 播放侧接线：`playback/formats.go:11-19` 的 container、`sniff.go`、`renderFormatTable`（`render_formats.go:17`）、`renderDecoderNames`（:71-94，按 ffmpeg 解码器门控，DSD 要特判）。
- 负载偏移未入库：需在 `OpenForRender` 里重新嗅探（同 `verifyRenderWAV`，`render_source.go:107`），经 `RenderSource` → `app/renderer.go` → `render.Track` 传下去。
- `dsdreader` 若放 `playback/render`，分析包用不到，应放中性包。
- 来源段：`player`/`transition`/`crossfade` 直接读 `decoder.ring`（`transition.go:155-182`、`crossfade.go:28-35`）；最省事是 DSD 填充协程往同一 `byteRing` 写 f32le，复用 `decoder.read`。
- `concreteFormat` 与起播定位（`decoder.go:49`）直接用 `Track.SampleRate`，DSD 要用来源段输出率 352.8k；`sameSourceFormat`（`negotiate.go:141`）比较源采样率/位深/整数性，DSD 与 352.8k PCM 无缝按现代码不成立。
- 会话固定 `Reserve(2)`（`renderer.go:90`），DSD 不起解码进程。
- 分析：`analyzeInput` 有 stdin 路径但参数是 `[]byte`（`analysis.go:114,135`），要改成 `io.Reader` 并在 `-i` 前加 `-f/-ar/-ac`；按 `TrackAnalysisCandidate.SourceKind` 分支。
- 扫描允许 CUE 引用 .dsf/.dff，但 `cue_virtual` 没有 DSD 行（2026-11-09 用户裁决：支持）。
- 前提：扫描 ⑩ 已在 main（`parser/parser.go:74-96`、`fingerprint.go:77`、`mp3.go:50`、`scan_filter.go:41-57`，`.dsf/.dff` 目前在候选表）。

## E. PEQ

- OPRA：仓库里没有。嵌入照抄 textnorm/opencc：`go:embed` + `SOURCE.md` + `LICENSE`（CC BY-SA 4.0 全文随二进制）。二进制约 +1–2 MB，常驻 20–30 MB（估算）。前端产物提交在 `backend/cmd/roomusic/web`（996K），OPRA 标识图片进这里。`opra-update` 联网，放 `backend/cmd` 或 scripts，不要放 internal、不要让 Go 命令写 frontend 目录。
- §8 的 `peq_invalid` 与字段路径：新增错误映射或改用 `invalid_input`。

## F. 卷积

- 延迟后的换轨判定：render 用 `s.written`（输出帧数）判断下一首是否已进链（`events.go:30,93`、`transition.go:198`）。有延迟 L 时下一首已进卷积器却被当成未进链，改队列不触发 `rebuild_needed`，被移除的曲目照样播出。需要“已送入链的帧数”和 `Chain.Latency()`。
- 量子 = 采样率/5（48k 下 9600 帧，`session.go:441`）；抽头 > ~9600 时 block 比量子长。处理器可攒满 block 再算并把多出的延迟计入 `Latency()`（或分区卷积）。
- 开流在播放区锁里（`render_events.go:64`，经 openSession），同步读库 + ffmpeg 解码 + 重采样会卡住播放区；`Open` 只预留 2 个 ffmpeg 进程（`renderer.go:86-93`）；`StartOptions.Source` 只接 `*os.File`。装载要移到会话协程，或上传时解码成浮点存起来；render 需要新的 IR 来源端口（`render.Options` 只有 `Sources`）。
- `ON DELETE SET NULL` 与 zones 内存缓存冲突（`service.go:150-175`）：数据库置空后下次 `UpdateZone` 会写回旧 `ir_id` 撞外键，播放区命令全部失败。删除 IR 必须经过 zones 并清内存引用。
- `ir_id` 归属检查要放 `plan()` 与 RegisterDevice（`devices.go:17`）。
- 上传：后端无 multipart，处理器都用 1 MiB `MaxBytesReader`；前端无 FormData/XHR；服务器只设 `ReadHeaderTimeout`（`main.go:86`）。
- 存 bytea 随 `pg_dump` 备份（`backup/service.go:262`）——2026-11-09 用户裁决：IR 进备份。
- 有损档 192k 源会在 192k 上卷积（SRC 把 ② 搬进链后，有损档链应先降到 48k）。

## G. PCM→DSD

- `dsp/sdm`、`dsp/sdm/internal/gen` 要登记依赖规则。
- `dsp/dsd`、`dsp/internal/refdata`、`dsp/internal/fft` 都不存在，需定归属。
- sdm 输出交错 `[]byte`，`dsd.Converter.Process` 收 `[][]byte`（dsd-playback design.md:83），往返测试要先拆开。

## H. 跨子项冲突热点

- 迁移：最新 0044；SRC、降混、PEQ、卷积都写“00NN”，都改 `playback_zones`。
- zones 设置链路（每个键约 8 处）：`types.go:33` SettingsPatch、`store.go:49` PlaybackSettings、`store.go:89-105` UPDATE 位置参数（到 `$17`）、`store_read.go:70-91`、`ports.go:33` ZoneSettings、`service.go:211-215`、`validation.go`、`commands.go:382`、`httpapi/input.go:125`。
- API 样本：`internal/app/testdata/api-samples` 下 54 个文件含 `settings`。
- render：`chain.go` Spec/Build、`session.go:59-100`、`negotiate.go` concreteFormat、`signal.go`、`decoder.go:88-94`（SRC 删 `-ar`/变率，降混删 `-ac`，同一行）、`encode.go:34-53`、`events.go` 延迟判定。
- 前端：`model.ts`、`decoders.ts`、`api.ts`、`controller.ts:105-115`、`playback-settings-form.tsx`、`signal-path.tsx`、`test/fake-zones.ts`；提交在仓库的 `backend/cmd/roomusic/web` 产物（带哈希，只在最后一次合并时重建）。
- 包依赖：DSD 依赖 SRC 纯包与 PEQ 的 `ButterworthLowPass`；卷积依赖 SRC 的 fft 与 `ResampleAll`；SRC 设置编解码依赖 `dspsettings`。

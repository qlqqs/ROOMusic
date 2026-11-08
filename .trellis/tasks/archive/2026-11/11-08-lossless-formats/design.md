# ⑩ 格式扩展：技术设计

需求见 [prd.md](./prd.md)；父任务的复刻原则与四线划分见 [扫描子系统对标](../11-08-scan-reference-port/prd.md)。
本文 `B/` = `backend/internal/`，`F/` = `frontend/src/`；参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 包 / 文件 | 本项的职责 | 不做 |
| --- | --- | --- |
| `B/library/parser`（新文件 `ape.go`、`wavpack.go`、`aiff.go`、`apev2.go`） | 读文件头取音频参数与标签，产出 `AudioObservation`；三种内容指纹 | 不解码音频、不调用 ffmpeg；不改 `tags.go`、`mp3.go` 的现有函数（T 线会改） |
| `B/library/parser/parser.go`、`fingerprint.go`、`cue_path.go` | 扩展名分派、指纹种类表、CUE 同名回退扩展名 | 不改 CUE 宽松匹配（① R8 负责） |
| `B/library/scanner.go` | 扩展名表：`.ape .wv .aif .aiff` 进支持表；`.tak` 进候选（不支持）表 | 不支持格式的诊断语义归 ① |
| `B/playback/derivation` | `ConvertJob` 增加输入规格；探测 ffmpeg 的解码器清单；按格式的转换策略串 | 不读数据库、不导入内部包（不变） |
| `B/playback`（`formats.go`、`sniff.go`、`derived.go`） | 派生格式表加三种格式；容器核对；派生的位深关卡 | 直出格式表不变 |
| `B/playback/analysis` | 不改：输入靠 ffmpeg 探测，新格式自然可分析（见 3.6） | — |
| `F/features/player/components/signal-path.tsx`、`F/features/catalog/labels.ts` | 新格式的无损/有损判定与显示名 | 不加任何说明文字 |

依赖方向不变：`parser` 仍是叶子包；`derivation` 仍不导入内部包，解码器名与封装名由 `playback` 的格式表以字符串传入。
新解析器都是无状态的纯函数，满足 ⑤ 并发解析的要求。

## 2. 数据流

```
扫描：WalkDir → 扩展名（.ape/.wv/.aif/.aiff）→ ParseAudioFile → 解析器校验魔数并读参数与标签
     → scan_observations → 身份规划（指纹按扩展名分派到新算法）→ Release Graph
CUE：sheet 的 FILE 指向 .ape/.wv/.aiff → ParseAudioFile 取父文件参数（现有流程，无需改）
播放：预检 → findFormat(source_kind, codec) 命中派生项 → 位深关卡 → 来源核对（容器魔数）
     → derivation 任务：重算指纹 → ffmpeg（按格式的 -f/-c:a）→ FLAC + PCM MD5 → STREAMINFO 核对 → 第二遍 MD5 → 发布
分析：工作者按现有候选规则取到新格式曲目 → ffmpeg 探测输入 → 结果入库（无改动）
```

## 3. 接口合同

### 3.1 解析结果（`AudioObservation`）

| 格式 | `SourceKind` | `Codec` | 说明 |
| --- | --- | --- | --- |
| APE `.ape` | `ape_apev2` | `ape` | 位深 8/16/24/32 整数 |
| WavPack `.wv` | `wavpack_apev2` | `wavpack`（整数无损）、`wavpack_hybrid`（混合模式，没有 `.wvc` 时是有损）、`wavpack_float`、`wavpack_dsd` | 后三种按块头标志区分 |
| AIFF `.aif` `.aiff` | `aiff_id3` | `aiff`（AIFF 与 AIFF-C 的 `NONE`/`twos`/`sowt` 整数 PCM）、`aiff_float`（`fl32`/`fl64`，大小写不敏感）、`aifc_<压缩类型小写去空白>`（其他压缩） | 不用 `pcm`：前端 `pcm` 的显示名是“WAV（PCM）” |

比照 WAV 的 `ieee_float`/`wav_extensible` 先例：浮点、DSD 与其他压缩照常入库、显示，只是不在派生表里（不能播放）。
时长、采样率、位深、声道照常填；`Bitrate` 用现有 `estimatedBitrateKbps(文件大小, 时长)`；标签缺失按现有 `applyFilenameFallback`。

**APE**（参照 Monkey's Audio 文件布局与 `libavformat/ape.c`）：

- 文件开头若是 `ID3`，按 ID3v2 头的同步安全大小跳过（≤ 16 MiB，含页脚标志时多 10 字节），记下偏移 `junk`；之后必须是 `MAC `，否则报错。
- 版本 ≥ 3980：52 字节描述块（`descriptorBytes`、`headerBytes`、`seekTableBytes`、`headerDataBytes`、`apeFrameDataBytes` 低/高 32 位、`terminatingDataBytes`、16 字节 MD5）
  加 24 字节头（`compressionLevel`、`formatFlags`、`blocksPerFrame`、`finalFrameBlocks`、`totalFrames`、`bitsPerSample`、`channels`、`sampleRate`）。
- 版本 3800–3979（旧头，3.97 在国内老资源里常见，必须支持）：`version, compressionLevel, formatFlags, channels, sampleRate, headerBytes, terminatingBytes, totalFrames, finalFrameBlocks`；
  位深由标志定（`0x1` → 8，`0x8` → 24，否则 16）；`blocksPerFrame`：版本 ≥ 3950 为 294912，≥ 3900 或（≥ 3800 且压缩级别 4000）为 73728，否则 9216。
  标志 `0x4` 时多 4 字节峰值，`0x10` 时多 4 字节的 seek 元素数；未设 `0x20`（CREATE_WAV_HEADER）时跳过 `headerBytes` 的 WAV 头，然后是 seek 表。
- 版本 < 3800 或 > 9999、声道 0、采样率 0、`totalFrames` 0 → 错误。总块数 = `(totalFrames − 1) × blocksPerFrame + finalFrameBlocks`，时长 = 总块数 / 采样率。
- 标签：文件尾的 APEv2（见 3.2）。开头的 ID3v2 只跳过，不读。

**WavPack**（参照 WavPack 4/5 块格式与 `libavcodec/wavpack.c`）：

- 偏移 0 必须是 `wvpk`；32 字节块头：`ckSize`、`version`（0x402–0x410）、`total_samples_u8`/`total_samples`（40 位）、`block_samples`、`flags`。`ckSize` 须在 24 到 16 MiB 之间。
- 位深 = `((flags & 3) + 1) × 8 − ((flags >> 13) & 0x1f)`；采样率取 `(flags >> 23) & 0xf` 的标准表，15 表示自定义，取第一块的 `ID_SAMPLE_RATE`（0x27）子块；
  声道：第一块有 `ID_CHANNEL_INFO`（0x0d）时用它，否则从第一块（`INITIAL_BLOCK`）到 `FINAL_BLOCK` 累计（`MONO_FLAG` 为 1 声道，否则 2）。
- 总样本数为全 1（未知）时遍历块头，累计带 `INITIAL_BLOCK` 的块的 `block_samples`。
- 标志：`FLOAT_DATA`（0x80）→ `wavpack_float`、位深记 32；`HYBRID_FLAG`（0x8）→ `wavpack_hybrid`；`DSD_FLAG`（0x80000000）→ `wavpack_dsd`。
- 标签：APEv2。

**AIFF**：

- `FORM` + `AIFF`/`AIFC`；块按大端 4+4 字节头遍历，奇数长度补 1 字节，`FORM` 声明的长度不得超过文件；必须有 `COMM` 与 `SSND`。
- `COMM`：声道、`numSampleFrames`、`sampleSize`、80 位扩展精度采样率；AIFC 还有 4 字节压缩类型（决定 `Codec`）。时长 = 帧数 / 采样率。
- 标签：`ID3 `（也认 `id3 `）块里是完整 ID3v2 标签：解析 10 字节头后调用现有 `parseID3Frames(data, obs, major, flags)`，不改 `mp3.go`；
  另读 `NAME` → 标题、`AUTH` → 艺人，只在 ID3 没给出时采用。元数据块各自 ≤ 16 MiB。

### 3.2 APEv2 标签读取（`apev2.go`，APE 与 WavPack 共用）

- 位置：文件末尾 32 字节是 `APETAGEX` 页脚；若末尾是 128 字节 ID3v1（`TAG`），页脚在它之前。版本 1000/2000 都认；标签体 ≤ 16 MiB，条目 ≤ 1024。
- 条目：值长度、标志、以 NUL 结尾的 ASCII 键、值。类型（标志位 1–2）为文本时按 UTF-8，多值以 NUL 分隔，**每个值调用一次现有 `setAudioTag`**（与 Vorbis 注释重复键的语义相同，T 线 ⑥ 的多值规则随之生效）。
- 键先做两处改名再交给 `setAudioTag`：`TRACK` → `TRACKNUMBER`，`DISC` → `DISCNUMBER`（其余 APEv2 常用键 `Title`、`Artist`、`Album Artist`、`Year`、`Genre`、`Composer`、`Lyrics` 等已被识别，键不区分大小写）。
- 二进制条目 `Cover Art (Front)`：值是“文件名 NUL 图像字节”，图像按魔数认 JPEG（`FF D8`）或 PNG（8 字节签名），其余丢弃；大小上限与其他格式的内嵌封面相同。
- 标签体畸形（长度越界、键不是 ASCII）时忽略整个标签，不让解析失败（与 `parseVorbisComments` 的宽容一致）。

### 3.3 内容指纹

写入 `fingerprint.go` 的版本表，`fingerprintFunc` 按扩展名分派（`.ape`、`.wv`、`.aif`、`.aiff`）：

| 种类 | 输入 | 说明 |
| --- | --- | --- |
| `ape-md5-v1` | 描述块的 16 字节 MD5（非全零）加总块数、采样率、声道、位深 | 只读文件头。Monkey's Audio 3.98 起写入，覆盖 WAV 头数据、帧数据与 APE 头，不含尾部标签 |
| `ape-frames-v1` | SHA-256（总块数、采样率、声道、位深 ‖ 帧数据） | MD5 全零或旧版本时使用。帧数据：新版本为 `[帧起点, 帧起点 + apeFrameDataBytes)`；旧版本为 `[seek 表首项 + junk, 文件末尾 − 尾部标签)` |
| `wavpack-blocks-v1` | SHA-256（从第一个 `wvpk` 起连续的全部块字节） | 遇到不是 `wvpk` 的位置即为音频结束（其后是 APEv2/ID3v1） |
| `aiff-ssnd-v1` | SHA-256（`COMM` 的声道、帧数、位深、10 字节采样率、压缩类型 ‖ `SSND` 声音数据） | `SSND` 跳过 8 字节 offset/blockSize 与 offset 指定的填充 |

都与标签无关：改 APEv2、ID3 块、`NAME`/`AUTH` 不改指纹，改音频负载才会改。按块检查 context，不整读文件（沿用 `hashSection`）。
没有音频负载（帧数据长度 0、没有块、`SSND` 为空）的退化文件返回空串。播放侧 `library.ContentFingerprintOf` 按扩展名同样分派，不需要改签名。

### 3.4 扫描扩展名

- `isSupportedAudioExtension` 增加 `.ape .wv .aif .aiff`；`isAudioCandidateExtension` 去掉 `.aif .aiff .ape`，增加 `.tak`（让 TAK 进入“不支持”诊断而不是静默忽略）。
- 仍不支持：`.dsf .dff .wma .mka .aac .tak`，按 ① 的新语义只记诊断。
- `cue_path.go` 的 `cueAudioExtensions` 末尾追加 `.ape .wv .aif .aiff`（EAC 的 CUE 写 `x.wav`、实际压成了 APE 的情况），原有顺序不变。
- 预计数（`scan_progress.go`）调用同一组函数，自动一致。

### 3.5 派生播放

**格式表**（`derivedFormatTable` 追加，只在派生可用且 ffmpeg 有对应解码器时出现）：

| `source_kind` | `codec` | ffmpeg 输入参数 | 转换策略串（参与缓存键） |
| --- | --- | --- | --- |
| `m4a` | `alac` | `-c:a alac -f mov`（不变） | `alac-flac-lossless-v1`（不变，已有缓存继续命中） |
| `ape_apev2` | `ape` | `-c:a ape -f ape` | `ape-flac-lossless-v1` |
| `wavpack_apev2` | `wavpack` | `-c:a wavpack -f wv` | `wavpack-flac-lossless-v1` |
| `wavpack_apev2` | `wavpack_hybrid` | `-c:a wavpack -f wv` | `wavpack-flac-lossless-v1` |
| `aiff_id3` | `aiff` | `-f aiff`（不强制解码器） | `aiff-flac-lossless-v1` |

`GET /api/v1/playback/formats` 的形状不变，只多出这些派生项（`delivery: "derived"`、`mime_type: "audio/flac"`、`codecs: null`）。
`wavpack_hybrid` 解码的是有损部分，交付的 FLAC 与解码结果逐位相同；前端按有损显示（见 3.7）。

**`derivation` 包的改动**：

- `ConvertJob` 增加 `Input Input`：`Input{Demuxer, Decoder string}`。`convertArguments` 把原先写死的 `-c:a alac -f mov` 换成 `[-c:a <Decoder>] -f <Demuxer>`（`Decoder` 为空时不写 `-c:a`）；其余参数、两遍 MD5、`-fs`、bitexact 都不变。
- `SourceVersion` 增加 `Policy string`，`deriveKey` 用它代替常量 `policyALACFLAC`；ALAC 传原串，所以已有缓存条目不失效。
- `ProbeFFmpeg` 在自检之后多跑一次 `ffmpeg -hide_banner -decoders`（5 秒），解析出解码器名集合；`FFmpeg.HasDecoder(name)` 与 `Manager.Supports(decoder)` 供格式表筛选。
  这一步失败不影响 ALAC（自检已证明 alac 可用），只让新格式不进格式表，并记一条 `playback.derivation.decoders_unavailable`（无路径）。
- 自检素材仍只有 ALAC；新格式不各带自检素材（APE 无法用 ffmpeg 生成，见 implement.md）。

**`playback` 的改动**：

- `Format` 增加派生用的 `input`（`derivation.Input`）、`policy`、`decoder`（筛选用；AIFF 的 `decoder` 记 `pcm_s16be`，作为“这个 ffmpeg 有 PCM 解码器”的代表）。`Formats()` 只放入 `derivation.Supports(decoder)` 为真的派生项。
- **位深关卡**：派生格式命中后，扫描记录的位深不是 0 且不是 16 或 24 时返回 `ErrNotDirectlyPlayable`（409 `not_directly_playable`），不启动转换。
  依据：输出核对只接受 16/24（合同第 5 节），ffmpeg 7.1.1 的 FLAC 编码器遇 32 位输入会降成 24 位（实测），两遍 MD5 会拦下，但白白跑一次转换并记 10 分钟失败。
  ALAC 同样受此关卡：20/32 位 ALAC 从 `derivation_failed` 变为 `not_directly_playable`，结果都是不能播放，只是提前判定。
- `sniff.go` 增加三种容器核对：APE（可选 ID3v2 后是 `MAC `，跳过上限与解析器相同）、WavPack（偏移 0 是 `wvpk`）、AIFF（`FORM` + `AIFF`/`AIFC`）。
- 派生任务第 1 步重算指纹沿用 `library.ContentFingerprintOf(ctx, filepath.Ext(path), …)`，按扩展名得到新算法，无需改。

**不放宽到 32 位**（R4 的核对结论）：ffmpeg 7.1.1 的 FLAC 编码器写 32 位需要 `-strict experimental`，Chromium 对 32 位 FLAC 的解码没有验证证据；32 位整数源极少。位深仍只接受 16/24。

### 3.6 音频分析

不改代码。现有命令不强制输入格式，`-i file:/dev/fd/3` 由 ffmpeg 按内容探测（六种现有格式也是这样），APE、WavPack、AIFF 都能被探测到；
浮点、DSD WavPack 与 AIFF 浮点也能分析（分析结果只是测量值，不交付音频）。R5 的“ffmpeg 输入按格式区分”由此改写为“沿用探测”，理由：分析不承担“只转换无损源”的安全职责，强制格式不带来任何收益。
验证：真实 ffmpeg 分析测试加一首 WavPack 与一首 AIFF（运行时生成）。

### 3.7 前端

- `signal-path.tsx`：无损集合加 `ape`、`wavpack`、`aiff`；有损集合加 `wavpack_hybrid`。数据源一行与服务端一行的编码名改用短名表：`ape` → `APE`，`wavpack`/`wavpack_hybrid` → `WavPack`，`aiff` → `AIFF`，其他沿用大写。服务端一行如 `APE → FLAC`、`WavPack → FLAC`。
- `labels.ts` 的 `codecLabel`：`ape: "APE"`、`wavpack: "WavPack"`、`wavpack_hybrid: "WavPack（有损）"`、`wavpack_float: "WavPack（浮点）"`、`wavpack_dsd: "WavPack（DSD）"`、`aiff: "AIFF"`、`aiff_float: "AIFF（浮点）"`；`aifc_*` 原样显示。
- 不加任何解释文字（全局文案约定）：不写“已无损转换”“服务器转成 FLAC”之类。
- 格式表解码器若对 `source_kind`/`codec` 做枚举校验，须放开为字符串（实现时核对 `F/features/player/api/decoders.ts`）。

### 3.8 迁移与版本

- **无迁移**：`source_kind`、`codec` 没有数据库 CHECK；指纹值 ≤ 70 字节，低于 256 字节上限。
- 指纹种类：新增 `ape-md5-v1`、`ape-frames-v1`、`wavpack-blocks-v1`、`aiff-ssnd-v1`，现有种类不变。
- 派生缓存：`keyDomain` 不变；新格式各有策略串；ALAC 键不变。
- 与 ④ 解析器版本号的配合（按 ④ design 3.1、第 8 节定稿）：
  - ④ 按扩展名记版本（`parser/version.go` 的 `formatVersions`）。若 ④ 先合入，本项在 `formatVersions` 加 `.ape`、`.wv`、`.aif`、`.aiff`，各为 1；不在表里的扩展名不缓存。若本项先合入，④ 落地时把四个扩展名一并列入。
  - `cueAudioExtensions` 追加扩展名不需要升版本：④ 每次扫描都重新解析 CUE 文本（只有父音频走缓存），候选签名也包含成员与 CUE sheet，新回退找到的 `.ape` 会让成员变化、候选重写。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 8.1 扩展名映射是唯一权威的“是否尝试”判定（680–703） | 照搬 | 扫描器仍只按扩展名决定是否交给解析器 |
| 8.1 定长后缀比较、零分配（689–703） | 改写 | 用 `strings.ToLower(filepath.Ext)` 加 `switch`：Go 的开销可忽略，且不会把 `x.mp3flac` 误认成 FLAC（原文自认的缺陷） |
| 8.2 扩展名只决定尝试，内容嗅探决定是否成立（705–713） | 照搬 | 每个新解析器校验魔数与结构；播放前 `sniff.go` 再核对容器 |
| 8.2 流参数合法性校验，不在范围内即跳过（711–713） | 改写 | 扫描阶段照常入库并记录参数（与 WAV 浮点先例一致，用户能在库里看到这些曲目）；不能播放的组合在播放预检拒绝（位深关卡、格式表） |
| 8.3 跳过记录与原因码（715–725） | 不在本项 | 由 ① 负责；本项沿用 `unsupported_format`、`parse_failure` |
| 8.5 APE/WavPack/TAK 不在白名单即跳过（742–759） | 改写 | APE、WavPack（及 AIFF）纳入；TAK 不纳入但改为记诊断：Linux 上没有 TAK 编码器，无法得到样本验证解析，按 PRD R3 以样本可得性决定 |
| 9.1 抽取参数后立即释放解码器（762–779） | 改写 | ROOMusic 解析器只读文件头、不创建解码器，`defer file.Close()` 等价且更轻 |
| 9.1 打开路径统一转小写（779） | 改写 | 只对扩展名小写，路径原样（25.1.4 第 5 条同理：小写只用于判定类型） |
| 9.2 外部解码器存在性校验含版本判定（781–796） | 改写 | 启动时一次 `ffmpeg -decoders` 判断有无 `ape`/`wavpack`/PCM 解码器，缺哪个就不放哪个格式进格式表（对应 25.3 的 P1/P2）；不做版本正则：镜像按摘要固定在 7.1.1，这三个解码器多年稳定 |
| 10.1 音频摘要 → 文件结构摘要的回退（905–919） | 改写 | 按格式实现：APE 先用描述块 MD5（相当于原生层摘要），全零或旧版本时退到帧数据哈希，与 FLAC 的 MD5 → 帧数据回退同构 |
| 10.2 三点采样摘要（921–952） | 不采用 | 头块含标签，破坏“身份与标签无关”（父任务“不复刻”已列） |
| 10.3 读取前后比对修改时间（954–972） | 不在本项 | 播放侧已在转换前后核对大小与修改时间；扫描侧的读取竞争处理沿用现状，不在本线改 |
| 10.4 摘要缺失不致命（976–979） | 照搬（现状） | 现有 `fingerprint_failure` 诊断与暂缓写入；失败语义随 ① 调整 |
| 25.1.1 格式枚举含 AIFF/AIFF-C（4769–4787） | 照搬并扩展 | AIFF 的识别依据（`FORM` + `AIFF`/`AIFC` + `COMM`）照搬；枚举里没有 APE、WavPack，按本项需求增加 |
| 25.1.2 三段式：扩展名预筛、内容判定、不命中即 Unknown（4789–4801） | 改写 | 扩展名预筛是硬条件（本地文件都有扩展名，无扩展名或未知扩展名不尝试）；内容判定与“不命中即失败”照搬；不做跨格式的多候选收敛（扩展名与内容不符直接报错，例如 `.ape` 里装的是 FLAC） |
| 25.1.2 “认不出格式”与“读文件出错”是两条错误路径（4801） | 不在本项 | 两者现在都落到 `parse_failure`；是否拆分诊断类别属 ① 的诊断设计 |
| 25.1.3 四级降级 L2/L3（4803–4810） | 改写 | L2（能识别但无解码）对应浮点、DSD、AIFC 压缩：入库但不在格式表 → `not_directly_playable`；L3（参数超能力）对应位深关卡 |
| 25.1.3 L4 外部解码器缺失 → “缺解码器”（4811–4815） | 改写 | ffmpeg 不可用时沿用现有降级（派生项全部不出现，`not_directly_playable`）；有 ffmpeg 但缺某解码器时只缺那一项 |
| 25.1.4 扩展名表只有小写一份（4845、4848） | 照搬 | 扩展名表都是小写字面量，比较前把扩展名小写 |
| 25.2.1 七步串行抽参，任一步失败即中止（4852–4875） | 改写 | 解析器按头部字段顺序校验，任一字段非法即返回错误；没有解码器可创建/销毁 |
| 25.2.2 声道布局位掩码（4877–4911） | 不采用 | 现在只存声道数，不存布局；前端也不显示布局 |
| 25.2.4 模式 A 位深 16/24（4928–4934） | 照搬 | 派生只接受 16/24 位（位深关卡与 STREAMINFO 核对） |
| 25.2.4 声道 1–2/1–8、17 个采样率白名单（4928–4946） | 不采用 | FLAC 能承载 ≤ 8 声道与任意常见采样率，转换后核对参数与 PCM；白名单会把罕见但合法的采样率挡在外面（原文自认“不是尽量播”） |
| 25.2.4 DSD 位深 1 的采样率规则（4941） | 不采用 | DSD 不在本项范围（PRD“不做”） |
| 25.3 解码矩阵：AIFF 内建解码（4950–4960） | 改写 | ROOMusic 没有内建解码器；AIFF、APE、WavPack 都经 ffmpeg 无损转 FLAC，浏览器只解码 FLAC |
| 25.3 外部可执行体查找顺序（4962） | 照搬（现状） | 现有 `ROOMUSIC_FFMPEG_PATH` 或 `PATH` 查找 |
| 25.3 `-decoders` 能力细分（4964–4970） | 照搬 | 见 9.2 行 |
| 25.3 版本正则（4972–4982） | 不采用 | 只影响定点 AAC，与本项无关；镜像版本固定 |
| 25.3 缺失时的降级，不做“老版本尽力而为”（4984） | 照搬 | 缺解码器的格式不进格式表，前端显示不能播放，不尝试转换 |

## 5. 兼容与已有数据

- 已有曲目、指纹、派生缓存都不变；ALAC 的缓存键不变。
- 第一次扫描后，原先记 `unsupported_format` 的 `.ape/.aif/.aiff` 与原先静默忽略的 `.wv` 入库为新曲目（新 UUID）；`.tak` 开始出现在诊断里。
- 引用 `.ape` 的 CUE 原先因父文件解析失败整张跳过，现在虚拟轨入库；CUE 虚拟轨仍不能播放（现有限制，`cue_virtual` 不在格式表），但能浏览、能分析。
- 位深关卡让 20/32 位 ALAC 的错误码从 `derivation_failed` 变为 `not_directly_playable`；前端两种错误都显示为不能播放这首。
- 前端按字符串处理编码与来源类别；未知编码原样显示，旧前端遇到新编码也不会崩。

## 6. 风险与回滚

| 风险 | 对策 |
| --- | --- |
| 旧版 APE（3.80–3.97）头部字段理解有误，参数错或解析失败 | 以 `libavformat/ape.c` 为对照写合成头单测（真实库目前没有 APE）。失败只是该文件记诊断，不影响其它文件 |
| APE 无法用 ffmpeg 生成样本，派生只靠单测与 smoke | 提交一个 ≤ 16 KiB 的 APE 素材（已裁决，生成方法见 implement.md），真实派生测试读它 |
| ffmpeg 的 ape/wavpack 解码器对 24 位输出的 `bits_per_raw_sample` 不对，FLAC 写成 32 位 | WavPack/AIFF 已实测 24 位输出正确（7.1.1）；APE 若出现，按格式在输入后加 `-bits_per_raw_sample` 修正并在测试中锁住 |
| 32 位源在 ffmpeg 里被静默降到 24 位 | 位深关卡提前拒绝；即使漏过，两遍 PCM MD5 也会拦下 |
| 与 ① ③ ⑥ ⑦ ⑧ 在 `parser/`、`scanner.go`、`cue_path.go` 冲突 | 新代码放新文件；共享文件只改分派表与扩展名表各几行；见 implement.md 的共享文件清单 |
| 解析器 bug 让大量文件解析失败 | ① 之后单文件失败不阻断对账；回滚即删除扩展名分派，已入库的新格式曲目在下次扫描后变 missing（不删数据） |

回滚点：后端整体回退本项提交即可（无迁移）。若只想停掉播放，删掉 `derivedFormatTable` 的新增项，曲目仍可浏览。

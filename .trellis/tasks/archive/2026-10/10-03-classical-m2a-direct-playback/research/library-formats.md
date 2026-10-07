# 真实库格式构成与浏览器解码预期（M2a 规划用）

## 统计方式

- 日期：2026-10-03。用途：确定 M2a 的格式验收子矩阵与合成样本，判断仅直出能覆盖多少曲目。
- 方式：临时测试文件经 `go test -overlay` 挂到 `backend/internal/library/parser`（不写入仓库），设置
  `ROOMUSIC_REAL_MUSIC_ROOT` 后只读遍历 `music/`，调用现有 `ParseAudioFile` 与 `ParseCueDocument`；FLAC 只读元数据块头，
  M4A 只读顶层 atom 头。没有计算内容指纹，没有写入任何文件。
- 输出只有聚合计数，不含路径、文件名或标签内容（遵守 `.trellis/spec/guides/real-music-assets.md`）。

## 音频文件

| 容器 / 编码 | 位深 / 采样率 | 文件数 |
| --- | --- | --- |
| FLAC | 16 bit / 44.1 kHz | 164 |
| FLAC | 16 bit / 48 kHz | 13 |
| FLAC | 24 bit / 44.1 kHz | 9 |
| FLAC | 24 bit / 48 kHz | 13 |
| M4A / ALAC | 16 bit / 44.1 kHz | 10 |
| M4A / ALAC | 24 bit / 44.1 kHz | 1 |
| M4A / ALAC | 24 bit / 48 kHz | 73 |
| MP3 | — / 44.1 kHz | 1 |

- 共 284 个音频文件，全部双声道，没有高于 48 kHz 的采样率；没有 AAC、WAV、Ogg Vorbis 或 Opus。
- FLAC 199 个中 198 个带 SEEKTABLE；ALAC 84 个的 `moov` 都在 `mdat` 之前，浏览器无需先取文件尾部就能开始播放和拖动。
- 文件大小：中位数 24 MiB，最大 91 MiB。解析失败 0 个。

## CUE

- 8 个 CUE，全部是“每轨一个文件”的多 FILE 形式，共 132 条 TRACK。
- FILE 声明：70 个指向 `.wav`，全部不存在（扫描跳过对应 TRACK 并记 `cue_reference` 诊断）；62 个指向 `.flac`，其中 59 个存在。
- 指向存在的 FLAC 的声明大多一个文件只对应一条 TRACK。按 `resolveCuePhysicalCoexistence`（`backend/internal/catalog/organizer.go`），
  一个物理文件只被一条 CUE 曲目引用时保留物理曲目并合并 CUE 证据，所以这些曲目会是普通物理 FLAC 曲目。
  **推论：真实库目前大概率没有需要切分的 CUE 虚拟轨**；确切数字以一次完整扫描为准，本次没有扫描。
- 附带观察（留给 M2b 的 CUE 语义）：EAC“间隙附加到前一轨”的多 FILE 写法里，某轨的 `TRACK` 与 `INDEX 00` 位于前一个 FILE、
  `INDEX 01` 位于下一个 FILE；解析器在切换 FILE 时结束当前 TRACK，之后的 `INDEX 01` 没有所属 TRACK 而被丢弃，
  该轨因缺少 `INDEX 01` 被跳过（对应的物理 FLAC 仍按普通文件入库，只是没有合并 CUE 标题等证据）。

## 浏览器解码预期（外部资料，2026-10-03 查阅）

| 编码 | Chrome | Edge | Firefox | Safari | 来源 |
| --- | --- | --- | --- | --- | --- |
| FLAC（原生 FLAC 容器） | 支持 | 支持 | 51+ | 11+ | MDN Web audio codec guide |
| ALAC（MP4 容器） | 不支持 | 不支持 | 不支持 | 支持 | MDN Web audio codec guide；Chromium 音视频格式清单不含 ALAC |

- 资料：<https://developer.mozilla.org/en-US/docs/Web/Media/Guides/Formats/Audio_codecs>、<https://www.chromium.org/audio-video/>。
- 这些只是预期，不是本项目的兼容证据；ADR 0004 §4 要求以实际加载、播放、拖动成功为准，并记录浏览器版本与操作系统。

## 对 M2a 的含义

- 只做原文件直出时，FLAC 与 MP3（200/284，约 70%）预期在四种桌面浏览器都能播放。
- ALAC（84/284，约 30%）预期只有 macOS 上的 Safari 能直出播放；在 Chrome、Edge、Firefox 上要标注不可播放，直到 M2b 提供无损派生。
- 需要精确切分的 CUE 虚拟轨在 M2a 一律标注不可播放（ADR 0004 D1）；按上面的推论，这对现有真实库几乎没有影响。

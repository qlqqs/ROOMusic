# ⑩ 格式扩展

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，M 线第 1 项。参考设计 8.5（能力边界）、25.1–25.3 章（嗅探与解码矩阵）。

## 目标

APE、WavPack、AIFF 文件能入库、显示标签、播放，不再被当成不支持的格式跳过。

## 现状（`B/` = `backend/internal/`）

- 解析只接受 `.flac .mp3 .ogg .opus .wav .m4a`（`B/library/parser/parser.go:67-84`）。扫描器候选表 `.aac .aif .aiff .ape .dff .dsf .m4a .mka .wma` 记 unsupported_format；`.wv`、`.tak` 不在任何表里，静默忽略（`B/library/scanner.go:927-943,351-353`）。
- 内容指纹按格式分别实现（`B/library/parser/fingerprint.go:20-26,71-85`），改算法要升版本号。
- 播放：直出 flac、mp3、wav/pcm、m4a/aac、vorbis、opus（`B/playback/formats.go:45-52`）；派生只有 m4a/alac → flac（`:57-68`）。派生合同（`.trellis/spec/backend/playback-derivation-contracts.md`）：预检接口触发、最多等 20 秒；任务重开源文件核对指纹；两遍 ffmpeg 比对解码 PCM 的 MD5；位深只接受 16/24；缓存键不含路径，默认上限 10 GiB。ffmpeg 输入强制 `-c:a alac -f mov`（`B/playback/derivation/ffmpeg.go:204-217`）。
- 生产镜像 ffmpeg 来自 `mwader/static-ffmpeg`（7.1.1，`deploy/production/Dockerfile:17,25`），2026-11-08 已在同一摘要的本机构建上实测：解码器 `ape`、`wavpack`、`tak`、`pcm_s16be/s24be/s32be/s16le/s24le` 与封装 `ape`、`wv`、`aiff` 都在；WavPack 与 AIFF 24 位转 FLAC 两遍 PCM MD5 相同；32 位会被 FLAC 编码器降成 24 位。
- 前端只有信号路径弹窗与版本对比显示格式（`frontend/src/features/player/components/signal-path.tsx:11-32,90`，`frontend/src/features/catalog/comparison.ts:16-26`）。

## 需求

- **R1 解析**：新增 APE（`.ape`）、WavPack（`.wv`）、AIFF（`.aif`、`.aiff`，含 AIFF-C 的 PCM）的解析：音频参数（采样率、位深、声道、时长）与标签（APE、WavPack 读 APEv2，AIFF 读 ID3 块与 NAME/AUTH 文本块），标签映射与现有容器一致。
- **R2 内容指纹**：每种新格式一个与标签无关的指纹种类（只哈希音频负载与关键参数），写入 `fingerprint.go` 的版本表。
- **R3 扫描**：新格式移入支持表；DSD（`.dsf`、`.dff`）、`.wma`、`.mka`、裸 `.aac` 仍不支持，按 ① 的规则只记诊断。`.tak` 不纳入，进不支持表只记诊断（Linux 上没有 TAK 编码器，拿不到样本）。
- **R4 播放**：新格式走现有的无损派生流程转成 FLAC，沿用两遍 PCM 比对校验与缓存；ffmpeg 输入参数按格式区分（`-f`/`-c:a`，ALAC 缓存键不变）。位深仍只接受 16/24：派生前按扫描记录的位深提前拒绝其他位深（`not_directly_playable`）。
- **R5 分析**：新格式能被后台音频分析处理（沿用 ffmpeg 按内容探测输入，不按格式强制，见已裁决）。
- **R6 前端**：信号路径与版本对比显示新格式名称与“→ FLAC”派生标记。
- **R7 合同同步**：更新 `library-scan-contracts.md`、`playback-contracts.md`、`playback-derivation-contracts.md`。

## 验收

- 单测：三种格式的参数与标签解析、指纹稳定（改标签不变、改音频变）、损坏文件返回错误。
- 用小样本文件跑派生：PCM 比对通过、缓存命中；浏览器能播放。
- 真实库只读 smoke：原来的 unsupported_format 诊断数下降，对应专辑入库。

## 不做

- DSD 转 PCM（不是逐位无损，另行决定）、WMA、多声道下混。

## 已裁决

- 2026-11-08 主会话裁决（设计审阅）：TAK 不纳入，进“不支持”表只记诊断（Linux 上没有 TAK 编码器，拿不到样本验证）。
- 2026-11-08 主会话裁决（设计审阅）：提交一个 ≤ 16 KiB 的 APE 测试素材（Monkey's Audio 在固定摘要容器里生成，命令写进 `scripts/gen-playback-fixtures.sh`），APE 的真实派生测试读它。
- 2026-11-08 主会话裁决（设计审阅）：位深仍只接受 16/24（R4 核对结论：ffmpeg 7.1.1 会把 32 位悄悄降成 24 位）。
- 2026-11-08 主会话裁决（设计审阅）：APE/WavPack + CUE 的整轨镜像能入库、浏览、分析，播放沿用现有限制（CUE 精确分轨仍按路线图另行规划）。依据：八号机真实库 8 份 CUE 都是每轨一个 FILE 的写法，没有整轨镜像，也没有 APE/WavPack/AIFF 文件。
- R5 由“ffmpeg 输入按格式区分”改为“沿用 ffmpeg 内容探测”：分析不交付音频，不承担只转换无损源的职责，强制格式没有收益（design 3.6）。

## 依赖

M 线第 1 项，依赖 ① 对不支持格式的新处理（合入时协调 `scanner.go` 扩展名表）。与 ③ ⑥ ⑦ ⑧ 都改 `parser/`，合入时协调。⑨ 在本项之后。

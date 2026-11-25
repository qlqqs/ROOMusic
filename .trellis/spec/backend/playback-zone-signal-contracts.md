# 播放区协议：信号路径与 Headroom

从[播放区协议 v1](./playback-zone-contracts.md) 第 7 节原样拆出（2026-11-25，原文件超过 24 KB）。

## 7. 信号路径与 Headroom

`Zone.signal` 是正在放的那首的信号路径（播放头到达分段起点时更新）；没有流时为 `null`。样本 events/zone-signal。

```text
Signal = {quality, steps: Step[], clipping?: bool, processing_speed?: number}   // 两个可选键不适用时省略（不是 null）
Step = {kind, quality, lossy: bool, compat: bool, params}
quality ∈ "lossless" > "enhanced" > "high" > "low"（从好到差；Signal.quality 取最差的一步）
```

| `kind` | `params` | 评级 |
| --- | --- | --- |
| `source` | `{codec, sample_rate, channels, bit_depth?}` | 无损源 `lossless`；`mp3/aac/vorbis/opus/wavpack_hybrid` 为 `low`、有损 |
| `sample_rate` | `{from_rate:int, to_rate:int, filter:string}` | 用户转换 `enhanced`；设备/有损约束 `high`、兼容转换；仅链 ② 产生 |
| `downmix` | `{from_channels, to_channels, from_layout, to_layout, by:"roomusic", mode:"matrix"\|"select"}` | `high`、兼容转换；`select` 为纯选择；兼容旧 `by:"ffmpeg"` |
| `leveling` | `{gain_db, basis}` | `enhanced`；电平化不为“关”时出现 |
| `headroom` | `{gain_db}` | `enhanced`；开启时出现 |
| `dither` | `{bits, kind: "tpdf"}` | `enhanced`；降位深时 `high`、兼容转换；逐位直通时不出现 |
| `encode` | 无损 `{codec, sample_rate, bit_depth}`；有损 `{codec, sample_rate, bitrate_kbps}` | 无损 `lossless`；有损 `low`、有损 |

- 交叉渐变是瞬时的，不进信号路径；输出设备一步由客户端按设备名补上。
- **Headroom**：`db` ∈ [−30, 0]、步进 0.1，否则 `400 invalid_input`。开启时在处理链开头乘 `10^(db/20)`，整条流不再逐位、量化时加抖动，无损档按 24 位输出。改动经 `set_settings`（样本 set-settings-headroom）。
- **削波指示**：只在 `clip_indicator` 开时统计（无损档在量化钳位处，有损档在交给编码器前统计 |x| > 1）。有削波立即 `clipping: true`，最后一次削波约 450 ms 后变回 `false`。
- **处理速度**：处理的音频时长 / 解码、处理链与量化花的墙钟时长，一位小数，变化且距上次 ≥ 1 秒才更新。

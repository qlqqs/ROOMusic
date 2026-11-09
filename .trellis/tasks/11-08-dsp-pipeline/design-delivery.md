# 技术设计（二）：编码交付、协商与 iOS 交付小验证

[design.md](./design.md) 的续篇（章节号与它共用、从 §1 起各自编号，引用时写“design-delivery.md §N”）。父设计指[父任务 design.md](../11-08-audio-dsp-reference-port/design.md)；zones 设计指播放区线的定稿设计。

## 1. 编码与交付（P9）

### 1.1 无损档

| 编码 | 做法 | MIME |
| --- | --- | --- |
| FLAC | 编码 ffmpeg：`-f s16le\|s24le -ar R -ac C -i pipe:0 -c:a flac -compression_level 5 -flush_packets 1 -f flac pipe:1` | `audio/flac` |
| WAV | Go 直接写：44 字节头（PCM 格式码 1，RIFF 与 data 的长度都写 `0xFFFFFFFF`，即“长度未知”的流式约定）+ `s16le`/`s24le` | `audio/wav` |

- 量化器（dither）输出打包好的 16/24 位小端整数；24 位按 3 字节打包。ffmpeg 收到 `s24le` 时按 24 位编 FLAC，收到 `s16le` 按 16 位编。
- FLAC 写在不可定位的管道上，STREAMINFO 的总样本数与 MD5 为 0（规范允许，表示未知）。
- FLAC 编码是无损的，所以“逐位一致”只取决于量化器是否直通（dither §3）。

### 1.2 有损档

| 编码 | 做法 | MIME |
| --- | --- | --- |
| Opus | 编码 ffmpeg：`-f f32le -ar R -ac C -i pipe:0 [-af aresample=48000:resampler=soxr] -c:a libopus -b:a 128k -application audio -f ogg -page_duration 100000 -flush_packets 1 pipe:1` | `audio/ogg; codecs="opus"` |
| AAC-LC | 编码 ffmpeg：`… -c:a aac -b:a 256k -f adts pipe:1`；链采样率高于 96 kHz 时先 `aresample` 到同族的 48/44.1 kHz | `audio/aac` |

- 有损档不经过量化器：链输出的浮点直接交给编码器（编码器在自己的域里量化，前面再加抖动没有意义）。交接前统计削波（design.md §8.2）。
- 码率固定：Opus 128 kbps（立体声接近透明），AAC 256 kbps（ffmpeg 自带的 AAC 编码器需要更高码率才与 Opus 相当）。第一批不做码率选项。
- `aresample` 在编码进程里，整条流一个进程，跨曲目连续（父设计 §6）。soxr 不可用时用 ffmpeg 默认重采样器。
- Ogg 页长 100 ms：浏览器能尽早拿到第一页开始播放；ADTS 本身是逐帧的。

### 1.3 HTTP 交付（zones 合同 §4.4 的实现细节）

- `200`；`Content-Type` 为流的 MIME；**不带 `Content-Length`**（HTTP/1.1 分块传输，HTTP/2 按帧）；`Cache-Control: no-store`；`Accept-Ranges: none`；`X-Content-Type-Options: nosniff`；`X-Accel-Buffering: no`（与事件流同样的反代要求）。
- 请求带 `Range` 时忽略，照常回 `200` 完整流（HTTP 允许）。是否要改成 §1.4 的做法由 iOS 验证决定。
- zones/httpapi 以 32 KB 缓冲从 `Body` 拷贝，每次读到数据就写出并 `Flush`。客户端断开时 handler 返回，不关闭会话（会话生命周期归 zones）。
- 同一条流的第二个连接：建议回 `410 stream_gone`（父设计 §8 第 5 条）。

### 1.4 备用：头部重放与伪长度（只在 iOS 验证证明需要时启用）

- **头部重放**：会话保留 `Body` 最开头的 64 KB。第二个连接的 `Range` 从 0 开始（或没有 `Range`），且前一个连接读走的不超过 64 KB 时，先重放头部再接着读实时输出。用于“先发 `bytes=0-1` 探测、再正式请求”的客户端。
- **伪长度**：对 `Range: bytes=0-1` 回 `206`、`Content-Range: bytes 0-1/<L>`；对 `bytes=0-` 回 `206`、`Content-Range: bytes 0-<L−1>/<L>` 并开始实时输出。`L` 取一个足够大的上界（按“流格式的最高码率 × 6 小时”算）。流提前结束时连接正常关闭，客户端按结束处理。
- 两者都只是 HTTP 层的处理，`Stream` 的形状与输出端的对齐逻辑都不变。

### 1.5 码率参考

FLAC 44.1/16 约 0.7–1.0 Mbps；开了电平化后按 24 位输出（§2.2）约 1.3–1.6 Mbps；WAV 44.1/16 为 1.41 Mbps；Opus 128 kbps；AAC 256 kbps。

## 2. 协商（`Negotiate`）与具体格式

### 2.1 `Negotiate(caps, quality)` 选编码

1. 把 `caps.stream_formats`（按输出端偏好排序）过滤成服务端能产出的：`flac`（ffmpeg 有 flac 编码器）、`wav`（总是）、`opus`（有 libopus）、`aac`（有 aac 编码器）。
2. `quality == "lossless"`：取第一个 `lossless == true` 的；没有就取第一个有损的（信号路径会显示有损）。`quality == "lossy"`：取第一个有损的；没有就取第一个无损的。
3. 一个都没有：返回 `ErrNoStreamFormat`（zones 记 `fault: no_stream_format`）。
4. 返回 `StreamFormat{Codec, MimeType, Quality}`，并在会话内部记下这一项的 `max_sample_rate`、`max_bit_depth` 与 `caps.max_channels`，供 §2.2 使用。

`Negotiate` 不看源，所以同一台设备、同一档位总是得到同一种编码；需要按源调整的只有采样率、位深与声道（§2.2）。

### 2.2 `Open` 确定具体格式（按首曲的源格式）

| 项 | 规则 |
| --- | --- |
| 声道 | `min(源声道, max_channels)`；源更多时解码 ffmpeg 降混（父设计 §6） |
| 链采样率 | 源采样率；超过所选编码的 `max_sample_rate`（无损）时按同族减半直到不超过（解码 ffmpeg 转换，父设计 §6） |
| 输出采样率 | 无损档同链采样率；Opus 48 kHz；AAC 不超过 96 kHz（超过则同族减半） |
| 输出位深（只无损档） | **链透明**（整数源，电平化关、Headroom 关）时取源位深：≤16 → 16，17–24 → 24，>24 → 24；否则取 24（不超过 `max_bit_depth`，没写时按 24） |

- “链透明”时保留源位深，这样逐位一致、码率也不变大。开了处理就按 24 位输出：抖动噪声落在 −141 dBFS 而不是 16 位的 −93 dBFS，代价是 16 位源的码率增加约一半（局域网无所谓，在外有省流量档）。
- 一条流内格式不变。下一首的源格式（采样率、位深、声道、浮点与否）与首曲相同才能接在同一条流里（gapless-crossfade G1），所以上面的推导对整条流都成立。

## 3. 浏览器的能力声明（zones 合同 §8 的“候选”）

前端 output 模块按下表顺序探测（`canPlayType` 返回非空即算能解），能解的按顺序放进 `stream_formats`：

| 顺序 | `codec` | `mime_type` | `lossless` | `max_sample_rate` | `max_bit_depth` |
| --- | --- | --- | --- | --- | --- |
| 1 | `flac` | `audio/flac` | true | 192000 | 24 |
| 2 | `wav` | `audio/wav` | true | 192000 | 24 |
| 3 | `opus` | `audio/ogg; codecs="opus"` | false | — | — |
| 4 | `aac` | `audio/aac` | false | — | — |

- `max_channels: 2`、`dsd: "none"`、`exclusive: false`（zones 合同 §8）。
- 192 kHz 是先取的稳妥上限：桌面浏览器多半更高，但浏览器之后还要经过系统混音，更高的采样率没有收益。
- **iOS 的取值等 §4 的结果再定**（例如 FLAC 不行就去掉 1，把 WAV 放第一；Opus 不行就去掉 3）。结果写回本节，由 zones F2 照表实现。

## 4. iOS 交付小验证

目的：在写渲染器之前，用真机确认 iPhone Safari 能否用“一条没有长度的长 HTTP 响应”放音乐，锁屏后能否一直放下去，并据此定交付方式（zones design-protocol.md §3.6 列出的风险）。与 zones B1 并行，最先做。

### 4.1 要验证的各项

| 编号 | 项 | 怎么判断 |
| --- | --- | --- |
| V1 | `<audio>` 能播没有长度的长 FLAC 响应 | 有声音；页面日志里 `playing` |
| V2 | 同上，WAV（长度写 `0xFFFFFFFF`） | 同上 |
| V3 | 锁屏后连续播放，跨过多段（10 分钟、20 段） | 用户听；服务端日志里流被持续读取、没有中断重连 |
| V4 | 锁屏时 `timeupdate` 仍触发，页面发出的回报能到达服务器 | 服务端收到 `visibilityState: hidden` 的日志，间隔 ≤10 秒 |
| V5 | 锁屏时页面能按播放位置换 Media Session 标题 | 用户看锁屏标题每 30 秒变一次 |
| V6a | 锁屏中，页面在媒体事件里给同一元素换新流并 `play()`（不经手势） | 第 3 分钟音色变化后继续出声 |
| V6b | 锁屏控件暂停超过 5 秒后，从锁屏控件恢复，处理函数里换新流并 `play()` | 恢复后有声音 |
| V7 | `currentTime` 单调，与墙钟一致 | 页面日志的 `currentTime` 与时间戳对照，误差 < 0.5 秒/10 分钟、不回退 |
| V8 | Safari 是否发 `Range`，对“200、无长度、`Accept-Ranges: none`”的反应 | 服务端记录每个请求的 `Range` 头与结果 |
| V9 | 有损档：AAC（ADTS）与 Opus（Ogg）能否播 | 有声音；`canPlayType` 结果 |
| V10 | 96/192 kHz、24 bit 的 FLAC 能否播 | 有声音 |
| V11 | 备用：HLS（fMP4 ALAC 片段）能播且锁屏连续 | 只在 V1、V2 都失败时需要 |

另外页面加载时记下：Safari 版本、`isSecureContext`、`navigator.mediaSession` 是否存在、上表各 MIME 的 `canPlayType`、`ManagedMediaSource`/`MediaSource` 是否存在。

### 4.2 探针程序（临时，不进集成分支）

- `backend/cmd/ios-probe/`：一个 `main.go` 加嵌入的 `index.html`，只依赖标准库与 ffmpeg；不连数据库、不鉴权，只在局域网用。放在一次性分支 `ios-probe` 上，结论记录后删除分支。
- 运行：在八号机上 `./ios-probe -addr :18090 -ffmpeg <ffmpeg 路径>`（ffmpeg 用 `scripts/ffmpeg-pinned.sh` 取出的静态版本一并拷过去）。
- **流** `GET /stream/{case}`：ffmpeg 用 `aevalsrc` 实时生成正弦，每 30 秒换一次音高（模拟“换曲”）；换流后的新流用另一种音色（两个音的和弦），让人听得出“换过流了”。编码与 HTTP 头完全照 §1.1–§1.3（`200`、无长度、`Accept-Ranges: none`、逐块 `Flush`），生成速度由客户端读取的背压决定，与真实渲染一致。
  加 `?range=fake` 时按 §1.4 的伪长度处理（只在第一轮结果需要时让用户重测一次）。
- **HLS** `GET /hls/index.m3u8`：启动时用 ffmpeg 预先生成 2 分钟的 ALAC fMP4 片段（4 秒一片，VOD 播放列表）放在临时目录。
- **日志** `POST /log`、`POST /result`：页面用 `fetch(…, {keepalive: true})` 上报事件（`play`、`playing`、`waiting`、`stalled`、`pause`、`ended`、`error`，以及每 5 秒一次 `timeupdate`），带 `currentTime`、`buffered` 末端、`readyState`、`visibilityState`、页面时间戳；
  用户每项的结论也发到这里。服务端把它们与每个流请求的记录（请求头、发出的字节与帧数、开始与结束时间、怎么结束的）写进同一个 JSONL 文件，并打印到终端。
- **页面**：每项一个卡片（标题、一句提示、「开始」「正常」「有问题」按钮与一个可选的备注框），设好 Media Session 的标题与播放/暂停处理函数；顶部显示当前 `currentTime` 与状态。没有别的说明文字。

### 4.3 各项的用例

| 用例 | 内容 | 覆盖 |
| --- | --- | --- |
| A | FLAC 44.1/16 长流 10 分钟，锁屏。第 3 分钟页面在 `timeupdate` 里换到新流（音色变化）。标题每 30 秒变“第 N 段” | V1 V3 V4 V5 V6a V7 V8 |
| B | FLAC 长流，锁屏后用锁屏控件暂停 10 秒再恢复；处理函数换新流从原位置 `play()` | V6b |
| C | WAV 44.1/16 长流 2 分钟，锁屏 | V2 V3 |
| D | AAC 30 秒，放完自动接 Opus 30 秒，不锁屏 | V9 |
| E | FLAC 96/24 15 秒，放完自动接 192/24 15 秒，不锁屏 | V10 |
| F | HLS ALAC 2 分钟，锁屏 | V11 |

### 4.4 用户的步骤（真机，约 20 分钟）

1. iPhone 连家里 Wi-Fi，用 Safari 打开 `http://192.168.50.112:18090`。
2. 页面上有 A–F 六项，从上往下做：点「开始」，照那一项的提示做。
   - A：听到声音后锁屏，手机放下就行；锁屏时看一眼标题，会每 30 秒变一次。大约 10 分钟后声音停了再解锁。
   - B：听到声音后锁屏，在锁屏界面点暂停，等 10 秒再点播放。
   - C：听到声音后锁屏，大约 2 分钟后声音停了再解锁。
   - D、E：不用锁屏，听完两段声音。
   - F：A 和 C 都正常就点「跳过」；否则同 C。
3. 每项结束点「正常」或「有问题」，有问题可以写一句（比如“第 3 分钟断了”）。
4. 做完说一声“测完了”。

主会话读日志与结论，按 §4.5 定方案，写回 [prd.md](./prd.md) 的“已裁决”与本文 §3。

### 4.5 结果与交付方案

| 结果 | 交付方案 | 影响 |
| --- | --- | --- |
| A 全部正常 | iOS 与桌面相同：FLAC 长流，`200`、无长度、不支持 Range | 无 |
| A 不能播或锁屏后断，C 正常 | iOS 无损档用 WAV（码率约多一半到一倍）；§3 把 WAV 排在 FLAC 前 | 只改候选表 |
| A/C 失败，日志显示 Safari 先发 `bytes=0-1` 再请求 | 用 `?range=fake` 重测 A；通过就启用 §1.4 的头部重放与伪长度 | 只改 HTTP 层 |
| A、C（含伪长度）都失败，F 正常 | iOS 改用 HLS：同一个渲染会话的输出切成 fMP4 片段、提供播放列表 | **要改 zones 合同 §4.4（流地址指向播放列表、片段子资源）与 output 模块，交主会话裁决** |
| V4 失败（锁屏时回报到不了服务器） | 在线判定加一条“渲染流仍在被读取”，播放头按“已被读取的帧 − 估计的客户端缓冲”估算 | 改 zones design-protocol.md §3.7，交主会话裁决 |
| V5 失败（锁屏时标题不变） | 锁屏信息只在页面可见时更新；锁屏时显示开流时的那首 | 只改 output 模块，记为已知限制 |
| V6a 失败（后台不能换源） | iOS 输出端尽量不在后台换流：格式不同的下一首在同一条流里转换成当前流的格式（第一批用解码 ffmpeg 转），定位、改设置的重建留到页面可见时 | 改 zones §3.5 的接续流规则（只对声明了此限制的输出端），交主会话裁决 |
| V6b 失败（锁屏恢复不能换流） | iOS 输出端暂停时不关会话：流靠背压停在原处，恢复只 `play()`；超过 5 秒也不重建 | 改 zones §3.5 的“暂停超过 5 秒关闭会话”，交主会话裁决 |
| V7 失败（`currentTime` 不准或回退） | 位置改用服务端估算（同 V4 的估算方法），`currentTime` 只用于本地显示 | 改 zones §3.4 的播放头来源，交主会话裁决 |
| V9：AAC 正常、Opus 不行 | iOS 有损档用 AAC：§3 去掉 Opus | 只改候选表 |
| V10：192 kHz 不行 | iOS 的 `max_sample_rate` 写 96000（更高的源按兼容性转换） | 只改候选表 |
| `navigator.mediaSession` 不存在（非安全上下文） | 用 HTTPS 地址复测 A 的 V5，其余结论照用 | 无 |

需要交主会话裁决的几行都不改协议的“期望状态 + 回报”形状，只改个别规则或交付的 HTTP 形式。

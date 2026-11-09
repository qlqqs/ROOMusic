# 技术设计（二）：编码交付、协商、iOS 交付小验证与 HLS 交付

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
- 请求带 `Range` 时忽略，照常回 `200` 完整流（HTTP 允许）。长流只给桌面浏览器，iPhone/iPad 用 HLS（§5），这里不再改。
- zones/httpapi 以 32 KB 缓冲从 `Body` 拷贝，每次读到数据就写出并 `Flush`。客户端断开时 handler 返回，不关闭会话（会话生命周期归 zones）。
- 同一条流的第二个连接：建议回 `410 stream_gone`（父设计 §8 第 5 条）。

### 1.4 备用：头部重放与伪长度（不启用：iOS 改用 HLS，§5；只留作记录）

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
- **能放 HLS 的浏览器（WebKit：iPhone/iPad 与桌面 Safari）不用本表**：只声明 §5.7 的两项 HLS 候选（2026-11-09 裁决，判定见 §5.7）。

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
| A、C（含伪长度）都失败，F 正常 | iOS 改用 HLS：同一个渲染会话的输出切成 fMP4 片段、提供播放列表 | **要改 zones 合同 §4.4（流地址指向播放列表、片段子资源）与 output 模块，交主会话裁决**；**已采用（2026-11-09），见 §5** |
| V4 失败（锁屏时回报到不了服务器） | 在线判定加一条“渲染流仍在被读取”，播放头按“已被读取的帧 − 估计的客户端缓冲”估算 | 改 zones design-protocol.md §3.7，交主会话裁决 |
| V5 失败（锁屏时标题不变） | 锁屏信息只在页面可见时更新；锁屏时显示开流时的那首 | 只改 output 模块，记为已知限制 |
| V6a 失败（后台不能换源） | iOS 输出端尽量不在后台换流：格式不同的下一首在同一条流里转换成当前流的格式（第一批用解码 ffmpeg 转），定位、改设置的重建留到页面可见时 | 改 zones §3.5 的接续流规则（只对声明了此限制的输出端），交主会话裁决 |
| V6b 失败（锁屏恢复不能换流） | iOS 输出端暂停时不关会话：流靠背压停在原处，恢复只 `play()`；超过 5 秒也不重建 | 改 zones §3.5 的“暂停超过 5 秒关闭会话”，交主会话裁决；**不采用**（用户 2026-11-09 接受锁屏久停需解锁） |
| V7 失败（`currentTime` 不准或回退） | 位置改用服务端估算（同 V4 的估算方法），`currentTime` 只用于本地显示 | 改 zones §3.4 的播放头来源，交主会话裁决 |
| V9：AAC 正常、Opus 不行 | iOS 有损档用 AAC：§3 去掉 Opus | 只改候选表 |
| V10：192 kHz 不行 | iOS 的 `max_sample_rate` 写 96000（更高的源按兼容性转换） | 只改候选表 |
| `navigator.mediaSession` 不存在（非安全上下文） | 用 HTTPS 地址复测 A 的 V5，其余结论照用 | 无 |

需要交主会话裁决的几行都不改协议的“期望状态 + 回报”形状，只改个别规则或交付的 HTTP 形式。

**实际结果（2026-11-09，见 prd“已裁决”）**：FLAC/WAV 长流要伪长度才能放且很脆弱，长流锁屏后页面没有任何回报；F（HLS）锁屏正常——命中“改用 HLS”一行，用户已裁决采用，设计见 §5。表中其他行是按长流写的，对 HLS 不再适用；HLS 上还没覆盖到的行为改由 §5.10 补验。

## 5. iOS：HLS 交付

2026-11-09 裁决：iPhone/iPad 的输出端用 HLS 取服务端渲染流，桌面浏览器照旧用长流（§1.1–§1.3）。渲染会话、未来事件队列、播放头、接续流、回报都不变；
变的只有编码器的封装（分片 MP4）、会话把输出切成分片并维护播放列表、流端点的形式，以及浏览器的能力声明。依据见 prd“已裁决”。

### 5.1 播放列表：增长型 EVENT 列表

```text
#EXTM3U
#EXT-X-VERSION:7
#EXT-X-TARGETDURATION:4
#EXT-X-PLAYLIST-TYPE:EVENT
#EXT-X-START:TIME-OFFSET=0,PRECISE=YES
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-MAP:URI="init.mp4"
#EXTINF:4.087,
0.m4s
#EXTINF:4.087,
1.m4s
#EXT-X-ENDLIST            ← 只在会话自然结束（队列放完或格式边界）后写
```

- 一条渲染流 = 一个播放列表。分片完整产出后才追加；条目只增不删（EVENT 的规则），时间线从流开头算起，所以 `currentTime` 就是流内秒数，与已验证的 VOD 列表相同。
- `EXT-X-START:TIME-OFFSET=0`：没有 ENDLIST 的列表，客户端默认从末尾附近起播，必须显式要求从头放。
- 片长 4 秒（与验证一致）：编码器满 4 秒后在下一个包边界切，最长约 4.2 秒，四舍五入不超过 `TARGETDURATION`。`EXTINF` 取分片里的真实样本时长（§5.2），三位小数。
- 列表里不足 3 片且流没结束时，播放列表请求等待，最多 10 秒，之后按当时内容回应。理由：HLS 规定没有 ENDLIST 的列表不应从最后 3 片内起播；渲染远快于实时，3 片通常几百毫秒内就有。
- 列表随流增长，每片约 22 字节，Safari 每个片长重取一次。同格式连续播放几小时（例如整库 CD 抓轨随机播放）时，3 小时的列表约 60 KB，不压缩约 120 kbps，相对有损档的 256 kbps 不能忽略；
  所以请求带 `Accept-Encoding: gzip` 时播放列表用 gzip 回应（重复度高，约压到 1/6 以下）。分片不压缩。
- **放弃滑动窗口的直播列表**（不写 PLAYLIST-TYPE、按 MEDIA-SEQUENCE 删旧条目）：列表大小恒定，但窗口滑动后 `currentTime` 以哪里为零点没有验证过，而位置回报依赖它；EVENT 的时间线由规范固定。

### 5.2 编码与切片

| 档 | 分片里的编码 | 编码参数（输入同 §1.1、§1.2） | `Stream.format.codec` |
| --- | --- | --- | --- |
| 无损 | ALAC | `-c:a alac` | `alac` |
| 有损 | AAC-LC 256 kbps | `-c:a aac -profile:a aac_low -b:a 256k` | `aac` |

两档共用封装参数 `-f mp4 -movflags +empty_moov+default_base_moof+skip_trailer -frag_duration 4000000 -flush_packets 1 pipe:1`。

- 无损档仍经量化器、按 §2.2 定 16/24 位，以 `s16le`/`s24le` 交给编码器；ALAC 是无损的，逐位一致与 FLAC 一样只取决于量化器是否直通。有损档同 §1.2 交浮点。
- **由 Go 切片**：会话按顶层 box 读编码器 stdout，`ftyp`+`moov` 是初始化段，此后每个 `moof`+`mdat` 是一片，其他顶层 box 忽略。片长从 `moof` 里读出
  （`trun` 的样本时长，没有就用 `tfhd`/`trex` 的默认值，单位是 `mdhd` 的时间刻度），不按墙钟估。
- 编码器进程、名额、失败处理沿用 P-c 的 `encoder`，只是 stdout 交给切片器而不是 `Body` 的管道。切片器一直在读，编码器不再受 HTTP 背压；产出由渲染循环的领先上限（design.md §2，20 秒）约束，与长流相同。
- **放弃 ffmpeg 的 `hls` 封装器**（探针用的就是它）：它只能写目录，要监视它重写的播放列表文件才知道分片何时写完，列表还得按我们的地址、START、ENDLIST 规则重写，按时间删旧片也只在滑动窗口模式下有。
  自己切只需读顶层 box，数据都在内存，分片可用是确定的事件。两者产出的都是 ffmpeg mp4 封装器的分片；自己切的分片 Safari 能放，已在 H2 验证（§5.10）。
- 信号路径的 `encode` 步骤：`alac` 记 `lossless`，`aac` 同 §1.2 记 `low`、有损。交付方式不进信号路径。

### 5.3 分片的保留与上限

- 分片只在内存里，不落盘。分片的结束时刻早于“播放头 − 30 秒”就删除（播放头即 design.md §6.2 的值；30 秒覆盖回报间隔与外推误差）。被删分片的条目仍留在列表里（EVENT 的规则）；
  Safari 不会回头取已放过的片，真被取到回 404。
- 产出端受领先上限约束，每个会话常驻约 30 + 20 + 4 秒的音频：ALAC 44.1/16 约 6 MB，96/24 约 20 MB，192/24 约 40 MB；AAC 约 2 MB。一个播放区最多两个会话（当前流与接续流）。时长窗口已经封顶，不另设字节上限。
- 会话 `Close` 或出错结束后整份丢弃，之后的请求得 410。

### 5.4 HTTP 与协议的对应

- **地址**：`Stream.url` 指向播放列表 `/api/v1/me/playback/zones/{zone_id}/streams/{seq}/index.m3u8`，`mime_type` 为 `application/vnd.apple.mpegurl`；
  列表里用相对地址 `init.mp4`、`{n}.m4s`，即同一目录下的 `GET …/streams/{seq}/{file}`。
- **可重复取**：能取的条件与长流相同（该播放区有当前输出会话、`seq` 是当前流或接续流、会话还在），不满足得 `410 stream_gone`；但不限次数，播放列表会被反复取，分片可能被重试。
  片号还没产出或已删除：`404 not_found`。长流“只能取一次、第二个连接 410”的规则只对长流成立。
- **响应头**：播放列表 `application/vnd.apple.mpegurl`；初始化段与分片 `audio/mp4`，用 `http.ServeContent` 回应（带长度、支持 Range）。都带 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff`。
- **鉴权**：同源请求，浏览器带 Cookie（HttpOnly、SameSite=Lax），经 `Guard.Authenticate`；地址里没有凭据（合同 §1）。原生 HLS 播放器给分片请求带不带 Cookie 没验证过，见 H1。
- **位置**：`currentTime` 就是流内秒数（§5.1），回报照合同 §4.2；`Observe`、事件队列、分段换算都不变。AAC 的编码延迟同 design.md §6.2，不修正。
- **`stream_lost`**：元素出 `error`（播放列表 410/404、分片取不到）或长时间 `waiting` 时回报，照常续流。HLS 每片一个请求，短暂断网、换网络时 Safari 自己重试，多半不必续流。
- **开新流**：重建、定位、暂停超过 5 秒后恢复、接手、转移，对输出端都是换一个播放列表地址，规则不变（zones design-protocol.md §3.5）。
  旧流随会话关闭得 410；旧元素随后出的错误带旧 `stream_seq`，服务端按“不是当前流”只刷新在线时间。

### 5.5 格式边界：沿用接续流

- **选 `next_stream`**：会话在格式边界写 ENDLIST，Safari 放完出 `ended`，输出端换上已在备用元素预载的下一条播放列表，与桌面相同（zones design-protocol.md §3.5）。
- **放弃同一播放列表内 `EXT-X-DISCONTINUITY` + 新 `EXT-X-MAP`**：好处是锁屏下换格式不靠页面脚本。但一条流要跨两种格式：要么渲染会话中途换链与编码器，`Stream.format` 与帧号换算都变成分段的；
  要么 zones 把两个会话拼进一个列表，回报的流内秒数再按会话拆开。两种都要改 B3 刚定的 zones 模型与合同，而跨采样率、位深的 DISCONTINUITY 是否无声断续同样没验证。
- **代价**：锁屏下在 `ended` 里对另一个元素 `play()`（H3a）补测中，通过前不派 HLS 实现。失败时的退路是 §4.5 的 V6a 一行
  （只对 HLS 输出端：格式不同的下一首在流内转成当前格式，信号路径标兼容转换），交主会话裁决。

### 5.6 协商

- 服务端能产出的组合（§2.1 第 1 步的过滤表）增加两项，过滤从“只看 `codec`”改成按 `(codec, mime_type)` 配对：

| `codec` | `mime_type` | 无损 | 交付 | 需要的编码器 |
| --- | --- | --- | --- | --- |
| `flac` / `wav` / `opus` / `aac` | 同 §3 表 | 同 §3 表 | 长流 | 同 §2.1 |
| `alac` | `application/vnd.apple.mpegurl` | 是 | HLS | `alac` |
| `aac` | `application/vnd.apple.mpegurl` | 否 | HLS | `aac` |

- 其余规则不变：§2.1 选档，§2.2 定采样率、位深与声道，ALAC 按无损的规则。`StreamFormat` 加内部字段 `Delivery`（`stream` | `hls`，`json:"-"`），会话据此选封装与切片，zones 据此给地址。

### 5.7 能力声明（zones 合同 §2.6、§8）

- 合同 §2.6 只增加取值：`codec` 增加 `alac`；`mime_type` 为 `application/vnd.apple.mpegurl` 表示 HLS（分片是 fMP4，里面是 `codec`），其他是长流。不加新字段。
- 浏览器的两项 HLS 候选：

| `codec` | `mime_type` | `lossless` | `max_sample_rate` | `max_bit_depth` | 探测（`canPlayType` 都非空） |
| --- | --- | --- | --- | --- | --- |
| `alac` | `application/vnd.apple.mpegurl` | true | 192000 | 24 | 播放列表类型，与 `audio/mp4; codecs="alac"` |
| `aac` | `application/vnd.apple.mpegurl` | false | 48000 | — | 播放列表类型，与 `audio/mp4; codecs="mp4a.40.2"` |

- **何时声明**：两项都能探测到时，**只声明这两项**，不声明 §3 的长流候选；否则照旧只用 §3 的四项。
  长流在 iOS 上锁屏没有回报、FLAC/WAV 要伪长度，不能当退路。能原生放 HLS + ALAC 的只有 WebKit（iOS 上所有浏览器都是），桌面 Safari 也随之走 HLS，这是有意的；不识别 UA。
  音量能否写入不参与判定（2026-11-09 真机返工）：iOS 27 的 Edge 音量写入可读回，不能用它识别 iPhone。
- 只用 `canPlayType`，不用 `mediaCapabilities.decodingInfo`（iOS 对能放的格式也报不支持）。
- AAC 写 48 kHz：iOS 上 96 kHz AAC 没验证，256 kbps 下也没有收益；P-c 已按声明的 `max_sample_rate` 同族减半，不用改代码。

### 5.8 锁屏信息

- 输出端在 `Stream.segments` 的曲目边界设置完整的 `MediaMetadata`（标题、艺人、专辑、封面）与 `setPositionState`（曲目时长、曲目内位置），由 `timeupdate` 驱动；
  锁屏下至少每 5 秒一次，边界最多晚这么久。只给标题时 iOS 锁屏不显示；HLS 下给齐信息能显示、播放中途能切换（H2 已验证）。现有 `MediaSessionBinding` 已经这样做，HLS 不用改。
- 增长型列表的 `duration` 是 `Infinity`，锁屏进度条是否按 `setPositionState` 显示归 E 的真机核对，不影响本设计。

### 5.9 反代与改动的文件

- **反代没有新要求**：HLS 都是普通短请求，播放列表最多等 10 秒，在现有 60 秒读超时内；nginx 播放区 location 的 `gzip off` 不影响应用自己压缩的播放列表。长流的要求仍为桌面保留，部署 README 不改。
- 端口（zones 定义，算法线实现）：

```go
type HLSMedia interface {
	Playlist(ctx context.Context) ([]byte, error) // 不足 3 片且流没结束时等待，最多 10 秒
	Init() ([]byte, error)
	Segment(n int64) ([]byte, error)              // 没产出或已删除：ErrSegmentMissing；会话已关闭或出错：ErrStreamGone
}
// RenderSession 增加 HLS() HLSMedia：Delivery 为 hls 时非 nil，此时 Body() 立即 EOF；长流为 nil。
```

| 文件 | 改动 |
| --- | --- |
| `playback/render/hls.go`（新） | 切片器（读顶层 box、片长）、分片仓库（保留窗口）、播放列表（等待、ENDLIST） |
| `render/encode.go`、`negotiate.go`、`session.go` | HLS 的封装参数与 stdout 去向；`(codec, mime_type)` 配对表与 `Delivery`；`HLS()` 与按播放头删旧片 |
| `zones/ports.go`、`render_events.go`、`outputs.go` | 端口；按 `Delivery` 给 `Stream.url`；按 `TakeStream` 的检查取 `HLSMedia`，但不置 `taken` |
| `zones/httpapi/handlers.go`、`stream.go` | `GET …/streams/{seq}/{file}`，文件名只认 `index.m3u8`、`init.mp4`、`<数字>.m4s`；播放列表 gzip；分片 `ServeContent` |
| `frontend/src/features/player/output/capabilities.ts` | §5.7 的候选与声明规则 |

### 5.10 实现前补测（已验证）

2026-11-09 用 `ios-probe` 的 G–J 在 iPhone 上的 Edge 测过（HLS 按 §5.1–§5.2 实时生成，经 HTTPS 反代），结论与用户裁决见 prd“已裁决”的 HLS 补测一条：

- H1：播放列表、初始化段、分片请求都带 HttpOnly Cookie，地址不带凭据。
- H2：增长型 EVENT 列表锁屏连续播放，列表重取 ≤ 8 秒，从 0 起播，隐藏时回报照常；锁屏上标题、艺人、专辑与封面能显示并随换曲切换。
- H4：ALAC 44.1/24、96/24、192/24 与 AAC 48 kHz 都能放，ALAC 上限 192 kHz/24 位（§5.7）。
- H3b/H3c：锁屏暂停约 10 秒后要解锁才能继续，用户接受，不采用 V6b（§4.5），锁屏按钮照常由页面接管。
- H3a（锁屏下 `ended` 后在备用元素上 `play()` 接续流）：补测中，通过前不派 HLS 实现；失败按 §5.5 的退路。

### 5.11 需要改动的 zones 合同条目（主会话 2026-11-09 全部采纳，已改入 zones 文档）

| 位置 | 现在 | 建议 |
| --- | --- | --- |
| 合同 §2.2 `Stream` | `url` 注释“服务端给出，客户端不拼接”；`format.codec` 未列 ALAC | `url`：“长流指向流端点（§4.4），HLS 指向播放列表（§4.5）”；`mime_type`：“HLS 为 `application/vnd.apple.mpegurl`”；`format.codec` 可为 `alac` |
| 合同 §2.6 `stream_formats` | `codec: "flac" \| "wav" \| "opus" \| "aac" \| string`；“具体取值由 dsp-pipeline 的 iOS 验证定” | 增加 `"alac"`；加一句“`mime_type` 为 `application/vnd.apple.mpegurl` 表示 HLS（fMP4 分片里是 `codec`），否则是长流”；取值改指 dsp-pipeline design-delivery.md §3、§5.7 |
| 合同 §4.4 | 标题“渲染流”；“若 … Safari 先发探测请求 … 改用头部重放”；“是否支持 Range、是否带长度，由 … iOS 验证决定” | 标题改“渲染流（长流）”；删去头部重放一句；改为“不支持 Range、不带长度（design-delivery.md §1.3）” |
| 合同新增 §4.5 | — | “HLS 流 `GET /zones/{zone_id}/streams/{seq}/{file}`”：文件为 `index.m3u8`、`init.mp4`、`{n}.m4s`；能取的条件同 §4.4 但可重复取；不满足 `410 stream_gone`，片号没产出或已删除 `404 not_found`；EVENT 列表从流开头起、流结束时有 ENDLIST，`stream_seconds` 即列表时间线上的位置 |
| 合同 §6 | `410 stream_gone`：“… 或这条流已经被取过” | “… 或这条长流已经被取过”；`404 not_found` 的条件加“HLS 分片没产出或已删除” |
| 合同 §8 “能力”行 | “用 `canPlayType` / `MediaSource.isTypeSupported` 探测 dsp-pipeline 给出的候选” | “只用 `canPlayType`（不用 `mediaCapabilities.decodingInfo`）；HLS 候选都能探测到且音量只读时只声明 HLS 候选，否则只声明长流候选（design-delivery.md §3、§5.7）” |
| design-protocol.md §3.2 “地址” | “一条流只能被取一次 … iOS 上用长 FLAC/WAV 响应还是 ManagedMediaSource，由 dsp-pipeline 设计（§14）” | “长流只能被取一次；HLS 的播放列表与分片在流有效期内可重复取（合同 §4.5）。iPhone/iPad 用 HLS，桌面用长流（dsp-pipeline design-delivery.md §5）” |
| design-protocol.md §3.6 | “锁屏信息：… 换 Media Session 的标题与时长”；末条“iOS 风险（须先做小验证）…” | 锁屏信息改为“标题、艺人、专辑、封面与时长（只给标题时 iOS 锁屏不显示）”；末条改为“iOS：已验证并裁决用 HLS（dsp-pipeline prd 2026-11-09），锁屏下页面照常运行、回报照常” |
| design.md §14 端口 | `RenderSession` 只有 `Body()`；表中“流只能被取一次”“iOS 交付验证 … 结论决定 … 流端点是否需要 Range” | 增加 `HLS() HLSMedia` 与 `HLSMedia` 接口（§5.9），`StreamFormat` 加内部字段 `Delivery`；表中改为“长流只能被取一次，HLS 可重复取”“iOS 用 HLS，已定” |

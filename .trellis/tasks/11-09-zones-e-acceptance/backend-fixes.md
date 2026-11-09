# 长静音 FLAC 起播修复与格式样本更新

日期：2026-11-09。对应 `acceptance-real.md` 问题 3；沿用本任务，不提交、不归档，不修改前端或其他会话的改动。

## 根因证据

使用临时 HTTP 探针直接调用真实 `render.Renderer`，假 `TrackSource` 只提供临时合成的 180 秒、44.1 kHz、16 位、双声道零样本 FLAC；电平化 `off`、Headroom 关闭，逐次读取并冲刷 `Body`，不调用 `Observe`。不启动数据库，不依赖前端控制器，不重跑整套验收。

```bash
# 固定工具；以下探针文件只用于定位，已从工作树移出。
export ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg
"$ROOMUSIC_TEST_FFMPEG" -hide_banner -loglevel error \
  -f lavfi -i anullsrc=r=44100:cl=stereo -t 180 \
  -c:a flac -sample_fmt s16 /tmp/roomusic-silence-probe/silence.flac
(cd backend && go run ./tmp-silence-probe)
curl -sS --max-time 10 -D /tmp/roomusic-silence-probe/headers.txt \
  --trace-time --trace /tmp/roomusic-silence-probe/curl.trace \
  http://127.0.0.1:18769/stream -o /tmp/roomusic-silence-probe/before.flac
"$ROOMUSIC_TEST_FFMPEG" -hide_banner -v info -err_detect crccheck+explode \
  -i /tmp/roomusic-silence-probe/before.flac -f md5 -
```

- HTTP 200、`audio/flac`、chunked。10 秒共收到 **11,023 字节**；请求于 07:36:29.057588 UTC 发出，最后数据于 07:36:29.099411 到达，之后没有增长。`curl` 返回 28 是主动限定 10 秒截取尚未结束的长流，不是 HTTP 失败。
- `fLaC` 与元数据块完整：STREAMINFO 34 字节、Vorbis comment 44 字节、PADDING 8192 字节，音频偏移 8286。
- 音频有 **191 帧**，每帧 14/15 字节、4608 样本；逐帧 CRC-16 全部正确，最后一帧完整。解码得到 **880,128 样本帧 / 19.957551 秒**，没有解码错误；PCM s16le MD5 `d8c21178ab2675199c3849486c6a917d` 等于同长度全零字节的 MD5。
- 不经过项目播放器，临时页面只放原生 `<audio>`。旧流 10 秒后 `readyState=0`、`networkState=2`、`currentTime=0`。Chromium 的 FFmpeg AVIO 桥使用 32 KiB 读取缓冲（定位时源码快照保存在证据目录的 `ffmpeg_glue.cc`、`blocking_url_protocol.cc`）。现有完整流头和近 20 秒 PCM 仍不够一次按字节读取；播放头不动又让渲染无法越过 20 秒上限。

因此不是坏流头、坏帧、HTTP 未冲刷，也不是前端控制器故障，而是**FLAC 高压缩静音的字节供给与有界预渲染、客户端读取之间形成互相等待**。既有逐位测试只覆盖短有限流，EOF 会解除读取等待，未覆盖这一长流场景。

## 当前修复：时间上限加编码字节下限

撤掉 `-frame_size`，恢复 FLAC 默认分帧、压缩级别 5 和逐包冲刷。用户否决对所有曲目统一采用 5 ms 分帧：它增加正常音乐的帧头与预测预热开销，不应为罕见静音内容让所有曲目承担压缩代价。

- 所有非 HLS 长流统一使用字节下限（`Delivery != "hls"`，包含 FLAC、Opus、AAC、WAV），不再按编码器限制。在现有 `waitToRender` 内，领先达到 20 秒且未播放编码字节达到 **64 KiB** 才等待；字节不足允许继续渲染，够了仍按时间上限停。短于字节下限的有限流可以渲染到真实 EOF，不补造数据。
- 计量口径为**已编码但尚未播放**，包括已被浏览器读走的字节。按块记录流帧号与实际累计编码输出字节，通过播放头换算并修剪检查点；不把 PCM 长度、总累计字节或服务端未读缓冲当作未播放编码量。
- 实现只修改 `encode.go`、`session.go`、`events.go`：非 HLS 的 ffmpeg 输出经现有输出协程，在读取编码输出后、写交付管道前原子累计字节，不持会话锁阻塞写；WAV 不经 ffmpeg，流头与 PCM 直接写出同样使用计数出口，避免计数始终为零；`waitToRender` 统一计算并返回可渲染帧数，移除 `run` 中原先重复的 20 秒判断。字节不足时每次至多放行一个现有量子，64 KiB 不是硬缓存上限。
- 检查点把当前累计字节近似归属到最新已提交 PCM 块的末帧，同帧合并；异步落后的编码输出可能归到后续采样块，EOF 再补采尾部字节，不新增编码帧解析器。`Observe` 和既有派发时钟只修剪检查点，保留已播累计基线，回退不复活已删除历史；自然结束后仍可修剪，不改变事件队列及派发语义。
- 不增加协程或定时器，不改 PCM、量化、增益、抖动；HLS 不参与计数与字节下限，保留原时间上限。正常音乐在原有 20 秒内已经达到字节下限，继续使用原来的时间限制与默认分帧。
- 按用户明确裁决，不处理连续一百多首极短静音填满 `pending` 256 条的极端场景，不改事件队列、不加对应测试。接续流与 `rebuild_needed` 仍沿用已有策略；低压缩字节量导致的更长预渲染可能提前跨过曲目边界，这是字节下限方案的取舍。

## 已撤销短帧方案的定位对照（不是当前修复的验证）

先前定位阶段曾用临时 ffmpeg 包装器增加 `-frame_size max(16, 输出采样率/200)` 作对照，以下数据保留为根因证据，不代表当前字节下限实现的浏览器实测：

- 相同 10 秒请求收到 **74,144 字节**，其中元数据仍为 8286 字节，4002 帧、每帧 220 样本；全部 CRC 正确。
- 解码 880,440 样本帧 / 19.964626 秒；PCM s16le MD5 `f2806b67df101be9b5e09c70fd899c34` 等于同长度全零字节。
- 同一原生音频页面在 **454 ms** 内已有 `currentTime=0.225335`、`readyState=3`、`paused=false`。无项目播放器代码参与。该数字仅为本机定位证据，不是延迟承诺。

浏览器探针用本地 `frontend/node_modules/playwright` 的 Chromium，通过 `source /home/qlqq/.local/share/playwright-deps/env.sh; node` 启动；打开临时页面、点击解锁后调用原生 `play()`，等待 `currentTime > 0.2`。没有新增浏览器用例或修改前端文件。

临时探针源码、包装器、curl trace、前后 FLAC、CRC 检查脚本都保留在 `/tmp/roomusic-silence-probe/`；探针进程已停止，`backend/tmp-silence-probe/` 已删除。当前字节下限实现由下述 render 回归验证，本轮不重复浏览器探针。

## 格式样本

上一轮只选择生成格式样本的实际测试用例；样本已获用户接受，本轮不重新生成或验证：

```bash
ROOMUSIC_UPDATE_API_SAMPLES=1 \
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-zones \
ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' \
ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg \
scripts/test-integration.sh
```

通过，实际用例耗时 35.13 秒。`playback/formats.json` 增加 13 项 `cue_virtual`，总项数 13→26，原有项不变。该用例没有更窄的独立格式子用例：运行前备份 646 份样本，运行时重写 209 份，**除 formats.json 外其他样本内容均未变化**，无需恢复；没有顺手更新其他样本。日志与备份：`/tmp/zones-formats-samples-me_vrsly/`。只使用并清理 `roomusic-test-zones`，未操作用户排除的容器。

## 局部验证

沿用 `backend/internal/playback/render/silence_test.go` 的 `TestRenderSilentFLACStartsWithinRenderWindow`（44.1 kHz 双声道、8 kHz 单声道），按字节下限更新原先“绝不超过 20 秒/不能自然结束”的断言。无 `Observe` 时仍须取得至少 32 KiB，完整解码 PCM 长度与 MD5 必须等于原始全零数据；不新增事件队列测试。

本轮指定命令，在 `backend/` 执行：

```bash
ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg \
  go test ./internal/playback/render \
  -run '^(TestRenderSilentFLACStartsWithinRenderWindow|TestRenderSession)$' -count=1
```

上一轮仅 FLAC 字节下限实现执行上述测试集一次，**通过，测试耗时 1.397 秒（墙钟 2.15 秒）**。静音两子例均满足无 `Observe` 读取 32 KiB、完整 180 秒 PCM 长度与 MD5 一致；`TestRenderSession` 的既有会话和 16/24 位逐位一致断言通过。生产代码聚焦审查未发现阻塞问题，未扩大测试范围。

修改文件的 `gofmt -l`、`git diff --check` 通过；未跟踪文件用 `git diff --no-index --check /dev/null <文件>` 检查，包括 `silence_test.go` 与本记录。字节下限实现合计涉及三个生产文件，测试仍为原来的一个回归函数；本轮推广到非 HLS 只再调整 `encode.go` 与 `session.go`，常量更名为 `streamUnplayedBytes`，不新增或修改测试，文档同步两份。

上一轮短帧实现的最小测试曾通过（3.764 秒），该结果仅保留历史，不作为本轮字节下限实现的证据。

本轮推广至所有非 HLS 长流后，独立检查代理仅执行上述同一命令一次，**通过，测试耗时 1.352 秒（墙钟 2.06 秒）**；未新增或修改测试。聚焦检查确认 WAV 头部及 PCM 均计数，HLS 仍走独立交付路径且不放宽领先限制；`gofmt -l` 与限定文件差异检查通过。

本轮只执行以上两组测试及修改文件的格式检查，不运行全量、不复跑 E 端到端验收、不重复生成格式样本。未提交 Git，未归档整个 E 任务。

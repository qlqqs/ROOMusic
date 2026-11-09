# P-c 实现与测试交接

## 范围与文件

只实现 P-c 第 1–4 步。不新增测试、不启动数据库、不提交 git；P-c 第 5 步交独立测试 agent，任务继续 in_progress。

- `backend/internal/playback/render/ffmpeg/`：tool、process、平台进程组、自检与复制的合成 ALAC 素材。
- `backend/internal/playback/render/`：renderer、source、decoder、player、session、events、encode、negotiate、monitor。
- `backend/internal/playback/service.go`、`render_formats.go`、`render_source.go`、`sniff_wav.go`：复用安全来源解析与打开，新增渲染能力表、错误分类与 WAV 子类型核验。
- `backend/internal/app/renderer.go`、`app.go`、`backend/cmd/roomusic/main.go`：探测、来源分析适配、真实渲染装配与停机回收。
- `backend/internal/app/dependency_rules_test.go`：只增加真实新包与 app 的依赖规则，不新增测试函数，不修改既有业务断言。
- `backend/internal/zones/ports.go`：只给 StreamFormat 增加三个内部能力上限字段；未改 zones 服务逻辑。
- `.trellis/spec/backend/playback-render-contracts.md` 及目录/索引：新增可执行合同。

## 主要类型与接口

ffmpeg 的 Tool/Reservation/Process 分别拥有能力与总配额、原子预留名额、单进程管道及回收。Open 含探测与自检；来源解码进程启用读取超时，编码器不把背压当超时。调用方必须 Close 进程，即使已经 Wait。

render 的 Renderer 管理会话；TrackSource/Track 是来源端口；内部 decoder 包含预缓冲和电平化，player 是 G-2 唯一取帧接点，session 拥有跨曲链/量化器/编码器，events 负责播放头与未来事件。Track.IsFloat 明确区分扩展 WAV 整数/浮点；预留的起止帧是源 PCM 帧，而非 CUE 75 Hz 索引。

playback 的 RenderDecoders 只需 HasDecoder；WithRenderDecoders 开启 OpenForRender/RenderFormats，不改变旧 Formats/HTTP。RenderError.FailureReason 保留曲目级分类，DB/取消归 transient。

## 协商、Open 与事件

Negotiate 按设备偏好和目标档位选择可用编码，返回编码/MIME/实际档位，以及 MaxSampleRate/MaxBitDepth/MaxChannels。Open 根据首曲确定具体格式并固定，先原子预留编码与解码名额，不等待预缓冲；Body 等 33% 或 EOF 就绪后输出。

time 在派发器中按 200 ms 网格补齐，播放头来自 Observe 与墙钟外推并钳到已写帧。track_started、track_failed、stream_ended 和分段 signal 排入未来队列；format_boundary、rebuild_needed 和速度立即发出。关闭取消可解除事件背压。编码错误结束 Body 并取消事件；自然 EOF 不立即取消派发器。

## 与设计的具体化及合并提示

1. 源码禁止在 Open 内同步 Cursor.Next，与设计“Open 同步跳过坏首曲并确定成功曲格式”冲突。选择遵守锁合同：首曲准备失败的会话使用空流格式，协程跳过坏条目并以 format_boundary 为成功曲请求接续流；不在返回后悄悄改 Format。
2. StreamFormat 增加三个带 `json:"-"` 的内部上限字段，避免按编码缓存最近一次协商结果造成设备间串扰；对外 Stream.format 不变。B3 合并时保留这三字段即可。
3. `RenderFormats` 先提供独立入口；旧 HTTP 格式表、派生启动和缓存不退役，P-d 再切换。
4. P-e 的 Headroom/削波/完整步骤尚未接入；P-c 的 signal 只携带分段与速度（分段 signal 的 Signal 为 nil，速度只带 processing_speed）。G-2 接入淡化、就绪超时与 0.5 秒静音间隔；P-g 再放开 CUE。
5. 第一批压缩输出使用最多八声道的标准布局，WAV 保留设备声道上限；Opus 多声道显式设置 mapping_family=1。浏览器 max_channels=2 的路径不受影响。
6. `app.go` 只提前构造 playbackService、接入独立文件的装配函数、传 Renderer、停机调用关闭。B3 的 PlayRecorder 等装配按相同 zones.Options 合并，不重写这段生命周期。

## 给测试 agent

- 用假 TrackSource 返回合成文件，不需要数据库。用固定 ffmpeg 生成 16/24 位 FLAC，关闭 leveling 后经 Body 收流，解码为 pcm_s32le 比 MD5；短曲目可不喂播放头，超过 20 秒必须 Observe 前进。
- 通过 `Options.Clock` 和 `NewTicker` 控制播放头、50 ms 派发与100 ms领先检查；Observe 可唤醒。核对时间网格补齐、回退不重发、首条定位偏移、次曲边界、先 time 后尾部 stream_ended。
- 通过 ffmpeg.OpenWithOptions 的 NewTimer 控制名额5秒与解码10秒；Timer 需实现 C/Stop。模拟ffmpeg脚本必须先满足 Open 的探测和自检，可在自检后通过外部开关只挂起后续解码，避免测到启动自检超时而误以为是会话超时。
- byteRing 的就绪、源读取及处理速度可在 render 包内测：33% 前不出 Body 字节、EOF 短曲可出、完整帧累计（首次进程 Read 可只给1字节），等待不进速度统计。
- 假 Cursor 测同格式拼接、异格式边界、短曲循环、失败上限和 NextChanged；必须覆盖 Cursor 持锁时会话 Close 返回，Renderer.Close 能取消并等待 Open/会话。
- Negotiate 表驱动覆盖档位回退、编码器不可用、设备上限隔离、整数/浮点 WAV、超采样率/降混。检查扩展 WAV 整数仍保留 Exact，浮点不直通。
- OpenForRender 的安全打开、缺失/停用/内容变化/异常WAV、app分析读取另做针对性测试；P-d/P-e/P-g用例本次不应作为已实现功能断言。

## 自查

实际执行（初轮及审阅修复后的最终轮均通过）：

```sh
cd backend && go build ./... && go vet ./internal/playback/... ./internal/app && go test ./internal/playback/render/... ./internal/app -run TestPackageDependencyDirections -count=1
```

render 和 ffmpeg 包显示 `[no test files]`，app 依赖方向测试通过；这不替代 P-c 第 5 步的行为测试。

一次手工冒烟：`bash /tmp/dsp-render-pc-smoke.sh`（临时脚本不入库，首次通过后停止）。脚本以 `scripts/ffmpeg-pinned.sh` 的 ffmpeg 7.1.1，用 lavfi 生成 2 秒、997 Hz、44.1 kHz、16 bit、双声道 FLAC；临时 Go 程序在 backend 内调用 ffmpeg.Open → render.New → Negotiate → Open → io.Copy(Body)，不开电平化。输出 34235 字节。

ffprobe 7.1.1：`codec_name=flac, sample_rate=44100, channels=2, bits_per_raw_sample=16`。两侧用 `ffmpeg -i <文件> -map 0:a:0 -c:a pcm_s32le -f md5 -`：

```text
source: MD5=009a0695dc2c5ba0bd1f2d4c9d72dc7a
render: MD5=009a0695dc2c5ba0bd1f2d4c9d72dc7a
```

临时 Go 程序和音频由脚本退出 trap 删除。另执行 gofmt、git diff --check，未新增测试、未修改既有业务测试期望，仅扩充依赖规则。未运行全量测试、不启动数据库：用户限定本次验证范围，独立测试 agent 负责行为用例。

## 审阅收敛与防回归

- 进程生命周期：stdout EOF 不等于进程退出；新增注入计时器的退出等待上限，避免停止读取监控后无界 Wait。覆盖“关闭 stdout 后继续挂起”的假进程。
- 事件生产与派发：同帧排序只能排序已登记事件，不能阻止生产者尚未登记下一首；使用已写帧的开区间和 renderComplete 尾部屏障，覆盖两量子之间恰好到边界的 Observe。
- 接续流一经对外宣布不能静默撤回；NextChanged 仅替换边界或要求重建，覆盖异格式→同格式与移除后继。
- signal 的质量不是 Stream 的档位；P-c 不伪造完整质量合同，P-e 再统一装配。

这些合同已写入 playback-render-contracts.md；新测试仍交独立 agent，不在本轮实现中补写。

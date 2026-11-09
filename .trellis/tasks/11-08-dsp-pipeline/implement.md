# 执行清单：DSP 处理链与服务端渲染

设计：[design.md](./design.md)（会话结构、解码、事件队列、电平化、Headroom、信号路径、派生退役）、[design-delivery.md](./design-delivery.md)（编码交付、协商、iOS 验证）；
跨子项：[父任务 design.md](../11-08-audio-dsp-reference-port/design.md) 与 [implement.md](../11-08-audio-dsp-reference-port/implement.md)（顺序、分支、跨子项验收）。zones 的时间点见 [zones implement.md](../11-08-server-playback-zones/implement.md)。

通用命令（本线 worktree 根目录；ffmpeg 用固定版本）：

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
(cd backend && go test ./internal/dsp/... -count=1)
(cd backend && go test ./internal/playback/render/... -count=1)
(cd backend && go test ./internal/app -run TestPackageDependencyDirections -count=1)
(cd frontend && npx vitest run src/features/player && npm run typecheck && npm run lint)   # 只在 P-f
```

渲染会话的测试在 `ROOMUSIC_TEST_FFMPEG` 为空时跳过（沿用分析器测试的做法）；合成素材在测试里用 ffmpeg `lavfi` 现场生成，不进仓库。

## P-a iOS 交付小验证（最先做，与 zones B1 并行）

1. [ ] 后端 GPT：在一次性分支 `ios-probe` 上写 `backend/cmd/ios-probe/`（design-delivery.md §4.2：流、HLS、日志、页面、`?range=fake`）。只用标准库与 ffmpeg；页面文案只有每项一句提示。
2. [ ] 主会话：构建并拷到八号机（连同 `scripts/ffmpeg-pinned.sh` 取出的 ffmpeg），后台运行在 `:18090`；自己用桌面 Chrome 打开走一遍 A 的前 30 秒，确认能出声、日志在写。
3. [ ] 用户：按 design-delivery.md §4.4 的四步在 iPhone 上测（约 20 分钟）。
4. [ ] 主会话：读 JSONL 日志与结论，按 §4.5 定方案；结论写进本 prd“已裁决”（附 Safari 版本），更新 design-delivery.md §3 的 iOS 候选；命中“交主会话裁决”的行先裁决。停掉探针、删除分支。

验收：§4.1 的 V1–V10 每项都有结论（V11 只在需要时）。没有自动化测试。

## P-b 纯包（与 P-a 并行；gapless-crossfade 的 `dsp/fade`、dither 的 `dsp/dither` 同期）

1. [ ] 后端 GPT：`dsp/chain`：`Format`、`Buffer`、`GainRegion`、`Processor`/`Node`、`Wrap`（延迟补偿、变长输出、按绝对帧号搬运区域）、`Build`（参考稿 §6.1 的顺序，第一批只装 ① 与出口量化）、`Drain`、`Headroom` 节点、`SignalStep` 结构（父设计 §2–§5）。
2. [ ] 后端 GPT：`dsp/leveling`：`Choose`、`Linear`、`Apply`（design.md §7）。
3. [ ] `dependency_rules_test.go` 加 `dsp/*` 的规则（都不允许导入项目内包，`dsp/chain` 只允许 `dsp/` 下的算法包）。
4. [ ] 测试 agent：design.md §11 的 `dsp/leveling` 与 `dsp/chain` 几条。

验证：`go test ./internal/dsp/...`；依赖方向。回滚点：P-b 提交。

## P-c 渲染会话骨架（zones B2 端口定稿、P-a 结论之后）

1. [x] 后端 GPT：`playback/render/ffmpeg`：从 `playback/derivation/ffmpeg.go` 搬来定位、版本、解码器清单解析；加编码器清单（`-encoders`）、soxr 探测、进程组与 `WaitDelay`、全局进程信号量（`3 × GOMAXPROCS`，取名额最多等 5 秒）、带起始与停滞超时的读取。
2. [x] `playback/render`：`TrackSource` 端口与 `Track`；来源段（解码命令 design.md §4.2、预缓冲环 §5.1、电平化区域生成与施加 §7.2）；`player.read` 的最小实现（格式相同就短读拼接、不淡化、不超时，留出 gapless-crossfade 的挂点）；
       渲染循环（§5.2：量子、领先 20 秒、背压、处理速度计时）；事件队列与派发器（§6）；编码器（design-delivery.md §1.1–§1.2）；`Negotiate` 与具体格式（§2）；`Renderer`/`RenderSession` 的全部方法（design.md §3）。
       量化先用 `dsp/dither` 已有的接口（D 接入前若 `dsp/dither` 还没合入，用直通 + 四舍五入的临时实现，D 接入时删掉）。
3. [x] `playback`：`Service.OpenForRender`（复用 `resolve` 与 `openVerified`，按 design.md §4.1 映射错误）；可渲染格式表（design.md §10.1）。
4. [x] `app`：`OpenRenderer`（定位、清单、自检 §10.2）与装配：`TrackSource` 适配器（`playback.Service` + `catalog.Store.LoadTrackAnalysis`）、`render.New`、交给 `zones.NewService`；关停时 `Renderer.Close`。替换 zones B2 的假渲染器在生产装配里的位置（测试装配里假渲染器保留）。
5. [ ] 测试 agent：design.md §11 的 `playback/render` 各条（逐位 MD5、事件队列、预缓冲、超时、定位）；`Negotiate` 的规则表一条表驱动测试（lossless/lossy × 有无对应格式 × 没有可用格式）。

验证：`go test ./internal/playback/render/...`、`./internal/playback/...`（只本包）、依赖方向。联调信号：一首 FLAC 能在桌面 Chrome 里经 zones 放出来（主会话手工一次）。
风险文件：`internal/app/app.go`、`cmd/roomusic/main.go`（装配与关停顺序）、`playback/service.go`。回滚点：P-c 提交。

## P-h iOS：HLS 交付（design-delivery.md §5；小验证现在就可做，实现在 P-c 合入、§5.11 的合同改动裁决之后）

1. [ ] 后端 GPT：在 `ios-probe` 分支加 G–J（§5.10 H1–H4：HLS 按 §5.1–§5.2 实时生成，测试 Cookie 只记有无）。主会话部署到八号机，经 HTTPS 反代；用户在 iPhone 上测约 15 分钟。
       主会话按结果填 §5.7 ALAC 的上限；任一项落到“交主会话裁决”就先停在这里。
2. [ ] 主会话：按 §5.11 改 zones 合同与协议文档（与 B3 的收尾一起裁决）。
3. [ ] 后端 GPT：`render`：`(codec, mime_type)` 配对表与 `Delivery`（§5.6）、分片 MP4 封装参数（§5.2）、`hls.go` 切片器与分片仓库与播放列表（§5.1–§5.3）、`HLS()`；
       `zones`：`HLSMedia` 端口、按 `Delivery` 给 `Stream.url`、取媒体不置 `taken`；`zones/httpapi`：`GET …/streams/{seq}/{file}`、文件名校验、播放列表 gzip、分片 `ServeContent`（§5.4、§5.9）。
4. [ ] 前端 Opus（新开一个子 agent）：`output/capabilities.ts` 按 §5.7 声明候选（只用 `canPlayType`）。不加任何界面文案。
5. [ ] 测试 agent（只测下列行为）：`Negotiate` 配对表加两行（HLS 候选只配 HLS、长流候选不配 HLS）；切片器对真实 ffmpeg 分片 MP4 输出的片长之和等于写入帧数；
       ALAC HLS 逐位一致（初始化段 + 全部分片拼起来解码，MD5 等于源，16/24 位各一）；播放列表（START、不足 3 片时等待、结束后有 ENDLIST、删旧片后取它得 404）；
       handler（非当前 `seq` 得 410、播放列表可重复取、gzip）；前端能力两条（音量只读且 HLS 可探测时只有两项 HLS，否则与原表相同）。

验证：`go test ./internal/playback/render/... ./internal/zones/...`（只本次相关的测试名）、`(cd frontend && npx vitest run src/features/player/output)`。联调：iPhone 上放一张专辑并锁屏（归 E）。
风险文件：`zones/ports.go`、`zones/outputs.go`、`zones/render_events.go`、`zones/httpapi/stream.go`、`render/session.go`、`render/encode.go`。回滚点：P-h 提交。

## P-e Headroom、削波、处理速度、信号路径（后端；P-c 之后，与 G、D、P-d 并行）

1. [ ] 后端 GPT：迁移 `00NN_playback_zone_headroom.sql`（+ `.down.sql`，编号接在 zones 的 0037 之后，design.md §8.1）；zones 的设置结构、`set_settings` 校验与合同 DTO 加 `headroom` 键（父设计 §8 第 7 条，改 zones 代码需主会话先确认 B2 已合入）。
2. [ ] `render`：链 ① 装 Headroom；削波统计与 `clip` 事件、450 ms 窗口与 250 ms 复评（§8.2）；处理速度（§8.3）；信号路径的计算与按分段派发（§9.1）。
3. [ ] 为合同新增的 `settings.headroom` 与 `signal` 生成样本到 zones 的 `api-samples/playback-zones/`（与 B3 同一目录）。
4. [ ] 测试 agent：Headroom 开启后信号路径出现 `headroom`；削波亮起约 0.5 秒后熄灭（时钟注入）；`set_settings` 拒绝越界的 `db`；迁移升级与回退各一条（隔离库，只在本步需要时用 `roomusic-test-dsp`）。

回滚点：P-e 提交。

## P-d 派生退役（P-c 之后）

1. [ ] 后端 GPT：删除 `playback/derivation/`、`playback/derived.go`、预检的 202 等待与 `derivation_*` 错误码；`/playback/formats` 改为可渲染表（字段不变，design.md §10.1）；
       启动时删除旧 `media-cache`（§10.2）；`platform/config` 去掉 `ROOMUSIC_MEDIA_CACHE_MAX_MIB`（仍设置时忽略）；README 两处改动；`scripts/playback-e2e.sh` 里给派生用的说明改为渲染。
2. [ ] 测试 agent：旧缓存目录存在时被删除且记一条日志、不存在时不报错；`/playback/formats` 含 ALAC 等组合；旧接口对原派生格式返回 `not_directly_playable`（zones design.md §8）。删除随派生一起失效的旧测试。

风险文件：`backend/internal/playback/httpapi/`、`platform/config`、README。回滚点：P-d 提交。

## P-g CUE 虚拟分轨（P-c 之后，与 P-d 并行；prd P12）

1. [ ] 后端 GPT：可渲染格式表放开 `cue_virtual`；来源段按 `cue_index_frames`/`cue_end_frames` 截取（打开的是整轨父文件，复用分析里的同一套定位）；同一整轨的相邻两轨按无缝条件拼接。
2. [ ] 测试 agent：相邻两轨无缝拼接后的 PCM 与整轨对应区间逐位相同（MD5）；起点不在 0 的单轨位置与时长正确。

## P-f 前端：信号路径与 Headroom 设置（zones F3 之后；Opus，一个子 agent 做完）

1. [ ] 前端 Opus：播放栏的信号路径按钮与弹出列表（design.md §9.2）；“设置 › 播放”加 Headroom 开关、增益滑块、削波指示开关（作用对象与 zones F3 的其他设置相同）；zones 客户端解码器加 `signal` 与 `settings.headroom`。文案遵守全局约束：不写说明句。
2. [ ] 测试 agent：信号路径按 `quality` 显示颜色、有损与兼容转换的行有标记、削波时图标变色；设置改动发出 `set_settings`；五种宽度不溢出、axe。

验证：前端三件套与相关组件测试。回滚点：P-f 提交。

## 完成标准

- P-a 有结论并已落到设计；P-b、P-c、P-h、P-e、P-d、P-f 各自的测试通过并经主会话验收、合入 `playback-baseline`。
- 本子项的 prd 验收由 E 统一核对（父 implement.md §2）。

# M2b 无损派生与 ALAC 播放合同

## 1. 范围与触发

M2b 让浏览器不能直接解码的 ALAC 曲目也能播放：服务端用 ffmpeg 把 ALAC 无损转换成 FLAC，核对解码后的 PCM 与源相同才发布，
缓存在受管目录里，之后按与直出相同的鉴权、来源核对、Range 与 10 秒复核交付。不做有损转码、码率档位、实时流式转码、
扫描后预生成与多实例共享缓存；CUE 精确分轨延后。项目不分发 ffmpeg，由部署者安装。

包与依赖方向：`backend/internal/playback/derivation`（缓存、任务、ffmpeg 适配、输出核对；不含 HTTP，不读数据库，不依赖任何内部包）；
`playback`（格式表、`Preflight` 与 `Prepare` 编排；依赖 `catalog`、`library`、`playback/derivation`）；`playback/httpapi`（传输）；
`app`（`OpenDerivation` 与装配）。通用的播放接口、安全打开与复核见 [播放合同](./playback-contracts.md)，本文只写派生新增与改变的部分。
设计与验证见 [M2b 任务](../../tasks/archive/2026-10/10-09-classical-m2b-derived-playback/design.md) 与
[ADR 0004](../../../docs/architecture/adr/0004-media-delivery.md)。

## 2. 配置与启动

| 环境变量 | 默认 | 含义 |
| --- | --- | --- |
| `ROOMUSIC_FFMPEG_PATH` | 空 | ffmpeg 的绝对路径；为空时在 `PATH` 中查找；相对路径使配置加载失败 |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | `10240` | 派生缓存上限（MiB，整数），最小 256；非整数或越界使配置加载失败 |

`config.Server` 增加 `FFmpegPath`、`MediaCacheMaxBytes`。默认值与下限只在 `platform/config` 定义一处，`derivation` 只要求上限为正。

`app.OpenDerivation(ctx, settings, logger)` 在 `app.New` 之前被 `main` 调用，只在数据库可用时调用：

1. 缓存目录是 `<ROOMUSIC_DATA_DIR>/media-cache`（权限 0700，子目录 `tmp/` 与 `v1/`）。创建任何目录**之前**先比较：对每个允许的音乐根与缓存目录
   取真实路径（符号链接已解析；尚不存在的末尾接在最深的已存在祖先之后），任一方包含另一方（含相等）即返回错误，`main` 记录
   `platform.config.invalid` 并退出。在音乐根里创建目录本身就违背“不写音乐目录”，所以不能先建后查。
2. `derivation.Open` 清空 `tmp/`，扫描 `v1/` 重建索引（键 → 大小、最近使用 = 文件修改时间；只认命名为 `<64 位小写十六进制>.flac` 的文件，
   其他文件忽略且不删除），总量超过上限时按最近使用淘汰。
3. 定位 ffmpeg，`-version` 取第一行作版本串（5 秒），再用内置的 `selftest.m4a`（0.25 秒 16 bit/44.1 kHz 立体声 ALAC，两个声道频率不同，
   `moov` 在文件末尾）跑一遍完整的派生与核对（30 秒）。任一步失败返回 `*UnavailableError{Reason}`，`Reason` 为
   `not_found|version_failed|selftest_failed|unsupported_platform`；`OpenDerivation` 记录一条 `playback.derivation.unavailable`
   （只含 `reason`，不含路径与 stderr）并返回 `(nil, nil)`，应用照常启动，ALAC 保持 `not_directly_playable`。
4. 可用时记录 `playback.derivation.ready`：`ffmpeg_version`、`max_bytes`、`entries`、`bytes`。
5. `app.WithDerivation(manager)` 装配；`main` 在 HTTP 停止、扫描结束之后调用 `Close(5s)`：取消排队与运行中的任务，终止 ffmpeg 进程组，
   删除临时文件。传给 `WithDerivation` 的接口值不得持有 nil 指针（`OpenDerivation` 返回 nil 就不要装配）。

推荐的安装方式：`scripts/ffmpeg-pinned.sh` 从按摘要固定的镜像（`mwader/static-ffmpeg` 7.1.1）取出静态 ffmpeg 到
`${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/ffmpeg-<摘要前 12 位>/ffmpeg` 并打印路径，测试、CI 与推荐部署使用同一个构建；也可用发行版的包。
升级镜像须重新核对素材与测试，并同步脚本里的摘要与版本前缀。

## 3. 接口合同

### 3.1 格式表

`GET /api/v1/playback/formats` 每项增加 `delivery`：`original`（直出原文件）或 `derived`（服务端无损转换）。派生项只在派生可用时出现：
`{ "source_kind": "m4a", "codec": "alac", "delivery": "derived", "mime_type": "audio/flac", "codecs": null }`；
`mime_type` 与 `codecs` 描述交给浏览器的表示（FLAC），不是源文件。直出六项不变，`delivery` 为 `original`。

### 3.2 预检（唯一启动或加入转换的入口）

`GET /api/v1/tracks/{id}/playback` 的检查顺序与直出相同。直出格式立即返回 200（`kind: "original"`）。派生格式通过源文件核对后：

| 情况 | 响应 |
| --- | --- |
| 缓存命中 | 200，`representation.kind` 为 `derived`，`url` 仍是 `/api/v1/tracks/<id>/audio`，`size_bytes` 是派生文件大小 |
| 未命中 | 启动或加入该键的任务并登记为等待者，最多等 20 秒（`playback.DefaultPreflightWait`）：完成 → 200；仍在进行 → 202 `{ "track_id", "status": "preparing" }`，任务继续运行，客户端立即再次预检 |
| 该键 10 分钟内失败过 | 409 `derivation_failed` |
| 上限已满、磁盘空间不足或正在关停 | 503 `derivation_busy` |

等待同时受请求与应用生命周期约束：客户端断开即注销等待者；应用生命周期结束时立即以 503 `derivation_busy`（`Retry-After: 2`）结束，
`server.Shutdown` 不会被 20 秒长轮询拖住。HEAD 预检同样会等待。

### 3.3 音频

`GET|HEAD /api/v1/tracks/{id}/audio` **从不启动转换**。检查顺序仍是 401 → 400 → 404 → 409（目录、格式、来源核对）→ 缓存 → Range（416）：

- 来源核对与直出相同（打开、核对大小/修改时间/容器后立即关闭源句柄），并要求扫描记录有内容指纹（没有则 409 `source_changed`，需重新扫描）；
- 缓存命中：`Content-Type: audio/flac`、`Cache-Control: private, no-store`、`X-Content-Type-Options: nosniff`、`Accept-Ranges: bytes`，
  强 ETag = `SHA-256("derived|"+曲目 ID+"|"+缓存键)` 前 32 个十六进制字符，与原文件的 ETag 不同；Range、If-Range、416、条件请求规则同直出；
  读取经 `NewRecheckingReader`，每 10 秒复核会话/成员与 `StillPlayable`；
- 未命中：409 `derivation_not_ready`（JSON），不启动转换。目录停用、来源缺失或变化后即使缓存仍在也拒绝。

### 3.4 新增错误码

| 情况 | 状态与错误码 | 文案 |
| --- | --- | --- |
| 音频请求时派生文件未就绪 | 409 `derivation_not_ready` | 转换尚未完成 |
| ffmpeg 失败、核对不一致、超时、输出超限 | 409 `derivation_failed` | 无损转换失败，无法播放这首曲目 |
| 并发、排队或单成员上限、剩余空间不足、关停 | 503 `derivation_busy`，`Retry-After: 2` | 服务器正忙于转换，请稍后重试 |
| 派生不可用时请求 ALAC | 409 `not_directly_playable` | 这首曲目不能直接播放 |

转换前的内容指纹不符、转换期间源文件被改写归入 `source_changed`/`source_unavailable`，不计入失败记忆。文案固定，不含路径、stderr 或类别。
`derivation_failed` 的类别（`ffmpeg_exit|verify_mismatch|timeout|output_limit|io`）只进日志。

## 4. 缓存键与布局

- 布局：`media-cache/v1/<key>.flac`（完成品）、`media-cache/tmp/<随机>.partial`（生成中）。完成品只经 `rename` 出现，所以任何可命中的文件都完整且已核对；
  所有文件操作限定在缓存目录之内（`os.Root`），只删除 `v1/` 下命名合法的条目与 `tmp/` 内的文件。
- 键：`SHA-256` 覆盖带长度前缀的 `roomusic-derivation-v1`、转换策略 `alac-flac-lossless-v1`、ffmpeg 版本串、内容指纹、源大小、
  修改时间（微秒，UTC）、来源类别与编码。不含曲目 ID：内容相同的两首曲目共用派生文件，每次请求仍各自鉴权与核对来源。改变键的组成或转换策略必须升版本号。
- 位深、采样率、声道只用于核对输出，不参与键。
- 索引在内存里，启动时由目录重建；最近使用时间持久化为文件修改时间（同一条目每小时最多写一次）。发布新条目后总量超过上限，
  按最近使用从旧到新淘汰（不删刚发布的条目）。正在传输的文件被淘汰时，已打开的句柄仍可读完，下次请求重新生成，所以不需要读者引用计数。
- 发布时 `fsync` 临时文件、`rename`、`fsync` 目录；目录同步失败要删除已发布的文件再报错，不留下孤儿。

## 5. 转换流程

任务**自己重新安全打开源文件**（`os.Root` + `O_RDONLY|O_NONBLOCK` + 对句柄核对大小、修改时间、容器），不借用请求的句柄或 context，
所以不受第一个请求生命周期的影响。回调由 `playback` 提供（`Source.Open`、`Source.Verify`），本包不知道曲目与权限。

1. 对句柄用 `library.ContentFingerprintOf` 重算内容指纹，与扫描记录不同则失败（`source_changed`，不记失败记忆）。
2. 预留配额 = 源大小 × 2 + 16 MiB；缓存目录所在文件系统剩余空间须 ≥ 预留量 + 1 GiB，否则 `derivation_busy`。
3. 第一遍：一次 ffmpeg 同时输出 FLAC 与“源解码后 PCM（`pcm_s32le`）的 MD5”。固定参数数组，不经 shell：只允许 `file` 协议，
   输入强制用 ALAC 解码器（不是 ALAC 即失败），只取第一路音频，丢弃元数据与章节，`-n` 不覆盖已有文件，`-fs` 限制输出，
   两侧 bitexact，`-threads 1`；源句柄作为 fd 3 传入并读 `file:/dev/fd/3`，所以路径在检查后被替换也读不到别的文件。
4. 核对输出：达到 `-fs` 上限即视为截断（`output_limit`）；`STREAMINFO` 的位深（只接受 16 或 24）、采样率、声道与扫描记录一致
   （记录缺失的项不比较），总样本数大于 0。
5. 第二遍：解码输出再取同一种 MD5，两个摘要必须相同，否则 `verify_mismatch`。摘要取 32 位整数，使 16/24 位的比较都在完整精度上进行。
6. 转换之后对句柄再核对一次大小与修改时间，转换期间被改写则失败；发布。

任一步失败或取消：终止整个进程组，删除临时文件，释放预留。进程管理：`Setpgid`，取消时向进程组发 SIGKILL，`WaitDelay` 2 秒，
环境变量只给 `LC_ALL=C`，工作目录为 `tmp/`，stderr 进 4 KiB 有界缓冲（只供测试与调试）。没有摘要行（例如输出已存在）按 `ffmpeg_exit` 处理。

## 6. 任务、等待者与上限

| 项 | 取值 |
| --- | --- |
| 预检等待 | 20 秒，到期返回 202，任务继续 |
| 并发转换 / 排队（先进先出） | 2 / 16；排队满返回 `derivation_busy` |
| 每个成员同时等待的不同键 | 3 |
| 最后一个等待者离开后的宽限期 | 10 秒，期间有新等待者则继续，否则取消并回收 ffmpeg |
| 单次转换超时 | 10 分钟（`timeout`） |
| 输出上限 | 源大小 × 2 + 16 MiB（`output_limit`） |
| 剩余空间下限 | 预留量 + 1 GiB |
| 失败记忆 | 10 分钟，最多 1024 条；期间同一个键直接返回 `derivation_failed` |
| 缓存上限 | 默认 10 GiB，最小 256 MiB |

管理器按键合并任务（同一时刻每个键最多一个）；每个预检请求在等待期间登记为等待者，完成、到期或客户端断开即注销。
任务上下文来自管理器的生命周期，不来自任何请求。日志只含曲目 ID 与安全字段，不含路径与 stderr：
`playback.derivation.completed`（`duration_ms`、`output_bytes`）、`playback.derivation.failed`（`category`）、
`playback.derivation.canceled`（`reason`：`no_waiters|shutdown`）。

## 7. 必需测试与断言

- 单元（不需要 ffmpeg）：`go test -race ./internal/playback/... ./internal/platform/config/ ./internal/app/ -run 'Test(Classify|Derived|Verify|Format|Key|Manager|Cache|Queued|OpenDerivation|Resolve|IsWithin|Load|PackageDependency)'`
  覆盖键、索引重建、淘汰、同键合并（假转换器与假时钟）、宽限期取消、上限、失败记忆、`STREAMINFO`、进程组回收（挂起的假 ffmpeg 脚本）、配置校验、缓存目录与音乐根的重叠检查。
- `TestFFmpeg…`（`ROOMUSIC_TEST_FFMPEG` 指向固定版本，未设置时跳过）：真实转换与 PCM 核对、自检、损坏输入、`-fs` 超限、不覆盖已有输出。
- `TestPostgreSQLPlayback…`（需要隔离数据库；真实 ffmpeg 用例同时需要 `ROOMUSIC_TEST_FFMPEG`）：格式表含派生项、202/200、状态码与检查顺序（转换之前不启动）、
  稳定错误码与 `Retry-After`、关停时长轮询结束、派生流在目录停用时被切断、真实 ffmpeg 下 ALAC 两组参数的 STREAMINFO 与 PCM 逐位相同、
  保留大小与修改时间的原地改写在转换前被拒绝。`TestPostgreSQLAPIResponseSamples` 用假派生记录样本（任何机器上相同）。
- `scripts/test-integration.sh` 总是通过 `scripts/ffmpeg-pinned.sh` 取出 ffmpeg 并运行 `TestPostgreSQL|TestFFmpeg`；取不到时失败，不静默跳过派生用例。
- `./scripts/playback-e2e.sh`：同一个 ffmpeg 经 `ROOMUSIC_FFMPEG_PATH` 交给隔离服务；ALAC 两组参数由真实 Chromium 解码 `audio/flac`、真实拖动、自然结束，
  专用探针文件验证音频请求从不启动转换；不使用真实 `music/`。
- 真实库验证只在宿主机装好 ffmpeg 后只读进行，输出汇总计数，不保存任何派生文件。

## 8. 错误与正确做法

错误：让音频请求顺手启动转换（切歌会堆积无用任务）；借用第一个请求的句柄或 context 跑转换；在检查之前就在音乐根里创建缓存目录；
把 ffmpeg 的 stderr 或路径写进响应与普通日志；用文件名而不是内容版本做缓存键；转换成功但不核对 PCM 就发布；
把派生缓存当作授权依据（命中缓存的请求同样要鉴权并核对来源）。

正确：预检是唯一入口，等待者就是正在等待的预检请求；任务自己打开源文件并对句柄取证；先核对路径再建目录；固定参数数组加进程组回收；
键包含内容版本、转换器版本与策略版本；两遍摘要与 `STREAMINFO` 都通过才 `rename` 发布；ffmpeg 不可用时降级为“不能直接播放”而不是启动失败。

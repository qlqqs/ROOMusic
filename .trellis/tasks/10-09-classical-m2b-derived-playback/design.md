# M2b 派生播放技术设计

需求与验收见 [prd.md](prd.md)，执行计划见 [implement.md](implement.md)。本文的取值（20 秒、10 秒、上限等）对应 PRD 的 B 系列验收与 K 系列决策。

## 1. 边界与包结构

### 后端

- 新包 `backend/internal/playback/derivation`：受管缓存、派生任务管理、ffmpeg 适配与输出核对。它不含 HTTP，不读数据库，不知道曲目与权限；
  输入是已经核对过的源文件句柄与派生参数，输出是缓存中的完整 FLAC。依赖规则：不依赖任何内部包。
- `backend/internal/playback`：保留直出用例，新增“派生格式”与编排：格式表的每项带 `Delivery`（`original`、`derived`）；
  `Service.Prepare` 对派生格式返回“已核对的源 + 派生计划”，再由 `derivation` 查缓存或生成。依赖规则改为 `catalog`、`library`、`playback/derivation`。
- `playback/httpapi`：预检长轮询与 202、音频接口对派生表示的分支、新错误码映射。依赖不变。
- `catalog.TrackSource` 增加 `BitDepth`、`SampleRate`、`Channels`（可空），供输出核对。
- `library` 增加 `ContentFingerprintOf(ctx, extension, io.ReaderAt, size)`，包装 `parser` 现有的按格式指纹函数，使调用方能对已打开的句柄重算指纹
  （`playback` 不能直接依赖 `parser`）。
- `platform/config` 增加 `FFmpegPath`、`MediaCacheMaxBytes`；`app` 负责定位 ffmpeg、自检、校验缓存目录并装配。

### 前端

- `features/player/api`：格式项的 `delivery`、派生表示、202 `preparing` 的解码。
- `engine/capability.ts`：派生格式按输出类型（`audio/flac`）询问浏览器。
- `engine/player-engine.ts`：新状态 `preparing`、预检轮询、预取下一首。
- `components/signal-path.tsx`、`player-labels.ts`：服务端环节与错误文案。

## 2. 配置与启动

| 环境变量 | 默认 | 含义 |
| --- | --- | --- |
| `ROOMUSIC_FFMPEG_PATH` | 空 | ffmpeg 可执行文件的绝对路径；为空时用 `exec.LookPath("ffmpeg")`；找不到则关闭派生 |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | `10240` | 派生缓存上限（MiB），最小 256；非法值使配置加载失败 |

启动顺序（`app.New` 内，在注册路由之前）：

1. 缓存目录为 `<ROOMUSIC_DATA_DIR>/media-cache`，权限 0700，子目录 `tmp/` 与 `v1/`。`MkdirAll` 后取真实路径，若位于任一允许的音乐根之内，
   或任一音乐根位于其内，**配置错误，启动失败**（ADR 0004 §3：派生文件只写入与音乐根隔离的受管目录）。
2. 清空 `tmp/`；遍历 `v1/` 重建索引（键 → 大小、最近使用时间 = 文件修改时间），只认可命名合法的 `<64 位十六进制>.flac`，其他文件忽略且不删除；
   总量超过上限时按最近使用淘汰。
3. 定位 ffmpeg，执行 `ffmpeg -hide_banner -version`（5 秒超时）取第一行作为版本串；再用内置自检素材（`derivation/selftest.m4a`，0.25 秒 16 bit/44.1 kHz
   立体声 ALAC，`go:embed`）跑一遍完整派生与核对，输出写到 `tmp/` 后删除。任一步失败：派生关闭，记录一条
   `playback.derivation.unavailable`，字段 `reason` 为 `not_found|version_failed|selftest_failed`，不含路径与 stderr；应用照常启动。
4. 派生开启时记录 `playback.derivation.ready`（版本串、缓存上限、已有条目数与字节数）。

## 3. 接口合同

### 3.1 `GET /api/v1/playback/formats`

每项增加 `delivery`：

```json
{ "items": [
  { "source_kind": "flac_vorbis_comment", "codec": "flac", "delivery": "original", "mime_type": "audio/flac", "codecs": null },
  { "source_kind": "m4a", "codec": "alac", "delivery": "derived", "mime_type": "audio/flac", "codecs": null }
] }
```

派生项只在派生开启时出现；`mime_type`、`codecs` 描述交给浏览器的表示（FLAC），不是源文件。

### 3.2 `GET /api/v1/tracks/{id}/playback`

检查顺序与 M2a 相同。派生格式通过源文件核对后：

| 情况 | 响应 |
| --- | --- |
| 缓存命中 | 200 `{ "track_id", "representation": { "kind": "derived", "url": "/api/v1/tracks/<id>/audio", "mime_type": "audio/flac", "codecs": null, "size_bytes" } }` |
| 未命中 | 启动或加入该键的任务，并以本请求登记为等待者；最多等 20 秒：完成同上 200；失败按 §3.4；仍在进行返回 202 `{ "track_id", "status": "preparing" }` |
| 该键 10 分钟内失败过 | 直接 409 `derivation_failed` |

直出格式的 200 响应不变（`kind: "original"`）。预检是唯一会启动转换的入口；前端收到 202 后立即再次预检（服务端负责等待，前端不需要退避）。

### 3.3 `GET|HEAD /api/v1/tracks/{id}/audio`

派生格式：鉴权 → 标识 → 曲目 → 目录 → 格式 → 源文件安全打开与大小/修改时间核对（与直出相同，确认来源仍有效）→ 关闭源句柄 → 查缓存：

- 命中：只读打开缓存文件（`os.Root` 限定在 `v1/` 内），`Content-Type: audio/flac`、`Cache-Control: private, no-store`、`X-Content-Type-Options: nosniff`、
  `Accept-Ranges: bytes`、强 ETag = `SHA-256("derived"|track_id|cache_key)` 前 32 个十六进制字符；Range/If-Range/416 规则与 M2a 相同；
  读取经 `NewRecheckingReader`，每 10 秒复核会话、成员与 `StillPlayable`。更新该条目的最近使用时间（同一条目每小时最多写一次文件修改时间）。
- 未命中：409 `derivation_not_ready`（JSON），不启动转换。

### 3.4 错误码

| 情况 | 状态与错误码 | 文案 |
| --- | --- | --- |
| 音频请求时派生文件未就绪 | 409 `derivation_not_ready` | 转换尚未完成 |
| ffmpeg 失败、核对不一致、超时、输出超限 | 409 `derivation_failed` | 无损转换失败，无法播放这首曲目 |
| 并发、排队或单用户上限，剩余空间不足 | 503 `derivation_busy`，`Retry-After: 2` | 服务器正忙于转换，请稍后重试 |
| 派生关闭时请求 ALAC | 409 `not_directly_playable`（与 M2a 相同） | 这首曲目不能直接播放 |

其余错误（401/400/404/`root_disabled`/`source_unavailable`/`source_changed`/416/503 `database_unavailable`）与 M2a 相同；转换前指纹不符归入 `source_changed`。

## 4. 派生流程

任务输入：根目录真实路径、相对路径、扫描记录（大小、修改时间、内容指纹、位深、采样率、声道）、缓存键。任务**自己重新安全打开源文件**
（`os.Root` + `O_RDONLY|O_NONBLOCK` + 对句柄核对大小/修改时间/容器，复用 M2a 的 `openVerified`），不借用第一个请求的句柄，因此不受该请求生命周期影响。

1. 用 `library.ContentFingerprintOf` 对句柄重算内容指纹，与扫描记录不同则失败为 `source_changed`（不记入失败记忆）。
2. 预留配额：预留量 = 源大小 × 2 + 16 MiB；检查缓存目录所在文件系统剩余空间 ≥ 预留量 + 1 GiB，否则 `derivation_busy`。配额账本在进程内由互斥锁保护。
3. 第一遍（转换并摘要源 PCM）：

   ```text
   ffmpeg -nostdin -hide_banner -loglevel error -xerror -threads 1
     -protocol_whitelist file -f mov -i file:/dev/fd/3
     -map 0:a:0 -map_metadata -1 -map_chapters -1 -c:a flac -compression_level 5 -fs <预留量> -f flac <tmp/随机名.partial>
     -map 0:a:0 -c:a pcm_s32le -f md5 pipe:1
   ```

   源句柄经 `ExtraFiles` 作为 fd 3 传入；Linux 上打开 `/dev/fd/3` 得到同一个 inode，即使路径在检查后被替换也读不到别的文件。stdin 为空，
   stdout 是 MD5 行（上限 128 字节），stderr 进有界缓冲（4 KiB，只用于测试与调试，不写普通日志）。
4. 第二遍（解码输出再摘要）：`ffmpeg -nostdin -hide_banner -loglevel error -xerror -threads 1 -protocol_whitelist file -f flac -i <partial> -map 0:a:0 -c:a pcm_s32le -f md5 pipe:1`。
   两遍摘要必须相同；按 32 位整数摘要使 16/24 位输出与源的比较都在完整精度上进行。
5. 读输出的 FLAC `STREAMINFO`：采样率、声道、位深与扫描记录相同（记录缺失的项不比较），位深只接受 16 或 24，总样本数大于 0。
6. 源句柄在第 4 步之后才关闭：第 1 步之后与第 4 步之后各对句柄 `Stat` 一次，大小与修改时间必须始终等于扫描记录，转换期间被改写则失败。
7. `fsync` 临时文件，`rename` 到 `v1/<key>.flac`，`fsync` 目录；登记索引，释放预留、计入实际大小；超过上限时按最近使用淘汰其他条目。

任一步失败或任务被取消：结束进程组，删除临时文件，释放预留。ffmpeg 退出码非零、核对不一致、超时、输出超限记入失败记忆（该键 10 分钟内直接返回 `derivation_failed`）。

**进程管理**：`exec.CommandContext`，不经 shell，参数为固定数组；`SysProcAttr.Setpgid = true`，`Cancel` 向整个进程组发 SIGKILL，`WaitDelay` 2 秒；
环境变量只给 `LC_ALL=C`；工作目录为 `tmp/`。单次任务上下文 10 分钟超时。

**缓存键**：`SHA-256` 覆盖 `roomusic-derivation-v1`、转换策略 `alac-flac-lossless-v1`、ffmpeg 版本串、内容指纹、源大小、修改时间（微秒）、来源类别与编码。
不含曲目 ID：内容相同的两首曲目共用派生文件，每次请求仍各自鉴权与核对来源（ADR 0004 §6）。

## 5. 任务、等待者与取消

- 管理器按键合并任务（同一时刻每个键最多一个任务）。每个预检请求在等待期间登记为该键的等待者，请求结束（完成、20 秒到期、客户端断开）即注销。
- 任务的上下文来自应用生命周期，不来自第一个请求；等待者降为 0 后计时 10 秒，期间有新等待者则继续，否则取消。
- 上限：同时运行 2 个（信号量），排队 16 个，每个用户同时等待的不同键最多 3 个。超出返回 `derivation_busy`，不排队。
- 关停：应用生命周期结束时取消全部任务，最多等 5 秒进程退出；残留临时文件由下次启动清理。
- 日志：`playback.derivation.completed`（曲目 ID、耗时、输出字节）、`playback.derivation.failed`（曲目 ID、类别
  `ffmpeg_exit|verify_mismatch|timeout|output_limit|io`）、`playback.derivation.canceled`（曲目 ID、`no_waiters|shutdown`）；都不含路径与 stderr。

## 6. 缓存

- 布局：`media-cache/v1/<key>.flac`（完成品）、`media-cache/tmp/<随机>.partial`（生成中）。完成品只经 `rename` 出现，因此任何可命中的文件都是完整且已核对的。
- 索引在内存中，启动时由目录重建；最近使用时间持久化为文件修改时间。
- 淘汰：发布新条目后，若总量超过上限，按最近使用从旧到新删除（不删刚发布的条目）。正在传输的文件被删除时，已打开的句柄仍可读完（POSIX 语义），
  下一次请求会重新生成，因此不需要读者引用计数。
- 版本隔离：ffmpeg 升级或转换策略变化会得到新键，旧条目不再被命中，随淘汰删除。
- 只删除 `v1/` 下命名合法的条目与 `tmp/` 内的文件，绝不递归清理数据目录或音乐根。

## 7. 前端

- **解码器**：格式项必须有 `delivery`；表示为 `kind: "original" | "derived"` 的联合，地址规则不变；预检按状态码解码：200 为表示，202 为
  `{ track_id, status: "preparing" }`。接口样本覆盖两种格式项、派生表示与 202。
- **能力判定**：派生项按 `typeString`（`audio/flac`）询问浏览器；可解码即可播放，不能解码为 `browser_cannot_decode`，不在表中仍为 `format_not_direct`。
  判定结果不区分直出与派生（真实环节在预检后由信号路径展示）。
- **引擎**：`PlayerStatus` 增加 `preparing`（文案“正在准备”）。`loadPlayback` 返回 `ready` 或 `preparing`；收到 `preparing` 时状态改为 `preparing`
  并立即再次预检，直到 `ready`、失败或被放弃（切歌、停止、会话结束会中止当前预检）；从第一次 202 起超过 11 分钟仍未就绪按失败处理（服务端 10 分钟超时之后）。
  音频请求失败时沿用 M2a 的“再次预检分类”：得到 202 就回到 `preparing`。
- **预取**：当前条目第一次进入 `playing` 后，取 `upNext(queue, 1)`；若下一首可播放且属于派生格式，发起一次预检（独立的 `AbortController`），
  结果不改变当前状态；当前条目变化、队列变化、停止或会话结束时中止；单曲循环不预取。
- **信号路径**：“服务端”环节按当前表示显示：原文件为“原文件直出”；派生为“无损转换：ALAC 解码后重新编码为 FLAC，位深、采样率与声道不变，
  发布前核对两者解码后的 PCM 一致；播放前仍核对来源与权限”。
- **文案**：`failureLabels` 增加 `derivation_failed`、`derivation_busy`；`derivation_busy` 可重试（与网络失败相同的重试入口）。

## 8. 测试

### 后端

- `derivation` 单元测试（假的转换器，不需要 ffmpeg）：键的组成与稳定性；索引重建（忽略非法文件名）；淘汰顺序与不删刚发布的条目；
  同键合并（并发 10 个等待者只运行一次）；最后一个等待者离开后 10 秒取消（可注入时钟与宽限期）；上限与 `derivation_busy`；失败记忆过期；
  预留与剩余空间判断；`STREAMINFO` 解析与核对。
- `TestFFmpeg…`（需要 `ROOMUSIC_TEST_FFMPEG`，未设置时跳过）：两组 ALAC 素材的真实派生与 PCM 核对；自检；损坏输入失败且无残留；
  `-fs` 超限失败；取消时进程组被回收（用会挂起的假 ffmpeg 脚本记录 PID）；源文件在转换中被改动则失败。
- `TestPostgreSQLPlayback…` 扩展（同时需要数据库与 ffmpeg）：格式表含派生项；预检 202/200 与同键只转换一次；音频命中 200/206/416 与 ETag；
  未命中 409；目录停用、来源缺失或变化后拒绝已缓存的派生；派生关闭时 ALAC 为 409 `not_directly_playable`；`derivation_busy`。
- 配置：上限解析与缓存目录位于音乐根内时启动失败。
- 接口样本：`formats.json` 更新，新增派生表示、202、三个新错误码的样本；前端解码器读取同一批样本。
- 依赖规则：`playback/derivation` 不依赖内部包；`playback` 新增该依赖。

### 前端

- Vitest：解码器（两种表示、202、拒绝错误地址）；能力判定（派生项、浏览器不能解码 FLAC）；引擎（`preparing` 循环、中止、超时、音频失败后回到 `preparing`、
  预取的触发与中止、单曲循环不预取）；信号路径两种文案；错误文案。
- Playwright（路由模拟）：ALAC 曲目可播放；准备中的播放栏状态；信号路径；五种宽度与 axe 不回退。

### 真实服务自动化

- 新脚本 `scripts/ffmpeg-pinned.sh`：从按摘要固定的镜像取出静态 ffmpeg 到 `${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/ffmpeg-<摘要前 12 位>/`，
  校验版本串后打印路径；已存在则直接打印。`scripts/test-integration.sh` 与 `scripts/playback-e2e.sh` 用它设置 `ROOMUSIC_TEST_FFMPEG`、`ROOMUSIC_FFMPEG_PATH`。
- `playback.real.ts`：ALAC 两组参数从“不能直接播放”改为派生播放、拖动与自然结束；断言音频响应为 `audio/flac` 且信号路径显示无损转换。
- CI：集成作业的测试模式加上 `TestFFmpeg`；真实播放作业已有 Docker，无需新增步骤。

## 9. 取舍

- **完整派生文件而非流式转码**：完成品有确定长度、强 ETag 与随机读取，直接复用 M2a 的 Range 与复核；代价是首播等待一次转换（实测 5 分钟曲目约 1 秒）。
- **预检长轮询而非任务接口**：不新增资源与持久化；等待者就是正在等待的预检请求，正好对应 ADR 0004 §6 的“最后一个等待者离开可取消生成”。
- **两遍核对**：比单遍多解码一次输出（实测约为转换耗时的三分之一），换来每个发布的派生文件都有 PCM 一致的证据。
- **转换前重算指纹**：每次生成多读一次源文件；命中缓存时不重算，保持与 M2a 相同的核对成本。
- **不分发 ffmpeg**：部署多一步安装，但项目不承担 GPL 构建的分发义务；固定版本脚本让测试、CI 与推荐部署使用同一构建。
- **缓存不进数据库**：目录即状态，重启后由文件重建；单实例足够，多实例协调另行设计。

## 10. 兼容与回退

- 开发阶段不做兼容：格式表、预检响应的形状变化与前端解码器同一提交更新。
- 没有迁移；回退时删除 `media-cache/` 即可，源文件与数据库不受影响。
- 关闭派生只需让 ffmpeg 不可用（或回退代码）：ALAC 回到“不能直接播放”，直出格式不受影响。

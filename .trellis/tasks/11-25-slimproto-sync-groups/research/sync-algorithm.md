# 调研：SlimProto 多台播放器同步机制

- **问题**：多台 Squeezelite 怎样同步播放同一条流，以及 ROOMusic 用 Go 实现同步的最小方案
- **范围**：内部源码阅读（一手资料只读）；没有做外部检索，论坛经验没有核实
- **日期**：2026-11-25
- **资料**：`temp/research-slimproto/` 下的克隆。下文用 `sq/` 指 `temp/research-slimproto/squeezelite/`（GPL-3），用 `lms/` 指 `temp/research-slimproto/slimserver/`（GPL-2）。只用来理解机制，笔记是转述，没有复制代码。

## 总览

LMS 的同步分三块，彼此独立：

1. **时钟映射**：每条 STAT 都带播放器的 `jiffies`（毫秒计数）。服务器用“收到时刻 − jiffies”的下包络来估计播放器时钟零点（jiffiesEpoch），不靠 `strm t` 往返。
2. **同步开始**：所有成员先以 autostart=0 拉流，只缓冲、不出声。全部发回 `STMl` 以后，服务器取“现在 + 200 ms + 各成员最大固定延迟”作为统一开始时刻，换算成每台播放器自己的 jiffies，用 `strm u` 下发。
3. **维持同步**：播放器约每秒上报一次 (jiffies, elapsed_ms)，服务器换算出“这首曲子在服务器时钟上的表观开始时刻”。每 0.95 s 比较一次各成员；落后超过 10 ms（Squeezebox2 系列默认值）时发 `strm a` 让落后者跳过对应毫秒数。播放器端不做重采样。

---

## 1. 时钟：jiffies 与服务器时钟的偏移

**结论**：LMS 不用往返时间测钟。每收到一条 STAT（任何事件都算），它就算一次 `offset = 服务器收到时刻 − jiffies/1000`，取这些 offset 的**最小值**作为 epoch，因为单向网络延迟只会让 offset 变大。为了跟上两边时钟的漂移，它还会把 epoch 慢慢往上调。`strm t` 里的时间戳字段已经弃用，固定发 0。网络延迟只用一个常数 `packetLatency`（默认 2 ms）粗略扣掉。

细节：

- 播放器侧：`jiffies` 就是 `gettime_ms()`，取自 `CLOCK_MONOTONIC` 的毫秒值，32 位，约 49.7 天回绕一次（`sq/utils.c:103-121`）。每条 STAT 在组包时取一次当前时间填进去（`sq/slimproto.c:160`、`sq/slimproto.c:186`）。
- `strm t`：Squeezelite 收到后立刻回一条 `STMt`，把 `strm` 里 replay_gain 字段的 4 个字节原样放进 STAT 的 `server_timestamp`。源码注释说这个字段已经不再用于测延迟，只是保留支持（`sq/slimproto.c:284-286`、`sq/slimproto.c:192`）。服务器发 `strm t` 时把这个字段固定填 0，注释给的理由是这种测延迟的办法太不可靠（`lms/Slim/Player/Squeezebox.pm:1099-1101`）。
- 采样来源与频率：
  - 播放器在 `OUTPUT_RUNNING` 状态下每超过 1000 ms 主动发一次 `STMt`（`sq/slimproto.c:734-737`）。它的状态循环每 100 ms 跑一轮（`sq/slimproto.c:619`）。
  - 服务器每 5 s 对每台播放器发一次 `strm t`，注释写明目的之一是初始化 epoch（`lms/Slim/Networking/Slimproto.pm:40`、`:198-239`）。
  - 所有事件 STAT（STMs/STMl/STMd 等）也参与估计。
- 估计算法（`lms/Slim/Player/Player.pm:927-990`，`trackJiffiesEpoch`）：
  - 收到时刻在 STAT 处理函数入口取（`lms/Slim/Networking/Slimproto.pm:695`），每条 STAT 都调用估计（`:779`）。
  - offset 小于当前 epoch 时，说明这次延迟更小，立即采用；offset 比 epoch 大 50 s 以上时，视为首次或回绕，直接重置（`Player.pm:944-955`）。
  - 每次记下 `diff = offset − epoch`，保留最近 50 个（`Player.pm:957-963`）。
  - 漂移跟踪：攒够至少 10 个，且这些 diff 的最小值仍大于 1 ms 时，说明播放器时钟相对服务器在变慢（或延迟整体变大），就把 epoch 上调这个最小值，并清空列表重新统计。上调量超过 5 ms 时，要等攒满 50 个才执行，防止被少量样本带偏（`Player.pm:965-985`）。
  - 换算：`jiffiesToTimestamp = epoch + jiffies/ticspersec − packetLatency`（`Player.pm:988-996`）。注释说明 packetLatency 只在换算时扣，不并入 epoch，这样一次算错的延迟不会永久带偏 epoch。Squeezebox 系列 `ticspersec = 1000`（`lms/Slim/Player/Squeezebox.pm:149-151`），`packetLatency` 默认 2 ms（`lms/Slim/Player/Player.pm:69`、`:923-925`）。
  - 重连时清空 epoch，避免长时间断线期间积累的漂移影响后续估计（`lms/Slim/Networking/Slimproto.pm:1219-1220`）。
- 下发开始时刻时，换算方向反过来：`jiffies = (服务器时刻 − epoch) × 1000`，这一步**不**扣 packetLatency（`lms/Slim/Player/Squeezebox2.pm:1095-1108`）。

含义：epoch 里含有“最小单向延迟”这个偏差。同一网段上各成员的偏差相近，所以对组内的相对同步影响不大。

## 2. 同步开始

**结论**：成员数大于 1 时，`strm s` 一律 autostart=0（只缓冲不出声）。服务器等**所有**成员都发回 `STMl`（buffer ready）后执行 `_syncStart`，计算 `startAt = 现在 + (max(startDelay+playDelay) + syncStartDelay[默认 200 ms])`，每台播放器的开始时刻再减去自己的 `startDelay+playDelay`，换成该播放器的 jiffies，用 `strm u` 下发。Squeezelite 在到点之前一直输出静音，到点后开始出声。

细节：

- 暂停式起播：成员数大于 1、且当前处于 STOPPED 或 BUFFERING 时，`paused=1`（`lms/Slim/Player/StreamingController.pm:1265-1269`），于是 `autostart=0`（`lms/Slim/Player/Squeezebox.pm:569-570`）。直连流再加 2（`Squeezebox.pm:796` 等处）。
- 缓冲阈值：`strm s` 的 threshold 字段以 KB 计，默认取偏好 `bufferThreshold=255`（`lms/Slim/Player/Player.pm:65`、`Squeezebox.pm:572`）。远程流按码率换算成约 `bufferSecs`（默认 3 s）的量，上限 255 KB（`Squeezebox.pm:160-181`）；小文件会调低（`Squeezebox.pm:917-921`）。
- 每台播放器各自建一条 HTTP 连接拉流，但内容是同一份：服务器从源读出一块数据后，把这块数据同时放进组内所有活动成员的发送队列（`lms/Slim/Player/Source.pm:150-155`）。任何一个成员的队列积压到 10 块，整组暂停读源（`Source.pm:103`、`:130-136`）。
- Squeezelite 侧：
  - 流缓冲收到的字节数超过 threshold 后，`STREAMING_BUFFERING` 转为 `STREAMING_HTTP`（`sq/stream.c:733-734`）。
  - 这时如果解码器处于 `DECODE_READY`（`codec_open` 时设置，`sq/decode.c:303`）：autostart=0 时，解码器开始运行，并发一次 `STMl`；autostart=1 时，解码器开始运行，输出进入 `OUTPUT_BUFFER`，等输出缓冲达到门限后自行起播（`sq/slimproto.c:668-680`、`:712-714`，`sq/output.c:62-67`）。
  - 所以 Squeezelite 的 `STMl` 表示“流缓冲到了阈值，解码已经开始”，不表示输出缓冲已满。服务器的 200 ms 余量也用来给解码留时间。
- 服务器事件路径：`STMl` → `bufferReady=1` → `playerBufferReady`（`lms/Slim/Player/Squeezebox2.pm:158-160`）。状态机在 BUFFERING 下执行 `_WaitToSync`，在 WAITING_TO_SYNC 下执行 `_StartIfReady`（`StreamingController.pm:189-195`）。`_StartIfReady` 逐个检查 `isBufferReady`，全部就绪才执行 `_Start` → `_syncStart`（`StreamingController.pm:1519-1534`、`:1403-1425`）。流已经读完（EndOfStream）时也会触发 `_Start`（`StreamingController.pm:211-212`）。
- 开始时刻计算：`StreamingController.pm:1487-1517`，取值见上面的结论。`syncStartDelay` 默认 200 ms，偏好说明里写的用途是保证每台播放器在开始前收到命令，并提到无线网络拥塞时可能不够（`lms/strings.txt` 的 `SETUP_SYNCSTARTDELAY_DESC`）。
- 换算与下发：`startAt` 发 `strm u`，replay_gain 字段放目标 jiffies（ms）（`lms/Slim/Player/Squeezebox2.pm:1095-1101`，`lms/Slim/Player/Squeezebox.pm:1090-1098`）。
- 固定延迟有两项，都是每台播放器单独的偏好，默认 0（`lms/Slim/Player/Player.pm:70-71`）：
  - `startDelay`：播放器收到开始命令后，要过一段时间才真正出声（数字输出、某些软件播放器）。只在起播时计入。
  - `playDelay`：播放器上报的进度与实际听到的声音之间的持续差。起播和维持同步时都计入（`StreamingController.pm:507-512`）。
  - 说明见 `strings.txt` 的 `SETUP_STARTDELAY_DESC`、`SETUP_PLAYDELAY_DESC`。`packetLatency` 只在 jiffies 换成服务器时刻时用到（见第 1 节）。
- Squeezelite 收到 `strm u` 后回 `STMr`（`sq/slimproto.c:332-343`）。组内第一首曲子的 `STMs` 被服务器忽略，开始由 unpause 驱动（`StreamingController.pm:2256-2259`）。

## 3. 播放点

**结论**：播放点是一个三元组：`statusTime = jiffiesToTimestamp(jiffies)`、`apparentStreamStartTime = statusTime − elapsed_ms/1000`，以及 songElapsed。“这台播放器此刻在流里的位置”等于“服务器当前时刻 − apparentStreamStartTime”，比较各成员时只需比较 apparentStreamStartTime。AccuratePlayPoints=1 表示播放器报上来的 elapsed 已经扣掉输出设备里还没播出的帧，并按上报时刻做了外推，服务器可以直接用单个样本，不必平均。

细节：

- Squeezelite 怎么算 elapsed（`sq/slimproto.c:160-172`）：
  - 先算 `(frames_played − device_frames) / 采样率`，再加上从最近一次测量设备延迟到现在的毫秒数，作为 `elapsed_milliseconds`；`elapsed_seconds` 取它的整秒部分（`:189-191`）。
  - `device_frames` 由 ALSA 的 `snd_pcm_delay` 在每次写设备前测得，同时记下测量时刻 `updated` 和当时的 `frames_played`（`sq/output_alsa.c:831-851`）。
  - `frames_played` 在每首曲子开始时清零（`sq/output.c:154-156`）。
  - 因此 elapsed 指的是“jiffies 这一刻从 DAC 出来的位置”，前提是 ALSA 报的延迟准确。
- HELO 能力串：Squeezelite 固定带 `Model=squeezelite,AccuratePlayPoints=1,...`，设备号 12（squeezeplay）（`sq/slimproto.c:123`、`:141`）。服务器把它当成 SqueezePlay 类，这个类继承 Squeezebox2（`lms/Slim/Player/SqueezePlay.pm:24-27`、`:84`）。
- AccuratePlayPoints=1 时，`playPoint()` 走 Squeezebox2 的实现，用最近一条 STAT 直接算三元组（`SqueezePlay.pm:287-295`，`Squeezebox2.pm:1074-1093`）。
- AccuratePlayPoints=0 时，走加权播放点：只在同步且播放中计算（`lms/Slim/Networking/Slimproto.pm:865-876`）。保留最近 8 个 apparentStart，最新一个与平均值相差不到 15 ms 才发布平均值（`lms/Slim/Player/Player.pm:997-1027`）。
- songElapsed 的取法：毫秒值可能被截断，所以当毫秒换算出的秒数小于 elapsed_seconds 时，用 elapsed_seconds 加毫秒的余数（`Squeezebox2.pm:1084-1087`）。

## 4. 维持同步

**结论**：

- **触发**：只有主播放器的普通心跳 STAT（实际上主要是 `STMt`）会触发检查，两次检查至少间隔 0.95 s。
- **数据**：每个成员都要有 3 s 内的播放点，否则这一轮放弃。统计量就是每个成员最新的一个点（accurate 类），不做平均或中位数。
- **基准**：按 apparentStart 排序后取基准播放器。Squeezelite 都支持 skipAhead，所以基准实际上是**最超前**的那台。
- **修正**：其余成员落后多少就发 `strm a` 跳过多少毫秒。只修正偏差在 [max(10 ms, minSyncAdjust), 10 s] 范围内的成员。每次跳过后，下一次检查推迟 1 s。
- **暂停修正**：`strm p`（暂停 N 毫秒）只用于比“不能 skip 的基准”更超前的成员（老设备才会出现这种情况）。

细节（`lms/Slim/Player/StreamingController.pm:480-583`，`_CheckSync`）：

- 常量：检查间隔 0.950 s；最小修正 10 ms；最大修正 10 s，超出不修；播放点新鲜度 3 s（`StreamingController.pm:480-483`）。
- 只在 PLAYING 状态下由 StatusHeartbeat 事件触发（`StreamingController.pm:237-243`）。只处理主播放器的心跳（`StreamingController.pm:2363-2369`）。STMt 以及其他没有专门处理的事件码会归到心跳（`lms/Slim/Player/Squeezebox2.pm:177-179`）。
- 每个成员取 `apparentStart + playDelay`。关闭了 `maintainSync` 的成员不参与（`StreamingController.pm:498-520`）。
- 按 apparentStart **从大到小**排序，排在最前面的最落后（`:536`）。
- 找基准：从最落后的开始往前找第一台**不能** skipAhead 的；如果都能 skip，循环停在最后一台，也就是最超前的那台（`:543-548`）。
- 对每个非基准成员：比基准落后的，`skipAhead(delta)`，下一次检查推迟 1 s；比基准超前的，如果能 `pauseForInterval`，就暂停 delta，下一次检查推迟 delta（`:555-582`）。
- 最小修正量 `minSyncAdjust`：Squeezebox2 系列（含 Squeezelite）默认 10 ms，基类默认 30 ms（`lms/Slim/Player/Squeezebox2.pm:51`、`:64-71`，`lms/Slim/Player/Player.pm:68`）。设置页滑块范围 5–1000 ms（`lms/HTML/EN/settings/player/synchronization.html:55`）。
- 下发：`pauseForInterval` 发 `strm p`，`skipAhead` 发 `strm a`，replay_gain 字段放 `int(秒 × 1000)` 毫秒（`lms/Slim/Player/Squeezebox2.pm:1112-1126`，`lms/Slim/Player/Squeezebox.pm:1090-1098`）。
- 恢复播放（resume）之后，第一次检查推迟到开始时刻之后 2 s（`StreamingController.pm:1650`）。

## 5. 播放器侧执行（Squeezelite）

**结论**：

- `strm u <jiffies>`：进入 `OUTPUT_START_AT`，到点前持续写静音，到点后转为 RUNNING。
- `strm p <ms>`：插入 `ms × 采样率 / 1000` 帧静音，期间 frames_played 不增加。
- `strm a <ms>`：从输出缓冲里一次丢掉同样多的帧，frames_played 照样累加。
- 精度：帧数按毫秒换算，是整数帧；时间判断以毫秒为单位，靠写设备的循环推进，所以起播对齐精度受这个循环的粒度影响（ALSA 下一次最多写一个 period）。
- **没有任何重采样或调速去跟随主时钟**。`resample.c` 只做固定采样率转换。时钟漂移完全靠第 4 节的跳帧修正。

细节：

- `strm u`（`sq/slimproto.c:332-343`）：jiffies 为 0 时立即 RUNNING，否则进入 START_AT 并回 `STMr`。
- START_AT 的处理（`sq/output.c:98-109`）：
  - 当前毫秒时间已经到点，或者目标比现在晚 10 s 以上（视为无效值），就转 RUNNING。
  - 否则写 `min(剩余毫秒对应的帧数, 本次可写帧数, 2048)` 帧静音（`MAX_SILENCE_FRAMES=2048`，`sq/squeezelite.h:446`）。
  - 这里比较的是“写入设备的时刻”，不是“听到的时刻”。设备缓冲里的延迟由第 3 节的 `device_frames` 计入 elapsed，起播后由服务器的维持同步修正。
  - 比较用的是 32 位无符号毫秒，jiffies 回绕点附近会出错（实际极少碰到）。
- `strm p`（`sq/slimproto.c:306-320`、`sq/output.c:85-96`）：
  - interval 不为 0 时进入 PAUSE_FRAMES，分多次写静音，每次最多 2048 帧，直到写完。
  - interval 为 0 时转为 STOPPED（真正暂停），并回 `STMp`。
- `strm a`（`sq/slimproto.c:322-330`、`sq/output.c:69-83`）：
  - 只能丢掉输出缓冲里**现有**的帧；不够时只丢现有的，然后直接回到 RUNNING。
  - 已经写进设备缓冲的帧不受影响。所以跳帧在一个设备延迟之后才听得出来，而 elapsed 会立即增加。
- 输出缓冲默认 `44100×8×10` 字节，约 44.1 kHz 下 10 s（`sq/squeezelite.h:266`）。
- ALSA 默认 buffer 40 ms、4 个 period（`sq/squeezelite.h:272-273`）。写设备循环每次最多写一个 period（writei 模式，`sq/output_alsa.c:795-800`），设备满时等待（`:762-791`）。
- 曲间换采样率可以插入 `rate_delay` 静音，前后各一半（`sq/output.c:130-152`）。同组成员的设置不一致时，会引入新的偏差，之后由维持同步修正。
- 起播精度估计：毫秒取整带来 ≤1 ms 误差；剩下是写设备循环醒来的抖动和 ALSA 延迟测量误差。源码里没有给出精度数字，需要实测。

## 6. 暂停、继续、跳转、换曲、掉线与欠载

**结论**：

- 暂停：先淡出，再对所有成员发 `strm p 0`。
- 继续：用“各成员暂停时的 elapsed − 组的暂停位置”给超前的成员推迟开始时刻，再统一 `strm u <jiffies>`。
- 跳转：停掉整组，再按同步开始流程重来。
- 无缝换曲：要等**所有**成员都报 `STMd`（解码完当前流），才给每台播放器发下一首的 `strm s`（autostart=1）。下一首接在输出缓冲后面，连续播放，不需要重新同步。
- 一台掉线：把它从活动成员里移除，其他成员继续播放。
- 一台欠载（`STMo`）：整组暂停，重新缓冲，再同步继续。

细节：

- 暂停 `_Pause`（`lms/Slim/Player/StreamingController.pm:1552-1589`）：记下组的 elapsed 作为 resumeTime。所有成员一起淡出，主播放器淡出完成的回调里对全体发 `pause`（`strm p` 不带时长）。
- 继续 `_Resume`（`StreamingController.pm:1620-1657`）：
  - 基准时刻 = 现在 + syncStartDelay。
  - 每个成员：`delay = 该成员播放点里的 songElapsed − pausedAt`。delay ≥ 0 时开始时刻加上 delay，小于 0 的不处理。然后发 `strm u <jiffies>`（`lms/Slim/Player/Squeezebox2.pm:1104-1110`）。
  - 继续时不计入 startDelay/playDelay，这与 `_syncStart` 不同。
- 跳转 `_JumpToTime`（`StreamingController.pm:1092-1142`）：`_Stop` 后 `_Stream` 带 seekdata。这时状态已经是 STOPPED，于是又走 paused 起播 → STMl → `_syncStart`。
- 成员加入正在播放的组 `sync`/`playerActive`：以当前 elapsed 对**整组** `_JumpToTime`，所有人短暂中断后重新同步开始（`StreamingController.pm:1955-1960`、`:2080-2083`）。
- 无缝换曲：
  - `ReadyToStream`（STMd）在 PLAYING 下走 `_StreamIfReady`，逐个检查 `isReadyToStream`，全部为真才 `_Stream`（`StreamingController.pm:216-222`、`:1386-1401`）。
  - PLAYING 状态下 `paused=0`，所以 autostart=1（`StreamingController.pm:1265-1269`）。
  - 曲目切换以主播放器的 `STMs` 为准（`:2250-2266`）。
  - 曲目边界上，各成员的 elapsed 清零时刻有先后。这时两台的 apparentStart 会相差一首曲子的长度，超过 10 s 上限，不会触发修正（`:480-483`、`:558-561`）。
- 欠载：
  - Squeezelite 在输出缓冲为 0 且流还在传输时发 `STMo`，状态仍是 RUNNING，继续写静音（`sq/slimproto.c:725-729`、`sq/output.c:111-115`）。
  - 服务器 OutputUnderrun → `_Rebuffer`：全体暂停，进入 rebuffering，记下 resumeTime（`StreamingController.pm:230-236`、`:1659-1672`）。
  - 每台播放器在定时器里检查缓冲量是否达标，达标后报 bufferReady（`lms/Slim/Player/Player.pm:1035-1096`、`:1177-1181`）。全部就绪后走 `_Start` → `_Resume`（`StreamingController.pm:1403-1425`）。
  - 解码已经结束、输出也放空时发的 `STMu` 表示正常播完，只处理主播放器的（`sq/slimproto.c:716-722`，`StreamingController.pm:2223-2248`）。
- 掉线：
  - 15 s（3 个 5 s 轮询周期）没收到 STAT 就关闭连接（`lms/Slim/Networking/Slimproto.pm:217-231`）。
  - 关闭时如果它不是唯一的活动成员，就 `playerInactive`：只停它自己，从活动列表里移除；如果它是主播放器，重新选主（`Slimproto.pm:265-276`，`StreamingController.pm:2086-2122`）。
  - 选主规则就是活动列表里的第一个（`StreamingController.pm:2383-2392`）。
  - 重连后 `playerActive` → 整组跳转重启（`lms/Slim/Player/Squeezebox.pm:84-98`）。
  - 隐患：掉线成员在被判定掉线之前（最长 15 s），会因为 10 块的发送积压上限让整组停止读源（`lms/Slim/Player/Source.pm:130-136`）。同步开始阶段也要等所有成员的 STMl（`StreamingController.pm:1519-1534`），没有超时。

## 7. 不同能力的播放器同组

**结论**：全组共用**同一条流、同一种格式、同一个采样率**。格式取所有活动成员都支持的格式，并按发起播放的播放器的偏好顺序排列。采样率上限取全组 `MaxSampleRate` 的最小值，超出就要求转码降采样。

细节：

- 格式取交集（`lms/Slim/Player/CapabilitiesHelper.pm:38-58`，`supportedFormats`），用于转码选择（`lms/Slim/Player/TranscodingHelper.pm:345-351`）和直接播放的检查（`lms/Slim/Player/Song.pm:449-452`）。
- 采样率取全组最小值（`CapabilitiesHelper.pm:12-36`）。源是 44.1k 系列、上限是 48k 系列时，按 11025/12000 的比例换成同系列的最大值。超出上限时：转码路径加降采样要求 'D'（`TranscodingHelper.pm:342-343`）；不转码的直接播放报 `UNSUPPORTED_SAMPLE_RATE`（`Song.pm:453-456`）。
- 上限来自 HELO 能力串：Squeezelite 发 `MaxSampleRate=`（`sq/slimproto.c:883`），服务器映射到 `maxSupportedSamplerate`（`lms/Slim/Player/SqueezePlay.pm:83`）。
- 数据是同一份字节广播给各成员（第 2 节 `Source.pm:150-155`），所以不存在“每台按自己的能力单独转码”。源码里有 TODO 说直连判断还没逐台做（`Song.pm:474`）。
- 可以同组的设备：除自己以外，所有 slimproto 播放器里身为主播放器或没有同步的那些（`lms/Slim/Player/Sync.pm:98-121`），没有按型号限制。

## 8. 已知限制与误差量级

**结论**：从源码常量和说明来看，SlimProto 的同步是**毫秒级**的，不是样本级：

- 时间戳分辨率 1 ms。
- 默认允许偏差 10 ms（Squeezelite 一类）或 30 ms（基类），不到这个量不修正。
- 说明文字认为超过约 30 ms 就听得出来。
- 修正方式是跳帧或插静音，有可闻的不连续点。
- 漂移没有连续补偿。

PRD 写的“样本级同步”用这套协议做不到，需要调整目标表述。

依据与估算：

- `SETUP_MAINTAINSYNC_DESC`：几分钟后会漂开，超过约 30 ms 可闻（`lms/strings.txt`）。最小修正 10 ms / 30 ms（第 4 节）。加权播放点允许的波动是 15 ms（`lms/Slim/Player/Player.pm:999`）。
- 分辨率：jiffies、`strm u/p/a` 的参数都是毫秒（`sq/slimproto.c:306-343`）。
- 网络：
  - `SETUP_PACKETLATENCY_DESC` 承认自动测得的延迟有时太不稳定，需要手填。
  - `SETUP_SYNCSTARTDELAY_DESC` 说播放器多或者用无线网络时，200 ms 可能不够。
  - 估计器在样本里取最小值，所以 Wi-Fi 的随机抖动只会让收敛变慢。真正的风险是**非对称或持续**的延迟（省电轮询、拥塞），它们会让 epoch 偏离真实值。
- 漂移量级（我的估算，没有在源码中出现）：两块 DAC 晶振相差 20–100 ppm，每小时差 72–360 ms。按 10 ms 阈值算，每 100 s 到 8 min 会触发一次跳帧。
- 能力上限：Squeezelite 的 elapsed 取决于 ALSA `snd_pcm_delay` 是否准确。USB DAC、HDMI、功放内部的额外延迟需要手工填 `playDelay`/`startDelay`。
- 论坛上关于同步精度的经验说法：这次**没有做外部检索，没有核实**。

---

## ROOMusic 方案建议（Go，最小可行）

前提沿用 `11-25-slimproto-squeezelite` 的决定 S4：每个播放区输出一条连续的“长流”（FLAC 或 PCM）。这样同步组内就不存在逐曲 `strm s`，曲目边界上的 elapsed 清零问题也不会出现。

### A. 时钟估计（每台播放器一个估计器）

- 在 TCP 读循环里，一读到 STAT 帧头就记下收到时刻，用单调钟（Go 的 `time.Now()` 带单调读数，统一换成相对进程基准的纳秒）。不要等解析完、拿到锁以后再取时间。
- 先把 32 位 jiffies 展开成 64 位，处理回绕。
- 计算 `off = recv − jiffies_ms`，做下包络估计：
  - 新的 off 比当前 epoch 小，就立即采用。
  - 保留最近 N 个（例如 50 个）`off − epoch`。至少攒够 M 个（例如 10 个）、且最小值仍大于 1 ms 时，把 epoch 上调这个最小值。上调超过 5 ms 时，要求攒满 N 个。
  - 以上沿用 LMS 的结构（第 1 节）。
- 可选增强：`strm t` 的 4 字节字段由我方填入服务器毫秒时间戳，Squeezelite 会原样回传（`sq/slimproto.c:284-286`）。用它计算 RTT，只把 RTT 最小的那批样本当作好样本，或者只用来监控链路质量。Squeezelite 收到就回，LMS 弃用这个办法时说的“不可靠”可能针对老固件，需要实测再决定。
- 采样频率：Squeezelite 播放时每秒会自己发 STMt。不播放时它不主动上报，所以我方在成员进入组或准备起播时，要以较高频率（例如 250–500 ms 一次）发 `strm t`，热身几秒；平时 1–5 s 一次。
- 重连和 HELO 时重置估计器。
- 需实测：N、M、上调阈值、热身时长、RTT 过滤是否值得做。

### B. 同步开始（起播、跳转、继续都走这里）

1. 对组内每个成员发 `strm s`：autostart=0，格式和 URL 相同，threshold 按长流码率换算成约 1–3 s 的数据量，上限 255 KB。服务器把**同一份**编码后的字节分发给各成员的 HTTP 连接。
2. 每个成员的发送缓冲**各自有上限**。某个成员的积压超过上限时，单独把它踢出组，不让它像 LMS 那样卡住整组读源（第 6 节）。这也符合审查清单 C5、C6。
3. 等所有成员回 `STMl`，设一个超时（例如 5–10 s，需实测）。超时没回的成员移出本次起播，其余成员照常开始。
4. 统一开始时刻 `T = now + syncStartDelay + max_i(startDelay_i + playDelay_i)`。每个成员下发 `jiffies_i = T − (startDelay_i + playDelay_i) − epoch_i`，用 `strm u` 发出，换算成 ms 取整。
   - 要保证 `jiffies_i ≠ 0`，且 `T − now < 10 s`，否则 Squeezelite 会立即开始（`sq/output.c:100`）。
   - syncStartDelay 默认 200 ms，Wi-Fi 下可能要 300–500 ms，需实测。
5. 继续播放：可以用 LMS 的做法。暂停后，各成员 `STMp` 之后的 STAT 里有精确的 elapsed。取最小值 `e_min`，每台的开始时刻加 `e_i − e_min`。也可以更简单：把继续当作从 `e_min` 处跳转（`strm q` 后重新 B 流程），慢一些但不会错。MVP 推荐后者，先求正确。

### C. 维持同步

- 每条 STAT 更新该成员的播放点：`apparent_i = (epoch_i + jiffies − packetLatency) − elapsed_ms − playDelay_i`。
- 每 ~1 s 检查一次，由定时器驱动，不依赖某个主播放器的心跳。
  - 要求所有成员的播放点都在 3 s 以内，否则跳过这一轮。
  - 基准取**最超前**的成员，也就是 apparent 最小的那个。
  - 对落后 delta 毫秒的成员，delta ∈ [minAdjust, 10 s] 时发 `strm a <delta>`，此后这个成员 1 s 内不再修正。
  - 偏差超过 10 s 的成员视为异常，走重新入组（下文 D）。
- 防抖：Wi-Fi 下可以用每个成员最近 3–5 个点的中位数代替最新点（需实测是否需要）。连续两轮都超过阈值才修正，也可以作为选项。
- 需实测：minAdjust（Squeezelite 默认 10 ms；有线可以试 5 ms）、检查间隔、新鲜度窗口，以及跳帧在实际听感上的可接受频率。

### D. 掉线、欠载与入组

- 心跳：播放中 3–5 s 收不到 STAT，或者 TCP 断开，就把成员移出活动组，其他成员不受影响（比 LMS 的 15 s 短，需实测误判率）。
- 欠载：
  - LMS 的做法是整组暂停、重新缓冲、再同步继续（第 6 节）。
  - 按 ROOMusic 的故障隔离原则，建议**只处理欠载的那台**：它会落后，偏差不超过上限时由 C 的跳帧追上；超过上限或连续欠载，就把它单独移出，再按“入组”重新接入。
  - 播放区本身的渲染不暂停。
- 入组和重连：
  - MVP 沿用 LMS，以当前位置对整组跳转重启，实现简单，会有一次全组中断。
  - 后续可以做不打断的入组：ROOMusic 自己渲染长流，能从精确样本位置开始给新成员供流。服务器预测“组在某个时刻 T 的位置 P”，给新成员从 P 开始的流（autostart=0），等它 STMl 后按 T 发 `strm u`，再由 C 收敛。
- 采样率与格式：组内取交集，采样率取最小 `MaxSampleRate`（第 7 节），由组的渲染链统一输出。一台低能力设备会拉低整组，这一点需要在 UI 或文档里说明。

### E. 在 Docker 里用两台无声卡 Squeezelite 验证

**各输出方式是否按实时节奏输出（从源码判断）**：

- `-o -`（标准输出）：
  - **不自带节奏**。输出线程循环调用 `_output_frames(2048)`，然后 `fwrite` 到 stdout，`device_frames` 恒为 0，`updated` 取写入前的时刻（`sq/output_stdout.c:105-124`）。
  - 写到 `/dev/null` 会全速空转；节奏完全取决于读管道的一方。
  - 管道本身的缓冲（默认 64 KiB，约 186 ms@44.1k/S32 立体声）不计入 elapsed。两台配置相同时这部分延迟相互抵消。
- ALSA `null` 设备：源码注释说 alsa null 这类设备会拒收数据，于是写入 0 帧，休眠 10 ms 再试（`sq/output_alsa.c:856-861`）。按这个判断，它**不能**提供按实时推进的播放时钟，不适合测同步。
- `snd-dummy`、`snd-aloop` 内核模块：这两个是按实时推进的虚拟声卡，但要在宿主机加载内核模块，属于系统改动，需要先取得授权。不建议作为默认方案。
- PulseAudio null sink：按实时推进，但需要 Squeezelite 的 Pulse 构建（`sq/Makefile.pulse`）并在容器里跑 pulse，依赖较重。

**推荐做法**：每个容器里运行 `squeezelite -o - -a 16 ... | pacedsink`。`pacedsink` 是测试工程自带的小程序（Go）：

- 按标称采样率、以单调钟节拍读 stdin，模拟 DAC。
- 可以配置 ±ppm 偏差，模拟两块晶振的漂移。
- 可以用 `F_SETPIPE_SZ` 把 stdin 管道缩小，降低不计入 elapsed 的延迟。
- 测试音源用合成数据：例如每秒一个脉冲。`pacedsink` 检测到脉冲时，记下这一帧在流中的序号和读出时刻（单调钟）。
- 同一宿主机上的容器共用内核 `CLOCK_MONOTONIC`，两个容器的记录可以直接相减，得到两台之间的偏差，精度远高于 1 ms。
- 用脉冲检测而不是逐样本比较，是因为音量和增益会改变样本值。

**测试矩阵**：

1. 同时起播的初始偏差。
2. 一台 +100 ppm 时，长时间运行的偏差曲线和跳帧次数。
3. 用 `tc netem` 给一个容器加 5±2 ms 延迟抖动（需要容器有 `NET_ADMIN` 权限，不改宿主机），检查估计器和起播。
4. 杀掉或冻结一台容器（`docker pause`），检查另一台不受影响、心跳超时后被移出。
5. 暂停、继续、跳转后的偏差。

每次测试用独立的 Compose 项目名和临时目录，用合成音频，符合 AGENTS.md。

**需实测确定的参数汇总**：估计器窗口和上调阈值、热身时长、`strm t` 轮询频率、syncStartDelay、STMl 等待超时、minAdjust、检查间隔、播放点新鲜度、心跳超时、每台设备的 playDelay/startDelay，以及 Wi-Fi 下是否需要中位数防抖。

## 文件索引

| 文件 | 内容 |
|---|---|
| `temp/research-slimproto/squeezelite/slimproto.c` | STAT 组包与 elapsed 计算（160-200）、strm t/q/f/p/a/u/s（284-399）、状态循环与事件上报（615-765）、HELO 能力（123-155、883） |
| `temp/research-slimproto/squeezelite/output.c` | START_AT、PAUSE_FRAMES、SKIP_FRAMES、起播门限、曲目开始（62-160） |
| `temp/research-slimproto/squeezelite/output_alsa.c` | 写设备循环、`snd_pcm_delay` 测延迟（745-862） |
| `temp/research-slimproto/squeezelite/output_stdout.c` | stdout 输出，没有节奏、device_frames=0（105-124） |
| `temp/research-slimproto/squeezelite/stream.c`、`decode.c` | 缓冲阈值转换（733-734）、DECODE_READY（303） |
| `temp/research-slimproto/slimserver/Slim/Player/Player.pm` | jiffies epoch 估计（927-996）、加权播放点（997-1027）、同步相关偏好默认值（61-71）、重新缓冲（1035-1181） |
| `temp/research-slimproto/slimserver/Slim/Player/StreamingController.pm` | 状态表（102-245）、`_CheckSync` 及常量（480-583）、`_Stream`（1144-1357）、`_syncStart`（1487-1517）、`_StartIfReady`（1519-1534）、暂停与继续（1552-1657）、`_Rebuffer`（1659-1672）、入组、退组与掉线（1900-2134）、事件入口（2223-2369） |
| `temp/research-slimproto/slimserver/Slim/Player/Squeezebox2.pm` | 事件码到控制器的映射（140-180）、playPoint、startAt、resume、pauseForInterval、skipAhead（1074-1126） |
| `temp/research-slimproto/slimserver/Slim/Player/Squeezebox.pm` | strm s 参数（505-1074）、其他 strm 命令与 `t` 时间戳弃用（1078-1124） |
| `temp/research-slimproto/slimserver/Slim/Player/SqueezePlay.pm` | 能力映射、AccuratePlayPoints（79-97、287-305） |
| `temp/research-slimproto/slimserver/Slim/Networking/Slimproto.pm` | 心跳轮询与超时（40、198-239）、STAT 解析（691-879）、重连清空 epoch（1219-1220） |
| `temp/research-slimproto/slimserver/Slim/Player/Source.pm` | 同组数据分发与积压上限（103-160） |
| `temp/research-slimproto/slimserver/Slim/Player/CapabilitiesHelper.pm` | 格式交集、采样率上限（12-58） |
| `temp/research-slimproto/slimserver/strings.txt` | 同步偏好说明（`SETUP_*DELAY*_DESC`、`SETUP_MAINTAINSYNC_DESC`、`SETUP_MINSYNCADJUST_DESC`、`SETUP_PACKETLATENCY_DESC`） |

## 注意 / 未查到

- 没有做外部检索（本次可用工具里没有网页搜索），论坛上的同步精度经验没有核实。
- ALSA `null` 是否按实时推进，只依据 Squeezelite 源码注释判断，没有查 alsa-lib 源码。
- 第 5 节的起播精度、第 8 节的漂移频率是按源码逻辑做的推断和估算，需要实测。
- 没有查 Squeezelite 的 `-R` 重采样能否间接用来做漂移补偿。源码里没有任何根据服务器反馈调整速率的路径，所以按“没有”记录。

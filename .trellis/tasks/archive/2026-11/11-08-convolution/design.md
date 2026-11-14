与第一批代码对齐：2026-11-09

# 技术设计：卷积校正

需求见 [prd.md](prd.md)；父任务 [音频算法对标](../../../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§6.4、§6.1（第 ⑤ 步）、§14 第 7 条。
重叠相加（OLA）与 Convolver 的 cfg 格式都是公开知识。

接入以[公共骨架合同](../11-09-dsp-batch2-skeleton/design.md)为准；基线为 `main e29b65d`，新接口均为拟定。纯包与接入的启动条件见本任务 implement.md。前端由主会话写，不另派前端 agent。

## 0. 结论

- 纯包 `backend/internal/dsp/convolution`：矩阵卷积器（任意条路径，每条路径一个 IR、带输入/输出权重），短段 OLA（不是均匀分区卷积），几何完全由 IR 长度决定；按父设计 §3.1 的处理器形状实现；FFT 复用 `dsp/internal/fft`（父设计 §1）。
- IR 装载放在服务包 `dspsettings/irload`：zip/wav/flac/aiff/m4a 与 `.pcm`/`.dbl`/`.cfg` 解析、声道匹配、延时、重采样（调 `dsp/src`）与代数和归一、算固有延迟；解码音频 IR 用现有的 ffmpeg。
- **V7：`Latency()` 报出固有延迟**（所有路径里绝对值最大的抽头位置，§4），由 `chain.Wrap` 在流头丢掉这么多帧、流尾补零冲刷（父设计 §3.2）。链对外零延迟，位置、歌词与计次都不需要偏移。线性相位的房间校正 IR 常见几万抽头，不补偿会让它们偏后零点几秒。
- IR 文件按成员归属，原文件存在 PostgreSQL（bytea，随数据库备份）；每台设备（播放区）可选一个 IR、开关，并决定卷积排在 PEQ 之前还是之后（父设计 §5.2 ⑤ 的用户顺序，§7.1）。

## 1. 引擎（V1）

### 1.1 几何

```text
taps  = 所有路径 IR 的最大长度（短的补零）
nfft  = next_pow2(2 × taps)
block = nfft − taps + 1
bins  = nfft/2 + 1
```

4096 抽头 → nfft 8192、block 4097（验收值）；8192 → 16384、8193。IR 频谱在装载时预乘 `1/nfft`，逆变换不再缩放（与 FFT 包的约定一致）。

### 1.2 处理

```go
package convolution // backend/internal/dsp/convolution

type Path struct {
	IR      []float64            // 已在流采样率上
	Inputs  []Weighted           // {Channel int, Weight float64}，输入加权求和后进本路径
	Outputs []Weighted           // 本路径结果按权重加到这些输出声道
}

type Config struct {
	Channels       int     // 流的声道数，输入输出相同（§3 已做映射）
	Paths          []Path
	InDelay        []int   // 每输入声道的延时样本数
	OutDelay       []int   // 每输出声道的延时样本数
}

func New(c Config) (*Convolver, error)
func (c *Convolver) Process(block [][]float64) [][]float64 // 输出块长 = 输入块长
func (c *Convolver) Latency() int                          // §4
func (c *Convolver) Reset()
```

- 每次调用把输入切成 ≤ block 的段：每个被用到的输入声道做一次 FFT（补零到 nfft）；每个输出声道累加 `Σ_路径 w_out · (Σ w_in · X_in) · H_路径`，再做一次逆 FFT；与重叠缓冲（长度 taps−1）相加后输出本段长度的样本，余下的留作下一段的重叠。
- 段可以短于 block（处理链的块长可变），所以**不额外引入缓冲延迟**：输出 y[n] 只依赖 x[≤ n]，与直接卷积逐样本相同。
- 成本：每段 `输入声道数` 次 FFT + `输出声道数` 次逆 FFT + `路径数 × bins` 次复数乘加。现有量子为采样率/5，48 kHz 下只有 9600 帧，长 IR 的 block 完全可能更大。本版仍采用短段补零 OLA，每次返回等长样本、保存正确重叠；不为攒满 block 而等待下一次调用。若以后改成攒块，必须实现输出时间轴上的固定启动延迟并纳入 Latency，不可仅把“暂时没输出”计为信号延时。
- 输入/输出延时是整数样本的延时线，放在卷积前/后。

### 1.3 上限

- 重采样后的 taps ≤ 2¹⁹（nfft ≤ 2²⁰，每条路径的频谱约 8 MB）；路径 ≤ 32；声道 ≤ 8。上传核验源几何；装载时在分配重采样缓冲前先按目标率推算 taps 并核验上限，不能声称上传能覆盖所有未来重采样率（§5）。
- 装载后的 IR 在进程内按 `(user_id, ir_id, 内容摘要, 流采样率, 声道布局)` 缓存最近 4 份，定位与改设置重建链时不用重新解码和重采样。

## 2. IR 文件（V2、V3）

### 2.1 顶层格式

| 顶层 | 处理 |
| --- | --- |
| `.wav` `.flac` `.aiff`/`.aif` `.m4a` | 单个多声道 IR，用 ffmpeg 解成 `pcm_f64le` 的 WAV 写到管道，自己读 WAV 头拿采样率与声道数（不需要 ffprobe，也支持浮点 WAV） |
| `.zip` | 内存里读（`archive/zip`），不解压到磁盘；有 `.cfg` 时以 cfg 为准，否则取 zip 里唯一的音频文件（多于一个且没有 cfg 时拒绝） |

zip 内还可以有裸浮点：`.pcm` 是单声道 float32 小端，`.dbl` 是单声道 float64 小端，采样率取 cfg 第一行。zip 限制：条目 ≤ 64，解压总量 ≤ 256 MB。

### 2.2 cfg 文法（Convolver 格式）

```text
48000 2 2 3          采样率、输入声道数、输出声道数、输出声道掩码（十六进制，不带 0x）
0 0                  输入延时（毫秒，每输入声道一个）
0 0                  输出延时（毫秒，每输出声道一个）
room.wav             路径 1：IR 文件名（相对 cfg 所在目录，大小写不敏感匹配）
0                    文件内声道（从 0 起）
0.0                  输入：空格分隔的“声道.权重”
0.0                  输出：同上
room.wav             路径 2 …
1
1.0
1.0
```

- 头部与两行延时按**记号流**读：参考稿描述为“4 行头 + 输入延时行 + 输出延时行”，常见文件把 4 个头字段写在一行、延时写成两行，也有把输入输出延时写在一行的，按记号读三种都对。路径块按行读（文件名可能含空格）。
- “声道.权重”：小数点前是声道号，小数点后的数字是权重（`1.5` = 声道 1、权重 0.5）；小数部分为 0 表示权重 1（`0.0` = 声道 0、权重 1）；负号表示反相（声道 0 反相写作 `-0.99999`）。
- 掩码只用于确定声道顺序的布局名，路由完全按路径块。
- zip 里有多个 cfg 时：先选采样率等于当前流的，再选输入声道数等于流声道数的，再选文件名排序第一个。cfg 里的“文件列表模式”（每行一个 cfg 或 wav）不支持，按 cfg 无效拒绝。
- 解析错误返回行号（`ir_cfg_invalid`，`line`）。

## 3. 声道匹配与延时（V4）

流的声道数记为 C（第 ⑤ 步时的声道数，已经过第 ③ 步降混）。

| 情况 | 规则 |
| --- | --- |
| 无 cfg，IR 只有 1 个声道 | 复制到每个声道：C 条路径 i → i |
| 无 cfg，IR 声道数 K = C | 路径 i → i，用 IR 第 i 声道 |
| 无 cfg，K ≠ C 且 K > 1 | 路径 i → i 只建 i < min(K, C) 的；流里多出的声道输出 0，IR 多出的声道不用 |
| cfg 只有一条路径，且为单输入 → 单输出 | 复制到每个声道（单路径 IR），保留权重与所用声道的延时；见下方裁定 |
| cfg 的输入/输出声道数 ≠ C | 插入声道映射：流声道 i 接 cfg 输入 i，cfg 缺的输入填 0；cfg 输出 j 接流声道 j，流里没有路径写到的输出声道为 0，cfg 多出的输出丢弃 |

**主会话 2026-11-09 裁定**：只有“单输入 → 单输出”的单路径 cfg 才复制成 C 条 i → i 路径；每条保留原路径的输入权重和输出权重。输入/输出延时分别取原路径所用输入声道和输出声道的延时，套到每个流声道。单路径但含多个输入或多个输出权重时不复制，按矩阵映射规则处理。

- 延时毫秒按流采样率换成样本：`floor(ms × fs / 1000)`。
- 输出声道数始终等于 C，后续节点不受影响。

## 4. 重采样与归一（V5），固有延迟（V7）

- IR 采样率 ≠ 流采样率时，每个 IR 声道用 `src.ResampleAll(x, from, to, Linear, Precise)` 重采样（SRC 子任务 §1，已去掉滤波器延迟、长度 ceil(len·to/from)）。
- 归一按**代数和**：`h ← h × (Σ原始 / Σ重采样后)`，每个 IR 声道各算一次，不按能量。
  IR 的直流增益接近 0 时（例如分频用的高通，`|Σ原始| < 1e−3 · Σ|原始|`），代数和之比没有意义，改乘 `from/to`——这正是重采样让样本数变化带来的增益差，是同一个归一在直流为 0 时的极限，不是额外的兜底。
- **固有延迟**：在重采样后的全部路径 IR 里找绝对值最大的抽头，其下标即 `Latency()`（输入、输出延时不计入，那是用户有意设置的对齐）。最小相位 IR 的峰在开头附近，延迟约为 0；线性相位 IR 约为长度的一半。
- 补偿归适配器：`chain.Wrap` 流头丢掉 `Latency()` 帧，相当于把 IR 的峰对齐到时间 0；峰之前的前振铃只作用于流开始之前，被丢掉的就是这一段。流尾补零直到输出帧数等于真实输入帧数；只保留该时长内补偿后的结果，**不额外延长曲目来播放完整混响尾巴**。卷积器本身不做任何截断（偏离参考稿 §14 第 7 条“只申报不补偿”的理由见父设计 §10）。

## 5. 存储、上传与删除（V6）

### 5.1 迁移与备份

- **0048_dsp_ir_files.sql/.down.sql**：`dsp_ir_files` 的 id、user_id（users 外键）、name（1–64）、file_name、container、sample_rate、channels、paths、taps、size_bytes（≤32 MiB）、sha256、content bytea、created_at；`UNIQUE(id,user_id)` 及 user_id 索引。所有查询带 user_id，每成员最多 50 个。
- **0049_playback_zone_convolution.sql/.down.sql**：`playback_zones.convolution jsonb NOT NULL DEFAULT '{"enabled":false,"ir_id":null,"before_peq":false}'`。统一骨架不可变文档，不再拆三列；首版不增加 JSON 投影列或复合外键。引用完整性由 zones 的服务操作与同事务资源检查负责，代价是数据库单独手改不能依赖 FK 修正。
- 原文件 bytea 随现有 `backend/internal/backup/service.go` 的整库 pg_dump 备份；不放外部文件路径。恢复带回 IR 与播放区选择。缓存是派生、可丢弃，不需备份。回退先 0049 后 0048；删除原文件是破坏性动作，执行 schema 回退前按现有机制备份。

### 5.2 上传边界

现有 `zones/httpapi/input.go` 是 1 MiB JSON 请求，不能用于上传。新增 `dspsettings/httpapi` 的专用 multipart handler：

1. 成员鉴权、来源/CSRF 边界沿用 app；`http.MaxBytesReader` 限制为 32 MiB 文件加有限 multipart 开销（建议 1 MiB），流式读取 file、name，每项只出现一次；不全量 ReadAll 请求，不信任 Content-Length 或扩展名。
2. 单个 file 独立计数 ≤32 MiB；有界临时文件只写私有临时目录，在成功、失败、取消时关闭删除。现有 `ffmpeg.StartOptions.Source` 只接 `*os.File`，不能直接传 zip 条目的 io.Reader；每个需解码的条目复制到有界临时文件，仍不把 zip 路径解压到文件系统。禁止 cfg 引用 zip 外路径/URL。
3. 独立请求上下文与上传/解析期限（建议 60 s 接收、120 s 处理）；用 handler 级读取 deadline 终止慢读并在结束清除，不能给所有长音频响应加全局 WriteTimeout。现有 main 只有 ReadHeaderTimeout，不等于上传体有超时。ffmpeg 解码用自己的 Reservation 和上下文，不能占 render 的 1/2 个开流名额。
4. zip 条目≤64、解压总量≤256 MiB，逐项累加真实解压字节（不是只看 zip 声明长度）。有 cfg 时按 §2 解析并检查所有被引用文件，输出长度/几何超限停止；音频只解第一个音轨。
5. 上传同步完整验证后只存原文件与元数据；解码输出/字节数有上限。数量检查与插入在短事务内按成员串行，重计算不持事务/播放区锁。

前端新增 FormData 上传路径，不复用强制 JSON 的 API helper；需要进度则由主会话封装有取消能力的 XHR（携带既有认证/来源规则）。不要手填 multipart Content-Type，交浏览器生成 boundary。

### 5.3 引用与删除经 zones

`zones/validation.go` 继续只做纯文档校验。新增消费方资源端口（app 适配 dspsettings），用于 `plan()` 的 set_settings 和 `devices.go/RegisterDevice` 核对非空 ir_id 是否属于本人；不可因为 enabled=false 就允许跨成员引用。不存在/他人 ID 均映射 not_found。拟定 `zones.IRReferences` 提供 `Check(ctx, tx *sql.Tx, userID, irID string) error` 与 `Delete(ctx, tx *sql.Tx, userID, irID string) error`：Check 的 nil tx 只用于 plan/RegisterDevice 预检，写入事务内再传实际 tx 复核；Delete 必须给 tx。app 将 dspsettings 的资源不存在错误映射为 zones.ErrNotFound，资源包不反向调用 zones 服务。

采用现有锁序延伸：`userRuntime.mu` → 按 zone_id 排序的 `dispatch` → `zone.mu` → 数据库事务。普通 Command 已拿用户读锁，删除拿用户写锁，RegisterDevice 本就拿写锁，因此删除与设置/注册不会互相穿过。IR 删除由 `zones.Service.DeleteIR(ctx,userID,id)` 编排，dspsettings/httpapi 调这个用例，而不是直接执行 DELETE：

1. 装载该用户运行态（包括未在线播放区），锁住引用此 IR 的区；制作新设置（ir_id=null、enabled=false，before_peq 保留），revision 各增一次。
2. 同一事务经资源端口删本人 IR，并由 zones Store 更新所有引用区；资源端口接受调用方 `*sql.Tx`，不自己提交，不访问 zones 私表。任何失败整体回滚，内存不变。
3. 提交后替换内存设置、关闭/失效旧待用会话与 IR 缓存，对正在播放区走现有去抖重建并发布完整 zone 更新。渲染已持有的不可变 IR 内存可保留到旧会话结束，但不能再用于新会话。
4. 归属检查在事务写入前再核验资源存在（必要时锁行），并将资源 not_found 保留为业务错误，不由 `databaseError` 包成 503。所有资源删除只能走本用例；不使用 `ON DELETE SET NULL` 绕开内存缓存。

备选是三列加复合 FK，但仍必须经 zones 删除。这里推荐统一 JSON，省掉双重表示；若主会话坚持数据库 FK，应在 0049 改为显式 IR 引用列、唯一编解码投影，不采用生成列强制 cast 坏 JSON 导致整行写失败。

## 6. 接口

`/api/v1/me/dsp/` 下，按当前成员鉴权：

| 方法与路径 | 作用 |
| --- | --- |
| `GET ir-files` | 列表 `[{id, name, file_name, container, sample_rate, channels, paths, taps, created_at}]` |
| `POST ir-files` | multipart：`name`、`file` → 元数据；`ir_format_unsupported`、`ir_cfg_invalid`、`ir_decode_failed` 为 400，`ir_too_large` 为 413，`ir_limit_reached` 为 409；不为 cfg 行号新增平台错误 JSON 字段，服务错误保留行号并在安全消息内展示 |
| `PATCH ir-files/{id}` | 改名 |
| `DELETE ir-files/{id}` | 删除 |

## 7. 接入（骨架 → SRC → 降混 → PEQ 之后）

### 7.1 异步准备，不持播放区锁

`zones/render_events.go/createRender` 持区锁调用 `Renderer.Open`，`render.Options` 现在只有 Sources 等字段。因此新增消费方 `IRSource` 端口与 `Options.IRs`，由 `app/renderer.go` 装配 dspsettings/irload，render 不导入资源服务。拟定端口在 render 定义：

```go
type IRSource interface {
    Load(ctx context.Context, userID, irID string, format chain.Format) (LoadedIR, error)
}
type LoadedIR struct {
    Name string
    ResampledFrom int
    Config convolution.Config
}
```

返回配置是只读快照；Convolver.New 只读 IR 并创建自己独占的频谱/处理状态，不修改缓存路径的切片。端口包含成员ID与实际布局，不能仅按 ir_id 跨用户读缓存。

- Open 仅做轻量格式规划、取得设置不可变快照；outputPCM 率/声道不依赖 IR 内容（§3 始终保持 C），可同步发布固定 Format。**不得**在 Open 中读 IR 表、解码、生成频谱或重采样。
- 会话启动协程先 `IRs.Load(ctx,userID,irID,目标率,布局)`，命中缓存则复用只读路径；再实例化独占 Convolver、组装链、启动编码/来源生产。已有 session.start 的“encoder=nil 就 runFailure”分支须明确拆成“准备中/来源失败”，不要把异步未创建 encoder 当失败。ready、Body/HLS 等待与 Close 取消必须覆盖准备期。
- `Renderer.Open` 预留 PCM 解码+编码的名额不要跨 IR 重算占住；准备后才 Reserve 所需数。IR 解码名额独立且有期限；WAV 原本只需 1 个，不是固定 2。上下文取消关闭临时文件与进程，不回调 zones 锁内方法。
- 正常装载失败沿用本子项既定降级：只省略卷积节点、记一条脱敏警告，其他处理继续；取消则结束会话，不能降级继续播放。位深按已启用设置保守规划，信号只展示实际装上的节点，不在 Format 发布后从 24 改 16。坏资源不是坏 JSON，不能在 zones 静默改用户设置。

### 7.2 节点与顺序

`convolution.enabled && ir_id != null` 时在 `Spec.User` 加 `chain.Wrap(conv,f,f,step,true)`；默认 PEQ → 卷积，before_peq=true 时交换。先经 ② SRC、③ 降混，再按实际率/布局装载，所以有损 192k 源不会在 192k 上做卷积。用户启用且无有效 IR 时不插节点；成功装入的单位 IR 仍算处理。

`Latency()` 为峰位置，短段 OLA 不加计算批量延迟。骨架 inputSubmitted 负责队列移除判定，不能退回 written 或写死 `written+Latency`。跨曲目不 Reset，量子不足 block 仍即时处理。

### 7.3 信号与设置

`kind: convolution`，params `{name,taps,resampled_from?}`，`quality: enhanced, compat:false`；前端 model/decoder/stepIcons 同步。HTTP settings 是完整 convolution 文档，错误沿用骨架；本资源接口单独映射 §6 的错误。API 样本仅更新所属 recorder 与一份非默认卷积样本。

## 8. 界面（主会话实现）

播放区设置里加“卷积”一组：

- 开关 + IR 下拉（我的 IR 文件名）。
- PEQ 与卷积都开着时，两项按当前顺序列出，第二项带一个“上移”图标按钮，点一下交换（写 `before_peq`）；不配说明文字。
- “管理”打开列表面板：每行名称、采样率、声道、抽头数；改名、删除图标按钮；顶部“上传”按钮（接受 `.zip .wav .flac .aif .aiff .m4a`），上传中显示进度，失败显示一句原因（例如“cfg 第 7 行看不懂”“文件太大，最多 32 MB”）。
- 不写说明段落。

## 9. 测试（最小集）

后端（测试 GPT）：

1. 几何：taps 4096 → nfft 8192、block 4097、bins 4097。
2. 单位冲激 IR：输出等于输入（误差 ≤ 1e−12）；分块大小不同（1、1000、block、3·block）结果相同。
3. 与直接卷积对照：随机 IR（300 抽头）与随机输入，误差 ≤ 1e−10。
4. 矩阵路由：带 cfg 的小 zip（2 进 2 出，含交叉路径、权重 `1.5`、反相 `-0.99999`、输入延时 1 ms），用冲激检查每个输出声道的路由与权重；同一份 cfg 用一行写延时和两行写延时都能解析。
5. 声道匹配：§3 表里的五种情况，各一个小用例（表驱动）。
6. 重采样归一：44.1 kHz 的 IR 装到 48 kHz 流，代数和与原 IR 一致（相对误差 ≤ 1e−9）；直流为 0 的 IR 走 `from/to` 分支。
7. 固有延迟：峰在第 2048 个抽头的 IR，Latency()==2048；经 Wrap 后峰在第 0 帧，尾长不超真实输入；再用一条 block>9600 的 IR 核实短量子不攒块。
8. 接入必要失败路径：慢 IR 装载时其他区命令/Close 不被锁住；选中 IR 的删除事务提交后 DB/缓存/运行态同时清引用，随后普通区命令不写回旧 ID；RegisterDevice 与 set_settings 各验证他人 IR 拒绝；一次超限上传/取消清理。复用已有备份还原测试放入一个小 IR 与选择，核对恢复，不另写整套备份系统测试。

前端（测试 GPT）：上传成功后列表出现新项；选中 IR 并打开开关后提交的设置正确。各一个用例。

## 10. 分工与依赖

后端实现、独立测试均 `codex/gpt-6-astra`（high）；前端主会话写。纯引擎、cfg/zip 解析可先写；FFT 必须取得 SRC 的纯包提交，IR 重采样另需 ResampleAll，不把这两项说成完全无依赖。资源服务、0048/0049、删除编排及渲染接入只在骨架 → SRC → 降混 → PEQ 合入后进行。依赖登记遵循骨架 §8；dspsettings/irload 可导入 ffmpeg 工具但不导入 render，会话通过端口获得 IR。

## 11. 待裁决

无。上传大小（32 MB）与个数（50）是技术上限，按需再调。

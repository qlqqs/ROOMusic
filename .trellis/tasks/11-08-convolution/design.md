# 技术设计：卷积校正

需求见 [prd.md](./prd.md)；父任务 [音频算法对标](../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§6.4、§6.1（第 ⑤ 步）、§14 第 7 条。
重叠相加（OLA）与 Convolver 的 cfg 格式都是公开知识。

## 0. 结论

- 纯包 `backend/internal/dsp/convolution`：矩阵卷积器（任意条路径，每条路径一个 IR、带输入/输出权重），均匀分块 OLA，几何完全由 IR 长度决定；按父设计 §3.1 的处理器形状实现；FFT 复用 `dsp/internal/fft`（父设计 §1）。
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
- 成本：每段 `输入声道数` 次 FFT + `输出声道数` 次逆 FFT + `路径数 × bins` 次复数乘加。处理链的量子是 200 ms，通常不短于 block。
- 输入/输出延时是整数样本的延时线，放在卷积前/后。

### 1.3 上限

- 重采样后的 taps ≤ 2¹⁹（nfft ≤ 2²⁰，每条路径的频谱约 8 MB）；路径 ≤ 32；声道 ≤ 8。超限在上传时拒绝（§5）。
- 装载后的 IR 在进程内按 `(ir_id, 流采样率, 声道数)` 缓存最近 4 份，定位与改设置重建链时不用重新解码和重采样。

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
| cfg 只有一条路径 | 复制到每个声道（单路径 IR） |
| cfg 的输入/输出声道数 ≠ C | 插入声道映射：流声道 i 接 cfg 输入 i，cfg 缺的输入填 0；cfg 输出 j 接流声道 j，流里没有路径写到的输出声道为 0，cfg 多出的输出丢弃 |

- 延时毫秒按流采样率换成样本：`floor(ms × fs / 1000)`。
- 输出声道数始终等于 C，后续节点不受影响。

## 4. 重采样与归一（V5），固有延迟（V7）

- IR 采样率 ≠ 流采样率时，每个 IR 声道用 `src.ResampleAll(x, from, to, Linear, Precise)` 重采样（SRC 子任务 §1，已去掉滤波器延迟、长度 ceil(len·to/from)）。
- 归一按**代数和**：`h ← h × (Σ原始 / Σ重采样后)`，每个 IR 声道各算一次，不按能量。
  IR 的直流增益接近 0 时（例如分频用的高通，`|Σ原始| < 1e−3 · Σ|原始|`），代数和之比没有意义，改乘 `from/to`——这正是重采样让样本数变化带来的增益差，是同一个归一在直流为 0 时的极限，不是额外的兜底。
- **固有延迟**：在重采样后的全部路径 IR 里找绝对值最大的抽头，其下标即 `Latency()`（输入、输出延时不计入，那是用户有意设置的对齐）。最小相位 IR 的峰在开头附近，延迟约为 0；线性相位 IR 约为长度的一半。
- 补偿归适配器：`chain.Wrap` 流头丢掉 `Latency()` 帧，相当于把 IR 的峰对齐到时间 0；峰之前的前振铃只作用于流开始之前，被丢掉的就是这一段。流尾补零直到输出帧数等于输入帧数，IR 的尾巴完整输出。卷积器本身不做任何截断（偏离参考稿 §14 第 7 条“只申报不补偿”的理由见父设计 §10）。

## 5. 存储与管理（V6）

```sql
CREATE TABLE dsp_ir_files (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
  file_name text NOT NULL,
  container text NOT NULL CHECK (container IN ('zip','wav','flac','aiff','m4a')),
  sample_rate integer NOT NULL,
  channels smallint NOT NULL,            -- 无 cfg 时是 IR 声道数，有 cfg 时是 cfg 的输入声道数
  paths smallint NOT NULL,
  taps integer NOT NULL,                 -- 原采样率下的最长路径
  size_bytes integer NOT NULL CHECK (size_bytes <= 33554432),
  sha256 bytea NOT NULL,
  content bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, user_id)
);
CREATE INDEX dsp_ir_files_user_idx ON dsp_ir_files (user_id);

ALTER TABLE playback_zones
  ADD COLUMN convolution_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN convolution_ir_id uuid,
  ADD COLUMN convolution_before_peq boolean NOT NULL DEFAULT false,
  ADD FOREIGN KEY (convolution_ir_id, user_id) REFERENCES dsp_ir_files (id, user_id) ON DELETE SET NULL (convolution_ir_id);
```

- 迁移文件 `00NN_dsp_ir_files.sql`，编号接在 dsp-pipeline 的 Headroom 迁移之后、按第二批合入顺序顺延，带 `.down.sql`。
- 按父设计 §7 加在 `playback_zones` 上。这三项都是简单值，用普通列而不是 jsonb，换来外键：复合外键保证播放区引用的 IR 属于播放区主人；删除 IR 时只把引用置空（PostgreSQL 15 起支持列清单，项目用 18）。
- 上传：单个文件 ≤ 32 MB，每人 ≤ 50 个。上传时**同步完整解析一次**（容器、cfg、解码、上限检查），失败返回具体错误码；成功只存原文件与元数据，不存解码结果。
- 删除被播放区选中的 IR：引用自动置空，那台设备的卷积节点随之消失（下一次重建链时生效）。
- `zones` 的 `set_settings` 接受键 `convolution: {enabled, ir_id, before_peq}`；`ir_id` 经 `dspsettings` 核对属于播放区主人，不属于按不存在处理（与播放区线 design-protocol §4 一致）；改动由 zones 去抖重建。

## 6. 接口

`/api/v1/me/dsp/` 下，按当前成员鉴权：

| 方法与路径 | 作用 |
| --- | --- |
| `GET ir-files` | 列表 `[{id, name, file_name, container, sample_rate, channels, paths, taps, created_at}]` |
| `POST ir-files` | multipart：`name`、`file` → 新建的元数据；错误码 `ir_format_unsupported`、`ir_cfg_invalid`（带 `line`）、`ir_too_large`、`ir_limit_reached`、`ir_decode_failed` |
| `PATCH ir-files/{id}` | 改名 |
| `DELETE ir-files/{id}` | 删除 |

## 7. 接入

### 7.1 第 ⑤ 步与用户顺序

- 父设计 §5.2 的 ⑤ 是用户处理项链，现在只有 PEQ 与卷积两项。顺序由 `convolution_before_peq` 表示：默认 false（PEQ → 卷积，PEQ 修耳机或音箱的频响、卷积做房间校正，是常见用法）；两项不全时它不起作用。以后 ⑤ 出现第三种项目时再改成有序列表。
- `convolution_enabled` 为真且 `convolution_ir_id` 非空时插入：`chain.Wrap(conv, f, f, step, true)`，延迟补偿见 §4。会话内链不变（父设计 §5.3）；IR 装载在开流时完成，命中 §1.3 的缓存时不重新解码。
- 装载失败（例如数据库里的文件读不出）时不插节点，信号路径不显示卷积，服务端记一条警告日志；不让整条流失败。

### 7.2 信号路径步骤

按 dsp-pipeline §9.1：`kind: "convolution"`，`params: {name, taps, resampled_from?}`，`quality: "enhanced"`，`compat: false`。IR 被重采样时附带原采样率，显示为“卷积 · 名称（48 → 44.1 kHz）”。

## 8. 界面（Opus 实现）

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
7. 固有延迟：峰在第 2048 个抽头的 IR，`Latency() == 2048`；经 `chain.Wrap` 后单位冲激输入在输出第 0 帧出峰（补偿由适配器测试覆盖，这里只测一次接线）。

前端（测试 GPT）：上传成功后列表出现新项；选中 IR 并打开开关后提交的设置正确。各一个用例。

## 10. 分工与依赖

- 后端（GPT）：`dsp/convolution`、`dspsettings/irload`、IR 表与接口、迁移、`set_settings` 键、⑤ 节点与顺序、信号路径步骤。
- 前端（Opus）：§8，一个 agent 一次做完（可与 PEQ 前端合在同一个 agent，若同时开工）。
- 测试（GPT）：§9。
- 依赖：SRC 子任务的 `dsp/internal/fft` 与 `src.ResampleAll`（纯包部分合入后即可开工）；接入等第一批（处理链、`chain.Wrap`、Headroom 迁移、信号路径）合入 main。与 PEQ 子任务谁先合入都可以：PEQ 未合入时 ⑤ 只有卷积，`before_peq` 先存着。

## 11. 待裁决

无。上传大小（32 MB）与个数（50）是技术上限，按需再调。

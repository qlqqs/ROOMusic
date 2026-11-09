# 技术设计：参数均衡与耳机校正

需求见 [prd.md](./prd.md)；父任务 [音频算法对标](../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§6.3、§6.9（OHD 耳机校正）、§6.1（第 ⑤ 步）。
滤波器公式来自公开的 Audio-EQ-Cookbook 与双线性变换；OPRA 数据按 CC BY-SA 4.0 使用。

## 0. 结论

- 纯包 `backend/internal/dsp/peq`：双二阶系数设计（Cookbook；低通/高通用 `K = tan(πf/fs)` 预畸变形式）+ Butterworth 固定 Q 级联（1–6 阶，即 6–36 dB/倍频程）+ **一个**多通道 IIR 处理器。
- 服务包 `backend/internal/dspsettings`（父设计 §1 已记）：PEQ 文档的校验与换算、成员预设、OPRA 目录、文本导入；HTTP 在 `dspsettings/httpapi`。
- 播放区存一份完整的 PEQ 文档（`playback_zones.peq jsonb`，父设计 §7；复制而不是引用预设）；预设是成员自己的表。PEQ 在 ⑤ 里排在卷积之前还是之后由卷积子任务的 `convolution_before_peq` 决定（§9）。
- OPRA 数据裁剪后 gzip 随仓库与镜像分发（约 1–2 MB），附来源与许可说明；更新用一个生成命令手动跑，运行时不联网。
- 界面：PEQ 编辑器（段列表 + 频响曲线）、OPRA 选择、粘贴导入、预设；OPRA 署名按其要求显示，作者比 OPRA 标识更醒目。

## 1. 滤波器（Q1）

记 `w0 = 2πf/fs`，`A = 10^(G/40)`，`α = sin(w0)/(2Q)`，`K = tan(w0/2)`。系数统一除以 a0 后按 `(a1, a2, b0, b1, b2)` 交给处理器。

| 类型 | 公式来源 | 参数 |
| --- | --- | --- |
| 峰值 `peaking` | Cookbook PeakingEQ：`b = (1+αA, −2cos w0, 1−αA)`，`a = (1+α/A, −2cos w0, 1−α/A)` | f、G、Q |
| 低架 / 高架 `low_shelf` / `high_shelf` | Cookbook lowShelf/highShelf，用 Q 形式的 α（与 AutoEq、Equalizer APO 的 LSC/HSC 一致） | f、G、Q |
| 低通 / 高通 `low_pass` / `high_pass` | 双线性预畸变：二阶 `n = 1/(1 + K/Q + K²)`，低通 `b = (K²n, 2K²n, K²n)`，高通 `b = (n, −2n, n)`，`a1 = 2(K²−1)n`，`a2 = (1 − K/Q + K²)n`；一阶低通 `b0 = b1 = K/(1+K)`，一阶高通 `b0 = 1/(1+K)`、`b1 = −b0`，`a1 = (K−1)/(K+1)` | f、斜率（或二阶时的 Q） |
| 带通 `band_pass` | Cookbook BPF（峰值 0 dB）：`b = (α, 0, −α)`，`a = (1+α, −2cos w0, 1−α)` | f、Q |
| 陷波 `notch` | Cookbook notch：`b = (1, −2cos w0, 1)`，`a` 同带通 | f、Q |
| 全通 `all_pass` | Cookbook APF：`b = (1−α, −2cos w0, 1+α)`，`a` 同带通 | f、Q |

- 二阶低通/高通的 `tan` 形式与 RBJ 的 `sin/cos` 形式在代数上相同；用 `tan` 形式是为了和一阶节、固定 Q 级联共用一套写法（参考稿 §6.3 的要求）。
- 自检：1 kHz、+6 dB、Q=1、44.1 kHz 的峰值滤波器得到 `b0=1.04763003, b1=−1.88499125, b2=0.85665646, a1=−1.88499125, a2=0.90428649`，`b1 == a1`（本机已按公式复算一致）。
- 频率钳到 `[10 Hz, 0.49·fs]`：流的采样率低于设置时（例如 44.1 kHz 流上的 30 kHz 段）照钳位后的值算，不报错。

## 2. 高阶：Butterworth 固定 Q 级联（Q2）

n 阶（n = 1…6，斜率 6n dB/倍频程）拆成 ⌊n/2⌋ 个二阶节，奇数阶再加一个一阶节。二阶节的 Q：

```text
偶数 n：Q_k = 1 / (2 cos((2k−1)π / (2n)))    k = 1 … n/2
奇数 n：Q_k = 1 / (2 cos(kπ / n))           k = 1 … (n−1)/2
Q 四舍五入到两位小数
```

得到：2 阶 0.71；3 阶 1.00 + 一阶；4 阶 0.54、1.31；5 阶 0.62、1.62 + 一阶；6 阶 0.52、0.71、1.93。与参考稿列出的固定 Q 集一致，属近似 Butterworth。
OPRA 的低通/高通只给斜率（6–36），正好覆盖 1–6 阶。斜率为 12 且显式给了 Q 时（Equalizer APO 的 `LPQ`/`HPQ`），用单个二阶节和这个 Q。

## 3. 多通道 IIR 处理器（Q3）

```go
package peq // backend/internal/dsp/peq

type Section struct {
	A1, A2, B0, B1, B2 float64
	Channel            int
}

type IIR struct{ /* sections、每段 2 个状态、每声道的段下标 */ }

// 满足父设计 §3.1；会话内链不变，段与前级增益在构造时给定。
func NewIIR(channels int, sections []Section, preampDB float64) *IIR
func (f *IIR) Process(in [][]float64) [][]float64 // 就地处理并返回 in
func (f *IIR) Latency() int                       // 0
func (f *IIR) Reset()
```

- 所有启用的段 × 声道折叠进这一个处理器：每声道按段的顺序串联，转置直接 II 型（`y = b0x + s1; s1 = b1x − a1y + s2; s2 = b2x − a2y`），float64。
- 前级增益（preamp）在第一段前乘一次。
- 非规格化数：每块结束时把绝对值小于 1e−30 的状态置 0（Go 没有 FTZ，静音后状态衰减到非规格化数会拖慢）。
- 设计函数也给其他包用：`Design(band Band, fs float64) []Section`、`ButterworthLowPass(order int, f, fs float64) []Section`（DSD 来源段的后置低通用它，见 DSD 子任务 §3.4）。

## 4. PEQ 文档（Q4）

播放区与预设存同一种 JSON 文档：

```json
{
  "enabled": true,
  "preamp_db": -6.2,
  "bands": [
    {"type": "peaking", "freq_hz": 105, "gain_db": 6.5, "q": 0.7, "channels": "all", "enabled": true},
    {"type": "high_pass", "freq_hz": 20, "slope_db": 12, "channels": [0], "enabled": true}
  ],
  "source": {"kind": "opra", "eq_id": "…", "vendor": "…", "product": "…", "author": "…", "details": "…", "link": "…"}
}
```

- `type`：`peaking | low_shelf | high_shelf | low_pass | high_pass | band_pass | notch | all_pass`。
- `channels`：`"all"` 或声道下标数组（0 = 左、1 = 右……）；下标超出当前流的声道数时那一段在该流上不生效。
- 范围：段数 ≤ 32；`freq_hz` 10–40000；`gain_db` −30…+30；`q` 0.1–30；`slope_db` ∈ {6,12,18,24,30,36}；`preamp_db` −30…+30。超范围在保存时 400 拒绝（`peq_invalid`，带字段路径）。
- `source`：`opra`（套用 OPRA 配置）、`preset`（`{preset_id, name}`，套用时的快照，预设之后被改或被删不影响播放区）、`import`、或省略（手动）。用户在编辑器里改动任何一段后 `source` 保留，界面显示为“基于 …”。
- 没有启用的段且 preamp 为 0 时，处理链不插节点。

## 5. OPRA 耳机校正（Q6）

### 5.1 数据与打包

- 上游：Roon Labs 维护的 OPRA 库（`github.com/opra-project/OPRA`），`database_v1.jsonl` 每行 `{"type": "vendor"|"product"|"eq", "id", "data"}`；EQ 的 `data` 有 `product_id, author, details, link, type, parameters{gain_db, bands[{type, frequency, gain_db, q, slope}]}`。按 OPRA 的说明，开源项目从 Roon Labs 的镜像 `opra.roonlabs.net` 取数据。
- 裁剪：只留厂商 `{id, name}`、产品 `{id, vendor_id, name, subtype}`、EQ `{id, product_id, author, details, link, parameters}`，丢掉介绍文字、标识与线稿图路径；`type` 不是 `parametric_eq` 的 EQ 丢弃（§6.9）。按 id 排序后写成 JSONL 再 gzip，放 `backend/internal/dspsettings/opra/data/opra-v1.jsonl.gz`，同目录 `SOURCE.md` 记上游地址、抓取日期、上游文件 SHA-256、许可（CC BY-SA 4.0）与署名要求。
- 体积：上游约 12 MB，裁剪并压缩后预计 1–2 MB，`go:embed` 进二进制，镜像不需要额外文件。
- 更新：`go run ./internal/dspsettings/opra/cmd/opra-update`（下载、校验字段、裁剪、排序、压缩、改写 `SOURCE.md`），由维护者在发版前手动跑并提交；CI 不联网。
- OPRA 标识图片：更新命令同时从 OPRA 仓库的 `images/` 取标识，存 `frontend/src/assets/opra/`。

### 5.2 加载与搜索

- 第一次请求时解压解析到内存（产品按厂商分组，EQ 按产品分组），之后常驻；预计 20–30 MB 内存。
- 搜索：把“厂商名 + 产品名”规范化（小写、去空白与标点、全角转半角）后做子串匹配，按匹配位置与名字长度排序，最多返回 30 个产品。

### 5.3 映射到 PEQ

| OPRA | PEQ |
| --- | --- |
| `parameters.gain_db` | `preamp_db` |
| `peak_dip` | `peaking`（§6.9） |
| `low_shelf` / `high_shelf` | 同名，用 `q` |
| `low_pass` / `high_pass` | 同名，`slope_db = slope` |
| `band_pass` | `band_pass`，`gain_db` 不用（Cookbook BPF 峰值固定 0 dB） |
| `band_stop` | `notch` |

段数超过 32 时按 OPRA 的“按优先级排序”截断前 32 段。套用后 `source.kind = "opra"`，带厂商、产品、作者、说明与链接。

### 5.4 署名

- 选择面板底部：OPRA 标识 + 一句“耳机校正数据来自 OPRA（CC BY-SA 4.0）”+ 仓库链接。
- 每条配置：作者名比 OPRA 标识更醒目（OPRA 的要求），后面是说明（`details`）与来源链接。
- 套用后，PEQ 编辑器顶部显示“产品名 · 作者”，作者可点开来源链接。信号路径的 PEQ 节点名用“产品名 · 作者”。

## 6. 文本导入（Q7）

`POST …/peq/import` 只解析不保存，返回 PEQ 文档与无法识别的行号。支持 AutoEq 的 `ParametricEQ.txt` 与 Equalizer APO 的同类写法：

```text
Preamp: -6.2 dB
Filter 1: ON PK Fc 105 Hz Gain 6.5 dB Q 0.70
Filter 2: ON LSC Fc 105 Hz Gain 6.5 dB Q 0.70
```

| APO 类型 | PEQ |
| --- | --- |
| `PK`、`PEQ`、`Modal` | `peaking` |
| `LSC` / `HSC`（带 Q） | `low_shelf` / `high_shelf` |
| `LS` / `HS`（不带 Q） | 同上，Q = 0.71 |
| `LP` / `HP` | 二阶，`slope_db = 12` |
| `LPQ` / `HPQ` | 二阶，用给出的 Q |
| `BP` | `band_pass` |
| `NO` | `notch` |
| `AP` | `all_pass` |

- `OFF` 的段跳过；大小写不敏感；数字用点作小数点（`6.5`、`-6`），解析不了的行算无法识别，不报错。
- `Preamp` 多次出现时取最后一个；`Channel:`、`GraphicEQ:`、`Include:` 等行算无法识别。一个 Filter 都没有时 400 `peq_import_empty`。

## 7. 预设（Q5）

```sql
CREATE TABLE dsp_peq_presets (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
  peq jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, name)
);
```

- 只属于创建者；所有读写都带 `user_id` 条件，别人的 ID 按不存在处理（404）。每人上限 200 个。
- 套用预设 = 把预设的 `peq` 复制进播放区，`source = {kind: "preset", preset_id, name}`。
- 播放区线的 `set_settings` 不需要核对预设归属（播放区里只有快照）；需要核对归属的只有卷积的 IR 引用。

## 8. 接口

都在 `/api/v1/me/dsp/` 下，按当前成员鉴权：

| 方法与路径 | 作用 |
| --- | --- |
| `GET opra/search?q=` | 产品列表 `[{product_id, vendor, name, subtype, eq_count}]` |
| `GET opra/products/{id}` | 产品与它的配置 `[{eq_id, author, details, link, peq}]`（已映射成 PEQ 文档） |
| `POST peq/import` | `{text}` → `{peq, unrecognized_lines: [int]}` |
| `POST peq/response` | `{peq, sample_rate?}` → 每个声道组一条曲线 `{curves: [{channels, points: [[hz, db]]}]}`，20 Hz–20 kHz 对数 256 点，默认 48 kHz |
| `GET/POST peq-presets`，`PATCH/DELETE peq-presets/{id}` | 预设的增删改查（改名、覆盖内容） |

播放区里的 PEQ 经 `zones` 的 `set_settings` 写入（键 `peq`）；`zones` 调用 `dspsettings` 的校验函数（父设计 §1 的导入方向），超范围返回 400 `peq_invalid`。

## 9. 接入

- 迁移 `00NN_playback_zone_peq.sql`（编号接在 dsp-pipeline 的 Headroom 迁移之后、按第二批合入顺序顺延，带 `.down.sql`）：`ALTER TABLE playback_zones ADD COLUMN peq jsonb NOT NULL DEFAULT '{"enabled":false,"preamp_db":0,"bands":[]}'`。解码校验与 Headroom 相同：坏值按默认处理。
- 位置：父设计 §5.2 的 ⑤ 用户处理项链。与卷积的先后由 `convolution_before_peq` 决定（卷积子任务 §7.1，默认 PEQ 在前）；卷积未合入时 ⑤ 只有 PEQ。
- 构建：开流时按流的采样率与声道数（③ 降混之后）把文档换成 `[]Section`，`chain.Wrap(peq.NewIIR(…), f, f, step, true)`；会话内不变，改动由 zones 去抖重建（父设计 §5.3）。
- 信号路径步骤（dsp-pipeline §9.1）：`kind: "peq"`，`params: {label, bands}`，`quality: "enhanced"`，`compat: false`。`label` 是 `source` 的显示名（OPRA 为“产品名 · 作者”、预设为预设名），手动编辑为“参数均衡”；`bands` 是生效的段数。

## 10. 界面（Opus 实现）

在播放区设置里加“参数均衡”入口，打开编辑面板：

- 顶部：总开关、前级增益输入、来源名（OPRA/预设/导入）。
- 频响曲线：调用 `peq/response`（输入后 150 ms 去抖），多声道组时用不同颜色的线；不画网格以外的装饰。
- 段列表：每行类型下拉、频率、增益（低通/高通/带通/陷波/全通时隐藏）、Q（低通/高通显示斜率下拉）、声道（全部/左/右）、启用勾选、删除图标按钮；末尾“添加”图标按钮。
- 工具栏图标按钮：“耳机校正”（OPRA 面板）、“粘贴导入”（文本框 + 预览曲线 + 应用；有无法识别的行时显示一句“有 N 行没有识别”）、“预设”（列表：套用、改名、删除；“存为预设”输入名称）。
- OPRA 面板：搜索框、产品列表（厂商 + 型号）、点开后是配置列表（作者醒目、说明、来源链接、套用按钮）、底部署名（§5.4）。
- 文案遵守全局约束：不解释算法、不写权限或隔离说明；按钮能说明的不加段落。

## 11. 测试（最小集）

后端（测试 GPT）：

1. Cookbook 自检向量（§1，误差 ≤ 1e−8）与 `b1 == a1`。
2. 每种类型一个频点表：峰值/架类在 f 处的增益等于 G（±0.01 dB）；低通/高通 1–6 阶在 f 处约 −3 dB（±0.1 dB，Q 取两位小数带来的偏差在内）、f = 100 Hz / 48 kHz 时低通在 10f 处约 −20n dB（±0.5 dB）；带通在 f 处 0 dB；陷波在 f 处 ≤ −100 dB；全通各频点 0 dB。频响用系数直接算，不跑信号。
3. 固定 Q 表（§2 的 6 行）。
4. IIR 处理器：冲激响应与按系数直接递推的结果一致；声道只作用于指定下标；分块大小不同结果相同。
5. OPRA：用一份 3 行的小样本（厂商、产品、含 `peak_dip`/`low_pass` 的 EQ）测映射；嵌入的真数据只测“能解析、条数大于 0”。
6. 导入：一段真实格式的 AutoEq `ParametricEQ.txt`（10 段 + Preamp）与一行无法识别的行。
7. 预设接口：本人能增删改查、他人 ID 返回 404（一个用例）。

前端（测试 GPT）：编辑器加段、改类型后提交的文档正确；导入预览后应用；OPRA 配置套用后显示作者与链接。各一个用例。

## 12. 分工与依赖

- 后端（GPT）：`dsp/peq`、`dspsettings`（文档、预设、OPRA、导入、接口、迁移）、`set_settings` 键、⑤ 节点与信号路径步骤。
- 前端（Opus）：§10 全部界面，一个 agent 一次做完。
- 测试（GPT）：§11。
- 依赖：`dsp/peq` 纯包与 OPRA、导入、预设可以先做；接入等第一批（处理链、Headroom 迁移、信号路径）合入 main。DSD 子任务的后置低通复用 `ButterworthLowPass`。

## 13. 已裁决

- **OPRA 列表不带产品图片**：只显示厂商与型号文字（2026-11-08 用户采纳推荐）。数据包保持在 1–2 MB；不需要耳机外形图辅助辨认。

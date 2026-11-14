与第一批代码对齐：2026-11-09

# 技术设计：参数均衡与耳机校正

需求见 [prd.md](prd.md)；父任务 [音频算法对标](../../../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§6.3、§6.9（OHD 耳机校正）、§6.1（第 ⑤ 步）。
滤波器公式来自公开的 Audio-EQ-Cookbook 与双线性变换；OPRA 数据按 CC BY-SA 4.0 使用。

接入以[公共骨架合同](../11-09-dsp-batch2-skeleton/design.md)为准；基线为 `main e29b65d`，新接口均为拟定。纯包与接入的启动条件见本任务 implement.md。前端由主会话写，不另派前端 agent。

## 0. 结论

- 纯包 `backend/internal/dsp/peq`：双二阶系数设计（Cookbook；低通/高通用 `K = tan(πf/fs)` 预畸变形式）+ Butterworth 固定 Q 级联（1–6 阶，即 6–36 dB/倍频程）+ **一个**多通道 IIR 处理器。
- 服务包 `backend/internal/dspsettings`（父设计 §1 已记）：成员预设、OPRA 目录、文本导入与曲线；播放区文档类型/规范化/纯校验归 zones，算法换算在 render 或服务调用点；HTTP 在 `dspsettings/httpapi`。
- 播放区存一份完整的 PEQ 文档（`playback_zones.peq jsonb`，父设计 §7；复制而不是引用预设）；预设是成员自己的表。PEQ 在 ⑤ 里排在卷积之前还是之后由卷积子任务的 `convolution.before_peq` 决定（§9）。
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
- 范围：段数 ≤ 32；`freq_hz` 10–40000；`gain_db` −30…+30；`q` 0.1–30；`slope_db` ∈ {6,12,18,24,30,36}；`preamp_db` −30…+30。超范围在保存时 `400 invalid_input` 拒绝，不带字段路径；与现有 zones 错误形状一致。
- `source`：`opra`（套用 OPRA 配置）、`preset`（`{preset_id, name}`，套用时的快照，预设之后被改或被删不影响播放区）、`import`、或省略（手动）。用户在编辑器里改动任何一段后 `source` 保留，界面显示为“基于 …”。
- 只有 `enabled=false` 才不插节点；enabled=true 即使当前没有生效段且 preamp=0，仍插入恒等 IIR 并算处理，符合骨架 K3。

## 5. OPRA 耳机校正（Q6）

### 5.1 数据与打包

- 上游：Roon Labs 维护的 OPRA 库（`github.com/opra-project/OPRA`），`database_v1.jsonl` 每行 `{"type": "vendor"|"product"|"eq", "id", "data"}`；EQ 的 `data` 有 `product_id, author, details, link, type, parameters{gain_db, bands[{type, frequency, gain_db, q, slope}]}`。按 OPRA 的说明，开源项目从 Roon Labs 的镜像 `opra.roonlabs.net` 取数据。
- 裁剪：只留厂商 `{id, name}`、产品 `{id, vendor_id, name, subtype}`、EQ `{id, product_id, author, details, link, parameters}`，丢掉介绍文字、标识与线稿图路径；`type` 不是 `parametric_eq` 的 EQ 丢弃（§6.9）。按 id 排序后写成 JSONL 再 gzip，放 `backend/internal/dspsettings/opra/data/opra-v1.jsonl.gz`，同目录 `SOURCE.md` 记上游地址、抓取日期、上游文件 SHA-256、许可（CC BY-SA 4.0）与署名要求。
- 体积：上游约 12 MB，裁剪并压缩后预计 1–2 MB，`go:embed` 进二进制，镜像不需要额外文件。
- 更新：`go run ./cmd/opra-update`（从 backend 运行；下载、校验字段、裁剪、排序、压缩、改写 `SOURCE.md`），由维护者在发版前手动跑并提交；CI 不联网。
- OPRA 标识图片由主会话放入 `frontend/src/assets/opra/` 并维护来源；Go 工具只写 backend 数据目录，不跨层写 frontend。最终随 `npm run build` 进入 `backend/cmd/roomusic/web`，不是手改带哈希的构建产物。
- 仿照 `backend/internal/catalog/textnorm/opencc.go` 与 `data/opencc/` 的嵌入方式，数据旁提交 `SOURCE.md` 和上游 `LICENSE`，许可证全文与来源说明一起 go:embed 随二进制分发；运行时不联网。1–2 MB 压缩量与 20–30 MB 常驻量是旧设计估算，实现时实测回报，不作为本次已验证数字。

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

段数超过 32 时按 OPRA 的“按优先级排序”截断前 32 段。频率、增益、Q 超出 §4 的设置范围时，映射时钳到范围内（如真实数据里的 8.5 Hz 段按 10 Hz，与算法 §1 的频率钳位一致），不丢段、不报错；更新工具回报被钳位的段数（2026-11-09 实现时核出，主会话裁定）。套用后 `source.kind = "opra"`，带厂商、产品、作者、说明与链接。

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

播放区里的 PEQ 经 zones 的 set_settings 写入（键 peq）；zones 自己规范化与校验，dspsettings 复用 zones 公开文档合同，不形成反向依赖。设置及预设文档非法均为 `400 invalid_input`，`peq_import_empty` 仅由新的 dspsettings/httpapi 显式映射；预设他人 ID 为 404 not_found。HTTP 注册接到 app，不能假设现有 zones handler 会识别这些新错误。

## 9. 接入（骨架 → SRC → 降混之后）

- **0047_playback_zone_peq.sql/.down.sql** 一起增加 `playback_zones.peq jsonb NOT NULL DEFAULT '{"enabled":false,"preamp_db":0,"bands":[]}'` 和 §7 的 dsp_peq_presets。zones 文档封装与规范 JSON 等值比较/新切片解码依骨架 §7；不能直接给可比较 Settings 塞入 []Band 或共享预设切片。
- 真正的组装在 `backend/internal/playback/render/session.go` 的链准备处：按 ③ 后格式将 zones 文档转 Section，`chain.Wrap(peq.NewIIR(...), f, f, step, true)` 插 `Spec.User`。卷积尚未接入时只有 PEQ；随后根据 convolution.before_peq 调整 User 顺序，chain 不导入 peq。
- `render/negotiate.go` 的计划与 `signal.go` 共用骨架 hasProcessing。开了 PEQ 但增益为零仍属于处理；关掉时不修改 Exact、无损 MD5 不变。实际全部段因声道下标不匹配而不生效也按已启用节点处理。
- `kind: peq`，params `{label,bands}`，`quality: enhanced, compat: false`；label 为来源快照显示名（OPRA：产品名 · 作者；预设：预设名；手动：参数均衡），bands 为当前生效段数。同步前端 model/decoder/stepIcons，否则新 kind 会被丢弃。
- zones 的 `SettingsPatch`、共同编解码、SQL、默认、HTTP 白名单及金样按骨架固定清单接入。预设引用只是快照元信息，不给它增加运行期归属查询；IR 引用才需要资源服务核对。

## 10. 界面（主会话实现）

在播放区设置里加“参数均衡”入口，打开编辑面板：

- 顶部：总开关、前级增益输入、来源名（OPRA/预设/导入）。
- 本地草稿与持久化分离：拖动时只更新草稿；沿用 `playback-settings-form.tsx` 的 GainSlider 约定，在指针松手、键盘结束、数值确认/失焦或“应用”时提交完整 peq，一次用户操作一次 set_settings；服务器已有 300 ms 去抖，每次提交都可能新建 seq，不能用 150 ms 持续自动保存。
- 频响曲线：调用 `peq/response`（输入后 150 ms 去抖，只预览不写播放区），多声道组时用不同颜色的线；不画网格以外的装饰。
- 段列表：每行类型下拉、频率、增益（低通/高通/带通/陷波/全通时隐藏）、Q（低通/高通显示斜率下拉）、声道（全部/左/右）、启用勾选、删除图标按钮；末尾“添加”图标按钮。
- 工具栏图标按钮：“耳机校正”（OPRA 面板）、“粘贴导入”（文本框 + 预览曲线 + 应用；有无法识别的行时显示一句“有 N 行没有识别”）、“预设”（列表：套用、改名、删除；“存为预设”输入名称）。
- OPRA 面板：搜索框、产品列表（厂商 + 型号）、点开后是配置列表（作者醒目、说明、来源链接、套用按钮）、底部署名（§5.4）。
- 文案遵守全局约束：不解释算法、不写权限或隔离说明；按钮能说明的不加段落。

## 11. 测试（最小集）

后端（测试 GPT）：

1. Cookbook 自检向量（§1，误差 ≤ 1e−8）与 `b1 == a1`。
2. 每种类型一个频点表：峰值在 f 处为 G、架类在转折 f 处为 G/2 且通带端趋于 G（±0.01 dB）；低通/高通 1–6 阶在 f 处约 −3 dB（±0.1 dB，Q 取两位小数带来的偏差在内）、f = 100 Hz / 48 kHz 时低通在 10f 处约 −20n dB（±0.5 dB）；带通在 f 处 0 dB；陷波在 f 处 ≤ −100 dB；全通各频点 0 dB。频响用系数直接算，不跑信号。
3. 固定 Q 表（§2 的 6 行）。
4. IIR 处理器：冲激响应与按系数直接递推的结果一致；声道只作用于指定下标；分块大小不同结果相同。
5. OPRA：用一份 3 行的小样本（厂商、产品、含 `peak_dip`/`low_pass` 的 EQ）测映射；嵌入的真数据只测“能解析、条数大于 0”。
6. 导入：一段真实格式的 AutoEq `ParametricEQ.txt`（10 段 + Preamp）与一行无法识别的行。
7. 预设接口：本人能增删改查、他人 ID 返回 404（一个用例）；设置规范化后的同值提交不重建、DTO bands 不共享复用骨架表测。

前端最小验收：编辑器加段、改类型后提交的文档正确，拖动期间不提交、松手只提交一次；导入预览后应用；OPRA 配置套用后显示作者与链接。各一个用例。

## 12. 分工与依赖

后端实现、独立测试均 `codex/gpt-6-astra`（high）；前端主会话直接写。dsp/peq（包括给 DSD 的 ButterworthLowPass）现在可开工；OPRA 数据裁剪和导入纯解析可先准备，但 zones 设置合同、资源仓储/HTTP/迁移与渲染接入须在骨架 → SRC → 降混之后。

PEQ 的 UI 阶数仍限 1–6；纯 ButterworthLowPass 供 DSD filter=3 调用时支持 8 阶，不能因为 UI 校验只到 6 就在纯函数拒绝 8。本次依赖登记含 dsp/peq、dspsettings、dspsettings/opra、dspsettings/httpapi 与 render/app 具体导入；离线工具在 backend/cmd/opra-update，不放 internal 的 cmd 子包。

## 13. 已裁决

- **OPRA 列表不带产品图片**：只显示厂商与型号文字（2026-11-08 用户采纳推荐）。数据包保持在 1–2 MB；不需要耳机外形图辅助辨认。

- **2026-11-09 接入合同补充**：预设响应固定为 `{preset_id,name,peq,created_at,updated_at}`；GET 列表用 `{items:[…]}`。`sample_rate` 省略时 48000，显式值只能是 8000–768000 的整数。
- **字段完整性**：峰值/架类必须 gain_db 与 q；低通/高通必须 slope_db，仅 slope_db=12 可额外给 q；带通/陷波/全通必须 q；不接受不适用字段。source 不可 null；opra 固定包含 kind/eq_id/vendor/product/author/details/link（字符串可空），preset 包含 kind/preset_id/name，import 仅 kind。
- **曲线分组**：始终保留 all 曲线，只含 all 段；显式声道叠加 all 段与自身段，按等效启用滤波集合合并声道，每条均计入前级。完整可执行合同见 [PEQ 后端规范](../../../../spec/backend/peq-contracts.md)。

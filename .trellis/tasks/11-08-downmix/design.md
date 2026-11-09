与第一批代码对齐：2026-11-09

# 技术设计：多声道降混

需求见 [prd.md](./prd.md)；父任务 [音频算法对标](../11-08-audio-dsp-reference-port/design.md)（下称“父设计”）。参考稿章节：§6.8、§6.1（第 ③ 步与第 ⑨ 步）。

接入以[公共骨架合同](../11-09-dsp-batch2-skeleton/design.md)为准；基线为 `main e29b65d`，新接口均为拟定。纯包与接入的启动条件见本任务 implement.md。前端由主会话写，不另派前端 agent。

## 0. 结论

- 纯包 `backend/internal/dsp/downmix`：声道布局掩码与显示名、**按扬声器类别生成矩阵**的构建器、矩阵处理器、“何时降混”的判定函数。
- 一个构建规则同时得到参考稿表里的三种矩阵（5.0/5.1→2.0、7.1→2.0、7.1→5.1），其他布局（3.0、4.0、6.1 等）按同一规则得到，不另写特例。
- D3：多声道源在声道不够的输出上**默认降混**，播放区设置可关（关掉后按参考稿取前几个声道）。这是对参考稿的偏离，理由已写在 prd。
- 无状态、无延迟。按父设计 §3.1 的处理器形状实现，经 `chain.Wrap` 成为链上 ③ 节点。
- 合入后替换第一批解码 ffmpeg 的 `-ac 2`（ffmpeg 默认矩阵），降混改由链上 ③ 完成（§5.2）。

## 1. 声道布局（D2）

- 32 位掩码，位序即流里的声道序号（WAVEFORMATEXTENSIBLE 的位定义）：

| 位 | 0x1 | 0x2 | 0x4 | 0x8 | 0x10 | 0x20 | 0x40 | 0x80 | 0x100 | 0x200 | 0x400 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 声道 | FL | FR | FC | LFE | BL | BR | FLC | FRC | BC | SL | SR |

  0x800 以上的顶部声道不参与降混（丢弃），音乐文件里几乎没有。
- 布局来自骨架 K5：`playback/sniff_wav.go` 保留 WAV 掩码，经 `RenderSource → app/renderer.go → render.Track → sourcePCM/Stage.In` 传入。非 WAV 当前没有 ffprobe/probe，不引入 `MaskFromFFmpegLayout` 或假设扫描已存布局。
- 默认布局集中在 `chain.DefaultLayout`，1–8 及 4→4.0、7→6.1 按骨架表；纯 downmix 包只收掩码，不导入 chain、不复制默认表。真实掩码优先，解码的声道顺序须和掩码一致。
- 9–32 声道、掩码未知且需要降到更少声道时，不套矩阵，改为只取前 N 个声道（经 `Tail` 声道选择），信号路径照常显示降混步骤；保证第一批用 ffmpeg `-ac` 能播的文件仍能播，不出现放不了的曲目（主会话 2026-11-09 裁定）。不改变无需降混的 WAV 多声道通路。
- 布局名（信号路径用）：0x4 “1.0”、0x3 “2.0”、0x7 “3.0”、0x107 “4.0”、0x33/0x603 “4.0”、0x607/0x37 “5.0”、0x60F/0x3F “5.1”、0x70F “6.1”、0x637 “7.0”、0x63F “7.1”，其他写“N 声道”。

## 2. 矩阵（D1）

### 2.1 构建规则

先给每个源声道定类别和相对增益，再乘基准增益：

| 源声道 | 到 2.0 | 到 5.1 |
| --- | --- | --- |
| FL、FLC / FR、FRC | 同侧 1 | 同名声道 1（FLC/FRC 并入 FL/FR） |
| FC | 左右各 −3 dB（双写） | FC 1 |
| LFE | 丢弃 | LFE 1 |
| BL、SL / BR、SR | 同侧 −6 dB | 并入同侧环绕，各 1（等权相加） |
| BC | 左右各 −9 dB（环绕 −6 dB 再双写 −3 dB） | 左右环绕各 −3 dB |

基准增益：到 2.0 时源 ≤ 6 声道用 −6.0 dB、> 6 声道用 −6.8 dB；到 5.1 用 −3.2 dB。

这是我们对参考稿表格的解读：“中置 −3 dB”“环绕 −6.0 dB”是相对前置声道的增益，基准增益乘在整行上；参考稿对 7.1→2.0 的后/侧环绕没写增益，我们按 5.1 的环绕同样取 −6 dB。

### 2.2 得到的三种矩阵（绝对增益）

| 场景 | L/R 前置 | 中置 → L、R | 环绕 → 同侧 | LFE |
| --- | --- | --- | --- | --- |
| 5.0/5.1 → 2.0 | −6.0 dB（0.5012） | −9.0 dB（0.3548） | −12.0 dB（0.2512） | 丢弃 |
| 7.1 → 2.0 | −6.8 dB（0.4571） | −9.8 dB（0.3236） | 后、侧各 −12.8 dB（0.2291） | 丢弃 |
| 7.1 → 5.1 | −3.2 dB（0.6918） | 中置保留 0.6918 | 环绕 = 0.6918 ×（后 + 侧） | 保留 0.6918 |

- 输出 5.1 的布局取 0x60F（侧环绕 SL/SR）；不要混用 ffmpeg 的后环绕布局名与这个掩码。
- 矩阵行和可能大于 1（例如 5.1→2.0 的左声道 0.5012 + 0.3548 + 0.2512 = 1.107），处理链是浮点，越界在定点编码时按削波统计（父任务 P8）；要留余量由用户开 Headroom。不在这里另加归一，保持参考稿的电平。

## 3. 何时降混（D3）

```go
// Target 返回需要降到的布局；不需要降混时 ok = false。
func Target(src Mask, outMaxChannels int, enabled bool) (dst Mask, ok bool)
```

- `enabled == false`：不降混（第 ⑨ 步声道映射按参考稿取前几个声道）。
- 源声道数 ≤ 输出最多声道数：不降混。
- 输出最多声道数 ≥ 6 且源多于 6 声道（6.1、7.0、7.1）：降到 5.1。
- 其余：降到 2.0。
- 输出声明少于 2 声道时先返回不降混（交给第 ⑨ 的纯选择），本批不新增单声道混合矩阵。

## 4. 处理器

```go
package downmix // backend/internal/dsp/downmix

type Matrix struct {
	In, Out Mask
	Gains   [][]float64 // [out][in]
}

func Build(src, dst Mask) (Matrix, error) // dst 只能是 2.0 或 5.1

// Processor 满足父设计 §3.1：输出声道数与输入不同，块长不变；返回的切片归处理器所有。
func New(m Matrix) *Processor
func (p *Processor) Process(in [][]float64) [][]float64
func (p *Processor) Latency() int // 0
func (p *Processor) Reset()       // 无状态
```

每个输出样本是一次长度为源声道数的点积；稀疏度不值得优化。进入链前 `decoder.read` 已清空增益区域；降混清 Exact，不另实现区域处理。

## 5. 接入（骨架与 SRC 合入之后）

### 5.1 ③ 与最终声道选择

`backend/internal/playback/render/negotiate.go` 取设备级 MaxChannels（1–32，非法值拒绝），非 WAV 再限到 8；不能只用浏览器固定 2。SRC 的 Stage.Out 作为本节点输入，目标按 `Target` 计算；`chain.Wrap(downmix.New(m), in, out, step, true)` 放入 `Spec.Stages` 的 SRC 后面。PEQ/卷积使用此时的声道与布局。

关闭 downmix 时跳过矩阵，③ 后仍是原声道；在骨架 `Spec.Tail` 的浮点出口按前 N 声道选择，使最终 outputPCM 不超过能力。这是参考稿 ⑨ 的纯选择前移至量化前的技术实现，不得又让 ffmpeg 混一次。单声道输出也由这个选择路径处理，不向用户承诺本批新增单声道混合算法。布局按选中声道构造；单声道交付采用标准 mono 标识，不能给一声道数据标 stereo。

### 5.2 真实文件

- `render/decoder.go/openDecoder`：去掉目标 `-ac`，保持源通道输出；若需要显式输出布局，只声明与源匹配的布局，不触发额外混合。SRC 已处理变率参数，基于其合入版本修改同一行，不从旧第一批版本覆盖。
- `render/negotiate.go`：sourcePCM 回到原通道；具体交付 format 与链输出一致，保留位深统一判定。`player.go/crossfade.go/transition.go` 的 ring、混音仍用 sourcePCM，编码/监测用 outputPCM。
- `render/signal.go`：去掉旧的 Track.Channels 与 pcm.Channels 比较后生成 ffmpeg downmix 的分支；实际节点给出步骤。声道只选不混合可用同一 downmix kind，params 增加 `mode: select`，显示为声道选择，不能误称矩阵降混。

### 5.3 设置与迁移

**0046_playback_zone_downmix.sql/.down.sql** 加 `downmix jsonb NOT NULL DEFAULT '{"enabled":true}'`，HTTP `settings.downmix` 也是完整对象 `{enabled}`。统一采用骨架的每算法文档方案，不单独用 boolean 列；旧版尚无该键，无需兼容一个从未上线的 boolean 协议。zones 规范化与比较、存储坏值默认 true、白名单、API 样本依骨架 §7。

### 5.4 信号与前端合同

`kind: downmix`，params 为 `{from_channels,to_channels,from_layout,to_layout,by:"roomusic",mode:"matrix"|"select"}`，`quality: high, compat: true`。保留现有 **by 必填**以减少协议改动；前端同时增加可选布局名/mode，旧的 `by: ffmpeg` 仍能解码。`zones/model.ts`、`decoders.ts`、`signal-path.tsx` 一起改，不能后端删 by 而前端整步解码失败。

## 6. 界面（主会话实现）

播放区设置里加一个开关“多声道降为双声道”，默认开，不配说明文字。信号路径组件的 `downmix` 步骤有布局名时显示布局名（“5.1 → 2.0”），否则显示声道数。

## 7. 测试（最小集）

后端（独立测试 agent）：

1. 矩阵：5.1→2.0、7.1→2.0、7.1→5.1 与 §2.2 表逐项比对（误差 ≤ 1e−9）；4.0→2.0、6.1→2.0 各一行抽查 BC 的增益。
2. 验收用的正弦测试：5.1、7.1 各声道放不同频率的正弦（如 300、500、700 … Hz，1 秒，48 kHz），过降混后对每个输出声道用 DFT 取各频率的幅度，与矩阵增益相比误差 ≤ 0.01 dB，矩阵里为 0 的成分 ≤ −120 dB。三种场景一个表驱动测试。
3. `Target` 判定：表驱动，覆盖开关关闭、声道够用、到 5.1、到 2.0。
4. 掩码与布局显示表测；接入复用骨架默认布局测试，增加一份带显式 WAV 掩码的来源到输出矩阵测试，以及关闭时只选声道、不经 ffmpeg 降混的测试。

前端最小验收：开关切换后提交的设置正确；信号路径显示“降混 5.1 → 2.0”。各一个用例。

## 8. 分工与依赖

后端实现与独立测试：`codex/gpt-6.1-sol`（high）。前端由主会话写。纯包现在可开工、无第一批依赖；接入必须等骨架 → SRC 合入 main，之后轮到本项，再交给 PEQ。默认布局归骨架，纯包不导入 chain；新增 dsp/downmix 与 render 导入须在 dependency_rules_test.go 登记。

## 9. 待裁决

无（D3 已由 prd 裁定默认降混）。

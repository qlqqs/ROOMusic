与第一批代码对齐：2026-11-09

# 技术设计：第二批公共骨架

依据：[prd.md](./prd.md) K1–K7、[代码核对](./research/code-reconciliation.md)，自行核实基线 `main e29b65d`。本文是第二、三批**接入合同**；父设计保留算法意图与参考稿章节索引，第一批尚未实现的接口设想不再作为接入依据。以下新增类型、函数名是拟定接口，不表示基线已有。本轮仅规划，等待主会话审阅，不启动实现。

## 1. 边界与保持行为

- 不实现 SRC、降混、PEQ、卷积或 DSD 算法，不新增用户设置键、迁移或界面；现有 Headroom 的坏值读取修正属于 K6。
- 第一批 `decoder.go` 中的 ffmpeg 变率/降混、`encode.go` 中的编码前变率暂时保留，分别由 SRC、降混接入删除。骨架空节点列表必须保持现有音频、HTTP 形状与信号路径。
- `backend/internal/playback/render` 负责组装，`dsp/chain` 不新增对具体算法包的导入；现存 `dither`、`leveling` 等第一批基础依赖保留。来源段在 `decoder.read` 内生成并立即施加电平化；共享链输入 `Regions` 必须为空，沿用 `requireApplied`，不设计跨 SRC 搬运增益区域的新链路。
- 会话格式及链不可变；跨曲目不断开共享节点，设置变化仍经 `zones/render_runtime.go` 的 300 ms 去抖重建。

## 2. K1：多节点与格式推导

在 `backend/internal/dsp/chain/chain.go` 扩展现有 `Spec`，保留 `Format/Headroom/OutputBits/Seed`：

```go
type Stage struct {
    Node Node
    In, Out Format
    Modifies bool
}
// Spec 新增 Stages []Stage、User []Stage、Tail []Stage。
// Build 顺序：Headroom(①) → Stages(② SRC、③ 降混) → User(⑤) → Tail → Quantize(⑧)。
func (c *Chain) InputFormat() Format
func (c *Chain) OutputFormat() Format
func (c *Chain) OutputFrame(inputFrame int64) int64
func (c *Chain) Latency() int
func (c *Chain) ModifiesSamples() bool
```

- render 顺序构建节点：先确定输入格式，SRC 构建后把 `Out` 交给降混，再把实际格式交给 PEQ/卷积；不能先用原曲格式构建所有节点。`Stages/User/Tail` 是实例而非全局工厂，实例归一个会话独占。骨架测试在包内装配测试节点，不为测试增加对外产品设置。
- `Build` 校验 `Spec.Format == 第一个 In`、相邻 `Out == In`、正采样率/声道数、非零掩码的位数等于声道数；Headroom 格式恒等。空列表的最终格式就是 `Spec.Format`。复制列表，不克隆带状态节点；不允许同一节点交给两个会话。
- 现有 `Node` 接口（`buffer.go`）不强制扩展；无延迟节点无需写空方法。`Wrap` 的 `Adapter` 新增 `Latency() int`，返回底层处理器的固有延迟（本节点输出帧）；直接节点可实现同名可选接口，缺省 0。非零延迟的直接节点必须自己完成与 Wrap 同等的丢头/冲刷，通常直接用 Wrap。
- `Chain.Latency()` 是内部需提前计算的时长，统一换算为**最终链输出帧**：`ceil(Σ Li * 最终输出率 / 第i节点输出率)`；只在求和后向上取整。不是已补偿音频上的额外时间偏移，不再从播放头减一次。Headroom、PEQ、降混的延迟为 0。
- `OutputFrame(n)` 顺着 Stage 的变率边界逐级执行绝对帧比例换算，与现有 `Adapter.scaled` 的逐级向下取整一致；不逐块取整再求和。多级 SRC 应封装为一个 Stage，由其纯包处理内部相位。事件映射和实际 Drain 长度必须用同一映射。
- `Process/Drain/Reset/Quantize` 沿用现有所有权与顺序；零帧输出合法，仍继续进料。`Drain` 不计新输入；仅输出真实输入对应的时长，不承诺额外播放整段混响尾巴。`ModifiesSamples` 来自 Headroom 启用或任一 `Stage.Modifies`；即使 0 dB/单位冲激，已启用节点也算处理。纯格式恒等且不启用的节点根本不插入。
- `Tail` 留给关闭降混后的声道选择：在用户处理之后选前 N 个声道，再量化。它与参考稿 ⑨ 的纯声道选择等效地前移到浮点出口，避免重复字节出口；不是上混或额外矩阵。此技术取舍由降混接入落实，信号步骤顺序忠实于实际计算。
- 保留已有 `chain.New(nodes...)` 给低层测试；生产渲染必须经 `Build` 获得完整格式与映射，不能从缺失格式的 `New` 推导事件。

## 3. K2：三种格式、两套帧域

`Track` 的文件事实与会话 PCM 必须分开：

| 名称 | 定义与使用 |
| --- | --- |
| `Track.SampleRate/Channels/BitDepth/IsFloat/Layout` | 文件元数据，信号路径 source、PCM 文件 CUE 截取；DSD 接入后 SampleRate 是比特率，不直接作 PCM 时钟 |
| `session.sourcePCM` | 来源段解码后、共享链之前的 PCM 格式；`decoder.format`、ring、player、crossfade 的单位。骨架仍是 ffmpeg 临时适配后的格式；SRC/降混接入分别恢复原采样率/声道 |
| `session.outputPCM` | `Chain.OutputFormat()`；量化、有损 f32le 打包、`newEncoder` 的输入格式、所有会话公共事件的帧时钟 |
| `session.format` | `zones.StreamFormat`，最终交付编码与格式；骨架阶段可与 outputPCM 不同（编码侧临时变率）；SRC 接入后率/声道相等 |

`concreteFormat` 改为返回具体交付格式与轻量 `renderPlan`（至少含 sourcePCM、outputPCM、处理事实）；只做格式/设置规划，不读 IR、不生成大核。`newSession` 消费 plan 并校验真实链输出与计划一致；不在 `Format()` 发布后改格式。沉重准备使用会话启动协程（见卷积设计），不延长 zones 锁内的 Open。

| 真实文件（均在 `backend/internal/playback/render/`） | 必须替换的使用点 |
| --- | --- |
| `session.go` | 字段 pcm 拆分；openDecoder 用 sourcePCM，newEncoder 用 outputPCM；HLS prune 秒数、NextChanged 的 sampleFrame、encodePCM 的声道步长用 outputPCM；run 中区分输入帧与 Process 后输出帧 |
| `player.go`、`transition.go`、`crossfade.go` | cursorFrames/endFrame、间隔、截断点、混音长度、ring 容量/就绪判断、decoder.position 均用 sourcePCM；不要改成链输出率 |
| `events.go` | timedEvent.frame、segment.frame、written/dispatched/nextTime、sampleFrame、queueEvent、dueEvent 全用 outputPCM；segment 另保存 inputFrame 供污染判断 |
| `monitor.go` | recordOutput 按 outputPCM 帧累加 written；速度分母为输出帧/outputPCM.rate，零输出的处理耗时仍累计；削波事件在当前输出块起点 |
| `encode.go`（含 WAV 出口） | 编码输入参数与 WAV 字节数使用 outputPCM；交付格式继续用 format；不假设源声道等于编码声道 |
| `negotiate.go`、`signal.go` | 计划来源段格式、输出格式及处理事实；转换两端明确，不再把 sourcePCM 当最终率；构建 SignalSteps 后才追加量化/编码 |

- 在 player→session 边界新增 `queueSourceEvent(inputFrame, event)`、`addSourceSegment(inputFrame, ...)`，内部只映射一次 `chain.OutputFrame`；`track_failed`、`track_started`、分段起点（包括交叉淡化首帧）走它。`recordOutput`、`stream_ended` 已是输出帧，继续调用 `queueEvent`，避免二次换算。
- 曲内时间 = `TrackStartSeconds + (输出播放头 - 段输出起点)/outputPCM.SampleRate`；流秒数 = 输出帧/outputPCM.SampleRate。舍入最多为边界映射误差，不因曲数或块数累积。编码器暂时变率只改变每秒编码样本数，不改变秒数语义。
- `waitToRender` 的 20 s 领先额度以源域**已提交帧**限流：`sourceBudget = floor((head/outputRate + 20s + Latency/outputRate)*sourceRate) - inputSubmitted`；每次最多 `sourceRate/5`。延迟额度只容纳管线内部已补偿的前瞻；现有长流低于 64 KiB 的额外一量子规则保留。按输出 written 限流会让长延迟处理器无限吞输入。
- 骨架保留现有 `sameSourceFormat` 的整数性/位深限制并加入布局；DSD 接入负责引入有效来源格式比较，不提前把所有不同整数位深的 PCM 宣称可无缝。

## 4. K4：已送入链与已输出必须分开

新增 `inputSubmitted int64`（sourcePCM 帧）。在 `session.run` 中，`player.read` 产生非空缓冲后、调用 `chain.Process` **之前**，在 `s.mu` 下增加输入帧数并发布原子快照。这样 Process 内等待、返回零帧或 encoder.write 阻塞时，控制面也知道新曲已经污染共享状态。Drain 不增加它；`written` 仍只在 encoder 成功接受输出后增加，绝不拿 inputSubmitted 推进播放头。

具体替换：

1. `events.go/publishChangeStateLocked` 的 `segments[i].frame < s.written` 改为 `segments[i].inputFrame < s.inputSubmitted`；快照保留 `written` 供播放头钳制，同时带输入提交边界、未听到换轨标志及其输出位置；分段显式区分初始段与后续换轨，不能把首曲起点当污染后继。
2. `events.go/hasUnheardTransition` 同样按 inputFrame 确认是否进入链，再按输出播放头确认是否听到；处理不足一个输出帧的下采样段时，以输入提交状态为准，不能仅用映射后的帧号大小猜测。
3. `session.go/NextChanged` 继续只读不可变快照、锁存通知时状态；不获取会话锁或调用 Cursor。通知发生于 player.read 与提交之间时，后续 `nextChanged` 再查提交后的状态，不丢通知。
4. `transition.go/player.nextChanged` 保留两个入口（锁存标志或 hasUnheardTransition）：只预缓冲、未送入链则关闭预取重选；已送入链且未听到则立即 `rebuild_needed`。不要只改这里而漏掉快照生产者。事件分段不能在相应污染判断完成前被提前裁剪。

`Chain.Latency()` 用于领先额度与诊断，不使用 `written + latency` 估算输入提交数：不同率、短块、EOF 都使估算失真。

## 5. K3：统一“有处理”判定

在 render 新建轻量处理事实类型及纯函数 `hasProcessing(facts) bool`，供 `negotiate.go` 位深选择与 `signal.go/ditherStep` 共用。输入事实：来源不是精确整数、来源段实际变率/变声道或 DSD 转换、实际电平化线性增益不等于 1、Headroom 启用、计划中任一已启用处理 Stage。`clip_indicator`、保留但关闭的设置不算处理；瞬时交叉淡化不算整曲常驻处理。

- `concreteFormat` 用 `leveling.Choose`（含首曲 AlbumContext）算实际增益，避免简单 `Leveling != off` 与 ditherStep 的 0 dB 特判分叉。候选后继还须检查在既有出口位深上是否安全；需要更高精度则走格式边界，不重设共享链位深。
- 无损默认 16 位仅当精确整数源 ≤16 位且 `!hasProcessing`；其余选 24 位，能力只允许 16 位时仍选 16 位并量化。`ditherStep` 由同一事实加“源有效位深 > 出口位深”决定是否展示，启用节点不能漏判。电平化恰好 0 dB 仍视为未修改，这是修复现有两处判定不一致。
- **实际抖动**继续依赖 `Buffer.Exact/SourceBits`，不改成仅依赖设置：crossfade 只清当前块 Exact，信号路径不展示瞬时抖动；每个修改样本的节点都清 Exact。关掉所有新算法保持第一批 MD5 回归。

## 6. K5：声道布局

路径：`playback/sniff_wav.go.renderWAVFacts.layout` → `verifyRenderWAV` → `RenderSource.Layout` → `app/renderer.go` → `render.Track.Layout` → 来源格式及 `concreteFormat` 的链计划。`zones.StreamFormat` 可带内部 `Layout uint32`（`json:"-"`）；不扩展本批设备能力协议。

- WAV 扩展头 `format[20:24]` 的掩码已有位数检查，仅未保存；保留非零有效值，零表示未知。普通 WAV 与其他当前容器没有布局探测，不新增 ffprobe，也不声称目录表已有布局。
- 在 `chain` 的格式辅助文件集中 `DefaultLayout(channels)`：1/2/3/4/5/6/7/8 分别为 `0x4/0x3/0x7/0x107/0x607/0x60f/0x70f/0x63f`；4 是 4.0，7 是 6.1。9–32 未知布局返回 0，不编造扬声器位置；已有 WAV 等声道透传仍可用。需要降声道而布局未知时不套矩阵、由降混接入改为只取前 N 个声道（见降混设计 §1），保证仍能播放。
- 优先显式掩码，缺省才用默认；原声道顺序需与掩码位序一致。现有 ffmpeg 若改变声道数，则 sourcePCM 布局采用实际临时转换目标；Track 保留原掩码。降混上线后读取按原布局输出，降混包只接掩码，不拥有第二份默认表。
- 能力实际是 `max_channels` 1–32（非法 >32 拒绝），非 WAV 再限到 8；没有离散布局/采样率集合。有效输出声道布局进入后继 PEQ/卷积构建。

## 7. K6：设置表示与 HTTP

### 7.1 推荐与备选

**推荐每算法一个 jsonb 文档列**：`sample_rate`、`downmix`、`peq`、`convolution`，Headroom 保留现有列。域内统一使用不可变的规范 JSON 值封装 `SettingsDocument{canonical string}`（字段不导出，MarshalJSON 输出对象，不是字符串）；在构造时解码到明确类型、补默认、校验再序列化，禁止直接包裹任意 RawMessage。

- 规范比较忽略对象键顺序、空白、数值拼写差异；有序 bands 数组保持顺序，集合型 channels 可排序去重；null 与具体值明确区分。`custom_rate` 的指针只在临时解码类型中出现，不参与结构体 `==`。
- `PlaybackSettings`、`ZoneSettings` 可共享同一可比较值类型/别名，`settingsOf` 不再逐字段搬运。比较统一 `EqualSettings`，当前可用 `==` 实现；克隆是不可变值复制。取出 PEQ bands 等临时配置时每次解码新切片，render 独占，不把内部缓存切片返回 DTO。
- Headroom 外部对象形状不变，可保留可比较类型，但必须进入同一默认/校验/坏值读取入口。
- 备选 A：一个 `dsp jsonb` 聚合所有键，只改一次 SQL，后续迁移只更新默认值；缺点是按子项发布/回退及坏值隔离更复杂，整文档容易误覆盖别的键。备选 B：直接存带切片/指针结构，统一深比较与深克隆；类型直观，但每次新增嵌套字段都要审拷贝边界。推荐不引通用反射框架，不混用以上三种。

### 7.2 单一编解码拥有者

- **设置类型、默认、规范化、纯校验放 zones**（建议 `settings.go`、`settings_documents.go`）；`dspsettings` 仍需要，但只拥有成员 PEQ 预设、OPRA/导入/曲线、IR 资源，不成为 zones 的依赖。它可调用 zones 的公开文档编解码；`render` 从 zones 得到临时类型后自行转为算法配置。
- 请求严格：未知键、整对象缺字段、非法枚举/范围拒绝；patch 只替换给出的算法**完整文档**，不是递归合并，未给出的键保持。可空仅允许文档中的 `custom_rate`、`ir_id` 等明示字段；设置对象本身不能 null。
- 读取宽容：持久化 JSON 解码或语义校验失败，仅该键回退默认；Headroom 同样做。`store_read.go` 不再因 headroom 解码失败让 LoadUser 失败。读取不自动写库；下一次真实 UpdateZone 才写规范值。数据库查询错误、队列损坏不伪装成 DSP 默认。
- 注入 logger（沿用 Service.Options.Logger，调整 LoadUser 调用链），每次加载对每个坏键记录一次 `zones.settings.invalid` WARN，含 zone_id、key、reason，不记原 JSON、路径或内容；无运行时循环日志。
- 骨架只用 Headroom 和测试文档验证基础机制；未来算法文档在所属接入提交注册，不能骨架先加四个 HTTP 键。

### 7.3 新增一键的固定清单

1. zones 的文档类型/默认/规范化入口、`SettingsPatch`、共享 settings 值增加字段；`validation.go` 只转调统一入口，`commands.go` 只用 EqualSettings/apply，不重复规则。
2. 所属迁移加 jsonb 默认值与 down 文件；`store.go/UpdateZone`、`store_read.go/loadZones` 的固定列、Scan 与参数一处成组维护；核实 `store_devices.go/CreateDevice` 的默认写入路径。**不为了省几行改成动态 SQL 注册框架**。
3. `service.go/defaultSettings/settingsOf` 使用统一默认/值复制；`devices.go/RegisterDevice` 与 Command 均复用校验。卷积资源归属另走服务端口（见卷积设计），纯校验不访问 DB。
4. `zones/httpapi/input.go` 的设置白名单与完整对象检查转调 zones 导出的文档形状校验入口，避免嵌套键再维护一份；通用重复键检测、1 MiB JSON 限制仍在 HTTP。
5. `frontend/.../zones/{model,decoders,api,controller}.ts`、`test/fake-zones.ts`、设置表单及信号 kind 同步；React 界面由主会话写，数值编辑松手/确认才 set_settings。

错误：设置统一 `400 invalid_input`，无字段路径，不新增 `peq_invalid`。资源不存在/非本人使用现有 `404 not_found` 语义，不泄漏归属；独立 dspsettings 的导入/上传错误在其 httpapi 明确映射，内部类型错误不能从 zones 泄漏为 500。

### 7.4 API 样本

不改协议外形、不把默认键省略以绕过金样。骨架无新键，现有样本应不变；每项接入只批量刷新 `TestPostgreSQLZonesAPIResponseSamples` 拥有的响应，再添加一份本项非默认请求/响应覆盖。不引入 54 份手写分支，也不全目录机械加键（有 settings 字符串不等于本项 owns）。沿用 `ROOMUSIC_UPDATE_API_SAMPLES=1` 与 recorder 的所有权检查，前端 decoder/fake 同一提交同步。真实响应若涉及其他 recorder，再按实际差异扩展一次。

## 8. K7：迁移与包归属

### 8.1 编号预留（当前最大 0044）

| 编号 | 拥有子项 | 内容 |
| --- | --- | --- |
| 无 | 骨架 | 不改 schema；规范化已有 Headroom 读取 |
| 0045 | SRC | `0045_playback_zone_sample_rate`，sample_rate 文档 |
| 0046 | 降混 | `0046_playback_zone_downmix`，downmix 文档 |
| 0047 | PEQ | `0047_playback_zone_peq`，peq 文档与 dsp_peq_presets 表 |
| 0048 | 卷积 | `0048_dsp_ir_files`，成员 IR 元数据与原始 bytea |
| 0049 | 卷积 | `0049_playback_zone_convolution`，convolution 文档；依赖 0048 |
| 无 | DSD、PCM→DSD | 复用现有来源/分析列；不得自行占号，确需迁移先更新本表 |

每号含 `.sql/.down.sql`；0049→0048 顺序回退。所有分支禁止 `00NN` 占位入库。若其他 main 任务先占号，由主会话统一顺延本表与未合入迁移；不重编号已应用迁移。

### 8.2 包与规则（路径均相对 `backend/internal/`）

| 包 | 谁创建、哪一步 | 允许的项目内依赖 |
| --- | --- | --- |
| `dsp/internal/refdata` | 骨架基础步骤，独立小提交可供纯包测试先取用；仅 Dir(t) 定位、缺数据跳过 | 无；只供带 refcompare 标签的测试使用 |
| `dsp/internal/fft` | SRC 纯包第一步；卷积取该纯包提交，不重复造实现 | 无；逆变换不缩放，调用方负责 1/N |
| `dsp/src` | SRC 纯包 | `dsp/internal/fft`；测试可用 refdata |
| `dsp/downmix` | 降混纯包 | 无；掩码是 uint32，默认布局由装配层提供 |
| `dsp/peq` | PEQ 纯包 | 无 |
| `dsp/convolution` | 卷积纯包 | `dsp/internal/fft` |
| `dsp/dsd`、`dsp/dsd/internal/gen` | DSD 纯包/生成器 | 无；生成器标准库，系数是自主生成产物 |
| `dsp/sdm`、`dsp/sdm/internal/gen` | PCM→DSD 纯包/生成器 | 生产无；测试用 dsd、fft、refdata |
| `dspsettings`、`dspsettings/opra`、`dspsettings/httpapi` | PEQ 接入；离线 OPRA 工具在 `backend/cmd/opra-update` | 主包→zones、dsp/peq、dspsettings/opra、platform/uuid；卷积接入再加 dspsettings/irload；opra 标准库；httpapi→本能力、zones、identity、platform/httpserver/uuid |
| `dspsettings/irload` | 卷积接入（格式解析可先独立准备） | dsp/src、dsp/convolution、playback/render/ffmpeg；不导入 render/zones |
| `playback/dsdio`、`playback/dsdpcm` | DSD 接入；前者中性容器/字节读取，后者来源 PCM 流 | dsdio 标准库；dsdpcm→dsdio、dsp/dsd、dsp/src、dsp/peq；不导入 render、analysis 或 chain |

`app/dependency_rules_test.go` 的 packageRules 对**实际存在的每个 Go 目录**登记（包括 internal/gen）；不存在的规则也会失败，不能预写一堆空规则。骨架只登记实际建的 refdata，不建无调用的空算法包。SRC 自己创建 fft；纯包主功能不依赖骨架，参考测试可等 refdata 小提交，不阻塞算法工作。

`render` 按接入次序显式获准导入 src/downmix/peq/convolution、DSD 时 dsdpcm；不导入 dspsettings，由 `render.Options` 的 IR 来源端口经 app 适配。`zones` 不导入 DSP 算法或 dspsettings，保持现有依赖边界。DSD 时 `library/parser`、`playback` 可导入 dsdio，playback 可导入 dsdpcm；analysis 只消费 io.Reader 与 PCM 输入描述。`app` 增加 dspsettings、dspsettings/httpapi、dspsettings/irload 及端口适配需要的类型依赖；实际用到哪个才登记哪个。规则测试目前只扫 `pkg.Imports`，不扫 TestImports/XTestImports；仍在设计中约束测试依赖，不借测试绕开 Go internal 可见性，不扩大为全仓架构工具改造。

## 9. 最小验收、风险与回退

最小新增集：一个多 Stage 变率/变声道测试（含零帧输出和 Drain）、一个带延迟的预取移除测试（先入链尚未输出，通知时锁存）、一组处理判定表（0 dB、电平化、启用恒等节点、16 位上限）、一组文档规范化/独立解码/坏 Headroom 回退测试、一组 WAV 掩码/默认 4 和 7 声道传递测试。复用已有会话 MD5/CUE/事件测试，不重写全套算法或 ffmpeg 测试。

骨架可能改变的只有错误修复：坏 Headroom 不再拖垮加载；0 dB 电平化不再让协商与信号判定分叉。无新处理情况下音频逐位保持。合入前按父 implement §5 跑一次完整 real e2e 与 mock e2e（第一批教训），日常只跑上述受影响包。回滚代码骨架无需 schema 回退；后续子项尚未合入时可整体回退，已接入后须逆序回退调用方。

## 10. 对核对文件的补充修正

- `renderer.go/Open` 是普通编码预留 2、WAV 预留 1，不是固定 Reserve(2)；DSD 还要扣掉来源解码进程，见其设计。
- `negotiate.go` 对非法 MaxChannels >32 返回错误，合法值内才与源声道数取小、非 WAV 再限 8；不是把任意声明钳成 32。
- `signal.go` 明确不为 Opus 编码侧变率单列 convert；其他编码可出现解码/编码两步，SRC 接入应删两处临时实现而非假设所有路径都两步。
- 卷积现稿的短段 OLA 本来就能每次返回等长输出，不必攒满 block；保留短段 OLA 作为首选，只有采用攒块实现才新增启动缓冲延迟，不能把计算批量与信号延时混为一谈。
- `TrackAnalysisCandidate` 现有 SourceKind 但无 Codec；DSD CUE 的 SourceKind 是 cue_virtual，不能仅按 SourceKind 分支，需从安全句柄重新嗅探父容器。
- 依赖规则检查还拒绝不存在目录的登记；API recorder 比较整份响应；两个数据库/real-e2e 脚本共用全局锁文件，独立项目名不代表本机可同时运行它们。

# ⑤ 并发解析 · 技术设计

需求见 [prd.md](./prd.md)。S 线第 4 项，以 [①](../11-08-scan-file-filter/design.md)（失败分类与保住登记）和 [④](../11-08-scan-incremental/design.md)（解析缓存、候选跳过）为前提：④ 先把要解析的文件减到“新文件与变化的文件”，本项把剩下的读盘工作并发。参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 部分 | 归属 |
| --- | --- |
| 并发度配置 `ROOMUSIC_SCAN_WORKERS` | `platform/config`（解析与校验）→ `app` 装配 → `library.Service` |
| 有序工作池（通用） | 新包 `internal/platform/workpool`：`Ordered`（遍历用的有序流水线）、`Map`（按输入顺序返回结果的有界并行） |
| 遍历流水线 | `library`（`scanner.go` 遍历回调改为“提交 + 按序应用”，新文件 `scan_pipeline.go`） |
| 指纹并行 | `catalog/ingest`（`resolveFingerprints` 每页用 `workpool.Map`），`PlanOptions.Concurrency` |
| OGG/Opus 流式解析 | `library/parser`（`ogg.go`） |

不改：数据库访问方式（仍是整个扫描一条持锁连接、一个 goroutine 读写）、暂存与写入顺序、身份规划规则、多根串行。

## 2. 数据流

```
遍历 goroutine（唯一访问数据库与做遍历的 goroutine）
  WalkDir 按字节序走每个条目 → 生成一个“槽位”追加到 FIFO：
    · 过滤/跳过/失败（① 的分类）、缓存命中（④）→ 槽位直接就绪，内容是要做的写入
    · 物理文件未命中缓存 → 提交解析任务给工作池，槽位等待结果
    · .cue → 遍历 goroutine 解析 sheet、查父音频缓存；父音频未命中的作为一个任务提交
  每追加一个槽位后：依次“应用”FIFO 头部所有已就绪的槽位
    应用 = 写解析缓存 / 构造观察 / 暂存 / 诊断 / HoldSource / 跳过计数 / feedback.processed
  FIFO 长度达到窗口（2 × 并发度）时：阻塞等待头部槽位就绪再应用
  遍历结束：按序应用剩余槽位
工作池 N 个 goroutine：只做纯文件读取（parser.ParseAudioFile），不碰数据库、不写共享状态
规划阶段：resolveFingerprints 每页 100 个路径 → workpool.Map 并行计算 → 按路径顺序写回与记诊断
```

因为所有写入都由遍历 goroutine 按 WalkDir 的顺序应用，暂存行、诊断的先后与串行完全相同；诊断 100 条上限截掉的也是同一批。

## 3. 接口合同

### 3.1 配置（R5）

- `ROOMUSIC_SCAN_WORKERS`：整数 1–16，默认 4。空值取默认；非整数或越界 → 启动失败，错误信息 `ROOMUSIC_SCAN_WORKERS must be an integer between 1 and 16`（与 `ROOMUSIC_AUDIO_ANALYSIS` 的校验方式一致，`config.go:185`）。
- 1 等价于串行（工作池只有一个 goroutine，行为与改动前相同）。
- `compose.prod.yaml` 增加 `ROOMUSIC_SCAN_WORKERS: "${ROOMUSIC_SCAN_WORKERS:-4}"`；`.env.prod.example` 增加注释行 `# ROOMUSIC_SCAN_WORKERS=4`；`core0-runtime-contracts.md` 环境变量段补一句。若 `scripts/check-production-compose.sh` 校验环境变量清单，同步更新。

### 3.2 `platform/workpool`

```go
// Ordered 是有序流水线：Submit 按调用顺序排队，结果按同样顺序交给 apply（在调用 Submit/Drain 的 goroutine 上执行）。
// 同时在途的任务不超过 window；window 满时 Submit 先阻塞应用头部结果。ctx 取消后不再启动新任务，已完成未应用的结果丢弃。
type Ordered[T any] struct{ /* … */ }
func NewOrdered[T any](ctx context.Context, workers, window int, apply func(T) error) *Ordered[T]
func (p *Ordered[T]) Submit(job func(context.Context) T) error // 返回 apply 的第一个错误或 ctx 错误
func (p *Ordered[T]) Ready(value T) error                     // 不需要计算的槽位，直接排队
func (p *Ordered[T]) Drain() error                            // 应用剩余结果；返回前等所有 worker 退出
func (p *Ordered[T]) Close()                                  // 取消路径上调用：停止并等待 worker 退出，不应用

// Map 以至多 workers 个 goroutine 对 items 执行 fn，按输入顺序返回结果；ctx 取消后尽快返回 ctx.Err()。
func Map[I, O any](ctx context.Context, workers int, items []I, fn func(context.Context, I) O) ([]O, error)
```

- `platform/workpool` 只依赖标准库，不含业务规则（符合“platform 只放进程共享的机制”）。新包要在 `internal/app/dependency_rules_test.go` 的 `packageRules` 里声明，并允许 `library` 与 `catalog/ingest` 导入它；`directory-structure.md` 的依赖说明同步（`catalog/ingest` 允许导入的内部包增加 `platform/workpool`）。
- worker 内部 panic 不捕获（与现有代码一致，解析器 panic 本来就会让进程崩溃）。
- `apply` 返回错误（例如持锁连接写诊断失败）时，流水线停止接收新任务，`Submit`/`Drain` 返回该错误，调用方按现有规则让该根不完整。
- 窗口取 `2 × workers`：保证 worker 不因为头部一个慢文件而全部空闲，同时把在途结果的内存限制在窗口内。

### 3.3 遍历回调的改造（R1、R3）

- 槽位类型 `scanSlot` 携带：条目相对路径、要写的诊断与保住登记、要暂存的观察（或 CUE 的一组虚拟观察）、要写的缓存项、是否计入 `processed`、是否计入候选数 `seen`。
- 解析任务只调用 `parser.ParseAudioFile(path)`（或 CUE 的若干父音频），返回观察或错误；**构造观察、判断失败类别、写任何东西都在应用阶段**。
- 取消：`ctx` 取消后 WalkDir 回调返回 `ctx.Err()`，调用 `Close()` 等 worker 退出；已经在跑的解析跑完后结果丢弃（解析器不接受 `ctx`，单个文件的读取有上限，见 3.5）。根随后按现有规则判为不完整。
- 单个任务失败（解析错误）在应用阶段按 ① 的规则处理（诊断 + 保住），不影响其他任务。
- 封面延迟加载（④ 3.4）与抓轨日志读取在保存阶段，仍串行（只在重写的候选上发生，量小）。

### 3.4 指纹并行（R1）

`resolveFingerprints` 每取一页（100 个 `fingerprint_path`）：

1. `workpool.Map(ctx, options.Concurrency, paths, fingerprinter.Fingerprint)` 并行计算；
2. 按路径顺序逐个执行现有的“写回指纹 / 记 `fingerprint_failure` 并暂缓 scope”逻辑。

`PlanOptions` 增加 `Concurrency int`（≤0 视为 1）。`rootFingerprinter.Fingerprint` 只做 `Stat`、containment 与 `parser.ContentFingerprint`，本身无共享状态，可并发调用。测试注入的 `service.fingerprinter` 同样需并发安全（测试桩加锁即可）。

### 3.5 内存上限（R2）

- **OGG/Opus**：`parseOggStream` 从 `os.ReadFile` 改为 `bufio.Reader` 逐页读取：页头 27 字节 + 段表 + 段数据；跨页拼包沿用现有 16 MiB 单包上限；头部包之后的音频包边读边喂给指纹哈希；时长取最后一页的 granule position。单个文件的内存占用 = 一页 + 一个包（≤16 MiB），不随文件大小增长。解析结果（标签、指纹、时长、错误类别）与改动前逐字节一致。
- **其他解析器**：实现时逐个核对 `flac.go`、`mp3.go`、`m4a.go`、`wav.go`、`fingerprint.go` 没有按文件大小整体读入（现状均用 `os.Open` 加有界读取；M4A 元数据单原子 16 MiB、总计 32 MiB，`parser.go:50-51`）；内嵌封面均有 16 MiB 上限。发现整体读入的同样改为流式。
- **并发后的总量**：在途结果 ≤ 窗口（默认 8）个，每个最多一张 16 MiB 封面加标签，最坏约 130 MiB；worker 自身的读取缓冲是常数。

### 3.6 进度（R4）

`feedback.processed` 在应用阶段按序调用，所以计数与百分比单调、与串行相同；`files_processed` 的含义不变（每个候选文件一次）。预计数阶段不并发（只列目录，不读文件）。

### 3.7 合同同步

- `library-scan-contracts.md`：新增“并发”一段（工作池只做纯文件读取，单一 goroutine 按遍历顺序写入，诊断顺序与串行一致，取消时丢弃在途结果）；OGG 解析的内存约束。
- `core0-runtime-contracts.md`：`ROOMUSIC_SCAN_WORKERS`。
- `scan-feedback-contracts.md`：注明并发下 `processed` 仍按遍历顺序推进。
- `directory-structure.md`：新包 `platform/workpool` 与 `catalog/ingest` 的允许依赖。

无迁移，无接口变化，无前端改动。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 记录构造并发度固定 4、全局共享（7.1，620–628；19，2046） | 改写 | 默认 4，可用 `ROOMUSIC_SCAN_WORKERS` 在 1–16 调整（用户裁决）；一次只扫一个根，不需要跨根共享 |
| 工作队列消费并发 1，避免并发写状态（7.1，624–625） | 照搬 | 遍历与数据库写入是同一个 goroutine |
| 扇出 + 屏障汇聚：全部投入调度器后统一等待（7.1，628） | 改写为有序滑动窗口 | 屏障要把一整批结果留在内存里；滑动窗口边算边按序写，内存有界，诊断顺序与串行一致 |
| 单文件失败在任务内捕获、批次结束后统一抛出（16.4，1904–1912） | 改写 | 失败在应用阶段逐个按 ① 的规则处理，不抛出整批 |
| 失败指数退避重试（7.2，630–644） | 不采用 | 读失败的文件由下一次扫描自然重试（① 保住、④ 不缓存失败） |
| 所有来自文件的自述长度设上限（7.4，655–676；20-7，2158–2159） | 照搬（补 OGG） | 已有的封面 16 MiB、M4A 原子上限保留；OGG 由整文件读入改为流式 |
| 进度上报节流 0.5 秒（6.4，323–325；19，2049） | 不改 | 现有 256 单位或 1 秒的刷新边界（扫描反馈合同）不变 |

## 5. 兼容与已有数据

- 结果与串行一致：暂存内容、诊断、身份规划、写入都不变，已有数据不受影响，不需要重扫。
- `ROOMUSIC_SCAN_WORKERS=1` 可随时回到串行行为。
- 部署：不设该变量时默认 4；并发读同一块盘可能比串行快得有限，按 `music/` smoke 的结果再定是否调整默认值。

## 6. 风险与回滚

| 风险 | 应对 |
| --- | --- |
| 并发引入数据竞争（共享 map、feedback） | worker 不触碰共享状态；`go test -race` 覆盖工作池与扫描流水线 |
| 应用阶段的写入顺序与串行不一致 | 集成测试比较 1 与 4 并发下暂存行（相对路径序列）与诊断序列完全相同 |
| 取消后 goroutine 泄漏 | `Close()` 等待 worker 退出；测试在取消后断言在途 worker 数归零（不引入新依赖） |
| OGG 流式改写引入解析差异 | 用现有 OGG/Opus 测试样本对比改动前后的观察与指纹逐字段相同 |
| NAS 上并发读反而更慢 | 环境变量调低；默认值按 smoke 结果再定 |

回滚：设 `ROOMUSIC_SCAN_WORKERS=1` 立即回到串行；代码回滚无数据影响。

## 7. 线间关系

- 本线前置：① 的失败分类（应用阶段调用）、④ 的缓存命中（槽位直接就绪）与 `PlanOptions`。
- 共享文件：`library/scanner.go`（遍历回调第三次改）、`catalog/ingest/identity_plan.go`（`resolveFingerprints`）、`library/parser/ogg.go`、`platform/config/config.go`、`compose.prod.yaml`、`.env.prod.example`。
- **M 线 ⑩** 若新增格式解析器，需满足“流式、不按文件大小整体读入”与“可被并发调用”（无包级可变状态）；写进 ⑩ 合入核对项。
- **T 线 ⑥⑦⑧** 修改 parser 时同样不得引入包级可变状态。

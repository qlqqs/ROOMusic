# 固定样本与计时设计

## 范围和入口

| PRD 事项 | 实施交付 | 核对依据 |
| --- | --- | --- |
| 固定样本 | 下表的合成音频、模拟发行目录、两种运行方式各自的答案 | ADR 0009 第 3、4、6 节，按扫描成员、实际外部编号和自动层写入评分 |
| 回归入口 | 扩展现有 `TestPostgreSQLCurationMetrics`，一次调用依次跑程序、程序加脚本模型 | 逐张对照、两份汇总、相对基线的差值 |
| 基线 | `.trellis/tasks/11-26-curation-fixed-samples/research/baseline.md` | 本地 main 的产品代码，记录提交、设置、两轮原始测量和当前缺口 |
| 空等计时 | `.trellis/tasks/11-26-curation-fixed-samples/research/idle-gaps.md` | 父任务 A3 的程序轮，固定响应延迟、真实时钟、逐请求时间线及归因 |

在仓库根目录运行以下命令。这里定义的是实施后的入口。

```sh
ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

脚本负责一次性 PostgreSQL 和固定的媒体工具。两个模式各开新的测试 schema、曲库根、缓存、mock 和模型状态，按清单顺序运行。现有入口未选 suite 时仍跑原来的 142 张曲库及旧断言。测试说明放在 `.trellis/spec/backend/quality-guidelines.md`。

## 样本、模拟目录和答案

在 `backend/internal/app/testdata/curation_regression/fixed.json` 按固定顺序列出样本。通过 `source_sample` 引用同目录 `samples.json` 里的已有数据，新输入沿用 `metricLocal`、`metricExternal`、`metricExpect`。在固定清单中明确给出模式、唯一目标结论、外部编号、扫描成员路径/碟序及允许和必需写入的实际值。复用数据的变体只列改动。新建外部发行、组、录音使用互异且固定的合成 MBID。模型脚本独立于评分答案。

表内“版”表示目标发行对应、发行组与逐轨录音按清单核实，空缺字段只补该版提供且允许写的值。清单对每张枚举字段和值，对已有本地值注明保留。表内“组”只建正确的组级对应，年份缺失可补组首发年，测试特意提供不同的版级厂牌、条码以验证未写。“原样”要求外部发行、组、录音对应为空、外部自动字段无新增，另查表内写明的本地处理。每一行是一张预期本地专辑，甲、乙共用目录时也各占一行。

| 样本 ID 和输入 | 仅程序 | 加脚本模型 | 扫描、写入与请求证据 |
| --- | --- | --- | --- |
| editions-model-quartet，已有同组四版、同艺人同名、每版五首，本地也五首 | 组 | 组 | 曲名均有标签，补组首发年；程序直接认到组，版详情读取和模型识别调用均应为零 |
| embedded-artist，中性目录，专辑标签“堤灯社 - 微雨塘”，艺人堤灯社，目录仅收录《微雨塘》 | 未匹配 | 版 | 程序原样；本地辅助取原文片段“微雨塘”，再由程序核对，补该版厂牌 |
| one-char，合成标题“枝航圃”，目录仅有“枝航谱”，四首时长一致 | 未匹配 | 版 | 脚本提供正确写法再查；核对后可在名字差异项写正确标题，识别完成独立计分 |
| romanized，Hoshi no Mado 对“星の窓”，本地艺人有值，四首，不给编号 | 未匹配 | 版 | 脚本给日文搜索线索；建立对应，保留本地罗马字显示 |
| legacy-one，沿用 legacy-id3v1 的原始字节和单解判定，配置唯一同名外部版 | 版 | 版 | 扫描解出“去靛雾”，用解码名搜，补版级空字段，核对原始字节未改 |
| legacy-many，沿用 legacy-latin1 的双解字节，合成目录含“靛雾”版 | 不查 | 版 | 无编号和声纹，缺可用专辑名；程序保留原文且查询不含乱码，脚本在现有候选中选编码，由程序解码核对 |
| sampler-five，沿用 sampler-even 的生成方式，至少五位曲目艺人，各一首，无专辑艺人 | 版 | 版 | 扫描为群星合辑，模拟目录署名群星，逐轨署名和录音保持对应 |
| disc-folders，已有 CD1、CD2 | 版 | 版 | 四个成员的碟/轨次依次为 1/1、1/2、2/1、2/2 |
| named-bonus，CD1 - 初声、CD2 - 夜声、Bonus 三目录，模拟版三碟 | 版 | 版 | 同一发行，前两碟有标题，附加碟排在其后，核对录音顺序 |
| filename-only，中性目录，仅有“01 镜舟 - 昏灯.flac”等文件名，脚本答无法判断 | 不查 | 不查 | 专辑名取目录，标题保留“镜舟 - 昏灯”，艺人为未知，无外部写入 |
| name-only-gap，正确发行 MBID 和结构，一首曲名只差一个同文种汉字 | 版 | 版 | 两轮均立即算识别完成；程序保留原曲名，脚本判错字后另外核对自动层曲名 |
| mixed-contiguous，同目录两种专辑名，标签及文件名曲号 1–4，标题有明确多数，无编号 | 版 | 版 | 一张四首，外部仅有多数名的版，核对成员与逐轨录音 |
| mixed-a，同目录甲的轨号 1、2，与乙重号，文件名也不能组成连续序列 | 版 | 版 | 按甲专辑名分，甲成员只对应甲版 |
| mixed-b，同目录乙的轨号 1、2，两版各有唯一名字与艺人 | 版 | 版 | 按乙专辑名分，乙成员只对应乙版；脚本保留扫描分组 |
| amp-comma，合成单一艺名“棱镜 & 林, 海”，各曲同一署名 | 版 | 版 | 扫描及外部均为一人，核对姓名、署名位置及真实艺人对应数 |
| picard-01，已有文件内专辑 MBID | 版 | 版 | 直接读该版，保留已有年份等标签，核对新增对应 |
| cue-01，已有合成整轨及 CUE | 版 | 版 | 用 TOC 生成 DiscID，检查 discid 请求和版级空厂牌、年份 |
| identifier-01，已有条码 | 版 | 版 | 用清单条码命中，核对对应，保留条码 |
| catalog-label，复用 identifier-04，补上与外部一致的本地厂牌 | 版 | 版 | 唯一目录号和厂牌命中，补空条码，保留厂牌 |
| isrc-only，四首有 ISRC，无更强编号 | 版 | 版 | 从录音追溯到唯一版，有真实 ISRC 请求和结构佐证 |
| fingerprint-only，四段可解码的合成音频，占位标题和艺人，无编号 | 版 | 版 | 程序实算指纹，AcoustID 返回录音及发行，核对写入来自查回的发行、曲名和录音 |
| id-conflict，文件 MBID 指向实际存在、曲数不同且时长有反证的发行 | 有冲突 | 有冲突 | 两轮原样；脚本定不了，冲突后没有按更弱线索搜索 |
| unlisted-01，已有合成网络专辑，名字齐全，上游目录无此张 | 未匹配 | 未匹配 | 有搜索、零结果，原样；记录所查来源 |
| spoken-podcast，已有样本，补扫描约定可读的播客标记 | 不查 | 不查 | 名字齐全也无识别请求及外部写入 |
| unknown-disc-a，父目录下“晨册”子目录，标签可独立组成甲专辑 | 未匹配 | 未匹配 | 与“暮册”各是一张；目录均无命中，脚本保留分组，原样 |
| unknown-disc-b，同父目录下“暮册”，标签与甲区分 | 未匹配 | 未匹配 | 按乙的成员单独评分，原样 |
| missing-position，无碟号、曲号重新从 1 起，另有一首完全无编号 | 未匹配 | 未匹配 | 同碟、不推断第二碟；无编号项按文件名排序，读标签/来源断言未补轨号，脚本保留，原样 |
| field-tie，四首年份两两打平，名字齐全，目录零候选 | 未匹配 | 未匹配 | 扫描年份空且有字段冲突，和专辑识别结论分别记录，原样 |

对只凭模型名字命中的三张，模拟目录给出至少四首有实测时长的同序录音，排除额外的编号、别名、宽松搜误中。目录名格式可规则确定的样本照扫描合同执行。声纹仅在对应样本配置合成密钥及工具；其余用同一声明配置关闭声纹，以便观察名字、编号和不查分支。旧编码两张均提供可用艺人和足够曲目来作为专辑检索，沿用原字节而非复制旧单曲数据。

## 数据流、接口与落点

1. `backend/internal/app/curation_metrics_fixture_integration_test.go` 复用 `writeMetricSample`、`metricLegacyMP3`、`writeMetricCue`、`writeMetricCatalog`。后者已经将样本外部数据写成 `upstreammock.Options.DataDir` 可加载的 `catalog.json`，包括 CUE 的 DiscID。随本次样本补 `Fingerprints` 映射。声纹素材用 `curation_b3_fingerprint_integration_test.go` 的音频生成器和固定版 fpcalc 对相同音频建立 mock 查表键，曲目到期望录音的映射由清单指定。合成音频不入 Git。内置 demo 目录无需维护第二份答案。
2. 在 `backend/internal/app/curation_metrics_integration_test.go` 的现有测试入口选 suite，固定回归的驱动集中到同包 `curation_fixed_samples_integration_test.go`。只跑程序时关闭模型 worker 且不设端点；带模型时将实际提供方和当前审查配置指到同一 `httptest.Server` 的 OpenAI 兼容端点，批大小 1。模拟源统一取 `config.Server.UpstreamMockURL`。与识别无关的封面、歌词、艺人资料来源在两轮一致关闭。旧模型工具需要的外部检索只接模拟源。
3. 使用 `backend/internal/upstreammock/script.go` 现有按工具、用户消息、工具结果匹配的首条规则及当前巡检、选择、搁置、审查回包。固定回复放 `backend/internal/app/testdata/curation_regression/fixed-script.json`，用 `Options.ScriptFile` 加载。需回传动态项 ID 的工具动作从当前单项请求解析实际 ID，可在 `script.go` 的现有回包分支仅给固定样本规则绑定该字段。沿用 `oracle.go` 当前项的可见候选、工具结果核对方式，评分清单不传给模型，识别线索仅使程序查询。当前接口支持多少就实测多少；新本地辅助和识别判断接口落地时分别由子任务 8、9 更新脚本。
4. 用正常扫描发现文件。以相对路径集对齐预期专辑，CUE 另按 sheet、碟/轨定位；同时保存每个实际发行的全部成员。当前 `metricScannedEntry` 在同样本被拆成多张时会 Fatal，固定回归须在入口收集完整实际分区，输出拆错、并错和对应，不让一张影响其余样本。对多张同目录的输入，一次写齐并扫描；在快速回归里逐个放行已扫描发行的确认。沿用 `scanAndConfirmCuration` 只回拨本次目标的 `curation_changed_at`，再正常确认扫描。当前确认扫描会检查整根，领取条件还要求距成员变化满 60 秒（视图 `curation_stable_releases`）；在快速夹具中将同批稍后才处理的实际发行的变化时间置于本轮结束之后，轮到时再回拨。按视图验证本批只有目标可领取。每个处理窗口关联到目标的路径集合；错误合并的请求只记到第一个稳定排序的成员样本，其他受影响样本列共享归属。拆出的多张分别放行，再汇总到同一个预期样本。
5. 用现有测试时钟推进程序轮次，快速模式将来源等待设为零，记录实际经过的时间。识别到版/组在对应及该层写入提交时记完成；负例在本轮投影、查询和可执行的识别工作结束时定结论。程序模式下待模型项可留待配置。名字核对单列其写入结果，附带项的等待不改变专辑完成时间。在收齐本窗口已发出的请求及脚本调用后再开始下一张。沿用旧 `metricDrainResult` 的轮次上限和进度观测，按固定模式选终止条件，不能要求每张均完成巡检。超时保留未结束状态和已见请求，报告运行不完整。
6. `curation_metrics_scan_integration_test.go` 的扫描字段、结构和禁止查询检查，以及 `curation_metrics_values_integration_test.go` 的 `metricStoredLinks`、`metricStoredFields` 供固定评分器使用。读取 `curation_work_states` 的状态、结果、冲突，`curation_pending_items` 的处理记录和 `external_links`、有效字段、操作前后值。用“查过且明确零结果”判未匹配，用识别阶段跳过且无识别查询判不查，编号候选有反证判有冲突。运行中、来源错误或无终态单列实际状态。名称核对、艺人资料等待单列。完整曲库原有的安全终态集合及 `Complete`、`Accepted` 适用于旧报告。

## 四项度量与基线

在同包 `curation_fixed_report_integration_test.go` 输出每张的 `id, mode, expected, actual, external_ids, writes, scan_diff, requests_by_endpoint, elapsed_ms, issues`。相对路径和固定外部编号用于对比，运行生成的本地 UUID 不进对比键。

- **认对几张**。数认到清单指定版或组、结构及必需识别写入都对的本地专辑，同一张最多加一。正确的未匹配、有冲突、不查分项报达标张数。另列已正确对应但缺写、应识别但未认出的张数。
- **认错几张**。外部版/组/录音对应错误、负例建立对应、错误合并或实际写入错误自动值的专辑按 ID 去重，列目标值、实际值和操作来源。附带项未完成不算错。遗漏与不安全写入分开显示。
- **每张发几次请求**。计业务窗口里 mock 实收的每次出站，包括重试和脚本模型调用，排除管理、配置探测、缓存命中和本机进度查询。沿用 `metricCounters` 的服务/端点计数，按请求路径细分 MB 的实体、search、lookup、discid、isrc，模型推理单列。输出逐张明细、MB 小计、全来源小计、总数除以全部样本张数的均值（含零请求）。同窗多次尝试保留，逐张之和应与全轮端点增量相等。
- **用时**。用单调时钟从该目标放行确认扫描开始至识别和必需写入完成；未匹配等负例取确定结论的时刻。拆成多张的样本合计各处理窗口。每种模式另报全体样本从首次放行到最后一张结论的墙钟时间，记录含准备、编译及数据库启动的命令实际耗时以验证“几分钟”。快速模式用时只能与同配置快速基线比较。真实调度的诊断时间另列。

以产品基点 `38d7a63270513907e3082228b18534823df51a7b`（设计时本地 main）加测试驱动测量，先于任何业务修复。两次全样本冷启动核对结论、写入、端点计数一致，时间逐次原样保留并报差值，机器负载会影响毫秒值。选择首个完整运行为比较基线，同一数据导出 `backend/internal/app/testdata/curation_regression/fixed-baseline.json`，并在任务的 `research/baseline.md` 列出逐张、逐模式数据和已知差距。机器基线随回归曲库存放，Trellis 任务归档后仍可用。常规命令只读基线，按 `mode + id` 给出各数的“本轮减基线”，请求与用时不设新的阈值。旧代码未做到的目标如实报差异；每个错误按模式、样本、目标、错误类别和错值与基线比较。已知错认标红并列负责子任务，只有新增错认让常规回归失败；首次采集保留全部错误证据并成功结束。认对张数减少只报差值，最终总验收要求认错为零。

## 空等如何定位

选 `ROOMUSIC_CURATION_METRICS_SUITE=idle` 复用同一集成入口，只跑程序。选定固定的 MBID 命中、同组多版、零结果几张一次入队。参照 `backend/internal/app/curation_live_corpus_integration_test.go` 的程序轮配置（轮询 2 秒、完整盘点 30 秒、仅开 MB），在回环 mock 设 `slow`、`delay_ms=100`。使用正常 MB 限速、实时 `Now/After` 与真实的成员稳定期和确认扫描；所有样本一起经历一次静默期。起始静默、最后一次请求后的写入分别计时。旧 `liveTransport.musicRequest` 会将地址改向外网，此处只复用时间和端点分类方法。

在 `backend/internal/app/curation_fixed_timing_integration_test.go` 给 mock HTTP handler 外包一层计时，记录相同单调时间原点的序号、端点、请求进入及完整响应返回时刻、状态。mock 本身的请求日志只有开始时刻且最多 500 条。用 `runtime/trace` 的用户事件标记 HTTP 和进度采样边界，结合 goroutine 阻塞/唤醒栈、`program.Options.After` 的原时长计时器、`sources.NewLimiter` 按声明间隔的计时器、`scan_runs` 实际起止和队列 ready/running/retry 快照核对各段。保留调度器内部 `time.NewTimer` 的真实等待。观察器不能推动程序，也不能把采样间隔本身算作队列轮询。

对相邻的无重叠请求算“下次开始减上次结束”，有重叠则用在途请求区间并集求空档。把每个正空档按时间分段，列两侧请求、起止、持续毫秒、阻塞中的业务栈/事件、队列状态和对应源码函数。依次核对 `backend/internal/curation/program/queue_worker.go` 的 Run、drainQueues 和 `queue_progress.go` 的 nextDelay、`worker.go` 的 ready/rest/wait、`queue_query.go` 的领取条件、`backend/internal/library/curation_observations.go` 的 StartCurationConfirmation、`backend/internal/sources/limiter.go` 的 acquire 和 release。扫描在运行并不等于该请求一定在等扫描，要能在关键路径上看到阻塞与恢复；并行等待只计一次。无证据部分列残余毫秒并对该段定向复测。对可疑原因一次只改诊断参数（如只调整轮询间隔），核对相应空档是否随之移动。报告列请求占用并集、各类空档之和、首尾耗时并与整段墙钟时间对账。旧真实轮的 4–15 秒只作参照。

## 兼容、取舍与回滚

测试读现有表，数据库迁移为零，产品 REST 接口无新增。基点为 `parser.CommonVersion=5`、`ingest.GroupingVersion=7`、`program.LocalRuleVersion=3`、`program.MatchRuleVersion=104`，本任务不升业务规则版本。回滚测试驱动、固定清单、机器基线与测试说明即可；已有实例无需数据回退。样本和测量先行，若发现仅数行且只归本任务的空等修复，须先保存未修基线、定位证据和修复后同设置的对照；属于扫描、重试、排队、调度的改动交相应子任务。

## 主代理决定

历史真实轮只留有请求账，无法从固定延迟的成功响应逐一证明过去十处空档的原因。如果模拟样本没有重现某一类空档，按实测报告本次各段、可证实的原因和历史仍未知的部分。若旧代码已产生错认，保留红色结果和逐张清单，由主代理分派对应规则任务。

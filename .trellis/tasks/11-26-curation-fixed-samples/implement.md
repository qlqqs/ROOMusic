# 固定样本与计时实施顺序

本计划从已批准实施时开始。所有命令从仓库根目录执行。实现代理负责样本、现有回归入口的扩展、实测和两份研究记录；新增独立测试用例由测试代理编写。下列三个新增 Go 文件属于 app 包的测试装配，只放现有入口调用的驱动、评分与计时函数。

### 1. 固定产品基点，准备输入和权威答案

- 先核对 `git rev-parse HEAD main`。用设计记录的产品提交测量，即使本地 main 在并行实施中前进，也标明实际 commit。核对基点后的差异只有回归设施；保留产品代码和默认配置。
- 新增 `backend/internal/app/testdata/curation_regression/fixed.json`，把设计的每一行落为一个稳定 ID。复用已有样本时读同目录 `samples.json`，逐项覆盖本任务的输入/答案。固定混放目录的全体文件、两个模式的目标、应保留/应写的值、禁止的对应与查询。两个旧编码输入要先确认候选个数，实际音轨要能走专辑识别。
- 扩展 `backend/internal/app/curation_metrics_fixture_integration_test.go`，复用已有 FLAC、CUE、ID3 原始帧和模拟目录生成器。声纹样本用固定音源参数和固定版工具，先从生成音频算 mock 的 fingerprints 键，再供后台正常识别。其余样本走已有快速素材路径。
- 本步验证清单格式、旧样本加载、已有来源路由。具体新增样本的扫描结果留在第 3 步由真实扫描取证。

```sh
python3 -m json.tool backend/internal/app/testdata/curation_regression/fixed.json >/dev/null
(cd backend && mise exec -- go test ./internal/app -run '^TestCurationLiveGapSampleCoverage$')
(cd backend && mise exec -- go test ./internal/upstreammock -run '^(TestMockMusicBrainzRoutes|TestMockSourceAndSearchRoutes)$')
```

### 2. 接通当前协议的脚本模型

- 新增 `backend/internal/app/testdata/curation_regression/fixed-script.json`，在 `backend/internal/upstreammock/script.go` 复用现有首条命中、探测、巡检、识别和审查回包。需要回传的动态 item_id 取自当前项。对错字、罗马字提供明确的搜索字符串；受限于当前协议的步骤在报告中留下现状。冲突和未收录反例脚本需明确给出保留结论。两种运行方式使用相同的外部目录。
- 第 3 步的运行窗口计脚本动作及其后实际识别请求。对运行时标识、查询结果的核对借用 `oracle.go` 的现成解析；不调用带目标答案的旧度量模型。子任务 8、9 合入新模型接口时分别维护该文件对应规则。
- 本步先验 JSON 和现有 mock 的协议行为，第 3 步验证固定脚本的业务回包确实被模型 worker 消费。

```sh
python3 -m json.tool backend/internal/app/testdata/curation_regression/fixed-script.json >/dev/null
(cd backend && mise exec -- go test ./internal/upstreammock -run '^(TestMockModelRoutes|TestOracleEvidenceScope|TestOracleLookupReadsBeforeSubmitting)$')
```

### 3. 接入现有集成入口并输出可计分的观测

- 改 `backend/internal/app/curation_metrics_integration_test.go`，在设置旧 oracle 默认值之前分流 `ROOMUSIC_CURATION_METRICS_SUITE=fixed`。在 `backend/internal/app/curation_fixed_samples_integration_test.go` 组装两套隔离的应用、模拟上游、程序及模型时钟。复用库根、扫描、确认、模型配置和来源设置的现有测试助手；如旧 helper 绑定 oracle，仅抽出共用配置步骤。
- 按输入目录批次跑正常扫描，再按样本放行识别。取所有相对路径、来源状态、组/版/录音对应及操作前后值。允许产品扫描分区偏离预期，继续收全 28 行。记录发到模拟源和模型的每一次业务请求；区分 release/search、release/lookup、artist/search 等，和 mock 的每服务计数对账。首个计数边界在凭据探测和 provider 设置完成后，最后一个在业务请求结束后。
- 在 `backend/internal/app/curation_fixed_report_integration_test.go` 实现五类预期结论、未结束/错误的实际状态、写入对照和四个数。逐张解释已对上但缺写、错认、正确跳过、不符扫描预期的样本。采集模式 `fixed-capture` 和普通 `fixed` 共用驱动与评分器；采集模式无需预先存在的机器基线。两者都先输出明细再检查错认。
- 先复用 `ROOMUSIC_CURATION_METRICS_SAMPLES` 只选以下形态做一次端到端验证；同目录成组选择。旧评分逻辑保持原入口的结果语义。

```sh
(cd backend && mise exec -- go test ./internal/app -run '^(TestMetricsSafeOutcomesAndAttribution|TestMetricsSuggestionValues)$')
ROOMUSIC_CURATION_METRICS_SUITE=fixed-capture ROOMUSIC_CURATION_METRICS_SAMPLES=picard-01,fingerprint-only,mixed-a,mixed-b,one-char,unlisted-01,spoken-podcast ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

### 4. 采集 main 基线，验证冷启动复现与差值

- 运行完整的 `fixed-capture`。测试用已有的 `ROOMUSIC_CURATION_METRICS_OUT` 保存结构化结果；下列文件名相对于 Go 的 app 包测试目录，实际文件均在仓库的 `temp/` 内。首次完整结果形成 `backend/internal/app/testdata/curation_regression/fixed-baseline.json`（固定排序，保留比较所需字段）；在 `.trellis/tasks/11-26-curation-fixed-samples/research/baseline.md` 写程序与脚本两表，每张列目标/实际、写入差异、按接口的次数和用时，并列四数汇总、错认清单和条件。
- 第二次用普通入口完整运行，和首轮逐项核对业务输出、端点计数及时间差。报告记录产品 SHA、夹具版本（Git 提交或文件摘要）、工具版本、两次开始时间、实测命令时长及是否在几分钟内完成。运行失败、跳过、计数漂移或样本未结束要明记并定位；不把半轮导出当基线。后续运行读机器基线，打印差值。
- 若 main 有错认，照常保存和复核两份结果，保留非零退出和原因。基线不能把错认改为以后自动接受的预期。

```sh
time env ROOMUSIC_CURATION_METRICS_SUITE=fixed-capture ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/first.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
time env ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/second.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

### 5. 单测程序轮中的请求空档

- 在 `backend/internal/app/curation_metrics_integration_test.go` 分流 `idle`，定向计时驱动写在同包 `curation_fixed_timing_integration_test.go`，复用第 3 步的输入和 HTTP 观测。按设计使用 realtime、MB 的正常限速、固定 100 毫秒响应延迟、历史程序轮的调度参数；从首次扫描开始记录确认静默、扫描、队列状态和每次 HTTP 起止。
- 在任务的 `research/idle-gaps.md` 列出所有间隔，包括短间隔、重叠在途、首尾等待。每段注明影响发出下一次请求的事件、代码文件及函数、时长和残余；对无证据的长空档根据 trace 栈做单因素对照。记录实际代码行号、参数、对照命令和结论。只要跨到子任务 2–7 的调度、扫描或限速逻辑，就把证据交主代理；数行的本任务内修复也必须发生在基线之后。
- 这条测量独立于日常的几分钟回归。首次诊断运行用以下命令，原始轨迹和大体积 trace 写本次 `temp/` 子目录，研究笔记仅引用仓库内相对路径并保存可复核的事件摘录。

```sh
ROOMUSIC_CURATION_METRICS_SUITE=idle ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/idle.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

### 6. 发布测试说明并核对交付

- 在 `.trellis/spec/backend/quality-guidelines.md` 的测试说明登记一条命令、两轮输出、基线文件、各项口径、认错的退出状态及空等诊断入口。供下游实现代理原样复用。研究文件记录真实执行命令、输出和当前缺口。
- 复用第 4、5 步的实测结果。最终确认没有修改其他子任务的业务实现；检查依赖方向并核对格式。

```sh
(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
git diff --check
```

## 风险点

- 旧度量默认启用带答案的 oracle，关闭 AcoustID，并要求巡检完成；直接套用会让程序轮、声纹和“仅名字差异”三项失真。按第 3 步查看配置、实际出站和对应提交的先后。
- 同目录样本可能在基点被错误拆并。先取真实分区，再做归因；不能让预期分区决定数据库的分组。共享请求标出唯一归属，明确哪几张受影响。
- 快速测试的虚拟时间、零间隔、手动推进确认不能用来解释历史空等。程序诊断中采样只观测，trace 中出现后台等待还需证明该等待阻碍了下一次出站。
- 脚本按协议首条命中；若出现默认回话、假工具调用或测试时钟错过领取时间，先诊断回包与调用端，不能把驱动未工作记作程序未匹配。临时数据与凭据均为本轮合成。
- 初始扫描、合成素材、数据库初始化和按样本等待每秒轮询会影响运行预算。复用 fixture 与已有模拟时钟；报告实际耗时。时间比较呈现原值与差值，跨机器或不同配置的记录注明条件。

## 测试代理补充清单

| 用例范围 | 需补的证据 |
| --- | --- |
| 样本完整性 | ID 唯一，两种模式都有答案和写入约束，覆盖四个同组版本、全部编号、A1.5 各情况；混放两行共享一次扫描 |
| 模型与目录 | 同一脚本不同运行时 item_id 都取当前项；正确名字产生实际查找，未知和冲突按脚本保留；声纹键命中正确录音，无真实 HTTP 请求 |
| 扫描与身份 | 一样本拆成两张、两样本并到一张均能输出完整报告；校验版、组、录音、错写与保留字段；待名字核对时专辑已完成 |
| 评分和门槛 | 正确版/组，正确负例，安全漏认，错版/错组、负例错连和重复错写；同张多错只计一次，已知差异有报告，错认退出非零 |
| 请求与时间 | 缓存命中和配置探测排除、实际重试计入、零请求纳入分母、共享请求只归一次；已知请求、并发重叠、首尾等待的区间加总可对账 |
| 入口及兼容 | 两种模式各有独立缓存，程序不发模型请求；完整运行两次对比稳定字段；旧 142 张入口及旧 oracle 模式沿用。复用已跑的定向证据，仅对新增行为补用例 |

# 实施计划：采样率转换（SRC）

设计：[design.md](design.md)；共用接口：[骨架](../11-09-dsp-batch2-skeleton/design.md)；跨项合入门禁：[父 implement §5](../../../11-08-audio-dsp-reference-port/implement.md)。本文是待审规划，不等于授权启动实现。

后端实现与独立测试均用 `codex/gpt-6-astra`（high），由不同 agent 执行；前端由主会话直接写。工作树建议 `ROOMusic-dsp-src`，纯包分支 `dsp/src-pure`，接入分支 `dsp/src-integration`（SDM只需pure）；测试项目 `roomusic-test-src`。不改他人分支/测试库；参考稿只引章节号，本机数据只读。

## 1. 纯包（现在可开工）

1. SRC 线独占创建 `dsp/internal/fft`（逆变换不缩放）、登记包规则；把可复用小提交交卷积线，不让卷积重写 FFT。
2. `scripts/dspgen/src2s.py` 用固定版本的离线数值工具生成自主 2s 核；核、生成器、来源方法说明一起保留，不提交参考 CSV/附录。
3. dsp/src 的核生成/缓存、多相重采样、Latency/Reset、ResampleAll、Choose；按 design §1–§5。渲染 floor 长度与 IR ceil 长度分开。
4. 独立测试 agent 写 design §8.1 的 FFT、核规格、块长不变性、策略表测；本机 refcompare 只报告余量。refdata 若未到位可等待该基础小提交，但不阻塞核心算法。

## 2. 接入（骨架合入后，按顺序）

1. 在骨架已合入的 main 上更新分支；接入顺序第一项。登记 render→dsp/src 依赖，0045 增 sample_rate 文档。
2. zones 按骨架统一清单加键、默认、不可变规范化、存取、完整对象白名单；无 dspsettings SRC 服务。刷本项 recorder 样本，不能让 *int 地址参与相等判断。
3. renderPlan/Spec.Stages ② 装配，sourcePCM 恢复源采样率；decoder 只删变率，保留 -ac。Opus 固定48k、AAC不超48k，均在用户处理前变率；编码器不再暗中变率。
4. 主会话写 React 两个设置控件，model/decoder/API/controller/fake 同步，signal kind=sample_rate。每次明确选择提交，不增加解释段落。
5. 独立测试补 design §8 接入最小集；主会话核对有损192→48、无损DXD回退与无转换 MD5；父 implement §5 两组整套 e2e 门禁通过后合 main，交降混接入。

## 3. 最小验证集与命令

只验 design 指定的实际改动，不写假想场景或全量兜底。以下 go 命令从 backend 运行；真实渲染测试先在仓库根 `export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。本机参考测试若跳过，须单列未完成对照，不能写“指标通过”。

```bash
go test ./internal/dsp/internal/fft ./internal/dsp/src -count=1
go test -tags refcompare -run TestReferenceKernels ./internal/dsp/src -count=1
# 接入后：
go test ./internal/zones/... ./internal/playback/render -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

```bash
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-src \
  ROOMUSIC_TEST_RUN='TestPostgreSQLZones|TestPostgreSQL.*DSP|TestPostgreSQLMigrationRollbackRoundTrip' \
  scripts/test-integration.sh
# 有意合同改变后仅更新所属响应，再不带更新变量复跑：
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-src \
  ROOMUSIC_TEST_RUN='^TestPostgreSQLZonesAPIResponseSamples$' scripts/test-integration.sh
```

迁移号 **0045**，带 down；新增资源测试沿用 TestPostgreSQL...DSP/IR 命名以纳入范围，实际执行时列出匹配到的用例，不把零匹配/跳过当通过。独立 project 仍受脚本全局锁约束，本机数据库和 real e2e 串行。

```bash
(cd frontend && npx vitest run src/features/player/zones/decoders.test.ts src/features/player/components/signal-path.test.tsx)
(cd frontend && npm run typecheck)
```

新增本项表单/管理/上传交互测试由测试 agent 命名并在交接时追加**精确文件路径**的 vitest 命令；上述既有两文件不能代替新增交互验收。前端主会话在本子项合入前构建一次，产物随本项提交以满足 CI 漂移检查，不由各并行后端分支分别重建。

合入前额外执行父 §5 的整组 real e2e 与 mock e2e，禁止只跑单个新用例代替；日常不跑全仓 go test ./...。运行 `git diff --check`。生成器只在方法/系数变动时运行，CI检查自主产物性质，不重算昂贵优化。

## 4. 回滚点

纯包无调用方时可单独回退；已被卷积/DSD 引用后不删共享 FFT。接入回退须同时恢复骨架阶段 decoder/encoder 的临时变率和旧协商，回退 0045；先备份再删设置列。只把策略改 none 并非完整回滚，兼容变率仍会使用 SRC。

## 5. 交接回报

文件与接口变化；真实执行命令/结果/跳过项；最小验收对应证据；迁移升级→回退→再升级结果（有迁移时）；信号路径/界面截图（有界面时）；尚未完成的本机对照与最终iPhone验收。不得把本计划命令列表当作执行记录。各子项实现收尾再更新所属 spec，本次只起草任务文档。

SRC 另回报自主2s六组核的规格、每族本机最差余量、最大最小相位核的生成耗时与峰值内存；不输出参考核。

# 实施计划：多声道降混

设计：[design.md](./design.md)；共用接口：[骨架](../11-09-dsp-batch2-skeleton/design.md)；跨项合入门禁：[父 implement §5](../11-08-audio-dsp-reference-port/implement.md)。本文是待审规划，不等于授权启动实现。

后端实现与独立测试均用 `codex/gpt-6.1-sol`（high），由不同 agent 执行；前端由主会话直接写。工作树建议 `ROOMusic-dsp-downmix`，纯包分支 `dsp/downmix-pure`，接入分支 `dsp/downmix-integration`（SDM只需pure）；测试项目 `roomusic-test-downmix`。不改他人分支/测试库；参考稿只引章节号，本机数据只读。

## 1. 纯包（现在可开工）

1. dsp/downmix：uint32 掩码/显示名、按扬声器类别构建矩阵、Target、零延迟处理器；不导入 chain，不复制骨架 DefaultLayout，不写没有调用方的 ffprobe 布局映射。
2. 登记包规则。独立测试 agent 写 design §7 的矩阵、分频正弦、Target 表；素材在测试中生成。

## 2. 接入（骨架合入后，按顺序）

1. 等骨架 → SRC 合 main，基于最新 decoder.go 接入，随后才轮到 PEQ。
2. 0046 加 downmix JSON 文档 `{enabled:true}`；zones 设置与样本按骨架清单，不能改成旧稿 boolean。
3. 确认原布局经 RenderSource/app/Track 已传递；源声道恢复，删除目标 -ac，在 SRC 后放③。关闭矩阵时在 Tail 选前N声道，使 outputPCM 等于交付格式。
4. 信号保留 by，新增 mode/布局名；同步前端 decoder、model、stepIcons。主会话写开关与显示，不加说明。
5. 只增加显式 WAV 掩码接线、关闭不混合、开关提交/信号显示的必要测试；普通 MD5 复用第一批。合 main 前父 §5 real/mock 整组门禁。

## 3. 最小验证集与命令

只验 design 指定的实际改动，不写假想场景或全量兜底。以下 go 命令从 backend 运行；真实渲染测试先在仓库根 `export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。本机参考测试若跳过，须单列未完成对照，不能写“指标通过”。

```bash
go test ./internal/dsp/downmix -count=1
# 接入后：
go test ./internal/zones/... ./internal/playback/render -count=1
go test ./internal/playback -run 'Test.*(WAV|Render)' -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

```bash
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-downmix \
  ROOMUSIC_TEST_RUN='TestPostgreSQLZones|TestPostgreSQL.*DSP|TestPostgreSQLMigrationRollbackRoundTrip' \
  scripts/test-integration.sh
# 有意合同改变后仅更新所属响应，再不带更新变量复跑：
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-downmix \
  ROOMUSIC_TEST_RUN='^TestPostgreSQLZonesAPIResponseSamples$' scripts/test-integration.sh
```

迁移号 **0046**，带 down；新增资源测试沿用 TestPostgreSQL...DSP/IR 命名以纳入范围，实际执行时列出匹配到的用例，不把零匹配/跳过当通过。独立 project 仍受脚本全局锁约束，本机数据库和 real e2e 串行。

```bash
(cd frontend && npx vitest run src/features/player/zones/decoders.test.ts src/features/player/components/signal-path.test.tsx)
(cd frontend && npm run typecheck)
```

新增本项表单/管理/上传交互测试由测试 agent 命名并在交接时追加**精确文件路径**的 vitest 命令；上述既有两文件不能代替新增交互验收。前端主会话在本子项合入前构建一次，产物随本项提交以满足 CI 漂移检查，不由各并行后端分支分别重建。

合入前额外执行父 §5 的整组 real e2e 与 mock e2e，禁止只跑单个新用例代替；日常不跑全仓 go test ./...。运行 `git diff --check`。生成器只在方法/系数变动时运行，CI检查自主产物性质，不重算昂贵优化。

## 4. 回滚点

回退 0046 和本项渲染/前端接线，恢复 SRC 已合入版本的 ffmpeg -ac（不可恢复其已删除的变率）。关闭开关是“取前N声道”，不是恢复第一批 ffmpeg 矩阵。纯包被实际调用前可单独撤回。

## 5. 交接回报

文件与接口变化；真实执行命令/结果/跳过项；最小验收对应证据；迁移升级→回退→再升级结果（有迁移时）；信号路径/界面截图（有界面时）；尚未完成的本机对照与最终iPhone验收。不得把本计划命令列表当作执行记录。各子项实现收尾再更新所属 spec，本次只起草任务文档。

# 实施计划：第二批公共骨架

设计：[design.md](./design.md)。仅规划；主会话审阅后才能启动。后端实现与独立测试分别由 `codex/gpt-6-astra`（high）执行；本任务不写新界面、不提交任何新算法。测试 agent 只补本计划列出的直接受影响行为。

## 1. 纯包基础（现在可准备）

1. 核对 main 最大迁移仍为 0044、工作区无他人改动被覆盖；不为预留编号创建 SQL。
2. 单独准备 `dsp/internal/refdata` 基础工具与实际包规则（design §8），只读本机数据、缺失跳过、绝不输出参考系数。可把这个小提交先交纯包线使用；不建立 fft/算法空壳，不让算法核心等待完整骨架。

## 2. 公共接入（必须先于 SRC 合入）

- [ ] K1：Stage 描述、Spec 三段列表、输入/输出格式核验、OutputFrame、Latency、ModifiesSamples；Wrap 暴露延迟。保留 Headroom ① 先于 SRC ②，不误放在降混之后。
- [ ] K2：`renderPlan` 和 sourcePCM/outputPCM；逐项替换 design §3 表列的字段/函数。先保留第一批 ffmpeg 适配，确认没有无意改变有损档流。
- [ ] K4：run 的输入提交边界、events 的 inputFrame/输出 frame、原子换轨快照、nextChanged、按输入预算限制领先；零帧输出与 Drain 完整贯通。
- [ ] K3：统一 hasProcessing；协商与信号共用实际增益、计划节点事实，实际抖动仍由 Buffer 控制。
- [ ] K5：WAV 掩码安全重读、RenderSource→app→Track→plan；集中默认布局，不新增数据库布局列或 ffprobe。
- [ ] K6：zones 不可变文档基础、EqualSettings/默认/解码、LoadUser 的坏键回退日志（Headroom 一并）；HTTP 统一形状校验但不提前暴露未来键。核实 CreateDevice 与 UpdateZone 两条写路径。
- [ ] 测试 agent 补 design §9 的最小集；沿用现有 MD5/CUE/事件回归，不扩展到未实现算法。
- [ ] 主会话检查 diff、合同与 API 样本零变更；按父 implement §5 完整 real/mock e2e 门禁一次，审阅后独立合入 main，再交 SRC 接入。

## 3. 验证命令

命令从仓库根运行；新增测试名由测试 agent 按场景命名，下面现有包命令不会伪装成新场景已覆盖。

```bash
git diff --check
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
(cd backend && go test ./internal/dsp/chain ./internal/playback/render ./internal/zones/... -count=1)
(cd backend && go test ./internal/playback -run 'Test.*(WAV|CUE|Render)' -count=1)
(cd backend && go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1)
(cd backend && go vet ./internal/dsp/chain ./internal/playback/render ./internal/zones/...)
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-dsp-skeleton \
  ROOMUSIC_TEST_RUN='TestPostgreSQLZones' scripts/test-integration.sh
```

需要数据库的坏值读取测试纳入 `TestPostgreSQLZones...`，不能将跳过算通过。新增 fake 节点的时间/延迟场景用假时钟和合成源，不启动 DB。合入门禁命令见父 implement §5；没有 UI 变更不另跑全部 Vitest。规范更新留本子项实现收尾，不在本次文档起草修改 `.trellis/spec/`。

## 4. 回滚点与交接

- refdata 基础提交独立；被纯包测试依赖后不要单独删除。
- chain/format 与 render 两侧接口变更作为一个可构建单元；不能只回退任一侧。K6 可独立回退但会恢复坏值导致 LoadUser 失败的旧缺陷。
- 骨架无迁移，回滚不动 DB。SRC 接入以后，需先回退算法接入再回退骨架。
- 回报：实际命令/结果、跳过项、MD5 基线、变率/声道事件单位、输入提交污染回归、样本差异、是否有未收敛合同。未执行整库测试需写原因；不拿局部通过代替合入前的两组 e2e。

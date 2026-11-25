# 整理链 M：度量与回归曲库

状态：已完成（2026-11-25）。父任务：`.trellis/tasks/11-25-curation-chain`。需求、原则与总体设计见父任务的 `prd.md`、`design.md`，
现状与审查发现见父任务的 `research/`。设计见本目录 `design.md`，执行见 `implement.md`。

## 目标

合成回归曲库与受控服务上的度量：外部请求数、进入模型的项数、token、完成比例与错误数，先测现有代码的基线。

## 对应需求

父任务的 R19。

## 依赖

无。

## 范围

- 回归曲库覆盖：整轨 CD（带 CUE）、Picard 打过标签的、流媒体下载的专辑与散放单曲（标签完整，无条码与目录号）、标签很差或没有标签的文件、华语与欧美、合辑与古典、别名艺人（Jay Chou 与周杰伦）、错字专辑名、同一专辑多个版本、外部源未收录的专辑。
- 反例集（父任务 V17）：同名的不同专辑、外部库未收录但有近似候选、曲目数相同的不同合辑、结构相符的错误编号、不该合并的两个艺人名、搜索结果被截断的常见标题。每个样本标注期望结果与是否“程序可定”。
- 沿用 `backend/cmd/roomusic-upstream-mock` 与 `backend/internal/upstreammock`，让受控服务按来源统计请求数，并能模拟 503 限流。
- 受控的模型端点统计调用次数与 token，按任务类型分开。
- 一条命令或一个集成测试跑到队列排空并输出度量，包括错连、错并、错改的数量；在现有代码上跑一次，把基线数字记在本任务目录。
- 两项实测：用程序自己的标识测 MusicBrainz 的实际成功速度（少量请求）；用同一专辑不同来源版本的时长数据量出时长容差的通过率。
- 为父任务 V14 准备一份用真实专辑的标签与时长制作的合成曲库（音频为静音，时长一致）。

## 验收

- [x] 基线数字已记录，包含每种曲库形态的外部请求数、进入模型的项数、模型 token、不需要人处理的完成比例、错误数。
- [x] 度量可以重复运行，结果稳定。
- [x] 两项实测的结果已记录，并回写父任务 `research/external-services.md`。

## 不在范围内

- 不改变任何整理行为。

## 结果

- 基线：`research/baseline-2026-11-25.md`；实测：`research/live-measurements-2026-11-25.md`；原始 JSON 在 `research/`。
- 运行度量：`ROOMUSIC_TEST_RUN='TestPostgreSQLCurationMetrics' ROOMUSIC_CURATION_METRICS_OUT=<文件> ./scripts/test-integration.sh`，
  `ROOMUSIC_CURATION_METRICS_MODEL=first_candidate` 切换模型模式。约 4 到 5 分钟。
- 已知局限：声纹不覆盖；受控服务的文字查询语法有限、艺人搜索只索引主名；模型端点不主动发明候选、不提交字段修正；时长容差实测样本小且不均衡。

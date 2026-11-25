# 整理链重构：实施顺序

父任务不启动。每个子任务单独规划（`prd.md`，复杂的加 `design.md`、`implement.md`），规划完直接实施，
完成后提交并合回本地 `main`，不推送。

## 分工

- 主代理：规划、前端实现、派活、验收与提交。
- 后端实现与测试交给 Codex 子代理，实现与测试由不同的子代理执行；两个及以上子代理协同时用 Orca 编排。
- 每个子任务用独立的任务分支与工作目录（`orca worktree create`）；X0 只改文档，直接在主工作区做。

## 顺序与依赖

| 序 | 子任务 | 依赖 | 主要改动位置 | 完成的标志 |
| --- | --- | --- | --- | --- |
| 1 | X0 决策与文档 | 无 | `docs/architecture/adr/0006-*`、`docs/architecture/architecture-decisions.md`、`.trellis/spec/guides/product-goals.md`、`.trellis/spec/backend/` 的审查合同与整理合同 | 文档反映 `prd.md` 的原则与取代表 |
| 2 | M 度量与回归曲库 | 无 | `backend/internal/upstreammock`、`backend/internal/app` 的集成测试、测试夹具 | 现有代码上的基线数字（含错误数）记录在任务目录 |
| 3 | A 真实性标记、本地分类与版本拆分 | M | `backend/internal/catalog/curation_evidence.go`、`backend/internal/curation/program`（`worker.go`、`pass.go`、`digest.go`） | 版本与摘要拆分先合入；V3 通过；分类可查询 |
| 4 | C 程序匹配规则与终止状态 | A | `backend/internal/curation/program/match*.go`、`artist_links.go`、`enrich_media.go`、`backend/internal/curation/join_group.go`、`backend/internal/sources/musicbrainz` | V1、V4、V18 通过，V2 的请求数部分通过，V17 中属于程序的反例通过 |
| 5 | F 自动层规则 | 无（可与 C 并行） | `backend/internal/catalog/governance`、`backend/internal/curation/writer.go`、迁移 | V11 通过 |
| 6 | B 队列、并行与中断恢复 | A、C | `backend/internal/curation/program/worker.go`、`pass.go`、`backend/internal/sources`、扫描的观测记录 | V2 的限速器部分、V9、V10 通过 |
| 7 | E 模型兜底改造 | B、C、F | `backend/internal/curation/model`、`curation/stewardtools`、`curation/program/model_matcher.go`、`backend/internal/app/curation_model.go`、`steward/runtime` | V5、V7、V8、V15 通过，V17 中属于模型的反例通过 |
| 8 | D 模型巡检与本地整理 | E、A | `backend/internal/curation/model`、新的巡检存储与任务 | 可行性测试有结论；V6、V12、V16 通过 |
| 9 | G 界面 | B、E、D 的接口 | `frontend/src/features/curation` | 整理状态页、未采用的建议、按核对方式筛选与撤销、设置项可用 |
| 10 | Z 跨子任务验收 | 全部 | 集成测试、真实环境试用记录 | V1–V18 全部有结论 |

A、C、F 合入后，不配置模型的曲库已经能受益（P1、P2、P10、P11、P12）。B 合入后解决排队与限流（P3–P5、P13）。
E、D 合入后模型环节按新方式运行（P6–P9、P14）。

V6（别名艺人不经模型合并）的程序部分在 C 完成，D 只补“一边的专辑对不上、要靠巡检给规范名”的情形。

## 每个子任务的验证

- 只跑与改动直接相关的包与用例，按包或用例名收窄（`go test ./internal/<包>/... -run <用例>`，集成测试用 `ROOMUSIC_TEST_RUN`）。
- 子代理跑过且之后相关代码没改的，验收时不复跑。
- 集成测试用独立的临时目录与唯一的 Compose 项目名，使用合成数据。
- 前端改动：相关的单元测试与受影响的端到端用例。
- 每个子任务合入后用 M 的回归曲库跑一次度量（含错误数），数字记在该子任务目录。

## 需要先核实再动手的事项

| 事项 | 由谁核实 | 做法 |
| --- | --- | --- |
| 无标签文件的占位判断是否失效 | A | 用真实扫描路径写用例 |
| 自动值是否不随标签变化作废 | F | 写用例：自动补年份后改标签再扫描 |
| 根目录暂时不可用时整理结果是否保留 | B | 读扫描的缺失处理并写用例 |
| 扫描的触发方式（手动、定时、监视） | B | 读扫描入口，决定“稳定”的判断由谁推动 |
| 合并搜索与别名搜索的写法、发行搜索是否认艺人别名、繁简是否互通 | C | 对真实服务用程序自己的标识做少量实测 |
| 巡检按名字独立产出规范身份是否可行 | D | 带标注的别名集，测并错率与拆错率 |
| 本地艺人是否按规范化名字归一 | D | 读艺人入库的实现 |
| MusicBrainz 的实际成功速度、时长容差的通过率 | M | 见 `design.md` 第 12 节 |

核实结果与原判断不符时，在对应子任务的 PRD 里调整范围，并回写父任务的 `research/current-chain.md`。

## 回退点

- 每个子任务一个合并提交，可以单独回退。
- C 与 E 改变判定结果，受外部匹配规则版本控制；回退代码后版本号回到旧值，已写入的结果仍可按批撤销。
- 迁移只增不改：新增列与表，不删除现有的例外与待定项数据。

## 启动前检查

- `prd.md` 的“规划结论的确认方式”已记录用户的确认。
- 各子任务目录已创建，PRD 写明对应需求与依赖。
- 启动某个子任务前，为它整理 `implement.jsonl` 与 `check.jsonl`（规范与调研文件）。

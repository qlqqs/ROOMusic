# 自动整理 C：验证记录

## 已运行

| 层 | 内容 | 结果 |
| --- | --- | --- |
| 后端构建与静态检查 | `go build ./...`、`go vet ./...`（`backend/` 下） | 通过 |
| 单元测试 | `go test ./internal/curation/... ./internal/catalog/ ./internal/sources/...`：判定规则、相反证据、光盘目录、歌词接受、摘要、MCN、`FrontImage` 与投影 | 通过 |
| 地基集成（T1） | `TestPostgreSQLCurationC*` 8 个、迁移往返（含 0062）、接口样本；受 `GroupChangeOperations` 改签名影响的 5 个归组相关用例 | 通过 |
| 程序层集成（T2 与修复后重跑） | `TestPostgreSQLCurationProgram*` 13 个与 `TestPostgreSQLCurationEvidenceProjection`，含经 `app` 装配、三个真实适配器对本地 HTTP 服务的端到端 | 修复后整组重跑 14 个通过，0 失败，0 跳过 |
| 回归 | `TestPostgreSQLCuration|TestPostgreSQLAPIResponseSamples`（S2 装配之后） | 27 个顶层用例通过；现有样本只有两个设置样本多出 `paused` |
| 变异检查 | T1 7 个（撤销写入者、强证据三项、人工结构、归组撤销的来源、`OnlyEmpty`）；T2 23 个（16 条相反证据、数量与时长佐证、半数门槛、条码第二项佐证、歌词容差、命中即定、代次） | 全部被测试抓到 |
| 前端 | `vitest run src/test/api-contract.test.ts src/features/governance`、`tsc --noEmit`、`eslint` | 897 通过，1 个跳过（已有的大样本用例，需外部目录）；类型检查与 lint 通过 |

## 发现并修复的缺陷

- T2 发现：封面写入已提交、状态未保存时中断，之后文件丢失，重启后同键回放不恢复文件却记完成。
  修复：回放之后重新核对封面缺口，仍缺时代次加一、用新键再写一次（B2 规则：新的操作替换旧行，旧的那条显示“已被替换”）；第二次仍缺记 `try_later`。
  用例 `TestPostgreSQLCurationProgramRestartArtworkReplay`、`…Retry` 覆盖。

## 没有验证的

- 真实的 MusicBrainz、Cover Art Archive、LRCLIB：只用了受控的本地服务与假对象（父任务收尾的 V12 试用里核对），包括 B1 留下的两点：条码的 `OR` 写法、`/isrc/` 的返回结构。
- 大曲库（1 万发行、15 万曲目）上一轮“扫一遍”的耗时：没有做性能测量；进度计数查询的执行计划确认走索引。
- 界面：设置、进度与整理记录的界面在 E1；本任务只验证了操作记录页的解码与文案（单元测试），没有截图。

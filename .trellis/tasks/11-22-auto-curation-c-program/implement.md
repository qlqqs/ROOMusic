# 自动整理 C：执行计划

工作分支 `feat/auto-curation-c`（主工作目录）。并行的测试线在独立工作目录 `temp/wt-c-tests`（基于 S1 完成后的快照，做法同 B2：
测试代理的改动以补丁合回）。后端实现与测试交给 Codex 子代理，实现与测试分属不同代理；前端由主代理实现。子代理不提交 Git，报告写到 `temp/`。

## 顺序

```text
S1 地基（astra） ──► S2 程序层（astra） ──────────► T2 程序层测试（astra）──► 验收、规范、提交
        └──────────► T1 地基测试（sol，独立工作目录）──► F 前端（主代理，登记 T1 的样本）
```

### S1 地基（`gpt-6.2-astra`）

允许修改：
- `backend/migrations/0062_*`
- `backend/internal/curation/*.go`（不建子包）、`backend/internal/curation/httpapi/*.go`
- `backend/internal/catalog/governance/` 下与归组撤销相关的文件；调用 `GroupChangeOperations()` 的现有测试只改调用处（传入测试里的空实现），不改断言
- `backend/internal/sources/sources.go`、`backend/internal/sources/coverartarchive/client.go`、`backend/internal/sources/musicbrainz/project.go`（补中立类型的字段）
- `backend/internal/operations/operations.go`（只加类型常量）、`backend/internal/app/app.go`（只改装配）

1. 迁移 0062 与回退脚本（design 第二节）。
2. `operations.TypeExternalLink`；`Writer.ApplyLinks` 与 `ExternalLinkOperations(w)`（design 6.1）。
3. `Writer.JoinGroup`（6.2，错误码 `no_strong_evidence`、`manual_structure`）；`governance.GroupChangeOperations(suppressions)` 与 `GroupSuppressionWriter`（6.3）。
4. `Request.OnlyEmpty`（4.2，跳过原因 `occupied`）。
5. 工作状态与待定项的存取、`ExternalIdentitySuppressed`、`LinkSuppressed`、`CloseOpenBatches`（导出给 S2）。
6. 暂停：`Settings.Paused`、`SetPaused`、`PUT settings` 的 `{enabled?, paused?}`；闸门放行检查增加暂停与可注入的扫描回调（`curation_paused`、`scan_running`，7.2）。
7. 进度：`curation.ProgramStatus` 接口、计数查询、`GET /progress`；标记列表包含曲目与专辑（6.4）。
8. `sources.FrontImage`；`sources.Release` 补字段（3.3）；`app.go` 注册新类型、传入写入器、接口层拿到 `ProgramStatus`（先传 nil）、闸门拿到扫描回调。
- 验证（`backend/` 下）：`go build ./...`；`go vet ./internal/curation/... ./internal/catalog/governance/... ./internal/sources/... ./internal/app/...`；
  `go test ./internal/sources/... ./internal/curation/...`。不写新测试。

### T1 地基测试（`gpt-6.2-sol`，独立工作目录）

只新增或修改测试文件与样本：`backend/internal/app/*_integration_test.go`、`backend/internal/app/testdata/api-samples/`、
`backend/internal/platform/postgres/migrations_rollback_integration_test.go`（版本 62 的夹具，注意 design 第二节的三点）、`backend/internal/curation/*_test.go`、`backend/internal/sources/**/*_test.go`。

| 断言点 | 用例前缀 `TestPostgreSQLCurationC` |
| --- | --- |
| 对应的写入：一条记录多项、被标记的跳过、相同的跳过、已对应别处的 `occupied`、不属于发行的对象被拒、开关关闭与批次关闭 | `…LinksApply` |
| 对应的撤销：删除与写标记、被改动后拒绝撤销、按批撤销 | `…LinksRevert` |
| `Describe` 与操作记录列表、撤销记录的 `changes` | `…LinksOperations` |
| 强证据归组：三项证据各缺一项时 `no_strong_evidence`；人工归组之后 `manual_structure`；成功时来源、`provider` 与批次；撤销后写 `group_join` 且再次归入被挡；人工归组的撤销不写标记；按来源撤销能取到它 | `…JoinGroup` |
| `OnlyEmpty`：有修正行或自动值的字段 `occupied` | `…OnlyEmpty` |
| 暂停：读写、成员 403、从关到开时清除；闸门在暂停与扫描回调为真时拒绝且零出站 | `…Paused` |
| 进度：各项计数只算可见发行、`unavailable`；计数查询的 EXPLAIN 走 `curation_work_states_progress_idx`（照 `group_target_integration_test.go:315` 关顺序扫描） | `…Progress` |
| 标记列表包含曲目与专辑 | `…Suppressions` |
| `sources.FrontImage` 与 `Release` 新字段的投影 | 单元测试 |
| 迁移 62 往返 | `TestPostgreSQLMigrationRollbackRoundTrip/0062_…` |
| 样本：`curation/settings-paused`、`curation/progress`、`operations/link-list`、`operations/link-reverted`；`curation/settings-*` 多出 `paused` | `TestPostgreSQLAPIResponseSamples` |

- 变异检查（`-overlay`，不改真实文件）：撤销的“仍是这条操作写的”、`JoinGroup` 的三项证据与人工结构、归组撤销只在来源为自动整理时写标记、`OnlyEmpty`。
- 还要跑受签名变化影响的现有测试：归组、默认版本、认领、撤销（`TestPostgreSQLRevert|TestPostgreSQLDefaultVersion|TestPostgreSQLClaim|TestPostgreSQLGroupChange` 一类，按实际名字收窄）。

### S2 程序层（`gpt-6.2-astra`）

允许修改：新包 `backend/internal/curation/program/`；新文件 `backend/internal/catalog/curation_evidence.go`（只读入口，可用 `catalog/internal/...`）；
`backend/internal/app/app.go`（启动循环、装配选项、接上 `ProgramStatus`）；`backend/internal/app/dependency_rules_test.go`（只加新包的规则与 `app` 的依赖）；
`backend/internal/app/` 里共用的测试夹具（只加“默认关掉循环”的选项，不改断言）；`backend/cmd/roomusic/main.go`（等待循环退出）。

1. `catalog.LoadCurationEvidence`（3.1）。
2. 摘要（7.3）、匹配：专辑方式六步、单曲方式三步、核对与相反证据（3.2–3.4）；纯函数与外部调用分开。
3. 有把握之后的四步与歌词（第四节，含代次与幂等键）；待定项（第五节）。
4. 后台循环、两轮、批次、让路、暂停、重试、变化标记（第七节）；时钟与间隔可注入。
5. 装配与选项（7.5）。
- 验证（`backend/` 下）：`go build ./...`；`go vet ./internal/curation/... ./internal/catalog/ ./internal/app/...`；`go test ./internal/app -run TestPackageDependencyDirections`。不写新测试。

### T2 程序层测试（`gpt-6.2-astra`）

只新增测试文件：`backend/internal/curation/program/*_test.go`、`backend/internal/app/curation_program_*_integration_test.go`、`backend/internal/catalog/curation_evidence*_test.go`。
外部源用测试里实现能力接口的假对象，经 `curation.NewSources` 注册（走真实的闸门）；另有一条经 `app` 装配、用本地 HTTP 服务代替三个外部源的端到端用例。

| 断言点 | 层 |
| --- | --- |
| 每一步的正例；每一种相反证据各一个反例；候选不唯一、多于 3 个、列表截断；搜索与模糊目录从不判定；信息不足不查询；空键不相等 | 单元 |
| CUE 的 MCN 当作条码；光盘目录的构造（缺帧、多个 CUE、曲号不连续时不建） | 单元 |
| 位置重复与推断曲号的规则 | 单元 |
| 歌词接受规则：时长、标题、艺人写法（含繁简与全半角）、纯音乐、多份不同的候选 | 单元 |
| 摘要：同样的输入相同；每一类输入变化都改变摘要；时长小数变化不改变 | 单元 |
| U7 合成曲库：Picard 标签专辑、只有条码、只有厂牌与编号、ISRC 专辑与单曲、CUE 整轨、多碟、华语、无标签——各自的预期结果与写入 | 集成 |
| 补空缺只补真正空着的字段；人工修正优先 | 集成 |
| 对上同一外部专辑的两张发行归到一起；撤销后不再归入；人工移出之后不再归回 | 集成 |
| 撤销外部对应后重新判定为拿不准并有待定项；清除标记后重新对上（不是回放）；判定有把握后待定项结束 | 集成 |
| 关闭总开关、开启者失效、暂停、关闭单个源：请求数为零或跳过对应主题；单元进行中暂停或开始扫描，下一次出站被挡 | 集成 |
| 扫描进行时不出站；重扫、规则版本不变时不重复请求；规则版本加一后重新判定；人工修正后由变化标记触发重判 | 集成 |
| `ErrTryLater` 之后退避、尝试次数不变；中断后重做同键回放、不产生重复记录、遗留批次被收掉 | 集成 |
| 端到端：经 `app` 装配，本地 HTTP 服务代替三个外部源，跑完一轮后进度、操作记录、封面与歌词可读 | 集成 |

- 变异检查：每条相反证据、`count_equal`、`durations_ok` 的阈值、条码一步的第二项佐证、歌词的时长容差、“命中即定”不再往后走、代次的递增。

### F 前端（主代理，T1 的样本合入之后）

`frontend/src/features/governance/`：`OPERATION_TYPES`、`externalLinkChangesDecoder`、`change-summary.ts` 的文案、撤销对话框标题；
`frontend/src/test/governance-data.ts`；`api-contract.test.ts` 登记新样本。验证：`frontend/` 下 `mise exec -- pnpm test -- governance api-contract`、`pnpm typecheck`、`pnpm lint`。

## 验证命令（范围上限）

```bash
# backend/ 下
mise exec -- go build ./... && mise exec -- go vet ./...
mise exec -- go test ./internal/curation/... ./internal/sources/... ./internal/catalog/governance/...
# 仓库根目录下
sg docker -c 'ROOMUSIC_TEST_RUN="TestPostgreSQLCuration|TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQLAPIResponseSamples|<受签名影响的归组相关用例>" mise exec -- ./scripts/test-integration.sh'
```

- 视图没有改动，目录的 EXPLAIN 与性能门禁不需要重跑。
- 集成测试有全局锁，串行执行。

## 回退点

- S1 之后：新的写入口没有生产调用方，行为与之前相同（除设置多出 `paused`）。
- S2 之后：循环只在总开关打开时工作；有问题时关掉总开关即可停止，已写内容按来源撤销。

## 启动前的检查

- [x] 规划子代理审阅三份文档（2026-11-22），必须改的 8 项与建议改的 12 项已并入（见 `prd.md`）。
- [x] `implement.jsonl`、`check.jsonl` 登记规范与调研。
- [ ] `task.py start`，建分支。

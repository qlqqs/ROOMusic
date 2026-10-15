# M4 默认版本与版本对比

> 状态：2026-10-14 创建，同日规划获批；2026-10-15 实施完成，布局经用户确认（见“实施结果”）。技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)。
>
> 批准记录：路线是用户批准最终规划摘要，批准人 qlqq。K1 由用户在候选里选定，K2–K9 由本规划提出，随批准生效；批准后规划材料有实质变化时，需重新走一遍最终审阅。

## 目标与用户价值

一张专辑（ReleaseGroup）有多个版本时，让资料库说清“默认用哪个版本”，也让人看出各版本到底哪里不同。

现在代表版本固定按“年份最早”自动挑选，管理员改不了。例如把原版 CD 和后来的高解析度重制版归在一起后，专辑卡片、列表排序和打开专辑时显示的，都会是原版 CD。
这是 Core 1 阶段 M4 的第一项，对应父任务需求 R6“区分版本和管理默认版本”。用户于 2026-10-14 从“下一步”候选里选定了这一项。

## 背景（已核对的事实）

**代表版本现在怎么定、在哪里起作用**

- 规则：可见成员中年份最早（没有年份排最后），其次创建最早，最后 ID 最小（`backend/internal/catalog/groups.go:21-23`，
  [Catalog REST 合同](../../../../spec/backend/catalog-rest-contracts.md)“按组读取”）。两处都写明“M4 引入默认版本后，默认版本排在最前”。
- 作用：
  - 专辑卡片的封面、标题与艺人（`frontend/src/features/catalog/components/album-card.tsx:21`）；
  - 列表排序（`groups.go:57`、`groups.go:77`）；
  - 详情页不带 `?release=` 时显示的版本（`frontend/src/features/catalog/components/album-detail-view.tsx:178`；地址里只有非代表版本才带 `release`，见同文件 `:157-160`）；
  - 版本导航的顺序与“版本 N”编号（`frontend/src/features/catalog/components/version-nav.tsx:44-55`）；
  - “归入其他专辑”对话框里的目标专辑（`frontend/src/features/governance/components/join-album-dialog.tsx:58-63`）；
  - 归组记录的标题快照（`backend/internal/catalog/group_change.go:48-54`、`:83-89`）。
- “播放”按钮播放当前选中的版本，所以打开专辑后默认播放的就是代表版本。专辑卡片上没有播放按钮。

**多版本专辑与写入路径**

- 多版本专辑只来自 M3 的人工归组：扫描从不改既有发行的 `group_id`，新发行各自新建一个组（`group_change.go:12-15`）。
- `release_groups` 没有自己的字段，从不删除；发行也从不删除，只会因曲目缺失而不可见。
- 受治理写入只走 `operations.Run`，每种操作类型注册一个 `Kind`（`backend/internal/operations/kind.go:11-21`）。现在的约束都只针对发行：
  - 类型与目标类型的检查约束只有发行（`backend/migrations/0017_governed_operations.sql`）；
  - 撤销响应只有 `release_id`（`kind.go:38-44`）；
  - 记录列表只能按 `release_id` 筛选（`backend/internal/operations/httpapi/handlers.go:59-68`、`:78-90`）。

**对比可用的数据**

- 组详情的成员摘要有：年份、来源与介质类型、介质数、曲目数、封面（`backend/internal/catalog/projection.go:5-17`）。
- 发行详情另有：流派、编号、版本说明、厂牌、条码，以及每首曲目的编码、位深、采样率、时长（`projection.go:27-42`、`:71-90`）。
- 前端现在只为选中的版本读详情（`version-nav.tsx:19-31`）。

**设计依据**

- [DESIGN](../../../../../frontend/DESIGN.md)：
  - 用版本标签和差异下划线表达“为什么不同”（第 58 行）；
  - 版本对比对话框 `min(980px,100%)`、最大 `90vh`，窄屏内部滚动（第 175 行）；
  - 不把高采样率称作“最高音质”，也不按数组下标认定为同一首录音（第 291 行）；
  - 版本偏好规则需独立确认（第 349 行）；
  - 默认版与差异对比在 M4（第 348、365 行）。
- 原型（`frontend-desgin/ROOMusic 原型.dc.html`）：
  - “对比全部版本”按钮（第 135 行）；
  - “字段 × 版本”的对比表，以第一个版本为基准、差异金色下划线、点列头切换（第 482-503 行）；
  - “资料库的默认播放版本”（第 515 行）；
  - 操作记录“为《范特西》设置默认版本 · 撤销”（第 567 行）。
- 决策约束：
  - [ADR 0003](../../../../../docs/architecture/adr/0003-governed-operations.md) 第 255 行：M4 逐类批准默认版本等版本关系操作；
  - [ADR 0002](../../../../../docs/architecture/adr/0002-metadata-overlays.md) 第 207 行：默认版本策略另行确认，不能让某个版本的“设空”清空整组；第 279 行：默认版本范围另行批准；
  - [路线图](../../../../../docs/architecture/core-1-roadmap.md) 第 97 行：证据可追溯，结构变更走统一执行。

## 裁决

| 编号 | 问题 | 结论 | 来源 |
| --- | --- | --- | --- |
| K1 | 默认版本由谁、以什么方式决定 | **全库一个默认版本，由管理员逐张指定**；立刻生效，有操作记录，能撤销；没有指定的专辑仍按“年份最早”自动选择 | 用户 2026-10-14 选择（推荐项） |
| K2 | 默认版本影响哪里 | 它成为专辑的代表版本，现有代表版本起作用的地方一起跟着变（见“背景”）；不另设规则 | 本规划提出，随批准生效 |
| K3 | 默认版本不可见或被移出这张专辑时 | 静默失效，专辑回到自动规则；版本回来（文件恢复、撤销移出、被归回）时重新生效；扫描与归组都不改默认版本 | 同上 |
| K4 | 管理员在哪里、怎样设定 | 专辑详情头部（多于一个版本时）：不是默认版本的版本有“设为默认版本”，包括自动选出的那个，设定之后就不会被以后归入的更早版本挤下去；默认版本上有“改回自动选择”。不弹确认对话框，结果提示带“撤销”；操作之后页面停在刚才那个版本 | 同上 |
| K5 | 谁能看到默认版本 | 所有成员在版本导航与对比里看到“默认版本”标签；自动选择时不加标签 | 同上 |
| K6 | 版本对比 | 所有登录成员在多于一个版本的专辑上可以打开“对比版本”对话框，逐项比较发行字段与内容（介质、曲目、时长、音频格式的具体参数）；与第一列不同的格加金色下划线并带读屏文字；人工修正有标记；不做音质结论，不编造“为什么没有合并” | 同上 |
| K7 | 对比数据怎么取 | 新增只读接口，一次取回各版本字段与格式汇总；不加重组详情 | 同上 |
| K8 | 记录与撤销 | 新操作类型“设定默认版本”，目标是专辑；组有自己的人工修改版本号；只能撤销专辑最近一次默认版本调整；专辑详情的“修改记录”同时列出这个版本和这张专辑的记录 | 同上 |
| K9 | 移出默认版本 | “移出为独立专辑”的确认对话框说明：移出后专辑改按自动规则显示，它回来时会重新成为默认版本 | 同上 |

## 需求

- **R1 默认版本**：管理员可以为多版本专辑指定一个默认版本，也可以改回自动选择；默认版本按 K2 成为代表版本，按 K3 在失效时回到自动规则。
- **R2 设定入口与反馈**：按 K4、K5 提供入口与标记；操作结果就近提示并可撤销；普通成员没有任何设定入口。
- **R3 受治理写入**：设定、清除与撤销都走共同写入流程：只有管理员；带幂等键与组的预期版本；每次成功写一条操作记录（操作者、时间、专辑、前后默认版本）。
  撤销只针对专辑最近一次默认版本调整，撤销本身不能再撤销。
- **R4 记录可见**：操作记录页能看到并撤销默认版本记录（有“专辑”列）；专辑详情的“修改记录”按 K8 列出专辑级记录，“全部记录”与它一致。
- **R5 版本对比**：按 K6、K7 提供对比，只展示有真实来源的值。
- **R6 重扫与归组不破坏**：重扫后默认版本保持；归组不改默认版本（K3、K9）；没有设定默认版本的专辑，代表版本、顺序与接口输出和现在逐项一致。

## 验收标准

| 编号 | 可观察结果 | 需求 |
| --- | --- | --- |
| A1 | 在多版本专辑的某个版本上点“设为默认版本”后：专辑卡片显示它的封面与标题，列表按它排序，打开专辑先显示它，版本导航把它排第一并标“默认版本”；页面停在这个版本，提示带“撤销” | R1、R2 |
| A2 | 在默认版本上点“改回自动选择”后回到“年份最早”；页面仍显示刚才的版本 | R1、R2 |
| A3 | 撤销（在提示里、“修改记录”里、操作记录页）恢复前一个设定；之后又调整过时拒绝并写明原因；撤销记录不能再撤销 | R3、R4 |
| A4 | 默认版本因曲目缺失不可见，或被移出这张专辑时，专辑按自动规则显示；撤销移出后它重新是默认版本；移出默认版本的确认对话框写明这一点 | R1、R6 |
| A5 | 真实重扫（合成素材）后默认版本保持 | R6 |
| A6 | 权限与并发：普通成员无入口、接口 403；跨源 403、未登录 401，均无副作用；同键重放得到同一响应、同键异义 409；过期版本 409；版本不在这张专辑里 409；没有变化 400；专辑不存在或不可见 404；设定与归组同时进行不死锁 | R3 |
| A7 | 每次设定、清除与撤销各有一条记录；操作记录页的“专辑”列与改动行（如“默认版本：自动选择 → 《…》（2015 · Remaster）”）正确；“修改记录”同时列出版本与专辑级记录 | R3、R4 |
| A8 | 所有登录成员在多版本专辑上能打开“对比版本”，看到年份、版本说明、厂牌、编号、条码、流派、来源/介质类型、介质与曲目、总时长、音频格式；与第一列不同的格有下划线与读屏文字；人工修正有标记；全部为空的行不出现；可从列头切换到某个版本 | R5 |
| A9 | 没有设定默认版本时，按组读取的代表版本、顺序和既有接口样本与现在一致（只多出新增字段） | R6 |
| A10 | 五种宽度、axe（WCAG 2.1 A/AA）、纯键盘完成设定、撤销与对比；对比对话框在窄屏内部滚动不溢出；Vitest、Playwright 与真实服务用例（隔离 PostgreSQL、真实 Chromium、合成素材）通过 | 全部 |
| A11 | 规范、DESIGN、ADR 0002/0003 的决策记录、路线图（含 M4 的拆分）与父任务同步，规范文件不超过 24,000 字节 | 全部 |
| A12 | 用户确认版本导航、设定按钮与提示、对比对话框、操作记录的宽屏与窄屏截图 | R2、R4、R5 |

## 规划决策记录

- 路线：人工选择。选择者：用户（qlqq）。
- 2026-10-14：用户从“下一步”候选里选定“默认版本与版本对比”，K1 选择“全库一个默认版本，由管理员逐张指定（推荐）”；同日批准最终规划摘要（K2–K9 与九步计划）。
- 2026-10-15：A12 用户看过合成数据的宽屏与窄屏布局图（专辑详情的设定提示与版本导航、版本对比对话框、操作记录页与“修改记录”、移出默认版本的确认对话框）后选择“布局可以，继续收尾（推荐）”。

## 实施结果（2026-10-15）

提交（均在 `main`，未推送）：`59ca218` 规划；`9aa02aa` 启动；`4b51a32` 更正 M3 文档的措辞（与本任务无关的小修）；`3b3793b` 迁移 0018 与操作记录的专辑目标（无行为变化）；
`71ba314` 默认版本的读写接口；`b356463` 版本对比接口；`6f35d01` 专辑页的默认版本标签与版本对比对话框；`0ad6219` 设为默认版本、改回自动选择、记录与撤销；
`04e2728` 浏览器用例；`2864d46` 真实服务用例；`e905586` 文档同步；`c3b13cf` 收尾截图时发现的三处布局问题。收尾（本结果、归档、链接修正与 journal）随后提交。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| A1 | 达成 | `TestPostgreSQLDefaultVersionBecomesTheRepresentative`（组详情的成员顺序、代表版本、列表排序与卡片、归组记录的标题快照都跟着变）、`TestPostgreSQLGroupDefaultAPISetClearRevertAndReads`；Vitest `default-version.test.tsx`（请求体与幂等键、页面停在这个版本、标签与“撤销”）；Playwright `default-version.spec.ts`；真实服务：页面上设定后，专辑列表与重新打开专辑都先显示它，版本导航排第一并带标签 |
| A2 | 达成 | 同上两个后端测试的清除路径；Vitest 的“改回自动选择”（`release_id` 为 null、地址带上 `release`、页面不动）与“刷新期间不按旧数据规范化”的确定性用例；Playwright 设定、改回与撤销的完整流程 |
| A3 | 达成 | `TestPostgreSQLRevertGroupDefault`（恢复每种前态，含失效中的前态；之后又有调整时 `revert_conflict`；撤销记录不能再撤销）、`…ReplaysWithTheSameKey`、`TestPostgreSQLRevertExecutorReportsTheGroupTarget`；Vitest：提示里、“修改记录”里与操作记录页撤销，更早的记录写出不能撤销的原因；真实服务：在“修改记录”里撤销后回到自动规则 |
| A4 | 达成 | `TestPostgreSQLDefaultVersionGoesDormantAndComesBack`（曲目缺失与移出时回到自动规则、版本号与已存值不变，回来与撤销移出后重新生效）、`TestPostgreSQLPinningTheAutomaticVersionAndClearingADormantDefault`；Vitest：移出默认版本时确认对话框多一段说明，撤销移出后它重新是默认版本 |
| A5 | 达成 | `TestPostgreSQLDefaultVersionSurvivesARealRescan`（经真实扫描接口重扫合成素材）；真实服务用例改了标准版的专辑标签后重扫，默认版本、专辑版本号与操作记录不变；反向验证：临时让扫描清空默认版本，这条用例随即失败，已还原 |
| A6 | 达成 | `TestPostgreSQLGroupDefaultAPIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects`（401、跨源与非管理员 403、各种 400 都没有副作用）、`…APISetClearRevertAndReads`（同键重放、同键异义、`revision_conflict`、`not_in_group`、`no_changes`、404）、`…APIConcurrentRequests`、`TestPostgreSQLSetDefaultRejectsInvalidRequestsWithoutSideEffects`、`TestPostgreSQLDefaultVersionAndGroupChangesNeverDeadlock`、`TestPostgreSQLSetDefaultAndDetachQueueOnTheSameRelease`；Vitest 的 409 与结果不确定（同一个键重试）；真实服务：成员的写接口 403 且没有副作用 |
| A7 | 达成 | `TestPostgreSQLGroupDefaultDescribe`（专辑、前后快照、能否撤销，专辑没有成员时仍有名字）、`TestPostgreSQLListUnionsReleaseAndGroupTargets`（并集、分页与总数）；`change-summary.test.ts` 的改动行；Vitest：操作记录页的专辑列链接到专辑、类型名、组筛选的说明，“修改记录”同时列出版本与专辑级记录 |
| A8 | 达成 | `TestPostgreSQLReleaseComparisonReportsEffectiveValuesAndContent`（有效值与 `corrected_fields`、只计 present 曲目、总时长规则、格式分组与排序）、`…FollowsTheRepresentativeRule`、`…IsBoundedAndUsesAFixedNumberOfQueries`、`…BoundsTheFormatsOfAVersion`、`TestPostgreSQLComparisonAPIPermissionsErrorsAndReads`（普通成员可读、未登录 401、404）；`comparison.test.ts`（13 项）与 `version-comparison.test.tsx`（10 项）；Playwright 确认差异下划线在真实浏览器里画出来；真实服务：两种音频格式与年份的差异被标出、人工修正有标记、没有质量结论 |
| A9 | 达成 | 既有按组读取测试与接口样本不改即通过，只多出组详情的 `revision` 与 `default_release_id`（`release-groups/get.json`、`get-multiple.json` 各多两行）；`TestPostgreSQLDefaultVersionBecomesTheRepresentative` 断言没有设定时与自动规则逐项一致 |
| A10 | 达成 | Playwright（路由模拟）全量 227 项通过，其中 `default-version.spec.ts` 7 项：设定、改回、撤销、对比，纯键盘与 Esc，五种宽度下对比对话框在视野内、不超过 90vh、内部滚动、自己没有横向滚动、窄屏堆叠并左右并排，axe 与最小字号；真实服务 33 项通过（新增 6 项，加 M3 治理 4 项与播放 23 项） |
| A11 | 达成 | `e905586`：新增 `default-version-contracts.md` 并同步受治理操作合同、Catalog REST、数据库、目录结构、运行合同、前端组件/hook/状态/目录/质量规范、DESIGN、ADR 0002/0003 决策记录、路线图（M4 分五项立项）、架构决策摘要、产品目标、README 与父任务；规范文件都不超过 24,000 字节；`trellis-check` 逐条对照代码核对并修正了 6 处表述 |
| A12 | 达成 | 见“规划决策记录”2026-10-15；截图前发现并修好三处布局问题（`c3b13cf`），每处都有 Playwright 断言，逐项去掉修正时对应断言失败 |

验证：收尾时在最终树上、隔离 PostgreSQL（`roomusic-test`，空闲端口、随机库名，用后 `down -v`）上重跑 `gofmt -l .`、`go vet ./...`、`go test -race -count=1 ./internal/operations/...`、
`go test -count=1 ./internal/catalog/... ./internal/app/... ./cmd/...`（含接口样本逐字比较）与 `./internal/library/...`，全部通过，并核对关键集成用例确实运行而不是跳过；
各步骤提交时的测试与变异检查见各提交说明。前端：`npm run lint`、`npx tsc --noEmit`、全量 Vitest 70 个文件 1474 项（在导出的暂存树上）、
`npm run build` 后内嵌产物与提交的逐文件一致、Playwright（路由模拟）227 项、真实服务 33 项（`./scripts/playback-e2e.sh`，步骤 7）。
没有运行 Go 全量测试：本任务只改了 `operations`、`catalog` 与 `app` 的装配，其余包由各步骤的相关测试覆盖。

### 与设计的差异

已补记在 [design.md](design.md) 第 13 节：总时长改为“有一首未知就是 null”；地址规范化暂缓改由视图自己读组详情的刷新状态判断，并顺带修好 M3 归组落到别的版本的问题；
`List` 的参数改为目标列表；对比的格式组合加了上限；收尾截图后修正了三处布局。

### 已接受的限制与后续

- 失效中的默认版本在页面上看不出来，也不能单独清除（只能换成别的版本或撤销）；它回来时会重新生效（design §12）。
- 版本对比不做曲目逐首对应，也没有“切换播放版本”；默认版本对所有成员都一样，个人偏好属于 M5；不按格式参数自动挑选。
- 浏览器验证只有 Linux Chromium 的自动化证据（五种宽度、axe、键盘），没有 Safari、Firefox 与 Windows 的实际渲染，也没有人工读屏验证。
- 部署后无需额外操作：迁移 0018 只新增两列并放宽约束，已有专辑没有默认版本、版本号为 1，读取结果与升级前相同，不需要重扫。
- 后续（均未启动）：M4 的其余各项（问题处理、认领、扫描反馈、Release 合并与拆分）分别立项；成员写操作接入操作记录（M3 的 G6，独立小任务）。
  F1 手工实播与 F3 远端 CI 仍按用户裁决延后。

## 范围外

- M4 的其余各项分别立项：问题处理、认领、扫描反馈、Release 合并与拆分。
- 个人默认版本或个人偏好（M5）；按格式参数自动挑选“最高音质”（DESIGN 第 291、349 行）。
- 跨版本的曲目对应与“切换播放版本”（DESIGN 第 291 行，需要另外的数据合同）。
- 原型里“为什么没有合并”的说明：没有数据来源，不编造。
- 组自己的展示字段（组标题等）与字段继承（ADR 0002 第 207 行）。
- 单独清除一个失效中的默认版本（见 design §12）。

# P 服务端播放历史

## 目标与用户价值

Core 1 阶段 P：把真实发生的播放按用户隔离地保存在服务端，概览上能看到自己最近播放的专辑，也能清除自己的记录；
这些记录是以后历史页面、收藏、歌单与推荐（M5）的数据基础。浏览器本地不作为权威存储
（[路线图](../../../docs/architecture/core-1-roadmap.md)“旧任务承接”）。技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)。

## 背景与已确认的事实

### 已批准的范围与验收出口

- 2026-09-28 D4：Core 1 包含 P（服务端播放历史）；P 排在阶段 I 之后，因为它引用曲目 ID，必须等身份规则稳定。
- 路线图 P 行：前置为阶段 I 与 M2a（均已完成）；验收出口为“只记录真实播放；重扫与改名后仍指向同一曲目”；
  P“只是真实播放的最小用户隔离记录，历史页面、收藏和歌单属于 M5”。
- [DESIGN.md](../../../frontend/DESIGN.md) §7：概览的“聆听历史随 P”；“收藏/历史/播放列表”行要求跨用户隔离必须成立。
  原型概览有“继续聆听”区块（专辑卡片、“第 5 首 · 剩 24 分”、进度条）和“全部历史”链接；原型数值是示例，
  DESIGN §8 要求不展示虚构数值，也不给未实现的功能做禁用占位。
- [播放器规范](../../spec/frontend/player-design-guidelines.md)：没有音频服务时不推进播放时钟、不记录播放历史。

### 现有代码与数据

- 曲目从不物理删除：完整扫描后找不到的文件只把 `tracks.source_status` 标为 `missing`
  （`backend/internal/catalog/persist.go:243`、`persist_track.go:98`），文件回来后沿用原 ID；只有空的 media 会被删除（`persist.go:93`），
  发行不删除。曲目 ID 按阶段 I 的规则跨重扫、改名、同 root 移动保持不变，原地换了音频内容的文件得到新 ID
  （[目录与扫描流程合同](../../spec/backend/library-scan-contracts.md)）。
- 曲目可能随重扫换到另一个发行（`persist_track.go:80` 更新 `medium_id`）；`tracks.duration_seconds` 是扫描得到的时长，可能为空或 0。
- 用户只能停用不能删除（`users.disabled_at`，迁移 0006）；停用的成员无法通过鉴权。
- 状态变更请求必须通过 Origin 校验、HttpOnly 会话和后端授权（[运行合同](../../spec/backend/core0-runtime-contracts.md)“请求与响应”）。
- 后端跨能力只经公开合同，各能力不读对方的私有表（[目录结构规范](../../spec/backend/directory-structure.md)）。
- 播放引擎（`frontend/src/features/player/engine/player-engine.ts`）的状态与进度只来自媒体事件；单曲循环在同一个已加载媒体上从头再播
  （`advanceAfterEnd`）。队列里的 `history` 只是随机模式“上一首”用的条目 ID 列表，存在 sessionStorage，与本任务无关。
- 概览页（`frontend/src/app/routes/overview-page.tsx`）目前由资料库、扫描、目录（管理员）、成员（管理员）四个区块组成。

## 需求

- **R1 记录**：一次播放达到计次规则（P2）时，前端上报一次；后端为当前会话用户写一条记录，引用曲目 ID，记下服务端时间与达到规则时的出声秒数。
- **R2 只记真实播放**：只累计媒体真正出声的时间；预检、正在准备、加载、缓冲、暂停、失败与拖动跳过的部分不计；
  后端按扫描得到的曲目时长校验门槛，低于门槛的上报不写入。
- **R3 隔离**（P3）：任何用户（含管理员）只能读取、清除自己的记录；接口不接受用户 ID；写请求经同源校验。不保存 IP、设备与客户端信息。
- **R4 身份稳定**：重扫、改名、同 root 移动后记录仍指向同一曲目；曲目缺失时记录保留但不显示，文件回来后重新显示；
  曲目随重扫换到另一个发行时按它当前所在的发行显示。
- **R5 可靠**（P5）：同一次播放重复上报只记一次；上报失败不影响播放、不提示用户；页面打开期间有限次重试，关闭页面即放弃；
  退出、会话失效或换用户之后，上一位用户还没发出的记录不再发出。
- **R6 最近播放**（P1）：概览最前显示最多 8 张最近播放的专辑卡片（倒序、每张专辑一次），整张卡片链接到播放过的那个版本，写明上次播放时间；
  没有记录时说明计次规则；有记录时可以清除自己的全部播放历史（需要确认，不能恢复）。不照搬原型“继续聆听”的进度条、剩余时间与“全部历史”链接。
- **R7 保留**（P4）：记录不自动过期，只有本人可以清除。

## 决策

| 编号 | 决策 | 结论 | 状态 |
| --- | --- | --- | --- |
| P1 | P 是否带界面 | 记录 + 概览“最近播放”区块（专辑卡片，可清除自己的历史）；不照搬原型“继续聆听”的进度与剩余时间 | 2026-10-11 用户选择（AskUserQuestion：“记录 + 概览‘最近播放’（推荐）”） |
| P2 | 怎样算一次播放 | 一次访问内实际出声累计达到曲长一半或 4 分钟（先到者）记一次，每次访问最多一次；拖动跳过的不算；换曲或从头重播是新的访问，暂停、重试后继续算同一次；静音也算 | 2026-10-11 用户批准（最终规划摘要后回复“批准执行”） |
| P3 | 隐私边界 | 只本人可读、可清除，管理员也看不到他人记录；不存 IP、设备与客户端信息；停用成员的记录保留，重新启用后本人可见 | 2026-10-11 用户批准（最终规划摘要后回复“批准执行”） |
| P4 | 保留期限 | 不自动过期，清除由用户发起 | 2026-10-11 用户批准（最终规划摘要后回复“批准执行”） |
| P5 | 上报失败 | 页面打开期间有限次重试（同一个幂等键），关闭页面即放弃，不写浏览器存储；会话结束时作废未发出的记录 | 2026-10-11 用户批准（最终规划摘要后回复“批准执行”） |
| P6 | 布局确认 | 收尾时用合成数据截取概览的宽屏与窄屏（有记录、空态、清除确认），请用户确认后再归档 | 2026-10-11 用户批准；同日布局确认通过（回复“布局可以，继续收尾（推荐）”） |

## 验收标准

- [x] A1（R1、R2）：引擎测试证明达到门槛时恰好上报一次；未达到门槛、只拖动、预检失败、正在准备期间都不上报；单曲循环每轮各一次，
  暂停或重试后继续累计。
- [x] A2（R2）：低于门槛的上报得到 `400 play_too_short`，不写记录；曲目不存在或已缺失得到 404。
- [x] A3（R5）：同一幂等键重放返回同一条记录，请求体不同得到 `409 idempotency_conflict`，并发同键只写一行。
- [x] A4（R3）：用户 A、用户 B、管理员互相读不到、清不掉对方的记录；跨源写入 403；未登录 401。
- [x] A5（R4）：改名与同 root 移动后重扫，记录仍指向同一曲目 ID，最近播放仍显示同一专辑；缺失曲目的记录不显示但仍在库中。
- [x] A6（R5）：上报失败不改变播放状态；会话结束后，等待重试的上报不再发出。
- [x] A7（R6、R7）：概览按倒序、去重、最多 8 张显示最近播放，空态与错误态各有文案；清除需要确认，成功后区块为空；
  五种宽度不溢出、axe 无违规、键盘可以完成清除。
- [x] A8（R1–R6）：真实服务（Go、隔离 PostgreSQL、Chromium）中，合成 FLAC 真实播放过半后出现记录与专辑卡片，清除后为空；只拖动不产生记录。
- [x] A9：接口样本与前端解码器一致；规范与路线文档同步；概览布局经用户确认（P6）。

## 范围外

- 完整的历史页面、收藏、歌单、播放统计与推荐，以及“继续聆听”的进度和从上次停下处继续（M5）。
- 暂停记录的开关、删除单条记录（M5 随历史页面考虑）。
- 管理员查看或统计他人的播放。
- 外部服务同步（如 Last.fm）。
- 浏览器本地历史的迁移（开发阶段不做兼容）。
- 速率限制与防止用户伪造自己的记录（只影响本人的历史，见 design §7）。

## 规划决策记录

- 路线：人工选择。
- 选择者：用户（qlqq）。
- 2026-10-11：用户回复“按照推荐开始下一阶段规划”，选定阶段 P 并同意创建任务进入规划；随后在 P1 中选择“记录 + 概览‘最近播放’（推荐）”。
- 2026-10-11：用户在最终规划摘要后回复“批准执行”，批准 P2–P6 的推荐结论与 [implement.md](implement.md) 的七步计划；随后提交规划文档并运行 `task.py start`。
- 2026-10-11：P6 布局确认通过，不改页面，继续收尾：用户查看合成数据的概览截图（宽屏 1440 与窄屏 375；有记录含超长标题、空态、清除确认、清除后）后回复“布局可以，继续收尾（推荐）”。

## 实施结果（2026-10-11）

提交（均在 `main`，未推送）：`ecf0bb1` 数据表、计次规则与服务（迁移 0016、catalog 两个公开读取函数、`internal/listening`）；`ee05b97` 三个接口、样本与前端解码；
`7de1750` 播放器计次与上报（跟踪器、引擎、上报器、provider 接线）；`e94b77d` 概览“最近播放”（区块、清除、测试替身与浏览器用例）；
`d70f7e9` 真实服务覆盖；`8718340` 合同与规范同步。规划与启动：`b611c13`、`733248f`。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| A1 | 达成 | `listen-tracker.test.ts`（20 项：门槛、拖动与后退不算、跳变折算、暂停缓冲、新访问与继续）；`player-engine.test.ts` 的“计一次播放（阶段 P）”20 项：恰好交出一次、拖动不算、没有 seeking 事件的跳变不算、暂停与重试继续同一次、缓冲与预检与准备期间不算、换曲与单曲循环与队首“上一首”与停止与播完后再播重新计、卸载再挂载保留、刷新恢复后从零、静音照常、出口抛错不影响播放、会话结束作废；对引擎与上报器的关键行为做过变异检查（改坏实现，对应用例失败） |
| A2 | 达成 | `TestPostgreSQLRecordEnforcesTheThresholdAndTrackRules`（低于门槛 `play_too_short`、时长未知只校验范围、曲目不存在与已缺失 404）、`TestPostgreSQLRecordClassifiesAForeignKeyViolationAsAMissingTrack`、`TestPostgreSQLListeningRequestsAreCheckedInOrder`；`TestThreshold`、`TestMeetsThresholdAllowsOneSecondOfTolerance`、`TestValidPlayedSeconds` |
| A3 | 达成 | `TestPostgreSQLRecordStoresAndReplaysByIdempotencyKey`（重放回放首次记录，请求体不同 409）、`TestPostgreSQLRecordConcurrentRequestsWithTheSameKeyWriteOneRow`（并发同键只写一行）、`TestPostgreSQLPlayHistoryConstraints` |
| A4 | 达成 | `TestPostgreSQLHistoryAndClearAreIsolatedPerUser`（服务层）、`TestPostgreSQLListeningHistoryIsPrivateEvenFromAdministrators`（用户 A、B 与管理员互相读不到、清不掉）、`TestPostgreSQLListeningRequestsAreCheckedInOrder`（跨源 403 先于鉴权、未登录 401）、`TestPostgreSQLListeningResponseShapesAndDisabledMembers`（停用成员的记录保留） |
| A5 | 达成 | `TestPostgreSQLListeningFollowsTracksThroughRescans`：合成 FLAC 经扫描接口改名、跨目录移动、文件消失再回来，曲目 ID 与发行 ID 不变，记录仍指向同一曲目，最近播放仍显示它当前所在的专辑，缺失曲目隐藏但记录仍在、文件回来后重现；`TestPostgreSQLRecentReleases{AreNewestFirstOncePerRelease,AreLimitedAndHideMissingTracks,FollowTracksToTheirCurrentRelease,AreReadInBatches}`；catalog 的 `TestPostgreSQLLoadTrackPlacements` 与 `…FailClosedOnMalformedArtwork` |
| A6 | 达成 | 引擎用例“记录出口抛出错误也不影响播放”“会话结束作废还没有发出的上报”；`play-reporter.test.ts`（24 项：2/10/30 秒同一个键重试、4xx 与取消放弃、`dispose` 中止在途请求并清除定时器、作废后到达的失败不再安排重试、上限 50）；`player-provider.test.tsx` 的默认装配用例：上报成功使“最近播放”失效，上报失败重试后会话结束，等待中的重试不再发出（接线改坏时失败） |
| A7 | 达成 | `recent-plays.test.tsx`（12 项：占位与空态、倒序卡片与链接、点击进入版本、有记录但无可显示项、读取失败与重试、清除的确认与取消焦点、成功后焦点到标题与状态提示、不等重新读取、新播放使提示收起、失败与重试、失败后再打开不残留错误、请求中忙碌、纯键盘）；Playwright 新增 13 项：五种宽度不溢出（含八张卡片与超长标题、清除确认打开、有记录但无可显示项、读取失败）、axe 桌面与手机（有记录、确认对话框、清除后、读取失败）、真实 `<dialog>` 下的纯键盘清除（焦点约束、Esc 还回、Enter 确认后焦点到标题） |
| A8 | 达成 | `./scripts/playback-e2e.sh` 23 项通过（原有 21 项加 2 项）：合成 FLAC 真实播放过半后页面只上报一次、服务端有这首曲目的记录、概览显示该专辑并链接到播放过的版本、清除确认后区块与服务端都为空且刷新后仍为空；真实鼠标把 180 秒素材拖到末尾再播完，没有上报、服务端没有记录 |
| A9 | 达成 | 三个接口的样本（`listening/*`、`errors/play-too-short`）由后端集成测试生成，前端解码器经 `api-contract.test.ts` 往返核对，上限与后端一致；新增 `listening-contracts.md`，并同步运行合同、索引、catalog 合同、目录结构、播放器规范、状态管理、质量规范、DESIGN、产品目标、路线图与父任务；P6 布局已由用户确认（见规划决策记录） |

验证：隔离 PostgreSQL（`roomusic-test`，空闲端口、随机库名，结束 `down -v`）上 `internal/listening`、`internal/catalog`、`internal/app`、`cmd` 的后端测试与 `go vet` 通过；
Vitest 59 个文件 1192 项、Playwright（路由模拟）197 项、真实服务 23 项通过；`npm run lint`、`npm run typecheck` 通过；
内嵌产物在导出的暂存树上重新构建后与提交的逐文件一致，每个涉及前端的提交前都在导出的暂存树上重复 lint、类型检查与全量 Vitest。

### 与设计的差异

实施中的调整都已写进 [design.md](design.md)：写路由先同源再鉴权；字段类型不符是 `invalid_json`；最近播放同一时间按曲目 ID 排序；`listening.NewService` 只收 `*sql.DB`（依赖规则不允许 app 导入 catalog）；
引擎的依赖是 `playRecorder: { record, discard }`（而不是单个 `recordPlay`），`load` 显式区分新访问与继续；上报器的依赖是 `newKey` 而不是 `now`，另有 50 个待处理上限与同步抛错的处理；
“有记录但曲目都已缺失”用单独的说明文案；清除成功后立刻置空并提示“已清除”、焦点带到标题；对话框渲染在区块之外；测试替身放在已登录会话的默认路由里。
已接受的限制：上报失败后等待重试的记录，在随后清除历史时不会被取消（窗口约 42 秒，且清除成功说明服务已恢复），见 design 第 7 节与合同第 5 节。

### 仍需用户完成

- 部署后无需额外操作：迁移 0016 只新增一张表，不改写已有数据；已有的播放不会补记，只记录升级之后的真实播放。
- 概览区块在 Safari、Firefox 与 Windows 浏览器里的实际渲染目前只有 Linux Chromium 的自动化证据；已在三组手工实播的“每组交互检查”里增加一行“最近播放”，与 F1 一起做；
  F1 与下次推送后的远端 CI（F3）见后续验收任务 `10-06-m2a-acceptance-followup`。

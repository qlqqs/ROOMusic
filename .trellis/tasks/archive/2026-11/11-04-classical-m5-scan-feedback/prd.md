# M5 ⑥：扫描反馈

## 目标

扫描时看得到进行到哪了，扫描后知道这次做了什么：管理员看进度条和完整报告，所有成员在概览看到“最近新增”的专辑。原 M4 ④（`docs/architecture/core-1-roadmap.md:33,104-105`）。父任务见 [M5](../../../11-04-classical-m5/prd.md)。

## 决定

规划路线同 ② 收藏（用户逐项裁决，qlqq，2026-11-04）。

最终规划批准：路线 (a) 人工批准最终规划摘要；批准人 qlqq，2026-11-04 回复“批准……直接开始执行”；理由：五项的裁决、默认、合同与并行方案（三条线、各自隔离库 `roomusic-test-m5p/m5f/m5s`、文件锁排队、合入时迁移改号）均已在摘要中列出并获同意。

**用户裁决**：
1. **进行中**：扫描先快速数一遍文件（只看目录，不读内容），之后显示进度条、“已处理 1,234 / 4,812 个文件”和当前阶段（数文件 / 读取标签 / 整理专辑 / 保存）。大库会多几秒到几十秒的计数时间。
2. **完成后**：扫描详情里有报告——发现多少文件，新增 / 更新 / 消失了多少专辑和曲目，有多少问题，并列出新增的专辑（可能是旧版本替身的标出来，带去认领的链接）。概览给所有成员加一个“最近新增”专辑区块。

**默认**：
- 多个目录时仍逐个目录读取、整理、保存（先扫完的目录里的专辑先出现）；总体进度按各目录文件数加权，全程不倒退，阶段显示当前目录的阶段和“第 i / 共 R 个目录”（主会话按设计稿裁决，取代全部目录统一分阶段的方案：那样所有专辑要等最后才出现）。
- 报告口径（设计稿 §4）：曲目按身份分新增、更新、消失三类互斥计数，恢复算更新，同一路径换了内容算一首新增加一首消失；专辑按整张专辑计；报告是这次扫描实际提交的变化，不是前后两次全库的差。记录报告失败时不影响扫描本身，报告显示为不可用。
- 进度给所有登录用户看：概览的扫描状态显示进度条和阶段；管理员在扫描详情还能看到当前目录（第几个 / 共几个）。
- 进度每处理一批文件写一次库（有上限的批次，`.trellis/spec/backend/database-guidelines.md:96-98`），不逐文件写。失败、取消、未完成的扫描保留停下时的进度，并照现有规则显示状态，不会显示成“完成”。
- 报告只对本功能上线后的扫描有；之前的扫描详情不显示报告，也不补算。
- “最近新增”：按加入时间倒序最多 8 张可见专辑，与专辑卡片一致；没有时整块隐藏。专辑库加“最近新增”排序，区块里的“查看全部”跳到这个排序。
- “可能是旧版本的替身”：新发行的曲目与某个待认领旧版本的文件或目录相同（沿用认领推荐的依据），就标“可能接替了《…》”并链接到认领。

## 现状与约束

- `scan_runs` 只有状态与时间，没有任何计数（`backend/migrations/0002_core_slice.sql:26-32`，`0009_scan_cancellation.sql`）；`scan_diagnostics` 每次最多 100 条（`backend/internal/library/scanner.go:916-918`）；暂存表 `scan_observations` 每个根扫完就清空。识别计划的计数只写进日志（`backend/internal/library/identity_plan.go:73-83`）。
- 执行：逐个根单次 `WalkDir`，边走边读标签边暂存，事先不知道总数；之后识别规划、按 256 个范围分页持久化；只有成功的扫描才标记缺失（`scanner.go:75-454`）。
- “本次新增”不能靠 `releases.created_at` 落在扫描时间窗内推断（设计稿 §1 核对），曲目也没有 `created_at`、不记录哪次扫描变缺失，所以报告要在扫描写入时记录变化。“最近新增”的加入时间取当前专辑内可见版本的最早创建时间。认领候选是实时计算的（`backend/internal/catalog/governance/claims.go:15,31-38`）。
- 接口：`GET /scans` 列表与诊断只给管理员，`/scans/{id}`、`/scans/active` 所有登录用户；诊断每次最多保留 100 条，报告里的问题数到 100 时写“至少 100 条”；取消每 500 ms 检查一次，这是检查周期，不是最长等待时间；前端扫描中每 5 秒（概览）/2 秒（详情）轮询；解码器忽略未知字段（`.trellis/spec/backend/scan-coordination-contracts.md:13-60`）。
- 页面：`/library` 与扫描详情只给管理员；概览的 `ScanSummary` 所有人可见。现有设计“没有总数合同就不显示百分比”（`frontend/DESIGN-components.md:16,141`，`frontend/DESIGN-acceptance.md:18-19`）——本项建立总数合同，相关文档一并改写。

## 验收

- F1 进度：扫描中总体进度单调不减，每个目录内阶段按顺序推进；总数在计数阶段后固定；多个根时显示第几个；概览与扫描详情都能看到。
- F2 终态：成功、失败、取消、未完成各自显示正确，进度停在停下的位置，不显示成完成。
- F3 报告：在隔离库上构造新增、更新、消失的专辑与曲目，报告计数与实际一致；新增专辑列表正确，替身标记与认领推荐一致。
- F4 最近新增：概览区块对所有成员可见，最多 8 张，不可见的专辑不出现；专辑库“最近新增”排序可用。
- F5 质量：接口样本与解码器往返；五种宽度不溢出、axe 无违规；Playwright 与真实服务用例通过（真实服务：扫描一个小目录，进度到 100%，报告计数正确）；截图复核（含扫描中）。

## 范围外

暂停与续跑、定时扫描、文件监听、剩余时间估算、报告的导出、旧扫描的补算。

## 设计与执行

见 [design.md](design.md) 与 [implement.md](implement.md)。

## 实施结果

### 日期、提交与结论

2026-11-04。实现提交为 `34d944b`（后端）、`85ab070`（前端与测试）、`a7f9dba`（最近新增只显示整行）；变基到已含 P 线的 `main` 时，迁移由 0021/0022 调整为 0023/0024，与收藏、歌单迁移保持连续。

唯一导出基准为 `a7f9dba`，全量门禁全绿；1440 与 375 截图主会话已复核。

### F1–F5 验收证据

下表的前端文件路径以 `frontend/` 为根，后端路径以 `backend/` 为根；用例数均来自本轮导出树结果。Go 文件列顶层测试数，Vitest 列断言用例数；各行会引用同一测试文件，不能相加作为唯一总数。截图根目录为仓库内 `frontend/test-results/m5-scan-feedback-screenshots/`，下文 `{1440,375}` 表示两个宽度均有对应文件。

| 验收项 | 测试与证据 | 截图 |
| --- | --- | --- |
| F1 进度 | `internal/library/scan_progress_test.go` 3 项；`internal/library/scan_feedback_boundaries_integration_test.go` 13 项，覆盖多根、计数与持久化边界。`src/features/library/components/scan-progress.test.tsx` 19 项、`src/features/library/hooks/use-scan-catalog-refresh.test.tsx` 16 项，覆盖阶段、单调显示、空总数、计数后漂移、缓存失效；`src/app/library-scans.test.tsx` 52 项包含管理员目录信息与扫描交互。 | `{1440,375}/overview-scanning.png`、`{1440,375}/scan-detail-running.png`；合成场景为第 2 / 共 2 个目录、49% 读取标签。 |
| F2 终态 | `src/features/library/scan-status.test.ts` 8 项；上述进度组件 19 项和 `src/app/scan-feedback.test.tsx` 28 项覆盖成功、失败、取消、未完成与报告降级。`e2e/scan-feedback.spec.ts` 12 项中的三终态冻结用例分别验证停止轮询、未成功不显示 100%。后端边界集成测试验证终态冻结。 | `{1440,375}/report-succeeded.png`、`report-failed.png`、`report-canceled.png`、`report-incomplete.png`；成功 100%，其他为 49% 冻结与部分报告提示。 |
| F3 报告 | `internal/catalog/ingest/scan_feedback_integration_test.go` 15 项、`internal/library/scan_feedback_integration_test.go` 4 项，验证提交变化、新增/更新/消失与原样重扫；`internal/app/scan_feedback_governance_integration_test.go` 6 项（另含 37 个子项），覆盖治理与报告并发。`src/app/scan-feedback.test.tsx` 28 项覆盖 ready/partial/unavailable、503、历史/待完成隐藏、至少 100 条、当前不可见、认领链接及认领后失效；专项 E2E 包含纯键盘打开报告、翻页与焦点。 | 两宽的四种 `report-*.png`，包含新增专辑、替身认领提示与当前不可见项。 |
| F4 最近新增 | `internal/catalog/recent_additions_integration_test.go` 2 项、`internal/catalog/httpapi/recent_additions_integration_test.go` 1 项。`src/app/recently-added.test.tsx` 22 项、`src/features/catalog/components/recently-added-panel.test.tsx` 28 项、`src/features/catalog/api/recently-added.test.ts` 9 项，覆盖成员可见、空/404 隐藏、错误、列数变化与占位；`src/app/album-sort.test.tsx` 7 项、`src/features/catalog/album-search.test.ts` 15 项覆盖地址排序保留与换排序回首页。专项 E2E 验证元素位置、额外卡片不在 DOM/Tab 顺序及 curator/listener。 | `{1440,375}/recently-added-short.png`、`recently-added-full.png`、`albums-recently-added.png`；满集合在 1440 为 6 张一行，在 375 为 4 张两行，375 短集合为 1 张。 |
| F5 质量 | `src/features/library/api/scan-feedback-decoders.test.ts` 87 项；`src/test/api-contract.test.ts` 279 项（含 F 新增 59 份样本及其他功能样本），并由 `internal/app/scan_feedback_samples_integration_test.go` 1 项校验真实接口样本。`e2e/scan-feedback.spec.ts` 12 项覆盖 360/390/768/1280/1440 五宽、axe、键盘与扫描状态；`e2e/real/scan-feedback.real.ts` 1 项真实扫描通过。本轮所有全量门禁数字见下表。 | 两宽各 9 张，共 18 张；临时截图 spec 的 18 项通过，spec 不入库。 |

真实服务全套里的扫描反馈用例在既有 13 个合成文件基线上增加 3 个 WAV，终态为 16/16 文件、100%；报告专辑新增/更新/消失为 2/0/0，曲目为 3/0/0。10 个真实快照依次观察到 counting 0、organizing 6140、saving 8020/9900、succeeded 10000（单位为基点，含重复快照）。原样重扫两类变化均为 0，历史报告计数不变；普通成员读取报告返回 403，但可读取进度与最近新增。此处不将全套基线下的 16 个文件误写为专项入口的 3 个文件。

### 导出树门禁

只执行一次 `git archive a7f9dba` 导出。使用 Node v24.22.1、Go go1.25.10。后端只连接本轮空闲端口和随机库；使用 `--env-file /dev/null`、导出树绝对 compose 路径与 `roomusic-test-m5f`。浏览器和真实服务均在共享 `flock /tmp/roomusic-m5-e2e.lock` 内、同一 shell 加载 Playwright 环境，互不并发占用。

| 门禁 | 实际命令（在导出树对应目录） | PASS / FAIL / SKIP |
| --- | --- | --- |
| 后端 race | `go test -race ./internal/library/... ./internal/catalog/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/listening/... -count=1 -timeout=20m -json` | 顶层 **570 / 0 / 0**；含子项 **1602 / 0 / 0**；其中 PostgreSQL 顶层 **341 / 0 / 0**、含子项 **798 / 0 / 0**。19 包通过，另 2 个辅助包 `[no test files]` 的包级 skip 不是测试跳过。 |
| 后端静态检查 | 上述六组包运行 `go vet`、`go build`；`find internal/{library,catalog,app,platform,personal,listening} -name '*.go' -type f -print0 \| xargs -0 gofmt -l` | 全部通过，gofmt 仅检查且无输出。 |
| 前端依赖与静态检查 | `npm ci`、`npm run lint`、`npm run typecheck`、`npm run build` | 全部退出 0。 |
| 内嵌产物 | 原始 web 副本与重建结果 `diff -r`；所有浏览器运行后再次比较 | 两次均无差异。导出树 1,989 个原跟踪文件逐一与基准 blob 核对，无差异。 |
| Vitest 全量 | `npx vitest run --reporter=default --reporter=json --outputFile=<证据文件>` | 103 文件，**2713 / 0 / 0**；退出码复核轮同为 **2713 / 0 / 0**，45.42 秒、退出 0。 |
| 模拟 Playwright 全量 | 锁内 `npx playwright test --workers=2 --retries=0 --reporter=list,json` | **295 / 0 / 0**，无 flaky，259.114 秒、退出 0。 |
| 真实服务全量 | 锁内 `ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-m5f scripts/playback-e2e.sh --reporter=list,json,html`（未加专项过滤） | **48 / 0 / 0**，无 flaky，浏览器阶段 97.381 秒、脚本退出 0；含 P 线收藏/歌单、既有治理/播放及 F 线。 |
| 验收截图 | 锁内 `npx playwright test --config ../acceptance-screenshots/playwright.config.ts`（临时配置 workers=1、retries=0） | **18 / 0 / 0**，25.0 秒、退出 0。 |

异常说明：第一次 Vitest 输出与 JSON 均为 2713 项全部通过、`success: true`，但外层执行工具返回 1，日志未指出测试失败；保留原始证据，并在同一导出树完整复核，显式记录退出码为 0。不能将该外层返回码异常认定为产品失败，也不抹掉第一次记录。本轮没有浏览器失败或 flaky，因此没有触发 `--repeat-each=20 --retries=0` 定位。既有 jsdom 媒体方法未实现提示未造成用例失败。

大体积原始证据在仓外，以下为纯文本路径（临时目录后续可能被清理，需归档时另行复制）：

- 导出树：/tmp/roomusic-m5f-export-a7f9dba.2cPQrH/source
- 后端完整命令、JSON、统计与清理：/tmp/roomusic-m5f-export-a7f9dba.2cPQrH/evidence/backend/
- 前端日志、两轮 Vitest JSON、模拟/真实 Playwright JSON、原文件完整性与最终清理：/tmp/roomusic-m5f-export-a7f9dba.2cPQrH/evidence/
- 真实服务 HTML（含真实快照、原样重扫报告与截图）：/tmp/roomusic-m5f-export-a7f9dba.2cPQrH/source/frontend/coverage/playback-real/report/index.html
- 临时截图 spec、日志、退出码、PNG 尺寸清单：/tmp/roomusic-m5f-export-a7f9dba.2cPQrH/source/acceptance-screenshots/

后端库于 17:10:48 UTC 完成 `down -v` 后才开始真实服务；真实服务另取随机库并在退出时 `down -v`。17:19:57 UTC 最终按项目标签核对容器、卷、网络均为空，未连接或操作用户的 PostgreSQL 容器或 `roomusic` 库。

### 性能证据与边界

见 [读取延迟补验](verification/catalog-read-followup.md)、[正常构建 HTTP 基线](verification/catalog-http-performance-jit-only.md)、[执行计划与测量边界](verification/catalog-explain.md)。规模为 100,000 曲目、10,000 发行、8,000 组，5% 曲目缺失、7,800 可见组；每端点/统计状态 5 次热身、40 次正式请求，不剔除异常值。

- recent 服务端完整 handler p95：辅助空表未 ANALYZE 为 **100.959ms**，ANALYZE 后为 **104.354ms**，均低于 **150ms** 门槛。
- `added_at_desc` 首页 50 项服务端完整 handler p95：**194.115ms / 229.816ms**，均低于 **300ms** 门槛。
- 仅在短只读 REPEATABLE READ 事务中 `SET LOCAL jit = off`，没有更改连接池默认或全局参数，也没有业务内 ANALYZE。
- 上述为既有正常构建、暖缓存顺序请求基线；计时覆盖服务端完整 handler，不含 TCP 建连、请求解析或客户端解码，不等同客户端端到端延迟；本轮导出 race 的 EXPLAIN/HTTP 输出只用于规模正确性，不冒充新的性能 SLA 验收，不承诺冷盘或多用户并发时延。历史 [race 报告](verification/catalog-http-performance-race.md) 同理。

### 已裁决的产品行为

- 最近新增接口最多返回 8 张；布局能放至少 3 列时只渲染一行、最多 8 张，1–2 列时最多两行；超过可显示数的卡片不进 DOM，也不进入 Tab 顺序。数据不足时不补卡，加载占位使用同一规则；jsdom 无法测得列数时全部显示。
- 失败：“扫描失败，没扫到的曲目保持原样。”取消：“扫描已取消，没扫到的曲目保持原样。”未完成：“有些文件没能扫完，没扫到的曲目保持原样。看一下诊断，处理后再扫一次。”
- 多根仍逐根读取、整理、保存，整体按文件数加权且单调；计数结束后总数固定，文件漂移显示诚实计数说明，不推算剩余时间。只有成功显示 100%，其他终态冻结并说明停在哪里。
- 报告记录实际已提交变化，不把 SQL 写入次数或原样重扫算作更新；报告故障允许 unavailable 降级，不撤销已提交音乐结果；旧扫描不补报告。

### 截图

`frontend/test-results/m5-scan-feedback-screenshots/{1440,375}/` 下各 9 张（概览扫描中、扫描详情运行中、四种终态报告、最近新增不足一行与满行、专辑库最近新增排序），合成数据，本地、未入库，主会话已复核。

### 已知风险与待办

- `.trellis/spec/backend/claim-contracts.md` 原写“扫描不锁组行”，与扫描反馈先锁发行、后锁组行的实际顺序不符，已在收尾时改正（顺序仍与治理一致，不形成环形等待）。
- 无活动扫描时不持续轮询，可能漏掉另一管理员启动且很快结束的扫描；沿用聚焦/手动刷新。短真实扫描不保证采到每个阶段，逐阶段边界由受控集成测试覆盖。
- 未在真实浏览器中另做全部改标签/删除/替换的扫描报告矩阵；新增、原样重扫与历史保持已实测，其余报告口径由隔离数据库集成测试覆盖。不能将既有真实治理场景等同于完整报告矩阵。
- `state-management.md` 超过文档建议体积仍是待整理事项，本轮未扩大到文档拆分；性能基线的并发/冷盘边界见上节。

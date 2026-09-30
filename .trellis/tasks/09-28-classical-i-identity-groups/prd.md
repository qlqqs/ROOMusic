# 阶段 I：身份稳定与 ReleaseGroup 只读

> 状态：已实施并验证（2026-09-30 用户批准规划后分六步实施，见文末“实施结果”）。父任务见
> [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)；2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)
> （第二部分）创建。技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)，
> 核实事实与推导见 [research/identity-analysis.md](research/identity-analysis.md)。

## 目标与用户价值

让 Track、Release 的 UUID 在重扫、增删文件、同一 root 内改名或移动后保持不变，使后续的修正（M3）、
播放历史（P）、收藏与歌单（M5）不会因为整理文件夹而与实体脱钩；并提供按组读取的接口供 M1b 使用。

## 背景

- 曲目身份取决于相对路径（`backend/internal/catalog/identity.go:21`）；Release 按锚点查找，未命中就新建 Release 与组
  （`backend/internal/catalog/persist.go:40`–`47`）；散落和同目录拆分候选的锚点取组内最小路径
  （`backend/internal/catalog/organizer.go:922`）。改名、移动或增删文件都可能换 ID。
- 扫描逐 root 遍历并暂存观察，再逐候选短事务写入；root 不完整时也写入有效候选（`backend/internal/library/scanner.go:410`）；
  只有整轮 `succeeded` 才把未观察到的曲目置为 missing（`persist.go:215`）。
- FLAC 的 STREAMINFO 已被解析（`backend/internal/library/parser/parser.go:1556`），但其中的音频 MD5 尚未读取；
  其他格式目前只读元数据，不读音频数据。
- 真实库扫描以 `succeeded` 结束（2026-09-03 冒烟，约 5 秒），“只在完整遍历中重绑定”在实际中可行。

## 决策记录

| 日期 | 选择者 | 决策 |
| --- | --- | --- |
| 2026-09-28 | qlqq | D2：成员关系匹配 + 同 root 保守重绑定（唯一 1:1 音频相同才重绑定；取消、失败、不完整不重绑定；跨 root 不推定）。D6：Core 1 只有人工确认归组，阶段 I 提供按组只读接口。见 [ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 决策记录 |
| 2026-09-29 | qlqq | 开发阶段不做兼容，有必要可直接重构 |
| 2026-09-30 | qlqq | 重绑定的“遍历完整”按 root 判断：一个 root 遍历完整即在该 root 内重绑定，不受其他 root 不可用影响；missing 对账仍要求整轮 `succeeded` |

规划中据此确定、在批准范围内的做法（理由见研究记录第二节）：

- 指纹在曲目首次出现或文件变化时计算并保存：配对时旧文件已不在原处，“只在配对时计算”只适用于新出现的一侧。
- 候选逐个短事务提交，重绑定无法等到扫描的最终状态；“完整”指写入开始前遍历完整且未取消，写入阶段中断时已提交的重绑定保留。
- 暂缓写入（R4）是为了不让一次不完整扫描把移动后的文件写成新曲目，否则之后的完整扫描再也配不上对。
- 扫描暂存表改归 catalog，身份规划在同一能力内完成（design.md 第 1 节）。

## 需求

| 编号 | 需求 |
| --- | --- |
| R1 | **音频指纹**：`tracks` 保存与标签无关的内容指纹（含种类与版本）及来源文件大小、修改时间。FLAC 用 STREAMINFO 音频 MD5（非零时）加总样本数、采样率、声道、位深，MD5 为零时回退为音频帧哈希；M4A 用 `mdat` 负载哈希；MP3 用去掉首尾标签的帧数据哈希；OGG/Opus 用头部之后的音频页数据哈希；WAV 用 `fmt` 与 `data` 块哈希；CUE 虚拟轨 = 父文件指纹 + 区间。文件未变时复用；计算失败时该 scope 暂缓写入、root 记为不完整。 |
| R2 | **同路径换内容**：同一来源身份下，两边指纹都已知且不同时，新内容建新曲目；原曲目失去来源身份，没有配对到别处时置为 missing；记管理员可见诊断。 |
| R3 | **同 root 保守重绑定**：root 遍历完整时，本轮 organize 后保留的观察中没有身份匹配或换了内容的，与本 root 中 present、有指纹、本轮未被使用的曲目按指纹键（含 CUE 区间）配对；两侧都恰好一条才重绑定，沿用 UUID，更新路径与来源身份；否则不重绑定，记诊断，留待人工认领。 |
| R4 | **不完整或取消的遍历**：不重绑定、不置 missing；含可疑观察的 scope 暂缓写入并记诊断，下一次完整遍历仍可重绑定。 |
| R5 | **Release 成员关系匹配**：候选的保留曲目中超过一半当前属于同一个既有 Release 时复用它；多个候选争同一 Release 时重叠多者得，平局先选留在原锚点的一方，再按候选键升序；落选或无倾向的候选新建 Release 与组。锚点只作定位信息。 |
| R6 | **ReleaseGroup 只读接口**：`GET /api/v1/release-groups` 与 `GET /api/v1/release-groups/{id}`；参数、错误、present-only、搜索与 attention 语义与 `/releases` 对齐；组摘要 = 版本数 + attention 之和 + 代表版本摘要（含封面），详情另加成员摘要（最多 100 条）。代表版本：最早年份（无年份排后）→ 最早创建 → ID；M4 的默认版本以后排在最前。 |
| R7 | **旧身份兼容删除**：曲目只按当前来源身份匹配。 |
| R8 | **合同与文档**：Catalog REST 合同、运行合同（扫描规划、诊断）、数据库规范（身份规则）、目录结构（暂存归属）、ADR 0002、路线图同步更新。 |

## 验收标准

| 编号 | 对应需求 | 可观察结果 |
| --- | --- | --- |
| A1 | R1 | 各格式“同音频、不同标签 → 指纹相同”“音频改变 → 指纹不同”的单元测试通过；完整扫描后 present 曲目都有指纹；第二次扫描未变文件不再计算（可计数的测试端口证明） |
| A2 | R3 | 目录改名、同 root 内移到另一个专辑目录、同目录两文件交换名称、CUE 目录改名后，曲目 UUID 不变；跨 root 移动得到新身份；两份相同音频不重绑定并有 `identity_ambiguous` 诊断 |
| A3 | R4 | 不完整扫描与取消的扫描中：不重绑定、不置 missing、不为疑似移动的文件建新身份，有 `identity_deferred` 诊断；随后的完整扫描完成重绑定；另一个 root 不可用时，遍历完整的 root 仍重绑定，整轮为 incomplete、不置 missing |
| A4 | R2 | 同路径换内容后得到新曲目 UUID，原曲目为 missing，有 `content_replaced` 诊断 |
| A5 | R5 | 未变化重扫、新增排序更靠前的文件、删除最小路径文件、目录改名、在专辑间移动文件后，相关 Release UUID 不变；拆分时重叠多的一方沿用（平局时留在原位置的一方）；“2 轨加 3 轨”新建 Release |
| A6 | R6 | 每组一个版本时，按组列表与 `/releases` 的总数、顺序逐项对应，代表版本即该 Release；搜索、attention、分页、present-only 结果一致；多成员组的代表版本、成员数与 attention 之和正确；401、400 `invalid_id`、404 覆盖；路由表测试含两个新路由 |
| A7 | R7 | 代码中不再有旧身份查找；`TestPostgreSQLFirstRepairScanReusesLegacyCueTrackIdentity` 删除；只新增迁移 0013、0014，旧迁移文件不变 |
| A8 | R8 | 文档与合同同步；`/releases` 的既有测试不改动并通过；修改过的 Markdown 相对链接有效 |
| A9 | 全部 | 每步 `gofmt`、`go vet ./...`、`go build ./...`、`go test ./...`、`go test -race ./...`、`./scripts/test-integration.sh`、`git diff --check` 通过 |
| A10 | R1 | 只读真实库核对完成，按种类的指纹计数、失败数、MD5 为零的 FLAC 数、重复指纹数、读取量与耗时记入 `research/`，不含路径或文件名 |

## 范围外

- 人工归组、认领及任何写操作（M3）；默认版本与版本对比（M4）；自动强证据归组；外部元数据源；前端页面（M1b）。
- missing 对账规则的变更（仍要求整轮 `succeeded`）；按路径粒度的缺失证据；跨 root 映射。
- 被放弃的 Release 与组的清理（不做自动物理清理）。

## 风险与已知限制

- 迁移后第一次扫描要为非 FLAC 文件和 MD5 为零的 FLAC 各读一次音频数据；A10 记录真实库的读取量与耗时。
- 扫描主流程改动较大：先做纯搬迁，再分步提交（见 implement.md），每步全量测试。
- 某个 root 一直不完整时，该 root 的移动一直暂缓，旧条目保留，直到修好问题文件。
- 原地替换音频（如重新抓轨）得到新曲目 ID；整张专辑被替换时发行也会新建，原有修正需人工认领（M3 及以后）。
- 新增文件多于原有文件时新建 Release，原 Release 失去全部曲目后不可见。

## 实施结果

2026-09-30 分六步实施，逐步提交到 `main`（未推送）：

| 步骤 | 提交 | 内容 |
| --- | --- | --- |
| 规划 | `83d5ebb` | PRD、design、implement、research |
| 1 | `7260178` | `parser.ContentFingerprint` 与各格式指纹；只读真实库覆盖核对（[research/fingerprint-coverage.md](research/fingerprint-coverage.md)） |
| 2 | `abcc077` | 扫描暂存表归入 catalog（纯搬迁，测试名单前后一致） |
| 3 | `b54cfca` | 迁移 0013、`PlanStagedRoot` 身份规划、曲目按计划写入与重绑定、删除旧身份兼容 |
| 4 | `7194559` | 迁移 0014、Release 成员关系匹配 |
| 5 | `75ef107` | ReleaseGroup 只读接口与 REST 合同 |
| 6 | 见 `git log`（`docs: 同步阶段 I 的身份规则与按组接口`） | 规范、ADR 0002、路线图与架构图同步 |

**与规划的差异**（实施中确定，已同步到 [design.md](design.md)）：

- 暂存行增加 `planned` 标志：只有规划成功结束时才对保留下来且未暂缓的行置为真，`PersistCandidate` 只写已规划的观察，
  避免规划中途失败时按“新曲目”写入；规划读取暂存时不带封面字节。
- 没有音频负载的退化文件（无帧的 FLAC、无 `mdat` 的 M4A、只有标签的 MP3、只有头部包的 OGG）指纹为空（未知），
  不与其他文件比较；否则它们会互相被当成同一份音频。
- 配对键中，曲目侧只有 CUE 虚拟轨才带起止帧：单轨 CUE 会把区间事实合并进物理轨，暂存的物理观察没有这些值。
- 索引：暂存表用 `(scan_run_id, root_id, fingerprint_path)` 与 `(…, candidate_key)`；设计里的来源身份索引没有查询使用，已去掉。
- 除 `TestPostgreSQLFirstRepairScanReusesLegacyCueTrackIdentity` 外，只测旧身份函数的单元测试
  `TestLegacyCueSourceIdentityCanBeReusedAfterUpgrade` 也随函数一并删除。

**验收对照**（测试名见 [implement.md](implement.md)）：

| 编号 | 证据 |
| --- | --- |
| A1 | parser 单元测试（各格式同音频不同标签相同、音频变化不同、FLAC MD5 为零回退、MP3 首尾标签、OGG 注释包分页、M4A 多个 `mdat`、WAV 块、取消）；`TestPostgreSQLRescanKeepsTrackIdentity` 断言曲目都有指纹与文件状态；`TestPostgreSQLUnchangedFilesReuseStoredFingerprints` 用可计数的端口证明未变文件不再计算 |
| A2 | 改名、同 root 移动、互换与三文件轮换、CUE 目录改名、单轨 CUE 改名各一个场景；跨 root 移动得到新身份；相同音频不重绑定并记 `identity_ambiguous` |
| A3 | `TestPostgreSQLIncompleteScanDefersMovesUntilCompleteScan`、`…DefersReplacedContent`、`TestPostgreSQLCanceledScanDoesNotRebind`（规划期间取消、遍历前取消）、`TestPostgreSQLCompleteRootRebindsWhenAnotherRootIsUnavailable`、`TestPostgreSQLFingerprintFailureDefersScope` |
| A4 | `TestPostgreSQLReplacedContentGetsNewIdentity` |
| A5 | `TestPostgreSQLAddedEarlierFileKeepsReleaseIdentity`、`…RemovedSmallestFile…`、`…DirectoryRenameKeepsReleaseIdentity`、`…MovedFileJoinsTargetRelease`、`…SplitReleaseKeepsIdentityForLargerPart`（含平局）、`…MinorityOverlapCreatesNewRelease`、`…ExactlyHalfOverlapIsNotAMajority`；重扫与改名场景补了 Release ID 断言 |
| A6 | `TestPostgreSQLReleaseGroupListMirrorsReleaseList`（13 组查询逐项对应）、`…RepresentativeAndCounts`、`…DetailErrors`；路由表测试含两个新路由 |
| A7 | `legacySourceIdentity`、`legacyCueSourceIdentity` 与同路径兜底已删除；只新增迁移 0013、0014 |
| A8 | 规范、ADR 0002、路线图、架构图已同步；`/releases` 既有测试未改动并通过（只在 `read.go` 内抽出共用的搜索谓词与摘要列扫描，行为不变） |
| A9 | 每步执行：`gofmt -l .`、`go vet ./...`、`go build ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`、`./scripts/test-integration.sh`、`git diff --check`，均通过；关键逻辑另做了变异验证（关闭配对、去掉“先腾出”、不暂缓、不复用指纹、不判换内容、Release 分配与比较规则、按组的代表版本/attention/搜索/可见性/排序）确认测试会失败 |
| A10 | 真实库 284 个音频文件全部得到指纹，失败 0，无重复；迁移后第一次扫描额外读约 3.05 GiB、约 32 秒，见 research |

**遗留与已知限制**：见上文“风险与已知限制”；另外真实库没有 OGG/Opus/WAV 文件，这三类只由合成夹具验证；
被放弃的 Release 与组不做自动清理（范围外）。

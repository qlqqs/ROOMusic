# 设计：管家单张专辑整理工具与技能

路径相对 `backend/internal`，另注明的除外。

## 1. 总体

- 新增 5 个管家工具和 1 个技能机制，只在聊天与 MCP 注册，不进后台整理注册表（`app/steward.go:165-169`）。
- 写入工具全部复用整理链或本地辅助现有的核对与执行器，不另写判定规则。
- 所有工具最低身份为管理员；输出含标签、文件名，`Untrusted: true`。

| 工具 | Scope | 可撤销 | 复用 |
| --- | --- | --- | --- |
| `match_release_external` | shared | 是（逐步操作） | `program.prepareSelected`、`ModelMatcher.Apply`/`writeMatch` |
| `regroup_local_files` | shared | 是 | `checkGrouping`、`storeGroup`、`requestGroupScan`、`LocalGroupingOperations` |
| `list_library_directory` | read | — | `ListRoots`、`scan_filter`、`tracks`/`release_attachments` |
| `read_library_file` | read | — | `os.OpenRoot`、`parser`、`decodeCueText`、`image.DecodeConfig` |
| `load_skill` | read | — | `go:embed` |

## 2. 建立对应 `match_release_external`

输入：`release_id`、`entity`（`release` 或 `recording`）、`mbid`。

流程：在 `curation/program` 新增导出入口 `SelectExternal(ctx, releaseID, entity, mbid, actor)`。
1. 取与 `SelectMatch` 相同的发行级 advisory 锁（`program/match_selection.go:28`），读证据、工作状态与 file_digest。
2. 前置拒绝：
   - 发行不可见或已无整理资格 → `not_found`/`not_eligible`。
   - 已有 MusicBrainz 发行或组级对应 → `already_matched`，原因写明先用 `revert_operation` 撤销原对应。
   - 该 ID 在"不再提出"里（`LinkSuppressed`，`curation/suppressions.go:171`）→ `suppressed`，原因写明到自动整理设置的"不再提出"里清除。
   - `entity=recording` 只接受单轨散曲（`loose_unknown`），反之 `release` 不接受散曲。
3. 自造 `Candidate{ExternalID, Entity, Method:"model"}` 与空快照，走 `prepareSelected`（:145）：重取发行详情，`decideAlbum` 不过退到 `partialAlbumMatch`。核对不过返回 `insufficient_corroboration`，带具体冲突码（曲数、碟序、时长、反证）给管家。
4. 与 `SelectMatch` 不同，允许组级结论（部分对应、多版本），写 `release_group_match`，与整理链后台写法一致。
5. 以 `Source{Kind:"source", Provider:"steward"}`、`ConfirmedBy=actor` 调 `Apply`，绑定第 1 步的 file_digest 与摘要；`operations.origin` 记会话来源。
6. 署名拆分、艺人对应、封面、歌词由程序 worker 轮询到这条操作后照常补（`program/track_fields.go:103`、`work_states.go:43`），工具结果写明"其余字段稍后自动补齐"。
7. 结案：`SaveWorkState`（generation 加一、当前 `MatchRuleVersion`）、`ResolvePendingItems` 结束该发行等待中的待定项；模型手里处理中的项会在它下次提交时因摘要变化报 `item_stale`。

预览：目标版本的标题、艺人、碟数和曲数、对应层级（版本或发行组），以及会补的空字段。预览只读，执行时重新核对。

## 3. 重新归组 `regroup_local_files`

输入：`root_id`、`directory`（相对路径）、`albums:[{files:[{path, disc, track}]}]`。路径在工具里换成 `file_id = Fingerprint([root_id, relative_path])`。

流程：在 `curation` 新增不依赖待定项的入口 `SubmitStewardGrouping`。
1. 由 `localScopeKeys`（`local_items.go:245`）从该目录构造本地范围（连通目录），不能跨不相关的文件夹。
2. 前置：没有正在运行的扫描；范围内发行都稳定（`curation_stable_releases`）；范围内有受保护发行（已有外部对应或人工结构操作，`local_items.go:84`、:310）时拒绝 `protected`，列出哪张、为什么，管家先撤销。
3. `checkGrouping` 原样核对：覆盖全部非保护文件、文件不重复、同碟曲号不重复、已有真实碟轨号必须一致。不过返回 `invalid_grouping` 与具体原因。
4. `storeGroup` 写记录，操作类型 `local_model_grouping`，origin 记会话，`ConfirmedBy=actor`。
5. 这次归组算人工结构：扩展 `local_items.go:310` 的判断，把非整理来源的 `local_model_grouping` 也算作人工结构，之后本地辅助不再改它。
6. 触发确认扫描时不等 60 秒静默：文件没有变化，把相关发行的 `curation_changed_at` 回拨到满足稳定条件，下一轮确认扫描即生效。工具结果写明"下一次扫描完成后生效，可用列目录确认"。
7. 撤销沿用 `LocalGroupingOperations.Revert`（`local_grouping.go:207`）。

## 4. 列目录 `list_library_directory`

输入：`root_id`、`path`（相对路径，空为根）、分页。`list_library_roots` 已能列出根与 ID。

- 通过 `os.OpenRoot(root.path)` 读目录，不跟随符号链接；目录项是符号链接时只列名字并标"链接"，不展开。
- 隐藏：内置忽略目录与点开头的文件（`library/scan_filter.go:13-39`）、用户排除路径及其子孙（新导出 `library.PathExcluded(root, rel)`，逐段判定，抽自 `scan_scope.go:36`）。请求的路径本身被排除时返回 `not_found`，不提示被排除。
- 每项：名字、类型（目录、音频、图片、文本、其他）、大小。音频与 CUE 标上所属发行 ID、标题、碟号、曲号（`tracks` 按 `source_root_id, relative_path` 和 CUE 的 `cue_sheet_path` 反查，只取 `present`）；图片与文档标上 `release_attachments` 的所属发行。
- 只给相对路径，不给绝对路径与挂载位置。

## 5. 读文件 `read_library_file`

输入：`root_id`、`path`。

- `os.OpenRoot` + `root.Open`，挡 `..` 与越界链接；排除判定同第 4 节。
- 文本（`.cue .log .nfo .txt .m3u .m3u8`）：上限 64 KiB，超出返回 `too_large`；编码识别抽出 `decodeCueText`（`library/parser/cue_text.go:19`）为导出函数，返回文本与编码名。
- 音频：给解析后的标签与音频参数、编号、注释；不给封面字节、歌词（ADR 0005 第 3 节）和原始字节。`parser.ParseAudioFile` 现在按路径打开，新增接受已打开文件的入口，避免先检查后打开的窗口。
- 图片：格式、宽高、大小（`image.DecodeConfig`）。其他：只给类型和大小。
- `catalog/stewardtools/review_c7_integration_test.go:46` 断言审查证据不含 `relative_path`；新工具的输出不进独立审查的证据，测试与合同写明这个例外。

## 6. 技能 `load_skill`

- 技能放在 `steward/skills/*.md`，`go:embed` 打包；每份开头写名字、一句话用途和最低身份。
- 系统提示在模式附加句之后追加"可用技能"列表（名字与用途），按调用者身份过滤（`runtime/run_content.go:244`）。
- `load_skill(name)` 只读，返回全文，管家自己判断何时加载。
- 第一个技能 `album-curation`（整理一张专辑），内容要点：
  1. 看本地：列目录、读 CUE、抓轨日志、NFO 与完整标签，确认专辑由哪些文件组成、有没有混入别的专辑。
  2. 找编号：日志里的光盘目录、NFO 的条码和目录号、标签编号；都没有再按名字搜，网络专辑可按艺人列出他名下的发行。
  3. 比对：曲数、碟序、每首时长逐首对照，不只看名字。
  4. 归组：混放或拆错时先重新归组；遇到受保护的专辑，先撤销原对应或人工合并，再重新归组，等扫描生效后再认版本。
  5. 写入：用建立对应工具，字段由整理链写入流程补；不直接用修正改识别得来的字段。
  6. 被拦下：读退回的冲突码，换候选或补查一次；同一原因连续两次就停下，说明情况。
  7. MusicBrainz 没有：认定未收录，不拿相近的专辑凑；配了联网搜索时可查官方页面，把找到的内容和链接告诉用户，用户同意才用修正写入；不凭模型知识补年份、厂牌；提示艺人可交给艺人联网。
  8. 散曲（无专辑标签的单轨）只认录音；有专辑标签的单曲按专辑流程，最多认到发行组。
  9. 节制：同样的查询不重发；接近单次运行上限时总结进度，等用户"继续"。

## 7. 前端

- `frontend/src/features/steward/tool-labels.ts` 增加 5 个工具的中文名；确认框沿用现有预览展示。
- 不新增页面。

## 8. 兼容与回退

- 不改后台整理链调度、规则版本和数据表；第 3 节第 5 点的人工结构判断是唯一扩展。
- 无迁移。如需回退，关掉工具注册即可，写入的数据都在既有表里，可逐条撤销。

## 9. 风险

- 撤销原对应后到重新归组之间，后台可能把它对到另一个候选上。撤销会把原 ID 记入"不再提出"并重新排队（`suppressions.go:183`），被抢先时管家的归组会因 `protected` 被拒，按技能再撤销一次即可。
- 单次运行 24 次模型请求可能不够处理复杂专辑，靠技能里的"总结进度、等继续"兜住。
- 列目录与读文件的内容会发给用户配置的模型服务商。

# ① 文件过滤与失败处理

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，S 线第 1 项。对照依据：[gap-analysis.md](../11-08-scan-reference-port/research/gap-analysis.md) G1、G2、G3、G8；参考设计 8.3、14、16、26.7.3 章。

## 目标

扫描不再被系统文件、个别坏文件或没挂上的 NAS 拖垮：该忽略的文件不进扫描；单个文件出问题只影响它自己，其余文件照常入库，真正删掉的曲目照常标 missing。

## 现状

- 没有任何路径过滤：`.xxx`、`._x.flac`、`@eaDir`、`#recycle` 都进入遍历（`backend/internal/library/scanner.go:318-481`）。只有目录浏览接口过滤 `.` 开头的项（`backend/internal/library/folders.go:60`）。回收站里的音频会被当成正常曲目入库。
- 任何单文件问题都把根目录标成不完整：不支持的格式（`scanner.go:447-452`）、解析失败（`:454-460`，`._x.flac` 必然走到这里）、权限错误、非普通文件、断链。合同规定扫描不完整时不做 missing 对账、不做移动配对（`.trellis/spec/backend/library-scan-contracts.md:36,141`）。结果是库里只要有一个这样的文件，删除和移动就永远不会被处理。
- 所有符号链接在扩展名判断之前就解析，断掉的 `cover.jpg` 链接也会让扫描不完整（`scanner.go:339-348`）。
- 根目录存在但为空（NAS 没挂上）时遍历成功，整库被标 missing（`backend/internal/catalog/ingest/persist.go:283-290`）；挂上后重扫能恢复，ID 不变（`persist_track.go:81`）。
- CUE 的 `FILE` 引用和同名回退都按字节匹配（`backend/internal/library/parser/cue_path.go:36,77`），从 macOS 拷来的 NFD 文件名或大小写不一致时整张 CUE 的分轨被跳过。
- 预计数与遍历各自判断候选（`backend/internal/library/scan_progress.go:195-269`）。
- 前端“不完整”的说明：“有些文件没能扫完，没扫到的曲目保持原样。看一下诊断，处理后再扫一次。”（`frontend/src/features/library/scan-status.ts:39`）。

## 需求

- **R1 默认路径过滤**：内置一份忽略清单，按路径段匹配（不区分大小写，不做子串匹配）；命中的目录跳过整棵子树，命中的文件直接跳过，不记诊断。清单至少覆盖：以 `.` 开头的名字（含 `._*`、`.Trash-*`）、`@eaDir`、`#recycle`、`@Recycle`、`#snapshot`、`$RECYCLE.BIN`、`System Volume Information`、`lost+found`；另加 `@Recently-Snapshot`、`__MACOSX`，以及八号机核实的 fnOS 卷级系统目录 `@appmeta`、`@team`（fnOS 回收站 `.@#local/trash` 已被点开头规则覆盖）。预计数与遍历用同一个判断函数。
- **R1b 用户排除子文件夹**：在扫描目录设置里，每个已登记目录可以添加“不扫描的子文件夹”，用现有目录浏览器挑选，只能选该目录内的子文件夹；排除的子文件夹跳过整棵子树，与内置清单同一个判断函数。排除项的增删带幂等，保存后下次扫描生效；被排除目录下已有的曲目按真正消失处理（标 missing）。
- **R2 扩展名先于链接解析**：不是音频候选、也不是 CUE 的条目，不论是否为链接都直接忽略。
- **R3 单文件失败不致命**：不支持的格式、解析失败、权限错误、非普通文件、断链或越界链接，只记诊断，不再把根目录标成不完整。该路径上已有的曲目保持原状，并计为“本轮见到”，不会因此被标 missing，也不参与移动配对；没有已有曲目的就不入库。下次扫描自动重试。
- **R4 目录读取失败**：某个子目录读不了时，记诊断，该目录下已有的曲目全部保持原状并计为本轮见到，其余目录照常对账。
- **R5 CUE 失败**：CUE 无法解析或引用不安全时，记诊断，这张 CUE 已有的虚拟曲目保持原状，不阻断整次对账。
- **R6 仍然判为不完整的情况**：根目录不可用、暂存写入或读取失败、数据库错误、取消、进程重启。这些保持现有行为。
- **R7 空根保护**：根目录可以访问，但一个音频候选都没扫到，而库里这个根下还有 present 曲目时，判为根目录不可用，记诊断“目录是空的，可能还没挂载”，这个根不写入、不对账，扫描状态为不完整。
- **R8 CUE 引用宽松匹配**：精确路径找不到时，在同一目录内按 NFC 规范化加大小写折叠比较，恰好一个匹配才采用；同名 stem 的格式回退用同一套比较。多个匹配按找不到处理并记诊断。
- **R9 扫描结果的表达**：只有 R3–R5 这类单文件或单目录问题时，扫描状态为完成；状态旁提示跳过了几个文件，点开看诊断。界面文案遵守 CLAUDE.md 的前端文案规则：一句日常的话，不解释实现。
- **R10 合同同步**：更新 `library-scan-contracts.md` 的完整性定义与校验矩阵、`scan-feedback-contracts.md` 中相关的计数口径，以及前端诊断类别名（`frontend/src/features/library/diagnostic-kinds.ts`）。

## 验收

- 单测覆盖：忽略清单各项（目录跳过整棵子树、文件跳过、大小写不敏感、`my.recycle` 这类不误伤）；各类单文件失败后根目录仍完整；已有曲目在失败后保持 present；目录读取失败只保住该目录；空根保护触发与不触发（新库、真的删空）；CUE 的 NFC/NFD 与大小写匹配、多个匹配时不采用。
- 一次包含 `._x.flac`、`.ape`、损坏的 FLAC 和一个真正删除的文件的扫描：前三者只出现在诊断里，删除的文件被标 missing。
- 真实库只读 smoke：对比改动前后的扫描状态、发行数、曲目数、诊断类别分布。

## 不做

- 跟随目录链接、子串式规则、回收站多特征联合判定（参考设计 14.2 的 3、4 条，ROOMusic 的根目录是显式登记的，不需要）。

## 已裁决

- 2026-11-08 用户裁决：读不了的文件对应的已有曲目保持原样，扫描算完成，提示跳过的文件数（R3、R9）。
- 2026-11-08 用户裁决：内置忽略清单之外，每个目录可排除子文件夹（R1b）。
- 2026-11-08 用户裁决：空根保护只在一首都扫不到时触发（R7）。
- 2026-11-08 主会话裁决（设计审阅）：坏掉的文件夹封面（不安全、为空、超限、格式无效）同样只记诊断 `artwork_unreadable`、保留原封面，不让扫描不完整；与 R3“单个文件的问题不影响整体”同一原则。受管封面存储与绑定失败仍令扫描不完整。
- 2026-11-08 主会话核实（八号机只读）：fnOS 回收站在 `/vol2/1001/.@#local/trash`（点开头，隐藏项规则已覆盖）；卷级系统目录 `@appmeta`、`@team` 补进内置名单；`thumb` 名字太常见，不加。音乐目录内只有 1 个点开头的隐藏文件，没有 `._*`。

## 依赖

S 线第 1 项，无前置。⑩ 格式扩展依赖本项对不支持格式的新处理；④ 增量扫描在本项之后。

## 实施结果（2026-11-08）

提交：`c4a4514`（默认过滤与按目录排除、单文件失败只记诊断并保住原曲目、空根保护、坏封面只记诊断、CUE 宽松匹配、扫描详情显示跳过数）、`f29aa77`（浏览器用例改读新的不完整说明）。随 S1 合并 `c08c15e` 入 main，迁移 `0034_scan_file_filter`。

裁决与偏离：实施中没有新增，均按上文“已裁决”实现。

验收：

- 单测与集成：满足。`TestScanFilter*`（隐藏项、内置名单整段相等且不分大小写、用户排除按相对路径、预计数跳过整棵子树）；`TestPostgreSQLScanLifecycleHeld*`（读不了的目录只保住该子树、不安全 CUE 保住虚拟轨）、`TestPostgreSQLHeldTrackCannotLoseUUIDToNewMatchingFingerprint`；`TestPostgreSQLEmptyRootProtection`；`TestCueNameEqualNormalizesUnicodeAndCase`、`TestCueReferenceLooseMatchingRequiresUniqueFile`；坏封面 `TestPostgreSQLArtworkIntegrationUnreadableCoverKeepsExistingBinding`；排除项接口 `TestPostgreSQLExclusionAPI*`。PostgreSQL 用例 0 SKIP。
- 混合扫描：满足。`TestPostgreSQLScanLifecycleHeldFilesStillReconcilesDeletedSources`：`._x.flac` 被过滤（不计数、不记诊断），不支持格式与损坏 FLAC 各记一条诊断、跳过 2、原曲目 UUID 保留，真正删除的文件标 missing。⑩ 支持 APE 后，夹具里的不支持格式改为 `.wma`（`973d9ed`）。
- 真实库只读 smoke：满足。本机 music/ 上 `a7ea895`→`f3d3d04` 两个新库各首扫、重扫，均 succeeded，68 发行/284 曲目，missing 0，诊断都是 `cue_reference` 94 条且逐项一致，新增字段 `skipped_files=0`，重扫 ID 全保留。本库没有触发 R1、R3–R8 的异常路径，只证明无回归。
- 前端：排除项、跳过文件数与新的不完整说明在 1440/375 截图经主会话目检；S1 浏览器四套件 160 通过、1 失败，失败为旧文案断言，已由 `f29aa77` 同步。

已知局限：内置名单按整段名字相等匹配，专辑目录恰好与名单同名时会被跳过（design R-4，可接受）。

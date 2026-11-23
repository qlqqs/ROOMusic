# Research: 发行的合并与拆分（子任务 H）调研与失败用例清单

- **Query**：扫描怎样把曲目归成发行、发行身份怎样跨重扫保持；扫描会在哪些情形把一个发行分成几个或把几个并成一个；什么挂在发行上；已有的相似原语；强证据有哪些数据；撤销的要求与阻碍；界面入口；工作量与风险；方案选项
- **Scope**：internal（只读代码与文档，未运行测试，未查外部资料）
- **Date**：2026-11-23

约定：每节先写有出处的事实（`文件:行`），再写标明 **推断** 的内容。路径相对仓库根。

---

## 0. 决策背景（为什么现在做、当初为什么搁置）

| 出处 | 事实 |
| --- | --- |
| `docs/architecture/core-1-roadmap.md:65` | R1（2026-11-03）：Release 合并与拆分**搁置**，“等真实库出现扫描分错、而现有的‘归入其他专辑’‘移出为独立专辑’解决不了时，再按 R4 立项”；前提是“实际只有一个管理员”（同文件 :61）。2026-11-22 由 M7 取代，恢复为子任务 H |
| `docs/architecture/adr/0002-metadata-overlays-decisions.md:14` | 同一裁决的 ADR 0002 记录：“整组合并/拆分搁置（R1）……按 ADR 0003 的轻量标准”。**没有记录技术上的反对理由**，只记了“没有真实需求 + 单管理员” |
| `docs/architecture/adr/0003-governed-operations-decisions.md:18` | 2026-11-22 恢复立项：“一律由模型提出并审查，只在强证据下自动使用”；仍待批准的是“Release 合并/拆分的具体方案（M7 子任务 H）” |
| `docs/architecture/adr/0006-auto-curation-and-sources.md:117` | 结构操作（含发行合并拆分）“不是字段值，没有‘层’；靠操作记录的来源区分人工与自动” |
| 同上 :184、:186–187 | 发行的合并与拆分“只在强证据下：外部对应表明这些曲目属于同一个外部发行（合并）或不同的外部发行（拆分）。一律由模型提出并审查，程序层不直接做”；强证据“必须是已经保存、状态为生效的外部对应。模型……声称有依据而库里没有对应记录时，后端转人工” |
| 同上 :270 | 审查策略第 2 版：“强证据类操作（归组、发行合并拆分）……证据里没有生效的外部对应时，后端直接转人工” |
| 同上 :312 | R1 的搁置被取代，“自动使用只限强证据” |
| 同上 :390 | 验收：“自动的专辑归组、艺人合并、发行合并只在有强证据时发生，每一项都能撤销并回到操作前原样” |
| `.trellis/tasks/11-22-auto-curation-sources/prd.md:118` | R13：“把扫描分错的发行合成一个、把合错的拆开；重扫后保持；修正与核对有明确去向；可撤销；自动使用只限强证据” |
| `.trellis/tasks/11-22-auto-curation-sources/design.md:228–230` | “扫描按曲目成员关系认发行。合并与拆分记为人工或自动的‘成员归属意图’，扫描落库时尊重它，重扫不打回原形。修正、核对、默认版本、收藏的去向在操作时确定并记入前态，撤销按前态恢复。这是本任务技术风险最高的一项” |
| 同上 :277 | 已知风险：“发行合并与拆分与扫描的身份规则直接相互作用，是最可能返工的一项” |
| `.trellis/tasks/11-22-auto-curation-sources/implement.md:171–174` | H 范围：先调研与失败用例清单；成员归属意图、重扫保持、修正与核对的去向、撤销；界面入口；接入 D。验证：重扫、增删文件、改名移动之后保持；撤销回到原样；与认领、归组、默认版本的相互作用 |
| 同上 :157、:168 | I 留给 H：曲目修正挂在曲目上，H 移动曲目时修正随曲目走，撤销记录仍在原发行上。G 留给 H：结构类撤销判定照 G（“写下的每一项仍是它写的 + 之后没有别的生效中的操作”，按插入时刻，0066）；开放结构工具时审查策略加强证据那一条（第 4 版） |

ADR 0002 里早已写下、至今有效的约束（R1 之前就存在，可视为“当初识别出的问题”）：

| 出处 | 约束 |
| --- | --- |
| `docs/architecture/adr/0002-metadata-overlays.md:136` | “Track 来源相同，但扫描拟改变其所属版本：字段修正与关系修正分别判断。**已确认的关系不能被当前 `medium_id` 重写逻辑绕过**；必须先形成 catalog 冲突处理路径” |
| 同上 :137 | “合并、拆分或重新归组：先明确每个修正的目标映射和冲突决议；不能把 Release 修正复制给全部拆分结果，或按最后写入覆盖合并双方修正” |
| 同上 :193–196 | 严格多碟与 CUE 父子规则只说明一个 Release 的来源结构；“Group 成员调整不是删除/折叠 Release；Release 合并、拆分和 Track 重挂必须分别定义类型化操作、修正分配、版本冲突和逆操作。它们不能藏在一次字段编辑里” |
| 同上 :249 | 验收矩阵：“合并/拆分/重新归组带相斥修正：先给出完整映射与冲突决议；失败不改变任何相关实体；逆操作恢复修正意图而非源标签” |
| `docs/architecture/adr/0003-governed-operations-appendix.md:12` | 风险分类：合并拆分属“结构性或批量共享变更”，需要“完整影响预览、范围上限、相关 revision 和可执行逆操作” |

---

## 1. 扫描现在怎样把曲目归成发行

### 1.1 整理范围（scope）——组织的最小单位

- `backend/internal/catalog/ingest/scope.go:10–23` `OrganizationScope`：碟目录 → `disc-parent:<父目录>`；注册根下的文件有专辑标签 → `root-album:<专辑名哈希>`，没有 → `root-source:<路径>`；其余 → `directory:<目录>`。
- `backend/internal/library/scanner.go:757–769`：扫描**逐个 scope** 读暂存观察并调用 `ingest.OrganizeObservations(observations)`；每个候选单独 `persistOneCandidate`（:817，独立短事务）。
- 结论（事实）：**organizer 永远看不到别的 scope 的观察**。跨 scope 的合并只有一条：碟目录合到它的父目录（`disc-parent:`）。

### 1.2 候选种类（`backend/internal/catalog/ingest/organizer.go:17–23`）

| 种类 | 来源规则 | 锚点 Partition（`stableAnchorPartition`，:1288–1304） |
| --- | --- | --- |
| `ordinary_directory` | 普通目录（:712） | 空 |
| `strict_multidisc` | 父目录下碟目录满足合成条件（:713–716，`planDiscParent` :311–390） | 空 |
| `box_leaf` | 碟目录不满足合成条件，各自成为候选（:717） | 空 |
| `same_dir_split` | 同目录专辑分区冲突、无严格多数、整目录不连续（:788–803） | `source:<组内最小相对路径>` |
| `format_split` | 同目录两种以上格式、各自连续、跨格式重号（:761–766；`continuity.go:94–129`） | `format:<格式>`，`VersionGroup="format:"+目录`（:819） |
| `loose_album` | 注册根下有专辑标签的散文件，按 `专辑|专辑艺人` 分组（:741–758） | `source:<最小路径>` |
| `loose_unknown` | 注册根下没有专辑标签的散文件，**每个文件一个候选**（:748） | `source:<路径>` |

### 1.3 分组键与同目录规则

- 分区键 `albumPartition`（`organizer.go:504–517`）= 规范化的“去碟号专辑名 | 专辑艺人”；推断出的专辑名或专辑艺人当作空。缺专辑艺人的观察在同专辑只有一个明确专辑艺人时与它兼容（`compatibleAlbumPartition` :540–550）。
- 同目录（:768–786）：`len(partCounts)<=1`、或最多的分区严格过半（`winnerCount*2 > authoritativeCount`）、或整目录连续（`continuousObservations`）→ **整目录一个候选**；否则按分区拆成 `same_dir_split`，无专辑标签的每个文件单独一个（:796–799），并带 `same_directory_conflict` 关注原因（:883–885）。
- 连续性（`continuity.go:13–46`）：至少 2 首、有第 1 碟、每碟轨号恰为 1..n、无重号，允许缺碟。
- 碟目录识别（`.trellis/spec/backend/library-scan-grouping.md:49`）：只认 `disc/disk/cd` + 1–99 的数字、英文数词或罗马数字；**纯数字目录、中文碟号、`[CD]`、`BACD-0001` 不认**（测试 `discmarker_test.go:40–53`：`"1"`、`"01"`、`"第一张"`、`"第1碟"`、`"碟1"`、`"2CD"`、`"CD1A"` 等都不是碟目录）。
- 多碟合成条件（`library-scan-grouping.md:51–55`；`organizer.go:311–390`）：父目录不独占（父目录或兄弟目录有别的音频）时只认“纯碟号目录”（Prefix/Suffix 均空）、≥2 个、编号连续、兼容分区 ≤1；独占时还接受带前缀/后缀的碟目录，但前缀须唯一、分区唯一。
- CUE（`organizer.go:1062` `resolveCueCandidateGroups`、:1309–1334 `resolveCuePhysicalCoexistence`；`library-scan-pipeline.md:89–93`）：CUE 虚拟轨按父音频对齐 scope；同一候选内，同一父音频有多个虚拟轨时隐藏整轨、只有一个时保留物理轨；“无关物理文件永远不参与该决策”。
- 字段决定按候选内多数决（`chooseValue`，`organizer.go:93`），发行级扫描值写入 `releases` 本表（`persist.go:184–212`）。

### 1.4 发行身份怎样跨重扫保持

| 机制 | 出处 | 事实 |
| --- | --- | --- |
| 曲目身份 | `library-scan-pipeline.md:70–81` | 曲目 UUID 按来源身份匹配，同 root 内改名移动在遍历完整且指纹唯一 1:1 时沿用；同路径换内容新建曲目 |
| 发行身份 | `identity_plan.go:431–459` `allocateReleases`；`library-scan-pipeline.md:82–85` | 候选已规划沿用的曲目里，**严格过半当前属于同一个既有 Release** 时沿用；多个候选争同一个 Release 时重叠多者得，平局时“留在原位置的一方”（`candidate_anchor` 等于候选键）优先，再按候选键；落选或无倾向的候选**新建 Release 与新组** |
| 锚点 | `organizer.go:1348–1351`；`persist.go:185、:202` | `candidate_anchor = <root>:v2:<kind>:<scope>:<partition>`，只记最近一次位置，不参与查找 |
| 原发行 | `library-scan-pipeline.md:104`（迁移 0037） | 暂存列 `previous_release_id` 记规划前曲目所在发行，只用于收藏转移（`group_moves.go:7–27`） |
| 写入时改挂曲目 | `persist_track.go:81–82` | 沿用曲目时 `UPDATE tracks SET medium_id=$1 …`：**每次写入候选都把曲目挂到本候选的发行上** |
| 增量跳过 | `candidate_state.go:37–71`；`library-scan-incremental.md:58` | 只有“所有曲目的 Medium 仍属于状态 Release”才跳过；“**治理或其他候选改变曲目归属后必须重写**” |
| 组 | `governance-contracts.md:154`；`library-scan-grouping.md:77` | 扫描不改既有发行的 `group_id`；沿用 Release 的组永不移动 |
| 测试 | `backend/internal/library/release_membership_integration_test.go` | 加更靠前的文件（:78）、删最小文件（:128）、目录改名（:174）沿用 Release ID；移动的文件加入目标发行（:211）；5 首里 3 首移走时多的一方沿用（:242–294）；少数重叠（2 加 3）与恰好一半（1 加 1）新建 Release（:296、:328） |

### 1.5 “成员归属意图”可能的含义（推断）

**推断**：设计文档（`design.md:228`）要的是一份扫描写入时会查的持久记录，说明“这些曲目属于发行 X”，并且满足：

1. 重扫不打回：`persistTrack` 的 `medium_id` 重写（`persist_track.go:81`）与增量“治理改归属后重写”（`library-scan-incremental.md:58`）都要服从它；这正是 ADR 0002:136 “已确认的关系不能被 `medium_id` 重写逻辑绕过”。
2. 键要跟着曲目身份走，不跟路径：曲目 UUID 在改名移动时沿用（§1.4），所以以 `track_id` 为键的意图天然经得起同 root 内改名移动；换内容（新曲目 UUID）与跨 root 移动时意图丢失，与 I 的已知限制同类（`adr/0006:44` I 说明）。
3. 增删文件：新增的文件没有意图。可能的规则是“新文件跟它所在候选的多数走”，或“意图以候选（scope + partition）为单位”而不是逐曲目。两种语义不同：逐曲目意图在 scope 新增曲目时不覆盖它们；按候选的意图（例如“scope Y 的候选并入发行 X”）会把新增文件一起带过去。
4. 发行身份：意图里的目标发行 ID 必须让 `allocateReleases` 不再因为“不过半”去新建发行，否则意图指向的发行会被扫描丢下（见 §2.3 的 F-R1）。

---

## 2. 失败用例清单

### 2.1 扫描把一个真实发行拆成几个（需要“合并”）

| 编号 | 情形 | 现在的结果 | 依据 |
| --- | --- | --- | --- |
| S1 | 碟目录名不被识别：`Album/1`、`Album/2`；中文 `第一张`、`碟1`；`2CD`、`CD1A` | 每个目录一个 `ordinary_directory` | `organizer_test.go:571`（“plain numeric folders are ordinary”）；`discmarker_test.go:40–53` |
| S2 | 碟目录在艺人目录下、带前缀：`艺人/某精选 CD1`、`艺人/某精选 CD2` 与 `艺人/另一张专辑` 同级（父目录不独占，带前缀的碟目录不参与严格多碟） | 各自 `box_leaf` | `organizer_test.go:563`、:565；`organizer.go:321–333` |
| S3 | 碟目录专辑标签不同（`First`/`Second`，或 `Album Vol.1`/`Vol.2` 这类 `stripAlbumDiscMarker` 不剥的写法） | 各自 `box_leaf` | `organizer_test.go:568`；`library-scan-grouping.md:50` |
| S4 | 碟号不连续且没有专辑标签（`CD1`、`CD3`）；或部分有标签 | 各自 `box_leaf` | `organizer_test.go:558–559`；`TestOrganizeNonContiguousDiscDirectoriesStayLeafCandidates` :117–130 |
| S5 | Box 里前缀不唯一：`Box/X CD1`、`Box/X CD2`、`Box/Y CD1`；或同号重复副标题（`Disc 1 - 夏`、`Disc 1 - 春`） | 全部 `box_leaf`（X 的两碟也被拆开） | `organizer_test.go:570`、:556 |
| S6 | 碟目录加父目录直接音频（`A/CD1` + `A/direct.flac`） | `box_leaf` + `ordinary_directory` | `organizer_test.go:562` |
| S7 | 附加内容放在非碟子目录（`A/CD1`、`A/CD2`、`A/Bonus`） | 多碟一个 + `A/Bonus` 单独一个 | `organizer_test.go:566` |
| S8 | 同目录两三种专辑分区，没有严格多数且整目录不连续（例如合辑每首专辑艺人写成曲目艺人、同时轨号缺失或重复） | 每个分区一个 `same_dir_split`，无专辑标签的每个文件一个 | `organizer.go:784–803`；`TestOrganizeSameDirectoryConflictUsesMajorityOrStableSplit` :98–114；`release_membership_integration_test.go:101–125` |
| S9 | 注册根下的无标签散文件（一整张专辑直接放在根目录） | 每个文件一个 `loose_unknown` | `organizer.go:748`；`TestOrganizeRootUnknownAndCueIdentityAreExplicit` :133–139 |
| S10 | 同一张专辑分在两个目录（例如原目录 + `Album (bonus tracks)` 兄弟目录，或部分曲目被移到另一个目录） | 两个发行，多的一方沿用原 ID | `release_membership_integration_test.go:274–283` |
| S11 | 同一张专辑跨两个注册根 | scope 按根划分，从不合并 | `library-scan-pipeline.md:79`（跨 root 从不重绑定）；`scanner.go:757` 逐根逐 scope |
| S12 | 同目录同一张专辑两种格式（FLAC + MP3） | `format_split` 两个发行、同组——这是**有意**的版本拆分，不算失败 | `library-scan-grouping.md:75`；`organizer_test.go:849` |

### 2.2 扫描把几个真实发行并成一个（需要“拆分”）

| 编号 | 情形 | 现在的结果 | 依据 |
| --- | --- | --- | --- |
| M1 | 同目录两张专辑，一张严格过半（例如 `A/` 里 3 首 A 专辑 + 1 首 B 专辑） | 少数的并入多数的发行，字段 `uncertain_apply` | `organizer.go:784`；`release_membership_integration_test.go:221–228`（“放错位置的曲目……作为杂音留在 A 的 Release 中”）；`TestOrganizeSameDirectoryConflictUsesMajorityOrStableSplit` :80–96 |
| M2 | 同目录多张专辑都没有专辑标签（“Singles”“未整理”文件夹） | `len(partCounts)<=1` → 整目录一个 `ordinary_directory` | `organizer.go:772–786`（无标签不计入分区） |
| M3 | 同目录两张专辑分区不同，但合起来轨号连续（例如 1–6 与 7–12） | 连续性优先 → 整目录一个 | `organizer.go:784`（`continuousObservations`） |
| M4 | 父目录下两个纯碟号目录实际是两张不同专辑但都没有专辑标签或标签相同 | 合成一个 `strict_multidisc` | `organizer_test.go:548`（无标签的 `CD1`/`CD2` 合成）；`library-scan-grouping.md:52–55` |
| M5 | 注册根下散文件，同 `专辑|专辑艺人` 但其实是两个版本（同名重发、不同年份） | 一个 `loose_album` | `organizer.go:741–758`（只按分区键） |
| M6 | 同目录里 CUE 整轨 + 同一张专辑的分轨文件（同格式） | **推断**：`resolveCuePhysicalCoexistence` 只隐藏 CUE 的父整轨（`organizer.go:1325–1329`），分轨文件留在同一个候选里，形成重复曲目的一个发行；若编码不同则可能走 `format_split`。仓库里未找到覆盖这一情形的测试 | `organizer.go:1306–1334` |
| M7 | 一张合辑里不同曲目带了不同的 MusicBrainz 发行 ID（标签说它们属于不同外部发行） | 本地仍是一个发行；程序层匹配记 `mixed_release_tags`，拿不准 | `backend/internal/curation/program/match_album.go:248–250`；`curation-contracts.md:401` |

### 2.3 现有身份规则下，“操作后再扫描”的已知失败（推断，均由 §1.4 的代码推出）

| 编号 | 情形 | 推断的后果 | 依据 |
| --- | --- | --- | --- |
| F-R1 | 只在库里把 B 的曲目改挂到 A（不改扫描） | 下次扫描：B 所在 scope 的候选与 A 所在 scope 的候选都倾向 A（重叠 100%），重叠多者得 A，**另一个新建一个全新的 Release 与组**（不是回到 B）；B 变成空的不可见发行，若有修正就进入“待认领” | `identity_plan.go:444–457`；`persist.go:190–200`；`claims.go:31–37` |
| F-R2 | 只在库里把 A 的一部分曲目改挂到新发行 C | 下次扫描：A 的候选仍过半沿用 A，`persistTrack` 把 C 的曲目改挂回 A；C 变空、不可见 | `persist_track.go:81`；`identity_plan.go:449` |
| F-R3 | 增量扫描 | 被治理改过归属的候选不能跳过，一定重写 | `candidate_state.go:42–45`；`library-scan-incremental.md:58` |
| F-R4 | 两个候选写同一个发行（跨 scope 合并若让扫描照做） | `PersistCandidate` 每次都整体重写发行的扫描字段、字段决定、归组证据、专辑级署名（`persist.go:135–165`、:202），后写的覆盖先写的；介质按 `(release_id, position)` 取（:81），两个候选的第 1 碟会落在同一个介质；`loadTrackPlans` 要求一个候选的观察指向同一个规划发行（`persist_track.go:57–59`） | 同左 |
| F-R5 | 合并后 B 的文件被整张替换或新增文件多于原有 | 规划新建发行，意图指向的旧曲目失去来源；与认领已知限制同类 | `governance-contracts.md:157`；`claim-contracts.md:49` |

### 2.4 已记录的相关限制

- `.trellis/spec/backend/claim-contracts.md:163–164`：认领“不做……一个旧版本拆给多个新版本，或多个旧版本合到一个新版本时逐项挑选；……不合并或拆分 Release（M4 第五项）”。
- `.trellis/spec/backend/library-scan-checks.md:65`：不过半新建 Release，“格式版本可加入已有组，其他情况新建 ReleaseGroup”。
- `docs/architecture/adr/0002-metadata-overlays.md:112`：“新 Release 不自动继承旧 Release 的人工意图”。
- 现有界面只能“归入其他专辑”“移出为独立专辑”（组层面），对 S1–S10 的结果是“同一专辑下的两个版本”，不是一个发行（`release-admin-actions.tsx:86–87`）。

---

## 3. 挂在发行上的东西：合并、拆分时要搬或要定去向

### 3.1 发行级（键是 `release_id` 或 `group_id`，曲目移走时**不会**自动跟随）

| 数据 | 表 / 出处 | 扫描是否读写 | 备注 |
| --- | --- | --- | --- |
| 人工修正 | `release_corrections`（`governance-contracts.md:31`，迁移 0017） | 不碰 | 认领的规则：目标已有同字段的以目标为准（`claim-contracts.md:66`） |
| 自动层 | `release_auto_values`、`releases.auto_revision`（`curation-contracts.md:23–25`） | 不碰 | “认领不搬自动层”（`curation-contracts.md:51`） |
| 人工版本号 | `releases.edit_revision`（`governance-contracts.md:29`） | 不碰 | 修正、归组、核对、认领共用 |
| 扫描字段与证据 | `releases` 扫描列、`release_field_decisions`、`release_grouping_evidence`、`release_credits`（`persist.go:135–165`） | 每次写入整体重写 | 合并后由谁的候选写、怎样合，是扫描侧问题（F-R4） |
| 本地封面 | `release_artworks`（迁移 0005） | 扫描按候选重新选取 | 歧义候选只用内嵌封面（`library-scan-grouping.md:30`） |
| 外部封面 | `release_external_artworks`（`curation-contracts.md:262`），一个发行至多一张 | 不碰；认领不搬（:292） | 撤销按记录的目标发行加锁（:329） |
| 外部对应（发行） | `external_links` object_type=`release`（`curation-contracts.md:206–214`） | 不碰 | 一对象一源至多一条；`object_id` 无外键 |
| 外部对应（专辑） | `external_links` object_type=`release_group` | 不碰 | 强证据归组依据（`curation-contracts.md:448`） |
| 问题核对 | `release_issue_reviews`（`issue-review-contracts.md:19–33`） | 不碰 | `missing_tracks` 依据按曲目 ID；`grouping` 依据是 `{reason, source_refs}`，候选变了就过期 |
| 归组 | `releases.group_id`（`governance-contracts.md:98–103`） | 不改既有发行 | 组从不删除（:40） |
| 默认版本 | `release_groups.default_release_id`、组的 `edit_revision`（`default-version-contracts.md:27`） | 不碰 | 不可见或不在组里时静默失效 |
| 专辑收藏 | `favorite_albums(group_id)`（迁移 0021:14–20） | 扫描只在原组完全腾空时转移（`group_moves.go:7–27`；`library-scan-pipeline.md:105–107`） | 由 `personal.MoveAlbumFavorites` 执行 |
| 艺人作用域覆盖 | `artist_assignment_overrides`（release_id 或 track_id，迁移 0026:39–47） | — | 按专辑拆分艺人保存“发行快照 + 既有 track 覆盖”（`artist-contracts.md:75`）；发行级覆盖不会跟着搬走的曲目 |
| 艺人信号 | `artist_alias_signals(release_id, alias_release_id)`（迁移 0031:7–21） | 投影重算 | — |
| 整理工作状态 | `curation_work_states`（主题 release，目标发行）（`curation-contracts.md:382`） | 不碰 | `input_digest` 由发行证据算出，成员变化会让摘要变 |
| 待定项 / 例外 | `curation_pending_items`（每发行每种至多一条未结束的）、`curation_model_exceptions`（`curation-contracts.md:385`、:649） | 不碰 | — |
| 不再提出标记 | `curation_suppressions`，target 为 release（字段、`group_join`、`external_link`、`artwork*`）（`curation-contracts.md:35–37`、:269–276） | 不碰 | — |
| 操作记录 | `operations.target_id`（发行；不设外键）（`governance-contracts.md:20–24`） | 不碰 | 认领另有按来源的表达式索引（`claim-contracts.md:21`） |
| 增量状态 | `scan_candidate_states(release_id)`（迁移 0041） | 扫描读写 | 记录候选键 → 发行 |
| 搜索投影 | `searchindex.RefreshRelease`（`persist.go:169`） | 扫描与治理同事务刷新 | `library-scan-pipeline.md:122` |

### 3.2 曲目级（键是 `track_id`，曲目改挂时自动跟随）

| 数据 | 出处 | 备注 |
| --- | --- | --- |
| 曲目修正、曲目自动层 | `track_corrections`、`track_auto_values`（迁移 0065:4、:16） | 操作记录的目标是“当时所在的发行”；撤销记录归当前发行（`governance-contracts.md:237`；`curation-contracts.md:733`） |
| 外部歌词 | `track_external_lyrics`（`curation-contracts.md:265`） | “歌词操作记在曲目当时所在的发行上：曲目之后被改挂到别的发行时，按新发行筛选操作记录看不到它”（:351） |
| 外部对应（曲目 → 录音） | `external_links` object_type=`track` | — |
| 标签标识 | `track_identifiers`（迁移 0059） | 扫描每次重写（`persist_track.go:123–139`） |
| 声学指纹缓存、音频分析 | 迁移 0063、0033 | — |
| 收藏曲目、歌单、播放历史、播放区队列 | 迁移 0021:6–10、0022:17/44、0016:10、0043:42 | 显示位置按曲目**当前**所在发行实时取（`backend/internal/catalog/track_placement.go:15–29`：“曲目会随重扫换到另一个发行，所以位置只在读取的那一刻有效”） |
| 曲目署名、艺人标签证据 | `track_credits`、`artist_tag_evidence` | 扫描重写；`artistgraph.Refresh` 刷新 |

### 3.3 介质与位置

- 介质 `media(release_id, position, title)`，曲目 `tracks(medium_id, position, disc_number)`（`persist.go:75–90`、`persist_track.go:76–82`）。
- ADR 0006 U9：“曲序与碟号不可修正”（`adr/0006:27`、:114）。
- **推断**：合并两个各自只有第 1 碟的发行，必须给被并入的一方的介质换一个位置（例如接在后面），否则 `(release_id, position)` 冲突；这既是数据问题也是“碟号不可修正”边界内的一个新决定。

---

## 4. 已有的相似原语

### 4.1 认领 `release_claim`（最接近“合并”的现成写路径）

- 范围：把**不可见**旧发行上的四类人工状态（修正、所在专辑、默认版本、核对）迁给**可见**新发行；新发行已有的以新发行为准（`claim-contracts.md:5`、:64–69）。
- 不搬：自动层、外部封面、外部歌词、外部对应（`curation-contracts.md:51`、:212、:292）。
- 加锁：记录（核对行引用的操作 `FOR KEY SHARE`）→ 两个发行行（UUID 升序 `FOR UPDATE`）→ 涉及的组行（UUID 升序）（`claim-contracts.md:84–86`；`governance/claim.go:515–544`）。
- 计划与预览共用纯函数 `buildClaimPlan`（`claim.go:134`），预览令牌拦截版本号覆盖不到的变化（`claim-contracts.md:71–74`）。
- 撤销：恢复两个发行与相关组的全部前态，任一方之后又改过就 `revert_conflict`；撤销不要求新发行可见（`claim-contracts.md:103–107`）。
- 旧发行被移进一个新建的空组，才离开待认领列表（`claim-contracts.md:91`）。
- 待认领判定 `releaseClaimablePredicate`（`governance/claims.go:28–37`）：不可见 + 有修正 / 组里有别的发行 / 是默认版本 / 有核对。
- **推断**：合并后被清空的发行（若扫描或操作让它变得不可见）会自动满足这个谓词、出现在“待认领”里；H 若复用认领的搬迁逻辑，要决定被并入方是否要像认领一样移到空组以免重复提示。

### 4.2 归组 `release_group_change`

- `POST /api/v1/releases/{id}/group`，`join | detach`；先锁发行行再按 UUID 锁组行；`edit_revision + 1`（`governance-contracts.md:98–103`；`governance/group_change.go:65–104`）。
- 程序层强证据归组 `Writer.JoinGroup`：发行有 MB 发行对应且其 `external_link` 记录后态里有外部专辑 X，目标专辑有对 X 的对应；最近一次归组是人做的 → `409 manual_structure`；撤销来源为 curation 的归入写 `group_join` 标记（`curation-contracts.md:446–452`）。
- **这是 H 的“强证据 + 人工结构优先 + 撤销写标记”的直接样板。**

### 4.3 艺人合并撤销（G）

- 能撤销的条件：“写下的每一项仍是它写的 + 涉及的艺人之后没有别的生效中的操作”（按 `operations_artist_targets` 与 `(created_at,id)` 判断），支持链式倒序撤销（`artist-contracts.md:100`；`governance-contracts.md:150`）。
- 多目标的记录表 `operations_artist_targets(operation_id, artist_id)`（迁移 `0026_artist_catalog.sql:119–123`）。
- 自动合并的阻断：闭包标记 `artist_merge`、人工拆分或撤销同时涉及两侧 → `manual_structure`（`curation-contracts.md:791–801`）。
- 艺人拆分仍不可撤销（`artist-contracts.md:95`）。

### 4.4 曲目身份规则

- 曲目 UUID 跨改名移动保持、换内容新建（§1.4）；ADR 0002:133 “不按标题相似搬移修正”。I 的已知限制：同一路径换了文件产生新曲目，旧修正留在不在库的曲目上（`adr/0006:44`）。

### 4.5 扫描自身的“归属变化”处理

- `EmptiedGroupMoves`（`group_moves.go:7–27`）：根据 `previous_release_id` 与当前归属，原组所有发行都没有任何曲目时把专辑收藏转给接收最多的发行的组；“仅转专辑收藏，不搬修正/认领/默认/歌单/历史”（`library-scan-pipeline.md:106`）。

### 4.6 R1 搁置时识别出的问题（汇总）

事实层面，R1 的记录只有“单管理员、等真实需求”（§0）。技术问题散见于 ADR 0002（:136、:137、:195–196、:249）与 ADR 0003 附录（:12），归纳为：
1. 扫描的 `medium_id` 重写会绕过人工确认的关系；
2. 修正在合并时不能“后写覆盖”，在拆分时不能复制给全部结果，需要显式映射与冲突决议；
3. 发行合并、拆分、曲目重挂要各自是类型化操作，带版本冲突与逆操作；
4. 需要完整影响预览、范围上限与 revision。

---

## 5. 强证据：库里现在有哪些数据

### 5.1 存储

- `external_links`：`(source, object_type, object_id)` 唯一，反查索引 `(source, object_type, external_id)` **不唯一**（`curation-contracts.md:206–214`）。
  - 发行 → MB release；专辑 → MB release group；曲目 → MB recording；艺人 → MB artist。
  - 行在即生效；只经 `Writer.ApplyLinks`（类型 `external_link`）写入；撤销写 `external_link` 标记（:433）。
- 匹配记录：`external_link` 操作的后态含每一项 `{object_type, object_id, external_id, external_group_id?}`（`curation-contracts.md:425`、:435）；发行与配对曲目在**同一条记录**里。
- 标签标识：`track_identifiers` 的 `mb_release`、`mb_release_group`、`mb_recording`、`mb_track`、`mb_disc`、`isrc`、`acoustid` 等（`backend/internal/catalog/metadata/identifier.go:17–26`）——**它们是扫描值，不是“已保存的外部对应”**；ADR 0006:186 要求强证据必须是已保存、生效的外部对应。

### 5.2 程序层什么时候会建出这些对应（决定了强证据实际存在与否）

| 情形 | 事实 | 出处 |
| --- | --- | --- |
| 一个本地发行只是外部发行的一部分（例如 S2、S3 里的一碟） | 只有 `tag_id` 一步允许本地是外部的子集（曲号不是推断时）；其他各步都要求 `count_equal`，否则 `track_count` 拿不准 | `program/match_album.go:165–170`；`curation-contracts.md:401–405` |
| 同上，模型层 | `ModelMatcher.readMatch` 要求碟数相等（`media_count_mismatch`）与 `countEqual`（`track_count`） | `program/model_matcher.go:180–190` |
| AcoustID 一步 | 先去掉曲目数或碟数与本地不符的发行 | `curation-contracts.md:576` |
| 两个本地发行对上同一个外部发行 | `addUnoccupiedLink` 只按本地对象判重（`ON CONFLICT(source,object_type,object_id)`），不阻止两个本地发行对应同一个外部 ID | `curation/external_link.go:135–160` |
| 已对应的两半随后的归组 | `planGroup` 按外部专辑 ID 反查本地组，唯一可见时归入 | `program/enrich.go:110–157` |
| 一个本地发行的曲目带了不同的 `mb_release` 标签（M7） | 直接 `mixed_release_tags` 拿不准，**不建任何对应** | `program/match_album.go:248–250` |
| 曲目级对应 | 只在发行匹配成功时为配对曲目写；单曲方式只对 `loose_unknown`（`curation-contracts.md:415–418`、:425） | — |

### 5.3 推断：两个方向的强证据可得性不同

- **合并方向**：只有当两半都带 MusicBrainz 发行标签（`tag_id`）时，现有程序层会让两个本地发行各自对上同一个 MB release（并因同一个 release group 被自动归入同一张专辑，表现为“两个版本”）。这时库里已有强证据：`ObjectsLinkedTo("musicbrainz","release",X)` 返回 ≥2 个本地发行（`curation/links.go:71`）。没有标签的两半（S1–S10 多数情形）既过不了程序层也过不了模型匹配（§5.2），**库里不会有对应，按 ADR 0006:186 一律转人工**。
- **拆分方向**：混在一个本地发行里的曲目（M1–M7）整体匹配时就拿不准，曲目级对应不会建立；库里**没有**“这些曲目属于不同外部发行”的已保存对应。要有拆分的强证据，需要一种新的、对发行内部分曲目建立录音对应（并记下它们所属外部发行）的匹配步骤；现有 `external_links` 只存录音 ID，不存“录音在哪个外部发行上”，后者需要从外部发行的曲目表读回（适配器有缓存，`adr/0006:224`）。
- 因此按现状，“模型提出、审查通过、自动执行”的 H 只在“带标签、被拆开的发行重新合并”这一情形有数据支撑；其余由人工入口完成。

### 5.4 审查与后台工具的现状

- 后台白名单：`search_music_metadata`、`get_music_metadata`、`read_pending_items`、`resolve_release_match`、`shelve_pending_item`、`correct_release`、`correct_track`（`backend/internal/app/curation_model.go:107`）。
- 审查策略版本 3（`backend/internal/steward/runtime/review.go:20`）；规则文本 :27–37 里还没有“证据里没有生效外部对应就转人工”这条。
- 例外表 `tool` 约束只允许 `resolve_release_match`、`correct_release`、`correct_track`（迁移 `0065_track_corrections.sql:52`）。
- 对话与 MCP 的治理工具（`backend/internal/catalog/governance/stewardtools/tools.go:63–76`）有 `change_release_group`、`claim_release`（配 `preview_release_claim`）、`merge_artists` 等，**没有发行合并拆分工具**。
- 待定项种类只有 `release_match | recording_match`（`curation-contracts.md:385`）。

---

## 6. 撤销：“回到操作前原样”需要什么、阻碍在哪

### 6.1 现有规则

- 通用：撤销是新操作，恢复前态的意图；只撤最近一次（发行的 `edit_revision` 相等）；撤销不能再撤（`governance-contracts.md:139–144`）。
- 认领：多发行多组的前态全量记录，任一方之后改过 `revert_conflict`，不做部分撤销（`claim-contracts.md:103–107`）。
- G：按“仍是它写的 + 之后没有生效中的操作”判定，按插入时刻（`operations.created_at` 默认 `clock_timestamp()`，迁移 0066；`governance-contracts.md:150`）。
- 自动层：不看 `edit_revision`，按每格写入者判断（`curation-contracts.md:101–105`）。
- ADR 0006:141–142：“结构操作的撤销沿用各自现有的规则。之后被人工改过的结构，自动撤销被拒绝并在结果里列出”。

### 6.2 “原样”包含的项（推断，按 §3 列）

合并的前态至少要记：被并入发行 B 的每首曲目原来的 `medium_id`/介质位置；B 的修正、核对、自动层、外部封面、发行与专辑外部对应、默认版本、组与收藏在操作时的去向；A 原来的同类状态（冲突时谁胜）；整理工作状态与待定项的状态。拆分是反方向：被拆出的曲目清单、新发行 C 的 ID 与组、A 的哪些状态复制或留给谁。

### 6.3 阻碍（推断）

| 阻碍 | 说明 | 依据 |
| --- | --- | --- |
| B1 重扫之后的“原样”不存在 | 合并后若扫描（按意图）把新增文件放进合并后的发行，撤销时这些新曲目没有“原来所在的发行”；B 原来的扫描字段已被重写 | `persist.go:135–165`、:202 |
| B2 扫描新建发行 | 意图期间若 B 的成员被整张替换，B 的 ID 已不再被扫描使用；撤销要恢复到的发行可能已不可见 | `identity_plan.go:449`；`claim-contracts.md:107`（认领为此允许目标不可见） |
| B3 之后的人工修改 | 合并后的发行 A 又被修正、核对、归组：`edit_revision` 已变，按通用规则撤不了；按 G 的规则要判断“之后有没有生效中的操作”涉及 A、B 两方 | `governance-contracts.md:143`；`artist-contracts.md:100` |
| B4 曲目级操作的目标漂移 | 曲目修正、外部歌词等记录的目标是“当时所在的发行”，合并之后再做的曲目操作目标是 A；撤销合并后它们的记录目标与曲目实际所在不一致（已有的已知限制同类） | `curation-contracts.md:351`、:733 |
| B5 自动层与外部对应的“写入者” | 合并时若把 B 的自动层或对应搬到 A，原操作的“仍是它写的”判断会失效（写入者还是原操作，但目标发行变了）；认领因此选择不搬自动层 | `curation-contracts.md:51`、:101–104 |
| B6 不再提出 | 撤销自动合并要写“不再提出”标记，键需要能挡住“同一组发行再次合并”，并在扫描重建 ID 后仍命中（G 用身份闭包解决同类问题） | `curation-contracts.md:801` |
| B7 搜索、艺人投影、收藏 | 合并与撤销都要在同一事务刷新两方的搜索投影与艺人有效署名；专辑收藏转移在撤销时要还原 | `governance-contracts.md:219–225`；`library-scan-pipeline.md:105–107` |
| B8 加锁顺序 | 现有顺序“记录 → 发行行（UUID 升序）→ 组行（UUID 升序）”，扫描反馈先锁全部既有发行再锁组；CATP（目录写锁）在业务行锁之前 | `claim-contracts.md:135`；`governance-contracts.md:161`；`ingest/scan_feedback_snapshot.go:34–39` |

---

## 7. 界面入口

| 位置 | 事实 |
| --- | --- |
| `frontend/src/features/governance/components/release-admin-actions.tsx:57–123` | 专辑详情“更多 → 管理”分组：编辑发行信息、设为默认版本/改回自动选择（多版本时）、归入其他专辑…、移出为独立专辑（多版本时）、查看问题；面板里显示“可能接替了已不可见的《…》”的认领线索与上一次操作的撤销提示（`undoIntent` :26–42 按命令种类给出撤销） |
| `frontend/src/app/routes/album-detail-page.tsx:67–78` | `AdminAlbumActionRow` 把上述 actions 放进操作行 |
| 同上 :87–99、:154–159、:174 | 曲目行菜单（含右键）：管理员有“修正曲目信息…”（I），打开 `EditTrackDialog`；**没有多选** |
| `frontend/src/features/governance/components/join-album-dialog.tsx` | “归入其他专辑”的专辑搜索对话框；认领的手动搜索复用它（`claim-contracts.md:47`） |
| `frontend/src/features/governance/components/claim-*.tsx`、`app/routes/claims-page.tsx`、`app/router.tsx:320–322` | 待认领页 `/claims`：列表、详情、预览（逐项 move/skip）、目标选择 |
| `frontend/src/app/routes/issues-page.tsx`、`release-issues-detail.tsx` | 问题页，`grouping`（`same_directory_conflict`）类问题在这里列出（`issue-review-contracts.md:32`） |
| `frontend/src/app/routes/curation-page.tsx` | 整理记录、例外（E1、E2） |
| `frontend/src/features/governance/components/release-history.tsx` | 专辑详情的修改记录（按发行加专辑取并集） |

**推断**：合并的自然入口是专辑详情“管理”分组里的“与另一个发行合并…”（复用 `join-album-dialog` 的搜索），或在多版本专辑里对两个版本操作；拆分需要从曲目列表选择若干首，现在曲目列表没有多选，需新增交互。问题页的 `same_directory_conflict` 与认领页是另外两个可放提示的地方。

---

## 8. 工作量与风险评估（推断）

| 部分 | 规模 | 风险 | 理由 |
| --- | --- | --- | --- |
| 扫描侧尊重意图（`allocateReleases`、`PersistCandidate`、增量跳过、跨 scope） | 大 | 高 | organizer 按 scope 纯函数运行、每候选一个事务；跨 scope 合并意味着多个候选写同一发行（F-R4）；规划 SQL、介质位置、发行扫描字段的来源、增量签名都要改；身份规则的现有测试面大（§1.4 测试、`library-scan-checks.md:128–135`） |
| 读取侧叠加（若采用重定向/虚拟合并） | 大 | 中高 | 大量查询直接 `tracks JOIN media JOIN releases`（摘要、搜索、统计、曲目位置、艺人投影、对比、问题），叠加要在每处生效，并守住“无意图时逐项不变” |
| 治理操作本身（执行器、预览、前后态、撤销、Describe、多目标记录） | 中 | 中 | 有认领、归组、G 三份样板；新增多目标记录表（照 `operations_artist_targets`） |
| 发行级状态的去向（修正、核对、自动层、外部封面与对应、默认版本、收藏） | 中 | 中 | 每类要有明确规则与测试；认领已定过“目标已有的以目标为准”的先例 |
| 程序层/模型层接入（强证据核对、后台工具、审查第 4 版、例外 tool 约束、不再提出） | 中 | 中 | 合并方向的证据可由 `ObjectsLinkedTo` 直接核对；拆分方向现在没有证据来源（§5.3），若要做需新增部分匹配 |
| 前端（合并对话框、拆分的曲目多选、预览、撤销提示） | 中 | 低 | 有认领预览与归组对话框可复用；拆分的多选是新交互 |
| 迁移 | 小到中 | 低 | 只新增表与放宽约束（`adr/0006:354`） |

---

## H 的方案选项（推断）

以下均为推断，供规划时比较；未经实现验证。

### 方案 A：扫描查询“成员归属意图”表（设计文档原意）

- 做法：新表记“曲目 T（或候选键 K）属于发行 R，碟位置映射 M”，人工或自动写入，带操作 ID。扫描在 `allocateReleases` 之后、写入之前读意图：意图覆盖的观察强制规划到 R；`PersistCandidate` 对意图发行改为“追加模式”（不覆盖 R 的发行级扫描字段，或由意图指定哪个来源候选是 R 的“主候选”），介质位置按映射写；拆分时把被拆出的观察从原候选挪到一个新的候选（新锚点种类，如 `manual_split`，Partition = 意图 ID）。增量跳过的条件把意图纳入签名。
- 优点：重扫、增删文件、改名移动后保持（意图跟曲目 ID 走）；读取侧零改动，所有接口自然一致；ADR 0002:136 的要求在扫描处直接满足。
- 缺点与风险：改动集中在身份规划与写入，影响面最大（§8 第一行）；跨 scope 合并要改“一个候选一个事务、一个候选写一个发行”的结构；新增文件不在意图里时的归属规则要另定；回退时旧扫描器会把关系打回原形（ADR 0002:228–230 的兼容要求）。
- 范围内：合并（含跨目录、跨 scope，同 root）、拆分、重扫保持、撤销按前态恢复。范围外：跨 root、换内容后的意图延续（与 I 的已知限制一致）。

### 方案 B：扫描之后的再分配操作（像认领一样），扫描只做“不打回”的最小改动

- 做法：合并 = 把 B 的曲目改挂到 A（新介质位置）+ 按认领规则搬 B 的人工状态；拆分 = 新建发行 C（与新组或原组）并把选中曲目改挂到 C。扫描只加一条最小规则：`allocateReleases` 规划时若曲目带“已确认归属”（一张只记 `track_id → release_id` 的表），候选里这部分观察按已确认归属写入、其余照旧；即把“已确认归属”的曲目当作固定成员，不参与过半计算。
- 优点：治理侧大量复用认领（计划纯函数、预览令牌、全前态撤销）；扫描改动比 A 小（只在规划与 `persistTrack` 的 `medium_id` 处分流）。
- 缺点与风险：仍要处理 F-R4（一个候选的观察分到两个发行，`loadTrackPlans` 的“同一规划发行”约束要放开，`persist.go:57–59` 一带）；被分出的发行没有自己的候选，其扫描字段（标题、年份等）不再随扫描更新，只能靠修正层或从原候选复制；新增文件跟候选走，可能落回“错的”那一边。
- 范围内：合并、拆分、撤销；重扫后已确认的曲目不动。范围外：新增文件的智能归属、跨 root。

### 方案 C：先只做“合并分开的多碟/分目录发行”，并且只用强证据或人工

- 做法：只做合并方向，且只做“被并入方整体并入”（B 的全部曲目 → A，作为 A 的追加介质）。实现可选“发行重定向”（照 G 的艺人重定向）：B 记 `merged_into = A` 与碟位置偏移，扫描照常写 B、身份规则不变；读取时 A 的视图包含 B 的介质与曲目，B 不单独出现；撤销 = 删掉重定向并还原搬迁的人工状态。自动使用只在 `ObjectsLinkedTo("musicbrainz","release",X)` 给出 ≥2 个本地发行时由模型提出、审查（§5.3 里唯一有数据支撑的情形）。
- 优点：扫描零改动，重扫天然保持（扫描不碰重定向表，B 与 A 的 ID 由各自的成员关系保持）；撤销简单；覆盖 S1–S7、S10 这类最常见的“多碟被拆开”；强证据可直接核对。
- 缺点与风险：读取侧要在发行详情、摘要、搜索、统计、曲目位置、艺人投影、对比、问题、可见性等处叠加重定向（§8 第二行），并保证无重定向时逐项不变；B 被整张替换时重定向挂在旧 B 上，新 B 单独出现（与认领同类限制）；不解决 M1–M7（合错要拆）。
- 范围内：整体合并、撤销、强证据自动合并。范围外：拆分、部分曲目重挂、跨 root。

### 推荐的最小可行范围与顺序（推断）

1. **先定语义再写代码**：在 PRD 里定下“意图的单位”（逐曲目 / 按候选 / 整个发行重定向）、新增文件的归属规则、介质位置规则、被并入方的去向（空组？待认领？），以及每类发行级状态的冲突规则（照认领：目标已有的为准；自动层与外部对应是否搬）。
2. **第一步做合并、人工入口**：范围取方案 C 的“整体并入”（覆盖 §2.1 的多数情形）；实现路线在“重定向（扫描零改动、读取叠加）”与“方案 A/B 的扫描侧固定成员”之间二选一，选之前先做一次读取处清点（哪些查询直接连 `tracks → media → releases`），用清点结果比较两边的改动面。
3. **第二步接自动**：强证据只用“两个本地发行对应同一个 MB release”这一条（库里已有数据）；照强证据归组加 `manual_structure`、`no_strong_evidence`、不再提出标记；后台工具与审查策略第 4 版、例外表 `tool` 约束随之放开。
4. **拆分放到最后或另立**：现在没有拆分的强证据来源（§5.3），人工拆分需要曲目多选与新发行的扫描字段来源；可先只做人工拆分（方案 B 的“已确认归属”），自动拆分等有部分匹配能力后再议。
5. 验证重点：§2.3 的 F-R1–F-R5 每条写成真实重扫用例；与认领、归组、默认版本、核对、曲目修正、外部歌词的交错；无意图时既有接口样本逐字不变。

---

## Caveats / Not Found

- R1 的归档记录里**没有**技术评估文本，只有“单管理员、等真实需求”的裁决；§4.6 的“问题”是从 ADR 0002/0003 的条文归纳的，不是 R1 当时的原话。
- M6（CUE 整轨与分轨文件同目录共存）的结果是从代码推出的，仓库里没有找到覆盖它的测试。
- 未统计读取侧直接连接 `tracks → media → releases` 的查询数量；方案 C 与方案 A/B 的改动面比较需要这一步清点。
- 未运行任何测试；所有“现在的结果”来自代码与现有测试的断言，不是本次实测。
- 未查外部资料（MusicBrainz 对多碟、分碟发行的建模等）；强证据部分只看了本地已保存的数据形态。

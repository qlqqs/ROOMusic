# ② 文件夹名规则

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，O 线第 1 项。对照依据：[gap-analysis.md](../11-08-scan-reference-port/research/gap-analysis.md) G4、G5；参考设计 12.1、12.2、12.8、13.4–13.6、21.12、8.4 章。

## 目标

按碟分目录、碟目录带副标题或别的写法、专辑名里带碟号的专辑，都能归成一张多碟专辑；放在 `Scans/`、`Artwork/` 子目录里的封面也能找到。

## 现状

- 碟目录识别（`backend/internal/catalog/ingest/organizer.go:272-293`）：目录名小写后必须**以** `disc`/`cd`/`disk` 开头，接数字（>0），数字后除 `_-.()[]` 和空白外不能有别的字。认 `CD1`、`cd 01`、`Disc.2`、`DISK_3`、`CD(1)`；不认 `Disc One`、罗马数字、`Disc 1 - 副标题`、`CD1 Bonus`、`Disc 1 of 2`、`[Disc 1]`、`Album CD1`、纯数字。
- 分组只在 scope 内进行（`scope.go:9-22`）：识别出的碟目录归到 `disc-parent:<父目录>`，其余目录各自是 `directory:<目录>`。没认出来的碟目录会各成一张同名专辑。
- 严格多碟（`organizer.go:455-483`）要同时满足：至少 2 种碟号、各碟专辑与专辑艺人一致、每个碟号只有一个目录、1 到最大号齐全；不满足就每碟一张（`box_leaf`）。只有 `CD1` 一个碟目录时也是 `box_leaf`，没有专辑标签时专辑名就是 `CD1`（`backend/internal/library/parser/tags.go:138-142`）。
- 专辑名里的 `(Disc 2)`、`CD1` 不剥离；同一张专辑两碟的专辑名不同，严格多碟判定失败，拆成两张（`organizer.go:457`）。
- 文件夹封面只查发行目录与各曲目目录（`ingest.OrganizedCandidate.ArtworkDirectories`），名单 `cover.{jpg,jpeg,png,webp}`、`folder.{jpg,jpeg,png}`、`front.{jpg,jpeg,png}`，名字精确匹配（`backend/internal/library/scanner.go:989-1060`；合同 `library-scan-contracts.md:117-129`）。
- 样本库 `music/` 里有 `Disc 1`/`Disc 2`、`Scans`、`Artwork` 子目录。

## 需求

- **R1 碟号解析器**：一个纯函数，输入一个名字，输出碟号与去掉碟号标记后的剩余部分。规则以参考设计 12.2 为基础：
  - 标记 `disc`/`disk`/`cd` 可在名字任意位置，不区分大小写，可被括号包住（`[Disc 1]`、`(CD 2)`）；
  - 编号支持数字、英文数字词（`One`）、罗马数字（`II`）；
  - `of N` 这类总数写法（`Disc 1 of 2`）算标记的一部分；
  - 纯数字名字不算碟目录。
  - 中文写法（如 `第1碟`、`碟1`）不纳入：真实库没有，`第一张` 常见于专辑名（统计见 research/m8-name-stats.md）。
  - `[CD]`、`{NetEase, WEB, CD}`、`BACD-0001` 这类介质标记与唱片编号不认作碟号。
- **R2 碟目录识别与归组**：目录名用 R1 识别出碟号即视为碟目录，同一父目录下的碟目录进同一个 scope。合成一张多碟专辑的规则：
  - 带副标题的碟（`Disc 1 - 夏`、`Disc 2 - 冬`）合成一张，剩余部分作该碟的碟名，显示在“第 N 张”后面（现有介质标题字段）。
  - 附加碟（剩余部分是 `Bonus`、`Bonus CD`、`Extra`、`特典` 一类，或与正碟碟号重复的那一组）并入主专辑，排在正碟之后依次编号，碟名保留剩余部分。
  - 缺号（只有 CD1、CD3）时，各碟的专辑与专辑艺人标签一致（不含 inferred）就合成一张，缺的碟不补；没有标签时仍每碟一张。
  - 只有一个碟目录（`专辑/CD1/`）且父目录里没有直接放的音频时，归到父目录，无专辑标签时用父目录名作专辑兜底名。
  - 各碟标签明确冲突（不同的专辑或专辑艺人）时不合并，沿用现在的 `box_leaf`。
- **R3 专辑名碟号剥离**：专辑标签末尾或括号内的碟号标记（`X (Disc 2)`、`X CD1`、`X [Disc 1]`）用 R1 剥离后再参与分组与 Release 标题；剥离后为空则不剥。剥出的碟号在碟号标签缺失时作为证据，来源单独标记。
- **R4 附属目录封面**：查找文件夹封面时，在每个已查目录下再查名为 `scans`、`artwork`、`art`、`covers`（不区分大小写）的直接子目录，排在原目录之后。封面名在现有名单之后增加“去扩展名后包含 `front`”的规则（如 `00-front.jpg`、`Cover (Front).png`）。不加中文目录名（真实库只有 `Scans`、`Artwork`）。
- **R5 已有数据**：规则改变后的第一次扫描，Release 沿用仍按严格过半规则（`backend/internal/catalog/ingest/identity_plan.go:357-397`），曲目 UUID 不变。落选的旧 Release 变空时，按父任务的跨项裁决把其 ReleaseGroup 上的专辑收藏转到接收曲目最多的 Release 的 ReleaseGroup（已收藏的不重复），修正、认领、默认版本不搬；该逻辑放在候选写入路径上，对所有原因导致的合并都生效，③、⑥ 复用。
- **R6 合同同步**：更新 `library-scan-contracts.md` 中碟目录、多碟、封面发现的规则。

## 验收

- 单测：R1 的输入输出对照表（至少覆盖现状里列出的所有认与不认的写法、纯数字、`Disc 1 of 2`、`CD1 Bonus`）；R2、R3 的分组结果对照；R4 的子目录与 front 规则，以及优先级不变。
- 样本库 `music/` 扫描前后对比：`Disc 1`/`Disc 2` 仍是一张，`Scans`/`Artwork` 里的封面被用上（原来没有封面的专辑）。
- 真实库只读 smoke：列出前后合并或拆开的专辑清单，逐条人工确认。

## 不做

- 只要识别出碟号就上提、不做校验（参考设计 12.1 的做法）。
- 用 `艺人 - 专辑` 合成兜底名（folder_metadata 已覆盖，更准）。
- 祖先目录名参与归组。

## 已裁决

- 2026-11-08 主会话裁决（设计审阅）：所有新的碟目录合成规则（单碟归父、碟名、附加碟、缺号、带前缀）都要求父目录的整棵子树里除这些碟目录外没有别的含音频目录（独占）；不独占时保持现状（只有纯碟号目录按严格多碟合成，其余按 `box_leaf` 锚在碟目录本身）。避免专辑锚到艺人目录上拿错标题与封面。
- 2026-11-08 主会话裁决（设计审阅）：中文碟号写法与中文附属目录名不纳入（真实库没有；`第一张` 常见于专辑名，误认风险高）。
- 2026-11-08 用户裁决（R2）：带副标题的碟合成一张、副标题作碟名；附加碟并入主专辑排在后面；缺号时标签一致就合并；只有一个碟目录时归到父目录。
- 2026-11-08 主会话裁决（实现验收）：被认作碟目录的目录，取目录元数据时只解析碟号标记之前的部分，为空则不产生目录证据（`Disc 1 - 夏` 不再被拆成艺人 `Disc 1`，`周杰伦 - 叶惠美 CD1` 照旧得到艺人 `周杰伦`）。
- 2026-11-08 主会话裁决（合入 main 时）：附属目录与包含式封面查找也按 ① 的扫描过滤走，用户排除的文件夹、隐藏与系统文件不拿来当封面。
- 2026-11-08 主会话裁决（合入 main 时）：独占判断把本次扫描保住的来源（读取失败的文件或目录）和其他未知范围也算作“别的含音频目录”；父目录下有这类来源时不算独占，碟目录保持现状。

## 依赖

O 线第 1 项，无前置。③ 依赖本项的碟号解析器。

## 实施结果（2026-11-08）

提交：`679ea7f`（碟号解析与专辑名剥离碟号、碟目录合成、四轮封面查找含附属子目录、被腾空的旧专辑收藏转给接收组）、`19427e6`（封面查找按扫描过滤走；碟目录父级有读取失败的来源时不上提）、`b695a05`（这两条的回归用例）。随 O 线合并 `e042f2c` 入 main，迁移 `0037_scan_observation_previous_release`（O 线同步 main 时在 `d5b1b0c` 改号）。

裁决与偏离：上文“已裁决”中实现验收一条（碟目录只解析碟号前的部分取目录元数据）在 `679ea7f` 落实；合入 main 时两条（附属目录与包含式封面查找走 ① 的扫描过滤；独占判断把本次保住的来源算作别的目录）在 `19427e6` 落实、`b695a05` 补用例。

验收：

- 单测：满足。R1 `TestParseDiscMarker`、`TestParseDiscMarkerRejectsNonDiscNames`、`TestStripAlbumDiscMarker`（含纯数字、`Disc 1 of 2`、`CD1 Bonus`）；R2、R3 `TestOrganizeDiscDirectoryDecisionTable`、`TestOrganizeDiscNumberEvidencePriority`；R4 `TestFolderArtworkFourRoundDecisionTable`、`TestFolderArtworkRecognizesAllSubsidiaryNames`、`TestFolderArtworkHonorsScanFilter`；集成 `TestPostgreSQLDiscParentScopesAndMediumTitle`、`TestPostgreSQLDiscParentHeldSiblingPreventsPromotion`、`TestPostgreSQLDiscFavoriteTransfer`、`TestPostgreSQLMoveAlbumFavorites`。O 线合入前 ingest 215 项、library 48 项（含子测试）通过，0 SKIP。
- 样本库 music/ 前后对比：部分满足。`6ee547b`→`19427e6` 两版本均 68 发行/69 介质/284 曲目，碟目录合并、拆分与其他归组变化都是 0，原有多碟专辑仍是一张；封面两边都是 15 张（内嵌 7、目录 8），来源类型与内容哈希不变。本库没有因附属目录新增封面的专辑，“原来没封面的专辑用上附属目录封面”只由合成用例证明。
- 真实库只读 smoke：满足，合并或拆开的清单为空，无需逐条确认。本轮只在本机 music/ 上对比，两版本各自首扫→重扫 ID 全保留。

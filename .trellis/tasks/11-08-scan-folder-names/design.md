# ② 文件夹名规则：技术设计

依据：[prd.md](./prd.md)；父任务 [prd.md](../11-08-scan-reference-port/prd.md)（复刻原则、跨项裁决“落选 Release 的数据”）；
[gap-analysis.md](../11-08-scan-reference-port/research/gap-analysis.md) G4、G5；参考设计 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)
8.4（727–741）、12.1（1243–1273）、12.2（1274–1340）、12.8（1516–1533）、13.4–13.7（1610–1694）、21.12（2515–2567）。

## 1. 边界与归属

| 部分 | 归属 | 说明 |
| --- | --- | --- |
| 碟号解析器 `parseDiscMarker` | `catalog/ingest/discmarker.go`（新文件） | 纯函数，包内使用；③ 复用 |
| 碟目录识别、归组、介质标题、专辑名碟号剥离 | `catalog/ingest/organizer.go`、`scope.go` | O 线独占 `organizer.go` |
| 碟父目录独占标记 | `catalog/ingest/staging.go`（新函数 `MarkDiscParentScopes`）、`catalog/ingest/observation.go`（新字段 `DiscParentExclusive`）、`library/scanner.go`（调用一行） | 在 CUE 对齐之后 |
| 介质标题写入 | `catalog/ingest/persist.go` | `media.title` 列已存在（0002），不加迁移 |
| 封面子目录与 `front` 规则 | `catalog/ingest/persist.go`（`ArtworkDirectories` 拆两组）、`library/scanner.go`（`discoverFolderArtwork`） | |
| 落选专辑的收藏转移 | 计算在 `catalog/ingest`（新文件 `group_moves.go`），执行在 `personal`（新方法），由 `library` 在根写入完成后调用，`app` 注入 | 迁移 0034 |
| 合同 | `library-scan-contracts.md`、`personal-contracts.md`、`database-guidelines.md` | |

**不改** `library/parser/`（包括 `tags.go`）。专辑名碟号剥离放在 organizer 的比较键里，不改暂存的观察值，所以本线与 T 线在 `parser/` 没有冲突。

前端：介质小标题已支持“第 N 张 · 标题”（`frontend/src/features/catalog/format.ts:40` `mediumHeading`），本项**不需要前端改动**。

## 2. 数据流

```
遍历 → StageObservation（OrganizationScope：碟目录 → disc-parent:<父>，用新解析器）
     → AlignCueStagingScopes（不变）
     → MarkDiscParentScopes（新：父目录的子树里含音频的只有这些碟目录时，给该 scope 的暂存行打“独占”标记）
     → PlanStagedRoot（不变的身份规划；新：把每条沿用曲目“原来所在的 Release”写进 previous_release_id）
     → 逐 scope：OrganizeObservations（新：碟目录分组、介质标题、专辑名剥离）→ PersistCandidate（新：写介质标题）→ 封面（新：子目录与 front）
     → 新：本根写完后，EmptiedGroupMoves → personal.MoveAlbumFavorites（单个短事务）
     → finalizeScan（不变）
```

## 3. R1 碟号解析器

### 3.1 签名

```go
// discMarker 是名字里识别出的碟号标记：碟号，标记之前与之后的剩余文字（已去掉首尾空白、分隔符与落单的括号），
// 以及标记是否从名字开头开始。
type discMarker struct {
	Number         int
	Prefix, Suffix string
	AtStart        bool
}

// parseDiscMarker 在 name 里从左到右找第一个能取到碟号的 disc/disk/cd 标记；纯数字的名字不算。
func parseDiscMarker(name string) (discMarker, bool)

// stripAlbumDiscMarker 用同一解析器剥离专辑名里的碟号（21.12 的截断语义）；剥离后为空时原样返回、ok 为 false。
func stripAlbumDiscMarker(album string) (stripped string, disc int, ok bool)

// isBonusRemainder 判断剩余文字是不是附加碟一类（Bonus、Extra、特典……）。
func isBonusRemainder(text string) bool
```

### 3.2 规则

1. 名字（`strings.TrimSpace` 后）为空、或全是数字：失败（12.2 的“裸数字”开关在归组链路关闭）。
2. 名字不区分大小写地不含 `disc`、`disk`、`cd`：失败（12.2 的前置检查）。
3. 正则（Go RE2，`(?i)`）：

   ```
   (?:^|[^0-9A-Za-z_])            左边界：开头或非 ASCII 单词字符（与原文 \b 等价，中文字符算边界）
   ([(\[{<]\s*)?                  可选左括号
   (disc|disk|cd)                 标记
   [\s_.\-]*(?:\(\s*)?            连接：空白、下划线、点、连字符，可再带一个左括号（兼容现有 CD(1)）
   (\d{1,3}|[A-Za-z]+)            编号
   (\s*(?:of|/)\s*\d{1,3})?       可选总数（Disc 1 of 2、CD 1/2）
   (\s*\))?(\s*[)\]}>])?          可选右括号
   (?:$|[^0-9A-Za-z_])            右边界
   ```

   标记片段的起点是可选左括号（没有就是标记）的起点，终点是最后一个命中分组的终点；左右边界字符不属于片段。
4. 编号取值（12.2 三分支）：全数字 → 整数；全字母 → 先英文数词（`one`…`twenty`），再**规范**罗马数字（`i`…`xcix`，正则 `^(xc|xl|l?x{0,3})(ix|iv|v?i{0,3})$`，不认 `iiii`、`vx`）；其它不取。
5. 碟号必须在 1–99；否则这一处匹配作废，**继续向右找下一处**（改写，见对照表），都不行才失败。
6. 剩余文字：`Prefix = name[:片段起点]`，`Suffix = name[片段终点:]`；两者都去掉首尾空白与 `-_.,:;~·–—` 分隔符，再去掉首尾落单的括号（`(` `[` `{` `<` 及对应右括号）。
   `AtStart` = `Prefix` 为空。
7. **中文写法不纳入**（`第1碟`、`碟1`、`第一张`、`光盘1` 等都不认）：真实库没有这类碟目录，而 `第一张` 一类写法常出现在专辑名里（“××的第一张专辑”），误认风险高于收益（统计见 [research/m8-name-stats.md](./research/m8-name-stats.md)）。

`stripAlbumDiscMarker`：`AtStart` 时结果是 `Suffix`，否则结果是 `Prefix`（21.12：“匹配在开头只移除片段，否则从匹配处截断到结尾”）。结果为空则不剥。

`isBonusRemainder`：文字小写后，下列词出现在开头、或紧跟在空白/分隔符之后即为真（`CD Bonus`、`Disc 3 - Bonus Live` 都算）：`bonus`、`extra`、`special`、`特典`、`初回特典`、`附赠`、`附录`、`赠品`、`おまけ`（中文、日文词出现在任何位置都算）。

### 3.3 解析对照（单测表，覆盖 PRD 全部写法）

| 输入 | 结果 | 编号 | Prefix | Suffix | 附加碟 |
| --- | --- | --- | --- | --- | --- |
| `CD1` | 认 | 1 | | | |
| `cd 01` | 认 | 1 | | | |
| `Disc.2` | 认 | 2 | | | |
| `DISK_3` | 认 | 3 | | | |
| `CD(1)` | 认 | 1 | | | |
| `Disc One` | 认 | 1 | | | |
| `Disc II` | 认 | 2 | | | |
| `Disc 1 - 夏` | 认 | 1 | | `夏` | |
| `CD1 Bonus` | 认 | 1 | | `Bonus` | 是 |
| `CD 1 Bonus CD` | 认 | 1 | | `Bonus CD` | 是 |
| `Disc 3 - 特典` | 认 | 3 | | `特典` | 是 |
| `Disc 1 of 2` | 认 | 1 | | | |
| `[Disc 1]` | 认 | 1 | | | |
| `(CD 2)` | 认 | 2 | | | |
| `Album CD1` | 认 | 1 | `Album` | | |
| `Bonus CD2` | 认 | 2 | `Bonus` | | 是 |
| `专辑CD1` | 认 | 1 | `专辑` | | |
| `CD Bonus CD2` | 认（第一处取不到编号，继续向右） | 2 | `CD Bonus` | | 是 |
| `1`、`01`、`2046` | 不认（纯数字） | | | | |
| `Discography`、`Disco` | 不认（`ography`/`o` 不是数） | | | | |
| `CD Collection`、`The CD` | 不认 | | | | |
| `2CD`、`SACD 1`、`HDCD` | 不认（左边界是字母或数字） | | | | |
| `CD-R`、`CD-DA` | 不认 | | | | |
| `CD0`、`Disc 100` | 不认（不在 1–99） | | | | |
| `Disc Mix` | 不认（MIX 不是规范的 1–99 罗马数字） | | | | |
| `CD1A` | 不认（右边界是字母） | | | | |
| `X [CD][ABCD-12345][FLAC]` | 不认（`[CD]` 后面没有编号；`ABCD` 里的 `CD` 左边是字母） | | | | |
| `X {NetEase, WEB, CD}` | 不认（`CD` 后面是 `}`，没有编号） | | | | |
| `BACD-0001` | 不认（`CD` 左边是字母） | | | | |
| `第一张`、`第1碟`、`碟1` | 不认（中文写法不纳入） | | | | |

专辑名剥离对照（`stripAlbumDiscMarker`）：

| 专辑标签 | 剥离后 | 碟号 |
| --- | --- | --- |
| `X (Disc 2)` | `X` | 2 |
| `X CD1` | `X` | 1 |
| `X [Disc 1]` | `X` | 1 |
| `X - CD 2` | `X` | 2 |
| `X (Disc 1 of 2)`、`X (CD 1/2)` | `X` | 1 |
| `X (Deluxe) [CD2]` | `X (Deluxe)` | 2 |
| `X CD2 Live` | `X`（截断语义） | 2 |
| `Disc 2: Y` | `Y` | 2 |
| `CD1` | 不剥（剥后为空） | — |
| `Discovery`、`2CD Set` | 不剥 | — |

## 4. R2 碟目录识别与归组

### 4.1 识别与 scope

- `locateObservation`：目录最后一段用 `parseDiscMarker` 识别出碟号即为碟目录；`observationLocation` 增加 `marker discMarker`。旧的前缀判断删除。
- `OrganizationScope` 不变（碟目录 → `disc-parent:<父>`）。
- **父目录是不是“专辑目录”**：新的合成规则（单碟目录归父、带碟名或附加碟、缺号、带 Prefix）都会让候选锚在父目录 `P` 上：兜底标题取 `P` 的名字，文件夹封面先找 `P`。
  如果 `P` 其实是艺人目录（`艺人/某精选 CD1/` 旁边还有 `艺人/另一张专辑/`），就会拿艺人目录名当标题、拿艺人照片当封面。所以新规则只在 `P` **独占**时适用：
  `P` 的整棵子树里，含音频的目录只有这个 scope 的碟目录（`P` 自己没有直接放的音频，也没有别的含音频的子目录，递归算；只放图片的附属目录不暂存，自然不算）。
- **数据来源**：organizer 只看得到本 scope，兄弟目录在别的 scope，所以由暂存层判断。**新** `ingest.MarkDiscParentScopes(ctx, db, scanID, rootID)`，在 scanner 里放在 `AlignCueStagingScopes` **之后**（scope 已最终确定）：

  ```sql
  WITH parents AS (
    SELECT DISTINCT organization_scope AS scope, substring(organization_scope FROM 13) AS parent
    FROM scan_observations
    WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope LIKE 'disc-parent:_%'   -- 父目录非空
  ), exclusive AS (
    SELECT p.scope FROM parents p
    WHERE NOT EXISTS (
      SELECT 1 FROM scan_observations x
      WHERE x.scan_run_id=$1::uuid AND x.root_id=$2::uuid AND x.organization_scope<>p.scope
        AND left(x.relative_path, length(p.parent)+1) = p.parent || '/')        -- 不用 LIKE，免转义
  )
  UPDATE scan_observations o SET observation = o.observation || '{"disc_parent_exclusive":true}'::jsonb
  FROM exclusive e
  WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.organization_scope=e.scope
  ```

  `SourceObservation` 增加 `DiscParentExclusive bool \`json:"disc_parent_exclusive,omitempty"\``（`observation.go`，与 ④ 新增的两个字段按字母序合并）。暂存行每次扫描都由 `StageObservation` 整行覆盖，标记不会残留。
  规划（`assignCandidateKeys`）与写入读的是同一批暂存行，两次整理看到的标记一致，候选键一致。
  标记跟着目录结构变：之后在 `P` 下新增别的专辑目录，标记消失，候选种类与锚点随之变化，候选键也变，④ 的未变候选跳过不会把它误判为没变。
- 不再移动父目录直接音频的 scope（上一稿的 `AlignDiscParentScopes` 取消）：“`P` 下没有直接放的音频”已包含在独占条件里，organizer 不需要看到它们。

### 4.2 归组算法（替换 `organizer.go` 的 `strictParents` 段）

对每个父目录 `P`：

- **`P` 独占**（碟目录观察带 `DiscParentExclusive`）：按下面第 1–9 步。
- **`P` 不独占**，或 `P` 为空（注册根下直接的碟目录）：只对“名字就是碟号标记”（`Prefix`、`Suffix` 都为空，相当于现在认的 `CD1`、`Disc.2`、`CD(1)`）的碟目录做**现在的**严格多碟判断（≥2 种碟号、每个碟号一个目录、1..max 齐全、partition ≤1 种 → `strict_multidisc` 锚在 `P`），其余碟目录各自 `box_leaf`。分组结果与现在相同（只有以前没认出来的带 Prefix/Suffix 碟目录种类从 `ordinary_directory` 变为 `box_leaf`，锚点目录不变），新规则不在这里生效。
  其中“整个 `P` 只有 1 个碟目录”时，它是 `box_leaf` 锚在碟目录本身（锚点目录与现在相同；以前没认出来的名字如 `某精选 CD1` 种类从 `ordinary_directory` 变为 `box_leaf`，曲目全部留在原 Release，按严格过半照常沿用；封面只查它自己）；没有权威专辑标签、且 `Prefix` 非空时，标题兜底用 `Prefix`（`艺人/某精选 CD1/` → `某精选`，规则名 `disc_dir_prefix_fallback_v1`），`Prefix` 为空时保持现状（目录名）。
  选这一种而不是“锚在父目录”：锚在碟目录不碰艺人目录的名字和图片，候选键也不变。

`P` 独占时：

1. 收集 `P` 下的碟目录 `D`（每个有编号 `n`、`Prefix`、`Suffix`、附加碟标记 `bonus = isBonusRemainder(Suffix) || isBonusRemainder(Prefix)`）。
   **专辑标签**按 `compatibleAlbumPartition` 计（`albumPartition` 已用剥离后的专辑名，见第 5 节）。
2. **标签冲突**：`P` 下所有碟目录观察的非 `|` partition 多于 1 种 → 全部 `box_leaf`（现状）。
3. **名字分组**：非附加碟的 `Prefix`（规范化：小写、压空白）有两种及以上 → 全部 `box_leaf`（`Box/A CD1` 与 `Box/B CD1` 不合并，保持现状）。只有一种时，它作为兜底标题候选 `label`。
4. **正碟与附加碟**：
   - 正碟 = 非附加碟；若没有正碟（全是附加碟），全部当正碟。
   - 正碟编号互不重复 → 全部是正碟。
   - 编号重复时：`Suffix` 为空的正碟若存在且编号互不重复，它们是正碟，其余转为附加碟（PRD“与正碟碟号重复的那一组”，如 `CD1`、`CD2`、`CD1 (Live)`、`CD2 (Live)`）；否则全部 `box_leaf`。
5. **能否合成一张**（满足其一）：
   - 正碟编号是 1..max 连续（max ≥ 2），或正碟只有 1 个且 `P` 下还有附加碟——不要求有标签（与现在的严格多碟一致）；
   - 正碟编号有缺口（如 1、3，或 2、3）：要求**每个**碟目录都至少有一条权威专辑标签，且第 2 步只有 1 种 partition；
   - 整个 `P` 只有 1 个碟目录：不要求标签（独占已保证 `P` 下没有直接音频和别的专辑）。
   - 否则全部 `box_leaf`。
6. **合成后的介质**：正碟的介质位置 = 目录编号（缺号不补）；附加碟按（规范化 `Suffix` 或 `Prefix`、编号、目录路径）排序后从 `max(正碟编号)+1` 依次编号。
   介质标题：正碟 = `Suffix`；附加碟 = `Suffix`，为空时用 `Prefix`（`Bonus CD2` → `Bonus`）；都为空则无标题。
   合成候选里每首曲目的介质位置**一律按目录**定（标签碟号与目录冲突时目录胜；标签碟号缺失或 inferred 时把观察的 `DiscNumber` 改为介质位置、来源 `folder_structure`，与现状一致）。
7. **候选种类与锚点**：沿用 `strict_multidisc`，`Scope = P`，`Partition = ""`。不加新种类（前端与 `read.go` 的种类白名单不变）。
8. **兜底标题**：没有权威专辑标签时，`label` 非空用 `label`，否则用 `P` 的目录名（沿用 `multidisc_parent_fallback_v1`）。
9. **字段证据**：`folderDecisionScope` 保持“首个物理碟目录”（现有注释所述的 V0 约定，不在本项改）。

`P` 的直接音频（非碟目录）在自己的 `directory:<P>` scope 里照旧按 `ordinary_directory` 走同目录规则；有直接音频时 `P` 不独占，碟目录只走现在的严格多碟判断，两者种类不同，候选键不冲突（与现在相同）。

### 4.3 OrganizedCandidate 变化

```go
type OrganizedCandidate struct {
	// ……现有字段
	// MediumTitles 是介质位置到介质标题；没有标题的介质不出现。
	MediumTitles map[int]string
}
```

`PersistCandidate`：新建介质时 `INSERT INTO media(id,release_id,position,title)`；已有介质 `UPDATE media SET title=$1 WHERE id=$2 AND title IS DISTINCT FROM $1`。没有标题的介质写 `'Medium'`（列默认值，前端视为没有标题），这样标题去掉后下次扫描会恢复。目前没有任何人工修改介质标题的入口，扫描独占这一列。

## 5. R3 专辑名碟号剥离

- 只改比较键，不改观察值：`albumPartition` 的专辑部分改用 `stripAlbumDiscMarker(o.Album)` 的结果；Release 标题决定（`chooseValue(..., "title", getter)`）的 getter 改为同一剥离结果。
  `scope.go` 的 `root-album` 摘要通过 `albumEvidence → albumPartition` 自动使用剥离后的值，根目录下 `X (Disc 1)`、`X (Disc 2)` 因而进同一个 scope。
- 暂存与 `track_observations` 里的 `album` 仍是标签原文（证据保真）。
- **碟号证据**：organizer 入口预处理时，物理观察的碟号是 inferred（标签缺失）且剥离出了碟号 → `DiscNumber = 剥出的碟号`，`FieldSources["disc_number"] = "album_title"`，仍标 inferred。
  非合成候选的碟号优先级：标签 > CUE > `album_title` > 碟目录（`folder_structure`）> 默认 1；合成候选按第 4.2 节第 6 步由目录定。③ 在 `album_title` 与碟目录之后再加文件名一级（见 ③ 设计）。
- 性能：`albumPartition` 在一次整理里对每条观察调用常数次，正则只作用于短字符串；实现可在 `OrganizeObservations` 入口按观察下标缓存剥离结果，不改函数签名。

## 6. R4 附属目录封面

- `ArtworkDirectories()` 保持返回原目录顺序（发行目录 → 各曲目目录）。新增 `ArtworkSubdirectoryNames = {"scans","artwork","art","covers"}`（小写比较；不加中文名，真实库没有，见 [research/m8-name-stats.md](./research/m8-name-stats.md)）。
- `discoverFolderArtwork` 的查找顺序改为四轮，**前一轮命中即返回**：
  1. 原目录 × 现有名单（与现在完全相同）；
  2. 原目录 × “去扩展名后小写包含 `front`”且扩展名是 `jpg/jpeg/png/webp` 的文件，同目录内按文件名字节序；
  3. 原目录的附属子目录（每个原目录下名字命中的直接子目录，按原目录顺序、子目录名字节序）× 现有名单；
  4. 附属子目录 × 包含 `front` 的规则。
- 这样凡是现在能找到封面的发行，结果不变（第 1 轮与现在相同）；只有现在找不到封面的发行会多找到一张。安全检查、错误记录（第一个错误、全部找完才报）不变。
- 附属子目录通过 `os.ReadDir` 的结果按名字小写匹配取得，必须是目录且不是符号链接（`DirEntry.Type()`）；不递归。

## 7. R5 落选 Release 的专辑收藏转移

### 7.1 规则

本根所有候选写完后执行一次（`library` 调用，独立短事务）：

1. 本次扫描里被沿用（`planned_track_id` 非空）且现在所在 Release 与规划时不同的曲目，按（原 Release，新 Release）计数。
2. 原 Release 所在的组如果**已经没有任何一条曲目**（组内所有 Release 的介质下都没有曲目），这个组被腾空。组里还有别的版本有曲目时不转移：专辑还在，收藏仍然有效。
3. 被腾空的组 `G` 的接收方：从 `G` 移出曲目最多的新 Release；平局按新 Release 的 `created_at`、再按 ID 升序。转到它所在的组 `T`。
4. `favorite_albums` 中 `group_id = G` 的行改到 `T`：保留 `created_at`、`saved_title`、`saved_artist`；同一用户已收藏 `T` 时只删掉 `G` 那一行（不重复）。受影响用户的 `personal_favorite_state.revision` 各加一。
5. 修正、认领、默认版本、听歌历史、歌单、曲目收藏都不动（父任务裁决）。

这条规则只看事实（曲目实际从哪里移到了哪里），所以 ② 的碟目录合并、③ 的规则变化、⑥ 的归组变化和用户自己整理目录导致的合并都一样生效。

### 7.2 实现

- **迁移 0034**（`0034_scan_observation_previous_release.sql` / `.down.sql`）：

  ```sql
  ALTER TABLE scan_observations ADD COLUMN previous_release_id UUID;
  -- down
  ALTER TABLE scan_observations DROP COLUMN IF EXISTS previous_release_id;
  ```

  暂存表，不加外键、不加索引（按 `scan_run_id, root_id` 已有索引过滤）。合入时由主会话重新编号。
- `StageObservation` 的 `ON CONFLICT DO UPDATE` 增加 `previous_release_id=NULL`。
- `PlanStagedRoot` 在 `allocateReleases` 之后增加一条：

  ```sql
  UPDATE scan_observations o SET previous_release_id=m.release_id
  FROM tracks t JOIN media m ON m.id=t.medium_id
  WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND NOT o.withheld AND o.planned_track_id=t.id
  ```

- `catalog/ingest/group_moves.go`：

  ```go
  // EmptiedGroupMoves 返回本次扫描该根里被腾空的 ReleaseGroup 到接收曲目最多的 Release 所在组的映射（from → to）。
  // 只读；必须在本根所有候选写入之后调用。
  func EmptiedGroupMoves(ctx context.Context, database queryer, scanID, rootID string) (map[string]string, error)
  ```

  一条 SQL：`moved`（`scan_observations` ⋈ `tracks` ⋈ `media`，`m.release_id <> o.previous_release_id`，按原、新 Release 计数）→ 原组 → 过滤“组内没有任何曲目” → `DISTINCT ON (原组)` 按计数降序、新 Release `created_at`、ID 取接收方。
- `personal`：

  ```go
  // MoveAlbumFavorites 在调用方事务里把 from 组上的专辑收藏转到 to 组；已收藏 to 的用户只删除 from 那一行。
  // 受影响用户按 user_id 升序锁收藏版本行并各加一。返回转移或合并的收藏行数。
  func (s *Service) MoveAlbumFavorites(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error)
  ```

  顺序：`SELECT DISTINCT user_id FROM favorite_albums WHERE group_id = ANY(from)` → `SELECT … FROM personal_favorite_state WHERE user_id = ANY($1) ORDER BY user_id FOR UPDATE` →
  `INSERT … SELECT … ON CONFLICT (user_id,group_id) DO NOTHING` → `DELETE … WHERE group_id = ANY(from)` → `UPDATE personal_favorite_state SET revision = revision + 1 WHERE user_id = ANY($1)`。
  只访问个人收藏表，不读 catalog 私表（分组映射由调用方给出），符合 personal 合同的数据权威。
- `library`：

  ```go
  // AlbumFavoriteMover 在调用方事务里转移专辑收藏，由 app 注入 personal 的实现；nil 时不转移。
  type AlbumFavoriteMover func(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error)
  func (service *Service) SetAlbumFavoriteMover(mover AlbumFavoriteMover)
  ```

  `scanRoot` 在 `persistStagedCandidates` 之后（`planned` 为真、未取消）调用 `transferEmptiedAlbumFavorites`：开事务 → `ingest.LockProjectionWrites` → `EmptiedGroupMoves` → 映射为空则提交返回 → `mover` → 提交。
  失败只记 `Warn` 日志 `library.scan.favorite_transfer_failed`（只含计数与扫描 ID），**不改变扫描完整性**：转移是附带动作，曲目与发行已经写好。
  锁序 CATP → 收藏版本行，与 personal 现有写入（CATP → 本人 state）一致。
- `app`：`personalService := personal.NewService(database)`，`libraryService.SetAlbumFavoriteMover(personalService.MoveAlbumFavorites)`。依赖方向不变（`library` 不引用 `personal`），`TestPackageDependencyDirections` 不需要改。

已知限制：扫描在本根写完、转移之前被取消时，这次的转移不会补做（下次扫描曲目已在新位置，不再有“移动”）。收藏会显示为“暂时不可用的专辑”（保存的标题仍在），用户可以重新收藏。不为此加持久化补偿（回滚成本低，不过度兜底）。

## 8. 接口合同汇总

| 项 | 变化 |
| --- | --- |
| 迁移 | 暂定 0034：`scan_observations.previous_release_id UUID`（含 down）。S 线 ①⑫④⑪ 也暂定从 0034 起，合入时由主会话按合入顺序重新编号 |
| REST 读取 | 形状不变。发行详情 `media[].title` 会出现真实碟名（`夏`、`Bonus`），没有碟名仍是 `"Medium"`；接口样本不受影响（合成素材没有碟名目录） |
| 候选种类 | 不新增；更多情形落到 `strict_multidisc` |
| 收藏 | 扫描可能改变用户的专辑收藏集合并递增其 `revision`；客户端持旧 revision 分页会得到既有的 `409 favorites_changed` |
| Go 接口 | `ingest.MarkDiscParentScopes`、`SourceObservation.DiscParentExclusive`、`ingest.EmptiedGroupMoves`、`OrganizedCandidate.MediumTitles`、`personal.(*Service).MoveAlbumFavorites`、`library.AlbumFavoriteMover` / `SetAlbumFavoriteMover` |
| 版本号 | 规则只在 organizer、暂存标记、封面发现与写入路径上变化，**不改解析结果**：只升 ④ 的 `ingest.GroupingVersion`，`parser.CommonVersion` 与各格式版本不升（与 ④ 设计第 8 节对照表一致）。② 与 ③ 在 O 线一起合入 `main` 时，`GroupingVersion` 合并为加 1 次。两种合入顺序：**④ 先合入**——O 线 rebase 到 `main` 后在 `ingest/version.go` 把 `GroupingVersion` 加 1，并把 ④ 候选签名里的封面行（`"a"`）改为覆盖 ② 的四轮查找范围（附属子目录与包含 `front` 的文件），否则在 `Scans/` 里新放的封面不会触发重写；**O 线先合入**——此时还没有版本常量，现状每次扫描都重写全部候选、重新发现封面，下一次扫描即生效；④ 合入时以 O 线规则为基线（`GroupingVersion = 1` 不另加），由 S 线让签名的封面行覆盖 ② 的查找范围 |
| 证据规则名 | `release_grouping_evidence.rule_id` 保持 `organizer_v2`，候选键前缀保持 `v2`（锚点只作定位与平局，换前缀会让一次扫描丢掉“留在原位置”的平局优先） |

## 9. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 12.2 标题正则 `(?:[(<\[{]\|\b)(?i:disc\|disk\|cd)[\s-]*([\w\-]+)(?:[)>\]}]\|\b)`（1282） | 改写 | 保留左右边界、四种括号、标记与“空白/连字符连接”；连接符增加 `_`、`.`、`(`（否则 `DISK_3`、`Disc.2`、`CD(1)` 这些现在认的写法会退化）；捕获从 `[\w\-]+` 收窄为“纯数字或纯字母”（原文三分支里混排本来就取不到编号，收窄只是让回溯后的右边界判断正确）；增加可选 `of N`/`/N`（PRD R1 要求） |
| 12.2 “名字里不含 disc/disk/cd 直接失败”（1289） | 照搬 | |
| 12.2 编号三分支：数字 / 英文数词 / 罗马数字（1293–1300） | 改写 | 罗马数字只认规范写法且限 1–99，英文数词限 one–twenty；碟号限 1–99。原文对 `Mix` 这类词会解析出 1009，限定后误判消失 |
| 12.2 只取第一处匹配（1290–1291） | 改写 | 第一处取不到编号时继续向右找（`CD Bonus CD2`）；原文直接失败，改写不影响原文能认的写法 |
| 12.2 余量：开头只删片段，否则截断到结尾（1302–1304） | 照搬（专辑名）/ 改写（目录名） | 专辑名剥离照搬截断语义（21.12）；目录名同时保留 Prefix 与 Suffix，因为 ROOMusic 用 Suffix 作碟名、用 Prefix 判断是不是另一张专辑，原文截断会丢掉 `Album CD1 Bonus` 的 `Bonus` |
| 12.2 余量只“除空白”（1303–1304） | 改写 | 额外去掉首尾分隔符与落单括号，否则 `Disc 1 - 夏` 的碟名会是 `- 夏` |
| 12.2 / 13.4 “裸数字”开关在归组链路关闭（1286、1619） | 照搬 | `1/`、`2/` 不当碟目录 |
| 12.1 规则一：识别出碟号就上提到祖父目录（1256–1259） | 改写 | PRD“不做”：不做无校验上提。ROOMusic 只把同一父目录下的碟目录放进同一 scope，再按父目录独占、标签一致、编号连续/缺号需标签、正碟附加碟规则决定是否合成；不满足的保持现状 |
| 12.1 余量非空时归到 `祖父/余量`（1259、13.5 1633–1634、1644） | 改写 | `CD 1 Bonus` 不另成 `专/Bonus` 一组，而是按 PRD 裁决并入主专辑排在正碟后；`Album CD1` 的 Prefix 作名字分组与兜底标题，不造虚拟目录 |
| 13.5 实例表（1627–1638） | 改写 | `CD1`/`Disc 2`/`Cd3` → 一张（同）；`CD 1 Bonus` → 并入主专辑第 max+1 张（不同，PRD 裁决）；`Album CD1` → 单独一个且父目录独占时归父目录、标题兜底 `Album`，不独占时锚在自己、标题兜底 `Album`；多个不同 Prefix 时各自 `box_leaf`；`Scans/封.jpg` → 只用于封面查找（同效果）；库根文件 → 不归一组（不复刻） |
| 12.1 规则二：附属目录 `art/artwork/scans/extras` 上提（1261–1265） | 改写 | 只用于封面查找；名单按 PRD 用 `scans/artwork/art/covers`，不含 `extras`（`extras` 常放音频附加内容，不是图片目录）；不加中文名（真实库没有） |
| 12.8 附属文件按归组目录绑定（1516–1533） | 改写 | ROOMusic 不绑定 PDF/扫描图等附属文件，只取一张封面；查找范围是发行目录与曲目目录及其附属子目录 |
| 8.4 封面判定：基名等于 `cover`/`folder` 或包含 `front`（727–741） | 改写 | 现有名单与优先级保持在前（含 `front.*` 精确名）；“包含 front”作为后两轮，保证现在已有封面的发行结果不变 |
| 13.6 兜底名 `段[^2] - 段[^1]`（1650–1673） | 不采用 | PRD“不做”；folder_metadata 更准。单碟目录与合成候选兜底用 Prefix 或父目录名 |
| 13.5 库根文件全部归一组（1638、1648） | 不采用 | 父任务“不复刻”；根目录碟目录也不适用“单碟目录归父” |
| 21.12 专辑名触发条件与同一正则（2517–2523） | 照搬 | 共用 `parseDiscMarker` |
| 21.12 改写两分支“砍尾”（2536–2541） | 照搬 | |
| 21.12 只在取到碟号时改写，碟号只在碟号字段为空时填入（2543–2544） | 照搬 | 来源标 `album_title`，仍是 inferred |
| 21.12 不开启纯数字分支（2545） | 照搬 | |
| 13.7 名字比较 NFD + 自动大小写（1674–1694） | 不采用 | 父任务“不复刻”（大小写自动探测）；解析器本身不区分大小写 |

## 10. 规则对照表（输入 → 期望分组与编号）

目录 `A/` 下（“有标签”指专辑与专辑艺人标签一致且非 inferred）：

| 输入 | 期望 |
| --- | --- |
| `CD1/`、`CD2/`（有或无标签） | 一张，`strict_multidisc`，介质 1、2 |
| `Disc 1/`、`Disc 2/`（样本库 `【推しの子】` OST） | 一张（与现在相同） |
| `Disc 1 - 夏/`、`Disc 2 - 冬/` | 一张，介质 1 `夏`、2 `冬` |
| `Disc One/`、`Disc Two/` | 一张，介质 1、2 |
| `[Disc 1]/`、`[Disc 2]/` | 一张 |
| `Disc 1 of 2/`、`Disc 2 of 2/` | 一张 |
| `CD1/`、`CD2/`、`CD1 Bonus/` | 一张，介质 1、2、3 `Bonus` |
| `CD1/`、`CD2/`、`Bonus CD1/` | 一张，介质 1、2、3 `Bonus` |
| `CD1/`、`CD2/`、`CD1 (Live)/`、`CD2 (Live)/` | 一张，介质 1、2、3 `Live`、4 `Live` |
| `Disc 1 - 夏/`、`Disc 2 - 冬/`、`Disc 1 - 春/` | 各自 `box_leaf`（编号重复且没有无碟名的正碟） |
| `CD1/`、`CD3/`，有标签 | 一张，介质 1、3（不补 2） |
| `CD1/`、`CD3/`，无标签或只有部分目录有标签 | 各自 `box_leaf` |
| `CD2/`、`CD3/`，有标签 | 一张，介质 2、3 |
| `CD1/` 一个，`A/` 下没有别的音频（可以有 `Scans/`），无标签 | 一张，锚在 `A`，标题兜底 `A`，介质 1 |
| `CD1/` 一个，`A/` 下还有直接音频 | `A` 的直接音频一张 + `CD1` 一张 `box_leaf`（现状） |
| `艺人/某精选 CD1/` 一个，旁边还有 `艺人/另一张专辑/`（含音频），无标签 | `某精选 CD1` 一张 `box_leaf`，锚在碟目录本身，标题兜底 `某精选`；封面只查 `某精选 CD1/`，不查 `艺人/` |
| `艺人/某精选 CD1/` 一个，`艺人/` 下另有 `某精选 CD1/附录/`（含音频） | 同上（递归算，`P` 不独占） |
| `艺人/Disc 1 - 夏/`、`艺人/Disc 2 - 冬/`，旁边还有 `艺人/另一张专辑/` | 各自 `box_leaf`（不独占，新规则不生效；带碟名的目录不参与现在的严格多碟） |
| `A/CD1/`、`A/CD2/`，`A/` 下另有含音频的 `A/Bonus/` | `CD1`+`CD2` 一张 `strict_multidisc`（现在的规则），`Bonus/` 照旧一张 |
| `A CD1/` 一个，`A/` 下没有别的音频，无标签 | 一张，锚在 `A`，标题兜底 `A`（Prefix） |
| `CD1/`、`CD2/`，两碟专辑标签不同 | 各自 `box_leaf`（现状） |
| `CD1/` 标 `X (Disc 1)`、`CD2/` 标 `X (Disc 2)` | 一张，标题 `X`，介质 1、2 |
| `X CD1/`、`X CD2/`、`Y CD1/`（`Box/` 下） | 各自 `box_leaf`（Prefix 不止一种） |
| `1/`、`2/` | 各自 `ordinary_directory`（不是碟目录） |
| `Scans/`、`Artwork/` 只有图片 | 不产生候选；只参与封面查找 |
| 注册根下直接 `CD1/`、`CD2/` | 与现在相同（父目录为根，只走现在的严格多碟） |
| 根目录 `X (Disc 1).flac`、`X (Disc 2).flac`（都标 album） | 同一 `root-album` scope，一张 `loose_album`，碟号来自专辑名 |

曲目碟号（非合成候选）：标签 `2` → 2；标签缺失、专辑名 `X (Disc 2)` → 2（`album_title`）；标签缺失、在 `CD3/`（box_leaf）→ 3（`folder_structure`）；都没有 → 1（inferred）。

封面（发行目录 `A`，曲目目录 `A/CD1`）：

| 文件 | 结果 |
| --- | --- |
| `A/cover.jpg` 与 `A/Scans/front.jpg` | `A/cover.jpg`（第 1 轮，与现在相同） |
| `A/CD1/folder.png` 与 `A/Scans/cover.jpg` | `A/CD1/folder.png`（第 1 轮） |
| 只有 `A/00-front.jpg` | 用它（第 2 轮） |
| 只有 `A/Scans/Cover.JPG` | 用它（第 3 轮） |
| 只有 `A/Artwork/Box - Front.png`、`Inlay - Front.png` | `Box - Front.png`（第 4 轮，字节序） |
| `A/Scans/back.jpg` 只有它 | 没有封面 |
| `A/Scans` 是符号链接 | 不查 |
| `A/Scans/front.jpg` 损坏，`A/CD1/Scans/cover.jpg` 可用 | 用后者，不报错 |

## 11. 八号机统计（已执行）

已执行，聚合结果与结论见 [research/m8-name-stats.md](./research/m8-name-stats.md)：中文碟号写法不纳入（第 3.2 节第 7 条）；附属子目录名单不加中文名（第 6 节）；`[CD]`、`{…, CD}`、`BACD-0001` 三种写法加进解析对照表的“不认”（第 3.3 节）。

## 12. 兼容与已有数据

- 第一次按新规则扫描时，曲目 UUID 不变（身份规划不变）；Release 沿用仍按严格过半（`identity_plan.go` `allocateReleases`）。
- 典型变化：原来两个 `box_leaf`（`Disc 1 - 夏` 10 首、`Disc 2 - 冬` 10 首）合成一个 20 首候选，与任一旧 Release 重叠都只有一半、不过半 → 新建 Release 与组；两个旧 Release 都被腾空，它们组上的专辑收藏转到新组。
  原来严格多碟 20 首 + `CD1 Bonus` 5 首 → 25 首候选与旧多碟 Release 重叠 20，过半 → 沿用；`Bonus` 的旧 Release 被腾空，收藏转到沿用的那张。
- 被腾空的旧 Release 与组保留（发行与组从不删除），在读取侧隐藏（没有 present 曲目）；它们上面的修正、认领、默认版本不搬。
- 介质标题：第一次扫描把识别出的碟名写进 `media.title`，其余介质保持 `'Medium'`。
- 封面：只有现在没有文件夹封面的发行可能多出封面；新绑定会出现在扫描反馈的封面变化里。
- 收藏转移会递增受影响用户的收藏版本；正在翻页的收藏列表下一次请求得到 `409 favorites_changed`，前端已有处理。

## 13. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| 解析器误认专辑名（`CD I Love` 之类） | 碟号 1–99、规范罗马数字、边界要求；剥离只影响比较键与 Release 标题，原文仍在观察里；真实库 smoke 列出全部剥离过的标题人工核对 |
| 合成规则把两张不同专辑合成一张 | 标签冲突、Prefix 不一、编号重复无主碟、缺号无标签都不合；真实库 smoke 列出前后合并/拆开清单逐条确认 |
| 合成导致新建 Release，旧 Release 上的修正、认领、默认版本“丢失” | 父任务已裁决不搬；smoke 清单标出带修正或默认版本的落选 Release，供用户知情 |
| 父目录是艺人目录时合成候选锚在艺人目录上 | 新规则只在父目录独占时生效，否则保持现状，单碟目录锚在自己 |
| 独占标记的 SQL 成本 | 只对 `disc-parent:` scope 逐个查一次“父目录下有没有别的暂存行”，按 `(scan_run_id, root_id)` 过滤；样本库与真实库只有个位数碟父目录 |
| 收藏转移写错用户数据 | 只按事实（组被腾空）触发；一条 SQL 计算 + 一个短事务；集成测试覆盖转移、已收藏不重复、未腾空不转 |
| `extras` 不在附属名单 | 真实库没有这种目录；以后出现再加一个名字 |

回滚：代码按提交回退即可恢复旧归组；下一次扫描按旧规则重新归组（同样按严格过半沿用）。已经转移的收藏不回退（转移后的组有曲目，回退后旧组也可能再次有曲目，用户可手动调整）；迁移 0034 只加暂存列，down 迁移删列，无数据损失。

## 14. 已定稿的技术选择

没有待用户裁决的产品问题。中文碟号写法与中文附属目录名不纳入（第 11 节统计）；`extras` 不纳入附属目录名单；名字分组（Prefix 不同）时不合并；新合成规则只在父目录独占时生效，单碟目录不独占时锚在碟目录本身并用 Prefix 兜底标题。

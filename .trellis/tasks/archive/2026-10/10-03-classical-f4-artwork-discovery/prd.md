# F4：后端封面发现缺口

> 状态：已实施并验证（2026-10-03 创建并实施，代码提交 `b66f569`）。父任务：[Classical 重设计 / Core 1](../../../09-27-classical-core-1/prd.md)。
> 来源：M1b 真实接口冒烟发现、记入[运行合同技术债](../../../../spec/backend/core0-runtime-contracts.md)的两处封面发现缺口；用户 2026-10-03 要求先解决。
> 规划时用真实资料库的只读聚合统计又发现了第三处（文件名大小写），一并纳入。
> 只改后端扫描；技术设计见 [design.md](design.md)，执行顺序与验证命令见 [implement.md](implement.md)。

## 目标与用户价值

扫描后，以下专辑都能拿到文件夹封面，专辑页不再如实显示“没有封面”：CUE 专辑、封面放在父目录的多碟专辑，以及封面文件名不是全小写的专辑。
在用户的真实资料库里，第三类数量最多。

## 已核实事实（2026-10-03）

**现在的查找方式**

- 每个候选写入后调用 `persistOneCandidate`（`backend/internal/library/scanner.go:776`），它取 `OrganizedCandidate.ArtworkObservation()`
  （`backend/internal/catalog/persist.go:163`）。这个方法返回第一首带内嵌封面的曲目；如果都没有，就按碟序返回第一首曲目。
  随后 `saveArtworkForRelease`（`scanner.go:822`）在这一首曲目的目录里找封面，目录由 `filepath.Dir(RelativePath)` 求得。
- `discoverFolderArtworkWithReader`（`scanner.go:846`）按固定顺序逐个 `Lstat` 10 个全小写文件名：
  `cover.{jpg,jpeg,png,webp}`、`folder.{jpg,jpeg,png}`、`front.{jpg,jpeg,png}`。符号链接、非普通文件、空文件、超过 16 MiB 或格式无法识别的文件
  都会记下错误，然后继续找下一个名字。全部找不到时，如果途中有过错误，就返回这个错误；扫描因此 `incomplete`（`artwork_failure`），原有封面关系保留。
- 每次扫描都会对全部候选重新执行这一步（`scanner.go:626`）。所以修复后，下一次扫描就会补上封面，不需要迁移。

**缺口 1：CUE 专辑**

CUE 虚拟曲目的 `RelativePath` 形如 `album.cue#track-1@album/image.flac#index-0`（`scanner.go:517`）。对它求 `filepath.Dir` 得到的目录并不存在。
观察本身已有正确的 `Directory`，就是 sheet 所在的目录（`scanner.go:520`）。CUE 父文件的内嵌封面已经由 organizer 转给第一条虚拟轨
（`backend/internal/catalog/organizer.go:813`），这部分不是缺口。

**缺口 2：多碟专辑**

严格多碟候选的发行目录是父目录（`organizer.go:453`，即 `Anchor.Scope`，写入 `releases.relative_directory`），但封面只在第一张碟的目录里找。
Box 叶子目录（`organizer.go:455`）的发行目录就是它自己。

**缺口 3：文件名大小写**

Linux 文件系统区分大小写（`music/` 在 ext 文件系统上），所以 `Cover.jpg`、`Folder.JPG` 找不到。

**真实资料库统计**

2026-10-03 做了一次只读聚合统计：只读文件名，不读文件内容，只输出计数。

- 65 个含音频的目录中，13 个有命名封面。其中 7 个是全小写，6 个只有大小写不同的名字，后者里有 5 个是 CUE 目录。
- 8 个 CUE 目录中，1 个的封面是全小写，5 个只有大小写不同的名字。
- 唯一的一张多碟专辑，封面在父目录，而且名字不是全小写。
- 52 个目录没有任何图片，只能靠内嵌封面。没有哪个目录只有“非命名图片”（例如 `scan.jpg`）。

也就是说，只修缺口 1、2，真实库里只有 1 张 CUE 专辑会多出封面；加上缺口 3，最多约 8 张专辑会多出封面（已有内嵌封面的不受影响）。

## 需求

- **R1 查找目录**：文件夹封面先在发行目录（`Anchor.Scope`）里找，再按碟序、曲序在各曲目的目录里找，重复的目录只找一次。
  曲目的目录取观察的 `Directory` 字段，不再从 `RelativePath` 推导。查找范围不超出发行本身：
  - 普通专辑的两个目录相同，行为不变。
  - CUE 专辑在 sheet 所在的目录里找。
  - 严格多碟专辑先找父目录，再依次找 CD1、CD2……
  - Box 叶子目录不去找盒子的父目录。
  - 根目录下的散曲仍只找根目录。
  - CUE 跨目录引用的音频所在目录不在查找范围内。
- **R2 文件名不区分大小写**：名字表和优先顺序都不变（cover → folder → front，扩展名顺序也不变），比较时不区分大小写。
  同一目录里有几个文件只差大小写时，全小写的优先，其余按字节序取，保证结果确定。
- **R3 内嵌优先**：任何曲目的内嵌封面仍优先于文件夹封面，选择规则不变。
- **R4 失败语义不变**：
  - 不安全或无效的命名文件（符号链接、非普通文件、空文件、超过 16 MiB、格式无法识别）一律跳过，继续往下找。
  - 后面的目录里找到可用封面就用它。
  - 所有目录都没有可用封面时，只要途中遇到过上述问题，或某个目录读取失败（目录不存在除外），就返回错误：扫描 `incomplete`（`artwork_failure`），原有封面关系保留，不当作“没有封面”。
  - 目录不存在，视为这个目录里没有封面。
- **R5 文档**：
  - 运行合同的封面规则补上查找目录、顺序和大小写规则，错误矩阵与必需测试同步更新，技术债里的两条划掉并注明由 F4 修复。
  - 真实库统计记在 `research/real-library-artwork-layout.md`，只写计数。
  - 父任务 PRD 的任务表加上 F4。

## 验收标准

| # | 标准 | 对应 |
| --- | --- | --- |
| A1 | 整轨 FLAC 加 CUE、目录里有 `cover.png`、音频没有内嵌封面：扫描后该发行版本有 `source_type='folder'` 的封面，字节与 `cover.png` 相同 | R1 |
| A2 | 严格多碟专辑，`CD1/`、`CD2/` 里没有封面，父目录有 `Cover.JPG`：扫描后用的是父目录的封面 | R1、R2 |
| A3 | 父目录与 `CD1/` 都有封面时用父目录的；只有 `CD2/` 有封面时用 `CD2/` 的 | R1 |
| A4 | `Cover.JPG`、`FOLDER.png` 这类大小写变体能被发现；`Cover.jpg` 与 `cover.jpg` 同时存在时取 `cover.jpg`；大小写不影响 cover → folder → front 的优先顺序 | R2 |
| A5 | Box 叶子目录不取父目录的封面；普通专辑与根目录散曲的结果不变（已有测试的期望不改） | R1 |
| A6 | 有内嵌封面时仍用内嵌封面 | R3 |
| A7 | 原有三个失败测试（符号链接、无效文件、读取失败）仍然通过。新增两个用例：前一个目录的命名文件无效、后一个目录有效时，用后一个；所有目录都只有无效文件时，返回错误 | R4 |
| A8 | `gofmt -l`、`go vet ./...`、`go build ./...` 通过；`go test -race ./internal/library/... ./internal/catalog/... -count=1` 通过；`./scripts/test-integration.sh` 通过，API 样本没有变化 | 全部 |
| A9 | 运行合同、技术债、research 文档与父任务表已更新；文档中的相对链接都能解析 | R5 |

## 不做

- 新增文件名（如 `albumart.jpg`、`scans/` 子目录）：真实库里没有只有“非命名图片”的目录。
- 调整内嵌封面与文件夹封面之间的优先级，或补齐名字表里缺的 `folder.webp`、`front.webp`。
- 缩略图、封面尺寸限制与前端改动：页面已经能正确显示封面。
- 修正 `candidateSourceDirectories`（抓轨日志用）在根目录 CUE 跨目录引用时的路径推导：它只会多查一个不存在的目录，没有可见影响。
- 在用户自己的 `roomusic` 环境里重扫：由用户自己触发。验证只用合成素材和 `roomusic-test` 项目。

## 关键决定

- **多碟专辑先找父目录**：发行版本代表整张专辑，父目录里的封面是专辑级的，碟目录里的可能是单碟封面。
  另一种做法是保持“先找 CD1”，只把父目录当后备，改动更小，但两处都有封面时会用单碟封面。真实库没有两处都有封面的多碟专辑，两种做法的结果一样。
- **查找不超出发行目录**：Box 叶子目录、CUE 跨目录引用的音频目录都不去找。宁可显示“没有封面”，也不把盒子或公共目录里的封面绑到每张专辑上。
- **大小写纳入本任务**：缺口 1、2 修好之后，真实库里的效果主要取决于大小写；改动集中在同一个函数里。
- **不做迁移，也不兼容旧数据**：每次扫描都会重新绑定封面；开发阶段不为旧结果写兼容逻辑（用户既有裁决）。

## 实施结果与验收对照

2026-10-03 实施。代码在 `b66f569`（`backend/internal/catalog/persist.go`、`backend/internal/library/scanner.go` 与测试），文档在随后的提交里。

| # | 结果 |
| --- | --- |
| A1 | 通过：集成测试 `TestPostgreSQLCueAlbumGetsFolderArtworkAndRescanRebindsIt`（先没有封面，放入 `Cover.PNG` 后重扫绑定 `folder` 封面）；冒烟里 CUE 专辑 800×500 封面可经接口读取 |
| A2 | 通过：`TestPostgreSQLStrictMultiDiscUsesParentFolderArtwork`（父目录 `Cover.JPG`，JPEG） |
| A3 | 通过：`...ParentArtworkOutranksFirstDisc`（父目录胜过 `CD1/`）、`...FallsBackToDiscFolderArtwork`（只有 `CD2/` 时用 `CD2/`）；冒烟阶段 B、C 复现 |
| A4 | 通过：`evidence_test.go` 的大小写变体、全小写优先、字节序、优先级不受大小写影响 4 个单元测试；冒烟里 `Folder.PNG` 被发现 |
| A5 | 通过：`TestPostgreSQLBoxLeafDoesNotUseBoxParentArtwork`；catalog 单元测试覆盖普通、根目录散曲；原有测试期望没有改 |
| A6 | 通过：`TestPostgreSQLEmbeddedArtworkOutranksFolderArtwork`（内嵌封面与目录里的 `cover.png` 同时存在，取内嵌） |
| A7 | 通过：原有三个失败测试只改了函数签名；新增“前一个目录无效、后一个有效”“只有无效文件返回错误”“目录不存在跳过”“读目录失败报告”等用例 |
| A8 | 通过：`gofmt -l`、`go vet ./...`、`go build ./...`；`go test -race ./internal/catalog/... ./internal/library/... -count=1`；`./scripts/test-integration.sh` 全部通过，`api-samples` 没有变化。没有运行全量 `go test -race ./...`：改动只在 catalog、library 两个包，REST 与前端没有变化 |
| A9 | 通过：运行合同补了查找目录、顺序、大小写与错误矩阵、必需测试，技术债两条（外加大小写）划掉并注明 F4；父任务表已加 F4；相对链接检查见提交记录 |

- 集成测试的变异检查：去掉发行目录后，多碟父目录的两个集成测试失败；改回用 `filepath.Dir(RelativePath)` 取曲目目录后，CUE 的单元测试失败。
- 端到端冒烟见 [research/e2e-smoke.md](research/e2e-smoke.md)：合成素材上 15 个组有封面，CUE 专辑从“没有”变为有，A/B/C 三阶段一致。
- 设计与实现的差别：`ArtworkDirectories` 取曲目目录时复用 organizer 的 `locateObservation`（观察的 `Directory`，为空时才取路径的目录），
  与整理时对同一条观察的目录判断一致，没有另写一份。
- 用户重扫真实库后的预期：按 [research/real-library-artwork-layout.md](research/real-library-artwork-layout.md) 的统计，最多约 8 张专辑多出文件夹封面（6 个含 CUE 的目录、唯一的多碟专辑，以及 1 个只有大小写不同封面的普通目录）；
  已有内嵌封面的专辑不变；没有变化的专辑不会被改写。扫描本身不改动 `music/`。

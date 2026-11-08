# O / T / M 合入前语义交叉审查

日期：2026-11-08。对象为当前合并后的工作树；主会话和实施代理仍在并行收尾，本文行号对应本次读取时的文件。

## 后续裁决与修复（2026-11-08）

以下正文保留首次审查时的证据；用户随后批准在本次合并补齐三个格式的 `finalizeTags` 接线，接受文件名 `VA` 的合辑行为，并确认 CUE 不继承父音频 Credits/Compilation 为本次不改的已知边界。最新修复和验证结果见 [整合记录](./merge-main-integration.md) 的“裁决后的最小接线修复”，不再将 F1 视为未处理项。

## 范围与证据等级

- 只读产品代码、规则、任务设计、相关后端合同及测试源码；仅新增本报告。未修改产品代码、测试或规则，未提交、重置或操作其他工作树，未派生代理。
- 本审查没有运行测试、启动数据库或重复构建。运行结果单独注明为主会话回报，不把静态检查、跳过的数据库测试或测试源码存在当作通过。
- 已加载本机 AGENTS、backend index、质量/数据库/搜索/艺人/扫描相关合同、当前任务 check/implement 上下文及 PRD/design/implement，并核对⑥⑦⑧与 M 的相关设计。

## 已修复发现

无。此次授权仅允许报告，不允许修改产品语义或接线。

## 未修改的发现

### F1：确认的合入接线缺口——三种新格式没有执行标签最终处理

**位置：**

- `backend/internal/library/parser/ape.go:38–41`：`readAPEv2` 后直接 `applyFilenameFallback`。
- `backend/internal/library/parser/wavpack.go:113–116`：同上。
- `backend/internal/library/parser/aiff.go:60–62,78–89,190–202`：ID3 块调用 `parseID3Frames`，补 NAME/AUTH 后直接 `applyFilenameFallback`；两层均没有 `finalizeTags`。
- `backend/internal/library/parser/parser.go:74–95`：公共分派直接返回上述各格式结果，没有上层补调。
- 对照正常 MP3 入口：`backend/internal/library/parser/mp3.go:45–46` 明确先 `finalizeTags` 再文件名兜底。

**实际断点，不是由函数名推测：**

1. 专辑艺人只在 `captureArtistTag` 中记录证据，不直接赋 `AlbumArtist`；只有曲目艺人会即时赋值（`artist_evidence.go:46–59`）。专辑艺人的优先级归并与最终赋值位于 `tag_finalization.go:14–27,39–48`。因此三种格式即使读到了合法专辑艺人证据，最终 `AlbumArtist` 仍为空。
2. 作品和乐章先写进私有 `workTags`，不是公开 `Work/MovementName/MovementNumber`（`work_tags.go:20–50`）；公开字段只在 `finalizeWorkTags` 写入（同文件 `53–82`），而它仅由 `finalizeTags` 调用（`tag_finalization.go:10–13`）。因此三种格式的作品/乐章标签不会进入 scanner 暂存，不能靠后续归组或持久化恢复。
3. 歌词文本本身能读到，但歌词头部署名只在 `finalizeTags → addLyricsCredits` 产生（`tag_finalization.go:12`，`lyrics_credits.go:15–62`）。三种格式的 `Source=lyrics` 署名缺失；已有 `Source=tag` 标签署名仍可正常产出，不能一概说“署名全丢”。
4. 同根因还使原始年份回退不执行（`tag_finalization.go:29–35`）。署名最终排序去重虽未在 parser 执行，持久化还有 `CanonicalCredits`（`ingest/persist.go:114–115`），但这不能补回从未生成的歌词署名、专辑艺人或作品。

**实证：**主会话回报最小 Go 测试中的 `parser/TestAPEv2Tags` 四个 version/header 子用例失败，`lossless_test.go:195`；观察中有 `ALBUM ARTIST=专辑艺人`、来源 `ape_apev2`，但 `AlbumArtist` 为空。这与上述断点完全一致。WavPack、AIFF 及作品/歌词署名的影响在本报告中是代码路径确认，尚非本审查运行所得的实证；现有失败也可能在到达后续格式断言前已终止子用例。

**组合影响：**缺失真实专辑艺人不仅影响展示。`applyCompilation` 的“真实专辑艺人优先于合辑标志”保护依赖最终 `AlbumArtist`（`ingest/compilation.go:7–8`）；例如文件同时有真实专辑艺人与合辑标志，缺少最终处理会让原本应保留的专辑艺人被当成缺失，进入合辑分支。对归组分区的影响还取决于目录和其余曲目的标签，不能未经测试声称所有目录都会误合并。

**建议：**合入前由获授权实施方统一补齐三个容器在全部标签读取完成后、文件名兜底之前的最终处理，并为下面矩阵中的延迟字段补最小文件级回归。这是接入既有规则，不建议为通过测试另造 APE/AIFF 专用优先级。此次只报告，不代改。

### R1：新组合的产品风险——文件名推断占位词会升级为高置信度合辑决定

**可确认行为：**没有真实专辑艺人的目录，若文件名推断出的发行多数艺人恰为 `VA`、`Various Artists`、`群星` 等，占位词分支不检查来源或 inferred 标记，会改写成 `artist=群星, source=compilation, confidence=high, action=auto_apply`。

- `ingest/filename_numbers.go:158–176` 从缺标签标题里恰好一个 ` - ` 分隔处提取艺人，保留 `InferredFields[artist]=true`，来源改为 `filename_fallback`；例如 `album/VA - Song.flac`。
- `ingest/organizer.go:547–557,828–830` 在无明确专辑艺人时对曲目艺人选值，再调用合辑处理。
- `ingest/compilation.go:7–27` 仅检查专辑艺人、占位词及合辑票数，不区分文件名与标签来源。
- `catalog/internal/artistgraph/facts.go:82–99` 不产生发行级占位署名；但 `102,144–160` 特意允许 `filename_fallback` 的曲目艺人入图，曲目层的 `VA` 仍可成为艺人，不是整个人被删除。

**定性：**行为可由代码确认，但“这是不是误判”取决于文件名实际含义及产品期望。⑥既有设计明确对 Release artist 做整串占位判定，并未规定文件名例外，因此不是已经证实的违约实现。O③新增了能进入该分支的推断来源，这一组合与置信度升级值得主会话确认；不能擅自增加来源门槛或禁词。

**建议：**确认文件名占位是否沿用现合同；若沿用，补一条 `VA - Song` 与普通文件名艺人的并排断言，固定“发行无占位 refs、曲目仍保留 refs、观察仍为 inferred”的预期即可。

### R2：边界风险——不能把物理轨连通结论扩大成所有 CUE 虚拟轨

`library/scanner.go:628–724` 的 `buildCueVirtualObservation` 虽先复制 parent，但返回的是新建 `SourceObservation`：显式带了 `Work/MovementName/MovementNumber`（`696–698`），没有带 `Credits` 和 `Compilation`。因而父文件标签署名/合辑标志不会经该虚拟观察直接传递。对多轨整轨镜像，物理父观察被 CUE 去重后，不能笼统声称父标签署名仍会保留。

这不是本次 O 合并才引入的变化：读取 `HEAD:backend/internal/library/scanner.go` 也有相同字段遗漏。父文件署名是否应复制到每个虚拟轨涉及语义归属，当前⑦设计未明确授权这一继承规则，因此只列风险/测试边界，不当作本次物理轨回归，也不擅自复制。单轨 CUE 合回物理轨返回原 physical 对象（`ingest/organizer.go:1116–1126,1214`），不受此特定返回字段遗漏影响。

## 无问题项一：O③ 文件名艺人与 T⑥多值拆分

- 文件名提取只负责分出原始艺人段，并不另建一套逗号/斜杠规则（`ingest/filename_numbers.go:167–176`）。物理轨入库统一调用 `metadata.CanonicalTrackArtist`（`ingest/persist_track.go:73–82`）；艺人图从 `track_observations` 恢复 `filename_fallback` 原文后使用同一 `CanonicalArtistNames`（`artistgraph/facts.go:34,102,144–156`）。未发现 O③使文件名名段绕过或重复改变 T⑥规则。
- `metadata/artist.go:65–109,184–215`：换行/NUL 先分、固定组合及拉丁“姓, 名”保护、紧凑标点拆分顺序仍一致。`Bach, Johann Sebastian` 保留，`成龙,金喜善` 拆分，`AC/DC` 保留；`Tom & Jerry` 拆分、`Queen, David Bowie` 保留是已接受规则/局限，不能作为此次新缺陷要求改规则。
- 占位只在发行级做整串判断（`metadata/compilation.go:8–20`），曲目艺人不会因为命中占位词而被过滤。`Various Artists feat. 某某` 这类多名整串并不等于占位词；现设计不是“先拆出每个人再全局剔除占位词”，不可据此擅自改成逐人过滤。
- 现有文件名集成测试只覆盖普通单一艺人（`catalog/artists_reads_integration_test.go:340–405`）；占位集成测试只覆盖已写入的发行/曲目事实（`artistgraph/compilation_integration_test.go:5–32`）。没有看到它们组合后的文件名占位断言。

## 无问题项二：已正确解析的物理轨字段经过 O②/③的完整通路

下述结论以解析器已经产出公开字段为前提；不抵消 F1。

| 阶段 | 逐行证据与结论 |
| --- | --- |
| parser → scanner | `library/scanner.go:624–625` 明确复制 `Compilation`、`Credits` 整个结构切片及 `Work/MovementName/MovementNumber`，并非只拷 role/name。`metadata/text.go:14–18` 的 Credit 含 `detail/source` JSON 字段。 |
| 暂存/装载 | `ingest/observation.go:24–26,39,60–61` 定义全部相关字段；`staging.go:53–81,133–155` 对整个观察 Marshal/Unmarshal。无封面规划读取仅减去 `artwork`，碟目录/CUE scope 对齐只改变 scope 或追加 JSON 键（`187–203`），没有重建窄字段投影。 |
| O② 碟目录合并 | `organizer.go:645–681,691–718` 按目录收集并移动完整 SourceObservation；`864–891` 仅调整介质/轨位及相应来源，不修改 Credits 或作品字段。 |
| O③ format_split | `continuity.go:85–122` 按 codec/扩展名把完整 observation 分桶；`organizer.go:742–747,797–800,891` 保存原观察及版本组。不存在按第一种格式复制全部字段给其它格式的路径。 |
| 署名写入 | `persist.go:111–119` 按轨重建 `track_credits(role,name,detail,source,position)`；`metadata/text.go:44–79` 以角色/名字/细分去重、空 source 补 tag，同键 tag 优先。新角色不会在此被裁掉。 |
| 作品写入 | `persist.go:38,91–99` 对每个候选独立 AssignWorks；`works.go:21–81` 按曲目路径返回作品事实；`persist.go:212–224` 按碟/轨位排序；`persist_track.go:76–82` 新增和更新路径都写六个作品列。多格式候选独立分配作品组，不跨候选串组。 |
| 艺人图/搜索 | `artistgraph/facts.go:28–54,104,159–162` 按角色/名字建立事实，同一人同角色多 detail 只建一份身份事实，这是⑦约定，不是把 track_credits 的 detail/source 删了。`searchindex/write.go:138–164`、`batch.go:145–179` 单条与批量路径都读取作品名/编号及 credit；`values.go:25` 接受 lyricist/arranger。搜索本就不按 detail/source 匹配。 |
| REST 读取 | `catalog/read.go:182–255` 读取 work_index/work_title/movement_title；`283–320` 读取署名 role/name/detail，并按 track ID 放回正确介质曲目。仅有标签作品单曲组的 work_index 为空时 REST work=null，是⑧既定合同，作品标题仍在库及搜索中。 |

**关于 `Credit.Source` 的精确结论：**`tag/lyrics` 来源完整进入暂存和 `track_credits.source`。当前 `read.go:283–290` **不 SELECT c.source**，`projection.go:70–75` 的公开 `ReleaseCredit` 也没有 Source 字段；⑦设计 §3.6 只承诺增加公开 `detail`，没有承诺公开来源。因此“读取层保留 detail”成立，“REST 也返回 credit source”不成立，但不属于现合同缺陷。若要求新增公开来源，是接口变更，需要另行授权。

## M 格式逐入口核对矩阵

### APE / WavPack：APEv2

公共链为 `ParseAudioFile → parseAPE/parseWavPack → readAPEv2`。`apev2.go:92–106` 仅将键转大写、把 `TRACK/DISC` 改名，然后将文本值按 NUL 分开，**每个值**调用 `setAudioTag`；没有按格式删掉 T 新键，也没有自动加 `TXXX:` 前缀。

| 能力 | 当前接入状态与优先级 |
| --- | --- |
| 曲目艺人多值 | **已接入**。`ARTIST` 经 `tags.go:47` 到 `artist_evidence.go:54–58,63–78`，证据保留各值，展示按共同拆分去重排序。NUL 在 APEv2 层切，换行在艺人规则层切。 |
| 专辑艺人多值/回退 | **证据已接入，最终值缺失（F1）**。`ALBUMARTIST/ALBUM_ARTIST/ALBUM ARTIST` 都被识别；预期优先级为 1/2/3，同级全部值合并，须由 `tag_finalization.go:14–27,39–48` 执行。不是缺 `Album Artist` 键映射，也不是 APEv2 忘了按 NUL 遍历。 |
| 合辑标志 | **已直接接入**。`COMPILATION/ITUNESCOMPILATION`（共享表也识别 TCMP）在 `tags.go:62–66` 按真值置位；不依赖 finalize。真实专辑艺人保护因 F1 可能失效。 |
| 普通标签署名 | **已接入**。`COMPOSER/LYRICIST/WRITER/ARRANGER/CONDUCTOR/PRODUCER/MIXER/ENGINEER/REMIXER/MIXARTIST/DJMIXER/PERFORMER/PERFORMERS/SOLOIST/SOLOISTS/ENSEMBLE/ORCHESTRA/LIBRETTIST` 经 `tags.go:96 → credits.go:13–45`；WRITER 双角色、制作细分、乐器括号均走同一实现。`credits.go:86–89` 明确写 `Source=tag`；艺人证据 SourceKind 仍是 ape_apev2 / wavpack_apev2，两种来源含义不可混淆。 |
| 成对人员表 | APEv2 的文本键不会进入 `addID3PairedCredits`；只有真实 ID3 帧分派有该入口。不能把 APEv2 中一个名为 TIPL/TMCL 的普通文本键说成已支持成对表，也没有在 M/T 设计中找到要求支持这种键的合同。APE 文件头 ID3 本来只跳过，见 `ape.go:44–68`，不是 AIFF 的 ID3 路径。 |
| 歌词头部署名 | **歌词可读，署名缺失（F1）**。`LYRICS` 优先于 `UNSYNCEDLYRICS/UNSYNCED LYRICS`，同类第一个有效值优先（`lyrics.go:51–69`）；但不调用 `addLyricsCredits`，没有 Source=lyrics。 |
| 作品/乐章 | **键收集已接入，公开字段缺失（F1）**。`WORK/MOVEMENTNAME/MOVEMENTNUMBER/MOVEMENT` 由 `tags.go:41–42 → work_tags.go:20–50` 收集；数字或 n/m 的 MOVEMENT 取号，否则作名，明确名/号优先等规则均只在 finalize 执行（`53–82`）。 |

### AIFF：ID3 块

`aiff.go:51–62,190–202` 把真实 ID3 块头的版本/标志交给共享 `parseID3Frames`；不是调用 `parseMP3`，因此不会继承 MP3 外层的 finalize。

| 能力 | 当前接入状态与优先级 |
| --- | --- |
| 曲目艺人多值 | **已接入**。TPE1 按 `decodeID3ArtistValues` 遍历所有值（`mp3.go:116–129`），即时写艺人展示；AUTH 仅在 ID3 未给艺人时兜底（`aiff.go:83–87`）。 |
| 专辑艺人多值/回退 | **证据已接入，最终值缺失（F1）**。TPE2 全部值，以及 TXXX 的 ALBUMARTIST/ALBUM_ARTIST/ALBUM ARTIST 均进入证据（`mp3.go:97–128`）；原生 TPE2 优先于 TXXX:ALBUMARTIST，再优先于其它 TXXX 变体，依赖 finalize，不能声称 AIFF 当前已完成该优先级。 |
| 合辑标志 | **已直接接入**。TCMP/TXXX:COMPILATION 在 `mp3.go:97–113` 交给 setAudioTag；真值 OR 置位，不依赖 finalize。 |
| 原生/自定义署名 | **已接入**。TCOM/TEXT/TPE3 走 `mp3.go:116–128`；TXXX 已知署名键走 `97–113 → setAudioTag`；均输出 Source=tag，支持⑦新角色和 detail。 |
| TIPL/IPLS/TMCL | **已接入**。`mp3.go:89–95 → credits.go:105–133` 真正按 NUL 成对解析，TIPL/IPLS 查工种、未知归 producer，TMCL 查乐器、未知归 performer，detail 保留规定的值；标签来源为 tag，不依赖 finalize 才生成。 |
| 歌词头部署名 | **USLT 文本已接入，署名缺失（F1）**。`mp3.go:73–80` 保留 USLT，空描述优先；没有外层 finalize，未生成歌词来源署名。 |
| 作品/乐章 | **帧/键收集已接入，公开字段缺失（F1）**。TIT1/MVNM/MVIN 已在帧白名单，并与 TXXX 共同转到 setAudioTag（`mp3.go:83,97–113`）。TIT1 是否属于旧分组的保护、TIT1 对 TXXX:WORK 优先、原生乐章对自定义键优先等均在 `work_tags.go:53–82`，当前 AIFF 未执行。 |

## 最终合并产物附加核对

- **read.go：无覆盖丢失。**`normalizeCandidateKind` 在 `570` 保留 format_split；`182–190` 同时有 M 的 `NULLIF(dd.duration_seconds,0)` / `track_display_durations` 连接和 T 的三项作品读取；`250–255` 构造 Work；`284,304` 读取 credit detail。Source 的公开边界见上文，不能误报“read.go 兼有 c.source”。
- **回滚夹具 34–40：没有互相覆盖。**`platform/postgres/migrations_rollback_integration_test.go:16–58` 七个 map 键各一份：34 文件过滤、35 根改路径、36/37 空夹具、38 署名、39 作品、40 搜索空夹具。`131–165` 的前置依赖分别对应 38、39 和 34/35，35 的路径保留期望与断言在 `176–189` 仍在。38 与 39 某些合成 UUID 尾缀相同不是碰撞：每个迁移子用例独立建 schema（`120–124`），仅执行自身版本夹具（`169–175`）。这里只确认映射与逻辑未互相覆盖，不代替数据库往返实证。
- **迁移文件顺序：**0034 scan_file_filter、0035 root_relocate、0036 track_analysis_loudness_range、0037 scan_observation_previous_release、0038 credit_roles、0039 classical_works、0040 search_punctuation，每项 up/down 文件均在。
- **版本：4/2/1 正确。**`artistgraph/state.go:11` 为 4；`searchindex/state.go:13` 为 2；`textnorm/textnorm.go:13` 为 1。
- 读取 `git diff --name-only --diff-filter=U` 为空；未发现仍留在 unmerged 索引中的文件。此项不等同于完成全部行为验证。

## 验证与测试缺口

- **静态检查：**主会话回报 vet 通过；本审查未重复运行。
- **编译检查：**主会话回报 build 通过；本审查未重复运行。
- **测试：失败（主会话实证）**：最小 Go 测试唯一失败为 `TestAPEv2Tags` 的四个 version/header 子用例，专辑艺人为空；其余 library/catalog/platform 包回报通过。依赖方向测试当时被 `&&` 短路，主会话另行执行后已通过（收尾补记）。没有数据库门禁原始执行结果，不将普通包通过推断成 PostgreSQL 往返通过。
- **缺口：**现有 `parser/lossless_test.go:142–166` AIFF 断言只有基本 ID3/NAME/AUTH 映射，没有专辑艺人优先级、作品或歌词署名；APEv2 测试已有专辑艺人断言，正好检出 F1，但缺⑦新角色/detail/source 与⑧的文件级组合断言。
- **缺口：**`scanner_merge_formats_test.go:14–85`、`scanner_format_split_integration_test.go:13` 及碟目录相关测试未见把⑦完整 Credit 和⑧标签作品放入 O②/O③后再读取的断言。`ingest/works_test.go:9` 与 `app/work_search_integration_test.go:11` 是作品分组/普通扫描搜索证据，不等同于多碟合并和多格式拆分组合证据。
- **建议的最小补充：**三个新格式各一条带专辑艺人回退冲突、一个标签 detail/source、一个歌词署名、作品/乐章的解析断言；O②/O③各一条包含上述已解析字段的暂存→归组→保存→读取检查；文件名占位测试待 R1 语义确认。只列缺口，不在本审查新增或执行测试。
- **实际执行的核对命令类别：**`cat`/`sed -n`/`nl -ba`/`rg -n` 读取上述代码与合同；`git show HEAD:backend/internal/library/scanner.go` 对比 CUE 既有边界；`git diff --name-only --diff-filter=U` 检查未合并索引。没有执行 go test、数据库命令或全量测试；遵守本次只读、不重复测试的授权。

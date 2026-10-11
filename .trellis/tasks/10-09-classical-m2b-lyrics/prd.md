# Classical M2b：内嵌歌词与同步显示

## 目标与用户价值

真实库有 134 个文件内嵌歌词，其中 110 份带 LRC 时间轴。扫描时把歌词作为曲目事实保存，正在播放页按时间轴逐行同步显示、点一行跳到该处；
没有时间轴的歌词静态显示，没有歌词就如实说明。技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)。

## 依据

- `docs/architecture/core-1-roadmap.md` 阶段表 M2b 行（含歌词）；父任务 R4。
- 2026-10-09 用户裁决 M2b 范围为“ALAC 派生 + 歌词，CUE 精确分轨延后”，本任务是其中的歌词部分；与派生任务相互独立，建议在派生任务之后实施
  （两者都改正在播放页，先后进行避免冲突）。
- 设计规则：`frontend/DESIGN.md:327`、`:355`、`:358`，`.trellis/spec/frontend/player-design-guidelines.md:42`；原型的歌词面板。
- 调研：[research/lyrics-evidence.md](research/lyrics-evidence.md)。

## 已核实事实

- 歌词来源只有内嵌标签：FLAC 的 `LYRICS` 50 个、MP4 的 `©lyr` 84 个；没有外置 `.lrc`。
- 110 份带时间戳，29 份含 LRC 元标签，5 份有同一时间戳的多行；没有 `[offset:]` 与逐字时间戳；最大 5263 字节。
- 解析器目前不提取歌词；`tracks` 没有歌词列；没有按曲目读取歌词的接口。
- `library-scan-contracts.md` 只剩约 750 字节余量，歌词规则需要新的专题规范。

## 范围

做：

- 解析器提取内嵌歌词：Vorbis 注释（FLAC、Ogg、Opus）的 `LYRICS`，没有时取 `UNSYNCEDLYRICS`；MP4 的 `©lyr`；MP3 的 ID3v2 `USLT`。
- 迁移 0015：`tracks.lyrics`、`tracks.lyrics_source`；扫描时写入或清空。
- `GET /api/v1/tracks/{id}/lyrics`：返回解析后的行（同步或纯文本）。
- 正在播放页：按原型改为“封面 | 歌词”两栏，标题与控制在下；逐行同步、跟随与静态阅读、点击跳转、双语分组、空态。
- 真实服务自动化覆盖带歌词的合成素材；规范与文档。

不做：

- 外置 `.lrc`、联网获取、歌词编辑或修正（M3 起的覆盖层）、逐字高亮、ID3 `SYLT`、CUE 虚拟轨的歌词。
- 在专辑页或曲目行显示歌词标记。

## 需求与验收

| 编号 | 需求 | 验收 |
| --- | --- | --- |
| L1 | 提取内嵌歌词 | 合成素材中 FLAC/Ogg/Opus 的 `LYRICS`（优先）与 `UNSYNCEDLYRICS`、MP4 的 `©lyr`、MP3 的 `USLT`（含 ISO-8859-1、UTF-16、UTF-8 三种编码）都能提取；超过 64 KiB、非法 UTF-8 或只有空白的歌词不提取；CUE 虚拟轨没有歌词 |
| L2 | 保存与更新 | 迁移 0015 增加两列及约束；重扫后歌词随文件更新或清空；曲目身份规划与 API 样本中的其他字段不变 |
| L3 | 歌词接口 | 登录成员可读；非 UUID 400、未登录 401、不存在或非 present 404；没有歌词返回 `lyrics: null`；同步歌词按时间排序、一行多个时间戳展开、元标签去掉、`[offset:]` 生效、逐字标记去掉；纯文本保留空行分段；有接口样本与前端解码器 |
| L4 | 同步显示 | 当前曲有同步歌词时，当前行随播放位置变化并保持在可视区中部；手动滚动后暂停跟随并出现“回到当前行”；媒体可拖动时点击或按 Enter 跳到该行时间，不可拖动时行不可点；同一时间戳的多行作为一组显示 |
| L5 | 纯文本、空态与错误 | 纯文本歌词静态显示、不高亮、不可点；没有歌词显示“这首曲目没有内嵌歌词”；加载中与读取失败有明确状态和重试 |
| L6 | 可访问性与布局 | 当前行变化不做读屏播报；尊重减少动态效果（不平滑滚动）；高亮不改变行高；五种宽度布局不溢出（窄屏单栏）；axe 无新增问题；键盘可完成跳转与“回到当前行” |
| L7 | 真实服务自动化 | 合成的 FLAC 与 ALAC 素材带 LRC 歌词：扫描后接口返回同步歌词，播放时当前行前进，点击一行后播放位置跳到该行时间 |
| L8 | 文档 | 新增歌词专题规范；更新 catalog REST 合同、扫描合同的指引、前端规范、DESIGN.md、路线图与父任务；收尾时用合成数据的页面截图请用户确认布局 |

## 待批准决策（推荐方案）

| 编号 | 决策 | 推荐 | 不同选择的代价 |
| --- | --- | --- | --- |
| Y1 | 歌词来源 | 只读内嵌标签（真实库的全部来源）；不读外置 `.lrc`、不联网 | 支持外置 `.lrc` 要让扫描跟踪额外文件的变化，真实库目前用不上 |
| Y2 | 保存方式 | 扫描时提取并存入曲目记录（上限 64 KiB），接口读取时解析；已入库的曲目要重扫一次才有歌词 | 播放时现读文件不需要重扫，但每次都要安全打开并解析音频文件，也无法作为曲目事实被后续修正覆盖 |
| Y3 | 页面布局 | 按原型改为“封面 \| 歌词”两栏，标题、进度与控制移到下方；没有歌词时歌词栏显示空态，布局不变 | 保留现有首屏、把歌词放到下方更省改动，但偏离原型，歌词不在首屏 |
| Y4 | 当前行样式 | 只改颜色与透明度，不改字号（原型的放大会让行高跳动）；跟随时当前行保持在中部 | 照原型放大字号更醒目，但每次换行都会引起布局跳动 |
| Y5 | 收尾确认 | 用合成数据的页面截图（宽屏、窄屏，同步、纯文本、空态）请你确认 | 不看截图直接归档，视觉问题要事后再改 |

## 风险

- 解析规则以真实库的形态为准：遇到不规范的时间戳（如负数、超过 24 小时）该行忽略；整份无法识别时按纯文本显示。
- 已有曲目要重扫才会出现歌词；ALAC 的 84 首要等派生任务完成才能播放，但接口在此之前已能返回歌词。

## 规划决策记录

| 日期 | 路线 | 选择者 | 结果 | 理由 |
| --- | --- | --- | --- | --- |
| 2026-10-09 | 人工选择 | qlqq | M2b 范围：ALAC 派生 + 歌词两个子任务，CUE 精确分轨延后 | 真实库没有 CUE 虚拟轨，134 个文件内嵌歌词 |
| 2026-10-09 | 人工选择 | qlqq | 批准 Y1–Y5 与本规划，均按推荐方案执行；本任务在派生任务归档后启动 | 用户阅读最终规划摘要（含“先做派生播放，归档后再做歌词”的顺序）后回复“批准执行” |
| 2026-10-11 | 人工选择 | qlqq | Y5 布局确认通过，不改页面，继续收尾 | 用户查看合成数据的页面截图（宽屏与窄屏；同步、纯文本、没有歌词）后回复“布局可以，继续收尾（推荐）” |

## 实施结果（2026-10-11）

提交（均在 `main`，未推送）：`d6c9fd4` 扫描时提取内嵌歌词（解析器、迁移 0015、曲目写入）；`62023a1` 歌词接口与逐行同步显示（接口、前端面板、正在播放页、内嵌产物）；
`ca17fa3` 真实服务覆盖歌词显示（带歌词的素材与用例）；`63099c8` 歌词合同与规范同步；`c25a70b` 真实库歌词覆盖的只读统计；
`a42f6f9` “回到当前行”的键盘用例。

真实库的只读核对（`TestRealMusicLyricsCoverage`，只输出计数，没有路径、文件名或歌词内容，不写入音乐目录）：284 个音频文件中 134 个带内嵌歌词
（MP4 的 `©lyr` 84 个、FLAC 的 `LYRICS` 50 个），其中同步 110、纯文本 24，没有解析失败，原文最大 5263 字节、解析后最多 89 行；库里唯一的 MP3 是 ID3v2.3。
与[调研](research/lyrics-evidence.md)完全一致。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| L1 | 达成 | `TestNormalizeLyrics`、`TestFLACParserReadsVorbisLyrics`、`TestOggAndOpusParsersReadVorbisLyrics`、`TestM4AParserReadsLyrics`、`TestMP3ParserReadsUSLTLyrics`（四种编码）、`…PicksTheUSLTFrameWithAnEmptyDescriptor`、`…IgnoresMalformedUSLTFrames`、`TestID3FrameSizeFollowsTheTagVersion`、`TestRemoveUnsynchronisation`；素材事实测试核对真实 ffmpeg 写出的歌词；`TestPostgreSQLCueVirtualTracksHaveNoLyricsButTranscriptTracksKeepThem`（虚拟轨没有歌词，逐轨抄本的物理轨保留） |
| L2 | 达成 | 迁移 `0015_track_lyrics.sql`；`TestPostgreSQLScanStoresUpdatesAndClearsEmbeddedLyrics`（写入、改标签后更新、删除歌词后清空）、`TestPostgreSQLTrackLyricsConstraint`（约束的各格，含“有歌词、没有来源”）；既有扫描与整理测试照常通过；API 样本只有合成 FLAC 的 `size_bytes` 因带了歌词标签而变大（481 → 669） |
| L3 | 达成 | `TestParseLyrics{Synced,Offset,Plain,WithNothingToShow,Limits}`、`TestLyricsJSONShape`；`TestPostgreSQLTrackLyricsRequireLoginAndCheckInputInOrder`、`TestPostgreSQLTrackLyricsAreServedParsedToAnyMember`；`TestPostgreSQLAPIResponseSamples` 的 `tracks/lyrics-{synced,plain,none}` 三份样本，前端解码器与契约测试读取同一批样本 |
| L4 | 达成 | `lyrics-panel.test.tsx`（当前组、跟随、暂停跟随与“回到当前行”、点击与键盘跳转、不可拖动时不可点）、`engine/lyrics.test.ts`（分组、二分与线性查找一致、跟随下标）；Playwright“同步歌词：当前行随播放位置变化并保持在中部，点击与键盘跳转，高亮不改变行高”与“…手动滚动后暂停跟随…”；真实服务里真实点击一行后媒体位置不早于该行 |
| L5 | 达成 | `lyrics-panel.test.tsx` 的纯文本、没有歌词、读取失败与重试、404 按错误显示；Playwright“歌词读取失败：显示带重试的错误…；曲目没有歌词与纯文本如实显示” |
| L6 | 达成 | 面板不设 `aria-live`、减少动态效果时不平滑滚动、字号与行高不随当前行变化的用例；Playwright 三种形态在五种宽度下布局不溢出且 axe 无严重问题；键盘有用例：Tab 只停在当前行、方向键、Home/End、Enter 跳转，Tab 到“回到当前行”后 Enter 恢复跟随（`a42f6f9`，把按钮改成空操作时失败） |
| L7 | 达成 | `./scripts/playback-e2e.sh` 21 项通过（原有 16 项加歌词 5 项）：FLAC 与 ALAC 素材经真实扫描后接口返回同步歌词；正在播放页显示歌词，真实点击一行后媒体位置不早于该行，继续播放时当前行前进；没有歌词的素材与 CUE 虚拟轨（即使整轨镜像文件带着歌词）返回 `null` |
| L8 | 达成 | 新增 `.trellis/spec/backend/lyrics-contracts.md`；更新后端索引、运行合同、扫描合同（只加指引）、catalog REST 合同，前端的播放器设计规范、组件规范、状态管理、质量规范、索引与目录结构，`frontend/DESIGN.md`、README、路线图与父任务；Y5 已由用户确认（见规划决策记录） |

验证：隔离 PostgreSQL（`roomusic-test`，空闲端口、随机库名，结束 `down -v`）上相关后端包的 `-race` 测试通过；Vitest 1073 项、Playwright（路由模拟）182 项、真实服务 21 项通过；
无环境变量的重新构建与内嵌产物逐文件一致；每个提交前在导出的暂存树上重复构建与测试。

### 与设计的差异

实施中有几处与 [design.md](design.md) 不同，已在其第 9 节记录原因：ID3v2.2 的 `ULT` 不读取；ID3 帧改为按版本读取（synchsafe 大小、同步消除、帧格式标志）；
Ogg 与 Opus 保留既有的单页假设；`tracks_lyrics_ck` 显式要求来源非空；解析规则的若干细化；接口多一个 503；歌词写进既有素材而不是新增文件；
面板只订阅当前组下标；当前组带 40 毫秒容差；键盘与读屏结构（只在当前行停靠、`role="group"` 滚动区）；一条既有长标题用例的断言随新布局调整。

### 仍需用户完成

- 升级后**重新扫描一次真实库**：已入库的曲目迁移后没有歌词，重扫才会写入（134 个文件）；重扫后在正在播放页确认歌词显示。
- 带歌词的 84 个文件是 ALAC，要装好 ffmpeg（`./scripts/ffmpeg-pinned.sh`，或发行版的包），设置 `ROOMUSIC_FFMPEG_PATH`（或放进 `PATH`）并重启服务后才能播放；
  没有 ffmpeg 时，播放时只有 50 个 FLAC 的歌词看得到。
- 歌词面板在 Safari、Firefox 与 Windows 浏览器里的实际渲染（容器查询、渐隐遮罩、滚动跟随）目前只有 Linux Chromium 的自动化证据；
  已在三组手工实播的“每组交互检查”里增加一行“歌词面板”，与 F1 一起做；F1 与下次推送后的远端 CI（F3）见后续验收任务 `10-06-m2a-acceptance-followup`。

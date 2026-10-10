# 歌词规划依据

核对日期：2026-10-09。真实库数据由临时脚本只读汇总（方法与格式构成见
[派生任务的调研](../../archive/2026-10/10-09-classical-m2b-derived-playback/research/library-evidence.md)），只输出计数，不输出路径、文件名或歌词内容。

## 真实库的内嵌歌词

| 指标 | 结果 |
| --- | --- |
| 有歌词的文件 | 134 / 284 个音频（FLAC 的 Vorbis 注释 `LYRICS` 50 个，MP4 的 `©lyr` 84 个，即全部 ALAC 文件）；没有 `UNSYNCEDLYRICS` |
| 带 LRC 时间戳 | 110 份；纯文本 24 份 |
| LRC 元标签（`[ar:]`、`[ti:]`、`[al:]`、`[by:]` 等） | 29 份 |
| `[offset:]` | 0 份 |
| 逐字时间戳（`<mm:ss.xx>`） | 0 份 |
| 同一时间戳多行（疑似双语对照） | 5 份 |
| 外置 `.lrc` 文件 | 0 个 |
| 文本大小 | 中位 1551 字节，95% 分位 3185 字节，最大 5263 字节 |

ALAC 的 84 个文件要等派生任务（`10-09-classical-m2b-derived-playback`）完成才能播放；歌词接口本身不依赖播放格式。

## 现有代码

- 解析器读取 Vorbis 注释（`backend/internal/library/parser/parser.go:1604`，字段分派在 `:1903` 附近）、MP4 的 iTunes item
  （`parseM4AItemValue`，`:626`；单个 `data` atom 最多读 16 MiB，`:104`）与 ID3v2 帧（`:1673`），目前都不提取歌词。
- 观察经暂存 JSON（`backend/internal/catalog/observation.go`）进入 organizer，再由 `persist_track.go:75`、`:80` 写入 `tracks`；新增字段要同时改两处 SQL。
- catalog 的 REST 注册在 `backend/internal/catalog/httpapi/handlers.go:34`–`39`；还没有按曲目读取的接口。
- `library-scan-contracts.md` 已 23,246 字节，离 24,000 字节上限只剩约 750 字节，歌词规则要写进新的专题规范。

## 设计依据

- 原型 `frontend-desgin/ROOMusic 原型.dc.html:183`–`205`：正在播放页第一行是“封面 | 歌词”两栏，歌词区上下渐隐、当前行居中放大并着色、
  点任一行跳到该处，底部注明来源与“逐行同步”；第二行是标题、进度与控制。
- `frontend/DESIGN.md:327`：原型的“本地 LRC、逐行点击定位、1 秒推进”是生成文本与定时状态；M2 基于真实来源、时间戳和媒体事件，未知时间禁用定位。
- `frontend/DESIGN.md:355`、`:358`：歌词逐行与播放时间不持续占用读屏公告，提供不自动滚动或静态阅读方式；源稿 `.3s` 字号/颜色过渡只是样机事实，需评估布局跳动，支持减少动态效果。
- `.trellis/spec/frontend/player-design-guidelines.md:42`：歌词有真实数据才显示，缺失展示空态；来源、时间轴和不支持格式不伪造。
- 当前正在播放页（`frontend/src/features/player/pages/now-playing-page.tsx`）是“封面 | 标题与控制”的首屏，下面是信号路径与队列。

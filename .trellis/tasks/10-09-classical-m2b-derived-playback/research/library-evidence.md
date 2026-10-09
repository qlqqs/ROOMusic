# M2b 规划依据：真实库只读汇总与运行环境

核对日期：2026-10-09。按 [真实音乐资产规范](../../../spec/guides/real-music-assets.md)，用临时 Python 脚本只读遍历 `music/`，
只输出按种类的汇总计数，不输出路径、文件名或内容；脚本没有入库。

## 格式构成

| 类别 | 数量 | 说明 |
| --- | --- | --- |
| `.flac` | 199 | M2a 已直出 |
| `.m4a` | 84 | sample entry 全部是 `alac`：24 bit/48 kHz 73 个、16 bit/44.1 kHz 10 个、24 bit/44.1 kHz 1 个；合计 3127 MiB |
| `.cue` | 8 | 见下节 |
| `.mp3` | 1 | M2a 已直出 |

ALAC 约占音频文件的 30%，M2a 收尾后在所有浏览器都标注“这种格式目前不能直接播放”，是真实库最大的播放缺口。

## CUE：真实库没有 CUE 虚拟轨

8 份 CUE 共 132 条曲目，全部是每条曲目一个 `FILE` 的逐轨抄本（FILE 类型都写 `WAVE`），不是整轨镜像。
引用扩展名 `.flac` 62 个、`.wav` 70 个；按写法能找到的引用 59 个（都是 FLAC 16 bit/44.1 kHz 立体声），找不到的 73 个。

按现有规则模拟暂存与去重：

- 69 条曲目在自己的 `FILE` 下没有 `INDEX 01`：EAC 式布局把下一轨的 `INDEX 00` 写在上一个 `FILE` 末尾、`INDEX 01` 写在下一个 `FILE` 下，
  解析器遇到新的 `FILE` 会先结束当前曲目（`backend/internal/library/parser/parser.go:859`），之后没有当前曲目的 `INDEX` 被忽略（`parser.go:921`），
  于是这些曲目按“缺少 INDEX 01”跳过（`backend/internal/library/scanner.go:461`）。
- 61 条曲目引用的文件找不到（多数是 CUE 仍写 `.wav`，实际文件已是 `.flac`），跳过。
- 2 条暂存为虚拟轨，各自是父文件唯一的虚拟轨，按 `backend/internal/catalog/organizer.go:940` 保留物理曲目并合并 CUE 字段。

结论：**真实库扫描后没有 `cue_virtual` 曲目**，这些专辑都以物理 FLAC 入库，M2a 已能直接播放。CUE 精确分轨目前在真实库里没有受益曲目。

顺带发现（不属于 M2b）：EAC 式布局的 CUE 会产生“缺少 INDEX 01”诊断，CUE 独有的字段（ISRC、目录号）也合并不到这些物理曲目；
FLAC 文件自身的标签仍提供标题等元数据，影响限于诊断噪声和少量字段。

## 内嵌歌词

| 指标 | 结果 |
| --- | --- |
| 有歌词的文件 | 134 个（FLAC 的 `LYRICS` 50 个，MP4 的 `©lyr` 84 个，即全部 ALAC 文件） |
| 带 LRC 时间戳 | 110 份；纯文本 24 份 |
| LRC 元标签（`[ar:]`、`[ti:]` 等） | 29 份；没有 `[offset:]`，没有逐字时间戳 |
| 同一时间戳多行（疑似双语对照） | 5 份 |
| 外置 `.lrc` 文件 | 0 个 |
| 文本大小 | 中位 1551 字节，95% 分位 3185 字节，最大 5263 字节 |

## 运行环境

- 宿主机没有 `ffmpeg`/`ffprobe`；Docker 29.1.3 可用。
- 生产由 `scripts/prod.sh` 在宿主机运行 Go（`go run ./cmd/roomusic`），`compose.yaml` 只承载 PostgreSQL 等开发依赖；
  CI 没有发布镜像或二进制的步骤，项目目前不分发任何 FFmpeg 构建。
- `scripts/gen-playback-fixtures.sh` 已按摘要固定 `mwader/static-ffmpeg@sha256:11a44711…`（7.1.1，静态构建），只用于生成合成素材。
- `ROOMUSIC_DATA_DIR` 默认 `./data`（`backend/internal/platform/config/config.go:24`），由资料库与封面使用。

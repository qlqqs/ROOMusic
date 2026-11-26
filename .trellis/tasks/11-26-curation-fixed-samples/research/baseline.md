# 固定样本基线

评分更正：按当前跑法的预期核对外部对应；预期为未匹配、不查或有冲突时，任何已建立的对应都计为错认，包括另一跑法允许的版、组、录音和艺人。

产品为本地 main 的 `38d7a63270513907e3082228b18534823df51a7b`，仅加本任务的测试驱动和 mock 规则。使用 PostgreSQL 18.1、Go 1.25.10、ffmpeg 7.1.1、fpcalc 1.6.2。曲库来自 28 张固定样本；两次运行各建独立 Compose 项目，仅接回环模拟上游。每个模式按同一顺序处理，以目录批次隔离 schema、缓存和上游计数。共享目录一次扫描，逐张放行。模型批大小及并发均为 1。

## 首次完整基线

开始于 2026-11-26 16:45:26 UTC。包含数据库启动、编译和清理的整条命令耗时 **3 分 30.808 秒**。各模式的墙钟还包括各目录的装配、扫描、模型收尾和清理，单张一栏使用放行后的识别耗时。采集时错认按样本去重，附带项独立显示。

| 模式 | 认对 | 认错 | 请求总数 | 每张请求 | 模式墙钟（秒） | 正确负例 |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| 程序 | 12 | 0 | 72 | 2.57 | 70.401 | 有冲突 1、未匹配 6 |
| 程序＋脚本 | 15 | 0 | 343 | 12.25 | 125.876 | 有冲突 1、未匹配 3 |

基线已有错认清单为空。未识别、缺写和扫描差异见下表。正确负例需同时满足目标结论和标注的扫描约束。

```sh
time env ROOMUSIC_CURATION_METRICS_SUITE=fixed-capture ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/first.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

完整机器基线在 `backend/internal/app/testdata/curation_regression/fixed-baseline.json`，包含逐张实际外部编号和自动字段。下表的请求简称：搜版、读版、读组、搜艺、读艺、搜录音均为 MusicBrainz；查碟为 DiscID，查指纹为 AcoustID，模型为 Chat Completions。

## 程序逐张明细

| 样本 | 预期 | 实际 | 按接口计数 | 总数 | 用时（毫秒） | 缺写、扫描或路径差异 |
| --- | --- | --- | --- | ---: | ---: | --- |
| editions-model-quartet | 组 | 候选过多 | 搜版 2 | 2 | 614 | — |
| embedded-artist | 未匹配 | 未匹配 | 搜艺 1、搜版 3 | 4 | 856 | — |
| one-char | 未匹配 | 未匹配 | 搜艺 1、搜版 3 | 4 | 662 | — |
| romanized | 未匹配 | 未匹配 | 搜艺 1、搜版 3 | 4 | 963 | — |
| legacy-one | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 985 | title 期望 去靛雾，实际 È¥µåÎí |
| legacy-many | 不查 | 未匹配 | 搜艺 1 | 1 | 662 | — |
| sampler-five | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 1426 | — |
| disc-folders | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 783 | — |
| named-bonus | 具体一版 | 有冲突 | 搜艺 1、搜版 3 | 4 | 1826 | 碟标题：Medium；预期一张，实得 3 张 |
| filename-only | 不查 | 未匹配 | 搜艺 1、搜版 2 | 3 | 641 | 01 镜舟 - 昏灯1.flac 标题 昏灯1；02 镜舟 - 昏灯2.flac 标题 昏灯2；03 镜舟 - 昏灯3.flac 标题 昏灯3；04 镜舟 - 昏灯4.flac 标题 昏灯4 |
| name-only-gap | 具体一版 | 具体一版 | 读版 1 | 1 | 811 | — |
| mixed-contiguous | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 816 | — |
| mixed-a | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 875 | — |
| mixed-b | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 753 | — |
| amp-comma | 具体一版 | 未匹配 | 搜艺 1、搜版 2 | 3 | 505 | artist 期望 棱镜 & 林, 海，实际 林 / 棱镜 / 海；署名 [林 棱镜 海] |
| picard-01 | 具体一版 | 具体一版 | 读版 1 | 1 | 721 | — |
| cue-01 | 具体一版 | 具体一版 | 查碟 1、搜版 1 | 2 | 701 | — |
| identifier-01 | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 764 | — |
| catalog-label | 具体一版 | 具体一版 | 读版 1、搜版 1 | 2 | 1166 | — |
| isrc-only | 具体一版 | 具体一版 | 搜录音 1、读版 1、搜版 1 | 3 | 715 | — |
| fingerprint-only | 具体一版 | 具体一版 | 查指纹 4、读版 1 | 5 | 1029 | — |
| id-conflict | 有冲突 | 有冲突 | 读艺 1、读版 1 | 2 | 484 | — |
| unlisted-01 | 未匹配 | 未匹配 | 搜艺 1、搜版 2 | 3 | 496 | — |
| spoken-podcast | 不查 | 未匹配 | 搜艺 1、搜版 2 | 3 | 498 | — |
| unknown-disc-a | 未匹配 | 未匹配 | 搜艺 1、搜版 2 | 3 | 522 | — |
| unknown-disc-b | 未匹配 | 未匹配 | 搜版 2 | 2 | 469 | — |
| missing-position | 未匹配 | 未匹配 | 搜艺 1、搜版 2 | 3 | 596 | 叶丁.flac 碟/轨 1/3，推断=true |
| field-tie | 未匹配 | 未匹配 | 搜艺 1、搜版 2 | 3 | 519 | 打平年份被选为 2010 |

## 程序加脚本模型逐张明细

| 样本 | 预期 | 实际 | 按接口计数 | 总数 | 用时（毫秒） | 缺写、扫描或路径差异 |
| --- | --- | --- | --- | ---: | ---: | --- |
| editions-model-quartet | 组 | 组 | 读版 4、搜版 2、模型 4 | 10 | 1487 | 专辑/year；读版详情 4 次；进入模型识别 |
| embedded-artist | 具体一版 | 具体一版 | 搜艺 1、读组 1、读版 1、搜版 4、模型 8 | 15 | 4761 | — |
| one-char | 具体一版 | 具体一版 | 搜艺 1、读组 1、读版 1、搜版 4、模型 9 | 16 | 1562 | — |
| romanized | 具体一版 | 具体一版 | 搜艺 1、读组 1、读版 1、搜版 4、模型 8 | 15 | 1543 | — |
| legacy-one | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 843 | title 期望 去靛雾，实际 È¥µåÎí |
| legacy-many | 具体一版 | 未匹配 | 搜艺 1、搜版 1、模型 21 | 23 | 3708 | forbidden_query/musicbrainz/query 期望 不得包含 µåÎí，实际 µåÎí |
| sampler-five | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 6 | 8 | 1106 | — |
| disc-folders | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 797 | — |
| named-bonus | 具体一版 | 有冲突 | 搜艺 1、读版 1、搜版 4、模型 84 | 90 | 12389 | 碟标题：Medium；预期一张，实得 3 张 |
| filename-only | 不查 | 未匹配 | 搜艺 1、搜版 3、模型 9 | 13 | 2082 | 01 镜舟 - 昏灯1.flac 标题 昏灯1；02 镜舟 - 昏灯2.flac 标题 昏灯2；03 镜舟 - 昏灯3.flac 标题 昏灯3；04 镜舟 - 昏灯4.flac 标题 昏灯4 |
| name-only-gap | 具体一版 | 具体一版 | 读版 1、模型 4 | 5 | 860 | — |
| mixed-contiguous | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 877 | — |
| mixed-a | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 2245 | — |
| mixed-b | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 1910 | — |
| amp-comma | 具体一版 | 具体一版 | 搜艺 1、读组 1、读版 1、搜版 3、模型 15 | 21 | 3897 | artist 期望 棱镜 & 林, 海，实际 林 / 棱镜 / 海；署名 [林 棱镜 海] |
| picard-01 | 具体一版 | 具体一版 | 读版 1、模型 2 | 3 | 869 | — |
| cue-01 | 具体一版 | 具体一版 | 查碟 1、搜版 1、模型 2 | 4 | 1044 | — |
| identifier-01 | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 813 | — |
| catalog-label | 具体一版 | 具体一版 | 读版 1、搜版 1、模型 2 | 4 | 822 | — |
| isrc-only | 具体一版 | 具体一版 | 搜录音 1、读版 1、搜版 1、模型 2 | 5 | 853 | — |
| fingerprint-only | 具体一版 | 具体一版 | 查指纹 4、读版 1、模型 3 | 8 | 1214 | — |
| id-conflict | 有冲突 | 有冲突 | 读艺 1、读版 1、模型 8 | 10 | 2442 | — |
| unlisted-01 | 未匹配 | 未匹配 | 搜艺 1、搜版 2、模型 4 | 7 | 1552 | — |
| spoken-podcast | 不查 | 未匹配 | 搜艺 1、搜版 3、模型 9 | 13 | 2116 | — |
| unknown-disc-a | 未匹配 | 未匹配 | 搜艺 1、搜版 3、模型 8 | 12 | 1751 | — |
| unknown-disc-b | 未匹配 | 未匹配 | 搜版 3、模型 8 | 11 | 1892 | — |
| missing-position | 未匹配 | 未匹配 | 搜艺 1、搜版 3、模型 9 | 13 | 2080 | 叶丁.flac 碟/轨 1/3，推断=true |
| field-tie | 未匹配 | 未匹配 | 搜艺 1、搜版 3、模型 9 | 13 | 2080 | 打平年份被选为 2010 |

## 夹具版本

| 文件 | SHA-256 |
| --- | --- |
| fixed.json | `010c7c1e3920c2b086bee9834d7beeeb947d5bfb98e1a4b8b39d8c500c7b94dc` |
| fixed-script.json | `8ac2edd1df05e351b2c899d2ff478fb15035e95e521aa81204986c49fd7342de` |
| samples.json | `a4cd286396b1ad76c909886cf5317d6ae4c31db5e424b353466fdb9eebf16056` |

## 实测边界

- 为避免上一个样本的模型工作、来源缓存和来源开关污染下一个样本，快速模式按顶层目录批次创建独立 schema 和 mock。混放及同父目录样本共用该批次，按成员路径逐张放行。每个模式都读取同一份完整合成外部目录。
- 正确值能在旧自动层得到，但扫描层未按目标规则写好时，单独列扫描差异。legacy-one 的自动层标题为“去靛雾”，旧扫描证据仍为“È¥µåÎí”，因此未把正确解码文字算作错写。
- editions-model-quartet 四个版本各有五首。程序轮得到“候选过多”；脚本轮进入模型识别并读四次版详情，最终只连到发行组，缺组首发年。子任务 5 可直接用该行核对直接认组的路径。
- name-only-gap 在两个模式下均认到正确版本，脚本轮另写纠正后的曲名。named-bonus 在现有扫描下拆成三张，按同一预期样本合计三张的工作；mixed-a、mixed-b 各自形成一张，共用扫描和目录缓存。
- 脚本使用当前项的可见 ID、选择候选及当前工具协议。当前 mock 已有解析当前项和读取工具结果的回包，固定套件直接复用 `script.go`；旧度量入口继续使用 `oracle.go`。后续子任务 8、9 更新相应模型协议时同步更新脚本。

## 定向验证记录

以下相对路径从仓库根目录起算。集成测试由 `sg docker -c` 在本机已有的 docker 组中执行原命令。遇到其他子任务持有集成锁时，脚本退出 1；这些尝试没有启动测试库，释放锁后重试。

| 结果 | 命令或范围 |
| --- | --- |
| 通过 | `python3 -m json.tool backend/internal/app/testdata/curation_regression/fixed.json >/dev/null` |
| 通过 | `python3 -m json.tool backend/internal/app/testdata/curation_regression/fixed-script.json >/dev/null` |
| 通过 | `(cd backend && mise exec -- go test ./internal/app -run '^TestCurationLiveGapSampleCoverage$')` |
| 通过 | `(cd backend && mise exec -- go test ./internal/upstreammock -run '^(TestMockMusicBrainzRoutes\|TestMockSourceAndSearchRoutes)$')` |
| 通过 | `(cd backend && mise exec -- go test ./internal/upstreammock -run '^(TestMockModelRoutes\|TestOracleEvidenceScope\|TestOracleLookupReadsBeforeSubmitting)$')` |
| 通过 | `(cd backend && mise exec -- go test ./internal/app -run '^(TestMetricsSafeOutcomesAndAttribution\|TestMetricsSuggestionValues)$')` |
| 通过 | `ROOMUSIC_CURATION_METRICS_SUITE=fixed-capture ROOMUSIC_CURATION_METRICS_SAMPLES=picard-01,fingerprint-only,mixed-a,mixed-b,one-char,unlisted-01,spoken-podcast ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh` |
| 通过 | 本文首轮采集命令，两个模式各 28 张 |
| 通过 | 空等诊断命令及完整时间线见同目录 `idle-gaps.md` |
| 通过 | `(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')` |
| 通过 | `git diff --check` |
| 未运行 | 测试代理的独立新断言、全量后端测试、旧 142 张整库、真实模型或真实 MusicBrainz；这些不属于本轮实施验证 |

## 第二次冷启动

第二次完整命令于 2026-11-26 16:52 UTC 取得集成锁并启动。通过，整条命令用时 **3 分 07.800 秒**。两次共比较 56 个模式与样本组合，逐张结论、查询接口及次数、实际外部编号、写入、错认、缺写和扫描差异一致。纯程序仍为认对 12、认错 0、每张 2.57 次请求，墙钟 66.970 秒（−3.431 秒）；程序加脚本仍为认对 15、认错 0、每张 12.25 次请求，墙钟 112.077 秒（−13.799 秒）。

```sh
time env ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/second.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

| 样本 | 第二次程序用时（毫秒） | 较首次 | 第二次脚本用时（毫秒） | 较首次 |
| --- | ---: | ---: | ---: | ---: |
| editions-model-quartet | 489 | -125 | 1202 | -285 |
| embedded-artist | 519 | -337 | 1438 | -3323 |
| one-char | 716 | +54 | 1473 | -89 |
| romanized | 502 | -461 | 1409 | -134 |
| legacy-one | 751 | -234 | 899 | +56 |
| legacy-many | 483 | -179 | 3605 | -103 |
| sampler-five | 948 | -478 | 1110 | +4 |
| disc-folders | 687 | -96 | 1603 | +806 |
| named-bonus | 1551 | -275 | 15093 | +2704 |
| filename-only | 509 | -132 | 2238 | +156 |
| name-only-gap | 828 | +17 | 770 | -90 |
| mixed-contiguous | 1317 | +501 | 796 | -81 |
| mixed-a | 716 | -159 | 848 | -1397 |
| mixed-b | 679 | -74 | 751 | -1159 |
| amp-comma | 625 | +120 | 2494 | -1403 |
| picard-01 | 688 | -33 | 826 | -43 |
| cue-01 | 741 | +40 | 870 | -174 |
| identifier-01 | 694 | -70 | 850 | +37 |
| catalog-label | 707 | -459 | 809 | -13 |
| isrc-only | 704 | -11 | 815 | -38 |
| fingerprint-only | 1025 | -4 | 1108 | -106 |
| id-conflict | 734 | +250 | 2288 | -154 |
| unlisted-01 | 731 | +235 | 1551 | -1 |
| spoken-podcast | 525 | +27 | 2098 | -18 |
| unknown-disc-a | 509 | -13 | 1687 | -64 |
| unknown-disc-b | 408 | -61 | 1825 | -67 |
| missing-position | 518 | -78 | 2084 | +4 |
| field-tie | 489 | -30 | 2343 | +263 |

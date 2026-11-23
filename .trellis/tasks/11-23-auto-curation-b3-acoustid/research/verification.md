# B3 规划时的核实记录（2026-11-23）

## 1. 指纹工具的来源

| 方案 | 核实结果 |
| --- | --- |
| 固定的静态 ffmpeg 自带 chromaprint 复用器 | 没有：`ffmpeg -muxers` 里没有 `chromaprint`，`-buildconf` 里没有 `--enable-chromaprint`（mwader/static-ffmpeg 7.1.1，摘要 `11a44711684c…`） |
| Alpine 3.23 的 `chromaprint` 包 | 带 `/usr/bin/fpcalc`（1.6.0-r0），依赖 `libavcodec.so.62` 等动态库；`apk add` 共装 65 个包，`/usr/lib` 约 104.5 MiB。生产镜像现在 251 MB |
| 纯 Go 的现成实现 | 没找到（只有 cgo 绑定与调用 `fpcalc` 的封装）；Rust 有位精确的移植 chromaprint-next，说明移植可行，但 chromaprint 是 LGPL 2.1 |
| 上游官方静态构建 | `acoustid/chromaprint` 的 GitHub 发布带 `chromaprint-fpcalc-<版本>-linux-{x86_64,arm64}.tar.gz`；`file` 显示 statically linked，5,401,536 字节 |

固定的版本（v1.6.2，发布于 2026-10-15）：

| 文件 | sha256 |
| --- | --- |
| `chromaprint-fpcalc-1.6.2-linux-x86_64.tar.gz` | `c968ad4a0821592a79b158fb2e1f46cbe0a22a0a1dbc0743c67d68e75d15d2bb` |
| `chromaprint-fpcalc-1.6.2-linux-arm64.tar.gz` | `3aa1498126ed272d805bc9d081c77965c9e1e1a0891bb112c13b56ef2ad47bad` |

下载地址：`https://github.com/acoustid/chromaprint/releases/download/v1.6.2/<文件名>`；压缩包里是 `chromaprint-fpcalc-1.6.2-linux-<架构>/fpcalc`。
`fpcalc -version` 输出 `fpcalc version 1.6.2 (FFmpeg Lavc62.11.100 Lavf62.3.100 SwR6.1.100)`。

## 2. 管道输入

`fpcalc -h`：`-format NAME`、`-rate NUM`、`-channels NUM`（原始输入）、`-length SECS`（默认 120）、`-algorithm NUM`（默认 2）、`-raw`、`-json`、`-plain`；文件名 `-` 是标准输入。

用合成的 60 秒粉红噪声（FLAC，立体声）比较：

- `fpcalc n.flac` 与 `ffmpeg -i n.flac -t 120 -ac 1 -ar 11025 -f s16le - | fpcalc -format s16le -rate 11025 -channels 1 -` 的压缩指纹长度相同（1343 字符），内容不完全相同；
- 原始指纹逐位比较：463 个 32 位整数，位差率 0.00047（重采样器不同带来的差别，AcoustID 的检索本来就容忍这个量级）；
- 解码加计算 60 秒音频，实际耗时 0.032 s。

结论：解码与取源照音频分析走我们的 ffmpeg，`fpcalc` 只读标准输入的 PCM。

## 3. AcoustID 网络服务

来源：`https://acoustid.org/webservice`、`https://acoustid.org/faq`、`https://acoustid.org/license`，以及服务端源码
`github.com/acoustid/acoustid-server` 的 `acoustid/api/errors.py` 与 `acoustid/api/v2/__init__.py`。

- 限速原文：“Do not make more than 3 requests per second.”；没有并发规定。
- 条款：“This service is provided for free for non-commercial use only.”，商业用途走 acoustid.biz；预计流量大时先联系。
  每个应用要注册（`/new-application`）拿 `client` 密钥；`user` 密钥只用于提交，不用。
- 数据许可：指纹与元数据 CC BY-SA 3.0，MusicBrainz 对应关系 CC0。
- 查询：`https://api.acoustid.org/v2/lookup`，参数 `client`、`duration`（整数秒，1..32767）、`fingerprint`、`meta`、`format`；POST 时可 `Content-Encoding: gzip`。
  服务端按时长在 `maxdurationdiff`（默认值由常量给出）内找指纹，所以 `duration` 要用整首的时长，而不是只解码的 120 秒。
- `meta=recordings+releaseids` 时：`results[] = {id, score, recordings:[{id, title, duration, artists:[{id,name}], releases:[{id}]}]}`；
  没有元数据的结果只有 `id` 与 `score`。`compress` 合并重复的录音与发行。
- 错误：`{"status":"error","error":{"code":N,"message":"…"}}`。码表（errors.py）：1 格式、2 缺参数、3 指纹无效、**4 密钥无效**、5 内部错误（500）、
  7 UUID 无效、8 时长无效、12 不允许、**13 服务不可用（503）**、**14 请求太多（429）**、16 只允许 HTTPS、**17 未知应用**、18 指纹不存在（404）、19 请求太大（413）。

## 4. 仓库现状

- `track_identifiers.kind` 已含 `acoustid`（迁移 0059）；`tracks.content_fingerprint`（迁移 0013）。
- 音频分析：`backend/internal/playback/analysis_worker.go`（取源 `openSource`：允许的根、`openScannedSource` 核对大小与修改时间、`sourceAudioPath` 处理 CUE），
  `analysis_dsd.go`（DSD 先转 PCM 再交给 ffmpeg 的 `pipe:0`），`analysis/analysis.go`（ffmpeg 以 `-protocol_whitelist file,pipe -i file:/dev/fd/3` 读打开的文件句柄（可定位），`-ss/-t` 取 CUE 区间）。
- 源的设置：`curation_source_settings(source, enabled, updated_by, updated_at)`；`curation.Sources.Check` 依次判断总开关、开启者、源开关、暂停、扫描。
- 密钥：`backend/internal/steward/secret`，AES-GCM，按（用户 ID、用途）绑定；实例密钥文件 `<数据目录>/steward/instance.key`。
- 匹配：`backend/internal/curation/program/match_lookup.go`（`Match`、`matchAlbum`、`matchSingle`、`sampleISRCs`、`isrcReleaseList`、`fullCandidates`），
  `match_album.go`（`comparison.passes`），`match_single.go`（`DecideSingle`），`pass.go`（`needsWork`）。
- 发布：`.github/workflows/release.yml` 分别在 amd64 与 arm64 上构建；CI 只构建 amd64。

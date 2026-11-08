# ⑩ 格式扩展测试记录（2026-11-08）

## 本轮边界

只补充测试、合成构造函数、APE 素材与测试脚本，不修改实现代码、不提交。已有实现、合同和 API 格式表样本的未提交改动保持原样。

## 覆盖

- `parser/lossless_test.go`、`parsertest/lossless.go`：APE 新旧头、ID3v2 前缀；WavPack 标准/自定义采样率、多声道、未知总样本、浮点/混合/DSD；AIFF/AIFC 映射、ID3 与文本兜底；APEv2 键改名、多值、封面、畸形标签整段忽略；损坏输入；四种指纹改标签不变、改音频改变、APE 零 MD5 回退、AIFF SSND 偏移。
- WavPack DSD 合成块：基础率 44100、倍率 8、打包样本数 352800，期望原生采样率 2822400、位深 1、双声道、时长 1 秒。
- `parser/lossless_ffmpeg_test.go`：运行时生成 WV 16/24 位与浮点、AIFF 24 位和 AIFC sowt；核对参数、标签、时长；另读取固定 APE 核对 16/44100/2/0.25 与非空指纹。
- `library/scan_rules_test.go`：四种新扩展名受支持，不再落到 unsupported；TAK 只进入诊断候选。
- `derivation/lossless_test.go`、`decoders_unix_test.go`：固定参数数组按输入选择，音频解码器名解析，清单失败只保留已自检的 ALAC 与安全日志。
- `derivation/key_test.go`：ALAC 固定旧键、策略参与键；已有版本事实补 `Policy`，真实转换任务补 `Input`。
- `derivation/lossless_ffmpeg_test.go`：WV 16/24、AIFF 24、APE 真实转换，源/FLAC 解码 PCM MD5 一致，STREAMINFO 参数与样本数正确、缓存命中。
- `playback/formats_test.go`、`sniff_test.go`：11 项格式表、按解码器筛选、三种新容器与错配。
- `playback/lossless_integration_test.go`：五种派生组合 × 位深 0/16/24/32；前三种放行并完整传递 Input/Policy，32 位在路径核对之前拒绝，预检和音频入口都不进入派生。
- `analysis/ffmpeg_integration_test.go`：WV、AIFF 按内容探测并完成真实分析。
- `app/playback_derivation_integration_test.go`：已有格式表断言从 7 改为 11；API 样本与现有新格式表样本一致。
- 前端三个 Vitest 文件：新格式质量圆点、短名、仅服务端行出现派生标记、七个目录编码名、后端格式表样本解码。
- `scripts/playback-e2e.sh` 与 `frontend/e2e/real/playback.real.ts`：只在临时媒体生成 WV 24 位，加入现有浏览器派生解码/拖动/自然结束用例；公共曲目数同步为 14。本轮没有运行浏览器用例。

## APE 素材

- 路径：`backend/internal/playback/derivation/testdata/ape-16-44100.ape`。
- 大小：3468 字节，低于 16 KiB。
- SHA-256：`877679256060ae2bfb2eec861bd3125ebd3591596e7582b5d1b761b75a971475`。
- 生成入口：`scripts/gen-playback-fixtures.sh --ape-only`，已真实执行成功；默认完整生成入口也包含 APE。
- 固定 ffmpeg 7.1.1 生成 0.25 秒、16 位、44.1 kHz、双声道 WAV；固定摘要 `python@sha256:6a0e06f5ba0b373d051aabd3fffb9b873f27f9a2a3a14bcbe37dee6f0e3bbc70` 容器内安装 `monkeys-audio` / `libmac10` 10.16-dmo1，以 `mac input.wav output.ape -c2000` 编码。
- 两个 Debian 包的 SHA-256 写死在脚本并在安装前核对。下载沿用 shell 的 HTTP(S)_PROXY，不打印代理值；容器使用默认 bridge，未创建网络。编码后检查大小，再复制到目标路径。
- Alpine 仓库查询未找到所举包名；实际找到并验证的是同一 Monkey's Audio 编码器的 Debian 包，没有切换编码方案。

## ALAC 旧缓存键

通过 `git show HEAD:backend/internal/playback/derivation/key.go` 核对旧字段顺序、固定策略与 `%d:%s\n` 长度前缀；没有使用当前实现生成期望值。
测试以现有 `sampleVersion()` 和 `ffmpeg version 7.1.1` 固定断言：

```text
f89f5c3f5f2ca5843932ac0fcfe5d631c4f5d564b454575b1d87625e204634c4
```

## 实际验证

以下后端命令均在 `backend/` 中执行；真实 ffmpeg 与 PostgreSQL 用例均为 **PASS，不是 SKIP**。

```bash
go test ./internal/library/parser/ -run 'Test(APE|WavPack|AIFF|APEv2|Fingerprint)' -count=1
go test ./internal/library/ -run 'Test(Supported|Candidate)Extension|TestScannerOnlyDiagnosesKnownUnsupportedAudioExtensions' -count=1
go test -race ./internal/playback/... -run 'Test(Format|Derived|Key|DeriveKey|Convert|Decoder|Sniff|Container|BitDepth)' -count=1
ROOMUSIC_TEST_FFMPEG="$(../scripts/ffmpeg-pinned.sh)" go test -v ./internal/library/parser/ ./internal/playback/derivation/ ./internal/playback/analysis/ -run 'TestFFmpeg' -count=1
```

全部相关测试 PASS。扫描命令额外包含已有测试名，播放命令额外包含 `DeriveKey`，避免计划中的筛选表达式漏掉它们。播放命令中的 analysis/httpapi 包没有匹配用例（不是测试 SKIP）；分析的真实用例由后续 `TestFFmpeg` 命令覆盖。

PostgreSQL 严格按用户指定方式取空闲端口、随机 `integration_$RANDOM` 数据库、固定测试账号，使用 `compose.test.yaml`、`--env-file /dev/null`、项目 `roomusic-test-scan-m`；同时设置固定 ffmpeg 与 PostgreSQL 客户端包装路径。没有使用 `scripts/test-integration.sh`。

```bash
go test -v ./internal/app/ -run 'TestPostgreSQL(APIResponseSamples|PlaybackFormatsAddTheDerivedFormatWhenDerivationIsEnabled)$' -count=1
go test -race -v ./internal/playback/ -run '^TestPostgreSQLPlaybackBitDepthGateAndLosslessInputFacts$' -count=1
```

首条的 API 样本与 11 项格式表两项 PASS；第二条的 20 个位深子例全部 PASS。分两批独立启动同一专用项目，两批均通过 EXIT trap 执行 `down -v`。最终查询该项目的容器、数据卷与网络，均为空。

前端：

```bash
cd frontend
npx vitest run src/features/player/components/signal-path src/features/catalog/labels src/features/player/api/decoders
npx tsc --noEmit
npx eslint src/features/player/components/signal-path.test.tsx src/features/catalog/labels.test.ts src/features/player/api/decoders.test.ts
npx eslint e2e/real/playback.real.ts src/features/player/components/signal-path.test.tsx
```

Vitest 三个文件共 95 项 PASS；定向 ESLint PASS。首次类型检查发现新测试使用了 Testing Library 不支持的 `getByRole(..., { exact: true })`，只修正测试选项后类型检查 PASS；不是实现问题。

脚本 `bash -n scripts/gen-playback-fixtures.sh`、`bash -n scripts/playback-e2e.sh` 与 `git diff --check` 均 PASS。

## 实现问题与保留项

本轮实际验证未发现实现问题。静态审查发现计划中两处测试筛选遗漏（扫描已有测试名、`TestDeriveKey`），实际命令已补上，未改动实施计划原文。未运行全量测试，原因是上述最小集已经覆盖本轮受影响行为；未运行 `scripts/playback-e2e.sh`，按用户要求留给主会话合入前统一安排。未访问生产环境，未 commit、push、rebase、stash 或 reset。

# Squeezelite 发行版与 ROOMusic 兼容性

首轮矩阵中，1468、1476 在单台默认缓冲下均停在 352.8 kHz 起播，编组套件通过。1517、1541 和 1613 两项均通过。piCorePlayer 11.1.0 的 32 位和 64 位镜像实际搭载 1524；单台通过，编组首次无漂移超限，单行复测通过。

## 版本来源

2026-11-25 核对。每个提交链接指向参与构建的 `squeezelite.h`。旧版镜像从完整 Git SHA 下载归档，构建时检查 `MICRO_VERSION`。使用生产 Dockerfile 的 Alpine 基础镜像、编解码选项及 APK 生成方式；1613 对照直接走生产 Dockerfile。运行架构为 Linux amd64，设备输出为标准输出，每个编组使用同一版本的两台播放器。

| 来源 | 实际发行版本 | 上游固定提交 | 发行依据 |
| --- | --- | --- | --- |
| piCorePlayer 9.0.0 | v2.0.0-1468-pCP | [df4df96](https://github.com/ralph-irving/squeezelite/blob/df4df96fefd316ab51d455cf5272cdeb13317cb3/squeezelite.h#L29) | [发布说明](https://docs.picoreplayer.org/releases/pcp900/) · [64 位镜像](https://repo.pcplayer.org/insitu/piCorePlayer9.0.0/piCorePlayer9.0.0-64Bit.img.xz) |
| piCorePlayer 10.0.0 | v2.0.0-1476-pCP | [7878fad](https://github.com/ralph-irving/squeezelite/blob/7878fada339edebb72904da54f28350b44021510/squeezelite.h#L29) | [发布说明](https://docs.picoreplayer.org/releases/pcp1000/) · [64 位镜像](https://repo.pcplayer.org/insitu/piCorePlayer10.0.0/piCorePlayer10.0.0-64Bit.img.xz) |
| piCorePlayer 11.1.0 | 2.0.0-1524-pCP | [db51a7b](https://github.com/ralph-irving/squeezelite/blob/db51a7b16934f41b72437394bf8114c3a85e0a91/squeezelite.h#L29) | [发布说明写 1542](https://docs.picoreplayer.org/releases/pcp1110/) · [32 位镜像](https://repo.pcplayer.org/insitu/piCorePlayer11.1.0/piCorePlayer11.1.0.img.xz) · [64 位镜像](https://repo.pcplayer.org/insitu/piCorePlayer11.1.0/piCorePlayer11.1.0-64Bit.img.xz) |
| moOde 10.3.7（r1037） | 2.0.0-1541+git20250609.72e1fd8-1moode1 | [72e1fd8](https://github.com/ralph-irving/squeezelite/blob/72e1fd8abfa9b2f8e9636f033247526920878718/squeezelite.h#L29) | [最新正式版](https://github.com/moode-player/moode/releases/tag/r1037) · [制镜清单第 30 行](https://github.com/moode-player/imgbuild/blob/aa95d6838dfefe65d47d2adabec7ef3bccef9b82/moode-cfg/stage3_01-moode-install_01-packages#L30) · [基于 Debian 源包加入 RPI/GPIO 选项的配方](https://github.com/moode-player/pkgbuild/blob/227cf31e44aaa117b87b419f9b3ebe0df2da8997/packages/squeezelite/build.sh) |
| Debian 13 stable（trixie） | 源码包 2.0.0-1517+git20241227.262994a-1，amd64/arm64/armhf 二进制包后缀 +b1 | [262994a](https://github.com/ralph-irving/squeezelite/blob/262994a989dc29ce3be4390c57c6a43373dfdca2/squeezelite.h#L29) | [Debian 包及各架构版本](https://packages.debian.org/trixie/squeezelite) |
| ROOMusic 生产边车 | 2.0.0-1613 | [b02a9ac](https://github.com/ralph-irving/squeezelite/blob/b02a9ac77794c9914fa2b70caa98010e9f23cc2d/squeezelite.h#L29) | [固定提交与下载校验和](../../../../../../deploy/production/squeezelite.Dockerfile) |

对 piCorePlayer 的核对读了官方镜像第二分区的 `/tce/optional/pcp-squeezelite.tcz`，从中提取 `usr/local/bin/squeezelite`，检查 ELF 内嵌帮助首行和上报给服务器的 `Firmware=` 字符串。11.1.0 的 32 位和 64 位包都标为 1524。上游在 `72e1fd8` 定义 1541，下一个改变 `MICRO_VERSION` 的提交定义 1555。矩阵采用镜像内实际版本。 [上游 1541 → 1555 变更](https://github.com/ralph-irving/squeezelite/commit/1e8fb181366467b6c4046119eedb821e4a5f9adb)

| 官方镜像 | 下载文件 SHA-256 | 镜像内 pcp-squeezelite.tcz 的 SHA-256 |
| --- | --- | --- |
| 9.0.0 64 位 | `cd01e71d3549a6ea108ae4798d5fcc174ff1d57f902d6d0e8f147ff170d4a4a5` | — |
| 10.0.0 64 位 | `51145a74e5c7b9c6d439f3c72e5b2161edbb99f9c77251b708a36909b90f5e3d` | — |
| 11.1.0 32 位 | `c29e81e37b4631bf18ab27f82dd1e5bec7da2bc35aabc6985b8189b757bc19b5` | `8d4b981dce803979a5ee748b97f8859b9e59606a86e023f98c6be9f1ee938b8f` |
| 11.1.0 64 位 | `89c266b26cab2ab4f6a623e49491b6b511c154edd65ff03c191b5f9c71eec8d9` | `05960a974d1493782b5bd8a8765125d03df8cdfdbab01d62d79ec9283603a25c` |

## 测试条件

`scripts/test-slimproto-versions.sh` 逐行串行运行两个现有套件。单台声明最高 705600 Hz，保留默认输出缓冲。编组覆盖无漂移、暂停与继续、跳转、同格式和跨采样率换曲、网络抖动、掉线重连、服务器重启和漂移恢复。无漂移和有漂移的持续测量各 60 秒，成员设 +300 ppm，使约 33 秒累积 10 ms；每轮同步阈值沿用 p95 ≤ 10 ms、峰值 ≤ 15 ms。默认独立编组入口仍为 600 秒和 +100 ppm。

## 逐版本结果

完整矩阵本轮目录为 `temp/slimproto-versions/20261125T071101Z.onWIGwCt/`。表中 p95、峰值及修正次数来自成员 +300 ppm 的 60 秒窗口，取两台实录定时脉冲配对。修正次数按主设备 / 成员排列。

| 来源 | MICRO_VERSION | 单台套件 | 编组套件 | 漂移 p95 / 峰值（ms） | strm a |
| --- | ---: | --- | --- | ---: | ---: |
| piCorePlayer 9.0.0 | 1468 | 修正前 352.8 kHz 起播超时，修正后单台通过 | 通过 | 9.320 / 10.220 | 1 / 0 |
| piCorePlayer 10.0.0 | 1476 | 修正前 352.8 kHz 起播超时，修正后单台通过 | 通过 | 9.228 / 9.931 | 1 / 0 |
| piCorePlayer 11.1.0 | 1524 | 通过 | 首轮失败，单行复测通过 | 8.984 / 9.883（复测） | 1 / 0（复测） |
| moOde 10.3.7 | 1541 | 通过 | 通过 | 9.092 / 9.992 | 1 / 0 |
| Debian 13 | 1517 | 通过 | 通过 | 9.571 / 10.470 | 1 / 0 |
| ROOMusic | 1613 | 通过 | 通过 | 9.215 / 10.115 | 1 / 0 |

## 1524 无漂移时的同步偏差

`1524-sync.log` 中两台收到定时起播命令仅相隔 8 微秒，实际输出线程开始相隔 0.733 ms。60 对脉冲均能配对，p50 为 1.484 ms，p95 为 11.551 ms，峰值为 44.251 ms，超过 10/15 ms 门槛。主设备第 33 个一秒间隔结束于首脉冲后 33.052 秒，此前该设备已跳过 11 ms 源音频，名义上应为 32.989 秒；差值约 +63 ms，表明这一侧实际出声或计时出现过正向延迟。第 33、34 秒成员分别领先 44.251、34.251 ms；稍后第 43 秒成员落后 19.668 ms。主设备依次执行 485、441、1896 帧修正（11、10、43 ms），成员随后执行 617、882 帧修正（14、20 ms）。该轮先后换边追赶，首轮无漂移测量就停止，后续暂停、换曲、漂移等场景均未执行。

ROOMusic 每秒按各设备上报位置推算表观起播时刻，对落后 ≥10 ms 的一方发送整毫秒跳帧命令。日志确认了两边的修正及脉冲实际偏差；两台时钟均设为 0 ppm，夹具只保留末尾 8 条时钟事件，无法由该轮还原最初时序偏差的起因。1524 和 1613 的上游 `slimproto.c` 和 `output.c` 差异只有版权年份，单次超限不足以归为某项旧版协议差异。 [ROOMusic 比较及修正](../../../../../../backend/internal/devices/slimproto/group_timing.go) · [上游两版差异](https://github.com/ralph-irving/squeezelite/compare/db51a7b16934f41b72437394bf8114c3a85e0a91...b02a9ac77794c9914fa2b70caa98010e9f23cc2d)

在 `temp/slimproto-versions/20261125T073836Z.iEnr0ywg/1524-sync.log` 中，同样的 60 秒无漂移复测配对 60 个脉冲，p95 和峰值均为 0.355 ms，两台均未收到修正。其后各阶段全部通过。漂移阶段 61 对脉冲，p95 8.984 ms、峰值 9.883 ms，主设备修正一次，随后三对中最大偏差 0.783 ms。

## 高采样率起播

1468 和 1476 的单台套件均成功完成发现、认领、44.1/96/192 kHz 播放、限频、换曲、暂停继续与断线恢复。352800 Hz 用例抓到 FLAC STREAMINFO（双声道、24 bit、块长 4096），HTTP 200 后播放器确认 STMc，25 秒内没有这条流的 STMs 或有声 PCM（断言要求 ≥9.5 秒）；同一循环的 705600 Hz 后续用例未执行。

修正前，ROOMusic 对采样率 ≥176400 Hz 的流发 `output_threshold=20`。旧播放器在 `output.c` 等到输出缓冲大于 `20 × rate / 10` 帧才播放。在 352800 Hz 需要超过 705600 帧，而默认 `OUTPUTBUF_SIZE = 44100 × 8 × 10` 字节，只能装 441000 个 8 字节帧。在 192000 Hz 门槛为 384000 帧，仍可起播。上游从 1479 起在 `decode_newstream` 依据缓冲实际容量下调门槛。 [ROOMusic 开流参数](../../../../../../backend/internal/devices/slimproto/protocol.go#L134) · [旧版输出条件](https://github.com/ralph-irving/squeezelite/blob/df4df96fefd316ab51d455cf5272cdeb13317cb3/output.c#L63) · [默认容量](https://github.com/ralph-irving/squeezelite/blob/df4df96fefd316ab51d455cf5272cdeb13317cb3/squeezelite.h#L266) · [上游 1479 修正](https://github.com/ralph-irving/squeezelite/commit/1043a18f45a81357ff9475b969396c67fa88b53d)

## 运行入口及记录

传入 `ROOMUSIC_SLIMPROTO_PLAYER_IMAGE` 后，两套入口都直接使用该镜像。编组抖动用到的 `tc` 若尚未安装，只在本次成员容器内通过 `apk add --no-cache iproute2-tc` 安装。默认入口仍自行构建和清理镜像。矩阵中的 1613 通过默认入口，其他各行通过外部镜像入口。

单台套件在第一个失败用例处退出；矩阵保留该版本日志，继续运行同版本编组及后续各行。每轮日志位于 `temp/slimproto-versions/<UTC 时间戳>.<随机后缀>/`，保留 `results.tsv`、`<版本>-single.log`、`<版本>-sync.log`，自建上游镜像还保留 `<版本>-build.log`。可用 `sg docker -c 'scripts/test-slimproto-versions.sh 1468'` 单独复查一行。

## 执行验证

- `for script in scripts/test-slimproto{,-sync,-versions}.sh; do bash -n "$script" || exit; done` 与 `PYTHONPYCACHEPREFIX="$PWD/temp/pycache-versions" python3 -m py_compile scripts/slimproto/sync-playback.py` 成功。更新矩阵的清单读取和编组失败诊断后，分别再次运行相应脚本的 `bash -n`，均成功。
- 单台的 `compose.yaml + compose.squeezelite.yaml + sidecar.yaml` 与编组的 `compose.yaml + sync.yaml` 分别用 `sg docker -c` 执行 `docker compose --env-file /dev/null -p <独立测试项目名> ... config --quiet`，均退出 0。
- 首次运行 `sg docker -c scripts/test-slimproto-versions.sh` 在 1468 行结束后退出 2。该次脚本在运行中更新过版本表，外层 Bash 在后续行报告语法错误，日志在 `temp/slimproto-versions/20261125T070452Z.BwVIY6Wo/`。该行已发现 352.8 kHz 单台超时且编组通过。
- 修正后的同一命令完整执行六行（12 次套件），以 1 返回。1468、1476 单台和 1524 首轮编组各有一个失败，其余九项通过。每次套件只清理本次独立项目及自建镜像，保留上述日志目录。
- 单行复核 `sg docker -c 'scripts/test-slimproto-versions.sh 1524'` 退出 0，单台和编组均通过。首轮时序异常的较低层原因尚未确定。

## 门槛修正后复测

2026-11-25（UTC）。ROOMusic 在采样率 ≥176400 Hz 时按整数除法发送 `output_threshold=min(20, 3528000/rate)`。本轮用默认输出缓冲复测 1468、1476 和 1613 的单台套件。编组套件仅覆盖 44.1/48 kHz，本轮未重跑，逐版本表沿用原矩阵的编组结果。

在仓库根目录执行以下命令，每条均退出 0。

```bash
(cd backend && mise exec -- go test ./internal/devices/slimproto/...)
sg docker -c 'docker build --progress=plain -t roomusic-test-slimproto-retest-swtghs7g:1468 --build-arg SQUEEZELITE_COMMIT=df4df96fefd316ab51d455cf5272cdeb13317cb3 --build-arg SQUEEZELITE_MICRO=1468 -f scripts/slimproto/squeezelite-ref.Dockerfile scripts/slimproto'
sg docker -c 'ROOMUSIC_SLIMPROTO_PLAYER_IMAGE=roomusic-test-slimproto-retest-swtghs7g:1468 scripts/test-slimproto.sh'
sg docker -c 'docker build --progress=plain -t roomusic-test-slimproto-retest-swtghs7g:1476 --build-arg SQUEEZELITE_COMMIT=7878fada339edebb72904da54f28350b44021510 --build-arg SQUEEZELITE_MICRO=1476 -f scripts/slimproto/squeezelite-ref.Dockerfile scripts/slimproto'
sg docker -c 'ROOMUSIC_SLIMPROTO_PLAYER_IMAGE=roomusic-test-slimproto-retest-swtghs7g:1476 scripts/test-slimproto.sh'
sg docker -c scripts/test-slimproto.sh
sg docker -c 'docker image rm roomusic-test-slimproto-retest-swtghs7g:1468 roomusic-test-slimproto-retest-swtghs7g:1476'
```

三版均通过单台套件。两个高采样率用例均抓到 24 bit FLAC、最大块长 4096 帧，有声 PCM 时长达到套件要求的 9.5 秒。

| Squeezelite | 352800 Hz 有声 PCM | 705600 Hz 有声 PCM |
| --- | ---: | ---: |
| 1468 | 9.522 秒 | 9.595 秒 |
| 1476 | 9.568 秒 | 9.555 秒 |
| 1613（默认入口） | 9.542 秒 | 9.516 秒 |

构建、单台测试与清理日志保留在 `temp/slimproto-retest/20261125T074749Z.sWtGhs7G/`。两个自建镜像已删除，默认入口也已清理其临时镜像。

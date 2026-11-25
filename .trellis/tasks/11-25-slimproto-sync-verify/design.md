# 设计

## 可听性脚本

- 复用 `scripts/slimproto/compose.yaml` 和 `sync.yaml`、`sync.Dockerfile`、`playback.py` 的建库与认领，以及 `sync-playback.py` 的 `Suite` 编组流程。Compose 项目名用 `roomusic-compose-test-slimproto-skip-<后缀>`。
- `sync-clock.py` 加一个可选开关（例如工作目录里有 `record` 文件时），把读到的原始 PCM 追加写入 `audio.raw`，与 `frames.bin` 对应。不开时行为不变。
- 信号：一首长曲。按时长分成三段：正弦（如 −12 dBFS、1 kHz 或 440 Hz）、和弦（几个基频加泛音）、宽带噪声或密集打击。段长不少于一个修正周期，按 +100 ppm 约 100 秒一次修正估算，必要时把成员 ppm 调大，让总时长可控。
  - 修正时机由服务器端决定，脚本不能指定，所以每段要足够长，保证段内至少修正一次。
- 定位修正点：输出和已知的源信号对齐。可以给源信号嵌一个不影响听感的位置标记，也可以直接对源做互相关。从这里得到修正点的输出帧号和源帧跳变。主设备同时刻的片段作对照。
- 导出：16 位或 24 位立体声 WAV，44.1 kHz。保存目录是 `temp/slimproto-skip/<UTC 时间戳>/`，不在 `mktemp` 工作目录里，cleanup 不删。文件名包含段名和修正序号。

## 镜像覆盖

- `test-slimproto.sh`、`test-slimproto-sync.sh`：传入环境变量（例如 `ROOMUSIC_SLIMPROTO_PLAYER_IMAGE`）时跳过构建和删除，直接用该镜像。`test-slimproto.sh` 的 `apk list` 版本检查只在自建镜像时做，或改成读 `squeezelite -?` 的版本行。
- 单台测试的 `-r` 声明了 705600。旧版本如果声明的上限或格式不同，测试的期望值按版本区分，或在矩阵报告里注明。不为通过而放宽现行版本的断言。

## 版本矩阵

- `scripts/slimproto/squeezelite-ref.Dockerfile`：ARG 为上游提交，构建方式同生产 Dockerfile（Alpine 3.23，`-DLINKALL -DNO_FAAD -DNO_MAD`）。旧源码在新 GCC 下编译不过时，只加编译选项（如 `-fcommon`、`-std=gnu17`），不改源码；改源码才能编译的版本记为构建失败。
- 矩阵脚本 `scripts/test-slimproto-versions.sh`：依次构建每个版本，跑两项测试，汇总通过或失败，最后删掉自建镜像。版本表写在脚本里，每项注明来源。

# 播放测试素材

合成的立体声正弦音，只用于测试，不含任何真实音乐。由 `scripts/gen-playback-fixtures.sh` 用固定摘要的 FFmpeg 容器生成
（容器没有网络，只挂载本目录；输出带 bitexact 标志，重复生成得到相同字节），合计不到 1 MB。运行时的 ffmpeg 由部署者安装；
渲染器的真实 ffmpeg 测试用 `scripts/ffmpeg-pinned.sh` 取出的同一个固定镜像里的 ffmpeg（设置 `ROOMUSIC_TEST_FFMPEG`）。
同一个脚本还生成渲染器的启动自检素材 `backend/internal/playback/render/ffmpeg/testdata/selftest.m4a`（0.25 秒 ALAC，两个声道频率不同，`moov` 在文件末尾）。

除 APE 外，以下文件的专辑是 `Playback Fixtures`，艺人是 `Fixture Artist`，曲名与音轨号如下。

| 文件 | 编码 | 位深 / 采样率 | 时长 | 说明 |
| --- | --- | --- | --- | --- |
| `flac-16-44100.flac` | FLAC | 16 / 44.1 kHz | 3 秒 | 直出，三组手工实播必测；带内嵌同步歌词（Vorbis 注释 `LYRICS`） |
| `flac-24-48000.flac` | FLAC | 24 / 48 kHz | 2 秒 | 直出，三组手工实播必测 |
| `alac-16-44100.m4a` | ALAC | 16 / 44.1 kHz | 2 秒 | 不在直出格式表中：经服务端渲染播放；`moov` 在 `mdat` 之前；带内嵌同步歌词（`©lyr`） |
| `alac-24-48000.m4a` | ALAC | 24 / 48 kHz | 1 秒 | 同上；无损渲染逐位保留源 PCM |
| `mp3-44100.mp3` | MP3（64 kbps） | — / 44.1 kHz | 2 秒 | ID3v2.3 标签 |
| `aac-44100.m4a` | AAC-LC（64 kbps） | — / 44.1 kHz | 2 秒 | |
| `wav-16-44100.wav` | PCM | 16 / 44.1 kHz | 1 秒 | 直出 |
| `wav-24-48000.wav` | PCM（WAVE_FORMAT_EXTENSIBLE） | 24 / 48 kHz | 0.5 秒 | 扫描记为 `wav_extensible`，不在直出格式表中，通过服务端渲染播放 |
| `vorbis-44100.ogg` | Ogg Vorbis | — / 44.1 kHz | 2 秒 | |
| `opus-48000.opus` | Opus（64 kbps） | — / 48 kHz | 2 秒 | |

`ape-16-44100.ape` 是 Monkey's Audio 生成的 0.25 秒、16 bit/44.1 kHz 双声道合成素材，保留给解析器测试；可用同一脚本的 `--ape-only` 重建。

## 内嵌歌词

只有 `flac-16-44100.flac` 与 `alac-16-44100.m4a` 带歌词，内容相同，由脚本里的 `LYRICS` 变量写入（ffmpeg 的 `-metadata lyrics=`：FLAC 写成 Vorbis 注释 `LYRICS`，
MP4 写成 `©lyr`）：两行元标签、三个时间点（0.00、0.80、1.60 秒，都在较短的 ALAC 的 2 秒之内），第二个时间点有英文与中文两行（双语对照，分成一组）。
歌词的真实服务用例（`frontend/e2e/real/playback.real.ts`）与解析器的素材事实测试（`playback_fixture_test.go` 的 `fixtureLyrics`）都按这份歌词核对，
改脚本里的 `LYRICS` 要同时改这两处。MP3 的 `USLT` 无法由 ffmpeg 写出，只在 Go 测试里构造。
`playback-e2e.sh` 把 `flac-16-44100.flac` 复制成 CUE 整轨镜像：它带着歌词，而它的虚拟轨必须没有歌词（整张镜像的歌词不能分给单条曲目）。

修改素材规格后重新运行脚本，再用 `cd backend && go test ./internal/library/parser/ ./internal/playback/ -run Fixture`
核对素材事实与直出格式表仍然一致。

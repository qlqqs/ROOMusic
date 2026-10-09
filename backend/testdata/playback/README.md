# 播放测试素材

合成的立体声正弦音，只用于测试，不含任何真实音乐。由 `scripts/gen-playback-fixtures.sh` 用固定摘要的 FFmpeg 容器生成
（容器没有网络，只挂载本目录；输出带 bitexact 标志，重复生成得到相同字节），合计不到 1 MB。运行时的 ffmpeg 由部署者安装；
派生核心的真实 ffmpeg 测试用 `scripts/ffmpeg-pinned.sh` 取出的同一个固定镜像里的 ffmpeg（设置 `ROOMUSIC_TEST_FFMPEG`）。
同一个脚本还生成派生核心的启动自检素材 `backend/internal/playback/derivation/selftest.m4a`（0.25 秒 ALAC，两个声道频率不同，`moov` 在文件末尾）。

所有文件的专辑是 `Playback Fixtures`，艺人是 `Fixture Artist`，曲名与音轨号如下。

| 文件 | 编码 | 位深 / 采样率 | 时长 | 说明 |
| --- | --- | --- | --- | --- |
| `flac-16-44100.flac` | FLAC | 16 / 44.1 kHz | 3 秒 | 直出，三组手工实播必测 |
| `flac-24-48000.flac` | FLAC | 24 / 48 kHz | 2 秒 | 直出，三组手工实播必测 |
| `alac-16-44100.m4a` | ALAC | 16 / 44.1 kHz | 2 秒 | 只有 Safari 能解码；Safari 实播通过前不在直出格式表中，标注“不能直接播放”；`moov` 在 `mdat` 之前 |
| `alac-24-48000.m4a` | ALAC | 24 / 48 kHz | 1 秒 | 同上 |
| `mp3-44100.mp3` | MP3（64 kbps） | — / 44.1 kHz | 2 秒 | ID3v2.3 标签 |
| `aac-44100.m4a` | AAC-LC（64 kbps） | — / 44.1 kHz | 2 秒 | |
| `wav-16-44100.wav` | PCM | 16 / 44.1 kHz | 1 秒 | 直出 |
| `wav-24-48000.wav` | PCM（WAVE_FORMAT_EXTENSIBLE） | 24 / 48 kHz | 0.5 秒 | 扫描记为 `wav_extensible`，不在直出格式表中：用来演示“不能直接播放”的标注 |
| `vorbis-44100.ogg` | Ogg Vorbis | — / 44.1 kHz | 2 秒 | |
| `opus-48000.opus` | Opus（64 kbps） | — / 48 kHz | 2 秒 | |

修改素材规格后重新运行脚本，再用 `cd backend && go test ./internal/library/parser/ ./internal/playback/ -run Fixture`
核对素材事实与直出格式表仍然一致。

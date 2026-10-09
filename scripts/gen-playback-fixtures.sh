#!/usr/bin/env bash
# 重新生成 backend/testdata/playback 下的合成播放素材：立体声正弦音，带专辑、艺人、曲名与音轨号标签。
# 素材已经提交到仓库；只有修改素材规格时才需要运行本脚本，运行后用
#   cd backend && go test ./internal/library/parser/ -run Fixture
# 核对素材与直出格式表没有脱节。
#
# 素材由按摘要固定的镜像里的 FFmpeg 生成：容器没有网络，只挂载输出目录（绝不挂载 music/）。镜像引用只在
# scripts/ffmpeg-pinned.sh 里定义一处，测试与 CI 用它取出同一个构建；运行时的 ffmpeg 由部署者安装，不由本脚本提供。
# 输出一侧的 -fflags +bitexact 去掉编码器版本、时间戳与随机流序号，同一镜像下重复运行得到相同字节。
#
# 除 backend/testdata/playback 的素材外，还会生成派生核心的启动自检素材 backend/internal/playback/derivation/selftest.m4a（go:embed）。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOMUSIC_PLAYBACK_FIXTURE_DIR:-$ROOT_DIR/backend/testdata/playback}"
SELFTEST_DIR="${ROOMUSIC_SELFTEST_FIXTURE_DIR:-$ROOT_DIR/backend/internal/playback/derivation}"
# mwader/static-ffmpeg 7.1.1（静态构建，含 libmp3lame、libvorbis、libopus）。升级镜像须重新核对素材并更新 ffmpeg-pinned.sh 里的摘要。
FFMPEG_IMAGE="$("$ROOT_DIR/scripts/ffmpeg-pinned.sh" --image)"
MAX_TOTAL_BYTES=1000000
MAX_SELFTEST_BYTES=32768

mkdir -p "$OUT_DIR" "$SELFTEST_DIR"

# run_ffmpeg_in <输出目录> <FFmpeg 参数...>：输出目录挂载为容器内的 /out。
run_ffmpeg_in() {
  local directory="$1"
  shift
  docker run --rm --network none -v "$directory:/out" -u "$(id -u):$(id -g)" "$FFMPEG_IMAGE" \
    -hide_banner -loglevel error -y "$@"
}

run_ffmpeg() {
  run_ffmpeg_in "$OUT_DIR" "$@"
}

# 两种源信号：440 Hz / 44.1 kHz 与 523.25 Hz / 48 kHz，对应真实库里最常见的两组采样率。
SINE_44100="sine=frequency=440:sample_rate=44100"
SINE_48000="sine=frequency=523.25:sample_rate=48000"

# generate <输出文件> <曲名> <音轨号> <源信号> <时长秒> <编码参数...>
generate() {
  local file="$1" title="$2" track="$3" source="$4" seconds="$5"
  shift 5
  # bitexact 必须放在输出一侧（输入之后），才会作用于封装器与编码器，去掉版本字符串、时间戳与 Ogg 的随机流序号。
  run_ffmpeg -f lavfi -i "$source" -t "$seconds" -ac 2 -fflags +bitexact -flags:a +bitexact "$@" -map_metadata -1 \
    -metadata album="Playback Fixtures" -metadata artist="Fixture Artist" -metadata album_artist="Fixture Artist" \
    -metadata title="$title" -metadata track="$track" "/out/$file"
}

# 时长按体积取舍：16 位与有损格式 2–3 秒，24 位无损与 WAV 更短，合计不超过 1 MB。
generate flac-16-44100.flac "FLAC 16-44.1" 1 "$SINE_44100" 3 -c:a flac -sample_fmt s16
generate flac-24-48000.flac "FLAC 24-48" 2 "$SINE_48000" 2 -c:a flac -sample_fmt s32 -bits_per_raw_sample 24
generate alac-16-44100.m4a "ALAC 16-44.1" 3 "$SINE_44100" 2 -c:a alac -sample_fmt s16p -movflags +faststart
generate alac-24-48000.m4a "ALAC 24-48" 4 "$SINE_48000" 1 -c:a alac -sample_fmt s32p -bits_per_raw_sample 24 -movflags +faststart
generate mp3-44100.mp3 "MP3" 5 "$SINE_44100" 2 -c:a libmp3lame -b:a 64k -id3v2_version 3
generate aac-44100.m4a "AAC-LC" 6 "$SINE_44100" 2 -c:a aac -b:a 64k -movflags +faststart
generate wav-16-44100.wav "WAV 16-44.1" 7 "$SINE_44100" 1 -c:a pcm_s16le
generate wav-24-48000.wav "WAV 24-48" 8 "$SINE_48000" 0.5 -c:a pcm_s24le
generate vorbis-44100.ogg "Vorbis" 9 "$SINE_44100" 2 -c:a libvorbis -q:a 2
generate opus-48000.opus "Opus" 10 "$SINE_48000" 2 -c:a libopus -b:a 64k

# 派生核心的启动自检素材：0.25 秒 16 bit/44.1 kHz 立体声 ALAC，两个声道是不同频率的正弦（能暴露声道错位），
# moov 在文件末尾（不加 faststart，证明 ffmpeg 能经文件描述符定位读取）。没有标签与封面。
run_ffmpeg_in "$SELFTEST_DIR" \
  -filter_complex "sine=frequency=440:sample_rate=44100:duration=0.25[l];sine=frequency=660:sample_rate=44100:duration=0.25[r];[l][r]amerge=inputs=2[a]" \
  -map "[a]" -fflags +bitexact -flags:a +bitexact -c:a alac -sample_fmt s16p -map_metadata -1 /out/selftest.m4a
selftest_size="$(stat -c %s "$SELFTEST_DIR/selftest.m4a")"
printf '%8d  %s\n' "$selftest_size" "selftest.m4a（派生核心自检素材）"
if [ "$selftest_size" -gt "$MAX_SELFTEST_BYTES" ]; then
  echo "自检素材 ${selftest_size} 字节，超过 ${MAX_SELFTEST_BYTES}；请缩短时长后重新生成" >&2
  exit 1
fi

total=0
for file in "$OUT_DIR"/*; do
  size="$(stat -c %s "$file")"
  total=$((total + size))
  printf '%8d  %s\n' "$size" "$(basename "$file")"
done
printf '%8d  合计\n' "$total"
if [ "$total" -gt "$MAX_TOTAL_BYTES" ]; then
  echo "素材合计 ${total} 字节，超过 ${MAX_TOTAL_BYTES}；请缩短时长后重新生成" >&2
  exit 1
fi

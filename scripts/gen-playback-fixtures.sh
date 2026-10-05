#!/usr/bin/env bash
# 重新生成 backend/testdata/playback 下的合成播放素材：立体声正弦音，带专辑、艺人、曲名与音轨号标签。
# 素材已经提交到仓库；只有修改素材规格时才需要运行本脚本，运行后用
#   cd backend && go test ./internal/library/parser/ -run Fixture
# 核对素材与直出格式表没有脱节。
#
# FFmpeg 只在这里用于生成素材，不进入运行时、部署或 CI：镜像按摘要固定，容器没有网络，
# 只挂载输出目录（绝不挂载 music/）。输出一侧的 -fflags +bitexact 去掉编码器版本、时间戳与随机流序号，同一镜像下重复运行得到相同字节。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOMUSIC_PLAYBACK_FIXTURE_DIR:-$ROOT_DIR/backend/testdata/playback}"
# mwader/static-ffmpeg 7.1.1（静态构建，含 libmp3lame、libvorbis、libopus）。升级镜像须重新核对素材并更新这里的摘要。
FFMPEG_IMAGE="mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c"
MAX_TOTAL_BYTES=1000000

mkdir -p "$OUT_DIR"

run_ffmpeg() {
  docker run --rm --network none -v "$OUT_DIR:/out" -u "$(id -u):$(id -g)" "$FFMPEG_IMAGE" \
    -hide_banner -loglevel error -y "$@"
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

package parser

import (
	"bytes"
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFFmpegMP3XingDurationMatchesDecodedSamples(t *testing.T) {
	ffmpeg := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("未设置 ROOMUSIC_TEST_FFMPEG")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "vbr-xing.mp3")
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo:d=4",
		"-f", "lavfi", "-i", "anoisesrc=r=44100:d=6:a=0.3:seed=1",
		"-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1,aformat=channel_layouts=stereo",
		"-c:a", "libmp3lame", "-q:a", "0", path}
	if output, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("生成 Xing VBR MP3：%v：%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data[:min(len(data), 512)], []byte("Xing")) {
		t.Fatal("生成的 VBR MP3 缺少 Xing 头")
	}
	got, err := ParseAudioFile(path)
	if err != nil {
		t.Fatalf("解析 Xing VBR MP3：%v", err)
	}
	decode := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", path, "-map", "0:a:0", "-ac", "1", "-ar", "44100", "-f", "s16le", "pipe:1")
	var stderr bytes.Buffer
	decode.Stderr = &stderr
	pcm, err := decode.Output()
	if err != nil {
		t.Fatalf("解码 Xing VBR MP3：%v：%s", err, stderr.String())
	}
	decodedSeconds := float64(len(pcm)) / (2 * 44100)
	if math.Abs(decodedSeconds-10) >= 0.01 {
		t.Fatalf("解码样本时长 = %.6f 秒，期望约 10 秒", decodedSeconds)
	}
	if diff := math.Abs(got.DurationSeconds - decodedSeconds); diff >= 0.1 {
		t.Fatalf("扫描时长 %.6f 秒与解码时长 %.6f 秒相差 %.6f 秒，期望 < 0.1 秒", got.DurationSeconds, decodedSeconds, diff)
	}
	t.Logf("Xing 扫描 %.6f 秒，真实解码 %.6f 秒，差值 %.6f 秒", got.DurationSeconds, decodedSeconds, math.Abs(got.DurationSeconds-decodedSeconds))
}

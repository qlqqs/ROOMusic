package analysis

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFFmpegAnalysisMeasuresLoudnessSilenceAndCUEInterval(t *testing.T) {
	path := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if path == "" {
		t.Skip("ROOMUSIC_TEST_FFMPEG is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	analyzer, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open ffmpeg analyzer: %v", err)
	}
	input := filepath.Join(t.TempDir(), "silence-tone.wav")
	command := exec.CommandContext(ctx, path, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo:d=1",
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=44100:duration=3",
		"-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1[out]",
		"-map", "[out]", "-c:a", "pcm_s16le", input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthesize audio: %v: %s", err, output)
	}
	file, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := analyzer.Analyze(ctx, file, nil, nil)
	_ = file.Close()
	if err != nil {
		t.Fatalf("analyze synthesized audio: %v", err)
	}
	if result.IntegratedLUFS == nil || math.Abs(*result.IntegratedLUFS-(-21.1)) > .5 {
		t.Fatalf("integrated loudness = %v LUFS, want -21.1 ± 0.5", result.IntegratedLUFS)
	}
	if result.LeadingSilenceMS == nil || abs32(*result.LeadingSilenceMS-1000) > 20 {
		t.Fatalf("leading silence = %v ms, want approximately 1000", result.LeadingSilenceMS)
	}
	cueStart, cueEnd := int32(75), int32(150) // 1–2 seconds, exactly one second
	file, err = os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	cueResult, err := analyzer.Analyze(ctx, file, &cueStart, &cueEnd)
	_ = file.Close()
	if err != nil {
		t.Fatalf("analyze CUE interval: %v", err)
	}
	if cueResult.DurationMS == nil || abs32(*cueResult.DurationMS-1000) > 20 {
		t.Fatalf("CUE duration = %v ms, want approximately 1000", cueResult.DurationMS)
	}
}

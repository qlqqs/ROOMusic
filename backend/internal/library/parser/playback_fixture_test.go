package parser

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// playbackFixtureDirectory 是 scripts/gen-playback-fixtures.sh 生成并提交的合成播放素材（立体声正弦音，总计不到 1 MB）。
var playbackFixtureDirectory = filepath.Join("..", "..", "..", "testdata", "playback")

// 这些事实与脚本里的编码参数、播放格式表（playback.formatTable）和 E2E 用例共同约束：任何一边改了而另一边没改，这里先失败。
func TestPlaybackFixturesMatchTheirDocumentedFacts(t *testing.T) {
	for _, expected := range []struct {
		file       string
		sourceKind string
		codec      string
		bitDepth   int // 0 表示有损编码，不断言
		sampleRate int
		title      string
		seconds    float64 // 0 表示解析器不计算时长（Ogg 与 Opus）
	}{
		{"flac-16-44100.flac", "flac_vorbis_comment", "flac", 16, 44100, "FLAC 16-44.1", 3},
		{"flac-24-48000.flac", "flac_vorbis_comment", "flac", 24, 48000, "FLAC 24-48", 2},
		{"alac-16-44100.m4a", "m4a", "alac", 16, 44100, "ALAC 16-44.1", 2},
		{"alac-24-48000.m4a", "m4a", "alac", 24, 48000, "ALAC 24-48", 1},
		{"mp3-44100.mp3", "mp3_id3v2", "mp3", 0, 44100, "MP3", 2},
		{"aac-44100.m4a", "m4a", "aac", 0, 44100, "AAC-LC", 2},
		{"wav-16-44100.wav", "wav_info", "pcm", 16, 44100, "WAV 16-44.1", 1},
		// FFmpeg 把 24 位 WAV 写成 WAVE_FORMAT_EXTENSIBLE；解析器按头部格式标签记为 wav_extensible，不在直出格式表中。
		{"wav-24-48000.wav", "wav_info", "wav_extensible", 24, 48000, "WAV 24-48", 0.5},
		{"vorbis-44100.ogg", "ogg_vorbis_comment", "vorbis", 0, 44100, "Vorbis", 0},
		{"opus-48000.opus", "opus_tags", "opus", 0, 48000, "Opus", 0},
	} {
		t.Run(expected.file, func(t *testing.T) {
			observation, err := ParseAudioFile(filepath.Join(playbackFixtureDirectory, expected.file))
			if err != nil {
				t.Fatalf("ParseAudioFile: %v", err)
			}
			if observation.SourceKind != expected.sourceKind || observation.Codec != expected.codec {
				t.Errorf("source kind / codec = %s / %s, want %s / %s", observation.SourceKind, observation.Codec, expected.sourceKind, expected.codec)
			}
			if observation.SampleRate != expected.sampleRate || observation.Channels != 2 {
				t.Errorf("sample rate / channels = %d / %d, want %d / 2", observation.SampleRate, observation.Channels, expected.sampleRate)
			}
			if expected.bitDepth != 0 && observation.BitDepth != expected.bitDepth {
				t.Errorf("bit depth = %d, want %d", observation.BitDepth, expected.bitDepth)
			}
			if observation.Title != expected.title || observation.Album != "Playback Fixtures" || observation.Artist != "Fixture Artist" {
				t.Errorf("tags = %q / %q / %q", observation.Title, observation.Album, observation.Artist)
			}
			if expected.seconds != 0 && math.Abs(observation.DurationSeconds-expected.seconds) > 0.05 {
				t.Errorf("duration = %.3f s, want %.3f s", observation.DurationSeconds, expected.seconds)
			}
		})
	}
}

func TestPlaybackFixturesAreAllCoveredAndSmall(t *testing.T) {
	entries, err := os.ReadDir(playbackFixtureDirectory)
	if err != nil {
		t.Fatalf("read fixture directory: %v", err)
	}
	var total int64
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) == ".md" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		total += info.Size()
		count++
	}
	// 十个文件：FLAC×2、ALAC×2、MP3、AAC、WAV×2、Vorbis、Opus。增删素材要同时更新上面的事实表。
	if count != 10 {
		t.Errorf("fixture files = %d, want 10", count)
	}
	if total > 1_000_000 {
		t.Errorf("fixtures total %d bytes, want at most 1,000,000", total)
	}
}

package config

import (
	"strings"
	"testing"
)

// setRequired 设置 Load 必需的变量，并清掉本测试关心的可选变量，使结果不受外部环境影响。
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("ROOMUSIC_DATABASE_URL", "postgres://example.invalid/roomusic")
	t.Setenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS", "/music")
	t.Setenv("ROOMUSIC_ENV", "development")
	t.Setenv("ROOMUSIC_FFMPEG_PATH", "")
	t.Setenv("ROOMUSIC_MEDIA_CACHE_MAX_MIB", "")
}

func TestLoadUsesTheDerivationDefaults(t *testing.T) {
	setRequired(t)
	settings, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.FFmpegPath != "" {
		t.Errorf("FFmpegPath = %q, want empty (look up PATH)", settings.FFmpegPath)
	}
	if want := int64(10) << 30; settings.MediaCacheMaxBytes != want {
		t.Errorf("MediaCacheMaxBytes = %d, want %d", settings.MediaCacheMaxBytes, want)
	}
}

func TestLoadReadsTheDerivationSettings(t *testing.T) {
	setRequired(t)
	t.Setenv("ROOMUSIC_FFMPEG_PATH", "  /opt/ffmpeg/bin/ffmpeg  ")
	t.Setenv("ROOMUSIC_MEDIA_CACHE_MAX_MIB", " 2048 ")
	settings, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.FFmpegPath != "/opt/ffmpeg/bin/ffmpeg" {
		t.Errorf("FFmpegPath = %q", settings.FFmpegPath)
	}
	if want := int64(2048) << 20; settings.MediaCacheMaxBytes != want {
		t.Errorf("MediaCacheMaxBytes = %d, want %d", settings.MediaCacheMaxBytes, want)
	}
}

func TestLoadAcceptsTheMinimumMediaCacheLimit(t *testing.T) {
	setRequired(t)
	t.Setenv("ROOMUSIC_MEDIA_CACHE_MAX_MIB", "256")
	settings, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := int64(256) << 20; settings.MediaCacheMaxBytes != want {
		t.Errorf("MediaCacheMaxBytes = %d, want %d", settings.MediaCacheMaxBytes, want)
	}
}

func TestLoadRejectsAnInvalidMediaCacheLimit(t *testing.T) {
	for _, value := range []string{"255", "0", "-1", "abc", "1.5", "10G", "9223372036854775807", "99999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_MEDIA_CACHE_MAX_MIB", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_MEDIA_CACHE_MAX_MIB") {
				t.Fatalf("Load error = %v, want a ROOMUSIC_MEDIA_CACHE_MAX_MIB error", err)
			}
		})
	}
}

func TestLoadRejectsARelativeFFmpegPath(t *testing.T) {
	setRequired(t)
	t.Setenv("ROOMUSIC_FFMPEG_PATH", "bin/ffmpeg")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_FFMPEG_PATH") {
		t.Fatalf("Load error = %v, want a ROOMUSIC_FFMPEG_PATH error", err)
	}
}

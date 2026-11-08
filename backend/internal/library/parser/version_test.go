package parser

import (
	"fmt"
	"strings"
	"testing"
)

func TestCacheVersionKnownExtensions(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a", ".ape", ".wv", ".aif", ".aiff"} {
		t.Run(extension, func(t *testing.T) {
			version, ok := formatVersions[extension]
			if !ok || version < 1 {
				t.Fatalf("已支持格式 %s 没有有效的独立版本号", extension)
			}
			want := fmt.Sprintf("c%d.f%d", CommonVersion, version)
			if got := CacheVersion(extension); got != want {
				t.Fatalf("CacheVersion(%q) = %q，期望 %q", extension, got, want)
			}
		})
	}
}

func TestCacheVersionUnknownExtensions(t *testing.T) {
	for _, extension := range []string{"", ".unknown", ".aac", ".cue", "flac"} {
		t.Run(extension, func(t *testing.T) {
			if got := CacheVersion(extension); got != "" {
				t.Fatalf("CacheVersion(%q) = %q，未知格式不应进入缓存", extension, got)
			}
		})
	}
}

func TestCacheVersionCaseInsensitive(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a", ".ape", ".wv", ".aif", ".aiff"} {
		for _, variant := range []string{strings.ToUpper(extension), strings.ToUpper(extension[:2]) + extension[2:]} {
			t.Run(variant, func(t *testing.T) {
				if got, want := CacheVersion(variant), CacheVersion(extension); got == "" || got != want {
					t.Fatalf("CacheVersion(%q) = %q，期望与小写扩展名相同的版本 %q", variant, got, want)
				}
			})
		}
	}
}

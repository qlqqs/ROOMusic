package config

import (
	"reflect"
	"strings"
	"testing"
)

// setRequired 设置 Load 必需的变量，并清掉本测试关心的可选变量，使结果不受外部环境影响。
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("ROOMUSIC_DATABASE_URL", "postgres://example.invalid/roomusic")
	t.Setenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS", "/music")
	t.Setenv("ROOMUSIC_ENV", "development")
	t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", "")
	t.Setenv("ROOMUSIC_SECURE_COOKIES", "")
	t.Setenv("ROOMUSIC_PUBLIC_URL", "")
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

func TestLoadAllowedOriginsNormalizesAndDeduplicates(t *testing.T) {
	setRequired(t)
	t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", " , HTTP://192.168.50.206:8080/ , HTTPS://Music.Example.com:443,https://music.example.com/,http://localhost:80,, http://[FD00::1]:8090/ , ")
	settings, err := Load()
	want := []string{"http://192.168.50.206:8080", "https://music.example.com", "http://localhost", "http://[fd00::1]:8090"}
	if err != nil || !reflect.DeepEqual(settings.AllowedOrigins, want) {
		t.Fatalf("AllowedOrigins = %v, %v，期望按首次出现顺序去重为 %v", settings.AllowedOrigins, err, want)
	}
}

func TestLoadProductionWithoutAllowedOrigins(t *testing.T) {
	for _, value := range []string{"", " , , "} {
		for _, secure := range []string{"", "true", "false"} {
			t.Run(value+"/secure="+secure, func(t *testing.T) {
				setRequired(t)
				t.Setenv("ROOMUSIC_ENV", "production")
				t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", value)
				t.Setenv("ROOMUSIC_SECURE_COOKIES", secure)
				settings, err := Load()
				if secure == "" {
					if err != nil || settings.OriginMode != OriginModeSameHost {
						t.Fatalf("生产缺少清单：mode=%q err=%v，期望主机一致模式", settings.OriginMode, err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_SECURE_COOKIES") {
					t.Fatalf("生产缺少清单且设旧开关：%v，期望旧开关冲突", err)
				}
			})
		}
	}
}

func TestLoadProductionAcceptsOriginsWithoutLegacyCookieSetting(t *testing.T) {
	for _, origin := range []string{"http://192.168.50.206:8080", "https://music.example.com", "http://192.168.50.206:8080,https://music.example.com"} {
		t.Run(origin, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_ENV", "production")
			t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", origin)
			settings, err := Load()
			if err != nil || len(settings.AllowedOrigins) == 0 || settings.SecureCookies {
				t.Fatalf("生产应允许清单且不设旧开关：AllowedOrigins=%v SecureCookies=%v err=%v", settings.AllowedOrigins, settings.SecureCookies, err)
			}
		})
	}
}

func TestLoadAllowedOriginsChecksLegacyCookieSetting(t *testing.T) {
	for _, value := range []string{"true", "false", "invalid", " "} {
		t.Run(value, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", "http://192.168.50.206:8080,https://music.example.com")
			t.Setenv("ROOMUSIC_SECURE_COOKIES", value)
			settings, err := Load()
			if strings.TrimSpace(value) == "" {
				if err != nil || settings.OriginMode != OriginModeAllowList {
					t.Fatalf("空白旧开关应视为空：mode=%q err=%v", settings.OriginMode, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_SECURE_COOKIES") || !strings.Contains(err.Error(), "ROOMUSIC_ALLOWED_ORIGINS") {
				t.Fatalf("清单与非空旧开关 %q 冲突，期望错误指出两个配置项，得到 %v", value, err)
			}
		})
	}
}

func TestLoadAllowedOriginsChecksPublicURLMembership(t *testing.T) {
	for _, tc := range []struct {
		publicURL string
		wantError bool
	}{
		{"", false},
		{" HTTPS://Music.Example.com:443 ", false},
		{"http://192.168.50.206:8080", false},
		{"http://music.example.com", true},
		{"https://other.example.com", true},
		{"https://music.example.com/app", true},
		{"https://music.example.com/", true},
	} {
		t.Run(tc.publicURL, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", "http://192.168.50.206:8080,https://music.example.com")
			t.Setenv("ROOMUSIC_PUBLIC_URL", tc.publicURL)
			settings, err := Load()
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_PUBLIC_URL") {
					t.Fatalf("PUBLIC_URL %q 应被拒绝并指出配置项：%v", tc.publicURL, err)
				}
				return
			}
			if err != nil || settings.PublicURL != strings.TrimSpace(tc.publicURL) {
				t.Fatalf("PUBLIC_URL = %q, err=%v", settings.PublicURL, err)
			}
		})
	}
}

func TestLoadAllowedOriginsRejectsInvalidItems(t *testing.T) {
	for _, item := range []string{"https://music.example.com/app", "https://user@music.example.com", "https://music.example.com?x=1", "https://music.example.com#x", "null", "file:///music", "ftp://music.example.com", "http://", "http://localhost:0", "http://localhost:65536", "https://音乐.example", "https://*.example.com", "http://fd00::1"} {
		for _, environment := range []string{"development", "production"} {
			t.Run(environment+"/"+item, func(t *testing.T) {
				setRequired(t)
				t.Setenv("ROOMUSIC_ENV", environment)
				t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", "https://music.example.com, "+item+" ,")
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), "ROOMUSIC_ALLOWED_ORIGINS") || !strings.Contains(err.Error(), item) {
					t.Fatalf("非法清单项：%v，期望指出配置项及 %q", err, item)
				}
			})
		}
	}
}

func TestLoadLegacyOriginSettingsRemainUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, secure, publicURL string
		wantSecure              bool
	}{
		{"defaults", "", "", false},
		{"http proxy", "false", "http://localhost:5173", false},
		{"https proxy", "true", " https://music.example.com ", true},
		{"only exact true enables secure", "TRUE", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", " , , ")
			t.Setenv("ROOMUSIC_SECURE_COOKIES", tc.secure)
			t.Setenv("ROOMUSIC_PUBLIC_URL", tc.publicURL)
			settings, err := Load()
			if err != nil || len(settings.AllowedOrigins) != 0 || settings.SecureCookies != tc.wantSecure || settings.PublicURL != strings.TrimSpace(tc.publicURL) {
				t.Fatalf("旧模式读取不符：AllowedOrigins=%v SecureCookies=%v PublicURL=%q err=%v", settings.AllowedOrigins, settings.SecureCookies, settings.PublicURL, err)
			}
		})
	}
}

func TestLoadOriginModeMatrix(t *testing.T) {
	const (
		allowListConflict = "ROOMUSIC_SECURE_COOKIES cannot be combined with ROOMUSIC_ALLOWED_ORIGINS; the Secure flag follows each origin's scheme"
		productionSecure  = "ROOMUSIC_SECURE_COOKIES is not used in production; the Secure flag follows each origin's scheme"
		productionPublic  = "ROOMUSIC_PUBLIC_URL is not used in production; list origins in ROOMUSIC_ALLOWED_ORIGINS to restrict access"
	)
	for _, tc := range []struct {
		name, environment, origins, secure, publicURL string
		wantMode                                      OriginMode
		wantError                                     string
	}{
		{"row 1 allow-list conflicts with secure", "production", "https://music.example.com", "true", "", "", allowListConflict},
		{"row 2 allow-list without legacy settings", "production", "http://192.168.50.206:18090", "", "", OriginModeAllowList, ""},
		{"row 3 public URL normalized into allow-list", "development", "https://music.example.com", "", " HTTPS://Music.Example.COM:443 ", OriginModeAllowList, ""},
		{"row 4 public URL outside allow-list", "production", "https://music.example.com", "", "http://music.example.com", "", "ROOMUSIC_PUBLIC_URL must be one of ROOMUSIC_ALLOWED_ORIGINS"},
		{"row 5 production conflicts with secure", "production", "", "true", "", "", productionSecure},
		{"row 6 production conflicts with public URL", "production", "", "", "https://music.example.com", "", productionPublic},
		{"row 7 production defaults to same-host", "production", "", "", "", OriginModeSameHost, ""},
		{"row 8 development preserves legacy", "development", "", "true", " https://music.example.com ", OriginModeLegacy, ""},
		{"production false still conflicts", "production", "", "false", "", "", productionSecure},
		{"production invalid secure still conflicts", "production", "", "invalid", "", "", productionSecure},
		{"production secure takes precedence over public URL", "production", "", "true", "https://music.example.com", "", productionSecure},
		{"production false takes precedence over public URL", "production", "", "false", "https://music.example.com", "", productionSecure},
		{"production empty list entries are same-host", "production", " , , ", "", "", OriginModeSameHost, ""},
		{"production whitespace settings are empty", "production", " ", " \t ", " \t ", OriginModeSameHost, ""},
		{"allow-list false still conflicts", "development", "https://music.example.com", "false", "", "", allowListConflict},
		{"dev remains legacy", "dev", " , ", "false", "http://localhost:5173", OriginModeLegacy, ""},
		{"test remains legacy", "test", "", "", "", OriginModeLegacy, ""},
		{"legacy only exact true enables secure", "development", "", " true ", "", OriginModeLegacy, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_ENV", tc.environment)
			t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", tc.origins)
			t.Setenv("ROOMUSIC_SECURE_COOKIES", tc.secure)
			t.Setenv("ROOMUSIC_PUBLIC_URL", tc.publicURL)
			settings, err := Load()
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError {
					t.Fatalf("Load 错误 = %v，期望 %q", err, tc.wantError)
				}
				return
			}
			if err != nil || settings.OriginMode != tc.wantMode {
				t.Fatalf("Load mode=%q err=%v，期望模式 %q", settings.OriginMode, err, tc.wantMode)
			}
			if tc.wantMode == OriginModeLegacy && (settings.SecureCookies != (tc.secure == "true") || settings.PublicURL != strings.TrimSpace(tc.publicURL) || len(settings.AllowedOrigins) != 0) {
				t.Fatalf("旧模式读取发生变化：SecureCookies=%v PublicURL=%q AllowedOrigins=%v", settings.SecureCookies, settings.PublicURL, settings.AllowedOrigins)
			}
		})
	}
}

func TestLoadOriginValidationOrder(t *testing.T) {
	for _, tc := range []struct {
		name, origins, secure, publicURL, wantError string
	}{
		{"public URL format before list format and conflicts", "null", "true", "https://music.example.com/app", "ROOMUSIC_PUBLIC_URL must be an absolute origin"},
		{"public URL format before production secure conflict", "", "true", "https://music.example.com/", "ROOMUSIC_PUBLIC_URL must be an absolute origin"},
		{"list format before secure conflict", "null", "true", "", `ROOMUSIC_ALLOWED_ORIGINS item "null"`},
		{"list format before public URL membership", "null", "", "https://music.example.com", `ROOMUSIC_ALLOWED_ORIGINS item "null"`},
		{"list secure conflict before public URL membership", "https://music.example.com", "true", "https://other.example.com", "ROOMUSIC_SECURE_COOKIES cannot be combined with ROOMUSIC_ALLOWED_ORIGINS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("ROOMUSIC_ENV", "production")
			t.Setenv("ROOMUSIC_ALLOWED_ORIGINS", tc.origins)
			t.Setenv("ROOMUSIC_SECURE_COOKIES", tc.secure)
			t.Setenv("ROOMUSIC_PUBLIC_URL", tc.publicURL)
			_, err := Load()
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantError) {
				t.Fatalf("Load 错误 = %v，期望优先报告 %q", err, tc.wantError)
			}
		})
	}
}

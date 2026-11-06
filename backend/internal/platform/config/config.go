// Package config 解码并校验进程的环境变量配置。它只读环境，不做任何 I/O，也不含业务默认策略。
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/weborigin"
)

const (
	// DefaultMediaCacheMiB 是派生缓存的默认上限：10 GiB（约容纳几十张无损专辑的 FLAC 派生）。
	DefaultMediaCacheMiB = 10240
	// MinMediaCacheMiB 是允许配置的派生缓存上限下限：再小连一张高解析度专辑的派生都放不下。
	MinMediaCacheMiB = 256
	// maxMediaCacheMiB 只防止换算为字节时溢出（1 PiB）。
	maxMediaCacheMiB = 1 << 30
)

// OriginMode 是配置加载时确定的来源校验模式。
type OriginMode string

const (
	OriginModeLegacy    OriginMode = "legacy"     // 非生产且清单为空：保留旧行为。
	OriginModeAllowList OriginMode = "allow-list" // 清单非空。
	OriginModeSameHost  OriginMode = "same-host"  // 生产且清单为空。
)

// Server 是进程启动所需的配置。
type Server struct {
	Backup              Backup
	Address             string
	Environment         string
	DatabaseURL         string
	AllowedLibraryRoots []string
	AllowedOrigins      []string
	OriginMode          OriginMode
	SecureCookies       bool
	DataDirectory       string
	PublicURL           string
	// FFmpegPath 是 ffmpeg 可执行文件的绝对路径；空表示在 PATH 中查找。找不到或自检不过时派生关闭，应用照常启动。
	FFmpegPath string
	// MediaCacheMaxBytes 是派生缓存的上限（字节），缓存目录是 <DataDirectory>/media-cache。
	MediaCacheMaxBytes int64
}

// Load 从环境变量读取并校验配置；缺少必需项或生产环境不安全时返回错误。
func Load() (Server, error) {
	dataDirectory := strings.TrimSpace(os.Getenv("ROOMUSIC_DATA_DIR"))
	if dataDirectory == "" {
		dataDirectory = "./data"
	}
	publicURL := strings.TrimSpace(os.Getenv("ROOMUSIC_PUBLIC_URL"))
	if publicURL != "" {
		parsed, err := url.Parse(publicURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Server{}, fmt.Errorf("ROOMUSIC_PUBLIC_URL must be an absolute origin")
		}
	}
	ffmpegPath := strings.TrimSpace(os.Getenv("ROOMUSIC_FFMPEG_PATH"))
	if ffmpegPath != "" && !filepath.IsAbs(ffmpegPath) {
		return Server{}, fmt.Errorf("ROOMUSIC_FFMPEG_PATH must be an absolute path")
	}
	mediaCacheMaxBytes, err := configuredMediaCacheMaxBytes()
	if err != nil {
		return Server{}, err
	}
	databaseURL, err := DatabaseURL()
	if err != nil {
		return Server{}, err
	}
	backup, err := LoadBackup()
	if err != nil {
		return Server{}, err
	}
	config := Server{Backup: backup, Address: configuredAddress(), Environment: configuredEnvironment(), DatabaseURL: databaseURL, SecureCookies: os.Getenv("ROOMUSIC_SECURE_COOKIES") == "true", DataDirectory: dataDirectory, PublicURL: publicURL, FFmpegPath: ffmpegPath, MediaCacheMaxBytes: mediaCacheMaxBytes}
	for _, configuredRoot := range strings.Split(os.Getenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS"), ",") {
		if root := strings.TrimSpace(configuredRoot); root != "" {
			config.AllowedLibraryRoots = append(config.AllowedLibraryRoots, root)
		}
	}
	if len(config.AllowedLibraryRoots) == 0 {
		return Server{}, fmt.Errorf("ROOMUSIC_ALLOWED_LIBRARY_ROOTS must contain at least one root")
	}
	config.AllowedOrigins, err = configuredAllowedOrigins(publicURL)
	if err != nil {
		return Server{}, err
	}
	switch {
	case len(config.AllowedOrigins) > 0:
		config.OriginMode = OriginModeAllowList
	case config.Environment == "production":
		if strings.TrimSpace(os.Getenv("ROOMUSIC_SECURE_COOKIES")) != "" {
			return Server{}, fmt.Errorf("ROOMUSIC_SECURE_COOKIES is not used in production; the Secure flag follows each origin's scheme")
		}
		if publicURL != "" {
			return Server{}, fmt.Errorf("ROOMUSIC_PUBLIC_URL is not used in production; list origins in ROOMUSIC_ALLOWED_ORIGINS to restrict access")
		}
		config.OriginMode = OriginModeSameHost
	default:
		config.OriginMode = OriginModeLegacy
	}
	return config, nil
}

// configuredAllowedOrigins 规范化并按首次出现顺序去重；清单模式拒绝含糊的旧配置。
func configuredAllowedOrigins(publicURL string) ([]string, error) {
	var origins []string
	seen := make(map[string]bool)
	for _, item := range strings.Split(os.Getenv("ROOMUSIC_ALLOWED_ORIGINS"), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		origin, err := weborigin.ParseConfigured(item)
		if err != nil {
			return nil, fmt.Errorf("ROOMUSIC_ALLOWED_ORIGINS item %q %w", item, err)
		}
		if !seen[origin] {
			origins = append(origins, origin)
			seen[origin] = true
		}
	}
	if len(origins) == 0 {
		return origins, nil
	}
	if strings.TrimSpace(os.Getenv("ROOMUSIC_SECURE_COOKIES")) != "" {
		return nil, fmt.Errorf("ROOMUSIC_SECURE_COOKIES cannot be combined with ROOMUSIC_ALLOWED_ORIGINS; the Secure flag follows each origin's scheme")
	}
	if publicURL != "" {
		origin, err := weborigin.ParseConfigured(publicURL)
		if err != nil || !seen[origin] {
			return nil, fmt.Errorf("ROOMUSIC_PUBLIC_URL must be one of ROOMUSIC_ALLOWED_ORIGINS")
		}
	}
	return origins, nil
}

// configuredMediaCacheMaxBytes 读取派生缓存上限（MiB，整数）并换算为字节；未设置用默认值，非法值使配置加载失败。
func configuredMediaCacheMaxBytes() (int64, error) {
	value := strings.TrimSpace(os.Getenv("ROOMUSIC_MEDIA_CACHE_MAX_MIB"))
	if value == "" {
		return DefaultMediaCacheMiB << 20, nil
	}
	mebibytes, err := strconv.ParseInt(value, 10, 64)
	if err != nil || mebibytes < MinMediaCacheMiB || mebibytes > maxMediaCacheMiB {
		return 0, fmt.Errorf("ROOMUSIC_MEDIA_CACHE_MAX_MIB must be an integer between %d and %d", MinMediaCacheMiB, maxMediaCacheMiB)
	}
	return mebibytes << 20, nil
}

func configuredEnvironment() string {
	if environment := os.Getenv("ROOMUSIC_ENV"); environment != "" {
		return environment
	}
	return "development"
}

func configuredAddress() string {
	if address := os.Getenv("ROOMUSIC_HTTP_ADDR"); address != "" {
		return address
	}
	return ":8080"
}

// DatabaseURL 仅读取恢复密码所需的数据库配置，不要求服务启动配置。
func DatabaseURL() (string, error) {
	value := strings.TrimSpace(os.Getenv("ROOMUSIC_DATABASE_URL"))
	if value == "" {
		return "", fmt.Errorf("ROOMUSIC_DATABASE_URL is required")
	}
	return value, nil
}

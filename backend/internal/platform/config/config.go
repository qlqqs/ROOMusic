// Package config 解码并校验进程的环境变量配置。它读取环境变量与指定的凭据文件，不含业务默认策略。
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
	// FFmpegPath 是 ffmpeg 可执行文件的绝对路径；空表示在 PATH 中查找。找不到或自检不过时渲染不可用，应用照常启动。
	FFmpegPath    string
	AudioAnalysis bool
	ScanWorkers   int
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
	databaseURL, err := DatabaseURL()
	if err != nil {
		return Server{}, err
	}
	backup, err := LoadBackup()
	if err != nil {
		return Server{}, err
	}
	audioAnalysis, err := configuredAudioAnalysis()
	if err != nil {
		return Server{}, err
	}
	scanWorkers, err := configuredScanWorkers()
	if err != nil {
		return Server{}, err
	}
	config := Server{ScanWorkers: scanWorkers, AudioAnalysis: audioAnalysis, Backup: backup, Address: configuredAddress(), Environment: configuredEnvironment(), DatabaseURL: databaseURL, SecureCookies: os.Getenv("ROOMUSIC_SECURE_COOKIES") == "true", DataDirectory: dataDirectory, PublicURL: publicURL, FFmpegPath: ffmpegPath}
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
	passwordFile := os.Getenv("ROOMUSIC_DB_PASSWORD_FILE")
	if passwordFile == "" {
		return value, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.User == nil || parsed.User.Username() == "" {
		return "", fmt.Errorf("使用密码文件时 ROOMUSIC_DATABASE_URL 必须是包含用户名的 PostgreSQL URL")
	}
	if _, present := parsed.User.Password(); present || parsed.Query().Has("password") {
		return "", fmt.Errorf("数据库密码不能同时配置在 URL 和密码文件中")
	}
	content, err := os.ReadFile(passwordFile)
	if err != nil {
		return "", fmt.Errorf("无法读取 ROOMUSIC_DB_PASSWORD_FILE")
	}
	// 与 PostgreSQL 官方入口的文件读取一致，去除文件末尾的换行。
	password := strings.TrimRight(string(content), "\n")
	if password == "" || strings.ContainsAny(password, "\r\n\x00") {
		return "", fmt.Errorf("数据库密码文件必须包含非空的单行密码")
	}
	parsed.User = url.UserPassword(parsed.User.Username(), password)
	return parsed.String(), nil
}

// configuredAudioAnalysis 解析后台分析开关；默认开启。
func configuredAudioAnalysis() (bool, error) {
	switch value := strings.TrimSpace(os.Getenv("ROOMUSIC_AUDIO_ANALYSIS")); value {
	case "", "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("ROOMUSIC_AUDIO_ANALYSIS must be on or off")
	}
}

// configuredScanWorkers 解析扫描读盘并发度；未配置时使用四个工作者。
func configuredScanWorkers() (int, error) {
	value := strings.TrimSpace(os.Getenv("ROOMUSIC_SCAN_WORKERS"))
	if value == "" {
		return 4, nil
	}
	workers, err := strconv.Atoi(value)
	if err != nil || workers < 1 || workers > 16 {
		return 0, fmt.Errorf("ROOMUSIC_SCAN_WORKERS must be an integer between 1 and 16")
	}
	return workers, nil
}

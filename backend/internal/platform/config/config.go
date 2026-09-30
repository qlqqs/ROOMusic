// Package config 解码并校验进程的环境变量配置。它只读环境，不做任何 I/O，也不含业务默认策略。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Server 是进程启动所需的配置。
type Server struct {
	Address             string
	Environment         string
	DatabaseURL         string
	AllowedLibraryRoots []string
	SecureCookies       bool
	DataDirectory       string
	PublicURL           string
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
	config := Server{Address: configuredAddress(), Environment: configuredEnvironment(), DatabaseURL: strings.TrimSpace(os.Getenv("ROOMUSIC_DATABASE_URL")), SecureCookies: os.Getenv("ROOMUSIC_SECURE_COOKIES") == "true", DataDirectory: dataDirectory, PublicURL: publicURL}
	for _, configuredRoot := range strings.Split(os.Getenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS"), ",") {
		if root := strings.TrimSpace(configuredRoot); root != "" {
			config.AllowedLibraryRoots = append(config.AllowedLibraryRoots, root)
		}
	}
	if config.DatabaseURL == "" {
		return Server{}, fmt.Errorf("ROOMUSIC_DATABASE_URL is required")
	}
	if len(config.AllowedLibraryRoots) == 0 {
		return Server{}, fmt.Errorf("ROOMUSIC_ALLOWED_LIBRARY_ROOTS must contain at least one root")
	}
	if config.Environment == "production" && !config.SecureCookies {
		return Server{}, fmt.Errorf("ROOMUSIC_SECURE_COOKIES must be true in production")
	}
	return config, nil
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

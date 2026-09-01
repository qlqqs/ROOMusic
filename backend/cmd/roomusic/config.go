package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type serverConfig struct {
	Address             string
	Environment         string
	DatabaseURL         string
	AllowedLibraryRoots []string
	SecureCookies       bool
	DataDirectory       string
	PublicURL           string
}

func loadServerConfig() (serverConfig, error) {
	dataDirectory := strings.TrimSpace(os.Getenv("ROOMUSIC_DATA_DIR"))
	if dataDirectory == "" {
		dataDirectory = "./data"
	}
	publicURL := strings.TrimSpace(os.Getenv("ROOMUSIC_PUBLIC_URL"))
	if publicURL != "" {
		parsed, err := url.Parse(publicURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return serverConfig{}, fmt.Errorf("ROOMUSIC_PUBLIC_URL must be an absolute origin")
		}
	}
	config := serverConfig{Address: configuredAddress(), Environment: configuredEnvironment(), DatabaseURL: strings.TrimSpace(os.Getenv("ROOMUSIC_DATABASE_URL")), SecureCookies: os.Getenv("ROOMUSIC_SECURE_COOKIES") == "true", DataDirectory: dataDirectory, PublicURL: publicURL}
	for _, configuredRoot := range strings.Split(os.Getenv("ROOMUSIC_ALLOWED_LIBRARY_ROOTS"), ",") {
		if root := strings.TrimSpace(configuredRoot); root != "" {
			config.AllowedLibraryRoots = append(config.AllowedLibraryRoots, root)
		}
	}
	if config.DatabaseURL == "" {
		return serverConfig{}, fmt.Errorf("ROOMUSIC_DATABASE_URL is required")
	}
	if len(config.AllowedLibraryRoots) == 0 {
		return serverConfig{}, fmt.Errorf("ROOMUSIC_ALLOWED_LIBRARY_ROOTS must contain at least one root")
	}
	if config.Environment == "production" && !config.SecureCookies {
		return serverConfig{}, fmt.Errorf("ROOMUSIC_SECURE_COOKIES must be true in production")
	}
	return config, nil
}

func configuredEnvironment() string {
	if environment := os.Getenv("ROOMUSIC_ENV"); environment != "" {
		return environment
	}
	return "development"
}

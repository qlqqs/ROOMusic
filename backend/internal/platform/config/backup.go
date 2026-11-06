package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Backup 保存备份与恢复所需的配置，不依赖音乐目录配置。
type Backup struct {
	DatabaseURL     string
	Directory       string
	CreateDirectory bool // 仅自动创建开发环境的默认目录，不修复显式配置的挂载目录。
	Hour            int
	KeepDays        int // 从未在网页保存保留规则时使用的默认天数。
	Location        *time.Location
	PGBinDirectory  string
}

func LoadBackup() (Backup, error) {
	databaseURL, err := DatabaseURL()
	if err != nil {
		return Backup{}, err
	}
	data := strings.TrimSpace(os.Getenv("ROOMUSIC_DATA_DIR"))
	if data == "" {
		data = "./data"
	}
	directory, explicit := os.LookupEnv("ROOMUSIC_BACKUP_DIR")
	directory = strings.TrimSpace(directory)
	if explicit && directory == "" {
		return Backup{}, fmt.Errorf("ROOMUSIC_BACKUP_DIR 不能为空")
	}
	if !explicit {
		directory = filepath.Join(data, "backups")
	}
	hour, err := backupInteger("ROOMUSIC_BACKUP_HOUR", 3, 0, 23)
	if err != nil {
		return Backup{}, err
	}
	days, err := backupInteger("ROOMUSIC_BACKUP_KEEP_DAYS", 14, 1, 3650)
	if err != nil {
		return Backup{}, err
	}
	zone := strings.TrimSpace(os.Getenv("ROOMUSIC_TIMEZONE"))
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return Backup{}, fmt.Errorf("ROOMUSIC_TIMEZONE 必须是有效时区")
	}
	bin := strings.TrimSpace(os.Getenv("ROOMUSIC_PG_BIN_DIR"))
	if bin != "" && !filepath.IsAbs(bin) {
		return Backup{}, fmt.Errorf("ROOMUSIC_PG_BIN_DIR 必须是绝对路径")
	}
	return Backup{DatabaseURL: databaseURL, Directory: directory, CreateDirectory: !explicit && configuredEnvironment() != "production", Hour: hour, KeepDays: days, Location: location, PGBinDirectory: bin}, nil
}

func backupInteger(name string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum || number > maximum {
		return 0, fmt.Errorf("%s 必须是 %d 到 %d 之间的整数", name, minimum, maximum)
	}
	return number, nil
}

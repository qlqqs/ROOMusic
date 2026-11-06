package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/backup"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
)

type backupCommandEnv struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	load           func() (config.Backup, error)
	connect        func(context.Context, string) (*sql.DB, error)
	newService     func(config.Backup, *slog.Logger, ...backup.Option) *backup.Service
}

func defaultBackupCommandEnv(env resetPasswordEnv) backupCommandEnv {
	return backupCommandEnv{stdin: env.stdin, stdout: env.stdout, stderr: env.stderr, load: config.LoadBackup, connect: postgres.Connect, newService: backup.NewService}
}
func backupService(settings config.Backup, env backupCommandEnv) *backup.Service {
	return env.newService(settings, slog.New(slog.NewJSONHandler(env.stderr, nil)), backup.WithWaitingNotification(func() { fmt.Fprintln(env.stderr, "另一份备份正在进行，等它完成…") }))
}
func runBackup(ctx context.Context, args []string, env backupCommandEnv) int {
	if len(args) != 0 {
		fmt.Fprintln(env.stderr, "用法：roomusic backup")
		return 2
	}
	settings, err := env.load()
	if err != nil {
		fmt.Fprintln(env.stderr, "备份配置无效，请检查环境变量。")
		return 1
	}
	database, err := env.connect(ctx, settings.DatabaseURL)
	if err != nil {
		if ctx.Err() != nil {
			return 130
		}
		fmt.Fprintln(env.stderr, "无法连接数据库。")
		return 1
	}
	defer database.Close()
	service := backupService(settings, env)
	file, err := service.RunNow(ctx, database, backup.SourceManual)
	if ctx.Err() != nil {
		return 130
	}
	if err != nil {
		stage := "prepare"
		var failure *backup.Error
		if errors.As(err, &failure) {
			stage = failure.Stage
		}
		fmt.Fprintf(env.stderr, "备份没有成功（阶段：%s）。\n", stage)
		return 1
	}
	fmt.Fprintf(env.stdout, "已备份：%s（%s）\n", file.Name, formatBackupSize(file.SizeBytes))
	return 0
}
func formatBackupSize(size int64) string {
	value := float64(size)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}

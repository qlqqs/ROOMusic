package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/backup"
)

func runRestore(ctx context.Context, args []string, env backupCommandEnv) int {
	if len(args) != 1 {
		fmt.Fprintln(env.stderr, "用法：roomusic restore <备份文件>")
		return 2
	}
	settings, err := env.load()
	if err != nil {
		fmt.Fprintln(env.stderr, "备份配置无效，请检查环境变量。")
		return 1
	}
	service := backupService(settings, env)
	session, err := service.PrepareRestore(ctx, args[0])
	if err != nil {
		return restoreCommandError(ctx, err, env)
	}
	defer session.Close()
	preview := session.Preview
	fmt.Fprintf(env.stdout, "备份文件：%s\n备份时间：%s（%s）\n大小：%s\n备份里的迁移版本：%s\n当前数据库的迁移版本：%s\n",
		preview.Name, preview.CreatedAt.In(settings.Location).Format("2006-01-02 15:04"), settings.Location.String(), formatBackupSize(preview.SizeBytes), migrationVersion(preview.BackupMigrations), migrationVersion(preview.CurrentMigrations))
	if preview.NewerVersion {
		fmt.Fprintln(env.stdout, "这份备份来自更新的版本，恢复后用对应版本的镜像启动。")
	}
	fmt.Fprintf(env.stdout, "恢复会用这份备份覆盖数据库 %s，覆盖前会先备份当前数据库。\n输入数据库名 %s 确认：", preview.Database, preview.Database)
	confirmation := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(io.LimitReader(env.stdin, 4096)).ReadString('\n')
		confirmation <- strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	}()
	select {
	case <-ctx.Done():
		return 130
	case line := <-confirmation:
		if line != preview.Database {
			fmt.Fprintln(env.stderr, "已取消。")
			return 1
		}
	}
	result, err := session.Restore(ctx)
	if err != nil {
		return restoreCommandError(ctx, err, env)
	}
	fmt.Fprintf(env.stdout, "schema_migrations：最高版本 %s，%d 行\n", migrationVersion(result.Migrations), result.Migrations.Rows)
	for _, table := range result.Tables {
		if table.Missing {
			fmt.Fprintf(env.stdout, "%s：缺失\n", table.Name)
		} else {
			fmt.Fprintf(env.stdout, "%s：%d 行\n", table.Name, table.Rows)
		}
	}
	fmt.Fprintln(env.stdout, "已恢复。启动 app：docker compose up -d --wait")
	return 0
}
func migrationVersion(summary backup.MigrationSummary) string {
	if summary.Rows == 0 {
		return "无"
	}
	return strconv.FormatInt(summary.Version, 10)
}
func restoreCommandError(ctx context.Context, err error, env backupCommandEnv) int {
	var restore *backup.RestoreError
	if errors.As(err, &restore) {
		if restore.SafetyBackup != "" {
			fmt.Fprintln(env.stderr, restore.Error())
			fmt.Fprintf(env.stderr, "用刚才的恢复前备份还原：\n  docker compose run --rm app restore %s\n", restore.SafetyBackup)
			return 1
		}
		if ctx.Err() != nil {
			return 130
		}
		fmt.Fprintln(env.stderr, restore.Error())
		return 1
	}
	if ctx.Err() != nil {
		return 130
	}
	fmt.Fprintln(env.stderr, "无法准备数据库恢复。")
	return 1
}

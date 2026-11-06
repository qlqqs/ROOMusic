package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
)

const commandUsage = `用法：
  roomusic reset-password <用户名>
  roomusic backup
  roomusic restore <备份文件>
  roomusic --version`

func runCommand(args []string, env resetPasswordEnv) int {
	if len(args) == 0 {
		fmt.Fprintln(env.stderr, commandUsage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(env.stdout, commandUsage)
		return 0
	case "version", "--version":
		if len(args) != 1 {
			fmt.Fprintln(env.stderr, commandUsage)
			return 2
		}
		info := buildinfo.Current()
		version := info.Version
		if version == "" {
			version = "开发版"
		}
		if info.Commit != "" {
			fmt.Fprintf(env.stdout, "roomusic %s (%s)\n", version, info.Commit[:12])
		} else {
			fmt.Fprintf(env.stdout, "roomusic %s\n", version)
		}
		return 0
	case "reset-password":
		return runResetPassword(ctx, args[1:], env)
	case "backup":
		return runBackup(ctx, args[1:], defaultBackupCommandEnv(env))
	case "restore":
		return runRestore(ctx, args[1:], defaultBackupCommandEnv(env))
	default:
		fmt.Fprintf(env.stderr, "未知命令：%s\n%s\n", args[0], commandUsage)
		return 2
	}
}

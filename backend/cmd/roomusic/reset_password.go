package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"golang.org/x/term"
)

const resetPasswordUsage = "用法：roomusic reset-password <用户名>"

type resetPasswordEnv struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	isTerminal  func() bool
	readSecret  func(prompt string) (string, error)
	databaseURL func() (string, error)
	connect     func(ctx context.Context, url string) (*sql.DB, error)
}

func defaultResetPasswordEnv() resetPasswordEnv {
	return resetPasswordEnv{
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		isTerminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		readSecret: func(prompt string) (string, error) {
			fmt.Fprint(os.Stderr, prompt)
			secret, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			return string(secret), err
		},
		databaseURL: config.DatabaseURL,
		connect:     postgres.Connect,
	}
}

// runCLICommand 在进程退出前恢复终端，包括被信号中断的隐藏输入。
func runCLICommand(args []string) int {
	env := defaultResetPasswordEnv()
	if len(args) == 2 && args[0] == "reset-password" && env.isTerminal() {
		state, err := term.GetState(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Fprintln(env.stderr, "没有读到密码。")
			return 1
		}
		defer term.Restore(int(os.Stdin.Fd()), state)
	}
	return runCommand(args, env)
}

func runResetPassword(ctx context.Context, args []string, env resetPasswordEnv) int {
	if len(args) != 1 {
		fmt.Fprintln(env.stderr, resetPasswordUsage)
		return 2
	}
	password, message := readResetPassword(ctx, env)
	if ctx.Err() != nil {
		return 130
	}
	if message != "" {
		fmt.Fprintln(env.stderr, message)
		return 1
	}
	if !identity.ValidPassword(password) {
		fmt.Fprintln(env.stderr, strings.TrimPrefix(identity.PasswordRuleMessage, "新")+"。")
		return 1
	}
	databaseURL, err := env.databaseURL()
	if err != nil {
		fmt.Fprintln(env.stderr, "没有设置 ROOMUSIC_DATABASE_URL。")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	database, err := env.connect(ctx, databaseURL)
	if err != nil {
		return passwordCommandError(ctx, env, err, args[0], true)
	}
	defer database.Close()
	result, err := identity.ResetPasswordByUsername(ctx, database, args[0], password)
	if err != nil {
		return passwordCommandError(ctx, env, err, args[0], false)
	}
	fmt.Fprintf(env.stdout, "已重置“%s”的密码，这个账号已在所有设备上退出登录。\n", args[0])
	if result.Disabled {
		fmt.Fprintln(env.stdout, "这个账号现在是停用的，要在成员页重新启用后才能登录。")
	}
	return 0
}

type passwordInput struct {
	password string
	message  string
}

func readResetPassword(ctx context.Context, env resetPasswordEnv) (string, string) {
	// 输入不设超时；最多一个读取协程，让进程能在收到中断信号后恢复终端并退出。
	input := make(chan passwordInput, 1)
	go func() {
		password, message := readPasswordInput(ctx, env)
		input <- passwordInput{password: password, message: message}
	}()
	select {
	case <-ctx.Done():
		return "", ""
	case result := <-input:
		return result.password, result.message
	}
}

func readPasswordInput(ctx context.Context, env resetPasswordEnv) (string, string) {
	if env.isTerminal() {
		password, err := env.readSecret("新密码：")
		if err != nil {
			return "", "没有读到密码。"
		}
		if ctx.Err() != nil {
			return "", ""
		}
		confirmation, err := env.readSecret("再输入一次：")
		if err != nil {
			return "", "没有读到密码。"
		}
		if password != confirmation {
			return "", "两次输入的密码不一致。"
		}
		if password == "" {
			return "", "没有读到密码。"
		}
		return password, ""
	}
	line, err := bufio.NewReader(io.LimitReader(env.stdin, 4*1024)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", "没有读到密码。"
	}
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
	}
	if line == "" {
		return "", "没有读到密码。"
	}
	return line, ""
}

func passwordCommandError(ctx context.Context, env resetPasswordEnv, err error, username string, connecting bool) int {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return 130
	case connecting || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintln(env.stderr, "无法连接数据库。")
	case errors.Is(err, identity.ErrUserNotFound):
		fmt.Fprintf(env.stderr, "找不到用户“%s”。\n", username)
	default:
		fmt.Fprintln(env.stderr, "数据库出错，密码没有修改。")
	}
	return 1
}

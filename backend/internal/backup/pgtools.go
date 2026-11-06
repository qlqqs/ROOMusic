package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Command 的流由调用方拥有。Env 是完整环境，不与父进程合并。
type Command struct {
	Path   string
	Args   []string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
}

// Runner 隔离进程效果；测试替身可以检查参数、写入输出或等待上下文取消。
type Runner interface {
	Run(context.Context, Command) error
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) error {
	path := command.Path
	if !filepath.IsAbs(path) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return failure("prepare", "tool_missing")
		}
		path = resolved
	}
	cmd := exec.CommandContext(ctx, path, command.Args...)
	cmd.Env = append([]string{}, command.Env...)
	cmd.Stdin, cmd.Stdout = command.Stdin, command.Stdout
	stderr := &boundedBuffer{limit: 64 * 1024}
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		reason := classifyToolError(stderr.String())
		if exit != nil {
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				reason = "killed"
			}
		}
		if ctx.Err() != nil {
			reason = "killed"
		}
		return &Error{Reason: reason, ExitCode: code}
	}
	return nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

// 不嵌入 bytes.Buffer，避免其 ReadFrom 被 io.Copy 优先调用而绕过上限。
func (b *boundedBuffer) String() string { return b.buffer.String() }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := b.limit - b.buffer.Len()
	if len(p) > room {
		p = p[:room]
		b.exceeded = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
func classifyToolError(text string) string {
	text = strings.ToLower(text)
	switch {
	case strings.Contains(text, "could not obtain lock"), strings.Contains(text, "lock timeout"):
		return "lock_timeout"
	case strings.Contains(text, "password authentication failed"), strings.Contains(text, "no password supplied"):
		return "auth_failed"
	case strings.Contains(text, "server version mismatch"), strings.Contains(text, "unsupported version"):
		return "version_mismatch"
	case strings.Contains(text, "no space left"):
		return "disk_full"
	case strings.Contains(text, "connection"), strings.Contains(text, "could not translate host"):
		return "connect_failed"
	default:
		return "other"
	}
}

func connectionArguments(raw string) (string, []string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.Fragment != "" {
		return "", nil, failure("prepare", "unsupported_database_url")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", nil, failure("prepare", "unsupported_database_url")
	}
	password := ""
	if parsed.User != nil {
		password, _ = parsed.User.Password()
		parsed.User = url.User(parsed.User.Username())
	}
	if query.Has("password") {
		password = query.Get("password")
	}
	clean := url.Values{}
	for _, key := range []string{"sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout", "target_session_attrs", "options"} {
		if query.Has(key) {
			clean.Set(key, query.Get(key))
		}
	}
	parsed.RawQuery = clean.Encode()
	env := []string{"PGAPPNAME=roomusic-backup", "PGCONNECT_TIMEOUT=10", "LC_ALL=C"}
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	return parsed.String(), env, nil
}

func (s *Service) tool(name string) string {
	if s.settings.PGBinDirectory != "" {
		return filepath.Join(s.settings.PGBinDirectory, name)
	}
	return name
}
func (s *Service) command(ctx context.Context, stage, tool string, args []string, env []string, stdin io.Reader, stdout io.Writer) error {
	err := s.runner.Run(ctx, Command{Path: s.tool(tool), Args: args, Env: env, Stdin: stdin, Stdout: stdout})
	if err == nil {
		return nil
	}
	result := &Error{Stage: stage, Reason: "other", ExitCode: -1}
	var classified *Error
	if errors.As(err, &classified) {
		result.Reason = classified.Reason
		result.ExitCode = classified.ExitCode
	}
	if ctx.Err() != nil {
		result.Reason = "killed"
	}
	return result
}

var toolVersionPattern = regexp.MustCompile(`\(PostgreSQL\) ([0-9]+)\.`)

func (s *Service) prepare(ctx context.Context, serverMajor int) (string, []string, error) {
	address, env, err := connectionArguments(s.settings.DatabaseURL)
	if err != nil {
		return "", nil, err
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		output := &boundedBuffer{limit: 4096}
		if err := s.command(ctx, "prepare", tool, []string{"--version"}, env, nil, output); err != nil {
			return "", nil, err
		}
		match := toolVersionPattern.FindStringSubmatch(output.String())
		if len(match) != 2 {
			return "", nil, failure("prepare", "version_mismatch")
		}
		major, _ := strconv.Atoi(match[1])
		if major < serverMajor {
			return "", nil, failure("prepare", "version_mismatch")
		}
	}
	return address, env, nil
}

func (s *Service) verify(ctx context.Context, file *os.File, env []string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return failure("verify", "other")
	}
	return s.command(ctx, "verify", "pg_restore", []string{"--list"}, env, file, io.Discard)
}

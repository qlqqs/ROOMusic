// Package backup 拥有数据库备份、保留、调度与恢复策略，不包含 HTTP。
package backup

import (
	"errors"
	"fmt"
	"time"
)

type Source string

const (
	SourceScheduled    Source = "scheduled"
	SourceManual       Source = "manual"
	SourcePreMigration Source = "pre_migration"
	SourcePreRestore   Source = "pre_restore"
)

var (
	ErrBusy     = errors.New("另一份备份正在进行")
	ErrNotFound = errors.New("备份不存在")
)

type File struct {
	Name      string    `json:"name"`
	Source    Source    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}
type Running struct {
	Source    Source    `json:"source"`
	StartedAt time.Time `json:"started_at"`
}
type Failure struct {
	At     time.Time `json:"at"`
	Stage  string    `json:"stage"`
	Source Source    `json:"source"`
}
type Status struct {
	Running         *Running   `json:"running"`
	Directory       string     `json:"directory"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	LastFailure     *Failure   `json:"last_failure"`
	NextScheduledAt *time.Time `json:"next_scheduled_at"`
}
type Overview struct {
	Status  Status `json:"status"`
	Backups []File `json:"backups"`
	Total   int    `json:"total"`
}

// Error 只暴露分类，绝不携带子进程原文、连接串或密码。
type Error struct {
	Stage, Reason string
	ExitCode      int
}

func (e *Error) Error() string {
	return fmt.Sprintf("备份失败（阶段：%s，原因：%s）", e.Stage, e.Reason)
}
func failure(stage, reason string) error { return &Error{Stage: stage, Reason: reason, ExitCode: -1} }

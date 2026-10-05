package playback

import (
	"context"
	"errors"
	"io"
	"time"
)

// DefaultRecheckInterval 是已建立的传输复核授权与来源的间隔：撤销会话、停用成员或停用目录后，
// 服务端最迟约这么久切断连接（不含浏览器已缓冲的音频，那部分无法收回）。
const DefaultRecheckInterval = 10 * time.Second

// AbortReason 是传输被服务端中止的原因，写入 playback.stream.aborted 日志；只描述类别，不含路径。
type AbortReason string

const (
	AbortSessionInvalid   AbortReason = "session_invalid"
	AbortTrackUnavailable AbortReason = "track_unavailable"
	AbortRootDisabled     AbortReason = "root_disabled"
	AbortCheckFailed      AbortReason = "check_failed"
	AbortShutdown         AbortReason = "shutdown"
)

// AbortError 是复核失败使 Read 返回的错误。Check 返回它可以指明原因；返回其他错误时按 AbortCheckFailed 处理。
type AbortError struct {
	Reason AbortReason
	Err    error
}

func (abort *AbortError) Error() string {
	if abort.Err == nil {
		return "playback stream aborted: " + string(abort.Reason)
	}
	return "playback stream aborted: " + string(abort.Reason) + ": " + abort.Err.Error()
}

func (abort *AbortError) Unwrap() error { return abort.Err }

// ReasonFor 把 Service.StillPlayable 返回的错误归为中止原因：曲目不再 present 是 track_unavailable，
// 目录被停用是 root_disabled，其他（数据库不可用等）都按失败即关闭处理为 check_failed。
func ReasonFor(err error) AbortReason {
	switch {
	case errors.Is(err, ErrTrackNotFound):
		return AbortTrackUnavailable
	case errors.Is(err, ErrRootDisabled):
		return AbortRootDisabled
	default:
		return AbortCheckFailed
	}
}

// Check 复核一次授权与来源：nil 表示可以继续，任何错误都会中止传输（失败即关闭，数据库不可用也不放行）。
type Check func() error

// RecheckingReader 包装已打开的音频文件，供 http.ServeContent 读取。距上次复核满 interval 时，
// 在下一次 Read 里同步复核；复核失败，或应用生命周期上下文已结束（进程关停），Read 返回 *AbortError 并且之后一直如此。
// ServeContent 停止写出后响应长度不足，net/http 会关闭连接。它只在处理请求的 goroutine 里使用，不是并发安全的。
type RecheckingReader struct {
	file      io.ReadSeeker
	lifecycle context.Context
	interval  time.Duration
	now       func() time.Time
	check     Check
	checkedAt time.Time
	aborted   *AbortError
}

// NewRecheckingReader 返回读取器；创建时刻视为刚完成一次复核（请求刚经过鉴权与来源核对）。
func NewRecheckingReader(file io.ReadSeeker, lifecycle context.Context, interval time.Duration, now func() time.Time, check Check) *RecheckingReader {
	return &RecheckingReader{file: file, lifecycle: lifecycle, interval: interval, now: now, check: check, checkedAt: now()}
}

// Read 实现 io.Reader。
func (reader *RecheckingReader) Read(buffer []byte) (int, error) {
	if reader.aborted != nil {
		return 0, reader.aborted
	}
	if err := reader.lifecycle.Err(); err != nil {
		reader.aborted = &AbortError{Reason: AbortShutdown, Err: err}
		return 0, reader.aborted
	}
	if current := reader.now(); current.Sub(reader.checkedAt) >= reader.interval {
		if err := reader.check(); err != nil {
			var abort *AbortError
			if !errors.As(err, &abort) {
				abort = &AbortError{Reason: AbortCheckFailed, Err: err}
			}
			reader.aborted = abort
			return 0, abort
		}
		reader.checkedAt = current
	}
	return reader.file.Read(buffer)
}

// Seek 实现 io.Seeker，直接透传：定位本身不复核，读取才复核。
func (reader *RecheckingReader) Seek(offset int64, whence int) (int64, error) {
	return reader.file.Seek(offset, whence)
}

// Aborted 报告传输是否被服务端中止及原因，供处理请求的 goroutine 在 ServeContent 返回后记日志。
func (reader *RecheckingReader) Aborted() (AbortReason, bool) {
	if reader.aborted == nil {
		return "", false
	}
	return reader.aborted.Reason, true
}

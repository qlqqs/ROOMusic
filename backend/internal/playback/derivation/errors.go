// Package derivation 拥有“把浏览器不能直接播放的无损源文件转换成经过核对的 FLAC，并受管地缓存”的机制：
// 缓存目录与按最近使用淘汰、按键合并的转换任务（等待者、无人等待后的宽限期取消、并发与排队上限、失败记忆）、
// ffmpeg 适配与输出核对。它不含 HTTP，不读数据库，不知道曲目与权限：调用方（playback）提供源文件的版本事实与
// “安全打开并核对”的回调，取回缓存中完整且已核对的 FLAC。本包不依赖任何内部包。
package derivation

import (
	"errors"
	"fmt"
)

var (
	// ErrBusy 表示并发、排队、单用户等待或剩余磁盘空间的上限已满；调用方应让客户端稍后重试。
	ErrBusy = errors.New("derivation busy")
	// ErrClosed 表示管理器已关闭（应用关停）。
	ErrClosed = errors.New("derivation closed")
	// ErrNotCached 表示缓存中没有该键的完成品。
	ErrNotCached = errors.New("derivation not cached")

	// errNoWaiters 是任务因最后一个等待者离开并超过宽限期而被取消的原因。
	errNoWaiters = errors.New("derivation has no waiters")
)

// 转换失败的类别，也是 playback.derivation.failed 事件的 category 字段值。
const (
	FailureFFmpegExit     = "ffmpeg_exit"
	FailureVerifyMismatch = "verify_mismatch"
	FailureTimeout        = "timeout"
	FailureOutputLimit    = "output_limit"
	FailureIO             = "io"
)

// FailedError 表示一次转换失败：ffmpeg 退出码非零、核对不一致、超时、输出超限或本地读写失败。
// 来源文件版本问题（被改写、无法打开）不是转换失败，由调用方的回调返回自己的错误。
// 同一个键在失败记忆期内的重复请求直接得到同一个类别。Error 不含 ffmpeg 的 stderr 与路径；
// Stderr 只保留末尾几 KiB 供测试与调试读取，调用方不得把它写进日志或响应。
type FailedError struct {
	Category string
	Err      error
	Stderr   string
}

func (e *FailedError) Error() string {
	if e.Err == nil {
		return "derivation failed (" + e.Category + ")"
	}
	return fmt.Sprintf("derivation failed (%s): %v", e.Category, e.Err)
}

func (e *FailedError) Unwrap() error { return e.Err }

// ffmpeg 不可用的原因类别，也是 playback.derivation.unavailable 事件的 reason 字段值。
const (
	ReasonNotFound        = "not_found"
	ReasonVersionFailed   = "version_failed"
	ReasonSelfTestFailed  = "selftest_failed"
	ReasonUnsupportedHost = "unsupported_platform"
)

// UnavailableError 表示派生不可用：找不到可执行文件、版本探测失败或启动自检失败。应用照常启动，只是不提供派生。
type UnavailableError struct {
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string {
	if e.Err == nil {
		return "derivation unavailable (" + e.Reason + ")"
	}
	return fmt.Sprintf("derivation unavailable (%s): %v", e.Reason, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// canceledError 表示任务被取消；reason 是 playback.derivation.canceled 事件的 reason 字段值。
type canceledError struct{ reason string }

func (e *canceledError) Error() string { return "derivation canceled: " + e.reason }

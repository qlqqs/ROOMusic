package render

import (
	"context"
	"errors"
	"os"
)

// TrackSource 由装配层提供安全打开与分析结果，不向渲染层泄露目录存储实现。
type TrackSource interface {
	Prepare(ctx context.Context, userID, trackID string) (Track, error)
}

// Track 的 File 由渲染会话关闭；帧范围按源 PCM 采样率计，不是 CUE 的 75 Hz 索引。
type Track struct {
	File                           *os.File
	SourceKind, Codec              string
	SampleRate, Channels, BitDepth int
	IsFloat                        bool
	DurationSeconds                float64
	ReleaseID                      string
	LeadingSilence, TrailingStart  *float64
	TrackGainDB, AlbumGainDB       *float64
	CueStartFrame, CueEndFrame     *int64
}

// SourceError 只携带可公开的失败分类，底层原因留在错误链中。
type SourceError struct {
	Reason string
	Err    error
}

func (e *SourceError) Error() string         { return "render: " + e.Reason }
func (e *SourceError) Unwrap() error         { return e.Err }
func (e *SourceError) FailureReason() string { return e.Reason }

func failureReason(err error) string {
	var source interface{ FailureReason() string }
	if errors.As(err, &source) {
		switch source.FailureReason() {
		case "not_found", "root_disabled", "unsupported_format", "source_unavailable", "source_changed", "decode_failed":
			return source.FailureReason()
		}
	}
	return "transient"
}

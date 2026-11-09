package zones

import (
	"errors"
	"io"
	"sync/atomic"
	"time"
)

var (
	ErrUnsupportedProtocol = errors.New("unsupported_protocol")
	ErrOutputSuperseded    = errors.New("output_superseded")
	ErrOutputUnknown       = errors.New("output_unknown")
	ErrStreamGone          = errors.New("stream_gone")
)

const (
	OutputLease            = 45 * time.Second
	OfflineScanInterval    = 5 * time.Second
	TakeoverWindow         = 10 * time.Second
	PositionSaveInterval   = 10 * time.Second
	DeviceSeenSaveInterval = 5 * time.Minute
)

type PlayingOutput struct {
	EntryID         string  `json:"entry_id"`
	PositionSeconds float64 `json:"position_seconds"`
}
type ClaimOutputInput struct {
	Protocol     int            `json:"protocol"`
	Capabilities Capabilities   `json:"capabilities"`
	Takeover     bool           `json:"takeover"`
	Playing      *PlayingOutput `json:"playing"`
}
type ReportOutputInput struct {
	Kind          string  `json:"kind"`
	StreamSeq     *int64  `json:"stream_seq"`
	StreamSeconds float64 `json:"stream_seconds"`
	Status        string  `json:"status"`
	Fault         *string `json:"fault"`
}
type ReleaseOutputInput struct {
	StreamSeq     *int64  `json:"stream_seq"`
	StreamSeconds float64 `json:"stream_seconds"`
}
type OutputResult struct {
	ServerTime Timestamp `json:"server_time"`
	Device     Device    `json:"device"`
	Zone       Zone      `json:"zone"`
}
type ClaimOutputResult struct {
	ServerTime Timestamp `json:"server_time"`
	Protocol   int       `json:"protocol"`
	SessionID  string    `json:"session_id"`
	Device     Device    `json:"device"`
	Zone       Zone      `json:"zone"`
}
type StreamBody struct {
	Body     io.Reader
	MimeType string
}

// 身份索引的归属与指针不可变；可变字段由对应播放区锁保护。
type outputSession struct {
	id, userID  string
	zone        *zoneRuntime
	state       string
	lastReport  time.Time
	connections atomic.Int64
}

func (s *Service) Done() <-chan struct{}                     { return s.ctx.Done() }
func (s *Service) Now() time.Time                            { return s.options.Now() }
func (s *Service) AfterFunc(d time.Duration, f func()) Timer { return s.options.AfterFunc(d, f) }

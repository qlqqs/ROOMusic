// Package zonestest 提供仅供测试与开发装配使用的静音渲染器。
package zonestest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

// Options 的时长以曲目 ID 为键；未指定的曲目用 DefaultDuration（缺省一分钟）。
// Realtime 按 Observe 的播放状态每 200 ms 推进；关闭时由 Advance 或 Emit 驱动。
// OpenError 与 NegotiateError 用于脚本化依赖故障，不模拟曲目级错误。
type Options struct {
	Now             func() time.Time
	Durations       map[string]time.Duration
	DefaultDuration time.Duration
	Realtime        bool
	OpenError       error
	NegotiateError  error
}

// Renderer 保存已打开的会话，供测试检查 RenderSpec 与生命周期。
type Renderer struct {
	options  Options
	mu       sync.Mutex
	sessions []*Session
}

func NewRenderer(options Options) *Renderer {
	durations := make(map[string]time.Duration, len(options.Durations))
	for id, duration := range options.Durations {
		durations[id] = duration
	}
	options.Durations = durations
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.DefaultDuration <= 0 {
		options.DefaultDuration = time.Minute
	}
	return &Renderer{options: options}
}

func (r *Renderer) Negotiate(_ zones.Capabilities, _ string) (zones.StreamFormat, error) {
	if r.options.NegotiateError != nil {
		return zones.StreamFormat{}, r.options.NegotiateError
	}
	return zones.StreamFormat{Codec: "wav", MimeType: "audio/wav", Quality: "lossless"}, nil
}

func (r *Renderer) Open(ctx context.Context, spec zones.RenderSpec) (zones.RenderSession, error) {
	if r.options.OpenError != nil {
		return nil, r.options.OpenError
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := newSession(ctx, spec, r.options)
	r.mu.Lock()
	r.sessions = append(r.sessions, s)
	r.mu.Unlock()
	return s, nil
}

func (r *Renderer) Sessions() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Session(nil), r.sessions...)
}

var ErrClosed = errors.New("fake render session closed")

var _ zones.Renderer = (*Renderer)(nil)

package render

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dsp/chain"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// Ticker 与 Clock 同时注入，避免测试用真实时间等待派发与领先窗口。
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
type wallTicker struct{ *time.Ticker }

func (t wallTicker) C() <-chan time.Time { return t.Ticker.C }

type Options struct {
	FFmpeg    *ffmpeg.Tool
	Sources   TrackSource
	Clock     func() time.Time
	NewTicker func(time.Duration) Ticker
	Logger    *slog.Logger
}

type Renderer struct {
	options   Options
	mu        sync.Mutex
	closed    bool
	sessions  map[*session]struct{}
	workers   sync.WaitGroup
	lifecycle context.Context
	cancel    context.CancelFunc
}

func New(options Options) *Renderer {
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.NewTicker == nil {
		options.NewTicker = func(d time.Duration) Ticker { return wallTicker{time.NewTicker(d)} }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	return &Renderer{options: options, sessions: make(map[*session]struct{}), lifecycle: lifecycle, cancel: cancel}
}

func (r *Renderer) Open(ctx context.Context, spec zones.RenderSpec) (zones.RenderSession, error) {
	r.mu.Lock()
	if r.closed || r.options.FFmpeg == nil || r.options.Sources == nil {
		r.mu.Unlock()
		return nil, ErrUnavailable
	}
	r.workers.Add(1)
	r.mu.Unlock()
	defer r.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.lifecycle, cancel)
	handedOff := false
	defer func() {
		if !handedOff {
			stop()
			cancel()
		}
	}()
	if math.IsNaN(spec.StartSeconds) || math.IsInf(spec.StartSeconds, 0) || spec.StartSeconds < 0 {
		return nil, errors.New("render: 起始位置无效")
	}
	if spec.Settings.Leveling == "" {
		spec.Settings.Leveling = "off"
	}
	switch spec.Settings.Leveling {
	case "off", "track", "album", "auto":
	default:
		return nil, errors.New("render: 电平化模式无效")
	}
	count := 2
	if spec.Format.Codec == "wav" {
		count = 1
	}
	reservation, err := r.options.FFmpeg.Reserve(ctx, count)
	if err != nil {
		return nil, errors.Join(ErrBusy, err)
	}
	defer reservation.Close()
	source, prepareErr := r.options.Sources.Prepare(ctx, spec.UserID, spec.Start.TrackID)
	if prepareErr == nil {
		prepareErr = validateTrack(source)
	}
	if prepareErr != nil && source.File != nil {
		_ = source.File.Close()
		source.File = nil
	}
	if prepareErr != nil && failureReason(prepareErr) == "transient" {
		return nil, prepareErr
	}
	var format zones.StreamFormat
	if spec.Format.Delivery == "hls" {
		format.Delivery, format.MimeType = spec.Format.Delivery, spec.Format.MimeType
	}
	var pcm chain.Format
	if prepareErr == nil {
		format, pcm, err = concreteFormat(spec, source)
		if err != nil {
			if source.File != nil {
				_ = source.File.Close()
			}
			return nil, err
		}
	}
	s, err := newSession(ctx, r, spec, format, pcm, source, prepareErr, reservation)
	if err != nil {
		if source.File != nil {
			_ = source.File.Close()
		}
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		s.Close()
		s.start()
		<-s.done
		return nil, ErrUnavailable
	}
	r.sessions[s] = struct{}{}
	r.workers.Add(1)
	r.mu.Unlock()
	s.start()
	handedOff = true
	go func() {
		defer stop()
		defer cancel()
		defer r.workers.Done()
		<-s.done
		r.mu.Lock()
		delete(r.sessions, s)
		r.mu.Unlock()
	}()
	return s, nil
}

func validateTrack(t Track) error {
	if t.File == nil || t.SampleRate < 5 || t.SampleRate > 768000 || t.Channels < 1 || t.Channels > 32 || t.BitDepth < 0 || t.BitDepth > 64 {
		return &SourceError{Reason: "unsupported_format"}
	}
	return nil
}

// Close 在 zones 释放全部播放区锁后调用，等待子进程和会话协程全部回收。
func (r *Renderer) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	for s := range r.sessions {
		s.Close()
	}
	r.mu.Unlock()
	r.workers.Wait()
}

var _ zones.Renderer = (*Renderer)(nil)

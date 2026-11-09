package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/playback/render"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

// OpenRenderer 完成探测与无损往返自检；工具不可用时应用仍可启动，但不装配渲染器。
func OpenRenderer(ctx context.Context, settings config.Server, logger *slog.Logger) (*ffmpeg.Tool, error) {
	if logger == nil {
		logger = slog.Default()
	}
	tool, err := ffmpeg.Open(ctx, settings.FFmpegPath)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var unavailable *ffmpeg.UnavailableError
	if errors.As(err, &unavailable) {
		logger.Warn("渲染器不可用", "event", "playback.render.unavailable", "module", "playback", "reason", unavailable.Reason)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open renderer: %w", err)
	}
	logger.Info("渲染器就绪", "event", "playback.render.ready", "module", "playback", "ffmpeg_version", tool.Version(), "encoders", tool.Encoders(), "soxr", tool.Soxr())
	return tool, nil
}

// WithRenderer 注入已经通过启动自检的 ffmpeg；真实会话由 app 组合来源适配器后创建。
func WithRenderer(tool *ffmpeg.Tool) Option {
	return func(o *options) { o.renderer = tool }
}

func newPlaybackService(database *sql.DB, settings config.Server, assembly options) *playback.Service {
	var opts []playback.ServiceOption
	if assembly.renderer != nil {
		opts = append(opts, playback.WithRenderDecoders(assembly.renderer))
	}
	return playback.NewService(database, settings.AllowedLibraryRoots, opts...)
}

func assembleRenderer(database *sql.DB, service *playback.Service, tool *ffmpeg.Tool, logger *slog.Logger) (zones.Renderer, func()) {
	if tool == nil || database == nil {
		return nil, func() {}
	}
	renderer := render.New(render.Options{
		FFmpeg:  tool,
		Sources: renderTrackSource{playback: service, catalog: catalog.NewStore(database)},
		Logger:  logger,
	})
	return renderer, renderer.Close
}

// renderTrackSource 仅组合公开读取合同；调用方完成鉴权，playback 负责文件核验。
type renderTrackSource struct {
	playback *playback.Service
	catalog  *catalog.Store
}

func (s renderTrackSource) Prepare(ctx context.Context, userID, trackID string) (render.Track, error) {
	opened, err := s.playback.OpenForRender(ctx, trackID)
	if err != nil {
		return render.Track{}, err
	}
	track, err := s.withAnalysis(ctx, trackID, opened)
	if err != nil {
		_ = opened.File.Close()
		return render.Track{}, err
	}
	return track, nil
}

func (s renderTrackSource) withAnalysis(ctx context.Context, trackID string, opened *playback.RenderSource) (render.Track, error) {
	placements, err := s.catalog.LoadTrackPlacements(ctx, []string{trackID})
	if err != nil {
		return render.Track{}, &render.SourceError{Reason: "transient", Err: err}
	}
	placement, ok := placements[opened.TrackID]
	if !ok {
		return render.Track{}, &render.SourceError{Reason: "not_found", Err: catalog.ErrTrackNotFound}
	}
	result, err := s.catalog.LoadTrackAnalysis(ctx, trackID, analysis.Version)
	if err != nil {
		reason := "transient"
		if errors.Is(err, catalog.ErrAnalysisTrackNotFound) {
			reason = "not_found"
		}
		return render.Track{}, &render.SourceError{Reason: reason, Err: err}
	}
	track := render.Track{
		File: opened.File, SourceKind: opened.SourceKind, Codec: opened.Codec,
		SampleRate: opened.SampleRate, Channels: opened.Channels, BitDepth: opened.BitDepth, IsFloat: opened.IsFloat,
		DurationSeconds: opened.DurationSeconds, ReleaseID: placement.Release.ID,
		CueStartFrame: opened.CueStartFrame, CueEndFrame: opened.CueEndFrame,
	}
	if result.Analysis != nil {
		track.TrackGainDB = result.Analysis.TrackGainDB
		track.LeadingSilence = renderSeconds(result.Analysis.LeadingSilenceMS)
		track.TrailingStart = renderSeconds(result.Analysis.TrailingSilenceMS)
	}
	if result.Album != nil {
		track.AlbumGainDB = &result.Album.GainDB
	}
	return track, nil
}

func renderSeconds(milliseconds *int32) *float64 {
	if milliseconds == nil {
		return nil
	}
	seconds := float64(*milliseconds) / 1000
	return &seconds
}

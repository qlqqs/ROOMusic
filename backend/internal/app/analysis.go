package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
)

// OpenAnalysis 定位 ffmpeg 并自检后台分析链路；不可用时只记录原因并保持应用可启动。
func OpenAnalysis(ctx context.Context, settings config.Server, logger *slog.Logger) (*analysis.Analyzer, error) {
	if !settings.AudioAnalysis {
		logger.Info("playback analysis unavailable", "event", "playback.analysis.unavailable", "reason", "disabled")
		return nil, nil
	}
	analyzer, err := analysis.Open(ctx, settings.FFmpegPath)
	var unavailable *analysis.UnavailableError
	if errors.As(err, &unavailable) {
		logger.Warn("playback analysis unavailable", "event", "playback.analysis.unavailable", "reason", unavailable.Reason)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open audio analyzer: %w", err)
	}
	logger.Info("playback analysis ready", "event", "playback.analysis.ready", "ffmpeg_version", analyzer.FFmpegVersion())
	return analyzer, nil
}

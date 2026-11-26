package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
	"github.com/qlqq/roomusic/backend/internal/sources/acoustid"
)

// OpenFingerprinter 自检本地指纹管道并记录启动状态。
func OpenFingerprinter(ctx context.Context, settings config.Server, logger *slog.Logger) (*fingerprint.Fingerprinter, error) {
	tool, err := fingerprint.Open(ctx, settings.FFmpegPath, settings.FPCalcPath)
	var unavailable *fingerprint.UnavailableError
	if errors.As(err, &unavailable) {
		logger.Warn("声学指纹工具不可用", "event", "playback.fingerprint.unavailable", "reason", unavailable.Reason)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	logger.Info("声学指纹工具就绪", "event", "playback.fingerprint.ready", "fpcalc_version", tool.FPCalcVersion())
	return tool, nil
}

// WithFingerprinter 装配通过自检的本地声学指纹工具。
func WithFingerprinter(tool *fingerprint.Fingerprinter) Option {
	return func(o *options) { o.fingerprinter = tool }
}

// WithAcoustID 设置 AcoustID 的服务地址与出站选项。
func WithAcoustID(opts acoustid.Options) Option {
	return func(o *options) { o.acoustid = opts }
}

type programFingerprints struct{ prints *playback.Fingerprints }

func (p programFingerprints) Fingerprint(ctx context.Context, trackID string) (program.TrackFingerprint, program.FingerprintStatus, error) {
	result, status, err := p.prints.Fingerprint(ctx, trackID)
	print := program.TrackFingerprint{Value: result.Value, DurationSeconds: result.DurationSeconds}
	return print, program.FingerprintStatus(status), err
}

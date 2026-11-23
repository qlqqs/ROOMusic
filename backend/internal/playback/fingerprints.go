package playback

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
)

type FingerprintStatus int

const (
	FingerprintOK FingerprintStatus = iota
	FingerprintPermanent
	FingerprintTransient
)

type TrackFingerprint struct {
	Value           string
	DurationSeconds int
	RetryAfter      time.Duration
}

type Fingerprints struct {
	database catalog.Queryer
	store    *catalog.Store
	roots    []string
	runner   *fingerprint.Fingerprinter
	logger   *slog.Logger
}

func NewFingerprints(database catalog.Queryer, allowedRoots []string, runner *fingerprint.Fingerprinter, logger *slog.Logger) *Fingerprints {
	return &Fingerprints{database: database, store: catalog.NewStore(database), roots: allowedRoots, runner: runner, logger: logger}
}

// Fingerprint 按需计算；缺失或变化的来源下轮可重试，取消与数据库错误中止调用。
func (p *Fingerprints) Fingerprint(ctx context.Context, trackID string) (TrackFingerprint, FingerprintStatus, error) {
	src, err := p.store.LoadFingerprintSource(ctx, trackID, fingerprint.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackFingerprint{}, FingerprintTransient, nil
	}
	if err != nil {
		return TrackFingerprint{}, FingerprintTransient, err
	}
	belowMinimumDuration := src.ExactDurationSeconds >= 0 && src.ExactDurationSeconds < 10
	if src.Cached != nil && (!belowMinimumDuration || src.Cached.FailureCategory == "too_short") {
		if src.Cached.FailureCategory != "" {
			return TrackFingerprint{}, FingerprintPermanent, nil
		}
		return TrackFingerprint{Value: src.Cached.Value, DurationSeconds: src.Cached.DurationSeconds}, FingerprintOK, nil
	}

	if src.ExactDurationSeconds < 0 {
		// 扫描与分析都还没有测出时长，下次读到时长后再计算。
		return TrackFingerprint{}, FingerprintTransient, nil
	}

	file, err := openAnalysisSource(ctx, p.database, p.roots, src.TrackAnalysisCandidate)
	if err != nil {
		if errors.Is(err, errAnalysisDatabase) {
			return TrackFingerprint{}, FingerprintTransient, err
		}
		return TrackFingerprint{}, FingerprintTransient, ctx.Err()
	}
	defer file.Close()
	var value string
	if belowMinimumDuration {
		err = fingerprint.ErrTooShort
	} else {
		value, err = p.calculate(ctx, file, src.TrackAnalysisCandidate)
	}
	if ctx.Err() != nil {
		return TrackFingerprint{}, FingerprintTransient, ctx.Err()
	}

	status := FingerprintOK
	result := catalog.AcousticFingerprint{Value: value, DurationSeconds: src.DurationSeconds}
	if err != nil {
		category := "decode_failed"
		status = FingerprintTransient
		var prepare *analysis.Failure
		switch {
		case errors.As(err, &prepare) && (prepare.Category == "source_unavailable" || prepare.Category == "source_changed"):
			return TrackFingerprint{}, FingerprintTransient, nil
		case errors.Is(err, fingerprint.ErrTooShort):
			category = "too_short"
			status = FingerprintPermanent
		case errors.Is(err, context.DeadlineExceeded):
			category = "timeout"
		}
		result = catalog.AcousticFingerprint{FailureCategory: category}
		if status != FingerprintPermanent {
			p.logger.WarnContext(ctx, "曲目声学指纹计算失败", "event", "playback.fingerprint.failed", "track_id", trackID, "category", category, "error", err)
		}
	}
	saved, saveErr := p.store.SaveAcousticFingerprint(ctx, src, fingerprint.Version, result, belowMinimumDuration)
	if !saved || saveErr != nil {
		return TrackFingerprint{}, FingerprintTransient, saveErr
	}
	if status == FingerprintTransient {
		if src.Attempts+1 >= 3 {
			return TrackFingerprint{}, FingerprintPermanent, nil
		}
		return TrackFingerprint{RetryAfter: 24 * time.Hour}, status, nil
	}
	return TrackFingerprint{Value: result.Value, DurationSeconds: result.DurationSeconds}, status, nil
}

func (p *Fingerprints) calculate(ctx context.Context, file *os.File, track catalog.TrackAnalysisCandidate) (string, error) {
	layout, err := analysisDSDLayout(file, track)
	if err != nil {
		return "", err
	}
	if layout == nil {
		if track.SourceKind == "cue_virtual" {
			return p.runner.File(ctx, file, track.CueIndexFrames, track.CueEndFrames)
		}
		return p.runner.File(ctx, file, nil, nil)
	}
	pcm, input, err := newAnalysisDSDStream(ctx, file, *layout, track)
	if err != nil {
		return "", err
	}
	return p.runner.PCM(ctx, pcm, input.SampleRate, input.Channels)
}

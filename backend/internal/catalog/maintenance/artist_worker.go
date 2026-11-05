package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func runArtistsLocked(ctx context.Context, conn *sql.Conn, logger *slog.Logger, options RunOptions) (err error) {
	state, err := loadArtistState(ctx, conn)
	if err != nil {
		return err
	}
	if state.Ready() {
		return nil
	}
	if !state.CurrentVersion() {
		return errors.New("catalog artist projection version mismatch")
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			markArtistsFailed(ctx, conn, state)
		}
	}()
	if err = textnorm.LoadEmbedded(); err != nil {
		return err
	}
	logger.InfoContext(ctx, "开始目录艺人投影维护", "event", "catalog_projection_backfill_started", "module", "catalog",
		"capability", "artists", "build_id", state.BuildID, "target_generation", state.TargetGeneration, "textnorm_version", state.TextnormVersion, "artist_projection_version", state.SearchProjectionVersion)
	// 同版本旧构建可能已封闭发行清单，但尚未登记独立 profiles 阶段。
	if err = discoverArtists(ctx, conn, state, false); err != nil {
		return err
	}
	releaseLimit, profileLimit := 10, 10
	releaseBudget := initialArtistReleaseBudget()
	averages := map[string]float64{}
	for {
		workload := &artistReleaseBatch{Budget: releaseBudget}
		result, err := processArtistBatch(ctx, conn, state, releaseLimit, profileLimit, workload)
		if err != nil {
			return err
		}
		yield := batchYield(result.Duration)
		if result.BatchSize == 0 {
			published, err := publishArtistsOrRepair(ctx, conn, state, options)
			if err != nil {
				return err
			}
			if published {
				logger.InfoContext(ctx, "目录艺人投影已发布", "event", "catalog_projection_published", "module", "catalog",
					"capability", "artists", "build_id", state.BuildID, "target_generation", state.TargetGeneration, "elapsed_ms", time.Since(state.StartedAt).Milliseconds())
				return nil
			}
		} else {
			averagePerSubject := averages[result.Kind]
			sample := float64(result.Duration+yield) / float64(result.BatchSize)
			if averagePerSubject == 0 {
				averagePerSubject = sample
			} else {
				averagePerSubject = 0.8*averagePerSubject + 0.2*sample
			}
			averages[result.Kind] = averagePerSubject
			current, err := loadArtistState(ctx, conn)
			if err != nil {
				return err
			}
			attributes := []any{"event", "catalog_projection_backfill_batch", "module", "catalog",
				"capability", "artists", "build_id", state.BuildID, "target_generation", state.TargetGeneration, "textnorm_version", state.TextnormVersion,
				"artist_projection_version", state.SearchProjectionVersion, "phase", result.Phase, "completed", result.Completed, "total", result.Total,
				"batch_size", result.BatchSize, "written", result.Written, "skipped_current", result.BatchSize - result.Written,
				"batch_ms", result.Duration.Milliseconds(), "yield_ms", float64(yield) / float64(time.Millisecond), "yield_status", "planned", "elapsed_ms", time.Since(state.StartedAt).Milliseconds(),
				"live_updated", current.LiveUpdated, "eta_ms", int64(averagePerSubject * float64(result.Total-result.Completed) / float64(time.Millisecond))}
			if result.Kind == "release" {
				attributes = append(attributes, "track_budget", releaseBudget.Tracks, "name_budget", releaseBudget.Names,
					"estimated_tracks", workload.Selected.Tracks, "estimated_input_names", workload.Selected.Names,
					"input_estimate_complete", workload.Selected.EstimateComplete, "subject_over_budget", workload.Selected.Oversized)
			}
			logger.InfoContext(ctx, "目录艺人投影回填批次已提交", attributes...)
			if workload.Selected.Oversized || result.Duration > time.Second || (result.Written == 1 && result.Duration > 200*time.Millisecond) {
				logger.WarnContext(ctx, "艺人批次超预算，需结合CATP取证；不截断完整subject", "event", "catalog_projection_batch_budget_exceeded",
					"capability", "artists", "phase", result.Phase, "batch_size", result.BatchSize,
					"subject_over_budget", workload.Selected.Oversized, "single_subject_over_target", result.Written == 1 && result.Duration > 200*time.Millisecond,
					"batch_ms", result.Duration.Milliseconds())
			}
			if result.Kind == "artist" {
				profileLimit = adaptLimit(profileLimit, 100, result.Duration)
			} else {
				releaseLimit = adaptLimit(releaseLimit, 100, result.Duration)
				if result.Written > 0 {
					releaseBudget = adaptArtistReleaseBudget(releaseBudget, result.Duration)
				}
			}
		}
		if err = wait(ctx, yield); err != nil {
			return err
		}
	}
}

func adaptArtistReleaseBudget(current artistReleaseBudget, duration time.Duration) artistReleaseBudget {
	return artistReleaseBudget{
		Tracks: adaptLimit(current.Tracks, maxArtistBatchTracks, duration),
		Names:  adaptLimit(current.Names, maxArtistBatchNames, duration),
	}
}

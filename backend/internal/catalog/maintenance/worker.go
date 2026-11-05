package maintenance

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

const maintenanceNamespace = 0x4341544d // CATM：只排斥同一 schema 的后台维护者。

// RunOptions 的收藏端口由 app 装配；维护包不依赖 personal 或其私有表。
type RunOptions struct {
	RefreshArtistResolutions func(context.Context, *sql.Tx, map[string]string) error
}

var ErrArtistResolutionRefreshRequired = errors.New("artist resolution refresh callback required")

// Run 在专用连接上选主，先搜索后艺人；调用方拥有 goroutine 与取消生命周期。
// 可恢复错误安全记录并退避，已提交进度保留。缺少发布回调属于装配错误，直接返回。
func Run(ctx context.Context, db *sql.DB, logger *slog.Logger, options ...RunOptions) error {
	var opts RunOptions
	if len(options) > 1 {
		return errors.New("catalog maintenance accepts one options value")
	}
	if len(options) == 1 {
		opts = options[0]
	}
	if logger == nil {
		logger = slog.Default()
	}
	for ctx.Err() == nil {
		conn, err := db.Conn(ctx)
		if err == nil {
			var key int32
			var acquired bool
			key, acquired, err = acquireLease(ctx, conn)
			if err == nil && acquired {
				err = runLocked(ctx, conn, logger, opts)
				releaseLease(conn, key)
				if err == nil || errors.Is(err, ErrArtistResolutionRefreshRequired) {
					return err
				}
			} else {
				if err != nil {
					// 选主响应失败时不能确定服务端是否已取得会话锁；丢弃物理连接，不能带锁回池。
					_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				}
				_ = conn.Close()
			}
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			logger.ErrorContext(ctx, "目录投影维护暂时失败", "event", "catalog_projection_backfill_failed", "module", "catalog", "error_code", "projection_build_failed")
		}
		if err = wait(ctx, time.Second); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// acquireLease 与扫描锁一样按 schema 隔离，并保存实际键值，解锁不重算 search_path。
func acquireLease(ctx context.Context, conn *sql.Conn) (int32, bool, error) {
	var key int32
	var acquired bool
	err := conn.QueryRowContext(ctx, `SELECT lock_key,pg_try_advisory_lock($1::integer,lock_key)
  FROM (SELECT hashtext(current_schema()) AS lock_key) AS maintenance_lock`, maintenanceNamespace).Scan(&key, &acquired)
	return key, acquired, err
}

// releaseLease 不将仍带会话锁的连接放回池；清理失败时丢弃物理连接。
func releaseLease(conn *sql.Conn, key int32) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, maintenanceNamespace, key).Scan(&unlocked); err != nil || !unlocked {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

func runLocked(ctx context.Context, conn *sql.Conn, logger *slog.Logger, options RunOptions) error {
	if err := runSearchLocked(ctx, conn, logger); err != nil {
		return err
	}
	return runArtistsLocked(ctx, conn, logger, options)
}

func runSearchLocked(ctx context.Context, conn *sql.Conn, logger *slog.Logger) (err error) {
	state, err := searchindex.LoadState(ctx, conn)
	if err != nil {
		return err
	}
	if state.Ready() {
		return nil
	}
	if !state.CurrentVersion() {
		return searchindex.ErrVersionMismatch
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			markFailed(ctx, conn, state)
		}
	}()
	if err = textnorm.LoadEmbedded(); err != nil {
		return err
	}
	logger.InfoContext(ctx, "开始目录搜索投影维护", "event", "catalog_projection_backfill_started", "module", "catalog",
		"build_id", state.BuildID, "target_generation", state.TargetGeneration, "textnorm_version", state.TextnormVersion, "search_projection_version", state.SearchProjectionVersion)
	if !state.ManifestSealed {
		if err = discover(ctx, conn, state); err != nil {
			return err
		}
	}
	started := state.StartedAt
	var averagePerSubject float64
	releaseLimit, trackLimit := 10, 50
	for {
		result, err := processBatch(ctx, conn, state, releaseLimit, trackLimit)
		if err != nil {
			return err
		}
		yield := batchYield(result.Duration)
		if result.BatchSize == 0 {
			published, err := publishOrRepair(ctx, conn, state)
			if err != nil {
				return err
			}
			if published {
				logger.InfoContext(ctx, "目录搜索投影已发布", "event", "catalog_projection_published", "module", "catalog",
					"build_id", state.BuildID, "target_generation", state.TargetGeneration, "elapsed_ms", time.Since(started).Milliseconds())
				return nil
			}
		} else {
			sample := float64(result.Duration+yield) / float64(result.BatchSize)
			if averagePerSubject == 0 {
				averagePerSubject = sample
			} else {
				averagePerSubject = 0.8*averagePerSubject + 0.2*sample
			}
			current, loadErr := searchindex.LoadState(ctx, conn)
			if loadErr != nil {
				return loadErr
			}
			logger.InfoContext(ctx, "目录搜索投影回填批次已提交", "event", "catalog_projection_backfill_batch", "module", "catalog",
				"build_id", state.BuildID, "target_generation", state.TargetGeneration, "textnorm_version", state.TextnormVersion,
				"search_projection_version", state.SearchProjectionVersion, "phase", result.Phase, "completed", result.Completed, "total", result.Total,
				"batch_size", result.BatchSize, "written", result.Written, "skipped_current", result.BatchSize-result.Written,
				"batch_ms", result.Duration.Milliseconds(), "yield_ms", float64(yield)/float64(time.Millisecond), "yield_status", "planned", "elapsed_ms", time.Since(started).Milliseconds(),
				"live_updated", current.LiveUpdated, "eta_ms", int64(averagePerSubject*float64(result.Total-result.Completed)/float64(time.Millisecond)))
			if result.Kind == "track" {
				trackLimit = adaptLimit(trackLimit, 500, result.Duration)
			} else {
				releaseLimit = adaptLimit(releaseLimit, 100, result.Duration)
			}
		}
		// 每批提交并释放 CATP 后正时长让出；日志中的 yield_ms 为计划值，取消时可提前结束。
		if err = wait(ctx, yield); err != nil {
			return err
		}
	}
}

func adaptLimit(current, maximum int, duration time.Duration) int {
	if duration > 200*time.Millisecond {
		return max(1, current/2)
	}
	if duration < 100*time.Millisecond {
		return min(maximum, current+max(1, current/4))
	}
	return current
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errBuildChanged = errors.New("catalog projection build changed")

func lockBuild(ctx context.Context, tx *sql.Tx, expected searchindex.State) error {
	if err := writegate.Lock(ctx, tx); err != nil {
		return err
	}
	state, err := searchindex.LoadState(ctx, tx)
	if err != nil {
		return err
	}
	if state.BuildID != expected.BuildID || state.TargetGeneration != expected.TargetGeneration || !state.CurrentVersion() {
		return errBuildChanged
	}
	return nil
}

func markFailed(ctx context.Context, conn *sql.Conn, state searchindex.State) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err = lockBuild(ctx, tx, state); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='failed',updated_at=NOW()
  WHERE capability='search' AND build_id=$1::uuid AND status<>'ready'`, state.BuildID); err == nil {
		_ = tx.Commit()
	}
}

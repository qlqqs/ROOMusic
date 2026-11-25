package program

import (
	"context"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// LookupTierReady 只在扫描后的完整投影已发布、程序高优先项排空时向第二档让出额度。
func (w *Worker) LookupTierReady(ctx context.Context, releaseID string, now time.Time) (bool, error) {
	if w.opts.NamesPatrolled == nil || w.policy.Pause("musicbrainz") != nil {
		return false, nil
	}
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		switch failureCode(err) {
		case "curation_disabled", "curation_paused", "scan_running", "source_disabled", "not_found":
			return false, nil
		}
		return false, err
	}
	if !w.projectionMu.TryLock() {
		return false, nil
	}
	defer w.projectionMu.Unlock()
	if w.marker == nil {
		return false, nil
	}
	current, err := w.projectionMarker(ctx, now)
	if err != nil || current != *w.marker {
		return false, err
	}
	var stable bool
	err = w.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT EXISTS(SELECT FROM curation_stable_releases stable JOIN visible USING(id) WHERE stable.id=$1)`, releaseID).Scan(&stable)
	if err != nil || !stable {
		return false, err
	}
	high, err := w.hasHigherIdentity(ctx, now)
	if err != nil || high {
		return false, err
	}
	return w.opts.NamesPatrolled(ctx, releaseID)
}

func (w *Worker) LookupTierContext(ctx context.Context) context.Context {
	return w.policy.Context(ctx, sources.QueueModelLookup, nil)
}

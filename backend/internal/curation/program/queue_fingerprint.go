package program

import (
	"context"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type computedPrint struct {
	release, identity string
	generation        int
	value             TrackFingerprint
	status            FingerprintStatus
	retry             time.Duration
}

func (w *Worker) computePrint(ctx context.Context, item queueItem) error {
	step, err := w.fingerprintStep(ctx)
	if err != nil {
		return err
	}
	if step == nil {
		return w.finishFingerprint(ctx, item)
	}
	value, status, retry, err := step.read(ctx, item.TargetID)
	if err != nil {
		return err
	}
	if err = w.ready(ctx); err != nil {
		return err
	}
	w.printMu.Lock()
	w.prints[item.TargetID] = computedPrint{item.ReleaseID, item.InputDigest, item.Generation, value, status, retry}
	w.printMu.Unlock()
	return w.finishFingerprint(ctx, item)
}

func (w *Worker) dropPrints(release string) {
	w.printMu.Lock()
	defer w.printMu.Unlock()
	for id, p := range w.prints {
		if p.release == release {
			delete(w.prints, id)
		}
	}
}

// 只有识别实际走到声纹分支才登记抽样。重启后仍由声纹队列读取已有持久化缓存。
func (w *Worker) queuedSamples(ctx context.Context, item queueItem, tracks []catalog.CurationTrack) ([]sampledFingerprint, error) {
	var missing []string
	var result []sampledFingerprint
	var transient bool
	var retry time.Duration
	w.printMu.Lock()
	for _, t := range tracks {
		p, ok := w.prints[t.ID]
		if !ok || p.release != item.ReleaseID || p.generation != item.Generation || p.identity != item.InputDigest {
			missing = append(missing, t.ID)
			continue
		}
		switch p.status {
		case FingerprintOK:
			result = append(result, sampledFingerprint{t.ID, p.value})
		case FingerprintTransient:
			transient = true
			retry = max(retry, p.retry)
		}
	}
	w.printMu.Unlock()
	if len(missing) > 0 {
		if err := w.queueFingerprints(ctx, item, missing); err != nil {
			return nil, err
		}
		return nil, errWaitingForPrints
	}
	if 2*len(result) < len(tracks) {
		if transient {
			return nil, &fingerprintRetry{retry}
		}
		return nil, nil
	}
	return result, nil
}

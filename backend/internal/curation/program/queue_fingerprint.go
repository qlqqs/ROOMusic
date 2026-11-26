package program

import (
	"context"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type computedPrint struct {
	release, identity, files string
	generation               int
	hit                      fingerprintHit
	status                   FingerprintStatus
	failure                  error
}

func (w *Worker) computePrint(ctx context.Context, item queueItem) error {
	step, err := w.fingerprintStep(ctx)
	if err != nil {
		return err
	}
	if step == nil {
		return w.finishFingerprint(ctx, item)
	}
	value, status, err := step.Prints.Fingerprint(ctx, item.TargetID)
	if err != nil {
		return err
	}
	result := computedPrint{release: item.ReleaseID, identity: item.InputDigest, files: item.Files, generation: item.Generation, status: status}
	switch status {
	case FingerprintOK:
		result.hit, err = step.lookup(ctx, item.TargetID, value)
		switch {
		case err == nil:
		case curationQueryFailure(err), errors.Is(err, sources.ErrInvalidKey), failureCode(err) == "source_disabled":
			result.failure = err
		default:
			return err
		}
	case FingerprintTransient:
		result.failure = &sampleFailure{"local_fingerprint", errLocalPrint}
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	w.printMu.Lock()
	w.prints[item.TargetID] = result
	w.printMu.Unlock()
	if result.failure != nil {
		// 同一发行只向识别队列交回一次错误，下一轮按需重取样本。
		_, err := w.db.ExecContext(ctx, `UPDATE curation_work_states SET status='done',needs_work=false,updated_at=now()
            WHERE subject='fingerprint' AND queue_release_id=$1 AND generation=$2 AND input_digest=$3 AND needs_work`,
			item.ReleaseID, item.Generation, item.InputDigest)
		return err
	}
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

func curationQueryFailure(err error) bool {
	var pause *sources.QueuePause
	return errors.As(err, &pause) || errors.Is(err, sources.ErrInvalidQuery) || errors.Is(err, sources.ErrInvalidResponse)
}

func (w *Worker) queuedHits(ctx context.Context, item queueItem, tracks []catalog.CurationTrack) ([]fingerprintHit, error) {
	var missing []string
	var hits []fingerprintHit
	var computed int
	w.printMu.Lock()
	for _, t := range tracks {
		p, ok := w.prints[t.ID]
		if !ok || p.release != item.ReleaseID || p.identity != item.InputDigest || p.files != item.Files || p.generation != item.Generation {
			missing = append(missing, t.ID)
			continue
		}
		if p.failure != nil {
			w.printMu.Unlock()
			return nil, p.failure
		}
		if p.status == FingerprintOK {
			computed++
			if len(p.hit.recordings) > 0 {
				hits = append(hits, p.hit)
			}
		}
	}
	w.printMu.Unlock()
	if len(missing) > 0 {
		if err := w.queueFingerprints(ctx, item, missing); err != nil {
			return nil, err
		}
		return nil, errWaitingForPrints
	}
	if 2*computed < len(tracks) {
		return nil, nil
	}
	return hits, nil
}

func canPrefetch(e catalog.CurationEvidence) bool {
	if e.ExternalID != "" || e.ReleaseGroupMatchID != "" || len(e.Tracks) == 0 || e.CandidateKind == "loose_unknown" && len(e.Tracks) != 1 {
		return false
	}
	if len(tags(e.Tracks, "mb_release")) > 0 || len(tags(e.Tracks, "mb_recording")) > 0 || len(tags(e.Tracks, "mb_disc")) > 0 ||
		len(DiscTOCs(e)) > 0 || len(e.SearchBarcodes()) > 0 || e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber) != "" || len(sampleISRCTracks(e)) > 0 {
		return false
	}
	mode := "album"
	if e.CandidateKind == "loose_unknown" {
		mode = "single"
	}
	names := localNames(curation.LocalSearchEvidence(e), mode)
	hasArtist := names.Artist != "" || names.ArtistID != "" || curation.CompilationEvidence(e)
	return (names.Title != "") == hasArtist
}

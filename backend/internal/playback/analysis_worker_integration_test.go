package playback

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
)

type workerTestAnalyzer struct {
	calls  atomic.Int32
	cancel context.CancelFunc
}

func (*workerTestAnalyzer) Version() int16 { return analysis.Version }
func (a *workerTestAnalyzer) Analyze(ctx context.Context, _ *os.File, _, _ *int32) (analysis.Result, error) {
	call := a.calls.Add(1)
	if call == 1 {
		return analysis.Result{}, &analysis.Failure{Category: "ffmpeg_exit", Err: errors.New("synthetic decode failure")}
	}
	if a.cancel != nil {
		a.cancel()
	}
	return analysis.Result{}, ctx.Err()
}

func TestAnalysisWorkerWaitsForScanWithoutAnalyzing(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.insertPlayableFLAC(t, "first.flac")
	analyzer := &workerTestAnalyzer{}
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewAnalysisWorker(fixture.database, []string{fixture.allowedRoot}, analyzer, func(context.Context) (bool, error) {
		cancel()
		return true, nil
	}, nil)
	worker.Run(ctx)
	if analyzer.calls.Load() != 0 {
		t.Fatalf("Analyze called %d times while scan active", analyzer.calls.Load())
	}
	if worker.State() != "waiting_for_scan" {
		t.Fatalf("state = %q, want waiting_for_scan", worker.State())
	}
}

func TestAnalysisWorkerPersistsFailureAndContinuesToNextCandidate(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.insertPlayableFLAC(t, "first.flac")
	fixture.insertPlayableFLAC(t, "second.flac")
	candidates, err := catalog.NewStore(fixture.database).ListAnalysisCandidates(context.Background(), analysis.Version, 10)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates = %d, err=%v", len(candidates), err)
	}
	firstID, secondID := candidates[0].TrackID, candidates[1].TrackID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	analyzer := &workerTestAnalyzer{cancel: cancel}
	worker := NewAnalysisWorker(fixture.database, []string{fixture.allowedRoot}, analyzer, func(context.Context) (bool, error) { return false, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.Run(ctx)
	if analyzer.calls.Load() != 2 {
		t.Fatalf("Analyze called %d times, want failure then next candidate", analyzer.calls.Load())
	}
	var status, category string
	if err := fixture.database.QueryRow(`SELECT status,failure_category FROM track_analyses WHERE track_id=$1::uuid`, firstID).Scan(&status, &category); err != nil {
		t.Fatalf("read failed analysis: %v", err)
	}
	if status != "failed" || category != "ffmpeg_exit" {
		t.Fatalf("first analysis = %s/%s", status, category)
	}
	var secondRows int
	if err := fixture.database.QueryRow(`SELECT count(*) FROM track_analyses WHERE track_id=$1::uuid`, secondID).Scan(&secondRows); err != nil {
		t.Fatal(err)
	}
	if secondRows != 0 {
		t.Fatalf("second analysis unexpectedly persisted after cancellation: %d rows", secondRows)
	}
}

func TestAnalysisWorkerRunReturnsWhenContextIsCanceled(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.insertPlayableFLAC(t, "first.flac")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := NewAnalysisWorker(fixture.database, []string{fixture.allowedRoot}, &workerTestAnalyzer{}, func(context.Context) (bool, error) { return false, nil }, nil)
	worker.Run(ctx)
	if worker.State() != "idle" {
		t.Fatalf("state = %q after canceled Run", worker.State())
	}
}

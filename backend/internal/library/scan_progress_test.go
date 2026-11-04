package library

import (
	"context"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

func TestScanProgressWeightedBoundaries(t *testing.T) {
	cases := []struct{ base, weight, total, n, d, want int64 }{
		{0, 100, 400, 1, 1, 2850}, {100, 300, 400, 0, 1, 2850}, {100, 300, 400, 3, 10, 4965},
		{100, 300, 400, 1, 1, 9900}, {0, 1, 2, 1, 1, 5200}, {0, 0, 400, 1, 1, 500},
		{0, maxFeedbackCount, maxFeedbackCount, 1, 2, 5200},
		{maxFeedbackCount - 1, 1, maxFeedbackCount, 1, 1, 9900},
	}
	for _, c := range cases {
		if got := weightedProgress(c.base, c.weight, c.total, c.n, c.d); got != c.want {
			t.Errorf("%+v got %d", c, got)
		}
	}
}
func TestScanProgressCandidateDefinition(t *testing.T) {
	for _, name := range []string{"A.FLAC", "a.CUE", "a.ape", "a.wav", "a.mka", "a.m4a"} {
		if !isScanCandidate(name) {
			t.Errorf("candidate %s", name)
		}
	}
	for _, name := range []string{"cover.jpg", "x.log", "x.txt", "no_extension"} {
		if isScanCandidate(name) {
			t.Errorf("attachment %s", name)
		}
	}
}
func TestScanProgressDriftCancellationAndOverflow(t *testing.T) {
	f := &scanFeedback{counts: []rootCount{{files: 1}}, progress: ScanProgress{Total: 1}, denominator: 1, weight: 1, changes: ingest.NewScanFeedback(""), lastAttempt: time.Now()}
	f.processed(context.Background())
	f.processed(context.Background())
	if f.progress.FilesProcessed != 2 || !f.progress.FilesChanged || f.progress.PercentBP != 6140 {
		t.Fatalf("progress %+v", f.progress)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.processed(ctx)
	if f.progress.FilesProcessed != 2 {
		t.Fatal("取消的尝试不可计入")
	}
	count := maxFeedbackCount
	f.add(&count)
	if !f.invalid || !f.changes.Broken() || count != maxFeedbackCount {
		t.Fatal("溢出必须降级而非截断伪造")
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	smoke "github.com/qlqq/roomusic/backend/cmd/roomusic-smoke"
)

func writeTestSnapshot(t *testing.T, path string, snapshot smoke.Snapshot) {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunCompareWritesReportAndReturnsDiffStatus(t *testing.T) {
	dir := t.TempDir()
	expectedPath := filepath.Join(dir, "expected.json")
	actualPath := filepath.Join(dir, "actual.json")
	reportPath := filepath.Join(dir, "reports", "comparison.json")
	base := smoke.Snapshot{Version: smoke.SnapshotVersion, Implementation: "expected", CorpusDigest: "corpus"}
	actual := base
	actual.Implementation = "actual"
	actual.Releases = []smoke.Release{{Key: "release", MediumKeys: []string{"medium"}}}
	actual.Media = []smoke.Medium{{Key: "medium", ReleaseKey: "release", Position: 1, TrackKeys: []string{"track"}}}
	actual.Tracks = []smoke.Track{{
		Key:             "track",
		MediumKey:       "medium",
		Position:        1,
		SourceKey:       "source",
		ParentSourceKey: "source",
		SourceKind:      "physical",
		Title:           "changed",
	}}
	actual.Files = []smoke.File{{Key: "file", ReleaseKey: "release", SourceKey: "source"}}
	writeTestSnapshot(t, expectedPath, base)
	writeTestSnapshot(t, actualPath, actual)

	if code := run([]string{"compare", "--expected", expectedPath, "--actual", actualPath, "--output", reportPath}); code != 0 {
		t.Fatalf("compare without fail flag returned %d", code)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"compare", "--expected", expectedPath, "--actual", actualPath, "--output", reportPath, "--fail-on-diff"}); code != 1 {
		t.Fatalf("compare with fail flag returned %d", code)
	}
	if code := run([]string{"compare", "--expected", expectedPath, "--actual", actualPath, "--output", reportPath, "--fail-on-category", "capability_gap"}); code != 0 {
		t.Fatalf("compare with absent fail category returned %d", code)
	}
	if code := run([]string{"compare", "--expected", expectedPath, "--actual", actualPath, "--output", reportPath, "--fail-on-category", "current_regression"}); code != 1 {
		t.Fatalf("compare with matching fail category returned %d", code)
	}
}

func TestRunCompareRejectsIncompatibleSnapshots(t *testing.T) {
	dir := t.TempDir()
	expectedPath := filepath.Join(dir, "expected.json")
	actualPath := filepath.Join(dir, "actual.json")
	reportPath := filepath.Join(dir, "comparison.json")
	base := smoke.Snapshot{Version: smoke.SnapshotVersion, Implementation: "one", CorpusDigest: "corpus"}
	other := base
	other.CorpusDigest = "different"
	writeTestSnapshot(t, expectedPath, base)
	writeTestSnapshot(t, actualPath, other)
	if code := run([]string{"compare", "--expected", expectedPath, "--actual", actualPath, "--output", reportPath}); code != 2 {
		t.Fatalf("incompatible compare returned %d", code)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type failingScanExecutor struct {
	err error
}

func (executor failingScanExecutor) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, executor.err
}

func (executor failingScanExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, executor.err
}

func (executor failingScanExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, executor.err
}

func (executor failingScanExecutor) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

type cleanupFailureScanExecutor struct {
	cleanupCalls     int
	cleanupFailureAt int
	cleanupError     error
	diagnosticError  error
	diagnosticKinds  []string
}

func (executor *cleanupFailureScanExecutor) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

func (executor *cleanupFailureScanExecutor) ExecContext(_ context.Context, query string, arguments ...any) (sql.Result, error) {
	if strings.HasPrefix(query, "DELETE FROM scan_observations") {
		executor.cleanupCalls++
		if executor.cleanupCalls == executor.cleanupFailureAt {
			return nil, executor.cleanupError
		}
		return nil, nil
	}
	if strings.HasPrefix(query, "INSERT INTO scan_diagnostics") {
		if len(arguments) >= 3 {
			if kind, ok := arguments[2].(string); ok {
				executor.diagnosticKinds = append(executor.diagnosticKinds, kind)
			}
		}
		return nil, executor.diagnosticError
	}
	return nil, errors.New("unexpected statement")
}

func (executor *cleanupFailureScanExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (executor *cleanupFailureScanExecutor) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func TestBuildCueVirtualObservationClonesMutableProvenancePerTrack(t *testing.T) {
	parent := audioObservation{
		Title:       "image",
		Artist:      "未知艺术家",
		Album:       "Album",
		TrackNumber: 1,
		DiscNumber:  1,
		SourceKind:  "flac_vorbis_comment",
		InferredFields: map[string]bool{
			"title":        true,
			"artist":       true,
			"album":        true,
			"track_number": true,
			"disc_number":  true,
		},
	}
	document := cueDocument{Title: "CUE Album"}
	first := buildCueVirtualObservation(parent, document, cueTrack{
		Number:           1,
		Title:            "First",
		Artist:           "First Artist",
		PerformerPresent: true,
		ReferencedFile:   "image.flac",
		IndexPresent:     true,
	}, "Album/album.cue")
	second := buildCueVirtualObservation(parent, document, cueTrack{
		Number:         2,
		ReferencedFile: "image.flac",
		IndexFrames:    750,
		IndexPresent:   true,
	}, "Album/album.cue")

	if !parent.InferredFields["title"] || !parent.InferredFields["artist"] || !parent.InferredFields["album"] || !parent.InferredFields["track_number"] {
		t.Fatalf("first CUE track mutated its parent facts: %+v", parent.InferredFields)
	}
	if first.InferredFields["title"] || first.InferredFields["artist"] || first.InferredFields["album"] || first.InferredFields["track_number"] {
		t.Fatalf("explicit first-track CUE facts remained inferred: %+v", first)
	}
	if !second.InferredFields["title"] || !second.InferredFields["artist"] || second.InferredFields["album"] || second.InferredFields["track_number"] {
		t.Fatalf("first-track facts contaminated the second CUE track: %+v", second)
	}
	if first.FieldSources["title"] != "cue_track" || second.FieldSources["title"] != "filename_fallback" || second.FieldSources["track_number"] != "cue_track" {
		t.Fatalf("unexpected per-track provenance: first=%+v second=%+v", first.FieldSources, second.FieldSources)
	}
	if first.TrackNumber != 1 || second.TrackNumber != 2 || first.CueParentRelativePath != "Album/image.flac" || second.CueParentRelativePath != "Album/image.flac" {
		t.Fatalf("CUE identity facts were not preserved: first=%+v second=%+v", first, second)
	}

	first.InferredFields["sentinel"] = true
	first.FieldSources["sentinel"] = "first"
	if parent.InferredFields["sentinel"] || second.InferredFields["sentinel"] || second.FieldSources["sentinel"] != "" {
		t.Fatal("CUE observations still share mutable provenance maps")
	}
}

func TestBuildCueVirtualObservationDistinguishesSheetAndTrackPerformer(t *testing.T) {
	parent := audioObservation{Artist: "未知艺术家", InferredFields: map[string]bool{"artist": true}}
	document := cueDocument{Artist: "Sheet Artist"}
	sheetPerformer := buildCueVirtualObservation(parent, document, cueTrack{
		Number:         1,
		Artist:         "Sheet Artist",
		SheetArtist:    "Sheet Artist",
		ReferencedFile: "image.flac",
		IndexPresent:   true,
	}, "Album/album.cue")
	trackPerformer := buildCueVirtualObservation(parent, document, cueTrack{
		Number:           2,
		Artist:           "Track Artist",
		PerformerPresent: true,
		SheetArtist:      "Sheet Artist",
		ReferencedFile:   "image.flac",
		IndexPresent:     true,
	}, "Album/album.cue")
	if sheetPerformer.Artist != "Sheet Artist" || sheetPerformer.FieldSources["artist"] != "cue_sheet" {
		t.Fatalf("sheet performer provenance = %+v", sheetPerformer)
	}
	if trackPerformer.Artist != "Track Artist" || trackPerformer.FieldSources["artist"] != "cue_track" {
		t.Fatalf("track performer provenance = %+v", trackPerformer)
	}
}

func TestCueReferenceCompletionAndStagingPolicy(t *testing.T) {
	testCases := []struct {
		name       string
		status     string
		index      bool
		incomplete bool
		staged     bool
	}{
		{name: "present with index", status: "present", index: true, staged: true},
		{name: "present without index", status: "present", index: false},
		{name: "missing parent", status: "missing", index: true},
		{name: "unsafe parent", status: "unsafe", index: true, incomplete: true},
		{name: "unchecked parent", status: "unchecked", index: true, incomplete: true},
		{name: "unknown status", status: "", index: true, incomplete: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			track := cueTrack{ReferenceStatus: testCase.status, IndexPresent: testCase.index}
			if got := cueReferenceMakesScanIncomplete(testCase.status); got != testCase.incomplete {
				t.Fatalf("cueReferenceMakesScanIncomplete(%q) = %t, want %t", testCase.status, got, testCase.incomplete)
			}
			if got := cueTrackCanBeStaged(track); got != testCase.staged {
				t.Fatalf("cueTrackCanBeStaged(%+v) = %t, want %t", track, got, testCase.staged)
			}
		})
	}
}

func TestCanonicalCreditObservationsAreStableAndDeduplicated(t *testing.T) {
	got := canonicalCreditObservations([]creditObservation{
		{Role: " Producer ", Name: " Producer One "},
		{Role: "composer", Name: "Composer Two"},
		{Role: "COMPOSER", Name: "Composer One"},
		{Role: "composer", Name: "Composer One"},
		{Role: "", Name: "ignored"},
	})
	want := []creditObservation{
		{Role: "composer", Name: "Composer One"},
		{Role: "composer", Name: "Composer Two"},
		{Role: "producer", Name: "Producer One"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical credits = %+v，期望 %+v", got, want)
	}
}

func TestScanRootFailsWhenInitialStagingCleanupFails(t *testing.T) {
	expected := errors.New("cleanup failed")
	application := &roomusicApplication{}
	err := application.scanRoot(context.Background(), failingScanExecutor{err: expected}, "scan-id", registeredRoot{ID: "root-id", Path: t.TempDir()})
	if !errors.Is(err, expected) {
		t.Fatalf("initial staging cleanup error was discarded: %v", err)
	}
}

func TestScanRootRecordsInitialStagingCleanupFailure(t *testing.T) {
	cleanupError := errors.New("initial cleanup failed")
	executor := &cleanupFailureScanExecutor{cleanupFailureAt: 1, cleanupError: cleanupError}

	err := (&roomusicApplication{}).scanRoot(context.Background(), executor, "scan-id", registeredRoot{ID: "root-id", Path: t.TempDir()})
	if !errors.Is(err, cleanupError) {
		t.Fatalf("initial staging cleanup error was discarded: %v", err)
	}
	if executor.cleanupCalls != 1 {
		t.Fatalf("staging cleanup calls = %d, want 1", executor.cleanupCalls)
	}
	if strings.Join(executor.diagnosticKinds, ",") != "staging_write_failure" {
		t.Fatalf("diagnostic kinds = %v", executor.diagnosticKinds)
	}
}

func TestScanRootPreservesEarlyFailureWhenExitStagingCleanupFails(t *testing.T) {
	cleanupError := errors.New("exit cleanup failed")
	executor := &cleanupFailureScanExecutor{cleanupFailureAt: 2, cleanupError: cleanupError}
	missingRoot := filepath.Join(t.TempDir(), "missing")

	err := (&roomusicApplication{}).scanRoot(context.Background(), executor, "scan-id", registeredRoot{ID: "root-id", Path: missingRoot})
	if !errors.Is(err, cleanupError) || !strings.Contains(err.Error(), "root unavailable") {
		t.Fatalf("scan root did not preserve both failures: %v", err)
	}
	if executor.cleanupCalls != 2 {
		t.Fatalf("staging cleanup calls = %d, want 2", executor.cleanupCalls)
	}
	if strings.Join(executor.diagnosticKinds, ",") != "root_unavailable,staging_write_failure" {
		t.Fatalf("diagnostic kinds = %v", executor.diagnosticKinds)
	}
}

func TestScanRootReturnsDiagnosticPersistenceFailure(t *testing.T) {
	diagnosticError := errors.New("diagnostic insert failed")
	executor := &cleanupFailureScanExecutor{diagnosticError: diagnosticError}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.cue"), []byte("TRACK broken AUDIO\n"), 0o644); err != nil {
		t.Fatalf("write malformed CUE: %v", err)
	}

	err := (&roomusicApplication{}).scanRoot(context.Background(), executor, "scan-id", registeredRoot{ID: "root-id", Path: root})
	if !errors.Is(err, diagnosticError) {
		t.Fatalf("diagnostic persistence error was discarded: %v", err)
	}
	if strings.Join(executor.diagnosticKinds, ",") != "unsupported_cue" {
		t.Fatalf("diagnostic kinds = %v", executor.diagnosticKinds)
	}
}

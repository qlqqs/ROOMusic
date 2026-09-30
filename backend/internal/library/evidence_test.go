package library

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

func TestObservationFieldSourcesDistinguishFallbacks(t *testing.T) {
	observation := parser.AudioObservation{InferredFields: map[string]bool{"title": true, "artist": true, "album": true}}
	sources := observationFieldSources(observation)
	if sources["title"] != "filename_fallback" || sources["artist"] != "display_fallback" || sources["album"] != "folder_fallback" {
		t.Fatalf("unexpected fallback sources: %#v", sources)
	}
}

func TestFolderArtworkDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, minimalPNGArtwork(), 0o644); err != nil {
		t.Fatalf("write outside artwork: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "cover.png")); err != nil {
		t.Fatalf("create artwork symlink: %v", err)
	}
	if data, _, err := discoverFolderArtwork(root); err == nil || len(data) != 0 {
		t.Fatal("folder artwork symlink was not rejected")
	}
}

func TestFolderArtworkRejectsInvalidNamedCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cover.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write invalid artwork: %v", err)
	}
	if data, _, err := discoverFolderArtwork(root); err == nil || len(data) != 0 {
		t.Fatal("invalid named artwork was treated as absent")
	}
}

func TestFolderArtworkReadFailureIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cover.png"), minimalPNGArtwork(), 0o644); err != nil {
		t.Fatalf("write folder artwork: %v", err)
	}
	_, _, err := discoverFolderArtworkWithReader(root, func(string) ([]byte, error) {
		return nil, fmt.Errorf("read denied")
	})
	if err == nil {
		t.Fatal("folder artwork read failure was treated as missing artwork")
	}
}

func TestArtworkPersistenceFailureKeepsDiagnosticCategory(t *testing.T) {
	kind, message := candidatePersistenceDiagnostic(errArtworkBinding)
	if kind != "artwork_failure" || message != "封面处理失败" {
		t.Fatalf("artwork diagnostic = %q/%q", kind, message)
	}
}

func TestDiscoverCandidateRipLogEvidenceRequiresExplicitSignatureAndSkipsSymlink(t *testing.T) {
	rootPath := t.TempDir()
	albumPath := filepath.Join(rootPath, "Album")
	if err := os.Mkdir(albumPath, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	for name, content := range map[string]string{
		"eac.LOG":   "Exact Audio Copy V1.6\nEAC extraction logfile",
		"notes.log": "CD FLAC 44.1 kHz without a ripping-tool signature",
		"late.log":  strings.Repeat("x", maxRipLogHeaderBytes) + "Exact Audio Copy V1.6",
		"xld.log":   "X Lossless Decoder version 2026\nAll tracks ripped",
	} {
		if err := os.WriteFile(filepath.Join(albumPath, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("Exact Audio Copy"), 0o644); err != nil {
		t.Fatalf("write outside log: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(albumPath, "linked.log")); err != nil {
		t.Fatalf("create log symlink: %v", err)
	}

	candidate := catalog.OrganizedCandidate{
		Anchor:  catalog.CandidateAnchor{Kind: catalog.CandidateOrdinary, Scope: "Album"},
		Mediums: map[int][]catalog.OrganizedTrack{1: {{Observation: catalog.SourceObservation{RelativePath: "Album/01.flac", Directory: "Album"}}}},
	}
	references, diagnostics := discoverCandidateRipLogEvidence(registeredRoot{ID: "root", Path: rootPath}, candidate)
	if !reflect.DeepEqual(references, []string{"Album/eac.LOG", "Album/xld.log"}) || len(diagnostics) != 0 {
		t.Fatalf("unexpected rip-log evidence: refs=%v diagnostics=%v", references, diagnostics)
	}
}

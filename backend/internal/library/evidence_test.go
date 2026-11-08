package library

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

func TestObservationFieldSourcesDistinguishFallbacks(t *testing.T) {
	observation := parser.AudioObservation{InferredFields: map[string]bool{"title": true, "artist": true, "album": true}}
	sources := observationFieldSources(observation)
	if sources["title"] != "filename_fallback" || sources["artist"] != "display_fallback" || sources["album"] != "folder_fallback" {
		t.Fatalf("unexpected fallback sources: %#v", sources)
	}
}

func minimalJPEGArtwork(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatalf("生成 JPEG 夹具：%v", err)
	}
	return buffer.Bytes()
}

func writeArtworkFile(t *testing.T, directory, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create artwork directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), data, 0o644); err != nil {
		t.Fatalf("write artwork %s: %v", name, err)
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
	if data, _, err := discoverFolderArtwork(root, []string{""}); err == nil || len(data) != 0 {
		t.Fatal("folder artwork symlink was not rejected")
	}
}

func TestFolderArtworkRejectsInvalidNamedCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cover.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write invalid artwork: %v", err)
	}
	if data, _, err := discoverFolderArtwork(root, []string{""}); err == nil || len(data) != 0 {
		t.Fatal("invalid named artwork was treated as absent")
	}
}

func TestFolderArtworkReadFailureIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cover.png"), minimalPNGArtwork(), 0o644); err != nil {
		t.Fatalf("write folder artwork: %v", err)
	}
	_, _, err := discoverFolderArtworkWithReader(root, []string{""}, func(string) ([]byte, error) {
		return nil, fmt.Errorf("read denied")
	})
	if err == nil {
		t.Fatal("folder artwork read failure was treated as missing artwork")
	}
}

func TestFolderArtworkMatchesNamesCaseInsensitively(t *testing.T) {
	for _, name := range []string{"Cover.JPG", "FOLDER.Png", "Front.jpeg"} {
		root := t.TempDir()
		writeArtworkFile(t, root, name, minimalJPEGArtwork(t))
		data, mimeType, err := discoverFolderArtwork(root, []string{""})
		if err != nil || len(data) == 0 || mimeType != "image/jpeg" {
			t.Fatalf("%s was not found: mime=%q err=%v", name, mimeType, err)
		}
	}
}

func TestFolderArtworkCaseOnlyVariantsPreferLowercaseThenByteOrder(t *testing.T) {
	root := t.TempDir()
	writeArtworkFile(t, root, "cover.png", minimalPNGArtwork())
	writeArtworkFile(t, root, "Cover.PNG", minimalJPEGArtwork(t))
	if _, mimeType, err := discoverFolderArtwork(root, []string{""}); err != nil || mimeType != "image/png" {
		t.Fatalf("lowercase variant did not win: mime=%q err=%v", mimeType, err)
	}

	variants := t.TempDir()
	writeArtworkFile(t, variants, "Cover.jpg", minimalPNGArtwork())
	writeArtworkFile(t, variants, "COVER.jpg", minimalJPEGArtwork(t))
	if _, mimeType, err := discoverFolderArtwork(variants, []string{""}); err != nil || mimeType != "image/jpeg" {
		t.Fatalf("byte order did not pick COVER.jpg: mime=%q err=%v", mimeType, err)
	}
}

func TestFolderArtworkNamePriorityIgnoresCase(t *testing.T) {
	root := t.TempDir()
	writeArtworkFile(t, root, "FRONT.jpg", minimalJPEGArtwork(t))
	writeArtworkFile(t, root, "Folder.jpg", minimalJPEGArtwork(t))
	writeArtworkFile(t, root, "COVER.PNG", minimalPNGArtwork())
	if _, mimeType, err := discoverFolderArtwork(root, []string{""}); err != nil || mimeType != "image/png" {
		t.Fatalf("cover did not outrank folder and front: mime=%q err=%v", mimeType, err)
	}
	if err := os.Remove(filepath.Join(root, "COVER.PNG")); err != nil {
		t.Fatalf("remove cover: %v", err)
	}
	// 同名前缀内按扩展名顺序：folder.jpg 排在 folder.png 与 front.jpg 之前。
	writeArtworkFile(t, root, "Folder.png", minimalPNGArtwork())
	if _, mimeType, err := discoverFolderArtwork(root, []string{""}); err != nil || mimeType != "image/jpeg" {
		t.Fatalf("folder.jpg did not outrank folder.png and front: mime=%q err=%v", mimeType, err)
	}
}

func TestFolderArtworkSearchesDirectoriesInOrder(t *testing.T) {
	root := t.TempDir()
	writeArtworkFile(t, filepath.Join(root, "Album"), "cover.png", minimalPNGArtwork())
	writeArtworkFile(t, filepath.Join(root, "Album", "CD1"), "cover.jpg", minimalJPEGArtwork(t))
	if _, mimeType, err := discoverFolderArtwork(root, []string{"Album", "Album/CD1"}); err != nil || mimeType != "image/png" {
		t.Fatalf("earlier directory did not win: mime=%q err=%v", mimeType, err)
	}
	if _, mimeType, err := discoverFolderArtwork(root, []string{"Album/CD1", "Album"}); err != nil || mimeType != "image/jpeg" {
		t.Fatalf("directory order was ignored: mime=%q err=%v", mimeType, err)
	}
}

func TestFolderArtworkSkipsInvalidFileInEarlierDirectory(t *testing.T) {
	root := t.TempDir()
	writeArtworkFile(t, filepath.Join(root, "A"), "cover.png", []byte("not an image"))
	writeArtworkFile(t, filepath.Join(root, "B"), "cover.png", minimalPNGArtwork())
	data, mimeType, err := discoverFolderArtwork(root, []string{"A", "B"})
	if err != nil || len(data) == 0 || mimeType != "image/png" {
		t.Fatalf("valid artwork in a later directory was not used: mime=%q err=%v", mimeType, err)
	}
	if data, _, err := discoverFolderArtwork(root, []string{"A"}); err == nil || len(data) != 0 {
		t.Fatal("invalid artwork alone was treated as absent")
	}
}

func TestFolderArtworkMissingDirectoryIsSkipped(t *testing.T) {
	root := t.TempDir()
	if data, mimeType, err := discoverFolderArtwork(root, []string{"missing"}); err != nil || len(data) != 0 || mimeType != "" {
		t.Fatalf("missing directory was not treated as absent: err=%v", err)
	}
	writeArtworkFile(t, root, "cover.png", minimalPNGArtwork())
	if _, mimeType, err := discoverFolderArtwork(root, []string{"missing", ""}); err != nil || mimeType != "image/png" {
		t.Fatalf("search stopped at a missing directory: mime=%q err=%v", mimeType, err)
	}
}

func TestFolderArtworkDirectoryReadFailureIsReported(t *testing.T) {
	root := t.TempDir()
	// 把“目录”换成普通文件，ReadDir 返回的是 ENOTDIR 而不是“不存在”。
	writeArtworkFile(t, root, "NotADirectory", []byte("x"))
	if data, _, err := discoverFolderArtwork(root, []string{"NotADirectory"}); err == nil || len(data) != 0 {
		t.Fatal("directory read failure was treated as missing artwork")
	}
	writeArtworkFile(t, filepath.Join(root, "Album"), "cover.png", minimalPNGArtwork())
	if _, mimeType, err := discoverFolderArtwork(root, []string{"NotADirectory", "Album"}); err != nil || mimeType != "image/png" {
		t.Fatalf("directory read failure hid a usable cover: mime=%q err=%v", mimeType, err)
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

	candidate := ingest.OrganizedCandidate{
		Anchor:  ingest.CandidateAnchor{Kind: ingest.CandidateOrdinary, Scope: "Album"},
		Mediums: map[int][]ingest.OrganizedTrack{1: {{Observation: ingest.SourceObservation{RelativePath: "Album/01.flac", Directory: "Album"}}}},
	}
	references, diagnostics := discoverCandidateRipLogEvidence(registeredRoot{ID: "root", Path: rootPath}, candidate)
	if !reflect.DeepEqual(references, []string{"Album/eac.LOG", "Album/xld.log"}) || len(diagnostics) != 0 {
		t.Fatalf("unexpected rip-log evidence: refs=%v diagnostics=%v", references, diagnostics)
	}
}

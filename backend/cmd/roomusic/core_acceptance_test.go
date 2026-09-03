package main

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseSearchParsingTrimsAndEscapesWildcards(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/releases?q=%20%25Rock_%20", nil)
	query, err := parseReleaseSearch(request)
	if err != nil || query != "%Rock_" {
		t.Fatalf("unexpected parsed query %q, %v", query, err)
	}
	if escaped := escapeLikePattern(query); escaped != "~%Rock~_" {
		t.Fatalf("unexpected escaped pattern %q", escaped)
	}
	longQuery := strings.Repeat("x", 201)
	if _, err := parseReleaseSearch(httptest.NewRequest(http.MethodGet, "/api/v1/releases?q="+longQuery, nil)); err == nil {
		t.Fatal("overlong search query was accepted")
	}
}

func TestValidateLibraryPathEnforcesRealContainment(t *testing.T) {
	temporaryDirectory := t.TempDir()
	allowedRoot := filepath.Join(temporaryDirectory, "music")
	registeredDirectory := filepath.Join(allowedRoot, "collection")
	outsideDirectory := filepath.Join(temporaryDirectory, "music-backup")
	for _, directory := range []string{registeredDirectory, outsideDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create test directory: %v", err)
		}
	}

	validatedPath, err := validateLibraryPath(registeredDirectory, []string{allowedRoot})
	if err != nil {
		t.Fatalf("validate contained directory: %v", err)
	}
	if validatedPath != registeredDirectory {
		t.Fatalf("expected canonical directory %q, got %q", registeredDirectory, validatedPath)
	}

	if _, err := validateLibraryPath(filepath.Join(allowedRoot, "..", filepath.Base(outsideDirectory)), []string{allowedRoot}); err == nil {
		t.Fatal("path traversal outside the allowed root was accepted")
	}
	if _, err := validateLibraryPath(outsideDirectory, []string{allowedRoot}); err == nil {
		t.Fatal("prefix-collision directory outside the allowed root was accepted")
	}

	directoryLink := filepath.Join(allowedRoot, "linked-collection")
	if err := os.Symlink(registeredDirectory, directoryLink); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}
	if _, err := validateLibraryPath(directoryLink, []string{allowedRoot}); err == nil {
		t.Fatal("directory symlink registration was accepted")
	}
}

func TestRegisteredRootMustRemainARealDirectory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	realParent := filepath.Join(temporaryDirectory, "real-parent")
	registeredRoot := filepath.Join(realParent, "collection")
	if err := os.MkdirAll(registeredRoot, 0o755); err != nil {
		t.Fatalf("create registered root: %v", err)
	}
	if !registeredRootAvailable(registeredRoot) {
		t.Fatal("real registered root was rejected")
	}

	directLink := filepath.Join(temporaryDirectory, "root-link")
	if err := os.Symlink(registeredRoot, directLink); err != nil {
		t.Fatalf("create root symlink: %v", err)
	}
	if registeredRootAvailable(directLink) {
		t.Fatal("registered root replaced by a symlink was accepted")
	}

	ancestorLink := filepath.Join(temporaryDirectory, "parent-link")
	if err := os.Symlink(realParent, ancestorLink); err != nil {
		t.Fatalf("create ancestor symlink: %v", err)
	}
	if registeredRootAvailable(filepath.Join(ancestorLink, "collection")) {
		t.Fatal("registered root below a symlink ancestor was accepted")
	}
}

func TestFileSymlinkTargetsMustRemainWithinRoot(t *testing.T) {
	temporaryDirectory := t.TempDir()
	allowedRoot := filepath.Join(temporaryDirectory, "music")
	outsideRoot := filepath.Join(temporaryDirectory, "outside")
	if err := os.MkdirAll(allowedRoot, 0o755); err != nil {
		t.Fatalf("create allowed root: %v", err)
	}
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("create outside root: %v", err)
	}
	insideTarget := filepath.Join(allowedRoot, "inside.mp3")
	outsideTarget := filepath.Join(outsideRoot, "outside.mp3")
	for _, target := range []string{insideTarget, outsideTarget} {
		if err := os.WriteFile(target, []byte{0xff, 0xfb, 0x90, 0x64}, 0o644); err != nil {
			t.Fatalf("write test target: %v", err)
		}
	}

	insideLink := filepath.Join(allowedRoot, "inside-link.mp3")
	outsideLink := filepath.Join(allowedRoot, "outside-link.mp3")
	brokenLink := filepath.Join(allowedRoot, "broken-link.mp3")
	if err := os.Symlink(insideTarget, insideLink); err != nil {
		t.Fatalf("create inside link: %v", err)
	}
	if err := os.Symlink(outsideTarget, outsideLink); err != nil {
		t.Fatalf("create outside link: %v", err)
	}
	if err := os.Symlink(filepath.Join(allowedRoot, "missing.mp3"), brokenLink); err != nil {
		t.Fatalf("create broken link: %v", err)
	}

	if !isWithin(allowedRoot, insideLink) {
		t.Fatal("same-root file symlink was rejected")
	}
	if isWithin(allowedRoot, outsideLink) {
		t.Fatal("escaping file symlink was accepted")
	}
	if isWithin(allowedRoot, brokenLink) {
		t.Fatal("broken file symlink was accepted")
	}
}

func TestAudioParsersReturnMetadataAndRejectInvalidInput(t *testing.T) {
	temporaryDirectory := t.TempDir()
	flacPath := filepath.Join(temporaryDirectory, "fallback.flac")
	mp3Path := filepath.Join(temporaryDirectory, "fallback.mp3")
	if err := os.WriteFile(flacPath, createFLACFixture(t, []string{"TITLE=FLAC 标题", "ARTIST=FLAC 艺术家", "ALBUM=FLAC 专辑", "TRACKNUMBER=2/9", "DISCNUMBER=1/2"}), 0o644); err != nil {
		t.Fatalf("write FLAC fixture: %v", err)
	}
	if err := os.WriteFile(mp3Path, createMP3Fixture(t, map[string]string{"TIT2": "MP3 标题", "TPE1": "MP3 艺术家", "TALB": "MP3 专辑", "TRCK": "3/10", "TPOS": "2/2"}), 0o644); err != nil {
		t.Fatalf("write MP3 fixture: %v", err)
	}

	flacObservation, err := parseAudioFile(flacPath)
	if err != nil {
		t.Fatalf("parse valid FLAC fixture: %v", err)
	}
	if flacObservation.Title != "FLAC 标题" || flacObservation.Artist != "FLAC 艺术家" || flacObservation.Album != "FLAC 专辑" || flacObservation.TrackNumber != 2 || flacObservation.DiscNumber != 1 {
		t.Fatalf("unexpected FLAC observation: %+v", flacObservation)
	}

	mp3Observation, err := parseAudioFile(mp3Path)
	if err != nil {
		t.Fatalf("parse valid MP3 fixture: %v", err)
	}
	if mp3Observation.Title != "MP3 标题" || mp3Observation.Artist != "MP3 艺术家" || mp3Observation.Album != "MP3 专辑" || mp3Observation.TrackNumber != 3 || mp3Observation.DiscNumber != 2 {
		t.Fatalf("unexpected MP3 observation: %+v", mp3Observation)
	}

	for _, invalidFixture := range []struct {
		name    string
		path    string
		content []byte
	}{
		{name: "damaged FLAC", path: filepath.Join(temporaryDirectory, "damaged.flac"), content: []byte("not-flac")},
		{name: "damaged MP3", path: filepath.Join(temporaryDirectory, "damaged.mp3"), content: []byte("not-an-mp3")},
		{name: "unsupported extension", path: filepath.Join(temporaryDirectory, "track.ogg"), content: []byte("OggS")},
	} {
		t.Run(invalidFixture.name, func(t *testing.T) {
			if err := os.WriteFile(invalidFixture.path, invalidFixture.content, 0o644); err != nil {
				t.Fatalf("write invalid fixture: %v", err)
			}
			if _, err := parseAudioFile(invalidFixture.path); err == nil {
				t.Fatal("invalid or unsupported audio input was accepted")
			}
		})
	}
}

func TestFLACParserReadsAllCommentsAfterLongValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi-comment.flac")
	comments := []string{"TITLE=First", "ARTIST=Second", "ALBUM=Third"}
	if err := os.WriteFile(path, createFLACFixture(t, comments), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	observation, err := parseAudioFile(path)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if observation.Title != "First" || observation.Artist != "Second" || observation.Album != "Third" {
		t.Fatalf("comments were not all parsed: %+v", observation)
	}
}

func TestMissingReconciliationRequiresSuccessfulTerminalState(t *testing.T) {
	statusCases := []struct {
		name     string
		outcome  scanOutcome
		expected string
	}{
		{name: "complete", outcome: scanOutcome{Complete: true}, expected: "succeeded"},
		{name: "partial root", outcome: scanOutcome{}, expected: "incomplete"},
		{name: "fatal infrastructure failure", outcome: scanOutcome{Failed: true}, expected: "failed"},
		{name: "cancellation takes precedence", outcome: scanOutcome{Canceled: true, Failed: true}, expected: "canceled"},
	}
	for _, statusCase := range statusCases {
		t.Run(statusCase.name, func(t *testing.T) {
			if status := terminalScanStatus(statusCase.outcome); status != statusCase.expected {
				t.Fatalf("expected terminal status %q, got %q", statusCase.expected, status)
			}
		})
	}

	for _, status := range []string{"running", "failed", "canceled", "incomplete"} {
		if scanStatusAllowsMissingReconciliation(status) {
			t.Fatalf("status %q was allowed to mark sources missing", status)
		}
	}
	if !scanStatusAllowsMissingReconciliation("succeeded") {
		t.Fatal("successful scan was not allowed to reconcile missing sources")
	}
}

func TestScannerOnlyDiagnosesKnownUnsupportedAudioExtensions(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".ogg", ".opus", ".wav"} {
		if !isSupportedAudioExtension(extension) {
			t.Fatalf("supported extension %s was not recognized", extension)
		}
	}
	for _, extension := range []string{".aac", ".m4a", ".ape", ".dsf"} {
		if !isAudioCandidateExtension(extension) {
			t.Fatalf("unsupported audio extension %s was not recognized", extension)
		}
	}
	for _, extension := range []string{".jpg", ".png", ".txt", ".nfo"} {
		if isAudioCandidateExtension(extension) {
			t.Fatalf("non-audio extension %s was treated as an unsupported audio file", extension)
		}
	}
}

func TestTrackSourceIdentityIsStableOnlyForSameRootAndPath(t *testing.T) {
	originalIdentity, err := createTrackSourceIdentity("root-one", "album/./song.mp3")
	if err != nil {
		t.Fatalf("create original identity: %v", err)
	}
	repeatedIdentity, err := createTrackSourceIdentity("root-one", "album/song.mp3")
	if err != nil {
		t.Fatalf("create repeated identity: %v", err)
	}
	if originalIdentity != repeatedIdentity {
		t.Fatalf("same normalized source changed identity: %+v != %+v", originalIdentity, repeatedIdentity)
	}
	renamedIdentity, _ := createTrackSourceIdentity("root-one", "album/renamed.mp3")
	otherRootIdentity, _ := createTrackSourceIdentity("root-two", "album/song.mp3")
	if originalIdentity == renamedIdentity || originalIdentity == otherRootIdentity {
		t.Fatal("renamed or cross-root source inherited the original identity")
	}
	if _, err := createTrackSourceIdentity("root-one", "../escaped.mp3"); err == nil {
		t.Fatal("escaping source path was accepted as an identity")
	}
	if _, err := createTrackSourceIdentity("root-one", `..\escaped.mp3`); err == nil {
		t.Fatal("backslash path escape was accepted as an identity")
	}
	if _, err := createTrackSourceIdentity("root-one", `C:\music\track.mp3`); err == nil {
		t.Fatal("drive-qualified source path was accepted as an identity")
	}
}

func TestProductionFrontendServesRootFallbackAndAsset404(t *testing.T) {
	handler := productionFrontendHandler()
	for _, requestPath := range []string{"/", "/releases/example"} {
		responseRecorder := httptest.NewRecorder()
		handler.ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if responseRecorder.Code != http.StatusOK {
			t.Fatalf("expected frontend path %q to return 200, got %d", requestPath, responseRecorder.Code)
		}
		if !bytes.Contains(responseRecorder.Body.Bytes(), []byte("ROOMusic")) {
			t.Fatalf("frontend path %q did not serve the embedded index", requestPath)
		}
	}

	responseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing production asset returned %d instead of 404", responseRecorder.Code)
	}
}

func createFLACFixture(t *testing.T, comments []string) []byte {
	t.Helper()
	metadata := &bytes.Buffer{}
	writeLittleEndianString(t, metadata, "roomusic-test")
	if err := binary.Write(metadata, binary.LittleEndian, uint32(len(comments))); err != nil {
		t.Fatalf("write FLAC comment count: %v", err)
	}
	for _, comment := range comments {
		writeLittleEndianString(t, metadata, comment)
	}
	blockLength := metadata.Len()
	return append([]byte{'f', 'L', 'a', 'C', 0x84, byte(blockLength >> 16), byte(blockLength >> 8), byte(blockLength)}, metadata.Bytes()...)
}

func writeLittleEndianString(t *testing.T, destination *bytes.Buffer, value string) {
	t.Helper()
	if err := binary.Write(destination, binary.LittleEndian, uint32(len(value))); err != nil {
		t.Fatalf("write string length: %v", err)
	}
	if _, err := destination.WriteString(value); err != nil {
		t.Fatalf("write string: %v", err)
	}
}

func createMP3Fixture(t *testing.T, frames map[string]string) []byte {
	t.Helper()
	tagData := &bytes.Buffer{}
	for _, frameID := range []string{"TIT2", "TPE1", "TALB", "TRCK", "TPOS"} {
		value := frames[frameID]
		frameContent := append([]byte{0}, []byte(value)...)
		if _, err := tagData.WriteString(frameID); err != nil {
			t.Fatalf("write MP3 frame identifier: %v", err)
		}
		if err := binary.Write(tagData, binary.BigEndian, uint32(len(frameContent))); err != nil {
			t.Fatalf("write MP3 frame length: %v", err)
		}
		if _, err := tagData.Write([]byte{0, 0}); err != nil {
			t.Fatalf("write MP3 frame flags: %v", err)
		}
		if _, err := tagData.Write(frameContent); err != nil {
			t.Fatalf("write MP3 frame content: %v", err)
		}
	}
	tagLength := tagData.Len()
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(tagLength >> 21), byte(tagLength >> 14), byte(tagLength >> 7), byte(tagLength)}
	return append(header, tagData.Bytes()...)
}

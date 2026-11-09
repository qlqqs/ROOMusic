package library

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

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

	validatedPath, err := ValidatePath(registeredDirectory, []string{allowedRoot})
	if err != nil {
		t.Fatalf("validate contained directory: %v", err)
	}
	if validatedPath != registeredDirectory {
		t.Fatalf("expected canonical directory %q, got %q", registeredDirectory, validatedPath)
	}

	if _, err := ValidatePath(filepath.Join(allowedRoot, "..", filepath.Base(outsideDirectory)), []string{allowedRoot}); err == nil {
		t.Fatal("path traversal outside the allowed root was accepted")
	}
	if _, err := ValidatePath(outsideDirectory, []string{allowedRoot}); err == nil {
		t.Fatal("prefix-collision directory outside the allowed root was accepted")
	}

	directoryLink := filepath.Join(allowedRoot, "linked-collection")
	if err := os.Symlink(registeredDirectory, directoryLink); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}
	if _, err := ValidatePath(directoryLink, []string{allowedRoot}); err == nil {
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
	for _, extension := range []string{".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a", ".ape", ".wv", ".aif", ".aiff", ".dsf", ".dff"} {
		if !isSupportedAudioExtension(extension) {
			t.Fatalf("supported extension %s was not recognized", extension)
		}
	}
	for _, extension := range []string{".aac", ".wma", ".mka", ".tak"} {
		if !isAudioCandidateExtension(extension) || isSupportedAudioExtension(extension) {
			t.Fatalf("不支持的音频扩展名 %s 没有仅进入诊断候选", extension)
		}
	}
	for _, extension := range []string{".jpg", ".png", ".txt", ".nfo", ".ape", ".wv", ".aif", ".aiff"} {
		if isAudioCandidateExtension(extension) {
			t.Fatalf("扩展名 %s 不应进入不支持格式诊断", extension)
		}
	}

	t.Run("DST普通及CUE诊断", func(t *testing.T) {
		root := t.TempDir()
		chunk := func(id string, body []byte) []byte {
			data := append([]byte(id), binary.BigEndian.AppendUint64(nil, uint64(len(body)))...)
			data = append(data, body...)
			if len(body)%2 != 0 {
				data = append(data, 0)
			}
			return data
		}
		properties := append([]byte("SND "), chunk("FS  ", binary.BigEndian.AppendUint32(nil, 2822400))...)
		properties = append(properties, chunk("CHNL", append(binary.BigEndian.AppendUint16(nil, 2), "SLFTSRGT"...))...)
		properties = append(properties, chunk("CMPR", []byte{'D', 'S', 'T', ' ', 0})...)
		body := append([]byte("DSD "), chunk("FVER", []byte{1, 5, 0, 0})...)
		body = append(body, chunk("PROP", properties)...)
		body = append(body, chunk("DST ", []byte{1, 2, 3, 4})...)
		parent := filepath.Join(root, "compressed.dff")
		if err := os.WriteFile(parent, chunk("FRM8", body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, parseErr := parser.ParseAudioFile(parent)
		if !errors.Is(parseErr, parser.ErrUnsupportedEncoding) {
			t.Fatalf("DST 未进入不支持编码分支：%v", parseErr)
		}
		cuePath := filepath.Join(root, "album.cue")
		if err := os.WriteFile(cuePath, []byte("FILE \"compressed.dff\" WAVE\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		document, err := parser.ParseCueSheet(cuePath, root)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, relative string
			cue            *parser.CueDocument
		}{
			{"普通", "compressed.dff", nil},
			{"CUE", "album.cue", &document},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// 在诊断持久化处截获类别；无需数据库，也不把解析失败冒充已入库。
				stop := errors.New("已捕获诊断")
				executor := &cleanupFailureScanExecutor{diagnosticError: stop}
				pipeline := &scanPipeline{
					ctx: context.Background(), service: &Service{}, executor: executor,
					cache: &parseCache{}, scanID: "scan-id", root: registeredRoot{ID: "root-id", Path: root},
				}
				err := pipeline.apply(scanSlot{relative: tc.relative, cue: tc.cue, audio: []scanAudio{{
					path: parent, err: parseErr, diagnosticKind: "parse_failure", diagnosticMessage: "CUE 引用音频解析失败",
				}}})
				if !errors.Is(err, stop) || len(executor.diagnosticKinds) != 1 || executor.diagnosticKinds[0] != "unsupported_format" {
					t.Fatalf("诊断=%v，错误=%v；期望 unsupported_format", executor.diagnosticKinds, err)
				}
			})
		}
	})
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

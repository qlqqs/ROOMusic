package playback

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scanTime 带有纳秒残留：文件系统的修改时间有纳秒精度，扫描记录（TIMESTAMPTZ）只到微秒。
var scanTime = time.Date(2026, 10, 1, 8, 30, 15, 123456789, time.UTC)

// flacContent 是只有魔数的最小“FLAC”内容；容器核对只看文件头。
func flacContent(total int) []byte {
	content := make([]byte, total)
	copy(content, "fLaC")
	return content
}

// writeSource 在目录 root 下写入 relative 并把修改时间设为 modified，返回内容。
func writeSource(t *testing.T, root, relative string, content []byte, modified time.Time) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create source directory: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("set source modification time: %v", err)
	}
}

// recorded 返回扫描会记录的版本：大小加截到微秒的修改时间。
func recorded(size int, modified time.Time) expectedSource {
	length := int64(size)
	truncated := modified.UTC().Truncate(time.Microsecond)
	return expectedSource{Size: &length, ModifiedAt: &truncated, ContentFingerprint: "fingerprint-a"}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return root
}

func TestOpenVerifiedOpensTheScannedFileAndSignsItsVersion(t *testing.T) {
	root := realTempDir(t)
	content := flacContent(4096)
	writeSource(t, root, "Album/01.flac", content, scanTime)
	expected := recorded(len(content), scanTime)

	opened, err := openVerified(root, "Album/01.flac", expected, containerFLAC, "track-1")
	if err != nil {
		t.Fatalf("openVerified: %v", err)
	}
	defer opened.File.Close()
	if opened.Size != int64(len(content)) {
		t.Fatalf("Size = %d, want %d", opened.Size, len(content))
	}
	buffer := make([]byte, 4)
	if _, err := opened.File.ReadAt(buffer, 0); err != nil || string(buffer) != "fLaC" {
		t.Fatalf("opened handle does not read the file: %q, %v", buffer, err)
	}
	if len(opened.ETag) != 32 {
		t.Fatalf("ETag = %q, want 32 hexadecimal characters", opened.ETag)
	}

	again, err := openVerified(root, "Album/01.flac", expected, containerFLAC, "track-1")
	if err != nil {
		t.Fatalf("second openVerified: %v", err)
	}
	defer again.File.Close()
	if again.ETag != opened.ETag {
		t.Fatalf("ETag changed for an unchanged file: %q then %q", opened.ETag, again.ETag)
	}
}

func TestEntityTagChangesWithEveryVersionInput(t *testing.T) {
	base := recorded(4096, scanTime)
	original := entityTagValue("track-1", base, 4096)
	otherFingerprint := base
	otherFingerprint.ContentFingerprint = "fingerprint-b"
	laterModified := recorded(4096, scanTime.Add(time.Microsecond))
	laterModified.ContentFingerprint = base.ContentFingerprint
	for name, other := range map[string]string{
		"track":       entityTagValue("track-2", base, 4096),
		"fingerprint": entityTagValue("track-1", otherFingerprint, 4096),
		"size":        entityTagValue("track-1", base, 4097),
		"modified":    entityTagValue("track-1", laterModified, 4096),
	} {
		if other == original {
			t.Errorf("ETag does not depend on the %s", name)
		}
	}
}

func TestOpenVerifiedRejectsAFileThatDiffersFromTheScanRecord(t *testing.T) {
	root := realTempDir(t)
	content := flacContent(4096)
	writeSource(t, root, "01.flac", content, scanTime)
	writeSource(t, root, "02.flac", append([]byte("RIFF\x00\x00\x00\x00WAVE"), make([]byte, 100)...), scanTime)
	exact := recorded(len(content), scanTime)
	smaller := recorded(len(content)-1, scanTime)
	older := recorded(len(content), scanTime.Add(-time.Microsecond))
	noSize, noTime := exact, exact
	noSize.Size = nil
	noTime.ModifiedAt = nil

	for _, testCase := range []struct {
		name     string
		relative string
		expected expectedSource
		kind     container
	}{
		{"size differs", "01.flac", smaller, containerFLAC},
		{"modification time differs by one microsecond", "01.flac", older, containerFLAC},
		{"scan record has no size", "01.flac", noSize, containerFLAC},
		{"scan record has no modification time", "01.flac", noTime, containerFLAC},
		{"container does not match the format", "02.flac", recorded(112, scanTime), containerFLAC},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			opened, err := openVerified(root, testCase.relative, testCase.expected, testCase.kind, "track-1")
			if opened != nil {
				opened.File.Close()
			}
			if !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("openVerified error = %v, want ErrSourceChanged", err)
			}
		})
	}
}

func TestOpenVerifiedRefusesPathsOutsideTheRootAndNonRegularFiles(t *testing.T) {
	root := realTempDir(t)
	outside := realTempDir(t)
	content := flacContent(4096)
	writeSource(t, outside, "secret.flac", content, scanTime)
	writeSource(t, root, "inside/real.flac", content, scanTime)
	if err := os.Symlink(filepath.Join(outside, "secret.flac"), filepath.Join(root, "escape.flac")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}
	if err := os.Symlink("inside/real.flac", filepath.Join(root, "alias.flac")); err != nil {
		t.Fatalf("create in-root symlink: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.flac"), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	expected := recorded(len(content), scanTime)

	for _, relative := range []string{
		"missing.flac",
		"escape.flac",
		"escape-dir/secret.flac",
		"../" + filepath.Base(outside) + "/secret.flac",
		"inside/../../" + filepath.Base(outside) + "/secret.flac",
		"/etc/passwd",
		"directory.flac",
	} {
		t.Run(relative, func(t *testing.T) {
			opened, err := openVerified(root, relative, expected, containerFLAC, "track-1")
			if opened != nil {
				opened.File.Close()
			}
			if !errors.Is(err, ErrSourceUnavailable) {
				t.Fatalf("openVerified(%q) error = %v, want ErrSourceUnavailable", relative, err)
			}
		})
	}

	// 指向根目录内另一个文件的链接没有越界：打开的就是那个文件，大小与修改时间核对照常进行。
	opened, err := openVerified(root, "alias.flac", expected, containerFLAC, "track-1")
	if err != nil {
		t.Fatalf("an in-root symlink was refused: %v", err)
	}
	opened.File.Close()
}

func TestOpenVerifiedReportsAMissingRoot(t *testing.T) {
	missing := filepath.Join(realTempDir(t), "gone")
	if _, err := openVerified(missing, "01.flac", recorded(10, scanTime), containerFLAC, "track-1"); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("openVerified in a missing root = %v, want ErrSourceUnavailable", err)
	}
}

func TestOpenVerifiedReportsAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not apply to root")
	}
	root := realTempDir(t)
	content := flacContent(4096)
	writeSource(t, root, "01.flac", content, scanTime)
	if err := os.Chmod(filepath.Join(root, "01.flac"), 0); err != nil {
		t.Fatalf("remove permissions: %v", err)
	}
	if _, err := openVerified(root, "01.flac", recorded(len(content), scanTime), containerFLAC, "track-1"); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("openVerified of an unreadable file = %v, want ErrSourceUnavailable", err)
	}
}

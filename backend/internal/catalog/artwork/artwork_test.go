package artwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtworkDimensionsRejectMalformedAndReadWebP(t *testing.T) {
	if width, height := dimensions([]byte{0xff, 0xd8, 0xff}, "image/jpeg"); width != 0 || height != 0 {
		t.Fatalf("malformed JPEG dimensions = %dx%d", width, height)
	}
	webp := make([]byte, 30)
	copy(webp[:4], "RIFF")
	copy(webp[8:12], "WEBP")
	copy(webp[12:16], "VP8X")
	webp[24], webp[27] = 1, 2 // WebP 将宽高保存为实际值减一。
	if width, height := dimensions(webp, "image/webp"); width != 2 || height != 3 {
		t.Fatalf("WebP dimensions = %dx%d, want 2x3", width, height)
	}
}

func TestSafeStorageKeyOnlyAcceptsContentAddressedNames(t *testing.T) {
	hashed := strings.Repeat("a1", 32) + ".png"
	for _, key := range []string{hashed, "catalog-cover.webp", "original-cover.png", "a_b-c.JPEG", strings.Repeat("x", 255)} {
		if !safeStorageKey(key) {
			t.Errorf("safeStorageKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{
		"", ".", "..", strings.Repeat("x", 256), "a/b.png", `a\b.png`, "../escape.png", `quote".png`, "with space.png", "tab\t.png",
		"new\nline.png", "名字.png", "nul\x00.png", "colon:name.png",
	} {
		if safeStorageKey(key) {
			t.Errorf("safeStorageKey(%q) = true, want false", key)
		}
	}
}

func TestReadReturnsTheStoredBytesAndFailsClosed(t *testing.T) {
	dataDirectory := t.TempDir()
	store := NewStore(dataDirectory)
	if err := os.MkdirAll(filepath.Join(dataDirectory, "artwork"), 0o750); err != nil {
		t.Fatalf("create artwork directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, "artwork", "cover.png"), []byte("bytes"), 0o640); err != nil {
		t.Fatalf("write artwork fixture: %v", err)
	}
	// 数据目录之外的文件：即使键里带着路径也不能读到。
	if err := os.WriteFile(filepath.Join(dataDirectory, "secret.txt"), []byte("secret"), 0o640); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}

	data, err := store.Read(Resource{ID: "cover.png", MIMEType: "image/png"})
	if err != nil || string(data) != "bytes" {
		t.Fatalf("Read = %q, %v", data, err)
	}
	if _, err := store.Read(Resource{ID: "missing.png"}); err == nil {
		t.Fatal("reading a missing file succeeded")
	}
	for _, key := range []string{"../secret.txt", "", "..", `quote".png`} {
		if data, err := store.Read(Resource{ID: key}); err == nil {
			t.Fatalf("Read(%q) = %q, want an error", key, data)
		}
	}
}

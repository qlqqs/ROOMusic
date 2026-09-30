package artwork

import "testing"

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

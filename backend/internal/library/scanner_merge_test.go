package library

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

func TestScannerMergeArtworkSourceErrorsAcrossRounds(t *testing.T) {
	for _, relative := range []string{
		"Album/cover.png",
		"Album/00-front.png",
		"Album/Scans/cover.png",
		"Album/Scans/00-front.png",
	} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			writeArtworkFile(t, filepath.Join(root, filepath.Dir(relative)), filepath.Base(relative), []byte("invalid image"))
			data, mimeType, err := discoverFolderArtwork(root, []string{"Album"})
			if !errors.Is(err, errArtworkSource) || len(data) != 0 || mimeType != "" {
				t.Fatalf("无可用封面时应保留来源错误分类：data=%x MIME=%q err=%v", data, mimeType, err)
			}

			// 前三轮或第四轮较早的无效图片，不应阻止第四轮找到有效图片。
			want := minimalPNGArtwork()
			writeArtworkFile(t, filepath.Join(root, "Album", "Scans"), "zz-front.png", want)
			data, mimeType, err = discoverFolderArtwork(root, []string{"Album"})
			if err != nil || !bytes.Equal(data, want) || mimeType != "image/png" {
				t.Fatalf("第四轮应返回可用封面：data=%x MIME=%q err=%v", data, mimeType, err)
			}
		})
	}
}

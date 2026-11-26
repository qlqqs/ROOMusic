package library

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"golang.org/x/image/tiff"
)

func TestListAttachmentsFiltersAndReadsImageHeaders(t *testing.T) {
	root := registeredRoot{Path: t.TempDir(), filter: newScanFilter([]string{"Album/Extras"})}
	var tif bytes.Buffer
	if err := tiff.Encode(&tif, image.NewRGBA(image.Rect(0, 0, 5, 7)), nil); err != nil {
		t.Fatal(err)
	}
	// VP8X 文件头声明 3×4。附件登记只读取尺寸。
	webp := []byte("RIFF\x16\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00\x02\x00\x00\x03\x00\x00")
	for path, data := range map[string][]byte{
		"Album/ArTwOrK/pages.TIFF":    tif.Bytes(),
		"Album/sCaNs/page.WeBp":       webp,
		"Album/aRT/liner.PDF":         []byte("%PDF-1.7"),
		"Album/Extras/excluded.pdf":   []byte("%PDF-1.7"),
		"Album/Notes/unlisted.pdf":    []byte("%PDF-1.7"),
		"Album/sCaNs/deep/hidden.pdf": []byte("%PDF-1.7"),
		"Album/.hidden.pdf":           []byte("%PDF-1.7"),
	} {
		writeFixtureFile(t, root, path, data)
	}
	if err := os.Symlink("aRT/liner.PDF", filepath.Join(root.Path, "Album", "linked.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("aRT", filepath.Join(root.Path, "Album", "Art")); err != nil {
		t.Fatal(err)
	}
	directory := listAttachments(root, "Album")
	got, err := directory.read(root)
	want := []ingest.Attachment{
		{RelativePath: "Album/ArTwOrK/pages.TIFF", Kind: "image", Format: "tiff", Width: 5, Height: 7},
		{RelativePath: "Album/aRT/liner.PDF", Kind: "document"},
		{RelativePath: "Album/sCaNs/page.WeBp", Kind: "image", Format: "webp", Width: 3, Height: 4},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("附属文件 = %+v，期望 %+v，错误 %v", got, want, err)
	}

	if err := os.Remove(filepath.Join(root.Path, want[0].RelativePath)); err != nil {
		t.Fatal(err)
	}
	if got, err := directory.read(root); err == nil || got != nil {
		t.Fatalf("列举后丢失图片应报错并放弃本次清单，结果 %+v、错误 %v", got, err)
	}
	if got, err := listAttachments(root, "Missing").read(root); err == nil || got != nil {
		t.Fatalf("不可读目录应报错，结果 %+v、错误 %v", got, err)
	}
}

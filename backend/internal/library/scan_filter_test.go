package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFilterHiddenFilesAndDirectories(t *testing.T) {
	filter := newScanFilter(nil)
	for _, name := range []string{".hidden", "._x.flac", ".Trash-1000", ".DS_Store", ".@#local"} {
		t.Run(name, func(t *testing.T) {
			if !filter.skipFile(name) || !filter.skipDir("Album/"+name, name) {
				t.Fatalf("隐藏文件或目录未被过滤：%q", name)
			}
		})
	}
}

func TestScanFilterBuiltInDirectoriesMatchWholeNamesIgnoringCase(t *testing.T) {
	filter := newScanFilter(nil)
	for _, name := range []string{
		"@eaDir", "#recycle", "@Recycle", "@Recently-Snapshot", "#snapshot",
		"$RECYCLE.BIN", "System Volume Information", "lost+found", "__MACOSX", "@appmeta", "@team",
	} {
		t.Run(name, func(t *testing.T) {
			for _, variant := range []string{name, strings.ToLower(name), strings.ToUpper(name)} {
				if !filter.skipDir("Artist/"+variant, variant) {
					t.Errorf("内置目录未被过滤：%q", variant)
				}
				if filter.skipFile(variant) {
					t.Errorf("目录名单不应过滤同名普通文件：%q", variant)
				}
			}
		})
	}
	for _, name := range []string{"my.recycle", "recycle", "Music@eaDir2", "lost+found music", "thumb", "track.flac"} {
		if filter.skipDir("Album/"+name, name) || filter.skipFile(name) {
			t.Errorf("误过滤普通文件或目录：%q", name)
		}
	}
}

func TestScanFilterUserExclusionsUseExactRelativeDirectory(t *testing.T) {
	filter := newScanFilter([]string{"Artist/Bootlegs"})
	for _, testCase := range []struct {
		relative string
		want     bool
	}{
		{relative: "Artist/Bootlegs", want: true},
		{relative: "Artist/Bootlegs Deluxe"},
		{relative: "Artist/bootlegs"},
		{relative: "Other/Bootlegs"},
		{relative: "Bootlegs"},
	} {
		if got := filter.skipDir(testCase.relative, filepath.Base(testCase.relative)); got != testCase.want {
			t.Errorf("skipDir(%q) = %t，期望 %t", testCase.relative, got, testCase.want)
		}
	}
	if filter.skipFile("Bootlegs") {
		t.Fatal("目录排除项误伤同名普通文件")
	}
}

func TestScanFilterCountingSkipsSubtreesButNotRegisteredRoot(t *testing.T) {
	for _, rootName := range []string{".registered", "@eaDir"} {
		t.Run(rootName, func(t *testing.T) {
			rootPath := filepath.Join(t.TempDir(), rootName)
			for _, relative := range []string{
				".hidden/deep/song.flac", "@EADIR/deep/song.flac", "Artist/Bootlegs/deep/song.flac",
				"._x.flac", "cover.jpg", "visible.flac", "sheet.CUE", "unsupported.tak",
				"supported.APE", "supported.WV", "supported.AIF", "supported.AIFF",
				"Artist/Bootlegs Deluxe/song.flac", "Other/Bootlegs/song.flac", "my.recycle/song.flac",
			} {
				path := filepath.Join(rootPath, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			roots := []registeredRoot{{ID: "root", Path: rootPath, Exclusions: []string{"Artist/Bootlegs"}}}
			service := &Service{}
			// 只验证文件计数，进度持久化失败不影响本次计数。
			executor := failingScanExecutor{err: errors.New("未连接数据库")}
			feedback := newScanFeedback(service, executor, "scan", roots)
			if !service.countScanRoots(context.Background(), executor, "scan", roots, feedback) {
				t.Fatal("过滤导致根计数不完整")
			}
			if !feedback.progress.CountComplete || feedback.progress.FilesTotal == nil || *feedback.progress.FilesTotal != 10 || feedback.counts[0].files != 10 {
				t.Fatalf("预计数未跳过过滤子树或误过滤登记根：progress=%+v counts=%+v", feedback.progress, feedback.counts)
			}
		})
	}
}

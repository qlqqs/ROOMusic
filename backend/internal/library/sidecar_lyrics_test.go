package library

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

// lyricsDirectorySnapshot 同时检查目录项、内容、权限与 mtime；读取造成的 atime 变化不算写入。
func lyricsDirectorySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%s %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(" %x", sha256.Sum256(data))
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += " " + target
		}
		snapshot[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func requireLyricsDirectoryUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	if after := lyricsDirectorySnapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("扫描或读取改写了音乐目录：之前=%v，之后=%v", before, after)
	}
}

func TestSidecarLyricsMatching(t *testing.T) {
	for _, tc := range []struct {
		name, audio     string
		files           map[string]string
		directory, link string
		want            string
	}{
		{name: "精确名优先", audio: "Song.flac", files: map[string]string{"Song.lrc": "精确", "Song.LRC": "变体", "song.lrc": "另一变体"}, want: "精确"},
		{name: "扩展名大小写", audio: "Song.flac", files: map[string]string{"Song.LRC": "歌词"}, want: "歌词"},
		{name: "主名大小写", audio: "Song.flac", files: map[string]string{"sOnG.lrc": "歌词"}, want: "歌词"},
		{name: "NFC 对 NFD", audio: "Café.flac", files: map[string]string{"Cafe\u0301.LRC": "歌词"}, want: "歌词"},
		{name: "NFD 对 NFC", audio: "Cafe\u0301.flac", files: map[string]string{"CAFÉ.lrc": "歌词"}, want: "歌词"},
		{name: "多个变体无精确名", audio: "Song.flac", files: map[string]string{"song.lrc": "甲", "SONG.LRC": "乙"}},
		{name: "多个规范化变体无精确名", audio: "Café.flac", files: map[string]string{"Cafe\u0301.lrc": "甲", "CAFÉ.LRC": "乙"}},
		{name: "精确目录不回退", audio: "Song.flac", directory: "Song.lrc", files: map[string]string{"song.LRC": "变体"}},
		{name: "精确符号链接不回退", audio: "Song.flac", link: "Song.lrc", files: map[string]string{"song.LRC": "变体"}},
		{name: "唯一变体符号链接不取", audio: "Song.flac", link: "SONG.LRC"},
		{name: "隐藏歌词不取", audio: ".Song.flac", files: map[string]string{".Song.lrc": "隐藏"}},
		{name: "不含音频扩展名", audio: "Song.flac", files: map[string]string{"Song.flac.lrc": "错误"}},
		{name: "只去掉最后扩展名", audio: "a.b.flac", files: map[string]string{"a.b.lrc": "歌词"}, want: "歌词"},
		{name: "不查子目录", audio: "Song.flac", files: map[string]string{"Sub/Song.lrc": "错误"}},
		{name: "空文件", audio: "Song.flac", files: map[string]string{"Song.lrc": ""}},
		{name: "原始字节超限", audio: "Song.flac", files: map[string]string{"Song.lrc": strings.Repeat("\x00", parser.MaxExternalLyricsBytes) + "a"}},
		{name: "原始字节恰好上限", audio: "Song.flac", files: map[string]string{"Song.lrc": strings.Repeat("\x00", parser.MaxExternalLyricsBytes-1) + "a"}, want: "a"},
		{name: "规范化后超限", audio: "Song.flac", files: map[string]string{"Song.lrc": strings.Repeat("a", parser.MaxLyricsBytes+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := registeredRoot{Path: t.TempDir()}
			for name, text := range tc.files {
				writeFixtureFile(t, root, name, []byte(text))
			}
			if tc.directory != "" {
				if err := os.Mkdir(filepath.Join(root.Path, tc.directory), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.link != "" {
				writeFixtureFile(t, root, "target.txt", []byte("链接内容"))
				if err := os.Symlink("target.txt", filepath.Join(root.Path, tc.link)); err != nil {
					t.Fatal(err)
				}
			}
			before := lyricsDirectorySnapshot(t, root.Path)
			var index sidecarLyricsIndex
			if got := index.read(root.Path, filepath.Join(root.Path, tc.audio), scanFilter{}); got != tc.want {
				t.Fatalf("歌词=%q，期望=%q", got, tc.want)
			}
			requireLyricsDirectoryUnchanged(t, root.Path, before)
		})
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealMusicReadOnlySmoke is opt-in and never runs in CI by default.
func TestRealMusicReadOnlySmoke(t *testing.T) {
	root := os.Getenv("ROOMUSIC_REAL_MUSIC_ROOT")
	if root == "" {
		t.Skip("ROOMUSIC_REAL_MUSIC_ROOT 未设置")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("真实音乐目录不可用")
	}
	checked := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || checked >= 24 {
			return nil
		}
		switch filepath.Ext(path) {
		case ".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a":
			if _, parseErr := parseAudioFile(path); parseErr != nil {
				t.Errorf("音频解析失败 (%s): %v", filepath.Base(path), parseErr)
			}
			checked++
		case ".cue":
			if _, _, parseErr := parseCue(path); parseErr != nil {
				t.Errorf("CUE 解析失败 (%s): %v", filepath.Base(path), parseErr)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历真实音乐目录失败")
	}
	if checked == 0 {
		t.Fatal("真实音乐目录中未找到支持的音频")
	}
}

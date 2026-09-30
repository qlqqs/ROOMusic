package parser

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
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
			if _, parseErr := ParseAudioFile(path); parseErr != nil {
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

// TestRealMusicFingerprintCoverage 只读遍历真实库，按种类汇总内容指纹的覆盖情况：计数、失败数、
// 需要额外读盘的文件数与字节数、重复指纹数和耗时。只输出汇总，不输出任何路径或文件名。
// 未设置 ROOMUSIC_REAL_MUSIC_ROOT 时跳过，从不写入该目录。
func TestRealMusicFingerprintCoverage(t *testing.T) {
	root := os.Getenv("ROOMUSIC_REAL_MUSIC_ROOT")
	if root == "" {
		t.Skip("ROOMUSIC_REAL_MUSIC_ROOT 未设置")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("真实音乐目录不可用")
	}

	type tally struct {
		files     int
		readFiles int
		readBytes int64
		elapsed   time.Duration
	}
	const consistencySample = 20
	byKind := map[string]*tally{}
	occurrences := map[string]int{}
	var audioFiles, parseFailures, fingerprintFailures, consistencyChecked, consistencyMismatches int
	var parseElapsed time.Duration
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a":
		default:
			return nil
		}
		audioFiles++
		started := time.Now()
		observation, parseErr := ParseAudioFile(path)
		parseElapsed += time.Since(started)
		if parseErr != nil {
			parseFailures++
			return nil
		}
		fingerprint := observation.ContentFingerprint
		var readBytes int64
		var elapsed time.Duration
		onDemand := fingerprint == ""
		if onDemand {
			started = time.Now()
			computed, computeErr := ContentFingerprint(context.Background(), path)
			elapsed = time.Since(started)
			if computeErr != nil {
				fingerprintFailures++
				return nil
			}
			fingerprint = computed
			if fileInfo, infoErr := entry.Info(); infoErr == nil {
				readBytes = fileInfo.Size()
			}
		} else if consistencyChecked < consistencySample {
			// 解析时带出的指纹必须与按需计算的结果一致。
			consistencyChecked++
			if computed, computeErr := ContentFingerprint(context.Background(), path); computeErr != nil || computed != fingerprint {
				consistencyMismatches++
			}
		}
		kind, _, _ := strings.Cut(fingerprint, ":")
		if byKind[kind] == nil {
			byKind[kind] = &tally{}
		}
		entryTally := byKind[kind]
		entryTally.files++
		entryTally.elapsed += elapsed
		if onDemand {
			entryTally.readFiles++
			entryTally.readBytes += readBytes
		}
		occurrences[fingerprint]++
		return nil
	})
	if walkErr != nil {
		t.Fatalf("遍历真实音乐目录失败")
	}
	if audioFiles == 0 {
		t.Fatal("真实音乐目录中未找到支持的音频")
	}

	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	fingerprinted := 0
	for _, kind := range kinds {
		entryTally := byKind[kind]
		fingerprinted += entryTally.files
		t.Logf("种类 %s：%d 个文件；需要额外读盘 %d 个（%d 字节，%d ms）", kind, entryTally.files, entryTally.readFiles, entryTally.readBytes, entryTally.elapsed.Milliseconds())
	}
	duplicateGroups, duplicateFiles := 0, 0
	for _, count := range occurrences {
		if count > 1 {
			duplicateGroups++
			duplicateFiles += count
		}
	}
	t.Logf("音频文件 %d 个；得到指纹 %d 个；解析失败 %d 个；指纹计算失败 %d 个", audioFiles, fingerprinted, parseFailures, fingerprintFailures)
	t.Logf("不同指纹 %d 个；重复指纹 %d 组，涉及 %d 个文件", len(occurrences), duplicateGroups, duplicateFiles)
	t.Logf("解析耗时 %d ms；解析带出与按需计算一致性抽查 %d 个，不一致 %d 个", parseElapsed.Milliseconds(), consistencyChecked, consistencyMismatches)
	if parseFailures > 0 || fingerprintFailures > 0 {
		t.Errorf("%d 个文件解析失败，%d 个文件指纹计算失败", parseFailures, fingerprintFailures)
	}
	if consistencyMismatches > 0 {
		t.Errorf("%d 个文件的解析指纹与按需计算不一致", consistencyMismatches)
	}
}

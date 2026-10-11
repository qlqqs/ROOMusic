package library

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

// TestRealMusicLyricsCoverage 只读遍历真实库，汇总能提取歌词的文件：总数、按来源键与形态（同步或纯文本，形态取自后端
// 接口使用的 catalog.ParseLyrics）的份数、解析后行数与原文大小的范围，以及 MP3 的 ID3 主版本分布（v2.2 的帧不被读取）。
// 只输出汇总，不输出任何路径、文件名或歌词内容；未设置 ROOMUSIC_REAL_MUSIC_ROOT 时跳过，从不写入该目录，也不保存任何派生文件。
func TestRealMusicLyricsCoverage(t *testing.T) {
	root := os.Getenv("ROOMUSIC_REAL_MUSIC_ROOT")
	if root == "" {
		t.Skip("ROOMUSIC_REAL_MUSIC_ROOT 未设置")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("真实音乐目录不可用")
	}

	var audioFiles, parseFailures, withLyrics, unparsable int
	var largestRawBytes, mostLines int
	byKey := map[string]int{}
	byKind := map[string]int{}
	byFormat := map[string]int{}
	id3Majors := map[byte]int{}
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		switch extension {
		case ".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a":
		default:
			return nil
		}
		audioFiles++
		if extension == ".mp3" {
			if file, openErr := os.Open(path); openErr == nil {
				header := make([]byte, 10)
				if read, _ := file.Read(header); read == 10 && string(header[:3]) == "ID3" {
					id3Majors[header[3]]++
				}
				_ = file.Close()
			}
		}
		observation, parseErr := parser.ParseAudioFile(path)
		if parseErr != nil {
			parseFailures++
			return nil
		}
		if observation.Lyrics == "" {
			return nil
		}
		withLyrics++
		byKey[observation.LyricsSource]++
		byFormat[extension]++
		largestRawBytes = max(largestRawBytes, len(observation.Lyrics))
		lyrics, ok := catalog.ParseLyrics(observation.Lyrics)
		if !ok {
			unparsable++
			return nil
		}
		byKind[lyrics.Kind]++
		mostLines = max(mostLines, len(lyrics.Lines))
		return nil
	})
	if walkErr != nil {
		t.Fatalf("遍历真实音乐目录失败")
	}
	if audioFiles == 0 {
		t.Fatal("真实音乐目录中未找到支持的音频")
	}
	sorted := func(counts map[string]int) []string {
		result := make([]string, 0, len(counts))
		for name, count := range counts {
			result = append(result, name+"="+strconv.Itoa(count))
		}
		sort.Strings(result)
		return result
	}
	majors := make([]string, 0, len(id3Majors))
	for major, count := range id3Majors {
		majors = append(majors, "v2."+strconv.Itoa(int(major))+"="+strconv.Itoa(count))
	}
	sort.Strings(majors)
	t.Logf("音频文件 %d（解析失败 %d），带歌词 %d（解析后无可显示行 %d）", audioFiles, parseFailures, withLyrics, unparsable)
	t.Logf("来源键：%v；形态：%v；文件类型：%v", sorted(byKey), sorted(byKind), sorted(byFormat))
	t.Logf("歌词原文最大 %d 字节，解析后最多 %d 行；MP3 的 ID3 主版本：%v", largestRawBytes, mostLines, majors)
}

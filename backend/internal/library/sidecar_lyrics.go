package library

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"golang.org/x/text/unicode/norm"
)

// sidecarLyricsIndex 只保留当前音频目录的歌词文件名；遍历返回父目录时重建，不缓存歌词内容。
type sidecarLyricsIndex struct {
	directory string
	names     map[string]bool
	variants  map[string][]string
}

func (index *sidecarLyricsIndex) enterDirectory(root, directory string) {
	if index.directory == directory {
		return
	}
	index.directory = directory
	index.names = make(map[string]bool)
	index.variants = make(map[string][]string)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || !isWithin(root, directory) {
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		extension := filepath.Ext(name)
		if !strings.EqualFold(extension, ".lrc") {
			continue
		}
		index.names[name] = true
		key := externalLyricsNameKey(strings.TrimSuffix(name, extension))
		index.variants[key] = append(index.variants[key], name)
	}
}

// externalLyricsNameKey 先做 NFC，再取每个 SimpleFold 循环中的最小字符，保持与 EqualFold 相同的比较语义。
func externalLyricsNameKey(stem string) string {
	return strings.Map(func(character rune) rune {
		minimum := character
		for folded := unicode.SimpleFold(character); folded != character; folded = unicode.SimpleFold(folded) {
			minimum = min(minimum, folded)
		}
		return minimum
	}, norm.NFC.String(stem))
}

func (index *sidecarLyricsIndex) read(root, audioPath string, filter scanFilter) string {
	directory := filepath.Dir(audioPath)
	index.enterDirectory(root, directory)
	base := filepath.Base(audioPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	name := stem + ".lrc"
	if !index.names[name] {
		variants := index.variants[externalLyricsNameKey(stem)]
		if len(variants) != 1 {
			return ""
		}
		name = variants[0]
	}
	if filter.skipFile(name) {
		return ""
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > parser.MaxExternalLyricsBytes {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, parser.MaxExternalLyricsBytes+1))
	if err != nil {
		return ""
	}
	text, ok := parser.DecodeExternalLyrics(data)
	if !ok {
		return ""
	}
	return text
}

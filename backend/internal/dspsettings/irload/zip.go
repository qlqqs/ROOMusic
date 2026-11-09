package irload

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const (
	MaxZIPEntries = 64
	MaxZIPBytes   = 256 << 20
)

// Source 是内存中的一个 IR 文件，不是可供打开的磁盘路径。
type Source struct {
	Name string
	Data []byte
}

// Archive 的 Sources 与 Config.Paths 一一对应，重复引用共享只读 Data。
// 无 cfg 时 Config 为 nil，Sources 只含唯一的音频文件。
// 音频内容留给后续解码边界；这里只校验容器、cfg 和引用，不声称完成音频核验。
type Archive struct {
	Config     *CFG
	ConfigName string
	Sources    []Source
}

type cfgCandidate struct {
	name   string
	config *CFG
}

// ParseZIP 在内存解压并按实际读取字节计总量；不访问文件系统或网络。
// cfg 优先级依次为目标采样率匹配、输入声道数匹配、文件名字典序。
func ParseZIP(data []byte, sampleRate, channels int) (*Archive, error) {
	if sampleRate <= 0 || channels < 1 || channels > 8 {
		return nil, fmt.Errorf("irload: invalid target format")
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("irload: invalid zip: %w", err)
	}
	if len(r.File) > MaxZIPEntries {
		return nil, fmt.Errorf("irload: zip entry limit exceeded")
	}
	files := make(map[string]Source)
	var candidates []cfgCandidate
	var audio []Source
	total := int64(0)
	for _, f := range r.File {
		name, err := archiveName(".", f.Name)
		if err != nil {
			return nil, err
		}
		if !f.Mode().IsRegular() && !f.FileInfo().IsDir() {
			return nil, fmt.Errorf("irload: non-regular zip entry")
		}
		contents, err := readEntry(f, int64(MaxZIPBytes)-total)
		if err != nil {
			return nil, err
		}
		total += int64(len(contents))
		if f.FileInfo().IsDir() {
			if len(contents) != 0 {
				return nil, fmt.Errorf("irload: zip directory contains data")
			}
			continue
		}
		key := strings.ToLower(name)
		if _, exists := files[key]; exists {
			return nil, fmt.Errorf("irload: ambiguous zip filename")
		}
		source := Source{Name: name, Data: contents}
		files[key] = source
		ext := strings.ToLower(path.Ext(name))
		if ext == ".cfg" {
			cfg, err := ParseCFG(contents)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, cfgCandidate{name: name, config: cfg})
		} else if audioExtension(ext) {
			audio = append(audio, source)
		}
	}
	if len(candidates) == 0 {
		if len(audio) != 1 {
			return nil, fmt.Errorf("irload: zip without cfg must contain exactly one audio file")
		}
		return &Archive{Sources: audio}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if am, bm := a.config.SampleRate == sampleRate, b.config.SampleRate == sampleRate; am != bm {
			return am
		}
		if am, bm := a.config.InputChannels == channels, b.config.InputChannels == channels; am != bm {
			return am
		}
		return a.name < b.name
	})
	selected := candidates[0]
	archive := &Archive{Config: selected.config, ConfigName: selected.name}
	for _, p := range selected.config.Paths {
		name, err := archiveName(path.Dir(selected.name), p.File)
		if err != nil {
			return nil, cfgError(p.Line, "IR reference must stay inside zip")
		}
		source, exists := files[strings.ToLower(name)]
		if !exists {
			return nil, cfgError(p.Line, "referenced IR file is missing")
		}
		ext := strings.ToLower(path.Ext(source.Name))
		if !audioExtension(ext) && ext != ".pcm" && ext != ".dbl" {
			return nil, cfgError(p.Line, "unsupported IR file format")
		}
		if (ext == ".pcm" || ext == ".dbl") && p.Channel != 0 {
			return nil, cfgError(p.Line, "raw IR has only channel zero")
		}
		archive.Sources = append(archive.Sources, source)
	}
	return archive, nil
}

func readEntry(f *zip.File, remaining int64) ([]byte, error) {
	// 声明大小只用于提前拒绝；真正的限额仍由有界读取和累计字节保证。
	if f.UncompressedSize64 > uint64(remaining) {
		return nil, fmt.Errorf("irload: zip decompressed size limit exceeded")
	}
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("irload: open zip entry: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(r, remaining+1))
	closeErr := r.Close()
	if int64(len(data)) > remaining {
		return nil, fmt.Errorf("irload: zip decompressed size limit exceeded")
	}
	if readErr != nil {
		return nil, fmt.Errorf("irload: read zip entry: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("irload: close zip entry: %w", closeErr)
	}
	return data, nil
}

func archiveName(base, name string) (string, error) {
	// cfg 可以使用 Windows 分隔符，但不能使用驱动器、绝对路径或 URL。
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00") {
		return "", fmt.Errorf("irload: invalid zip-relative path")
	}
	name = path.Join(base, name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("irload: path escapes zip")
	}
	return name, nil
}

func audioExtension(ext string) bool {
	switch ext {
	case ".wav", ".flac", ".aiff", ".aif", ".m4a":
		return true
	}
	return false
}

// Package parser 解析音频文件与 CUE 抄本，只产出安全、有界的事实观察，不读取音乐库之外的位置，
// 也不写入任何文件。它是叶子包：只依赖标准库、字符集解码库、catalog/metadata 与 catalog/classical。
package parser

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type AudioObservation struct {
	Title, Artist, Album    string
	AlbumArtist             string
	Compilation             bool
	TrackNumber, DiscNumber int
	SourceKind              string
	Inferred                bool
	InferredFields          map[string]bool
	Artwork                 []byte
	ArtworkMIME             string
	DurationSeconds         float64
	Codec                   string
	BitDepth                int
	SampleRate              int
	Channels                int
	Bitrate                 int
	Year                    int
	Genres                  []string
	Work                    string
	MovementName            string
	MovementNumber          int
	Catalog                 string
	ISRC                    string
	SourceType              string
	MediaType               string
	Edition                 string
	Label                   string
	Barcode                 string
	Credits                 []metadata.Credit
	ArtistTagEvidence       []metadata.ArtistTagEvidence
	RawAtoms                map[string]string
	// ContentFingerprint 是解析时无需额外读盘就能得到的内容指纹（FLAC 的音频 MD5、OGG 的音频包哈希）；
	// 其他格式为空，需要时用 ContentFingerprint 函数计算。
	ContentFingerprint string
	// Lyrics 是规范化后的内嵌歌词原文（见 NormalizeLyrics），LyricsSource 是来源键（LyricsSource* 常量）；没有有效歌词时两者都为空。
	// 解析器只提取原文，不解析 LRC。
	Lyrics       string
	LyricsSource string

	originalYears [8]int
	workTags      workTags
}

const (
	maxM4AMetadataAtom    = int64(16 << 20)
	maxM4AMetadataTotal   = int64(32 << 20)
	maxM4ARecursion       = 32
	maxM4AChildren        = 10000
	maxM4AAtoms           = 10000
	maxCueBytes           = int64(8 << 20)
	maxCueDiagnostics     = 32
	maxCueDiagnosticBytes = 512
	maxCueLineBytes       = 64 << 10
	maxCueTokenBytes      = 4096
	maxCueFiles           = 4096
	maxCueTracks          = 10000
	maxM4ARawAtoms        = 64
)

const maxMP3FrameSearchBytes = 64 << 10

func ParseAudioFile(path string) (AudioObservation, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".flac":
		return parseFLAC(path)
	case ".mp3":
		return parseMP3(path)
	case ".ogg":
		return parseOgg(path)
	case ".opus":
		return parseOpus(path)
	case ".wav":
		return parseWAV(path)
	case ".m4a":
		return parseM4A(path)
	case ".ape":
		return parseAPE(path)
	case ".wv":
		return parseWavPack(path)
	case ".aif", ".aiff":
		return parseAIFF(path)
	default:
		return AudioObservation{}, fmt.Errorf("unsupported format")
	}
}

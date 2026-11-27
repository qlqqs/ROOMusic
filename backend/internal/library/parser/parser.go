// Package parser 解析音频文件与 CUE 抄本，只产出安全、有界的事实观察，不读取音乐库之外的位置，
// 也不写入任何文件。标签规则复用 catalog/metadata 与 catalog/classical，DSD 容器事实复用 playback/dsdio。
package parser

import (
	"fmt"
	"io"
	"os"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type AudioObservation struct {
	Format                  string             `json:",omitempty"`
	ScanTags                *metadata.ScanTags `json:",omitempty"`
	Title, Artist, Album    string
	AlbumArtist             string
	Compilation             bool
	TrackNumber, DiscNumber int
	VinylSide               int
	LegacyText              map[string][]byte
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
	Identifiers             []metadata.Identifier
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
	// Lyrics 是规范化后的内嵌歌词原文（见 metadata.NormalizeLyrics），LyricsSource 是来源键（LyricsSource* 常量）；没有有效歌词时两者都为空。
	// 解析器只提取原文，不解析 LRC。
	Lyrics       string
	LyricsSource string

	scanInput       scanTagInput
	latinArtistTags map[int]bool
	originalYears   [8]int
	workTags        workTags
	artworkFront    bool
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
	file, err := os.Open(path)
	if err != nil {
		return AudioObservation{}, err
	}
	defer file.Close()
	return ParseOpenedAudioFile(file, path)
}

// ParseOpenedAudioFile 从已打开的句柄解析容器和标签。name 只用于文件名推断，句柄仍由调用方关闭。
func ParseOpenedAudioFile(file *os.File, name string) (AudioObservation, error) {
	info, err := file.Stat()
	if err != nil {
		return AudioObservation{}, err
	}
	format, err := detectFormat(file, info.Size())
	if err != nil {
		return AudioObservation{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return AudioObservation{}, err
	}
	observation, err := parseAudioFormat(file, format)
	observation.Format = format
	if err == nil {
		observation = applyFilenameFallback(name, observation)
	}
	return observation, err
}

func parseAudioFormat(file *os.File, extension string) (AudioObservation, error) {
	switch extension {
	case ".flac":
		return parseFLAC(file)
	case ".mp3":
		return parseMP3(file)
	case ".ogg":
		return parseOggStream(file, false)
	case ".opus":
		return parseOggStream(file, true)
	case ".wav":
		return parseWAV(file)
	case ".m4a":
		return parseM4A(file)
	case ".ape":
		return parseAPE(file)
	case ".wv":
		return parseWavPack(file)
	case ".aif", ".aiff":
		return parseAIFF(file)
	case ".dsf":
		return parseDSF(file)
	case ".dff":
		return parseDFF(file)
	default:
		return AudioObservation{}, fmt.Errorf("unsupported format")
	}
}

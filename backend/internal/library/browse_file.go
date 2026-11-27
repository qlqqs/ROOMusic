package library

import (
	"context"
	"errors"
	"image"
	"io"
	"path"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

const maxLibraryTextBytes = 64 << 10

var (
	ErrLibraryFileTooLarge = errors.New("library text file exceeds size limit")
	ErrInvalidLibraryFile  = errors.New("library file could not be decoded")
)

// LibraryFileRead 是安全打开文件后返回的类型、大小和可读取内容。
type LibraryFileRead struct {
	Path      string         `json:"path"`
	Type      string         `json:"type"`
	SizeBytes int64          `json:"size_bytes"`
	Encoding  string         `json:"encoding,omitempty"`
	Text      *string        `json:"text,omitempty"`
	Format    string         `json:"format,omitempty"`
	Width     int            `json:"width,omitempty"`
	Height    int            `json:"height,omitempty"`
	Audio     *LibraryAudio  `json:"audio,omitempty"`
	Tags      map[string]any `json:"tags,omitempty"`
}

// LibraryAudio 是已解析的容器参数。
type LibraryAudio struct {
	Codec           string  `json:"codec"`
	DurationSeconds float64 `json:"duration_seconds"`
	SampleRate      int     `json:"sample_rate,omitempty"`
	Channels        int     `json:"channels,omitempty"`
	BitDepth        int     `json:"bit_depth,omitempty"`
	Bitrate         int     `json:"bitrate,omitempty"`
}

// ReadLibraryFile 仅从已通过根内校验的句柄读取内容。
func (s *Service) ReadLibraryFile(ctx context.Context, rootID, relative string) (LibraryFileRead, error) {
	file, size, err := s.openBrowseFile(ctx, rootID, relative)
	if err != nil {
		return LibraryFileRead{}, err
	}
	defer file.Close()

	ext := strings.ToLower(path.Ext(relative))
	out := LibraryFileRead{Path: relative, Type: libraryFileType(ext), SizeBytes: size}
	switch out.Type {
	case "text":
		if size > maxLibraryTextBytes {
			return LibraryFileRead{}, ErrLibraryFileTooLarge
		}
		data, err := io.ReadAll(io.LimitReader(file, maxLibraryTextBytes+1))
		if err != nil {
			return LibraryFileRead{}, err
		}
		if len(data) > maxLibraryTextBytes {
			return LibraryFileRead{}, ErrLibraryFileTooLarge
		}
		value, encoding, err := parser.DecodeText(data)
		if err != nil {
			return LibraryFileRead{}, ErrInvalidLibraryFile
		}
		out.Text, out.Encoding, out.SizeBytes = &value, encoding, int64(len(data))
	case "image":
		config, format, err := image.DecodeConfig(file)
		if err != nil {
			return LibraryFileRead{}, ErrInvalidLibraryFile
		}
		out.Format, out.Width, out.Height = format, config.Width, config.Height
	case "audio", "other":
		if !isScannableAudio(ext) {
			break
		}
		parsed, err := parser.ParseOpenedAudioFile(file, relative)
		if out.Type == "other" && errors.Is(err, parser.ErrUnknownFormat) {
			break
		}
		if err != nil {
			return LibraryFileRead{}, ErrInvalidLibraryFile
		}
		out.Type = "audio"
		out.Format = strings.TrimPrefix(parsed.Format, ".")
		out.Audio = &LibraryAudio{Codec: parsed.Codec, DurationSeconds: parsed.DurationSeconds,
			SampleRate: parsed.SampleRate, Channels: parsed.Channels, BitDepth: parsed.BitDepth, Bitrate: parsed.Bitrate}
		out.Tags = libraryAudioTags(parsed)
	}
	return out, nil
}

func libraryFileType(ext string) string {
	if isSupportedAudioExtension(ext) {
		return "audio"
	}
	switch ext {
	case ".cue", ".log", ".nfo", ".txt", ".m3u", ".m3u8":
		return "text"
	case ".jpeg", ".jpg", ".png", ".gif", ".bmp", ".tif", ".tiff", ".webp":
		return "image"
	default:
		return "other"
	}
}

func libraryAudioTags(o parser.AudioObservation) map[string]any {
	// 只投影用于辨认发行的标签与参数，保持原始标签和歌词留在解析器内部。
	tags := map[string]any{
		"title": o.Title, "artist": o.Artist, "album": o.Album, "album_artist": o.AlbumArtist,
		"compilation": o.Compilation, "track_number": o.TrackNumber, "disc_number": o.DiscNumber,
		"vinyl_side": o.VinylSide, "inferred_fields": o.InferredFields, "year": o.Year, "genres": o.Genres,
		"work": o.Work, "movement_name": o.MovementName, "movement_number": o.MovementNumber,
		"catalog_number": o.Catalog, "isrc": o.ISRC, "identifiers": o.Identifiers,
		"source_type": o.SourceType, "media_type": o.MediaType, "edition": o.Edition, "label": o.Label,
		"barcode": o.Barcode, "credits": o.Credits,
	}
	if s := o.ScanTags; s != nil {
		tags["track_total"], tags["disc_total"] = s.TrackTotal, s.DiscTotal
		tags["release_date"], tags["original_release_date"] = s.ReleaseDate, s.OriginalReleaseDate
		tags["recording_date"], tags["recording_start_date"], tags["recording_end_date"] = s.RecordingDate, s.RecordingStartDate, s.RecordingEndDate
		tags["single"], tags["live"], tags["bootleg"], tags["ep"] = s.Single, s.Live, s.Bootleg, s.EP
		tags["soundtrack"], tags["primary_version"] = s.Soundtrack, s.PrimaryVersion
		tags["track_gain"], tags["album_gain"], tags["track_peak"], tags["album_peak"] = s.TrackGain, s.AlbumGain, s.TrackPeak, s.AlbumPeak
		tags["comments"], tags["podcast"], tags["audiobook"] = s.Comments, s.Podcast, s.Audiobook
	}
	return tags
}

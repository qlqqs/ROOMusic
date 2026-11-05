package ingest

import (
	"strconv"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// SourceObservation 是 scanner 到 organizer 的领域合同，只包含规范化元数据与安全的
// 相对来源标识。CUE offset 等解析器专属值保持为事实，由 organizer 负责归组和选择
// Release 字段。
type SourceObservation struct {
	RelativePath    string            `json:"relative_path"`
	Directory       string            `json:"directory"`
	Title           string            `json:"title,omitempty"`
	Album           string            `json:"album,omitempty"`
	AlbumArtist     string            `json:"album_artist,omitempty"`
	Artist          string            `json:"artist,omitempty"`
	SourceType      string            `json:"source_type,omitempty"`
	MediaType       string            `json:"media_type,omitempty"`
	Genre           string            `json:"genre,omitempty"`
	CatalogNumber   string            `json:"catalog_number,omitempty"`
	Edition         string            `json:"edition,omitempty"`
	Label           string            `json:"label,omitempty"`
	Barcode         string            `json:"barcode,omitempty"`
	Year            int               `json:"year,omitempty"`
	TrackNumber     int               `json:"track_number,omitempty"`
	DiscNumber      int               `json:"disc_number,omitempty"`
	SourceKind      string            `json:"source_kind"`
	InferredFields  map[string]bool   `json:"inferred_fields,omitempty"`
	FieldSources    map[string]string `json:"field_sources,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	Codec           string            `json:"codec,omitempty"`
	BitDepth        int               `json:"bit_depth,omitempty"`
	SampleRate      int               `json:"sample_rate,omitempty"`
	Channels        int               `json:"channels,omitempty"`
	Bitrate         int               `json:"bitrate,omitempty"`

	// CUE 事实只保存安全的相对引用；虚拟轨身份包含 sheet、父来源、轨号与 INDEX 01。
	CueSheetPath          string                       `json:"cue_sheet_path,omitempty"`
	CueParentRelativePath string                       `json:"cue_parent_relative_path,omitempty"`
	CueReferencedFile     string                       `json:"cue_referenced_file,omitempty"`
	CueIndexFrames        int                          `json:"cue_index_frames,omitempty"`
	CueIndexPresent       bool                         `json:"cue_index_present,omitempty"`
	CueEndFrames          int                          `json:"cue_end_frames,omitempty"`
	CueEndPresent         bool                         `json:"cue_end_present,omitempty"`
	CueISRC               string                       `json:"cue_isrc,omitempty"`
	Artwork               []byte                       `json:"artwork,omitempty"`
	ArtworkMIME           string                       `json:"artwork_mime,omitempty"`
	RelatedSourceRefs     []string                     `json:"related_source_refs,omitempty"`
	Credits               []metadata.Credit            `json:"credits,omitempty"`
	ArtistTagEvidence     []metadata.ArtistTagEvidence `json:"artist_tag_evidence,omitempty"`

	// 内嵌歌词的规范化原文与来源键（见 parser.NormalizeLyrics）；没有有效歌词时为空。CUE 虚拟轨没有歌词。
	Lyrics       string `json:"lyrics,omitempty"`
	LyricsSource string `json:"lyrics_source,omitempty"`

	// 以下三个字段不进入暂存的 JSON 载荷，而是写入 scan_observations 的专用列，供身份规划使用：来源文件
	// （CUE 虚拟轨为其父文件）的大小、修改时间与内容指纹。指纹为空表示解析时没有带出，规划阶段会复用已存值
	// 或通过 Fingerprinter 计算；修改时间为零值表示没有取得文件状态，不会复用已存指纹。
	SourceSize         int64     `json:"-"`
	SourceModifiedAt   time.Time `json:"-"`
	ContentFingerprint string    `json:"-"`
}

func (o SourceObservation) fieldObservations() []fieldObservation {
	fields := []fieldObservation{
		{Name: "title", Value: o.Title},
		{Name: "artist", Value: o.Artist},
		{Name: "album", Value: o.Album},
		{Name: "album_artist", Value: o.AlbumArtist},
		{Name: "track_number", Value: strconv.Itoa(o.TrackNumber)},
		{Name: "disc_number", Value: strconv.Itoa(o.DiscNumber)},
	}
	if o.Year > 0 {
		fields = append(fields, fieldObservation{Name: "year", Value: strconv.Itoa(o.Year)})
	}
	for _, optional := range []fieldObservation{{Name: "source_type", Value: o.SourceType}, {Name: "media_type", Value: o.MediaType}, {Name: "genre", Value: o.Genre}, {Name: "catalog_number", Value: o.CatalogNumber}, {Name: "edition", Value: o.Edition}, {Name: "label", Value: o.Label}, {Name: "barcode", Value: o.Barcode}, {Name: "isrc", Value: o.CueISRC}} {
		if optional.Value != "" {
			fields = append(fields, optional)
		}
	}
	for i := range fields {
		fields[i].Inferred = o.InferredFields[fields[i].Name]
		fields[i].SourceKind = o.FieldSources[fields[i].Name]
		if fields[i].SourceKind == "" {
			fields[i].SourceKind = o.SourceKind
		}
		if fields[i].Inferred {
			if o.FieldSources[fields[i].Name] != "" {
				fields[i].SourceKind = o.FieldSources[fields[i].Name]
			} else {
				fields[i].SourceKind = "default_fallback"
			}
		}
	}
	return fields
}

// fieldObservation 是一条字段观察：字段名、值、来源类别与是否推断，写入 track_observations。
type fieldObservation struct {
	Name, Value, SourceKind string
	Inferred                bool
}

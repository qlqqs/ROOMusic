package ingest

import (
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// SourceObservation 是 scanner 到 organizer 的领域合同，只包含规范化元数据与安全的
// 相对来源标识。CUE offset 等解析器专属值保持为事实，由 organizer 负责归组和选择
// Release 字段。
// 来源大小、修改时间与内容指纹写入暂存专用列，不进入 JSON；CUE 使用父音频状态。
// CUE 事实只保存安全的相对引用；虚拟轨没有歌词。ArtworkRelativePath 保留封面原音频来源。
type SourceObservation struct {
	Album                 string                       `json:"album,omitempty"`
	AlbumArtist           string                       `json:"album_artist,omitempty"`
	Artist                string                       `json:"artist,omitempty"`
	ArtistTagEvidence     []metadata.ArtistTagEvidence `json:"artist_tag_evidence,omitempty"`
	Artwork               []byte                       `json:"artwork,omitempty"`
	ArtworkDeferred       bool                         `json:"artwork_deferred,omitempty"`
	ArtworkMIME           string                       `json:"artwork_mime,omitempty"`
	ArtworkRelativePath   string                       `json:"artwork_relative_path,omitempty"`
	Barcode               string                       `json:"barcode,omitempty"`
	BitDepth              int                          `json:"bit_depth,omitempty"`
	Bitrate               int                          `json:"bitrate,omitempty"`
	CatalogNumber         string                       `json:"catalog_number,omitempty"`
	Channels              int                          `json:"channels,omitempty"`
	Codec                 string                       `json:"codec,omitempty"`
	ContentFingerprint    string                       `json:"-"`
	Credits               []metadata.Credit            `json:"credits,omitempty"`
	CueEndFrames          int                          `json:"cue_end_frames,omitempty"`
	CueEndPresent         bool                         `json:"cue_end_present,omitempty"`
	CueISRC               string                       `json:"cue_isrc,omitempty"`
	CueIndexFrames        int                          `json:"cue_index_frames,omitempty"`
	CueIndexPresent       bool                         `json:"cue_index_present,omitempty"`
	CueParentRelativePath string                       `json:"cue_parent_relative_path,omitempty"`
	CueReferencedFile     string                       `json:"cue_referenced_file,omitempty"`
	CueSheetPath          string                       `json:"cue_sheet_path,omitempty"`
	Directory             string                       `json:"directory"`
	DiscNumber            int                          `json:"disc_number,omitempty"`
	// DiscParentExclusive 由暂存层在 CUE scope 对齐之后标记。
	DiscParentExclusive bool              `json:"disc_parent_exclusive,omitempty"`
	DurationSeconds     float64           `json:"duration_seconds,omitempty"`
	Edition             string            `json:"edition,omitempty"`
	FieldSources        map[string]string `json:"field_sources,omitempty"`
	Genres              []string          `json:"genres,omitempty"`
	InferredFields      map[string]bool   `json:"inferred_fields,omitempty"`
	Label               string            `json:"label,omitempty"`
	Lyrics              string            `json:"lyrics,omitempty"`
	LyricsSource        string            `json:"lyrics_source,omitempty"`
	MediaType           string            `json:"media_type,omitempty"`
	ParserVersion       string            `json:"parser_version,omitempty"`
	RelatedSourceRefs   []string          `json:"related_source_refs,omitempty"`
	RelativePath        string            `json:"relative_path"`
	SampleRate          int               `json:"sample_rate,omitempty"`
	SourceKind          string            `json:"source_kind"`
	SourceModifiedAt    time.Time         `json:"-"`
	SourceSize          int64             `json:"-"`
	SourceType          string            `json:"source_type,omitempty"`
	Title               string            `json:"title,omitempty"`
	TrackNumber         int               `json:"track_number,omitempty"`
	Year                int               `json:"year,omitempty"`
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
	for _, optional := range []fieldObservation{{Name: "source_type", Value: o.SourceType}, {Name: "media_type", Value: o.MediaType}, {Name: "genre", Value: strings.Join(o.Genres, "; ")}, {Name: "catalog_number", Value: o.CatalogNumber}, {Name: "edition", Value: o.Edition}, {Name: "label", Value: o.Label}, {Name: "barcode", Value: o.Barcode}, {Name: "isrc", Value: o.CueISRC}} {
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

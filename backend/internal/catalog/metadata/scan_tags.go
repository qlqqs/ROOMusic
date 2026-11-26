package metadata

// ScanDate 保留文件提供的日期精度。
type ScanDate struct {
	Year  int `json:"year"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

type ScanComment struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ScanTags 随曲目保存当前物理文件的标签事实，指针区分缺失值与显式零值。
type ScanTags struct {
	TrackTotal          int                           `json:"track_total,omitempty"`
	DiscTotal           int                           `json:"disc_total,omitempty"`
	ReleaseDate         *ScanDate                     `json:"release_date,omitempty"`
	OriginalReleaseDate *ScanDate                     `json:"original_release_date,omitempty"`
	RecordingDate       *ScanDate                     `json:"recording_date,omitempty"`
	RecordingStartDate  *ScanDate                     `json:"recording_start_date,omitempty"`
	RecordingEndDate    *ScanDate                     `json:"recording_end_date,omitempty"`
	Single              *bool                         `json:"single,omitempty"`
	Live                *bool                         `json:"live,omitempty"`
	Bootleg             *bool                         `json:"bootleg,omitempty"`
	EP                  *bool                         `json:"ep,omitempty"`
	Soundtrack          *bool                         `json:"soundtrack,omitempty"`
	PrimaryVersion      *bool                         `json:"primary_version,omitempty"`
	TrackGain           *float64                      `json:"track_gain,omitempty"`
	AlbumGain           *float64                      `json:"album_gain,omitempty"`
	TrackPeak           *float64                      `json:"track_peak,omitempty"`
	AlbumPeak           *float64                      `json:"album_peak,omitempty"`
	SyncedLyrics        string                        `json:"synced_lyrics,omitempty"`
	Comments            []ScanComment                 `json:"comments,omitempty"`
	Podcast             bool                          `json:"podcast,omitempty"`
	Audiobook           bool                          `json:"audiobook,omitempty"`
	LegacyArtistParts   map[string][]LegacyArtistPart `json:"legacy_artist_parts,omitempty"`
	LegacyEncodings     *[]string                     `json:"legacy_encodings,omitempty"`
}

// LegacyArtistPart 按原始标签次序保存参与同一署名的 Unicode 文本或疑似旧编码字节。
type LegacyArtistPart struct {
	TagKey     string `json:"tag_key,omitempty"`
	Occurrence int    `json:"occurrence,omitempty"`
	Text       string `json:"text,omitempty"`
	Raw        []byte `json:"raw,omitempty"`
}

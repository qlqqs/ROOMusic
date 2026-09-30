package catalog

// 以下类型是 Release 读取投影，JSON 标签是 REST 合同的一部分，由 catalog REST 合同规范约束。

type ReleaseSummary struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Artist         string          `json:"artist"`
	AlbumArtist    *string         `json:"album_artist"`
	Year           *int            `json:"year"`
	SourceType     *string         `json:"source_type"`
	MediaType      *string         `json:"media_type"`
	MediumCount    int64           `json:"medium_count"`
	TrackCount     int64           `json:"track_count"`
	AttentionCount int64           `json:"attention_count"`
	Artwork        *ReleaseArtwork `json:"artwork"`
}

type ReleaseCue struct {
	IndexFrames  *int     `json:"index_frames"`
	EndFrames    *int     `json:"end_frames"`
	StartSeconds *float64 `json:"start_seconds"`
	EndSeconds   *float64 `json:"end_seconds"`
	ISRC         *string  `json:"isrc"`
}

type ReleaseTrack struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Artist          string          `json:"artist"`
	Position        int             `json:"position"`
	Source          string          `json:"source"`
	SourceKind      string          `json:"source_kind"`
	DurationSeconds *float64        `json:"duration_seconds"`
	Codec           *string         `json:"codec"`
	BitDepth        *int            `json:"bit_depth"`
	SampleRate      *int            `json:"sample_rate"`
	Channels        *int            `json:"channels"`
	Bitrate         *int            `json:"bitrate"`
	Cue             *ReleaseCue     `json:"cue"`
	Credits         []ReleaseCredit `json:"credits"`
}

type ReleaseMedium struct {
	ID       string         `json:"id"`
	Position int            `json:"position"`
	Title    string         `json:"title"`
	Tracks   []ReleaseTrack `json:"tracks"`
}

type ReleaseArtwork struct {
	ResourceID string `json:"resource_id"`
	MIME       string `json:"mime"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

type ReleaseCredit struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type ReleaseEvidenceSummary struct {
	Field      string `json:"field"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
	Action     string `json:"action"`
	RuleID     string `json:"rule_id"`
}

type ReleaseDetail struct {
	ReleaseSummary
	CandidateKind string                   `json:"candidate_kind"`
	Genre         *string                  `json:"genre"`
	CatalogNumber *string                  `json:"catalog_number"`
	Edition       *string                  `json:"edition"`
	Label         *string                  `json:"label"`
	Barcode       *string                  `json:"barcode"`
	Media         []ReleaseMedium          `json:"media"`
	Credits       []ReleaseCredit          `json:"credits"`
	Evidence      []ReleaseEvidenceSummary `json:"evidence"`
}

type ReleaseFieldEvidence struct {
	ReleaseEvidenceSummary
	Value      *string  `json:"value"`
	Candidates []string `json:"candidates"`
	ReasonCode *string  `json:"reason_code"`
}

type ReleaseGroupingEvidence struct {
	CandidateKind string   `json:"candidate_kind"`
	RuleID        string   `json:"rule_id"`
	SourceRefs    []string `json:"source_refs"`
	ReasonCodes   []string `json:"reason_codes"`
}

type ReleaseEvidence struct {
	ReleaseID string                   `json:"release_id"`
	Fields    []ReleaseFieldEvidence   `json:"fields"`
	Grouping  *ReleaseGroupingEvidence `json:"grouping"`
	Truncated bool                     `json:"truncated"`
}

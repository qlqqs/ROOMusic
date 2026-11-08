package catalog

import "time"

// 以下类型是 Release 读取投影，JSON 标签是 REST 合同的一部分，由 catalog REST 合同规范约束。

type ReleaseSummary struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Artist          string          `json:"artist"`
	ArtistRefs      []ArtistRef     `json:"artist_refs"`
	AlbumArtistRefs []ArtistRef     `json:"album_artist_refs"`
	AlbumArtist     *string         `json:"album_artist"`
	Year            *int            `json:"year"`
	SourceType      *string         `json:"source_type"`
	MediaType       *string         `json:"media_type"`
	MediumCount     int64           `json:"medium_count"`
	TrackCount      int64           `json:"track_count"`
	AttentionCount  int64           `json:"attention_count"`
	Artwork         *ReleaseArtwork `json:"artwork"`
}

type ReleaseCue struct {
	IndexFrames  *int     `json:"index_frames"`
	EndFrames    *int     `json:"end_frames"`
	StartSeconds *float64 `json:"start_seconds"`
	EndSeconds   *float64 `json:"end_seconds"`
	ISRC         *string  `json:"isrc"`
}

type ReleaseTrackWork struct {
	Index    int     `json:"index"`
	Title    string  `json:"title"`
	Movement *string `json:"movement"`
}

type ReleaseTrack struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Artist          string            `json:"artist"`
	ArtistRefs      []ArtistRef       `json:"artist_refs"`
	Position        int               `json:"position"`
	Source          string            `json:"source"`
	SourceKind      string            `json:"source_kind"`
	DurationSeconds *float64          `json:"duration_seconds"`
	Codec           *string           `json:"codec"`
	BitDepth        *int              `json:"bit_depth"`
	SampleRate      *int              `json:"sample_rate"`
	Channels        *int              `json:"channels"`
	Bitrate         *int              `json:"bitrate"`
	Cue             *ReleaseCue       `json:"cue"`
	Work            *ReleaseTrackWork `json:"work"`
	Credits         []ReleaseCredit   `json:"credits"`
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
	Detail     *string     `json:"detail"`
	Role       string      `json:"role"`
	Name       string      `json:"name"`
	ArtistRefs []ArtistRef `json:"artist_refs"`
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
	Genres        []string                 `json:"genres"`
	CatalogNumber *string                  `json:"catalog_number"`
	Edition       *string                  `json:"edition"`
	Label         *string                  `json:"label"`
	Barcode       *string                  `json:"barcode"`
	Media         []ReleaseMedium          `json:"media"`
	Credits       []ReleaseCredit          `json:"credits"`
	Evidence      []ReleaseEvidenceSummary `json:"evidence"`
	// Revision 是发行的人工修改版本号（修正、归组与撤销共用；扫描从不改它），写操作用它做预期版本。
	// CorrectedFields 是有人工修正的字段名（固定顺序，没有时为空数组），不含修正值的来源与操作者。
	Revision        int64    `json:"revision"`
	CorrectedFields []string `json:"corrected_fields"`
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

// ReleaseGroupSummary 是按组读取的摘要：组 ID、可见成员数、可见成员 attention 之和，以及代表版本的完整摘要（含封面）。
// M3 人工归组之前每组恰好一个版本，代表版本就是它；组内有多个版本时，代表版本按 ReleaseGroupDetail 中的成员顺序排在最前。
type ReleaseGroupSummary struct {
	AddedAt        time.Time      `json:"added_at"`
	ID             string         `json:"id"`
	ReleaseCount   int64          `json:"release_count"`
	AttentionCount int64          `json:"attention_count"`
	Representative ReleaseSummary `json:"representative"`
}

// ReleaseGroupDetail 在组摘要上加成员摘要：按代表版本规则排序，最多 maxReleaseGroupMembers 条；ReleaseCount 仍是完整数量。
// Revision 是专辑的人工修改版本号（默认版本的设定、清除与撤销递增；归组与扫描不改它），写操作用它做预期版本。
// DefaultReleaseID 是生效中的默认版本：没有指定，或指定的版本现在不可见、不在这张专辑里时为 null（专辑按自动规则显示）。
type ReleaseGroupDetail struct {
	ReleaseGroupSummary
	Revision         int64            `json:"revision"`
	DefaultReleaseID *string          `json:"default_release_id"`
	Releases         []ReleaseSummary `json:"releases"`
}

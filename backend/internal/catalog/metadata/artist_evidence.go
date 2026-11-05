package metadata

import (
	"errors"
	"unicode/utf8"
)

// MaxArtistTagEvidenceItems 是每条曲目的原始署名证据上限（跨角色合计）。
const MaxArtistTagEvidenceItems = 100

// ArtistTagEvidence 保存解码后、拆分与规范化之前的原始署名；重复项不能去重。
// OccurrenceIndex 是相同角色、标签键与来源内从零开始的出现次序。
type ArtistTagEvidence struct {
	Role            string `json:"role"`
	TagKey          string `json:"tag_key"`
	RawValue        string `json:"raw_value"`
	SourceKind      string `json:"source_kind"`
	OccurrenceIndex int    `json:"occurrence_index"`
}

// ValidateArtistTagEvidence 在解析及持久化边界拒绝超限证据，不截断原文。
func ValidateArtistTagEvidence(values []ArtistTagEvidence) error {
	if len(values) > MaxArtistTagEvidenceItems {
		return errors.New("artist tag evidence exceeds item limit")
	}
	for _, value := range values {
		if len(value.RawValue) > MaxTextBytes || !utf8.ValidString(value.RawValue) {
			return errors.New("artist tag evidence contains invalid or overlong text")
		}
	}
	return nil
}

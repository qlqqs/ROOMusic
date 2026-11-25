package catalog

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func TestCurationClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*CurationEvidence)
		want  CurationClassification
	}{
		{"完整发行即使缺逐曲艺人和歌词仍完整", func(*CurationEvidence) {}, CurationClassification{Class: LocalComplete}},
		{"标签发行编号优先于信息不足", func(e *CurationEvidence) {
			e.EffectiveTitleAuth, e.EffectiveArtistAuth = metadata.Placeholder, metadata.Placeholder
			e.LocalArtwork, e.EffectiveYear = false, 0
			e.Tracks[0].Identifiers = map[string][]string{"mb_release": {"标签发行一", "冲突的标签发行二"}}
		}, CurationClassification{Class: TaggedID, GapOrder: [4]int{2, 0, 1, 1}}},
		{"信息不足优先于缺口", func(e *CurationEvidence) {
			e.EffectiveTitleAuth, e.EffectiveArtistAuth = metadata.Placeholder, metadata.Placeholder
			e.Tracks[0].EffectiveTitleAuth = metadata.Placeholder
			e.LocalArtwork, e.EffectiveYear = false, 0
			e.Barcodes = []string{"占位条码"}
			e.BarcodeAuthenticity = []metadata.Authenticity{metadata.Placeholder}
		}, CurationClassification{Class: Insufficient, GapOrder: [4]int{2, 1, 1, 1}}},
		{"只有发行组标签仍可查且归缺口", func(e *CurationEvidence) {
			e.EffectiveTitleAuth, e.EffectiveArtistAuth = metadata.Placeholder, metadata.Placeholder
			e.Tracks[0].Identifiers = map[string][]string{"mb_release_group": {"发行组"}}
		}, CurationClassification{Class: HasGaps, GapOrder: [4]int{2, 0, 0, 0}}},
		{"现有外部对应提供线索但不算标签", func(e *CurationEvidence) {
			e.EffectiveTitleAuth, e.EffectiveArtistAuth = metadata.Placeholder, metadata.Placeholder
			e.ExternalID = "已有对应"
		}, CurationClassification{Class: HasGaps, GapOrder: [4]int{2, 0, 0, 0}}},
		{"散放单曲使用曲名艺人与录音编号", func(e *CurationEvidence) {
			e.CandidateKind = "loose_unknown"
			e.Tracks[0].EffectiveTitleAuth = metadata.Inferred
			e.Tracks[0].Identifiers = map[string][]string{"mb_recording": {"标签录音"}}
		}, CurationClassification{Class: TaggedID, GapOrder: [4]int{2, 1, 0, 0}}},
		{"普通专辑的录音编号不使其进入标签类", func(e *CurationEvidence) {
			e.Tracks[0].Identifiers = map[string][]string{"mb_recording": {"标签录音"}}
		}, CurationClassification{Class: LocalComplete}},
		{"排序键按发行文字曲名封面年份排列", func(e *CurationEvidence) {
			e.EffectiveTitleAuth = metadata.Inferred
			e.Tracks[0].EffectiveTitleAuth = metadata.Inferred
			e.Tracks = append(e.Tracks, CurationTrack{EffectiveTitleAuth: metadata.Placeholder})
			e.EffectiveYear = 0
		}, CurationClassification{Class: HasGaps, GapOrder: [4]int{1, 2, 0, 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := CurationEvidence{
				EffectiveTitleAuth: metadata.Real, EffectiveArtistAuth: metadata.Real, EffectiveYear: 2020, LocalArtwork: true,
				Tracks: []CurationTrack{{EffectiveTitleAuth: metadata.Real, EffectiveArtistAuth: metadata.Placeholder}},
			}
			tc.setup(&e)
			if got := e.Classification(); got != tc.want {
				t.Errorf("分类和缺口排序键 %+v，期望 %+v", got, tc.want)
			}
		})
	}
}

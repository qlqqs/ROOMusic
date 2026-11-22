package ingest

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func TestCandidateMetadataSignatureCreditsAndArtwork(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      func(*SourceObservation)
		wantChanged bool
	}{
		{
			name: "仅修改一条署名的detail",
			change: func(observation *SourceObservation) {
				observation.Credits[0].Detail = "piano"
			},
			wantChanged: true,
		},
		{
			name: "仅修改一条署名的source",
			change: func(observation *SourceObservation) {
				observation.Credits[0].Source = "lyrics"
			},
			wantChanged: true,
		},
		{
			name: "仅修改封面字节",
			change: func(observation *SourceObservation) {
				observation.Artwork = []byte{4, 5, 6}
			},
		},
		{
			name: "内嵌封面改为延迟加载",
			change: func(observation *SourceObservation) {
				observation.Artwork = nil
				observation.ArtworkDeferred = true
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := OrganizedCandidate{
				Anchor: CandidateAnchor{Kind: CandidateOrdinary, Scope: "album"},
				Title:  "Album",
				Mediums: map[int][]OrganizedTrack{1: {{
					Disc: 1, Position: 1,
					Observation: SourceObservation{
						RelativePath: "album/01.flac", Directory: "album", Album: "Album",
						SourceKind: "flac_vorbis_comment",
						Credits: []metadata.Credit{
							{Role: "performer", Name: "Artist", Detail: "violin", Source: "tag"},
						},
						Artwork: []byte{1, 2, 3}, ArtworkMIME: "image/jpeg", ArtworkRelativePath: "album/01.flac",
					},
				}}},
			}
			before, err := CandidateMetadataSignature(candidate)
			if err != nil {
				t.Fatalf("计算基准签名失败：%v", err)
			}
			tc.change(&candidate.Mediums[1][0].Observation)
			after, err := CandidateMetadataSignature(candidate)
			if err != nil {
				t.Fatalf("计算修改后的签名失败：%v", err)
			}
			if changed := before != after; changed != tc.wantChanged {
				t.Fatalf("签名是否变化 = %v，期望 %v", changed, tc.wantChanged)
			}
		})
	}
}

func TestCandidateMetadataSignatureIdentifiers(t *testing.T) {
	candidate := OrganizedCandidate{
		Anchor: CandidateAnchor{Kind: CandidateOrdinary, Scope: "album"},
		Title:  "Album",
		Mediums: map[int][]OrganizedTrack{1: {{
			Disc: 1, Position: 1,
			Observation: SourceObservation{
				RelativePath: "album/01.flac", Directory: "album", Album: "Album",
				SourceKind: "flac_vorbis_comment",
			},
		}}},
	}
	signature := func() string {
		t.Helper()
		got, err := CandidateMetadataSignature(candidate)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// 此摘要按 B1 增加 Identifiers 之前的候选 JSON 计算，包含编码器写入的末尾换行。
	const beforeB1 = "2f2849d700d4b1542432ec1cde61be14c87bb9875a93b96c15deaac64162ca38"
	observation := &candidate.Mediums[1][0].Observation
	if got := signature(); got != beforeB1 {
		t.Errorf("nil 标识的签名 = %s，期望旧签名 %s", got, beforeB1)
	}
	observation.Identifiers = []metadata.Identifier{}
	if got := signature(); got != beforeB1 {
		t.Errorf("空标识切片的签名 = %s，期望旧签名 %s", got, beforeB1)
	}
	observation.Identifiers = []metadata.Identifier{{Kind: "isrc", Value: "USX1Z2500001"}}
	first := signature()
	if first == beforeB1 {
		t.Error("加入标识后签名未变")
	}
	observation.Identifiers[0].Value = "USX1Z2500002"
	if got := signature(); got == first || got == beforeB1 {
		t.Errorf("仅更换标识值后签名 = %s，期望新的签名", got)
	}
}

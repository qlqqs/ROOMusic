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
	// 版本 8 的无标识候选摘要包含编码器写入的末尾换行。
	const withoutIdentifiers = "c8bacd2d81684110bd8e947815e545ae598c647b7ee7d0f22e70e6b9f5732ce7"
	observation := &candidate.Mediums[1][0].Observation
	if got := signature(); got != withoutIdentifiers {
		t.Errorf("nil 标识的签名 = %s，期望旧签名 %s", got, withoutIdentifiers)
	}
	observation.Identifiers = []metadata.Identifier{}
	if got := signature(); got != withoutIdentifiers {
		t.Errorf("空标识切片的签名 = %s，期望旧签名 %s", got, withoutIdentifiers)
	}
	observation.Identifiers = []metadata.Identifier{{Kind: "isrc", Value: "USX1Z2500001"}}
	first := signature()
	if first == withoutIdentifiers {
		t.Error("加入标识后签名未变")
	}
	observation.Identifiers[0].Value = "USX1Z2500002"
	if got := signature(); got == first || got == withoutIdentifiers {
		t.Errorf("仅更换标识值后签名 = %s，期望新的签名", got)
	}
}

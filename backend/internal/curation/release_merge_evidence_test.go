package curation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
)

func TestBuildMergeLayoutTrackLimit(t *testing.T) {
	for _, tc := range []struct {
		total   int
		omitted bool
	}{
		{total: 300},
		{total: 301, omitted: true},
	} {
		t.Run(fmt.Sprint(tc.total), func(t *testing.T) {
			plan := governance.ReleaseMergePreview{
				Target: governance.MergeReleaseRef{ID: "target-release"},
				Source: governance.MergeReleaseRef{ID: "source-release"},
				Media: []governance.MergePreviewMedium{
					{FromRelease: "target-release", PositionBefore: 1, PositionAfter: 1, OriginPosition: 1, TrackCount: 150},
					{FromRelease: "source-release", PositionBefore: 1, PositionAfter: 2, OriginPosition: 1, TrackCount: tc.total - 150},
				},
			}
			entries := make(map[string]catalog.CurationEvidence)
			for _, medium := range plan.Media {
				tracks := make([]catalog.CurationTrack, medium.TrackCount)
				for i := range tracks {
					tracks[i] = catalog.CurationTrack{
						Disc:           1,
						Position:       i + 1,
						Title:          "扫描曲名",
						EffectiveTitle: fmt.Sprintf("%s 曲目 %d", medium.FromRelease, i+1),
					}
					if i != 0 {
						tracks[i].ExternalID = fmt.Sprintf("%s 录音 %d", medium.FromRelease, i+1)
					}
				}
				entries[medium.FromRelease] = catalog.CurationEvidence{Tracks: tracks}
			}
			layout := buildMergeLayout(plan, entries)
			raw, err := json.Marshal(layout)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				LayoutTrackTotal int  `json:"layout_track_total"`
				TracksOmitted    bool `json:"tracks_omitted"`
				Layout           []struct {
					Position   int             `json:"position"`
					TrackCount int             `json:"track_count"`
					Tracks     json.RawMessage `json:"tracks"`
				} `json:"layout"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.LayoutTrackTotal != tc.total || payload.TracksOmitted != tc.omitted || len(payload.Layout) != 2 {
				t.Fatalf("总曲目数 %d，省略曲目 %t，碟数 %d", payload.LayoutTrackTotal, payload.TracksOmitted, len(payload.Layout))
			}
			for i, disc := range payload.Layout {
				medium := plan.Media[i]
				if disc.Position != medium.PositionAfter || disc.TrackCount != medium.TrackCount {
					t.Errorf("第 %d 张碟的碟号 %d，曲目数 %d", i+1, disc.Position, disc.TrackCount)
				}
				if tc.omitted {
					if disc.Tracks != nil {
						t.Errorf("第 %d 张碟仍包含 tracks 字段", i+1)
					}
					continue
				}
				var tracks []struct {
					Position   int     `json:"position"`
					Title      string  `json:"title"`
					ExternalID *string `json:"external_id"`
				}
				if err := json.Unmarshal(disc.Tracks, &tracks); err != nil {
					t.Fatal(err)
				}
				if len(tracks) != medium.TrackCount {
					t.Fatalf("第 %d 张碟有 %d 首，期望 %d 首", i+1, len(tracks), medium.TrackCount)
				}
				for j, track := range tracks {
					want := entries[medium.FromRelease].Tracks[j]
					if track.Position != want.Position || track.Title != want.EffectiveTitle {
						t.Errorf("第 %d 张碟第 %d 首的位置 %d，标题 %q", i+1, j+1, track.Position, track.Title)
					}
					if track.ExternalID == nil {
						t.Errorf("第 %d 张碟第 %d 首缺少 external_id", i+1, j+1)
					} else if *track.ExternalID != want.ExternalID {
						t.Errorf("第 %d 张碟第 %d 首的 external_id %q，期望 %q", i+1, j+1, *track.ExternalID, want.ExternalID)
					}
				}
			}

			for range 32 {
				if again := buildMergeLayout(plan, entries); !reflect.DeepEqual(again, layout) {
					t.Fatal("重复构造的布局不同")
				}
			}
		})
	}
}

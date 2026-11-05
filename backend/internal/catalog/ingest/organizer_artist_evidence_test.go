package ingest

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func cueMergeArtistInputs() (SourceObservation, SourceObservation) {
	physical := SourceObservation{
		RelativePath: "album/image.flac", Directory: "album", Album: "Album", Artist: "Physical Artist", AlbumArtist: "Physical Album Artist",
		SourceKind: "flac_vorbis_comment", TrackNumber: 1, DiscNumber: 1,
		ArtistTagEvidence: []metadata.ArtistTagEvidence{
			{Role: "track_artist", TagKey: "ARTIST", RawValue: "Physical Artist", SourceKind: "flac_vorbis_comment"},
			{Role: "album_artist", TagKey: "ALBUMARTIST", RawValue: "Physical Album Artist", SourceKind: "flac_vorbis_comment"},
			{Role: "composer", TagKey: "COMPOSER", RawValue: "Physical Composer", SourceKind: "flac_vorbis_comment"},
		},
	}
	cue := SourceObservation{
		RelativePath: "album/disc.cue#track-1", Directory: "album", Album: "Album", Artist: "Cue Artist", AlbumArtist: "Cue Album Artist",
		SourceKind: "cue_virtual", CueParentRelativePath: physical.RelativePath,
		FieldSources: map[string]string{"artist": "cue_track", "album_artist": "cue_sheet"},
		ArtistTagEvidence: []metadata.ArtistTagEvidence{
			{Role: "track_artist", TagKey: "PERFORMER", RawValue: " Cue Artist ", SourceKind: "cue_track"},
			{Role: "track_artist", TagKey: "PERFORMER", RawValue: " Cue Artist ", SourceKind: "cue_track", OccurrenceIndex: 1},
			{Role: "album_artist", TagKey: "PERFORMER", RawValue: " Cue Album Artist ", SourceKind: "cue_sheet"},
			{Role: "track_artist", TagKey: "PERFORMER", RawValue: "Unselected Sheet Artist", SourceKind: "cue_sheet"},
			{Role: "composer", TagKey: "COMPOSER", RawValue: "Unselected Cue Composer", SourceKind: "cue_track"},
		},
	}
	return physical, cue
}

func TestSingleCueArtistEvidenceFollowsAdoptedFieldSources(t *testing.T) {
	for _, tc := range []struct {
		name                string
		artist, albumArtist bool
	}{
		{"保留物理署名", false, false}, {"只采用曲目艺人", true, false},
		{"只采用专辑艺人", false, true}, {"采用两个字段", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			physical, cue := cueMergeArtistInputs()
			physical.InferredFields = map[string]bool{"artist": tc.artist, "album_artist": tc.albumArtist}
			original := append([]metadata.ArtistTagEvidence(nil), physical.ArtistTagEvidence...)
			got := mergeSingleCueEvidence(physical, []SourceObservation{cue})
			var want []metadata.ArtistTagEvidence
			if !tc.artist {
				want = append(want, original[0])
			}
			if !tc.albumArtist {
				want = append(want, original[1])
			}
			want = append(want, original[2])
			if tc.artist {
				want = append(want, cue.ArtistTagEvidence[:2]...)
			}
			if tc.albumArtist {
				want = append(want, cue.ArtistTagEvidence[2])
			}
			if !reflect.DeepEqual(got.ArtistTagEvidence, want) {
				t.Fatalf("证据没有跟随实际采用来源：got=%+v want=%+v", got.ArtistTagEvidence, want)
			}

			// 去掉新证据的同一输入必须得到完全相同的字段、身份、候选与分组决定。
			withoutPhysical, withoutCue := physical, cue
			withoutPhysical.ArtistTagEvidence, withoutCue.ArtistTagEvidence = nil, nil
			baseline := OrganizeObservations([]SourceObservation{withoutPhysical, withoutCue})
			candidates := OrganizeObservations([]SourceObservation{physical, cue})
			for i := range candidates {
				for disc, tracks := range candidates[i].Mediums {
					for j := range tracks {
						tracks[j].Observation.ArtistTagEvidence = nil
					}
					candidates[i].Mediums[disc] = tracks
				}
			}
			if !reflect.DeepEqual(candidates, baseline) {
				t.Fatal("新证据改变了组织/归组结果")
			}
			if tc.artist || tc.albumArtist {
				got.ArtistTagEvidence[0].RawValue = "changed"
				if !reflect.DeepEqual(physical.ArtistTagEvidence, original) {
					t.Fatal("合并修改了输入证据切片")
				}
			}
		})
	}
}

func TestSingleCueArtistEvidenceUsesExistingDefaultProvenance(t *testing.T) {
	physical, cue := cueMergeArtistInputs()
	physical.Artist, physical.AlbumArtist = "", ""
	cue.FieldSources = nil
	got := mergeSingleCueEvidence(physical, []SourceObservation{cue})
	want := []metadata.ArtistTagEvidence{physical.ArtistTagEvidence[2], cue.ArtistTagEvidence[2], cue.ArtistTagEvidence[3]}
	if !reflect.DeepEqual(got.ArtistTagEvidence, want) || got.FieldSources["artist"] != "cue_sheet" {
		t.Fatalf("未沿用默认来源：%+v", got)
	}
}

func repeatedComposerEvidence(count int) []metadata.ArtistTagEvidence {
	result := make([]metadata.ArtistTagEvidence, count)
	for i := range result {
		result[i] = metadata.ArtistTagEvidence{Role: "composer", TagKey: "COMPOSER", RawValue: "Repeated Composer", SourceKind: "flac_vorbis_comment", OccurrenceIndex: i}
	}
	return result
}

func TestSingleCueArtistEvidencePreservesOverflowForCandidateRejection(t *testing.T) {
	for _, count := range []int{99, 100} {
		physical, cue := cueMergeArtistInputs()
		physical.Artist = ""
		physical.ArtistTagEvidence = repeatedComposerEvidence(count)
		cue.ArtistTagEvidence = cue.ArtistTagEvidence[:1]
		got := mergeSingleCueEvidence(physical, []SourceObservation{cue})
		if len(got.ArtistTagEvidence) != count+1 {
			t.Fatal("合并静默截断或去重了原始证据")
		}
		if err := metadata.ValidateArtistTagEvidence(got.ArtistTagEvidence); (err == nil) != (count == 99) {
			t.Fatalf("合并后预算错误：count=%d err=%v", count+1, err)
		}
	}
}

func TestPostgreSQLSingleCueArtistEvidenceOverflowRollsBackCandidate(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	physical, cue := cueMergeArtistInputs()
	initial := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, physical))
	track := feedbackTracks(t, db, release)[0]

	physical.Artist = ""
	physical.ArtistTagEvidence = repeatedComposerEvidence(100)
	cue.ArtistTagEvidence = cue.ArtistTagEvidence[:1]
	scanID := feedbackRun(t, db)
	candidate := feedbackCandidate(t, db, scanID, root, release, []string{track}, physical, cue)
	if _, err := PersistCandidate(context.Background(), db, scanID, root, candidate); err == nil || !strings.Contains(err.Error(), "artist tag evidence exceeds item limit") {
		t.Fatalf("未将证据溢出传递为候选失败：%v", err)
	}
	var artist string
	if err := db.QueryRow(`SELECT artist FROM tracks WHERE id=$1::uuid`, track).Scan(&artist); err != nil {
		t.Fatal(err)
	}
	if artist != "Physical Artist" {
		t.Fatalf("候选失败仍提交了曲目修改：%q", artist)
	}
	if got := searchFeedbackCount(t, db, `SELECT count(*) FROM artist_tag_evidence WHERE track_id=$1::uuid`, track); got != 3 {
		t.Fatalf("候选失败覆盖旧证据：%d", got)
	}
}

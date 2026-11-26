package ingest

import (
	"maps"
	"testing"
)

func TestScanReadNamesAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, dir, title, titleSource, artist, artistSource, album, albumSource, year string
		tagged                                                                        bool
	}{
		{"filename", "普通专辑", "甲 - 乙", "filename_fallback", "未知艺术家", "display_fallback", "普通专辑", "folder_fallback", "", false},
		{"tagged_artist", "普通专辑", "甲 - 乙", "filename_fallback", "真实艺人", "flac", "普通专辑", "folder_fallback", "", true},
		{"directory_pattern", "目录艺人 - 1999 - 目录专辑", "甲 - 乙", "filename_fallback", "未知艺术家", "display_fallback", "目录专辑", "folder", "1999", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := filenameObservation(tc.dir + "/01 甲 - 乙 [FLAC].flac")
			if tc.tagged {
				o.Artist = "真实艺人"
				delete(o.InferredFields, "artist")
				delete(o.FieldSources, "artist")
			}
			candidates := OrganizeObservations([]SourceObservation{o})
			if len(candidates) != 1 || len(candidates[0].Mediums[1]) != 1 {
				t.Fatalf("归组结果 = %+v", candidates)
			}
			candidate := candidates[0]
			track := candidate.Mediums[1][0].Observation
			if track.Title != tc.title || track.Artist != tc.artist || candidate.Title != tc.album {
				t.Errorf("曲名/曲目艺人/专辑 = %q/%q/%q，期望 %q/%q/%q", track.Title, track.Artist, candidate.Title, tc.title, tc.artist, tc.album)
			}
			gotSources := map[string]string{}
			for _, field := range track.fieldObservations() {
				switch field.Name {
				case "title", "artist", "track_number":
					gotSources[field.Name] = field.SourceKind
				}
			}
			want := map[string]string{"title": tc.titleSource, "artist": tc.artistSource, "track_number": "filename_fallback"}
			if !maps.Equal(gotSources, want) || track.TrackNumber != 1 {
				t.Errorf("曲号/来源 = %d/%v，期望 1/%v", track.TrackNumber, gotSources, want)
			}
			decisions := map[string]FieldDecision{}
			for _, d := range candidate.Decisions {
				decisions[d.Field] = d
			}
			if decisions["title"].Source != tc.albumSource || decisions["year"].Value != tc.year ||
				(tc.year != "" && (decisions["year"].Source != "folder" || candidate.AlbumArtist != "目录艺人")) {
				t.Errorf("专辑名/年份/专辑艺人 = %+v/%+v/%q", decisions["title"], decisions["year"], candidate.AlbumArtist)
			}
		})
	}
	t.Run("classical_work", func(t *testing.T) {
		for _, tc := range []struct {
			genre, work, source string
		}{
			{"古典", "", "title"}, {"古典音乐", "", "title"}, {"古典乐", "", "title"},
			{"新古典", "", ""}, {"古典跨界", "", ""}, {"印度古典", "", ""},
			{"印度古典", "Concerto Op. 17", "tag"},
		} {
			t.Run(tc.genre+"_"+tc.source, func(t *testing.T) {
				songs := []SourceObservation{
					{RelativePath: "album/01.flac", Title: "Concerto Op. 17: I. Allegro", Work: tc.work},
					{RelativePath: "album/02.flac", Title: "Concerto Op. 17: II. Adagio", Work: tc.work},
				}
				candidate := OrganizedCandidate{Genres: []string{tc.genre}, Mediums: map[int][]OrganizedTrack{
					1: {{Observation: songs[0], Disc: 1, Position: 1}, {Observation: songs[1], Disc: 1, Position: 2}},
				}}
				works := AssignWorks(candidate)
				if tc.source == "" {
					if len(works) != 0 {
						t.Errorf("%s 误推断作品：%+v", tc.genre, works)
					}
					return
				}
				for _, song := range songs {
					if w := works[song.RelativePath]; w.Title != "Concerto Op. 17" || w.Source != tc.source || w.Index != 1 {
						t.Errorf("%s 的作品 = %+v，期望整组两轨，来源 %s", song.Title, w, tc.source)
					}
				}
			})
		}
	})
}

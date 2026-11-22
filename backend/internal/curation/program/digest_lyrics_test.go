package program

import (
	"encoding/hex"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"testing"
)

func TestSubjectDigests(t *testing.T) {
	all := func(e catalog.CurationEvidence) [3]string {
		return [3]string{ReleaseDigest(e), ArtworkDigest(e), LyricsDigest(e, e.Tracks[0])}
	}
	original, _ := pairedAlbum(1)
	base := all(original)
	for _, sum := range base {
		b, err := hex.DecodeString(sum)
		if err != nil || len(b) != 32 {
			t.Fatal(sum)
		}
	}
	for _, tc := range []struct {
		name            string
		artwork, lyrics bool
		change          func(*catalog.CurationEvidence)
	}{
		{"identity", false, false, func(e *catalog.CurationEvidence) { e.ID = "other" }},
		{"group", false, false, func(e *catalog.CurationEvidence) { e.GroupID = "other" }},
		{"kind", false, false, func(e *catalog.CurationEvidence) { e.CandidateKind = "loose_unknown" }},
		{"title", false, true, func(e *catalog.CurationEvidence) { e.Title = "新" }},
		{"artist", false, false, func(e *catalog.CurationEvidence) { e.Artist = "新" }},
		{"album_artist", false, false, func(e *catalog.CurationEvidence) { e.AlbumArtist = "新" }},
		{"title_placeholder", false, true, func(e *catalog.CurationEvidence) { e.TitlePlaceholder = true }},
		{"artist_placeholder", false, false, func(e *catalog.CurationEvidence) { e.ArtistPlaceholder = true }},
		{"year", false, false, func(e *catalog.CurationEvidence) { e.Year = 2020 }},
		{"genre", false, false, func(e *catalog.CurationEvidence) { e.Genres = []string{"新"} }},
		{"edition", false, false, func(e *catalog.CurationEvidence) { e.Edition = "新" }},
		{"label", false, false, func(e *catalog.CurationEvidence) { e.Label = "新" }},
		{"barcode", false, false, func(e *catalog.CurationEvidence) { e.Barcode = "123" }},
		{"barcodes", false, false, func(e *catalog.CurationEvidence) { e.Barcodes = []string{"22"} }},
		{"catalog", false, false, func(e *catalog.CurationEvidence) { e.CatalogNumber = "新" }},
		{"mcn", false, false, func(e *catalog.CurationEvidence) { e.CatalogNumberIsMCN = true }},
		{"format", false, false, func(e *catalog.CurationEvidence) { e.MediaType = "CD" }},
		{"corrected", false, false, func(e *catalog.CurationEvidence) { e.Corrected = map[string]bool{"title": true} }},
		{"automatic", false, false, func(e *catalog.CurationEvidence) { e.Automatic = map[string]bool{"label": true} }},
		{"release_link", true, false, func(e *catalog.CurationEvidence) { e.ExternalID = "new" }},
		{"remote_group", true, false, func(e *catalog.CurationEvidence) { e.ExternalGroupID = "X" }},
		{"group_link", false, false, func(e *catalog.CurationEvidence) { e.GroupExternalID = "X" }},
		{"group_origin", false, false, func(e *catalog.CurationEvidence) { e.GroupChangeSource = "rest" }},
		{"media", false, false, func(e *catalog.CurationEvidence) { e.Media = append(e.Media, 2) }},
		{"track_added", false, false, func(e *catalog.CurationEvidence) { e.Tracks = append(e.Tracks, catalog.CurationTrack{ID: "two"}) }},
		{"track_title", false, true, func(e *catalog.CurationEvidence) { e.Tracks[0].Title = "新" }},
		{"track_artist", false, true, func(e *catalog.CurationEvidence) { e.Tracks[0].Artist = "新" }},
		{"duration", false, true, func(e *catalog.CurationEvidence) { e.Tracks[0].DurationSeconds++ }},
		{"disc", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].Disc++ }},
		{"position", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].Position++ }},
		{"inferred", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].PositionInferred = true }},
		{"identifier", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].Identifiers["mb_recording"] = []string{"new"} }},
		{"cue", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].CueSheetPath = "a.cue" }},
		{"cue_index", false, false, func(e *catalog.CurationEvidence) { v := 15; e.Tracks[0].CueIndexFrames = &v }},
		{"cue_end", false, false, func(e *catalog.CurationEvidence) { v := 55; e.Tracks[0].CueEndFrames = &v }},
		{"cue_parent", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].ParentFile = "a.flac" }},
		{"cue_isrc", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].CueISRC = "new" }},
		{"track_link", false, false, func(e *catalog.CurationEvidence) { e.Tracks[0].ExternalID = "new" }},
		{"local_cover", true, false, func(e *catalog.CurationEvidence) { e.LocalArtwork = true }},
		{"external_cover", true, false, func(e *catalog.CurationEvidence) { e.ExternalArtwork = true }},
		{"cover_file", true, false, func(e *catalog.CurationEvidence) { e.ArtworkFile = true }},
		{"local_lyrics", false, true, func(e *catalog.CurationEvidence) { e.Tracks[0].LocalLyrics = true }},
		{"external_lyrics", false, true, func(e *catalog.CurationEvidence) { e.Tracks[0].ExternalLyrics = true }},
		{"release_marker", false, false, func(e *catalog.CurationEvidence) {
			e.Suppressions = []catalog.CurationSuppression{{TargetType: "release", TargetID: e.ID, Subject: "external_link", ValueHash: "a"}}
		}},
		{"cover_marker", true, false, func(e *catalog.CurationEvidence) {
			e.Suppressions = []catalog.CurationSuppression{{TargetType: "release", TargetID: e.ID, Subject: "artwork_source", ValueHash: "a"}}
		}},
		{"lyrics_marker", false, true, func(e *catalog.CurationEvidence) {
			e.Suppressions = []catalog.CurationSuppression{{TargetType: "track", TargetID: e.Tracks[0].ID, Subject: "lyrics_source", ValueHash: "a"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := pairedAlbum(1)
			if all(e) != base {
				t.Fatal("相同输入的摘要变化")
			}
			tc.change(&e)
			updated := all(e)
			for i, wantChanged := range []bool{true, tc.artwork, tc.lyrics} {
				if (updated[i] != base[i]) != wantChanged {
					t.Errorf("主题 %d 摘要变化期望 %v", i, wantChanged)
				}
			}
		})
	}
}

func TestLyricsAcceptance(t *testing.T) {
	track := catalog.CurationTrack{Title: "臺灣Ａ！", Artist: "樂團 feat. Guest", DurationSeconds: 100}
	base := sources.Lyrics{ID: "1", Title: "台湾A", Artist: "乐团 & 合作人", Plain: "第一行", DurationSeconds: 100}
	for _, name := range []string{"normalized", "minus_two", "plus_two", "over", "under", "unknown", "wrong_title", "wrong_artist", "instrumental", "empty", "different", "deduplicated_synced"} {
		t.Run(name, func(t *testing.T) {
			item := base
			more := []sources.Lyrics{}
			status := "done"
			id := "1"
			switch name {
			case "minus_two":
				item.DurationSeconds = 98
			case "plus_two":
				item.DurationSeconds = 102
			case "over":
				item.DurationSeconds = 102.01
				status = "no_match"
			case "under":
				item.DurationSeconds = 97.99
				status = "no_match"
			case "unknown":
				item.DurationSeconds = 0
				status = "no_match"
			case "wrong_title":
				item.Title = "其他"
				status = "no_match"
			case "wrong_artist":
				item.Artist = "其他"
				status = "no_match"
			case "instrumental":
				item.Instrumental = true
				status = "no_match"
			case "empty":
				item.Plain = " "
				status = "no_match"
			case "different":
				other := item
				other.ID = "2"
				other.Plain = "另一份"
				more = append(more, other)
				status = "uncertain"
			case "deduplicated_synced":
				other := item
				other.ID = "2"
				other.Synced = "[00:01.00]第一行"
				more = append(more, other)
				id = "2"
			}
			choice, got := chooseLyrics(track, append([]sources.Lyrics{item}, more...))
			if got != status || (got == "done" && choice.id != id) || (got != "done" && choice.id != "") {
				t.Fatalf("得到 %s %q，期望 %s %q", got, choice.id, status, id)
			}
		})
	}
	for _, sep := range []string{" feat. ", " ft. ", " & ", "/", "、", ",", ";", "×"} {
		if !lyricsArtistMatches("王"+sep+"甲", "王"+sep+"乙") {
			t.Errorf("首位艺人分隔符 %q", sep)
		}
	}
}

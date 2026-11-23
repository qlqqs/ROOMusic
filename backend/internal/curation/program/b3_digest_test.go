package program

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestB3DigestBaseline(t *testing.T) {
	evidence, _ := pairedAlbum(2)
	evidence.ExternalID = "release-id"
	evidence.ArtworkFile = true
	evidence.Tracks[0].Artist = "独奏"
	evidence.Tracks[0].EffectiveTitle = evidence.Tracks[0].Title
	evidence.Tracks[0].EffectiveArtist = evidence.Tracks[0].Artist
	evidence.Tracks[0].Identifiers["isrc"] = []string{"USABC2300001"}
	evidence.Suppressions = []catalog.CurationSuppression{
		{TargetType: "release", TargetID: evidence.ID, Subject: "artwork_source", ValueHash: "cover"},
		{TargetType: "track", TargetID: evidence.Tracks[0].ID, Subject: "lyrics_source", ValueHash: "lyric"},
	}
	// 基准来自 main 的 97174a96，使用同一份证据。
	for _, tc := range []struct{ subject, got, want string }{
		{"release", ReleaseDigest(evidence), "1e7aa4fd3049550dac9a871f5632550795058ae6b0fec5171d9787b1425af014"},
		{"artwork", ArtworkDigest(evidence), "9024352c70a05458139a60bef331bd890cca97f611ffa6794dfa03c5b328bff3"},
		{"lyrics", LyricsDigest(evidence, evidence.Tracks[0]), "e2cbf2cd1292ed78dcc08b100e1fc63e2ff9e933926f50b26686f47daf40cd10"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s 摘要=%s，期望 %s", tc.subject, tc.got, tc.want)
		}
	}
}

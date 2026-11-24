package curation

import (
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

func TestParseArtistLink(t *testing.T) {
	const mbid = "abcdef12-0000-4000-8000-000000000001"
	for _, tc := range []struct{ name, input, source, id string }{
		{"mbid", " " + strings.ToUpper(mbid) + "\n", "musicbrainz", mbid},
		{"musicbrainz_url", "https://beta.musicbrainz.org/artist/" + mbid + "/relationships?tab=aliases", "musicbrainz", mbid},
		{"qid", "Q123456789012", "wikidata", "Q123456789012"},
		{"wikidata_url", "www.wikidata.org/wiki/Q42", "wikidata", "Q42"},
		{"wikidata_entity_url", "https://www.wikidata.org/entity/Q42", "wikidata", "Q42"},
		{"qid_too_long", "Q1234567890123", "", ""},
		{"qid_too_long_in_url", "https://www.wikidata.org/wiki/Q1234567890123", "", ""},
		{"leading_zero", "Q042", "", ""},
		{"incorrect_entity", "https://musicbrainz.org/release/" + mbid, "", ""},
		{"incorrect_host", "https://musicbrainz.org.example.test/artist/" + mbid, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArtistLink(tc.input)
			if tc.source == "" {
				if status, code, _ := operations.Problem(err); err == nil || status != 400 || code != "invalid_input" {
					t.Fatalf("parseArtistLink(%q) = %+v, %v，期望 400 invalid_input", tc.input, got, err)
				}
				return
			}
			if want := (externalIdentity{Source: tc.source, ExternalID: tc.id}); err != nil || got != want {
				t.Fatalf("parseArtistLink(%q) = %+v, %v，期望 %+v", tc.input, got, err, want)
			}
		})
	}
}

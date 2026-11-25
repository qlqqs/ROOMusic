package protocol_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func TestSubsonicBrowseWireEncoding(t *testing.T) {
	gain := 0.0
	const root = `<subsonic-response xmlns="http://subsonic.org/restapi" status="ok" version="1.16.1" type="roomusic" serverVersion="fixture" openSubsonic="true">`
	const jsonRoot = `{"subsonic-response":{"status":"ok","version":"1.16.1","type":"roomusic","serverVersion":"fixture","openSubsonic":true,`
	for _, tc := range []struct {
		name, xmlBody, jsonBody string
		response                protocol.Response
	}{
		{
			name: "NestedAlbum",
			response: protocol.Response{Album: &protocol.Album{
				ID: "album", Name: "Two Discs", Artist: "Atlas", ArtistID: "artist", DisplayArtist: "Atlas",
				SongCount: 1, Duration: 10, Created: "2026-11-25T12:00:00Z", DiscTitles: []protocol.DiscTitle{{Disc: 2, Title: "Finale"}},
				Songs: []protocol.Song{{
					ID: "song", Parent: "album", AlbumID: "album", Album: "Two Discs", Title: "A & B", Artist: "Atlas",
					Duration: 10, DiscNumber: 2, Type: "music", MediaType: "song", DisplayArtist: "Atlas", DisplayAlbumArtist: "Atlas",
					Artists: []protocol.NamedArtist{{ID: "artist", Name: "Atlas"}}, AlbumArtists: []protocol.NamedArtist{{ID: "artist", Name: "Atlas"}},
					Genre: "Jazz", Genres: []protocol.ItemGenre{{Name: "Jazz"}}, ReplayGain: &protocol.ReplayGain{TrackGain: &gain},
				}},
			}},
			xmlBody: `<album id="album" name="Two Discs" artist="Atlas" artistId="artist" displayArtist="Atlas" songCount="1" duration="10" created="2026-11-25T12:00:00Z">
				<discTitles disc="2" title="Finale"></discTitles>
				<song id="song" parent="album" isDir="false" title="A &amp; B" album="Two Discs" artist="Atlas" genre="Jazz" duration="10"
					discNumber="2" albumId="album" isVideo="false" type="music" mediaType="song" displayArtist="Atlas" displayAlbumArtist="Atlas">
					<artists id="artist" name="Atlas"></artists><albumArtists id="artist" name="Atlas"></albumArtists>
					<genres name="Jazz"></genres><replayGain trackGain="0"></replayGain>
				</song></album>`,
			jsonBody: `"album":{"id":"album","name":"Two Discs","artist":"Atlas","artistId":"artist","displayArtist":"Atlas",
				"songCount":1,"duration":10,"created":"2026-11-25T12:00:00Z","discTitles":[{"disc":2,"title":"Finale"}],
				"song":[{"id":"song","parent":"album","isDir":false,"title":"A & B","album":"Two Discs","artist":"Atlas","genre":"Jazz","duration":10,
				"discNumber":2,"albumId":"album","isVideo":false,"type":"music","mediaType":"song","displayArtist":"Atlas","displayAlbumArtist":"Atlas",
				"artists":[{"id":"artist","name":"Atlas"}],"albumArtists":[{"id":"artist","name":"Atlas"}],"genres":[{"name":"Jazz"}],"replayGain":{"trackGain":0}}]}`,
		},
		{
			name: "ArtistIndex",
			response: protocol.Response{Artists: &protocol.Artists{Indexes: []protocol.ArtistIndex{
				{Name: "A", Artists: []protocol.Artist{{ID: "artist", Name: "Atlas", AlbumCount: 2, CoverArt: "ar-artist"}}},
			}}},
			xmlBody:  `<artists ignoredArticles=""><index name="A"><artist id="artist" name="Atlas" albumCount="2" coverArt="ar-artist"></artist></index></artists>`,
			jsonBody: `"artists":{"ignoredArticles":"","index":[{"name":"A","artist":[{"id":"artist","name":"Atlas","albumCount":2,"coverArt":"ar-artist"}]}]}`,
		},
		{
			name:     "GenreTextAndCounts",
			response: protocol.Response{Genres: &protocol.Genres{Genres: []protocol.Genre{{Name: "Jazz & Blues", AlbumCount: 2, SongCount: 5}}}},
			xmlBody:  `<genres><genre albumCount="2" songCount="5">Jazz &amp; Blues</genre></genres>`,
			jsonBody: `"genres":{"genre":[{"value":"Jazz & Blues","albumCount":2,"songCount":5}]}`,
		},
		{
			name:     "EmptyPages",
			response: protocol.Response{SearchResult3: &protocol.SearchResult{Artists: []protocol.Artist{}, Albums: []protocol.Album{}, Songs: []protocol.Song{}}},
			xmlBody:  `<searchResult3></searchResult3>`,
			jsonBody: `"searchResult3":{"artist":[],"album":[],"song":[]}`,
		},
		{
			name:     "EmptyInfo",
			response: protocol.Response{AlbumInfo: &protocol.AlbumInfo{}},
			xmlBody:  `<albumInfo></albumInfo>`,
			jsonBody: `"albumInfo":{}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotXML := encodeSubsonicResponse(t, "xml", "fixture", http.StatusOK, "application/xml; charset=utf-8", tc.response)
			wantXML := root + tc.xmlBody + "</subsonic-response>"
			if !reflect.DeepEqual(subsonicXMLTokens(t, gotXML), subsonicXMLTokens(t, wantXML)) {
				t.Errorf("XML = %s，期望 %s", gotXML, wantXML)
			}
			gotJSON := encodeSubsonicResponse(t, "json", "fixture", http.StatusOK, "application/json", tc.response)
			wantJSON := jsonRoot + tc.jsonBody + "}}"
			var got, want any
			if err := json.Unmarshal([]byte(gotJSON), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("JSON = %s，期望 %s", gotJSON, wantJSON)
			}
		})
	}
}

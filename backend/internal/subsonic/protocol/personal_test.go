package protocol_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func TestSubsonicPersonalWireEncoding(t *testing.T) {
	const created = "2026-11-24T10:00:00Z"
	const changed = "2026-11-25T12:00:00Z"
	song := protocol.Song{
		ID: "track", Parent: "album", Title: "合成曲目", Album: "合成专辑", Artist: "合成艺人",
		Duration: 90, DiscNumber: 1, AlbumID: "album", ArtistID: "artist", Type: "music", MediaType: "song",
		DisplayArtist: "合成艺人", DisplayAlbumArtist: "合成艺人", Starred: changed,
	}
	const songAttrs = `id="track" parent="album" isDir="false" title="合成曲目" album="合成专辑" artist="合成艺人" duration="90" discNumber="1"
		albumId="album" artistId="artist" isVideo="false" type="music" mediaType="song" displayArtist="合成艺人" displayAlbumArtist="合成艺人"
		starred="2026-11-25T12:00:00Z"`
	const songJSON = `{"id":"track","parent":"album","isDir":false,"title":"合成曲目","album":"合成专辑","artist":"合成艺人",
		"duration":90,"discNumber":1,"albumId":"album","artistId":"artist","isVideo":false,"type":"music","mediaType":"song",
		"displayArtist":"合成艺人","displayAlbumArtist":"合成艺人","starred":"2026-11-25T12:00:00Z"}`
	list := protocol.Playlist{
		ID: "list", Name: "A & B", Owner: "member", SongCount: 1, Duration: 90,
		Created: created, Changed: changed, CoverArt: "pl-list",
	}
	detail := list
	detail.Entries = []protocol.Song{song}
	const playlistAttrs = `id="list" name="A &amp; B" owner="member" public="false" songCount="1" duration="90"
		created="2026-11-24T10:00:00Z" changed="2026-11-25T12:00:00Z" coverArt="pl-list"`
	const playlistJSON = `"id":"list","name":"A & B","owner":"member","public":false,"songCount":1,"duration":90,
		"created":"2026-11-24T10:00:00Z","changed":"2026-11-25T12:00:00Z","coverArt":"pl-list"`
	zero, later := int64(0), int64(12345)
	for _, tc := range []struct {
		name, xmlBody, jsonBody string
		response                protocol.Response
	}{
		{
			name: "Starred",
			response: protocol.Response{Starred2: &protocol.SearchResult{
				Artists: []protocol.Artist{{ID: "artist", Name: "合成艺人", AlbumCount: 1, Starred: changed}},
				Albums:  []protocol.Album{{ID: "album", Name: "合成专辑", Artist: "合成艺人", ArtistID: "artist", DisplayArtist: "合成艺人", SongCount: 1, Duration: 90, Created: created, Starred: changed}},
				Songs:   []protocol.Song{song},
			}},
			xmlBody: `<starred2><artist id="artist" name="合成艺人" albumCount="1" starred="2026-11-25T12:00:00Z"></artist>
				<album id="album" name="合成专辑" artist="合成艺人" artistId="artist" songCount="1" duration="90"
					created="2026-11-24T10:00:00Z" starred="2026-11-25T12:00:00Z" displayArtist="合成艺人"></album>
				<song ` + songAttrs + `></song></starred2>`,
			jsonBody: `"starred2":{"artist":[{"id":"artist","name":"合成艺人","albumCount":1,"starred":"2026-11-25T12:00:00Z"}],
				"album":[{"id":"album","name":"合成专辑","artist":"合成艺人","artistId":"artist","songCount":1,"duration":90,
				"created":"2026-11-24T10:00:00Z","starred":"2026-11-25T12:00:00Z","displayArtist":"合成艺人"}],"song":[` + songJSON + `]}`,
		},
		{
			name:     "EmptyStarred",
			response: protocol.Response{Starred2: &protocol.SearchResult{Artists: []protocol.Artist{}, Albums: []protocol.Album{}, Songs: []protocol.Song{}}},
			xmlBody:  `<starred2></starred2>`,
			jsonBody: `"starred2":{"artist":[],"album":[],"song":[]}`,
		},
		{
			name:     "PlaylistList",
			response: protocol.Response{Playlists: &protocol.Playlists{Items: []protocol.Playlist{list}}},
			xmlBody:  `<playlists><playlist ` + playlistAttrs + `></playlist></playlists>`,
			jsonBody: `"playlists":{"playlist":[{` + playlistJSON + `}]}`,
		},
		{
			name:     "PlaylistDetail",
			response: protocol.Response{Playlist: &detail},
			xmlBody:  `<playlist ` + playlistAttrs + `><entry ` + songAttrs + `></entry></playlist>`,
			jsonBody: `"playlist":{` + playlistJSON + `,"entry":[` + songJSON + `]}`,
		},
		{
			name:     "EmptyPlaylistList",
			response: protocol.Response{Playlists: &protocol.Playlists{Items: []protocol.Playlist{}}},
			xmlBody:  `<playlists></playlists>`,
			jsonBody: `"playlists":{"playlist":[]}`,
		},
		{
			name:     "LegacyLyrics",
			response: protocol.Response{Lyrics: &protocol.Lyrics{Artist: "合成艺人", Title: "A & B", Value: "第一行\nno <tag>"}},
			xmlBody:  "<lyrics artist=\"合成艺人\" title=\"A &amp; B\">第一行\nno &lt;tag&gt;</lyrics>",
			jsonBody: `"lyrics":{"artist":"合成艺人","title":"A & B","value":"第一行\nno <tag>"}`,
		},
		{
			name: "StructuredLyrics",
			response: protocol.Response{LyricsList: &protocol.LyricsList{Items: []protocol.StructuredLyrics{
				{Language: "eng", Synced: true, Lines: []protocol.LyricsLine{{Start: &zero, Value: "opening"}, {Start: &later, Value: "A & B"}}},
				{Language: "und", Lines: []protocol.LyricsLine{{Value: "plain"}, {Value: ""}}},
			}}},
			xmlBody: `<lyricsList><structuredLyrics lang="eng" synced="true"><line start="0">opening</line><line start="12345">A &amp; B</line></structuredLyrics>
				<structuredLyrics lang="und" synced="false"><line>plain</line><line></line></structuredLyrics></lyricsList>`,
			jsonBody: `"lyricsList":{"structuredLyrics":[{"lang":"eng","synced":true,"line":[{"start":0,"value":"opening"},{"start":12345,"value":"A & B"}]},
				{"lang":"und","synced":false,"line":[{"value":"plain"},{"value":""}]}]}`,
		},
		{
			name:     "NoLyrics",
			response: protocol.Response{LyricsList: &protocol.LyricsList{Items: []protocol.StructuredLyrics{}}},
			xmlBody:  `<lyricsList></lyricsList>`,
			jsonBody: `"lyricsList":{"structuredLyrics":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const xmlRoot = `<subsonic-response xmlns="http://subsonic.org/restapi" status="ok" version="1.16.1" type="roomusic" serverVersion="fixture" openSubsonic="true">`
			gotXML := encodeSubsonicResponse(t, "xml", "fixture", http.StatusOK, "application/xml; charset=utf-8", tc.response)
			wantXML := xmlRoot + tc.xmlBody + "</subsonic-response>"
			if !reflect.DeepEqual(subsonicXMLTokens(t, gotXML), subsonicXMLTokens(t, wantXML)) {
				t.Errorf("XML = %s，期望 %s", gotXML, wantXML)
			}
			const jsonRoot = `{"subsonic-response":{"status":"ok","version":"1.16.1","type":"roomusic","serverVersion":"fixture","openSubsonic":true,`
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

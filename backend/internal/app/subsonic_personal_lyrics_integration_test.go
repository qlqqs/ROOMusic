package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func TestPostgreSQLSubsonicPersonalLyrics(t *testing.T) {
	app := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, app)
	f := &subsonicBrowseFixture{app: app}
	root := filepath.Join(app.allowedRoot, "subsonic-lyrics")
	fixtures := []struct {
		title, artist, raw string
		id                 string
	}{
		{title: "Shared Refrain", artist: "Cedar Band", raw: "[lang:en]\n[00:00.000]Cedar & sea\n[00:12.345]Second line"},
		{title: "Shared Refrain", artist: "Cedar Band Live", raw: "[lang:???]\nAn unrelated first line\n\nA second paragraph"},
		{title: "Quiet Ending", artist: "Cedar Band", raw: ""},
	}
	for i, item := range fixtures {
		album := "Lyrics by " + item.artist
		if item.raw == "" {
			album = "Instrumentals"
		}
		writeSyntheticTrack(t, filepath.Join(root, album, "01.flac"), byte(i+1),
			"TITLE="+item.title, "ALBUM="+album, "ARTIST="+item.artist, "ALBUMARTIST="+item.artist)
	}
	createRoot(t, app, admin, root, uuid.New())
	runScan(t, app, &admin)
	if err := app.app.WaitForScans(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := range fixtures {
		item := &fixtures[i]
		err := app.database.QueryRowContext(t.Context(), `SELECT id::text FROM tracks WHERE title=$1 AND artist=$2 AND source_status='present'`,
			item.title, item.artist).Scan(&item.id)
		if err != nil {
			t.Fatalf("读取合成曲目 %q / %q：%v", item.title, item.artist, err)
		}
		if item.raw != "" {
			mustExec(t, app, `UPDATE tracks SET lyrics=$1,lyrics_source='vorbis_lyrics' WHERE id=$2::uuid`, item.raw, item.id)
		}
	}

	start, second := int64(0), int64(12345)
	want := [][]protocol.StructuredLyrics{
		{{Language: "eng", Synced: true, Lines: []protocol.LyricsLine{{Start: &start, Value: "Cedar & sea"}, {Start: &second, Value: "Second line"}}}},
		{{Language: "und", Lines: []protocol.LyricsLine{{Value: "An unrelated first line"}, {Value: ""}, {Value: "A second paragraph"}}}},
		{},
	}
	for _, format := range []string{"json", "xml"} {
		t.Run(format, func(t *testing.T) {
			for i, item := range fixtures {
				list := subsonicBrowseResult[protocol.LyricsList](t, f, format, "getLyricsBySongId", "lyricsList", "id", item.id)
				if i == 2 && format == "xml" {
					if len(list.Items) != 0 {
						t.Fatalf("无歌词的 XML lyricsList 包含歌词：%+v", list)
					}
				} else if !reflect.DeepEqual(list.Items, want[i]) {
					t.Fatalf("%s / %s 的 lyricsList 为 %+v，期望 %+v", item.artist, item.title, list.Items, want[i])
				}
			}
			for _, tc := range []struct {
				artist, title string
				want          protocol.Lyrics
			}{
				{"  cedar band  ", "shared refrain", protocol.Lyrics{Artist: fixtures[0].artist, Title: fixtures[0].title, Value: "Cedar & sea\nSecond line"}},
				{fixtures[1].artist, fixtures[1].title, protocol.Lyrics{Artist: fixtures[1].artist, Title: fixtures[1].title, Value: "An unrelated first line\n\nA second paragraph"}},
				{"Cedar", fixtures[0].title, protocol.Lyrics{}},
				{fixtures[0].artist, "Shared", protocol.Lyrics{}},
				{fixtures[2].artist, fixtures[2].title, protocol.Lyrics{}},
			} {
				got := subsonicBrowseResult[protocol.Lyrics](t, f, format, "getLyrics", "lyrics", "artist", tc.artist, "title", tc.title)
				if got != tc.want {
					t.Fatalf("getLyrics(%q, %q) 为 %+v，期望 %+v", tc.artist, tc.title, got, tc.want)
				}
			}
		})
	}
}

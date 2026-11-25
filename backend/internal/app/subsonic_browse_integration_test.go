package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

const (
	subsonicZebra = "Zebra Record"
	subsonicMaple = "Maple Record"
	subsonicBlue  = "Blue Record"
	subsonicCue   = "Amber Cues"
	subsonicAlt   = "Zebra Deluxe"
)

type subsonicBrowseRelease struct {
	release, group string
}

type subsonicBrowseFixture struct {
	app     *integrationTestApplication
	admin   integrationTestUser
	albums  map[string]subsonicBrowseRelease
	tracks  map[string]string
	artists map[string]string
}

func newSubsonicBrowseFixture(t *testing.T) *subsonicBrowseFixture {
	t.Helper()
	app := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, app)
	setSubsonicEnabled(t, app, &admin, true)
	root := filepath.Join(app.allowedRoot, "subsonic-browse")
	for i, track := range []struct {
		dir, title, album, artist, albumArtist, genre string
		year, disc, position                          int
	}{
		{"zebra", "Overture", subsonicZebra, "Atlas Collective", "Atlas Collective", "Rock", 1998, 1, 1},
		{"zebra", "Duet & Guests", subsonicZebra, "Alice Guest / Beacon Soloist", "Atlas Collective", "Rock", 1998, 1, 2},
		{"zebra", "Finale", subsonicZebra, "Atlas Collective", "Atlas Collective", "Rock", 1998, 2, 1},
		{"maple", "Maple Song", subsonicMaple, "Zenith Group", "Zenith Group", "Rock", 2010, 1, 1},
		{"blue", "Blue Song", subsonicBlue, "Cedar Band", "Cedar Band", "Jazz", 2001, 1, 1},
		{"deluxe", "Deluxe Bonus", subsonicAlt, "Atlas Collective", "Atlas Collective", "Metal", 2020, 1, 1},
	} {
		writeSyntheticTrack(t, filepath.Join(root, track.dir, fmt.Sprintf("%02d.flac", i+1)), byte(i+1),
			"TITLE="+track.title, "ALBUM="+track.album, "ARTIST="+track.artist, "ALBUMARTIST="+track.albumArtist,
			"GENRE="+track.genre, fmt.Sprintf("DATE=%d", track.year),
			fmt.Sprintf("DISCNUMBER=%d", track.disc), fmt.Sprintf("TRACKNUMBER=%d", track.position))
	}
	writeSyntheticTrack(t, filepath.Join(root, "cue", "image.flac"), 21)
	const cue = `TITLE "Amber Cues"
PERFORMER "Echo Ensemble"
REM DATE 2005
FILE "image.flac" FLAC
  TRACK 01 AUDIO
    TITLE "Cue One"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Cue Two"
    INDEX 01 02:00:00
`
	if err := os.WriteFile(filepath.Join(root, "cue", "album.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	cover := subsonicBrowsePNG(t, 1200, 600, 17, true)
	if err := os.WriteFile(filepath.Join(root, "zebra", "cover.png"), cover, 0o644); err != nil {
		t.Fatal(err)
	}
	createRoot(t, app, admin, root, uuid.New())
	runScan(t, app, &admin)
	if err := app.app.WaitForScans(t.Context()); err != nil {
		t.Fatal(err)
	}

	f := &subsonicBrowseFixture{
		app: app, admin: admin, albums: map[string]subsonicBrowseRelease{},
		tracks: map[string]string{}, artists: map[string]string{},
	}
	for i, name := range []string{subsonicZebra, subsonicBlue, subsonicCue, subsonicMaple, subsonicAlt} {
		var ids subsonicBrowseRelease
		if err := app.database.QueryRow(`SELECT id::text,group_id::text FROM releases WHERE title=$1`, name).
			Scan(&ids.release, &ids.group); err != nil {
			t.Fatalf("读取扫描专辑 %q：%v", name, err)
		}
		f.albums[name] = ids
		mustExec(t, app, `UPDATE releases SET created_at=$2 WHERE id=$1`, ids.release,
			time.Date(2026, 11, i+1, 0, 0, 0, 0, time.UTC))
	}
	zebra := f.albums[subsonicZebra]
	mustExec(t, app, `UPDATE releases SET group_id=$2 WHERE id=$1`, f.albums[subsonicAlt].release, zebra.group)
	mustExec(t, app, `UPDATE release_groups SET default_release_id=$1 WHERE id=$2`, zebra.release, zebra.group)
	mustExec(t, app, `UPDATE media SET title=CASE position WHEN 1 THEN 'Opening Disc' ELSE 'Closing Disc' END WHERE release_id=$1`, zebra.release)
	for _, name := range []string{"Overture", "Duet & Guests", "Finale", "Maple Song", "Blue Song", "Deluxe Bonus", "Cue One", "Cue Two"} {
		var id string
		if err := app.database.QueryRow(`SELECT id::text FROM tracks WHERE title=$1 AND source_status='present'`, name).Scan(&id); err != nil {
			t.Fatalf("读取扫描曲目 %q：%v", name, err)
		}
		f.tracks[name] = id
	}
	return f
}

func (f *subsonicBrowseFixture) publish(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := maintenance.Run(ctx, f.app.database, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.RunOptions{
		RefreshArtistResolutions: personal.RefreshArtistResolution,
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Atlas Collective", "Alice Guest", "Beacon Soloist", "Cedar Band", "Echo Ensemble", "Zenith Group"} {
		var id string
		if err := f.app.database.QueryRow(`SELECT artist_id::text FROM artist_profiles WHERE effective_display_name=$1`, name).Scan(&id); err != nil {
			t.Fatalf("读取艺人 %q：%v", name, err)
		}
		f.artists[name] = id
	}
}

func (f *subsonicBrowseFixture) params(format string, fields ...string) url.Values {
	params := subsonicParameters(subsonicAdmin, subsonicPassword, "plain")
	params.Set("f", format)
	for i := 0; i < len(fields); i += 2 {
		params.Set(fields[i], fields[i+1])
	}
	return params
}

func subsonicBrowseJSON[T any](t *testing.T, response *httptest.ResponseRecorder, key string) T {
	t.Helper()
	payload, ok := requireSubsonicOK(t, response)[key]
	if !ok || payload == nil {
		t.Fatalf("JSON 缺少 %s 对象：%s", key, response.Body)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析 JSON %s：%v，正文 %s", key, err, raw)
	}
	return out
}

func subsonicBrowseResult[T any](t *testing.T, f *subsonicBrowseFixture, format, endpoint, key string, fields ...string) T {
	t.Helper()
	response := requestSubsonic(f.app, http.MethodGet, endpoint, f.params(format, fields...))
	if format == "json" {
		return subsonicBrowseJSON[T](t, response, key)
	}
	requireSubsonicHTTP(t, response, http.StatusOK, "application/xml; charset=utf-8")
	const ns = "http://subsonic.org/restapi"
	decoder := xml.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	nextElement := func(local string) xml.StartElement {
		t.Helper()
		for {
			token, err := decoder.Token()
			if err != nil {
				t.Fatalf("XML 缺少 %s：%s（%v）", local, response.Body, err)
			}
			if element, ok := token.(xml.StartElement); ok {
				if element.Name != (xml.Name{Space: ns, Local: local}) {
					t.Fatalf("XML 应为 %s，得到 %s：%s", local, element.Name.Local, response.Body)
				}
				return element
			}
		}
	}
	root := nextElement("subsonic-response")
	attrs := make(map[string]string, len(root.Attr))
	for _, attr := range root.Attr {
		attrs[attr.Name.Local] = attr.Value
	}
	version := buildinfo.Current().Version
	if version == "" {
		version = "dev"
	}
	if attrs["status"] != "ok" || attrs["version"] != "1.16.1" || attrs["type"] != "roomusic" ||
		attrs["serverVersion"] != version || attrs["openSubsonic"] != "true" {
		t.Fatalf("XML 外壳属性不符：%s", response.Body)
	}
	child := nextElement(key)
	var out T
	if err := decoder.DecodeElement(&out, &child); err != nil {
		t.Fatalf("解析 XML %s：%v", key, err)
	}
	return out
}

func TestSubsonicBrowseProtocol(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	for _, format := range []string{"json", "xml"} {
		t.Run(format, func(t *testing.T) {
			t.Run("getArtists", func(t *testing.T) {
				result := subsonicBrowseResult[protocol.Artists](t, f, format, "getArtists", "artists")
				var initials, names []string
				for _, index := range result.Indexes {
					initials = append(initials, index.Name)
					for _, artist := range index.Artists {
						names = append(names, artist.Name)
						if artist.ID != f.artists[artist.Name] || artist.AlbumCount != 1 {
							t.Errorf("索引中的艺人引用或专辑数不符：%+v", artist)
						}
					}
				}
				if result.IgnoredArticles != "" || !slices.Equal(initials, []string{"A", "C", "E", "Z"}) ||
					!slices.Equal(names, []string{"Atlas Collective", "Cedar Band", "Echo Ensemble", "Zenith Group"}) {
					t.Fatalf("专辑艺人索引不符：%+v", result)
				}
			})
			t.Run("getArtist", func(t *testing.T) {
				id := f.artists["Atlas Collective"]
				artist := subsonicBrowseResult[protocol.Artist](t, f, format, "getArtist", "artist", "id", id)
				if artist.ID != id || artist.Name != "Atlas Collective" || artist.CoverArt != "ar-"+id ||
					artist.AlbumCount != 1 || len(artist.Albums) != 1 || artist.Albums[0].ID != f.albums[subsonicZebra].group {
					t.Fatalf("艺人及其专辑引用不符：%+v", artist)
				}
			})
			t.Run("getAlbum", func(t *testing.T) {
				group := f.albums[subsonicZebra].group
				album := subsonicBrowseResult[protocol.Album](t, f, format, "getAlbum", "album", "id", group)
				if album.ID != group || album.Name != subsonicZebra || album.CoverArt != "al-"+group ||
					album.ArtistID != f.artists["Atlas Collective"] || album.SongCount != 3 || album.Duration <= 0 ||
					album.Year == nil || *album.Year != 1998 || album.Genre != "Rock" || len(album.Songs) != 3 {
					t.Fatalf("默认版本的多碟专辑字段不符：%+v", album)
				}
				if _, err := time.Parse(time.RFC3339Nano, album.Created); err != nil {
					t.Errorf("created = %q：%v", album.Created, err)
				}
				if want := []protocol.DiscTitle{{Disc: 1, Title: "Opening Disc"}, {Disc: 2, Title: "Closing Disc"}}; !slices.Equal(album.DiscTitles, want) {
					t.Errorf("碟标题 = %v，期望 %v", album.DiscTitles, want)
				}
				for i, name := range []string{"Overture", "Duet & Guests", "Finale"} {
					song := album.Songs[i]
					if song.Title != name || song.ID != f.tracks[name] || song.Track != []int{1, 2, 1}[i] ||
						song.DiscNumber != []int{1, 1, 2}[i] || song.Parent != album.ID ||
						song.AlbumID != album.ID || song.CoverArt != album.CoverArt {
						t.Errorf("碟序、曲序或曲目引用不符：%+v", song)
					}
				}
			})
			t.Run("getSong", func(t *testing.T) {
				id, group := f.tracks["Duet & Guests"], f.albums[subsonicZebra].group
				song := subsonicBrowseResult[protocol.Song](t, f, format, "getSong", "song", "id", id)
				wantArtists := []protocol.NamedArtist{
					{ID: f.artists["Alice Guest"], Name: "Alice Guest"}, {ID: f.artists["Beacon Soloist"], Name: "Beacon Soloist"},
				}
				wantAlbumArtists := []protocol.NamedArtist{{ID: f.artists["Atlas Collective"], Name: "Atlas Collective"}}
				if song.ID != id || song.Title != "Duet & Guests" || song.Parent != group || song.AlbumID != group ||
					song.ArtistID != wantArtists[0].ID || song.CoverArt != "al-"+group || song.IsVideo || song.IsDir ||
					song.Type != "music" || song.MediaType != "song" || song.DisplayArtist != "Alice Guest / Beacon Soloist" ||
					song.DisplayAlbumArtist != "Atlas Collective" || !slices.Equal(song.Artists, wantArtists) ||
					!slices.Equal(song.AlbumArtists, wantAlbumArtists) {
					t.Errorf("歌曲 ID、署名及实体引用不符：%+v", song)
				}
			})
			t.Run("CUE", func(t *testing.T) {
				group := f.albums[subsonicCue].group
				album := subsonicBrowseResult[protocol.Album](t, f, format, "getAlbum", "album", "id", group)
				if album.SongCount != 2 || len(album.Songs) != 2 || len(album.DiscTitles) != 0 || album.CoverArt != "" || album.Genre != "" {
					t.Fatalf("CUE 专辑的曲目或可选字段不符：%+v", album)
				}
				for i, name := range []string{"Cue One", "Cue Two"} {
					want := album.Songs[i]
					got := subsonicBrowseResult[protocol.Song](t, f, format, "getSong", "song", "id", f.tracks[name])
					if !reflect.DeepEqual(got, want) || got.ID != f.tracks[name] || got.Parent != group || got.AlbumID != group ||
						got.DiscNumber != 1 || got.Track != i+1 || got.Duration != 120 || got.Suffix != "flac" ||
						got.ContentType != "audio/flac" || got.CoverArt != "" || got.Genre != "" {
						t.Errorf("CUE 曲目 %d 或父文件格式不符：%+v", i+1, got)
					}
				}
			})
			t.Run("getAlbumList2", func(t *testing.T) {
				list := subsonicBrowseResult[protocol.AlbumList](t, f, format, "getAlbumList2", "albumList2",
					"type", "alphabeticalByName", "size", "1")
				if len(list.Albums) != 1 || list.Albums[0].ID != f.albums[subsonicCue].group || list.Albums[0].Name != subsonicCue {
					t.Fatalf("专辑列表不符：%+v", list)
				}
			})
			t.Run("search3", func(t *testing.T) {
				search := subsonicBrowseResult[protocol.SearchResult](t, f, format, "search3", "searchResult3", "query", "")
				if len(search.Artists) != 6 || len(search.Albums) != 4 || len(search.Songs) != 7 {
					t.Fatalf("搜索三类索引数据分别为 %d、%d、%d，期望 6、4、7", len(search.Artists), len(search.Albums), len(search.Songs))
				}
			})
			t.Run("getRandomSongs", func(t *testing.T) {
				random := subsonicBrowseResult[protocol.RandomSongs](t, f, format, "getRandomSongs", "randomSongs", "size", "1", "genre", "Jazz")
				if len(random.Songs) != 1 || random.Songs[0].ID != f.tracks["Blue Song"] {
					t.Fatalf("随机 Jazz 曲目不符：%+v", random)
				}
			})
			t.Run("getGenres", func(t *testing.T) {
				genres := subsonicBrowseResult[protocol.Genres](t, f, format, "getGenres", "genres")
				want := []protocol.Genre{{Name: "Jazz", AlbumCount: 1, SongCount: 1}, {Name: "Rock", AlbumCount: 2, SongCount: 4}}
				if !slices.Equal(genres.Genres, want) {
					t.Fatalf("流派统计 = %+v，期望 %+v", genres.Genres, want)
				}
			})
			t.Run("EmptyInfo", func(t *testing.T) {
				artist := subsonicBrowseResult[protocol.ArtistInfo](t, f, format, "getArtistInfo2", "artistInfo2", "id", f.artists["Cedar Band"])
				album := subsonicBrowseResult[protocol.AlbumInfo](t, f, format, "getAlbumInfo2", "albumInfo", "id", f.albums[subsonicBlue].group)
				if artist != (protocol.ArtistInfo{}) || album != (protocol.AlbumInfo{}) {
					t.Fatalf("没有资料的艺人、专辑应返回空对象：%+v / %+v", artist, album)
				}
			})
		})
	}
	t.Run("PopulatedInfo", func(t *testing.T) {
		artist, release := f.artists["Atlas Collective"], f.albums[subsonicZebra]
		artistMBID, releaseMBID := uuid.New(), uuid.New()
		linkArtistMedia(t, f.app, artist, artistMBID)
		mustExec(t, f.app, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
 VALUES(gen_random_uuid(),'musicbrainz','release',$1,$2,'manual','certain')`, release.release, releaseMBID)
		const bio = "Atlas Collective is a synthetic test ensemble."
		revision := readArtistMedia(t, f.app, &f.admin, artist).Revision
		result := artistMediaCommand(t, f.app, &f.admin, http.MethodPut, artist, "/bio", uuid.New(),
			map[string]any{"expected_revision": revision, "text": bio, "lang": "en"})
		requireArtistMediaEdit(t, result, artist, "", revision+1)
		for _, format := range []string{"json", "xml"} {
			info := subsonicBrowseResult[protocol.ArtistInfo](t, f, format, "getArtistInfo2", "artistInfo2", "id", artist)
			if info.Biography != bio || info.MusicBrainzID != artistMBID {
				t.Errorf("%s 艺人资料 = %+v", format, info)
			}
			album := subsonicBrowseResult[protocol.AlbumInfo](t, f, format, "getAlbumInfo2", "albumInfo", "id", release.group)
			if album.MusicBrainzID != releaseMBID {
				t.Errorf("%s 专辑外链 = %+v，期望 %s", format, album, releaseMBID)
			}
		}
	})
}

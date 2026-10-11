package library

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

// writeLyricsFLAC 写一个合成 FLAC：seed 决定音频内容与指纹，所以同一个 seed 重写只改标签，曲目身份不变。
func writeLyricsFLAC(t *testing.T, root registeredRoot, relativePath string, seed byte, track int, comments ...string) {
	t.Helper()
	var md5 [16]byte
	for index := range md5 {
		md5[index] = seed + byte(index) + 1
	}
	tags := append([]string{"TITLE=Track " + strconv.Itoa(track), "ARTIST=Artist", "ALBUM=Album", "TRACKNUMBER=" + strconv.Itoa(track)}, comments...)
	writeFixtureFile(t, root, relativePath, parsertest.FLACWithAudio(t, md5, bytes.Repeat([]byte{seed}, 64), tags...))
}

// writeLyricsMP3 写一个带 USLT（UTF-8）的 ID3v2.3 MP3：标签之后跟几个 MPEG 帧头字节，让文件有可计算指纹的音频部分。
func writeLyricsMP3(t *testing.T, root registeredRoot, relativePath, title, lyrics string) {
	t.Helper()
	frame := func(id string, payload []byte) []byte {
		out := append([]byte(id), byte(len(payload)>>24), byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload)), 0, 0)
		return append(out, payload...)
	}
	body := frame("TIT2", append([]byte{3}, title...))
	if lyrics != "" {
		body = append(body, frame("USLT", append([]byte{3, 'e', 'n', 'g', 0}, lyrics...))...)
	}
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(len(body)>>21) & 0x7f, byte(len(body)>>14) & 0x7f, byte(len(body)>>7) & 0x7f, byte(len(body)) & 0x7f}
	content := append(append(header, body...), bytes.Repeat([]byte{0xff, 0xfb, 0x90, 0x00}, 64)...)
	writeFixtureFile(t, root, relativePath, content)
}

type storedLyrics struct {
	Lyrics, Source sql.NullString
}

func readStoredLyrics(t *testing.T, fixture *libraryFixture, rootID string) map[string]storedLyrics {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT relative_path,lyrics,lyrics_source FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'`, rootID)
	if err != nil {
		t.Fatalf("read lyrics: %v", err)
	}
	defer rows.Close()
	stored := map[string]storedLyrics{}
	for rows.Next() {
		var path string
		var row storedLyrics
		if err := rows.Scan(&path, &row.Lyrics, &row.Source); err != nil {
			t.Fatalf("scan lyrics: %v", err)
		}
		stored[path] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate lyrics: %v", err)
	}
	return stored
}

func requireStoredLyrics(t *testing.T, stored map[string]storedLyrics, path, wantLyrics, wantSource string) {
	t.Helper()
	row, present := stored[path]
	if !present {
		t.Fatalf("no present track at %q (have %d tracks)", path, len(stored))
	}
	if wantLyrics == "" {
		if row.Lyrics.Valid || row.Source.Valid {
			t.Fatalf("%s: lyrics = %q (%s), want NULL", path, row.Lyrics.String, row.Source.String)
		}
		return
	}
	if row.Lyrics.String != wantLyrics || row.Source.String != wantSource {
		t.Fatalf("%s: lyrics = %q (%s), want %q (%s)", path, row.Lyrics.String, row.Source.String, wantLyrics, wantSource)
	}
}

func TestPostgreSQLScanStoresUpdatesAndClearsEmbeddedLyrics(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "embedded-lyrics")
	service := scannerTestApplication(fixture)
	synced := "[ti:Song]\n[00:01.00]第一行\n[00:02.50]第二行"
	writeLyricsFLAC(t, root, "Album/01.flac", 1, 1, "LYRICS="+synced)
	writeLyricsMP3(t, root, "Album/02.mp3", "Track 2", "line one\r\nline two")
	writeLyricsFLAC(t, root, "Album/03.flac", 3, 3)
	writeLyricsFLAC(t, root, "Album/04.flac", 4, 4, "UNSYNCEDLYRICS=plain words")

	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	stored := readStoredLyrics(t, fixture, root.ID)
	if len(stored) != 4 {
		t.Fatalf("stored tracks = %d, want 4", len(stored))
	}
	requireStoredLyrics(t, stored, "Album/01.flac", synced, parser.LyricsSourceVorbisLyrics)
	requireStoredLyrics(t, stored, "Album/02.mp3", "line one\nline two", parser.LyricsSourceID3USLT)
	requireStoredLyrics(t, stored, "Album/03.flac", "", "")
	requireStoredLyrics(t, stored, "Album/04.flac", "plain words", parser.LyricsSourceVorbisUnsynced)
	idsBefore := presentTrackIDs(t, fixture, root.ID)

	// 重写标签但保持音频内容：曲目沿用同一个 ID，歌词随标签更新、清空或新增。
	writeLyricsFLAC(t, root, "Album/01.flac", 1, 1, "LYRICS=[00:01.00]changed")
	writeLyricsFLAC(t, root, "Album/03.flac", 3, 3, "UNSYNCEDLYRICS=now with words")
	writeLyricsFLAC(t, root, "Album/04.flac", 4, 4)
	writeLyricsMP3(t, root, "Album/02.mp3", "Track 2", "")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	stored = readStoredLyrics(t, fixture, root.ID)
	requireStoredLyrics(t, stored, "Album/01.flac", "[00:01.00]changed", parser.LyricsSourceVorbisLyrics)
	requireStoredLyrics(t, stored, "Album/02.mp3", "", "")
	requireStoredLyrics(t, stored, "Album/03.flac", "now with words", parser.LyricsSourceVorbisUnsynced)
	requireStoredLyrics(t, stored, "Album/04.flac", "", "")
	idsAfter := presentTrackIDs(t, fixture, root.ID)
	for path, id := range idsBefore {
		requireTrackAt(t, idsAfter, path, id)
	}
	requireNoMissingTracks(t, fixture, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations`, 0)
}

func TestPostgreSQLCueVirtualTracksHaveNoLyricsButTranscriptTracksKeepThem(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "cue-lyrics")
	service := scannerTestApplication(fixture)

	// 整轨镜像加多轨 CUE：虚拟轨不继承整张镜像文件里的歌词。
	writeLyricsFLAC(t, root, "Image/image.flac", 10, 1, "LYRICS=[00:01.00]belongs to the whole image")
	writeFixtureFile(t, root, "Image/album.cue", []byte(strings.Join([]string{
		`TITLE "Cue Release"`,
		`PERFORMER "Cue Artist"`,
		`FILE "image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "First"`,
		`    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`,
		`    TITLE "Second"`,
		`    INDEX 01 01:00:00`,
	}, "\n")))
	// 逐轨抄本：每个物理文件对应只含一个轨的 CUE，曲目仍是物理文件，歌词保留。
	writeLyricsFLAC(t, root, "Transcript/01.flac", 20, 1, "LYRICS=[00:01.00]transcript lyrics")
	writeFixtureFile(t, root, "Transcript/01.cue", []byte(strings.Join([]string{
		`TITLE "Transcript Release"`,
		`PERFORMER "Cue Artist"`,
		`FILE "01.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "Track 1"`,
		`    INDEX 01 00:00:00`,
	}, "\n")))

	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND lyrics IS NULL AND lyrics_source IS NULL`, 2, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND relative_path LIKE 'Image/%'`, 2, root.ID)
	stored := readStoredLyrics(t, fixture, root.ID)
	requireStoredLyrics(t, stored, "Transcript/01.flac", "[00:01.00]transcript lyrics", parser.LyricsSourceVorbisLyrics)
}

func TestPostgreSQLTrackLyricsConstraint(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "lyrics-constraint")
	service := scannerTestApplication(fixture)
	writeLyricsFLAC(t, root, "Album/01.flac", 1, 1)
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	trackID := presentTrackIDs(t, fixture, root.ID)["Album/01.flac"]
	if trackID == "" {
		t.Fatal("the fixture track was not scanned")
	}
	update := func(lyrics, source any) error {
		_, err := fixture.database.Exec(`UPDATE tracks SET lyrics=$1,lyrics_source=$2 WHERE id=$3::uuid`, lyrics, source, trackID)
		return err
	}

	for name, testCase := range map[string]struct {
		lyrics, source any
		wantError      bool
	}{
		"both columns empty":               {nil, nil, false},
		"each known source":                {"text", parser.LyricsSourceVorbisLyrics, false},
		"unsynced Vorbis source":           {"text", parser.LyricsSourceVorbisUnsynced, false},
		"MP4 source":                       {"text", parser.LyricsSourceMP4, false},
		"ID3 source":                       {"text", parser.LyricsSourceID3USLT, false},
		"exactly 64 KiB":                   {strings.Repeat("a", parser.MaxLyricsBytes), parser.LyricsSourceVorbisLyrics, false},
		"lyrics without a source":          {"text", nil, true},
		"a source without lyrics":          {nil, parser.LyricsSourceVorbisLyrics, true},
		"empty lyrics":                     {"", parser.LyricsSourceVorbisLyrics, true},
		"an unknown source":                {"text", "lrc_sidecar", true},
		"one byte over 64 KiB":             {strings.Repeat("a", parser.MaxLyricsBytes+1), parser.LyricsSourceVorbisLyrics, true},
		"the limit counts bytes":           {strings.Repeat("歌", parser.MaxLyricsBytes/3+1), parser.LyricsSourceMP4, true},
		"multi-byte text within the limit": {strings.Repeat("歌", parser.MaxLyricsBytes/3), parser.LyricsSourceMP4, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := update(testCase.lyrics, testCase.source)
			if testCase.wantError && err == nil {
				t.Fatal("the database accepted an inconsistent lyrics row")
			}
			if !testCase.wantError && err != nil {
				t.Fatalf("the database rejected a valid lyrics row: %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), "tracks_lyrics_ck") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

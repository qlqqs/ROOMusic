package app

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback"
)

func writeAcceptanceAudio(t *testing.T, root, album, title, tone string, tags ...string) {
	t.Helper()
	dir := filepath.Join(root, album)
	requireCurationOK(t, os.MkdirAll(dir, 0755))
	args := []string{"-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", tone, "-c:a", "flac"}
	for _, tag := range append([]string{"album=" + album, "title=" + title, "artist=合成正式艺人"}, tags...) {
		args = append(args, "-metadata", tag)
	}
	args = append(args, filepath.Join(dir, title+".flac"))
	if out, err := exec.CommandContext(t.Context(), b3RequireTool(t, "ROOMUSIC_TEST_FFMPEG"), args...).CombinedOutput(); err != nil {
		t.Fatalf("生成合成音频 %v %s", err, out)
	}
}

type acceptanceAlbum struct {
	release, recording, group string
	cover                     []byte
}

func newAcceptanceAlbum(t *testing.T) (acceptanceAlbum, *httptest.Server) {
	t.Helper()
	a := acceptanceAlbum{uuid.New(), uuid.New(), uuid.New(), externalFixturePNG(t, 4, 4)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mb/release/" + a.release:
			fmt.Fprintf(w, `{"id":%q,"title":"合成正式专辑","date":"2020","artist-credit":[{"name":"合成正式艺人"}],"release-group":{"id":%q},"media":[{"position":1,"track-count":1,"tracks":[{"id":"t","position":1,"length":12000,"recording":{"id":%q}}]}]}`, a.release, a.group, a.recording)
		case "/covers/release/" + a.release + "/":
			fmt.Fprint(w, `{"images":[{"id":17,"front":true}]}`)
		case "/covers/release/" + a.release + "/17-1200":
			w.Header().Set("Content-Type", "image/png")
			w.Write(a.cover)
		case "/lyrics/api/get":
			q := r.URL.Query()
			fmt.Fprintf(w, `{"id":8,"trackName":%q,"artistName":%q,"duration":12,"syncedLyrics":"[00:01.00]第一行"}`, q.Get("track_name"), q.Get("artist_name"))
		case "/mb/release", "/mb/artist", "/lyrics/api/search":
			http.NotFound(w, r)
		default:
			t.Errorf("意外请求 %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return a, server
}

type acceptanceFile struct {
	dir    bool
	digest [sha256.Size]byte
	mtime  time.Time
}

func acceptanceTree(t *testing.T, root string, makeReadOnly bool) map[string]acceptanceFile {
	t.Helper()
	tree := make(map[string]acceptanceFile)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if makeReadOnly {
			mode := info.Mode().Perm()
			if err := os.Chmod(path, mode&^0222); err != nil {
				return err
			}
			t.Cleanup(func() {
				if err := os.Chmod(path, mode); err != nil {
					t.Error(err)
				}
			})
		}
		entry := acceptanceFile{dir: d.IsDir()}
		if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.digest, entry.mtime = sha256.Sum256(data), info.ModTime()
		}
		tree[path] = entry
		return nil
	})
	requireCurationOK(t, err)
	return tree
}

func TestPostgreSQLCurationReadOnlyMusicTree(t *testing.T) {
	remote, server := newAcceptanceAlbum(t)
	tool := b3RealFingerprinter(t)
	app, clock, admin := b3ProgramApp(t, tool, server)
	b3Root(t, app)
	writeAcceptanceAudio(t, app.allowedRoot, "合成正式专辑", "第一首", b3LowTone, "MUSICBRAINZ_ALBUMID="+remote.release, "TRACKNUMBER=1")
	writeSyntheticTrack(t, filepath.Join(app.allowedRoot, "第二版", "02.flac"), 42, "ALBUM=第二版", "TITLE=第二首", "ARTIST=合成正式艺人", "TRACKNUMBER=2")
	before := acceptanceTree(t, app.allowedRoot, true)
	scanAndConfirmCuration(t, app, &admin)
	id := programString(t, app, `SELECT id FROM releases WHERE relative_directory='合成正式专辑'`)
	other := programString(t, app, `SELECT id FROM releases WHERE relative_directory='第二版'`)
	track, _ := externalTrack(t, app, id)
	prints := playback.NewFingerprints(app.database, []string{app.allowedRoot}, tool, slog.Default())
	print, status, err := prints.Fingerprint(t.Context(), track)
	if err != nil || status != playback.FingerprintOK || print.Value == "" || print.DurationSeconds != 12 {
		t.Fatalf("指纹状态 %v，时长 %d，错误 %v", status, print.DurationSeconds, err)
	}
	b3Enable(t, app, admin)
	writer := curation.NewWriter(app.database, app.dataDirectory, nil)
	art := externalArtworkRequest(id)
	art.Data = remote.cover
	applyExternalArtwork(t, writer, art)
	applyExternalLyrics(t, writer, externalLyricsRequest(track))
	clock.advance(t, time.Minute)
	b3RequireReleaseResult(t, app, id, "matched", "tag_id", "not_reached", "")
	url := "/api/v1/releases/" + id
	var preview struct{ Token string }
	getJSON(t, app, &admin, url+"/merge-preview?source="+other, &preview)
	res := app.request(t, "POST", url+"/merge", map[string]string{"source_release_id": other, "token": preview.Token}, &admin, idempotencyHeader(uuid.New()))
	if res.Code != http.StatusOK {
		t.Fatal(res.Code, res.Body.String())
	}
	runScan(t, app, &admin)
	b3RequireCount(t, app, 2, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 AND t.source_status='present'`, id)
	after := acceptanceTree(t, app.allowedRoot, false)
	for path, entry := range before {
		if got, found := after[path]; !found || got != entry {
			t.Errorf("源路径 %s 发生变化，之前 %+v，之后 %+v", path, entry, got)
		}
	}
	for path := range after {
		if _, found := before[path]; !found {
			t.Errorf("音乐目录新增路径 %s", path)
		}
	}
}

func TestPostgreSQLCurationScannedAlbumProvenance(t *testing.T) {
	a, server := newAcceptanceAlbum(t)
	app, clock, admin := b3ProgramApp(t, nil, server)
	b3Root(t, app)
	writeAcceptanceAudio(t, app.allowedRoot, "合成正式专辑", "第一首", b3LowTone, "MUSICBRAINZ_ALBUMID="+a.release, "TRACKNUMBER=1")
	b3Enable(t, app, admin)
	scanAndConfirmCuration(t, app, &admin)
	id := programString(t, app, `SELECT id FROM releases WHERE relative_directory='合成正式专辑' AND year IS NULL`)
	track, _ := externalTrack(t, app, id)
	clock.advance(t, time.Minute)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND year=2020`, id)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, id, a.release)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1 AND external_id=$2`, track, a.recording)
	stored, _ := externalArtworkFile(t, app, a.cover)
	externalArtworkState(t, app, id, programString(t, app, `SELECT operation_id FROM release_external_artworks WHERE release_id=$1`, id), stored)
	requireExternalArtworkHTTP(t, app, &admin, stored, a.cover)
	requireExternalLyricsHTTP(t, app, &admin, track, "online", "synced", []map[string]any{{"start_ms": 1000, "text": "第一行"}})
	var log struct {
		Items []struct {
			Type   string
			Origin struct{ Source, Provider, Basis string }
		}
	}
	getJSON(t, app, &admin, "/api/v1/operations", &log)
	want := map[string]string{"external_link": "musicbrainz", "release_auto_correction": "musicbrainz", "release_external_artwork": "coverartarchive", "track_external_lyrics": "lrclib"}
	got := map[string]string{}
	for _, entry := range log.Items {
		if entry.Origin.Source != "curation" || strings.TrimSpace(entry.Origin.Basis) == "" {
			t.Errorf("整理记录缺少来源或依据 %+v", entry)
		}
		got[entry.Type] = entry.Origin.Provider
	}
	if len(log.Items) != len(want) || !maps.Equal(got, want) {
		t.Fatalf("整理记录 %+v，期望 %v", log.Items, want)
	}
}

func TestPostgreSQLCurationDisabledBeforeFirstEnable(t *testing.T) {
	t.Run("acoustid", func(t *testing.T) {
		remote := b3NewRemoteRelease()
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v2/lookup":
				hits.Add(1)
				form := b3LookupForm(t, r)
				i := 0
				switch form.Get("duration") {
				case "12":
				case "16":
					i = 1
				default:
					t.Errorf("意外的音频时长 %s", form.Get("duration"))
				}
				fmt.Fprintf(w, `{"status":"ok","results":[{"id":"hit","score":0.98,"recordings":[%s]}]}`, b3AcoustHit(remote, i))
			case "/mb/release/" + remote.ID:
				remote.writeMusicBrainz(w)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		app, clock, admin := b3ProgramApp(t, b3RealFingerprinter(t), server)
		b3Root(t, app)
		b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
		b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
		writeAcceptanceAudio(t, app.allowedRoot, "合成正式专辑", "第一首", b3LowTone, "TRACKNUMBER=1")
		writeAcceptanceAudio(t, app.allowedRoot, "合成正式专辑", "第二首", b3HighTone, "TRACKNUMBER=2")
		runScan(t, app, &admin)
		id := programString(t, app, `SELECT id FROM releases WHERE relative_directory='合成正式专辑'`)
		clock.advance(t, time.Minute)
		if n := hits.Load(); n != 0 {
			t.Fatalf("总开关关闭时 AcoustID 收到 %d 次请求", n)
		}
		confirmProgramRelease(t, app, &admin, id)
		b3Enable(t, app, admin)
		clock.advance(t, time.Minute)
		b3RequireReleaseResult(t, app, id, "matched", "acoustid", "used", "")
		if n := hits.Load(); n != 2 {
			t.Fatalf("开启后两首合成曲目的指纹查询数 %d", n)
		}
	})
	t.Run("model", func(t *testing.T) {
		f := newSchedulerFixture(t)
		f.configure(t, &f.admin)
		f.pending(t, f.a)
		select {
		case q := <-f.requests:
			t.Fatalf("首次开启前请求了模型 %s", q.Model)
		case <-time.After(200 * time.Millisecond):
		}
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
		f.enable(t)
		req := f.next(t, false)
		f.pause(t)
		req.complete(t)
	})
}

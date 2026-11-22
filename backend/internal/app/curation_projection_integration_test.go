package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

// A-V3：同一音频的原样重扫与标签变化后重扫，既检查有效值也检查自动行的物理版本。
func TestPostgreSQLCurationRescan(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	library := createLibraryDirectory(t, app, "rescan")
	path := filepath.Join(library, "disc", "01.flac")
	write := func(album string) {
		t.Helper()
		writeSyntheticTrack(t, path, 1, "TITLE=Prelude", "ARTIST=Orchestra", "ALBUM="+album, "ALBUMARTIST=Orchestra", "TRACKNUMBER=1")
	}
	write("Original")
	r := app.request(t, "POST", "/api/v1/library-roots", map[string]string{"path": library}, &admin, idempotencyHeader("rescan-root"))
	if r.Code != 201 {
		t.Fatalf("登记扫描目录：%d %s", r.Code, r.Body.String())
	}
	runScan(t, app, &admin)
	var id string
	if err := app.database.QueryRow("SELECT id FROM releases WHERE title='Original'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	w := curation.NewWriter(app.database, app.dataDirectory, nil)
	if _, err := w.SetSettings(t.Context(), admin.id, true); err != nil {
		t.Fatal(err)
	}
	applied, err := w.Apply(t.Context(), autoRequest(id, "fixture", "", map[string]any{"title": "自动标题"}))
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := app.database.QueryRow("SELECT xmin::text FROM release_auto_values WHERE release_id=$1 AND field='title'", id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, scanned := range []string{"Original", "Retagged edition"} {
		if scanned != "Original" {
			write(scanned)
		}
		runScan(t, app, &admin)
		var title, effective, owner, rowVersion string
		var revision int
		err := app.database.QueryRow(`SELECT r.title,e.title,a.operation_id::text,a.xmin::text,r.auto_revision FROM releases r JOIN release_effective e ON e.id=r.id JOIN release_auto_values a ON a.release_id=r.id AND a.field='title' WHERE r.id=$1`, id).Scan(&title, &effective, &owner, &rowVersion, &revision)
		if err != nil || title != scanned || effective != "自动标题" || owner != applied.OperationID || rowVersion != before || revision != 2 {
			t.Fatalf("重扫 %q：扫描=%q，有效=%q，操作=%q，行版本=%s（原 %s），自动版本=%d，错误=%v", scanned, title, effective, owner, rowVersion, before, revision, err)
		}
	}
}

// A-V7：专辑署名、按署名浏览的排序与已发布的搜索投影随写入和撤销同步更新。
func TestPostgreSQLCurationArtistProjection(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	for _, x := range []struct{ id, name string }{{f.a, "Zulu Ensemble"}, {f.b, "Middle Ensemble"}} {
		mustExec(t, f.application, "UPDATE releases SET album_artist=$2,artist=$2 WHERE id=$1", x.id, x.name)
		mustExec(t, f.application, "INSERT INTO release_credits(release_id,role,name) VALUES($1,'album_artist',$2)", x.id, x.name)
	}
	(&artistSamples{t: t, app: f.application}).publish()
	check := func(want, absent string, order []string) {
		t.Helper()
		var detail struct{ Credits []catalog.ReleaseCredit }
		getJSON(t, f.application, &f.ordinary, "/api/v1/releases/"+f.a, &detail)
		if len(detail.Credits) != 1 || detail.Credits[0].Role != "album_artist" || detail.Credits[0].Name != want {
			t.Fatalf("发行署名=%+v，期望 %s", detail.Credits, want)
		}
		var artists catalog.ArtistPage[catalog.ArtistSummary]
		getJSON(t, f.application, &f.ordinary, "/api/v1/artists?sort=name&role=album_artist", &artists)
		var names []string
		var artistID string
		for _, a := range artists.Items {
			names = append(names, a.Name)
			if a.Name == want {
				artistID = a.ID
			}
		}
		if !slices.Equal(names, order) || artistID == "" {
			t.Fatalf("艺人署名排序=%v，期望 %v", names, order)
		}
		var albums catalog.ArtistPage[catalog.ArtistAlbum]
		getJSON(t, f.application, &f.ordinary, "/api/v1/artists/"+artistID+"/albums", &albums)
		if len(albums.Items) != 1 || albums.Items[0].CreditedReleaseID != f.a {
			t.Fatalf("艺人页的专辑=%+v", albums)
		}
		for _, query := range []string{want, absent} {
			var page searchPage[catalog.SearchAlbumItem]
			getJSON(t, f.application, &f.ordinary, searchURL("albums", query), &page)
			if page.CatalogState.SearchMode != "normalized" {
				t.Fatalf("搜索投影尚未发布：%+v", page.CatalogState)
			}
			if query == want {
				if len(page.Items) != 1 || page.Items[0].MatchedReleaseID != f.a {
					t.Fatalf("搜索 %s 未找到对应发行：%+v", query, page)
				}
			} else if page.Pagination.Total != 0 {
				t.Fatalf("搜索 %s 仍命中：%+v", query, page)
			}
		}
	}
	check("Zulu Ensemble", "Alpha Quartet", []string{"Middle Ensemble", "Zulu Ensemble"})
	op := f.apply(t, f.a, "fixture", "", map[string]any{"album_artist": "Alpha Quartet"})
	check("Alpha Quartet", "Zulu Ensemble", []string{"Alpha Quartet", "Middle Ensemble"})
	f.undo(t, op)
	check("Zulu Ensemble", "Alpha Quartet", []string{"Middle Ensemble", "Zulu Ensemble"})
}

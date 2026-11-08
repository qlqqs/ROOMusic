package app

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLWorkSearchCatalogPropagatesToAlbum(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	user := app.createUser(t, "user")
	library := createLibraryDirectory(t, app, "work-search")
	const work = "Cello Suite No. 1 in G major, BWV 1007"
	for i, movement := range []string{"Prelude", "Allemande", "Bonus"} {
		comments := []string{"ALBUM=Cello Recordings", "ARTIST=Cellist", "GENRE=Pop", "TITLE=" + movement, "TRACKNUMBER=" + []string{"1", "2", "3"}[i]}
		if i < 2 {
			comments = append(comments, "WORK="+work, "MOVEMENTNAME="+movement, "MOVEMENTNUMBER="+[]string{"1", "2"}[i])
		}
		writeSyntheticTrack(t, filepath.Join(library, "album", []string{"01.flac", "02.flac", "03.flac"}[i]), byte(61+i), comments...)
	}
	response := app.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": library}, &admin, idempotencyHeader(uuid.New()))
	if response.Code != http.StatusCreated {
		t.Fatalf("登记作品搜索素材：%d %s", response.Code, response.Body.String())
	}
	scanID := startScanFeedback(t, app, &admin)
	if status := waitForScanToFinish(t, app, scanID); status != "succeeded" {
		t.Fatalf("作品搜索素材扫描 = %s，期望 succeeded", status)
	}
	// 后台发布使用与生产一致的作品投影入口。
	publishSearch(t, app)
	var releaseID, groupID string
	rows, err := app.database.Query(`SELECT t.id::text,r.id::text,r.group_id::text,t.work_title,t.work_catalog,t.work_index,t.movement_number,t.movement_title,t.work_source
  FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
  WHERE t.position<=2 ORDER BY t.position`)
	if err != nil {
		t.Fatal(err)
	}
	var trackIDs []string
	for rows.Next() {
		var id, title, catalog, movement, source string
		var index, number int
		if err := rows.Scan(&id, &releaseID, &groupID, &title, &catalog, &index, &number, &movement, &source); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if title != work || catalog != "BWV 1007" || index != 1 || number != len(trackIDs)+1 || movement != []string{"Prelude", "Allemande"}[len(trackIDs)] || source != "tag" {
			t.Errorf("作品写入 = %q / %q / %d / %d / %q / %q", title, catalog, index, number, movement, source)
		}
		trackIDs = append(trackIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(trackIDs) != 2 {
		t.Fatalf("作品轨数 = %d，期望 2", len(trackIDs))
	}
	for _, query := range []string{"BWV 1007", "bwv1007"} {
		t.Run(query, func(t *testing.T) {
			tracks := requireSearchTracks(t, app, &user, query, trackIDs...)
			for _, item := range tracks.Items {
				if item.Match.Field != "work_catalog" || item.Match.Original != "BWV 1007" {
					t.Errorf("编号命中说明 = %+v", item.Match)
				}
			}
			albums := requireSearchAlbums(t, app, &user, query, groupID)
			item := albums.Items[0]
			if item.MatchedReleaseID != releaseID || item.Match.Field != "work_catalog" || item.Match.Original != "BWV 1007" || item.Match.TrackID == nil || (*item.Match.TrackID != trackIDs[0] && *item.Match.TrackID != trackIDs[1]) {
				t.Fatalf("作品未正确传播专辑：%+v", item)
			}
		})
	}
	// 同时确认作品名进入普通文本分支；没有作品的同专辑第三轨不能被兄弟轨带中。
	tracks := requireSearchTracks(t, app, &user, "Cello Suite", trackIDs...)
	for _, item := range tracks.Items {
		if item.Match.Field != "work_title" || item.Match.Original != work {
			t.Errorf("作品名命中说明 = %+v", item.Match)
		}
	}
	requireSearchAlbums(t, app, &user, "Cello Suite", groupID)
}

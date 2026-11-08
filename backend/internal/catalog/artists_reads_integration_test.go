package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func publishArtistReadFixture(t *testing.T, f *placementFixture) {
	t.Helper()
	ctx := context.Background()
	ids, err := f.store.artistIDs(ctx, `SELECT id::text FROM releases`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := AcquireArtistWriteGate(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := artistgraph.Refresh(ctx, tx, ids); err != nil {
		t.Fatal(err)
	}
	// 在线发行刷新不再补齐无关身份；夹具发布前显式准备包括孤立种子在内的全部身份。
	artistIDs, err := NewStore(tx).artistIDs(ctx, `SELECT id::text FROM artists ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for start := 0; start < len(artistIDs); start += 100 {
		if err := artistgraph.RefreshProfiles(ctx, tx, artistIDs[start:min(start+100, len(artistIDs))]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version,catalog_epoch=catalog_epoch+1 WHERE capability='artists'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	publishSearchFixture(t, f)
}

func findArtistReadFixture(t *testing.T, f *placementFixture, key string) string {
	t.Helper()
	var id string
	if err := f.database.QueryRow(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key=$1`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLArtistReadPreparingAndUnavailable(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	id := "a0000000-0000-4000-8000-000000000001"
	for name, read := range map[string]func() error{
		"resolve-empty": func() error { _, err := f.store.ResolveArtists(ctx, nil); return err },
		"summary-empty": func() error { _, err := f.store.LoadArtistSummaries(ctx, nil); return err },
		"list":          func() error { _, err := f.store.ListArtists(ctx, ArtistListParams{Page: 1, PageSize: 50}); return err },
		"detail":        func() error { _, err := f.store.LoadArtist(ctx, id); return err },
		"names":         func() error { _, err := f.store.ListArtistNames(ctx, id, 1, 50); return err },
		"albums":        func() error { _, err := f.store.ListArtistAlbums(ctx, id, 1, 50); return err },
		"tracks":        func() error { _, err := f.store.ListArtistTracks(ctx, id, "composer", 1, 50); return err },
		"queue":         func() error { _, err := f.store.LoadArtistQueue(ctx, id, "album", nil); return err },
		"search": func() error {
			_, err := f.store.Search(ctx, SearchParams{Type: "artists", Page: 1, PageSize: 50})
			return err
		},
	} {
		if err := read(); !errors.Is(err, ErrArtistCatalogPreparing) {
			t.Fatalf("%s preparing: %v", name, err)
		}
	}
	publishArtistReadFixture(t, f)
	detail, err := f.store.LoadArtist(ctx, id)
	if err != nil || detail.Artist.Available || detail.Artist.AlbumCount != 0 || detail.Artist.TrackCount != 0 || len(detail.Names) != 3 {
		t.Fatalf("unavailable: %+v %v", detail, err)
	}
	page, err := f.store.ListArtists(ctx, ArtistListParams{Page: 1, PageSize: 50})
	if err != nil || page.Pagination.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("empty list: %+v %v", page, err)
	}
	queue, err := f.store.LoadArtistQueue(ctx, id, "album", nil)
	if err != nil || queue.Items == nil || queue.Total != 0 || queue.Seed != nil {
		t.Fatalf("empty queue: %+v %v", queue, err)
	}
	if _, err := f.store.LoadArtist(ctx, uuid.New()); !errors.Is(err, ErrArtistNotFound) {
		t.Fatalf("missing artist: %v", err)
	}
}

func TestPostgreSQLArtistReadParticipationAndAliases(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	representative := f.release("A 代表")
	sibling := f.track(representative, "兄弟曲目", "present", 120)
	member := f.release("Z 参与版本")
	track := f.track(member, "独唱", "present", 180)
	missing := f.track(member, "缺失", "missing", 200)
	f.exec(`UPDATE releases SET group_id=$1::uuid,artist='周杰倫',album_artist='周杰倫' WHERE id=$2::uuid`, representative.groupID, member.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, representative.releaseID, representative.groupID)
	f.exec(`UPDATE tracks SET artist='周杰倫' WHERE id=ANY($1::uuid[])`, []string{track, missing})
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','周杰伦',1),($1::uuid,'performer','周杰倫',1),($2::uuid,'composer','周杰伦',1)`, track, missing)
	publishArtistReadFixture(t, f)
	id := findArtistReadFixture(t, f, "周杰伦")
	detail, err := f.store.LoadArtist(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Artist.AlbumCount != 1 || detail.Artist.TrackCount != 1 || !detail.Artist.Available || len(detail.RoleCounts) != 5 {
		t.Fatalf("participation counts: %+v", detail)
	}
	for _, count := range detail.RoleCounts {
		if count.Role == "composer" && (count.Total != 2 || count.Present != 1) {
			t.Fatalf("missing role count: %+v", count)
		}
	}
	list, err := f.store.ListArtists(ctx, ArtistListParams{Role: "composer", Sort: "name", Query: "Jay Chou", Page: 1, PageSize: 1})
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != id || list.Items[0].TrackCount != 1 {
		t.Fatalf("role/query list: %+v %v", list, err)
	}
	albums, err := f.store.ListArtistAlbums(ctx, id, 1, 50)
	if err != nil || len(albums.Items) != 1 || albums.Items[0].CreditedReleaseID != member.releaseID || albums.Items[0].Group.Representative.ID != representative.releaseID {
		t.Fatalf("actual credited member: %+v %v", albums, err)
	}
	tracks, err := f.store.ListArtistTracks(ctx, id, "composer", 1, 50)
	if err != nil || len(tracks.Items) != 2 || tracks.Pagination.Total != 2 {
		t.Fatalf("missing participation: %+v %v", tracks, err)
	}
	queue, err := f.store.LoadArtistQueue(ctx, id, "album", nil)
	if err != nil || queue.Total != 1 || queue.Returned != 1 || queue.MissingTotal != 1 || queue.Items[0].TrackID != track {
		t.Fatalf("dedup queue: %+v %v", queue, err)
	}
	for _, kind := range []string{"artists", "albums", "tracks"} {
		result, err := f.store.Search(ctx, SearchParams{Type: kind, Query: "Jay Chou", Page: 1, PageSize: 50})
		if err != nil || result.Pagination.Total != 1 {
			t.Fatalf("alias %s: %+v %v", kind, result, err)
		}
		var match SearchMatch
		switch kind {
		case "artists":
			match = result.Items.([]SearchArtistItem)[0].Match
		case "albums":
			item := result.Items.([]SearchAlbumItem)[0]
			match = item.Match
			if item.MatchedReleaseID != member.releaseID {
				t.Fatalf("alias release: %+v", item)
			}
		case "tracks":
			item := result.Items.([]SearchTrackItem)[0]
			match = item.Match
			if item.TrackID == sibling || item.TrackID != track {
				t.Fatalf("propagated to unrelated sibling: %+v", item)
			}
		}
		if match.Field != "artist_alias" || match.Alias == nil || *match.Alias != "Jay Chou" || match.ArtistID == nil || *match.ArtistID != id {
			t.Fatalf("alias match: %+v", match)
		}
	}
	direct, err := f.store.Search(ctx, SearchParams{Type: "tracks", Query: "周杰倫", Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	directMatch := direct.Items.([]SearchTrackItem)[0].Match
	if directMatch.Field == "artist_alias" || directMatch.ArtistID == nil || *directMatch.ArtistID != id || directMatch.Alias != nil {
		t.Fatalf("direct artist identity: %+v", directMatch)
	}
	suggestions, err := f.store.SearchSuggestions(ctx, "Jay Chou", 5)
	if err != nil || !reflect.DeepEqual(suggestions.AvailableTypes, []string{"artists", "albums", "tracks"}) || len(suggestions.Artists.Items) != 1 || len(suggestions.Tracks.Items) != 1 {
		t.Fatalf("suggestions: %+v %v", suggestions, err)
	}
	releases, err := f.store.ListReleases(ctx, ListParams{Search: "Jay Chou", Page: 1, PageSize: 50})
	if err != nil || releases.Total != 1 {
		t.Fatalf("legacy release route alias: %+v %v", releases, err)
	}
	groups, err := f.store.ListReleaseGroups(ctx, ListParams{Search: "Jay Chou", Sort: ReleaseGroupSortAddedAtDesc, Page: 1, PageSize: 50})
	if err != nil || groups.Total != 1 {
		t.Fatalf("added-at route alias: %+v %v", groups, err)
	}
	names, err := f.store.ListArtistNames(ctx, id, 10, 1)
	if err != nil || names.Pagination.Total != 3 || len(names.Items) != 0 {
		t.Fatalf("out of range names: %+v %v", names, err)
	}
}

func TestPostgreSQLArtistReadCanonicalPersonalAndSafety(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	release := f.release("Album")
	track := f.track(release, "Track", "present", 120)
	f.exec(`UPDATE tracks SET artist='周杰倫' WHERE id=$1::uuid`, track)
	publishArtistReadFixture(t, f)
	id := findArtistReadFixture(t, f, "周杰伦")
	old := uuid.New()
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'旧身份')`, old)
	f.exec(`INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version) VALUES($1::uuid,'旧身份','旧身份','旧身份','J',1)`, old)
	f.exec(`INSERT INTO artist_overrides(artist_id,redirect_artist_id) VALUES($1::uuid,$2::uuid)`, old, id)
	resolved, err := f.store.ResolveArtists(ctx, []string{old, strings.ToUpper(id), id, "bad", uuid.New()})
	if err != nil || len(resolved) != 2 || resolved[old] != id || resolved[id] != id {
		t.Fatalf("resolve: %v %v", resolved, err)
	}
	summaries, err := f.store.LoadArtistSummaries(ctx, []string{old, id})
	if err != nil || len(summaries) != 1 || summaries[id].ID != id {
		t.Fatalf("canonical summaries: %v %v", summaries, err)
	}
	detail, err := f.store.LoadArtist(ctx, old)
	if err != nil || detail.Artist.ID != old || detail.RedirectArtistID == nil || *detail.RedirectArtistID != id {
		t.Fatalf("redirect detail: %+v %v", detail, err)
	}
	queue, err := f.store.LoadArtistQueue(ctx, old, "album", nil)
	if err != nil || queue.ArtistID != id || len(queue.Items) != 1 {
		t.Fatalf("canonical queue: %+v %v", queue, err)
	}
	many := make([]string, 1001)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := f.store.ResolveArtists(ctx, many); err == nil {
		t.Fatal("unbounded personal artist read")
	}
	f.artwork(release, "../unsafe.jpg")
	if _, err := f.store.LoadArtistQueue(ctx, id, "album", nil); err == nil {
		t.Fatal("bad safe summary must fail whole queue")
	}
}

func TestPostgreSQLArtistQueueFullRandomBeforeLimit(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	release := f.release("Album")
	f.exec(`UPDATE releases SET artist='周杰伦',album_artist='周杰伦' WHERE id=$1::uuid`, release.releaseID)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT md5('artist-queue:'||i)::uuid,$1::uuid,i,'Track '||i,'',$2::uuid,'queue/'||i||'.flac','present',now(),'physical',60 FROM generate_series(1,10001) i`, release.mediumID, f.rootID)
	publishArtistReadFixture(t, f)
	postgrestest.AnalyzeSchema(t, ctx, f.database)
	id := findArtistReadFixture(t, f, "周杰伦")
	seed := "b0000000-0000-4000-8000-000000000001"
	started := time.Now()
	queue, err := f.store.LoadArtistQueue(ctx, id, "random", &seed)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	payload, err := json.Marshal(queue)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("完整10000首队列：读取=%s JSON字节=%d", elapsed, len(payload))
	if queue.Total != 10001 || queue.Returned != 10000 || !queue.Truncated || queue.MissingTotal != 0 || len(queue.Items) != 10000 {
		t.Fatalf("queue metadata: total=%d returned=%d truncated=%v", queue.Total, queue.Returned, queue.Truncated)
	}
	want, err := f.store.artistIDs(ctx, `SELECT id::text FROM tracks ORDER BY md5($1::text||':'||id::text),id LIMIT 10000`, seed)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(queue.Items))
	for i, item := range queue.Items {
		got[i] = item.TrackID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("random selection differs from full-candidate ordering")
	}
	again, err := f.store.LoadArtistQueue(ctx, id, "random", &seed)
	if err != nil {
		t.Fatal(err)
	}
	for i := range again.Items {
		if again.Items[i].TrackID != got[i] {
			t.Fatalf("seed changed at %d", i)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.store.LoadArtistQueue(cancelled, id, "album", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("queue cancellation: %v", err)
	}
	// 别名命中沿发行署名扩展，深页仍可越过 1,000 下界。
	result, err := f.store.Search(ctx, SearchParams{Type: "tracks", Query: "Jay Chou", Page: 21, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pagination.Total != 1000 || result.Pagination.TotalRelation != "gte" || !result.Pagination.HasMore || len(result.Items.([]SearchTrackItem)) != 50 {
		t.Fatalf("alias deep page: %s", fmt.Sprint(result.Pagination))
	}
}

func TestPostgreSQLArtistListCountsAndSearchDeepPages(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	first := f.release("First")
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
 SELECT md5('artist-pages:'||i)::uuid,$1::uuid,i,'Track '||i,'Solo '||lpad(i::text,4,'0'),$2::uuid,'pages/'||i||'.flac','present',now() FROM generate_series(1,1002) i`, first.mediumID, f.rootID)
	second := f.release("Second")
	extra := f.track(second, "Another role", "present", 120)
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','Solo 0001',1)`, extra)
	publishArtistReadFixture(t, f)
	postgrestest.AnalyzeSchema(t, ctx, f.database)
	result, err := f.store.Search(ctx, SearchParams{Type: "artists", Query: "Solo", Page: 21, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pagination.Total != 1000 || result.Pagination.TotalRelation != "gte" || result.Pagination.HasMore || len(result.Items.([]SearchArtistItem)) != 2 {
		t.Fatalf("artist deep page: %+v", result.Pagination)
	}
	result, err = f.store.Search(ctx, SearchParams{Type: "artists", Query: "Solo", Page: 22, PageSize: 50})
	if err != nil || result.Pagination.Total != 1000 || len(result.Items.([]SearchArtistItem)) != 0 {
		t.Fatalf("artist empty deep page: %+v %v", result.Pagination, err)
	}
	id := findArtistReadFixture(t, f, "solo 0001")
	filtered, err := f.store.ListArtists(ctx, ArtistListParams{Query: "Solo", Role: "composer", Sort: "album_count", Page: 1, PageSize: 50})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != id || filtered.Items[0].AlbumCount != 2 || filtered.Items[0].TrackCount != 2 {
		t.Fatalf("role filter must not redefine counts: %+v %v", filtered, err)
	}
	ordered, err := f.store.ListArtists(ctx, ArtistListParams{Query: "Solo", Sort: "album_count", Page: 1, PageSize: 50})
	if err != nil || ordered.Pagination.Total != 1002 || ordered.Items[0].ID != id {
		t.Fatalf("album count ordering: %+v %v", ordered, err)
	}
}

// filenameArtistFingerprinter 用于纯合成观察：没有物理文件，内容指纹保持未知。
type filenameArtistFingerprinter struct{}

func (filenameArtistFingerprinter) Fingerprint(context.Context, string) (string, error) {
	return "", nil
}

func TestPostgreSQLArtistReadFilenameInferredArtist(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	scanID := uuid.New()
	f.exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID)
	// 模拟无标签解析结果；艺人必须由 organizer 从文件名拆出，不能直接塞进艺人事实表。
	observation := ingest.SourceObservation{
		RelativePath: "Filename/示例艺人 - 示例曲.flac", Directory: "Filename",
		Title: "示例艺人 - 示例曲", Album: "Filename", Artist: "未知艺术家",
		TrackNumber: 1, DiscNumber: 1, SourceKind: "flac_vorbis_comment", Codec: "flac",
		InferredFields: map[string]bool{"title": true, "album": true, "artist": true, "track_number": true, "disc_number": true},
		FieldSources: map[string]string{
			"title": "filename_fallback", "album": "folder_fallback", "artist": "display_fallback",
			"track_number": "default_fallback", "disc_number": "default_fallback",
		},
	}
	if err := ingest.StageObservation(ctx, f.database, scanID, f.rootID, observation); err != nil {
		t.Fatal(err)
	}
	plan, err := ingest.PlanStagedRoot(ctx, f.database, scanID, f.rootID, ingest.PlanOptions{WalkComplete: true, Fingerprinter: filenameArtistFingerprinter{}})
	if err != nil || plan.ScopeFailures != 0 || plan.FingerprintFailures != 0 || plan.DeferredScopes != 0 {
		t.Fatalf("文件名观察规划失败: %+v %v", plan, err)
	}
	candidates := ingest.OrganizeObservations([]ingest.SourceObservation{observation})
	if len(candidates) != 1 {
		t.Fatalf("文件名观察应只有一个候选: %+v", candidates)
	}
	releaseID, err := ingest.PersistCandidate(ctx, f.database, scanID, f.rootID, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ingest.ClearStagedObservations(ctx, f.database, scanID, f.rootID); err != nil {
		t.Fatal(err)
	}
	var trackID, title, artist, source string
	var inferred bool
	if err := f.database.QueryRow(`SELECT t.id::text,t.title,t.artist,o.source_kind,o.inferred
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN track_observations o ON o.track_id=t.id AND o.field_name='artist'
 WHERE m.release_id=$1::uuid`, releaseID).Scan(&trackID, &title, &artist, &source, &inferred); err != nil {
		t.Fatal(err)
	}
	if title != "示例曲" || artist != "示例艺人" || source != "filename_fallback" || !inferred {
		t.Fatalf("文件名艺人及其推断证据未写入: 标题=%q 艺人=%q 来源=%q inferred=%t", title, artist, source, inferred)
	}
	// 复用现有发布流程，在 CATP 内真实刷新艺人图后再读公开摘要。
	publishArtistReadFixture(t, f)
	id := findArtistReadFixture(t, f, "示例艺人")
	detail, err := f.store.LoadArtist(ctx, id)
	if err != nil || detail.Artist.ID != id || detail.Artist.Name != "示例艺人" || !detail.Artist.Available || detail.Artist.TrackCount != 1 || detail.Artist.AlbumCount != 1 {
		t.Fatalf("文件名艺人未成为可读取的实体: %+v %v", detail, err)
	}
	summaries, err := f.store.LoadTrackSummaries(ctx, []string{trackID})
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := summaries[trackID]
	if !ok || !summary.Present || summary.Title != "示例曲" || summary.Artist != "示例艺人" {
		t.Fatalf("文件名曲目不可读取: %+v", summaries)
	}
	assertArtistRefs(t, summary.ArtistRefs, "track_artist", "示例艺人", "示例艺人", id)
	var placeholders int
	if err := f.database.QueryRow(`SELECT COUNT(*) FROM artist_name_keys WHERE normalized_key='未知艺术家'`).Scan(&placeholders); err != nil {
		t.Fatal(err)
	}
	if placeholders != 0 {
		t.Fatal("无标签占位艺人不应被建成实体")
	}
}

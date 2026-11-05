package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func artistRefsReady(f *placementFixture) {
	f.exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,
 published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
}

// 直接构造已发布事实，测试读取合同，不依赖并行实现中的写者或后台回填。
func artistRefsFact(t *testing.T, f *placementFixture, releaseID, trackID, role, name, artistID, display string) {
	t.Helper()
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,$2) ON CONFLICT DO NOTHING`, artistID, display)
	f.exec(`INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version)
 VALUES($1::uuid,$2,$2,$2,'A',1) ON CONFLICT DO NOTHING`, artistID, display)
	sum := sha256.Sum256([]byte(name))
	var keyID, nameID string
	err := f.database.QueryRow(`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
 VALUES($1::uuid,1,$2,$3,$4::uuid) ON CONFLICT(normalization_version,key_hash) DO UPDATE SET normalized_key=EXCLUDED.normalized_key RETURNING id::text`, uuid.New(), name, sum[:], artistID).Scan(&keyID)
	if err != nil {
		t.Fatal(err)
	}
	err = f.database.QueryRow(`INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin) VALUES($1::uuid,$2::uuid,$3,$4,'scan')
 ON CONFLICT(key_id,spelling_hash) DO UPDATE SET spelling=EXCLUDED.spelling RETURNING id::text`, uuid.New(), keyID, name, sum[:]).Scan(&nameID)
	if err != nil {
		t.Fatal(err)
	}
	creditID := uuid.New()
	f.exec(`INSERT INTO artist_credits(id,release_id,track_id,role,name_id,position,layer)
 VALUES($1::uuid,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5::uuid,1,'scan')`, creditID, releaseID, trackID, role, nameID)
	f.exec(`INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position)
 VALUES($1::uuid,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5::uuid,$6::uuid,1)`, creditID, releaseID, trackID, role, nameID, artistID)
}

func assertArtistRefs(t *testing.T, refs []ArtistRef, role, name, display string, ids ...string) {
	t.Helper()
	if refs == nil || len(refs) != len(ids) {
		t.Fatalf("引用数量或 null 不符: %+v, want %v", refs, ids)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, ref := range refs {
		if ref.Name != name || ref.DisplayName != display || !want[ref.ArtistID] {
			t.Fatalf("引用角色、原名、主名或作用域错误: %+v, want %s/%s/%s/%v", ref, role, name, display, ids)
		}
		delete(want, ref.ArtistID)
	}
}

func TestPostgreSQLArtistRefsSharedProjections(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	r := f.release("引用专辑")
	track := f.track(r, "存在曲目", "present", 100)
	missing := f.track(r, "缺失曲目", "missing", 90)
	id := uuid.New()
	split := uuid.New()
	f.exec(`UPDATE releases SET artist='Jay Chou',album_artist='Jay Chou' WHERE id=$1::uuid`, r.releaseID)
	f.exec(`UPDATE tracks SET artist='Jay Chou' WHERE id=ANY($1::uuid[])`, []string{track, missing})
	for _, role := range []string{"release_artist", "album_artist"} {
		artistRefsFact(t, f, r.releaseID, "", role, "Jay Chou", id, "周杰伦")
	}
	artistRefsFact(t, f, "", track, "track_artist", "Jay Chou", id, "周杰伦")
	artistRefsFact(t, f, "", missing, "track_artist", "Jay Chou", split, "周杰伦")
	// 未发布时即使图里已有数据，也只能返回原文与空数组。
	preparing, err := f.store.LoadTrackSummaries(ctx, []string{track, missing})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range preparing {
		assertArtistRefs(t, summary.ArtistRefs, "track_artist", "Jay Chou", "周杰伦")
		assertArtistRefs(t, summary.Release.ArtistRefs, "release_artist", "Jay Chou", "周杰伦")
		assertArtistRefs(t, summary.Release.AlbumArtistRefs, "album_artist", "Jay Chou", "周杰伦")
	}
	artistRefsReady(f)
	q := &personalCountingQueryer{Queryer: f.database}
	tracks, err := NewStore(q).LoadTrackSummaries(ctx, []string{track, missing})
	if err != nil {
		t.Fatal(err)
	}
	if q.queries != 1 {
		t.Fatalf("批量摘要发生额外查询: %d", q.queries)
	}
	assertArtistRefs(t, tracks[track].ArtistRefs, "track_artist", "Jay Chou", "周杰伦", id)
	assertArtistRefs(t, tracks[missing].ArtistRefs, "track_artist", "Jay Chou", "周杰伦", split)
	summary, err := f.store.LoadReleaseSummary(ctx, r.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	assertArtistRefs(t, summary.ArtistRefs, "release_artist", "Jay Chou", "周杰伦", id)
	assertArtistRefs(t, summary.AlbumArtistRefs, "album_artist", "Jay Chou", "周杰伦", id)
	if !reflect.DeepEqual(summary, tracks[missing].Release) {
		t.Fatalf("曲目摘要与共享发行摘要不一致: %+v", tracks[missing].Release)
	}
	groups, err := f.store.LoadReleaseGroupSummaries(ctx, []string{r.groupID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary, *groups[r.groupID].Representative) {
		t.Fatal("个人收藏组摘要丢失 refs")
	}
	group, err := f.store.LoadReleaseGroup(ctx, r.groupID)
	if err != nil || !reflect.DeepEqual(summary, group.Representative) {
		t.Fatalf("组摘要丢失 refs: %v", err)
	}
	placements, err := f.store.LoadTrackPlacements(ctx, []string{track})
	if err != nil || !reflect.DeepEqual(summary, placements[track].Release) {
		t.Fatalf("播放历史摘要丢失 refs: %v", err)
	}
	list, err := f.store.ListReleases(ctx, ListParams{Page: 1, PageSize: 10})
	if err != nil || len(list.Items) != 1 || !reflect.DeepEqual(summary, list.Items[0]) {
		t.Fatalf("列表摘要丢失 refs: %v", err)
	}
	searchTracks, err := f.store.completeSearch(ctx, "tracks", []searchHit{{ID: track}})
	if err != nil {
		t.Fatal(err)
	}
	assertArtistRefs(t, searchTracks.([]SearchTrackItem)[0].ArtistRefs, "track_artist", "Jay Chou", "周杰伦", id)
	searchAlbums, err := f.store.completeSearch(ctx, "albums", []searchHit{{ID: r.groupID, MatchedReleaseID: r.releaseID}})
	if err != nil || !reflect.DeepEqual(summary, searchAlbums.([]SearchAlbumItem)[0].Group.Representative) {
		t.Fatalf("搜索结果补全丢失 refs: %v", err)
	}
	media, err := f.store.LoadReleaseMedia(ctx, r.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	assertArtistRefs(t, media[0].Tracks[0].ArtistRefs, "track_artist", "Jay Chou", "周杰伦", id)
	data, err := json.Marshal(tracks[track])
	if err != nil || !strings.Contains(string(data), `"name":"Jay Chou"`) || !strings.Contains(string(data), `"display_name":"周杰伦"`) {
		t.Fatalf("JSON 名字被重写: %s, %v", data, err)
	}
}

func TestPostgreSQLArtistRefsCreditsAndSnapshot(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	r := f.release("署名")
	a := f.track(r, "一", "present", 100)
	b := f.track(r, "二", "present", 100)
	first, second, other := uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','同名',1),($1::uuid,'performer','同名',2),($1::uuid,'composer','另一人',3),($2::uuid,'composer','同名',1)`, a, b)
	f.exec(`INSERT INTO release_credits(release_id,role,name,position) VALUES($1::uuid,'composer','同名',1),($1::uuid,'album_artist','甲 / 乙',2)`, r.releaseID)
	f.exec(`UPDATE releases SET album_artist='甲 / 乙' WHERE id=$1::uuid`, r.releaseID)
	for _, item := range []struct{ track, role, name, id string }{{a, "composer", "同名", first}, {b, "composer", "同名", second}, {a, "performer", "同名", other}, {a, "composer", "另一人", other}} {
		artistRefsFact(t, f, "", item.track, item.role, item.name, item.id, "主显示名")
	}
	artistRefsFact(t, f, r.releaseID, "", "album_artist", "甲", first, "主显示名")
	artistRefsFact(t, f, r.releaseID, "", "album_artist", "乙", second, "主显示名")
	artistRefsReady(f)
	media, err := f.store.LoadReleaseMedia(ctx, r.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, medium := range media {
		for _, track := range medium.Tracks {
			for _, credit := range track.Credits {
				id := other
				if credit.Role == "composer" && credit.Name == "同名" {
					id = first
					if track.ID == b {
						id = second
					}
				}
				assertArtistRefs(t, credit.ArtistRefs, credit.Role, credit.Name, "主显示名", id)
			}
		}
	}
	credits, err := f.store.LoadReleaseCredits(ctx, r.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	assertArtistRefs(t, credits[0].ArtistRefs, "composer", "同名", "主显示名", first, second)
	if len(credits[1].ArtistRefs) != 2 || credits[1].ArtistRefs[0].Name != "乙" || credits[1].ArtistRefs[1].Name != "甲" {
		t.Fatalf("合成专辑署名未保留各原名: %+v", credits[1])
	}
	// 显式快照已建立后，另一连接切换状态并删图；旧快照仍读完整旧图，新快照必须只读空引用。
	tx, err := f.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot := NewStore(tx)
	before, err := snapshot.LoadReleaseCredits(ctx, r.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE catalog_projection_state SET status='preparing',manifest_sealed=false WHERE capability='artists'`)
	f.exec(`DELETE FROM artist_effective_credits`)
	after, err := snapshot.LoadReleaseCredits(ctx, r.releaseID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("读取跨快照半图: %v", err)
	}
	fresh, err := f.store.LoadReleaseCredits(ctx, r.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, credit := range fresh {
		if credit.ArtistRefs == nil || len(credit.ArtistRefs) != 0 {
			t.Fatal("重建中暴露 refs")
		}
	}
}

func TestPostgreSQLArtistRefsFailClosedAndVersionGate(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	r := f.release("上限")
	track := f.track(r, "曲目", "present", 10)
	for i := 0; i < 101; i++ {
		artistRefsFact(t, f, "", track, "track_artist", fmt.Sprintf("署名%03d", i), uuid.New(), "主名")
	}
	artistRefsReady(f)
	if _, err := f.store.LoadTrackSummaries(ctx, []string{track}); err == nil {
		t.Fatal("超过100引用必须失败，不能截断")
	}
	f.exec(`UPDATE catalog_projection_state SET textnorm_version=2,published_textnorm_version=2 WHERE capability='artists'`)
	got, err := f.store.LoadTrackSummaries(ctx, []string{track})
	if err != nil || got[track].ArtistRefs == nil || len(got[track].ArtistRefs) != 0 {
		t.Fatalf("版本不匹配不得读取坏图: %v", err)
	}
	f.exec(`UPDATE catalog_projection_state SET textnorm_version=1,published_textnorm_version=1 WHERE capability='artists'`)
	f.exec(`DELETE FROM artist_effective_credits WHERE credit_id=(SELECT credit_id FROM artist_effective_credits ORDER BY credit_id LIMIT 1)`)
	bounded, err := f.store.LoadTrackSummaries(ctx, []string{track})
	if err != nil || len(bounded[track].ArtistRefs) != 100 {
		t.Fatalf("100 项应通过: %v", err)
	}
	f.exec(`DELETE FROM artist_effective_credits WHERE credit_id NOT IN (SELECT credit_id FROM artist_effective_credits ORDER BY credit_id LIMIT 1)`)
	f.exec(`DELETE FROM artist_profiles`)
	if _, err := f.store.LoadTrackSummaries(ctx, []string{track}); err == nil {
		t.Fatal("ready时缺主名投影不得静默丢引用")
	}
}

func TestPostgreSQLArtistRefsReadyBatchPlan(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	r := f.release("批量引用")
	seed := f.track(r, "种子", "present", 100)
	artist := uuid.New()
	artistRefsFact(t, f, "", seed, "track_artist", "原署名", artist, "主名")
	for _, role := range []string{"release_artist", "album_artist"} {
		artistRefsFact(t, f, r.releaseID, "", role, "原署名", artist, "主名")
	}
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT md5('refs-batch-track-'||i)::uuid,$1::uuid,i+1,'合成曲目'||i,'原署名',1,$2::uuid,'refs-'||i||'.flac','present',NOW(),'physical',100 FROM generate_series(1,1000) i`, r.mediumID, f.rootID)
	f.exec(`INSERT INTO artist_credits(id,track_id,role,name_id,position,layer)
 SELECT gen_random_uuid(),t.id,'track_artist',c.name_id,1,'scan' FROM tracks t CROSS JOIN artist_credits c
 WHERE c.track_id=$1::uuid AND t.id<>$1::uuid`, seed)
	f.exec(`INSERT INTO artist_effective_credits(credit_id,track_id,role,name_id,artist_id,position)
 SELECT c.id,c.track_id,c.role,c.name_id,$1::uuid,c.position FROM artist_credits c WHERE c.track_id<>$2::uuid`, artist, seed)
	for _, table := range []string{"tracks", "media", "releases", "release_corrections", "release_artworks", "release_issue_reviews", "release_field_decisions", "release_grouping_evidence", "artist_credits", "artist_effective_credits", "artist_names", "artist_profiles"} {
		f.exec("ANALYZE " + table)
	}
	artistRefsReady(f)
	rows, err := f.database.Query(`SELECT id::text FROM tracks WHERE id<>$1::uuid ORDER BY id`, seed)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	q := &personalCountingQueryer{Queryer: f.database}
	summaries, err := NewStore(q).LoadTrackSummaries(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1000 || q.queries != 1 {
		t.Fatalf("批量形状不符: %d, %d", len(summaries), q.queries)
	}
	for _, summary := range summaries {
		assertArtistRefs(t, summary.ArtistRefs, "track_artist", "原署名", "主名", artist)
		assertArtistRefs(t, summary.Release.ArtistRefs, "release_artist", "原署名", "主名", artist)
	}
	var raw []byte
	if err := f.database.QueryRowContext(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) `+loadTrackSummariesSQL, ids).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plans []map[string]any
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	if plans[0]["JIT"] != nil {
		t.Fatalf("有界批量 refs 不应引入 JIT: %s", raw)
	}
	t.Logf("ready 图：1000 曲目/同一发行，execution_ms=%v，JIT=%v", plans[0]["Execution Time"], plans[0]["JIT"])
}

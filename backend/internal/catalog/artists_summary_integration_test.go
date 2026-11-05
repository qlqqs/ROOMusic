package catalog

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// 摘要专用查询必须与完整参与关系等价：跨名字/角色/发行去重，但不遗漏角色或当前归组。
func TestPostgreSQLArtistSummaryMatchesParticipation(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	first := f.release("First")
	second := f.release("Second")
	f.exec(`UPDATE releases SET artist='周杰倫',album_artist='Jay Chou' WHERE id=$1::uuid`, first.releaseID)
	f.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, second.releaseID, first.groupID)
	track := f.track(first, "First track", "present", nil)
	inherited := f.track(first, "Inherited only", "present", nil)
	missing := f.track(first, "Missing", "missing", nil)
	otherTrack := f.track(second, "Other track", "present", nil)
	f.exec(`UPDATE tracks SET artist='周杰倫 / Jay Chou / Other Artist' WHERE id=$1::uuid`, track)
	f.exec(`UPDATE tracks SET artist='Other Artist' WHERE id=$1::uuid`, otherTrack)
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES
 ($1::uuid,'composer','周杰倫',1),($1::uuid,'composer','Jay Chou',2),
 ($1::uuid,'conductor','周杰倫',1),($1::uuid,'performer','Other Artist',1),
 ($2::uuid,'composer','周杰倫',1),($3::uuid,'producer','周杰倫',1),
 ($3::uuid,'composer','Missing Only',1)`, track, otherTrack, missing)
	publishArtistReadFixture(t, f)
	ids := []string{
		findArtistReadFixture(t, f, "周杰伦"),
		findArtistReadFixture(t, f, "other artist"),
		findArtistReadFixture(t, f, "missing only"),
	}
	check := func() map[string]ArtistSummary {
		t.Helper()
		actual, err := f.store.LoadArtistSummaries(ctx, ids)
		if err != nil {
			t.Fatal(err)
		}
		// 原来的共享参与关系仍用于详情/参与专辑；将它作为独立的完整关系对照。
		rows, err := f.database.QueryContext(ctx, `WITH `+artistParticipationCTE+`, counts AS (
 SELECT artist_id,count(DISTINCT group_id) FILTER (WHERE present) AS albums,
 count(DISTINCT track_id) FILTER (WHERE present) AS tracks FROM artist_participation GROUP BY artist_id
 ) SELECT a.id::text,p.effective_display_name,p.initial_group,COALESCE(c.albums,0),COALESCE(c.tracks,0),
 to_json(ARRAY(SELECT role FROM (SELECT DISTINCT role FROM artist_participation ap WHERE ap.artist_id=a.id AND present) roles ORDER BY `+artistRoleOrder+`))
 FROM artists a JOIN artist_profiles p ON p.artist_id=a.id LEFT JOIN counts c ON c.artist_id=a.id
 WHERE a.id=ANY($1::uuid[])`, ids)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		expected := map[string]ArtistSummary{}
		for rows.Next() {
			var item ArtistSummary
			var roles []byte
			if err := rows.Scan(&item.ID, &item.Name, &item.InitialGroup, &item.AlbumCount, &item.TrackCount, &roles); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(roles, &item.Roles); err != nil {
				t.Fatal(err)
			}
			item.Available = item.TrackCount > 0
			expected[item.ID] = item
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("摘要与参与关系不等价：got=%+v want=%+v", actual, expected)
		}
		return actual
	}
	got := check()
	if got[ids[0]].AlbumCount != 1 || got[ids[0]].TrackCount != 3 || !reflect.DeepEqual(got[ids[0]].Roles, []string{"release_artist", "album_artist", "track_artist", "composer", "conductor"}) || got[ids[1]].TrackCount != 2 || got[ids[2]].Available || len(got[ids[2]].Roles) != 0 {
		t.Fatalf("夹具必须覆盖跨角色去重、发行传播、同组多版本和缺失角色：%+v", got)
	}
	// 恢复缺失轨后，先前只出现在缺失轨上的身份与角色也必须立即出现。
	f.exec(`UPDATE tracks SET source_status='present' WHERE id=$1::uuid`, missing)
	got = check()
	if got[ids[0]].TrackCount != 4 || !reflect.DeepEqual(got[ids[0]].Roles, []string{"release_artist", "album_artist", "track_artist", "composer", "conductor", "producer"}) || !got[ids[2]].Available {
		t.Fatalf("恢复轨目未恢复身份或角色：%+v", got)
	}
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, missing)
	// 不刷新投影：当前归组与状态必须立即反映在摘要中。
	f.exec(`UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, second.releaseID, second.groupID)
	got = check()
	if got[ids[0]].AlbumCount != 2 || got[ids[1]].AlbumCount != 2 {
		t.Fatalf("未读取当前归组：%+v", got)
	}
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id=ANY($1::uuid[])`, []string{track, inherited})
	check()
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, otherTrack)
	got = check()
	for _, item := range got {
		if item.Available || item.TrackCount != 0 || item.AlbumCount != 0 || len(item.Roles) != 0 {
			t.Fatalf("全缺失身份仍须返回零计数与空角色：%+v", item)
		}
	}
}

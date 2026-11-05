package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistI2AlbumsAnyParticipation(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	representative := f.release("A 代表%_~")
	f.track(representative, "无关曲目", "present", 120)
	member := f.release("Z 别版")
	track := f.track(member, "参与者曲目", "present", 120)
	f.exec(`UPDATE releases SET group_id=$1::uuid,artist='参与者',album_artist='参与者' WHERE id=$2::uuid`, representative.groupID, member.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, representative.releaseID, representative.groupID)
	f.exec(`UPDATE tracks SET artist='参与者' WHERE id=$1::uuid`, track)
	for _, role := range []string{"composer", "conductor", "performer", "producer"} {
		f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,'参与者',1)`, track, role)
	}
	composer := f.release("B 作曲专辑")
	composerTrack := f.track(composer, "独立作品", "present", 120)
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','参与者',1),($1::uuid,'performer','参与者',1)`, composerTrack)
	releaseOnly := f.release("C 发行署名")
	f.track(releaseOnly, "曲目", "present", 120)
	f.exec(`UPDATE releases SET artist='参与者' WHERE id=$1::uuid`, releaseOnly.releaseID)
	// missing 署名不能借同发行/同组的其他 present 曲目变成可见参与。
	missingTrack := f.track(representative, "缺失参与", "missing", 120)
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','缺失参与者',1)`, missingTrack)
	missing := f.release("D 隐藏%_~")
	missingOnly := f.track(missing, "缺失", "missing", 120)
	f.exec(`UPDATE tracks SET artist='参与者' WHERE id=$1::uuid`, missingOnly)
	publishArtistReadFixture(t, f)
	id := findArtistReadFixture(t, f, "参与者")

	legacy, err := f.store.ListArtistAlbums(ctx, id, 1, 50)
	if err != nil || legacy.Pagination.Total != 1 || len(legacy.Items) != 1 || legacy.Items[0].CreditedReleaseID != member.releaseID || legacy.Items[0].Group.Representative.ID != representative.releaseID {
		t.Fatalf("默认行为发生变化: %+v %v", legacy, err)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(encoded), `"roles":`) {
		t.Fatalf("默认响应不应增加角色: %s %v", encoded, err)
	}
	anyOption := ArtistAlbumOptions{Participation: "any"}
	all, err := f.store.ListArtistAlbums(ctx, id, 1, 50, anyOption)
	if err != nil || all.Pagination.Total != 3 || len(all.Items) != 3 {
		t.Fatalf("全角色专辑: %+v %v", all, err)
	}
	expected := []struct {
		group, release string
		roles          []string
	}{
		{representative.groupID, member.releaseID, []string{"release_artist", "album_artist", "track_artist", "composer", "conductor", "performer", "producer"}},
		{composer.groupID, composer.releaseID, []string{"composer", "performer"}},
		{releaseOnly.groupID, releaseOnly.releaseID, []string{"release_artist"}},
	}
	for i, want := range expected {
		got := all.Items[i]
		if got.Group.ID != want.group || got.CreditedReleaseID != want.release || !reflect.DeepEqual(got.Roles, want.roles) {
			t.Fatalf("专辑/角色/顺序不符: %+v want %+v", got, want)
		}
	}
	page, err := f.store.ListArtistAlbums(ctx, id, 2, 1, anyOption)
	if err != nil || page.Pagination.Total != 3 || len(page.Items) != 1 || page.Items[0].Group.ID != composer.groupID {
		t.Fatalf("分页: %+v %v", page, err)
	}
	empty, err := f.store.ListArtistAlbums(ctx, id, 4, 1, anyOption)
	if err != nil || empty.Pagination.Total != 3 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("越界分页: %+v %v", empty, err)
	}
	for _, query := range []string{"%", "_", "~", "%_~", "别版"} {
		filtered, err := f.store.ListArtistAlbums(ctx, id, 1, 1, ArtistAlbumOptions{Participation: "any", Query: query})
		if err != nil || filtered.Pagination.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Group.ID != representative.groupID || !reflect.DeepEqual(filtered.Items[0].Roles, expected[0].roles) {
			t.Fatalf("字面标题筛选 %q: %+v %v", query, filtered, err)
		}
	}
	for _, query := range []string{"参与者曲目", "参与者", "隐藏", "无匹配"} {
		filtered, err := f.store.ListArtistAlbums(ctx, id, 1, 50, ArtistAlbumOptions{Participation: "any", Query: query})
		if err != nil || filtered.Pagination.Total != 0 || len(filtered.Items) != 0 {
			t.Fatalf("非专辑标题不应命中 %q: %+v %v", query, filtered, err)
		}
	}
	absent, err := f.store.ListArtistAlbums(ctx, findArtistReadFixture(t, f, "缺失参与者"), 1, 50, anyOption)
	if err != nil || absent.Pagination.Total != 0 || len(absent.Items) != 0 {
		t.Fatalf("缺失署名借用可见兄弟曲目: %+v %v", absent, err)
	}
	if _, err := f.store.ListArtistAlbums(ctx, uuid.New(), 1, 50, anyOption); !errors.Is(err, ErrArtistNotFound) {
		t.Fatalf("未知身份: %v", err)
	}
	f.exec(`UPDATE catalog_projection_state SET status='preparing' WHERE capability='artists'`)
	if _, err := f.store.ListArtistAlbums(ctx, id, 1, 50, anyOption); !errors.Is(err, ErrArtistCatalogPreparing) {
		t.Fatalf("整理状态: %v", err)
	}
}

func TestPostgreSQLArtistI6AlbumArtistRoleUnion(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	release := f.release("角色测试")
	track := f.track(release, "曲目", "present", 120)
	f.exec(`UPDATE releases SET artist='发行独有 / 共同参与',album_artist='专辑独有 / 共同参与' WHERE id=$1::uuid`, release.releaseID)
	f.exec(`UPDATE tracks SET artist='曲目独有' WHERE id=$1::uuid`, track)
	hidden := f.release("不可见发行")
	f.track(hidden, "缺失", "missing", 120)
	f.exec(`UPDATE releases SET artist='仅缺失发行' WHERE id=$1::uuid`, hidden.releaseID)
	publishArtistReadFixture(t, f)
	releaseID := findArtistReadFixture(t, f, "发行独有")
	albumID := findArtistReadFixture(t, f, "专辑独有")
	bothID := findArtistReadFixture(t, f, "共同参与")
	for _, tc := range []struct {
		role string
		ids  []string
	}{{"album_artist", []string{releaseID, albumID, bothID}}, {"release_artist", []string{releaseID, bothID}}} {
		got, err := f.store.ListArtists(ctx, ArtistListParams{Role: tc.role, Page: 1, PageSize: 50})
		if err != nil || got.Pagination.Total != int64(len(tc.ids)) || len(got.Items) != len(tc.ids) {
			t.Fatalf("角色 %s: %+v %v", tc.role, got, err)
		}
		actual := map[string]bool{}
		for _, artist := range got.Items {
			actual[artist.ID] = true
		}
		for _, id := range tc.ids {
			if !actual[id] {
				t.Fatalf("角色并集缺少 %s: %+v", id, got)
			}
		}
		page, err := f.store.ListArtists(ctx, ArtistListParams{Role: tc.role, Page: 2, PageSize: 1})
		if err != nil || page.Pagination.Total != int64(len(tc.ids)) || len(page.Items) != 1 || page.Items[0].ID != got.Items[1].ID {
			t.Fatalf("去重后分页: %+v %v", page, err)
		}
	}
}

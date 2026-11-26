package catalog

import (
	"slices"
	"strings"
	"testing"
)

func TestPostgreSQLGroupingAlbumListsUseEffectiveRepresentativeSort(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx := t.Context()
	beach := f.visibleRelease("Beach Boys")
	beatles := f.visibleRelease("Zebra")
	tied := f.visibleRelease("Beatles The")
	les := f.visibleRelease("Les amis")
	theatre := f.visibleRelease("Theatre")
	alternate := f.visibleRelease("Aardvark")
	f.correct(beatles.releaseID, "title", "The Béa!tles")
	f.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, beatles.groupID, alternate.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$2::uuid WHERE id=$1::uuid`, beatles.groupID, beatles.releaseID)
	f.exec(`UPDATE releases SET artist='Anchor',album_artist='Anchor',created_at='2020-01-01'::timestamptz`)
	f.exec(`UPDATE releases SET created_at='2025-01-01'::timestamptz WHERE id=$1::uuid`, theatre.releaseID)
	publishArtistReadFixture(t, f.placementFixture)

	byRelease := []placementRelease{beatles, tied}
	slices.SortFunc(byRelease, func(a, b placementRelease) int { return strings.Compare(a.releaseID, b.releaseID) })
	wantReleases := []string{alternate.releaseID, beach.releaseID, byRelease[0].releaseID, byRelease[1].releaseID, les.releaseID, theatre.releaseID}
	wantGroups := []string{beach.groupID, byRelease[0].groupID, byRelease[1].groupID, les.groupID, theatre.groupID}
	byGroup := []string{beatles.groupID, tied.groupID}
	slices.Sort(byGroup)
	wantBrowse := []string{beach.groupID, byGroup[0], byGroup[1], les.groupID, theatre.groupID}
	var gotReleases, gotGroups, gotBrowse, gotArtistAlbums []string
	artist := findArtistReadFixture(t, f.placementFixture, "anchor")
	for i := 0; i < len(wantReleases); i++ {
		page, err := f.store.ListReleases(ctx, ListParams{Page: i + 1, PageSize: 1})
		if err != nil || page.Total != int64(len(wantReleases)) || len(page.Items) != 1 {
			t.Fatalf("发行第 %d 页 = %+v，错误 %v", i+1, page, err)
		}
		gotReleases = append(gotReleases, page.Items[0].ID)
		if i >= len(wantGroups) {
			continue
		}
		groups, err := f.store.ListReleaseGroups(ctx, ListParams{Page: i + 1, PageSize: 1})
		if err != nil || groups.Total != int64(len(wantGroups)) || len(groups.Items) != 1 {
			t.Fatalf("专辑组第 %d 页 = %+v，错误 %v", i+1, groups, err)
		}
		item := groups.Items[0]
		if item.ID == beatles.groupID && (item.Representative.ID != beatles.releaseID || item.Representative.Title != "The Béa!tles") {
			t.Errorf("专辑组未按修正后的代表版本显示：%+v", item)
		}
		gotGroups = append(gotGroups, item.ID)
		browse, err := f.store.ListBrowseAlbums(ctx, BrowseAlbumParams{BrowsePage: BrowsePage{Limit: 1, Offset: i}, Sort: BrowseAlbumSortTitle})
		if err != nil || len(browse) != 1 {
			t.Fatalf("兼容专辑浏览第 %d 页 = %+v，错误 %v", i+1, browse, err)
		}
		gotBrowse = append(gotBrowse, browse[0].ID)
		artistAlbums, err := f.store.ListArtistAlbums(ctx, artist, i+1, 1)
		if err != nil || len(artistAlbums.Items) != 1 {
			t.Fatalf("艺人专辑第 %d 页 = %+v，错误 %v", i+1, artistAlbums, err)
		}
		gotArtistAlbums = append(gotArtistAlbums, artistAlbums.Items[0].Group.ID)
	}
	for _, tc := range []struct {
		name      string
		got, want []string
	}{
		{"发行", gotReleases, wantReleases},
		{"组", gotGroups, wantGroups},
		{"兼容浏览", gotBrowse, wantBrowse},
		{"艺人专辑", gotArtistAlbums, wantBrowse},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s分页排序 = %v，期望 %v", tc.name, tc.got, tc.want)
		}
	}
	newest, err := f.store.ListReleaseGroups(ctx, ListParams{Sort: ReleaseGroupSortAddedAtDesc, Page: 1, PageSize: 1})
	if err != nil || len(newest.Items) != 1 || newest.Items[0].ID != theatre.groupID {
		t.Errorf("加入时间排序第一张 = %+v，期望 Theatre，错误 %v", newest, err)
	}
}

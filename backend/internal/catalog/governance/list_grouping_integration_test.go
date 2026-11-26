package governance

import (
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

func TestPostgreSQLGroupingArtistListSortRenameAndUndo(t *testing.T) {
	f := newArtistFixture(t)
	ctx := t.Context()
	_, _, beach, _ := addArtistRevertRelease(t, f, "Béa.ch Boys")
	_, _, beatles, _ := addArtistRevertRelease(t, f, "The Beatles")
	_, _, tied, _ := addArtistRevertRelease(t, f, "Beatles The")
	_, _, les, _ := addArtistRevertRelease(t, f, "Les amis")
	_, _, theatre, _ := addArtistRevertRelease(t, f, "Theatre")
	tiedIDs := []string{beatles, tied}
	slices.Sort(tiedIDs)

	assertPageOrder := func(initial string, want ...string) {
		t.Helper()
		var got []string
		for i := range want {
			page, err := f.store.ListArtists(ctx, catalog.ArtistListParams{Initial: initial, Page: i + 1, PageSize: 1})
			if err != nil || len(page.Items) != 1 || page.Pagination.Total != int64(len(want)) {
				t.Fatalf("首字母 %q 第 %d 页 = %+v，期望总数 %d，错误 %v", initial, i+1, page, len(want), err)
			}
			if initial != "" && page.Items[0].InitialGroup != initial {
				t.Errorf("按 %s 查到的艺人分栏为 %s", initial, page.Items[0].InitialGroup)
			}
			got = append(got, page.Items[0].ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("首字母 %q 分页顺序 = %v，期望 %v", initial, got, want)
		}
	}
	assertOriginalNameSearch := func(initial, name string) {
		t.Helper()
		page, err := f.store.ListArtists(ctx, catalog.ArtistListParams{Query: "The Beatles", Initial: initial, Page: 1, PageSize: 10})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != beatles || page.Items[0].Name != name {
			t.Errorf("查询原名 The Beatles，分栏 %s，结果 = %+v，错误 %v", initial, page, err)
		}
	}
	assertPageOrder("", f.alpha, beach, tiedIDs[0], tiedIDs[1], f.beta, les, theatre)
	assertPageOrder("B", beach, tiedIDs[0], tiedIDs[1], f.beta)
	assertOriginalNameSearch("B", "The Beatles")
	renamed := "The Àardvark"
	rename := mustArtistRename(t, f, beatles, &renamed)
	assertPageOrder("", beatles, f.alpha, beach, tied, f.beta, les, theatre)
	assertPageOrder("B", beach, tied, f.beta)
	assertOriginalNameSearch("A", renamed)
	mustArtistUndo(t, f, operations.TypeArtistRename, rename)
	assertPageOrder("B", beach, tiedIDs[0], tiedIDs[1], f.beta)
	assertOriginalNameSearch("B", "The Beatles")
}

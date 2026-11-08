package maintenance

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestPostgreSQLSearchLooseBackfill(t *testing.T) {
	f := newFixture(t, 1)
	f.exec(`UPDATE releases SET title='AC/DC',artist='ac/dc',album_artist='ac/dc' WHERE id=$1::uuid`, f.release)
	f.run()
	previous, previousArtists := f.state(), f.artistState()
	keysBefore := f.count(`SELECT count(*) FROM artist_name_keys`)
	if !previous.Ready() || !previousArtists.Ready() || keysBefore == 0 {
		t.Fatal("升级前夹具未完成发布或缺少名字键")
	}
	// 模拟旧版本已发布的投影：只有去标点派生缺失，不修改身份键或规范化版本。
	f.exec(`UPDATE catalog_search_values SET loose_value=NULL,loose_grams=NULL`)
	f.exec(`UPDATE artist_search_names SET loose_value=NULL,loose_grams=NULL`)
	f.exec(`UPDATE catalog_projection_state SET search_projection_version=search_projection_version-1,
 published_search_projection_version=published_search_projection_version-1 WHERE capability IN ('search','artists')`)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	current, currentArtists := f.state(), f.artistState()
	if current.Ready() || currentArtists.Ready() || current.TargetGeneration != previous.TargetGeneration+1 ||
		currentArtists.TargetGeneration != previousArtists.TargetGeneration+1 {
		t.Fatalf("版本升级未隔离旧投影：search=%+v artists=%+v", current, currentArtists)
	}
	store := catalog.NewStore(f.db)
	before, err := store.Search(f.ctx, catalog.SearchParams{Type: "albums", Query: "acdc", Page: 1, PageSize: 50})
	if err != nil || before.CatalogState.SearchMode != "legacy" || before.Pagination.Total != 0 {
		t.Fatalf("发布前应走旧路径且不忽略标点：%+v %v", before, err)
	}
	f.run()
	if !f.state().Ready() || !f.artistState().Ready() || f.count(`SELECT count(*) FROM catalog_projection_work WHERE NOT completed`) != 0 {
		t.Fatal("后台回填未完成发布")
	}
	if f.count(`SELECT count(*) FROM catalog_search_values WHERE release_id=$1::uuid AND field='release_title'
 AND loose_value='acdc' AND loose_grams IS NOT NULL AND projection_generation=$2`, f.release, current.TargetGeneration) != 1 ||
		f.count(`SELECT count(*) FROM artist_search_names WHERE original_value='ac/dc' AND loose_value='acdc'
 AND loose_grams IS NOT NULL AND projection_generation=$1`, currentArtists.TargetGeneration) == 0 {
		t.Fatal("搜索或艺人回填未写入当前代次的去标点键")
	}
	after, err := store.Search(f.ctx, catalog.SearchParams{Type: "albums", Query: "acdc", Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	items := after.Items.([]catalog.SearchAlbumItem)
	if after.CatalogState.SearchMode != "normalized" || after.Pagination.Total != 1 || len(items) != 1 ||
		items[0].MatchedReleaseID != f.release || items[0].Match.Original != "AC/DC" || items[0].Match.Kind != "exact" {
		t.Fatalf("发布后 acdc 未命中 AC/DC：%+v", after)
	}
	if keysAfter := f.count(`SELECT count(*) FROM artist_name_keys`); keysAfter != keysBefore {
		t.Fatalf("回填改变了名字键数量：before=%d after=%d", keysBefore, keysAfter)
	}
	if textnorm.Version != 1 || f.count(`SELECT count(*) FROM catalog_projection_state WHERE capability IN ('search','artists')
 AND textnorm_version=1 AND published_textnorm_version=1`) != 2 {
		t.Fatal("去标点回填不得升级 textnorm.Version")
	}
}

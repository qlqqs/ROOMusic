package artistgraph_test

import (
	"fmt"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

func TestPostgreSQLArtistGraphRestoredSearchBatchOnlyChangedTracks(t *testing.T) {
	f := newGraphReviewFixture(t)
	changed := []string{}
	for i := 0; i < 130; i++ {
		id := f.track("旧显示", "present")
		if i%2 == 0 {
			f.exec(`UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, id)
		}
		changed = append(changed, id)
		f.exec(`INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred,observed_at)
 VALUES($1::uuid,$2::uuid,'artist',$3,'flac',false,NOW())`, id, f.scan, fmt.Sprintf("Zulu %03d / Alpha", i))
		for p, role := range []string{"composer", "conductor", "performer", "producer"} {
			f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,$4)`, id, role, "署名 "+role, p+1)
		}
	}
	untouched := f.track("不变", "present")
	if err := searchindex.RefreshTrack(f.ctx, f.tx, untouched); err != nil {
		t.Fatal(err)
	}
	untouchedRow := f.text(`SELECT id::text FROM catalog_search_values WHERE track_id=$1::uuid AND field='track_artist'`, untouched)
	f.exec(`UPDATE catalog_projection_state SET target_generation=9 WHERE capability='search'`)
	f.refresh()
	if n := f.count(`SELECT count(*) FROM catalog_projection_subject_state WHERE subject_kind='track' AND subject_id=ANY($1::uuid[])
 AND input_revision=1 AND search_applied_input_revision=1 AND search_applied_generation=9`, changed); n != 130 {
		t.Fatalf("跨chunk变更轨未同代完成：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_search_values WHERE track_id=ANY($1::uuid[]) AND projection_generation=9`, changed); n != 130*7 {
		t.Fatalf("标题/两艺人/四credit漏写：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM tracks WHERE id=ANY($1::uuid[]) AND artist LIKE 'Alpha / Zulu %'`, changed); n != 130 {
		t.Fatalf("未恢复多名排序：%d", n)
	}
	if got := f.count(`SELECT input_revision FROM catalog_projection_subject_state WHERE subject_kind='track' AND subject_id=$1::uuid`, untouched); got != 1 {
		t.Fatalf("未变化轨被额外Touch：%d", got)
	}
	if got := f.text(`SELECT id::text FROM catalog_search_values WHERE track_id=$1::uuid AND field='track_artist'`, untouched); got != untouchedRow {
		t.Fatal("未变化轨搜索值被重建")
	}
	f.refresh()
	if n := f.count(`SELECT count(*) FROM catalog_projection_subject_state WHERE subject_id=ANY($1::uuid[]) AND input_revision<>1`, changed); n != 0 {
		t.Fatal("恢复后的无变化重刷重复Touch")
	}
}

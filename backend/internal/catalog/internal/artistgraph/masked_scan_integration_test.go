package artistgraph_test

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistGraphReadyMaskedScanCreatesProfile(t *testing.T) {
	f := newGraphReviewFixture(t)
	f.track("", "present")
	f.exec(`UPDATE catalog_projection_state SET target_generation=7 WHERE capability='artists'`)
	f.exec(`INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id)
 VALUES($1::uuid,'album_artist','set','人工覆盖署名',$2::uuid)`, f.release, f.operation)
	f.refresh()
	// ready 前实际准备孤立种子，不手工写任何完成代次。
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{"a0000000-0000-4000-8000-000000000001"}); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,
 published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	if ready, err := artistgraph.Ready(f.ctx, f.tx); err != nil || !ready {
		t.Fatalf("夹具未就绪：%v %v", ready, err)
	}
	// 此孤立身份用于证明在线刷新仍没有恢复全库兜底。
	unrelated := uuid.New()
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'无关孤立身份')`, unrelated)
	f.exec(`UPDATE releases SET artist='新发现扫描署名',album_artist='新发现扫描署名' WHERE id=$1::uuid`, f.release)
	f.refresh()
	id := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='新发现扫描署名'`)
	if n := f.count(`SELECT count(*) FROM artist_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.release_id=$1::uuid AND c.layer='scan' AND n.spelling='新发现扫描署名'`, f.release); n != 2 {
		t.Fatalf("新原始发行署名未保留：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1::uuid`, id); n != 0 {
		t.Fatalf("被修正遮盖的 scan 身份进入有效署名：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_profiles WHERE artist_id=$1::uuid AND applied_generation=7
 AND auto_display_name='新发现扫描署名' AND effective_display_name='新发现扫描署名'`, id); n != 1 {
		t.Fatalf("新建无有效署名身份缺少同代 profile：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_search_names WHERE artist_id=$1::uuid
 AND original_value='新发现扫描署名' AND projection_generation=7`, id); n != 2 {
		t.Fatalf("新身份的名字及主名搜索投影不完整：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_profiles WHERE artist_id=$1::uuid`, unrelated); n != 0 {
		t.Fatal("在线刷新补齐了无关孤立身份")
	}
	if err := f.tx.Commit(); err != nil {
		t.Fatal(err)
	}
	detail, err := catalog.NewStore(f.db).LoadArtist(f.ctx, id)
	if err != nil {
		t.Fatalf("按新身份 ID 读取失败：%v", err)
	}
	if detail.Artist.ID != id || detail.Artist.Name != "新发现扫描署名" || detail.Artist.Available || detail.Artist.AlbumCount != 0 || detail.Artist.TrackCount != 0 {
		t.Fatalf("被遮盖身份未返回安全空摘要：%+v", detail.Artist)
	}
}

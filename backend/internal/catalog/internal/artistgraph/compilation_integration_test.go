package artistgraph_test

import "testing"

func TestPostgreSQLArtistGraphCompilationSkipsReleasePlaceholders(t *testing.T) {
	f := newGraphReviewFixture(t)
	track := f.track("曲目艺人", "present")
	placeholderTrack := f.track("群星", "present")
	f.exec(`UPDATE releases SET artist='群星',album_artist='Various Artists' WHERE id=$1::uuid`, f.release)
	f.exec(`INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id)
 VALUES($1::uuid,'album_artist','set','ＶＡ',$2::uuid)`, f.release, f.operation)
	f.refresh()

	if n := f.count(`SELECT count(*) FROM artist_credits WHERE release_id=$1::uuid`, f.release); n != 0 {
		t.Fatalf("scan 或 correction 占位词建立了发行级事实：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE release_id=$1::uuid`, f.release); n != 0 {
		t.Fatalf("发行占位词进入有效署名：%d", n)
	}
	for _, tc := range []struct{ id, name string }{{track, "曲目艺人"}, {placeholderTrack, "群星"}} {
		if n := f.count(`SELECT count(*) FROM artist_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.track_id=$1::uuid AND c.role='track_artist' AND c.layer='scan' AND n.spelling=$2`, tc.id, tc.name); n != 1 {
			t.Fatalf("曲目 %q 的原始事实未保留：%d", tc.name, n)
		}
		if n := f.count(`SELECT count(*) FROM artist_effective_credits c JOIN artists a ON a.id=c.artist_id
 JOIN artist_names n ON n.id=c.name_id
 WHERE c.track_id=$1::uuid AND c.role='track_artist' AND n.spelling=$2`, tc.id, tc.name); n != 1 {
			t.Fatalf("曲目 %q 未照常建立艺人及有效署名：%d", tc.name, n)
		}
	}
	if n := f.count(`SELECT count(*) FROM artist_names WHERE spelling IN ('Various Artists','ＶＡ')`); n != 0 {
		t.Fatalf("仅发行层出现的占位词建立了名字：%d", n)
	}
}

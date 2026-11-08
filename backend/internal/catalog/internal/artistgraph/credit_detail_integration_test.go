package artistgraph_test

import "testing"

func TestPostgreSQLArtistGraphCreditDetailDeduplicatesPerformerFacts(t *testing.T) {
	f := newGraphReviewFixture(t)
	track := f.track("", "present")
	f.exec(`INSERT INTO track_credits(track_id,role,name,detail,source,position) VALUES
 ($1::uuid,'performer','Hilary Hahn','violin','tag',1),
 ($1::uuid,'performer','Hilary Hahn','cello','tag',2)`, track)

	f.refresh()

	if n := f.count(`SELECT count(*) FROM artist_credits WHERE track_id=$1::uuid AND role='performer' AND layer='scan'`, track); n != 1 {
		t.Fatalf("同曲同人的两种乐器应只建一条 performer 事实，实际 %d 条", n)
	}
	if name := f.text(`SELECT n.spelling FROM artist_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.track_id=$1::uuid AND c.role='performer' AND c.layer='scan'`, track); name != "Hilary Hahn" {
		t.Fatalf("乐器细分不应改变艺人名字：%q", name)
	}
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE track_id=$1::uuid AND role='performer'`, track); n != 1 {
		t.Fatalf("同曲同人的两种乐器应只保留一条有效 performer 署名，实际 %d 条", n)
	}
	if n := f.count(`SELECT count(*) FROM track_credits WHERE track_id=$1::uuid`, track); n != 2 {
		t.Fatalf("艺人事实去重不应删除曲目署名细分，实际 %d 条", n)
	}
	if details := f.text(`SELECT string_agg(detail, ',' ORDER BY position) FROM track_credits
 WHERE track_id=$1::uuid AND role='performer' AND name='Hilary Hahn' AND source='tag'`, track); details != "violin,cello" {
		t.Fatalf("曲目署名应保留两种乐器及原顺序，实际 %q", details)
	}
}

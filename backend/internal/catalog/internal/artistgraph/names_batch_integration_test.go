package artistgraph_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
)

func TestPostgreSQLArtistGraphNamesBatchExistingAndNew(t *testing.T) {
	f := newGraphReviewFixture(t)
	// 超过一批的已知名字，外加同键新写法和真正的新身份；未命中项不能被批量读取丢掉。
	f.exec(`INSERT INTO artists(id,initial_name) SELECT md5('batch-artist:'||i)::uuid,'Batch '||lpad(i::text,3,'0') FROM generate_series(1,300) i`)
	f.exec(`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
 SELECT md5('batch-key:'||i)::uuid,1,'batch '||lpad(i::text,3,'0'),sha256(convert_to('batch '||lpad(i::text,3,'0'),'UTF8')),md5('batch-artist:'||i)::uuid FROM generate_series(1,300) i`)
	f.exec(`INSERT INTO artist_names(id,key_id,spelling,spelling_hash,origin)
 SELECT md5('batch-name:'||i)::uuid,md5('batch-key:'||i)::uuid,'Batch '||lpad(i::text,3,'0'),sha256(convert_to('Batch '||lpad(i::text,3,'0'),'UTF8')),'seed' FROM generate_series(1,300) i`)
	for batch := 0; batch < 3; batch++ {
		names := []string{}
		for i := batch*100 + 1; i <= (batch+1)*100; i++ {
			names = append(names, fmt.Sprintf("Batch %03d", i))
		}
		f.track(strings.Join(names, " / "), "present")
	}
	track := f.track("Batch 001 / Ｂａｔｃｈ ００１ / 首次出现", "present")
	before := f.count(`SELECT count(*) FROM artists`)
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artists`); n != before+1 {
		t.Fatalf("预取未复用已有身份/同键默认路由：%d，预期 %d", n, before+1)
	}
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE role='track_artist'`); n != 303 {
		t.Fatalf("跨批已有或未命中名字丢失：%d", n)
	}
	if n := f.count(`SELECT count(DISTINCT artist_id) FROM artist_effective_credits WHERE track_id=$1::uuid`, track); n != 2 {
		t.Fatalf("同键不同写法未共用身份：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_names WHERE origin='seed' AND spelling LIKE 'Batch %'`); n != 300 {
		t.Fatalf("预取改写种子来源：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_profiles WHERE applied_generation=1`); n != 301 {
		t.Fatalf("新身份或已有身份派生缺失：%d", n)
	}
	if err := f.tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM artists WHERE initial_name='首次出现'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("事务回滚残留新身份：%d %v", count, err)
	}
}

func TestPostgreSQLArtistGraphNamesBatchChecksFullHashes(t *testing.T) {
	for _, tc := range []struct{ name, corrupt, want string }{
		{"规范化键", `UPDATE artist_name_keys SET normalized_key='不同规范化键' WHERE normalized_key='已有姓名'`, "artist name hash collision"},
		{"原文", `UPDATE artist_names SET spelling='不同原文' WHERE spelling='已有姓名'`, "artist spelling hash collision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGraphReviewFixture(t)
			f.track("已有姓名", "present")
			f.refresh()
			before := f.count(`SELECT count(*) FROM artist_credits`)
			f.exec(tc.corrupt)
			if err := artistgraph.Refresh(f.ctx, f.tx, []string{f.release}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("批量命中未核对全文：%v", err)
			}
			if n := f.count(`SELECT count(*) FROM artist_credits`); n != before {
				t.Fatalf("失败刷新改写事实：%d", n)
			}
		})
	}
}

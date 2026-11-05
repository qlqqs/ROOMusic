package maintenance

import (
	"context"
	"database/sql"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
)

// 集合预置仅供完整规模诊断；不计入正式空派生回填证据。不新增公共fixture端口。
// 与graph SQL诊断保持相同集合及严格基数，真实process批仍验证署名业务字段等价。
func prepareArtistBatchDiagnosticFixture(t testing.TB, ctx context.Context, db *sql.DB) map[string]int64 {
	t.Helper()
	var err error
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 先让真实图解析创建夹具唯一未预置的作曲家及其规范化键，不复制生产归一算法。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writegate.Lock(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = artistgraph.Refresh(ctx, tx, []string{"30000000-0000-4000-8000-000000000001"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	// 首次刷新可能按展示顺序重排 track.artist；还原此发行原串，使集合构造与第一次真实扫描的 position 相同。
	if _, err = tx.ExecContext(ctx, `UPDATE tracks SET artist=CASE WHEN position%10=0 THEN 'Teresa Teng' ELSE '周杰倫' END||' / Artist '||lpad((1+(position+9999)%20000)::text,5,'0') WHERE medium_id='40000000-0000-4000-8000-000000000001'`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM artist_credits`)
	exec(`INSERT INTO artist_credits(id,release_id,track_id,role,name_id,position,layer)
 WITH raw AS (
 SELECT r.id AS release_id,NULL::uuid AS track_id,v.role,v.name,1::int AS position FROM releases r
 CROSS JOIN LATERAL(VALUES('release_artist',r.artist),('album_artist',r.album_artist)) v(role,name)
 UNION ALL SELECT NULL,t.id,'track_artist',v.name,v.position::int FROM tracks t
 CROSS JOIN LATERAL unnest(string_to_array(t.artist,' / ')) WITH ORDINALITY v(name,position)
 UNION ALL SELECT NULL,c.track_id,c.role,c.name,1 FROM track_credits c
 ) SELECT gen_random_uuid(),r.release_id,r.track_id,r.role,n.id,r.position,'scan' FROM raw r JOIN artist_names n ON n.spelling=r.name`)
	exec(`INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position)
 SELECT c.id,c.release_id,c.track_id,c.role,c.name_id,k.initial_artist_id,c.position FROM artist_credits c
 JOIN artist_names n ON n.id=c.name_id JOIN artist_name_keys k ON k.id=n.key_id`)
	counts := map[string]int64{}
	for _, check := range []struct {
		name, sql string
		want      int64
	}{
		{"present", `SELECT count(*) FROM tracks WHERE source_status='present'`, 100000},
		{"missing", `SELECT count(*) FROM tracks WHERE source_status='missing'`, 10000},
		{"releases", `SELECT count(*) FROM releases`, 10000},
		{"groups", `SELECT count(*) FROM release_groups`, 8000},
		{"facts", `SELECT count(*) FROM artist_credits`, 680000},
		{"effective", `SELECT count(*) FROM artist_effective_credits`, 680000},
		{"two_artists", `SELECT count(*) FROM (SELECT track_id FROM artist_credits WHERE role='track_artist' GROUP BY track_id HAVING count(*)=2) s`, 110000},
		{"four_credits", `SELECT count(*) FROM (SELECT track_id FROM artist_credits WHERE role IN ('composer','conductor','performer','producer') GROUP BY track_id HAVING count(*)=4) s`, 110000},
		{"participating_pool", `SELECT count(DISTINCT e.artist_id) FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id WHERE t.source_status='present' AND e.artist_id::text LIKE '60000000-%'`, 20000},
		{"pool_aliases", `SELECT count(*) FROM artist_name_keys WHERE id::text LIKE '62000000-%' AND initial_artist_id::text LIKE '60000000-%'`, 4000},
		{"popular_tracks", `SELECT count(DISTINCT t.id) FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id WHERE e.artist_id='a0000000-0000-4000-8000-000000000001' AND t.source_status='present'`, 50000},
	} {
		var n int64
		if err = db.QueryRowContext(ctx, check.sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[check.name] = n
		if n != check.want {
			t.Fatalf("诊断夹具 %s=%d，预期 %d", check.name, n, check.want)
		}
	}
	return counts
}

package artistgraph_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistGraphBatchVotesAndUnboundedScopedNames(t *testing.T) {
	f := newGraphReviewFixture(t)
	for start := 0; start < 260; start += 100 {
		names := []string{}
		for i := start; i < min(start+100, 260); i++ {
			names = append(names, fmt.Sprintf("Alias %03d", i))
		}
		f.track(strings.Join(names, " / "), "present")
	}
	f.exec(`UPDATE releases SET artist='Alias 259',album_artist='Alias 259' WHERE id=$1::uuid`, f.release)
	f.refresh()
	target := uuid.New()
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'作用域目标')`, target)
	// 目标没有全局名字，只拥有作用域署名；不能靠全局归属查询掩盖递归名字枚举被截断。
	f.exec(`INSERT INTO artist_assignment_overrides(release_id,key_id,artist_id,operation_id)
 SELECT $1::uuid,id,$2::uuid,$3::uuid FROM artist_name_keys WHERE normalized_key LIKE 'alias %'`, f.release, target, f.operation)
	f.refresh()
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, target); got != "Alias 259" {
		t.Fatalf("发行跨角色一票与轨目票未正确累加：%q", got)
	}
	if n := f.count(`SELECT count(DISTINCT name_id) FROM artist_search_names WHERE artist_id=$1::uuid`, target); n != 260 {
		t.Fatalf("作用域名字枚举被截断：%d", n)
	}
	// 保留旧票集合定义作为独立对照，覆盖同 subject/name 去重和 present 过滤。
	const compareVotes = `WITH votes AS (
 SELECT DISTINCT c.artist_id,c.release_id,c.track_id,c.name_id FROM artist_effective_credits c
 WHERE c.artist_id=$1::uuid AND (EXISTS(SELECT 1 FROM tracks t WHERE t.id=c.track_id AND t.source_status='present') OR
 EXISTS(SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=c.release_id AND t.source_status='present'))
 ), counts AS (SELECT name_id,count(*) AS votes FROM votes GROUP BY name_id)
 SELECT n.spelling FROM counts c JOIN artist_names n ON n.id=c.name_id ORDER BY votes DESC,n.spelling COLLATE "C",n.id LIMIT 1`
	want := f.text(compareVotes, target)
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, target); got != want {
		t.Fatalf("新旧计票不等价：%q != %q", got, want)
	}
	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, f.medium)
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{target}); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, target); got != "Alias 000" {
		t.Fatalf("全 missing 的零票没有回退到完整归属名字：%q", got)
	}
	if n := f.count(`SELECT count(DISTINCT name_id) FROM artist_search_names WHERE artist_id=$1::uuid`, target); n != 260 {
		t.Fatalf("missing 署名别名丢失：%d", n)
	}
}

func TestPostgreSQLArtistGraphProfileDensityProbeDoesNotLimitVotes(t *testing.T) {
	trace := &denseProfileSQLTrace{}
	f := newGraphReviewFixture(t, trace)
	// 前4096轨两种写法各2048票；第4097轨决定胜出，探测预算不能成为计票上限。
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
 SELECT ('10000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,$1::uuid,i,'密集票',
 CASE WHEN i%2=1 THEN 'Beyoncé' ELSE 'Beyonce' END,$2::uuid,'dense-'||i||'.flac','present',NOW()
 FROM generate_series(1,4097) i`, f.medium, f.root)
	f.refresh()
	if got := f.profile("beyonce"); got != "Beyoncé" {
		t.Fatalf("密集路径漏了探测预算之后的票：%q", got)
	}
	id := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='beyonce'`)
	// 同一轨跨两角色仍只给Beyonce一票；本批另一个身份不能串票。
	f.exec(`INSERT INTO track_credits(track_id,role,name,position)
 VALUES('10000000-0000-4000-8000-000000000002','composer','Beyonce',1),
 ('10000000-0000-4000-8000-000000000002','performer','Beyonce',2)`)
	f.track("Solo Artist", "present")
	f.refresh()
	other := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='solo artist'`)
	for _, ids := range [][]string{{id, id, strings.ToUpper(id), other}, {id, strings.ReplaceAll(id, "-", ""), other}} {
		if err := artistgraph.RefreshProfiles(f.ctx, f.tx, ids); err != nil {
			t.Fatal(err)
		}
		if got := f.profile("beyonce"); got != "Beyoncé" {
			t.Fatalf("密集跨角色或重复身份计票改变：%q", got)
		}
		if got := f.profile("solo artist"); got != "Solo Artist" {
			t.Fatalf("密集批混入另一身份票：%q", got)
		}
	}
	// 发行跨角色仅一票：ASCII写法靠发行票追平；若重复身份倍增轨票，会错误改回带重音写法。
	f.exec(`UPDATE releases SET artist='Beyonce',album_artist='Beyonce' WHERE id=$1::uuid`, f.release)
	f.refresh()
	mixed := []string{id, strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), other}
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, mixed); err != nil {
		t.Fatal(err)
	}
	if got := f.profile("beyonce"); got != "Beyonce" {
		t.Fatalf("相同uuid重复轨票压过发行竞争票：%q", got)
	}
	finalSQL := trace.query
	if !strings.Contains(finalSQL, "SELECT DISTINCT id FROM unnest($1::uuid[])") {
		t.Fatal("未捕获生产UUID去重SQL")
	}
	// 最小真实PG计划与生产实际SQL；不将这个小夹具计划冒充完整SEARCH_READY性能证据。
	var plan json.RawMessage
	if err := f.tx.QueryRowContext(f.ctx, "EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+finalSQL, mixed).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("ROOMUSIC_ARTIST_DENSE_PLAN_DIR"); out != "" {
		if !filepath.IsAbs(out) {
			t.Fatal("诊断输出必须是仓外绝对路径")
		}
		if err := os.MkdirAll(out, 0700); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(map[string]any{"sql": finalSQL, "sql_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(finalSQL))), "arguments": mixed, "plan": plan, "scope": "4097轨+重复uuid+发行票竞争的最小PG计划，非完整性能验收"}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(out, "dense-distinct-minimal-plan.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.exec(`UPDATE releases SET artist='',album_artist='' WHERE id=$1::uuid`, f.release)
	f.refresh()
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id='10000000-0000-4000-8000-000000004097'`)
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{id}); err != nil {
		t.Fatal(err)
	}
	if got := f.profile("beyonce"); got != "Beyonce" {
		t.Fatalf("密集路径没有扣除非present票：%q", got)
	}
	f.exec(`DELETE FROM track_credits WHERE track_id='10000000-0000-4000-8000-000000000002'`)
	f.exec(`UPDATE tracks SET artist='' WHERE id='10000000-0000-4000-8000-000000004097'`)
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1::uuid`, id); n != 4096 {
		t.Fatalf("稀疏边界事实数量：%d", n)
	}
	if got := f.profile("beyonce"); got != "Beyonce" {
		t.Fatalf("路径切换改变计票：%q", got)
	}
}

// 仅测试记录真实生产主名SQL，避免复制另一套查询后误称最终计划。
type denseProfileSQLTrace struct{ query string }

func (tr *denseProfileSQLTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, "WITH ") && strings.Contains(d.SQL, "popular AS (") {
		tr.query = d.SQL
	}
	return ctx
}
func (tr *denseProfileSQLTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPostgreSQLArtistGraphDenseDuplicateUUIDReleaseVoteCompetition(t *testing.T) {
	f := newGraphReviewFixture(t)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
 SELECT gen_random_uuid(),$1::uuid,i,'热身份','Hot Artist',$2::uuid,'hot-'||i||'.flac','present',NOW()
 FROM generate_series(1,4097) i`, f.medium, f.root)
	f.track("Zulu", "present")
	f.exec(`UPDATE releases SET artist='Alpha',album_artist='Alpha' WHERE id=$1::uuid`, f.release)
	f.refresh()
	const target = "aa000000-0000-4000-8000-000000000001"
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'Alpha')`, target)
	f.exec(`INSERT INTO artist_name_bindings(key_id,artist_id,operation_id)
 SELECT id,$1::uuid,$2::uuid FROM artist_name_keys WHERE normalized_key IN('alpha','zulu')`, target, f.operation)
	f.refresh()
	hot := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='hot artist'`)
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{target, hot}); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, target); got != "Alpha" {
		t.Fatalf("发行/轨各1票基线：%q", got)
	}
	// hot身份只负责触发dense；目标Alpha1发行票与Zulu1轨票平手，按C排序Alpha胜。
	// 删除SQL DISTINCT后，大小写相同UUID给Zulu重复轨票，错误地改为Zulu胜。
	if err := artistgraph.RefreshProfiles(f.ctx, f.tx, []string{target, strings.ToUpper(target), hot}); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT auto_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, target); got != "Alpha" {
		t.Fatalf("UUID重复轨票破坏发行竞争：got %q want Alpha", got)
	}
}

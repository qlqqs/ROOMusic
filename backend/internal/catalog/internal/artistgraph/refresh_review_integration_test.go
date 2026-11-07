package artistgraph_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type graphReviewFixture struct {
	t                                      *testing.T
	ctx                                    context.Context
	db                                     *sql.DB
	tx                                     *sql.Tx
	root, release, medium, operation, scan string
}

func newGraphReviewFixture(t *testing.T, tracers ...pgx.QueryTracer) *graphReviewFixture {
	t.Helper()
	scoped := postgrestest.NewSchema(t, "artist_graph_review_")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := postgres.Open(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracers) > 0 {
		db.Close()
		config, err := pgx.ParseConfig(scoped)
		if err != nil {
			t.Fatal(err)
		}
		config.Tracer = tracers[0]
		db = stdlib.OpenDB(*config)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	if err := writegate.Lock(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_projection_state SET search_projection_version=$1 WHERE capability='artists'`, artistgraph.Version); err != nil {
		t.Fatal(err)
	}
	f := &graphReviewFixture{t: t, ctx: ctx, db: db, tx: tx, root: uuid.New(), release: uuid.New(), medium: uuid.New(), operation: uuid.New(), scan: uuid.New()}
	group, actor := uuid.New(), uuid.New()
	f.exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, f.scan)
	f.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'disabled',1)`, f.root, t.TempDir())
	f.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,candidate_kind,candidate_anchor) VALUES($1::uuid,$2::uuid,'测试发行','','',$3::uuid,'ordinary_directory',$1::text)`, f.release, group, f.root)
	f.exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, f.medium, f.release)
	f.exec(`INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'graph-review','x')`, actor)
	f.exec(`INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
 VALUES($1::uuid,$2::uuid,'artist_rename','artist','a0000000-0000-4000-8000-000000000001',$3::uuid,'graph-review',1,2,'{}','{}','{}','graph-review')`, f.operation, actor, uuid.New())
	return f
}
func (f *graphReviewFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.tx.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *graphReviewFixture) text(q string, args ...any) string {
	f.t.Helper()
	var s string
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *graphReviewFixture) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *graphReviewFixture) track(name, status string) string {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
 VALUES($1::uuid,$2::uuid,1,'测试曲目',$3,$4::uuid,$1::text||'.flac',$5,NOW())`, id, f.medium, name, f.root, status)
	return id
}
func (f *graphReviewFixture) refresh() {
	f.t.Helper()
	if err := artistgraph.Refresh(f.ctx, f.tx, []string{f.release}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *graphReviewFixture) profile(key string) string {
	return f.text(`SELECT p.auto_display_name FROM artist_profiles p JOIN artist_name_keys k ON k.initial_artist_id=p.artist_id WHERE k.normalized_key=$1`, key)
}

func TestPostgreSQLArtistGraphReviewVotesAndMissing(t *testing.T) {
	f := newGraphReviewFixture(t)
	first := f.track("Beyoncé", "present")
	second := f.track("Beyonce", "present")
	f.exec(`UPDATE releases SET artist='Beyoncé',album_artist='Beyoncé' WHERE id=$1::uuid`, f.release)
	// 两个发行角色只能计一票；同轨的三个角色也只能计一票。
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','Beyonce',1),($1::uuid,'performer','Beyonce',2)`, second)
	for range 3 {
		f.refresh()
	}
	if got := f.profile("beyonce"); got != "Beyoncé" {
		t.Fatalf("跨角色或重刷重复计票：%q", got)
	}
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, first)
	f.refresh()
	// 发行仍可见，双方各一票；原文 C 排序让 ASCII 写法获胜。
	if got := f.profile("beyonce"); got != "Beyonce" {
		t.Fatalf("缺失轨仍参与计票：%q", got)
	}
	f.exec(`UPDATE releases SET album_artist='Beyonce',artist='Beyonce' WHERE id=$1::uuid`, f.release)
	f.exec(`INSERT INTO release_corrections(release_id,field,mode,text_value,operation_id) VALUES($1::uuid,'album_artist','set','Beyoncé',$2::uuid)`, f.release, f.operation)
	f.exec(`UPDATE tracks SET source_status='present' WHERE id=$1::uuid`, first)
	f.refresh()
	if got := f.profile("beyonce"); got != "Beyoncé" {
		t.Fatalf("扫描层覆盖了修正层票数：%q", got)
	}
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE release_id=$1::uuid AND name_id IN(SELECT id FROM artist_names WHERE spelling='Beyonce')`, f.release); n != 0 {
		t.Fatalf("无效扫描署名进入有效图：%d", n)
	}
	f.exec(`DELETE FROM release_corrections WHERE release_id=$1::uuid`, f.release)
	f.refresh()
	if got := f.profile("beyonce"); got != "Beyonce" {
		t.Fatalf("清除修正未恢复扫描层：%q", got)
	}
}

func TestPostgreSQLArtistGraphReviewRawEvidenceAndCandidates(t *testing.T) {
	f := newGraphReviewFixture(t)
	track := f.track("周杰伦", "present")
	f.exec(`INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred,observed_at) VALUES($1::uuid,$2::uuid,'artist','Jay Chou','flac',false,NOW())`, track, f.scan)
	f.exec(`UPDATE releases SET artist='专辑署名',album_artist='专辑署名' WHERE id=$1::uuid`, f.release)
	f.refresh()
	if got := f.text(`SELECT artist FROM tracks WHERE id=$1::uuid`, track); got != "Jay Chou" {
		t.Fatalf("可信原文未恢复：%q", got)
	}
	if n := f.count(`SELECT count(*) FROM artist_candidate_signals WHERE kind='same_track_role'`); n != 0 {
		t.Fatal("旧观察伪造重复标签候选")
	}
	if n := f.count(`SELECT count(*) FROM artist_candidate_signals WHERE kind='album_track_one_to_one'`); n != 1 {
		t.Fatalf("完整单一署名未建立一对一候选：%d", n)
	}
	missingArtist := f.track("未知艺术家", "present")
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artist_candidate_signals WHERE kind='album_track_one_to_one'`); n != 0 {
		t.Fatal("缺少真实署名的轨被补成一对一")
	}
	if n := f.count(`SELECT count(*) FROM artist_name_keys WHERE normalized_key='未知艺术家'`); n != 0 {
		t.Fatal("旧无证据占位被建成人")
	}
	f.exec(`INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred,observed_at) VALUES($1::uuid,$2::uuid,'artist','未知艺术家','flac',false,NOW())`, missingArtist, f.scan)
	f.exec(`INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind) VALUES($1::uuid,'track_artist','ARTIST',0,'Jay Chou','flac'),($1::uuid,'track_artist','ARTIST',1,'周杰倫','flac'),($1::uuid,'track_artist','ARTIST',2,'另一人','flac')`, track)
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artist_effective_credits WHERE track_id=$1::uuid AND role='track_artist'`, track); n != 3 {
		t.Fatalf("原始多值署名丢失：%d", n)
	}
	if n := f.count(`SELECT count(*) FROM artist_name_keys WHERE normalized_key='未知艺术家'`); n != 1 {
		t.Fatal("真实同名标签被当作占位")
	}
	if n := f.count(`SELECT count(*) FROM artist_candidate_signals`); n != 0 {
		t.Fatalf("同曲同身份共同署名仍产生候选信号：%d", n)
	}
	f.exec(`DELETE FROM artist_tag_evidence WHERE track_id=$1::uuid`, track)
	f.exec(`INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind) VALUES($1::uuid,'track_artist','ARTIST',0,'Jay Chou / 另一人','flac')`, track)
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artist_candidate_signals WHERE kind='same_track_role'`); n != 0 {
		t.Fatal("明确多人列表被当作重复标签")
	}
}

func TestPostgreSQLArtistGraphReviewRawEvidenceCrossAlbumBracketAlias(t *testing.T) {
	f := newGraphReviewFixture(t)
	f.track("杨璐（羊鹿鹿）", "present")
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artist_alias_signals`); n != 0 {
		t.Fatalf("没有单独写法时产生别名信号：%d", n)
	}
	group, release, medium, track := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,candidate_kind,candidate_anchor)
 VALUES($1::uuid,$2::uuid,'别名专辑','','',$3::uuid,'ordinary_directory',$1::text)`, release, group, f.root)
	f.exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, medium, release)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
 VALUES($1::uuid,$2::uuid,1,'别名曲目','羊鹿鹿',$3::uuid,$1::text||'.flac','present',NOW())`, track, medium, f.root)
	// 只刷新后扫描到的单独写法端，也必须补出跨发行的信号。
	if err := artistgraph.Refresh(f.ctx, f.tx, []string{release}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM artist_alias_signals s JOIN artist_name_keys k ON k.id=s.bracket_key_id
 WHERE s.release_id=$1::uuid AND s.alias_release_id=$2::uuid AND s.alias_part='inner'
 AND k.normalized_key='杨璐(羊鹿鹿)' AND (s.left_name,s.right_name) IN (('杨璐（羊鹿鹿）','羊鹿鹿'),('羊鹿鹿','杨璐（羊鹿鹿）'))`, f.release, release); n != 1 {
		t.Fatalf("单独写法端刷新未生成正确的跨专辑括号别名信号：%d", n)
	}
	f.refresh()
	if n := f.count(`SELECT count(*) FROM artist_alias_signals`); n != 1 {
		t.Fatalf("括号端刷新重复或丢失信号：%d", n)
	}
	f.exec(`UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, track)
	if err := artistgraph.Refresh(f.ctx, f.tx, []string{release}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM artist_alias_signals`); n != 0 {
		t.Fatalf("单独写法端缺失后仍保留信号：%d", n)
	}
}

func TestPostgreSQLArtistGraphReviewSeedIntentAndScopePrecedence(t *testing.T) {
	f := newGraphReviewFixture(t)
	track := f.track("Jay Chou", "present")
	f.refresh()
	const seed = "a0000000-0000-4000-8000-000000000001"
	const key = "a1000000-0000-4000-8000-000000000002"
	const otherKey = "a1000000-0000-4000-8000-000000000001"
	global, release, individual := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []string{global, release, individual} {
		f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'拆分身份')`, id)
	}
	f.exec(`INSERT INTO artist_name_bindings(key_id,artist_id,operation_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, key, global, f.operation)
	f.exec(`INSERT INTO artist_assignment_overrides(release_id,key_id,artist_id,operation_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, f.release, key, release, f.operation)
	f.exec(`INSERT INTO artist_assignment_overrides(track_id,key_id,artist_id,operation_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, track, key, individual, f.operation)
	f.exec(`INSERT INTO artist_overrides(artist_id,display_name,name_operation_id) VALUES($1::uuid,'人工主名',$2::uuid)`, individual, f.operation)
	f.exec(`INSERT INTO artist_candidate_ignores(left_key_id,right_key_id,operation_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, otherKey, key, f.operation)
	snapshot := `SELECT jsonb_build_object('bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY key_id) FROM artist_name_bindings b),'assignments',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM artist_assignment_overrides a),'overrides',(SELECT jsonb_agg(to_jsonb(o) ORDER BY artist_id) FROM artist_overrides o),'ignores',(SELECT jsonb_agg(to_jsonb(i) ORDER BY left_key_id,right_key_id) FROM artist_candidate_ignores i),'revisions',(SELECT jsonb_agg(jsonb_build_array(id,edit_revision) ORDER BY id) FROM artists),'operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM operations o))::text`
	before := f.text(snapshot)
	for range 3 {
		f.refresh()
	}
	if after := f.text(snapshot); after != before {
		t.Fatal("重刷改变人工意图、revision或operations")
	}
	if got := f.text(`SELECT artist_id::text FROM artist_effective_credits WHERE track_id=$1::uuid`, track); got != individual {
		t.Fatalf("曲目覆盖未优先：%s", got)
	}
	if got := f.text(`SELECT effective_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, individual); got != "人工主名" {
		t.Fatalf("人工主名被扫描改写：%q", got)
	}
	future := f.track("Jay Chou", "present")
	f.refresh()
	if got := f.text(`SELECT artist_id::text FROM artist_effective_credits WHERE track_id=$1::uuid`, future); got != release {
		t.Fatalf("同发行新增轨未跟随：%s", got)
	}
	if got := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE id=$1::uuid`, key); got != seed {
		t.Fatal("改写了种子初始路由")
	}
	f.exec(`SET CONSTRAINTS ALL IMMEDIATE`)
	// 回滚事务后不能有半套新目录或派生数据被提交。
	if err := f.tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM artist_effective_credits`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("事务回滚残留有效图：%d %v", n, err)
	}
}

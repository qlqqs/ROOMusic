package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type batchFixture struct {
	t   *testing.T
	ctx context.Context
	db  *sql.DB
	tx  *sql.Tx
	ids []string
}

func newBatchFixture(t *testing.T, n int) *batchFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "search_batch_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &batchFixture{t: t, ctx: ctx, db: db}
	f.begin()
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'disabled',1)`, root, t.TempDir())
	f.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,source_root_id,candidate_kind,candidate_anchor) VALUES($1::uuid,$2::uuid,'发行标题','发行艺人',$3::uuid,'ordinary_directory',$1::text)`, release, group, root)
	f.exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, medium, release)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1)
		f.ids = append(f.ids, id)
		status := "present"
		if i%3 == 0 {
			status = "missing"
		}
		f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
 VALUES($1::uuid,$2::uuid,$3,'繁體 ＡＢＣ %_~','Zulu / Beyoncé / Alpha / Alpha',$4::uuid,$1::text||'.flac',$5,NOW())`, id, medium, i+1, root, status)
		for j, role := range []string{"composer", "conductor", "performer", "producer"} {
			f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,$4)`, id, role, "原文 "+role+" / 保留整条", j+1)
		}
	}
	f.exec(`UPDATE catalog_projection_state SET target_generation=4,manifest_sealed=true WHERE capability='search'`)
	f.exec(`INSERT INTO catalog_projection_subject_state(subject_kind,subject_id,input_revision,search_applied_generation,search_applied_input_revision,search_title)
 SELECT 'track',id,5,1,5,'旧标题' FROM tracks`)
	return f
}
func (f *batchFixture) begin() {
	f.t.Helper()
	var err error
	f.tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	tx := f.tx
	f.t.Cleanup(func() { tx.Rollback() })
	if err = writegate.Lock(f.ctx, f.tx); err != nil {
		f.t.Fatal(err)
	}
}
func (f *batchFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.tx.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *batchFixture) snapshot() string {
	f.t.Helper()
	var s string
	err := f.tx.QueryRowContext(f.ctx, `SELECT jsonb_build_object(
 'values',(SELECT jsonb_agg(to_jsonb(v)-'id' ORDER BY track_id,field,role,position) FROM catalog_search_values v),
 'markers',(SELECT jsonb_agg(to_jsonb(s) ORDER BY subject_kind,subject_id) FROM catalog_projection_subject_state s),
 'live',(SELECT live_updated FROM catalog_projection_state WHERE capability='search'))::text`).Scan(&s)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func TestRefreshTracksEmptyNoOp(t *testing.T) {
	if err := RefreshTracks(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLRefreshTracksMatchesSingleAcrossChunks(t *testing.T) {
	f := newBatchFixture(t, 260)
	f.exec(`UPDATE tracks SET title='',artist='' WHERE id=$1::uuid`, f.ids[1])
	f.exec(`DELETE FROM track_credits WHERE track_id=$1::uuid`, f.ids[1])
	// 已在封闭清单中的对象不增加 live_updated；其余显式变更对象仍各增加一次。
	f.exec(`INSERT INTO catalog_projection_phases(build_id,phase,subject_kind,total)
 SELECT build_id,'tracks','track',1 FROM catalog_projection_state WHERE capability='search'`)
	f.exec(`INSERT INTO catalog_projection_work(build_id,phase,subject_kind,subject_id)
 SELECT build_id,'tracks','track',$1::uuid FROM catalog_projection_state WHERE capability='search'`, f.ids[0])
	ids := append(append([]string{}, f.ids...), uuid.New()) // 未知对象也与 Apply 一样生成空完成 marker。
	f.exec(`SAVEPOINT compare_single`)
	for _, id := range ids {
		if err := RefreshTrack(f.ctx, f.tx, id); err != nil {
			t.Fatal(err)
		}
	}
	expected := f.snapshot()
	f.exec(`ROLLBACK TO SAVEPOINT compare_single`)
	input := append(append([]string{}, ids...), ids[0], strings.ToUpper(ids[len(ids)-1]))
	if err := RefreshTracks(f.ctx, f.tx, input); err != nil {
		t.Fatal(err)
	}
	if got := f.snapshot(); got != expected {
		t.Fatalf("批量与逐轨字段/顺序/revision/目标marker/live不等价\n%s\n%s", got, expected)
	}
	var n int
	if err := f.tx.QueryRowContext(f.ctx, `SELECT count(*) FROM catalog_projection_subject_state WHERE search_applied_generation=4 AND search_applied_input_revision=input_revision`).Scan(&n); err != nil || n != 261 {
		t.Fatalf("跨chunk不完整：%d %v", n, err)
	}
	// Apply 仍应跳过本代同revision，不能再次覆盖批量写好的投影。
	s, err := LoadState(f.ctx, f.tx)
	if err != nil {
		t.Fatal(err)
	}
	written, err := Apply(f.ctx, f.tx, s, "track", ids[0])
	if err != nil || written {
		t.Fatalf("完成marker不被Apply识别：%v %v", written, err)
	}
}

func TestPostgreSQLRefreshTracksFailureAndRollback(t *testing.T) {
	f := newBatchFixture(t, 129)
	before := f.snapshot()
	f.exec(`SAVEPOINT invalid_source`)
	f.exec(`UPDATE tracks SET title=$2 WHERE id=$1::uuid`, f.ids[128], strings.Repeat("x", 4097))
	if err := RefreshTracks(f.ctx, f.tx, f.ids); err == nil || !strings.Contains(err.Error(), "search source text exceeds budget") {
		t.Fatalf("批量放松原文预算：%v", err)
	}
	// 首chunk已经写入，但任何后续错误仍由共同事务回滚，不能自行提交部分chunk。
	f.exec(`ROLLBACK TO SAVEPOINT invalid_source`)
	if got := f.snapshot(); got != before {
		t.Fatal("后续chunk失败后回滚残留marker/搜索值")
	}
	f.exec(`SAVEPOINT wrong_version`)
	f.exec(`UPDATE catalog_projection_state SET textnorm_version=textnorm_version+1 WHERE capability='search'`)
	if err := RefreshTracks(f.ctx, f.tx, f.ids); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("未拒绝未知版本：%v", err)
	}
	if got := f.snapshot(); got != before {
		t.Fatal("未知版本推进revision")
	}
	f.exec(`ROLLBACK TO SAVEPOINT wrong_version`)
	if err := RefreshTracks(f.ctx, f.tx, f.ids); err != nil {
		t.Fatal(err)
	}
	f.exec(`ROLLBACK TO SAVEPOINT wrong_version`)
	if got := f.snapshot(); got != before {
		t.Fatal("成功调用提前提交了chunk")
	}
}

func TestPostgreSQLRefreshTracksCancellationRollsBack(t *testing.T) {
	f := newBatchFixture(t, 2)
	f.exec(`CREATE FUNCTION delay_search_batch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(3); RETURN NEW; END $$`)
	f.exec(`CREATE TRIGGER delay_search_batch BEFORE INSERT ON catalog_search_values FOR EACH ROW EXECUTE FUNCTION delay_search_batch()`)
	if err := f.tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.begin()
	ctx, cancel := context.WithTimeout(f.ctx, 200*time.Millisecond)
	defer cancel()
	if err := RefreshTracks(ctx, f.tx, f.ids); err == nil {
		t.Fatal("取消未中断批量双写")
	}
	if ctx.Err() == nil {
		t.Fatal("未到取消点即出现其他错误")
	}
	f.tx.Rollback()
	var n int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM catalog_projection_subject_state WHERE input_revision<>5 OR search_applied_generation<>1 OR search_applied_input_revision<>5`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("取消残留revision/marker：%d %v", n, err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM catalog_search_values`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("取消残留投影：%d %v", n, err)
	}
}

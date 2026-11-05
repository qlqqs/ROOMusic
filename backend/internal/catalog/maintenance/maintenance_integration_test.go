package maintenance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type fixture struct {
	t                     *testing.T
	db                    *sql.DB
	root, release, medium string
	ctx                   context.Context
	leaseKey              int32
}

func newFixture(t *testing.T, tracks int) *fixture {
	t.Helper()
	scoped := postgrestest.NewSchema(t, "search_maintenance_")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := postgres.Open(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{t: t, db: db, root: uuid.New(), release: uuid.New(), medium: uuid.New(), ctx: ctx}
	f.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'disabled',1)`, f.root, t.TempDir())
	group := uuid.New()
	f.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,candidate_kind,candidate_anchor)
  VALUES($1::uuid,$2::uuid,'繁體發行','Beyoncé','周杰倫',$3::uuid,'ordinary_directory',$4)`, f.release, group, f.root, f.release)
	f.exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, f.medium, f.release)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
  SELECT gen_random_uuid(),$1::uuid,n,CASE WHEN n=1 THEN '' ELSE '旧曲 '||n::text END,'',$2::uuid,
  n::text||'.wav',CASE WHEN n%2=0 THEN 'missing' ELSE 'present' END,NOW() FROM generate_series(1,$3::integer)n`, f.medium, f.root, tracks)
	return f
}
func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.db.QueryRowContext(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *fixture) state() searchindex.State {
	f.t.Helper()
	s, err := searchindex.LoadState(f.ctx, f.db)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *fixture) lease() *sql.Conn {
	f.t.Helper()
	conn, err := f.db.Conn(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	var locked bool
	if f.leaseKey, locked, err = acquireLease(f.ctx, conn); err != nil || !locked {
		conn.Close()
		f.t.Fatalf("lease: %v %v", locked, err)
	}
	return conn
}
func (f *fixture) batch(conn *sql.Conn, s searchindex.State, releaseLimit, trackLimit int) batchResult {
	f.t.Helper()
	r, err := processBatch(f.ctx, conn, s, releaseLimit, trackLimit)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}
func (f *fixture) run() {
	f.t.Helper()
	if err := Run(f.ctx, f.db, slog.New(slog.NewTextHandler(io.Discard, nil)), artistTestOptions()); err != nil {
		f.t.Fatal(err)
	}
}

func TestPostgreSQLCatalogBackfillRestartAndAtomicPublish(t *testing.T) {
	f := newFixture(t, 120)
	before := f.state()
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM catalog_projection_work`) != 0 || f.count(`SELECT count(*) FROM catalog_search_values`) != 0 {
		t.Fatal("startup performed backfill")
	}
	state := f.state()
	if state.BuildID != before.BuildID || state.CatalogEpoch != before.CatalogEpoch {
		t.Fatal("same-version startup reset build")
	}
	conn := f.lease()
	if err := discover(f.ctx, conn, state); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	releaseBatch := f.batch(conn, state, 10, 50)
	if releaseBatch.Kind != "release" || releaseBatch.BatchSize != 1 || f.count(`SELECT count(*) FROM catalog_search_values WHERE track_id IS NOT NULL`) != 0 {
		t.Fatal("release batch expanded its tracks")
	}
	tracks := f.batch(conn, state, 10, 17)
	if tracks.BatchSize != 17 {
		t.Fatalf("batch=%+v", tracks)
	}
	if f.state().Ready() {
		t.Fatal("partially filled projection was published")
	}
	var oldValueID string
	if err := f.db.QueryRowContext(f.ctx, `SELECT id::text FROM catalog_search_values WHERE track_id IS NOT NULL LIMIT 1`).Scan(&oldValueID); err != nil {
		t.Fatal(err)
	}
	releaseLease(conn, f.leaseKey)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if f.state().BuildID != state.BuildID || f.count(`SELECT completed FROM catalog_projection_phases WHERE phase='tracks'`) != 17 {
		t.Fatal("restart reset durable progress")
	}
	f.run()
	after := f.state()
	if !after.Ready() || after.CatalogEpoch != state.CatalogEpoch+1 {
		t.Fatalf("published state=%+v", after)
	}
	if f.count(`SELECT count(*) FROM catalog_search_values WHERE id=$1::uuid`, oldValueID) != 1 {
		t.Fatal("restart rewrote completed subject")
	}
	if f.count(`SELECT count(*) FROM catalog_projection_subject_state WHERE search_applied_generation=$1 AND search_applied_input_revision=input_revision`, after.TargetGeneration) != 121 {
		t.Fatal("missing/empty subjects lack completion markers")
	}
	if f.count(`SELECT count(*) FROM catalog_projection_work WHERE NOT completed`) != 0 {
		t.Fatal("pending tasks after publish")
	}
	epoch := after.CatalogEpoch
	f.run()
	if f.state().CatalogEpoch != epoch {
		t.Fatal("ready restart republished")
	}
}

func TestPostgreSQLCatalogBackfillCurrentRevisionRollbackAndRepair(t *testing.T) {
	f := newFixture(t, 3)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	state := f.state()
	conn := f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err := discover(f.ctx, conn, state); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writegate.Lock(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(f.ctx, `UPDATE releases SET title='新 Beyoncé' WHERE id=$1::uuid`, f.release); err != nil {
		t.Fatal(err)
	}
	if err = searchindex.RefreshRelease(f.ctx, tx, f.release); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var valueID string
	if err = f.db.QueryRowContext(f.ctx, `SELECT id::text FROM catalog_search_values WHERE release_id=$1::uuid AND field='release_title'`, f.release).Scan(&valueID); err != nil {
		t.Fatal(err)
	}
	result := f.batch(conn, state, 10, 50)
	if result.Written != 0 || result.BatchSize != 1 {
		t.Fatalf("live revision was not skipped: %+v", result)
	}
	if f.count(`SELECT count(*) FROM catalog_search_values WHERE id=$1::uuid AND normalized_value='新 beyonce'`, valueID) != 1 {
		t.Fatal("stale backfill overwrote real-time projection")
	}
	// 业务事实、revision 与投影一起回滚。
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writegate.Lock(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(f.ctx, `UPDATE releases SET title=$2 WHERE id=$1::uuid`, f.release, strings.Repeat("x", 4097)); err != nil {
		t.Fatal(err)
	}
	if err = searchindex.RefreshRelease(f.ctx, tx, f.release); err == nil {
		t.Fatal("oversized projection unexpectedly succeeded")
	}
	tx.Rollback()
	if f.count(`SELECT count(*) FROM releases WHERE id=$1::uuid AND title='新 Beyoncé'`, f.release) != 1 {
		t.Fatal("projection failure did not rollback business")
	}
	f.batch(conn, state, 10, 50)
	// 模拟发布覆盖检查发现清单之后的缺口，不能靠最大 UUID 游标直接发布。
	id := uuid.New()
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at)
  VALUES($1::uuid,$2::uuid,100,'后来创建','',$3::uuid,'late.wav','missing',NOW())`, id, f.medium, f.root)
	published, err := publishOrRepair(f.ctx, conn, state)
	if err != nil || published {
		t.Fatalf("published gap: %v %v", published, err)
	}
	repair := f.batch(conn, state, 10, 50)
	if !strings.HasPrefix(repair.Phase, "repair_tracks_") || repair.Total != 1 {
		t.Fatalf("repair=%+v", repair)
	}
	published, err = publishOrRepair(f.ctx, conn, state)
	if err != nil || !published {
		t.Fatalf("publish: %v %v", published, err)
	}
}

func TestCatalogBackfillBatchLimitAndCancellation(t *testing.T) {
	if adaptLimit(50, 500, 250*time.Millisecond) != 25 || adaptLimit(500, 500, time.Millisecond) != 500 || adaptLimit(1, 500, time.Second) != 1 {
		t.Fatal("batch bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// scan 执行真实 library 扫描：解析合成 WAV、暂存、身份规划、PersistCandidate 与 missing 对账。
func scan(t *testing.T, ctx context.Context, service *library.Service) {
	t.Helper()
	result, err := service.StartScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.WaitForScans(ctx); err != nil {
		t.Fatal(err)
	}
	completed, err := service.GetScan(ctx, result.Scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "succeeded" {
		t.Fatalf("scan=%+v", completed)
	}
}
func writeWAV(t *testing.T, path string) {
	t.Helper()
	data := make([]byte, 44+160)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 160)
	for i := 44; i < len(data); i++ {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLCatalogBackfillDuringRealScan(t *testing.T) {
	f := newFixture(t, 160)
	root := t.TempDir()
	rootID := uuid.New()
	f.exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, root)
	oldPath := filepath.Join(root, "旧曲.wav")
	writeWAV(t, oldPath)
	service := library.NewService(f.ctx, f.db, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	scan(t, f.ctx, service)
	var trackID string
	if err := f.db.QueryRowContext(f.ctx, `SELECT id::text FROM tracks WHERE source_root_id=$1::uuid`, rootID).Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	// 在测试中模拟已登记的新代，历史值仍在旧代。
	f.exec(`UPDATE catalog_projection_state SET target_generation=target_generation+1,build_id=gen_random_uuid(),
  status='preparing',manifest_sealed=FALSE,catalog_epoch=catalog_epoch+1`)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	state := f.state()
	conn := f.lease()
	if err := discover(f.ctx, conn, state); err != nil {
		t.Fatal(err)
	}
	f.batch(conn, state, 10, 50)
	// 保持维护会话锁，模拟批间让出；真实扫描应不受选主锁阻断。
	if err := os.Rename(oldPath, filepath.Join(root, "Beyoncé.wav")); err != nil {
		t.Fatal(err)
	}
	scan(t, f.ctx, service)
	var currentID, title string
	if err := f.db.QueryRowContext(f.ctx, `SELECT id::text,title FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'`, rootID).Scan(&currentID, &title); err != nil {
		t.Fatal(err)
	}
	if currentID != trackID || !strings.Contains(title, "Beyoncé") {
		t.Fatalf("rescan identity/title=%s %q", currentID, title)
	}
	var projectedID string
	if err := f.db.QueryRowContext(f.ctx, `SELECT id::text FROM catalog_search_values WHERE track_id=$1::uuid AND field='track_title' AND normalized_value='beyonce' AND projection_generation=$2`, trackID, state.TargetGeneration).Scan(&projectedID); err != nil {
		t.Fatal(err)
	}
	if f.state().Ready() {
		t.Fatal("scan published unfinished build")
	}
	// 清单后新 UUID 由同一实际扫描写入，发布不能漏掉。
	writeWAV(t, filepath.Join(root, "新曲.wav"))
	scan(t, f.ctx, service)
	if err := f.db.QueryRowContext(f.ctx, `SELECT id::text FROM catalog_search_values WHERE track_id=$1::uuid AND field='track_title'`, trackID).Scan(&projectedID); err != nil {
		t.Fatal(err)
	}
	releaseLease(conn, f.leaseKey)
	var logs bytes.Buffer
	if err := Run(f.ctx, f.db, slog.New(slog.NewJSONHandler(&logs, nil)), artistTestOptions()); err != nil {
		t.Fatal(err)
	}
	if !f.state().Ready() {
		t.Fatal("not ready")
	}
	if f.count(`SELECT count(*) FROM catalog_search_values WHERE id=$1::uuid`, projectedID) != 1 {
		t.Fatal("backfill rewrote fresh scan title")
	}
	if f.count(`SELECT count(*) FROM tracks t JOIN catalog_projection_subject_state s ON s.subject_kind='track' AND s.subject_id=t.id
  WHERE t.source_root_id=$1::uuid AND s.search_applied_generation=$2 AND s.search_applied_input_revision=s.input_revision`, rootID, state.TargetGeneration) != 2 {
		t.Fatal("new object was missed")
	}
	if !strings.Contains(logs.String(), `"skipped_current"`) || !strings.Contains(logs.String(), `"live_updated"`) {
		t.Fatal("batch progress logs missing")
	}
	// 完成扫描的 missing 对账在发布后仍维持标记；搜索值不删除。
	if err := os.Remove(filepath.Join(root, "Beyoncé.wav")); err != nil {
		t.Fatal(err)
	}
	scan(t, f.ctx, service)
	if f.count(`SELECT count(*) FROM tracks t JOIN catalog_projection_subject_state s ON s.subject_id=t.id AND s.subject_kind='track'
  WHERE t.id=$1::uuid AND t.source_status='missing' AND s.input_revision=s.search_applied_input_revision`, trackID) != 1 {
		t.Fatal("missing transaction broke completion")
	}
	if f.count(`SELECT count(*) FROM catalog_search_values WHERE id=$1::uuid`, projectedID) != 1 {
		t.Fatal("missing reconciliation rewrote retained text")
	}
}

func TestPostgreSQLCatalogBackfillSingleWriterCancellation(t *testing.T) {
	f := newFixture(t, 4)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	// 一个维护者持有专用会话锁；第二个只能等待，取消不得发现清单或写入投影。
	owner := f.lease()
	ctx, cancel := context.WithTimeout(f.ctx, 80*time.Millisecond)
	err := Run(ctx, f.db, slog.New(slog.NewTextHandler(io.Discard, nil)), artistTestOptions())
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		releaseLease(owner, f.leaseKey)
		t.Fatalf("竞争者取消结果：%v", err)
	}
	if f.count(`SELECT count(*) FROM catalog_projection_work`) != 0 {
		releaseLease(owner, f.leaseKey)
		t.Fatal("竞争者绕过维护选主锁")
	}
	releaseLease(owner, f.leaseKey)
	// 原会话释放后另一 worker 可完成；完成后连接回池也不能带残留会话锁。
	f.run()
	if !f.state().Ready() {
		t.Fatal("取消竞争者影响了后续发布")
	}
	owner = f.lease()
	releaseLease(owner, f.leaseKey)
}

func TestPostgreSQLCatalogBackfillVersionUpgradeRejectsStaleBuild(t *testing.T) {
	f := newFixture(t, 3)
	f.run()
	previous := f.state()
	// 模拟上次二进制留下不同的规则版本。登记新规则必须关闭读取并开启新代次。
	f.exec(`UPDATE catalog_projection_state SET textnorm_version=textnorm_version+1,
  published_textnorm_version=published_textnorm_version+1`)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	current := f.state()
	if current.Ready() || current.ManifestSealed || current.TargetGeneration != previous.TargetGeneration+1 || current.CatalogEpoch != previous.CatalogEpoch+1 || current.BuildID == previous.BuildID {
		t.Fatalf("版本登记未隔离旧代：%+v", current)
	}
	conn := f.lease()
	if _, err := processBatch(f.ctx, conn, previous, 10, 50); !errors.Is(err, errBuildChanged) {
		releaseLease(conn, f.leaseKey)
		t.Fatalf("旧任务不能写新代：%v", err)
	}
	releaseLease(conn, f.leaseKey)
	f.run()
	if !f.state().Ready() || f.state().CatalogEpoch != previous.CatalogEpoch+2 {
		t.Fatal("新代未原子发布")
	}
	if f.count(`SELECT count(*) FROM catalog_projection_subject_state WHERE search_applied_generation=$1 AND search_applied_input_revision=input_revision`, current.TargetGeneration) != 4 {
		t.Fatal("升级缺少subject完成标记")
	}
}

func TestPostgreSQLCatalogBackfillOwnerCancellationReleasesLease(t *testing.T) {
	f := newFixture(t, 3)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	holder, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if err = writegate.Lock(f.ctx, holder); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, f.db, slog.New(slog.NewTextHandler(io.Discard, nil)), artistTestOptions()) }()
	defer cancel()
	// 清单建立不占CATP；看到封闭清单后，拥有维护锁的worker必然在等测试gate。
	deadline := time.After(5 * time.Second)
	for !f.state().ManifestSealed {
		select {
		case <-deadline:
			cancel()
			_ = holder.Rollback()
			<-done
			t.Fatal("后台未建立清单")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("拥有者取消：%v", err)
		}
	case <-time.After(5 * time.Second):
		_ = holder.Rollback()
		t.Fatal("取消未解除worker的等待")
	}
	if err = holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.run()
	if !f.state().Ready() {
		t.Fatal("维护锁残留导致后继worker无法发布")
	}
}

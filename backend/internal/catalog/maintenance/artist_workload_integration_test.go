package maintenance

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func insertArtistWorkloadRelease(t *testing.T, f *fixture, ordinal, tracks int, status, artist string) string {
	t.Helper()
	id := fmt.Sprintf("10000000-0000-4000-8000-%012d", ordinal)
	f.emptyRelease(id)
	medium := uuid.New()
	f.exec(`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, medium, id)
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,source_kind,observed_at)
 SELECT gen_random_uuid(),$1::uuid,n,'曲目'||n,$4,$2::uuid,$3||'/'||n||'.flac',$5,'flac_vorbis_comment',NOW()
 FROM generate_series(1,$6::int)n`, medium, f.root, id, artist, status, tracks)
	return id
}

func estimateArtistWorkloadForTest(t *testing.T, f *fixture, id string, budget artistReleaseBudget) artistReleaseCost {
	t.Helper()
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = writegate.Lock(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	cost, err := estimateArtistReleaseWork(f.ctx, tx, id, budget)
	if err != nil {
		t.Fatal(err)
	}
	return cost
}

func TestPostgreSQLArtistWorkloadEstimatesRawInputsIncludingMissing(t *testing.T) {
	f := emptyArtistFixture(t, 0)
	id := insertArtistWorkloadRelease(t, f, 1, 3, "present", "甲 / 乙")
	f.exec(`UPDATE releases SET artist='发行甲 / 发行乙',album_artist='专辑甲' WHERE id=$1::uuid`, id)
	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid) AND position=3`, id)
	var missing string
	if err := f.db.QueryRowContext(f.ctx, `SELECT t.id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid AND t.position=3`, id).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','作曲甲 / 作曲乙',1),($1::uuid,'performer','演奏者',1)`, missing)
	f.exec(`INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind)
 VALUES($1::uuid,'track_artist','ARTIST',0,'标签甲 / 标签乙','flac_vorbis_comment'),
 ($1::uuid,'track_artist','ARTIST',1,'标签丙','flac_vorbis_comment')`, missing)
	scan := uuid.New()
	f.exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scan)
	f.exec(`INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred)
 VALUES($1::uuid,$2::uuid,'artist','观察甲 / 观察乙','flac_vorbis_comment',false)`, missing, scan)
	// 同schema另一个发行不应进入局部估计。
	insertArtistWorkloadRelease(t, f, 2, 5, "present", "无关甲 / 无关乙 / 无关丙")
	cost := estimateArtistWorkloadForTest(t, f, id, artistReleaseBudget{Tracks: 50, Names: 256})
	// 发行3 + 三轨展示6 + missing轨的credit3/证据3/观察2 = 17个原输入署名。
	if cost != (artistReleaseCost{Tracks: 3, Names: 17, EstimateComplete: true}) {
		t.Fatalf("missing/多人展示/证据/credit/观察估计不完整或串到其他发行：%+v", cost)
	}
	cost = estimateArtistWorkloadForTest(t, f, id, artistReleaseBudget{Tracks: 2, Names: 256})
	if cost.Tracks != 3 || cost.EstimateComplete || cost.Names != 0 {
		t.Fatalf("轨数已超额仍预读全部署名，或漏算missing：%+v", cost)
	}
	cost = estimateArtistWorkloadForTest(t, f, id, artistReleaseBudget{Tracks: 50, Names: 5})
	if cost.Tracks != 3 || cost.Names <= 5 || cost.EstimateComplete {
		t.Fatalf("原输入署名估计没有在预算处停止并标记下界：%+v", cost)
	}
	if f.count(`SELECT count(*) FROM artist_credits`) != 0 || f.count(`SELECT count(*) FROM artist_effective_credits`) != 0 || f.count(`SELECT count(*) FROM artist_projection_releases`) != 0 {
		t.Fatal("工作量估计依赖或写入了尚不存在的派生事实")
	}
}

func TestPostgreSQLArtistWorkloadPrefixOversizedSubjectAndMarkerResume(t *testing.T) {
	f := emptyArtistFixture(t, 0)
	small := insertArtistWorkloadRelease(t, f, 1, 4, "present", "Alice / Bob")
	large := insertArtistWorkloadRelease(t, f, 2, 100, "missing", "Alice / Bob")
	after := insertArtistWorkloadRelease(t, f, 3, 1, "present", "Alice / Bob")
	empty := insertArtistWorkloadRelease(t, f, 4, 0, "present", "")
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	work := &artistReleaseBatch{Budget: initialArtistReleaseBudget()}
	result, err := processArtistBatch(f.ctx, conn, state, 10, 10, work)
	if err != nil || result.BatchSize != 1 || result.Written != 1 || result.Completed != 1 || !reflect.DeepEqual(work.Selected.Refresh, []string{small}) {
		t.Fatalf("没有在后续大subject之前结束连续前缀：%+v %+v %v", result, work.Selected, err)
	}
	if f.count(`SELECT count(*) FROM artist_projection_releases`) != 1 || f.count(`SELECT count(*) FROM catalog_projection_work WHERE build_id=$1::uuid AND phase='artist_releases' AND completed`, state.BuildID) != 1 {
		t.Fatal("未选中的发行被虚标完成")
	}
	// 初始化不重置已提交marker/阶段，下一批从完整的大subject继续。
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if f.artistState().BuildID != state.BuildID {
		t.Fatal("续跑重置构建")
	}
	work = &artistReleaseBatch{Budget: initialArtistReleaseBudget()}
	result, err = processArtistBatch(f.ctx, conn, state, 10, 10, work)
	if err != nil || result.BatchSize != 1 || result.Written != 1 || result.Completed != 2 || work.Selected.Tracks != 100 || !work.Selected.Oversized || work.Selected.EstimateComplete || !reflect.DeepEqual(work.Selected.Refresh, []string{large}) {
		t.Fatalf("首个大subject被跳过、截断或混入后续发行：%+v %+v %v", result, work.Selected, err)
	}
	if f.count(`SELECT count(*) FROM artist_projection_releases WHERE release_id=$1::uuid AND applied_generation=$2`, large, state.TargetGeneration) != 1 || f.count(`SELECT count(DISTINCT t.id) FROM tracks t JOIN media m ON m.id=t.medium_id JOIN artist_effective_credits c ON c.track_id=t.id WHERE m.release_id=$1::uuid AND t.source_status='missing' AND c.role='track_artist'`, large) != 100 || f.count(`SELECT count(*) FROM artist_effective_credits c JOIN tracks t ON t.id=c.track_id JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid AND c.role='track_artist'`, large) != 200 {
		t.Fatal("大subject marker未与全部100条missing轨/200署名同事务完成")
	}
	if f.count(`SELECT count(*) FROM artist_projection_releases WHERE release_id=ANY($1::uuid[])`, []string{after, empty}) != 0 {
		t.Fatal("大subject批提前标记了后续发行")
	}
	work = &artistReleaseBatch{Budget: initialArtistReleaseBudget()}
	result, err = processArtistBatch(f.ctx, conn, state, 10, 10, work)
	if err != nil || result.BatchSize != 2 || result.Written != 2 || result.Completed != 4 || !reflect.DeepEqual(work.Selected.Refresh, []string{after, empty}) || work.Selected.Oversized {
		t.Fatalf("大subject之后的普通/零署名发行没有续跑：%+v %+v %v", result, work.Selected, err)
	}
	if f.count(`SELECT count(*) FROM artist_projection_releases WHERE applied_generation=$1`, state.TargetGeneration) != 4 {
		t.Fatal("零署名或此前已完成发行丢失marker")
	}
}

func TestPostgreSQLArtistWorkloadEstimateFailureIsAtomicAndCurrentSkipsInputs(t *testing.T) {
	f := emptyArtistFixture(t, 0)
	current := insertArtistWorkloadRelease(t, f, 1, 1, "present", "Alice")
	pending := insertArtistWorkloadRelease(t, f, 2, 1, "present", "Bob")
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = writegate.Lock(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = artistgraph.Refresh(f.ctx, tx, []string{current}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var credit string
	if err = f.db.QueryRowContext(f.ctx, `SELECT c.id::text FROM artist_credits c JOIN tracks t ON t.id=c.track_id JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid AND c.role='track_artist'`, current).Scan(&credit); err != nil {
		t.Fatal(err)
	}
	f.exec(`ALTER TABLE artist_tag_evidence RENAME TO workload_evidence_unavailable`)
	inputsUnavailable := true
	defer func() {
		if inputsUnavailable {
			f.exec(`ALTER TABLE workload_evidence_unavailable RENAME TO artist_tag_evidence`)
		}
	}()
	work := &artistReleaseBatch{Budget: initialArtistReleaseBudget()}
	if _, err = processArtistBatch(f.ctx, conn, state, 2, 10, work); err == nil {
		t.Fatal("输入估计SQL异常被吞掉")
	}
	if f.count(`SELECT completed FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_releases'`, state.BuildID) != 0 || f.count(`SELECT count(*) FROM catalog_projection_work WHERE build_id=$1::uuid AND completed`, state.BuildID) != 0 || f.count(`SELECT count(*) FROM artist_projection_releases WHERE release_id=$1::uuid`, pending) != 0 || len(work.Selected.IDs) != 0 {
		t.Fatal("估计失败提交了部分进度/marker/统计")
	}
	// 输入表仍不可用，但本代current对象应仅标任务完成，不读取输入或再次刷新。
	result, err := processArtistBatch(f.ctx, conn, state, 1, 10, work)
	if err != nil || result.BatchSize != 1 || result.Written != 0 || result.Completed != 1 || !reflect.DeepEqual(work.Selected.IDs, []string{current}) || work.Selected.Tracks != 0 || work.Selected.Names != 0 {
		t.Fatalf("在线current项仍读取输入/消耗预算：%+v %+v %v", result, work.Selected, err)
	}
	if f.count(`SELECT count(*) FROM artist_credits WHERE id=$1::uuid`, credit) != 1 {
		t.Fatal("后台覆盖了在线完成的署名")
	}
	f.exec(`ALTER TABLE workload_evidence_unavailable RENAME TO artist_tag_evidence`)
	inputsUnavailable = false
	work = &artistReleaseBatch{Budget: initialArtistReleaseBudget()}
	result, err = processArtistBatch(f.ctx, conn, state, 2, 10, work)
	if err != nil || result.Written != 1 || result.Completed != 2 || !reflect.DeepEqual(work.Selected.Refresh, []string{pending}) || f.count(`SELECT count(*) FROM artist_projection_releases WHERE applied_generation=$1`, state.TargetGeneration) != 2 {
		t.Fatalf("输入恢复后未沿原marker续跑：%+v %+v %v", result, work.Selected, err)
	}
}

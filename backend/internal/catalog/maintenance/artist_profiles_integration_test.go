package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
)

func (f *fixture) finishArtistBatches(conn *sql.Conn, state artistState) {
	f.t.Helper()
	for i := 0; i < 1000; i++ {
		if f.artistBatch(conn, state, 10).BatchSize == 0 {
			return
		}
	}
	f.t.Fatal("艺人批次未耗尽")
}

func emptyArtistFixture(t *testing.T, artists int) *fixture {
	t.Helper()
	f := newFixture(t, 0)
	f.exec(`DELETE FROM media WHERE id=$1::uuid`, f.medium)
	f.exec(`DELETE FROM releases WHERE id=$1::uuid`, f.release)
	f.exec(`INSERT INTO artists(id,initial_name)
 SELECT ('00000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,'孤立身份'||n FROM generate_series(1,$1::int)n`, artists)
	return f
}

func TestPostgreSQLArtistProfilesBoundedRestartLiveSkipAndLateUUID(t *testing.T) {
	f := emptyArtistFixture(t, 25)
	state := f.artistState()
	conn := f.lease()
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	total := f.count(`SELECT count(*) FROM artists`)
	result, err := processArtistBatch(f.ctx, conn, state, 100, 3)
	if err != nil || result.Kind != "artist" || result.Phase != "artist_profiles" || result.BatchSize != 3 || result.Written != 3 || result.Completed != 3 || result.Total != total {
		releaseLease(conn, f.leaseKey)
		t.Fatalf("身份阶段未使用独立限额：%+v %v", result, err)
	}
	if f.count(`SELECT count(*) FROM artist_profiles`) != 3 {
		releaseLease(conn, f.leaseKey)
		t.Fatal("短批刷新了清单限额外的身份")
	}
	// 专用连接仍持 CATM，另一连接却能获取 CATP，证明批结束已让出写 gate。
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(f.ctx, `SET LOCAL lock_timeout='1s'`); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	if err = writegate.Lock(f.ctx, tx); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	liveID := "00000000-0000-4000-8000-000000000005"
	if err = artistgraph.RefreshProfiles(f.ctx, tx, []string{liveID}); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	var nameID string
	if err = f.db.QueryRowContext(f.ctx, `SELECT id::text FROM artist_search_names WHERE artist_id=$1::uuid`, liveID).Scan(&nameID); err != nil {
		releaseLease(conn, f.leaseKey)
		t.Fatal(err)
	}
	releaseLease(conn, f.leaseKey)
	if err = Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if f.artistState().BuildID != state.BuildID || f.count(`SELECT completed FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_profiles'`, state.BuildID) != 3 {
		t.Fatal("重启丢失身份阶段进度")
	}
	conn = f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err = discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	result, err = processArtistBatch(f.ctx, conn, state, 100, 3)
	if err != nil || result.BatchSize != 3 || result.Written != 2 || result.Completed != 6 || result.Total != total {
		t.Fatalf("在线已完成身份未跳过：%+v %v", result, err)
	}
	if f.count(`SELECT count(*) FROM artist_search_names WHERE id=$1::uuid`, nameID) != 1 {
		t.Fatal("身份批覆盖了在线已完成的搜索名字")
	}
	f.finishArtistBatches(conn, state)
	lateID := "00000000-0000-4000-8000-000000000000"
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'清单之后的新身份')`, lateID)
	if err = runSearchLocked(f.ctx, conn, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	published, err := publishArtistsOrRepair(f.ctx, conn, state, artistTestOptions())
	if err != nil || published {
		t.Fatalf("发布遗漏新UUID：%v %v", published, err)
	}
	result = f.artistBatch(conn, state, 3)
	if result.Kind != "artist" || !strings.HasPrefix(result.Phase, "repair_artist_profiles_") || result.Total != 1 || result.Written != 1 {
		t.Fatalf("身份补缺口阶段错误：%+v", result)
	}
	if f.count(`SELECT total FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_profiles'`, state.BuildID) != total {
		t.Fatal("修复阶段改写了初始分母")
	}
	published, err = publishArtistsOrRepair(f.ctx, conn, state, artistTestOptions())
	if err != nil || !published || f.count(`SELECT count(*) FROM artist_profiles WHERE applied_generation=$1`, state.TargetGeneration) != total+1 {
		t.Fatalf("完整身份派生未发布：%v %v", published, err)
	}
}

func TestPostgreSQLArtistProfilesResumeSealedOldBuild(t *testing.T) {
	f := emptyArtistFixture(t, 12)
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	// 模拟升级前已封闭、只具备发行清单的进行中构建。
	f.exec(`DELETE FROM catalog_projection_work WHERE build_id=$1::uuid AND subject_kind='artist'`, state.BuildID)
	f.exec(`DELETE FROM catalog_projection_phases WHERE build_id=$1::uuid AND subject_kind='artist'`, state.BuildID)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if !f.artistState().ManifestSealed || f.count(`SELECT count(*) FROM catalog_projection_phases WHERE build_id=$1::uuid AND subject_kind='artist'`, state.BuildID) != 0 {
		t.Fatal("短启动发现了身份或重置旧清单")
	}
	if err := runLocked(f.ctx, conn, slog.New(slog.NewTextHandler(io.Discard, nil)), artistTestOptions()); err != nil {
		t.Fatal(err)
	}
	if !f.artistState().Ready() || f.artistState().BuildID != state.BuildID || f.count(`SELECT count(*) FROM artist_profiles WHERE applied_generation=$1`, state.TargetGeneration) != f.count(`SELECT count(*) FROM artists`) {
		t.Fatal("旧sealed构建未补齐身份阶段")
	}
}

func TestPostgreSQLArtistProfilesReadyZeroMarkerWaitsForNextBuild(t *testing.T) {
	f := emptyArtistFixture(t, 2)
	f.run()
	previous := f.artistState()
	f.exec(`UPDATE artist_profiles SET applied_generation=0`)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	f.run()
	if f.artistState() != previous || f.count(`SELECT count(*) FROM artist_profiles WHERE applied_generation<>0`) != 0 {
		t.Fatal("仅因新增marker而重建了原ready目录")
	}
	f.exec(`UPDATE catalog_projection_state SET target_generation=target_generation+1,build_id=gen_random_uuid(),status='preparing',manifest_sealed=FALSE,catalog_epoch=catalog_epoch+1 WHERE capability='artists'`)
	f.run()
	current := f.artistState()
	if !current.Ready() || current.TargetGeneration != previous.TargetGeneration+1 || current.SearchProjectionVersion != previous.SearchProjectionVersion || f.count(`SELECT count(*) FROM artist_profiles WHERE applied_generation=$1`, current.TargetGeneration) != f.count(`SELECT count(*) FROM artists`) {
		t.Fatal("下一代构建未补齐旧0 marker，或错误改变投影版本")
	}
}

func TestPostgreSQLArtistProfilesFailureRollsBackWholeBatch(t *testing.T) {
	f := emptyArtistFixture(t, 2)
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	f.exec(`ALTER TABLE artist_profiles ADD CONSTRAINT profile_failure CHECK(artist_id<>'00000000-0000-4000-8000-000000000002'::uuid)`)
	if _, err := processArtistBatch(f.ctx, conn, state, 10, 2); err == nil {
		t.Fatal("测试失败注入未生效")
	}
	if f.count(`SELECT count(*) FROM artist_profiles`) != 0 || f.count(`SELECT count(*) FROM artist_search_names`) != 0 || f.count(`SELECT completed FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_profiles'`, state.BuildID) != 0 {
		t.Fatal("失败批留下部分身份派生/完成进度")
	}
	f.exec(`ALTER TABLE artist_profiles DROP CONSTRAINT profile_failure`)
	result := f.artistBatch(conn, state, 2)
	if result.BatchSize != 2 || result.Written != 2 || result.Completed != 2 {
		t.Fatalf("失败批无法续跑：%+v", result)
	}
}

func TestPostgreSQLArtistProfilesPublishOnlyChecksMissingAndStale(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%v", missing), func(t *testing.T) {
			f := emptyArtistFixture(t, 1)
			state := f.artistState()
			conn := f.lease()
			defer releaseLease(conn, f.leaseKey)
			if err := runSearchLocked(f.ctx, conn, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
				t.Fatal(err)
			}
			if err := discoverArtists(f.ctx, conn, state, false); err != nil {
				t.Fatal(err)
			}
			f.finishArtistBatches(conn, state)
			id := "00000000-0000-4000-8000-000000000001"
			if missing {
				f.exec(`DELETE FROM artist_profiles WHERE artist_id=$1::uuid`, id)
			} else {
				f.exec(`UPDATE artist_profiles SET applied_generation=0 WHERE artist_id=$1::uuid`, id)
			}
			calls := 0
			options := RunOptions{RefreshArtistResolutions: func(context.Context, *sql.Tx, map[string]string) error { calls++; return nil }}
			published, err := publishArtistsOrRepair(f.ctx, conn, state, options)
			if err != nil || published || calls != 0 || f.count(`SELECT count(*) FROM artist_profiles WHERE artist_id=$1::uuid AND applied_generation=$2`, id, state.TargetGeneration) != 0 {
				t.Fatalf("发布越过缺口或在发布内重算：published=%v calls=%d err=%v", published, calls, err)
			}
			result := f.artistBatch(conn, state, 2)
			if result.Kind != "artist" || result.Written != 1 || result.Total != 1 {
				t.Fatalf("未仅修补单一身份缺口：%+v", result)
			}
			published, err = publishArtistsOrRepair(f.ctx, conn, state, options)
			if err != nil || !published || calls != 1 {
				t.Fatalf("补齐后未原子发布：%v %v", published, err)
			}
		})
	}
}

func TestPostgreSQLArtistProfilesCancellationRollsBackAndResumes(t *testing.T) {
	f := emptyArtistFixture(t, 2)
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn, f.leaseKey)
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	var workerPID, blockerPID int
	if err := conn.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&workerPID); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if err = blocker.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.ExecContext(f.ctx, `SELECT pg_advisory_xact_lock(101010,$1)`, workerPID); err != nil {
		t.Fatal(err)
	}
	// 测试专有屏障停在第二个身份：第一条INSERT已经执行，但尚未提交。
	f.exec(fmt.Sprintf(`CREATE FUNCTION profile_batch_barrier() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.artist_id='00000000-0000-4000-8000-000000000002'::uuid THEN
 PERFORM pg_advisory_xact_lock(101010,%d); END IF; RETURN NEW; END $$`, workerPID))
	f.exec(`CREATE TRIGGER profile_batch_barrier BEFORE INSERT ON artist_profiles FOR EACH ROW EXECUTE FUNCTION profile_batch_barrier()`)
	ctx, cancel := context.WithCancel(f.ctx)
	result, exited := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(exited)
		_, err := processArtistBatch(ctx, conn, state, 10, 2)
		result <- err
	}()
	defer func() { cancel(); <-exited }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
waitForBarrier:
	for {
		var blocked bool
		if err = f.db.QueryRowContext(f.ctx, `SELECT $1::int=ANY(pg_blocking_pids($2))`, blockerPID, workerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break waitForBarrier
		}
		select {
		case err := <-result:
			t.Fatalf("身份批未到屏障即退出：%v", err)
		case <-deadline.C:
			t.Fatal("没有观察到测试屏障阻塞链")
		case <-ticker.C:
		}
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("已取消的身份批仍提交成功")
	}
	if err = blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM artist_profiles`) != 0 || f.count(`SELECT count(*) FROM artist_search_names`) != 0 || f.count(`SELECT completed FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_profiles'`, state.BuildID) != 0 {
		t.Fatal("取消留下半批身份派生或完成进度")
	}
	f.exec(`DROP TRIGGER profile_batch_barrier ON artist_profiles`)
	f.exec(`DROP FUNCTION profile_batch_barrier()`)
	// 释放旧专用连接选主锁，模拟重启后的另一worker续跑。
	if _, err = conn.ExecContext(f.ctx, `SELECT pg_advisory_unlock($1::integer,1)`, maintenanceNamespace); err != nil && err != sql.ErrConnDone {
		// 取消可能已使驱动丢弃连接；Close 同样释放其会话锁。
		_ = conn.Close()
	}
	f.run()
	if !f.artistState().Ready() || f.artistState().BuildID != state.BuildID {
		t.Fatal("取消后未沿原构建续跑发布")
	}
}

func TestPostgreSQLArtistProfilesEmptySearchNamesStillComplete(t *testing.T) {
	f := emptyArtistFixture(t, 1)
	// 存储允许的历史空白主名规范化后无搜索行，完成判定不能依赖名字行数。
	f.exec(`UPDATE artists SET initial_name='   ' WHERE id='00000000-0000-4000-8000-000000000001'::uuid`)
	f.run()
	if !f.artistState().Ready() || f.count(`SELECT count(*) FROM artist_profiles WHERE artist_id='00000000-0000-4000-8000-000000000001'::uuid AND applied_generation=$1`, f.artistState().TargetGeneration) != 1 || f.count(`SELECT count(*) FROM artist_search_names WHERE artist_id='00000000-0000-4000-8000-000000000001'::uuid`) != 0 {
		t.Fatal("零搜索行身份未用独立marker完成")
	}
}

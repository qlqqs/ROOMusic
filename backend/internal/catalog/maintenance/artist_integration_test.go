package maintenance

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 维护包只验证端口的事务合同；收藏持久化由 personal 自己的集成测试覆盖。
func artistTestOptions() RunOptions {
	return RunOptions{RefreshArtistResolutions: func(context.Context, *sql.Tx, map[string]string) error { return nil }}
}

func (f *fixture) artistState() artistState {
	f.t.Helper()
	state, err := loadArtistState(f.ctx, f.db)
	if err != nil {
		f.t.Fatal(err)
	}
	return state
}

func (f *fixture) emptyRelease(id string) {
	f.t.Helper()
	group := uuid.New()
	f.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	f.exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,candidate_kind,candidate_anchor)
 VALUES($1::uuid,$2::uuid,'','','',$3::uuid,'ordinary_directory',$4)`, id, group, f.root, id)
}

func (f *fixture) artistBatch(conn *sql.Conn, state artistState, limit int) batchResult {
	f.t.Helper()
	result, err := processArtistBatch(f.ctx, conn, state, limit, limit)
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func TestPostgreSQLArtistBackfillRestartCurrentMarkersAndLateUUID(t *testing.T) {
	f := newFixture(t, 2)
	emptyID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	f.emptyRelease(emptyID)
	before := f.artistState()
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM catalog_projection_work`) != 0 || f.count(`SELECT count(*) FROM artist_projection_releases`) != 0 {
		t.Fatal("启动路径执行了对象发现或回填")
	}
	state := f.artistState()
	if state.BuildID != before.BuildID || state.CatalogEpoch != before.CatalogEpoch {
		t.Fatal("同版本启动重置艺人任务")
	}
	conn := f.lease()
	if err := runSearchLocked(f.ctx, conn, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	// 实时写者先完成；回填必须保留其署名 ID，不能重复刷新。
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = writegate.Lock(f.ctx, tx); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(f.ctx, `UPDATE releases SET album_artist='新署名' WHERE id=$1::uuid`, f.release); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	if err = artistgraph.Refresh(f.ctx, tx, []string{f.release}); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	var creditID string
	if err = f.db.QueryRowContext(f.ctx, `SELECT id::text FROM artist_credits WHERE release_id=$1::uuid AND role='album_artist' LIMIT 1`, f.release).Scan(&creditID); err != nil {
		releaseLease(conn)
		t.Fatal(err)
	}
	result := f.artistBatch(conn, state, 1)
	if result.BatchSize != 1 || result.Written != 0 || result.Completed != 1 || result.Total != 2 {
		releaseLease(conn)
		t.Fatalf("未跳过在线写者：%+v", result)
	}
	if f.count(`SELECT count(*) FROM artist_credits WHERE id=$1::uuid`, creditID) != 1 {
		releaseLease(conn)
		t.Fatal("覆盖了在线署名")
	}
	releaseLease(conn)
	if err = Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if f.artistState().BuildID != state.BuildID || f.count(`SELECT completed FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_releases'`, state.BuildID) != 1 {
		t.Fatal("重启丢失已提交进度")
	}
	conn = f.lease()
	defer releaseLease(conn)
	result = f.artistBatch(conn, state, 1)
	if result.Written != 1 || f.count(`SELECT count(*) FROM artist_projection_releases WHERE release_id=$1::uuid AND applied_generation=$2`, emptyID, state.TargetGeneration) != 1 {
		t.Fatal("零署名发行缺少完成标记")
	}
	f.finishArtistBatches(conn, state)
	// 在初始清单之后插入最小 UUID，不能靠最大 UUID 游标宣告完成。
	lateID := "00000000-0000-4000-8000-000000000001"
	f.emptyRelease(lateID)
	published, err := publishArtistsOrRepair(f.ctx, conn, state, artistTestOptions())
	if err != nil || published {
		t.Fatalf("遗漏新 UUID：%v %v", published, err)
	}
	result = f.artistBatch(conn, state, 10)
	if !strings.HasPrefix(result.Phase, "repair_artists_") || result.Total != 1 || result.Written != 1 {
		t.Fatalf("补漏阶段错误：%+v", result)
	}
	published, err = publishArtistsOrRepair(f.ctx, conn, state, artistTestOptions())
	if err != nil || !published {
		t.Fatalf("发布失败：%v %v", published, err)
	}
	after := f.artistState()
	if !after.Ready() || after.CatalogEpoch != state.CatalogEpoch+1 {
		t.Fatalf("发布状态错误：%+v", after)
	}
	if f.count(`SELECT count(*) FROM catalog_projection_work WHERE build_id=$1::uuid AND NOT completed`, state.BuildID) != 0 {
		t.Fatal("发布后还有待处理项")
	}
}

func TestPostgreSQLArtistBackfillPublishRequiresAtomicFavoriteRefresh(t *testing.T) {
	f := newFixture(t, 1)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn)
	if err := runSearchLocked(f.ctx, conn, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	f.finishArtistBatches(conn, state)
	published, err := publishArtistsOrRepair(f.ctx, conn, state, RunOptions{})
	if published || !errors.Is(err, ErrArtistResolutionRefreshRequired) {
		t.Fatalf("缺少回调仍发布：%v %v", published, err)
	}
	// 用测试自有表证明回调和发布属于同一事务，不访问 personal 的私有表。
	f.exec(`CREATE TABLE artist_publish_probe(anchor UUID PRIMARY KEY,canonical UUID NOT NULL)`)
	failure := errors.New("测试刷新失败")
	calls := 0
	refresh := func(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
		calls++
		var ready bool
		if err := tx.QueryRowContext(ctx, `SELECT status='ready' FROM catalog_projection_state WHERE capability='artists'`).Scan(&ready); err != nil {
			return err
		}
		if ready {
			t.Error("回调之前已经发布")
		}
		var total int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM artists`).Scan(&total); err != nil {
			return err
		}
		if len(mapping) != total {
			t.Errorf("映射不是完整集合：%d != %d", len(mapping), total)
		}
		for anchor, canonical := range mapping {
			if _, err := tx.ExecContext(ctx, `INSERT INTO artist_publish_probe VALUES($1::uuid,$2::uuid)`, anchor, canonical); err != nil {
				return err
			}
		}
		if calls == 1 {
			return failure
		}
		return nil
	}
	options := RunOptions{RefreshArtistResolutions: refresh}
	published, err = publishArtistsOrRepair(f.ctx, conn, state, options)
	if published || !errors.Is(err, failure) {
		t.Fatalf("失败回调被吞掉：%v %v", published, err)
	}
	if f.artistState().Ready() || f.count(`SELECT count(*) FROM artist_publish_probe`) != 0 {
		t.Fatal("失败发布留下部分收藏缓存")
	}
	if f.artistState().CatalogEpoch != state.CatalogEpoch {
		t.Fatal("失败事务递增 epoch")
	}
	published, err = publishArtistsOrRepair(f.ctx, conn, state, options)
	if err != nil || !published || calls != 2 {
		t.Fatalf("发布未恢复：%v %v calls=%d", published, err, calls)
	}
	if f.count(`SELECT count(*) FROM artist_publish_probe`) != f.count(`SELECT count(*) FROM artists`) {
		t.Fatal("发布缺少完整收藏解析")
	}
}

func TestPostgreSQLArtistBackfillVersionAndWorkerOrdering(t *testing.T) {
	f := newFixture(t, 1)
	f.run()
	previous := f.artistState()
	search := f.state()
	f.exec(`UPDATE catalog_projection_state SET search_projection_version=search_projection_version+1,
 published_search_projection_version=published_search_projection_version+1 WHERE capability='artists'`)
	if err := Initialize(f.ctx, f.db); err != nil {
		t.Fatal(err)
	}
	state := f.artistState()
	if state.Ready() || state.ManifestSealed || state.BuildID == previous.BuildID || state.TargetGeneration != previous.TargetGeneration+1 || state.CatalogEpoch != previous.CatalogEpoch+1 {
		t.Fatalf("艺人升级未登记：%+v", state)
	}
	if f.state() != search {
		t.Fatal("艺人版本升级影响了已发布搜索")
	}
	conn := f.lease()
	if _, err := processArtistBatch(f.ctx, conn, previous, 1, 1); !errors.Is(err, errBuildChanged) {
		releaseLease(conn)
		t.Fatalf("旧构建仍可写：%v", err)
	}
	releaseLease(conn)
	var logs bytes.Buffer
	calls := 0
	options := RunOptions{RefreshArtistResolutions: func(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
		calls++
		var ready bool
		if err := tx.QueryRowContext(ctx, `SELECT status='ready' FROM catalog_projection_state WHERE capability='search'`).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			t.Error("搜索未完成就发布艺人")
		}
		return nil
	}}
	if err := Run(f.ctx, f.db, slog.New(slog.NewJSONHandler(&logs, nil)), options); err != nil {
		t.Fatal(err)
	}
	if !f.artistState().Ready() || calls != 1 {
		t.Fatal("未串行发布艺人")
	}
	for _, field := range []string{`"capability":"artists"`, `"phase":"artist_profiles"`, `"total"`, `"completed"`, `"skipped_current"`, `"batch_ms"`, `"elapsed_ms"`} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("缺少进度字段 %s", field)
		}
	}
	if err := Run(f.ctx, f.db, slog.New(slog.NewTextHandler(io.Discard, nil)), options); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || f.artistState().CatalogEpoch != previous.CatalogEpoch+2 {
		t.Fatal("同版本重启重复刷新或发布")
	}
}

func TestPostgreSQLArtistBackfillMissingCallbackReleasesLease(t *testing.T) {
	f := newFixture(t, 1)
	err := Run(f.ctx, f.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, ErrArtistResolutionRefreshRequired) {
		t.Fatalf("缺少回调没有返回明确装配错误：%v", err)
	}
	if !f.state().Ready() || f.artistState().Ready() {
		t.Fatal("收藏回调缺失影响搜索或提前发布艺人")
	}
	conn := f.lease()
	releaseLease(conn)
	f.run()
	if !f.artistState().Ready() {
		t.Fatal("补充回调后没有续跑发布")
	}
}

func TestPostgreSQLArtistBackfillEmptyCatalogKeepsSeedProfiles(t *testing.T) {
	f := newFixture(t, 0)
	f.exec(`DELETE FROM media WHERE id=$1::uuid`, f.medium)
	f.exec(`DELETE FROM releases WHERE id=$1::uuid`, f.release)
	f.run()
	if !f.artistState().Ready() {
		t.Fatal("空目录未发布艺人")
	}
	if f.count(`SELECT count(*) FROM artist_profiles`) != f.count(`SELECT count(*) FROM artists`) {
		t.Fatal("空目录的种子艺人缺少展示投影")
	}
}

func TestPostgreSQLArtistBackfillBatchFailureKeepsProgressAndFactsAtomic(t *testing.T) {
	f := newFixture(t, 1)
	state := f.artistState()
	conn := f.lease()
	defer releaseLease(conn)
	if err := discoverArtists(f.ctx, conn, state, false); err != nil {
		t.Fatal(err)
	}
	// 第一个角色可写，第二个角色超过 100 人；整个派生事务必须回滚，不能留下前半份姓名。
	names := make([]string, 101)
	for i := range names {
		names[i] = fmt.Sprintf("姓名%d", i)
	}
	f.exec(`UPDATE releases SET artist='应当回滚的姓名',album_artist=$2 WHERE id=$1::uuid`, f.release, strings.Join(names, " / "))
	before := f.count(`SELECT count(*) FROM artists`)
	if _, err := processArtistBatch(f.ctx, conn, state, 10, 10); err == nil {
		t.Fatal("超预算署名没有失败")
	}
	if f.count(`SELECT count(*) FROM artists`) != before || f.count(`SELECT count(*) FROM artist_projection_releases`) != 0 {
		t.Fatal("失败批次留下部分艺人事实")
	}
	if f.count(`SELECT completed FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_releases'`, state.BuildID) != 0 {
		t.Fatal("失败批次虚增完成数")
	}
	if f.count(`SELECT count(*) FROM catalog_projection_work WHERE build_id=$1::uuid AND completed`, state.BuildID) != 0 {
		t.Fatal("失败批次留下完成任务")
	}
	f.exec(`UPDATE releases SET album_artist='已修复姓名' WHERE id=$1::uuid`, f.release)
	result := f.artistBatch(conn, state, 10)
	if result.Completed != 1 || result.Written != 1 {
		t.Fatalf("重试未修复：%+v", result)
	}
}

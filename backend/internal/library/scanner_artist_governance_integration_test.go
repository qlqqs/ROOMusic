package library

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type scannerArtistResolutionPort struct{}

func (scannerArtistResolutionPort) RefreshArtistResolution(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
	return personal.RefreshArtistResolution(ctx, tx, mapping)
}

// scannerArtistGovernanceFixture 只使用临时目录里的合成 FLAC，扫描经真实解析、暂存、身份规划与候选事务。
type scannerArtistGovernanceFixture struct {
	t       *testing.T
	fixture *libraryFixture
	service *Service
	root    registeredRoot
	actor   string
	ctx     context.Context
}

func (f *scannerArtistGovernanceFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.fixture.database.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *scannerArtistGovernanceFixture) text(query string, args ...any) string {
	f.t.Helper()
	var result string
	if err := f.fixture.database.QueryRowContext(f.ctx, query, args...).Scan(&result); err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *scannerArtistGovernanceFixture) revision(id string) int64 {
	f.t.Helper()
	var revision int64
	if err := f.fixture.database.QueryRowContext(f.ctx, `SELECT edit_revision FROM artists WHERE id=$1::uuid`, id).Scan(&revision); err != nil {
		f.t.Fatal(err)
	}
	return revision
}

func (f *scannerArtistGovernanceFixture) run(kind, fingerprint string, execute operations.Executor) {
	f.t.Helper()
	_, err := operations.Run(f.ctx, f.fixture.database, operations.Spec{ActorID: f.actor, Type: kind, Key: uuid.New(), Fingerprint: fingerprint, RequestID: "scanner-artist-governance"}, execute)
	if err != nil {
		f.t.Fatalf("治理操作 %s：%v", kind, err)
	}
}

func (f *scannerArtistGovernanceFixture) writeAlpha(title string) {
	f.t.Helper()
	writeScanFixtureFile(f.t, f.root, "Alpha/01.flac", parsertest.FLACWithAudio(f.t, [16]byte{1}, []byte{1, 2, 3},
		"TITLE="+title, "ALBUM=Alpha Album", "ALBUMARTIST=Scan Alpha", "ARTIST=Scan Alpha", "TRACKNUMBER=1"))
}

// scan 同时执行 scanRoot 与正式终态处理；取消分支模拟完整遍历后、missing 对账前收到持久化取消。
func (f *scannerArtistGovernanceFixture) scan(want string) string {
	f.t.Helper()
	scanID := insertRunningScan(f.t, f.fixture)
	holder, acquired, err := acquireScanExecution(f.ctx, f.fixture.database)
	if err != nil || !acquired {
		f.t.Fatalf("取得扫描执行权：acquired=%t err=%v", acquired, err)
	}
	defer holder.close(f.ctx)
	scanErr := f.service.scanRoot(f.ctx, holder.connection, scanID, f.root)
	if want == "incomplete" {
		if scanErr == nil {
			f.t.Fatal("注入的候选失败未传播到扫描")
		}
	} else if scanErr != nil {
		f.t.Fatal(scanErr)
	}
	if want == "canceled" {
		f.exec(`UPDATE scan_runs SET cancel_requested_at=NOW() WHERE id=$1::uuid`, scanID)
	}
	status, err := f.service.finalizeScan(f.ctx, holder.connection, scanID, []string{f.root.ID}, scanOutcome{Complete: scanErr == nil})
	if err != nil || status != want {
		f.t.Fatalf("扫描终态=%q，期望%q：%v", status, want, err)
	}
	return scanID
}

func (f *scannerArtistGovernanceFixture) intentSnapshot() string {
	f.t.Helper()
	return f.text(`SELECT jsonb_build_object(
 'bindings',(SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY key_id),'[]') FROM artist_name_bindings b),
 'assignments',(SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM artist_assignment_overrides a),
 'overrides',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY artist_id),'[]') FROM artist_overrides o),
 'ignores',(SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY left_key_id,right_key_id),'[]') FROM artist_candidate_ignores i),
 'revisions',(SELECT jsonb_agg(jsonb_build_array(id,edit_revision) ORDER BY id) FROM artists),
 'operations',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY id),'[]') FROM operations o))::text`)
}

func (f *scannerArtistGovernanceFixture) graphSnapshot() string {
	f.t.Helper()
	// current credit 行会被重建，不把临时 credit_id 当作身份；断言有效 subject/name/artist 关系。
	return f.text(`SELECT COALESCE(jsonb_agg(jsonb_build_array(release_id,track_id,role,name_id,artist_id,position)
 ORDER BY release_id,track_id,role,name_id,artist_id,position),'[]')::text FROM artist_effective_credits`)
}

func (f *scannerArtistGovernanceFixture) trackArtist(trackID string) string {
	f.t.Helper()
	return f.text(`SELECT artist_id::text FROM artist_effective_credits WHERE track_id=$1::uuid AND role='track_artist'`, trackID)
}

func (f *scannerArtistGovernanceFixture) unchangedRescan() {
	f.t.Helper()
	before, graph := f.intentSnapshot(), f.graphSnapshot()
	scanID := f.scan("succeeded")
	if f.intentSnapshot() != before {
		f.t.Fatal("真实重扫覆盖了艺人治理意图、revision 或 operations")
	}
	if f.graphSnapshot() != graph {
		f.t.Fatal("真实重扫改变了有效署名归属")
	}
	assertDatabaseCount(f.t, f.fixture, `SELECT count(DISTINCT track_id) FROM track_observations WHERE scan_run_id=$1::uuid`, 3, scanID)
}

func TestPostgreSQLScannerArtistGovernanceSurvivesRealRescans(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	f := &scannerArtistGovernanceFixture{t: t, fixture: fixture, service: scannerTestApplication(fixture), root: insertScannerRoot(t, fixture, "artist-governance"), actor: uuid.New(), ctx: ctx}
	f.exec(`INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'scan-admin','x')`, f.actor)
	if err := maintenance.Initialize(ctx, fixture.database); err != nil {
		t.Fatal(err)
	}
	f.writeAlpha("Alpha 原标题")
	// 现行候选规则只保留跨发行的括号别名信号；同曲多值 ARTIST 不再产生候选。
	writeScanFixtureFile(t, f.root, "Beta/01.flac", parsertest.FLACWithAudio(t, [16]byte{2}, []byte{4, 5, 6},
		"TITLE=Beta", "ALBUM=Beta Album", "ALBUMARTIST=Candidate Two", "ARTIST=Scan Beta", "TRACKNUMBER=1"))
	writeScanFixtureFile(t, f.root, "Candidates/01.flac", parsertest.FLACWithAudio(t, [16]byte{3}, []byte{7, 8, 9},
		"TITLE=Candidates", "ALBUM=Candidate Album", "ALBUMARTIST=Candidate One (Candidate Two)", "ARTIST=Candidate One (Candidate Two)", "TRACKNUMBER=1"))
	f.scan("succeeded")
	if err := maintenance.Run(ctx, fixture.database, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.RunOptions{RefreshArtistResolutions: personal.RefreshArtistResolution}); err != nil {
		t.Fatal(err)
	}
	governor := governance.NewArtistGovernance(scannerArtistResolutionPort{})
	alpha := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='scan alpha'`)
	beta := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='scan beta'`)
	betaKey := f.text(`SELECT id::text FROM artist_name_keys WHERE normalized_key='scan beta'`)
	alphaTrack := f.text(`SELECT id::text FROM tracks WHERE source_root_id=$1::uuid AND relative_path='Alpha/01.flac'`, f.root.ID)
	betaTrack := f.text(`SELECT id::text FROM tracks WHERE source_root_id=$1::uuid AND relative_path='Beta/01.flac'`, f.root.ID)
	betaGroup := f.text(`SELECT r.group_id::text FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.id=$1::uuid`, betaTrack)

	merge := governance.ArtistMergeRequest{ArtistID: alpha, ExpectedRevision: f.revision(alpha), SourceArtistID: beta, SourceExpectedRevision: f.revision(beta)}
	f.run(operations.TypeArtistMerge, merge.Fingerprint(), governor.MergeExecutor(merge))
	f.writeAlpha("Alpha 重扫后的标题")
	f.unchangedRescan()
	if f.trackArtist(alphaTrack) != alpha || f.trackArtist(betaTrack) != alpha {
		t.Fatal("合并后的真实重扫没有保留有效归属")
	}
	if got := f.text(`SELECT title FROM tracks WHERE id=$1::uuid`, alphaTrack); got != "Alpha 重扫后的标题" {
		t.Fatalf("没有实际更新扫描事实：%q", got)
	}

	// 按专辑拆分产生 track/release 两种持久覆盖，不只覆盖全局 alias binding。
	scope := governance.ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{betaKey}, GroupIDs: []string{betaGroup}}
	preview, err := governance.PreviewArtistSplit(ctx, fixture.database, alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	split := governance.ArtistSplitRequest{ArtistID: alpha, ExpectedRevision: f.revision(alpha), ArtistSplitScope: scope, Basis: preview.Basis, Target: governance.ArtistSplitTarget{Kind: "new"}}
	f.run(operations.TypeArtistSplit, split.Fingerprint(), governor.SplitExecutor(split))
	splitID := f.trackArtist(betaTrack)
	if splitID == alpha || splitID == beta {
		t.Fatal("按专辑拆分未创建独立有效身份")
	}
	f.unchangedRescan()
	if f.trackArtist(betaTrack) != splitID || f.trackArtist(alphaTrack) != alpha {
		t.Fatal("重扫覆盖了按专辑拆分")
	}

	manualName := "管理员指定的主名"
	rename := governance.ArtistRenameRequest{ArtistID: splitID, ExpectedRevision: f.revision(splitID), Mode: "set", Name: &manualName}
	f.run(operations.TypeArtistRename, rename.Fingerprint(), governor.RenameExecutor(rename))
	f.unchangedRescan()
	assertManualName := func() {
		t.Helper()
		if got := f.text(`SELECT effective_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, splitID); got != manualName {
			t.Fatalf("人工主名被扫描覆盖：%q", got)
		}
	}
	assertManualName()

	candidates, err := governance.ListArtistCandidates(ctx, fixture.database, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	candidateBracket := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='candidate one (candidate two)'`)
	candidateTwo := f.text(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='candidate two'`)
	var pairs []governance.ArtistKeyPair
	for _, candidate := range candidates.Items {
		if candidate.Left.ID == candidateBracket && candidate.Right.ID == candidateTwo || candidate.Left.ID == candidateTwo && candidate.Right.ID == candidateBracket {
			pairs = candidate.KeyPairs
		}
	}
	if len(pairs) == 0 {
		t.Fatal("括号别名未产生可忽略候选")
	}
	ignored := true
	ignore := governance.ArtistCandidateIgnoreRequest{ArtistID: candidateBracket, ExpectedRevision: f.revision(candidateBracket), OtherArtistID: candidateTwo, OtherExpectedRevision: f.revision(candidateTwo), KeyPairs: pairs, Ignored: &ignored}
	f.run(operations.TypeArtistCandidateIgnore, ignore.Fingerprint(), governor.CandidateIgnoreExecutor(ignore))
	f.unchangedRescan()
	candidates, err = governance.ListArtistCandidates(ctx, fixture.database, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates.Items {
		if candidate.Left.ID == candidateBracket && candidate.Right.ID == candidateTwo || candidate.Left.ID == candidateTwo && candidate.Right.ID == candidateBracket {
			t.Fatal("真实重扫重新提示已忽略的名字对")
		}
	}
	for _, table := range []string{"artist_name_bindings", "artist_assignment_overrides", "artist_overrides", "artist_candidate_ignores"} {
		if f.text(`SELECT (count(*)>0)::text FROM `+table) != "true" {
			t.Fatalf("未实际覆盖意图表：%s", table)
		}
	}
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM operations`, 4)

	// 合成源暂时消失：候选事务失败和终态前取消都不得把旧艺人署名变成 missing。
	alphaPath := filepath.Join(f.root.Path, "Alpha", "01.flac")
	parked := filepath.Join(t.TempDir(), "parked.flac")
	if err := os.Rename(alphaPath, parked); err != nil {
		t.Fatal(err)
	}
	before, graph := f.intentSnapshot(), f.graphSnapshot()
	// nextval 不随候选事务回滚，证明两个剩余候选都实际触发故障，而非只检查汇总诊断。
	f.exec(`CREATE SEQUENCE artist_rescan_fault_attempts;
 CREATE FUNCTION reject_artist_rescan() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM nextval('artist_rescan_fault_attempts');
 RAISE EXCEPTION 'artist rescan fault'; END $$;
 CREATE TRIGGER reject_artist_rescan BEFORE INSERT ON artist_tag_evidence FOR EACH ROW EXECUTE FUNCTION reject_artist_rescan()`)
	failedScan := f.scan("incomplete")
	assertDatabaseCount(t, fixture, `SELECT last_value FROM artist_rescan_fault_attempts WHERE is_called`, 2)
	// persistStagedCandidates 尝试全部候选并返回首个错误，scanRoot 据此只记录一条根级诊断。
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid AND kind='catalog_write_failure' AND relative_path='' AND message='无法保存整理证据'`, 1, failedScan)
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM track_observations WHERE scan_run_id=$1::uuid`, 0, failedScan)
	if f.intentSnapshot() != before || f.graphSnapshot() != graph {
		t.Fatal("失败扫描改变艺人图或治理快照")
	}
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'`, 3, f.root.ID)
	assertManualName()
	f.exec(`DROP TRIGGER reject_artist_rescan ON artist_tag_evidence`)
	f.scan("canceled")
	if f.intentSnapshot() != before || f.graphSnapshot() != graph {
		t.Fatal("取消扫描改变艺人图或治理快照")
	}
	assertDatabaseCount(t, fixture, `SELECT count(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'`, 3, f.root.ID)
	assertManualName()
	if err := os.Rename(parked, alphaPath); err != nil {
		t.Fatal(err)
	}
	f.unchangedRescan()
	assertManualName()
}

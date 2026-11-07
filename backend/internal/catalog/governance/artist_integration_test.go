package governance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type artistResolutionTestPort struct {
	calls []map[string]string
	fail  bool
}

func (p *artistResolutionTestPort) RefreshArtistResolution(_ context.Context, _ *sql.Tx, m map[string]string) error {
	if p.fail {
		return errors.New("injected resolution failure")
	}
	p.calls = append(p.calls, m)
	return nil
}

type artistFixture struct {
	*effectiveFixture
	governor                       *ArtistGovernance
	port                           *artistResolutionTestPort
	alpha, beta, keyAlpha, keyBeta string
	first, second                  placementRelease
	trackA, trackB                 string
}

func newArtistFixture(t *testing.T) *artistFixture {
	t.Helper()
	f := &artistFixture{effectiveFixture: newEffectiveFixture(t), port: &artistResolutionTestPort{}}
	f.exec(`UPDATE catalog_projection_state SET search_projection_version=$1 WHERE capability='artists'`, artistgraph.Version)
	f.governor = NewArtistGovernance(f.port)
	f.first = f.release("第一张")
	f.second = f.release("第二张")
	f.trackA = f.track(f.first, "甲轨", "present", 100.0)
	f.trackB = f.track(f.second, "乙轨", "present", 100.0)
	f.exec(`UPDATE releases SET artist='Alpha',album_artist='Alpha' WHERE id=$1::uuid`, f.first.releaseID)
	f.exec(`UPDATE releases SET artist='Beta',album_artist='Beta' WHERE id=$1::uuid`, f.second.releaseID)
	f.exec(`UPDATE tracks SET artist=CASE WHEN id=$1::uuid THEN 'Alpha' ELSE 'Beta' END`, f.trackA)
	f.refresh(f.first.releaseID, f.second.releaseID)
	if err := f.database.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='alpha'`).Scan(&f.alpha, &f.keyAlpha); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='beta'`).Scan(&f.beta, &f.keyBeta); err != nil {
		t.Fatal(err)
	}
	f.ready(true)
	return f
}
func (f *artistFixture) ready(value bool) {
	if value {
		f.exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	} else {
		f.exec(`UPDATE catalog_projection_state SET status='preparing' WHERE capability='artists'`)
	}
}
func (f *artistFixture) refresh(ids ...string) {
	f.t.Helper()
	tx, err := f.database.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writegate.Lock(context.Background(), tx); err != nil {
		f.t.Fatal(err)
	}
	if err := artistgraph.Refresh(context.Background(), tx, ids); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}
func (f *artistFixture) run(kind, key, fp string, execute operations.Executor) (operations.Result, error) {
	return operations.Run(context.Background(), f.database, operations.Spec{ActorID: f.actorID, Type: kind, Key: key, Fingerprint: fp, RequestID: "artist-test"}, execute)
}
func (f *artistFixture) revision(id string) int64 {
	var v int64
	if err := f.database.QueryRow(`SELECT edit_revision FROM artists WHERE id=$1::uuid`, id).Scan(&v); err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *artistFixture) count() int {
	var n int
	if err := f.database.QueryRow(`SELECT count(*) FROM operations`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *artistFixture) trackArtist(id string) string {
	var artist string
	if err := f.database.QueryRow(`SELECT artist_id::text FROM artist_effective_credits WHERE track_id=$1::uuid AND role='track_artist'`, id).Scan(&artist); err != nil {
		f.t.Fatal(err)
	}
	return artist
}
func (f *artistFixture) intentSnapshot() string {
	var raw []byte
	if err := f.database.QueryRow(`SELECT jsonb_build_object(
 'bindings',(SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY key_id),'[]') FROM artist_name_bindings b),
 'assignments',(SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM artist_assignment_overrides a),
 'overrides',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY artist_id),'[]') FROM artist_overrides o),
 'ignores',(SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY left_key_id,right_key_id),'[]') FROM artist_candidate_ignores i),
 'revisions',(SELECT jsonb_agg(jsonb_build_array(id,edit_revision) ORDER BY id) FROM artists))`).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	return string(raw)
}
func assertArtistFailure(t *testing.T, err error, code string) {
	t.Helper()
	_, got, _ := operations.Problem(err)
	if err == nil || got != code {
		t.Fatalf("error=%v code=%s want %s", err, got, code)
	}
}
func (f *artistFixture) merge() operations.Result {
	f.t.Helper()
	r := ArtistMergeRequest{ArtistID: f.alpha, ExpectedRevision: f.revision(f.alpha), SourceArtistID: f.beta, SourceExpectedRevision: f.revision(f.beta)}
	result, err := f.run(operations.TypeArtistMerge, uuid.New(), r.Fingerprint(), f.governor.MergeExecutor(r))
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func TestPostgreSQLArtistMergeRestoreAndDurableIntent(t *testing.T) {
	f := newArtistFixture(t)
	name := "人工 Beta"
	rename := ArtistRenameRequest{ArtistID: f.beta, ExpectedRevision: 1, Mode: "set", Name: &name}
	if _, err := f.run(operations.TypeArtistRename, uuid.New(), rename.Fingerprint(), f.governor.RenameExecutor(rename)); err != nil {
		t.Fatal(err)
	}
	merge := ArtistMergeRequest{ArtistID: f.alpha, ExpectedRevision: 1, SourceArtistID: f.beta, SourceExpectedRevision: 2}
	key := uuid.New()
	first, err := f.run(operations.TypeArtistMerge, key, merge.Fingerprint(), f.governor.MergeExecutor(merge))
	if err != nil {
		t.Fatal(err)
	}
	if f.trackArtist(f.trackB) != f.alpha || f.revision(f.alpha) != 2 || f.revision(f.beta) != 3 {
		t.Fatal("merge state mismatch")
	}

	var aliasSearchable bool
	if err := f.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM artist_search_names WHERE artist_id=$1::uuid AND original_value=$2)`, f.alpha, name).Scan(&aliasSearchable); err != nil || !aliasSearchable {
		t.Fatalf("source manual name missing from merged search: %v", err)
	}
	if len(f.port.calls) != 1 || f.port.calls[0][f.beta] != f.alpha {
		t.Fatal("resolution mapping missing")
	}
	f.ready(false)
	replay, err := f.run(operations.TypeArtistMerge, key, merge.Fingerprint(), f.governor.MergeExecutor(merge))
	if err != nil || !replay.Replayed || !artistJSONEqual(first.Response, replay.Response) {
		t.Fatalf("not-ready replay: %+v %v", replay, err)
	}
	f.ready(true)
	conflict := merge
	conflict.SourceExpectedRevision++
	_, err = f.run(operations.TypeArtistMerge, key, conflict.Fingerprint(), f.governor.MergeExecutor(conflict))
	assertArtistFailure(t, err, "idempotency_conflict")
	snapshot := f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if snapshot != f.intentSnapshot() {
		t.Fatal("rescan mutated governance intent or revision")
	}
	list, err := operations.List(context.Background(), f.database, operations.ListParams{Targets: artistTargets(f.beta), Page: 1, PageSize: 50})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("source artist records %+v %v", list, err)
	}
	description, err := (ArtistMergeKind{}).Describe(context.Background(), f.database, []operations.Record{list.Items[0]})
	if err != nil || description[first.OperationID].Changes == nil || description[first.OperationID].Revertible {
		t.Fatalf("description %+v %v", description, err)
	}
	f.ready(false)
	description, err = (ArtistMergeKind{}).Describe(context.Background(), f.database, []operations.Record{list.Items[0]})
	if err != nil || description[first.OperationID].Artist.Visible || description[first.OperationID].Artist.Name == "" {
		t.Fatalf("preparing description %+v %v", description, err)
	}
	f.ready(true)
	scope := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyBeta}}
	preview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	f.refresh(f.first.releaseID, f.second.releaseID)
	again, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil || again.Basis != preview.Basis {
		t.Fatal("unchanged rescan invalidated basis", err)
	}
	split := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 2, ArtistSplitScope: scope, Basis: preview.Basis, Target: ArtistSplitTarget{Kind: "restore", ArtistID: f.beta, ExpectedRevision: 3}}
	splitResult, err := f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split))
	if err != nil {
		t.Fatal(err)
	}

	if err := f.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM artist_search_names WHERE artist_id=$1::uuid AND original_value=$2)`, f.alpha, name).Scan(&aliasSearchable); err != nil || aliasSearchable {
		t.Fatalf("restored manual name remained on former target: %v", err)
	}
	if f.trackArtist(f.trackB) != f.beta || f.revision(f.alpha) != 3 || f.revision(f.beta) != 4 || len(f.port.calls) != 2 || f.port.calls[1][f.beta] != f.beta {
		t.Fatal("restore did not preserve old identity")
	}
	var display string
	var redirect *string
	if err := f.database.QueryRow(`SELECT display_name,redirect_artist_id::text FROM artist_overrides WHERE artist_id=$1::uuid`, f.beta).Scan(&display, &redirect); err != nil || display != name || redirect != nil {
		t.Fatalf("restored intent name=%s redirect=%v err=%v", display, redirect, err)
	}
	snapshot = f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if snapshot != f.intentSnapshot() {
		t.Fatal("restored intent lost on scan")
	}
	list, err = operations.List(context.Background(), f.database, operations.ListParams{Targets: artistTargets(f.beta), Page: 1, PageSize: 50})
	if err != nil || len(list.Items) != 3 || list.Items[0].ID != splitResult.OperationID {
		t.Fatal("restore target omitted from operation filter", err)
	}
}

func TestPostgreSQLArtistAlbumSplitScopeAndFutureTracks(t *testing.T) {
	f := newArtistFixture(t)
	// 两张发行使用同一个真实姓名，但只选择第一张所在的专辑。
	f.exec(`UPDATE releases SET artist='Alpha',album_artist='Alpha' WHERE id=$1::uuid`, f.second.releaseID)
	f.exec(`UPDATE tracks SET artist='Alpha' WHERE id=$1::uuid`, f.trackB)
	f.refresh(f.second.releaseID)
	scope := ArtistSplitScope{Mode: "albums", GroupIDs: []string{f.first.groupID}, NameKeyIDs: []string{f.keyAlpha}}
	preview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	fresh := f.track(f.first, "新增", "present", 80.0)
	f.exec(`UPDATE tracks SET artist='Alpha' WHERE id=$1::uuid`, fresh)
	f.refresh(f.first.releaseID)
	split := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 1, ArtistSplitScope: scope, Basis: preview.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	_, err = f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split))
	assertArtistFailure(t, err, "artist_scope_changed")
	if f.count() != 0 {
		t.Fatal("rejected split recorded")
	}
	preview, err = PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	split.Basis = preview.Basis
	result, err := f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Target struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
		} `json:"target"`
	}
	if err := json.Unmarshal(result.Response, &body); err != nil {
		t.Fatal(err)
	}
	newID := body.Target.ID
	if newID == f.alpha || body.Target.Revision != 1 || f.trackArtist(f.trackA) != newID || f.trackArtist(f.trackB) != f.alpha {
		t.Fatal("split selected wrong subjects")
	}
	later := f.track(f.first, "以后新增", "present", 120.0)
	f.exec(`UPDATE tracks SET artist='Alpha' WHERE id=$1::uuid`, later)
	f.refresh(f.first.releaseID)
	if f.trackArtist(later) != newID {
		t.Fatal("future same-release track did not follow")
	}
	// 新入组的发行不自动继承；既有轨换发行仍优先用 track 覆盖。
	f.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, f.first.groupID, f.second.releaseID)
	f.exec(`UPDATE tracks SET medium_id=$1::uuid,source_status='missing' WHERE id=$2::uuid`, f.second.mediumID, f.trackA)
	before := f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if f.trackArtist(f.trackA) != newID || f.trackArtist(f.trackB) != f.alpha || f.intentSnapshot() != before {
		t.Fatal("scope followed group or lost moved missing identity")
	}
	f.exec(`UPDATE tracks SET source_status='present' WHERE id=$1::uuid`, f.trackA)
	f.refresh(f.first.releaseID, f.second.releaseID)
	if f.trackArtist(f.trackA) != newID {
		t.Fatal("missing restore lost scope")
	}
	// 把同名实体合并，必须移动 scope，随后可在明确范围中恢复旧 ID。
	merge := ArtistMergeRequest{ArtistID: f.alpha, ExpectedRevision: 2, SourceArtistID: newID, SourceExpectedRevision: 1}
	if _, err := f.run(operations.TypeArtistMerge, uuid.New(), merge.Fingerprint(), f.governor.MergeExecutor(merge)); err != nil {
		t.Fatal(err)
	}
	if f.trackArtist(f.trackA) != f.alpha || f.trackArtist(later) != f.alpha {
		t.Fatal("same-name merge did not update scoped mapping")
	}
	record, err := operations.List(context.Background(), f.database, operations.ListParams{Targets: artistTargets(newID), Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	var beforeState artistRecordState
	if err := json.Unmarshal(record.Items[0].Before, &beforeState); err != nil || len(beforeState.Assignments) < 3 {
		t.Fatal("merge omitted scoped prior ownership", err)
	}
	var afterState artistRecordState
	if err := json.Unmarshal(record.Items[0].After, &afterState); err != nil {
		t.Fatal(err)
	}
	changes, ok := afterState.Changes.(map[string]any)
	if !ok || changes["name_count"] != float64(1) {
		t.Fatal("scoped names omitted from merge summary", afterState.Changes)
	}

}

func TestPostgreSQLArtistGuardRollbackAndConcurrentRevisions(t *testing.T) {
	f := newArtistFixture(t)
	f.ready(false)
	name := "新名"
	yes := true
	rename := ArtistRenameRequest{ArtistID: f.alpha, ExpectedRevision: 1, Mode: "set", Name: &name}
	merge := ArtistMergeRequest{ArtistID: f.alpha, ExpectedRevision: 1, SourceArtistID: f.beta, SourceExpectedRevision: 1}
	split := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 1, ArtistSplitScope: ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyAlpha}}, Basis: strings.Repeat("a", 64), Target: ArtistSplitTarget{Kind: "new"}}
	ignore := ArtistCandidateIgnoreRequest{ArtistID: f.alpha, ExpectedRevision: 1, OtherArtistID: f.beta, OtherExpectedRevision: 1, KeyPairs: []ArtistKeyPair{{f.keyAlpha, f.keyBeta}}, Ignored: &yes}
	for kind, execute := range map[string]operations.Executor{operations.TypeArtistRename: f.governor.RenameExecutor(rename), operations.TypeArtistMerge: f.governor.MergeExecutor(merge), operations.TypeArtistSplit: f.governor.SplitExecutor(split), operations.TypeArtistCandidateIgnore: f.governor.CandidateIgnoreExecutor(ignore)} {
		_, err := f.run(kind, uuid.New(), kind, execute)
		assertArtistFailure(t, err, "artist_catalog_not_ready")
	}
	if f.count() != 0 || f.revision(f.alpha) != 1 {
		t.Fatal("not-ready changed state")
	}
	f.ready(true)
	snapshot := f.intentSnapshot()
	f.port.fail = true
	_, err := f.run(operations.TypeArtistMerge, uuid.New(), merge.Fingerprint(), f.governor.MergeExecutor(merge))
	if err == nil || f.intentSnapshot() != snapshot || f.count() != 0 || f.trackArtist(f.trackB) != f.beta {
		t.Fatal("resolution failure did not roll back", err)
	}
	f.port.fail = false
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.run(operations.TypeArtistRename, uuid.New(), rename.Fingerprint(), f.governor.RenameExecutor(rename))
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(errorsOut)
	success, conflicts := 0, 0
	for err := range errorsOut {
		if err == nil {
			success++
		} else {
			assertArtistFailure(t, err, "revision_conflict")
			conflicts++
		}
	}
	if success != 1 || conflicts != 1 || f.count() != 1 {
		t.Fatal("concurrent revision checks", success, conflicts)
	}
	rename.ExpectedRevision = 2
	_, err = f.run(operations.TypeArtistRename, uuid.New(), rename.Fingerprint(), f.governor.RenameExecutor(rename))
	assertArtistFailure(t, err, "no_changes")
}

func TestPostgreSQLArtistAliasSplitPreservesThirdPartyScope(t *testing.T) {
	f := newArtistFixture(t)
	scope := ArtistSplitScope{Mode: "albums", GroupIDs: []string{f.first.groupID}, NameKeyIDs: []string{f.keyAlpha}}
	p, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	r := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 1, ArtistSplitScope: scope, Basis: p.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	result, err := f.run(operations.TypeArtistSplit, uuid.New(), r.Fingerprint(), f.governor.SplitExecutor(r))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	_ = json.Unmarshal(result.Response, &body)
	third := body.Target.ID
	before := f.trackArtist(f.trackA)
	if before != third {
		t.Fatal("scope split failed")
	}
	aliases := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyAlpha}}
	p, err = PreviewArtistSplit(context.Background(), f.database, f.alpha, aliases)
	if err != nil || p.Scope.ExcludedScopeCount != 2 || len(p.Scope.ExcludedScopes) != 2 {
		t.Fatalf("third scope not shown %+v %v", p, err)
	}
	for _, excluded := range p.Scope.ExcludedScopes {
		if excluded.ArtistID != third || excluded.KeyID != f.keyAlpha {
			t.Fatal("wrong excluded scope", excluded)
		}
	}
	r = ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 2, ArtistSplitScope: aliases, Basis: p.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	if _, err = f.run(operations.TypeArtistSplit, uuid.New(), r.Fingerprint(), f.governor.SplitExecutor(r)); err != nil {
		t.Fatal(err)
	}
	if f.trackArtist(f.trackA) != third {
		t.Fatal("alias split stole third-party scope")
	}
}

func TestPostgreSQLArtistOperationTargetsUnionAndDescribeCorruption(t *testing.T) {
	f := newArtistFixture(t)
	merged := f.merge()
	correction := CorrectionRequest{ReleaseID: f.first.releaseID, ExpectedRevision: 1, Changes: map[string]CorrectionChange{"title": {Mode: "set", Text: "新标题"}}}
	if _, err := f.run(operations.TypeReleaseCorrection, uuid.New(), correction.Fingerprint(), CorrectionExecutor(correction)); err != nil {
		t.Fatal(err)
	}
	page, err := operations.List(context.Background(), f.database, operations.ListParams{Targets: append(artistTargets(f.beta), operations.Target{Type: operations.TargetRelease, ID: f.first.releaseID}), Page: 1, PageSize: 50})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("union=%+v %v", page, err)
	}
	record, err := operations.Load(context.Background(), f.database, merged.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	record.After = []byte(`{"schema_version":1,"changes":{"kind":"artist_merge","source":null}}`)
	described, err := (ArtistMergeKind{}).Describe(context.Background(), f.database, []operations.Record{record})
	if err != nil || described[record.ID].Changes != nil {
		t.Fatal("damaged state broke page", err)
	}

	f.ready(false)
	described, err = (ArtistMergeKind{}).Describe(context.Background(), f.database, []operations.Record{record})
	if err != nil || described[record.ID].Changes != nil || described[record.ID].Artist.Name == "" || described[record.ID].Artist.Visible {
		t.Fatal("damaged state while preparing broke safe artist summary", err)
	}
	f.ready(true)
	before := f.intentSnapshot()
	f.exec(`UPDATE tracks SET observed_at=NOW() WHERE id=$1::uuid`, f.trackA)
	f.refresh(f.first.releaseID, f.second.releaseID)
	if !reflect.DeepEqual(before, f.intentSnapshot()) {
		t.Fatal("timestamp rescan changed intentions")
	}
}

func TestPostgreSQLArtistCandidatesExcludeSameRoleCoCredit(t *testing.T) {
	f := newArtistFixture(t)
	f.exec(`UPDATE releases SET artist='',album_artist='' WHERE id=$1::uuid`, f.first.releaseID)
	f.exec(`INSERT INTO artist_tag_evidence(track_id,role,tag_key,occurrence_index,raw_value,source_kind)
 VALUES($1::uuid,'track_artist','ARTIST',0,'杨璐（羊鹿鹿）','tag'),
 ($1::uuid,'track_artist','ARTIST',1,'羊鹿鹿','tag'),($1::uuid,'track_artist','ARTIST',2,'夏小夏','tag')`, f.trackA)
	f.refresh(f.first.releaseID)
	ctx := context.Background()
	assertEmpty := func() {
		t.Helper()
		for name, list := range map[string]func(context.Context, *sql.DB, int, int) (catalog.ArtistPage[ArtistCandidate], error){
			"待处理": ListArtistCandidates, "已忽略": ListIgnoredArtistCandidates,
		} {
			page, err := list(ctx, f.database, 1, 50)
			if err != nil || page.Pagination.Total != 0 || len(page.Items) != 0 {
				t.Fatalf("同曲同身份共同署名仍在%s提示：%+v，错误：%v", name, page, err)
			}
		}
	}
	var signals int
	if err := f.database.QueryRow(`SELECT count(*) FROM artist_alias_signals`).Scan(&signals); err != nil || signals != 1 {
		t.Fatalf("应存在被读取层排除的括号别名信号：%d，错误：%v", signals, err)
	}
	assertEmpty()
	f.exec(`UPDATE artist_tag_evidence SET role='composer',tag_key='COMPOSER' WHERE track_id=$1::uuid AND raw_value='羊鹿鹿'`, f.trackA)
	// 作曲等身份的扫描事实来自 track_credits，必须与标签证据同步。
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','羊鹿鹿',1)`, f.trackA)
	f.refresh(f.first.releaseID)
	pending, err := ListArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || pending.Pagination.Total != 1 || len(pending.Items) != 1 {
		t.Fatalf("不同身份的括号别名应提示：%+v，错误：%v", pending, err)
	}
	candidate := pending.Items[0]
	if candidate.EvidenceCount != 1 || len(candidate.Reasons) != 1 {
		t.Fatalf("括号别名依据数不正确：%+v", candidate)
	}
	reason := candidate.Reasons[0]
	if reason.Kind != "bracket_alias" || reason.ReleaseID != f.first.releaseID || reason.AliasPart == nil || *reason.AliasPart != "inner" || reason.BracketSide == nil {
		t.Fatalf("括号别名理由字段不完整：%+v", reason)
	}
	side, leftName, rightName := "left", "杨璐（羊鹿鹿）", "羊鹿鹿"
	if candidate.Left.Name == "羊鹿鹿" {
		side, leftName, rightName = "right", rightName, leftName
	}
	if *reason.BracketSide != side || reason.LeftName != leftName || reason.RightName != rightName {
		t.Fatalf("括号别名左右名字或括号侧不一致：%+v", candidate)
	}
	f.setCandidateIgnored(candidate, true)
	hidden, err := ListIgnoredArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || hidden.Pagination.Total != 1 || len(hidden.Items) != 1 {
		t.Fatalf("不同身份的已忽略候选丢失：%+v，错误：%v", hidden, err)
	}
	f.exec(`UPDATE artist_tag_evidence SET role='track_artist',tag_key='ARTIST' WHERE track_id=$1::uuid AND raw_value='羊鹿鹿'`, f.trackA)
	f.exec(`DELETE FROM track_credits WHERE track_id=$1::uuid AND role='composer' AND name='羊鹿鹿'`, f.trackA)
	f.refresh(f.first.releaseID)
	assertEmpty()
	// 被共同署名排除的名字对仍可恢复，不应被判为范围已变化。
	f.setCandidateIgnored(candidate, false)
	assertEmpty()
}

func TestPostgreSQLArtistCandidateIgnoreRescanAndRestore(t *testing.T) {
	f := newArtistIgnoredCandidatesFixture(t)
	listed, err := ListArtistCandidates(context.Background(), f.database, 1, 50)
	if err != nil || len(listed.Items) != 1 || len(listed.Items[0].KeyPairs) != 1 || listed.Items[0].Reasons[0].Kind != "bracket_alias" {
		t.Fatalf("candidates %+v %v", listed, err)
	}
	reason := listed.Items[0].Reasons[0]
	side, leftName, rightName := "left", "杨璐（羊鹿鹿）", "羊鹿鹿"
	if listed.Items[0].Left.ID != f.alpha {
		side, leftName, rightName = "right", rightName, leftName
	}
	if listed.Items[0].EvidenceCount != 1 || len(listed.Items[0].Reasons) != 1 || reason.ReleaseID != f.first.releaseID ||
		reason.BracketSide == nil || *reason.BracketSide != side || reason.AliasPart == nil || *reason.AliasPart != "inner" ||
		reason.LeftName != leftName || reason.RightName != rightName {
		t.Fatalf("跨专辑括号别名理由不正确：%+v", listed.Items[0])
	}
	ignored := true
	r := ArtistCandidateIgnoreRequest{ArtistID: f.alpha, ExpectedRevision: 1, OtherArtistID: f.beta, OtherExpectedRevision: 1, KeyPairs: listed.Items[0].KeyPairs, Ignored: &ignored}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	result, err := f.run(operations.TypeArtistCandidateIgnore, uuid.New(), r.Fingerprint(), f.governor.CandidateIgnoreExecutor(r))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if f.intentSnapshot() != snapshot {
		t.Fatal("rescan cleared ignore")
	}
	listed, err = ListArtistCandidates(context.Background(), f.database, 1, 50)
	if err != nil || len(listed.Items) != 0 {
		t.Fatal("ignored pair reappeared", err)
	}
	record, err := operations.Load(context.Background(), f.database, result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := (ArtistCandidateIgnoreKind{}).Describe(context.Background(), f.database, []operations.Record{record})
	if err != nil || d[record.ID].Changes == nil || d[record.ID].Revertible {
		t.Fatal("ignore description", err)
	}
	ignored = false
	r.ExpectedRevision = 2
	r.OtherExpectedRevision = 2
	if _, err = f.run(operations.TypeArtistCandidateIgnore, uuid.New(), r.Fingerprint(), f.governor.CandidateIgnoreExecutor(r)); err != nil {
		t.Fatal(err)
	}
	listed, err = ListArtistCandidates(context.Background(), f.database, 1, 50)
	if err != nil || len(listed.Items) != 1 {
		t.Fatal("unignore did not restore hint", err)
	}
	// 单独写法所在专辑显式拆分后，不再用跨专辑证据提示合并。
	scope := ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{f.keyBeta}, GroupIDs: []string{f.second.groupID}}
	p, err := PreviewArtistSplit(context.Background(), f.database, f.beta, scope)
	if err != nil {
		t.Fatal(err)
	}
	split := ArtistSplitRequest{ArtistID: f.beta, ExpectedRevision: 3, ArtistSplitScope: scope, Basis: p.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	if _, err = f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split)); err != nil {
		t.Fatal(err)
	}
	listed, err = ListArtistCandidates(context.Background(), f.database, 1, 50)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("explicit split candidate %+v %v", listed, err)
	}
}

// 候选信号由真实投影刷新产生，不直接写候选表或忽略表。
func newArtistIgnoredCandidatesFixture(t *testing.T) *artistFixture {
	t.Helper()
	f := newArtistFixture(t)
	f.exec(`UPDATE releases SET artist='杨璐（羊鹿鹿）',album_artist='杨璐（羊鹿鹿）' WHERE id=$1::uuid`, f.first.releaseID)
	f.exec(`UPDATE tracks SET artist='杨璐（羊鹿鹿）' WHERE id=$1::uuid`, f.trackA)
	f.exec(`UPDATE releases SET artist='羊鹿鹿',album_artist='羊鹿鹿' WHERE id=$1::uuid`, f.second.releaseID)
	f.exec(`UPDATE tracks SET artist='羊鹿鹿' WHERE id=$1::uuid`, f.trackB)
	f.refresh(f.first.releaseID, f.second.releaseID)
	if err := f.database.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='杨璐(羊鹿鹿)'`).Scan(&f.alpha, &f.keyAlpha); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='羊鹿鹿'`).Scan(&f.beta, &f.keyBeta); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *artistFixture) setCandidateIgnored(candidate ArtistCandidate, ignored bool) operations.Result {
	f.t.Helper()
	request := ArtistCandidateIgnoreRequest{
		ArtistID: candidate.Left.ID, ExpectedRevision: f.revision(candidate.Left.ID),
		OtherArtistID: candidate.Right.ID, OtherExpectedRevision: f.revision(candidate.Right.ID),
		KeyPairs: candidate.KeyPairs, Ignored: &ignored,
	}
	if err := request.Normalize(); err != nil {
		f.t.Fatal(err)
	}
	result, err := f.run(operations.TypeArtistCandidateIgnore, uuid.New(), request.Fingerprint(), f.governor.CandidateIgnoreExecutor(request))
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func TestPostgreSQLArtistIgnoredCandidatesListAndRestore(t *testing.T) {
	f := newArtistIgnoredCandidatesFixture(t)
	ctx := context.Background()
	pending, err := ListArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || pending.Pagination.Total != 1 || len(pending.Items) != 1 {
		t.Fatalf("忽略前候选：%+v，错误：%v", pending, err)
	}
	original := pending.Items[0]
	f.setCandidateIgnored(original, true)
	assertLists := func(ignored bool) {
		t.Helper()
		pending, err := ListArtistCandidates(ctx, f.database, 1, 50)
		if err != nil {
			t.Fatal(err)
		}
		hidden, err := ListIgnoredArtistCandidates(ctx, f.database, 1, 50)
		if err != nil {
			t.Fatal(err)
		}
		visible, empty := pending, hidden
		if ignored {
			visible, empty = hidden, pending
		}
		if empty.Pagination.Total != 0 || len(empty.Items) != 0 || visible.Pagination.Total != 1 || len(visible.Items) != 1 {
			t.Fatalf("ignored=%t，待处理：%+v，已忽略：%+v", ignored, pending, hidden)
		}
		candidate := visible.Items[0]
		if candidate.Left.ID != original.Left.ID || candidate.Right.ID != original.Right.ID ||
			!reflect.DeepEqual(candidate.KeyPairs, original.KeyPairs) || candidate.EvidenceCount != original.EvidenceCount ||
			!reflect.DeepEqual(candidate.Reasons, original.Reasons) {
			t.Fatalf("候选身份、名字对或依据发生变化：%+v，原候选：%+v", candidate, original)
		}
	}
	assertLists(true)
	snapshot := f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if f.intentSnapshot() != snapshot {
		t.Fatal("刷新覆盖了忽略意图或版本")
	}
	assertLists(true)
	f.setCandidateIgnored(original, false)
	assertLists(false)
	snapshot = f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if f.intentSnapshot() != snapshot {
		t.Fatal("刷新覆盖了恢复意图或版本")
	}
	assertLists(false)
}

func TestPostgreSQLArtistIgnoredCandidatesExcludeMergedPairs(t *testing.T) {
	f := newArtistIgnoredCandidatesFixture(t)
	ctx := context.Background()
	pending, err := ListArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || len(pending.Items) != 1 {
		t.Fatalf("准备合并候选：%+v，错误：%v", pending, err)
	}
	f.setCandidateIgnored(pending.Items[0], true)
	hidden, err := ListIgnoredArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || hidden.Pagination.Total != 1 || len(hidden.Items) != 1 {
		t.Fatalf("合并前已忽略列表：%+v，错误：%v", hidden, err)
	}
	f.merge()
	assertEmpty := func() {
		t.Helper()
		for name, list := range map[string]func(context.Context, *sql.DB, int, int) (catalog.ArtistPage[ArtistCandidate], error){
			"待处理": ListArtistCandidates, "已忽略": ListIgnoredArtistCandidates,
		} {
			page, err := list(ctx, f.database, 1, 50)
			if err != nil || page.Pagination.Total != 0 || len(page.Items) != 0 {
				t.Fatalf("合并后%s仍有候选：%+v，错误：%v", name, page, err)
			}
		}
	}
	assertEmpty()
	f.refresh(f.first.releaseID, f.second.releaseID)
	assertEmpty()
}

func TestPostgreSQLArtistIgnoredCandidatesOrderAndPaging(t *testing.T) {
	f := newArtistIgnoredCandidatesFixture(t)
	// 较早忽略的一对有更多依据，确保忽略时间优先于原有的依据数排序。
	extra := f.release("括号写法再版")
	extraTrack := f.track(extra, "再版曲目", "present", 100.0)
	f.exec(`UPDATE releases SET artist='杨璐（羊鹿鹿）',album_artist='杨璐（羊鹿鹿）' WHERE id=$1::uuid`, extra.releaseID)
	f.exec(`UPDATE tracks SET artist='杨璐（羊鹿鹿）' WHERE id=$1::uuid`, extraTrack)
	third := f.release("第三张")
	track := f.track(third, "丙轨", "present", 100.0)
	f.exec(`UPDATE releases SET artist='Gamma',album_artist='Delta' WHERE id=$1::uuid`, third.releaseID)
	f.exec(`UPDATE tracks SET artist='Gamma' WHERE id=$1::uuid`, track)
	f.refresh(f.first.releaseID, f.second.releaseID, third.releaseID, extra.releaseID)
	ctx := context.Background()
	pending, err := ListArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || pending.Pagination.Total != 2 || len(pending.Items) != 2 || pending.Items[0].EvidenceCount <= pending.Items[1].EvidenceCount {
		t.Fatalf("准备不同依据数的两对候选：%+v，错误：%v", pending, err)
	}
	older, newer := pending.Items[0], pending.Items[1]
	for i, candidate := range []ArtistCandidate{older, newer} {
		result := f.setCandidateIgnored(candidate, true)
		// 固定操作时间，不靠 sleep 或数据库时钟分辨率决定先后。
		f.exec(`UPDATE operations SET created_at='2026-01-01T00:00:00Z'::timestamptz+($2::int*interval '1 second') WHERE id=$1::uuid`, result.OperationID, i)
	}
	pending, err = ListArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || pending.Pagination.Total != 0 || len(pending.Items) != 0 {
		t.Fatalf("忽略后仍有待处理：%+v，错误：%v", pending, err)
	}
	all, err := ListIgnoredArtistCandidates(ctx, f.database, 1, 50)
	if err != nil || all.Pagination.Total != 2 || len(all.Items) != 2 ||
		!reflect.DeepEqual(all.Items[0].KeyPairs, newer.KeyPairs) || !reflect.DeepEqual(all.Items[1].KeyPairs, older.KeyPairs) {
		t.Fatalf("已忽略未按最近时间排序：%+v，错误：%v", all, err)
	}
	for page := 1; page <= 3; page++ {
		got, err := ListIgnoredArtistCandidates(ctx, f.database, page, 1)
		if err != nil || got.Pagination.Page != page || got.Pagination.PageSize != 1 || got.Pagination.Total != 2 {
			t.Fatalf("第%d页分页信息：%+v，错误：%v", page, got, err)
		}
		if page == 3 {
			if len(got.Items) != 0 {
				t.Fatalf("超出范围的页不为空：%+v", got)
			}
		} else if len(got.Items) != 1 || !reflect.DeepEqual(got.Items[0], all.Items[page-1]) {
			t.Fatalf("第%d页与完整排序不一致：%+v", page, got)
		}
	}
}

func TestPostgreSQLArtistPreviewMembershipAndCorrectionBasis(t *testing.T) {
	f := newArtistFixture(t)
	scope := ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{f.keyAlpha}, GroupIDs: []string{f.first.groupID}}
	original, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, f.first.groupID, f.second.releaseID)
	next, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil || next.Basis == original.Basis {
		t.Fatal("group membership did not invalidate basis", err)
	}
	correction := CorrectionRequest{ReleaseID: f.first.releaseID, ExpectedRevision: 1, Changes: map[string]CorrectionChange{"album_artist": {Mode: "set", Text: "Beta"}}}
	if _, err := f.run(operations.TypeReleaseCorrection, uuid.New(), correction.Fingerprint(), CorrectionExecutor(correction)); err != nil {
		t.Fatal(err)
	}
	corrected, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil || corrected.Basis == next.Basis {
		t.Fatal("album artist correction did not invalidate basis", err)
	}
	// 曲目自身艺人不被专辑艺人修正覆盖。
	if f.trackArtist(f.trackA) != f.alpha {
		t.Fatal("album correction replaced track artist")
	}
}

func artistJSONEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestPostgreSQLArtistCandidatePairPaginationDoesNotIgnoreUnseenPairs(t *testing.T) {
	f := newArtistFixture(t)
	// 同一对艺人的 101 对具体名字必须拆成两条候选，单次只忽略展示的一百对。
	for i := 0; i < 101; i++ {
		keyID := uuid.New()
		key := fmt.Sprintf("alpha alias %03d", i)
		hash := sha256.Sum256([]byte(key))
		f.exec(`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id) VALUES($1::uuid,1,$2,$3,$4::uuid)`, keyID, key, hash[:], f.alpha)
		f.exec(`INSERT INTO artist_candidate_signals(left_key_id,right_key_id,kind,release_id,track_id,role,left_name,right_name)
   VALUES(least($1::uuid,$2::uuid),greatest($1::uuid,$2::uuid),'album_track_one_to_one',$3::uuid,NULL,NULL,
   CASE WHEN $1::uuid<$2::uuid THEN $4 ELSE 'Beta' END,CASE WHEN $1::uuid<$2::uuid THEN 'Beta' ELSE $4 END)`, keyID, f.keyBeta, f.first.releaseID, key)
	}
	page, err := ListArtistCandidates(context.Background(), f.database, 1, 1)
	if err != nil || page.Pagination.Total != 2 || len(page.Items) != 1 || len(page.Items[0].KeyPairs) != 100 || len(page.Items[0].Reasons) != 3 || page.Items[0].EvidenceCount != 100 {
		t.Fatalf("pair chunks %+v %v", page, err)
	}
	yes := true
	request := ArtistCandidateIgnoreRequest{ArtistID: f.alpha, ExpectedRevision: 1, OtherArtistID: f.beta, OtherExpectedRevision: 1, Ignored: &yes, KeyPairs: page.Items[0].KeyPairs}
	if err := request.Normalize(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(operations.TypeArtistCandidateIgnore, uuid.New(), request.Fingerprint(), f.governor.CandidateIgnoreExecutor(request)); err != nil {
		t.Fatal(err)
	}
	page, err = ListArtistCandidates(context.Background(), f.database, 1, 50)
	if err != nil || page.Pagination.Total != 1 || len(page.Items[0].KeyPairs) != 1 || page.Items[0].EvidenceCount != 1 {
		t.Fatalf("unseen pair lost %+v %v", page, err)
	}
}

func TestPostgreSQLArtistMergeScopeBudgetIsAtomic(t *testing.T) {
	f := newArtistFixture(t)
	f.exec(`INSERT INTO artist_name_keys(id,normalization_version,normalized_key,key_hash,initial_artist_id)
 SELECT gen_random_uuid(),1,'bulk-'||n,sha256(convert_to('bulk-'||n,'UTF8')),$1::uuid FROM generate_series(1,1000) n`, f.alpha)
	before := f.intentSnapshot()
	request := ArtistMergeRequest{ArtistID: f.beta, ExpectedRevision: 1, SourceArtistID: f.alpha, SourceExpectedRevision: 1}
	_, err := f.run(operations.TypeArtistMerge, uuid.New(), request.Fingerprint(), f.governor.MergeExecutor(request))
	assertArtistFailure(t, err, "artist_scope_too_large")
	if f.count() != 0 || f.intentSnapshot() != before || len(f.port.calls) != 0 {
		t.Fatal("scope budget rejection changed state")
	}
}

func TestPostgreSQLArtistRenameDoesNotRetainOldManualNamesOrStealKeys(t *testing.T) {
	f := newArtistFixture(t)
	for index, name := range []string{"Alpha", "第二人工名", "第三人工名"} {
		request := ArtistRenameRequest{ArtistID: f.beta, ExpectedRevision: int64(index + 1), Mode: "set", Name: &name}
		if _, err := f.run(operations.TypeArtistRename, uuid.New(), request.Fingerprint(), f.governor.RenameExecutor(request)); err != nil {
			t.Fatal(err)
		}
		if f.trackArtist(f.trackA) != f.alpha || f.trackArtist(f.trackB) != f.beta {
			t.Fatal("rename stole another artist's name key")
		}
	}
	var oldManual bool
	if err := f.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM artist_search_names WHERE artist_id=$1::uuid AND original_value IN ('Alpha','第二人工名'))`, f.beta).Scan(&oldManual); err != nil || oldManual {
		t.Fatal("old pure manual names became permanent aliases", err)
	}
	request := ArtistRenameRequest{ArtistID: f.beta, ExpectedRevision: 4, Mode: "auto"}
	if _, err := f.run(operations.TypeArtistRename, uuid.New(), request.Fingerprint(), f.governor.RenameExecutor(request)); err != nil {
		t.Fatal(err)
	}
	var current string
	if err := f.database.QueryRow(`SELECT effective_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, f.beta).Scan(&current); err != nil || current != "Beta" {
		t.Fatalf("auto display=%s error=%v", current, err)
	}
	if err := f.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM artist_search_names WHERE artist_id=$1::uuid AND original_value='第三人工名')`, f.beta).Scan(&oldManual); err != nil || oldManual {
		t.Fatal("auto retained old pure manual name", err)
	}
}

func TestPostgreSQLArtistScopedRestoreUsesOldIdentity(t *testing.T) {
	f := newArtistFixture(t)
	scope := ArtistSplitScope{Mode: "albums", GroupIDs: []string{f.first.groupID}, NameKeyIDs: []string{f.keyAlpha}}
	preview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	split := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 1, ArtistSplitScope: scope, Basis: preview.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	if _, err := f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split)); err != nil {
		t.Fatal(err)
	}
	oldIdentity := f.trackArtist(f.trackA)
	merge := ArtistMergeRequest{ArtistID: f.alpha, ExpectedRevision: 2, SourceArtistID: oldIdentity, SourceExpectedRevision: 1}
	if _, err := f.run(operations.TypeArtistMerge, uuid.New(), merge.Fingerprint(), f.governor.MergeExecutor(merge)); err != nil {
		t.Fatal(err)
	}
	preview, err = PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
	if err != nil {
		t.Fatal(err)
	}
	split = ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 3, ArtistSplitScope: scope, Basis: preview.Basis, Target: ArtistSplitTarget{Kind: "restore", ArtistID: oldIdentity, ExpectedRevision: 2}}
	if _, err := f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split)); err != nil {
		t.Fatal(err)
	}
	if f.trackArtist(f.trackA) != oldIdentity || f.trackArtist(f.trackB) != f.beta || f.revision(oldIdentity) != 3 {
		t.Fatal("scoped restore made a new identity or expanded scope")
	}
	if len(f.port.calls) != 2 || f.port.calls[0][oldIdentity] != f.alpha || f.port.calls[1][oldIdentity] != oldIdentity {
		t.Fatal("scoped restore did not restore original favorite anchor")
	}
	snapshot := f.intentSnapshot()
	f.refresh(f.first.releaseID, f.second.releaseID)
	if snapshot != f.intentSnapshot() {
		t.Fatal("scoped restore did not survive rescan")
	}
}

package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func artistUndoKind(f *artistFixture, kind string) operations.Kind {
	for _, registered := range ArtistOperations(f.governor, nil) {
		if registered.Type() == kind {
			return registered
		}
	}
	panic("未登记的艺人操作 " + kind)
}

func artistUndo(f *artistFixture, kind, id string) (operations.Result, error) {
	return f.run(operations.TypeRevert, uuid.New(), id, operations.RevertExecutor(artistUndoKind(f, kind), id))
}

func mustArtistUndo(t *testing.T, f *artistFixture, kind string, original operations.Result) operations.Result {
	t.Helper()
	reverted, err := artistUndo(f, kind, original.OperationID)
	if err != nil {
		t.Fatalf("撤销 %s %s 失败：%v", kind, original.OperationID, err)
	}
	return reverted
}

func mustArtistRename(t *testing.T, f *artistFixture, id string, name *string) operations.Result {
	t.Helper()
	mode := "set"
	if name == nil {
		mode = "auto"
	}
	request := ArtistRenameRequest{ArtistID: id, ExpectedRevision: f.revision(id), Mode: mode, Name: name}
	result, err := f.run(operations.TypeArtistRename, uuid.New(), request.Fingerprint(), f.governor.RenameExecutor(request))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func addArtistRevertRelease(t *testing.T, f *artistFixture, name string) (placementRelease, string, string, string) {
	t.Helper()
	release := f.release(name)
	track := f.track(release, "合成曲目", "present", 100.0)
	f.exec(`UPDATE releases SET artist=$2,album_artist=$2 WHERE id=$1`, release.releaseID, name)
	f.exec(`UPDATE tracks SET artist=$2 WHERE id=$1`, track, name)
	f.refresh(release.releaseID)
	var artist, key string
	if err := f.database.QueryRow(`SELECT k.initial_artist_id::text,k.id::text FROM artist_name_keys k
 JOIN artist_names n ON n.key_id=k.id WHERE n.spelling=$1`, name).Scan(&artist, &key); err != nil {
		t.Fatal(err)
	}
	return release, track, artist, key
}

type favoriteRevertResolution struct{}

func (favoriteRevertResolution) RefreshArtistResolution(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
	return personal.RefreshArtistResolution(ctx, tx, mapping)
}

func TestPostgreSQLArtistMergeRevertRestoresIntent(t *testing.T) {
	f := newArtistFixture(t)
	f.governor = NewArtistGovernance(favoriteRevertResolution{})
	_, trackC, c, _ := addArtistRevertRelease(t, f, "Gamma")
	_, trackD, d, keyD := addArtistRevertRelease(t, f, "Delta")
	name := "乙艺人手工主名"
	mustArtistRename(t, f, f.beta, &name)
	favorites := personal.NewService(f.database)
	for _, artist := range []string{f.alpha, f.beta, c, d} {
		if _, err := favorites.SetFavorite(context.Background(), f.actorID, "artist", artist, true); err != nil {
			t.Fatal(err)
		}
	}
	first := mergedSourcesMerge(t, f, f.beta, d)
	// 构造两个旧作用域，让后续合并同时搬运全局绑定、发行覆盖和曲目覆盖。
	f.exec(`INSERT INTO artist_assignment_overrides (release_id,track_id,key_id,artist_id,operation_id)
 VALUES ($1,NULL,$3,$4,$5),(NULL,$2,$3,$4,$5)`, f.second.releaseID, f.trackB, f.keyBeta, f.beta, first.OperationID)
	f.refresh(f.second.releaseID)

	read := func(query string, args ...any) string {
		t.Helper()
		var result string
		if err := f.database.QueryRow(query, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	const intents = `SELECT jsonb_build_object(
 'bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY key_id) FROM artist_name_bindings b),
 'assignments',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM artist_assignment_overrides a),
 'overrides',(SELECT jsonb_agg(to_jsonb(o) ORDER BY artist_id) FROM artist_overrides o))::text`
	const credits = `SELECT jsonb_agg(jsonb_build_array(release_id,track_id,role,artist_id,name_id)
 ORDER BY release_id,track_id,role,artist_id,name_id)::text FROM artist_effective_credits`
	const names = `SELECT jsonb_build_object(
 'main',(SELECT jsonb_agg(jsonb_build_array(artist_id,effective_display_name) ORDER BY artist_id) FROM artist_profiles),
 'search',(SELECT jsonb_agg(jsonb_build_array(artist_id,original_value) ORDER BY artist_id,original_value) FROM artist_search_names))::text`
	const resolved = `SELECT jsonb_object_agg(artist_id,resolved_artist_id)::text FROM artist_favorites WHERE user_id=$1`
	baselineIntent, baselineCredits, baselineNames := read(intents), read(credits), read(names)
	baselineFavorites := read(resolved, f.actorID)
	beforeSources := mergedSourcesRead(t, f, f.beta)
	mergedSourcesAssert(t, beforeSources, d, first.OperationID, f.revision(d), keyD)

	merged := mergedSourcesMerge(t, f, c, f.beta)
	for _, track := range []string{f.trackB, trackC, trackD} {
		if got := f.trackArtist(track); got != c {
			t.Fatalf("合并后曲目 %s 的艺人为 %s，期望 %s", track, got, c)
		}
	}
	if got := read(`SELECT resolved_artist_id::text FROM artist_favorites WHERE user_id=$1 AND artist_id=$2`, f.actorID, d); got != c {
		t.Fatalf("扁平化旧身份的收藏解析为 %s，期望 %s", got, c)
	}
	undo := mustArtistUndo(t, f, operations.TypeArtistMerge, merged)
	if got := read(`SELECT redirect_operation_id::text FROM artist_overrides WHERE artist_id=$1`, d); got != first.OperationID {
		t.Errorf("旧身份的 redirect_operation_id 为 %s，期望 %s", got, first.OperationID)
	}
	for _, check := range []struct{ label, got, want string }{
		{"全局绑定、两种作用域与重定向", read(intents), baselineIntent},
		{"有效署名", read(credits), baselineCredits},
		{"主名与搜索名", read(names), baselineNames},
		{"收藏 resolved_artist_id", read(resolved, f.actorID), baselineFavorites},
	} {
		if check.got != check.want {
			t.Errorf("%s 恢复错误：%s，期望 %s", check.label, check.got, check.want)
		}
	}
	if got := mergedSourcesRead(t, f, f.beta); !reflect.DeepEqual(got, beforeSources) {
		t.Errorf("撤销后的 merged-sources 为 %+v，期望 %+v", got, beforeSources)
	}
	if got := mergedSourcesRead(t, f, c); len(got) != 0 {
		t.Errorf("撤销后的原目标仍有合并来源：%+v", got)
	}
	ctx := context.Background()
	record, err := operations.Load(ctx, f.database, undo.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := operations.Load(ctx, f.database, merged.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	before, after := artistRecordState{}, artistRecordState{}
	if err := json.Unmarshal(record.Before, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(record.After, &after); err != nil {
		t.Fatal(err)
	}
	wantChanges, _ := decodeArtistChanges(original)
	for _, changes := range []any{before.Changes, after.Changes} {
		raw, err := json.Marshal(changes)
		if err != nil {
			t.Fatal(err)
		}
		var got artistChangeDescription
		if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(&got, wantChanges) {
			t.Fatalf("撤销记录的 changes 为 %s，期望 %+v，错误 %v", raw, wantChanges, err)
		}
	}
	if record.TargetID != c || record.ChangeType() != operations.TypeArtistMerge ||
		len(before.Artists) != 3 || len(after.Artists) != 3 ||
		!sameString(before.Artists[2].RedirectArtistID, &c) || !sameString(after.Artists[2].RedirectArtistID, &f.beta) {
		t.Fatalf("撤销记录未保留目标及前后状态：%+v → %+v", before, after)
	}
	list, err := operations.List(ctx, f.database, operations.ListParams{Targets: artistTargets(d), Page: 1, PageSize: 10})
	if err != nil || len(list.Items) != 3 || list.Items[0].ID != undo.OperationID {
		t.Fatalf("按扁平化旧身份筛选撤销记录：%+v，错误 %v", list, err)
	}
}

func TestPostgreSQLArtistMergeRevertChainReverseOrder(t *testing.T) {
	f := newArtistFixture(t)
	_, trackC, c, _ := addArtistRevertRelease(t, f, "Gamma")
	first := mergedSourcesMerge(t, f, f.beta, f.alpha)
	second := mergedSourcesMerge(t, f, c, f.beta)
	_, err := artistUndo(f, operations.TypeArtistMerge, first.OperationID)
	assertArtistFailure(t, err, "revert_conflict")
	mustArtistUndo(t, f, operations.TypeArtistMerge, second)
	if f.trackArtist(f.trackA) != f.beta || f.trackArtist(f.trackB) != f.beta {
		t.Fatal("第一次撤销没有恢复 A→B")
	}
	mustArtistUndo(t, f, operations.TypeArtistMerge, first)
	if f.trackArtist(f.trackA) != f.alpha || f.trackArtist(f.trackB) != f.beta || f.trackArtist(trackC) != c {
		t.Fatal("倒序撤销后署名未恢复")
	}
	if a, b, cr := f.revision(f.alpha), f.revision(f.beta), f.revision(c); a != 3 || b != 5 || cr != 3 {
		t.Fatalf("撤销后的版本为 %d/%d/%d，期望 3/5/3", a, b, cr)
	}
}

func TestPostgreSQLArtistMergeRevertBlockedByLaterDecision(t *testing.T) {
	for _, tc := range []struct {
		name  string
		later func(*testing.T, *artistFixture, string)
	}{
		{"rename", func(t *testing.T, f *artistFixture, _ string) {
			name := "合并后的主名"
			mustArtistRename(t, f, f.alpha, &name)
		}},
		{"ignore", func(t *testing.T, f *artistFixture, third string) {
			var key string
			if err := f.database.QueryRow(`SELECT id::text FROM artist_name_keys WHERE initial_artist_id=$1`, third).Scan(&key); err != nil {
				t.Fatal(err)
			}
			pair := addArtistRevertSignal(f, f.keyAlpha, key)
			f.setCandidateIgnored(ArtistCandidate{Left: catalog.ArtistSummary{ID: f.alpha}, Right: catalog.ArtistSummary{ID: third}, KeyPairs: []ArtistKeyPair{pair}}, true)
		}},
		{"split", func(t *testing.T, f *artistFixture, _ string) {
			// 只改 Alpha 的专辑作用域，使合并写入的 Beta 路由、身份保持原样。
			scope := ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{f.keyAlpha}, GroupIDs: []string{f.first.groupID}}
			mergedSourcesSplit(t, f, f.alpha, scope, ArtistSplitTarget{Kind: "new"})
		}},
		{"D_to_C", func(t *testing.T, f *artistFixture, third string) {
			mergedSourcesMerge(t, f, f.alpha, third)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newArtistFixture(t)
			_, _, third, _ := addArtistRevertRelease(t, f, "Delta")
			merged := f.merge()
			tc.later(t, f, third)
			before, count := f.intentSnapshot(), f.count()
			_, err := artistUndo(f, operations.TypeArtistMerge, merged.OperationID)
			assertArtistFailure(t, err, "revert_conflict")
			if status, _, _ := operations.Problem(err); status != 409 || f.intentSnapshot() != before || f.count() != count {
				t.Fatalf("拒绝后状态变化或错误状态码 %d", status)
			}
		})
	}
}

func addArtistRevertSignal(f *artistFixture, left, right string) ArtistKeyPair {
	f.t.Helper()
	if left > right {
		left, right = right, left
	}
	f.exec(`INSERT INTO artist_candidate_signals(left_key_id,right_key_id,kind,release_id,left_name,right_name)
 VALUES($1,$2,'album_track_one_to_one',$3,'合成左艺人','合成右艺人')`, left, right, f.first.releaseID)
	return ArtistKeyPair{LeftKeyID: left, RightKeyID: right}
}

func TestPostgreSQLArtistMergeRevertAfterRescan(t *testing.T) {
	f := newArtistFixture(t)
	merged := f.merge()
	added := f.track(f.second, "重扫发现的新曲目", "present", 110.0)
	f.exec(`UPDATE tracks SET artist='Beta',observed_at=clock_timestamp() WHERE id=$1`, added)
	f.refresh(f.second.releaseID)
	if f.trackArtist(added) != f.alpha {
		t.Fatal("合并期间新署名没有沿用合并路由")
	}
	mustArtistUndo(t, f, operations.TypeArtistMerge, merged)
	if f.trackArtist(added) != f.beta || f.trackArtist(f.trackB) != f.beta {
		t.Fatal("撤销没有还原重扫前后的 Beta 署名")
	}
}

func TestPostgreSQLArtistRenameRevertChain(t *testing.T) {
	f := newArtistFixture(t)
	firstName, secondName := "第一人工主名", "第二人工主名"
	first := mustArtistRename(t, f, f.beta, &firstName)
	second := mustArtistRename(t, f, f.beta, &secondName)
	auto := mustArtistRename(t, f, f.beta, nil)
	_, err := artistUndo(f, operations.TypeArtistRename, first.OperationID)
	assertArtistFailure(t, err, "revert_conflict")
	for _, step := range []struct {
		original             operations.Result
		name, writer, absent string
	}{
		{auto, secondName, second.OperationID, firstName},
		{second, firstName, first.OperationID, secondName},
		{first, "Beta", "", firstName},
	} {
		mustArtistUndo(t, f, operations.TypeArtistRename, step.original)
		var name, writer string
		var stale bool
		err := f.database.QueryRow(`SELECT p.effective_display_name,COALESCE(o.name_operation_id::text,''),
 EXISTS(SELECT 1 FROM artist_search_names WHERE artist_id=p.artist_id AND original_value=$2)
 FROM artist_profiles p LEFT JOIN artist_overrides o USING (artist_id) WHERE p.artist_id=$1`, f.beta, step.absent).Scan(&name, &writer, &stale)
		if err != nil || name != step.name || writer != step.writer || stale {
			t.Fatalf("撤销 %s 后主名=%q 写入者=%q 旧搜索名=%t，错误 %v", step.original.OperationID, name, writer, stale, err)
		}
	}
}

func TestPostgreSQLArtistCandidateIgnoreRevertMixedPairs(t *testing.T) {
	f := newArtistFixture(t)
	_, _, alias, aliasKey := addArtistRevertRelease(t, f, "Beta Variant")
	_, _, extra, extraKey := addArtistRevertRelease(t, f, "Beta Extra")
	mergedSourcesMerge(t, f, f.beta, alias)
	mergedSourcesMerge(t, f, f.beta, extra)
	firstPair := addArtistRevertSignal(f, f.keyAlpha, f.keyBeta)
	secondPair := addArtistRevertSignal(f, f.keyAlpha, aliasKey)
	thirdPair := addArtistRevertSignal(f, f.keyAlpha, extraKey)
	candidate := ArtistCandidate{Left: catalog.ArtistSummary{ID: f.alpha}, Right: catalog.ArtistSummary{ID: f.beta}, KeyPairs: []ArtistKeyPair{firstPair}}
	first := f.setCandidateIgnored(candidate, true)
	candidate.KeyPairs = []ArtistKeyPair{firstPair, secondPair}
	mixed := f.setCandidateIgnored(candidate, true)
	candidate.KeyPairs = []ArtistKeyPair{firstPair, secondPair, thirdPair}
	unignore := f.setCandidateIgnored(candidate, false)
	check := func(pair ArtistKeyPair, want string) {
		t.Helper()
		var got string
		err := f.database.QueryRow(`SELECT COALESCE((SELECT operation_id::text FROM artist_candidate_ignores WHERE left_key_id=$1 AND right_key_id=$2),'')`, pair.LeftKeyID, pair.RightKeyID).Scan(&got)
		if err != nil || got != want {
			t.Errorf("键对 %+v 写入者 %q，期望 %q，错误 %v", pair, got, want, err)
		}
	}
	mustArtistUndo(t, f, operations.TypeArtistCandidateIgnore, unignore)
	check(firstPair, first.OperationID)
	check(secondPair, mixed.OperationID)
	check(thirdPair, "")
	mustArtistUndo(t, f, operations.TypeArtistCandidateIgnore, mixed)
	check(firstPair, first.OperationID)
	check(secondPair, "")
	check(thirdPair, "")
	mustArtistUndo(t, f, operations.TypeArtistCandidateIgnore, first)
	check(firstPair, "")
}

func artistRevertDescription(t *testing.T, f *artistFixture, kind string, id string) operations.Description {
	t.Helper()
	ctx := context.Background()
	record, err := operations.Load(ctx, f.database, id)
	if err != nil {
		t.Fatal(err)
	}
	descriptions, err := artistUndoKind(f, kind).Describe(ctx, f.database, []operations.Record{record})
	if err != nil {
		t.Fatal(err)
	}
	description, ok := descriptions[id]
	if !ok {
		t.Fatalf("列表缺少操作 %s", id)
	}
	return description
}

func TestPostgreSQLArtistRevertDescribeMatchesExecutor(t *testing.T) {
	for _, mode := range []string{"available", "invisible", "later_decision", "changed_intent", "damaged_snapshot", "already_reverted", "revert_record"} {
		t.Run(mode, func(t *testing.T) {
			f := newArtistFixture(t)
			original := f.merge()
			id, want := original.OperationID, ""
			switch mode {
			case "invisible":
				f.exec(`UPDATE tracks SET source_status='missing' WHERE id IN ($1,$2)`, f.trackA, f.trackB)
				f.refresh(f.first.releaseID, f.second.releaseID)
			case "later_decision":
				name := "后续名称"
				mustArtistRename(t, f, f.alpha, &name)
				want = "revert_conflict"
			case "changed_intent":
				f.exec(`DELETE FROM artist_name_bindings WHERE key_id=$1`, f.keyBeta)
				want = "revert_conflict"
			case "damaged_snapshot":
				f.exec(`UPDATE operations SET after_state=after_state-'bindings' WHERE id=$1`, id)
				want = "revert_conflict"
			case "already_reverted":
				mustArtistUndo(t, f, operations.TypeArtistMerge, original)
				want = "already_reverted"
			case "revert_record":
				id = mustArtistUndo(t, f, operations.TypeArtistMerge, original).OperationID
				want = "not_revertible"
			}
			description := artistRevertDescription(t, f, operations.TypeArtistMerge, id)
			if mode == "invisible" && (description.Artist == nil || description.Artist.Visible) {
				t.Fatalf("目标应不可见：%+v", description)
			}
			_, err := artistUndo(f, operations.TypeArtistMerge, id)
			if want != "" {
				assertArtistFailure(t, err, want)
			} else if err != nil {
				t.Fatal(err)
			}
			if description.Revertible != (err == nil) {
				t.Fatalf("列表 revertible=%t，执行结果 %v", description.Revertible, err)
			}
		})
	}
}

func TestPostgreSQLArtistRevertConcurrent(t *testing.T) {
	f := newArtistFixture(t)
	merged := f.merge()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gate, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback()
	if err := writegate.Lock(ctx, gate); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			execute := operations.RevertExecutor(artistUndoKind(f, operations.TypeArtistMerge), merged.OperationID)
			_, err := operations.Run(ctx, f.database, operations.Spec{ActorID: f.actorID, Type: operations.TypeRevert, Key: uuid.New(), Fingerprint: merged.OperationID},
				func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
					entered <- struct{}{}
					return execute(ctx, tx, id)
				})
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := gate.Commit(); err != nil {
		t.Fatal(err)
	}
	success := 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else {
			assertArtistFailure(t, err, "already_reverted")
		}
	}
	if success != 1 || f.count() != 2 || f.trackArtist(f.trackB) != f.beta || f.revision(f.alpha) != 3 || f.revision(f.beta) != 3 {
		t.Fatalf("并发撤销成功 %d 次，操作记录 %d 条", success, f.count())
	}
}

func TestPostgreSQLArtistRevertCatalogNotReady(t *testing.T) {
	f := newArtistFixture(t)
	merged := f.merge()
	f.ready(false)
	before := f.intentSnapshot()
	if d := artistRevertDescription(t, f, operations.TypeArtistMerge, merged.OperationID); d.Revertible {
		t.Fatal("目录整理期间仍显示可撤销")
	}
	_, err := artistUndo(f, operations.TypeArtistMerge, merged.OperationID)
	assertArtistFailure(t, err, "artist_catalog_not_ready")
	if status, _, _ := operations.Problem(err); status != 409 || f.intentSnapshot() != before || f.count() != 1 {
		t.Fatalf("目录未就绪时状态变化或错误状态码 %d", status)
	}
	f.ready(true)
	mustArtistUndo(t, f, operations.TypeArtistMerge, merged)
}

// 较早开启的事务故意后拿 CATP，让事务开始时间与实际操作顺序相反。
func TestPostgreSQLArtistRevertUsesInsertOrder(t *testing.T) {
	f := newArtistFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	laterBegan := make(chan time.Time, 1)
	allowLater := make(chan struct{})
	type operation struct {
		result operations.Result
		err    error
	}
	laterDone := make(chan operation, 1)
	name := "后写入的人工主名"
	later := ArtistRenameRequest{ArtistID: f.alpha, ExpectedRevision: 2, Mode: "set", Name: &name}
	go func() {
		result, err := operations.Run(ctx, f.database, operations.Spec{
			ActorID: f.actorID, Type: operations.TypeArtistRename, Key: uuid.New(), Fingerprint: later.Fingerprint(),
		}, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
			var started time.Time
			if err := tx.QueryRowContext(ctx, "SELECT transaction_timestamp()").Scan(&started); err != nil {
				return operations.Outcome{}, err
			}
			laterBegan <- started
			select {
			case <-allowLater:
				return f.governor.RenameExecutor(later)(ctx, tx, id)
			case <-ctx.Done():
				return operations.Outcome{}, ctx.Err()
			}
		})
		laterDone <- operation{result, err}
	}()
	var started time.Time
	select {
	case started = <-laterBegan:
	case result := <-laterDone:
		t.Fatalf("后写入事务未进入执行器：%v", result.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	merge := ArtistMergeRequest{ArtistID: f.alpha, ExpectedRevision: 1, SourceArtistID: f.beta, SourceExpectedRevision: 1}
	earlier, err := operations.Run(ctx, f.database, operations.Spec{
		ActorID: f.actorID, Type: operations.TypeArtistMerge, Key: uuid.New(), Fingerprint: merge.Fingerprint(),
	}, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		if err := writegate.Lock(ctx, tx); err != nil {
			return operations.Outcome{}, err
		}
		var mergeStarted time.Time
		if err := tx.QueryRowContext(ctx, "SELECT transaction_timestamp()").Scan(&mergeStarted); err != nil {
			return operations.Outcome{}, err
		}
		if !started.Before(mergeStarted) {
			t.Errorf("事务开始顺序未倒置：改名 %s，合并 %s", started, mergeStarted)
		}
		close(allowLater)
		return f.governor.MergeExecutor(merge)(ctx, tx, id)
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := <-laterDone
	if finished.err != nil {
		t.Fatal(finished.err)
	}
	var mergeCreated, renameCreated time.Time
	err = f.database.QueryRow(`SELECT a.created_at,b.created_at FROM operations a CROSS JOIN operations b WHERE a.id=$1 AND b.id=$2`,
		earlier.OperationID, finished.result.OperationID).Scan(&mergeCreated, &renameCreated)
	if err != nil {
		t.Fatal(err)
	}
	if !mergeCreated.Before(renameCreated) {
		t.Errorf("操作记录未按插入时间排序：合并 %s，改名 %s", mergeCreated, renameCreated)
	}
	_, err = artistUndo(f, operations.TypeArtistMerge, earlier.OperationID)
	assertArtistFailure(t, err, "revert_conflict")
}

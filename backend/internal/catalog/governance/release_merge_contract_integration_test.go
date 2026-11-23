package governance

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (f *effectiveFixture) mergeRequest(target, source string) ReleaseMergeRequest {
	f.t.Helper()
	preview, err := f.governance.PreviewReleaseMerge(f.t.Context(), target, source)
	if err != nil {
		f.t.Fatal(err)
	}
	return ReleaseMergeRequest{TargetID: target, SourceID: source, Token: preview.Token}
}

func (f *effectiveFixture) runReleaseMerge(ctx context.Context, kind, key string, req ReleaseMergeRequest) (operations.Result, error) {
	spec := operations.Spec{ActorID: f.actorID, Type: kind, Key: key, Fingerprint: req.Fingerprint(), RequestID: "test"}
	if kind == operations.TypeReleaseAutoMerge {
		spec.Origin = &operations.Origin{Source: "curation", ProviderKind: "model", Provider: "test-model"}
	}
	return operations.Run(ctx, f.database, spec, ReleaseMergeExecutor(req))
}

func (f *effectiveFixture) mergeRelease(target, source, kind string) operations.Result {
	f.t.Helper()
	op, err := f.runReleaseMerge(f.t.Context(), kind, uuid.New(), f.mergeRequest(target, source))
	if err != nil {
		f.t.Fatal(err)
	}
	return op
}

// 治理夹具只控制写标记的成功与失败；真实写入器由扫描夹具的撤销路径验证。
type mergeTestSuppressions struct{ err error }

func (s mergeTestSuppressions) SuppressReleaseMerge(context.Context, *sql.Tx, string, string, string, string) error {
	return s.err
}

func requireMergeError(t *testing.T, err error, status int, code string) {
	t.Helper()
	gotStatus, gotCode, _ := operations.Problem(err)
	if gotStatus != status || gotCode != code {
		t.Fatalf("错误=%v (%d %s)，期望 %d %s", err, gotStatus, gotCode, status, code)
	}
}

func TestPostgreSQLReleaseMergeManual(t *testing.T) {
	f := newArtistFixture(t)
	a, b := f.first, f.second
	f.correct(b.releaseID, "title", "合成来源标题")
	before, err := f.store.LoadReleaseSummary(t.Context(), a.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	req := f.mergeRequest(a.releaseID, b.releaseID)
	key := uuid.New()
	op, err := f.runReleaseMerge(t.Context(), operations.TypeReleaseMerge, key, req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.runReleaseMerge(t.Context(), operations.TypeReleaseMerge, key, req)
	if err != nil || replay.OperationID != op.OperationID {
		t.Fatalf("幂等重放=%+v err=%v", replay, err)
	}
	after, err := f.store.LoadReleaseSummary(t.Context(), a.releaseID)
	if err != nil || after.Title != before.Title || after.Artist != before.Artist || after.TrackCount != 2 || after.MediumCount != 2 {
		t.Fatalf("A 摘要=%+v err=%v", after, err)
	}
	media, err := f.store.LoadReleaseMedia(t.Context(), a.releaseID)
	if err != nil || len(media) != 2 || media[0].ID != a.mediumID || media[1].ID != b.mediumID || media[0].Position != 1 || media[1].Position != 2 ||
		len(media[0].Tracks) != 1 || len(media[1].Tracks) != 1 || media[0].Tracks[0].ID != f.trackA || media[1].Tracks[0].ID != f.trackB {
		t.Fatalf("A 的碟与曲目=%+v err=%v", media, err)
	}
	if _, err = f.store.LoadReleaseSummary(t.Context(), b.releaseID); !errors.Is(err, catalog.ErrReleaseNotFound) {
		t.Fatalf("已并入的 B 详情=%v", err)
	}
	groups, err := f.store.ListReleaseGroups(t.Context(), catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || len(groups.Items) != 1 || groups.Items[0].ID != a.groupID {
		t.Fatalf("专辑列表=%+v err=%v", groups, err)
	}
	for _, tc := range []struct {
		query string
		want  int64
	}{{"第一张", 1}, {"合成来源标题", 0}} {
		result, err := f.store.Search(t.Context(), catalog.SearchParams{Type: "albums", Query: tc.query, Page: 1, PageSize: 10})
		if err != nil || result.Pagination.Total != tc.want {
			t.Fatalf("搜索 %s=%+v err=%v", tc.query, result, err)
		}
	}
	albums, err := f.store.ListArtistAlbums(t.Context(), f.beta, 1, 10, catalog.ArtistAlbumOptions{Participation: "any"})
	if err != nil || len(albums.Items) != 1 || albums.Items[0].Group.ID != a.groupID || albums.Items[0].CreditedReleaseID != a.releaseID {
		t.Fatalf("B 曲目艺人的专辑=%+v err=%v", albums, err)
	}
	placements, err := f.store.LoadTrackPlacements(t.Context(), []string{f.trackA, f.trackB})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.trackA, f.trackB} {
		if p := placements[id]; p.Release.ID != a.releaseID || p.GroupID != a.groupID {
			t.Fatalf("曲目 %s 的位置=%+v", id, p)
		}
	}
	claims, err := f.governance.ListClaims(t.Context(), ClaimListParams{1, 10})
	if err != nil || claims.Total != 0 {
		t.Fatalf("来源有标题修正，并入后的待认领列表=%+v err=%v", claims, err)
	}
	claim := ClaimRequest{ReleaseID: a.releaseID, SourceID: b.releaseID, ExpectedRevision: 1, SourceRevision: 2, Preview: "stale"}
	_, err = f.run(operations.TypeReleaseClaim, uuid.New(), claim.Fingerprint(), ClaimExecutor(claim))
	requireMergeError(t, err, 409, "not_claimable")
}

func TestPostgreSQLLayoutMergedMedia(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second []int
		want          []int
	}{
		{"两个连续双碟", []int{1, 2}, []int{1, 2}, []int{1, 2, 3, 4, 5}},
		{"保留原碟号跳号", []int{3, 5}, []int{1, 8}, []int{1, 3, 5, 6, 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCorrectionFixture(t)
			a := f.visibleRelease("A")
			ids := []string{a.mediumID}
			for i, positions := range [][]int{tc.first, tc.second} {
				r := f.visibleRelease([]string{"B", "C"}[i])
				f.exec(`UPDATE media SET position=$2 WHERE id=$1`, r.mediumID, positions[0])
				ids = append(ids, r.mediumID)
				for _, pos := range positions[1:] {
					medium := uuid.New()
					f.exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,$3)`, medium, r.releaseID, pos)
					f.track(placementRelease{mediumID: medium}, "附碟", "present", 100)
					ids = append(ids, medium)
				}
				f.mergeRelease(a.releaseID, r.releaseID, operations.TypeReleaseMerge)
			}
			media, err := f.store.LoadReleaseMedia(t.Context(), a.releaseID)
			if err != nil || len(media) != len(ids) {
				t.Fatalf("碟=%+v err=%v", media, err)
			}
			for i, m := range media {
				if m.ID != ids[i] || m.Position != tc.want[i] {
					t.Fatalf("第 %d 张碟=(%s,%d)，期望 (%s,%d)", i+1, m.ID, m.Position, ids[i], tc.want[i])
				}
			}
		})
	}
}

func TestPostgreSQLReleaseMergeRevert(t *testing.T) {
	s := newVisibleClaimScenario(t)
	f, a, b := s.fixture, s.target, s.source
	var track string
	if err := f.database.QueryRow(`SELECT id::text FROM tracks WHERE medium_id=$1 AND source_status='present' ORDER BY id LIMIT 1`, b.mediumID).Scan(&track); err != nil {
		t.Fatal(err)
	}
	fields, reviews := s.correctionFields(b.releaseID), s.reviewIssues(b.releaseID)
	aRev, bRev := f.revisionOf(a.releaseID), f.revisionOf(b.releaseID)
	op := f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge)
	if f.revisionOf(a.releaseID) != aRev || f.revisionOf(b.releaseID) != bRev+1 {
		t.Fatal("并入后发行版本号不符合预期")
	}
	group, err := f.store.LoadReleaseGroup(t.Context(), s.album)
	if err != nil || group.DefaultReleaseID != nil {
		t.Fatalf("隐藏来源后的默认版本=%+v err=%v", group, err)
	}
	req := TrackCorrectionRequest{TrackID: track, ReleaseID: a.releaseID, ExpectedRevision: 1, Changes: map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "并入期间的修正"}}}
	_, err = operations.Run(t.Context(), f.database, operations.Spec{ActorID: f.actorID, Type: TypeTrackCorrection, Key: uuid.New(), Fingerprint: req.Fingerprint(), RequestID: "test"}, TrackCorrectionExecutor(req))
	if err != nil {
		t.Fatal(err)
	}
	undo := f.mustRevert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), op.OperationID)
	if got := f.revisionOf(b.releaseID); got != bRev+2 {
		t.Fatalf("撤销后 B 版本=%d", got)
	}
	summary, err := f.store.LoadReleaseSummary(t.Context(), b.releaseID)
	if err != nil || summary.Title != "我改过的标题" || !reflect.DeepEqual(s.correctionFields(b.releaseID), fields) || !reflect.DeepEqual(s.reviewIssues(b.releaseID), reviews) {
		t.Fatalf("来源修正、核对恢复失败=%+v err=%v", summary, err)
	}
	p, err := f.store.LoadTrackPlacements(t.Context(), []string{track})
	if err != nil || p[track].Release.ID != b.releaseID || p[track].Title != "并入期间的修正" {
		t.Fatalf("还原后的曲目=%+v err=%v", p, err)
	}
	group, err = f.store.LoadReleaseGroup(t.Context(), s.album)
	if err != nil || group.DefaultReleaseID == nil || *group.DefaultReleaseID != b.releaseID {
		t.Fatalf("还原后的默认版本=%+v err=%v", group, err)
	}
	page, err := operations.List(t.Context(), f.database, operations.ListParams{Targets: releaseTargets(b.releaseID), Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, item := range page.Items {
		found[item.ID] = item.Type
	}
	if found[op.OperationID] != operations.TypeReleaseMerge || found[undo.OperationID] != operations.TypeRevert {
		t.Fatalf("来源的操作记录=%v", found)
	}
}

func TestPostgreSQLReleaseMergeRevertGuards(t *testing.T) {
	t.Run("来源版本改变", func(t *testing.T) {
		f := newCorrectionFixture(t)
		a, b := f.visibleRelease("A"), f.visibleRelease("B")
		op := f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge)
		f.exec(`UPDATE releases SET edit_revision=edit_revision+1 WHERE id=$1`, b.releaseID)
		_, err := f.revert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), op.OperationID)
		requireMergeError(t, err, 409, "revert_conflict")
		requireActiveMerge(t, f, a, b, op.OperationID)
	})
	t.Run("写标记失败整体回滚", func(t *testing.T) {
		f := newCorrectionFixture(t)
		a, b := f.visibleRelease("A"), f.visibleRelease("B")
		op := f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge)
		_, err := f.revert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{errors.New("标记写入失败")}), op.OperationID)
		if err == nil {
			t.Fatal("写标记失败时撤销却成功")
		}
		requireActiveMerge(t, f, a, b, op.OperationID)
		if f.revisionOf(b.releaseID) != 2 || f.operationCount() != 1 {
			t.Fatal("标记失败后发行或操作仍发生变化")
		}
	})
	for _, kind := range []string{operations.TypeReleaseAutoMerge, operations.TypeReleaseMerge} {
		for _, later := range []string{"归组", "认领", "合并"} {
			t.Run(kind+"/"+later, func(t *testing.T) {
				f := newCorrectionFixture(t)
				a, b, c := f.visibleRelease("A"), f.visibleRelease("B"), f.visibleRelease("C")
				if later == "认领" {
					f.mustCorrect(c.releaseID, 1, map[string]CorrectionChange{"title": {Mode: catalog.CorrectionSet, Text: "认领的修正"}})
					f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1`, c.mediumID)
				}
				op := f.mergeRelease(a.releaseID, b.releaseID, kind)
				var subsequent operations.Result
				var undoKind operations.Kind
				switch later {
				case "归组":
					_, subsequent = f.mustChangeGroup(a.releaseID, 1, GroupJoin, c.groupID)
					undoKind = GroupChangeOperations(testGroupSuppressions{})
				case "认领":
					scenario := claimScenario{fixture: f, target: a, source: c}
					_, subsequent = scenario.mustClaim(t)
					undoKind = ClaimOperations()
				case "合并":
					subsequent = f.mergeRelease(a.releaseID, c.releaseID, operations.TypeReleaseMerge)
					undoKind = ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{})
				}
				originalKind := ReleaseMergeOperations(kind, mergeTestSuppressions{})
				if kind == operations.TypeReleaseAutoMerge {
					_, err := f.revert(originalKind, op.OperationID)
					requireMergeError(t, err, 409, "revert_conflict")
					requireActiveMerge(t, f, a, b, op.OperationID)
					f.mustRevert(undoKind, subsequent.OperationID)
				}
				f.mustRevert(originalKind, op.OperationID)
			})
		}
	}
}
func requireActiveMerge(t *testing.T, f *correctionFixture, a, b placementRelease, op string) {
	t.Helper()
	var active bool
	if err := f.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM release_merges r JOIN media m ON m.origin_release_id=r.source_release_id AND m.release_id=r.target_release_id
    WHERE m.id=$1 AND r.target_release_id=$2 AND r.source_release_id=$3 AND r.operation_id=$4)`, b.mediumID, a.releaseID, b.releaseID, op).Scan(&active); err != nil || !active {
		t.Fatalf("原合并关系=%v err=%v", active, err)
	}
}

func (f *effectiveFixture) mergeGuardSnapshot() string {
	f.t.Helper()
	var state string
	err := f.database.QueryRow(`SELECT jsonb_build_array(
        (SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM releases r),
        (SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM media m),
        (SELECT jsonb_agg(to_jsonb(r) ORDER BY source_release_id) FROM release_merges r),
        (SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM operations o),
        (SELECT jsonb_agg(to_jsonb(f) ORDER BY user_id,group_id) FROM favorite_albums f),
        (SELECT jsonb_agg(to_jsonb(s) ORDER BY user_id) FROM personal_favorite_state s)
    )::text`).Scan(&state)
	if err != nil {
		f.t.Fatal(err)
	}
	return state
}

func TestPostgreSQLReleaseMergeGuards(t *testing.T) {
	f := newCorrectionFixture(t)
	a, b, c := f.visibleRelease("A"), f.visibleRelease("B"), f.visibleRelease("C")
	f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge)
	hidden := f.release("不可见")
	for _, tc := range []struct {
		name, target, source string
		status               int
		code                 string
	}{
		{"相同来源", b.releaseID, b.releaseID, 400, "invalid_input"},
		{"无效标识", "bad", b.releaseID, 400, "invalid_input"},
		{"未知优先于已并入", uuid.New(), b.releaseID, 404, "not_found"},
		{"目标已经并入", b.releaseID, c.releaseID, 409, "release_merged"},
		{"来源已经并入", c.releaseID, b.releaseID, 409, "release_merged"},
		{"已并入优先于链", b.releaseID, a.releaseID, 409, "release_merged"},
		{"链优先于不可见", hidden.releaseID, a.releaseID, 409, "merge_chain"},
		{"目标不可见", hidden.releaseID, c.releaseID, 404, "not_found"},
		{"来源不可见", c.releaseID, hidden.releaseID, 404, "not_found"},
		{"令牌过期", a.releaseID, c.releaseID, 409, "merge_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.mergeGuardSnapshot()
			req := ReleaseMergeRequest{TargetID: tc.target, SourceID: tc.source, Token: "过期"}
			_, err := f.runReleaseMerge(t.Context(), operations.TypeReleaseMerge, uuid.New(), req)
			requireMergeError(t, err, tc.status, tc.code)
			if f.mergeGuardSnapshot() != before {
				t.Fatal("拒绝后数据改变")
			}
		})
	}
	for _, limit := range []string{"碟", "曲目", "署名"} {
		t.Run(limit, func(t *testing.T) {
			f := newCorrectionFixture(t)
			a, b := f.visibleRelease("A"), f.visibleRelease("B")
			switch limit {
			case "碟":
				f.exec(`WITH discs AS (INSERT INTO media(id,release_id,position) SELECT gen_random_uuid(),$1,n FROM generate_series(2,256) n RETURNING id,position)
                INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at)
                SELECT gen_random_uuid(),id,1,'曲目','艺人',$2,'disc-'||position,now() FROM discs`, a.releaseID, f.rootID)
			case "曲目":
				f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at)
                SELECT gen_random_uuid(),$1,n,'曲目','艺人',$2,'track-'||n,now() FROM generate_series(2,10000) n`, a.mediumID, f.rootID)
			case "署名":
				f.exec(`INSERT INTO track_credits(track_id,role,name,position)
                SELECT t.id,'performer','署名'||n,n FROM tracks t CROSS JOIN generate_series(1,10000) n WHERE medium_id=$1`, a.mediumID)
				f.exec(`INSERT INTO track_credits(track_id,role,name,position) SELECT id,'composer','另一个署名',1 FROM tracks WHERE medium_id=$1`, b.mediumID)
			}
			before := f.mergeGuardSnapshot()
			_, err := f.runReleaseMerge(t.Context(), operations.TypeReleaseMerge, uuid.New(), ReleaseMergeRequest{TargetID: a.releaseID, SourceID: b.releaseID, Token: "过期"})
			requireMergeError(t, err, 409, "release_too_large")
			if f.mergeGuardSnapshot() != before {
				t.Fatal("超过上限后数据改变")
			}
		})
	}
}

func TestPostgreSQLReleaseMergeArtistOverrides(t *testing.T) {
	artists := newArtistFixture(t)
	a, b := artists.first, artists.second
	// 同一个名字在两张发行上分别指派给 Alpha 和 Beta，曲目没有曲目级覆盖。
	f := &correctionFixture{artists.effectiveFixture}
	op := f.mustCorrect(b.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionSet, Genres: []string{"测试"}}})
	f.exec(`INSERT INTO artist_assignment_overrides(release_id,key_id,artist_id,operation_id) VALUES($1,$3,$4,$6),($2,$3,$5,$6)`, a.releaseID, b.releaseID, artists.keyBeta, artists.alpha, artists.beta, op.OperationID)
	artists.refresh(a.releaseID, b.releaseID)
	if got := artists.trackArtist(artists.trackB); got != artists.beta {
		t.Fatalf("合并前 B 艺人=%s", got)
	}
	preview, err := f.governance.PreviewReleaseMerge(t.Context(), a.releaseID, b.releaseID)
	if err != nil || preview.Hidden.ArtistOverrides != 1 {
		t.Fatalf("艺人覆盖预览=%+v err=%v", preview.Hidden, err)
	}
	merged := f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge)
	if got := artists.trackArtist(artists.trackB); got != artists.alpha {
		t.Fatalf("合并后 B 曲目艺人=%s，期望 A 的覆盖 %s", got, artists.alpha)
	}
	f.mustRevert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), merged.OperationID)
	if got := artists.trackArtist(artists.trackB); got != artists.beta {
		t.Fatalf("撤销后 B 曲目艺人=%s，期望 %s", got, artists.beta)
	}
}

func TestPostgreSQLReleaseMergeRevertFavorites(t *testing.T) {
	f := newCorrectionFixture(t)
	a, b := f.visibleRelease("A"), f.visibleRelease("B")
	users := []string{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for _, user := range users {
		f.exec(`INSERT INTO users(id,username,password_hash) VALUES($1,$1::uuid::text,'test-only')`, user)
		f.exec(`INSERT INTO personal_favorite_state(user_id,revision) VALUES($1,10)`, user)
		f.exec(`INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist)
          VALUES($1,$2,'2026-01-02 03:04:05.765432Z','原收藏标题','原收藏艺人')`, user, b.groupID)
	}
	f.exec(`INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist)
        VALUES($1,$2,'2025-10-12 03:04:05Z','原目标标题','原目标艺人')`, users[1], a.groupID)
	snapshots := func() map[string]string {
		t.Helper()
		rows, err := f.database.Query(`SELECT user_id::text||'/'||group_id::text,jsonb_build_array(created_at,saved_title,saved_artist)::text FROM favorite_albums`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			result[k] = v
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	revisions := func(want int) {
		t.Helper()
		var n int
		if err := f.database.QueryRow(`SELECT count(*) FROM personal_favorite_state WHERE user_id=ANY($1::uuid[]) AND revision=$2`, users, want).Scan(&n); err != nil || n != len(users) {
			t.Fatalf("版本为 %d 的用户=%d err=%v", want, n, err)
		}
	}
	before := snapshots()
	preview, err := f.governance.PreviewReleaseMerge(t.Context(), a.releaseID, b.releaseID)
	if err != nil || preview.FavoritesMoved != 4 {
		t.Fatalf("收藏转移预览=%+v err=%v", preview, err)
	}
	op := f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge)
	revisions(11)
	moved := snapshots()
	if len(moved) != 4 {
		t.Fatalf("转移后的收藏=%v", moved)
	}
	for _, user := range users {
		want := before[user+"/"+b.groupID]
		if user == users[1] {
			want = before[user+"/"+a.groupID]
		}
		if moved[user+"/"+a.groupID] != want {
			t.Fatalf("转移用户 %s 的目标快照=%v，期望 %v", user, moved[user+"/"+a.groupID], want)
		}
	}
	f.exec(`UPDATE favorite_albums SET created_at=created_at+interval '1 microsecond' WHERE user_id=$1`, users[2])
	f.exec(`UPDATE favorite_albums SET saved_artist='后来收藏的艺人' WHERE user_id=$1`, users[3])
	changed := snapshots()
	f.mustRevert(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), op.OperationID)
	revisions(12)
	for _, user := range users[2:] {
		key := user + "/" + a.groupID
		before[key] = changed[key]
	}
	if restored := snapshots(); !reflect.DeepEqual(restored, before) {
		t.Fatalf("撤销后的收藏=%v，期望 %v", restored, before)
	}
}

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

func TestPostgreSQLJudgeBackgroundDoesNotPatrolSearchOrReview(t *testing.T) {
	var web atomic.Int32
	f := newSchedulerFixtureWithSources(t, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/searxng") {
			web.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	item := modelRouteFixture(t, f, f.pending(t, f.a), "")
	f.enable(t)

	request := f.next(t, false)
	if len(request.Tools) != 0 || strings.Contains(curationRulePrompt(t, request), "submit_patrol") {
		t.Fatalf("后台识别轮收到巡检、搜索或其他工具：%+v", request.Tools)
	}
	request.undetermined(t, item.ID, "本地轨道与外部资料无法定案")
	modelWaitResolution(t, f, item.ID, "undetermined")

	f.clock.now.Add(int64(6 * time.Minute))
	select {
	case next := <-f.requests:
		t.Fatalf("定不了后再次启动后台模型 %s，工具 %+v", next.Model, next.Tools)
	case <-time.After(120 * time.Millisecond):
	}
	app := f.application
	b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_reviews`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='name_patrol' OR route IN ('lookup','web')`)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_tool_calls WHERE tool_name IN
	 ('submit_patrol','search_music_metadata','get_music_metadata','web_search','fetch_page')`)
	if web.Load() != 0 || f.requestCount.Load() != 1 {
		t.Fatalf("后台搜索请求=%d，模型请求=%d，期望 0 和 1", web.Load(), f.requestCount.Load())
	}

	f.pause(t)
	chat := f.startChat(t)
	f.next(t, false).tool(t, "correct_release", schedulerCorrection(f.a, 1, "edition", setChange("对话采用的版本")))
	f.next(t, true).review(t, "approved")
	f.next(t, false).complete(t)
	finishSchedulerChat(t, chat)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM steward_reviews`)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND edition='对话采用的版本'`, f.a)
}

func TestPostgreSQLJudgeConflictSelectionRevalidatesSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*testing.T, *programFixture, curation.MatchChoice)
	}{
		{"文件变化", "item_stale", func(t *testing.T, f *programFixture, _ curation.MatchChoice) {
			mustExec(t, f.application, `UPDATE tracks SET source_size=1234 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.a)
		}},
		{"曲长反证", "insufficient_corroboration", func(_ *testing.T, f *programFixture, choice curation.MatchChoice) {
			r := f.source.releases[choice.ExternalID]
			duration := int64(140000)
			r.Media[0].Tracks[0].LengthMS = &duration
			f.source.releases[choice.ExternalID] = r
		}},
		{"轨道错位", "insufficient_corroboration", func(_ *testing.T, f *programFixture, choice curation.MatchChoice) {
			r := f.source.releases[choice.ExternalID]
			r.Media[0].Tracks[0].Position = 2
			f.source.releases[choice.ExternalID] = r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, matcher, choice, _ := selectionFixture(t)
			tc.change(t, f, choice)
			key := uuid.New()
			_, err := matcher.SelectMatch(t.Context(), f.a, f.admin.id, key, choice)
			curationCRequireProblem(t, err, http.StatusConflict, tc.code)
			f.assertCount(t, 0, `SELECT count(*) FROM operations WHERE idempotency_key=$1`, key)
			f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, f.a)
			rows, err := f.writer.ReleaseMatchResults(t.Context(), f.a)
			requireCurationOK(t, err)
			if len(rows) != 1 || rows[0].Status != "conflict" || rows[0].SnapshotID != choice.SnapshotID {
				t.Fatalf("落库应保留原冲突快照：%+v", rows)
			}
		})
	}
}

func TestPostgreSQLJudgeAdoptNameUsesCurrentBasis(t *testing.T) {
	f, matcher, choice, _ := selectionFixture(t)
	ctx := t.Context()
	selection, err := matcher.SelectMatch(ctx, f.a, f.admin.id, uuid.New(), choice)
	requireCurationOK(t, err)
	if selection.Status != "matched" {
		t.Fatalf("选择合成版后应标为已匹配：%+v", selection)
	}
	app := f.application
	member := &f.ordinary
	var detail struct {
		Names []catalog.MatchName `json:"musicbrainz_names"`
	}
	var in curation.AdoptName
	for _, actor := range []*integrationTestUser{member, &f.admin} {
		getJSON(t, app, actor, "/api/v1/releases/"+f.a, &detail)
		found := false
		for _, name := range detail.Names {
			if name.Field == "title" && name.TargetType == "track" && name.MusicBrainzValue == "合成外部曲名" {
				found = true
				if name.CanAdopt != (actor == &f.admin) {
					t.Fatalf("只有管理员可采用 MusicBrainz 写法：%+v", name)
				}
				in = curation.AdoptName{TargetID: name.TargetID, Field: name.Field, Basis: name.BasisOperationID, Revision: name.Revision}
			}
		}
		if !found {
			t.Fatalf("已登录用户应读到 MusicBrainz 写法：%+v", detail.Names)
		}
	}
	path := "/api/v1/admin/curation/releases/" + f.a + "/adopt-name"
	post := func(actor *integrationTestUser, key string, input curation.AdoptName, status int) map[string]any {
		t.Helper()
		r := app.request(t, "POST", path, input, actor, idempotencyHeader(key))
		if r.Code != status {
			t.Fatalf("POST %s HTTP %d，期望 %d：%s", path, r.Code, status, r.Body)
		}
		out := map[string]any{}
		requireCurationOK(t, json.Unmarshal(r.Body.Bytes(), &out))
		return out
	}
	post(member, uuid.New(), in, http.StatusForbidden)
	stale := in
	stale.Basis = uuid.New()
	problem := post(&f.admin, uuid.New(), stale, http.StatusConflict)
	if e, ok := problem["error"].(map[string]any); !ok || e["code"] != "item_stale" {
		t.Fatalf("旧依据应返回 item_stale：%+v", problem)
	}

	key := uuid.New()
	first := post(&f.admin, key, in, http.StatusOK)
	replay := post(&f.admin, key, in, http.StatusOK)
	if first["effective_value"] != "合成外部曲名" || first["operation_id"] == nil || replay["operation_id"] != first["operation_id"] {
		t.Fatalf("采用或同键回放不一致：首次=%+v，回放=%+v", first, replay)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE idempotency_key=$1`, key)
}

func TestPostgreSQLJudgeBatchCommitsEachValidReply(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	a := judgeBatchItem(t, f, f.a)
	b := judgeBatchItem(t, f, f.b)
	c := curationRulePending(t, f, "定不了")
	d := curationRulePending(t, f, "格式错误")
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates='[]' WHERE id=$1`, b.ID)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 1})
	first := f.next(t, false)
	requireCurationRuleItems(t, first, a.ID, b.ID, c.ID, d.ID)
	prompt := curationRulePrompt(t, first)
	offers, found, err := metricsPendingItems(prompt)
	requireCurationOK(t, err)
	if !found || !strings.Contains(prompt, f.remote.ID) || len(offers) != 4 {
		t.Fatalf("实际模型请求缺少本批候选或任务：%s", prompt)
	}
	group := curation.JudgeGroup{Name: curation.JudgeName{Release: "合成原文"}}
	send := func(q schedulerRequest, rows []any) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"items": rows})
		requireCurationOK(t, err)
		q.reply <- q.frame(t, map[string]string{"content": string(raw)}, "stop")
	}
	send(first, []any{
		curation.JudgeAnswer{ItemID: a.ID, Decision: "judge", SelectedID: f.remote.ID, Groups: []curation.JudgeGroup{group}},
		curation.JudgeAnswer{ItemID: b.ID, Decision: "judge", Groups: []curation.JudgeGroup{group}},
		curation.JudgeAnswer{ItemID: c.ID, Decision: "undetermined", Reason: "当前证据无法定案"},
		map[string]any{"item_id": d.ID, "decision": "judge", "groups": "无效形态"},
	})
	modelWaitResolution(t, f, a.ID, "judge_submitted")
	modelWaitResolution(t, f, b.ID, "judge_submitted")
	modelWaitResolution(t, f, c.ID, "undetermined")
	for _, id := range []string{a.ID, b.ID} {
		var saved struct{ Judge curation.JudgeAnswer }
		requireCurationOK(t, json.Unmarshal(f.item(t, id).Evidence, &saved))
		wantSelected := ""
		if id == a.ID {
			wantSelected = f.remote.ID
		}
		if saved.Judge.SelectedID != wantSelected || len(saved.Judge.Groups) != 1 || saved.Judge.Groups[0] != group {
			t.Fatalf("选定候选与查证组应逐项持久化：%+v", saved.Judge)
		}
	}
	schedulerWait(t, "只对格式错误的行记录空答", func() bool { return f.item(t, d.ID).EmptyCount == 1 })
	f.clock.now.Add(int64(5 * time.Second))
	retry := f.next(t, false)
	requireCurationRuleItems(t, retry, d.ID)
	retry.undetermined(t, d.ID, "没有可用线索")
	modelWaitResolution(t, f, d.ID, "undetermined")
	requireCurationRuleRequestCount(t, f, 2)
}

func TestPostgreSQLJudgeClaimCannotWriteRetaggedAudiobook(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := modelRouteFixture(t, f, f.pending(t, f.a), "")
	f.enable(t)
	q := f.next(t, false)
	claimed := f.item(t, item.ID)
	mustExec(t, f.application, `UPDATE tracks SET scan_tags='{"audiobook":true}' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.a)

	group := curation.JudgeGroup{Name: curation.JudgeName{Release: "合成有声节目"}}
	answer := curation.JudgeAnswer{ItemID: item.ID, Decision: "judge", Groups: []curation.JudgeGroup{group}}
	err := f.writer.SubmitJudge(t.Context(), claimed, answer)
	curationCRequireProblem(t, err, http.StatusConflict, "item_stale")
	raw, err := json.Marshal(map[string]any{"items": []curation.JudgeAnswer{answer}})
	requireCurationOK(t, err)
	q.reply <- q.frame(t, map[string]string{"content": string(raw)}, "stop")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND result ? 'judge'`, f.a)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, f.a)
	queued, err := f.writer.SelectionItems(t.Context(), f.clock.options().Now(), 10, nil)
	requireCurationOK(t, err)
	if len(queued) != 0 {
		t.Fatalf("已重扫为有声书时不得继续领取：%+v", queued)
	}
}

func judgeBatchItem(t *testing.T, f *schedulerFixture, release string) curation.PendingItem {
	t.Helper()
	item := modelRouteFixture(t, f, f.pending(t, release), "")
	mustExec(t, f.application, `UPDATE curation_work_states SET file_digest=curation_release_file_digest(target_id),match_rule_version=$2
		WHERE subject='release' AND target_id=$1`, release, program.MatchRuleVersion)
	return item
}

func TestPostgreSQLJudgeDifferentChoicesOnlyOneCommits(t *testing.T) {
	f, matcher, choice, checked := selectionFixture(t)
	ctx := t.Context()
	remote := f.source.releases[choice.ExternalID]
	other := remote
	other.ID = uuid.New()
	f.source.releases[other.ID] = other
	candidates, err := json.Marshal([]program.Candidate{
		{Source: choice.Source, ExternalID: remote.ID, Method: "tag_id", Entity: "release", Title: remote.Title},
		{Source: choice.Source, ExternalID: other.ID, Method: "tag_id", Entity: "release", Title: other.Title},
	})
	requireCurationOK(t, err)
	// 两个候选具备相同结构，移除将选择限制到第一版的文件编号。
	mustExec(t, f.application, `DELETE FROM track_identifiers WHERE kind='mb_release' AND track_id IN
	 (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1)`, f.a)
	requireCurationOK(t, curation.SaveReleaseMatchResult(ctx, f.application.database, f.a, curation.ReleaseMatchResult{
		Source: choice.Source, Status: "conflict", Reason: "ambiguous_editions", CheckedAt: &checked, SnapshotID: choice.SnapshotID, Candidates: candidates,
	}))
	type response struct {
		curation.ReleaseMatchResult
		err error
	}
	out := make(chan response, 2)
	start := make(chan struct{})
	for _, candidate := range []string{remote.ID, other.ID} {
		go func() {
			<-start
			input := choice
			input.ExternalID = candidate
			result, err := matcher.SelectMatch(ctx, f.a, f.admin.id, uuid.New(), input)
			out <- response{result, err}
		}()
	}
	close(start)
	a, b := <-out, <-out
	if a.err != nil {
		a, b = b, a
	}
	requireCurationOK(t, a.err)
	curationCRequireProblem(t, b.err, http.StatusConflict, "item_stale")
	if a.Status != "matched" || a.Selection == nil || len(a.Selection.OperationIDs) == 0 {
		t.Fatalf("先提交的一版应保存结果和操作：%+v", a)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, f.a, a.Selection.ExternalID)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='external_link'`, f.a)
}

func TestPostgreSQLJudgeSameArtistLinkAndRevert(t *testing.T) {
	f := newArtistProgram(t)
	track, release := f.tagged(t)
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, track)
	release.Artists = []sources.Credit{{Name: "不同写法的合成乐团", ArtistID: uuid.New()}}
	f.source.releases[release.ID] = release
	publishSearch(t, f.application)
	artist := artistIDByName(t, f, "Artist A")
	f.run(t)
	f.assertRelease(t, f.a, "matched", "tag_id")
	ctx := t.Context()
	requireCurationOK(t, f.writer.MaterializeVerificationItems(ctx))
	itemID := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind='verify_artist_credit'`, f.a)
	pending, err := f.writer.PendingItem(ctx, itemID)
	requireCurationOK(t, err)
	item := modelClaim(t, f.curationFixture, modelConversation(t, f.curationFixture), pending)
	plan, err := f.writer.VerificationPlan(ctx, item, curation.VerificationInput{ItemID: item.ID, Verdict: "same", Reason: "同一署名位置的合成异名"}, nil)
	requireCurationOK(t, err)
	op, err := f.writer.ApplyVerification(ctx, item, plan, f.batch(t, "model"), uuid.New(), "synthetic")
	requireCurationOK(t, err)

	path := "/api/v1/artists/" + artist
	read := func() catalog.ArtistLinks {
		t.Helper()
		var detail struct{ Links catalog.ArtistLinks }
		getJSON(t, f.application, &f.ordinary, path, &detail)
		return detail.Links
	}
	links := read()
	if !links.JudgedByModel || links.MusicBrainz == nil || *links.MusicBrainz != release.Artists[0].ArtistID || links.OperationID == nil || *links.OperationID != op {
		t.Fatalf("艺人对应应带同一人的模型操作依据：%+v，操作 %s", links, op)
	}
	f.undo(t, op)
	links = read()
	if links.JudgedByModel || links.MusicBrainz != nil {
		t.Fatalf("撤销艺人对应后不应显示模型来源：%+v", links)
	}
}

func TestPostgreSQLJudgeNameKeepUncertainAndLateCorrection(t *testing.T) {
	for _, verdict := range []string{"keep", "undetermined", "typo"} {
		t.Run(verdict, func(t *testing.T) {
			const before, after = "合成正试专辑", "合成正式专辑"
			f := newSchedulerFixture(t)
			app := f.application
			release, tracks := b3ReleaseTracks(t, app, f.rootID, before, "合成正式艺人", 2)
			mustExec(t, app, `UPDATE tracks SET duration_seconds=CASE position WHEN 1 THEN 12 ELSE 16 END
				WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
			e, err := f.writer.LoadCurationEvidence(t.Context(), []string{release})
			requireCurationOK(t, err)
			requireCurationOK(t, curation.SaveWorkState(t.Context(), app.database, curation.WorkState{
				Subject: "release", TargetID: release, Status: "matched", RuleVersion: program.MatchRuleVersion, MatchRuleVersion: program.MatchRuleVersion,
				InputDigest: program.ReleaseDigest(e[release]), IdentityDigest: program.IdentityDigest(e[release]), FileDigest: e[release].FileDigest,
			}))
			f.enable(t)
			link := curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(release, "",
				curationCLink("release", release, f.remote.ID),
				curationCLink("track", tracks[0], f.remote.Recordings[0]),
				curationCLink("track", tracks[1], f.remote.Recordings[1]),
			))
			itemID := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
				ReleaseID: release, Field: "title", Before: before, After: after, Structure: true,
				ExternalID: f.remote.ID, ExternalValue: after, OperationID: link.OperationID,
			})
			f.configure(t, &f.admin)
			req := f.next(t, false)
			requireCurationRuleItems(t, req, itemID)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='matched'`, release)
			want := before
			if verdict == "typo" {
				want = "管理员正在使用的标题"
				f.correct(t, release, 1, map[string]any{"title": setChange(want)})
			}
			replyAndWaitJudgeVerification(t, f, req, curation.VerificationInput{
				ItemID: itemID, Verdict: verdict, Reason: "合成名字核对决定",
			})
			if verdict != "typo" {
				modelWaitResolution(t, f, itemID, "undetermined")
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND title=$2`, release, want)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'`, release)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, itemID)
		})
	}
}

func TestPostgreSQLJudgeSameArtistMergeAndRevert(t *testing.T) {
	f := newArtistMergeFixture(t)
	f.seedEvidence(t)
	ctx := t.Context()
	e, err := f.writer.LoadCurationEvidence(ctx, []string{f.a})
	requireCurationOK(t, err)
	tx, err := f.application.database.BeginTx(ctx, nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	v := curation.VerificationCandidate{ReleaseID: f.a, Before: "Artist A", After: "Artist Alias", ArtistA: f.primary, ArtistB: f.alias, ExternalID: f.mbid}
	requireCurationOK(t, curation.QueueVerification(ctx, tx, "verify_artist_merge", program.IdentityDigest(e[f.a]), v))
	requireCurationOK(t, tx.Commit())
	id := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind='verify_artist_merge'`, f.a)
	pending, err := f.writer.PendingItem(ctx, id)
	requireCurationOK(t, err)
	item := modelClaim(t, f.curationFixture, modelConversation(t, f.curationFixture), pending)
	plan, err := f.writer.VerificationPlan(ctx, item, curation.VerificationInput{ItemID: id, Verdict: "same", Reason: "两种署名属于同一合成艺人"}, nil)
	requireCurationOK(t, err)
	op, err := f.writer.ApplyVerification(ctx, item, plan, f.batch(t, "model"), uuid.New(), "synthetic")
	requireCurationOK(t, err)

	target := programString(t, f.application, `SELECT redirect_artist_id FROM artist_overrides WHERE redirect_operation_id=$1`, op)
	path := "/api/v1/artists/" + target + "/merged-sources"
	var rows governance.ArtistMergedSources
	getJSON(t, f.application, &f.admin, path, &rows)
	if len(rows.Items) != 1 || !rows.Items[0].JudgedByModel || rows.Items[0].OperationID != op {
		t.Fatalf("合并来源应带同一人的模型依据：%+v，操作 %s", rows, op)
	}
	f.undo(t, op)
	getJSON(t, f.application, &f.admin, path, &rows)
	if len(rows.Items) != 0 {
		t.Fatalf("撤销后应清除该合并来源：%+v", rows)
	}
}

func TestPostgreSQLJudgeInvalidResponsesStopAfterThree(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.sourceEnabled(t, "lrclib", false)
	f.sourceEnabled(t, "coverartarchive", false)
	ctx := t.Context()
	db := f.application.database
	remote := uuid.New()
	var lookups, other atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release/"+remote {
			lookups.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"缺少曲表"}`))
			return
		}
		other.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)
	client := curationMusicBrainz{musicbrainz.New(sources.Options{
		BaseURL: upstream.URL, NewClient: b3HTTPClient(upstream), Limiter: sources.NewLimiter(0, nil, nil),
	})}
	f.gate = curation.NewSources(db, curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: client, Recordings: client}).WithScanCheck(f.scanCheck)
	f.sourceEnabled(t, "musicbrainz", true)

	local, err := f.writer.LoadCurationEvidence(ctx, []string{f.a})
	requireCurationOK(t, err)
	e := local[f.a]
	checked := f.clock.options().Now().Add(-time.Hour)
	requireCurationOK(t, curation.SaveReleaseMatchResult(ctx, db, f.a, curation.ReleaseMatchResult{
		Source: "musicbrainz", Status: "unmatched", CheckedAt: &checked,
	}))
	state := curation.WorkState{Subject: "release", TargetID: f.a, Status: "unmatched", RuleVersion: program.MatchRuleVersion, MatchRuleVersion: program.MatchRuleVersion,
		LocalRuleVersion: program.LocalRuleVersion, InputDigest: program.ReleaseDigest(e), IdentityDigest: program.IdentityDigest(e), GapDigest: program.GapDigest(e), FileDigest: e.FileDigest,
		Generation: 1}
	requireCurationOK(t, curation.SaveWorkState(ctx, db, state))
	raw, err := json.Marshal([]program.Candidate{{ExternalID: remote, Method: "tag_id"}})
	requireCurationOK(t, err)
	requireCurationOK(t, curation.UpsertPendingItem(ctx, db, curation.PendingItem{
		TargetID: f.a, Kind: "release_match", RuleVersion: program.StoredRuleVersion, MatchRuleVersion: program.MatchRuleVersion,
		InputDigest: state.InputDigest, IdentityDigest: state.IdentityDigest, Evidence: []byte("{}"), Candidates: raw,
	}))
	item, err := f.writer.PendingItem(ctx, programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind='release_match'`, f.a))
	requireCurationOK(t, err)
	item = modelClaim(t, f.curationFixture, modelConversation(t, f.curationFixture), item)
	requireCurationOK(t, f.writer.SubmitJudge(ctx, item, curation.JudgeAnswer{ItemID: item.ID, Decision: "judge", SelectedID: remote}))

	f.run(t)
	for i, delay := range []time.Duration{5 * time.Second, 10 * time.Second} {
		got := f.stateFor(t, "release", f.a)
		if got.Status != "try_later" || got.RetryCount != i+1 || lookups.Load() != int32(i+1) ||
			got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(f.clock.options().Now().Add(delay)) {
			t.Fatalf("第 %d 次无效响应，实际状态 %+v、详情请求 %d，期望等待 %v", i+1, got, lookups.Load(), delay)
		}
		f.clock.advance(t, delay)
	}
	stopped := f.stateFor(t, "release", f.a)
	if stopped.Status != "query_error" || stopped.RetryCount != 3 || stopped.NextAttemptAt != nil || stopped.Generation != state.Generation {
		t.Fatalf("第三次响应无效应在原代次停止：%+v", stopped)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND subject='release'
		AND result->'retry'->>'step'='model_hints' AND result->'retry'->>'reason'='invalid_response'
		AND result->'judge'->>'used'='3' AND result->'judge'->>'next'='-1'`, f.a)
	f.clock.advance(t, 90*24*time.Hour)
	if lookups.Load() != 3 || other.Load() != 0 {
		t.Fatalf("三次无效详情应耗三发并停止，详情 %d 次、其它 %d 次", lookups.Load(), other.Load())
	}
	f.assertCount(t, 1, `SELECT count(*) FROM curation_release_match_results WHERE release_id=$1 AND source='musicbrainz' AND status='unmatched' AND checked_at=$2`, f.a, checked)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match' AND resolution='judge_submitted'`, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, f.a)
}

func queueJudgeArtistPair(t *testing.T, f *schedulerFixture, target, digest, left, right, leftName, rightName, external string) string {
	t.Helper()
	tx, err := f.application.database.BeginTx(t.Context(), nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.QueueVerification(t.Context(), tx, "verify_artist_merge", digest, curation.VerificationCandidate{
		ReleaseID: target, Before: leftName, After: rightName, ArtistA: left, ArtistB: right, ExternalID: external,
	}))
	requireCurationOK(t, tx.Commit())
	return programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND input_digest=$2 AND kind='verify_artist_merge'`, target, digest)
}

func replyAndWaitJudgeVerification(t *testing.T, f *schedulerFixture, request schedulerRequest, input curation.VerificationInput) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"items": []curation.VerificationInput{input}})
	requireCurationOK(t, err)
	conversationID := f.item(t, input.ItemID).ConversationID
	request.reply <- request.frame(t, map[string]string{"content": string(raw)}, "stop")
	schedulerWait(t, "核对工具成功记录答复 "+input.ItemID, func() bool {
		return countRows(t, f.application, `SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id
			WHERE m.conversation_id=$1 AND c.tool_name='submit_verification' AND c.status='succeeded'`, conversationID) == 1
	})
}

func TestPostgreSQLJudgeArtistPairConcurrentSingleDecision(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	left, right, otherLeft, otherRight, external := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mustExec(t, f.application, `INSERT INTO artists(id,initial_name) VALUES($1,'合成甲团'),($2,'合成乙团'),($3,'合成甲团'),($4,'合成乙团')`, left, right, otherLeft, otherRight)
	mustExec(t, f.application, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES(gen_random_uuid(),'musicbrainz','artist',$1,$3,'artist_credit','certain'),
		      (gen_random_uuid(),'musicbrainz','artist',$2,$3,'artist_credit','certain')`, left, otherLeft, external)
	first := queueJudgeArtistPair(t, f, f.a, "a", left, right, "合成甲团", "合成乙团", external)
	second := queueJudgeArtistPair(t, f, f.b, "b", otherRight, otherLeft, "合成乙团", "合成甲团", external)
	otherRelease, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "并行的独立发行", artist: "独立合成乐队", tracks: []string{"present"},
	})
	independent := f.pending(t, otherRelease)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "model_concurrency": 2})

	var active, parallel schedulerRequest
	for range 2 {
		req := f.next(t, false)
		prompt := curationRulePrompt(t, req)
		if strings.Contains(prompt, independent.ID) {
			requireCurationRuleItems(t, req, independent.ID)
			parallel = req
			continue
		}
		requireCurationRuleItems(t, req, first)
		active = req
	}
	if active.reply == nil || parallel.reply == nil || f.item(t, second).Status != "waiting_model" {
		t.Fatalf("并发会话中只有同一无序名字对的首项可领取，反向项为 %+v", f.item(t, second))
	}
	parallel.undetermined(t, independent.ID, "独立发行的证据不足")
	replyAndWaitJudgeVerification(t, f, active, curation.VerificationInput{ItemID: first, Verdict: "different", Reason: "合成身份核对"})
	modelWaitResolution(t, f, first, "different")
	modelWaitResolution(t, f, second, "different")
	modelWaitResolution(t, f, independent.ID, "undetermined")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_artist_negative_pairs WHERE model='test-main' AND reason='合成身份核对'`)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_tool_calls WHERE tool_name='submit_verification' AND status='succeeded'`)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)
	requireCurationRuleRequestCount(t, f, 2)
}

func TestPostgreSQLJudgeUncertainArtistPairCanAskOnNewEvidence(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	left, right, external := uuid.New(), uuid.New(), uuid.New()
	mustExec(t, f.application, `INSERT INTO artists(id,initial_name) VALUES($1,'合成甲团'),($2,'合成乙团')`, left, right)
	mustExec(t, f.application, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES(gen_random_uuid(),'musicbrainz','artist',$1,$2,'artist_credit','certain')`, left, external)

	first := queueJudgeArtistPair(t, f, f.a, "原证据", left, right, "合成甲团", "合成乙团", external)
	f.enable(t)
	initial := f.next(t, false)
	requireCurationRuleItems(t, initial, first)
	replyAndWaitJudgeVerification(t, f, initial, curation.VerificationInput{ItemID: first, Verdict: "undetermined", Reason: "合成身份核对"})
	modelWaitResolution(t, f, first, "undetermined")
	same := queueJudgeArtistPair(t, f, f.a, "原证据", left, right, "合成甲团", "合成乙团", external)
	if same != first || f.item(t, same).Status != "resolved" {
		t.Fatalf("相同输入应沿用已结案项，实际 %+v", f.item(t, same))
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_artist_negative_pairs`)

	second := queueJudgeArtistPair(t, f, f.a, "新证据", left, right, "合成甲团", "合成乙团", external)
	if first == second {
		t.Fatal("新证据应产生可独立领取的待办")
	}
	retry := f.next(t, false)
	requireCurationRuleItems(t, retry, second)
	replyAndWaitJudgeVerification(t, f, retry, curation.VerificationInput{ItemID: second, Verdict: "undetermined", Reason: "合成身份核对"})
	modelWaitResolution(t, f, second, "undetermined")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_artist_negative_pairs`)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='verify_artist_merge' AND resolution='undetermined'`, f.a)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM steward_tool_calls WHERE tool_name='submit_verification' AND status='succeeded'`)
	requireCurationRuleRequestCount(t, f, 2)
}

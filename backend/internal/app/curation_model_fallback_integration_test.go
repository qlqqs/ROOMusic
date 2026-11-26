package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type testLookupGate struct{ ready atomic.Bool }

func (g *testLookupGate) LookupTierReady(context.Context, string, time.Time) (bool, error) {
	return g.ready.Load(), nil
}
func (*testLookupGate) LookupTierContext(ctx context.Context) context.Context { return ctx }

func modelRouteFixture(t *testing.T, f *schedulerFixture, item curation.PendingItem, route string) curation.PendingItem {
	t.Helper()
	app := f.application
	evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{item.TargetID}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	digest := curation.ReleaseIdentityDigest(evidence[item.TargetID])
	requireCurationOK(t, curation.SaveWorkState(t.Context(), app.database, curation.WorkState{
		Subject: "release", TargetID: item.TargetID, Status: "conflict", RuleVersion: program.StoredRuleVersion,
		InputDigest: item.InputDigest, IdentityDigest: digest, Result: []byte(`{}`),
	}))
	mustExec(t, app, `UPDATE curation_pending_items SET identity_digest=$2,route=NULLIF($3,''),status=CASE WHEN $3='' THEN 'waiting_model' ELSE 'waiting_route' END WHERE id=$1`, item.ID, digest, route)
	return f.item(t, item.ID)
}

func modelWaitResolution(t *testing.T, f *schedulerFixture, id, want string) {
	t.Helper()
	schedulerWait(t, "模型项结案", func() bool { return f.item(t, id).Status == "resolved" })
	got := f.item(t, id)
	if got.Resolution == nil || *got.Resolution != want {
		t.Fatalf("期望结论 %s，实际 %+v", want, got)
	}
}

func TestPostgreSQL整理迁移旧待办为可采纳建议(t *testing.T) {
	f := newSchedulerFixture(t)
	ctx := t.Context()
	conversation := modelConversation(t, f.curationFixture)
	item := modelClaim(t, f.curationFixture, conversation, f.pending(t, f.a))
	input, err := json.Marshal(schedulerCorrection(f.a, 1, "edition", setChange("旧建议的版本")))
	requireCurationOK(t, err)
	id, err := f.writer.CreateException(ctx, curation.ModelException{
		PendingItemID: item.ID, PlanItemID: uuid.New(), ConversationID: &conversation, Tool: "correct_release", Provider: "test-main",
		Input: input, Preview: []byte(`{"changes":[{"field":"edition","before":null,"after":"旧建议的版本"}]}`),
		Review: curation.ExceptionReview{ID: uuid.New(), Model: "test-review", Source: "model", Decision: "needs_human_confirmation", Reasons: []string{"旧建议"}},
	})
	requireCurationOK(t, err)
	runMigration := func(file string) {
		t.Helper()
		raw, err := os.ReadFile("../../migrations/" + file)
		requireCurationOK(t, err)
		mustExec(t, f.application, string(raw))
	}
	runMigration("0091_curation_model_fallback.down.sql")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='waiting_human'`, item.ID)
	runMigration("0091_curation_model_fallback.sql")
	modelWaitResolution(t, f, item.ID, "suggestion_recorded")
	suggestion, err := f.writer.Exception(ctx, id)
	requireCurationOK(t, err)
	if suggestion.Status != "open" || !suggestion.CanAccept || suggestion.ReasonCode != "review_uncertain" || suggestion.Review.ID == "" {
		t.Fatalf("迁移后的建议 %+v", suggestion)
	}
	if p := f.progress(t); p.Items.WaitingHuman != 0 {
		t.Fatalf("迁移后仍有人工待办 %+v", p.Items)
	}
	readExceptionDecision(t, f.decide(t, &f.admin, id, "accept"), "accepted", f.admin.id)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND edition='旧建议的版本'`, f.a)
}

func TestPostgreSQL整理第二档查到候选外编号直接写入(t *testing.T) {
	correct, suggested := uuid.New(), uuid.New()
	lookup := &testLookupGate{}
	var tracks []map[string]any
	for i := 1; i <= 5; i++ {
		title, id := fmt.Sprintf("曲目 %d", i), uuid.New()
		tracks = append(tracks, map[string]any{"id": uuid.New(), "position": i, "number": fmt.Sprint(i), "title": title, "length": 100000,
			"recording": map[string]any{"id": id, "title": title}})
	}
	release := func(id string) map[string]any {
		title, list := "原文专辑", tracks
		if id == suggested {
			title = "同名现场版"
			list = make([]map[string]any, len(tracks))
			for i := range tracks {
				list[i] = map[string]any{"id": uuid.New(), "position": i + 1, "number": fmt.Sprint(i + 1), "title": fmt.Sprintf("现场曲目 %d", i+1), "length": 145000,
					"recording": map[string]any{"id": uuid.New(), "title": fmt.Sprintf("现场曲目 %d", i+1)}}
			}
		}
		return map[string]any{"id": id, "title": title, "artist-credit": []any{map[string]any{"name": "原文艺人"}},
			"media": []any{map[string]any{"position": 1, "track-count": 5, "tracks": list}}}
	}
	source := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimSuffix(r.URL.Path, "/") {
		case "/mb/release/" + correct, "/mb/release/" + suggested:
			_ = json.NewEncoder(w).Encode(release(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]))
		case "/mb/release":
			if q := r.URL.Query().Get("query"); q != "原文专辑" {
				t.Errorf("查询条件 %q", q)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"releases": []any{release(correct)}, "count": 1})
		default:
			http.NotFound(w, r)
		}
	})
	f := newSchedulerFixtureWithSources(t, lookup, source)
	target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "原文专辑", artist: "原文艺人", tracks: []string{"present", "present", "present", "present", "present"},
	})
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100,title='曲目 '||position WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, target)
	item := modelRouteFixture(t, f, f.pending(t, target), "")
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, item.ID, suggested)
	f.configure(t, &f.admin)
	f.enable(t)
	first := f.next(t, false)
	var prompt string
	requireCurationOK(t, json.Unmarshal(first.Messages[len(first.Messages)-1].Content, &prompt))
	if !strings.Contains(prompt, suggested) || strings.Contains(prompt, correct) || !strings.Contains(prompt, "现场曲目 5") {
		t.Fatalf("第一档缺少完整候选或提前看到正确编号：%s", prompt)
	}
	first.selectCandidate(t, item.ID, "")
	schedulerWait(t, "第一档升级", func() bool { return f.item(t, item.ID).Route == "lookup" })
	select {
	case <-f.requests:
		t.Fatal("第二档门槛关闭时发出了模型请求")
	case <-time.After(120 * time.Millisecond):
	}
	lookup.ready.Store(true)
	f.next(t, false).tool(t, "search_music_metadata", map[string]string{"entity": "release", "query": "原文专辑"})
	read := f.next(t, false)
	if !strings.Contains(read.lastToolResult(t), correct) {
		t.Fatalf("第二档未查到候选外编号：%s", read.lastToolResult(t))
	}
	read.tool(t, "get_music_metadata", map[string]any{"entity": "release", "mbid": correct, "include": []string{"recordings"}})
	submit := f.next(t, false)
	if !strings.Contains(submit.lastToolResult(t), "曲目 5") {
		t.Fatalf("未读取曲目明细：%s", submit.lastToolResult(t))
	}
	submit.tool(t, "resolve_release_match", map[string]any{"item_id": item.ID, "external_id": correct, "basis": map[string]string{"note": "五首曲目完全一致"}})
	modelWaitResolution(t, f, item.ID, "model_matched")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND external_id=$2 AND method='album_structure'`, target, correct)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
}

func TestPostgreSQL整理相同曲数的时长反证不能写入(t *testing.T) {
	for _, tc := range []struct{ name, decision, evidence string }{
		{"审查拒绝", "rejected", "difference_ms"},
		{"批准仍有反证", "approved", "difference_ms"},
		{"真实文字反证", "approved", "tag_text_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			external := b3NewRemoteRelease()
			source := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/mb/release/"+external.ID {
					http.NotFound(w, r)
					return
				}
				fmt.Fprintf(w, `{"id":%q,"title":"外部专辑","artist-credit":[{"name":"外部艺人"}],"media":[{"position":1,"track-count":2,"tracks":[{"position":1,"title":"异名甲","length":12000,"recording":{"id":%q}},{"position":2,"title":"异名乙","length":16000,"recording":{"id":%q}}]}]}`, external.ID, external.Recordings[0], external.Recordings[1])
			})
			f := newSchedulerFixtureWithSources(t, nil, source)
			release, _, _ := f.matchFixture(t)
			mustExec(t, f.application, `DELETE FROM track_identifiers WHERE track_id IN (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1)`, release)
			selected := external.ID
			if tc.evidence == "difference_ms" {
				selected = f.remote.ID
				mustExec(t, f.application, `UPDATE tracks SET duration_seconds=85 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
			}
			item := f.pending(t, release)
			if selected == external.ID {
				mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, item.ID, selected)
			}
			f.configure(t, &f.admin)
			f.enable(t)
			f.next(t, false).selectCandidate(t, item.ID, selected)
			review := f.next(t, true)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, release)
			if !strings.Contains(string(review.Messages[len(review.Messages)-1].Content), tc.evidence) {
				t.Fatalf("审查收到的依据缺少 %s：%s", tc.evidence, review.Messages[len(review.Messages)-1].Content)
			}
			review.review(t, tc.decision)
			modelWaitResolution(t, f, item.ID, "suggestion_recorded")
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE target_id=$1`, release)
			list, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
			requireCurationOK(t, err)
			if total != 1 || list[0].CanAccept || list[0].ReasonCode != "review_rejected" || (tc.decision == "approved" && list[0].Review.Source != "policy") {
				t.Fatalf("反证建议 %+v", list)
			}
			requireAPIError(t, f.decide(t, &f.admin, list[0].ID, "accept"), 409, "nothing_to_apply")
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, release)
		})
	}
}

func queueModelVerification(t *testing.T, f *schedulerFixture, kind string, v curation.VerificationCandidate) string {
	t.Helper()
	ctx := t.Context()
	db := f.application.database
	e, err := catalog.LoadCurationEvidence(ctx, db, []string{v.ReleaseID}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	tx, err := db.BeginTx(ctx, nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.QueueVerification(ctx, tx, kind, program.IdentityDigest(e[v.ReleaseID]), v))
	requireCurationOK(t, tx.Commit())
	return programString(t, f.application, `SELECT id FROM curation_pending_items WHERE kind=$1 AND target_id=$2`, kind, v.ReleaseID)
}

func configureModelWeb(t *testing.T, f *schedulerFixture) {
	t.Helper()
	f.update(t, "PUT", stewardBase+"/settings", map[string]any{"search_source": "searxng", "searxng_url": f.endpoint + "/searxng"})
}

func TestPostgreSQL整理无佐证主张先联网再留建议(t *testing.T) {
	search := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/searxng/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[]}`)
	})
	f := newSchedulerFixtureWithSources(t, nil, search)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	item := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
		ReleaseID: f.a, Field: "title", Before: "Album A", After: "另一个标题",
	})
	f.enable(t)
	submit := map[string]string{"item_id": item, "verdict": "typo", "reason": "推测标题有误"}
	f.next(t, false).tool(t, "submit_verification", submit)
	web := f.next(t, false)
	if got := f.item(t, item); got.Route != "web" {
		t.Fatalf("缺少佐证时未转联网：%+v", got)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item)
	web.tool(t, "web_search", map[string]string{"q": "Album A 另一个标题"})
	afterSearch := f.next(t, false)
	if result := afterSearch.lastToolResult(t); !strings.Contains(result, `"results":[]`) {
		t.Fatalf("联网求证结果 %s", result)
	}
	afterSearch.tool(t, "submit_verification", submit)
	modelWaitResolution(t, f, item, "suggestion_recorded")
	requireNoEvidenceSuggestion(t, f, item, "no_evidence")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
}

func requireNoEvidenceSuggestion(t *testing.T, f *schedulerFixture, item, reason string) {
	t.Helper()
	exceptions, total, err := f.writer.ListExceptions(t.Context(), "open", 1, 10)
	requireCurationOK(t, err)
	if total != 1 || exceptions[0].PendingItemID != item || exceptions[0].ReasonCode != reason ||
		len(exceptions[0].Reasons) == 0 || exceptions[0].Review.ID != "" || !exceptions[0].CanAccept {
		t.Fatalf("缺少未采用建议 %+v", exceptions)
	}
}

func TestPostgreSQL整理模型记忆的事实只留建议(t *testing.T) {
	gate := &testLookupGate{}
	gate.ready.Store(true)
	f := newSchedulerFixtureWithSources(t, gate, nil)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	item := modelRouteFixture(t, f, f.pending(t, f.a), "lookup")
	f.enable(t)
	f.next(t, false).tool(t, "shelve_pending_item", map[string]any{
		"item_id": item.ID, "reason": "模型记忆中的厂牌", "fact": map[string]string{"field": "label", "value": "合成厂牌"},
	})
	web := f.next(t, false)
	if got := f.item(t, item.ID); got.Route != "web" || !strings.Contains(string(got.Evidence), "合成厂牌") {
		t.Fatalf("事实主张未随路线送往联网：%+v", got)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
	web.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "联网缺少开放库佐证"})
	modelWaitResolution(t, f, item.ID, "unlisted")
	requireNoEvidenceSuggestion(t, f, item.ID, "unverified_fact")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
}

func TestPostgreSQL整理联网额度与单项额度(t *testing.T) {
	for _, tc := range []struct {
		name        string
		webLimit    int
		startRoute  string
		limitTokens int
		limitCalls  int
	}{
		{"联网日额用尽仍可离线选择", 20, "web", 13, 0},
		{"第二档单项超额后转联网", 0, "lookup", 150000, 0},
		{"联网单项超额后结束", 0, "web", 300000, 0},
		{"第二档超出工具次数后转联网", 0, "lookup", 13, 20},
		{"联网超出工具次数后结束", 0, "web", 13, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := &testLookupGate{}
			gate.ready.Store(true)
			f := newSchedulerFixtureWithSources(t, gate, nil)
			f.configure(t, &f.admin)
			configureModelWeb(t, f)
			first := modelRouteFixture(t, f, f.pending(t, f.a), tc.startRoute)
			if tc.limitCalls != 0 {
				mustExec(t, f.application, `UPDATE curation_pending_items SET item_tool_calls=$2 WHERE id=$1`, first.ID, tc.limitCalls)
			}
			if tc.webLimit > 0 {
				second := modelRouteFixture(t, f, f.pending(t, f.b), "web")
				f.update(t, "PUT", curationBase+"/settings", map[string]int{"web_daily_token_limit": tc.webLimit, "model_concurrency": 1})
				f.enable(t)
				q := f.next(t, false)
				// 联网按在库组数及创建顺序领取，先让第一项耗完额度。
				q.tool(t, "shelve_pending_item", map[string]string{"item_id": first.ID, "reason": "没有可信来源"})
				modelWaitResolution(t, f, first.ID, "unlisted")
				schedulerWait(t, "达到联网日额", func() bool { return f.progress(t).WebLimitReached })
				p := f.progress(t)
				if p.WebToday != p.Today || p.WebRetryAt == nil || p.State == "limit_reached" || f.item(t, second.ID).Status != "waiting_route" {
					t.Fatalf("联网子限额 %+v，下一项 %+v", p, f.item(t, second.ID))
				}
				release, _, offline := f.matchFixture(t)
				f.next(t, false).selectCandidate(t, offline.ID, f.remote.ID)
				f.next(t, true).review(t, "approved")
				modelWaitResolution(t, f, offline.ID, "model_matched")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1`, release)
				if f.item(t, second.ID).Status != "waiting_route" {
					t.Fatal("离线写入时仍领取了联网项")
				}
				return
			}
			f.enable(t)
			q := f.next(t, false)
			q.inputTokens = tc.limitTokens
			if tc.startRoute == "lookup" {
				q.tool(t, "search_music_metadata", map[string]string{"entity": "release", "query": "合成"})
				next := f.next(t, false)
				if p := f.item(t, first.ID); p.Route != "web" || p.ItemToolCalls != 0 || p.ItemTokens != 0 {
					t.Fatalf("第二档触限后未转到新档或未重置单项额度：%+v", p)
				}
				next.tool(t, "shelve_pending_item", map[string]string{"item_id": first.ID, "reason": "联网无结果"})
			} else {
				q.tool(t, "web_search", map[string]string{"q": "合成"})
			}
			modelWaitResolution(t, f, first.ID, "unlisted")
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states
 WHERE subject='release' AND target_id=$1 AND status='conflict' AND next_attempt_at IS NULL`, f.a)
		})
	}
}

func TestPostgreSQL整理专辑名字只纠正有佐证的错字(t *testing.T) {
	for _, tc := range []struct {
		name, local, remote, verdict string
	}{
		{"错字", "合成正试专辑", "合成正式专辑", "typo"},
		{"繁简", "合成正式專輯", "合成正式专辑", ""},
		{"大小写", "COAST LETTERS", "Coast Letters", ""},
		{"标点", "海岸：来信", "海岸 来信", "style"},
		{"外部特殊写法", "沿海来信", "沿haI来信", "style"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := b3NewRemoteRelease()
			source := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/mb/release/"+remote.ID {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":%q,"title":%q,"artist-credit":[{"name":"甲艺人"}],
					"media":[{"position":1,"track-count":2,"tracks":[
					{"id":"track-1","position":1,"title":"第一曲","length":12000,"recording":{"id":%q}},
					{"id":"track-2","position":2,"title":"第二曲","length":16000,"recording":{"id":%q}}]}]}`,
					remote.ID, tc.remote, remote.Recordings[0], remote.Recordings[1])
			})
			f := newSchedulerFixtureWithSources(t, nil, source)
			target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: tc.local, artist: "甲艺人", tracks: []string{"present", "present"}})
			mustExec(t, f.application, `UPDATE tracks SET duration_seconds=CASE position WHEN 1 THEN 12 ELSE 16 END,
				title=CASE position WHEN 1 THEN '第一曲' ELSE '第二曲' END WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, target)
			item := f.pending(t, target)
			mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, item.ID, remote.ID)
			f.configure(t, &f.admin)
			f.enable(t)
			f.next(t, false).selectCandidate(t, item.ID, remote.ID)
			f.next(t, true).review(t, "approved")
			modelWaitResolution(t, f, item.ID, "model_matched")
			if tc.verdict != "" {
				judgment := f.next(t, false)
				verify := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE kind='verify_name' AND target_id=$1 AND evidence->>'field'='title'`, target)
				judgment.tool(t, "submit_verification", map[string]string{"item_id": verify, "verdict": tc.verdict, "reason": "对照外部标题"})
				if tc.verdict == "typo" {
					f.next(t, true).review(t, "approved")
					schedulerWait(t, "有效标题和原始标题同时保留", func() bool {
						return countRows(t, f.application, `SELECT count(*) FROM release_effective e JOIN releases r ON r.id=e.id WHERE r.id=$1 AND r.title=$2 AND e.title=$3`, target, tc.local, tc.remote) == 1
					})
					var correction struct {
						Fields []struct {
							Field          string
							ScanValue      any `json:"scan_value"`
							EffectiveValue any `json:"effective_value"`
						}
					}
					getJSON(t, f.application, &f.admin, "/api/v1/releases/"+target+"/corrections", &correction)
					found := false
					for _, field := range correction.Fields {
						if field.Field == "title" && field.ScanValue == tc.local && field.EffectiveValue == tc.remote {
							found = true
						}
					}
					if !found {
						t.Fatalf("修正接口未同时显示原值和有效值：%+v", correction.Fields)
					}
					op := programString(t, f.application, `SELECT operation_id FROM release_auto_values WHERE release_id=$1 AND field='title' AND writer_level=3`, target)
					f.undo(t, op)
				} else {
					modelWaitResolution(t, f, verify, "suggestion_recorded")
					b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1 AND tool='observation' AND review_id IS NULL`, verify)
				}
			} else {
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='verify_name' AND evidence->>'field'='title'`, target)
			}
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND title=$2`, target, tc.local)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='title'`, target)
		})
	}
}

func TestPostgreSQL整理无佐证不合并艺人与发行(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	seed := artistSamples{t: t, app: f.application}
	seed.identity(1, "甲艺人")
	seed.identity(2, "乙艺人")
	artist := queueModelVerification(t, f, "verify_artist_merge", curation.VerificationCandidate{
		ReleaseID: f.a, Field: "artist", Before: "甲艺人", After: "乙艺人", ArtistA: seed.id(1), ArtistB: seed.id(2),
	})
	f.enable(t)
	f.next(t, false).tool(t, "submit_verification", map[string]string{"item_id": artist, "verdict": "same", "reason": "模型猜测是别名"})
	modelWaitResolution(t, f, artist, "suggestion_recorded")
	requireNoEvidenceSuggestion(t, f, artist, "no_evidence")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)

	item := modelPending(t, f.curationFixture, f.a, "release_merge", "假定的发行对应")
	raw, err := json.Marshal([]curation.MergeCandidate{{ReleaseID: f.b, Title: "Album B", DiscCount: 1, TrackCount: 1}})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, item.ID, raw)
	f.next(t, false).tool(t, "merge_releases", releaseMergeProposal(item.ID))
	schedulerWait(t, "无佐证的发行并入被拒绝", func() bool {
		return countRows(t, f.application, `SELECT count(*) FROM steward_tool_calls WHERE tool_name='merge_releases' AND status='failed'`) > 0
	})
	if got := f.modelToolResults(t); !strings.Contains(got, "no_strong_evidence") && !strings.Contains(got, "item_stale") && !strings.Contains(got, "rejected_by_review") {
		t.Fatalf("无佐证的发行并入返回 %s", got)
	}
	f.pause(t)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='release_auto_merge'`)
}

func TestPostgreSQL整理同曲并列署名否决艺人合并(t *testing.T) {
	f := newArtistMergeFixture(t)
	f.seedEvidence(t)
	// 在一首曲目同一层保留两个不同位置，外部同一 MBID 也不能授权合并。
	mustExec(t, f.application, `INSERT INTO artist_credits(id,track_id,role,name_id,position,layer)
		SELECT gen_random_uuid(),$1,'track_artist',n.id,2,'scan' FROM artist_names n JOIN artist_name_keys k ON k.id=n.key_id
		WHERE k.initial_artist_id=$2 AND n.spelling='Artist A'`, f.track, f.primary)
	f.assertCount(t, 2, `SELECT count(DISTINCT c.position) FROM artist_credits c JOIN artist_names n ON n.id=c.name_id
		JOIN artist_name_keys k ON k.id=n.key_id WHERE c.track_id=$1 AND c.role='track_artist' AND c.layer='scan'
		AND k.initial_artist_id IN ($2,$3)`, f.track, f.primary, f.alias)
	_, err := f.writer.MergeArtists(t.Context(), f.mergeRequest(t, f.primary, f.alias))
	curationCRequireProblem(t, err, 409, "no_strong_evidence")
	if !strings.Contains(err.Error(), "并列署名") {
		t.Fatalf("合并拒绝原因 %v", err)
	}
	f.assertCount(t, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)
}

func TestPostgreSQL整理同位库内署名经审查可合并(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	seed := artistSamples{t: t, app: f.application}
	seed.identity(1, "甲乐团")
	seed.identity(2, "旧称乐团")
	release, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "合成专辑", artist: "甲乐团", tracks: []string{"present", "present"},
	})
	mustExec(t, f.application, `UPDATE tracks SET artist=CASE position WHEN 1 THEN '甲乐团' ELSE '旧称乐团' END
		WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, release)
	publishSearch(t, f.application)
	item := queueModelVerification(t, f, "verify_artist_merge", curation.VerificationCandidate{
		ReleaseID: release, Field: "artist_merge", Before: "甲乐团", After: "旧称乐团", ArtistA: seed.id(1), ArtistB: seed.id(2),
	})
	f.enable(t)
	f.next(t, false).tool(t, "submit_verification", map[string]string{
		"item_id": item, "verdict": "same", "reason": "同一发行相邻曲目的同位署名",
	})
	review := f.next(t, true)
	if !strings.Contains(string(review.Messages[len(review.Messages)-1].Content), "track_credit_position") {
		t.Fatalf("审查缺少库内署名佐证：%s", review.Messages[len(review.Messages)-1].Content)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)
	review.review(t, "approved")
	schedulerWait(t, "合并艺人并记录来源", func() bool {
		return countRows(t, f.application, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'
			AND origin->>'provider'='library_evidence' AND origin->>'review_decision'='approved'`) == 1
	})
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist'`)
}

func TestPostgreSQL整理同位外部署名经审查建立对应(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	publishSearch(t, f.application)
	ctx := t.Context()
	db := f.application.database
	credits, err := catalog.LoadCurationArtistCredits(ctx, db, f.a)
	requireCurationOK(t, err)
	if len(credits.ReleaseArtists) != 1 {
		t.Fatalf("发行署名夹具 %+v", credits.ReleaseArtists)
	}
	local := credits.ReleaseArtists[0]
	externalArtist, externalRelease := uuid.New(), uuid.New()
	f.enable(t)
	curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.a, "", curationCLink("release", f.a, externalRelease)))
	evidence, err := catalog.LoadCurationEvidence(ctx, db, []string{f.a}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	digest := program.IdentityDigest(evidence[f.a])
	requireCurationOK(t, curation.SaveWorkState(ctx, db, curation.WorkState{
		Subject: "release", TargetID: f.a, Status: "matched", RuleVersion: program.StoredRuleVersion,
		InputDigest: program.ReleaseDigest(evidence[f.a]), IdentityDigest: digest,
	}))
	tx, err := db.BeginTx(ctx, nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.SaveArtistCreditProposals(ctx, tx, f.a, digest, []curation.ArtistCreditProposal{{
		Local:    curation.ProposalLocalCredit{ArtistID: local.ArtistID, Role: "release_artist", Position: 1, Name: local.Name},
		External: curation.ProposalExternalCredit{Provider: "musicbrainz", ArtistID: externalArtist, CreditName: "曲库艺人的外部署名"},
		Basis:    curation.ProposalCreditBasis{Level: "release", ExternalID: externalRelease, Rule: "合成发行结构"},
	}}))
	requireCurationOK(t, tx.Commit())
	id := programString(t, f.application, `SELECT id FROM curation_artist_credit_proposals WHERE release_id=$1`, f.a)
	f.next(t, false).tool(t, "submit_verification", map[string]string{"item_id": id, "verdict": "same", "reason": "核对发行同位署名"})
	review := f.next(t, true)
	if !strings.Contains(string(review.Messages[len(review.Messages)-1].Content), "external_credit") {
		t.Fatalf("审查缺少外部署名佐证：%s", review.Messages[len(review.Messages)-1].Content)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist'`)
	review.review(t, "approved")
	modelWaitResolution(t, f, id, "model_verified")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='artist'
		AND object_id=$1 AND external_id=$2 AND method='external_credit'`, local.ArtistID, externalArtist)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_artist_credit_proposals WHERE id=$1 AND status='accepted'`, id)
}

func TestPostgreSQL整理艺人联网无结果后程序队列排空(t *testing.T) {
	f := newArtistWebSearchFixture(t, "", true, false, nil)
	f.configureMock(t)
	artist := f.addArtist(t, "合成无外部对应艺人")
	f.enable(t)
	f.clock.advance(t, time.Minute)
	item := f.artistItem(t, artist)
	conversation := modelConversation(t, f.curationFixture)
	claims, err := f.writer.ClaimModelKind(t.Context(), conversation, f.clock.options().Now(), 1, []string{"artist"}, "web", "")
	requireCurationOK(t, err)
	if len(claims) != 1 || claims[0].ID != item.ID {
		t.Fatalf("艺人项未领取：%+v", claims)
	}
	requireCurationOK(t, f.writer.FinishModelItem(t.Context(), claims[0], conversation, "no_result"))
	f.waitArtistResolution(t, item.ID, "no_result")
	// 经过完整盘点窗口，保证重新检查该艺人的队列资格。
	f.clock.advance(t, 6*time.Hour)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE artist_id=$1`, artist)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_work_states WHERE subject='artist_media' AND target_id=$1 AND needs_work`, artist)
}

func TestPostgreSQL整理网页明确区分艺人时不能合并(t *testing.T) {
	const sentence = "甲乐团和乙乐团是两个不同的艺人，分别在两座城市活动。"
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/searxng/search":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]string{{
				"title": "合成艺人目录", "url": "http://" + r.Host + "/fixture/artists", "content": sentence,
			}}})
		case "/fixture/artists":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<!doctype html><html><body><p>%s</p></body></html>", sentence)
		default:
			return false
		}
		return true
	})
	f.addRelease(t, "甲乐团")
	f.addRelease(t, "乙乐团")
	mustExec(t, f.application, `UPDATE releases SET title='乙乐团的不同专辑' WHERE artist='乙乐团'`)
	publishSearch(t, f.application)
	first, second := f.artistID(t, "甲乐团"), f.artistID(t, "乙乐团")
	release := programString(t, f.application, `SELECT id FROM releases WHERE artist='甲乐团'`)
	item := queueModelVerification(t, f.schedulerFixture, "verify_artist_merge", curation.VerificationCandidate{
		ReleaseID: release, Field: "artist_merge", Before: "甲乐团", After: "乙乐团", ArtistA: first, ArtistB: second,
	})
	f.enable(t)
	input := curation.VerificationInput{ItemID: item, Verdict: "same", Reason: "模型认为是同一身份"}
	f.next(t, false).tool(t, "submit_verification", input)
	f.next(t, false).tool(t, "web_search", map[string]string{"q": "甲乐团 乙乐团"})
	input.Citation = &curation.VerificationCitation{URL: f.endpoint + "/fixture/artists", Quote: sentence}
	f.next(t, false).tool(t, "submit_verification", input)
	schedulerWait(t, "网页求证后结项", func() bool {
		select {
		case review := <-f.requests:
			if review.Model != "test-review" {
				t.Fatalf("引用提交后收到额外主模型请求：%s", review.Model)
			}
			review.review(t, "approved")
		default:
		}
		return f.item(t, item).Status == "resolved"
	})
	if count := countRows(t, f.application, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`); count != 0 {
		t.Fatalf("网页明确区分两个艺人，仍写入了 %d 笔合并", count)
	}
}

func TestPostgreSQL整理观察建议的改动字段保持数组(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	mustExec(t, f.application, `UPDATE releases SET title='海岸：来信' WHERE id=$1`, f.a)
	item := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{
		ReleaseID: f.a, Field: "title", Before: "海岸：来信", After: "海岸 来信",
	})
	f.enable(t)
	f.next(t, false).tool(t, "submit_verification", map[string]string{
		"item_id": item, "verdict": "style", "reason": "保留原有标点",
	})
	modelWaitResolution(t, f, item, "suggestion_recorded")
	var page struct {
		Items []struct {
			Tool      string
			CanAccept bool `json:"can_accept"`
			Preview   struct{ Changes any }
		}
	}
	getJSON(t, f.application, &f.admin, curationBase+"/exceptions", &page)
	if len(page.Items) != 1 || page.Items[0].Tool != "observation" || page.Items[0].CanAccept {
		t.Fatalf("观察建议 %+v", page.Items)
	}
	if changes, ok := page.Items[0].Preview.Changes.([]any); !ok || len(changes) != 0 {
		t.Fatalf("观察建议的 preview.changes 需要空数组，得到 %#v", page.Items[0].Preview.Changes)
	}
}

func TestPostgreSQL整理第二档按本地艺人打包(t *testing.T) {
	f := newSchedulerFixture(t)
	releaseIDs := []string{f.a}
	for _, spec := range []groupedRelease{
		{title: "同艺人另一张", artist: "Artist A", tracks: []string{"present"}},
		{title: "另一艺人的发行", artist: "Artist B", tracks: []string{"present"}},
	} {
		release, _ := insertGroupedRelease(t, f.application, f.rootID, spec)
		releaseIDs = append(releaseIDs, release)
	}
	publishSearch(t, f.application)
	for _, id := range releaseIDs {
		modelRouteFixture(t, f, f.pending(t, id), "lookup")
	}
	conversation := modelConversation(t, f.curationFixture)
	items, err := f.writer.ClaimModelKind(t.Context(), conversation, f.clock.options().Now(), 5, []string{"release"}, "lookup", f.a)
	requireCurationOK(t, err)
	if len(items) != 2 || items[0].TargetID != f.a || items[1].TargetID != releaseIDs[1] {
		t.Fatalf("第二档打包 %+v", items)
	}
}

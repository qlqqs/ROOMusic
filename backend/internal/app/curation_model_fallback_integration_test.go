package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
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
func (*testLookupGate) PreparationContext(ctx context.Context) context.Context { return ctx }

func modelRouteFixture(t *testing.T, f *schedulerFixture, item curation.PendingItem, route string) curation.PendingItem {
	t.Helper()
	app := f.application
	evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{item.TargetID}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	digest := curation.ReleaseIdentityDigest(evidence[item.TargetID])
	requireCurationOK(t, curation.SaveWorkState(t.Context(), app.database, curation.WorkState{
		Subject: "release", TargetID: item.TargetID, Status: "conflict",
		RuleVersion: program.StoredRuleVersion, MatchRuleVersion: program.MatchRuleVersion, LocalRuleVersion: program.LocalRuleVersion,
		InputDigest: item.InputDigest, IdentityDigest: digest, FileDigest: evidence[item.TargetID].FileDigest,
		GapDigest: program.GapDigest(evidence[item.TargetID]), Result: []byte(`{}`),
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
	runMigration("0101_curation_model_rules.down.sql")
	runMigration("0091_curation_model_fallback.down.sql")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE id=$1 AND status='waiting_human'`, item.ID)
	runMigration("0091_curation_model_fallback.sql")
	runMigration("0101_curation_model_rules.sql")
	modelWaitResolution(t, f, item.ID, "suggestion_recorded")
	suggestion, err := f.writer.Exception(ctx, id)
	requireCurationOK(t, err)
	if suggestion.Status != "open" || !suggestion.CanAccept || suggestion.ReasonCode != "review_uncertain" || suggestion.Review.ID == "" {
		t.Fatalf("迁移后的建议 %+v", suggestion)
	}
	if p := f.progress(t); p.Items.WaitingHuman != 0 {
		t.Fatalf("迁移后仍有人工待办 %+v", p.Items)
	}
	requireAPIError(t, f.decide(t, &f.admin, id, "accept"), 404, "not_found")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_effective WHERE id=$1 AND edition='旧建议的版本'`, f.a)
}

func TestPostgreSQL整理第二档查到候选外编号直接写入(t *testing.T) {
	s := liveGapsEditions(5)
	s.External.Releases = s.External.Releases[:1]
	correct := s.External.Releases[0].ID
	f, clock := newJudgeProgramFixture(t, liveGapsSource(t, s))
	target, tracks := liveGapsLocal(t, f, s, s.Local.Album)
	wrong := uuid.New()
	p := judgeFixtureItem(t, f, target, program.Candidate{ExternalID: wrong, Method: "search"})
	judgeFixtureAnswer(t, f, p, curation.JudgeAnswer{Groups: []curation.JudgeGroup{{Name: curation.JudgeName{Release: s.Local.Album, Artist: s.Local.Artist}}}})
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, target)
	clock.advance(t, 0)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, target, correct)
	b3RequireCount(t, f.application, len(tracks), `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=ANY($1::uuid[])`, tracks)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
}

func TestPostgreSQL整理相同曲数的时长反证不能写入(t *testing.T) {
	for _, reason := range []string{"duration_mismatch", "tag_text_mismatch"} {
		t.Run(reason, func(t *testing.T) {
			s := liveGapsEditions(5)
			s.External.Releases = s.External.Releases[:1]
			f, clock := newJudgeProgramFixture(t, liveGapsSource(t, s))
			target, tracks := liveGapsLocal(t, f, s, s.Local.Album)
			if reason == "duration_mismatch" {
				mustExec(t, f.application, `UPDATE tracks SET duration_seconds=85 WHERE id=ANY($1::uuid[])`, tracks)
			} else {
				mustExec(t, f.application, `UPDATE releases SET title='另一个专辑',artist='另一个艺人',album_artist='另一个艺人' WHERE id=$1`, target)
				mustExec(t, f.application, `UPDATE tracks SET artist='另一个艺人',title='其他曲目 '||position WHERE id=ANY($1::uuid[])`, tracks)
			}
			remote := s.External.Releases[0].ID
			method := "search"
			if reason == "tag_text_mismatch" {
				method = "tag_id"
				programTag(t, f.application, tracks[0], "mb_release", remote)
			}
			p := judgeFixtureItem(t, f, target, program.Candidate{ExternalID: remote, Method: method})
			judgeFixtureAnswer(t, f, p, curation.JudgeAnswer{SelectedID: remote})
			clock.advance(t, 0)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1 OR object_id=ANY($2::uuid[])`, target, tracks)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, p.ID)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='conflict' AND NOT result ? 'judge'`, target)
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

func (q schedulerRequest) answerItems(t *testing.T, rows ...any) {
	t.Helper()
	if len(q.Tools) != 0 {
		t.Fatalf("结构化判断请求附带工具 %+v", q.Tools)
	}
	raw, err := json.Marshal(map[string]any{"items": rows})
	requireCurationOK(t, err)
	q.reply <- q.frame(t, map[string]string{"content": string(raw)}, "stop")
}

func configureModelWeb(t *testing.T, f *schedulerFixture) {
	t.Helper()
	f.update(t, "PUT", stewardBase+"/settings", map[string]any{"search_source": "searxng", "searxng_url": f.endpoint + "/searxng"})
}

func TestPostgreSQL整理无佐证主张先联网再留建议(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	item := f.pending(t, f.a)
	f.enable(t)
	q := f.next(t, false)
	q.undetermined(t, item.ID, "本地和候选资料均无佐证")
	modelWaitResolution(t, f, item.ID, "undetermined")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_tool_calls WHERE tool_name IN ('web_search','fetch_page')`)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
	requireCurationRuleRequestCount(t, f, 1)
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
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	item := f.pending(t, f.a)
	f.enable(t)
	f.next(t, false).answerItems(t, map[string]any{"item_id": item.ID, "decision": "undetermined", "reason": "记忆中的厂牌无法在本地核实", "fact": map[string]string{"field": "label", "value": "合成厂牌"}})
	schedulerWait(t, "答复处理完成", func() bool { return f.item(t, item.ID).Status != "in_progress" })
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
	requireCurationRuleRequestCount(t, f, 1)
}

func TestPostgreSQL整理联网额度与单项额度(t *testing.T) {
	f := newControlledArtistWebSearch(t, nil)
	artist := f.addArtist(t, "共享日额的艺人")
	item := f.startArtist(t, artist)
	q := f.next(t, false)
	f.update(t, "PUT", curationBase+"/settings", map[string]any{"enabled": true, "daily_token_limit": 20, "model_concurrency": 1})
	offline := f.pending(t, f.a)
	q.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "没有可靠身份候选"})
	f.waitArtistResolution(t, item.ID, "no_result")
	p := f.progress(t)
	if p.State != "limit_reached" || p.Today.InputTokens+p.Today.OutputTokens != 20 || f.item(t, offline.ID).Status != "waiting_model" {
		t.Fatalf("艺人已用额度后整理仍应留队：%+v", p)
	}
	status := f.status(t, artist, "none", 0)
	if status.Usage.TokenLimit != 50000 || status.Usage.ToolCallLimit != 10 {
		t.Fatalf("艺人单项额度 %+v", status.Usage)
	}
}

func TestPostgreSQL整理专辑名字只纠正有佐证的错字(t *testing.T) {
	for _, tc := range []struct {
		name, local, remote, verdict string
	}{
		{"错字", "合成正试专辑", "合成正式专辑", "typo"},
		{"繁简", "合成正式專輯", "合成正式专辑", ""},
		{"大小写", "COAST LETTERS", "Coast Letters", ""},
		{"标点", "海岸：来信", "海岸 来信", ""},
		{"外部特殊写法", "海岸的来信", "海岸来信", "keep"},
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
			target, tracks := b3ReleaseTracks(t, f.application, f.rootID, tc.local, "甲艺人", 2)
			mustExec(t, f.application, `UPDATE tracks SET duration_seconds=CASE position WHEN 1 THEN 12 ELSE 16 END,
				title=CASE position WHEN 1 THEN '第一曲' ELSE '第二曲' END WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, target)
			f.enable(t)
			link := curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(target, "", curationCLink("release", target, remote.ID), curationCLink("track", tracks[0], remote.Recordings[0]), curationCLink("track", tracks[1], remote.Recordings[1])))
			evidence, err := f.writer.LoadCurationEvidence(t.Context(), []string{target})
			requireCurationOK(t, err)
			tx, err := f.application.database.BeginTx(t.Context(), nil)
			requireCurationOK(t, err)
			defer tx.Rollback()
			requireCurationOK(t, curation.QueueVerification(t.Context(), tx, "verify_name", program.IdentityDigest(evidence[target]), curation.VerificationCandidate{ReleaseID: target, Field: "title", Before: tc.local, After: tc.remote, Structure: true, ExternalID: remote.ID, ExternalValue: tc.remote, OperationID: link.OperationID}))
			requireCurationOK(t, tx.Commit())
			f.configure(t, &f.admin)
			if tc.verdict != "" {
				verify := programString(t, f.application, `SELECT id FROM curation_pending_items WHERE target_id=$1 AND kind='verify_name'`, target)
				f.next(t, false).answerItems(t, curation.VerificationInput{ItemID: verify, Verdict: tc.verdict, Reason: "核对来源的标题和曲目"})
				if tc.verdict == "typo" {
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
					modelWaitResolution(t, f, verify, "undetermined")
					b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, verify)
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
	f.next(t, false).answerItems(t, curation.VerificationInput{ItemID: artist, Verdict: "same", Reason: "模型猜测是别名"})
	modelWaitResolution(t, f, artist, "undetermined")
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, artist)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`)

	item := modelPending(t, f.curationFixture, f.a, "release_merge", "假定的发行对应")
	raw, err := json.Marshal([]curation.MergeCandidate{{ReleaseID: f.b, Title: "Album B", DiscCount: 1, TrackCount: 1}})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, item.ID, raw)
	f.next(t, false).answerItems(t, map[string]string{"item_id": item.ID, "verdict": "merge", "reason": "模型猜测发行相同"})
	schedulerWait(t, "程序拒绝无佐证合并", func() bool {
		return countRows(t, f.application, `SELECT count(*) FROM steward_tool_calls WHERE tool_name='merge_releases' AND status='failed'`) > 0
	})
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
	external := uuid.New()
	f.enable(t)
	curationCApplyLinks(t, f.curationFixture, artistLinkRequest(release, seed.id(1), external))
	item := queueModelVerification(t, f, "verify_artist_merge", curation.VerificationCandidate{
		ExternalID: external, ReleaseID: release, Field: "artist_merge", Before: "甲乐团", After: "旧称乐团", ArtistA: seed.id(1), ArtistB: seed.id(2),
	})
	f.enable(t)
	f.next(t, false).answerItems(t, curation.VerificationInput{ItemID: item, Verdict: "same", Reason: "同一发行相邻曲目的同位署名"})
	modelWaitResolution(t, f, item, "model_verified")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE operation_type='artist_merge' AND origin->>'review_id' IS NULL`)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='artist' AND external_id=$1`, external)
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
	f.next(t, false).answerItems(t, curation.VerificationInput{ItemID: id, Verdict: "same", Reason: "核对发行同位署名"})
	modelWaitResolution(t, f, id, "model_verified")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='artist'
		AND object_id=$1 AND external_id=$2 AND method='model_identity'`, local.ArtistID, externalArtist)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_artist_credit_proposals WHERE id=$1 AND status='accepted'`, id)
}

func TestPostgreSQLArtistWebSearchNoResultDoesNotRestart(t *testing.T) {
	f := newArtistWebSearchFixture(t, "", true, false, nil)
	f.configureMock(t)
	artist := f.addArtist(t, "合成无外部对应艺人")
	f.enable(t)
	f.clock.advance(t, time.Minute)
	item := f.startArtist(t, artist)
	conversation := modelConversation(t, f.curationFixture)
	claims, err := f.writer.ClaimModelKind(curation.WithArtistWebActor(t.Context(), f.admin.id), conversation, f.clock.options().Now(), 1, []string{"artist"}, "artist_web", "")
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
	f.next(t, false).answerItems(t, curation.VerificationInput{ItemID: item, Verdict: "different", Reason: sentence})
	modelWaitResolution(t, f.schedulerFixture, item, "different")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_artist_negative_pairs WHERE reason=$1`, sentence)
	if count := countRows(t, f.application, `SELECT count(*) FROM operations WHERE operation_type='artist_merge'`); count != 0 {
		t.Fatalf("网页明确区分两个艺人，仍写入了 %d 笔合并", count)
	}
}

func TestPostgreSQL整理观察建议的改动字段保持数组(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	mustExec(t, f.application, `UPDATE releases SET title='海岸：来信' WHERE id=$1`, f.a)
	tx, err := f.application.database.BeginTx(t.Context(), nil)
	requireCurationOK(t, err)
	defer tx.Rollback()
	requireCurationOK(t, curation.QueueVerification(t.Context(), tx, "verify_name", modelDigestForTarget(t, f, f.a), curation.VerificationCandidate{ReleaseID: f.a, Field: "title", Before: "海岸：来信", After: "海岸 来信", Structure: true}))
	requireCurationOK(t, tx.Commit())
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='verify_name'`, f.a)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions`)
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
		modelRouteFixture(t, f, f.pending(t, id), "")
	}
	conversation := modelConversation(t, f.curationFixture)
	items, err := f.writer.ClaimModelKind(t.Context(), conversation, f.clock.options().Now(), 5, []string{"release"}, "select", "")
	requireCurationOK(t, err)
	if len(items) != len(releaseIDs) {
		t.Fatalf("识别批次缺少发行 %+v", items)
	}
	for _, p := range items {
		if p.Kind != "release_match" || !slices.Contains(releaseIDs, p.TargetID) {
			t.Fatalf("识别批次混入意外项目 %+v", p)
		}
	}
}

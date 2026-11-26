package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

// 条件就绪，出站限流、503 冷却以及队列优先级使用真实的来源策略。
type liveGapsLookup struct{ policy *sources.QueuePolicy }

func (l liveGapsLookup) LookupTierReady(context.Context, string, time.Time) (bool, error) {
	return l.policy.Pause("musicbrainz") == nil, nil
}
func (l liveGapsLookup) PreparationContext(ctx context.Context) context.Context {
	return l.policy.Context(ctx, sources.QueueModelLookup, nil)
}

func TestPostgreSQLLiveGapsSourceRetry(t *testing.T) {
	for _, tc := range []struct {
		route string
		limit bool
	}{{"lookup", false}, {"web", false}, {"lookup", true}, {"web", true}} {
		exit := "放弃"
		if tc.limit {
			exit = "触限"
		}
		t.Run(tc.route+"/"+exit, func(t *testing.T) {
			s := liveGapsEditions(5)
			s.External.Releases = s.External.Releases[:1]
			source := liveGapsSource(t, s)
			var healthy atomic.Bool
			var failures atomic.Int32
			var longRecovery atomic.Bool
			sourceClock := newProgramClock()
			gate := liveGapsLookup{sources.NewQueuePolicy(sourceClock.options().Now)}
			server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !healthy.Load() {
					failures.Add(1)
					// 第二档的 Retry-After 大于十分钟，能够识别用固定退避替代来源恢复时刻的错误。
					retry := "0"
					if tc.route == "lookup" {
						retry = "1200"
						if longRecovery.Load() {
							retry = "691200"
						}
					}
					w.Header().Set("Retry-After", retry)
					http.Error(w, "合成目录维护中", http.StatusServiceUnavailable)
					return
				}
				source.ServeHTTP(w, r)
			})
			f := newSchedulerFixtureWithSources(t, gate, server, func(o *options) { o.music.Now = sourceClock.options().Now })
			metricObserveClaims(t, f.application)
			target, _ := liveGapsLocal(t, f, s, s.Local.Album)
			p := modelRouteFixture(t, f, f.pending(t, target), tc.route)
			remote := s.External.Releases[0].ID
			mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, p.ID, remote)
			if tc.limit {
				// 首轮查询与放弃消耗两次工具调用、40 token，下一轮先取得本次来源错误，再触及上限。
				calls, tokens := 16, 0
				if tc.route == "web" {
					calls, tokens = 0, 300000-80
				}
				mustExec(t, f.application, `UPDATE curation_pending_items SET item_tool_calls=$2,item_tokens=$3 WHERE id=$1`, p.ID, calls, tokens)
			}
			f.configure(t, &f.admin)
			if tc.route == "web" {
				configureModelWeb(t, f)
			}
			before := b3WorkState(t, f.application, "release", target)
			f.enable(t)
			args := map[string]any{"item_id": p.ID, "external_id": remote, "basis": map[string]string{"note": "按曲序核对候选版本"}}
			rounds := 2
			if !tc.limit && tc.route == "lookup" {
				rounds = 1
			}
			for i := 0; i < rounds; i++ {
				q := f.next(t, false)
				start := f.item(t, p.ID)
				firstFailures := failures.Load()
				capped := tc.limit && i == 1
				if capped {
					longRecovery.Store(true)
				}
				if tc.route == "lookup" {
					q.tool(t, "get_music_metadata", map[string]string{"entity": "release", "mbid": remote})
				} else {
					q.tool(t, "resolve_release_match", args)
				}
				afterFailure := f.next(t, false)
				if got := afterFailure.lastToolResult(t); !strings.Contains(got, "source_unavailable") {
					t.Fatalf("模型应收到来源不可用，实际 %s", got)
				}
				if capped {
					afterFailure.tool(t, "get_music_metadata", map[string]string{"entity": "release", "mbid": remote})
				} else {
					afterFailure.tool(t, "shelve_pending_item", map[string]string{"item_id": p.ID, "reason": "来源暂时不可用"})
				}
				const used = 2
				schedulerWait(t, "本次领取已结束", func() bool { return f.item(t, p.ID).Status != "in_progress" })
				got := f.item(t, p.ID)
				after := b3WorkState(t, f.application, "release", target)
				delay := (5 * time.Second) << i
				requests := int32(5)
				if tc.route == "lookup" {
					delay, requests = 20*time.Minute, 1
				}
				if capped && tc.route == "lookup" {
					delay = 8 * 24 * time.Hour
				}
				deadline := f.clock.options().Now().Add(delay)
				if failures.Load()-firstFailures != requests || got.SourceRetryCount != i+1 || got.Status != "waiting_route" || got.Route != tc.route || got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(deadline) || got.Attempts != p.Attempts || got.Resolution != nil || after.Status != before.Status || after.Attempts != before.Attempts || after.Generation != before.Generation {
					t.Fatalf("第 %d 轮 %d 次来源请求，期望 %d 次、%s 后重试；待定项 %+v，发行 %+v", i+1, failures.Load()-firstFailures, requests, delay, got, after)
				}
				if got.ItemToolCalls != start.ItemToolCalls+used || got.ItemTokens != start.ItemTokens+int64(20*used) {
					t.Fatalf("本轮使用 %d 次工具，累计用量 %d/%d → %d/%d", used, start.ItemToolCalls, start.ItemTokens, got.ItemToolCalls, got.ItemTokens)
				}
				if retry, ok := metricSourceRetries(t, f.application, f.clock.options().Now())[p.ID]; !ok || retry.Route != tc.route {
					t.Fatalf("评分缺少来源等待记录 %+v", retry)
				}
				if _, ok := metricSourceRetries(t, f.application, deadline)[p.ID]; ok {
					t.Fatal("评分仍将到期项计为等待")
				}
				last := i+1 == rounds
				healthy.Store(last)
				sourceClock.now.Store(deadline.UnixNano())
				if capped {
					f.clock.now.Store(deadline.Add(-time.Second).UnixNano())
					select {
					case <-f.requests:
						t.Fatal("到期前收到新模型请求")
					case <-time.After(100 * time.Millisecond):
					}
					b3RequireCount(t, f.application, rounds, `SELECT count(*) FROM steward_conversations WHERE kind='curation'`)
					waiting := f.item(t, p.ID)
					if waiting.ItemToolCalls != got.ItemToolCalls || waiting.ItemTokens != got.ItemTokens {
						t.Fatal("到期领取前已清零用量")
					}
				}
				f.clock.now.Store(deadline.UnixNano())
				if !last {
					// 下一次领取应原样继承本轮累计用量。
					schedulerWait(t, "普通重试已领取", func() bool { return f.item(t, p.ID).Status == "in_progress" })
					next := f.item(t, p.ID)
					if next.ItemToolCalls != got.ItemToolCalls || next.ItemTokens != got.ItemTokens || next.SourceRetryCount != got.SourceRetryCount {
						t.Fatalf("普通重试丢失累计值 %+v", next)
					}
				}
			}
			resumed := f.next(t, false)
			if tc.limit {
				got := f.item(t, p.ID)
				if got.ItemToolCalls != 0 || got.ItemTokens != 0 || got.SourceRetryCount != 0 {
					t.Fatalf("满额后到期领取未清零 %+v", got)
				}
			}
			resumed.tool(t, "resolve_release_match", args)
			modelWaitResolution(t, f, p.ID, "model_matched")
			schedulerWait(t, "成功后清除来源退避", func() bool { return f.item(t, p.ID).SourceRetryCount == 0 })
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND object_type='release' AND external_id=$2`, target, remote)
		})
	}
}

func TestPostgreSQLLiveGapsTransientSource(t *testing.T) {
	s := liveGapsEditions(5)
	s.External.Releases = s.External.Releases[:1]
	remote := s.External.Releases[0].ID
	source := liveGapsSource(t, s)
	var reads atomic.Int32
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release/"+remote && reads.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "合成的短暂故障", http.StatusServiceUnavailable)
			return
		}
		source.ServeHTTP(w, r)
	})
	gate := &testLookupGate{}
	gate.ready.Store(true)
	f := newSchedulerFixtureWithSources(t, gate, server)
	target, _ := liveGapsLocal(t, f, s, s.Local.Album)
	item := modelRouteFixture(t, f, f.pending(t, target), "lookup")
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, item.ID, remote)
	f.configure(t, &f.admin)
	f.enable(t)
	q := f.next(t, false)
	conversation := f.item(t, item.ID).ConversationID
	q.tool(t, "get_music_metadata", map[string]string{"entity": "release", "mbid": remote})
	next := f.next(t, false)
	got := f.item(t, item.ID)
	if reads.Load() != 2 || !strings.Contains(next.lastToolResult(t), remote) || got.Status != "in_progress" || got.ConversationID != conversation || got.NextAttemptAt != nil || got.SourceRetryCount != 0 || got.ClaimSourceRecoveryAt != nil {
		t.Fatalf("请求内恢复后读了 %d 次，待定项 %+v", reads.Load(), got)
	}
	next.tool(t, "resolve_release_match", map[string]any{"item_id": item.ID, "external_id": remote, "basis": map[string]string{"note": "核对合成曲序"}})
	modelWaitResolution(t, f, item.ID, "model_matched")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, target, remote)
}

func TestPostgreSQLLiveGapsWebSuggestionRetry(t *testing.T) {
	clock := newProgramClock()
	f := newSchedulerFixture(t, func(o *options) { o.music.Now = clock.options().Now })
	title := programString(t, f.application, `SELECT title FROM releases WHERE id=$1`, f.a)
	item := queueModelVerification(t, f, "verify_name", curation.VerificationCandidate{ReleaseID: f.a, Field: "title", Before: title, After: "合成的另一种写法"})
	mustExec(t, f.application, `UPDATE curation_pending_items SET status='waiting_route',route='web' WHERE id=$1`, item)
	f.configure(t, &f.admin)
	configureModelWeb(t, f)
	f.enable(t)
	f.next(t, false).tool(t, "get_music_metadata", map[string]string{"entity": "release", "mbid": unavailableMusicBrainzID})
	q := f.next(t, false)
	if !strings.Contains(q.lastToolResult(t), "source_unavailable") {
		t.Fatal("该项没有收到来源错误")
	}
	proposal := map[string]string{"item_id": item, "verdict": "style", "reason": "保留原写法"}
	q.tool(t, "submit_verification", proposal)
	schedulerWait(t, "本次建议执行完毕", func() bool { return f.item(t, item).Status != "in_progress" })
	got := f.item(t, item)
	deadline := f.clock.options().Now().Add(5 * time.Second)
	if got.Status != "waiting_route" || got.Route != "web" || got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(deadline) || got.Attempts != 0 || got.Resolution != nil {
		t.Fatalf("来源故障后提交建议，待定项 %+v", got)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, f.a)
	// 到期后同一建议在新领取中可以正常结案。
	clock.now.Store(deadline.UnixNano())
	f.clock.now.Store(deadline.UnixNano())
	f.next(t, false).tool(t, "submit_verification", proposal)
	modelWaitResolution(t, f, item, "suggestion_recorded")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item)
}

func TestPostgreSQLLiveGapsUpgrade(t *testing.T) {
	t.Run("版本号", func(t *testing.T) {
		if program.MatchRuleVersion < 104 || program.LocalRuleVersion != 4 || runtime.ReviewPolicyVersion != 7 {
			t.Fatalf("匹配、本地、审查规则为 %d/%d/%d，匹配规则至少为 104，本地和审查规则应为 4/7", program.MatchRuleVersion, program.LocalRuleVersion, runtime.ReviewPolicyVersion)
		}
	})
	for _, kind := range []string{"unlisted", "uncertain", "track_count", "position_conflict", "artist_mismatch", "tag_text_mismatch", "matched"} {
		t.Run(kind, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			if kind == "matched" {
				f.tagged(t)
			}
			f.run(t)
			base := f.stateFor(t, "release", f.a)
			if kind == "matched" && base.Status != "matched" || kind != "matched" && base.Status != "unlisted" {
				t.Fatalf("初始夹具状态 %+v", base)
			}
			initialSearches := f.source.callCount("SearchReleases")
			initialReleaseReads := f.source.callCount("ReleaseByID")
			if kind != "matched" && initialSearches == 0 {
				t.Fatal("夹具首次识别没有出站，无法计量补查次数")
			}
			// 登记和消费可能发生在同一次排空；直接记录标记变为当前匹配规则版本的次数。
			mustExec(t, f.application, `CREATE TABLE gaps_requery_events (target_id uuid NOT NULL)`)
			mustExec(t, f.application, `CREATE FUNCTION record_gaps_requery() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN INSERT INTO gaps_requery_events VALUES(NEW.target_id); RETURN NEW; END $$`)
			mustExec(t, f.application, fmt.Sprintf(`CREATE TRIGGER gaps_requery AFTER UPDATE OF requery_match_version ON curation_work_states
				FOR EACH ROW WHEN (NEW.subject='release' AND NEW.requery_match_version=%d AND OLD.requery_match_version IS DISTINCT FROM NEW.requery_match_version)
				EXECUTE FUNCTION record_gaps_requery()`, program.MatchRuleVersion))
			status := kind
			if kind != "matched" && kind != "unlisted" {
				status = "uncertain"
			}
			// 历史未收录有正常的长周期复查期限。版本补查应在升级时独立执行。
			mustExec(t, f.application, `UPDATE curation_work_states SET match_rule_version=102,local_rule_version=2,status=$2,
				next_attempt_at=CASE WHEN $2='unlisted' THEN $3::timestamptz END WHERE subject='release' AND target_id=$1`, f.a, status, f.clock.options().Now().Add(60*24*time.Hour))
			if kind != "uncertain" && status == "uncertain" {
				candidates, err := json.Marshal([]program.Candidate{{ExternalID: uuid.New(), Title: "雾港候选", Source: "musicbrainz", Method: "search"}})
				requireCurationOK(t, err)
				conflicts, err := json.Marshal([]string{kind})
				requireCurationOK(t, err)
				requireCurationOK(t, curation.UpsertPendingItem(t.Context(), f.application.database, curation.PendingItem{
					TargetID: f.a, Kind: "release_match", RuleVersion: program.StoredRuleVersion, MatchRuleVersion: 102,
					InputDigest: base.InputDigest, IdentityDigest: base.IdentityDigest, Evidence: []byte(`{"method":"search"}`), Candidates: candidates, Conflicts: conflicts, Route: "lookup",
				}))
			}
			f.clock.advance(t, 6*time.Hour)
			// 允许一次投影轮登记补查，下一轮消费。
			f.clock.advance(t, 6*time.Hour)
			got := f.stateFor(t, "release", f.a)
			wantDelta := 1
			if kind == "matched" {
				wantDelta = 0
			}
			searches := f.source.callCount("SearchReleases")
			if kind != "matched" && (got.MatchRuleVersion != program.MatchRuleVersion || got.LocalRuleVersion != program.LocalRuleVersion) {
				t.Errorf("补查后的匹配和本地规则版本为 %d/%d", got.MatchRuleVersion, got.LocalRuleVersion)
			}
			if got.RequeryMatchVersion != 0 || got.Generation != base.Generation+wantDelta || got.Attempts != base.Attempts+wantDelta || searches-initialSearches != initialSearches*wantDelta {
				t.Errorf("升级后版本 %d/%d 补查标记 %d；代次 %d→%d 尝试 %d→%d，搜索 %d→%d；应补查 %d 轮", got.MatchRuleVersion, got.LocalRuleVersion, got.RequeryMatchVersion, base.Generation, got.Generation, base.Attempts, got.Attempts, initialSearches, searches, wantDelta)
			}
			f.clock.advance(t, 6*time.Hour)
			stable := f.stateFor(t, "release", f.a)
			b3RequireCount(t, f.application, wantDelta, `SELECT count(*) FROM gaps_requery_events WHERE target_id=$1`, f.a)
			if kind == "matched" && (stable.Status != "matched" || f.source.callCount("ReleaseByID") != initialReleaseReads) {
				t.Errorf("已匹配发行应保留结论且不重查对应，结论 %s，按编号读取 %d→%d", stable.Status, initialReleaseReads, f.source.callCount("ReleaseByID"))
			}
			if stable.Attempts != got.Attempts || stable.Generation != got.Generation || f.source.callCount("SearchReleases") != searches {
				t.Fatal("下一轮重复补查")
			}
		})
	}
}

func TestPostgreSQLLiveGapsLookupWithoutDecision(t *testing.T) {
	for _, tc := range []struct {
		name string
		web  bool
	}{
		{"可联网时同一项继续识别", true},
		{"未配置联网时记为未收录", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := liveGapsEditions(5)
			s.External.Releases = s.External.Releases[:1]
			gate := &testLookupGate{}
			gate.ready.Store(true)
			f := newSchedulerFixtureWithSources(t, gate, liveGapsSource(t, s))
			target, _ := liveGapsLocal(t, f, s, s.Local.Album)
			p := modelRouteFixture(t, f, f.pending(t, target), "lookup")
			f.configure(t, &f.admin)
			if tc.web {
				configureModelWeb(t, f)
			}
			f.enable(t)
			for attempt := 0; attempt < 3; attempt++ {
				q := f.next(t, false)
				item := f.item(t, p.ID)
				if item.Status != "in_progress" || item.Route != "lookup" || int(item.Attempts) != attempt {
					t.Fatalf("第二档第 %d 次领取状态 %s/%s，尝试 %d", attempt+1, item.Status, item.Route, item.Attempts)
				}
				q.complete(t)
			}
			schedulerWait(t, "第二档第三次无决定后放弃本档", func() bool {
				item := f.item(t, p.ID)
				return item.Route == "web" || item.Status == "resolved" || item.Status == "shelved"
			})
			item := f.item(t, p.ID)
			if tc.web {
				if item.Route != "web" || item.ShelvedReason != nil {
					t.Fatalf("第二档三次无决定后应转联网，实际 %s/%s，尝试 %d", item.Status, item.Route, item.Attempts)
				}
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND status='unmatched'`, target)
				// 联网档收到同一待定项后能接受新决定，并由后端实际读回版本明细定案。
				web := f.next(t, false)
				id := s.External.Releases[0].ID
				web.tool(t, "resolve_release_match", map[string]any{"item_id": p.ID, "external_id": id, "basis": map[string]string{"note": "联网档重新核对五首曲目的曲序与时长"}})
				modelWaitResolution(t, f, p.ID, "model_matched")
				b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, target, id)
				return
			}
			if item.Status != "resolved" || item.Resolution == nil || *item.Resolution != "unlisted" || item.ShelvedReason != nil {
				t.Fatalf("第二档三次无决定且无联网来源时应结案为未收录，实际 %s/%s，尝试 %d", item.Status, item.Route, item.Attempts)
			}
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND status='conflict' AND next_attempt_at IS NULL`, target)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, target)
		})
	}
}

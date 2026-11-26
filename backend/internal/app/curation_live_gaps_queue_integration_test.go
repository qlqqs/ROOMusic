package app

import (
	"context"
	"encoding/json"
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
func (l liveGapsLookup) LookupTierContext(ctx context.Context) context.Context {
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
			sourceClock := newProgramClock()
			gate := liveGapsLookup{sources.NewQueuePolicy(sourceClock.options().Now)}
			server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !healthy.Load() {
					failures.Add(1)
					// 第二档的 Retry-After 大于十分钟，能够识别用固定退避替代来源恢复时刻的错误。
					retry := "0"
					if tc.route == "lookup" {
						retry = "1200"
					}
					w.Header().Set("Retry-After", retry)
					http.Error(w, "合成目录维护中", http.StatusServiceUnavailable)
					return
				}
				source.ServeHTTP(w, r)
			})
			f := newSchedulerFixtureWithSources(t, gate, server, func(o *options) { o.music.Now = sourceClock.options().Now })
			target, _ := liveGapsLocal(t, f, s, s.Local.Album)
			p := modelRouteFixture(t, f, f.pending(t, target), tc.route)
			remote := s.External.Releases[0].ID
			mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, p.ID, remote)
			if tc.limit {
				// 第一次真实工具失败；第二次调用才越过上限。
				maxCalls := 20
				if tc.route == "web" {
					maxCalls = 30
				}
				mustExec(t, f.application, `UPDATE curation_pending_items SET item_tool_calls=$2 WHERE id=$1`, p.ID, maxCalls-1)
			}
			f.configure(t, &f.admin)
			if tc.route == "web" {
				configureModelWeb(t, f)
			}
			before := b3WorkState(t, f.application, "release", target)
			f.enable(t)
			q := f.next(t, false)
			args := map[string]any{"item_id": p.ID, "external_id": remote, "basis": map[string]string{"note": "按曲序核对候选版本"}}
			if tc.route == "lookup" {
				q.tool(t, "get_music_metadata", map[string]string{"entity": "release", "mbid": remote})
			} else {
				q.tool(t, "resolve_release_match", args)
			}
			afterFailure := f.next(t, false)
			if failures.Load() == 0 {
				t.Fatal("失败用例没有收到实际的 MusicBrainz 请求")
			}
			if got := afterFailure.lastToolResult(t); !strings.Contains(got, "source_unavailable") {
				t.Errorf("模型收到的工具结果未把来源不可用与查无结果区分：%s", got)
			}
			afterFailure.tool(t, "shelve_pending_item", map[string]string{"item_id": p.ID, "reason": "来源暂时不可用"})
			schedulerWait(t, "本次领取已结束", func() bool { v := f.item(t, p.ID); return v.Status != "in_progress" })
			got := f.item(t, p.ID)
			after := b3WorkState(t, f.application, "release", target)
			deadline := f.clock.options().Now().Add(10 * time.Minute)
			if tc.route == "lookup" {
				deadline = f.clock.options().Now().Add(20 * time.Minute)
			}
			if got.Status != "waiting_route" || got.Route != tc.route || got.NextAttemptAt == nil || got.NextAttemptAt.Before(deadline) || !got.NextAttemptAt.After(f.clock.options().Now()) || got.Attempts != p.Attempts || after.Status == "unlisted" || after.Attempts != before.Attempts || after.Generation != before.Generation {
				t.Fatalf("503 后待定项=%s/%s 下次=%v 尝试=%d；发行=%s 尝试 %d→%d 代次 %d→%d", got.Status, got.Route, got.NextAttemptAt, got.Attempts, after.Status, before.Attempts, after.Attempts, before.Generation, after.Generation)
			}
			healthy.Store(true)
			sourceClock.now.Store(got.NextAttemptAt.UnixNano())
			f.clock.now.Store(got.NextAttemptAt.UnixNano())
			resumed := f.next(t, false)
			resumed.tool(t, "resolve_release_match", args)
			modelWaitResolution(t, f, p.ID, "model_matched")
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND object_type='release' AND external_id=$2`, target, remote)
		})
	}
}

func TestPostgreSQLLiveGapsUpgrade(t *testing.T) {
	t.Run("版本号", func(t *testing.T) {
		if program.MatchRuleVersion != 103 || program.LocalRuleVersion != 3 || runtime.ReviewPolicyVersion != 7 {
			t.Fatalf("匹配、本地、审查规则为 %d/%d/%d，预期 103/3/7", program.MatchRuleVersion, program.LocalRuleVersion, runtime.ReviewPolicyVersion)
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
			// 登记和消费可能发生在同一次排空；直接记录标记从空变为 103 的次数。
			mustExec(t, f.application, `CREATE TABLE gaps_requery_events (target_id uuid NOT NULL)`)
			mustExec(t, f.application, `CREATE FUNCTION record_gaps_requery() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN INSERT INTO gaps_requery_events VALUES(NEW.target_id); RETURN NEW; END $$`)
			mustExec(t, f.application, `CREATE TRIGGER gaps_requery AFTER UPDATE OF requery_match_version ON curation_work_states
				FOR EACH ROW WHEN (NEW.subject='release' AND NEW.requery_match_version=103 AND OLD.requery_match_version IS DISTINCT FROM NEW.requery_match_version)
				EXECUTE FUNCTION record_gaps_requery()`)
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
			if kind != "matched" && (got.MatchRuleVersion != 103 || got.LocalRuleVersion != 3) {
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
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND status='unlisted'`, target)
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
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND status='unlisted'`, target)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, target)
		})
	}
}

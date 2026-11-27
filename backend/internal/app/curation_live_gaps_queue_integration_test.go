package app

import (
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
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

// 只让指定版本的详情接口失败，以区分真实出站与冷却期间的本地等待。
func judgeRetryFixture(t *testing.T, failures int32, retryAfter string) (*schedulerFixture, *programClock, string, string, *atomic.Int32) {
	t.Helper()
	s := liveGapsEditions(5)
	s.External.Releases = s.External.Releases[:1]
	remote := s.External.Releases[0].ID
	source := liveGapsSource(t, s)
	var reads atomic.Int32
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release/"+remote && reads.Add(1) <= failures {
			w.Header().Set("Retry-After", retryAfter)
			http.Error(w, "合成目录维护中", http.StatusServiceUnavailable)
			return
		}
		source.ServeHTTP(w, r)
	})
	f, clock := newJudgeProgramFixture(t, server)
	target, _ := liveGapsLocal(t, f, s, s.Local.Album)
	p := judgeFixtureItem(t, f, target, program.Candidate{ExternalID: remote, Method: "search"})
	judgeFixtureAnswer(t, f, p, curation.JudgeAnswer{SelectedID: remote})
	return f, clock, target, remote, &reads
}

func TestPostgreSQLLiveGapsSourceRetry(t *testing.T) {
	for _, delay := range []time.Duration{20 * time.Minute, 8 * 24 * time.Hour} {
		t.Run(delay.String(), func(t *testing.T) {
			f, clock, target, remote, reads := judgeRetryFixture(t, 1, fmt.Sprint(int(delay.Seconds())))
			clock.advance(t, 0)
			deadline := clock.options().Now().Add(delay)
			before := b3WorkState(t, f.application, "release", target)
			if before.Status != "try_later" || before.RetryCount != 0 || before.NextAttemptAt == nil || !before.NextAttemptAt.Equal(deadline) || reads.Load() != 1 {
				t.Fatalf("暂缓至 %s，实际状态 %+v，出站 %d 次", deadline, before, reads.Load())
			}
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND result->'retry'->>'source'='musicbrainz' AND result->'judge'->>'next'='-1'`, target)
			clock.advance(t, delay-time.Second)
			if reads.Load() != 1 {
				t.Fatal("恢复前再次出站")
			}
			clock.advance(t, time.Second)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND external_id=$2`, target, remote)
			requireCurationRuleRequestCount(t, f, 1)
		})
	}
}

func TestPostgreSQLLiveGapsTransientSource(t *testing.T) {
	f, clock, target, remote, reads := judgeRetryFixture(t, 1, "0")
	clock.advance(t, 0)
	if reads.Load() != 2 {
		t.Fatalf("一次短重试后应成功，详情请求 %d 次", reads.Load())
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND external_id=$2`, target, remote)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='matched' AND retry_count=0 AND NOT result ? 'retry'`, target)
	requireCurationRuleRequestCount(t, f, 1)
}

func TestPostgreSQLLiveGapsWebSuggestionRetry(t *testing.T) {
	var healthy atomic.Bool
	f := newControlledArtistWebSearch(t, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/searxng/") || healthy.Load() {
			return false
		}
		w.Header().Set("Retry-After", "0")
		http.Error(w, "合成搜索服务临时故障", http.StatusServiceUnavailable)
		return true
	})
	artist := f.addArtist(t, "暂时无法查证的艺人")
	item := f.startArtist(t, artist)
	q := f.next(t, false)
	q.tool(t, "web_search", map[string]string{"q": "暂时无法查证的艺人"})
	next := f.next(t, false)
	if !strings.Contains(next.lastToolResult(t), "source_unavailable") {
		t.Fatalf("来源错误 %s", next.lastToolResult(t))
	}
	next.tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "查找服务不可用"})
	schedulerWait(t, "艺人待办按来源错误推迟", func() bool { return f.item(t, item.ID).NextAttemptAt != nil })
	got := f.item(t, item.ID)
	if got.SourceRetryCount != 1 || got.ClaimSourceRecoveryAt != nil || got.ItemToolCalls != 2 || got.Resolution != nil || got.Attempts != 0 {
		t.Fatalf("来源错误后待办 %+v", got)
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_model_exceptions WHERE pending_item_id=$1`, item.ID)
	healthy.Store(true)
	f.clock.now.Store(got.NextAttemptAt.UnixNano())
	f.next(t, false).tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "未找到证据"})
	f.waitArtistResolution(t, item.ID, "no_result")
}

func TestPostgreSQLLiveGapsUpgrade(t *testing.T) {
	t.Run("版本号", func(t *testing.T) {
		if program.MatchRuleVersion < 104 || program.LocalRuleVersion != 4 || runtime.ReviewPolicyVersion != 7 {
			t.Fatalf("匹配、本地、审查规则为 %d/%d/%d，匹配规则至少为 104，本地和审查规则应为 4/7", program.MatchRuleVersion, program.LocalRuleVersion, runtime.ReviewPolicyVersion)
		}
	})
	for _, kind := range []string{"unmatched", "conflict", "track_count", "position_conflict", "artist_mismatch", "tag_text_mismatch", "matched"} {
		t.Run(kind, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			if kind == "matched" {
				f.tagged(t)
			}
			f.run(t)
			base := f.stateFor(t, "release", f.a)
			if kind == "matched" && base.Status != "matched" || kind != "matched" && base.Status != "unmatched" {
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
			if kind != "matched" && kind != "unmatched" {
				status = "conflict"
			}
			// 历史未收录有正常的长周期复查期限。版本补查应在升级时独立执行。
			mustExec(t, f.application, `UPDATE curation_work_states SET match_rule_version=102,local_rule_version=2,status=$2,
				next_attempt_at=CASE WHEN $2='unmatched' THEN $3::timestamptz END WHERE subject='release' AND target_id=$1`, f.a, status, f.clock.options().Now().Add(60*24*time.Hour))
			if kind != "conflict" && status == "conflict" {
				candidates, err := json.Marshal([]program.Candidate{{ExternalID: uuid.New(), Title: "雾港候选", Source: "musicbrainz", Method: "search"}})
				requireCurationOK(t, err)
				conflicts, err := json.Marshal([]string{kind})
				requireCurationOK(t, err)
				requireCurationOK(t, curation.UpsertPendingItem(t.Context(), f.application.database, curation.PendingItem{
					TargetID: f.a, Kind: "release_match", RuleVersion: program.StoredRuleVersion, MatchRuleVersion: 102,
					InputDigest: base.InputDigest, IdentityDigest: base.IdentityDigest, Evidence: []byte(`{"method":"search"}`), Candidates: candidates, Conflicts: conflicts, Route: "select",
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
	for _, web := range []bool{false, true} {
		t.Run(fmt.Sprintf("网页来源开启=%t", web), func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.configure(t, &f.admin)
			if web {
				configureModelWeb(t, f)
			}
			p := modelRouteFixture(t, f, f.pending(t, f.a), "")
			f.enable(t)
			for _, delay := range []time.Duration{5 * time.Second, 10 * time.Second, 0} {
				f.next(t, false).complete(t)
				if delay > 0 {
					deadline := f.clock.options().Now().Add(delay)
					schedulerWait(t, "空答推迟重试", func() bool { return f.item(t, p.ID).NextAttemptAt != nil })
					if !f.item(t, p.ID).NextAttemptAt.Equal(deadline) {
						t.Fatalf("空答重试时间 %+v", f.item(t, p.ID))
					}
					f.clock.now.Store(deadline.UnixNano())
				}
			}
			schedulerWait(t, "三次空答后搁置", func() bool { return f.item(t, p.ID).Status == "shelved" })
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_work_states WHERE target_id=$1 AND status='conflict'`, f.a)
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_id=$1`, f.a)
			requireCurationRuleRequestCount(t, f, 3)
		})
	}
}

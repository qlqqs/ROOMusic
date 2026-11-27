package app

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
)

func TestPostgreSQL整理第一档连续限流只推迟缺详情的项(t *testing.T) {
	s := liveGapsEditions(5)
	s.External.Releases = s.External.Releases[:1]
	remote := s.External.Releases[0].ID
	source := liveGapsSource(t, s)
	var reads atomic.Int32
	f, clock := newJudgeProgramFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release/"+remote {
			n := reads.Add(1)
			if n <= 2 {
				w.Header().Set("Retry-After", fmt.Sprint(12*n))
				http.Error(w, "来源正在限流", 503)
				return
			}
		}
		source.ServeHTTP(w, r)
	}))
	target, _ := liveGapsLocal(t, f, s, s.Local.Album)
	p := judgeFixtureItem(t, f, target, program.Candidate{ExternalID: remote, Method: "search"})
	judgeFixtureAnswer(t, f, p, curation.JudgeAnswer{SelectedID: remote})
	clock.advance(t, 0)
	for i, d := range []time.Duration{12 * time.Second, 24 * time.Second} {
		deadline := clock.options().Now().Add(d)
		got := b3WorkState(t, f.application, "release", target)
		if got.Status != "try_later" || got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(deadline) || reads.Load() != int32(i+1) {
			t.Fatalf("第 %d 次来源恢复时间，实际 %+v，出站 %d 次", i+1, got, reads.Load())
		}
		if i == 0 {
			f.pause(t)
			other, _ := liveGapsLocal(t, f, s, "同批独立的发行")
			peer := judgeFixtureItem(t, f, other)
			f.update(t, "PUT", curationBase+"/settings", map[string]bool{"paused": false})
			f.next(t, false).undetermined(t, peer.ID, "本地证据不足")
			modelWaitResolution(t, f, peer.ID, "undetermined")
		}
		clock.advance(t, d)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_id=$1 AND external_id=$2`, target, remote)
	if reads.Load() != 3 {
		t.Fatalf("两次 503 与一次成功共三次，实际 %d", reads.Load())
	}
	requireCurationRuleRequestCount(t, f, 2)
}

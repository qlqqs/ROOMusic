package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPostgreSQL整理第一档连续限流只推迟缺详情的项(t *testing.T) {
	remote := b3NewRemoteRelease()
	var reads atomic.Int32
	started, proceed := make(chan struct{}), make(chan struct{})
	service := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release-group/"+remote.Group {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q,"title":"合成正式专辑","releases":[{"id":%q,"title":"合成正式专辑"}]}`, remote.Group, remote.ID)
			return
		}
		if r.URL.Path != "/mb/release/"+remote.ID {
			http.NotFound(w, r)
			return
		}
		n := reads.Add(1)
		if n == 1 {
			close(started)
			select {
			case <-proceed:
			case <-r.Context().Done():
				return
			case <-t.Context().Done():
				return
			}
		}
		if n <= 2 {
			http.Error(w, "来源正在限流", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		remote.writeMusicBrainz(w)
	})
	// 保留程序队列的真实来源策略，仅控制其时钟及唤醒点。
	clock := newProgramClock()
	f := newSchedulerFixtureWithSources(t, nil, service, WithCurationProgram(true, clock.options()))
	clock.wait(t)
	f.configure(t, &f.admin)
	target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "合成正式专辑", artist: "合成正式艺人", tracks: []string{"present", "present"},
	})
	blocked := f.pending(t, target)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=jsonb_build_array(jsonb_build_object('external_id',$2::text)) WHERE id=$1`, blocked.ID, remote.ID)

	// 两项同时等待第一档，一项需要取详情，另一项已有完整候选。
	readyTarget, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "合成正式专辑（同批版本）", artist: "合成正式艺人", tracks: []string{"present", "present"},
	})
	ready := f.pending(t, readyTarget)
	stored := sources.Release{ID: f.remote.ID, Title: "合成正式专辑", Complete: true, Media: []sources.Medium{{Position: 1, TrackCount: 2,
		Tracks: []sources.Track{{Position: 1, RecordingID: f.remote.Recordings[0]}, {Position: 2, RecordingID: f.remote.Recordings[1]}}}}}
	candidates, err := json.Marshal([]program.Candidate{{ExternalID: stored.ID, Release: &stored}})
	requireCurationOK(t, err)
	mustExec(t, f.application, `UPDATE curation_pending_items SET candidates=$2 WHERE id=$1`, ready.ID, candidates)

	// 记录插入本身，避免把先创建再删除空会话误判为没有创建。
	mustExec(t, f.application, `CREATE TABLE selection_conversation_creations(id uuid PRIMARY KEY)`)
	mustExec(t, f.application, `CREATE FUNCTION record_selection_conversation() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN INSERT INTO selection_conversation_creations VALUES(NEW.id); RETURN NEW; END $$`)
	mustExec(t, f.application, `CREATE TRIGGER record_selection_conversation AFTER INSERT ON steward_conversations
		FOR EACH ROW WHEN (NEW.kind='curation') EXECUTE FUNCTION record_selection_conversation()`)

	advance := func(d time.Duration) {
		clock.now.Add(int64(d))
		f.clock.now.Add(int64(d))
	}
	waitRetry := func(delay time.Duration, hits int32, conversations int) {
		t.Helper()
		deadline := clock.options().Now().Add(delay)
		schedulerWait(t, "第一档待定项按 MusicBrainz 的来源恢复时刻重试", func() bool {
			item := f.item(t, blocked.ID)
			return item.NextAttemptAt != nil && item.NextAttemptAt.Equal(deadline)
		})
		item := f.item(t, blocked.ID)
		if item.Status != "waiting_model" || item.Route != blocked.Route || item.Kind != "release_match" || item.Attempts != 0 || item.ConversationID != "" || item.ClaimedAt != nil || reads.Load() != hits {
			t.Fatalf("来源退避应保留初始状态，详情请求 %d，待定项 %+v", reads.Load(), item)
		}
		b3RequireCount(t, f.application, conversations, `SELECT count(*) FROM selection_conversation_creations`)
	}
	f.enable(t)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("未收到缺详情项的首次来源请求")
	}
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM selection_conversation_creations`)
	close(proceed)
	// 来源和模型时钟冻结。另一项在第一档完成落库，失败项的两秒期限仍在未来。
	peer := f.next(t, false)
	if !strings.Contains(string(peer.Messages[len(peer.Messages)-1].Content), ready.ID) || strings.Contains(string(peer.Messages[len(peer.Messages)-1].Content), blocked.ID) {
		t.Fatal("冷却期间发给模型的消息应只含已备齐候选的同批项")
	}
	peer.selectCandidate(t, ready.ID, f.remote.ID)
	f.next(t, true).review(t, "approved")
	modelWaitResolution(t, f, ready.ID, "model_matched")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, readyTarget, f.remote.ID)
	waitRetry(2*time.Second, 1, 1)
	advance(time.Second)
	waitRetry(time.Second, 1, 1)

	advance(time.Second)
	waitRetry(4*time.Second, 2, 1)
	advance(4 * time.Second)
	request := f.next(t, false)
	var content string
	requireCurationOK(t, json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &content))
	items, found, err := metricsPendingItems(content)
	requireCurationOK(t, err)
	if !found || len(items) != 1 || items[0].ID != blocked.ID || len(items[0].Candidates) != 1 || items[0].Candidates[0].ID != remote.ID {
		t.Fatalf("恢复后应使用原待定项和候选：%s", content)
	}
	request.selectCandidate(t, blocked.ID, remote.ID)
	f.next(t, true).review(t, "approved")
	modelWaitResolution(t, f, blocked.ID, "model_matched")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, target, remote.ID)
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM selection_conversation_creations`)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_conversations c WHERE c.kind='curation' AND NOT EXISTS (SELECT FROM steward_messages m WHERE m.conversation_id=c.id)`)
	if hits := reads.Load(); hits != 3 {
		t.Fatalf("前两次 503 和恢复后的 200 应共三次请求，实际 %d", hits)
	}
}

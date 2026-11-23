package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

const unavailableMusicBrainzID = "9634b40c-a111-493b-abae-015ce393854b"

const stewardBase = "/api/v1/me/steward"

type schedulerRequest struct {
	Model      string
	ToolChoice json.RawMessage `json:"tool_choice"`
	Messages   []struct {
		Role    string
		Content json.RawMessage
	}
	inputTokens int
	reply       chan string
}

func (q schedulerRequest) tool(t *testing.T, name string, input any) {
	t.Helper()
	args, err := json.Marshal(input)
	requireCurationOK(t, err)
	call := map[string]any{"index": 0, "id": uuid.New(), "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}
	q.reply <- q.frame(t, map[string]any{"tool_calls": []any{call}}, "tool_calls")
}

func (q schedulerRequest) frame(t *testing.T, delta any, reason string) string {
	t.Helper()
	tokens := q.inputTokens
	if tokens == 0 {
		tokens = 13
	}
	raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}},
		"usage": map[string]int{"prompt_tokens": tokens, "completion_tokens": 7}})
	requireCurationOK(t, err)
	return "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
}

func (q schedulerRequest) complete(t *testing.T) {
	t.Helper()
	q.reply <- q.frame(t, map[string]string{"content": "完成"}, "stop")
}

func (q schedulerRequest) review(t *testing.T, decision string) {
	t.Helper()
	var content string
	requireCurationOK(t, json.Unmarshal(q.Messages[len(q.Messages)-1].Content, &content))
	var evidence struct {
		Items []struct {
			ID string `json:"item_id"`
		}
	}
	requireCurationOK(t, json.Unmarshal([]byte(content), &evidence))
	if len(evidence.Items) != 1 || !uuid.Valid(evidence.Items[0].ID) {
		t.Fatalf("审查收到的方案项 %s", content)
	}
	q.tool(t, "submit_review", map[string]any{"items": []any{map[string]any{"item_id": evidence.Items[0].ID, "decision": decision, "risk": "low", "reasons": []string{"合成审查理由"}}}})
}

type schedulerFixture struct {
	*curationFixture
	clock    *programClock
	requests chan schedulerRequest
	endpoint string
	remote   b3RemoteRelease
}

func newSchedulerFixture(t *testing.T, opts ...Option) *schedulerFixture {
	t.Helper()
	f := &schedulerFixture{clock: newProgramClock(), requests: make(chan schedulerRequest), remote: b3NewRemoteRelease()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mb/release/"+unavailableMusicBrainzID {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "合成资料源故障", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/mb/release/"+f.remote.ID {
			w.Header().Set("Content-Type", "application/json")
			f.remote.writeMusicBrainz(w)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		q := schedulerRequest{reply: make(chan string, 1)}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		select {
		case f.requests <- q:
		case <-r.Context().Done():
			return
		case <-t.Context().Done():
			return
		}
		select {
		case wire := <-q.reply:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, wire)
		case <-r.Context().Done():
		case <-t.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	f.endpoint = server.URL
	src := func(path string) sources.Options {
		return sources.Options{BaseURL: server.URL + path, NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil)}
	}
	opts = append(opts, WithCurationModel(true, model.Options{Now: f.clock.options().Now, PollInterval: 20 * time.Millisecond}),
		WithCurationSources(src("/mb"), coverartarchive.Options{BaseURL: server.URL + "/covers", NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil), ManifestLimiter: sources.NewLimiter(0, nil, nil)}, src("/lyrics")))
	g := newGroupEnvironment(t, opts...)
	f.curationFixture = &curationFixture{&operationsEnvironment{g}, curation.NewWriter(g.application.database, g.application.dataDirectory, nil)}
	return f
}

func (f *schedulerFixture) configure(t *testing.T, u *integrationTestUser) {
	t.Helper()
	resp := f.application.request(t, "PUT", stewardBase+"/settings", map[string]string{"endpoint": f.endpoint, "protocol": "openai", "model": "test-main", "review_model": "test-review"}, u, nil)
	if resp.Code != 200 {
		t.Fatalf("配置模型 HTTP %d %s", resp.Code, resp.Body)
	}
}

func (f *schedulerFixture) next(t *testing.T, review bool) schedulerRequest {
	t.Helper()
	select {
	case q := <-f.requests:
		want := "test-main"
		if review {
			want = "test-review"
		}
		if q.Model != want || (len(q.ToolChoice) > 0) != review || len(q.Messages) == 0 {
			t.Fatalf("请求模型=%q，tool_choice=%s，期望 %q", q.Model, q.ToolChoice, want)
		}
		return q
	case <-time.After(5 * time.Second):
		t.Fatalf("未收到模型请求（审查=%t）", review)
		return schedulerRequest{}
	}
}

func (f *schedulerFixture) progress(t *testing.T) curation.ModelProgress {
	t.Helper()
	var body struct{ Model curation.ModelProgress }
	getJSON(t, f.application, &f.admin, curationBase+"/progress", &body)
	return body.Model
}

func (f *schedulerFixture) pending(t *testing.T, target string) curation.PendingItem {
	t.Helper()
	rows, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{target}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	return modelPending(t, f.curationFixture, target, "release_match", program.ReleaseDigest(rows[target]))
}

func (f *schedulerFixture) item(t *testing.T, id string) curation.PendingItem {
	t.Helper()
	item, err := f.writer.PendingItem(t.Context(), id)
	requireCurationOK(t, err)
	return item
}

func schedulerWait(t *testing.T, description string, condition func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(end) {
			t.Fatalf("等待超时：%s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *schedulerFixture) pause(t *testing.T) {
	t.Helper()
	_, err := f.writer.SetPaused(t.Context(), f.admin.id, true)
	requireCurationOK(t, err)
}

func TestPostgreSQLCurationModelStartsCurationConversation(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	var ids []string
	for i := 0; i < 6; i++ {
		target, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: fmt.Sprintf("合成发行 %d", i), artist: "合成艺人", tracks: []string{"present"}})
		item := f.pending(t, target)
		ids = append(ids, item.ID)
	}
	f.enable(t)
	request := f.next(t, false)
	first := f.item(t, ids[0])
	store := runtime.NewStore(f.application.database)
	detail, err := store.GetConversation(t.Context(), f.admin.id, first.ConversationID)
	requireCurationOK(t, err)
	if c := detail.Conversation; c.Kind != "curation" || c.Mode != "steward" || c.UserID != f.admin.id || len(detail.Messages) != 1 || detail.Messages[0].Role != "user" {
		t.Fatalf("自动整理会话 %+v", detail)
	}
	for i, id := range ids {
		item := f.item(t, id)
		inBatch := i < 5
		if strings.Contains(string(detail.Messages[0].Content), id) != inBatch || (item.Status == "in_progress" && item.ConversationID == first.ConversationID) != inBatch {
			t.Fatalf("第 %d 项 %+v，首条消息 %s", i, item, detail.Messages[0].Content)
		}
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM steward_conversations`)
	f.pause(t)
	request.complete(t)
}

func TestPostgreSQLCurationModelShelveAndReopen(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	f.enable(t)
	f.next(t, false).tool(t, "shelve_pending_item", map[string]string{"item_id": item.ID, "reason": "候选缺少介质信息"})
	last := f.next(t, false)
	got := f.item(t, item.ID)
	if got.Status != "shelved" || got.ShelvedReason == nil || *got.ShelvedReason != "候选缺少介质信息" {
		t.Fatalf("搁置后 %+v", got)
	}
	f.pause(t)
	last.complete(t)
	requireCurationOK(t, curation.UpsertPendingItem(t.Context(), f.application.database, item))
	if got := f.item(t, item.ID); got.Status != "shelved" {
		t.Fatalf("证据相同后 %+v", got)
	}
	mustExec(t, f.application, `UPDATE releases SET barcode='123456789012' WHERE id=$1`, f.a)
	again := f.pending(t, f.a)
	if again.ID != item.ID || again.Status != "waiting_model" || again.ShelvedReason != nil || again.Attempts != 0 {
		t.Fatalf("证据变化后 %+v", again)
	}
}

func TestPostgreSQLCurationModelUnhandledThreeTimes(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	f.enable(t)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		q := f.next(t, false)
		got := f.item(t, item.ID)
		if int(got.Attempts) != i || got.Status != "in_progress" || seen[got.ConversationID] {
			t.Fatalf("第 %d 次领取 %+v", i+1, got)
		}
		seen[got.ConversationID] = true
		q.complete(t)
	}
	schedulerWait(t, "第三次未处理后搁置", func() bool { return f.item(t, item.ID).Status == "shelved" })
	got := f.item(t, item.ID)
	if got.Attempts != 3 || got.ShelvedReason == nil || *got.ShelvedReason != "unhandled" {
		t.Fatalf("三次未处理后 %+v", got)
	}
}
